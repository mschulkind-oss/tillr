package main

import "testing"

func TestWaitingToolingDefectNamesRepair(t *testing.T) {
	rule := waitingRuleFor("tooling-defect")
	if rule.class != "waits on work" || rule.where != "queue" || rule.mover != "the tooling defect item to land or close" {
		t.Fatalf("unexplained tooling defect hold: %+v", rule)
	}
}

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
