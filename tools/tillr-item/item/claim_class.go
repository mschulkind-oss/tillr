package item

// Claim classes are stored on criteria and constrain every verdict writer.
const (
	// ClaimVerification is a claim whose truth is settled by applying a named predicate
	// to recorded evidence, with no value choice left to make.
	ClaimVerification = "verification-only"
	// ClaimRedGateTriage is waiving or landing past a red gate: §13's triage sign-off.
	ClaimRedGateTriage = "red-gate-triage"
	// ClaimDirection is choosing between two defensible directions.
	ClaimDirection = "direction"
	// ClaimOwnerOnly is every class §28.1 reserves to the owner.
	ClaimOwnerOnly = "owner-only"
)

var claimClasses = map[string]bool{
	ClaimVerification: true, ClaimRedGateTriage: true, ClaimDirection: true, ClaimOwnerOnly: true,
}

// validateClaimClass checks new writes only; historical payloads remain readable.
func validateClaimClass(class, id string) error {
	if class == "qa-verification" {
		return Rejectionf("actor-retired", id,
			"claim class %q is retired: write %q for verification claims instead", class, ClaimVerification)
	}
	if !claimClasses[class] {
		return Usagef("%q is not a claim class: want one of %s, %s, %s, %s",
			class, ClaimVerification, ClaimRedGateTriage, ClaimDirection, ClaimOwnerOnly)
	}
	return nil
}
