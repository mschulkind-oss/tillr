package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestMergedInheritedSpendReplacesCumulativeMeasurement(t *testing.T) {
	_, l := passFixture(t, "item-a")
	l.sessionID = "new-loop"
	var total LoopResult
	for _, cost := range []float64{.06, .09} {
		local := &LoopResult{}
		l.countSettledSpend(local, spendRecord("inherited", "pi", count(1500), dollars(cost)), "old-loop")
		mergeExamination(&total, local)
	}
	if total.CarriedSpend.CostUSD != .09 || total.CarriedSpend.Runs != 1 || total.SpendUSD != .09 {
		t.Fatalf("cumulative spend counted twice: %+v", total)
	}
}

func TestSnapshotRecoversCarriedSpendBeforeCompletionIsConsumed(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.sessionID = "replacement"
	rec := run.Record{RunID: "inherited", ItemID: "item-a", LaneID: "lane-1", Backend: "pi", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), Usage: &run.Usage{Tokens: count(1500), CostUSD: dollars(.03)}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	if err := l.recordSettlementSpend(context.Background(), "pass-2", "earlier", rec); err != nil {
		t.Fatal(err)
	}
	// No completion has been merged: shutdown must recover durable accounting.
	var result LoopResult
	if err := l.snapshotSpend(&result); err != nil {
		t.Fatal(err)
	}
	if result.CarriedSpend.CostUSD != .03 || result.CarriedSpend.Runs != 1 || result.StartedSpend.Runs != 0 {
		t.Fatalf("shutdown lost carried spend: %+v", result)
	}
}
