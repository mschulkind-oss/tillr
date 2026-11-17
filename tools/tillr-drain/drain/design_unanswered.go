package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A raised conflict gets one design-seat dispatch (conflictDecision), and the claim marks
// it awaiting that design run. The design run's settlement answers it in most outcomes:
// an amended block that lands answers it, and a report, a removed block or an unchanged
// block hands it to the owner. The outcomes that end the design run before any of those —
// a candidate refused at the gate, a run with no commit, a refused landing, a run a loop
// stop interrupted — left the conflict awaiting a run that had ended, and every view said
// "waits on work: design" with nobody working (2026-11-17: five of seven design
// conflicts). That is a hold whose mover cannot act
// (docs/design/landing-principles.md, section 13).
//
// The rule, applied at each pass to the records as they stand:
//
//   - An interrupted design run (a loop stop orphaned it) is no attempt, like any
//     interrupted run: the conflict is raised again and the design seat dispatched.
//   - A design run that ended any other way without answering is an attempt. When an
//     input it read — the item's block or its design document — differs on trunk from the
//     run's base, or a trunk fixer landed after its dispatch, the conflict is raised once
//     more. That is the readmission rule's one automatic attempt per input
//     change (README, "Automatic second attempts after an input changes"). Otherwise the
//     conflict goes to the owner, naming the design run and its outcome.
//   - An owner hold this rule wrote is read again when the trunk moves, so an input change
//     that lands later still earns the one automatic attempt, if it was not spent.
//   - A second unanswered design run after an automatic re-raise goes to the owner,
//     whatever changed: no unbounded retries, and nothing is timed.
//
// A triage conflict keeps its own rule (each request version gets at most one triage run).

const designUnansweredKind = "drain.design-unanswered"

func init() {
	if err := event.Register(designUnansweredKind, Utility); err != nil {
		panic(err)
	}
}

// designUnanswered is drain.design-unanswered's payload: one design run that ended
// without answering its conflict, what changed since its dispatch, and whether the loop
// raised the conflict again for it. A reraised record spends the conflict's one
// automatic re-raise.
type designUnanswered struct {
	ConflictID string   `json:"conflict_id"`
	ItemID     string   `json:"item_id"`
	RunID      string   `json:"run_id"`
	Outcome    string   `json:"outcome"`
	Changes    []string `json:"changes"`
	Reraised   bool     `json:"reraised"`
}

type designFinding struct {
	conflict    item.Conflict
	runID       string
	interrupted bool
	outcome     string
	changes     []string
	spent       bool
}

// settleUnansweredDesignRuns applies the rule above to every blocked item whose conflict
// is awaiting an ended design run, or is with the owner by this rule.
func (l *Loop) settleUnansweredDesignRuns(ctx context.Context) error {
	if l.designUnansweredChecked == nil {
		l.designUnansweredChecked = map[string]string{}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	trunk, err := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", TrunkRef)
	if err != nil {
		return err
	}
	found := []designFinding{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		works, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		items := map[string]itemRow{}
		for _, row := range rows {
			items[row.ItemID] = row
		}
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		marks, err := readDesignUnanswered(r)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(conflicts))
		for id := range conflicts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			c := conflicts[id]
			row, ok := items[id]
			if _, faulted := faults[id]; !ok || faulted || row.State != StateBlocked || IsTrunkFixer(id) || c.DesignRunID == "" {
				continue
			}
			spent := false
			for _, m := range marks[c.ConflictID] {
				spent = spent || m.Reraised
			}
			switch {
			case c.Status == "awaiting":
				w := works[id]
				if w == nil || !w.Done || w.Decision.RunRequest == nil || w.Decision.RunRequest.RunID != c.DesignRunID || w.Decision.BlockConflict == nil || w.Decision.BlockConflict.ConflictID != c.ConflictID {
					continue
				}
				rec, err := terminalRecord(r, c.DesignRunID)
				if err != nil {
					return err
				}
				if rec == nil {
					// Claimed but never started: releaseUnlaunchedClaims' record.
					continue
				}
				f := designFinding{conflict: c, runID: c.DesignRunID, spent: spent}
				if w.Orphaned {
					f.interrupted = true
					found = append(found, f)
					continue
				}
				if c.TriageVersion != "" {
					continue
				}
				if f.outcome, err = designOutcome(r, c.DesignRunID, rec.State, rec.EndCause); err != nil {
					return err
				}
				if f.changes, err = l.designInputChanges(ctx, r, row, c.DesignRunID, w.BaseSHA); err != nil {
					return err
				}
				found = append(found, f)
			case c.Status == "owner" && c.TriageVersion == "":
				m := marks[c.ConflictID]
				if len(m) == 0 || m[len(m)-1].RunID != c.DesignRunID || m[len(m)-1].Reraised || spent || l.designUnansweredChecked[c.ConflictID] == trunk {
					continue
				}
				w := byRun[c.DesignRunID]
				if w == nil {
					continue
				}
				changes, err := l.designInputChanges(ctx, r, row, c.DesignRunID, w.BaseSHA)
				if err != nil {
					return err
				}
				if len(changes) == 0 {
					l.designUnansweredChecked[c.ConflictID] = trunk
					continue
				}
				found = append(found, designFinding{conflict: c, runID: c.DesignRunID, outcome: m[len(m)-1].Outcome, changes: changes})
			}
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return err
	}
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		current, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		for _, f := range found {
			// A newer disposition, by the owner or by the loop, wins over this read.
			c := current[f.conflict.ItemID]
			if c.ConflictID != f.conflict.ConflictID || c.Status != f.conflict.Status || c.DesignRunID != f.runID {
				continue
			}
			if err := refuseTerminalItem(tx.Reader(), c.ItemID, "design-unanswered"); err != nil {
				continue
			}
			mark := designUnanswered{ConflictID: c.ConflictID, ItemID: c.ItemID, RunID: f.runID, Outcome: f.outcome, Changes: f.changes}
			switch {
			case f.interrupted:
				c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
				c.Text += "\n\nDesign run " + f.runID + " was interrupted by a loop stop before it answered. An interrupted run is no attempt, so the design seat is dispatched again."
				l.reportf("loop: %s design run %s was interrupted; conflict %s is raised again\n", c.ItemID, f.runID, c.ConflictID)
			case len(f.changes) > 0 && !f.spent:
				mark.Reraised = true
				if c.Status == "awaiting" {
					c.Text += "\n\nDesign run " + f.runID + " ended without answering this conflict (" + f.outcome + ")."
				}
				c.Text += "\n\nChanged since design run " + f.runID + " was dispatched: " + strings.Join(f.changes, ", ") + ". The loop raises the conflict once more for the design seat; if that design run also ends without answering, the owner decides."
				c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
				l.reportf("loop: %s conflict %s is raised again for the design seat: %s changed since design run %s\n", c.ItemID, c.ConflictID, strings.Join(f.changes, ", "), f.runID)
			default:
				c.Text += "\n\nDesign run " + f.runID + " ended without answering this conflict (" + f.outcome + "). "
				if f.spent {
					c.Text += "The automatic second design run was already spent, so the owner decides: amend the block and release the item, or close it."
				} else {
					c.Text += "Nothing it read has changed since it was dispatched, so the owner decides: amend the item block or its design document (the loop then raises the conflict once more), or release or close the item."
				}
				c.Status, c.WaitingOn = "owner", "owner"
				l.reportf("loop: %s conflict %s waits on the owner: design run %s ended without answering it (%s)\n", c.ItemID, c.ConflictID, f.runID, f.outcome)
			}
			if err := item.RecordConflict(tx, c, at); err != nil {
				return err
			}
			if !f.interrupted {
				if err := appendEffect(tx, designUnansweredKind, f.runID, mark, at); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// readDesignUnanswered answers every drain.design-unanswered record by conflict, oldest
// first.
func readDesignUnanswered(r *store.Reader) (map[string][]designUnanswered, error) {
	rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, designUnansweredKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]designUnanswered{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m designUnanswered
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		out[m.ConflictID] = append(out[m.ConflictID], m)
	}
	return out, rows.Err()
}

// designOutcome names how the design run ended: the step that refused its candidate,
// when one did, otherwise its terminal state.
func designOutcome(r *store.Reader, runID, state, cause string) (string, error) {
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, RefusalKind, runID).Scan(&raw)
	if err == nil {
		var p refusalPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		if p.Step != "" {
			return "refused at " + p.Step, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if cause != "" {
		return "ended " + state + ", " + cause, nil
	}
	return "ended " + state, nil
}

// designInputChanges answers what the design run read that has changed since its
// dispatch: the item's block (its definition in the body document) and its design
// document, compared between the run's base and the trunk tip, and any trunk fixer that
// landed after the run was dispatched. Queue rank, timestamps, and trunk movement that
// leaves those alone are not changes.
func (l *Loop) designInputChanges(ctx context.Context, r *store.Reader, row itemRow, runID, base string) ([]string, error) {
	changes := []string{}
	if base == "" {
		return changes, nil
	}
	var ref string
	if err := r.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id = ?`, row.ItemID).Scan(&ref); err != nil {
		return nil, err
	}
	cache := blockSource{}
	if ref != "" {
		before, okBefore, err := blockBodyAt(ctx, l.cfg.Root, base, ref, row.ItemID, cache)
		if err != nil {
			return nil, err
		}
		after, okAfter, err := blockBodyAt(ctx, l.cfg.Root, TrunkRef, ref, row.ItemID, cache)
		if err != nil {
			return nil, err
		}
		if okBefore && okAfter && before != after {
			changes = append(changes, "item block "+ref)
		}
	}
	if doc := row.DesignDoc; doc != "" {
		if filepath.IsAbs(doc) {
			if rel, err := filepath.Rel(l.cfg.Root, doc); err == nil {
				doc = rel
			}
		}
		doc = filepath.ToSlash(filepath.Clean(doc))
		file, _, _ := strings.Cut(ref, "#")
		if !filepath.IsAbs(doc) && doc != ".." && !strings.HasPrefix(doc, "../") && doc != filepath.ToSlash(filepath.Clean(file)) {
			before, err := l.git(ctx, l.cfg.Root, "ls-tree", base, "--", doc)
			if err != nil {
				return nil, err
			}
			after, err := l.git(ctx, l.cfg.Root, "ls-tree", TrunkRef, "--", doc)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(before) != strings.TrimSpace(after) {
				changes = append(changes, "design document "+doc)
			}
		}
	}
	var dispatched int64
	if err := r.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events WHERE kind = ? AND object_id = ?`, dispatchStartedKind, runID).Scan(&dispatched); err != nil {
		return nil, err
	}
	rows, err := r.Query(`SELECT COALESCE(json_extract(payload, '$.item_id'), '') FROM events WHERE kind = 'land.landing' AND seq > ? AND json_extract(payload, '$.result') = 'landed' ORDER BY seq`, dispatched)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if IsTrunkFixer(id) {
			changes = append(changes, "trunk fix "+id)
			break
		}
	}
	return changes, rows.Err()
}
