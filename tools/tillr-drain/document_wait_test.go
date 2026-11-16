package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// triggerReport signals the drained wait and the dispatch without sharing an unsafe
// buffer with the running loop. Waiting for the report ensures the commit is made
// while the loop is actually idle, not during startup.
type triggerReport struct {
	wait         chan struct{}
	dispatch     chan struct{}
	waitOnce     sync.Once
	dispatchOnce sync.Once
}

func (r *triggerReport) Write(p []byte) (int, error) {
	text := string(p)
	if strings.Contains(text, "waiting for a trigger") {
		r.waitOnce.Do(func() { close(r.wait) })
	}
	if strings.Contains(text, "dispatch item-during-wait ->") {
		r.dispatchOnce.Do(func() { close(r.dispatch) })
	}
	return len(p), nil
}

// emptyLoopArgs names all required programs without changing the default wait.
func emptyLoopArgs(t *testing.T) []string {
	t.Helper()
	f := newLoopFixture(t, stub(t, "unused-adapter", "exit 0\n"))
	fixtureAt(t, f.store)
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(scratch(t, "empty-policy"), "policy.json")
	writeFile(t, policy, string(raw))
	return []string{"loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot,
		"--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"),
		"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true",
		"--rolling", "", "--trace-bin", ""}
}

func TestExitWhenDrainedKeepsQueueDrainedExitZero(t *testing.T) {
	code, out := invoke(t, append(emptyLoopArgs(t), "--exit-when-drained")...)
	if code != 0 || !strings.Contains(string(out), "queue-drained") || strings.Contains(string(out), "waiting for a trigger") {
		t.Fatalf("exit opt-out: code %d: %s", code, out)
	}
}

func TestWaitingDefaultStopsCleanlyOnOperatorSignals(t *testing.T) {
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			cmd := exec.Command(bin, emptyLoopArgs(t)...)
			var output bytes.Buffer
			trigger := &triggerReport{wait: make(chan struct{}), dispatch: make(chan struct{})}
			writer := io.MultiWriter(&output, trigger)
			cmd.Stdout, cmd.Stderr = writer, writer
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			finished := false
			defer func() {
				if !finished {
					_ = cmd.Process.Kill()
					<-done
				}
			}()
			select {
			case <-trigger.wait:
			case err := <-done:
				finished = true
				t.Fatalf("default loop exited instead of waiting: %v: %s", err, output.String())
			case <-time.After(15 * time.Second):
				t.Fatal("default loop did not start waiting")
			}
			select {
			case err := <-done:
				finished = true
				t.Fatalf("default loop exited during idle polling: %v: %s", err, output.String())
			case <-time.After(3 * drain.StorePollInterval):
			}
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				finished = true
				if err != nil || !strings.HasSuffix(output.String(), "loop: stopped by the operator\n") {
					t.Fatalf("operator stop must exit 0 with a final explanation: %v: %s", err, output.String())
				}
			case <-time.After(15 * time.Second):
				t.Fatal("waiting loop ignored the operator signal")
			}
		})
	}
}

func TestLoopHelpNamesExitWhenDrained(t *testing.T) {
	code, out := invoke(t, "loop", "--help")
	if code != 0 || !strings.Contains(string(out), "-exit-when-drained") || strings.Contains(string(out), "-stay") {
		t.Fatalf("loop --help does not expose only the exit opt-out: exit %d: %s", code, out)
	}
}

func TestRemovedStayNamesTheDaemonDefault(t *testing.T) {
	code, out := invoke(t, "loop", "--stay")
	if code != 2 || !strings.Contains(string(out), "daemon by default") || !strings.Contains(string(out), "--exit-when-drained") {
		t.Fatalf("removed --stay must explain the new default: exit %d: %s", code, out)
	}
}

func TestDefaultLoopDispatchesADocumentCommittedDuringTriggerWait(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", counting+"printf 'RESULT: done\\n'"))
	fixtureAt(t, f.store)
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	report := &triggerReport{wait: make(chan struct{}), dispatch: make(chan struct{})}
	loop, err := drain.NewLoop(drain.LoopConfig{
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: steps,
		Stage:        passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		QuestionsCmd: strings.Fields(indexStub(t, map[string]any{"path": "docs/plan.md", "questions": []map[string]any{}})),
		Now:          steps.Now, Report: report,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result drain.LoopResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() { result, err := loop.Run(ctx); finished <- outcome{result, err} }()
	var exited bool
	defer func() {
		cancel()
		if exited {
			return
		}
		select {
		case out := <-finished:
			if out.err != nil || out.result.Stop != drain.StopOperatorStop {
				t.Errorf("canceled default loop: %+v / %v", out.result, out.err)
			}
			if out.result.Dispatched != 1 || out.result.SyncedSHA != git(t, f.root, "rev-parse", "main") {
				t.Errorf("dispatch or synced commit missing: %+v", out.result)
			}
		case <-time.After(10 * time.Second):
			t.Error("loop did not stop after cancellation")
		}
	}()
	select {
	case <-report.wait:
	case out := <-finished:
		exited = true
		t.Fatalf("default loop exited before waiting: %+v / %v", out.result, out.err)
	case <-time.After(10 * time.Second):
		t.Fatal("loop never reached trigger wait")
	}
	// Leave the queue unchanged for multiple polling intervals before adding work.
	select {
	case out := <-finished:
		exited = true
		t.Fatalf("default loop exited during idle passes: %+v / %v", out.result, out.err)
	case <-time.After(3 * drain.StorePollInterval):
	}
	commitDocument(t, f, "docs/plan.md", "# Plan\n\n### Work during wait\n\n<!-- tillr: item id=\"item-during-wait\" paths=\"candidate.txt\" -->\n\n- **c1:** candidate.txt exists.\n")
	select {
	case <-report.dispatch:
	case out := <-finished:
		exited = true
		t.Fatalf("default loop exited before dispatch: %+v / %v", out.result, out.err)
	case <-time.After(10 * time.Second):
		t.Fatal("committed block was not dispatched")
	}
}
