package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestForkDriverInputsSelectDrainIntegrationAtLanding(t *testing.T) {
	checks, err := filepath.Abs("../../tools/gate/checks")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"bin/pi-forks", "tools/fork-check.py", "unrelated.txt"} {
		t.Run(path, func(t *testing.T) {
			root, bin := scratch(t, "driver-repo"), scratch(t, "driver-bin")
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "-q")
			git("commit", "--allow-empty", "-qm", "base")
			base := git("rev-parse", "HEAD")
			for _, dir := range []string{filepath.Dir(path), "tools/tillr-drain", "checks"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, path), []byte("changed\n"), 0644); err != nil {
				t.Fatal(err)
			}
			git("add", "--", path)
			git("commit", "-qm", "change")
			head := git("rev-parse", "HEAD")
			// Run the real change-gate declaration; substitute only the Go command.
			for _, file := range []string{"pi-forks-drain.json", "pi-forks-drain.py", "change_common.py", "roadmap_sections.py"} {
				raw, err := os.ReadFile(filepath.Join(checks, file))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(root, "tools/gate/checks"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "tools/gate/checks", file), raw, 0644); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(file, ".json") {
					if err := os.WriteFile(filepath.Join(root, "checks", file), raw, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$DRIVER_TEST_COMMAND\"\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(bin, "command")
			cmd := exec.Command(gateBin, "checks", "--fast-tree", "--base", base, "--head", head, "--worktree", root, "--checks", filepath.Join(root, "checks"), "--logs", filepath.Join(bin, "logs"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "DRIVER_TEST_COMMAND="+log, "PYTHONDONTWRITEBYTECODE=1")
			out, err := cmd.CombinedOutput()
			if path == "unrelated.txt" {
				if _, statErr := os.Stat(log); err != nil || !os.IsNotExist(statErr) {
					t.Fatalf("unrelated change invoked the test: %v, %v: %s", err, statErr, out)
				}
				return
			}
			if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
				t.Fatalf("test failure did not fail the landing gate: %v: %s", err, out)
			}
			argv, err := os.ReadFile(log)
			if err != nil || strings.TrimSpace(string(argv)) != "test -count=1 ./drain -run ^TestForkBuildDeliveryAndSuccessiveLaneBases$" {
				t.Fatalf("wrong targeted command: %v: %s", err, argv)
			}
		})
	}
}
