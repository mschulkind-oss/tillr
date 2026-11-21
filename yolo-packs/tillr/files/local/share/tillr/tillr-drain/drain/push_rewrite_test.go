package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const replayMarker = "refs/tillr/landed/run-x"

func assertRecordedReplay(t *testing.T, f refusalLoopFixture, old, replacement string) {
	t.Helper()
	if h := rewriteHistory(t, f); h.Lost != "" {
		t.Fatalf("trunk-rewritten hold raised on replay: %+v", h)
	}
	if got := runGit(t, f.root, "rev-parse", replayMarker); got != replacement {
		t.Fatalf("landed marker = %s, want %s", got, replacement)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		resolved, err := heavy.RewriteResolve(r, old)
		if err != nil || resolved != replacement {
			t.Fatalf("RewriteResolve(%s) = (%s, %v), want %s", old, resolved, err, replacement)
		}
		return nil
	})
}

func TestRefusedPushDirectRewritesSupersessionAndMarkers(t *testing.T) {
	p := newPushFixture(t)
	runGit(t, p.f.root, "update-ref", replayMarker, p.local[0])
	// Upstream applies one.txt along with another file, so the local change is
	// already present but its patch cannot match the upstream commit's patch.
	writeForkFile(t, filepath.Join(p.other, "remote.txt"), "upstream only\n")
	remote := p.remoteCommit(t, "one.txt", "one.txt\n")
	n, err := p.l.healRefusedPush(context.Background(), settings.Push{}, p.refused(t))
	if err != nil || n != 3 {
		t.Fatalf("heal = %d, %v", n, err)
	}
	if got := runGit(t, p.f.root, "rev-list", "--count", remote+"..HEAD"); got != "2" {
		t.Errorf("replay wrote %s commits, want two (no empty commit)", got)
	}
	assertRecordedReplay(t, p.f, p.local[0], remote)
}

func TestRefusedPushShiftedContextChangesPatchID(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	lines := strings.Split("one two three four five six seven eight nine ten", " ")
	writeForkFile(t, filepath.Join(f.root, "shared.txt"), strings.Join(lines, "\n")+"\n")
	runGit(t, f.root, "add", "shared.txt")
	runGit(t, f.root, "commit", "-qm", "base shared.txt")
	p := &pushFixture{f: f, l: l, remote: filepath.Join(filepath.Dir(f.root), "remote.git"), other: filepath.Join(filepath.Dir(f.root), "other")}
	runGit(t, f.root, "init", "--bare", "-q", p.remote)
	runGit(t, f.root, "remote", "add", "origin", p.remote)
	runGit(t, f.root, "push", "-q", "-u", "origin", TrunkRef)
	runGit(t, f.root, "clone", "-q", "-b", TrunkRef, p.remote, p.other)

	local := append([]string(nil), lines...)
	local[8] = "local-edit-9"
	writeForkFile(t, filepath.Join(f.root, "shared.txt"), strings.Join(local, "\n")+"\n")
	runGit(t, f.root, "add", "shared.txt")
	runGit(t, f.root, "commit", "-qm", "local edit line 9")
	old := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", replayMarker, old)

	// The upstream edit is inside the diff context of line 9 but does not conflict.
	lines[6] = "remote-edit-7"
	p.remoteCommit(t, "shared.txt", strings.Join(lines, "\n")+"\n")
	n, err := l.healRefusedPush(ctx, settings.Push{}, p.refused(t))
	if err != nil || n != 1 {
		t.Fatalf("heal = %d, %v", n, err)
	}
	replacement := runGit(t, f.root, "rev-parse", "HEAD")
	_, patches, err := patchIDs(ctx, f.root, old+"..."+replacement)
	if err != nil || patches[old] == "" || patches[replacement] == "" || patches[old] == patches[replacement] {
		t.Fatalf("shifted context did not change the patch ID: %v, %v", patches, err)
	}
	assertRecordedReplay(t, f, old, replacement)
}
