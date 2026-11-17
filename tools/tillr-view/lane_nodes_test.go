package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLaneWorkflowCountsAttemptsAndSpansAllRecordedNodes(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE node_runs(item_id TEXT,attempt INTEGER,node_id TEXT,run_id TEXT,outcome TEXT); UPDATE runs SET item_id='item-seed-04' WHERE run_id='run-1'; INSERT INTO node_runs VALUES('item-seed-04',1,'z-scout','run-1','passed'),('item-seed-04',1,'a-build','run-2','running')`)
	for _, fixture := range []struct{ id, body string }{
		{"run-1", "{\"step\":\"stage\",\"started_at_ms\":500,\"finished_at_ms\":800}\n{\"step\":\"graph\",\"started_at_ms\":1000,\"finished_at_ms\":2000}\n"},
		{"run-2", "{\"step\":\"graph\",\"started_at_ms\":3000,\"finished_at_ms\":null}\n"},
	} {
		dir := filepath.Join(h.loopDir, fixture.id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pipeline.jsonl"), []byte(fixture.body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	status, _, raw := h.get("/api/v1/lanes")
	var answer struct {
		Data []laneRow `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatal(err)
	}
	if status != 200 || len(answer.Data) != 1 {
		t.Fatalf("lanes: %d %s", status, raw)
	}
	lane := answer.Data[0]
	if lane.Attempt != 1 || len(lane.Nodes) != 2 || lane.Nodes[0].NodeID != "z-scout" || lane.Nodes[1].State != "running" {
		t.Fatalf("nodes: %+v", lane)
	}
	var graph pipelineStep
	for _, step := range lane.Pipeline {
		if step.Step == "graph" {
			graph = step
		}
	}
	if graph.StartedAtMS != 1000 || graph.FinishedAtMS != nil {
		t.Fatalf("graph did not span nodes: %+v", graph)
	}
	if err := os.WriteFile(filepath.Join(h.loopDir, "run-2", "pipeline.jsonl"), []byte("{\"step\":\"graph\",\"started_at_ms\":3000,\"finished_at_ms\":5000}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, raw = h.get("/api/v1/lanes")
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatal(err)
	}
	for _, step := range answer.Data[0].Pipeline {
		if step.Step == "graph" && (step.StartedAtMS != 1000 || step.FinishedAtMS == nil || *step.FinishedAtMS != 5000) {
			t.Fatalf("finished span: %+v", step)
		}
	}
}

func TestTranscriptIdentitySurvivesChronologicalMerge(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog(`{"type":"message_end","message":{"role":"assistant","timestamp":2000,"content":[{"type":"text","text":"original"}]}}`)
	before := h.obs().transcript(h.run(), transcriptQuery{})
	if len(before.Entries) != 1 || before.Entries[0].ID == "" {
		t.Fatalf("identity missing: %+v", before)
	}
	original := before.Entries[0]
	h.writeSteps(`{"step":"run","argv":["worker"],"cwd":"/work","started_at_ms":1000,"finished_at_ms":3000,"duration_ms":2000,"exit_code":0}`)
	after := h.obs().transcript(h.run(), transcriptQuery{})
	if len(after.Entries) != 2 || after.Entries[1].ID != original.ID || after.Entries[1].Seq == original.Seq {
		t.Fatalf("identity moved: %+v", after)
	}
	whole := h.obs().transcript(h.run(), transcriptQuery{EntryID: original.ID, Untruncated: true})
	if len(whole.Entries) != 1 || whole.Entries[0].Text != "original" {
		t.Fatalf("wrong expansion: %+v", whole)
	}
	missing := h.obs().transcript(h.run(), transcriptQuery{EntryID: "log:9999:0"})
	if len(missing.Entries) != 0 {
		t.Fatalf("missing identity returned another entry: %+v", missing)
	}
}
