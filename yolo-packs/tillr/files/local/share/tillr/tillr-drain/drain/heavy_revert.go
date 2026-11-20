package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The owner ruled on 2026-11-17 (graph design OQ-GP1, overturning gate rework D-GR6)
// that tillr reverts a landing by itself when the full suite proves it broke the trunk.
// The heavy runner offers each landing its bisection named to revertLanding before any
// fixer is filed. The revert lands only when it applies cleanly on the tip, the fast gate
// passes with it, and every check the landing broke passes on the tip with it; it is
// refused for a revert, a fixer's landing, and a landing a landed item depends on. The
// reverted item goes back to its worker with the failing check's output, and its next
// gate includes that check (requiredChecks). Only a revert that ends "not possible"
// leaves the red to a fixer, and the reason rides into the fixer's brief. Nothing here
// counts or times anything: each condition is a verdict about this landing on this tip.

const (
	// RevertItemPrefix names the landing a revert is recorded under. It is not an item
	// in the store: a landing row names the work it is for, and a revert is the drain's
	// own work about one landing. The red cap exempts it as it exempts a fixer.
	RevertItemPrefix = "revert-main-"
	// RevertKind is one attributed landing's automatic revert, landed or not possible,
	// keyed by the reverted landing's trunk commit.
	RevertKind = "drain.revert"
	// RefusalReverted is the ending a reverted landing's run is given: its item goes back
	// to its worker with the failing check's output (returnedCauses).
	RefusalReverted = "reverted"
	// ReasonLandingReverted is the planner's hold spelling of that ending, read the way
	// the other returned endings are (returnedReasons).
	ReasonLandingReverted = "landing-reverted"
	// revertBy is the actor recorded on the item's move out of `landed`.
	revertBy = "tillr-drain"
)

// RevertItemID is the landing id a revert of landing sha is recorded under.
func RevertItemID(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > TrunkFixerIDLength {
		sha = sha[:TrunkFixerIDLength]
	}
	return RevertItemPrefix + sha
}

// IsRevertItem answers whether a landing's item id is an automatic revert.
func IsRevertItem(itemID string) bool { return strings.HasPrefix(itemID, RevertItemPrefix) }

// revertPayload is the drain.revert event: what was reverted, why, and what became of it.
type revertPayload struct {
	Landing string   `json:"landing"`
	ItemID  string   `json:"item_id,omitempty"`
	RunID   string   `json:"run_id,omitempty"`
	Tip     string   `json:"tip"`
	Checks  []string `json:"checks"`
	// Result is "reverted" or "not-possible".
	Result   string `json:"result"`
	Revert   string `json:"revert,omitempty"`
	Reason   string `json:"reason,omitempty"`
	StillRed bool   `json:"still_red,omitempty"`
}

// landingOf is the landing row a trunk commit was made by.
type landingOf struct{ itemID, runID string }

// revertLanding is HeavyRunner.Revert for the loop: it builds the inverse of landing on
// tip, and lands it only when every condition of the ruling holds.
func (l *Loop) revertLanding(ctx context.Context, tip, landing string, checks []HeavyCheck, replay HeavyReplay) (out RevertOutcome, err error) {
	ids := heavyIDs(checks)
	record := revertPayload{Landing: landing, Tip: tip, Checks: ids}
	refuse := func(reason string) (RevertOutcome, error) {
		record.Result, record.Reason, record.StillRed = "not-possible", reason, out.StillRed
		l.reportf("loop: heavy %s red from %s: automatic revert not possible: %s; filing a fixer\n", strings.Join(ids, ","), shortSHA(landing), reason)
		if err := l.appendRevert(ctx, record, nil); err != nil {
			return RevertOutcome{}, err
		}
		out.Refused = reason
		return out, nil
	}
	of, found, err := l.landingRecord(ctx, landing)
	if err != nil {
		return RevertOutcome{}, err
	}
	if !found {
		return refuse("no landing record names commit " + landing)
	}
	record.ItemID, record.RunID = of.itemID, of.runID
	workerSession, err := l.landedWorkerSession(ctx, of.runID)
	if err != nil {
		return RevertOutcome{}, err
	}
	switch {
	case IsRevertItem(of.itemID):
		return refuse(shortSHA(landing) + " is itself an automatic revert; a remedy is not undone by a remedy")
	case IsTrunkFixer(of.itemID):
		return refuse(shortSHA(landing) + " is trunk fixer " + of.itemID + "'s landing; a remedy is not undone by a remedy")
	}
	dependents, err := l.landedDependents(ctx, of.itemID)
	if err != nil {
		return RevertOutcome{}, err
	}
	if len(dependents) > 0 {
		return refuse("landed item " + strings.Join(dependents, ", ") + " declares a dependency on " + of.itemID)
	}
	l.reportf("loop: heavy %s red from landing %s (item %s); reverting it on tip %s\n", strings.Join(ids, ","), shortSHA(landing), of.itemID, shortSHA(tip))
	work := filepath.Join(l.cfg.LaneRoot, ".revert", "worktree")
	defer l.removeRevertTree(work)
	revert, reason, err := l.buildRevert(ctx, work, tip, landing, of, ids)
	if err != nil {
		return RevertOutcome{}, err
	}
	if reason != "" {
		return refuse(reason)
	}
	candidate := Candidate{ItemID: RevertItemID(landing), LaneID: "revert", RunID: "run-revert-" + shortSHA(landing) + "-" + shortSHA(tip), Worktree: work, BaseSHA: tip, HeadSHA: revert}
	gate, err := l.cfg.Steps.Gate(ctx, candidate)
	if err != nil {
		if ctx.Err() != nil {
			return RevertOutcome{}, err
		}
		return refuse("the fast gate could not judge the revert: " + err.Error())
	}
	if gate.Outcome != GatePassed && gate.Outcome != GatePassedInherited {
		detail := gate.Outcome
		if len(gate.FailingChecks) > 0 {
			detail += " (" + strings.Join(gate.FailingChecks, ", ") + ")"
		}
		return refuse("the fast gate did not pass with the revert: " + detail)
	}
	// The replay is the test that the attribution was right: each check the landing
	// broke must pass on the tip with the landing taken out. One check, not a proof.
	for _, id := range ids {
		red, err := replay(ctx, revert, id)
		if err != nil {
			if ctx.Err() != nil || cleanupLost(err) {
				return RevertOutcome{}, err
			}
			return refuse(id + " gave no verdict on the tip with " + shortSHA(landing) + " reverted: " + err.Error())
		}
		if red {
			out.StillRed = true
			return refuse(id + " still fails on the tip with " + shortSHA(landing) + " reverted, so that landing does not explain the red; the fixer starts from the tip")
		}
	}
	candidate.ObservedTrunk, err = heavyGitOutput(ctx, l.cfg.Root, "rev-parse", TrunkRef)
	if err != nil {
		return RevertOutcome{}, err
	}
	landed, err := l.landWithActions(ctx, candidate)
	if err != nil {
		if ctx.Err() != nil {
			return RevertOutcome{}, err
		}
		return refuse("the lander could not land the revert: " + err.Error())
	}
	if landed.Result != LandLanded {
		return refuse(strings.TrimSpace("the lander answered " + landed.Result + " " + landed.Reason + " " + strings.Join(landed.ConflictPaths, ", ")))
	}
	after := landed.TrunkAfter
	if after == "" {
		if after, err = heavyGitOutput(ctx, l.cfg.Root, "rev-parse", TrunkRef); err != nil {
			return RevertOutcome{}, err
		}
	}
	record.Result, record.Revert = "reverted", after
	ending := l.revertEnding(landing, of, tip, checks, after)
	ending.WorkerSession = workerSession
	if err := l.appendRevert(ctx, record, &ending); err != nil {
		return RevertOutcome{}, err
	}
	// The runner itself proves the moved trunk next: its loop re-reads the tip.
	l.reportf("loop: reverted %s as %s; %s passes on the tip with it\n", shortSHA(landing), shortSHA(after), strings.Join(ids, ","))
	if err := l.returnReverted(ctx, of, landing, ids); err != nil {
		// The trunk is repaired either way; the item's return is what failed, and the
		// operator reads why on this line.
		l.reportf("loop: reverted %s, but item %s could not go back to its author: %v\n", shortSHA(landing), of.itemID, err)
	} else {
		l.reportf("loop: reverted %s, %s back with its author\n", shortSHA(landing), of.itemID)
	}
	return RevertOutcome{Reverted: after}, nil
}

// buildRevert makes the inverse of landing on tip in a detached checkout and commits it.
// A reason is answered, not an error, when the revert does not apply cleanly or there
// is nothing left to revert.
func (l *Loop) buildRevert(ctx context.Context, work, tip, landing string, of landingOf, ids []string) (string, string, error) {
	l.removeRevertTree(work)
	if err := os.MkdirAll(filepath.Dir(work), 0755); err != nil {
		return "", "", err
	}
	if err := withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
		_, err := heavyGitOutput(ctx, l.cfg.Root, "worktree", "add", "--detach", work, tip)
		return err
	}); err != nil {
		return "", "", err
	}
	if _, err := heavyGitOutput(ctx, work, "revert", "--no-commit", landing); err != nil {
		conflicts, _ := heavyGitOutput(ctx, work, "diff", "--name-only", "--diff-filter=U")
		_, _ = heavyGitOutput(ctx, work, "revert", "--abort")
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		paths := strings.Join(strings.Fields(conflicts), ", ")
		if paths == "" {
			return "", "the revert of " + shortSHA(landing) + " does not apply on tip " + shortSHA(tip) + ": " + heavyBrief(err.Error()), nil
		}
		return "", "the revert of " + shortSHA(landing) + " does not apply cleanly on tip " + shortSHA(tip) + ": later landings changed " + paths, nil
	}
	if _, err := heavyGitOutput(ctx, work, "diff", "--cached", "--quiet"); err == nil {
		return "", shortSHA(landing) + " has nothing left to revert on tip " + shortSHA(tip), nil
	}
	subject, err := heavyGitOutput(ctx, l.cfg.Root, "show", "-s", "--format=%s", landing)
	if err != nil {
		return "", "", err
	}
	message := fmt.Sprintf("revert: %s\n\nThis reverts commit %s.\n\nThe full-suite check %s failed on the trunk from this landing on (proof of %s).\ntillr reverted it automatically, and item %s returns to its worker with the\nfailing output (graph design OQ-GP1).\n", subject, landing, strings.Join(ids, ", "), tip, of.itemID)
	if _, err := heavyGitOutput(ctx, work, "commit", "-q", "-m", message); err != nil {
		return "", "", err
	}
	revert, err := heavyGitOutput(ctx, work, "rev-parse", "HEAD")
	return revert, "", err
}

func (l *Loop) removeRevertTree(work string) {
	_ = withWorktreeRegistryLock(context.Background(), l.cfg.Root, func() error {
		_, _ = heavyGitOutput(context.Background(), l.cfg.Root, "worktree", "remove", "--force", work)
		return nil
	})
	_ = os.RemoveAll(work)
}

// landingRecord reads the landing row that made trunk commit sha.
func (l *Loop) landingRecord(ctx context.Context, sha string) (landingOf, bool, error) {
	var of landingOf
	found := false
	err := l.readStore(ctx, func(r *store.Reader) error {
		shas, err := landingShas(r, sha)
		if err != nil {
			return err
		}
		in, args := heavy.InList(shas)
		err = r.QueryRow(`SELECT item_id, run_id FROM landings WHERE trunk_after IN `+in+` AND result = 'landed' ORDER BY event_seq DESC LIMIT 1`, args...).Scan(&of.itemID, &of.runID)
		if errors.Is(err, sql.ErrNoRows) || err != nil && strings.Contains(err.Error(), "no such table") {
			return nil
		}
		found = err == nil
		return err
	})
	return of, found, err
}

// landedDependents answers the landed items that declare a dependency on itemID.
func (l *Loop) landedDependents(ctx context.Context, itemID string) ([]string, error) {
	var out []string
	err := l.readStore(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT e.item_id FROM item_edges e JOIN items i ON i.item_id = e.item_id WHERE e.blocked_by = ? AND i.state = ? ORDER BY e.item_id`, itemID, StateLanded)
		if err != nil {
			if strings.Contains(err.Error(), "no such table") {
				return nil
			}
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}

func (l *Loop) readStore(ctx context.Context, fn func(*store.Reader) error) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.ReadTx(ctx, fn)
}

// revertEnding is the reverted run's ending: what its worker is told, and which checks
// its next gate must include.
func (l *Loop) revertEnding(landing string, of landingOf, tip string, checks []HeavyCheck, revert string) refusalPayload {
	ids := heavyIDs(checks)
	var detail strings.Builder
	fmt.Fprintf(&detail, "Your landing %s was reverted by %s: the full suite proved it broke the trunk. ", shortSHA(landing), shortSHA(revert))
	fmt.Fprintf(&detail, "On tip %s, %s failed from %s on and passed with it reverted.", shortSHA(tip), strings.Join(ids, ", "), shortSHA(landing))
	ending := []EndingCheck{}
	for _, check := range checks {
		if text := strings.TrimSpace(check.Detail); text != "" {
			fmt.Fprintf(&detail, "\n\n%s output:\n%s", check.CheckID, heavyBrief(text))
		}
		c := EndingCheck{CheckID: check.CheckID, LogRef: l.heavyLog(tip, check.CheckID)}
		if c.LogRef != "" {
			if body, err := os.ReadFile(c.LogRef); err == nil {
				c.Digest = evidenceDigest(string(body), of.runID)
			}
		}
		if c.Digest == "" {
			c.Digest = evidenceDigest(check.Detail, of.runID)
		}
		ending = append(ending, c)
	}
	detail.WriteString("\n\nRedo the change on the current trunk so that these checks pass; your next gate runs them.")
	sort.Slice(ending, func(i, j int) bool { return ending[i].CheckID < ending[j].CheckID })
	text := detail.String()
	return refusalPayload{Step: RefusalReverted, ItemID: of.itemID, RedSHA: landing, FailingChecks: ids, Detail: text, Checks: ending, Evidence: endingKey(RefusalReverted, ending, text, of.runID), Selectors: l.heavySelectors(ids)}
}

// heavyLog is the declaration log the proof of tip wrote for check id, or "".
func (l *Loop) heavyLog(tip, id string) string {
	matches, _ := filepath.Glob(filepath.Join(l.cfg.LaneRoot, ".rolling", "logs", tip, "*-"+strings.ReplaceAll(id, "/", "-")+".log"))
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

// heavySelectors keeps a legacy check's replay selector beside its id, so the item's
// next gate can run it after a loop restart has emptied the runner's memory.
func (l *Loop) heavySelectors(ids []string) map[string]string {
	source, ok := l.cfg.Steps.(interface{ HeavySelector(string) string })
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, id := range ids {
		if selector := source.HeavySelector(id); selector != "" {
			out[id] = selector
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// appendRevert records one revert decision, once per reverted landing, and, for a revert
// that landed, the reverted run's ending in the same transaction: a revert on the record
// always names the ending its item was given.
func (l *Loop) appendRevert(ctx context.Context, p revertPayload, ending *refusalPayload) error {
	body, err := json.Marshal(p)
	if err != nil {
		return Internalf("encoding the %s payload: %v", RevertKind, err)
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	landing := p.Landing
	var evidence refusalEvidence
	if ending != nil && p.RunID != "" {
		evidence = l.readRefusalEvidence(p.RunID, *ending)
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		var done int
		if err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_type='landing' AND object_id=? AND json_extract(payload,'$.result')='reverted'`, RevertKind, landing).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			return nil
		}
		if _, err := tx.Append(event.Event{Kind: RevertKind, ObjectType: "landing", ObjectID: &landing, Payload: body, At: at}); err != nil {
			return Internalf("appending the %s event: %v", RevertKind, err)
		}
		if ending == nil || p.RunID == "" {
			return nil
		}
		raw, err := json.Marshal(ending)
		if err != nil {
			return Internalf("encoding the %s payload: %v", RefusalKind, err)
		}
		run := p.RunID
		if _, err := tx.Append(event.Event{Kind: RefusalKind, ObjectType: "run", ObjectID: &run, Payload: raw, At: at}); err != nil {
			return Internalf("appending the %s event: %v", RefusalKind, err)
		}
		if err := recordLandingCallLanding(tx, p.RunID, p.Landing, at); err != nil {
			return err
		}
		return l.answerLandingRefusal(tx, Candidate{RunID: p.RunID, HeadSHA: p.Revert, BaseSHA: p.Tip}, *ending, evidence, at)
	})
}

// ItemReturnStep moves a landed item back into the queue, with the reason on its record.
// ProcessSteps implements it through `tillr-item state`; a step set without it leaves
// the item landed, and the loop says so.
type ItemReturnStep interface {
	ReturnLanded(ctx context.Context, itemID, runID, reason string) error
}

// returnReverted sends a reverted landing's item back to its worker.
func (l *Loop) returnReverted(ctx context.Context, of landingOf, landing string, ids []string) error {
	step, ok := l.cfg.Steps.(ItemReturnStep)
	if !ok {
		return Usagef("the configured steps cannot move a landed item back into the queue")
	}
	if err := step.ReturnLanded(ctx, of.itemID, of.runID, "landing "+landing+" reverted: "+strings.Join(ids, ", ")+" failed on the trunk from it on"); err != nil {
		return err
	}
	// The revert withdraws what the landing settled (revert_withdraw.go); the next document
	// sync repairs a withdrawal that did not commit here.
	return l.withdrawRevertedVerdicts(ctx)
}

// ReturnLanded moves a landed item to the queue through the item face, with a receipt.
func (s *ProcessSteps) ReturnLanded(ctx context.Context, itemID, runID, reason string) error {
	return s.itemState(ctx, itemID, runID, StateInQueue, []string{"--reason", reason, "--by", revertBy})
}

// requiredChecks are the full-suite checks a returned item's gate must include: those a
// revert of its landing named, until the item lands again.
func (l *Loop) requiredChecks(ctx context.Context, itemID string) ([]string, map[string]string, error) {
	var ids []string
	selectors := map[string]string{}
	err := l.readStore(ctx, func(r *store.Reader) error {
		var after int64
		if err := r.QueryRow(`SELECT COALESCE(MAX(event_seq), 0) FROM landings WHERE item_id = ? AND result = 'landed'`, itemID).Scan(&after); err != nil {
			if strings.Contains(err.Error(), "no such table") {
				after = 0
			} else {
				return err
			}
		}
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND seq > ? AND json_extract(payload, '$.step') = ? AND json_extract(payload, '$.item_id') = ? ORDER BY seq`, RefusalKind, after, RefusalReverted, itemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		seen := map[string]bool{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var p refusalPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			for _, id := range p.FailingChecks {
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			for id, selector := range p.Selectors {
				selectors[id] = selector
			}
		}
		return rows.Err()
	})
	return ids, selectors, err
}

// RequiredCheckStep judges one full-suite check on a candidate's checkout, outside the
// heavy runner's lock: the check a revert named, which the fast gate has shown it
// cannot see.
type RequiredCheckStep interface {
	JudgeRequired(ctx context.Context, dir, checkID, selector string) (HeavyCheck, error)
}

// judgeRequired runs a returned item's required checks after its fast gate passed, and
// answers the refusal detail when one fails. A check that answers no verdict is not red:
// it is reported, and the heavy proof after the landing judges it.
func (l *Loop) judgeRequired(ctx context.Context, candidate Candidate) (string, error) {
	ids, selectors, err := l.requiredChecks(ctx, candidate.ItemID)
	if err != nil || len(ids) == 0 {
		return "", err
	}
	step, ok := l.cfg.Steps.(RequiredCheckStep)
	if !ok {
		l.reportf("loop: %s must pass %s, but the configured steps cannot run a full-suite check at the gate\n", candidate.ItemID, strings.Join(ids, ","))
		return "", nil
	}
	failed := []string{}
	for _, id := range ids {
		check, err := step.JudgeRequired(ctx, candidate.Worktree, id, selectors[id])
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			l.reportf("loop: %s required check %s gave no verdict: %v\n", candidate.ItemID, id, err)
			continue
		}
		switch check.Result {
		case "pass":
			l.reportf("loop: %s required check %s passes\n", candidate.ItemID, id)
		case "fail":
			text := id + " failed"
			if detail := strings.TrimSpace(check.Detail); detail != "" {
				text += ":\n" + heavyBrief(detail)
			}
			failed = append(failed, text)
		default:
			l.reportf("loop: %s required check %s answered %s, not a verdict: %s\n", candidate.ItemID, id, check.Result, heavyBrief(check.Detail))
		}
	}
	if len(failed) == 0 {
		return "", nil
	}
	return "the gate passed, but this item's landing was reverted for a full-suite check, and its candidate must pass that check before it lands: " + strings.Join(failed, "\n\n"), nil
}

// HeavySelector is a legacy check's kind:label replay selector, when a proof recorded it.
func (s *ProcessSteps) HeavySelector(id string) string {
	if value, ok := s.heavyReplaySelectors.Load(id); ok {
		return value.(string)
	}
	return ""
}

// heavyUnownedKey marks a judge call made outside the heavy runner: its processes run in
// their own group, reaped on return, rather than under the runner's supervisor.
type heavyUnownedKey struct{}

// JudgeRequired judges one heavy check in dir without the heavy runner's lock.
func (s *ProcessSteps) JudgeRequired(ctx context.Context, dir, id, selector string) (HeavyCheck, error) {
	if selector != "" {
		if _, known := s.heavyReplaySelectors.Load(id); !known {
			s.heavyReplaySelectors.Store(id, selector)
		}
	}
	checks, err := s.JudgeHeavy(context.WithValue(ctx, heavyUnownedKey{}, true), dir, id)
	if err != nil {
		return HeavyCheck{}, err
	}
	if len(checks) != 1 || checks[0].CheckID != id {
		return HeavyCheck{}, Externalf("required check %s gave no verdict", id)
	}
	return checks[0], nil
}
