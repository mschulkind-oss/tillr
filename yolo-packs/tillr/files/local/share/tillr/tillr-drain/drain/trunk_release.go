package drain

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// releasableItemWait returns the next state for a fulfilled named item wait.
// Generated repair holds keep their stronger rules: closing the repair can release
// the hold, and a repair landing newer than the selected staging base does not.
func (l *Loop) releasableItemWait(ctx context.Context, r *store.Reader, id string) (string, error) {
	var state string
	if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
		return "", err
	}
	if state != StateBlocked {
		return "", nil
	}
	waiting, underlying, err := item.ReadWaitingOn(r, id)
	if err != nil || waiting == nil {
		return "", err
	}
	fixer, ok := strings.CutPrefix(waiting.Waiter, "item:")
	if !ok || fixer == "" {
		return "", nil
	}
	trunk := IsTrunkFixer(fixer)
	// A tooling obstacle's wait is released by the same rule: its defect item landed in
	// the base staging will select (defect.go).
	defect := IsDefectItem(fixer) && strings.HasPrefix(waiting.Reason, RefusalToolingDefect+":")
	if !trunk && !defect {
		// A test-fix-open hold only auto-releases when its fix item lands: if the fix
		// item closed without landing, the candidate's gate-failed run is unanswered, so
		// it stays held for the operator retry command rather than faulting SetState.
		blocker, err := item.LandedBlocker(r, waiting)
		if err != nil || blocker == "" {
			return "", err
		}
		if underlying == "not-started" {
			return underlying, nil // Restore a draft without admitting it to dispatch.
		}
		return StateInQueue, nil
	}
	return l.repairedTrunkWaiter(ctx, r, id, fixer, underlying, defect)
}

func (l *Loop) repairedTrunkWaiter(ctx context.Context, r *store.Reader, id, fixer, underlying string, defect bool) (string, error) {
	if underlying != StateInQueue && underlying != "not-started" {
		return "", nil
	}
	var state string
	err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, fixer).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	declared, err := definition.ForReader(r)
	if err != nil {
		return "", err
	}
	if state == declared.Parsed.Closed || state == "closed" {
		// Closing a fixer or defect item is the operator's ruling that it no longer stands
		// in the way (including when its fix was landed by hand): release every item held
		// behind it on the next pass without an operator retry.
		return underlying, nil
	}
	if defect && state != StateLanded {
		if finishedState(declared.Parsed, state) {
			return underlying, nil
		}
		return "", nil
	}
	green, err := provedLaneBase(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
	if err != nil {
		return "", err
	}
	if state != StateLanded {
		// A fixer resolved without landing (resolveProvedFixers, or an already-on-trunk
		// resolution later proved) — or a live fixer kept open after a rerun passed its
		// checks — repaired nothing itself yet: the proof of its checks is the repair
		// evidence, recorded by resolveProvedFixers. Release once the selected base
		// contains the proved commit, so the fresh run starts from a green tree. An open
		// fixer whose checks no proof has passed after its last red observation releases
		// nothing.
		proved, err := fixerProvedGreenAfterSeen(r, fixer)
		if err != nil {
			return "", err
		}
		if proved == "" && finishedState(declared.Parsed, state) {
			proved, err = fixerProvedGreen(r, fixer)
			if err != nil {
				return "", err
			}
		}
		if proved == "" {
			return "", nil
		}
		if green != "" {
			th, err := readTrunkHistory(r)
			if err != nil {
				return "", err
			}
			ok, err := proofPastRed(ctx, l.cfg.Root, th.Lost, proved, green)
			if err != nil || !ok {
				return "", err
			}
		}
		return underlying, nil
	}
	// A waiter is not a dependency edge. Check its landing before clearing it,
	// using the same proof rule as ordinary blockers, so the fresh run cannot
	// start from a green pointer that still excludes the repair.
	waits, err := unprovedBlockers(ctx, r, l.cfg.Root, green, "",
		map[string]string{fixer: StateLanded}, map[string][]string{id: {fixer}})
	if err != nil || waits[id] != "" {
		return "", err
	}
	return underlying, nil
}

func trunkAcquittedWait(wait item.WaitingOn) bool {
	fixer, ok := strings.CutPrefix(wait.Waiter, "item:")
	return ok && IsTrunkFixer(fixer)
}

func (l *Loop) candidateCommitAvailable(ctx context.Context, work *pendingWork) bool {
	if work == nil || work.Candidate == nil || work.Candidate.HeadSHA == "" {
		return false
	}
	c := *work.Candidate
	if work.LaneReleased {
		return l.pinnedCandidate(ctx, c) == nil
	}
	if c.Worktree == "" {
		return false
	}
	head, err := l.revParse(ctx, c.Worktree, "HEAD")
	return err == nil && head == c.HeadSHA
}

// releaseTrunkAcquittals also releases ordinary named item waits before planning.
// Recheck under the write transaction so an owner's newer disposition cannot be
// overwritten by an earlier read. No write is needed when nothing is releasable.
func (l *Loop) releaseTrunkAcquittals(ctx context.Context) error {
	registerRewriteStore(l.cfg.Root, l.cfg.Store)
	if err := l.releaseTrunkObstacleHolds(ctx); err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var ready []string
	var pendingCandidates map[string]*pendingWork
	// The read phase gathers the git answers (the green pointer, ancestry) the write's
	// recheck asks again, so the write runs none (gathered.go).
	gctx, facts := gatheringFor(ctx)
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != StateBlocked {
				continue
			}
			state, err := l.releasableItemWait(gctx, r, row.ItemID)
			if err != nil {
				return err
			}
			if state != "" {
				ready = append(ready, row.ItemID)
			}
		}
		if len(ready) == 0 {
			return nil
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, id := range ready {
			w := works[id]
			if w != nil && !w.Done && w.Candidate != nil && w.Decision.RunRequest != nil {
				if pendingCandidates == nil {
					pendingCandidates = map[string]*pendingWork{}
				}
				pendingCandidates[id] = w
			}
		}
		return nil
	})
	if err != nil || len(ready) == 0 {
		return err
	}
	type missingCandidate struct {
		itemID, runID, headSHA string
	}
	missing := map[string]missingCandidate{}
	for id, w := range pendingCandidates {
		if !l.candidateCommitAvailable(ctx, w) {
			missing[id] = missingCandidate{
				itemID:  id,
				runID:   w.Decision.RunRequest.RunID,
				headSHA: w.Candidate.HeadSHA,
			}
		}
	}
	type landedTestFix struct {
		blockerID, sha string
	}
	var retired []missingCandidate
	var landedFixes []landedTestFix
	err = writeGatheredFrom(ctx, s, facts, func(ctx context.Context, tx *store.Tx) error {
		var curWorks map[string]*pendingWork
		if len(missing) > 0 {
			var werr error
			curWorks, werr = readPending(tx.Reader())
			if werr != nil {
				return werr
			}
		}
		at := l.cfg.Now().UTC()
		for _, id := range ready {
			state, err := l.releasableItemWait(ctx, tx.Reader(), id)
			if err != nil {
				return err
			}
			if state == "" {
				continue
			}
			if wait, _, werr := item.ReadWaitingOn(tx.Reader(), id); werr == nil && wait != nil && testFixHeldWait(*wait) {
				if blockerID, berr := item.LandedBlocker(tx.Reader(), wait); berr == nil && blockerID != "" {
					sha, _ := landedBlockerSHA(tx.Reader(), blockerID)
					landedFixes = append(landedFixes, landedTestFix{blockerID: blockerID, sha: sha})
				}
			}
			if m, ok := missing[id]; ok {
				if w := curWorks[id]; w != nil && !w.Done && w.Decision.RunRequest != nil && w.Decision.RunRequest.RunID == m.runID {
					if err := appendEffect(tx, examinationDoneKind, m.runID, struct{}{}, at); err != nil {
						return err
					}
					retired = append(retired, m)
				}
			}
			if _, err := item.SetState(ctx, tx, id, state, at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if l.cfg.Root != "" {
		for _, tf := range landedFixes {
			repaired := tf.sha
			if repaired == "" {
				repaired, _ = l.revParse(ctx, l.cfg.Root, TrunkRef)
			}
			if repaired != "" {
				if recErr := recordTrunkRepair(ctx, l.cfg.Root, repaired, "test fix "+tf.blockerID+" landed"); recErr != nil {
					l.reportf("loop: %s landed but could not record trunk repair at %s: %v\n", tf.blockerID, shortSHA(repaired), recErr)
				}
			}
		}
	}
	for _, m := range retired {
		l.reportf("loop: %s released from trunk hold, but kept candidate %s is gone; dispatching a new run\n", m.itemID, shortSHA(m.headSHA))
	}
	return nil
}
