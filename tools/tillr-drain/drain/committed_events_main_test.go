package drain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-loop/loop"
	"github.com/mschulkind-oss/tillr/store"
)

// The loop process reads its store with the narrow selected-event cache on (loopFace), so
// this package's tests do too: every read the suite makes goes through the cache the loop
// uses without holding unselected event kinds in memory.
func TestMain(m *testing.M) {
	if os.Getenv("TILLR_DRAIN_EXEC_LOOP_ADAPTER") == "1" {
		_ = os.Unsetenv("TILLR_DRAIN_EXEC_LOOP_ADAPTER")
		os.Exit(loop.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(runDrainTests(m))
}

func runDrainTests(m *testing.M) int {
	// The stand-in `pi` programs these cases write do not answer `pi --features`, so the
	// features they stand for are stated to the adapter instead of probed.
	os.Setenv("TILLR_PI_FEATURES", "parked-tool-calls")
	if os.Getenv("TILLR_RUN_BIN") == "" {
		if tillrRunDir, err := filepath.Abs(filepath.Join("..", "..", "tillr-run")); err == nil {
			if _, err := os.Stat(filepath.Join(tillrRunDir, "go.mod")); err == nil {
				dir, err := os.MkdirTemp("", "tillr-run-bin-")
				if err != nil {
					return 1
				}
				defer os.RemoveAll(dir)
				bin := filepath.Join(dir, "tillr-run")
				build := exec.Command("go", "-C", tillrRunDir, "build", "-o", bin, ".")
				if out, err := build.CombinedOutput(); err != nil {
					os.Stderr.Write(out)
					return 1
				}
				os.Setenv("TILLR_RUN_BIN", bin)
			}
		}
	}
	if os.Getenv("TILLR_DRAIN_TEST_GATE_BIN") == "" {
		if tillrGateDir, err := filepath.Abs(filepath.Join("..", "..", "tillr-gate")); err == nil {
			if _, err := os.Stat(filepath.Join(tillrGateDir, "go.mod")); err == nil {
				dir, err := os.MkdirTemp("", "tillr-gate-bin-")
				if err != nil {
					return 1
				}
				defer os.RemoveAll(dir)
				bin := filepath.Join(dir, "tillr-gate")
				build := exec.Command("go", "-C", tillrGateDir, "build", "-o", bin, ".")
				if out, err := build.CombinedOutput(); err != nil {
					os.Stderr.Write(out)
					return 1
				}
				os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				os.Setenv("TILLR_DRAIN_TEST_GATE_BIN", bin)
			}
		}
	}
	store.CacheSelectedEvents()
	defer runTestCleanups()
	return m.Run()
}
