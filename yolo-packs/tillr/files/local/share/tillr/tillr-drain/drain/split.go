package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// FileSplit receives a person-filed design statement. Automatic calls to the
// design seat and donor carving are separate from this entry point.
func FileSplit(ctx context.Context, root, path, parent string, input io.Reader, at time.Time) (map[string]string, error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return nil, Externalf("reading split statement: %v", err)
	}
	// encoding/json would silently replace invalid bytes and unpaired surrogates.
	if !utf8.Valid(raw) {
		return nil, Usagef("invalid split statement: input must be UTF-8")
	}
	if err := checkSplitJSONSurrogates(raw); err != nil {
		return nil, Usagef("invalid split statement: %v", err)
	}
	if err := uniqueJSON(raw); err != nil {
		return nil, Usagef("invalid split statement: %v", err)
	}
	var statement item.SplitStatement
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&statement); err != nil {
		return nil, Usagef("invalid split statement: %v", err)
	}
	if err := checkSplitJSONKeys(raw); err != nil {
		return nil, Usagef("invalid split statement: %v", err)
	}
	if err := item.RequestWorkflowCheck(root, item.RequestDefaultWorkflow["build"]); err != nil {
		return nil, err
	}
	s, err := store.Open(ctx, path, true)
	if err != nil {
		return nil, Incompatiblef("opening split store: %v", err)
	}
	defer s.Close()
	var head int64
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if w := works[parent]; w != nil && !w.Done {
			return item.Rejectionf("split-in-flight", parent, "settle the parent's current attempt before filing a split")
		}
		head, err = pendingHistoryHead(r)
		return err
	}); err != nil {
		return nil, err
	}
	var parts map[string]string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		// A loop start between the read and the write can claim the parent.
		changed, err := pendingHistoryChanged(tx.Reader(), head)
		if err != nil {
			return err
		}
		if changed {
			return item.Rejectionf("split-queue-changed", parent, "dispatch history changed while checking the parent; retry the split")
		}
		parts, err = item.FileSplit(ctx, tx, parent, statement, at)
		return err
	})
	if err != nil {
		return nil, err
	}
	return parts, nil
}

// checkSplitJSONSurrogates walks escapes before any JSON decoding can discard
// their original spelling. The JSON decoder still checks all syntax.
func checkSplitJSONSurrogates(raw []byte) error {
	codeUnit := func(b []byte) rune {
		if len(b) < 6 || b[0] != '\\' || b[1] != 'u' {
			return -1
		}
		u, err := strconv.ParseUint(string(b[2:6]), 16, 16)
		if err != nil {
			return -1
		}
		return rune(u)
	}
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		u := codeUnit(raw[i:])
		if !utf16.IsSurrogate(u) {
			i++ // Includes an escaped backslash: its following 'u' is literal.
			continue
		}
		v := codeUnit(raw[i+6:])
		if u >= 0xdc00 || v < 0xdc00 || v > 0xdfff {
			return fmt.Errorf("unpaired surrogate escape at byte %d", i+1)
		}
		i += 11 // Skip both six-byte escapes, including the low surrogate.
	}
	return nil
}

// checkSplitJSONKeys checks exact field spellings after the typed decode checked
// the shape. encoding/json accepts case aliases even with DisallowUnknownFields;
// map keys (criterion IDs and sibling labels) are not struct fields.
func checkSplitJSONKeys(raw []byte) error {
	var shape struct {
		Parts    []map[string]json.RawMessage            `json:"parts"`
		Coverage map[string][]map[string]json.RawMessage `json:"coverage"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	keys := func(fields map[string]json.RawMessage, allowed ...string) error {
		for key := range fields {
			if !slices.Contains(allowed, key) {
				return fmt.Errorf("unknown field %q", key)
			}
		}
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	if err := keys(top, "parts", "coverage"); err != nil {
		return err
	}
	for _, part := range shape.Parts {
		if err := keys(part, "label", "title", "share", "leaves", "criteria", "follows", "required"); err != nil {
			return err
		}
	}
	for _, entries := range shape.Coverage {
		for _, entry := range entries {
			if err := keys(entry, "clause", "part", "closing_check"); err != nil {
				return err
			}
		}
	}
	return nil
}

// splitWait describes a blocked parent without releasing it. Until there is a
// closing verifier, a parent with all required parts landed needs the operator.
func splitWait(row *WhyRow, blockers []string, states map[string]string, def definition.Definition) {
	row.ClassSource = "store"
	row.WaitingOn = []string{}
	for _, id := range blockers {
		if states[id] != StateLanded {
			row.WaitingOn = append(row.WaitingOn, id)
		}
	}
	if len(row.WaitingOn) == 0 {
		row.Class, row.Mover = "needs you", "check the parent's criteria by hand"
		row.Why = "required parts have landed; check the parent's criteria by hand"
		row.tableReason = row.Why
		return
	}
	row.Class, row.Why = "waits on work", "waiting for required parts to land"
	row.Mover = strings.Join(row.WaitingOn, ", ")
	for _, id := range row.WaitingOn {
		if finishedState(def, states[id]) {
			row.Class, row.Mover = "needs you", "owner; required part did not land: "+id
			row.Why = "required part did not land: " + id + " (" + states[id] + ")"
			return
		}
	}
}

func readItemTitles(r *store.Reader) (map[string]string, error) {
	out := map[string]string{}
	rows, err := r.Query(`SELECT item_id, title FROM items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out[id] = title
	}
	return out, rows.Err()
}

func formatSplitCounts(total, landed, working, held, queued, closed int) string {
	var parts []string
	if landed > 0 {
		parts = append(parts, fmt.Sprintf("%d landed", landed))
	}
	if working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", working))
	}
	if held > 0 {
		parts = append(parts, fmt.Sprintf("%d held", held))
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", queued))
	}
	if closed > 0 {
		parts = append(parts, fmt.Sprintf("%d closed", closed))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("split into %d parts", total)
	}
	return fmt.Sprintf("split into %d parts: %s", total, strings.Join(parts, ", "))
}

func decorateSplitParents(answer *WhyAnswer, splits map[string]item.SplitStatement, states map[string]string, def definition.Definition) {
	if len(splits) == 0 || len(answer.Rows) == 0 {
		return
	}
	rowByID := make(map[string]*WhyRow, len(answer.Rows))
	for i := range answer.Rows {
		rowByID[answer.Rows[i].ItemID] = &answer.Rows[i]
	}
	for i := range answer.Rows {
		row := &answer.Rows[i]
		st, ok := splits[row.ItemID]
		if !ok {
			continue
		}
		sp := &WhySplitParent{TotalParts: len(st.Parts)}
		var closedRequired []string
		var unlandedRequired []string
		for _, p := range st.Parts {
			partID := item.SplitPartID(row.ItemID, p.Label)
			req := p.Required != nil && *p.Required
			if req {
				sp.RequiredParts++
			}
			partState := states[partID]
			switch {
			case partState == StateLanded:
				sp.Landed++
				if req {
					sp.RequiredLanded++
				}
			case finishedState(def, partState):
				sp.Closed++
				if req {
					closedRequired = append(closedRequired, partID)
					unlandedRequired = append(unlandedRequired, partID)
				}
			default:
				if req {
					unlandedRequired = append(unlandedRequired, partID)
				}
				partRow := rowByID[partID]
				if partRow != nil && (partRow.Position == "running" || partRow.Class == "moving") {
					sp.Working++
				} else if partRow != nil && (partRow.Class == "needs you" || partRow.Class == "alarm" || partRow.Class == "needs a cause") {
					sp.Held++
					sp.HeldParts = append(sp.HeldParts, WhySplitHeldPart{
						ItemID: partID,
						Label:  p.Label,
						Reason: partRow.TableReason(),
						Mover:  partRow.Mover,
					})
				} else {
					sp.Queued++
				}
			}
		}
		sp.ProgressSummary = fmt.Sprintf("%d of %d required parts landed", sp.RequiredLanded, sp.RequiredParts)
		row.SplitParent = sp
		if finishedState(def, row.State) {
			continue
		}
		summary := formatSplitCounts(sp.TotalParts, sp.Landed, sp.Working, sp.Held, sp.Queued, sp.Closed)
		switch {
		case len(closedRequired) > 0:
			id := closedRequired[0]
			row.Class = "needs you"
			row.Mover = "owner; required part did not land: " + id
			row.Why = summary + "; required part did not land: " + id + " (" + states[id] + ")"
			row.tableReason = row.Why
		case sp.RequiredLanded == sp.RequiredParts:
			row.Class = "needs you"
			row.Mover = "operator; check the parent's criteria by hand"
			row.Why = summary + "; check the parent's criteria by hand"
			row.tableReason = row.Why
		case len(sp.HeldParts) > 0:
			heldDetails := make([]string, 0, len(sp.HeldParts))
			heldMovers := make([]string, 0, len(sp.HeldParts))
			for _, hp := range sp.HeldParts {
				heldDetails = append(heldDetails, fmt.Sprintf("%s (%s; mover: %s)", hp.Label, hp.Reason, hp.Mover))
				if hp.Mover != "" {
					heldMovers = append(heldMovers, hp.Label+": "+hp.Mover)
				}
			}
			row.Why = summary + "; held: " + strings.Join(heldDetails, ", ")
			row.tableReason = row.Why
			if len(heldMovers) > 0 {
				row.Mover = strings.Join(heldMovers, "; ")
			} else if len(unlandedRequired) > 0 {
				row.Mover = strings.Join(unlandedRequired, ", ")
			}
		default:
			row.Why = summary
			row.tableReason = summary
			if len(unlandedRequired) > 0 {
				row.Mover = strings.Join(unlandedRequired, ", ")
			}
		}
	}
}
