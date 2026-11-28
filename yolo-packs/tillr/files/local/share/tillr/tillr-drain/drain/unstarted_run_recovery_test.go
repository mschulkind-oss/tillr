package drain_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A run face can exit before starting its worker or writing a terminal run row.
// Repairing its adapter and clearing the recorded fault must offer that item again;
// retry cannot authorize an attempt that never started, and must name the real mover.
func TestUnstartedRunIsOfferedAgainAfterRepairAndClearFault(t *testing.T) {
	const itemID = "item_041"
	adapter := stub(t, "adapter", refusingAdapter)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecord(itemID, "in-queue"))
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	pauseKind := filepath.Join(filepath.Dir(adapter), "pause-kind")
	if err := os.Remove(pauseKind); err != nil {
		t.Fatalf("removing the adapter's pause-kind declaration: %v", err)
	}

	// The deadline is a hang guard, not a time limit: the loop reads any cancelled context,
	// its deadline included, as an operator stop, so a short deadline under load ends a
	// passing run as "operator-stop".
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	firstCtx, stop := context.WithCancel(ctx)
	defer stop()
	report := &stoppingReport{cancelOn: "loop: " + itemID + " mover:", cancel: stop}
	first, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot}, report).Run(firstCtx)
	if err != nil || first.Stop != drain.StopOperatorStop || first.Dispatched != 1 {
		t.Fatalf("first loop did not stop after recording the fault: %+v (%v)\n%s", first, err, report)
	}
	fault := faultOf(t, f.store, itemID)
	if fault.Seq == 0 || fault.RunID == "" || !strings.Contains(fault.Detail, "reading adapter pause-kind") {
		t.Fatalf("missing fault for the run face's pause-kind failure: %+v", fault)
	}
	spans := stepSpans(t, f.root, fault.RunID)
	if len(spans) != 1 || spans[0].Step != "run" || spans[0].ExitCode == nil || *spans[0].ExitCode != 2 || !strings.Contains(spans[0].StderrTail, "reading adapter pause-kind") {
		t.Fatalf("first exec did not exit 2 reading adapter pause-kind: %+v", spans)
	}
	if countMatching(lines(t, f.count), adapter) != 0 {
		t.Fatalf("worker started despite the missing pause-kind declaration: %v", lines(t, f.count))
	}
	s, err := store.OpenRead(ctx, f.store)
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var runs int
		if err := r.QueryRow(`SELECT count(*) FROM runs WHERE item_id = ?`, itemID).Scan(&runs); err != nil {
			return err
		}
		if runs != 0 {
			t.Errorf("unstarted worker left %d run records, want none", runs)
		}
		var leased bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes WHERE run_id = ?)`, fault.RunID).Scan(&leased); err != nil {
			return err
		}
		if leased {
			t.Error("the refused run still holds a lane while its fault is active")
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Obtain both refusals before another loop can create a real run with this id.
	beforeCode, beforeOut := authorizeRetry(t, f.store, itemID, fault.RunID,
		"operator", "adapter declaration repaired", steps.Now().Format(time.RFC3339))
	writeFile(t, pauseKind, run.PauseWhereItStands+"\n")
	if code, out := invoke(t, "clear-fault", "--store", f.store, "--item", itemID, "--fault", fmt.Sprint(fault.Seq)); code != 0 {
		t.Fatalf("clearing active fault %d: exit %d: %s", fault.Seq, code, out)
	}
	if remaining := faultOf(t, f.store, itemID); remaining.Seq != 0 {
		t.Fatalf("active fault remained after clear-fault: %+v", remaining)
	}
	afterCode, afterOut := authorizeRetry(t, f.store, itemID, fault.RunID,
		"operator", "adapter declaration repaired", steps.Now().Format(time.RFC3339))
	t.Logf("retry before clear-fault: exit %d: %s", beforeCode, beforeOut)
	t.Logf("retry after clear-fault, before next loop: exit %d: %s", afterCode, afterOut)

	// Check eligibility before the replacement loop can reap the old claim.
	// Clearing the fault must be sufficient; neither a lease expiry nor a restart
	// should be needed to make the item dispatchable.
	s, err = store.OpenRead(ctx, f.store)
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		lanes := []string{laneName(1)}
		answer, err := drain.Why(r, drain.Pass{
			Root: f.root, StorePath: f.store, AvailableLanes: &lanes,
			Now: steps.Now().Format(time.RFC3339),
		}, f.policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == itemID {
				if row.Decision.Action != drain.ActionDispatch || !strings.Contains(row.Mover, "next pass") {
					t.Errorf("after clear-fault, why = %s; mover: %s; action: %s; want dispatch on the next pass", row.Why, row.Mover, row.Decision.Action)
				}
				return nil
			}
		}
		t.Error("why omitted the item after clear-fault")
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Assert actual redispatch before checking diagnostic wording: a useful retry
	// refusal is not evidence that the repaired item can really start again.
	second, err := f.newLoop(t, 1, steps).Run(ctx)
	adapterRuns := countMatching(lines(t, f.count), adapter)
	if err != nil || second.Stop != drain.StopQueueDrained || second.Dispatched != 1 || adapterRuns != 1 {
		t.Fatalf("repaired unstarted item was not redispatched: %+v (%v), adapter invocations=%d: %v", second, err, adapterRuns, lines(t, f.count))
	}
	if next := faultOf(t, f.store, itemID); next.Reason != drain.ReasonAdapterRefused || next.RunID != fault.RunID {
		t.Errorf("the pre-worker failure must not consume a run ID: first = %s; next fault = %+v", fault.RunID, next)
	}
	if beforeCode != 2 || !strings.Contains(string(beforeOut), fault.ClearCommand(f.store)) {
		t.Errorf("retry before clear-fault must refuse and name the active fault command %q: exit %d: %s", fault.ClearCommand(f.store), beforeCode, beforeOut)
	}
	if afterCode != 2 || strings.Contains(string(afterOut), "clear-fault") || !strings.Contains(strings.ToLower(string(afterOut)), "next pass") {
		t.Errorf("retry after clear-fault must refuse, omit the stale fault command, and name the next pass: exit %d: %s", afterCode, afterOut)
	}
}
