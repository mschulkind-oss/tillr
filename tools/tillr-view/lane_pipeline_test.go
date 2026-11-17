package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLanePipelineReadsRunningAndFinishedSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.jsonl")
	raw := `{"step":"stage","started_at_ms":100,"finished_at_ms":null}
{"step":"stage","started_at_ms":100,"finished_at_ms":200}
{"step":"graph","started_at_ms":201,"finished_at_ms":null}
{"step":"graph"`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, note := readLanePipeline(path, nil)
	if note != "" || len(steps) != 2 || steps[0].FinishedAtMS == nil || *steps[0].FinishedAtMS != 200 || steps[1].FinishedAtMS != nil {
		t.Fatalf("steps=%+v note=%s", steps, note)
	}
	if _, note := readLanePipeline(path+"missing", nil); note == "" {
		t.Fatal("missing timing must be explicit")
	}
}
