package main_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type inventoryRow struct {
	Part        string `json:"part"`
	Name        string `json:"name"`
	Grain       string `json:"grain"`
	Count       int64  `json:"count"`
	Bytes       *int64 `json:"bytes"`
	Description string `json:"description"`
}
type inventoryPayload struct {
	Rows       []inventoryRow `json:"rows"`
	LaneTotals []inventoryRow `json:"lane_totals"`
	Totals     []struct {
		Directory string `json:"directory"`
		Bytes     int64  `json:"bytes"`
		RowBytes  int64  `json:"row_bytes"`
	} `json:"totals"`
	LargestRuns []struct {
		RunID            string `json:"run_id"`
		Bytes            int64  `json:"bytes"`
		LargestFile      string `json:"largest_file"`
		LargestFileBytes int64  `json:"largest_file_bytes"`
	} `json:"largest_runs"`
}

func clearGitRepoEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE", "GIT_GRAFT_FILE",
			"GIT_SHALLOW_FILE", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
			"GIT_PREFIX", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func inventoryGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = clearGitRepoEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
func inventoryFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, ".tillr", name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func inventoryFixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	inventoryGit(t, root, "init", "-q")
	// Git's empty tree supplies an object without commits or an invented identity.
	tree := strings.TrimSpace(inventoryGit(t, root, "hash-object", "-w", "-t", "tree", "--stdin"))
	for _, ref := range []string{"runs/a", "runs/b", "preserved/a", "new-kind/a"} {
		inventoryGit(t, root, "update-ref", "refs/tillr/"+ref, tree)
	}
	path := filepath.Join(root, ".tillr", "state.db")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Include a quoted identifier to ensure SQL identifiers are escaped, not interpolated raw.
	if _, err := db.Exec(`CREATE TABLE "new""table" (value TEXT); INSERT INTO "new""table" VALUES ('committed')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		inventoryFile(t, root, fmt.Sprintf("loop/run-%02d/git-trace2.jsonl", i), strings.Repeat("x", i+1))
		inventoryFile(t, root, fmt.Sprintf("loop/run-%02d/nested/new.bin", i), "zz")
	}
	inventoryFile(t, root, "loop/loose.bin", "loose")
	inventoryFile(t, root, "lanes/lane-1/worktree/src/a.go", "source")
	inventoryFile(t, root, "lanes/lane-1/report.md", "report")
	inventoryFile(t, root, "lanes/lane-2/brief.md", "brief")
	inventoryFile(t, root, "lanes/loose.bin", "lane-loose")
	inventoryFile(t, root, "other/deep/new.bin", "outside-known-parts")
	// The target is deliberately outside the store, and must not enter its totals.
	if err := os.Symlink(root, filepath.Join(root, ".tillr", "loop", "run-00", "link")); err != nil {
		t.Fatal(err)
	}
	return root, db
}
func readInventory(t *testing.T, root string) (inventoryPayload, []byte) {
	t.Helper()
	code, raw := run(t, "inventory", "--root", root, "--json")
	if code != 0 {
		t.Fatalf("inventory exit %d: %s", code, raw)
	}
	var env struct {
		Kind    string           `json:"kind"`
		Payload inventoryPayload `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "store.inventory" {
		t.Fatalf("kind %s", env.Kind)
	}
	return env.Payload, raw
}
func findInventoryRow(t *testing.T, p inventoryPayload, part, name string) inventoryRow {
	t.Helper()
	for _, row := range p.Rows {
		if row.Part == part && row.Name == name {
			return row
		}
	}
	t.Fatalf("missing %s/%s in %+v", part, name, p.Rows)
	return inventoryRow{}
}
func findInventoryLaneTotal(t *testing.T, p inventoryPayload, name string) inventoryRow {
	t.Helper()
	return findInventoryRow(t, inventoryPayload{Rows: p.LaneTotals}, "lanes", name)
}

func TestInventoryAccountsForEveryPart(t *testing.T) {
	root, _ := inventoryFixture(t)
	p, _ := readInventory(t, root)
	trace := findInventoryRow(t, p, "loop", "git-trace2.jsonl")
	if trace.Count != 12 || trace.Bytes == nil || *trace.Bytes != 78 || trace.Grain != "runs" {
		t.Fatalf("trace %+v", trace)
	}
	unknown := findInventoryRow(t, p, "loop", "new.bin")
	if unknown.Count != 12 || *unknown.Bytes != 24 || unknown.Description != "undescribed" {
		t.Fatalf("unknown %+v", unknown)
	}
	table := findInventoryRow(t, p, "state.db", "new\"table")
	if table.Count != 1 || table.Bytes != nil || table.Description != "undescribed" {
		t.Fatalf("table %+v", table)
	}
	if row := findInventoryLaneTotal(t, p, "lane-1/worktree"); row.Count != 1 || *row.Bytes != 6 {
		t.Fatalf("worktree %+v", row)
	}
	if row := findInventoryLaneTotal(t, p, "lane-1/other"); row.Count != 1 || *row.Bytes != 6 {
		t.Fatalf("other %+v", row)
	}
	if row := findInventoryLaneTotal(t, p, "lane-2/worktree"); row.Count != 0 || *row.Bytes != 0 {
		t.Fatalf("empty worktree %+v", row)
	}
	if row := findInventoryRow(t, p, "refs/tillr", "runs"); row.Count != 2 || row.Bytes != nil {
		t.Fatalf("refs %+v", row)
	}
	if row := findInventoryRow(t, p, "refs/tillr", "new-kind"); row.Description != "undescribed" {
		t.Fatalf("unknown ref %+v", row)
	}
	if row := findInventoryRow(t, p, "files", "other/deep/new.bin"); row.Description != "undescribed" {
		t.Fatalf("unknown root file %+v", row)
	}
	for _, summary := range p.LaneTotals {
		var sum, count int64
		for _, row := range p.Rows {
			if row.Part == "lanes" && (row.Name == summary.Name || strings.HasPrefix(row.Name, summary.Name+"/")) {
				sum += *row.Bytes
				count += row.Count
			}
		}
		if sum != *summary.Bytes || count != summary.Count {
			t.Fatalf("lane total %+v: sum=%d count=%d", summary, sum, count)
		}
	}
	// Compare independent recursive size sums, including symlink length but not targets.
	for _, total := range p.Totals {
		dir := filepath.Join(root, total.Directory)
		var size int64
		if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, row := range p.Rows {
			if row.Bytes != nil && (total.Directory == ".tillr" || row.Part == strings.TrimPrefix(total.Directory, ".tillr/")) {
				sum += *row.Bytes
			}
		}
		if size != total.Bytes || sum != size || total.RowBytes != sum {
			t.Fatalf("total %+v: files=%d rows=%d", total, size, sum)
		}
	}
	if len(p.Totals) != 3 {
		t.Fatalf("totals %+v", p.Totals)
	}
	if len(p.LargestRuns) != 10 || p.LargestRuns[0].RunID != "run-00" {
		t.Fatalf("largest %+v", p.LargestRuns)
	}
	// run-00 has a long symlink; the next run is run-11, with a 12-byte trace.
	if p.LargestRuns[1].RunID != "run-11" || p.LargestRuns[1].LargestFile != "git-trace2.jsonl" || p.LargestRuns[1].LargestFileBytes != 12 {
		t.Fatalf("rank %+v", p.LargestRuns)
	}
	code, text := run(t, "inventory", "--root", root)
	if code != 0 || bytes.HasPrefix(text, []byte("{")) {
		t.Fatalf("text %d: %s", code, text)
	}
	for _, row := range append(p.Rows, p.LaneTotals...) {
		expected := fmt.Sprintf("%q\t%q\t%s\t%d\t", row.Part, row.Name, row.Grain, row.Count)
		if row.Bytes == nil {
			expected += "-"
		} else {
			expected += fmt.Sprint(*row.Bytes)
		}
		expected += "\t" + row.Description
		if !strings.Contains(string(text), expected) {
			t.Fatalf("text missing %s: %s", expected, text)
		}
	}
}

type inventoryFileState struct {
	Data     string
	Modified time.Time
	Mode     fs.FileMode
}

func inventorySnapshot(t *testing.T, root string) map[string]inventoryFileState {
	t.Helper()
	states := map[string]inventoryFileState{}
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data := ""
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			data = string(raw)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			data, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		states[path] = inventoryFileState{data, info.ModTime(), info.Mode()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return states
}
func TestInventoryWritesNothingWithAWriterOpen(t *testing.T) {
	root, s := inventoryFixture(t)
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO "new""table" VALUES ('uncommitted')`); err != nil {
		t.Fatal(err)
	}
	before := inventorySnapshot(t, root)
	p, _ := readInventory(t, root)
	if row := findInventoryRow(t, p, "state.db", "new\"table"); row.Count != 1 {
		t.Fatalf("read uncommitted data: %+v", row)
	}
	if code, out := run(t, "inventory", "--root", root); code != 0 {
		t.Fatalf("text: %d %s", code, out)
	}
	after := inventorySnapshot(t, root)
	if !reflect.DeepEqual(before, after) {
		for path, want := range before {
			if got := after[path]; got != want {
				t.Errorf("inventory changed %s (bytes equal=%v, mtime equal=%v)", path, got.Data == want.Data, got.Modified.Equal(want.Modified))
			}
		}
		for path := range after {
			if _, ok := before[path]; !ok {
				t.Errorf("inventory created %s", path)
			}
		}
	}
}
func TestInventoryRefusesMissingRootWithoutCreatingFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	code, out := run(t, "inventory", "--root", root, "--json")
	if code != 2 || len(out) != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root created: %v", err)
	}
}

func TestInventoryDescribesItemPinRefs(t *testing.T) {
	root, _ := inventoryFixture(t)
	tree := strings.TrimSpace(inventoryGit(t, root, "mktree"))
	inventoryGit(t, root, "update-ref", "refs/tillr/items/item-a", tree)
	inventoryGit(t, root, "update-ref", "refs/tillr/items-long/0123456789abcdef", tree)

	p, _ := readInventory(t, root)
	for _, name := range []string{"items", "items-long"} {
		row := findInventoryRow(t, p, "refs/tillr", name)
		if row.Count != 1 || row.Bytes != nil {
			t.Fatalf("%s row = %+v, want count=1 bytes=nil", name, row)
		}
		if row.Description == "" || row.Description == "undescribed" {
			t.Fatalf("%s description = %q, want described", name, row.Description)
		}
	}
}
