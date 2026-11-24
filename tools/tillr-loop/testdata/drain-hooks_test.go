package drain

// Compiled into the drain package by test.py's Go overlay. This keeps the test
// with the hook program while exercising the drain's real dispatch code paths.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTillrLoopDefinitionHookFlagParity(t *testing.T) {
	_, adapter := repositoryFile(t, "tools/tillr-loop/tillr-loop")
	_, fixture := repositoryFile(t, drainArgvFixture)
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var cases []argvCase
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	f, _ := passFixture(t, "item-a")
	adapterStubs(t)
	worktree := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	head := runGit(t, worktree, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(worktree, "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	hooks, _ := hookStub(t, []string{"gate", "land", "verdict", "fixer-gate", "replay", "rolling"}, "")
	hooks.Argv = []string{"python3", adapter}
	steps := &ProcessSteps{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: time.Now}
	steps.ConfigureHooks(hooks, nil) // No explicit step flags: only the definition supplies them.
	if err := steps.CheckPrograms(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			args := append([]string{}, tc.Args...)
			for i, word := range args {
				word = strings.ReplaceAll(word, "<worktree>", worktree)
				word = strings.ReplaceAll(word, "<root>", f.root)
				word = strings.ReplaceAll(word, "<store>", f.storePath)
				args[i] = strings.ReplaceAll(word, "<head>", head)
			}
			values := map[string]string{}
			for i := 0; i < len(args); i++ {
				if args[i] != "--all" {
					values[args[i]] = args[i+1]
					i++
				}
			}
			c := Candidate{ItemID: values["--item"], LaneID: values["--lane"], RunID: values["--run"], Worktree: values["--worktree"], BaseSHA: values["--base"], HeadSHA: values["--head"], ObservedTrunk: values["--observed-trunk"], BaseRef: values["--base-ref"], WorkerHeadSHA: values["--message-from"]}
			if tc.Face == "land" || tc.Face == "verdict" {
				writeRunRecord(t, RunDir(f.root, c.RunID), "{}\n")
			}
			face := tc.Face
			switch face {
			case "gate":
				out, err := steps.Gate(context.Background(), c)
				if err != nil || out.Outcome != GatePassed {
					t.Fatalf("gate: %+v / %v", out, err)
				}
			case "fixer-gate":
				// The named check is a fast whole-tree declaration, so the adapter hands it to
				// the tree's own tree-check program; the stand-in answers a pass.
				if err := os.MkdirAll(filepath.Join(worktree, "tools", "gate", "checks"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(worktree, "tools", "gate", "checks", "fixture.json"),
					`{"id": "`+values["--only"]+`", "scope": "tree", "tier": "fast"}`+"\n")
				writeForkFile(t, filepath.Join(worktree, "tools", "gate", "tree-check.py"), "print('named check ran')\n")
				if answer, output := steps.fixerCheck(context.Background(), c, values["--only"]); answer != "pass" {
					t.Fatalf("named check %s: %s", answer, output)
				}
			case "land":
				out, err := steps.Land(context.Background(), c)
				if err != nil || out.Result != LandLanded {
					t.Fatalf("land: %+v / %v", out, err)
				}
			case "verdict":
				if err := steps.Verdict(context.Background(), c, values["--verdict"]); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unhandled fixture face %q", face)
			}
			got := lastSpanArgv(t, f.root, c.RunID)
			want := append([]string{"python3", adapter, face}, args...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("definition hook argv = %v, flag form = %v", got, want)
			}
		})
	}
}
