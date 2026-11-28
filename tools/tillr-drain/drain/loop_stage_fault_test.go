package drain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type settlingStartSteps struct {
	recordingSteps
	fixture           refusalLoopFixture
	now               func() time.Time
	settling, release chan struct{}
	started           chan string
}

func (s *settlingStartSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.ItemID == "item-a" {
		wt := filepath.Join(s.fixture.laneRoot, req.LaneID, req.WorkingDir)
		file := "tools/item-a/work.txt"
		if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, file)), 0755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(filepath.Join(wt, file), []byte("work\n"), 0644); err != nil {
			return run.Record{}, "", err
		}
		for _, args := range [][]string{{"add", file}, {"commit", "-m", "item-a work"}} {
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Dir = wt
			if out, err := cmd.CombinedOutput(); err != nil {
				return run.Record{}, "", fmt.Errorf("git %v: %w: %s", args, err, out)
			}
		}
	}
	st, err := store.Open(ctx, s.fixture.storePath, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer st.Close()
	exit := 0
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{}}
	if err := st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		return rec, "", err
	}
	if req.ItemID == "item-b" {
		s.started <- req.LaneID
	}
	return rec, "", nil
}

func (s *settlingStartSteps) Gate(ctx context.Context, candidate Candidate) (GateOutcome, error) {
	if candidate.ItemID == "item-a" {
		close(s.settling)
		select {
		case <-s.release:
		case <-ctx.Done():
			return GateOutcome{}, ctx.Err()
		}
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

type settlingStartStager struct{ root string }

func (s settlingStartStager) Stage(_ context.Context, spec StageSpec) error {
	for _, name := range []string{FenceFile, ItemFenceFile} {
		if err := os.WriteFile(filepath.Join(s.root, spec.LaneID, name), []byte("tools/\n"), 0644); err != nil {
			return err
		}
	}
	return nil
}

func fileStartWhileSettling(t *testing.T, l *Loop, at time.Time) {
	t.Helper()
	s, err := store.Open(context.Background(), l.cfg.Store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedRefusalItemAt(t, s, "item-b", at)
}

type stageFaultStager struct{ settlingStartStager }

func (s stageFaultStager) Stage(ctx context.Context, spec StageSpec) error {
	if spec.ItemID == "item-b" {
		return errors.New("fork checkout has unlanded or divergent commits; staging will not reset it")
	}
	return s.settlingStartStager.Stage(ctx, spec)
}

type stageFaultSteps struct {
	recordingSteps
	started chan run.Request
}

func (s *stageFaultSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.started <- req
	// Leave item-a live until the test stops the loop. No terminal worker event
	// can clear the fault or free its lane in the meantime.
	<-ctx.Done()
	return run.Record{}, "", ctx.Err()
}

func stageFaultLoop(t *testing.T, f refusalLoopFixture, steps Steps) (*Loop, *bytes.Buffer) {
	t.Helper()
	t.Setenv("TILLR_LOOP_STORE", "")
	holdWALKeeper(t, f.storePath)
	l := f.loop(t, steps, stageFaultStager{settlingStartStager{f.laneRoot}})
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
	l.cfg.BackoffBase = 0 // Exercise the backoff path without waiting on elapsed time.
	var report bytes.Buffer
	l.cfg.Report = &report
	return l, &report
}

func TestStageFaultDoesNotBlockFreshStartWhileWorkerIsLive(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 2, "item-a")
	steps := &stageFaultSteps{started: make(chan run.Request, 2)}
	l, report := stageFaultLoop(t, f, steps)
	ctx, cancel := context.WithCancel(t.Context())
	var res LoopResult
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err = l.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	next := func() run.Request {
		t.Helper()
		select {
		case req := <-steps.started:
			return req
		case <-done:
			t.Fatalf("loop stopped before worker started: %+v (%v)\n%s", res, err, report)
			return run.Request{}
		}
	}
	a := next()
	if a.ItemID != "item-a" || a.LaneID != "lane-1" {
		t.Fatalf("first worker = %s on %s, want item-a on lane-1", a.ItemID, a.LaneID)
	}
	s, openErr := store.Open(ctx, f.storePath, true)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer s.Close()
	seedRefusalItem(t, s, "item-b")
	seedRefusalItem(t, s, "item-c")
	c := next()
	if c.ItemID != "item-c" || c.LaneID != "lane-2" {
		t.Errorf("next worker = %s on %s, want item-c on lane-2", c.ItemID, c.LaneID)
	}
	cancel()
	<-done
	log := report.String()
	if err != nil || res.InfraFailures == 0 || !strings.Contains(log, "staging item-b on lane-2 failed in the infrastructure class") {
		t.Fatalf("expected item-b infrastructure staging failure: %+v (%v)\n%s", res, err, log)
	}
	if strings.Contains(log, "and nothing dispatched; waiting") {
		t.Fatalf("loop backed off with item-a still live:\n%s", log)
	}
}

func TestIdleStageFaultBacksOffAndReopensStarts(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-b")
	l, report := stageFaultLoop(t, f, &recordingSteps{})
	l.cfg.Lanes = []string{"lane-1"}
	res, err := l.Run(t.Context())
	if err != nil || res.Stop != StopQueueDrained || res.Dispatched != 0 || res.InfraFailures != 2 {
		t.Fatalf("expected unchanged fault to be held after retry: %+v (%v)\n%s", res, err, report)
	}
	log := report.String()
	backoff := strings.Index(log, "1 consecutive infrastructure failure(s) and nothing dispatched; waiting 0s before the next attempt")
	held := strings.Index(log, "staging failure repeated unchanged")
	if backoff < 0 || held <= backoff {
		t.Fatalf("expected idle backoff followed by another staging attempt:\n%s", log)
	}
}

type splitRefreshRecoveringStager struct {
	settlingStartStager
	loop     *Loop
	failures int
	calls    int
}

func (s *splitRefreshRecoveringStager) Stage(ctx context.Context, spec StageSpec) error {
	s.calls++
	if s.calls <= s.failures {
		return fmt.Errorf("the brief could not be rendered (attempt %d)", s.calls)
	}
	return s.settlingStartStager.Stage(ctx, spec)
}

func TestStagingFailureAfterStartViewObservesClaimStillBacksOffAndRetries(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-b")
	t.Setenv("TILLR_LOOP_STORE", "")
	holdWALKeeper(t, f.storePath)
	steps := &stageFaultSteps{started: make(chan run.Request, 1)}
	stager := &splitRefreshRecoveringStager{
		settlingStartStager: settlingStartStager{f.laneRoot},
		failures:            2,
	}
	l := f.loop(t, steps, stager)
	stager.loop = l
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
	l.cfg.BackoffBase = 0
	var report bytes.Buffer
	l.cfg.Report = &report

	ctx, cancel := context.WithCancel(t.Context())
	var res LoopResult
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err = l.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	select {
	case req := <-steps.started:
		if req.ItemID != "item-b" || req.LaneID != "lane-1" {
			t.Fatalf("recovered worker = %s on %s, want item-b on lane-1", req.ItemID, req.LaneID)
		}
	case <-done:
		t.Fatalf("loop stopped before recovered attempt dispatched (stop=%q, infra=%d, dispatched=%d, detail=%q, err=%v)\n%s",
			res.Stop, res.InfraFailures, res.Dispatched, res.Detail, err, report.String())
	}
	cancel()
	<-done
	if err != nil {
		t.Fatalf("loop failed: %v\n%s", err, report.String())
	}
	if res.InfraFailures != 2 {
		t.Fatalf("the loop counted %d infrastructure failure(s), want 2 (dispatched=%d, detail=%q)\n%s",
			res.InfraFailures, res.Dispatched, res.Detail, report.String())
	}
	if stager.calls != 3 || res.Dispatched != 1 {
		t.Fatalf("recovered attempt calls=%d dispatched=%d, want 3 and 1\n%s",
			stager.calls, res.Dispatched, report.String())
	}
	log := report.String()
	for _, want := range []string{
		"1 consecutive infrastructure failure(s) and nothing dispatched; waiting 0s before the next attempt",
		"2 consecutive infrastructure failure(s) and nothing dispatched; waiting 0s before the next attempt",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("report missing %q:\n%s", want, log)
		}
	}
}

func TestConsumeStageFaultBackoffWaitsForCoordinatorRetraction(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-b")
	l, _ := stageFaultLoop(t, f, &recordingSteps{})
	l.mu.Lock()
	l.stageLaunch.Ensure()
	l.stageLaunch.Stage.MarkStart("item-b", "run-item-b-a1")
	l.stageLaunch.RecordFault("run-item-b-a1")
	l.infraStreak = 1
	l.mu.Unlock()

	if l.consumeStageFaultBackoff(0, 0) {
		t.Fatal("consumeStageFaultBackoff returned true while faulting run was still tracked by stage coordinator")
	}
	l.mu.Lock()
	stillRecorded := l.stageLaunch.HasFaults()
	l.stageLaunch.Launch.MarkStart("item-a", "run-item-a-a1")
	l.stageLaunch.Stage.MarkDone("item-b", "run-item-b-a1")
	l.mu.Unlock()
	if !stillRecorded {
		t.Fatal("consumeStageFaultBackoff cleared Faults while faulting run was still retracting")
	}

	if l.consumeStageFaultBackoff(0, 0) {
		t.Fatal("consumeStageFaultBackoff returned true while another run was active in launch coordinator")
	}

	l.mu.Lock()
	l.stageLaunch.Launch.MarkDone("item-a", "run-item-a-a1")
	l.stageLaunch.RecordFault("run-item-b-a1")
	l.mu.Unlock()

	if !l.consumeStageFaultBackoff(0, 0) {
		t.Fatal("consumeStageFaultBackoff returned false after retraction finished and loop became idle")
	}
	l.mu.Lock()
	hasFaults := l.stageLaunch.HasFaults()
	inBackoff := l.stageLaunch.Backoff
	l.mu.Unlock()
	if hasFaults || !inBackoff {
		t.Fatalf("after consume: hasFaults=%v backoff=%v, want false and true", hasFaults, inBackoff)
	}
}

func TestPassReplansInsteadOfLanesLeasedWhenCoordinatorCompletesMidPass(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-b")
	l, _ := stageFaultLoop(t, f, &recordingSteps{})
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.ExitWhenDrained = true
	if err := l.holdStageFailed(context.Background(), Decision{
		ItemID:     "item-b",
		LaneID:     "lane-1",
		RunRequest: &run.Request{ItemID: "item-b", RunID: "run-item-b-a1", LaneID: "lane-1"},
	}, "stage", errors.New("staging failure repeated unchanged: fork checkout")); err != nil {
		t.Fatalf("holdStageFailed: %v", err)
	}
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = 'run-item-b-a1', leased_until = NULL WHERE lane_id = 'lane-1'`, run.LaneStateLeased)
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	l.mu.Lock()
	l.stageLaunch.Ensure()
	l.stageLaunch.Stage.MarkStart("item-b", "run-item-b-a1")
	l.mu.Unlock()

	// Complete the coordinator's lane release + MarkDone right as plan() ends,
	// after passPlansMode has snapshotted the still-leased lane and before
	// pass() checks stageLaunchBusy / planNothing.
	sawPlan := false
	retracted := false
	baseNow := l.cfg.Now
	l.cfg.Now = func() time.Time {
		if !sawPlan && strings.Contains(string(debug.Stack()), "passPlansMode") {
			sawPlan = true
		}
		return baseNow()
	}
	l.passTimer = newLoopPassTimer(func() time.Time {
		if sawPlan && !retracted {
			retracted = true
			st, openErr := store.Open(context.Background(), f.storePath, true)
			if openErr != nil {
				t.Errorf("open store to release lane: %v", openErr)
			} else {
				if txErr := st.WriteTx(context.Background(), func(tx *store.Tx) error {
					_, execErr := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = 'lane-1'`, run.LaneStateIdle)
					return execErr
				}); txErr != nil {
					t.Errorf("release lane: %v", txErr)
				}
				st.Close()
			}
			l.mu.Lock()
			l.stageLaunch.Stage.MarkDone("item-b", "run-item-b-a1")
			l.mu.Unlock()
		}
		return baseNow()
	})

	var res LoopResult
	stop, err := l.pass(context.Background(), &res)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !retracted {
		t.Fatal("test hook did not fire after plan phase")
	}
	if stop != "" {
		t.Fatalf("first pass stop = %q (detail=%q), want empty string to re-plan after mid-pass coordinator completion", stop, res.Detail)
	}

	l.passTimer = nil
	stop, err = l.pass(context.Background(), &res)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if stop != StopQueueDrained {
		t.Fatalf("second pass stop = %q (detail=%q), want %q", stop, res.Detail, StopQueueDrained)
	}
}

// The mid-pass dispatch pause is for a staging fault an earlier pass's claim recorded: one
// still retracting when this pass began, or, under the stage coordinator, one for a run
// this pass did not claim. A fault the pass's own batch records is not that: the rest of
// the batch still stages and counts, and the backoff waits for the pass to end.
func TestMidPassStageFaultPauseOnlyForFaultsRetractingAtPassStart(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-b")
	l, _ := stageFaultLoop(t, f, &recordingSteps{})
	l.mu.Lock()
	l.stageLaunch.Ensure()
	l.stageLaunch.Stage.MarkStart("item-b", "run-item-b-a1")
	l.stageLaunch.RecordFault("run-item-b-a1")
	l.infraStreak = 1
	retracting := l.retractingStageFaultsLocked()
	l.mu.Unlock()
	if !retracting["run-item-b-a1"] || len(retracting) != 1 {
		t.Fatalf("retracting at pass start = %v, want only run-item-b-a1", retracting)
	}
	if l.retractedStageFaultBackoffReady(retracting, nil) {
		t.Fatal("paused while the carried fault's claim was still retracting")
	}
	l.mu.Lock()
	l.stageLaunch.Stage.MarkDone("item-b", "run-item-b-a1")
	l.mu.Unlock()
	if !l.retractedStageFaultBackoffReady(retracting, nil) {
		t.Fatal("did not pause once the carried fault's retraction finished and the loop was idle")
	}

	// A fault recorded by this pass's own dispatch, with nothing retracting at its start.
	l.mu.Lock()
	l.stageLaunch.ClearFaults()
	atStart := l.retractingStageFaultsLocked()
	l.stageLaunch.RecordFault("run-item-a-a1")
	idle := l.stageFaultBackoffReadyLocked()
	l.mu.Unlock()
	if !idle {
		t.Fatal("control: the loop should be idle with a recorded fault")
	}
	if l.retractedStageFaultBackoffReady(atStart, nil) {
		t.Fatal("a fault the pass's own batch recorded stopped the rest of the batch")
	}

	// While the stage coordinator stages claims outside the pass, a fault for a run this
	// pass did not claim was an earlier pass's claim, recorded and retracted after the pass
	// took its snapshot; one for a run this pass claimed is its own batch's.
	l.mu.Lock()
	l.stageLaunch.Stage.Active = true
	l.mu.Unlock()
	if !l.retractedStageFaultBackoffReady(atStart, nil) {
		t.Fatal("did not pause for an earlier claim's fault the coordinator recorded after the pass began")
	}
	if l.retractedStageFaultBackoffReady(atStart, map[string]bool{"run-item-a-a1": true}) {
		t.Fatal("a fault for a run this pass claimed stopped the rest of the batch under the coordinator")
	}
}

type gatedFailingStager struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
	errText string
}

func (s *gatedFailingStager) Stage(ctx context.Context, _ StageSpec) error {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	if first {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.New(s.errText)
}

type cancelOnLineReport struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	cancelOn string
	cancel   context.CancelFunc
}

func (r *cancelOnLineReport) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.buf.Write(p)
	hit := strings.Contains(string(p), r.cancelOn)
	r.mu.Unlock()
	if hit {
		r.cancel()
	}
	return len(p), nil
}

func (r *cancelOnLineReport) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func callerHasFrame(needle string) bool {
	var pcs [16]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, needle) {
			return true
		}
		if !more {
			return false
		}
	}
}

// A staging infrastructure failure that completes between passes (after the prior pass's
// HasFaults check and before the next pass dispatches) or mid-pass before plan/apply must
// still run the first-streak backoff before the item can be dispatched a second time.
// Otherwise the second attempt hits the repeated-unchanged stage-failed hold and skips the
// first backoff delay.
func TestIdleStageFaultBeforeNextPassStillBacksOffFirst(t *testing.T) {
	cases := []struct {
		name  string
		match func() bool
	}{
		{
			name: "between_passes",
			match: func() bool {
				return callerHasFrame("(*Loop).recordProcessCPUIfDue")
			},
		},
		{
			name: "mid_pass_before_plan",
			match: func() bool {
				return callerHasFrame("(*Loop).passPlansMode")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefusalLoopFixture(t, "item-b")
			t.Setenv("TILLR_LOOP_STORE", "")
			holdWALKeeper(t, f.storePath)

			stager := &gatedFailingStager{
				entered: make(chan struct{}),
				release: make(chan struct{}),
				errText: "the brief could not be rendered",
			}
			l := f.loop(t, &recordingSteps{}, stager)
			l.cfg.Lanes = []string{"lane-1"}
			l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
			l.cfg.BackoffBase = 20 * time.Millisecond

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			report := &cancelOnLineReport{
				cancelOn: "nothing dispatched; waiting 20ms before the next attempt",
				cancel:   cancel,
			}
			l.cfg.Report = report

			baseNow := l.cfg.Now
			var releasedOnce sync.Once
			l.cfg.Now = func() time.Time {
				select {
				case <-stager.entered:
					if tc.match() && !callerHasFrame("(*Loop).cancelStaging") {
						releasedOnce.Do(func() {
							close(stager.release)
							deadline := time.Now().Add(hangGuard)
							for time.Now().Before(deadline) {
								l.mu.Lock()
								done := l.stageLaunch.HasFaults() && len(l.stageLaunch.Stage.Runs) == 0
								l.mu.Unlock()
								if done {
									return
								}
								select {
								case <-ctx.Done():
									return
								case <-time.After(time.Millisecond):
								}
							}
						})
					}
				default:
				}
				return baseNow()
			}

			res, err := l.Run(ctx)
			if err != nil {
				t.Fatalf("loop failed: %v\n%s", err, report.String())
			}
			if res.Stop != StopOperatorStop {
				t.Fatalf("loop stopped %q (%s), want %q\n%s", res.Stop, res.Detail, StopOperatorStop, report.String())
			}
			if !strings.Contains(res.Detail, "waiting out an infrastructure failure") {
				t.Fatalf("stop detail = %q, want interrupted infrastructure backoff", res.Detail)
			}
			stager.mu.Lock()
			calls := stager.calls
			stager.mu.Unlock()
			if calls != 1 {
				t.Fatalf("stager calls = %d, want 1 before first backoff\n%s", calls, report.String())
			}
		})
	}
}
