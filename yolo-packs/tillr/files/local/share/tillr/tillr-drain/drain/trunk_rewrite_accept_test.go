package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

var acceptAt = time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC)

// reauthor recreates every commit in base..tip with an edited message, and moves the last
// commit's content into the one before it, leaving the last empty: the commits are as many,
// and the tip has the same tree, but those two have not the patches they had. Identity-only rewrites and rebases that keep each
// commit's patch recover automatically (trunk_rewrite_pull_test.go); these fixtures must
// still exercise the broader, explicitly accepted content-preserving rewrite.
func reauthor(t *testing.T, root, base, tip string) string {
	t.Helper()
	parent := base
	commits := strings.Fields(runGit(t, root, "rev-list", "--reverse", base+".."+tip))
	for i, c := range commits {
		tree := runGit(t, root, "rev-parse", c+"^{tree}")
		if i >= len(commits)-2 {
			tree = runGit(t, root, "rev-parse", tip+"^{tree}")
		}
		msg := runGit(t, root, "log", "-1", "--format=%B", c) + " [edited]"
		cmd := exec.Command("git", "-C", root, "commit-tree", tree, "-p", parent, "-m", msg)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=tillr", "GIT_AUTHOR_EMAIL=tillr@example.invalid",
			"GIT_COMMITTER_NAME=tillr", "GIT_COMMITTER_EMAIL=tillr@example.invalid")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("commit-tree: %v", err)
		}
		parent = strings.TrimSpace(string(out))
	}
	if parent == tip {
		t.Fatal("re-authoring reproduced the same commit")
	}
	return parent
}

func rewriteHistory(t *testing.T, f refusalLoopFixture) trunkHistory {
	t.Helper()
	r, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var h trunkHistory
	if err := r.ReadTx(context.Background(), func(tx *store.Reader) error { var err error; h, err = readTrunkHistory(tx); return err }); err != nil {
		t.Fatal(err)
	}
	return h
}

func acceptRewrite(f refusalLoopFixture, lost, current string) (RewriteAcceptance, error) {
	return AcceptRewrite(context.Background(), AcceptRewriteRequest{
		Root: f.root, Store: f.storePath, Lost: lost, Current: current,
		By: "owner", Why: "re-authored two commits", At: acceptAt,
	})
}

func ownerCommit(t *testing.T, root, name string) string {
	t.Helper()
	writeForkFile(t, filepath.Join(root, name), name+"\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", name)
	return runGit(t, root, "rev-parse", "HEAD")
}

// A re-authored trunk keeps every tree. Accepting it clears the hold, adopts the
// rewritten tip, pins the lost tip as evidence, and the retained already-gated
// candidate, staged on a commit only the old history has, lands without a second gate.
func TestAcceptedReauthoringLandsTheRetainedCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	base := runGit(t, f.root, "rev-parse", "HEAD")
	ownerCommit(t, f.root, "one.txt")
	lost := ownerCommit(t, f.root, "two.txt")
	candidate := historyCandidate(t, f, l, lost)
	if _, err := steps.Gate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := appendJudgment(context.Background(), f.storePath, candidateGatedKind, "run", candidate.RunID, candidate, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	current := reauthor(t, f.root, base, lost)
	runGit(t, f.root, "reset", "-q", "--hard", current)
	var result LoopResult
	historyPass(t, l, &result)
	if d := mustPlan(t, l)[0].decision; d.Action != ActionHold || !strings.HasPrefix(d.Reason, "trunk-rewritten: "+lost+" -> "+current) {
		t.Fatalf("the rewrite did not hold: %+v", d)
	}
	if steps.lands != 0 {
		t.Fatalf("landed over a rewrite: %+v", steps)
	}

	got, err := acceptRewrite(f, lost, current)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got.Lost != lost || got.Current != current || got.Pin != acceptedTrunkPrefix+lost {
		t.Fatalf("acceptance: %+v", got)
	}
	if h := rewriteHistory(t, f); h.Lost != "" || h.Observed != current {
		t.Fatalf("history after acceptance: %+v", h)
	}
	if pin := runGit(t, f.root, "rev-parse", acceptedTrunkPrefix+lost); pin != lost {
		t.Fatalf("lost tip not pinned: %s", pin)
	}
	if out := runGit(t, f.root, "for-each-ref", lostTrunkRef); out != "" {
		t.Fatalf("lost ref kept: %s", out)
	}
	r, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	var payload string
	err = r.ReadTx(context.Background(), func(tx *store.Reader) error {
		return tx.QueryRow(`SELECT payload FROM events WHERE kind=?`, rewriteAcceptedKind).Scan(&payload)
	})
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{lost, current, `"by":"owner"`, `"why":"re-authored two commits"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("event %s lacks %s", payload, want)
		}
	}

	historyPass(t, l, &result)
	if steps.lands != 1 || steps.gates != 1 || result.Landed != 1 {
		t.Fatalf("the retained candidate did not land without a gate: %+v / %+v", steps, result)
	}
	if _, err := acceptRewrite(f, lost, current); err == nil || !strings.Contains(err.Error(), "no rewrite is recorded") {
		t.Fatalf("a second acceptance: %v", err)
	}
}

// The operator does not need to know whether a pass has seen the rewrite yet. The proof
// pointer, which the rewrite moved off the trunk, moves to the commit that replaced it.
func TestAcceptBeforeTheLoopObservesTheRewrite(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	green := ownerCommit(t, f.root, "one.txt")
	runGit(t, f.root, "update-ref", GreenRef, green)
	ownerCommit(t, f.root, "two.txt")
	lost := ownerCommit(t, f.root, "three.txt")
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	current := reauthor(t, f.root, base, lost)
	runGit(t, f.root, "reset", "-q", "--hard", current)
	got, err := acceptRewrite(f, lost, current)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if h := rewriteHistory(t, f); h.Lost != "" || h.Observed != current {
		t.Fatalf("history: %+v", h)
	}
	carried := runGit(t, f.root, "rev-parse", GreenRef)
	if carried != runGit(t, f.root, "rev-parse", current+"~2") || carried == green || got.GreenFrom != green || got.GreenTo != carried {
		t.Fatalf("green not carried: %s (from %s) / %+v", carried, green, got)
	}
	if runGit(t, f.root, "rev-parse", carried+"^{tree}") != runGit(t, f.root, "rev-parse", green+"^{tree}") {
		t.Fatal("green carried to a different tree")
	}
	h, err := l.observeTrunk(context.Background(), "")
	if err != nil || h.Lost != "" {
		t.Fatalf("the next pass re-held: %+v / %v", h, err)
	}
}

func TestAcceptRewriteRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		rewrite    func(t *testing.T, f refusalLoopFixture, base, lost string) (named, current string)
	}{
		{"tree-differs", "trees differ", func(t *testing.T, f refusalLoopFixture, base, lost string) (string, string) {
			runGit(t, f.root, "reset", "-q", "--hard", base)
			c := ownerCommit(t, f.root, "other.txt")
			return lost, c
		}},
		{"commit-count-differs", "commit counts differ", func(t *testing.T, f refusalLoopFixture, base, lost string) (string, string) {
			squashed := runGit(t, f.root, "commit-tree", lost+"^{tree}", "-p", base, "-m", "squashed")
			runGit(t, f.root, "reset", "-q", "--hard", squashed)
			return lost, squashed
		}},
		{"wrong-lost-tip", "the recorded lost tip is", func(t *testing.T, f refusalLoopFixture, base, lost string) (string, string) {
			c := reauthor(t, f.root, base, lost)
			runGit(t, f.root, "reset", "-q", "--hard", c)
			return base, c
		}},
		{"current-off-trunk", "is not on the trunk", func(t *testing.T, f refusalLoopFixture, base, lost string) (string, string) {
			c := reauthor(t, f.root, base, lost)
			runGit(t, f.root, "reset", "-q", "--hard", base)
			ownerCommit(t, f.root, "elsewhere.txt")
			return lost, c
		}},
		{"green-off-both-histories", "on neither the trunk nor the lost history", func(t *testing.T, f refusalLoopFixture, base, lost string) (string, string) {
			runGit(t, f.root, "checkout", "-q", "-b", "side", base)
			runGit(t, f.root, "update-ref", GreenRef, ownerCommit(t, f.root, "side.txt"))
			runGit(t, f.root, "checkout", "-q", "main")
			c := reauthor(t, f.root, base, lost)
			runGit(t, f.root, "reset", "-q", "--hard", c)
			return lost, c
		}},
		{"no-rewrite", "no rewrite is recorded", func(t *testing.T, f refusalLoopFixture, base, lost string) (string, string) {
			return lost, ownerCommit(t, f.root, "advance.txt")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			base := runGit(t, f.root, "rev-parse", "HEAD")
			ownerCommit(t, f.root, "one.txt")
			lost := ownerCommit(t, f.root, "two.txt")
			if _, err := l.observeTrunk(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			named, current := tc.rewrite(t, f, base, lost)
			if _, err := l.observeTrunk(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			before := rewriteHistory(t, f)
			_, err := acceptRewrite(f, named, current)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a refusal naming %q, got %v", tc.want, err)
			}
			if after := rewriteHistory(t, f); after != before {
				t.Fatalf("a refusal changed history: %+v -> %+v", before, after)
			}
			if out := runGit(t, f.root, "for-each-ref", acceptedTrunkPrefix, observedTrunkRef); !strings.Contains(out, before.Observed) || strings.Contains(out, acceptedTrunkPrefix) {
				t.Fatalf("a refusal pinned: %s", out)
			}
		})
	}
}

// rebaseOnto replays base..tip onto a trunk that gained a commit of its own, as
// `git pull --rebase` does to unpushed landings. skip names files whose commits are dropped.
func rebaseOnto(t *testing.T, root, base, tip string, skip ...string) string {
	t.Helper()
	commits := strings.Fields(runGit(t, root, "rev-list", "--reverse", base+".."+tip))
	runGit(t, root, "reset", "-q", "--hard", base)
	ownerCommit(t, root, "upstream.txt")
next:
	for _, c := range commits {
		subject := runGit(t, root, "log", "-1", "--format=%s", c)
		for _, s := range skip {
			if s == subject {
				continue next
			}
		}
		runGit(t, root, "cherry-pick", c)
	}
	return runGit(t, root, "rev-parse", "HEAD")
}

// A pull that rebases unpushed landings onto a moved remote changes every sha and the
// tip's tree. Each lost commit still has a commit with the same patch on the trunk, so
// the rewrite kept the content and the operator's acceptance takes it before any pass has
// seen it; the next pass does not hold.
func TestAcceptARebaseOntoAMovedBase(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	ownerCommit(t, f.root, "one.txt")
	lost := ownerCommit(t, f.root, "two.txt")
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	current := rebaseOnto(t, f.root, base, lost)
	if runGit(t, f.root, "rev-parse", lost+"^{tree}") == runGit(t, f.root, "rev-parse", current+"^{tree}") {
		t.Fatal("the fixture's rebase kept the tree, so it does not exercise the moved base")
	}
	// No pass runs between the rebase and the acceptance: a pass now absorbs this rewrite
	// itself (TestPullRebaseOfUnpushedLandingsIsAbsorbed), so the operator's command is
	// exercised on the rewrite no pass has recorded yet.
	got, err := acceptRewrite(f, lost, current)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if !got.Rebased || got.Commits != 2 {
		t.Fatalf("acceptance = %+v, want a rebase of 2 commits", got)
	}
	if runGit(t, f.root, "rev-parse", acceptedTrunkPrefix+lost) != lost {
		t.Fatal("the lost tip is not pinned")
	}
	if h, err := l.observeTrunk(context.Background(), ""); err != nil || h.Lost != "" || h.Observed != current {
		t.Fatalf("the next pass re-held: %+v / %v", h, err)
	}
}

// A rebase that dropped a landed commit did not keep the content. It is refused, the
// refusal names the commit with no match, and nothing changes.
func TestAcceptRefusesARebaseThatDroppedACommit(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	one := ownerCommit(t, f.root, "one.txt")
	lost := ownerCommit(t, f.root, "two.txt")
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	current := rebaseOnto(t, f.root, base, lost, "one.txt")
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	before := rewriteHistory(t, f)
	_, err := acceptRewrite(f, lost, current)
	if err == nil || !strings.Contains(err.Error(), "trees differ") || !strings.Contains(err.Error(), one) {
		t.Fatalf("want a refusal naming the dropped commit %s, got %v", one, err)
	}
	if after := rewriteHistory(t, f); after != before {
		t.Fatalf("a refusal changed history: %+v -> %+v", before, after)
	}
}
