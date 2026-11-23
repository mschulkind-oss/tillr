package main

import (
	"net/http"
	"strings"
	"testing"
)

// The Item page reads the worker records of the item, not the payload of every worker
// record the store ever held: a live store holds over a million child-session events and
// decoding each one for its item_id cost the page three seconds. A record that names
// another item is therefore never decoded, which a payload that cannot be decoded proves.
func TestItemPageSkipsWorkerRecordsOfOtherItems(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.child-session','run','run-other','not json',1001)`)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.progress','run','run-2','{"run_id":"run-2","item_id":"item-seed-02","criterion_id":"c1"}',1002)`)
	status, body, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("a record of another item was decoded: %d %s", status, raw)
	}
	if n := len(body["data"].(map[string]any)["progress"].([]any)); n != 1 {
		t.Fatalf("the item's own claim is absent: %s", raw)
	}
}

func TestItemPageStillCarriesChildSessionsOfItsRuns(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.child-session','run','run-1','{"run_id":"run-1","item_id":"item-seed-02","session_id":"child-z","role":"reviewer"}',1001)`)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.child-session','run','run-2','{"run_id":"run-2","item_id":"item-seed-04","session_id":"child-y","role":"reviewer"}',1002)`)
	status, _, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK || !strings.Contains(string(raw), "child-z") || strings.Contains(string(raw), "child-y") {
		t.Fatalf("the item's child sessions: %d %s", status, raw)
	}
}
