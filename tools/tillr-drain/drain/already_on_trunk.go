package drain

import "context"

// LandAlreadyOnTrunk is the lander's refusal for a change the trunk already carries
// (tillr-land's `already-on-trunk`): the squash reproduced the tip's own tree, so nothing
// moved and no commit was written.
const LandAlreadyOnTrunk = "already-on-trunk"

// StateResolvedElsewhere is the item state for work whose change reached the trunk by
// another route. It is one of the item model's finished states in the store’s definition, so the
// item stops blocking its dependents and is not planned again.
const StateResolvedElsewhere = "resolved-elsewhere"

// settleAlreadyOnTrunk is landing principle 10 on the dispatch side: work whose change is
// already on the trunk exits the queue on that finding, never waits for a person. The
// lander found the whole change already there — the owner committed the candidate's
// content (as a dirty-path hold advises), applied it by hand, or this run's own landing
// lost both its receipt and its marker. The gate passed, so the verdict is recorded; the
// item did not land through this candidate, so it is resolved elsewhere rather than
// landed. The caller then marks the work examined and releases the lane, as after a
// landing, and the lander has already pinned the head.
func (l *Loop) settleAlreadyOnTrunk(ctx context.Context, candidate Candidate) error {
	// A hand landing that put the change there may already have moved the item to a
	// terminal state; it keeps that state, and no second verdict is recorded.
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "already-on-trunk"); err != nil {
		return err
	}
	l.reportf("loop: %s is already on the trunk (%s): resolving it elsewhere\n", candidate.ItemID, candidate.HeadSHA)
	if candidate.BlockConflict != nil {
		return l.answerBlock(ctx, candidate)
	}
	if err := l.cfg.Steps.Verdict(ctx, candidate, VerdictPassed); err != nil {
		return err
	}
	// The state write rereads the item in its own transaction (resolveElsewhere), so a
	// hand landing that commits after the check above is never overwritten.
	return l.resolveElsewhere(ctx, candidate)
}
