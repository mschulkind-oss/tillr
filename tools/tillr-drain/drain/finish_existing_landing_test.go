package drain

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

func finishWithExistingLanding(t *testing.T, f refusalLoopFixture, commit string) {
	t.Helper()
	at := time.Now().UTC()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		const id = "item-a"
		if err := appendEffect(tx, land.LandingKind, id, map[string]string{"item_id": id, "result": "landed", "trunk_after": commit}, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq)
			VALUES('original-landing',?,'original-run','original-verdict','landed','','','',?,(SELECT MAX(seq) FROM events))`, id, commit); err != nil {
			return err
		}
		_, err := item.SetStateWithReceipt(context.Background(), tx, id, StateLanded, at, item.MoveReceipt{
			FinishExistingLanding: true, By: "operator", Reason: "the rest is covered", ResolvesVia: "commit:abc123",
		})
		return err
	})
}

func TestExistingLandingFinishSettlesUnfinishedRunsAndWhyShowsReceipt(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	finishWithExistingLanding(t, p.f, p.base)
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq)
			VALUES('stale-paused','item-a','lane-1','fixture','paused',1,2,'[]',0)`)
		return err
	})
	steps := &terminalSteps{}
	l := p.replacement(t, steps)
	before := snapshotStore(t, p.f.storePath)
	result := runOnePass(t, l)
	if result.Dispatched != 0 || steps.gates != 0 || steps.lands != 0 || steps.verdicts != 0 || steps.states != 0 {
		t.Fatalf("dispatched or judged finished item: %+v / %+v", result, steps)
	}
	assertSettledUnjudged(t, p, l, result, before, StateLanded)

	s := openFixture(t, p.f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM runs WHERE run_id='stale-paused'`).Scan(&state); err != nil {
			return err
		}
		if !isTerminalRunState(state) {
			t.Errorf("unfinished run still %s", state)
		}
		tick := Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}
		answer, err := WhyForItem(r, tick, l.cfg.Policy, nil, "item-a")
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "item-a" {
				want := item.LandingReference{LandingID: "original-landing", RunID: "original-run", TrunkAfter: p.base}
				if row.Class != "done" || row.Mover != "no further work: landed" || row.State != StateLanded || row.Landing == nil || *row.Landing != want {
					t.Errorf("why: %+v, want done with receipt %+v", row, want)
				}
				return nil
			}
		}
		t.Error("named why omitted the landed item")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExistingLandingFinishInterruptsOnlyItsPendingRun(t *testing.T) {
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
	// A test-owned process with the same markers as a worker whose face died.
	worker := exec.Command("sleep", "60")
	worker.Env = append(os.Environ(), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+f.storePath)
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- worker.Wait() }()
	t.Cleanup(func() { _ = worker.Process.Kill() })
	finishWithExistingLanding(t, f, base)
	result := runOnePass(t, l)
	if result.Dispatched != 0 {
		t.Errorf("dispatched on a claimed lane: %+v", result)
	}
	select {
	case err := <-wait:
		if err == nil {
			t.Error("unfinished worker was not interrupted")
		}
	case <-time.After(hangGuard):
		t.Fatal("unfinished worker survived the finish pass")
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var ended, other int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, loopOrphanedKind, req.RunID).Scan(&ended); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, loopOrphanedKind, plans[1].decision.RunRequest.RunID).Scan(&other); err != nil {
			return err
		}
		if ended != 1 || other != 0 {
			t.Errorf("interruption counts for finished/other: %d/%d", ended, other)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
