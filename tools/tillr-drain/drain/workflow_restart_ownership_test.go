package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkflowRestartPreservesARecordedFinish(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	graphPass(t, l, result)
	work := pendingForTest(t, f, "item-a")
	head := runGit(t, l.laneTarget("lane-1"), "rev-parse", "HEAD")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, loopOrphanedKind, work.Decision.RunRequest.RunID, struct{}{}, l.cfg.Now())
	})
	next := replaceGraphLoop(t, f, l, steps)
	graphPass(t, next, result)
	graphPass(t, next, result)
	if len(steps.runs) != 2 || steps.runs[1].RunID != "run-item-a-a1-n2" || steps.heads[1] != head {
		t.Fatalf("finished node rerun or commit lost: %+v %v", steps.runs, steps.heads)
	}
}

func TestWorkflowRestartRecoversSignalRecordWithoutStopMarker(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	work := pendingForTest(t, f, "item-a")
	if _, err := l.stageDispatches(context.Background(), []Decision{work.Decision}, work.BaseSHA, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: p.decision.RunRequest.RunID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(-1), EndCause: run.EndSignal, StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{}})
	})
	next := replaceGraphLoop(t, f, l, steps)
	graphPass(t, next, &LoopResult{})
	if len(steps.runs) != 1 || steps.runs[0].RunID != "run-item-a-a1-n1-restart" {
		t.Fatalf("interrupted terminal record not replayed: %+v", steps.runs)
	}
}

func TestWorkflowRestartTwiceBeforeRestagingKeepsOwnership(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	w := pendingForTest(t, f, "item-a")
	if _, err := l.stageDispatches(context.Background(), []Decision{w.Decision}, w.BaseSHA, result); err != nil {
		t.Fatal(err)
	}
	target := l.laneTarget("lane-1")
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(target, "tools/item-a/unfinished.txt"), "discard\n")
	runGit(t, target, "add", ".")
	runGit(t, target, "commit", "-qm", "unfinished")
	next := replaceGraphLoop(t, f, l, steps)
	p = mustPlan(t, next)[0]
	deadOwnerClaim(t, f, next, p)
	again := replaceGraphLoop(t, f, next, steps)
	graphPass(t, again, result)
	if len(steps.runs) != 1 || steps.runs[0].RunID != "run-item-a-a1-n1-002-restart" || strings.Contains(runGit(t, target, "ls-files"), "unfinished.txt") {
		t.Fatalf("staging ownership lost across repeated restart: %+v %+v", steps.runs, result)
	}
}

func TestWorkflowCloseBeforeStagingReleasesWithoutTouchingLane(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "unowned"}[exists], func(t *testing.T) {
			f, l, steps := restartGraphFixture(t)
			target := l.lanePath("lane-1")
			head := ""
			if exists {
				base := runGit(t, f.root, "rev-parse", "HEAD")
				if err := l.cfg.Stage.(*LaneStager).Stage(context.Background(), StageSpec{LaneID: "lane-1", ItemID: "item-a", RunID: "unowned", Backend: "fixture", WorkingDir: WorktreeDir, BaseSHA: base}); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(target, "tools/item-a/unowned.txt"), "preserve\n")
				runGit(t, target, "add", ".")
				runGit(t, target, "commit", "-qm", "unowned")
				head = runGit(t, target, "rev-parse", "HEAD")
			}
			deadOwnerClaim(t, f, l, mustPlan(t, l)[0])
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`)
				return err
			})
			next := replaceGraphLoop(t, f, l, steps)
			graphPass(t, next, &LoopResult{})
			if !pendingForTest(t, f, "item-a").Done || len(steps.runs) != 0 {
				t.Fatal("unstaged closed item not released")
			}
			if exists && runGit(t, target, "rev-parse", "HEAD") != head {
				t.Fatal("unowned commit reset")
			}
		})
	}
}

func TestWorkflowReleaseSurvivesExaminationCrash(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		graphPass(t, l, result)
	}
	head := runGit(t, l.laneTarget("lane-1"), "rev-parse", "HEAD")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`)
		return err
	})
	p := mustPlan(t, l)[0]
	ok, w, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", "HEAD"), l.cfg.Now())
	if err != nil || !ok || w.Owner == nil {
		t.Fatalf("release claim has no recoverable owner: %v %v %+v", ok, err, w)
	}
	owner := *w.Owner
	owner.PID = 2147483647
	w.Owner = &owner
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, p.decision.RunRequest.RunID, w, l.cfg.Now())
	})
	next := replaceGraphLoop(t, f, l, steps)
	var trace strings.Builder
	next.cfg.Report = &trace
	graphPass(t, next, result)
	if !pendingForTest(t, f, "item-a").Done || len(steps.runs) != 2 {
		t.Fatal("release claim not recoverable")
	}
	ref := "refs/tillr/runs/" + p.decision.RunRequest.RunID
	if refExists(t, f, ref) || !strings.Contains(trace.String(), "removed "+ref+" ("+head+")") {
		t.Fatalf("original head was not retained through release then retired: %s", trace.String())
	}
}

func TestWorkflowReleaseRestartAfterResetKeepsOriginalHead(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		graphPass(t, l, result)
	}
	head := runGit(t, l.laneTarget("lane-1"), "rev-parse", "HEAD")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`)
		return err
	})
	p := mustPlan(t, l)[0]
	ok, w, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", "HEAD"), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim %v %v", ok, err)
	}
	if err := l.pin(context.Background(), p.decision.RunRequest.RunID, head); err != nil {
		t.Fatal(err)
	}
	if err := l.resetLane(context.Background(), "lane-1"); err != nil {
		t.Fatal(err)
	}
	owner := *w.Owner
	owner.PID = 2147483647
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, examinationClaimedKind, p.decision.RunRequest.RunID, &owner, l.cfg.Now())
	})
	next := replaceGraphLoop(t, f, l, steps)
	var trace strings.Builder
	next.cfg.Report = &trace
	graphPass(t, next, result)
	if !pendingForTest(t, f, "item-a").Done || len(steps.runs) != 2 {
		t.Fatal("release not completed")
	}
	ref := "refs/tillr/runs/" + p.decision.RunRequest.RunID
	if refExists(t, f, ref) || !strings.Contains(trace.String(), "removed "+ref+" ("+head+")") {
		t.Fatalf("release replay failed to retain then retire original head %s: %s", head, trace.String())
	}
}

func TestWorkflowRestartReleasesCompletedExaminationLease(t *testing.T) {
	for _, done := range []bool{false, true} {
		t.Run(map[bool]string{false: "between-nodes", true: "examined"}[done], func(t *testing.T) {
			f, l, steps := restartGraphFixture(t)
			result := &LoopResult{}
			for i := 0; i < 4; i++ {
				graphPass(t, l, result)
			}
			w := pendingForTest(t, f, "item-a")
			owner, err := currentLoopOwner()
			if err != nil {
				t.Fatal(err)
			}
			owner.PID = 2147483647
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE lanes SET state='leased',run_id=?,leased_until=NULL WHERE lane_id='lane-1'`, w.ParentRunID); err != nil {
					return err
				}
				if err := appendEffect(tx, examinationClaimedKind, w.Decision.RunRequest.RunID, owner, l.cfg.Now()); err != nil {
					return err
				}
				if done {
					return appendEffect(tx, examinationDoneKind, w.Decision.RunRequest.RunID, struct{}{}, l.cfg.Now())
				}
				return nil
			})
			next := replaceGraphLoop(t, f, l, steps)
			plans := mustPlan(t, next)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var state string
				if err := tx.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-1'`).Scan(&state); err != nil {
					return err
				}
				if state != "idle" {
					t.Fatalf("dead completed examination retained lease: %s %+v", state, plans)
				}
				return nil
			})
		})
	}
}

func TestWorkflowRecoveryBookkeepingDoesNotChangeOrdinaryExaminations(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", "HEAD")
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch claim %v %v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: p.decision.RunRequest.RunID, ItemID: "item-a", LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{}})
	})
	exam := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(context.Background(), exam, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("exam claim %v %v", ok, err)
	}
	if err := l.releaseExamination(context.Background(), exam.decision); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind IN (?,?)`, examinationClaimedKind, examinationReleasedKind).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("workflow recovery events added to an ordinary item: %d", n)
		}
		return nil
	})
}

func TestWorkflowContinuationRefusesUnrecordedHead(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		graphPass(t, l, result)
	}
	target := l.laneTarget("lane-1")
	writeForkFile(t, filepath.Join(target, "tools/item-a/unowned.txt"), "must preserve\n")
	runGit(t, target, "add", ".")
	runGit(t, target, "commit", "-qm", "unowned")
	head := runGit(t, target, "rev-parse", "HEAD")
	next := replaceGraphLoop(t, f, l, steps)
	graphPass(t, next, result)
	if len(steps.runs) != 2 || result.InfraFailures != 1 || runGit(t, target, "rev-parse", "HEAD") != head {
		t.Fatalf("unrecorded commit adopted or discarded: %+v %+v", steps.runs, result)
	}
}

func TestWorkflowRestartBeforeStagingDoesNotClaimUnownedCommits(t *testing.T) {
	for _, unowned := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-lane", true: "unowned-commit"}[unowned], func(t *testing.T) {
			f, l, steps := restartGraphFixture(t)
			result := &LoopResult{}
			base := runGit(t, f.root, "rev-parse", "HEAD")
			target := l.lanePath("lane-1")
			if unowned {
				stager := l.cfg.Stage.(*LaneStager)
				if err := stager.Stage(context.Background(), StageSpec{LaneID: "lane-1", ItemID: "item-a", RunID: "unowned", Backend: "fixture", WorkingDir: WorktreeDir, BaseSHA: base}); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(target, "tools/item-a/unowned.txt"), "must preserve\n")
				runGit(t, target, "add", ".")
				runGit(t, target, "commit", "-qm", "unowned")
			}
			p := mustPlan(t, l)[0]
			deadOwnerClaim(t, f, l, p)
			next := replaceGraphLoop(t, f, l, steps)
			graphPass(t, next, result)
			if unowned {
				// The lane's own unlanded commit takes it out of rotation; it is not an
				// infrastructure failure (lane_rotation.go).
				if len(steps.runs) != 0 || result.InfraFailures != 0 || next.laneConditions["lane-1"] == nil || !next.laneConditions["lane-1"].out {
					t.Fatalf("unowned lane was reused or not taken out of rotation: %+v %+v", steps.runs, result)
				}
				if !strings.Contains(runGit(t, target, "ls-files"), "unowned.txt") {
					t.Fatal("unowned commit discarded")
				}
				err := l.cfg.Stage.(*LaneStager).Stage(context.Background(), StageSpec{LaneID: "lane-1", ItemID: "item-a", RunID: "other", Backend: "fixture", WorkingDir: WorktreeDir, BaseSHA: base})
				if err == nil || !strings.Contains(err.Error(), "change nobody landed") {
					t.Fatalf("ordinary refusal changed: %v", err)
				}
			} else if len(steps.runs) != 1 || steps.runs[0].RunID != "run-item-a-a1-n1-restart" {
				t.Fatalf("unstaged node not replayed: %+v", steps.runs)
			}
		})
	}
}
