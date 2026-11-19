package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestProcessStepsGateReadsPassInheritedVerdict(t *testing.T) {
	root := t.TempDir()
	storePath := filepath.Join(root, ".tillr", "state.db")
	st, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	runID := "run-inherited-1"
	runDir := RunDir(root, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planBytes := []byte(`{"record_version":1,"kind":"gate.plan","id":"p-inh-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-19T00:00:00Z","payload":{"plan_id":"p-inh-1","checks":[{"check_id":"go-test"},{"check_id":"pack-render"}]}}`)
	payload, err := json.Marshal(map[string]any{
		"verdict_id":      "v-inh-1",
		"plan_id":         "p-inh-1",
		"registry_digest": "sha256:abc",
		"base_sha":        "base123",
		"tree_sha":        "tree456",
		"result":          GatePassedInherited,
		"checks": []map[string]any{
			{"check_id": "go-test", "result": "pass", "duration_ms": 10, "log_ref": "a.log"},
			{"check_id": "pack-render", "result": "inherited", "duration_ms": 20, "log_ref": "b.log"},
		},
		"inherited_failures": []InheritedFailure{
			{CheckID: "pack-render", TipSHA: "tip789", LogRef: "b.log", Output: "pack-render failed on tip"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	envBytes, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          GateVerdictKind,
		ID:            "v-inh-1",
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
		Payload:       payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	verdictFile := filepath.Join(root, "verdict.json")
	if err := os.WriteFile(verdictFile, envBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(root, "plan.json")
	if err := os.WriteFile(planFile, planBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(root, "fake-gate.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncp "+planFile+" "+filepath.Join(runDir, GatePlanRecord)+"\ncp "+verdictFile+" "+filepath.Join(runDir, GateVerdictRecord)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	steps := &ProcessSteps{
		Root:        root,
		Store:       storePath,
		GateProgram: script,
		Now:         func() time.Time { return time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC) },
	}
	out, err := steps.Gate(context.Background(), Candidate{
		ItemID:   "item-1",
		LaneID:   "lane-1",
		RunID:    runID,
		Worktree: root,
		BaseSHA:  "base123",
		HeadSHA:  "head456",
	})
	if err != nil {
		t.Fatalf("Gate failed: %v", err)
	}
	if out.Outcome != GatePassedInherited {
		t.Fatalf("Outcome = %q, want %q", out.Outcome, GatePassedInherited)
	}
	if len(out.InheritedFailures) != 1 || out.InheritedFailures[0].CheckID != "pack-render" || out.InheritedFailures[0].Output != "pack-render failed on tip" {
		t.Fatalf("InheritedFailures = %+v", out.InheritedFailures)
	}
}
