package drain_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestLoopExpressSettingsReachReservedPool(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[lanes]\nexpress=2\n")
	policy := filepath.Join(scratch(t, "express-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	code, out := invoke(t, "loop", "--root", f.root, "--store", f.store, "--policy", policy, "--item-bin", sibling(t, "item"), "--lanes", "2", "--lane-root", f.laneRoot, "--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true", "--rolling=", "--exit-when-drained")
	if code != 0 {
		t.Fatalf("loop exit=%d: %s", code, out)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var ordinary, express int
		if err := r.QueryRow(`SELECT SUM(lane_id LIKE 'lane-%'), SUM(lane_id LIKE 'express-%') FROM lanes`).Scan(&ordinary, &express); err != nil {
			return err
		}
		if ordinary != 2 || express != 2 {
			t.Fatalf("settings lost: ordinary=%d express=%d", ordinary, express)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
