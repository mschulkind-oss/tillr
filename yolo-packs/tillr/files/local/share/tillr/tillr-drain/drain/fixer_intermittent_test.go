package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

type fixerLandSteps struct {
	*recordingSteps
	root      string
	storePath string
	t         *testing.T
	landedSHA string
}

func (*fixerLandSteps) fixerCheck(context.Context, Candidate, string) (string, string) {
	return "pass", ""
}

func (s *fixerLandSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if c.HeadSHA != "" {
		s.landedSHA = c.HeadSHA
		runGit(s.t, s.root, "update-ref", "refs/heads/"+TrunkRef, c.HeadSHA)
	}
	return LandOutcome{Result: LandLanded}, nil
}

func (s *fixerLandSteps) Verdict(ctx context.Context, c Candidate, _ string) error {
	st, err := store.Open(ctx, s.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET state='done' WHERE item_id=?`, c.ItemID)
		return err
	})
}

func (s *fixerLandSteps) ItemState(ctx context.Context, itemID, _, state string, wait ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		if state == StateLanded {
			_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, state, itemID)
			return err
		}
		_, err := item.SetState(ctx, tx, itemID, state, time.Now().UTC(), wait...)
		return err
	})
}

func runPassAndDrain(t *testing.T, ctx context.Context, l *Loop) {
	t.Helper()
	var res LoopResult
	if _, err := l.pass(ctx, &res); err != nil {
		t.Fatal(err)
	}
	for len(l.active) > 0 {
		if _, err := l.takeCompletion(<-l.completions, &res); err != nil {
			t.Fatal(err)
		}
	}
	l.examinations.Wait()
}

func claimFixerLease(t *testing.T, f refusalLoopFixture, runID, fixerID, laneID, backend string, at time.Time) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES (?, 'idle')`, laneID); err != nil {
			return err
		}
		_, err := run.ClaimLease(context.Background(), tx, run.Request{
			RunID:   runID,
			ItemID:  fixerID,
			Backend: backend,
			Seat:    "build",
		}, laneID, at)
		return err
	})
}

func recordFixerRunExit(t *testing.T, f refusalLoopFixture, runID, fixerID, laneID, backend string, started, finished time.Time, exitCode int, endCause, route string, tokens int64, cost float64) {
	t.Helper()
	rec := run.Record{
		RunID:      runID,
		ItemID:     fixerID,
		LaneID:     laneID,
		Backend:    backend,
		Route:      route,
		State:      run.StateExited,
		StartedAt:  started.UTC().Format(time.RFC3339),
		FinishedAt: finished.UTC().Format(time.RFC3339),
		ExitCode:   &exitCode,
		EndCause:   endCause,
		Usage:      &run.Usage{Tokens: &tokens, CostUSD: &cost},
	}
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.Apply(context.Background(), tx, record.Envelope{
			RecordVersion: record.SupportedVersion,
			Kind:          run.RecordKind,
			ID:            "test-exit-" + runID,
			Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
			CreatedAt:     finished.UTC(),
			Subject:       &record.Subject{Kind: "run", ID: runID},
			Payload:       body,
		})
		return err
	})
}

func TestLiveFixerProvedByRerunStaysOpenAndReleasesWaiter(t *testing.T) {
	ctx := context.Background()
	f, l, fixer, _ := fixerRoutingFixture(t)

	// Acquitted waiter behind the fixer.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, "item-spare", StateBlocked, l.cfg.Now(), item.WaitingOn{
			Waiter: "item:" + fixer,
			Reason: RefusalTrunkAlreadyRed + ": go-tool/unit",
		})
		return err
	})

	// First-turn active run: lane leased via ClaimLease + drain.started + dispatches,
	// with ZERO rows in runs yet (exact production state while worker is running).
	run1 := "run-" + fixer + "-a1"
	now := l.cfg.Now().UTC()
	claimFixerLease(t, f, run1, fixer, "lane-1", "fixture", now)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, run1, pendingWork{
			Decision: Decision{ItemID: fixer, LaneID: "lane-1", RunRequest: &run.Request{RunID: run1, ItemID: fixer, LaneID: "lane-1", Backend: "fixture"}},
			BaseSHA:  runGit(t, f.root, "rev-parse", TrunkRef),
		}, now)
	})

	// A later proof passes the fixer's check on trunk.
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated change")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	later := now.Add(time.Minute)
	writeGreenProof(t, l, tip, later, []string{"go-tool/unit"})

	// Run Loop.pass while run1 is still active (no runs row yet):
	// 1. resolveProvedFixers keeps the fixer open and records FixerProvedKind.
	// 2. releaseTrunkAcquittals releases item-spare immediately even though fixer is still in-queue.
	l.cfg.Now = func() time.Time { return later }
	if _, err := l.pass(ctx, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, fixer); got != StateInQueue {
		t.Fatalf("first-turn active fixer was closed prematurely: %s", got)
	}
	if got := fixerState(t, f, "item-spare"); got != StateInQueue {
		t.Fatalf("acquitted waiter behind kept proved fixer was not released: %s", got)
	}

	// Second-turn active run: run1 exited and was examined with a non-commit refusal,
	// and run2 is now actively leased with no runs row yet for run2.
	recordFixerRunExit(t, f, run1, fixer, "lane-1", "fixture", now, later, 0, run.EndNormalExit, "fixture/test", 10, 0.01)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, RefusalKind, run1, refusalPayload{Step: RefusalGate, ItemID: fixer, LaneID: "lane-1"}, later); err != nil {
			return err
		}
		return appendEffect(tx, examinationDoneKind, run1, struct{}{}, later)
	})
	run2 := "run-" + fixer + "-a2"
	t2 := later.Add(time.Minute)
	claimFixerLease(t, f, run2, fixer, "lane-1", "fixture", t2)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, run2, pendingWork{
			Decision: Decision{ItemID: fixer, LaneID: "lane-1", RunRequest: &run.Request{RunID: run2, ItemID: fixer, LaneID: "lane-1", Backend: "fixture"}},
			BaseSHA:  tip,
		}, t2)
	})
	l.cfg.Now = func() time.Time { return t2 }
	if _, err := l.pass(ctx, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, fixer); got != StateInQueue {
		t.Fatalf("second-turn active fixer was closed prematurely: %s", got)
	}
}

func TestExitedLiveFixerSettlesMultiCommitAndLandsRetitledViaPass(t *testing.T) {
	ctx := context.Background()
	f, l, fixer, _ := fixerRoutingFixture(t)
	var report bytes.Buffer
	l.cfg.Report = &report
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id IN ('item-declared', 'item-spare')`)
		return err
	})

	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated change")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	now := l.cfg.Now().UTC()
	later := now.Add(time.Minute)
	writeGreenProof(t, l, tip, later, []string{"go-tool/unit"})

	// Worker run ran and produced TWO commits on lane-1, then run.Apply marked it exited
	// and released the lane BEFORE Loop.settle examined it (no candidateReadyKind yet!).
	runID := "run-" + fixer + "-a1"
	worktree := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	writeForkFile(t, filepath.Join(f.laneRoot, "lane-1", "fence.txt"), "base.txt\n")
	runGit(t, worktree, "checkout", "--detach", tip)
	writeForkFile(t, filepath.Join(worktree, "base.txt"), "first flaky fix\n")
	runGit(t, worktree, "add", "base.txt")
	runGit(t, worktree, "commit", "-qm", "worker commit 1: stabilize setup")
	writeForkFile(t, filepath.Join(worktree, "base.txt"), "second flaky fix\n")
	runGit(t, worktree, "add", "base.txt")
	runGit(t, worktree, "commit", "-qm", "worker commit 2: tighten assertion")

	baseSteps := l.cfg.Steps.(*recordingSteps)
	l.cfg.Steps = &fixerLandSteps{recordingSteps: baseSteps, root: f.root, storePath: f.storePath, t: t}

	claimFixerLease(t, f, runID, fixer, "lane-1", "fixture", now)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, runID, pendingWork{
			Decision: Decision{ItemID: fixer, LaneID: "lane-1", Seat: "build", RunRequest: &run.Request{RunID: runID, ItemID: fixer, LaneID: "lane-1", Backend: "fixture"}},
			BaseSHA:  tip,
		}, now)
	})
	recordFixerRunExit(t, f, runID, fixer, "lane-1", "fixture", now, later, 0, run.EndNormalExit, "fixture/test", 20, 0.02)

	// Drive Loop.pass: pass-start resolveProvedFixers sees the unexamined drain.started
	// and keeps the exited run open; ActionSettle examines the 2 commits and produces
	// candidateReadyKind; next Loop.pass ActionJudge gates, retitles, and lands on TrunkRef.
	l.cfg.Now = func() time.Time { return later.Add(time.Second) }
	runPassAndDrain(t, ctx, l)
	if got := fixerState(t, f, fixer); got == StateResolvedElsewhere {
		t.Fatalf("exited fixer before Loop.settle was closed prematurely")
	}
	// Run pass again so ActionJudge runs on the prepared candidate (or if synchronous, it already landed).
	for i := 0; i < 3 && fixerState(t, f, fixer) != StateLanded; i++ {
		l.cfg.Now = func() time.Time { return later.Add(time.Duration(i+2) * time.Second) }
		runPassAndDrain(t, ctx, l)
	}
	if got := fixerState(t, f, fixer); got != StateLanded {
		t.Fatalf("expected intermittent fixer with commits to land, got %s\nreport:\n%s", got, report.String())
	}

	msg := runGit(t, f.root, "log", "-1", "--format=%B", TrunkRef)
	firstLine, body, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	if !strings.HasPrefix(firstLine, "fix: intermittent check") || !strings.Contains(firstLine, "go-tool/unit") {
		t.Fatalf("unexpected landed intermittent fixer title: %q", firstLine)
	}
	if !strings.Contains(body, "worker commit 1: stabilize setup") || !strings.Contains(body, "worker commit 2: tighten assertion") {
		t.Fatalf("multi-commit messages lost from body: %q", body)
	}
	email := runGit(t, f.root, "log", "-1", "--format=%ae%n%ce", TrunkRef)
	if strings.Contains(email, "tillr-drain@localhost") {
		t.Fatalf("landed commit used hardcoded tillr-drain@localhost identity: %q", email)
	}
}

func TestNewerRedAttachmentClearsOldIntermittentProof(t *testing.T) {
	ctx := context.Background()
	f, l, fixer, _ := fixerRoutingFixture(t)
	now := l.cfg.Now().UTC()

	// Old proof at now+1m.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, FixerProvedKind, fixer, fixerProof{FixerItem: fixer, Green: "abc", Checks: []string{"go-tool/unit"}}, now.Add(time.Minute))
	})
	// Newer red attachment at now+2m.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, FixerAttachedKind, fixer, struct{}{}, now.Add(2*time.Minute))
	})

	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var intermittent bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var e error
		intermittent, _, e = readIntermittentFixer(r, fixer)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if intermittent {
		t.Fatal("readIntermittentFixer treated an older proof before FixerAttachedKind as intermittent")
	}
}

func TestLiveFixerProvedByRerunClosesViaPassWhenEndedWithNoCommit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		report string
	}{
		{name: "no-commit-done", report: "No changes needed.\nRESULT: done\n"},
		{name: "no-commit-obstacle", report: "RESULT: blocked\nOBSTACLE: owner\nQUESTION: should we?\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, fixer, _ := fixerRoutingFixture(t)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id IN ('item-declared', 'item-spare')`)
				return err
			})

			runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated change")
			tip := runGit(t, f.root, "rev-parse", TrunkRef)
			now := l.cfg.Now().UTC()
			later := now.Add(time.Minute)
			writeGreenProof(t, l, tip, later, []string{"go-tool/unit"})

			runID := "run-" + fixer + "-a1"
			worktree := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
			runGit(t, worktree, "checkout", "--detach", tip)
			claimFixerLease(t, f, runID, fixer, "lane-1", "fixture", now)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				return appendEffect(tx, dispatchStartedKind, runID, pendingWork{
					Decision: Decision{ItemID: fixer, LaneID: "lane-1", Seat: "build", RunRequest: &run.Request{RunID: runID, ItemID: fixer, LaneID: "lane-1", Backend: "fixture"}},
					BaseSHA:  tip,
				}, now)
			})
			// Record proof while run is live.
			if err := l.resolveProvedFixers(ctx); err != nil {
				t.Fatal(err)
			}
			if got := fixerState(t, f, fixer); got != StateInQueue {
				t.Fatalf("live fixer closed prematurely: %s", got)
			}

			// Worker exits with no commit.
			recordFixerRunExit(t, f, runID, fixer, "lane-1", "fixture", now, later, 0, run.EndNormalExit, "fixture/test", 5, 0.01)
			l.cfg.Now = func() time.Time { return later.Add(time.Second) }
			runPassAndDrain(t, ctx, l)

			// Also verify Loop.settle directly with obstacle report when no commit was made.
			if tc.name == "no-commit-obstacle" {
				// Covered by TestKeptIntermittentFixerObstacleWithNoCommitResolvesElsewhere below.
			}
			if got := fixerState(t, f, fixer); got != StateResolvedElsewhere {
				t.Fatalf("kept proved fixer ending with no commit should be resolved-elsewhere, got %s", got)
			}
		})
	}
}

func TestKeptIntermittentFixerObstacleWithNoCommitResolvesElsewhere(t *testing.T) {
	ctx := context.Background()
	f, l, fixer, _ := fixerRoutingFixture(t)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated change")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	now := l.cfg.Now().UTC()
	later := now.Add(time.Minute)
	writeGreenProof(t, l, tip, later, []string{"go-tool/unit"})

	runID := "run-" + fixer + "-a1"
	worktree := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	runGit(t, worktree, "checkout", "--detach", tip)
	claimFixerLease(t, f, runID, fixer, "lane-1", "fixture", now)
	if err := l.resolveProvedFixers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, fixer); got != StateInQueue {
		t.Fatalf("live fixer closed prematurely: %s", got)
	}

	d := Decision{
		ItemID: fixer,
		LaneID: "lane-1",
		Seat:   "build",
		RunRequest: &run.Request{
			RunID:   runID,
			ItemID:  fixer,
			LaneID:  "lane-1",
			Backend: "fixture",
		},
	}
	outcome, handled := l.routeObstacle(ctx, d, *d.RunRequest, "lane-1", "gen-1", tip, Obstacle{Kind: ObstacleOwner, Fields: map[string]string{"QUESTION": "which?"}}, &LoopResult{})
	if !handled || outcome.err != nil {
		t.Fatalf("routeObstacle did not handle kept fixer without commit: handled=%v err=%v", handled, outcome.err)
	}
	if got := fixerState(t, f, fixer); got != StateResolvedElsewhere {
		t.Fatalf("kept proved fixer with obstacle and no commit should be resolved-elsewhere, got %s", got)
	}
}

func TestCommittedIntermittentFixerRebaseConflictGoesToConflictFixNotResolved(t *testing.T) {
	ctx := context.Background()
	f, l, c, tip, _ := conflictingFixer(t)
	now := l.cfg.Now().UTC()
	writeGreenProof(t, l, tip, now.Add(time.Minute), []string{"go-tool/unit"})
	writeTipProgress(t, l, tip, nil, map[string]time.Time{"go-tool/unit": now.Add(time.Minute)})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, FixerProvedKind, c.ItemID, fixerProof{
			FixerItem: c.ItemID, Green: tip, Checks: []string{"go-tool/unit"},
		}, now.Add(time.Minute))
	})

	_, err := l.preRebaseFixer(ctx, c)
	conflict := asFixerRebaseConflict(err)
	if conflict == nil {
		t.Fatalf("expected fixer rebase conflict, got %v", err)
	}
	stop, landed, err := l.fixerRebaseConflicted(ctx, c, conflict, &LoopResult{}, &PassTiming{})
	if err != nil || stop != "" || landed {
		t.Fatalf("committed intermittent fixer rebase conflict failed: stop=%q landed=%v err=%v", stop, landed, err)
	}
	if got := fixerState(t, f, c.ItemID); got == StateResolvedElsewhere {
		t.Fatalf("committed intermittent fixer was closed as resolved-elsewhere on rebase conflict")
	}
}

func TestCommittedIntermittentFixerSurvivesRefusalsAndClosesOnRepeatedConflict(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		repeated bool
		setup    func(t *testing.T, l *Loop, c Candidate)
	}{
		{
			name: "conflict",
			setup: func(t *testing.T, l *Loop, c Candidate) {
				if err := l.recordConflict(ctx, c, LandOutcome{
					Result:        LandSquashConflict,
					ConflictPaths: []string{"CONFLICT (content): Merge conflict in base.txt"},
				}, "run-fix-1", "conflict-key", false); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "gate-refusal",
			setup: func(t *testing.T, l *Loop, c Candidate) {
				if err := l.recordRefusal(ctx, c, RefusalGate); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "verify-not-done",
			setup: func(t *testing.T, l *Loop, c Candidate) {
				if err := l.recordRefusal(ctx, c, RefusalVerifyNotDone); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "judge-refusal",
			setup: func(t *testing.T, l *Loop, c Candidate) {
				if err := l.holdJudge(ctx, c, "judge-run-1", ReasonEvalFailed, "eval failed", &LoopResult{}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "repeated-conflict-closes",
			repeated: true,
			setup: func(t *testing.T, l *Loop, c Candidate) {
				if err := l.recordConflict(ctx, c, LandOutcome{
					Result:        LandSquashConflict,
					ConflictPaths: []string{"CONFLICT (content): Merge conflict in base.txt"},
				}, "", "conflict-key", true); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, fixer, _ := fixerRoutingFixture(t)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id IN ('item-declared', 'item-spare')`)
				return err
			})

			runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated change")
			tip := runGit(t, f.root, "rev-parse", TrunkRef)
			now := l.cfg.Now().UTC()
			later := now.Add(time.Minute)
			writeGreenProof(t, l, tip, later, []string{"go-tool/unit"})

			runID := "run-" + fixer + "-a1"
			recordFixerRunExit(t, f, runID, fixer, "lane-1", "fixture", now, later, 0, run.EndNormalExit, "fixture/test", 10, 0.01)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				return appendEffect(tx, examinationDoneKind, runID, struct{}{}, later)
			})

			worktree := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
			runGit(t, worktree, "checkout", "--detach", tip)
			writeForkFile(t, filepath.Join(worktree, "base.txt"), "flaky fix\n")
			runGit(t, worktree, "add", "base.txt")
			runGit(t, worktree, "commit", "-qm", "fix flaky check")
			candHead := runGit(t, worktree, "rev-parse", "HEAD")

			cand := Candidate{
				ItemID: fixer, LaneID: "lane-1", RunID: runID, Worktree: worktree,
				BaseSHA: tip, HeadSHA: candHead,
			}
			tc.setup(t, l, cand)

			l.cfg.Now = func() time.Time { return later.Add(time.Second) }
			if _, err := l.pass(ctx, &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			got := fixerState(t, f, fixer)
			if tc.repeated {
				if got != StateResolvedElsewhere {
					t.Fatalf("discarded repeated conflict should close as resolved-elsewhere, got %s", got)
				}
				return
			}
			if got == StateResolvedElsewhere {
				t.Fatalf("committed intermittent fixer returning from %s was closed as resolved-elsewhere", tc.name)
			}

			// Also verify a -judge run.allocate lease keeps the fixer open across Loop.pass.
			judgeRun := runID + "-judge"
			claimFixerLease(t, f, judgeRun, fixer, "lane-2", "fixture", later.Add(2*time.Second))
			if err := l.resolveProvedFixers(ctx); err != nil {
				t.Fatal(err)
			}
			if got := fixerState(t, f, fixer); got == StateResolvedElsewhere {
				t.Fatalf("fixer with active -judge run.allocate lease was closed prematurely")
			}
		})
	}
}

func TestSettleResolvedFixerRunsAcrossPass(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		verify func(t *testing.T, f refusalLoopFixture, l *Loop, fixer, runID string, started time.Time)
	}{
		{
			name: "paused-run",
			verify: func(t *testing.T, f refusalLoopFixture, l *Loop, fixer, runID string, started time.Time) {
				claimFixerLease(t, f, runID, fixer, "lane-1", "fixture", started)
				rec := run.Record{
					RunID: runID, ItemID: fixer, LaneID: "lane-1", Backend: "fixture",
					State: run.StatePaused, StartedAt: started.Format(time.RFC3339),
					FinishedAt: started.Add(time.Second).Format(time.RFC3339), EndCause: run.EndNormalExit,
				}
				body, _ := json.Marshal(rec)
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := run.Apply(ctx, tx, record.Envelope{
						RecordVersion: record.SupportedVersion, Kind: run.RecordKind, ID: "pause-" + runID,
						Producer:  record.Producer{System: record.System, Utility: Utility, Version: Version},
						CreatedAt: started.Add(time.Second), Subject: &record.Subject{Kind: "run", ID: runID}, Payload: body,
					})
					return err
				})
				if _, err := l.pass(ctx, &LoopResult{}); err != nil {
					t.Fatal(err)
				}
				s, _ := store.OpenRead(ctx, f.storePath)
				defer s.Close()
				var state, laneState string
				_ = s.ReadTx(ctx, func(r *store.Reader) error {
					_ = r.QueryRow(`SELECT state FROM runs WHERE run_id=?`, runID).Scan(&state)
					return r.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-1'`).Scan(&laneState)
				})
				if state != run.StateTimedOut || laneState != run.LaneStateIdle {
					t.Fatalf("paused run not settled: state=%q lane=%q", state, laneState)
				}
			},
		},
		{
			name: "resumed-segment",
			verify: func(t *testing.T, f refusalLoopFixture, l *Loop, fixer, runID string, started time.Time) {
				claimFixerLease(t, f, runID, fixer, "lane-1", "fixture", started)
				recordFixerRunExit(t, f, runID, fixer, "lane-1", "fixture", started, started.Add(time.Second), 0, run.EndNormalExit, "fixture/r", 10, 0.01)
				fixtureWrite(t, f, func(tx *store.Tx) error {
					if _, err := run.ClaimLease(ctx, tx, run.Request{
						RunID: runID, ItemID: fixer, Backend: "fixture",
					}, "lane-1", started.Add(2*time.Second)); err != nil {
						return err
					}
					return appendEffect(tx, run.ResumeKind, runID, map[string]any{"run_id": runID}, started.Add(2*time.Second))
				})
				if _, err := l.pass(ctx, &LoopResult{}); err != nil {
					t.Fatal(err)
				}
				s, _ := store.OpenRead(ctx, f.storePath)
				defer s.Close()
				var state, backend, laneState string
				var startedMS int64
				var finishRaw []byte
				_ = s.ReadTx(ctx, func(r *store.Reader) error {
					_ = r.QueryRow(`SELECT state, backend, started_at FROM runs WHERE run_id=?`, runID).Scan(&state, &backend, &startedMS)
					_ = r.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-1'`).Scan(&laneState)
					return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, run.FinishKind, runID).Scan(&finishRaw)
				})
				var rec run.Record
				_ = json.Unmarshal(finishRaw, &rec)
				if state != run.StateTimedOut || laneState != run.LaneStateIdle || !rec.Resumed || backend != "fixture" || startedMS != started.UnixMilli() {
					t.Fatalf("resumed run not settled properly: state=%q lane=%q resumed=%v backend=%q startedMS=%d", state, laneState, rec.Resumed, backend, startedMS)
				}
			},
		},
		{
			name: "run-allocate-only-lease-and-unexamined-started-and-active",
			verify: func(t *testing.T, f refusalLoopFixture, l *Loop, fixer, runID string, started time.Time) {
				claimFixerLease(t, f, runID, fixer, "lane-1", "fixture", started)
				cancelled := false
				ctx2, cancel := context.WithCancel(ctx)
				go func() { <-ctx2.Done(); cancelled = true }()
				l.active[runID] = true
				l.activeCancels[runID] = cancel
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return appendEffect(tx, dispatchStartedKind, runID, pendingWork{
						Decision: Decision{ItemID: fixer, LaneID: "lane-1", RunRequest: &run.Request{RunID: runID, ItemID: fixer, LaneID: "lane-1", Backend: "fixture"}},
					}, started)
				})
				if _, err := l.pass(ctx, &LoopResult{}); err != nil {
					t.Fatal(err)
				}
				s, _ := store.OpenRead(ctx, f.storePath)
				defer s.Close()
				var laneState string
				var examined bool
				_ = s.ReadTx(ctx, func(r *store.Reader) error {
					_ = r.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-1'`).Scan(&laneState)
					return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_id=?)`, examinationDoneKind, runID).Scan(&examined)
				})
				if laneState != run.LaneStateIdle || !examined {
					t.Fatalf("run.allocate/drain.started not settled: lane=%q examined=%v", laneState, examined)
				}
				// Verify cancel was invoked (no sleep needed; ctx2.Err() is synchronous).
				if ctx2.Err() == nil && !cancelled {
					t.Fatal("locally active run cancel func was not called")
				}
			},
		},
		{
			name: "examination-leased-preserves-terminal-run",
			verify: func(t *testing.T, f refusalLoopFixture, l *Loop, fixer, runID string, started time.Time) {
				claimFixerLease(t, f, runID, fixer, "lane-1", "fixture", started)
				recordFixerRunExit(t, f, runID, fixer, "lane-1", "fixture", started, started.Add(time.Second), 0, run.EndNormalExit, "fixture/preserved", 42, 0.42)
				fixtureWrite(t, f, func(tx *store.Tx) error {
					if _, err := tx.Exec(`UPDATE lanes SET state='leased', run_id=?, leased_until=NULL WHERE lane_id='lane-1'`, runID); err != nil {
						return err
					}
					return appendEffect(tx, dispatchStartedKind, runID, pendingWork{
						Decision: Decision{ItemID: fixer, LaneID: "lane-1", RunRequest: &run.Request{RunID: runID, ItemID: fixer, LaneID: "lane-1", Backend: "fixture"}},
					}, started)
				})
				if _, err := l.pass(ctx, &LoopResult{}); err != nil {
					t.Fatal(err)
				}
				s, _ := store.OpenRead(ctx, f.storePath)
				defer s.Close()
				var state, endCause, route, laneState string
				var exitCode int
				var tokens int64
				_ = s.ReadTx(ctx, func(r *store.Reader) error {
					_ = r.QueryRow(`SELECT state, exit_code, end_cause, route, usage_tokens FROM runs WHERE run_id=?`, runID).Scan(&state, &exitCode, &endCause, &route, &tokens)
					return r.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-1'`).Scan(&laneState)
				})
				if laneState != run.LaneStateIdle || state != run.StateExited || exitCode != 0 || endCause != run.EndNormalExit || route != "fixture/preserved" || tokens != 42 {
					t.Fatalf("examination-leased run was altered or not released: lane=%q state=%q exit=%d cause=%q route=%q tokens=%d", laneState, state, exitCode, endCause, route, tokens)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, fixer, _ := fixerRoutingFixture(t)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id IN ('item-declared', 'item-spare')`); err != nil {
					return err
				}
				_, err := item.SetStateWithReceipt(ctx, tx, fixer, StateResolvedElsewhere, l.cfg.Now().UTC(), item.MoveReceipt{
					Reason: "resolved by green proof", By: Utility, ResolvesVia: "commit:abc",
				})
				return err
			})
			started := l.cfg.Now().UTC().Add(-time.Minute)
			tc.verify(t, f, l, fixer, "run-"+fixer+"-a1", started)
		})
	}
}
