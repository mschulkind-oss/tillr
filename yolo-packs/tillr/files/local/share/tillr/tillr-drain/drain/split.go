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
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

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
	pending = map[string]item.SplitCall{}
	noSplit = map[string]item.SplitCall{}
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='split_calls')`).Scan(&exists); err != nil {
		return nil, nil, err
	}
	if !exists {
		return pending, noSplit, nil
	}
	rows, err := r.Query(
		`SELECT call_id, item_id, caller, run_id, donor_ref, donor_sha, base_sha, signals, status, design_run_id, no_split_reason, last_rejection_reason, created_at, answered_at
		 FROM split_calls WHERE status IN (?,?) ORDER BY call_id ASC`,
		item.SplitCallStatusPending, item.SplitCallStatusNoSplit,
	)
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
		lane, ok := p.freeLane(row.Express)
		if !ok {
			break
		}
		p.assignLane(lane)
		d.Action, d.From, d.To, d.LaneID = ActionDispatch, "queued", "running", lane
		d.SelectSeat = true
		d.RunRequest = p.request(row.ItemID, lane, backend)
		stampDecisionRequest(d, d.RunRequest)
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
			"1. Write a valid split statement JSON (matching item.SplitStatement: `parts` with 2+ parts and `coverage` assigning every non-standard parent criterion/clause) to `%s` (or include a fenced ```json block in your report), OR\n"+
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
			"Parts filed from a split statement take nothing from the donor in this slice; every non-standard parent criterion (c5+) or its clauses must be owned in `coverage`.",
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
