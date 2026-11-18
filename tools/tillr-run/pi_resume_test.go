package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiContinuationRequiresExistingSession(t *testing.T) {
	adapter, err := filepath.Abs("backends/pi/run-pi")
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "pi-resume")
	bin := filepath.Join(dir, "bin")
	argv := filepath.Join(dir, "argv")
	input := filepath.Join(dir, "input")
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$STUB_ARGV\"\ncat > \"$STUB_INPUT\"\n")
	worktree := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	brief := filledBrief(t, worktree)
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(brief), 0644); err != nil {
		t.Fatal(err)
	}
	session := "12345678-1234-1234-1234-123456789abc"
	if err := os.WriteFile(filepath.Join(dir, "session-id"), []byte(session+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(dir, "resume-prompt")
	notice := "The gate refused documents. Repair the anchor and commit.\n"
	if err := os.WriteFile(prompt, []byte(notice), 0644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"new", "resume"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
			cmd.Dir = worktree
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_ARGV="+argv, "STUB_INPUT="+input, "TILLR_SESSION_MODE="+mode, "TILLR_RESUME_PROMPT_FILE=")
			expected := brief
			if mode == "resume" {
				cmd.Env = append(cmd.Env, "TILLR_RESUME_PROMPT_FILE="+prompt)
				expected = notice
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("adapter: %v %s", err, out)
			}
			args, err := os.ReadFile(argv)
			if err != nil {
				t.Fatal(err)
			}
			flag := "--session-id"
			if mode == "resume" {
				flag = "--session"
				if strings.Contains(string(args), "--session-id\n") {
					t.Fatal("a missing session would start cold")
				}
			}
			if !strings.Contains(string(args), flag+"\n"+session+"\n") {
				t.Fatalf("wrong session argv: %s", args)
			}
			raw, err := os.ReadFile(input)
			if err != nil || string(raw) != expected {
				t.Fatalf("prompt: %v %q", err, raw)
			}
		})
	}
}
