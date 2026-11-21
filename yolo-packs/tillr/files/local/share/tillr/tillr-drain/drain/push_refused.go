package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/settings"
)

// A push the remote refuses because it has moved (a rejected ref "fetch first",
// "non-fast-forward" or "stale info") is not a push that failed for any other reason. Only
// the former is healed: the unpublished trunk commits are replayed, linear, onto the
// fetched remote tip under the landing lock, and the loop's own rewrite absorption
// (trunk_rewrite.go) then maps each replaced commit by patch or by the replay's own pairs
// and carries refs/tillr/green.
// A conflict leaves main, green and the checkout unchanged and files a fenced repair.
// A merge among unpublished commits or a checkout that cannot be reset safely requires
// the operator. Never force a push or write a merge commit on main.

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
	// Pin the accepted tip for future ancestry checks even if replay cannot proceed.
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
	trunk, err := land.OpenTrunk(root)
	if err != nil {
		return fail("%v", err)
	}
	trunk.SetContext(ctx)
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
		mt := exec.CommandContext(ctx, "git", "-C", root, "merge-tree", "--write-tree", "--name-only", "-z", "--merge-base="+parent, cur, c)
		mt.Env = boundaryGitEnv()
		var mtErr bytes.Buffer
		mt.Stderr = &mtErr
		tree, err := mt.Output()
		if err != nil {
			if paths, detail := parseMergeTreeConflict(string(tree)); len(paths) > 0 && l.cfg.Store != "" {
				remoteMsgs, commitMsg, summary := l.collectRemoteSyncContext(ctx, res.base, remoteTip, c)
				st := remoteSyncState{ItemID: RemoteSyncItemID(remoteTip), Remote: remote, Ref: ref, RemoteTip: remoteTip,
					MergeBase: res.base, BaseSHA: cur, ConflictCommit: c, ConflictParent: parent,
					ConflictPaths: paths, ConflictOutput: detail, RemoteMessages: remoteMsgs, CommitMessage: commitMsg,
					LandingSummary: summary, OldTrunk: tip, ReplayedPairs: known.Pairs, ReplayedOrder: known.Order}
				conflict, err := l.fileOrUpdateRemoteSyncItemLocked(ctx, st, "")
				if err != nil {
					return fail("filing %s: %v", st.ItemID, err)
				}
				return res, conflict
			}
			return fail("replaying %s conflicts or failed: %s %s", shortSHA(c), strings.Join(strings.Fields(string(tree)), " "), strings.TrimSpace(mtErr.String()))
		}
		treeID := strings.TrimRight(string(tree), "\n\x00")
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
		cur, err = commitReplay(ctx, trunk, root, treeID, cur, c)
		if err != nil {
			return fail("%v", err)
		}
		known.Pairs[c] = cur
	}
	known.Tip = cur
	known.Evidence = "replayed onto " + remote + " " + shortSHA(remoteTip) + " under the landing lock; each replacement was determined by a three-way merge"
	// Move the trunk, and the checkout with it. `reset --keep` refuses, changing nothing,
	// when a path the move touches has local changes, and keeps changes elsewhere.
	dirty := func(detail string) (healResult, error) {
		return res, &pushStateError{kind: pushKindWorktreeDirty, remoteTip: remoteTip,
			resetTarget: cur, escalated: "operator", detail: fmt.Sprintf("%v\nmover: operator. %s", refused, detail)}
	}
	if !onMain(ctx, root) {
		return dirty("The main checkout is not on " + TrunkRef + "; check out " + TrunkRef + " to allow replay.")
	}
	if got, err := rawGit(ctx, root, nil, "rev-parse", "refs/heads/"+TrunkRef); err != nil || strings.TrimSpace(got) != tip {
		return fail("the trunk moved while the replay was built")
	}
	if _, err := rawGit(ctx, root, nil, "reset", "--keep", "-q", cur); err != nil {
		return dirty(fmt.Sprintf("The main checkout refused the replay reset; commit, stash or restore the clashing changes: %v", err))
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
