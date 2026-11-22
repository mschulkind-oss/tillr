package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSeatDefaultsPlanAndOnceCLI(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", "exit 0\n"))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	ctx := context.Background()
	s, err := store.Open(ctx, f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.WriteTx(ctx, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET seat='build'`); return err }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "config/tillr/workflows/two@v1.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	raw := `{"name":"two","version":1,"stages":[{"id":"first","kind":"agent","seat":"build","access":"write"},{"id":"second","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"first","to_node":"second"}]}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, ".tillr.toml"), []byte("[workflows.default]\nbuild = 'two@v1'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, f.root, "add", ".")
	git(t, f.root, "commit", "-qm", "workflow default fixture")
	for name := range f.policy.Backends {
		f.policy.Seats = map[string]string{"build": name}
		break
	}
	policyPath := filepath.Join(f.root, "policy.json")
	encoded, _ := json.Marshal(f.policy)
	if err := os.WriteFile(policyPath, encoded, 0644); err != nil {
		t.Fatal(err)
	}
	tickPath := tick(t, "2026-11-15T00:00:00Z", []string{"lane-1"}, 1, policyPath)
	output := filepath.Join(scratch(t, "workflow-plan"), "decisions.json")
	code, out := invoke(t, "plan", "--root", f.root, "--store", f.store, "--input", tickPath, "--output", output)
	if code != 0 {
		t.Fatalf("plan: %d %s", code, out)
	}
	decisions := readDecisions(t, output)
	if len(decisions) != 1 || decisions[0].Payload.RunRequest == nil || !strings.HasSuffix(decisions[0].Payload.RunRequest.RunID, "-n1") {
		t.Fatalf("default not planned: %+v", decisions)
	}
	// plan must not persist a graph. A failed handoff must roll it back too.
	assertSelection := func(want string) {
		t.Helper()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			selected, source, _, err := item.ReadWorkflowSelection(r, "item_041")
			if err != nil {
				return err
			}
			if selected != want || (want != "" && source != "default") {
				t.Fatalf("selection %q/%q want %q", selected, source, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertSelection("")
	sinkDir := scratch(t, "workflow-sink")
	code, _ = invoke(t, "once", "--root", f.root, "--store", f.store, "--input", tickPath, "--runner", "record-sink", "--runner-output", sinkDir)
	if code == 0 {
		t.Fatal("invalid handoff succeeded")
	}
	assertSelection("")
	sink := filepath.Join(sinkDir, "requests.json")
	code, out = invoke(t, "once", "--root", f.root, "--store", f.store, "--input", tickPath, "--runner", "record-sink", "--runner-output", sink)
	if code != 0 {
		t.Fatalf("once: %d %s", code, out)
	}
	requests := readRequests(t, sink)
	if len(requests) != 1 || !strings.HasSuffix(requests[0].Payload.RunID, "-n1") {
		t.Fatalf("default not dispatched: %+v", requests)
	}
	assertSelection("two@v1")
	// A stored graph must survive a settings change even outside the loop.
	if err := os.WriteFile(filepath.Join(f.root, ".tillr.toml"), []byte("[workflows.default]\nbuild = 'missing@v1'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	code, out = invoke(t, "plan", "--root", f.root, "--store", f.store, "--input", tickPath, "--output", output)
	if code != 0 {
		t.Fatalf("pinned plan: %d %s", code, out)
	}
	decisions = readDecisions(t, output)
	if len(decisions) != 1 || decisions[0].Payload.RunRequest == nil || !strings.HasSuffix(decisions[0].Payload.RunRequest.RunID, "-n1") {
		t.Fatalf("stored graph replaced: %+v", decisions)
	}
}
