package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestConflictRepairRefusesMovedCheckout(t *testing.T) {
	_, l, d, _ := savedFixture(t)
	target := l.laneTarget(d.LaneID)
	base := runGit(t, l.cfg.Root, "rev-parse", TrunkRef)
	runGit(t, target, "add", "base.txt")
	runGit(t, target, "commit", "-qm", "candidate")
	candidateHead := runGit(t, target, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(target, "base.txt"), "new owner\n")
	runGit(t, target, "add", "base.txt")
	runGit(t, target, "commit", "-qm", "later commit")
	later := runGit(t, target, "rev-parse", "HEAD")
	c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: target, BaseSHA: base, HeadSHA: candidateHead, WorkerHeadSHA: base, Request: d.RunRequest}
	_, _, err := l.conflictFix(context.Background(), c, LandOutcome{ConflictPaths: []string{"CONFLICT: base.txt"}}, &LoopResult{}, &PassTiming{})
	if !errors.Is(err, errItemFaultHeld) {
		t.Fatalf("moved branch not held: %v", err)
	}
	if got := runGit(t, target, "rev-parse", "HEAD"); got != later {
		t.Fatalf("moved branch overwritten: %s", got)
	}
	// A retry may instead name the exact reset its earlier repair recorded.
	if _, _, err := l.conflictFix(context.Background(), c, LandOutcome{ConflictPaths: []string{"CONFLICT: base.txt"}}, &LoopResult{}, &PassTiming{}, later); err != nil {
		t.Fatalf("recorded repair reset refused: %v", err)
	}
	if got := runGit(t, target, "rev-parse", "HEAD"); got != later {
		t.Fatalf("recorded reset changed: %s", got)
	}
}

func TestRetainedLaneGoesToMatchingReturnedWorker(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1"}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	dir := l.laneDirPath("lane-1")
	ids := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	runs := []string{}
	for i, itemID := range []string{"item-a", "item-b"} {
		p := decisionFor(t, l, itemID)
		writeForkFile(t, filepath.Join(dir, SessionIDFile), ids[i]+"\n")
		writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
		finishRefused(t, f, l, p, RefusalGate, "old failure", "unit", itemID+" failed")
		runs = append(runs, p.decision.RunRequest.RunID)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, StateLanded, itemID)
			return err
		})
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, itemID := range []string{"item-a", "item-b"} {
			if _, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, StateInQueue, itemID); err != nil {
				return err
			}
			s := &WorkerSession{SessionID: ids[i], LaneID: "lane-1", LaneDir: dir, BaseSHA: base, Backend: "fixture", Route: l.cfg.Policy.request(itemID, "lane-1", runs[i], "fixture").Route()}
			if err := appendEffect(tx, RefusalKind, runs[i], refusalPayload{Step: RefusalReverted, ItemID: itemID, WorkerSession: s, Detail: itemID + " reverted"}, l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	plans := mustPlan(t, l)
	decisions := map[string]Decision{}
	for _, p := range plans {
		decisions[p.decision.ItemID] = p.decision
	}
	a, b := decisions["item-a"], decisions["item-b"]
	if a.Action != ActionDispatch || a.LaneID != "lane-2" || a.WorkerSessionReason != "lane-unavailable" {
		t.Fatalf("stale worker: %+v", a)
	}
	if b.Action != ActionDispatch || b.LaneID != "lane-1" || b.WorkerSessionReason != "resumed-worker" {
		t.Fatalf("matching worker: %+v", b)
	}
}

func TestRetainedLaneReservationCannotBeStolen(t *testing.T) {
	lanes := []string{"lane-1", "lane-2"}
	p := &planner{tick: Pass{AvailableLanes: &lanes}, held: map[string]bool{}, assigned: map[string]bool{}, workerLanes: map[string]string{"lane-1": "item-b"}}
	if p.retainedLaneFreeFor("lane-1", "item-a") || !p.retainedLaneFreeFor("lane-1", "item-b") {
		t.Fatal("another returned item took the owner session's lane")
	}
	if lane, ok := p.freeLane(); !ok || lane != "lane-2" {
		t.Fatalf("fresh fallback: %s %v", lane, ok)
	}
}

func TestConflictWithoutRunSnapshotDoesNotBorrowLaneSession(t *testing.T) {
	_, l, first, _ := returnedSessionFixture(t, RefusalGate)
	c := Candidate{RunID: first.decision.RunRequest.RunID, LaneID: first.decision.LaneID}
	worker, err := l.captureLandedWorkerSession(context.Background(), c)
	if err != nil || worker != nil {
		t.Fatalf("missing run snapshot borrowed lane: %+v %v", worker, err)
	}
}

func TestConflictMovedCandidateCannotResumeAnotherLane(t *testing.T) {
	dir := scratchUnderDurable(t)
	adapter := filepath.Join(dir, "adapter")
	writeForkFile(t, adapter, "#!/bin/sh\n")
	writeForkFile(t, filepath.Join(dir, "pause-kind"), run.PauseAtGap+"\n")
	id := "11111111-1111-4111-8111-111111111111"
	writeForkFile(t, filepath.Join(dir, SessionIDFile), id+"\n")
	worker := &WorkerSession{SessionID: id, LaneID: "lane-1", LaneDir: dir, Backend: "fixture"}
	req := &run.Request{Backend: "fixture", Command: []string{adapter}}
	worker.Route = req.Route()
	if got := workerSessionReason(worker, req); got != "resumed-worker" {
		t.Fatalf("fixture is not resumable: %s", got)
	}
	if got := conflictWorkerReason(worker, req, "lane-2"); got != "lane-unavailable" {
		t.Fatalf("moved candidate: %s", got)
	}
}

// A landing's snapshot is taken before the lane is released, not from whichever
// item happens to use that lane when the rolling suite finds the regression.
func revertedSessionFixture(t *testing.T) (refusalLoopFixture, *Loop, string, string) {
	return revertedSessionFixtureForSeat(t, "build")
}

func revertedSessionFixtureForSeat(t *testing.T, seat string) (refusalLoopFixture, *Loop, string, string) {
	t.Helper()
	f, l := designSeatFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]' WHERE item_id='item-a'`)
		return err
	})
	first := mustPlan(t, l)[0]
	first.decision.Seat = seat
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	lane := first.decision.LaneID
	dir := l.laneDirPath(lane)
	session := "11111111-1111-4111-8111-111111111111"
	writeForkFile(t, filepath.Join(dir, SessionIDFile), session+"\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	target := l.laneTarget(lane)
	writeForkFile(t, filepath.Join(target, "candidate.txt"), "broken\n")
	runGit(t, target, "add", "candidate.txt")
	runGit(t, target, "commit", "-qm", "candidate")
	head := runGit(t, target, "rev-parse", "HEAD")
	ok, _, err := l.applyPlan(context.Background(), first, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	id := first.decision.RunRequest.RunID
	if err := os.MkdirAll(RunDir(f.root, id), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(RunDir(f.root, id), SessionIDFile), session+"\n")
	writeForkFile(t, filepath.Join(RunDir(f.root, id), BaseSHAFile), base+"\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: id, ItemID: "item-a", LaneID: lane, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), EndCause: run.EndNormalExit})
	})
	candidate := Candidate{ItemID: "item-a", LaneID: lane, RunID: id, Worktree: target, BaseSHA: base, HeadSHA: head}
	candidate.WorkerSession, err = l.captureLandedWorkerSession(context.Background(), candidate)
	if err != nil || candidate.WorkerSession == nil {
		t.Fatalf("snapshot: %+v %v", candidate.WorkerSession, err)
	}
	// A recycled lane's current handle must not be mistaken for the landing run.
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "22222222-2222-4222-8222-222222222222\n")
	recovered, err := l.captureLandedWorkerSession(context.Background(), candidate)
	if err != nil || recovered == nil || recovered.SessionID != session {
		t.Fatalf("run snapshot changed: %+v %v", recovered, err)
	}
	writeForkFile(t, filepath.Join(dir, SessionIDFile), session+"\n")
	if err := appendJudgment(context.Background(), f.storePath, candidateGatedKind, "run", id, candidate, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "cherry-pick", head)
	landing := runGit(t, f.root, "rev-parse", TrunkRef)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := recordFixtureLanding(tx, "item-a", id, landing, 100); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='item-a'`, StateLanded)
		return err
	})
	if err := l.markExamined(context.Background(), first.decision); err != nil {
		t.Fatal(err)
	}
	if stop := l.abandon(context.Background(), lane, "", ""); stop != "" {
		t.Fatal(stop)
	}
	steps := &revertSteps{root: f.root, store: f.storePath, mu: &sync.Mutex{}, gated: &[]Candidate{}, returned: &[]string{}}
	l.cfg.Steps, l.cfg.Report = steps, &bytes.Buffer{}
	out, err := l.revertLanding(context.Background(), landing, landing, []HeavyCheck{{CheckID: "unit", Result: "fail", Detail: "unique failing output"}}, func(context.Context, string, string) (bool, error) { return false, nil })
	if err != nil || out.Reverted == "" {
		t.Fatalf("revert: %+v %v", out, err)
	}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	return f, l, lane, session
}

func TestConflictReturnsToWorkerThreeTimesOnItsBranch(t *testing.T) {
	f, l := designSeatFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]' WHERE item_id='item-a'`)
		return err
	})
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	lane := p.decision.LaneID
	dir := l.laneDirPath(lane)
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "11111111-1111-4111-8111-111111111111\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	target := l.laneTarget(lane)
	writeForkFile(t, filepath.Join(target, "candidate.txt"), "candidate\n")
	runGit(t, target, "add", "candidate.txt")
	runGit(t, target, "commit", "-qm", "candidate")
	head := runGit(t, target, "rev-parse", "HEAD")
	// Simulate the loop's prepared retirement commit above the worker's head.
	writeForkFile(t, filepath.Join(target, "retirement.txt"), "loop preparation\n")
	runGit(t, target, "add", "retirement.txt")
	runGit(t, target, "commit", "-qm", "retirement")
	prepared := runGit(t, target, "rev-parse", "HEAD")
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	for i := 0; i < 3; i++ {
		var d Decision
		if i == 0 {
			ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			d = p.decision
		} else {
			d = applyReturnedTurn(t, l, p, base)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, run.Record{RunID: d.RunRequest.RunID, ItemID: "item-a", LaneID: lane, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), EndCause: run.EndNormalExit})
		})
		c := Candidate{ItemID: "item-a", LaneID: lane, RunID: d.RunRequest.RunID, Worktree: target, BaseSHA: base, HeadSHA: head}
		if i == 0 {
			c.HeadSHA, c.WorkerHeadSHA = prepared, head
		}
		var err error
		c.WorkerSession, err = l.captureWorkerSession(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("conflict-%d.txt", i)
		callID := fmt.Sprintf("call-%d", i)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendLandingCallEvent(tx, callOffered, callID, landingCall{landingOffer: landingOffer{CallID: callID, Commit: head, Base: base}, ItemID: c.ItemID, RunID: c.RunID}, l.cfg.Now())
		})
		outcome := LandOutcome{ConflictPaths: []string{"CONFLICT (content): Merge conflict in " + path}}
		if err := l.recordConflict(context.Background(), c, outcome, "", fmt.Sprintf("distinct-%d", i), false); err != nil {
			t.Fatal(err)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			var raw []byte
			if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=?`, callAnswered, callID).Scan(&raw); err != nil {
				return err
			}
			var answer landingAnswer
			if err := json.Unmarshal(raw, &answer); err != nil {
				return err
			}
			if answer.Kind != "conflict" || !strings.Contains(answer.Text, path) {
				t.Fatalf("conflict answer: %+v", answer)
			}
			return nil
		})
		if err := l.markExamined(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		if stop := l.abandon(context.Background(), lane, d.RunRequest.RunID, c.HeadSHA); stop != "" {
			t.Fatal(stop)
		}
		if c := itemConflict(t, f); c.ConflictID != "" {
			t.Fatalf("conflict %d raised a hold: %+v", i, c)
		}
		p = mustPlan(t, l)[0]
		if p.decision.Action != ActionDispatch || p.decision.WorkerSessionReason != "resumed-worker" || !p.decision.RunRequest.SessionTurn {
			t.Fatalf("return %d: %+v", i, p.decision)
		}
		if !strings.Contains(p.decision.RunRequest.ResumePrompt, path) {
			t.Fatalf("missing path %s: %s", path, p.decision.RunRequest.ResumePrompt)
		}
		if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
			t.Fatalf("branch reset: %s != %s", got, head)
		}
	}
	// The third returned dispatch is staged too, not just planned.
	d := applyReturnedTurn(t, l, p, base)
	if !strings.Contains(d.RunRequest.ResumePrompt, "conflict-2.txt") {
		t.Fatal(d.RunRequest.ResumePrompt)
	}
	if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
		t.Fatalf("staging reset branch: %s", got)
	}
}

func TestRevertedLandingReturnsToWorkerSession(t *testing.T) {
	_, l, lane, session := revertedSessionFixture(t)
	p := mustPlan(t, l)[0]
	if p.decision.WorkerSessionReason != "resumed-worker" || !p.decision.RunRequest.SessionTurn || p.decision.LaneID != lane {
		t.Fatalf("not resumed: %+v", p.decision)
	}
	base := runGit(t, l.cfg.Root, "rev-parse", TrunkRef)
	d := applyReturnedTurn(t, l, p, base)
	if !strings.Contains(d.RunRequest.ResumePrompt, "unique failing output") {
		t.Fatalf("missing check output: %s", d.RunRequest.ResumePrompt)
	}
	if got, err := ReadSessionID(l.laneDirPath(lane)); err != nil || got != session {
		t.Fatalf("session=%q err=%v", got, err)
	}
}

func TestRevertedLandingKeepsNonBuildWorkerSession(t *testing.T) {
	_, l, _, _ := revertedSessionFixtureForSeat(t, "docs")
	p := mustPlan(t, l)[0]
	if p.decision.WorkerSessionReason != "resumed-worker" || !p.decision.RunRequest.SessionTurn {
		t.Fatalf("docs worker not resumed: %+v", p.decision)
	}
}

func TestRevertedWorkflowStagesProducingNodeOnCurrentTrunk(t *testing.T) {
	f, l, lane, _ := revertedSessionFixture(t)
	l.cfg.Policy.Seats["read"] = "fixture"
	w := &Workflow{Name: "scout-build", Version: 1, Stages: []WorkflowStage{
		{ID: "scout", Kind: "agent", Seat: "read", Access: "read_only"},
		{ID: "builder", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"candidate.txt"}}},
	}, Edges: []WorkflowEdge{{From: "scout", To: "builder", WhenOutcome: "pass"}}}
	var id string
	var ending refusalPayload
	fixtureRead(t, f, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT object_id,payload FROM events WHERE kind=? AND json_extract(payload,'$.step')=? ORDER BY seq DESC LIMIT 1`, RefusalKind, RefusalReverted).Scan(&id, &raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &ending)
	})
	ending.WorkerSession.Workflow = w
	ending.WorkerSession.NodeIndex = 1
	ending.WorkerSession.NodeBaseSHA = ending.WorkerSession.BaseSHA
	ending.WorkerSession.Attempt = 1
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,'scout','old-scout','passed')`); err != nil {
			return err
		}
		return appendEffect(tx, RefusalKind, id, ending, l.cfg.Now())
	})
	writeForkFile(t, filepath.Join(f.root, "trunk-change.txt"), "new trunk\n")
	runGit(t, f.root, "add", "trunk-change.txt")
	runGit(t, f.root, "commit", "-qm", "new trunk")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	if p.decision.NodeIndex != 1 || p.decision.WorkerSessionReason != "resumed-worker" {
		t.Fatalf("workflow not resumed: %+v", p.decision)
	}
	d := applyReturnedTurn(t, l, p, tip)
	if head := runGit(t, l.laneTarget(lane), "rev-parse", "HEAD"); head != tip {
		t.Fatalf("workflow checkout %s, want trunk %s", head, tip)
	}
	if d.NodeBaseSHA != tip || l.laneBase(lane) != tip {
		t.Fatalf("workflow boundary %+v, lane base %s", d, l.laneBase(lane))
	}
}

func TestRevertedLandingFreshFallbacks(t *testing.T) {
	for _, why := range []string{"lane-unavailable", "session-missing", "seat-changed"} {
		t.Run(why, func(t *testing.T) {
			f, l, lane, session := revertedSessionFixture(t)
			switch why {
			case "lane-unavailable":
				// Another item owns this still-offered lane; do not touch its checkout.
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE lanes SET state=?, leased_until=NULL WHERE lane_id=?`, LaneStateLeased, lane)
					return err
				})
				writeForkFile(t, filepath.Join(l.laneTarget(lane), "other-item.txt"), "other item\n")
			case "session-missing":
				writeForkFile(t, filepath.Join(l.laneDirPath(lane), SessionIDFile), "22222222-2222-4222-8222-222222222222\n")
			case "seat-changed":
				backend := l.cfg.Policy.Backends["fixture"]
				backend.Model = "another-model"
				l.cfg.Policy.Backends["fixture"] = backend
			}
			p := mustPlan(t, l)[0]
			if p.decision.WorkerSessionReason != why || p.decision.RunRequest == nil || p.decision.RunRequest.SessionTurn {
				t.Fatalf("fallback: %+v", p.decision)
			}
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			d := applyReturnedTurn(t, l, p, base)
			if got, err := ReadSessionID(l.laneDirPath(d.LaneID)); err != nil || got == session {
				t.Fatalf("not fresh: %q %v", got, err)
			}
			if why == "lane-unavailable" {
				if d.LaneID == lane {
					t.Fatal("dispatched onto occupied lane")
				}
				body, err := os.ReadFile(filepath.Join(l.laneTarget(lane), "other-item.txt"))
				if err != nil || string(body) != "other item\n" {
					t.Fatalf("occupied lane changed: %q %v", body, err)
				}
			}
			brief, err := os.ReadFile(filepath.Join(l.laneDirPath(d.LaneID), BriefFile))
			if err != nil || !strings.Contains(string(brief), "unique failing output") {
				t.Fatalf("evidence: %v %s", err, brief)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, d.RunRequest.RunID).Scan(&raw); err != nil {
					return err
				}
				var w pendingWork
				if err := json.Unmarshal(raw, &w); err != nil {
					return err
				}
				if w.Decision.WorkerSessionReason != why {
					t.Errorf("recorded: %s", raw)
				}
				return nil
			})
		})
	}
}
