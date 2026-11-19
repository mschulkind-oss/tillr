package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A pending run whose item reached a terminal state by another route — landed or closed by
// hand, or resolved elsewhere — is not judged. Its candidate is not gated or landed, no fault,
// refusal or conflict is recorded against the item, and the item's state and verdicts are
// left exactly as they are. The run is settled once: its head is pinned, its lane is reset
// and released, and one event says why.
//
// The planner is where this is decided for every examination: each settle and judge the
// loop performs, including a takeover of a killed loop's pending record, comes from a plan
// (Plan's pending-work branch). Each step that would record something against the item
// rereads its state, because a hand landing can commit while a run is being examined;
// those that write in their own short transaction reread it there.
const (
	// ReasonItemAlreadyTerminal is the settle decision for such a run.
	ReasonItemAlreadyTerminal = "item-already-terminal"
	itemTerminalSettledKind   = "drain.item-already-terminal"
)

func init() {
	if err := event.Register(itemTerminalSettledKind, Utility); err != nil {
		panic(err)
	}
}

// itemTerminalSettlement is the settling event's payload. Step names where the terminal
// state was found: "plan" when the pass planned the settlement, or the judging step that
// reread it during the examination.
type itemTerminalSettlement struct {
	ItemID    string `json:"item_id"`
	RunID     string `json:"run_id"`
	LaneID    string `json:"lane_id"`
	ItemState string `json:"item_state"`
	Step      string `json:"step"`
	HeadSHA   string `json:"head_sha,omitempty"`
	Landed    bool   `json:"landed_by_this_run,omitempty"`
	Detail    string `json:"detail"`
}

// itemTerminalError stops an examination at the step that found the item terminal.
// launchExamination turns it into the settlement rather than a loop error.
type itemTerminalError struct {
	ItemID, State, Step string
	// Landed is set when this run's own landing moved the trunk before the reread.
	Landed bool
}

func (e *itemTerminalError) Error() string {
	return fmt.Sprintf("item %s is already %s (found at %s)", e.ItemID, e.State, e.Step)
}

func asItemTerminal(err error) *itemTerminalError {
	var terminal *itemTerminalError
	if errors.As(err, &terminal) {
		return terminal
	}
	return nil
}

// refuseTerminalItem answers an itemTerminalError when the item is in a terminal state. It
// runs inside the caller's read or write transaction, so a write guarded by it cannot
// commit against an item that went terminal before that transaction began.
func refuseTerminalItem(r *store.Reader, itemID, step string) error {
	var state string
	err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&state)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	declared, err := definition.ForReader(r)
	if err != nil {
		return err
	}
	if finishedState(declared.Parsed, state) {
		return &itemTerminalError{ItemID: itemID, State: state, Step: step}
	}
	return nil
}

// checkItemTerminal is refuseTerminalItem in its own read, for a step whose next act is a
// subprocess (the gate, the lander) or a write made elsewhere.
func (l *Loop) checkItemTerminal(ctx context.Context, itemID, step string) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.ReadTx(ctx, func(r *store.Reader) error { return refuseTerminalItem(r, itemID, step) })
}

// terminalSettlement replaces a pending run's examination with its settlement when the item
// is already terminal, and answers false when the run must keep its ordinary hold: a run
// with no terminal record is still live, and a leased lane is still the run's or another
// examination's. A host-boundary violation keeps its hold too, because that fault is about
// the host rather than the item, and only the operator clears it. A dirty-path,
// trunk-rewritten or repair hold waits for something that no longer matters, so it settles.
func terminalSettlement(r *store.Reader, work *pendingWork, at time.Time) (Decision, bool, error) {
	if work.BoundaryFailed || work.Decision.RunRequest == nil {
		return Decision{}, false, nil
	}
	if work.Candidate == nil {
		rec, err := terminalRecord(r, work.Decision.RunRequest.RunID)
		if err != nil || rec == nil {
			return Decision{}, false, err
		}
	}
	if !work.LaneReleased {
		leases, err := activeLeases(r, at, []string{work.Decision.LaneID})
		if err != nil || len(leases) != 0 {
			return Decision{}, false, err
		}
	}
	settled := work.Decision
	settled.From = "running"
	if work.Candidate != nil {
		settled.From = "candidate"
	}
	settled.Action, settled.Reason, settled.To = ActionSettle, ReasonItemAlreadyTerminal, "held"
	identifyDecision(&settled)
	return settled, true, nil
}

// settleTerminalItem settles one claimed run whose item is terminal. The Git work (the pin
// and the lane reset) runs with no transaction open; the write that marks the run examined
// and records why rereads the item's state, so the event names the state it saw.
func (l *Loop) settleTerminalItem(ctx context.Context, work *pendingWork, found *itemTerminalError) error {
	d := work.Decision
	runID := d.RunRequest.RunID
	if err := pipelineCloseGraph(l.cfg.Root, runID); err != nil {
		return err
	}
	head := ""
	if work.Candidate != nil {
		head = work.Candidate.HeadSHA
	}
	if head == "" && !l.nonLanding {
		head = l.headOf(ctx, d.LaneID)
	}
	// No spend is counted here. A run found terminal by the plan is a record an earlier
	// loop left, and this pass judged nothing; a run found terminal during its settlement
	// was already counted by that settlement.
	if l.nonLanding {
		// A hook move can outlive the supervisor before end-of-run records.
		// Recover its spend without repeating the hook or touching Git.
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return err
		}
		var rec *run.Record
		err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; rec, err = terminalRecord(r, runID); return err })
		s.Close()
		if err != nil {
			return err
		}
		if rec != nil {
			if err := l.recordSettlementSpend(ctx, d.RunRequest.CorrelationID, work.LoopID, *rec); err != nil {
				return err
			}
		}
	} else {
		var stop string
		if found.Landed {
			stop = l.abandon(ctx, d.LaneID, "", "")
		} else {
			stop = l.abandon(ctx, d.LaneID, runID, head, "item already terminal")
		}
		if stop != "" {
			return retainedAfter(d.LaneID, stop)
		}
		if err := l.prunePreservedWork(ctx); err != nil {
			return err
		}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	body := itemTerminalSettlement{ItemID: d.ItemID, RunID: runID, LaneID: d.LaneID, ItemState: found.State, Step: found.Step, HeadSHA: head, Landed: found.Landed}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var state string
		if err := tx.QueryRow(`SELECT state FROM items WHERE item_id = ?`, d.ItemID).Scan(&state); err != nil {
			return err
		}
		body.ItemState = state
		body.Detail = fmt.Sprintf("%s was already %s when run %s was examined (%s): nothing was gated, landed or recorded against it", d.ItemID, state, runID, found.Step)
		if found.Landed {
			body.Detail = fmt.Sprintf("%s was already %s when run %s's landing finished: the trunk carries the change, and the item's state and verdicts were left as they were", d.ItemID, state, runID)
		}
		at := l.cfg.Now().UTC()
		if err := appendEffect(tx, examinationDoneKind, runID, struct{}{}, at); err != nil {
			return err
		}
		return appendEffect(tx, itemTerminalSettledKind, runID, body, at)
	})
	if err != nil {
		return err
	}
	l.reportf("loop: %s\n", body.Detail)
	return nil
}

// resolveElsewhere is the already-on-trunk settlement's state write, made in one short
// transaction that rereads the item first. An item landed by hand while the lander ran is
// left landed: the change is on the trunk either way, and the hand landing is the record.
func (l *Loop) resolveElsewhere(ctx context.Context, c Candidate) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "already-on-trunk"); err != nil {
			return err
		}
		_, err := item.SetStateWithReceipt(ctx, tx, c.ItemID, StateResolvedElsewhere, l.cfg.Now().UTC(), item.MoveReceipt{
			ResolvesVia: "commit:" + c.HeadSHA, Reason: "candidate change is already on the trunk", By: Utility,
		})
		if rejection, ok := item.RejectionOf(err); ok && rejection.Rule == "already-landed" {
			_, err = item.SetState(ctx, tx, c.ItemID, "landed", l.cfg.Now().UTC())
		}
		return err
	})
}
