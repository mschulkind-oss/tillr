package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestAfterEdgesDepAddRemoveReplaceAndCycle(t *testing.T) {
	db := filepath.Join(scratch(t, "after-dep"), "board.sqlite3")
	for _, id := range []string{"dep-a", "dep-b", "dep-c", "target"} {
		it := base(id, "item "+id)
		if code, out := apply(t, db, envelope(t, "item.record", "setup-"+id, it)); code != 0 {
			t.Fatalf("apply %s: %d: %s", id, code, out)
		}
	}

	// Add dep-a as blocked_by and dep-b as after.
	code, out := run(t, "dep", "--store", db, "--item", "target",
		"--add", "dep-a", "--add-after", "dep-b", "--now", "2026-11-26T10:00:00Z")
	if code != 0 {
		t.Fatalf("dep add + add-after: %d: %s", code, out)
	}
	got := decode(t, out)
	if !slices.Equal(got.Payload.BlockedBy, []string{"dep-a"}) {
		t.Fatalf("blocked_by = %v, want [dep-a]", got.Payload.BlockedBy)
	}
	if !slices.Equal(got.Payload.After, []string{"dep-b"}) {
		t.Fatalf("after = %v, want [dep-b]", got.Payload.After)
	}

	// Verify edge kinds in SQLite.
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	var kinds map[string]string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT blocked_by, kind FROM item_edges WHERE item_id = 'target' ORDER BY blocked_by`)
		if err != nil {
			return err
		}
		defer rows.Close()
		kinds = map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			kinds[k] = v
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if kinds["dep-a"] != "blocked_by" || kinds["dep-b"] != "after" {
		t.Fatalf("stored edge kinds = %v, want dep-a=blocked_by dep-b=after", kinds)
	}

	// Replace kind: move dep-a to after and dep-b to blocked_by.
	code, out = run(t, "dep", "--store", db, "--item", "target",
		"--add-after", "dep-a", "--add", "dep-b", "--now", "2026-11-26T10:01:00Z")
	if code != 0 {
		t.Fatalf("dep replace kinds: %d: %s", code, out)
	}
	got = decode(t, out)
	if !slices.Equal(got.Payload.BlockedBy, []string{"dep-b"}) || !slices.Equal(got.Payload.After, []string{"dep-a"}) {
		t.Fatalf("after kind swap: blocked_by=%v after=%v", got.Payload.BlockedBy, got.Payload.After)
	}

	// --remove removes edges of either kind.
	code, out = run(t, "dep", "--store", db, "--item", "target",
		"--remove", "dep-a", "--remove", "dep-b", "--now", "2026-11-26T10:02:00Z")
	if code != 0 {
		t.Fatalf("dep remove both kinds: %d: %s", code, out)
	}
	got = decode(t, out)
	if len(got.Payload.BlockedBy) != 0 || len(got.Payload.After) != 0 {
		t.Fatalf("after remove both: blocked_by=%v after=%v", got.Payload.BlockedBy, got.Payload.After)
	}

	// Mixed-kind cycle detection: target --add-after dep-c, dep-c --add target -> edge-cycle.
	if code, out := run(t, "dep", "--store", db, "--item", "target", "--add-after", "dep-c"); code != 0 {
		t.Fatalf("setup target->dep-c: %d: %s", code, out)
	}
	for _, tc := range []struct {
		name string
		args []string
		rule string
	}{
		{"overlap-in-one-call", []string{"dep", "--store", db, "--item", "target", "--add", "dep-a", "--add-after", "dep-a"}, "edge-overlap"},
		{"self-after", []string{"dep", "--store", db, "--item", "target", "--add-after", "target"}, "edge-self"},
		{"missing-after", []string{"dep", "--store", db, "--item", "target", "--add-after", "no-such-item"}, "edge-target-missing"},
		{"mixed-cycle-blocked-by", []string{"dep", "--store", db, "--item", "dep-c", "--add", "target"}, "edge-cycle"},
		{"mixed-cycle-after", []string{"dep", "--store", db, "--item", "dep-c", "--add-after", "target"}, "edge-cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := run(t, tc.args...)
			if code != 1 {
				t.Fatalf("exit %d, want 1: %s", code, out)
			}
			rej := decode(t, out)
			if rej.Kind != "item.rejection" || rej.Payload.Rule != tc.rule {
				t.Fatalf("got %s (rule %q), want rule %q", out, rej.Payload.Rule, tc.rule)
			}
		})
	}
}

func TestAfterEdgesRequestFlagAndInputHash(t *testing.T) {
	path, s := placeFixture(t, "a", "b")
	root := scratch(t, "request-after-root")
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "tillr", "workflows", "planned-build@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config/tillr/workflows/planned-build@v1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	const words = "Run after writer 0 and 1 finish, however they finish.\n"
	code, out := requestRun(t, words,
		"--store", path, "--root", root,
		"--title", "Order after 0 and 1",
		"--after", "0",
		"--after-item", "1",
		"--after-item", "0",
		"--after-item", "1",
		"--now", "2026-11-26T10:00:00Z",
	)
	if code != 0 {
		t.Fatalf("request with --after-item: %d: %s", code, out)
	}
	if !strings.Contains(out, `"after":["0","1"]`) {
		t.Fatalf("request output missing normalized after: %s", out)
	}

	var env struct {
		Payload struct {
			ItemID string   `json:"item_id"`
			Place  int      `json:"place"`
			After  []string `json:"after"`
		} `json:"payload"`
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "{") {
			if err := json.Unmarshal([]byte(line), &env); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if env.Payload.ItemID == "" || !slices.Equal(env.Payload.After, []string{"0", "1"}) {
		t.Fatalf("unexpected request payload: %+v", env.Payload)
	}
	// --after 0 placed it after item 0 (place 2).
	if env.Payload.Place != 2 {
		t.Fatalf("place = %d, want 2 (placed after 0)", env.Payload.Place)
	}

	// Verify stored edge kinds.
	var stored []string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT blocked_by || ':' || kind FROM item_edges WHERE item_id = ? ORDER BY blocked_by`, env.Payload.ItemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e string
			if err := rows.Scan(&e); err != nil {
				return err
			}
			stored = append(stored, e)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored, []string{"0:after", "1:after"}) {
		t.Fatalf("stored edges = %v, want [0:after 1:after]", stored)
	}

	// Idempotent refile with reversed --after-item order succeeds.
	code, again := requestRun(t, words,
		"--store", path, "--root", root,
		"--title", "Order after 0 and 1",
		"--after-item", "0",
		"--after-item", "1",
		"--now", "2026-11-26T10:01:00Z",
	)
	if code != 0 || !strings.Contains(again, env.Payload.ItemID) {
		t.Fatalf("idempotent refile failed: %d: %s", code, again)
	}

	// Refiling with different --after-item is refused by requestInputHash.
	code, diffOut := requestRun(t, words,
		"--store", path, "--root", root,
		"--title", "Order after 0 and 1",
		"--after-item", "0",
		"--now", "2026-11-26T10:02:00Z",
	)
	if code == 0 || !strings.Contains(diffOut, "request-differs") {
		t.Fatalf("refile with different after-item: %d: %s", code, diffOut)
	}

	// Refiling with no --after-item is also refused by requestInputHash.
	code, noAfterOut := requestRun(t, words,
		"--store", path, "--root", root,
		"--title", "Order after 0 and 1",
		"--now", "2026-11-26T10:03:00Z",
	)
	if code == 0 || !strings.Contains(noAfterOut, "request-differs") {
		t.Fatalf("refile without after-item: %d: %s", code, noAfterOut)
	}

	// Overlap between --blocked-by and --after-item is refused.
	code, overlapOut := requestRun(t, "Overlap test words.\n",
		"--store", path, "--root", root,
		"--title", "Overlap",
		"--blocked-by", "0",
		"--after-item", "0",
		"--now", "2026-11-26T10:04:00Z",
	)
	if code != 1 || !strings.Contains(overlapOut, "edge-overlap") {
		t.Fatalf("overlap refusal: %d: %s", code, overlapOut)
	}
}

func TestAfterEdgesApplyOverlapAndShowListRoundTrip(t *testing.T) {
	db := filepath.Join(scratch(t, "after-roundtrip"), "board.sqlite3")
	first := base("first", "first item")
	if code, out := apply(t, db, envelope(t, "item.record", "s1", first)); code != 0 {
		t.Fatalf("apply first: %d: %s", code, out)
	}
	second := base("second", "second item")
	second.After = []string{"first"}
	if code, out := apply(t, db, envelope(t, "item.record", "s2", second)); code != 0 {
		t.Fatalf("apply second: %d: %s", code, out)
	}

	// Overlap in apply is refused.
	overlap := base("third", "third item")
	overlap.BlockedBy = []string{"first"}
	overlap.After = []string{"first"}
	code, out := apply(t, db, envelope(t, "item.record", "s3", overlap))
	if code != 1 || decode(t, out).Payload.Rule != "edge-overlap" {
		t.Fatalf("apply overlap: %d: %s", code, out)
	}

	// show preserves after and feeds back into apply cleanly.
	code, shown := run(t, "show", "--store", db, "--item", "second")
	if code != 0 {
		t.Fatalf("show second: %d: %s", code, shown)
	}
	if !strings.Contains(string(shown), `"after":["first"]`) {
		t.Fatalf("show missing after: %s", shown)
	}
	if code, out := apply(t, db, string(shown)); code != 0 {
		t.Fatalf("show->apply round-trip failed: %d: %s", code, out)
	}

	// list surfaces after on second and omits it on first.
	items := listItems(t, db)
	want := []listedItem{
		{"first", "first item", "not-started", []string{}, nil, "2026-11-10T00:00:00Z"},
		{"second", "second item", "not-started", []string{}, []string{"first"}, "2026-11-10T00:00:00Z"},
	}
	if !slices.EqualFunc(items, want, func(a, b listedItem) bool {
		return a.ItemID == b.ItemID && a.Title == b.Title && a.State == b.State &&
			slices.Equal(a.BlockedBy, b.BlockedBy) && slices.Equal(a.After, b.After) && a.UpdatedAt == b.UpdatedAt
	}) {
		t.Fatalf("list = %+v, want %+v", items, want)
	}
}

func TestAfterEdgesLegacyStoreWithoutKind(t *testing.T) {
	db := filepath.Join(scratch(t, "after-legacy"), "legacy.sqlite3")
	it42 := base("item_042", "blocker")
	if code, out := apply(t, db, envelope(t, "item.record", "s1", it42)); code != 0 {
		t.Fatalf("apply item_042: %d: %s", code, out)
	}
	it43 := base("item_043", "dependent")
	it43.BlockedBy = []string{"item_042"}
	if code, out := apply(t, db, envelope(t, "item.record", "s2", it43)); code != 0 {
		t.Fatalf("apply item_043: %d: %s", code, out)
	}

	// Simulate a legacy store whose item_edges table predates the kind column.
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`ALTER TABLE item_edges DROP COLUMN kind`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	// Read-only show and list must succeed without migrating item_edges.
	code, out := run(t, "show", "--store", db, "--item", "item_043")
	if code != 0 {
		t.Fatalf("show on legacy store: %d: %s", code, out)
	}
	got := decode(t, out)
	if !slices.Equal(got.Payload.BlockedBy, []string{"item_042"}) || len(got.Payload.After) != 0 {
		t.Fatalf("legacy show edges: blocked_by=%v after=%v", got.Payload.BlockedBy, got.Payload.After)
	}

	items := listItems(t, db)
	if len(items) != 2 || !slices.Equal(items[1].BlockedBy, []string{"item_042"}) || len(items[1].After) != 0 {
		t.Fatalf("legacy list edges: %+v", items)
	}

	// Verify the legacy store was not migrated (kind column still absent).
	conn, err = sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var hasKind bool
	if err := conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('item_edges') WHERE name = 'kind')`).Scan(&hasKind); err != nil {
		t.Fatal(err)
	}
	if hasKind {
		t.Fatal("read-only show/list migrated legacy item_edges table")
	}
}
