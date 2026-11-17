package item

import "regexp"

var ownerCriterion = regexp.MustCompile(`(?i)^owner decision recorded\b`)

// checkClaimMove enforces the authority order from tillr-judge-and-lifecycle.md:
// triage and direction are incomparable; only the owner may supersede either.
func checkClaimMove(itemID string, held, next criterion) error {
	if held.ClaimClass == next.ClaimClass || held.ClaimClass == ClaimVerification ||
		next.ClaimClass == ClaimOwnerOnly && (held.ClaimClass == ClaimDirection || held.ClaimClass == ClaimRedGateTriage) {
		return nil
	}
	return Rejectionf("claim-class-weakened", itemID,
		"criterion %q cannot change claim class from %q to %q: authority may only become stricter",
		next.CriterionID, held.ClaimClass, next.ClaimClass)
}

// checkClaimMoves is also used by sync's dry-run planner, before definition freezes.
func checkClaimMoves(itemID string, held, next []criterion) error {
	byID := make(map[string]criterion, len(held))
	for _, c := range held {
		byID[c.CriterionID] = c
	}
	for _, c := range next {
		if old, ok := byID[c.CriterionID]; ok {
			if err := checkClaimMove(itemID, old, c); err != nil {
				return err
			}
			delete(byID, c.CriterionID)
		}
	}
	// Removing an unjudged authority-bearing criterion would let a caller
	// recreate it with the default class and bypass the monotonic rule.
	for _, c := range held {
		if _, omitted := byID[c.CriterionID]; omitted && c.ClaimClass != ClaimVerification {
			return Rejectionf("claim-class-weakened", itemID,
				"criterion %q with class %q cannot be omitted: keep its stored authority", c.CriterionID, c.ClaimClass)
		}
	}
	return nil
}
