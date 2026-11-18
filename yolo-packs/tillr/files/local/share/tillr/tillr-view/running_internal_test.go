package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// runningHarness is the smallest board the running face can be asked about: one lane
// working a run the store has no row for, one lane idle, one run that finished and landed,
// and the loop result that says why the loop stopped. The three blocks are three different
// sources on purpose — a view that read only the `runs` table would show one of them.
type runningHarness struct {
	t        *testing.T
	store    string
	loopDir  string
	laneRoot string
	// now is the viewer's clock the assertions read elapsed and lease-left against.
	now time.Time
}

func newRunningHarness(t *testing.T) *runningHarness {
	t.Helper()
	dir := t.TempDir()
	h := &runningHarness{
		t:        t,
		store:    filepath.Join(dir, "state.db"),
		loopDir:  filepath.Join(dir, "loop"),
		laneRoot: filepath.Join(dir, "lanes"),
		now:      time.Date(2026, 11, 13, 6, 1, 5, 0, time.UTC),
	}
	h.seed()
	return h
}

// seed writes the fixture's tables and the three rows the view's three blocks come from.
// The live run is a lane lease plus a `run.allocate` event and no `runs` row, exactly the
// shape `run.Exec` leaves while it is working.
func (h *runningHarness) seed() {
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
		h.t.Fatalf("opening the store: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(string(schema)); err != nil {
		h.t.Fatalf("creating the fixture tables: %v", err)
	}
	started := time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)
	allocation, err := json.Marshal(map[string]any{
		"run_id": "run-live", "item_id": "item-live", "backend": "pi", "lane_id": "lane-1",
		"started_at": started.Format(time.RFC3339),
	})
	if err != nil {
		h.t.Fatalf("encoding the allocation: %v", err)
	}
	verdict, err := json.Marshal(map[string]any{
		"run_id": "run-done", "verdict_id": "verdict-1", "result": "pass",
	})
	if err != nil {
		h.t.Fatalf("encoding the verdict: %v", err)
	}
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO items (item_id, title, body_ref, state, updated_at)
		  VALUES ('item-live', 'live item', '', 'in-queue', 1000)`, nil},
		{`INSERT INTO items (item_id, title, body_ref, state, updated_at)
		  VALUES ('item-done', 'done item', '', 'in-queue', 2000)`, nil},
		{`INSERT INTO lanes (lane_id, state, run_id, leased_until)
		  VALUES ('lane-1', 'leased', 'run-live', ?)`, []any{h.now.Add(115 * time.Second).UnixMilli()}},
		{`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES ('lane-2', 'idle', NULL, NULL)`, nil},
		{`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, usage_cost_usd)
		  VALUES ('run-done', 'item-done', 'lane-1', 'pi', 'exited', 0, 1000, 2000, 0.25)`, nil},
		{`INSERT INTO candidates (candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at)
		  VALUES ('cand-1', 'item-done', 'main', 'base', 'head', 'tree', 'landed', 1500)`, nil},
		{`INSERT INTO landing_attempts (attempt_id, candidate_id, verdict_id, result, trunk_before, trunk_after)
		  VALUES ('attempt-1', 'cand-1', 'verdict-1', 'landed', 'trunk-before', 'landed-sha')`, nil},
		{`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (?, 'run.allocate', 'run', 'run-live', ?)`,
			[]any{started.UnixMilli(), string(allocation)}},
		{`INSERT INTO events (ts, kind, object_type, object_id, payload) VALUES (?, 'gate.verdict', 'run', 'run-done', ?)`,
			[]any{time.Date(2026, 11, 13, 6, 30, 0, 0, time.UTC).UnixMilli(), string(verdict)}},
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			h.t.Fatalf("seeding the store with %q: %v", stmt.sql[:40], err)
		}
	}
	writeLoopResult(h.t, h.loopDir, "20261113T065219Z", "queue-drained", nil)
	if err := os.MkdirAll(filepath.Join(h.laneRoot, "lane-1"), 0o755); err != nil {
		h.t.Fatalf("creating the lane directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.laneRoot, "lane-1", "run-live.log"), []byte("{}\n"), 0o644); err != nil {
		h.t.Fatalf("writing the run's log: %v", err)
	}
}

// addEvent appends one event at ts and answers its sequence. It is how the stale case puts
// a newer record beside the loop result the harness wrote.
func (h *runningHarness) addEvent(ts time.Time, kind, objectID, payload string) int64 {
	h.t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		h.t.Fatalf("opening the store: %v", err)
	}
	defer db.Close()
	res, err := db.Exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
		VALUES (?, ?, 'run', ?, ?)`, ts.UnixMilli(), kind, objectID, payload)
	if err != nil {
		h.t.Fatalf("appending the event: %v", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		h.t.Fatalf("reading the event's seq: %v", err)
	}
	return seq
}

// TestRunningJoinsTheThreeSources is the item's own test: one live lane, one finished run
// and one loop file, each in the block it belongs to and nowhere else. The live run is
// found from the lease and its allocation (the store has no `runs` row for it), the
// finished run from its `runs` row and its landing attempt, and the stop reason from the
// loop file.
func TestRunningJoinsTheThreeSources(t *testing.T) {
	h := newRunningHarness(t)
	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatalf("reading the running view: %v", err)
	}

	if payload.Lanes.Total != 2 || payload.Lanes.Leased != 1 {
		t.Fatalf("lanes = %+v, want 1 leased of 2", payload.Lanes)
	}
	if len(payload.Lanes.Idle) != 1 || payload.Lanes.Idle[0] != "lane-2" {
		t.Fatalf("idle lanes = %v, want [lane-2]", payload.Lanes.Idle)
	}

	if len(payload.Active) != 1 {
		t.Fatalf("active runs = %d, want 1: %+v", len(payload.Active), payload.Active)
	}
	live := payload.Active[0]
	if live.RunID != "run-live" || live.ItemID != "item-live" || live.LaneID != "lane-1" {
		t.Fatalf("active run = %+v, want run-live on item-live in lane-1", live)
	}
	if live.Title != "live item" {
		t.Fatalf("active title = %q, want the item's title", live.Title)
	}
	if live.ElapsedMS == nil || *live.ElapsedMS != 65_000 {
		t.Fatalf("elapsed = %v, want 65000ms from the allocation's start", live.ElapsedMS)
	}
	if live.LeaseLeftMS == nil || *live.LeaseLeftMS != 115_000 {
		t.Fatalf("lease left = %v, want 115000ms", live.LeaseLeftMS)
	}
	if want := filepath.Join(h.laneRoot, "lane-1", "run-live.log"); live.LogPath != want {
		t.Fatalf("log path = %q, want %q", live.LogPath, want)
	}

	if len(payload.Finished) != 1 {
		t.Fatalf("finished runs = %d, want 1: %+v", len(payload.Finished), payload.Finished)
	}
	done := payload.Finished[0]
	if done.RunID != "run-done" || done.ItemID != "item-done" {
		t.Fatalf("finished run = %+v, want run-done on item-done", done)
	}
	if done.Outcome != "landed landed-sha" || done.LandedSHA != "landed-sha" {
		t.Fatalf("outcome = %q (sha %q), want landed landed-sha", done.Outcome, done.LandedSHA)
	}
	if done.DurationMS != 1000 {
		t.Fatalf("duration = %dms, want 1000", done.DurationMS)
	}
	if done.CostUSD == nil || *done.CostUSD != 0.25 {
		t.Fatalf("cost = %v, want 0.25", done.CostUSD)
	}

	if payload.Loop.StopReason != "queue-drained" {
		t.Fatalf("stop reason = %q, want queue-drained", payload.Loop.StopReason)
	}
	if payload.Loop.Stale {
		t.Fatalf("the loop record is stale beside no newer event: %+v", payload.Loop)
	}

	// The negative half: the three sources are three blocks, and a row that appeared in
	// the wrong one would mean the face had borrowed another source's answer.
	for _, run := range payload.Active {
		if run.RunID == done.RunID {
			t.Fatalf("the finished run %q also appears as active", done.RunID)
		}
	}
	for _, run := range payload.Finished {
		if run.RunID == live.RunID {
			t.Fatalf("the live run %q also appears as finished", live.RunID)
		}
	}
}

// TestRunningRefusesAStaleStopRecord is the take-6 shape: a loop file says the loop
// stopped, and a later event says something ran after it. The header must not present the
// old stop as current, and the seq it names is the newer record an operator can go read.
func TestRunningRefusesAStaleStopRecord(t *testing.T) {
	h := newRunningHarness(t)
	seq := h.addEvent(time.Date(2026, 11, 13, 7, 0, 0, 0, time.UTC), "run.allocate", "run-later",
		`{"run_id":"run-later","item_id":"item-late","backend":"pi","lane_id":"lane-1","started_at":"2026-11-13T07:00:00Z"}`)

	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatalf("reading the running view: %v", err)
	}
	if !payload.Loop.Stale {
		t.Fatalf("a newer event did not make the stop record stale: %+v", payload.Loop)
	}
	if payload.Loop.StaleSeq != seq {
		t.Fatalf("stale_seq = %d, want the newer event's seq %d", payload.Loop.StaleSeq, seq)
	}
	want := "stale stop record — newer run exists at seq " + strconv.FormatInt(seq, 10)
	if payload.Loop.StaleNote != want {
		t.Fatalf("stale_note = %q, want %q", payload.Loop.StaleNote, want)
	}

	var out bytes.Buffer
	renderRunning(&out, payload)
	if !strings.Contains(out.String(), want) {
		t.Fatalf("the table does not say the stop record is stale:\n%s", out.String())
	}
}

func TestRunningLiveSessionSupersedesOldStop(t *testing.T) {
	h := newRunningHarness(t)
	h.now = time.Date(2026, 11, 13, 7, 1, 0, 0, time.UTC)
	h.addEvent(h.now, "run.allocate", "run-later", `{}`)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER,
	 refreshed_at INTEGER, next_pass_at INTEGER, ended_at INTEGER);
	 INSERT INTO drain_sessions VALUES ('session-live', ?, ?, ?, NULL)`,
		h.now.Add(-30*time.Second).UnixMilli(), h.now.Add(-time.Second).UnixMilli(), h.now.Add(time.Second).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"live", "overdue", "ended"} {
		t.Run(state, func(t *testing.T) {
			if state == "overdue" {
				_, err = db.Exec(`UPDATE drain_sessions SET next_pass_at = ?`, h.now.Add(-6*time.Second).UnixMilli())
			}
			if state == "ended" {
				_, err = db.Exec(`UPDATE drain_sessions SET next_pass_at = ?, ended_at = ?`, h.now.Add(time.Second).UnixMilli(), h.now.UnixMilli())
			}
			if err != nil {
				t.Fatal(err)
			}
			p, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			renderRunning(&out, p)
			text := out.String()
			if state == "live" {
				for _, want := range []string{"loop: live", "session-live", "2026-11-13T07:00:30Z", "2026-11-13T07:00:59Z"} {
					if !strings.Contains(text, want) {
						t.Errorf("missing %q:\n%s", want, text)
					}
				}
				if p.Loop.Stale || p.Loop.StopReason != "" || strings.Contains(text, "queue-drained") || strings.Contains(text, "stale stop record") {
					t.Errorf("live session shows old stop: %+v\n%s", p.Loop, text)
				}
			} else if !p.Loop.Stale || !strings.Contains(text, "stale stop record") {
				t.Errorf("no live session lost stale stop: %+v\n%s", p.Loop, text)
			}
		})
	}
}

func TestRunningLeaseWithoutExpiryIsStillRecorded(t *testing.T) {
	h := newRunningHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE lanes SET leased_until = NULL WHERE lane_id = 'lane-1'`); err != nil {
		t.Fatal(err)
	}
	p, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	renderRunning(&out, p)
	if strings.Contains(out.String(), "lease not recorded") || !strings.Contains(out.String(), "lease has no expiry") {
		t.Fatalf("unbounded lease is mislabeled:\n%s", out.String())
	}
}

// TestRunningTablePrintsThreeBlocks pins the table's shape: the header names the lane
// lease and the stop reason, the live block lists the run, and the finished block carries
// its outcome, duration and cost.
func TestRunningTablePrintsThreeBlocks(t *testing.T) {
	h := newRunningHarness(t)
	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatalf("reading the running view: %v", err)
	}
	var out bytes.Buffer
	renderRunning(&out, payload)
	text := out.String()
	for _, want := range []string{
		"lanes leased: 1/2",
		"loop: queue-drained",
		"LIVE RUNS (1)",
		"lane-1",
		"item-live",
		"run-live",
		"1m05s",
		"1m55s left",
		"FINISHED (last 5)",
		"run-done",
		"landed landed-sha",
		"1s",
		"$0.25",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the table is missing %q:\n%s", want, text)
		}
	}
}

// TestRunningNamesTheIdleLanesWhenNothingIsLive is the stopped board's answer: no live
// runs is a sentence with the idle lanes in it, never an empty block.
func TestRunningNamesTheIdleLanesWhenNothingIsLive(t *testing.T) {
	h := newRunningHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	if _, err := db.Exec(`UPDATE lanes SET state = 'idle', run_id = NULL, leased_until = NULL`); err != nil {
		db.Close()
		t.Fatalf("releasing the lanes: %v", err)
	}
	db.Close()

	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatalf("reading the running view: %v", err)
	}
	if len(payload.Active) != 0 {
		t.Fatalf("active runs = %d, want none", len(payload.Active))
	}
	var out bytes.Buffer
	renderRunning(&out, payload)
	if !strings.Contains(out.String(), "0 live runs; lanes idle: lane-1, lane-2") {
		t.Fatalf("the stopped board does not name its idle lanes:\n%s", out.String())
	}
}

// TestRunningReadsAClassifiedOutcome pins the four outcomes the planner acts on. The
// negative control is in the same table: a run with a gate-fail verdict is not landed,
// and one whose item landed elsewhere is not credited with that landing.
func TestRunningReadsAClassifiedOutcome(t *testing.T) {
	exitZero := 0
	cases := []struct {
		name     string
		run      storeRun
		verdicts map[string]gateVerdict
		landed   map[string]storeAttempt
		want     string
	}{
		{
			name: "landed",
			run:  storeRun{RunID: "r1", State: "exited", ExitCode: &exitZero},
			verdicts: map[string]gateVerdict{
				"r1": {RunID: "r1", VerdictID: "v1", Result: "pass"},
			},
			landed: map[string]storeAttempt{
				"v1": {AttemptID: "a1", CandidateID: "c1", VerdictID: "v1", Result: "landed", TrunkAfter: "landed-sha"},
			},
			want: "landed landed-sha",
		},
		{
			name: "gate-failed",
			run:  storeRun{RunID: "r2", State: "exited", ExitCode: &exitZero},
			verdicts: map[string]gateVerdict{
				"r2": {RunID: "r2", VerdictID: "v2", Result: "fail"},
			},
			want: "gate-failed",
		},
		{
			name: "exited",
			run:  storeRun{RunID: "r3", State: "exited", ExitCode: &exitZero},
			want: "exited 0",
		},
		{
			name: "timed-out",
			run:  storeRun{RunID: "r4", State: "timed-out"},
			want: "timed-out",
		},
		{
			name: "a run whose item landed later is not credited with that landing",
			run:  storeRun{RunID: "r5", State: "exited", ExitCode: &exitZero},
			verdicts: map[string]gateVerdict{
				"r5": {RunID: "r5", VerdictID: "v5", Result: "pass"},
				"r6": {RunID: "r6", VerdictID: "v6", Result: "pass"},
			},
			landed: map[string]storeAttempt{
				"v6": {AttemptID: "a6", CandidateID: "c6", VerdictID: "v6", Result: "landed", TrunkAfter: "someone-else"},
			},
			want: "exited 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runOutcome(tc.run, tc.verdicts, tc.landed, nil)
			if got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunningWatchRendersUntilTheRenderStops is the watch loop without a clock: the render
// function answers, and the second answer stops the loop. A test that waited a real second
// would be measuring the machine, not the loop.
func TestRunningWatchRendersUntilTheRenderStops(t *testing.T) {
	renders := 0
	code := watchRunning(time.Millisecond, func() int {
		renders++
		if renders == 2 {
			return record.ExitUsage
		}
		return record.ExitOK
	})
	if code != record.ExitUsage {
		t.Fatalf("watch answered %d, want the render's %d", code, record.ExitUsage)
	}
	if renders != 2 {
		t.Fatalf("watch rendered %d times, want 2", renders)
	}
}

// TestRunningJSONEmitsTheRecordEnvelope pins the `--json` half of the face: the envelope is
// a view.running record whose payload is the same block the table draws.
func TestRunningJSONEmitsTheRecordEnvelope(t *testing.T) {
	h := newRunningHarness(t)
	env, code := runningFace([]string{
		"--store", h.store,
		"--loop-dir", h.loopDir,
		"--lane-root", h.laneRoot,
		"--now", h.now.Format(time.RFC3339),
		"--json",
	})
	if code != record.ExitOK {
		t.Fatalf("running --json exited %d, want 0", code)
	}
	if env.Kind != "view.running" {
		t.Fatalf("running --json emitted kind %q, want view.running", env.Kind)
	}
	var payload runningPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("the envelope payload is not the running view: %v", err)
	}
	if len(payload.Active) != 1 || payload.Active[0].RunID != "run-live" {
		t.Fatalf("the envelope's active block = %+v, want run-live", payload.Active)
	}
	if len(payload.Finished) != 1 || payload.Finished[0].RunID != "run-done" {
		t.Fatalf("the envelope's finished block = %+v, want run-done", payload.Finished)
	}
}

// TestRunningShowsAContinuedPausedRunAsLive: a paused run the loop continues as the same
// attempt keeps its paused `runs` row until the continuation finishes, and the run.resume
// event its continuation claims the lane with is newer than that row. The lease is the
// continuation's, so the run is live, and it must not be listed as a paused finished run.
// A lease held over a terminal row with no newer resume is a settling run, not a live one.
func TestRunningShowsAContinuedPausedRunAsLive(t *testing.T) {
	for _, resumed := range []bool{true, false} {
		t.Run(strconv.FormatBool(resumed), func(t *testing.T) {
			h := newRunningHarness(t)
			finish := h.addEvent(h.now.Add(-time.Minute), "run.finish", "run-live", `{}`)
			db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, event_seq)
				VALUES ('run-live', 'item-live', 'lane-1', 'pi', 'paused', NULL, 1000, 3000, ?)`, finish); err != nil {
				t.Fatal(err)
			}
			if resumed {
				h.addEvent(h.now.Add(-30*time.Second), "run.resume", "run-live", `{"run_id":"run-live","lane_id":"lane-1"}`)
			}
			p, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
			if err != nil {
				t.Fatal(err)
			}
			active := false
			for _, r := range p.Active {
				active = active || r.RunID == "run-live"
			}
			finished := false
			for _, r := range p.Finished {
				finished = finished || r.RunID == "run-live"
			}
			if active != resumed || finished == resumed {
				t.Fatalf("resumed=%v: active=%v finished=%v", resumed, active, finished)
			}
		})
	}
}
