package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func compactDesignerJSON(raw []byte) string {
	var b bytes.Buffer
	_ = json.Compact(&b, raw)
	return b.String()
}

func TestDesignAndCommentObservatoryRecords(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE last_designers(document TEXT PRIMARY KEY,record TEXT NOT NULL);
 CREATE TABLE comment_runs(run_id TEXT PRIMARY KEY,item_id TEXT,session_id TEXT,reason TEXT);
 CREATE TABLE comment_batches(item_id TEXT PRIMARY KEY,document TEXT,batch TEXT);
 CREATE TABLE comment_arrivals(item_id TEXT,comment_id TEXT,session_id TEXT,reason TEXT,cache_lifetime_seconds INTEGER,seconds_since_request REAL);
 CREATE TABLE comment_deliveries(run_id TEXT PRIMARY KEY,item_id TEXT,document TEXT,blob_id TEXT,body TEXT,written INTEGER);
 CREATE TABLE comment_reply_states(run_id TEXT,comment_id TEXT,state TEXT);
 CREATE TABLE comment_confirmation(id INTEGER PRIMARY KEY,configured INTEGER);
 INSERT INTO last_designers VALUES('docs/design.md','{"session_id":"session","last_request":"2999-01-01T00:00:00Z","cache_lifetime_seconds":60,"context_tokens":4200,"compacted":false}');
 INSERT INTO comment_batches VALUES('item-seed-02','docs/design.md','{"document":"docs/design.md","comments":[{"id":"comment","comment":"Why this?","created_at":1,"reactions":[]}]}');
 INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq,usage_cost_usd) VALUES('run-seed-02','item-seed-02','lane-a','pi','finished',1000,2000,'[]',10,0.25);
 INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq,usage_cost_usd) VALUES('run-seed-03','item-seed-02','lane-a','pi','finished',1000,2000,'[]',11,0.50);
 INSERT INTO comment_runs VALUES('run-seed-02','item-seed-02','session','resumed-warm'),('run-seed-03','item-seed-02','session','resumed-warm');
 INSERT INTO comment_arrivals VALUES('item-seed-02','comment','session','resumed-warm',60,30),('late','c2','session','resumed-expired',60,90),('early','c3','session','resumed-warm',60,0),('new','c4','','no-designer',NULL,NULL);
 INSERT INTO comment_deliveries VALUES('run-seed-02','item-seed-02','docs/design.md','blob','{"id":"comment","round":0,"summary":"See Shape"}',1);`)
	code, _, raw := h.get("/api/v1/designs")
	if code != 200 {
		t.Fatalf("designs %d %s", code, raw)
	}
	for _, want := range []string{`"within_lifetime":2`, `"after_lifetime":1`, `"no_session":1`, `"context_tokens":4200`, `"cost_usd":0.75`, `"state":"warm"`, `"session_id":"session"`, `"reason":"resumed-warm"`} {
		if !strings.Contains(compactDesignerJSON(raw), want) {
			t.Errorf("missing %s: %s", want, raw)
		}
	}
	code, _, raw = h.get("/api/v1/items/item-seed-02")
	if code != 200 {
		t.Fatalf("item %d %s", code, raw)
	}
	for _, want := range []string{"Why this?", "See Shape", `"state":"written"`, "replies are not being confirmed because no Vantage server is set"} {
		if !strings.Contains(compactDesignerJSON(raw), want) {
			t.Errorf("missing %s: %s", want, raw)
		}
	}
	h.exec(`INSERT INTO comment_reply_states VALUES('run-seed-02','comment','delivered'); INSERT INTO comment_confirmation VALUES(1,1);`)
	_, _, raw = h.get("/api/v1/items/item-seed-02")
	if !strings.Contains(compactDesignerJSON(raw), `"state":"delivered"`) || strings.Contains(compactDesignerJSON(raw), "no Vantage server") {
		t.Fatalf("confirmed %s", raw)
	}
	h.exec(`UPDATE comment_deliveries SET written=0; DELETE FROM comment_reply_states;`)
	_, _, raw = h.get("/api/v1/items/item-seed-02")
	if !strings.Contains(compactDesignerJSON(raw), "waiting on the served checkout") {
		t.Fatalf("waiting %s", raw)
	}
	// The existing document pane also gets the last-designer data.
	if err := os.MkdirAll(filepath.Join(h.dir, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "docs/design.md"), []byte("# Design\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pointItemAt(t, h.store, "item-seed-02", "docs/design.md")
	_, _, raw = h.get("/api/v1/items/item-seed-02/doc")
	if !strings.Contains(compactDesignerJSON(raw), `"session_id":"session"`) {
		t.Fatalf("doc %s", raw)
	}
	h.exec(`UPDATE last_designers SET record='{"session_id":"session","last_request":"2000-01-01T00:00:00Z","cache_lifetime_seconds":60,"compacted":false}';`)
	_, _, raw = h.get("/api/v1/designs?path=docs%2Fdesign.md")
	if !strings.Contains(compactDesignerJSON(raw), `"state":"expired"`) {
		t.Fatalf("expired %s", raw)
	}
	h.exec(`UPDATE last_designers SET record='{"session_id":"session","compacted":true}';`)
	_, _, raw = h.get("/api/v1/designs?path=docs%2Fdesign.md")
	if !strings.Contains(compactDesignerJSON(raw), `"state":"compacted"`) {
		t.Fatalf("compacted %s", raw)
	}
}

func TestDesignObservatoryBeforeTablesExist(t *testing.T) {
	h := newServeHarness(t)
	code, _, raw := h.get("/api/v1/designs")
	if code != 200 {
		t.Fatalf("old store %d %s", code, raw)
	}
	code, _, raw = h.get("/api/v1/items/item-seed-02")
	if code != 200 {
		t.Fatalf("old item %d %s", code, raw)
	}
}
