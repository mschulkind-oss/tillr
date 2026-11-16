package record

// The exit-code contract. The distinction that matters most to an agent caller is
// between a negative decision (the utility ran and the answer is no) and no decision
// (the utility could not answer).
const (
	// ExitOK: completed; where the face makes a decision, the decision is positive.
	ExitOK = 0
	// ExitNegative: completed with a negative decision; the record says why.
	ExitNegative = 1
	// ExitUsage: usage error or malformed/unsupported input record.
	ExitUsage = 2
	// ExitIncompatible: store, schema or record version incompatible, or the store
	// stayed locked past the caller's bound.
	ExitIncompatible = 3
	// ExitExternal: an external dependency prevented a decision, distinct from a
	// failed check.
	ExitExternal = 4
	// ExitInternal: a bug, not an outcome; any write transaction is rolled back.
	ExitInternal = 5
)
