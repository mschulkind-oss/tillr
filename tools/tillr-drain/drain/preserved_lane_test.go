package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPreservedDirtyLaneReuseClearsOnlyCoveredWork(t *testing.T) {
	for _, mode := range []string{"covered", "novel-working", "novel-index", "novel-file", "ignored-file", "missing-ref", "pruned", "other-lane", "other-head"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a", "item-b")
			l.cfg.Lanes = []string{"lane-1"}
			if err := l.stageLanes(ctx); err != nil {
				t.Fatal(err)
			}
			wt := l.lanePath("lane-1")
			runGit(t, wt, "commit", "--allow-empty", "-qm", "unlanded original run")
			head := runGit(t, wt, "rev-parse", "HEAD")
			runGit(t, f.root, "update-ref", "refs/tillr/runs/run-original", head)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id,state) VALUES('lane-1','idle')`); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('run-original','item-a','lane-1','fixture','finished',1,2,'[]',1)`)
				return err
			})
			writeForkFile(t, filepath.Join(wt, "base.txt"), "staged bytes\n")
			runGit(t, wt, "add", "base.txt")
			writeForkFile(t, filepath.Join(wt, "base.txt"), "working bytes\n")
			writeForkFile(t, filepath.Join(wt, "untracked.txt"), "saved untracked\n")
			if err := l.preserveWork(ctx, "lane-1", "run-original", "fixture preservation"); err != nil {
				t.Fatal(err)
			}
			ref := preservedPrefix + "run-original"
			switch mode {
			case "novel-working":
				writeForkFile(t, filepath.Join(wt, "base.txt"), "new working bytes\n")
			case "novel-index":
				writeForkFile(t, filepath.Join(wt, "base.txt"), "new staged bytes\n")
				runGit(t, wt, "add", "base.txt")
				writeForkFile(t, filepath.Join(wt, "base.txt"), "working bytes\n")
			case "novel-file":
				writeForkFile(t, filepath.Join(wt, "novel.txt"), "novel\n")
			case "ignored-file":
				writeForkFile(t, filepath.Join(wt, "ignored.txt"), "novel ignored bytes\n")
				info := runGit(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
				writeForkFile(t, info, "ignored.txt\n")
			case "missing-ref":
				runGit(t, f.root, "update-ref", "-d", ref)
			case "other-head":
				runGit(t, wt, "commit", "--allow-empty", "-qm", "another head")
			case "pruned", "other-lane":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					if mode == "pruned" {
						return appendEffect(tx, workPrunedKind, "run-original", PreservedWork{Ref: ref, Repository: f.root}, l.cfg.Now())
					}
					if err := appendEffect(tx, workPrunedKind, "run-original", PreservedWork{Ref: ref, Repository: f.root}, l.cfg.Now()); err != nil {
						return err
					}
					return appendEffect(tx, workPreservedKind, "run-original", PreservedWork{RunID: "run-original", LaneID: "lane-2", Ref: ref, Repository: f.root}, l.cfg.Now())
				})
			}
			status := runGit(t, wt, "status", "--porcelain", "--untracked-files=all")
			index := runGit(t, wt, "write-tree")
			before, err := os.ReadFile(filepath.Join(wt, "base.txt"))
			if err != nil {
				t.Fatal(err)
			}
			stager := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Now: l.cfg.Now}
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			err = stager.ensureWorktree(ctx, StageSpec{LaneID: "lane-1", ItemID: "item-b", RunID: "run-next", BaseSHA: base}, l.laneDirPath("lane-1"), wt)
			if mode == "covered" {
				if err != nil {
					t.Fatalf("covered foreign-item reuse: %v", err)
				}
				if got := runGit(t, wt, "status", "--porcelain", "--untracked-files=all"); got != "" {
					t.Fatalf("dirty after reuse: %s", got)
				}
				if got := runGit(t, f.root, "show", ref+":base.txt"); got != "working bytes" {
					t.Fatal(got)
				}
				if got := runGit(t, f.root, "show", ref+"^2:base.txt"); got != "staged bytes" {
					t.Fatal(got)
				}
			} else {
				if refusal := asLaneRefusal(err); refusal == nil || refusal.Reason != ReasonLaneUncommittedWork {
					t.Fatalf("want dirty lane refusal, got %v", err)
				}
				if got := runGit(t, wt, "status", "--porcelain", "--untracked-files=all"); got != status {
					t.Fatalf("status changed: %s", got)
				}
				if got := runGit(t, wt, "write-tree"); got != index {
					t.Fatal("index changed")
				}
				after, _ := os.ReadFile(filepath.Join(wt, "base.txt"))
				if string(after) != string(before) {
					t.Fatal("working bytes changed")
				}
			}
		})
	}
}
