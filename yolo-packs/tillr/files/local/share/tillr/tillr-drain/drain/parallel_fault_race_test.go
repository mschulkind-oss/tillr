package drain

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The primary reader of a parallel read-only group, replaced for an unreadable report,
// stays inside the group's bracket: once the replacement has ended, the examination
// coordinator does not claim the group while runParallel is still running, so settlement
// never reads a node run whose id the next replacement is about to overwrite (the
// `settle: sql: no rows in result set` fault).
func TestReplacedPrimaryReaderStaysInsideTheBracketUntilTheGroupEnds(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.writerAfter = 5
	steps.badReportNode = "-n1"
	steps.badReport = "no report footer\n"
	var mu sync.Mutex
	var examinable []examinableRun
	var bracketed bool
	steps.afterTerminal = func(req run.Request) error {
		if !strings.HasSuffix(req.RunID, "-n1-restart") {
			return nil
		}
		found, err := l.findExaminableRuns(context.Background())
		l.mu.Lock()
		bracketed = l.activeBrackets[req.RunID]
		l.mu.Unlock()
		mu.Lock()
		examinable = found
		mu.Unlock()
		return err
	}
	result := &LoopResult{}
	for i := 0; i < 5; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	mu.Lock()
	defer mu.Unlock()
	if !bracketed {
		t.Error("the replacement of the primary reader was not registered in activeBrackets while runParallel ran")
	}
	for _, c := range examinable {
		if c.itemID == "item-a" {
			t.Errorf("the examination coordinator would claim the group while its readers still run: %+v", c)
		}
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var faults int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, itemFaultKind).Scan(&faults); err != nil {
			return err
		}
		if faults != 0 {
			t.Errorf("%d item faults recorded, want none", faults)
		}
		return nil
	})
}

// No restart of a node is claimed or dispatched while its item holds an open fault: the
// replacement claim refuses without writing, the group launches no replacement, and the
// stage and launch coordinators skip the item.
func TestNoReplacementIsClaimedOrLaunchedUnderAnOpenItemFault(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.writerAfter = 5
	steps.badReportNode = "-n1"
	steps.badReport = "no report footer\n"
	ctx := context.Background()
	var mu sync.Mutex
	var claimedUnderFault bool
	var startedBefore, startedAfter, nodeRunsBefore, nodeRunsAfter int
	var stageable, launchable int
	injected := false
	steps.beforeTerminal = func(req run.Request) error {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasSuffix(req.RunID, "-n1") || injected {
			return nil
		}
		injected = true
		if err := l.holdItemFault(ctx, Candidate{ItemID: "item-a", LaneID: req.LaneID, RunID: req.RunID}, ReasonStageFailed, "a fault is open"); err != nil {
			return err
		}
		work := pendingForTest(t, f, "item-a")
		nodes := parallelNodes(work.Decision)
		replacement := nodes[0]
		next := *replacement.RunRequest
		next.RunID = markRunID(next.RunID, "restart")
		replacement.RunRequest = &next
		startedBefore = storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind=?`, dispatchStartedKind)
		nodeRunsBefore = storeCount(t, f, `SELECT COUNT(*) FROM node_runs WHERE run_id=?`, next.RunID)
		updated, claimed, err := l.claimParallelReplacement(ctx, nodes, 0, replacement)
		claimedUnderFault = claimed || updated[0].RunRequest.RunID != nodes[0].RunRequest.RunID
		startedAfter = storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind=?`, dispatchStartedKind)
		nodeRunsAfter = storeCount(t, f, `SELECT COUNT(*) FROM node_runs WHERE run_id=?`, next.RunID)
		if err != nil {
			return err
		}
		stage, err := l.findStageableRuns(ctx)
		if err != nil {
			return err
		}
		launch, err := l.findLaunchableRuns(ctx)
		if err != nil {
			return err
		}
		stageable, launchable = len(stage), len(launch)
		return nil
	}
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	mu.Lock()
	defer mu.Unlock()
	if !injected {
		t.Fatal("the fault was never injected")
	}
	if claimedUnderFault || startedAfter != startedBefore || nodeRunsAfter != nodeRunsBefore {
		t.Errorf("claim under an open fault: claimed=%v drain.started %d->%d node_runs %d->%d, want nothing written", claimedUnderFault, startedBefore, startedAfter, nodeRunsBefore, nodeRunsAfter)
	}
	if stageable != 0 || launchable != 0 {
		t.Errorf("an item under an open fault was found stageable=%d launchable=%d", stageable, launchable)
	}
	if restarts := storeCount(t, f, `SELECT COUNT(*) FROM runs WHERE run_id LIKE '%-restart%'`); restarts != 0 {
		t.Errorf("%d restart runs started under an open fault, want none", restarts)
	}
}
