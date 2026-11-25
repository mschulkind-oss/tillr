package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func writeFixtureBackend(t *testing.T, backendsDir string, tokens string) {
	t.Helper()
	dir := filepath.Join(backendsDir, "fixture")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pause-kind"), []byte("stops-where-it-stands\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run-fixture"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		`out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--out" ]; then out=$a; fi
  prev=$a
done
printf '# run report\ntokens: ` + tokens + `\ncomplete: true\nresult: done\n---\n' > "$out"
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "extract-report"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestInstallDefaults(t *testing.T) {
	dir := t.TempDir()
	consumer := filepath.Join(dir, "consumer")
	if err := os.MkdirAll(consumer, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit := exec.Command("git", "init")
	gitInit.Dir = consumer
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	installRoot := filepath.Join(dir, "install")
	installedBackends := filepath.Join(installRoot, "tillr-run", "backends")
	writeFixtureBackend(t, installedBackends, "42")

	db := filepath.Join(dir, "board.sqlite3")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	laneRoot := filepath.Join(dir, "lanes")
	exe, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}

	runExec := func(runID string, extraArgs ...string) (int, []byte) {
		t.Helper()
		reqPath := filepath.Join(dir, runID+"-request.json")
		reqBody := envelope(t, "run.request", runID, request{
			RunID:      runID,
			ItemID:     "item_042",
			LaneID:     "",
			Backend:    "fixture",
			Command:    []string{"fixture/run-fixture"},
			WorkingDir: ".",
			TimeoutMS:  60000,
			EnvRefs:    []string{},
		})
		if err := os.WriteFile(reqPath, []byte(reqBody), 0644); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"exec", "--store", db, "--lane-root", laneRoot, "--input", reqPath}, extraArgs...)
		cmd := exec.Command(exe, args...)
		cmd.Dir = consumer
		cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"), "TILLR_INSTALL_ROOT="+installRoot)
		out, err := cmd.CombinedOutput()
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), out
		}
		if err != nil {
			t.Fatalf("exec failed: %v", err)
		}
		return 0, out
	}

	decodeTokens := func(out []byte) int64 {
		t.Helper()
		var env struct {
			Payload struct {
				Usage *struct {
					Tokens *int64 `json:"tokens"`
				} `json:"usage"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("decode output: %v\n%s", err, out)
		}
		if env.Payload.Usage == nil || env.Payload.Usage.Tokens == nil {
			t.Fatalf("missing usage tokens in %s", out)
		}
		return *env.Payload.Usage.Tokens
	}

	// (a) Consumer repo with no tools/tillr-run/backends succeeds using TILLR_INSTALL_ROOT.
	code, out := runExec("run-install")
	if code != 0 {
		t.Fatalf("(a) exit %d: %s", code, out)
	}
	if got := decodeTokens(out); got != 42 {
		t.Fatalf("(a) tokens = %d, want 42", got)
	}

	// (b) Decoy <consumer>/tools/tillr-run/backends is ignored when TILLR_INSTALL_ROOT is set.
	decoyBackends := filepath.Join(consumer, "tools", "tillr-run", "backends")
	writeFixtureBackend(t, decoyBackends, "999")
	code, out = runExec("run-decoy")
	if code != 0 {
		t.Fatalf("(b) exit %d: %s", code, out)
	}
	if got := decodeTokens(out); got != 42 {
		t.Fatalf("(b) tokens = %d, want 42 (decoy was read)", got)
	}

	// (c) Missing installed directory is refused naming the installed path (no fallback to decoy).
	if err := os.RemoveAll(installedBackends); err != nil {
		t.Fatal(err)
	}
	code, out = runExec("run-missing")
	if code == 0 {
		t.Fatalf("(c) expected non-zero exit when installed backends dir is missing, got 0: %s", out)
	}
	if !strings.Contains(string(out), installedBackends) {
		t.Fatalf("(c) expected error naming %q, got: %s", installedBackends, out)
	}

	// (d) Explicit --backends-dir still wins even when installed backends dir is missing.
	explicitBackends := filepath.Join(dir, "explicit-backends")
	writeFixtureBackend(t, explicitBackends, "77")
	code, out = runExec("run-explicit", "--backends-dir", explicitBackends)
	if code != 0 {
		t.Fatalf("(d) exit %d: %s", code, out)
	}
	if got := decodeTokens(out); got != 77 {
		t.Fatalf("(d) tokens = %d, want 77", got)
	}
}
