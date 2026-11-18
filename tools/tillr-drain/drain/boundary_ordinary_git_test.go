package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// lastBoundaryCheck answers the payload of the newest boundary.check for objectID.
func lastBoundaryCheck(t *testing.T, f refusalLoopFixture, objectID string) map[string]any {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var raw []byte
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq DESC LIMIT 1`, BoundaryCheckKind, objectID).Scan(&raw)
	}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

// The owner ruled on 2026-11-17: "why do we convict anyone for removing worktrees? that
// sounds stupid? we should be free to act with normal tools without breaking the drain,
// goes along with ad hoc commits". So while a run is in flight the owner and other agents do
// everything ordinary Git does in this repository — add and remove an unrelated worktree,
// create and delete a branch and a tag, commit on main by hand, fetch a remote-tracking ref,
// stash in the main checkout, switch the main checkout's branch and back, export a fork
// series that moves refs/heads/patch/<fork>, and hand-write an operator pin under
// refs/tillr/ — and none of it convicts the run. What two snapshots cannot attribute is
// recorded on the run's boundary.check as an observation, and the check stays clean.
func TestOrdinaryGitUseDuringARunNeverConvictsIt(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	own := l.lanePath("lane-1")
	watched := []string{"lane-2"}

	// A remote whose branch will move, so a fetch updates a remote-tracking ref.
	remote := filepath.Join(scratchUnderDurable(t), "remote")
	runGit(t, f.root, "clone", "-q", "--bare", f.root, remote)
	runGit(t, f.root, "remote", "add", "origin", remote)
	runGit(t, f.root, "fetch", "-q", "origin")
	pusher := filepath.Join(scratchUnderDurable(t), "pusher")
	runGit(t, f.root, "clone", "-q", remote, pusher)
	// A patch branch the owner's fork export will move.
	runGit(t, f.root, "branch", "patch/pi", TrunkRef)

	before, err := l.snapshot(ctx, []string{own}, watched)
	if err != nil {
		t.Fatal(err)
	}

	unrelated := filepath.Join(scratchUnderDurable(t), "agent-worktree")
	runGit(t, f.root, "worktree", "add", "-q", "--detach", unrelated, TrunkRef)
	runGit(t, f.root, "worktree", "remove", "--force", unrelated)
	other := filepath.Join(scratchUnderDurable(t), "still-here")
	runGit(t, f.root, "worktree", "add", "-q", "-b", "owner-topic", other, TrunkRef)
	runGit(t, f.root, "branch", "scratch-branch")
	runGit(t, f.root, "tag", "v-test")
	runGit(t, f.root, "branch", "-D", "scratch-branch")
	runGit(t, f.root, "tag", "-d", "v-test")
	runGit(t, f.root, "tag", "kept-tag")
	writeForkFile(t, filepath.Join(f.root, "hand.txt"), "hand\n")
	runGit(t, f.root, "add", "hand.txt")
	runGit(t, f.root, "commit", "-qm", "a hand commit on main")
	writeForkFile(t, filepath.Join(pusher, "upstream.txt"), "upstream\n")
	runGit(t, pusher, "add", "upstream.txt")
	runGit(t, pusher, "commit", "-qm", "upstream")
	runGit(t, pusher, "push", "-q", "origin", "HEAD")
	runGit(t, f.root, "fetch", "-q", "origin")
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "owner edit\n")
	runGit(t, f.root, "stash", "-q")
	runGit(t, f.root, "switch", "-q", "-c", "owner-side")
	runGit(t, f.root, "switch", "-q", TrunkRef)
	runGit(t, f.root, "switch", "-q", "owner-side")
	runGit(t, f.root, "update-ref", "refs/heads/patch/pi", "HEAD")
	runGit(t, f.root, "update-ref", "refs/tillr/held/operator-pin", "HEAD")

	after, err := l.snapshot(ctx, []string{own}, watched)
	if err != nil {
		t.Fatal(err)
	}
	request := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1"}
	result := &LoopResult{}
	if _, err := l.finishRunBracket(ctx, own, request, "tick", l.cfg.Now(), before, after, result); err != nil {
		t.Fatal(err)
	}
	check := lastBoundaryCheck(t, f, request.RunID)
	if check["clean"] != true {
		t.Fatalf("ordinary Git use convicted the run: %v", check)
	}
	observed, _ := check["observed"].(float64)
	if observed == 0 {
		t.Fatalf("the bracket recorded no observation of the repository's movement: %v", check)
	}
	text, _ := json.Marshal(check["observations"])
	for _, want := range []string{"still-here", "refs/tillr/held/operator-pin", "refs/heads/patch/pi"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("the observations do not name %s: %s", want, text)
		}
	}
	faults := faultsFor(t, f)
	if _, held := faults["item-a"]; held {
		t.Fatalf("ordinary Git use held the item: %+v", faults["item-a"])
	}
}

// What a run's bracket still convicts is a write into another lane: a path whose status
// changed in a watched, idle sibling lane worktree. Ordinary use of the repository never
// writes there — it is another item's candidate under the drain's own lane root — so the
// change is the run's to answer for.
func TestAWriteIntoAnIdleSiblingLaneStillConvicts(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	own := l.lanePath("lane-1")
	before, err := l.snapshot(ctx, []string{own}, []string{"lane-2"})
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(l.lanePath("lane-2"), "escaped.txt"), "escaped\n")
	after, err := l.snapshot(ctx, []string{own}, []string{"lane-2"})
	if err != nil {
		t.Fatal(err)
	}
	request := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1"}
	if _, err := l.finishRunBracket(ctx, own, request, "tick", l.cfg.Now(), before, after, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	check := lastBoundaryCheck(t, f, request.RunID)
	if check["clean"] != false || !strings.Contains(checkText(t, check), "escaped.txt") {
		t.Fatalf("a write into an idle sibling lane was not convicted: %v", check)
	}
}

func faultsFor(t *testing.T, f refusalLoopFixture) map[string]ItemFault {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var faults map[string]ItemFault
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		faults, err = ReadItemFaults(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return faults
}

func checkText(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A real landing, with the owner's stash present, writes its recovery copy inside the run's
// bracket. The boundary check ignores the copy and the stash alike.
func TestARealLandingWithTheOwnersStashPresentDoesNotConvictTheRun(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	own := l.lanePath("lane-1")
	watched := []string{"lane-2"}
	writeForkFile(t, filepath.Join(f.root, "m.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n")
	runGit(t, f.root, "add", "m.txt")
	runGit(t, f.root, "commit", "-qm", "m")
	tip := strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD"))
	runGit(t, f.root, "switch", "-q", "-c", "cand")
	writeForkFile(t, filepath.Join(f.root, "m.txt"), "L1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n")
	runGit(t, f.root, "commit", "-qam", "candidate")
	landed := strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD"))
	runGit(t, f.root, "switch", "-q", TrunkRef)
	runGit(t, f.root, "branch", "-D", "cand")
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "stashed edit\n")
	runGit(t, f.root, "stash", "-q")
	writeForkFile(t, filepath.Join(f.root, "m.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")

	before, err := l.snapshot(ctx, []string{own}, watched)
	if err != nil {
		t.Fatal(err)
	}
	var during *Boundary
	land.OwnerHooks.BeforeLastRead = func() {
		if refs := runGit(t, f.root, "for-each-ref", land.RecoveryPrefix); strings.TrimSpace(refs) == "" {
			t.Error("the recovery copy is absent while the landing is in flight")
		}
		if during, err = l.snapshot(ctx, []string{own}, watched); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(land.ResetOwnerHooks)
	trunk, err := land.OpenTrunk(f.root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := trunk.PlanOwner(TrunkRef, "run-item-a-a1", tip, landed)
	if err != nil {
		t.Fatal(err)
	}
	install, err := trunk.MoveOwner(plan, TrunkRef, landed, tip)
	if err != nil {
		t.Fatal(err)
	}
	if faults := install.Finish(); len(faults) != 0 {
		t.Fatalf("faults: %v", faults)
	}
	if during == nil || strings.TrimSpace(runGit(t, f.root, "stash", "list")) == "" {
		t.Fatal("the landing's snapshot or the owner's stash is missing")
	}
	request := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1"}
	if _, err := l.finishRunBracket(ctx, own, request, "tick", l.cfg.Now(), before, during, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if check := lastBoundaryCheck(t, f, request.RunID); check["clean"] != true {
		t.Fatalf("a real landing with a stash convicted the run: %v", check)
	}
}
