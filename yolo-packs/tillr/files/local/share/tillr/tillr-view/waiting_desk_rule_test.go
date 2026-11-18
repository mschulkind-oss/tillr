package main

import "testing"

func TestWaitingDeskHoldsExplainWhatFreesTheDesk(t *testing.T) {
	for _, tc := range []struct {
		reason string
		mover  string
	}{
		{"waits on a desk", "a design desk to free up"},
		{"waits on the document's desk", "the document's desk to finish its current item"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			rule := waitingRuleFor(tc.reason)
			if rule.class != "waits on work" || rule.where != "desk" || rule.mover != tc.mover {
				t.Fatalf("unexplained desk hold: %+v", rule)
			}
		})
	}
}
