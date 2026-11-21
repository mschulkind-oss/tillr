package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestForkCheckoutFreshStagingRetention(t *testing.T) {
	for _, tc := range []struct {
		name              string
		ref               func(string) string
		preservedSnapshot bool
		unpinnedTip       bool
	}{
		{name: "run-pin", ref: pinRef},
		{name: "long-run-pin", ref: pinRef},
		{name: "preserved-snapshot", ref: func(id string) string { return preserved.Prefix + id }, preservedSnapshot: true},
		{name: "unpinned-tip", ref: pinRef, unpinnedTip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, fork, trunk, base := forkNodeBaseFixture(t)
			ctx := context.Background()
			p := mustPlan(t, l)[0]
			ok, work, err := l.applyPlan(ctx, p, trunk, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("dispatch: %v / %v", ok, err)
			}
			prior := "run-earlier-attempt"
			if tc.name == "long-run-pin" {
				prior += strings.Repeat("x", 256)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				at := l.cfg.Now().Format(time.RFC3339)
				return applyTerminal(tx, run.Record{RunID: prior, ItemID: p.decision.ItemID, LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}})
			})
			writeForkFile(t, filepath.Join(fork, "candidate.txt"), "earlier attempt\n")
			runGit(t, fork, "add", "candidate.txt")
			runGit(t, fork, "commit", "-qm", "earlier attempt")
			head := runGit(t, fork, "rev-parse", "HEAD")
			ref, kept := tc.ref(prior), head
			if tc.preservedSnapshot {
				kept = runGit(t, fork, "commit-tree", "HEAD^{tree}", "-p", head, "-m", "preserved snapshot")
				// Also exercise a delivery that diverged from the earlier attempt.
				base = runGit(t, fork, "commit-tree", base+"^{tree}", "-p", base, "-m", "new delivery")
				runGit(t, f.root, "fetch", "--no-tags", fork, base+":refs/heads/patch/pi")
			}
			runGit(t, fork, "update-ref", ref, kept)
			if tc.unpinnedTip {
				runGit(t, fork, "commit", "--allow-empty", "-qm", "unretained work above the pin")
				head = runGit(t, fork, "rev-parse", "HEAD")
			}

			result := &LoopResult{}
			staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, trunk, result)
			if err != nil || result.InfraFailures != 0 {
				t.Fatalf("staging: %v; infrastructure failures: %d", err, result.InfraFailures)
			}
			wantHead := base
			if tc.unpinnedTip {
				wantHead = head
				if len(staged) != 0 {
					t.Fatal("staged over unretained work")
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					fault := mustFaults(t, r)[p.decision.ItemID]
					if fault.Reason != ReasonStageFailed || fault.RunID != work.Decision.RunRequest.RunID || !strings.Contains(fault.Detail, fork) || !strings.Contains(fault.Detail, head) {
						t.Fatalf("stage hold = %+v, want fault naming the checkout and its unretained tip", fault)
					}
					_, _, mover := whyPresentation(Decision{Action: ActionHold, Reason: fault.Reason})
					if !strings.Contains(mover, "tillr drain clear-fault") {
						t.Fatalf("stage hold mover = %q", mover)
					}
					return nil
				})
			} else {
				if len(staged) != 1 || l.laneTarget(p.decision.LaneID) != fork || l.laneBase(p.decision.LaneID) != base {
					t.Fatalf("did not stage a fresh run at delivered base %s: %+v", base, staged)
				}
				runGit(t, fork, "merge-base", "--is-ancestor", head, ref)
				if _, err := os.Stat(filepath.Join(fork, "candidate.txt")); !os.IsNotExist(err) {
					t.Fatalf("earlier candidate file still in staged checkout: %v", err)
				}
			}
			if got := runGit(t, fork, "rev-parse", "HEAD", "refs/heads/main"); got != wantHead+"\n"+wantHead {
				t.Fatalf("checkout HEAD/main = %s, want %s", got, wantHead)
			}
			if got := runGit(t, fork, "rev-parse", ref); got != kept {
				t.Fatalf("retention ref %s moved from %s to %s", ref, kept, got)
			}
			if got := runGit(t, f.root, "rev-parse", "refs/heads/patch/pi"); got != base {
				t.Fatalf("delivery ref moved from %s to %s", base, got)
			}
		})
	}
}

func TestForkCheckoutFreshStagingFastForwardsWithoutRunPin(t *testing.T) {
	f, l, fork, _, base := forkNodeBaseFixture(t)
	const delivery = "refs/heads/patch/pi"
	tip := runGit(t, f.root, "commit-tree", base+"^{tree}", "-p", base, "-m", "new delivery")
	runGit(t, f.root, "update-ref", delivery, tip, base)
	stager := l.cfg.Stage.(*LaneStager)
	if err := stager.refreshForkCheckout(context.Background(), ForkTarget{Checkout: fork, Ref: delivery}); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, fork, "rev-parse", "refs/heads/main"); got != tip {
		t.Errorf("lane main = %s, want delivered tip %s", got, tip)
	}
}

func TestForkCheckoutFreshStagingAfterLandedRebase(t *testing.T) {
	for _, tc := range []struct {
		name, recordedFork string
		extraCommit        bool
		alreadySaved       bool
		wantRefresh        bool
	}{
		{name: "old-series-ancestor", recordedFork: "pi", wantRefresh: true},
		{name: "previously-saved-old-tip", recordedFork: "pi", alreadySaved: true, wantRefresh: true},
		{name: "unlanded-work-above-old-series", recordedFork: "pi", extraCommit: true},
		{name: "other-fork-history", recordedFork: "other"},
		{name: "no-history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, fork, _, base := forkNodeBaseFixture(t)
			const delivery = "refs/heads/patch/pi"
			oldTip := runGit(t, f.root, "commit-tree", base+"^{tree}", "-p", base, "-m", "last commit before rebase")
			newTip := runGit(t, f.root, "commit-tree", base+"^{tree}", "-m", "rebased series")
			runGit(t, f.root, "update-ref", delivery, oldTip, base)
			if tc.recordedFork != "" {
				runGit(t, f.root, "update-ref", "refs/tillr/patch-history/"+tc.recordedFork+"/"+oldTip, oldTip)
			}
			runGit(t, f.root, "update-ref", delivery, newTip, oldTip)
			if tc.extraCommit {
				runGit(t, fork, "fetch", "--quiet", f.root, oldTip)
				runGit(t, fork, "merge", "--ff-only", oldTip)
				runGit(t, fork, "commit", "--allow-empty", "-qm", "unlanded work")
			}
			if tc.alreadySaved {
				runGit(t, fork, "fetch", "--quiet", f.root, oldTip)
				runGit(t, fork, "merge", "--ff-only", oldTip)
				runGit(t, fork, "branch", "kept/pre-rebase-"+oldTip, oldTip)
			}
			head := runGit(t, fork, "rev-parse", "HEAD")
			stager := l.cfg.Stage.(*LaneStager)
			err := stager.refreshForkCheckout(context.Background(), ForkTarget{Checkout: fork, Ref: delivery})
			if tc.wantRefresh {
				if err != nil {
					t.Fatalf("old delivered history refused: %v", err)
				}
				if got := runGit(t, fork, "rev-parse", "HEAD", "refs/heads/main", "refs/heads/kept/pre-rebase-"+head); got != newTip+"\n"+newTip+"\n"+head {
					t.Fatalf("HEAD/main/kept = %q, want delivered tip %s and saved head %s", got, newTip, head)
				}
			} else {
				if _, ok := err.(*forkCheckoutUnretainedError); !ok {
					t.Fatalf("staging = %v, want unretained work refusal", err)
				}
				if got := runGit(t, fork, "rev-parse", "HEAD", "refs/heads/main"); got != head+"\n"+head {
					t.Fatalf("HEAD/main = %q, want untouched %s", got, head)
				}
			}
		})
	}
}

func TestForkCheckoutFreshStagingDoesNotWriteThroughCacheAlias(t *testing.T) {
	f, l, fork, _, _ := forkNodeBaseFixture(t)
	cache := filepath.Join(f.root, ".forks", "pi")
	if err := os.Rename(fork, cache); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cache, fork); err != nil {
		t.Fatal(err)
	}
	runGit(t, cache, "commit", "--allow-empty", "-qm", "cache work")
	head := runGit(t, cache, "rev-parse", "HEAD")
	runGit(t, cache, "update-ref", pinRef("run-cache"), head)
	stager := l.cfg.Stage.(*LaneStager)
	err := stager.refreshForkCheckout(context.Background(), ForkTarget{Checkout: fork, Ref: "refs/heads/patch/pi"})
	if err == nil || !strings.Contains(err.Error(), "aliases another checkout") {
		t.Errorf("staging cache alias: got %v, want alias refusal", err)
	}
	if got := runGit(t, cache, "rev-parse", "HEAD"); got != head {
		t.Errorf("shared cache HEAD moved from %s to %s", head, got)
	}
	if _, err := os.Stat(filepath.Join(cache, ".git", "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Errorf("staging wrote to shared cache: FETCH_HEAD stat: %v", err)
	}
}
