package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// The epistemic states every span, exchange and artifact carries (design §2.3). They are
// the whole point of the observatory: a gap that renders as a zero or a green is worse
// than a gap, so each one is a closed spelling rather than a hint.
//
//	observed        a record exists and says this happened, with a recorded clock
//	derived_clock   the record exists but its time was inferred (a file's mtime, or a
//	                turn whose clock was copied from the preceding one)
//	unobserved_seam nothing recorded it: the emitter is not wired yet, and the row is
//	                here to be seen as missing
//	unreadable      the source exists and could not be read
const (
	stateObserved       = "observed"
	stateDerivedClock   = "derived_clock"
	stateUnobservedSeam = "unobserved_seam"
	stateUnreadable     = "unreadable"
)

// The store's own tables, spelled here because a read face must not depend on the
// package that registered their DDL. The columns are the ones this face selects, and a
// select that names a column a store does not have is answered as an unreadable section
// rather than a 500, so a half-migrated store still answers what it can.
var (
	errTableMissing = errors.New("the table is not in this store")
)

// isMissingTable answers whether a read failed because the store does not carry the
// table: a store created by a program that never linked that utility's package has no
// items table, and that is an absence to report, not a failure to hide.
func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// isMissingColumn answers whether a read failed because a column is not in this store.
// A store OLDER than this binary is read (schemaSupported), and a column a later schema
// added — items.rank is schema 5's — is simply not there yet: the row answers the fact
// the column would have carried as its zero value, which for a rank is "unranked".
func isMissingColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such column")
}

// storeItem is one row of the store's items table.
type storeItem struct {
	ItemID       string
	Title        string
	BodyRef      string
	State        string
	DesignDoc    string
	UpdatedAtMS  int64
	AllowedPaths []string
	// Rank is the item's place in the queue's order (schema 5): a sparse lexical token,
	// empty for an item the queue sorts after every ranked one.
	Rank string
}

// storeCriterion is one row of item_criteria.
type storeCriterion struct {
	ItemID      string
	CriterionID string
	Text        string
	State       string
}

// storeEdge is one unresolved blocked_by edge.
type storeEdge struct {
	ItemID    string
	BlockedBy string
}

// storeLane is one row of lanes.
type storeLane struct {
	LaneID        string
	State         string
	RunID         string
	LeasedUntilMS *int64
}

// storeRun is one row of runs, with the spend the run recorded. UsageTokens and CostUSD
// are pointers because "the backend reported nothing" and "the run cost zero" are
// different answers, and a 0 in their place would be a measurement nobody made.
type storeRun struct {
	RunID        string
	ItemID       string
	LaneID       string
	Backend      string
	State        string
	ExitCode     *int
	StartedAtMS  int64
	FinishedAtMS int64
	EventSeq     int64
	UsageTokens  *int64
	CostUSD      *float64
}

// storeCandidate is one row of candidates.
type storeCandidate struct {
	CandidateID string
	ItemID      string
	BaseRef     string
	BaseSHA     string
	HeadSHA     string
	TreeSHA     string
	State       string
	EnqueuedMS  int64
}

// storeAttempt is one row of landing_attempts.
type storeAttempt struct {
	AttemptID   string
	CandidateID string
	VerdictID   string
	Result      string
	Reason      string
	TrunkBefore string
	TrunkAfter  string
}

// storeDispatch is one row of dispatches: the loop's own decision, written by the tick
// that made it.
type storeDispatch struct {
	DecisionID   string
	ItemID       string
	LaneID       string
	RunID        string
	Action       string
	Reason       string
	DispatchedMS int64
	PassID       string
}

// nullableString reads a TEXT column that may be NULL.
func nullableString(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

// readItems answers every item in the store, ordered by id so two reads of one moment
// answer the same order. The queue's rank is selected with it: the board's order is
// rank first, and a store too old to carry the column answers the same rows unranked
// rather than refusing the whole board.
func readItems(r *store.Reader) ([]storeItem, error) {
	rows, err := r.Query(`SELECT item_id, title, COALESCE(body_ref, ''), state,
		COALESCE(design_doc, ''), updated_at, COALESCE(allowed_paths, ''), COALESCE(rank, '') FROM items ORDER BY item_id`)
	if isMissingColumn(err) {
		return readItemsUnranked(r)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeItem
	for rows.Next() {
		var it storeItem
		var paths string
		if err := rows.Scan(&it.ItemID, &it.Title, &it.BodyRef, &it.State, &it.DesignDoc,
			&it.UpdatedAtMS, &paths, &it.Rank); err != nil {
			return nil, err
		}
		if it.AllowedPaths, err = decodeAllowedPaths(paths); err != nil {
			return nil, fmt.Errorf("item %s: %w", it.ItemID, err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// readItemsUnranked is readItems for a store that predates the rank column: the same
// rows, every one unranked.
func readItemsUnranked(r *store.Reader) ([]storeItem, error) {
	rows, err := r.Query(`SELECT item_id, title, COALESCE(body_ref, ''), state,
		COALESCE(design_doc, ''), updated_at, COALESCE(allowed_paths, '') FROM items ORDER BY item_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeItem
	for rows.Next() {
		var it storeItem
		var paths string
		if err := rows.Scan(&it.ItemID, &it.Title, &it.BodyRef, &it.State, &it.DesignDoc,
			&it.UpdatedAtMS, &paths); err != nil {
			return nil, err
		}
		if it.AllowedPaths, err = decodeAllowedPaths(paths); err != nil {
			return nil, fmt.Errorf("item %s: %w", it.ItemID, err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// decodeAllowedPaths reads the fence's stored form: the list as JSON text, which is the
// shape tillr-item writes into the column (its encodeAllowedPaths). Splitting the column
// on a delimiter instead hands the whole escaped list through as one path, which reads to
// an operator as a fence with a single entry.
//
// An empty column is no fence — every item stored before the column existed — and answers
// an empty list, so an item with no fence is an empty array rather than a list holding an
// empty string. A value that is not JSON is an error rather than a split: this face reports
// a fence it cannot read instead of inventing one it can.
func decodeAllowedPaths(stored string) ([]string, error) {
	if stored == "" {
		return []string{}, nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(stored), &paths); err != nil {
		return nil, fmt.Errorf("reading allowed_paths: %w", err)
	}
	if paths == nil {
		return []string{}, nil
	}
	return paths, nil
}

// readItem answers one item, and whether the store carries it.
func readItem(r *store.Reader, itemID string) (storeItem, bool, error) {
	var it storeItem
	var paths string
	err := r.QueryRow(`SELECT item_id, title, COALESCE(body_ref, ''), state,
		COALESCE(design_doc, ''), updated_at, COALESCE(allowed_paths, ''), COALESCE(rank, '')
		FROM items WHERE item_id = ?`, itemID).
		Scan(&it.ItemID, &it.Title, &it.BodyRef, &it.State, &it.DesignDoc, &it.UpdatedAtMS, &paths, &it.Rank)
	if isMissingColumn(err) {
		err = r.QueryRow(`SELECT item_id, title, COALESCE(body_ref, ''), state,
			COALESCE(design_doc, ''), updated_at, COALESCE(allowed_paths, '')
			FROM items WHERE item_id = ?`, itemID).
			Scan(&it.ItemID, &it.Title, &it.BodyRef, &it.State, &it.DesignDoc, &it.UpdatedAtMS, &paths)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return storeItem{}, false, nil
	}
	if err != nil {
		return storeItem{}, false, err
	}
	if it.AllowedPaths, err = decodeAllowedPaths(paths); err != nil {
		return storeItem{}, false, fmt.Errorf("item %s: %w", itemID, err)
	}
	return it, true, nil
}

// readCriteria answers every criterion of one item, in the item's own order.
func readCriteria(r *store.Reader, itemID string) ([]storeCriterion, error) {
	rows, err := r.Query(`SELECT item_id, criterion_id, text, state FROM item_criteria
		WHERE item_id = ? ORDER BY criterion_id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeCriterion
	for rows.Next() {
		var c storeCriterion
		if err := rows.Scan(&c.ItemID, &c.CriterionID, &c.Text, &c.State); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// readEdges answers the item's blocked_by edges, ordered by the blocking item.
func readEdges(r *store.Reader, itemID string) ([]storeEdge, error) {
	rows, err := r.Query(`SELECT item_id, blocked_by FROM item_edges WHERE item_id = ? ORDER BY blocked_by`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeEdge
	for rows.Next() {
		var e storeEdge
		if err := rows.Scan(&e.ItemID, &e.BlockedBy); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// readLanes answers every lane, ordered by id.
func readLanes(r *store.Reader) ([]storeLane, error) {
	rows, err := r.Query(`SELECT lane_id, state, COALESCE(run_id, ''), leased_until FROM lanes ORDER BY lane_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeLane
	for rows.Next() {
		var l storeLane
		var leased sql.NullInt64
		if err := rows.Scan(&l.LaneID, &l.State, &l.RunID, &leased); err != nil {
			return nil, err
		}
		if leased.Valid {
			ms := leased.Int64
			l.LeasedUntilMS = &ms
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// readRuns answers every run, ordered by start so a caller's list reads as a history.
func readRuns(r *store.Reader) ([]storeRun, error) {
	rows, err := r.Query(`SELECT run_id, COALESCE(item_id, ''), lane_id, backend, state, exit_code,
		started_at, finished_at, usage_tokens, usage_cost_usd, event_seq FROM runs ORDER BY started_at, run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// readRunOrLive answers one run from the store, or the run a lane is still holding. The
// `runs` row is written by the terminal write, so a run that is going has none — and the one
// moment a reader wants a run's transcript is while it is working. The projection is the
// snapshot's own (`liveRuns`), so the two answers agree rather than being two readings of a
// lease.
func readRunOrLive(r *store.Reader, runID string) (storeRun, bool, error) {
	run, found, err := readRun(r, runID)
	if err != nil || found {
		return run, found, err
	}
	lanes, err := readLanes(r)
	if err != nil {
		return storeRun{}, false, nil
	}
	allocations, err := readAllocations(r)
	if err != nil {
		return storeRun{}, false, nil
	}
	for _, live := range liveRuns(lanes, nil, allocations) {
		if live.RunID == runID {
			return live, true, nil
		}
	}
	return storeRun{}, false, nil
}

// readRun answers one run, and whether the store carries it.
func readRun(r *store.Reader, runID string) (storeRun, bool, error) {
	rows, err := r.Query(`SELECT run_id, COALESCE(item_id, ''), lane_id, backend, state, exit_code,
		started_at, finished_at, usage_tokens, usage_cost_usd, event_seq FROM runs WHERE run_id = ?`, runID)
	if err != nil {
		return storeRun{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return storeRun{}, false, rows.Err()
	}
	run, err := scanRun(rows)
	if err != nil {
		return storeRun{}, false, err
	}
	return run, true, nil
}

// readItemRuns answers the runs dispatched for one item.
func readItemRuns(r *store.Reader, itemID string) ([]storeRun, error) {
	rows, err := r.Query(`SELECT run_id, COALESCE(item_id, ''), lane_id, backend, state, exit_code,
		started_at, finished_at, usage_tokens, usage_cost_usd, event_seq FROM runs
		WHERE item_id = ? ORDER BY started_at, run_id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// rowScanner is what both *sql.Rows and *sql.Row offer the one run scan.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(sc rowScanner) (storeRun, error) {
	var run storeRun
	var exitCode, tokens sql.NullInt64
	var cost sql.NullFloat64
	if err := sc.Scan(&run.RunID, &run.ItemID, &run.LaneID, &run.Backend, &run.State, &exitCode,
		&run.StartedAtMS, &run.FinishedAtMS, &tokens, &cost, &run.EventSeq); err != nil {
		return storeRun{}, err
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		run.ExitCode = &code
	}
	if tokens.Valid {
		n := tokens.Int64
		run.UsageTokens = &n
	}
	if cost.Valid {
		usd := cost.Float64
		run.CostUSD = &usd
	}
	return run, nil
}

// readItemCandidates answers the candidates enqueued for one item, newest last.
func readItemCandidates(r *store.Reader, itemID string) ([]storeCandidate, error) {
	rows, err := r.Query(`SELECT candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at
		FROM candidates WHERE item_id = ? ORDER BY enqueued_at, candidate_id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeCandidate
	for rows.Next() {
		var c storeCandidate
		if err := rows.Scan(&c.CandidateID, &c.ItemID, &c.BaseRef, &c.BaseSHA, &c.HeadSHA,
			&c.TreeSHA, &c.State, &c.EnqueuedMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// readAttempts answers the landing attempts for one candidate.
func readAttempts(r *store.Reader, candidateID string) ([]storeAttempt, error) {
	rows, err := r.Query(`SELECT attempt_id, candidate_id, verdict_id, result, COALESCE(reason, ''),
		trunk_before, trunk_after FROM landing_attempts WHERE candidate_id = ? ORDER BY event_seq, attempt_id`, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeAttempt
	for rows.Next() {
		var a storeAttempt
		if err := rows.Scan(&a.AttemptID, &a.CandidateID, &a.VerdictID, &a.Result, &a.Reason,
			&a.TrunkBefore, &a.TrunkAfter); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// readCandidates answers every candidate in the store, ordered by enqueue time.
func readCandidates(r *store.Reader) ([]storeCandidate, error) {
	rows, err := r.Query(`SELECT candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at
		FROM candidates ORDER BY enqueued_at, candidate_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeCandidate
	for rows.Next() {
		var c storeCandidate
		if err := rows.Scan(&c.CandidateID, &c.ItemID, &c.BaseRef, &c.BaseSHA, &c.HeadSHA,
			&c.TreeSHA, &c.State, &c.EnqueuedMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// readAllAttempts answers every landing attempt in the store.
func readAllAttempts(r *store.Reader) ([]storeAttempt, error) {
	rows, err := r.Query(`SELECT attempt_id, candidate_id, verdict_id, result, COALESCE(reason, ''),
		trunk_before, trunk_after FROM landing_attempts ORDER BY event_seq, attempt_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeAttempt
	for rows.Next() {
		var a storeAttempt
		if err := rows.Scan(&a.AttemptID, &a.CandidateID, &a.VerdictID, &a.Result, &a.Reason,
			&a.TrunkBefore, &a.TrunkAfter); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// readAllDispatches answers every decision the loop wrote, oldest first.
func readAllDispatches(r *store.Reader) ([]storeDispatch, error) {
	rows, err := r.Query(`SELECT decision_id, item_id, COALESCE(lane_id, ''), COALESCE(run_id, ''),
		action, reason, dispatched_at, COALESCE(pass_id, '') FROM dispatches ORDER BY dispatched_at, rowid`)
	legacy := isMissingColumn(err)
	if legacy {
		rows, err = r.Query(`SELECT decision_id, item_id, COALESCE(lane_id, ''), COALESCE(run_id, ''),
		 action, reason, dispatched_at FROM dispatches ORDER BY dispatched_at, decision_id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeDispatch
	for rows.Next() {
		var d storeDispatch
		columns := []any{&d.DecisionID, &d.ItemID, &d.LaneID, &d.RunID, &d.Action, &d.Reason, &d.DispatchedMS}
		if !legacy {
			columns = append(columns, &d.PassID)
		}
		if err := rows.Scan(columns...); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// storeTick is one row of drain_passes: the gate's own wall time for one tick, with the
// phases it was measured beside and the budget it was measured against.
type storeTick struct {
	TickID       string
	StartedAtMS  int64
	StageMS      int64
	DispatchMS   int64
	GateMS       int64
	LandMS       int64
	GateBudgetMS int64
	OverBudget   bool
}

// readDrainTicks answers every tick the loop measured, oldest first. A store written by a
// drain that had no tick table answers no rows rather than failing the board: the trend is a
// measurement the board shows, not the board's subject.
func readDrainTicks(r *store.Reader) ([]storeTick, error) {
	rows, err := r.Query(`SELECT tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
		gate_budget_ms, gate_over_budget FROM drain_passes WHERE COALESCE(gate_count, 1) > 0 ORDER BY started_at, tick_id`)
	if isMissingColumn(err) {
		rows, err = r.Query(`SELECT tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
		 gate_budget_ms, gate_over_budget FROM drain_passes ORDER BY started_at, tick_id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeTick
	for rows.Next() {
		var t storeTick
		var over int
		if err := rows.Scan(&t.TickID, &t.StartedAtMS, &t.StageMS, &t.DispatchMS, &t.GateMS,
			&t.LandMS, &t.GateBudgetMS, &over); err != nil {
			return nil, err
		}
		t.OverBudget = over != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// readItemDispatches answers the loop's decisions about one item, oldest first.
func readItemDispatches(r *store.Reader, itemID string) ([]storeDispatch, error) {
	rows, err := r.Query(`SELECT decision_id, item_id, COALESCE(lane_id, ''), COALESCE(run_id, ''),
		action, reason, dispatched_at, COALESCE(pass_id, '') FROM dispatches WHERE item_id = ? ORDER BY dispatched_at, rowid`, itemID)
	legacy := isMissingColumn(err)
	if legacy {
		rows, err = r.Query(`SELECT decision_id, item_id, COALESCE(lane_id, ''), COALESCE(run_id, ''),
		 action, reason, dispatched_at FROM dispatches WHERE item_id = ? ORDER BY dispatched_at, decision_id`, itemID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeDispatch
	for rows.Next() {
		var d storeDispatch
		columns := []any{&d.DecisionID, &d.ItemID, &d.LaneID, &d.RunID, &d.Action, &d.Reason, &d.DispatchedMS}
		if !legacy {
			columns = append(columns, &d.PassID)
		}
		if err := rows.Scan(columns...); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// countTicks answers how many ticks the loop recorded a decision in. A tick writes one
// decision row per item it considered, so the row count is not the tick count: on the live
// store on 2026-11-14 the two differed by two orders of magnitude, 1548 rows across 27
// ticks, and the header bar drew "1548 ticks". Every row a tick writes carries that tick's
// clock, so the distinct `dispatched_at` values are the ticks.
func countTicks(r *store.Reader) (int, error) {
	var n int
	err := r.QueryRow(`SELECT
	 (SELECT COUNT(*) FROM drain_passes WHERE ended_at IS NOT NULL) +
	 (SELECT COUNT(DISTINCT COALESCE(pass_id, dispatched_at)) FROM dispatches
	  WHERE pass_id IS NULL OR pass_id NOT IN (SELECT tick_id FROM drain_passes WHERE ended_at IS NOT NULL))`).Scan(&n)
	if isMissingColumn(err) || isMissingTable(err) {
		err = r.QueryRow(`SELECT COUNT(DISTINCT dispatched_at) FROM dispatches`).Scan(&n)
	}
	return n, err
}

// countDispatches answers how many decisions the loop recorded, per action.
func countDispatches(r *store.Reader) (map[string]int, error) {
	rows, err := r.Query(`SELECT action, COUNT(*) FROM dispatches GROUP BY action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			return nil, err
		}
		out[action] = n
	}
	return out, rows.Err()
}

// countAttempts answers how many landing attempts ended each way.
func countAttempts(r *store.Reader) (map[string]int, error) {
	rows, err := r.Query(`SELECT result, COUNT(*) FROM landing_attempts GROUP BY result`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var result string
		var n int
		if err := rows.Scan(&result, &n); err != nil {
			return nil, err
		}
		out[result] = n
	}
	return out, rows.Err()
}

// itemProjection is the closed seven-state reading of one item (design §3.1): what the
// operator sees, the raw machinery strings it was read from, and, for a blocked item,
// what it is waiting on.
type itemProjection struct {
	State      string   `json:"state"`
	RawState   string   `json:"raw_state"`
	Underlying string   `json:"underlying,omitempty"`
	WaitingOn  []string `json:"waiting_on,omitempty"`
	// Hold is the loop's own reason for holding the item, when its latest decision is a
	// question hold (`question-open`, `questions-unknown`, `design-doc-missing`). The
	// seven-state projection stays closed: the state is blocked, and the reason is this
	// field, so a rail row can read `question-open` without inventing an eighth state.
	Hold string `json:"hold,omitempty"`
}

// ProjectItemState maps one item row to the seven human states. It is the only place
// the store's raw machinery strings are read as a state, so a view cannot invent an
// eighth one: `in-progress` reads as Working only while a run is live, and a candidate
// the queue holds reads as InQueue whatever the row says.
//
// hold is the loop's latest question-hold reason for the item, or empty when its latest
// decision was not a question hold. It is a reason rather than a state: the item is
// blocked by it, and the state remains one of the seven.
func ProjectItemState(item storeItem, enqueued *storeCandidate, active *storeRun, waitingOn []string, hold string) itemProjection {
	p := itemProjection{State: "not_started", RawState: item.State}
	switch item.State {
	case "landed":
		p.State = "landed"
	case "resolved-elsewhere":
		p.State = "resolved_elsewhere"
	case "closed":
		p.State = "closed"
	case "completed":
		// The legacy spelling stays readable: it is Landed only when a landing attempt
		// says so, because the state name alone is not evidence anything landed.
		if enqueued != nil {
			p.State = "landed"
		}
	case "blocked":
		p.State = "blocked"
		p.Underlying = "the store marks it blocked"
		p.WaitingOn = waitingOn
	case "in-queue":
		p.State = "in_queue"
	case "in-progress":
		// The store's `in-progress` is a retired spelling: it is evidence the item was
		// dispatched, never a claim that anything is running now. It reads as queued, which
		// is where the migration takes the row; the lease below is what decides Working, so
		// a stopped item can no longer read as "8 working" over zero live lanes.
		p.State = "in_queue"
	}
	if enqueued != nil {
		p.State = "in_queue"
	}
	if active != nil && active.State == "running" {
		p.State = "working"
	}
	if len(waitingOn) > 0 && p.State != "landed" && p.State != "resolved_elsewhere" && p.State != "closed" {
		p.State = "blocked"
		if p.Underlying == "" {
			p.Underlying = "an unresolved blocked_by edge"
		}
		p.WaitingOn = waitingOn
	}
	// A question hold is the loop's latest decision about the item, so it wins over the
	// store's own state: the store says in-queue, and the planner says the design still
	// asks something. A terminal item keeps its terminal state; a stale hold cannot reopen
	// work that has landed.
	if hold != "" && p.State != "landed" && p.State != "resolved_elsewhere" && p.State != "closed" {
		p.State = "blocked"
		p.Hold = hold
		p.Underlying = hold
	}
	return p
}

// traceIndex is the tillr-trace index the observatory reads, or the reason it could not.
// It is never created here: the index is derived, and a read face that built one would
// be a second writer of another utility's artifact.
type traceIndex struct {
	db    *sql.DB
	path  string
	state string
	note  string
}

// openTraceIndex opens the trace index read-only. A missing index is an absence the
// caller reports rather than an error: the store alone answers some of every question.
func openTraceIndex(ctx context.Context, path string) *traceIndex {
	t := &traceIndex{path: path}
	if path == "" {
		t.state = "absent"
		t.note = "no trace index path was named; tillr-trace has not run here"
		return t
	}
	if _, err := os.Stat(path); err != nil {
		t.state = "absent"
		t.note = fmt.Sprintf("%s is not there: tillr-trace has not ingested this machine's runs", path)
		return t
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.state = stateUnreadable
		t.note = err.Error()
		return t
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.state = stateUnreadable
		t.note = err.Error()
		return t
	}
	t.db = db
	t.state = "present"
	return t
}

func (t *traceIndex) Close() {
	if t.db != nil {
		t.db.Close()
	}
}

// watermarkNS is the newest source file mtime the index has ingested: the index's own
// cursor, so a client can tell two index states apart.
func (t *traceIndex) watermarkNS() *int64 {
	if t.db == nil {
		return nil
	}
	var ms sql.NullInt64
	if err := t.db.QueryRow(`SELECT MAX(mtime_ns) FROM ingested_files`).Scan(&ms); err != nil || !ms.Valid {
		return nil
	}
	return &ms.Int64
}

// runIndex is a run's row in the trace index: which of the run's three artifact streams
// exist, and at what paths. missing is the honest answer for a file no emitter wrote.
type runIndex struct {
	RunID     string
	LogPath   *string
	LogState  string
	ProcPath  *string
	ProcState string
	GitPath   *string
	GitState  string
	BriefPath *string
	FencePath *string
	BaseSHA   *string
	Detail    string
}

// runRow answers one run's index row, and whether the index carries it.
func (t *traceIndex) runRow(runID string) (runIndex, bool, error) {
	if t.db == nil {
		return runIndex{}, false, nil
	}
	var (
		row                           runIndex
		logPath, procPath, gitPath    sql.NullString
		briefPath, fencePath, baseSHA sql.NullString
	)
	err := t.db.QueryRow(`SELECT run_id, log_path, log_state, proc_path, proc_state, git_path, git_state,
		brief_path, fence_path, base_sha, COALESCE(detail, '') FROM runs_index WHERE run_id = ?`, runID).
		Scan(&row.RunID, &logPath, &row.LogState, &procPath, &row.ProcState, &gitPath, &row.GitState,
			&briefPath, &fencePath, &baseSHA, &row.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return runIndex{}, false, nil
	}
	if err != nil {
		return runIndex{}, false, err
	}
	assign := func(dst **string, src sql.NullString) {
		if src.Valid && src.String != "" {
			v := src.String
			*dst = &v
		}
	}
	assign(&row.LogPath, logPath)
	assign(&row.ProcPath, procPath)
	assign(&row.GitPath, gitPath)
	assign(&row.BriefPath, briefPath)
	assign(&row.FencePath, fencePath)
	assign(&row.BaseSHA, baseSHA)
	row.RunID = runID
	return row, true, nil
}

// obsEvent is one recorded observation of a run: a model turn, a tool call, a process
// execution or a git trace2 region.
type obsEvent struct {
	ObsID      string
	Kind       string
	TSStart    string
	TSEnd      *string
	DurationMS *int64
	Seam       string
	SpanID     string
	Summary    string
	Payload    string
}

// runEvents answers the recorded observations of one run, in the recorded order. It is
// the index's answer to "what went between the tools"; an empty list means nothing was
// recorded and is not evidence that nothing ran.
func (t *traceIndex) runEvents(runID string) ([]obsEvent, error) {
	if t.db == nil {
		return nil, nil
	}
	rows, err := t.db.Query(`SELECT obs_id, kind, ts_start, ts_end, duration_ms, COALESCE(seam, ''),
		COALESCE(span_id, ''), COALESCE(summary, ''), payload FROM obs_events
		WHERE run_id = ? ORDER BY ts_start, seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []obsEvent
	for rows.Next() {
		var e obsEvent
		var end sql.NullString
		var duration sql.NullInt64
		if err := rows.Scan(&e.ObsID, &e.Kind, &e.TSStart, &end, &duration, &e.Seam, &e.SpanID,
			&e.Summary, &e.Payload); err != nil {
			return nil, err
		}
		if end.Valid {
			e.TSEnd = &end.String
		}
		if duration.Valid {
			d := duration.Int64
			e.DurationMS = &d
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// claimCheck is one of the four temporal verification predicates the trace index
// answers for a run.
type claimCheck struct {
	ClaimID           string
	ClaimKind         string
	ClaimText         string
	ObservedStatus    string
	DiscrepancyReason string
	ObservedAt        string
}

// runClaims answers the claim checks the index holds for one run.
func (t *traceIndex) runClaims(runID string) ([]claimCheck, error) {
	if t.db == nil {
		return nil, nil
	}
	rows, err := t.db.Query(`SELECT claim_id, claim_kind, claim_text, observed_status,
		COALESCE(discrepancy_reason, ''), observed_at FROM claim_checks WHERE run_id = ? ORDER BY claim_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []claimCheck
	for rows.Next() {
		var c claimCheck
		if err := rows.Scan(&c.ClaimID, &c.ClaimKind, &c.ClaimText, &c.ObservedStatus,
			&c.DiscrepancyReason, &c.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// allocation is the run.allocate event a lane claim appends: the run's start as recorded
// before any terminal record exists. It is the live half of the ledger — the `runs` table's
// own comment says the row is created by the terminal write, so a run that is still going
// has an event and no row (`tools/tillr-run/run/lease.go`, ClaimLease).
type allocation struct {
	RunID       string
	ItemID      string
	Backend     string
	LaneID      string
	StartedAtMS int64
}

// allocateKind is `run.AllocateKind` from tools/tillr-run, spelled here because this module
// does not depend on that one: the reader wants one string, and a dependency for it would
// pull the lease machinery into the read-only face. tests/tillr-observatory-*.test.* pins
// the two spellings equal.
const allocateKind = "run.allocate"

// readAllocations answers the last allocation per run id. A run is allocated once, so a
// second event for one run id would be a bug worth seeing rather than overwriting silently —
// the last one wins and the read is ordered so that "last" is the store's own sequence.
func readAllocations(r *store.Reader) (map[string]allocation, error) {
	rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, allocateKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]allocation{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var body struct {
			RunID     string `json:"run_id"`
			ItemID    string `json:"item_id"`
			Backend   string `json:"backend"`
			LaneID    string `json:"lane_id"`
			StartedAt string `json:"started_at"`
		}
		if err := json.Unmarshal([]byte(payload), &body); err != nil {
			// An unreadable allocation is not a reason to refuse the board: the run it
			// describes is simply not projected as live, and the lane row still shows the
			// lease. The alternative — answering no board at all — hides every other item
			// behind one bad row.
			continue
		}
		started, _ := time.Parse(time.RFC3339, body.StartedAt)
		out[body.RunID] = allocation{
			RunID:       body.RunID,
			ItemID:      body.ItemID,
			Backend:     body.Backend,
			LaneID:      body.LaneID,
			StartedAtMS: started.UnixMilli(),
		}
	}
	return out, rows.Err()
}

// storeGateSpan is one row of the `gate_spans` table: a gate check's measurement, keyed by
// the verdict it belongs to. The nanosecond bounds are read as int64 and serialized as
// decimal strings by the payload; a JSON number above 2^53 would lose precision in the
// browser, and the whole point of the column is the exact instant.
type storeGateSpan struct {
	SpanID         string
	TraceID        string
	ParentSpanID   string
	VerdictID      string
	RunID          string
	TickID         string
	CheckID        string
	Kind           string
	Label          string
	Status         string
	StartUnixNano  int64
	EndUnixNano    int64
	WaitedMSJSON   string
	AttributesJSON string
}

// gateSpansTable reports whether the store carries the `gate_spans` table. A store created
// before the table existed answers no spans, which is a fact about the store rather than a
// failure to read it: the server is read-only and never migrates a store it is inspecting.
func gateSpansTable(r *store.Reader) (bool, error) {
	var name string
	err := r.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'gate_spans'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return name == "gate_spans", nil
}

// readGateSpans answers one run's executed gate spans, in the order the gate recorded
// them: the plan first, then the checks by their start.
func readGateSpans(r *store.Reader, runID string) ([]storeGateSpan, error) {
	exists, err := gateSpansTable(r)
	if err != nil || !exists {
		return nil, err
	}
	rows, err := r.Query(`SELECT span_id, trace_id, parent_span_id, verdict_id, run_id,
		tick_id, check_id, kind, label, status, start_unix_nano, end_unix_nano,
		waited_ms_json, attributes_json FROM gate_spans WHERE run_id = ?
		ORDER BY start_unix_nano, span_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeGateSpan
	for rows.Next() {
		var span storeGateSpan
		if err := rows.Scan(&span.SpanID, &span.TraceID, &span.ParentSpanID, &span.VerdictID,
			&span.RunID, &span.TickID, &span.CheckID, &span.Kind, &span.Label, &span.Status,
			&span.StartUnixNano, &span.EndUnixNano, &span.WaitedMSJSON,
			&span.AttributesJSON); err != nil {
			return nil, err
		}
		out = append(out, span)
	}
	return out, rows.Err()
}
