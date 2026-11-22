package main_test

import (
	"bytes"
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

type aggregateFaceRun struct {
	Outcome  checkresult.Outcome `json:"outcome"`
	Detail   string              `json:"detail"`
	ExitCode int                 `json:"exit_code"`
}

type aggregateFaceReply struct {
	Checks []checkresult.Result `json:"checks"`
	Run    aggregateFaceRun     `json:"run"`
}

func classifyArgsInvocation(dir, input string, args ...string) *exec.Cmd {
	cmd := exec.Command(gateBin, append([]string{"classify"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+dir)
	cmd.Stdin = strings.NewReader(input)
	return cmd
}

func TestClassifyFaceAggregateOrdersMatchPackageAndPlainChecks(t *testing.T) {
	root := scratch(t, "classify-aggregate")
	decls := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(decls, 0o755); err != nil {
		t.Fatal(err)
	}
	tripwire := filepath.Join(root, "aggregate-command-started")
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

	batches := []struct {
		name  string
		input string
	}{
		{"empty", "[]\n"},
		{
			"omitted-outcomes-only",
			`[
				{"check":"d","source":"result-word","word":"disabled"},
				{"check":"q","source":"result-word","word":"QUARANTINED"},
				{"check":"dr","source":"judging-program","exit_code":77},
				{"check":"sk","source":"result-word","word":"skipped: no changed path is read"}
			]`,
		},
		{
			"fail-and-error",
			`[
				{"check":"tripwire","worktree":".","source":"check-command","exit_code":1},
				{"check":"tripwire","worktree":".","source":"check-command","exit_code":4}
			]`,
		},
		{
			"fail-error-and-stall",
			`[
				{"check":"tripwire","worktree":".","source":"check-command","exit_code":1},
				{"check":"tripwire","worktree":".","source":"check-command","exit_code":4},
				{"check":"tripwire","worktree":".","source":"judging-program","exit_code":6}
			]`,
		},
		{
			"pass-and-not-run",
			`[
				{"check":"tripwire","worktree":".","source":"check-command","exit_code":0},
				{"check":"legacy","source":"legacy-check","exit_code":1,"legacy":{"not_run":true}}
			]`,
		},
		{
			"pass-and-legacy-sentinel-skip",
			`[
				{"check":"tripwire","worktree":".","source":"result-word","word":"CACHED","detail":"cached log\n"},
				{"check":"legacy","source":"legacy-check","exit_code":1,"legacy":{"sentinel_skip":true}}
			]`,
		},
	}

	orders := []checkresult.AggregateOrder{
		checkresult.RunnerOrder,
		checkresult.ShadowOrder,
		checkresult.LegacyGateOrder,
		checkresult.LegacyReplayOrder,
	}

	for _, batch := range batches {
		plainOut, err := classifyArgsInvocation(root, batch.input).Output()
		if err != nil {
			t.Fatalf("plain classify %s: %v", batch.name, err)
		}
		var plainChecks []checkresult.Result
		if err := json.Unmarshal(plainOut, &plainChecks); err != nil {
			t.Fatalf("decode plain %s (%s): %v", batch.name, plainOut, err)
		}
		wantPlainBytes, err := json.Marshal(plainChecks)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(plainOut, append(wantPlainBytes, '\n')) {
			t.Fatalf("plain %s output changed: got %q, want %q", batch.name, plainOut, append(wantPlainBytes, '\n'))
		}

		for _, order := range orders {
			t.Run(batch.name+"/"+string(order), func(t *testing.T) {
				out, err := classifyArgsInvocation(root, batch.input, "--aggregate", string(order)).Output()
				if err != nil {
					var exit *exec.ExitError
					if errors.As(err, &exit) {
						t.Fatalf("aggregate %s: exit %d, stderr=%q", order, exit.ExitCode(), exit.Stderr)
					}
					t.Fatalf("aggregate %s: %v", order, err)
				}

				var rawShape map[string]json.RawMessage
				if err := json.Unmarshal(out, &rawShape); err != nil {
					t.Fatalf("decode raw shape %s: %v", out, err)
				}
				if len(rawShape) != 2 || rawShape["checks"] == nil || rawShape["run"] == nil {
					t.Fatalf("unexpected top-level fields in %s", out)
				}
				var rawRun map[string]json.RawMessage
				if err := json.Unmarshal(rawShape["run"], &rawRun); err != nil {
					t.Fatalf("decode raw run %s: %v", rawShape["run"], err)
				}
				if len(rawRun) != 3 || rawRun["outcome"] == nil || rawRun["detail"] == nil || rawRun["exit_code"] == nil {
					t.Fatalf("unexpected run fields in %s", rawShape["run"])
				}
				if !bytes.Equal(append(bytes.TrimSpace(rawShape["checks"]), '\n'), plainOut) {
					t.Fatalf("checks bytes %q != plain output %q", rawShape["checks"], plainOut)
				}

				dec := json.NewDecoder(bytes.NewReader(out))
				dec.DisallowUnknownFields()
				var got aggregateFaceReply
				if err := dec.Decode(&got); err != nil {
					t.Fatalf("strict decode %s: %v", out, err)
				}
				if !reflect.DeepEqual(got.Checks, plainChecks) {
					t.Fatalf("checks = %+v, want %+v", got.Checks, plainChecks)
				}
				wantRun := checkresult.Aggregate(order, plainChecks)
				gotRun := checkresult.RunResult{
					Outcome:  got.Run.Outcome,
					Detail:   got.Run.Detail,
					ExitCode: got.Run.ExitCode,
				}
				if gotRun != wantRun {
					t.Fatalf("run = %+v, want %+v", gotRun, wantRun)
				}
			})
		}
	}

	if _, err := os.Stat(tripwire); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command tripwire should not exist: stat returned %v", err)
	}
}

func TestClassifyFaceAggregateRejectsUnknownOrderAndInvalidInput(t *testing.T) {
	root := scratch(t, "classify-aggregate-invalid")
	for _, badOrder := range []string{"unknown", "", "RUNNER", "legacy_gate"} {
		t.Run("order="+badOrder, func(t *testing.T) {
			out, err := classifyArgsInvocation(root, "[]\n", "--aggregate="+badOrder).Output()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != record.ExitUsage || len(out) != 0 {
				t.Fatalf("--aggregate=%q: out=%q err=%v; want exit %d and empty stdout", badOrder, out, err, record.ExitUsage)
			}
			stderr := string(exit.Stderr)
			for _, name := range []string{"runner", "shadow", "legacy-gate", "legacy-replay"} {
				if !strings.Contains(stderr, name) {
					t.Fatalf("--aggregate=%q stderr %q missing order name %q", badOrder, stderr, name)
				}
			}
		})
	}

	for _, args := range [][]string{
		{"--aggregate"},
		{"--aggregate", "runner", "extra"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			out, err := classifyArgsInvocation(root, "[]\n", args...).Output()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != record.ExitUsage || len(out) != 0 {
				t.Fatalf("args %v: out=%q err=%v; want exit %d and empty stdout", args, out, err, record.ExitUsage)
			}
		})
	}

	for _, badInput := range []string{"", "null", "{}", "[null]", `[{"exit_cdoe":0}]`} {
		t.Run("stdin="+badInput, func(t *testing.T) {
			out, err := classifyArgsInvocation(root, badInput, "--aggregate", "runner").Output()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != record.ExitUsage || len(out) != 0 {
				t.Fatalf("stdin %q: out=%q err=%v; want exit %d and empty stdout", badInput, out, err, record.ExitUsage)
			}
		})
	}

	code, helpOut := run(t, "classify", "--help")
	if code != record.ExitOK || !strings.Contains(string(helpOut), "aggregate") {
		t.Fatalf("classify --help: exit %d, %q", code, helpOut)
	}
}
