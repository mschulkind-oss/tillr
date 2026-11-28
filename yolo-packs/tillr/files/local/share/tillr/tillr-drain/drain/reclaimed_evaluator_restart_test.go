package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// reclaimedEvaluatorRun builds the sequence a loop relaunch began for an item on 2026-11-28:
// its verify run was live at the relaunch, so the starting loop reclaimed the run's lease
// (run.finish timed-out, lease-reclaimed) and planned the workflow restart of the verify
// node. The build node's commit is kept in the lane.
//
// It answers the starting loop, the ended verify run id, the kept build commit and the
// restart the starting loop planned.
func reclaimedEvaluatorRun(t *testing.T) (refusalLoopFixture, *Loop, *judgeSignalSteps, string, string, plannedApplication) {
	t.Helper()
	f, l, ws := workflowLoopFixture(t, "")
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture", Model: "review-model"}
	l.cfg.Policy.Seats["review"] = "reviewer"
	raw := `{"name":"eval-reclaim","version":1,"stages":[{"id":"build","kind":"agent","seat":"build","access":"write","prompt":"build"},{"id":"verify","kind":"agent","seat":"review","access":"read_only","judge_classes":["verification-only"],"prompt":"verify"}],"edges":[{"from_node":"build","to_node":"verify","when_outcome":"pass"}],"exit":{"evaluator_slot":"verify","requires_nodes":["build","verify"]}}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/eval-reclaim@v1.json"), raw)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "eval-reclaim workflow")
	ws.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET workflow='eval-reclaim@v1' WHERE item_id='item-a'`)
		return err
	})
	// The first verify launch is the one the relaunch cut off; it never answers here. The
	// next one passes.
	steps := &judgeSignalSteps{workflowSteps: ws, judgeCount: 1}
	l.cfg.Steps = reclaimSteps{steps, f.root}

	result := &LoopResult{}
	var verify plannedApplication
	for i := 0; i < 8; i++ {
		p := decisionFor(t, l, "item-a")
		if p.decision.Action == ActionDispatch && p.decision.RunRequest != nil && p.decision.RunRequest.NodeID == "verify" {
			verify = p
			break
		}
		graphPass(t, l, result)
	}
	if verify.decision.RunRequest == nil {
		t.Fatalf("the verify node was never offered; runs: %+v", steps.runs)
	}
	id := verify.decision.RunRequest.RunID
	lane := verify.decision.LaneID
	kept := runGit(t, l.laneTarget(lane), "rev-parse", "HEAD")

	// The verify run starts after the build run's recorded finish, as it did in the store.
	l.cfg.Now = afterTheBuild
	// The verify run launches under a loop that the relaunch ends.
	owner := relaunchedJailClaim(t, f, l, verify)
	recordSession(t, f, owner, true)
	runStreams(t, f, id)

	// The starting loop reclaims the run's lease and records the verify node's restart.
	starting := replaceGraphLoop(t, f, l, l.cfg.Steps)
	starting.cfg.Now = afterTheBuild
	reapAtStart(t, starting)
	if rec := terminalFor(t, f, id); rec == nil || rec.EndCause != "lease-reclaimed" {
		t.Fatalf("the cut-off verify run has no lease-reclaimed record: %+v", rec)
	}
	restart := decisionFor(t, starting, "item-a")
	if d := restart.decision; d.Action != ActionDispatch || !d.WorkflowRestart || d.RunRequest == nil || d.RunRequest.RunID != markRunID(id, "restart") {
		t.Fatalf("the starting loop did not restart the ended verify run under a fresh run id: %+v", d)
	}
	return f, starting, steps, id, kept, restart
}

// abandonedRestartClaim records the claim the starting loop wrote before this fix: the
// verify node's restart under the ended run's own id, whose loop is gone before any run is
// allocated. That is the state the specimen item still holds in the store.
func abandonedRestartClaim(t *testing.T) (refusalLoopFixture, *Loop, *judgeSignalSteps, string, string) {
	t.Helper()
	f, starting, steps, id, kept, restart := reclaimedEvaluatorRun(t)
	req := *restart.decision.RunRequest
	req.RunID = id
	restart.decision.RunRequest = &req
	identifyDecision(&restart.decision)
	relaunchedJailClaim(t, f, starting, restart)
	if holder := laneHolder(t, f, restart.decision.LaneID); holder != id {
		t.Fatalf("the restart claim did not lease %s to %s: %q", restart.decision.LaneID, id, holder)
	}
	return f, starting, steps, id, kept
}

// afterTheBuild is a clock later than the build record judgeSignalSteps writes.
func afterTheBuild() time.Time { return time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC) }

// reclaimSteps tells the reaper where each run face writes its streams, as ProcessSteps
// does, so a face of a gone loop that holds nothing open reads as ended.
type reclaimSteps struct {
	*judgeSignalSteps
	root string
}

func (s reclaimSteps) runOutputFiles(id string) []string {
	return streamSteps{root: s.root}.runOutputFiles(id)
}

// reapAtStart runs the reap a starting loop's first pass runs before it plans.
func reapAtStart(t *testing.T, l *Loop) {
	t.Helper()
	if _, err := l.reapDeadAndExpired(context.Background(), l.cfg.Now(), nil); err != nil {
		t.Fatal(err)
	}
}

func terminalFor(t *testing.T, f refusalLoopFixture, id string) *runRecordView {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var out *runRecordView
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rec, err := terminalRecord(r, id)
		if err != nil || rec == nil {
			return err
		}
		out = &runRecordView{State: rec.State, EndCause: rec.EndCause}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

type runRecordView struct{ State, EndCause string }

// On 2026-11-28 an item sat for hours as `running  no-lane`: the dead-loop reaper read the
// restart claim's run id, found the terminal record the earlier reclaim wrote, and kept the
// claim's lease as if that record were the claimed run's end. The planner then held the item
// behind its own lane's lease, naming a judge run that did not exist, across every later
// restart.
func TestARestartClaimOnAReclaimedEvaluatorRunIsReapedAndVerifyContinuesFromTheKeptCommit(t *testing.T) {
	f, stuck, steps, id, kept := abandonedRestartClaim(t)
	next := replaceGraphLoop(t, f, stuck, stuck.cfg.Steps)
	next.cfg.Now = afterTheBuild
	reapAtStart(t, next)
	d := decisionFor(t, next, "item-a").decision
	if d.Action != ActionDispatch || d.RunRequest == nil || d.RunRequest.NodeID != "verify" {
		t.Fatalf("an item with no live run and an abandoned restart claim is not dispatched: %s %s from %s", d.Action, d.Reason, d.From)
	}
	if d.Action == ActionHold && d.From == "running" {
		t.Fatalf("an item with no live run is reported running: %+v", d)
	}
	if holder := laneHolder(t, f, d.LaneID); holder == id {
		t.Fatalf("lane %s is still leased to the finished run %s", d.LaneID, id)
	}
	result := &LoopResult{}
	before := len(steps.runs)
	for i := 0; i < 8 && result.Landed == 0; i++ {
		graphPass(t, next, result)
	}
	if result.Landed != 1 {
		t.Fatalf("the item did not land from its kept build commit: %+v; runs %+v", result, steps.runs[before:])
	}
	for _, req := range steps.runs[before:] {
		if req.NodeID != "verify" {
			t.Fatalf("the restart reran node %s instead of continuing at verify: %+v", req.NodeID, steps.runs[before:])
		}
	}
	if got := runGit(t, f.root, "show", TrunkRef+":tools/item-a/out.txt"); !strings.Contains(got, "-n1") {
		t.Fatalf("the landed tree is not the kept build commit %s: out.txt = %q", kept, got)
	}
}

// The starting loop launches the verify restart it planned, under its fresh run id, and the
// item lands from the kept build commit. Before the fix the restart kept the ended run's id,
// and the start coordinator never launched a claim whose run already has a record.
func TestTheStartingLoopLaunchesTheRestartOfAReclaimedEvaluatorRun(t *testing.T) {
	f, starting, steps, id, _, _ := reclaimedEvaluatorRun(t)
	result := &LoopResult{}
	before := len(steps.runs)
	for i := 0; i < 8 && result.Landed == 0; i++ {
		graphPass(t, starting, result)
	}
	if result.Landed != 1 || len(steps.runs) == before {
		t.Fatalf("the verify restart was not launched: %+v; runs %+v", result, steps.runs[before:])
	}
	if got := steps.runs[before].RunID; got == id {
		t.Fatalf("the restart ran under the ended run's id %s", got)
	}
	if got := runGit(t, f.root, "show", TrunkRef+":tools/item-a/out.txt"); !strings.Contains(got, "-n1") {
		t.Fatalf("the landed tree is not the kept build commit: out.txt = %q", got)
	}
}

// The operator's accepted retry of the cut-off verify run must lead to a dispatch, not to the
// same hold.
func TestAnAcceptedRetryOfAReclaimedEvaluatorRunDispatches(t *testing.T) {
	f, stuck, _, id, _ := abandonedRestartClaim(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "item-a", RunID: id, By: "operator", Why: "inspected the kept commit", Acknowledge: RetryAckUnjudged, At: stuck.cfg.Now()})
		return err
	})
	next := replaceGraphLoop(t, f, stuck, stuck.cfg.Steps)
	next.cfg.Now = afterTheBuild
	reapAtStart(t, next)
	d := decisionFor(t, next, "item-a").decision
	if !isDispatch(d) {
		t.Fatalf("an accepted retry of the cut-off verify run did not dispatch: %s %s from %s", d.Action, d.Reason, d.From)
	}
}
