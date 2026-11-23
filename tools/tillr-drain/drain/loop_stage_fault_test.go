package drain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

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
	if s.calls == 1 {
		// Force startView to sync from SQLite while the claim is live, before
		// cancelStaging retracts it. Under high load, this split refresh
		// happens across startViewWatchBatch and leaves storeLanes holding the
		// lane unless pending-history events trigger a lane lease refresh.
		if v := s.loop.startView.Load(); v != nil {
			for !v.snapshot().storeLanes[spec.LaneID] {
				select {
				case v.laneTargetChanged <- struct{}{}:
				default:
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				runtime.Gosched()
			}
		}
	}
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
