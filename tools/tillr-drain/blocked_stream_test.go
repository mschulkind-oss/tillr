package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func piBlockedStream(text string) string {
	raw, _ := json.Marshal(map[string]any{"type": "agent_end", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text + "\nRESULT: blocked\n"}}}}})
	return counting + fmt.Sprintf("printf '%%s\\n' '%s'\n", raw)
}

func TestPiStructuredBlockedReportsReachDesignAndOwner(t *testing.T) {
	f := blockedRouteFixture(t, piBlockedStream("Conflict: c1 contradicts the API\nOBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: c1 contradicts the API"), piBlockedStream("Conflict: c1 requires an owner ruling\nNEXT-ACTOR: owner"))
	extractor, err := filepath.Abs(filepath.Join("..", "tillr-run", "backends", "pi", "extract-report"))
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"fixture", "designer"} {
		path := filepath.Join(filepath.Dir(f.policy.Backends[backend].Adapter), "extract-report")
		if err := writeForkSafe(path, []byte(fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", extractor)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	steps.BackendsDir = f.root
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt", "docs/work.md"}}}
	result, err := routeLoop(t, f, steps, stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 2 || !strings.Contains(result.Detail, "conflict-owner") {
		t.Fatalf("result %+v", result)
	}
	if countMatching(lines(t, f.count), "gate") != 0 {
		t.Fatal("blocked JSON reached gate")
	}
}
