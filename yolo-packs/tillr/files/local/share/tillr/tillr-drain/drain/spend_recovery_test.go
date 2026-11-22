package drain

import "testing"

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
