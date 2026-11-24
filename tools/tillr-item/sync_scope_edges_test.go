package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestSyncRoadmapScopeCustomNamesParkingAndDryRun(t *testing.T) {
	repo := syncRepo(t, map[string]string{
		"docs/area.md":   "# Area\n[Shared](shared.md)\n" + scopeBlock("inline") + "## Icebox\n[Parked](parked.md)\n[Other roadmap](other/roadmap.md)\n",
		"docs/shared.md": scopeBlock("shared"), "docs/parked.md": scopeBlock("parked"),
		"docs/other/roadmap.md":  scopeBlock("other-inline") + "[Indirect](indirect.md)\n",
		"docs/other/indirect.md": scopeBlock("indirect"),
	})
	db := filepath.Join(scratch(t, "scope-custom"), "state.db")
	args := []string{"sync", "--store", db, "--root", repo, "--roadmap", "./docs/area.md", "--roadmap", "docs/area.md", "--json"}
	code, preview := run(t, append(args, "--dry-run")...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, preview)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("preview created store")
	}
	code, out := run(t, args...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	before, after := scopePayload(t, preview), scopePayload(t, out)
	// Created ids are a set; writes follow dependency/file order, not placement order.
	for _, p := range []map[string]any{before, after} {
		ids := p["created"].([]any)
		sort.Slice(ids, func(i, j int) bool { return ids[i].(string) < ids[j].(string) })
	}
	for _, field := range []string{"created", "placed", "scope", "skipped_blocks", "skipped_count", "unshared_open", "stale_doc_blocks"} {
		if !reflect.DeepEqual(before[field], after[field]) {
			t.Fatalf("%s differs: %v vs %v", field, before[field], after[field])
		}
	}
	if after["documents"] != float64(2) || after["skipped_count"] != float64(3) {
		t.Fatalf("unexpected intake: %s", out)
	}
	wantSkipped := []any{
		map[string]any{"file": "docs/other/indirect.md", "item_id": "item-indirect"},
		map[string]any{"file": "docs/other/roadmap.md", "item_id": "item-other-inline"},
		map[string]any{"file": "docs/parked.md", "item_id": "item-parked"},
	}
	if !reflect.DeepEqual(after["skipped_blocks"], wantSkipped) {
		t.Fatalf("skipped_blocks = %v, want %v", after["skipped_blocks"], wantSkipped)
	}
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var rows string
		if err := r.QueryRow(`SELECT group_concat(item_id||':'||state,',') FROM (SELECT item_id,state FROM items ORDER BY item_id)`).Scan(&rows); err != nil {
			return err
		}
		if rows != "item-inline:in-queue,item-shared:in-queue" {
			t.Fatal(rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncRoadmapScopeCrowdedPlacementRollsBackUnreadRanks(t *testing.T) {
	repo := syncRepo(t, map[string]string{"docs/seed.md": scopeBlock("source") + scopeBlock("next"), "docs/roadmap.md": "# Roadmap\n"})
	db := filepath.Join(scratch(t, "scope-crowded"), "state.db")
	code, out := run(t, "sync", "--store", db, "--root", repo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	s, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET rank=CASE item_id WHEN 'item-source' THEN 'a' ELSE 'a0' END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	block := strings.Replace(scopeBlock("new"), `paths="docs/"`, `paths="docs/" from="item-source"`, 1)
	if err := os.WriteFile(filepath.Join(repo, "docs/roadmap.md"), []byte(block), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := store.Fingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		args := append([]string{"sync", "--store", db, "--root", repo, "--roadmap", "docs/roadmap.md"}, extra...)
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
		out, err = cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "unread item") || !strings.Contains(string(out), "sync of every document") {
			t.Fatalf("%v error %v: %s", extra, err, out)
		}
	}
	after, err := store.Fingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("refused scoped placement wrote store")
	}
}
