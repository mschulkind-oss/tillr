package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

type scaleEvent struct {
	Kind string `json:"kind"`
	At   int64  `json:"at_ns"`
}
type scaleObservation struct {
	RunID  string       `json:"run_id"`
	Events []scaleEvent `json:"events"`
}
type scaleSteps struct {
	*drain.ProcessSteps
	t            *testing.T
	lanes        int
	mu           sync.Mutex
	entered      int
	release      chan struct{}
	observations map[string][]scaleEvent
}

func (s *scaleSteps) note(run, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations[run] = append(s.observations[run], scaleEvent{Kind: kind, At: time.Now().UnixNano()})
}
func (s *scaleSteps) Gate(ctx context.Context, c drain.Candidate) (drain.GateOutcome, error) {
	s.note(c.RunID, "gate-start")
	s.mu.Lock()
	s.entered++
	release := s.release
	if s.entered%s.lanes == 0 {
		close(release)
		s.release = make(chan struct{})
	}
	s.mu.Unlock()
	select {
	case <-release:
	case <-ctx.Done():
		return drain.GateOutcome{}, ctx.Err()
	}
	dir := drain.RunDir(s.Root, c.RunID)
	tree := git(s.t, c.Worktree, "rev-parse", "HEAD^{tree}")
	// A declared check really reads the lane's commit and creates a lane-local
	// file. No shared barrier file is available to a gate; synchronization above
	// belongs to the test driver, not to a check.
	check := map[string]any{"check_id": "scale-check", "kind": "fixture", "label": "lane-local check", "scope": "tree", "tier": "fast", "budget_ms": 10000, "remember": false,
		"command": []string{"sh", "-c", fmt.Sprintf("mkdir -p .gate-fixture; cp changes/%s/%s .gate-fixture/%s", c.LaneID, c.RunID, c.RunID)},
		"reads":   []string{"changes/" + c.LaneID + "/" + c.RunID}, "writes": []string{".gate-fixture/" + c.RunID}, "depends_on": []string{}}
	raw := envelope("gate.plan", "plan-"+c.RunID, map[string]any{"plan_id": "plan-" + c.RunID, "registry_digest": "scale-fixture", "base_sha": c.BaseSHA, "tree_sha": tree, "checks": []any{check}, "in_process": []any{}})
	if err := os.MkdirAll(dir, 0755); err != nil {
		return drain.GateOutcome{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, drain.GatePlanRecord), []byte(raw), 0644); err != nil {
		return drain.GateOutcome{}, err
	}
	before := scaleFiles(s.t, c.Worktree)
	// The real gate executes every declared check; the process step records its
	// real verdict in the result store before the lander may acquire the lock.
	result, err := s.ProcessSteps.Gate(ctx, c)
	if err != nil {
		return result, err
	}
	after := scaleFiles(s.t, c.Worktree)
	created := []string{}
	for path := range after {
		if !before[path] {
			created = append(created, path)
		}
	}
	if len(created) != 1 || created[0] != filepath.Join(c.Worktree, ".gate-fixture", c.RunID) {
		s.t.Errorf("gate %s created undeclared files: %v", c.RunID, created)
	}
	s.note(c.RunID, "gate-verdict")
	return result, nil
}
func (s *scaleSteps) Verdict(ctx context.Context, c drain.Candidate, verdict string) error {
	return (&evidenceProcessSteps{s.ProcessSteps}).Verdict(ctx, c, verdict)
}
func (s *scaleSteps) JudgeHeavy(context.Context, string, string) ([]drain.HeavyCheck, error) {
	return []drain.HeavyCheck{{CheckID: "fixture-heavy", Result: "pass"}}, nil
}

type scaleStager struct {
	laneRoot string
	t        *testing.T
}

func (s scaleStager) Stage(_ context.Context, spec drain.StageSpec) error {
	// A fresh run starts at its staged base; the production stager recreates
	// the worktree here. Retaining a prior round's branch would gate that
	// lane's earlier change again instead of one new change.
	git(s.t, filepath.Join(s.laneRoot, spec.LaneID, "worktree"), "reset", "--hard", spec.BaseSHA)
	return writeFence(s.laneRoot, spec.LaneID, []string{"changes/" + spec.LaneID + "/"})
}

// Exercise the initial wave and one reuse of every lane. Extra waves repeat
// the same assertions but can exhaust the shard watchdog under proof load.
const scaleRounds = 2

func TestTenLanesLandWithoutWaitingOnEachOther(t *testing.T) {
	testLanesLandWithoutWaitingOnEachOther(t, 10)
}

func TestTwentyLanesLandWithoutWaitingOnEachOther(t *testing.T) {
	testLanesLandWithoutWaitingOnEachOther(t, 20)
}

func testLanesLandWithoutWaitingOnEachOther(t *testing.T, n int) {
	t.Helper()
	if scaleRaceDetector {
		t.Skip("store.Open migrates with BEGIN IMMEDIATE on every open: twenty-lane -race run exhausts SQLite busy timeout; documented in docs/design/gate-rework.md#twenty-lanes-land-without-waiting-on-each-other; remove this skip with the store repair")
	}
	adapter := stub(t, "scale-adapter", `lane=$(basename "$(dirname "$PWD")")
mkdir -p "changes/$lane"
printf '%s\n' "$TILLR_RUN_ID" > "changes/$lane/$TILLR_RUN_ID"
git add "changes/$lane/$TILLR_RUN_ID"
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m 'test: add lane change'
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, adapter)
	global := filepath.Join(scratch(t, "identity"), "gitconfig")
	writeFile(t, global, "[user]\n name = Fixture Landing\n email = landing@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	policyDir := filepath.Join(f.root, "config", "tillr")
	if err := os.MkdirAll(policyDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(policyDir, "gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":0},"lane_base":"tip"}`)
	git(t, f.root, "add", "config")
	git(t, f.root, "commit", "-m", "test: configure heavy checks")
	git(t, f.root, "update-ref", drain.GreenRef, "HEAD")
	writeFile(t, filepath.Join(f.root, ".git", "info", "exclude"), ".tillr/\n.gate-fixture/\n")
	gateBin := sibling(t, "gate")
	gate := stub(t, "scale-gate", fmt.Sprintf(`if [ "$1" = --help ]; then exit 0; fi
run=
while [ "$#" -gt 0 ]; do
 [ "$1" = --run ] && run=$2
 shift 2
done
exec %q run --input "%s/.tillr/loop/$run/gate.plan.json" --worktree "$PWD" --output "%s/.tillr/loop/$run/gate.verdict.json" --logs "%s/.tillr/loop/$run/check-logs"
`, gateBin, f.root, f.root, f.root))
	landBin := sibling(t, "land")
	// The wrapper consumes precisely the existing drain argv, then invokes the real lander.
	land := stub(t, "scale-land", fmt.Sprintf(`if [ "$1" = --help ]; then exit 0; fi
item= run= base= head= ref=
while [ "$#" -gt 0 ]; do
 case "$1" in --item) item=$2;; --run) run=$2;; --base) base=$2;; --head) head=$2;; --base-ref) ref=$2;; esac
 shift 2
done
exec %q pass --store %q --repo %q --item "$item" --run "$run" --base "$base" --head "$head" --base-ref "$ref" --verdict "%s/.tillr/loop/$run/gate.verdict.json"
`, landBin, f.store, f.root, f.root))
	all := map[string][]scaleEvent{}
	records := []string{}
	for round := 0; round < scaleRounds; round++ {
		for lane := 1; lane <= n; lane++ {
			records = append(records, itemRecord(fmt.Sprintf("scale-%02d-%02d", round, lane), "in-queue"))
		}
	}
	fixtureAt(t, f.store, records...)
	steps := &scaleSteps{ProcessSteps: f.steps(t, gate, land, always(t, "verdict", 0)), t: t, lanes: n, release: make(chan struct{}), observations: map[string][]scaleEvent{}}
	var report strings.Builder
	ids := []string{}
	for i := 1; i <= n; i++ {
		ids = append(ids, laneName(i))
	}
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true, Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: ids, Policy: f.policy, Steps: steps, Stage: scaleStager{f.laneRoot, t}, Now: steps.Now, Report: &report})
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(context.Background())
	if err != nil || result.Landed != scaleRounds*n {
		t.Fatalf("landed=%d stop=%s %v\n%s", result.Landed, result.Stop, err, report.String())
	}
	for _, field := range []string{"land-lock longest=", "median=", "landings per heavy run="} {
		if !strings.Contains(report.String(), field) {
			t.Fatalf("summary missing %s:\n%s", field, report.String())
		}
	}
	for run, events := range steps.observations {
		raw, err := os.ReadFile(filepath.Join(drain.RunDir(f.root, run), "land.observation.json"))
		if err != nil {
			t.Fatal(err)
		}
		var observation scaleObservation
		if err = json.Unmarshal(raw, &observation); err != nil {
			t.Fatal(err)
		}
		if observation.RunID != run {
			t.Fatal("wrong run observation")
		}
		all[run] = append(events, observation.Events...)
	}
	assertScaleEvents(t, all, n)
	assertScaleIsolation(t, f, all)
	for lane := 1; lane <= n; lane++ {
		entries, err := os.ReadDir(filepath.Join(f.root, "changes", laneName(lane)))
		if err != nil || len(entries) != scaleRounds {
			t.Fatalf("lane %d: %d files, %v", lane, len(entries), err)
		}
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var landed int
	if err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM landings WHERE result='landed'`).Scan(&landed)
	}); err != nil || landed != scaleRounds*n {
		t.Fatalf("landings=%d %v", landed, err)
	}
}
func assertScaleEvents(t *testing.T, runs map[string][]scaleEvent, lanes int) {
	t.Helper()
	type event struct {
		scaleEvent
		run string
	}
	ordered := []event{}
	for run, events := range runs {
		for _, e := range events {
			ordered = append(ordered, event{e, run})
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].At < ordered[j].At })
	active, peak := 0, 0
	holder := ""
	verdict := map[string]bool{}
	stages := []string{}
	for _, e := range ordered {
		switch e.Kind {
		case "gate-start":
			active++
			if active > peak {
				peak = active
			}
			if holder == e.run {
				t.Fatal("gate starts under its lock")
			}
		case "gate-verdict":
			active--
			verdict[e.run] = true
			if holder == e.run {
				t.Fatal("gate finishes under its lock")
			}
		case "acquired":
			if holder != "" || !verdict[e.run] {
				t.Fatalf("invalid acquisition %s, holder=%s verdict=%v", e.run, holder, verdict[e.run])
			}
			holder = e.run
			stages = nil
		case "squash", "trunk-move", "receipt":
			if holder != e.run {
				t.Fatalf("%s outside its hold", e.Kind)
			}
			stages = append(stages, e.Kind)
		case "released":
			if holder != e.run || strings.Join(stages, ",") != "squash,trunk-move,receipt" {
				t.Fatalf("invalid hold %s: %v", e.run, stages)
			}
			holder = ""
		default:
			t.Fatalf("unexpected event %s", e.Kind)
		}
	}
	if peak != lanes || active != 0 || holder != "" || len(runs) != scaleRounds*lanes {
		t.Fatalf("peak=%d active=%d holder=%s runs=%d", peak, active, holder, len(runs))
	}
}
