package checkresult

import "testing"

func TestClassifyBudgetNote(t *testing.T) {
	cases := []struct {
		word    string
		outcome Outcome
		detail  string
	}{
		{"over-budget", Unknown, DetailOverBudget},
		{"stalled", Unknown, DetailStalled},
		{"STALLED", Unknown, DetailStalled},
		{"error", Unknown, DetailError},
		{"pass", Pass, DetailPassed},
		{"fail", Fail, DetailFailed},
		{"disabled", Disabled, DetailDisabled},
		{"", Unknown, DetailError},
	}
	for _, tc := range cases {
		t.Run("budget/"+tc.word, func(t *testing.T) {
			got := Classify(Observation{Source: BudgetNote, Word: tc.word})
			assertResult(t, got, Result{Outcome: tc.outcome, Detail: tc.detail, RawWord: tc.word})
		})
	}
}
