package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func timingMetricOf(t *testing.T, data map[string]any, name string) map[string]any {
	t.Helper()
	m, ok := data["timing"].(map[string]any)[name].(map[string]any)
	if !ok {
		t.Fatalf("timing.%s missing: %+v", name, data["timing"])
	}
	return m
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunPageTimingSplitAndSchedule(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `
		INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,event_seq)
		VALUES ('run-t','item-seed-02','lane-a','pi','finished',0,1000,61000,'[]',20),
		       ('run-nolog','item-seed-02','lane-b','pi','finished',0,1000,2000,'[]',21);
		INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,dispatched_at)
		VALUES ('dec-t','item-seed-02','lane-a','run-t','dispatch','ready',900);
		INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
		 (62000,'drain.refusal','run','run-t','{"run_id":"run-t","item_id":"item-seed-02","step":"gate","detail":"red check"}'),
		 (1000,'run.allocate','run','run-t','{"run_id":"run-t","item_id":"item-seed-02","lane_id":"lane-a"}'),
		 (30000,'run.progress','run','run-t','{"run_id":"run-t"}'),
		 (61500,'gate.verdict','run','run-t','{"run_id":"run-t","result":"fail"}');
	`)
	log := strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"read-1","name":"read","arguments":{"path":"tests/fixture.go"}}],"timestamp":1000,"durationMs":2000}}`,
		// File-read output quoting shard notices must NOT be mistaken for a real slot wait.
		`{"type":"message_end","message":{"role":"toolResult","toolCallId":"read-1","toolName":"read","content":[{"type":"text","text":"go-test-shards: waiting for a test slot after 99s: fixture"}],"timestamp":5000,"durationMs":1000}}`,
		`{"type":"message_end","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"go-test-shards: github.com/mschulkind-oss/tillr-view shard 2 waiting for a test slot: busy\ngo-test-shards: github.com/mschulkind-oss/tillr-view shard 2 waiting for a test slot after 35s: busy"}],"timestamp":50000,"durationMs":44000}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[],"timestamp":51000,"durationMs":3000}}`,
		``,
	}, "\n")
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-t.log"), log)

	status, body, raw := h.get("/api/v1/runs/run-t")
	if status != 200 {
		t.Fatalf("run-t: %d %s", status, raw)
	}
	data := body["data"].(map[string]any)
	want := map[string]float64{
		"wall_ms":      60000,
		"model_ms":     5000,
		"tool_ms":      45000,
		"slot_wait_ms": 35000,
		"uncovered_ms": 10000,
	}
	for name, ms := range want {
		if got := timingMetricOf(t, data, name)["ms"]; got != ms {
			t.Fatalf("%s = %v, want %v (%s)", name, got, ms, raw)
		}
	}
	// A lower_bound slot wait from log notices has no interval bounds, so tool_active_ms stays absent.
	if act := timingMetricOf(t, data, "tool_active_ms"); act["ms"] != nil || act["epistemic_state"] != "absent" {
		t.Fatalf("tool_active_ms with lower_bound slot_wait = %+v, want absent/nil", act)
	}
	if st := timingMetricOf(t, data, "slot_wait_ms")["epistemic_state"]; st != "lower_bound" {
		t.Fatalf("slot wait read from notices must be a lower bound, got %v", st)
	}
	if st := timingMetricOf(t, data, "wall_ms")["epistemic_state"]; st != "observed" {
		t.Fatalf("wall_ms state = %v", st)
	}

	var kinds []string
	var last float64
	for _, e := range data["schedule"].([]any) {
		entry := e.(map[string]any)
		if at := entry["at_ms"].(float64); at < last {
			t.Fatalf("schedule out of time order: %s", raw)
		} else {
			last = at
		}
		kinds = append(kinds, entry["kind"].(string))
	}
	if strings.Join(kinds, ",") != "dispatch,run.allocate,gate.verdict,drain.refusal" {
		t.Fatalf("schedule kinds = %v (run.progress is the agent's, not a decision)", kinds)
	}

	status, body, raw = h.get("/api/v1/runs/run-nolog")
	if status != 200 {
		t.Fatalf("run-nolog: %d %s", status, raw)
	}
	data = body["data"].(map[string]any)
	for _, name := range []string{"model_ms", "tool_ms", "tool_active_ms", "slot_wait_ms", "uncovered_ms"} {
		m := timingMetricOf(t, data, name)
		if m["ms"] != nil || m["epistemic_state"] != "absent" || m["note"] == "" {
			t.Fatalf("%s must say it is not recorded, got %+v", name, m)
		}
	}
}

func TestRunTimingSlotWaitsUnionImmediateUntimedAndNoShardRunner(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `
		INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,event_seq)
		VALUES ('run-union','item-seed-02','lane-a','pi','finished',0,10000,50000,'[]',30),
		       ('run-imm','item-seed-02','lane-a','pi','finished',0,10000,20000,'[]',31),
		       ('run-untimed','item-seed-02','lane-a','pi','finished',0,10000,35000,'[]',32),
		       ('run-noshards','item-seed-02','lane-a','pi','finished',0,10000,20000,'[]',33),
		       ('run-shards-unrecorded','item-seed-02','lane-a','pi','finished',0,10000,25000,'[]',34);
	`)

	// 1. Overlapping concurrent shard waits union to [15000, 35000] = 20000ms (never 30000ms sum),
	// and overhang [8000, 12000] clips to [10000, 12000] = 2000ms -> total 22000ms.
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-union.log"), strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"python3 tools/go-test-shards.py tools/tillr-view"}}],"timestamp":10000,"durationMs":2000}}`,
		`{"type":"message_end","message":{"role":"toolResult","toolCallId":"c1","content":[{"type":"text","text":"ok"}],"timestamp":42000,"durationMs":30000}}`,
	}, "\n")+"\n")
	writeTestFile(t, filepath.Join(h.loopDir, "run-union", "slot-waits.jsonl"), strings.Join([]string{
		`{"kind":"slot.wait","ts_start_ms":8000,"ts_end_ms":12000,"waited_ms":4000,"slot":"slot-0","class":"ordinary","run":"run-union","label":"pkg shard 0","acquired":true}`,
		`{"kind":"slot.wait","ts_start_ms":15000,"ts_end_ms":30000,"waited_ms":15000,"slot":"slot-0","class":"ordinary","run":"run-union","label":"pkg shard 1","acquired":true}`,
		`{"kind":"slot.wait","ts_start_ms":20000,"ts_end_ms":35000,"waited_ms":15000,"slot":"slot-1","class":"ordinary","run":"run-union","label":"pkg shard 2","acquired":true}`,
	}, "\n")+"\n")

	status, body, raw := h.get("/api/v1/runs/run-union")
	if status != 200 {
		t.Fatalf("run-union: %d %s", status, raw)
	}
	data := body["data"].(map[string]any)
	if got := timingMetricOf(t, data, "slot_wait_ms")["ms"]; got != float64(22000) {
		t.Fatalf("slot_wait_ms = %v, want 22000 unioned+clipped (%s)", got, raw)
	}
	if st := timingMetricOf(t, data, "slot_wait_ms")["epistemic_state"]; st != "observed" {
		t.Fatalf("slot_wait_ms state = %v, want observed", st)
	}
	// Tool [12000, 42000] minus slot_wait [10000,12000] U [15000,35000] leaves [12000,15000] + [35000,42000] = 10000ms.
	if got := timingMetricOf(t, data, "tool_active_ms")["ms"]; got != float64(10000) {
		t.Fatalf("tool_active_ms = %v, want 10000 (%s)", got, raw)
	}

	// Timeline route exposes slot_wait spans for the waterfall.
	tStatus, tBody, tRaw := h.get("/api/v1/runs/run-union/timeline")
	if tStatus != 200 {
		t.Fatalf("run-union timeline: %d %s", tStatus, tRaw)
	}
	slotSpans := 0
	for _, s := range tBody["data"].(map[string]any)["spans"].([]any) {
		sp := s.(map[string]any)
		if sp["category"] == "slot_wait" {
			slotSpans++
		}
	}
	if slotSpans != 3 {
		t.Fatalf("slot_wait spans on timeline = %d, want 3 (%s)", slotSpans, tRaw)
	}

	// 2. Immediate acquisition (waited_ms == 0) in slot-waits.jsonl reports observed 0ms.
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-imm.log"), strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","content":[],"timestamp":10000,"durationMs":1000}}`,
		`{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"ok"}],"timestamp":15000,"durationMs":4000}}`,
	}, "\n")+"\n")
	writeTestFile(t, filepath.Join(h.loopDir, "run-imm", "slot-waits.jsonl"),
		`{"kind":"slot.wait","ts_start_ms":11000,"ts_end_ms":11000,"waited_ms":0,"slot":"slot-0","class":"ordinary","run":"run-imm","acquired":true}`+"\n")
	_, body, _ = h.get("/api/v1/runs/run-imm")
	immWait := timingMetricOf(t, body["data"].(map[string]any), "slot_wait_ms")
	if immWait["ms"] != float64(0) || immWait["epistemic_state"] != "observed" {
		t.Fatalf("immediate slot wait = %+v, want observed 0", immWait)
	}

	// 3. Untimed <30s notice ("waiting for a test slot: ... busy" with NO "after Ns") must report absent (nil), never 0ms.
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-untimed.log"), strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","content":[],"timestamp":10000,"durationMs":1000}}`,
		`{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"go-test-shards: pkg shard 1 waiting for a test slot: ordinary share (8 slots) busy"}],"timestamp":30000,"durationMs":18000}}`,
	}, "\n")+"\n")
	_, body, _ = h.get("/api/v1/runs/run-untimed")
	untimed := timingMetricOf(t, body["data"].(map[string]any), "slot_wait_ms")
	if untimed["ms"] != nil || untimed["epistemic_state"] != "absent" {
		t.Fatalf("untimed <30s notice must be absent (not 0ms), got %+v", untimed)
	}
	if active := timingMetricOf(t, body["data"].(map[string]any), "tool_active_ms"); active["ms"] != nil {
		t.Fatalf("tool_active_ms with unrecorded slot wait must be nil, got %+v", active)
	}

	// 4. Observed tool inputs that never invoked go-test-shards report observed 0ms slot wait.
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-noshards.log"), strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-git","name":"bash","arguments":{"command":"git status --short"}}],"timestamp":10000,"durationMs":1000}}`,
		`{"type":"message_end","message":{"role":"toolResult","toolCallId":"c-git","content":[{"type":"text","text":"clean"}],"timestamp":14000,"durationMs":3000}}`,
	}, "\n")+"\n")
	_, body, _ = h.get("/api/v1/runs/run-noshards")
	noShards := body["data"].(map[string]any)
	if m := timingMetricOf(t, noShards, "slot_wait_ms"); m["ms"] != float64(0) || m["epistemic_state"] != "observed" {
		t.Fatalf("no-shards slot_wait_ms = %+v, want observed 0", m)
	}
	if m := timingMetricOf(t, noShards, "tool_active_ms"); m["ms"] != float64(3000) {
		t.Fatalf("no-shards tool_active_ms = %+v, want 3000", m)
	}

	// 5. Observed go-test-shards call with no slot-waits.jsonl and no timed notice reports absent.
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-shards-unrecorded.log"), strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"c-sh","name":"bash","arguments":{"command":"python3 tools/go-test-shards.py tools/tillr-view"}}],"timestamp":10000,"durationMs":1000}}`,
		`{"type":"message_end","message":{"role":"toolResult","toolCallId":"c-sh","content":[{"type":"text","text":"ok"}],"timestamp":22000,"durationMs":10000}}`,
	}, "\n")+"\n")
	_, body, _ = h.get("/api/v1/runs/run-shards-unrecorded")
	if m := timingMetricOf(t, body["data"].(map[string]any), "slot_wait_ms"); m["ms"] != nil || m["epistemic_state"] != "absent" {
		t.Fatalf("unrecorded go-test-shards slot_wait_ms = %+v, want absent", m)
	}
}

func TestRunTimingHarnessesTurnEndProcFallbackAndLiveClaude(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `
		INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,event_seq)
		VALUES ('run-turnend','item-seed-02','lane-a','pi','finished',0,1000,10000,'[]',40),
		       ('run-proc','item-seed-02','lane-a','pi','finished',0,1763632800000,1763632810000,'[]',41),
		       ('run-claude-closed','item-seed-02','lane-a','claude','finished',0,1763632800000,1763632806000,'[]',42),
		       ('run-claude-live','item-seed-02','lane-a','claude','running',NULL,1763632799500,0,'[]',43);
	`)

	// 1. Pi log with both message_end and turn_end + tool_execution_end deduplicates by responseId and toolCallId.
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-turnend.log"), strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","responseId":"resp-1","timestamp":1000,"requestStartedAt":1000,"durationMs":1500,"content":[{"type":"toolCall","id":"tc-1","name":"bash","arguments":{"command":"ls"}}]}}`,
		`{"type":"tool_execution_end","toolCallId":"tc-1","toolName":"bash","durationMs":2500,"result":{"content":[{"type":"text","text":"a.go"}]}}`,
		`{"type":"turn_end","message":{"role":"assistant","responseId":"resp-1","timestamp":1000,"requestStartedAt":1000,"durationMs":1500,"content":[{"type":"toolCall","id":"tc-1","name":"bash","arguments":{"command":"ls"}}]},"toolResults":[{"role":"toolResult","toolCallId":"tc-1","timestamp":5000,"durationMs":2500,"content":[{"type":"text","text":"a.go"}]}]}`,
	}, "\n")+"\n")
	_, body, raw := h.get("/api/v1/runs/run-turnend")
	te := body["data"].(map[string]any)
	if got := timingMetricOf(t, te, "model_ms")["ms"]; got != float64(1500) {
		t.Fatalf("deduplicated model_ms = %v, want 1500 (%s)", got, raw)
	}
	if got := timingMetricOf(t, te, "tool_ms")["ms"]; got != float64(2500) {
		t.Fatalf("deduplicated tool_ms = %v, want 2500 (%s)", got, raw)
	}

	// 2. proc-exec.jsonl fallback when log carries no tool durations.
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-proc.log"),
		`{"type":"turn_end","message":{"role":"assistant","responseId":"r-p","timestamp":1763632800000,"durationMs":1000,"content":[{"type":"text","text":"running"}]}}`+"\n")
	writeTestFile(t, filepath.Join(h.loopDir, "run-proc", "proc-exec.jsonl"), strings.Join([]string{
		`{"ts":"2025-11-20T10:00:02.000Z","ts_end":"2025-11-20T10:00:05.000Z","duration_ms":3000,"cmd":"git status"}`,
		`{"ts":"2025-11-20T10:00:04.000Z","ts_end":"2025-11-20T10:00:06.000Z","duration_ms":2000,"cmd":"git diff"}`,
	}, "\n")+"\n")
	_, body, raw = h.get("/api/v1/runs/run-proc")
	pr := body["data"].(map[string]any)
	if m := timingMetricOf(t, pr, "tool_ms"); m["ms"] != float64(4000) || m["epistemic_state"] != "derived" || m["source"] != "proc-exec.jsonl" {
		t.Fatalf("proc-exec fallback tool_ms = %+v, want derived 4000 (%s)", m, raw)
	}
	if m := timingMetricOf(t, pr, "slot_wait_ms"); m["ms"] != float64(0) || m["epistemic_state"] != "observed" {
		t.Fatalf("proc-exec no-shards slot_wait_ms = %+v, want observed 0", m)
	}

	// 3. Closed vs live/unclosed Claude stream-json logs.
	claudeOpen := strings.Join([]string{
		`{"type":"system","subtype":"init","model":"claude-example"}`,
		`{"type":"assistant","timestamp":"2025-11-20T10:00:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Checking."}]}}`,
		`{"type":"assistant","timestamp":"2025-11-20T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":"git status"}}]}}`,
		`{"type":"user","timestamp":"2025-11-20T10:00:03.500Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-1","content":"clean"}]}}`,
		`{"type":"assistant","timestamp":"2025-11-20T10:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`,
	}, "\n") + "\n"
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-claude-live.log"), claudeOpen)
	writeTestFile(t, filepath.Join(h.laneRoot, "lane-a", "run-claude-closed.log"),
		claudeOpen+`{"type":"result","subtype":"success","duration_ms":6000,"duration_api_ms":2200,"num_turns":2}`+"\n")

	_, body, _ = h.get("/api/v1/runs/run-claude-closed")
	closed := body["data"].(map[string]any)
	if m := timingMetricOf(t, closed, "model_ms"); m["ms"] != float64(2200) || m["epistemic_state"] != "observed" {
		t.Fatalf("closed claude model_ms = %+v, want observed 2200", m)
	}
	if m := timingMetricOf(t, closed, "tool_ms"); m["ms"] != float64(2500) || m["epistemic_state"] != "derived" {
		t.Fatalf("closed claude tool_ms = %+v, want derived 2500", m)
	}

	_, body, _ = h.get("/api/v1/runs/run-claude-live")
	live := body["data"].(map[string]any)
	// [1763632799500..1763632800000]=500 + [..01000]=1000 + [03500..05000]=1500 = 3000ms derived.
	if m := timingMetricOf(t, live, "model_ms"); m["ms"] != float64(3000) || m["epistemic_state"] != "derived" {
		t.Fatalf("live claude model_ms = %+v, want derived 3000", m)
	}
}

func TestRunScheduleRetriesMultiNodeRefusalStepsPipelineGateAndCLI(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `
		CREATE TABLE IF NOT EXISTS retries (
			retry_id          TEXT PRIMARY KEY,
			item_id           TEXT NOT NULL,
			supersedes_run_id TEXT NOT NULL,
			requested_by      TEXT NOT NULL,
			reason            TEXT NOT NULL,
			requested_at      INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS landings (
			landing_id   TEXT PRIMARY KEY,
			item_id      TEXT NOT NULL,
			run_id       TEXT NOT NULL,
			verdict_id   TEXT NOT NULL,
			trunk_before TEXT NOT NULL,
			trunk_after  TEXT NOT NULL,
			gated_tree   TEXT NOT NULL,
			landed_tree  TEXT NOT NULL,
			result       TEXT NOT NULL,
			reason       TEXT NOT NULL DEFAULT '',
			event_seq    INTEGER NOT NULL
		);
		INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,event_seq)
		VALUES ('run-w1','item-seed-02','lane-a','pi','finished',0,1000,10000,'[]',50),
		       ('run-v3','item-seed-02','lane-a','claude','finished',0,11000,15000,'[]',51),
		       ('run-w2','item-seed-02','lane-a','pi','finished',0,25000,30000,'[]',52);
		INSERT INTO retries(retry_id,item_id,supersedes_run_id,requested_by,reason,requested_at)
		VALUES ('retry-w1','item-seed-02','run-w1','matt','flaky external fixture',20000);
		INSERT INTO events(seq,ts,kind,object_type,object_id,payload) VALUES
		 (900, 15500, 'drain.refusal', 'run', 'run-v3', '{"item_id":"item-seed-02","step":"verify-not-done","detail":"missing edge case","worker_session":{"run_id":"run-w1","session_id":"sess-1"}}'),
		 (901, 20001, 'drain.retry.ack', 'retry', 'retry-w1', '{"item_id":"item-seed-02","acknowledge":"gate-failed","requested_by":"matt"}'),
		 (902, 31000, 'land.recorded', 'landing', 'land-w2', '{"landing_id":"land-w2"}');
		INSERT INTO landings(landing_id,item_id,run_id,verdict_id,trunk_before,trunk_after,gated_tree,landed_tree,result,reason,event_seq)
		VALUES ('land-w2','item-seed-02','run-w2','verdict-w2','aaa','bbb','ccc','ccc','landed','',902);
		DELETE FROM gate_spans WHERE run_id = 'run-w1';
		INSERT INTO gate_spans VALUES
		 ('gs-1','tr','p','verdict-f1','run-w1','tick-1','unit-check','check','unit','FAIL',10000000000,11000000000,'{"pool":4200}','{}');
	`)

	// steps.jsonl has stage, run, verdict, item-state; pipeline.jsonl has an in-progress gate start!
	writeTestFile(t, filepath.Join(h.loopDir, "run-w1", "steps.jsonl"), strings.Join([]string{
		`{"step":"stage","started_at_ms":900,"finished_at_ms":950,"exit_code":0}`,
		`{"step":"run","started_at_ms":1000,"finished_at_ms":10000,"exit_code":0}`,
		`{"step":"verdict","started_at_ms":10100,"finished_at_ms":10200,"exit_code":0}`,
		`{"step":"item-state","started_at_ms":10250,"finished_at_ms":10300,"exit_code":0}`,
	}, "\n")+"\n")
	writeTestFile(t, filepath.Join(h.loopDir, "run-w1", "pipeline.jsonl"), strings.Join([]string{
		`{"step":"stage","started_at_ms":900,"finished_at_ms":950}`,
		`{"step":"gate","started_at_ms":10500,"finished_at_ms":null}`,
	}, "\n")+"\n")
	writeTestFile(t, filepath.Join(h.loopDir, "run-w1", "gate.plan.json"),
		`{"record_version":1,"kind":"gate.plan","created_at":"1970-01-01T00:00:10.600Z","payload":{"plan_id":"p1","checks":[{"check_id":"unit-check"},{"check_id":"lint-check"}]}}`)
	writeTestFile(t, filepath.Join(h.loopDir, "run-w1", "gate.verdict.json"),
		`{"record_version":1,"kind":"gate.verdict","created_at":"1970-01-01T00:00:10.900Z","payload":{"verdict_id":"verdict-f1","result":"fail","checks":[{"check_id":"unit-check","result":"fail"},{"check_id":"lint-check","result":"pass"}]}}`)

	status, body, raw := h.get("/api/v1/runs/run-w1")
	if status != 200 {
		t.Fatalf("run-w1: %d %s", status, raw)
	}
	sched := body["data"].(map[string]any)["schedule"].([]any)
	bySummary := map[string]map[string]any{}
	var foundRefusal, foundRetry, foundRetryAck, foundLiveGate, foundPlan, foundVerdict bool
	for _, item := range sched {
		entry := item.(map[string]any)
		summary := entry["summary"].(string)
		bySummary[summary] = entry
		switch entry["kind"] {
		case "drain.refusal":
			foundRefusal = true
			if entry["category"] != "refusal" {
				t.Fatalf("refusal category = %v", entry)
			}
			links := entry["links"].(map[string]any)
			if links["evaluating_run_id"] != "run-v3" {
				t.Fatalf("multi-node refusal links = %+v", links)
			}
		case "retry":
			foundRetry = true
			if entry["category"] != "retry" {
				t.Fatalf("retry category = %v", entry)
			}
			links := entry["links"].(map[string]any)
			if links["next_run_id"] != "run-w2" {
				t.Fatalf("retry next_run_id = %+v", links)
			}
		case "drain.retry.ack":
			foundRetryAck = true
		case "gate.plan":
			foundPlan = strings.Contains(summary, "checks: 2")
		case "gate.verdict":
			foundVerdict = strings.Contains(summary, "failed: unit-check") && strings.Contains(summary, "gate_slot_wait_ms: 4200")
		}
		if summary == "step gate started (in progress)" && entry["category"] == "gate" {
			foundLiveGate = true
		}
	}
	// Step categories: `run` is allocation, `verdict` and `item-state` are other (never gate!).
	if bySummary["step run started"]["category"] != "allocation" {
		t.Fatalf("step run category = %+v, want allocation", bySummary["step run started"])
	}
	if bySummary["step verdict started"]["category"] != "other" {
		t.Fatalf("step verdict category = %+v, want other", bySummary["step verdict started"])
	}
	if bySummary["step item-state started"]["category"] != "other" {
		t.Fatalf("step item-state category = %+v, want other", bySummary["step item-state started"])
	}
	if !foundRefusal || !foundRetry || !foundRetryAck || !foundLiveGate || !foundPlan || !foundVerdict {
		t.Fatalf("missing schedule items (refusal=%v retry=%v ack=%v liveGate=%v plan=%v verdict=%v): %s",
			foundRefusal, foundRetry, foundRetryAck, foundLiveGate, foundPlan, foundVerdict, raw)
	}

	// Superseding run `run-w2` sees both `retry.authorized` (linking run-w1) and `landing` from `landings`.
	_, body2, raw2 := h.get("/api/v1/runs/run-w2")
	var sawAuth, sawLanding bool
	for _, item := range body2["data"].(map[string]any)["schedule"].([]any) {
		entry := item.(map[string]any)
		if entry["kind"] == "retry.authorized" && entry["links"].(map[string]any)["supersedes_run_id"] == "run-w1" {
			sawAuth = true
		}
		if entry["kind"] == "landing" && entry["category"] == "landing" && entry["links"].(map[string]any)["landed_sha"] == "bbb" {
			sawLanding = true
		}
	}
	if !sawAuth || !sawLanding {
		t.Fatalf("run-w2 schedule missing retry.authorized=%v or landing=%v: %s", sawAuth, sawLanding, raw2)
	}

	// CLI `tillr-view run` face matches HTTP projection (`D-OR5`).
	var cliJSON bytes.Buffer
	_, code := runFace([]string{"--store", h.store, "--run", "run-w1", "--lane-root", h.laneRoot, "--loop-dir", h.loopDir, "--json"}, &cliJSON)
	if code != 0 {
		t.Fatalf("runFace --json exit %d", code)
	}
	var parsed map[string]any
	if err := json.Unmarshal(cliJSON.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed["schedule"].([]any)) != len(sched) {
		t.Fatalf("CLI schedule len %d != HTTP schedule len %d", len(parsed["schedule"].([]any)), len(sched))
	}
	var cliText bytes.Buffer
	if _, code := runFace([]string{"--store", h.store, "--run", "run-w1", "--lane-root", h.laneRoot, "--loop-dir", h.loopDir}, &cliText); code != 0 {
		t.Fatalf("runFace text exit %d", code)
	}
	if !strings.Contains(cliText.String(), "SCHEDULE") || !strings.Contains(cliText.String(), "retry-w1") {
		t.Fatalf("CLI text missing schedule output:\n%s", cliText.String())
	}
	if _, code := runFace([]string{"--help"}, &cliText); code != helpExit {
		t.Fatalf("runFace --help code = %d, want helpExit", code)
	}
}

func TestMergeAndSubtractIntervals(t *testing.T) {
	merged := mergeIntervals([]msInterval{{5, 10}, {0, 6}, {20, 30}, {30, 31}, {7, 7}})
	if sumIntervals(merged) != 21 {
		t.Fatalf("merged = %+v", merged)
	}
	rest := subtractIntervals([]msInterval{{0, 100}}, []msInterval{{10, 20}, {50, 60}})
	if sumIntervals(rest) != 80 {
		t.Fatalf("rest = %+v", rest)
	}
}
