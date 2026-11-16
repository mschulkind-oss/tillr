package main_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// commitDocument commits one tracked document into the fixture repository.
func commitDocument(t *testing.T, f loopFixture, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(f.root, filepath.FromSlash(name))), 0o755); err != nil {
		t.Fatalf("creating the document's directory failed: %v", err)
	}
	writeFile(t, filepath.Join(f.root, filepath.FromSlash(name)), content)
	git(t, f.root, "add", name)
	git(t, f.root, "commit", "-m", "document "+name)
}

// syncLoop is the fixture's loop with the questions source stubbed to answer the documents
// given as present with nothing open, so an item synced from a document is dispatchable on
// its merits rather than held for an index this machine may not have.
func syncLoop(t *testing.T, f loopFixture, steps *drain.ProcessSteps, report *bytes.Buffer, docs ...string) *drain.Loop {
	t.Helper()
	entries := []map[string]any{}
	for _, d := range docs {
		entries = append(entries, map[string]any{"path": d, "questions": []map[string]any{}})
	}
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: steps,
		Stage:        passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		QuestionsCmd: strings.Fields(indexStub(t, entries...)),
		Now:          steps.Now, Report: report,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	return loop
}

// The loop syncs the documents before it plans: an item that exists only as a block in a
// tracked document is in the store by the first tick, so that tick dispatches and lands it.
// A loop that planned first would find an empty store and stop drained with nothing run.
func TestTheLoopSyncsTheDocumentsBeforeItsFirstPlan(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	commitDocument(t, f, "docs/plan.md", "# Plan\n\n### Write the candidate\n\n"+
		"<!-- tillr: item\n     id=\"item-from-doc\"\n     paths=\"candidate.txt\" -->\n\n- **c1:** candidate.txt exists.\n")
	fixtureAt(t, f.store)

	var report bytes.Buffer
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	result, err := syncLoop(t, f, steps, &report, "docs/plan.md").Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v\n%s", err, report.String())
	}
	if result.Sync == nil || result.Sync.Created != 1 || len(result.Sync.Findings) != 0 {
		t.Fatalf("the startup sync did not create the document's item: %+v\n%s", result.Sync, report.String())
	}
	if result.Dispatched != 1 || result.Landed != 1 {
		t.Fatalf("the loop dispatched %d and landed %d, want the synced item run and landed\n%s",
			result.Dispatched, result.Landed, report.String())
	}
	if countMatching(lines(t, f.count), "gate --item item-from-doc") != 1 {
		t.Fatalf("the gate never judged the synced item: %v", lines(t, f.count))
	}
	text := report.String()
	if strings.Index(text, "loop: sync: created 1") < 0 {
		t.Fatalf("the report does not carry the sync line:\n%s", text)
	}
}

// A sync finding is the documents' defect, not the loop's: the loop reports it, records it
// in its result, and drains what was written.
func TestASyncFindingDoesNotStopTheLoop(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	commitDocument(t, f, "docs/bad.md", "### Bad\n<!-- tillr: item id=\"item-bad\" paths=\"candidate.txt\" rank=\"a\" -->\n- **c1:** x\n")
	fixtureAt(t, f.store)

	var report bytes.Buffer
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	result, err := syncLoop(t, f, steps, &report, "docs/bad.md").Run(context.Background())
	if err != nil {
		t.Fatalf("a sync finding failed the loop: %v\n%s", err, report.String())
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q, want %q", result.Stop, drain.StopQueueDrained)
	}
	if result.Sync == nil || len(result.Sync.Findings) != 1 || !strings.Contains(result.Sync.Findings[0], "unknown-attribute") {
		t.Fatalf("the result does not record the finding: %+v", result.Sync)
	}
	if !strings.Contains(report.String(), "loop: sync finding: docs/bad.md:2 (Bad) item-bad: unknown-attribute") {
		t.Fatalf("the report does not name the finding:\n%s", report.String())
	}
}

// A sync that cannot run at all is a fault before the first tick, recorded like every other
// startup fault.
func TestASyncThatCannotRunIsAFault(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store)
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	steps.ItemBin = filepath.Join(scratch(t, "no-item"), "tillr-item")

	var report bytes.Buffer
	result, err := syncLoop(t, f, steps, &report).Run(context.Background())
	if err == nil {
		t.Fatalf("a missing item binary did not fail the loop")
	}
	if result.Stop != drain.StopFault || result.Ticks != 0 || !strings.Contains(result.Detail, "sync could not run") {
		t.Fatalf("the loop stopped %q after %d tick(s): %s", result.Stop, result.Ticks, result.Detail)
	}
	if len(lines(t, f.count)) != 0 {
		t.Fatalf("something ran after a failed sync: %v", lines(t, f.count))
	}
}
