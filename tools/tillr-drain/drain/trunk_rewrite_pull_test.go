package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/store"
)

// pullFixture is a trunk with two landings the remote has never seen, observed by the loop,
// and a remote that has since moved: the state in which `git pull` with pull.rebase=true
// rewrites the two landings' shas.
type pullFixture struct {
	f      refusalLoopFixture
	l      *Loop
	remote string
	old    []string // the two landings, under the shas they were recorded with
	result LoopResult
}

func newPullFixture(t *testing.T) *pullFixture {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state='blocked'`); return err })
	p := &pullFixture{f: f, l: l, remote: filepath.Join(filepath.Dir(f.root), "pull-remote")}
	runGit(t, f.root, "clone", f.root, p.remote)
	writeForkFile(t, filepath.Join(p.remote, "remote.txt"), "remote\n")
	runGit(t, p.remote, "add", ".")
	runGit(t, p.remote, "commit", "-qm", "remote")
	for i, name := range []string{"one.txt", "two.txt"} {
		writeForkFile(t, filepath.Join(f.root, name), name+"\n")
		runGit(t, f.root, "add", ".")
		runGit(t, f.root, "commit", "-qm", name)
		sha := runGit(t, f.root, "rev-parse", "HEAD")
		p.old = append(p.old, sha)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return recordFixtureLanding(tx, "item-"+name, "run-"+name, sha, i+1)
		})
	}
	historyPass(t, l, &p.result) // the loop observes the tip before the pull
	return p
}

func (p *pullFixture) history(t *testing.T) trunkHistory {
	t.Helper()
	var h trunkHistory
	s, err := store.OpenRead(context.Background(), p.f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error { var err error; h, err = readTrunkHistory(r); return err }); err != nil {
		t.Fatal(err)
	}
	return h
}

func (p *pullFixture) mapped(t *testing.T) int {
	t.Helper()
	n := 0
	s, err := store.OpenRead(context.Background(), p.f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM trunk_rewrites`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// A pull that rebases the unpushed landings onto a moved remote gives them new shas. The
// loop proves each by patch id, records the mapping and goes on with no hold; every reader
// of the store finds what it recorded under the old shas.
func TestPullRebaseOfUnpushedLandingsIsAbsorbed(t *testing.T) {
	p := newPullFixture(t)
	f, l := p.f, p.l
	// An idle lane still based on the old trunk tip.
	lane := filepath.Join(filepath.Dir(f.root), "idle-lane")
	runGit(t, f.root, "worktree", "add", "--detach", lane, p.old[1])

	runGit(t, f.root, "pull", "--rebase", p.remote, "main")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	if tip == p.old[1] {
		t.Fatal("the pull did not rewrite")
	}

	historyPass(t, l, &p.result)
	if h := p.history(t); h.Lost != "" || h.Observed != tip {
		t.Fatalf("the rewrite was not absorbed: %+v", h)
	}
	if n := p.mapped(t); n != 2 {
		t.Fatalf("mapped %d commits, want 2", n)
	}
	newTwo := runGit(t, f.root, "rev-parse", tip)
	for _, d := range mustPlan(t, l) {
		if d.decision.Action == ActionHold && strings.HasPrefix(d.decision.Reason, ReasonTrunkRewritten) {
			t.Fatalf("hold after an absorbed rewrite: %+v", d.decision)
		}
	}

	// A landing recorded under its old sha is found from its new sha.
	of, found, err := l.landingRecord(context.Background(), newTwo)
	if err != nil || !found || of.runID != "run-two.txt" {
		t.Fatalf("landing by the new sha: %+v %v %v", of, found, err)
	}
	has, err := HeavyRunner{Store: f.storePath}.HasLandingRecord(context.Background(), newTwo)
	if err != nil || !has {
		t.Fatalf("a red first commit has no landing record under its new sha: %v %v", has, err)
	}
	// The old trunk is contained in the new one, though git says otherwise.
	if ok, err := containsCommit(context.Background(), f.root, p.old[1], TrunkRef); err != nil || !ok {
		t.Fatalf("the old tip is not contained: %v %v", ok, err)
	}
	// The idle lane is reset and reused, not refused as holding a change nobody landed.
	stager := &LaneStager{Root: f.root, Store: f.storePath}
	if err := stager.refuseUnlandedCommit(context.Background(), StageSpec{LaneID: "lane-1", BaseSHA: tip}, lane); err != nil {
		t.Fatalf("the idle lane was refused: %v", err)
	}
	// An unmapped commit in a lane is still a change nobody landed.
	writeForkFile(t, filepath.Join(lane, "stray.txt"), "stray\n")
	runGit(t, lane, "add", ".")
	runGit(t, lane, "commit", "-qm", "stray")
	if err := stager.refuseUnlandedCommit(context.Background(), StageSpec{LaneID: "lane-1", BaseSHA: tip}, lane); err == nil {
		t.Fatal("an unlanded commit was cleared")
	}

	absorbed, ok, err := func() (heavy.RewriteAbsorbed, bool, error) {
		s, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			return heavy.RewriteAbsorbed{}, false, err
		}
		defer s.Close()
		var a heavy.RewriteAbsorbed
		var ok bool
		err = s.ReadTx(context.Background(), func(r *store.Reader) error { var err error; a, ok, err = heavy.LatestRewrite(r); return err })
		return a, ok, err
	}()
	if err != nil || !ok || absorbed.Commits != 2 || !strings.Contains(absorbed.Line(), "absorbed a rewrite at") || !strings.Contains(absorbed.Line(), "2 commits mapped") {
		t.Fatalf("the one-line note: %+v %v %v", absorbed, ok, err)
	}
}

// A rewrite that drops or changes a landed commit is not absorbed: the hold stays and names
// the commit that has no match.
func TestRewriteThatDropsOrChangesALandingRaisesTheHold(t *testing.T) {
	for name, rewrite := range map[string]func(t *testing.T, p *pullFixture){
		"dropped": func(t *testing.T, p *pullFixture) {
			runGit(t, p.f.root, "reset", "-q", "--hard", "FETCH_HEAD")
			runGit(t, p.f.root, "cherry-pick", p.old[0])
		},
		"changed": func(t *testing.T, p *pullFixture) {
			runGit(t, p.f.root, "reset", "-q", "--hard", "FETCH_HEAD")
			runGit(t, p.f.root, "cherry-pick", p.old[0])
			writeForkFile(t, filepath.Join(p.f.root, "two.txt"), "changed\n")
			runGit(t, p.f.root, "add", ".")
			runGit(t, p.f.root, "commit", "-qm", "two.txt")
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPullFixture(t)
			runGit(t, p.f.root, "fetch", p.remote, "main")
			rewrite(t, p)
			historyPass(t, p.l, &p.result)
			h := p.history(t)
			if h.Lost != p.old[1] {
				t.Fatalf("the rewrite was absorbed: %+v", h)
			}
			if n := p.mapped(t); n != 0 {
				t.Fatalf("a refused rewrite mapped %d commits", n)
			}
			s, err := store.OpenRead(context.Background(), p.f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var reason string
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error { reason = rewriteReason(r, h); return nil }); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(reason, "trunk-rewritten: "+p.old[1]+" -> ") || !strings.Contains(reason, "no match on the trunk for "+p.old[1]) || strings.Contains(reason, p.old[0]+",") {
				t.Fatalf("the hold does not name the commit: %s", reason)
			}
		})
	}
}
