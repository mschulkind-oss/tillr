package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// /why shows the loop's last recorded pass. These cases count planner runs with a decoy
// planner that leaves a marker each time it is started.
func whyPassHarness(t *testing.T, live bool) (*serveHarness, func() int) {
	t.Helper()
	h := newServeHarnessWithQuestions(t, "", stubIndexCommand(t, `{"index":{"documents":[]}}`))
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	planner := filepath.Join(dir, "tillr-drain")
	body := `{"event_head_seq":1,"rows":[{"item_id":"planned","class":"ready","why":"x"},{"item_id":"gone","class":"done","why":"no further work"} ],"summary":{"held":{}}}`
	writeScript(t, planner, "cat >/dev/null\necho ran >> "+marker+"\nprintf '%s' '"+body+"'\n")
	h.srv.plannerFlag = planner
	h.exec(`CREATE TABLE IF NOT EXISTS design_without_item(document TEXT PRIMARY KEY, roadmap TEXT NOT NULL, roadmap_line INTEGER NOT NULL, first_seen INTEGER NOT NULL)`)
	h.exec(`INSERT INTO design_without_item VALUES ('docs/tillr/design.md','docs/tillr/roadmap.md',7,1000),('docs/pi/design.md','docs/pi/roadmap.md',3,1000)`)
	if err := os.WriteFile(filepath.Join(h.dir, ".tillr.toml"), []byte("[sync]\nroadmaps = [\"docs/tillr/roadmap.md\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if live {
		at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		h.setNow(at)
		now := at.UnixMilli()
		h.exec(`CREATE TABLE IF NOT EXISTS drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER)`)
		h.exec(fmt.Sprintf(`INSERT INTO drain_sessions VALUES ('live', 1, %d, %d, NULL)`, now, now+3600_000))
	}
	return h, func() int {
		raw, _ := os.ReadFile(marker)
		return strings.Count(string(raw), "ran")
	}
}

func TestWhyShowsTheLastRecordedPassWithoutPlanning(t *testing.T) {
	h, planned := whyPassHarness(t, true)
	status, body, raw := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, raw)
	}
	data := body["data"].(map[string]any)
	if planned() != 0 {
		t.Fatalf("the planner ran %d times; a recorded pass answers", planned())
	}
	source := data["source"].(map[string]any)
	if source["kind"] != "pass" || source["pass_id"] != "tick-2" || !strings.HasPrefix(source["text"].(string), "as of pass tick-2, ") {
		t.Fatalf("source: %v", source)
	}
	ids := map[string]map[string]any{}
	for _, v := range data["rows"].([]any) {
		row := v.(map[string]any)
		ids[row["item_id"].(string)] = row
	}
	if len(ids) != 2 || ids["item-seed-02"] == nil || ids["item-seed-06"] == nil || ids["item-seed-04"] != nil {
		t.Fatalf("rows are the open items only: %s", raw)
	}
	decision := ids["item-seed-02"]["decision"].(map[string]any)
	if decision["decision_id"] != "decision-item-seed-02-tick-2" || decision["action"] != "hold" || decision["reason"] != "not-ready" {
		t.Fatalf("decision: %v", decision)
	}
	if ids["item-seed-06"]["why"] != "not yet planned" {
		t.Fatalf("an item with no recorded decision says so: %v", ids["item-seed-06"])
	}
	designs := data["designs_waiting"].([]any)
	if len(designs) != 1 || designs[0].(map[string]any)["document"] != "docs/tillr/design.md" {
		t.Fatalf("designs come from the selected roadmap only: %v", designs)
	}
	for _, field := range []string{"alarms", "lanes", "summary"} {
		if _, ok := data[field]; !ok {
			t.Fatalf("missing %s: %s", field, raw)
		}
	}
	// Repeat requests never plan either, however many events arrive.
	h.appendEvent("run.progress", "run", "run-1")
	h.get("/api/v1/why")
	if planned() != 0 {
		t.Fatalf("the planner ran after a write")
	}
}

func TestWhyFallsBackToThePlannerWithNoLoop(t *testing.T) {
	h, planned := whyPassHarness(t, false)
	status, body, raw := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, raw)
	}
	data := body["data"].(map[string]any)
	if planned() != 1 {
		t.Fatalf("planner runs: %d, want 1", planned())
	}
	source := data["source"].(map[string]any)
	if source["kind"] != "planner" || !strings.Contains(source["fallback"].(string), "no loop is running") {
		t.Fatalf("source: %v", source)
	}
	rows := data["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["item_id"] != "planned" {
		t.Fatalf("done and design rows leave rows: %s", raw)
	}
	if len(data["designs_waiting"].([]any)) != 1 {
		t.Fatalf("designs_waiting: %s", raw)
	}
}

func TestWhyFallsBackWhenNoPassIsRecorded(t *testing.T) {
	h, planned := whyPassHarness(t, true)
	h.exec(`DELETE FROM drain_passes`)
	_, body, raw := h.get("/api/v1/why")
	source := body["data"].(map[string]any)["source"].(map[string]any)
	if planned() != 0 && source["kind"] != "planner" {
		t.Fatalf("unexpected source: %v %s", source, raw)
	}
	if planned() != 1 || source["kind"] != "planner" || !strings.Contains(source["fallback"].(string), "no pass is recorded") {
		t.Fatalf("fallback: %v %s", source, raw)
	}
}

func TestWhyQueuedItemsBehindBusyLanesShowLaneWaitInsteadOfNotYetPlanned(t *testing.T) {
	h, planned := whyPassHarness(t, true)
	at := time.Date(2026, 11, 23, 5, 20, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()

	// Configure 2 ordinary lanes and 1 express lane on the live session, plus rank and express columns on items.
	h.exec(`ALTER TABLE drain_sessions ADD COLUMN lanes TEXT`)
	h.exec(fmt.Sprintf(`UPDATE drain_sessions SET refreshed_at = %d, next_pass_at = %d, lanes = '["lane-1","lane-2","express-1"]' WHERE session_id = 'live'`, now, now+3600_000))
	h.exec(`ALTER TABLE items ADD COLUMN rank TEXT NOT NULL DEFAULT ''`)
	h.exec(`ALTER TABLE items ADD COLUMN express INTEGER NOT NULL DEFAULT 0`)
	h.exec(`ALTER TABLE dispatches ADD COLUMN pass_id TEXT NOT NULL DEFAULT ''`)

	h.exec(`DELETE FROM dispatches; DELETE FROM item_edges; DELETE FROM runs; DELETE FROM lanes; DELETE FROM items; DELETE FROM drain_passes`)
	h.exec(fmt.Sprintf(`INSERT INTO drain_passes VALUES ('tick-9', %d, 12, 4, 3, 5, 0, '')`, now-30_000))

	// All 3 lanes (lane-1, lane-2, express-1) are busy, and there are more queued items than lanes:
	//   #1 item-run-1 (running on lane-1)
	//   #2 item-run-2 (running on lane-2)
	//   #3 item-idea-run-3 (express, running on express-1)
	//   #4 item-ord-wait-1 (ordinary, in-queue, waiting behind busy lanes)
	//   #5 item-ord-wait-2 (ordinary, in-queue, waiting behind busy lanes)
	//   #6 item-exp-wait-1 (express column = 1, in-queue, waiting behind busy lanes)
	//   #7 item-idea-wait-2 (express by item-idea- prefix, in-queue, waiting behind busy lanes)
	//   #8 item-unseen (in-queue, added after the loop's latest pass and refresh — never looked at yet)
	//   parked item-parked (not-started — never looked at yet)
	queuedAt := now - 60_000
	h.exec(fmt.Sprintf(`INSERT INTO items(item_id, title, state, updated_at, rank, express) VALUES
		('item-run-1',       'Running one',         'in-queue',    %d, 'a', 0),
		('item-run-2',       'Running two',         'in-queue',    %d, 'b', 0),
		('item-idea-run-3',  'Running express',     'in-queue',    %d, 'c', 0),
		('item-ord-wait-1',  'Queued ordinary one', 'in-queue',    %d, 'd', 0),
		('item-ord-wait-2',  'Queued ordinary two', 'in-queue',    %d, 'e', 0),
		('item-exp-wait-1',  'Queued express col',  'in-queue',    %d, 'f', 1),
		('item-idea-wait-2', 'Queued idea express', 'in-queue',    %d, 'g', 0),
		('item-unseen',      'Filed after refresh', 'in-queue',    %d, 'h', 0),
		('item-parked',      'Parked draft',        'not-started', %d, 'i', 0)`,
		queuedAt, queuedAt, queuedAt, queuedAt+1000, queuedAt+2000, queuedAt+3000, queuedAt+4000, now+500, queuedAt))

	h.exec(fmt.Sprintf(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES
		('lane-1',    'leased', 'run-1', %d),
		('lane-2',    'leased', 'run-2', %d),
		('express-1', 'leased', 'run-3', %d)`, now+600_000, now+600_000, now+600_000))
	h.exec(fmt.Sprintf(`INSERT INTO dispatches(decision_id, item_id, run_id, lane_id, action, reason, dispatched_at, pass_id) VALUES
		('d-1', 'item-run-1',      'run-1', 'lane-1',    'dispatch', 'ready', %d, 'tick-9'),
		('d-2', 'item-run-2',      'run-2', 'lane-2',    'dispatch', 'ready', %d, 'tick-9'),
		('d-3', 'item-idea-run-3', 'run-3', 'express-1', 'dispatch', 'ready', %d, 'tick-9')`,
		now-30_000, now-30_000, now-30_000))

	status, body, raw := h.get("/api/v1/why")
	if status != 200 || planned() != 0 {
		t.Fatalf("why status=%d planned=%d: %s", status, planned(), raw)
	}
	data := body["data"].(map[string]any)
	rows := map[string]map[string]any{}
	for _, v := range data["rows"].([]any) {
		row := v.(map[string]any)
		rows[row["item_id"].(string)] = row
	}

	assertWaitingForLane := func(id string, wantPlace int, wantAhead, wantUse, wantMover string, wantSinceMS int64) {
		t.Helper()
		row := rows[id]
		if row == nil {
			t.Fatalf("missing row %s in %s", id, raw)
		}
		if int(row["place"].(float64)) != wantPlace {
			t.Fatalf("%s place = %v, want %d", id, row["place"], wantPlace)
		}
		if row["class"] != "waits on work" || row["where"] != "queue" || row["reason"] != "no-lane" {
			t.Fatalf("%s classification = %v", id, row)
		}
		why := row["why"].(string)
		for _, fragment := range []string{"waiting for a lane", fmt.Sprintf("place %d", wantPlace), wantAhead, wantUse} {
			if !strings.Contains(why, fragment) {
				t.Fatalf("%s why = %q, missing %q", id, why, fragment)
			}
		}
		if strings.Contains(why, "not yet planned") {
			t.Fatalf("%s why still says not yet planned: %q", id, why)
		}
		if row["mover"] != wantMover {
			t.Fatalf("%s mover = %q, want %q", id, row["mover"], wantMover)
		}
		wantSince := time.UnixMilli(wantSinceMS).UTC().Format(time.RFC3339Nano)
		if row["since"] != wantSince {
			t.Fatalf("%s since = %q, want %q", id, row["since"], wantSince)
		}
	}

	assertWaitingForLane("item-ord-wait-1", 4, "3 items ahead", "ordinary lanes (lane-1, lane-2)", "a free ordinary lane (lane-1, lane-2)", queuedAt+1000)
	assertWaitingForLane("item-ord-wait-2", 5, "4 items ahead", "ordinary lanes (lane-1, lane-2)", "a free ordinary lane (lane-1, lane-2)", queuedAt+2000)
	assertWaitingForLane("item-exp-wait-1", 6, "5 items ahead", "ordinary or express lanes (lane-1, lane-2, express-1)", "a free ordinary or express lane (lane-1, lane-2, express-1)", queuedAt+3000)
	assertWaitingForLane("item-idea-wait-2", 7, "6 items ahead", "ordinary or express lanes (lane-1, lane-2, express-1)", "a free ordinary or express lane (lane-1, lane-2, express-1)", queuedAt+4000)

	// Work the loop has genuinely not looked at keeps 'not yet planned' / 'the next pass' / 'no pass yet'.
	for _, unseenID := range []string{"item-unseen", "item-parked"} {
		row := rows[unseenID]
		if row["class"] != "waits on the loop" || row["why"] != "not yet planned" || row["mover"] != "the next pass" || row["since"] != "no pass yet" {
			t.Fatalf("%s should show as never looked at by the loop: %v", unseenID, row)
		}
	}

	summary := data["summary"].(map[string]any)
	held := summary["held"].(map[string]any)
	if int(held["no-lane"].(float64)) != 4 || int(held["not yet planned"].(float64)) != 2 {
		t.Fatalf("summary held = %v, want no-lane=4 and not yet planned=2", held)
	}
	if int(summary["lanes_offered"].(float64)) != 3 || int(summary["lanes_busy"].(float64)) != 3 {
		t.Fatalf("summary lanes = %v", summary)
	}

	// When express-1 frees up while lane-1 and lane-2 stay busy, ordinary items still wait on
	// ordinary lanes, while express items now have a free lane and wait on the next pass.
	h.exec(`DELETE FROM dispatches WHERE decision_id = 'd-3'; UPDATE items SET state = 'landed' WHERE item_id = 'item-idea-run-3'; UPDATE lanes SET state = 'idle', run_id = NULL, leased_until = NULL WHERE lane_id = 'express-1'`)
	_, body, raw = h.get("/api/v1/why")
	rows = map[string]map[string]any{}
	for _, v := range body["data"].(map[string]any)["rows"].([]any) {
		row := v.(map[string]any)
		rows[row["item_id"].(string)] = row
	}
	// With item-idea-run-3 landed, item-ord-wait-1 moves to place 3 (2 items ahead) and still waits on ordinary lanes.
	assertWaitingForLane("item-ord-wait-1", 3, "2 items ahead", "ordinary lanes (lane-1, lane-2)", "a free ordinary lane (lane-1, lane-2)", queuedAt+1000)
	if rows["item-exp-wait-1"]["why"] != "not yet planned" || rows["item-exp-wait-1"]["mover"] != "the next pass" || rows["item-exp-wait-1"]["since"] != "no pass yet" {
		t.Fatalf("express item with a free express lane should wait on the next pass, got %v", rows["item-exp-wait-1"])
	}

	// Singular queue counts (0 items ahead, 1 item ahead) and single-lane pool labels.
	h.exec(fmt.Sprintf(`UPDATE drain_sessions SET lanes = '["lane-1"]' WHERE session_id = 'live'; DELETE FROM dispatches; DELETE FROM items; DELETE FROM lanes; INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES ('lane-1', 'leased', 'run-settling', %d)`, now+600_000))
	h.exec(fmt.Sprintf(`INSERT INTO items(item_id, title, state, updated_at, rank, express) VALUES
		('item-first',  'First queued',  'in-queue', %d, 'a', 0),
		('item-second', 'Second queued', 'in-queue', %d, 'b', 0)`, queuedAt, queuedAt+1000))
	_, body, raw = h.get("/api/v1/why")
	rows = map[string]map[string]any{}
	for _, v := range body["data"].(map[string]any)["rows"].([]any) {
		row := v.(map[string]any)
		rows[row["item_id"].(string)] = row
	}
	assertWaitingForLane("item-first", 1, "0 items ahead", "ordinary lane (lane-1)", "a free ordinary lane (lane-1)", queuedAt)
	assertWaitingForLane("item-second", 2, "1 item ahead", "ordinary lane (lane-1)", "a free ordinary lane (lane-1)", queuedAt+1000)

	// Express-only lane pool: express item names express lane; ordinary item cannot use express-1.
	h.exec(fmt.Sprintf(`UPDATE drain_sessions SET lanes = '["express-1"]' WHERE session_id = 'live'; DELETE FROM lanes; INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES ('express-1', 'leased', 'run-exp', %d); UPDATE items SET express = 1 WHERE item_id = 'item-second'`, now+600_000))
	_, body, raw = h.get("/api/v1/why")
	rows = map[string]map[string]any{}
	for _, v := range body["data"].(map[string]any)["rows"].([]any) {
		row := v.(map[string]any)
		rows[row["item_id"].(string)] = row
	}
	if rows["item-first"]["why"] != "not yet planned" {
		t.Fatalf("ordinary item must not claim express-1: %v", rows["item-first"])
	}
	assertWaitingForLane("item-second", 2, "1 item ahead", "express lane (express-1)", "a free express lane (express-1)", queuedAt+1000)
}
