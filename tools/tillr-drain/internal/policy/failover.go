// Reference: docs/reference/tillr-muse-backend.md
package policy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// AttemptClass is the driver's classification of one terminal run: the one fact a failover
// turns on (docs/reference/tillr-muse-backend.md, D-MM11).
//
// Five classes exist, and only AttemptInfrastructure fails over:
//
//   - AttemptInterrupted — external SIGTERM or SIGINT stopped the run; work is preserved
//     without closing the backend or failing over.
//   - AttemptInfrastructure — the attempt produced no judgable candidate because the
//     harness or the model route failed, so nothing about the item has been learned. The
//     harness could not start, or died without ever writing a report. This is machinery:
//     the backend closes until recovery evidence arrives and open backends pick up the work.
//   - AttemptRefused — the adapter exited 2 with its refusal prefix before any model ran;
//     the item is held for the operator without closing the backend or failing over.
//   - AttemptAnswered — the run answered: it exited cleanly as a command/ended session, or
//     wrote a report whose footer says the model's own `done`/`blocked`/`failed`/`trunk-broken`.
//     A failing test, an empty diff, an out-of-fence write and a red gate are all *this*
//     class, because they are statements about the work; spending a second backend on them
//     would pay twice to learn what the gate already said once.
//   - AttemptUnjudged — a wall cut the run short: `run exec`'s own timeout, or the
//     adapter's inner `timeout` exiting 124. A tree exists that may hold half-finished
//     work, and no report says anything about the item, so the run is inspected before
//     anything re-dispatches. It is deliberately NOT infrastructure: a candidate that runs
//     long and is killed mid-work must not be read as broken machinery, because that is
//     how one long item silently spends a second backend's budget on the same work.
type AttemptClass string

// The attempt classes.
const (
	// AttemptInfrastructure is a run that failed for the machinery's reasons and learned
	// nothing about the item: the one class a failover fires on.
	// AttemptInterrupted is a supervisor interruption, not an item outcome.
	AttemptInterrupted    AttemptClass = "interrupted"
	AttemptInfrastructure AttemptClass = "infrastructure"
	// AttemptRefused is an adapter's own preflight refusal: it printed `<adapter>: refused:
	// <reason>` and exited 2 before any model ran. The brief or the environment is what is
	// wrong, so the same brief on another backend would be refused for the same reason: the
	// item is held for the operator with the adapter's words, no backend is closed and no
	// failover runs.
	AttemptRefused AttemptClass = "refused"
	// AttemptAnswered is a run that produced an answer, however disappointing: judged
	// work, which never fails over.
	AttemptAnswered AttemptClass = "answered"
	// AttemptUnjudged is a run a wall cut short: unjudged work, inspected rather than
	// re-dispatched.
	AttemptUnjudged AttemptClass = "unjudged"
	// AttemptRepeating is a run whose session pi ended for a repeating call. It is not
	// infrastructure (it never closes a backend) and not an answer.
	AttemptRepeating AttemptClass = "repeating-call"
)

// The exit codes the classification reads. They are the adapters' documented contract, not
// a harness's habit: every adapter here exits 2 for a preflight refusal that ran nothing
// (`require_opt`, `require_harness`, `preflight_brief`) and 124 when its inner `timeout`
// killed the harness at the wall (tools/tillr-run/backends/*/run-*).
const (
	// AdapterRefusedExit is the adapters' "refusal that ran nothing" exit: no process
	// reached the harness, no tokens were spent, nothing was learned.
	AdapterRefusedExit = 2
	// AdapterWallExit is the exit `timeout` gives a harness it killed at the wall.
	AdapterWallExit = 124
)

// ReportFooter is the marker every adapter's report ends with (design §3, section 8): the
// model's own answer to whether it finished. Its presence is what tells a harness that
// answered and then exited non-zero from one that died before writing anything.
const ReportFooter = "RESULT:"

// ClassifyAttempt answers what one terminal run's result classifies as. log is the run's
// log text — the adapter's stream as `run exec` captured it — and may be empty when the
// store names no log or the log is gone, which is treated as "no report".
//
// The order of the tests is the classification: a run cut short by a wall is judged before
// the exit code is looked at, because a candidate that times out mid-run leaves a non-zero
// exit and no footer and would otherwise be indistinguishable from a harness that never
// started.
func ClassifyAttempt(rec run.Record, log string) AttemptClass {
	if rec.Backend == "command" {
		if rec.State == run.StateExited && rec.ExitCode != nil && (*rec.ExitCode == 0 || *rec.ExitCode == 1) {
			return AttemptAnswered
		}
		return AttemptInfrastructure
	}
	switch rec.State {
	case run.StateFailedToStart:
		// There was no process: the CLI could not start (binary absent, auth expired),
		// which design §6 classifies as an environment defect rather than an attempt.
		return AttemptInfrastructure
	case run.StateTimedOut:
		return AttemptUnjudged
	case run.StateExited:
		code := 0
		if rec.ExitCode != nil {
			code = *rec.ExitCode
		}
		switch {
		case code == 0:
			return AttemptAnswered
		case code == AdapterWallExit:
			// The adapter's own wall: the harness was killed, the tree is unjudged.
			return AttemptUnjudged
		case rec.EndCause != run.EndSettledIdle && rec.EndCause != run.EndRepeatingCall && (rec.EndCause == run.EndSignal || ((code == 143 || code == 130) && rec.EndingException == "" && !ReadableFooter(ReportFooterWord(log)))):
			// An external SIGTERM/SIGINT is a supervisor or operator interruption,
			// not evidence about the backend or the item.
			return AttemptInterrupted
		case code == AdapterRefusedExit:
			// The adapter's preflight refusal: nothing ran, nothing was spent. One that
			// states its words is about the brief or the environment, and is the operator's.
			if _, ok := RecordAdapterRefusal(rec, log); ok {
				return AttemptRefused
			}
			return AttemptInfrastructure
		case strings.Contains(log, ReportFooter):
			// The harness answered and then exited non-zero. The answer is judged work,
			// whatever it says.
			return AttemptAnswered
		default:
			// A harness that died with no report at all: no judgable candidate, so
			// nothing about the item has been learned.
			return AttemptInfrastructure
		}
	}
	// A state this face does not know is not evidence that the run answered, and treating
	// it as infrastructure would re-dispatch on a guess. Answered is the direction that
	// spends nothing.
	return AttemptAnswered
}

// RecordAdapterRefusal answers the adapter's preflight refusal words for rec, checking
// log first (usually the extracted report) and falling back to the raw stream log
// artifact when present. A preflight refusal prints `run-<adapter>: refused: <reason>`
// to stderr before any harness JSON stream starts, so the dialect's extracted report.md
// has an empty body while the raw log carries the refusal line.
func RecordAdapterRefusal(rec run.Record, log string) (string, bool) {
	if words, ok := AdapterRefusal(log); ok {
		return words, true
	}
	if raw := RawLogTail(rec.ArtifactRefs); raw != "" {
		return AdapterRefusal(raw)
	}
	return "", false
}

// AdapterRefusal answers the adapter's own refusal words from a run's log: the text after
// the last `run-<adapter>: refused: ` marker that backends/preflight.sh prints (`refuse`). It
// reports false when the log carries none, which is how an exit 2 from anything other than
// an adapter's preflight stays what it was.
func AdapterRefusal(log string) (string, bool) {
	const marker = ": refused: "
	words := ""
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimRight(line, "\r")
		idx := strings.Index(line, marker)
		if idx <= 0 || !strings.HasPrefix(line, "run-") || strings.ContainsAny(line[:idx], " \t") {
			continue
		}
		words = strings.TrimSpace(line[idx+len(marker):])
	}
	return words, words != ""
}

// ReportFooterWord answers the model's own answer from a run's report footer: the word after
// the last `RESULT:` marker, lowercased, or "" when the log carries none. It is the one
// place the loop reads the model's answer rather than the attempt's class: a run that says
// `blocked` produced a judgable report and no candidate to judge, which is a fact about the
// item rather than about the code.
//
// The word is read through Markdown emphasis and code markers and a closing full stop, so
// `**RESULT: done**` and "RESULT: done." both answer "done": a model that bolds its
// footer still answered, and reading the markers as part of the word is what held the
// 2026-11-18 triage item at read-9.
func ReportFooterWord(log string) string {
	idx := strings.LastIndex(log, ReportFooter)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(log[idx+len(ReportFooter):])
	if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.ToLower(strings.Trim(rest, "*_`. \t"))
}

// ReadableFooter reports whether a footer names an ending a workflow node's contract
// knows. Anything else is no answer the loop can read, the same fact as no footer.
func ReadableFooter(footer string) bool {
	switch footer {
	case "done", "failed", "blocked", "trunk-broken":
		return true
	}
	return false
}

// UnreadFooterReason describes an unread RESULT footer for the restarted run's brief.
func UnreadFooterReason(footer string) string {
	return fmt.Sprintf("report ended with unread %s %q (accepted: done, failed, blocked, trunk-broken)", ReportFooter, footer)
}

// PriorRun is one terminal run the store holds for an item, as the planner reads it.
type PriorRun struct {
	RunID   string
	Backend string
	Class   AttemptClass
	// BackendFailure distinguishes machinery closure from quota and other
	// infrastructure classifications answered by their own rule.
	BackendFailure bool
	// TransportError marks a design run whose pi log ended on a model or transport
	// error after pi's automatic retries failed.
	TransportError bool
	// Restaged marks an interruption the loop made itself: the run was staged on a
	// base that lacked what made its item ready (drain.stale-base).
	Restaged bool
	// Repeated is the recorded repeating-call ending of a run whose class is
	// AttemptRepeating, with the route the settlement chose for it.
	Repeated *runhistory.RepeatedCall
	// State, EndCause, Finished, Node and NodeOutcome are what the run's own records say,
	// kept so a hold the planner cannot classify can name what was found (retryHoldReason).
	// Finished is true when a run finish record exists; Node is empty for a run that is no
	// workflow node.
	State       string
	EndCause    string
	Finished    bool
	Node        string
	NodeOutcome string
}

// ReadPriorRunsWith reads every item's terminal runs, oldest first, keyed by item id.
// Callers supply outcomes (from cache or fresh read) and an optional design-transport
// classifier for unsaved or infrastructure endings.
func ReadPriorRunsWith(
	r *store.Reader,
	outcomes []BackendOutcome,
	latestFinished map[string]string,
	designTransportErr func(rec run.Record, saved bool, class AttemptClass) (bool, error),
) (map[string][]PriorRun, error) {
	interrupted, restaged, err := ReadInterruptedRuns(r)
	if err != nil {
		return nil, err
	}
	repeated, err := runhistory.ReadRepeatedCalls(r)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	type disposition struct {
		class         AttemptClass
		closes, saved bool
	}
	byID := make(map[string]disposition, len(outcomes))
	for _, o := range outcomes {
		if _, seen := byID[o.Rec.RunID]; !seen {
			byID[o.Rec.RunID] = disposition{o.Class, o.Closes, o.Saved}
		}
	}
	judged, endings, finishPayloads, err := ReadRunJudgmentsAndEndings(r)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	var hasNodes bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil {
		return nil, storelog.ReadFailure(err)
	}
	isNode := `0`
	if hasNodes {
		isNode = `EXISTS (SELECT 1 FROM node_runs n WHERE n.run_id=runs.run_id)`
	}
	type nodeRecord struct{ node, outcome string }
	nodeOf := map[string]nodeRecord{}
	if hasNodes {
		nodeRows, err := r.Query(`SELECT run_id, node_id, outcome FROM node_runs`)
		if err != nil {
			return nil, storelog.ReadFailure(err)
		}
		for nodeRows.Next() {
			var id string
			var rec nodeRecord
			if err := nodeRows.Scan(&id, &rec.node, &rec.outcome); err != nil {
				nodeRows.Close()
				return nil, storelog.ReadFailure(err)
			}
			nodeOf[id] = rec
		}
		if err := nodeRows.Err(); err != nil {
			nodeRows.Close()
			return nil, storelog.ReadFailure(err)
		}
		nodeRows.Close()
	}
	rows, err := r.Query(
		`SELECT item_id, run_id, backend, state, exit_code, COALESCE(end_cause, ''), artifact_refs, event_seq, started_at, rowid, ` + isNode + `
		   FROM runs WHERE item_id IS NOT NULL`)
	if err != nil && strings.Contains(err.Error(), "no such column: end_cause") {
		rows, err = r.Query(
			`SELECT item_id, run_id, backend, state, exit_code, '', artifact_refs, event_seq, started_at, rowid, ` + isNode + `
			   FROM runs WHERE item_id IS NOT NULL`)
	}
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	defer rows.Close()
	type priorRow struct {
		itemID       string
		rec          run.Record
		exitCode     sql.NullInt64
		artifactRefs string
		order        int64
		startedAt    int64
		rowid        int64
	}
	type latestRow struct {
		runID     string
		terminal  bool
		order     int64
		startedAt int64
		rowid     int64
	}
	newest := map[string]latestRow{}
	var ordered []priorRow
	for rows.Next() {
		var row priorRow
		var node bool
		if err := rows.Scan(&row.itemID, &row.rec.RunID, &row.rec.Backend, &row.rec.State, &row.exitCode, &row.rec.EndCause, &row.artifactRefs, &row.order, &row.startedAt, &row.rowid, &node); err != nil {
			return nil, storelog.ReadFailure(err)
		}
		if seq, ended := endings[row.rec.RunID]; ended {
			row.order = seq
		}
		if latestFinished != nil {
			cand := latestRow{
				runID:     row.rec.RunID,
				terminal:  runhistory.TerminalRunStates[row.rec.State],
				order:     row.order,
				startedAt: row.startedAt,
				rowid:     row.rowid,
			}
			prev, seen := newest[row.itemID]
			if !seen ||
				cand.order > prev.order ||
				(cand.order == prev.order && (cand.startedAt > prev.startedAt || (cand.startedAt == prev.startedAt && cand.rowid > prev.rowid))) {
				newest[row.itemID] = cand
			}
		}
		// Old stores have no workflow tables. Ordinary judges stay excluded there;
		// with node runs, an evaluator is itself a workflow run and may be returned.
		if judged[row.rec.RunID] && !node {
			continue
		}
		ordered = append(ordered, row)
	}
	if err := rows.Err(); err != nil {
		return nil, storelog.ReadFailure(err)
	}
	if latestFinished != nil {
		for itemID, cand := range newest {
			if cand.terminal {
				latestFinished[itemID] = cand.runID
			}
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.itemID != b.itemID {
			return a.itemID < b.itemID
		}
		if a.order != b.order {
			return a.order < b.order
		}
		if a.startedAt != b.startedAt {
			return a.startedAt < b.startedAt
		}
		return a.rowid < b.rowid
	})
	priors := map[string][]PriorRun{}
	for _, row := range ordered {
		itemID, rec, artifactRefs := row.itemID, row.rec, row.artifactRefs
		if row.exitCode.Valid {
			code := int(row.exitCode.Int64)
			rec.ExitCode = &code
		}
		var refs []run.ArtifactRef
		if err := json.Unmarshal([]byte(artifactRefs), &refs); err != nil {
			return nil, drainmodel.Internalf("reading the artifact refs of run %q: %v", rec.RunID, err)
		}
		rec.ArtifactRefs = refs
		ending, measured := byID[rec.RunID]
		isDesignTransportErr := false
		if designTransportErr != nil {
			isDesignTransportErr, err = designTransportErr(rec, ending.saved, ending.class)
			if err != nil {
				return nil, storelog.ReadFailure(err)
			}
		}
		class := ending.class
		if !ending.saved {
			if payload := finishPayloads[rec.RunID]; len(payload) > 0 {
				var terminal run.Record
				if err := json.Unmarshal(payload, &terminal); err != nil {
					return nil, storelog.ReadFailure(err)
				}
				if rec.EndCause == "" {
					rec.EndCause = terminal.EndCause
				}
				if rec.EndingException == "" {
					rec.EndingException = terminal.EndingException
				}
			}
			if isDesignTransportErr {
				class = AttemptInfrastructure
			} else {
				class = ClassifyAttempt(rec, LogTail(refs))
			}
		}
		if interrupted[rec.RunID] {
			class = AttemptInterrupted
		}
		var repeat *runhistory.RepeatedCall
		if rc, ok := repeated[rec.RunID]; ok {
			class, repeat = AttemptRepeating, &rc
		}
		priors[itemID] = append(priors[itemID], PriorRun{
			RunID:          rec.RunID,
			Backend:        rec.Backend,
			Class:          class,
			BackendFailure: class == AttemptInfrastructure && (!measured || ending.closes),
			TransportError: isDesignTransportErr,
			Restaged:       restaged[rec.RunID],
			Repeated:       repeat,
			State:          string(rec.State), EndCause: rec.EndCause,
			Finished: len(finishPayloads[rec.RunID]) > 0,
			Node:     nodeOf[rec.RunID].node, NodeOutcome: nodeOf[rec.RunID].outcome,
		})
	}
	return priors, nil
}

// ReadRunJudgmentsAndEndings answers the runs a judge request names and, for each run that
// ended, the seq of its newest ending before supersession and the payload of its newest
// finish event, read by kind.
func ReadRunJudgmentsAndEndings(r *store.Reader) (map[string]bool, map[string]int64, map[string][]byte, error) {
	const judgeRequestKind = "drain.judge.request"
	events, err := r.EventsOfKind(0, judgeRequestKind, run.FinishKind, runhistory.RunSupersededKind)
	if err != nil {
		return nil, nil, nil, err
	}
	judged, endings, payloads := map[string]bool{}, map[string]int64{}, map[string][]byte{}
	payloadSeq, supersededSeq := map[string]int64{}, map[string]int64{}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		id := *e.ObjectID
		switch e.Kind {
		case judgeRequestKind:
			judged[id] = true
		case runhistory.RunSupersededKind:
			if supersededSeq[id] == 0 {
				supersededSeq[id] = e.Seq
			}
			if _, exists := endings[id]; !exists {
				var p runhistory.RunSuperseded
				if err := json.Unmarshal(e.Payload, &p); err == nil && p.PriorSeq > 0 {
					endings[id] = p.PriorSeq
				} else {
					endings[id] = 0
				}
			}
		case run.FinishKind:
			if e.Seq > payloadSeq[id] {
				payloadSeq[id] = e.Seq
				payloads[id] = e.Payload
			}
			if seq, sup := supersededSeq[id]; sup && e.Seq >= seq {
				continue
			}
			if e.Seq > endings[id] {
				endings[id] = e.Seq
			}
		}
	}
	return judged, endings, payloads, nil
}

// LogTail answers the tail of the pinned extracted report when one exists, otherwise
// the legacy plain-text log. A missing pinned report stays missing rather than reading
// a different run's reusable lane report.
//
// The tail rather than the whole file: the report's footer is the last thing a run writes,
// and a model run's stream is megabytes this decision has no use for.
func LogTail(refs []run.ArtifactRef) string {
	path := ""
	for _, ref := range refs {
		if ref.Kind == "report" {
			path = ref.Ref
			break
		}
	}
	for _, ref := range refs {
		if path != "" {
			break
		}
		if ref.Kind == "log" {
			path = ref.Ref
			break
		}
	}
	if path == "" {
		return ""
	}
	return ReadFileTail(path)
}

func RawLogTail(refs []run.ArtifactRef) string {
	for _, ref := range refs {
		if ref.Kind == "log" {
			return ReadFileTail(ref.Ref)
		}
	}
	return ""
}

func ReadFileTail(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	const tail = 64 << 10
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	if offset := info.Size() - tail; offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return ""
		}
	}
	body, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	return string(body)
}
