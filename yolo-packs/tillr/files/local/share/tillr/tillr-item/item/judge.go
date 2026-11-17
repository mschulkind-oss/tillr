package item

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// JudgeOptions is caller-owned provenance. Head and Tree must be resolved from the
// candidate in Git, never copied from the output being checked.
type JudgeOptions struct {
	ItemID, Head, Tree, Actor, JudgedActor, GrantID, Landed string
	At                                                      time.Time
}

// JudgeOutput is the written verdict file defined by tillr-judge-and-lifecycle.md
// Section 2.1. WaitingOn is retained for the caller's blocked-state routing; intake
// records criteria only and never changes an item's disposition.
type JudgeOutput struct {
	EvaluatedHead string       `json:"evaluated_head"`
	EvaluatedTree string       `json:"evaluated_tree"`
	Outcome       string       `json:"outcome"`
	Verdicts      []JudgeEntry `json:"verdicts"`
}
type JudgeEntry struct {
	CriterionID   string          `json:"criterion_id"`
	CriterionHash string          `json:"criterion_hash"`
	Verdict       string          `json:"verdict"`
	ClaimClass    string          `json:"claim_class"`
	Reason        string          `json:"reason"`
	Evidence      []string        `json:"evidence"`
	WaitingOn     json.RawMessage `json:"waiting_on,omitempty"`
}

// JudgeResult is a checked intake, including the aggregate outcome and envelopes
// ready for RecordVerdict. Validation and recording share the same transaction
// when writing; dry-run calls CheckJudge with a read-only transaction instead.
type JudgeResult struct {
	Outcome string
	Output  JudgeOutput
	Records []record.Envelope
}

// CheckJudge validates without writing. Every stored criterion must occur exactly
// once; provenance is supplied only by the caller. RecordVerdict remains the writer
// of each criterion, and its binding and authority checks are also applied here.
func CheckJudge(ctx context.Context, r *store.Reader, raw []byte, o JudgeOptions) (JudgeResult, error) {
	reject := func(rule, reason string) (JudgeResult, error) {
		return JudgeResult{}, Rejectionf(rule, o.ItemID, "%s", reason)
	}
	if strings.TrimSpace(o.ItemID) == "" || strings.TrimSpace(o.Actor) == "" || strings.TrimSpace(o.JudgedActor) == "" || o.Head == "" || o.Tree == "" || o.At.IsZero() {
		return JudgeResult{}, Usagef("judge needs an item, resolved head and tree, actor, judged actor and clock")
	}
	// Scan keys before decoding typed fields: even null/empty provenance is forbidden,
	// including a field nested in an entry. Duplicate keys cannot overwrite a refusal.
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := judgeJSON(dec, o.ItemID); err != nil {
		return JudgeResult{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return reject("judge-output", "expected exactly one JSON object")
	}
	var output JudgeOutput
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&output); err != nil {
		return reject("judge-output", "reading verdict file: "+err.Error())
	}
	if output.EvaluatedHead != o.Head {
		return reject("evaluated-head", "evaluated_head differs from the candidate head")
	}
	if output.EvaluatedTree != o.Tree {
		return reject("evaluated-tree", "evaluated_tree differs from the candidate tree")
	}
	if output.Outcome != "pass" && output.Outcome != "fail" {
		return reject("judge-outcome", "outcome must be pass or fail")
	}
	exists, err := itemExists(ctx, r, o.ItemID)
	if err != nil {
		return JudgeResult{}, err
	}
	if !exists {
		return reject("not-found", "no such item in the store")
	}
	criteria, err := readCriteria(r, o.ItemID)
	if err != nil {
		return JudgeResult{}, err
	}
	held := map[string]criterion{}
	for _, c := range criteria {
		held[c.CriterionID] = c
	}
	result := JudgeResult{Outcome: "pass", Output: output}
	seen := map[string]bool{}
	for _, entry := range output.Verdicts {
		c, ok := held[entry.CriterionID]
		if !ok {
			return reject("criterion-unknown", "unknown criterion "+entry.CriterionID)
		}
		if seen[entry.CriterionID] {
			return reject("criterion-duplicate", "duplicate criterion "+entry.CriterionID)
		}
		seen[entry.CriterionID] = true
		if entry.CriterionHash != CriterionHash(c.Text) {
			return reject("criterion-hash", "criterion hash differs for "+entry.CriterionID)
		}
		if entry.ClaimClass != c.ClaimClass {
			return reject("claim-class-mismatch", "claim class differs for "+entry.CriterionID)
		}
		if !verdictValues[entry.Verdict] {
			return reject("verdict-value", "unknown verdict for "+entry.CriterionID)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return reject("verdict-reason", "empty reason for "+entry.CriterionID)
		}
		if len(entry.Evidence) == 0 {
			return reject("verdict-evidence", "no evidence for "+entry.CriterionID)
		}
		for _, pointer := range entry.Evidence {
			if strings.TrimSpace(pointer) == "" || len(strings.Fields(pointer)) != 1 {
				return reject("verdict-evidence", "evidence must contain non-empty locators, not prose, for "+entry.CriterionID)
			}
		}
		if output.Outcome == "pass" && entry.Verdict != VerdictDone {
			return reject("outcome-pass", "pass requires every criterion to be done")
		}
		switch entry.Verdict {
		case VerdictDefective:
			result.Outcome = VerdictDefective
		case VerdictBlocked:
			if result.Outcome != VerdictDefective {
				result.Outcome = VerdictBlocked
			}
		case VerdictNotDone:
			if result.Outcome == "pass" {
				result.Outcome = VerdictNotDone
			}
		}
		v := verdictPayload{ItemID: o.ItemID, CriterionID: entry.CriterionID, CriterionHash: entry.CriterionHash, Verdict: entry.Verdict, ClaimClass: entry.ClaimClass, Reason: entry.Reason, Evidence: append([]string(nil), entry.Evidence...), EvaluatedHead: o.Head, ActorKind: ActorAgent, Actor: o.Actor, JudgedActor: o.JudgedActor, GrantID: o.GrantID}
		if entry.Verdict == VerdictDone && o.Landed != "" {
			v.Landed = []string{o.Landed}
			v.Evidence = append(v.Evidence, "commit:"+o.Landed)
		}
		env, err := envelopeOf(VerdictRecordKind, "verdict-"+o.ItemID+"-"+entry.CriterionID, o.At, v)
		if err != nil {
			return JudgeResult{}, err
		}
		if _, err := decodeVerdict(env); err != nil {
			return JudgeResult{}, err
		}
		if v.ClaimClass == ClaimOwnerOnly {
			return reject("claim-class-actor", "an agent cannot judge owner-only criteria")
		}
		if err := checkIndependence(v); err != nil {
			return JudgeResult{}, err
		}
		if err := checkDelegation(ctx, r, v, o.At); err != nil {
			return JudgeResult{}, err
		}
		result.Records = append(result.Records, env)
	}
	for _, c := range criteria {
		if !seen[c.CriterionID] {
			return reject("criterion-missing", "missing criterion "+c.CriterionID)
		}
	}
	if len(criteria) == 0 {
		return reject("criterion-missing", "item carries no criteria")
	}
	if output.Outcome == "fail" && result.Outcome == "pass" {
		return reject("judge-outcome", "fail requires a non-done verdict")
	}
	return result, nil
}

// RecordJudge checks and records the entire file in the caller's transaction. The
// caller must roll back on error, just as with RecordVerdict; no partial file counts.
func RecordJudge(ctx context.Context, tx *store.Tx, raw []byte, o JudgeOptions) (JudgeResult, error) {
	result, err := CheckJudge(ctx, tx.Reader(), raw, o)
	if err != nil {
		return JudgeResult{}, err
	}
	for i, env := range result.Records {
		stored, err := RecordVerdict(ctx, tx, env)
		if err != nil {
			return JudgeResult{}, err
		}
		result.Records[i] = stored
	}
	return result, nil
}

func judgeJSON(dec *json.Decoder, id string) error {
	token, err := dec.Token()
	if err != nil {
		return Rejectionf("judge-output", id, "reading JSON: %v", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return Rejectionf("judge-output", id, "reading JSON key: %v", err)
			}
			name, ok := key.(string)
			if !ok {
				return Rejectionf("judge-output", id, "expected a JSON key")
			}
			if name == "actor" || name == "judged_actor" {
				return Rejectionf("actor-in-output", id, "%s is caller-owned and must not appear in judge output", name)
			}
			if seen[name] {
				return Rejectionf("judge-output", id, "duplicate JSON key %q", name)
			}
			seen[name] = true
			if err := judgeJSON(dec, id); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := judgeJSON(dec, id); err != nil {
				return err
			}
		}
	default:
		return Rejectionf("judge-output", id, "unexpected JSON delimiter")
	}
	if _, err := dec.Token(); err != nil {
		return Rejectionf("judge-output", id, "reading JSON end: %v", err)
	}
	return nil
}
