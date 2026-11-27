package drain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
