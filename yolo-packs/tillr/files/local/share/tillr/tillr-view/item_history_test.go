package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestItemHistoryWithoutCandidates(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE runs ADD COLUMN end_cause TEXT`)
	h.exec(`CREATE TABLE decisions (decision_id TEXT PRIMARY KEY, text TEXT, scope_kind TEXT, scope_ref TEXT, decided_by TEXT, decided_at INTEGER, status TEXT, supersedes TEXT, session_id TEXT)`)
	h.exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,event_seq,usage_tokens,usage_cost_usd,end_cause) VALUES
 ('history-b','item-seed-06','lane-b','claude','finished',1,2000,2500,'[]',0,NULL,NULL,'failed-to-start'),
 ('history-a','item-seed-06','lane-b','pi','finished',0,1000,1500,'[]',0,42,0.5,'normal-exit')`)
	h.exec(`INSERT INTO decisions VALUES
 ('ledger-b','try again','items','item-seed-02, item-seed-06','owner',3500,'active','ledger-a','owner-session'),
 ('ledger-a','first ruling','items','item-seed-06','owner',3000,'superseded',NULL,'owner-session'),
 ('ledger-other','not this item','items','item-seed-060','owner',3000,'active',NULL,'owner-session')`)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
 (500,'item.add','item','item-seed-06','{"state":"in-queue"}'),
 (2600,'item.verdict','item','item-seed-06','{"criterion_id":"ac-1","verdict":"not-done","reason":"no candidate","evidence":["run:history-b"],"actor":"reviewer","actor_kind":"human"}'),
 (4000,'item.update','item','item-seed-06','{"state":"closed","lifecycle_action":"close","lifecycle_reason":"not wanted","lifecycle_by":"owner"}'),
 (4100,'item.update','item','item-seed-06','{"state":"closed","title":"edit only"}'),
 (5000,'item.update','item','item-seed-06','{"state":"in-queue","lifecycle_action":"reopen","lifecycle_reason":"wanted again","lifecycle_by":"owner"}'),
 (6000,'item.update','item','item-seed-06','{"state":"blocked","waiting_on":{"reason":"needs ruling"},"actor":"planner"}')`)
	h.exec(`INSERT INTO events (ts, kind, actor, object_type, object_id, payload) VALUES
 (7000, 'item.update', 'legacy-owner', 'item', 'item-seed-06', '{"state":"in-queue","reason":"legacy event attribution"}')`)
	status, body, raw := h.get("/api/v1/items/item-seed-06")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	data := body["data"].(map[string]any)
	assertRows := func(key string, want []map[string]any) {
		t.Helper()
		rows, ok := data[key].([]any)
		if !ok || len(rows) != len(want) {
			t.Fatalf("%s: want %d entries: %s", key, len(want), raw)
		}
		for i, expected := range want {
			for field, value := range expected {
				if !reflect.DeepEqual(rows[i].(map[string]any)[field], value) {
					t.Errorf("%s[%d].%s = %v; want %v", key, i, field, rows[i].(map[string]any)[field], value)
				}
			}
		}
	}
	assertRows("runs", []map[string]any{
		{"run_id": "history-a", "backend": "pi", "end_cause": "normal-exit", "started_at_ms": float64(1000), "finished_at_ms": float64(1500), "usage_tokens": float64(42), "cost_usd": nil},
		{"run_id": "history-b", "backend": "claude", "end_cause": "failed-to-start", "started_at_ms": float64(2000), "finished_at_ms": float64(2500), "usage_tokens": nil, "cost_usd": nil},
	})
	assertRows("verdicts", []map[string]any{{"criterion_id": "ac-1", "verdict": "not-done", "reason": "no candidate", "evidence": []any{"run:history-b"}, "actor": "reviewer"}})
	assertRows("owner_decisions", []map[string]any{
		{"decision_id": "ledger-a", "text": "first ruling", "status": "superseded", "decided_by": "owner", "decided_at_ms": float64(3000)},
		{"decision_id": "ledger-b", "text": "try again", "supersedes": "ledger-a", "session_id": "owner-session", "decided_at_ms": float64(3500)},
	})
	assertRows("state_moves", []map[string]any{
		{"from": "", "to": "in-queue", "at_ms": float64(500)},
		{"from": "in-queue", "to": "closed", "action": "close", "reason": "not wanted", "actor": "owner", "at_ms": float64(4000)},
		{"from": "closed", "to": "in-queue", "action": "reopen", "reason": "wanted again", "actor": "owner", "at_ms": float64(5000)},
		{"from": "in-queue", "to": "blocked", "reason": "needs ruling", "actor": "planner", "at_ms": float64(6000)},
		{"from": "blocked", "to": "in-queue", "reason": "legacy event attribution", "actor": "legacy-owner", "at_ms": float64(7000)},
	})
	for _, key := range []string{"item", "criteria", "blocked_by", "decisions", "attempts", "closed_blockers", "usage"} {
		if _, ok := data[key]; !ok {
			t.Errorf("existing key %s missing", key)
		}
	}
	usage := data["usage"].(map[string]any)
	if usage["tokens"] != float64(42) || usage["unrecorded_runs"] != float64(1) || usage["lower_bound"] != true {
		t.Errorf("existing usage summary changed: %v", usage)
	}
	if len(data["attempts"].([]any)) != 0 {
		t.Fatal("fixture unexpectedly has a candidate")
	}
}

func TestItemHistoryMissingSpendColumns(t *testing.T) {
	for _, missing := range [][]string{{"usage_tokens"}, {"usage_cost_usd"}, {"usage_tokens", "usage_cost_usd"}} {
		t.Run(strings.Join(missing, "+"), func(t *testing.T) {
			h := newServeHarness(t)
			for _, column := range missing {
				h.exec(`ALTER TABLE runs DROP COLUMN ` + column)
			}
			status, body, raw := h.get("/api/v1/items/item-seed-02")
			if status != http.StatusOK {
				t.Fatalf("status %d: %s", status, raw)
			}
			rows := body["data"].(map[string]any)["runs"].([]any)
			if len(rows) != 1 {
				t.Fatalf("legacy run missing: %s", raw)
			}
			row := rows[0].(map[string]any)
			// The HTTP observatory strips prices even when the store records them.
			want := map[string]any{"run_id": "run-1", "usage_tokens": float64(1234), "cost_usd": nil}
			for _, column := range missing {
				if column == "usage_tokens" {
					want["usage_tokens"] = nil
				} else {
					want["cost_usd"] = nil
				}
			}
			for key, value := range want {
				if !reflect.DeepEqual(row[key], value) {
					t.Errorf("%s = %v; want %v", key, row[key], value)
				}
			}
		})
	}
}

func TestItemHistoryOlderStore(t *testing.T) {
	h := newServeHarness(t)
	status, body, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	data := body["data"].(map[string]any)
	rows, ok := data["runs"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("runs: %s", raw)
	}
	if rows[0].(map[string]any)["end_cause"] != nil {
		t.Fatalf("unrecorded end cause must be null: %s", raw)
	}
	for _, key := range []string{"owner_decisions", "state_moves"} {
		if _, ok := data[key].([]any); !ok {
			t.Errorf("%s must be an array: %s", key, raw)
		}
	}
}
