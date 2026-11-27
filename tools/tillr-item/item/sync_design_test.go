package item_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

func syncWithIndex(t *testing.T, s *store.Store, idx *vantage.VantageIndex, at time.Time, docs ...item.SyncDocument) item.SyncReport {
	t.Helper()
	var report item.SyncReport
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		report, err = item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{
			Documents: docs,
			Index:     idx,
			At:        at,
		})
		return err
	}); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	return report
}

func TestSyncMirrorsRoadmapDesignsSlice1AndIdempotentSecondSync(t *testing.T) {
	s := syncStore(t)
	// Seed stale design_without_item row to prove mirrored sync clears it.
	exec(t, s, `INSERT INTO design_without_item (document, roadmap, roadmap_line, first_seen) VALUES ('docs/design/accepted.md', 'docs/roadmap.md', 2, ?)`, syncAt.UnixMilli())

	rawIndex := `{
		"index": {
			"config": {
				"stages": {"SKETCH": "open", "DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"}
			},
			"documents": [
				{"path": "docs/design/accepted.md", "status": "accepted", "stage": "DECIDED", "questions": []},
				{"path": "docs/design/upstream.md", "status": "in-review", "stage": "DESIGN", "questions": [{"id": "OQ-UP1", "state": "open", "title": "Upstream open"}]},
				{"path": "docs/design/has-oq.md", "status": "in-review", "stage": "DECIDED", "dependsOn": [{"raw": "./upstream.md#OQ-UP1", "target": "docs/design/upstream.md", "fragment": "OQ-UP1", "line": 4}], "questions": [{"id": "OQ-H1", "state": "open", "title": "Own open"}]},
				{"path": "docs/design/parked.md", "status": "draft", "stage": "SKETCH", "questions": []}
			]
		}
	}`
	idx := vantage.ParseIndex([]byte(rawIndex), "slice1-fixture")

	roadmap := strings.Join([]string{
		"# Roadmap",
		"1. [Accepted](design/accepted.md)",
		"2. [Has OQ](design/has-oq.md)",
		"3. [Paired plan should be skipped](design/accepted-plan.md)",
		"## Icebox",
		"- [Parked](design/parked.md)",
		"- Documentless icebox bullet",
	}, "\n")

	docs := []item.SyncDocument{
		doc("docs/roadmap.md", roadmap),
		doc("docs/design/accepted.md", "---\ntitle: \"Accepted Design\"\nstage: DECIDED\n---\n# Accepted\n"),
		doc("docs/design/accepted-plan.md", "---\nstage: SKETCH\n---\n# Plan\n"),
		doc("docs/design/has-oq.md", "---\ntitle: \"Open Question Design\"\nstage: DECIDED\n---\n# Has OQ\n"),
		doc("docs/design/upstream.md", "---\nstage: DESIGN\n---\n# Upstream\n"),
		doc("docs/design/parked.md", "---\ntitle: \"Parked Design\"\nstage: SKETCH\n---\n# Parked\n"),
	}

	report := syncWithIndex(t, s, &idx, syncAt, docs...)
	if len(report.Warnings) != 0 {
		t.Fatalf("expected zero design-without-item warnings, got %+v", report.Warnings)
	}
	if n := count(t, s, `SELECT count(*) FROM design_without_item`); n != 0 {
		t.Fatalf("expected zero design_without_item rows, got %d", n)
	}
	if report.IceboxWithoutDoc != 1 {
		t.Fatalf("icebox_without_doc = %d, want 1", report.IceboxWithoutDoc)
	}
	if got := item.SyncText(report, syncAt); !strings.Contains(got, "icebox_without_doc: 1") {
		t.Fatalf("SyncText missing icebox_without_doc: %s", got)
	}
	if len(report.Created) != 3 {
		t.Fatalf("created = %v, want 3 design items (and no paired plan)", report.Created)
	}

	type row struct {
		id, state, seat, rank, waiting string
	}
	readRow := func(id string) row {
		t.Helper()
		var r row
		r.id = id
		if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT state, seat, rank, waiting_on FROM items WHERE item_id=?`, id).Scan(&r.state, &r.seat, &r.rank, &r.waiting)
		}); err != nil {
			t.Fatalf("reading %s: %v", id, err)
		}
		return r
	}

	acc := readRow("item-design-design-accepted")
	if acc.state != "in-queue" || acc.seat != "plan" || acc.rank == "" || acc.waiting != "" {
		t.Fatalf("accepted row = %+v", acc)
	}
	oq := readRow("item-design-design-has-oq")
	if oq.state != "in-queue" || oq.seat != "design" || oq.rank == "" || oq.waiting != "" {
		t.Fatalf("has-oq row = %+v", oq)
	}
	if !(acc.rank < oq.rank) {
		t.Fatalf("roadmap order not preserved: accepted rank %q >= has-oq rank %q", acc.rank, oq.rank)
	}
	parked := readRow("item-design-design-parked")
	if parked.state != "blocked" || parked.seat != "design" || parked.rank != "" {
		t.Fatalf("parked row = %+v", parked)
	}
	var w item.WaitingOn
	if err := json.Unmarshal([]byte(parked.waiting), &w); err != nil || w.Waiter != "owner" || w.Reason != "icebox" {
		t.Fatalf("parked waiting_on = %q (%v)", parked.waiting, err)
	}

	// Second sync with no document change creates, changes, and closes nothing.
	seqBefore := column(t, s, `SELECT COALESCE(MAX(seq), 0) FROM events`)
	second := syncWithIndex(t, s, &idx, syncAt.Add(time.Minute), docs...)
	if len(second.Created) != 0 || len(second.Updated) != 0 || len(second.Parked) != 0 || len(second.Unparked) != 0 || len(second.Unchanged) != 3 {
		t.Fatalf("second sync not idempotent: %+v", second)
	}
	seqAfter := column(t, s, `SELECT COALESCE(MAX(seq), 0) FROM events`)
	if seqBefore != seqAfter {
		t.Fatalf("second sync wrote events: %s -> %s", seqBefore, seqAfter)
	}
}

func TestSyncMirrorsRespectsExistingCoverageAndTransitions(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{
		doc("docs/roadmap.md", "# Roadmap\n- [With block](design/with-block.md)\n- [With manual](design/with-manual.md)\n- [Target](design/target.md)\n- [Both](design/both.md)\n## Icebox\n- [Both](design/both.md)\n"),
		doc("docs/design/with-block.md", "---\nstage: DECIDED\n---\n"+goodBlock),
		doc("docs/design/with-manual.md", "---\nstage: DECIDED\n---\n# Manual\n"),
		doc("docs/design/target.md", "---\nstage: DESIGN\n---\n# Target\n"),
		doc("docs/design/both.md", "---\nstage: DECIDED\n---\n# Both\n"),
	}
	// Create an existing unfinished item naming docs/design/with-manual.md.
	runSync(t, s, "", doc("docs/other.md", strings.ReplaceAll(goodBlock, "item-build-thing", "item-manual-1")))
	exec(t, s, `UPDATE items SET design_doc='docs/design/with-manual.md' WHERE item_id='item-manual-1'`)

	idx1 := indexForStages(
		map[string]string{"DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"},
		map[string]string{
			"docs/design/with-block.md":  "DECIDED",
			"docs/design/with-manual.md": "DECIDED",
			"docs/design/target.md":      "DESIGN",
			"docs/design/both.md":        "DECIDED",
		},
	)
	r1 := syncWithIndex(t, s, idx1, syncAt, docs...)
	// Only target and both should get item-design-* rows; with-block and with-manual get none.
	if n := count(t, s, `SELECT count(*) FROM items WHERE item_id IN ('item-design-design-with-block','item-design-design-with-manual')`); n != 0 {
		t.Fatalf("existing coverage got second item (%d)", n)
	}
	if len(r1.Created) != 3 { // item-build-thing (from goodBlock), item-design-design-target, item-design-design-both
		t.Fatalf("created = %v", r1.Created)
	}
	// A design linked on both spine and Icebox stays unparked (in-queue).
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-both'`); st != "in-queue" {
		t.Fatalf("both state = %q, want in-queue", st)
	}

	// Seat update: target becomes DECIDED -> seat moves from design to plan.
	idx2 := indexForStages(
		map[string]string{"DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"},
		map[string]string{
			"docs/design/target.md": "DECIDED",
			"docs/design/both.md":   "DECIDED",
		},
	)
	syncWithIndex(t, s, idx2, syncAt.Add(time.Minute), docs...)
	if seat := column(t, s, `SELECT seat FROM items WHERE item_id='item-design-design-target'`); seat != "plan" {
		t.Fatalf("seat after DECIDED = %q, want plan", seat)
	}

	// Park target by moving it to Icebox; unroute both.
	docs[0] = doc("docs/roadmap.md", "# Roadmap\n- [With block](design/with-block.md)\n- [With manual](design/with-manual.md)\n## Icebox\n- [Target](design/target.md)\n")
	r3 := syncWithIndex(t, s, idx2, syncAt.Add(2*time.Minute), docs...)
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-target'`); st != "blocked" {
		t.Fatalf("parked target state = %q, want blocked", st)
	}
	if len(r3.Parked) != 1 || r3.Parked[0] != "item-design-design-target" {
		t.Fatalf("parked = %v", r3.Parked)
	}
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-both'`); st != "closed" {
		t.Fatalf("unrouted both state = %q, want closed", st)
	}

	// Unpark target back to spine, then mark it BUILT -> resolved-elsewhere.
	docs[0] = doc("docs/roadmap.md", "# Roadmap\n- [With block](design/with-block.md)\n- [With manual](design/with-manual.md)\n- [Target](design/target.md)\n")
	r4 := syncWithIndex(t, s, idx2, syncAt.Add(3*time.Minute), docs...)
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-target'`); st != "in-queue" {
		t.Fatalf("unparked target state = %q, want in-queue", st)
	}
	if len(r4.Unparked) != 1 || r4.Unparked[0] != "item-design-design-target" {
		t.Fatalf("unparked = %v", r4.Unparked)
	}

	idxBuilt := indexForStages(
		map[string]string{"DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"},
		map[string]string{"docs/design/target.md": "BUILT"},
	)
	syncWithIndex(t, s, idxBuilt, syncAt.Add(4*time.Minute), docs...)
	if st := column(t, s, `SELECT state FROM items WHERE item_id='item-design-design-target'`); st != "resolved-elsewhere" {
		t.Fatalf("built target state = %q, want resolved-elsewhere", st)
	}
	if via := column(t, s, `SELECT resolves_via FROM items WHERE item_id='item-design-design-target'`); via != "doc:docs/design/target.md" {
		t.Fatalf("resolves_via = %q, want doc:docs/design/target.md", via)
	}
}

func TestSyncMirrorsFailClosedOnRefusedIndexAndCheckDocumentSharingExemptsDesignItems(t *testing.T) {
	s := syncStore(t)
	refused := vantage.ParseIndex([]byte(`{"index":{"refused":true,"documents":[]}}`), "refused-fixture")
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{
			Index: &refused,
			Documents: []item.SyncDocument{
				doc("docs/roadmap.md", "[D](design/d.md)"),
				doc("docs/design/d.md", "---\nstage: DESIGN\n---\n# D\n"),
			},
			At: syncAt,
		})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "index-unreadable") {
		t.Fatalf("refused index did not fail closed: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM items`); n != 0 {
		t.Fatalf("refused index wrote %d items", n)
	}

	// Now create a real item-design-* row and verify CheckDocumentSharing never reports it unshared_open.
	valid := indexForStages(map[string]string{"DESIGN": "open"}, map[string]string{"docs/design/d.md": "DESIGN"})
	docs := []item.SyncDocument{
		doc("docs/roadmap.md", "[D](design/d.md)"),
		doc("docs/design/d.md", "---\nstage: DESIGN\n---\n# D\n"),
	}
	syncWithIndex(t, s, valid, syncAt, docs...)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		state, err := item.ReadSyncStoreState(r)
		if err != nil {
			return err
		}
		_, unshared, _, err := item.CheckDocumentSharing(state, "", docs)
		if err != nil {
			return err
		}
		if len(unshared) != 0 {
			t.Fatalf("item-design-* reported as unshared_open: %v", unshared)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
