// Package tests contains end-to-end integration smoke tests for the
// post-reset tillr CLI. These build the binary, init a project in a
// temp directory, add a feature, comment on it, and start the server
// briefly to verify the embedded SPA + API responds.
package tests

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var tillrBinary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "tillr-test-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	tillrBinary = filepath.Join(tmp, "tillr")
	cmd := exec.Command("go", "build", "-o", tillrBinary, "./cmd/tillr")
	cmd.Dir = findRepoRoot()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building tillr: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

func findRepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

// TestFullWorkflow verifies the post-reset CLI surface end-to-end:
// init, feature add, feature list, comment, feature show.
func TestFullWorkflow(t *testing.T) {
	tmp := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command(tillrBinary, args...)
		cmd.Dir = tmp
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tillr %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	run("init", "smoke-test")
	if _, err := os.Stat(filepath.Join(tmp, ".tillr.json")); err != nil {
		t.Fatalf(".tillr.json missing after init: %v", err)
	}

	run("feature", "add", "first feature")
	listOut := run("feature", "list")
	if !strings.Contains(listOut, "first feature") {
		t.Fatalf("expected 'first feature' in list output, got:\n%s", listOut)
	}

	// Pull the feature ID from JSON list
	out := run("--json", "feature", "list")
	var features []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(out), &features); err != nil {
		t.Fatalf("parse json feature list: %v\n%s", err, out)
	}
	if len(features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(features))
	}
	id := fmt.Sprintf("%d", features[0].ID)

	run("comment", id, "first thoughts on this")
	showOut := run("feature", "show", id)
	if !strings.Contains(showOut, "first thoughts on this") {
		t.Fatalf("expected comment in show output, got:\n%s", showOut)
	}
}

// TestPersonaWorkflow exercises the Stage 0 / MVP persona surface:
// definition discovery → feature with --persona → claim → append.
func TestPersonaWorkflow(t *testing.T) {
	tmp := t.TempDir()
	// Seed a .claude/agents/implementer.md so persona list discovers it
	agentsDir := filepath.Join(tmp, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(agentsDir, "implementer.md"),
		[]byte("---\nname: implementer\n---\nplaceholder\n"),
		0o644,
	); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	run := func(args ...string) string {
		cmd := exec.Command(tillrBinary, args...)
		cmd.Dir = tmp
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tillr %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	run("init", "persona-test")

	listOut := run("persona", "list")
	if !strings.Contains(listOut, "implementer") {
		t.Fatalf("persona list missing implementer: %s", listOut)
	}

	run("feature", "add", "--persona", "implementer", "do the thing")

	claimOut := run("--json", "persona", "claim", "implementer")
	var claimed struct {
		ID            int64  `json:"id"`
		Status        string `json:"status"`
		TargetPersona string `json:"target_persona"`
	}
	if err := json.Unmarshal([]byte(claimOut), &claimed); err != nil {
		t.Fatalf("parse claim: %v\n%s", err, claimOut)
	}
	if claimed.Status != "claimed" {
		t.Fatalf("expected claimed status, got %q", claimed.Status)
	}
	if claimed.TargetPersona != "implementer" {
		t.Fatalf("target_persona mismatch: %q", claimed.TargetPersona)
	}

	run("persona", "append", "--summary", "Did the work", "implementer", "Body line")

	contextOut := run("persona", "context", "implementer")
	if !strings.Contains(contextOut, "Did the work") || !strings.Contains(contextOut, "Body line") {
		t.Fatalf("context missing entry: %s", contextOut)
	}
}

// TestConfigSurface ensures the config CLI roundtrips.
func TestConfigSurface(t *testing.T) {
	tmp := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command(tillrBinary, args...)
		cmd.Dir = tmp
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tillr %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	run("init", "config-test")

	run("config", "set", "max-parallelism", "4")
	got := strings.TrimSpace(run("config", "get", "max-parallelism"))
	if got != "4" {
		t.Fatalf("config get returned %q, want 4", got)
	}
	showOut := run("config", "show")
	if !strings.Contains(showOut, "max-parallelism") || !strings.Contains(showOut, "4") {
		t.Fatalf("config show missing entry: %s", showOut)
	}
}

// TestServerStartsAndResponds verifies tillr serve binds and answers
// /api/health and /api/features.
func TestServerStartsAndResponds(t *testing.T) {
	tmp := t.TempDir()
	cmd := exec.Command(tillrBinary, "init", "server-test")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	port := freePort(t)
	srv := exec.Command(tillrBinary, "serve", "--port", fmt.Sprintf("%d", port))
	srv.Dir = tmp
	if err := srv.Start(); err != nil {
		t.Fatalf("starting server: %v", err)
	}
	defer func() { _ = srv.Process.Kill() }()

	url := fmt.Sprintf("http://localhost:%d", port)
	if !waitForServer(url+"/api/health", 5*time.Second) {
		t.Fatalf("server did not respond on %s within 5s", url)
	}

	resp, err := http.Get(url + "/api/features")
	if err != nil {
		t.Fatalf("GET /api/features: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 from /api/features, got %d", resp.StatusCode)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func waitForServer(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
