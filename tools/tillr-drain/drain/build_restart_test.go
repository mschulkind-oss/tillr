package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestNewBuildPausesThreeWorkersAndDispatchesBeforeTheirLastPause(t *testing.T) {
	h := newRestartHarness(t, nil, nil, "item-a", "item-b", "item-c", "item-d")
	l := h.l
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4"}
	steps := &continuationSteps{t: t, store: h.f.storePath, laneRoot: h.f.laneRoot, at: l.cfg.Now()}
	l.cfg.Steps = steps
	adapter := filepath.Join(t.TempDir(), "exec")
	writeForkFile(t, adapter, "#!/bin/sh\nfile=\"$TILLR_ITEM_ID.txt\"; echo candidate > \"$file\"; git add \"$file\"; git commit -qm candidate\necho 'RESULT: done'\n")
	if err := os.Chmod(adapter, 0755); err != nil {
		t.Fatal(err)
	}
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: adapter}
	fixtureWrite(t, h.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["' || item_id || '.txt"]'`)
		return err
	})
	base := runGit(t, h.f.root, "rev-parse", TrunkRef)
	for i := 1; i <= 4; i++ {
		dir := l.laneDirPath(fmt.Sprintf("lane-%d", i))
		for name, body := range map[string]string{BriefFile: "finish the work\n", FenceFile: fmt.Sprintf("item-%c.txt\n", 'a'+i-1), BaseSHAFile: base + "\n", SessionIDFile: "33b5f2d2-0cd4-4a49-b293-c58f5635561f\n"} {
			writeForkFile(t, filepath.Join(dir, name), body)
		}
	}
	plans := mustPlan(t, l)
	for _, p := range plans[:3] {
		if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
	}
	l.build.Revision = "new-loop"
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	fixtureRead(t, h.f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, run.PauseRequestKind).Scan(&n); err != nil {
			return err
		}
		if n != 3 {
			t.Errorf("pauses=%d", n)
		}
		if err := r.QueryRow(`SELECT count(*) FROM runs WHERE item_id='item-d'`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Error("free lane did not dispatch before the last pause")
		}
		return nil
	})
	for _, p := range plans[:3] {
		req := p.decision.RunRequest
		fixtureWrite(t, h.f, func(tx *store.Tx) error {
			return applyTerminal(tx, run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StatePaused, EndCause: run.EndRestart, StartedAt: steps.at.Format(time.RFC3339), FinishedAt: steps.at.Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
		})
		settleContinuation(t, l, result)
	}
	for i := 0; i < 3; i++ {
		settleContinuation(t, l, result)
	}
	fixtureRead(t, h.f, func(r *store.Reader) error {
		var n int
		for _, p := range plans[:3] {
			if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, run.ResumeKind, p.decision.RunRequest.RunID).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				t.Errorf("%s resumes=%d", p.decision.ItemID, n)
			}
			if err := r.QueryRow(`SELECT count(*) FROM runs WHERE item_id=?`, p.decision.ItemID).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				t.Errorf("%s attempts=%d", p.decision.ItemID, n)
			}
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, run.PauseRequestKind).Scan(&n); err != nil {
			return err
		}
		if n != 3 {
			t.Errorf("repeated pauses=%d", n)
		}
		return nil
	})
}

func TestOlderWorkflowEvaluatorReceivesTheSameAttemptPause(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	p := mustPlan(t, h.l)[0]
	ok, w, err := h.l.applyPlan(context.Background(), p, runGit(t, h.f.root, "rev-parse", TrunkRef), h.l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim=%v/%v", ok, err)
	}
	w.Decision.Workflow = &Workflow{Stages: []WorkflowStage{{ID: "evaluate", Kind: "agent", Access: "read_only"}}, Exit: &WorkflowExit{EvaluatorSlot: "evaluate"}}
	fixtureWrite(t, h.f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, w.Decision.RunRequest.RunID, w, h.l.cfg.Now())
	})
	h.l.build.Revision = "new-build"
	if err := h.l.migrateBuildRuns(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, h.f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, run.PauseRequestKind, w.Decision.RunRequest.RunID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("older workflow evaluator pauses=%d, want one", n)
		}
		return nil
	})
}

func TestBuildMigrationTreatsMissingFactsAsOlderAndKeepsCurrentRuns(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			h := newRestartHarness(t, nil, nil)
			p := mustPlan(t, h.l)[0]
			if legacy {
				p.decision.Inputs = nil
			}
			if ok, _, err := h.l.applyPlan(context.Background(), p, runGit(t, h.f.root, "rev-parse", TrunkRef), h.l.cfg.Now()); err != nil || !ok {
				t.Fatalf("claim=%v/%v", ok, err)
			}
			if err := h.l.migrateBuildRuns(context.Background(), &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			fixtureRead(t, h.f, func(r *store.Reader) error {
				var n int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, run.PauseRequestKind).Scan(&n); err != nil {
					return err
				}
				want := 0
				if legacy {
					want = 1
				}
				if n != want {
					t.Errorf("legacy=%v pauses=%d, want %d", legacy, n, want)
				}
				return nil
			})
		})
	}
}

func TestNewBuildRequestsOnePauseWithoutChangingDispatchInputs(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	p := mustPlan(t, h.l)[0]
	ok, _, err := h.l.applyPlan(context.Background(), p, runGit(t, h.f.root, "rev-parse", TrunkRef), h.l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	old := h.l.build
	h.l.build.Revision = "replacement"
	for i := 0; i < 2; i++ {
		if err := h.l.migrateBuildRuns(context.Background(), &LoopResult{}); err != nil {
			t.Fatal(err)
		}
	}
	fixtureRead(t, h.f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, run.PauseRequestKind, p.decision.RunRequest.RunID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("pause requests=%d, want one", n)
		}
		facts, err := readInputDecisions(r)
		if err != nil {
			return err
		}
		hash, _ := old.Hash()
		if facts[p.decision.RunRequest.RunID].Inputs.TillrBuild != hash {
			t.Error("pause changed the starting build before resume")
		}
		return nil
	})
	if err := h.l.recordResumedBuild(context.Background(), *p.decision.RunRequest); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, h.f, func(r *store.Reader) error {
		facts, err := readInputDecisions(r)
		if err != nil {
			return err
		}
		got := *facts[p.decision.RunRequest.RunID].Inputs
		want := *p.decision.Inputs
		want.TillrBuild, _ = h.l.build.Hash()
		if got != want {
			t.Errorf("resume inputs=%+v, want %+v", got, want)
		}
		return nil
	})
}
