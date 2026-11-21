package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkflowEmptyPiTurnRestartsRatherThanFinishes(t *testing.T) {
	f, l, _ := restartGraphFixture(t)
	l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
	l.cfg.Policy.Preference = []string{"fixture", "alternate"}
	l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "alternate"}}
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := l.stageDispatches(ctx, []Decision{p.decision}, base, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	work := pendingForTest(t, f, p.decision.ItemID)
	d := work.Decision
	// Even if a previous turn left a clean commit, the last turn did not
	// answer the node. The Pi extractor supplies a report without a footer.
	target := l.laneTarget(d.LaneID)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(target, "tools/item-a/result.txt"), "work\n")
	runGit(t, target, "add", "tools/item-a/result.txt")
	runGit(t, target, "commit", "-qm", "prior work")
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend,
		State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-21T00:00:00Z", FinishedAt: "2026-11-21T00:00:01Z"}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	const report = "complete: no\nresult: missing\n\n---\n\n[Pi's last assistant turn had no text or tool call.]\n"
	if handled, err := l.settleNode(ctx, d, rec, report, work.BaseSHA, &LoopResult{}, false); err != nil || !handled {
		t.Fatalf("settlement: handled=%v, err=%v", handled, err)
	}
	next := pendingForTest(t, f, d.ItemID).Decision
	if !next.WorkflowRestart || next.NodeIndex != d.NodeIndex || next.RunRequest.Backend != "fixture" || next.RunRequest.RunID != markRunID(d.RunRequest.RunID, "restart") {
		t.Fatalf("expected a same-backend restart of the same node, got %+v", next)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var outcome string
		if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID).Scan(&outcome); err != nil {
			return err
		}
		if outcome != "running" {
			t.Errorf("node outcome = %q, want running", outcome)
		}
		return nil
	})
}

func TestAbandonForkPreservesWorkAndRestages(t *testing.T) {
	for _, name := range []string{"dirty", "mid-rebase"} {
		t.Run(name, func(t *testing.T) {
			f, l, c := heldForkFixture(t)
			ctx := context.Background()
			if name == "mid-rebase" {
				runGit(t, c.Worktree, "checkout", "--detach", c.BaseSHA)
				runGit(t, c.Worktree, "commit", "--allow-empty", "-qm", "new upstream")
				upstream := runGit(t, c.Worktree, "rev-parse", "HEAD")
				runGit(t, c.Worktree, "checkout", "main")
				cmd := exec.Command("git", "-C", c.Worktree, "rebase", "--exec", "false", upstream)
				if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "execution failed") {
					t.Fatalf("expected rebase to pause after replaying a commit: %v: %s", err, out)
				}
				cDuringRebase := c
				cDuringRebase.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")
				if detail, err := l.fenceViolation(ctx, cDuringRebase); err != nil || !strings.Contains(detail, "still rebasing") {
					t.Fatalf("even a clean mid-rebase HEAD is not a candidate: %q, %v", detail, err)
				}
			}
			head := runGit(t, c.Worktree, "rev-parse", "HEAD")
			writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "staged resolution\n")
			runGit(t, c.Worktree, "add", "candidate.txt")
			writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "further working edit\n")
			writeForkFile(t, filepath.Join(c.Worktree, "notes.txt"), "untracked notes\n")
			if stop := l.abandon(ctx, c.LaneID, c.RunID, head); stop != "" {
				t.Fatalf("abandon: %s", stop)
			}
			stager := LaneStager{Root: f.root, Store: f.storePath, LaneRoot: l.cfg.LaneRoot}
			if err := stager.refreshForkCheckout(ctx, ForkTarget{Ref: c.BaseRef, Checkout: c.Worktree}); err != nil {
				t.Fatalf("next item's fork staging: %v", err)
			}
			if got := runGit(t, c.Worktree, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
				t.Fatalf("fork is not on main: %s", got)
			}
			if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.BaseSHA {
				t.Fatalf("fork was not restored to delivery: %s", got)
			}
			if _, err := os.Stat(filepath.Join(c.Worktree, ".git", "rebase-merge")); !os.IsNotExist(err) {
				t.Fatalf("rebase state still exists: %v", err)
			}
			ref := preservedPrefix + c.RunID
			for object, want := range map[string]string{
				ref + ":candidate.txt": "further working edit", ref + "^2:candidate.txt": "staged resolution",
				ref + ":notes.txt": "untracked notes",
			} {
				if got := runGit(t, c.Worktree, "show", object); got != want {
					t.Errorf("%s = %q, want %q", object, got, want)
				}
			}
			for object, want := range map[string]string{pinRef(c.RunID): head, ref + "^1": head} {
				if got := runGit(t, c.Worktree, "rev-parse", object); got != want {
					t.Errorf("%s = %q, want %q", object, got, want)
				}
			}
			if _, err := l.revParse(ctx, f.root, head+"^{commit}"); err == nil {
				t.Error("pinning imported the fork commit into the root repository")
			}
			if name == "mid-rebase" {
				if got := runGit(t, c.Worktree, "rev-parse", pinRef(c.RunID+"-pre-rebase")); got != c.HeadSHA {
					t.Fatalf("original branch tip not retained: %s", got)
				}
			}
		})
	}
}
