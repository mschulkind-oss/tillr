package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An item's own `{{` is carried into the brief intact, and staging states how many such pairs
// the brief carries in the lane's `brace-literals` file. Each adapter's preflight accepts the
// brief then, and still refuses the same brief when staging stated none.
func TestEachAdapterPreflightAcceptsABracePairStagingStated(t *testing.T) {
	for _, c := range []struct{ name, adapter, harness string }{
		{"pi", filepath.Join("backends", "pi", "run-pi"), "pi"},
		{"claude", filepath.Join("backends", "claude", "run-claude"), "claude"},
	} {
		t.Run(c.name, func(t *testing.T) {
			adapter, err := filepath.Abs(c.adapter)
			if err != nil {
				t.Fatal(err)
			}
			base := scratch(t, "brace-"+c.name)
			bin := filepath.Join(base, "bin")
			writeExecutable(t, filepath.Join(bin, c.harness), "#!/bin/sh\ncat > /dev/null\n")
			worktree := filepath.Join(base, "lane", "worktree")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			brief := strings.Replace(filledBrief(t, worktree), "a seated run reaches its adapter", "a title with {{x}} and {{TITLE}} in it", 1)
			if err := os.WriteFile(filepath.Join(base, "lane", "brief.md"), []byte(brief), 0o644); err != nil {
				t.Fatal(err)
			}
			launch := func() (string, error) {
				cmd := exec.Command(adapter, "--backend", c.name, "--model", "fixture-model", "--effort", "medium")
				cmd.Dir = worktree
				for _, entry := range os.Environ() {
					if !strings.HasPrefix(entry, "TILLR_RESUME_PROMPT_FILE=") && !strings.HasPrefix(entry, "TILLR_SESSION_MODE=") {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				cmd.Env = append(cmd.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				out, err := cmd.CombinedOutput()
				return string(out), err
			}
			if out, err := launch(); err == nil || !strings.Contains(out, "refused") {
				t.Fatalf("%s accepted brace pairs staging did not state: %v: %s", c.name, err, out)
			}
			if err := os.WriteFile(filepath.Join(base, "lane", "brace-literals"), []byte("2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := launch(); err != nil || strings.Contains(out, "refused") {
				t.Fatalf("%s refused brace pairs staging stated: %v: %s", c.name, err, out)
			}
		})
	}
}
