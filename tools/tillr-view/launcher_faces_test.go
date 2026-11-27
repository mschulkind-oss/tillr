package main_test

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// failingNpmEnv builds on noNodeEnv by placing stub node and npm executables on
// PATH. The stub npm records every invocation to npmLog and fails with ENOENT,
// reproducing an installed copy that has node/npm on PATH but no node_modules.
func failingNpmEnv(t *testing.T, dir, home, npmLog string) []string {
	t.Helper()
	env := noNodeEnv(t, dir, home)
	binDir := filepath.Join(dir, "bin")
	nodeStub := "#!/bin/sh\necho linux-amd64-gnu\n"
	if err := os.WriteFile(filepath.Join(binDir, "node"), []byte(nodeStub), 0o755); err != nil {
		t.Fatal(err)
	}
	npmStub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + npmLog + "\"\n" +
		"echo \"npm error code ENOENT\" >&2\n" +
		"echo \"npm error path tillr-view/web/node_modules\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "npm"), []byte(npmStub), 0o755); err != nil {
		t.Fatal(err)
	}
	return env
}

func startLauncherServe(t *testing.T, launcher string, env []string, store, planner, outPath string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcher, "serve", "--store", store, "--addr", addr, "--perf-log", "off", "--tillr-drain", planner)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		out.Close()
		t.Fatal(err)
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = out.Close()
	}
	t.Cleanup(stop)
	return "http://" + addr, stop
}

func TestShippedLauncherTableFaceSkipsFrontEndBuildWithoutNodeModules(t *testing.T) {
	t.Parallel()
	dir, home, launcher, planner := renderPackTree(t)
	seedNoObsLauncherCache(t, home)

	nodeModules := filepath.Join(home, ".local", "share", "tillr", "tillr-view", "web", "node_modules")
	if _, err := os.Stat(nodeModules); !os.IsNotExist(err) {
		t.Fatalf("expected installed copy to have no node_modules directory, got err=%v", err)
	}

	npmLog := filepath.Join(dir, "npm.log")
	env := failingNpmEnv(t, dir, home, npmLog)
	store := fixture(t)

	// Cold and warm calls to a table-printing read face run no front-end build
	// and print no front-end error.
	for _, call := range []string{"cold", "warm"} {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(launcher, "running", "--store", store)
		cmd.Env = env
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s running failed: %v\nstdout:\n%s\nstderr:\n%s", call, err, stdout.String(), stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("%s running wrote unexpected stderr:\n%s", call, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) == "" {
			t.Fatalf("%s running printed no output", call)
		}
		if _, err := os.Stat(npmLog); !os.IsNotExist(err) {
			raw, _ := os.ReadFile(npmLog)
			t.Fatalf("%s running invoked npm:\n%s", call, raw)
		}
	}

	// A subsequent serve call still attempts the front-end build, explains once
	// what the failure means for serve, and still serves the API without UI.
	serveOut1 := filepath.Join(dir, "serve1.out")
	base1, stop1 := startLauncherServe(t, launcher, env, store, planner, serveOut1)
	if fe := waitForFrontEnd(t, base1, serveOut1); fe != "none" {
		raw, _ := os.ReadFile(serveOut1)
		t.Fatalf("first serve front_end = %q, want %q\n%s", fe, "none", raw)
	}
	stop1()

	raw1, _ := os.ReadFile(serveOut1)
	wantNote := "tillr-view serve: observatory front-end build failed; serve will run without the observatory web UI"
	if !strings.Contains(string(raw1), wantNote) {
		t.Fatalf("first serve output missing %q:\n%s", wantNote, raw1)
	}
	npmCalls, err := os.ReadFile(npmLog)
	if err != nil {
		t.Fatalf("expected serve to attempt npm once: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(string(npmCalls)), "\n"); len(lines) != 1 {
		t.Fatalf("first serve invoked npm %d times, want 1:\n%s", len(lines), npmCalls)
	}

	// A second serve call on the unchanged installed copy does not re-run the
	// failed front-end build or repeat the failure note.
	serveOut2 := filepath.Join(dir, "serve2.out")
	base2, stop2 := startLauncherServe(t, launcher, env, store, planner, serveOut2)
	if fe := waitForFrontEnd(t, base2, serveOut2); fe != "none" {
		raw, _ := os.ReadFile(serveOut2)
		t.Fatalf("second serve front_end = %q, want %q\n%s", fe, "none", raw)
	}
	stop2()

	raw2, _ := os.ReadFile(serveOut2)
	if strings.Contains(string(raw2), "observatory front-end build failed") {
		t.Fatalf("second serve repeated front-end failure note:\n%s", raw2)
	}
	npmCalls2, _ := os.ReadFile(npmLog)
	if lines := strings.Split(strings.TrimSpace(string(npmCalls2)), "\n"); len(lines) != 1 {
		t.Fatalf("second serve re-invoked npm (total %d calls, want 1):\n%s", len(lines), npmCalls2)
	}
}
