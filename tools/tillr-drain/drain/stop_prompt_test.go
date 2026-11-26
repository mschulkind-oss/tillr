package drain

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func prepareReadyCandidate(t *testing.T, f refusalLoopFixture, l *Loop, itemID string) Candidate {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]' WHERE item_id=?`, itemID)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	var planned *plannedApplication
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == itemID {
			copy := p
			planned = &copy
			break
		}
	}
	if planned == nil {
		t.Fatalf("no plan for %s", itemID)
	}
	d := planned.decision
	ok, _, err := l.applyPlan(context.Background(), *planned, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %s: %v/%v", itemID, ok, err)
	}
	path := l.lanePath(d.LaneID)
	writeForkFile(t, filepath.Join(path, "candidate.txt"), itemID+"\n")
	runGit(t, path, "add", "candidate.txt")
	runGit(t, path, "commit", "-qm", "candidate "+itemID)
	head := runGit(t, path, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "candidate.txt\n")
	c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: path, BaseSHA: base, HeadSHA: head}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture",
			State: run.StateExited, ExitCode: countPtr(0),
			StartedAt:    l.cfg.Now().Format(time.RFC3339),
			FinishedAt:   l.cfg.Now().Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})
	return c
}

func requestSessionStop(t *testing.T, storePath string, flag int) {
	t.Helper()
	s, err := store.Open(context.Background(), storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		res, err := tx.Exec(`UPDATE drain_sessions SET stop_requested = ? WHERE ended_at IS NULL`, flag)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("updated %d open sessions, want 1", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type blockingGateSteps struct {
	recordingSteps
	started chan Candidate
	release chan struct{}
	mu      sync.Mutex
	gated   []Candidate
	landed  []Candidate
}

func (s *blockingGateSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	first := len(s.gated) == 0
	s.gated = append(s.gated, c)
	s.mu.Unlock()
	if first {
		close(s.started)
		select {
		case <-ctx.Done():
			return GateOutcome{}, ctx.Err()
		case <-s.release:
		}
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *blockingGateSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	s.mu.Lock()
	s.landed = append(s.landed, c)
	s.mu.Unlock()
	return LandOutcome{Result: LandLanded}, nil
}

// A stop requested in drain_sessions while a gate run is in progress cancels the
// gate without waiting for it to finish, records no verdict or harness fault on
// the candidate, and leaves the candidate ready for the next loop start to gate
// and land. Heartbeat observation uses an injected clock and manual tick channel.
func TestStopDuringGateRunEndsPromptlyAndNextLoopResumesCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a")
	c := prepareReadyCandidate(t, f, l, "item-a")
	clock := &manualClock{now: l.cfg.Now(), ticks: make(chan time.Time)}
	steps := &blockingGateSteps{started: make(chan Candidate), release: make(chan struct{})}
	l.cfg.Now = clock.Now
	l.heartbeatClock = clock.heartbeat
	l.cfg.Steps = steps

	type runOutcome struct {
		result LoopResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		res, err := l.Run(context.Background())
		done <- runOutcome{res, err}
	}()
	<-steps.started

	requestSessionStop(t, f.storePath, 1)
	clock.advance(PassInterval)
	var out runOutcome
	select {
	case clock.ticks <- clock.Now():
		out = <-done
	case out = <-done:
	}
	if out.err != nil || out.result.Stop != StopOperatorStop || out.result.Landed != 0 {
		t.Fatalf("stopped loop answered %+v / %v, want operator-stop with 0 landed", out.result, out.err)
	}

	s := openFixture(t, f.storePath)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if fault, held := faults["item-a"]; held {
			t.Fatalf("interrupted gate faulted the item: %+v", fault)
		}
		var verdicts, noVerdict int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, GateVerdictKind).Scan(&verdicts); err != nil {
			return err
		}
		if verdicts != 0 {
			t.Fatalf("interrupted gate recorded %d gate.verdict judgment(s), want 0", verdicts)
		}
		if err := r.QueryRow(`SELECT count(*) FROM dispatches WHERE action=? AND reason=?`, ActionHold, ReasonGateNoVerdict).Scan(&noVerdict); err != nil {
			return err
		}
		if noVerdict != 0 {
			t.Fatalf("interrupted gate recorded %d %s hold(s), want 0", noVerdict, ReasonGateNoVerdict)
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		work := works["item-a"]
		if work == nil || work.Done || work.Examining || work.Candidate == nil || work.Candidate.RunID != c.RunID {
			t.Fatalf("pending work after stop = %+v, want unexamined ready candidate %s", work, c.RunID)
		}
		return nil
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	close(steps.release)
	next, err := NewLoop(l.cfg)
	if err != nil {
		t.Fatal(err)
	}
	next.heartbeatClock = nil
	resumed, err := next.Run(context.Background())
	if err != nil || resumed.Stop != StopQueueDrained || resumed.Landed != 1 {
		t.Fatalf("resumed loop answered %+v / %v, want queue-drained with 1 landed", resumed, err)
	}
	steps.mu.Lock()
	defer steps.mu.Unlock()
	if len(steps.gated) != 2 || steps.gated[1].RunID != c.RunID || len(steps.landed) != 1 || steps.landed[0].RunID != c.RunID {
		t.Fatalf("gated=%+v landed=%+v, want same candidate gated again and landed once", steps.gated, steps.landed)
	}
}

// ProcessSteps.Gate spawns wrappers that place checks in separate process groups
// and sessions while inheriting stdout/stderr pipes. Canceling the gate's context
// must reap the entire descendant tree so Gate returns ctx.Err() without waiting
// for those background sessions or writing a bogus gate.verdict.
func TestProcessStepsGateStopEndsSeparateSessionDescendantsAndRecordsNoVerdict(t *testing.T) {
	f, l := passFixture(t, "item-a")
	c := prepareReadyCandidate(t, f, l, "item-a")
	work := scratchUnderDurable(t)
	ready := filepath.Join(work, "ready")
	pidsFile := filepath.Join(work, "pids")

	gateProg := executable(t, `exec python3 - <<'PY'
import os, pathlib, signal, subprocess, time
group = subprocess.Popen(
    ['python3', '-c', '''
import os, pathlib, subprocess, time
leaf = subprocess.Popen(["sleep", "300"], start_new_session=True)
pathlib.Path("`+pidsFile+`.part").write_text(f"{os.getpid()} {leaf.pid}\\n")
os.replace("`+pidsFile+`.part", "`+pidsFile+`")
pathlib.Path("`+ready+`").touch()
leaf.wait()
'''],
    start_new_session=True,
)
group.wait()
PY`)
	steps := &ProcessSteps{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: l.cfg.Now,
		GateProgram: gateProg,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var got GateOutcome
	go func() {
		var err error
		got, err = steps.Gate(ctx, c)
		done <- err
	}()
	waitForPath(t, ready, done)
	raw, err := os.ReadFile(pidsFile)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(raw))
	var pids []int
	for _, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		if !processAlive(pid) {
			t.Fatalf("descendant %d not alive before cancel", pid)
		}
	}

	cancel()
	err = <-done
	if err == nil || ctx.Err() == nil {
		t.Fatalf("canceled Gate answered %+v, %v; want context cancellation error", got, err)
	}
	for _, pid := range pids {
		if processAlive(pid) {
			t.Fatalf("separate-session gate descendant %d survived cancellation", pid)
		}
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var verdicts, stopped int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, GateVerdictKind).Scan(&verdicts); err != nil {
			return err
		}
		if verdicts != 0 {
			t.Fatalf("canceled Gate recorded %d gate.verdict row(s), want 0", verdicts)
		}
		var reason string
		if err := r.QueryRow(`SELECT count(*), COALESCE(MAX(json_extract(payload, '$.reason')), '') FROM events WHERE kind=? AND object_type='run' AND object_id=?`, GateStoppedKind, c.RunID).Scan(&stopped, &reason); err != nil {
			return err
		}
		if stopped != 1 || reason != "cancelled" {
			t.Fatalf("canceled Gate recorded gate.stopped count=%d reason=%q, want 1 / cancelled", stopped, reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Repeated SIGTERMs (for example when both awaitDetachedGate and the caller's
// stop path call stopDetachedGate concurrently on loop shutdown) must not kill
// the gate supervisor with default SIGTERM disposition before finishHeavyTree
// has killed and reaped its separate-session descendants.
func TestDetachedGateSupervisorRepeatedSIGTERMReapsSeparateSessionDescendants(t *testing.T) {
	f, l := passFixture(t, "item-a")
	c := prepareReadyCandidate(t, f, l, "item-a")
	work := scratchUnderDurable(t)
	ready := filepath.Join(work, "ready")
	pidsFile := filepath.Join(work, "pids")

	gateProg := executable(t, `exec python3 - <<'PY'
import os, pathlib, subprocess
group = subprocess.Popen(
    ['python3', '-c', '''
import os, pathlib, subprocess
leaf = subprocess.Popen(["sleep", "300"], start_new_session=True)
pathlib.Path("`+pidsFile+`.part").write_text(f"{os.getpid()} {leaf.pid}\\n")
os.replace("`+pidsFile+`.part", "`+pidsFile+`")
pathlib.Path("`+ready+`").touch()
leaf.wait()
'''],
    start_new_session=True,
)
group.wait()
PY`)
	steps := &ProcessSteps{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: l.cfg.Now,
		GateProgram: gateProg,
	}
	handle, err := steps.startOrAttachDetachedGate(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { steps.stopDetachedGate(handle) })

	waitForPath[error](t, ready, nil)
	raw, err := os.ReadFile(pidsFile)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		if !processAlive(pid) {
			t.Fatalf("descendant %d not alive before stop", pid)
		}
	}

	stopSpam := make(chan struct{})
	spamDone := make(chan struct{})
	go func() {
		defer close(spamDone)
		for {
			select {
			case <-stopSpam:
				return
			default:
				if running, err := heavySupervisorRunning(handle.face.PID, handle.face.StartTicks); err != nil || !running {
					return
				}
				_ = syscall.Kill(handle.face.PID, syscall.SIGTERM)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	steps.stopDetachedGate(handle)
	close(stopSpam)
	<-spamDone

	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("separate-session gate descendant %d survived repeated SIGTERM during stop", pid)
		}
	}
}

type blockingRollingSteps struct {
	recordingSteps
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	runs    int
}

func (s *blockingRollingSteps) RollingConfigured() bool { return true }

func (s *blockingRollingSteps) Rolling(ctx context.Context, _, _ string) (RollingOutcome, error) {
	s.mu.Lock()
	s.runs++
	first := s.runs == 1
	s.mu.Unlock()
	if first {
		close(s.started)
		select {
		case <-ctx.Done():
			return RollingOutcome{}, ctx.Err()
		case <-s.release:
		}
	}
	return RollingOutcome{Green: true, Known: true, ExitCode: 0}, nil
}

// A stop requested while a rolling trunk proof run is in progress ends the loop
// with operator-stop without waiting for the proof to finish or recording an
// unknown rolling run, and the next loop start runs that proof and advances the
// rolling watermark.
func TestStopDuringRollingProofEndsPromptlyAndNextLoopResumesProof(t *testing.T) {
	f, l := passFixture(t)
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	clock := &manualClock{now: l.cfg.Now(), ticks: make(chan time.Time)}
	steps := &blockingRollingSteps{started: make(chan struct{}), release: make(chan struct{})}
	l.cfg.Now = clock.Now
	l.heartbeatClock = clock.heartbeat
	l.cfg.Steps = steps

	type runOutcome struct {
		result LoopResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		res, err := l.Run(context.Background())
		done <- runOutcome{res, err}
	}()
	<-steps.started

	requestSessionStop(t, f.storePath, 1)
	clock.advance(PassInterval)
	clock.ticks <- clock.Now()

	out := <-done
	if out.err != nil || out.result.Stop != StopOperatorStop {
		t.Fatalf("stopped rolling loop answered %+v / %v, want operator-stop", out.result, out.err)
	}

	s := openFixture(t, f.storePath)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var rows int
		if err := r.QueryRow(`SELECT count(*) FROM rolling_runs`).Scan(&rows); err != nil {
			return err
		}
		if rows != 0 {
			t.Fatalf("canceled rolling proof recorded %d rolling_runs row(s), want 0", rows)
		}
		return nil
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	// Verify ProcessSteps.Rolling itself returns ctx.Err() on cancellation rather than Known=false.
	ready := filepath.Join(scratchUnderDurable(t), "rolling-ready")
	procSteps := &ProcessSteps{
		Root: f.root, LaneRoot: f.laneRoot,
		RollingProgram: executable(t, "touch "+ready+"; sleep 300"),
	}
	rollCtx, rollCancel := context.WithCancel(context.Background())
	rollDone := make(chan error, 1)
	go func() {
		_, err := procSteps.Rolling(rollCtx, "", tip)
		rollDone <- err
	}()
	waitForPath(t, ready, rollDone)
	rollCancel()
	if err := <-rollDone; err == nil {
		t.Fatal("canceled ProcessSteps.Rolling returned nil error instead of ctx.Err()")
	}

	close(steps.release)
	next, err := NewLoop(l.cfg)
	if err != nil {
		t.Fatal(err)
	}
	next.heartbeatClock = nil
	resumed, err := next.Run(context.Background())
	if err != nil || resumed.Stop != StopQueueDrained || resumed.RollingGreen != 1 {
		t.Fatalf("resumed rolling loop answered %+v / %v, want queue-drained with RollingGreen=1", resumed, err)
	}
	state, err := next.loadRollingState(context.Background())
	if err != nil || state.WatermarkSHA != tip || state.LastOutcome != "green" {
		t.Fatalf("rolling state after resume = %+v / %v, want green at %s", state, err, tip)
	}
}

type heavyProofSteps struct {
	recordingSteps
	judge func(context.Context, string, string) ([]HeavyCheck, error)
}

func (s *heavyProofSteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	return s.judge(ctx, dir, only)
}

// A stop requested while an after-merge heavy proof run is in progress ends the
// loop promptly with operator-stop without waiting for the in-flight check, and
// the next loop start resumes the proof reusing checks already completed.
func TestStopDuringHeavyProofEndsPromptlyAndNextLoopReusesCompletedChecks(t *testing.T) {
	f, l := passFixture(t)
	checksDir := filepath.Join(f.root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		writeForkFile(t, filepath.Join(checksDir, id+".json"), `{"id":"`+id+`","scope":"tree","tier":"heavy"}`)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "declare heavy checks")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	clock := &manualClock{now: l.cfg.Now(), ticks: make(chan time.Time)}
	gateProg := executable(t, `id=all
while [ "$#" -gt 0 ]; do
 if [ "$1" = --only ]; then shift; id=$1; fi
 shift
done
echo "$id" >> `+filepath.Join(f.laneRoot, "heavy-calls")+`
if [ "$id" = b ] && [ ! -e `+filepath.Join(f.laneRoot, "entered-b")+` ]; then
 touch `+filepath.Join(f.laneRoot, "entered-b")+`
 sleep 300
fi
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"pass"}]}}\n' "$id"`)

	procSteps := &ProcessSteps{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: clock.Now,
		HeavyGateBin: gateProg,
	}
	var report bytes.Buffer
	cfg := l.cfg
	cfg.Now = clock.Now
	cfg.Report = &report
	cfg.Steps = &heavyProofSteps{judge: procSteps.JudgeHeavy}
	first, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	first.gatePolicy.LandingStrategy = "after-merge"
	first.heartbeatClock = clock.heartbeat

	type runOutcome struct {
		result LoopResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		res, err := first.Run(context.Background())
		done <- runOutcome{res, err}
	}()
	waitForPath(t, filepath.Join(f.laneRoot, "entered-b"), done)

	requestSessionStop(t, f.storePath, 1)
	clock.advance(PassInterval)
	var out runOutcome
	select {
	case clock.ticks <- clock.Now():
		out = <-done
	case out = <-done:
	}
	if out.err != nil || out.result.Stop != StopOperatorStop {
		t.Fatalf("stopped heavy loop answered %+v / %v, want operator-stop\n%s", out.result, out.err, report.String())
	}
	if strings.Contains(report.String(), "runner fault") {
		t.Fatalf("stopped heavy loop reported a runner fault:\n%s", report.String())
	}

	report.Reset()
	cfg.Now = time.Now
	next, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next.gatePolicy.LandingStrategy = "after-merge"
	resumed, err := next.Run(context.Background())
	if err != nil || resumed.Stop != StopQueueDrained {
		t.Fatalf("resumed heavy loop answered %+v / %v\n%s", resumed, err, report.String())
	}
	raw, err := os.ReadFile(filepath.Join(f.laneRoot, "heavy-calls"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "a\nb\nb\n" || !strings.Contains(report.String(), "reused checks=a") {
		t.Fatalf("heavy calls=%q report=%s", raw, report.String())
	}
	status, err := ReadHeavyStatus(context.Background(), f.root, f.storePath)
	if err != nil || status.GreenCommit != tip {
		t.Fatalf("heavy status=%+v / %v, want green at %s", status, err, tip)
	}
}

type stopMidPassStager struct {
	t          *testing.T
	storePath  string
	beforeStop func()
}

func (s stopMidPassStager) Stage(_ context.Context, _ StageSpec) error {
	// Request stop mid-pass while staging the dispatch, before Run launches,
	// without advancing the heartbeat ticker.
	if s.beforeStop != nil {
		s.beforeStop()
	}
	requestSessionStop(s.t, s.storePath, 1)
	return nil
}

type failIfRunSteps struct {
	recordingSteps
	ran bool
}

func (s *failIfRunSteps) Run(context.Context, run.Request) (run.Record, string, error) {
	s.ran = true
	return run.Record{}, "", nil
}

// A stop requested mid-pass between steps is noticed during the pass without
// waiting for a heartbeat tick or starting subsequent steps, and the next loop
// start picks up the claimed dispatch cleanly.
func TestStopMidPassBetweenStepsEndsBeforeLaunchingNextStep(t *testing.T) {
	f, l := passFixture(t, "item-a")
	clock := &manualClock{now: l.cfg.Now(), ticks: make(chan time.Time)}
	steps := &failIfRunSteps{}
	l.cfg.Now = clock.Now
	l.heartbeatClock = clock.heartbeat
	l.cfg.Steps = steps
	l.cfg.Stage = stopMidPassStager{
		t:         t,
		storePath: f.storePath,
	}

	res, err := l.Run(context.Background())
	if err != nil || res.Stop != StopOperatorStop {
		t.Fatalf("mid-pass stop answered %+v / %v, want operator-stop", res, err)
	}
	if steps.ran {
		t.Fatal("mid-pass stop still launched Steps.Run after stop_requested was set")
	}

	// Next loop releases the unlaunched claim and dispatches the item cleanly.
	runCtx, cancelNext := context.WithCancel(context.Background())
	defer cancelNext()
	nextCfg := l.cfg
	nextCfg.Stage = &itemContentStager{}
	nextSteps := &startupHeldSteps{cancel: cancelNext}
	nextCfg.Steps = nextSteps
	next, err := NewLoop(nextCfg)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := next.Run(runCtx)
	if err != nil || resumed.Stop != StopOperatorStop || resumed.Dispatched != 1 || !nextSteps.ran("item-a") {
		t.Fatalf("next loop after mid-pass stop answered %+v / %v, want Dispatched=1 for item-a", resumed, err)
	}
}

// Reproduces the ref-lock failure when a mid-pass stop cancels the loop's context
// while observeTrunk's `git update-ref refs/tillr/trunk-history/observed` is holding
// the ref lock: killing `git update-ref` mid-lock leaves observed.lock stale on disk,
// so the next loop's observeTrunk fails with a lockfile error.
func TestStopMidPassWhileUpdateRefHoldsLockLeavesNextLoopClean(t *testing.T) {
	f, l := passFixture(t, "item-a")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := scratchUnderDurable(t)
	once := filepath.Join(bin, "once")
	inFIFO := filepath.Join(bin, "update-ref.in")
	outFIFO := filepath.Join(bin, "update-ref.out")
	if err := syscall.Mkfifo(inFIFO, 0600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(outFIFO, 0600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-C" ] && [ "$2" = %q ] && [ "$3" = "update-ref" ] && [ "$4" = %q ] && [ ! -f %q ]; then
	: > %q
	exec %q -C "$2" update-ref --stdin < %q > %q
fi
exec %q "$@"
`, f.root, observedTrunkRef, once, once, realGit, inFIFO, outFIFO, realGit)
	if err := writeForkSafe(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	prepared := make(chan struct{})
	driverDone := make(chan error, 1)
	go func() {
		in, err := os.OpenFile(inFIFO, os.O_WRONLY, 0)
		if err != nil {
			driverDone <- err
			return
		}
		out, err := os.OpenFile(outFIFO, os.O_RDONLY, 0)
		if err != nil {
			_ = in.Close()
			driverDone <- err
			return
		}
		defer out.Close()
		if _, err := fmt.Fprintf(in, "start\nupdate %s %s\nprepare\n", observedTrunkRef, tip); err != nil {
			_ = in.Close()
			driverDone <- err
			return
		}
		sc := bufio.NewScanner(out)
		gotPrepare := false
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "prepare: ok" {
				gotPrepare = true
				break
			}
		}
		if !gotPrepare {
			_ = in.Close()
			driverDone <- fmt.Errorf("git update-ref --stdin exited before prepare: ok (%v)", sc.Err())
			return
		}
		close(prepared)
		l.mu.Lock()
		loopCtx := l.loopCtx
		l.mu.Unlock()
		if loopCtx != nil {
			<-loopCtx.Done()
		}
		if _, err := fmt.Fprintln(in, "commit"); err != nil {
			_ = in.Close()
			driverDone <- fmt.Errorf("commit after stop: %w", err)
			return
		}
		_ = in.Close()
		gotCommit := false
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "commit: ok" {
				gotCommit = true
			}
		}
		if sc.Err() != nil {
			driverDone <- sc.Err()
			return
		}
		if !gotCommit {
			driverDone <- fmt.Errorf("git update-ref --stdin exited before commit: ok")
			return
		}
		driverDone <- nil
	}()

	clock := &manualClock{now: l.cfg.Now(), ticks: make(chan time.Time)}
	steps := &failIfRunSteps{}
	l.cfg.Now = clock.Now
	l.heartbeatClock = clock.heartbeat
	l.cfg.Steps = steps
	l.cfg.Stage = stopMidPassStager{
		t:         t,
		storePath: f.storePath,
		beforeStop: func() {
			<-prepared
		},
	}

	res, err := l.Run(context.Background())
	if err != nil || res.Stop != StopOperatorStop {
		t.Fatalf("mid-pass stop answered %+v / %v, want operator-stop", res, err)
	}
	if err := <-driverDone; err != nil {
		t.Fatalf("update-ref driver: %v", err)
	}
	if steps.ran {
		t.Fatal("mid-pass stop still launched Steps.Run after stop_requested was set")
	}

	runCtx, cancelNext := context.WithCancel(context.Background())
	defer cancelNext()
	nextCfg := l.cfg
	nextCfg.Stage = &itemContentStager{}
	nextSteps := &startupHeldSteps{cancel: cancelNext}
	nextCfg.Steps = nextSteps
	next, err := NewLoop(nextCfg)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := next.Run(runCtx)
	if err != nil || resumed.Stop != StopOperatorStop || resumed.Dispatched != 1 || !nextSteps.ran("item-a") {
		t.Fatalf("next loop after mid-pass stop answered %+v / %v, want Dispatched=1 for item-a", resumed, err)
	}
}
