package item_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestApplyAcceptsOwnProducers(t *testing.T) {
	ctx := context.Background()
	for _, producer := range []string{"sync", "comments", "seed", "loop"} {
		t.Run(producer, func(t *testing.T) {
			source := syncStore(t)
			var payload json.RawMessage
			switch producer {
			case "sync":
				report := runSync(t, source, "", doc("docs/example.md", goodBlock))
				if report.CreatedCount != 1 || len(report.Findings) != 0 {
					t.Fatalf("sync: %+v", report)
				}
			case "comments":
				root := scratch(t)
				if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "docs/example.md"), []byte("# Example\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := source.WriteTx(ctx, func(tx *store.Tx) error {
					_, err := item.TakeComments(ctx, tx, root, "docs/example.md", []item.ReviewComment{{ID: "comment-1", Comment: "Why?", CreatedAt: 1}}, syncAt)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			case "seed":
				items, err := item.ReadSeed([]byte(`{"kind":"tillr.seed","items":[{"item_id":"seed-item","title":"Seed item","state":"in-queue","criteria":[{"criterion_id":"c1","text":"Build it","state":"not-done"}],"blocked_by":[],"allowed_paths":["tools/thing/"],"rank":"m","seat":"build"}]}`))
				if err != nil {
					t.Fatal(err)
				}
				payload, err = json.Marshal(items[0])
				if err != nil {
					t.Fatal(err)
				}
			case "loop":
				// The trunk-fixer shape written by drain/trunk.go: a loop-produced
				// record uses utility "drain", not "item", and has no body_ref.
				payload = json.RawMessage(`{"item_id":"fixer-main-abc","title":"Repair broken trunk","state":"in-queue","rank":"000","allowed_paths":["tools/thing/"],"seat":"build","criteria":[{"criterion_id":"trunk-green","text":"The trunk gate passes","state":"not-done"}]}`)
			}
			if payload == nil {
				// Read the actual payload Apply materialized for the intake. These
				// producers marshal itemPayload; the event adds only updated_at.
				events, err := source.Events(ctx, 0)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range events {
					if e.Kind == item.AddKind {
						payload = e.Payload
					}
				}
				if payload == nil {
					t.Fatal("intake emitted no item.add payload")
				}
			}
			utility := item.Utility
			if producer == "loop" {
				utility = "drain"
			}
			env := record.Envelope{
				RecordVersion: record.SupportedVersion, Kind: item.RecordKind, ID: "rec-" + producer,
				Producer:  record.Producer{System: record.System, Utility: utility, Version: item.Version},
				CreatedAt: syncAt, Payload: payload,
			}
			target := syncStore(t)
			if err := target.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := item.Apply(ctx, tx, env)
				return err
			}); err != nil {
				t.Fatalf("apply %s record: %v", producer, err)
			}
			if got := count(t, target, "SELECT count(*) FROM items"); got != 1 {
				t.Fatalf("apply stored %d items, want 1", got)
			}
		})
	}
}
