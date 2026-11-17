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
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6000; i++ {
		faces, err := liveRunFaces(l.cfg.Store, runID, here)
		if err != nil {
			t.Fatal(err)
		}
		if len(faces) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture: the fake run face was never recognized as one")
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
