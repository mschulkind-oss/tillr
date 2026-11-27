package drain

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type restartHarness struct {
	f      refusalLoopFixture
	l      *Loop
	execed []string
	env    []string
}

func newRestartHarness(t *testing.T, onDisk []byte, check func(context.Context, string) error, items ...string) *restartHarness {
	t.Helper()
	if len(items) == 0 {
		items = []string{"item-a"}
	} else if items[0] == "" {
		items = nil
	}
	f, l := passFixture(t, items...)
	h := &restartHarness{f: f, l: l}
	exe := filepath.Join(t.TempDir(), "tillr-drain")
	if onDisk == nil {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if onDisk, err = os.ReadFile(self); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeForkSafe(exe, onDisk, 0o755); err != nil {
		t.Fatal(err)
	}
	l.cfg.Restart = &RestartConfig{
		Executable: exe, Argv: []string{"tillr-drain", "loop", "--root", f.root},
		Check: check,
		Exec: func(path string, argv, env []string) error {
			h.execed, h.env = argv, env
			return nil
		},
	}
	return h
}

func (h *restartHarness) request(t *testing.T, n int) []restart.Request {
	t.Helper()
	var out []restart.Request
	for i := 0; i < n; i++ {
		r, err := restart.File(context.Background(), h.f.storePath, restart.RoleDrain, time.Now().Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (h *restartHarness) answer(t *testing.T, id string) restart.Answer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a, err := restart.AwaitAnswer(ctx, h.f.storePath, id, time.Millisecond)
	if err != nil {
		t.Fatalf("request %s unanswered: %v", id, err)
	}
	return a
}

func dispatches(t *testing.T, f refusalLoopFixture) int {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var n int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRestartedAnswerReplacesTheProcessBeforeAnyDispatch(t *testing.T) {
	h := newRestartHarness(t, []byte("a different build"), func(context.Context, string) error { return nil })
	req := h.request(t, 1)[0]
	stop, err := h.l.pass(context.Background(), &LoopResult{})
	if err != nil || stop != StopRestarted {
		t.Fatalf("pass = %q, %v; want %q", stop, err, StopRestarted)
	}
	if got := dispatches(t, h.f); got != 0 {
		t.Fatalf("a pending request let %d decision(s) through", got)
	}
	if strings.Join(h.execed, " ") != "tillr-drain loop --root "+h.f.root {
		t.Fatalf("exec argv = %v", h.execed)
	}
	if !containsEnv(h.env, restart.RequestEnv+"="+req.ID) {
		t.Fatalf("exec env does not carry the request: %v", h.env)
	}
	// The replacement's first act answers the request, naming both builds.
	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := h.l.adoptRestart(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := h.answer(t, req.ID)
	if a.Answer != restart.AnswerRestarted || a.FromBuild != "old-build" || a.ToBuild != h.l.build.String() || a.ToDigest != h.l.build.Digest {
		t.Fatalf("answer = %+v", a)
	}
}

type restartWhileSettlingSteps struct {
	*settlingStartSteps
	worker     *exec.Cmd
	workerDone chan error
	landed     chan struct{}
	phase      string
}

func (s *restartWhileSettlingSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if s.phase == "gate" {
		return s.settlingStartSteps.Gate(ctx, c)
	}
	return s.recordingSteps.Gate(ctx, c)
}

func (s *restartWhileSettlingSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if s.phase == "land" && c.ItemID == "item-a" {
		close(s.settling)
		select {
		case <-s.release:
		case <-ctx.Done():
			return LandOutcome{}, ctx.Err()
		}
	}
	if c.ItemID == "item-a" {
		defer close(s.landed)
	}
	return s.recordingSteps.Land(ctx, c)
}

func (s *restartWhileSettlingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.ItemID == "item-a" {
		return s.settlingStartSteps.Run(ctx, req)
	}
	// Exercise the real process handoff. There is no CommandContext: the worker
	// outlives the old loop's wait, just as it does in ProcessSteps.Run.
	p := &ProcessSteps{Store: s.fixture.storePath}
	if err := p.startRunFaceContext(ctx, s.worker, req); err != nil {
		return run.Record{}, "", err
	}
	go func() { s.workerDone <- s.worker.Wait() }()
	s.started <- req.LaneID
	<-ctx.Done()
	return run.Record{}, "", ctx.Err()
}

func TestPendingRestartStopsWorkerStartWhileSettlementDrains(t *testing.T) {
	for _, phase := range []string{"gate", "land"} {
		t.Run(phase, func(t *testing.T) { testWorkerStartWhileRestartWaits(t, phase, nil) })
	}
}

func TestPendingRestartWaitsForDelayedReplacementAfterGate(t *testing.T) {
	delayed := false
	testWorkerStartWhileRestartWaits(t, "gate", func(ctx context.Context) {
		delayed = true
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	})
	if !delayed {
		t.Fatal("injected replacement delay did not run")
	}
}

func testWorkerStartWhileRestartWaits(t *testing.T, phase string, delayReplacement func(context.Context)) {
	t.Helper()
	t.Setenv("TILLR_LOOP_STORE", "")
	f := newRefusalLoopFixtureWithLanes(t, 2, "item-a")
	worker := exec.Command("sh", "-c", `read -r line && test "$line" = survived`)
	input, err := worker.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	steps := &restartWhileSettlingSteps{
		worker: worker, workerDone: make(chan error, 1), landed: make(chan struct{}), phase: phase,
		settlingStartSteps: &settlingStartSteps{
			fixture: f, now: time.Now, settling: make(chan struct{}), release: make(chan struct{}), started: make(chan string, 1),
		},
	}
	l := f.loop(t, steps, settlingStartStager{f.laneRoot})
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
	l.cfg.Report = io.Discard
	holdWALKeeper(t, f.storePath)
	exe := filepath.Join(t.TempDir(), "tillr-drain")
	if err := writeForkSafe(exe, []byte("replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	l.cfg.Restart = &RestartConfig{Executable: exe,
		Check: func(ctx context.Context, _ string) error {
			if delayReplacement != nil {
				delayReplacement(ctx)
			}
			return nil
		},
		Exec: func(string, []string, []string) error {
			return nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result LoopResult
	var loopErr error
	go func() { defer close(done); result, loopErr = l.Run(ctx) }()
	releaseClosed := false
	closeRelease := func() {
		if !releaseClosed {
			releaseClosed = true
			close(steps.release)
		}
	}
	defer func() {
		closeRelease()
		cancel()
		select {
		case <-done:
		case <-time.After(hangGuard):
			t.Error("loop did not stop after cancellation")
		}
	}()
	select {
	case <-steps.settling:
	case <-done:
		t.Fatalf("loop stopped before %s entry: %+v, %v", phase, result, loopErr)
	case <-time.After(hangGuard):
		t.Fatalf("item-a did not enter %s", phase)
	}
	// Under D-SN2, gate and land execution do not hold restartMechanical;
	// hold a brief transition lock to verify a pending restart still blocks
	// new worker starts while a short WriteTx / cmd.Start hold drains.
	_, releaseTransition, err := l.lockRestartSettlement(ctx)
	if err != nil {
		t.Fatalf("lockRestartSettlement during %s: %v", phase, err)
	}
	transitionReleased := false
	defer func() {
		if !transitionReleased {
			releaseTransition()
		}
	}()
	req, err := restart.File(ctx, f.storePath, restart.RoleDrain, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if handled, stop, err := l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil {
		t.Fatalf("replacement acted while transition hold active during %s: %v, %q, %v", phase, handled, stop, err)
	}
	fileStartWhileSettling(t, l, time.Now())
	if _, err := l.pass(ctx, &LoopResult{}); err != nil {
		t.Fatalf("pass while settling and restart pending: %v", err)
	}
	select {
	case lane := <-steps.settlingStartSteps.started:
		t.Fatalf("worker started on %s while restart was pending during %s", lane, phase)
	default:
	}
	// Releasing the short transition hold lets the loop replace immediately
	// without waiting for the in-flight gate or land step to finish (D-SN2).
	releaseTransition()
	transitionReleased = true
	select {
	case <-done:
	case <-time.After(hangGuard):
		t.Fatalf("loop did not replace after transition hold released during %s", phase)
	}
	if loopErr != nil || result.Stop != StopRestarted {
		t.Fatalf("loop stopped without replacing: %+v, %v", result, loopErr)
	}
	select {
	case lane := <-steps.settlingStartSteps.started:
		t.Fatalf("worker started on %s before replacement %s finished", lane, req.ID)
	default:
	}
	closeRelease()
}

func TestAlreadyCurrentAnswerLeavesTheLoopRunning(t *testing.T) {
	h := newRestartHarness(t, nil, func(context.Context, string) error { t.Fatal("checked a binary that is the running one"); return nil })
	req := h.request(t, 1)[0]
	if stop, err := h.l.pass(context.Background(), &LoopResult{}); err != nil || stop != "" {
		t.Fatalf("pass = %q, %v", stop, err)
	}
	if a := h.answer(t, req.ID); a.Answer != restart.AnswerAlreadyCurrent || h.execed != nil {
		t.Fatalf("answer %+v exec %v", a, h.execed)
	}
	if _, err := h.l.pass(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if dispatches(t, h.f) == 0 {
		t.Fatal("the loop did not carry on dispatching after the answer")
	}
}

func TestRefusedAnswerKeepsTheOldLoopRunning(t *testing.T) {
	h := newRestartHarness(t, []byte("a broken build"), func(context.Context, string) error {
		return errors.New("noise\nthe policy is unreadable\n")
	})
	req := h.request(t, 1)[0]
	if stop, err := h.l.pass(context.Background(), &LoopResult{}); err != nil || stop != "" {
		t.Fatalf("pass = %q, %v", stop, err)
	}
	a := h.answer(t, req.ID)
	if a.Answer != restart.AnswerRefused || a.Detail != "the policy is unreadable" || h.execed != nil {
		t.Fatalf("answer %+v exec %v", a, h.execed)
	}
	if _, err := h.l.pass(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if dispatches(t, h.f) == 0 {
		t.Fatal("a refused request stopped the old loop")
	}
}

func TestOnlyTheNewestRequestIsActedOn(t *testing.T) {
	h := newRestartHarness(t, []byte("a different build"), func(context.Context, string) error { return nil })
	reqs := h.request(t, 3)
	if stop, err := h.l.pass(context.Background(), &LoopResult{}); err != nil || stop != StopRestarted {
		t.Fatalf("pass = %q, %v", stop, err)
	}
	for _, old := range reqs[:2] {
		if a := h.answer(t, old.ID); a.Answer != restart.AnswerSuperseded || !strings.Contains(a.Detail, reqs[2].ID) {
			t.Fatalf("older request answer %+v", a)
		}
	}
	if !containsEnv(h.env, restart.RequestEnv+"="+reqs[2].ID) {
		t.Fatal("the newest request was not the one carried over the exec")
	}
}

func TestARequestWrittenOfflineIsAnsweredByTheNextLoop(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	req := h.request(t, 1)[0]
	// A fresh loop over the same store, as after a process start, answers at its first pass.
	if _, err := h.l.pass(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if a := h.answer(t, req.ID); a.Answer != restart.AnswerAlreadyCurrent {
		t.Fatalf("answer %+v", a)
	}
}

func TestNoRequestMeansNoRestartAcrossAHundredPasses(t *testing.T) {
	h := newRestartHarness(t, []byte("the binary was replaced on disk"), func(context.Context, string) error {
		t.Fatal("checked a binary with no request")
		return nil
	}, "")
	before := h.l.build
	var report strings.Builder
	h.l.cfg.Report = &report
	for i := 0; i < 100; i++ {
		if stop, err := h.l.pass(context.Background(), &LoopResult{}); err != nil || stop == StopRestarted {
			t.Fatalf("pass %d = %q, %v", i, stop, err)
		}
	}
	if h.execed != nil || h.l.build != before || strings.Contains(report.String(), "loop: build") {
		t.Fatalf("the loop restarted without a request: exec=%v build=%v", h.execed, h.l.build)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func TestARequestFiledDuringTheCheckSupersedesTheSelectedOne(t *testing.T) {
	var h *restartHarness
	var second restart.Request
	h = newRestartHarness(t, []byte("a different build"), func(context.Context, string) error {
		if second.ID == "" {
			second = h.request(t, 1)[0]
		}
		return nil
	})
	first := h.request(t, 1)[0]
	if stop, err := h.l.pass(context.Background(), &LoopResult{}); err != nil || stop != StopRestarted {
		t.Fatalf("pass = %q, %v", stop, err)
	}
	if a := h.answer(t, first.ID); a.Answer != restart.AnswerSuperseded || !strings.Contains(a.Detail, second.ID) {
		t.Fatalf("the selected request was %+v, want superseded by %s", a, second.ID)
	}
	if !containsEnv(h.env, restart.RequestEnv+"="+second.ID) {
		t.Fatalf("the exec carried %v, not the newest request", h.env)
	}
}

// A process started by a pack launcher runs from an immutable cache file: the launcher is
// asked which binary is current, and that one is compared, checked and executed.
func TestALauncherBackedProcessRestartsOntoTheLaunchersCurrentBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selfBytes, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	next := filepath.Join(dir, "tillr-drain-next")
	if err := writeForkSafe(next, []byte("the build the launcher now names"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "launcher")
	script := "#!/bin/sh\n[ -n \"$" + restart.LauncherResolveEnv + "\" ] && echo " + next + " && exit 0\nexit 9\n"
	if err := writeForkSafe(launcher, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(restart.LauncherEnv, launcher)
	var checked string
	// The immutable file this process "runs from" is identical to itself: only the launcher
	// can tell that a newer build exists.
	h := newRestartHarness(t, selfBytes, func(_ context.Context, path string) error { checked = path; return nil })
	req := h.request(t, 1)[0]
	var execed string
	h.l.cfg.Restart.Exec = func(path string, argv, env []string) error { execed = path; h.env = env; return nil }
	if stop, err := h.l.pass(context.Background(), &LoopResult{}); err != nil || stop != StopRestarted {
		t.Fatalf("pass = %q, %v (answer %+v)", stop, err, h.answer(t, req.ID))
	}
	want, _ := filepath.EvalSymlinks(next)
	if checked != want || execed != want {
		t.Fatalf("checked %q and executed %q, want the launcher's %q for both", checked, execed, want)
	}
}

// The roll an earlier process of this build accepted is still owed after that process died.
func TestTheRunningBuildIsPublishedAtStart(t *testing.T) {
	h := newRestartHarness(t, nil, nil, "")
	if err := h.l.adoptRestart(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := openFixture(t, h.f.storePath)
	defer s.Close()
	var fact *restart.BuildFact
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		fact, err = restart.ReadBuild(r, restart.RoleDrain)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if fact == nil || fact.Digest != h.l.build.Digest || fact.PID != os.Getpid() {
		t.Fatalf("published build = %+v, want this process's %s", fact, h.l.build.Digest)
	}
}

// Children inherited across an exec are reaped by pid, and a child the new image starts
// itself keeps its exit status for its own Wait.
func TestInheritedChildrenAreReapedWithoutStealingNewOnes(t *testing.T) {
	proc, err := os.StartProcess("/bin/sh", []string{"sh", "-c", "exit 0"}, &os.ProcAttr{})
	if err != nil {
		t.Fatal(err)
	}
	// Nobody waits on proc: it is the zombie an exec leaves behind.
	deadline := time.Now().Add(hangGuard)
	zombie := func(pid int) bool {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		return err == nil && strings.Contains(string(raw[strings.LastIndexByte(string(raw), ')'):]), " Z ")
	}
	for !zombie(proc.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("the child never became a zombie")
		}
		time.Sleep(10 * time.Millisecond)
	}
	found := false
	for _, pid := range inheritedChildren() {
		found = found || pid == proc.Pid
	}
	if !found {
		t.Fatalf("inheritedChildren missed %d", proc.Pid)
	}
	cmd := exec.Command("/bin/sh", "-c", "sleep 0.3")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reapInherited(ctx, []int{proc.Pid})
	if _, err := os.Stat("/proc/" + strconv.Itoa(proc.Pid)); err == nil {
		t.Fatal("the inherited zombie was not reaped")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the new child's own Wait lost its status: %v", err)
	}
}

func TestRestartRechecksABinaryRenamedAfterTheCheck(t *testing.T) {
	checks := 0
	var h *restartHarness
	h = newRestartHarness(t, []byte("checked bytes"), func(_ context.Context, path string) error {
		checks++
		if checks == 1 {
			replacement := path + ".new"
			if err := writeForkSafe(replacement, []byte("unchecked bytes"), 0755); err != nil {
				return err
			}
			return os.Rename(replacement, path)
		}
		return errors.New("replacement is invalid")
	})
	req := h.request(t, 1)[0]
	_, _, err := h.l.gateRestart(context.Background(), &LoopResult{})
	if err != nil {
		t.Fatal(err)
	}
	if h.execed != nil || checks != 2 {
		t.Fatalf("unchecked rename executed: exec=%v checks=%d", h.execed, checks)
	}
	if a := h.answer(t, req.ID); a.Answer != restart.AnswerRefused {
		t.Fatalf("answer=%+v", a)
	}
}

func TestDiskDigestReadsTheBytesEvenAtADigestNamedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tillr-drain-"+strings.Repeat("a", 64))
	if err := writeForkSafe(path, []byte("first image"), 0755); err != nil {
		t.Fatal(err)
	}
	first := DiskDigest(path)
	if first != fileDigest(path) {
		t.Fatal("disk digest trusted the filename")
	}
	if err := writeForkSafe(path+".new", []byte("second image"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
	if second := DiskDigest(path); second == first || second != fileDigest(path) {
		t.Fatal("rename under digest-named path was invisible")
	}
}

func TestRestartChecksAndExecutesOneResolvedPathDespiteASymlinkSwap(t *testing.T) {
	h := newRestartHarness(t, []byte("checked image"), nil)
	target := h.l.cfg.Restart.Executable
	link := target + "-link"
	other := target + "-other"
	if err := writeForkSafe(other, []byte("unchecked image"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	h.l.cfg.Restart.Executable = link
	var checked, executed string
	h.l.cfg.Restart.Check = func(_ context.Context, path string) error {
		checked = path
		if err := os.Remove(link); err != nil {
			return err
		}
		return os.Symlink(other, link)
	}
	h.l.cfg.Restart.Exec = func(path string, _, _ []string) error { executed = path; return nil }
	h.request(t, 1)
	if _, stop, err := h.l.gateRestart(context.Background(), &LoopResult{}); err != nil || stop != StopRestarted {
		t.Fatalf("restart=%s %v", stop, err)
	}
	if checked != target || executed != target {
		t.Fatalf("check=%s exec=%s want resolved %s", checked, executed, target)
	}
}
