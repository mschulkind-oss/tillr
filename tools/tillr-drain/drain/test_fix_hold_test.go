package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func writeGateLog(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type testFixSteps struct {
	obstacleRequestSteps
	mu        sync.Mutex
	gateCalls []Candidate
	runIDs    []string
	gateFn    func(c Candidate) (GateOutcome, error)
	replays   map[string]ReplayOutcome
	commitSeq int
}

func (s *testFixSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.mu.Lock()
	s.commitSeq++
	seq := s.commitSeq
	s.runIDs = append(s.runIDs, req.RunID)
	s.mu.Unlock()
	if req.ItemID != "" && !IsTrunkFixer(req.ItemID) {
		target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
		name := req.ItemID + ".txt"
		path := filepath.Join(target, name)
		if err := os.WriteFile(path, []byte("change "+string(rune('0'+seq))+"\n"), 0o644); err != nil {
			return run.Record{}, "", err
		}
		runGit(s.t, target, "add", name)
		runGit(s.t, target, "commit", "-qm", "candidate commit "+req.ItemID)
	}
	return s.obstacleRequestSteps.Run(ctx, req)
}

func (s *testFixSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	s.gateCalls = append(s.gateCalls, c)
	fn := s.gateFn
	s.mu.Unlock()
	return fn(c)
}

func (s *testFixSteps) Replay(_ context.Context, _ Candidate, rev string, _ []string) (ReplayOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if out, ok := s.replays[rev]; ok {
		return out, nil
	}
	return ReplayOutcome{Rev: rev, Known: true}, nil
}

func hasRefusalStep(t *testing.T, f refusalLoopFixture, itemID, step string) bool {
	t.Helper()
	var count int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT count(*) FROM events WHERE kind = ? AND json_extract(payload, '$.item_id') = ? AND json_extract(payload, '$.step') = ?`,
			RefusalKind, itemID, step,
		).Scan(&count)
	})
	return count > 0
}

// TestCandidateHeldBehindOpenTestFixItemAndRegatedOnLanding verifies both halves
// of the open-test-fix hold on a single lane:
//  1. When a candidate's gate fails only on a test that an open item names as
//     the test it fixes (and trunk replay does not confirm trunk red), the
//     candidate is held blocked by that item with no refusal recorded, keeping
//     its commit and releasing its lane so the fixing item can run and land.
//  2. When the fixing item lands, the held candidate is rebased onto the new
//     trunk and gated again without a new worker run, then lands.
func TestCandidateHeldBehindOpenTestFixItemAndRegatedOnLanding(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)

	candID := fileFixtureRequestWith(t, f, "Implement feature change.\n", "build", item.SingleWorkerWorkflow, []string{"cand.txt"}, true)
	fixerID := fileFixtureRequestWith(t, f,
		"TestOneAppliedPassSharesItsGenerationAcrossRequests fails under load in trigger_test.go; fix the race at its cause.\n",
		"build", item.SingleWorkerWorkflow, []string{"fixer.txt"}, true)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET title=? WHERE item_id=?`,
			"Make the one-pass-one-generation drain test pass under load, at its cause", fixerID); err != nil {
			return err
		}
		// Ensure candID runs first on the single lane.
		if _, err := tx.Exec(`UPDATE items SET rank='a' WHERE item_id=?`, candID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET rank='b' WHERE item_id=?`, fixerID)
		return err
	})

	logDir := t.TempDir()
	failLog := writeGateLog(t, logDir, "drain-unit.log",
		"=== RUN   TestOneAppliedPassSharesItsGenerationAcrossRequests\n"+
			"    trigger_test.go:101: one pass allocated multiple generations\n"+
			"--- FAIL: TestOneAppliedPassSharesItsGenerationAcrossRequests (0.04s)\n"+
			"FAIL\n")

	var candRuns, fixerRuns int
	var candGates, fixerGates []Candidate
	steps := &testFixSteps{
		obstacleRequestSteps: obstacleRequestSteps{
			t: t, f: f,
			report: func(req run.Request) (string, *landingOffer) {
				return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
			},
		},
		// Replay passes on base (load-sensitive failure, not trunk-already-red).
		replays: map[string]ReplayOutcome{
			base: {Rev: base, Red: nil, Known: true},
		},
	}
	steps.gateFn = func(c Candidate) (GateOutcome, error) {
		if c.ItemID == candID {
			candGates = append(candGates, c)
			if len(candGates) == 1 {
				return GateOutcome{
					Outcome:       GateFailed,
					FailingChecks: []string{"go-tool/tools-tillr-drain"},
					CheckLogs:     map[string]string{"go-tool/tools-tillr-drain": failLog},
				}, nil
			}
			return GateOutcome{Outcome: GatePassed}, nil
		}
		fixerGates = append(fixerGates, c)
		return GateOutcome{Outcome: GatePassed}, nil
	}
	l.cfg.Steps = steps
	var report strings.Builder
	l.cfg.Report = &report

	// Pass 1: candID runs on the single lane, gates, fails only on the test
	// fixerID fixes, and is held blocked by fixerID while releasing its lane.
	res1 := &LoopResult{}
	if _, err := l.pass(ctx, res1); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, res1)

	for _, rid := range steps.runIDs {
		if strings.Contains(rid, candID) {
			candRuns++
		} else if strings.Contains(rid, fixerID) {
			fixerRuns++
		}
	}
	if candRuns != 1 || len(candGates) != 1 {
		t.Fatalf("after pass 1: candRuns=%d candGates=%d (runs=%v, report=%s), want 1 and 1", candRuns, len(candGates), steps.runIDs, report.String())
	}
	if got := fixerState(t, f, candID); got != StateBlocked {
		t.Fatalf("after pass 1: candidate state = %s, want %s", got, StateBlocked)
	}
	var wait *item.WaitingOn
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		wait, _, err = item.ReadWaitingOn(r, candID)
		return err
	})
	if wait == nil || wait.Waiter != "item:"+fixerID || !strings.HasPrefix(wait.Reason, TestFixWaitPrefix) {
		t.Fatalf("candidate waiting_on = %+v, want Waiter=item:%s with prefix %q", wait, fixerID, TestFixWaitPrefix)
	}
	if hasRefusalStep(t, f, candID, RefusalGate) {
		t.Fatalf("unexpected gate refusal recorded for %s", candID)
	}
	if d := planFor(t, l, candID); d.Action != ActionHold || d.Reason != ReasonBlocked || d.DeferredBy != fixerID {
		t.Fatalf("held plan = %+v, want hold/%s deferred by %s", d, ReasonBlocked, fixerID)
	}

	// Pass 2: with the single lane released, fixerID runs, gates, and lands.
	// candID must not be dispatched again while fixerID is still open.
	res2 := &LoopResult{}
	if _, err := l.pass(ctx, res2); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, res2)

	if got := fixerState(t, f, fixerID); got != StateLanded {
		t.Fatalf("after pass 2: fixer state = %s, want %s", got, StateLanded)
	}
	landedTip := runGit(t, f.root, "rev-parse", TrunkRef)
	if landedTip == base {
		t.Fatalf("expected trunk to advance when %s landed", fixerID)
	}

	// Pass 3: fixerID has landed, so candID is released, rebased onto the new
	// trunk tip, and re-gated WITHOUT a new worker run, then lands.
	res3 := &LoopResult{}
	if _, err := l.pass(ctx, res3); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, res3)

	candRuns = 0
	for _, rid := range steps.runIDs {
		if strings.Contains(rid, candID) {
			candRuns++
		}
	}
	if candRuns != 1 {
		t.Fatalf("candidate worker runs = %d, want 1 (no new worker run after fixer landed)", candRuns)
	}
	if len(candGates) != 2 {
		t.Fatalf("candidate gate calls = %d, want 2", len(candGates))
	}
	if candGates[1].RunID != candGates[0].RunID {
		t.Fatalf("re-gated run = %s, want same run %s", candGates[1].RunID, candGates[0].RunID)
	}
	if candGates[1].BaseSHA != landedTip {
		t.Fatalf("re-gated base = %s, want rebased onto landed fixer %s", candGates[1].BaseSHA, landedTip)
	}
	if got := fixerState(t, f, candID); got != StateLanded {
		t.Fatalf("after pass 3: candidate state = %s, want %s", got, StateLanded)
	}
}

func TestCandidateNotHeldWhenGateFailsOnMultipleTests(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)

	candID := fileFixtureRequestWith(t, f, "Feature work.\n", "build", item.SingleWorkerWorkflow, []string{"cand.txt"}, true)
	fixerID := fileFixtureRequestWith(t, f, "Fix TestAlpha under load.\n", "build", item.SingleWorkerWorkflow, []string{"fixer.txt"}, true)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET title='Fix TestAlpha', rank='b' WHERE item_id=?`, fixerID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET rank='a' WHERE item_id=?`, candID)
		return err
	})

	failLog := writeGateLog(t, t.TempDir(), "unit.log",
		"--- FAIL: TestAlpha (0.01s)\n--- FAIL: TestBeta (0.01s)\nFAIL\n")
	steps := &acquittalRegateSteps{
		obstacleRequestSteps: obstacleRequestSteps{
			t: t, f: f,
			report: func(req run.Request) (string, *landingOffer) {
				return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
			},
		},
		replays: map[string]ReplayOutcome{
			base: {Rev: base, Red: nil, Known: true},
		},
		gateFn: func(c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateFailed,
				FailingChecks: []string{"unit"},
				CheckLogs:     map[string]string{"unit": failLog},
			}, nil
		},
	}
	l.cfg.Steps = steps

	res := &LoopResult{}
	if _, err := l.pass(ctx, res); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, res)

	if got := fixerState(t, f, candID); got == StateBlocked {
		t.Fatalf("candidate with two failing tests was unexpectedly held blocked")
	}
	if !hasRefusalStep(t, f, candID, RefusalGate) {
		t.Fatalf("expected ordinary gate refusal when two tests failed")
	}
}

func TestCandidateNotHeldWhenOpenItemOnlyMentionsTestIncidentally(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)

	candID := fileFixtureRequestWith(t, f, "Feature work.\n", "build", item.SingleWorkerWorkflow, []string{"cand.txt"}, true)
	otherID := fileFixtureRequestWith(t, f,
		"What happened: item-request-abc's gate failed only on TestAlpha, the load-sensitive test that open item item-request-xyz fixes. Hold the candidate instead of redoing it.\n",
		"build", item.SingleWorkerWorkflow, []string{"other.txt"}, true)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET title='A candidate refused only by a test an open item is fixing waits for that fix', rank='b' WHERE item_id=?`, otherID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET rank='a' WHERE item_id=?`, candID)
		return err
	})

	failLog := writeGateLog(t, t.TempDir(), "unit.log", "--- FAIL: TestAlpha (0.01s)\nFAIL\n")
	steps := &acquittalRegateSteps{
		obstacleRequestSteps: obstacleRequestSteps{
			t: t, f: f,
			report: func(req run.Request) (string, *landingOffer) {
				return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
			},
		},
		replays: map[string]ReplayOutcome{
			base: {Rev: base, Red: nil, Known: true},
		},
		gateFn: func(c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateFailed,
				FailingChecks: []string{"unit"},
				CheckLogs:     map[string]string{"unit": failLog},
			}, nil
		},
	}
	l.cfg.Steps = steps

	res := &LoopResult{}
	if _, err := l.pass(ctx, res); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, res)

	if got := fixerState(t, f, candID); got == StateBlocked {
		t.Fatalf("candidate was falsely held behind an item that only mentioned TestAlpha incidentally")
	}
	if !hasRefusalStep(t, f, candID, RefusalGate) {
		t.Fatalf("expected ordinary gate refusal")
	}
}

func TestOpenTestFixerDoesNotBlockOnSelf(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)

	fixerID := fileFixtureRequestWith(t, f, "Fix TestAlpha under load.\n", "build", item.SingleWorkerWorkflow, []string{"fixer.txt"}, false)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET title='Fix TestAlpha' WHERE item_id=?`, fixerID)
		return err
	})

	failLog := writeGateLog(t, t.TempDir(), "unit.log", "--- FAIL: TestAlpha (0.01s)\nFAIL\n")
	steps := &acquittalRegateSteps{
		obstacleRequestSteps: obstacleRequestSteps{
			t: t, f: f,
			report: func(req run.Request) (string, *landingOffer) {
				return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
			},
		},
		replays: map[string]ReplayOutcome{
			base: {Rev: base, Red: nil, Known: true},
		},
		gateFn: func(c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateFailed,
				FailingChecks: []string{"unit"},
				CheckLogs:     map[string]string{"unit": failLog},
			}, nil
		},
	}
	l.cfg.Steps = steps

	res := &LoopResult{}
	if _, err := l.pass(ctx, res); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, res)

	if got := fixerState(t, f, fixerID); got == StateBlocked {
		t.Fatalf("fixer item blocked on itself")
	}
	if !hasRefusalStep(t, f, fixerID, RefusalGate) {
		t.Fatalf("expected ordinary gate refusal for fixer failing its own test")
	}
}

func TestSingleFailingTestNames(t *testing.T) {
	dir := t.TempDir()
	triageOne := writeGateLog(t, dir, "triage-one.log",
		"fork-check: triage -- 1 failing check\n"+
			"  triage go-tool/drain: drain tests\n"+
			"    case: TestOneAppliedPassSharesItsGenerationAcrossRequests -- trigger_test.go:101\n"+
			"    assertion: one pass allocated multiple generations\n")
	triageTwo := writeGateLog(t, dir, "triage-two.log",
		"fork-check: triage -- 1 failing check\n"+
			"  triage go-tool/drain: drain tests\n"+
			"    case: TestOne -- a_test.go:10\n"+
			"    case: TestTwo -- b_test.go:20\n")
	triageUnnamed := writeGateLog(t, dir, "triage-unnamed.log",
		"fork-check: triage -- 1 failing check\n"+
			"  triage lint: lint check\n"+
			"    no case named in the output; reproduce with: lint\n")
	rawGoOne := writeGateLog(t, dir, "go-one.log", "--- FAIL: TestAlpha (0.02s)\nFAIL\n")
	rawGoSub := writeGateLog(t, dir, "go-sub.log", "--- FAIL: TestAlpha (0.02s)\n    --- FAIL: TestAlpha/case_1 (0.01s)\nFAIL\n")
	rawGoTwoSubs := writeGateLog(t, dir, "go-two-subs.log", "--- FAIL: TestAlpha (0.02s)\n    --- FAIL: TestAlpha/a (0.01s)\n    --- FAIL: TestAlpha/b (0.01s)\nFAIL\n")
	rawBuildFail := writeGateLog(t, dir, "build-fail.log", "FAIL\tgithub.com/example/pkg [build failed]\n")
	rawShard := writeGateLog(t, dir, "shard.log", "go-test-shards: drain shard 2 failed (exit 1): TestShardedFlake\n")

	cases := []struct {
		name string
		gate GateOutcome
		want []string
	}{
		{
			name: "triage block single test",
			gate: GateOutcome{FailingChecks: []string{"gate:python3"}, CheckLogs: map[string]string{"gate:python3": triageOne}},
			want: []string{"TestOneAppliedPassSharesItsGenerationAcrossRequests"},
		},
		{
			name: "triage block two tests",
			gate: GateOutcome{FailingChecks: []string{"gate:python3"}, CheckLogs: map[string]string{"gate:python3": triageTwo}},
			want: nil,
		},
		{
			name: "triage block unnamed failure",
			gate: GateOutcome{FailingChecks: []string{"gate:python3"}, CheckLogs: map[string]string{"gate:python3": triageUnnamed}},
			want: nil,
		},
		{
			name: "raw go test single",
			gate: GateOutcome{FailingChecks: []string{"unit"}, CheckLogs: map[string]string{"unit": rawGoOne}},
			want: []string{"TestAlpha"},
		},
		{
			name: "raw go test single subtest",
			gate: GateOutcome{FailingChecks: []string{"unit"}, CheckLogs: map[string]string{"unit": rawGoSub}},
			want: []string{"TestAlpha/case_1", "TestAlpha"},
		},
		{
			name: "raw go test multiple subtests same parent",
			gate: GateOutcome{FailingChecks: []string{"unit"}, CheckLogs: map[string]string{"unit": rawGoTwoSubs}},
			want: []string{"TestAlpha"},
		},
		{
			name: "go build failure",
			gate: GateOutcome{FailingChecks: []string{"unit"}, CheckLogs: map[string]string{"unit": rawBuildFail}},
			want: nil,
		},
		{
			name: "go-test-shards single test",
			gate: GateOutcome{FailingChecks: []string{"unit"}, CheckLogs: map[string]string{"unit": rawShard}},
			want: []string{"TestShardedFlake"},
		},
		{
			name: "second failing check has no log",
			gate: GateOutcome{FailingChecks: []string{"unit", "lint"}, CheckLogs: map[string]string{"unit": rawGoOne}},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := singleFailingTestNames("", Candidate{}, tc.gate)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("singleFailingTestNames = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestItemFixesTest(t *testing.T) {
	cases := []struct {
		name     string
		title    string
		criteria []string
		prose    string
		words    string
		testName string
		want     bool
	}{
		{
			name:     "title names test with fix intent",
			title:    "Fix TestAlpha under load",
			testName: "TestAlpha",
			want:     true,
		},
		{
			name:     "criterion names test with pass intent",
			title:    "Drain stability",
			criteria: []string{"TestAlpha passes under load"},
			testName: "TestAlpha",
			want:     true,
		},
		{
			name:     "test-fix title with test named in request words",
			title:    "Make the one-pass-one-generation drain test pass under load, at its cause",
			words:    "TestOneAppliedPassSharesItsGenerationAcrossRequests fails intermittently in trigger_test.go.",
			testName: "TestOneAppliedPassSharesItsGenerationAcrossRequests",
			want:     true,
		},
		{
			name:     "direct fix statement in request words",
			title:    "trigger_test.go race",
			words:    "Fix TestOneAppliedPassSharesItsGenerationAcrossRequests at its cause.",
			testName: "TestOneAppliedPassSharesItsGenerationAcrossRequests",
			want:     true,
		},
		{
			name:     "incidental mention in incident report",
			title:    "A candidate refused only by a test an open item is fixing waits for that fix instead of being redone",
			words:    "What happened, 2026-11-23 (ET): item-request-c37e25c799's gate failed at 05:15 only on TestOneAppliedPassSharesItsGenerationAcrossRequests (\"one pass allocated multiple generations\", trigger_test.go), the load-sensitive test that open item item-request-2eb7c49739 (\"Make the one-pass-one-generation drain test pass under load, at its cause\") fixes.",
			testName: "TestOneAppliedPassSharesItsGenerationAcrossRequests",
			want:     false,
		},
		{
			name:     "substring identifier does not match",
			title:    "Fix TestAlphaExtended",
			testName: "TestAlpha",
			want:     false,
		},
		{
			name:     "negated mention in words does not match",
			title:    "Refactor drain",
			words:    "Do not fix TestAlpha in this item.",
			testName: "TestAlpha",
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := itemFixesTest(tc.title, tc.criteria, tc.prose, tc.words, tc.testName); got != tc.want {
				t.Fatalf("itemFixesTest = %v, want %v", got, tc.want)
			}
		})
	}
}
