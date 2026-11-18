package drain

import (
	"context"
	"github.com/mschulkind-oss/tillr/store"
	"testing"
)

func TestMessageRewriteKeepsLostHistoryHold(t *testing.T) {
	f, l, steps := identityFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state='blocked'`); return err })
	base := runGit(t, f.root, "rev-parse", "HEAD")
	bad := foreignCommit(t, f.root, base, false)
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	edited := runGit(t, f.root, "commit-tree", runGit(t, f.root, "rev-parse", bad+"^{tree}"), "-p", base, "-m", "edited message")
	runGit(t, f.root, "reset", "--hard", edited)
	var result LoopResult
	historyPass(t, l, &result)
	h := rewriteHistory(t, f)
	if h.Lost != bad || h.Observed != edited || steps.lands != 0 {
		t.Fatalf("message edit waived history: %+v", h)
	}
}

type identityRacingSteps struct {
	dirtyPathSteps
	advanced bool
}

func (s *identityRacingSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if !s.advanced {
		s.advanced = true
		foreignCommit(s.t, s.root, runGit(s.t, s.root, "rev-parse", "HEAD"), false)
		return LandOutcome{Result: LandRefused, Reason: ReasonForeignIdentity}, nil
	}
	return s.dirtyPathSteps.Land(ctx, c)
}
func TestLockedIdentityRefusalRetainsCandidateWithoutConvictingIt(t *testing.T) {
	f, l, baseSteps := identityFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='blocked' WHERE item_id='item-b'`)
		return err
	})
	steps := &identityRacingSteps{dirtyPathSteps: *baseSteps}
	l.cfg.Steps = steps
	base := runGit(t, f.root, "rev-parse", "HEAD")
	historyCandidate(t, f, l, base)
	var result LoopResult
	historyPass(t, l, &result)
	if result.Rejected != 0 || result.Landed != 0 || steps.verdicts != 0 {
		t.Fatalf("race convicted candidate: %+v", result)
	}
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionHold || len(d.Reason) < len(ReasonForeignIdentity) || d.Reason[:len(ReasonForeignIdentity)] != ReasonForeignIdentity {
		t.Fatalf("lock refusal not held: %+v", d)
	}
}
