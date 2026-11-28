package drain

import (
	"context"
	"slices"

	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
)

// refuse records one candidate's refusal: the step that made it, and the loop's own
// aggregate of how many candidates it has refused without landing one.
func (l *Loop) refuse(ctx context.Context, candidate Candidate, result *LoopResult, step string) error {
	// A refusal holds an item in the queue; a terminal item is not refused or counted.
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "refusal"); err != nil {
		return err
	}
	l.mu.Lock()
	result.Rejected++
	if result.RefusedSteps == nil {
		result.RefusedSteps = map[string]int{}
	}
	result.RefusedSteps[step]++
	if !slices.Contains(result.RefusedItems, candidate.ItemID) {
		result.RefusedItems = append(result.RefusedItems, candidate.ItemID)
	}
	var line string
	if result.Landed == 0 && len(result.RefusedItems) >= RepeatedRefusalThreshold {
		result.RepeatedRefusal = repeatedRefusal(result)
		line = RepeatedRefusalLine(*result)
	}
	l.mu.Unlock()
	if line != "" {
		l.reportf("loop: repeated-refusal: %s\n", line)
	}
	switch step {
	case RefusalLand, RefusalDeliveryOrder, RefusalGate, RefusalVerifyNotDone, RefusalPlanWrong, RefusalFence, RefusalNoOp, RefusalNodeOutput, RefusalReportMissing, RefusalPlanOutput, RefusalWordsMissing, RefusalWordsAltered, RefusalIdeaNoOutcome:
		return l.recordRefusal(ctx, candidate, step)
	}
	return nil
}

// finalizeRepeatedRefusal sets or clears the report from the run's final totals.
func (l *Loop) finalizeRepeatedRefusal(result *LoopResult) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if result.Landed > 0 || len(result.RefusedItems) < RepeatedRefusalThreshold {
		result.RepeatedRefusal = nil
		return
	}
	result.RepeatedRefusal = repeatedRefusal(result)
}

// repeatedRefusal is the report's payload, built from the loop's own totals.
func repeatedRefusal(result *LoopResult) *RepeatedRefusal {
	return &RepeatedRefusal{
		Candidates: result.Candidates,
		Rejected:   result.Rejected,
		Steps:      result.RefusedSteps,
		Items:      result.RefusedItems,
		Ticks:      result.Ticks,
		Tokens:     result.Tokens,
		SpendUSD:   result.SpendUSD,
	}
}

// RepeatedRefusalLine is the repeated-refusal report as one line: the blast radius the
// loop measured, in the words an operator reads at exit. It answers "" when the run is
// not a repeated refusal.
func RepeatedRefusalLine(result LoopResult) string {
	return runhistory.FormatRepeatedRefusalLine(result.RepeatedRefusal)
}
