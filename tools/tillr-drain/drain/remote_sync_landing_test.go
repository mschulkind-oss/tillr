package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func stagedReplay(t *testing.T) (*pushFixture, remoteSyncState, Candidate) {
	t.Helper()
	p, st := fileDispatchableRemoteSyncRepair(t)
	return p, st, gateFirstRemoteSyncRepair(t, p, st)
}

func gateFirstRemoteSyncRepair(t *testing.T, p *pushFixture, st remoteSyncState) Candidate {
	t.Helper()
	if err := advancePublished(context.Background(), p.f.root, st.Remote, st.Ref, st.RemoteTip); err != nil {
		t.Fatal(err)
	}
	lane := p.l.cfg.Lanes[0]
	wt := p.l.laneTarget(lane)
	runGit(t, wt, "checkout", "-q", "--detach", st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved two\n")
	runGit(t, wt, "add", "two.txt")
	tree := runGit(t, wt, "write-tree")
	cmd := exec.Command("git", "-C", wt, "-c", "i18n.commitEncoding=ISO-8859-1", "commit-tree", tree, "-p", st.BaseSHA)
	cmd.Stdin = strings.NewReader("caf\xe9: resolved two.txt") // Latin-1, intentionally no final newline
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Worker", "GIT_AUTHOR_EMAIL=worker@example.invalid",
		"GIT_COMMITTER_NAME=Worker", "GIT_COMMITTER_EMAIL=worker@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("creating worker commit: %v: %s", err, out)
	}
	head := strings.TrimSpace(string(out))
	runGit(t, wt, "update-ref", "HEAD", head, st.BaseSHA)
	c := Candidate{ItemID: st.ItemID, LaneID: lane, RunID: "run-sync-replay", Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: head}
	gateRemoteSyncRepair(t, p, c)
	return c
}

func gateRemoteSyncRepair(t *testing.T, p *pushFixture, c Candidate) {
	t.Helper()
	gate := &recoveryLandingSteps{t: t, ProcessSteps: &ProcessSteps{Root: p.f.root, Store: p.f.storePath, Now: p.l.cfg.Now}}
	if _, err := gate.Gate(context.Background(), c); err != nil {
		t.Fatal(err)
	}
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

func rawRepairMessage(t *testing.T, root, commit string) (message, encoding string) {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "cat-file", "commit", commit).Output()
	if err != nil {
		t.Fatal(err)
	}
	header, message, ok := strings.Cut(string(out), "\n\n")
	if !ok {
		t.Fatalf("commit %s has no message boundary", commit)
	}
	for _, line := range strings.Split(header, "\n") {
		if value, found := strings.CutPrefix(line, "encoding "); found {
			encoding = value
		}
	}
	return message, encoding
}

func TestRemoteSyncLandingRestampsWorker(t *testing.T) {
	p, st, c := stagedReplay(t)
	root := p.f.root
	tip := landStagedReplay(t, p, c)
	fixtureRead(t, p.f, func(r *store.Reader) error {
		repair, err := heavy.RewriteResolve(r, st.ConflictCommit)
		if err != nil {
			return err
		}
		if repair == c.HeadSHA || runGit(t, root, "merge-base", repair, tip) != repair || runGit(t, root, "merge-base", c.HeadSHA, tip) == c.HeadSHA {
			t.Fatalf("published replay %s retains worker commit %s instead of a restamped repair (%s)", tip, c.HeadSHA, repair)
		}
		if got := runGit(t, root, "show", "-s", "--format=%an <%ae>|%cn <%ce>", repair); got != "tillr <tillr@example.invalid>|tillr <tillr@example.invalid>" {
			t.Fatalf("repair author and committer = %q", got)
		}
		if got, want := runGit(t, root, "show", "-s", "--format=%P|%T|%aI", repair), runGit(t, root, "show", "-s", "--format=%P|%T|%aI", c.HeadSHA); got != want {
			t.Fatalf("stamping changed the gated repair's parent, tree or author date: %q != %q", got, want)
		}
		want, wantEncoding := rawRepairMessage(t, root, c.HeadSHA)
		if want != "caf\xe9: resolved two.txt" || wantEncoding != "ISO-8859-1" {
			t.Fatalf("unexpected fixture message: %q (%s)", want, wantEncoding)
		}
		if message, encoding := rawRepairMessage(t, root, repair); message != want || encoding != wantEncoding {
			t.Fatalf("repair changed message: %q (%s), want %q (%s)", message, encoding, want, wantEncoding)
		}
		return nil
	})
}

func TestRemoteSyncLandCompletesLinearlyAndHoldsGreen(t *testing.T) {
	p, st, c := stagedReplay(t)
	root := p.f.root
	c4 := ownerCommit(t, root, "four.txt")
	// reset --keep must leave unrelated local edits alone.
	writeForkFile(t, filepath.Join(root, "base.txt"), "unrelated edit\n")
	setReplayIdentity(t, root)
	tip := landStagedReplay(t, p, c)
	assertReplayIdentity(t, root, p.local[2], tip+"~1")
	assertReplayIdentity(t, root, c4, tip)
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

func stagedRemoteSyncWithLaterConflict(t *testing.T) (*pushFixture, remoteSyncState, Candidate) {
	t.Helper()
	p := newPushFixture(t)
	root := p.f.root
	p.remoteCommit(t, "two.txt", "remote two\n")
	remote := p.remoteCommit(t, "three.txt", "remote three\n")
	runGit(t, root, "fetch", "-q", "origin")
	parent := runGit(t, root, "rev-parse", p.local[0]+"^")
	tree := runGit(t, root, "merge-tree", "--write-tree", "--merge-base="+parent, remote, p.local[0])
	base := runGit(t, root, "commit-tree", tree, "-p", remote, "-m", "replay one.txt")
	st := remoteSyncFilingFixture(t, p, base, remote, p.local[1])
	st.ReplayedOrder = []string{p.local[0]}
	st.ReplayedPairs = map[string]string{p.local[0]: base}
	if _, err := p.l.fileOrUpdateRemoteSyncItem(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["two.txt","base.txt"]' WHERE item_id=?`, st.ItemID)
		return err
	})
	return p, st, gateFirstRemoteSyncRepair(t, p, st)
}

func assertRemoteSyncSecondStage(t *testing.T, p *pushFixture, c Candidate) remoteSyncState {
	t.Helper()
	st, found, err := p.l.readRemoteSyncState(context.Background(), c.ItemID)
	if err != nil || !found || st.BaseSHA == c.HeadSHA || st.ConflictCommit != p.local[2] || !slices.Equal(st.ConflictPaths, []string{"three.txt"}) {
		t.Fatalf("second stage = %+v, %v, %v", st, found, err)
	}
	if got, want := runGit(t, p.f.root, "rev-parse", st.BaseSHA+"^{tree}"), runGit(t, p.f.root, "rev-parse", c.HeadSHA+"^{tree}"); got != want {
		t.Fatalf("second stage base tree = %s, want first gated tree %s", got, want)
	}
	for ref, want := range map[string]string{TrunkRef: p.local[2], GreenRef: p.local[1], remoteSyncPinRef(c.ItemID): st.BaseSHA} {
		if got := runGit(t, p.f.root, "rev-parse", ref); got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
	return st
}

func TestRemoteSyncLaterConflictAdvancesBase(t *testing.T) {
	ctx := context.Background()
	p, first, c := stagedRemoteSyncWithLaterConflict(t)
	var result LoopResult
	if tip, ok, err := p.l.landRemoteSync(ctx, c, &result); err != nil || ok || tip != "" || result.Landed != 0 || result.RefusedSteps[RefusalLand] != 1 {
		t.Fatalf("landing with a conflicted suffix = %q, %v, %v; result %+v", tip, ok, err, result)
	}
	st := assertRemoteSyncSecondStage(t, p, c)
	// A repeated first conflict from the push path must not replace the new step.
	if _, err := p.l.fileOrUpdateRemoteSyncItem(ctx, first); err != nil {
		t.Fatal(err)
	}
	assertRemoteSyncSecondStage(t, p, c)

	wt := c.Worktree
	runGit(t, wt, "checkout", "--detach", st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "three.txt"), "resolved three\n")
	writeForkFile(t, filepath.Join(wt, "two.txt"), "undo the first resolution\n")
	runGit(t, wt, "add", "two.txt", "three.txt")
	runGit(t, wt, "commit", "-qm", "resolved three.txt")
	checkRemoteSyncCandidate(t, p, st, wt, "two.txt\nthree.txt\n", "altered non-conflicting path(s) relative to the mechanical merge: two.txt")
	runGit(t, wt, "restore", "--source=HEAD^", "--staged", "--worktree", "--", "two.txt")
	runGit(t, wt, "commit", "--amend", "--no-edit", "-q")
	checkRemoteSyncCandidate(t, p, st, wt, "three.txt\n", "")
	second := Candidate{ItemID: st.ItemID, RunID: "run-sync-replay-second", LaneID: c.LaneID, Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: runGit(t, wt, "rev-parse", "HEAD")}
	gateRemoteSyncRepair(t, p, second)
	landStagedReplay(t, p, second)
	if got := runGit(t, p.f.root, "rev-parse", GreenRef); got != st.MergeBase {
		t.Fatalf("green crossed the first resolution: %s, want %s", got, st.MergeBase)
	}
}

func TestRemoteSyncLandingRestampsLegacyStage(t *testing.T) {
	ctx := context.Background()
	p, first, worker := stagedRemoteSyncWithLaterConflict(t)
	root := p.f.root
	trunk, err := land.OpenTrunk(root)
	if err != nil {
		t.Fatal(err)
	}
	// The old implementation staged the raw worker commit as the base of
	// the next conflict instead of stamping it first.
	_, legacy, err := replayRemoteSyncSuffix(ctx, trunk, root, first, worker.HeadSHA, first.OldTrunk)
	if err != nil || legacy == nil || legacy.BaseSHA != worker.HeadSHA {
		t.Fatalf("legacy stage = %+v, %v", legacy, err)
	}
	if _, err := p.l.fileOrUpdateRemoteSyncItemLocked(ctx, *legacy, first.BaseSHA); err != nil {
		t.Fatal(err)
	}
	wt := worker.Worktree
	runGit(t, wt, "checkout", "--detach", legacy.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "three.txt"), "resolved three\n")
	runGit(t, wt, "add", "three.txt")
	runGit(t, wt, "commit", "-qm", "resolved three.txt")
	second := Candidate{ItemID: legacy.ItemID, RunID: "run-legacy-replay-second", LaneID: worker.LaneID,
		Worktree: wt, BaseSHA: legacy.BaseSHA, HeadSHA: runGit(t, wt, "rev-parse", "HEAD")}
	gateRemoteSyncRepair(t, p, second)
	setReplayIdentity(t, root)
	tip := landStagedReplay(t, p, second)
	if got := runGit(t, root, "merge-base", worker.HeadSHA, tip); got == worker.HeadSHA {
		t.Fatalf("published trunk %s retains legacy worker commit %s", tip, worker.HeadSHA)
	}
	fixtureRead(t, p.f, func(r *store.Reader) error {
		for i, old := range p.local {
			want := runGit(t, root, "rev-parse", tip+[]string{"~2", "^", ""}[i])
			got, err := heavy.RewriteResolve(r, old)
			if err != nil || got != want {
				t.Fatalf("rewrite of %s = %s, %v; want %s", old, got, err, want)
			}
			if i == 0 {
				assertReplayIdentity(t, root, first.BaseSHA, got)
				continue
			}
			if ident := runGit(t, root, "show", "-s", "--format=%an <%ae>|%cn <%ce>", got); ident != "Replay Lander <replay@example.invalid>|Replay Lander <replay@example.invalid>" {
				t.Fatalf("repair %s identity = %q", got, ident)
			}
			head := []string{"", worker.HeadSHA, second.HeadSHA}[i]
			if gotMeta, want := runGit(t, root, "show", "-s", "--format=%T|%aI", got), runGit(t, root, "show", "-s", "--format=%T|%aI", head); gotMeta != want {
				t.Fatalf("restamping changed gated repair tree or author date: %q != %q", gotMeta, want)
			}
			want, wantEncoding := rawRepairMessage(t, root, head)
			if message, encoding := rawRepairMessage(t, root, got); message != want || encoding != wantEncoding {
				t.Fatalf("restamping changed gated repair message: %q (%s), want %q (%s)", message, encoding, want, wantEncoding)
			}
		}
		return nil
	})
	if got := runGit(t, root, "rev-parse", tip+"~3"); got != first.RemoteTip {
		t.Fatalf("reconstructed prefix changed fetched history: %s != %s", got, first.RemoteTip)
	}
}

func TestRemoteSyncLaterConflictRefusesRetainedCandidateAfterInterruption(t *testing.T) {
	ctx := context.Background()
	p, _, c := stagedRemoteSyncWithLaterConflict(t)
	// Lose the first refusal write after the new stage and fence are durable.
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER interrupt_remote_sync_refusal BEFORE INSERT ON events
			WHEN NEW.kind='drain.refusal' AND NEW.object_id='run-sync-replay'
			BEGIN SELECT RAISE(ABORT,'fixture interrupted refusal'); END`)
		return err
	})
	var interrupted LoopResult
	if tip, ok, err := p.l.landRemoteSync(ctx, c, &interrupted); err == nil || !strings.Contains(err.Error(), "fixture interrupted refusal") || ok || tip != "" {
		t.Fatalf("interrupted landing = %q, %v, %v", tip, ok, err)
	}
	st := assertRemoteSyncSecondStage(t, p, c)
	if refused, err := p.l.concludedByRefusal(ctx, c.RunID); err != nil || refused {
		t.Fatalf("interrupted refusal recorded = %v, %v", refused, err)
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER interrupt_remote_sync_refusal`)
		return err
	})
	for _, base := range []string{c.BaseSHA, st.BaseSHA} {
		// On retry the retained candidate is still gated for the first step.
		// Rewriting its advertised base alone cannot make its head a repair either.
		retry := c
		retry.BaseSHA = base
		var result LoopResult
		if tip, ok, err := p.l.landRemoteSync(ctx, retry, &result); err != nil || ok || tip != "" || result.Landed != 0 || result.RefusedSteps[RefusalLand] != 1 {
			t.Fatalf("retained landing at base %s = %q, %v, %v; result %+v", base, tip, ok, err, result)
		}
		assertRemoteSyncSecondStage(t, p, c)
	}
	if refused, err := p.l.concludedByRefusal(ctx, c.RunID); err != nil || !refused {
		t.Fatalf("retained refusal recorded = %v, %v", refused, err)
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
	for _, phase := range []string{"uninterrupted", "interrupted", "newer-conflict"} {
		t.Run(phase, func(t *testing.T) { testRemoteSyncLandingEvidence(t, phase) })
	}
}

func testRemoteSyncLandingEvidence(t *testing.T, phase string) {
	ctx := context.Background()
	p, st, c := stagedReplay(t)
	bin := filepath.Join(t.TempDir(), "tillr-item")
	declareSiblingInputs(t)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../../tillr-item"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-item: %v: %s", err, out)
	}
	p.l.cfg.Steps = &ProcessSteps{Root: p.f.root, Store: p.f.storePath, Now: p.l.cfg.Now, ItemBin: bin, VerdictProgram: "tillr-loop verdict --tillr-item " + bin}
	var tip string
	if phase == "uninterrupted" {
		tip = landStagedReplay(t, p, c)
	} else {
		fixtureWrite(t, p.f, func(tx *store.Tx) error {
			_, err := tx.Exec(`CREATE TRIGGER stop_sync BEFORE INSERT ON landings BEGIN SELECT RAISE(ABORT, 'interrupted replay'); END`)
			return err
		})
		var result LoopResult
		if _, ok, err := p.l.landRemoteSync(ctx, c, &result); ok || err == nil || !strings.Contains(err.Error(), "interrupted replay") {
			t.Fatalf("interrupted landing = %v, %v", ok, err)
		}
		tip = runGit(t, p.f.root, "rev-parse", TrunkRef)
		fixtureRead(t, p.f, func(r *store.Reader) error {
			got, err := heavy.RewriteResolve(r, st.ConflictCommit)
			if err != nil || got != runGit(t, p.f.root, "rev-parse", tip+"^") {
				t.Fatalf("interrupted replay did not record the stamped repair: %s, %v", got, err)
			}
			return nil
		})
		fixtureWrite(t, p.f, func(tx *store.Tx) error { _, err := tx.Exec(`DROP TRIGGER stop_sync`); return err })
		var newerID string
		if phase == "newer-conflict" {
			remote := p.remoteCommit(t, "two.txt", "newer remote edit\n")
			newerID = RemoteSyncItemID(remote)
			writeForkFile(t, filepath.Join(p.f.root, ".tillr.toml"), "[push]\nwhat = \"tip\"\n")
			if out, err := PushOnce(ctx, p.f.root, p.l.cfg.Store); err == nil || out.Kind != pushKindConflict || !strings.Contains(out.Detail, newerID) {
				t.Fatalf("manual push did not file the newer conflict: %+v, %v", out, err)
			}
			if got := itemStateOf(t, p.f.storePath, st.ItemID); got != StateLanded {
				t.Fatalf("manual push left the interrupted repair %s", got)
			}
		}
		restarted := &Loop{cfg: p.l.cfg}
		if _, err := restarted.passMode(ctx, &result); err != nil {
			t.Fatal(err)
		}
		if got := runGit(t, p.f.root, "rev-parse", TrunkRef); got != tip {
			t.Fatalf("recovery moved main to %s, want %s", got, tip)
		}
		if newerID == "" && result.Landed != 1 || newerID != "" && result.Landed != 0 {
			t.Fatalf("next pass counted %d landings after %s", result.Landed, phase)
		}
		if newerID != "" {
			if state := itemStateOf(t, p.f.storePath, newerID); state != StateInQueue {
				t.Fatalf("newer repair is %s, want queued", state)
			}
			if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindConflict || !strings.Contains(ev["detail"].(string), newerID) {
				t.Fatalf("next pass hid the newer conflict: %+v", ev)
			}
		}
	}
	if got := itemStateOf(t, p.f.storePath, st.ItemID); got != StateLanded {
		t.Fatalf("item state = %s, want landed", got)
	}
	if pin := optionalCommit(context.Background(), p.f.root, remoteSyncPinRef(st.ItemID)); pin != "" {
		t.Fatalf("completed repair kept staging pin %s", pin)
	}
	if replay, err := p.l.readRemoteSyncReplay(ctx); err != nil || replay != nil {
		t.Fatalf("settled repair kept its intent: %+v, %v", replay, err)
	}
	fixtureRead(t, p.f, func(r *store.Reader) error {
		var state, head, attributed, tree, landedTree, verdict, replayTip string
		var receipts int
		if err := r.QueryRow(`SELECT state FROM item_criteria WHERE item_id=? AND criterion_id=?`, st.ItemID, RemoteSyncCriterion).Scan(&state); err != nil {
			return err
		}
		if state != "done" {
			t.Errorf("repair criterion is %s, want done", state)
		}
		if err := r.QueryRow(`SELECT head_sha,trunk_after,gated_tree,landed_tree,verdict_id,json_extract(e.payload,'$.replay_tip'),count(*) OVER () FROM landings l JOIN events e ON e.seq=l.event_seq WHERE l.item_id=? AND l.run_id=? AND l.result='landed' AND e.kind='land.landing'`, st.ItemID, c.RunID).Scan(&head, &attributed, &tree, &landedTree, &verdict, &replayTip, &receipts); err != nil {
			return err
		}
		if receipts != 1 {
			t.Errorf("repair recorded %d landing receipts, want one", receipts)
		}
		if head != c.HeadSHA || attributed != runGit(t, p.f.root, "rev-parse", tip+"^") || tree != runGit(t, c.Worktree, "rev-parse", "HEAD^{tree}") || landedTree != tree || verdict != "fixture-verdict" || replayTip != tip {
			t.Errorf("landing evidence = %s %s %s %s %s %s", head, attributed, tree, landedTree, verdict, replayTip)
		}
		return nil
	})
}

func TestRemoteSyncSupersededStageCannotResetMain(t *testing.T) {
	p, st, c := stagedReplay(t)
	ctx := context.Background()
	remote := p.remoteCommit(t, "two.txt", "newer remote edit\n")
	runGit(t, p.f.root, "fetch", "-q", "origin")
	newer := remoteSyncFilingFixture(t, p, remote, remote, st.ConflictCommit)
	if _, err := p.l.fileOrUpdateRemoteSyncItem(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if got := itemStateOf(t, p.f.storePath, st.ItemID); got != "closed" {
		t.Fatalf("superseded stage is %s, want closed", got)
	}
	before := runGit(t, p.f.root, "rev-parse", TrunkRef)
	if _, ok, err := p.l.landRemoteSync(ctx, c, &LoopResult{}); ok || asItemTerminal(err) == nil {
		t.Fatalf("stale candidate landed=%v, err=%v", ok, err)
	}
	if got := runGit(t, p.f.root, "rev-parse", TrunkRef); got != before {
		t.Fatalf("stale candidate reset main from %s to %s", before, got)
	}
	if replay, err := p.l.readRemoteSyncReplay(ctx); err != nil || replay != nil {
		t.Fatalf("stale candidate wrote an intent: %+v, %v", replay, err)
	}
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
	waitAction(t, &p.l.pushAction)
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
	waitAction(t, &p.l.pushAction)
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
	waitAction(t, &p.l.pushAction)
	if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindOK || ev["remote_tip"] != tip {
		t.Fatalf("completed tip-mode repair did not publish: %+v", ev)
	}
	if got := runGit(t, p.remote, "rev-parse", st.Ref); got != tip {
		t.Fatalf("remote tip = %s, want repaired tip %s", got, tip)
	}
}
