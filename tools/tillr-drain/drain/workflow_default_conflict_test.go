package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSeatDefaultConflictFixRunsOneNode(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", conflictAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	ctx := context.Background()
	s, err := store.Open(ctx, f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='build',workflow='single-worker@v1'`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "config/tillr/workflows/two@v1.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"name":"two","version":1,"stages":[{"id":"first","kind":"agent","seat":"build","access":"write"},{"id":"second","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"first","to_node":"second"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, f.root, "add", ".")
	git(t, f.root, "commit", "-qm", "default workflow")
	f.policy.Seats = map[string]string{"build": "stub"}
	// Use the fixture's actual backend name, not a second model choice.
	for name := range f.policy.Backends {
		f.policy.Seats["build"] = name
		break
	}
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, landingEnvelope(t, "landed", nil), 0)
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt"}}}
	steps := f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0))
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true, Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"}, Policy: f.policy, Steps: &evidenceProcessSteps{steps}, Stage: stage, Now: steps.Now, Report: os.Stderr, WorkflowDefaults: map[string]string{"build": "two@v1"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConflictFixes != 1 || result.Landed != 1 || len(stage.specs) != 2 {
		t.Fatalf("runs: %+v specs=%+v", result, stage.specs)
	}
	if stage.specs[1].Node != nil || stage.specs[1].Conflict == nil {
		t.Fatalf("repair expanded: %+v", stage.specs[1])
	}
}
