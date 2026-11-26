package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func scopeBlock(id string) string { return strings.ReplaceAll(syncGood, "item-good", "item-"+id) }
func scopeRepo(t *testing.T) string {
	return syncRepo(t, map[string]string{
		"docs/a/roadmap.md": "# A\n[One](one.md)\n[Two](two.md#two)\n```md\n[Ignored](../unlinked.md)\n```\n",
		"docs/a/one.md":     scopeBlock("a-one") + "[Indirect](../unlinked.md)\n",
		"docs/a/two.md":     scopeBlock("a-two"),
		"docs/b/roadmap.md": "# B\n[One](one.md)\n[Two](two.md)\n",
		"docs/b/one.md":     scopeBlock("b-one"), "docs/b/two.md": scopeBlock("b-two"),
		"docs/unlinked.md": scopeBlock("unlinked"), "outside.md": "# Outside\n",
	})
}
func scopePayload(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var record struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return record.Payload
}
func scopeOrder(t *testing.T, db string) string {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var result string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items ORDER BY rank,item_id)`).Scan(&result)
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestSyncRoadmapScopeReadsOnlyDirectTrackedDocuments(t *testing.T) {
	repo := scopeRepo(t)
	db := filepath.Join(scratch(t, "scope"), "state.db")
	code, out := run(t, "sync", "--store", db, "--root", repo, "--roadmap", "docs/a/roadmap.md", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := scopeOrder(t, db); got != "item-a-one,item-a-two" {
		t.Fatal(got)
	}
	p := scopePayload(t, out)
	if !reflect.DeepEqual(p["scope"], []any{"docs/a/roadmap.md"}) || p["documents"] != float64(3) {
		t.Fatalf("scope: %s", out)
	}
	for _, field := range []string{"unshared_open", "stale_doc_blocks"} {
		v, ok := p[field]
		if !ok || v != nil {
			t.Fatalf("%s must be explicitly not computed: %s", field, out)
		}
	}
	if p["skipped_count"] != float64(3) {
		t.Fatalf("skipped_count: %s", out)
	}
	code, out = run(t, "sync", "--store", db, "--root", repo, "--roadmap", "docs/a/roadmap.md")
	if code != 0 || !strings.Contains(string(out), ", skipped 3\n") || !strings.Contains(string(out), "skipped: docs/b/one.md item-b-one\n") || !strings.Contains(string(out), "not computed") || !strings.Contains(string(out), "sync of every document") {
		t.Fatalf("text exit %d: %s", code, out)
	}
}
func TestSyncRoadmapScopeInterleavesFlagOrder(t *testing.T) {
	repo := scopeRepo(t)
	for _, roads := range [][]string{{"docs/a/roadmap.md", "docs/b/roadmap.md"}, {"docs/b/roadmap.md", "docs/a/roadmap.md"}} {
		db := filepath.Join(scratch(t, "scope-order"), "state.db")
		args := []string{"sync", "--store", db, "--root", repo, "--json"}
		for _, road := range roads {
			args = append(args, "--roadmap", road)
		}
		code, out := run(t, args...)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		first, second := "a", "b"
		if strings.Contains(roads[0], "/b/") {
			first, second = second, first
		}
		want := "item-" + first + "-one,item-" + second + "-one,item-" + first + "-two,item-" + second + "-two"
		if got := scopeOrder(t, db); got != want {
			t.Fatalf("%v: %s want %s", roads, got, want)
		}
	}
}
func TestSyncRoadmapScopePreservesUnreadItemsAndWholeObservation(t *testing.T) {
	repo := scopeRepo(t)
	db := filepath.Join(scratch(t, "scope-preserve"), "state.db")
	code, out := run(t, "sync", "--store", db, "--root", repo, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if scopePayload(t, out)["scope"] != "all-documents" {
		t.Fatalf("whole scope: %s", out)
	}
	s, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Legacy unkeyed rows must also stay unkeyed when their blocks are outside the scope.
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET rank='',state='blocked' WHERE item_id='item-b-one'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var v string
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT title||'|'||state||'|'||rank||'|'||updated_at FROM items WHERE item_id='item-b-one'`).Scan(&v)
		}); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	if err := os.WriteFile(filepath.Join(repo, "docs/b/one.md"), []byte(strings.ReplaceAll(scopeBlock("b-one"), "Good", "Changed")), 0600); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		var v string
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT synced_at||unshared_open_ids||stale_doc_block_ids FROM item_sync_residue`).Scan(&v)
		}); err != nil {
			t.Fatal(err)
		}
		return v
	}
	observation := fingerprint()
	code, out = run(t, "sync", "--store", db, "--root", repo, "--roadmap", "docs/a/roadmap.md", "--now", "2030-01-01T00:00:00Z", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if after := snapshot(); after != before {
		t.Fatalf("unread item changed: %s -> %s", before, after)
	}
	if fingerprint() != observation {
		t.Fatal("partial sync replaced whole observation")
	}
}
func TestSyncRoadmapScopeRefusesInvalidPathsWithoutWrites(t *testing.T) {
	repo := scopeRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "untracked-outside.md"), []byte("# Untracked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, road := range []string{"docs/draft.md", "docs/missing.md", "untracked-outside.md", "../docs/a/roadmap.md", "docs/a", ""} {
		db := filepath.Join(scratch(t, "scope-invalid"), "state.db")
		cmd := exec.Command(bin, "sync", "--store", db, "--root", repo, "--roadmap", "docs/a/roadmap.md", "--roadmap", road)
		cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
		out, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), road) {
			t.Fatalf("path %q error %v: %s", road, err, out)
		}
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Fatalf("invalid path %q created store", road)
		}
	}
}

func TestSyncSettingsRoadmapsLimitsImportsAndReportsSkippedBlocks(t *testing.T) {
	repo := syncRepo(t, map[string]string{
		"docs/tillr/roadmap.md": "# Tillr Roadmap\n" +
			"Passing mention of [parked](../design/tillr-parked.md#q1) and [pi](../pi/roadmap.md).\n" +
			"- [Active design](../design/tillr-active.md)\n" +
			scopeBlock("tillr-inline") +
			"## Icebox\n" +
			"- [Parked design](../design/tillr-parked.md)\n" +
			scopeBlock("tillr-icebox-inline") +
			"## Exploration\n" +
			"### Draft without id\n<!-- tillr: item paths=\"docs/\" -->\n- **c1:** draft\n" +
			"## What this page does not cover\n" +
			"- [Excluded design](../design/tillr-excluded.md)\n",
		"docs/design/tillr-active.md":   scopeBlock("tillr-active"),
		"docs/design/tillr-parked.md":   scopeBlock("tillr-parked"),
		"docs/design/tillr-excluded.md": scopeBlock("tillr-excluded"),
		"docs/pi/roadmap.md": "# Pi Roadmap\n- [Pi design](../design/pi-only.md)\n" +
			"## What this file does not cover\n- [Tillr](../tillr/roadmap.md)\n",
		"docs/design/pi-only.md": scopeBlock("pi-only"),
		"docs/unlinked.md":       "### Draft\n<!-- tillr: item id=\"item-unlinked\" paths=\"docs/\" unknown=\"x\" -->\n- **c1:** draft\n",
	})
	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[sync]\nroadmaps = [\"docs/tillr/roadmap.md\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	wantSkipped := []any{
		map[string]any{"file": "docs/design/pi-only.md", "item_id": "item-pi-only"},
		map[string]any{"file": "docs/design/tillr-excluded.md", "item_id": "item-tillr-excluded"},
		map[string]any{"file": "docs/design/tillr-parked.md", "item_id": "item-tillr-parked"},
		map[string]any{"file": "docs/tillr/roadmap.md", "item_id": "item-tillr-icebox-inline"},
		map[string]any{"file": "docs/tillr/roadmap.md", "item_id": ""},
		map[string]any{"file": "docs/unlinked.md", "item_id": "item-unlinked"},
	}

	for _, extra := range [][]string{nil, {"--roadmap", "docs/tillr/roadmap.md"}} {
		db := filepath.Join(scratch(t, "settings-tillr"), "state.db")
		jsonArgs := append([]string{"sync", "--store", db, "--root", repo, "--json"}, extra...)
		code, out := run(t, jsonArgs...)
		if code != 0 {
			t.Fatalf("%v exit %d: %s", extra, code, out)
		}
		if got := scopeOrder(t, db); got != "item-tillr-active,item-tillr-inline" {
			t.Fatalf("%v imported %q, want only tillr items", extra, got)
		}
		p := scopePayload(t, out)
		if !reflect.DeepEqual(p["scope"], []any{"docs/tillr/roadmap.md"}) || p["documents"] != float64(2) || p["blocks"] != float64(2) {
			t.Fatalf("%v payload: %s", extra, out)
		}
		if p["skipped_count"] != float64(len(wantSkipped)) || !reflect.DeepEqual(p["skipped_blocks"], wantSkipped) {
			t.Fatalf("%v skipped_blocks = %v (count %v), want %v", extra, p["skipped_blocks"], p["skipped_count"], wantSkipped)
		}

		textArgs := append([]string{"sync", "--store", db, "--root", repo}, extra...)
		code, textOut := run(t, textArgs...)
		if code != 0 {
			t.Fatalf("%v text exit %d: %s", extra, code, textOut)
		}
		text := string(textOut)
		for _, wantLine := range []string{
			"sync: 2 block(s) in 2 tracked document(s): created 0, unchanged 2, updated 0, frozen 0, skipped 6\n",
			"skipped: docs/design/pi-only.md item-pi-only\n",
			"skipped: docs/tillr/roadmap.md (missing id)\n",
		} {
			if !strings.Contains(text, wantLine) {
				t.Fatalf("%v missing %q in:\n%s", extra, wantLine, text)
			}
		}
	}

	// Explicit --roadmap overrides .tillr.toml's [sync] roadmaps.
	db := filepath.Join(scratch(t, "settings-override"), "state.db")
	code, out := run(t, "sync", "--store", db, "--root", repo, "--roadmap", "docs/pi/roadmap.md", "--json")
	if code != 0 {
		t.Fatalf("override exit %d: %s", code, out)
	}
	if got := scopeOrder(t, db); got != "item-pi-only" {
		t.Fatalf("override imported %q, want item-pi-only", got)
	}
	p := scopePayload(t, out)
	if !reflect.DeepEqual(p["scope"], []any{"docs/pi/roadmap.md"}) {
		t.Fatalf("override scope: %s", out)
	}

	// Empty [sync] roadmaps = [] keeps unscoped summary text byte-identical when skipped_count == 0.
	cleanRepo := scopeRepo(t)
	if err := os.WriteFile(filepath.Join(cleanRepo, ".tillr.toml"), []byte("[sync]\nroadmaps = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	wholeDB := filepath.Join(scratch(t, "settings-empty"), "state.db")
	code, wholeText := run(t, "sync", "--store", wholeDB, "--root", cleanRepo)
	if code != 0 || strings.Contains(string(wholeText), "skipped") {
		t.Fatalf("whole sync exit %d: %s", code, wholeText)
	}
}

func TestSyncSettingsRoadmapsRefusesInvalidConfigWithoutStoreWrites(t *testing.T) {
	repo := scopeRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "untracked-outside.md"), []byte("# Untracked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"[sync\n",
		"[sync]\nroadmaps = \"docs/a/roadmap.md\"\n",
		"[sync]\nroadmaps = [\"docs/missing.md\"]\n",
		"[sync]\nroadmaps = [\"untracked-outside.md\"]\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		db := filepath.Join(scratch(t, "settings-invalid"), "state.db")
		code, out := run(t, "sync", "--store", db, "--root", repo)
		if code != 2 {
			t.Fatalf("config %q exited %d, want 2: %s", raw, code, out)
		}
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Fatalf("invalid config %q created store", raw)
		}
	}
}

func TestSyncRoadmapOutsideDocs(t *testing.T) {
	repo := syncRepo(t, map[string]string{
		"tools/alpha/roadmap.md": "# Alpha Roadmap\n" +
			"- [Design](../../docs/design/alpha.md)\n" +
			"- [Local notes](notes.md)\n" +
			scopeBlock("alpha-inline"),
		"tools/alpha/notes.md":    scopeBlock("alpha-notes"),
		"tools/beta/roadmap.md":   scopeBlock("beta-inline"),
		"docs/design/alpha.md":    scopeBlock("alpha-design"),
		"docs/design/unlinked.md": scopeBlock("unlinked"),
	})
	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[sync]\nroadmaps = [\"tools/alpha/roadmap.md\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	wantSkipped := []any{
		map[string]any{"file": "docs/design/unlinked.md", "item_id": "item-unlinked"},
	}

	for _, extra := range [][]string{nil, {"--roadmap", "tools/alpha/roadmap.md"}} {
		db := filepath.Join(scratch(t, "outside-docs"), "state.db")
		args := append([]string{"sync", "--store", db, "--root", repo, "--json"}, extra...)
		code, out := run(t, args...)
		if code != 0 {
			t.Fatalf("%v exit %d: %s", extra, code, out)
		}
		if got := scopeOrder(t, db); got != "item-alpha-design,item-alpha-inline" {
			t.Fatalf("%v imported %q, want item-alpha-design,item-alpha-inline", extra, got)
		}
		p := scopePayload(t, out)
		if !reflect.DeepEqual(p["scope"], []any{"tools/alpha/roadmap.md"}) || p["documents"] != float64(2) || p["blocks"] != float64(2) {
			t.Fatalf("%v payload: %s", extra, out)
		}
		if p["skipped_count"] != float64(1) || !reflect.DeepEqual(p["skipped_blocks"], wantSkipped) {
			t.Fatalf("%v skipped_blocks = %v (count %v), want %v (must not scan un-named files outside docs/)", extra, p["skipped_blocks"], p["skipped_count"], wantSkipped)
		}
	}

	// Unscoped sync (roadmaps = []) does not scan outside docs/.
	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[sync]\nroadmaps = []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	wholeDB := filepath.Join(scratch(t, "outside-unscoped"), "state.db")
	code, out := run(t, "sync", "--store", wholeDB, "--root", repo, "--json")
	if code != 0 {
		t.Fatalf("unscoped exit %d: %s", code, out)
	}
	if got := scopeOrder(t, wholeDB); got != "item-alpha-design,item-unlinked" {
		t.Fatalf("unscoped imported %q, want only docs/ items", got)
	}
}
