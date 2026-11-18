package main_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const blockedDefinition = "### Work\n\n<!-- tillr: item id=\"item-route\" paths=\"candidate.txt,docs/work.md\" seat=\"build\" -->\n\n- **c1:** impossible contract\n"

func blockedRouteFixture(t *testing.T, worker, designer string) loopFixture {
	t.Helper()
	f := newLoopFixture(t, stub(t, "worker", worker))
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "docs/work.md"), blockedDefinition)
	git(t, f.root, "add", "docs/work.md")
	git(t, f.root, "commit", "-m", "definition")
	f.policy.Seats = map[string]string{"design": "designer", "build": "fixture"}
	f.policy.Backends["designer"] = drain.Backend{Adapter: stub(t, "designer", designer), Model: "design-model"}
	fixtureAt(t, f.store, envelope("item.record", "item-route", map[string]any{
		"item_id": "item-route", "title": "Work", "state": "in-queue", "seat": "build",
		"body_ref": "docs/work.md#work", "allowed_paths": []string{"candidate.txt", "docs/work.md"},
		"blocked_by": []string{}, "criteria": []map[string]string{{"criterion_id": "c1", "text": "impossible contract", "state": ""}},
	}))
	return f
}

func routeLoop(t *testing.T, f loopFixture, steps *drain.ProcessSteps, stage drain.Stager, report io.Writer) *drain.Loop {
	t.Helper()
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true, Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"}, Policy: f.policy, Steps: steps, Stage: stage, Now: steps.Now, Report: report, QuestionsCmd: strings.Fields(indexStub(t, map[string]any{"path": "docs/work.md", "questions": []any{}}))})
	if err != nil {
		t.Fatal(err)
	}
	return loop
}

// The design run objects naming the owner, which is the only way a design conflict
// reaches the owner (the owner's ruling of 2026-11-17).
func TestBlockedReportRoutesOnceToDesignThenOwner(t *testing.T) {
	f := blockedRouteFixture(t, counting+"printf 'Conflict: c1 contradicts the API\nRESULT: blocked\n'", counting+"printf 'Conflict: c1 requires an owner ruling\nNEXT-ACTOR: owner\nRESULT: blocked\n'")
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt", "docs/work.md"}}}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	result, err := routeLoop(t, f, steps, stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 2 {
		t.Fatalf("dispatched %d, want worker and one design run", result.Dispatched)
	}
	if len(stage.specs) != 2 || stage.specs[1].Backend != "designer" {
		t.Fatalf("staging: %+v", stage.specs)
	}
	if got := itemStateOf(t, f.store, "item-route"); got != "blocked" {
		t.Fatalf("state %s", got)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		waiting, underlying, err := item.ReadWaitingOn(r, "item-route")
		if err != nil {
			return err
		}
		if waiting == nil || waiting.Waiter != "owner" || waiting.Reason != "Conflict: c1 contradicts the API" || underlying != "in-queue" {
			t.Fatalf("stored blocked metadata: %+v, from %q", waiting, underlying)
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	events := eventsOf(t, f.store)
	var last map[string]any
	for _, e := range events {
		if e.Kind == "item.conflict" {
			if err := json.Unmarshal(e.Payload, &last); err != nil {
				t.Fatal(err)
			}
		}
	}
	if last == nil || last["waiting_on"] != "owner" || !strings.Contains(last["text"].(string), "owner ruling") {
		t.Fatalf("conflict: %+v", last)
	}
	if !strings.Contains(last["run_id"].(string), "item-route") || !strings.Contains(last["body_ref"].(string), "docs/work.md#work") {
		t.Fatalf("provenance: %+v", last)
	}
	runs := lines(t, f.count)
	if countMatching(runs, "gate") != 0 || countMatching(runs, "verdict") != 0 {
		t.Fatalf("blocked was judged: %v", runs)
	}
	// Replacement loops must not spend a second unanswered design run.
	again, err := routeLoop(t, f, steps, stage, os.Stderr).Run(context.Background())
	if err != nil || again.Dispatched != 0 {
		t.Fatalf("replacement: %+v / %v", again, err)
	}
}

func TestWorkerCannotEditItsOwnBlockBeforeGate(t *testing.T) {
	f := blockedRouteFixture(t, counting+`printf '%s\n' '### Work' '' '<!-- tillr: item id="item-route" paths="candidate.txt,docs/work.md" -->' '' '- **c1:** easier contract' > docs/work.md
git add docs/work.md
git -c user.name=stub -c user.email=stub@example.invalid commit -qm rewrite
printf 'RESULT: done\n'
`, counting+"printf 'RESULT: blocked\n'")
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt", "docs/work.md"}}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	var report strings.Builder
	_, err := routeLoop(t, f, steps, stage, &report).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if countMatching(lines(t, f.count), "gate") != 0 {
		t.Fatal("self-edit reached gate")
	}
	if !strings.Contains(report.String(), "docs/work.md#work") {
		t.Fatalf("refusal lost block: %s", report.String())
	}
}
