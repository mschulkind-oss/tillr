package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeavyProcessRunsAffectedChecksFirst(t *testing.T) {
	bin := filepath.Join(scratchUnderDurable(t), "tillr-gate")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..", "tillr-gate")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, raw)
	}
	for _, tc := range []struct {
		name    string
		paths   []string
		noGreen bool
		want    string
	}{
		{"declaration", []string{"late-input"}, false, "z-affected a-unaffected legacy"},
		{"legacy", []string{"legacy-input"}, false, "legacy a-unaffected z-affected"},
		{"both", []string{"late-input", "legacy-input"}, false, "z-affected legacy a-unaffected"},
		{"neither", []string{"other"}, false, "a-unaffected z-affected legacy"},
		{"no-green", []string{"late-input", "legacy-input"}, true, "a-unaffected z-affected legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := heavyRepo(t)
			work := scratchUnderDurable(t)
			order := filepath.Join(work, "order")
			checks := filepath.Join(root, "tools", "gate", "checks")
			if err := os.MkdirAll(checks, 0755); err != nil {
				t.Fatal(err)
			}
			for id, read := range map[string]string{"a-unaffected": "unrelated/", "z-affected": "late-input"} {
				raw, err := json.Marshal(map[string]any{"id": id, "label": id, "scope": "tree", "tier": "heavy", "budget_ms": 1, "timeout_ms": 1000, "command": []string{"sh", "-c", "echo " + id + " >> \"$1\"", "sh", order}, "reads": []string{read}, "remember": false})
				if err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(checks, id+".json"), string(raw))
			}
			writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), `print('{"declarations":[{"id":"legacy","watches":[{"path":"legacy-input"}]}]}')`)
			base := heavyCommit(t, root, "declarations")
			if !tc.noGreen {
				heavyGit(t, root, "update-ref", GreenRef, base)
			}
			for _, path := range tc.paths {
				writeForkFile(t, filepath.Join(root, path), "changed")
			}
			heavyCommit(t, root, "changed")
			legacy := executable(t, `echo legacy >> '`+order+`'
cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"legacy","status":"PASS","attributes_json":"{}"}]}
JSON`)
			steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: bin, RollingProgram: legacy}
			runner := HeavyRunner{Root: root, Work: filepath.Join(work, ".rolling", "worktree"), Trunk: TrunkRef, Judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
				judged, err := steps.JudgeHeavy(ctx, dir, only)
				if err == nil {
					var ids []string
					for _, check := range judged {
						ids = append(ids, check.CheckID)
					}
					if got := strings.Join(ids, " "); got != tc.want {
						t.Errorf("verdict order = %q; want %q", got, tc.want)
					}
				}
				return judged, err
			}}
			out, err := runner.Run(context.Background())
			if err != nil || len(out.Runs) != 1 || out.Runs[0].Result != "green" {
				t.Fatalf("want green run: %+v, %v", out, err)
			}
			raw, err := os.ReadFile(order)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(strings.Fields(string(raw)), " "); got != tc.want {
				t.Fatalf("execution order = %q; want %q", got, tc.want)
			}
		})
	}
}
