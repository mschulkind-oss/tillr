package land

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

func TestDecodeVerdictAcceptsPassInherited(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"verdict_id":      "v-inherited-1",
		"plan_id":         "p-1",
		"registry_digest": "sha256:abc",
		"base_sha":        "base123",
		"tree_sha":        "tree456",
		"result":          VerdictPassInherited,
		"checks": []map[string]any{
			{"check_id": "go-test", "result": "pass", "duration_ms": 10, "log_ref": "a.log"},
			{"check_id": "pack-render", "result": "inherited", "duration_ms": 20, "log_ref": "b.log"},
		},
		"inherited_failures": []map[string]any{
			{
				"check_id": "pack-render",
				"tip_sha":  "tip789",
				"log_ref":  "b.log",
				"output":   "pack-render failed on trunk tip",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          VerdictKind,
		ID:            "v-inherited-1",
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
		Payload:       payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := DecodeVerdict(raw)
	if err != nil {
		t.Fatalf("DecodeVerdict failed for %s: %v", VerdictPassInherited, err)
	}
	if v.Result != VerdictPassInherited {
		t.Fatalf("Result = %q, want %q", v.Result, VerdictPassInherited)
	}
	if len(v.InheritedFailures) != 1 {
		t.Fatalf("InheritedFailures = %+v, want 1 entry", v.InheritedFailures)
	}
	if v.InheritedFailures[0].CheckID != "pack-render" || v.InheritedFailures[0].Output != "pack-render failed on trunk tip" || v.InheritedFailures[0].TipSHA != "tip789" {
		t.Fatalf("InheritedFailures[0] = %+v", v.InheritedFailures[0])
	}
}

func TestDecideAcceptsPassInheritedAndBindsTreeAndBase(t *testing.T) {
	base := "base123"
	tree := "tree456"
	v := Verdict{
		VerdictID: "v-inh",
		BaseSHA:   base,
		TreeSHA:   tree,
		Result:    VerdictPassInherited,
		InheritedFailures: []InheritedFailure{
			{CheckID: "pack-render", TipSHA: "tip789", Output: "failed on tip"},
		},
	}

	res, reason := decide(LandingRequest{BaseRef: "main", BaseSHA: base, Verdict: v}, tree)
	if res != ResultLanded || reason != "" {
		t.Fatalf("decide(matching) = (%q, %q), want (%q, \"\")", res, reason, ResultLanded)
	}

	res, reason = decide(LandingRequest{BaseRef: "main", BaseSHA: base, Verdict: v}, "other-tree")
	if res != ResultRefused || reason != ReasonVerdictMismatch {
		t.Fatalf("decide(tree mismatch) = (%q, %q), want (%q, %q)", res, reason, ResultRefused, ReasonVerdictMismatch)
	}

	res, reason = decide(LandingRequest{BaseRef: "main", BaseSHA: "other-base", Verdict: v}, tree)
	if res != ResultRefused || reason != ReasonVerdictMismatch {
		t.Fatalf("decide(base mismatch) = (%q, %q), want (%q, %q)", res, reason, ResultRefused, ReasonVerdictMismatch)
	}

	// Patch ref still requires fork-build pass.
	res, reason = decide(LandingRequest{BaseRef: "refs/heads/patch/pi", BaseSHA: base, Verdict: v}, tree)
	if res != ResultRefused || reason != ReasonVerdictMismatch {
		t.Fatalf("decide(patch without fork-build) = (%q, %q), want (%q, %q)", res, reason, ResultRefused, ReasonVerdictMismatch)
	}

	vWithForkBuild := v
	vWithForkBuild.Checks = []VerdictCheck{{CheckID: "fork-build", Result: VerdictPass}}
	res, reason = decide(LandingRequest{BaseRef: "refs/heads/patch/pi", BaseSHA: base, Verdict: vWithForkBuild}, tree)
	if res != ResultLanded || reason != "" {
		t.Fatalf("decide(patch with fork-build) = (%q, %q), want (%q, \"\")", res, reason, ResultLanded)
	}
}
