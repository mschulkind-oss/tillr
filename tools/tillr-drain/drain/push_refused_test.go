package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

// pushFixture is a trunk with three unpublished commits, a bare remote that holds the
// published base, and a second clone that can move the remote.
type pushFixture struct {
	f      refusalLoopFixture
	l      *Loop
	remote string
	other  string
	local  []string
}

func newPushFixture(t *testing.T) *pushFixture {
	t.Helper()
	f, l := passFixture(t, "item-a")
	p := &pushFixture{f: f, l: l, remote: filepath.Join(filepath.Dir(f.root), "remote.git"), other: filepath.Join(filepath.Dir(f.root), "other")}
	runGit(t, f.root, "init", "--bare", "-q", p.remote)
	runGit(t, f.root, "remote", "add", "origin", p.remote)
	runGit(t, f.root, "push", "-q", "-u", "origin", TrunkRef)
	runGit(t, f.root, "clone", "-q", "-b", TrunkRef, p.remote, p.other)
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		p.local = append(p.local, ownerCommit(t, f.root, name))
	}
	runGit(t, f.root, "update-ref", GreenRef, p.local[1])
	l.cfg.PostLanding.Push.What = settings.PushGreen
	return p
}

func (p *pushFixture) remoteCommit(t *testing.T, name, body string) string {
	t.Helper()
	writeForkFile(t, filepath.Join(p.other, name), body)
	runGit(t, p.other, "add", ".")
	runGit(t, p.other, "commit", "-qm", "remote "+name)
	runGit(t, p.other, "push", "-q", "origin", TrunkRef)
	return runGit(t, p.other, "rev-parse", "HEAD")
}

func (p *pushFixture) refused(t *testing.T) error {
	t.Helper()
	err := pushTrunk(context.Background(), p.f.root, settings.Push{})
	if _, ok := err.(*refusedPushError); !ok {
		t.Fatalf("push of a trunk behind the remote = %v, want a refused push", err)
	}
	return err
}

// The remote has a commit the trunk lacks: the unpublished commits are replayed on top of
// it, linear, every one present by patch, green carried by patch, and the next push works.
func TestRefusedPushRebasesOntoTheMovedRemote(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "remote.txt", "remote\n")
	err := p.refused(t)
	n, herr := p.l.healRefusedPush(ctx, settings.Push{}, err)
	if herr != nil || n != 3 {
		t.Fatalf("heal = %d, %v", n, herr)
	}
	root := p.f.root
	if runGit(t, root, "rev-list", "--merges", "--count", remoteTip+"..HEAD") != "0" || runGit(t, root, "merge-base", remoteTip, "HEAD") != remoteTip {
		t.Fatal("the trunk is not linear on top of the remote tip")
	}
	if got := runGit(t, root, "rev-list", "--count", remoteTip+"..HEAD"); got != "3" {
		t.Fatalf("%s commits on top of the remote", got)
	}
	if cherry := runGit(t, root, "cherry", "HEAD", p.local[2]); strings.Contains(cherry, "+") {
		t.Fatalf("a commit has no same-patch commit on the trunk: %s", cherry)
	}
	green := runGit(t, root, "rev-parse", GreenRef)
	if green == p.local[1] || runGit(t, root, "rev-parse", "HEAD~1") != green {
		t.Fatalf("green %s was not carried to the replayed commit", green)
	}
	if status := runGit(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("checkout not clean: %s", status)
	}
	if _, err := os.Stat(filepath.Join(root, "remote.txt")); err != nil {
		t.Fatalf("checkout lacks the remote's file: %v", err)
	}
	if h := rewriteHistory(t, p.f); h.Lost != "" {
		t.Fatalf("a trunk-rewritten hold was raised: %+v", h)
	}
	if err := pushTrunk(ctx, root, settings.Push{}); err != nil {
		t.Fatalf("push after the rebase: %v", err)
	}
}

// A conflicting remote commit changes nothing on main/green/checkout and files sync-remote-<r12>.
func TestRefusedPushConflictChangesNothing(t *testing.T) {
	p := newPushFixture(t)
	remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
	err := p.refused(t)
	before := runGit(t, p.f.root, "rev-parse", "HEAD")
	green := runGit(t, p.f.root, "rev-parse", GreenRef)
	_, herr := p.l.healRefusedPush(context.Background(), settings.Push{}, err)
	syncID := RemoteSyncItemID(remoteTip)
	if herr == nil || !strings.Contains(herr.Error(), "mover: "+syncID) {
		t.Fatalf("failure = %v", herr)
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != before || runGit(t, p.f.root, "rev-parse", GreenRef) != green || runGit(t, p.f.root, "status", "--porcelain") != "" {
		t.Fatal("a conflict changed the trunk, green or the checkout")
	}
}

// Owner content on a path the move changes is not overwritten.
func TestRefusedPushDirtyPathChangesNothing(t *testing.T) {
	p := newPushFixture(t)
	p.remoteCommit(t, "remote.txt", "remote\n")
	err := p.refused(t)
	writeForkFile(t, filepath.Join(p.f.root, "remote.txt"), "owner\n")
	before := runGit(t, p.f.root, "rev-parse", "HEAD")
	if _, herr := p.l.healRefusedPush(context.Background(), settings.Push{}, err); herr == nil || !strings.Contains(herr.Error(), "mover: operator") {
		t.Fatalf("failure = %v", herr)
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != before {
		t.Fatal("the trunk moved under owner content")
	}
	if b, _ := os.ReadFile(filepath.Join(p.f.root, "remote.txt")); string(b) != "owner\n" {
		t.Fatalf("owner content changed: %q", b)
	}
}

// A push that fails for another reason is no refusal: nothing is fetched or rebased.
func TestOrdinaryPushFailureDoesNotRebase(t *testing.T) {
	p := newPushFixture(t)
	p.remoteCommit(t, "remote.txt", "remote\n")
	before := runGit(t, p.f.root, "rev-parse", "HEAD")
	runGit(t, p.f.root, "remote", "set-url", "origin", filepath.Join(filepath.Dir(p.remote), "missing.git"))
	green := runGit(t, p.f.root, "rev-parse", GreenRef)
	p.l.afterGreen(context.Background(), green)
	waitAction(t, func() bool { return !p.l.pushAction.busy() })
	if err := pushTrunk(context.Background(), p.f.root, settings.Push{}); err == nil {
		t.Fatal("push to a missing remote succeeded")
	} else if _, ok := err.(*refusedPushError); ok {
		t.Fatalf("an unreachable remote was classified as refused: %v", err)
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != before {
		t.Fatal("an ordinary failure moved the trunk")
	}
	if d, ok := lastPushDetail(t, p.l, green); !ok || strings.Contains(d, "mover: operator") {
		t.Fatalf("recorded %q (%v)", d, ok)
	}
}

// Through the loop's own push action: a refused green push rebases the trunk, records the
// outcome, and leaves the remote alone until the new tip is proved.
func TestRefusedGreenPushHealsThroughTheAction(t *testing.T) {
	p := newPushFixture(t)
	remoteTip := p.remoteCommit(t, "remote.txt", "remote\n")
	green := runGit(t, p.f.root, "rev-parse", GreenRef)
	p.l.afterGreen(context.Background(), green)
	waitAction(t, func() bool { return !p.l.pushAction.busy() })
	if runGit(t, p.f.root, "merge-base", remoteTip, "HEAD") != remoteTip {
		t.Fatal("the trunk was not rebased onto the remote")
	}
	if runGit(t, p.f.root, "--git-dir="+p.remote, "rev-parse", "refs/heads/"+TrunkRef) != remoteTip {
		t.Fatal("the unproved tip was pushed")
	}
	if d, ok := lastPushDetail(t, p.l, green); !ok || !strings.Contains(d, "rebased onto it") {
		t.Fatalf("recorded %q (%v)", d, ok)
	}
}

// A commit another machine pushed under another identity arrives with the rebase. It was
// published by someone with push access, so it must not hold landings; the commits
// replayed on top stay under the check, and a foreign commit made here still holds.
func TestRefusedPushRebaseTrustsTheRemoteIdentity(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	runGit(t, p.other, "-c", "user.name=Other", "-c", "user.email=other@example.invalid", "commit", "-q", "--allow-empty", "-m", "from another machine")
	runGit(t, p.other, "push", "-q", "origin", TrunkRef)
	foreign := runGit(t, p.other, "rev-parse", "HEAD")
	err := p.refused(t)
	if _, herr := p.l.healRefusedPush(ctx, settings.Push{}, err); herr != nil {
		t.Fatal(herr)
	}
	if err := p.l.observeIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if held, err := p.l.identityHeld(ctx); err != nil || held {
		t.Fatalf("a commit from the remote held landings: held=%v err=%v", held, err)
	}
	// Control: a foreign commit that did not come from the remote still holds.
	local := foreignCommit(t, p.f.root, "HEAD", false)
	if err := p.l.observeIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if held, err := p.l.identityHeld(ctx); err != nil || !held {
		t.Fatalf("a local foreign commit %s did not hold: held=%v err=%v", local, held, err)
	}
	if runGit(t, p.f.root, "rev-parse", "--verify", "refs/tillr/published/origin/"+TrunkRef) != foreign {
		t.Fatal("the fetched tip was not pinned")
	}
}
