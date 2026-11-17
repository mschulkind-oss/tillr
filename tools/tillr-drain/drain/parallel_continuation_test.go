package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestParallelReadersContinueOnlyTheReaderCutShort(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.wallNode = "-n2"
	steps.writerAfter = 5
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.resumes != 1 || steps.started != 5 || steps.builders != 1 || result.Tokens != 50 {
		t.Fatalf("resumes=%d started=%d builders=%d tokens=%d", steps.resumes, steps.started, steps.builders, result.Tokens)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var id string
		err := r.QueryRow(`SELECT run_id FROM node_runs WHERE node_id='scout2'`).Scan(&id)
		if id != "run-item-a-a1-n2" {
			t.Errorf("continued reader became another run: %s", id)
		}
		return err
	})
}

func TestParallelReadersRetainUnjudgedHoldWhenContinuationCannotAnswer(t *testing.T) {
	for _, missingSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "second-wall", true: "missing-own-session"}[missingSession], func(t *testing.T) {
			f, l, steps := parallelFixture(t)
			steps.wallNode = "-n2"
			steps.wallAgain = true
			result := &LoopResult{}
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)
			if missingSession {
				if err := os.Remove(filepath.Join(run.NodeDir(l.laneDirPath("lane-1"), "run-item-a-a1-n2"), SessionIDFile)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)
			want := 1
			if missingSession {
				want = 0
			}
			if steps.resumes != want || steps.builders != 0 {
				t.Fatalf("resumes=%d builders=%d", steps.resumes, steps.builders)
			}
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID == "item-a" && p.decision.Reason != ReasonRunUnjudged {
					t.Fatalf("lost durable unjudged hold: %+v", p.decision)
				}
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var outcome string
				err := r.QueryRow(`SELECT outcome FROM node_runs WHERE node_id='scout2'`).Scan(&outcome)
				if outcome != "unjudged" {
					t.Errorf("outcome=%s", outcome)
				}
				return err
			})
		})
	}
}

func TestParallelReadersRecoverForkBaseFromStagingArchive(t *testing.T) {
	f, l, steps := parallelFixture(t)
	fork := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	if err := os.MkdirAll(fork, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(fork, "base.txt"), "fork base\n")
	runGit(t, fork, "add", ".")
	runGit(t, fork, "commit", "-qm", "fork base")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "ignore fork checkout")
	base := runGit(t, fork, "rev-parse", "HEAD")
	runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='[".forks/lanes/lane-1/pi"]' WHERE item_id='item-a'`)
		return err
	})
	deadOwnerClaim(t, f, l, mustPlan(t, l)[0])
	work := pendingForTest(t, f, "item-a")
	d := work.Decision
	// Stage succeeds, but the owner dies before recording the actual target base.
	spec := StageSpec{LaneID: d.LaneID, ItemID: d.ItemID, RunID: d.RunRequest.RunID, Backend: d.RunRequest.Backend, BaseSHA: work.BaseSHA, WorkingDir: WorktreeDir, Node: &d.Workflow.Stages[d.NodeIndex]}
	if err := l.cfg.Stage.Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	got := pendingForTest(t, f, "item-a")
	if steps.started != 4 || result.Rejected != 0 || got.Decision.NodeBaseSHA != base {
		t.Fatalf("fork recovery: started=%d rejected=%d base=%s want=%s", steps.started, result.Rejected, got.Decision.NodeBaseSHA, base)
	}
}

func TestResumeParallelReaderUsesOwnSessionAndFence(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.wallNode = "-n2"
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	d := pendingForTest(t, f, "item-a").Decision
	req := d.Parallel[0].RunRequest
	ownDir := run.NodeDir(l.laneDirPath(d.LaneID), req.RunID)
	ownSession, err := ReadSessionID(ownDir)
	if err != nil {
		t.Fatal(err)
	}
	sharedFence, err := os.ReadFile(filepath.Join(l.laneDirPath(d.LaneID), FenceFile))
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["docs/read-only"]' WHERE item_id='item-a'`)
		return err
	})
	got, err := Resume(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps}, ResumeSpec{RunID: req.RunID, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	afterFence, err := os.ReadFile(filepath.Join(l.laneDirPath(d.LaneID), FenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != ownSession || steps.resumes != 1 || string(afterFence) != string(sharedFence) || len(got.Fence) != 1 || got.Fence[0] != "docs/read-only" {
		t.Fatalf("sibling metadata mixed: %+v resumes=%d", got, steps.resumes)
	}
}

func TestParallelReadersOrphanedUnjudgedReaderRestartsOnlyIt(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.wallNode = "-n2"
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	// Model an interrupted continuation: its last terminal segment remains
	// unjudged, while the controller has durably orphaned the group.
	work := pendingForTest(t, f, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, loopOrphanedKind, work.Decision.RunRequest.RunID, struct{}{}, l.cfg.Now())
	})
	steps.wallNode = ""
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if steps.started != 5 || steps.resumes != 0 {
		t.Fatalf("restart repeated finished siblings: started=%d resumes=%d", steps.started, steps.resumes)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var id string
		err := r.QueryRow(`SELECT run_id FROM node_runs WHERE node_id='scout2'`).Scan(&id)
		if id != "run-item-a-a1-n2-restart" {
			t.Errorf("unfinished reader not restarted: %s", id)
		}
		return err
	})
}

func TestParallelReadersLiveContinuationKeepsDeadExaminersLease(t *testing.T) {
	f, l, _ := parallelFixture(t)
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	work := pendingForTest(t, f, "item-a")
	d := work.Decision
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, examinationClaimedKind, d.RunRequest.RunID, *work.Owner, l.cfg.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state=?,run_id=?,leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, d.RunRequest.RunID, d.LaneID)
		return err
	})
	reclaimed := []string{}
	evidence := reapEvidence{dead: map[LoopOwner]bool{*work.Owner: true}, faces: map[string]bool{d.Parallel[0].RunRequest.RunID: true}, reclaimed: &reclaimed}
	fixtureWrite(t, f, func(tx *store.Tx) error { return l.reapDeadLoopRuns(context.Background(), tx, l.cfg.Now(), evidence) })
	if got := pendingForTest(t, f, "item-a"); !got.Examining || got.Orphaned {
		t.Fatalf("live sibling lost pending ownership: %+v", got)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id=?`, d.LaneID).Scan(&state)
		if state != run.LaneStateLeased {
			t.Errorf("live continuation lease=%s", state)
		}
		return err
	})
}

func TestParallelReadersStandaloneContinuationReopensHeldSibling(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.wallNode = "-n2"
	steps.wallAgain = true
	steps.writerAfter = 6
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	work := pendingForTest(t, f, "item-a")
	if !work.Done {
		t.Fatal("fixture did not hold")
	}
	req := *work.Decision.Parallel[0].RunRequest
	req.Resume = true
	session, err := ReadSessionID(run.NodeDir(l.laneDirPath(req.LaneID), req.RunID))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"run_id": req.RunID, "item_id": req.ItemID, "lane_id": req.LaneID, "backend": req.Backend, "session_id": session, "resumed_at": l.cfg.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, req, req.LaneID, l.cfg.Now()); err != nil {
			return err
		}
		_, err := tx.Append(event.Event{Kind: run.ResumeKind, ObjectType: "run", ObjectID: &req.RunID, Payload: body, At: l.cfg.Now()})
		return err
	})
	if pendingForTest(t, f, "item-a").Done {
		t.Fatal("sibling continuation did not reopen the group")
	}
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" && (p.decision.Action != ActionHold || p.decision.Reason == ReasonRunUnjudged) {
			t.Fatalf("old terminal segment was settled while continuation runs: %+v", p.decision)
		}
	}
	steps.wallAgain = false
	if _, err := Resume(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps}, ResumeSpec{RunID: req.RunID, Request: &req}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.started != 6 || steps.resumes != 2 || steps.builders != 1 {
		t.Fatalf("continued group did not advance exactly once: readers=%d resumes=%d builders=%d", steps.started, steps.resumes, steps.builders)
	}
}

func TestParallelReadersAfterWriterKeepOneCandidate(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.writerFirst = true
	steps.builderSuffix = "-n1"
	changeParallelWorkflow(t, f, func(w *Workflow) {
		builder := w.Stages[4]
		w.Stages = append([]WorkflowStage{builder}, w.Stages[:4]...)
		w.Edges = nil
		for _, reader := range w.Stages[1:] {
			w.Edges = append(w.Edges, WorkflowEdge{From: "build", To: reader.ID})
		}
	})
	steps.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
	result := &LoopResult{}
	for i := 0; i < 5; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.builders != 1 || steps.started != 4 || steps.gates != 1 || result.Landed != 1 {
		t.Fatalf("builders=%d readers=%d gates=%d result=%+v", steps.builders, steps.started, steps.gates, result)
	}
}
