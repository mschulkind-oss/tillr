package drain

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// secondFailureReading is a test seam: it runs where the routing starts reading the
// store's history, so a test can see which lock that reading holds.
var secondFailureReading func()

// secondFailure is a spent second attempt's routing to the design seat, as read from the
// store before the write that records it: the item, the attempt the conflict is named
// for, the run it is raised on, and the evidence text.
type secondFailure struct {
	itemID, outcomeRun, current, text string
}

// readSecondFailure answers the routing an examination of d raises, or nil when it raises
// none. It only reads, and the caller runs it in a read transaction before its write
// (markExamined): the routing reads the whole history — every event, several times over,
// every run of every item, and the runs' reports on disk — and on 2026-11-17 that reading,
// done inside the examination's write transaction, held the store's one write lock for
// 4.8 seconds, so every other writer queued behind it and one waited past its busy
// timeout. Completing the examination and raising the conflict still share one write
// transaction (recordSecondFailure), so a restart cannot observe a spent second attempt
// without its design-seat disposition.
//
// The item's state and its open conflict are not read here: they are the inputs another
// writer can change between this read and the write, so the write reads them, and they
// are small reads. Nothing else read here is changed by the examination being
// recorded: its own examination record is consulted only for the attempts before it.
func readSecondFailure(r *store.Reader, d Decision) (*secondFailure, error) {
	if d.RunRequest == nil {
		return nil, nil
	}
	outcomeRun := d.RunRequest.RunID
	// A squash repair finishes the originating attempt, not a new allowance.
	// The existing durable handoff retains the parent across loop replacement.
	if d.Reason == ReasonConflictFix {
		works, err := readPending(r)
		if err != nil {
			return nil, err
		}
		if work := works[d.ItemID]; work != nil && work.ParentRunID != "" {
			decisions, err := readInputDecisions(r)
			if err != nil {
				return nil, err
			}
			d = decisions[work.ParentRunID]
		}
	}
	if d.RunRequest == nil || d.BlockConflict != nil || (!d.Readmitted && d.Action != ActionRetry) {
		return nil, nil
	}
	if secondFailureReading != nil {
		secondFailureReading()
	}
	attemptHistory, err := readRunHistory(r)
	if err != nil {
		return nil, err
	}
	if attemptHistory.refusals[outcomeRun] == RefusalTrunkAlreadyRed || attemptHistory.judgeHolds[outcomeRun] == ReasonJudgeUnreadable || attemptHistory.judgeHolds[outcomeRun] == ReasonJudgeBlocked {
		// A checked blocker already names the next mover; it is not a failed
		// attempt to reroute to design.
		return nil, nil
	}
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	orphaned, examined := map[string]bool{}, map[string]bool{}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		if e.Kind == loopOrphanedKind {
			orphaned[*e.ObjectID] = true
		}
		if e.Kind == examinationDoneKind {
			examined[*e.ObjectID] = true
		}
	}
	// Historical markers still describe standalone runs. For loop attempts,
	// use the reader's effective state: a matching continuation supersedes the
	// old orphan/examination, while judged segments and replay cannot reopen it.
	_, history, err := readPendingHistory(r)
	if err != nil {
		return nil, err
	}
	for id, work := range history {
		orphaned[id], examined[id] = work.Orphaned, work.Done
	}
	current := d.RunRequest.RunID
	if orphaned[current] || orphaned[outcomeRun] {
		return nil, nil
	}
	priors, err := readPriorRuns(r)
	if err != nil {
		return nil, err
	}
	decisions, err := readInputDecisions(r)
	if err != nil {
		return nil, err
	}
	previous := ""
	for _, prior := range priors[d.ItemID] {
		if prior.RunID == current {
			break
		}
		if d.SupersedesRunID != "" && prior.RunID != d.SupersedesRunID {
			continue
		}
		pd := decisions[prior.RunID]
		if attemptHistory.refusals[prior.RunID] == RefusalTrunkAlreadyRed || attemptHistory.judgeHolds[prior.RunID] == ReasonJudgeUnreadable || attemptHistory.judgeHolds[prior.RunID] == ReasonJudgeBlocked || pd.BlockConflict != nil || pd.Seat == "design" {
			continue
		}
		// An authorized predecessor can be a standalone terminal run. The
		// operator's explicit link supplies the basis; no loop examination is
		// required. Automatic re-admission still needs an examined predecessor.
		if orphaned[prior.RunID] || (!examined[prior.RunID] && d.SupersedesRunID == "") {
			previous = ""
			continue
		}
		previous = prior.RunID
	}
	if previous == "" {
		return nil, nil
	}
	text := fmt.Sprintf("Second failed attempt: %s and %s require the design seat.", previous, current)
	reportRuns := []string{previous, current}
	if outcomeRun != current {
		reportRuns = append(reportRuns, outcomeRun)
	}
	for _, id := range reportRuns {
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
		found := false
		for _, e := range events {
			if e.ObjectID == nil || *e.ObjectID != id {
				continue
			}
			if e.Kind == GateVerdictKind || e.Kind == RefusalKind {
				text += "\n" + e.Kind + ": " + string(e.Payload)
				found = true
			}
		}
		if !found {
			text += "\nGate: no verdict or refusal recorded."
		}
	}
	return &secondFailure{itemID: d.ItemID, outcomeRun: outcomeRun, current: current, text: text}, nil
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
	if hasConflict && existing.Status != "closed" && existing.Status != "answered" {
		if (existing.RunID != sf.current && existing.RunID != sf.outcomeRun) || strings.HasPrefix(existing.Text, "Second failed attempt:") {
			return nil
		}
	}
	text := sf.text
	var ref string
	if err := tx.QueryRow(`SELECT COALESCE(body_ref,'') FROM items WHERE item_id = ?`, sf.itemID).Scan(&ref); err != nil {
		return err
	}
	c := item.Conflict{ConflictID: "conflict-" + sf.outcomeRun, ItemID: sf.itemID, RunID: sf.current, BodyRef: ref, Text: strings.TrimSpace(text), Status: "raised", WaitingOn: "design", BlockedCriteria: []string{"not stated in reports"}}
	if hasConflict && existing.Status != "closed" && existing.Status != "answered" {
		// A blocked second report has already routed a conflict. Enrich that
		// same conflict without changing its seat or losing its criteria.
		c = existing
		c.RunID = sf.current
		c.Text = strings.TrimSpace(text) + "\n\nBlocked finding:\n" + existing.Text
	}
	// Match the blocked-report route: without a source block the design seat
	// cannot amend the item. Keep the evidence in an explicit owner hold.
	if ref == "" {
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text += "\n\nNo item block reference is recorded; the owner must supply one before a design amendment."
	}
	return item.RecordConflict(tx, c, at)
}
