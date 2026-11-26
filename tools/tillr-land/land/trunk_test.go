package land_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

// The identity a fixture repository deploys. It is the value every fixture commit is made
// with, so a squash that publishes the deployed identity is visible as a commit whose
// author and committer are this, and a squash that copied the lane's would be a different
// one.
const (
	fixtureName  = "tillr"
	fixtureEmail = "tillr@example.invalid"
)

func gitEnv() []string {
	env := os.Environ()
	kept := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitAs(t, dir, fixtureName, fixtureEmail, args...)
}

func requireFixtureCommitRepo(t testing.TB, dir string, args []string) {
	t.Helper()
	commit := false
	for _, arg := range args {
		if arg == "commit" || arg == "commit-tree" {
			commit = true
			break
		}
	}
	if !commit {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving fixture git dir %q: %v", dir, err)
	}
	cmd := exec.Command("git", "-C", absDir, "rev-parse", "--git-dir")
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") || strings.HasPrefix(entry, "GIT_CEILING_DIRECTORIES=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(absDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refusing git %v in %s: directory is not its own repository (%v: %s)", args, dir, err, strings.TrimSpace(string(out)))
	}
}

func gitAs(t *testing.T, dir, name, email string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, dir, args)
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

// deployIdentity points the test process's git at a config file that states the identity a
// landing publishes, which is the scope LandingIdentity reads.
func deployIdentity(t *testing.T) {
	t.Helper()
	path := filepath.Join(scratch(t), "gitconfig")
	write(t, path, "[user]\n\tname = "+fixtureName+"\n\temail = "+fixtureEmail+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", path)
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(key, "")
	}
}

// repo is a trunk at base with a candidate branch one commit ahead, and the trunk checked
// out at base: the state every landing starts from.
type repo struct {
	dir     string
	base    string
	head    string
	headTre string
}

func newRepo(t *testing.T) repo {
	t.Helper()
	deployIdentity(t)
	dir := scratch(t)
	git(t, dir, "init", "-b", "main")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	git(t, dir, "add", "base.txt")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "candidate")
	write(t, filepath.Join(dir, "candidate.txt"), "candidate\n")
	git(t, dir, "add", "candidate.txt")
	git(t, dir, "commit", "-m", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	headTree := git(t, dir, "rev-parse", "HEAD^{tree}")
	git(t, dir, "checkout", "main")
	return repo{dir: dir, base: base, head: head, headTre: headTree}
}

func (r repo) trunk(t *testing.T) *land.Trunk {
	t.Helper()
	trunk, err := land.OpenTrunk(r.dir)
	if err != nil {
		t.Fatalf("opening the trunk: %v", err)
	}
	return trunk
}

// TestSquashWritesOneCommitWithNoItemTrailerAndTheDeployedIdentity is the whole of landing
// principle 10's squash: one commit, authored and committed by the deployed identity, that
// carries the head's message and names no queue item (ruling D-RS3: commits never carry a
// `Tillr-Item:` trailer; the shared receipt is the item block's absence).
func TestSquashWritesOneCommitWithNoItemTrailerAndTheDeployedIdentity(t *testing.T) {
	r := newRepo(t)
	trunk := r.trunk(t)
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	squash, err := trunk.Squash(r.base, r.base, r.head, at)
	if err != nil {
		t.Fatalf("squashing: %v", err)
	}
	if squash.Conflicted() {
		t.Fatalf("a change onto its own base conflicted: %v", squash.Conflicts)
	}
	if got := git(t, r.dir, "rev-parse", squash.Commit+"^{tree}"); got != r.headTre {
		t.Fatalf("the squash tree is %s, want the head's tree %s", got, r.headTre)
	}
	if got := git(t, r.dir, "rev-parse", squash.Commit+"^"); got != r.base {
		t.Fatalf("the squash parent is %s, want the trunk %s", got, r.base)
	}
	message := git(t, r.dir, "log", "-1", "--format=%B", squash.Commit)
	if strings.Contains(message, "Tillr-Item") {
		t.Fatalf("the squash commit carries an item trailer:\n%s", message)
	}
	if !strings.Contains(message, "candidate") {
		t.Fatalf("the squash dropped the head's message:\n%s", message)
	}
	identity := git(t, r.dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>", squash.Commit)
	want := fixtureName + " <" + fixtureEmail + ">|" + fixtureName + " <" + fixtureEmail + ">"
	if identity != want {
		t.Fatalf("the squash identity is %q, want %q", identity, want)
	}
}

// TestSquashTitlesAMultiCommitChangeAfterTheFirstCommitAndListsTheRest: when an item's
// change spans more than one commit, the squashed commit carries the first commit's subject
// and lists the remaining commits' subjects in its body.
func TestSquashTitlesAMultiCommitChangeAfterTheFirstCommitAndListsTheRest(t *testing.T) {
	r := newRepo(t)
	git(t, r.dir, "checkout", "candidate")
	write(t, filepath.Join(r.dir, "second.txt"), "second\n")
	git(t, r.dir, "add", "second.txt")
	git(t, r.dir, "commit", "-m", "test(tillr): cover edge cases")
	write(t, filepath.Join(r.dir, "third.txt"), "third\n")
	git(t, r.dir, "add", "third.txt")
	git(t, r.dir, "commit", "-m", "docs(tillr): note new behavior")
	head := git(t, r.dir, "rev-parse", "HEAD")
	git(t, r.dir, "checkout", "main")

	squash, err := r.trunk(t).Squash(r.base, r.base, head, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("squashing: %v", err)
	}
	if got, want := git(t, r.dir, "log", "-1", "--format=%s", squash.Commit), "candidate"; got != want {
		t.Fatalf("the squash subject is %q, want %q", got, want)
	}
	wantBody := "- test(tillr): cover edge cases\n- docs(tillr): note new behavior"
	if got := git(t, r.dir, "log", "-1", "--format=%b", squash.Commit); got != wantBody {
		t.Fatalf("the squash body is %q, want %q", got, wantBody)
	}
}

// TestSquashStampsTheCommitterDateInTheMachinesLocalZone: the landing's committer date is
// the landing instant written in the zone of the machine that landed it, not in UTC, so a
// landed commit reads in the same zone as a commit made by hand on that machine (owner
// ruling, 2026-11-15). The instant itself is unchanged.
func TestSquashStampsTheCommitterDateInTheMachinesLocalZone(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("fixture", -5*60*60)
	t.Cleanup(func() { time.Local = saved })

	r := newRepo(t)
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	squash, err := r.trunk(t).Squash(r.base, r.base, r.head, at)
	if err != nil {
		t.Fatalf("squashing: %v", err)
	}
	if got, want := git(t, r.dir, "log", "-1", "--format=%cI", squash.Commit), "2026-11-15T07:00:00-05:00"; got != want {
		t.Fatalf("the committer date is %s, want the landing instant in the local zone, %s", got, want)
	}
}

// TestSquashOnAConflictNamesTheConflictAndNoCommit: a change and a trunk that touched the
// same line have no tree the verdict could be carried to, so the squash names the conflict
// and writes no commit.
func TestSquashOnAConflictNamesTheConflictAndNoCommit(t *testing.T) {
	deployIdentity(t)
	dir := scratch(t)
	git(t, dir, "init", "-b", "main")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	git(t, dir, "add", "base.txt")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "candidate")
	write(t, filepath.Join(dir, "base.txt"), "candidate\n")
	git(t, dir, "commit", "-am", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "main")
	write(t, filepath.Join(dir, "base.txt"), "trunk\n")
	git(t, dir, "commit", "-am", "trunk")
	trunk := git(t, dir, "rev-parse", "HEAD")

	opened, err := land.OpenTrunk(dir)
	if err != nil {
		t.Fatalf("opening the trunk: %v", err)
	}
	squash, err := opened.Squash(base, trunk, head, time.Now())
	if err != nil {
		t.Fatalf("squashing: %v", err)
	}
	if !squash.Conflicted() {
		t.Fatalf("a same-line change squashed cleanly: %+v", squash)
	}
	if squash.Commit != "" {
		t.Fatalf("a conflicted squash named a commit %s", squash.Commit)
	}
}

// TestPinAnswersTheRunRef: the ref a non-landing pins is refs/tillr/runs/<run_id>, at the
// head the landing inspected, so a garbage collection cannot take it.
func TestPinAnswersTheRunRef(t *testing.T) {
	r := newRepo(t)
	if err := r.trunk(t).Pin("run-1", r.head); err != nil {
		t.Fatalf("pinning: %v", err)
	}
	if got := git(t, r.dir, "rev-parse", "refs/tillr/runs/run-1"); got != r.head {
		t.Fatalf("refs/tillr/runs/run-1 is %s, want %s", got, r.head)
	}
}

// TestBranchRefusesADetachedHead: a landing onto no branch is a landing in the wrong place.
func TestBranchRefusesADetachedHead(t *testing.T) {
	r := newRepo(t)
	git(t, r.dir, "checkout", "--detach", r.base)
	if _, err := r.trunk(t).Branch(); err == nil {
		t.Fatal("a detached HEAD named a branch")
	}
}
