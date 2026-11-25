package main_test

// `tillr view restart` against a real server process: it finishes what it is serving,
// replaces itself in place under the same process id, answers the request, and reports the
// answer in the board's header data. A binary that fails its check is refused and the old
// server keeps serving.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func viewScratch(t *testing.T) string {
	t.Helper()
	root := os.Getenv("TILLR_TEST_TMPDIR")
	if root == "" {
		root = os.Getenv("YOLO_DURABLE_DIR")
	}
	if root == "" {
		root = os.TempDir()
	}
	dir, err := os.MkdirTemp(root, "view-restart-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

type servedView struct {
	exe, store, base string
	cmd              *exec.Cmd
	out              string
}

func startServedView(t *testing.T) *servedView { return startServedViewEnv(t, nil) }

func startServedViewEnv(t *testing.T, env []string) *servedView {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	dir := viewScratch(t)
	v := &servedView{exe: filepath.Join(dir, "tillr-view"), store: fixture(t), base: "http://" + addr, out: filepath.Join(dir, "serve.out")}
	raw, err := os.ReadFile(viewBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.exe, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(v.out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	planner := filepath.Join(dir, "tillr-drain")
	if out, err := exec.Command("go", "-C", "../tillr-drain", "build", "-o", planner, ".").CombinedOutput(); err != nil {
		t.Fatalf("building the planner: %v: %s", err, out)
	}
	v.cmd = exec.Command(v.exe, "serve", "--store", v.store, "--addr", addr, "--perf-log", "off", "--tillr-drain", planner)
	v.cmd.Env = append(os.Environ(), env...)
	v.cmd.Stdout, v.cmd.Stderr = out, out
	if err := v.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.cmd.Process.Kill(); v.cmd.Wait() })
	v.waitServing(t)
	return v
}

// digest reads the build digest the server stamps on every API answer, refusals included:
// the fixture store has no item tables, so the routes answer 503, and the stamp is still
// there. It is the way a test tells one build's server from another's.
func (v *servedView) digest() (string, bool) {
	resp, err := http.Get(v.base + "/api/v1/snapshot")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var body struct {
		Snapshot struct {
			Build struct{ Digest string } `json:"build"`
		} `json:"snapshot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Snapshot.Build.Digest == "" {
		return "", false
	}
	return body.Snapshot.Build.Digest, true
}

func (v *servedView) waitServing(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if _, ok := v.digest(); ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	raw, _ := os.ReadFile(v.out)
	t.Fatalf("the server never answered:\n%s", raw)
}

func (v *servedView) restart(t *testing.T) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, viewBin, "restart", "--store", v.store, "--wait")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		served, _ := os.ReadFile(v.out)
		t.Fatalf("restart --wait never returned: %s\nserver:\n%s", out, served)
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func TestViewRestartReplacesTheServerInPlaceAndTheHeaderReportsIt(t *testing.T) {
	v := startServedView(t)
	before, _ := v.digest()
	next := v.exe + ".next"
	raw, _ := os.ReadFile(viewBin)
	if err := os.WriteFile(next, append(raw, []byte("\n# a different build\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, v.exe); err != nil {
		t.Fatal(err)
	}
	answer, code := v.restart(t)
	if code != 0 || !strings.Contains(answer, "restart-view-") || !strings.Contains(answer, "restarted") {
		t.Fatalf("restart --wait = %d %q", code, answer)
	}
	if syscall.Kill(v.cmd.Process.Pid, 0) != nil {
		t.Fatal("the server's process id did not survive the restart")
	}
	var after string
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if d, ok := v.digest(); ok && d != before {
			after = d
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if after == "" || after == before {
		t.Fatalf("the replacement never served a new build (before %s)", before)
	}
}

func TestViewRestartRefusesABinaryThatFailsItsCheck(t *testing.T) {
	v := startServedView(t)
	before, _ := v.digest()
	next := v.exe + ".next"
	if err := os.WriteFile(next, []byte("#!/bin/sh\necho 'the store cannot be opened' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, v.exe); err != nil {
		t.Fatal(err)
	}
	answer, code := v.restart(t)
	if code == 0 || !strings.Contains(answer, "refused") || !strings.Contains(answer, "the store cannot be opened") {
		t.Fatalf("restart --wait = %d %q", code, answer)
	}
	if d, ok := v.digest(); !ok || d != before {
		t.Fatalf("the old server did not keep serving: ok=%v digest=%s", ok, d)
	}
}

// A candidate that passes its check and is gone before the exec leaves the old server
// serving: the refusal is recorded and the listener is back.
func TestViewRestartKeepsServingWhenTheExecFailsAfterTheCheck(t *testing.T) {
	v := startServedView(t)
	before, _ := v.digest()
	next := v.exe + ".next"
	script := "#!/bin/sh\nfor a; do last=$a; done\nif [ \"$last\" = --check ]; then rm -f \"$0\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(next, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, v.exe); err != nil {
		t.Fatal(err)
	}
	answer, code := v.restart(t)
	if code == 0 || !strings.Contains(answer, "refused") || !strings.Contains(answer, "exec") {
		t.Fatalf("restart --wait = %d %q", code, answer)
	}
	deadline := time.Now().Add(hangGuard)
	for {
		if d, ok := v.digest(); ok && d == before {
			return
		}
		if time.Now().After(deadline) {
			raw, _ := os.ReadFile(v.out)
			t.Fatalf("the old server is not serving again:\n%s", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// --check refuses what a launch would refuse: here a front-end directory that is missing.
func TestViewCheckRefusesAMissingFrontEnd(t *testing.T) {
	dir := viewScratch(t)
	planner := filepath.Join(dir, "tillr-drain")
	if out, err := exec.Command("go", "-C", "../tillr-drain", "build", "-o", planner, ".").CombinedOutput(); err != nil {
		t.Fatalf("building the planner: %v: %s", err, out)
	}
	args := []string{"serve", "--store", fixture(t), "--tillr-drain", planner, "--check"}
	if out, err := exec.Command(viewBin, append(args, "--ui-at", "vite")...).CombinedOutput(); err != nil {
		t.Fatalf("a valid configuration failed its check: %v: %s", err, out)
	}
	out, err := exec.Command(viewBin, append(args, "--web-dist", filepath.Join(dir, "absent"))...).CombinedOutput()
	if err == nil {
		t.Fatalf("a missing --web-dist passed its check: %s", out)
	}
}

// A launcher-backed server restarts onto the launcher's current build, not its own file.
func TestViewRestartThroughALauncherReachesTheLaunchersBuild(t *testing.T) {
	dir := viewScratch(t)
	current := filepath.Join(dir, "current")
	launcher := filepath.Join(dir, "launcher")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n[ -n \"$TILLR_LAUNCHER_RESOLVE\" ] && cat "+current+" && exit 0\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := startServedViewEnv(t, []string{"TILLR_LAUNCHER=" + launcher})
	before, _ := v.digest()
	next := filepath.Join(dir, "tillr-view-next")
	raw, _ := os.ReadFile(viewBin)
	if err := os.WriteFile(next, append(raw, []byte("\n# the launcher's build\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte(next+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if answer, code := v.restart(t); code != 0 || !strings.Contains(answer, "restarted") {
		t.Fatalf("restart --wait = %d %q", code, answer)
	}
	deadline := time.Now().Add(hangGuard)
	for {
		if d, ok := v.digest(); ok && d != before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the replacement never served the launcher's build")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The restart through the shipped pack launcher, run against a scratch-rendered copy of the
// delivered source tree: unchanged source answers already-current, changed source moves the
// running server onto the newly built binary.
func TestViewRestartThroughTheShippedLauncherMovesOntoChangedSource(t *testing.T) {
	t.Parallel()
	dir, home, launcher, planner := renderPackTree(t)
	seedNoObsLauncherCache(t, home)
	share := filepath.Join(home, ".local", "share")
	env := noNodeEnv(t, dir, home)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	v := &servedView{store: fixture(t), base: "http://" + addr, out: filepath.Join(dir, "serve.out")}
	out, err := os.Create(v.out)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	v.cmd = exec.Command(launcher, "serve", "--store", v.store, "--addr", addr, "--perf-log", "off", "--tillr-drain", planner)
	v.cmd.Env = env
	v.cmd.Stdout, v.cmd.Stderr = out, out
	if err := v.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.cmd.Process.Kill(); v.cmd.Wait() })
	deadline := time.Now().Add(hangGuard)
	var before string
	for {
		if d, ok := v.digest(); ok {
			before = d
			break
		}
		if time.Now().After(deadline) {
			raw, _ := os.ReadFile(v.out)
			t.Fatalf("the launcher-built server never answered:\n%s", raw)
		}
		time.Sleep(100 * time.Millisecond)
	}
	restartVia := func() string {
		t.Helper()
		c := exec.Command(launcher, "restart", "--store", v.store, "--wait")
		c.Env = env
		answer, err := c.CombinedOutput()
		if err != nil {
			raw, _ := os.ReadFile(v.out)
			t.Fatalf("restart: %v: %s\n%s", err, answer, raw)
		}
		return string(answer)
	}
	if answer := restartVia(); !strings.Contains(answer, "already-current") {
		t.Fatalf("unchanged source answered %q", answer)
	}
	changed := filepath.Join(share, "tillr", "tillr-view", "build.go")
	body, err := os.ReadFile(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changed, []byte(strings.Replace(string(body), `"no-revision"`, `"new-build-no-revision"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if answer := restartVia(); !strings.Contains(answer, "restarted") {
		t.Fatalf("changed source answered %q", answer)
	}
	deadline = time.Now().Add(hangGuard)
	for {
		if d, ok := v.digest(); ok && d != before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never moved onto the launcher's new build")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if syscall.Kill(v.cmd.Process.Pid, 0) != nil {
		t.Fatal("the server's process id did not survive the restart")
	}
}
