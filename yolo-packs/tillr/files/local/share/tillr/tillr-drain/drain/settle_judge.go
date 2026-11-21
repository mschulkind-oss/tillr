package drain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) runCandidateExamination(ctx context.Context, work *pendingWork, local *LoopResult, timing *PassTiming) laneOutcome {
	c := *work.Candidate
	c.TickID = timing.TickID
	var stop string
	var landed bool
	var err error
	if work.RepairHold != nil {
		// Retry the retained repair, not another gate and land of the same
		// original candidate. The original conflict remains the evidence.
		stop, landed, err = l.conflictFix(ctx, c, work.RepairHold.Conflict, local, timing, work.RepairHold.TrunkSHA)
	} else if work.DirtyHold != nil || work.Gated {
		stop, landed, err = l.landCandidate(ctx, c, local, timing)
	} else {
		stop, landed, err = l.verify(ctx, &c, local, timing)
	}
	if terminal := asItemTerminal(err); terminal != nil {
		// The item went terminal by another route during this examination.
		return laneOutcome{err: l.settleTerminalItem(ctx, work, terminal)}
	}
	held, keep := retainLane(err)
	itemFault := errors.Is(err, errItemFaultHeld)
	repairHeld := errors.Is(err, errRepairHeld)
	if held {
		err = nil
		if !keep {
			copy := *work
			copy.Candidate = &c
			reason := ""
			if repairHeld {
				reason = ReasonRepairStagingFailed
			}
			// A candidate mismatch must retain the unexpected checkout.
			if itemFault {
				if head, headErr := l.revParse(ctx, c.Worktree, "HEAD"); headErr != nil || head != c.HeadSHA {
					reason = ReasonCandidateMismatch
				}
			}
			_, err = l.releaseCandidate(ctx, &copy, reason, true)
		}
	}
	var pinErr *runPinError
	if (err != nil || stop != "") && !errors.As(err, &pinErr) {
		if pinErr := l.pinCandidateHead(ctx, c); pinErr != nil {
			if err == nil {
				return laneOutcome{err: pinErr}
			}
			// Keep the original error when the pin was a secondary failure.
			l.reportf("loop: %v\n", pinErr)
		}
	}
	if err == nil && stop == "" && !held {
		if landed {
			stop = l.abandon(ctx, c.LaneID, "", "")
			err = l.prunePreservedWork(ctx)
		} else {
			stop = l.abandon(ctx, c.LaneID, c.RunID, c.HeadSHA)
		}
		if stop == "" && err == nil {
			err = l.markExamined(ctx, work.Decision)
		}
	}
	return laneOutcome{stop: stop, err: err}
}

// judgeSettledCandidate continues straight into gate and land inside the same
// background examination when settlement yielded a candidate ready for the gate
// on its own lane, unless a hold on the item or a waiting interactive request
// for that lane needs the planner to interpose.
func (l *Loop) judgeSettledCandidate(ctx context.Context, d Decision, local *LoopResult, timing *PassTiming) (laneOutcome, string) {
	work, claimed, err := l.claimSettledCandidateJudge(ctx, d, timing)
	if err != nil {
		return laneOutcome{err: err}, ActionJudge
	}
	if !claimed {
		return laneOutcome{}, ActionSettle
	}
	return l.runCandidateExamination(ctx, work, local, timing), ActionJudge
}

func (l *Loop) claimSettledCandidateJudge(ctx context.Context, d Decision, timing *PassTiming) (*pendingWork, bool, error) {
	if l.nonLanding || d.RunRequest == nil || d.LaneID == "" {
		return nil, false, nil
	}
	if stopped, err := l.stopRequested(ctx); err != nil || stopped {
		return nil, false, err
	}
	if pending, err := l.buildRestartPending(ctx); err != nil || pending {
		return nil, false, err
	}
	here, err := currentLoopOwner()
	if err != nil {
		return nil, false, err
	}
	rs, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, false, err
	}
	var (
		ready             bool
		judgeDecision     Decision
		version           string
		interactiveDigest string
		definitionSHA     string
	)
	err = rs.ReadTx(ctx, func(r *store.Reader) error {
		folds, err := readVersionFolds(r)
		if err != nil {
			return err
		}
		w := folds.works[d.ItemID]
		if w == nil || w.Done || w.Candidate == nil || w.LaneReleased || w.Decision.LaneID != d.LaneID || w.Decision.RunRequest == nil || w.Decision.RunRequest.RunID != d.RunRequest.RunID {
			return nil
		}
		owned, err := examinationOwned(r, w, d, here)
		if err != nil || !owned {
			return err
		}
		available, err := availableRotationLanes(r, l.cfg.Lanes)
		if err != nil {
			return err
		}
		now := l.cfg.Now().UTC()
		tick := Pass{
			Comments:            l.cfg.Comments,
			WorkflowDefaults:    l.cfg.WorkflowDefaults,
			GatesInFlight:       l.gateCount(),
			Build:               &l.build,
			StorePath:           l.cfg.Store,
			Now:                 now.Format(time.RFC3339Nano),
			AvailableLanes:      &available,
			ownExaminationLane:  d.LaneID,
			ownExaminationRunID: d.RunRequest.RunID,
		}
		if stager, ok := l.cfg.Stage.(*LaneStager); ok {
			tick.BriefTemplate = &stager.Template
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return readFailure(err)
		}
		items, err := readItems(r)
		if err != nil {
			return err
		}
		// Only plan the retained candidate unless we need lane offers to decide
		// whether an interactive request should take priority. Do not run every
		// queued item's readiness checks on the way to this gate.
		tick.examineItem = d.ItemID
		byItem := make(map[string]itemRow)
		for _, row := range items {
			if row.ItemID != d.ItemID && (row.Express || item.IsCommentItem(row.ItemID)) {
				pending := folds.works[row.ItemID]
				// Retained work on another lane cannot be waiting for this
				// lane, regardless of what the planner answers for it.
				if pending != nil && !pending.Done && !pending.LaneReleased && pending.Decision.LaneID != d.LaneID {
					continue
				}
				byItem[row.ItemID] = row
				if !finishedState(declared.Parsed, row.State) || (pending != nil && !pending.Done) {
					tick.examineItem = ""
				}
			}
		}
		questions := &VantageQuestions{Root: l.cfg.Root, Command: l.cfg.QuestionsCmd, Context: ctx}
		envs, err := planDefinition(r, tick, l.currentPolicy(), questions, declared.Parsed)
		if err != nil {
			return err
		}
		foundJudge := false
		for _, env := range envs {
			var planned Decision
			if err := json.Unmarshal(env.Payload, &planned); err != nil {
				return err
			}
			if planned.ItemID == d.ItemID {
				if planned.Action != ActionJudge || planned.LaneID != d.LaneID || planned.RunRequest == nil || planned.RunRequest.RunID != d.RunRequest.RunID {
					return nil
				}
				judgeDecision = planned
				foundJudge = true
				continue
			}
			if row, ok := byItem[planned.ItemID]; ok && interactiveRequestWaitsForLane(row, planned, d.LaneID, folds.works[row.ItemID], l.cfg.Comments) {
				return nil
			}
		}
		if !foundJudge {
			return nil
		}
		version, err = inputVersionFrom(r, judgeDecision, folds)
		if err != nil {
			return err
		}
		interactiveDigest, err = interactiveQueueDigest(r)
		if err != nil {
			return err
		}
		definitionSHA = declared.SHA256
		ready = true
		return nil
	})
	rs.Close()
	if err != nil || !ready {
		return nil, false, err
	}

	ws, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, false, err
	}
	defer ws.Close()
	var claimed *pendingWork
	err = ws.WriteTx(ctx, func(tx *store.Tx) error {
		ok, err := currentDefinition(tx.Reader(), plannedApplication{definitionSHA: definitionSHA})
		if err != nil || !ok {
			return err
		}
		folds, err := readVersionFolds(tx.Reader())
		if err != nil {
			return err
		}
		w := folds.works[d.ItemID]
		if w == nil || w.Done || w.Candidate == nil || w.LaneReleased || w.Decision.LaneID != d.LaneID || w.Decision.RunRequest == nil || w.Decision.RunRequest.RunID != d.RunRequest.RunID {
			return nil
		}
		owned, err := examinationOwned(tx.Reader(), w, d, here)
		if err != nil || !owned {
			return err
		}
		currentVersion, err := inputVersionFrom(tx.Reader(), judgeDecision, folds)
		if err != nil || currentVersion != version {
			return err
		}
		currentInteractive, err := interactiveQueueDigest(tx.Reader())
		if err != nil || currentInteractive != interactiveDigest {
			return err
		}
		claimAt := l.cfg.Now().UTC()
		passID := timing.TickID
		if passID == "" {
			passID, err = allocateGeneration(tx)
			if err != nil {
				return err
			}
			timing.TickID = passID
		}
		judgeDecision.PassID = passID
		req := *judgeDecision.RunRequest
		req.CorrelationID = passID
		judgeDecision.RunRequest = &req
		if err := recordPassDecision(tx, judgeDecision, claimAt); err != nil {
			return err
		}
		copy := *w
		copy.Decision = judgeDecision
		claimed = &copy
		return nil
	})
	return claimed, claimed != nil && err == nil, err
}

func examinationOwned(r *store.Reader, w *pendingWork, d Decision, here *LoopOwner) (bool, error) {
	if d.Workflow != nil {
		if !w.Examining || w.Owner == nil || *w.Owner != *here {
			return false, nil
		}
	} else {
		if w.Examiner == nil || *w.Examiner != *here {
			return false, nil
		}
	}
	var n int
	err := r.QueryRow(`SELECT COUNT(*) FROM lanes WHERE lane_id=? AND state=? AND run_id=? AND leased_until IS NULL`, d.LaneID, run.LaneStateLeased, d.RunRequest.RunID).Scan(&n)
	return n == 1, err
}

func availableRotationLanes(r *store.Reader, offered []string) ([]string, error) {
	events, err := r.EventsOfKind(0, laneRefusedKind, laneRestoredKind)
	if err != nil {
		return nil, readFailure(err)
	}
	refused := map[string]bool{}
	for _, e := range events {
		if e.ObjectType != "lane" || e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case laneRefusedKind:
			refused[*e.ObjectID] = true
		case laneRestoredKind:
			delete(refused, *e.ObjectID)
		}
	}
	lanes := make([]string, 0, len(offered))
	for _, lane := range offered {
		if !refused[lane] {
			lanes = append(lanes, lane)
		}
	}
	return lanes, nil
}

func interactiveRequestWaitsForLane(row itemRow, decision Decision, laneID string, work *pendingWork, comments *settings.Comments) bool {
	if !row.Express && !item.IsCommentItem(row.ItemID) {
		return false
	}
	if work != nil && !work.Done && !work.LaneReleased {
		return work.Decision.LaneID == laneID && decision.Action == ActionHold && decision.Reason == ReasonNoLane
	}
	if decision.Action == ActionHold {
		switch decision.Reason {
		case ReasonNoLane:
			return row.Express && !strings.HasPrefix(laneID, "desk-")
		case ReasonDocumentDeskBusy:
			doc := deskDocument(row, comments)
			return doc != "" && commentDesk(doc) == laneID
		case ReasonDeskCapacity:
			return documentDesk(laneID)
		}
	}
	if row.Express && decision.Returned != nil && decision.Returned.WorkerSession != nil &&
		decision.Returned.WorkerSession.LaneID == laneID && decision.WorkerSessionReason == "lane-unavailable" {
		return true
	}
	return false
}

func interactiveQueueDigest(r *store.Reader) (string, error) {
	var hasExpress bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
		return "", readFailure(err)
	}
	expressColumn := "0"
	if hasExpress {
		expressColumn = "COALESCE(express, 0)"
	}
	rows, err := r.Query(`SELECT item_id, state, COALESCE(rank, ''), ` + expressColumn + `, updated_at FROM items ORDER BY item_id`)
	if err != nil {
		return "", readFailure(err)
	}
	defer rows.Close()
	sum := sha256.New()
	for rows.Next() {
		var (
			id, state, rank string
			express         bool
			updatedAt       int64
		)
		if err := rows.Scan(&id, &state, &rank, &express, &updatedAt); err != nil {
			return "", readFailure(err)
		}
		if !express && !strings.HasPrefix(id, "item-idea-") && !item.IsCommentItem(id) {
			continue
		}
		raw, _ := json.Marshal([]any{id, state, rank, express, updatedAt})
		sum.Write(raw)
		sum.Write([]byte{'\n'})
	}
	if err := rows.Err(); err != nil {
		return "", readFailure(err)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
