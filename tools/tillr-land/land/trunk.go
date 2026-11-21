package land

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// Trunk is the git ref a candidate lands on. It is the one external write this utility
// makes, and it is why every face takes --repo: the ref is read before a decision and
// moved only by a landed attempt.
type Trunk struct {
	repo string
	ctx  context.Context
}

// OpenTrunk answers the trunk of the repository at repo. A path that is not a git work
// tree is refused here rather than at the first read, so a face that cannot reach the
// repository says so before it opens a store.
func OpenTrunk(repo string) (*Trunk, error) {
	if repo == "" {
		return nil, Usagef("a repository path is required")
	}
	t := &Trunk{repo: repo}
	if _, err := t.git("rev-parse", "--git-dir"); err != nil {
		return nil, Usagef("%s is not a git repository: %v", repo, err)
	}
	return t, nil
}

// SetContext binds subsequent Git reads to the caller's cancellation.
func (t *Trunk) SetContext(ctx context.Context) { t.ctx = ctx }

// Head answers the commit a ref points at.
func (t *Trunk) Head(ref string) (string, error) {
	out, err := t.git("rev-parse", "--verify", normalizeRef(ref)+"^{commit}")
	if err != nil {
		return "", Externalf("reading %s in %s: %v", ref, t.repo, err)
	}
	return strings.TrimSpace(out), nil
}

// HasCommit answers whether a sha names a commit in this repository. A candidate whose
// base or head the repository does not hold is malformed input, not a refusal: there is
// nothing to evaluate.
func (t *Trunk) HasCommit(sha string) error {
	if _, err := t.git("cat-file", "-e", sha+"^{commit}"); err != nil {
		return Usagef("%s names no commit in %s", sha, t.repo)
	}
	return nil
}

// Squash is what squashing a candidate's change onto a trunk produced: the commit that
// would land, the tree it carries, and — when the change and the trunk touched the same
// lines — the conflicts git reported instead of a tree.
type Squash struct {
	Commit    string
	Tree      string
	Conflicts []string
	// Empty is a squash whose merged tree is the trunk's own: the trunk already carries
	// the whole change. It names no commit, because the only commit it could write is an
	// empty one (landing principle 10: already present is a finding, not a landing).
	Empty bool
}

// Conflicted reports whether the change does not squash onto the trunk. A conflicted squash
// names no commit: the tree git merged holds conflict markers, and a change that cannot be
// squashed must never land.
func (s Squash) Conflicted() bool { return len(s.Conflicts) > 0 }

// Squash re-applies the change base..head onto trunk and answers the one commit that
// carries it there, without moving any ref and without a working tree.
//
// It is landing principles 2 and 3 in one call: the change is squashed, not the patch, so
// a replayed diff that differs is the expected result on a moved trunk, and the squashed
// commit is written with the repository's scope-checked identity rather than the lane's.
// `git merge-tree --write-tree --merge-base=<base>` is the three-way merge git's own
// cherry-pick uses, on trees rather than on a checkout; `git commit-tree` writes the commit
// with explicit GIT_AUTHOR_* and GIT_COMMITTER_* from LandingIdentity() and the candidate's
// message: a one-commit range keeps its message unchanged, while a multi-commit range takes
// the first commit's message and lists the remaining commits' subjects in the body. A commit
// never names a queue item (ruling D-RS3 in docs/design/queue-persistence-ruling.md),
// because the item is private store state and the shared receipt is the item block's absence
// from its document. The author date is the first commit's, so the change keeps the date it
// was authored; the committer date is the landing's clock. Squash takes no item id, so it
// has none to write.
//
// Nothing here writes a ref, an index or a working tree, so check and pass compute the same
// commit. `--merge-base` needs git 2.38 or newer.
func (t *Trunk) Squash(base, trunk, head string, at time.Time) (Squash, error) {
	return t.SquashWithMessage(base, trunk, head, head, at)
}

// MessageSource resolves an explicit commit and checks membership in base..head,
// including head and excluding base and all commits reachable from base.
func (t *Trunk) MessageSource(base, head, source string) (string, error) {
	resolved, err := t.git("rev-parse", "--verify", source+"^{commit}")
	if err != nil {
		return "", Usagef("--message-from %s names no commit", source)
	}
	resolved = strings.TrimSpace(resolved)
	commits, err := t.git("rev-list", base+".."+head)
	if err != nil {
		return "", Externalf("reading candidate range: %v", err)
	}
	for _, sha := range strings.Fields(commits) {
		if sha == resolved {
			return resolved, nil
		}
	}
	return "", Usagef("--message-from %s is not a commit in %s..%s", source, base, head)
}

// SquashWithMessage merges head's tree and builds the commit message and author date from
// the commits in base..source: a single commit keeps its message and author date verbatim;
// more than one commit uses the first commit's subject (and body, if any) and lists the
// other commits' subjects in the body, with the first commit's author date.
// A merge that answers trunk's own tree is an Empty squash and writes no commit.
func (t *Trunk) SquashWithMessage(base, trunk, head, source string, at time.Time) (Squash, error) {
	identity, err := t.LandingIdentity()
	if err != nil {
		return Squash{}, err
	}
	out, stderr, err := t.runGit(nil, "merge-tree", "--write-tree", "--merge-base="+base, trunk, head)
	if err != nil {
		if conflicts := conflictReports(out); len(conflicts) > 0 {
			return Squash{Conflicts: conflicts}, nil
		}
		return Squash{}, Externalf("squashing %s onto %s in %s: %s",
			head, trunk, t.repo, failureText(stderr, err))
	}
	tree, _, _ := strings.Cut(out, "\n")
	tree = strings.TrimSpace(tree)
	if tree == "" {
		return Squash{}, Externalf("squashing %s onto %s in %s answered no tree", head, trunk, t.repo)
	}
	trunkTree, err := t.TreeOf(trunk)
	if err != nil {
		return Squash{}, err
	}
	if tree == trunkTree {
		return Squash{Tree: tree, Empty: true}, nil
	}
	message, authorDate, err := t.squashMessageAndDate(base, source)
	if err != nil {
		return Squash{}, err
	}
	commit, stderr, err := t.runGit([]string{
		"GIT_AUTHOR_NAME=" + identity.Name,
		"GIT_AUTHOR_EMAIL=" + identity.Email,
		"GIT_AUTHOR_DATE=" + authorDate,
		"GIT_COMMITTER_NAME=" + identity.Name,
		"GIT_COMMITTER_EMAIL=" + identity.Email,
		"GIT_COMMITTER_DATE=" + at.Local().Format(time.RFC3339),
	}, "commit-tree", tree, "-p", trunk, "-m", message)
	if err != nil {
		return Squash{}, Externalf("writing the squash of %s onto %s in %s: %s",
			head, trunk, t.repo, failureText(stderr, err))
	}
	return Squash{Commit: strings.TrimSpace(commit), Tree: tree}, nil
}

// squashMessageAndDate reads the commits in base..source in chronological order. A single
// commit keeps its message verbatim; multiple commits keep the first commit's subject (and
// body, if any) and list the remaining commits' subjects in the body.
func (t *Trunk) squashMessageAndDate(base, source string) (string, string, error) {
	revs, err := t.git("rev-list", "--reverse", base+".."+source)
	if err != nil {
		return "", "", Externalf("reading candidate commits %s..%s in %s: %v", base, source, t.repo, err)
	}
	commits := strings.Fields(revs)
	first := source
	if len(commits) > 0 {
		first = commits[0]
	}
	raw, err := t.git("cat-file", "commit", first)
	if err != nil {
		return "", "", Externalf("reading the message of %s in %s: %v", first, t.repo, err)
	}
	_, message, ok := strings.Cut(raw, "\n\n")
	if !ok {
		return "", "", Externalf("commit %s has no message boundary", first)
	}
	authorDate, err := t.git("log", "-1", "--format=%aI", first)
	if err != nil {
		return "", "", Externalf("reading the author date of %s in %s: %v", first, t.repo, err)
	}
	if len(commits) <= 1 {
		return message, strings.TrimSpace(authorDate), nil
	}
	var bullets []string
	for _, sha := range commits[1:] {
		subj, err := t.git("log", "-1", "--format=%s", sha)
		if err != nil {
			return "", "", Externalf("reading the subject of %s in %s: %v", sha, t.repo, err)
		}
		bullets = append(bullets, "- "+strings.TrimSpace(subj))
	}
	message = strings.TrimRight(message, "\r\n") + "\n\n" + strings.Join(bullets, "\n") + "\n"
	return message, strings.TrimSpace(authorDate), nil
}

// PreserveRange pins the squashed head and lists its commits in the run's directory.
// It happens before the trunk move: an unwritable receipt must not silently lose history.
func (t *Trunk) PreserveRange(runID, base, head, dir string) error {
	list, err := t.git("log", "--reverse", "--format=%H %s", base+".."+head)
	if err != nil {
		return Externalf("listing the commits of %s: %v", runID, err)
	}
	if err := t.Pin(runID, head); err != nil {
		return err
	}
	if dir == "" {
		common, err := t.git("rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return Externalf("reading the run artifact root: %v", err)
		}
		dir = filepath.Join(strings.TrimSpace(common), "tillr", "runs", runID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Externalf("creating run directory %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commits.txt"), []byte(list), 0o644); err != nil {
		return Externalf("writing commit list for %s: %v", runID, err)
	}
	return nil
}

// Pin answers refs/tillr/runs/<run_id> at sha. Both landed and refused heads are pinned:
// otherwise resetting the lane could make the original range unreachable, allowing git
// garbage collection to remove it. The ref is a pin, not a promotion: it lives under
// refs/tillr/ and is retired with the epoch.
func (t *Trunk) Pin(runID, sha string) error {
	if runID == "" {
		return Usagef("a run id is required to pin a head")
	}
	if _, err := t.git("update-ref", "refs/tillr/runs/"+runID, sha); err != nil {
		return Externalf("pinning refs/tillr/runs/%s at %s in %s: %v", runID, sha, t.repo, err)
	}
	return nil
}

// landedMarkerRef is the ref a landing points at its squash commit before it moves the
// trunk. It is the lander's own record, in Git, of which commit a run's landing wrote: Git's
// trunk move and the store's receipt are two writes, so a process killed between them, or
// an OS crash that keeps the ref and loses the store's last commits, leaves a trunk that
// carries a landing no `landings` row names. The marker is how a retry of that run finds the
// commit again instead of squashing the change a second time. It lives under refs/tillr/
// beside the run pins, is never pushed, and names no item: the commit itself stays the
// head's message only (ruling D-RS3).
func landedMarkerRef(runID string) string { return "refs/tillr/landed/" + runID }

// checkRunRef refuses a run id that is not a valid ref name component, by git's
// check-ref-format rules. The id is spliced into the marker's ref name and from there into a
// revision expression, so an id carrying revision syntax (`x~1`, `x^`, `x@{1}`) would read a
// different object than the marker; it is refused before any Git read rather than left for
// a later update-ref to reject.
func checkRunRef(runID string) error {
	bad := func(why string) error {
		return Usagef("run id %q cannot name a ref (%s): use letters, digits, '-', '_' and '.'", runID, why)
	}
	if runID == "" || runID == "@" {
		return bad("empty or '@'")
	}
	if strings.Contains(runID, "..") || strings.Contains(runID, "@{") || strings.Contains(runID, "//") {
		return bad("'..', '@{' or '//'")
	}
	if strings.HasSuffix(runID, "/") || strings.HasSuffix(runID, ".") {
		return bad("ends with '/' or '.'")
	}
	for _, c := range runID {
		if c < 0x20 || c == 0x7f || strings.ContainsRune(" ~^:?*[\\", c) {
			return bad(fmt.Sprintf("contains %q", c))
		}
	}
	for _, component := range strings.Split(runID, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return bad("a component starts with '.' or ends with '.lock'")
		}
	}
	return nil
}

// LandedMarker answers the commit a previous landing of runID wrote, if any.
func (t *Trunk) LandedMarker(runID string) (string, bool, error) {
	if err := checkRunRef(runID); err != nil {
		return "", false, err
	}
	out, stderr, err := t.runGit(nil, "rev-parse", "--verify", "--quiet", landedMarkerRef(runID)+"^{commit}")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && strings.TrimSpace(stderr) == "" {
			return "", false, nil
		}
		return "", false, Externalf("reading %s in %s: %s", landedMarkerRef(runID), t.repo, failureText(stderr, err))
	}
	return strings.TrimSpace(out), true, nil
}

// MarkLanded points runID's marker at the squash commit its landing is about to publish.
// It is written before the trunk moves, so a move that survives a crash always has one;
// a landing refused or failed after this leaves a marker at a commit the trunk does not
// carry, which RecoveredLanding ignores.
func (t *Trunk) MarkLanded(runID, commit string) error {
	if err := checkRunRef(runID); err != nil {
		return err
	}
	if _, err := t.git("update-ref", landedMarkerRef(runID), commit); err != nil {
		return Externalf("marking %s at %s in %s: %v", landedMarkerRef(runID), commit, t.repo, err)
	}
	return nil
}

// RecoveredLanding answers whether marker is a landing of base..head that the trunk at tip
// already carries, and the trunk commit it was squashed onto. Both must hold, so no tree
// comparison alone can satisfy it: the marker commit is in tip's history (an interleaved
// later landing does not hide it), and it is exactly the squash of base..head onto its own
// parent (a later, different head for the same run is not it). An unrelated commit with the
// same tree is never the marker's commit, because only the lander writes the marker, and it
// writes one only for a non-empty squash, so a marker commit always changed something.
func (t *Trunk) RecoveredLanding(base, head, marker, tip string) (string, bool, error) {
	onTrunk, err := t.historyAncestor(marker, tip)
	if err != nil || !onTrunk {
		return "", false, err
	}
	parent, err := t.git("rev-parse", "--verify", marker+"^1")
	if err != nil {
		return "", false, nil
	}
	parent = strings.TrimSpace(parent)
	out, stderr, err := t.runGit(nil, "merge-tree", "--write-tree", "--merge-base="+base, parent, head)
	if err != nil {
		if len(conflictReports(out)) > 0 {
			return "", false, nil
		}
		return "", false, Externalf("re-squashing %s onto %s in %s: %s", head, parent, t.repo, failureText(stderr, err))
	}
	merged, _, _ := strings.Cut(out, "\n")
	merged = strings.TrimSpace(merged)
	markerTree, err := t.TreeOf(marker)
	if err != nil {
		return "", false, err
	}
	return parent, merged == markerTree, nil
}

// Identity is the author and committer a landing publishes: the identity the repository's
// deployed git config states.
type Identity struct {
	Name  string
	Email string
}

// laneWritableScopes are the config scopes a lane can write. `local` is the repository's
// own config, shared by every worktree of the checkout, so a lane's `git config user.name`
// lands there and would otherwise be the identity every landing in this repository
// publishes; `worktree` is per-worktree and equally not the deployed identity. A landing
// refuses both rather than reading past them, because the value a lane wrote is exactly the
// value tests/git-identity.test.py refuses.
var laneWritableScopes = map[string]bool{"local": true, "worktree": true}

// configEntry is one value git config reports for a key, and the scope it came from.
type configEntry struct {
	Scope string
	Value string
}

// LandingIdentity answers the identity a landing must publish, read from the scopes a lane
// cannot write, and refuses when the repository states none or states one a lane can
// replace. The refusal is not a decision about a candidate: a repository that cannot say
// who commits is a repository no landing may publish to, so it is an external failure and
// nothing is written.
func (t *Trunk) LandingIdentity() (Identity, error) {
	identity := Identity{}
	for _, field := range []struct {
		key  string
		into *string
	}{
		{"user.name", &identity.Name},
		{"user.email", &identity.Email},
	} {
		entries, err := t.configEntries(field.key)
		if err != nil {
			return Identity{}, err
		}
		for _, entry := range entries {
			if laneWritableScopes[entry.Scope] {
				return Identity{}, Externalf("%s sets %s = %q in its %s config, so the identity a commit carries is a value a lane can write: unset it and let the deployed global identity answer",
					t.repo, field.key, entry.Value, entry.Scope)
			}
			*field.into = entry.Value
		}
	}
	if identity.Name == "" || identity.Email == "" {
		return Identity{}, Externalf("the landing identity is not configured in %s (git user.name and user.email)", t.repo)
	}
	return identity, nil
}

// configEntries answers every value git config holds for key, in the order it reports them.
// `--show-scope` names the layer each came from, which is what tells the deployed identity
// from one a lane wrote. An unset key is an empty list rather than a failed read: git
// answers exit 1 for it, and that is an answer.
func (t *Trunk) configEntries(key string) ([]configEntry, error) {
	out, stderr, err := t.runGit(nil, "config", "--show-scope", "--get-all", key)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil
		}
		return nil, Externalf("reading %s in %s: %s", key, t.repo, failureText(stderr, err))
	}
	var entries []configEntry
	for _, line := range strings.Split(out, "\n") {
		scope, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "\t")
		if !ok {
			continue
		}
		entries = append(entries, configEntry{Scope: scope, Value: strings.TrimSpace(value)})
	}
	return entries, nil
}

// Branch answers the branch the repository is checked out on, and refuses a detached HEAD:
// a landing onto no branch is a landing in the wrong place.
func (t *Trunk) Branch() (string, error) {
	name, err := t.git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", Externalf("reading the branch of %s: %v", t.repo, err)
	}
	if name = strings.TrimSpace(name); name == "HEAD" || name == "" {
		return "", Usagef("%s is on a detached HEAD: name the branch to land onto", t.repo)
	}
	return name, nil
}

// conflictReports answers the CONFLICT lines in a merge's output. git writes them to
// stdout beside the tree it merged and exits non-zero, so the exit status alone cannot
// tell a conflict from a repository that would not merge at all.
func conflictReports(out string) []string {
	var reports []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "CONFLICT") {
			reports = append(reports, line)
		}
	}
	return reports
}

// failureText answers what to report about a git command that failed: git's own stderr,
// and the exit status when it printed nothing.
func failureText(stderr string, err error) string {
	if msg := strings.TrimSpace(stderr); msg != "" {
		return msg
	}
	return err.Error()
}

var errTrunkChanged = errors.New("trunk tip changed during landing preparation")

// failedMove distinguishes a stale squash from a Git failure. An unreadable ref, a
// delivery ref, or a tip that may already carry the attempted move is not a retry signal.
func (t *Trunk) failedMove(ref, to, from string, failure error) error {
	if PatchRef(ref) == "" {
		if tip, err := t.Head(ref); err == nil && tip != from {
			if published, err := t.historyAncestor(to, tip); err == nil && !published {
				return errTrunkChanged
			}
		}
	}
	return failure
}

// Move points ref at to, and only if it is at from, and leaves the worktree that has ref
// checked out holding what to holds.
//
// A bare `update-ref` was this function's first shape, and on 2026-11-13 it moved a trunk
// and left the repository it landed in with its index and working tree at the pre-landing
// content: `git status` then reported the landed change in reverse, 4 insertions and 242
// deletions over the five files the candidate had added
// (docs/design/tillr-drain-acceptance-2026-11-13.md §4.1). A landing is a write to the
// repository, not to a ref, and a ref write is invisible to the index and the files.
//
// So when a worktree of this repository has ref checked out, the move is git's own
// fast-forward in that worktree: the branch, the index and the files advance as one step.
// The compare-and-swap survives as the branch read immediately before the merge and the
// read after it — a branch that is not at from is refused rather than moved through, and a
// fast-forward that did not end at to is a failure rather than a landing.
//
// Two properties of the merge are load-bearing and are git's, not this function's:
// uncommitted content on a path the move changes stops it before anything moves, and
// merge.autoStash=false is what keeps `git config merge.autoStash` from absorbing that
// content into conflict markers and exiting 0 instead. Nothing is reset, stashed or
// discarded. The refusal a clash produces is decided by the caller before this runs, where
// it is a decision about the repository and not about the candidate; a clash that appears
// in the window between that decision and this write is an external failure here.
//
// A repository whose trunk is not checked out anywhere — a bare one, or one whose trunks
// are only refs — has no index or working tree to keep in step, so the guarded update-ref
// is the whole of the move there.
//
// The swap names the tip the change was re-applied onto. A stale squash must be
// prepared again; other write failures still leave as external errors.
func (t *Trunk) Move(ref, to, from string) error {
	worktree, err := t.WorktreeFor(ref)
	if err != nil {
		return err
	}
	if worktree == "" {
		if _, err := t.git("update-ref", normalizeRef(ref), to, from); err != nil {
			return t.failedMove(ref, to, from, Externalf("moving %s to %s in %s: %v", ref, to, t.repo, err))
		}
		return nil
	}
	at, err := t.gitIn(worktree, "rev-parse", "--verify", normalizeRef(ref)+"^{commit}")
	if err != nil {
		return Externalf("reading %s in %s: %v", ref, worktree, err)
	}
	if at = strings.TrimSpace(at); at != from {
		if PatchRef(ref) == "" {
			return errTrunkChanged
		}
		return Externalf("moving %s to %s in %s: %s is at %s, not the %s the landing was decided against",
			ref, to, t.repo, ref, at, from)
	}
	if _, stderr, err := t.runGitIn(worktree, nil,
		"-c", "merge.autoStash=false", "merge", "--ff-only", "--no-edit", to); err != nil {
		return Externalf("fast-forwarding %s to %s in %s: %s", ref, to, worktree, failureText(stderr, err))
	}
	after, err := t.gitIn(worktree, "rev-parse", "--verify", normalizeRef(ref)+"^{commit}")
	if err != nil {
		return Externalf("reading %s in %s after the fast-forward: %v", ref, worktree, err)
	}
	if after = strings.TrimSpace(after); after != to {
		return Externalf("fast-forwarding %s to %s in %s left it at %s", ref, to, worktree, after)
	}
	return nil
}

// WorktreeFor answers the path of the worktree of this repository whose checked-out branch
// is ref, and "" when no worktree has it checked out. `git worktree list` answers every
// worktree of the checkout, including when it is asked from a linked one, which is what a
// landing needs: the trunk is usually checked out somewhere other than the lane landing
// onto it. A worktree with a detached HEAD is not one of them — a detached HEAD is not the
// branch, and moving it is not this landing's act.
func (t *Trunk) WorktreeFor(ref string) (string, error) {
	out, err := t.git("worktree", "list", "--porcelain")
	if err != nil {
		return "", Externalf("listing the worktrees of %s: %v", t.repo, err)
	}
	want := normalizeRef(ref)
	worktree := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			worktree = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			if strings.TrimPrefix(line, "branch ") == want {
				return worktree, nil
			}
		}
	}
	return "", nil
}

// UncommittedPaths answers the paths a worktree holds content for that its own HEAD does not:
// everything `git diff HEAD` reports, staged or not, plus the untracked files a checkout
// refuses to overwrite. Ignored files are not content a fast-forward overwrites, so they are
// not listed — a repository is dirty with an ignored build artifact and clean for the git
// `git status` reports by default.
func (t *Trunk) UncommittedPaths(worktree string) ([]string, error) {
	tracked, err := t.gitIn(worktree, "diff", "--no-renames", "--name-only", "HEAD")
	if err != nil {
		return nil, Externalf("reading the uncommitted paths of %s: %v", worktree, err)
	}
	untracked, err := t.gitIn(worktree, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, Externalf("reading the untracked paths of %s: %v", worktree, err)
	}
	return append(pathLines(tracked), pathLines(untracked)...), nil
}

// pathLines answers the non-empty lines of a git command's path list.
func pathLines(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

// LandLock answers the lock this repository's landings take: `land.lock` beside the
// repository's common git directory. The shared location is the point, not a convention:
// every landing path in this checkout takes this one file, so a hand-run landing and a
// drain's cannot both decide on one trunk. It is also where the retired merge queue took
// its lock, so a lock it left held is still respected rather than raced.
//
// `--git-common-dir` in a linked worktree is the main checkout's git directory, so a
// landing started from a lane takes the same lock as one started from the checkout.
func (t *Trunk) LandLock() (*Lock, error) {
	out, err := t.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, Externalf("reading the git dir of %s: %v", t.repo, err)
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(t.repo, dir)
	}
	return OpenLock(filepath.Join(dir, "land.lock"))
}

// gitEnv is this process's environment with the repository variables removed. An
// inherited GIT_DIR would redirect every read to whatever repository the caller's shell
// was in, so a face handed --repo would silently evaluate a different repository than
// the one it was told about. They are removed rather than blanked: git reads an empty
// GIT_DIR as a path and refuses it, so `GIT_DIR=` is not the same as unset.
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

// gitEnvWith is gitEnv with the caller's own variables added. A variable the environment
// already carries is removed first: os/exec passes duplicates through, and the first one
// is what a program reads, so appending alone would silently keep the inherited value.
func gitEnvWith(extra []string) []string {
	env := gitEnv()
	if len(extra) == 0 {
		return env
	}
	overridden := map[string]bool{}
	for _, entry := range extra {
		if key, _, ok := strings.Cut(entry, "="); ok {
			overridden[key] = true
		}
	}
	kept := env[:0]
	for _, entry := range env {
		if key, _, ok := strings.Cut(entry, "="); ok && overridden[key] {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, extra...)
}

// PatchRef answers the patch branch a candidate's base_ref names, or "" when the ref is
// not a patch branch. A patch branch is a branch under refs/heads/patch/: it is the
// delivery a fork-series item moves, and it is a source of truth, so a landing that would
// rewind it is refused rather than carried.
func PatchRef(baseRef string) string {
	ref := normalizeRef(baseRef)
	if strings.HasPrefix(ref, "refs/heads/patch/") && len(ref) > len("refs/heads/patch/") {
		return ref
	}
	return ""
}

// normalizeRef accepts a ref as a caller writes it and as git stores it. §5.4's
// candidate carries a `base_ref`, and a caller naming `master` and one naming
// `refs/heads/master` mean the same ref; a bare name is a branch.
func normalizeRef(ref string) string {
	if strings.HasPrefix(ref, "refs/") {
		return ref
	}
	return "refs/heads/" + ref
}

// git runs one git command against this trunk's repository and answers its stdout.
func (t *Trunk) git(args ...string) (string, error) {
	return t.gitIn(t.repo, args...)
}

// gitIn runs one git command against dir and answers its stdout.
func (t *Trunk) gitIn(dir string, args ...string) (string, error) {
	out, stderr, err := t.runGitIn(dir, nil, args...)
	if err != nil {
		return "", errors.New(failureText(stderr, err))
	}
	return out, nil
}

// runGit runs one git command and answers both streams, for the commands that report on
// stdout and fail: a merge writes the tree it merged and the conflicts it met to stdout,
// and the exit status is non-zero for both.
func (t *Trunk) runGit(extraEnv []string, args ...string) (string, string, error) {
	return t.runGitIn(t.repo, extraEnv, args...)
}

// runGitIn runs one git command against dir rather than this trunk's repository: a landing's
// fast-forward happens in the worktree that has the trunk checked out, which is not
// necessarily the directory the face was pointed at.
func (t *Trunk) runGitIn(dir string, extraEnv []string, args ...string) (string, string, error) {
	return t.runGitStdin(dir, nil, extraEnv, args...)
}

// runGitStdin is runGitIn with the command's standard input.
func (t *Trunk) runGitStdin(dir string, stdin []byte, extraEnv []string, args ...string) (string, string, error) {
	// No git command starts while this goroutine holds the store's write lock: the guard
	// refuses it here, before the process exists (tools/tillr/store/ioguard.go).
	if err := store.CheckIO("git " + gitSubcommand(args)); err != nil {
		return "", "", err
	}
	if OwnerHooks.GitCall != nil {
		OwnerHooks.GitCall(dir, args)
	}
	ctx := t.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnvWith(extraEnv)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	started := time.Now()
	trace := os.Getenv("TILLR_SQLITE_TRACE") == "1"
	if trace {
		t.traceGit(dir, args, "begin", started, nil)
	}
	out, err := cmd.Output()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if trace {
		t.traceGit(dir, args, "end", started, err)
	}
	return string(out), stderr.String(), err
}

func (t *Trunk) traceGit(dir string, args []string, phase string, started time.Time, err error) {
	// Keep commit messages and environment out of diagnostics; the subcommand is
	// enough to correlate the remaining Move/history spans with SQLite's writer.
	command := gitSubcommand(args)
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	fmt.Fprintf(os.Stderr, "tillr-git at=%s pid=%d repo=%q command=%q phase=%s elapsed=%s error=%q\n", time.Now().UTC().Format(time.RFC3339Nano), os.Getpid(), dir, command, phase, time.Since(started), errorText)
}

// gitSubcommand names a git invocation by its subcommand, past one leading `-c key=value`.
func gitSubcommand(args []string) string {
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	if command == "-c" && len(args) > 2 {
		command = args[2]
	}
	return command
}

// IsAncestor answers whether ancestor is reachable from descendant in this repository. It
// is the question a patch branch's rewind refusal asks: a head that does not descend from
// the branch's tip would drop the commits between them.
func (t *Trunk) IsAncestor(ancestor, descendant string) bool {
	_, err := t.git("merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

// MergeBase answers the commit two commits diverge from: the base a candidate's change is
// measured from, which is the trunk's own history and not the trunk's tip.
func (t *Trunk) MergeBase(a, b string) (string, error) {
	out, err := t.git("merge-base", a, b)
	if err != nil {
		return "", Externalf("reading the merge base of %s and %s in %s: %v", a, b, t.repo, err)
	}
	return strings.TrimSpace(out), nil
}

// TreeOf answers the tree a commit carries, which is what a verdict binds.
func (t *Trunk) TreeOf(commit string) (string, error) {
	out, err := t.git("rev-parse", "--verify", commit+"^{tree}")
	if err != nil {
		return "", Externalf("reading the tree of %s in %s: %v", commit, t.repo, err)
	}
	return strings.TrimSpace(out), nil
}

// syscallENOTDIR is what lstat answers when a parent of the path is a file.
var syscallENOTDIR = syscall.ENOTDIR
