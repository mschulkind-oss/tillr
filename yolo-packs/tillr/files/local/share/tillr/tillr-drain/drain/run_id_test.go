package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

// A node's run id carries one counter however many times it fails over or restarts, so
// 100+ failovers and >999 mixed turns stay distinct, carry no repeated marker words, and
// remain strictly under 120 characters even on long item stems.
func TestARunIDDoesNotGrowPerFailoverOrRestart(t *testing.T) {
	for _, kinds := range [][]string{{"failover"}, {"restart"}, {"failover", "restart"}, {"restart", "failover", "fresh", "interrupted"}} {
		id := "run-item-a-a1-n2"
		var second string
		for turn := 1; turn <= 30; turn++ {
			id = markRunID(id, kinds[turn%len(kinds)])
			if turn == 2 {
				second = id
			}
		}
		if len(id) > len(second) {
			t.Fatalf("%v: the 30th id %q is longer than the 2nd %q", kinds, id, second)
		}
	}
	if got := markRunID("run-x-n1", "restart"); got != "run-x-n1-restart" {
		t.Fatalf("the first restart is %q, want the unnumbered form", got)
	}
	if got := markRunID("run-x-n1-restart", "restart"); got != "run-x-n1-002-restart" {
		t.Fatalf("the second restart is %q", got)
	}
}

func TestARunFailedOverOneHundredTimesStaysUnder120Characters(t *testing.T) {
	seen := map[string]int{}
	id := attemptRunID("item-request-b983843cc9", 1) + "-n1"
	for turn := 1; turn <= 100; turn++ {
		id = markRunID(id, "failover")
		if len(id) >= 120 {
			t.Fatalf("turn %d id %q has length %d, want < 120", turn, id, len(id))
		}
		if strings.Count(id, "-failover") != 1 {
			t.Fatalf("turn %d id %q repeats -failover", turn, id)
		}
		if prev, dup := seen[id]; dup {
			t.Fatalf("turn %d collided with turn %d on %q", turn, prev, id)
		}
		seen[id] = turn
	}
	if id != "run-item-request-b983843cc9-a1-n1-100-failover" {
		t.Fatalf("100th failover id = %q", id)
	}

	// Continue past 999 mixed turns without clamping or colliding, still < 120 chars.
	kinds := []string{"failover", "restart", "fresh", "interrupted"}
	for turn := 101; turn <= 1050; turn++ {
		id = markRunID(id, kinds[turn%len(kinds)])
		if len(id) >= 120 {
			t.Fatalf("turn %d id %q has length %d, want < 120", turn, id, len(id))
		}
		if prev, dup := seen[id]; dup {
			t.Fatalf("turn %d collided with turn %d on %q", turn, prev, id)
		}
		seen[id] = turn
	}
	if !strings.HasSuffix(id, "-1050-fresh") {
		t.Fatalf("1050th turn id = %q, want -1050-fresh suffix", id)
	}
}

func TestLongStemsJudgeAndConflictFixSuffixesStayBoundedUnder120Characters(t *testing.T) {
	longItem := "item-request-" + strings.Repeat("very-long-component-", 10)
	base := attemptRunID(longItem, 42)
	if len(base) >= 120 {
		t.Fatalf("attemptRunID(%d chars) = %q (%d chars), want < 120", len(longItem), base, len(base))
	}
	if got := attemptFromRunID(longItem, base+"-n1"); got != 42 {
		t.Fatalf("attemptFromRunID on bounded stem = %d, want 42", got)
	}
	if boundRunIDBase(base) != base {
		t.Fatalf("boundRunIDBase is not idempotent on %q", base)
	}

	id := base + "-n1"
	for i := 0; i < 100; i++ {
		id = markRunID(id, "failover")
	}
	if len(id) >= 120 {
		t.Fatalf("100 failovers on long item = %q (%d chars), want < 120", id, len(id))
	}

	jID := judgeRunID(id)
	if !strings.HasSuffix(jID, "-judge") || len(jID) >= 120 {
		t.Fatalf("judgeRunID = %q (%d chars), want -judge suffix and < 120 chars", jID, len(jID))
	}
	jrID := markRunID(jID, "restart")
	if !strings.HasSuffix(jrID, "-judge-restart") || len(jrID) >= 120 {
		t.Fatalf("restarted judge id = %q (%d chars), want -judge-restart suffix and < 120 chars", jrID, len(jrID))
	}

	cfID := id
	for i := 0; i < 20; i++ {
		cfID = conflictFixRunID(cfID)
		if !strings.HasSuffix(cfID, ConflictFixSuffix) || len(cfID) >= 120 {
			t.Fatalf("conflictFixRunID iter %d = %q (%d chars)", i, cfID, len(cfID))
		}
	}

	// Legacy pre-fix id with 24 repeated -failover and 3 -restart words collapses below 120 chars.
	legacy := "run-item-request-add5da8454-a3-n1" + strings.Repeat("-failover", 24) + strings.Repeat("-restart", 3)
	collapsed := markRunID(legacy, "restart")
	if collapsed != "run-item-request-add5da8454-a3-n1-028-restart" || len(collapsed) >= 120 {
		t.Fatalf("collapsed legacy id = %q (%d chars)", collapsed, len(collapsed))
	}
}

func TestARunIDKeepsItsKindSuffixAndWalksBackToTheNodesFirstRun(t *testing.T) {
	id := markRunID(markRunID(markRunID("run-x-n1", "restart"), "restart"), "restart")
	if id != "run-x-n1-003-restart" {
		t.Fatalf("the third restart is %q", id)
	}
	want := []string{"run-x-n1-002-restart", "run-x-n1-restart", "run-x-n1"}
	got := runIDAncestors(id)
	if len(got) != len(want) {
		t.Fatalf("ancestors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ancestors = %v, want %v", got, want)
		}
	}
	if runIDAncestors("run-x-n1") != nil {
		t.Fatal("a node's own id has no ancestors")
	}
}

func TestStagedNodeRunFindsArchiveAcrossMixedContinuationsAndBoundedStems(t *testing.T) {
	root := t.TempDir()
	l := &Loop{cfg: LoopConfig{Root: root}}
	longItem := "item-request-" + strings.Repeat("segment-", 12)
	wf := &Workflow{Stages: []WorkflowStage{{ID: "plan"}}}

	// Stage archive on turn 1 as -failover, then query from turn 105 as -restart.
	stem := attemptRunID(longItem, 1) + "-n1"
	turn1 := markRunID(stem, "failover")
	if err := os.MkdirAll(RunDir(root, turn1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(RunDir(root, turn1), BaseSHAFile), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cur := turn1
	for i := 2; i <= 105; i++ {
		if i%2 == 0 {
			cur = markRunID(cur, "interrupted")
		} else {
			cur = markRunID(cur, "restart")
		}
	}
	d := Decision{
		ItemID:     longItem,
		Attempt:    1,
		NodeIndex:  0,
		Workflow:   wf,
		RunRequest: &run.Request{RunID: cur},
	}
	got, err := l.stagedNodeRun(d)
	if err != nil {
		t.Fatalf("stagedNodeRun failed: %v", err)
	}
	if got != turn1 {
		t.Fatalf("stagedNodeRun = %q, want %q", got, turn1)
	}
}

func TestAdapterRefusalWordsClassifyAnExit2AsRefusedNotInfrastructure(t *testing.T) {
	code := 2
	rec := run.Record{State: run.StateExited, ExitCode: &code}
	log := "noise\nrun-pi: refused: the brief 'b' has no '## Fence' section\n"
	if got := ClassifyAttempt(rec, log); got != AttemptRefused {
		t.Fatalf("an adapter refusal classified %q, want %q", got, AttemptRefused)
	}
	if words, _ := AdapterRefusal(log); words != "the brief 'b' has no '## Fence' section" {
		t.Fatalf("words = %q", words)
	}
	if got := ClassifyAttempt(rec, "usage: x\n"); got != AttemptInfrastructure {
		t.Fatalf("an exit 2 with no refusal words classified %q, want infrastructure", got)
	}
	if got := ClassifyAttempt(rec, "see: refused: in prose\n"); got != AttemptInfrastructure {
		t.Fatalf("prose containing the marker classified %q", got)
	}
}
