package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestInterruptedRetryCannotReadPrecedingAttemptsReport(t *testing.T) {
	backends, adapter := backendWithExtractor(t, "# run report\ntokens: 1500\ncost usd: 0.03\n---\n", "0")
	dir := scratch(t)
	s, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	opts := run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), BackendsDir: backends, Now: time.Now()}
	req := run.Request{RunID: "first", LaneID: "same-lane", Backend: "fixture", Command: []string{adapter}, WorkingDir: ".", TimeoutMS: 10000}
	first, err := run.Exec(context.Background(), s, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	var rec run.Record
	if err = json.Unmarshal(first.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Usage == nil || rec.Usage.Tokens == nil || *rec.Usage.Tokens != 1500 || rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != 0.03 {
		t.Fatalf("first usage: %+v", rec.Usage)
	}
	// The next process is interrupted before output; extraction fails without writing.
	if err = os.WriteFile(adapter, []byte("#!/bin/sh\nexec sleep 5\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(adapter), "extract-report"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	req.RunID = "retry"
	req.TimeoutMS = 10
	retry, err := run.Exec(context.Background(), s, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	rec = run.Record{}
	if err = json.Unmarshal(retry.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Usage != nil {
		t.Fatalf("interrupted retry inherited previous spend: %+v", rec.Usage)
	}
	for _, artifact := range rec.ArtifactRefs {
		if artifact.Kind == "report" {
			t.Fatalf("interrupted retry attached a preceding report: %+v", artifact)
		}
	}
	saved, found := storedRun(t, s, "retry")
	if !found || saved.Usage != nil {
		t.Fatalf("stored retry inherited spend: %+v", saved)
	}
	previous, found := storedRun(t, s, "first")
	if !found || previous.Usage == nil || previous.Usage.Tokens == nil || *previous.Usage.Tokens != 1500 || previous.Usage.CostUSD == nil || *previous.Usage.CostUSD != 0.03 {
		t.Fatalf("first attempt's evidence changed: %+v", previous)
	}
}
