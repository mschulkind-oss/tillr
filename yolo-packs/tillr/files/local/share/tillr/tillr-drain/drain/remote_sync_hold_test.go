package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRemoteSyncDirtyWorktreeHolds(t *testing.T) {
	t.Run("clashing-path", func(t *testing.T) {
		p, remoteTip, clear := blockRemoteSyncReplay(t)
		before := lastPushEvent(t, p.f)
		p.pollPushHold(t)
		if after := lastPushEvent(t, p.f); !reflect.DeepEqual(before, after) {
			t.Fatalf("retried while checkout still blocked: %+v -> %+v", before, after)
		}
		clear()
		root := p.f.root
		unrelated := filepath.Join(root, "one.txt")
		writeForkFile(t, unrelated, "keep my unstaged edit\n")

		p.pollPushHold(t)

		pushed := runGit(t, root, "--git-dir="+p.remote, "rev-parse", TrunkRef)
		if pushed == remoteTip || pushed != runGit(t, root, "rev-parse", TrunkRef) ||
			runGit(t, root, "merge-base", pushed, remoteTip) != remoteTip {
			t.Fatalf("remote = %s, want main replayed on %s", pushed, remoteTip)
		}
		if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindOK {
			t.Fatalf("recovery did not clear the hold: %+v", ev)
		}
		if b, err := os.ReadFile(unrelated); err != nil || string(b) != "keep my unstaged edit\n" {
			t.Fatalf("unrelated edit lost: %q, %v", b, err)
		}
		if got := runGit(t, root, "status", "--porcelain"); got != "M one.txt" {
			t.Fatalf("unrelated edit is not the sole uncommitted change: %q", got)
		}
	})

	t.Run("off-main", func(t *testing.T) {
		p := newPushFixture(t)
		root := p.f.root
		p.l.cfg.PostLanding.Push.What = settings.PushTip
		remoteTip := p.remoteCommit(t, "base.txt", "remote edit\n")
		runGit(t, root, "checkout", "-qb", "operator")
		unrelated := filepath.Join(root, "one.txt")
		writeForkFile(t, unrelated, "keep my operator edit\n")

		p.l.afterLanding(context.Background(), p.local[2])
		waitAction(t, &p.l.pushAction)

		if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindOK {
			t.Fatalf("off-main replay should advance via update-ref without holding: %+v", ev)
		}
		pushed := runGit(t, root, "--git-dir="+p.remote, "rev-parse", TrunkRef)
		if pushed == remoteTip || pushed != runGit(t, root, "rev-parse", TrunkRef) ||
			runGit(t, root, "merge-base", pushed, remoteTip) != remoteTip {
			t.Fatalf("remote = %s, want main replayed on %s", pushed, remoteTip)
		}
		if got := runGit(t, root, "symbolic-ref", "--short", "HEAD"); got != "operator" {
			t.Fatalf("HEAD = %q, want operator branch untouched", got)
		}
		if b, err := os.ReadFile(unrelated); err != nil || string(b) != "keep my operator edit\n" {
			t.Fatalf("operator edit lost: %q, %v", b, err)
		}
	})
}

func TestRemoteSyncClearanceDoesNotTriggerWhenNothingUnpublished(t *testing.T) {
	p, remoteTip, clear := blockRemoteSyncReplay(t)
	clear()
	// The operator elected to use the published tip instead of keeping local commits.
	runGit(t, p.f.root, "reset", "--keep", remoteTip)
	before := lastPushEvent(t, p.f)
	p.pollPushHold(t)
	if after := lastPushEvent(t, p.f); !reflect.DeepEqual(before, after) {
		t.Fatalf("clearance triggered an unnecessary push: %+v -> %+v", before, after)
	}
}

func TestRemoteSyncPatchMoveClearancePushesGreen(t *testing.T) {
	p := newPushFixture(t)
	ctx, root := context.Background(), p.f.root
	before := &Boundary{Repo: root, Refs: map[string]string{"refs/heads/patch/pi": "old"}}
	after := &Boundary{Repo: root, Refs: map[string]string{"refs/heads/patch/pi": "new"}}
	_, observed, err := CompareRunObserved(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.l.recordBoundaryCheck(ctx, BoundaryScopeRun, "tick", []runRef{{RunID: "run-a", ItemID: "item-a", LaneID: "lane-1"}}, nil, nil, p.l.cfg.Now(), observed...); err != nil {
		t.Fatal(err)
	}
	hook := pushTestHook(t, filepath.Join(root, ".git"), "pre-push", "patch move not acknowledged")
	p.l.afterGreen(ctx, p.local[1])
	waitAction(t, &p.l.pushAction)
	event := lastPushEvent(t, p.f)
	if event["kind"] != pushKindPatchMoveHeld {
		t.Fatalf("missing patch move hold: %+v", event)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	p.pollPushHold(t)
	if got := lastPushEvent(t, p.f); !reflect.DeepEqual(got, event) {
		t.Fatalf("retried before acknowledgment: %+v", got)
	}
	s := openFixture(t, p.f.storePath)
	defer s.Close()
	var pending []patchmoves.Hold
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		pending, err = patchmoves.Pending(r)
		return err
	}); err != nil || len(pending) != 1 {
		t.Fatalf("pending patch moves = %v, %v", pending, err)
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		return patchmoves.Acknowledge(tx, pending[0].Observation, pending[0].Fork(), "owner", p.l.cfg.Now())
	})
	p.pollPushHold(t)
	if got := runGit(t, root, "--git-dir="+p.remote, "rev-parse", TrunkRef); got != p.local[1] {
		t.Fatalf("remote = %s, want proved tip %s", got, p.local[1])
	}
	if event := lastPushEvent(t, p.f); event["kind"] != pushKindOK {
		t.Fatalf("recovery did not clear the patch move hold: %+v", event)
	}
}

func blockRemoteSyncReplay(t *testing.T) (*pushFixture, string, func()) {
	t.Helper()
	p := newPushFixture(t)
	root := p.f.root
	p.l.cfg.PostLanding.Push.What = settings.PushTip
	remoteTip := p.remoteCommit(t, "base.txt", "remote edit\n")
	clash := filepath.Join(root, "base.txt")
	clear := func() { runGit(t, root, "restore", "--", "base.txt") }
	writeForkFile(t, clash, "owner's uncommitted file\n")
	p.l.afterLanding(context.Background(), p.local[2])
	waitAction(t, &p.l.pushAction)
	if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindWorktreeDirty || ev["escalated_mover"] != "operator" {
		t.Fatalf("blocked replay did not record an operator hold: %+v", ev)
	}
	if runGit(t, root, "rev-parse", TrunkRef) != p.local[2] ||
		runGit(t, root, "--git-dir="+p.remote, "rev-parse", TrunkRef) != remoteTip {
		t.Fatal("blocked replay moved main or the remote")
	}
	return p, remoteTip, clear
}

func (p *pushFixture) pollPushHold(t *testing.T) {
	t.Helper()
	if _, err := p.l.passMode(context.Background(), &LoopResult{}, true); err != nil {
		t.Fatal(err)
	}
	waitAction(t, &p.l.pushAction)
}
