// Package tests contains end-to-end integration tests for the tillr CLI.
// These tests build the actual binary and exercise the full workflow in an
// isolated temp directory, verifying that a fresh user can init a project,
// add features, run cycles, and start the server.
package tests

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var tillerBinary string

func TestMain(m *testing.M) {
	// Build the tillr binary once for all tests
	tmp, err := os.MkdirTemp("", "tillr-test-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	tillerBinary = filepath.Join(tmp, "tillr")
	cmd := exec.Command("go", "build", "-o", tillerBinary, "./cmd/tillr")
	cmd.Dir = findRepoRoot()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building tillr: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmp) //nolint:errcheck
	os.Exit(code)
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// run executes the tillr binary in the given directory and returns stdout.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(tillerBinary, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tillr %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// runJSON executes the tillr binary with --json and unmarshals the output.
func runJSON(t *testing.T, dir string, v any, args ...string) {
	t.Helper()
	args = append(args, "--json")
	cmd := exec.Command(tillerBinary, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tillr %s --json failed: %v\n%s", strings.Join(args[:len(args)-1], " "), err, out)
	}
	if err := json.Unmarshal(out, v); err != nil {
		t.Fatalf("unmarshaling JSON from tillr %s: %v\n%s", strings.Join(args[:len(args)-1], " "), err, out)
	}
}

// runMayFail executes the binary and returns output + error without failing the test.
func runMayFail(dir string, args ...string) (string, error) {
	cmd := exec.Command(tillerBinary, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// freePort finds an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() //nolint:errcheck
	return port
}

// --- Tests ---

func TestFullWorkflow(t *testing.T) {
	// Create an isolated project directory
	projectDir, err := os.MkdirTemp("", "tillr-integration-*")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(projectDir) //nolint:errcheck

	// Initialize a git repo (tillr expects one)
	exec.Command("git", "init", projectDir).Run()                                        //nolint:errcheck
	exec.Command("git", "-C", projectDir, "config", "user.email", "test@test.com").Run() //nolint:errcheck
	exec.Command("git", "-C", projectDir, "config", "user.name", "Test").Run()           //nolint:errcheck

	// Step 1: Init project
	out := run(t, projectDir, "init", "test-project")
	if !strings.Contains(out, "test-project") {
		t.Errorf("init output should mention project name, got: %s", out)
	}

	// Verify DB was created
	dbPath := filepath.Join(projectDir, "tillr.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("tillr.db was not created")
	}

	// Verify config was created
	configPath := filepath.Join(projectDir, ".tillr.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal(".tillr.json was not created")
	}

	// Step 2: Check status
	out = run(t, projectDir, "status")
	if !strings.Contains(out, "test-project") {
		t.Errorf("status should show project name, got: %s", out)
	}

	// Step 3: Add a feature
	run(t, projectDir, "feature", "add", "Login Page", "--spec", "Build a login page with email/password")

	// Step 4: List features
	var features []map[string]any
	runJSON(t, projectDir, &features, "feature", "list")
	if len(features) == 0 {
		t.Fatal("feature list should have at least one feature")
	}
	featureID := features[0]["id"].(string)
	if featureID == "" {
		t.Fatal("feature should have an ID")
	}

	// Step 5: Show feature detail
	out = run(t, projectDir, "feature", "show", featureID)
	if !strings.Contains(out, "Login Page") {
		t.Errorf("feature show should contain name, got: %s", out)
	}

	// Step 6: Start a cycle
	out = run(t, projectDir, "cycle", "start", "feature-implementation", featureID)
	if !strings.Contains(out, "Started") {
		t.Errorf("cycle start should confirm, got: %s", out)
	}

	// Step 7: Check cycle status
	var cycles []map[string]any
	runJSON(t, projectDir, &cycles, "cycle", "status")
	if len(cycles) == 0 {
		t.Fatal("should have an active cycle")
	}
	if cycles[0]["entity_id"] != featureID {
		t.Errorf("cycle entity_id should be %s, got %v", featureID, cycles[0]["entity_id"])
	}
	if cycles[0]["entity_type"] != "feature" {
		t.Errorf("cycle entity_type should be 'feature', got %v", cycles[0]["entity_type"])
	}

	// Step 8: Add a roadmap item
	run(t, projectDir, "roadmap", "add", "User Authentication", "--priority", "high", "--effort", "m")

	// Step 9: Doctor check
	out = run(t, projectDir, "doctor")
	if !strings.Contains(out, "OK") && !strings.Contains(out, "ok") && !strings.Contains(out, "✓") {
		// Doctor may report warnings but shouldn't fail
		t.Logf("doctor output: %s", out)
	}

	// Step 10: History
	var events []map[string]any
	runJSON(t, projectDir, &events, "history")
	if len(events) == 0 {
		t.Error("should have events after creating features and cycles")
	}
}

func TestServerStartsAndResponds(t *testing.T) {
	// Create an isolated project
	projectDir, err := os.MkdirTemp("", "tillr-server-test-*")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(projectDir) //nolint:errcheck

	exec.Command("git", "init", projectDir).Run() //nolint:errcheck
	run(t, projectDir, "init", "server-test")
	run(t, projectDir, "feature", "add", "Test Feature")

	// Start server on a free port
	port := freePort(t)
	cmd := exec.Command(tillerBinary, "serve", "--port", fmt.Sprintf("%d", port))
	cmd.Dir = projectDir
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting server: %v", err)
	}
	defer func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	}()

	// Wait for server to be ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	var lastErr error
	for i := 0; i < 30; i++ {
		resp, err := http.Get(baseURL + "/api/status")
		if err == nil {
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode == 200 {
				lastErr = nil
				break
			}
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("server didn't start within timeout: %v", lastErr)
	}

	// Test API endpoints
	endpoints := []struct {
		path       string
		expectCode int
	}{
		{"/api/status", 200},
		{"/api/features", 200},
		{"/api/cycles", 200},
		{"/api/roadmap", 200},
	}
	for _, ep := range endpoints {
		resp, err := http.Get(baseURL + ep.path)
		if err != nil {
			t.Errorf("GET %s: %v", ep.path, err)
			continue
		}
		resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != ep.expectCode {
			t.Errorf("GET %s: expected %d, got %d", ep.path, ep.expectCode, resp.StatusCode)
		}
	}

	// Test that status response has valid JSON with project info
	resp, err := http.Get(baseURL + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decoding status response: %v", err)
	}
	project, ok := status["project"].(map[string]any)
	if !ok {
		t.Fatal("status response should have a 'project' object")
	}
	if project["name"] == nil || project["name"] == "" {
		t.Errorf("project name should be set, got %v", project["name"])
	}
}

func TestCycleAdvanceWorkflow(t *testing.T) {
	projectDir, err := os.MkdirTemp("", "tillr-cycle-test-*")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(projectDir) //nolint:errcheck

	exec.Command("git", "init", projectDir).Run() //nolint:errcheck
	run(t, projectDir, "init", "cycle-test")
	run(t, projectDir, "feature", "add", "Cycle Feature")

	// Get feature ID
	var features []map[string]any
	runJSON(t, projectDir, &features, "feature", "list")
	featureID := features[0]["id"].(string)

	// Start a collaborative-design cycle (has human steps)
	run(t, projectDir, "cycle", "start", "collaborative-design", featureID)

	// Verify cycle is active at step 0 (intake — agent step)
	var cycles []map[string]any
	runJSON(t, projectDir, &cycles, "cycle", "status")
	if len(cycles) == 0 {
		t.Fatal("should have an active cycle")
	}
	if cycles[0]["current_step"].(float64) != 0 {
		t.Errorf("should start at step 0, got %v", cycles[0]["current_step"])
	}

	// Try to advance a non-human step (should fail)
	_, err = runMayFail(projectDir, "cycle", "advance", "--feature", featureID, "--approve")
	if err == nil {
		t.Error("advancing a non-human step should fail")
	}
}

func TestDoubleInitFails(t *testing.T) {
	projectDir, err := os.MkdirTemp("", "tillr-double-init-*")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(projectDir) //nolint:errcheck

	exec.Command("git", "init", projectDir).Run() //nolint:errcheck
	run(t, projectDir, "init", "first-project")

	// Second init should fail (project already exists)
	_, err = runMayFail(projectDir, "init", "second-project")
	if err == nil {
		t.Error("double init should fail")
	}
}
