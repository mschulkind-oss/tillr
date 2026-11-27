package drain

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A resumed build that offers a commit must be followed by the workflow's next node on
// that commit, even when the returned worker session names no attempt (or names one whose
// earlier nodes were themselves recorded as reused from an attempt that never ran them)
// and the item's worktree is collected between the offer and the next node.
//
// Recorded 2026-11-27: the returned session carried attempt 0, so the plan node was
// recorded as reused from attempt 0, where it never ran. After the resumed build offered
// its commit, the plan node ran again as the "next" node, its node-0 staging reset the lane
// to the base, and the verifier read the base with an empty diff. Each later attempt copied
// the same attempt-0 source forward.
func TestResumedBuildNextNodeReadsOfferedCommitAfterCollection(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sessionFrom int // the returned worker session's recorded attempt
		seed        string
	}{
		{name: "session-without-attempt", sessionFrom: 0},
		{name: "stale-reused-source", sessionFrom: 10, seed: `INSERT INTO node_reused(item_id,attempt,node_id,source_attempt) VALUES(?,10,'plan',0)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newRefusalLoopFixture(t)
			l, _ := newWorktreeCollectTestLoop(t, f)

			itemID := "item-resumed-" + tc.name
			const attempt = 11
			buildRun := "run-" + itemID + "-a11-n2"
			// The builder's worktree holds the commit it offered with land.
			_, wt, _, _, offered := stageFixtureItemDir(t, f, itemID, buildRun)
			base := runGit(t, f.root, "rev-parse", "HEAD")

			w := &Workflow{Name: "planned-build", Version: 1,
				Stages: []WorkflowStage{
					{ID: "plan", Kind: "agent", Seat: "plan", Access: "read_only"},
					{ID: "work", Kind: "agent", Seat: "build", Access: "write"},
					{ID: "verify", Kind: "agent", Seat: "verify", Access: "read_only"},
				},
				Edges: []WorkflowEdge{{From: "plan", To: "work"}, {From: "work", To: "verify"}},
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT INTO item_criteria(item_id, criterion_id, text, state) VALUES(?, 'c1', 'criterion 1', 'open')`, itemID); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES
 (?,8,'plan',?,'passed'),(?,8,'work',?,'passed'),(?,8,'verify',?,'evaluated-fail')`,
					itemID, "run-"+itemID+"-a8-n1", itemID, "run-"+itemID+"-a8-n2", itemID, "run-"+itemID+"-a8-n3"); err != nil {
					return err
				}
				if tc.seed != "" {
					if _, err := tx.Exec(tc.seed, itemID); err != nil {
						return err
					}
				}
				d := Decision{
					ItemID:     itemID,
					Attempt:    attempt,
					NodeIndex:  1,
					Workflow:   w,
					RunRequest: &run.Request{RunID: buildRun, SessionTurn: true},
					Returned: &ReturnedEnding{Cause: "verify-not-done", WorkerSession: &WorkerSession{
						RunID: "run-" + itemID + "-a10-n2", NodeIndex: 1, Attempt: tc.sessionFrom, Workflow: w,
					}},
				}
				if err := carryReturnedNodeInputs(tx, d); err != nil {
					return err
				}
				// The resumed build passed: it offered its commit with land.
				// (stageFixtureItemDir recorded its run as a placeholder node; make it this one.)
				_, err := tx.Exec(`UPDATE node_runs SET attempt=?, node_id='work', outcome='passed' WHERE run_id=?`, attempt, buildRun)
				return err
			})

			// The item's worktree is collected for the size bound before the next node stages.
			if !l.collectOneIdleWorktree(ctx, idleWorktreeCandidate{laneID: itemID, itemID: itemID, itemState: StateInQueue, latestRunID: buildRun, waitReason: "lru-bound"}) {
				t.Fatal("the item's worktree was not collected")
			}

			next, err := l.nextWorkflowNode(ctx, Decision{ItemID: itemID, Attempt: attempt, Workflow: w})
			if err != nil {
				t.Fatal(err)
			}
			if next != 2 {
				t.Fatalf("next node after the resumed build = %d, want verify (2): the plan node was not carried into attempt %d", next, attempt)
			}

			tpl := filepath.Join(f.root, "brief-template.md")
			writeCollectFile(t, tpl, "# {{ITEM_ID}}\n{{TASK}}\n")
			stager := &LaneStager{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Template: tpl}
			node := w.Stages[next]
			if err := stager.Stage(ctx, StageSpec{
				ItemID: itemID, RunID: "run-" + itemID + "-a11-n3", LaneID: itemID, WorkingDir: WorktreeDir,
				BaseSHA: base, ItemBaseSHA: base, Continue: true, Node: &node,
			}); err != nil {
				t.Fatalf("staging the next node: %v", err)
			}
			if got := runGit(t, wt, "rev-parse", "HEAD"); got != offered {
				t.Fatalf("next node staged on %s, want the offered commit %s", got, offered)
			}
		})
	}
}
