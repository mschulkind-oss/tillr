package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
