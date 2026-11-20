package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const legacySpansFail = `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"go-tool/tools-x","kind":"go-tool","label":"tools x","status":"FAIL","attributes_json":"{}"}]}
JSON
`

// legacyProofHarness answers a harness whose proof judges the tree with the real legacy
// program body, so the loop log and the fixer's brief come from the real path.
func legacyProofHarness(t *testing.T, body string) *revertHarness {
	t.Helper()
	h := newRevertHarness(t, nil)
	if err := os.MkdirAll(filepath.Join(h.root, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.land("item-seed", "tools/gate/checks/.keep", "\n")
	heavyGit(t, h.root, "update-ref", GreenRef, "HEAD")
	h.base = heavyGit(t, h.root, "rev-parse", "HEAD")
	steps := &ProcessSteps{Root: h.root, LaneRoot: h.loop.cfg.LaneRoot, RollingProgram: executable(t, body)}
	h.steps.judge = func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		if only != "" {
			return []HeavyCheck{{CheckID: only, Result: "pass"}}, nil
		}
		return steps.JudgeHeavy(ctx, dir, only)
	}
	h.loop.gatePolicy.AutoRevert = false
	return h
}

// The loop log line for a legacy proof names the output file, and the trunk fixer's brief
// names it too and quotes the failing lines and the runner's final error.
func TestLegacyFailureReachesTheLoopLogAndTheFixerBrief(t *testing.T) {
	h := legacyProofHarness(t, `echo "=== RUN   TestIntermittent"; echo "--- FAIL: TestIntermittent (0.01s)"
for i in 1 2 3 4 5 6 7 8 9 10; do echo "ok line $i"; done
echo "FAIL tools/x 3.2s final-runner-error" >&2
`+legacySpansFail+`exit 1`)
	h.land("item-later", "c", "unrelated\n")
	h.proof()
	log := h.report.String()
	logs := filepath.Join(h.loop.cfg.LaneRoot, ".rolling", "logs")
	matches, err := filepath.Glob(filepath.Join(logs, "*", "legacy-proof.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("logs=%v err=%v", matches, err)
	}
	if !strings.Contains(log, "failing: go-tool/tools-x; output: "+matches[0]) {
		t.Fatalf("the loop log does not name the file %s:\n%s", matches[0], log)
	}
	if raw, err := os.ReadFile(matches[0]); err != nil || !strings.Contains(string(raw), "--- FAIL: TestIntermittent") || !strings.Contains(string(raw), "ok line 10") || !strings.Contains(string(raw), "final-runner-error") {
		t.Fatalf("the log file does not hold the whole output of both streams: %q %v", raw, err)
	}
	if strings.Contains(log, "DEFECT") {
		t.Fatalf("a failure with output is no defect:\n%s", log)
	}
	fixers := h.fixers()
	if len(fixers) != 1 {
		t.Fatalf("fixers=%v", fixers)
	}
	text := h.fixerText(fixers[0])
	for _, want := range []string{matches[0], "--- FAIL: TestIntermittent", "final-runner-error"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the fixer brief lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ok line 1 ") {
		t.Fatalf("the brief quotes the whole log, not a bounded tail:\n%s", text)
	}
}

// A failed check that left no output is a defect in the loop log, not silence.
func TestLegacyFailureWithNoOutputIsReportedAsADefect(t *testing.T) {
	h := legacyProofHarness(t, legacySpansFail+"exit 1")
	h.land("item-later", "c", "unrelated\n")
	h.proof()
	log := h.report.String()
	if !strings.Contains(log, "loop: heavy DEFECT: failed legacy check go-tool/tools-x left no output") {
		t.Fatalf("the loop log does not report the defect:\n%s", log)
	}
}
