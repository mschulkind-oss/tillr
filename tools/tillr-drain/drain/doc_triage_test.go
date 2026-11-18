package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

var triageDocs = []string{"docs/design/a.md", "docs/design/b.md", "docs/design/c.md", "docs/design/d.md"}

// docTriageSteps records each node's brief and request, and has each reader write its
// declared finding output before the shared parallel fixture settles it.
type docTriageSteps struct {
	*parallelSteps
	mu       sync.Mutex
	briefs   map[int]string
	requests map[int]run.Request
}

func (s *docTriageSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	index, err := strconv.Atoi(req.RunID[strings.LastIndex(req.RunID, "-n")+2:])
	if err != nil {
		return run.Record{}, "", err
	}
	briefPath := filepath.Join(s.f.laneRoot, req.LaneID, BriefFile)
	for i, arg := range req.Command {
		if arg == "--brief" && i+1 < len(req.Command) {
			briefPath = req.Command[i+1]
		}
	}
	brief, err := os.ReadFile(briefPath)
	if err != nil {
		return run.Record{}, "", err
	}
	s.mu.Lock()
	s.briefs[index], s.requests[index] = string(brief), req
	s.mu.Unlock()
	if index <= len(triageDocs) {
		finding := fmt.Sprintf("Finding for %s\n", triageDocs[index-1])
		if err := os.WriteFile(filepath.Join(nodeOutputsDir(s.f.root, req.RunID), "finding"), []byte(finding), 0644); err != nil {
			return run.Record{}, "", err
		}
	}
	return s.parallelSteps.Run(ctx, req)
}

// An item that names doc-triage@v1 and supplies its documents in workflow_args runs one
// read-only reader per document on the scout seat, in parallel, then one report writer on
// the design seat that receives every finding and writes within the item's fence; the
// item lands once.
func TestDocTriageWorkflowRunsReadersThenOneReport(t *testing.T) {
	f, l, base := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["design"] = "fixture"
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "tillr", "workflows", "doc-triage@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/doc-triage@v1.json"), string(raw))
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "tracked triage workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id!='item-a'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET seat='design', workflow='doc-triage@v1', workflow_args=? WHERE item_id='item-a'`,
			`{"docs":["`+strings.Join(triageDocs, `","`)+`"]}`)
		return err
	})
	var checked strings.Builder
	if !CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows"), &checked) {
		t.Fatal(checked.String())
	}
	base.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
	steps := &docTriageSteps{parallelSteps: &parallelSteps{workflowSteps: base, overlap: make(chan struct{})}, briefs: map[int]string{}, requests: map[int]run.Request{}}
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 12 && result.Landed == 0; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if len(steps.requests) != 5 || result.Landed != 1 || steps.builders != 1 || steps.ended != 4 {
		t.Fatalf("runs=%d builders=%d readers ended=%d result=%+v", len(steps.requests), steps.builders, steps.ended, result)
	}
	report := steps.briefs[5]
	if !strings.Contains(report, "Run only node report on the design seat; access: write.") || !strings.Contains(report, "Write one triage report") {
		t.Errorf("report brief:\n%s", report)
	}
	for i, doc := range triageDocs {
		reader := steps.briefs[i+1]
		if !strings.Contains(reader, fmt.Sprintf("Run only node read-%d on the scout seat; access: read_only.", i+1)) || !strings.Contains(reader, "Triage one document: "+doc+".") || !strings.Contains(reader, "Read only: do not change files") {
			t.Errorf("reader %d brief:\n%s", i+1, reader)
		}
		for j, other := range triageDocs {
			if j != i && strings.Contains(reader, other+".") {
				t.Errorf("reader %d carries %s", i+1, other)
			}
		}
		entry := fmt.Sprintf("Input finding from node read-%d: %s", i+1, filepath.Join(nodeOutputsDir(f.root, steps.requests[i+1].RunID), "finding"))
		if !strings.Contains(report, entry) {
			t.Errorf("report lacks %s", entry)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, node := range []string{"read-1", "read-2", "read-3", "read-4", "report"} {
			var outcome string
			if err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-a' AND node_id=?`, node).Scan(&outcome); err != nil {
				return fmt.Errorf("node %s: %w", node, err)
			}
			if outcome != "passed" {
				t.Errorf("node %s: %s", node, outcome)
			}
		}
		return nil
	})
}

// The graph a triage item selects is bound from its own record: each reader is a read-only
// scout node, and the report node writes only within the item's fence (it inherits it).
func TestDocTriageGraphIsBoundFromTheItem(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["design"] = "fixture"
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "tillr", "workflows", "doc-triage@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/doc-triage@v1.json"), string(raw))
	row := itemRow{ItemID: "item-a", Seat: "design", Workflow: "doc-triage@v1", WorkflowArgs: map[string]any{"docs": []any{"docs/design/a.md", "docs/design/b.md"}}}
	var w *Workflow
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var err error
		w, _, _, err = selectWorkflow(tx.Reader(), f.root, row, nil, l.cfg.Policy)
		return err
	})
	if len(w.Stages) != 3 {
		t.Fatalf("stages %+v", w.Stages)
	}
	for i, s := range w.Stages[:2] {
		if s.ID != fmt.Sprintf("read-%d", i+1) || s.Seat != "scout" || s.Access != "read_only" || !strings.Contains(s.Prompt, row.WorkflowArgs["docs"].([]any)[i].(string)) {
			t.Errorf("reader %+v", s)
		}
	}
	if s := w.Stages[2]; s.ID != "report" || s.Seat != "design" || s.Access != "write" || s.WriteScope.Paths != nil {
		t.Errorf("report %+v", s)
	}
	row.WorkflowArgs = nil
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, _, _, err := selectWorkflow(tx.Reader(), f.root, row, nil, l.cfg.Policy)
		if err == nil || !strings.Contains(err.Error(), "workflow-argument-missing: docs") {
			t.Errorf("an item with no documents: %v", err)
		}
		return nil
	})
}

// A document-less triage item carries its workflow's arguments in its record, so the
// document-fed prerequisite does not send it to triage-no-block for a document block it
// was never meant to have; the same item without arguments still goes there.
func TestAStoreDeclaredItemIsNotSentForADocumentBlock(t *testing.T) {
	const id = "item-doc-triage-2026-11-18-designs"
	f, l := generatedFixture(t, id)
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/doc-triage@v1.json"), mustReadTriageWorkflow(t))
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "triage workflow")
	l.cfg.Policy.Seats["scout"] = "fixture"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='design', body_ref='', workflow='doc-triage@v1', workflow_args='' WHERE item_id=?`, id)
		return err
	})
	// With no body reference to triage into, the request goes straight to the owner.
	if d := planFor(t, l, id); d.Reason != ReasonConflictOwner || d.BlockConflict == nil {
		t.Fatalf("control: an argument-less stored item should be held for a document: %+v", d)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow_args='{"docs":["docs/request.md"]}' WHERE item_id=?`, id)
		return err
	})
	d := planFor(t, l, id)
	if d.Reason == ReasonTriageNoBlock || d.Reason == ReasonConflictOwner || d.BlockConflict != nil {
		t.Fatalf("store-declared item was sent for a document block: %+v", d)
	}
}

func mustReadTriageWorkflow(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "tillr", "workflows", "doc-triage@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
