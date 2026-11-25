package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// startFailingSteps answers every run with the error ProcessSteps.Run gives when the run
// face exits 5 (SQLITE_BUSY at its lane claim) before it printed a record.
type startFailingSteps struct{ recordingSteps }

func (s *startFailingSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	return run.Record{}, "", &runStartError{
		ExitError: Externalf("tillr-run exec could not run %s: exit status 5: tillr-run: store: beginning the write transaction: database is locked (5) (SQLITE_BUSY)", req.RunID).(*ExitError),
		RunID:     req.RunID,
	}
}

// A run whose face cannot start is that run's failure: the pass finishes, the item is held
// with the cause and the run named, and the loop stays up.
func TestARunThatCannotStartHoldsItsItemAndNotTheLoop(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &startFailingSteps{}, &itemContentStager{})
	holdWALKeeper(t, f.storePath)
	base := commitBlock(t, f.root, "item-a", "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	result := &LoopResult{}
	d := f.decisions()[0]
	l.active[d.RunRequest.RunID] = true
	l.launchRun(context.Background(), d, l.cfg.Now(), nil, result)
	c := <-l.completions
	if c.step != "run" || c.outcome.err == nil {
		t.Fatalf("the start failure did not reach the completion: %+v", c)
	}
	stop, err := l.takeCompletion(c, result)
	if err != nil || stop != "" || result.StopItemID != "" {
		t.Fatalf("one run's start failure stopped the loop: stop=%q err=%v result=%+v", stop, err, result)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		fault, ok := mustFaults(t, r)["item-a"]
		if !ok || fault.Reason != ReasonSettleFailed || fault.RunID != d.RunRequest.RunID || !strings.Contains(fault.Detail, "SQLITE_BUSY") {
			t.Fatalf("the run's failure is not recorded with its cause: %+v", fault)
		}
		return nil
	})
}

// An older loop recorded the setup refusal without retracting its dispatch.
// Clearing that existing fault restores the pending workflow node too.
func TestClearFaultRetractsAnExistingExecSetupRefusal(t *testing.T) {
	f, l, _, d := node1Claim(t)
	id := d.RunRequest.RunID
	failing := ItemFault{ItemID: d.ItemID, RunID: id, Reason: ReasonSettleFailed,
		Detail: "run: tillr-run exec could not run " + id + ": exit status 2: tillr-run: reading adapter pause-kind: no such file or directory"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return l.recordItemFaultTx(tx, failing)
	})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return ClearItemFault(tx, d.ItemID, mustFaults(t, tx.Reader())[d.ItemID].Seq, l.cfg.Now())
	})
	assertClaimRetracted(t, f, d)
	if got := decisionFor(t, l, d.ItemID).decision; got.Action != ActionDispatch || got.RunRequest == nil || got.RunRequest.RunID != id {
		t.Fatalf("the pending node was not offered again: %+v", got)
	}
}

func mustFaults(t *testing.T, r *store.Reader) map[string]ItemFault {
	t.Helper()
	faults, err := ReadItemFaults(r)
	if err != nil {
		t.Fatal(err)
	}
	return faults
}

// A loop that ends on an error says so in the store's events, with the reason; a clean stop
// does not.
func TestALoopThatEndsOnAnErrorRecordsItsEnd(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	holdWALKeeper(t, f.storePath)
	l.recordLoopEnded(LoopResult{Stop: StopOperatorStop, Detail: "asked to stop"})
	l.recordLoopEnded(LoopResult{Stop: StopFault, Detail: "tillr-run exec could not run run-x: exit status 5", ExitCode: 5})
	var ended []loopEndedForTest
	fixtureRead(t, f, func(r *store.Reader) error {
		evs, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, e := range evs {
			if e.Kind == loopEndedKind {
				ended = append(ended, loopEndedForTest{e})
			}
		}
		return nil
	})
	if len(ended) != 1 || !strings.Contains(string(ended[0].Payload), "exit status 5") || !strings.Contains(string(ended[0].Payload), `"stop":"fault"`) {
		t.Fatalf("loop-ended events: %+v", ended)
	}
}

type loopEndedForTest struct{ event.Event }
