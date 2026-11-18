package main_test

// The restart through the shipped pack launcher, not a stand-in for it: the launcher script
// in yolo-packs/tillr/programs is run against a scratch-rendered copy of the delivered source
// tree, exactly as a host's ~/.local/share/tillr is, so a launcher that loses its
// TILLR_LAUNCHER export or its resolve mode fails here.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// renderedPack copies the delivered tree into a scratch home and answers the environment a
// launcher needs there. The Go caches stay the shared default ones, named explicitly because
// the launcher's HOME is the scratch.
func renderedPack(t *testing.T) (home string, env []string) {
	t.Helper()
	home = scratch(t, "rendered-home")
	source, err := filepath.Abs(filepath.Join("..", "..", "yolo-packs", "tillr", "files", "local", "share", "tillr"))
	if err != nil {
		t.Fatal(err)
	}
	share := filepath.Join(home, ".local", "share")
	if err := os.MkdirAll(share, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-r", source, filepath.Join(share, "tillr")).CombinedOutput(); err != nil {
		t.Fatalf("rendering the pack tree: %v: %s", err, out)
	}
	goEnv := func(name string) string {
		out, err := exec.Command("go", "env", name).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	env = append(os.Environ(), "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"GOCACHE="+goEnv("GOCACHE"), "GOMODCACHE="+goEnv("GOMODCACHE"), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOPROXY=off")
	return home, env
}

func launcherPath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "yolo-packs", "tillr", "programs", name, "run"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func buildLineCount(path string) int {
	raw, _ := os.ReadFile(path)
	return len(buildLines(string(raw)))
}

func TestRestartThroughTheShippedLauncherMovesOntoChangedSourceAndAnswersAlreadyCurrent(t *testing.T) {
	home, env := renderedPack(t)
	launcher := launcherPath(t, "tillr-drain")
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store)
	policy := filepath.Join(scratch(t, "launcher-policy"), "policy.json")
	raw, _ := json.Marshal(f.policy)
	writeFile(t, policy, string(raw))
	out := filepath.Join(scratch(t, "launcher-out"), "loop.out")
	outFile, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	cmd := exec.Command(launcher, "loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", "1", "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"), "--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0))
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = outFile, outFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(300 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				body, _ := os.ReadFile(out)
				t.Fatalf("timed out waiting for %s:\n%s", what, body)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor("the launcher-built loop to start", func() bool { return buildLineCount(out) == 1 })
	restartVia := func() string {
		t.Helper()
		c := exec.Command(launcher, "restart", "--root", f.root, "--store", f.store, "--wait")
		c.Env = env
		answer, err := c.CombinedOutput()
		if err != nil {
			body, _ := os.ReadFile(out)
			t.Fatalf("restart: %v: %s\n%s", err, answer, body)
		}
		return string(answer)
	}
	// Unchanged source: the launcher names the binary already running.
	if answer := restartVia(); !strings.Contains(answer, "already-current") {
		t.Fatalf("unchanged source answered %q", answer)
	}
	if n := buildLineCount(out); n != 1 {
		t.Fatalf("an already-current answer restarted the loop: %d build lines", n)
	}
	// Changed source in the rendered tree: the next launch of the program would build it,
	// and a restart must move the running loop onto it.
	changed := filepath.Join(home, ".local", "share", "tillr", "tillr-drain", "drain", "build.go")
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
	lines := buildLines(func() string { b, _ := os.ReadFile(out); return string(b) }())
	if len(lines) != 2 || lines[0] == lines[1] {
		t.Fatalf("build lines = %v, want a new build", lines)
	}
	if err := exec.Command("kill", "-0", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		t.Fatal("the loop's process id did not survive the restart")
	}
}
