package drain

import "testing"

func TestLandingCallAnswerChoice(t *testing.T) {
	for _, tc := range []struct{ outcome, kind, stage, close string }{
		{"fence-refused", "refused", "fence", ""},
		{"judge-refused", "refused", "judge", ""},
		{"gate-refused", "refused", "gate", ""},
		{"conflict", "conflict", "", ""},
		{"reverted", "reverted", "", ""},
		{"identical-repeat", "design-answer", "", ""},
		{"landed", "", "", ""},
		{"proven", "", "", "proven"},
		{"superseded", "", "", "superseded"},
		{"withdrawn", "", "", "withdrawn"},
		{"trunk-taken-in", "", "", ""},
		{"check-incomplete", "", "", ""},
		{"gate-stalled", "", "", ""},
		{"trunk-dirty", "", "", ""},
		{"red-cap", "", "", ""},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			got := chooseLandingCallAnswer(tc.outcome)
			if got.Kind != tc.kind || got.Stage != tc.stage || got.Close != tc.close {
				t.Fatalf("choice = %+v, want kind=%q stage=%q close=%q", got, tc.kind, tc.stage, tc.close)
			}
		})
	}
}
