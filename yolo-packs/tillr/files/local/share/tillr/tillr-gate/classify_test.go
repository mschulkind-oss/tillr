package main_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

func classifyInvocation(dir, input string) *exec.Cmd {
	cmd := exec.Command(gateBin, "classify")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+dir)
	cmd.Stdin = strings.NewReader(input)
	return cmd
}

func TestClassifyFaceOrderedBatch(t *testing.T) {
	root := t.TempDir()
	decls := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(decls, 0o755); err != nil {
		t.Fatal(err)
	}
	tripwire := filepath.Join(root, "command-started")
	decl, err := json.Marshal(map[string]any{
		"id": "tripwire", "label": "tripwire", "command": []string{"touch", tripwire},
		"scope": "tree", "tier": "fast", "budget_ms": 50, "reads": []string{"."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decls, "tripwire.json"), decl, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := classifyInvocation(root, `[
		{"check":"tripwire","worktree":".","source":"check-command","exit_code":6},
		{"check":"tripwire","worktree":".","source":"judging-program","exit_code":6},
		{"check":"named","commit":"older-tree","source":"judging-program","exit_code":77},
		{"check":"tripwire","worktree":".","source":"result-word","word":"CACHED","detail":"original log\n"},
		{"check":"go-build","source":"go-module-build","exit_code":4},
		{"check":"documents","source":"document-render","exit_code":1,"report_read":true,"has_finding":true},
		{"check":"legacy","source":"legacy-check","exit_code":1,"legacy":{"group":"named"}}
	]`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("classify: %v\n%s", err, out)
	}
	var got []checkresult.Result
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	want := []checkresult.Result{
		{Outcome: checkresult.Fail, Detail: "failed"},
		{Outcome: checkresult.Unknown, Detail: "stalled"},
		{Outcome: checkresult.Unavailable, Detail: "dropped"},
		{Outcome: checkresult.Pass, Detail: "cached", RawWord: "CACHED", RawDetail: "original log\n"},
		{Outcome: checkresult.Fail, Detail: "failed"},
		{Outcome: checkresult.Fail, Detail: "failed"},
		{Outcome: checkresult.Unknown, Detail: "error"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if _, err := os.Stat(tripwire); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command tripwire should not exist: stat returned %v", err)
	}
}

func TestClassifyFaceHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"classify", "--help"}} {
		code, out := run(t, args...)
		if code != record.ExitOK || !strings.Contains(string(out), "classify") {
			t.Fatalf("%v: exit %d, %q", args, code, out)
		}
	}
}

func TestClassifyFaceInput(t *testing.T) {
	root := t.TempDir()
	if out, err := classifyInvocation(root, "[]\n").CombinedOutput(); err != nil || string(out) != "[]\n" {
		t.Fatalf("empty batch: %s (%v)", out, err)
	}
	for _, input := range []string{"", "null", "{}", "[] []", "[] trailing", "[null]", `[{"exit_cdoe":0}]`} {
		t.Run(input, func(t *testing.T) {
			out, err := classifyInvocation(root, input).Output()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != record.ExitUsage || len(out) != 0 {
				t.Fatalf("invalid batch produced %q (%v); want usage error and no stdout", out, err)
			}
		})
	}
	cmd := classifyInvocation(root, "[]")
	cmd.Args = append(cmd.Args, "extra")
	out, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != record.ExitUsage || len(out) != 0 {
		t.Fatalf("extra argument produced %q (%v); want usage error and no stdout", out, err)
	}
}
