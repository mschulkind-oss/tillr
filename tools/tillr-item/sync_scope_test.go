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
	code, out = run(t, "sync", "--store", db, "--root", repo, "--roadmap", "docs/a/roadmap.md")
	if code != 0 || !strings.Contains(string(out), "not computed") || !strings.Contains(string(out), "sync of every document") {
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
	for _, road := range []string{"docs/draft.md", "docs/missing.md", "outside.md", "../docs/a/roadmap.md", "docs/a", ""} {
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
