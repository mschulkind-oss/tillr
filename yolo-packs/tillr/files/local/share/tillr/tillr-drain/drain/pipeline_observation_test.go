package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineStartIsVisibleBeforeProgramFinishes(t *testing.T) {
	root := t.TempDir()
	s := &ProcessSteps{Root: root}
	path := filepath.Join(RunDir(root, "run-a"), "pipeline.jsonl")
	// The program itself inspects the record while it is still running.
	program := filepath.Join(root, "inspect")
	if err := writeForkSafe(program, []byte("#!/bin/sh\ntest -s '"+path+"' || exit 1\ncat '"+path+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, _, err := s.program(context.Background(), "gate", root, program, Candidate{RunID: "run-a"})
	if err != nil || code != 0 {
		t.Fatalf("start was not visible: code=%d out=%s err=%v", code, out, err)
	}
	if !strings.Contains(out, `"step":"gate"`) || !strings.Contains(out, `"finished_at_ms":null`) {
		t.Fatalf("not a running step: %s", out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != 2 || strings.Count(string(raw), `"finished_at_ms":null`) != 1 {
		t.Fatalf("missing finish: %s", raw)
	}
}
