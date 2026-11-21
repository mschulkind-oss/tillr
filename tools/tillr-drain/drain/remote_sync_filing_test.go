package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// Exercise the filing seam directly. Routing a refused push here and dispatching
// the resulting repair are separate slices.
func remoteSyncFilingFixture(t *testing.T, p *pushFixture, base, remoteTip, commit string) remoteSyncState {
	t.Helper()
	root := p.f.root
	parent := runGit(t, root, "rev-parse", commit+"^")
	cmd := exec.Command("git", "-C", root, "merge-tree", "--write-tree", "--name-only", "-z", "--merge-base="+parent, base, commit)
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("expected merge-tree conflicts, got %q, %v", out, err)
	}
	paths, detail := parseMergeTreeConflict(string(out))
	mergeBase := runGit(t, root, "merge-base", TrunkRef, remoteTip)
	msgs, msg, summary := p.l.collectRemoteSyncContext(context.Background(), mergeBase, remoteTip, commit)
	return remoteSyncState{
		ItemID: RemoteSyncItemID(remoteTip), Remote: "origin", Ref: "refs/heads/" + TrunkRef,
		RemoteTip: remoteTip, MergeBase: mergeBase, BaseSHA: base, OldTrunk: runGit(t, root, "rev-parse", TrunkRef),
		ConflictCommit: commit, ConflictParent: parent, ConflictPaths: paths, ConflictOutput: detail,
		RemoteMessages: msgs, CommitMessage: msg, LandingSummary: summary,
	}
}

func filedRemoteSyncItem(t *testing.T, p *pushFixture, id string) (row itemRow, waiting *item.WaitingOn, stages int) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, p.f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		for _, it := range rows {
			if it.ItemID == id {
				row = it
			}
		}
		waiting, _, err = item.ReadWaitingOn(r, id)
		if err != nil {
			return err
		}
		return r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, remoteSyncStagedKind, id).Scan(&stages)
	}); err != nil {
		t.Fatal(err)
	}
	if row.ItemID != id {
		t.Fatalf("missing repair item %s", id)
	}
	return row, waiting, stages
}

func TestRemoteSyncConflictFilesRepairItemAndPins(t *testing.T) {
	ctx := context.Background()
	p := newPushFixture(t)
	root := p.f.root
	remote := p.remoteCommit(t, "two.txt", "remote two\n")
	runGit(t, root, "fetch", "-q", "origin")
	parent := runGit(t, root, "rev-parse", p.local[0]+"^")
	tree := runGit(t, root, "merge-tree", "--write-tree", "--merge-base="+parent, remote, p.local[0])
	base := runGit(t, root, "commit-tree", tree, "-p", remote, "-m", "replay one.txt")
	st := remoteSyncFilingFixture(t, p, base, remote, p.local[1])
	writeForkFile(t, filepath.Join(root, "one.txt"), "owner's uncommitted edit\n")
	before := runGit(t, root, "status", "--porcelain")
	retry := st
	retry.BaseSHA = remote // A retry cannot replace the original staged prefix.
	for _, attempt := range []remoteSyncState{st, retry} {
		conflict, err := p.l.fileOrUpdateRemoteSyncItem(ctx, attempt)
		if err != nil || conflict == nil || conflict.kind != pushKindConflict || !strings.Contains(conflict.Error(), "mover: "+st.ItemID) {
			t.Fatalf("filing = %v, %v", conflict, err)
		}
	}
	if !IsGeneratedItem(st.ItemID) || !IsGeneratedRepair(st.ItemID) {
		t.Fatalf("%s is not recognized as a generated repair", st.ItemID)
	}
	row, waiting, stages := filedRemoteSyncItem(t, p, st.ItemID)
	if row.State != StateInQueue || !row.Express || !reflect.DeepEqual(row.Allowed, []string{"two.txt"}) || waiting != nil || stages != 1 {
		t.Fatalf("item = %+v, waiting = %+v, staging records = %d", row, waiting, stages)
	}
	if got, ok, err := p.l.readRemoteSyncState(ctx, st.ItemID); err != nil || !ok || !reflect.DeepEqual(got, st) || !strings.Contains(got.ConflictOutput, "CONFLICT") || !strings.Contains(got.RemoteMessages, "remote two.txt") || !strings.Contains(got.CommitMessage, "two.txt") {
		t.Fatalf("staged conflict = %+v, %v, %v", got, ok, err)
	}
	if got := optionalCommit(ctx, root, remoteSyncPinRef(st.ItemID)); got != base {
		t.Fatalf("repair pin = %s, want %s", got, base)
	}
	if got := runGit(t, root, "rev-parse", TrunkRef, GreenRef, "HEAD"); got != strings.Join([]string{p.local[2], p.local[1], p.local[2]}, "\n") {
		t.Fatalf("main, green or HEAD moved: %s", got)
	}
	body, err := os.ReadFile(filepath.Join(root, "one.txt"))
	if err != nil || string(body) != "owner's uncommitted edit\n" || runGit(t, root, "status", "--porcelain") != before {
		t.Fatalf("main checkout changed: %q, %v", body, err)
	}
	if !runRefExcused(remoteSyncPinRef(st.ItemID), &Boundary{}) {
		t.Fatal("the boundary treats a drain-owned repair pin as a worker change")
	}
	if err := p.l.pruneRunRefs(ctx); err != nil {
		t.Fatal(err)
	}
	if got := optionalCommit(ctx, root, remoteSyncPinRef(st.ItemID)); got != base {
		t.Fatalf("sweep removed the open repair's pin: %q", got)
	}
	s, err := store.Open(ctx, p.f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, st.ItemID, "reconciled by hand", "owner", p.l.cfg.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.l.pruneRunRefs(ctx); err != nil {
		t.Fatal(err)
	}
	if got := optionalCommit(ctx, root, remoteSyncPinRef(st.ItemID)); got != "" {
		t.Fatalf("sweep kept the finished repair's pin: %s", got)
	}
	remote2 := p.remoteCommit(t, "upstream.txt", "remote advanced\n")
	runGit(t, root, "fetch", "-q", "origin")
	tree2 := runGit(t, root, "merge-tree", "--write-tree", "--merge-base="+parent, remote2, p.local[0])
	base2 := runGit(t, root, "commit-tree", tree2, "-p", remote2, "-m", "replay one.txt on newer tip")
	st2 := remoteSyncFilingFixture(t, p, base2, remote2, p.local[1])
	if _, err := p.l.fileOrUpdateRemoteSyncItem(ctx, st2); err != nil {
		t.Fatal(err)
	}
	row2, _, stages2 := filedRemoteSyncItem(t, p, st2.ItemID)
	if st2.ItemID == st.ItemID || row2.State != StateInQueue || stages2 != 1 || optionalCommit(ctx, root, remoteSyncPinRef(st2.ItemID)) != base2 {
		t.Fatalf("new tip did not get its own staged repair: %+v, stages = %d", row2, stages2)
	}
}

func TestRemoteSyncConflictPreservesLiteralPath(t *testing.T) {
	for _, name := range []string{"café.txt", " space name.txt "} {
		t.Run(name, func(t *testing.T) {
			p := newPushFixture(t)
			commit := ownerCommit(t, p.f.root, name)
			remote := p.remoteCommit(t, name, "remote version\n")
			runGit(t, p.f.root, "fetch", "-q", "origin")
			st := remoteSyncFilingFixture(t, p, remote, remote, commit)
			if _, err := p.l.fileOrUpdateRemoteSyncItem(context.Background(), st); err != nil {
				t.Fatal(err)
			}
			row, waiting, _ := filedRemoteSyncItem(t, p, st.ItemID)
			if !reflect.DeepEqual(row.Allowed, []string{name}) || waiting != nil {
				t.Fatalf("fence = %q, waiting = %+v; want literal %q", row.Allowed, waiting, name)
			}
		})
	}
}

func TestRemoteSyncGateOnlyConflictEscalatesToOwner(t *testing.T) {
	testRemoteSyncOwnerFence(t, "tools/tillr-gate/main.go", false)
}

func TestRemoteSyncMixedGateConflictCannotPassPreGate(t *testing.T) {
	// Filing half only: the tree check belongs to a later slice.
	testRemoteSyncOwnerFence(t, "tools/tillr-gate/main.go", true)
}

func TestRemoteSyncWildcardPathEscalatesWithoutWideningFence(t *testing.T) {
	for _, name := range []string{"notes?.txt", "notes*.txt"} {
		t.Run(name, func(t *testing.T) { testRemoteSyncOwnerFence(t, name, true) })
	}
}

func TestRemoteSyncUnrepresentablePathsEscalateWithoutWideningFence(t *testing.T) {
	for _, name := range []string{"notes\nsecond.txt", "notes\xff.txt"} {
		t.Run(name, func(t *testing.T) { testRemoteSyncOwnerFence(t, name, true) })
	}
}

func testRemoteSyncOwnerFence(t *testing.T, excluded string, mixed bool) {
	t.Helper()
	p := newPushFixture(t)
	for _, root := range []string{p.f.root, p.other} {
		path := filepath.Join(root, excluded)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, path, root+"\n")
	}
	root := p.f.root
	runGit(t, root, "add", "--", excluded)
	changed, want := excluded, []string(nil)
	if mixed {
		runGit(t, root, "commit", "--amend", "--no-edit", "-q")
		changed, want = "three.txt", []string{"three.txt"}
	} else {
		runGit(t, root, "commit", "-qm", "local conflict")
	}
	remote := p.remoteCommit(t, changed, "remote conflict\n")
	runGit(t, root, "fetch", "-q", "origin")
	st := remoteSyncFilingFixture(t, p, remote, remote, runGit(t, root, "rev-parse", "HEAD"))
	if len(st.ConflictPaths) != len(want)+1 || !strings.Contains(strings.Join(st.ConflictPaths, "\n"), excluded) {
		t.Fatalf("conflict paths = %q", st.ConflictPaths)
	}
	conflict, err := p.l.fileOrUpdateRemoteSyncItem(context.Background(), st)
	if err != nil || conflict == nil || conflict.kind != pushKindConflict || conflict.escalated != "owner" {
		t.Fatalf("filing = %v, %v; want owner escalation", conflict, err)
	}
	row, waiting, _ := filedRemoteSyncItem(t, p, st.ItemID)
	// The owner-facing JSON replaces bytes that are not valid UTF-8; the fence
	// must not acquire that replacement as a path.
	if row.State != StateBlocked || waiting == nil || waiting.Waiter != "owner" || !strings.Contains(waiting.Reason, strings.ToValidUTF8(excluded, "\uFFFD")) || !reflect.DeepEqual(row.Allowed, want) {
		t.Fatalf("state = %s, waiting = %+v, allowed = %q; want owner hold and %q", row.State, waiting, row.Allowed, want)
	}
}
