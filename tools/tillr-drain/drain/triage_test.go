package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

const triageBlock = `### Add the requested feature

Readers need this feature to complete their work.

<!-- tillr: item id="item-declared" paths="docs/" prose="2" -->

- **c1:** The feature answers the request.
`

func triageFixture(t *testing.T) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := passFixture(t, "item-declared", "item-request")
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), triageBlock)
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "declare one item")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='build', body_ref='docs/request.md#missing', title='Add the missing request'`)
		return err
	})
	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "designer"}
	l.cfg.Policy.Backends["designer"] = Backend{Adapter: "fixture"}
	l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", "printf '%s' '{\"index\":{\"documents\":[]}}'"}}
	return f, l
}

func findTriagePlan(t *testing.T, l *Loop, id string) plannedApplication {
	t.Helper()
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == id {
			return p
		}
	}
	t.Fatalf("no plan for %s", id)
	return plannedApplication{}
}

func TestDocumentFedPrerequisiteRoutesBeforeSeatAndWorkflow(t *testing.T) {
	f, l := triageFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='missing@v1' WHERE item_id='item-request'`)
		return err
	})
	d := findTriagePlan(t, l, "item-request").decision
	if d.Action != ActionDispatch || d.Reason != "triage-no-block" || d.Seat != "design" || d.BlockConflict == nil || d.Workflow != nil {
		t.Fatalf("not triaged: %+v", d)
	}
	if d.RunRequest.Backend != "designer" {
		t.Fatalf("used build backend: %+v", d)
	}
	declared := findTriagePlan(t, l, "item-declared").decision
	if declared.Action != ActionDispatch || declared.Seat != "build" {
		t.Fatalf("declared not build: %+v", declared)
	}
}

func TestDocumentFreeStoreKeepsItsOwnSeats(t *testing.T) {
	f, l := triageFixture(t)
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), "# No declarations\n")
	l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", "printf '%s' '{\"index\":{\"documents\":[]}}'"}}
	for _, p := range mustPlan(t, l) {
		if p.decision.Action != ActionDispatch || p.decision.Seat != "build" || p.decision.Reason == "triage-no-block" {
			t.Fatalf("document-free store changed: %+v", p.decision)
		}
	}
}

func TestTriageBriefOffersThreeResultsAndStoredRequest(t *testing.T) {
	f, l := triageFixture(t)
	d := findTriagePlan(t, l, "item-request").decision
	s := &LaneStager{Root: f.root, Store: f.storePath, LaneRoot: l.cfg.LaneRoot, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	// Exercise the exact renderer without creating another lane's worktree.
	recorded, err := s.readItem(context.Background(), d.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	recorded.AllowedPaths = []string{"docs/request.md"}
	values, err := briefValuesFor(recorded, StageSpec{RunID: d.RunRequest.RunID, ItemID: d.ItemID, BlockConflict: d.BlockConflict}, "target", "lane worktree", "base", "scratch", l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	var text, fence string
	for _, v := range values {
		if v.placeholder == "TASK" {
			text = v.text
		}
		if v.placeholder == "FENCE_PATHS" {
			fence = v.text
		}
	}
	for _, want := range []string{"Add the missing request", "docs/request.md#missing", "exactly one", "numbered question", "duplicates a named item", "already done", "prose=\"2\""} {
		if !strings.Contains(text, want) {
			t.Errorf("brief missing %q: %s", want, text)
		}
	}
	if fence != "docs/request.md" {
		t.Fatalf("triage fence %q", fence)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if err := s.Stage(context.Background(), StageSpec{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, WorkingDir: WorktreeDir, BaseSHA: base, BlockConflict: d.BlockConflict}); err != nil {
		t.Fatal(err)
	}
	actualFence, err := os.ReadFile(filepath.Join(s.LaneRoot, d.LaneID, FenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(actualFence) != "docs/request.md\n" {
		t.Fatalf("staged fence: %s", actualFence)
	}
	brief, err := os.ReadFile(filepath.Join(s.LaneRoot, d.LaneID, BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{recorded.Title, recorded.BodyRef, recorded.Criteria[0].Text, "exactly one", "numbered question", "duplicates a named item", "already done"} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("staged brief omits %q", want)
		}
	}
}
