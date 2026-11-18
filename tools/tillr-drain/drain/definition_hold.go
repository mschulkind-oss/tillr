package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A definition-sync-failed hold says the trunk's document could not be applied to its
// item. It is a condition of the trunk and the store, so it ends when they change so that
// the document applies: the mover is whoever corrects the definition on the trunk (or, for
// a reverted landing, the withdrawal of its verdicts), and the next document sync, which
// every trunk move and every loop start runs, observes the correction. No one clears it by
// hand, and nothing is retried on a clock or counted.
//
//   - A held landed design answer (its conflict still awaiting the design run that landed
//     and was examined) is recorded again from the trunk's document. When it applies, the
//     conflict is answered for the build seat and the hold is cleared in the same
//     transaction; when it does not, the hold stands with the new detail.
//   - A hold the whole-document sync wrote (no run) is cleared once a whole sync applies
//     that item's block without a frozen definition or a finding.

// settleDefinitionHolds applies the rule above after a whole-document sync.
func (l *Loop) settleDefinitionHolds(ctx context.Context, outcome SyncOutcome) error {
	if outcome.Skipped != "" {
		return nil
	}
	type heldAnswer struct {
		fault     ItemFault
		candidate Candidate
	}
	var answers []heldAnswer
	var superseded []ItemFault
	if err := l.readStore(ctx, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		var conflicts map[string]item.Conflict
		for _, f := range faults {
			if f.Reason == ReasonDefinitionSyncFailed {
				if conflicts, err = item.ReadConflicts(r); err != nil {
					return err
				}
				break
			}
		}
		for _, f := range faults {
			if f.Reason != ReasonDefinitionSyncFailed {
				continue
			}
			if c, ok := conflicts[f.ItemID]; ok && c.Status == "awaiting" && c.DesignRunID != "" {
				head, landed, err := examinedLanding(r, f.ItemID, c.DesignRunID)
				if err != nil {
					return err
				}
				if landed {
					conflict := c
					answers = append(answers, heldAnswer{f, Candidate{ItemID: f.ItemID, RunID: c.DesignRunID, LaneID: f.LaneID, HeadSHA: head, BlockConflict: &conflict}})
					continue
				}
			}
			if _, failing := outcome.ItemFaults[f.ItemID]; f.RunID == "" && !failing {
				superseded = append(superseded, f)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(answers, func(i, j int) bool { return answers[i].fault.ItemID < answers[j].fault.ItemID })
	for _, a := range answers {
		fault := a.fault
		err := l.recordBlockAnswerThen(ctx, a.candidate, func(tx *store.Tx) error {
			return clearCurrentFault(tx, fault, l.cfg.Now().UTC())
		})
		var held *answerDefinitionError
		switch {
		case errors.As(err, &held):
			if err := l.holdItemFault(ctx, a.candidate, ReasonDefinitionSyncFailed, held.detail); err != nil {
				return err
			}
		case asItemTerminal(err) != nil:
			// A finished item's answer is its terminal settlement, which the pass records.
		case err != nil:
			return err
		default:
			l.reportf("loop: %s: landed design answer %s now applies; conflict %s answered for the build seat\n", a.candidate.ItemID, a.candidate.RunID, a.candidate.BlockConflict.ConflictID)
		}
	}
	if len(superseded) == 0 {
		return nil
	}
	sort.Slice(superseded, func(i, j int) bool { return superseded[i].ItemID < superseded[j].ItemID })
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, f := range superseded {
			if err := clearCurrentFault(tx, f, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, f := range superseded {
		l.reportf("loop: %s: its block now syncs; definition-sync-failed hold %d cleared\n", f.ItemID, f.Seq)
	}
	return nil
}

// clearCurrentFault clears fault when it is still the item's current hold for its reason;
// a newer hold written since it was read stands, and a cleared one is not cleared twice.
func clearCurrentFault(tx *store.Tx, fault ItemFault, at time.Time) error {
	var held, cleared int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(CASE WHEN kind = ? THEN seq END), 0), COALESCE(MAX(CASE WHEN kind = ? THEN seq END), 0) FROM events WHERE kind IN (?, ?) AND object_type = 'item' AND object_id = ? AND json_extract(payload, '$.reason') = ?`,
		itemFaultKind, itemFaultClearedKind, itemFaultKind, itemFaultClearedKind, fault.ItemID, fault.Reason).Scan(&held, &cleared); err != nil {
		return err
	}
	if held != fault.Seq || cleared > held {
		return nil
	}
	body, err := json.Marshal(fault)
	if err != nil {
		return err
	}
	id := fault.ItemID
	_, err = tx.Append(event.Event{Kind: itemFaultClearedKind, ObjectType: "item", ObjectID: &id, Payload: body, At: at})
	return err
}

// examinedLanding answers the head a run landed and whether the loop has examined it: a
// landing whose settlement already ran (and held its answer), not one in flight.
func examinedLanding(r *store.Reader, itemID, runID string) (string, bool, error) {
	var head string
	err := r.QueryRow(`SELECT head_sha FROM landings WHERE item_id = ? AND run_id = ? AND result = 'landed' ORDER BY event_seq DESC LIMIT 1`, itemID, runID).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) || err != nil && strings.Contains(err.Error(), "no such table") {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var examined bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE kind = ? AND object_id = ?)`, examinationDoneKind, runID).Scan(&examined); err != nil {
		return "", false, err
	}
	return head, examined, nil
}
