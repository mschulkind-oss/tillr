package drain_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestForkBuildCommandContracts(t *testing.T) {
	cmd := exec.Command("python3", "../fork-gate.test.py")
	cmd.Env = append(os.Environ(),
		"PYTHONDONTWRITEBYTECODE=1",
		"FORK_GATE_TEST_SCRATCH="+scratch(t, "fork-parser"),
		"TILLR_GATE_BIN="+sibling(t, "gate"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fork gate parser contracts: %v\n%s", err, out)
	}
}

func TestForkDispatchMaterializesAndFencesTheAssignedLane(t *testing.T) {
	f := newForkLaneFixture(t)
	// Staging owns materialization; the stub copies a local delivered fork, not a network upstream.
	driver := filepath.Join(f.root, "bin", "pi-forks")
	if err := os.MkdirAll(filepath.Dir(driver), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, driver, "#!/bin/sh\nset -eu\n[ \"$1\" = setup ]\nif [ \"$2\" = --fork ]; then\n git clone -q \"$PI_FORKS_ROOT/.forks/lanes/lane-1/pi\" \"$PI_FORKS_ROOT/.forks/pi\"\nelse\n git clone -q \"$PI_FORKS_ROOT/.forks/pi\" \"$PI_FORKS_ROOT/.forks/lanes/$3/$5\"\nfi\n")
	if err := os.Chmod(driver, 0755); err != nil {
		t.Fatal(err)
	}
	if err := f.stager().Stage(context.Background(), f.spec("lane-3")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.laneRoot, "lane-3")
	target := filepath.Join(f.root, ".forks/lanes/lane-3/pi")
	for name, want := range map[string]string{drain.TargetFile: target, drain.PatchRefFile: "refs/heads/patch/pi", drain.BaseSHAFile: f.forkBase} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(body)) != want {
			t.Fatalf("%s = %s, want %s", name, body, want)
		}
	}
	for _, name := range []string{drain.BriefFile, drain.FenceFile} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), ".forks/lanes/lane-3/pi") || strings.Contains(string(body), ".forks/lanes/lane-1/pi") {
			t.Fatalf("%s did not rebind the fence: %s", name, body)
		}
		if name == drain.BriefFile && !strings.Contains(string(body), "Do not run `bin/pi-forks export`") {
			t.Fatalf("brief did not reserve delivery to the lander: %s", body)
		}
	}
}
