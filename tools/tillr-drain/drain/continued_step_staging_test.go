package drain

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestClearableLaneCopyClearsLandedClosedAndSupersededItems(t *testing.T) {
	f, _ := passFixture(t, "item-landed", "item-closed", "item-superseded", "item-open")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	stager := &LaneStager{Root: f.root, Store: f.storePath}
	spec := StageSpec{LaneID: "lane-1", BaseSHA: base}

	runGit(t, f.root, "checkout", "-qb", "lane-copy-items")

	// 1. Landed item (recorded in drain.candidate + item state landed, no run pin).
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "landed item commit")
	landedSHA := runGit(t, f.root, "rev-parse", "HEAD")
	now := time.Now().UTC()
	if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", "run-landed-item", Candidate{
		ItemID: "item-landed", LaneID: "lane-1", RunID: "run-landed-item", BaseSHA: base, HeadSHA: landedSHA,
	}, now); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-landed'`, StateLanded)
		return err
	})
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err != nil {
		t.Fatalf("clean lane at landed item's commit was refused: %v", err)
	}

	// 2. Closed item (recorded in drain.candidate + item state closed, no run pin).
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "closed item commit")
	closedSHA := runGit(t, f.root, "rev-parse", "HEAD")
	if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", "run-closed-item", Candidate{
		ItemID: "item-closed", LaneID: "lane-1", RunID: "run-closed-item", BaseSHA: base, HeadSHA: closedSHA,
	}, now); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-closed'`)
		return err
	})
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err != nil {
		t.Fatalf("clean lane at closed item's commit was refused: %v", err)
	}

	// 3. Superseded candidate (recorded in drain.candidate-superseded, no run pin).
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "superseded item commit")
	supersededSHA := runGit(t, f.root, "rev-parse", "HEAD")
	if err := appendJudgment(context.Background(), f.storePath, candidateSupersededKind, "run", "run-superseded-item", candidateSuperseded{
		Candidate: Candidate{ItemID: "item-superseded", LaneID: "lane-1", RunID: "run-superseded-item", BaseSHA: base, HeadSHA: supersededSHA},
		RetryID:   "retry-1",
		PinRef:    "refs/tillr/runs/run-superseded-item",
		PinSHA:    supersededSHA,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err != nil {
		t.Fatalf("clean lane at superseded candidate's commit was refused: %v", err)
	}

	// 4. Open item with unlanded commit and no pin/supersession is still refused.
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "open unlanded commit")
	openSHA := runGit(t, f.root, "rev-parse", "HEAD")
	if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", "run-open-item", Candidate{
		ItemID: "item-open", LaneID: "lane-1", RunID: "run-open-item", BaseSHA: base, HeadSHA: openSHA,
	}, now); err != nil {
		t.Fatal(err)
	}
	err := stager.refuseUnlandedCommit(context.Background(), spec, f.root)
	if refusal := asLaneRefusal(err); refusal == nil || refusal.Reason != ReasonLaneUnlandedChange {
		t.Fatalf("open unlanded commit was not refused with LaneRefusalError: %v", err)
	}
}

func TestContinuedStepStagingResetsCleanLaneAtLandedCommitAndRefusesUnlanded(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-other")
	l := f.loop(t, &recordingSteps{}, &LaneStager{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	})
	var report bytes.Buffer
	l.cfg.Report = &report
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	// Put a landed item's commit on lane-1 (clean worktree), and record node-0's item_base_sha.
	lane1 := l.laneTarget("lane-1")
	if err := os.WriteFile(filepath.Join(l.laneDirPath("lane-1"), ItemBaseSHAFile), []byte(base+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, lane1, "commit", "--allow-empty", "-qm", "other landed item")
	landedSHA := runGit(t, lane1, "rev-parse", "HEAD")
	if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", "run-other", Candidate{
		ItemID: "item-other", LaneID: "lane-1", RunID: "run-other", BaseSHA: base, HeadSHA: landedSHA,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-other'`, StateLanded); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 1, 'build', 'run-a-n2', 'running')`)
		return err
	})

	// Put an unlanded commit on lane-2.
	lane2 := l.laneTarget("lane-2")
	runGit(t, lane2, "commit", "--allow-empty", "-qm", "unlanded work")

	wf := &Workflow{
		Name: "scout-build", Version: 1,
		Stages: []WorkflowStage{
			{ID: "scout", Seat: "scout", Kind: "agent", Access: "read-only"},
			{ID: "build", Seat: "build", Kind: "agent", Access: "write"},
		},
	}

	// Continued step on lane-1 should reset clean lane-1 to NodeBaseSHA and stage cleanly.
	d1 := Decision{
		ItemID: "item-a", LaneID: "lane-1", Action: ActionDispatch, Attempt: 1,
		Workflow: wf, NodeIndex: 1, NodeBaseSHA: base,
		RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-1", RunID: "run-a-n2", Backend: "fixture", WorkingDir: WorktreeDir},
	}
	if err := appendJudgment(context.Background(), f.storePath, dispatchStartedKind, "run", d1.RunRequest.RunID, pendingWork{Decision: d1}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	staged, err := l.stageDispatches(context.Background(), []Decision{d1}, base, &LoopResult{})
	if err != nil {
		t.Fatalf("stageDispatches on clean landed lane failed: %v", err)
	}
	if len(staged) != 1 {
		t.Fatalf("staged = %d, want 1 (report: %s)", len(staged), report.String())
	}
	if got := runGit(t, lane1, "rev-parse", "HEAD"); got != base {
		t.Fatalf("lane-1 HEAD after continued stage = %s, want %s", got, base)
	}

	// Continued step on lane-2 (unlanded commit) should take lane-2 out of rotation as LaneRefusalError.
	d2 := Decision{
		ItemID: "item-a", LaneID: "lane-2", Action: ActionDispatch,
		Workflow: wf, NodeIndex: 1, NodeBaseSHA: base,
		RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-2", RunID: "run-a-n2-b", Backend: "fixture", WorkingDir: WorktreeDir},
	}
	var res LoopResult
	staged2, err := l.stageDispatches(context.Background(), []Decision{d2}, base, &res)
	if err != nil {
		t.Fatalf("stageDispatches on unlanded lane returned error instead of lane refusal: %v", err)
	}
	if len(staged2) != 0 {
		t.Fatalf("staged2 = %d, want 0", len(staged2))
	}
	if res.InfraFailures != 0 {
		t.Fatalf("unlanded lane counted %d infra failures, want 0", res.InfraFailures)
	}
	if cond := l.laneConditions["lane-2"]; cond == nil || !cond.out || cond.reason != ReasonLaneUnlandedChange {
		t.Fatalf("lane-2 condition = %+v, want out with %q", cond, ReasonLaneUnlandedChange)
	}
}

func TestContinuedStepAppliesPreservedWorkOnDifferentLane(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &LaneStager{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	})
	var report bytes.Buffer
	l.cfg.Report = &report
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	// Record run-prev on lane-1 with item_base_sha in its run dir, and create preserved work
	// containing both staged index changes (^2) and unstaged/untracked working tree changes.
	lane1 := l.laneTarget("lane-1")
	writeForkFile(t, filepath.Join(lane1, "staged.txt"), "staged content\n")
	runGit(t, lane1, "add", "staged.txt")
	writeForkFile(t, filepath.Join(lane1, "unstaged.txt"), "unstaged content\n")
	runDir := RunDir(f.root, "run-prev")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, ItemBaseSHAFile), []byte(base+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO dispatches(decision_id, item_id, lane_id, run_id, action, reason, dispatched_at) VALUES('dec-prev', 'item-a', 'lane-1', 'run-prev', 'dispatch', 'ready', 1)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 1, 'scout', 'run-prev', 'completed')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 1, 'build', 'run-next', 'running')`)
		return err
	})
	if err := l.preserveWork(context.Background(), "lane-1", "run-prev", "session ended for looping"); err != nil {
		t.Fatal(err)
	}

	wf := &Workflow{
		Name: "scout-build", Version: 1,
		Stages: []WorkflowStage{
			{ID: "scout", Seat: "scout", Kind: "agent", Access: "read-only"},
			{ID: "build", Seat: "build", Kind: "agent", Access: "write"},
		},
	}
	lane2 := l.laneTarget("lane-2")
	// NodeStartRunID is empty just as advanceNode leaves it before the continued node is staged.
	d := Decision{
		ItemID: "item-a", LaneID: "lane-2", Action: ActionDispatch, Attempt: 1,
		Workflow: wf, NodeIndex: 1, NodeBaseSHA: base,
		RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-2", RunID: "run-next", Backend: "fixture", WorkingDir: WorktreeDir},
	}
	if err := appendJudgment(context.Background(), f.storePath, dispatchStartedKind, "run", d.RunRequest.RunID, pendingWork{Decision: d}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	staged, err := l.stageDispatches(context.Background(), []Decision{d}, base, &LoopResult{})
	if err != nil {
		t.Fatalf("stageDispatches on lane-2 failed: %v", err)
	}
	if len(staged) != 1 {
		t.Fatalf("staged = %d, want 1 (report: %s)", len(staged), report.String())
	}
	// Verify staged.txt is in lane-2's index and working tree, and unstaged.txt is in working tree only.
	if got, err := os.ReadFile(filepath.Join(lane2, "staged.txt")); err != nil || string(got) != "staged content\n" {
		t.Fatalf("lane-2 staged.txt = %q (%v), want %q", string(got), err, "staged content\n")
	}
	if got := runGit(t, lane2, "show", ":staged.txt"); got != "staged content" {
		t.Fatalf("lane-2 index :staged.txt = %q, want %q", got, "staged content")
	}
	if got, err := os.ReadFile(filepath.Join(lane2, "unstaged.txt")); err != nil || string(got) != "unstaged content\n" {
		t.Fatalf("lane-2 unstaged.txt = %q (%v), want %q", string(got), err, "unstaged content\n")
	}
	if status := runGit(t, lane2, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(status, "A  staged.txt") || !strings.Contains(status, "?? unstaged.txt") {
		t.Fatalf("lane-2 status = %q, want staged.txt added and unstaged.txt untracked", status)
	}

	// Negative control: a later attempt (attempt 2) with clean predecessor run-attempt2-scout
	// does not inherit attempt 1's preserved work from run-prev.
	runGit(t, lane2, "reset", "--hard", base)
	runGit(t, lane2, "clean", "-fd")
	runDir2 := RunDir(f.root, "run-attempt2-scout")
	if err := os.MkdirAll(runDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir2, ItemBaseSHAFile), []byte(base+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 2, 'scout', 'run-attempt2-scout', 'completed')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 2, 'build', 'run-attempt2-build', 'running')`)
		return err
	})
	dAttempt2 := Decision{
		ItemID: "item-a", LaneID: "lane-2", Action: ActionDispatch, Attempt: 2,
		Workflow: wf, NodeIndex: 1, NodeBaseSHA: base,
		RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-2", RunID: "run-attempt2-build", Backend: "fixture", WorkingDir: WorktreeDir},
	}
	if err := appendJudgment(context.Background(), f.storePath, dispatchStartedKind, "run", dAttempt2.RunRequest.RunID, pendingWork{Decision: dAttempt2}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	staged2, err := l.stageDispatches(context.Background(), []Decision{dAttempt2}, base, &LoopResult{})
	if err != nil || len(staged2) != 1 {
		t.Fatalf("attempt 2 stageDispatches = (%d, %v), want 1, nil", len(staged2), err)
	}
	if status := strings.TrimSpace(runGit(t, lane2, "status", "--porcelain", "--untracked-files=all")); status != "" {
		t.Fatalf("attempt 2 inherited earlier attempt's preserved work: %q", status)
	}
}

type unchangedFailingStager struct {
	calls int
	err   error
}

func (s *unchangedFailingStager) Stage(_ context.Context, _ StageSpec) error {
	s.calls++
	return s.err
}

func TestRepeatedUnchangedStagingFailureIsHeldOnceAndNotRetried(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	stager := &unchangedFailingStager{err: errors.New("persistent staging fault")}
	l := f.loop(t, &recordingSteps{}, stager)
	var report bytes.Buffer
	l.cfg.Report = &report

	// Pass 1: fails once in infrastructure class.
	var res1 LoopResult
	if _, err := l.pass(context.Background(), &res1); err != nil {
		t.Fatal(err)
	}
	if res1.InfraFailures != 1 || stager.calls != 1 {
		t.Fatalf("pass 1: infra=%d calls=%d, want 1, 1", res1.InfraFailures, stager.calls)
	}
	if _, held := l.held["item-a"]; held {
		t.Fatalf("item-a held after first failure, want hold only on repeat")
	}

	// Pass 2: same error repeats unchanged -> held once naming lane and mover.
	var res2 LoopResult
	if _, err := l.pass(context.Background(), &res2); err != nil {
		t.Fatal(err)
	}
	if stager.calls != 2 {
		t.Fatalf("pass 2 calls=%d, want 2", stager.calls)
	}
	hold, held := l.held["item-a"]
	wantReason := ReasonStagingFailed + ": lane-1"
	if !held || hold.reason != wantReason {
		t.Fatalf("item-a hold = (%v, %+v), want %q", held, hold, wantReason)
	}
	if !strings.Contains(report.String(), "correct the reported staging cause on lane-1") || !strings.Contains(report.String(), "mover:") {
		t.Fatalf("report does not name lane-specific mover: %s", report.String())
	}

	// Pass 3: held item is not retried; stager is not called again.
	var res3 LoopResult
	if _, err := l.pass(context.Background(), &res3); err != nil {
		t.Fatal(err)
	}
	if stager.calls != 2 || res3.InfraFailures != 0 {
		t.Fatalf("pass 3 retried held item: calls=%d infra=%d", stager.calls, res3.InfraFailures)
	}
	class, where, mover := whyPresentation(Decision{Action: ActionHold, Reason: wantReason})
	if class != "needs you" || where != "lane" || !strings.Contains(mover, "lane-1") {
		t.Fatalf("whyPresentation = (%q, %q, %q), want needs you / lane / naming lane-1", class, where, mover)
	}
	rule := holdrules.For(wantReason)
	if rule.Class != "needs you" || rule.Where != "lane" || !strings.Contains(rule.Mover, "lane-1") {
		t.Fatalf("holdrules.For(%q) = %+v, want needs you / lane / naming lane-1", wantReason, rule)
	}

	// Clearing the lane condition (or recording a retry after the hold) drops the hold so
	// once the staging fault is fixed, the item dispatches again.
	stager.err = nil
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(RetryDDL); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO retries(retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at) VALUES('ret-1', 'item-a', 'run-0', 'operator', 'fixed staging', ?)`, hold.at.Add(time.Second).UnixMilli())
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	var res4 LoopResult
	if _, err := l.pass(context.Background(), &res4); err != nil {
		t.Fatal(err)
	}
	if _, stillHeld := l.held["item-a"]; stillHeld {
		t.Fatalf("item-a still held after operator retry")
	}
	if stager.calls != 3 {
		t.Fatalf("pass 4 after retry calls=%d, want 3", stager.calls)
	}
}
