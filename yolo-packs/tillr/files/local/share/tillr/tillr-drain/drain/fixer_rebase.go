package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// fixerRebaseConflict is a trunk fixer whose pre-landing rebase onto the trunk tip
// conflicted. The rebase is already aborted and the lane is back on the fixer's own head
// when this is answered, so nothing downstream finds the lane mid-rebase.
//
// On 2026-11-18 (build 4f1ac49ab) the loop answered this as an external fault and exited
// with lane-1 left mid-rebase: the next start could not preserve its unmerged index, and
// the operator had to run `git rebase --abort` by hand.
type fixerRebaseConflict struct {
	ItemID, Tip string
	// Reports are the conflicted paths spelled as git's CONFLICT lines, the form a
	// landing conflict carries (LandOutcome.ConflictPaths), so the two key alike.
	Reports []string
	Cause   error
}

func (e *fixerRebaseConflict) Error() string {
	return fmt.Sprintf("rebasing the fixer %s onto %s failed: %v", e.ItemID, shortSHA(e.Tip), e.Cause)
}

func asFixerRebaseConflict(err error) *fixerRebaseConflict {
	var c *fixerRebaseConflict
	if errors.As(err, &c) {
		return c
	}
	return nil
}

// abortFixerRebase reads the conflicted paths of a failed fixer rebase, then aborts it and
// puts the lane back on the fixer's head in the same step. A rebase that fails for any
// reason is aborted: the loop never leaves a lane mid-rebase.
func (l *Loop) abortFixerRebase(ctx context.Context, candidate Candidate, tip string, cause error) error {
	unmerged, _ := l.git(ctx, candidate.Worktree, "diff", "--name-only", "--diff-filter=U")
	reports := []string{}
	for _, line := range strings.Split(unmerged, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			reports = append(reports, "CONFLICT (content): Merge conflict in "+path)
		}
	}
	_, _ = l.git(ctx, candidate.Worktree, "rebase", "--abort")
	if candidate.HeadSHA != "" {
		if _, err := l.git(ctx, candidate.Worktree, "reset", "-q", "--hard", candidate.HeadSHA); err != nil {
			return Externalf("restoring fixer %s's lane after a failed rebase: %v (rebase: %v)", candidate.ItemID, err, cause)
		}
	}
	return &fixerRebaseConflict{ItemID: candidate.ItemID, Tip: tip, Reports: reports, Cause: cause}
}

// fixerRebaseConflicted answers a fixer whose pre-landing rebase conflicted. The first
// question is whether the fixer is still needed: when every check it names already passes
// on the trunk tip (the tip judgment redOnTip makes), another commit repaired the trunk, so
// the fixer is resolved elsewhere with a proof event, which releases its waiters, and the
// run settles as one whose item is terminal. Otherwise the conflict is a landing conflict
// like any other (conflictFix): it goes back to its producer with the paths, on the latest
// trunk, uncounted, and an identical repeat is the stop.
func (l *Loop) fixerRebaseConflicted(ctx context.Context, candidate Candidate, conflict *fixerRebaseConflict, result *LoopResult, timing *PassTiming) (string, bool, error) {
	l.reportf("loop: the fixer %s does not rebase onto %s (%s); the rebase is aborted\n",
		candidate.ItemID, shortSHA(conflict.Tip), strings.Join(conflictPaths(conflict.Reports), ", "))
	checks, err := l.fixerNamedChecks(ctx, candidate.ItemID)
	if err != nil {
		return "", false, err
	}
	if len(checks) > 0 {
		replay, ok := l.cfg.Steps.(ReplayStep)
		if !ok {
			replay = unknownReplay{}
		}
		red, known, err := l.redOnTip(ctx, replay, candidate, conflict.Tip, checks)
		if err != nil {
			return "", false, err
		}
		if known && len(red) == 0 {
			if err := l.resolveStaleFixer(ctx, candidate.ItemID, conflict.Tip, checks); err != nil {
				return "", false, err
			}
			return "", false, l.checkItemTerminal(ctx, candidate.ItemID, "fixer rebase")
		}
	}
	return l.conflictFix(ctx, candidate, LandOutcome{Result: LandSquashConflict, ConflictPaths: conflict.Reports}, result, timing)
}

// unknownReplay is the replay a Steps without the capability answers with: it judges
// nothing, so only a recorded pass can show the tip green.
type unknownReplay struct{}

func (unknownReplay) Replay(context.Context, Candidate, string, []string) (ReplayOutcome, error) {
	return ReplayOutcome{}, nil
}

// fixerNamedChecks answers the checks a fixer's trunk-green criterion names.
func (l *Loop) fixerNamedChecks(ctx context.Context, fixer string) ([]string, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var text string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		err := r.QueryRow(`SELECT text FROM item_criteria WHERE item_id=? AND criterion_id='trunk-green'`, fixer).Scan(&text)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return fixerChecks(text), nil
}

// resolveStaleFixer moves a fixer whose named checks pass on tip to resolved-elsewhere and
// records the proof event the trunk acquittals are released by (repairedTrunkWaiter).
func (l *Loop) resolveStaleFixer(ctx context.Context, fixer, tip string, checks []string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	reason := "every check the fixer names (" + strings.Join(checks, ", ") + ") passes on the trunk tip " + tip + ": another commit repaired the trunk, and the fixer's own change no longer rebases onto it"
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		lifecycle, err := definition.ForReader(tx.Reader())
		if err != nil {
			return err
		}
		var state string
		if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, fixer).Scan(&state); err != nil {
			return err
		}
		if finishedState(lifecycle.Parsed, state) {
			return nil
		}
		if _, err := item.SetStateWithReceipt(ctx, tx, fixer, StateResolvedElsewhere, at, item.MoveReceipt{Reason: reason, By: Utility, ResolvesVia: "commit:" + tip}); err != nil {
			return err
		}
		return appendEffect(tx, FixerProvedKind, fixer, fixerProof{FixerItem: fixer, Green: tip, ProvedAt: at.Format("2006-01-02T15:04:05.000Z"), Checks: checks}, at)
	})
	if err == nil {
		l.reportf("loop: %s resolved: %s\n", fixer, reason)
	}
	return err
}
