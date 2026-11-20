package drain

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// usageErrorSteps answers "error" with the deterministic detail on every gate, counting them.
type usageErrorSteps struct {
	recordingSteps
	mu    sync.Mutex
	gated int
}

func (s *usageErrorSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gated++
	return GateOutcome{Outcome: GateErrored, ErrorDetail: "flag provided but not defined: -scope-to-change"}, nil
}

// A gate program that refuses its own arguments answers the same on every pass, so the loop
// gates it once, holds the item for the operator with the gate's words as the reason, and
// does not gate it again. Observed 2026-11-19/20: item-request-c04ea25d47 was gated about
// every eight seconds for ten hours on "flag provided but not defined: -scope-to-change".
func TestGateUsageErrorHoldsInsteadOfRegating(t *testing.T) {
	f, l := passFixture(t, "item-a")
	steps := &usageErrorSteps{}
	l.cfg.Steps = steps
	prepareReadyCandidate(t, f, l, "item-a")
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if steps.gated != 1 {
		t.Fatalf("a deterministic gate error was gated %d times, want exactly 1", steps.gated)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, held := faults["item-a"]
		if !held || fault.Reason != StopGateHarnessError || !strings.Contains(fault.Detail, "-scope-to-change") {
			t.Errorf("item-a fault = %+v (held %v), want gate-harness-error naming the gate's words", fault, held)
		}
		var waits int
		if err := r.QueryRow(`SELECT count(*) FROM dispatches WHERE item_id='item-a' AND reason=?`, ReasonGateNoVerdict).Scan(&waits); err != nil {
			return err
		}
		if waits != 0 {
			t.Errorf("recorded %d gate-no-verdict wait(s) for an answer that will not change", waits)
		}
		return nil
	})
}

// The step reads a usage refusal off the gate's stderr, keeps the first lines as the reason,
// and leaves an error with no such words as the ordinary re-gated no-verdict.
func TestProcessStepsGateClassifiesUsageRefusal(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	c := prepareReadyCandidate(t, f, l, "item-a")
	steps := &ProcessSteps{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: l.cfg.Now,
		GateProgram: executable(t, `echo "flag provided but not defined: -scope-to-change" >&2; echo "Usage of tillr-gate:" >&2; exit 4`)}
	got, err := steps.Gate(context.Background(), c)
	if err != nil || got.Outcome != GateErrored || !strings.Contains(got.ErrorDetail, "flag provided but not defined: -scope-to-change") {
		t.Fatalf("usage refusal answered %+v / %v, want an error carrying the gate's first line", got, err)
	}
	steps.GateProgram = executable(t, `echo "the disk is full" >&2; exit 4`)
	got, err = steps.Gate(context.Background(), c)
	if err != nil || got.Outcome != GateErrored || got.ErrorDetail != "" {
		t.Fatalf("a non-usage error answered %+v / %v, want the re-gated no-verdict", got, err)
	}
}

func TestGateBinaryDisagreementNamedWhenBothSidesChange(t *testing.T) {
	both := []string{"tools/gate/change-gate.py", "tools/tillr-gate/run.go"}
	msg := gateBinaryDisagreement(both)
	for _, want := range []string{"tools/gate/", "tools/tillr-gate/", "installed tillr-gate", "land the binary change first"} {
		if !strings.Contains(msg, want) {
			t.Errorf("disagreement %q does not say %q", msg, want)
		}
	}
	if !strings.Contains(gateErrorDetail("flag provided but not defined: -x", both), "installed tillr-gate") {
		t.Error("the hold's detail does not carry the disagreement")
	}
	for _, one := range [][]string{{"tools/gate/change-gate.py"}, {"tools/tillr-gate/run.go"}, nil} {
		if d := gateBinaryDisagreement(one); d != "" {
			t.Errorf("%v named a disagreement: %s", one, d)
		}
	}
}
