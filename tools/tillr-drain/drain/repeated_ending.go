package drain

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// repeatReading is a test seam: it runs where the routing starts reading the store's
// history, so a test can see which lock that reading holds.
var repeatReading func()

// IdenticalRepeatPrefix opens the text of a conflict an identical repeat raises for the
// design seat. A conflict whose text opens with it, or with the removed rule's
// "Second failed attempt:", is that routing's own and is not enriched twice.
const IdenticalRepeatPrefix = "Identical repeat:"

// secondFailure is an identical repeat's routing to the design seat, as read from the store
// before the write that records it: the item, the run the conflict is named for, the run it
// is raised on, and the evidence text. (The name is the removed second-failure rule's; the
// routing it records is the same conflict for the design seat.)
type secondFailure struct {
	WorkerSession                     *WorkerSession
	itemID, outcomeRun, current, text string
	// noDesignSeat is a policy that names no design seat: no seat can diagnose the repeat,
	// so it is the owner's (the policy is the owner's to change).
	noDesignSeat bool
}

// producerOf is who produced a run's ending, as the identical-repeat rule compares it: the
// design seat for a run that answered a conflict, else the seat the item's run acted for.
func producerOf(d Decision) string {
	switch {
	case d.BlockConflict != nil:
		return "design"
	case d.Seat != "":
		return d.Seat
	}
	return "build"
}

// readRepeatedEnding answers the routing an examination of d raises, or nil. The owner ruled
// on 2026-11-17 that a worker run's failure is never counted: a gate refusal, a run with no
// commit, a fence escape and a refused landing go back to the worker with the evidence
// every time (ReasonReturned). The one thing that sends such an ending elsewhere is an
// identical repeat (repeatedEnding): the same producer, cause and evidence key as an earlier
// ending of the item. Then the worker is not sent the same thing again; the conflict goes
// to the design seat with both records, and the design run must change the instruction or
// object (D-JL9). It replaces the removed second-failure rule, which sent any second failed
// attempt to the design seat and the owner whatever it said.
//
// It only reads, and the caller runs it in a read transaction before its write
// (markExamined): on 2026-11-17 the removed rule's reading, done inside the examination's
// write transaction, held the store's one write lock for 4.8 seconds. Completing the
// examination and raising the conflict still share one write transaction
// (recordSecondFailure), so a restart cannot observe the examined repeat without its route.
func readRepeatedEnding(r *store.Reader, d Decision) (*secondFailure, error) {
	if d.RunRequest == nil || d.BlockConflict != nil {
		return nil, nil
	}
	current := d.RunRequest.RunID
	if repeatReading != nil {
		repeatReading()
	}
	history, err := readRunHistory(r)
	if err != nil {
		return nil, err
	}
	latest, ok := history.endings[current]
	if !ok || !returnedCauses[latest.Step] || latest.Evidence == "" {
		return nil, nil
	}
	works, err := readPending(r)
	if err != nil {
		return nil, err
	}
	if work := works[d.ItemID]; work != nil && work.Decision.RunRequest != nil && work.Decision.RunRequest.RunID == current && work.Orphaned {
		return nil, nil
	}
	chain, _, err := readItemChain(r, history, d.ItemID)
	if err != nil {
		return nil, err
	}
	previous, repeated := repeatedEnding(chain, chainEnding{RunID: current, Producer: producerOf(d), Cause: latest.Step, Key: latest.Evidence})
	if !repeated {
		return nil, nil
	}
	events, err := r.EventsOfKind(0, GateVerdictKind, RefusalKind)
	if err != nil {
		return nil, err
	}
	isRequest, err := isFiledRequest(r, d.ItemID)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("%s run %s ended (refused at %s) with the same evidence as run %s, so the worker is not sent it again. Change the instruction the worker builds from — amend the item's block — or object: a design run that commits nothing names its next actor.", IdenticalRepeatPrefix, current, latest.Step, previous)
	if isRequest {
		cmd, err := acceptedBuildRetryCommand(r, d.ItemID, current)
		if err != nil {
			return nil, err
		}
		text = fmt.Sprintf("%s run %s ended (refused at %s) with the same evidence as run %s, so the worker is not sent it again. A filed request has no document block; deliver a revised instruction in the next run's brief with %s (or close the item with tillr item close --item %s).", IdenticalRepeatPrefix, current, latest.Step, previous, cmd, d.ItemID)
	}
	for _, id := range []string{previous, current} {
		rec, err := terminalRecord(r, id)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, nil
		}
		report := "Report not recorded."
		for _, ref := range rec.ArtifactRefs {
			if ref.Kind != "report" {
				continue
			}
			raw, err := os.ReadFile(ref.Ref)
			if err != nil {
				report = fmt.Sprintf("Report unavailable (%s): %v", ref.Ref, err)
			} else {
				report = string(raw)
			}
			break
		}
		text += "\n\nRun " + id + " report:\n" + report
		for _, e := range events {
			if e.ObjectID == nil || *e.ObjectID != id {
				continue
			}
			if e.Kind == GateVerdictKind || e.Kind == RefusalKind {
				text += "\n" + e.Kind + ": " + string(e.Payload)
			}
		}
	}
	routing := &secondFailure{itemID: d.ItemID, outcomeRun: current, current: current, text: text}
	if work := works[d.ItemID]; work != nil && !work.Done && work.Decision.RunRequest != nil && work.Decision.RunRequest.RunID == current {
		routing.WorkerSession = latest.WorkerSession
	}
	return routing, nil
}

// readItemChain answers an item's recorded endings in run order, as the identical-repeat
// rule compares them, and the decisions that dispatched its runs.
func readItemChain(r *store.Reader, history runHistory, itemID string) ([]chainEnding, map[string]Decision, error) {
	priors, err := readPriorRuns(r)
	if err != nil {
		return nil, nil, err
	}
	decisions, err := readInputDecisions(r)
	if err != nil {
		return nil, nil, err
	}
	chain := []chainEnding{}
	for _, prior := range priors[itemID] {
		e, ok := history.endings[prior.RunID]
		if !ok {
			continue
		}
		chain = append(chain, chainEnding{RunID: prior.RunID, Producer: producerOf(decisions[prior.RunID]), Cause: e.Step, Key: e.Evidence})
	}
	return chain, decisions, nil
}

// recordSecondFailure records the routing readSecondFailure read, inside the examination's
// write transaction, after rereading the two inputs another writer may have changed since:
// a terminal item raises nothing, and an open conflict raised for another attempt (or
// already this second failure's) is left alone. Neither read decodes the history: one is
// the item's row, the other the conflict records alone.
func recordSecondFailure(tx *store.Tx, sf *secondFailure, at time.Time) error {
	if sf == nil {
		return nil
	}
	var state string
	if err := tx.QueryRow(`SELECT state FROM items WHERE item_id = ?`, sf.itemID).Scan(&state); err != nil {
		return err
	}
	if state == StateLanded || state == "closed" || state == "resolved-elsewhere" {
		return nil
	}
	conflicts, err := item.ReadConflicts(tx.Reader())
	if err != nil {
		return err
	}
	existing, hasConflict := conflicts[sf.itemID]
	repeatText := func(text string) bool {
		return strings.HasPrefix(text, IdenticalRepeatPrefix) || strings.HasPrefix(text, "Second failed attempt:")
	}
	// enrich is a conflict a blocked report of one of the two runs already routed: the
	// repeat's evidence is added to it. An open repeat conflict on this same run is this
	// routing, already recorded (a re-examination after a restart). Any other open conflict
	// names an earlier run the item was dispatched past — the owner put it back and
	// authorized a retry — so it no longer holds the item, and this repeat raises its own.
	// Skipping it instead left the item returned to its worker on every pass, forever.
	enrich := false
	if hasConflict && existing.Status != "closed" && existing.Status != "answered" {
		switch {
		case existing.RunID == sf.current && repeatText(existing.Text):
			return nil
		case (existing.RunID == sf.current || existing.RunID == sf.outcomeRun) && !repeatText(existing.Text):
			enrich = true
		}
	}
	text := sf.text
	var ref string
	if err := tx.QueryRow(`SELECT COALESCE(body_ref,'') FROM items WHERE item_id = ?`, sf.itemID).Scan(&ref); err != nil {
		return err
	}
	c := item.Conflict{ConflictID: "conflict-" + sf.outcomeRun, ItemID: sf.itemID, RunID: sf.current, BodyRef: ref, Text: strings.TrimSpace(text), Status: "raised", WaitingOn: "design", BlockedCriteria: []string{"not stated in reports"}}
	if enrich {
		// A blocked report has already routed a conflict. Enrich that
		// same conflict without changing its seat or losing its criteria.
		c = existing
		c.RunID = sf.current
		c.Text = strings.TrimSpace(text) + "\n\nBlocked finding:\n" + existing.Text
	}
	// Match the blocked-report route: without a source block the design seat
	// cannot amend the item. Keep the evidence in an explicit owner hold.
	if ref == "" {
		c.Status, c.WaitingOn = "owner", "owner"
		isRequest, err := isFiledRequest(tx.Reader(), sf.itemID)
		if err != nil {
			return err
		}
		if isRequest {
			if !strings.Contains(c.Text, "tillr drain retry") {
				cmd, err := acceptedBuildRetryCommand(tx.Reader(), sf.itemID, sf.current)
				if err != nil {
					return err
				}
				c.Text += "\n\nThis item is a filed request with no document block; clear the hold and deliver a revised instruction in the next run's brief with: " + cmd
			}
		} else {
			c.Text += "\n\nNo item block reference is recorded; the owner must supply one before a design amendment."
		}
	} else if sf.noDesignSeat {
		// A design conflict with no design seat would be a policy refusal that stops the loop
		// for every item. The loop never stops on one item's condition: the repeat waits on
		// the owner, who can name a design seat in the policy or answer it.
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text += "\n\nThe backend policy names no design seat, so no seat can diagnose this repeat, and it waits on the owner, the one who can rule on it or name a design seat in the policy."
	}
	return item.RecordConflict(tx, c, at)
}
