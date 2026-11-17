package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestParallelReaderContinuationUpdatesSpendWithoutChargingSiblings(t *testing.T) {
	f, l, _ := parallelFixture(t)
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	id := attemptRunID("item-a", 1) + "-n2"
	tokens, cost := int64(30), 0.03
	rec := run.Record{RunID: id, Usage: &run.Usage{Tokens: &tokens, CostUSD: &cost}}
	if err := l.recordSettlementSpend(context.Background(), "continued-pass", l.sessionID, rec); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var rows int
		var total int64
		if err := tx.QueryRow(`SELECT COUNT(*),SUM(usage_tokens) FROM drain_pass_spend`).Scan(&rows, &total); err != nil {
			return err
		}
		if rows != 4 || total != 60 {
			t.Fatalf("continued cumulative usage lost or siblings charged twice: rows=%d tokens=%d", rows, total)
		}
		return nil
	})
}

func TestParallelReaderCrossLoopContinuationKeepsShutdownSpend(t *testing.T) {
	f, l, _ := parallelFixture(t)
	l.sessionID = "original-loop"
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	l.sessionID = "continuation-loop"
	id := attemptRunID("item-a", 1) + "-n2"
	tokens, cost := int64(30), 0.03
	rec := run.Record{RunID: id, Usage: &run.Usage{Tokens: &tokens, CostUSD: &cost}}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE runs SET usage_tokens=?,usage_cost_usd=? WHERE run_id=?`, tokens, cost, id)
		return err
	})
	if err := l.recordSettlementSpend(context.Background(), "continued-pass", "original-loop", rec); err != nil {
		t.Fatal(err)
	}
	// Re-examining a finished sibling must not attribute it to this new loop.
	tokens, cost = 10, 0.01
	rec.RunID = attemptRunID("item-a", 1) + "-n3"
	if err := l.recordSettlementSpend(context.Background(), "continued-pass", "original-loop", rec); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var snapshot LoopResult
		if err := l.snapshotSpend(&snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.StartedSpend.Runs != 0 || snapshot.CarriedSpend.Runs != 1 || snapshot.Tokens != 30 {
			t.Fatalf("continued usage omitted or unchanged siblings charged again: %+v", snapshot)
		}
	}
}

func TestParallelReadersShutdownSnapshotCountsEverySiblingOnce(t *testing.T) {
	_, l, steps := parallelFixture(t)
	l.sessionID = "parallel-spend-loop"
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.started != 4 || result.Tokens != 40 {
		t.Fatalf("before snapshot: readers=%d tokens=%d", steps.started, result.Tokens)
	}
	for i := 0; i < 2; i++ {
		if err := l.snapshotSpend(result); err != nil {
			t.Fatal(err)
		}
		if result.StartedSpend.Tokens != 40 || result.StartedSpend.Runs != 4 || result.CarriedSpend.Runs != 0 {
			t.Fatalf("snapshot lost or repeated siblings: %+v", result)
		}
	}
}
