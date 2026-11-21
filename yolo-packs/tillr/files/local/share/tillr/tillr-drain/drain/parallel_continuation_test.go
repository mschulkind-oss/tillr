package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
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
			if missingSession {
				steps.beforeTerminal = func(req run.Request) error {
					if req.NodeID == "scout2" && !req.Resume {
						return os.Remove(filepath.Join(run.NodeDir(l.laneDirPath(req.LaneID), req.RunID), SessionIDFile))
					}
					return nil
				}
			}
			result := &LoopResult{}
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)
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
		return recordFixtureFork(tx, "item-a")
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
	priorResumes := steps.resumes
	got, err := Resume(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps}, ResumeSpec{RunID: req.RunID, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	afterFence, err := os.ReadFile(filepath.Join(l.laneDirPath(d.LaneID), FenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != ownSession || steps.resumes != priorResumes+1 || string(afterFence) != string(sharedFence) || len(got.Fence) != 1 || got.Fence[0] != "docs/read-only" {
		t.Fatalf("sibling metadata mixed: %+v resumes=%d", got, steps.resumes)
	}
}

func TestParallelReadersOrphanedUnjudgedReaderRestartsOnlyIt(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.wallNode = "-n2"
	// Lose the controller after the wall record, before it can continue the reader.
	steps.afterTerminal = func(req run.Request) error {
		if req.NodeID == "scout2" {
			return errRestartPending
		}
		return nil
	}
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
	steps.afterTerminal = nil
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
	assertReaderWhy(t, f, l, &req, true, "", work.Decision.RunRequest.RunID)
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

func TestWhyReportsSharedLaneSurvivorAfterLateReap(t *testing.T) {
	f, l, _ := parallelFixture(t)
	ctx := context.Background()
	if err := l.refreshSession(ctx, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	work := pendingForTest(t, f, "item-a")
	primary := work.Decision.RunRequest.RunID
	sibling := work.Decision.Parallel[0].RunRequest
	fakeRunFace(t, l, sibling.RunID)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		// Replay the state at replacement: the primary and other siblings
		// finished, but this sibling's first segment still owns the group lease.
		if _, err := tx.Exec(`DELETE FROM runs WHERE run_id=?`, sibling.RunID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=0 WHERE run_id=?`, sibling.RunID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state='leased',run_id=?,leased_until=NULL WHERE lane_id=?`, sibling.ReadOnlyGroup, sibling.LaneID)
		return err
	})
	assertReaderWhy(t, f, l, sibling, false, "", primary)
	replaceReaderSession(t, f, l)
	assertReaderWhy(t, f, l, sibling, false, "the observed worker continues executing its recorded segment; timing does not establish historical takeover", primary)
}

func TestWhyDoesNotNameAnUnstartedReaderAsASurvivor(t *testing.T) {
	f, l, _ := parallelFixture(t)
	if err := l.refreshSession(context.Background(), l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	// The loop dies after allocating the entire group but starting only one
	// sibling. The primary still has its claim, but never had a process.
	deadOwnerClaim(t, f, l, mustPlan(t, l)[0])
	d := pendingForTest(t, f, "item-a").Decision
	survivor := d.Parallel[0].RunRequest
	fakeRunFace(t, l, survivor.RunID)
	// An inherited marker on a child isn't evidence of a live run face.
	startReaderProcess(t, f, d.RunRequest.RunID)
	replaceReaderSession(t, f, l)
	var unstarted []string
	for _, node := range parallelNodes(d) {
		if id := node.RunRequest.RunID; id != survivor.RunID {
			unstarted = append(unstarted, id)
		}
	}
	assertReaderWhy(t, f, l, survivor, false, "the worker continues executing; the replacement loop does not take over that execution", unstarted...)

	// With an inaccessible dispatch domain, the same lease provides group
	// context but does not identify individual surviving readers.
	work := pendingForTest(t, f, "item-a")
	work.Owner.Domain = "different-process-domain"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, l.cfg.Now())
	})
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{StorePath: f.storePath}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			if row.Position != "running" || row.Reason != ReasonRunLive || len(row.WaitingOn) != 0 ||
				!strings.Contains(row.Why, "unsettled reader claims") || !strings.Contains(row.Why, survivor.RunID+" on "+survivor.LaneID) ||
				!strings.Contains(row.Mover, "individual processes are not observable here") || strings.Contains(row.Mover, d.RunRequest.RunID) ||
				!strings.Contains(row.Mover, "timing does not establish takeover; drain acts on recorded worker finishes") {
				t.Fatalf("wrong report for an unobservable reader group: %+v", row)
			}
			return nil
		}
		t.Fatal("reader item missing from why")
		return nil
	})
}

func TestPlanDoesNotObserveWorkerProcesses(t *testing.T) {
	f, l, _ := parallelFixture(t)
	deadOwnerClaim(t, f, l, mustPlan(t, l)[0])
	d := pendingForTest(t, f, "item-a").Decision
	at := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetState(context.Background(), tx, d.ItemID, StateBlocked, at, item.WaitingOn{Waiter: "owner", Reason: "approval before the next dispatch"})
		return err
	})
	fixtureRead(t, f, func(r *store.Reader) error {
		tick := Pass{Now: at.Format(time.RFC3339Nano), StorePath: f.storePath, examineItem: d.ItemID}
		before, err := Plan(r, tick, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(before) != 1 || before[0].Subject == nil || before[0].Subject.ID != d.ItemID {
			t.Fatalf("expected one reader-group decision, got %+v", before)
		}
		fakeRunFace(t, l, d.Parallel[0].RunRequest.RunID)
		after, err := Plan(r, tick, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(after) != 1 || before[0].ID != after[0].ID || !bytes.Equal(before[0].Payload, after[0].Payload) {
			t.Fatalf("planning changed without new inputs: before %s; after %+v", before[0].Payload, after)
		}
		var observed []plannedApplication
		if err := l.readPassPlans(context.Background(), r, at, d.ItemID, nil, &observed, nil, false, nil); err != nil {
			return err
		}
		if len(observed) != 1 || observed[0].decision.Reason != ReasonRunLive {
			t.Fatalf("loop did not pass live-worker evidence to Plan: %+v", observed)
		}
		return nil
	})
}

func startReaderProcess(t *testing.T, f refusalLoopFixture, runID string) {
	t.Helper()
	identity, err := processStoreIdentity(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	worker := exec.Command("sleep", "60")
	worker.Env = append(os.Environ(), "TILLR_LOOP_STORE="+identity, "TILLR_RUN_ID="+runID)
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Process.Kill(); _ = worker.Wait() })
}

func replaceReaderSession(t *testing.T, f refusalLoopFixture, l *Loop) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		// The new session publishes before it records the old session's end.
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id,started_at,refreshed_at,next_pass_at,loop_owner,lanes)
		 SELECT 'replacement',started_at+100,refreshed_at+100,next_pass_at+100,loop_owner,lanes FROM drain_sessions WHERE session_id=?`, l.sessionID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE drain_sessions SET ended_at=started_at+200 WHERE session_id=?`, l.sessionID)
		return err
	})
}

func assertReaderWhy(t *testing.T, f refusalLoopFixture, l *Loop, live *run.Request, resumed bool, continuation string, absent ...string) {
	t.Helper()
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{StorePath: f.storePath}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			if row.Reason != ReasonRunLive || row.Position != "running" || len(row.WaitingOn) != 0 ||
				!strings.Contains(row.Mover, "worker "+live.RunID+" on "+live.LaneID) ||
				strings.Contains(row.Why, "(resumed)") != resumed || strings.Contains(row.Why, "precedes the current loop") != (continuation != "") {
				t.Fatalf("wrong shared-lane worker: %+v", row)
			}
			if continuation != "" && (row.Where != "lane / node "+live.NodeID ||
				!strings.Contains(row.Why, "precedes the current loop: "+live.RunID+"; "+continuation+"; drain waits for its recorded finish")) {
				t.Fatalf("wrong survivor and finish: where=%q why=%q", row.Where, row.Why)
			}
			for _, id := range absent {
				if strings.Contains(row.Mover, id) || strings.Contains(row.Why, id) {
					t.Fatalf("non-survivor %s got credit: why=%q mover=%q", id, row.Why, row.Mover)
				}
			}
			return nil
		}
		t.Fatal("reader item missing from why")
		return nil
	})
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
