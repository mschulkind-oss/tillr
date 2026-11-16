package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
)

func landingFixture(t *testing.T, h *serveHarness) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE landings (
 landing_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, run_id TEXT NOT NULL,
 verdict_id TEXT NOT NULL, result TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
 base_sha TEXT NOT NULL, head_sha TEXT NOT NULL, trunk_before TEXT NOT NULL,
 trunk_after TEXT NOT NULL, gated_tree TEXT NOT NULL DEFAULT '', landed_tree TEXT NOT NULL DEFAULT '',
 conflict_paths TEXT NOT NULL DEFAULT '[]', gate_skipped_reason TEXT NOT NULL DEFAULT '', event_seq INTEGER NOT NULL DEFAULT 0);
 INSERT INTO events(seq,ts,kind,object_type,object_id,payload) VALUES
 (101,1234,'land.landing','item','item-a','{}'),
 (102,1300,'drain.refusal','run','run-a','{"step":"conflict","item_id":"item-a","tick_id":"pass-a","fix_run_id":"run-fix"}'),
 (103,1400,'land.landing','item','item-a','{}'),
 (104,1500,'drain.refusal','run','run-b','{"step":"trunk-already-red","item_id":"item-b","tick_id":"pass-b","fixer_item":"fix-red","red_sha":"red-commit"}'),
 (105,1,'land.landing','item','item-c','{}'),
 (106,1700,'gate.verdict','run','run-c','{"tick_id":"pass-c"}');
 INSERT INTO dispatches(decision_id,item_id,run_id,action,reason,dispatched_at) VALUES
 ('decision-a','item-a','run-a','dispatch','ready',1000),
 ('decision-b','item-b','run-b','dispatch','ready',2000),
 ('decision-c','item-c','run-c','dispatch','ready',3000);
 INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq,conflict_paths) VALUES
 ('landing-a','item-a','run-a','v-a','squash-conflict','base','head','aaa','aaa',101,'["file.go"]'),
 ('landing-fix','item-a','run-fix','v-fix','landed','base','head','aaa','bbb',103,'[]'),
 ('landing-c','item-c','run-c','v-c','landed','base','head','ccc','ddd',105,'[]'),
 ('landing-unknown','item-c','run-unknown','v-u','refused','base','head','ddd','ddd',0,'[]');`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLandingsHistory(t *testing.T) {
	h := newServeHarness(t)
	landingFixture(t, h)
	status, body, raw := h.get("/api/v1/landings")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	var response struct {
		Data struct {
			Rows []landingRow `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	rows := response.Data.Rows
	if len(rows) != 6 {
		t.Fatalf("rows: %s", raw)
	}
	if rows[0].LandingID != "landing-a" || rows[0].AtMS == nil || *rows[0].AtMS != 1234 || rows[0].FixRunID != "run-fix" || rows[0].PassID != "19700101T000001Z" {
		t.Fatalf("conflict: %+v", rows[0])
	}
	if rows[1].PassID != "19700101T000001Z" {
		t.Fatalf("conflict repair lost its original pass: %+v", rows[1])
	}
	if rows[2].Kind != "acquittal" || rows[2].FixerItem != "fix-red" || rows[2].RedSHA != "red-commit" || rows[2].RunID != "run-b" || *rows[2].AtMS != 1500 {
		t.Fatalf("acquittal: %+v", rows[2])
	}
	if rows[3].Kind != "gap" || rows[3].TrunkBefore != "bbb" || rows[3].TrunkAfter != "ccc" || rows[3].AtMS != nil {
		t.Fatalf("gap: %+v", rows[3])
	}
	if rows[4].PassID != "19700101T000003Z" {
		t.Fatalf("pass: %+v", rows[4])
	}
	if rows[5].AtMS != nil || rows[5].EpistemicState != stateUnobservedSeam {
		t.Fatalf("missing clock: %+v", rows[5])
	}
	if body["snapshot"].(map[string]any)["event_head_seq"] != float64(106) {
		t.Fatal("no transaction cursor")
	}
	status, _, raw = h.get("/api/v1/landings/landing-a")
	if status != http.StatusOK {
		t.Fatalf("detail: %d %s", status, raw)
	}
	var detail struct {
		Data landingRow `json:"data"`
	}
	json.Unmarshal(raw, &detail)
	if detail.Data.ItemID != "item-a" || detail.Data.RunID != "run-a" || detail.Data.PassID != "19700101T000001Z" {
		t.Fatalf("detail: %s", raw)
	}
	status, _, raw = h.get("/api/v1/landings/missing")
	if status != http.StatusNotFound {
		t.Fatalf("missing: %d %s", status, raw)
	}
}

func TestLandingsClockAndGapSeams(t *testing.T) {
	h := newServeHarness(t)
	landingFixture(t, h)
	// Wall-clock skew must not reorder the sequence; and an absent clock must
	// not turn a measured commit mismatch into an apparently continuous trunk.
	h.exec(`UPDATE landings SET trunk_before = 'external-head' WHERE landing_id = 'landing-unknown'`)
	status, _, raw := h.get("/api/v1/landings")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	var response struct {
		Data struct {
			Rows []landingRow `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	rows := response.Data.Rows
	if len(rows) != 7 || rows[4].LandingID != "landing-c" || *rows[4].AtMS != 1 || rows[5].Kind != "gap" || rows[5].TrunkAfter != "external-head" {
		t.Fatalf("history: %s", raw)
	}
}

func TestLandingInvalidationIncludesRepairEvents(t *testing.T) {
	id := "run-a"
	for _, kind := range []string{"land.landing", "drain.refusal"} {
		resources := eventResources([]event.Event{{Kind: kind, ObjectType: "run", ObjectID: &id}})
		if len(resources) != 2 || resources[0] != "landings" || resources[1] != "runs:run-a" {
			t.Fatalf("%s: %v", kind, resources)
		}
	}
}

func TestLandingsMalformedPathsRefuse(t *testing.T) {
	h := newServeHarness(t)
	landingFixture(t, h)
	h.exec(`UPDATE landings SET conflict_paths = 'broken-json' WHERE landing_id = 'landing-a'`)
	status, _, raw := h.get("/api/v1/landings")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("%d: %s", status, raw)
	}
}

func TestLandingsMissingTableIsNotEmptyHistory(t *testing.T) {
	h := newServeHarness(t)
	status, _, raw := h.get("/api/v1/landings")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", status, raw)
	}
}
