package main_test

import (
	"encoding/json"
	"testing"
)

func TestRequestFaceRecordsSeatWorkflowAndNode(t *testing.T) {
	dir := scratch(t, "request-seat")
	adapter, _, _ := recordingAdapter(t, dir)
	policy := policyFile(t, dir, adapter)

	code, out := run(t, "request", "--policy", policy, "--backend", "fixture",
		"--item", "item_042", "--lane", "lane_1", "--run", "run_seat_042",
		"--seat", "edit", "--workflow", "work", "--node", "work/piece-run-record",
		"--now", "2026-11-20T00:00:00Z")
	if code != 0 {
		t.Fatalf("request exited %d: %s", code, out)
	}
	var env struct {
		Payload struct {
			Seat     string `json:"seat"`
			Workflow string `json:"workflow"`
			Node     string `json:"node"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal request: %v\n%s", err, out)
	}
	if env.Payload.Seat != "edit" || env.Payload.Workflow != "work" || env.Payload.Node != "work/piece-run-record" {
		t.Fatalf("request payload = %+v, want seat=edit workflow=work node=work/piece-run-record", env.Payload)
	}
}
