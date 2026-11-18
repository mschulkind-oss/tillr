package drain

// landingCallChoice separates an answer from a silent closing. An empty choice
// leaves the call open: machinery holds are not judgments of the offer.
type landingCallChoice struct {
	Kind  string
	Stage string
	Close string
}

// chooseLandingCallAnswer has no effects; callers persist its choice separately.
func chooseLandingCallAnswer(outcome string) landingCallChoice {
	switch outcome {
	case "fence-refused":
		return landingCallChoice{Kind: "refused", Stage: "fence"}
	case "judge-refused":
		return landingCallChoice{Kind: "refused", Stage: "judge"}
	case "gate-refused":
		return landingCallChoice{Kind: "refused", Stage: "gate"}
	case "conflict", "reverted":
		return landingCallChoice{Kind: outcome}
	case "identical-repeat":
		return landingCallChoice{Kind: "design-answer"}
	case "proven", "superseded", "withdrawn":
		return landingCallChoice{Close: outcome}
	default:
		return landingCallChoice{}
	}
}
