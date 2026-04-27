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
