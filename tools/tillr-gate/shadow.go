package main

// shadow.go is the shadow face: a candidate's landing tier run and measured beside the
// gate that decides, and recorded without anything landing.
//
// The plan already carries a tier per check, and the tier says when the check is owed:
// the landing tier is the proof a batch must carry
// on the tree the trunk will publish, and the rolling tier is the proof that follows on a
// clock. Until something runs the split, that field is a declaration no measurement has
// ever been compared against. This face is the measurement: it runs the plan's landing
// checks in the tree, records each check's tier and the wall clock its tier cost, and
// writes a `gate.shadow` record.
//
// It is a measurement, not a verdict. It moves no ref, writes no store and emits no
// `gate.verdict`, so a caller may run a candidate through the landing tier as often as it
// likes; two records over one candidate are two measurements of one plan, and the only
// difference between them is the measurement itself.
//
// The in-process checks the plan owes (`file-size`, `scratch`, `design-citations`) are the
// gate's own code, which this face cannot run. It names their tier and leaves them
// unmeasured rather than dropping them, so the landing wall it reports is a floor and the
// record says which checks it covers. That is the same honesty `run` states as an `error`
// (the next-steps ruling's A-INPROCESS); the difference is that a measurement may name
// what it could not measure, where a verdict may not omit it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
)

// ShadowKind is the record kind the shadow face emits. Like the plan and the verdict it is
// a stdout record kind, never an event: a shadow is a function of a plan, a tree and a
// run, so appending it would claim a state transition that does not exist.
const ShadowKind = "gate.shadow"

func init() {
	if err := event.Register(ShadowKind, "gate"); err != nil {
		panic(err)
	}
}

// The two tiers the design names (§4), and the safe side for a check that declares
// neither. An unnamed tier is the landing tier for the same reason the registry's audit
// gives: the worst a missing entry can do is make a landing slower, never quieter.
const (
	TierLanding = "landing"
	TierRolling = "rolling"
)

// tierOf is a check's tier. A declaration that names neither tier -- an in-process check
// the registry does not tier, an older registry -- is the landing tier.
func tierOf(tier string) string {
	if tier == TierRolling {
		return TierRolling
	}
	return TierLanding
}

// ShadowCheck is one planned check as the shadow record carries it: the tier it belongs
// to, whether this run measured it, and the measurement when it did. A check the run did
// not measure carries `ran: false` and no result, duration or log: it was deferred to the
// other tier, which is not the same as a check the change did not select.
type ShadowCheck struct {
	CheckID    string `json:"check_id"`
	Kind       string `json:"kind"`
	Tier       string `json:"tier"`
	Ran        bool   `json:"ran"`
	Result     string `json:"result,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	LogRef     string `json:"log_ref,omitempty"`
}

// ShadowInProcess is one in-process check the plan owes: the gate's own code, which this
// face cannot run. It is named with its tier so the landing tally can count it, and left
// unmeasured so the record never claims a wall it did not spend.
type ShadowInProcess struct {
	CheckID string `json:"check_id"`
	Kind    string `json:"kind"`
	Tier    string `json:"tier"`
}

// ShadowTier is one tier's share of the plan: how many planned checks it owns, how many
// in-process checks it owns, how many this run measured, and the wall clock those
// measurements cost. The face runs checks serially, in the plan's own order, so the wall
// is the sum of the measured durations -- the wall the tier would have cost on its own.
type ShadowTier struct {
	Checks    int   `json:"checks"`
	InProcess int   `json:"in_process"`
	Ran       int   `json:"ran"`
	WallMS    int64 `json:"wall_ms"`
}

// ShadowPayload is the `gate.shadow` payload.
//
// It binds what a verdict binds -- the plan, the registry and the tree -- because a
// measurement nobody can tie back to the tree it measured is a number, not evidence. It
// carries no clock: like the plan, a shadow is a function of its inputs, and a record that
// stamped the moment it ran would make two records of one candidate differ in something
// other than the measurement.
type ShadowPayload struct {
	ShadowID       string            `json:"shadow_id"`
	PlanID         string            `json:"plan_id"`
	RegistryDigest string            `json:"registry_digest"`
	BaseSHA        string            `json:"base_sha"`
	TreeSHA        string            `json:"tree_sha"`
	Tier           string            `json:"tier"`
	Result         string            `json:"result"`
	Landing        ShadowTier        `json:"landing"`
	Rolling        ShadowTier        `json:"rolling"`
	Checks         []ShadowCheck     `json:"checks"`
	InProcess      []ShadowInProcess `json:"in_process"`
}

// tier answers the aggregate for one tier, so the counting and the wall have one
// spelling each.
func (payload *ShadowPayload) tier(tier string) *ShadowTier {
	if tierOf(tier) == TierRolling {
		return &payload.Rolling
	}
	return &payload.Landing
}

// shadowID is the record's identity: a hash of the plan it measured, the tree it ran in
// and the tier it measured. It is derived rather than counted or clocked, so two shadow
// runs over one candidate are one identity, and a caller can name the log directory
// before the run starts.
func shadowID(planID, treeSHA, tier string) string {
	sum := sha256.Sum256([]byte(planID + "\x00" + treeSHA + "\x00" + tier))
	return hex.EncodeToString(sum[:])
}

// Shadow runs a plan's landing tier in a worktree and returns the shadow record.
//
// The landing tier is every planned check whose tier is `landing`; a rolling-tier check is
// recorded as deferred and not run, which is what makes the record a statement about the
// tree rather than about what the run felt like doing. The tree is the worktree the checks
// run in and the plan names, exactly as `run` takes it.
//
// The returned error is the one a refused or unreadable plan answers with. A check that
// did not pass is not an error: the face measured it, and the record carries its result.
// A check that could not start is `error` in the record, and the run still answers 0 --
// the product is the record, and a shadow red is a measurement, never a verdict.
func Shadow(planRecord record.Envelope, worktree, logDir string) (record.Envelope, error) {
	plan, root, logs, err := prepareRun(planRecord, worktree, logDir)
	if err != nil {
		return record.Envelope{}, err
	}
	payload := ShadowPayload{
		ShadowID:       shadowID(plan.PlanID, plan.TreeSHA, TierLanding),
		PlanID:         plan.PlanID,
		RegistryDigest: plan.RegistryDigest,
		BaseSHA:        plan.BaseSHA,
		TreeSHA:        plan.TreeSHA,
		Tier:           TierLanding,
		Checks:         make([]ShadowCheck, 0, len(plan.Checks)),
		InProcess:      make([]ShadowInProcess, 0, len(plan.InProcess)),
	}

	// The in-process checks are tallied first: they are the integrity set, they are the
	// landing tier's, and the face cannot measure them, so the landing wall must never be
	// read as the whole tier.
	for _, check := range plan.InProcess {
		tier := tierOf(check.Tier)
		payload.InProcess = append(payload.InProcess, ShadowInProcess{
			CheckID: check.CheckID, Kind: check.Kind, Tier: tier,
		})
		payload.tier(tier).InProcess++
	}

	measured := 0
	for _, check := range plan.Checks {
		if tierOf(check.Tier) == TierLanding {
			measured++
		}
	}
	// The log directory is made only once something will write to it: a plan whose
	// landing tier is empty leaves no directory behind.
	if measured > 0 {
		if err := os.MkdirAll(logs, 0o755); err != nil {
			return record.Envelope{}, internalf("creating the log directory %q: %v", logs, err)
		}
	}

	for index, check := range plan.Checks {
		tier := tierOf(check.Tier)
		entry := ShadowCheck{CheckID: check.CheckID, Kind: check.Kind, Tier: tier}
		aggregate := payload.tier(tier)
		aggregate.Checks++
		if tier == payload.Tier {
			outcome := runCheck(check, root, logs, index)
			entry.Ran = true
			entry.Result = outcome.Result
			entry.DurationMS = outcome.DurationMS
			entry.LogRef = outcome.LogRef
			aggregate.Ran++
			aggregate.WallMS += outcome.DurationMS
		}
		payload.Checks = append(payload.Checks, entry)
	}
	payload.Result = shadowResult(payload.Checks)

	raw, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, internalf("encoding the shadow payload: %v", err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          ShadowKind,
		ID:            payload.ShadowID,
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: Version},
		Subject:       planRecord.Subject,
		CorrelationID: planRecord.CorrelationID,
		Payload:       raw,
	}, nil
}

// shadowResult is the measured tier's aggregate result: `error` if any measured check
// could not be run, else `fail` if any failed, else `pass`. An error wins over a fail for
// the same reason it does in `run`: a check that was not run judged nothing, and a red
// from a run with a hole in it is not the run's verdict.
func shadowResult(checks []ShadowCheck) string {
	classified := make([]checkresult.Result, 0, len(checks))
	for _, check := range checks {
		if !check.Ran {
			continue
		}
		classified = append(classified, classifyCheck(checkresult.Observation{
			Check:  check.CheckID,
			Source: checkresult.ResultWord,
			Word:   check.Result,
		}))
	}
	return aggregateChecks(checkresult.ShadowOrder, classified).Detail
}

// encodeShadow writes the shadow record as the bytes the face emits.
func encodeShadow(envelope record.Envelope) ([]byte, error) {
	raw, err := record.Encode(envelope)
	if err != nil {
		return nil, internalf("encoding the shadow record: %v", err)
	}
	return append(raw, '\n'), nil
}
