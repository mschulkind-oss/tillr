package main_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPassRefusesAnItemClosedAfterItsGate(t *testing.T) {
	itemBin := filepath.Join(scratch(t, "item-bin"), "tillr-item")
	if out, err := exec.Command("go", "build", "-C", "../tillr-item", "-o", itemBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build item: %v %s", err, out)
	}
	for _, disposition := range []string{"closed", "resolved-elsewhere"} {
		t.Run(disposition, func(t *testing.T) {
			f := newFixture(t)
			db := filepath.Join(scratch(t, "lifecycle-store"), "state.db")
			itemRun := func(args ...string) {
				t.Helper()
				if out, err := exec.Command(itemBin, args...).CombinedOutput(); err != nil {
					t.Fatalf("item %v: %v %s", args, err, out)
				}
			}
			record := input(t, envelope(t, "item.record", "item-record", map[string]any{"item_id": "item_042", "title": "an item", "state": "in-queue", "criteria": []any{}, "blocked_by": []any{}}))
			itemRun("apply", "--store", db, "--input", record, "--now", "2026-11-16T11:00:00Z")
			verdict := f.verdictFile(t, "pass") // The candidate has passed its gate before the owner acts.
			if disposition == "closed" {
				itemRun("close", "--store", db, "--item", "item_042", "--reason", "not wanted", "--by", "owner")
			} else {
				itemRun("state", "--store", db, "--item", "item_042", "--state", disposition, "--now", "2026-11-16T12:00:00Z")
			}
			code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-closed", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", verdict, "--now", "2026-11-16T13:00:00Z")
			got := decodeLanding(t, out)
			if code != 1 || got.Payload.Result != "refused" || got.Payload.Reason != "item-not-landable" {
				t.Fatalf("pass: %d %s", code, out)
			}
			if f.trunk(t) != f.baseSHA || got.Payload.TrunkAfter != f.baseSHA {
				t.Fatalf("refused landing moved trunk: %s", out)
			}
			s, err := store.OpenRead(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var state string
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				return r.QueryRow(`SELECT state FROM items WHERE item_id = 'item_042'`).Scan(&state)
			}); err != nil {
				t.Fatal(err)
			}
			if state != disposition {
				t.Fatalf("lander changed disposition: %s", state)
			}
		})
	}
}
