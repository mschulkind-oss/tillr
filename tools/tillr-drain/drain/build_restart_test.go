package drain

import (
	"context"
	"encoding/json"
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
	writeForkFile(t, filepath.Join(filepath.Dir(adapter), "pause-kind"), run.PauseAtGap+"\n")
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
	declarePauseKind(t, h.l, run.PauseAtGap)
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

// A run with no dispatch fact has an unknown build, not an older one: the loop requests no
// migration pause for it, and it finishes where it started. A run whose fact names another
// build is paused; one that names this build is not.
func TestBuildMigrationLeavesARunWithNoDispatchFactAndPausesOnlyAnOlderBuild(t *testing.T) {
	for _, tc := range []struct {
		name         string
		legacy, next bool
		want         int
	}{{"current", false, false, 0}, {"older", false, true, 1}, {"no-fact", true, true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRestartHarness(t, nil, nil)
			declarePauseKind(t, h.l, run.PauseAtGap)
			p := mustPlan(t, h.l)[0]
			if tc.legacy {
				p.decision.Inputs, p.decision.DispatchBuild = nil, ""
			}
			if ok, _, err := h.l.applyPlan(context.Background(), p, runGit(t, h.f.root, "rev-parse", TrunkRef), h.l.cfg.Now()); err != nil || !ok {
				t.Fatalf("claim=%v/%v", ok, err)
			}
			if tc.next {
				h.l.build.Revision = "next-build"
			}
			if err := h.l.migrateBuildRuns(context.Background(), &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			if got := pauseRequests(t, h.f); got != tc.want {
				t.Errorf("pauses=%d, want %d", got, tc.want)
			}
		})
	}
}

// A design-seat dispatch (conflictDecision) carries no RunInputs. Its dispatch fact still
// names the build that started it, so a pass on that build does not pause it as older. On
// 2026-11-18 every conflict-design run was paused in its first second and SIGKILLed 120 s
// later (run.pause-request events 1288425, 1288450 and 1288597).
func TestADesignDispatchRecordsItsBuildAndIsNotPausedAsOlder(t *testing.T) {
	f, l := designQuotaFixture(t)
	l.cfg.Restart = &RestartConfig{}
	declarePauseKind(t, l, run.PauseAtGap)
	p := mustPlan(t, l)[0]
	if p.decision.Action != ActionDispatch || p.decision.Reason != ReasonConflictDesign || p.decision.Inputs != nil {
		t.Fatalf("fixture is not an inputs-free design dispatch: %+v", p.decision)
	}
	if ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim=%v/%v", ok, err)
	}
	if err := l.migrateBuildRuns(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if got := pauseRequests(t, f); got != 0 {
		t.Fatalf("a design run on the current build was paused %d time(s)", got)
	}
	hash, _ := l.build.Hash()
	fixtureRead(t, f, func(r *store.Reader) error {
		facts, err := readInputDecisions(r)
		if err != nil {
			return err
		}
		fact := facts[p.decision.RunRequest.RunID]
		if fact.Inputs == nil || fact.Inputs.TillrBuild != hash || fact.BlockConflict == nil {
			t.Errorf("design dispatch fact %+v, want build %s and its conflict", fact, hash)
		}
		return nil
	})
}

// A migration pause is requested only of an adapter that declares it stops at a gap, and
// with no straggler bound, so nothing kills the worker: one that cannot stop at a gap, or
// declares nothing, finishes on the build it started under.
func TestAMigrationPauseIsOnlyForAGapAdapterAndHasNoKillBound(t *testing.T) {
	for _, kind := range []string{run.PauseAtGap, run.PauseWhereItStands, run.PauseCannotResume, ""} {
		t.Run(kind, func(t *testing.T) {
			h := newRestartHarness(t, nil, nil)
			declarePauseKind(t, h.l, kind)
			p := mustPlan(t, h.l)[0]
			if ok, _, err := h.l.applyPlan(context.Background(), p, runGit(t, h.f.root, "rev-parse", TrunkRef), h.l.cfg.Now()); err != nil || !ok {
				t.Fatalf("claim=%v/%v", ok, err)
			}
			h.l.build.Revision = "next-build"
			if err := h.l.migrateBuildRuns(context.Background(), &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == run.PauseAtGap {
				want = 1
			}
			if got := pauseRequests(t, h.f); got != want {
				t.Fatalf("pauses=%d, want %d", got, want)
			}
			if want == 0 {
				return
			}
			fixtureRead(t, h.f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=?`, run.PauseRequestKind).Scan(&raw); err != nil {
					return err
				}
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					return err
				}
				if _, bounded := body["timeout_ms"]; bounded || body["tillr_build"] == nil {
					t.Errorf("migration pause %s, want a build and no timeout_ms", raw)
				}
				return nil
			})
		})
	}
}

// declarePauseKind points the fixture backend at an adapter whose directory declares kind,
// as each real adapter's pause-kind file does; "" declares nothing.
func declarePauseKind(t *testing.T, l *Loop, kind string) {
	t.Helper()
	dir := scratchUnderDurable(t)
	if kind != "" {
		writeForkFile(t, filepath.Join(dir, "pause-kind"), kind+"\n")
	}
	b := l.cfg.Policy.Backends["fixture"]
	b.Adapter = filepath.Join(dir, "fixture")
	l.cfg.Policy.Backends["fixture"] = b
}

func pauseRequests(t *testing.T, f refusalLoopFixture) int {
	t.Helper()
	var n int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, run.PauseRequestKind).Scan(&n)
	})
	return n
}

func TestNewBuildRequestsOnePauseWithoutChangingDispatchInputs(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	declarePauseKind(t, h.l, run.PauseAtGap)
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
