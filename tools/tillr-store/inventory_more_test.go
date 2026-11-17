package main_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestInventoryCountsRepeatedNamesOncePerRun(t *testing.T) {
	root, _ := inventoryFixture(t)
	inventoryFile(t, root, "loop/run-01/another/new.bin", "also")
	p, _ := readInventory(t, root)
	row := findInventoryRow(t, p, "loop", "new.bin")
	if row.Count != 12 || *row.Bytes != 28 {
		t.Fatalf("duplicate filename %+v", row)
	}
}

func TestInventoryWritesNothingWithoutAnotherConnection(t *testing.T) {
	for _, journal := range []string{"wal", "delete"} {
		t.Run(journal, func(t *testing.T) {
			root := t.TempDir()
			inventoryGit(t, root, "init", "-q")
			path := filepath.Join(root, ".tillr", "state.db")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if journal == "wal" {
				s, err := store.InitFixture(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				db, err := sql.Open("sqlite", "file:"+path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`CREATE TABLE unknown (value); INSERT INTO unknown VALUES (1)`); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := inventorySnapshot(t, root)
			p, _ := readInventory(t, root)
			if journal == "delete" && findInventoryRow(t, p, "state.db", "unknown").Count != 1 {
				t.Fatal("lost rows")
			}
			if journal == "wal" && findInventoryRow(t, p, "state.db", "meta").Count == 0 {
				t.Fatal("lost committed WAL rows")
			}
			if len(p.LargestRuns) != 0 {
				t.Fatalf("unexpected runs %+v", p.LargestRuns)
			}
			if after := inventorySnapshot(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("inventory changed files without another connection")
			}
		})
	}
}

func TestInventoryUnknownLaneFilesAreNamed(t *testing.T) {
	root, _ := inventoryFixture(t)
	inventoryFile(t, root, "lanes/lane-1/mystery.bin", "unknown")
	inventoryFile(t, root, "lanes/lane-1/worktree/mystery-source.bin", "unknown-source")
	inventoryFile(t, root, "lanes/lane-1/worktree/report.md", "source-document-not-a-worker-report")
	p, _ := readInventory(t, root)
	for _, name := range []string{"lane-1/other/mystery.bin", "lane-1/worktree/mystery-source.bin", "lane-1/worktree/report.md"} {
		row := findInventoryRow(t, p, "lanes", name)
		if row.Description != "undescribed" || row.Bytes == nil || row.Count != 1 {
			t.Fatalf("unknown %+v", row)
		}
	}
}

func TestInventoryLooseFileCannotMergeIntoALaneCategory(t *testing.T) {
	root, _ := inventoryFixture(t)
	inventoryFile(t, root, "lanes/lanes/report.md", "report")
	inventoryFile(t, root, "lanes/other", "loose")
	p, _ := readInventory(t, root)
	row := findInventoryLaneTotal(t, p, "lanes/other")
	if row.Count != 1 || *row.Bytes != 6 {
		t.Fatalf("loose file merged into lane: %+v", row)
	}
	row = findInventoryRow(t, p, "lanes", "./other")
	if row.Description != "undescribed" || *row.Bytes != 5 {
		t.Fatalf("loose file %+v", row)
	}
}

func TestInventoryEmptyRunsAndTies(t *testing.T) {
	root, _ := inventoryFixture(t)
	// Replace just this fixture's loop tree, never the repository's live store.
	loop := filepath.Join(root, ".tillr", "loop")
	if err := os.RemoveAll(loop); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "empty"} {
		if err := os.MkdirAll(filepath.Join(loop, id), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		inventoryFile(t, root, "loop/"+id+"/z", "same")
		inventoryFile(t, root, "loop/"+id+"/a", "same")
	}
	p, _ := readInventory(t, root)
	if len(p.LargestRuns) != 3 || p.LargestRuns[0].RunID != "a" || p.LargestRuns[1].RunID != "b" || p.LargestRuns[0].LargestFile != "a" || p.LargestRuns[2].RunID != "empty" || p.LargestRuns[2].LargestFile != "" || p.LargestRuns[2].Bytes != 0 {
		t.Fatalf("runs %+v", p.LargestRuns)
	}
}

func TestInventoryIgnoresInheritedGitRepository(t *testing.T) {
	root, _ := inventoryFixture(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing.git"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	p, _ := readInventory(t, root)
	if row := findInventoryRow(t, p, "refs/tillr", "runs"); row.Count != 2 {
		t.Fatalf("wrong repository %+v", row)
	}
}

func TestInventoryNoStoreOrGitNeverPrintsPartialReport(t *testing.T) {
	for _, missing := range []string{"database", "git"} {
		t.Run(missing, func(t *testing.T) {
			root := t.TempDir()
			if missing == "database" {
				inventoryGit(t, root, "init", "-q")
				if err := os.Mkdir(filepath.Join(root, ".tillr"), 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				s, err := store.InitFixture(context.Background(), filepath.Join(root, ".tillr", "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
			}
			before := inventorySnapshot(t, root)
			code, out := run(t, "inventory", "--root", root, "--json")
			if code == 0 || len(out) != 0 {
				t.Fatalf("partial report: exit=%d %s", code, out)
			}
			if after := inventorySnapshot(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("failure changed files")
			}
		})
	}
}
