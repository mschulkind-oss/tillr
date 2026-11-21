package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func stagedReplay(t *testing.T) (*pushFixture, remoteSyncState, Candidate) {
	t.Helper()
	p, st := fileDispatchableRemoteSyncRepair(t)
	if err := advancePublished(context.Background(), p.f.root, st.Remote, st.Ref, st.RemoteTip); err != nil {
		t.Fatal(err)
	}
	lane := p.l.cfg.Lanes[0]
	wt := p.l.laneTarget(lane)
	runGit(t, wt, "checkout", "-q", "--detach", st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved two\n")
	runGit(t, wt, "add", "two.txt")
	runGit(t, wt, "commit", "-qm", "resolved two.txt")
	c := Candidate{ItemID: st.ItemID, LaneID: lane, RunID: "run-sync-replay", Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: runGit(t, wt, "rev-parse", "HEAD")}
	gate := &recoveryLandingSteps{t: t, ProcessSteps: &ProcessSteps{Root: p.f.root, Store: p.f.storePath, Now: p.l.cfg.Now}}
	if _, err := gate.Gate(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return p, st, c
}

func landStagedReplay(t *testing.T, p *pushFixture, c Candidate) string {
	t.Helper()
	var result LoopResult
	tip, ok, err := p.l.landRemoteSync(context.Background(), c, &result)
	if err != nil || !ok || tip == "" || result.Landed != 1 {
		t.Fatalf("landRemoteSync = %q, %v, %v; landed %d", tip, ok, err, result.Landed)
	}
	return tip
}

func TestRemoteSyncLandCompletesLinearlyAndHoldsGreen(t *testing.T) {
	p, st, c := stagedReplay(t)
	root := p.f.root
	c4 := ownerCommit(t, root, "four.txt")
	// reset --keep must leave unrelated local edits alone.
	writeForkFile(t, filepath.Join(root, "base.txt"), "unrelated edit\n")
	tip := landStagedReplay(t, p, c)
	if got := runGit(t, root, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("main checkout = %s, want %s", got, tip)
	}
	span := st.RemoteTip + ".." + tip
	if merges, count := runGit(t, root, "rev-list", "--merges", "--count", span), runGit(t, root, "rev-list", "--count", span); merges != "0" || count != "4" {
		t.Fatalf("replay has %s merges and %s commits, want 0 and 4", merges, count)
	}
	if got := runGit(t, root, "rev-parse", GreenRef); got != st.MergeBase {
		t.Fatalf("green = %s, want %s strictly behind the repair", got, st.MergeBase)
	}
	if dirty, err := os.ReadFile(filepath.Join(root, "base.txt")); err != nil || string(dirty) != "unrelated edit\n" {
		t.Fatalf("reset changed unrelated edit: %q, %v", dirty, err)
	}
	fixtureRead(t, p.f, func(r *store.Reader) error {
		for i, old := range append(append([]string{}, p.local...), c4) {
			want := runGit(t, root, "rev-parse", tip+[]string{"~3", "~2", "~1", ""}[i])
			got, err := heavy.RewriteResolve(r, old)
			if err != nil || got != want {
				t.Errorf("rewrite of %s = %s, %v; want %s", old, got, err, want)
			}
		}
		return nil
	})
}

func TestRemoteSyncLandingLeavesLaterConflictUnpublished(t *testing.T) {
	p, st, c := stagedReplay(t)
	root := p.f.root
	writeForkFile(t, filepath.Join(root, "two.txt"), "a later local edit\n")
	runGit(t, root, "commit", "-qam", "later conflict")
	before := runGit(t, root, "rev-parse", TrunkRef)
	var result LoopResult
	if tip, ok, err := p.l.landRemoteSync(context.Background(), c, &result); err == nil || ok || tip != "" || result.Landed != 0 {
		t.Fatalf("landing with a conflicted suffix = %q, %v, %v", tip, ok, err)
	}
	if got := runGit(t, root, "rev-parse", TrunkRef); got != before {
		t.Errorf("later conflict moved main to %s; want %s", got, before)
	}
	if got := runGit(t, root, "rev-parse", remoteSyncPinRef(st.ItemID)); got != st.BaseSHA {
		t.Errorf("later conflict moved the stage to %s; want %s", got, st.BaseSHA)
	}
}

func TestRemoteSyncLandingRejectsVerdictForDifferentTree(t *testing.T) {
	p, st, c := stagedReplay(t)
	root := p.f.root
	writeForkFile(t, filepath.Join(c.Worktree, "two.txt"), "different resolution\n")
	runGit(t, c.Worktree, "commit", "-qam", "changed resolution", "--amend")
	c.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")
	var result LoopResult
	if tip, ok, err := p.l.landRemoteSync(context.Background(), c, &result); err != nil || ok || tip != "" || result.Rejected != 1 {
		t.Fatalf("unbound landing = %q, %v, %v, rejected %d", tip, ok, err, result.Rejected)
	}
	for ref, want := range map[string]string{TrunkRef: st.OldTrunk, GreenRef: p.local[1], remoteSyncPinRef(st.ItemID): st.BaseSHA} {
		if got := runGit(t, root, "rev-parse", ref); got != want {
			t.Errorf("ref %s moved to %s; want %s", ref, got, want)
		}
	}
}

func TestRemoteSyncLandingSettlesRealItemEvidence(t *testing.T) {
	p, st, c := stagedReplay(t)
	bin := filepath.Join(t.TempDir(), "tillr-item")
	declareSiblingInputs(t)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../../tillr-item"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-item: %v: %s", err, out)
	}
	adapter, err := filepath.Abs("../../tillr-loop/tillr-loop")
	if err != nil {
		t.Fatal(err)
	}
	p.l.cfg.Steps = &ProcessSteps{Root: p.f.root, Store: p.f.storePath, Now: p.l.cfg.Now, ItemBin: bin, VerdictProgram: "python3 " + adapter + " verdict --tillr-item " + bin}
	tip := landStagedReplay(t, p, c)
	if got := itemStateOf(t, p.f.storePath, st.ItemID); got != StateLanded {
		t.Fatalf("item state = %s, want landed", got)
	}
	if pin := optionalCommit(context.Background(), p.f.root, remoteSyncPinRef(st.ItemID)); pin != "" {
		t.Fatalf("completed repair kept staging pin %s", pin)
	}
	fixtureRead(t, p.f, func(r *store.Reader) error {
		var head, attributed, tree, landedTree, verdict, replayTip string
		if err := r.QueryRow(`SELECT head_sha,trunk_after,gated_tree,landed_tree,verdict_id,json_extract(e.payload,'$.replay_tip') FROM landings l JOIN events e ON e.seq=l.event_seq WHERE l.item_id=? AND l.run_id=? AND l.result='landed' AND e.kind='land.landing'`, st.ItemID, c.RunID).Scan(&head, &attributed, &tree, &landedTree, &verdict, &replayTip); err != nil {
			return err
		}
		if head != c.HeadSHA || attributed != c.HeadSHA || tree != runGit(t, c.Worktree, "rev-parse", "HEAD^{tree}") || landedTree != tree || verdict != "fixture-verdict" || replayTip != tip {
			t.Errorf("landing evidence = %s %s %s %s %s %s", head, attributed, tree, landedTree, verdict, replayTip)
		}
		return nil
	})
}

func TestRemoteSyncLandingPreservesNewerPublication(t *testing.T) {
	p, st, c := stagedReplay(t)
	ctx := context.Background()
	root := p.f.root
	runGit(t, root, "update-ref", GreenRef, st.MergeBase)
	r2 := p.remoteCommit(t, "remote.txt", "forward remote change\n")
	pub := publishedRef(st.Remote, st.Ref)
	writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[push]\nwhat = \"green\"\n")
	_, _ = PushOnce(ctx, root, p.l.cfg.Store)
	if got := runGit(t, root, "rev-parse", pub); got != r2 {
		t.Fatalf("already-published green confirmed %s; want %s", got, r2)
	}
	tip := landStagedReplay(t, p, c)
	for _, repoRef := range [][2]string{{root, pub}, {p.remote, st.Ref}} {
		if got := runGit(t, repoRef[0], "rev-parse", repoRef[1]); got != r2 {
			t.Fatalf("older repair replaced %s (%s) with %s", repoRef[1], r2, got)
		}
	}
	runGit(t, p.remote, "update-ref", st.Ref, st.RemoteTip, r2)
	runGit(t, root, "update-ref", GreenRef, tip)
	out, err := PushOnce(ctx, root, p.l.cfg.Store)
	if err == nil || out.Kind != pushKindRewritten {
		t.Errorf("PushOnce after rewind = %+v, %v; want remote-rewritten", out, err)
	}
	if got := runGit(t, p.remote, "rev-parse", st.Ref); got != st.RemoteTip {
		t.Errorf("push overwrote the rewind: %s; want %s", got, st.RemoteTip)
	}
	if got := runGit(t, root, "rev-parse", pub); got != r2 {
		t.Errorf("push after rewind changed the published pointer to %s; want %s", got, r2)
	}
}

func TestRemoteSyncHeavyProofAdvancesGreenAndPublishes(t *testing.T) {
	p, st, c := stagedReplay(t)
	ctx := context.Background()
	tip := landStagedReplay(t, p, c)
	if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindProving || ev["rebased_tip"] != tip {
		t.Fatalf("repair did not record the tip awaiting proof: %+v", ev)
	}
	p.l.afterGreen(ctx, st.MergeBase)
	waitAction(t, func() bool { return !p.l.pushAction.busy() })
	if got := runGit(t, p.remote, "rev-parse", st.Ref); got != st.RemoteTip {
		t.Fatalf("old green pushed unproved repair: %s", got)
	}
	if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindProving {
		t.Fatalf("old green cleared the proof hold: %+v", ev)
	}
	p.l.gatePolicy.LandingStrategy = "after-merge"
	p.l.cfg.Steps = heavyTestSteps{Steps: p.l.cfg.Steps, judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "fixture", Result: "pass"}}, nil
	}}
	if err := p.l.maybeRolling(ctx, tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	p.l.heavyWG.Wait()
	waitAction(t, func() bool { return !p.l.pushAction.busy() })
	if got := runGit(t, p.f.root, "rev-parse", GreenRef); got != tip {
		t.Fatalf("proof advanced green to %s, want %s", got, tip)
	}
	if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindOK || ev["remote_tip"] != tip {
		t.Fatalf("proof did not publish: %+v", ev)
	}
	if got := runGit(t, p.remote, "rev-parse", st.Ref); got != tip {
		t.Fatalf("remote tip = %s, want repaired tip %s", got, tip)
	}
}

func TestRemoteSyncTipModePublishesCompletedRepair(t *testing.T) {
	p, st, c := stagedReplay(t)
	p.l.cfg.PostLanding.Push.What = settings.PushTip
	tip := landStagedReplay(t, p, c)
	waitAction(t, func() bool { return !p.l.pushAction.busy() })
	if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindOK || ev["remote_tip"] != tip {
		t.Fatalf("completed tip-mode repair did not publish: %+v", ev)
	}
	if got := runGit(t, p.remote, "rev-parse", st.Ref); got != tip {
		t.Fatalf("remote tip = %s, want repaired tip %s", got, tip)
	}
}
