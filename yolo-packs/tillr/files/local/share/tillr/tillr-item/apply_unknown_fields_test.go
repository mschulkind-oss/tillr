package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestApplyRefusesUnknownFieldsWithoutWrites(t *testing.T) {
	for _, mode := range []string{"new", "update", "replay", "criterion", "malformed-replay", "empty-field", "early-field", "duplicate-new", "duplicate-replay"} {
		t.Run(mode, func(t *testing.T) {
			db := filepath.Join(scratch(t, "unknown-field"), "state.db")
			original := withKey(t, envelope(t, "item.record", "rec-original", base("existing", "original")), "request-1")
			if code, out := apply(t, db, original); code != 0 {
				t.Fatalf("initial apply: %d %s", code, out)
			}
			before := applyStoreSnapshot(t, db)
			id := "new-item"
			if mode == "update" || mode == "replay" || mode == "malformed-replay" || mode == "duplicate-replay" {
				id = "existing"
			}
			p := base(id, "changed")
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			field := "kind"
			if mode == "empty-field" {
				field = ""
			} else if mode == "early-field" {
				field = "aaa"
			}
			if mode == "criterion" {
				field = "extra"
				payload["criteria"].([]any)[0].(map[string]any)[field] = "lost"
			} else {
				payload[field] = "finding"
			}
			record := envelope(t, "item.record", "rec-unknown", payload)
			if mode == "replay" || mode == "malformed-replay" {
				if mode == "malformed-replay" {
					// A known-field type error before the unknown key must not hide
					// that key and turn the request into a successful replay.
					record = envelope(t, "item.record", "rec-unknown", json.RawMessage(`{"title":123,"item_id":"existing","kind":"finding"}`))
					record = strings.Replace(record, `"record_version":1`, `"idempotency_key":"request-1","record_version":1`, 1)
				} else {
					record = withKey(t, record, "request-1")
				}
			}
			if mode == "duplicate-new" || mode == "duplicate-replay" {
				// The second criteria array must not hide the unknown field in
				// the first one. Keep duplicate keys intact in the input bytes.
				payload := json.RawMessage(`{"item_id":"` + id + `","state":"not-started","criteria":[{"criterion_id":"c1","text":"the first criterion","state":"open","kind":"finding"}],"criteria":[{"criterion_id":"c1","text":"the first criterion","state":"open"}]}`)
				record = envelope(t, "item.record", "rec-duplicate", payload)
				if mode == "duplicate-replay" {
					record = strings.Replace(record, `"record_version":1`, `"idempotency_key":"request-1","record_version":1`, 1)
				}
			}
			code, out := apply(t, db, record)
			if code != 1 {
				t.Fatalf("unknown field apply: exit %d, want 1: %s", code, out)
			}
			got := decode(t, out)
			if got.Kind != "item.rejection" || got.Payload.Rule != "unknown-field" || got.Payload.ItemID != id || !strings.Contains(got.Payload.Reason, `"`+field+`"`) {
				t.Fatalf("refusal does not name the rule, item and field: %s", out)
			}
			if after := applyStoreSnapshot(t, db); !reflect.DeepEqual(before, after) {
				t.Fatalf("refused apply changed the store: before %v, after %v", before, after)
			}
		})
	}
}

// Include every item-owned row and the event count, not only the item's state.
type applySnapshot struct {
	Counts map[string]int
	Echo   string
}

func applyStoreSnapshot(t *testing.T, db string) applySnapshot {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatal(err)
	}
	// The unchanged echo also catches updates that leave all row counts unchanged.
	code, shown := run(t, "show", "--store", db, "--item", "existing")
	if code != 0 {
		t.Fatalf("show: %d %s", code, shown)
	}
	return applySnapshot{Counts: counts, Echo: string(shown)}
}

func TestApplyRefusesForeignProducerSystem(t *testing.T) {
	db := filepath.Join(scratch(t, "foreign-producer"), "state.db")
	raw := strings.Replace(envelope(t, "item.record", "foreign", base("foreign", "foreign")), `"system":"tillr"`, `"system":"forms"`, 1)
	cmd := exec.Command(bin, "apply", "--store", db, "--input", input(t, raw), "--now", "2026-11-10T00:00:00Z")
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "record from another system") || !strings.Contains(string(out), "forms") {
		t.Fatalf("foreign producer: %v %s", err, out)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("foreign producer created a store: %v", err)
	}
}
