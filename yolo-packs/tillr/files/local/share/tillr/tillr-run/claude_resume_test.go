package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeContinuationUsesResumeAndKeepsSessionFlagOrder(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "claude", "run-claude"))
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "claude-resume")
	bin := filepath.Join(dir, "bin")
	argvLog := filepath.Join(dir, "argv")
	inputLog := filepath.Join(dir, "input")
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$STUB_ARGV\"\ncat > \"$STUB_INPUT\"\n")
	worktree := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	brief := filledBrief(t, worktree)
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(brief), 0644); err != nil {
		t.Fatal(err)
	}
	const session = "12345678-1234-1234-1234-123456789abc"
	if err := os.WriteFile(filepath.Join(dir, "session-id"), []byte(session+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(dir, "resume-prompt")
	notice := "The session was interrupted by a restart. Continue the same attempt.\n"
	if err := os.WriteFile(prompt, []byte(notice), 0644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"new", "resume"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(adapter, "--backend", "claude", "--model", "fixture-model", "--effort", "medium", "--", "--tools", "Read,Edit,Bash")
			cmd.Dir = worktree
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_ARGV="+argvLog, "STUB_INPUT="+inputLog, "TILLR_SESSION_MODE="+mode, "TILLR_RESUME_PROMPT_FILE=")
			flag := "--session-id"
			wantPrompt := brief
			if mode == "resume" {
				flag = "--resume"
				wantPrompt = notice
				cmd.Env = append(cmd.Env, "TILLR_RESUME_PROMPT_FILE="+prompt)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("adapter: %v %s", err, out)
			}
			raw, err := os.ReadFile(argvLog)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			want := []string{"-p", "--output-format", "stream-json", "--verbose", "--model", "fixture-model", "--effort", "medium", flag, session, "--strict-mcp-config", "--dangerously-skip-permissions", "--tools", "Read,Edit,Bash"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("argv=%q want=%q", args, want)
			}
			raw, err = os.ReadFile(inputLog)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != wantPrompt {
				t.Fatalf("first prompt=%q want=%q", raw, wantPrompt)
			}
		})
	}
}
