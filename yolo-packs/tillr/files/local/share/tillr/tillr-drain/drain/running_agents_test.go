package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func setItemExpress(t *testing.T, f refusalLoopFixture, id string, express bool) {
	t.Helper()
	val := 0
	if express {
		val = 1
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET express=? WHERE item_id=?`, val, id)
		return err
	})
}

func planDecisionByItem(t *testing.T, f refusalLoopFixture, tick Pass, policy *Policy) map[string]Decision {
	t.Helper()
	var out map[string]Decision
	fixtureRead(t, f, func(r *store.Reader) error {
		envs, err := Plan(r, tick, policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		out = make(map[string]Decision, len(envs))
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			out[d.ItemID] = d
		}
		return nil
	})
	return out
}

func TestOrdinaryStartBlockedWhenParallelAgentsFillLimitEvenWithFreeLane(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-exp")
	at := l.cfg.Now().UTC()
	setItemExpress(t, f, "item-exp", true)

	// Lease lane-1 to a 2-agent read-only parallel group for item-a.
	// lane-2 is completely free, and the running-agent limit is 2.
	w := &Workflow{
		Name:    "scouts",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "scout1", Kind: "agent", Seat: "scout", Access: "read_only"},
			{ID: "scout2", Kind: "agent", Seat: "scout", Access: "read_only"},
			{ID: "build", Kind: "agent", Seat: "build", Access: "write"},
		},
		Edges: []WorkflowEdge{
			{From: "scout1", To: "build"},
			{From: "scout2", To: "build"},
		},
	}
	req1 := &run.Request{RunID: "run-item-a-a1-n1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", NodeID: "scout1", ReadOnlyGroup: "run-item-a-a1-n1"}
	req2 := &run.Request{RunID: "run-item-a-a1-n2", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", NodeID: "scout2", ReadOnlyGroup: "run-item-a-a1-n1"}
	d1 := Decision{ItemID: "item-a", LaneID: "lane-1", Action: ActionDispatch, Attempt: 1, Workflow: w, NodeIndex: 0, RunRequest: req1,
		Parallel: []Decision{{ItemID: "item-a", LaneID: "lane-1", Action: ActionDispatch, Attempt: 1, Workflow: w, NodeIndex: 1, RunRequest: req2}}}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, *req1, "lane-1", at); err != nil {
			return err
		}
		if _, err := run.ClaimLease(context.Background(), tx, *req2, "lane-1", at); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, req1.RunID, &pendingWork{Decision: d1}, at)
	})

	lanes := []string{"lane-1", "lane-2"}
	got := planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &lanes, Build: &l.build}, l.cfg.Policy)
	if got["item-b"].Action != ActionHold || got["item-b"].Reason != ReasonNoLane {
		t.Fatalf("item-b = %+v, want hold/%s while 2 parallel ordinary agents fill limit 2", got["item-b"], ReasonNoLane)
	}
	if got["item-exp"].Action != ActionDispatch || got["item-exp"].LaneID != "lane-2" {
		t.Fatalf("item-exp = %+v, want express dispatch on free staged lane-2", got["item-exp"])
	}

	// Settle one of item-a's parallel scouts: now only 1 ordinary agent is active (< limit 2).
	// Close item-exp and verify item-b can now start on lane-2.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, req2.RunID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-exp'`)
		return err
	})
	got = planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &lanes, Build: &l.build}, l.cfg.Policy)
	if got["item-b"].Action != ActionDispatch || got["item-b"].LaneID != "lane-2" {
		t.Fatalf("item-b after 1 scout settled = %+v, want dispatch on lane-2", got["item-b"])
	}
}

type serialCountingSteps struct {
	*workflowSteps
	onRun func(context.Context, run.Request) (run.Record, string, error)
}

func (s *serialCountingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	return s.onRun(ctx, req)
}

func TestParallelReadOnlyAgentsRespectRunningAgentLimitOneByOne(t *testing.T) {
	f, l, serial := workflowLoopFixtureWithLanes(t, 1, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id!='item-a'`)
		return err
	})
	raw := `{"name":"scout-then-build","version":1,"max_parallel":4,"stages":[` +
		`{"id":"scout1","kind":"agent","seat":"scout","access":"read_only"},` +
		`{"id":"scout2","kind":"agent","seat":"scout","access":"read_only"},` +
		`{"id":"scout3","kind":"agent","seat":"scout","access":"read_only"},` +
		`{"id":"build","kind":"agent","seat":"build","access":"write"}],` +
		`"edges":[{"from_node":"scout1","to_node":"build"},{"from_node":"scout2","to_node":"build"},{"from_node":"scout3","to_node":"build"}]}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "serial parallel limit fixture")
	serial.firstHead = runGit(t, f.root, "rev-parse", "HEAD")

	var mu sync.Mutex
	inFlight := 0
	maxInFlight := 0
	var runOrder []string
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Steps = &serialCountingSteps{
		workflowSteps: serial,
		onRun: func(ctx context.Context, req run.Request) (run.Record, string, error) {
			if req.LaneID != "lane-1" {
				return run.Record{}, "", os.ErrInvalid
			}
			mu.Lock()
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			runOrder = append(runOrder, req.NodeID)
			mu.Unlock()
			defer func() {
				mu.Lock()
				inFlight--
				mu.Unlock()
			}()
			if req.NodeID == "build" {
				return (&parallelSteps{workflowSteps: serial}).writeRun(ctx, req)
			}
			dir := run.DurableDir(filepath.Join(f.laneRoot, req.LaneID), req.RunID)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return run.Record{}, "", err
			}
			report := "Scout done.\nRESULT: done\n"
			path := filepath.Join(dir, "report.md")
			if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
				return run.Record{}, "", err
			}
			code := 0
			rec := run.Record{
				RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
				State: run.StateExited, ExitCode: &code,
				StartedAt: "2026-11-25T12:00:00Z", FinishedAt: "2026-11-25T12:00:00Z",
				ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}},
			}
			st, err := store.Open(ctx, f.storePath, true)
			if err != nil {
				return rec, report, err
			}
			defer st.Close()
			err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
			return rec, report, err
		},
	}

	result := &LoopResult{}
	for i := 0; i < 8; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if maxInFlight != 1 {
		t.Fatalf("max concurrent agents = %d, want 1 under --lanes 1 (order=%v)", maxInFlight, runOrder)
	}
	if len(runOrder) != 4 || runOrder[3] != "build" {
		t.Fatalf("runOrder = %v, want 3 scouts one by one then build", runOrder)
	}
	if _, err := os.Stat(filepath.Join(f.laneRoot, "lane-2")); !os.IsNotExist(err) {
		t.Fatalf("lane-2 directory must not be created when only lane-1 was staged (err=%v)", err)
	}
}

func TestScaledDownDrainingAgentBlocksOrdinaryStartOnFreeLaneButAllowsExpress(t *testing.T) {
	f, l := passFixture(t, "item-draining", "item-ord", "item-exp")
	at := l.cfg.Now().UTC()
	setItemExpress(t, f, "item-exp", true)

	// lane-2 (outside AvailableLanes = ["lane-1"]) still holds a live ordinary agent.
	req := &run.Request{RunID: "run-item-draining-a1", ItemID: "item-draining", LaneID: "lane-2", Backend: "fixture"}
	d := Decision{ItemID: "item-draining", LaneID: "lane-2", Action: ActionDispatch, Attempt: 1, RunRequest: req}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, *req, "lane-2", at); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, req.RunID, &pendingWork{Decision: d}, at)
	})

	lanes := []string{"lane-1"}
	got := planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &lanes, Build: &l.build}, l.cfg.Policy)
	if got["item-ord"].Action != ActionHold || got["item-ord"].Reason != ReasonNoLane {
		t.Fatalf("item-ord = %+v, want hold/%s while draining lane-2 fills limit 1", got["item-ord"], ReasonNoLane)
	}
	if got["item-exp"].Action != ActionDispatch || got["item-exp"].LaneID != "lane-1" {
		t.Fatalf("item-exp = %+v, want express dispatch on free staged lane-1", got["item-exp"])
	}

	// Release lane-2 and close item-exp: item-ord can now start on lane-1.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE lanes SET state='free', run_id=NULL, leased_until=NULL WHERE lane_id='lane-2'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-exp'`)
		return err
	})
	got = planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &lanes, Build: &l.build}, l.cfg.Policy)
	if got["item-ord"].Action != ActionDispatch || got["item-ord"].LaneID != "lane-1" {
		t.Fatalf("item-ord after draining release = %+v, want dispatch on lane-1", got["item-ord"])
	}
}

func TestCommandNodeDoesNotCountAgainstRunningAgentLimitAndNeverInventsLane(t *testing.T) {
	f, l := passFixture(t, "item-cmd", "item-draining", "item-ord")
	at := l.cfg.Now().UTC()

	cmdWorkflow := &Workflow{
		Name:    "cmd",
		Version: 1,
		Stages:  []WorkflowStage{{ID: "check", Kind: "command", Access: "read_only", Command: []string{"true"}}},
	}
	cmdReq := &run.Request{RunID: "run-item-cmd-a1-n1", ItemID: "item-cmd", LaneID: "lane-1", Backend: "command", NodeID: "check"}
	cmdDec := Decision{ItemID: "item-cmd", LaneID: "lane-1", Action: ActionDispatch, Attempt: 1, Workflow: cmdWorkflow, NodeIndex: 0, RunRequest: cmdReq}

	drainReq := &run.Request{RunID: "run-item-draining-a1", ItemID: "item-draining", LaneID: "lane-3", Backend: "fixture"}
	drainDec := Decision{ItemID: "item-draining", LaneID: "lane-3", Action: ActionDispatch, Attempt: 1, RunRequest: drainReq}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, *cmdReq, "lane-1", at); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, cmdReq.RunID, &pendingWork{Decision: cmdDec}, at); err != nil {
			return err
		}
		if _, err := run.ClaimLease(context.Background(), tx, *drainReq, "lane-3", at); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, drainReq.RunID, &pendingWork{Decision: drainDec}, at)
	})

	// With limit 2 (lane-1, lane-2): lane-1 is a command node (0 agents), lane-3 is 1 ordinary agent.
	// 1 < 2 and lane-2 is free -> item-ord starts on lane-2.
	twoLanes := []string{"lane-1", "lane-2"}
	got := planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &twoLanes, Build: &l.build}, l.cfg.Policy)
	if got["item-ord"].Action != ActionDispatch || got["item-ord"].LaneID != "lane-2" {
		t.Fatalf("item-ord with command on lane-1 and agent on lane-3 = %+v, want dispatch on lane-2", got["item-ord"])
	}

	// Free lane-3 so 0 ordinary agents are running, and offer only lane-1 (occupied by command node).
	// Even though 0 < 1, no staged lane worktree is free -> item-ord must hold (never invent lane-2).
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state='free', run_id=NULL, leased_until=NULL WHERE lane_id='lane-3'`)
		return err
	})
	oneLane := []string{"lane-1"}
	got = planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &oneLane, Build: &l.build}, l.cfg.Policy)
	if got["item-ord"].Action != ActionHold || got["item-ord"].Reason != ReasonNoLane || got["item-ord"].LaneID != "" {
		t.Fatalf("item-ord with no free staged worktree = %+v, want hold/%s with no invented lane", got["item-ord"], ReasonNoLane)
	}
}

func TestContinuingWorkflowKeepsSlotAcrossSettleAndReadyPasses(t *testing.T) {
	// item-a is ahead of item-w in queue order.
	f, l := passFixture(t, "item-a", "item-w", "item-draining")
	at := l.cfg.Now().UTC()
	l.cfg.Policy.Seats = map[string]string{"plan": "fixture", "build": "fixture"}

	w := &Workflow{
		Name:    "two-step",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "plan", Kind: "agent", Seat: "plan", Access: "read_only"},
			{ID: "build", Kind: "agent", Seat: "build", Access: "write"},
		},
		Edges: []WorkflowEdge{{From: "plan", To: "build"}},
	}
	planReq := &run.Request{RunID: "run-item-w-a1-n1", ItemID: "item-w", LaneID: "lane-1", Backend: "fixture", NodeID: "plan"}
	planDec := Decision{ItemID: "item-w", LaneID: "lane-1", Action: ActionDispatch, Attempt: 1, Workflow: w, NodeIndex: 0, Seat: "plan", RunRequest: planReq}

	drainReq := &run.Request{RunID: "run-item-draining-a1", ItemID: "item-draining", LaneID: "lane-3", Backend: "fixture"}
	drainDec := Decision{ItemID: "item-draining", LaneID: "lane-3", Action: ActionDispatch, Attempt: 1, RunRequest: drainReq}

	code := 0
	planRec := run.Record{
		RunID: planReq.RunID, ItemID: "item-w", LaneID: "lane-1", Backend: "fixture",
		State: run.StateExited, ExitCode: &code,
		StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
	}

	// Pass 1 (settle pass): lane-3 has 1 live agent, item-w's plan node just finished
	// (terminal record recorded, lane-1 lease released), work.Ready is still false.
	// Limit is 2 (lane-1, lane-2), lane-2 is free. item-a must NOT steal item-w's slot.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, *drainReq, "lane-3", at); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, drainReq.RunID, &pendingWork{Decision: drainDec}, at); err != nil {
			return err
		}
		if _, err := run.ClaimLease(context.Background(), tx, *planReq, "lane-1", at); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, planReq.RunID, &pendingWork{Decision: planDec}, at); err != nil {
			return err
		}
		return applyTerminal(tx, planRec)
	})

	lanes := []string{"lane-1", "lane-2"}
	got := planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &lanes, Build: &l.build}, l.cfg.Policy)
	if got["item-a"].Action != ActionHold || got["item-a"].Reason != ReasonNoLane {
		t.Fatalf("settle pass: item-a = %+v, want hold/%s so continuing item-w keeps its slot", got["item-a"], ReasonNoLane)
	}
	if got["item-w"].Action != ActionSettle {
		t.Fatalf("settle pass: item-w = %+v, want settle", got["item-w"])
	}

	// Pass 2 (ready pass): nodeAdvancedKind advances item-w to build (work.Ready=true,
	// lane-1 still unleased). item-a must STILL hold and item-w must dispatch build on lane-1.
	buildReq := &run.Request{RunID: "run-item-w-a1-n2", ItemID: "item-w", LaneID: "lane-1", Backend: "fixture", NodeID: "build"}
	buildDec := Decision{ItemID: "item-w", LaneID: "lane-1", Action: ActionDispatch, Attempt: 1, Workflow: w, NodeIndex: 1, Seat: "build", RunRequest: buildReq}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, nodeAdvancedKind, buildReq.RunID, &pendingWork{Decision: buildDec, Ready: true, ParentRunID: planReq.RunID}, at)
	})
	got = planDecisionByItem(t, f, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &lanes, Build: &l.build}, l.cfg.Policy)
	if got["item-a"].Action != ActionHold || got["item-a"].Reason != ReasonNoLane {
		t.Fatalf("ready pass: item-a = %+v, want hold/%s while item-w advances to build", got["item-a"], ReasonNoLane)
	}
	if got["item-w"].Action != ActionDispatch || got["item-w"].LaneID != "lane-1" {
		t.Fatalf("ready pass: item-w = %+v, want dispatch build on lane-1", got["item-w"])
	}
}

func TestStartSnapshotUpdatesActiveOrdinaryAgentsOnLeaseChangeWithNoItemChange(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx, at := context.Background(), l.cfg.Now().UTC()

	lanes := []string{"lane-1"}
	tick := Pass{AvailableLanes: &lanes, StorePath: f.storePath, Build: &l.build}
	questions := &VantageQuestions{Root: f.root}

	var syncer startStoreSync
	t.Cleanup(syncer.close)
	rebuild := func(at time.Time, prev startSnapshot) func(*store.Reader, *startSnapshot, map[string]bool) (func() error, error) {
		return func(r *store.Reader, next *startSnapshot, touched map[string]bool) (func() error, error) {
			tCopy := tick
			tCopy.Now = at.Format(time.RFC3339Nano)
			return next.prepareRepair(ctx, r, at, l.currentPolicy(), tCopy, questions, prev, touched)
		}
	}

	// Initial seed: 0 active ordinary agents, limit 1 -> item-a proposes lane-1.
	snap, err := syncer.syncStore(ctx, f.storePath, at, l.currentPolicy(), startSnapshot{}, rebuild(at, startSnapshot{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.activeOrdinaryAgents) != 0 {
		t.Fatalf("initial activeOrdinaryAgents = %+v, want empty", snap.activeOrdinaryAgents)
	}
	if e, ok := snap.nextFreshStart(); !ok || e.decision.ItemID != "item-a" || e.decision.LaneID != "lane-1" {
		t.Fatalf("initial nextFreshStart = (%+v, %v), want item-a on lane-1", e.decision, ok)
	}

	// Take a timed lease on draining lane-2 with NO item change.
	// Limit is 1 (lane-1), lane-1 is still free, but active ordinary agents becomes 1.
	leaseReq := run.Request{RunID: "run-draining-a1", ItemID: "other", LaneID: "lane-2", Backend: "fixture", TimeoutMS: 5000}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ClaimLease(ctx, tx, leaseReq, "lane-2", at)
		return err
	})

	snap2, err := syncer.syncStore(ctx, f.storePath, at.Add(time.Second), l.currentPolicy(), snap, rebuild(at.Add(time.Second), snap))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap2.activeOrdinaryAgents) != 1 {
		t.Fatalf("after lease taken with no item change: activeOrdinaryAgents = %+v, want 1", snap2.activeOrdinaryAgents)
	}
	if e, ok := snap2.nextFreshStart(); ok {
		t.Fatalf("after lease taken: nextFreshStart = %+v, want none while limit 1 is full", e.decision)
	}

	// Advance clock past lease expiry (timeout 5s + grace) with NO store write and NO item change.
	afterExpiry := at.Add(time.Duration(5000+run.LeaseGraceMS+1000) * time.Millisecond)
	snap3, err := syncer.syncStore(ctx, f.storePath, afterExpiry, l.currentPolicy(), snap2, rebuild(afterExpiry, snap2))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap3.activeOrdinaryAgents) != 0 {
		t.Fatalf("after lease expiry with no item change: activeOrdinaryAgents = %+v, want 0", snap3.activeOrdinaryAgents)
	}
	if e, ok := snap3.nextFreshStart(); !ok || e.decision.ItemID != "item-a" || e.decision.LaneID != "lane-1" {
		t.Fatalf("after lease expiry: nextFreshStart = (%+v, %v), want item-a on lane-1", e.decision, ok)
	}
}
