package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/lanestate"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const candidateLaneKind = "drain.candidate-lane"
const candidateReleaseStartedKind = "drain.candidate-release-started"
const candidateAssignedKind = "drain.candidate-assigned"
const candidateStagedKind = "drain.candidate-staged"

type candidateLane struct {
	Candidate Candidate `json:"candidate"`
	Released  bool      `json:"released"`
	Reason    string    `json:"reason"`
	Fence     string    `json:"fence"`
	ResetSHA  string    `json:"reset_sha,omitempty"`
}

func init() {
	for _, kind := range []string{candidateReleaseStartedKind, candidateLaneKind, candidateAssignedKind, candidateStagedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

// retainLane enumerates examination outcomes that hold a pending candidate. Only
// the repair handoff owns worktree-dependent progress; the others wait on inputs.
func retainLane(err error) (held, keep bool) {
	switch {
	case errors.Is(err, errRepairPending), errors.Is(err, errJudgeGrantHeld):
		return true, true
	case errors.Is(err, errGateStallHeld), errors.Is(err, errItemFaultHeld), errors.Is(err, errRepairHeld), errors.Is(err, errDirtyHeld), errors.Is(err, errRedCapHeld), errors.Is(err, errTrunkRewritten):
		return true, false
	default:
		return false, false
	}
}

func candidateKeepReason(work *pendingWork, reason string) string {
	baseRef := ""
	if work.Candidate != nil {
		baseRef = work.Candidate.BaseRef
	}
	return lanestate.KeepReason(work.Candidate != nil, baseRef, work.Decision.Workflow != nil, work.BoundaryFailed, reason)
}

// pinnedCandidate reads the commit back, not merely the ref name. A successful
// update-ref is insufficient evidence to reset the only checkout holding work.
func (l *Loop) pinnedCandidate(ctx context.Context, c Candidate) error {
	if c.RunID == "" || c.HeadSHA == "" {
		return fmt.Errorf("candidate has no run or head")
	}
	head, err := l.revParse(ctx, l.cfg.Root, "refs/tillr/runs/"+c.RunID+"^{commit}")
	if err != nil {
		return err
	}
	if head != c.HeadSHA {
		return fmt.Errorf("pin names %s, want %s", head, c.HeadSHA)
	}
	return nil
}

// claimCandidateRelease revalidates an idle snapshot under the store writer.
// The ordinary examination path may instead borrow its own existing claim.
// Both use the existing no-expiry examiner lease and dead-owner recovery.
func (l *Loop) claimCandidateRelease(ctx context.Context, work *pendingWork, reason string, examining bool) (*LoopOwner, bool, error) {
	owner, err := currentLoopOwner()
	if err != nil {
		return nil, false, err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, false, err
	}
	defer s.Close()
	claimed := false
	c := work.Candidate
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		current := works[c.ItemID]
		if current == nil || current.Done || current.LaneReleased || current.Candidate == nil || current.Candidate.RunID != c.RunID || current.Candidate.HeadSHA != c.HeadSHA || current.Decision.LaneID != c.LaneID || candidateKeepReason(current, reason) != "" {
			return nil
		}
		var state string
		var runID *string
		var until *int64
		if err := tx.QueryRow(`SELECT state,run_id,leased_until FROM lanes WHERE lane_id=?`, c.LaneID).Scan(&state, &runID, &until); err != nil {
			return err
		}
		if examining {
			if state != run.LaneStateLeased || runID == nil || *runID != c.RunID || until != nil || current.Examiner == nil || *current.Examiner != *owner {
				return nil
			}
		} else {
			if state != run.LaneStateIdle || runID != nil {
				return nil
			}
			if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=?,leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, c.RunID, c.LaneID); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationOwnerKind, c.RunID, owner, l.cfg.Now()); err != nil {
				return err
			}
		}
		// Preserve the examination's freshly prepared candidate, while taking
		// interrupted-release state from the current durable reservation.
		work.ReleasePending, work.HeldFence = current.ReleasePending, current.HeldFence
		claimed = true
		return nil
	})
	return owner, claimed && err == nil, err
}

func candidateReleaseOwned(r *store.Reader, c Candidate, owner *LoopOwner) (bool, error) {
	works, err := readPending(r)
	if err != nil {
		return false, err
	}
	w := works[c.ItemID]
	if w == nil || w.Done || w.Decision.LaneID != c.LaneID || w.Examiner == nil || *w.Examiner != *owner {
		return false, nil
	}
	var n int
	err = r.QueryRow(`SELECT COUNT(*) FROM lanes WHERE lane_id=? AND state=? AND run_id=? AND leased_until IS NULL`, c.LaneID, run.LaneStateLeased, c.RunID).Scan(&n)
	return n == 1, err
}

func clearCandidateRelease(tx *store.Tx, c Candidate, at time.Time) error {
	if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=? AND leased_until IS NULL`, run.LaneStateIdle, c.LaneID, c.RunID); err != nil {
		return err
	}
	return appendEffect(tx, examinationOwnerKind, c.RunID, (*LoopOwner)(nil), at)
}

// releaseHeldCandidate claims idle holds; it never borrows another examiner's
// same-run lease, even when that examiner lives in this process.
func (l *Loop) releaseHeldCandidate(ctx context.Context, work *pendingWork, reason string) (bool, error) {
	return l.releaseCandidate(ctx, work, reason, false)
}

func (l *Loop) releaseCandidate(ctx context.Context, work *pendingWork, reason string, examining bool) (released bool, err error) {
	if work.LaneReleased || work.Done || candidateKeepReason(work, reason) != "" {
		return false, nil
	}
	owner, claimed, err := l.claimCandidateRelease(ctx, work, reason, examining)
	if err != nil || !claimed {
		return false, err
	}
	c := *work.Candidate
	// An early operational failure must release only our new claim. An
	// inherited examination still owns its cleanup in launchExamination.
	defer func() {
		if !claimed || examining {
			return
		}
		s, openErr := store.Open(context.WithoutCancel(ctx), l.cfg.Store, true)
		if openErr != nil {
			err = errors.Join(err, openErr)
			return
		}
		defer s.Close()
		err = errors.Join(err, s.WriteTx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
			owned, err := candidateReleaseOwned(tx.Reader(), c, owner)
			if err != nil || !owned {
				return err
			}
			return clearCandidateRelease(tx, c, l.cfg.Now())
		}))
	}()
	kept := ""
	// Failed repair staging may have reset to its captured trunk, but never
	// authorizes discarding arbitrary later commits in the retained checkout.
	repairReset := ""
	if work.RepairHold != nil {
		repairReset = work.RepairHold.TrunkSHA
	}
	if reason == ReasonRepairStagingFailed {
		// The immediate examination still has its pre-repair snapshot; read the
		// durable basis the failed repair just recorded, rather than guessing main.
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return false, err
		}
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			var raw []byte
			err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, repairHeldKind, c.RunID).Scan(&raw)
			if err == sql.ErrNoRows {
				return nil
			}
			if err != nil {
				return err
			}
			var hold repairHold
			if err := json.Unmarshal(raw, &hold); err != nil {
				return err
			}
			repairReset = hold.TrunkSHA
			return nil
		})
		s.Close()
		if err != nil {
			return false, err
		}
	}
	if head, err := l.revParse(ctx, c.Worktree, "HEAD"); err != nil || (head != c.HeadSHA && head != repairReset && head != work.ReleasePending) {
		kept = "unrecorded lane HEAD needs inspection"
	}
	if kept == "" {
		if err := l.pin(ctx, c.RunID, c.HeadSHA); err != nil {
			kept = "candidate pin failed: " + err.Error()
		}
	}
	if kept == "" {
		if err := l.pinnedCandidate(ctx, c); err != nil {
			kept = "candidate pin cannot be read back: " + err.Error()
		}
	}
	fence := work.HeldFence
	if kept == "" && fence == "" {
		path, err := l.itemFencePath(c.LaneID)
		var raw []byte
		if err == nil {
			raw, err = os.ReadFile(path)
		}
		if err != nil {
			kept = "candidate fence cannot be saved: " + err.Error()
		} else {
			fence = string(raw)
		}
	}
	if kept == "" {
		if err := l.preserveWork(ctx, c.LaneID, c.RunID, "held candidate releases its lane"); err != nil {
			kept = "uncommitted work cannot be preserved: " + err.Error()
		}
	}
	resetSHA := work.ReleasePending
	if kept == "" && resetSHA == "" {
		var err error
		resetSHA, err = l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return false, err
		}
		// Git cannot commit together with the store. Persist the saved candidate,
		// fence and exact reset destination before changing the checkout.
		intent := candidateLane{Candidate: c, Fence: fence, ResetSHA: resetSHA, Reason: "candidate lane reset awaits completion"}
		if err := appendJudgment(ctx, l.cfg.Store, candidateReleaseStartedKind, "run", c.RunID, intent, l.cfg.Now()); err != nil {
			return false, err
		}
	}
	released = kept == ""
	if !released && kept == work.LaneKeepReason && fence == work.HeldFence {
		return false, nil
	}
	if !released {
		l.reportf("loop: lane %s kept for %s: %s\n", c.LaneID, c.ItemID, kept)
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		owned, err := candidateReleaseOwned(tx.Reader(), c, owner)
		if err != nil {
			return err
		}
		if !owned {
			return Externalf("candidate release lost its lane claim for %s", c.RunID)
		}
		// Keep the writer through the destructive Git operation and receipt:
		// an operator cannot claim this checkout between validation and reset.
		if released {
			if err := l.resetLaneTo(ctx, c.LaneID, resetSHA); err != nil {
				released, kept = false, "lane reset failed: "+err.Error()
			}
		}
		if err := appendEffect(tx, candidateLaneKind, c.RunID, candidateLane{Candidate: c, Released: released, Reason: kept, Fence: fence}, l.cfg.Now().UTC()); err != nil {
			return err
		}
		if released || !examining {
			if err := clearCandidateRelease(tx, c, l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && (released || !examining) {
		claimed = false
	}
	return released && err == nil, err
}

// Older loops may have left holds without a release event. Reconcile those before
// planning, so a newly free lane is offered to a ready sibling in this same pass.
func (l *Loop) releaseIdleHeldCandidates(ctx context.Context) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var works map[string]*pendingWork
	var faults map[string]ItemFault
	busy := map[string]bool{}
	trunkLost := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		works, err = readPending(r)
		if err != nil {
			return err
		}
		faults, err = ReadItemFaults(r)
		if err != nil {
			return err
		}
		busy, err = readLanes(r, l.cfg.Now())
		if err != nil {
			return err
		}
		history, err := readTrunkHistory(r)
		trunkLost = history.Lost != ""
		return err
	})
	s.Close()
	if err != nil {
		return err
	}
	for id, w := range works {
		if w.Done || w.LaneReleased || w.NeedsStage || w.Candidate == nil || w.Decision.RunRequest == nil || l.active[w.Decision.RunRequest.RunID] || busy[w.Decision.LaneID] {
			continue
		}
		reason := faults[id].Reason
		if reason == "" && w.GateStall == nil && w.DirtyHold == nil && w.RepairHold == nil && w.RedCapHold == nil && w.ReleasePending == "" && !(w.Gated && trunkLost) {
			continue
		}
		if w.RepairHold != nil {
			reason = ReasonRepairStagingFailed
		}
		// A passing verdict alone is not a hold. Do not reset a candidate that
		// still needs its recorded HEAD checked by the ordinary landing path.
		if _, err := l.releaseHeldCandidate(ctx, w, reason); err != nil {
			return err
		}
	}
	return nil
}

// finishReleaseIntent finishes only this recovery target's interrupted reset.
// It never reaps a lease or changes another item's assignment.
func (l *Loop) finishReleaseIntent(ctx context.Context, runID string) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var work *pendingWork
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		_, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		w := byRun[runID]
		if w == nil || w.Done || w.ReleasePending == "" {
			return nil
		}
		work, err = recoveryWork(r, runID)
		return err
	})
	s.Close()
	if err != nil || work == nil {
		return err
	}
	released, err := l.releaseHeldCandidate(ctx, work, "")
	if err == nil && !released {
		return Externalf("run %s's lane release could not finish; inspect the kept-lane reason", runID)
	}
	return err
}

// assignPinnedCandidate records the ordinary and operator examination's claim
// with the same restartable staging protocol. The caller holds the store writer
// and has verified that the selected lane is free.
func (l *Loop) assignPinnedCandidate(tx *store.Tx, work *pendingWork, d *Decision, at time.Time) error {
	if !work.LaneReleased {
		return nil
	}
	c := *work.Candidate
	c.LaneID, c.Worktree = d.LaneID, l.lanePath(d.LaneID)
	req := *d.RunRequest
	req.LaneID = d.LaneID
	d.RunRequest = &req
	work.Decision, work.Candidate, work.LaneReleased, work.NeedsStage = *d, &c, false, true
	c.Request, c.Seat = d.RunRequest, d.Seat
	return appendEffect(tx, candidateAssignedKind, c.RunID, work, at)
}

// stagePinnedCandidate materializes the exact held commit, without a new worker
// or another attempt. The saved fence covers the whole item, not only its last node.
func (l *Loop) stagePinnedCandidate(ctx context.Context, work *pendingWork) error {
	c := *work.Candidate
	if err := l.pinnedCandidate(ctx, c); err != nil {
		return err
	}
	// Reuse ordinary worktree admission without rendering another worker brief
	// or overwriting the archived run inputs and session. Admission compares a
	// free lane with today's trunk, not this candidate's older base.
	trunk, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return err
	}
	spec := StageSpec{LaneID: c.LaneID, ItemID: c.ItemID, RunID: c.RunID, BaseSHA: trunk}
	stager := &LaneStager{Root: l.cfg.Root, LaneRoot: l.cfg.LaneRoot}
	// A crash can leave the checkout materialized before the staged event.
	head, _ := l.revParse(ctx, c.Worktree, "HEAD")
	if _, statErr := os.Stat(c.Worktree); statErr == nil {
		paths, err := l.uncommittedPaths(ctx, c.Worktree)
		if err != nil {
			return err
		}
		if len(paths) > 0 {
			return Externalf("lane %s has uncommitted work; pinned candidate was not staged", c.LaneID)
		}
	}
	if head != c.HeadSHA {
		if err := stager.ensureWorktree(ctx, spec, l.laneDirPath(c.LaneID), c.Worktree); err != nil {
			return err
		}
		if _, err := l.git(ctx, c.Worktree, "checkout", "--force", "-B", LaneBranch(l.cfg.LaneRoot, c.LaneID), "refs/tillr/runs/"+c.RunID); err != nil {
			return err
		}
	}
	for _, name := range []string{TargetFile, PatchRefFile} {
		if err := os.Remove(filepath.Join(l.laneDirPath(c.LaneID), name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// A released lane may have belonged to another item since this candidate
	// was pinned. Replace both legacy and whole-item metadata with the saved
	// authority; no sibling base or fence may survive the restaging.
	for name, body := range map[string]string{
		BaseSHAFile: c.BaseSHA + "\n", ItemBaseSHAFile: c.BaseSHA + "\n",
		FenceFile: work.HeldFence, ItemFenceFile: work.HeldFence,
	} {
		if err := os.WriteFile(filepath.Join(l.laneDirPath(c.LaneID), name), []byte(body), 0600); err != nil {
			return err
		}
	}
	if head, err := l.revParse(ctx, c.Worktree, "HEAD"); err != nil || strings.TrimSpace(head) != c.HeadSHA {
		return fmt.Errorf("staging candidate %s: head %s, want %s: %v", c.RunID, head, c.HeadSHA, err)
	}
	if err := appendJudgment(ctx, l.cfg.Store, candidateStagedKind, "run", c.RunID, struct{}{}, l.cfg.Now()); err != nil {
		return err
	}
	work.NeedsStage = false
	return nil
}
