package item_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

var syncAt = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// doc is one in-memory tracked document.
func doc(path, content string) item.SyncDocument {
	return item.SyncDocument{Path: path, Content: []byte(content)}
}

// syncStore is an empty store under scratch.
func syncStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(scratch(t), "state.db"), true)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// runSync writes one sync of docs into s and answers the report. The root is empty, so no
// path-existence warning is computed unless a case names one.
func runSync(t *testing.T, s *store.Store, root string, docs ...item.SyncDocument) item.SyncReport {
	t.Helper()
	var report item.SyncReport
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		report, err = item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Root: root, Documents: docs, At: syncAt})
		return err
	})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	return report
}

func count(t *testing.T, s *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(query, args...).Scan(&n)
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func column(t *testing.T, s *store.Store, query string, args ...any) string {
	t.Helper()
	var v string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(query, args...).Scan(&v)
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func exec(t *testing.T, s *store.Store, query string, args ...any) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestSyncForkFenceIsMaterializedByStaging(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	report := runSync(t, s, root, doc("docs/fork.md", strings.Replace(goodBlock, `paths="tools/thing/"`, `paths=".forks/lanes/lane-1/pi/src/"`, 1)))
	if len(report.Findings) != 0 {
		t.Fatalf("fork item findings: %+v", report.Findings)
	}
	for _, warning := range report.Warnings {
		if warning.Rule == "path-missing" {
			t.Fatalf("staging's fork checkout was warned as missing: %+v", warning)
		}
	}
}

const goodBlock = `# Plan

### Build the thing

<!-- tillr: item id="item-build-thing" paths="tools/thing/" -->

- **c1:** The thing builds.
`

// The two queue-persistence documents mention the directive inside prose and inline code
// about forty times; a scanner that only skipped fences read each one as a malformed block
// (D-TC1). Neither inline mentions nor a whole directive inside a fence is a block.
func TestSyncIgnoresInlineMentionsAndFencedDirectives(t *testing.T) {
	content := "# Notes\n\n" +
		"Zero `<!-- tillr: item` blocks exist outside the example.\n" +
		"A block is `<!-- tillr: item id=\"item-x\" paths=\"a/\" -->` written on its own line.\n" +
		"- **Shape:** `<!-- tillr: item id=\"item-y\" -->` under a heading.\n" +
		"> <!-- tillr: item id=\"item-quoted\" paths=\"a/\" -->\n\n" +
		"```markdown\n### Example\n<!-- tillr: item id=\"item-fenced\" paths=\"a/\" -->\n\n- **c1:** fenced\n```\n\n" +
		"~~~\n<!-- tillr: item\n     id=\"item-tilde\" -->\n~~~\n\n" +
		"  ````md\n  <!-- tillr: item id=\"item-indented-fence\" paths=\"a/\" -->\n  ```\n  still fenced\n  ````\n"
	s := syncStore(t)
	report := runSync(t, s, "", doc("docs/design/notes.md", content))
	if report.Blocks != 0 || len(report.Findings) != 0 || report.CreatedCount != 0 {
		t.Fatalf("inline and fenced mentions were read as blocks: blocks %d, findings %v, created %v",
			report.Blocks, report.Findings, report.Created)
	}
	if n := count(t, s, `SELECT count(*) FROM items`); n != 0 {
		t.Fatalf("the store holds %d items, want 0", n)
	}
}

// Attributes may be wrapped one per line, and a criterion's text may continue on the lines
// below its bullet. The fields map exactly as the ruling's Section 1 says.
func TestSyncReadsAMultiLineDirectiveAndMapsItsFields(t *testing.T) {
	content := `# Design

### Add the read API

<!-- tillr: item
     id="item-read-api"
     paths=" tools/view/ , tools/view-web/,"
     blocked_by="item-base"
-->

- **c1:** The route answers
  with the payload,
  and nothing else.
- **c2:** It writes nothing.
- **Not a criterion:** prose that follows.

### Base

<!-- tillr: item id="item-base" paths="tools/base/" -->

- **c1:** The base exists.
`
	s := syncStore(t)
	report := runSync(t, s, "", doc("docs/design/api.md", content))
	if len(report.Findings) != 0 {
		t.Fatalf("findings: %v", report.Findings)
	}
	if report.CreatedCount != 2 {
		t.Fatalf("created %v, want two items", report.Created)
	}
	var stored struct{ title, bodyRef, designDoc, paths, state, rank, seat string }
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT title, body_ref, design_doc, allowed_paths, state, rank, seat FROM items WHERE item_id = 'item-read-api'`).
			Scan(&stored.title, &stored.bodyRef, &stored.designDoc, &stored.paths, &stored.state, &stored.rank, &stored.seat)
	}); err != nil {
		t.Fatalf("reading the item: %v", err)
	}
	if stored.title != "Add the read API" {
		t.Errorf("title %q", stored.title)
	}
	if stored.bodyRef != "docs/design/api.md#add-the-read-api" || stored.designDoc != "docs/design/api.md" {
		t.Errorf("body_ref %q design_doc %q", stored.bodyRef, stored.designDoc)
	}
	if stored.paths != "" {
		t.Errorf("allowed_paths %s", stored.paths)
	}
	if stored.state != "in-queue" || stored.rank == "" || stored.seat != "" {
		t.Errorf("state %q rank %q seat %q, want in-queue, keyed, no seat", stored.state, stored.rank, stored.seat)
	}
	if got := column(t, s, `SELECT text FROM item_criteria WHERE item_id = 'item-read-api' AND criterion_id = 'c1'`); got != "The route answers with the payload, and nothing else." {
		t.Errorf("c1 text %q", got)
	}
	if n := count(t, s, `SELECT count(*) FROM item_criteria WHERE item_id = 'item-read-api'`); n != 2 {
		t.Errorf("%d criteria, want 2", n)
	}
	if n := count(t, s, `SELECT count(*) FROM item_edges WHERE item_id = 'item-read-api' AND blocked_by = 'item-base'`); n != 1 {
		t.Errorf("the blocked_by edge was not written")
	}
}

// A standalone chore on a roadmap carries its directive indented beneath a bold bullet; the
// bullet's bold lead is the title, not the section heading.
func TestSyncTakesTheTitleFromAnEnclosingRoadmapBullet(t *testing.T) {
	content := `# Roadmap

## Chores

- **Retire the old checker** — it reads a deleted directory.
  <!-- tillr: item id="item-retire-checker" paths="tools/checker/" -->
  - **c1:** The checker is gone.
`
	s := syncStore(t)
	report := runSync(t, s, "", doc("docs/tillr/roadmap.md", content))
	if len(report.Findings) != 0 || report.CreatedCount != 1 {
		t.Fatalf("findings %v created %v", report.Findings, report.Created)
	}
	if got := column(t, s, `SELECT title FROM items WHERE item_id = 'item-retire-checker'`); got != "Retire the old checker" {
		t.Fatalf("title %q, want the bullet's bold lead", got)
	}
}

// Every finding kind is reported with its file and heading, no item is written from a file
// that carries one, and the other files are still written.
func TestSyncReportsEachFindingAndSkipsOnlyItsFile(t *testing.T) {
	cases := []struct {
		name, rule, content string
	}{
		{"missing id", "missing-id", "### H\n<!-- tillr: item paths=\"a/\" -->\n- **c1:** x\n"},
		{"invalid id", "invalid-id", "### H\n<!-- tillr: item id=\"Item_Bad\" paths=\"a/\" -->\n- **c1:** x\n"},
		{"no criterion", "missing-criterion", "### H\n<!-- tillr: item id=\"item-a\" paths=\"a/\" -->\nprose only\n"},
		{"unknown attribute", "unknown-attribute", "### H\n<!-- tillr: item id=\"item-a\" paths=\"a/\" rank=\"a\" -->\n- **c1:** x\n"},
		{"unknown blocker", "unknown-blocker", "### H\n<!-- tillr: item id=\"item-a\" paths=\"a/\" blocked_by=\"item-nowhere\" -->\n- **c1:** x\n"},
		{"malformed seat", "invalid-seat", "### H\n<!-- tillr: item id=\"item-a\" paths=\"a/\" seat=\"Architect\" -->\n- **c1:** x\n"},
		{"unclosed", "malformed-directive", "### H\n<!-- tillr: item id=\"item-a\" paths=\"a/\"\n- **c1:** x\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := syncStore(t)
			report := runSync(t, s, "",
				doc("docs/bad.md", "# Bad\n\n"+c.content),
				doc("docs/good.md", goodBlock))
			found := false
			for _, f := range report.Findings {
				if f.Rule == c.rule {
					found = true
					if f.File != "docs/bad.md" || f.Heading != "H" || f.Line == 0 {
						t.Errorf("finding not located: %+v", f)
					}
				}
			}
			if !found {
				t.Fatalf("no %s finding: %+v", c.rule, report.Findings)
			}
			if strings.Join(report.SkippedFiles, ",") != "docs/bad.md" {
				t.Errorf("skipped %v, want docs/bad.md only", report.SkippedFiles)
			}
			if n := count(t, s, `SELECT count(*) FROM items`); n != 1 {
				t.Errorf("the store holds %d items, want the good file's one", n)
			}
			if n := count(t, s, `SELECT count(*) FROM items WHERE item_id = 'item-build-thing'`); n != 1 {
				t.Errorf("the good file's item was not written")
			}
		})
	}
}

// One id in two files is two definitions of one item: both files are findings.
func TestSyncRefusesADuplicateIdAcrossFiles(t *testing.T) {
	s := syncStore(t)
	report := runSync(t, s, "",
		doc("docs/a.md", goodBlock),
		doc("docs/b.md", goodBlock),
		doc("docs/c.md", "### Other\n<!-- tillr: item id=\"item-other\" paths=\"x/\" -->\n- **c1:** y\n"))
	dups := 0
	for _, f := range report.Findings {
		if f.Rule == "duplicate-id" {
			dups++
		}
	}
	if dups != 2 {
		t.Fatalf("%d duplicate-id findings, want one per file: %+v", dups, report.Findings)
	}
	if n := count(t, s, `SELECT count(*) FROM items WHERE item_id = 'item-build-thing'`); n != 0 {
		t.Fatalf("a duplicated item was written")
	}
	if n := count(t, s, `SELECT count(*) FROM items WHERE item_id = 'item-other'`); n != 1 {
		t.Fatalf("the unrelated file's item was not written")
	}
}

// A blocker whose only block is in a skipped file does not exist when the edge is written, so
// the blocked file is a finding too; a blocker in a later file of good standing is written
// first.
func TestSyncOrdersBlockersAcrossFilesAndCascadesASkippedBlocker(t *testing.T) {
	s := syncStore(t)
	report := runSync(t, s, "",
		doc("docs/a.md", "### Later\n<!-- tillr: item id=\"item-later\" paths=\"a/\" blocked_by=\"item-first\" -->\n- **c1:** x\n"),
		doc("docs/z.md", "### First\n<!-- tillr: item id=\"item-first\" paths=\"z/\" -->\n- **c1:** x\n"))
	if len(report.Findings) != 0 || report.CreatedCount != 2 {
		t.Fatalf("a cross-file blocker was not ordered: findings %v created %v", report.Findings, report.Created)
	}

	s2 := syncStore(t)
	report = runSync(t, s2, "",
		doc("docs/a.md", "### Later\n<!-- tillr: item id=\"item-later\" paths=\"a/\" blocked_by=\"item-first\" -->\n- **c1:** x\n"),
		doc("docs/z.md", "### First\n<!-- tillr: item id=\"item-first\" paths=\"z/\" rank=\"a\" -->\n- **c1:** x\n"))
	rules := map[string]string{}
	for _, f := range report.Findings {
		rules[f.File] = f.Rule
	}
	if rules["docs/z.md"] != "unknown-attribute" || rules["docs/a.md"] != "blocker-unavailable" {
		t.Fatalf("findings %+v", report.Findings)
	}
	if n := count(t, s2, `SELECT count(*) FROM items`); n != 0 {
		t.Fatalf("%d items written, want none", n)
	}
}

// A refusal Apply makes for one file — two files blocking each other — rolls back that file
// alone under its savepoint and becomes its finding.
func TestSyncRollsBackAFileApplyRefuses(t *testing.T) {
	s := syncStore(t)
	report := runSync(t, s, "",
		doc("docs/a.md", "### X\n<!-- tillr: item id=\"item-x\" paths=\"a/\" blocked_by=\"item-y\" -->\n- **c1:** x\n"),
		doc("docs/b.md", "### Y\n<!-- tillr: item id=\"item-y\" paths=\"b/\" blocked_by=\"item-x\" -->\n- **c1:** y\n"),
		doc("docs/c.md", goodBlock))
	if len(report.Findings) == 0 {
		t.Fatalf("a cross-file cycle produced no finding")
	}
	if n := count(t, s, `SELECT count(*) FROM items`); n != 1 {
		t.Fatalf("the store holds %d items, want only the unrelated file's", n)
	}
	if n := count(t, s, `SELECT count(*) FROM events`); n != 1 {
		t.Fatalf("the store holds %d events, want the one item.add the good file made", n)
	}
}

// A second run over unchanged documents writes nothing and appends no event (D-TC2).
func TestSyncIsIdempotent(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{
		doc("docs/a.md", goodBlock),
		doc("docs/b.md", "### Y\n<!-- tillr: item id=\"item-y\" paths=\"b/\" blocked_by=\"item-build-thing\" seat=\"design\" -->\n- **c1:** y\n- **c2:** z\n"),
	}
	first := runSync(t, s, "", docs...)
	if first.CreatedCount != 2 {
		t.Fatalf("created %v", first.Created)
	}
	events := count(t, s, `SELECT count(*) FROM events`)
	updated := column(t, s, `SELECT group_concat(updated_at) FROM items`)
	second := runSync(t, s, "", docs...)
	if second.CreatedCount != 0 || second.UpdatedCount != 0 || second.UnchangedCount != 2 {
		t.Fatalf("second run: created %v updated %v unchanged %v", second.Created, second.Updated, second.Unchanged)
	}
	if n := count(t, s, `SELECT count(*) FROM events`); n != events {
		t.Fatalf("the second run appended %d event(s)", n-events)
	}
	if got := column(t, s, `SELECT group_concat(updated_at) FROM items`); got != updated {
		t.Fatalf("the second run rewrote rows: %s -> %s", updated, got)
	}
}

// A changed block is a changed item when no criterion has been judged: the definition moves,
// and the state and rank the store learned stay.
func TestSyncUpdatesAnUnjudgedDefinitionAndKeepsStateAndRank(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/a.md", goodBlock))
	exec(t, s, `UPDATE items SET state = 'blocked', rank = 'a' WHERE item_id = 'item-build-thing'`)
	events := count(t, s, `SELECT count(*) FROM events`)

	edited := `### Build the thing well

<!-- tillr: item id="item-build-thing" paths="tools/thing/,tests/thing/" seat="build" -->

- **c1:** The thing builds cleanly.
- **c2:** A test covers it.
`
	report := runSync(t, s, "", doc("docs/a.md", edited))
	if strings.Join(report.Updated, ",") != "item-build-thing" || len(report.Findings) != 0 || len(report.Frozen) != 0 {
		t.Fatalf("updated %v frozen %v findings %v", report.Updated, report.Frozen, report.Findings)
	}
	if n := count(t, s, `SELECT count(*) FROM events WHERE kind = 'item.update'`); n != 1 || count(t, s, `SELECT count(*) FROM events`) != events+1 {
		t.Fatalf("the update was not recorded as one item.update event")
	}
	if got := column(t, s, `SELECT state || '|' || rank || '|' || seat || '|' || title || '|' || allowed_paths FROM items`); got != `blocked|a|build|Build the thing well|` {
		t.Fatalf("stored %s", got)
	}
	if n := count(t, s, `SELECT count(*) FROM item_criteria WHERE item_id = 'item-build-thing'`); n != 2 {
		t.Fatalf("%d criteria, want 2", n)
	}
}

// When a criterion carries a recorded state, a changed block leaves the row alone and is
// reported definition-frozen — a report, not a finding.
func TestSyncLeavesAJudgedDefinitionFrozen(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/a.md", goodBlock))
	exec(t, s, `UPDATE item_criteria SET state = 'done' WHERE item_id = 'item-build-thing'`)
	events := count(t, s, `SELECT count(*) FROM events`)

	report := runSync(t, s, "", doc("docs/a.md", strings.Replace(goodBlock, "The thing builds.", "The thing builds twice.", 1)))
	if len(report.Frozen) != 1 || report.Frozen[0].Rule != "definition-frozen" || report.Frozen[0].ItemID != "item-build-thing" {
		t.Fatalf("frozen %+v", report.Frozen)
	}
	if len(report.Findings) != 0 || report.UpdatedCount != 0 {
		t.Fatalf("findings %v updated %v", report.Findings, report.Updated)
	}
	if got := column(t, s, `SELECT text || '|' || state FROM item_criteria WHERE item_id = 'item-build-thing'`); got != "The thing builds.|done" {
		t.Fatalf("the judged criterion moved: %s", got)
	}
	if n := count(t, s, `SELECT count(*) FROM events`); n != events {
		t.Fatalf("a frozen definition appended an event")
	}
}

// A seat names the role the item asks for; it is part of the definition.
func TestSyncMapsTheSeat(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/a.md", strings.Replace(goodBlock, `paths="tools/thing/"`, `paths="tools/thing/" seat="architect-2"`, 1)))
	if got := column(t, s, `SELECT seat FROM items`); got != "architect-2" {
		t.Fatalf("seat %q, want architect-2", got)
	}
	report := runSync(t, s, "", doc("docs/a.md", goodBlock))
	if report.UpdatedCount != 1 {
		t.Fatalf("dropping the seat did not update the definition: %+v", report)
	}
	if got := column(t, s, `SELECT seat FROM items`); got != "" {
		t.Fatalf("seat %q, want the default (none)", got)
	}
}

// A document a roadmap's Icebox or Exploration section links to is parked: its new items are
// not-started. A link from any other section parks nothing.
func TestSyncParksDocumentsTheRoadmapIceboxes(t *testing.T) {
	roadmap := `# Roadmap

## Active

- [Live plan](design/live.md)

## Exploration

- [Idea](design/idea.md#section)

## Icebox 🧊

- **The bar** — [plan](design/bar.md), iceboxed.

` + "```\n## Active\n[fenced](design/live2.md)\n```\n"
	s := syncStore(t)
	block := func(id string) string {
		return "### T\n<!-- tillr: item id=\"" + id + "\" paths=\"x/\" -->\n- **c1:** y\n"
	}
	report := runSync(t, s, "",
		doc("docs/roadmap.md", roadmap),
		doc("docs/design/live.md", block("item-live")),
		doc("docs/design/idea.md", block("item-idea")),
		doc("docs/design/bar.md", block("item-bar")))
	if len(report.Findings) != 0 {
		t.Fatalf("findings %v", report.Findings)
	}
	for id, want := range map[string]string{"item-live": "in-queue", "item-idea": "not-started", "item-bar": "not-started"} {
		if got := column(t, s, `SELECT state FROM items WHERE item_id = ?`, id); got != want {
			t.Errorf("%s is %s, want %s", id, got, want)
		}
	}
}

// The two residue counters: open store items no block declares, and blocks whose item the
// store holds as landed.
func TestSyncCountsUnsharedOpenAndStaleDocBlocks(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "",
		doc("docs/a.md", goodBlock),
		doc("docs/b.md", "### P\n<!-- tillr: item id=\"item-private\" paths=\"p/\" -->\n- **c1:** p\n"),
		doc("docs/c.md", "### D\n<!-- tillr: item id=\"item-done\" paths=\"d/\" -->\n- **c1:** d\n"),
		doc("docs/e.md", "### E\n<!-- tillr: item id=\"item-old-done\" paths=\"e/\" -->\n- **c1:** e\n"))
	exec(t, s, `UPDATE items SET state = 'landed' WHERE item_id IN ('item-done', 'item-old-done')`)
	exec(t, s, `UPDATE items SET updated_at = ? WHERE item_id = 'item-private'`, syncAt.Add(-3*time.Hour).UnixMilli())

	// item-private loses its block and stays open; item-old-done loses its block and is
	// landed, so it is neither residue nor stale; item-done keeps its block while landed.
	report := runSync(t, s, "",
		doc("docs/a.md", goodBlock),
		doc("docs/c.md", "### D\n<!-- tillr: item id=\"item-done\" paths=\"d/\" -->\n- **c1:** d\n"))
	if report.UnsharedOpen != 1 || strings.Join(report.UnsharedOpenIDs, ",") != "item-private" {
		t.Fatalf("unshared_open %d %v", report.UnsharedOpen, report.UnsharedOpenIDs)
	}
	if report.StaleDocBlocks != 1 || strings.Join(report.StaleDocBlockIDs, ",") != "item-done" {
		t.Fatalf("stale_doc_blocks %d %v", report.StaleDocBlocks, report.StaleDocBlockIDs)
	}
	if got := column(t, s, `SELECT state FROM items WHERE item_id = 'item-done'`); got != "landed" {
		t.Fatalf("sync moved a landed item to %s", got)
	}
	text := item.SyncText(report, syncAt)
	if !strings.Contains(text, "unshared_open: 1 (oldest 3h0m0s)") || !strings.Contains(text, "stale_doc_blocks: 1") {
		t.Fatalf("the text report does not carry the counters:\n%s", text)
	}
}

// A dry run reports what a real run would write and writes nothing.
func TestSyncDryRunWritesNothing(t *testing.T) {
	s := syncStore(t)
	var report item.SyncReport
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		report, err = item.Sync(context.Background(), r, nil, item.SyncInput{Documents: []item.SyncDocument{doc("docs/a.md", goodBlock)}, At: syncAt})
		return err
	}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !report.DryRun || report.CreatedCount != 1 {
		t.Fatalf("dry run report %+v", report)
	}
	if n := count(t, s, `SELECT count(*) FROM items`); n != 0 {
		t.Fatalf("a dry run wrote %d items", n)
	}
}

// Zero blocks is a valid queue: nothing written, nothing found.
func TestSyncOfNoBlocksIsAValidResult(t *testing.T) {
	s := syncStore(t)
	report := runSync(t, s, "", doc("docs/readme.md", "# Nothing here\n"))
	if report.Blocks != 0 || len(report.Findings) != 0 || len(report.Warnings) != 0 {
		t.Fatalf("report %+v", report)
	}
}

// A path that does not exist yet is a warning — the slice may create it — never a finding.
func TestSyncIgnoresLegacyPathValues(t *testing.T) {
	root := scratch(t)
	if err := os.MkdirAll(filepath.Join(root, "tools", "thing"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := syncStore(t)
	report := runSync(t, s, root, doc("docs/a.md", strings.Replace(goodBlock, `paths="tools/thing/"`, `paths="tools/*,.,tools/new/"`, 1)))
	if len(report.Findings) != 0 || report.CreatedCount != 1 {
		t.Fatalf("findings %v created %v", report.Findings, report.Created)
	}
	var pathWarnings []item.SyncNote
	for _, warning := range report.Warnings {
		if warning.Rule == "path-missing" {
			pathWarnings = append(pathWarnings, warning)
		}
	}
	if len(pathWarnings) != 0 {
		t.Fatalf("path warnings %+v", pathWarnings)
	}
}

// The repository's own tracked documents sync cleanly. The assertions are structural only —
// no item count and no item id — because blocks come and go as work lands.
func TestSyncOverThisRepositorysDocuments(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	docs, err := item.ReadSyncDocuments(root)
	if err != nil {
		t.Fatalf("reading the tracked documents: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("no tracked document under docs/ was read")
	}
	s := syncStore(t)
	report := runSync(t, s, root, docs...)
	if len(report.Findings) != 0 {
		t.Fatalf("the repository's documents carry findings:\n%s", item.SyncText(report, syncAt))
	}
	if n := count(t, s, `SELECT count(*) FROM items`); n != report.CreatedCount {
		t.Fatalf("the store holds %d items and the report created %d", n, report.CreatedCount)
	}
	if n := count(t, s, `SELECT count(*) FROM items WHERE allowed_paths NOT IN ('', '[]')`); n != 0 {
		t.Fatalf("%d synced item(s) still carry a fence", n)
	}
	if n := count(t, s, `SELECT count(*) FROM items i WHERE NOT EXISTS (SELECT 1 FROM item_criteria c WHERE c.item_id = i.item_id)`); n != 0 {
		t.Fatalf("%d synced item(s) carry no criterion", n)
	}
	seen := map[string]bool{}
	for _, id := range report.Created {
		if seen[id] {
			t.Fatalf("item %s was created twice", id)
		}
		seen[id] = true
	}
}

// A withdrawal (a reverted landing's, item.WithdrawVerdicts) clears the states that froze
// the definition, records what it cleared, and the changed block then syncs: the item is
// as amendable as before it was judged. A second withdrawal records nothing.
func TestSyncUpdatesADefinitionWhoseVerdictsWereWithdrawn(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/a.md", goodBlock))
	exec(t, s, `UPDATE item_criteria SET state = 'done' WHERE item_id = 'item-build-thing'`)
	changed := strings.Replace(goodBlock, "Build the thing", "Build the replacement", 1)
	if report := runSync(t, s, "", doc("docs/a.md", changed)); len(report.Frozen) != 1 {
		t.Fatalf("a judged definition is not frozen: %+v", report.Frozen)
	}
	withdraw := func() []item.WithdrawnCriterion {
		var out []item.WithdrawnCriterion
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			var err error
			out, err = item.WithdrawVerdicts(context.Background(), tx, "item-build-thing", "landing abc reverted", "tillr-drain", time.Unix(100, 0))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := withdraw(); len(got) == 0 || got[0].CriterionID != "c1" || got[0].State != "done" {
		t.Fatalf("withdrawn %+v", got)
	}
	if got := column(t, s, `SELECT group_concat(state, '|') FROM item_criteria WHERE item_id = 'item-build-thing'`); strings.Trim(got, "|") != "" {
		t.Fatalf("states after the withdrawal: %q", got)
	}
	if n := count(t, s, `SELECT count(*) FROM events WHERE kind = ? AND json_extract(payload, '$.reason') = 'landing abc reverted' AND json_extract(payload, '$.by') = 'tillr-drain'`, item.VerdictsWithdrawnKind); n != 1 {
		t.Fatalf("%d withdrawal events, want 1", n)
	}
	if got := withdraw(); len(got) != 0 {
		t.Fatalf("a second withdrawal cleared %+v", got)
	}
	if n := count(t, s, `SELECT count(*) FROM events WHERE kind = ?`, item.VerdictsWithdrawnKind); n != 1 {
		t.Fatalf("a second withdrawal appended an event: %d", n)
	}
	report := runSync(t, s, "", doc("docs/a.md", changed))
	if len(report.Frozen) != 0 || strings.Join(report.Updated, ",") != "item-build-thing" {
		t.Fatalf("after the withdrawal: updated %v frozen %+v", report.Updated, report.Frozen)
	}
	if got := column(t, s, `SELECT title FROM items WHERE item_id = 'item-build-thing'`); got != "Build the replacement" {
		t.Fatalf("paths %s", got)
	}
}

// A verdict a withdrawal named no longer satisfies --require-verdicts: only a verdict
// recorded after the withdrawal does.
func TestRequireVerdictsIgnoresAWithdrawnVerdict(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "", doc("docs/a.md", goodBlock))
	ctx := context.Background()
	verdict := func() {
		body, _ := json.Marshal(map[string]any{
			"item_id": "item-build-thing", "criterion_id": "c1", "criterion_hash": item.CriterionHash("The thing builds."),
			"verdict": "done", "claim_class": "verification-only", "reason": "landed", "evidence": []string{"gate-verdict-1"}, "actor_kind": "mechanical",
		})
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := item.RecordVerdict(ctx, tx, record.Envelope{Kind: item.VerdictRecordKind, CreatedAt: syncAt, Payload: body})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	move := func() error {
		return s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := item.SetStateWithReceipt(ctx, tx, "item-build-thing", "in-queue", syncAt, item.MoveReceipt{RequireVerdicts: true})
			return err
		})
	}
	verdict()
	if err := move(); err != nil {
		t.Fatalf("a judged item was refused: %v", err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.WithdrawVerdicts(ctx, tx, "item-build-thing", "landing abc reverted", "tillr-drain", syncAt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := move(); err == nil || !strings.Contains(err.Error(), "has no recorded verdict") {
		t.Fatalf("a withdrawn verdict still counts: %v", err)
	}
	verdict()
	if err := move(); err != nil {
		t.Fatalf("a verdict after the withdrawal was refused: %v", err)
	}
}
