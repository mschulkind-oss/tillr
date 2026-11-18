package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
)

func TestStatusShowsLatestUnsharedOpenCount(t *testing.T) {
	v := view{records: []record.Envelope{{Kind: "item.sync", Payload: json.RawMessage(`{"unshared_open":3}`)}, {Kind: "item.sync", Payload: json.RawMessage(`{"unshared_open":1}`)}}}
	p, code := assemble(v, nil, nil)
	if code != record.ExitOK || p.UnsharedOpen == nil || *p.UnsharedOpen != 1 {
		t.Fatalf("status count: %+v %d", p, code)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"unshared_open":1`) {
		t.Fatalf("count not printed: %s", raw)
	}
}

func TestWaitingTriageAndOwnerKeepTheirNextMoverAndReport(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{`ALTER TABLE items ADD COLUMN waiting_on TEXT NOT NULL DEFAULT ''`, `ALTER TABLE items ADD COLUMN underlying_state TEXT NOT NULL DEFAULT ''`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	for _, seat := range []string{"design", "owner"} {
		c := map[string]any{"conflict_id": "triage-request", "item_id": "item-seed-02", "run_id": "triage", "body_ref": "docs/request.md", "text": "Request has no block.\nThe report says it duplicates item-other.", "status": "awaiting", "waiting_on": seat, "triage_version": "version"}
		if seat == "owner" {
			c["status"] = "owner"
		}
		raw, _ := json.Marshal(c)
		waiting, _ := json.Marshal(map[string]string{"waiter": seat, "reason": "first line only"})
		if _, err := db.Exec(`UPDATE items SET state='blocked', waiting_on=? WHERE item_id='item-seed-02'`, string(waiting)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (1000,'item.conflict','item','item-seed-02',?)`, string(raw)); err != nil {
			t.Fatal(err)
		}
		code, body, out := h.get("/api/v1/waiting")
		if code != 200 {
			t.Fatalf("waiting: %d %s", code, out)
		}
		found := false
		for _, v := range body["data"].([]any) {
			row := v.(map[string]any)
			if row["item_id"] != "item-seed-02" {
				continue
			}
			found = true
			actor := "design seat;"
			if seat == "owner" {
				actor = "operator;"
			}
			if !strings.Contains(row["why"].(string), c["text"].(string)) || !strings.HasPrefix(row["mover"].(string), actor) {
				t.Fatalf("lost next mover/report: %+v", row)
			}
		}
		if !found {
			t.Fatal("missing request")
		}
	}
	rule := waitingRuleFor("triage-no-block")
	if !strings.Contains(rule.mover, "design") {
		t.Fatalf("due triage has no mover: %+v", rule)
	}
}
