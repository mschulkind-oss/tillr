package item_test

import (
	"context"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func reconcileTree(t *testing.T, entries []map[string]any, content map[string]string) item.ReconcileInput {
	t.Helper()
	root := scratch(t)
	for _, e := range entries {
		p := e["path"].(string)
		file := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content[p]), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range content {
		file := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(c), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		cmd := osexec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	raw, err := json.Marshal(map[string]any{"index": map[string]any{"documents": entries}})
	if err != nil {
		t.Fatal(err)
	}
	return item.ReconcileInput{Root: root, Command: []string{"python3", "-c", "import sys; print(sys.argv[1])", string(raw)}}
}
func reconcileEntry(path, stage, status string) map[string]any {
	return map[string]any{"path": path, "stage": stage, "status": status}
}
func reconcileRun(t *testing.T, in item.ReconcileInput, s *store.Store) item.ReconcileReport {
	t.Helper()
	var report item.ReconcileReport
	var err error
	if s == nil {
		report, err = item.Reconcile(context.Background(), nil, in)
	} else {
		err = s.ReadTx(context.Background(), func(r *store.Reader) error { report, err = item.Reconcile(context.Background(), r, in); return err })
	}
	if err != nil {
		t.Fatal(err)
	}
	return report
}
func TestReconcileSixCases(t *testing.T) {
	entries := []map[string]any{
		reconcileEntry("docs/unstaged.md", "", "accepted"),
		reconcileEntry("docs/current.md", "CURRENT", "accepted"),
		reconcileEntry("docs/built.md", "BUILT", ""),
		reconcileEntry("docs/blocks.md", "DESIGN", "accepted"),
		reconcileEntry("docs/design.md", "DECIDED", "accepted"),
		reconcileEntry("docs/draft.md", "SKETCH", "draft"),
	}
	in := reconcileTree(t, entries, map[string]string{"docs/blocks.md": goodBlock, "docs/outside.md": "no status"})
	report := reconcileRun(t, in, nil)
	want := map[string]string{"docs/unstaged.md": "unstaged", "docs/current.md": "terminal", "docs/built.md": "built", "docs/blocks.md": "decomposable", "docs/design.md": "design", "docs/draft.md": "held"}
	for _, d := range report.Documents {
		if d.Case != want[d.Path] {
			t.Errorf("%s: %s want %s", d.Path, d.Case, want[d.Path])
		}
	}
	total := 0
	for _, n := range report.Counts {
		if n != 1 {
			t.Errorf("count %d want 1", n)
		}
		total += n
	}
	if total != len(entries) {
		t.Fatalf("counts %v", report.Counts)
	}
	text := item.FormatReconcileReport(report)
	for _, s := range []string{`id="item-graduate-built" seat="design" paths="docs/"`, `id="item-design-design" seat="design" paths="docs/design.md,docs/roadmap.md,docs/tillr/roadmap.md"`, "stage: BUILT", "docs/draft.md: held needs-design", "outside-index: docs/outside.md"} {
		if !strings.Contains(text, s) {
			t.Errorf("missing %q in %s", s, text)
		}
	}
	if again := item.FormatReconcileReport(reconcileRun(t, in, nil)); again != text {
		t.Fatal("report changed on repeat")
	}
}
func TestReconcileRefusesBeforeReport(t *testing.T) {
	for _, raw := range []string{`{"index":{"refused":true,"documents":[{"path":"docs/a.md"}]}}`, `{"index":{"documents":[]}}`, `{}`, `not json`, `{"index":{"documents":[{"path":"docs/a.md"}],"unreadable":["docs/b.md"]}}`, `{"index":{"documents":[{"path":"docs/a.md"}],"skipped":["docs/b.md"]}}`} {
		t.Run(raw, func(t *testing.T) {
			in := item.ReconcileInput{Root: scratch(t), Command: []string{"python3", "-c", "import sys; print(sys.argv[1])", raw}}
			report, err := item.Reconcile(context.Background(), nil, in)
			if err == nil || len(report.Documents) != 0 {
				t.Fatalf("partial report %v, error %v", report, err)
			}
		})
	}
	in := reconcileTree(t, []map[string]any{reconcileEntry("docs/A_b.md", "BUILT", ""), reconcileEntry("docs/a-b.md", "DESIGN", "accepted")}, nil)
	report, err := item.Reconcile(context.Background(), nil, in)
	if err == nil || !strings.Contains(err.Error(), "slug-collision") || !strings.Contains(err.Error(), "docs/A_b.md") || !strings.Contains(err.Error(), "docs/a-b.md") || len(report.Documents) != 0 {
		t.Fatalf("report %v error %v", report, err)
	}
	in.Command = []string{"missing-reconcile-index-command"}
	if report, err = item.Reconcile(context.Background(), nil, in); err == nil || len(report.Documents) != 0 {
		t.Fatalf("report %v error %v", report, err)
	}
}
func TestReconcileHoldReasonsAndSlug(t *testing.T) {
	entries := []map[string]any{reconcileEntry("docs/pi/activity-ui/Plan.md", "BUILT", ""), reconcileEntry("docs/open.md", "DESIGN", "accepted"), reconcileEntry("docs/unruled.md", "DECIDED", ""), reconcileEntry("docs/sketch.md", "SKETCH", "draft")}
	entries[1]["questions"] = []map[string]string{{"id": "OQ-1", "state": "closed"}, {"id": "OQ-2", "state": "answered"}, {"id": "OQ-3", "state": "open"}}
	text := item.FormatReconcileReport(reconcileRun(t, reconcileTree(t, entries, nil), nil))
	for _, s := range []string{"item-graduate-pi-activity-ui-plan", "docs/open.md: held question-open OQ-1,OQ-3", "docs/unruled.md: held unruled", "docs/sketch.md: held needs-design"} {
		if !strings.Contains(text, s) {
			t.Errorf("missing %q: %s", s, text)
		}
	}
}
func TestReconcileCarryingAndBuiltInference(t *testing.T) {
	s := syncStore(t)
	entries := []map[string]any{reconcileEntry("docs/build.md", "DESIGN", "accepted"), reconcileEntry("docs/carried.md", "DECIDED", "accepted"), reconcileEntry("docs/graduated.md", "BUILT", ""), reconcileEntry("docs/block.md", "DECIDED", "accepted")}
	in := reconcileTree(t, entries, map[string]string{"docs/graduated.md": strings.Replace(goodBlock, "item-build-thing", "item-graduate-graduated", 1), "docs/block.md": goodBlock})
	runSync(t, s, "", doc("docs/build.md", strings.Replace(goodBlock, "item-build-thing", "item-inferred", 1)), doc("docs/carried.md", strings.Replace(goodBlock, "item-build-thing", "item-carried", 1)), doc("docs/block.md", goodBlock))
	exec(t, s, `UPDATE items SET state='landed' WHERE item_id IN ('item-inferred','item-build-thing')`)
	// Only body_ref names build.md; design_doc must not be required to carry it.
	exec(t, s, `UPDATE items SET design_doc='' WHERE item_id='item-inferred'`)
	report := reconcileRun(t, in, s)
	text := item.FormatReconcileReport(report)
	for _, v := range []string{"docs/build.md: built inferred-from-landed-items", "docs/block.md: built inferred-from-landed-items", "docs/carried.md: design carried", "docs/graduated.md: built stage: BUILT graduating"} {
		if !strings.Contains(text, v) {
			t.Errorf("missing %q: %s", v, text)
		}
	}
	if strings.Contains(text, `id="item-graduate-graduated"`) || strings.Contains(text, `id="item-design-carried"`) {
		t.Fatalf("already carried block proposed: %s", text)
	}
	without := item.FormatReconcileReport(reconcileRun(t, in, nil))
	if !strings.Contains(without, `id="item-design-build"`) || !strings.Contains(without, "docs/block.md: decomposable") {
		t.Fatal(without)
	}
	// Any unfinished carrying item prevents inference, even alongside a landing.
	exec(t, s, `UPDATE items SET design_doc='docs/build.md' WHERE item_id='item-carried'`)
	for _, state := range []string{"not-started", "in-progress"} {
		exec(t, s, `UPDATE items SET state=? WHERE item_id='item-carried'`, state)
		text = item.FormatReconcileReport(reconcileRun(t, in, s))
		if strings.Contains(text, "docs/build.md: built") {
			t.Fatalf("unfinished carrier %s inferred built: %s", state, text)
		}
	}
}
func TestReconcileRepositoryStructure(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	report := reconcileRun(t, item.ReconcileInput{Root: root}, nil)
	total := 0
	for _, n := range report.Counts {
		total += n
	}
	if len(report.Counts) != 6 || total != len(report.Documents) || total == 0 {
		t.Fatalf("invalid partition: %v, documents %d", report.Counts, len(report.Documents))
	}
	seen := map[string]bool{}
	for _, d := range report.Documents {
		if seen[d.Path] {
			t.Fatalf("duplicate %s", d.Path)
		}
		seen[d.Path] = true
	}
}
