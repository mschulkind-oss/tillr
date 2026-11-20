package main

import (
	"fmt"
	"net/http"
	"testing"
)

// started is a drain.started payload for one run of the item: a workflow node when node >= 0,
// otherwise a plain dispatch that names only its seat.
func startedPayload(item, seat string, node int) string {
	if node < 0 {
		return fmt.Sprintf(`{"decision":{"item_id":%q,"seat":%q}}`, item, seat)
	}
	return fmt.Sprintf(`{"decision":{"item_id":%q,"seat":%q,"node_index":%d,"workflow":{"stages":[{"id":"plan","seat":"plan"},{"id":"work","seat":"build"},{"id":"verify","seat":"verify"}],"edges":[]}}}`, item, seat, node)
}

func verifyRefusalsFor(t *testing.T, h *serveHarness, item string) []any {
	t.Helper()
	status, body, raw := h.get("/api/v1/items/" + item)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	rows, ok := body["data"].(map[string]any)["verify_refusals"].([]any)
	if !ok {
		t.Fatalf("verify_refusals is not a list: %s", raw)
	}
	return rows
}

func TestVerifyRefusalsNameTheStepThatRanNext(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE node_runs(item_id TEXT,attempt INTEGER,node_id TEXT,run_id TEXT,outcome TEXT);
 INSERT INTO node_runs VALUES('item-seed-06',1,'verify','verify-1','failed'),('item-seed-06',1,'work','work-2','passed'),('item-seed-06',2,'verify','verify-2','failed');`)
	refusal := func(run, detail, extra string) string {
		return fmt.Sprintf(`{"run_id":%q,"item_id":"item-seed-06","step":"verify-not-done","detail":%q%s}`, run, detail, extra)
	}
	// Refusal one is followed by the build step; refusal two by the plan step; refusal three by nothing.
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
 (1000,'drain.started','run','verify-1',?),
 (2000,'drain.refusal','run','verify-1',?),
 (3000,'drain.node-advanced','run','work-2',?),
 (4000,'drain.node-advanced','run','verify-2',?),
 (5000,'drain.refusal','run','verify-2',?),
 (6000,'drain.started','run','plan-3',?),
 (7000,'drain.refusal','run','verify-3',?),
 (7100,'drain.refusal','run','other-verify','{"item_id":"item-seed-02","step":"verify-not-done","detail":"not this item"}'),
 (7200,'drain.refusal','run','gate-run','{"item_id":"item-seed-06","step":"conflict","detail":"not a verify refusal"}')`,
		startedPayload("item-seed-06", "verify", 2), refusal("verify-1", "c1: missing test\nc2: wrong file", ""),
		startedPayload("item-seed-06", "build", 1), startedPayload("item-seed-06", "verify", 2),
		refusal("verify-2", "the plan was wrong", `,"replan":true`), startedPayload("item-seed-06", "plan", 0),
		refusal("verify-3", "c1: still red", ""))
	rows := verifyRefusalsFor(t, h, "item-seed-06")
	if len(rows) != 3 {
		t.Fatalf("want 3 verify refusals, got %d: %v", len(rows), rows)
	}
	want := []struct {
		run, detail string
		replan      bool
		attempt     any
		step, seat  any
		next        any
	}{
		{"verify-1", "c1: missing test\nc2: wrong file", false, float64(1), "work", "build", "work-2"},
		{"verify-2", "the plan was wrong", true, float64(2), "plan", "plan", "plan-3"},
		{"verify-3", "c1: still red", false, nil, nil, nil, nil},
	}
	for i, w := range want {
		got := rows[i].(map[string]any)
		for field, expect := range map[string]any{"run_id": w.run, "detail": w.detail, "replan": w.replan, "attempt": w.attempt,
			"next_step": w.step, "next_seat": w.seat, "next_run_id": w.next} {
			if got[field] != expect {
				t.Errorf("verify_refusals[%d].%s = %v; want %v", i, field, got[field], expect)
			}
		}
		if got["at_ms"] == nil || got["event_seq"] == nil {
			t.Errorf("verify_refusals[%d] has no recorded time or sequence: %v", i, got)
		}
		if (w.next == nil) != (got["next_event_seq"] == nil && got["next_at_ms"] == nil) {
			t.Errorf("verify_refusals[%d] next event %v/%v disagrees with next run %v", i, got["next_event_seq"], got["next_at_ms"], w.next)
		}
	}
}

func TestVerifyRefusalsNextStepOutsideAWorkflowIsTheSeat(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
 (2000,'drain.refusal','run','verify-1','{"item_id":"item-seed-06","step":"verify-not-done","detail":"d"}'),
 (3000,'drain.started','run','design-2',?)`, startedPayload("item-seed-06", "design", -1))
	rows := verifyRefusalsFor(t, h, "item-seed-06")
	got := rows[0].(map[string]any)
	if len(rows) != 1 || got["next_step"] != "design" || got["next_attempt"] != nil {
		t.Fatalf("want the design seat as the next step: %v", rows)
	}
}

func TestVerifyRefusalsEmptyIsAList(t *testing.T) {
	h := newServeHarness(t)
	if rows := verifyRefusalsFor(t, h, "item-seed-06"); len(rows) != 0 {
		t.Fatalf("want none: %v", rows)
	}
}
