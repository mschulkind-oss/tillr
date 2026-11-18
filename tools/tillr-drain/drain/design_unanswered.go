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
// it awaiting that design run. The design run's settlement answers it: a commit that
// passes the gate and lands answers it (whatever the commit changed), and an objection —
// a report that ends `RESULT: blocked`, or one that names the next actor — routes it to
// the actor the report names (designNextActor). The outcomes that end the design run
// before either leave the conflict awaiting a run that has ended, and every view said
// "waits on work: design" with nobody working (2026-11-17: five of seven design
// conflicts). That is a hold whose mover cannot act (docs/design/landing-principles.md,
// section 13).
//
// The owner's ruling of 2026-11-17 is the rule, applied at each pass to the records as
// they stand. Nothing here is counted and nothing is compared with what changed since the
// dispatch: an item reaches the owner only by an objection naming the owner.
//
//   - A gate refusal of the design run's commit is no attempt. The refusal goes back to
//     the design seat to fix, as many times as it takes. (The ruling sends it to the same
//     designer session; until that is built the design seat is dispatched again with the
//     refusal and the pinned commit in the conflict's text.)
//   - An interrupted or killed design run (a loop stop, a lease reclaimed after its loop
//     died, a signal) is no attempt: the design seat is dispatched again.
//   - A design run that committed nothing and named no next actor neither answered nor
//     objected: the design seat is dispatched again, told the contract.
//   - Any other refusal (the fence, the lander) goes back to the design seat the same way.
//   - An owner hold the removed count wrote (a drain.design-unanswered record naming the
//     design run the hold names) is raised again for the design seat once.
//
// A change to the item's block, its design document or the trunk raises nothing: the owner
// ruled that no such change is a reason to spend a design run. The owner's explicit
// `tillr drain retry --run <design run>` still raises a conflict the owner holds.
//
// A triage conflict keeps its own rule (each request version gets at most one triage
// run); only its interruption is settled here.

const designUnansweredKind = "drain.design-unanswered"

func init() {
	if err := event.Register(designUnansweredKind, Utility); err != nil {
		panic(err)
	}
}

// designUnanswered is drain.design-unanswered's payload: one design run that ended
// without answering its conflict, how it ended, and whether the loop raised the conflict
// again for it. Records written before the ruling of 2026-11-17 also carry the fields of
// the removed input-change and trunk-suspect rules; they are read only to recognize the
// owner holds that rule wrote.
type designUnanswered struct {
	ConflictID string   `json:"conflict_id"`
	ItemID     string   `json:"item_id"`
	RunID      string   `json:"run_id"`
	Outcome    string   `json:"outcome"`
	Changes    []string `json:"changes"`
	Reraised   bool     `json:"reraised"`
	// BaseSHA is the design run's base.
	BaseSHA string `json:"base_sha,omitempty"`
	// FailingChecks are the fast-gate checks that refused the run's candidate, when the
	// gate refused it.
	FailingChecks []string `json:"failing_checks,omitempty"`
	// TrunkSuspect and TrunkReraised are the removed trunk-suspect rule's records.
	TrunkSuspect  bool `json:"trunk_suspect,omitempty"`
	TrunkReraised bool `json:"trunk_reraised,omitempty"`
	// Misread records a committed design answer the settlement read, before 2026-11-17,
	// as no amendment or a removed block (settleMisreadDesignAnswers).
	Misread bool `json:"misread,omitempty"`
	// Uncounted records an owner hold the removed count wrote, raised again for the
	// design seat by the ruling of 2026-11-17.
	Uncounted bool `json:"uncounted,omitempty"`
}

type designFinding struct {
	conflict    item.Conflict
	runID       string
	interrupted bool
	// uncounted is an owner hold the removed count wrote.
	uncounted bool
	outcome   string
	// base and checks are the run's base and the fast-gate checks that refused it; head is
	// the candidate commit the run left, when it left one.
	base, head string
	checks     []string
}

// settleUnansweredDesignRuns applies the rule above to every blocked item whose conflict
// is awaiting an ended design run, or is with the owner by the removed count.
func (l *Loop) settleUnansweredDesignRuns(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	found := []designFinding{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		works, _, err := readPendingHistory(r)
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
				f := designFinding{conflict: c, runID: c.DesignRunID, base: w.BaseSHA}
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
				f.checks = l.designFailingChecks(c.DesignRunID, f.outcome)
				if pin, err := l.git(ctx, l.cfg.Root, "rev-parse", "--verify", "--quiet", "refs/tillr/runs/"+c.DesignRunID+"^{commit}"); err == nil {
					if pin = strings.TrimSpace(pin); pin != "" && pin != f.base {
						f.head = pin
					}
				}
				found = append(found, f)
			case c.Status == "owner" && c.TriageVersion == "":
				m := marks[c.ConflictID]
				if len(m) == 0 {
					continue
				}
				last := m[len(m)-1]
				if last.RunID != c.DesignRunID || last.Misread || last.Uncounted {
					continue
				}
				found = append(found, designFinding{conflict: c, runID: c.DesignRunID, uncounted: true, outcome: last.Outcome})
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
			mark := designUnanswered{ConflictID: c.ConflictID, ItemID: c.ItemID, RunID: f.runID, Outcome: f.outcome, Changes: []string{}, Reraised: true, BaseSHA: f.base, FailingChecks: f.checks, Uncounted: f.uncounted}
			c.Text += "\n\n" + designReraiseText(f)
			c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
			l.reportf("loop: %s conflict %s is raised again for the design seat: design run %s %s\n", c.ItemID, c.ConflictID, f.runID, designEnding(f))
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

// designEnding names how a design run ended, for the loop's report line.
func designEnding(f designFinding) string {
	switch {
	case f.interrupted:
		return "was interrupted"
	case f.uncounted:
		return "left an owner hold the removed count wrote"
	}
	return "ended without answering (" + f.outcome + ")"
}

// designReraiseText is the paragraph the conflict gains when it is raised again: what
// ended the design run, and what the next design run has to do about it.
func designReraiseText(f designFinding) string {
	pinned := ""
	if f.head != "" {
		pinned = " Its commit " + shortSHA(f.head) + " is pinned at refs/tillr/runs/" + f.runID + "."
	}
	switch {
	case f.interrupted:
		return "Design run " + f.runID + " was interrupted by a loop stop before it answered. An interrupted run is no attempt, so the design seat is dispatched again."
	case f.uncounted:
		return "Design run " + f.runID + " ended without answering (" + f.outcome + "), and a count of such runs sent this conflict to the owner. The owner ruled on 2026-11-17 that a design run's outcome is never counted toward the owner, so the conflict is raised again for the design seat."
	case f.outcome == "refused at "+RefusalGate:
		checks := ""
		if len(f.checks) > 0 {
			checks = " (failing checks: " + strings.Join(f.checks, ", ") + ")"
		}
		return "Design run " + f.runID + " was refused at the gate" + checks + "." + pinned + " A gate refusal is no attempt: fix what the gate refused and commit the answer again."
	case f.outcome == "refused at "+RefusalNoOp:
		return "Design run " + f.runID + " committed nothing and named no next actor. A design run either commits its answer, or objects: its report then carries a line `" + DesignNextActorField + ": owner` (a ruling only the owner can make) or `" + DesignNextActorField + ": build` (the block stands as written and the worker builds it)."
	case strings.HasPrefix(f.outcome, "refused at "):
		return "Design run " + f.runID + " was " + f.outcome + "." + pinned + " A refusal is no attempt: answer the conflict again within the refusal."
	}
	return "Design run " + f.runID + " ended without answering this conflict (" + f.outcome + ")." + pinned + " A killed or failed run is no attempt, so the design seat is dispatched again."
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

// designFailingChecks names the fast-gate checks that refused a design run's candidate,
// read from the run's fast verdict, or nil when the gate did not refuse it.
func (l *Loop) designFailingChecks(runID, outcome string) []string {
	if outcome != "refused at "+RefusalGate {
		return nil
	}
	spans, state := readFastGateSpans(filepath.Join(RunDir(l.cfg.Root, runID), GateFastVerdictFile))
	if state != GateSpansRead {
		return nil
	}
	checks := []string{}
	for _, span := range spans {
		if span.Status == "fail" {
			checks = append(checks, span.CheckID)
		}
	}
	sort.Strings(checks)
	return checks
}
