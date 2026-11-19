package drain

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// AttemptClass is the driver's classification of one terminal run: the one fact a failover
// turns on (design §2, "Failover: Ember first"; D-MM11).
//
// Three classes, and only the first fails over:
//
//   - AttemptInfrastructure — the attempt produced no judgable candidate because the
//     harness or the model route failed, so nothing about the item has been learned. The
//     adapter refused (it ran nothing), the harness could not start, or the harness died
//     without ever writing a report. This is machinery, and the same brief on another
//     backend costs one attempt rather than one attempt plus a wrong verdict.
//   - AttemptAnswered — the run answered: it exited cleanly, or it wrote a report whose
//     footer says the model's own `done`/`blocked`/`failed`. A failing test, an empty
//     diff, an out-of-fence write and a red gate are all *this* class, because they are
//     statements about the work; spending a second backend on them would pay twice to
//     learn what the gate already said once (design §2, "Judged work never fails over").
//   - AttemptUnjudged — a wall cut the run short: `run exec`'s own timeout, or the
//     adapter's inner `timeout` exiting 124. A tree exists that may hold half-finished
//     work, and no report says anything about the item, so the run is inspected before
//     anything re-dispatches. It is deliberately NOT infrastructure: a candidate that runs
//     long and is killed mid-work must not be read as broken machinery, because that is
//     how one long item silently spends a second backend's budget on the same work.
type AttemptClass string

// The three attempt classes.
const (
	// AttemptInfrastructure is a run that failed for the machinery's reasons and learned
	// nothing about the item: the one class a failover fires on.
	// AttemptInterrupted is a supervisor interruption, not an item outcome.
	AttemptInterrupted    AttemptClass = "interrupted"
	AttemptInfrastructure AttemptClass = "infrastructure"
	// AttemptAnswered is a run that produced an answer, however disappointing: judged
	// work, which never fails over.
	AttemptAnswered AttemptClass = "answered"
	// AttemptUnjudged is a run a wall cut short: unjudged work, inspected rather than
	// re-dispatched.
	AttemptUnjudged AttemptClass = "unjudged"
)

// The exit codes the classification reads. They are the adapters' documented contract, not
// a harness's habit: every adapter here exits 2 for a preflight refusal that ran nothing
// (`require_opt`, `require_harness`, `preflight_brief`) and 124 when its inner `timeout`
// killed the harness at the wall (tools/tillr-run/backends/*/run-*).
const (
	// adapterRefusedExit is the adapters' "refusal that ran nothing" exit: no process
	// reached the harness, no tokens were spent, nothing was learned.
	adapterRefusedExit = 2
	// adapterWallExit is the exit `timeout` gives a harness it killed at the wall.
	adapterWallExit = 124
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
		case code == adapterWallExit:
			// The adapter's own wall: the harness was killed, the tree is unjudged.
			return AttemptUnjudged
		case code == adapterRefusedExit:
			// The adapter's preflight refusal: nothing ran, nothing was spent.
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

// reportFooter answers the model's own answer from a run's report footer: the word after
// the last `RESULT:` marker, lowercased, or "" when the log carries none. It is the one
// place the loop reads the model's answer rather than the attempt's class: a run that says
// `blocked` produced a judgable report and no candidate to judge, which is a fact about the
// item rather than about the code.
//
// The word is read through Markdown emphasis and code markers and a closing full stop, so
// `**RESULT: done**` and "RESULT: done." both answer "done": a model that bolds its
// footer still answered, and reading the markers as part of the word is what held the
// 2026-11-18 triage item at read-9.
func reportFooter(log string) string {
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

// readableFooter reports whether a footer names an ending a workflow node's contract
// knows. Anything else is no answer the loop can read, the same fact as no footer.
func readableFooter(footer string) bool {
	switch footer {
	case "done", "failed", "blocked", "trunk-broken":
		return true
	}
	return false
}

// priorRun is one terminal run the store holds for an item, as the planner reads it.
type priorRun struct {
	RunID   string
	Backend string
	Class   AttemptClass
	// Restaged marks an interruption the loop made itself: the run was staged on a
	// base that lacked what made its item ready (drain.stale-base).
	Restaged bool
	// Repeated is the recorded repeating-call ending of a run whose class is
	// AttemptRepeating, with the route the settlement chose for it.
	Repeated *RepeatedCall
}

// readPriorRuns answers every item's terminal runs, oldest first, keyed by item id.
//
// The runs table is the item→run link (D-UT19), so the planner reads the link rather than
// inferring it from the event log: a run's backend and its exit are what the failover
// decision turns on, and both are columns here.
//
// The log is read for the footer test and for nothing else. It is a store-referenced
// artifact — the record's own artifact_refs name it — so the plan stays a function of the
// store and what the store points at, and a log that is gone reads as "no report" rather
// than as an error.
func readPriorRuns(r *store.Reader) (map[string][]priorRun, error) {
	interrupted, restaged, err := readInterruptedRuns(r)
	if err != nil {
		return nil, err
	}
	repeated, err := readRepeatedCalls(r)
	if err != nil {
		return nil, readFailure(err)
	}
	// Once the loop observes an ending, routing and availability share that
	// saved class; a report edit cannot move the item to a different retry rule.
	outcomes, err := readBackendOutcomes(r)
	if err != nil {
		return nil, err
	}
	classes := map[string]AttemptClass{}
	for _, o := range outcomes {
		if _, seen := classes[o.rec.RunID]; !seen && o.saved {
			classes[o.rec.RunID] = o.class
		}
	}
	// Endings define run order, not mutable row metadata: a usage correction
	// can advance runs.event_seq without ending a run again. Legacy rows with
	// no ending event retain their recorded sequence, timestamp, and row order.
	// Old stores have no workflow tables. Ordinary judges stay excluded there;
	// with node runs, an evaluator is itself a workflow run and may be returned.
	// A run the judge reviewed is excluded, and the rest are ordered by their newest ending.
	// Both facts are read by kind and joined here: a subquery per run row on events by
	// object_id visits every worker record of that run, which cost about 2 seconds a plan
	// on 2026-11-19 (TestNoPassQueryCorrelatesTheEventsWithEachRun).
	judged, endings, err := readRunJudgmentsAndEndings(r)
	if err != nil {
		return nil, readFailure(err)
	}
	var hasNodes bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil {
		return nil, readFailure(err)
	}
	isNode := `0`
	if hasNodes {
		isNode = `EXISTS (SELECT 1 FROM node_runs n WHERE n.run_id=runs.run_id)`
	}
	rows, err := r.Query(
		`SELECT item_id, run_id, backend, state, exit_code, artifact_refs, event_seq, started_at, rowid, ` + isNode + `
		   FROM runs WHERE item_id IS NOT NULL`)
	if err != nil {
		return nil, readFailure(err)
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
	var ordered []priorRow
	for rows.Next() {
		var row priorRow
		var node bool
		if err := rows.Scan(&row.itemID, &row.rec.RunID, &row.rec.Backend, &row.rec.State, &row.exitCode, &row.artifactRefs, &row.order, &row.startedAt, &row.rowid, &node); err != nil {
			return nil, readFailure(err)
		}
		// Old stores have no workflow tables. Ordinary judges stay excluded there;
		// with node runs, an evaluator is itself a workflow run and may be returned.
		if judged[row.rec.RunID] && !node {
			continue
		}
		if seq, ended := endings[row.rec.RunID]; ended {
			row.order = seq
		}
		ordered = append(ordered, row)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
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
	priors := map[string][]priorRun{}
	for _, row := range ordered {
		itemID, rec, artifactRefs := row.itemID, row.rec, row.artifactRefs
		if row.exitCode.Valid {
			code := int(row.exitCode.Int64)
			rec.ExitCode = &code
		}
		var refs []run.ArtifactRef
		if err := json.Unmarshal([]byte(artifactRefs), &refs); err != nil {
			return nil, Internalf("reading the artifact refs of run %q: %v", rec.RunID, err)
		}
		class := classes[rec.RunID]
		if class == "" {
			class = ClassifyAttempt(rec, logTail(refs))
		}
		if interrupted[rec.RunID] {
			class = AttemptInterrupted
		}
		var repeat *RepeatedCall
		if rc, ok := repeated[rec.RunID]; ok {
			class, repeat = AttemptRepeating, &rc
		}
		priors[itemID] = append(priors[itemID], priorRun{
			RunID:    rec.RunID,
			Backend:  rec.Backend,
			Class:    class,
			Restaged: restaged[rec.RunID],
			Repeated: repeat,
		})
	}
	return priors, nil
}

// readRunJudgmentsAndEndings answers the runs a judge request names and, for each run that
// ended, the seq of its newest ending, read by kind.
func readRunJudgmentsAndEndings(r *store.Reader) (map[string]bool, map[string]int64, error) {
	events, err := r.EventsOfKind(0, judgeRequestKind, run.FinishKind)
	if err != nil {
		return nil, nil, err
	}
	judged, endings := map[string]bool{}, map[string]int64{}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case judgeRequestKind:
			judged[*e.ObjectID] = true
		case run.FinishKind:
			if e.Seq > endings[*e.ObjectID] {
				endings[*e.ObjectID] = e.Seq
			}
		}
	}
	return judged, endings, nil
}

// logTail answers the tail of the pinned extracted report when one exists, otherwise
// the legacy plain-text log. A missing pinned report stays missing rather than reading
// a different run's reusable lane report.
//
// The tail rather than the whole file: the report's footer is the last thing a run writes,
// and a model run's stream is megabytes this decision has no use for.
func logTail(refs []run.ArtifactRef) string {
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
