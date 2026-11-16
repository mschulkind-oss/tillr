package main

import "testing"

func TestWaitingLandingRefusalHasALandingRule(t *testing.T) {
	rule := waitingRuleFor("land-refused")
	if rule.class == "unclassified" || rule.where != "land" {
		t.Fatalf("landing refusal is not classified at landing: %+v", rule)
	}
}
