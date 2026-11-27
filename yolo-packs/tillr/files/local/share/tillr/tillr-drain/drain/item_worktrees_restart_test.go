package drain

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestItemWorktreesOrphanedRunPreservedAndReplacementJudgesOnce(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.ItemWorktrees = true
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}
	l.cfg.Stage = stager

	ctx := context.Background()
	at := l.cfg.Now().UTC()
	base := runGit(t, f.root, "rev-parse", "main")
	if err := l.stageLandingLanes(ctx); err != nil {
		t.Fatalf("stageLandingLanes: %v", err)
	}

	// Stage an initial run in item-a's own directory and simulate loop death leaving
	// an orphaned run with uncommitted work and an unbounded lease.
	oldRunID := "run-item-a-a1"
	oldReq := run.Request{
		RunID:   oldRunID,
		ItemID:  "item-a",
		LaneID:  "item-a",
		Backend: "fixture",
		Command: []string{"true"},
	}
	if err := stager.Stage(ctx, StageSpec{
		LaneID:     "item-a",
		ItemID:     "item-a",
		RunID:      oldRunID,
		Backend:    "fixture",
		WorkingDir: WorktreeDir,
		BaseSHA:    base,
	}); err != nil {
		t.Fatalf("stage initial item dir: %v", err)
	}
	wt := filepath.Join(f.laneRoot, "item-a", WorktreeDir)
	writeItemTestFile(t, filepath.Join(wt, "tools", "item-a", "orphan.txt"), "uncommitted from dead loop\n")

	here, err := currentLoopOwner()
	if err != nil {
		t.Fatalf("currentLoopOwner: %v", err)
	}
	deadOwner := *here
	deadOwner.PID = 2147483647
	oldDec := Decision{
		ItemID:     "item-a",
		LaneID:     "item-a",
		Action:     ActionDispatch,
		Attempt:    1,
		RunRequest: &oldReq,
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(ctx, tx, oldReq, "item-a", at); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET leased_until=NULL WHERE lane_id='item-a'`); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, oldRunID, &pendingWork{
			Decision: oldDec,
			Owner:    &deadOwner,
			BaseSHA:  base,
		}, at)
	})

	// Start a replacement loop with ItemWorktrees on.
	steps := &orphanReplaceSteps{t: t, f: f}
	var report strings.Builder
	replacement, err := NewLoop(LoopConfig{
		Root:              f.root,
		Store:             f.storePath,
		LaneRoot:          f.laneRoot,
		Lanes:             []string{"lane-1", "lane-2"},
		ItemWorktrees:     true,
		Policy:            l.cfg.Policy,
		Steps:             steps,
		Stage:             stager,
		Now:               func() time.Time { return at.Add(time.Minute) },
		Report:            &report,
		ExitWhenDrained:   true,
		StorePollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLoop replacement: %v", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := replacement.Run(runCtx)
	if err != nil {
		t.Fatalf("replacement.Run: %v (result=%+v)\n%s", err, result, report.String())
	}
	if result.Landed != 1 || result.Rejected != 0 {
		t.Fatalf("replacement result = %+v, want Landed=1 Rejected=0\n%s", result, report.String())
	}
	if len(steps.runs) != 1 {
		t.Fatalf("replacement runs = %d, want 1: %+v", len(steps.runs), steps.runs)
	}
	if steps.runs[0].LaneID != "item-a" {
		t.Fatalf("replacement ran in lane %q, want item-a", steps.runs[0].LaneID)
	}
	if steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("replacement gates=%d lands=%d, want 1 each", steps.gates, steps.lands)
	}
	// Verify the orphaned run's uncommitted work was preserved in the store and refs.
	var preservedFound bool
	fixtureRead(t, f, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		for _, p := range saved {
			if p.RunID == oldRunID && p.LaneID == "item-a" {
				for _, file := range p.Files {
					if file.Path == "tools/item-a/orphan.txt" {
						preservedFound = true
					}
				}
			}
		}
		return nil
	})
	if !preservedFound {
		t.Fatalf("expected drain.preserved record for orphaned run %s in item-a with tools/item-a/orphan.txt", oldRunID)
	}
	refs := strings.TrimSpace(runGit(t, f.root, "for-each-ref", "--format=%(refname)", "refs/tillr/preserved/"))
	if !strings.Contains(refs, oldRunID) {
		t.Fatalf("expected preserved ref for %s, got %q", oldRunID, refs)
	}
}

type orphanReplaceSteps struct {
	stubSteps
	t            *testing.T
	f            refusalLoopFixture
	runs         []run.Request
	gates        int
	lands        int
	sawPreserved bool
}

func (s *orphanReplaceSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	wt := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if _, err := os.Stat(filepath.Join(wt, "tools", "item-a", "orphan.txt")); err == nil {
		s.sawPreserved = true
	}
	writeItemTestFile(s.t, filepath.Join(wt, "tools", "item-a", "done.txt"), "finished "+req.RunID+"\n")
	runGit(s.t, wt, "add", ".")
	runGit(s.t, wt, "commit", "-qm", "replacement "+req.RunID)
	zero := 0
	rec := run.Record{
		RunID:      req.RunID,
		ItemID:     req.ItemID,
		LaneID:     req.LaneID,
		Backend:    req.Backend,
		State:      run.StateExited,
		ExitCode:   &zero,
		StartedAt:  "2026-11-27T01:00:00Z",
		FinishedAt: "2026-11-27T01:01:00Z",
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE lanes SET state='idle', run_id=NULL, leased_until=NULL WHERE lane_id=?`, req.LaneID); err != nil {
			return err
		}
		return applyTerminal(tx, rec)
	})
	return rec, "RESULT: done\n", err
}

func (s *orphanReplaceSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	return s.stubSteps.Gate(ctx, c)
}

func (s *orphanReplaceSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	return s.stubSteps.Land(ctx, c)
}

func (s *orphanReplaceSteps) Verdict(ctx context.Context, c Candidate, to string) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, to, c.ItemID)
		return err
	})
}

func TestItemWorktreesPausedRunMidNodeResumesInSameItemDirectory(t *testing.T) {
	f, l, base := judgeRouteFixture(t, "pass")
	steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: "worker"}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.ItemWorktrees = true
	l.cfg.StorePollInterval = 5 * time.Millisecond

	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})

	ctx := context.Background()
	result := &LoopResult{}
	for i := 0; i < 6 && !steps.pausedOnce; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if !steps.pausedOnce {
		t.Fatalf("worker node never paused; runs=%+v", steps.runs)
	}
	for _, r := range steps.runs {
		if r.LaneID != "item-a" {
			t.Fatalf("pre-restart run used lane %q, want item-a", r.LaneID)
		}
	}

	// Restart a fresh Loop instance with ItemWorktrees on; the paused node must
	// resume in the same item directory (item-a) with the same session id.
	restarted, err := NewLoop(LoopConfig{
		Root:              f.root,
		Store:             f.storePath,
		LaneRoot:          f.laneRoot,
		Lanes:             []string{"lane-1", "lane-2"},
		ItemWorktrees:     true,
		Policy:            l.cfg.Policy,
		Steps:             steps,
		Stage:             l.cfg.Stage,
		Now:               l.cfg.Now,
		Report:            io.Discard,
		StorePollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLoop restarted: %v", err)
	}
	restartResult := &LoopResult{}
	for i := 0; i < 8 && restartResult.Landed == 0; i++ {
		if _, err := restarted.pass(ctx, restartResult); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, restarted, restartResult)
	}
	if restartResult.Landed != 1 {
		t.Fatalf("restarted loop did not land paused item: %+v", restartResult)
	}
	if len(steps.resumedReqs) != 1 {
		t.Fatalf("resumed requests = %d, want 1: %+v", len(steps.resumedReqs), steps.resumedReqs)
	}
	if steps.resumedReqs[0].LaneID != "item-a" {
		t.Fatalf("resumed in lane %q, want item-a", steps.resumedReqs[0].LaneID)
	}
	if steps.resumeSession != steps.pauseSession || steps.pauseSession == "" {
		t.Fatalf("resume session = %q, want %q", steps.resumeSession, steps.pauseSession)
	}
}

func TestItemWorktreesSweepLeavesItemDirectoryInPlace(t *testing.T) {
	f, l := passFixture(t, "item-open", "item-ended")
	l.cfg.ItemWorktrees = true
	l.cfg.Lanes = []string{"lane-1"}
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}
	l.cfg.Stage = stager

	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", "main")
	if err := stager.Stage(ctx, StageSpec{
		LaneID:     "item-open",
		ItemID:     "item-open",
		RunID:      "run-item-open-a1",
		Backend:    "fixture",
		WorkingDir: WorktreeDir,
		BaseSHA:    base,
	}); err != nil {
		t.Fatalf("stage item-open: %v", err)
	}
	openWT := filepath.Join(f.laneRoot, "item-open", WorktreeDir)
	openScratch := run.DurableDir(filepath.Join(f.laneRoot, "item-open"), "run-item-open-a1")
	writeItemTestFile(t, filepath.Join(openWT, "tools", "item-open", "wip.txt"), "keep me\n")
	writeItemTestFile(t, filepath.Join(openScratch, "cache.bin"), "scratch\n")

	// Also create an unowned item directory with no runs in the store yet.
	unownedWT := filepath.Join(f.laneRoot, "item-unowned", WorktreeDir)
	if err := os.MkdirAll(filepath.Dir(unownedWT), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "worktree", "add", "-q", "-b", LaneBranch(f.laneRoot, "item-unowned"), unownedWT, base)

	// Mark item-ended as closed with a prior run and dirty work; reclaimEndedItemLanes
	// saves its dirty work and collects the ended item's worktree.
	if err := stager.Stage(ctx, StageSpec{
		LaneID:     "item-ended",
		ItemID:     "item-ended",
		RunID:      "run-item-ended-a1",
		Backend:    "fixture",
		WorkingDir: WorktreeDir,
		BaseSHA:    base,
	}); err != nil {
		t.Fatalf("stage item-ended: %v", err)
	}
	endedWT := filepath.Join(f.laneRoot, "item-ended", WorktreeDir)
	writeItemTestFile(t, filepath.Join(endedWT, "tools", "item-ended", "dirty.txt"), "save me\n")
	at := l.cfg.Now().UTC()
	zero := 0
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-ended'`); err != nil {
			return err
		}
		return applyTerminal(tx, run.Record{
			RunID:      "run-item-ended-a1",
			ItemID:     "item-ended",
			LaneID:     "item-ended",
			Backend:    "fixture",
			State:      run.StateExited,
			ExitCode:   &zero,
			StartedAt:  at.Format(time.RFC3339),
			FinishedAt: at.Format(time.RFC3339),
		})
	})

	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes: %v", err)
	}

	// Active item directories (and their worktrees/scratch) stay untouched.
	for _, p := range []string{openWT, filepath.Join(openWT, "tools", "item-open", "wip.txt"), filepath.Join(openScratch, "cache.bin")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("sweep removed expected item path %s: %v", p, err)
		}
	}
	// Unowned (no items row) and ended item worktrees are collected.
	for _, p := range []string{unownedWT, endedWT} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("sweep left ended/removed item worktree %s in place: stat err=%v", p, err)
		}
	}
}

func TestItemWorktreesExamineAndResumeFindItemDirectoryRun(t *testing.T) {
	t.Run("examine", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		stager := &LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.storePath,
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			Now:      l.cfg.Now,
		}
		ctx := context.Background()
		at := l.cfg.Now().UTC()
		base := runGit(t, f.root, "rev-parse", "main")
		runID := "run-item-a-a1"
		if err := stager.Stage(ctx, StageSpec{
			LaneID:     "item-a",
			ItemID:     "item-a",
			RunID:      runID,
			Backend:    "fixture",
			WorkingDir: WorktreeDir,
			BaseSHA:    base,
		}); err != nil {
			t.Fatalf("stage item-a: %v", err)
		}
		wt := filepath.Join(f.laneRoot, "item-a", WorktreeDir)
		writeItemTestFile(t, filepath.Join(wt, "tools", "item-a", "change.txt"), "ok\n")
		runGit(t, wt, "add", ".")
		runGit(t, wt, "commit", "-qm", "item-a candidate")
		head := runGit(t, wt, "rev-parse", "HEAD")

		zero := 0
		req := run.Request{RunID: runID, ItemID: "item-a", LaneID: "item-a", Backend: "fixture", Command: []string{"true"}}
		dec := Decision{ItemID: "item-a", LaneID: "item-a", Action: ActionDispatch, Attempt: 1, RunRequest: &req}
		cand := Candidate{ItemID: "item-a", LaneID: "item-a", RunID: runID, Worktree: wt, BaseSHA: base, HeadSHA: head}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := appendEffect(tx, dispatchStartedKind, runID, &pendingWork{Decision: dec, BaseSHA: base}, at); err != nil {
				return err
			}
			if err := appendEffect(tx, candidateReadyKind, runID, cand, at); err != nil {
				return err
			}
			return applyTerminal(tx, run.Record{
				RunID:      runID,
				ItemID:     "item-a",
				LaneID:     "item-a",
				Backend:    "fixture",
				State:      run.StateExited,
				ExitCode:   &zero,
				StartedAt:  at.Format(time.RFC3339),
				FinishedAt: at.Format(time.RFC3339),
			})
		})

		// Mirror tools/tillr-drain/recovery.go: recoveryFace constructs Loop with
		// Lanes: []string{"recovery"} and no explicit ItemWorktrees flag.
		steps := &terminalSteps{}
		examineLoop, err := NewLoop(LoopConfig{
			Root:     f.root,
			Store:    f.storePath,
			LaneRoot: f.laneRoot,
			Lanes:    []string{"recovery"},
			Policy:   l.cfg.Policy,
			Steps:    steps,
			Stage:    stager,
			Now:      l.cfg.Now,
			Report:   io.Discard,
		})
		if err != nil {
			t.Fatalf("NewLoop examine: %v", err)
		}
		res, err := examineLoop.Examine(ctx, runID)
		if err != nil {
			t.Fatalf("Examine(%s): %v", runID, err)
		}
		if res.Landed != 1 || steps.gates != 1 || steps.lands != 1 || steps.verdicts != 1 {
			t.Fatalf("Examine result=%+v steps=%+v, want 1 landed/gate/land/verdict", res, steps)
		}
	})

	t.Run("resume", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		stager := &LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.storePath,
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			Now:      l.cfg.Now,
		}
		ctx := context.Background()
		at := l.cfg.Now().UTC()
		base := runGit(t, f.root, "rev-parse", "main")
		runID := "run-item-a-a1"
		if err := stager.Stage(ctx, StageSpec{
			LaneID:     "item-a",
			ItemID:     "item-a",
			RunID:      runID,
			Backend:    "fixture",
			WorkingDir: WorktreeDir,
			BaseSHA:    base,
		}); err != nil {
			t.Fatalf("stage item-a: %v", err)
		}
		const sessID = "11111111-2222-3333-4444-555555555555"
		laneDir := filepath.Join(f.laneRoot, "item-a")
		writeItemTestFile(t, filepath.Join(laneDir, SessionIDFile), sessID+"\n")

		req := run.Request{
			RunID:   runID,
			ItemID:  "item-a",
			LaneID:  "item-a",
			Backend: "fixture",
			Command: []string{"true"},
		}
		reqBody, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		envRaw, err := record.Encode(record.Envelope{
			RecordVersion: record.SupportedVersion,
			Kind:          run.RequestKind,
			ID:            "req-" + runID,
			Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
			Subject:       &record.Subject{Kind: "run", ID: runID},
			CreatedAt:     at,
			Payload:       reqBody,
		})
		if err != nil {
			t.Fatal(err)
		}
		writeItemTestFile(t, filepath.Join(f.laneRoot, ".requests", runID+".request.json"), string(envRaw))

		// Include an unstaged pool lane row in lanes to verify StoreLanes does not break
		// TakeBoundary when resuming an item directory run.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('lane-1','idle'),('item-a','idle') ON CONFLICT(lane_id) DO NOTHING`); err != nil {
				return err
			}
			return applyTerminal(tx, run.Record{
				RunID:      runID,
				ItemID:     "item-a",
				LaneID:     "item-a",
				Backend:    "fixture",
				State:      run.StateTimedOut,
				StartedAt:  at.Format(time.RFC3339),
				FinishedAt: at.Format(time.RFC3339),
			})
		})

		var gotReq run.Request
		zero := 0
		res, err := Resume(ctx, ResumeConfig{
			Root:     f.root,
			Store:    f.storePath,
			LaneRoot: f.laneRoot,
			Now:      l.cfg.Now,
			Steps: runStepFunc(func(_ context.Context, r run.Request) (run.Record, string, error) {
				gotReq = r
				return run.Record{
					RunID:    r.RunID,
					ItemID:   r.ItemID,
					LaneID:   r.LaneID,
					Backend:  r.Backend,
					State:    run.StateExited,
					ExitCode: &zero,
					Resumed:  true,
				}, "RESULT: done\n", nil
			}),
		}, ResumeSpec{RunID: runID})
		if err != nil {
			t.Fatalf("Resume(%s): %v", runID, err)
		}
		if !gotReq.Resume || gotReq.LaneID != "item-a" || res.LaneID != "item-a" || res.SessionID != sessID {
			t.Fatalf("unexpected resume result: req=%+v res=%+v", gotReq, res)
		}
		if len(res.Violations) != 0 {
			t.Fatalf("unexpected boundary violations: %+v", res.Violations)
		}
	})
}

type runStepFunc func(context.Context, run.Request) (run.Record, string, error)

func (fn runStepFunc) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	return fn(ctx, req)
}

func writeItemTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
