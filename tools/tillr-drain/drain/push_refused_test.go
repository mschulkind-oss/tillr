package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// Use three different identities so a replay cannot accidentally use the
// original author or the ambient Git committer as its committer.
func setReplayIdentity(t *testing.T, root string) {
	t.Helper()
	config := filepath.Join(filepath.Dir(root), "replay.gitconfig")
	writeForkFile(t, config, "[user]\n name = Replay Lander\n email = replay@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_COMMITTER_NAME", "Ambient Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "ambient@example.invalid")
}

func assertReplayIdentity(t *testing.T, root, old, replayed string) {
	t.Helper()
	const author = "%an <%ae>|%aI|%B"
	if got, want := runGit(t, root, "show", "-s", "--format="+author, replayed), runGit(t, root, "show", "-s", "--format="+author, old); got != want {
		t.Errorf("replay of %s changed author, date or message: %q != %q", old, got, want)
	}
	if got := runGit(t, root, "show", "-s", "--format=%cn <%ce>", replayed); got != "Replay Lander <replay@example.invalid>" {
		t.Errorf("replay of %s has committer %q, want configured identity", old, got)
	}
}

// The remote has a commit the trunk lacks: the unpublished commits are replayed on top of
// it, linear, every one present by patch, green carried by patch, and the next push works.
func TestRefusedPushRebasesOntoTheMovedRemote(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	remoteTip := p.remoteCommit(t, "remote.txt", "remote\n")
	err := p.refused(t)
	setReplayIdentity(t, p.f.root)
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
	replayed := strings.Fields(runGit(t, root, "rev-list", "--reverse", remoteTip+"..HEAD"))
	for i, sha := range replayed {
		assertReplayIdentity(t, root, p.local[i], sha)
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

// A refused push files work without touching main; a gated repair makes it publishable.
func TestRefusedPushConflictRepairsAndPublishes(t *testing.T) {
	for _, mode := range []string{settings.PushGreen, settings.PushTip} {
		t.Run(mode, func(t *testing.T) {
			p := newPushFixture(t)
			ctx := context.Background()
			root := p.f.root
			p.l.cfg.PostLanding.Push.What = mode
			remoteTip := p.remoteCommit(t, "two.txt", "remote version\n")
			runGit(t, p.remote, "config", "receive.denyNonFastForwards", "true")
			trace := filepath.Join(t.TempDir(), "git-trace")
			t.Setenv("GIT_TRACE", trace)
			before := runGit(t, root, "rev-parse", "HEAD")
			green := runGit(t, root, "rev-parse", GreenRef)
			p.l.triggerPushNow(ctx)
			waitAction(t, &p.l.pushAction)
			id := RemoteSyncItemID(remoteTip)
			if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindConflict || !strings.Contains(fmt.Sprint(ev["detail"]), "mover: "+id) {
				t.Fatalf("refusal did not file repair work: %+v", ev)
			}
			if runGit(t, root, "rev-parse", "HEAD") != before || runGit(t, root, "rev-parse", GreenRef) != green || runGit(t, root, "status", "--porcelain") != "" {
				t.Fatal("a conflict changed the trunk, green or the checkout")
			}
			st, ok, err := p.l.readRemoteSyncState(ctx, id)
			if err != nil || !ok {
				t.Fatalf("missing staged conflict: %+v, %v", st, err)
			}
			lane := p.l.cfg.Lanes[0]
			wt := p.l.laneTarget(lane)
			runGit(t, wt, "checkout", "-q", "--detach", remoteSyncPinRef(id))
			worker := exec.CommandContext(ctx, "sh", "-ec", `
				printf 'resolved two\n' > two.txt
				git add two.txt
				git commit -qm 'resolve two.txt'
			`)
			worker.Dir = wt
			if out, err := worker.CombinedOutput(); err != nil {
				t.Fatalf("repair worker: %v: %s", err, out)
			}
			c := Candidate{ItemID: id, LaneID: lane, RunID: "run-sync-repair", Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: runGit(t, wt, "rev-parse", "HEAD")}
			gateRemoteSyncRepair(t, p, c)
			p.l.cfg.Steps = nil // settle the generated item in-process
			var result LoopResult
			tip, landed, err := p.l.landCandidate(ctx, c, &result, nil)
			if err != nil || !landed || tip == "" || result.Landed != 1 {
				t.Fatalf("repair landing = %q, %v, %v; landed %d", tip, landed, err, result.Landed)
			}
			waitAction(t, &p.l.pushAction)
			if mode == settings.PushGreen {
				if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindProving || ev["rebased_tip"] != tip {
					t.Fatalf("repair did not wait for proof: %+v", ev)
				}
				if got := runGit(t, p.remote, "rev-parse", TrunkRef); got != remoteTip {
					t.Fatalf("remote published before proof: %s", got)
				}
				p.l.gatePolicy.LandingStrategy = "after-merge"
				p.l.cfg.Steps = heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
					return []HeavyCheck{{CheckID: "fixture", Result: "pass"}}, nil
				}}
				if err := p.l.maybeRolling(ctx, tip, false, &result); err != nil {
					t.Fatal(err)
				}
				p.l.heavyWG.Wait()
				waitAction(t, &p.l.pushAction)
				if got := runGit(t, root, "rev-parse", GreenRef); got != tip {
					t.Fatalf("proof advanced green to %s, want %s", got, tip)
				}
			}
			if ev := lastPushEvent(t, p.f); ev["kind"] != pushKindOK || ev["remote_tip"] != tip {
				t.Fatalf("publication did not clear the push waiting state: %+v", ev)
			}
			if got := runGit(t, p.remote, "rev-parse", TrunkRef); got != tip {
				t.Fatalf("remote tip = %s, want repaired tip %s", got, tip)
			}
			if runGit(t, root, "merge-base", remoteTip, TrunkRef) != remoteTip || runGit(t, root, "rev-list", "--merges", "--count", TrunkRef) != "0" {
				t.Fatal("main was not replayed linearly atop the remote")
			}
			if got := runGit(t, root, "show", tip+":two.txt"); got != "resolved two" {
				t.Fatalf("published repair content = %q", got)
			}
			if got := runGit(t, root, "rev-list", "--count", remoteTip+".."+tip); got != "3" {
				t.Fatalf("published %s commits beyond the remote tip, want the repair and both clean commits", got)
			}
			log, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			pushes := 0
			for _, line := range strings.Split(string(log), "\n") {
				if _, args, ok := strings.Cut(line, "built-in: git push "); ok {
					pushes++
					for _, arg := range strings.Fields(args) {
						if strings.HasPrefix(arg, "--force") || strings.HasPrefix(arg, "+") || arg == "-f" {
							t.Fatalf("forced push: %s", line)
						}
					}
				}
			}
			if pushes < 2 {
				t.Fatalf("trace shows %d pushes; want the refusal and publication", pushes)
			}
		})
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
	waitAction(t, &p.l.pushAction)
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
	waitAction(t, &p.l.pushAction)
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

func TestRefusedPushPinsFetchedTip(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	runGit(t, p.other, "-c", "user.name=Other", "-c", "user.email=other@example.invalid", "commit", "-q", "--allow-empty", "-m", "from another machine")
	runGit(t, p.other, "push", "-q", "origin", TrunkRef)
	foreign := runGit(t, p.other, "rev-parse", "HEAD")
	err := p.refused(t)
	if _, herr := p.l.healRefusedPush(ctx, settings.Push{}, err); herr != nil {
		t.Fatal(herr)
	}
	if runGit(t, p.f.root, "rev-parse", "--verify", "refs/tillr/published/origin/"+TrunkRef) != foreign {
		t.Fatal("the fetched tip was not pinned")
	}
}
