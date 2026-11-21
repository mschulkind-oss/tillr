package drain

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A design-seat dispatch (conflictDecision) carries no RunInputs. Its dispatch fact still
// names the build that started it.
func TestADesignDispatchRecordsItsBuild(t *testing.T) {
	f, l := designQuotaFixture(t)
	p := mustPlan(t, l)[0]
	if p.decision.Action != ActionDispatch || p.decision.Reason != ReasonConflictDesign || p.decision.Inputs != nil {
		t.Fatalf("fixture is not an inputs-free design dispatch: %+v", p.decision)
	}
	if ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim=%v/%v", ok, err)
	}
	hash, _ := l.build.Hash()
	fixtureRead(t, f, func(r *store.Reader) error {
		facts, err := readInputDecisions(r)
		if err != nil {
			return err
		}
		fact := facts[p.decision.RunRequest.RunID]
		if fact.Inputs == nil || fact.Inputs.TillrBuild != hash || fact.BlockConflict == nil {
			t.Errorf("design dispatch fact %+v, want build %s and its conflict", fact, hash)
		}
		return nil
	})
}

// declarePauseKind points the fixture backend at an adapter whose directory declares kind,
// as each real adapter's pause-kind file does; "" declares nothing.
func declarePauseKind(t *testing.T, l *Loop, kind string) {
	t.Helper()
	dir := scratchUnderDurable(t)
	if kind != "" {
		writeForkFile(t, filepath.Join(dir, "pause-kind"), kind+"\n")
	}
	b := l.cfg.Policy.Backends["fixture"]
	b.Adapter = filepath.Join(dir, "fixture")
	l.cfg.Policy.Backends["fixture"] = b
}

func TestResumeRecordsNewBuildWithoutChangingOtherDispatchInputs(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	p := mustPlan(t, h.l)[0]
	ok, _, err := h.l.applyPlan(context.Background(), p, runGit(t, h.f.root, "rev-parse", TrunkRef), h.l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	h.l.build.Revision = "replacement"
	if err := h.l.recordResumedBuild(context.Background(), *p.decision.RunRequest); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, h.f, func(r *store.Reader) error {
		facts, err := readInputDecisions(r)
		if err != nil {
			return err
		}
		got := *facts[p.decision.RunRequest.RunID].Inputs
		want := *p.decision.Inputs
		want.TillrBuild, _ = h.l.build.Hash()
		if got != want {
			t.Errorf("resume inputs=%+v, want %+v", got, want)
		}
		return nil
	})
}
