package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// landRemoteSync publishes one gated conflict resolution and the linear suffix
// on top of it.
func (l *Loop) landRemoteSync(ctx context.Context, c Candidate, result *LoopResult) (string, bool, error) {
	root := l.cfg.Root
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return "", false, err
	}
	defer release()
	if _, err := l.recoverRemoteSyncReplayLocked(ctx); err != nil {
		return "", false, err
	}
	refuse := func(detail string) (string, bool, error) {
		release()
		c.RefusalDetail = detail
		return "", false, l.refuse(ctx, c, result, RefusalLand)
	}
	if err := l.checkItemTerminal(ctx, c.ItemID, "land"); err != nil {
		return "", false, err
	}
	st, ok, err := l.readRemoteSyncState(ctx, c.ItemID)
	if err != nil {
		return "", false, err
	}
	pin := remoteSyncPinRef(c.ItemID)
	if !ok || st.BaseSHA == "" || optionalCommit(ctx, root, pin) != st.BaseSHA {
		return "", false, Externalf("remote-sync item %s has no pinned stage", c.ItemID)
	}
	if msg, err := l.checkRemoteSyncTree(ctx, c); err != nil {
		return "", false, err
	} else if msg != "" {
		return refuse(msg)
	}
	tree, err := l.revParse(ctx, root, c.HeadSHA+"^{tree}")
	if err != nil {
		return "", false, err
	}
	raw, err := os.ReadFile(filepath.Join(RunDir(root, c.RunID), GateVerdictRecord))
	if err != nil {
		return "", false, fmt.Errorf("reading remote-sync gate verdict: %w", err)
	}
	verdict, err := land.DecodeVerdict(raw)
	if err != nil {
		return "", false, err
	}
	verdictOK := verdict.Result == land.VerdictPassInherited || classifyCheck(checkresult.Observation{
		Commit: c.HeadSHA,
		Source: checkresult.ResultWord,
		Word:   verdict.Result,
	}).Outcome == checkresult.Pass
	if verdict.BaseSHA != st.BaseSHA || verdict.TreeSHA != tree || !verdictOK {
		return refuse("remote-sync needs a passing gate verdict bound to the staged base and candidate tree")
	}
	trunkRef := l.trunkRef()
	before, err := l.revParse(ctx, root, "refs/heads/"+trunkRef)
	if err != nil {
		return "", false, err
	}
	if already, err := trunkAncestor(ctx, root, st.RemoteTip, before); err != nil {
		return "", false, err
	} else if already {
		return refuse("remote-sync remote tip is already on " + trunkRef)
	}
	if present, err := trunkAncestor(ctx, root, st.ConflictCommit, before); err != nil {
		return "", false, err
	} else if !present {
		return refuse("remote-sync conflicting commit is no longer on " + trunkRef)
	}
	trunk, err := land.OpenTrunk(root)
	if err != nil {
		return "", false, err
	}
	trunk.SetContext(ctx)
	at := l.cfg.Now()
	prefix, err := l.stampRemoteSyncPrefix(ctx, trunk, st, at)
	if err != nil {
		return "", false, fmt.Errorf("stamping staged remote-sync prefix: %w", err)
	}
	// The prefix may have new commit IDs, but its base tree is unchanged.
	// Keep the original stage for the verdict, receipt, and pin operations.
	repair, err := trunk.StampRepair(prefix.BaseSHA, c.HeadSHA, tree, at)
	if err != nil {
		return "", false, fmt.Errorf("stamping remote-sync repair: %w", err)
	}
	known, next, err := replayRemoteSyncSuffix(ctx, trunk, root, prefix, repair, before)
	if err != nil {
		return "", false, err
	}
	if next != nil {
		_, next.CommitMessage, next.LandingSummary = l.collectRemoteSyncContext(ctx, "", "", next.ConflictCommit)
		conflict, err := l.fileOrUpdateRemoteSyncItemLocked(ctx, *next, st.BaseSHA)
		if err != nil {
			return "", false, err
		}
		release()
		l.recordPush(ctx, l.cfg.PostLanding.Push.What, before, conflict)
		return refuse(fmt.Sprintf("later commit %s conflicted on %s; %s advanced to base %s",
			shortSHA(next.ConflictCommit), strings.Join(next.ConflictPaths, ", "), st.ItemID, shortSHA(next.BaseSHA)))
	}
	dirty := func(detail string) (string, bool, error) {
		release()
		l.recordPush(ctx, l.cfg.PostLanding.Push.What, before, &pushStateError{
			kind: pushKindWorktreeDirty, remoteTip: st.RemoteTip, resetTarget: known.Tip, escalated: "operator",
			detail: detail + "\nmover: operator; commit, stash, or restore clashing paths in the " + trunkRef + " checkout",
		})
		return "", false, l.holdDirty(ctx, c, nil)
	}
	// Establish the exact old tip before moving it so ordinary observation
	// cannot miss the replay, or absorb it without the repair's green override.
	if h, err := l.observeTrunkLocked(ctx, before, nil); err != nil {
		return "", false, err
	} else if h.Lost != "" || h.Observed != before {
		return "", false, Externalf("cannot replay from %s while %s has unresolved history", shortSHA(before), trunkRef)
	}
	known.GreenOverride, err = remoteSyncGreen(ctx, root, st, known.Pairs)
	if err != nil {
		return "", false, err
	}
	if behind, err := trunkAncestor(ctx, root, known.GreenOverride, prefix.BaseSHA); err != nil {
		return "", false, err
	} else if !behind {
		return "", false, Externalf("remote-sync replay cannot hold green behind the repair")
	}
	if err := l.checkItemTerminal(ctx, c.ItemID, "land"); err != nil {
		return "", false, err
	}
	if got, err := l.revParse(ctx, root, "refs/heads/"+trunkRef); err != nil || got != before {
		return "", false, Externalf("%s moved while preparing the remote-sync replay", trunkRef)
	}
	intent := &remoteSyncReplay{Candidate: c, Rewrite: *known, Tree: tree, Verdict: verdict, Stage: st}
	if err := l.writeRemoteSyncReplay(ctx, intent); err != nil {
		return "", false, err
	}
	if onTrunk(ctx, root, trunkRef) {
		if _, err := rawGit(ctx, root, nil, "reset", "--keep", "-q", known.Tip); err != nil {
			return dirty(fmt.Sprintf("remote-sync-worktree-dirty: the main checkout refused the replay reset: %v", err))
		}
	} else {
		wt, err := trunk.WorktreeFor("refs/heads/" + trunkRef)
		if err != nil {
			return "", false, err
		}
		if wt != "" {
			return dirty(fmt.Sprintf("remote-sync-worktree-dirty: %s is checked out at %s", trunkRef, wt))
		}
		if _, err := rawGit(ctx, root, nil, "update-ref", "refs/heads/"+trunkRef, known.Tip, before); err != nil {
			return "", false, Externalf("advancing %s: %v", trunkRef, err)
		}
	}
	if err := l.finishRemoteSyncReplayLocked(ctx, intent); err != nil {
		return "", false, err
	}
	release()
	l.afterRemoteSyncReplay(ctx, intent, result)
	return known.Tip, true, nil
}

func (l *Loop) finishRemoteSyncReplayLocked(ctx context.Context, intent *remoteSyncReplay) error {
	root := l.cfg.Root
	c, known, tree, verdict := intent.Candidate, &intent.Rewrite, intent.Tree, intent.Verdict
	registerRewriteStore(root, l.cfg.Store)
	if h, err := l.observeTrunkLocked(ctx, "", known); err != nil {
		return fmt.Errorf("recording remote-sync rewrite: %w", err)
	} else if h.Lost != "" {
		return Externalf("%s changed before the remote-sync replay was recorded", l.trunkRef())
	}
	// This mapping is also durable across recovery and names the actual repair,
	// not the worker head or the tip after replaying later commits.
	repair := known.Pairs[intent.Stage.ConflictCommit]
	if err := l.recordRemoteSyncLanding(ctx, c, known.Lost, known.Tip, repair, tree, verdict); err != nil {
		return fmt.Errorf("recording remote-sync landing: %w", err)
	}
	if err := l.completeRemoteSyncReplayItem(ctx, c, repair); err != nil {
		return err
	}
	if _, err := rawGit(ctx, root, nil, "update-ref", "-d", remoteSyncPinRef(c.ItemID), c.BaseSHA); err != nil {
		return err
	}
	return l.writeRemoteSyncReplay(ctx, nil)
}

func (l *Loop) completeRemoteSyncReplayItem(ctx context.Context, c Candidate, repair string) error {
	if err := l.checkItemTerminal(ctx, c.ItemID, "settle-remote-sync"); err != nil {
		if terminal := asItemTerminal(err); terminal != nil && terminal.State == StateLanded {
			return l.endItemUnfinishedRuns(ctx, c.ItemID, c.RunID)
		}
		return err
	}
	if c.Judge != nil {
		receipt := *c.Judge
		receipt.Options.Landed = repair
		c.Judge = &receipt
		if err := appendJudgment(ctx, l.cfg.Store, judgeLandedKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
			return err
		}
	} else if l.cfg.Steps == nil {
		return l.completeRemoteSyncWithoutSteps(ctx, c)
	}
	return l.completeItem(ctx, c)
}

func (l *Loop) afterRemoteSyncReplay(ctx context.Context, replay *remoteSyncReplay, result *LoopResult) {
	if replay != nil && replay.Rewrite.Tip != "" {
		l.checkActiveGatesOnTrunkMove(ctx, replay.Rewrite.Tip)
	}
	l.publishRemoteSyncReplay(ctx, replay.Candidate.ItemID, replay.publication())
	l.mu.Lock()
	result.Landed++
	l.mu.Unlock()
	if err := l.bumpLandings(ctx); err != nil {
		l.reportf("loop: landed %s but could not record the landing count: %v\n", replay.Candidate.ItemID, err)
	}
}

// stampRemoteSyncPrefix normalizes only the unpublished replay above RemoteTip.
// Older stages saved raw worker heads and used ambient committers on clean
// steps. Earlier stage events identify repairs without judging author metadata
// on the original commits or rerunning an already-gated merge.
func (l *Loop) stampRemoteSyncPrefix(ctx context.Context, trunk *land.Trunk, st remoteSyncState, at time.Time) (remoteSyncState, error) {
	repairs := map[string]bool{}
	if st.FirstConflictParent != "" {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return st, err
		}
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT json_extract(payload,'$.conflict_commit') FROM events WHERE kind=? AND object_id=?`, remoteSyncStagedKind, st.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var old string
				if err := rows.Scan(&old); err != nil {
					return err
				}
				repairs[old] = true
			}
			return rows.Err()
		}); err != nil {
			return st, err
		}
	}
	identity, err := trunk.LandingIdentity()
	if err != nil {
		return st, err
	}
	root := l.cfg.Root
	oldParent, parent := st.RemoteTip, st.RemoteTip
	pairs := make(map[string]string, len(st.ReplayedPairs))
	for _, old := range st.ReplayedOrder {
		saved := st.ReplayedPairs[old]
		if saved == oldParent {
			// An empty clean replay points at the preceding commit.
			pairs[old] = parent
			continue
		}
		meta, err := rawGit(ctx, root, nil, "show", "-s", "--format=%P%x00%T%x00%an%x00%ae%x00%cn%x00%ce", saved)
		if err != nil {
			return st, err
		}
		fields := strings.Split(strings.TrimSuffix(meta, "\n"), "\x00")
		if len(fields) != 6 || fields[0] != oldParent {
			return st, Externalf("staged remote-sync commit %s does not follow %s", shortSHA(saved), shortSHA(oldParent))
		}
		isRepair := repairs[old]
		committerMatches := fields[4] == identity.Name && fields[5] == identity.Email
		authorMatches := fields[2] == identity.Name && fields[3] == identity.Email
		if parent == oldParent && committerMatches && (!isRepair || authorMatches) {
			parent = saved
		} else if isRepair {
			parent, err = trunk.StampRepair(parent, saved, fields[1], at)
		} else {
			parent, err = commitReplay(ctx, trunk, root, fields[1], parent, saved)
		}
		if err != nil {
			return st, err
		}
		oldParent, pairs[old] = saved, parent
	}
	if oldParent != st.BaseSHA || len(pairs) != len(st.ReplayedPairs) {
		return st, Externalf("staged remote-sync mappings do not describe the prefix ending at %s", shortSHA(st.BaseSHA))
	}
	st.BaseSHA, st.ReplayedPairs = parent, pairs
	return st, nil
}

func replayRemoteSyncSuffix(ctx context.Context, trunk *land.Trunk, root string, st remoteSyncState, repair, before string) (*rewriteAbsorption, *remoteSyncState, error) {
	known := &rewriteAbsorption{
		Lost: before, Tip: repair, Pairs: make(map[string]string),
		Order:    append(append([]string{}, st.ReplayedOrder...), st.ConflictCommit),
		Evidence: "linear replay onto " + shortSHA(st.RemoteTip) + " with conflict repair " + st.ItemID + " under the landing lock",
	}
	for old, replacement := range st.ReplayedPairs {
		known.Pairs[old] = replacement
	}
	known.Pairs[st.ConflictCommit] = repair
	listed, err := rawGit(ctx, root, nil, "rev-list", "--reverse", "--topo-order", "--parents", st.ConflictCommit+".."+before)
	if err != nil {
		return nil, nil, err
	}
	previous := st.ConflictCommit
	for _, line := range strings.Split(strings.TrimSpace(listed), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || f[1] != previous {
			return nil, nil, Externalf("commit %s is not part of the linear suffix after %s", shortSHA(f[0]), shortSHA(st.ConflictCommit))
		}
		old, parent := f[0], f[1]
		previous = old
		mt := exec.CommandContext(ctx, "git", "-C", root, "merge-tree", "--write-tree", "--name-only", "-z", "--merge-base="+parent, known.Tip, old)
		mt.Env = boundaryGitEnv()
		var stderr strings.Builder
		mt.Stderr = &stderr
		out, err := mt.Output()
		if err != nil {
			paths, output := parseMergeTreeConflict(string(out))
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || len(paths) == 0 {
				return nil, nil, Externalf("replaying %s onto %s failed: %v\n%s", shortSHA(old), shortSHA(known.Tip), err, strings.TrimSpace(stderr.String()))
			}
			next := st
			next.FirstConflictParent = remoteSyncFirstConflictParent(st)
			next.BaseSHA, next.OldTrunk = known.Tip, before
			next.ConflictCommit, next.ConflictParent = old, parent
			next.ConflictPaths, next.ConflictOutput = paths, output
			next.ReplayedOrder, next.ReplayedPairs = known.Order, known.Pairs
			return nil, &next, nil
		}
		tree, _, _ := strings.Cut(string(out), "\x00")
		currentTree, err := rawGit(ctx, root, nil, "rev-parse", known.Tip+"^{tree}")
		if err != nil {
			return nil, nil, err
		}
		if tree != strings.TrimSpace(currentTree) {
			known.Tip, err = commitReplay(ctx, trunk, root, tree, known.Tip, old)
			if err != nil {
				return nil, nil, err
			}
		}
		known.Order = append(known.Order, old)
		known.Pairs[old] = known.Tip
	}
	return known, nil, nil
}

// commitReplay keeps the original authorship and message, but Tillr is the
// committer of the replacement. Use the same protected config as a landing.
func commitReplay(ctx context.Context, trunk *land.Trunk, root, tree, parent, old string) (string, error) {
	identity, err := trunk.LandingIdentity()
	if err != nil {
		return "", err
	}
	meta, err := rawGit(ctx, root, nil, "log", "-1", "--date=raw", "--format=%an%x00%ae%x00%ad", old)
	if err != nil {
		return "", err
	}
	author := strings.Split(strings.TrimSuffix(meta, "\n"), "\x00")
	if len(author) != 3 {
		return "", Externalf("commit %s has unreadable author data", shortSHA(old))
	}
	msg, err := rawGit(ctx, root, nil, "log", "-1", "--format=%B", old)
	if err != nil {
		return "", err
	}
	ct := exec.CommandContext(ctx, "git", "-C", root, "commit-tree", tree, "-p", parent)
	ct.Env = append(boundaryGitEnv(),
		"GIT_AUTHOR_NAME="+author[0], "GIT_AUTHOR_EMAIL="+author[1], "GIT_AUTHOR_DATE="+author[2],
		"GIT_COMMITTER_NAME="+identity.Name, "GIT_COMMITTER_EMAIL="+identity.Email)
	ct.Stdin = strings.NewReader(msg)
	var stderr strings.Builder
	ct.Stderr = &stderr
	out, err := ct.Output()
	if err != nil {
		return "", Externalf("recreating %s failed: %v: %s", shortSHA(old), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func remoteSyncFirstConflictParent(st remoteSyncState) string {
	if st.FirstConflictParent != "" {
		return st.FirstConflictParent
	}
	return st.ConflictParent
}

func remoteSyncGreen(ctx context.Context, root string, st remoteSyncState, pairs map[string]string) (string, error) {
	green := optionalCommit(ctx, root, GreenRef)
	if green == "" {
		// Still identify an authoritative repair replay, without creating green.
		return st.MergeBase, nil
	}
	if preceded, err := trunkAncestor(ctx, root, green, remoteSyncFirstConflictParent(st)); err != nil {
		return "", err
	} else if preceded {
		if mapped := pairs[green]; mapped != "" {
			return mapped, nil
		}
		return green, nil
	}
	base, err := rawGit(ctx, root, nil, "merge-base", green, st.MergeBase)
	return strings.TrimSpace(base), err
}

func (l *Loop) publishRemoteSyncReplay(ctx context.Context, itemID string, res healResult) {
	p := l.cfg.PostLanding.Push
	switch p.What {
	case settings.PushTip:
		l.rollAfterRemoteSync(ctx, res)
		l.submitTipPush(ctx, p, res.newTip)
	case settings.PushGreen:
		l.maybeRollAfterRemoteSync(ctx, res)
		l.recordPush(ctx, settings.PushGreen, res.newTip, &pushStateError{
			kind: pushKindProving, remoteTip: res.remoteTip, rebasedTip: res.newTip,
			detail: fmt.Sprintf("rebased %d local commit(s) onto remote %s via %s; %s", res.replayed, shortSHA(res.remoteTip), itemID, awaitingRebasedProof(res.newTip)),
		})
	}
}

func (l *Loop) recordRemoteSyncLanding(ctx context.Context, c Candidate, before, after, repair, tree string, verdict land.Verdict) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := land.RecordReplayLanding(ctx, tx, c.ItemID, c.RunID, before, after, c.HeadSHA, repair, c.BaseSHA, tree, verdict, l.cfg.Now())
		return err
	})
}
