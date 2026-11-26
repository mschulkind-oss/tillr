package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type gateBaseRecordingSteps struct {
	recordingSteps
	gated []Candidate
}

func (s *gateBaseRecordingSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.gated = append(s.gated, c)
	return GateOutcome{Outcome: GatePassed}, nil
}

func ordinaryCandidateFixture(t *testing.T) (refusalLoopFixture, *Loop, *gateBaseRecordingSteps, Candidate, string, *strings.Builder) {
	t.Helper()
	f, l, _, oldBase := fixerRoutingFixture(t)
	steps := &gateBaseRecordingSteps{}
	l.cfg.Steps = steps
	report := &strings.Builder{}
	l.cfg.Report = report

	lane := l.laneTarget("lane-1")
	runGit(t, lane, "reset", "-q", "--hard", oldBase)
	laneDir := l.laneDirPath("lane-1")
	for _, name := range []string{BaseSHAFile, ItemBaseSHAFile} {
		if err := os.WriteFile(filepath.Join(laneDir, name), []byte(oldBase+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeForkFile(t, filepath.Join(lane, "feature.txt"), "candidate change\n")
	runGit(t, lane, "add", "feature.txt")
	runGit(t, lane, "commit", "-qm", "feat: candidate change")
	head := runGit(t, lane, "rev-parse", "HEAD")

	c := Candidate{
		ItemID:   "item-spare",
		LaneID:   "lane-1",
		RunID:    "run-ordinary-1",
		Worktree: lane,
		BaseSHA:  oldBase,
		HeadSHA:  head,
	}
	return f, l, steps, c, oldBase, report
}

// When the drain has recorded that the trunk is repaired at a commit newer than a
// candidate's base, the candidate is rebased onto the current trunk before its landing
// gate starts, and no gate run is started on the old base.
func TestPreRebaseRepairedTrunkRebasesBeforeGateWithNoOldBaseGate(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, oldBase, report := ordinaryCandidateFixture(t)

	writeForkFile(t, filepath.Join(f.root, "repair.txt"), "trunk repair\n")
	runGit(t, f.root, "add", "repair.txt")
	runGit(t, f.root, "commit", "-qm", "fix: repair flaky restart test")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)

	writeForkFile(t, filepath.Join(f.root, "later.txt"), "subsequent landing\n")
	runGit(t, f.root, "add", "later.txt")
	runGit(t, f.root, "commit", "-qm", "chore: subsequent landing")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": the focused check passed on the advanced trunk"); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil {
		t.Fatalf("Verify failed: %v\n%s", err, report.String())
	}
	if !landed {
		t.Fatalf("candidate did not land\n%s", report.String())
	}
	if len(steps.gated) != 1 {
		t.Fatalf("gate ran %d time(s), want 1: %+v", len(steps.gated), steps.gated)
	}
	got := steps.gated[0]
	if got.BaseSHA == oldBase {
		t.Fatalf("gate was started on the old base %s; want current trunk %s", shortSHA(oldBase), shortSHA(tip))
	}
	if got.BaseSHA != tip {
		t.Fatalf("gated BaseSHA = %s, want current trunk %s", shortSHA(got.BaseSHA), shortSHA(tip))
	}
	if got.HeadSHA == c.HeadSHA {
		t.Fatalf("gated HeadSHA was not rebased from %s", shortSHA(c.HeadSHA))
	}
	runGit(t, c.Worktree, "merge-base", "--is-ancestor", tip, got.HeadSHA)
	if gotBase := l.laneBase("lane-1"); gotBase != tip {
		t.Fatalf("lane base = %s, want %s", shortSHA(gotBase), shortSHA(tip))
	}
	if !strings.Contains(report.String(), "pre-rebased "+c.ItemID+" onto "+shortSHA(tip)+" before gate") {
		t.Fatalf("missing pre-rebase report line:\n%s", report.String())
	}
}

// Even when only heavy.json carries a retainRedRepairs note ("repair deferred to proof
// of <sha>: ..."), the drain still recognizes the recorded repair and rebases candidates
// from older bases onto the current trunk before their landing gate starts.
func TestPreRebaseRepairedTrunkReadsDeferredRepairFromHeavyJSON(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, oldBase, report := ordinaryCandidateFixture(t)

	writeForkFile(t, filepath.Join(f.root, "repair.txt"), "trunk repair\n")
	runGit(t, f.root, "add", "repair.txt")
	runGit(t, f.root, "commit", "-qm", "fix: repair restart test")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)

	dir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(HeavyRun{
		BaseSHA: oldBase,
		TipSHA:  oldBase,
		Result:  "red",
		Checks: []HeavyCheck{{
			CheckID: goTestPrefix + "tools/tillr-drain:example/drain:TestWhyAfterRestart",
			Result:  "fail",
			Detail:  "repair deferred to proof of " + shortSHA(repaired) + ": the focused check passed on the advanced trunk",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil || !landed {
		t.Fatalf("Verify = %v, %v\n%s", landed, err, report.String())
	}
	if len(steps.gated) != 1 || steps.gated[0].BaseSHA != repaired {
		t.Fatalf("gated = %+v, want single gate on repaired tip %s", steps.gated, shortSHA(repaired))
	}
}

// A candidate whose base is at or newer than the recorded repair is NOT needlessly
// rebased on unrelated trunk movement (landing principle 3).
func TestPreRebaseRepairedTrunkLeavesCandidateAtOrAfterRepairAlone(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, oldBase, report := ordinaryCandidateFixture(t)

	if err := recordTrunkRepair(ctx, f.root, oldBase, "repaired at candidate base"); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "other.txt"), "unrelated landing\n")
	runGit(t, f.root, "add", "other.txt")
	runGit(t, f.root, "commit", "-qm", "chore: unrelated landing")

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil || !landed {
		t.Fatalf("Verify = %v, %v\n%s", landed, err, report.String())
	}
	if len(steps.gated) != 1 || steps.gated[0].BaseSHA != oldBase {
		t.Fatalf("candidate at repair base was needlessly rebased: gated=%+v want base=%s", steps.gated, shortSHA(oldBase))
	}
	workerHead := steps.gated[0].WorkerHeadSHA
	if workerHead == "" {
		workerHead = steps.gated[0].HeadSHA
	}
	if workerHead != c.HeadSHA {
		t.Fatalf("candidate worker head changed from %s to %s without a newer repair", shortSHA(c.HeadSHA), shortSHA(workerHead))
	}
}

// A pre-gate rebase onto a repaired trunk that conflicts aborts cleanly, starts no gate
// on the old base, and routes through conflictFix.
func TestPreRebaseRepairedTrunkConflictRoutesToConflictFixWithoutGate(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, _, report := ordinaryCandidateFixture(t)

	writeForkFile(t, filepath.Join(c.Worktree, "base.txt"), "candidate edit\n")
	runGit(t, c.Worktree, "commit", "-qam", "candidate edits base")
	c.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")

	writeForkFile(t, filepath.Join(f.root, "base.txt"), "trunk repair edit\n")
	runGit(t, f.root, "commit", "-qam", "fix: conflicting trunk repair")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)

	if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": the focused check passed on the advanced trunk"); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil {
		t.Fatalf("Verify returned error on conflict: %v\n%s", err, report.String())
	}
	if landed {
		t.Fatal("conflicting candidate landed")
	}
	if len(steps.gated) != 0 {
		t.Fatalf("gate started %d time(s) on old base despite pre-gate rebase conflict: %+v", len(steps.gated), steps.gated)
	}
	var conflicts int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND payload LIKE '%"evidence":"conflict:paths=base.txt,hunks=%'`, RefusalKind, c.RunID).Scan(&conflicts)
	})
	if conflicts == 0 {
		t.Fatalf("no conflict refusal recorded for %s:\n%s", c.RunID, report.String())
	}
}

// A "trunk was rewritten" deferral note is not a trunk repair.
func TestDeferredRepairPrefixesIgnoresTrunkRewrite(t *testing.T) {
	got := deferredRepairPrefixes("repair deferred to proof of 51cd904b55b4: trunk was rewritten")
	if len(got) != 0 {
		t.Fatalf("deferredRepairPrefixes = %v, want empty", got)
	}
	got = deferredRepairPrefixes("first note; repair deferred to proof of 51cd904b55b4: the focused check passed on the advanced trunk")
	if len(got) != 1 || got[0] != "51cd904b55b4" {
		t.Fatalf("deferredRepairPrefixes = %v, want [51cd904b55b4]", got)
	}
}

type rebasedJudgeRouteSteps struct {
	*judgeRouteSteps
	gated []Candidate
}

func (s *rebasedJudgeRouteSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.Node == "judge" || strings.HasSuffix(req.RunID, "-judge") || req.NodeID == "evaluate" || strings.HasSuffix(req.NodeID, "/evaluate") {
		target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
		s.workerHead = runGit(s.t, target, "rev-parse", "HEAD")
	}
	return s.judgeRouteSteps.Run(ctx, req)
}

func (s *rebasedJudgeRouteSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gated = append(s.gated, c)
	return s.judgeRouteSteps.Gate(ctx, c)
}

// When a judged workflow candidate is rebased onto a repaired trunk before its gate,
// the drain re-judges the rebased candidate on its own and reaches the gate without
// holding the item as judge-unreadable.
func TestPreRebaseRepairedTrunkRejudgesWorkflowCandidateBeforeGate(t *testing.T) {
	ctx := context.Background()
	f, l, baseSteps := judgeRouteFixture(t, "pass")
	steps := &rebasedJudgeRouteSteps{judgeRouteSteps: baseSteps}
	l.cfg.Steps = steps
	report := &strings.Builder{}
	l.cfg.Report = report

	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
		return err
	})

	var repaired, firstJudgedHead string
	judgeCalls := 0
	baseSteps.afterJudge = func() {
		judgeCalls++
		if judgeCalls != 1 {
			return
		}
		firstJudgedHead = baseSteps.workerHead
		writeForkFile(t, filepath.Join(f.root, "repair.txt"), "trunk repair\n")
		runGit(t, f.root, "add", "repair.txt")
		runGit(t, f.root, "commit", "-qm", "fix: repair trunk after judge")
		repaired = runGit(t, f.root, "rev-parse", TrunkRef)
		if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": focused check passed"); err != nil {
			t.Fatalf("recordTrunkRepair: %v", err)
		}
	}

	result := &LoopResult{}
	for i := 0; i < 12; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatalf("pass %d: %v\n%s", i, err, report.String())
		}
		drainOwned(t, l, result)
	}

	wantOrder := []string{"worker", "worker", "judge", "judge", "gate", "landing"}
	if !reflect.DeepEqual(steps.order, wantOrder) {
		t.Fatalf("order = %v, want %v\n%s", steps.order, wantOrder, report.String())
	}
	if len(steps.gated) != 1 {
		t.Fatalf("gate ran %d time(s), want 1\n%s", len(steps.gated), report.String())
	}
	got := steps.gated[0]
	if got.BaseSHA != repaired {
		t.Fatalf("gated BaseSHA = %s, want repaired trunk %s\n%s", shortSHA(got.BaseSHA), shortSHA(repaired), report.String())
	}
	if got.Judge == nil {
		t.Fatalf("gated candidate has nil Judge\n%s", report.String())
	}
	workerHead := got.WorkerHeadSHA
	if workerHead == "" {
		workerHead = got.HeadSHA
	}
	if workerHead == firstJudgedHead {
		t.Fatalf("gated candidate head was not rebased from %s", shortSHA(firstJudgedHead))
	}
	if got.Judge.Options.Head != workerHead {
		t.Fatalf("gated Judge.Options.Head = %s, want rebased head %s", shortSHA(got.Judge.Options.Head), shortSHA(workerHead))
	}
	if !strings.Contains(report.String(), "pre-rebased item-a onto "+shortSHA(repaired)+" before gate") {
		t.Fatalf("missing pre-rebase report line:\n%s", report.String())
	}

	var unreadable, verdicts int
	fixtureRead(t, f, func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.step')=?`, RefusalKind, ReasonJudgeUnreadable).Scan(&unreadable); err != nil {
			return err
		}
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&verdicts)
	})
	if unreadable != 0 {
		t.Fatalf("judge-unreadable refusals = %d, want 0\n%s", unreadable, report.String())
	}
	if verdicts != 1 {
		t.Fatalf("item.verdict events = %d, want 1\n%s", verdicts, report.String())
	}
}

// A saved judgment whose head matches neither the current candidate nor a drain-owned
// pre-gate rebase still holds as judge-unreadable.
func TestPreRebaseRepairedTrunkStillHoldsUnrelatedSavedJudgment(t *testing.T) {
	ctx := context.Background()
	f, l, baseSteps := judgeRouteFixture(t, "pass")
	steps := &rebasedJudgeRouteSteps{judgeRouteSteps: baseSteps}
	l.cfg.Steps = steps

	lane := l.laneTarget("lane-1")
	oldBase := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, lane, "reset", "-q", "--hard", oldBase)
	if err := os.MkdirAll(filepath.Join(lane, "tools/item-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, "tools/item-a/work.txt"), "work\n")
	runGit(t, lane, "add", ".")
	runGit(t, lane, "commit", "-qm", "feat: work")
	head := runGit(t, lane, "rev-parse", "HEAD")

	c := Candidate{
		ItemID:          "item-a",
		LaneID:          "lane-1",
		RunID:           "run-unrelated-1",
		Worktree:        lane,
		BaseSHA:         oldBase,
		HeadSHA:         head,
		RebasedFromHead: oldBase,
		Judge: &JudgeReceipt{
			Request: run.Request{ItemID: "item-a", LaneID: "lane-1", RunID: "run-unrelated-1-judge", Backend: "reviewer"},
			Raw:     json.RawMessage(`{"outcome":"pass"}`),
		},
	}
	c.Judge.Options.ItemID = "item-a"
	c.Judge.Options.Head = "0000000000000000000000000000000000000000"

	result := &LoopResult{}
	_, accepted, err := l.judgeCandidate(ctx, c, result)
	if err != nil {
		t.Fatalf("judgeCandidate returned unexpected error: %v", err)
	}
	if accepted {
		t.Fatal("judgeCandidate accepted an unrelated saved judgment")
	}
	if result.RefusedSteps[ReasonJudgeUnreadable] != 1 {
		t.Fatalf("RefusedSteps = %v, want 1 %s", result.RefusedSteps, ReasonJudgeUnreadable)
	}
}

// When the fresh verify run after a pre-gate rebase writes a not-done verdict, it must
// take the verify-not-done path: record verify-not-done on the workflow run, dispatch the
// build step on the rebased candidate with the verifier's findings, and never record
// eval-failed or hold the item as run-link-missing for the operator.
func TestPreRebaseRejudgmentFailReturnsToBuilder(t *testing.T) {
	ctx := context.Background()
	f, l, baseSteps := judgeRouteFixture(t, "pass")
	steps := &rebasedJudgeRouteSteps{judgeRouteSteps: baseSteps}
	l.cfg.Steps = steps
	report := &strings.Builder{}
	l.cfg.Report = report

	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
		return err
	})

	const findingReason = "regression after rebase: broken edge case"
	var repaired, rebasedHead string
	var returnedBuildBrief, returnedBuildBase, returnedBuildParent string
	judgeCalls := 0
	workerCalls := 0

	origRun := steps.Run
	customSteps := &callbackSteps{
		rebasedJudgeRouteSteps: steps,
		beforeRun: func(req run.Request) {
			isJudge := req.Node == "judge" || strings.HasSuffix(req.RunID, "-judge") || req.NodeID == "evaluate" || strings.HasSuffix(req.NodeID, "/evaluate")
			if !isJudge {
				workerCalls++
				if workerCalls == 3 {
					// Returned build step after the fresh verify failed.
					target := filepath.Join(f.laneRoot, req.LaneID, WorktreeDir)
					raw, err := os.ReadFile(filepath.Join(f.laneRoot, req.LaneID, BriefFile))
					if err != nil {
						t.Fatalf("read returned build brief: %v", err)
					}
					returnedBuildBrief = string(raw)
					returnedBuildBase = l.laneBase(req.LaneID)
					returnedBuildParent = runGit(t, target, "rev-parse", "HEAD")
				}
				return
			}
			judgeCalls++
			if judgeCalls == 2 {
				target := filepath.Join(f.laneRoot, req.LaneID, WorktreeDir)
				rebasedHead = runGit(t, target, "rev-parse", "HEAD")
				baseSteps.entries = []item.JudgeEntry{{
					CriterionID:   "c1",
					CriterionHash: item.CriterionHash("the first criterion of item-a"),
					Verdict:       item.VerdictNotDone,
					ClaimClass:    item.ClaimVerification,
					Reason:        findingReason,
					Evidence:      []string{"file:tools/item-a/result.txt"},
					Findings: item.VerdictFindings{{
						Kind:  "regression",
						Case:  "rebased candidate broke edge case",
						Paths: []string{"file:tools/item-a/result.txt"},
					}},
				}}
			} else {
				baseSteps.entries = nil
			}
		},
		runFn: origRun,
	}
	l.cfg.Steps = customSteps

	baseSteps.afterJudge = func() {
		if judgeCalls != 1 {
			return
		}
		writeForkFile(t, filepath.Join(f.root, "repair.txt"), "trunk repair\n")
		runGit(t, f.root, "add", "repair.txt")
		runGit(t, f.root, "commit", "-qm", "fix: repair trunk after judge")
		repaired = runGit(t, f.root, "rev-parse", TrunkRef)
		if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": focused check passed"); err != nil {
			t.Fatalf("recordTrunkRepair: %v", err)
		}
	}

	result := &LoopResult{}
	for i := 0; i < 20; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatalf("pass %d: %v\n%s", i, err, report.String())
		}
		drainOwned(t, l, result)
	}

	var evalFailed, verifyNotDone, holds, verdicts int
	var itemState, refusalRunID string
	fixtureRead(t, f, func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.step')=?`, RefusalKind, ReasonEvalFailed).Scan(&evalFailed); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.step')=?`, RefusalKind, RefusalVerifyNotDone).Scan(&verifyNotDone); err != nil {
			return err
		}
		if verifyNotDone > 0 {
			if err := r.QueryRow(`SELECT object_id FROM events WHERE kind=? AND json_extract(payload,'$.step')=? ORDER BY seq DESC LIMIT 1`, RefusalKind, RefusalVerifyNotDone).Scan(&refusalRunID); err != nil {
				return err
			}
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE item_id='item-a' AND action='hold'`).Scan(&holds); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&verdicts); err != nil {
			return err
		}
		return r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&itemState)
	})
	if evalFailed != 0 {
		t.Fatalf("eval-failed refusals = %d, want 0\n%s", evalFailed, report.String())
	}
	if holds != 0 {
		t.Fatalf("operator holds = %d, want 0\n%s", holds, report.String())
	}
	if verifyNotDone != 1 {
		t.Fatalf("verify-not-done refusals = %d, want 1\n%s", verifyNotDone, report.String())
	}
	if refusalRunID != "run-item-a-a1-n3" {
		t.Fatalf("verify-not-done refusal run_id = %q, want run-item-a-a1-n3\n%s", refusalRunID, report.String())
	}
	if !strings.Contains(report.String(), "run-item-a-a1-n3-fresh") {
		t.Fatalf("expected fresh re-judgment run run-item-a-a1-n3-fresh in report:\n%s", report.String())
	}
	if workerCalls != 3 {
		t.Fatalf("worker calls = %d, want 3 (scout, build, returned build)\n%s", workerCalls, report.String())
	}
	if returnedBuildBase != repaired {
		t.Fatalf("returned build base = %s, want repaired trunk %s\n%s", shortSHA(returnedBuildBase), shortSHA(repaired), report.String())
	}
	if returnedBuildParent != rebasedHead {
		t.Fatalf("returned build started on %s, want rebased head %s\n%s", shortSHA(returnedBuildParent), shortSHA(rebasedHead), report.String())
	}
	if !strings.Contains(returnedBuildBrief, findingReason) {
		t.Fatalf("returned build brief lacks verifier finding %q:\n%s", findingReason, returnedBuildBrief)
	}
	if itemState != StateLanded || verdicts != 1 {
		t.Fatalf("final item state = %q, verdicts = %d, want landed and 1\n%s", itemState, verdicts, report.String())
	}
}

type callbackSteps struct {
	*rebasedJudgeRouteSteps
	beforeRun func(run.Request)
	runFn     func(context.Context, run.Request) (run.Record, string, error)
}

func (s *callbackSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if s.beforeRun != nil {
		s.beforeRun(req)
	}
	return s.runFn(ctx, req)
}

type failingGateSteps struct {
	recordingSteps
	gated []Candidate
}

func (s *failingGateSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.gated = append(s.gated, c)
	return GateOutcome{
		Outcome:       GateFailed,
		FailingChecks: []string{"check-a"},
	}, nil
}

// A builder piece that resumes on a candidate rebased onto a newer trunk before the
// gate is checked against the files that resumed run changed: trunk files brought in
// by the rebase (and earlier pieces' files) are not a scope escape, while an
// out-of-scope file changed by the resumed run itself is still refused.
func TestPreRebaseResumedPieceNotRefusedForTrunkChanges(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	declareWorkerResumeFixture(t, l)
	steps := &failingGateSteps{}
	l.cfg.Steps = steps
	report := &strings.Builder{}
	l.cfg.Report = report
	l.cfg.Stage = &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}

	first := mustPlan(t, l)[0]
	oldBase := runGit(t, f.root, "rev-parse", TrunkRef)
	w := &Workflow{
		Name:    "planned-build",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "work/piece-one", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"piece1.txt"}}},
			{ID: "work/piece-two", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"piece2.txt"}}},
		},
		Edges: []WorkflowEdge{{From: "work/piece-one", To: "work/piece-two", WhenOutcome: "pass"}},
	}

	laneID := first.decision.LaneID
	laneDir := l.laneDirPath(laneID)
	target := l.laneTarget(laneID)

	// Piece 1 commits piece1.txt atop oldBase.
	writeForkFile(t, filepath.Join(target, "piece1.txt"), "piece 1\n")
	runGit(t, target, "add", "piece1.txt")
	runGit(t, target, "commit", "-qm", "feat: piece 1")
	p1Head := runGit(t, target, "rev-parse", "HEAD")

	// Piece 2 starts at p1Head and commits piece2.txt.
	first.decision.Workflow = w
	first.decision.NodeIndex = 1
	first.decision.NodeBaseSHA = p1Head
	first.decision.RunRequest.RunID = "run-item-a-a1-n2"
	sessionID := "11111111-1111-4111-8111-111111111111"
	writeForkFile(t, filepath.Join(laneDir, SessionIDFile), sessionID+"\n")
	writeForkFile(t, filepath.Join(laneDir, BaseSHAFile), oldBase+"\n")
	writeForkFile(t, filepath.Join(laneDir, ItemBaseSHAFile), oldBase+"\n")
	writeForkFile(t, filepath.Join(laneDir, FenceFile), "piece2.txt\n")
	writeForkFile(t, filepath.Join(target, "piece2.txt"), "piece 2 initial\n")
	runGit(t, target, "add", "piece2.txt")
	runGit(t, target, "commit", "-qm", "feat: piece 2")
	p2Head := runGit(t, target, "rev-parse", "HEAD")

	ok, _, err := l.applyPlan(ctx, first, oldBase, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan: ok=%v err=%v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{
			RunID:        first.decision.RunRequest.RunID,
			ItemID:       "item-a",
			LaneID:       laneID,
			Backend:      "fixture",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			FinishedAt:   l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			ArtifactRefs: []run.ArtifactRef{},
			EndCause:     run.EndNormalExit,
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'work/piece-one','run-item-a-a1-n1','passed')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE node_runs SET outcome='passed' WHERE item_id='item-a' AND attempt=1 AND node_id='work/piece-two'`); err != nil {
			return err
		}
		return nil
	})

	// Trunk advances with a repair commit that touches an out-of-scope file.
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs", "trunk-only.md"), "# trunk change\n")
	runGit(t, f.root, "add", "docs/trunk-only.md")
	runGit(t, f.root, "commit", "-qm", "docs: trunk change outside piece scope")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)
	if err := recordTrunkRepair(ctx, f.root, repaired, "trunk repaired at "+shortSHA(repaired)); err != nil {
		t.Fatal(err)
	}

	// Verify pre-rebases the candidate onto the repaired trunk, then the gate fails.
	cand := Candidate{
		ItemID:   "item-a",
		LaneID:   laneID,
		RunID:    first.decision.RunRequest.RunID,
		Worktree: target,
		BaseSHA:  oldBase,
		HeadSHA:  p2Head,
	}
	var verifyRes LoopResult
	_, landed, err := l.Verify(ctx, cand, &verifyRes, nil)
	if err != nil {
		t.Fatalf("Verify failed: %v\n%s", err, report.String())
	}
	if landed {
		t.Fatalf("expected gate failure, not landing\n%s", report.String())
	}
	if len(steps.gated) != 1 || steps.gated[0].BaseSHA != repaired {
		t.Fatalf("expected pre-rebased gate on %s, got %+v\n%s", shortSHA(repaired), steps.gated, report.String())
	}
	rebasedHead := steps.gated[0].HeadSHA
	if rebasedHead == p2Head {
		t.Fatalf("expected rebased head different from %s", shortSHA(p2Head))
	}
	if err := l.markExamined(ctx, first.decision); err != nil {
		t.Fatal(err)
	}

	// Plan and stage the resumed piece-2 run on the rebased candidate.
	resumed := mustPlan(t, l)[0]
	if resumed.decision.Action != ActionDispatch || resumed.decision.NodeIndex != 1 || !resumed.decision.RunRequest.SessionTurn {
		t.Fatalf("expected resumed piece-2 dispatch, got %+v", resumed.decision)
	}
	if ok, _, err := l.applyPlan(ctx, resumed, repaired, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("applyPlan (resumed): ok=%v err=%v", ok, err)
	}
	staged, err := l.stageDispatches(ctx, []Decision{resumed.decision}, repaired, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stageDispatches failed: err=%v staged=%d\n%s", err, len(staged), report.String())
	}
	if staged[0].NodeBaseSHA != rebasedHead {
		t.Fatalf("staged NodeBaseSHA = %s, want rebased head %s (old node base was %s)",
			shortSHA(staged[0].NodeBaseSHA), shortSHA(rebasedHead), shortSHA(p1Head))
	}

	// Resumed piece-2 commits a change only to its in-scope file piece2.txt.
	writeForkFile(t, filepath.Join(target, "piece2.txt"), "piece 2 repaired\n")
	runGit(t, target, "add", "piece2.txt")
	runGit(t, target, "commit", "-qm", "fix: piece 2 gate repair")
	repairHead := runGit(t, target, "rev-parse", "HEAD")

	fencePath := filepath.Join(laneDir, FenceFile)
	escape, err := l.fenceViolationAt(ctx, Candidate{
		ItemID:   "item-a",
		LaneID:   laneID,
		RunID:    staged[0].RunRequest.RunID,
		Worktree: target,
		BaseSHA:  staged[0].NodeBaseSHA,
		HeadSHA:  repairHead,
	}, fencePath)
	if err != nil {
		t.Fatalf("fenceViolationAt failed: %v", err)
	}
	if escape != "" {
		t.Fatalf("resumed piece on rebased candidate was falsely refused: %s", escape)
	}

	// Positive control: an out-of-scope edit by the resumed run itself is still refused.
	writeForkFile(t, filepath.Join(target, "outside.txt"), "escaped\n")
	runGit(t, target, "add", "outside.txt")
	runGit(t, target, "commit", "-qm", "bad: touch out-of-scope file")
	escapedHead := runGit(t, target, "rev-parse", "HEAD")

	escape, err = l.fenceViolationAt(ctx, Candidate{
		ItemID:   "item-a",
		LaneID:   laneID,
		RunID:    staged[0].RunRequest.RunID,
		Worktree: target,
		BaseSHA:  staged[0].NodeBaseSHA,
		HeadSHA:  escapedHead,
	}, fencePath)
	if err != nil {
		t.Fatalf("fenceViolationAt (escape control) failed: %v", err)
	}
	if !strings.Contains(escape, ScopeEscape) || !strings.Contains(escape, "outside.txt") {
		t.Fatalf("expected boundary-scope-escape for outside.txt, got %q", escape)
	}
}

func TestNodeBaseOnCandidateAfterPreGateRebase(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	oldBase := runGit(t, f.root, "rev-parse", TrunkRef)
	lane := l.laneTarget("lane-1")

	writeForkFile(t, filepath.Join(lane, "piece1.txt"), "piece 1\n")
	runGit(t, lane, "add", "piece1.txt")
	runGit(t, lane, "commit", "-qm", "feat: piece 1")
	p1 := runGit(t, lane, "rev-parse", "HEAD")

	writeForkFile(t, filepath.Join(lane, "piece2.txt"), "piece 2\n")
	runGit(t, lane, "add", "piece2.txt")
	runGit(t, lane, "commit", "-qm", "feat: piece 2")
	p2 := runGit(t, lane, "rev-parse", "HEAD")

	// Unrebased lineage keeps the recorded node base for both first and later pieces.
	if got := l.nodeBaseOnCandidate(ctx, lane, oldBase, p1, oldBase); got != oldBase {
		t.Fatalf("unrebased first piece: got %s, want %s", shortSHA(got), shortSHA(oldBase))
	}
	if got := l.nodeBaseOnCandidate(ctx, lane, oldBase, p2, p1); got != p1 {
		t.Fatalf("unrebased later piece: got %s, want %s", shortSHA(got), shortSHA(p1))
	}

	// Rebase onto a newer trunk commit.
	writeForkFile(t, filepath.Join(f.root, "trunk-only.txt"), "trunk\n")
	runGit(t, f.root, "add", "trunk-only.txt")
	runGit(t, f.root, "commit", "-qm", "chore: trunk advance")
	newBase := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, lane, "rebase", "--onto", newBase, oldBase)
	rebasedHead := runGit(t, lane, "rev-parse", "HEAD")

	// Both an off-lineage later-piece node base (p1) and an older-than-base first-piece
	// node base (oldBase) advance to the commit the resumed run actually starts from.
	if got := l.nodeBaseOnCandidate(ctx, lane, newBase, rebasedHead, p1); got != rebasedHead {
		t.Fatalf("rebased later piece: got %s, want %s", shortSHA(got), shortSHA(rebasedHead))
	}
	if got := l.nodeBaseOnCandidate(ctx, lane, newBase, rebasedHead, oldBase); got != rebasedHead {
		t.Fatalf("rebased first piece: got %s, want %s", shortSHA(got), shortSHA(rebasedHead))
	}
}

// When a pre-gate rebase triggers a re-judgment and the first -fresh run ID is already
// recorded as ended in the store (for example from an earlier rebase before a second
// rebase arrived), the new re-judgment must advance to an unused run ID rather than
// reusing the ended run's ID and directory.
func TestPreRebaseRejudgmentAdvancesPastEndedFreshRunID(t *testing.T) {
	ctx := context.Background()
	f, l, baseSteps, c := prepareJudgeCandidate(t, "pass")
	var dispatchedRunID string
	rebasedSteps := &rebasedJudgeRouteSteps{judgeRouteSteps: baseSteps}
	custom := &callbackSteps{
		rebasedJudgeRouteSteps: rebasedSteps,
		beforeRun: func(req run.Request) {
			dispatchedRunID = req.RunID
		},
		runFn: rebasedSteps.Run,
	}
	l.cfg.Steps = custom

	rebasedFrom := c.HeadSHA
	writeForkFile(t, filepath.Join(c.Worktree, "rebased.txt"), "work rebased\n")
	runGit(t, c.Worktree, "add", "rebased.txt")
	runGit(t, c.Worktree, "commit", "-qm", "feat: work rebased")
	c.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")
	c.RebasedFromHead = rebasedFrom

	origRunID := "run-item-a-a1-n3"
	endedFreshID := markRunID(origRunID, "fresh")
	at := l.cfg.Now().Format(time.RFC3339)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID: endedFreshID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "reviewer",
			State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, EndCause: run.EndNormalExit,
		})
	})

	var binding item.EvaluatorBinding
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var err error
		binding, err = item.ReadEvaluatorBinding(r, c.ItemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c.Judge = &JudgeReceipt{
		Request: run.Request{ItemID: c.ItemID, LaneID: c.LaneID, RunID: origRunID, Backend: "reviewer", Seat: "review", Node: "judge", Workflow: binding.Workflow, NodeID: binding.EvaluatorNode},
		Raw:     json.RawMessage(`{"outcome":"pass"}`),
	}
	c.Judge.Options.ItemID = c.ItemID
	c.Judge.Options.Head = rebasedFrom

	res := &LoopResult{}
	_, accepted, err := l.judgeCandidate(ctx, c, res)
	if err != nil {
		t.Fatalf("judgeCandidate returned unexpected error: %v", err)
	}
	want := markRunID(endedFreshID, "fresh")
	if dispatchedRunID != want {
		t.Fatalf("re-judgment dispatched RunID %q, want unused %q (ended %q)", dispatchedRunID, want, endedFreshID)
	}
	if !accepted {
		t.Fatalf("judgeCandidate did not accept: refused=%v detail=%q", res.RefusedSteps, res.Detail)
	}
}
