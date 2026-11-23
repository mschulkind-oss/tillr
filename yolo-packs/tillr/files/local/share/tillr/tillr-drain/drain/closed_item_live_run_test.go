package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestClosingItemMidRunInterruptsOnlyClosedItemRun(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	plans := mustPlan(t, l)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	for _, p := range plans {
		if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
	}
	req := plans[0].decision.RunRequest
	if req == nil || req.ItemID != "item-a" || len(plans) != 2 || plans[1].decision.RunRequest == nil {
		t.Fatalf("expected claims for both items: %+v", plans)
	}
	worker := exec.Command("sleep", "60")
	worker.Env = append(os.Environ(), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+f.storePath)
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- worker.Wait() }()
	t.Cleanup(func() { _ = worker.Process.Kill() })

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(context.Background(), tx, "item-a", "premise was wrong", "operator", l.cfg.Now())
		return err
	})

	result := runOnePass(t, l)
	if result.Dispatched != 0 {
		t.Errorf("dispatched on a claimed lane: %+v", result)
	}
	select {
	case err := <-wait:
		if err == nil {
			t.Error("closed item's worker was not interrupted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closed item's worker survived the pass")
	}

	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var orphanedClosed, orphanedOther int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, loopOrphanedKind, req.RunID).Scan(&orphanedClosed); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, loopOrphanedKind, plans[1].decision.RunRequest.RunID).Scan(&orphanedOther); err != nil {
			return err
		}
		if orphanedClosed != 1 || orphanedOther != 0 {
			t.Errorf("drain.orphaned counts for closed/other: %d/%d", orphanedClosed, orphanedOther)
		}

		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=?`, runEndedItemClosedKind, req.RunID).Scan(&raw); err != nil {
			t.Fatalf("missing %s event for %s: %v", runEndedItemClosedKind, req.RunID, err)
		}
		var payload runEndedItemClosed
		if err := json.Unmarshal(raw, &payload); err != nil {
			return err
		}
		if payload.ItemID != "item-a" || payload.RunID != req.RunID || payload.LaneID != req.LaneID || payload.ItemState != "closed" || payload.Reason != "item-closed" || payload.Detail == "" {
			t.Errorf("unexpected %s payload: %+v", runEndedItemClosedKind, payload)
		}

		var otherClosed int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, runEndedItemClosedKind, plans[1].decision.RunRequest.RunID).Scan(&otherClosed); err != nil {
			return err
		}
		if otherClosed != 0 {
			t.Errorf("unexpected %s event for unrelated item: %d", runEndedItemClosedKind, otherClosed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClosingWorkflowItemMidRunEndsLiveNodeBeforeRelease(t *testing.T) {
	f, l, _ := plannedFixture(t)
	planFor := func(itemID string) plannedApplication {
		t.Helper()
		for _, p := range mustPlan(t, l) {
			if p.decision.ItemID == itemID {
				return p
			}
		}
		t.Fatalf("no plan for %s", itemID)
		return plannedApplication{}
	}
	first := planFor("item-a")
	if first.decision.Workflow == nil || first.decision.RunRequest == nil {
		t.Fatalf("expected workflow dispatch plan: %+v", first)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(context.Background(), first, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim workflow node: %v %v", ok, err)
	}
	req := first.decision.RunRequest

	worker := exec.Command("sleep", "60")
	worker.Env = append(os.Environ(), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+f.storePath)
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- worker.Wait() }()
	t.Cleanup(func() { _ = worker.Process.Kill() })

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(context.Background(), tx, "item-a", "premise was wrong", "operator", l.cfg.Now())
		return err
	})

	// While the workflow node is live, the planner must hold it as item-already-terminal
	// (not run-live and not workflow-invalidated) so the pass ends the live worker.
	held := planFor("item-a").decision
	if held.Action != ActionHold || held.Reason != ReasonItemAlreadyTerminal || held.WorkflowRelease {
		t.Fatalf("planned while workflow node live: %+v, want ActionHold/%s without WorkflowRelease", held, ReasonItemAlreadyTerminal)
	}

	var passRes LoopResult
	if stop, err := l.pass(context.Background(), &passRes); err != nil || (stop != "" && stop != "lanes-leased" && stop != "queue-drained") {
		t.Fatalf("pass while live: stop=%q err=%v", stop, err)
	}
	select {
	case err := <-wait:
		if err == nil {
			t.Error("closed workflow item's worker was not interrupted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closed workflow item's worker survived the pass")
	}

	// Simulate the run face recording its interrupted terminal record and releasing its lease.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq)
			VALUES(?,?,?,?,?,?,?,'[]',(SELECT MAX(seq) FROM events))`,
			req.RunID, "item-a", req.LaneID, req.Backend, run.StatePaused, 1, 2); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state=?, run_id=NULL, leased_until=NULL WHERE lane_id=?`, run.LaneStateIdle, req.LaneID)
		return err
	})

	// Once the worker exited and released its lease, the next pass releases the workflow
	// and marks it examined without landing.
	release := planFor("item-a").decision
	if release.Action != ActionSettle || !release.WorkflowRelease {
		t.Fatalf("planned after workflow node exited: %+v, want ActionSettle with WorkflowRelease", release)
	}
	res := runOnePass(t, l)
	if res.Landed != 0 {
		t.Fatalf("closed workflow item landed: %+v", res)
	}
	if w := pendingForTest(t, f, "item-a"); w == nil || !w.Done {
		t.Fatalf("workflow pending work not marked done: %+v", w)
	}

	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id=?`, req.LaneID).Scan(&state); err != nil {
			return err
		}
		if state != run.LaneStateIdle {
			t.Errorf("lane %s state = %q, want idle", req.LaneID, state)
		}
		var closedCount int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, runEndedItemClosedKind, req.RunID).Scan(&closedCount); err != nil {
			return err
		}
		if closedCount != 1 {
			t.Errorf("%s count = %d, want 1", runEndedItemClosedKind, closedCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
