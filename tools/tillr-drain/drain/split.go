package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr-drain/internal/splitclosing"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const ReasonSplitCallDesign = "split-call-design"

const splitStatementFile = "split-statement.json"

func parseSplitStatement(raw []byte) (item.SplitStatement, error) {
	// encoding/json would silently replace invalid bytes and unpaired surrogates.
	if !utf8.Valid(raw) {
		return item.SplitStatement{}, Usagef("invalid split statement: input must be UTF-8")
	}
	if err := checkSplitJSONSurrogates(raw); err != nil {
		return item.SplitStatement{}, Usagef("invalid split statement: %v", err)
	}
	if err := uniqueJSON(raw); err != nil {
		return item.SplitStatement{}, Usagef("invalid split statement: %v", err)
	}
	var statement item.SplitStatement
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&statement); err != nil {
		return item.SplitStatement{}, Usagef("invalid split statement: %v", err)
	}
	if err := checkSplitJSONKeys(raw); err != nil {
		return item.SplitStatement{}, Usagef("invalid split statement: %v", err)
	}
	return statement, nil
}

// FileSplit receives a person-filed design statement. Automatic calls to the
// design seat and donor carving are separate from this entry point.
func FileSplit(ctx context.Context, root, path, parent string, input io.Reader, at time.Time) (map[string]string, error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return nil, Externalf("reading split statement: %v", err)
	}
	statement, err := parseSplitStatement(raw)
	if err != nil {
		return nil, err
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
	return splitclosing.CheckSplitJSONSurrogates(raw)
}

// checkSplitJSONKeys checks exact field spellings after the typed decode checked
// the shape. encoding/json accepts case aliases even with DisallowUnknownFields;
// map keys (criterion IDs and sibling labels) are not struct fields.
func checkSplitJSONKeys(raw []byte) error {
	var shape struct {
		Donor    map[string]json.RawMessage              `json:"donor"`
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
	if err := keys(top, "donor", "parts", "coverage"); err != nil {
		return err
	}
	if shape.Donor != nil {
		if err := keys(shape.Donor, "run_id", "ref", "sha", "base_sha"); err != nil {
			return err
		}
	}
	for _, part := range shape.Parts {
		if err := keys(part, "label", "title", "share", "leaves", "criteria", "follows", "required", "takes", "paths", "shared_files", "inherited_findings"); err != nil {
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

func splitClosingHoldPresentation(held *splitclosing.HeldPayload) (class, mover string) {
	switch {
	case held.Mover == "owner":
		return "needs you", "owner; " + held.Reason
	case held.Mover == "operator" || held.Mover == "" || held.Mover == "review" || held.Mover == "design":
		return "needs you", "operator; " + held.Reason
	default:
		return "waits on work", held.Mover
	}
}

// splitWait describes a blocked parent without releasing it, reading the current
// split-closing state when all required parts have landed.
func splitWait(r *store.Reader, row *WhyRow, parent string, blockers []string, states map[string]string, def definition.Definition) error {
	row.ClassSource = "store"
	row.WaitingOn = []string{}
	for _, id := range blockers {
		if states[id] != StateLanded {
			row.WaitingOn = append(row.WaitingOn, id)
		}
	}
	if len(row.WaitingOn) == 0 {
		st, err := splitclosing.ReadState(r, parent)
		if err != nil {
			return err
		}
		if st.Held != nil {
			row.Class, row.Mover = splitClosingHoldPresentation(st.Held)
			row.Why = st.Held.Reason
			row.tableReason = row.Why
			return nil
		}
		if st.Finding != nil {
			row.Class = "waits on work"
			row.Mover = "design"
			why := splitclosing.FormatFindingWhy(st.Finding)
			if st.Refused != nil && strings.TrimSpace(st.Refused.Reason) != "" {
				why += "; prior extension refused: " + st.Refused.Reason
			}
			row.Why = why
			row.tableReason = row.Why
			return nil
		}
		row.Class, row.Mover = "waits on work", "review"
		row.Why = "required parts have landed; waiting on the closing check"
		row.tableReason = row.Why
		return nil
	}
	row.Class, row.Why = "waits on work", "waiting for required parts to land"
	row.Mover = strings.Join(row.WaitingOn, ", ")
	for _, id := range row.WaitingOn {
		if finishedState(def, states[id]) {
			var closeReason string
			if err := r.QueryRow(`SELECT close_reason FROM items WHERE item_id=?`, id).Scan(&closeReason); err != nil && !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no such column: close_reason") {
				return err
			}
			if item.IsDefectiveClose(states[id], closeReason) {
				st, err := splitclosing.ReadState(r, parent)
				if err != nil {
					return err
				}
				if st.Held != nil {
					row.Class, row.Mover = splitClosingHoldPresentation(st.Held)
					row.Why = st.Held.Reason
					row.tableReason = row.Why
					return nil
				}
				row.Class, row.Mover = "waits on work", "design"
				row.Why = "required part closed as defective: " + id + "; waiting on design seat"
				if st.Refused != nil && strings.TrimSpace(st.Refused.Reason) != "" {
					row.Why += "; prior extension refused: " + st.Refused.Reason
				}
				row.tableReason = row.Why
				return nil
			}
			row.Class, row.Mover = "needs you", "owner; required part did not land: "+id
			row.Why = "required part did not land: " + id + " (" + states[id] + ")"
			return nil
		}
	}
	return nil
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

func readItemTitlesFor(r *store.Reader, itemIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		batch := itemIDs[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(`SELECT item_id, title FROM items WHERE item_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, title string
			if err := rows.Scan(&id, &title); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = title
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
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

func decorateSplitParents(answer *WhyAnswer, splits map[string]item.SplitStatement, states, closeReasons map[string]string, splitStates map[string]splitclosing.State, def definition.Definition) {
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
		sp := &WhySplitParent{}
		var closedRequired []string
		var defectiveRequired []string
		var unlandedRequired []string
		for _, p := range st.Parts {
			if len(p.ReplacedBy) > 0 {
				continue
			}
			sp.TotalParts++
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
					if item.IsDefectiveClose(partState, closeReasons[partID]) {
						defectiveRequired = append(defectiveRequired, partID)
					} else {
						closedRequired = append(closedRequired, partID)
					}
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
		case len(defectiveRequired) > 0:
			id := defectiveRequired[0]
			cst := splitStates[row.ItemID]
			if cst.Held != nil {
				row.Class, row.Mover = splitClosingHoldPresentation(cst.Held)
				row.Why = summary + "; " + cst.Held.Reason
			} else {
				row.Class = "waits on work"
				row.Mover = "design"
				row.Why = summary + "; required part closed as defective: " + id + "; waiting on design seat"
				if cst.Refused != nil && strings.TrimSpace(cst.Refused.Reason) != "" {
					row.Why += "; prior extension refused: " + cst.Refused.Reason
				}
			}
			row.tableReason = row.Why
		case sp.RequiredLanded == sp.RequiredParts:
			if cst := splitStates[row.ItemID]; cst.Held != nil {
				row.Class, row.Mover = splitClosingHoldPresentation(cst.Held)
				row.Why = summary + "; " + cst.Held.Reason
			} else if row.Why != "" {
				row.Why = summary + "; " + row.Why
			} else {
				row.Why = summary + "; waiting on the closing check"
			}
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

func splitCallWhyText(sc *item.SplitCall) string {
	if sc != nil && sc.Caller != "" {
		if sc.Caller == item.SplitCallerVerifier {
			return "split called by the verifier; waiting on the design seat"
		}
		return "split called by " + sc.Caller + "; waiting on the design seat"
	}
	return "split called by the verifier; waiting on the design seat"
}

func s1Signal(size candidateSize) item.SplitSignal {
	return item.SplitSignal{
		Signal:         item.SplitSignalS1,
		Files:          size.Files,
		AddedLines:     size.AddedLines,
		FileLimit:      size.FileLimit,
		AddedLineLimit: size.AddedLineLimit,
	}
}

func s1Holds(size candidateSize) bool {
	return size.Files > size.FileLimit || size.AddedLines > size.AddedLineLimit
}

// verifierSplitCall evaluates whether a RefusalVerifyNotDone should record a
// verifier split call on S1. It routes ONLY on measured signal S1, only on a
// filed request item, never on repeated findings, and never re-raises an S1
// reading already answered by a no-split without new evidence.
func (l *Loop) verifierSplitCall(ctx context.Context, candidate Candidate, size *candidateSize, repeatedFinding string, at time.Time) (*item.SplitCall, error) {
	if size == nil || repeatedFinding != "" || !s1Holds(*size) {
		return nil, nil
	}
	words, err := requestWordsFor(ctx, l.cfg.Store, candidate.ItemID)
	if err != nil || words == nil {
		return nil, err
	}
	sig := s1Signal(*size)
	var answered bool
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var err error
		answered, err = item.S1AnsweredNoSplit(r, candidate.ItemID, sig)
		return err
	}); err != nil {
		return nil, err
	}
	if answered {
		return nil, nil
	}
	return &item.SplitCall{
		Caller:    item.SplitCallerVerifier,
		Timestamp: at,
		ItemID:    candidate.ItemID,
		RunID:     candidate.RunID,
		DonorRef:  pinRef(candidate.RunID),
		DonorSHA:  candidate.HeadSHA,
		BaseSHA:   candidate.BaseSHA,
		Signals:   []item.SplitSignal{sig},
		Status:    item.SplitCallStatusPending,
	}, nil
}

func readSplitCallsForPlanner(r *store.Reader) (pending map[string]item.SplitCall, noSplit map[string]item.SplitCall, err error) {
	return readSplitCallsForPlannerForItems(r, nil)
}

func readSplitCallsForPlannerForItems(r *store.Reader, itemIDs []string) (pending map[string]item.SplitCall, noSplit map[string]item.SplitCall, err error) {
	pending = map[string]item.SplitCall{}
	noSplit = map[string]item.SplitCall{}
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='split_calls')`).Scan(&exists); err != nil {
		return nil, nil, err
	}
	if !exists {
		return pending, noSplit, nil
	}
	query := `SELECT call_id, item_id, caller, run_id, donor_ref, donor_sha, base_sha, signals, status, design_run_id, no_split_reason, last_rejection_reason, created_at, answered_at
		 FROM split_calls WHERE status IN (?,?)`
	args := []any{item.SplitCallStatusPending, item.SplitCallStatusNoSplit}
	if len(itemIDs) > 0 {
		query += ` AND item_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(itemIDs)), ",") + `)`
		for _, id := range itemIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY call_id ASC`
	rows, err := r.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c item.SplitCall
		var rawSignals []byte
		var noSplitReason, createdAt, answeredAt string
		if err := rows.Scan(
			&c.CallID, &c.ItemID, &c.Caller, &c.RunID, &c.DonorRef, &c.DonorSHA, &c.BaseSHA,
			&rawSignals, &c.Status, &c.DesignRunID, &noSplitReason, &c.LastRejectionReason,
			&createdAt, &answeredAt,
		); err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(rawSignals, &c.Signals); err != nil {
			return nil, nil, err
		}
		if createdAt != "" {
			t, err := time.Parse(time.RFC3339Nano, createdAt)
			if err != nil {
				return nil, nil, err
			}
			c.Timestamp = t
		}
		if answeredAt != "" {
			t, err := time.Parse(time.RFC3339Nano, answeredAt)
			if err != nil {
				return nil, nil, err
			}
			c.AnsweredAt = &t
		}
		if c.Status == item.SplitCallStatusNoSplit {
			c.NoSplit = &item.SplitNoSplit{Reason: noSplitReason, Signals: slices.Clone(c.Signals)}
			noSplit[c.ItemID] = c
		} else if c.Status == item.SplitCallStatusPending {
			pending[c.ItemID] = c
		}
	}
	return pending, noSplit, rows.Err()
}

func reconstructSplitCallWorkerSession(r *store.Reader, call item.SplitCall) *WorkerSession {
	if r == nil || call.RunID == "" {
		return nil
	}
	var raw []byte
	if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, call.RunID).Scan(&raw); err != nil {
		return nil
	}
	var w pendingWork
	if err := json.Unmarshal(raw, &w); err != nil || w.Decision.Workflow == nil {
		return nil
	}
	wf := w.Decision.Workflow
	evalIdx := -1
	for i, s := range wf.Stages {
		if s.ID == evaluatorSlot(wf) {
			evalIdx = i
		}
	}
	if evalIdx < 0 {
		return nil
	}
	writeIdx := -1
	for i := evalIdx - 1; i >= 0; i-- {
		if wf.Stages[i].Access == "write" {
			writeIdx = i
			break
		}
	}
	if writeIdx < 0 {
		return nil
	}
	baseSHA := call.BaseSHA
	if baseSHA == "" {
		baseSHA = w.BaseSHA
	}
	return &WorkerSession{
		RunID:       call.RunID,
		LaneID:      w.Decision.LaneID,
		Workflow:    wf,
		NodeIndex:   writeIdx,
		BaseSHA:     baseSHA,
		NodeBaseSHA: baseSHA,
		HeadSHA:     call.DonorSHA,
	}
}

func (p *planner) splitCallDecision(row itemRow, sc item.SplitCall) Decision {
	d := Decision{
		ItemID:    row.ItemID,
		Seat:      "design",
		Attempt:   p.nextAttempt(row.ItemID),
		From:      "held",
		Action:    ActionHold,
		Reason:    ReasonSplitCallDesign,
		SplitCall: &sc,
	}
	switch {
	case p.policy == nil:
	case len(p.policy.SeatList("design")) == 0:
	default:
		backend := p.policy.SeatList("design")[0]
		if !policyHasBackend(p.policy, backend) {
			d.DeferredBy = backend
			break
		}
		if !p.policy.SeatMayStart(row.ItemID, "design", backend, p.shut) {
			d.DeferredBy = backend
			break
		}
		if p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches {
			break
		}
		// Under item-owned worktrees the split design run takes the item's own
		// directory and an ordinary agent slot, like every other dispatch; a bare
		// freeLane here placed it on a pool lane.
		if p.tick.ItemWorktrees && !p.ordinaryAgentSlotOpen(row.Express, false, "") {
			break
		}
		lane, ok := p.freeLaneForItem(row.ItemID, row.Express)
		if !ok {
			break
		}
		p.assignLane(lane)
		d.Action, d.From, d.To, d.LaneID = ActionDispatch, "queued", "running", lane
		d.SelectSeat = true
		d.RunRequest = p.request(row.ItemID, lane, backend)
		stampDecisionRequest(d, d.RunRequest)
		if p.tick.ItemWorktrees {
			p.claimOrdinaryAgentSlot(row.Express, false, lane, d.RunRequest.RunID)
		}
	}
	identifyDecision(&d)
	return d
}

func readSplitCallFindings(ctx context.Context, storePath, runID string) (string, error) {
	if storePath == "" || runID == "" {
		return "", nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var raw []byte
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, runID).Scan(&raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var p refusalPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	var b strings.Builder
	if p.Detail != "" {
		b.WriteString(strings.TrimSpace(p.Detail))
	}
	if p.VerifyFeedback != nil {
		for _, f := range p.VerifyFeedback.Findings {
			if len(f.Findings) == 0 {
				continue
			}
			for _, vf := range f.Findings {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				fmt.Fprintf(&b, "- %s [%s] (%s; paths: %s): %s", f.Criterion, vf.Kind, vf.Case, strings.Join(vf.Paths, ", "), f.Reason)
			}
		}
	}
	return b.String(), nil
}

func splitCallOutputPath(durableDir string) string {
	return filepath.Join(durableDir, splitStatementFile)
}

func splitCallBriefTask(call item.SplitCall, durableDir string) string {
	outPath := splitCallOutputPath(durableDir)
	return fmt.Sprintf(
		"Answer split call %d (raised by %s on run %s). No repository edits or commits are required.\n\n"+
			"Either:\n"+
			"1. Write a valid split statement JSON (matching item.SplitStatement: `parts` with 2+ parts and `coverage` assigning every non-standard parent criterion/clause) to `%s` (or include a fenced ```json block in your report). "+
			"When a donor is present, each part may set `takes` (`whole`, `named`, or `nothing`), `paths` and optional `shared_files` (for `named`), and `inherited_findings` (word-for-word findings the part inherits).\n"+
			"2. Answer no-split by ending your report with:\n"+
			"   SPLIT-ANSWER: no-split\n"+
			"   REASON: <non-empty reason why the item should not be split>",
		call.CallID, call.Caller, call.RunID, outPath,
	)
}

func splitCallBriefCriteria(recorded stagedItem) string {
	var b strings.Builder
	b.WriteString("Answer the split call with either a valid split statement JSON or a no-split decision with a non-empty reason.\n\nParent criteria (use these criterion IDs in `coverage` for non-standard criteria c5+):")
	for _, c := range recorded.Criteria {
		fmt.Fprintf(&b, "\n- %s: %s", c.CriterionID, c.Text)
	}
	return b.String()
}

func splitCallBriefFacts(call item.SplitCall, requestWords, findings string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Split call: id=%d caller=%s run=%s\n", call.CallID, call.Caller, call.RunID)
	if call.DonorRef != "" || call.DonorSHA != "" {
		fmt.Fprintf(&b, "Donor: ref=%s sha=%s base=%s\n", call.DonorRef, call.DonorSHA, call.BaseSHA)
	}
	for _, sig := range call.Signals {
		fmt.Fprintf(&b, "Measured signal %s: files=%d (limit %d), added_lines=%d (limit %d)\n", sig.Signal, sig.Files, sig.FileLimit, sig.AddedLines, sig.AddedLineLimit)
	}
	if strings.TrimSpace(call.LastRejectionReason) != "" {
		fmt.Fprintf(&b, "\nPrevious malformed-statement rejection reason (fix this before resubmitting):\n%s\n", strings.TrimSpace(call.LastRejectionReason))
	}
	if strings.TrimSpace(findings) != "" {
		fmt.Fprintf(&b, "\nVerifier findings from %s:\n%s\n", call.RunID, strings.TrimSpace(findings))
	}
	if strings.TrimSpace(requestWords) != "" {
		fmt.Fprintf(&b, "\nOwner's saved request words (verbatim):\n%s\n", strings.TrimSpace(requestWords))
	}
	return strings.TrimSpace(b.String())
}

func splitCallBriefSettled(durableDir string) string {
	return fmt.Sprintf(
		"This run acts on the design seat for a split call. No repository edits or commits are required.\n"+
			"Write your split statement JSON to `%s`, or answer `SPLIT-ANSWER: no-split` with `REASON: <reason>` in your report.\n"+
			"Every non-standard parent criterion (c5+) or its clauses must be owned in `coverage`.\n"+
			"Donor carving (§8.2): if every finding is a gap and the whole candidate was examined, one part may set `takes: \"whole\"` while another takes `nothing` with `inherited_findings`; "+
			"if regressions or unasked changes sit in separable paths, one part may set `takes: \"named\"` with `paths` (and optional `shared_files`) while another inherits the refused paths and `inherited_findings`; "+
			"if a regression sits in code the rest depends on, answer `no-split`.",
		splitCallOutputPath(durableDir),
	)
}

type splitNoSplitJSON struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func parseNoSplitReport(log string) (found bool, reason string) {
	hasNoSplit := false
	for _, line := range strings.Split(log, "\n") {
		trimmed := strings.TrimSpace(line)
		if val, ok := strings.CutPrefix(trimmed, "SPLIT-ANSWER:"); ok {
			if strings.EqualFold(strings.TrimSpace(val), "no-split") {
				hasNoSplit = true
			}
		} else if val, ok := strings.CutPrefix(trimmed, "REASON:"); ok {
			if reason == "" {
				reason = strings.TrimSpace(val)
			}
		}
	}
	return hasNoSplit, reason
}

func extractFencedJSON(log string) ([]byte, bool) {
	start := strings.Index(log, "```json")
	if start < 0 {
		return nil, false
	}
	rest := log[start+len("```json"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		return nil, false
	}
	return []byte(strings.TrimSpace(rest[:end])), true
}

func (l *Loop) readSplitAnswerRaw(lane, runID, log string) (raw []byte, fromFile bool, noSplit bool, noSplitReason string) {
	candidates := []string{
		splitCallOutputPath(run.DurableDir(filepath.Join(l.cfg.LaneRoot, lane), runID)),
	}
	if l.cfg.Root != "" {
		candidates = append(candidates, splitCallOutputPath(nodeOutputsDir(l.cfg.Root, runID)))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			raw = data
			fromFile = true
			break
		}
	}
	if !fromFile {
		if hasNoSplit, reason := parseNoSplitReport(log); hasNoSplit {
			return nil, false, true, reason
		}
		if fenced, ok := extractFencedJSON(log); ok {
			raw = fenced
		} else {
			return nil, false, false, ""
		}
	}
	var ns splitNoSplitJSON
	if err := json.Unmarshal(raw, &ns); err == nil && strings.EqualFold(strings.TrimSpace(ns.Decision), "no-split") {
		return raw, fromFile, true, strings.TrimSpace(ns.Reason)
	}
	return raw, fromFile, false, ""
}

func (l *Loop) recordSplitCallRejection(ctx context.Context, itemID, designRunID, reason string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return item.RecordSplitRejection(ctx, tx, itemID, designRunID, reason)
	})
}

func (l *Loop) settleSplitCallUnanswered(ctx context.Context, decision Decision, request run.Request, lane, outcome string, at time.Time) laneOutcome {
	sc := *decision.SplitCall
	conflictID := fmt.Sprintf("split-call-%d", sc.CallID)
	if sc.CallID == 0 {
		conflictID = "split-call-" + sc.RunID
	}
	block := evidenceDigest(sc.DonorSHA+"\x00"+sc.LastRejectionReason, "")
	mark := designUnanswered{ConflictID: conflictID, ItemID: sc.ItemID, RunID: request.RunID, Outcome: outcome, Changes: []string{}, Reraised: true}
	mark.Cause, mark.Key = designEndingKey(designEndingFacts{Step: designCauseMissingActor, ConflictID: conflictID, Block: block, RunID: request.RunID})
	marks, err := l.readItemDesignMarks(ctx, sc.ItemID)
	if err != nil {
		return laneOutcome{err: err}
	}
	if earlier, repeat := designRepeatOf(marks, mark); repeat {
		c := item.Conflict{
			ConflictID:      conflictID,
			ItemID:          sc.ItemID,
			RunID:           sc.RunID,
			Text:            fmt.Sprintf("Split call %d (donor %s) unanswered by design run %s.", sc.CallID, sc.DonorSHA, request.RunID),
			Status:          "raised",
			WaitingOn:       "design",
			BlockedCriteria: []string{},
		}
		l.routeDesignRepeat(&c, &mark, earlier)
		if err := l.saveDesignEnding(ctx, c, mark); err != nil {
			return laneOutcome{err: err}
		}
	} else {
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return laneOutcome{err: err}
		}
		writeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
			return appendEffect(tx, designUnansweredKind, mark.RunID, mark, at)
		})
		s.Close()
		if writeErr != nil {
			return laneOutcome{err: writeErr}
		}
	}
	return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), outcome)}
}

func (l *Loop) settleSplitCallRun(ctx context.Context, decision Decision, request run.Request, record run.Record, lane, log string, at time.Time) laneOutcome {
	if record.State != run.StateExited || record.ExitCode == nil || *record.ExitCode != 0 {
		return l.settleSplitCallUnanswered(ctx, decision, request, lane, "design run failed", at)
	}
	raw, _, noSplit, noSplitReason := l.readSplitAnswerRaw(lane, request.RunID, log)
	if noSplit {
		if strings.TrimSpace(noSplitReason) == "" {
			if err := l.recordSplitCallRejection(ctx, request.ItemID, request.RunID, "no-split answer requires a non-empty reason"); err != nil {
				return laneOutcome{err: err}
			}
			return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "no-split missing reason")}
		}
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return laneOutcome{err: err}
		}
		writeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := item.AnswerSplitNoSplit(ctx, tx, request.ItemID, request.RunID, noSplitReason, at)
			return err
		})
		s.Close()
		if writeErr != nil {
			return laneOutcome{err: writeErr}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "split call answered no-split")}
	}
	if len(raw) == 0 {
		return l.settleSplitCallUnanswered(ctx, decision, request, lane, "design run ended without a split statement or no-split answer", at)
	}
	statement, err := parseSplitStatement(raw)
	if err != nil {
		if recErr := l.recordSplitCallRejection(ctx, request.ItemID, request.RunID, err.Error()); recErr != nil {
			return laneOutcome{err: recErr}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "malformed split statement")}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return laneOutcome{err: err}
	}
	writeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := item.FileSplit(ctx, tx, request.ItemID, statement, at); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE split_calls SET design_run_id=? WHERE item_id=? AND status=? AND design_run_id=''`, request.RunID, request.ItemID, item.SplitCallStatusSplit)
		return err
	})
	s.Close()
	if writeErr != nil {
		if recErr := l.recordSplitCallRejection(ctx, request.ItemID, request.RunID, writeErr.Error()); recErr != nil {
			return laneOutcome{err: recErr}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "rejected split statement")}
	}
	return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "split statement filed")}
}

type splitClosingState struct {
	advanceMu        sync.Mutex
	active           map[string]bool
	results          map[string]splitclosing.RunResult
	inheritedPending bool
}

func (l *Loop) hasSplitClosingWork() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.splitClosing.active) > 0 || len(l.splitClosing.results) > 0 || l.splitClosing.inheritedPending
}

func (l *Loop) hasSplitClosingResults() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.splitClosing.results) > 0
}

func (l *Loop) startSplitClosingRun(ctx context.Context, req run.Request) error {
	runCtx := ctx
	if loopCtx := l.getLoopCtx(); loopCtx != nil {
		runCtx = loopCtx
	}
	l.mu.Lock()
	if l.splitClosing.active == nil {
		l.splitClosing.active = map[string]bool{}
	}
	if l.splitClosing.active[req.RunID] {
		l.mu.Unlock()
		return nil
	}
	l.splitClosing.active[req.RunID] = true
	l.mu.Unlock()

	go func() {
		rec, _, runErr := l.runRestartable(runCtx, req)
		l.mu.Lock()
		delete(l.splitClosing.active, req.RunID)
		if !errors.Is(runErr, errRestartPending) && runCtx.Err() == nil {
			if l.splitClosing.results == nil {
				l.splitClosing.results = map[string]splitclosing.RunResult{}
			}
			l.splitClosing.results[req.RunID] = splitclosing.RunResult{
				Record: rec,
				Err:    runErr,
			}
		}
		l.mu.Unlock()
		l.signalPassWake()
		l.signalTrunkProofWake()
	}()
	return nil
}

func (l *Loop) checkSplitClosingRun(ctx context.Context, runID string) (splitclosing.RunResult, splitclosing.RunState, error) {
	l.mu.Lock()
	if l.splitClosing.active[runID] {
		l.mu.Unlock()
		return splitclosing.RunResult{}, splitclosing.RunStateRunning, nil
	}
	if res, ok := l.splitClosing.results[runID]; ok {
		l.mu.Unlock()
		return res, splitclosing.RunStateFinished, nil
	}
	l.mu.Unlock()

	// Not tracked in this process (for example after a loop restart). Check the
	// store for a terminal run record first.
	readTerminal := func() (*run.Record, error) {
		db, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		var rec *run.Record
		err = db.ReadTx(ctx, func(r *store.Reader) error {
			var rerr error
			rec, rerr = terminalRecord(r, runID)
			return rerr
		})
		return rec, err
	}
	rec, err := readTerminal()
	if err != nil {
		return splitclosing.RunResult{}, splitclosing.RunStateRunning, err
	}
	if rec != nil && rec.State != "" && rec.State != "running" {
		return splitclosing.RunResult{Record: *rec}, splitclosing.RunStateFinished, nil
	}

	here, err := currentLoopOwner()
	if err != nil {
		return splitclosing.RunResult{}, splitclosing.RunStateRunning, err
	}
	liveFaces, err := liveRunFaces(l.cfg.Store, runID, here)
	if err != nil {
		return splitclosing.RunResult{}, splitclosing.RunStateRunning, err
	}
	if len(liveFaces) > 0 {
		return splitclosing.RunResult{}, splitclosing.RunStateRunning, nil
	}

	// Re-check the store after observing no live face to close the exit race
	// where the run face wrote its terminal record and exited in between.
	rec, err = readTerminal()
	if err != nil {
		return splitclosing.RunResult{}, splitclosing.RunStateRunning, err
	}
	if rec != nil && rec.State != "" && rec.State != "running" {
		return splitclosing.RunResult{Record: *rec}, splitclosing.RunStateFinished, nil
	}

	return splitclosing.RunResult{
		Err: fmt.Errorf("run %s exited without a terminal record", runID),
	}, splitclosing.RunStateFinished, nil
}

func (l *Loop) consumeSplitClosingRun(runID string) {
	l.mu.Lock()
	delete(l.splitClosing.results, runID)
	l.mu.Unlock()
}

func (l *Loop) advanceSplitClosingChecks(ctx context.Context, mainSHA string, result *LoopResult) (bool, error) {
	l.splitClosing.advanceMu.Lock()
	defer l.splitClosing.advanceMu.Unlock()
	adv, err := splitclosing.Advance(ctx, splitclosing.Env{
		NonLanding: l.nonLanding,
		Root:       l.cfg.Root,
		Store:      l.cfg.Store,
		LaneRoot:   l.cfg.LaneRoot,
		TrunkRef:   l.trunkRef(),
		Now:        l.cfg.Now,
		LaneDir:    l.laneDirPath,
		RevParse:   l.revParse,
		SelectSeats: func(seat string) (splitclosing.SeatSelection, bool) {
			p := l.currentPolicy()
			if p == nil {
				return splitclosing.SeatSelection{}, false
			}
			return splitclosing.SeatSelection{
				Backends:  p.SeatList(seat),
				SeatLines: seatLines(p),
				Request: func(itemID, laneID, runID, backend string) run.Request {
					return *p.Request(itemID, laneID, runID, backend)
				},
			}, true
		},
		CanStart: func(ctx context.Context) bool {
			return l.checkRestartAdmission(ctx, false) == nil
		},
		StartRun:   l.startSplitClosingRun,
		CheckRun:   l.checkSplitClosingRun,
		ConsumeRun: l.consumeSplitClosingRun,
		CountSpend: func(rec run.Record) { l.countSpend(result, rec) },
		SetItemState: func(ctx context.Context, id, runID, state string) error {
			return l.cfg.Steps.ItemState(ctx, id, runID, state)
		},
		RequestWordsSuffix: requestWordsSuffix,
		BraceLiteralCount:  braceLiteralCount,
	}, mainSHA)
	l.mu.Lock()
	l.splitClosing.inheritedPending = adv.Pending && len(l.splitClosing.active) == 0
	l.mu.Unlock()
	return adv.Worked, err
}
