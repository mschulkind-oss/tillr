package main_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// The cold start has no watermark. Even spans naming an absolute output directory
// are not a verdict when the rolling program exits without judging the tree.
func TestRollingWithoutAVerdictStillDispatches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  int
		spans string
	}{
		{"missing program", -1, ""},
		{"no spans", 1, ""},
		{"unreadable spans", 1, "not json"},
		{"no failing check", 1, `{"check_spans":[{"check_id":"unit","status":"ERR"}]}`},
		{"cold start absolute watch", 2, `{"check_spans":[{"check_id":"roster-storybook","status":"FAIL","attributes_json":"{\"writes\":[\"/durable/roster-storybook\"]}"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
			fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
			base := git(t, f.root, "rev-parse", "main")
			steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
			steps.RollingProgram = stub(t, "rolling", fmt.Sprintf("printf '%%s' '%s' > \"$TILLR_GATE_SPANS_OUT\"\nexit %d\n", tc.spans, tc.code))
			if tc.code == -1 {
				steps.RollingProgram += "-missing"
			}
			var report bytes.Buffer
			result, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report).Run(context.Background())
			if err != nil || result.Dispatched == 0 {
				t.Fatalf("no dispatch after unknown rolling run: %+v, %v\n%s", result, err, report.String())
			}
			if result.RollingRed != 0 || itemExists(t, f.store, drain.TrunkFixerID(base)) {
				t.Fatalf("unknown rolling run filed a fixer: %+v\n%s", result, report.String())
			}
			if !strings.Contains(report.String(), fmt.Sprintf("could not judge (exit %d)", tc.code)) {
				t.Fatalf("the rolling exit code is missing:\n%s", report.String())
			}
			if watermark, _ := rollingStateOf(t, f.store); watermark != "" {
				t.Fatalf("unknown run advanced the watermark to %q", watermark)
			}
		})
	}
}

func TestARefusedRollingFixerStillDispatches(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	// A wildcard reaches item.Apply's actual fence-wildcard refusal.
	steps.RollingProgram = stub(t, "rolling", `printf '%s' '{"check_spans":[{"check_id":"unit","status":"FAIL","attributes_json":"{\"writes\":[\"src/*\"]}"}]}' > "$TILLR_GATE_SPANS_OUT"
exit 1
`)
	var report bytes.Buffer
	result, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report).Run(context.Background())
	if err != nil || result.Dispatched == 0 {
		t.Fatalf("no dispatch after refused fixer: %+v, %v\n%s", result, err, report.String())
	}
	if itemExists(t, f.store, drain.TrunkFixerID(base)) {
		t.Fatal("a refused fixer was written")
	}
	if !strings.Contains(report.String(), `allowed_paths carries "src/*"`) || !strings.Contains(report.String(), "continuing dispatch") {
		t.Fatalf("the fixer refusal is not reported:\n%s", report.String())
	}
	if watermark, _ := rollingStateOf(t, f.store); watermark != "" {
		t.Fatalf("a red run advanced the watermark to %q", watermark)
	}
}
