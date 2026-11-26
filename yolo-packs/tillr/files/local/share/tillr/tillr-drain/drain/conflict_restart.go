package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// D-JL12 (docs/design/tillr-judge-and-lifecycle.md, row 1a): the second identical squash
// conflict of a retry cycle discards the candidate and returns the item so the planner
// starts a fresh attempt of its workflow from the first stage, on the current trunk. Only
// when that fresh attempt conflicts at landing does the item park for the operator. A
// retry cycle is the runs since the item's latest `tillr drain retry`.

// candidateDiscardedKind records that the drain discarded a candidate whose second identical
// conflict restarted its item: the candidate stays pinned, and no dirty or repair hold
// recorded for its run speaks any more, including one a replay folds in later.
const candidateDiscardedKind = "drain.candidate-discarded"

func init() {
	if err := event.Register(candidateDiscardedKind, Utility); err != nil {
		panic(err)
	}
}

// conflictCycle is what the item's current retry cycle holds, as the conflict rules read it.
type conflictCycle struct {
	// restarted is true when an earlier run of the cycle ended in a conflict restart.
	restarted bool
	// chain is the cycle's endings, in run order.
	chain []chainEnding
	// decisions are the decisions that dispatched the item's runs.
	decisions map[string]Decision
}

// readConflictCycle answers the item's endings since its latest retry authorization, with
// whether one of them was a conflict restart. The run the authorization supersedes closes
// the earlier cycle, and is not part of the new one.
func readConflictCycle(r *store.Reader, history runHistory, itemID string) (conflictCycle, error) {
	priors, err := readPriorRuns(r)
	if err != nil {
		return conflictCycle{}, err
	}
	decisions, err := readInputDecisions(r)
	if err != nil {
		return conflictCycle{}, err
	}
	retries, err := readRetries(r)
	if err != nil {
		return conflictCycle{}, err
	}
	runs := priors[itemID]
	start := 0
	if row, ok := retries[itemID]; ok {
		for i, prior := range runs {
			if prior.RunID == row.SupersedesRunID {
				start = i + 1
			}
		}
	}
	cycle := conflictCycle{decisions: decisions}
	for _, prior := range runs[start:] {
		e, ok := history.endings[prior.RunID]
		if !ok {
			continue
		}
		if e.Step == RefusalConflict && e.Restart {
			cycle.restarted = true
		}
		cycle.chain = append(cycle.chain, chainEnding{RunID: prior.RunID, Producer: producerOf(decisions[prior.RunID]), Cause: e.Step, Key: e.Evidence})
	}
	return cycle, nil
}

// conflictCycleOf reads the cycle of the candidate's item.
func (l *Loop) conflictCycleOf(ctx context.Context, itemID string) (conflictCycle, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return conflictCycle{}, readFailure(err)
	}
	defer s.Close()
	var cycle conflictCycle
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		history, err := readRunHistory(r)
		if err != nil {
			return err
		}
		cycle, err = readConflictCycle(r, history, itemID)
		return err
	})
	return cycle, err
}

// restartConflicted discards a candidate whose conflict repeated an earlier conflict of the
// retry cycle and returns the item to the planner. It pins the candidate, resets the lane to
// the current trunk, records the conflict as a restart (which answers the candidate's open
// landing call), and releases the dirty and repair holds recorded for the run. The planner
// then dispatches the item's workflow from its first stage (returnedEnding).
func (l *Loop) restartConflicted(ctx context.Context, candidate Candidate, landed LandOutcome, key, mainSHA string) error {
	if err := l.pinCandidateHead(ctx, candidate); err != nil {
		return err
	}
	if err := l.preserveWork(ctx, candidate.LaneID, candidate.RunID, "candidate conflicted twice during landing"); err != nil {
		return err
	}
	revision := l.trunkRef()
	if mainSHA != "" {
		revision = mainSHA
	}
	if err := l.resetLaneTo(ctx, candidate.LaneID, revision); err != nil {
		return err
	}
	if err := l.recordConflictRestart(ctx, candidate, landed, key); err != nil {
		return err
	}
	l.reportf("loop: %s conflicted twice with the same paths and hunks; the candidate is discarded and the item restarts from the current trunk\n", candidate.ItemID)
	return nil
}

func (l *Loop) recordConflictRestart(ctx context.Context, candidate Candidate, landed LandOutcome, key string) error {
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "conflict"); err != nil {
		return err
	}
	payload := refusalPayload{
		Detail:        strings.Join(landed.ConflictPaths, "\n"),
		Step:          RefusalConflict,
		ItemID:        candidate.ItemID,
		LaneID:        candidate.LaneID,
		TickID:        candidate.TickID,
		ConflictPaths: landed.ConflictPaths,
		Evidence:      key,
		Restart:       true,
	}
	payload.RefusalCauses = computeRefusalCauses(payload)
	at := l.cfg.Now().UTC()
	return appendJudgmentThen(ctx, l.cfg.Store, RefusalKind, "run", candidate.RunID, payload, at, func(tx *store.Tx) error {
		if err := closeLandingCall(tx, candidate.RunID, "conflict-restart", at); err != nil {
			return err
		}
		return appendEffect(tx, candidateDiscardedKind, candidate.RunID, struct{}{}, at)
	})
}

// closeLandingCall closes the run's open landing call without an answer: the candidate it
// offered is discarded, so no later run is handed a reply about it.
func closeLandingCall(tx *store.Tx, runID, reason string, at time.Time) error {
	var id string
	err := tx.QueryRow(`SELECT object_id FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?`, callOffered, runID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, kind := range []string{callAnswered, callClosed} {
		seen, err := judgmentSeen(tx, kind, "call", id)
		if err != nil {
			return err
		}
		if seen != 0 {
			return nil
		}
	}
	return appendLandingCallEvent(tx, callClosed, id, landingCallClose{Reason: reason}, at)
}

// restartBrief is the brief section of a fresh attempt that follows a conflict restart. It
// carries no conflict-fix instruction: the earlier attempt is not continued, and the item's
// own source reference above is what the attempt builds from.
func restartBrief(e ReturnedEnding) string {
	var b strings.Builder
	b.WriteString("\n## Earlier attempt\n\n")
	fmt.Fprintf(&b, "This is a fresh attempt from the current trunk. The item's earlier attempt, ending with run %s, conflicted twice with the trunk at landing, so it was discarded and nothing of it carries over. This is not a count against the item: build the item's work again from the current trunk, as this brief states it.\n", e.RunID)
	if paths := conflictPaths(strings.Split(strings.TrimSpace(e.Detail), "\n")); len(paths) > 0 {
		b.WriteString("\nThe paths that conflicted:\n\n")
		for _, p := range paths {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	fmt.Fprintf(&b, "\nThe discarded candidate is pinned at `refs/tillr/runs/%s`; read it with `git show` if it helps, and do not move the ref.\n", e.RunID)
	b.WriteString("\n" + ObstacleContract + "\n")
	return b.String()
}
