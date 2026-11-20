// running is the one-screen answer to "what is running right now": the loop's live
// session and lane leases, the runs a lane is working, and the last five runs that
// finished. It joins the store's sessions, leases and runs with stop result files:
//
//   - the lane rows and their `run.allocate` events, because `run.Exec` writes no `runs`
//     row until its terminal write and `run.Apply` clears `lanes.run_id` on release, so
//     a run that is going is a lease plus an allocation and nothing else;
//   - the terminal `runs` rows, because a lease is cleared the moment a run ends;
//   - the loop's session heartbeat, with `drain.loop` result files as stop evidence
//     only when no session is live.
//
// The view writes nothing: it opens the store through store.OpenRead, so pointing it at
// the live store while a drain writes is the intended use.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr-drain/judgegrants"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// runningFinishedLimit is how many finished runs the face shows. Five is the design's
// number: enough to see the shape of the last few outcomes, small enough to read without
// paging.
const runningFinishedLimit = 5

// runningPayload is the view.running record: the loop and lane header, the live runs, and
// the last few finished runs. Every field is named for the block it belongs to, so a
// reader can tell a live run from a finished one without reading a state word.
type runningPayload struct {
	Grants      []judgegrants.View `json:"grants,omitempty"`
	GeneratedAt time.Time          `json:"generated_at"`
	Snapshot    snapshot           `json:"snapshot"`
	Lanes       runningLanes       `json:"lanes"`
	Loop        runningLoop        `json:"loop"`
	Active      []runningRun       `json:"active"`
	Finished    []finishedRun      `json:"finished"`
}

// runningLanes is the header's lane arithmetic: how many lanes hold a lease, out of how
// many the store knows, and which ones are idle. The idle list is what makes "0 live
// runs" an answer rather than a blank.
type runningLanes struct {
	Leased int      `json:"leased"`
	Total  int      `json:"total"`
	Idle   []string `json:"idle"`
}

// runningLoop prefers the recorded live session over historical stop evidence.
// Stale describes a stop file followed by newer events, only when no session is live.
type runningLoop struct {
	Session      *loopSession  `json:"session"`
	Active       bool          `json:"active"`
	SessionNote  string        `json:"session_note"`
	StopReason   string        `json:"stop_reason"`
	StopNote     string        `json:"stop_note"`
	RecordPath   string        `json:"record_path,omitempty"`
	RecordedAt   *time.Time    `json:"recorded_at,omitempty"`
	Stale        bool          `json:"stale"`
	StaleSeq     int64         `json:"stale_seq,omitempty"`
	StaleNote    string        `json:"stale_note,omitempty"`
	RepeatedStop *repeatedStop `json:"repeated_stop,omitempty"`
}

// runningRun is one live run: the lane's lease, the run, the item it is for, and the two
// clocks (elapsed from the run's start, and the lease's own expiry). ElapsedMS and
// LeaseLeftMS are pointers because absent clocks differ from zero elapsed or expiry.
// LeaseNoExpiry distinguishes a leased lane with no expiration from a missing lease.
type runningRun struct {
	LaneID        string `json:"lane_id"`
	RunID         string `json:"run_id"`
	ItemID        string `json:"item_id"`
	Place         int    `json:"place,omitempty"`
	Title         string `json:"title"`
	Seat          string `json:"seat,omitempty"`
	Workflow      string `json:"workflow,omitempty"`
	Node          string `json:"node,omitempty"`
	StartedAtMS   int64  `json:"started_at_ms"`
	ElapsedMS     *int64 `json:"elapsed_ms"`
	LeasedUntilMS *int64 `json:"leased_until_ms"`
	LeaseLeftMS   *int64 `json:"lease_left_ms"`
	LeaseNoExpiry bool   `json:"lease_no_expiry"`
	LogPath       string `json:"log_path"`
	// Spend is what the run's own stream reports so far: its finished turns, tokens and
	// cost (liveSpend). The store holds a run's spend only once it has ended.
	Spend        liveSpend         `json:"spend"`
	LoopRecovery *loopRecoveryView `json:"loop_recovery,omitempty"`
}

// finishedRun is one terminal run with the outcome a person acts on: landed at a sha, a
// gate failure, an exit, or a wall. LandedSHA is filled only when the outcome is a
// landing, so a client never has to parse the outcome string to find the commit.
type finishedRun struct {
	RunID        string   `json:"run_id"`
	ItemID       string   `json:"item_id"`
	LaneID       string   `json:"lane_id"`
	Title        string   `json:"title"`
	Seat         string   `json:"seat,omitempty"`
	Workflow     string   `json:"workflow,omitempty"`
	Node         string   `json:"node,omitempty"`
	State        string   `json:"state"`
	ExitCode     *int     `json:"exit_code"`
	Outcome      string   `json:"outcome"`
	LandedSHA    string   `json:"landed_sha,omitempty"`
	StartedAtMS  int64    `json:"started_at_ms"`
	FinishedAtMS int64    `json:"finished_at_ms"`
	DurationMS   int64    `json:"duration_ms"`
	CostUSD      *float64 `json:"cost_usd"`
	UsageTokens  *int64   `json:"usage_tokens"`
	// EndCause is the runner's recorded end cause, empty when the store has none.
	// RepeatingCall is the call pi ended a `repeating-call` session for.
	// LoopRecovery summarizes any in-run loop recoveries pi performed during the session.
	EndCause      string             `json:"end_cause,omitempty"`
	RepeatingCall *repeatingCallView `json:"repeating_call,omitempty"`
	LoopRecovery  *loopRecoveryView  `json:"loop_recovery,omitempty"`
}

// repeatingCallView is the identical call a `repeating-call` run kept making, as the
// runner recorded it from pi's `loop_detected` event.
type repeatingCallView struct {
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Streak int    `json:"streak"`
	Shape  string `json:"shape,omitempty"`
}

// gateVerdict is the gate's own answer for one run, joined to the landing attempt by the
// verdict id: the attempt names the verdict it evaluated, and the verdict event names the
// run it judged. That pair is what makes "landed" a fact about this run rather than about
// its item.
type gateVerdict struct {
	RunID     string
	VerdictID string
	Result    string
}

// runningFace is the view.running face.
func runningFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("running")
	storePath := fs.String("store", "", "the store to read (required)")
	loopDir := fs.String("loop-dir", "", "the loop's record directory (default: <store dir>/loop)")
	laneRoot := fs.String("lane-root", "", "where the lane directories live (default: <store dir>/lanes)")
	watch := fs.Bool("watch", false, "re-render once a second in a terminal")
	asJSON := fs.Bool("json", false, "emit the record envelope instead of the table")
	nowFlag := fs.String("now", "", "the clock elapsed and lease-left are measured against, RFC 3339 (default: now)")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *storePath == "" {
		return record.Envelope{}, usagef("running needs --store <path>")
	}
	now := time.Now().UTC()
	if *nowFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return record.Envelope{}, usagef("--now %q is not an RFC 3339 timestamp: %v", *nowFlag, err)
		}
		now = parsed.UTC()
	}
	if *loopDir == "" {
		*loopDir = filepath.Join(filepath.Dir(*storePath), "loop")
	}
	if *laneRoot == "" {
		*laneRoot = filepath.Join(filepath.Dir(*storePath), "lanes")
	}

	render := func(at time.Time) (record.Envelope, int) {
		payload, err := readRunning(context.Background(), *storePath, *loopDir, *laneRoot, at)
		if err != nil {
			var mismatch schemaMismatch
			if errors.As(err, &mismatch) {
				fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
				return envelope("view.running", refusal{
					GeneratedAt: at,
					Snapshot:    snapshot{SchemaVersion: mismatch.version, Source: sourceStore},
				}), record.ExitIncompatible
			}
			return record.Envelope{}, exitFor(err)
		}
		return envelope("view.running", payload), record.ExitOK
	}

	if *watch {
		return record.Envelope{}, watchRunning(time.Second, func() int {
			at := time.Now().UTC()
			if *nowFlag != "" {
				at = now
			}
			env, code := render(at)
			if code != record.ExitOK {
				return code
			}
			if *asJSON {
				raw, err := record.Encode(env)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
					return record.ExitInternal
				}
				fmt.Println(string(raw))
				return record.ExitOK
			}
			var payload runningPayload
			if err := json.Unmarshal(env.Payload, &payload); err != nil {
				fmt.Fprintf(os.Stderr, "tillr-view: reading the running payload: %v\n", err)
				return record.ExitInternal
			}
			renderRunning(os.Stdout, payload)
			return record.ExitOK
		})
	}

	env, code := render(now)
	if code != record.ExitOK || *asJSON {
		return env, code
	}
	var payload runningPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return record.Envelope{}, usagef("reading the running payload: %v", err)
	}
	renderRunning(os.Stdout, payload)
	return record.Envelope{}, record.ExitOK
}

// readRunning reads the store and the loop's records into one payload. The store read is
// one transaction, so the lanes, the runs and the allocations describe the same moment;
// the loop result is a file beside the store, and the stale test joins it to that same
// transaction's event head.
func readRunning(ctx context.Context, storePath, loopDir, laneRoot string, now time.Time) (runningPayload, error) {
	obs := &observations{loopDir: loopDir}
	stop := obs.newestLoopResult()
	repeated := obs.repeatedLoopStop()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return runningPayload{}, err
	}
	defer s.Close()

	var out runningPayload
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		head, err := r.EventHeadSeq()
		if err != nil {
			return err
		}
		domain, err := readStoreDomainIn(r, loopDir)
		if err != nil {
			return err
		}
		dispatched, err := dispatchSeats(r)
		if err != nil {
			return err
		}
		if domain != nil {
			for i := range domain.runs {
				if domain.runs[i].Seat == "" {
					domain.runs[i].Seat = dispatched[domain.runs[i].RunID]
				}
			}
			for id, alloc := range domain.allocations {
				if alloc.Seat == "" {
					if seat := dispatched[id]; seat != "" {
						alloc.Seat = seat
						domain.allocations[id] = alloc
					}
				}
			}
		}
		verdicts, err := readGateVerdicts(r)
		if err != nil {
			return err
		}
		// The stop record is stale when the newest event is newer than the file. The
		// first event after the file is the newer run's first record, and naming that
		// seq is what lets an operator go and read it.
		staleSeq := int64(0)
		if !stop.Env.CreatedAt.IsZero() {
			_ = r.QueryRow(`SELECT seq FROM events WHERE ts > ? ORDER BY seq LIMIT 1`,
				stop.Env.CreatedAt.UnixMilli()).Scan(&staleSeq)
		}
		out = buildRunningPayload(now, snapshot{
			SchemaVersion: version,
			EventHeadSeq:  head,
			Source:        sourceStore,
		}, domain, verdicts, stop, staleSeq, laneRoot)
		out.Grants, err = waitingGrantLines(r, now, storePath)
		if err != nil {
			return err
		}
		session, active, note, err := readLoopSession(r, now)
		if err != nil {
			return err
		}
		if active {
			out.Loop = runningLoop{}
		}
		out.Loop.Session = session
		out.Loop.Active = active
		out.Loop.SessionNote = note
		out.Loop.RepeatedStop = repeated
		return readFinishedEndings(r, domain, out.Finished)
	})
	if err != nil {
		return runningPayload{}, err
	}
	// The live runs' spend and in-run loop recoveries are in their logs, read after the store's transaction.
	for i := range out.Active {
		out.Active[i].Spend, out.Active[i].LoopRecovery = readLiveRunStream(out.Active[i].LogPath)
	}
	return out, nil
}

// readFinishedEndings fills each finished row's end cause, any recorded in-run loop
// recovery from its `run.finish` event, and for a `repeating-call` run the call it
// repeated, which replaces a bare "exited 0" outcome: such a run exits like any ended
// run, and its cause is what an operator needs to see. An older store without the column
// answers no cause.
func readFinishedEndings(r *store.Reader, domain *storeDomain, finished []finishedRun) error {
	seqs := map[string]int64{}
	if domain != nil {
		for _, run := range domain.runs {
			seqs[run.RunID] = run.EventSeq
		}
	}
	for i := range finished {
		var cause sql.NullString
		err := r.QueryRow(`SELECT end_cause FROM runs WHERE run_id = ?`, finished[i].RunID).Scan(&cause)
		if err != nil && !isMissingColumn(err) && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			finished[i].EndCause = cause.String
		}
		seq := seqs[finished[i].RunID]
		if seq > 0 {
			var payload string
			err = r.QueryRow(`SELECT payload FROM events WHERE seq = ? AND kind = 'run.finish'`, seq).Scan(&payload)
			if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingTable(err) {
				return err
			}
			var body struct {
				RepeatingCall *repeatingCallView `json:"repeating_call"`
				LoopRecovery  *loopRecoveryView  `json:"loop_recovery"`
			}
			if payload != "" && json.Unmarshal([]byte(payload), &body) == nil {
				finished[i].LoopRecovery = body.LoopRecovery
				if cause.String == repeatingCallCause {
					finished[i].RepeatingCall = body.RepeatingCall
				}
			}
		}
		if cause.String == repeatingCallCause {
			if strings.HasPrefix(finished[i].Outcome, "exited") || finished[i].Outcome == finished[i].State {
				finished[i].Outcome = repeatingOutcome(finished[i].RepeatingCall)
			}
		}
	}
	return nil
}

// repeatingCallCause is tillr-run's end cause for a session pi ended because the model
// kept repeating one identical tool call.
const repeatingCallCause = "repeating-call"

func repeatingOutcome(call *repeatingCallView) string {
	if call == nil {
		return repeatingCallCause
	}
	name := call.Tool
	if name == "" {
		name = call.Shape
	}
	if name == "" {
		return repeatingCallCause
	}
	return fmt.Sprintf("%s: %s ×%d", repeatingCallCause, name, call.Streak)
}

// buildRunningPayload projects the store's rows and the loop's record into the view's
// three blocks. It is a pure function of its arguments so the projection can be tested
// without a database.
func buildRunningPayload(now time.Time, snap snapshot, domain *storeDomain, verdicts map[string]gateVerdict, stop loopResult, staleSeq int64, laneRoot string) runningPayload {
	out := runningPayload{
		GeneratedAt: now,
		Snapshot:    snap,
		Active:      []runningRun{},
		Finished:    []finishedRun{},
		Lanes:       runningLanes{Idle: []string{}},
	}
	if domain == nil {
		return out
	}
	places := itemPlaces(domain.items)
	items := map[string]storeItem{}
	for _, it := range domain.items {
		items[it.ItemID] = it
	}
	for _, lane := range domain.lanes {
		out.Lanes.Total++
		if lane.State == laneStateLeased {
			out.Lanes.Leased++
			continue
		}
		out.Lanes.Idle = append(out.Lanes.Idle, lane.LaneID)
	}

	// liveRuns answers the leases no row describes yet, and marks in place a row whose run
	// a lease is continuing (a resumed run keeps its earlier row): both are live.
	live := liveRuns(domain.lanes, domain.runs, domain.allocations)
	for _, run := range domain.runs {
		if run.State == liveRunState {
			live = append(live, run)
		}
	}
	for _, live := range live {
		item := items[live.ItemID]
		row := runningRun{
			LaneID:      live.LaneID,
			RunID:       live.RunID,
			ItemID:      live.ItemID,
			Place:       places[item.ItemID],
			Title:       item.Title,
			Seat:        live.Seat,
			Workflow:    live.Workflow,
			Node:        live.Node,
			StartedAtMS: live.StartedAtMS,
			LogPath:     runLogPath(laneRoot, live.LaneID, live.RunID),
		}
		if live.StartedAtMS > 0 {
			elapsed := now.UnixMilli() - live.StartedAtMS
			if elapsed < 0 {
				elapsed = 0
			}
			row.ElapsedMS = &elapsed
		}
		for _, lane := range domain.lanes {
			if lane.LaneID != live.LaneID || lane.State != laneStateLeased || lane.RunID != live.RunID {
				continue
			}
			row.LeaseNoExpiry = lane.LeasedUntilMS == nil
			row.LeasedUntilMS = lane.LeasedUntilMS
			if lane.LeasedUntilMS != nil {
				left := *lane.LeasedUntilMS - now.UnixMilli()
				row.LeaseLeftMS = &left
			}
		}
		out.Active = append(out.Active, row)
	}

	candidates := map[string]storeCandidate{}
	for _, c := range domain.candidates {
		candidates[c.CandidateID] = c
	}
	landed := map[string]storeAttempt{}
	for _, attempts := range domain.attempts {
		for _, a := range attempts {
			if a.Result == "landed" {
				landed[a.VerdictID] = a
			}
		}
	}

	finished := make([]storeRun, 0, len(domain.runs))
	for _, run := range domain.runs {
		if run.State == liveRunState || run.FinishedAtMS == 0 {
			continue
		}
		finished = append(finished, run)
	}
	sort.SliceStable(finished, func(i, j int) bool {
		if finished[i].FinishedAtMS != finished[j].FinishedAtMS {
			return finished[i].FinishedAtMS > finished[j].FinishedAtMS
		}
		return finished[i].RunID > finished[j].RunID
	})
	for _, run := range finished {
		if len(out.Finished) >= runningFinishedLimit {
			break
		}
		item := items[run.ItemID]
		outcome, sha := runOutcome(run, verdicts, landed, candidates)
		duration := run.FinishedAtMS - run.StartedAtMS
		if duration < 0 {
			duration = 0
		}
		out.Finished = append(out.Finished, finishedRun{
			RunID:        run.RunID,
			ItemID:       run.ItemID,
			LaneID:       run.LaneID,
			Title:        item.Title,
			Seat:         run.Seat,
			Workflow:     run.Workflow,
			Node:         run.Node,
			State:        run.State,
			ExitCode:     run.ExitCode,
			Outcome:      outcome,
			LandedSHA:    sha,
			StartedAtMS:  run.StartedAtMS,
			FinishedAtMS: run.FinishedAtMS,
			DurationMS:   duration,
			CostUSD:      run.CostUSD,
			UsageTokens:  run.UsageTokens,
		})
	}

	out.Loop = runningLoop{
		StopReason: stop.Stop,
		StopNote:   stop.Note,
		RecordPath: stop.Path,
	}
	if !stop.Env.CreatedAt.IsZero() {
		at := stop.Env.CreatedAt
		out.Loop.RecordedAt = &at
	}
	if staleSeq > 0 {
		out.Loop.Stale = true
		out.Loop.StaleSeq = staleSeq
		out.Loop.StaleNote = fmt.Sprintf("stale stop record — newer run exists at seq %d", staleSeq)
	}
	return out
}

// runOutcome answers what a person needs to know about a finished run: whether the tree
// it produced landed and where, whether the gate judged it and said no, or how it ended.
// The gate event is keyed by run and the landing attempt by verdict, so the join is the
// verdict id and never the item: a run whose item landed later does not borrow that
// landing.
func runOutcome(run storeRun, verdicts map[string]gateVerdict, landed map[string]storeAttempt, candidates map[string]storeCandidate) (string, string) {
	switch run.State {
	case "timed-out":
		return "timed-out", ""
	case "failed-to-start":
		return "failed-to-start", ""
	}
	if verdict, ok := verdicts[run.RunID]; ok {
		switch verdict.Result {
		case "fail":
			return "gate-failed", ""
		case "pass":
			if attempt, ok := landed[verdict.VerdictID]; ok {
				sha := attempt.TrunkAfter
				if sha == "" {
					sha = candidates[attempt.CandidateID].HeadSHA
				}
				if sha == "" {
					return "landed", ""
				}
				return "landed " + sha, sha
			}
		}
	}
	if run.ExitCode != nil {
		return fmt.Sprintf("exited %d", *run.ExitCode), ""
	}
	if run.State != "" {
		return run.State, ""
	}
	return "exited", ""
}

// readGateVerdicts answers the gate's own answer per run from the event log. A store with
// no events table answers none rather than failing: the gate's judgment is the join's
// evidence, and its absence is an outcome the view names.
func readGateVerdicts(r *store.Reader) (map[string]gateVerdict, error) {
	rows, err := r.Query(`SELECT COALESCE(object_id, ''), payload FROM events WHERE kind = ? AND object_type = 'run' ORDER BY seq`, "gate.verdict")
	if err != nil {
		if isMissingTable(err) {
			return map[string]gateVerdict{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := map[string]gateVerdict{}
	for rows.Next() {
		var runID, payload string
		if err := rows.Scan(&runID, &payload); err != nil {
			return nil, err
		}
		var body struct {
			RunID     string `json:"run_id"`
			VerdictID string `json:"verdict_id"`
			Result    string `json:"result"`
		}
		if err := json.Unmarshal([]byte(payload), &body); err != nil {
			continue
		}
		if runID == "" {
			runID = body.RunID
		}
		if runID == "" {
			continue
		}
		out[runID] = gateVerdict{RunID: runID, VerdictID: body.VerdictID, Result: body.Result}
	}
	return out, rows.Err()
}

// renderRunning writes the three blocks. The header is one line because it is the line an
// operator reads first; the stale sentence is beside it because a stop record that is no
// longer the newest record is the failure this view exists to make visible.
func renderRunning(w io.Writer, p runningPayload) {
	fmt.Fprintf(w, "lanes leased: %d/%d · %s\n", p.Lanes.Leased, p.Lanes.Total, loopLine(p))
	for _, g := range p.Grants {
		fmt.Fprintf(w, "%s\n", g.Summary)
	}
	if p.Loop.Stale {
		fmt.Fprintf(w, "%s\n", p.Loop.StaleNote)
	}
	if p.Loop.RepeatedStop != nil {
		fmt.Fprintf(w, "%s\n", repeatedStopLine(p.Loop.Active, p.Loop.RepeatedStop))
	}
	fmt.Fprintln(w)

	if len(p.Active) == 0 {
		fmt.Fprintf(w, "0 live runs; %s\n", idleLine(p.Lanes.Idle))
	} else {
		fmt.Fprintf(w, "LIVE RUNS (%d)\n", len(p.Active))
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "LANE\tITEM\t#\tTITLE\tSEAT\tELAPSED\tTURNS\tSPENT\tRECOVERIES\tLEASE LEFT\tLOG")
		for _, run := range p.Active {
			lease := leaseLeftText(run.LeaseLeftMS)
			if run.LeaseNoExpiry {
				lease = "lease has no expiry"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				orDash(run.LaneID), orDash(run.ItemID), placeText(run.Place), orDash(run.Title), orDash(run.Seat),
				elapsedText(run.ElapsedMS), turnsText(run.Spend.Turns), costText(run.Spend.CostUSD), loopRecoveryText(run.LoopRecovery), lease, orDash(run.LogPath))
		}
		tw.Flush()
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "FINISHED (last %d)\n", runningFinishedLimit)
	if len(p.Finished) == 0 {
		fmt.Fprintln(w, "no finished runs in the store")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tITEM\tSEAT\tOUTCOME\tRECOVERIES\tDURATION\tCOST")
	for _, run := range p.Finished {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			orDash(run.RunID), orDash(run.ItemID), orDash(run.Seat), run.Outcome, loopRecoveryText(run.LoopRecovery),
			spanText(run.DurationMS), costText(run.CostUSD))
	}
	tw.Flush()
}

func loopRecoveryText(r *loopRecoveryView) string {
	if r == nil || r.Count <= 0 {
		return "—"
	}
	if r.Shape != "" {
		return fmt.Sprintf("%d× (%s, %d→%d tok)", r.Count, r.Shape, r.TokensBefore, r.TokensAfter)
	}
	return fmt.Sprintf("%d× (%d→%d tok)", r.Count, r.TokensBefore, r.TokensAfter)
}

// loopLine names the live session's clocks, or the historical stop evidence.
func loopLine(p runningPayload) string {
	if p.Loop.Active && p.Loop.Session != nil {
		s := p.Loop.Session
		return fmt.Sprintf("loop: live session %s (started %s, last heartbeat %s)", s.SessionID,
			time.UnixMilli(s.StartedAtMS).UTC().Format(time.RFC3339Nano),
			time.UnixMilli(s.RefreshedAtMS).UTC().Format(time.RFC3339Nano))
	}
	if p.Loop.StopReason != "" {
		if p.Loop.RecordedAt != nil {
			return fmt.Sprintf("loop: %s (at %s)", p.Loop.StopReason, p.Loop.RecordedAt.UTC().Format(time.RFC3339))
		}
		return "loop: " + p.Loop.StopReason
	}
	if p.Loop.StopNote != "" {
		return "loop: " + p.Loop.StopNote
	}
	return "loop: no stop record"
}

func repeatedStopLine(active bool, rs *repeatedStop) string {
	cause := rs.Reason
	if rs.Detail != "" && rs.Detail != rs.Reason {
		cause += ": " + rs.Detail
	}
	firstAt := time.UnixMilli(rs.FirstAtMS).UTC().Format(time.RFC3339)
	lastAt := time.UnixMilli(rs.LastAtMS).UTC().Format(time.RFC3339)
	prefix := "stuck drain"
	if active {
		prefix = "stuck drain (restarted after repeated exits)"
	}
	return fmt.Sprintf("%s: loop exited %d times in a row (%s, %s..%s). Next: %s",
		prefix, rs.Count, cause, firstAt, lastAt, rs.Next)
}

// idleLine names the lanes a stopped board is not using. The lane list is the answer to
// "then what is idle", and an empty one says no lane is.
func idleLine(idle []string) string {
	if len(idle) == 0 {
		return "no lane is idle"
	}
	return "lanes idle: " + joinComma(idle)
}

func joinComma(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ", "
		}
		out += value
	}
	return out
}

func orDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

// elapsedText is the viewer's clock reading for a run that has not finished, or the
// absence when the store recorded no start.
func elapsedText(ms *int64) string {
	if ms == nil {
		return "no start recorded"
	}
	return spanText(*ms)
}

// turnsText is a live run's finished turns, or the absence when its log was not read.
func turnsText(turns *int) string {
	if turns == nil {
		return "—"
	}
	return strconv.Itoa(*turns)
}

// leaseLeftText is the lease's own clock: left, expired, or not recorded.
func leaseLeftText(ms *int64) string {
	if ms == nil {
		return "lease not recorded"
	}
	if *ms < 0 {
		return "expired " + spanText(-*ms) + " ago"
	}
	return spanText(*ms) + " left"
}

// spanText is the design's duration format: `12s`, `4m07s`, `1h12m`.
func spanText(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	total := ms / 1000
	if total < 60 {
		return strconv.FormatInt(total, 10) + "s"
	}
	minutes := total / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%02ds", minutes, total%60)
	}
	hours := minutes / 60
	return fmt.Sprintf("%dh%02dm", hours, minutes%60)
}

// costText is a run's reported cost, or the absence: a backend that reports no price is
// not a run that was free.
func costText(cost *float64) string {
	if cost == nil {
		return "—"
	}
	return fmt.Sprintf("$%.2f", *cost)
}

// watchRunning re-renders until render answers a non-OK code or the process is signalled.
// It clears a terminal between renders and never clears a pipe: a watch written to a file
// is a sequence of renders, not a screen.
func watchRunning(interval time.Duration, render func() int) int {
	if code := render(); code != record.ExitOK {
		return code
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	terminal := isCharDevice(os.Stdout)
	for {
		select {
		case <-signals:
			return record.ExitOK
		case <-ticker.C:
			if terminal {
				// Cursor home plus clear: the same two escapes a pager uses, so a
				// redirected run keeps every render instead of overwriting its own log.
				fmt.Fprint(os.Stdout, "\033[H\033[2J")
			}
			if code := render(); code != record.ExitOK {
				return code
			}
		}
	}
}

// isCharDevice answers whether f is a terminal. The watch loop's clear is a screen
// operation, and a pipe is not a screen.
func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
