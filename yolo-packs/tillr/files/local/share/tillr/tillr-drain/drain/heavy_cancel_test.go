package drain

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

type heavyFaultWatcher struct {
	t         *testing.T
	storePath string
	rule      watch.Rule
}

func newHeavyFaultWatcher(t *testing.T, storePath string, at time.Time) *heavyFaultWatcher {
	t.Helper()
	rules, err := watch.LoadRules(filepath.Join("..", "..", "tillr-watch", "rules"))
	if err != nil {
		t.Fatalf("load watch rules: %v", err)
	}
	idx := slices.IndexFunc(rules, func(r watch.Rule) bool { return r.ID == "drain-heavy-fault" })
	if idx < 0 {
		t.Fatal("missing drain-heavy-fault rule")
	}
	w := &heavyFaultWatcher{t: t, storePath: storePath, rule: rules[idx]}
	if notices := w.pending(at); len(notices) != 0 {
		t.Fatalf("priming watch on empty store opened notices: %+v", notices)
	}
	return w
}

func (w *heavyFaultWatcher) pending(now time.Time) []watch.Notice {
	w.t.Helper()
	s, err := store.Open(context.Background(), w.storePath, true)
	if err != nil {
		w.t.Fatalf("open store for watch: %v", err)
	}
	defer s.Close()
	ev := watch.Evaluator{
		Store: s,
		Rules: []watch.Rule{w.rule},
		Clock: func() time.Time { return now },
	}
	results, err := ev.EvaluateRules(context.Background())
	if err != nil {
		w.t.Fatalf("evaluate drain-heavy-fault: %v", err)
	}
	if len(results) != 1 || results[0].Diagnostic() != "" || results[0].Skipped != 0 {
		w.t.Fatalf("unexpected watch evaluation results: %+v", results)
	}
	notices, err := ev.Pending(context.Background(), "tillr-triage")
	if err != nil {
		w.t.Fatalf("read pending notices: %v", err)
	}
	return notices
}

func readHeavyFaultEvents(t *testing.T, storePath string) []event.Event {
	t.Helper()
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	all, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var out []event.Event
	for _, ev := range all {
		if ev.Kind == HeavyFaultKind {
			out = append(out, ev)
		}
	}
	return out
}

func TestDetachedHeavyProofCancelledByOperatorSignalOpensNoFault(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checksDir, "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy"}`)
	base := heavyGit(t, root, "rev-parse", TrunkRef)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "stale-tip")

	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recordFixtureLandings(t, s.Path(), tip)
	watcher := newHeavyFaultWatcher(t, s.Path(), time.Now())

	entered := filepath.Join(work, "proof-entered")
	gate := executable(t, `touch `+entered+`
sleep 60`)

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	l := newDetachedProofLoopFixture(t, root, work, s.Path(), gate, nil)
	if err := l.startHeavy(ctx, tip); err != nil {
		t.Fatalf("startHeavy: %v", err)
	}

	deadline := time.After(hangGuard)
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("detached heavy proof did not enter judge")
		case <-time.After(10 * time.Millisecond):
		}
	}

	gateDir, err := heavy.Directory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	face, ok := heavyrun.ReadHeavyProofFace(gateDir)
	if !ok || face.PID <= 0 {
		t.Fatalf("missing heavy proof face: ok=%v face=%+v", ok, face)
	}
	if err := syscall.Kill(face.PID, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM proof supervisor %d: %v", face.PID, err)
	}
	l.heavyWG.Wait()

	if faults := readHeavyFaultEvents(t, s.Path()); len(faults) != 0 {
		t.Fatalf("operator SIGTERM recorded %d drain.heavy-fault event(s): %+v", len(faults), faults)
	}
	if notices := watcher.pending(time.Now().Add(time.Second)); len(notices) != 0 {
		t.Fatalf("operator SIGTERM opened drain-heavy-fault alert(s): %+v", notices)
	}
	progress := readHeavyProgressEvents(t, s.Path())
	if len(progress) == 0 {
		t.Fatal("expected drain.heavy-progress events")
	}
	last := progress[len(progress)-1].Fact
	if last.Phase != "ended" || last.Outcome != "cancelled" {
		t.Fatalf("last heavy-progress = %+v, want ended/cancelled", last)
	}
	if last.CancelReason != "operator signal: SIGTERM" {
		t.Fatalf("cancel_reason = %q, want %q", last.CancelReason, "operator signal: SIGTERM")
	}
}

func TestHeavyProofCancelledByLoopStopOrRestartOpensNoFault(t *testing.T) {
	t.Run("detached_owner_exited", func(t *testing.T) {
		root, _ := heavyRepo(t)
		work := scratchUnderDurable(t)
		checksDir := filepath.Join(root, "tools", "gate", "checks")
		if err := os.MkdirAll(checksDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(checksDir, "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy"}`)
		base := heavyGit(t, root, "rev-parse", TrunkRef)
		heavyGit(t, root, "update-ref", GreenRef, base)
		tip := heavyCommit(t, root, "restart-tip")

		s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		recordFixtureLandings(t, s.Path(), tip)
		watcher := newHeavyFaultWatcher(t, s.Path(), time.Now())

		entered := filepath.Join(work, "proof-entered")
		gate := executable(t, `touch `+entered+`
sleep 60`)

		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()

		// Start a dummy owner process so --tillr-heavy-proof watches a real PID
		// that we can stop cleanly to simulate a loop stop or restart.
		ownerCmd := exec.Command("sleep", "60")
		if err := ownerCmd.Start(); err != nil {
			t.Fatalf("start dummy owner: %v", err)
		}
		defer func() {
			if ownerCmd.Process != nil {
				_ = ownerCmd.Process.Kill()
				_ = ownerCmd.Wait()
			}
		}()
		ownerProc, err := heavyrun.ReadHeavyProcess(ownerCmd.Process.Pid)
		if err != nil {
			t.Fatalf("read dummy owner start ticks: %v", err)
		}
		ownerStartTicks := ownerProc.Start

		gateDir, err := heavy.Directory(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(gateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		spec := heavyrun.HeavyProofSpec{
			Root:             root,
			Work:             filepath.Join(work, ".rolling", "worktree"),
			Trunk:            TrunkRef,
			Store:            s.Path(),
			LaneRoot:         work,
			RequestedTip:     tip,
			OwnerPID:         ownerCmd.Process.Pid,
			OwnerStartTicks:  ownerStartTicks,
			HeavyGateBin:     gate,
			Dir:              gateDir,
			FaceFile:         filepath.Join(gateDir, heavyrun.HeavyAttemptFaceFile),
			DoneFile:         filepath.Join(gateDir, heavyrun.HeavyAttemptDoneFile),
			EarlyRequestFile: filepath.Join(gateDir, heavyrun.HeavyEarlyRequestFile),
			EarlyAnswerFile:  filepath.Join(gateDir, heavyrun.HeavyEarlyAnswerFile),
		}
		if err := heavyrun.PublishHeavyJSON(gateDir, heavyrun.HeavyAttemptSpecFile, spec); err != nil {
			t.Fatal(err)
		}
		readyR, readyW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		supCmd := exec.Command(self, "--tillr-heavy-proof", filepath.Join(gateDir, heavyrun.HeavyAttemptSpecFile))
		supCmd.Dir = root
		supCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		supCmd.ExtraFiles = []*os.File{readyW}
		if err := supCmd.Start(); err != nil {
			_ = readyW.Close()
			_ = readyR.Close()
			t.Fatalf("start heavy proof supervisor: %v", err)
		}
		_ = readyW.Close()
		var readyBuf [1]byte
		_, _ = readyR.Read(readyBuf[:])
		_ = readyR.Close()

		deadline := time.After(hangGuard)
		for {
			if _, err := os.Stat(entered); err == nil {
				break
			}
			select {
			case <-deadline:
				t.Fatal("detached heavy proof did not enter judge")
			case <-time.After(10 * time.Millisecond):
			}
		}

		// Stop the dummy owner loop; the supervisor must cancel and write heavy-attempt.done.json.
		_ = ownerCmd.Process.Kill()
		_ = ownerCmd.Wait()
		ownerCmd.Process = nil
		if err := supCmd.Wait(); err != nil {
			t.Fatalf("supervisor exited with error: %v", err)
		}

		done, ok := heavyrun.ReadHeavyProofDone(gateDir)
		if !ok || !done.Interrupted {
			t.Fatalf("heavy-attempt.done.json = (ok=%v, %+v), want Interrupted=true", ok, done)
		}
		if done.CancelReason != "loop stopped or restarted" {
			t.Fatalf("done.CancelReason = %q, want %q", done.CancelReason, "loop stopped or restarted")
		}

		// A fresh replacement loop reclaims heavy-attempt.done.json; it must not record drain.heavy-fault.
		replacement := newDetachedProofLoopFixture(t, root, work, s.Path(), gate, nil)
		if err := replacement.startHeavy(ctx, tip); err != nil {
			t.Fatalf("replacement startHeavy: %v", err)
		}
		replacement.heavyWG.Wait()

		if faults := readHeavyFaultEvents(t, s.Path()); len(faults) != 0 {
			t.Fatalf("reclaimed restart cancellation recorded %d drain.heavy-fault event(s): %+v", len(faults), faults)
		}
		if notices := watcher.pending(time.Now().Add(time.Second)); len(notices) != 0 {
			t.Fatalf("reclaimed restart cancellation opened drain-heavy-fault alert(s): %+v", notices)
		}
		progress := readHeavyProgressEvents(t, s.Path())
		if len(progress) == 0 {
			t.Fatal("expected drain.heavy-progress events")
		}
		last := progress[len(progress)-1].Fact
		if last.Phase != "ended" || last.Outcome != "cancelled" || last.CancelReason != "loop stopped or restarted" {
			t.Fatalf("last heavy-progress = %+v, want ended/cancelled with cancel_reason %q", last, "loop stopped or restarted")
		}
	})

	t.Run("loop_stop_requested", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		tip := heavyCommit(t, root, "stop-tip")
		work := scratchUnderDurable(t)
		s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		recordFixtureLandings(t, s.Path(), tip)
		watcher := newHeavyFaultWatcher(t, s.Path(), time.Now())

		ctx, cancelCause := context.WithCancelCause(context.Background())
		entered := make(chan struct{})
		steps := heavyTestSteps{judge: func(jctx context.Context, _, _ string) ([]HeavyCheck, error) {
			close(entered)
			<-jctx.Done()
			return nil, jctx.Err()
		}}
		l := &Loop{
			gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
			cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: work, Steps: steps, Now: time.Now, Report: io.Discard},
		}
		if err := l.startHeavy(ctx, tip); err != nil {
			t.Fatalf("startHeavy: %v", err)
		}
		<-entered
		cancelCause(errStopRequested)
		l.heavyWG.Wait()

		if faults := readHeavyFaultEvents(t, s.Path()); len(faults) != 0 {
			t.Fatalf("loop stop recorded %d drain.heavy-fault event(s): %+v", len(faults), faults)
		}
		if notices := watcher.pending(time.Now().Add(time.Second)); len(notices) != 0 {
			t.Fatalf("loop stop opened drain-heavy-fault alert(s): %+v", notices)
		}
		progress := readHeavyProgressEvents(t, s.Path())
		if len(progress) == 0 {
			t.Fatal("expected drain.heavy-progress events")
		}
		last := progress[len(progress)-1].Fact
		if last.Phase != "ended" || last.Outcome != "cancelled" || last.CancelReason != errStopRequested.Error() {
			t.Fatalf("last heavy-progress = %+v, want ended/cancelled with cancel_reason %q", last, errStopRequested.Error())
		}
	})
}

func TestHeavyProofCancelledByTipMoveOpensNoFault(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "tip-1")
	work := scratchUnderDurable(t)
	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recordFixtureLandings(t, s.Path(), tip)
	watcher := newHeavyFaultWatcher(t, s.Path(), time.Now())

	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		return nil, heavyrun.ErrHeavyTipMoved
	}}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: work, Steps: steps, Now: time.Now, Report: io.Discard},
	}
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatalf("startHeavy: %v", err)
	}
	l.heavyWG.Wait()

	if faults := readHeavyFaultEvents(t, s.Path()); len(faults) != 0 {
		t.Fatalf("tip move cancellation recorded %d drain.heavy-fault event(s): %+v", len(faults), faults)
	}
	if notices := watcher.pending(time.Now().Add(time.Second)); len(notices) != 0 {
		t.Fatalf("tip move cancellation opened drain-heavy-fault alert(s): %+v", notices)
	}
	progress := readHeavyProgressEvents(t, s.Path())
	if len(progress) == 0 {
		t.Fatal("expected drain.heavy-progress events")
	}
	last := progress[len(progress)-1].Fact
	if last.Phase != "ended" || last.Outcome != "cancelled" || last.CancelReason != heavyrun.ErrHeavyTipMoved.Error() {
		t.Fatalf("last heavy-progress = %+v, want ended/cancelled with cancel_reason %q", last, heavyrun.ErrHeavyTipMoved.Error())
	}
}

func TestHeavyProofFaultOpensFaultAlert(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "fault-tip")
	work := scratchUnderDurable(t)
	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recordFixtureLandings(t, s.Path(), tip)
	watcher := newHeavyFaultWatcher(t, s.Path(), time.Now())

	const faultMsg = "judge binary failed: exit status 2"
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		return nil, errors.New(faultMsg)
	}}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: work, Steps: steps, Now: time.Now, Report: io.Discard},
	}
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatalf("startHeavy: %v", err)
	}
	l.heavyWG.Wait()

	faults := readHeavyFaultEvents(t, s.Path())
	if len(faults) != 1 || !strings.Contains(string(faults[0].Payload), faultMsg) {
		t.Fatalf("drain.heavy-fault events = %+v, want 1 event with %q", faults, faultMsg)
	}
	notices := watcher.pending(time.Now().Add(time.Second))
	if len(notices) != 1 || notices[0].Bundle.RuleID != "drain-heavy-fault" || notices[0].Bundle.Subject != tip {
		t.Fatalf("drain-heavy-fault alerts = %+v, want 1 alert for %s", notices, tip)
	}
	progress := readHeavyProgressEvents(t, s.Path())
	if len(progress) == 0 {
		t.Fatal("expected drain.heavy-progress events")
	}
	last := progress[len(progress)-1].Fact
	if last.Phase != "ended" || last.Outcome != "fault" || last.CancelReason != "" {
		t.Fatalf("last heavy-progress = %+v, want ended/fault with empty cancel_reason", last)
	}
}
