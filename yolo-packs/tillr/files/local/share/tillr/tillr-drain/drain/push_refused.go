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
// (trunk_rewrite.go) then maps each replaced commit by patch and carries refs/tillr/green.
// A conflict, a merge commit among the unpublished commits, a checkout that is not on the
// trunk or is dirty on a changed path changes nothing and fails with the operator's commands.
// Never forced, never a merge commit. A remote whose tip does not descend from the published
// pointer was force-pushed and is never rebased onto. A conflict is repaired as work (remote_sync.go) per
// docs/design/tillr-refused-push.md, OQ-RP2.

// refusedPushError marks a push the remote refused because it has moved.
type refusedPushError struct{ detail string }

func (e *refusedPushError) Error() string { return e.detail }

var refusedLine = regexp.MustCompile(`(?m)^!\t[^\t]*\t\[rejected\] \((fetch first|non-fast-forward|stale info)\)`)

// pushCommand runs `git push --porcelain` and answers a *refusedPushError when the porcelain
// status for the ref says the remote moved. A server-side decline ("[remote rejected]"), a
// hook failure and a transport failure stay ordinary errors.
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
		// The server answered and declined; the remote did not move, so nothing is fetched.
		return &pushStateError{kind: pushKindRemoteDeclined, detail: detail, escalated: "operator"}
	}
	return &pushStateError{kind: pushKindFailed, detail: detail, noRefLine: !porcelainRefLine.MatchString(stdout.String())}
}

// healResult is what a remote rebase did: the commits it replayed, the remote tip it
// replayed them onto, the merge-base of the old trunk and that tip, and the trunk's new tip.
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
	failKind := func(kind string, why string, a ...any) (healResult, error) {
		return res, &pushStateError{kind: kind, remoteTip: res.remoteTip, escalated: "operator", detail: fmt.Sprintf("%s\nmover: operator. The push was refused because the remote moved and the drain could not rebase onto it (%s). Nothing was changed. With the loop stopped, in %s: git fetch %s %s; git rebase FETCH_HEAD; then run the loop, which maps the rebased commits by patch (or tillr-drain accept-rewrite if it holds trunk-rewritten)",
			refused, fmt.Sprintf(why, a...), root, remote, ref)}
	}
	fail := func(why string, a ...any) (healResult, error) {
		_, err := failKind(pushKindFailed, why, a...)
		err.(*pushStateError).escalated = ""
		return res, err
	}
	// Record the trunk as it stands, so the replay is seen as a rewrite of this tip.
	if _, err := l.observeTrunk(ctx, ""); err != nil {
		return fail("%v", err)
	}
	// What this clone last confirmed on the remote, before the fetch can move the
	// remote-tracking ref.
	prev := priorPublished(ctx, root, remote, ref)
	fctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	if _, err := rawGit(fctx, root, nil, "fetch", "--", remote, ref); err != nil {
		return fail("fetching %s %s failed: %v", remote, ref, err)
	}
	out, err := rawGit(ctx, root, nil, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return fail("%v", err)
	}
	remoteTip := strings.TrimSpace(out)
	res.remoteTip = remoteTip
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if prev != "" {
		forward, err := trunkAncestor(ctx, root, prev, remoteTip)
		if err != nil {
			return fail("%v", err)
		}
		if !forward {
			// Someone force-pushed the remote branch. Replaying local commits, which hold
			// what the force-push removed, and pushing them would publish them again.
			return res, &pushStateError{kind: pushKindRewritten, remoteTip: remoteTip, previousTip: prev, escalated: "operator",
				detail: fmt.Sprintf("remote-rewritten: %s/%s was force-pushed (%s -> %s); autopush stopped so the commits it dropped are not published again.\nmover: operator; reconcile force-pushed %s/%s (%s -> %s). Nothing was changed",
					remote, branch, shortSHA(prev), shortSHA(remoteTip), remote, branch, shortSHA(prev), shortSHA(remoteTip))}
		}
	}

	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return fail("%v", err)
	}
	defer release()
	out, err = rawGit(ctx, root, nil, "rev-parse", "--verify", "refs/heads/"+TrunkRef+"^{commit}")
	if err != nil {
		return fail("%v", err)
	}
	tip := strings.TrimSpace(out)
	if ok, err := trunkAncestor(ctx, root, remoteTip, tip); err != nil {
		return fail("%v", err)
	} else if ok {
		release()
		_ = l.closeStaleRemoteSyncItems(ctx)
		return fail("the remote tip %s is already under the trunk, so a moved remote does not explain the refusal", shortSHA(remoteTip))
	}
	if b, err := rawGit(ctx, root, nil, "merge-base", remoteTip, tip); err == nil {
		res.base = strings.TrimSpace(b)
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
			conflictPaths, conflictOut := parseMergeTreeConflict(string(tree))
			if len(conflictPaths) > 0 && l.cfg.Store != "" {
				release()
				if _, perr := rawGit(ctx, root, nil, "update-ref", publishedRef(remote, ref), remoteTip); perr != nil {
					return fail("pinning the fetched tip failed: %v", perr)
				}
				remoteMsgs, commitMsg, landingSummary := l.collectRemoteSyncContext(ctx, res.base, remoteTip, c)
				syncID := RemoteSyncItemID(remoteTip)
				st := remoteSyncState{
					ItemID:         syncID,
					Remote:         remote,
					Ref:            ref,
					RemoteTip:      remoteTip,
					MergeBase:      res.base,
					BaseSHA:        cur,
					ConflictCommit: c,
					ConflictParent: parent,
					ConflictPaths:  conflictPaths,
					ConflictOutput: conflictOut,
					RemoteMessages: remoteMsgs,
					CommitMessage:  commitMsg,
					LandingSummary: landingSummary,
					OldTrunk:       tip,
					ReplayedPairs:  known.Pairs,
					ReplayedOrder:  known.Order,
				}
				escalatedOwner, ferr := l.fileOrUpdateRemoteSyncItem(ctx, st)
				if ferr != nil {
					return failKind(pushKindConflict, "replaying %s conflicted and filing %s failed: %v", shortSHA(c), syncID, ferr)
				}
				if escalatedOwner {
					return res, &pushStateError{
						kind:      pushKindConflict,
						remoteTip: remoteTip,
						escalated: "owner",
						detail:    fmt.Sprintf("remote-sync-conflict: %s conflicted on gate code paths while rebasing onto %s/%s (%s)\nmover: owner; resolve gate-code conflict onto %s/%s (%s)", syncID, remote, branch, shortSHA(remoteTip), remote, branch, shortSHA(remoteTip)),
					}
				}
				return res, &pushStateError{
					kind:      pushKindConflict,
					remoteTip: remoteTip,
					detail:    fmt.Sprintf("remote-sync-conflict: %s; resolve rebase conflict onto %s/%s (%s) on %s\nmover: %s; resolve rebase conflict onto %s/%s (%s)", syncID, remote, branch, shortSHA(remoteTip), strings.Join(conflictPaths, ", "), syncID, remote, branch, shortSHA(remoteTip)),
				}
			}
			return failKind(pushKindConflict, "replaying %s conflicts or failed: %s %s", shortSHA(c), strings.Join(strings.Fields(string(tree)), " "), strings.TrimSpace(mtErr.String()))
		}
		treeID, _, _ := strings.Cut(string(tree), "\n")
		known.Order = append(known.Order, c)
		if curTree, err := rawGit(ctx, root, nil, "rev-parse", cur+"^{tree}"); err != nil {
			return fail("%v", err)
		} else if strings.TrimSpace(curTree) == treeID {
			// What this commit changed is already in the tree it would be replayed onto, so
			// it has nothing left to add: it is replaced by what it would have followed.
			known.Pairs[c] = cur
			res.replayed++
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
		res.replayed++
	}
	res.newTip = cur
	known.Tip = cur
	known.Evidence = "replayed onto " + remote + " " + shortSHA(remoteTip) + " under the landing lock; each replacement was built by a three-way merge of the old commit"
	// The fetched tip is published: pin it so the identity check trusts the commits that
	// arrived with it (design Section 4.3). Commits replayed on top are not under it.
	if _, err := rawGit(ctx, root, nil, "update-ref", publishedRef(remote, ref), remoteTip); err != nil {
		return fail("pinning the fetched tip failed: %v", err)
	}
	// Move the trunk, and the checkout with it. `reset --keep` refuses, changing nothing,
	// when a path the move touches has local changes, and keeps changes elsewhere.
	head, _ := rawGit(ctx, root, nil, "symbolic-ref", "-q", "HEAD")
	if strings.TrimSpace(head) != "refs/heads/"+TrunkRef {
		return failKind(pushKindWorktreeDirty, "the main checkout is not on %s: check out %s", TrunkRef, TrunkRef)
	}
	if got, err := rawGit(ctx, root, nil, "rev-parse", "refs/heads/"+TrunkRef); err != nil || strings.TrimSpace(got) != tip {
		return fail("the trunk moved while the replay was built")
	}
	if _, err := rawGit(ctx, root, nil, "reset", "--keep", "-q", cur); err != nil {
		return failKind(pushKindWorktreeDirty, "the main checkout is dirty on a path the move changes (commit, stash or restore it): %v", err)
	}
	release()
	// The replaced commits are mapped by patch, or by the pairs built here when a patch
	// changed, and green is carried, by the loop's absorption.
	var pairs *rewriteAbsorption
	if len(known.Order) > 0 {
		pairs = known
	}
	if _, err := l.observeTrunkWith(ctx, "", pairs); err != nil {
		return res, fmt.Errorf("trunk rebased onto %s, recording the rewrite failed: %w", shortSHA(remoteTip), err)
	}
	_ = l.closeStaleRemoteSyncItems(ctx)
	return res, nil
}
