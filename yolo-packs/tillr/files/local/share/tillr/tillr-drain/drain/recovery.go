package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const setAsideKind = "drain.set-aside"

func init() {
	if err := event.Register(setAsideKind, Utility); err != nil {
		panic(err)
	}
}

// AcquitRun closes the current attributed fault, retaining both the diagnosis and
// the operator's explanation. It does not erase gate verdicts or authorize a retry.
func AcquitRun(tx *store.Tx, runID, why string, at time.Time) error {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(why) == "" {
		return Usagef("acquit needs --run and --why")
	}
	faults, err := ReadItemFaults(tx.Reader())
	if err != nil {
		return err
	}
	for itemID, f := range faults {
		if f.RunID != runID {
			continue
		}
		body, err := json.Marshal(struct {
			ItemFault
			Why string `json:"why"`
		}{f, why})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{Kind: itemFaultClearedKind, ObjectType: "item", ObjectID: &itemID, Payload: body, At: at})
		return err
	}
	return Usagef("run %q has no conviction", runID)
}

// recoveryWork validates the assignment under the same write lock that claims or
// releases it. No lease, including an expired-looking lease, is stolen here.
func recoveryWork(r *store.Reader, runID string) (*pendingWork, error) {
	works, byRun, err := readPendingHistory(r)
	if err != nil {
		return nil, err
	}
	w := byRun[runID]
	if w == nil || w.Done || works[w.Decision.ItemID] != w || w.Decision.RunRequest == nil {
		return nil, Usagef("run %q has no pending candidate in its lane", runID)
	}
	if !w.LaneReleased {
		var state string
		var leasedRun *string
		if err := r.QueryRow(`SELECT state,run_id FROM lanes WHERE lane_id=?`, w.Decision.LaneID).Scan(&state, &leasedRun); err != nil {
			return nil, err
		}
		if state != run.LaneStateIdle || leasedRun != nil {
			return nil, Usagef("lane %q is held; stop its loop before recovery", w.Decision.LaneID)
		}
	}
	rec, err := terminalRecord(r, runID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, Usagef("run %q has no terminal record", runID)
	}
	return w, nil
}

// Examine claims one pending lane and calls the loop's actual examination worker.
// Gate, landing, item verdict, reset and examination records therefore have one
// implementation rather than a second operator-only sequence.
func (l *Loop) Examine(ctx context.Context, runID string) (LoopResult, error) {
	result := LoopResult{}
	if strings.TrimSpace(runID) == "" {
		return result, Usagef("examine needs --run")
	}
	if err := l.finishReleaseIntent(ctx, runID); err != nil {
		return result, err
	}
	owner, err := currentLoopOwner()
	if err != nil {
		return result, err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return result, err
	}
	defer s.Close()
	var w *pendingWork
	var d Decision
	at := l.cfg.Now().UTC()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		w, err = recoveryWork(tx.Reader(), runID)
		if err != nil {
			return err
		}
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		if f, ok := faults[w.Decision.ItemID]; ok {
			return Usagef("item %q is held by fault %d: %s; diagnose and acquit first", f.ItemID, f.Seq, f.Detail)
		}
		if w.BoundaryFailed {
			return Usagef("run %q has a boundary conviction; acquit it first", runID)
		}
		// Use the complete planner, not pendingDecision alone: it observes
		// current repair inputs and invalidates changed workflows. Planning
		// does not apply other items or reap other lanes for this command.
		lanes := []string{w.Decision.LaneID}
		if w.LaneReleased {
			lanes = l.cfg.Lanes
		}
		tick := Pass{examineItem: w.Decision.ItemID, Build: &l.build, Now: at.Format(time.RFC3339), AvailableLanes: &lanes}
		if stager, ok := l.cfg.Stage.(*LaneStager); ok {
			tick.BriefTemplate = &stager.Template
		}
		questions := &VantageQuestions{Root: l.cfg.Root, Command: l.cfg.QuestionsCmd, Context: ctx}
		plans, err := Plan(tx.Reader(), tick, l.cfg.Policy, questions)
		if err != nil {
			return err
		}
		for _, plan := range plans {
			var next Decision
			if err := json.Unmarshal(plan.Payload, &next); err != nil {
				return err
			}
			if next.ItemID == w.Decision.ItemID {
				d = next
				break
			}
		}
		if d.RunRequest != nil && d.RunRequest.RunID != runID {
			return Usagef("run %q is no longer the item's pending transition", runID)
		}
		if d.Action != ActionJudge && d.Action != ActionSettle {
			return Usagef("run %q cannot be examined: %s", runID, d.Reason)
		}
		generation, err := allocateGeneration(tx)
		if err != nil {
			return err
		}
		d.PassID = generation
		request := *d.RunRequest
		request.CorrelationID = generation
		d.RunRequest = &request
		result.FirstGeneration, result.LastGeneration = generation, generation
		var state string
		var leasedRun *string
		if err := tx.QueryRow(`SELECT state,run_id FROM lanes WHERE lane_id=?`, d.LaneID).Scan(&state, &leasedRun); err != nil && err != sql.ErrNoRows {
			return err
		} else if err == nil && (state != run.LaneStateIdle || leasedRun != nil) {
			return Usagef("lane %q is held; stop its loop before recovery", d.LaneID)
		}
		if err := l.assignPinnedCandidate(tx, w, &d, at); err != nil {
			return err
		}
		kind := examinationOwnerKind
		if d.Workflow != nil {
			kind = examinationClaimedKind
		}
		if err := appendEffect(tx, kind, runID, owner, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES(?,?,?,NULL) ON CONFLICT(lane_id) DO UPDATE SET state=excluded.state,run_id=excluded.run_id,leased_until=NULL`, d.LaneID, run.LaneStateLeased, runID); err != nil {
			return err
		}
		return recordPassDecision(tx, d, at)
	})
	if err != nil {
		return result, err
	}
	l.launchExamination(ctx, d, w, at, &PassTiming{TickID: d.PassID, AtMS: at.UnixMilli()}, nil)
	completion := <-l.completions
	l.examinations.Wait()
	mergeExamination(&result, completion.result)
	result.Stop = completion.outcome.stop
	result.Detail = completion.detail
	if completion.outcome.err == nil && result.Stop == "" && !l.nonLanding {
		if err := l.pruneRunRefs(ctx); err != nil {
			return result, err
		}
	}
	if completion.outcome.err != nil || result.Stop != "" || d.Action != ActionSettle {
		return result, completion.outcome.err
	}
	// Settlement prepares an unrecorded terminal candidate; the ordinary
	// loop judges it on its next pass. Advance only that same run, never a
	// new workflow node or retry. Judgment itself is still one transition.
	prepared := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		work := works[d.ItemID]
		prepared = work != nil && !work.Done && work.Candidate != nil && work.Decision.RunRequest != nil && work.Decision.RunRequest.RunID == runID
		return nil
	})
	if err != nil || !prepared {
		return result, err
	}
	next, err := l.Examine(ctx, runID)
	mergeExamination(&result, &next)
	result.LastGeneration, result.Stop, result.Detail = next.LastGeneration, next.Stop, next.Detail
	return result, err
}

func recordSetAside(tx *store.Tx, c Candidate, ref, why string, at time.Time) error {
	body := struct {
		Candidate Candidate `json:"candidate"`
		Ref       string    `json:"ref"`
		Why       string    `json:"why"`
	}{c, ref, why}
	if err := appendEffect(tx, setAsideKind, c.RunID, body, at); err != nil {
		return err
	}
	if err := appendEffect(tx, examinationDoneKind, c.RunID, struct{}{}, at); err != nil {
		return err
	}
	f := ItemFault{ItemID: c.ItemID, RunID: c.RunID, LaneID: c.LaneID, Reason: ReasonLandRefused, Detail: fmt.Sprintf("candidate set aside at %s for rework: %s", ref, why)}
	return appendEffect(tx, itemFaultKind, c.ItemID, f, at)
}

// SetAside preserves a committed candidate at the run's named retention ref and
// releases the lane together with an append-only rework hold. Git cannot enlist
// in SQLite: the retention ref is written first, and a failed database commit
// restores the lane head. An interrupted invocation can be retried using that ref.
func (l *Loop) SetAside(ctx context.Context, itemID, why string) (string, error) {
	if strings.TrimSpace(itemID) == "" || strings.TrimSpace(why) == "" {
		return "", Usagef("set-aside needs --item and --why")
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var ref, head, worktree string
	moved := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		w := works[itemID]
		if w == nil || w.Decision.RunRequest == nil {
			return Usagef("item %q has no pending candidate", itemID)
		}
		w, err = recoveryWork(tx.Reader(), w.Decision.RunRequest.RunID)
		if err != nil {
			return err
		}
		if err := refuseTerminalItem(tx.Reader(), itemID, "set-aside"); err != nil {
			return err
		}
		if w.Candidate == nil {
			return Usagef("item %q has no recorded candidate to set aside; examine its terminal run first", itemID)
		}
		c := *w.Candidate
		if c.BaseRef != "" {
			return Usagef("set-aside does not release patch-branch candidates")
		}
		if w.LaneReleased {
			ref = "refs/tillr/runs/" + c.RunID
			if err := l.pinnedCandidate(ctx, c); err != nil {
				return err
			}
			return recordSetAside(tx, c, ref, why, l.cfg.Now().UTC())
		}
		worktree = c.Worktree
		head = c.HeadSHA
		ref = "refs/tillr/runs/" + c.RunID
		if worktree != l.laneTarget(c.LaneID) {
			return Usagef("candidate %q is not in its assigned lane", c.RunID)
		}
		status, err := l.git(ctx, worktree, "status", "--porcelain")
		if err != nil {
			return err
		}
		if strings.TrimSpace(status) != "" {
			return Usagef("lane %q has uncommitted work; commit it before set-aside", c.LaneID)
		}
		current, err := l.revParse(ctx, worktree, "HEAD")
		if err != nil {
			return err
		}
		base, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return err
		}
		pinned, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(objectname)", ref)
		if err != nil {
			return err
		}
		// Preserve the reset destination independently of trunk movement. This
		// intent survives rollback or process death before SQLite's commit.
		resetRef := "refs/tillr/runs/" + c.RunID + "-set-aside-base"
		resetHead, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(objectname)", resetRef)
		if err != nil {
			return err
		}
		resetHead = strings.TrimSpace(resetHead)
		if current != head && !(resetHead != "" && current == resetHead && strings.TrimSpace(pinned) == head) {
			return Usagef("lane HEAD differs from recorded candidate %s", head)
		}
		if strings.TrimSpace(pinned) != "" && strings.TrimSpace(pinned) != head {
			return Usagef("retention ref %s already names another candidate", ref)
		}
		if resetHead == "" {
			if _, err := l.git(ctx, l.cfg.Root, "update-ref", resetRef, base, strings.Repeat("0", 40)); err != nil {
				return err
			}
			resetHead = base
		}
		if err := l.pin(ctx, c.RunID, head); err != nil {
			return err
		}
		if _, err := l.git(ctx, worktree, "reset", "--hard", resetHead); err != nil {
			return err
		}
		moved = true
		at := l.cfg.Now().UTC()
		if err := recordSetAside(tx, c, ref, why, at); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=?`, run.LaneStateIdle, c.LaneID)
		return err
	})
	if err != nil && moved {
		if _, restoreErr := l.git(context.WithoutCancel(ctx), worktree, "reset", "--hard", head); restoreErr != nil {
			return ref, Externalf("set-aside failed: %v; restoring retained candidate also failed: %v", err, restoreErr)
		}
	}
	return ref, err
}
