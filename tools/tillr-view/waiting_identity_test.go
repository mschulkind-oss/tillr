package main

import (
	"strings"
	"testing"
)

func TestWaitingForeignIdentityWithoutQueuedItems(t *testing.T) {
	h := newServeHarness(t)
	sha := strings.Repeat("b", 40)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`DELETE FROM items`)
	h.exec(`CREATE TABLE IF NOT EXISTS trunk_identity (id INTEGER PRIMARY KEY, tip TEXT, pending TEXT)`)
	h.exec(`INSERT INTO trunk_identity VALUES (1,?,?)`, sha, `[{"sha":"`+sha+`","author":"Other <other@example.invalid>","committer":"Fixture"}]`)
	status, _, raw := h.get("/api/v1/waiting")
	for _, want := range []string{sha, "Other", "re-author", "accept-identity", "observation"} {
		if status != 200 || !strings.Contains(string(raw), want) {
			t.Fatalf("missing %q: %d %s", want, status, raw)
		}
	}
	h.exec(`UPDATE trunk_identity SET pending='[]'`)
	status, _, raw = h.get("/api/v1/waiting")
	if status != 200 || strings.Contains(string(raw), sha) {
		t.Fatalf("answered hold still visible: %d %s", status, raw)
	}
}

func TestWaitingForeignIdentityNamesCommitAuthorAndRemedy(t *testing.T) {
	h := newServeHarness(t)
	sha := strings.Repeat("a", 40)
	reason := "trunk-foreign-identity: " + sha + " author Other <other@example.invalid>; committer Other <other@example.invalid>"
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('identity','identity','in-queue',0)`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES('identity','identity','hold','` + reason + `',1000)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d %s", status, raw)
	}
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] != "identity" {
			continue
		}
		if row["class"] != "needs you" || row["where"] != "trunk" || row["why"] != reason || row["mover"] != "operator; re-author the commit, or record its id with tillr-drain accept-identity" {
			t.Fatalf("identity waiting: %v", row)
		}
		return
	}
	t.Fatal("identity hold missing")
}
