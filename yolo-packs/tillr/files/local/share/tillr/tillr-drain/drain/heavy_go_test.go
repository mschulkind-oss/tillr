package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeavyGoReplaySelectsOneTestAndRejectsOtherFailures(t *testing.T) {
	root := scratchUnderDurable(t)
	mod := filepath.Join(root, "tools/example")
	if err := os.MkdirAll(mod, 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(mod, "go.mod"), "module example.com/suite\ngo 1.22\n")
	config := filepath.Join(root, "gitconfig")
	writeForkFile(t, config, "[test]\n value = original\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	writeForkFile(t, filepath.Join(mod, "suite_test.go"), `package suite
import ("os/exec"; "testing")
func TestPrivateGit(t *testing.T) {
    if out, err := exec.Command("git", "config", "--global", "test.value").Output(); err != nil || string(out) != "original\n" {
        t.Fatalf("missing inherited config: %q, %v", out, err)
    }
    if err := exec.Command("git", "config", "--global", "test.value", "changed").Run(); err != nil { t.Fatal(err) }
}
func TestSibling(t *testing.T) { t.Fatal("not the selected test") }
func TestSelected(t *testing.T) {
    t.Run("passes", func(t *testing.T) {})
    t.Run("fails", func(t *testing.T) { t.Fatal("selected failure") })
}
`)
	s := &ProcessSteps{}
	for name, want := range map[string]string{"TestSelected/passes": "pass", "TestSelected/fails": "fail", "TestNotAddedYet": "pass", "TestPrivateGit": "pass"} {
		t.Run(name, func(t *testing.T) {
			id := "go-test:tools/example:example.com/suite:" + name
			got, err := s.JudgeRequired(context.Background(), root, id, "")
			if err != nil || got.Result != want {
				t.Fatalf("wanted %s: %+v, %v", want, got, err)
			}
		})
	}
	if raw, err := os.ReadFile(config); err != nil || !strings.Contains(string(raw), "original") {
		t.Fatalf("replay modified the inherited Git config: %q, %v", raw, err)
	}
	if err := os.WriteFile(filepath.Join(mod, "broken.go"), []byte("syntax error"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JudgeRequired(context.Background(), root, "go-test:tools/example:example.com/suite:TestSelected", ""); err == nil {
		t.Fatal("a build failure was treated as a test verdict")
	}
}
