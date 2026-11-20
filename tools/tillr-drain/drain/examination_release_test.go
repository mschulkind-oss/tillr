package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// examinedSettle claims one finished run's settlement the way a pass does — the lane is
// re-leased to the run with no expiry — and answers the settle decision and its work.
func examinedSettle(t *testing.T, f refusalLoopFixture, l *Loop) (Decision, *pendingWork) {
	t.Helper()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch claim: %v %v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		id := p.decision.RunRequest.RunID
		at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
		return applyTerminal(tx, run.Record{RunID: id, ItemID: p.decision.ItemID, LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit})
	})
	settle := mustPlan(t, l)[0]
	if settle.decision.Action != ActionSettle {
		t.Fatalf("fixture: the finished run did not plan a settlement: %+v", settle.decision)
	}
	ok, work, err := l.applyPlan(context.Background(), settle, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("settle claim: %v %v", ok, err)
	}
	if state, runID, expiry := laneRow(t, f, settle.decision.LaneID); state != run.LaneStateLeased || runID != settle.decision.RunRequest.RunID || expiry != "" {
		t.Fatalf("fixture: the settle claim left the lane %s/%s/%s, want leased to the run with no expiry", state, runID, expiry)
	}
	return settle.decision, work
}

func laneRow(t *testing.T, f refusalLoopFixture, lane string) (state, runID, expiry string) {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT state, COALESCE(run_id, ''), COALESCE(leased_until, '') FROM lanes WHERE lane_id = ?`, lane).Scan(&state, &runID, &expiry)
	}); err != nil {
		t.Fatal(err)
	}
	return state, runID, expiry
}

// laneReleases answers the causes of every drain.lane-released event for the run.
func laneReleases(t *testing.T, f refusalLoopFixture, runID string) []string {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var causes []string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq`, laneReleasedKind, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var payload laneReleased
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			causes = append(causes, payload.Cause)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return causes
}

// strayChild starts a process that carries the run's markers — TILLR_RUN_ID and
// TILLR_LOOP_STORE, inherited the way an adapter's MCP server inherits them — but is not
// the run face, and outlives it.
func strayChild(t *testing.T, l *Loop, runID string) int {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+runID, "TILLR_LOOP_STORE="+l.cfg.Store)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd.Process.Pid
}

// On 2026-11-17 a finished run's lane stayed leased with no expiry, forever, because the
// run's adapter left an MCP child behind: releaseExamination asked "does any process carry
// this run's markers", the child did, and the examination's release was skipped as though
// a continuation owned the lane. A stray child is not the run face; the examined lane is
// released, in the same write that records the examination, and the strays are reported.
func TestAnExaminedRunsLaneIsReleasedDespiteAStrayChildOfItsAdapter(t *testing.T) {
	f, l := passFixture(t, "item-a")
	var report bytes.Buffer
	l.cfg.Report = &report
	d, work := examinedSettle(t, f, l)
	pid := strayChild(t, l, d.RunRequest.RunID)
	if err := l.markExamined(context.Background(), work.Decision); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseExamination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if state, runID, _ := laneRow(t, f, d.LaneID); state != run.LaneStateIdle || runID != "" {
		t.Fatalf("the examined run's lane is %s/%s with a stray child alive, want idle", state, runID)
	}
	if got := laneReleases(t, f, d.RunRequest.RunID); len(got) != 1 || got[0] != "examined" {
		t.Fatalf("the release was recorded as %v, want one drain.lane-released with cause examined", got)
	}
	if !strings.Contains(report.String(), strconv.Itoa(pid)) || !strings.Contains(report.String(), d.RunRequest.RunID) {
		t.Fatalf("the stray child %d was not reported: %q", pid, report.String())
	}
}

// fakeRunFace starts a process the drain recognizes as a run face: argv[1] is "exec" and
// its flags name --input and --lane-root, and it carries the run's markers. It blocks on
// its stdin until the test ends.
func fakeRunFace(t *testing.T, l *Loop, runID string) {
	t.Helper()
	dir := scratchUnderDurable(t)
	if err := os.WriteFile(filepath.Join(dir, "exec"), []byte("read _\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "exec", "--input", "x", "--lane-root", "y")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+runID, "TILLR_LOOP_STORE="+l.cfg.Store)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); <-exited })
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	// No wall-clock bound: the face is a child process with no channel back, so this polls
	// until it is recognized. The face blocks on its stdin until the test ends, so one that
	// has exited can never be recognized, and that fails the case instead of waiting for it.
	for {
		faces, err := liveRunFaces(l.cfg.Store, runID, here)
		if err != nil {
			t.Fatal(err)
		}
		if len(faces) == 1 {
			return
		}
		select {
		case <-exited:
			t.Fatal("the fake run face exited before it was recognized")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A canceled examination may hand its lane to a live continuation — a run face resuming
// the same run — and that face, not the examination, releases the lane when it ends. The
// face is what keeps the lease, so it is still kept.
func TestALiveRunFaceStillKeepsTheExaminationsLease(t *testing.T) {
	f, l := passFixture(t, "item-a")
	d, _ := examinedSettle(t, f, l)
	fakeRunFace(t, l, d.RunRequest.RunID)
	if err := l.releaseExamination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if state, runID, _ := laneRow(t, f, d.LaneID); state != run.LaneStateLeased || runID != d.RunRequest.RunID {
		t.Fatalf("a live run face's lane is %s/%s, want still leased to it", state, runID)
	}
	if got := laneReleases(t, f, d.RunRequest.RunID); len(got) != 0 {
		t.Fatalf("a kept lease recorded releases %v", got)
	}
}

// The release of a non-workflow run is recorded too: before, only a workflow node wrote
// drain.examination-released, so an ordinary run's lane went idle with no event saying so.
func TestAnOrdinaryRunsReleaseIsRecorded(t *testing.T) {
	f, l := passFixture(t, "item-a")
	d, _ := examinedSettle(t, f, l)
	if err := l.releaseExamination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := laneRow(t, f, d.LaneID); state != run.LaneStateIdle {
		t.Fatalf("the lane is %s, want idle", state)
	}
	if got := laneReleases(t, f, d.RunRequest.RunID); len(got) != 1 || got[0] != "examination-ended" {
		t.Fatalf("the release was recorded as %v, want one drain.lane-released with cause examination-ended", got)
	}
}

// On 2026-11-17 a loop recorded run-item-drain-restart-on-request-a3's candidate in
// lane-5 and was stopped before it gated it. Its examination claim left lane-5 leased to
// the run with no expiry. Every later loop proved that examiner dead, but its reap released
// the lane only when no process carried the run's markers — and a test child the run had
// started (`tillr-view restart --wait`) still did. The lane stayed leased, so the candidate
// planned as no-lane behind its own lease for over three hours while ranked ready items
// waited on the other lanes. Only a live run face keeps an examination's lease; a stray
// child does not. The replacement loop judges the candidate in the lane it already holds,
// and no other item is dispatched to that lane first.
func TestADeadExaminersCandidateIsJudgedInItsOwnLaneDespiteAStrayChild(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-c")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	// item-a runs on lane-1 under a loop that then dies.
	p := mustPlan(t, l)[0]
	if p.decision.ItemID != "item-a" || p.decision.LaneID != "lane-1" {
		t.Fatalf("fixture: the first dispatch is %s on %s, want item-a on lane-1", p.decision.ItemID, p.decision.LaneID)
	}
	deadOwnerClaim(t, f, l, p)
	id := p.decision.RunRequest.RunID
	dead, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	dead.PID = 2147483647
	// The recorded facts: the run ended 0, the dead loop claimed its examination (lane
	// leased to the run, no expiry) and recorded its candidate, and was stopped there.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
		if err := applyTerminal(tx, run.Record{RunID: id, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationOwnerKind, id, dead, l.cfg.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, id, "lane-1"); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateReadyKind, id, Candidate{ItemID: "item-a", LaneID: "lane-1", RunID: id, Worktree: filepath.Join(f.laneRoot, "lane-1", WorktreeDir), BaseSHA: base, HeadSHA: base}, l.cfg.Now()); err != nil {
			return err
		}
		// item-c is ranked ahead of every other item and ready.
		_, err := tx.Exec(`UPDATE items SET rank = 'a' WHERE item_id = 'item-c'`)
		return err
	})
	// A child the run started outlives it, carrying its markers.
	strayChild(t, l, id)
	// The replacement loop. item-b holds the only other lane with a live run.
	next := f.loop(t, &recordingSteps{}, &itemContentStager{})
	next.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "run-item-b-live", ItemID: "item-b", Backend: "fixture"}, "lane-2", next.cfg.Now())
		return err
	})
	plans := mustPlan(t, next)
	var judge *plannedApplication
	for i := range plans {
		d := plans[i].decision
		if d.ItemID == "item-a" {
			judge = &plans[i]
		} else if d.Action != ActionHold && d.LaneID == "lane-1" {
			t.Fatalf("%s was planned onto lane-1, which item-a's candidate holds: %+v", d.ItemID, d)
		}
	}
	if judge == nil || judge.decision.Action != ActionJudge || judge.decision.LaneID != "lane-1" {
		t.Fatalf("the dead examiner's candidate planned %+v, want judged on lane-1", judge)
	}
	if got := laneReleases(t, f, id); len(got) != 1 || got[0] != laneReleaseReap {
		t.Fatalf("the dead examiner's lease release was recorded as %v, want one drain.lane-released with cause reap", got)
	}
	ok, _, err := next.applyPlan(context.Background(), *judge, base, next.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("the replacement loop could not claim the candidate's examination: %v %v", ok, err)
	}
	if state, runID, _ := laneRow(t, f, "lane-1"); state != run.LaneStateLeased || runID != id {
		t.Fatalf("the examination claim left lane-1 %s/%s, want leased to %s", state, runID, id)
	}
}

// A dead examiner's lease whose run face is alive — a continuation resuming the run — is
// kept: the face, not the reap, releases it when it ends.
func TestADeadExaminersLeaseIsKeptWhileTheRunFaceIsAlive(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	id := p.decision.RunRequest.RunID
	dead, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	dead.PID = 2147483647
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
		if err := applyTerminal(tx, run.Record{RunID: id, ItemID: "item-a", LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationOwnerKind, id, dead, l.cfg.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, id, p.decision.LaneID)
		return err
	})
	fakeRunFace(t, l, id)
	_ = mustPlan(t, f.loop(t, &recordingSteps{}, &itemContentStager{}))
	if state, runID, _ := laneRow(t, f, p.decision.LaneID); state != run.LaneStateLeased || runID != id {
		t.Fatalf("a dead examiner's lease with its run face alive is %s/%s after a pass, want kept", state, runID)
	}
}

// The safety net: a lane leased with no expiry to a run whose examination is recorded
// done, which no examination of this loop holds and whose run face is gone, is stuck. The
// pass's reap reclaims it, with an event, so a release missed by any path cannot strand the
// lane until a human edits the store.
func TestThePassReclaimsAStuckLeaseOfAnExaminedRunWhoseFaceIsGone(t *testing.T) {
	f, l := passFixture(t, "item-a")
	d, work := examinedSettle(t, f, l)
	// Record the examination without releasing the lane: the state the pre-fix release
	// left behind on 2026-11-17.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, examinationDoneKind, work.Decision.RunRequest.RunID, struct{}{}, l.cfg.Now())
	})
	strayChild(t, l, d.RunRequest.RunID)
	var report bytes.Buffer
	next := f.loop(t, &recordingSteps{}, &itemContentStager{})
	next.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	next.cfg.Report = &report
	_ = mustPlan(t, next)
	if state, runID, _ := laneRow(t, f, d.LaneID); state != run.LaneStateIdle || runID != "" {
		t.Fatalf("the stuck lease is %s/%s after a pass, want reclaimed", state, runID)
	}
	if got := laneReleases(t, f, d.RunRequest.RunID); len(got) != 1 || got[0] != "reap" {
		t.Fatalf("the reclaim was recorded as %v, want one drain.lane-released with cause reap", got)
	}
	if !strings.Contains(report.String(), d.LaneID) {
		t.Fatalf("the reclaim printed no line naming the lane: %q", report.String())
	}
	// The same stuck lease with its run face alive is not reclaimed: the face owns it.
	f2, l2 := passFixture(t, "item-b")
	d2, work2 := examinedSettle(t, f2, l2)
	fixtureWrite(t, f2, func(tx *store.Tx) error {
		return appendEffect(tx, examinationDoneKind, work2.Decision.RunRequest.RunID, struct{}{}, l2.cfg.Now())
	})
	fakeRunFace(t, l2, d2.RunRequest.RunID)
	next2 := f2.loop(t, &recordingSteps{}, &itemContentStager{})
	next2.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	_ = mustPlan(t, next2)
	if state, runID, _ := laneRow(t, f2, d2.LaneID); state != run.LaneStateLeased || runID != d2.RunRequest.RunID {
		t.Fatalf("a lease whose run face is alive is %s/%s after a pass, want kept", state, runID)
	}
}
