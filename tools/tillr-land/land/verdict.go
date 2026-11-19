package land

import (
	"encoding/json"

	"github.com/mschulkind-oss/tillr/record"
)

// The four verdict results design §5.7 and §8.2 give a gate.verdict.
const (
	// VerdictPass is a verdict that ran every planned check and all of them passed.
	VerdictPass = "pass"
	// VerdictPassInherited is a second passing verdict (design §8.2, OQ-GP7): the
	// candidate's only failing checks also fail on the trunk tip, each recorded in
	// InheritedFailures with its output.
	VerdictPassInherited = "pass-inherited"
	// VerdictFail is a completed negative verdict: a check ran and failed.
	VerdictFail = "fail"
	// VerdictError is a verdict that could not be completed. §5.7 is explicit that it
	// is "never to be read as a failure of the candidate", which is why land refuses
	// it rather than isolating the candidate on it.
	VerdictError = "error"
)

// InheritedFailure records one check that failed on both the candidate and the trunk tip,
// with the tip commit and output that acquitted the candidate.
type InheritedFailure struct {
	CheckID string `json:"check_id"`
	TipSHA  string `json:"tip_sha,omitempty"`
	LogRef  string `json:"log_ref,omitempty"`
	Output  string `json:"output,omitempty"`
}

// Verdict is the gate.verdict payload: design §5.7's field list, plus §8.2's
// inherited_failures when Result is pass-inherited.
//
// This type is Assumption A-VERDICT written as code. §5.7 lists the fields in prose and
// no Go type for them exists anywhere in the tree, so land's fixture is the specimen
// and GATE-3's conformance case must match it. A field §5.7 does not name is a design
// amendment, not an addition here.
type Verdict struct {
	VerdictID         string             `json:"verdict_id"`
	PlanID            string             `json:"plan_id"`
	RegistryDigest    string             `json:"registry_digest"`
	BaseSHA           string             `json:"base_sha"`
	TreeSHA           string             `json:"tree_sha"`
	Result            string             `json:"result"`
	Checks            []VerdictCheck     `json:"checks"`
	InheritedFailures []InheritedFailure `json:"inherited_failures,omitempty"`
}

// VerdictCheck is one check's outcome as the verdict carries it.
type VerdictCheck struct {
	CheckID    string `json:"check_id"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	LogRef     string `json:"log_ref"`
}

// DecodeVerdict reads a gate.verdict record and answers its payload. A record of
// another kind, or one whose payload this utility cannot describe, is malformed input:
// exit 2, no decision, nothing written.
func DecodeVerdict(raw []byte) (Verdict, error) {
	env, err := record.Decode(raw)
	if err != nil {
		return Verdict{}, Usagef("%v", err)
	}
	if env.Kind != VerdictKind {
		return Verdict{}, Usagef("check and pass consume %s, not %q", VerdictKind, env.Kind)
	}
	var v Verdict
	if err := json.Unmarshal(env.Payload, &v); err != nil {
		return Verdict{}, Usagef("reading the %s payload: %v", VerdictKind, err)
	}
	if err := checkVerdict(v); err != nil {
		return Verdict{}, err
	}
	return v, nil
}

// checkVerdict refuses a verdict the evaluation cannot reason about. It checks the
// fields the evaluation reads, not the whole §5.7 list: a verdict that binds the
// candidate and states a result is one land can decide on, and refusing it for a field
// land never reads would make land the arbiter of a shape it does not own.
func checkVerdict(v Verdict) error {
	if v.VerdictID == "" {
		return Usagef("a %s needs a verdict_id", VerdictKind)
	}
	if v.TreeSHA == "" || v.BaseSHA == "" {
		return Usagef("a %s needs a tree_sha and a base_sha: a verdict that binds no tree binds no candidate", VerdictKind)
	}
	switch v.Result {
	case VerdictPass, VerdictPassInherited, VerdictFail, VerdictError:
	default:
		return Usagef("%q is not a verdict result: want one of %s, %s, %s, %s",
			v.Result, VerdictPass, VerdictPassInherited, VerdictFail, VerdictError)
	}
	return nil
}
