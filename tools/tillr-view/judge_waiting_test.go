package main

import (
	"database/sql"
	"testing"
)

func TestWaitingClassifiesJudgeDispositions(t *testing.T) {
	for _, reason := range []string{"judge-blocked", "judge-defective"} {
		rule := waitingRuleFor(reason)
		if rule.class == "unclassified" || rule.where != "judge" || rule.mover == "" {
			t.Errorf("judge outcome %s: %+v", reason, rule)
		}
	}
}

func TestWaitingShowsJudgeReason(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`DELETE FROM dispatches`,
		`DELETE FROM landing_attempts`,
		`DELETE FROM candidates`,
		`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES('review-run','item-seed-02','lane-a','pi','exited',8000,8500,'[]',100)`,
		`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES('judge-hold','item-seed-02','hold','eval-failed',9000)`,
		`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES('legacy-grant-hold','item-seed-02','hold','judge-grant-absent',9100)`,
		`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(8500,'drain.refusal','run','review-run','{"item_id":"item-seed-02","step":"eval-failed","detail":"criterion c1 still lacks evidence"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	status, snapshot, raw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot status %d %s", status, raw)
	}
	if _, found := snapshot["data"].(map[string]any)["grants"]; found {
		t.Fatalf("snapshot advertises retired grants: %s", raw)
	}
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("status %d %s", status, raw)
	}
	rows := body["data"].([]any)
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["item_id"] == "item-seed-02" {
			if row["why"] != "eval-failed: criterion c1 still lacks evidence" || row["where"] != "judge" {
				t.Errorf("judge hold %v", row)
			}
			return
		}
	}
	t.Fatal("judge hold missing")
}
