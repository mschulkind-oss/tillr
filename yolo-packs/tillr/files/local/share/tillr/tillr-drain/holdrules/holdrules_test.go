package holdrules

import (
	"strings"
	"testing"
)

// Every hold names who or what clears it: a mover that is empty or says the cause is
// not recorded leaves a person nothing to act on.
func TestNoRuleLeavesTheMoverUnnamed(t *testing.T) {
	rules := map[string]Rule{"Unknown": Unknown}
	for reason, rule := range Rules {
		rules[reason] = rule
	}
	for reason, rule := range rules {
		if strings.TrimSpace(rule.Mover) == "" || strings.Contains(rule.Mover, "cause not recorded") {
			t.Errorf("%q has mover %q", reason, rule.Mover)
		}
	}
}

func TestRunLinkMissingDetailResolvesToItsRule(t *testing.T) {
	got := For("run-link-missing: run run-x-a1 has no finish record (state running)")
	if got != Rules["run-link-missing"] {
		t.Fatalf("the detailed reason resolves to %+v, want the run-link-missing rule", got)
	}
}

func TestIceboxDetailResolvesToItsRule(t *testing.T) {
	got := For(`icebox: "AutoQA" in docs/tillr/roadmap.md`)
	if got != Rules["icebox"] {
		t.Fatalf("the detailed reason resolves to %+v, want the icebox rule", got)
	}
}
