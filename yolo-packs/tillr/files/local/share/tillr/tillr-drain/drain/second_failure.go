package drain

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// Completing examination and raising the conflict share a transaction. A restart
// cannot observe a spent second attempt without its design-seat disposition.
func routeSecondFailure(tx *store.Tx, d Decision, at time.Time) error {
	if d.RunRequest == nil {
		return nil
	}
	outcomeRun := d.RunRequest.RunID
	// A squash repair finishes the originating attempt, not a new allowance.
	// The existing durable handoff retains the parent across loop replacement.
	if d.Reason == ReasonConflictFix {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		if work := works[d.ItemID]; work != nil && work.ParentRunID != "" {
			decisions, err := readInputDecisions(tx.Reader())
			if err != nil {
				return err
			}
			d = decisions[work.ParentRunID]
		}
	}
	if d.RunRequest == nil || d.BlockConflict != nil || (!d.Readmitted && d.Action != ActionRetry) {
		return nil
	}
	var state string
	if err := tx.QueryRow(`SELECT state FROM items WHERE item_id = ?`, d.ItemID).Scan(&state); err != nil {
		return err
	}
	if state == StateLanded || state == "closed" || state == "resolved-elsewhere" {
		return nil
	}
	conflicts, err := item.ReadConflicts(tx.Reader())
	if err != nil {
		return err
	}
	existing, hasConflict := conflicts[d.ItemID]
	if hasConflict && existing.Status != "closed" && existing.Status != "answered" {
		if (existing.RunID != d.RunRequest.RunID && existing.RunID != outcomeRun) || strings.HasPrefix(existing.Text, "Second failed attempt:") {
			return nil
		}
	}
	events, err := tx.Reader().Events(0)
	if err != nil {
		return err
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
	_, history, err := readPendingHistory(tx.Reader())
	if err != nil {
		return err
	}
	for id, work := range history {
		orphaned[id], examined[id] = work.Orphaned, work.Done
	}
	current := d.RunRequest.RunID
	if orphaned[current] || orphaned[outcomeRun] {
		return nil
	}
	priors, err := readPriorRuns(tx.Reader())
	if err != nil {
		return err
	}
	decisions, err := readInputDecisions(tx.Reader())
	if err != nil {
		return err
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
		if pd.BlockConflict != nil || pd.Seat == "design" {
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
		return nil
	}
	text := fmt.Sprintf("Second failed attempt: %s and %s require the design seat.", previous, current)
	reportRuns := []string{previous, current}
	if outcomeRun != current {
		reportRuns = append(reportRuns, outcomeRun)
	}
	for _, id := range reportRuns {
		rec, err := terminalRecord(tx.Reader(), id)
		if err != nil {
			return err
		}
		if rec == nil {
			return nil
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
	var ref string
	if err := tx.QueryRow(`SELECT COALESCE(body_ref,'') FROM items WHERE item_id = ?`, d.ItemID).Scan(&ref); err != nil {
		return err
	}
	c := item.Conflict{ConflictID: "conflict-" + outcomeRun, ItemID: d.ItemID, RunID: current, BodyRef: ref, Text: strings.TrimSpace(text), Status: "raised", WaitingOn: "design", BlockedCriteria: []string{"not stated in reports"}}
	if hasConflict && existing.Status != "closed" && existing.Status != "answered" {
		// A blocked second report has already routed a conflict. Enrich that
		// same conflict without changing its seat or losing its criteria.
		c = existing
		c.RunID = current
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
