package run_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	// The stand-in `pi` programs these cases write do not answer `pi --features`, so the
	// features they stand for are stated to the adapter instead of probed.
	os.Setenv("TILLR_PI_FEATURES", "parked-tool-calls")
	if os.Getenv("TILLR_RUN_BIN") == "" {
		root := os.Getenv("YOLO_DURABLE_DIR")
		if root == "" {
			root = filepath.Join("..", "..", "..", ".yolo", "durable")
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return 1
		}
		dir, err := os.MkdirTemp(root, "tillr-run-bin-")
		if err != nil {
			return 1
		}
		defer os.RemoveAll(dir)
		bin := filepath.Join(dir, "tillr-run")
		build := exec.Command("go", "build", "-o", bin, "..")
		if out, err := build.CombinedOutput(); err != nil {
			os.Stderr.Write(out)
			return 1
		}
		os.Setenv("TILLR_RUN_BIN", bin)
	}
	return m.Run()
}
