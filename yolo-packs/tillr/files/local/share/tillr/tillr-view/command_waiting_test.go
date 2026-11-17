package main

import "testing"

func TestWaitingCommandNodeFailureNamesNextMover(t *testing.T) {
	rule := waitingRuleFor("node-failed: check")
	if rule.class != "needs you" || rule.where != "lane" || rule.mover != "inspect the named command node and authorize a retry" {
		t.Fatalf("rule %+v", rule)
	}
}
