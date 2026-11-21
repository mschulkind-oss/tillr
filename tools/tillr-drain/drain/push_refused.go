package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr/settings"
)

// A push the remote refuses because it has moved (a rejected ref "fetch first",
// "non-fast-forward" or "stale info") is not a push that failed for any other reason. Only
// the former is healed: the unpublished trunk commits are replayed, linear, onto the
// fetched remote tip under the landing lock, and the loop's own rewrite absorption
// (trunk_rewrite.go) then maps each replaced commit by patch or by the replay's own pairs
// and carries refs/tillr/green.
// A conflict, a merge commit among the unpublished commits, a checkout that is not on the
// trunk or is dirty on a changed path leaves the trunk and checkout unchanged and fails
// with the operator's commands.
// Never forced, never a merge commit. The conflict-repair work of
// docs/design/tillr-refused-push.md is not built.

// refusedPushError marks a push the remote refused because it has moved.
type refusedPushError struct{ detail string }

func (e *refusedPushError) Error() string { return e.detail }

var refusedLine = regexp.MustCompile(`(?m)^!\t[^\t]*\t\[rejected\] \((fetch first|non-fast-forward|stale info)\)`)

// pushCommand runs `git push --porcelain` and answers a *refusedPushError when the porcelain
// status for the ref says the remote moved. Server declines, hook failures and
// transport failures do not trigger replay.
func pushCommand(ctx context.Context, root string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"push", "--porcelain"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(stdout.String()+stderr.String(), "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	detail := fmt.Sprintf("git: %v: %s", err, strings.Join(lines, "\n"))
	switch {
	case refusedLine.MatchString(stdout.String()):
		return &refusedPushError{detail: detail}
	case remoteRejectedLine.MatchString(stdout.String()):
		return &pushStateError{kind: pushKindRemoteDeclined, detail: detail, escalated: "operator"}
	default:
		return &pushStateError{kind: pushKindFailed, detail: detail, noRefLine: !porcelainRefLine.MatchString(stdout.String())}
	}
}

// healResult describes a refused-push replay, including superseded commits in replayed.
type healResult struct {
	replayed  int
	remoteTip string
	base      string
	newTip    string
}

// healRefusedPush replays the unpublished trunk commits onto the remote's tip. It answers
// the number of commits replayed, or an error that names the mover and the commands.
func (l *Loop) healRefusedPush(ctx context.Context, p settings.Push, refused error) (int, error) {
	res, err := l.healRefusedPushResult(ctx, p, refused)
	return res.replayed, err
}

func (l *Loop) healRefusedPushResult(ctx context.Context, p settings.Push, refused error) (healResult, error) {
	root := l.cfg.Root
	var res healResult
	remote, ref, err := pushDestination(ctx, root, p)
	if err != nil {
		return res, err
	}
	fail := func(why string, a ...any) (healResult, error) {
		return res, fmt.Errorf("%s\nmover: operator. The push was refused because the remote moved and the drain could not rebase onto it (%s). The trunk and checkout were not changed. With the loop stopped, in %s: git fetch %s %s; git rebase FETCH_HEAD; then run the loop, which maps the rebased commits by patch (or tillr-drain accept-rewrite if it holds trunk-rewritten)",
			refused, fmt.Sprintf(why, a...), root, remote, ref)
	}
	prev := priorPublished(ctx, root, remote, ref)
	fctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	remoteTip, err := fetchPushDestination(fctx, root, remote, ref)
	if err != nil {
		return fail("%v", err)
	}
	res.remoteTip = remoteTip
	if err := checkPublishedAncestry(ctx, root, remote, ref, prev, remoteTip); err != nil {
		return res, err
	}
	// Pin the accepted tip even if replay is unnecessary or cannot proceed.
	// The identity check trusts commits under it, not commits replayed on top.
	if err := advancePublished(ctx, root, remote, ref, remoteTip); err != nil {
		return fail("pinning the fetched tip failed: %v", err)
	}
	// Record the trunk as it stands, so the replay is seen as a rewrite of this tip.
	if _, err := l.observeTrunk(ctx, ""); err != nil {
		return fail("%v", err)
	}

	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return fail("%v", err)
	}
	defer release()
	out, err := rawGit(ctx, root, nil, "rev-parse", "--verify", "refs/heads/"+TrunkRef+"^{commit}")
	if err != nil {
		return fail("%v", err)
	}
	tip := strings.TrimSpace(out)
	if ok, err := trunkAncestor(ctx, root, remoteTip, tip); err != nil {
		return fail("%v", err)
	} else if ok {
		return fail("the remote tip %s is already under the trunk, so a moved remote does not explain the refusal", shortSHA(remoteTip))
	}
	if base, err := rawGit(ctx, root, nil, "merge-base", remoteTip, tip); err == nil {
		res.base = strings.TrimSpace(base)
	}
	listed, err := rawGit(ctx, root, nil, "rev-list", "--reverse", "--topo-order", "--parents", remoteTip+".."+tip)
	if err != nil {
		return fail("%v", err)
	}
	cur := remoteTip
	known := &rewriteAbsorption{Lost: tip, Pairs: map[string]string{}}
	for _, line := range strings.Split(strings.TrimSpace(listed), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return fail("commit %s is a merge or a root commit", shortSHA(f[0]))
		}
		c, parent := f[0], f[1]
		mt := exec.CommandContext(ctx, "git", "-C", root, "merge-tree", "--write-tree", "--merge-base="+parent, cur, c)
		mt.Env = boundaryGitEnv()
		var mtErr bytes.Buffer
		mt.Stderr = &mtErr
		tree, err := mt.Output()
		if err != nil {
			return fail("replaying %s conflicts or failed: %s %s", shortSHA(c), strings.Join(strings.Fields(string(tree)), " "), strings.TrimSpace(mtErr.String()))
		}
		treeID, _, _ := strings.Cut(string(tree), "\n")
		known.Order = append(known.Order, c)
		curTree, err := rawGit(ctx, root, nil, "rev-parse", cur+"^{tree}")
		if err != nil {
			return fail("%v", err)
		}
		if strings.TrimSpace(curTree) == treeID {
			// The change is already present. Record its replacement without an empty commit.
			known.Pairs[c] = cur
			continue
		}
		meta, err := rawGit(ctx, root, nil, "log", "-1", "--date=raw", "--format=%an%n%ae%n%ad", c)
		if err != nil {
			return fail("%v", err)
		}
		m := strings.Split(strings.TrimRight(meta, "\n"), "\n")
		if len(m) != 3 {
			return fail("commit %s has unreadable author data", shortSHA(c))
		}
		msg, err := rawGit(ctx, root, nil, "log", "-1", "--format=%B", c)
		if err != nil {
			return fail("%v", err)
		}
		ct := exec.CommandContext(ctx, "git", "-C", root, "commit-tree", treeID, "-p", cur)
		ct.Env = append(boundaryGitEnv(), "GIT_AUTHOR_NAME="+m[0], "GIT_AUTHOR_EMAIL="+m[1], "GIT_AUTHOR_DATE="+m[2])
		ct.Stdin = strings.NewReader(msg)
		var ctErr bytes.Buffer
		ct.Stderr = &ctErr
		made, err := ct.Output()
		if err != nil {
			return fail("recreating %s failed: %v: %s", shortSHA(c), err, strings.TrimSpace(ctErr.String()))
		}
		cur = strings.TrimSpace(string(made))
		known.Pairs[c] = cur
	}
	known.Tip = cur
	known.Evidence = "replayed onto " + remote + " " + shortSHA(remoteTip) + " under the landing lock; each replacement was determined by a three-way merge"
	// Move the trunk, and the checkout with it. `reset --keep` refuses, changing nothing,
	// when a path the move touches has local changes, and keeps changes elsewhere.
	head, _ := rawGit(ctx, root, nil, "symbolic-ref", "-q", "HEAD")
	if strings.TrimSpace(head) != "refs/heads/"+TrunkRef {
		return fail("the main checkout is not on %s", TrunkRef)
	}
	if got, err := rawGit(ctx, root, nil, "rev-parse", "refs/heads/"+TrunkRef); err != nil || strings.TrimSpace(got) != tip {
		return fail("the trunk moved while the replay was built")
	}
	if _, err := rawGit(ctx, root, nil, "reset", "--keep", "-q", cur); err != nil {
		return fail("the main checkout is dirty on a path the move changes: %v", err)
	}
	res.replayed, res.newTip = len(known.Order), cur
	release()
	// Prefer patch matching; the pairs built under the lock cover changed patches.
	if len(known.Order) == 0 {
		known = nil
	}
	if _, err := l.observeTrunkWith(ctx, "", known); err != nil {
		return res, fmt.Errorf("trunk rebased onto %s, recording the rewrite failed: %w", shortSHA(remoteTip), err)
	}
	return res, nil
}
