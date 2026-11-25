package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A retry refusal names its mover (docs/design/landing-principles.md, section 13): either
// the exact command the face would accept, or that none exists and who moves instead. On
// 2026-11-17 an operator facing eight owner holds could not learn from any refusal which
// spelling would have been accepted.

// retryCommand is the exact command this face accepts for itemID and runID. The two
// placeholders are the only words an operator supplies.
func retryCommand(itemID, runID, ack string) string {
	cmd := fmt.Sprintf("tillr drain retry --item %s --run %s --by <who> --why <reason>", itemID, runID)
	if ack != "" {
		cmd += " --acknowledge " + ack
	}
	return cmd
}

// noRetryCommand states that no retry command is accepted, and who moves.
func noRetryCommand(mover string) string {
	return "No retry command is accepted: " + mover + "."
}

// acceptedAck is the acknowledgment a run's record demands: the gate's word for a judged
// run, the wall's for an unjudged one, and none for a harness failure.
func acceptedAck(judged bool, class AttemptClass) string {
	switch {
	case judged:
		return RetryAckGateFailed
	case class == AttemptUnjudged:
		return RetryAckUnjudged
	}
	return ""
}

// latestRunCommand names the item's latest finished run as the run a retry would name, or
// states that the item has none.
func latestRunCommand(r *store.Reader, itemID string) string {
	var runID string
	err := r.QueryRow(
		`SELECT run_id FROM runs WHERE item_id = ? AND state IN (?, ?, ?)
		  ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`,
		itemID, run.StateExited, run.StateFailedToStart, run.StateTimedOut,
	).Scan(&runID)
	if err != nil || runID == "" {
		return noRetryCommand(fmt.Sprintf("item %q has no finished run, so the loop moves: it dispatches the item on the next pass that admits it", itemID))
	}
	return fmt.Sprintf("Item %q's latest finished run is %s. A retry names it: %s", itemID, runID, retryCommand(itemID, runID, ""))
}

// authorizeDesignRetry is the retry face's design form. When the named run is a design
// run of the item's conflict, it answers the authorization (or the refusal), and the
// caller writes nothing else; it answers nil, nil when the run is no design run, so the
// build retry applies.
//
// The conflict must be with the owner and name this run: that is the hold a design run's
// objection naming the owner leaves (reportedBlock), or one written before the owner's
// ruling of 2026-11-17. The authorization raises the conflict for one more design run,
// and that run is settled like any other: its answer lands, its objection routes, and its
// other endings go back to the design seat (settleUnansweredDesignRuns). A design run the
// conflict no longer names is stale, and the refusal names the current one; a conflict the
// loop is still settling, or has already raised, is the loop's.
func authorizeDesignRetry(ctx context.Context, tx *store.Tx, req RetryRequest, at time.Time) (*Retry, error) {
	r := tx.Reader()
	designRuns, err := designRunsOf(r, req.ItemID)
	if err != nil {
		return nil, err
	}
	if !designRuns[req.RunID] {
		return nil, nil
	}
	conflicts, err := item.ReadConflicts(r)
	if err != nil {
		return nil, readFailure(err)
	}
	c := conflicts[req.ItemID]
	switch {
	case c.TriageVersion != "":
		remedy := "the owner moves: amend the request (tillr item apply), or release or close the item"
		if hasLanded, err := obstacles.HasLanded(r, req.ItemID); err == nil && hasLanded {
			remedy = "the owner moves: amend the request (tillr item apply), or release the item"
		}
		return nil, Usagef("run %q is a triage run of item %q's request: a triage conflict is raised again when the request changes. %s", req.RunID, req.ItemID, noRetryCommand(remedy))
	case c.Status == "owner" && c.DesignRunID == req.RunID:
	case c.Status == "owner" && c.DesignRunID != "":
		return nil, Usagef("design run %q is not the one item %q's conflict %s now names: that is design run %s. The accepted command: %s", req.RunID, req.ItemID, c.ConflictID, c.DesignRunID, retryCommand(req.ItemID, c.DesignRunID, ""))
	case c.Status == "awaiting" || c.Status == "raised":
		wait, _, err := item.ReadWaitingOn(r, req.ItemID)
		if err != nil {
			return nil, readFailure(err)
		}
		if c.HasIndependentWait(wait) {
			blocker, err := item.LandedBlocker(r, wait)
			if err != nil {
				return nil, readFailure(err)
			}
			if blocker != "" {
				return nil, Usagef("item %q's blocker %s landed. %s", req.ItemID, blocker,
					noRetryCommand(fmt.Sprintf("the loop releases the wait on its next pass, or use tillr item state --item %s --state in-queue", req.ItemID)))
			}
		}
		waits := "the design seat's next dispatch"
		if c.Status == "awaiting" {
			waits = "the settlement of design run " + c.DesignRunID
		}
		return nil, Usagef("item %q's conflict %s is %s, waiting on %s. %s", req.ItemID, c.ConflictID, c.Status, waits, noRetryCommand("the loop moves"))
	default:
		return nil, Usagef("design run %q no longer holds item %q: its conflict %s is %q. %s", req.RunID, req.ItemID, c.ConflictID, c.Status, noRetryCommand("the loop moves, from the conflict's own state (tillr drain why)"))
	}
	if err := refuseTerminalItem(r, req.ItemID, "design-retry"); err != nil {
		return nil, Usagef("%v. %s", err, noRetryCommand("the owner moves: tillr item reopen puts the item back first"))
	}
	retry := Retry{
		RetryID:          "retry-design-" + req.ItemID + "-" + req.RunID,
		ItemID:           req.ItemID,
		SupersedesRunID:  req.RunID,
		RequestedBy:      req.By,
		Reason:           req.Why,
		RequestedAt:      at,
		ReraisesConflict: c.ConflictID,
	}
	c.Text += "\n\n" + req.By + " raised the conflict again after design run " + req.RunID + " (tillr drain retry): " + req.Why + ". The design seat gets one more design run; the owner decides again only if it objects naming the owner."
	c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
	if err := item.RecordConflict(tx, c, at); err != nil {
		return nil, Internalf("raising conflict %s for %q: %v", c.ConflictID, req.ItemID, err)
	}
	body, err := json.Marshal(retry)
	if err != nil {
		return nil, Internalf("encoding the design retry for %q: %v", req.ItemID, err)
	}
	id := req.RunID
	if _, err := tx.Append(event.Event{Kind: DesignRetryKind, ObjectType: "run", ObjectID: &id, Payload: body, At: at}); err != nil {
		return nil, Internalf("recording the design retry for %q: %v", req.ItemID, err)
	}
	return &retry, nil
}

// designRunsOf answers every design run any of the item's conflict transitions has named.
func designRunsOf(r *store.Reader, itemID string) (map[string]bool, error) {
	rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, item.ConflictKind)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]bool{}, nil
		}
		return nil, readFailure(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, readFailure(err)
		}
		var c item.Conflict
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, Internalf("reading a conflict transition: %v", err)
		}
		if c.ItemID == itemID && c.DesignRunID != "" {
			out[c.DesignRunID] = true
		}
	}
	return out, rows.Err()
}
