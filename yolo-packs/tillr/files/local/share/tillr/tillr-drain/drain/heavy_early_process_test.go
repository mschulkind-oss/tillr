package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestHeavyProcessReplaysAFailedCheckWhileTheNextOneRuns(t *testing.T) {
	testHeavyEarlyProcess(t, false)
}

func testHeavyEarlyProcess(t *testing.T, legacy bool) {
	t.Helper()
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	marker := filepath.Join(work, "recovered")
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0755); err != nil {
		t.Fatal(err)
	}
	declare := func(id, read, script string) {
		raw, err := json.Marshal(map[string]any{"id": id, "label": id, "scope": "tree", "tier": "heavy", "budget_ms": 1, "timeout_ms": 35000, "command": []string{"sh", "-c", script, "sh", marker}, "reads": []string{read}, "remember": false})
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(checks, id+".json"), string(raw))
	}
	failed, slow := "a-quick", "b-slow"
	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: heavyBuildGate(t, work)}
	if legacy {
		failed, slow = "legacy", "slow"
		writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), `print('{"declarations":[{"id":"legacy","kind":"fixture","label":"legacy","watches":[{"path":"value"}]}]}')`)
		steps.RollingProgram = executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"legacy","kind":"fixture","label":"legacy","status":"FAIL","attributes_json":"{}"}]}
JSON
exit 1`)
		steps.ReplayProgram = executable(t, `test "$(cat value)" = good`) + " {only}"
	} else {
		declare(failed, "value", `test "$(cat value)" != bad`)
	}
	declare(slow, "unrelated/", `for i in $(seq 30); do test ! -f "$1" || exit 0; sleep 1; done; test -f "$1"`)
	base := heavyCommit(t, root, "good")
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "bad")
	runner := HeavyRunner{Root: root, Lock: loopHeld(t, root), Work: filepath.Join(work, ".rolling", "worktree"), Trunk: TrunkRef, Judge: steps.JudgeHeavy}
	offers := 0
	runner.Revert = func(ctx context.Context, _, landing string, checks []HeavyCheck, replay HeavyReplay) (RevertOutcome, error) {
		offers++
		if landing != tip || len(checks) != 1 || checks[0].CheckID != failed || !slices.Equal(checks[0].Reads, []string{"value"}) {
			t.Errorf("offer: landing=%s checks=%+v", landing, checks)
		}
		if red, err := replay(ctx, base, failed); red || err != nil {
			t.Errorf("base replay: red=%v err=%v", red, err)
		}
		if err := os.WriteFile(marker, nil, 0644); err != nil {
			return RevertOutcome{}, err
		}
		return RevertOutcome{Refused: "fixture refusal"}, nil
	}
	out, err := runner.Run(context.Background())
	if err != nil || offers != 1 || len(out.Runs) != 1 || out.Runs[0].Result != "red" {
		t.Fatalf("run=%+v err=%v offers=%d", out, err, offers)
	}
	judged := out.Runs[0].Checks
	if len(judged) != 2 || judged[0].CheckID != failed || judged[0].Result != "fail" || judged[1].CheckID != slow || judged[1].Result != "pass" {
		t.Fatalf("checks=%+v", judged)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != base {
		t.Fatalf("green=%s want base %s", green, base)
	}
}
