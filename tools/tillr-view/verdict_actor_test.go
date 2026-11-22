package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestItemVerdictAuthors(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []map[string]any{}
	for i, kind := range []string{"mechanical", "agent", "human", "mechanical"} {
		v := map[string]any{"item_id": "item-seed-02", "criterion_id": "ac-1", "verdict": "done", "actor_kind": kind, "claim_class": "verification-only", "recorded_at": "2026-11-16T12:00:00Z"}
		if i < 3 {
			v["actor"] = kind + "-author"
		} else {
			delete(v, "recorded_at") // Older events still have the store's timestamp.
		}
		if i == 1 {
			v["verdict"] = "not-done"
			v["findings"] = []any{
				map[string]any{"kind": "gap", "case": "Empty input is unchecked", "paths": []any{"file:parse.go:24", "file:parse_test.go"}},
				map[string]any{"kind": "regression", "case": "Old inputs fail", "paths": []any{"file:parse.go:48"}},
				map[string]any{"kind": "unasked-change", "case": "Help changed", "paths": []any{"file:help.go"}},
			}
		}
		payload, _ := json.Marshal(v)
		if _, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (1000, 'item.verdict', 'item', 'item-seed-02', ?)`, string(payload)); err != nil {
			t.Fatal(err)
		}
		delete(v, "item_id")
		if i == 3 {
			v["actor"] = ""
			v["recorded_at"] = "1970-01-01T00:00:01Z"
		}
		want = append(want, v)
	}
	// Another item's verdict must never leak into this dossier.
	if _, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (1000, 'item.verdict', 'item', 'item-seed-04', '{"criterion_id":"other"}')`); err != nil {
		t.Fatal(err)
	}
	status, body, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	data := body["data"].(map[string]any)
	got, ok := data["verdicts"].([]any)
	if !ok || len(got) != len(want) {
		t.Fatalf("verdicts: %s", raw)
	}
	for i, expected := range want {
		row := got[i].(map[string]any)
		for field, value := range expected {
			if !reflect.DeepEqual(row[field], value) {
				t.Errorf("verdict %d %s = %v; want %v", i, field, row[field], value)
			}
		}
		if _, wantFindings := expected["findings"]; !wantFindings {
			if _, hasFindings := row["findings"]; hasFindings {
				t.Errorf("verdict %d unexpectedly has findings: %v", i, row["findings"])
			}
		}
	}
}

func TestItemClosedStructuredWaiter(t *testing.T) {
	for _, state := range []string{"closed", "landed"} {
		for _, edge := range []bool{false, true} {
			t.Run(state+"/edge="+fmt.Sprint(edge), func(t *testing.T) {
				h := newServeHarness(t)
				db, err := sql.Open("sqlite", h.store)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for _, query := range []string{
					`ALTER TABLE items ADD COLUMN waiting_on TEXT NOT NULL DEFAULT ''`,
					`UPDATE items SET waiting_on = '{"waiter":"item:item-seed-02","reason":"finish prerequisite"}' WHERE item_id = 'item-seed-06'`,
				} {
					if _, err := db.Exec(query); err != nil {
						t.Fatal(err)
					}
				}
				if !edge {
					if _, err := db.Exec(`DELETE FROM item_edges WHERE item_id = 'item-seed-06'`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := db.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-seed-02'`, state); err != nil {
					t.Fatal(err)
				}
				status, body, raw := h.get("/api/v1/items/item-seed-06")
				if status != http.StatusOK {
					t.Fatalf("status %d: %s", status, raw)
				}
				data := body["data"].(map[string]any)
				got := data["closed_blockers"].([]any)
				if state == "closed" {
					if len(got) != 1 || got[0] != "item-seed-02" {
						t.Fatalf("closed blockers = %v", got)
					}
				} else if len(got) != 0 {
					t.Fatalf("landed blockers alarmed: %v", got)
				}
				projection := data["item"].(map[string]any)["projected_state"].(map[string]any)
				if projection["underlying"] != "item:item-seed-02: finish prerequisite" {
					t.Fatalf("waiter not projected: %v", projection)
				}
			})
		}
	}
}

func TestItemClosedBlockers(t *testing.T) {
	for _, state := range []string{"closed", "landed"} {
		t.Run(state, func(t *testing.T) {
			h := newServeHarness(t)
			db, err := sql.Open("sqlite", h.store)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-seed-02'`, state); err != nil {
				t.Fatal(err)
			}
			status, body, raw := h.get("/api/v1/items/item-seed-06")
			if status != http.StatusOK {
				t.Fatalf("status %d: %s", status, raw)
			}
			data := body["data"].(map[string]any)
			if verdicts, ok := data["verdicts"].([]any); !ok || len(verdicts) != 0 {
				t.Fatalf("empty verdict history = %v", data["verdicts"])
			}
			got, ok := data["closed_blockers"].([]any)
			if !ok {
				t.Fatalf("missing closed_blockers: %s", raw)
			}
			if state == "closed" {
				if len(got) != 1 || got[0] != "item-seed-02" {
					t.Fatalf("closed blockers = %v", got)
				}
				projection := data["item"].(map[string]any)["projected_state"].(map[string]any)
				underlying, _ := projection["underlying"].(string)
				if !strings.Contains(underlying, "item-seed-02") || !strings.Contains(underlying, "without landing") || !strings.Contains(underlying, "owner/operator") {
					t.Fatalf("closed blocker projection underlying = %q, want item-seed-02, without landing, and owner/operator", underlying)
				}
			} else if len(got) != 0 {
				t.Fatalf("landed blockers alarmed: %v", got)
			}
		})
	}
}
