package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Broken-trunk detection and healing.
//
// This file is the loop's half of one rule: a red gate is a suspect until the failing
// checks are replayed on the base the candidate was staged from. A check that is red
// there is the trunk's failure, not the candidate's, and the item is acquitted while one
// deduplicated fixer item is filed against the red sha. A check that is green on the base
// stays the candidate's problem and takes the ordinary gate refusal.
//
// The three words this file uses:
//
//   - *suspect*: a red gate whose failing checks have not been replayed on the base yet.
//   - *trunk-already-red*: the replay reproduced the failure on the base, so the trunk is
//     red and the candidate is not at fault. The item is acquitted, never given a failing
//     verdict.
//   - *fixer*: the one item filed against a red trunk sha. It is a build item, ranked
//     first, fenced to what the suspect change and the failing checks can reach, and
//     forbidden from editing the gate's own code so it cannot pass by weakening the judge.
//
// The recursion exemption keeps the fixer from healing itself: a fixer is never acquitted
// by a base replay (its whole purpose is to be red on the base), it gates the full
// untiered range since the last green, and it rebases onto the trunk before it gates so
// the tree that lands is the tree that was judged.

const (
	// TrunkFixerPrefix is the item-id prefix of a fixer item, so a store read can answer
	// "is a fixer open" without a second index.
	TrunkFixerPrefix = "fixer-main-"
	// TrunkFixerRank places a fixer at the head of the queue: the trunk is red for every
	// other item, so a fixer that waited behind ordinary work would hold the whole queue.
	TrunkFixerRank = "000"
	// TrunkFixerSeat is the role a fixer asks for. A fixer is implementation work.
	TrunkFixerSeat = item.SeatBuild
	// RefusalTrunkAlreadyRed is the `drain.refusal` step a base replay writes when it
	// reproduces the gate's failure on the base. It is a refusal about the trunk, not a
	// verdict about the candidate.
	RefusalTrunkAlreadyRed = "trunk-already-red"
	// TrunkFixerIDLength is how many hex characters of the red sha the item id carries:
	// long enough to be unique in practice, short enough to read.
	TrunkFixerIDLength = 12
)

// Rolling cadence. The criteria fix these, so they are constants rather than a policy key:
// a rolling run every RollingEveryLandings landings, every RollingEvery of wall clock, and
// once before the queue drains.
const (
	RollingEveryLandings = 8
	RollingEvery         = 20 * time.Minute
)

// DefaultRollingProgram is the rolling tier's default program: the gate's rolling tier over
// the range since the watermark, waiting as long as an ordinary landing gate for the record
// lock. `--lock-timeout 30` is not decoration: a rolling run and a gate contend for the same
// lock, and a rolling run that refused at once would report "could not judge" every time a
// lane was gating. It matches `tillr-loop`'s gate argv for the same reason.
const DefaultRollingProgram = "python3 tools/fork-check.py --tier rolling {since} --lock-timeout 30"

// GateCodePaths is the gate's own code. A fixer's fence is the union of the suspect
// change and the failing checks' watched paths, with these removed: a fixer that could
// edit the gate could make the red disappear by weakening the judge, which is the one
// repair that must never be available to it. The entries are repo-relative prefixes.
var GateCodePaths = []string{
	"tools/fork-check.py",
	"tests/fork-check.test.py",
	"tools/tillr-gate/",
	"tools/tillr-loop/",
	"tools/preflight-declarations.json",
	"tools/gate-timing-declarations.json",
}

// TrunkFixerID answers the item id for one red sha: `fixer-main-<red_sha12>`.
func TrunkFixerID(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > TrunkFixerIDLength {
		sha = sha[:TrunkFixerIDLength]
	}
	return TrunkFixerPrefix + strings.ToLower(sha)
}

// IsTrunkFixer answers whether an item is a fixer.
func IsTrunkFixer(itemID string) bool {
	return strings.HasPrefix(itemID, TrunkFixerPrefix)
}

// TrunkFixerSHA answers the sha a fixer id names, or "" when it names none.
func TrunkFixerSHA(itemID string) string {
	if !IsTrunkFixer(itemID) {
		return ""
	}
	return strings.TrimPrefix(itemID, TrunkFixerPrefix)
}

// GateCode reports whether a repo-relative path is the gate's own code.
func GateCode(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, entry := range GateCodePaths {
		if path == strings.TrimSuffix(entry, "/") || strings.HasPrefix(path, entry) {
			return true
		}
	}
	return false
}

// TrunkFixerFence answers the fixer's fence: the union of the suspect paths and the
// failing checks' watched paths, minus the gate's own code.
//
// The subtraction is what makes the recursion exemption real rather than decorative. A
// fixer may repair anything the red change or a failing check touches; it may not reach
// the program that decides whether it passed. A fence that ends up empty is a refusal to
// file at all, which the caller reports rather than filing a fixer that cannot work.
func TrunkFixerFence(suspect []string, watches []string) []string {
	fence, _ := trunkFixerPaths(suspect, watches)
	return fence
}

// trunkFixerPaths also names paths that cannot belong to a repository-relative fence.
// Reject traversal components rather than cleaning them into a different permission.
func trunkFixerPaths(suspect []string, watches []string) ([]string, []string) {
	all := append(append([]string{}, suspect...), watches...)
	seen := map[string]bool{}
	fence, dropped := []string{}, []string{}
	for _, path := range all {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		unsafe := filepath.IsAbs(path) || path == "." || strings.HasPrefix(path, "./")
		for _, part := range strings.Split(path, "/") {
			unsafe = unsafe || part == ".."
		}
		if unsafe {
			dropped = append(dropped, path)
		} else if !GateCode(filepath.ToSlash(filepath.Clean(path))) {
			fence = append(fence, path)
		}
	}
	sort.Strings(fence)
	sort.Strings(dropped)
	return fence, dropped
}

// CheckWatches answers the paths a set of failing checks watches: every read and write
// path the gate's plan declared for them, in check-id order so the fixture fence is
// stable. A check whose watch set the plan did not carry contributes nothing.
func CheckWatches(watches map[string][]string, checks []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	ids := append([]string{}, checks...)
	sort.Strings(ids)
	for _, id := range ids {
		for _, path := range watches[id] {
			path = filepath.ToSlash(strings.TrimSpace(path))
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

// ReplayOutcome is what a replay of a set of checks answered on one revision.
//
// Known is the three-state honesty the rest of this repository uses: a replay program
// that is not there, or one that could not judge, answers Known=false, and the loop then
// refuses to acquit. Unknown is never "green": an unread replay leaves the gate's refusal
// standing, because the alternative is acquitting a candidate nobody cleared.
type ReplayOutcome struct {
	// Rev is the revision the replay ran on.
	Rev string
	// Red is the subset of the named checks that are red on Rev.
	Red []string
	// Known reports whether the replay actually ran.
	Known bool
}

// ReplayStep is the optional capability a Steps implementation may carry: replay a named
// set of checks on one revision without judging a candidate. ProcessSteps implements it;
// a stub that does not simply gets no acquittal, which is the conservative direction.
type ReplayStep interface {
	Replay(ctx context.Context, candidate Candidate, rev string, checks []string) (ReplayOutcome, error)
}

// RollingOutcome is one rolling run's answer.
type RollingOutcome struct {
	// Green reports that every rolling check passed on the trunk range.
	Green bool
	// Known reports whether the rolling program reached a judgment. Unknown advances nothing.
	Known bool
	// ExitCode is the rolling program's exit code, or -1 when it could not start.
	ExitCode int
	// Detail is why the run did not judge: the program's stderr tail for any exit that is not
	// a verdict, or the start failure when there was no exit code to report.
	Detail string
	// FailingChecks is the checks that failed, when the program named them. A red run that
	// named none is Known=false: the loop files no fixer against a guess.
	FailingChecks []string
	// CheckWatches maps a failing check to the paths its span recorded writing. It is the
	// fixer's fence when the checks are named, and the range is only the fallback.
	CheckWatches map[string][]string
	// SuspectPaths is the range the run examined. The fixer's fence falls back to it when
	// the failing checks are not named.
	SuspectPaths []string
}

// RollingStep is the optional capability a Steps implementation may carry: run the rolling
// tier over a trunk range. ProcessSteps implements it; a stub without it means the loop
// asks for no rolling run and the watermark never advances.
type RollingStep interface {
	Rolling(ctx context.Context, baseSHA, tipSHA string) (RollingOutcome, error)
	// RollingConfigured reports whether a rolling program is named. A loop with none asks
	// for no run and writes no state, rather than spending a store write per tick to record
	// that it could not judge.
	RollingConfigured() bool
}

// rollingDDL is drain's rolling state: one row of the live watermark, and an append-only
// log of the runs that moved (or refused to move) it.
//
// The state row is the watermark the next run starts from and the landing count since it
// last advanced. The log is the evidence: a reader can see every red range that did not
// advance the watermark, which is the thing a single mutable row would erase.
const rollingDDL = `
CREATE TABLE IF NOT EXISTS rolling_state (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  watermark_sha TEXT NOT NULL DEFAULT '',
  landings_since INTEGER NOT NULL DEFAULT 0,
  last_run_at   INTEGER NOT NULL DEFAULT 0,
  last_tip_sha  TEXT NOT NULL DEFAULT '',
  last_outcome  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS rolling_runs (
  run_id         TEXT PRIMARY KEY,
  base_sha       TEXT NOT NULL,
  tip_sha        TEXT NOT NULL,
  outcome        TEXT NOT NULL,
  failing_checks TEXT NOT NULL,
  at             INTEGER NOT NULL
);
`

// rollingState is the watermark and the landings since it last advanced.
type rollingState struct {
	WatermarkSHA  string
	LandingsSince int
	LastRunAt     time.Time
	// LastTipSHA and LastOutcome remember the last run, so the forced run before
	// StopQueueDrained does not repeat a red run on a tip that has not moved. A red tip is
	// already held by its fixer; running the same range again would prove the same red.
	LastTipSHA  string
	LastOutcome string
}

// loadRollingState reads the one state row, answering the zero value when it is absent.
func (l *Loop) loadRollingState(ctx context.Context) (rollingState, error) {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return rollingState{}, Incompatiblef("%v", err)
	}
	defer s.Close()
	var (
		state rollingState
		last  int64
	)
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		row := r.QueryRow(`SELECT watermark_sha, landings_since, last_run_at, last_tip_sha, last_outcome FROM rolling_state WHERE id = 1`)
		scanErr := row.Scan(&state.WatermarkSHA, &state.LandingsSince, &last, &state.LastTipSHA, &state.LastOutcome)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil
		}
		return scanErr
	})
	if err != nil {
		return rollingState{}, readFailure(err)
	}
	if last > 0 {
		state.LastRunAt = time.UnixMilli(last).UTC()
	}
	return state, nil
}

// saveRollingState writes the one state row.
func (l *Loop) saveRollingState(ctx context.Context, state rollingState) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO rolling_state (id, watermark_sha, landings_since, last_run_at, last_tip_sha, last_outcome)
			 VALUES (1, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   watermark_sha = excluded.watermark_sha,
			   landings_since = excluded.landings_since,
			   last_run_at = excluded.last_run_at,
			   last_tip_sha = excluded.last_tip_sha,
			   last_outcome = excluded.last_outcome`,
			state.WatermarkSHA, state.LandingsSince, state.LastRunAt.UnixMilli(),
			state.LastTipSHA, state.LastOutcome,
		); err != nil {
			return Internalf("writing the rolling state: %v", err)
		}
		return nil
	})
}

// recordRollingRun appends one rolling run to the log.
func (l *Loop) recordRollingRun(ctx context.Context, base, tip string, out RollingOutcome) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	checks, err := json.Marshal(out.FailingChecks)
	if err != nil {
		return Internalf("encoding the rolling failing checks: %v", err)
	}
	outcome := "red"
	if out.Green {
		outcome = "green"
	} else if !out.Known {
		outcome = "unknown"
	}
	id := "rolling-" + stamp(l.cfg.Now().UTC()) + "-" + shortSHA(tip)
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO rolling_runs (run_id, base_sha, tip_sha, outcome, failing_checks, at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, base, tip, outcome, string(checks), l.cfg.Now().UTC().UnixMilli(),
		); err != nil {
			return Internalf("recording the rolling run: %v", err)
		}
		return nil
	})
}

// rollingUnknownStreak is how many of the newest rolling runs in a row ended without a judgment.
// A store that cannot be read answers 0: the count only decorates a line, and a loop must not
// stop because it could not decorate one.
func (l *Loop) rollingUnknownStreak(ctx context.Context) int {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return 0
	}
	defer s.Close()
	streak := 0
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT outcome FROM rolling_runs ORDER BY at DESC, run_id DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var outcome string
			if err := rows.Scan(&outcome); err != nil || outcome != "unknown" {
				break
			}
			streak++
		}
		return nil
	})
	return streak
}

// bumpLandings advances the landing count the rolling trigger reads. It is called once per
// landed candidate, and it persists so a loop restarted mid-night does not forget how much
// trunk the rolling tier has not proved.
//
// It is one atomic UPSERT and not a read followed by a write: the count is taken inside a
// lane's goroutine, and a read transaction there would hold a shared lock against the
// other lane's gate verdict in the store's rollback-journal mode. One short write keeps
// the concurrency the two-lane case exists to prove.
func (l *Loop) bumpLandings(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO rolling_state (id, landings_since) VALUES (1, 1)
			 ON CONFLICT(id) DO UPDATE SET landings_since = landings_since + 1`,
		); err != nil {
			return Internalf("recording a landing: %v", err)
		}
		return nil
	})
}

// rollingRuns is how many rolling runs this loop has made, green or red. The tick uses it
// to tell "the forced run happened" from "the forced run was not due", so it re-plans only
// when a run could have filed a fixer.
func (l *Loop) rollingRuns(result *LoopResult) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return result.RollingGreen + result.RollingRed
}

// trunkAlreadyRed replays the gate's failing checks on the candidate's base and then on
// the trunk head, and answers whether either reproduced the failure.
//
// The order is deliberate. The base is the candidate's own starting trunk, so a red there
// names exactly the sha the candidate was staged from; the trunk head is the next best
// answer when the candidate was staged from an older verified commit. A check that is red
// on the base is the acquittal; a check that is green on the base and red on the head is
// still a broken trunk, just one that moved after the candidate started.
//
// A fixer item is never acquitted here: its whole purpose is to be red on the base, and
// replaying its own failure would let it heal itself by doing nothing.
func (l *Loop) trunkAlreadyRed(ctx context.Context, candidate Candidate, gate GateOutcome) (string, []string, bool, error) {
	if IsTrunkFixer(candidate.ItemID) {
		return "", nil, false, nil
	}
	replay, ok := l.cfg.Steps.(ReplayStep)
	if !ok || len(gate.FailingChecks) == 0 {
		return "", nil, false, nil
	}
	base := candidate.BaseSHA
	if base == "" {
		base = l.laneBase(candidate.LaneID)
	}
	revs := make([]string, 0, 2)
	if base != "" {
		revs = append(revs, base)
	}
	head, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return "", nil, false, err
	}
	if head != "" && head != base {
		revs = append(revs, head)
	}
	for _, rev := range revs {
		out, err := replay.Replay(ctx, candidate, rev, gate.FailingChecks)
		if err != nil {
			return "", nil, false, err
		}
		if !out.Known {
			// The replay could not answer. Keep looking at the next revision, but never
			// read "could not replay" as "green".
			continue
		}
		if len(out.Red) > 0 {
			red := out.Rev
			if red == "" {
				red = rev
			}
			return red, out.Red, true, nil
		}
	}
	return "", nil, false, nil
}

// acquitTrunkAlreadyRed is the gate-failure fork: it answers true when the failure was
// the trunk's and the item has been acquitted (a refusal recorded, a fixer filed, the
// item held blocked), and false when the ordinary gate refusal should stand.
func (l *Loop) acquitTrunkAlreadyRed(ctx context.Context, candidate Candidate, gate GateOutcome, result *LoopResult) (bool, error) {
	redSHA, redChecks, ok, err := l.trunkAlreadyRed(ctx, candidate, gate)
	if err != nil || !ok {
		return false, err
	}
	fixerID, err := l.fileTrunkFixer(ctx, redSHA, redChecks, gate)
	if err != nil {
		return false, err
	}
	if err := l.recordTrunkRefusal(ctx, candidate, redSHA, redChecks, fixerID); err != nil {
		return false, err
	}
	if err := l.cfg.Steps.ItemState(ctx, candidate.ItemID, candidate.RunID, StateBlocked, item.WaitingOn{Waiter: "item:" + fixerID, Reason: "trunk-already-red: " + strings.Join(redChecks, ", ")}); err != nil {
		return false, err
	}
	l.mu.Lock()
	result.TrunkAcquitted++
	l.mu.Unlock()
	l.reportf("loop: %s acquitted as trunk-already-red: %s is red on %s, so %s is held blocked by %s\n",
		candidate.ItemID, strings.Join(redChecks, ", "), shortSHA(redSHA), candidate.ItemID, fixerID)
	return true, nil
}

// recordTrunkRefusal appends the `drain.refusal` that names the red sha and the failing
// checks, and the fixer the item is held behind.
func (l *Loop) recordTrunkRefusal(ctx context.Context, candidate Candidate, redSHA string, redChecks []string, fixerID string) error {
	return appendJudgment(ctx, l.cfg.Store, RefusalKind, "run", candidate.RunID, refusalPayload{
		Step:          RefusalTrunkAlreadyRed,
		ItemID:        candidate.ItemID,
		LaneID:        candidate.LaneID,
		TickID:        candidate.TickID,
		RedSHA:        redSHA,
		FailingChecks: redChecks,
		FixerItem:     fixerID,
	}, l.cfg.Now().UTC())
}

// fileTrunkFixer files the one fixer for a red sha, and answers the item id. A sha that
// already has a fixer — open or landed — answers that existing id and writes nothing: the
// deduplication is the point, because a red trunk detected by ten lanes must not file ten
// fixers that fight over one branch.
//
// The item is written through item.Apply, the item package's one record→row mapping, so a
// fixer cannot be a state the item writer would refuse.
func (l *Loop) fileTrunkFixer(ctx context.Context, redSHA string, failing []string, gate GateOutcome) (string, error) {
	fixerID := TrunkFixerID(redSHA)
	fence, dropped := trunkFixerPaths(gate.SuspectPaths, CheckWatches(gate.CheckWatches, failing))
	if len(fence) == 0 {
		// A fixer with no reachable paths cannot repair anything and would be dispatchable
		// forever. Refusing to file it is the honest answer; the trunk is still red and the
		// item is still held, so a person sees both facts.
		return "", Externalf("the trunk is red at %s but the fixer fence is empty: the suspect change and the failing checks (%s) name no path outside the gate's own code, so no fixer can be filed",
			shortSHA(redSHA), strings.Join(failing, ", "))
	}
	criteria := "the full gate is green at " + shortSHA(redSHA) + ", and the next rolling run advances the watermark"
	if len(failing) > 0 {
		// The brief names what was red. A fixer told only the sha has to re-run the gate to
		// find out, and a rolling red already recorded its failing checks.
		criteria = "the full gate is green at " + shortSHA(redSHA) + " (red: " + strings.Join(failing, ", ") + "), and the next rolling run advances the watermark"
	}
	// A generated fixer has no body reference: its criteria are the whole brief body.
	if len(dropped) > 0 {
		criteria += ". Dropped paths outside the repository-relative fence: " + strings.Join(dropped, ", ")
	}
	payload, err := json.Marshal(map[string]any{
		"item_id":       fixerID,
		"title":         "fix the broken trunk at " + shortSHA(redSHA),
		"state":         StateInQueue,
		"blocked_by":    []string{},
		"rank":          TrunkFixerRank,
		"allowed_paths": fence,
		"seat":          TrunkFixerSeat,
		"criteria": []map[string]string{{
			"criterion_id": "trunk-green",
			"text":         criteria,
			"state":        "not-done",
		}},
	})
	if err != nil {
		return "", Internalf("encoding the fixer item: %v", err)
	}
	env := record.Envelope{
		RecordVersion: 1,
		Kind:          item.RecordKind,
		ID:            "item-" + fixerID,
		Producer:      record.Producer{System: "tillr", Utility: Utility, Version: Version},
		CreatedAt:     l.cfg.Now().UTC(),
		Payload:       payload,
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return "", Incompatiblef("%v", err)
	}
	defer s.Close()
	created := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var state string
		scanErr := tx.QueryRow(`SELECT state FROM items WHERE item_id = ?`, fixerID).Scan(&state)
		switch {
		case scanErr == nil:
			// A fixer for this sha already exists. Open or landed, it is the one fixer for
			// the sha; filing a second would be two writers on one repair.
			return nil
		case errors.Is(scanErr, sql.ErrNoRows):
		default:
			return readFailure(scanErr)
		}
		if _, err := item.Apply(ctx, tx, env); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return "", classifyWrite(err)
	}
	if created {
		l.reportf("loop: filed %s for the broken trunk at %s (fence %s)\n",
			fixerID, shortSHA(redSHA), strings.Join(fence, ", "))
	}
	return fixerID, nil
}

// trunkFixerOpen answers the open fixer id, or "" when the trunk is green.
//
// Open means a fixer item the store holds in a state that is not terminal. The planner
// reads this once per tick and holds every ordinary item behind it: dispatching ordinary
// work onto a red trunk would stage it from a base whose checks never passed, which is
// the defect the whole file exists to prevent.
func trunkFixerOpen(items []itemRow) string {
	for _, item := range items {
		if IsTrunkFixer(item.ItemID) && !terminalStates[item.State] {
			return item.ItemID
		}
	}
	return ""
}

// maybeRolling runs the rolling tier when its cadence says so, and files the same fixer
// when it is red. force runs it before the queue drains, which is the one cadence the
// landing count and the clock cannot express: the trunk must be proved green before the
// service goes idle, or a red range waits until someone arrives.
//
// A green run advances the watermark to the tip it judged and resets the landing count. A
// red run advances neither: the watermark is the newest trunk commit the rolling tier has
// proved, and a red range proves nothing. An unknown run — the program was not there or
// could not judge — likewise advances nothing.
func (l *Loop) maybeRolling(ctx context.Context, mainSHA string, force bool, result *LoopResult) error {
	step, ok := l.cfg.Steps.(RollingStep)
	if !ok || !step.RollingConfigured() || mainSHA == "" {
		return nil
	}
	state, err := l.loadRollingState(ctx)
	if err != nil {
		return err
	}
	now := l.cfg.Now().UTC()
	due := force ||
		state.LandingsSince >= RollingEveryLandings ||
		state.LastRunAt.IsZero() ||
		now.Sub(state.LastRunAt) >= RollingEvery
	if !due || state.WatermarkSHA == mainSHA {
		return nil
	}
	// Do not repeat an unchanged red or unknown judgment on a forced idle check.
	// Another immediate attempt would change nothing; the waiting daemon can poll many times
	// before a new tip arrives. Ordinary clock and landing cadence still retry.
	if force && state.LastTipSHA == mainSHA && (state.LastOutcome == "red" || state.LastOutcome == "red-unfiled" || state.LastOutcome == "unknown") {
		return nil
	}
	base := state.WatermarkSHA
	out, err := step.Rolling(ctx, base, mainSHA)
	if err != nil {
		return err
	}
	state.LastRunAt = now
	state.LastTipSHA = mainSHA
	state.LastOutcome = "red"
	if !out.Known || (!out.Green && len(out.FailingChecks) == 0) {
		out.Known = false
		state.LastOutcome = "unknown"
		if err := l.recordRollingRun(ctx, base, mainSHA, out); err != nil {
			return err
		}
		// The streak is read back from the log just written, so it counts across loop
		// restarts. A tier that has never judged is the one an operator needs to see: it
		// advances nothing and files nothing, so a red trunk would go unnoticed behind it.
		streak := l.rollingUnknownStreak(ctx)
		where := "the watermark stays at " + shortSHA(state.WatermarkSHA)
		span := shortSHA(base) + ".." + shortSHA(mainSHA)
		if base == "" {
			where = "there is no watermark yet, because no rolling run has judged the trunk"
			span = "the whole tree up to " + shortSHA(mainSHA)
		}
		l.reportf("loop: the rolling run over %s could not judge (exit %d); %s; continuing dispatch. %s\n",
			span, out.ExitCode, where, strings.TrimSpace(out.Detail))
		if streak > 1 || (streak == 1 && base == "") {
			noun := "runs have"
			if streak == 1 {
				noun = "run has"
			}
			l.reportf("loop: %d consecutive rolling %s not judged the trunk, so the rolling tier is giving no protection until one does\n", streak, noun)
		}
		return l.saveRollingState(ctx, state)
	}
	if out.Green {
		state.WatermarkSHA = mainSHA
		state.LandingsSince = 0
		state.LastOutcome = "green"
		if err := l.recordRollingRun(ctx, base, mainSHA, out); err != nil {
			return err
		}
		if err := l.saveRollingState(ctx, state); err != nil {
			return err
		}
		l.mu.Lock()
		result.RollingGreen++
		l.mu.Unlock()
		l.reportf("loop: rolling run green over %s..%s; watermark advances to %s\n",
			shortSHA(base), shortSHA(mainSHA), shortSHA(mainSHA))
		return nil
	}
	// Red: file the fixer for the tip and leave the watermark where it was.
	//
	// The failing checks' watched paths are the fence when the run named them; the range is
	// the fallback for a run that named no path. A first run has no range at all, so the
	// whole tree is the fence — and that is a broad fence deliberately, because a narrower
	// guess would leave the actual breakage outside the one item allowed to repair it.
	suspect := out.SuspectPaths
	watch := CheckWatches(out.CheckWatches, out.FailingChecks)
	if len(suspect) == 0 && len(watch) == 0 {
		suspect = l.rangePaths(ctx, base, mainSHA)
	}
	fixerGate := GateOutcome{SuspectPaths: suspect, FailingChecks: out.FailingChecks, CheckWatches: out.CheckWatches}
	fixerID, fixerErr := l.fileTrunkFixer(ctx, mainSHA, out.FailingChecks, fixerGate)
	if fixerErr != nil {
		l.reportf("loop: could not file %s: %v; continuing dispatch\n", TrunkFixerID(mainSHA), fixerErr)
		// There is no fixer holding the queue. Remember the red judgment and the
		// refused filing separately so the report never claims an item holds it.
		state.LastOutcome = "red-unfiled"
	}
	if err := l.recordRollingRun(ctx, base, mainSHA, out); err != nil {
		return err
	}
	if err := l.saveRollingState(ctx, state); err != nil {
		return err
	}
	l.mu.Lock()
	result.RollingRed++
	l.mu.Unlock()
	if fixerErr == nil {
		l.reportf("loop: rolling run RED over %s..%s (%s); watermark stays at %s and the queue is held behind %s\n",
			shortSHA(base), shortSHA(mainSHA), strings.Join(out.FailingChecks, ", "),
			shortSHA(state.WatermarkSHA), fixerID)
	}
	return nil
}

// rangePaths is the changed-path list between two revisions, used as the rolling run's
// suspect paths when the rolling program did not name them.
//
// An empty base is the first rolling run: nothing has been proved yet, so every tracked
// path is a suspect, and the fixer's fence is the whole tree minus the gate's own code.
// That is a broad fence and deliberately so — a narrower guess on the first run would
// leave the actual breakage outside the one item allowed to repair it.
func (l *Loop) rangePaths(ctx context.Context, base, tip string) []string {
	args := []string{"diff", "--name-only", base + ".." + tip}
	if base == "" {
		args = []string{"ls-tree", "-r", "--name-only", tip}
	}
	out, err := l.git(ctx, l.cfg.Root, args...)
	if err != nil {
		return nil
	}
	paths := make([]string, 0)
	for _, line := range strings.Split(out, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// preRebaseFixer rebases a fixer branch onto the current trunk before it is gated, so the
// tree that lands is the tree that was judged. A fixer can run for minutes while ordinary
// work lands around it; gating the pre-rebase tree and landing the post-land tree would
// judge one tree and ship another.
//
// A fixer that cannot be rebased cleanly answers an error: the trunk moved under it, and a
// conflict is a person's to resolve rather than a gate's to guess at.
func (l *Loop) preRebaseFixer(ctx context.Context, candidate Candidate) (Candidate, error) {
	if !IsTrunkFixer(candidate.ItemID) {
		return candidate, nil
	}
	mainSHA, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return candidate, err
	}
	if mainSHA == "" || mainSHA == candidate.BaseSHA {
		return candidate, nil
	}
	// The fixer's gate is the full untiered range since the last green. The watermark is
	// read once here, on the tick's own goroutine, so the gate step never opens a store
	// read while a sibling lane is writing.
	if state, err := l.loadRollingState(ctx); err == nil {
		candidate.FixerBaseSHA = state.WatermarkSHA
	}
	if _, err := l.git(ctx, candidate.Worktree, "rebase", mainSHA); err != nil {
		return candidate, Externalf("rebasing the fixer %s onto %s failed: %v", candidate.ItemID, shortSHA(mainSHA), err)
	}
	head, err := l.revParse(ctx, candidate.Worktree, "HEAD")
	if err != nil {
		return candidate, err
	}
	candidate.BaseSHA = mainSHA
	candidate.HeadSHA = head
	// The loop moved this branch, not the worker. Persist that binding before
	// the gate can hold it, so restart/clearance compares against the prepared
	// head rather than falsely convicting the loop's own rebase.
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return candidate, err
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, candidatePreparedKind, candidate.RunID, candidate, l.cfg.Now().UTC())
	})
	s.Close()
	if err != nil {
		return candidate, err
	}
	l.reportf("loop: pre-rebased %s onto %s so the gated tree is the landed tree\n",
		candidate.ItemID, shortSHA(mainSHA))
	return candidate, nil
}
