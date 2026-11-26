package keep

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveDrainExecutable(t *testing.T) {
	t.Setenv("TILLR_LAUNCHER", "/custom/launcher/tillr-drain")
	if got := resolveDrainExecutable(); got != "/custom/launcher/tillr-drain" {
		t.Fatalf("resolveDrainExecutable() = %q, want /custom/launcher/tillr-drain", got)
	}

	// Inside a go test binary (basename keep.test, not matching drainNameRE),
	// resolveDrainExecutable falls back to "tillr-drain".
	t.Setenv("TILLR_LAUNCHER", "")
	if got := resolveDrainExecutable(); got != "tillr-drain" {
		t.Fatalf("resolveDrainExecutable() = %q, want tillr-drain", got)
	}
}

func TestKeeperPropagatesInstallRootToLoopAndGateAdapter(t *testing.T) {
	repo := repoRoot(t)
	tmp := t.TempDir()

	// Build tillr-loop so we can verify end-to-end runner resolution in the adapter.
	loopBin := filepath.Join(tmp, "tillr-loop")
	buildCmd := exec.Command("go", "build", "-o", loopBin, ".")
	buildCmd.Dir = filepath.Join(repo, "tools", "tillr-loop")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-loop: %v\n%s", err, out)
	}

	// Custom TILLR_INSTALL_ROOT with a stub change-gate.py and python3 on PATH.
	customInstallRoot := filepath.Join(tmp, "custom-install-root")
	gateDir := filepath.Join(customInstallRoot, "tillr-gate")
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runnerLog := filepath.Join(tmp, "runner-path.txt")
	gateScript := filepath.Join(gateDir, "change-gate.py")
	if err := os.WriteFile(gateScript, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stub python3 records the gate runner script path ($1) and prints a passing verdict JSON.
	pythonStub := `#!/bin/sh
printf '%s\n' "$1" > "` + runnerLog + `"
printf '{"kind":"gate.verdict","verdict":"pass","checks":[]}\n'
`
	if err := os.WriteFile(filepath.Join(fakeBin, "python3"), []byte(pythonStub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "tillr-gate"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "tillr-land"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "tillr-item"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Set up a pack home where HOME/.local/share/tillr has a different (unused) gate dir,
	// and a stub `go` builds a `tillr-drain` child that invokes `tillr-loop gate`.
	home := filepath.Join(tmp, "home")
	packShare := filepath.Join(home, ".local", "share", "tillr")
	if err := os.MkdirAll(filepath.Join(packShare, "tillr-drain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packShare, "tillr-gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packShare, "tillr-gate", "change-gate.py"), []byte("# wrong\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	envLog := filepath.Join(tmp, "child-install-root.txt")
	loopErrLog := filepath.Join(tmp, "loop-err.txt")
	workRoot := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(workRoot, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workRoot, ".tillr.toml"), []byte("[trunk]\nbranch = \"main\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stub go builds a tillr-drain script that records TILLR_INSTALL_ROOT and invokes tillr-loop describe.
	goStub := `#!/bin/sh
if [ "$1" = build ]; then
  cat > "$3" <<'EOF'
#!/bin/sh
printf '%s\n' "$TILLR_INSTALL_ROOT" > "` + envLog + `"
"` + loopBin + `" self-check --root "` + workRoot + `" --worktree "` + workRoot + `" --store "` + filepath.Join(workRoot, ".tillr") + `" --run run-1 --base HEAD --head HEAD --lane lane-1 --item item-1 >"` + runnerLog + `.tmp" 2>"` + loopErrLog + `" || true
mv "` + runnerLog + `.tmp" "` + runnerLog + `"
exec sleep 5
EOF
  chmod +x "$3"
else
  exit 1
fi
`
	if err := os.WriteFile(filepath.Join(fakeBin, "go"), []byte(goStub), 0o755); err != nil {
		t.Fatal(err)
	}

	launcher := filepath.Join(repo, "yolo-packs", "tillr", "programs", "tillr-drain", "run")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_LAUNCHER", launcher)
	t.Setenv("TILLR_INSTALL_ROOT", customInstallRoot)

	var out, errBuf bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--root", workRoot}, &out, &errBuf)
	}()

	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(runnerLog); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Stop the keeper so Run exits cleanly.
	var stopOut, stopErr bytes.Buffer
	_ = Run([]string{"--root", workRoot, "--stop"}, &stopOut, &stopErr)

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("keep.Run exited %d: stderr=%s stdout=%s", code, errBuf.String(), out.String())
		}
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for keeper to stop")
	}

	gotEnv, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatalf("read child env log: %v (stdout=%s stderr=%s)", err, out.String(), errBuf.String())
	}
	if strings.TrimSpace(string(gotEnv)) != customInstallRoot {
		t.Fatalf("child TILLR_INSTALL_ROOT = %q, want %q", strings.TrimSpace(string(gotEnv)), customInstallRoot)
	}

	gotRunner, err := os.ReadFile(runnerLog)
	if err != nil {
		loopOut, _ := os.ReadFile(loopErrLog)
		t.Fatalf("read runner log: %v (tillr-loop stderr: %s)", err, string(loopOut))
	}
	if !strings.Contains(string(gotRunner), gateScript) {
		loopOut, _ := os.ReadFile(loopErrLog)
		t.Fatalf("tillr-loop describe did not resolve %q:\nstdout:\n%s\nstderr:\n%s", gateScript, string(gotRunner), string(loopOut))
	}
}
