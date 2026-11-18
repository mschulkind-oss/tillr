package drain

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

func identityFixture(t *testing.T) (refusalLoopFixture, *Loop, *dirtyPathSteps) {
	t.Helper()
	f, l := passFixture(t, "item-a", "item-b")
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	return f, l, steps
}
func foreignCommit(t *testing.T, root, parent string, committerOnly bool) string {
	t.Helper()
	name, email := "Other", "other@example.invalid"
	if committerOnly {
		name, email = "tillr", "tillr@example.invalid"
	}
	cmd := exec.Command("git", "-C", root, "commit-tree", runGit(t, root, "rev-parse", parent+"^{tree}"), "-p", parent, "-m", "foreign")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_NAME=Other", "GIT_COMMITTER_EMAIL=other@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("foreign commit: %v %s", err, out)
	}
	sha := strings.TrimSpace(string(out))
	runGit(t, root, "update-ref", "refs/heads/main", sha, parent)
	return sha
}
func TestForeignIdentityHoldsCandidateNotRunningWorkAndReleases(t *testing.T) {
	for _, resolution := range []string{"accept", "reauthor"} {
		t.Run(resolution, func(t *testing.T) {
			f, l, steps := identityFixture(t)
			base := runGit(t, f.root, "rev-parse", "HEAD")
			c := historyCandidate(t, f, l, base)
			// Keep the other lane's dispatched run live throughout the hold.
			l.cfg.Lanes = []string{"lane-2"}
			p := mustPlan(t, l)[1]
			if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); !ok || err != nil {
				t.Fatalf("running lane: %v %v", ok, err)
			}
			l.cfg.Lanes = []string{"lane-1", "lane-2"}
			if _, err := l.observeTrunk(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			bad := foreignCommit(t, f.root, base, false)
			var result LoopResult
			historyPass(t, l, &result)
			if steps.lands != 0 || result.Rejected != 0 {
				t.Fatalf("foreign commit did not hold: %+v %+v", steps, result)
			}
			d := mustPlan(t, l)[0].decision
			if !strings.HasPrefix(d.Reason, "trunk-foreign-identity: "+bad) || !strings.Contains(d.Reason, "Other <other@example.invalid>") {
				t.Fatalf("identity missing: %+v", d)
			}
			running := mustPlan(t, l)[1].decision
			if running.Action != ActionHold || running.Reason != ReasonRunLive {
				t.Fatalf("stopped running work: %+v", running)
			}
			// Replacing the loop must not forget the unanswered commit or
			// discard either the candidate or the other lane's live run.
			l = f.loop(t, steps, &itemContentStager{})
			l.cfg.Lanes = []string{"lane-1", "lane-2"}
			if resolution == "accept" {
				fixtureWrite(t, f, func(tx *store.Tx) error { return land.AcceptIdentity(tx, bad, "owner", "intentional") })
			} else {
				// A proof of the unchanged tree follows the identity-only rewrite.
				runGit(t, f.root, "update-ref", GreenRef, bad)
				good := runGit(t, f.root, "commit-tree", runGit(t, f.root, "rev-parse", bad+"^{tree}"), "-p", base, "-m", "foreign")
				runGit(t, f.root, "reset", "--hard", good)
				if _, err := l.observeTrunk(context.Background(), ""); err != nil {
					t.Fatal(err)
				}
				if green := runGit(t, f.root, "rev-parse", GreenRef); green != good {
					t.Fatalf("proof left on lost identity: %s != %s", green, good)
				}
			}
			historyPass(t, l, &result)
			if steps.lands != 1 || result.Landed != 1 {
				t.Fatalf("hold did not release: %+v %+v candidate %s history %+v plan %+v", steps, result, c.HeadSHA, rewriteHistory(t, f), mustPlan(t, l)[0].decision)
			}
			if h := rewriteHistory(t, f); h.Lost != "" {
				t.Fatalf("identity-only rewrite left history held: %+v", h)
			}
		})
	}
}
func TestForeignCommitterAndBuriedCommitAreFound(t *testing.T) {
	f, l, _ := identityFixture(t)
	base := runGit(t, f.root, "rev-parse", "HEAD")
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	bad := foreignCommit(t, f.root, base, true)
	ownerCommit(t, f.root, "later.txt")
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		h, err := land.ReadIdentityHistory(r)
		if len(h.Pending) != 1 || h.Pending[0].SHA != bad || !strings.Contains(h.Pending[0].Committer, "Other") {
			t.Fatalf("buried committer missed: %+v", h)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
