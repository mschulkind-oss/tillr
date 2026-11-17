package drain

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
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
func reportFooter(log string) string {
	idx := strings.LastIndex(log, ReportFooter)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(log[idx+len(ReportFooter):])
	if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.ToLower(strings.TrimSpace(rest))
}

// priorRun is one terminal run the store holds for an item, as the planner reads it.
type priorRun struct {
	RunID   string
	Backend string
	Class   AttemptClass
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
	interrupted := map[string]bool{}
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	// An interruption describes the segment it ended. A later continuation of the
	// same run (run.resume) is that run's new segment, and its outcome is judged.
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case loopOrphanedKind:
			interrupted[*e.ObjectID] = true
		case run.ResumeKind:
			delete(interrupted, *e.ObjectID)
		}
	}
	// Timestamp ties follow recording order, not lexical run-id order (a10 sorts
	// before a9). event_seq is the store event's sequence; SQLite's insertion row
	// number, rowid, breaks ties for legacy rows with no event sequence.
	rows, err := r.Query(
		`SELECT item_id, run_id, backend, state, exit_code, artifact_refs
		   FROM runs WHERE item_id IS NOT NULL ORDER BY item_id, started_at, event_seq, rowid`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	priors := map[string][]priorRun{}
	for rows.Next() {
		var (
			itemID       string
			rec          run.Record
			exitCode     sql.NullInt64
			artifactRefs string
		)
		if err := rows.Scan(&itemID, &rec.RunID, &rec.Backend, &rec.State, &exitCode, &artifactRefs); err != nil {
			return nil, readFailure(err)
		}
		if exitCode.Valid {
			code := int(exitCode.Int64)
			rec.ExitCode = &code
		}
		var refs []run.ArtifactRef
		if err := json.Unmarshal([]byte(artifactRefs), &refs); err != nil {
			return nil, Internalf("reading the artifact refs of run %q: %v", rec.RunID, err)
		}
		class := ClassifyAttempt(rec, logTail(refs))
		if interrupted[rec.RunID] {
			class = AttemptInterrupted
		}
		priors[itemID] = append(priors[itemID], priorRun{
			RunID:   rec.RunID,
			Backend: rec.Backend,
			Class:   class,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return priors, nil
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
