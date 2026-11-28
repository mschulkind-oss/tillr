package item_test

import (
	"context"
	"crypto/sha1" //nolint:gosec // git's blob identity
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

var companionStages = map[string]string{"DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"}

const companionRoadmap = "# Roadmap\n- [Foo](design/foo.md)\n"

const companionTasks = `## Tasks

### Task t1: Build the independent part

Why paragraph.

- **After:** none
- **Verify:** ` + "`go test ./...`" + `
- **Criteria:**
  - **c1:** The independent part works.

### Task t2: Build the part that needs the ruling

- **After:** t1
- **Seat:** edit
- **Waits on questions:**
  - ` + "`docs/design/foo.md#OQ-F1`" + ` — picks the storage format
- **Claims:** c2=direction
- **Criteria:**
  - **c1:** The dependent part works.
  - **c2:** The owner agrees with the direction.

### Task t3: Build the part that waits on prose

- **Waits on questions:** ` + "`docs/design/foo.md#open-choice` (\"Which format do we use?\")" + ` — needs that choice
- **Criteria:**
  - **c1:** The prose-dependent part works.
`

func companionPlanDoc(stage, tasks string) string {
	return "---\nstatus: accepted\nstage: " + stage + "\nplanning-actions: none\ndepends-on:\n  - foo.md\n---\n# Foo plan\n\n" + tasks
}

const companionDesign = "---\nstage: DESIGN\n---\n# Foo\n\n## Open choice\n\nWhich format do we use?\n"

func companionIndex(stages map[string]string) *vantage.VantageIndex {
	return indexForStages(companionStages, stages)
}

func blobSHA(content string) string {
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "blob %d\x00%s", len(content), content)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestCompanionPlanSyncImportsTasksAndRecordsWaits(t *testing.T) {
	s := syncStore(t)
	idx := companionIndex(map[string]string{"docs/design/foo.md": "DESIGN", "docs/design/foo-plan.md": "DECIDED"})
	docs := []item.SyncDocument{
		doc("docs/roadmap.md", companionRoadmap),
		doc("docs/design/foo.md", companionDesign),
		doc("docs/design/foo-plan.md", companionPlanDoc("DECIDED", companionTasks)),
	}
	report := syncWithIndex(t, s, idx, syncAt, docs...)
	if len(report.Findings) != 0 {
		t.Fatalf("findings: %+v", report.Findings)
	}
	for _, id := range []string{"item-design-design-foo", "item-foo-t1", "item-foo-t2", "item-foo-t3"} {
		if n := count(t, s, `SELECT count(*) FROM items WHERE item_id=?`, id); n != 1 {
			t.Fatalf("%s missing; created = %v", id, report.Created)
		}
	}
	if got := column(t, s, `SELECT design_doc || '|' || body_ref || '|' || seat || '|' || state FROM items WHERE item_id='item-foo-t1'`); got != "docs/design/foo.md|docs/design/foo-plan.md#task-t1-build-the-independent-part|build|in-queue" {
		t.Fatalf("t1 row = %q", got)
	}
	if got := column(t, s, `SELECT seat FROM items WHERE item_id='item-foo-t2'`); got != "edit" {
		t.Fatalf("t2 seat = %q", got)
	}
	if n := count(t, s, `SELECT count(*) FROM item_edges WHERE item_id='item-foo-t2' AND blocked_by='item-foo-t1'`); n != 1 {
		t.Fatalf("t2 is not blocked by t1")
	}
	// The design item stays: companion pieces do not cover it.
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-foo'`); st != "in-queue" {
		t.Fatalf("design item state = %q", st)
	}

	var waits map[string]item.QuestionWaits
	read := func() {
		t.Helper()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			var err error
			waits, err = item.ReadQuestionWaits(r)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if w, ok := waits["item-foo-t1"]; !ok || len(w.Refs) != 0 {
		t.Fatalf("t1 must be registered with no refs, got %+v present=%v", w, ok)
	}
	if w := waits["item-foo-t2"]; len(w.Refs) != 1 || w.Refs[0].Canonical() != "docs/design/foo.md#OQ-F1" || w.Refs[0].Reason != "picks the storage format" {
		t.Fatalf("t2 waits = %+v", w)
	}
	w3 := waits["item-foo-t3"]
	if len(w3.Refs) != 1 || w3.Refs[0].HeadingSlug != "open-choice" || w3.Refs[0].QuotedQuestion != "Which format do we use?" || w3.Refs[0].SectionSHA256 == "" || w3.Refs[0].Reason != "needs that choice" {
		t.Fatalf("t3 waits = %+v", w3)
	}

	// A worker-added wait and an override survive the next sync; plan rows are replaced.
	exec(t, s, `INSERT INTO item_question_waits(item_id, ref_key, doc_path, question_id, reason, source) VALUES('item-foo-t1','docs/design/foo.md#OQ-F9','docs/design/foo.md','OQ-F9','worker','worker-obstacle')`)
	exec(t, s, `INSERT INTO item_question_overrides(item_id, ref_key, op, reason) VALUES('item-foo-t2','docs/design/foo.md#OQ-F1','drop','operator')`)
	changed := strings.Replace(companionTasks, "picks the storage format", "picks the on-disk format", 1)
	docs[2] = doc("docs/design/foo-plan.md", companionPlanDoc("DECIDED", changed))
	syncWithIndex(t, s, idx, syncAt.Add(time.Minute), docs...)
	read()
	if w := waits["item-foo-t1"]; len(w.Refs) != 1 || w.Refs[0].Canonical() != "docs/design/foo.md#OQ-F9" {
		t.Fatalf("worker wait lost: %+v", w)
	}
	if w := waits["item-foo-t2"]; len(w.Refs) != 0 {
		t.Fatalf("drop override lost: %+v", w)
	}
	if got := column(t, s, `SELECT reason FROM item_question_waits WHERE item_id='item-foo-t2'`); got != "picks the on-disk format" {
		t.Fatalf("plan row not replaced: %q", got)
	}

	// Sharing: the plan declares its tasks, so none is private residue.
	var state item.SyncStoreState
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		state, err = item.ReadSyncStoreState(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, unshared, _, err := item.CheckDocumentSharing(state, "", docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(unshared) != 0 {
		t.Fatalf("unshared_open = %v, want none", unshared)
	}
}

func TestCompanionPlanSyncIgnoresSketchAndBuiltPlans(t *testing.T) {
	for _, stage := range []string{"SKETCH", "BUILT"} {
		t.Run(stage, func(t *testing.T) {
			s := syncStore(t)
			idx := companionIndex(map[string]string{"docs/design/foo.md": "DESIGN", "docs/design/foo-plan.md": stage})
			report := syncWithIndex(t, s, idx, syncAt,
				doc("docs/roadmap.md", companionRoadmap),
				doc("docs/design/foo.md", companionDesign),
				doc("docs/design/foo-plan.md", companionPlanDoc(stage, companionTasks)))
			if n := count(t, s, `SELECT count(*) FROM items WHERE item_id LIKE 'item-foo-%'`); n != 0 {
				t.Fatalf("%s plan imported %d tasks", stage, n)
			}
			if len(report.Findings) != 0 {
				t.Fatalf("findings: %+v", report.Findings)
			}
		})
	}
}

func TestCompanionPlanSyncRefusesInvalidPlans(t *testing.T) {
	cases := map[string]struct{ rule, plan string }{
		"question directive":  {"plan-has-question", companionPlanDoc("DECIDED", companionTasks) + "\n<!-- vantage: question id=OQ-X1 -->\n"},
		"unknown seat":        {"plan-task-invalid", companionPlanDoc("DECIDED", strings.Replace(companionTasks, "**Seat:** edit", "**Seat:** Bad_Seat", 1))},
		"cycle":               {"plan-task-invalid", companionPlanDoc("DECIDED", strings.Replace(companionTasks, "- **After:** none", "- **After:** t2", 1))},
		"no criteria":         {"plan-task-invalid", companionPlanDoc("DECIDED", strings.Replace(companionTasks, "  - **c1:** The independent part works.\n", "", 1))},
		"unknown after":       {"plan-task-invalid", companionPlanDoc("DECIDED", strings.Replace(companionTasks, "**After:** t1", "**After:** t9", 1))},
		"bad wait":            {"plan-task-invalid", companionPlanDoc("DECIDED", strings.Replace(companionTasks, "docs/design/foo.md#open-choice", "docs/design/foo.md#no-such-heading", 1))},
		"no planning-actions": {"plan-task-invalid", strings.Replace(companionPlanDoc("DECIDED", companionTasks), "planning-actions: none\n", "", 1)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			idx := companionIndex(map[string]string{"docs/design/foo.md": "DESIGN", "docs/design/foo-plan.md": "DECIDED"})
			docs := []item.SyncDocument{
				doc("docs/roadmap.md", companionRoadmap),
				doc("docs/design/foo.md", companionDesign),
				doc("docs/design/foo-plan.md", c.plan),
			}
			s := syncStore(t)
			report := syncWithIndex(t, s, idx, syncAt, docs...)
			found := false
			for _, f := range report.Findings {
				if f.Rule == c.rule && f.File == "docs/design/foo-plan.md" {
					found = true
				}
			}
			if !found {
				t.Fatalf("want a %s finding, got %+v", c.rule, report.Findings)
			}
			if n := count(t, s, `SELECT count(*) FROM items WHERE item_id LIKE 'item-foo-%'`); n != 0 {
				t.Fatalf("invalid plan wrote %d pieces", n)
			}
			// A dry run reports the same finding and writes nothing.
			dry, err := item.Sync(context.Background(), nil, nil, item.SyncInput{Documents: docs, Index: idx, At: syncAt})
			if err != nil {
				t.Fatal(err)
			}
			if !dry.DryRun || len(dry.Findings) != len(report.Findings) {
				t.Fatalf("dry run findings = %+v, want %+v", dry.Findings, report.Findings)
			}
		})
	}
}

func TestCompanionPlanSyncParksAndUnparksWithTheDesign(t *testing.T) {
	s := syncStore(t)
	idx := companionIndex(map[string]string{"docs/design/foo.md": "DESIGN", "docs/design/foo-plan.md": "DECIDED"})
	plan := doc("docs/design/foo-plan.md", companionPlanDoc("DECIDED", companionTasks))
	design := doc("docs/design/foo.md", companionDesign)
	parked := []item.SyncDocument{doc("docs/roadmap.md", "# Roadmap\n## Icebox\n- [Foo](design/foo.md)\n"), design, plan}
	syncWithIndex(t, s, idx, syncAt, parked...)
	if got := column(t, s, `SELECT state || '|' || rank || '|' || waiting_on FROM items WHERE item_id='item-foo-t1'`); !strings.HasPrefix(got, "blocked||") || !strings.Contains(got, "icebox") {
		t.Fatalf("parked piece = %q", got)
	}
	report := syncWithIndex(t, s, idx, syncAt.Add(time.Minute), doc("docs/roadmap.md", companionRoadmap), design, plan)
	if got := column(t, s, `SELECT state FROM items WHERE item_id='item-foo-t1'`); got != "in-queue" {
		t.Fatalf("unparked piece state = %q", got)
	}
	if got := column(t, s, `SELECT rank FROM items WHERE item_id='item-foo-t1'`); got == "" {
		t.Fatalf("unparked piece has no rank; report %+v", report)
	}
	found := false
	for _, id := range report.Unparked {
		found = found || id == "item-foo-t1"
	}
	if !found {
		t.Fatalf("unparked = %v", report.Unparked)
	}
	syncWithIndex(t, s, idx, syncAt.Add(2*time.Minute), parked...)
	if got := column(t, s, `SELECT state FROM items WHERE item_id='item-foo-t3'`); got != "blocked" {
		t.Fatalf("re-parked piece state = %q", got)
	}
}

func TestSyncRoutesUnstagedMarkdownOutsideDesignDirectory(t *testing.T) {
	s := syncStore(t)
	idx := companionIndex(map[string]string{})
	report := syncWithIndex(t, s, idx, syncAt,
		doc("docs/roadmap.md", "# Roadmap\n- [Notes](notes/plain.md)\n- [Ref](reference/page.md)\n"),
		doc("docs/notes/plain.md", "# A plain note with no frontmatter\n"),
		doc("docs/reference/page.md", "# A reference page\n"))
	if n := count(t, s, `SELECT count(*) FROM items WHERE item_id='item-design-notes-plain'`); n != 1 {
		t.Fatalf("unstaged note not routed; created = %v", report.Created)
	}
	if n := count(t, s, `SELECT count(*) FROM items WHERE item_id LIKE 'item-design-reference%'`); n != 0 {
		t.Fatalf("unstaged reference page routed")
	}
}

func TestSyncFrontmatterFreeDesignFinishesWhenItsPlanIsBuiltAndStamped(t *testing.T) {
	design := "# Plain design\n\nNo frontmatter here.\n"
	plan := func(stamp string) string {
		return companionPlanDoc("BUILT", "**Design stamped at:** `"+stamp+"`\n\n"+companionTasks)
	}
	idx := companionIndex(map[string]string{"docs/design/foo-plan.md": "BUILT"})
	roadmap := doc("docs/roadmap.md", companionRoadmap)

	// Outstanding first: no plan yet, so the design item exists.
	s := syncStore(t)
	syncWithIndex(t, s, idx, syncAt, roadmap, doc("docs/design/foo.md", design))
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-foo'`); st != "in-queue" {
		t.Fatalf("design item state = %q", st)
	}
	// A BUILT plan stamped with the design's blob sha finishes it.
	syncWithIndex(t, s, idx, syncAt.Add(time.Minute), roadmap, doc("docs/design/foo.md", design), doc("docs/design/foo-plan.md", plan(blobSHA(design))))
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-foo'`); st != "resolved-elsewhere" {
		t.Fatalf("design item state = %q, want resolved-elsewhere", st)
	}

	// A stamp for other bytes leaves the design outstanding, and a new design gets an item.
	s2 := syncStore(t)
	syncWithIndex(t, s2, idx, syncAt, roadmap, doc("docs/design/foo.md", design+"changed\n"), doc("docs/design/foo-plan.md", plan(blobSHA(design))))
	if st := column(t, s2, `SELECT state FROM items WHERE item_id='item-design-design-foo'`); st != "in-queue" {
		t.Fatalf("changed design state = %q, want in-queue", st)
	}
}
