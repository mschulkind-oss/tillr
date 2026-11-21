package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestContinuedStepPreservedConflictStagesCleanPinnedTrunk(t *testing.T) {
	for _, scenario := range []string{"modified-on-trunk", "deleted-on-trunk", "type-changed-on-trunk", "modified-and-deleted-on-trunk"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			f := newRefusalLoopFixtureWithLanes(t, 2, "item-a")
			l := f.loop(t, &recordingSteps{}, &LaneStager{
				Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot,
				Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			})
			var report bytes.Buffer
			l.cfg.Report = &report
			if err := l.stageLanes(ctx); err != nil {
				t.Fatal(err)
			}

			failedPath := "tools/item-a/overlap.txt"
			deletedPath := "tools/item-a/z-deleted.txt"
			failedPaths := []string{failedPath}
			mixed := scenario == "modified-and-deleted-on-trunk"
			if mixed {
				failedPaths = append(failedPaths, deletedPath)
			}
			stagedPath := "tools/item-a/staged.txt"
			if err := os.MkdirAll(filepath.Join(f.root, filepath.Dir(failedPath)), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, path := range failedPaths {
				writeForkFile(t, filepath.Join(f.root, path), "original content\n")
				runGit(t, f.root, "add", path)
			}
			runGit(t, f.root, "commit", "-qm", "original item base")
			itemBase := runGit(t, f.root, "rev-parse", TrunkRef)
			lane1, lane2 := l.laneTarget("lane-1"), l.laneTarget("lane-2")
			for _, lane := range []string{lane1, lane2} {
				runGit(t, lane, "reset", "--hard", itemBase)
			}

			// The index portion is independent of the conflicting working-tree portion.
			writeForkFile(t, filepath.Join(lane1, stagedPath), "staged content\n")
			runGit(t, lane1, "add", stagedPath)
			for _, path := range failedPaths {
				writeForkFile(t, filepath.Join(lane1, path), "preserved worker content\n")
			}
			if err := os.MkdirAll(RunDir(f.root, "run-prev"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(RunDir(f.root, "run-prev"), ItemBaseSHAFile), itemBase+"\n")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT INTO dispatches(decision_id, item_id, lane_id, run_id, action, reason, dispatched_at) VALUES('dec-prev', 'item-a', 'lane-1', 'run-prev', 'dispatch', 'ready', 1)`); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 1, 'scout', 'run-prev', 'completed')`); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 1, 'build', 'run-next', 'running')`)
				return err
			})
			if err := l.preserveWork(ctx, "lane-1", "run-prev", "session ended for looping"); err != nil {
				t.Fatal(err)
			}
			keptRef := preservedPrefix + "run-prev"
			keptSHA := runGit(t, f.root, "rev-parse", keptRef)

			switch scenario {
			case "deleted-on-trunk":
				runGit(t, f.root, "rm", failedPath)
			case "type-changed-on-trunk":
				if err := os.Remove(filepath.Join(f.root, failedPath)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("staged.txt", filepath.Join(f.root, failedPath)); err != nil {
					t.Fatal(err)
				}
				runGit(t, f.root, "add", failedPath)
			default:
				writeForkFile(t, filepath.Join(f.root, failedPath), "concurrent trunk content\n")
				runGit(t, f.root, "add", failedPath)
			}
			if mixed {
				runGit(t, f.root, "rm", deletedPath)
			}
			runGit(t, f.root, "commit", "-qm", "trunk overlaps preserved working tree")
			nodeBase := runGit(t, f.root, "rev-parse", TrunkRef)
			writeForkFile(t, filepath.Join(f.root, "pinned-trunk.txt"), "pinned trunk content\n")
			runGit(t, f.root, "add", "pinned-trunk.txt")
			runGit(t, f.root, "commit", "-qm", "advance trunk past node base")
			mainSHA := runGit(t, f.root, "rev-parse", TrunkRef)
			if itemBase == nodeBase || nodeBase == mainSHA {
				t.Fatal("fixture needs distinct item, node and pinned trunk bases")
			}

			wf := &Workflow{
				Name: "scout-build", Version: 1,
				Stages: []WorkflowStage{
					{ID: "scout", Seat: "scout", Kind: "agent", Access: "read-only"},
					{ID: "build", Seat: "build", Kind: "agent", Access: "write"},
				},
			}
			d := Decision{
				ItemID: "item-a", LaneID: "lane-2", Action: ActionDispatch, Attempt: 1,
				Workflow: wf, NodeIndex: 1, NodeBaseSHA: nodeBase,
				RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-2", RunID: "run-next", Backend: "fixture", WorkingDir: WorktreeDir},
			}
			if err := appendJudgment(ctx, f.storePath, dispatchStartedKind, "run", d.RunRequest.RunID, pendingWork{Decision: d}, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			var result LoopResult
			// The ordinary dispatch base can be green; the conflict must use the tip.
			staged, err := l.stageDispatches(ctx, []Decision{d}, itemBase, &result)
			if err != nil {
				t.Errorf("continued staging returned error: %v", err)
			}
			if len(staged) != 1 {
				t.Errorf("staged = %d, want 1 despite preserved conflict (report: %s)", len(staged), report.String())
			} else if staged[0].NodeBaseSHA != mainSHA {
				t.Errorf("staged node base = %s, want pinned trunk %s", staged[0].NodeBaseSHA, mainSHA)
			}
			if result.InfraFailures != 0 {
				t.Errorf("infra failures = %d, want 0", result.InfraFailures)
			}
			if got := runGit(t, lane2, "rev-parse", "HEAD"); got != mainSHA {
				t.Errorf("lane HEAD = %s, want pinned trunk %s (not node base %s)", got, mainSHA, nodeBase)
			}
			if status := runGit(t, lane2, "status", "--porcelain", "--untracked-files=all"); status != "" {
				t.Errorf("lane retains partially applied preserved work: %q", status)
			}
			if err := l.prunePreservedWork(ctx); err != nil {
				t.Fatal(err)
			}
			if got := runGit(t, f.root, "rev-parse", keptRef); got != keptSHA {
				t.Errorf("kept ref changed: %s, want %s", got, keptSHA)
			}

			laneDir := l.laneDirPath("lane-2")
			for name, want := range map[string]string{BaseSHAFile: mainSHA, ItemBaseSHAFile: mainSHA} {
				raw, err := os.ReadFile(filepath.Join(laneDir, name))
				if err != nil || strings.TrimSpace(string(raw)) != want {
					t.Errorf("%s = %q (%v), want %s", name, raw, err, want)
				}
			}
			brief, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
			if err != nil {
				t.Errorf("continued brief not rendered: %v", err)
			} else {
				if !bytes.Contains(brief, []byte(keptRef)) {
					t.Errorf("brief must name kept ref %s", keptRef)
				}
				for _, path := range failedPaths {
					if !bytes.Contains(brief, []byte("Kept-work conflict: \""+path+"\"")) {
						t.Errorf("brief does not name %s as a kept-work conflict", path)
					}
				}
				if bytes.Contains(brief, []byte("Kept-work conflict: \""+stagedPath+"\"")) {
					t.Errorf("brief mistakenly names the applicable staged file %s as a conflict", stagedPath)
				}
			}

			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				var holds int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id='item-a' AND json_extract(payload,'$.reason')=?`, itemFaultKind, ReasonStageFailed).Scan(&holds); err != nil {
					return err
				}
				if holds != 0 {
					t.Errorf("stage-failed hold events = %d, want 0 for preserved content conflict", holds)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApplyMismatchDoesNotMaskIndexWriteFailure(t *testing.T) {
	stderr := "Applied patch to 'kept.txt' with conflicts.\nerror: unable to write new index file\n"
	if paths, contentFailure := applyMismatchPaths(stderr); contentFailure || len(paths) != 0 {
		t.Fatalf("index write failure reported as content conflict: %v, %t", paths, contentFailure)
	}
}
