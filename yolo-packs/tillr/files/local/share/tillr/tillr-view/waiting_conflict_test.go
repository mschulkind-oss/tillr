package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestWaitingShowsConflictTextAndOwningSeatWithoutAPass(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, seat := range []string{"design", "owner"} {
		c := map[string]any{"conflict_id": "conflict-worker", "item_id": "item-seed-02", "run_id": "worker", "body_ref": "docs/work.md#work", "text": "c1 contradicts the API", "blocked_criteria": []string{"c1"}, "status": "raised", "waiting_on": seat}
		if seat == "owner" {
			c["status"] = "owner"
			c["text"] = "c1 requires an owner ruling"
		}
		raw, _ := json.Marshal(c)
		if _, err := db.Exec(`UPDATE items SET state = 'blocked' WHERE item_id = 'item-seed-02'`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (1000,'item.conflict','item','item-seed-02',?)`, string(raw)); err != nil {
			t.Fatal(err)
		}
		status, body, response := h.get("/api/v1/waiting")
		if status != 200 {
			t.Fatalf("status %d: %s", status, response)
		}
		rows := body["data"].([]any)
		found := false
		for _, value := range rows {
			row := value.(map[string]any)
			if row["item_id"] != "item-seed-02" {
				continue
			}
			found = true
			if !strings.HasPrefix(row["mover"].(string), seat) || !strings.Contains(row["why"].(string), c["text"].(string)) {
				t.Fatalf("seat %s: %+v", seat, row)
			}
			if seat == "owner" && row["class"] != "needs you" {
				t.Fatalf("owner class: %+v", row)
			}
		}
		if !found {
			t.Fatal("conflict item disappeared")
		}
	}
}
