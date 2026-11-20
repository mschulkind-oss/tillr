// Provenance, count parity, and refusal invariants: docs/reference/observatory-design.md
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/ownercriteria"
	"github.com/mschulkind-oss/tillr-drain/presence"
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
	OwnerOwed    *ownercriteria.OwnerCriterion
	ItemID       string
	Title        string
	BodyRef      string
	State        string
	DesignDoc    string
	UpdatedAtMS  int64
	AllowedPaths []string
	// Rank is the item's place in the queue's order (schema 5): a sparse lexical token,
	// empty for an item the queue sorts after every ranked one.
	Rank          string
	Waiter        string
	WaitingReason string
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
	ActiveNodes   []string
	LaneID        string
	State         string
	RunID         string
	LeasedUntilMS *int64
	// ResumedSeq is the seq of the latest run.resume for the run holding the lease. A
	// resume newer than that run's `runs` row is a continuation of the same run, which
	// keeps its earlier (paused) row until it finishes: the lease is live work, not a
	// finished run settling.
	ResumedSeq int64
	// DeadRunID and DeadResumedSeq preserve the lease's run identity when a leased run
	// is proven dead before the loop reaps it, so lanes can report its outcome as
	// "interrupted" while projecting the lane itself as idle.
	DeadRunID      string
	DeadResumedSeq int64
}

// storeRun is one row of runs, with the spend the run recorded. UsageTokens and CostUSD
// are pointers because "the backend reported nothing" and "the run cost zero" are
// different answers, and a 0 in their place would be a measurement nobody made.
type storeRun struct {
	TranscriptPath string // Only a recorded child session supplies an alternate log.
	RunID          string
	ItemID         string
	LaneID         string
	Backend        string
	Seat           string
	Workflow       string
	Node           string
	State          string
	ExitCode       *int
	StartedAtMS    int64
	FinishedAtMS   int64
	EventSeq       int64
	UsageTokens    *int64
	CostUSD        *float64
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
	items, err := readItemsBase(r)
	if err != nil {
		return nil, err
	}
	owed, err := ownercriteria.ReadOwnerCriteria(r)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].OwnerOwed = owed[items[i].ItemID]
	}
	rows, err := r.Query(`SELECT item_id, waiting_on FROM items WHERE state = 'blocked'`)
	if isMissingColumn(err) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waiting := map[string]struct {
		Waiter string `json:"waiter"`
		Reason string `json:"reason"`
	}{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if raw == "" {
			continue
		}
		w := waiting[id]
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, fmt.Errorf("item %s blocked metadata: %w", id, err)
		}
		waiting[id] = w
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Waiter, items[i].WaitingReason = waiting[items[i].ItemID].Waiter, waiting[items[i].ItemID].Reason
	}
	return items, nil
}

func readItemsBase(r *store.Reader) ([]storeItem, error) {
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
	if it.State == "blocked" {
		var raw string
		err := r.QueryRow(`SELECT COALESCE(waiting_on, '') FROM items WHERE item_id = ?`, itemID).Scan(&raw)
		if isMissingColumn(err) {
			return it, true, nil // Legacy stores did not record structured waiters.
		}
		if err != nil {
			return storeItem{}, false, err
		}
		if raw != "" {
			var waiting struct {
				Waiter string `json:"waiter"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(raw), &waiting); err != nil {
				return storeItem{}, false, fmt.Errorf("item %s blocked metadata: %w", itemID, err)
			}
			it.Waiter, it.WaitingReason = waiting.Waiter, waiting.Reason
		}
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

// readItemVerdicts reads the event log, where the verdict writer stores authorship.
// Sequence order retains repeated judgments of the same criterion.
func readItemVerdicts(r *store.Reader, itemID string) ([]verdictView, error) {
	rows, err := r.Query(`SELECT payload, ts FROM events
		WHERE kind = 'item.verdict' AND object_type = 'item' AND object_id = ? ORDER BY seq`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []verdictView{}
	for rows.Next() {
		var payload string
		var ts int64
		if err := rows.Scan(&payload, &ts); err != nil {
			return nil, err
		}
		var v verdictView
		if err := json.Unmarshal([]byte(payload), &v); err != nil {
			return nil, err
		}
		if v.RecordedAt == "" {
			v.RecordedAt = time.UnixMilli(ts).UTC().Format(time.RFC3339Nano)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// readClosedBlockers names abandoned prerequisites; a landed item is not an alarm.
func readClosedBlockers(r *store.Reader, itemID, waiter string) ([]string, error) {
	waiterID := ""
	if strings.HasPrefix(waiter, "item:") {
		waiterID = strings.TrimPrefix(waiter, "item:")
	}
	// Reading items once also deduplicates a waiter recorded as an edge as well.
	rows, err := r.Query(`SELECT item_id FROM items WHERE state = 'closed'
		AND (item_id IN (SELECT blocked_by FROM item_edges WHERE item_id = ?) OR item_id = ?)
		ORDER BY item_id`, itemID, waiterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
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

func readerStorePath(r *store.Reader) string {
	if r == nil {
		return ""
	}
	rows, err := r.Query(`PRAGMA database_list`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return ""
		}
		if name == "main" {
			if file == ":memory:" {
				return ""
			}
			return file
		}
	}
	return ""
}

func defaultLoopDir(storePath, loopDir string) string {
	if loopDir != "" {
		return loopDir
	}
	if storePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(storePath), "loop")
}

// readLanes answers every lane, ordered by id.
func readLanes(r *store.Reader) ([]storeLane, error) {
	return readLanesIn(r, "")
}

func readLanesIn(r *store.Reader, loopDir string) ([]storeLane, error) {
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
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	hasLeased := false
	for i := range out {
		if out[i].State != laneStateLeased {
			continue
		}
		hasLeased = true
		if out[i].RunID == "" {
			continue
		}
		if err := r.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ?`, resumeKind, out[i].RunID).Scan(&out[i].ResumedSeq); err != nil && !isMissingTable(err) {
			return nil, err
		}
	}
	exists, err := hasTable(r, "read_only_runs")
	if err != nil {
		return nil, err
	}
	if exists {
		members, err := r.Query(`SELECT lane_id,run_id FROM read_only_runs WHERE settled=0 ORDER BY run_id`)
		if err != nil {
			return nil, err
		}
		defer members.Close()
		for members.Next() {
			var lane, id string
			if err = members.Scan(&lane, &id); err != nil {
				return nil, err
			}
			for i := range out {
				if out[i].LaneID == lane && out[i].State == laneStateLeased {
					out[i].ActiveNodes = append(out[i].ActiveNodes, id)
				}
			}
		}
		if err = members.Err(); err != nil {
			return nil, err
		}
	}
	if hasLeased {
		storePath := readerStorePath(r)
		dead, err := presence.DeadRuns(r, storePath, defaultLoopDir(storePath, loopDir))
		if err != nil && !isMissingTable(err) {
			return nil, err
		}
		for i := range out {
			if out[i].State != laneStateLeased {
				continue
			}
			if len(out[i].ActiveNodes) > 0 {
				kept := out[i].ActiveNodes[:0]
				for _, id := range out[i].ActiveNodes {
					if !dead[id] {
						kept = append(kept, id)
					}
				}
				if len(kept) == 0 {
					out[i].ActiveNodes = nil
				} else {
					out[i].ActiveNodes = kept
				}
			}
			if dead[out[i].RunID] && len(out[i].ActiveNodes) == 0 {
				out[i].DeadRunID = out[i].RunID
				out[i].DeadResumedSeq = out[i].ResumedSeq
				out[i].State = "idle"
				out[i].RunID = ""
				out[i].LeasedUntilMS = nil
				out[i].ResumedSeq = 0
				out[i].ActiveNodes = nil
			}
		}
	}
	return out, nil
}

const runsSelectColumns = `run_id, COALESCE(item_id, ''), lane_id, backend, COALESCE(seat, ''), COALESCE(workflow, ''), COALESCE(node, ''), state, exit_code, started_at, finished_at, usage_tokens, usage_cost_usd, event_seq`

// queryRuns runs a `runs` SELECT and projects any absent schema-migration columns
// (seat, workflow, node, usage_tokens, usage_cost_usd) as empty/NULL so read-only opens
// against pre-change stores succeed without migrating.
func queryRuns(r *store.Reader, query string, args ...any) (*sql.Rows, error) {
	rows, err := r.Query(query, args...)
	for isMissingColumn(err) {
		switch {
		case strings.Contains(err.Error(), "no such column: seat"):
			query = strings.Replace(query, "COALESCE(seat, '')", "''", 1)
		case strings.Contains(err.Error(), "no such column: workflow"):
			query = strings.Replace(query, "COALESCE(workflow, '')", "''", 1)
		case strings.Contains(err.Error(), "no such column: node"):
			query = strings.Replace(query, "COALESCE(node, '')", "''", 1)
		case strings.Contains(err.Error(), "no such column: usage_tokens"):
			query = strings.Replace(query, "usage_tokens", "NULL", 1)
		case strings.Contains(err.Error(), "no such column: usage_cost_usd"):
			query = strings.Replace(query, "usage_cost_usd", "NULL", 1)
		default:
			return nil, err
		}
		rows, err = r.Query(query, args...)
	}
	return rows, err
}

// readRuns answers every run, ordered by start so a caller's list reads as a history.
func readRuns(r *store.Reader) ([]storeRun, error) {
	rows, err := queryRuns(r, `SELECT `+runsSelectColumns+` FROM runs ORDER BY started_at, run_id`)
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
	return readRunOrLiveIn(r, runID, "")
}

func readRunOrLiveIn(r *store.Reader, runID, loopDir string) (storeRun, bool, error) {
	run, found, err := readRun(r, runID)
	if err != nil || found {
		return run, found, err
	}
	child, childFound, childErr := readChildRun(r, runID)
	if childErr != nil || childFound {
		return child, childFound, childErr
	}
	lanes, err := readLanesIn(r, loopDir)
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
	rows, err := queryRuns(r, `SELECT `+runsSelectColumns+` FROM runs WHERE run_id = ?`, runID)
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
	rows, err := queryRuns(r, `SELECT `+runsSelectColumns+` FROM runs WHERE item_id = ? ORDER BY started_at, run_id`, itemID)
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
	if err := sc.Scan(&run.RunID, &run.ItemID, &run.LaneID, &run.Backend, &run.Seat, &run.Workflow, &run.Node, &run.State, &exitCode,
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
		p.Underlying = "waiter not recorded"
		p.WaitingOn = waitingOn
		if item.Waiter != "" {
			p.Underlying = item.Waiter + ": " + item.WaitingReason
			p.WaitingOn = []string{item.Waiter}
		}
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
			p.Underlying = "waiting on " + strings.Join(waitingOn, ", ")
		}
		if item.State != "blocked" || item.Waiter == "" {
			p.WaitingOn = waitingOn
		}
	}
	// A question hold is the loop's latest decision about the item, so it wins over the
	// store's own state: the store says in-queue, and the planner says the design still
	// asks something. A terminal item keeps its terminal state; a stale hold cannot reopen
	// work that has landed.
	if hold != "" && (item.State != "blocked" || item.Waiter == "") && p.State != "landed" && p.State != "resolved_elsewhere" && p.State != "closed" {
		p.State = "blocked"
		p.Hold = hold
		p.Underlying = hold
	}
	return p
}

func closedBlockersFor(it storeItem, waitingOn []string, states map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range waitingOn {
		if states[id] == "closed" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if depID, ok := strings.CutPrefix(it.Waiter, "item:"); ok && states[depID] == "closed" && !seen[depID] {
		seen[depID] = true
		out = append(out, depID)
	}
	sort.Strings(out)
	return out
}

func projectClosedBlockers(p *itemProjection, closedBlockers []string) {
	if len(closedBlockers) == 0 || p.State == "landed" || p.State == "resolved_elsewhere" || p.State == "closed" {
		return
	}
	p.State = "blocked"
	noun := "item"
	if len(closedBlockers) > 1 {
		noun = "items"
	}
	if p.Underlying == "" || p.Underlying == "waiter not recorded" || strings.HasPrefix(p.Underlying, "waiting on ") {
		p.Underlying = fmt.Sprintf("blocked by closed %s (%s) without landing; owner/operator: remove the dependency or reopen the closed %s",
			noun, strings.Join(closedBlockers, ", "), noun)
	}
	if len(p.WaitingOn) == 0 {
		p.WaitingOn = closedBlockers
	}
}

// traceReader answers a run's recorded observations by asking `tillr-trace run <id>`,
// which parses that run's artifacts (its log, proc-exec.jsonl and gate records) on demand
// and prints one JSON document. Nothing is cached and nothing is written: the owner ruled on
// 2026-11-18 that the persistent trace index was derived data to remove until a measured
// performance problem asks for a cache. One parser serves the CLI and the observatory, so
// the claim verdicts the observatory shows are the ones `tillr-trace audit` prints.
type traceReader struct {
	bin     string
	store   string
	loopDir string
	state   string
	note    string
	// load runs the program for one run and answers its stdout; a test replaces it.
	load func(runID string) ([]byte, error)
}

// newTraceReader resolves the program once. A missing program is an absence the caller
// reports rather than an error: the store alone answers some of every question.
func newTraceReader(bin, storePath, loopDir string) *traceReader {
	t := &traceReader{bin: bin, store: storePath, loopDir: loopDir}
	if bin == "" {
		t.state = "absent"
		t.note = "no tillr-trace program was named or found, so a run's own recorded streams are not read"
		return t
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.state = "absent"
		t.note = fmt.Sprintf("the tillr-trace program %s is not there: %v", bin, err)
		return t
	}
	t.state = "present"
	t.load = t.exec
	return t
}

// available says whether a run's recorded streams can be read at all.
func (t *traceReader) available() bool { return t.state == "present" && t.load != nil }

func (t *traceReader) exec(runID string) ([]byte, error) {
	args := []string{"--store", t.store}
	if t.loopDir != "" {
		args = append(args, "--loop-dir", t.loopDir)
	}
	cmd := exec.Command(t.bin, append(args, "run", runID)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s run %s: %v: %s", t.bin, runID, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// runTrace is one run as tillr-trace read it from the run's artifacts. Found is false for a
// run whose id neither the store nor a run directory knows.
type runTrace struct {
	Found  bool
	Row    runIndex
	Events []obsEvent
	Claims []claimCheck
}

// run reads one run's artifacts. Each call parses them again, so a handler reads once and
// passes the answer on; a large run (268,000 subprocess rows) takes about ten seconds.
func (t *traceReader) run(runID string) (runTrace, error) {
	if !t.available() {
		return runTrace{}, nil
	}
	raw, err := t.load(runID)
	if err != nil {
		return runTrace{}, err
	}
	var doc struct {
		Run *struct {
			RunID     string  `json:"run_id"`
			LogPath   *string `json:"log_path"`
			LogState  string  `json:"log_state"`
			ProcPath  *string `json:"proc_path"`
			ProcState string  `json:"proc_state"`
			GitPath   *string `json:"git_path"`
			GitState  string  `json:"git_state"`
			BriefPath *string `json:"brief_path"`
			FencePath *string `json:"fence_path"`
			BaseSHA   *string `json:"base_sha"`
			Detail    string  `json:"detail"`
		} `json:"run"`
		Events []struct {
			ObsID      string          `json:"obs_id"`
			Kind       string          `json:"kind"`
			TSStart    string          `json:"ts_start"`
			TSEnd      *string         `json:"ts_end"`
			DurationMS *int64          `json:"duration_ms"`
			Seam       string          `json:"seam"`
			SpanID     string          `json:"span_id"`
			Summary    string          `json:"summary"`
			Payload    json.RawMessage `json:"payload"`
		} `json:"events"`
		Claims []struct {
			ClaimID           string  `json:"claim_id"`
			ClaimKind         string  `json:"claim_kind"`
			ClaimText         string  `json:"claim_text"`
			ObservedStatus    string  `json:"observed_status"`
			DiscrepancyReason *string `json:"discrepancy_reason"`
			ObservedAt        string  `json:"observed_at"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return runTrace{}, fmt.Errorf("%s run %s answered a document that is not the run shape: %v", t.bin, runID, err)
	}
	if doc.Run == nil {
		return runTrace{}, nil
	}
	nonEmpty := func(v *string) *string {
		if v == nil || *v == "" {
			return nil
		}
		return v
	}
	r := doc.Run
	out := runTrace{Found: true, Row: runIndex{RunID: runID, LogPath: nonEmpty(r.LogPath), LogState: r.LogState,
		ProcPath: nonEmpty(r.ProcPath), ProcState: r.ProcState, GitPath: nonEmpty(r.GitPath), GitState: r.GitState,
		BriefPath: nonEmpty(r.BriefPath), FencePath: nonEmpty(r.FencePath), BaseSHA: nonEmpty(r.BaseSHA), Detail: r.Detail}}
	for _, e := range doc.Events {
		out.Events = append(out.Events, obsEvent{ObsID: e.ObsID, Kind: e.Kind, TSStart: e.TSStart, TSEnd: e.TSEnd,
			DurationMS: e.DurationMS, Seam: e.Seam, SpanID: e.SpanID, Summary: e.Summary, Payload: string(e.Payload)})
	}
	for _, c := range doc.Claims {
		reason := ""
		if c.DiscrepancyReason != nil {
			reason = *c.DiscrepancyReason
		}
		out.Claims = append(out.Claims, claimCheck{ClaimID: c.ClaimID, ClaimKind: c.ClaimKind, ClaimText: c.ClaimText,
			ObservedStatus: c.ObservedStatus, DiscrepancyReason: reason, ObservedAt: c.ObservedAt})
	}
	return out, nil
}

// runIndex is a run's row as tillr-trace reads it: which of the run's artifact streams
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

// obsEvent is one recorded observation of a run: a model turn, a tool call, a process
// execution or a gate record. Events are in the recorded order; an empty list means
// nothing was recorded and is not evidence that nothing ran.
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

// claimCheck is one of the four temporal verification predicates tillr-trace answers for
// a run.
type claimCheck struct {
	ClaimID           string
	ClaimKind         string
	ClaimText         string
	ObservedStatus    string
	DiscrepancyReason string
	ObservedAt        string
}

// allocation is the run.allocate event a lane claim appends: the run's start as recorded
// before any terminal record exists. It is the live half of the ledger — the `runs` table's
// own comment says the row is created by the terminal write, so a run that is still going
// has an event and no row (`tools/tillr-run/run/lease.go`, ClaimLease).
type allocation struct {
	RunID       string
	ItemID      string
	Backend     string
	Seat        string
	Workflow    string
	Node        string
	LaneID      string
	StartedAtMS int64
}

// allocateKind is `run.AllocateKind` from tools/tillr-run, spelled here because this module
// does not depend on that one: the reader wants one string, and a dependency for it would
// pull the lease machinery into the read-only face. tests/tillr-observatory-*.test.* pins
// the two spellings equal.
const allocateKind = "run.allocate"

// resumeKind is `run.ResumeKind` from tools/tillr-run, spelled here for the same reason as
// allocateKind: a continuation's claim of its lane, newer than the run's terminal row.
const resumeKind = "run.resume"

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
			Seat      string `json:"seat"`
			Workflow  string `json:"workflow"`
			Node      string `json:"node"`
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
			Seat:        body.Seat,
			Workflow:    body.Workflow,
			Node:        body.Node,
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
