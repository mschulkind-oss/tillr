package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// serveHarness is one fixture: a store with a two-item drain in it, a loop directory with
// one run's record files, a lane directory, and a trace index path that is not there —
// which is the state of the world until OBS-4 lands, and must be answered, not papered
// over.
type serveHarness struct {
	t        *testing.T
	dir      string
	store    string
	loopDir  string
	laneRoot string
	index    string
	// vantageBase is the user-scope Vantage base URL the face was started with; the
	// default harness configures none, which is the state the board must still draw a link
	// in.
	vantageBase string
	// questionsCmd is the stub that answers Vantage's index. The default fails, so a case
	// that points an item at a design document without configuring an index gets
	// `questions-unknown` rather than a question list this test invented.
	questionsCmd []string
	srv          *server
	http         *httptest.Server
}

// newServeHarness builds the fixture. The store is written through the DDL the real
// utilities register (see the blank imports), so the rows are the shape the write faces
// maintain rather than a second spelling of it.
func newServeHarness(t *testing.T) *serveHarness {
	t.Helper()
	return newServeHarnessWith(t, "")
}

// newServeHarnessWith builds the same fixture with the user-scope Vantage base URL the
// face is started with. It is a constructor argument rather than a later assignment
// because the server reads it on the request goroutine: a field written after the listener
// is up would be a race, and a test that wins one is not a test of the setting.
func newServeHarnessWith(t *testing.T, vantageBase string) *serveHarness {
	t.Helper()
	return newServeHarnessWithQuestions(t, vantageBase, []string{"false"})
}

// newServeHarnessWithQuestions builds the same fixture with the index command the case
// needs: the document route reads Vantage's index, and a case that points an item at a
// document must name the answer it expects.
func newServeHarnessWithQuestions(t *testing.T, vantageBase string, questionsCmd []string) *serveHarness {
	t.Helper()
	dir := t.TempDir()
	h := &serveHarness{
		t:            t,
		dir:          dir,
		store:        filepath.Join(dir, "state.db"),
		loopDir:      filepath.Join(dir, "loop"),
		laneRoot:     filepath.Join(dir, "lanes"),
		index:        filepath.Join(dir, "no-such-index.db"),
		vantageBase:  vantageBase,
		questionsCmd: questionsCmd,
	}
	h.seedStore()
	h.seedLoopDir()
	h.start()
	return h
}

// stubIndexCommand writes a script that answers the given index JSON and returns the argv
// the reader runs. The script names an absolute path, so the reader's `cmd.Dir` (the served
// root) does not change what it reads.
func stubIndexCommand(t *testing.T, index string) []string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	if err := os.WriteFile(path, []byte(index), 0o644); err != nil {
		t.Fatalf("writing the stub index: %v", err)
	}
	script := filepath.Join(dir, "index.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat "+path+"\n"), 0o755); err != nil {
		t.Fatalf("writing the stub index script: %v", err)
	}
	return []string{script}
}

// seedStore writes the board: two items a drain is working, one blocked, and the
// candidates and landing attempt that judge them. The table shapes are the fixture's own
// copy of what the write faces declare (testdata/serve-fixture.sql): this module reads a
// store, so it must not link a writer to have one to read.
func (h *serveHarness) seedStore() {
	h.t.Helper()
	s, err := store.Open(context.Background(), h.store, true)
	if err != nil {
		h.t.Fatalf("creating the store: %v", err)
	}
	if err := s.Close(); err != nil {
		h.t.Fatalf("closing the store: %v", err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		h.t.Fatalf("reading the fixture schema: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		h.t.Fatalf("opening the store to seed it: %v", err)
	}
	defer db.Close()
	// One transaction for the whole fixture. Each autocommit statement is its own
	// fsync, and the harness builds a fresh store per case: measured 2026-11-15,
	// 112 fsyncs per harness without this and 1 with it (strace -f -c -e trace=fsync
	// on one subtest). The schema rides in the same transaction because the driver
	// runs a multi-statement Exec as one script.
	tx, err := db.Begin()
	if err != nil {
		h.t.Fatalf("beginning the fixture transaction: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed transaction's rollback is a no-op
	if _, err := tx.Exec(string(schema)); err != nil {
		h.t.Fatalf("creating the fixture tables: %v", err)
	}
	statements := []string{
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
		 VALUES ('item-seed-02', 'second seed', 'items/02.md', 'in-queue', '', 1000,
		   '["tools/a/","tools/b/","tools/c/"]')`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
		 VALUES ('item-seed-04', 'fourth seed', 'items/04.md', 'landed', '', 2000, '["tools/c/"]')`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
		 VALUES ('item-seed-06', 'sixth seed', 'items/06.md', 'blocked', '', 3000, '')`,
		`INSERT INTO item_criteria (item_id, criterion_id, text, state)
		 VALUES ('item-seed-02', 'ac-1', 'it works', 'pending')`,
		`INSERT INTO item_criteria (item_id, criterion_id, text, state)
		 VALUES ('item-seed-02', 'ac-2', 'it is tested', 'pending')`,
		`INSERT INTO item_edges (item_id, blocked_by) VALUES ('item-seed-06', 'item-seed-02')`,
		`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES ('lane-a', 'leased', 'run-2', 900000)`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at,
		   artifact_refs, event_seq, usage_tokens, usage_cost_usd)
		 VALUES ('run-1', 'item-seed-02', 'lane-a', 'pi', 'finished', 0, 1000, 2000, '[]', 1, 1234, 0.25)`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at,
		   artifact_refs, event_seq, usage_tokens, usage_cost_usd)
		 VALUES ('run-2', 'item-seed-04', 'lane-a', 'pi', 'running', NULL, 3000, 0, '[]', 2, NULL, NULL)`,
		`INSERT INTO candidates (candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at, event_seq)
		 VALUES ('cand-1', 'item-seed-02', 'main', 'base1', 'head1', 'tree1', 'enqueued', 2500, 3)`,
		`INSERT INTO candidates (candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at, event_seq)
		 VALUES ('cand-2', 'item-seed-04', 'main', 'base0', 'head0', 'tree0', 'landed', 1500, 4)`,
		`INSERT INTO landing_attempts (attempt_id, candidate_id, verdict_id, result, reason, trunk_before,
		   trunk_after, event_seq) VALUES ('attempt-1', 'cand-2', 'verdict-1', 'landed', '', 'trunk-a',
		   'trunk-b', 5)`,
		// Two ticks' gate measurements, the second over its budget: the board's trend is the
		// recorded numbers, and the overrun is the one a reader must be able to see.
		`INSERT INTO drain_passes (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
		   gate_budget_ms, gate_over_budget) VALUES ('tick-1', 1000, 12, 40000, 71600, 90, 330000, 0)`,
		`INSERT INTO drain_passes (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
		   gate_budget_ms, gate_over_budget) VALUES ('tick-2', 2000, 15, 41000, 400000, 120, 330000, 1)`,
		// Two ticks' decisions: four rows, two per tick, because a tick decides once per
		// item it considered. The header bar's tick count is the two ticks, never the four
		// rows -- measured on the live store 2026-11-14, which held 1548 rows across 27 ticks.
		`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at)
		   VALUES ('decision-item-seed-02-tick-1', 'item-seed-02', 'lane-a', 'run-1', 'dispatch', 'ready', 1500)`,
		`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at)
		   VALUES ('decision-item-seed-04-tick-1', 'item-seed-04', NULL, NULL, 'hold', 'blocked', 1500)`,
		`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at)
		   VALUES ('decision-item-seed-02-tick-2', 'item-seed-02', NULL, NULL, 'hold', 'not-ready', 2500)`,
		// item-seed-04's own decision carries no lane: its run history is its own run-2, and
		// naming lane-a here would add run-1 to it through the "runs on a lane the subject was
		// dispatched on" rule (status_store.go assembleStoreSubject).
		`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at)
		   VALUES ('decision-item-seed-04-tick-2', 'item-seed-04', NULL, NULL, 'dispatch', 'ready', 2500)`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			h.t.Fatalf("seeding the store with %q: %v", stmt[:40], err)
		}
	}
	if err := tx.Commit(); err != nil {
		h.t.Fatalf("committing the fixture: %v", err)
	}
}

// seedLoopDir writes one tick's record files for run-1, and the lane's copy of the brief
// with no preserved run copy — the state OBS-1 exists to fix.
func (h *serveHarness) seedLoopDir() {
	h.t.Helper()
	runDir := filepath.Join(h.loopDir, "run-1")
	laneDir := filepath.Join(h.laneRoot, "lane-a")
	for _, dir := range []string{runDir, laneDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			h.t.Fatalf("creating %s: %v", dir, err)
		}
	}
	files := map[string]string{
		filepath.Join(runDir, "gate.change.json"):       `{"change_id":"change-1","paths":["tools/a/x.go"]}`,
		filepath.Join(runDir, "gate.plan.json"):         `{"plan_id":"plan-1","checks":[{"check_id":"unit"}]}`,
		filepath.Join(runDir, "gate.verdict.json"):      `{"verdict_id":"verdict-1","result":"pass","checks":[]}`,
		filepath.Join(runDir, "land.candidate.json"):    `{"candidate_id":"cand-1","item_id":"item-seed-02"}`,
		filepath.Join(runDir, "item.verdict.ac-1.json"): `{"criterion_id":"ac-1","verdict":"pass"}`,
		filepath.Join(laneDir, "brief.md"):              "# brief for item-seed-02\n",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			h.t.Fatalf("writing %s: %v", path, err)
		}
	}
}

// start opens the read-only face and serves it.
func (h *serveHarness) start() {
	h.t.Helper()
	srv := newServer(context.Background(), h.store, h.loopDir, h.laneRoot, h.index, h.dir, h.vantageBase, h.questionsCmd)
	if srv.problem != nil {
		h.t.Fatalf("opening the serve face answered %s: %s", srv.problem.code, srv.problem.note)
	}
	h.srv = srv
	h.http = httptest.NewServer(srv.routes())
	h.t.Cleanup(func() {
		h.http.Close()
		srv.close()
	})
}

// get runs one route and answers its status and decoded body.
func (h *serveHarness) get(path string) (int, map[string]any, []byte) {
	h.t.Helper()
	resp, err := http.Get(h.http.URL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		h.t.Fatalf("reading %s: %v", path, err)
	}
	raw := buf.Bytes()
	if err := json.Unmarshal(raw, &body); err != nil {
		h.t.Fatalf("GET %s answered unreadable JSON (%d): %v\n%s", path, resp.StatusCode, err, raw)
	}
	return resp.StatusCode, body, raw
}

// getRaw runs one route that may answer something other than JSON -- the front end's
// documents and assets -- and answers its status and exact bytes.
func (h *serveHarness) getRaw(path string) (int, []byte) {
	h.t.Helper()
	resp, err := http.Get(h.http.URL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		h.t.Fatalf("reading %s: %v", path, err)
	}
	return resp.StatusCode, buf.Bytes()
}

func (h *serveHarness) head() float64 {
	h.t.Helper()
	status, body, _ := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		h.t.Fatalf("snapshot answered %d", status)
	}
	snapshot, ok := body["snapshot"].(map[string]any)
	if !ok {
		h.t.Fatalf("the response carries no snapshot cursor: %s", body)
	}
	return snapshot["event_head_seq"].(float64)
}

// serveCase is one registered test case. Every case is named here and runs under
// TestServeCases, so `go test -v` names each one.
type serveCase struct {
	name string
	run  func(t *testing.T, h *serveHarness)
}

var serveCases = []serveCase{
	{"an_items_fence_is_the_path_list_the_store_holds", func(t *testing.T, h *serveHarness) {
		// The column holds the fence as JSON text (tillr-item's encodeAllowedPaths), so the
		// item detail has to answer the paths themselves. Measured against the board on
		// 2026-11-13: a three-path fence answered as one string holding the escaped list,
		// which an operator reads as a fence with a single entry.
		status, body, raw := h.get("/api/v1/items/item-seed-02")
		if status != http.StatusOK {
			t.Fatalf("item-seed-02 answered %d: %s", status, raw)
		}
		item, _ := body["data"].(map[string]any)["item"].(map[string]any)
		paths, ok := item["allowed_paths"].([]any)
		if !ok {
			t.Fatalf("allowed_paths answered %T, want a JSON array: %s", item["allowed_paths"], raw)
		}
		want := []string{"tools/a/", "tools/b/", "tools/c/"}
		if len(paths) != len(want) {
			t.Fatalf("allowed_paths carries %d entries, want the %d the store holds: %s",
				len(paths), len(want), raw)
		}
		for i, expected := range want {
			if paths[i] != expected {
				t.Fatalf("allowed_paths[%d] is %v, want %q: %s", i, paths[i], expected, raw)
			}
		}

		// An item with no fence answers an empty array: a list holding one empty string
		// reads as a path, and this item names none.
		_, emptyBody, emptyRaw := h.get("/api/v1/items/item-seed-06")
		emptyItem, _ := emptyBody["data"].(map[string]any)["item"].(map[string]any)
		emptyPaths, ok := emptyItem["allowed_paths"].([]any)
		if !ok {
			t.Fatalf("an unfenced item answered allowed_paths as %T, want an empty array: %s",
				emptyItem["allowed_paths"], emptyRaw)
		}
		if len(emptyPaths) != 0 {
			t.Fatalf("an unfenced item answered %d allowed_paths (%v), want none: %s",
				len(emptyPaths), emptyPaths, emptyRaw)
		}
	}},
	{"a_runs_item_link_survives_the_snapshot_summary", func(t *testing.T, h *serveHarness) {
		// The planner reads this summary to decide whether a run belongs to an item, and
		// a run whose link is dropped there is abandoned as `run-link-missing` while the
		// store has held the link all along. Measured 2026-11-13: `tillr drain loop` left
		// a completed candidate unjudged for exactly this reason.
		status, body, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("snapshot answered %d: %s", status, raw)
		}
		data, _ := body["data"].(map[string]any)
		runs, _ := data["recent_runs"].([]any)
		if len(runs) == 0 {
			t.Fatalf("the snapshot lists no runs: %s", raw)
		}
		want := map[string]string{"run-1": "item-seed-02", "run-2": "item-seed-04"}
		seen := 0
		for _, entry := range runs {
			run, _ := entry.(map[string]any)
			id, _ := run["run_id"].(string)
			item, _ := run["item_id"].(string)
			if expected, ok := want[id]; ok {
				seen++
				if item != expected {
					t.Fatalf("run %s names item %q in the summary, want %q (the store holds it)",
						id, item, expected)
				}
			}
		}
		if seen != len(want) {
			t.Fatalf("the summary carried %d of the %d runs the fixture wrote: %s", seen, len(want), raw)
		}
	}},

	{"the_transcript_route_reads_the_runs_own_records_and_pages_them", func(t *testing.T, h *serveHarness) {
		// The transcript is the one route whose source is the run's own files rather than
		// the store, so it is the one route that can answer a store row with no evidence at
		// all: run-2 is live in the fixture and has written nothing, and the route must say
		// so as a seam instead of answering an empty pane.
		if err := os.WriteFile(filepath.Join(h.laneRoot, "lane-a", "run-1.log"), []byte(
			"{\"type\":\"session\",\"timestamp\":\"1970-01-01T00:00:02.000Z\",\"cwd\":\"/w\"}\n"+
				"{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"timestamp\":2000,"+
				"\"content\":[{\"type\":\"text\",\"text\":\"one\"}]}}\n"+
				"{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"timestamp\":3000,"+
				"\"content\":[{\"type\":\"text\",\"text\":\"two\"}]}}\n"), 0o644); err != nil {
			t.Fatalf("writing the run's log: %v", err)
		}

		status, body, raw := h.get("/api/v1/runs/run-1/transcript?limit=2")
		if status != http.StatusOK {
			t.Fatalf("the transcript route answered %d: %s", status, raw)
		}
		data, _ := body["data"].(map[string]any)
		if data["epistemic_state"] != "observed" {
			t.Fatalf("a run with a log answers state %v, want observed: %s", data["epistemic_state"], raw)
		}
		entries, _ := data["entries"].([]any)
		if len(entries) != 2 {
			t.Fatalf("limit=2 answered %d entries: %s", len(entries), raw)
		}
		last := entries[len(entries)-1].(map[string]any)
		if last["text"] != "two" {
			t.Fatalf("the tail page ends at %v, want the newest message: %s", last["text"], raw)
		}
		before, _ := data["before"].(string)
		if before == "" {
			t.Fatalf("a tail page over a longer transcript answers no before cursor: %s", raw)
		}
		if _, ok := data["cursor"].(string); !ok {
			t.Fatalf("the page answers no cursor to resume from: %s", raw)
		}

		// The page before it is the first entry, and it says there is nothing earlier.
		status, head, headRaw := h.get("/api/v1/runs/run-1/transcript?limit=2&before=" + before)
		if status != http.StatusOK {
			t.Fatalf("the earlier page answered %d: %s", status, headRaw)
		}
		headData, _ := head["data"].(map[string]any)
		if carried, _ := headData["before"].(string); carried != "" {
			t.Fatalf("the first page answers a before cursor %q: %s", carried, headRaw)
		}
		headEntries, _ := headData["entries"].([]any)
		if len(headEntries) != 1 || headEntries[0].(map[string]any)["text"] != "the agent's session started in /w" {
			t.Fatalf("the first page is %v, want the session's own notice: %s", headEntries, headRaw)
		}

		// A call the fixture's log never records is a seam, not an empty transcript.
		status, live, liveRaw := h.get("/api/v1/runs/run-2/transcript")
		if status != http.StatusOK {
			t.Fatalf("a run with no records answered %d: %s", status, liveRaw)
		}
		liveData, _ := live["data"].(map[string]any)
		if liveData["epistemic_state"] != "unobserved_seam" {
			t.Fatalf("a run with no records answers state %v, want unobserved_seam: %s",
				liveData["epistemic_state"], liveRaw)
		}
		if note, _ := liveData["note"].(string); !strings.Contains(note, "absent") {
			t.Fatalf("the seam's note does not say what is missing: %v", liveData["note"])
		}

		// A cursor this face did not issue is a caller's mistake, not a page from the head.
		status, _, badRaw := h.get("/api/v1/runs/run-1/transcript?before=zzz")
		if status != http.StatusBadRequest {
			t.Fatalf("a bogus cursor answered %d, want 400: %s", status, badRaw)
		}
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(badRaw, &failure); err != nil || failure.Error.Code != "bad-query" {
			t.Fatalf("a bogus cursor answered code %q, want bad-query: %s", failure.Error.Code, badRaw)
		}
	}},
	{"every_route_answers_a_read_only_store", func(t *testing.T, h *serveHarness) {
		// The failure this face exists to prevent is a second writer. A store directory
		// no process may write is the state a writer would fail in; every route must
		// still answer 200 from it.
		if err := os.Chmod(h.dir, 0o555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(h.dir, 0o755) })
		// The handle behind those routes must refuse a write: 200s alone would also be
		// answered by a read-write open that simply was not asked to write.
		if err := h.srv.store.WriteMeta("probe", "x"); err == nil {
			t.Fatal("the serve face opened the store writable: this face exists because a second writer must be impossible")
		}
		routes := []string{
			"/api/v1/snapshot",
			"/api/v1/items/item-seed-02",
			"/api/v1/runs/run-1",
			"/api/v1/runs/run-1/exchanges",
			"/api/v1/runs/run-1/timeline",
			"/api/v1/runs/run-1/transcript",
			"/api/v1/runs/run-1/artifacts/gate.verdict",
		}
		for _, route := range routes {
			status, body, raw := h.get(route)
			if status != http.StatusOK {
				t.Fatalf("%s answered %d on a read-only store: %s", route, status, raw)
			}
			if _, ok := body["snapshot"]; !ok {
				t.Fatalf("%s carries no snapshot cursor", route)
			}
		}
	}},
	{"the_ui_never_answers_for_a_route_the_api_does_not_carry", func(t *testing.T, h *serveHarness) {
		// The built front end is served at `/`, and a view URL must be a page. Neither
		// may shadow the API: an unknown `/api/v1/` path answers JSON, never the index
		// document, because a client that receives HTML where it expected JSON would
		// read a routing bug as a data bug.
		webDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(webDir, "index.html"),
			[]byte("<!doctype html><div id=root></div>"), 0o644); err != nil {
			t.Fatalf("writing the front end: %v", err)
		}
		if err := os.WriteFile(filepath.Join(webDir, "app.js"), []byte("// bundle"), 0o644); err != nil {
			t.Fatalf("writing an asset: %v", err)
		}
		web, note, code := openWebAssets(webDir, "")
		if code != record.ExitOK {
			t.Fatalf("openWebAssets(%s) refused: %s", webDir, note)
		}
		h.srv.web = web
		if status, raw := h.getRaw("/"); status != http.StatusOK || !bytes.Contains(raw, []byte("id=root")) {
			t.Fatalf("/ answered %d with %q, want the index document", status, raw)
		}
		if status, raw := h.getRaw("/?item=item-seed-02&run=run-1&tab=timeline"); status != http.StatusOK || !bytes.Contains(raw, []byte("id=root")) {
			t.Fatalf("a view URL answered %d with %q, want the index document", status, raw)
		}
		if status, raw := h.getRaw("/app.js"); status != http.StatusOK || !bytes.Contains(raw, []byte("// bundle")) {
			t.Fatalf("/app.js answered %d with %q, want the asset", status, raw)
		}
		if status, raw := h.getRaw("/assets/missing.js"); status != http.StatusNotFound {
			t.Fatalf("a missing asset answered %d with %q, want 404: HTML where a script belongs is a build bug rendered as a page", status, raw)
		}
		status, body, raw := h.get("/api/v1/not-a-route")
		if status != http.StatusNotFound {
			t.Fatalf("an unknown API route answered %d, want 404", status)
		}
		if bytes.Contains(raw, []byte("id=root")) {
			t.Fatalf("an unknown API route answered the index document: %s", raw)
		}
		failure, ok := body["error"].(map[string]any)
		if !ok || failure["code"] != codeUnknownRoute {
			t.Fatalf("an unknown API route answered %v, want an error body with code %s", body, codeUnknownRoute)
		}
	}},
	{"the_face_cannot_write_the_store", func(t *testing.T, h *serveHarness) {
		// The read-only open is the enforcement, not the handler's discipline: a write
		// through this process's handle must fail at the driver.
		_, err := h.srv.store.Meta("probe")
		if err != nil {
			t.Fatalf("reading meta through the read-only handle failed: %v", err)
		}
		if err := h.srv.store.WriteMeta("probe", "x"); err == nil {
			t.Fatal("the serve face wrote to the store: WriteMeta succeeded, which is the failure this design exists to prevent")
		}
	}},
	{"one_read_transaction_pins_each_request", func(t *testing.T, h *serveHarness) {
		head := h.head()
		// A write another process makes is what advances the cursor: the face reports the
		// head it was read at, so two responses are comparable.
		db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatalf("opening the store to append an event: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO events (ts, kind, payload) VALUES (9999, 'item.record', '{}')`); err != nil {
			db.Close()
			t.Fatalf("appending an event: %v", err)
		}
		db.Close()
		advanced := h.head()
		if advanced != head+1 {
			t.Fatalf("event_head_seq went from %v to %v; want one more", head, advanced)
		}
		itemHead := 0.0
		status, body, _ := h.get("/api/v1/items/item-seed-02")
		if status != http.StatusOK {
			t.Fatalf("item answered %d", status)
		}
		itemHead = body["snapshot"].(map[string]any)["event_head_seq"].(float64)
		if itemHead != advanced {
			t.Fatalf("item answered at head %v, snapshot at %v: one request must have one cursor", itemHead, advanced)
		}
	}},
	{"items_project_the_human_states", func(t *testing.T, h *serveHarness) {
		status, body, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("snapshot answered %d", status)
		}
		byID := map[string]map[string]any{}
		for _, rawItem := range body["data"].(map[string]any)["items"].([]any) {
			item := rawItem.(map[string]any)
			byID[item["item_id"].(string)] = item
		}
		want := map[string]string{
			"item-seed-02": "in_queue", // an enqueued candidate holds it
			"item-seed-04": "working",  // its run is still running
			"item-seed-06": "blocked",  // an unresolved blocked_by edge
		}
		for id, state := range want {
			item, ok := byID[id]
			if !ok {
				t.Fatalf("the snapshot lists no %s: %s", id, raw)
			}
			projected := item["projected_state"].(map[string]any)
			if projected["state"] != state {
				t.Fatalf("%s projected %q, want %q", id, projected["state"], state)
			}
			if projected["raw_state"] == "" {
				t.Fatalf("%s carries no raw_state: the projection must say what it read", id)
			}
		}
		blocked := byID["item-seed-06"]["projected_state"].(map[string]any)
		waiting, _ := blocked["waiting_on"].([]any)
		if len(waiting) != 1 || waiting[0] != "item-seed-02" {
			t.Fatalf("the blocked item names %v as what it waits on, want item-seed-02", blocked["waiting_on"])
		}
	}},
	{"every_span_and_exchange_carries_its_epistemic_state", func(t *testing.T, h *serveHarness) {
		_, timeline, raw := h.get("/api/v1/runs/run-1/timeline")
		spans := timeline["data"].(map[string]any)["spans"].([]any)
		states := map[string]bool{}
		categories := map[string]bool{}
		for _, rawSpan := range spans {
			span := rawSpan.(map[string]any)
			state, _ := span["epistemic_state"].(string)
			if state == "" {
				t.Fatalf("a span carries no epistemic state: %s", span)
			}
			states[state] = true
			categories[span["category"].(string)] = true
		}
		// The gate's span is read from file mtimes and the seam is a gap, so both states
		// must be visible; neither may be flattened into "observed".
		if !states[stateDerivedClock] {
			t.Fatalf("no span is derived_clock: %v (raw %s)", states, raw)
		}
		if !states[stateUnobservedSeam] {
			t.Fatalf("no span is unobserved_seam: the subprocess gap is being rendered as empty (%v)", states)
		}
		if !categories["gap"] {
			t.Fatalf("no gap span: an unobserved stream must be a span, not an absence")
		}

		_, exchanges, rawX := h.get("/api/v1/runs/run-1/exchanges")
		list := exchanges["data"].(map[string]any)["exchanges"].([]any)
		layers := map[string]int{}
		seams := 0
		for _, rawExchange := range list {
			exchange := rawExchange.(map[string]any)
			layers[exchange["layer"].(string)]++
			if exchange["epistemic_state"] == stateUnobservedSeam {
				seams++
				if exchange["started_at_ms"] != nil {
					t.Fatalf("an unobserved seam carries a timestamp: %s", exchange)
				}
			}
			if exchange["epistemic_state"] == "" {
				t.Fatalf("an exchange carries no epistemic state: %s", exchange)
			}
		}
		if layers["supervisor"] == 0 {
			t.Fatalf("no supervisor exchange: the tick's record files are not being read (%s)", rawX)
		}
		if seams < 2 {
			t.Fatalf("%d unobserved seams; want the subprocess and git streams named", seams)
		}
	}},
	{"the_timeline_says_the_trace_index_is_absent", func(t *testing.T, h *serveHarness) {
		status, body, raw := h.get("/api/v1/runs/run-1/timeline")
		if status != http.StatusOK {
			t.Fatalf("timeline answered %d", status)
		}
		data := body["data"].(map[string]any)
		note, _ := data["note"].(string)
		if note == "" {
			t.Fatalf("the timeline claims no absence although the index is missing: %s", raw)
		}
		sources := data["sources"].(map[string]any)
		index := sources["trace_index"].(map[string]any)
		if index["state"] != "absent" {
			t.Fatalf("the trace index reads as %q, want absent", index["state"])
		}
		for _, rawSpan := range data["spans"].([]any) {
			span := rawSpan.(map[string]any)
			if span["category"] == "llm_turn" || span["category"] == "tool_call" {
				t.Fatalf("an invented observation: %s with no index to read it from", span)
			}
		}
		// The store alone still answers the run: the absence must not swallow it.
		if len(data["spans"].([]any)) == 0 {
			t.Fatal("no spans at all: the store's run row must survive a missing index")
		}
	}},
	{"an_absent_artifact_says_where_it_looked", func(t *testing.T, h *serveHarness) {
		status, body, raw := h.get("/api/v1/runs/run-1/artifacts/fence")
		if status != http.StatusOK {
			t.Fatalf("an absent artifact answered %d; it must answer with the absence, not a 404: %s", status, raw)
		}
		artifact := body["data"].(map[string]any)["artifact"].(map[string]any)
		if artifact["present"] != false {
			t.Fatalf("the absent fence reads as present: %s", raw)
		}
		if artifact["epistemic_state"] != "absent" {
			t.Fatalf("an absent artifact reads as %q: a client branches on that state", artifact["epistemic_state"])
		}
		searched, _ := artifact["searched_paths"].([]any)
		if len(searched) == 0 {
			t.Fatalf("the absent artifact names no path it looked in: %s", raw)
		}

		// The lane's copy of the brief exists and the preserved one does not: that must
		// carry the clobber warning rather than pass as a preserved input.
		_, briefBody, briefRaw := h.get("/api/v1/runs/run-1/artifacts/brief")
		brief := briefBody["data"].(map[string]any)["artifact"].(map[string]any)
		if brief["present"] != true {
			t.Fatalf("the lane copy of the brief was not found: %s", briefRaw)
		}
		if brief["clobber_warning"] == nil {
			t.Fatalf("a lane copy passed as a preserved brief: %s", briefRaw)
		}
		if briefBody["snapshot"] == nil {
			t.Fatal("the artifact response carries no snapshot cursor")
		}
	}},
	{"a_missing_subject_is_a_refusal", func(t *testing.T, h *serveHarness) {
		// An item and a run are two subjects with two remedies, so they are two codes:
		// a client that typed an item id is not told to go looking for a run.
		for route, wantCode := range map[string]string{
			"/api/v1/items/not-an-item": codeItemNotFound,
			"/api/v1/runs/not-a-run":    codeRunNotFound,
		} {
			status, body, raw := h.get(route)
			if status != http.StatusNotFound {
				t.Fatalf("%s answered %d, want 404: %s", route, status, raw)
			}
			failure := body["error"].(map[string]any)
			if failure["code"] != wantCode {
				t.Fatalf("%s answered code %q, want %s", route, failure["code"], wantCode)
			}
			if body["snapshot"] == nil {
				t.Fatalf("%s carries no snapshot cursor", route)
			}
		}
	}},
	{"an_unknown_artifact_kind_is_refused", func(t *testing.T, h *serveHarness) {
		// A kind outside the closed set is the caller's mistake, not a missing resource:
		// 400 says the request was wrong, and 404 would have said the kind exists nowhere.
		status, body, raw := h.get("/api/v1/runs/run-1/artifacts/not-a-kind")
		if status != http.StatusBadRequest {
			t.Fatalf("an unknown kind answered %d: %s", status, raw)
		}
		if body["error"].(map[string]any)["code"] != codeUnknownKind {
			t.Fatalf("an unknown kind answered %s, want %s", raw, codeUnknownKind)
		}
	}},
	{"the_board_is_ordered_by_the_queue_rank", func(t *testing.T, h *serveHarness) {
		// The board's order is the queue's order: rank ascending as a plain string, an item
		// with no rank after every ranked one, and item_id breaking a tie. The set seeded here
		// is out of order on purpose — b, a, no rank, am — because an order that survives
		// insertion order is not the order that was asked for, and `am` is the sparse
		// insertion between `a` and `b` that the alphabet exists for.
		// The fixture's copy of the item DDL predates schema 5's rank column, so the column the
		// real store has is added here before the rows are ranked.
		h.exec(`ALTER TABLE items ADD COLUMN rank TEXT NOT NULL DEFAULT ''`)
		h.exec(`UPDATE items SET rank = 'b' WHERE item_id = 'item-seed-02'`)
		h.exec(`UPDATE items SET rank = 'a' WHERE item_id = 'item-seed-04'`)
		// item-seed-06 keeps the empty rank: it is the unranked one.
		h.exec(`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths, rank)
			 VALUES ('item-seed-08', 'eighth seed', 'items/08.md', 'blocked', '', 4000, '', 'am')`)
		status, body, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("snapshot answered %d: %s", status, raw)
		}
		want := []string{"item-seed-04", "item-seed-08", "item-seed-02", "item-seed-06"}
		got := boardItemOrder(t, body, raw)
		if len(got) != len(want) {
			t.Fatalf("the board carries %v, want %v: %s", got, want, raw)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("the board's order is %v, want rank ascending with the unranked item last %v: %s",
					got, want, raw)
			}
		}
		// The rank reaches the client, not just the order it implied: a board that sorted
		// correctly and printed no rank would leave the operator unable to say where the
		// next item goes.
		ranks := map[string]string{
			"item-seed-02": "b", "item-seed-04": "a", "item-seed-06": "", "item-seed-08": "am",
		}
		for _, entry := range body["data"].(map[string]any)["items"].([]any) {
			item, _ := entry.(map[string]any)
			id, _ := item["item_id"].(string)
			if item["rank"] != ranks[id] {
				t.Fatalf("%s answered rank %v, want %q: %s", id, item["rank"], ranks[id], raw)
			}
		}
		// A single item's dossier carries the same token, because it is the same fact.
		status, detail, detailRaw := h.get("/api/v1/items/item-seed-02")
		if status != http.StatusOK {
			t.Fatalf("item-seed-02 answered %d: %s", status, detailRaw)
		}
		item, _ := detail["data"].(map[string]any)["item"].(map[string]any)
		if item["rank"] != "b" {
			t.Fatalf("the item's payload answered rank %v, want \"b\": %s", item["rank"], detailRaw)
		}
	}},
	{"the_board_counts_its_own_rows", func(t *testing.T, h *serveHarness) {
		// The header's numbers are the board's own tallies, computed here rather than by each
		// client. The fixture holds exactly one in-queue item with no blocker, one landed
		// item, one blocked item, and one run the store still calls running.
		status, body, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("snapshot answered %d: %s", status, raw)
		}
		counts, ok := body["data"].(map[string]any)["counts"].(map[string]any)
		if !ok {
			t.Fatalf("the snapshot carries no counts object: %s", raw)
		}
		byState, _ := counts["by_state"].(map[string]any)
		// item-seed-04 is stored landed and its run is still running, so the projection
		// renders it working: the counts follow the state the board shows.
		wantState := map[string]float64{"in_queue": 1, "working": 1, "blocked": 1}
		if len(byState) != len(wantState) {
			t.Fatalf("by_state is %v, want %v: %s", byState, wantState, raw)
		}
		for state, want := range wantState {
			if byState[state] != want {
				t.Fatalf("by_state[%s] is %v, want %v: %s", state, byState[state], want, raw)
			}
		}
		// item-seed-02 is in the merge queue: it has a run and an enqueued candidate, so the
		// planner would not dispatch it again. Zero is the honest figure, and the case that
		// proves a dispatchable row is counted is the blocker case below.
		for field, want := range map[string]float64{"dispatchable": 0, "blocked": 1, "runs_live": 1} {
			if counts[field] != want {
				t.Fatalf("counts.%s is %v, want %v: %s", field, counts[field], want, raw)
			}
		}
	}},
	{"status_and_the_snapshot_answer_the_same_counts", func(t *testing.T, h *serveHarness) {
		// §5.6: the board and the CLI are both primary, and a disagreement between them is
		// a defect. Both faces read this one store, so the four figures must be identical;
		// the test compares the CLI's assembled answers with the snapshot's JSON rather
		// than re-deriving either.
		status, body, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("snapshot answered %d: %s", status, raw)
		}
		wire, ok := body["data"].(map[string]any)["counts"].(map[string]any)
		if !ok {
			t.Fatalf("the snapshot carries no counts object: %s", raw)
		}

		v, err := openStore(context.Background(), h.store, 0)
		if err != nil {
			t.Fatalf("opening the store as status does: %v", err)
		}
		payload, code := assemble(v, nil, nil)
		if code != 0 {
			t.Fatalf("assembling the board exited %d", code)
		}
		if payload.Counts == nil {
			t.Fatal("status --store assembled no counts")
		}
		cli := map[string]any{
			"by_state":       payload.Counts.ByState,
			"dispatchable":   payload.Counts.Dispatchable,
			"blocked":        payload.Counts.Blocked,
			"runs_live":      payload.Counts.RunsLive,
			"question_holds": payload.Counts.QuestionHolds,
		}
		cliRaw, err := json.Marshal(cli)
		if err != nil {
			t.Fatalf("encoding the CLI's counts: %v", err)
		}
		wireRaw, err := json.Marshal(wire)
		if err != nil {
			t.Fatalf("encoding the snapshot's counts: %v", err)
		}
		if string(cliRaw) != string(wireRaw) {
			t.Fatalf("the CLI and the snapshot disagree:\n cli: %s\nwire: %s", cliRaw, wireRaw)
		}
	}},
	{"dispatchable_follows_the_blockers_not_the_rendered_state", func(t *testing.T, h *serveHarness) {
		// Dispatchable is the drain planner's rule: an in-queue item is dispatchable when every
		// blocker has reached a terminal state. The projection is more conservative — any
		// blocked_by edge renders as blocked — so the two differ exactly here, and the header
		// must follow the planner, which is what the queue will actually do.
		h.exec(`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
			 VALUES ('item-seed-10', 'tenth seed', 'items/10.md', 'in-queue', '', 5000, '')`)
		h.exec(`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
			 VALUES ('item-seed-12', 'twelfth seed', 'items/12.md', 'in-queue', '', 6000, '')`)
		// item-seed-04 landed, so it is a met blocker; item-seed-06 is blocked, so it is not.
		h.exec(`INSERT INTO item_edges (item_id, blocked_by) VALUES ('item-seed-10', 'item-seed-04')`)
		h.exec(`INSERT INTO item_edges (item_id, blocked_by) VALUES ('item-seed-12', 'item-seed-06')`)
		// Their state events, so neither is held as an unknown history: this case is about
		// the blockers.
		h.exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
		 VALUES (5000, 'item.record', 'item', 'item-seed-10', '{"state":"in-queue"}')`)
		h.exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
		 VALUES (6000, 'item.record', 'item', 'item-seed-12', '{"state":"in-queue"}')`)
		status, body, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("snapshot answered %d: %s", status, raw)
		}
		counts := body["data"].(map[string]any)["counts"].(map[string]any)
		// item-seed-10 is dispatchable; item-seed-12 waits on a blocked item, and
		// item-seed-02 is held because it has already run.
		if counts["dispatchable"] != float64(1) {
			t.Fatalf("dispatchable is %v, want just item-seed-10: %s", counts["dispatchable"], raw)
		}
		if counts["blocked"] != float64(3) {
			t.Fatalf("blocked is %v, want the three the projection renders blocked: %s", counts["blocked"], raw)
		}
	}},
	{"status_reads_the_store_not_a_record_stream", func(t *testing.T, h *serveHarness) {
		// VIEW-2: `status --store` assembles the item, its criteria, its candidates and
		// its runs from the store's own rows. The case drives the face's real store
		// path, so a store-mode read that stopped reading rows would fail here.
		v, err := openStore(context.Background(), h.store, 0)
		if err != nil {
			t.Fatalf("opening the store as status does: %v", err)
		}
		payload, code := assemble(v, &structSubjectItem, nil)
		if code != 0 {
			t.Fatalf("assembling item-seed-02 exited %d", code)
		}
		if payload.Item == nil {
			t.Fatal("status --store assembled no item: the store's own row was not read (VIEW-2)")
		}
		if payload.Item.ItemID != "item-seed-02" || payload.Item.State != "in-queue" {
			t.Fatalf("status assembled %s/%s, want item-seed-02/in-queue", payload.Item.ItemID, payload.Item.State)
		}
		if len(payload.Item.Criteria) != 2 {
			t.Fatalf("status assembled %d criteria, want 2", len(payload.Item.Criteria))
		}
		if len(payload.Candidates) != 1 {
			t.Fatalf("status assembled %d candidates, want 1", len(payload.Candidates))
		}
		if payload.Candidates[0].Candidate.CandidateID != "cand-1" || payload.Candidates[0].Candidate.State != "enqueued" {
			t.Fatalf("status assembled candidate %+v, want cand-1/enqueued", payload.Candidates[0].Candidate)
		}
		// The landed item is the one with an attempt: the store's attempt row is what
		// makes "landed" evidence rather than a state name.
		landed, landedCode := assemble(v, &record.Subject{Kind: "item", ID: "item-seed-04"}, nil)
		if landedCode != 0 {
			t.Fatalf("assembling item-seed-04 exited %d", landedCode)
		}
		if len(landed.Candidates) != 1 || landed.Candidates[0].LatestAttempt == nil ||
			landed.Candidates[0].LatestAttempt.Result != "landed" {
			t.Fatalf("status assembled %+v for item-seed-04, want its landed attempt", landed.Candidates)
		}
		if len(landed.Runs) != 1 || landed.Runs[0].RunID != "run-2" {
			t.Fatalf("status assembled runs %+v for item-seed-04, want run-2", landed.Runs)
		}
	}},
	{"a_live_runs_transcript_is_readable_while_its_lane_holds_it", func(t *testing.T, h *serveHarness) {
		// The one moment a reader wants a run's transcript is while it is working, and that is
		// the moment the store has no `runs` row for it: the terminal write creates the row. A
		// route that resolved only the row would answer 404 for exactly the run an operator is
		// watching, so the run is resolved from the lease that holds it, the same projection
		// the snapshot makes.
		db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatalf("opening the store to lease a lane: %v", err)
		}
		defer db.Close()
		if _, err := db.Exec(`INSERT INTO lanes (lane_id, state, run_id, leased_until)
			VALUES ('lane-b', 'leased', 'run-live', 990000)`); err != nil {
			t.Fatalf("leasing lane-b: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			VALUES (5000, 'run.allocate', 'lane', 'lane-b',
			  '{"run_id":"run-live","item_id":"item-seed-06","backend":"pi","lane_id":"lane-b",
			    "started_at":"2026-11-14T06:31:54Z","leased_until":990000}')`); err != nil {
			t.Fatalf("appending the allocation: %v", err)
		}
		// The lane's log, as the harness writes it: the run is mid-turn, so a line is
		// half-written and its one complete message is what the transcript has.
		if err := os.MkdirAll(filepath.Join(h.laneRoot, "lane-b"), 0o755); err != nil {
			t.Fatalf("creating the lane: %v", err)
		}
		if err := os.WriteFile(filepath.Join(h.laneRoot, "lane-b", "run-live.log"), []byte(
			"{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"timestamp\":2000,"+
				"\"content\":[{\"type\":\"thinking\",\"thinking\":\"working on it\"}]}}\n"+
				"{\"type\":\"tool_execution_start\",\"toolCallId\":\"call-1\"}"), 0o644); err != nil {
			t.Fatalf("writing the live run's log: %v", err)
		}

		status, body, raw := h.get("/api/v1/runs/run-live/transcript")
		if status != http.StatusOK {
			t.Fatalf("a live run's transcript answered %d: %s", status, raw)
		}
		data, _ := body["data"].(map[string]any)
		if data["epistemic_state"] != "observed" {
			t.Fatalf("a live run's transcript answers %v: %s", data["epistemic_state"], raw)
		}
		entries, _ := data["entries"].([]any)
		if len(entries) != 1 || entries[0].(map[string]any)["kind"] != "thinking" {
			t.Fatalf("the live run's transcript is %v, want the one message it has written: %s",
				entries, raw)
		}
		if run, _ := data["run"].(string); run != "run-live" {
			t.Fatalf("the transcript answers run %q, want run-live", run)
		}
	}},
}

// boardItemOrder reads the snapshot's item ids in the order it emits them.
func boardItemOrder(t *testing.T, body map[string]any, raw []byte) []string {
	t.Helper()
	items, ok := body["data"].(map[string]any)["items"].([]any)
	if !ok {
		t.Fatalf("the snapshot carries no items array: %s", raw)
	}
	order := make([]string, 0, len(items))
	for _, entry := range items {
		item, _ := entry.(map[string]any)
		id, _ := item["item_id"].(string)
		order = append(order, id)
	}
	return order
}

// structSubjectItem is the subject the status case asks about.
var structSubjectItem = record.Subject{Kind: "item", ID: "item-seed-02"}

// TestServeCases runs every registered case. Each one is named, so the -v output names
// the behaviour rather than a number.
func TestServeCases(t *testing.T) {
	for _, c := range serveCases {
		t.Run(c.name, func(t *testing.T) {
			h := newServeHarness(t)
			c.run(t, h)
		})
	}
}

// TestProjectItemStateNeverInventsAnEighthState pins the closed vocabulary: whatever the
// store holds, the projected state is one of the seven the operator reads.
func TestProjectItemStateNeverInventsAnEighthState(t *testing.T) {
	allowed := map[string]bool{
		"not_started": true, "working": true, "blocked": true, "in_queue": true,
		"landed": true, "resolved_elsewhere": true, "closed": true,
	}
	raw := []string{"not-started", "in-progress", "blocked", "in-queue", "landed",
		"resolved-elsewhere", "closed", "completed", "abandoned", "deleted", "", "something-new"}
	for _, state := range raw {
		projected := ProjectItemState(storeItem{ItemID: "x", State: state}, nil, nil, nil, "")
		if !allowed[projected.State] {
			t.Fatalf("raw state %q projected to %q, which is not one of the seven", state, projected.State)
		}
	}
}

// TestTheDocumentRouteAnswersTheDesignDocument pins what the route answers about a
// document — never its prose — and the four refusals a client must be able to tell apart:
// an item that names no document, a document that is not there, a relative reference that
// climbs out of the served root, and an item the store does not carry.
//
// The negative control is the escape case: with the root check removed, that request
// answers 200 with the file outside the root instead of refusing it.
func TestTheDocumentRouteAnswersTheDesignDocument(t *testing.T) {
	// The count is Vantage's index answer, not a parse of the document: the fixture
	// document numbers its questions, which the retired dash-bullet parser read as none.
	index := `{"index":{"documents":[{"path":"docs/design.md","questions":[
		{"id":"OQ-1","state":"open","title":"OQ-1: does the first thing work?"},
		{"id":"OQ-2","state":"open","title":"OQ-2: does the second thing work?"}]}]}}`
	h := newServeHarnessWithQuestions(t, "", stubIndexCommand(t, index))
	docs := filepath.Join(h.dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("creating %s: %v", docs, err)
	}
	body := `---
title: "A design worth reading"
status: in-review
stage: DESIGN
updated: 2026-11-13
tags:
  - tillr
next: "rule OQ-1"
---

# A design

## 6. Open questions

- **[OQ-1](#OQ-1) — does the first thing work?**
- **[OQ-2](#OQ-2) — does the second thing work?**
`
	if err := os.WriteFile(filepath.Join(docs, "design.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing the design document: %v", err)
	}
	// The escaped document sits beside the root, which is where "../outside.md" lands: a
	// file the route must refuse to read rather than a path that happens not to resolve.
	outside := filepath.Join(filepath.Dir(h.dir), "outside.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("writing the escaped document: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store to set design documents: %v", err)
	}
	defer db.Close()
	for item, doc := range map[string]string{
		"item-seed-02": "docs/design.md",
		"item-seed-04": "docs/missing.md",
		"item-seed-06": "../outside.md",
	} {
		if _, err := db.Exec(`UPDATE items SET design_doc = ? WHERE item_id = ?`, doc, item); err != nil {
			t.Fatalf("pointing %s at %q: %v", item, doc, err)
		}
	}

	status, decoded, raw := h.get("/api/v1/items/item-seed-02/doc")
	if status != http.StatusOK {
		t.Fatalf("the document route answered %d: %s", status, raw)
	}
	data, _ := decoded["data"].(map[string]any)
	if _, carried := data["markdown"]; carried {
		t.Fatalf("the route answered the document's text; the board renders no prose: %s", raw)
	}
	// The path is the spelling a Vantage link takes: relative to the served root, so the
	// link the pane builds is a URL Vantage can resolve rather than a jail path.
	if got, _ := data["path"].(string); got != "docs/design.md" {
		t.Fatalf("the route answered path %q, want the root-relative %q", got, "docs/design.md")
	}
	if got, _ := data["bytes"].(float64); int(got) != len(body) {
		t.Fatalf("the route answered bytes %v, want the file's %d", data["bytes"], len(body))
	}

	// The metadata card: the five scalar keys, and none of the nested blocks.
	frontmatter, ok := data["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("the route answered no frontmatter object: %s", raw)
	}
	wantFrontmatter := map[string]string{
		"title":   "A design worth reading",
		"status":  "in-review",
		"stage":   "DESIGN",
		"updated": "2026-11-13",
		"next":    "rule OQ-1",
	}
	for key, want := range wantFrontmatter {
		if got, _ := frontmatter[key].(string); got != want {
			t.Fatalf("frontmatter[%q] is %v, want %q: %s", key, frontmatter[key], want, raw)
		}
	}
	for _, nested := range []string{"tags", "summary"} {
		if _, carried := frontmatter[nested]; carried {
			t.Fatalf("frontmatter carries the nested key %q; the card is a glance, not the block", nested)
		}
	}

	// The question count is Vantage's index answer, and it is a count: the questions
	// themselves are not on the wire.
	if got, _ := data["open_questions"].(float64); int(got) != 2 {
		t.Fatalf("open_questions is %v, want the index's 2 open questions: %s",
			data["open_questions"], raw)
	}
	if got, _ := data["questions_state"].(string); got != "questions-ok" {
		t.Fatalf("questions_state is %q, want questions-ok: %s", got, raw)
	}

	for _, c := range []struct{ item, want string }{
		{"item-seed-04", "design-doc-missing"},
		{"item-seed-06", "design-doc-outside-root"},
		{"item-seed-01", "item-not-found"},
	} {
		status, decoded, raw := h.get("/api/v1/items/" + c.item + "/doc")
		if status != http.StatusNotFound && status != http.StatusInternalServerError {
			t.Fatalf("%s answered %d, want a refusal: %s", c.item, status, raw)
		}
		body, _ := decoded["error"].(map[string]any)
		if body["code"] != c.want {
			t.Fatalf("%s refused with code %v, want %q: %s", c.item, body["code"], c.want, raw)
		}
	}
}

// An index that cannot answer is `questions-unknown`, not a zero count: the route says
// which answer it is and carries a null count, so a reader cannot mistake silence for a
// finished design.
//
// Negative control: TestTheDocumentRouteAnswersTheDesignDocument starts the same route
// with an index that answers and gets questions-ok with a count.
func TestTheDocumentRouteReportsUnknownQuestions(t *testing.T) {
	h := newServeHarnessWithQuestions(t, "", []string{"false"})
	docs := filepath.Join(h.dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("creating %s: %v", docs, err)
	}
	if err := os.WriteFile(filepath.Join(docs, "design.md"), []byte("# a design\n"), 0o644); err != nil {
		t.Fatalf("writing the design document: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store to set the design document: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET design_doc = ? WHERE item_id = ?`, "docs/design.md", "item-seed-02"); err != nil {
		t.Fatalf("pointing item-seed-02 at docs/design.md: %v", err)
	}

	status, decoded, raw := h.get("/api/v1/items/item-seed-02/doc")
	if status != http.StatusOK {
		t.Fatalf("the document route answered %d: %s", status, raw)
	}
	data, _ := decoded["data"].(map[string]any)
	if got, _ := data["questions_state"].(string); got != "questions-unknown" {
		t.Fatalf("questions_state is %q, want questions-unknown: %s", got, raw)
	}
	if count, carried := data["open_questions"]; !carried || count != nil {
		t.Fatalf("open_questions is %v (carried=%v), want null: %s", count, carried, raw)
	}
}

// TestTheDocumentRouteNamesTheSectionTheItemPointsAt pins `anchor`: the fragment an item's
// body_ref carries, answered only when body_ref and design_doc name the same document.
//
// Three answers are pinned because they are the three a client must act on: the anchor it
// should scroll to, silence when the fragment belongs to a different document (an anchor
// into a file this response does not carry is a lie), and silence when body_ref names no
// fragment at all. The normalisation case is the doc route's own: `./docs/design.md` and
// `docs/design.md` are one file.
//
// The negative control is the mismatch: with the path comparison removed, that request
// answers `a-design` for a document that has no such heading.
func TestTheDocumentRouteNamesTheSectionTheItemPointsAt(t *testing.T) {
	h := newServeHarness(t)
	docs := filepath.Join(h.dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("creating %s: %v", docs, err)
	}
	for _, name := range []string{"design.md", "other.md"} {
		body := "# A design\n\n## The section the item names\n"
		if err := os.WriteFile(filepath.Join(docs, name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store to set body references: %v", err)
	}
	defer db.Close()

	cases := []struct {
		item    string
		bodyRef string
		anchor  string
		why     string
	}{
		{"item-seed-02", "docs/design.md#the-section-the-item-names", "the-section-the-item-names",
			"the fragment and the document both name the design document"},
		{"item-seed-04", "./docs/design.md#the-section-the-item-names", "the-section-the-item-names",
			"a './' spelling of the same file is the same file"},
		{"item-seed-06", "docs/other.md#the-section-the-item-names", "",
			"the fragment names a section of a document this response does not carry"},
		{"item-seed-02", "docs/design.md", "",
			"body_ref carries no fragment, so there is no section to name"},
	}
	for _, c := range cases {
		if _, err := db.Exec(`UPDATE items SET body_ref = ?, design_doc = 'docs/design.md' WHERE item_id = ?`,
			c.bodyRef, c.item); err != nil {
			t.Fatalf("pointing %s at %q: %v", c.item, c.bodyRef, err)
		}
		status, decoded, raw := h.get("/api/v1/items/" + c.item + "/doc")
		if status != http.StatusOK {
			t.Fatalf("%s answered %d: %s", c.item, status, raw)
		}
		data, _ := decoded["data"].(map[string]any)
		got, ok := data["anchor"].(string)
		if !ok {
			t.Fatalf("%s answered no anchor field: %s", c.item, raw)
		}
		if got != c.anchor {
			t.Fatalf("%s (%s) answered anchor %q, want %q: %s", c.item, c.why, got, c.anchor, raw)
		}
	}
}

// TestTheSnapshotCarriesTheVantageBaseURL pins the one user-scope setting the board's
// document link is built from. It is on the response envelope rather than in the document
// payload because the link belongs to the board: a second surface that links into Vantage
// reads the same field, and a face with none configured leaves it out rather than
// answering an empty address the client would have to tell from a configured "".
func TestTheSnapshotCarriesTheVantageBaseURL(t *testing.T) {
	h := newServeHarness(t)
	_, body, raw := h.get("/api/v1/snapshot")
	snapshot, _ := body["snapshot"].(map[string]any)
	if got, carried := snapshot["vantage_base_url"]; carried {
		t.Fatalf("a face with no base URL configured answered %v: %s", got, raw)
	}

	configured := newServeHarnessWith(t, "http://vantage.test:1234")
	_, body, raw = configured.get("/api/v1/snapshot")
	snapshot, _ = body["snapshot"].(map[string]any)
	if got, _ := snapshot["vantage_base_url"].(string); got != "http://vantage.test:1234" {
		t.Fatalf("the snapshot answered vantage_base_url %v, want the configured base: %s",
			snapshot["vantage_base_url"], raw)
	}
}

// TestVantageBaseURLPrefersTheFlagAndTrimsTheSlash pins the two spellings of the setting
// and the one rule the built link depends on: the flag wins over the environment, and a
// trailing slash is removed so the base and the document's path meet at one slash.
func TestVantageBaseURLPrefersTheFlagAndTrimsTheSlash(t *testing.T) {
	t.Setenv(vantageBaseEnv, "http://from-env:1/")
	if got := vantageBaseURL(""); got != "http://from-env:1" {
		t.Fatalf("an unset flag answered %q, want the environment's base", got)
	}
	if got := vantageBaseURL("http://from-flag:2/"); got != "http://from-flag:2" {
		t.Fatalf("the flag answered %q, want the flag's base", got)
	}
	t.Setenv(vantageBaseEnv, "")
	if got := vantageBaseURL(""); got != "" {
		t.Fatalf("no setting answered %q, want empty", got)
	}
}

// TestEveryResponseNamesTheBuildAndAUIDoesNot is the owner's question, pinned: "am I looking
// at the current build?" Two failures cost a debugging session each on 2026-11-14 and were
// one shape — a stale server that looked authoritative. It drew the queue in item-id order
// because its payload carried no `rank` (the client's order is rank-then-id), and it answered
// `/api/v1/items/{id}/doc` with "not a route this face serves". Nothing on screen said which
// program was answering.
//
// So: the cursor carries the build on every response, including a refusal, and a server with
// no front end names the executable serving it and the reason the pack-delivered binary never
// has one.
func TestEveryResponseNamesTheBuildAndAUIDoesNot(t *testing.T) {
	h := newServeHarness(t)
	// The harness carries no web assets, which is the delivered binary's state and the one
	// whose message the owner hit.
	h.srv.web = nil
	h.srv.build = buildInfo("none")

	status, decoded, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/snapshot: status %d, want 200\n%s", status, raw)
	}
	build, ok := decoded["snapshot"].(map[string]any)["build"].(map[string]any)
	if !ok {
		t.Fatalf("the cursor carries no build block, so the board cannot say which program answered:\n%s", raw)
	}
	if got := build["executable"]; got == "" || got == nil {
		t.Errorf("build.executable is %v: the stamp must name the program, which is the question it answers", got)
	}
	if got, _ := build["front_end"].(string); got != "none" {
		t.Errorf("build.front_end is %q, want %q for a server with no assets", got, "none")
	}

	// The page a reader sees, which is the message they pasted.
	resp, err := http.Get(h.http.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	var page bytes.Buffer
	if _, err := page.ReadFrom(resp.Body); err != nil {
		t.Fatalf("reading /: %v", err)
	}
	for _, want := range []string{
		"carries no built front end",
		"just observatory <port>",
		"the pack-delivered `tillr-view` on PATH never carries a front end",
		"front-end none",
	} {
		if !bytes.Contains(page.Bytes(), []byte(want)) {
			t.Errorf("the no-UI page does not say %q, so a reader cannot tell the delivered binary from an unbuilt checkout:\n%s",
				want, page.String())
		}
	}

	// And the dev shape, which is the one that read wrong: the API serves no UI because
	// another process has it, and saying `none` beside the build told a reader their board
	// had no interface while it was on screen. `--ui-at` names where the UI is, and the
	// page says where to look instead of naming a remedy for a problem that is not there.
	devWeb, devNote, code := openWebAssets("", "vite")
	if code != record.ExitOK || devWeb != nil {
		t.Fatalf("openWebAssets(\"\", \"vite\") = %v, %q, %d; want no fs, the label, ok", devWeb, devNote, code)
	}
	h.srv.build = buildInfo(frontEndLabel(devWeb, devNote))
	_, decoded, raw = h.get("/api/v1/snapshot")
	devBuild := decoded["snapshot"].(map[string]any)["build"].(map[string]any)
	if got, _ := devBuild["front_end"].(string); got != "vite" {
		t.Errorf("build.front_end is %q for a --ui-at server, want %q", got, "vite")
	}
	devResp, err := http.Get(h.http.URL + "/")
	if err != nil {
		t.Fatalf("GET / with a UI elsewhere: %v", err)
	}
	defer devResp.Body.Close()
	page.Reset()
	if _, err := page.ReadFrom(devResp.Body); err != nil {
		t.Fatalf("reading /: %v", err)
	}
	if !bytes.Contains(page.Bytes(), []byte("the board's UI is served by vite")) {
		t.Errorf("the API's page does not say where the UI is, so a reader is told a remedy instead of a location:\n%s", page.String())
	}
	if bytes.Contains(page.Bytes(), []byte("carries no built front end")) {
		t.Errorf("a --ui-at server still reports itself as carrying no front end, and the board is right there:\n%s", page.String())
	}

}

// The board's gate line is the loop's own measurement: the last tick's gate wall time, the
// budget it was measured against, and the recent trend. The number is the gate's, recorded
// beside the staging, dispatch and landing times, so a slow lane is never read as a slow
// gate.
//
// The fixture's second tick is over its budget, which is the row an operator must be able
// to see the night it happens. A store the loop has not measured yet answers no gate, not a
// zero: zero would read as a fast gate, the one thing this number exists to disprove.
func TestTheBoardShowsTheGateTimeAndItsTrend(t *testing.T) {
	h := newServeHarness(t)
	_, decoded, raw := h.get("/api/v1/snapshot")
	data, _ := decoded["data"].(map[string]any)
	summary, _ := data["drain_summary"].(map[string]any)
	if summary == nil {
		t.Fatalf("the snapshot carries no drain_summary:\n%s", raw)
	}
	if got, _ := summary["gate_ms"].(float64); got != 400000 {
		t.Errorf("drain_summary.gate_ms = %v, want 400000 (the last tick's gate):\n%s", summary["gate_ms"], raw)
	}
	if got, _ := summary["gate_budget_ms"].(float64); got != 330000 {
		t.Errorf("drain_summary.gate_budget_ms = %v, want 330000", summary["gate_budget_ms"])
	}
	trend, _ := summary["gate_trend"].([]any)
	if len(trend) != 2 {
		t.Fatalf("drain_summary.gate_trend carries %d tick(s), want 2:\n%s", len(trend), raw)
	}
	// Oldest first, and the overrun is stated rather than inferred from the number.
	first, _ := trend[0].(map[string]any)
	last, _ := trend[1].(map[string]any)
	if got, _ := first["tick_id"].(string); got != "tick-1" {
		t.Errorf("gate_trend[0].tick_id = %q, want tick-1 (the trend is oldest first)", got)
	}
	if got, _ := last["tick_id"].(string); got != "tick-2" {
		t.Errorf("gate_trend[1].tick_id = %q, want tick-2", got)
	}
	if on, _ := first["over_budget"].(bool); on {
		t.Errorf("gate_trend[0].over_budget = true, want false: 71.6s is inside a 330s budget")
	}
	if on, _ := last["over_budget"].(bool); !on {
		t.Errorf("gate_trend[1].over_budget = false, want true: 400s is over a 330s budget")
	}
}

// The header bar's tick count is how many ticks the loop ran, not how many decision rows
// they wrote. A tick writes one row per item it considered, so the two differ by the queue
// width: measured on the live store on 2026-11-14, 27 ticks had written 1548 rows and the
// header bar drew "1548 ticks".
func TestTheBoardCountsTicksNotDecisions(t *testing.T) {
	h := newServeHarness(t)
	_, decoded, raw := h.get("/api/v1/snapshot")
	data, _ := decoded["data"].(map[string]any)
	summary, _ := data["drain_summary"].(map[string]any)
	if summary == nil {
		t.Fatalf("the snapshot carries no drain_summary:\n%s", raw)
	}
	// The fixture holds four decision rows over two ticks. The tick count is the ticks; a
	// row count would answer 4, and a store with a wider queue would answer more for the
	// same number of ticks.
	if got, _ := summary["ticks"].(float64); got != 2 {
		t.Errorf("drain_summary.ticks = %v, want 2 (four decision rows over two ticks):\n%s", summary["ticks"], raw)
	}
	if got, _ := summary["dispatched"].(float64); got != 2 {
		t.Errorf("drain_summary.dispatched = %v, want 2 (the two `dispatch` rows):\n%s", summary["dispatched"], raw)
	}
}

// The board's stop reason is the loop's own result file, not a promise about a future
// slice. The loop writes a `drain.loop` record at <root>/.tillr/loop/drain-<stamp>.json
// when it stops; the snapshot reads the newest one, so "why did the night stop" is
// answered from the record the loop left rather than from the terminal that ran it.
//
// Negative control: with no result file the snapshot answers a null reason and an
// absence note, and the note names no other slice as the writer.
func TestTheBoardReadsTheStopReasonFromTheLoopsResultFile(t *testing.T) {
	h := newServeHarness(t)

	summary := func() map[string]any {
		t.Helper()
		_, decoded, raw := h.get("/api/v1/snapshot")
		data, _ := decoded["data"].(map[string]any)
		summary, _ := data["drain_summary"].(map[string]any)
		if summary == nil {
			t.Fatalf("the snapshot carries no drain_summary:\n%s", raw)
		}
		return summary
	}

	// No result file: the reason is unrecorded, and the note says so rather than naming
	// another slice as the writer.
	before := summary()
	if before["last_stop_reason"] != nil {
		t.Fatalf("last_stop_reason = %v with no result file, want null", before["last_stop_reason"])
	}
	if note, _ := before["stop_note"].(string); strings.Contains(note, "OBS-1") {
		t.Fatalf("stop_note still promises another slice: %q", note)
	}
	if before["build"] != nil {
		t.Fatalf("build = %v with no result file, want null", before["build"])
	}
	if note, _ := before["build_note"].(string); note == "" {
		t.Fatal("no result file and build_note is empty: an unnamed drain build must be drawn as unnamed")
	}

	// The loop's own file, in the shape the loop writes it: a drain.loop record whose
	// payload carries the stop reason. The board must read that reason, not a note.
	writeLoopResult(t, h.loopDir, "20261113T065219Z", "queue-drained", nil)
	got := summary()
	if got["last_stop_reason"] != "queue-drained" {
		t.Fatalf("last_stop_reason = %v, want %q from the loop's result file",
			got["last_stop_reason"], "queue-drained")
	}
	if note, _ := got["stop_note"].(string); note != "" {
		t.Fatalf("stop_note = %q beside a recorded reason, want no note", note)
	}
	// A result file written before the build stamp existed names no build: the board says
	// it cannot vouch for that drain rather than drawing an empty stamp as current.
	if got["build"] != nil {
		t.Fatalf("build = %v for a record with no stamp, want null", got["build"])
	}
	if note, _ := got["build_note"].(string); !strings.Contains(note, "cannot vouch") {
		t.Fatalf("build_note = %q for a record with no stamp, want it to say the board cannot vouch", note)
	}

	// The newest result is the loop's last stop: a later file wins over an earlier one, and
	// the build it names is the one the board reports.
	writeLoopResult(t, h.loopDir, "20261114T000000Z", "operator-stop", &drainBuild{
		Revision:   "4a79a8e7f383d495c32f8d21c4454575dfe90be8",
		Time:       "2026-11-14T11:00:00Z",
		Digest:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Executable: "/workspace/.yolo/durable/bin/tillr-drain",
	})
	got = summary()
	if got["last_stop_reason"] != "operator-stop" {
		t.Fatalf("last_stop_reason = %v, want the newest result's %q",
			got["last_stop_reason"], "operator-stop")
	}
	build, _ := got["build"].(map[string]any)
	if build == nil {
		t.Fatalf("build = %v for a stamped result, want the stamp the loop wrote", got["build"])
	}
	if build["revision"] != "4a79a8e7f383d495c32f8d21c4454575dfe90be8" {
		t.Fatalf("build.revision = %v, want the recorded revision", build["revision"])
	}
	if note, _ := got["build_note"].(string); note != "" {
		t.Fatalf("build_note = %q beside a named build, want no note", note)
	}
}

// writeLoopResult writes one loop result record the way tillr-drain does: a drain.loop
// envelope whose payload carries the stop reason and, when the case supplies one, the build
// stamp. A nil build is the older record a board must draw as a build it cannot name.
func writeLoopResult(t *testing.T, loopDir, stamp, stop string, build *drainBuild) {
	t.Helper()
	result := map[string]any{
		"stop": stop, "ticks": 1, "detail": "a full tick planned nothing dispatchable",
	}
	if build != nil {
		result["build"] = build
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encoding the loop result payload: %v", err)
	}
	at, err := time.Parse(time.RFC3339, "2026-11-13T06:52:19Z")
	if err != nil {
		t.Fatalf("parsing the fixture clock: %v", err)
	}
	body, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          "drain.loop",
		ID:            "loop-" + stamp,
		Producer:      record.Producer{System: record.System, Utility: "drain", Version: "0.1.0"},
		CreatedAt:     at,
		Payload:       payload,
	})
	if err != nil {
		t.Fatalf("encoding the loop result record: %v", err)
	}
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatalf("creating the loop directory: %v", err)
	}
	path := filepath.Join(loopDir, "drain-"+stamp+".json")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("writing the loop result: %v", err)
	}
}

// TestALiveRunIsProjectedFromItsLease is the board's half of the drain's own record: a run
// that is still going has NO `runs` row, because the terminal write creates it. The board
// therefore used to answer "the store records no run" for an item a lane was working at
// that moment, and counted zero running while a lease was held.
//
// Measured 2026-11-14 during the first real drain: `lane-1` was `leased` on
// `run-item-obs-filter-20261114T063154Z`, `counts.runs_live` answered 0 and
// `drain_summary.running_runs` answered 0. The lane row and the `run.allocate` event the
// claim appended are both in the store, so the projection has everything it needs.
//
// This case fails if the live-run projection is removed, which is the negative control.
func TestALiveRunIsProjectedFromItsLease(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store to add a lease: %v", err)
	}
	defer db.Close()
	// A second lane holding a run no `runs` row exists for, and the allocation event its
	// claim appended: the exact shape a live drain leaves.
	if _, err := db.Exec(`INSERT INTO lanes (lane_id, state, run_id, leased_until)
		VALUES ('lane-b', 'leased', 'run-live', 990000)`); err != nil {
		t.Fatalf("leasing lane-b: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
		VALUES (5000, 'run.allocate', 'lane', 'lane-b',
		  '{"run_id":"run-live","item_id":"item-seed-06","backend":"pi","lane_id":"lane-b",
		    "started_at":"2026-11-14T06:31:54Z","leased_until":990000}')`); err != nil {
		t.Fatalf("appending the allocation: %v", err)
	}

	_, decoded, raw := h.get("/api/v1/snapshot")
	data, _ := decoded["data"].(map[string]any)
	counts, _ := data["counts"].(map[string]any)
	// run-2 is a row the fixture gives the state `running`; run-live is the projection. Two
	// live runs, one from each half of the ledger, is the point: the row wins where it
	// exists and the lease answers where it does not.
	if got, _ := counts["runs_live"].(float64); got != 2 {
		t.Errorf("counts.runs_live = %v, want 2 (the row the store holds plus the run only the lease knows):\n%s", got, raw)
	}
	summary, _ := data["drain_summary"].(map[string]any)
	if got, _ := summary["running_runs"].(float64); got != 2 {
		t.Errorf("drain_summary.running_runs = %v, want 2", got)
	}

	// The item pane's answer for the item being worked: a run, named, with the start the
	// allocation recorded — not "the store records no run".
	var projected map[string]any
	for _, raw := range data["items"].([]any) {
		item, _ := raw.(map[string]any)
		if item["item_id"] == "item-seed-06" {
			projected, _ = item["latest_run"].(map[string]any)
		}
	}
	if projected == nil {
		t.Fatalf("item-seed-06 carries no latest_run, so the pane would say the store records no run while lane-b holds it:\n%s", raw)
	}
	if got, _ := projected["run_id"].(string); got != "run-live" {
		t.Errorf("latest_run.run_id = %q, want run-live", got)
	}
	if got, _ := projected["state"].(string); got != "running" {
		t.Errorf("latest_run.state = %q, want running", got)
	}
	// The clock is the allocation's, stated here independently of the read: a projection
	// that took the observed time instead of the recorded one would put a live run's start
	// at the moment the board was loaded.
	wantStart := float64(time.Date(2026, 11, 14, 6, 31, 54, 0, time.UTC).UnixMilli())
	if got, _ := projected["started_at_ms"].(float64); got != wantStart {
		t.Errorf("latest_run.started_at_ms = %v, want %v (2026-11-14T06:31:54Z):\n%s", got, wantStart, raw)
	}
}

// stubMutableIndexCommand is stubIndexCommand for a case that answers twice: the script
// reads the file the test rewrites, so the route's second read sees the second index.
func stubMutableIndexCommand(t *testing.T) ([]string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	script := filepath.Join(dir, "index.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat "+path+"\n"), 0o755); err != nil {
		t.Fatalf("writing the mutable index script: %v", err)
	}
	return []string{script}, path
}

// pointItemAt sets an item's design_doc and closes the handle. It is the fixture's own
// write, not the face's: the route reads the reference the store holds.
func pointItemAt(t *testing.T, storePath, itemID, doc string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+storePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store to point %s at %q: %v", itemID, doc, err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET design_doc = ? WHERE item_id = ?`, doc, itemID); err != nil {
		t.Fatalf("pointing %s at %q: %v", itemID, doc, err)
	}
}

// holdItem writes the loop's decision for one item, which is what the rail's hold reads:
// the board does not re-run the index to decide that an item is waiting on a ruling.
func holdItem(t *testing.T, storePath, itemID, reason string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+storePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store to hold %s: %v", itemID, err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at)
		VALUES (?, ?, NULL, NULL, 'hold', ?, 5000)`, "decision-"+itemID+"-hold", itemID, reason); err != nil {
		t.Fatalf("holding %s for %q: %v", itemID, reason, err)
	}
}

// TestTheQuestionsRouteListsEveryOpenQuestion pins the one place: each item whose document
// still asks something, its open question ids, and the document each link is built from.
// The answered question is not listed — a settled question is an answer.
//
// The snapshot's rail state and header count are asserted beside it, because the criterion
// is that the list, the rail and the header agree with the planner: the loop's own
// `question-open` decision makes the item blocked with the reason on it, and the header
// counts the hold.
func TestTheQuestionsRouteListsEveryOpenQuestion(t *testing.T) {
	index := `{"index":{"documents":[{"path":"docs/design.md","questions":[
		{"id":"OQ-1","state":"open","title":"OQ-1: does the first thing work?"},
		{"id":"OQ-2","state":"answered","title":"OQ-2: does the second thing work?"},
		{"id":"OQ-3","state":"open","title":"OQ-3: does the third thing work?"}]}]}}`
	h := newServeHarnessWithQuestions(t, "", stubIndexCommand(t, index))
	docs := filepath.Join(h.dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("creating %s: %v", docs, err)
	}
	if err := os.WriteFile(filepath.Join(docs, "design.md"), []byte("# a design\n"), 0o644); err != nil {
		t.Fatalf("writing the design document: %v", err)
	}
	pointItemAt(t, h.store, "item-seed-02", "docs/design.md")
	holdItem(t, h.store, "item-seed-02", "question-open")

	status, decoded, raw := h.get("/api/v1/questions")
	if status != http.StatusOK {
		t.Fatalf("the questions route answered %d: %s", status, raw)
	}
	data, _ := decoded["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the questions route listed %d item(s), want the one with an open question: %s", len(items), raw)
	}
	item, _ := items[0].(map[string]any)
	if got, _ := item["item_id"].(string); got != "item-seed-02" {
		t.Fatalf("the listed item is %q, want item-seed-02: %s", got, raw)
	}
	if got, _ := item["design_doc"].(string); got != "docs/design.md" {
		t.Fatalf("the listed document is %q, want docs/design.md: %s", got, raw)
	}
	if got, _ := item["hold"].(string); got != "question-open" {
		t.Fatalf("the list's hold is %q, want the planner's question-open: %s", got, raw)
	}
	questions, _ := item["questions"].([]any)
	if len(questions) != 2 {
		t.Fatalf("the item carries %d question(s), want the two open ones: %s", len(questions), raw)
	}
	for i, want := range []string{"OQ-1", "OQ-3"} {
		got, _ := questions[i].(map[string]any)["question_id"].(string)
		if got != want {
			t.Fatalf("question %d is %q, want %q: %s", i, got, want, raw)
		}
	}
	counts, _ := data["counts"].(map[string]any)
	for key, want := range map[string]float64{"items": 1, "questions": 2, "holds": 1} {
		if got, _ := counts[key].(float64); got != want {
			t.Fatalf("counts[%q] is %v, want %v: %s", key, counts[key], want, raw)
		}
	}
	if got, _ := data["state"].(string); got != "questions-ok" {
		t.Fatalf("the list's state is %q, want questions-ok: %s", got, raw)
	}

	// The same item in the snapshot: blocked with the planner's reason on it, and counted
	// as a question hold rather than as a dispatchable in-queue row.
	_, snapshot, snapshotRaw := h.get("/api/v1/snapshot")
	snapshotData, _ := snapshot["data"].(map[string]any)
	var projected map[string]any
	for _, row := range snapshotData["items"].([]any) {
		entry, _ := row.(map[string]any)
		if id, _ := entry["item_id"].(string); id == "item-seed-02" {
			projected, _ = entry["projected_state"].(map[string]any)
		}
	}
	if projected == nil {
		t.Fatalf("the snapshot carries no item-seed-02: %s", snapshotRaw)
	}
	if got, _ := projected["state"].(string); got != "blocked" {
		t.Fatalf("the rail state is %q, want blocked for a question hold: %s", got, snapshotRaw)
	}
	if got, _ := projected["hold"].(string); got != "question-open" {
		t.Fatalf("the rail hold is %q, want question-open: %s", got, snapshotRaw)
	}
	counts, _ = snapshotData["counts"].(map[string]any)
	if got, _ := counts["question_holds"].(float64); got != 1 {
		t.Fatalf("the header counts %v question hold(s), want 1: %s", counts["question_holds"], snapshotRaw)
	}
}

// TestTheQuestionsRouteKeepsUnknownAndMissingApart pins the two answers that are not the
// list: an index that could not be read, and a document the tree does not hold. Neither is
// drawn as "no questions".
func TestTheQuestionsRouteKeepsUnknownAndMissingApart(t *testing.T) {
	h := newServeHarnessWithQuestions(t, "", []string{"false"})
	docs := filepath.Join(h.dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("creating %s: %v", docs, err)
	}
	if err := os.WriteFile(filepath.Join(docs, "design.md"), []byte("# a design\n"), 0o644); err != nil {
		t.Fatalf("writing the design document: %v", err)
	}
	pointItemAt(t, h.store, "item-seed-02", "docs/design.md")
	pointItemAt(t, h.store, "item-seed-04", "docs/missing.md")

	status, decoded, raw := h.get("/api/v1/questions")
	if status != http.StatusOK {
		t.Fatalf("the questions route answered %d: %s", status, raw)
	}
	data, _ := decoded["data"].(map[string]any)
	if got, _ := data["state"].(string); got != "questions-unknown" {
		t.Fatalf("the list's state is %q, want questions-unknown: %s", got, raw)
	}
	if items, _ := data["items"].([]any); len(items) != 0 {
		t.Fatalf("an unread index listed %d item(s), want none: %s", len(items), raw)
	}
	unknown, _ := data["unknown"].([]any)
	if len(unknown) != 1 {
		t.Fatalf("the unknown list carries %d item(s), want item-seed-02: %s", len(unknown), raw)
	}
	if got, _ := unknown[0].(map[string]any)["item_id"].(string); got != "item-seed-02" {
		t.Fatalf("the unknown item is %q, want item-seed-02: %s", got, raw)
	}
	missing, _ := data["missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("the missing list carries %d item(s), want item-seed-04: %s", len(missing), raw)
	}
	if got, _ := missing[0].(map[string]any)["item_id"].(string); got != "item-seed-04" {
		t.Fatalf("the missing item is %q, want item-seed-04: %s", got, raw)
	}
}

// TestTheQuestionsListDropsAnAnsweredQuestionOnTheNextRead pins the turnaround: the route
// rebuilds the index when the tree's Markdown changes (a caller-named command is never
// reused), so an answer written in Vantage leaves the list on the next read rather than
// after a restart.
func TestTheQuestionsListDropsAnAnsweredQuestionOnTheNextRead(t *testing.T) {
	command, indexPath := stubMutableIndexCommand(t)
	h := newServeHarnessWithQuestions(t, "", command)
	docs := filepath.Join(h.dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("creating %s: %v", docs, err)
	}
	if err := os.WriteFile(filepath.Join(docs, "design.md"), []byte("# a design\n"), 0o644); err != nil {
		t.Fatalf("writing the design document: %v", err)
	}
	pointItemAt(t, h.store, "item-seed-02", "docs/design.md")
	writeIndex := func(state string) {
		t.Helper()
		index := `{"index":{"documents":[{"path":"docs/design.md","questions":[{"id":"OQ-1","state":"` + state + `","title":"OQ-1: is it ruled?"}]}]}}`
		if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
			t.Fatalf("writing the index: %v", err)
		}
	}

	writeIndex("open")
	_, first, firstRaw := h.get("/api/v1/questions")
	firstData, _ := first["data"].(map[string]any)
	if items, _ := firstData["items"].([]any); len(items) != 1 {
		t.Fatalf("the open question did not list the item: %s", firstRaw)
	}

	writeIndex("answered")
	_, second, secondRaw := h.get("/api/v1/questions")
	secondData, _ := second["data"].(map[string]any)
	if items, _ := secondData["items"].([]any); len(items) != 0 {
		t.Fatalf("an answered question still listed the item: %s", secondRaw)
	}
}
