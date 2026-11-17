package drain

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type rollingStallSequence struct {
	*ProcessSteps
	next int
}

func (s *rollingStallSequence) Rolling(context.Context, string, string) (RollingOutcome, error) {
	sets := [][]string{{"A"}, {"A", "B"}, {"B"}}
	checks := sets[s.next]
	s.next++
	return RollingOutcome{Stalled: true, ExitCode: 6, FailingChecks: checks,
		CheckWatches: map[string][]string{"A": {"tools/tillr-gate/"}, "B": {"src/"}}}, nil
}

func TestRollingStallPreservesNewlyObservedChecks(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	tip := runGit(t, root, "rev-parse", "HEAD")
	storePath := filepath.Join(root, ".tillr", "state.db")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	steps := &rollingStallSequence{ProcessSteps: &ProcessSteps{RollingProgram: "fixture"}}
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	var report bytes.Buffer
	cfg := LoopConfig{Root: root, Store: storePath, Steps: steps, Now: func() time.Time { return now }, Report: &report}
	for n := 0; n < 3; n++ {
		now = now.Add(RollingEvery)
		loop := &Loop{cfg: cfg} // restart between every observation
		if err := loop.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			checks := rollingChecks(t, storePath)
			if len(checks) != 2 || checks[0] != "A" || checks[1] != "B" {
				t.Fatalf("observation was filtered to fixer checks: %v", checks)
			}
		}
	}
	criteria := fixerCriteria(t, storePath, TrunkFixerID(tip))
	if len(criteria) != 1 || !strings.Contains(criteria[0], "red: B") {
		t.Fatalf("B's consecutive stall lost: %v; %s", criteria, report.String())
	}
}

func TestRollingStallRetainsWatermarkAndFilesOneFixerOnRepeat(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	tip := runGit(t, root, "rev-parse", "HEAD")
	laneRoot := filepath.Join(root, ".tillr", "lanes")
	storePath := filepath.Join(root, ".tillr", "state.db")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	program := executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"blocked","status":"STALLED","attributes_json":"{\"writes\":[\"src/\"]}"}]}
JSON
echo 'blocked: stalled; last output last; 60000 ms silent' >&2
exit 6`)
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	steps := &ProcessSteps{Root: root, LaneRoot: laneRoot, Store: storePath, RollingProgram: program}
	var report bytes.Buffer
	loop := &Loop{cfg: LoopConfig{Root: root, Store: storePath, LaneRoot: laneRoot, Steps: steps, Now: func() time.Time { return now }, Report: &report}}
	result := &LoopResult{}
	for n := 0; n < 3; n++ {
		now = now.Add(RollingEvery)
		if err := loop.maybeRolling(context.Background(), tip, false, result); err != nil {
			t.Fatal(err)
		}
		state, err := loop.loadRollingState(context.Background())
		if err != nil || state.WatermarkSHA != "" {
			t.Fatalf("stall advanced watermark: %+v %v", state, err)
		}
		var count int
		s, err := store.Open(context.Background(), storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		err = s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM items WHERE item_id = ?`, TrunkFixerID(tip)).Scan(&count)
		})
		s.Close()
		want := 1
		if n == 0 {
			want = 0
		}
		if err != nil || count != want {
			t.Fatalf("stall %d fixers=%d want %d: %v", n, count, want, err)
		}
		for _, different := range []struct{ base, tip, check string }{{"other", tip, "blocked"}, {"", "other", "blocked"}, {"", tip, "other"}} {
			repeated, err := loop.repeatedRollingStalls(context.Background(), different.base, different.tip, []string{different.check})
			if err != nil || len(repeated) != 0 {
				t.Fatalf("different range/check counted: %v %v", repeated, err)
			}
		}
		// Recreate the loop: the second observation must read persisted evidence.
		loop = &Loop{cfg: loop.cfg}
	}
	if !strings.Contains(report.String(), "blocked") || !strings.Contains(report.String(), "stalled") {
		t.Fatalf("stall lost: %s", report.String())
	}
	if criteria := fixerCriteria(t, storePath, TrunkFixerID(tip)); len(criteria) != 1 || !strings.Contains(criteria[0], "blocked") {
		t.Fatalf("fixer omitted stalled check: %v", criteria)
	}
}
