package drain

import "context"

// LandAlreadyOnTrunk is the lander's refusal for a change the trunk already carries
// (tillr-land's `already-on-trunk`): the squash reproduced the tip's own tree, so nothing
// moved and no commit was written.
const LandAlreadyOnTrunk = "already-on-trunk"

// StateResolvedElsewhere is the item state for work whose change reached the trunk by
// another route. It is one of the item model's terminal states (terminalStates), so the
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
	l.reportf("loop: %s is already on the trunk (%s): resolving it elsewhere\n", candidate.ItemID, candidate.HeadSHA)
	if candidate.BlockConflict != nil {
		return l.answerBlock(ctx, candidate)
	}
	if err := l.cfg.Steps.Verdict(ctx, candidate, VerdictPassed); err != nil {
		return err
	}
	return l.cfg.Steps.ItemState(ctx, candidate.ItemID, candidate.RunID, StateResolvedElsewhere)
}
