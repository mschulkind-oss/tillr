package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
)

func hookStub(t *testing.T, names []string, omit string) (*definition.Hooks, string) {
	t.Helper()
	root := scratchUnderDurable(t)
	log := filepath.Join(root, "calls.jsonl")
	script := filepath.Join(root, "hook program.py")
	answers := map[string][]string{}
	for _, name := range names {
		if name == omit {
			continue
		}
		for _, flag := range hookFlags(name) {
			if flag != omit {
				answers[name] = append(answers[name], flag)
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"hooks": answers})
	writeForkFile(t, script, `import json, sys
args = sys.argv[1:]
assert args.pop(0) == 'literal argument with spaces'
with open(`+fmtPython(log)+`, 'a') as f:
    f.write(json.dumps(args) + '\n')
if args == ['describe']:
    print(`+fmtPython(string(raw))+`)
`)
	return &definition.Hooks{Argv: []string{"python3", script, "literal argument with spaces"}, Names: names}, log
}

func fmtPython(s string) string { raw, _ := json.Marshal(s); return string(raw) }

func TestDefinitionHookArgumentsAndPreflight(t *testing.T) {
	names := []string{"gate", "land", "verdict", "replay", "rolling", "fixer-gate", "complete-item", "after-completion", "detail"}
	hooks, log := hookStub(t, names, "")
	root := filepath.Dir(log)
	itemBin := filepath.Join(root, "item")
	writeForkFile(t, itemBin, "#!/bin/sh\necho '--store --item --state --now --reason --by --resolves-via --require-verdicts'\n")
	if err := os.Chmod(itemBin, 0755); err != nil {
		t.Fatal(err)
	}
	steps := &ProcessSteps{Root: root, Store: "store", ItemBin: itemBin}
	steps.ConfigureHooks(hooks, nil)
	if err := steps.CheckPrograms(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	c := Candidate{ItemID: "item", LaneID: "lane", RunID: "run", Worktree: root, BaseSHA: "base", HeadSHA: "head", ObservedTrunk: "trunk", BaseRef: "ref", WorkerHeadSHA: "worker"}
	for _, name := range names[:6] {
		var argv []string
		switch name {
		case "gate":
			argv = steps.programArgv(steps.GateProgram, c)
		case "land":
			argv = steps.programArgv(steps.LandProgram, c, landArgs(c)...)
		case "verdict":
			argv = steps.programArgv(steps.VerdictProgram, c, steps.verdictArgs("pass")...)
		case "fixer-gate":
			argv = steps.programArgv(steps.fixerGate(c), c)
		case "replay":
			argv = steps.commandArgv(strings.ReplaceAll(steps.ReplayProgram, "{only}", "check"))
		case "rolling":
			argv = steps.commandArgv(strings.ReplaceAll(steps.RollingProgram, "{since}", "--all"))
		}
		cmd := steps.stepCommand(context.Background(), argv, nil)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 7 {
		t.Fatalf("unexpected calls: %s", raw)
	}
	for i, name := range names[:6] {
		var got []string
		if err := json.Unmarshal([]byte(lines[i+1]), &got); err != nil {
			t.Fatal(err)
		}
		want := []string{name}
		switch name {
		case "gate":
			want = append(want, candidateArgs(c, root)...)
		case "land":
			want = append(want, candidateArgs(c, root)...)
			want = append(want, landArgs(c)...)
		case "verdict":
			want = append(want, candidateArgs(c, root)...)
			want = append(want, "--store", "store", "--verdict", "pass")
		case "fixer-gate":
			want = append(want, "--all")
			want = append(want, candidateArgs(c, root)...)
		case "replay":
			want = append(want, "--only", "check")
		case "rolling":
			want = append(want, "--all")
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s argv = %v, want %v", name, got, want)
		}
	}
}

func TestDefinitionHooksRefuseMissingHookAndFlag(t *testing.T) {
	for _, missing := range []string{"land", "--observed-trunk", "--since", "--body-ref"} {
		t.Run(missing, func(t *testing.T) {
			hooks, log := hookStub(t, []string{"land", "rolling", "detail"}, missing)
			steps := &ProcessSteps{Root: filepath.Dir(log)}
			steps.ConfigureHooks(hooks, nil)
			err := steps.CheckPrograms(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("want refusal naming %s, got %v", missing, err)
			}
			raw, _ := os.ReadFile(log)
			if string(raw) != "[\"describe\"]\n" {
				t.Fatalf("preflight executed a hook: %s", raw)
			}
		})
	}
}

func TestDefinitionHooksExplicitAndAbsentSteps(t *testing.T) {
	hooks, _ := hookStub(t, []string{"gate"}, "")
	steps := &ProcessSteps{GateProgram: "explicit", ReplayProgram: DefaultReplayProgram, RollingProgram: DefaultRollingProgram, FixerGateProgram: DefaultFixerGateProgram}
	steps.ConfigureHooks(hooks, map[string]bool{"gate": true, "verdict": true})
	if steps.GateProgram != "explicit" || steps.LandProgram != "" || steps.VerdictProgram != "" || steps.ReplayProgram != "" || steps.RollingProgram != "" || steps.FixerGateProgram != "" {
		t.Fatalf("wrong precedence: %+v", steps)
	}
	if err := steps.CheckPrograms(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestDefinitionHookAtStandardAdapterPathIsNotReplaced(t *testing.T) {
	hooks, log := hookStub(t, []string{"gate"}, "")
	root := filepath.Dir(log)
	raw, err := os.ReadFile(hooks.Argv[1])
	if err != nil {
		t.Fatal(err)
	}
	standard := filepath.Join(root, "tools", "tillr-loop", "tillr-loop")
	if err := os.MkdirAll(filepath.Dir(standard), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, standard, string(raw))
	hooks.Argv[1] = standard
	steps := &ProcessSteps{Root: root}
	steps.ConfigureHooks(hooks, nil)
	if err := steps.CheckPrograms(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	code, _, _, err := steps.program(context.Background(), "verdict", root, steps.GateProgram, Candidate{RunID: "run"})
	if err != nil || code != 0 {
		t.Fatalf("hook substituted: %d / %v", code, err)
	}
	raw, err = os.ReadFile(log)
	if err != nil || !strings.Contains(string(raw), `["gate"`) {
		t.Fatalf("hook not called: %s / %v", raw, err)
	}
}

func TestDefinitionHookGateSelectionForForkCandidates(t *testing.T) {
	for _, selected := range []bool{true, false} {
		t.Run(fmtPythonBool(selected), func(t *testing.T) {
			f, _ := passFixture(t, "item-a")
			names := []string{"land"}
			if selected {
				names = append(names, "gate")
			}
			hooks, log := hookStub(t, names, "")
			steps := &ProcessSteps{Root: f.root, Store: f.storePath, Now: time.Now}
			steps.ConfigureHooks(hooks, nil)
			outcome, err := steps.Gate(context.Background(), Candidate{ItemID: "item-a", RunID: "run", Worktree: f.root, BaseRef: "refs/heads/patch/pi"})
			if selected {
				if err != nil || outcome.Outcome != GatePassed {
					t.Fatalf("definition gate bypassed: %+v / %v", outcome, err)
				}
				raw, err := os.ReadFile(log)
				if err != nil || !strings.Contains(string(raw), `["gate"`) {
					t.Fatalf("hook not called: %s / %v", raw, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "no program") {
					t.Fatalf("unlisted gate ran: %+v / %v", outcome, err)
				}
				if raw, err := os.ReadFile(log); !os.IsNotExist(err) {
					t.Fatalf("unlisted hook called: %s / %v", raw, err)
				}
			}
		})
	}
}
func fmtPythonBool(value bool) string {
	if value {
		return "selected"
	}
	return "omitted"
}

func TestDefinitionHooksAlsoRunInHeavyJudgment(t *testing.T) {
	hooks, log := hookStub(t, []string{"rolling", "replay"}, "")
	root, _ := committedRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "tools/gate/checks"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools/gate/checks/.keep"), "")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "declarations directory")
	raw, err := os.ReadFile(hooks.Argv[1])
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, hooks.Argv[1], string(raw)+`
import os
if args[0] == 'rolling':
    with open(os.environ['TILLR_GATE_SPANS_OUT'], 'w') as f:
        json.dump({'check_spans': []}, f)
`)
	steps := &ProcessSteps{Root: root, LaneRoot: filepath.Join(root, ".tillr/lanes"),
		HeavyGateBin: executable(t, `echo '{"kind":"gate.verdict","payload":{"checks":[]}}'`)}
	steps.ConfigureHooks(hooks, nil)
	steps.heavyReplaySelectors.Store("legacy", "shell:a check with spaces")
	runner := HeavyRunner{Root: root, Lock: loopHeld(t, root), Work: filepath.Join(steps.LaneRoot, ".rolling/worktree"), Trunk: TrunkRef,
		Judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
			if _, err := steps.JudgeHeavy(ctx, dir, ""); err != nil {
				return nil, err
			}
			return steps.JudgeHeavy(ctx, dir, "legacy")
		}}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "[\"rolling\", \"--all\"]\n[\"replay\", \"--only\", \"shell:a check with spaces\"]\n"
	if string(raw) != want {
		t.Fatalf("heavy argv = %s, want %s", raw, want)
	}
}
