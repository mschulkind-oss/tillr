package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr-drain/spend"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// waitingRow is shared by the terminal and HTTP faces. Since is an absolute UTC
// time, or the explicit absence of a decision; it is not the item's update time.
type waitingRow struct {
	backendReset time.Time
	Quota        *quota.QuotaStatus `json:"quota,omitempty"`
	GraphChanges []string           `json:"graph_changes,omitempty"`
	Place        int                `json:"place,omitempty"`
	ItemID       string             `json:"item_id"`
	RunID        string             `json:"run_id,omitempty"`
	Backend      string             `json:"backend,omitempty"`
	LaneID       string             `json:"lane_id,omitempty"`
	Document     string             `json:"document"`
	// Observation identifies a publication hold, never an item or design document.
	Observation string `json:"observation,omitempty"`
	Class       string `json:"class"`
	Where       string `json:"where"`
	Why         string `json:"why"`
	Mover       string `json:"mover"`
	Since       string `json:"since"`
	Waited      string `json:"waited"`
	ClassSource string `json:"class_source"`
	LaneStatus  string `json:"lane_status,omitempty"`
	LaneReason  string `json:"lane_reason,omitempty"`
}

// waitingRule is the view's copy of a holdrules.Rule. The rules themselves live in
// tillr-drain/holdrules, beside the reasons they describe: adding a hold reason means
// adding its row there, and the drain module's own tests fail until it exists. This
// file keeps only the presentation of a resolved rule.
type waitingRule struct{ class, where, mover string }

func waitingRuleFor(reason string) waitingRule {
	rule := holdrules.For(reason)
	return waitingRule{rule.Class, rule.Where, rule.Mover}
}

func waitingFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("waiting")
	path := fs.String("store", "", "the store to read (required)")
	asJSON := fs.Bool("json", false, "print the waiting rows as JSON instead of a table")
	history := fs.Bool("history", false, "read recorded decisions instead of the current planner (also supports older stores)")
	policyRef := fs.String("policy", "", "the loop's policy file; absent means no-policy")
	lanesArg := fs.String("lanes", "", "comma-separated lanes offered by the loop (default: all store lanes)")
	root := fs.String("root", ".", "the directory a relative design_doc resolves against")
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	plannerFlag := fs.String("tillr-drain", "", "the tillr-drain whose `why` answers (default: the one beside this executable, then PATH); it must be this program's build")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" {
		return record.Envelope{}, usagef("waiting needs --store <path>")
	}
	ctx := context.Background()
	questions := &vantage.VantageQuestions{Root: *root, Command: commandFields(*questionsCmd), Context: ctx}
	var rows []waitingRow
	var err error
	if *history {
		rows, err = readWaiting(ctx, *path, questions)
	} else {
		_, rows, err = askPlanner(ctx, *plannerFlag, buildInfo("none"), *path, *root, *policyRef, *lanesArg, commandFields(*questionsCmd), nil)
	}
	if err == nil && !*history {
		s, e := store.OpenRead(ctx, *path)
		if e != nil {
			err = e
		} else {
			err = s.ReadTx(ctx, func(r *store.Reader) error {
				quotas, e := waitingBackendQuotas(r, time.Now())
				if e != nil {
					return e
				}
				rows = mergeQuotaWaiting(rows, quotas)
				extra, e := waitingCommentIntake(r)
				if e != nil {
					return e
				}
				rows = append(rows, extra...)
				lanes, e := waitingLaneConditions(r)
				rows = append(rows, lanes...)
				if e != nil {
					return e
				}
				moves, e := waitingPublicationHolds(r, *path)
				if e != nil {
					return e
				}
				rows = append(rows, moves...)
				return addGraphRevisionHistory(r, rows)
			})
			s.Close()
		}
	}
	if err == nil {
		rows, err = policyWaitingRows(*root, rows)
	}
	if err == nil {
		rows, err = heavyWaitingRows(ctx, *root, *path, rows)
	}
	if err != nil {
		var mismatch schemaMismatch
		if errors.As(err, &mismatch) {
			fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
			return record.Envelope{}, record.ExitIncompatible
		}
		return record.Envelope{}, exitFor(err)
	}
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	declared, err := definition.ForStore(ctx, s)
	s.Close()
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	for i := range rows {
		normalizeWaitingMover(&rows[i])
	}
	sortWaitingDocuments(rows)
	if *asJSON {
		err = json.NewEncoder(os.Stdout).Encode(rows)
	} else {
		window, readErr := spend.Read(ctx, *path, time.Now())
		if readErr != nil {
			return record.Envelope{}, exitFor(readErr)
		}
		_, err = fmt.Fprintf(os.Stdout, "definition: %s\n%s\n", declared.Name, window)
		var laneRows []laneRow
		if err == nil {
			laneRows, err = readLaneRows(ctx, *path, filepath.Join(filepath.Dir(*path), "loop"), filepath.Join(filepath.Dir(*path), "lanes"), time.Now())
		}
		if err == nil {
			_, err = fmt.Fprintln(os.Stdout, laneCapacityLine(laneRows))
		}
		if err == nil {
			err = printWaiting(os.Stdout, rows)
		}
	}
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	return record.Envelope{}, record.ExitOK
}

func readWaiting(ctx context.Context, path string, questions vantage.QuestionSource) ([]waitingRow, error) {
	r, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []waitingRow
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		version, err := tx.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		out, err = assembleWaiting(tx, questions, path)
		return err
	})
	return out, err
}

func (s *server) waitingPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	rows, err := assembleWaiting(r, s.vantage.source(req.Context()), s.storePath)
	if err == nil {
		rows, err = policyWaitingRows(s.root, rows)
	}
	if err == nil {
		rows, err = heavyWaitingRows(req.Context(), s.root, s.storePath, rows)
	}
	if err != nil {
		return nil, failure(codeStoreUnreadable, "waiting could not read the store: %v", err)
	}
	sortWaitingDocuments(rows)
	return rows, nil
}

// One pinned read transaction supplies both callers. Optional utility tables are
// absent on older stores; SQL errors other than absence always refuse the read.
func assembleWaiting(r *store.Reader, questions vantage.QuestionSource, storePath string) ([]waitingRow, error) {
	conflicts, err := waitingConflicts(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	items, err := readItems(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	runs, err := readRuns(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	lanes, err := readLanes(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	allocations, err := readAllocations(r)
	if err != nil {
		return nil, err
	}
	runs = append(runs, liveRuns(lanes, runs, allocations)...)
	candidates, err := readCandidates(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	attempts, err := readAllAttempts(r)
	if err != nil && !isMissingTable(err) {
		return nil, err
	}
	states := map[string]string{}
	for _, item := range items {
		states[item.ItemID] = item.State
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Rank != items[j].Rank {
			if items[i].Rank == "" {
				return false
			}
			if items[j].Rank == "" {
				return true
			}
			return items[i].Rank < items[j].Rank
		}
		return items[i].ItemID < items[j].ItemID
	})
	itemFaults, err := faults.Read(r)
	if err != nil {
		return nil, err
	}
	places := itemPlaces(items)
	out := []waitingRow{}
	for _, item := range items {
		if waitingTerminal(item.State) {
			continue
		}
		row := waitingRow{Place: places[item.ItemID], ItemID: item.ItemID, Class: "waits on the loop", Where: "queue", Why: "not yet planned", Mover: "the next pass", Since: "no pass yet", ClassSource: "rule"}
		var latestRun *storeRun
		var active *storeRun
		for i := range runs {
			run := &runs[i]
			if run.ItemID != item.ItemID {
				continue
			}
			if latestRun == nil || waitingRunLater(*run, *latestRun) {
				latestRun = run
			}
			if run.State == liveRunState {
				active = run
			}
		}
		if latestRun != nil {
			row.Where = "lane"
		}
		for _, candidate := range candidates {
			if candidate.ItemID != item.ItemID {
				continue
			}
			row.Where = waitingFurtherStage(row.Where, "gate")
			for _, attempt := range attempts {
				if attempt.CandidateID == candidate.CandidateID {
					row.Where = waitingFurtherStage(row.Where, "land")
				}
			}
		}
		stage, err := waitingRecordedStage(r, item.ItemID, latestRun)
		if err != nil {
			return nil, err
		}
		row.Where = waitingFurtherStage(row.Where, stage)
		var stoppedAt int64
		var stopped bool
		var stoppedRun string
		if latestRun != nil {
			stoppedAt, stopped, err = waitingStoppedRun(r, latestRun.RunID)
			if err != nil {
				return nil, err
			}
			if stopped {
				stoppedRun = latestRun.RunID
				if active != nil && active.RunID == stoppedRun {
					active = nil
				}
			}
		}
		last, since, deferredBy, found, err := waitingDecision(r, item.ItemID, stoppedRun, stoppedAt)
		if err != nil {
			return nil, err
		}
		if found {
			rule := waitingRuleFor(last.Reason)
			row.Class, row.Why, row.Mover = rule.class, last.Reason, rule.mover
			if strings.TrimSpace(row.Why) == "" {
				row.Why = "reason not recorded"
			}
			row.Where = waitingFurtherStage(row.Where, rule.where)
			row.Since = time.UnixMilli(since).UTC().Format(time.RFC3339Nano)
			if deferredBy != "" {
				row.Mover = deferredBy
			}
			if last.Reason == "question-open" {
				answer := readQuestions(questions, item.DesignDoc)
				if answer.State == questionsOK && len(answer.Questions) > 0 {
					row.Mover = "owner; answer " + item.DesignDoc + "#" + answer.Questions[0].QuestionID
					row.Why += ": " + answer.Questions[0].Text
				} else if answer.State != "" && answer.State != questionsOK {
					row.Mover = answer.State
				}
			}
			if last.Reason == "blocked" {
				edges, err := readEdges(r, item.ItemID)
				if err != nil && !isMissingTable(err) {
					return nil, err
				}
				var blockers []string
				for _, edge := range edges {
					if !waitingTerminal(states[edge.BlockedBy]) {
						blockers = append(blockers, edge.BlockedBy)
					}
				}
				if len(blockers) > 0 {
					row.Mover = strings.Join(blockers, ", ")
				}
			}
		}
		if found && (last.Reason == "eval-failed" || last.Reason == "judge-unreadable") {
			var detail string
			err := r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events
 WHERE kind='drain.refusal' AND json_valid(payload) AND json_extract(payload,'$.item_id')=?
 AND json_extract(payload,'$.step')=? ORDER BY seq DESC LIMIT 1`, item.ItemID, last.Reason).Scan(&detail)
			if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingTable(err) {
				return nil, err
			}
			if detail != "" {
				row.Why += ": " + detail
			}
		}
		if found && last.Reason == "gate-stall-rerun" {
			detail, err := waitingGateStall(r, item.ItemID)
			if err != nil {
				return nil, err
			}
			if detail != "" {
				row.Why += ": " + detail
			}
		}
		if c, ok := conflicts[item.ItemID]; ok && item.State == "blocked" && c.Status != "closed" && c.Status != "answered" {
			row.Class, row.Mover, row.Why = "waits on work", c.WaitingOn, c.ConflictID+": "+c.Text
			row.Since = time.UnixMilli(c.Since).UTC().Format(time.RFC3339Nano)
			if c.WaitingOn == "owner" {
				row.Class, row.Mover = "needs you", "operator; route the conflict into a document question"
				if c.BodyRef == "" {
					row.Why += "; no document exists; name or triage one for " + item.ItemID
				}
			}
		}
		if c := conflicts[item.ItemID]; item.State == "blocked" && item.Waiter != "" && c.TriageVersion == "" {
			row.Class, row.Mover, row.Why = "waits on work", item.Waiter, item.WaitingReason
			if strings.TrimSpace(row.Why) == "" {
				row.Why = "waiting reason not recorded"
			}
			row.ClassSource = "store"
			row.Since = time.UnixMilli(item.UpdatedAtMS).UTC().Format(time.RFC3339Nano)
			if item.Waiter == "owner" {
				row.Class, row.Mover = "needs you", "operator; resolve the recorded hold"
				if c := conflicts[item.ItemID]; c.ConflictID != "" {
					row.Mover = "operator; route the conflict into a document question"
					if c.BodyRef == "" {
						row.Why += "; no document exists; name or triage one for " + item.ItemID
					}
				}
			}
		} else if item.State == "blocked" && !found && row.Why == "not yet planned" {
			row.Class, row.Mover, row.Why = "waits on work", "waiter not recorded", "waiter not recorded"
		}
		fixer := ""
		if found && last.Reason == "trunk-broken" {
			fixer = deferredBy
		}
		if strings.HasPrefix(item.Waiter, "item:fixer-main-") {
			fixer = strings.TrimPrefix(item.Waiter, "item:")
		}
		if fixer != "" {
			detail, owner, err := waitingTrunkFixer(r, fixer, conflicts)
			if err != nil {
				return nil, err
			}
			row.Why += ": " + detail
			row.Mover = detail
			if owner {
				row.Class = "needs you"
			}
		}
		if found && last.Action == "hold" && latestRun != nil && active == nil {
			cause, err := waitingEndCause(r, latestRun.RunID)
			if err != nil {
				return nil, err
			}
			// Preserve the seat, blocker, or owner that can move the item; the
			// run's termination explains the hold but does not replace its mover.
			row.Mover += "; " + cause
		}
		if latestRun != nil {
			hold, since, err := waitingRepairHold(r, item.ItemID, latestRun.RunID)
			if err != nil {
				return nil, err
			}
			if hold != nil {
				row.Class, row.Where = "needs you", "land"
				row.Why, row.Mover = "conflict-repair-staging-failed: "+hold.Cause, hold.NextAction
				row.Since = time.UnixMilli(since).UTC().Format(time.RFC3339Nano)
			}
		}
		if fault, held := itemFaults[item.ItemID]; held {
			rule := waitingRuleFor(fault.Reason)
			row.Class, row.Where = "needs you", rule.where
			row.Why = fmt.Sprintf("%s: %s", fault.Reason, fault.Detail)
			if fault.RunID != "" {
				row.Why += "; run " + fault.RunID
			}
			if fault.LaneID != "" {
				row.Why += "; lane " + fault.LaneID
			}
			row.Mover = fault.ClearCommand(storePath)
			row.Since = time.UnixMilli(fault.AtMS).UTC().Format(time.RFC3339Nano)
		}
		if active != nil {
			row.Class, row.Where, row.Mover = "moving", "lane", active.RunID
			if c := conflicts[item.ItemID]; c.TriageVersion != "" && c.WaitingOn == "design" {
				row.Mover = "triage on the design seat; " + active.RunID
			}
		}
		if stopped && (!found || last.DispatchedMS < stoppedAt) {
			row.Class, row.Where = "waits on the loop", "lane"
			row.Why, row.Mover = "run ended by a stop", latestRun.RunID
			row.Since = time.UnixMilli(stoppedAt).UTC().Format(time.RFC3339Nano)
			row.ClassSource = "rule"
		}
		if row.Class == "waits on work" && strings.HasPrefix(row.Why, "trunk-path-dirty: ") {
			started, err := time.Parse(time.RFC3339Nano, row.Since)
			if err != nil {
				return nil, err
			}
			elapsed := time.Since(started).Round(time.Second)
			if elapsed < 0 {
				elapsed = 0
			}
			row.Waited = elapsed.String()
		}
		for _, lane := range lanes {
			if len(lane.ActiveNodes) == 0 {
				continue
			}
			if allocated, ok := allocations[lane.ActiveNodes[0]]; ok && allocated.ItemID == item.ItemID {
				row.Class, row.Where = "waits on work", "lane"
				row.Why = fmt.Sprintf("%d nodes running", len(lane.ActiveNodes))
				row.Mover = "the item's nodes to finish"
			}
		}
		if c := conflicts[item.ItemID]; c.Status == "question" {
			decorateOwnerQuestionWaiting(&row, c, questions)
		}
		out = append(out, row)
	}
	addConflictHistory(out, conflicts)
	if err := addHeldLaneHistory(r, out); err != nil {
		return nil, err
	}
	if err := addPreservedHistory(r, out); err != nil {
		return nil, err
	}
	windows, err := waitingBackendWindows(r, time.Now())
	if err != nil {
		return nil, err
	}
	quotas, err := waitingBackendQuotas(r, time.Now())
	if err != nil {
		return nil, err
	}
	observed := map[string]bool{}
	for _, q := range quotas {
		observed[q.ItemID] = true
	}
	filtered := windows[:0]
	for _, w := range windows {
		if !observed[w.ItemID] {
			filtered = append(filtered, w)
		}
	}
	windows = append(filtered, quotas...)
	extra, err := waitingCommentIntake(r)
	if err != nil {
		return nil, err
	}
	laneRows, err := waitingLaneConditions(r)
	if err != nil {
		return nil, err
	}
	moves, err := waitingPublicationHolds(r, storePath)
	if err != nil {
		return nil, err
	}
	out = append(out, moves...)
	all, err := appendWaitingDesigns(r, append(append(mergeQuotaWaiting(out, windows), extra...), laneRows...))
	if err != nil {
		return nil, err
	}
	if err := addGraphRevisionHistory(r, all); err != nil {
		return nil, err
	}
	for i := range all {
		normalizeWaitingMover(&all[i])
	}
	return all, nil
}

// Window rows are optional on old stores, and disappear at (not after) reset.
// The waiting face reads the durable hold; it never guesses from a run's failure.
func waitingBackendWindows(r *store.Reader, now time.Time) ([]waitingRow, error) {
	rows, err := r.Query(`SELECT backend, kind, reset_at FROM backend_windows WHERE reset_at > ? ORDER BY backend`, now.UnixMilli())
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waitingRow
	for rows.Next() {
		var backend, kind string
		var reset int64
		if err := rows.Scan(&backend, &kind, &reset); err != nil {
			return nil, err
		}
		reopens := time.UnixMilli(reset).UTC().Format(time.RFC3339)
		out = append(out, waitingRow{ItemID: "backend:" + backend, Class: "waits on work", Where: "backend", Why: kind + " reopens " + reopens, Mover: "the reset time", Since: "—", ClassSource: "store", backendReset: time.UnixMilli(reset).UTC()})
	}
	return out, rows.Err()
}

// Run timestamps may share a second. Finish order and the store's event sequence
// distinguish those runs before an ID fallback; attempt IDs are not chronological.
func waitingRunLater(a, b storeRun) bool {
	if a.StartedAtMS != b.StartedAtMS {
		return a.StartedAtMS > b.StartedAtMS
	}
	if a.FinishedAtMS != b.FinishedAtMS {
		return a.FinishedAtMS > b.FinishedAtMS
	}
	if a.EventSeq != b.EventSeq {
		return a.EventSeq > b.EventSeq
	}
	return a.RunID > b.RunID
}

// waitingEndCause reads the last run's observed termination, never inferring it
// from state. An older store may lack the column and remains read-only here.
func waitingEndCause(r *store.Reader, runID string) (string, error) {
	var cause sql.NullString
	err := r.QueryRow(`SELECT end_cause FROM runs WHERE run_id = ?`, runID).Scan(&cause)
	if isMissingColumn(err) || errors.Is(err, sql.ErrNoRows) {
		return "end cause not recorded", nil
	}
	if err != nil {
		return "", err
	}
	switch cause.String {
	case "", "end cause not recorded":
		return "end cause not recorded", nil
	case "normal-exit":
		return "normal exit", nil
	case "time-limit":
		return "time limit", nil
	case "lease-reclaimed":
		return "lease reclaimed", nil
	case "failed-to-start":
		return "failure to start", nil
	default:
		return cause.String, nil
	}
}

// The event sequence orders stop/resume segments, even within one millisecond.
func waitingStoppedRun(r *store.Reader, runID string) (int64, bool, error) {
	var kind string
	var at int64
	err := r.QueryRow(`SELECT kind, ts FROM events WHERE object_type = 'run' AND object_id = ?
  AND kind IN ('drain.orphaned', 'run.resume') ORDER BY seq DESC LIMIT 1`, runID).Scan(&kind, &at)
	if isMissingTable(err) || errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil || kind != "drain.orphaned" {
		return 0, false, err
	}
	return at, true, nil
}

func waitingTerminal(state string) bool {
	return state == "landed" || state == "closed" || state == "resolved-elsewhere"
}

// Each decision query returns at most one row for one displayed item. SQL finds
// the latest answer, last interruption, and first answer after that interruption;
// no history slice or arbitrary time window can truncate the exact streak.
func waitingDecision(r *store.Reader, itemID, stoppedRun string, stoppedAt int64) (storeDispatch, int64, string, bool, error) {
	var last storeDispatch
	var mover string
	where := ` WHERE item_id = ?`
	args := []any{itemID}
	if stoppedRun != "" {
		// A launch tied with (or recorded after) its stop cannot supersede the
		// stop. Remove it before choosing a decision so an equally timed hold
		// wins regardless of lexical decision ID order. Retry also launches runs.
		where += ` AND NOT (COALESCE(run_id, '') = ? AND action IN ('dispatch', 'retry') AND dispatched_at >= ?)`
		args = append(args, stoppedRun, stoppedAt)
	}
	selectLatest := func(column string) error {
		return r.QueryRow(`SELECT decision_id, action, reason, dispatched_at, `+column+`
   FROM dispatches`+where+` ORDER BY dispatched_at DESC, decision_id DESC LIMIT 1`, args...).
			Scan(&last.DecisionID, &last.Action, &last.Reason, &last.DispatchedMS, &mover)
	}
	err := selectLatest(`COALESCE(deferred_by, '')`)
	if isMissingColumn(err) {
		err = selectLatest(`''`)
	}
	if isMissingTable(err) || errors.Is(err, sql.ErrNoRows) {
		return last, 0, "", false, nil
	}
	if err != nil {
		return last, 0, "", false, err
	}
	var brokenAt int64
	var brokenID string
	err = r.QueryRow(`SELECT dispatched_at, decision_id FROM dispatches`+where+`
  AND (action != ? OR reason != ?)
  ORDER BY dispatched_at DESC, decision_id DESC LIMIT 1`, append(args, last.Action, last.Reason)...).Scan(&brokenAt, &brokenID)
	var since int64
	if errors.Is(err, sql.ErrNoRows) {
		err = r.QueryRow(`SELECT dispatched_at FROM dispatches`+where+`
   ORDER BY dispatched_at, decision_id LIMIT 1`, args...).Scan(&since)
	} else if err == nil {
		err = r.QueryRow(`SELECT dispatched_at FROM dispatches`+where+`
   AND (dispatched_at, decision_id) > (?, ?)
   ORDER BY dispatched_at, decision_id LIMIT 1`, append(args, brokenAt, brokenID)...).Scan(&since)
	}
	return last, since, mover, true, err
}

var waitingStages = []string{"queue", "lane", "fence", "judge", "gate", "land", "trunk"}

func waitingFurtherStage(a, b string) string {
	for i := len(waitingStages) - 1; i >= 0; i-- {
		if waitingStages[i] == a || waitingStages[i] == b {
			return waitingStages[i]
		}
	}
	return "queue"
}

// Current writers leave gate/refusal events and landings, not candidate rows.
// Aggregate stage evidence in SQL so event history is not copied into the view.
func waitingRecordedStage(r *store.Reader, itemID string, run *storeRun) (string, error) {
	stage, runID := "queue", ""
	if run != nil {
		runID = run.RunID
		var step int
		err := r.QueryRow(`SELECT COALESCE(MAX(CASE
   WHEN kind = 'gate.verdict' THEN 4
   WHEN kind = 'drain.refusal' THEN CASE json_extract(CASE WHEN json_valid(payload) THEN payload ELSE '{}' END, '$.step')
    WHEN 'no-op' THEN 1 WHEN 'fence' THEN 2
    WHEN 'eval-failed' THEN 3 WHEN 'judge-unreadable' THEN 3 WHEN 'gate' THEN 4
    WHEN 'land' THEN 5 WHEN 'conflict' THEN 5 ELSE 0 END
   ELSE 0 END), 0) FROM events
   WHERE object_type = 'run' AND object_id = ? AND kind IN ('gate.verdict','drain.refusal')`, runID).Scan(&step)
		if err != nil && !isMissingTable(err) {
			return "", err
		}
		stage = waitingStages[step]
	}
	var result string
	err := r.QueryRow(`SELECT result FROM landings WHERE item_id = ? AND (? = '' OR run_id = ?)
  ORDER BY event_seq DESC, landing_id DESC LIMIT 1`, itemID, runID, runID).Scan(&result)
	if err != nil && !isMissingTable(err) && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil {
		if result == "landed" {
			stage = "trunk"
		} else {
			stage = waitingFurtherStage(stage, "land")
		}
	}
	return stage, nil
}

// decorateOwnerQuestionWaiting reads current document state even when the recorded
// plan predates publication. The origin fields are references, not a second parser.
func decorateOwnerQuestionWaiting(row *waitingRow, c waitingConflict, source vantage.QuestionSource) {
	file, id, _ := strings.Cut(c.QuestionRef, "#")
	row.Class, row.Mover, row.Why = "needs a cause", "operator; restore the question index or document", "questions-unknown: "+c.QuestionRef
	if source == nil || file == "" || id == "" {
		return
	}
	doc, err := source.Document(file)
	if err != nil {
		return
	}
	if !doc.Present {
		row.Why = "no document exists for " + row.ItemID + ": " + file
		return
	}
	for _, q := range doc.Questions {
		if q.ID != id {
			continue
		}
		row.Class, row.Mover, row.Why = "needs you", "owner; answer "+c.QuestionRef, "question-open: "+questionText(q)
		return
	}
	// A compacted question is settled by the next sync; a missing index entry
	// is never silently interpreted as an answer by this read face.
}

// normalizeWaitingMover keeps question answers separate from operational acts,
// including rows read from older stores that named only an owner.
func normalizeWaitingMover(row *waitingRow) {
	if row.Class == "ready" || row.Class == "moving" || row.Class == "done" {
		return
	}
	if strings.HasPrefix(row.Mover, "owner; answer ") && strings.Contains(row.Mover, "#OQ-") {
		return
	}
	for _, actor := range []string{"drain;", "design seat;", "operator;"} {
		if strings.HasPrefix(row.Mover, actor) {
			return
		}
	}
	actor := "drain"
	if row.Mover == "design" || row.Mover == "plan" || strings.HasPrefix(row.Mover, "design;") || strings.HasPrefix(row.Mover, "plan;") || strings.Contains(row.Mover, "design seat") {
		actor = "design seat"
	}
	if row.Class == "needs you" || row.Class == "needs a cause" || strings.HasPrefix(row.Why, "trunk-path-dirty:") {
		actor = "operator"
	}
	action := row.Mover
	for _, prefix := range []string{"the owner; ", "owner; ", "owner: ", "the operator; ", "operator; ", "design seat; ", "design; ", "plan; ", "drain; "} {
		action = strings.TrimPrefix(action, prefix)
	}
	switch action {
	case "owner", "the owner", "operator", "the operator":
		action = "resolve the recorded hold"
	case "design", "plan":
		action = "resolve the recorded planning hold"
	}
	row.Mover = actor + "; " + action
}

func printWaiting(w io.Writer, rows []waitingRow) error {
	owner, drain, design, operator := 0, 0, 0, 0
	for _, row := range rows {
		if row.Class == "ready" || row.Class == "moving" || row.Class == "done" {
			continue
		}
		switch {
		case strings.HasPrefix(row.Mover, "owner; answer ") && strings.Contains(row.Mover, "#OQ-"):
			owner++
		case strings.HasPrefix(row.Mover, "design seat;") || strings.Contains(row.Mover, "design seat"):
			design++
		case strings.HasPrefix(row.Mover, "operator;") || row.Class == "needs you" || row.Class == "needs a cause":
			operator++
		default:
			drain++
		}
	}
	if _, err := fmt.Fprintf(w, "summary: owner-waiting %d; next [drain=%d, design seat=%d, operator=%d]\n", owner, drain, design, operator); err != nil {
		return err
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "#\titem_id\tclass\twhere\twhy\tmover\tsince"); err != nil {
		return err
	}
	for _, row := range rows {
		place := placeText(row.Place)
		// Quote control characters supplied by store text so one item stays one row.
		why := row.Why
		for _, change := range row.GraphChanges {
			why += "; " + change
		}
		if row.Waited != "" {
			why += " (waited " + row.Waited + ")"
		}
		identity := row.ItemID
		if row.Document != "" {
			identity = row.Document
		}
		if row.Observation != "" {
			identity = row.Observation
		}
		cells := []string{place, identity, row.Class, row.Where, why, row.Mover, row.Since}
		for i, cell := range cells {
			cells[i] = strings.NewReplacer("\t", "\\t", "\n", "\\n", "\r", "\\r").Replace(cell)
		}
		if _, err := fmt.Fprintln(table, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return table.Flush()
}

// The durable wait carries the check's observations and the concurrency change
// that will wake it; the face never supplies a clock-based retry of its own.
func waitingGateStall(r *store.Reader, itemID string) (string, error) {
	var raw string
	err := r.QueryRow(`SELECT payload FROM events WHERE kind='drain.gate-stall' AND json_extract(payload,'$.candidate.item_id')=? ORDER BY seq DESC LIMIT 1`, itemID).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var hold struct {
		GatesInFlight int             `json:"gates_in_flight"`
		Observations  json.RawMessage `json:"observations"`
	}
	if err := json.Unmarshal([]byte(raw), &hold); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s; %d gates in flight at stall", hold.Observations, hold.GatesInFlight), nil
}
