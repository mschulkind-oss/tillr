package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if result.Dispatched != 2 || !strings.Contains(result.Detail, "item_041 ("+drain.ReasonBackendClosed+": first:") {
		t.Fatalf("expected original and failed fix, then a hold on first: %+v", result)
	}
	original := itemRun{RunID: "run-item_041-a1", Backend: "first"}
	fix := itemRun{RunID: original.RunID + drain.ConflictFixSuffix, Backend: "first"}
	before := runsForItem(t, f.store, "item_041")
	if len(before) != 2 || before[0] != original || before[1] != fix {
		t.Fatalf("expected distinct original and fix runs: %+v", before)
	}

	if err := drain.ProbeBackend(context.Background(), f.store, f.root, "first", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	result, err = f.newLoopWith(t, 1, steps, stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 1 || result.Landed != 1 {
		t.Fatalf("expected one recovered dispatch and landing: %+v", result)
	}
	runs := runsForItem(t, f.store, "item_041")
	retry := itemRun{RunID: "run-item_041-a3", Backend: "first"}
	if len(runs) != 3 || runs[0] != original || runs[1] != fix || runs[2] != retry {
		t.Fatalf("retry did not get its own run on the recovered backend: %+v", runs)
	}
}
