package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// laneRow describes only recorded states. A completed command is not evidence that
// the next command is in progress; LastStep is deliberately the last finished step.
type laneRow struct {
	Progress      []progressClaim         `json:"progress"`
	ChildSessions map[string]childrenView `json:"child_sessions"`
	RunningNodes  int                     `json:"running_nodes,omitempty"`
	Nodes         []laneNode              `json:"nodes"`
	Pipeline      []pipelineStep          `json:"pipeline"`
	PipelineNote  string                  `json:"pipeline_note,omitempty"`
	History       []laneHistoryRun        `json:"history"`
	Usage         usageSum                `json:"usage"`
	LaneID        string                  `json:"lane_id"`
	State         string                  `json:"state"`
	StateSinceMS  *int64                  `json:"state_since_ms"`
	RunID         string                  `json:"run_id"`
	ItemID        string                  `json:"item_id"`
	Seat          string                  `json:"seat"`
	// SeatSource says where Seat was read: "dispatch" (the seat the planner recorded on the
	// decision that started this run), "item" (the item's current seat, for a run whose
	// decision predates the dispatch column), or "none" — the item asked for no seat, and
	// the policy's default backend ran it. "none" is a recorded fact, not a missing record.
	SeatSource string `json:"seat_source"`
	Backend    string `json:"backend"`
	Attempt    int    `json:"attempt"`
	// Work is the item's work beside Attempt, which numbers allocations and so is not
	// a count of work done (item_progress.go).
	Work         itemProgress `json:"work"`
	Outcome      string       `json:"outcome"`
	LastStep     string       `json:"last_step"`
	LastStepAtMS *int64       `json:"last_step_at_ms"`
	LatestAction laneAction   `json:"latest_action"`
	LogGrewAtMS  *int64       `json:"log_grew_at_ms"`
	Note         string       `json:"note,omitempty"`
	// Condition is the lane's standing staging refusal, when the drain took it out of
	// rotation (drain.lane-refused with no later drain.lane-restored). It is omitted for a
	// lane in rotation.
	Condition *laneCondition `json:"condition,omitempty"`
	// Hold is the standing refusal an adapter made of this lane's run before any model ran
	// (an item fault with reason adapter-refused, naming this run). Detail is the adapter's
	// own words; Mover is the operator's act. It is omitted for a run nothing refused.
	Hold *laneHold `json:"hold,omitempty"`
}

type laneHold struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Mover  string `json:"mover"`
	AtMS   int64  `json:"at_ms"`
}

type laneAction struct {
	Kind  string `json:"kind"` // action, liveness, or unrecorded
	Text  string `json:"text"`
	AtMS  *int64 `json:"at_ms"`
	Clock string `json:"clock"` // observed, derived_clock, or no_clock
}

func lanesFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("lanes")
	path := fs.String("store", "", "the store to read (required)")
	loop := fs.String("loop-dir", "", "finished step directory (default: <store dir>/loop)")
	lanes := fs.String("lane-root", "", "lane stream directory (default: <store dir>/lanes)")
	asJSON := fs.Bool("json", false, "print lane rows as JSON instead of a table")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" {
		return record.Envelope{}, usagef("lanes needs --store <path>")
	}
	if *loop == "" {
		*loop = filepath.Join(filepath.Dir(*path), "loop")
	}
	if *lanes == "" {
		*lanes = filepath.Join(filepath.Dir(*path), "lanes")
	}
	rows, err := readLaneRows(context.Background(), *path, *loop, *lanes, time.Now())
	if err != nil {
		var mismatch schemaMismatch
		if errors.As(err, &mismatch) {
			fmt.Fprintln(os.Stderr, err)
			return record.Envelope{}, record.ExitIncompatible
		}
		return record.Envelope{}, exitFor(err)
	}
	if *asJSON {
		err = json.NewEncoder(os.Stdout).Encode(rows)
	} else {
		err = printLanes(os.Stdout, rows)
	}
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	return record.Envelope{}, record.ExitOK
}

// laneCapacityLine counts ordinary and reserved express lanes independently;
// document desks remain visible but belong to neither pool.
func laneCapacityLine(rows []laneRow) string {
	ordinary, running, express, expressRunning := 0, 0, 0, 0
	for _, row := range rows {
		if strings.HasPrefix(row.LaneID, "desk-") {
			continue
		}
		if strings.HasPrefix(row.LaneID, "express-") {
			express++
			if row.State == "running" {
				expressRunning++
			}
		} else {
			ordinary++
			if row.State == "running" {
				running++
			}
		}
	}
	return fmt.Sprintf("%d of %d running, %d of %d express", running, ordinary, expressRunning, express)
}

// laneWorkState distinguishes active work from a terminal run whose lease is held
// while it settles. Parallel readers remain active even if their representative ended.
const pausedRunState = "paused"

func laneWorkState(lane storeLane, run storeRun) string {
	if lane.State != laneStateLeased {
		return "idle"
	}
	if len(lane.ActiveNodes) == 0 && run.State == pausedRunState {
		// A restart pauses a run's row; its continuation keeps that row until it finishes,
		// and claims the lane with a run.resume event newer than the row.
		if lane.ResumedSeq > run.EventSeq {
			return "running"
		}
		return "paused"
	}
	if len(lane.ActiveNodes) == 0 && run.State != "" && run.State != liveRunState {
		return "settling"
	}
	return "running"
}

func printLanes(w io.Writer, rows []laneRow) error {
	if _, err := fmt.Fprintln(w, laneCapacityLine(rows)); err != nil {
		return err
	}
	for _, row := range rows {
		since := "—"
		if row.StateSinceMS != nil {
			since = time.UnixMilli(*row.StateSinceMS).UTC().Format(time.RFC3339Nano)
		}
		action := row.LatestAction.Text
		at := "—"
		if row.LatestAction.AtMS != nil {
			at = time.UnixMilli(*row.LatestAction.AtMS).UTC().Format(time.RFC3339Nano)
		}
		step := "—"
		if row.LastStepAtMS != nil {
			step = row.LastStep + " @ " + time.UnixMilli(*row.LastStepAtMS).UTC().Format(time.RFC3339Nano)
		}
		if _, err := fmt.Fprintf(w, "%s · %s since %s · %s · %s · %s · attempt %d · %s · %s · last finished %s · %s (%s, %s) @ %s\n", row.LaneID, row.State, since, row.ItemID, row.Seat, row.Backend, row.Attempt, row.RunID, row.Outcome, step, action, row.LatestAction.Kind, row.LatestAction.Clock, at); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  %s\n", row.Work.Text()); err != nil {
			return err
		}
		if h := row.Hold; h != nil {
			if _, err := fmt.Fprintf(w, "  held since %s: %s: %s. Mover: %s\n", time.UnixMilli(h.AtMS).UTC().Format(time.RFC3339), h.Reason, h.Detail, h.Mover); err != nil {
				return err
			}
		}
		if c := row.Condition; c != nil {
			if _, err := fmt.Fprintf(w, "  out of rotation since %s: %s: %s. Remedy: %s\n", time.UnixMilli(c.AtMS).UTC().Format(time.RFC3339), c.Reason, c.Detail, c.Remedy); err != nil {
				return err
			}
		}
	}
	return nil
}

func readLaneRows(ctx context.Context, path, loop, lanes string, now time.Time) ([]laneRow, error) {
	db, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var rows []laneRow
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		rows, err = assembleLanes(r, loop, lanes, now)
		return err
	})
	return rows, err
}

func (s *server) lanesPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	stats := &laneReadStats{}
	start := time.Now()
	rows, err := assembleLanesCounted(r, s.obs.loopDir, s.obs.laneRoot, time.Now(), stats)
	phase(req.Context(), "lane_logs", map[string]any{"ms": msSince(time.Now, start), "files": stats.files, "bytes": stats.bytes})
	if err != nil {
		return nil, failure(codeStoreUnreadable, "lanes could not read the store: %v", err)
	}
	runs, err := readUsageRuns(r)
	if err != nil {
		return nil, unreadable(err)
	}
	for i := range rows {
		selected := []storeRun{}
		for _, run := range runs {
			if run.LaneID == rows[i].LaneID {
				selected = append(selected, run)
			}
		}
		rows[i].Usage, err = sumUsage(r, selected)
		if err != nil {
			return nil, unreadable(err)
		}
	}
	return rows, nil
}

func assembleLanes(r *store.Reader, loop, lanes string, now time.Time) ([]laneRow, error) {
	return assembleLanesCounted(r, loop, lanes, now, nil)
}

// laneReadStats is what one lanes answer read from the lane directories: the performance
// log's `lane_logs` phase. A nil one counts nothing.
type laneReadStats struct {
	files int
	bytes int64
}

func (s *laneReadStats) add(n int) {
	if s != nil {
		s.files++
		s.bytes += int64(n)
	}
}

func assembleLanesCounted(r *store.Reader, loop, lanes string, now time.Time, stats *laneReadStats) ([]laneRow, error) {
	d, err := readStoreDomainIn(r, loop)
	if err != nil {
		return nil, err
	}
	verdicts, err := readGateVerdicts(r)
	if err != nil {
		return nil, err
	}
	seats := map[string]string{}
	rs, err := r.Query(`SELECT item_id, seat FROM items`)
	if err != nil && !isMissingColumn(err) && !isMissingTable(err) {
		return nil, err
	}
	if err == nil {
		defer rs.Close()
		for rs.Next() {
			var id, seat string
			if err := rs.Scan(&id, &seat); err != nil {
				return nil, err
			}
			seats[id] = seat
		}
		if err := rs.Err(); err != nil {
			return nil, err
		}
	}
	rows := buildLaneRowsCounted(d, verdicts, seats, loop, lanes, now, stats)
	progress, err := readItemProgress(r)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Work = progress[rows[i].ItemID]
	}
	if err := enrichLaneNodes(r, rows, loop, stats); err != nil {
		return nil, err
	}
	if err := enrichWorkerRecords(r, rows); err != nil {
		return nil, err
	}
	// The seat a run was dispatched for is on its decision row (tillr-drain's
	// dispatches.seat), and it outranks the item's seat: an item's seat can change after the
	// run started, and the lane is describing the run.
	dispatched, err := dispatchSeats(r)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if seat, ok := dispatched[rows[i].RunID]; ok && seat != "" {
			rows[i].Seat, rows[i].SeatSource = seat, "dispatch"
		}
	}
	conditions, err := readLaneConditions(r)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if c, ok := conditions[rows[i].LaneID]; ok {
			rows[i].Condition = &c
		}
	}
	itemFaults, err := faults.Read(r)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		f, ok := itemFaults[rows[i].ItemID]
		if ok && f.Reason == "adapter-refused" && f.RunID == rows[i].RunID {
			rows[i].Hold = &laneHold{Reason: f.Reason, Detail: f.Detail, Mover: holdrules.For(f.Reason).Mover, AtMS: f.AtMS}
		}
	}
	return rows, nil
}

// dispatchSeats answers the seat each run's dispatch recorded. A store whose dispatches
// table predates the seat column answers none, which leaves the item's seat in place.
func dispatchSeats(r *store.Reader) (map[string]string, error) {
	out := map[string]string{}
	rs, err := r.Query(`SELECT run_id, COALESCE(seat, '') FROM dispatches WHERE run_id IS NOT NULL AND run_id != ''`)
	if err != nil {
		if isMissingColumn(err) || isMissingTable(err) {
			return out, nil
		}
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var run, seat string
		if err := rs.Scan(&run, &seat); err != nil {
			return nil, err
		}
		if seat != "" {
			out[run] = seat
		}
	}
	return out, rs.Err()
}

func buildLaneRows(d *storeDomain, verdicts map[string]gateVerdict, seats map[string]string, loop, lanes string, now time.Time) []laneRow {
	return buildLaneRowsCounted(d, verdicts, seats, loop, lanes, now, nil)
}

func buildLaneRowsCounted(d *storeDomain, verdicts map[string]gateVerdict, seats map[string]string, loop, lanes string, now time.Time, stats *laneReadStats) []laneRow {
	rows := []laneRow{}
	runs := map[string]storeRun{}
	for id, a := range d.allocations {
		runs[id] = storeRun{RunID: id, ItemID: a.ItemID, LaneID: a.LaneID, Backend: a.Backend, State: liveRunState, StartedAtMS: a.StartedAtMS}
	}
	for _, r := range d.runs {
		runs[r.RunID] = r
	}
	ordered := make([]storeRun, 0, len(runs))
	for _, r := range runs {
		ordered = append(ordered, r)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartedAtMS != ordered[j].StartedAtMS {
			return ordered[i].StartedAtMS < ordered[j].StartedAtMS
		}
		return ordered[i].RunID < ordered[j].RunID
	})
	attempts := map[string]int{}
	numbers := map[string]int{}
	last := map[string]storeRun{}
	for _, r := range ordered {
		attempts[r.ItemID]++
		numbers[r.RunID] = attempts[r.ItemID]
		last[r.LaneID] = r
	}
	candidates := map[string]storeCandidate{}
	for _, c := range d.candidates {
		candidates[c.CandidateID] = c
	}
	landed := map[string]storeAttempt{}
	for _, as := range d.attempts {
		for _, a := range as {
			if a.Result == "landed" {
				landed[a.VerdictID] = a
			}
		}
	}
	for _, lane := range d.lanes {
		row := laneRow{LaneID: lane.LaneID, State: "idle", LatestAction: laneAction{Kind: "unrecorded", Clock: stateNoneClock}}
		row.RunningNodes = len(lane.ActiveNodes)
		run := last[lane.LaneID]
		if lane.State == laneStateLeased {
			run = runs[lane.RunID]
			if row.RunningNodes > 0 {
				run = runs[lane.ActiveNodes[0]]
			}
		}
		row.State = laneWorkState(lane, run)
		for i := len(ordered) - 1; i >= 0; i-- {
			r := ordered[i]
			if r.LaneID == lane.LaneID {
				row.History = append(row.History, laneHistoryRun{RunID: r.RunID, ItemID: r.ItemID, LaneID: r.LaneID, State: r.State, StartedAtMS: r.StartedAtMS, FinishedAtMS: r.FinishedAtMS})
			}
		}
		row.RunID = run.RunID
		if lane.State == laneStateLeased && row.RunID == "" {
			row.RunID = lane.RunID
			row.Note = "run allocation not recorded"
		}
		row.ItemID = run.ItemID
		row.Backend = run.Backend
		row.Seat = seats[run.ItemID]
		row.SeatSource = "none"
		if row.Seat != "" {
			row.SeatSource = "item"
		}
		row.Attempt = numbers[run.RunID]
		since := run.StartedAtMS
		if row.State != "running" {
			since = run.FinishedAtMS
			if run.RunID != "" {
				row.Outcome, _ = runOutcome(run, verdicts, landed, candidates)
				if lane.DeadRunID != "" && run.RunID == lane.DeadRunID && (run.State == liveRunState || lane.DeadResumedSeq > run.EventSeq) {
					row.Outcome = "interrupted"
				}
			}
		}
		row.StateSinceMS = clockPointer(since, since > 0)
		if row.RunID != "" {
			row.Pipeline, row.PipelineNote = readLanePipeline(filepath.Join(loop, row.RunID, "pipeline.jsonl"), stats)
			var note string
			row.LatestAction, note, row.LogGrewAtMS = readLaneActionCounted(runLogPath(lanes, lane.LaneID, row.RunID), row.Backend, stats)
			if note != "" {
				if row.Note != "" {
					row.Note += "; "
				}
				row.Note += note
			}
			if row.State == "settling" {
				row.LastStep, row.LastStepAtMS = lastFinishedLaneStepCounted(filepath.Join(loop, row.RunID, "steps.jsonl"), stats)
			}
			if row.LatestAction.Kind == "liveness" && row.LogGrewAtMS != nil {
				row.LatestAction.Text = "log grew " + laneAge(now.UnixMilli()-*row.LogGrewAtMS) + " ago"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func laneAge(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 60000 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%dm", ms/60000)
}

// Read only a bounded tail on each refresh, not an ever-growing transcript. Skip a
// partial first line and an unfinished last line; neither is an adapter record yet.
const laneTailBytes int64 = 256 * 1024

func laneTail(path string) ([]byte, *int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	start := info.Size() - laneTailBytes
	if start < 0 {
		start = 0
	}
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return nil, nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, laneTailBytes))
	if err != nil {
		return nil, nil, err
	}
	if start > 0 {
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			raw = raw[i+1:]
		} else {
			raw = nil
		}
	}
	if i := bytes.LastIndexByte(raw, '\n'); i >= 0 {
		raw = raw[:i+1]
	} else {
		raw = nil
	}
	at := info.ModTime().UnixMilli()
	return raw, &at, nil
}

func lastFinishedLaneStep(path string) (string, *int64) {
	return lastFinishedLaneStepCounted(path, nil)
}

func lastFinishedLaneStepCounted(path string, stats *laneReadStats) (string, *int64) {
	raw, _, err := laneTail(path)
	stats.add(len(raw))
	if err != nil {
		return "", nil
	}
	var last stepLine
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var s stepLine
		if json.Unmarshal(line, &s) == nil && s.FinishedAtMS > 0 && s.FinishedAtMS >= last.FinishedAtMS {
			last = s
		}
	}
	return last.Step, clockPointer(last.FinishedAtMS, last.FinishedAtMS > 0)
}

func readLaneAction(path, backend string) (laneAction, string, *int64) {
	return readLaneActionCounted(path, backend, nil)
}

func readLaneActionCounted(path, backend string, stats *laneReadStats) (laneAction, string, *int64) {
	missing := laneAction{Kind: "unrecorded", Clock: stateNoneClock}
	if path == "" {
		return missing, "no lane root is configured, so this run's stream log has no path to read", nil
	}
	raw, mtime, err := laneTail(path)
	stats.add(len(raw))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return missing, "no stream log at " + path + ": the run has written none there", nil
		}
		return missing, "the stream log " + path + " could not be read: " + err.Error(), nil
	}
	latest := laneAction{Kind: "liveness", AtMS: mtime, Clock: stateDerivedClock}
	// Backend names are policy aliases (for example pi-designer). Identify the
	// adapter by its recorded event shape, never by guessing from that alias.
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if action, ok := parseLaneAction(line, backend); ok {
			if action.AtMS == nil {
				action.AtMS = mtime
				action.Clock = stateDerivedClock
			}
			latest = action
		}
	}
	if latest.Kind == "liveness" {
		return latest, "no readable live action in the stream tail", mtime
	}
	return latest, "", mtime
}

// Decode object tokens in wire order: ranging a Go map cannot identify the first
// argument the harness actually recorded.
func firstLaneArgument(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') || !dec.More() {
		return ""
	}
	if _, err = dec.Token(); err != nil {
		return ""
	}
	var value any
	if dec.Decode(&value) != nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	b, _ := json.Marshal(value)
	return string(b)
}
func laneSnippet(text string) string {
	r := []rune(text)
	if len(r) > 80 {
		r = r[:80]
	}
	return string(r)
}

func parseLaneAction(raw []byte, backend string) (laneAction, bool) {
	var e struct {
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		ToolName  string          `json:"toolName"`
		Args      json.RawMessage `json:"args"`
		Message   *struct {
			Role      string `json:"role"`
			Timestamp int64  `json:"timestamp"`
			Content   []struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
				Input     json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return laneAction{}, false
	}
	switch e.Type {
	case "message_end", "message_start", "message_update", "tool_execution_start":
		backend = "pi"
	case "assistant":
		backend = "claude"
	default:
		return laneAction{}, false
	}
	at, ok := logClock(&logEvent{Timestamp: e.Timestamp})
	if e.Message != nil && e.Message.Timestamp > 0 {
		at = e.Message.Timestamp
		ok = true
	}
	a := laneAction{Kind: "action", AtMS: clockPointer(at, ok), Clock: stateObserved}
	if backend == "pi" && e.Type == "tool_execution_start" {
		a.Text = e.ToolName + ": " + firstLaneArgument(e.Args)
		return a, e.ToolName != ""
	}
	if e.Message == nil || e.Message.Role != "assistant" || !((backend == "pi" && (e.Type == "message_end" || e.Type == "message_start" || e.Type == "message_update")) || (backend == "claude" && e.Type == "assistant")) {
		return a, false
	}
	for _, b := range e.Message.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				a.Text = laneSnippet(b.Text)
			}
		case "toolCall":
			if backend == "pi" {
				a.Text = b.Name + ": " + firstLaneArgument(b.Arguments)
			}
		case "tool_use":
			if backend == "claude" {
				a.Text = b.Name + ": " + firstLaneArgument(b.Input)
			}
		}
	}
	return a, a.Text != ""
}
