package drain

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"

	"github.com/mschulkind-oss/tillr/store"
)

func TestParallelReadersDoNotFollowFailureEdgesForBlockedOrMissingReports(t *testing.T) {
	for _, report := range []string{"RESULT: blocked\n", "no report footer\n"} {
		t.Run(report, func(t *testing.T) {
			f, l, steps := parallelFixture(t)
			steps.badReportNode = "-n2"
			steps.badReport = report
			changeParallelWorkflow(t, f, func(w *Workflow) {
				w.Edges[1].WhenOutcome = "fail"
				w.Exit = &WorkflowExit{RequiresNodes: []string{"build"}}
			})
			result := &LoopResult{}
			for i := 0; i < 2; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			want := 4
			if report == "no report footer\n" {
				want = 5
			}
			if steps.ended != want || steps.builders != 0 {
				t.Fatalf("ended=%d builders=%d", steps.ended, steps.builders)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var outcome string
				err := r.QueryRow(`SELECT outcome FROM node_runs WHERE node_id='scout2'`).Scan(&outcome)
				if outcome == "handled-fail" || (report != "no report footer\n" && outcome == "passed") {
					t.Errorf("invalid failure classification: %s", outcome)
				}
				return err
			})
		})
	}
}

func TestParallelReadersMissingOutputRefusesInsteadOfFollowingFailEdge(t *testing.T) {
	f, l, steps := parallelFixture(t)
	changeParallelWorkflow(t, f, func(w *Workflow) {
		w.Stages[1].Outputs = map[string]string{"findings": "markdown"}
		w.Edges[1].WhenOutcome = "fail"
		w.Exit = &WorkflowExit{RequiresNodes: []string{"build"}}
	})
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.ended != 4 || steps.builders != 0 || result.RefusedSteps[RefusalNodeOutput] != 1 {
		t.Fatalf("ended=%d builders=%d result=%+v", steps.ended, steps.builders, result)
	}
}

func TestParallelReadersHoldThenRestartOnlyFailedHarness(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.failedStart = true
	steps.writerAfter = 5
	// Successful sibling endings also reopen a backend. Record them before the
	// failed reader so the fixture stays closed until the explicit probe below.
	siblingsEnded := make(chan struct{}, 3)
	steps.beforeTerminal = func(req run.Request) error {
		if req.RunID == "run-item-a-a1-n2" {
			for i := 0; i < 3; i++ {
				select {
				case <-siblingsEnded:
				case <-time.After(hangGuard):
					return context.DeadlineExceeded
				}
			}
		}
		return nil
	}
	steps.afterTerminal = func(req run.Request) error {
		switch req.RunID {
		case "run-item-a-a1-n1", "run-item-a-a1-n3", "run-item-a-a1-n4":
			siblingsEnded <- struct{}{}
		}
		return nil
	}
	l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "alternate"}
	l.cfg.Policy.SeatLists = map[string][]string{"scout": {"fixture", "alternate"}, "build": {"fixture", "alternate"}}
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	if steps.started != 4 || steps.ended != 4 || steps.builders != 0 {
		t.Fatalf("closed backend reran readers: started=%d ended=%d builders=%d", steps.started, steps.ended, steps.builders)
	}
	if held := planFor(t, l, "item-a"); held.Action != ActionHold || held.Reason != ReasonBackendClosed || held.DeferredBy != "fixture" {
		t.Fatalf("failed reader did not hold on its original backend: %+v", held)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		closed, err := readInfrastructureClosed(r)
		if !closed["fixture"] || closed["alternate"] {
			t.Errorf("expected fixture closed with alternate open: %v", closed)
		}
		return err
	})
	if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.started != 5 || steps.builders != 1 {
		t.Fatalf("started=%d builders=%d", steps.started, steps.builders)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var id, backend string
		err := r.QueryRow(`SELECT n.run_id,r.backend FROM node_runs n JOIN runs r ON r.run_id=n.run_id WHERE n.node_id='scout2'`).Scan(&id, &backend)
		if id != "run-item-a-a1-n2-restart" || backend != "fixture" {
			t.Errorf("wrong retry: %s on %s", id, backend)
		}
		if err != nil {
			return err
		}
		var siblings int
		if err := r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE node_id IN ('scout1','scout3','scout4') AND outcome='passed' AND run_id IN ('run-item-a-a1-n1','run-item-a-a1-n3','run-item-a-a1-n4')`).Scan(&siblings); err != nil {
			return err
		}
		if siblings != 3 {
			t.Errorf("kept %d finished siblings, want 3", siblings)
		}
		var duplicates int
		err = r.QueryRow(`SELECT COUNT(*) FROM (SELECT run_id FROM drain_pass_spend GROUP BY run_id HAVING COUNT(*)>1)`).Scan(&duplicates)
		if duplicates != 0 {
			t.Errorf("%d reader runs charged in more than one pass", duplicates)
		}
		return err
	})
}

func TestParallelReadersRecoverClaimBeforeInitialStaging(t *testing.T) {
	f, l, steps := parallelFixture(t)
	runGit(t, f.root, "worktree", "remove", "--force", l.lanePath("lane-1"))
	deadOwnerClaim(t, f, l, mustPlan(t, l)[0])
	if _, err := os.Stat(l.lanePath("lane-1")); !os.IsNotExist(err) {
		t.Fatalf("unexpected staged target: %v", err)
	}
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if steps.started != 4 {
		t.Fatalf("unstaged group not recovered: %d started", steps.started)
	}
}

func TestParallelReadersStoppedBeforeStartupCannotLaunchSibling(t *testing.T) {
	f, l, _ := parallelFixture(t)
	p := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", "HEAD"), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err = EndRuns(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	steps := &ProcessSteps{Store: f.storePath}
	started := false
	err = steps.startRun("run-item-a-a1-n2", func() error { started = true; return nil })
	if err == nil || started {
		t.Fatalf("stopped sibling started: %v %v", started, err)
	}
}

func TestParallelReadersRefuseDirtyInterruptedGroupBeforeRestart(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.interrupt = true
	steps.dirty = true
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, loopOrphanedKind, "run-item-a-a1-n1", struct{}{}, l.cfg.Now())
	})
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if steps.started != 4 || result.Rejected != 1 {
		t.Fatalf("dirty reader group reran: %d started %+v", steps.started, result)
	}
}
