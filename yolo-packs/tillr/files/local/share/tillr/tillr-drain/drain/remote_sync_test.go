package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRemoteSyncConflictFilesRepairItemAndPins(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	err := p.refused(t)
	_, herr := p.l.healRefusedPush(ctx, settings.Push{}, err)
	syncID := RemoteSyncItemID(remoteTip)
	if herr == nil || !strings.Contains(herr.Error(), "mover: "+syncID) {
		t.Fatalf("heal error = %v, want mover %s", herr, syncID)
	}
	if !IsRemoteSyncItem(syncID) || !IsGeneratedRepair(syncID) || !IsGeneratedItem(syncID) {
		t.Fatalf("%s not recognized as generated repair item", syncID)
	}
	if got := optionalCommit(ctx, p.f.root, publishedRef("origin", "refs/heads/"+TrunkRef)); got != remoteTip {
		t.Fatalf("published ref = %s, want %s", got, remoteTip)
	}
	pin := optionalCommit(ctx, p.f.root, remoteSyncPinRef(syncID))
	if pin == "" {
		t.Fatal("missing P_{k-1} pin ref")
	}
	// P_{k-1} for k=2 (two.txt) should be C_1' on top of remoteTip.
	if runGit(t, p.f.root, "rev-list", "--count", remoteTip+".."+pin) != "1" {
		t.Fatalf("pin %s is not 1 commit ahead of %s", pin, remoteTip)
	}
	allowed, err := p.l.readItemAllowedPaths(ctx, syncID)
	if err != nil || len(allowed) != 1 || allowed[0] != "two.txt" {
		t.Fatalf("allowed_paths = %v, %v", allowed, err)
	}
	facts := remoteSyncBriefFacts(ctx, p.l.cfg.Store, syncID)
	if !strings.Contains(facts, "two.txt") || !strings.Contains(facts, "CONFLICT") || !strings.Contains(facts, "remote two.txt") {
		t.Fatalf("brief facts missing expected context:\n%s", facts)
	}

	// Second heal on same R attaches without duplicating.
	_, herr2 := p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	if herr2 == nil || !strings.Contains(herr2.Error(), syncID) {
		t.Fatalf("second heal error = %v", herr2)
	}

	// A newer R supersedes and closes the older sync-remote-* item.
	remoteTip2 := p.remoteCommit(t, "three.txt", "remote three\n")
	_, herr3 := p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID2 := RemoteSyncItemID(remoteTip2)
	if herr3 == nil || !strings.Contains(herr3.Error(), syncID2) {
		t.Fatalf("third heal error = %v", herr3)
	}
	s, err := store.OpenRead(ctx, p.l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var state1, state2 string
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		_ = r.QueryRow(`SELECT state FROM items WHERE item_id=?`, syncID).Scan(&state1)
		_ = r.QueryRow(`SELECT state FROM items WHERE item_id=?`, syncID2).Scan(&state2)
		return nil
	})
	if state1 != "closed" || state2 != StateInQueue {
		t.Fatalf("states after supersede: %s=%s, %s=%s", syncID, state1, syncID2, state2)
	}
}

func TestRemoteSyncGateOnlyConflictEscalatesToOwner(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	gateFile := "tools/tillr-gate/main.go"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(p.f.root, gateFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(p.f.root, gateFile), "local gate\n")
	runGit(t, p.f.root, "add", gateFile)
	runGit(t, p.f.root, "commit", "-qm", "local gate")

	if err := os.MkdirAll(filepath.Dir(filepath.Join(p.other, gateFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	remoteTip := p.remoteCommit(t, gateFile, "remote gate\n")
	_, herr := p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	se, ok := herr.(*pushStateError)
	if !ok || se.kind != pushKindConflict || se.escalated != "owner" {
		t.Fatalf("heal error = %#v, want conflict escalated to owner", herr)
	}
	syncID := RemoteSyncItemID(remoteTip)
	s, err := store.OpenRead(ctx, p.l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var state, waiting string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state, COALESCE(waiting_on,'') FROM items WHERE item_id=?`, syncID).Scan(&state, &waiting)
	}); err != nil {
		t.Fatal(err)
	}
	if state != StateBlocked || !strings.Contains(waiting, `"owner"`) {
		t.Fatalf("gate-only conflict item state=%q waiting=%q, want blocked/owner", state, waiting)
	}
}

func TestRemoteSyncPreGateTreeCheck(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	_, _ = p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID := RemoteSyncItemID(remoteTip)
	st, ok, err := p.l.readRemoteSyncState(ctx, syncID)
	if err != nil || !ok {
		t.Fatalf("readRemoteSyncState = %v, %v", ok, err)
	}

	wt := filepath.Join(filepath.Dir(p.f.root), "sync-wt")
	runGit(t, p.f.root, "worktree", "add", "-q", "-B", "sync-branch", wt, st.BaseSHA)

	// 1. Out-of-fence alteration fails before gate.
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved\n")
	writeForkFile(t, filepath.Join(wt, "one.txt"), "tampered\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-qm", "tampered")
	badHead := runGit(t, wt, "rev-parse", "HEAD")
	msg, err := p.l.checkRemoteSyncTree(ctx, Candidate{ItemID: syncID, Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: badHead})
	if err != nil || !strings.Contains(msg, "one.txt") {
		t.Fatalf("out-of-fence check = %q, %v", msg, err)
	}

	// 2. Merge commit (2 parents) fails before gate.
	runGit(t, wt, "reset", "--hard", "-q", st.BaseSHA)
	_ = exec.Command("git", "-C", wt, "merge", "--no-ff", "--no-commit", st.ConflictCommit).Run()
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved\n")
	runGit(t, wt, "add", "two.txt")
	runGit(t, wt, "commit", "-qm", "merge commit")
	mergeHead := runGit(t, wt, "rev-parse", "HEAD")
	msg, err = p.l.checkRemoteSyncTree(ctx, Candidate{ItemID: syncID, Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: mergeHead})
	if err != nil || !strings.Contains(msg, "single linear commit") {
		t.Fatalf("merge-commit check = %q, %v", msg, err)
	}

	// 3. Clean linear resolution atop P_{k-1} passes.
	runGit(t, wt, "reset", "--hard", "-q", st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved\n")
	runGit(t, wt, "add", "two.txt")
	runGit(t, wt, "commit", "-qm", "resolved two.txt")
	goodHead := runGit(t, wt, "rev-parse", "HEAD")
	msg, err = p.l.checkRemoteSyncTree(ctx, Candidate{ItemID: syncID, Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: goodHead})
	if err != nil || msg != "" {
		t.Fatalf("clean resolution check = %q, %v", msg, err)
	}
}

func TestRemoteSyncLandCompletesLinearlyAndHoldsGreen(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	// Put green on local[1] (two.txt, which IS C_k) to verify green is held strictly behind C_k' (at B).
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	_, _ = p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID := RemoteSyncItemID(remoteTip)
	st, _, _ := p.l.readRemoteSyncState(ctx, syncID)

	// Concurrent local landing C_4 arrives while sync-remote runs.
	c4 := ownerCommit(t, p.f.root, "four.txt")

	wt := filepath.Join(filepath.Dir(p.f.root), "sync-wt-land")
	runGit(t, p.f.root, "worktree", "add", "-q", "-B", "sync-land", wt, st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved two\n")
	runGit(t, wt, "add", "two.txt")
	runGit(t, wt, "commit", "-qm", "resolved two.txt")
	ckPrime := runGit(t, wt, "rev-parse", "HEAD")

	var result LoopResult
	tip, ok, err := p.l.landRemoteSync(ctx, Candidate{ItemID: syncID, Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: ckPrime}, &result, nil)
	if err != nil || !ok || tip == "" {
		t.Fatalf("landRemoteSync = %q, %v, %v", tip, ok, err)
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != tip {
		t.Fatalf("main HEAD = %s, want %s", runGit(t, p.f.root, "rev-parse", "HEAD"), tip)
	}
	if runGit(t, p.f.root, "rev-list", "--merges", "--count", remoteTip+".."+tip) != "0" {
		t.Fatal("rebased trunk has merge commits")
	}
	if runGit(t, p.f.root, "rev-list", "--count", remoteTip+".."+tip) != "4" {
		t.Fatalf("expected 4 replayed commits atop %s, got %s", remoteTip, runGit(t, p.f.root, "rev-list", "--count", remoteTip+".."+tip))
	}
	green := runGit(t, p.f.root, "rev-parse", GreenRef)
	if green != st.MergeBase {
		t.Fatalf("green = %s, want merge-base %s (strictly behind C_k')", green, st.MergeBase)
	}
	// Verify trunk_rewrites recorded C_4 -> C_4' and C_2 -> C_2'.
	s, err := store.OpenRead(ctx, p.l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		resolvedCk, err := heavy.RewriteResolve(r, st.ConflictCommit)
		if err != nil || resolvedCk != ckPrime {
			t.Fatalf("RewriteResolve(C_k) = %s, %v; want %s", resolvedCk, err, ckPrime)
		}
		resolvedC4, err := heavy.RewriteResolve(r, c4)
		if err != nil || resolvedC4 == c4 || resolvedC4 != tip {
			t.Fatalf("RewriteResolve(C_4) = %s, %v; want %s", resolvedC4, err, tip)
		}
		return nil
	})
}

func TestRemoteSyncLaterConflictAdvancesBase(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	// Remote modifies two.txt AND three.txt.
	writeForkFile(t, filepath.Join(p.other, "two.txt"), "remote two\n")
	writeForkFile(t, filepath.Join(p.other, "three.txt"), "remote three\n")
	runGit(t, p.other, "add", ".")
	runGit(t, p.other, "commit", "-qm", "remote two and three")
	runGit(t, p.other, "push", "-q", "origin", TrunkRef)
	remoteTip := runGit(t, p.other, "rev-parse", "HEAD")

	_, _ = p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID := RemoteSyncItemID(remoteTip)
	st, _, _ := p.l.readRemoteSyncState(ctx, syncID)

	wt := filepath.Join(filepath.Dir(p.f.root), "sync-wt-adv")
	runGit(t, p.f.root, "worktree", "add", "-q", "-B", "sync-adv", wt, st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved two\n")
	runGit(t, wt, "add", "two.txt")
	runGit(t, wt, "commit", "-qm", "resolved two.txt")
	ckPrime := runGit(t, wt, "rev-parse", "HEAD")
	mainBefore := runGit(t, p.f.root, "rev-parse", "HEAD")

	var result LoopResult
	_, ok, err := p.l.landRemoteSync(ctx, Candidate{ItemID: syncID, RunID: "run-sync-1", Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: ckPrime}, &result, nil)
	if err != nil || ok {
		t.Fatalf("landRemoteSync on later conflict = %v, %v", ok, err)
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != mainBefore {
		t.Fatal("main moved despite later conflict C_m")
	}
	st2, found, err := p.l.readRemoteSyncState(ctx, syncID)
	if err != nil || !found {
		t.Fatalf("readRemoteSyncState = %v, %v", found, err)
	}
	if st2.BaseSHA != ckPrime || len(st2.ConflictPaths) != 1 || st2.ConflictPaths[0] != "three.txt" {
		t.Fatalf("advanced state = %+v", st2)
	}
}

func TestRemoteSyncObstacleEscalation(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	_, _ = p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID := RemoteSyncItemID(remoteTip)

	// Exempt from trunkAlreadyRed.
	reading, err := p.l.trunkAlreadyRed(ctx, Candidate{ItemID: syncID}, GateOutcome{FailingChecks: []string{"test"}})
	if err != nil || reading.Confirmed {
		t.Fatalf("trunkAlreadyRed for sync-remote = %+v, %v", reading, err)
	}

	// Escalate push state to operator.
	p.l.escalateRemoteSyncPushState(ctx, syncID, "operator", "repeated ending")
	s, err := store.OpenRead(ctx, p.l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var raw string
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`, postLandingKind).Scan(&raw)
	})
	var got struct {
		Kind           string `json:"kind"`
		EscalatedMover string `json:"escalated_mover"`
		Detail         string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != pushKindConflict || got.EscalatedMover != "operator" || !strings.Contains(got.Detail, "mover: operator; rebase main onto origin/main") {
		t.Fatalf("escalated push state = %+v", got)
	}
}

func TestRemoteSyncPushOnceAndHoldClearance(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	writeForkFile(t, filepath.Join(p.f.root, ".tillr.toml"), "[push]\nwhat = \"tip\"\n")
	runGit(t, p.f.root, "add", ".tillr.toml")
	runGit(t, p.f.root, "commit", "-qm", "add tillr.toml")
	remoteTip := p.remoteCommit(t, "remote.txt", "remote\n")

	out, err := PushOnce(ctx, p.f.root, p.l.cfg.Store)
	if err != nil || !out.OK || out.Kind != pushKindOK {
		t.Fatalf("PushOnce = %+v, %v", out, err)
	}
	if runGit(t, p.other, "ls-remote", "origin", "refs/heads/"+TrunkRef) == remoteTip {
		t.Fatal("remote did not advance")
	}

	// Simulate dirty-checkout clearance recovery.
	p.l.cfg.PostLanding.Push.What = settings.PushTip
	p.l.recordPush(ctx, settings.PushTip, out.Tip, &pushStateError{kind: pushKindWorktreeDirty, detail: "dirty"})
	if status := runGit(t, p.f.root, "status", "--porcelain"); status != "" {
		os.Remove(filepath.Join(p.f.root, ".tillr.toml"))
	}
	p.l.maybeRecoverClearedPushHold(ctx)
	for p.l.pushAction.busy() {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRemoteSyncDirtyWorktreeHolds(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	_, _ = p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID := RemoteSyncItemID(remoteTip)
	st, _, _ := p.l.readRemoteSyncState(ctx, syncID)

	wt := filepath.Join(filepath.Dir(p.f.root), "sync-wt-dirty")
	runGit(t, p.f.root, "worktree", "add", "-q", "-B", "sync-dirty", wt, st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved two\n")
	runGit(t, wt, "add", "two.txt")
	runGit(t, wt, "commit", "-qm", "resolved two.txt")
	ckPrime := runGit(t, wt, "rev-parse", "HEAD")

	// Uncommitted edit in main checkout on a clashing path (two.txt) blocks git reset --keep.
	writeForkFile(t, filepath.Join(p.f.root, "two.txt"), "uncommitted local edit\n")
	mainBefore := runGit(t, p.f.root, "rev-parse", "HEAD")

	var result LoopResult
	_, ok, err := p.l.landRemoteSync(ctx, Candidate{ItemID: syncID, RunID: "run-sync-dirty", Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: ckPrime}, &result, nil)
	if ok || err == nil {
		t.Fatalf("landRemoteSync on dirty checkout = (%v, %v), want (false, non-nil)", ok, err)
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != mainBefore {
		t.Fatal("main moved despite dirty clashing checkout")
	}
	ev := lastPushEvent(t, p.f)
	if ev["kind"] != pushKindWorktreeDirty {
		t.Fatalf("push event kind = %v, want %s (%+v)", ev["kind"], pushKindWorktreeDirty, ev)
	}
}

func TestRemoteSyncHeavyProofAdvancesGreenAndPublishes(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	_, _ = p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID := RemoteSyncItemID(remoteTip)
	st, _, _ := p.l.readRemoteSyncState(ctx, syncID)

	wt := filepath.Join(filepath.Dir(p.f.root), "sync-wt-proof")
	runGit(t, p.f.root, "worktree", "add", "-q", "-B", "sync-proof", wt, st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "two.txt"), "resolved two\n")
	runGit(t, wt, "add", "two.txt")
	runGit(t, wt, "commit", "-qm", "resolved two.txt")
	ckPrime := runGit(t, wt, "rev-parse", "HEAD")

	var result LoopResult
	tip, ok, err := p.l.landRemoteSync(ctx, Candidate{ItemID: syncID, Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: ckPrime}, &result, nil)
	if err != nil || !ok || tip == "" {
		t.Fatalf("landRemoteSync = %q, %v, %v", tip, ok, err)
	}
	// Green is strictly behind C_k'.
	if got := runGit(t, p.f.root, "rev-parse", GreenRef); got != st.MergeBase {
		t.Fatalf("green before proof = %s, want %s", got, st.MergeBase)
	}
	// Heavy proof passes on tip, advances refs/tillr/green, and pushGreen publishes tip.
	runGit(t, p.f.root, "update-ref", GreenRef, tip)
	gotGreen, err := pushGreen(ctx, p.f.root, settings.Push{})
	if err != nil || gotGreen != tip {
		t.Fatalf("pushGreen after proof = (%s, %v), want (%s, nil)", gotGreen, err, tip)
	}
	if got := optionalCommit(ctx, p.f.root, publishedRef("origin", "refs/heads/"+TrunkRef)); got != tip {
		t.Fatalf("published ref = %s, want %s", got, tip)
	}
}

func TestRemoteSyncFenceSeatAndOwnerObstacleEscalation(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	_, _ = p.l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	syncID := RemoteSyncItemID(remoteTip)

	// 1. fence obstacle on non-gate path widens allowed_paths and returns to worker.
	var res LoopResult
	out, handled := p.l.routeObstacle(ctx, Decision{ItemID: syncID, RunRequest: &run.Request{RunID: "run-f1", ItemID: syncID}},
		run.Request{RunID: "run-f1", ItemID: syncID}, "lane-1", "t1", remoteTip,
		Obstacle{Kind: ObstacleFence, Fields: map[string]string{"PATHS": "three.txt", "EVIDENCE": "needed"}}, &res)
	if !handled || out.stop != "" {
		t.Fatalf("fence routeObstacle = (%+v, %v), want handled returnToWorker", out, handled)
	}
	allowed, err := p.l.readItemAllowedPaths(ctx, syncID)
	if err != nil || len(allowed) != 2 {
		t.Fatalf("widened allowed_paths = %v, %v", allowed, err)
	}

	// 2. fence obstacle requesting GateCodePaths is refused by routeObstacle and escalates to owner via reportedBlock.
	_, handledGate := p.l.routeObstacle(ctx, Decision{ItemID: syncID, RunRequest: &run.Request{RunID: "run-f2", ItemID: syncID}},
		run.Request{RunID: "run-f2", ItemID: syncID}, "lane-1", "t2", remoteTip,
		Obstacle{Kind: ObstacleFence, Fields: map[string]string{"PATHS": "tools/tillr-gate/main.go", "EVIDENCE": "gate"}}, &res)
	if handledGate {
		t.Fatal("expected GateCodePaths fence request not to auto-widen")
	}
	if err := p.l.reportedBlock(ctx, Decision{ItemID: syncID, RunRequest: &run.Request{RunID: "run-f2", ItemID: syncID}},
		"OBSTACLE: fence\nPATHS: tools/tillr-gate/main.go\nEVIDENCE: gate\nRESULT: blocked\n"); err != nil {
		t.Fatalf("reportedBlock(fence): %v", err)
	}
	ev := lastPushEvent(t, p.f)
	if ev["kind"] != pushKindConflict || ev["escalated_mover"] != "owner" {
		t.Fatalf("push event after gate-code fence = %+v, want owner escalation", ev)
	}

	// 3. seat obstacle (when no stronger seat is available / repeated) escalates via reportedBlock.
	if err := p.l.reportedBlock(ctx, Decision{ItemID: syncID, RunRequest: &run.Request{RunID: "run-s1", ItemID: syncID}},
		"OBSTACLE: seat\nTRIED: x\nMISSING: stronger model\nRESULT: blocked\n"); err != nil {
		t.Fatalf("reportedBlock(seat): %v", err)
	}

	// 4. owner obstacle escalates push state to owner.
	if err := p.l.reportedBlock(ctx, Decision{ItemID: syncID, RunRequest: &run.Request{RunID: "run-o1", ItemID: syncID}},
		"OBSTACLE: owner\nQUESTION: which value wins?\nRESULT: blocked\n"); err != nil {
		t.Fatalf("reportedBlock(owner): %v", err)
	}
	evOwner := lastPushEvent(t, p.f)
	if evOwner["kind"] != pushKindConflict || evOwner["escalated_mover"] != "owner" {
		t.Fatalf("push event after owner obstacle = %+v, want owner escalation", evOwner)
	}
}
