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
	"github.com/mschulkind-oss/tillr-run/run"
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
//   - A gate refusal or a trunk-broken report is a trunk suspect and no attempt yet: it
//     goes to the owner, and is raised again (spending nothing) once a trunk commit that
//     changes a file the run's candidate touched is in the heavy proof's green commit
//     (trunkChangeSince). A second identical refusal after that is an attempt.
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
	// BaseSHA is the design run's base. A record written before the trunk-suspect rule
	// names none.
	BaseSHA string `json:"base_sha,omitempty"`
	// FailingChecks are the fast-gate checks that refused the run's candidate, when the
	// gate refused it.
	FailingChecks []string `json:"failing_checks,omitempty"`
	// TrunkSuspect records a refusal on a base the heavy proof had not proven green: the
	// conflict waits for the proof to pass BaseSHA, and the refusal is no attempt.
	TrunkSuspect bool `json:"trunk_suspect,omitempty"`
	// TrunkReraised records the conflict raised again because the proof passed the base a
	// trunk-suspect refusal blamed. It does not spend the input-change allowance.
	TrunkReraised bool `json:"trunk_reraised,omitempty"`
	// Misread records a committed design answer the settlement read, before 2026-11-17,
	// as no amendment or a removed block (settleMisreadDesignAnswers). It spends nothing.
	Misread bool `json:"misread,omitempty"`
}

type designFinding struct {
	conflict    item.Conflict
	runID       string
	interrupted bool
	outcome     string
	changes     []string
	spent       bool
	// base and checks are the run's base and the fast-gate checks that refused it.
	base   string
	checks []string
	// suspect is a refusal the trunk may have caused; trunkRaise is a suspect whose blamed
	// trunk has since changed, by trunkFix, a commit the heavy proof has proven.
	suspect, trunkRaise bool
	trunkFix            string
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
	green, err := heavyGreen(ctx, l.cfg.Root)
	if err != nil {
		return err
	}
	green = strings.TrimSpace(green)
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
				f.base = w.BaseSHA
				f.checks = l.designFailingChecks(c.DesignRunID, f.outcome)
				if f.base != "" && designBlamesTrunk(rec, f.outcome) && !repeatsTrunkReraise(marks[c.ConflictID], f.outcome, f.checks) {
					f.suspect = true
					if f.trunkFix, err = l.trunkChangeSince(ctx, c.DesignRunID, f.base, green); err != nil {
						return err
					}
					f.trunkRaise = f.trunkFix != ""
				}
				found = append(found, f)
			case c.Status == "owner" && c.TriageVersion == "":
				m := marks[c.ConflictID]
				if f, ok, err := l.trunkProvedPast(ctx, r, c, m, byRun, green); err != nil {
					return err
				} else if ok {
					found = append(found, f)
					continue
				}
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
			mark := designUnanswered{ConflictID: c.ConflictID, ItemID: c.ItemID, RunID: f.runID, Outcome: f.outcome, Changes: f.changes, BaseSHA: f.base, FailingChecks: f.checks}
			switch {
			case f.interrupted:
				c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
				c.Text += "\n\nDesign run " + f.runID + " was interrupted by a loop stop before it answered. An interrupted run is no attempt, so the design seat is dispatched again."
				l.reportf("loop: %s design run %s was interrupted; conflict %s is raised again\n", c.ItemID, f.runID, c.ConflictID)
			case f.trunkRaise:
				mark.TrunkReraised = true
				if c.Status == "awaiting" {
					c.Text += "\n\nDesign run " + f.runID + " ended without answering this conflict (" + f.outcome + ") on base " + shortSHA(f.base) + "."
				}
				c.Text += "\n\nTrunk commit " + shortSHA(f.trunkFix) + ", which the heavy proof has proven green, changed what design run " + f.runID + " was refused on since its base " + shortSHA(f.base) + ". A refusal the trunk may have caused is no attempt, so the loop raises the conflict again for the design seat."
				c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
				l.reportf("loop: %s conflict %s is raised again for the design seat: trunk commit %s changed what design run %s was refused on\n", c.ItemID, c.ConflictID, shortSHA(f.trunkFix), f.runID)
			case len(f.changes) > 0 && !f.spent:
				mark.Reraised = true
				if c.Status == "awaiting" {
					c.Text += "\n\nDesign run " + f.runID + " ended without answering this conflict (" + f.outcome + ")."
				}
				c.Text += "\n\nChanged since design run " + f.runID + " was dispatched: " + strings.Join(f.changes, ", ") + ". The loop raises the conflict once more for the design seat; if that design run also ends without answering, the owner decides."
				c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
				l.reportf("loop: %s conflict %s is raised again for the design seat: %s changed since design run %s\n", c.ItemID, c.ConflictID, strings.Join(f.changes, ", "), f.runID)
			case f.suspect:
				mark.TrunkSuspect = true
				c.Text += "\n\nDesign run " + f.runID + " ended without answering this conflict (" + f.outcome + ") on base " + shortSHA(f.base) + ". The trunk may have caused it, so it is no attempt yet: the loop raises the conflict again once a trunk commit that changes the files the run touched (any trunk commit, when it touched none) is proven green by the heavy proof. Until then the owner decides: amend the block, or release or close the item."
				c.Status, c.WaitingOn = "owner", "owner"
				l.reportf("loop: %s conflict %s waits for a proven trunk change: design run %s was refused on %s (%s)\n", c.ItemID, c.ConflictID, f.runID, shortSHA(f.base), f.outcome)
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

// designBlamesTrunk answers whether a design run's unanswered end can be the trunk's: its
// candidate was refused at the gate, or its report answered trunk-broken. Whether it is
// the trunk's then depends on the heavy proof at the run's base (provenAt).
//
// The gate's own trunk-already-red replay cannot judge this: the fast-gate check ids
// (documents, citations, and the rest) are not ids the replay program plans, so a replay
// of them answers unknown, and unknown never acquits (2026-11-17: `fork-check --replay
// --only documents` answered "no planned check matches --only").
func designBlamesTrunk(rec *run.Record, outcome string) bool {
	if outcome == "refused at "+RefusalGate {
		return true
	}
	return rec != nil && reportFooter(logTail(rec.ArtifactRefs)) == ReasonTrunkBroken
}

// repeatsTrunkReraise answers whether this conflict was already raised again for the
// trunk after a refusal with the same outcome and failing checks. A second identical
// refusal after the trunk changed and was proven is the candidate's: an attempt.
func repeatsTrunkReraise(marks []designUnanswered, outcome string, checks []string) bool {
	suspected := map[string]designUnanswered{}
	for _, m := range marks {
		if !m.TrunkReraised {
			suspected[m.RunID] = m
			continue
		}
		before, ok := suspected[m.RunID]
		if !ok {
			before = m
		}
		if before.Outcome == outcome && strings.Join(before.FailingChecks, ",") == strings.Join(checks, ",") {
			return true
		}
	}
	return false
}

// trunkChangeSince answers the oldest trunk commit since a refused design run's base that
// changed the files the run's candidate touched (any commit, when it touched none) and
// that the heavy proof's green commit contains, or "" when there is none. A hand commit
// counts like a recorded fixer: the evidence is the proven trunk, not who changed it.
func (l *Loop) trunkChangeSince(ctx context.Context, runID, base, green string) (string, error) {
	if green == "" || base == "" || green == base {
		return "", nil
	}
	if ok, err := containsCommit(ctx, l.cfg.Root, base, green); err != nil || !ok {
		return "", err
	}
	args := []string{"rev-list", "--reverse", base + ".." + green}
	if pin, err := l.git(ctx, l.cfg.Root, "rev-parse", "--verify", "--quiet", "refs/tillr/runs/"+runID+"^{commit}"); err == nil {
		if pin = strings.TrimSpace(pin); pin != "" && pin != base {
			names, err := l.git(ctx, l.cfg.Root, "diff", "--name-only", base, pin)
			if err != nil {
				return "", err
			}
			if paths := strings.Fields(names); len(paths) > 0 {
				args = append(append(args, "--"), paths...)
			}
		}
	}
	out, err := l.git(ctx, l.cfg.Root, args...)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(first), nil
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

// trunkProvedPast answers a trunk-suspect owner hold whose blamed trunk has since changed
// (trunkChangeSince). Records written before this rule name no base and carry no
// suspicion; they are read as suspect when their outcome blames the trunk, which is how
// the holds recorded before the rule recover.
func (l *Loop) trunkProvedPast(ctx context.Context, r *store.Reader, c item.Conflict, marks []designUnanswered, byRun map[string]*pendingWork, green string) (designFinding, bool, error) {
	if len(marks) == 0 || green == "" {
		return designFinding{}, false, nil
	}
	last := marks[len(marks)-1]
	if last.RunID != c.DesignRunID || last.Reraised || last.TrunkReraised {
		return designFinding{}, false, nil
	}
	base := last.BaseSHA
	if base == "" {
		w := byRun[c.DesignRunID]
		if w == nil || w.BaseSHA == "" {
			return designFinding{}, false, nil
		}
		base = w.BaseSHA
		rec, err := terminalRecord(r, c.DesignRunID)
		if err != nil {
			return designFinding{}, false, err
		}
		if !designBlamesTrunk(rec, last.Outcome) || repeatsTrunkReraise(marks[:len(marks)-1], last.Outcome, nil) {
			return designFinding{}, false, nil
		}
	} else if !last.TrunkSuspect {
		return designFinding{}, false, nil
	}
	fix, err := l.trunkChangeSince(ctx, c.DesignRunID, base, green)
	if err != nil || fix == "" {
		return designFinding{}, false, err
	}
	return designFinding{conflict: c, runID: c.DesignRunID, outcome: last.Outcome, changes: []string{}, base: base, checks: last.FailingChecks, suspect: true, trunkRaise: true, trunkFix: fix}, true, nil
}
