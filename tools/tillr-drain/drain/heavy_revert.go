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
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The owner ruled on 2026-11-17 (graph design OQ-GP1; D-GR6 in docs/reference/gate.md)
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

// revertPayload is the drain.revert event: what was reverted, why, and what became of it.
type revertPayload struct {
	Landing   string            `json:"landing"`
	ItemID    string            `json:"item_id,omitempty"`
	RunID     string            `json:"run_id,omitempty"`
	Tip       string            `json:"tip"`
	Checks    []string          `json:"checks"`
	CheckLogs map[string]string `json:"check_logs,omitempty"`
	// Result is "reverted", "dropped", or "not-possible".
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
	l.revertMu.Lock()
	defer l.revertMu.Unlock()
	if prior, err := revertedBy(ctx, l.cfg.Store, landing); err != nil {
		return RevertOutcome{}, err
	} else if prior != "" {
		return RevertOutcome{Reverted: prior}, nil
	}
	ids := heavyIDs(checks)
	logs := l.snapshotCheckLogs(filepath.Join(".revert", "logs", tip, landing), l.heavyCheckLogs(tip, checks))
	record := revertPayload{Landing: landing, Tip: tip, Checks: ids, CheckLogs: logs}
	refuse := func(reason string) (RevertOutcome, error) {
		record.Result, record.Reason, record.StillRed = "not-possible", reason, out.StillRed
		next := "filing a fixer"
		if out.StillRed {
			next = "discarding attribution and searching again"
		}
		l.reportf("loop: heavy %s red from %s: automatic revert not possible: %s; %s\n", strings.Join(ids, ","), shortSHA(landing), reason, next)
		if err := l.appendRevert(ctx, record, nil); err != nil {
			return RevertOutcome{}, err
		}
		out.Refused = reason
		return out, nil
	}
	drop := func(repairedTip string) (RevertOutcome, error) {
		reason := fmt.Sprintf("trunk tip %s already passes %s", shortSHA(repairedTip), strings.Join(ids, ", "))
		record.Result, record.Reason = "dropped", reason
		l.reportf("loop: heavy %s red from %s: automatic revert dropped: %s\n", strings.Join(ids, ","), shortSHA(landing), reason)
		if err := l.appendRevert(ctx, record, nil); err != nil {
			return RevertOutcome{}, err
		}
		return RevertOutcome{Dropped: reason}, nil
	}
	if fixerID, err := l.openFixerForLanding(ctx, landing); err != nil {
		return RevertOutcome{}, err
	} else if fixerID != "" {
		return refuse("trunk fixer " + fixerID + " is already open for " + shortSHA(landing))
	}
	of, found, err := l.landingRecord(ctx, landing)
	if err != nil {
		return RevertOutcome{}, err
	}
	if !found {
		if m, rebased, rerr := rebasedLanding(ctx, l.cfg.Root, l.cfg.Store, landing, tip); rerr == nil && rebased {
			return refuse(rebaseNote(landing, m))
		}
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
	case IsRemoteSyncItem(of.itemID):
		return refuse(shortSHA(landing) + " is remote-sync repair " + of.itemID + "'s landing; a remedy is not undone by a remedy")
	}
	dependents, err := l.landedDependents(ctx, of.itemID)
	if err != nil {
		return RevertOutcome{}, err
	}
	if len(dependents) > 0 {
		return refuse("landed item " + strings.Join(dependents, ", ") + " declares a dependency on " + of.itemID)
	}
	confirmedRedTip := tip
	if cur, err := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", l.trunkRef()); err != nil {
		return RevertOutcome{}, err
	} else if cur != confirmedRedTip {
		if replay == nil {
			return refuse("trunk advanced to " + shortSHA(cur) + " and cannot confirm whether " + strings.Join(ids, ", ") + " still fails there")
		}
		allPass, err := tipChecksPass(ctx, cur, ids, replay)
		if err != nil {
			if ctx.Err() != nil || cleanupLost(err) {
				return RevertOutcome{}, err
			}
			return refuse("trunk advanced to " + shortSHA(cur) + " and checking " + strings.Join(ids, ", ") + " there gave no verdict: " + err.Error())
		}
		if allPass {
			return drop(cur)
		}
		confirmedRedTip = cur
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
	gateCtx, cancelGate := context.WithCancel(ctx)
	watchCtx, cancelWatch := context.WithCancel(ctx)
	var (
		watchWG    sync.WaitGroup
		watchMu    sync.Mutex
		droppedTip string
	)
	if replay != nil {
		watchWG.Add(1)
		go func(initialChecked string) {
			defer watchWG.Done()
			ticker := time.NewTicker(l.revertTipPollInterval())
			defer ticker.Stop()
			lastChecked := initialChecked
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
				}
				cur, err := heavyGitOutput(watchCtx, l.cfg.Root, "rev-parse", l.trunkRef())
				if err != nil || cur == "" || cur == lastChecked {
					continue
				}
				lastChecked = cur
				allPass, err := tipChecksPass(watchCtx, cur, ids, replay)
				if err != nil {
					continue
				}
				if allPass {
					watchMu.Lock()
					droppedTip = cur
					watchMu.Unlock()
					cancelGate()
					return
				}
				watchMu.Lock()
				confirmedRedTip = cur
				watchMu.Unlock()
			}
		}(confirmedRedTip)
	}
	gate, err := l.cfg.Steps.Gate(gateCtx, candidate)
	cancelWatch()
	watchWG.Wait()
	cancelGate()
	l.sweepDisabledMarkers(ctx)
	watchMu.Lock()
	repairedTip := droppedTip
	watchMu.Unlock()
	if repairedTip != "" {
		return drop(repairedTip)
	}
	if ctx.Err() == nil && replay != nil {
		if cur, cerr := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", l.trunkRef()); cerr == nil && cur != "" && cur != confirmedRedTip {
			if allPass, perr := tipChecksPass(ctx, cur, ids, replay); perr == nil {
				if allPass {
					return drop(cur)
				}
				confirmedRedTip = cur
			}
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return RevertOutcome{}, err
		}
		return refuse("the fast gate could not judge the revert: " + err.Error())
	}
	// Even a fast-gate refusal can be the original red. Replay the attributed
	// checks before classifying the refusal, so that it cannot hide a wrong culprit.
	gateRefused := ""
	if gate.Outcome != GatePassed && gate.Outcome != GatePassedInherited {
		detail := gate.Outcome
		if len(gate.FailingChecks) > 0 {
			detail += " (" + strings.Join(gate.FailingChecks, ", ") + ")"
		}
		gateRefused = "the fast gate did not pass with the revert: " + detail
	}
	for _, id := range ids {
		red, err := replay(ctx, revert, id)
		if err != nil {
			if ctx.Err() != nil || cleanupLost(err) {
				return RevertOutcome{}, err
			}
			reason := id + " gave no verdict on the tip with " + shortSHA(landing) + " reverted: " + err.Error()
			return refuse(joinDetail(gateRefused, reason))
		}
		if red {
			out.StillRed, out.StillRedCheck = true, id
			return refuse(joinDetail(gateRefused, id+" still fails on the tip with "+shortSHA(landing)+" reverted, so the attribution did not explain the red"))
		}
	}
	if gateRefused != "" {
		return refuse(gateRefused)
	}
	candidate.ObservedTrunk, err = heavyGitOutput(ctx, l.cfg.Root, "rev-parse", l.trunkRef())
	if err != nil {
		return RevertOutcome{}, err
	}
	if candidate.ObservedTrunk != confirmedRedTip {
		if replay == nil {
			return refuse("trunk advanced to " + shortSHA(candidate.ObservedTrunk) + " and cannot confirm whether " + strings.Join(ids, ", ") + " still fails there")
		}
		allPass, err := tipChecksPass(ctx, candidate.ObservedTrunk, ids, replay)
		if err != nil {
			if ctx.Err() != nil || cleanupLost(err) {
				return RevertOutcome{}, err
			}
			return refuse("trunk advanced to " + shortSHA(candidate.ObservedTrunk) + " and checking " + strings.Join(ids, ", ") + " there gave no verdict: " + err.Error())
		}
		if allPass {
			return drop(candidate.ObservedTrunk)
		}
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
		if after, err = heavyGitOutput(ctx, l.cfg.Root, "rev-parse", l.trunkRef()); err != nil {
			return RevertOutcome{}, err
		}
	}
	record.Result, record.Revert = "reverted", after
	ending := l.revertEnding(landing, of, tip, checks, after, logs)
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

// openFixerForLanding answers the unresolved trunk fixer for landing, or "" when none is open.
func (l *Loop) openFixerForLanding(ctx context.Context, landing string) (string, error) {
	if l.cfg.Store == "" || landing == "" {
		return "", nil
	}
	fixerID := TrunkFixerID(landing)
	found := ""
	err := l.readStore(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		var state string
		err = r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, fixerID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) || err != nil && strings.Contains(err.Error(), "no such table") {
			return nil
		}
		if err != nil {
			return err
		}
		if !finishedState(declared.Parsed, state) {
			found = fixerID
		}
		return nil
	})
	return found, err
}

// revertFromGateReplay applies HeavyRunner's revert-before-fixer rule on the gate-replay
// path: it offers an attributed landing to revertLanding using the replay step, and turns
// a non-cancellation attempt error into a refusal so the caller falls back to a fixer.
func (l *Loop) revertFromGateReplay(ctx context.Context, candidate Candidate, replay ReplayStep, tip, landing string, redChecks []string, gate GateOutcome) (RevertOutcome, error) {
	if prior, err := revertedBy(ctx, l.cfg.Store, landing); err != nil {
		return RevertOutcome{}, err
	} else if prior != "" {
		return RevertOutcome{Reverted: prior}, nil
	}
	if l.cfg.LaneRoot == "" {
		return RevertOutcome{Refused: "revert worktree root is not configured"}, nil
	}
	currentTip, err := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", l.trunkRef())
	if err != nil {
		if ctx.Err() != nil {
			return RevertOutcome{}, err
		}
		return RevertOutcome{Refused: "the revert could not read trunk: " + err.Error()}, nil
	}
	if currentTip != tip {
		return RevertOutcome{Refused: "trunk advanced beyond the confirmed red commit"}, nil
	}
	checks := make([]HeavyCheck, 0, len(redChecks))
	for _, id := range redChecks {
		checks = append(checks, HeavyCheck{CheckID: id, LogPath: gate.CheckLogs[id]})
	}
	replayOne := func(ctx context.Context, sha, checkID string) (bool, error) {
		out, err := l.runReplay(ctx, replay, candidate, sha, []string{checkID})
		l.sweepDisabledMarkers(ctx)
		if err != nil {
			return false, err
		}
		if !out.Known || out.Rev != "" && out.Rev != sha {
			return false, Externalf("replay for %s at %s gave no verdict", checkID, shortSHA(sha))
		}
		return len(out.Red) > 0, nil
	}
	out, err := l.revertLanding(ctx, tip, landing, checks, replayOne)
	if err != nil {
		if ctx.Err() != nil || cleanupLost(err) {
			return out, err
		}
		out = RevertOutcome{Refused: "the revert could not be attempted: " + err.Error()}
	}
	if out.Reverted == "" && out.Dropped == "" && out.Refused == "" {
		out.Refused = "the revert answered neither a landing nor a reason"
	}
	return out, nil
}

func (l *Loop) revertTipPollInterval() time.Duration {
	if l.revertTipPoll > 0 {
		return l.revertTipPoll
	}
	return 200 * time.Millisecond
}

// tipChecksPass reports whether every named failing check passes on sha.
// It stops as soon as any check still fails or fails to produce a verdict.
func tipChecksPass(ctx context.Context, sha string, ids []string, replay HeavyReplay) (bool, error) {
	for _, id := range ids {
		red, err := replay(ctx, sha, id)
		if err != nil {
			return false, err
		}
		if red {
			return false, nil
		}
	}
	return true, nil
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

// landedDependents answers the landed items that declare a prerequisite (blocked_by) dependency on itemID.
func (l *Loop) landedDependents(ctx context.Context, itemID string) ([]string, error) {
	var out []string
	err := l.readStore(ctx, func(r *store.Reader) error {
		hasKind, err := hasItemEdgesKindColumn(r)
		if err != nil {
			return err
		}
		query := `SELECT e.item_id FROM item_edges e JOIN items i ON i.item_id = e.item_id WHERE e.blocked_by = ? AND i.state = ? ORDER BY e.item_id`
		if hasKind {
			query = `SELECT e.item_id FROM item_edges e JOIN items i ON i.item_id = e.item_id WHERE e.blocked_by = ? AND i.state = ? AND COALESCE(NULLIF(e.kind, ''), '` + item.EdgeKindBlockedBy + `') = '` + item.EdgeKindBlockedBy + `' ORDER BY e.item_id`
		}
		rows, err := r.Query(query, itemID, StateLanded)
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
func (l *Loop) revertEnding(landing string, of landingOf, tip string, checks []HeavyCheck, revert string, logs map[string]string) refusalPayload {
	ids := heavyIDs(checks)
	var detail strings.Builder
	fmt.Fprintf(&detail, "Your landing %s was reverted by %s: the full suite proved it broke the trunk. ", shortSHA(landing), shortSHA(revert))
	fmt.Fprintf(&detail, "On tip %s, %s failed from %s on and passed with it reverted.", shortSHA(tip), strings.Join(ids, ", "), shortSHA(landing))
	ending := []EndingCheck{}
	for _, check := range checks {
		if text := strings.TrimSpace(check.Detail); text != "" {
			fmt.Fprintf(&detail, "\n\n%s output:\n%s", check.CheckID, heavyBrief(text))
		}
		c := EndingCheck{CheckID: check.CheckID, LogRef: logs[check.CheckID]}
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
	p := refusalPayload{Step: RefusalReverted, ItemID: of.itemID, RedSHA: landing, FailingChecks: ids, Detail: text, Checks: ending, Evidence: endingKey(RefusalReverted, ending, text, of.runID), Selectors: l.heavySelectors(ids)}
	p.RefusalCauses = computeRefusalCauses(p)
	return p
}

// heavyLog is the declaration log the proof of tip wrote for check id, or "".
func (l *Loop) heavyLog(tip, id string) string {
	safe := strings.ReplaceAll(id, "/", "-")
	for _, suffix := range []string{".triage.log", ".log"} {
		matches, _ := filepath.Glob(filepath.Join(l.cfg.LaneRoot, ".rolling", "logs", tip, "*-"+safe+suffix))
		if len(matches) > 0 {
			sort.Strings(matches)
			return matches[len(matches)-1]
		}
	}
	return ""
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
		endingCopy := *ending
		endingCopy.RefusalCauses = computeRefusalCauses(endingCopy)
		raw, err := json.Marshal(endingCopy)
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
// attaches the replay evidence and answers the refusal detail when one fails. A check
// that answers no verdict is not red: the heavy proof after the landing judges it.
func (l *Loop) judgeRequired(ctx context.Context, candidate *Candidate) (string, error) {
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
		l.sweepDisabledMarkers(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			l.reportf("loop: %s required check %s gave no verdict: %v\n", candidate.ItemID, id, err)
			continue
		}
		classified := classifyCheck(checkresult.Observation{
			Check:    id,
			Commit:   candidate.HeadSHA,
			Worktree: candidate.Worktree,
			Source:   checkresult.HeavyCheckWord,
			Word:     check.Result,
		})
		switch classified.Outcome {
		case checkresult.Pass:
			l.reportf("loop: %s required check %s passes\n", candidate.ItemID, id)
		case checkresult.Fail:
			text := id + " failed"
			if detail := strings.TrimSpace(check.Detail); detail != "" {
				text += ":\n" + heavyBrief(detail)
			}
			evidence := l.keepRequiredFailure(*candidate, id, check)
			if evidence.LogRef != "" {
				text += "\nrequired replay log: " + evidence.LogRef
			} else {
				text += "\nrequired replay output could not be retained"
			}
			candidate.requiredFailures = append(candidate.requiredFailures, evidence)
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

// keepRequiredFailure keeps the replay's own output beside the run, independent of
// the rolling proof's logs and the passing fast-gate verdict.
func (l *Loop) keepRequiredFailure(candidate Candidate, id string, check HeavyCheck) EndingCheck {
	evidence := EndingCheck{CheckID: id}
	logDir := filepath.Join(RunDir(l.cfg.Root, candidate.RunID), "logs")
	name := fmt.Sprintf("required-%02d-%s", len(candidate.requiredFailures), sanitizeLogComponent(id))
	for _, src := range []string{check.LogRef, check.LogPath} {
		suffix := ".log"
		if strings.HasSuffix(src, ".triage.log") {
			suffix = ".triage.log"
		}
		if kept, ok := copyLogWithWhole(candidate.Worktree, src, logDir, name+suffix); ok {
			evidence.LogRef = kept
			break
		}
	}
	if evidence.LogRef == "" && check.Detail != "" {
		path := filepath.Join(logDir, name+".log")
		if err := os.MkdirAll(logDir, 0o755); err == nil && os.WriteFile(path, []byte(check.Detail), 0o644) == nil {
			evidence.LogRef = path
		}
	}
	if output, err := os.ReadFile(evidence.LogRef); err == nil {
		evidence.Digest = evidenceDigest(string(output), candidate.RunID)
		evidence.Failing = readTriageString(string(output))
	}
	// A focused Go replay already has the failing test in its package-qualified ID.
	if len(evidence.Failing) == 0 && strings.HasPrefix(id, goTestPrefix) {
		if parts := strings.SplitN(strings.TrimPrefix(id, goTestPrefix), ":", 3); len(parts) == 3 {
			evidence.Failing = []FailingCheck{{ID: id, Cases: []FailingCase{{Name: parts[2]}}}}
		}
	}
	return evidence
}

// HeavySelector is a legacy check's kind:label replay selector, when a proof recorded it.
func (s *ProcessSteps) HeavySelector(id string) string {
	if value, ok := s.heavyReplaySelectors.Load(id); ok {
		return value.(string)
	}
	return ""
}

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
