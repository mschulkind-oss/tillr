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
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/vantage"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// waitingRow is shared by the terminal and HTTP faces. Since is an absolute UTC
// time, or the explicit absence of a decision; it is not the item's update time.
type waitingRow struct {
	GraphChanges []string `json:"graph_changes,omitempty"`
	Rank         string   `json:"rank"`
	ItemID       string   `json:"item_id"`
	Document     string   `json:"document"`
	Class        string   `json:"class"`
	Where        string   `json:"where"`
	Why          string   `json:"why"`
	Mover        string   `json:"mover"`
	Since        string   `json:"since"`
	Waited       string   `json:"waited"`
	ClassSource  string   `json:"class_source"`
	LaneStatus   string   `json:"lane_status,omitempty"`
	LaneReason   string   `json:"lane_reason,omitempty"`
}

type waitingRule struct{ class, where, mover string }

// These are drain's exported reason spellings. Item faults use its shared reader;
// opening this face's store remains read-only. TestWaitingEveryDrainReasonHasARule enumerates the
// upstream constants rather than maintaining a second hand-written checklist.
// Fast whole-tree declarations run at landing, in just done, and in rolling;
// rolling also judges the deferred suites. Either red waits on the named fixer.
var waitingRules = map[string]waitingRule{
	"workflow-invalid":               {"needs you", "queue", "correct the named workflow file and run `tillr drain workflow check`"},
	"node-failed":                    {"needs you", "lane", "inspect the named node and authorize a retry"},
	"gate-stall-rerun":               {"waits on work", "gate", "same candidate reruns when fewer gates are in flight or none other are"},
	"gate-stalled":                   {"needs you", "gate", "diagnose the stalled check and clear the fault"},
	"gate-harness-error":             {"needs you", "gate", "diagnose the gate and clear the fault"},
	"host-boundary-violation":        {"needs you", "fence", "inspect the recorded violation and clear the fault"},
	"candidate-record-mismatch":      {"needs you", "lane", "restore the recorded candidate and clear the fault"},
	"definition-sync-failed":         {"needs you", "queue", "correct the definition and clear the fault"},
	"backend-window":                 {"waits on work", "backend", "the reset time"},
	"lane-holds-unlanded-change":     {"needs you", "lane", "land the change, or reset the lane deliberately"},
	"lane-branch-held":               {"needs you", "lane", "remove the worktree holding the lane's branch, or stage a different lane id"},
	"triage-no-block":                {"waits on work", "queue", "triage on the design seat"},
	"conflict-design":                {"waits on work", "queue", "design"},
	"design-under-comment":           {"waits on work", "queue", "the comment item"},
	"conflict-owner":                 {"needs you", "queue", "owner"},
	"conflict-answered":              {"ready", "queue", "the next pass"},
	"inputs-changed":                 {"ready", "queue", "the next pass"},
	"readmission-exhausted":          {"needs you", "lane", "automatic re-admission failed; run `tillr drain retry` after diagnosis"},
	"ready":                          {"ready", "queue", "the next pass"},
	"blocked":                        {"waits on work", "queue", "cause not recorded"},
	"not-ready":                      {"needs you", "queue", "the owner"},
	"run-link-missing":               {"needs a cause", "lane", "cause not recorded"},
	"boundary-scope-escape":          {"needs you", "fence", "inspect the named node and escaped path, then authorize a retry"},
	"node-scope-outside-fence":       {"needs you", "fence", "narrow the named node's write_scope to the item's fence"},
	"candidate-no-op":                {"needs a cause", "lane", "cause not recorded"},
	"run-unjudged":                   {"needs a cause", "lane", "cause not recorded"},
	"gate-failed":                    {"needs a cause", "gate", "cause not recorded"},
	"red-cap":                        {"waits on work", "land", "the heavy runner; prove trunk or file its fixer"},
	"land-refused":                   {"needs you", "land", "inspect the refused landing and authorize a retry"},
	"no-policy":                      {"needs you", "queue", "the owner"},
	"capacity":                       {"waits on work", "queue", "the next pass"},
	"no-lane":                        {"waits on work", "queue", "a free lane"},
	"no-fence":                       {"needs you", "queue", "the owner"},
	"run-live":                       {"waits on work", "lane", "the run from the earlier loop to finish; `tillr drain stop --end-runs` ends it"},
	"fork-held":                      {"waits on work", "queue", "cause not recorded"},
	"failover":                       {"ready", "queue", "the next pass"},
	"seat-degraded":                  {"ready", "queue", "the next pass"},
	"run-infra-exhausted":            {"needs a cause", "lane", "cause not recorded"},
	"harness-refused":                {"ready", "queue", "the next pass"},
	"retry-judged":                   {"needs you", "gate", "the owner"},
	"retry-unjudged":                 {"needs you", "lane", "the owner"},
	"retry-stale":                    {"needs you", "lane", "the owner"},
	"question-open":                  {"needs you", "queue", "question not recorded"},
	"questions-unknown":              {"needs a cause", "queue", "cause not recorded"},
	"design-doc-missing":             {"needs you", "queue", "the owner"},
	"trunk-path-dirty":               {"waits on work", "land", "commit or restore the path"},
	"trunk-rewritten":                {"needs you", "trunk", "restore the trunk's history"},
	"trunk-broken":                   {"waits on work", "trunk", "the trunk fixer to land"},
	"trunk-unproved":                 {"waits on work", "trunk", "the heavy runner; prove the trunk"},
	"conflict-repair-staging-failed": {"needs you", "land", "correct the reported staging cause; the next pass retries the retained candidate"},
	"conflict-fix":                   {"ready", "land", "the next pass"},
	"conflict-exhausted":             {"needs a cause", "land", "cause not recorded"},
	"item-unstagleable":              {"needs a cause", "queue", "cause not recorded"},
	// A terminal item leaves this view, so the rule names the settlement for history reads.
	"item-already-terminal": {"moving", "lane", "the next pass settles the run without judging it"},
	// Historical decisions remain readable after a planner removes a reason.
	"fence-overlap": {"waits on work", "queue", "cause not recorded"},
}

func waitingRuleFor(reason string) waitingRule {
	if strings.HasPrefix(reason, "workflow-invalid: ") {
		return waitingRules["workflow-invalid"]
	}
	for _, named := range []string{"node-failed", "node-scope-outside-fence", "boundary-scope-escape"} {
		if strings.HasPrefix(reason, named+": ") {
			return waitingRules[named]
		}
	}
	if rule, ok := waitingRules[reason]; ok {
		return rule
	}
	// Drain appends the seat/backend to this one reason. Preserve the full reason
	// in Why and recognize only the declared prefix, never arbitrary new reasons.
	if strings.HasPrefix(reason, "trunk-rewritten: ") {
		return waitingRules["trunk-rewritten"]
	}
	if strings.HasPrefix(reason, "red-cap: ") {
		return waitingRules["red-cap"]
	}
	if strings.HasPrefix(reason, "trunk-path-dirty: ") {
		return waitingRules["trunk-path-dirty"]
	}
	if strings.HasPrefix(reason, "inputs-changed: ") {
		return waitingRules["inputs-changed"]
	}
	if strings.HasPrefix(reason, "seat-degraded: seat ") {
		return waitingRules["seat-degraded"]
	}
	return waitingRule{"needs a cause", "queue", "add a history rule for this recorded reason"}
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
				return addGraphRevisionHistory(r, rows)
			})
			s.Close()
		}
	}
	if err == nil {
		rows, err = policyWaitingRows(*root, rows)
	}
	if err != nil {
		var mismatch schemaMismatch
		if errors.As(err, &mismatch) {
			fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
			return record.Envelope{}, record.ExitIncompatible
		}
		return record.Envelope{}, exitFor(err)
	}
	sortWaitingDocuments(rows)
	if *asJSON {
		err = json.NewEncoder(os.Stdout).Encode(rows)
	} else {
		err = printWaiting(os.Stdout, rows)
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
	out := []waitingRow{}
	for _, item := range items {
		if waitingTerminal(item.State) {
			continue
		}
		row := waitingRow{Rank: item.Rank, ItemID: item.ItemID, Class: "waits on the loop", Where: "queue", Why: "not yet planned", Mover: "the next pass", Since: "no pass yet", ClassSource: "rule"}
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
					row.Mover = answer.Questions[0].QuestionID
					if c := conflicts[item.ItemID]; c.TriageVersion != "" {
						row.Mover = "owner; answer " + row.Mover
						row.Why += ": " + answer.Questions[0].Text
					}
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
				row.Class = "needs you"
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
				row.Class = "needs you"
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
	extra, err := waitingCommentIntake(r)
	if err != nil {
		return nil, err
	}
	laneRows, err := waitingLaneConditions(r)
	if err != nil {
		return nil, err
	}
	all, err := appendWaitingDesigns(r, append(append(append(out, windows...), extra...), laneRows...))
	if err != nil {
		return nil, err
	}
	if err := addGraphRevisionHistory(r, all); err != nil {
		return nil, err
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
		out = append(out, waitingRow{ItemID: "backend:" + backend, Class: "waits on work", Where: "backend", Why: kind + " reopens " + reopens, Mover: "the reset time", Since: "—", ClassSource: "store"})
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

var waitingStages = []string{"queue", "lane", "fence", "gate", "land", "trunk"}

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
   WHEN kind = 'gate.verdict' THEN 3
   WHEN kind = 'drain.refusal' THEN CASE json_extract(CASE WHEN json_valid(payload) THEN payload ELSE '{}' END, '$.step')
    WHEN 'no-op' THEN 1 WHEN 'fence' THEN 2 WHEN 'gate' THEN 3
    WHEN 'land' THEN 4 WHEN 'conflict' THEN 4 ELSE 0 END
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

func printWaiting(w io.Writer, rows []waitingRow) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "rank\titem_id\tclass\twhere\twhy\tmover\tsince"); err != nil {
		return err
	}
	for _, row := range rows {
		rank := row.Rank
		if rank == "" {
			rank = "—"
		}
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
		cells := []string{rank, identity, row.Class, row.Where, why, row.Mover, row.Since}
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
