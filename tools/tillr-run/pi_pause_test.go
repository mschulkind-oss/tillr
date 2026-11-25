package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Exercise the real adapter and runner without a model. The injected bound makes
// tool completion and forced interruption decisions independent of machine speed.
func TestPiPauseAtGapAndStraggler(t *testing.T) {
	for _, honors := range []bool{true, false} {
		name := "honors"
		if !honors {
			name = "ignores"
		}
		t.Run(name, func(t *testing.T) {
			dir := scratch(t, "pi-pause")
			root := filepath.Join(dir, "lanes")
			lane := filepath.Join(root, "lane")
			worktree := filepath.Join(lane, "worktree")
			if err := os.MkdirAll(worktree, 0755); err != nil {
				t.Fatal(err)
			}
			write := func(path, text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(lane, "brief.md"), filledBrief(t, worktree))
			write(filepath.Join(lane, "session-id"), "12345678-1234-1234-1234-123456789abc\n")
			binDir := filepath.Join(dir, "bin")
			writeExecutable(t, filepath.Join(binDir, "pi"), `#!/usr/bin/env python3
import os, signal, subprocess, sys, time
from pathlib import Path
if os.environ.get("TILLR_SESSION_MODE") == "resume":
    Path("continuation").write_text(sys.stdin.read())
    sys.exit(0)
assert "--no-session" not in sys.argv
assert "--session-id" in sys.argv
Path("input").write_text(sys.stdin.read())
os.write(1, b'not readiness\n')
# Startup is deliberately held before the fork's print-mode handler exists.
signal.signal(signal.SIGUSR1, lambda *_: Path("early-signal").touch())
Path("starting").touch()
while not Path("allow-start").exists(): time.sleep(.01)
def requested(sig, frame):
    Path("request-seen").write_text("SIGUSR1")
signal.signal(signal.SIGUSR1, requested)
child = subprocess.Popen([sys.executable, "-c", '''
import signal, time
from pathlib import Path
signal.signal(signal.SIGUSR1, lambda *_: Path("child-signal").write_text("wrong target"))
Path("tool-ready").touch()
while not Path("release-tool").exists(): time.sleep(.01)
Path("tool-result").write_text("saved second call")
'''])
while not Path("tool-ready").exists(): time.sleep(.01)
Path("ready").touch()
# As in the fork, the JSON session header follows handler registration. Start the
# tool child first in this stand-in so an incorrect group signal is observable.
# Split writes and preceding diagnostics exercise transparent forwarding.
os.write(1, b'{"type":')
os.write(1, b'"session","id":"fixture"}\n')
while child.poll() is None: time.sleep(.01)
while not Path("request-seen").exists(): time.sleep(.01)
if os.environ["HONORS_STOP"] == "yes": sys.exit(0)
while True: time.sleep(.01)
`)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("HONORS_STOP", map[bool]string{true: "yes", false: "no"}[honors])
			// Do not inherit the enclosing lane's continuation or recording locations.
			t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
			t.Setenv("TILLR_SESSION_MODE", "new")
			t.Setenv("TILLR_TRACE_DIR", filepath.Join(dir, "trace"))
			s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			adapter, err := filepath.Abs("backends/pi/run-pi")
			if err != nil {
				t.Fatal(err)
			}
			backends, err := filepath.Abs("backends")
			if err != nil {
				t.Fatal(err)
			}
			bounds := make(chan time.Duration, 1)
			fire := make(chan time.Time)
			req := runner.Request{RunID: "run", LaneID: "lane", Backend: "pi", WorkingDir: "worktree", Command: []string{adapter, "--backend", "pi", "--model", "fixture", "--effort", "low", "--timeout", "60"}}
			opts := runner.ExecOptions{LaneRoot: root, BackendsDir: backends, Now: time.Now(), PauseAfter: func(d time.Duration) <-chan time.Time { bounds <- d; return fire }}
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					<-done
				}
			}()
			go func() {
				env, err := runner.Exec(ctx, s, req, opts)
				if err == nil {
					var r runner.Record
					err = json.Unmarshal(env.Payload, &r)
					if err == nil && r.State != runner.StatePaused {
						err = runner.Internalf("state=%s, want paused", r.State)
					}
				}
				done <- err
			}()
			waitFile := func(name string) {
				t.Helper()
				deadline := time.After(hangGuard)
				for {
					if _, err := os.Stat(filepath.Join(worktree, name)); err == nil {
						return
					}
					select {
					case err := <-done:
						joined = true
						t.Fatalf("harness ended before %s: %v", name, err)
					case <-deadline:
						t.Fatalf("harness never wrote %s", name)
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			waitFile("starting")
			if err := runner.RequestPause(ctx, s, "run", 120*time.Second, time.Now()); err != nil {
				t.Fatal(err)
			}
			select {
			case bound := <-bounds:
				if bound != 120*time.Second {
					t.Fatalf("bound=%s, want 120s", bound)
				}
			case err := <-done:
				joined = true
				t.Fatalf("ended before bound: %v", err)
			case <-time.After(hangGuard):
				t.Fatal("no pause bound")
			}
			// Give the watcher multiple polls while startup is held. This short
			// observation window exposes an early signal; it is not the injected
			// straggler clock, which remains under this test's control.
			select {
			case err := <-done:
				joined = true
				t.Fatalf("worker ended during startup: %v", err)
			case <-time.After(300 * time.Millisecond):
			}
			if _, err := os.Stat(filepath.Join(worktree, "early-signal")); !os.IsNotExist(err) {
				t.Fatal("SIGUSR1 sent before print mode registered its handler")
			}
			write(filepath.Join(worktree, "allow-start"), "ready to register")
			waitFile("ready")
			waitFile("tool-ready")
			waitFile("request-seen")
			if _, err := os.Stat(filepath.Join(worktree, "tool-result")); !os.IsNotExist(err) {
				t.Fatal("tool ended before release or bound")
			}
			if honors {
				write(filepath.Join(worktree, "release-tool"), "finish")
			} else {
				close(fire)
			}
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(hangGuard):
				t.Fatal("pause did not settle")
			}
			if _, err := os.Stat(filepath.Join(worktree, "early-signal")); !os.IsNotExist(err) {
				t.Fatal("startup pause arrived before handler registration")
			}
			if _, err := os.Stat(filepath.Join(worktree, "child-signal")); !os.IsNotExist(err) {
				t.Fatal("SIGUSR1 reached the tool child")
			}
			result, err := os.ReadFile(filepath.Join(worktree, "tool-result"))
			if honors && (err != nil || string(result) != "saved second call") {
				t.Fatalf("lost tool result: %s %v", result, err)
			}
			if !honors && !os.IsNotExist(err) {
				t.Fatal("straggler completed before kill")
			}
			log, err := os.ReadFile(filepath.Join(lane, "run.log"))
			if err != nil || string(log) != "not readiness\n{\"type\":\"session\",\"id\":\"fixture\"}\n" {
				t.Fatalf("JSON stream changed: %q %v", log, err)
			}
			req.Resume = true
			opts.PauseAfter = nil
			if _, err := runner.Exec(ctx, s, req, opts); err != nil {
				t.Fatal(err)
			}
			prompt, err := os.ReadFile(filepath.Join(worktree, "continuation"))
			if err != nil {
				t.Fatal(err)
			}
			interrupted := strings.Contains(string(prompt), "command was interrupted by a restart and may be run again")
			if interrupted == honors {
				t.Fatalf("wrong continuation: %s", prompt)
			}
			input, err := os.ReadFile(filepath.Join(worktree, "input"))
			if err != nil || string(input) != filledBrief(t, worktree) {
				t.Fatalf("stdin brief changed: %v", err)
			}
		})
	}
}

// Force the child to exit just after an empty pipe-readiness check. A watcher
// that checks exit only after that check drops the final output deterministically.
func TestPiPauseWatcherDrainsFinalOutput(t *testing.T) {
	helper, err := filepath.Abs("backends/pi/stop-at-gap")
	if err != nil {
		t.Fatal(err)
	}
	code := `import io, runpy, sys
from types import SimpleNamespace
namespace = runpy.run_path(sys.argv[1])
main = namespace["main"]
g = main.__globals__
class Child:
    stdout = SimpleNamespace(fileno=lambda: 7, close=lambda: None)
    exited = False
    def poll(self): return 17 if self.exited else None
    def wait(self): return 17
child = Child()
class Selector:
    calls = 0
    def __enter__(self): return self
    def __exit__(self, *args): pass
    def register(self, *args): pass
    def unregister(self, *args): pass
    def select(self, timeout):
        self.calls += 1
        if self.calls == 1:
            # Final output arrives with exit, after select found nothing.
            child.exited = True
            return []
        if self.calls <= 3: return [(SimpleNamespace(fileobj=child.stdout), 1)]
        return []
g["subprocess"].Popen = lambda *args, **kwargs: child
g["selectors"].DefaultSelector = Selector
chunks = iter([b'final output\n', b''])
g["os"].read = lambda *args: next(chunks)
output = io.BytesIO()
g["sys"] = SimpleNamespace(argv=["watcher", "unused-request", "stub"], stdout=SimpleNamespace(buffer=output))
assert main() == 17
assert output.getvalue() == b'final output\n', output.getvalue()
print("final output retained; exit 17 preserved")
`
	cmd := exec.Command("python3", "-c", code, helper)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("natural-exit drain: %v: %s", err, output)
	}
}
