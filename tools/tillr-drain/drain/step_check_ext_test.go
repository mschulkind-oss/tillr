package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

func stepCheckArgs(t *testing.T, f loopFixture) []string {
	t.Helper()
	policy := filepath.Join(scratch(t, "step-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	s, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return []string{"loop", "--root", f.root, "--store", f.store, "--policy", policy,
		"--lane-root", f.laneRoot, "--item-bin", sibling(t, "item"), "--rolling=", "--fixer-gate=", "--exit-when-drained"}
}

func TestStepFlagRefusalStopsBeforeDispatch(t *testing.T) {
	for _, check := range []bool{false, true} {
		t.Run(stringBool(check), func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
			raw, err := os.ReadFile(filepath.Join("..", "..", "tillr-loop", "tillr-loop"))
			if err != nil {
				t.Fatal(err)
			}
			// Model an installed adapter that has not learned one flag the loop sends.
			old := "        parser.add_argument(\"--observed-trunk\", default=None, metavar=\"SHA\",\n" +
				"                            help=\"the loop's last observed trunk tip, handed to `tillr-land pass`, \"\n" +
				"                                 \"which refuses with trunk-rewritten if the trunk no longer contains it\")\n"
			if !strings.Contains(string(raw), old) {
				t.Fatal("adapter's observed-trunk declaration changed")
			}
			path := filepath.Join(scratch(t, "old-adapter"), "tillr-loop")
			writeFile(t, path, strings.Replace(string(raw), old, "", 1))
			bad := "python3 " + path + " land"
			args := append(stepCheckArgs(t, f), "--gate", "/bin/true", "--land", bad, "--verdict", "/bin/true")
			if check {
				args = append(args, "--check")
			}
			code, out := invoke(t, args...)
			if code != 2 || !strings.Contains(string(out), bad) || !strings.Contains(string(out), "--observed-trunk") {
				t.Fatalf("want usage refusal naming program and flag, got %d: %s", code, out)
			}
			entries, err := os.ReadDir(f.laneRoot)
			if err != nil || len(entries) != 0 {
				t.Fatalf("startup dispatched a run: %v %v", entries, err)
			}
			if !check {
				result := readLoopResult(t, f.root)
				if result.ExitCode != 2 || result.Stop != drain.StopFault {
					t.Fatalf("wrong startup class: %+v", result)
				}
			}
		})
	}
}

func TestUncheckedStepWarnsOnceAndStillStarts(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
	legacy := stub(t, "legacy-step", "exit 0")
	args := append(stepCheckArgs(t, f), "--gate", legacy, "--land", legacy, "--verdict", legacy)
	code, out := invoke(t, args...)
	if code != 0 || !strings.Contains(string(out), "queue-drained") {
		t.Fatalf("loop did not start: %d %s", code, out)
	}
	if strings.Count(string(out), "unchecked") != 1 || !strings.Contains(string(out), legacy) {
		t.Fatalf("want one unchecked warning naming program: %s", out)
	}
}

func TestRuntimeStepUsageStopsAsConfigurationFault(t *testing.T) {
	for _, step := range []string{"gate", "land", "verdict"} {
		t.Run(step, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
			fixtureAt(t, f.store, itemRecord("item-a", "in-queue"))
			bad := stub(t, "bad-"+step, "echo 'unrecognized arguments: --new-flag' >&2\nexit 2")
			gate, land, verdict := "/bin/true", "/bin/true", "/bin/true"
			switch step {
			case "gate":
				gate = bad
			case "land":
				land = bad
			case "verdict":
				verdict = bad
			}
			loop := f.newLoop(t, 1, f.steps(t, gate, land, verdict))
			result, err := loop.Run(context.Background())
			if err == nil || result.Stop != drain.StopFault || result.ExitCode != 2 || !strings.Contains(result.Detail, bad) || !strings.Contains(result.Detail, "configuration") {
				t.Fatalf("want configuration usage stop naming %s, got %+v / %v", bad, result, err)
			}
		})
	}
}
