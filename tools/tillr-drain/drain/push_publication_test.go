package drain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/settings"
)

func requirePublicationRewindStopped(t *testing.T, p *pushFixture, previous, forward string) {
	t.Helper()
	ctx := context.Background()
	ref := "refs/heads/" + TrunkRef
	runGit(t, p.remote, "update-ref", ref, previous, forward)
	var state *pushStateError
	if err := pushTrunk(ctx, p.f.root, settings.Push{}); !errors.As(err, &state) || state.kind != pushKindRewritten {
		t.Errorf("push after rewind = %v, want remote-rewritten", err)
	}
	if got := optionalCommit(ctx, p.remote, ref); got != previous {
		t.Errorf("remote after rewind = %s, want %s", got, previous)
	}
	if got := optionalCommit(ctx, p.f.root, publishedRef("origin", ref)); got != forward {
		t.Errorf("published pointer after rewind = %s, want %s", got, forward)
	}
}

func TestPushGreenRecordsForwardFetchBeforeRewind(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	root := p.f.root
	ref := "refs/heads/" + TrunkRef
	if err := pushTrunk(ctx, root, settings.Push{}); err != nil {
		t.Fatal(err)
	}
	published := p.local[2]
	runGit(t, root, "update-ref", GreenRef, published)
	runGit(t, p.other, "pull", "--ff-only", "origin", TrunkRef)
	forward := p.remoteCommit(t, "upstream.txt", "forward remote\n")
	if _, err := pushGreen(ctx, root, settings.Push{}); !errors.Is(err, errAlreadyPublished) {
		t.Fatalf("pushGreen = %v, want already published", err)
	}
	if got := optionalCommit(ctx, root, publishedRef("origin", ref)); got != forward {
		t.Errorf("published pointer after accepted fetch = %s, want %s", got, forward)
	}
	runGit(t, root, "merge", "--ff-only", forward)
	ownerCommit(t, root, "next.txt")
	requirePublicationRewindStopped(t, p, published, forward)
}

func TestPushRecordsForwardFetchDespiteServerDecline(t *testing.T) {
	for _, mode := range []string{settings.PushTip, settings.PushGreen} {
		t.Run(mode, func(t *testing.T) {
			checkPublicationAfterPushFailure(t, mode, "pre-receive", pushKindRemoteDeclined)
		})
	}
}

func TestPushRecordsForwardFetchDespiteLocalFailure(t *testing.T) {
	checkPublicationAfterPushFailure(t, settings.PushTip, "pre-push", pushKindFailed)
}

func checkPublicationAfterPushFailure(t *testing.T, mode, hookName, kind string) {
	t.Helper()
	p := newPushFixture(t)
	ctx := context.Background()
	root := p.f.root
	ref := "refs/heads/" + TrunkRef
	if err := pushTrunk(ctx, root, settings.Push{}); err != nil {
		t.Fatal(err)
	}
	published := p.local[2]
	runGit(t, p.other, "pull", "--ff-only", "origin", TrunkRef)
	forward := p.remoteCommit(t, "upstream.txt", "forward remote\n")
	// Import the object without advancing the tracking ref: only the guard's
	// accepted fetch should teach this clone that forward is published.
	runGit(t, root, "fetch", "--refmap=", "origin", ref)
	runGit(t, root, "merge", "--ff-only", forward)
	tip := ownerCommit(t, root, "next.txt")
	runGit(t, root, "update-ref", GreenRef, tip)
	hookRoot := p.remote
	if hookName == "pre-push" {
		hookRoot = filepath.Join(root, ".git")
	}
	hook := pushTestHook(t, hookRoot, hookName, "declined for test")
	var err error
	if mode == settings.PushGreen {
		_, err = pushGreen(ctx, root, settings.Push{})
	} else {
		err = pushTrunk(ctx, root, settings.Push{})
	}
	var state *pushStateError
	if !errors.As(err, &state) || state.kind != kind {
		t.Fatalf("push = %v, want %s", err, kind)
	}
	if got := optionalCommit(ctx, root, publishedRef("origin", ref)); got != forward {
		t.Errorf("published pointer after %s = %s, want %s", kind, got, forward)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	requirePublicationRewindStopped(t, p, published, forward)
}

func TestRefusedGreenRecordsRemoteAlreadyUnderMain(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	root := p.f.root
	ref := "refs/heads/" + TrunkRef
	previous := runGit(t, root, "rev-parse", "refs/remotes/origin/"+TrunkRef)
	green, remoteTip, main := p.local[0], p.local[1], p.local[2]
	runGit(t, root, "update-ref", GreenRef, green)
	// The remote advanced between the guard and the push. Bypass the guard
	// so the refused-path fetch, not preflight, must record the tip.
	runGit(t, p.remote, "fetch", "-q", root, remoteTip)
	runGit(t, p.remote, "update-ref", ref, remoteTip, previous)
	refusal := pushCommand(ctx, root, "--", "origin", green+":"+ref)
	var refused *refusedPushError
	if !errors.As(refusal, &refused) {
		t.Fatalf("push = %v, want a refusal", refusal)
	}
	if _, err := p.l.healRefusedPush(ctx, settings.Push{}, refusal); err == nil || !strings.Contains(err.Error(), "already under the trunk") {
		t.Fatalf("heal = %v, want the remote-already-under-main return", err)
	}
	if got := optionalCommit(ctx, root, publishedRef("origin", ref)); got != remoteTip {
		t.Errorf("accepted tip = %s, want %s", got, remoteTip)
	}
	if got := runGit(t, root, "rev-parse", ref); got != main {
		t.Fatalf("heal rewrote main: %s, want %s", got, main)
	}
	requirePublicationRewindStopped(t, p, previous, remoteTip)
}

func TestPublishedPointerKeepsNewerAcceptedFetch(t *testing.T) {
	p := newPushFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root := p.f.root
	ref := "refs/heads/" + TrunkRef
	pubRef := publishedRef("origin", ref)
	r1 := p.remoteCommit(t, "r1.txt", "first\n")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	paused, resume := filepath.Join(shim, "paused"), filepath.Join(shim, "resume")
	t.Setenv("PUBLICATION_TEST_GIT", git)
	t.Setenv("PUBLICATION_TEST_REF", pubRef)
	t.Setenv("PUBLICATION_TEST_R1", r1)
	t.Setenv("PUBLICATION_TEST_PAUSED", paused)
	t.Setenv("PUBLICATION_TEST_RESUME", resume)
	// Pause outside Git's lock, after the writer's ancestry check but before
	// Git can compare and swap the pointer. Other Git commands run normally.
	script := `#!/bin/sh
if [ "$3" = update-ref ]; then
    case "$4" in
        --no-deref) ref=$5; tip=$6 ;;
        *) ref=$4; tip=$5 ;;
    esac
    if [ "$ref" = "$PUBLICATION_TEST_REF" ] && [ "$tip" = "$PUBLICATION_TEST_R1" ]; then
        touch "$PUBLICATION_TEST_PAUSED"
        while [ ! -e "$PUBLICATION_TEST_RESUME" ]; do sleep 0.01; done
    fi
fi
exec "$PUBLICATION_TEST_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	turn := func() error {
		prev := priorPublished(ctx, root, "origin", ref)
		tip, err := fetchPushDestination(ctx, root, "origin", ref)
		if err != nil {
			return err
		}
		if err := checkPublishedAncestry(ctx, root, "origin", ref, prev, tip); err != nil {
			return err
		}
		return advancePublished(ctx, root, "origin", ref, tip)
	}
	done := make(chan struct{})
	var firstErr error
	go func() {
		firstErr = turn()
		close(done)
	}()
	defer func() {
		// Also release and join the first turn if the test fails while it waits.
		_ = os.WriteFile(resume, nil, 0o600)
		<-done
	}()
	for {
		if _, err := os.Stat(paused); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("first turn did not pause: %v", firstErr)
		case <-ctx.Done():
			t.Fatal("first turn did not reach update-ref")
		case <-time.After(10 * time.Millisecond):
		}
	}
	r2 := p.remoteCommit(t, "r2.txt", "second\n")
	if err := turn(); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if err := os.WriteFile(resume, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-done
	if firstErr != nil {
		t.Fatalf("resumed first turn: %v", firstErr)
	}
	if got := optionalCommit(ctx, root, pubRef); got != r2 {
		t.Errorf("published pointer = %s, want newer accepted tip %s", got, r2)
	}
}
