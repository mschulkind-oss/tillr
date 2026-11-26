package drainmodel

// LandingCallChoice separates an answer from a silent closing. An empty choice
// leaves the call open: machinery holds are not judgments of the offer.
type LandingCallChoice struct {
	Kind  string
	Stage string
	Close string
}

// ChooseLandingCallAnswer has no effects; callers persist its choice separately.
func ChooseLandingCallAnswer(outcome string) LandingCallChoice {
	switch outcome {
	case "fence-refused":
		return LandingCallChoice{Kind: "refused", Stage: "fence"}
	case "judge-refused":
		return LandingCallChoice{Kind: "refused", Stage: "judge"}
	case "gate-refused":
		return LandingCallChoice{Kind: "refused", Stage: "gate"}
	case RefusalDeliveryOrder:
		return LandingCallChoice{Kind: "refused", Stage: RefusalDeliveryOrder}
	case "conflict", "reverted":
		return LandingCallChoice{Kind: outcome}
	case "identical-repeat":
		return LandingCallChoice{Kind: "design-answer"}
	case "proven", "superseded", "withdrawn":
		return LandingCallChoice{Close: outcome}
	default:
		return LandingCallChoice{}
	}
}
