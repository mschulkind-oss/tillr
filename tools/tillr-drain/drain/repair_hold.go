package drain

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const repairHeldKind = "drain.repair-held"

// ReasonRepairStagingFailed means a retained candidate's conflict repair cannot be
// staged. Only changed definition, policy, template, or executable inputs reopen it.
const ReasonRepairStagingFailed = "conflict-repair-staging-failed"

var errRepairHeld = errors.New("conflict repair held")
var errRepairPending = errors.New("conflict repair owns the pending transition")

type repairHold struct {
	ItemID     string `json:"item_id"`
	Cause      string `json:"cause"`
	NextAction string `json:"next_action"`
	// TrunkSHA retains the third input to the original conflicted merge even
	// when later landings move the trunk before an operator inspects the cause.
	TrunkSHA     string      `json:"trunk_sha"`
	Candidate    Candidate   `json:"candidate"`
	Conflict     LandOutcome `json:"conflict"`
	Inputs       RunInputs   `json:"inputs"`
	Template     string      `json:"template,omitempty"`
	TemplateHash string      `json:"template_hash,omitempty"`
}

func init() {
	if err := event.Register(repairHeldKind, Utility); err != nil {
		panic(err)
	}
}

// Missing/unreadable template inputs are themselves a stable value, so correcting
// them reopens the repair. Trunk movement and item timestamps are not an allowance.
func repairTemplateHash(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return inputHash(err.Error())
	}
	return inputHash(string(body))
}

func (h *repairHold) unchanged(inputs *RunInputs, template *string) (bool, error) {
	path := h.Template
	if template != nil {
		path = *template
	}
	hash, err := repairTemplateHash(path)
	return inputs == nil || (*inputs == h.Inputs && hash == h.TemplateHash), err
}

func (l *Loop) repairBasis(ctx context.Context, c Candidate, landed LandOutcome, policy *Policy) (*repairHold, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	hold := &repairHold{ItemID: c.ItemID, Candidate: c, Conflict: landed, NextAction: "correct the reported staging cause in the item, backend policy, brief template, or tillr build; the next pass retries repair of the retained candidate"}
	if stager, ok := l.cfg.Stage.(*LaneStager); ok {
		hold.Template = stager.Template
	}
	hold.TemplateHash, err = repairTemplateHash(hold.Template)
	if err != nil {
		return nil, err
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ItemID == c.ItemID {
				inputs, err := currentRunInputs(ctx, r, row, policy, l.cfg.Root, l.build)
				if err != nil {
					return err
				}
				hold.Inputs = *inputs
				return nil
			}
		}
		return errors.New("repair item disappeared")
	})
	return hold, err
}

func (l *Loop) holdRepair(ctx context.Context, hold *repairHold, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	hold.Cause = cause.Error()
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	// Append, rather than overwrite: a changed input may fail differently, and both
	// failures must remain evidence. Unchanged inputs never get here from the planner.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := requireHold(tx.Reader(), ReasonRepairStagingFailed); err != nil {
			return err
		}
		if err := refuseTerminalItem(tx.Reader(), hold.ItemID, "repair"); err != nil {
			return err
		}
		return appendEffect(tx, repairHeldKind, hold.Candidate.RunID, hold, l.cfg.Now().UTC())
	})
	if err != nil {
		return err
	}
	l.reportf("loop: holding %s (%s): %s; %s\n", hold.ItemID, ReasonRepairStagingFailed, hold.Cause, hold.NextAction)
	return errRepairHeld
}

// Move ownership before the repair process starts. A replacement loop must settle
// this request's terminal record/tree, not reset the original candidate and start
// the same repair ID cold. Both effects commit in one store transaction.
func (l *Loop) handoffRepair(ctx context.Context, original Candidate, decision Decision, base string) error {
	return l.handoffRepairWithStage(ctx, original, decision, base, "", nil)
}

func (l *Loop) handoffRepairWithStage(ctx context.Context, original Candidate, decision Decision, base, fence string, stage *heldLaneStage) error {
	owner, err := currentLoopOwner()
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// No repair run is started for an item that went terminal while it was staged.
		if err := refuseTerminalItem(tx.Reader(), original.ItemID, "repair"); err != nil {
			return err
		}
		if fence != "" || stage != nil {
			if err := appendEffect(tx, candidateLaneKind, original.RunID, candidateLane{Candidate: original, Released: true, Fence: fence, Stage: stage}, at); err != nil {
				return err
			}
		}
		if err := appendEffect(tx, examinationDoneKind, original.RunID, struct{}{}, at); err != nil {
			return err
		}
		if _, err := run.ClaimLease(ctx, tx, *decision.RunRequest, decision.LaneID, at); err != nil {
			return err
		}
		l.mu.Lock()
		l.stageLaunch.MarkLaunched(decision.RunRequest.RunID)
		l.mu.Unlock()
		work := pendingWork{Decision: decision, Owner: owner, BaseSHA: base, ParentRunID: original.RunID, LoopID: l.sessionID, Staged: true}
		if err := appendEffect(tx, dispatchStartedKind, decision.RunRequest.RunID, work, at); err != nil {
			return err
		}
		return recordPassDecision(tx, decision, at)
	})
}

// restoreConflictCandidate restores the pinned built candidate of an ended
// conflict-repair run so the next pass resumes conflict repair on that candidate
// instead of starting a new attempt at the workflow's first node.
func (l *Loop) restoreConflictCandidate(ctx context.Context, fixWork *pendingWork) (bool, error) {
	if fixWork == nil || fixWork.Decision.Reason != ReasonConflictFix || fixWork.ParentRunID == "" || fixWork.Decision.RunRequest == nil {
		return false, nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var parentCandidate *Candidate
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		_, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		if parent := byRun[fixWork.ParentRunID]; parent != nil && parent.Candidate != nil {
			cp := *parent.Candidate
			parentCandidate = &cp
		}
		return nil
	}); err != nil {
		return false, err
	}
	if parentCandidate == nil {
		return false, nil
	}
	if err := l.pinnedCandidate(ctx, *parentCandidate); err != nil {
		return false, nil
	}
	at := l.cfg.Now().UTC()
	var restored bool
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var txErr error
		restored, txErr = restoreConflictCandidateTx(tx, fixWork, at, true)
		return txErr
	})
	return restored, err
}

func restoreConflictCandidateTx(tx *store.Tx, fixWork *pendingWork, at time.Time, markExamined bool) (bool, error) {
	if fixWork == nil || fixWork.Decision.Reason != ReasonConflictFix || fixWork.ParentRunID == "" || fixWork.Decision.RunRequest == nil {
		return false, nil
	}
	if err := refuseTerminalItem(tx.Reader(), fixWork.Decision.ItemID, "conflict-repair-resume"); err != nil {
		return false, nil
	}
	_, byRun, err := readPendingHistory(tx.Reader())
	if err != nil {
		return false, err
	}
	parent := byRun[fixWork.ParentRunID]
	if parent == nil || parent.Candidate == nil || parent.Decision.RunRequest == nil {
		return false, nil
	}
	if markExamined {
		if err := appendEffect(tx, examinationDoneKind, fixWork.Decision.RunRequest.RunID, struct{}{}, at); err != nil {
			return false, err
		}
	}
	restored := *parent
	restored.Done = false
	restored.Owner = nil
	restored.Examiner = nil
	restored.Examining = false
	restored.Orphaned = false
	restored.ResumeAfterRestart = false
	restored.NeedsStage = false
	restored.LaneReleased = true
	restored.LaneKeepReason = ""
	restored.ReleasePending = ""
	restored.DirtyHold = nil
	restored.RedCapHold = nil
	restored.RepairHold = nil
	restored.GateStall = nil
	restored.LoopID = ""
	restored.Ready = false
	restored.examinationStartedAt = time.Time{}
	if err := appendEffect(tx, dispatchStartedKind, parent.Decision.RunRequest.RunID, restored, at); err != nil {
		return false, err
	}
	return true, nil
}
