package main_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestConflictFixFailureAndRetryHaveTheirOwnAttempts(t *testing.T) {
	marker := filepath.Join(scratch(t, "conflict-attempt"), "count")
	t.Setenv("CONFLICT_ATTEMPT_COUNT", marker)
	adapter := stub(t, "first", `n=0
if [ -f "$CONFLICT_ATTEMPT_COUNT" ]; then read -r n < "$CONFLICT_ATTEMPT_COUNT"; fi
n=$((n+1))
echo "$n" > "$CONFLICT_ATTEMPT_COUNT"
if [ "$n" -eq 2 ]; then exit 2; fi
if [ "$n" -gt 2 ]; then printf 'RESULT: done\n'; exit 0; fi
`+conflictAdapter)
	f := newLoopFixture(t, adapter)
	f.policy = &drain.Policy{DefaultBackend: "first", Preference: []string{"first", "second"}, Backends: map[string]drain.Backend{
		"first": {Adapter: adapter, Model: "first"}, "second": {Adapter: stub(t, "second", noOpAdapter), Model: "second"},
	}}
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	land := conflictLandScript(t, f.root, landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"}), landingEnvelope(t, "landed", nil), 0)
	steps := f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0))
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt"}}}
	result, err := f.newLoopWith(t, 1, steps, stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 3 {
		t.Fatalf("dispatches=%d, want original, fix and one failover", result.Dispatched)
	}
	runs := runsForItem(t, f.store, "item_041")
	if len(runs) != 3 {
		t.Fatalf("conflict-fix reused an earlier run: %+v", runs)
	}
	if runs[0].Backend != "first" {
		t.Fatalf("original run overwritten: %+v", runs[0])
	}
	if code, out := authorizeRetry(t, f.store, "item_041", "run-item_041-a3", "the owner", "retry no-op", steps.Now().Format("2006-01-02T15:04:05Z")); code != 0 {
		t.Fatalf("retry exited %d: %s", code, out)
	}
	if _, err := f.newLoopWith(t, 1, steps, stage, os.Stderr).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs = runsForItem(t, f.store, "item_041")
	want := []string{"run-item_041-a1", "run-item_041-a1" + drain.ConflictFixSuffix, "run-item_041-a3", "run-item_041-a4"}
	if len(runs) != len(want) {
		t.Fatalf("runs: %+v", runs)
	}
	for i, rec := range runs {
		if rec.RunID != want[i] {
			t.Errorf("run %d = %s, want %s", i, rec.RunID, want[i])
		}
	}
}
