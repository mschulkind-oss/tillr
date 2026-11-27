package main

import (
	"fmt"
	"net/http"
	"reflect"
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

func TestVerifyRefusalsReportRecordedCandidateSize(t *testing.T) {
	h := newServeHarness(t)
	cases := []struct {
		name, extra string
		want        map[string]any
	}{
		{"below limit", `,"candidate_size":{"files":3,"added_lines":71,"file_limit":8,"added_line_limit":100}`,
			map[string]any{"files": float64(3), "added_lines": float64(71), "file_limit": float64(8), "added_line_limit": float64(100)}},
		{"at limit", `,"candidate_size":{"files":9,"added_lines":80,"file_limit":9,"added_line_limit":80}`,
			map[string]any{"files": float64(9), "added_lines": float64(80), "file_limit": float64(9), "added_line_limit": float64(80)}},
		{"zero", `,"candidate_size":{"files":0,"added_lines":0,"file_limit":27,"added_line_limit":1200}`,
			map[string]any{"files": float64(0), "added_lines": float64(0), "file_limit": float64(27), "added_line_limit": float64(1200)}},
		{"older refusal", "", nil},
		{"measurement failed", `,"candidate_size_error":"missing candidate base"`, nil},
	}
	for i, tc := range cases {
		h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (?,'drain.refusal','run',?,?)`,
			2000+i, fmt.Sprintf("verify-%d", i), `{"item_id":"item-seed-06","step":"verify-not-done","detail":"c1: not done"`+tc.extra+`}`)
	}
	rows := verifyRefusalsFor(t, h, "item-seed-06")
	if len(rows) != len(cases) {
		t.Fatalf("want %d verify refusals, got %v", len(cases), rows)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := rows[i].(map[string]any)
			if row["detail"] != "c1: not done" {
				t.Errorf("refusal detail lost: %v", row)
			}
			if got := row["candidate_size"]; tc.want == nil {
				if got != nil {
					t.Errorf("unmeasured refusal has candidate_size: %v", got)
				}
			} else if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("candidate_size = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestVerifyRefusalsEmptyIsAList(t *testing.T) {
	h := newServeHarness(t)
	if rows := verifyRefusalsFor(t, h, "item-seed-06"); len(rows) != 0 {
		t.Fatalf("want none: %v", rows)
	}
}

func TestVerifyRefusalsIncludePendingSplitCallAndNoSplitAnswer(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE IF NOT EXISTS split_calls (
		call_id INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id TEXT NOT NULL,
		caller TEXT NOT NULL,
		run_id TEXT NOT NULL,
		donor_ref TEXT NOT NULL DEFAULT '',
		donor_sha TEXT NOT NULL DEFAULT '',
		base_sha TEXT NOT NULL DEFAULT '',
		signals TEXT NOT NULL,
		status TEXT NOT NULL,
		design_run_id TEXT NOT NULL DEFAULT '',
		no_split_reason TEXT NOT NULL DEFAULT '',
		last_rejection_reason TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		answered_at TEXT NOT NULL DEFAULT ''
	);`)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
		(2000,'drain.refusal','run','verify-1','{"item_id":"item-seed-06","step":"verify-not-done","detail":"c1: too big"}'),
		(5000,'drain.refusal','run','verify-2','{"item_id":"item-seed-06","step":"verify-not-done","detail":"c1: still big"}');
		INSERT INTO split_calls (item_id,caller,run_id,donor_ref,donor_sha,base_sha,signals,status,design_run_id,no_split_reason,created_at,answered_at) VALUES
		('item-seed-06','verifier','verify-1','refs/tillr/runs/verify-1','sha-donor-1','sha-base-1','[{"signal":"S1","files":30,"added_lines":1500,"file_limit":27,"added_line_limit":1200}]','no-split','design-1','one shared invariant prevents splitting','2026-11-27T01:00:00Z','2026-11-27T01:05:00Z'),
		('item-seed-06','verifier','verify-2','refs/tillr/runs/verify-2','sha-donor-2','sha-base-2','[{"signal":"S1","files":32,"added_lines":1600,"file_limit":27,"added_line_limit":1200}]','pending','','','2026-11-27T02:00:00Z','');`)

	rows := verifyRefusalsFor(t, h, "item-seed-06")
	if len(rows) != 2 {
		t.Fatalf("want 2 verify refusals, got %d: %v", len(rows), rows)
	}

	first, ok := rows[0].(map[string]any)["split_call"].(map[string]any)
	if !ok {
		t.Fatalf("verify_refusals[0].split_call missing: %v", rows[0])
	}
	if first["status"] != "no-split" || first["no_split_reason"] != "one shared invariant prevents splitting" || first["design_run_id"] != "design-1" {
		t.Errorf("unexpected no-split split_call: %v", first)
	}

	second, ok := rows[1].(map[string]any)["split_call"].(map[string]any)
	if !ok {
		t.Fatalf("verify_refusals[1].split_call missing: %v", rows[1])
	}
	wantFields := map[string]any{
		"caller":    "verifier",
		"status":    "pending",
		"donor_ref": "refs/tillr/runs/verify-2",
		"donor_sha": "sha-donor-2",
	}
	for k, want := range wantFields {
		if second[k] != want {
			t.Errorf("split_call.%s = %v, want %v", k, second[k], want)
		}
	}
	sigs, ok := second["signals"].([]any)
	if !ok || len(sigs) != 1 {
		t.Fatalf("split_call.signals = %v, want 1 signal", second["signals"])
	}
	sig := sigs[0].(map[string]any)
	if sig["signal"] != "S1" || sig["files"] != float64(32) || sig["file_limit"] != float64(27) || sig["added_lines"] != float64(1600) || sig["added_line_limit"] != float64(1200) {
		t.Errorf("unexpected signal: %v", sig)
	}
}
