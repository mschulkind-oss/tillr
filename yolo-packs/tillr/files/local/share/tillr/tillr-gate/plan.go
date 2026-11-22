package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
)

// PlanKind is the record kind the plan face emits. It is a stdout record kind, never an
// event: a plan is a function of a change and a registry, so appending it would claim a
// state transition that does not exist.
const PlanKind = "gate.plan"

// Version is the producer version every record this binary emits carries.
const Version = "0.1.0"

func init() {
	// Registered from the module's own init so a conformance consumer can see the kind
	// without running a face. A kind already owned by another utility is a defect, and
	// there is no way to answer it from an init.
	if err := event.Register(PlanKind, "gate"); err != nil {
		panic(err)
	}
}

// Change is the `gate.change` payload: the change a plan is computed for. `Subject` is
// the candidate or item the change belongs to, and is carried through to the plan's
// envelope rather than into its payload.
type Change struct {
	HeadSHA      string   `json:"head_sha,omitempty"`
	BaseSHA      string   `json:"base_sha"`
	TreeSHA      string   `json:"tree_sha"`
	ChangedPaths []string `json:"changed_paths"`
	Subject      string   `json:"subject,omitempty"`
}

// PlanCheck is one planned step as the plan record carries it.
//
// The design's six fields are the ones a runner needs to execute and serialize the step.
// The rest are what the registry declares and a command list cannot spell -- the
// environment a suite needs, the environment it must not inherit, the checkout it needs,
// its tier, whether it runs alone, and the flag that plans it when no path does. A plan
// that dropped them would attest less than the gate it replaces while binding a digest.
type PlanCheck struct {
	DeclarationDigest string `json:"declaration_digest,omitempty"`

	Scope           string            `json:"scope,omitempty"`
	BudgetMS        int               `json:"budget_ms,omitempty"`
	Tools           []string          `json:"tools,omitempty"`
	Remember        *bool             `json:"remember,omitempty"`
	CheckID         string            `json:"check_id"`
	Command         []string          `json:"command"`
	TimeoutMS       int               `json:"timeout_ms"`
	Reads           []string          `json:"reads"`
	Writes          []string          `json:"writes"`
	DependsOn       []string          `json:"depends_on"`
	Kind            string            `json:"kind"`
	Label           string            `json:"label"`
	Env             map[string]string `json:"env"`
	Drop            []string          `json:"drop"`
	Needs           []string          `json:"needs"`
	Tier            string            `json:"tier"`
	Exclusive       bool              `json:"exclusive"`
	ExclusiveReason string            `json:"exclusive_reason"`
	Forced          string            `json:"forced"`

	// Observation sources are supplied by the runner, never serialized in a plan.
	watchdog       *watchSources
	measureLanding bool
}

// PlanInProcess is one in-process check the plan owes: a check the gate runs as its own
// code, which a plan of commands cannot spell. It is carried so `run` can refuse the plan
// rather than silently omit the check -- a verdict that dropped a check would assert a
// transition that did not happen (the next-steps ruling's A-INPROCESS).
//
// An in-process check that selects nothing is owed by every change, the way a `forced`
// declaration is: the gate's own-code checks are not a function of the diff.
type PlanInProcess struct {
	CheckID string `json:"check_id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Tier    string `json:"tier,omitempty"`
}

// PlanPayload is the `gate.plan` payload.
type PlanPayload struct {
	PlanID         string          `json:"plan_id"`
	BaseSHA        string          `json:"base_sha"`
	TreeSHA        string          `json:"tree_sha"`
	RegistryDigest string          `json:"registry_digest"`
	Checks         []PlanCheck     `json:"checks"`
	InProcess      []PlanInProcess `json:"in_process"`
}

// Plan computes the plan record for one change against one registry.
//
// It is a pure function of `(registry, change, root)`: the same three produce the same
// bytes, so a plan can be compared and a digest can bind it. `plan_id` is derived from
// the change and the registry digest rather than from a clock or a counter, which is what
// makes a replayed plan recognisable; `gate.plan` carries no timestamp of its own, so
// there is no clock to inject.
//
// `root` is the worktree the change is against. A declaration's `must_exist` pattern reads
// it, so a plan computed against the wrong root silently selects nothing.
func Plan(reg *Registry, change Change, root string) (record.Envelope, error) {
	checks := reg.Plan(change.ChangedPaths, root)
	planned := make([]PlanCheck, 0, len(checks))
	for _, check := range checks {
		if check.Scope == "change" {
			if change.HeadSHA == "" {
				return record.Envelope{}, externalf("change check %q requires head_sha in gate.change; derive a new change record", check.ID)
			}
			check.Command = append(check.Command, change.BaseSHA, change.HeadSHA)
		}
		planned = append(planned, PlanCheck{
			DeclarationDigest: check.DeclarationDigest,

			Scope:           check.Scope,
			BudgetMS:        check.BudgetMS,
			Tools:           check.Tools,
			Remember:        check.Remember,
			CheckID:         check.ID,
			Command:         check.Command,
			TimeoutMS:       check.TimeoutMS,
			Reads:           check.Reads,
			Writes:          check.Writes,
			DependsOn:       check.DependsOn,
			Kind:            check.Kind,
			Label:           check.Label,
			Env:             check.Env,
			Drop:            check.Drop,
			Needs:           check.Needs,
			Tier:            check.Tier,
			Exclusive:       check.Exclusive,
			ExclusiveReason: check.ExclusiveReason,
			Forced:          check.Forced,
		})
	}
	payload := PlanPayload{
		PlanID:         planID(reg.Digest, change),
		BaseSHA:        change.BaseSHA,
		TreeSHA:        change.TreeSHA,
		RegistryDigest: reg.Digest,
		Checks:         planned,
		InProcess:      owedInProcess(reg, change.ChangedPaths, root),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, internalf("encoding the plan payload: %v", err)
	}
	envelope := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          PlanKind,
		ID:            payload.PlanID,
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: Version},
		Payload:       raw,
	}
	if change.Subject != "" {
		envelope.Subject = &record.Subject{Kind: "candidate", ID: change.Subject}
	}
	return envelope, nil
}

// owedInProcess is the in-process checks a change owes: every one that selects nothing,
// and every one a changed path selects. They are named in the plan because a plan that
// dropped them would attest less than the gate it replaces while binding a digest, and
// because `run` has no other way to know it cannot honour the plan.
func owedInProcess(reg *Registry, changed []string, root string) []PlanInProcess {
	owed := make([]PlanInProcess, 0, len(reg.InProcess))
	for _, check := range reg.InProcess {
		if len(check.Selects) > 0 && len(selectedPaths(check.Selects, changed, root)) == 0 {
			continue
		}
		owed = append(owed, PlanInProcess{CheckID: check.ID, Kind: check.Kind, Label: check.Label, Tier: check.Tier})
	}
	return owed
}

// planID is the plan's identity: a hash of the change and the registry digest, so two
// plans of one change against one registry are one plan and a replayed plan is
// recognisable. The changed paths are hashed in the order the change names them, because
// that order is part of the change.
func planID(registryDigest string, change Change) string {
	body := strings.Join([]string{
		registryDigest,
		change.BaseSHA,
		change.HeadSHA,
		change.TreeSHA,
		strings.Join(change.ChangedPaths, "\x00"),
	}, "\x00")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// decodeChange reads a `gate.change` record and returns its payload.
func decodeChange(raw []byte) (Change, error) {
	envelope, err := record.Decode(raw)
	if err != nil {
		return Change{}, usagef("the input is not a tillr record: %v", err)
	}
	if envelope.Kind != ChangeKind {
		return Change{}, usagef("the input is a %q record, not a gate.change", envelope.Kind)
	}
	var change Change
	if err := json.Unmarshal(envelope.Payload, &change); err != nil {
		return Change{}, usagef("decoding the gate.change payload: %v", err)
	}
	if change.BaseSHA == "" || change.TreeSHA == "" {
		return Change{}, usagef("the gate.change names no base_sha or no tree_sha")
	}
	if change.ChangedPaths == nil {
		return Change{}, usagef("the gate.change names no changed_paths")
	}
	return change, nil
}

// encodePlan writes the plan record as the bytes the face emits.
func encodePlan(envelope record.Envelope) ([]byte, error) {
	raw, err := record.Encode(envelope)
	if err != nil {
		return nil, internalf("encoding the plan record: %v", err)
	}
	return append(raw, '\n'), nil
}
