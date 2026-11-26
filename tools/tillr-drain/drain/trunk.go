package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// Broken-trunk detection and healing.
//
// This file is the loop's half of one rule: a red gate is a suspect until the failing
// checks are judged on the base the candidate was staged from. A check that is red
// there is the trunk's failure, not the candidate's, and the item is acquitted while one
// deduplicated fixer item is filed against the red sha. A check that is green on the base
// stays the candidate's problem and takes the ordinary gate refusal.
//
// The three words this file uses:
//
//   - *suspect*: a red gate whose failing checks have not been judged on the base yet.
//   - *trunk-already-red*: the replay reproduced the failure on the base, so the trunk is
//     red and the candidate is not at fault. The item is acquitted, never given a failing
//     verdict.
//   - *fixer*: the one item filed against a red trunk sha. It is a build item, ranked
//     first, fenced to what the suspect change and the failing checks can reach, and
//     forbidden from editing the gate's own code so it cannot pass by weakening the judge.
//
// A fixer cannot inherit a failure it was filed to repair. It rebases onto the trunk
// before its ordinary gate, then proves the named repairs on that candidate tree.

const (
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

// DefaultRollingProgram is empty: the declared heavy checks are the after-merge proof,
// and no separate rolling program runs unless the operator names one with --rolling.
const DefaultRollingProgram = ""

// DefaultFixerGateProgram judges a named check after the ordinary candidate gate passes.
// The drain supplies the candidate flags and the proof's selector as --only.
const DefaultFixerGateProgram = loopAdapterProgram + " fixer-gate"

// DefaultReplayProgram re-runs a red gate's failing checks on the base, `{only}` being the
// comma-separated check ids. It is handed no candidate flags: it judges a revision.
const DefaultReplayProgram = "tillr-loop replay --only {only}"

const goAffectedCheckID = "go-affected/affected-tillr-go-test-packages-pass"

// GateCodePaths is the gate's own code. A fixer's fence is the union of the suspect
// change and the failing checks' watched paths, with these removed: a fixer that could
// edit the gate could make the red disappear by weakening the judge, which is the one
// repair that must never be available to it. The entries are repo-relative prefixes.
var GateCodePaths = []string{
	".tillr.toml",
	"tools/tillr-gate/",
	"tools/gate/",
	"tools/tillr-loop/",
	"tools/tillr-drain/drain/fixer_gate.go",
	"tools/gate-timing-declarations.json",
	"yolo-packs/tillr/files/local/share/tillr/tillr-gate/",
	"yolo-packs/tillr/files/local/share/tillr/tillr-loop/",
	"yolo-packs/tillr/files/local/share/tillr/tillr-drain/drain/fixer_gate.go",
}

// TrunkFixerID answers the item id for one red sha: `fixer-main-<red_sha12>`.
func TrunkFixerID(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > TrunkFixerIDLength {
		sha = sha[:TrunkFixerIDLength]
	}
	return TrunkFixerPrefix + strings.ToLower(sha)
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
	return gateCodeWith(path, GateCodePaths)
}

func gateCodeWith(path string, gatePaths []string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, entry := range gatePaths {
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
	fence, _ := trunkFixerPathsWithGate(suspect, watches, GateCodePaths)
	return fence
}

// TrunkFixerFenceWithGate answers TrunkFixerFence using the given gate-protected paths.
func TrunkFixerFenceWithGate(suspect, watches, gatePaths []string) []string {
	fence, _ := trunkFixerPathsWithGate(suspect, watches, gatePaths)
	return fence
}

func (l *Loop) fixerGateCodePaths() []string {
	checksDir := l.postLandingSettings().ChecksDir()
	if l.cfg.Root != "" {
		if s, err := settings.Read(l.cfg.Root); err == nil {
			checksDir = s.ChecksDir()
		}
	}
	return append(append([]string{}, GateCodePaths...), checksDir+"/")
}

// trunkFixerPaths also names paths that cannot belong to a repository-relative fence.
// Reject traversal components rather than cleaning them into a different permission.
func trunkFixerPaths(suspect []string, watches []string) ([]string, []string) {
	return trunkFixerPathsWithGate(suspect, watches, GateCodePaths)
}

func trunkFixerPathsWithGate(suspect, watches, gatePaths []string) ([]string, []string) {
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
		} else if !gateCodeWith(filepath.ToSlash(filepath.Clean(path)), gatePaths) {
			// A broad directory must not grant indirect permission to edit the gate.
			containsGate := false
			for _, gate := range gatePaths {
				containsGate = containsGate || strings.HasPrefix(gate, filepath.ToSlash(filepath.Clean(path))+"/")
			}
			if containsGate {
				dropped = append(dropped, path)
			} else {
				fence = append(fence, path)
			}
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
	// CheckLogs maps a red check id to the output captured during replay.
	CheckLogs     map[string]string
	forkConflicts map[string]forkObservation
}

// ReplayStep is the optional capability a Steps implementation may carry: replay a named
// set of checks on one revision without judging a candidate. ProcessSteps implements it;
// a stub that does not simply gets no acquittal, which is the conservative direction.
type ReplayStep interface {
	Replay(ctx context.Context, candidate Candidate, rev string, checks []string) (ReplayOutcome, error)
}

// RollingOutcome is one rolling run's answer.
type RollingOutcome struct {
	// Coverage preserves what was actually checked beside the judged tip.
	Coverage RollingCoverage
	// Green reports that every rolling check passed on the trunk range.
	Green bool
	// Stalled reports named checks with no output or CPU growth for sixty samples.
	Stalled bool
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
	// CheckWatches maps a failing check to its declared read and write paths.
	// The fence combines those paths with every commit in the red range.
	CheckWatches map[string][]string
	// SuspectPaths is the range the run examined, combined with the check watches.
	SuspectPaths []string
	// CheckLogs maps a failing check id to its log or triage file path.
	CheckLogs map[string]string
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

const rollingOutcomeStalled = "stalled"

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
	skipped, err := json.Marshal(out.Coverage.SkippedChecks)
	if err != nil {
		return Internalf("encoding the rolling skipped checks: %v", err)
	}
	outcome := "red"
	if out.Green {
		outcome = "green"
	} else if out.Stalled {
		outcome = rollingOutcomeStalled
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
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO rolling_coverage (run_id, spans_state, ran, cached, skipped_checks)
			 VALUES (?, ?, ?, ?, ?)`,
			id, out.Coverage.SpansState, out.Coverage.Ran, out.Coverage.Cached, string(skipped),
		); err != nil {
			return Internalf("recording the rolling coverage: %v", err)
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
	if l.gatePolicy.LandingStrategy == "after-merge" {
		tip, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
		if err != nil {
			return err
		}
		return l.startHeavy(ctx, tip)
	}
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

// trunkReading is what a trunk claim's proof lookup and replay found.
//
//   - Confirmed: every named check is red on the candidate's base and, when the trunk has
//     moved past that base, on the trunk tip too. RedSHA is the commit the fixer is filed
//     for: the tip when the base is behind it, the base otherwise.
//   - RepairedAt: the checks are red on the candidate's old base but not all red on the
//     tip, so the trunk is already repaired at that tip and no fixer is filed.
//   - Neither: the failure is the candidate's, or a replay could not judge.
//
// Red is the replay's red subset, so a caller can say what it saw.
type trunkReading struct {
	RedSHA     string
	Red        []string
	Confirmed  bool
	RepairedAt string
	CheckLogs  map[string]string
	Evidence   string // How the base (and tip, if needed) were judged.
}

// trunkAlreadyRed judges the failing checks on the candidate's own base, and then on
// the current trunk tip. Only an all-red base replay can acquit: a newer
// trunk red does not erase the candidate's failure on a check that passed where it started;
// rolling files that newer trunk's independent fixer.
//
// The tip is the second question because work is always built against the latest trunk
// (the owner's ruling of 2026-11-18): a check red only on a base the trunk has moved past
// is a repair that already happened, and a fixer filed for that old base holds every item
// for nothing. On 2026-11-18 a run cut nine hours earlier filed fixer-main-1887afe2c948
// for a check the trunk had long since repaired. The tip run's recorded passes answer for
// the tip where they cover a check; only the rest is replayed there.
//
// A fixer cannot inherit any check it was filed to repair. A mixed refusal is
// returned whole, not split into a new verdict and an inherited failure.
func (l *Loop) trunkAlreadyRed(ctx context.Context, candidate Candidate, gate GateOutcome) (trunkReading, error) {
	// A repair's replay base is outside the trunk proof's scope.
	if IsRemoteSyncItem(candidate.ItemID) {
		return trunkReading{}, nil
	}
	if IsTrunkFixer(candidate.ItemID) {
		// A worker's trunk obstacle is not an ordinary gate verdict.
		if gate.Outcome != GateFailed {
			return trunkReading{}, nil
		}
		named, err := l.fixerNamedChecks(ctx, candidate.ItemID)
		if err != nil {
			return trunkReading{}, err
		}
		for _, failed := range gate.FailingChecks {
			for _, name := range named {
				if fixerCheckOverlaps(name, failed, candidate.Worktree) {
					return trunkReading{Evidence: "the fixer names " + name}, nil
				}
			}
		}
	}
	replay, ok := l.cfg.Steps.(ReplayStep)
	if !ok || len(gate.FailingChecks) == 0 {
		return trunkReading{}, nil
	}
	base := candidate.BaseSHA
	if base == "" {
		base = l.laneBase(candidate.LaneID)
	}
	if base == "" {
		return trunkReading{}, nil
	}
	// A worker's trunk obstacle still requests a base replay. For an actual gate
	// failure, consult the proof first; a fork base is outside that proof's scope.
	useBaseProof := gate.Outcome == GateFailed && candidate.BaseRef == ""
	if gate.Outcome == GateFailed && len(gate.CheckLogs) > 0 {
		candidate.replayCheckLogs = gate.CheckLogs
	}
	out, source, err := l.judgeTrunkChecks(ctx, replay, candidate, base, gate.FailingChecks, useBaseProof)
	reading := trunkReading{Evidence: "base " + source}
	if err != nil || !out.Known {
		return reading, err
	}
	reading.Red, reading.CheckLogs = out.Red, out.CheckLogs
	if IsTrunkFixer(candidate.ItemID) {
		reading.Red = gate.FailingChecks
	}
	if !allIn(gate.FailingChecks, out.Red) {
		return reading, nil
	}
	// A fork-series candidate's base is the fork's own HEAD, which the trunk tip does not
	// describe, so its base answers alone.
	if candidate.BaseRef != "" || l.cfg.Root == "" {
		reading.RedSHA, reading.Confirmed = base, true
		return reading, nil
	}
	tip, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
	if err != nil {
		return reading, err
	}
	if tip == "" || tip == base {
		reading.RedSHA, reading.Confirmed = base, true
		return reading, nil
	}
	tipOut, source, err := l.judgeTrunkChecks(ctx, replay, candidate, tip, gate.FailingChecks, true)
	reading.Evidence += "; tip " + source
	if err != nil || !tipOut.Known {
		// A tip nobody could judge confirms nothing: a fixer for the old base is the
		// defect this reading exists to prevent.
		return reading, err
	}
	if allIn(gate.FailingChecks, tipOut.Red) {
		reading.CheckLogs = joinCheckLogs(out.CheckLogs, tipOut.CheckLogs)
		reading.RedSHA, reading.Confirmed = tip, true
		if !IsTrunkFixer(candidate.ItemID) {
			reading.Red = tipOut.Red
		}
		return reading, nil
	}
	reading.RepairedAt, reading.CheckLogs = tip, nil
	return reading, nil
}

// joinCheckLogs keeps the tip's reference when both replays logged a check.
func joinCheckLogs(base, tip map[string]string) map[string]string {
	if len(tip) == 0 {
		return base
	}
	logs := make(map[string]string, len(base)+len(tip))
	for id, ref := range base {
		logs[id] = ref
	}
	for id, ref := range tip {
		logs[id] = ref
	}
	return logs
}

// allIn answers whether every one of want is in have.
func allIn(want, have []string) bool {
	set := map[string]bool{}
	for _, check := range have {
		set[check] = true
	}
	for _, check := range want {
		if !set[check] {
			return false
		}
	}
	return true
}

// redOnTip answers which of the checks are red on the trunk tip and whether that was judged.
func (l *Loop) redOnTip(ctx context.Context, replay ReplayStep, candidate Candidate, tip string, checks []string) ([]string, map[string]string, bool, error) {
	out, _, err := l.judgeTrunkChecks(ctx, replay, candidate, tip, checks, true)
	return out.Red, out.CheckLogs, out.Known, err
}

// judgeTrunkChecks uses only passes for the exact commit and exact check ID, plus
// green-<rev>.json whole-module coverage for go-affected/affected-tillr-go-test-packages-pass
// (which runs only at landing tier). Missing, unreadable, or red records cannot replace a replay.
func (l *Loop) judgeTrunkChecks(ctx context.Context, replay ReplayStep, candidate Candidate, rev string, checks []string, useProof bool) (ReplayOutcome, string, error) {
	rest := checks
	source := "replay"
	if useProof && l.cfg.Root != "" && filepath.Base(rev) == rev {
		if dir, err := heavyDirectory(ctx, l.cfg.Root); err == nil {
			passed := map[string]bool{}
			if green, err := readGreenProofFile(dir, rev); err == nil && !green.ProvedAt.IsZero() {
				greenPassed := map[string]bool{}
				for _, id := range green.Passed {
					if classifyCheck(checkresult.Observation{
						Check:  id,
						Commit: rev,
						Source: checkresult.CachedRecord,
						Word:   string(checkresult.Pass),
					}).Outcome == checkresult.Pass {
						passed[id] = true
						greenPassed[id] = true
					}
				}
				if !passed[goAffectedCheckID] && slices.Contains(checks, goAffectedCheckID) &&
					l.goAffectedCoveredByGreenProof(ctx, rev, candidate, greenPassed) &&
					classifyCheck(checkresult.Observation{
						Check:  goAffectedCheckID,
						Commit: rev,
						Source: checkresult.CachedRecord,
						Word:   string(checkresult.Pass),
					}).Outcome == checkresult.Pass {
					passed[goAffectedCheckID] = true
				}
			}
			if progress, err := readProgressCheckProof(dir, rev); err == nil {
				for id := range progress.At {
					if classifyCheck(checkresult.Observation{
						Check:  id,
						Commit: rev,
						Source: checkresult.CachedRecord,
						Word:   string(checkresult.Pass),
					}).Outcome == checkresult.Pass {
						passed[id] = true
					}
				}
			}
			rest = nil
			for _, id := range checks {
				if !passed[id] {
					rest = append(rest, id)
				}
			}
			if len(rest) == 0 {
				return ReplayOutcome{Rev: rev, Known: true}, "proof record", nil
			}
			if len(rest) != len(checks) {
				source = "proof record and replay"
			}
		}
	}
	if err := candidate.releaseLaneBeforeReplay(ctx); err != nil {
		return ReplayOutcome{}, source, err
	}
	out, err := restartWork(ctx, l, func(child context.Context) (ReplayOutcome, error) {
		return l.runReplay(child, replay, candidate, rev, rest)
	})
	l.sweepDisabledMarkers(ctx)
	if err != nil || !out.Known || out.Rev != "" && out.Rev != rev {
		return ReplayOutcome{}, source, err
	}
	return out, source, nil
}

// goAffectedCoveredByGreenProof answers whether green-<rev>.json's passed whole-module
// suites cover go-affected/affected-tillr-go-test-packages-pass at rev: either every
// tillr Go module present at rev passed its whole-suite check, or the candidate's
// go-affected failure summary names the failing packages and every module owning one
// passed its whole-suite check in green.Passed.
func (l *Loop) goAffectedCoveredByGreenProof(ctx context.Context, rev string, candidate Candidate, greenPassed map[string]bool) bool {
	if len(greenPassed) == 0 || l.cfg.Root == "" {
		return false
	}
	mods := tillrGoModulesAtRev(ctx, l.cfg.Root, rev)
	if len(mods) == 0 {
		return false
	}
	allModulesGreen := true
	for _, mod := range mods {
		if !greenPassed[tillrModuleProofCheckID(mod)] {
			allModulesGreen = false
			break
		}
	}
	if allModulesGreen {
		return true
	}
	if candidate.RunID == "" {
		return false
	}
	failingPkgs := readGoAffectedFailingPackages(l.cfg.Root, candidate.RunID)
	if len(failingPkgs) == 0 {
		return false
	}
	modPaths := make(map[string]string, len(mods))
	for _, mod := range mods {
		raw, err := heavyGitOutput(ctx, l.cfg.Root, "show", rev+":"+mod+"/go.mod")
		if err != nil {
			return false
		}
		mp := parseGoModPath(raw)
		if mp == "" {
			return false
		}
		modPaths[mod] = mp
	}
	for _, pkg := range failingPkgs {
		owner := ""
		ownerLen := -1
		for _, mod := range mods {
			if pkg == mod {
				owner = mod
				break
			}
			mp := modPaths[mod]
			if (pkg == mp || strings.HasPrefix(pkg, mp+"/")) && len(mp) > ownerLen {
				owner = mod
				ownerLen = len(mp)
			}
		}
		if owner == "" || !greenPassed[tillrModuleProofCheckID(owner)] {
			return false
		}
	}
	return true
}

func tillrModuleProofCheckID(mod string) string {
	kind := "go-tool"
	switch mod {
	case "tools/tillr":
		kind = "go-substrate"
	case "tools/tillr-gate":
		kind = "gate-runner"
	}
	return forkCheckDeclID(kind, mod+"'s whole suite passes", "")
}

func tillrGoModulesAtRev(ctx context.Context, root, rev string) []string {
	out, err := heavyGitOutput(ctx, root, "ls-tree", "-r", "--name-only", "-z", rev, "--", "tools")
	if err != nil || out == "" {
		return nil
	}
	seen := map[string]bool{}
	var mods []string
	for _, entry := range strings.Split(out, "\x00") {
		parts := strings.Split(entry, "/")
		if len(parts) != 3 || parts[0] != "tools" || parts[2] != "go.mod" {
			continue
		}
		if parts[1] != "tillr" && !strings.HasPrefix(parts[1], "tillr-") {
			continue
		}
		mod := "tools/" + parts[1]
		if !seen[mod] {
			seen[mod] = true
			mods = append(mods, mod)
		}
	}
	sort.Strings(mods)
	return mods
}

func readGoAffectedFailingPackages(root, runID string) []string {
	runDir := RunDir(root, runID)
	suffix := "-" + sanitizeLogComponent(goAffectedCheckID) + ".log"
	if entries, err := os.ReadDir(filepath.Join(runDir, "logs")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
				continue
			}
			if raw, err := os.ReadFile(filepath.Join(runDir, "logs", entry.Name())); err == nil {
				if pkgs := parseGoAffectedFailingPackages(string(raw)); len(pkgs) > 0 {
					return pkgs
				}
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(runDir, "gate.stdout")); err == nil {
		if pkgs := parseGoAffectedFailingPackages(string(raw)); len(pkgs) > 0 {
			return pkgs
		}
	}
	return nil
}

func parseGoAffectedFailingPackages(text string) []string {
	var last []string
	var cur []string
	itemPrefix := ""
	inBlock := false
	valid := false
	flush := func() {
		if inBlock && valid && len(cur) > 0 {
			last = cur
		}
		cur = nil
		itemPrefix = ""
		inBlock = false
		valid = false
	}
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		switch line {
		case "go-affected: failing packages and tests:":
			flush()
			inBlock = true
			valid = true
			itemPrefix = "  "
			continue
		case "       | go-affected: failing packages and tests:":
			flush()
			inBlock = true
			valid = true
			itemPrefix = "       |   "
			continue
		}
		if !inBlock {
			continue
		}
		if !strings.HasPrefix(line, itemPrefix) || len(line) <= len(itemPrefix) || line[len(itemPrefix)] == ' ' {
			flush()
			continue
		}
		pkg, _, hasColon := strings.Cut(line[len(itemPrefix):], ": ")
		if !hasColon || pkg == "" || strings.ContainsAny(pkg, " \t") {
			valid = false
			continue
		}
		cur = append(cur, pkg)
	}
	flush()
	return last
}

func parseGoModPath(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line, _, _ = strings.Cut(line, "//")
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return strings.Trim(fields[0], `"`)
			}
		}
	}
	return ""
}

// laneBasedOnTrunk reports whether the lane's recorded base is the trunk's: false for a
// fork-series lane, a workflow-node lane whose node base differs from its item base, and a
// non-landing loop.
func (l *Loop) laneBasedOnTrunk(lane string) bool {
	if l.nonLanding || l.laneForkTarget(lane) != "" {
		return false
	}
	itemBase := l.laneFile(lane, ItemBaseSHAFile)
	return itemBase == "" || itemBase == l.laneFile(lane, BaseSHAFile)
}

// moveLaneOntoTip rebases a returned worker's commits onto the trunk tip and records the
// tip as the lane's base, so the session it goes back to continues on the latest trunk. It
// answers the moved head, or "" when the lane is left as it was: a fork or workflow-node
// lane, whose base is not the trunk's, or a rebase that conflicts (aborted, with the lane
// reset to the head it had).
//
// from is the worker's own head; a gate candidate's lane also carries the loop's
// document-retirement commit, which is not the worker's to inherit.
func (l *Loop) moveLaneOntoTip(ctx context.Context, lane, from, tip string) (string, error) {
	if from == "" || !l.laneBasedOnTrunk(lane) {
		return "", nil
	}
	target := l.laneTarget(lane)
	original, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return "", err
	}
	restore := func() {
		_, _ = l.git(ctx, target, "rebase", "--abort")
		_, _ = l.git(ctx, target, "reset", "-q", "--hard", original)
	}
	if original != from {
		if _, err := l.git(ctx, target, "reset", "-q", "--hard", from); err != nil {
			restore()
			return "", nil
		}
	}
	if _, err := l.git(ctx, target, "rebase", "-q", "--autostash", tip); err != nil {
		l.reportf("loop: lane %s could not be rebased onto %s: %v\n", lane, shortSHA(tip), err)
		restore()
		return "", nil
	}
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return "", err
	}
	for _, name := range []string{BaseSHAFile, ItemBaseSHAFile} {
		if err := os.WriteFile(filepath.Join(l.laneDirPath(lane), name), []byte(tip+"\n"), 0o644); err != nil {
			return "", Externalf("recording lane %s's base: %v", lane, err)
		}
	}
	return head, nil
}

// repairedAtTipDetail is what a worker is told when its trunk claim was red only on the
// base it started from.
func repairedAtTipDetail(checks []string, base, tip, moved string) string {
	detail := fmt.Sprintf("Checks %s are red on this run's base %s but pass on the trunk tip %s: the trunk is repaired at %s. Take in the latest trunk and continue.", strings.Join(checks, ", "), shortSHA(base), shortSHA(tip), shortSHA(tip))
	if moved != "" {
		return detail + fmt.Sprintf(" The loop rebased this lane's commits onto %s, so the lane's base is now %s.", shortSHA(tip), shortSHA(tip))
	}
	return detail + fmt.Sprintf(" The loop could not rebase this lane onto %s without a conflict, so the lane stays on %s.", shortSHA(tip), shortSHA(base))
}

func repairedAtTipReleasedDetail(checks []string, base, tip string) string {
	return fmt.Sprintf("Checks %s are red on this run's base %s but pass on the trunk tip %s: the trunk is repaired at %s. Take in the latest trunk and continue. The lane was released while checking the trunk, so the next run starts from the latest trunk.", strings.Join(checks, ", "), shortSHA(base), shortSHA(tip), shortSHA(tip))
}

// openTrunkRedNotice returns a brief notice naming any open trunk fixers and their
// failing checks when staging a non-fixer run while the trunk is red (OQ-GP7).
// Opened read-only, never under the store write lock.
func openTrunkRedNotice(ctx context.Context, storePath string) (string, error) {
	r, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	var lines []string
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		declared, err := definition.ForReader(reader)
		if err != nil {
			return err
		}
		rows, err := reader.Query(`SELECT i.item_id, i.state, COALESCE(c.text, '') FROM items i LEFT JOIN item_criteria c ON i.item_id = c.item_id AND c.criterion_id = 'trunk-green' WHERE i.item_id LIKE 'fixer-main-%' ORDER BY i.item_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, state, text string
			if err := rows.Scan(&id, &state, &text); err != nil {
				return err
			}
			if finishedState(declared.Parsed, state) {
				continue
			}
			checks := fixerChecks(text)
			checkText := "unknown check"
			if len(checks) > 0 {
				checkText = strings.Join(checks, ", ")
			}
			lines = append(lines, fmt.Sprintf("- Trunk check red on %s; remedy fixer: %s. Do not spend your run on this pre-existing trunk failure.", checkText, id))
		}
		return rows.Err()
	})
	if err != nil || len(lines) == 0 {
		return "", err
	}
	return "Trunk state notice (pre-existing red check; not caused by this item):\n" + strings.Join(lines, "\n"), nil
}

// acquitTrunkAlreadyRed is the gate-failure fork: it answers true when the failure was
// the trunk's and the item has been acquitted (a refusal recorded, a fixer filed, the
// item held blocked), and false when the ordinary gate refusal should stand. A failure red
// only on a base the trunk has repaired since files no fixer: the lane is brought onto the
// tip and the ordinary refusal goes back to its worker saying so.
func (l *Loop) acquitTrunkAlreadyRed(ctx context.Context, candidate *Candidate, gate GateOutcome, result *LoopResult) (bool, error) {
	_, acquitted, err := l.settleTrunkAlreadyRed(ctx, candidate, gate, result)
	return acquitted, err
}

// settleTrunkAlreadyRed implements OQ-GP7:
//   - If the failing checks are not confirmed red on base and trunk tip, returns ("", false, nil)
//     (or updates candidate with repairedAtTipDetail when repaired on tip).
//   - If confirmed red on trunk tip:
//   - Compare candidate's changed paths against each red check's declared `reads`
//     (defaulting to ["."] when undeclared, matching checkfiles.go).
//   - If disjoint from every red check's declared reads, promote gate.verdict.json (if
//     present) to pass-inherited and return (GatePassedInherited, false, nil) so the
//     candidate lands.
//   - Otherwise, file/attach the fixer and hold the item behind the fixer with a reason
//     naming the check, fixer, and intersecting paths.
func (l *Loop) settleTrunkAlreadyRed(ctx context.Context, candidate *Candidate, gate GateOutcome, result *LoopResult) (string, bool, error) {
	reading, err := l.trunkAlreadyRed(ctx, *candidate, gate)
	if err != nil {
		return "", false, err
	}
	if reading.RepairedAt != "" {
		if recErr := recordTrunkRepair(ctx, l.cfg.Root, reading.RepairedAt, "trunk repaired at "+shortSHA(reading.RepairedAt)); recErr != nil {
			l.reportf("loop: %s trunk repair at %s not recorded: %v\n", candidate.ItemID, shortSHA(reading.RepairedAt), recErr)
		}
		oldBase := candidate.BaseSHA
		var moved string
		var detail string
		if candidate.laneReleasedForReplay() {
			detail = repairedAtTipReleasedDetail(gate.FailingChecks, oldBase, reading.RepairedAt)
			candidate.RestartOnTip = true
		} else {
			from := candidate.WorkerHeadSHA
			if from == "" {
				from = candidate.HeadSHA
			}
			moved, err = l.moveLaneOntoTip(ctx, candidate.LaneID, from, reading.RepairedAt)
			if err != nil {
				return "", false, err
			}
			detail = repairedAtTipDetail(gate.FailingChecks, oldBase, reading.RepairedAt, moved)
			if moved != "" {
				candidate.BaseSHA, candidate.HeadSHA, candidate.WorkerHeadSHA = reading.RepairedAt, moved, moved
			}
		}
		candidate.RefusalDetail = strings.TrimSpace(candidate.RefusalDetail + " " + detail)
		l.reportf("loop: %s's gate failure is red only on its base %s; the trunk is repaired at %s, so no fixer is filed\n", candidate.ItemID, shortSHA(oldBase), shortSHA(reading.RepairedAt))
	}
	if !reading.Confirmed {
		evidence := reading.Evidence
		if evidence == "" {
			evidence = "not checked"
		}
		if reading.RepairedAt == "" {
			held, err := l.holdForOpenTestFix(ctx, candidate, gate, evidence)
			if err != nil {
				return "", false, err
			}
			if held {
				return "", true, nil
			}
		}
		l.reportf("loop: gate failed %s: recording a refusal, not a verdict (trunk evidence: %s)\n", candidate.ItemID, evidence)
		return "", false, nil
	}
	changedPaths := gate.CandidateChangedPaths
	if len(changedPaths) == 0 {
		changedPaths = gate.SuspectPaths
	}
	if len(changedPaths) == 0 && candidate.BaseSHA != "" && candidate.Worktree != "" {
		if ps, ok := l.cfg.Steps.(*ProcessSteps); ok {
			changedPaths = ps.changedPaths(*candidate)
		}
	}
	checkReads := gate.CheckReads
	if checkReads == nil {
		checkReads = gate.CheckWatches
	}
	intersecting := intersectingRedPaths(reading.Red, checkReads, changedPaths)
	if len(intersecting) == 0 && len(changedPaths) > 0 {
		if err := l.promoteVerdictPassInherited(ctx, *candidate, reading.RedSHA, reading.Red); err != nil {
			return "", false, err
		}
		l.reportf("loop: %s acquitted as pass-inherited: red checks (%s) on %s do not read %s\n",
			candidate.ItemID, strings.Join(reading.Red, ", "), shortSHA(reading.RedSHA), strings.Join(changedPaths, ", "))
		return GatePassedInherited, false, nil
	}
	if len(gate.CheckLogs) > 0 && len(candidate.replayCheckLogs) == 0 {
		candidate.replayCheckLogs = gate.CheckLogs
	}
	gate.CheckLogs = preferReplayCheckLogs(gate.CheckLogs, reading.CheckLogs)
	held, err := l.acquitRedWithPaths(ctx, candidate, gate, reading.RedSHA, reading.Red, intersecting, result)
	return "", held, err
}

// preferReplayCheckLogs returns a copy of base with every non-empty replay log
// overriding the candidate's log for the same check id, so a trunk fixer carries
// what the base or tip replay saw rather than the candidate's own gate output.
func preferReplayCheckLogs(base, replay map[string]string) map[string]string {
	if len(replay) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(replay))
	for id, ref := range base {
		out[id] = ref
	}
	for id, ref := range replay {
		if ref != "" {
			out[id] = ref
		}
	}
	return out
}

// intersectingRedPaths returns the sorted unique candidate paths that intersect any red
// check's declared `reads`. Where a check declares no `reads` (empty slice or absent),
// checkfiles.go defaults it to ["."] (whole-tree), so every candidate path intersects.
func intersectingRedPaths(redChecks []string, checkReads map[string][]string, changedPaths []string) []string {
	if len(changedPaths) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, checkID := range redChecks {
		var reads []string
		if checkReads != nil {
			reads = checkReads[checkID]
		}
		if len(reads) == 0 {
			// Undeclared reads default to ["."] (whole-tree), matching checkfiles.go.
			reads = []string{"."}
		}
		for _, path := range changedPaths {
			if seen[path] {
				continue
			}
			if readsCoverPath(reads, path) {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	sort.Strings(out)
	return out
}

func readsCoverPath(reads []string, path string) bool {
	return fixer.ReadsCoverPath(reads, path)
}

// promoteVerdictPassInherited rewrites <runDir>/gate.verdict.json (when present) from
// "fail" to "pass-inherited" so tillr-land accepts the candidate with inherited failures
// recorded. If no verdict file exists (e.g. a stubbed test without verdict files), it is
// a no-op.
func (l *Loop) promoteVerdictPassInherited(_ context.Context, candidate Candidate, tipSHA string, redChecks []string) error {
	if l.cfg.Root == "" || candidate.RunID == "" {
		return nil
	}
	verdictPath := filepath.Join(RunDir(l.cfg.Root, candidate.RunID), GateVerdictRecord)
	raw, err := os.ReadFile(verdictPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	env, err := record.Decode(raw)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	res, _ := payload["result"].(string)
	if res == "" || res == GatePassedInherited || classifyCheck(checkresult.Observation{
		Commit:   candidate.HeadSHA,
		Worktree: candidate.Worktree,
		Source:   checkresult.ResultWord,
		Word:     res,
	}).Outcome != checkresult.Fail {
		return nil
	}
	redSet := map[string]bool{}
	for _, c := range redChecks {
		redSet[c] = true
	}
	for _, observation := range candidate.InheritedDrift {
		redSet[observation.CheckID] = true
	}
	// The wrapper record names one failed check for the legacy batch. Its children
	// are all accounted for by the settlement before this transition.
	if len(candidate.InheritedDrift) > 0 {
		for _, check := range gateFailingChecks(RunDir(l.cfg.Root, candidate.RunID), candidate.RunID) {
			redSet[check.CheckID] = true
		}
	}
	if checksRaw, ok := payload["checks"].([]any); ok {
		for _, item := range checksRaw {
			if m, ok := item.(map[string]any); ok {
				if id, _ := m["check_id"].(string); redSet[id] {
					r, _ := m["result"].(string)
					if r != "" && r != "inherited" && classifyCheck(checkresult.Observation{
						Check:    id,
						Commit:   candidate.HeadSHA,
						Worktree: candidate.Worktree,
						Source:   checkresult.ResultWord,
						Word:     r,
					}).Outcome == checkresult.Fail {
						m["result"] = "inherited"
					}
				}
			}
		}
	}
	inherited := make([]InheritedFailure, 0, len(redChecks))
	for _, c := range redChecks {
		inherited = append(inherited, InheritedFailure{
			CheckID: c,
			TipSHA:  tipSHA,
			Output:  fmt.Sprintf("%s failed on trunk tip %s", c, shortSHA(tipSHA)),
		})
	}
	for _, observation := range candidate.InheritedDrift {
		inherited = append(inherited, InheritedFailure{
			CheckID: observation.CheckID,
			Output: fmt.Sprintf("fork %s conflicts with %s (%s) on both candidate and base %s",
				observation.Fork, observation.Ref, observation.Commit, candidate.BaseSHA),
		})
	}
	payload["result"] = GatePassedInherited
	payload["inherited_failures"] = inherited
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env.Payload = encodedPayload
	encodedEnv, err := record.Encode(env)
	if err != nil {
		return err
	}
	return os.WriteFile(verdictPath, encodedEnv, 0o644)
}

// acquitRed records a replay's red base against a candidate: the fixer filed or attached,
// the trunk refusal recorded, and the item held blocked behind the fixer.
func (l *Loop) acquitRed(ctx context.Context, candidate Candidate, gate GateOutcome, redSHA string, redChecks []string, result *LoopResult) error {
	_, err := l.acquitRedWithPaths(ctx, &candidate, gate, redSHA, redChecks, nil, result)
	return err
}

func (l *Loop) acquitRedWithPaths(ctx context.Context, candidate *Candidate, gate GateOutcome, redSHA string, redChecks []string, intersectingPaths []string, result *LoopResult) (bool, error) {
	// The candidate's diff starts at its red base; it cannot name the earlier
	// commits that broke that base. Include the whole range since proved green.
	var green string
	if l.gatePolicy.LandingStrategy == "after-merge" {
		var err error
		green, err = heavyGreen(ctx, l.cfg.Root)
		if err != nil {
			return false, err
		}
	} else {
		state, err := l.loadRollingState(ctx)
		if err != nil {
			return false, err
		}
		green = state.WatermarkSHA
	}
	gate.SuspectPaths = append(gate.SuspectPaths, l.rangePaths(ctx, green, redSHA)...)
	tipSHA := redSHA
	note := ""
	if l.gatePolicy.LandingStrategy == "after-merge" {
		// Aim the fixer the way the proof does (HeavyRunner.firstRed): the first recorded
		// landing since the green pointer on which the check is red. A red some open fixer
		// already names attaches to it, so the search is not repeated per candidate.
		open, err := l.openFixerFor(ctx, redChecks)
		if err != nil {
			return false, err
		}
		if open == "" {
			if replay, ok := l.cfg.Steps.(ReplayStep); ok {
				if len(gate.CheckLogs) > 0 && len(candidate.replayCheckLogs) == 0 {
					candidate.replayCheckLogs = gate.CheckLogs
				}
				culprit, attrNote, bisected := l.culpritLandingAttribution(ctx, replay, *candidate, green, redSHA, redChecks)
				redSHA, note = culprit, attrNote
				if bisected && l.gatePolicy.AutoRevert && candidate.BaseRef == "" {
					out, err := l.revertFromGateReplay(ctx, *candidate, replay, tipSHA, culprit, redChecks, gate)
					if err != nil {
						return false, err
					}
					if out.Reverted != "" {
						if recErr := recordTrunkRepair(ctx, l.cfg.Root, out.Reverted, "reverted "+shortSHA(culprit)); recErr != nil {
							l.reportf("loop: %s trunk repair at %s not recorded: %v\n", candidate.ItemID, shortSHA(out.Reverted), recErr)
						}
						from := candidate.WorkerHeadSHA
						if from == "" {
							from = candidate.HeadSHA
						}
						moved, err := l.moveLaneOntoTip(ctx, candidate.LaneID, from, out.Reverted)
						if err != nil {
							return false, err
						}
						oldBase := candidate.BaseSHA
						detail := repairedAtTipDetail(redChecks, oldBase, out.Reverted, moved)
						if moved != "" {
							candidate.BaseSHA, candidate.HeadSHA, candidate.WorkerHeadSHA = out.Reverted, moved, moved
						}
						candidate.RefusalDetail = strings.TrimSpace(candidate.RefusalDetail + " " + detail)
						l.reportf("loop: %s's gate failure was from landing %s, reverted as %s; no fixer is filed\n",
							candidate.ItemID, shortSHA(culprit), shortSHA(out.Reverted))
						return false, nil
					}
					if out.Dropped != "" {
						if currentTip, tipErr := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", l.trunkRef()); tipErr == nil && currentTip != "" {
							from := candidate.WorkerHeadSHA
							if from == "" {
								from = candidate.HeadSHA
							}
							if moved, moveErr := l.moveLaneOntoTip(ctx, candidate.LaneID, from, currentTip); moveErr == nil {
								oldBase := candidate.BaseSHA
								detail := repairedAtTipDetail(redChecks, oldBase, currentTip, moved)
								if moved != "" {
									candidate.BaseSHA, candidate.HeadSHA, candidate.WorkerHeadSHA = currentTip, moved, moved
								}
								candidate.RefusalDetail = strings.TrimSpace(candidate.RefusalDetail + " " + detail)
							}
						}
						l.reportf("loop: %s's revert of %s dropped: %s; no fixer is filed\n",
							candidate.ItemID, shortSHA(culprit), heavyBrief(out.Dropped))
						return false, nil
					}
					note = joinDetail(note, "Automatic revert of "+shortSHA(culprit)+" for "+strings.Join(redChecks, ", ")+" was not possible: "+heavyBrief(out.Refused))
					if out.StillRed {
						redSHA = tipSHA
					}
				}
			}
		}
	}
	// Check and establish fixer-to-fixer waits serially: simultaneous settlements
	// must not each see the other fixer as unblocked and create a cycle.
	if IsTrunkFixer(candidate.ItemID) {
		l.fixerWaitMu.Lock()
		defer l.fixerWaitMu.Unlock()
	}
	fixerID, err := l.routeTrunkFixer(ctx, redSHA, redChecks, gate, note, candidate.ItemID)
	if err != nil {
		return false, err
	}
	if fixerID == candidate.ItemID {
		detail := fmt.Sprintf("%s already failed on trunk at %s and was added to this fixer because its change touches paths the check reads", strings.Join(redChecks, ", "), shortSHA(redSHA))
		l.reportf("loop: %s: %s\n", candidate.ItemID, detail)
		for _, check := range redChecks {
			if ref := gate.CheckLogs[check]; ref != "" {
				detail += "\n" + check + " output (" + ref + "): " + heavyBrief(legacyLogTail(ref))
			}
		}
		candidate.RefusalDetail = strings.TrimSpace(candidate.RefusalDetail + " " + detail)
		return false, nil
	}
	if err := l.recordTrunkRefusal(ctx, *candidate, redSHA, redChecks, fixerID); err != nil {
		return false, err
	}
	reason := "trunk-already-red: " + strings.Join(redChecks, ", ")
	if len(intersectingPaths) > 0 {
		reason += " (fixer " + fixerID + "; paths: " + strings.Join(intersectingPaths, ", ") + ")"
	}
	if err := l.cfg.Steps.ItemState(ctx, candidate.ItemID, candidate.RunID, StateBlocked, item.WaitingOn{Waiter: "item:" + fixerID, Reason: reason}); err != nil {
		return false, err
	}
	l.mu.Lock()
	result.TrunkAcquitted++
	l.mu.Unlock()
	l.reportf("loop: %s acquitted as trunk-already-red: replay found %s red on %s, so %s is held blocked by %s\n",
		candidate.ItemID, strings.Join(redChecks, ", "), shortSHA(redSHA), candidate.ItemID, fixerID)
	candidate.trunkAcquitted = true
	candidate.trunkFixer = &trunkFixerHold{
		fixerID:       fixerID,
		redSHA:        redSHA,
		failingChecks: append([]string(nil), redChecks...),
	}
	return true, nil
}

// culpritLanding answers the commit a confirmed trunk red is attributed to, and a note
// for the fixer's brief when no single landing explains it. It is the proof's culprit
// search (HeavyRunner.firstRed) driven by the replay step: the checks are replayed at the
// green pointer, then at the last recorded landing since it, then bisected over those
// landings; a commit is red when any of the checks is red on it. A red at the green
// pointer, no recorded landing in the range (a commit that reached the trunk without the
// lander is not one), a green last landing, or a replay with no verdict names no landing,
// and the red stays on the commit it was observed on.
func (l *Loop) culpritLanding(ctx context.Context, replay ReplayStep, candidate Candidate, green, red string, checks []string) (string, string) {
	sha, note, _ := l.culpritLandingAttribution(ctx, replay, candidate, green, red, checks)
	return sha, note
}

func (l *Loop) culpritLandingAttribution(ctx context.Context, replay ReplayStep, candidate Candidate, green, red string, checks []string) (string, string, bool) {
	if green == "" || green == red {
		return red, "", false
	}
	isRed := func(sha string) (bool, bool) {
		out, err := restartWork(ctx, l, func(child context.Context) (ReplayOutcome, error) {
			return l.runReplay(child, replay, candidate, sha, checks)
		})
		l.sweepDisabledMarkers(ctx)
		if err != nil || !out.Known || out.Rev != "" && out.Rev != sha {
			return false, false
		}
		return len(out.Red) > 0, true
	}
	unattributed := func(why string) (string, string, bool) {
		l.reportf("loop: %s red at %s names no landing: %s\n", strings.Join(checks, ", "), shortSHA(red), why)
		return red, "No single landing is named because " + why, false
	}
	if redAtGreen, known := isRed(green); !known {
		return unattributed("the replay at the green pointer " + shortSHA(green) + " gave no verdict")
	} else if redAtGreen {
		return unattributed("the checks are red at the green pointer " + shortSHA(green))
	}
	commits, err := heavyLandings(ctx, l.cfg.Root, l.cfg.Store, green, red)
	if err != nil {
		return unattributed("the recorded landings since " + shortSHA(green) + " could not be read: " + err.Error())
	}
	if len(commits) == 0 {
		return unattributed("no recorded landing lies between the green pointer " + shortSHA(green) + " and " + shortSHA(red) + " (a commit that reached the trunk without the lander is not one)")
	}
	if lastRed, known := isRed(commits[len(commits)-1]); !known || !lastRed {
		return unattributed("the last recorded landing " + shortSHA(commits[len(commits)-1]) + " is not red, so the landings do not explain " + shortSHA(red))
	}
	low, high := 0, len(commits)-1
	for low < high {
		mid := (low + high) / 2
		midRed, known := isRed(commits[mid])
		if !known {
			return unattributed("the replay at landing " + shortSHA(commits[mid]) + " gave no verdict")
		}
		if midRed {
			high = mid
		} else {
			low = mid + 1
		}
	}
	l.reportf("loop: %s red at %s; first red landing %s\n", strings.Join(checks, ", "), shortSHA(red), shortSHA(commits[low]))
	return commits[low], "", true
}

// openFixerFor answers an unresolved trunk fixer that already names every one of checks.
func (l *Loop) openFixerFor(ctx context.Context, checks []string) (string, error) {
	if len(checks) == 0 {
		return "", nil
	}
	found := ""
	err := l.readStore(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		rows, err := r.Query(`SELECT i.item_id,i.state,c.text FROM items i JOIN item_criteria c USING(item_id) WHERE i.item_id LIKE 'fixer-main-%' AND c.criterion_id='trunk-green' ORDER BY i.rank,i.item_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, state, text string
			if err := rows.Scan(&id, &state, &text); err != nil {
				return err
			}
			if !finishedState(declared.Parsed, state) && allIn(checks, fixerChecks(text)) {
				found = id
				return nil
			}
		}
		return rows.Err()
	})
	return found, err
}

// recordTrunkRefusal appends the `drain.refusal` that names the red sha and the failing
// checks, and the fixer the item is held behind.
func (l *Loop) recordTrunkRefusal(ctx context.Context, candidate Candidate, redSHA string, redChecks []string, fixerID string) error {
	payload := refusalPayload{
		Step:          RefusalTrunkAlreadyRed,
		ItemID:        candidate.ItemID,
		LaneID:        candidate.LaneID,
		TickID:        candidate.TickID,
		RedSHA:        redSHA,
		FailingChecks: redChecks,
		FixerItem:     fixerID,
	}
	payload.RefusalCauses = computeRefusalCauses(payload)
	return appendJudgment(ctx, l.cfg.Store, RefusalKind, "run", candidate.RunID, payload, l.cfg.Now().UTC())
}

// fileTrunkFixer creates a repair identified by its first red sha, or attaches a
// later observation to an unresolved repair for that sha or failing check. Attachment
// merges checks and paths without clearing owner holds. A terminal repair for the
// exact sha is retained rather than reopened.
//
// The item is written through item.Apply, the item package's one record→row mapping, so a
// fixer cannot be a state the item writer would refuse.
func (l *Loop) fileTrunkFixer(ctx context.Context, redSHA string, failing []string, gate GateOutcome) (string, error) {
	return l.fileTrunkFixerNoted(ctx, redSHA, failing, gate, "")
}

// fileTrunkFixerNoted is fileTrunkFixer with a sentence for the brief: how the red was
// (or could not be) attributed, and the failing check's recorded output. A new fixer
// carries it in its criteria; an attachment keeps the existing brief.
func (l *Loop) fileTrunkFixerNoted(ctx context.Context, redSHA string, failing []string, gate GateOutcome, note string) (string, error) {
	return l.routeTrunkFixer(ctx, redSHA, failing, gate, note, "")
}

func (l *Loop) routeTrunkFixer(ctx context.Context, redSHA string, failing []string, gate GateOutcome, note, waitingCandidate string) (string, error) {
	fixerID := TrunkFixerID(redSHA)
	paths, err := l.expandFixerPaths(ctx, redSHA, append(append([]string{}, gate.SuspectPaths...), CheckWatches(gate.CheckWatches, failing)...))
	if err != nil {
		return "", err
	}
	gatePaths := l.fixerGateCodePaths()
	fence, dropped := trunkFixerPathsWithGate(paths, nil, gatePaths)
	nextProof := "the next rolling run advances the watermark"
	if l.gatePolicy.LandingStrategy == "after-merge" {
		nextProof = "the next heavy run advances refs/tillr/green"
	}
	criteria := "the full gate is green at " + shortSHA(redSHA) + ", and " + nextProof
	if len(failing) > 0 {
		// The brief names what was red. A fixer told only the sha has to re-run the gate to
		// find out, and a rolling red already recorded its failing checks.
		criteria = "the full gate is green at " + shortSHA(redSHA) + " (red: " + strings.Join(failing, ", ") + "), and " + nextProof
	}
	if note = strings.TrimSpace(note); note != "" {
		criteria += ". " + note
	}
	if gate.RepairDetail != "" && !strings.Contains(criteria, gate.RepairDetail) {
		criteria += ". " + gate.RepairDetail
	}
	// A generated fixer has no body reference: its criteria are the whole brief body.
	if len(dropped) > 0 {
		criteria += ". Paths omitted from the repair hints: " + strings.Join(dropped, ", ")
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
	checkLogs := l.snapshotCheckLogs(filepath.Join(".fixers", fixerID), filterCheckLogs(gate.CheckLogs, failing))
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return "", Incompatiblef("%v", err)
	}
	defer s.Close()
	created := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		declared, err := definition.ForReader(tx.Reader())
		if err != nil {
			return err
		}
		// Check identity outlives a red tip. Reuse an unresolved repair, preserving
		// its blocked/owner state, and retain the newly observed tip as evidence.
		rows, err := tx.Query(`SELECT i.item_id,i.state,i.allowed_paths,c.text FROM items i JOIN item_criteria c USING(item_id) WHERE i.item_id LIKE 'fixer-main-%' AND c.criterion_id='trunk-green' ORDER BY i.rank,i.item_id`)
		if err != nil {
			return err
		}
		var attached, oldFence, oldText string
		for rows.Next() {
			var id, state, paths, text string
			if err := rows.Scan(&id, &state, &paths, &text); err != nil {
				rows.Close()
				return err
			}
			if finishedState(declared.Parsed, state) {
				continue
			}
			if id == fixerID {
				attached, oldFence, oldText = id, paths, text
			}
			for _, old := range fixerChecks(text) {
				for _, check := range failing {
					if old == check {
						attached, oldFence, oldText = id, paths, text
					}
				}
			}
			if attached != "" {
				break
			}
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return readErr
		}
		if IsTrunkFixer(waitingCandidate) && attached != "" && attached != waitingCandidate {
			cycle, err := fixerWaitReaches(tx.Reader(), attached, waitingCandidate)
			if err != nil {
				return err
			}
			if cycle {
				attached = waitingCandidate
				if err := tx.QueryRow(`SELECT i.allowed_paths,c.text FROM items i JOIN item_criteria c USING(item_id) WHERE i.item_id=? AND c.criterion_id='trunk-green'`, attached).Scan(&oldFence, &oldText); err != nil {
					return err
				}
			}
		}
		if attached != "" {
			fixerID = attached
			var paths []string
			if err := json.Unmarshal([]byte(oldFence), &paths); err != nil {
				return err
			}
			paths = TrunkFixerFenceWithGate(paths, fence, gatePaths)
			raw, err := json.Marshal(paths)
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			checks := []string{}
			for _, check := range append(fixerChecks(oldText), failing...) {
				if !seen[check] {
					checks = append(checks, check)
					seen[check] = true
				}
			}
			sort.Strings(checks)
			text := oldText
			if len(checks) > 0 {
				label := "(red: " + strings.Join(checks, ", ") + ")"
				start := strings.Index(oldText, "(red: ")
				if start >= 0 && strings.Contains(oldText[start:], ")") {
					end := strings.Index(oldText[start:], ")") + start + 1
					text = oldText[:start] + label + oldText[end:]
				} else {
					text += " " + label
				}
			}
			if gate.RepairDetail != "" && !strings.Contains(text, gate.RepairDetail) {
				text += ". " + gate.RepairDetail
			}
			if _, err := tx.Exec(`UPDATE items SET allowed_paths=? WHERE item_id=?`, string(raw), fixerID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE item_criteria SET text=? WHERE item_id=? AND criterion_id='trunk-green'`, text, fixerID); err != nil {
				return err
			}
			if len(checkLogs) > 0 {
				if err := appendEffectOn(tx, FixerLogsKind, "item", fixerID, map[string]any{"fixer_item": fixerID, "red_sha": redSHA, "check_logs": checkLogs}, l.cfg.Now()); err != nil {
					return err
				}
			}
			return appendEffect(tx, FixerAttachedKind, fixerID, map[string]any{"item_id": fixerID, "fixer_item": fixerID, "red_sha": redSHA, "failing_checks": failing, "allowed_paths": paths, "kept_candidate": false}, l.cfg.Now())
		}
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
		if len(checkLogs) > 0 {
			if err := appendEffectOn(tx, FixerLogsKind, "item", fixerID, map[string]any{"fixer_item": fixerID, "red_sha": redSHA, "check_logs": checkLogs}, l.cfg.Now()); err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if err != nil {
		return "", classifyWrite(err)
	}
	if created {
		l.reportf("loop: filed %s for the broken trunk at %s (path hints: %s)\n",
			fixerID, shortSHA(redSHA), strings.Join(fence, ", "))
	}
	return fixerID, nil
}

// FixerFenceWidenedKind records the paths a trunk fixer's fence grew by because its worker
// named them in a fence obstacle.
const FixerFenceWidenedKind = "drain.fixer-fence-widened"

// FixerLogsKind records the failing checks' log or triage paths for a trunk fixer.
const FixerLogsKind = "drain.fixer-logs"

func init() {
	if err := event.Register(FixerFenceWidenedKind, Utility); err != nil {
		panic(err)
	}
	if err := event.Register(FixerLogsKind, Utility); err != nil {
		panic(err)
	}
}

func (l *Loop) snapshotCheckLogs(location string, checkLogs map[string]string) map[string]string {
	if len(checkLogs) == 0 {
		return nil
	}
	if l.cfg.LaneRoot == "" {
		return checkLogs
	}
	dstDir := filepath.Join(l.cfg.LaneRoot, location)
	ids := make([]string, 0, len(checkLogs))
	for id := range checkLogs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make(map[string]string, len(ids))
	seenNames := map[string]bool{}
	for idx, id := range ids {
		ref := checkLogs[id]
		name := filepath.Base(ref)
		if name == "" || name == "." || name == "/" || seenNames[name] {
			suffix := ".log"
			if strings.HasSuffix(ref, ".triage.log") {
				suffix = ".triage.log"
			}
			name = fmt.Sprintf("%02d-%s%s", idx, sanitizeLogComponent(id), suffix)
		}
		seenNames[name] = true
		if copied, ok := copyLogWithWhole(l.cfg.Root, ref, dstDir, name); ok {
			out[id] = copied
		} else {
			out[id] = ref
		}
	}
	return out
}

func containsString(list []string, want string) bool {
	for _, have := range list {
		if have == want {
			return true
		}
	}
	return false
}

// trunkFixerOpen answers the open fixer id, or "" when the trunk is green.
//
// Open means a fixer item the store holds in a state that is not terminal. The planner
// reads this once per tick and holds every ordinary item behind it: dispatching ordinary
// work onto a red trunk would stage it from a base whose checks never passed, which is
// the defect the whole file exists to prevent.
func trunkFixerOpen(items []itemRow, declarations ...definition.Definition) string {
	def := defaultDefinition()
	if len(declarations) != 0 {
		def = declarations[0]
	}
	for _, item := range items {
		if IsTrunkFixer(item.ItemID) && !finishedState(def, item.State) {
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
	if l.nonLanding {
		return nil
	}
	if l.gatePolicy.LandingStrategy == "after-merge" {
		status, err := ReadHeavyStatusForBranch(ctx, l.cfg.Root, l.cfg.Store, l.trunkRef())
		if err != nil {
			return err
		}
		line := heavyStatusLine(status)
		if line != l.lastHeavyStatus {
			l.reportf("loop: %s\n", line)
			l.lastHeavyStatus = line
		}
		return l.startHeavy(ctx, mainSHA)
	}
	if pending, err := l.buildRestartPending(ctx); pending || err != nil {
		if err != nil {
			return err
		}
		return errRestartPending
	}
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
	out, err := restartWork(ctx, l, func(child context.Context) (RollingOutcome, error) {
		return step.Rolling(child, base, mainSHA)
	})
	l.sweepDisabledMarkers(ctx)
	if err != nil {
		return err
	}
	state.LastRunAt = now
	state.LastTipSHA = mainSHA
	state.LastOutcome = "red"
	fixerChecks := out.FailingChecks
	if out.Stalled {
		repeated, err := l.repeatedRollingStalls(ctx, base, mainSHA, out.FailingChecks)
		if err != nil {
			return err
		}
		l.reportf("loop: rolling run stalled over %s..%s (%s); watermark stays at %s; %s\n",
			shortSHA(base), shortSHA(mainSHA), strings.Join(out.FailingChecks, ", "), shortSHA(base), strings.TrimSpace(out.Detail))
		if len(repeated) == 0 {
			state.LastOutcome = "stalled"
			if err := l.recordRollingRun(ctx, base, mainSHA, out); err != nil {
				return err
			}
			return l.saveRollingState(ctx, state)
		}
		// Keep every observed check in the rolling record, including newly stalled
		// checks that can qualify on the next run. Only the fixer uses the intersection.
		fixerChecks = repeated
		// A repeated stall files the same deduplicated fixer as a red check, but
		// the rolling evidence remains stalled, never a content-failure verdict.
		out.Known = true
	}
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
		l.reportf("loop: the rolling run over %s could not judge (exit %d); %s; %s; continuing dispatch. %s\n",
			span, out.ExitCode, where, out.Coverage.line(), strings.TrimSpace(out.Detail))
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
		l.reportf("loop: rolling run green over %s..%s; watermark advances to %s; %s\n",
			shortSHA(base), shortSHA(mainSHA), shortSHA(mainSHA), out.Coverage.line())
		return nil
	}
	// Red: file the fixer for the tip and leave the watermark where it was.
	//
	// The failing checks' watched paths are the fence when the run named them; the range is
	// the fallback for a run that named no path. A first run has no range at all, so the
	// whole tree is the fence — and that is a broad fence deliberately, because a narrower
	// guess would leave the actual breakage outside the one item allowed to repair it.
	suspect := append(out.SuspectPaths, l.rangePaths(ctx, base, mainSHA)...)
	fixerGate := GateOutcome{SuspectPaths: suspect, FailingChecks: fixerChecks, CheckWatches: out.CheckWatches, CheckLogs: filterCheckLogs(out.CheckLogs, fixerChecks)}
	fixerID, fixerErr := l.fileTrunkFixer(ctx, mainSHA, fixerChecks, fixerGate)
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
		l.reportf("loop: rolling run RED over %s..%s (%s); watermark stays at %s and the queue is held behind %s; %s\n",
			shortSHA(base), shortSHA(mainSHA), strings.Join(fixerChecks, ", "),
			shortSHA(state.WatermarkSHA), fixerID, out.Coverage.line())
	}
	return nil
}

// repeatedRollingStalls reads the last observation, so consecutive stalls survive
// a restart and only the same check on the same range files a fixer.
func (l *Loop) repeatedRollingStalls(ctx context.Context, base, tip string, checks []string) ([]string, error) {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var oldBase, oldTip, outcome, raw string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT base_sha, tip_sha, outcome, failing_checks FROM rolling_runs ORDER BY at DESC, rowid DESC LIMIT 1`).Scan(&oldBase, &oldTip, &outcome, &raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, readFailure(err)
	}
	if oldBase != base || oldTip != tip || outcome != rollingOutcomeStalled {
		return nil, nil
	}
	var previous []string
	if err := json.Unmarshal([]byte(raw), &previous); err != nil {
		return nil, err
	}
	var repeated []string
	for _, check := range checks {
		for _, old := range previous {
			if check == old {
				repeated = append(repeated, check)
				break
			}
		}
	}
	return repeated, nil
}

// rangePaths is the changed-path list between two revisions, used as the rolling run's
// suspect paths when the rolling program did not name them.
//
// An empty base is the first rolling run: nothing has been proved yet, so every tracked
// path is a suspect, and the fixer's fence is the whole tree minus the gate's own code.
// That is a broad fence and deliberately so — a narrower guess on the first run would
// leave the actual breakage outside the one item allowed to repair it.
func (l *Loop) rangePaths(ctx context.Context, base, tip string) []string {
	args := []string{"log", "--format=", "--name-only", "--no-renames", "--diff-merges=first-parent", base + ".." + tip}
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
// A fixer that cannot be rebased cleanly answers a *fixerRebaseConflict, with the rebase
// aborted and the lane back on the fixer's own head: the trunk moved under it, and the
// verify step treats that as a landing conflict (fixerRebaseConflicted), never as the
// loop's fault.
func (l *Loop) preRebaseFixer(ctx context.Context, candidate Candidate) (Candidate, error) {
	if !IsTrunkFixer(candidate.ItemID) {
		return candidate, nil
	}
	mainSHA, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
	if err != nil {
		return candidate, err
	}
	if mainSHA == "" || mainSHA == candidate.BaseSHA {
		return candidate, nil
	}
	if _, err := l.git(ctx, candidate.Worktree, "rebase", mainSHA); err != nil {
		return candidate, l.abortFixerRebase(ctx, candidate, mainSHA, err)
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

// retitleIntermittentFixer rewrites a live intermittent fixer's commit message so the
// landed squash commit is titled as a fix for an intermittent check while keeping the
// worker's original commit message in the body.
func (l *Loop) retitleIntermittentFixer(ctx context.Context, candidate Candidate) (Candidate, error) {
	if !IsTrunkFixer(candidate.ItemID) || l.cfg.Store == "" || candidate.Worktree == "" ||
		candidate.BaseSHA == "" || candidate.HeadSHA == "" || candidate.BaseSHA == candidate.HeadSHA {
		return candidate, nil
	}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return candidate, err
	}
	var intermittent bool
	var checks []string
	err = r.ReadTx(ctx, func(rd *store.Reader) error {
		var e error
		intermittent, checks, e = readIntermittentFixer(rd, candidate.ItemID)
		return e
	})
	r.Close()
	if err != nil || !intermittent {
		return candidate, err
	}
	source := candidate.WorkerHeadSHA
	if source == "" {
		source = candidate.HeadSHA
	}
	revs, err := l.git(ctx, candidate.Worktree, "rev-list", "--reverse", candidate.BaseSHA+".."+source)
	if err != nil {
		return candidate, err
	}
	fields := strings.Fields(revs)
	first := source
	if len(fields) > 0 {
		first = fields[0]
	} else {
		fields = []string{source}
	}
	var msgParts []string
	for _, rev := range fields {
		part, err := l.git(ctx, candidate.Worktree, "log", "-1", "--format=%B", rev)
		if err != nil {
			return candidate, err
		}
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			msgParts = append(msgParts, trimmed)
		}
	}
	origMsg := strings.Join(msgParts, "\n\n")
	firstLine, _, _ := strings.Cut(origMsg, "\n")
	if strings.HasPrefix(firstLine, "fix: intermittent check") {
		return candidate, nil
	}
	title := "fix: intermittent check"
	if len(checks) > 0 {
		joined := strings.Join(checks, ", ")
		if len(title)+1+len(joined) <= 72 {
			title += " " + joined
		}
	}
	msg := title + "\n"
	if origMsg != "" {
		msg = title + "\n\n" + origMsg + "\n"
	}
	meta, err := l.git(ctx, candidate.Worktree, "log", "-1", "--format=%an%n%ae%n%aI", first)
	if err != nil {
		return candidate, err
	}
	lines := strings.Split(strings.TrimSpace(meta), "\n")
	an, ae, ai := "", "", l.cfg.Now().UTC().Format(time.RFC3339)
	if len(lines) >= 3 {
		an = strings.TrimSpace(lines[0])
		ae = strings.TrimSpace(lines[1])
		if strings.TrimSpace(lines[2]) != "" {
			ai = strings.TrimSpace(lines[2])
		}
	}
	cfgName, _ := l.git(ctx, candidate.Worktree, "config", "user.name")
	cfgEmail, _ := l.git(ctx, candidate.Worktree, "config", "user.email")
	cfgName = strings.TrimSpace(cfgName)
	cfgEmail = strings.TrimSpace(cfgEmail)
	if an == "" {
		an = cfgName
	}
	if ae == "" {
		ae = cfgEmail
	}
	if an == "" || ae == "" {
		return candidate, Externalf("retitling intermittent fixer %s: git user.name/user.email not configured", candidate.ItemID)
	}
	cn, ce := cfgName, cfgEmail
	if cn == "" {
		cn = an
	}
	if ce == "" {
		ce = ae
	}
	tree, err := l.git(ctx, candidate.Worktree, "rev-parse", candidate.HeadSHA+"^{tree}")
	if err != nil {
		return candidate, err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", candidate.Worktree, "commit-tree", strings.TrimSpace(tree), "-p", candidate.BaseSHA, "-m", msg)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+an,
		"GIT_AUTHOR_EMAIL="+ae,
		"GIT_AUTHOR_DATE="+ai,
		"GIT_COMMITTER_NAME="+cn,
		"GIT_COMMITTER_EMAIL="+ce,
		"GIT_COMMITTER_DATE="+l.cfg.Now().UTC().Format(time.RFC3339),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return candidate, Externalf("retitling intermittent fixer %s: %v (%s)", candidate.ItemID, err, strings.TrimSpace(string(out)))
	}
	newHead := strings.TrimSpace(string(out))
	if _, err := l.git(ctx, candidate.Worktree, "reset", "-q", "--hard", newHead); err != nil {
		return candidate, err
	}
	candidate.HeadSHA = newHead
	if candidate.WorkerHeadSHA != "" {
		candidate.WorkerHeadSHA = newHead
	}
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
	l.reportf("loop: retitled intermittent fixer %s (%s) as %q\n", candidate.ItemID, shortSHA(newHead), title)
	return candidate, nil
}
