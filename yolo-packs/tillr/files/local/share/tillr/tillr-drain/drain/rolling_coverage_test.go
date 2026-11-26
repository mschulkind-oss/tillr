package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRollingCoverageDistinguishesMissingUnreadableAndEmptySpans(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"missing", "", GateSpansAbsent},
		{"broken-json", "{", GateSpansUnreadable},
		{"broken-attributes", `{"check_spans":[{"attributes_json":"{"}]}`, GateSpansUnreadable},
		{"empty", `{"check_spans":[],"skipped_checks":[]}`, GateSpansRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(scratchUnderDurable(t), GateSpansFile)
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			coverage := readRollingCoverage(path)
			if coverage.SpansState != tc.want {
				t.Fatalf("state=%s, want %s", coverage.SpansState, tc.want)
			}
			if tc.want == GateSpansRead {
				if coverage.Line() != "0 ran, 0 cached, 0 skipped" {
					t.Fatal(coverage.Line())
				}
			} else if !strings.Contains(coverage.Line(), "counts unavailable") {
				t.Fatal(coverage.Line())
			}
		})
	}
}

func TestGreenRollingRunRecordsCoverageAndReportsCounts(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	tip := runGit(t, root, "rev-parse", "HEAD")
	storePath := filepath.Join(root, "state.db")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	laneRoot := filepath.Join(root, "lanes")
	program := executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"fresh","status":"ok","attributes_json":"{\"cached\":false}"},{"check_id":"reused","status":"ok","attributes_json":"{\"cached\":true}"}],"skipped_checks":[{"check_id":"unchanged-suite","kind":"go-tool","label":"unchanged suite","reason":"no source changed"}]}
JSON`)
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	var report bytes.Buffer
	steps := &ProcessSteps{Root: root, LaneRoot: laneRoot, RollingProgram: program}
	loop := &Loop{cfg: LoopConfig{Root: root, Store: storePath, LaneRoot: laneRoot, Steps: steps, Now: func() time.Time { return now }, Report: &report}}
	if err := loop.maybeRolling(context.Background(), tip, true, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "1 ran, 1 cached, 1 skipped") {
		t.Fatalf("missing coverage counts: %s", report.String())
	}
	read := openFixture(t, storePath)
	defer read.Close()
	var raw, recordedTip, state, watermark string
	var ran, cached int
	err = read.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT runs.tip_sha, coverage.spans_state, coverage.ran, coverage.cached, coverage.skipped_checks, state.watermark_sha FROM rolling_runs runs JOIN rolling_coverage coverage USING (run_id) JOIN rolling_state state ON state.id = 1`).Scan(&recordedTip, &state, &ran, &cached, &raw, &watermark)
	})
	if err != nil {
		t.Fatal(err)
	}
	var skips []gateSkip
	if err := json.Unmarshal([]byte(raw), &skips); err != nil {
		t.Fatal(err)
	}
	if recordedTip != tip || watermark != tip || state != GateSpansRead || ran != 1 || cached != 1 || len(skips) != 1 || skips[0].CheckID != "unchanged-suite" || skips[0].Reason != "no source changed" {
		t.Fatalf("coverage: tip=%s watermark=%s state=%s ran=%d cached=%d skipped=%s", recordedTip, watermark, state, ran, cached, raw)
	}
}
