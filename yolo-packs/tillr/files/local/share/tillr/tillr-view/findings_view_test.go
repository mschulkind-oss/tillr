package main

import (
	"reflect"
	"testing"
)

func TestItemFindingProjection(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
 (2000,'drain.refusal','run','verify-1','{"item_id":"item-seed-06","step":"verify-not-done","detail":"c1: incomplete","verify_feedback":{"findings":[{"criterion":"c1","reason":"incomplete","findings":[{"kind":"gap","case":"missing case","paths":["file:a.go:4","file:b.go"]},{"kind":"regression","case":"broken case","paths":["file:c.go"]}]},{"criterion":"c2","reason":"extra","findings":[{"kind":"unasked-change","case":"extra case","paths":["file:d.go"]}]}]}}'),
 (2100,'drain.refusal','run','verify-old','{"item_id":"item-seed-06","step":"verify-not-done","detail":"old"}'),
 (2200,'item.verdict','item','item-seed-06','{"criterion_id":"c1","verdict":"not-done","reason":"direct","findings":[{"kind":"gap","case":"direct case","paths":["file:e.go"]},{"kind":"unasked-change","case":"extra case","paths":["file:f.go"]}]}'),
 (2300,'item.verdict','item','item-seed-06','{"criterion_id":"c2","verdict":"not-done","reason":"old direct"}'),
 (2400,'drain.refusal','run','compat-old','{"item_id":"item-seed-06","step":"eval-failed","detail":"old compatibility refusal"}'),
 (2500,'drain.refusal','run','compat-1','{"item_id":"item-seed-06","step":"eval-failed","detail":"compatibility refusal","verify_feedback":{"findings":[{"criterion":"c3","reason":"broken","findings":[{"kind":"regression","case":"compatibility case","paths":["file:g.go:5"]}]}]}}'),
 (2600,'drain.refusal','run','blocked-old','{"item_id":"item-seed-06","step":"judge-blocked","detail":"old blocked refusal"}'),
 (2700,'drain.refusal','run','blocked-1','{"item_id":"item-seed-06","step":"judge-blocked","detail":"blocked for planning","verify_feedback":{"findings":[{"criterion":"c4","reason":"broken","findings":[{"kind":"regression","case":"blocked review case","paths":["file:h.go:6"]}]}]}}')`)
	rows := verifyRefusalsFor(t, h, "item-seed-06")
	if len(rows) != 4 {
		t.Fatalf("refusal rows = %v; want two planned and two classified review refusals", rows)
	}
	blocked := rows[3].(map[string]any)
	blockedFindings := []any{map[string]any{"criterion": "c4", "kind": "regression", "case": "blocked review case", "paths": []any{"file:h.go:6"}}}
	if blocked["run_id"] != "blocked-1" || blocked["detail"] != "blocked for planning" || !reflect.DeepEqual(blocked["findings"], blockedFindings) {
		t.Errorf("blocked refusal = %v; want controlling reason and findings %v", blocked, blockedFindings)
	}
	compatibility := rows[2].(map[string]any)
	compatibilityFindings := []any{map[string]any{"criterion": "c3", "kind": "regression", "case": "compatibility case", "paths": []any{"file:g.go:5"}}}
	if compatibility["run_id"] != "compat-1" || compatibility["detail"] != "compatibility refusal" || !reflect.DeepEqual(compatibility["findings"], compatibilityFindings) {
		t.Errorf("compatibility refusal = %v; want its original detail and findings %v", compatibility, compatibilityFindings)
	}
	want := []any{
		map[string]any{"criterion": "c1", "kind": "gap", "case": "missing case", "paths": []any{"file:a.go:4", "file:b.go"}},
		map[string]any{"criterion": "c1", "kind": "regression", "case": "broken case", "paths": []any{"file:c.go"}},
		map[string]any{"criterion": "c2", "kind": "unasked-change", "case": "extra case", "paths": []any{"file:d.go"}},
	}
	if got := rows[0].(map[string]any)["findings"]; !reflect.DeepEqual(got, want) {
		t.Errorf("refusal findings = %v; want %v", got, want)
	}
	if got := rows[1].(map[string]any)["findings"]; got != nil {
		t.Errorf("old refusal findings = %v", got)
	}
	_, body, _ := h.get("/api/v1/items/item-seed-06")
	verdicts := body["data"].(map[string]any)["verdicts"].([]any)
	if len(verdicts) != 2 {
		t.Fatalf("verdict rows = %v; want only the two directly recorded verdicts", verdicts)
	}
	direct := []any{map[string]any{"kind": "gap", "case": "direct case", "paths": []any{"file:e.go"}}, map[string]any{"kind": "unasked-change", "case": "extra case", "paths": []any{"file:f.go"}}}
	if got := verdicts[0].(map[string]any)["findings"]; !reflect.DeepEqual(got, direct) {
		t.Errorf("verdict findings = %v; want %v", got, direct)
	}
	if got := verdicts[1].(map[string]any)["findings"]; got != nil {
		t.Errorf("old verdict findings = %v", got)
	}
}

func TestItemRefusalComparisonFatesAndExamined(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
 (3000,'drain.refusal','run','verify-a','{"item_id":"item-seed-06","step":"verify-not-done","detail":"c1: first","candidate_size":{"files":2,"added_lines":40,"file_limit":27,"added_line_limit":1200},"verify_feedback":{"findings":[{"criterion":"c1","reason":"first","findings":[{"kind":"gap","case":"missing","paths":["file:a.go"]}]}],"examined":{"whole":true}}}'),
 (3100,'drain.refusal','run','verify-b','{"item_id":"item-seed-06","step":"verify-not-done","detail":"c1: second","candidate_size":{"files":5,"added_lines":160,"file_limit":27,"added_line_limit":1200},"verify_feedback":{"findings":[{"criterion":"c1","reason":"second","findings":[{"kind":"regression","case":"broken","paths":["file:b.go"]}]}],"earlier_finding_fates":[{"criterion_id":"c1","finding":"missing","paths":["file:a.go"],"fate":"fixed"}],"examined":{"whole":false,"unexamined_paths":["file:slow.go"]}},"verdict_comparison":{"previous_run_id":"verify-a","all_earlier_fixed":true,"new_finding_at_new_place":true,"new_finding_after_fix":true,"files_delta":3,"added_lines_delta":120,"repair_growing":true,"has_unexamined_paths":true}}'),
 (3200,'drain.refusal','run','verify-c','{"item_id":"item-seed-06","step":"verify-not-done","detail":"c1: third","candidate_size":{"files":4,"added_lines":150,"file_limit":27,"added_line_limit":1200}}'),
 (3300,'item.verdict','item','item-seed-06','{"criterion_id":"c1","verdict":"not-done","reason":"direct","earlier_finding_fates":[{"finding":"missing","fate":"fixed"}],"examined":{"whole":false,"unexamined_paths":["file:slow.go"]}}')`)

	rows := verifyRefusalsFor(t, h, "item-seed-06")
	if len(rows) != 3 {
		t.Fatalf("refusal rows = %d; want 3", len(rows))
	}
	first := rows[0].(map[string]any)
	if ex, ok := first["examined"].(map[string]any); !ok || ex["whole"] != true {
		t.Errorf("first examined = %v; want whole=true", first["examined"])
	}
	second := rows[1].(map[string]any)
	if fates, ok := second["earlier_finding_fates"].([]any); !ok || len(fates) != 1 {
		t.Errorf("second earlier_finding_fates = %v", second["earlier_finding_fates"])
	}
	if ex, ok := second["examined"].(map[string]any); !ok || ex["whole"] != false {
		t.Errorf("second examined = %v", second["examined"])
	}
	comp, ok := second["verdict_comparison"].(map[string]any)
	if !ok || comp["previous_run_id"] != "verify-a" || comp["new_finding_after_fix"] != true || comp["repair_growing"] != true || comp["has_unexamined_paths"] != true {
		t.Errorf("second verdict_comparison = %v", second["verdict_comparison"])
	}
	third := rows[2].(map[string]any)
	comp3, ok := third["verdict_comparison"].(map[string]any)
	if !ok || comp3["previous_run_id"] != "verify-b" || comp3["files_delta"] != float64(-1) || comp3["added_lines_delta"] != float64(-10) {
		t.Errorf("third fallback verdict_comparison = %v", third["verdict_comparison"])
	}

	_, body, _ := h.get("/api/v1/items/item-seed-06")
	verdicts := body["data"].(map[string]any)["verdicts"].([]any)
	if len(verdicts) != 1 {
		t.Fatalf("verdicts = %v; want 1", verdicts)
	}
	v0 := verdicts[0].(map[string]any)
	if fates, ok := v0["earlier_finding_fates"].([]any); !ok || len(fates) != 1 {
		t.Errorf("verdict earlier_finding_fates = %v", v0["earlier_finding_fates"])
	}
	if ex, ok := v0["examined"].(map[string]any); !ok || ex["whole"] != false {
		t.Errorf("verdict examined = %v", v0["examined"])
	}
}
