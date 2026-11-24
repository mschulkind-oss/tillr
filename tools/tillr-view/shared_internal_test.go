package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// sharedRoutes are the routes whose answers are built once per store change.
var sharedRoutes = []struct {
	path   string
	builds func(*server) *sharedAnswers
}{
	{"/api/v1/snapshot", func(s *server) *sharedAnswers { return &s.snapshotShare }},
	{"/api/v1/lanes", func(s *server) *sharedAnswers { return &s.lanesShare }},
	{"/api/v1/lanes?history=lane-a", func(s *server) *sharedAnswers { return &s.lanesShare }},
	{"/api/v1/passes", func(s *server) *sharedAnswers { return &s.passesShare }},
}

func appendTestEvent(t *testing.T, h *serveHarness) {
	t.Helper()
	db, err := store.Open(context.Background(), h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := event.Register("test.shared", "view-test"); err != nil {
		t.Fatal(err)
	}
	id := "item-seed-02"
	if err := db.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: "test.shared", ObjectType: "item", ObjectID: &id, Payload: json.RawMessage(`{}`), At: time.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func dataOf(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unreadable response: %v\n%s", err, raw)
	}
	return string(body.Data)
}

func TestSharedAnswersBuildOncePerStoreChange(t *testing.T) {
	for _, route := range sharedRoutes {
		t.Run(route.path, func(t *testing.T) {
			h := newServeHarness(t)
			share := route.builds(h.srv)
			const clients = 12
			answers := make([]string, clients)
			var wg sync.WaitGroup
			for i := 0; i < clients; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					status, raw := h.getRaw(route.path)
					if status != http.StatusOK {
						t.Errorf("%s answered %d: %s", route.path, status, raw)
					}
					answers[i] = dataOf(t, raw)
				}()
			}
			wg.Wait()
			if n := share.builds.Load(); n != 1 {
				t.Fatalf("%d concurrent requests at one store state ran the builder %d times, want 1", clients, n)
			}
			for i := range answers {
				if answers[i] != answers[0] {
					t.Fatalf("request %d got a different answer from request 0", i)
				}
			}

			// Byte-identical to the unshared path: a second server on the same store has
			// shared nothing, so its first answer is a plain build.
			other := newServer(context.Background(), h.store, h.loopDir, h.laneRoot, h.traceBin, h.dir, "", []string{"false"})
			defer other.close()
			ts := httptest.NewServer(other.routes())
			defer ts.Close()
			resp, err := http.Get(ts.URL + route.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var buf [1 << 20]byte
			var fresh []byte
			for {
				n, err := resp.Body.Read(buf[:])
				fresh = append(fresh, buf[:n]...)
				if err != nil {
					break
				}
			}
			if got := dataOf(t, fresh); got != answers[0] {
				t.Fatalf("the shared answer differs from the unshared build:\nshared: %.400s\nfresh:  %.400s", answers[0], got)
			}

			// A new event makes the next request rebuild, and only the next one.
			appendTestEvent(t, h)
			before := share.builds.Load()
			h.getRaw(route.path)
			h.getRaw(route.path)
			if n := share.builds.Load() - before; n != 1 {
				t.Fatalf("after a new event two requests ran the builder %d times, want 1", n)
			}
		})
	}
}

// A lane log that grows with no store write is an input of the lanes answer: its file
// overlay is rebuilt, an unchanged log is not, and the store-derived rows are not re-read.
func TestSharedLanesAnswerFollowsItsLogFiles(t *testing.T) {
	h := newServeHarness(t)
	share := &h.srv.lanesShare
	storeShare := &h.srv.lanesStoreShare
	log := filepath.Join(h.laneRoot, "lane-a")
	if err := os.MkdirAll(log, 0o755); err != nil {
		t.Fatal(err)
	}
	path := runLogPath(h.laneRoot, "lane-a", "run-2")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.getRaw("/api/v1/lanes")
	h.getRaw("/api/v1/lanes")
	if n := share.builds.Load(); n != 1 {
		t.Fatalf("an unchanged log rebuilt the answer: %d builds", n)
	}
	if n := storeShare.builds.Load(); n != 1 {
		t.Fatalf("lanesStoreShare builds = %d, want 1", n)
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"tool_execution_start\",\"toolName\":\"bash\",\"args\":{\"command\":\"ls\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, grown := h.getRaw("/api/v1/lanes")
	if n := share.builds.Load(); n != 2 {
		t.Fatalf("a changed log did not rebuild the answer: %d builds", n)
	}
	if n := storeShare.builds.Load(); n != 1 {
		t.Fatalf("a growing log rebuilt the SQLite store rows (%d builds, want 1)", n)
	}
	if !bytes.Contains(grown, []byte("bash: ls")) {
		t.Fatalf("grown log action missing from lanes answer: %s", grown)
	}
}

// A request that arrived before a build began is owed that build even if the store moved
// while it ran; a request that arrived after the build began and finds a moved store is not.
func TestSharedAnswerIsOwedToRequestsThatArrivedBeforeTheBuild(t *testing.T) {
	h := newServeHarness(t)
	var shared sharedAnswers
	call := func(arrived int64) {
		t.Helper()
		ctx := context.WithValue(context.Background(), arrivalKey{}, arrived)
		err := h.srv.store.ReadTx(ctx, func(r *store.Reader) error {
			shared.get(ctx, r, "k", nil, func() shareBuild { return shareBuild{value: 1} })
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	const late = int64(1) << 40
	call(late)
	appendTestEvent(t, h) // the store moves while later requests queue
	call(0)               // arrived before the build began
	if n := shared.builds.Load(); n != 1 {
		t.Fatalf("a request that arrived before the build rebuilt: %d builds", n)
	}
	call(late) // arrived after the build began, and the store has moved
	if n := shared.builds.Load(); n != 2 {
		t.Fatalf("a later request on a moved store reused the old answer: %d builds", n)
	}
}

// Non-event commits (such as the 1s drain_sessions heartbeat) move SQLite's data_version
// without moving the event head: neither snapshot nor lanes rebuilds its store payload,
// while the live session's refreshed heartbeat clock still reaches the snapshot answer.
func TestSnapshotAndLanesDoNotRebuildOnHeartbeatCommits(t *testing.T) {
	h := newServeHarness(t)
	at := time.Date(2026, 11, 23, 5, 50, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()
	h.exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER)`)
	h.exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at)
		VALUES ('live', ?, ?, ?)`, now-30000, now, now+60000)

	status, _, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("first snapshot: %d %s", status, raw)
	}
	status, _, raw = h.get("/api/v1/lanes")
	if status != http.StatusOK {
		t.Fatalf("first lanes: %d %s", status, raw)
	}
	status, _, raw = h.get("/api/v1/passes")
	if status != http.StatusOK {
		t.Fatalf("first passes: %d %s", status, raw)
	}
	if n := h.srv.snapshotShare.builds.Load(); n != 1 {
		t.Fatalf("snapshot builds after first poll = %d, want 1", n)
	}
	if n := h.srv.lanesShare.builds.Load(); n != 1 {
		t.Fatalf("lanes builds after first poll = %d, want 1", n)
	}
	if n := h.srv.passesShare.builds.Load(); n != 1 {
		t.Fatalf("passes builds after first poll = %d, want 1", n)
	}

	// Simulate the drain's 1-second session heartbeat: data_version advances, event head does not.
	later := at.Add(time.Second)
	h.setNow(later)
	h.exec(`UPDATE drain_sessions SET refreshed_at = ?, next_pass_at = ? WHERE session_id = 'live'`,
		later.UnixMilli(), later.Add(time.Minute).UnixMilli())

	status, env, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("second snapshot: %d %s", status, raw)
	}
	if n := h.srv.snapshotShare.builds.Load(); n != 1 {
		t.Fatalf("heartbeat commit rebuilt snapshot (%d builds, want 1)", n)
	}
	session := env["data"].(map[string]any)["drain_summary"].(map[string]any)["session"].(map[string]any)
	if got := session["refreshed_at_ms"]; got != float64(later.UnixMilli()) {
		t.Fatalf("refreshed_at_ms = %v, want %d", got, later.UnixMilli())
	}

	status, _, raw = h.get("/api/v1/lanes")
	if status != http.StatusOK {
		t.Fatalf("second lanes: %d %s", status, raw)
	}
	if n := h.srv.lanesShare.builds.Load(); n != 1 {
		t.Fatalf("heartbeat commit rebuilt lanes (%d builds, want 1)", n)
	}
	status, _, raw = h.get("/api/v1/passes")
	if status != http.StatusOK {
		t.Fatalf("second passes: %d %s", status, raw)
	}
	if n := h.srv.passesShare.builds.Load(); n != 1 {
		t.Fatalf("heartbeat commit rebuilt passes (%d builds, want 1)", n)
	}
}

// Repeated GET /api/v1/snapshot polls with no store change (and across non-event commits)
// perform the payload build once, recorded in the performance log's payload_ms, and strip
// pricing identically on both built and cached answers.
func TestSnapshotRepeatedPollPerfLogPayloadMSAndPricing(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`UPDATE items SET title = 'item costing $12.34' WHERE item_id = 'item-seed-02'`)
	h.exec(`UPDATE runs SET usage_cost_usd = 4.50 WHERE run_id = 'run-1'`)

	perfDir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)}
	perf := openPerfLog(perfDir, "rev-test", clock.now)
	h.srv.perf = perf
	h.srv.snapshotShare.onBuild = func() {
		clock.advance(500 * time.Millisecond)
	}
	inst := httptest.NewServer(h.srv.routes())
	defer inst.Close()

	fetchSnap := func() string {
		t.Helper()
		resp, err := http.Get(inst.URL + "/api/v1/snapshot")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("snapshot status %d: %s", resp.StatusCode, buf.String())
		}
		body := buf.String()
		if strings.Contains(body, "$12.34") || strings.Contains(strings.ToLower(body), "cost_usd") {
			t.Fatalf("pricing leaked in snapshot answer: %s", body)
		}
		if !strings.Contains(body, "[price omitted]") {
			t.Fatalf("expected [price omitted] in snapshot answer: %s", body)
		}
		return dataOf(t, buf.Bytes())
	}

	first := fetchSnap()
	second := fetchSnap()
	if first != second {
		t.Fatalf("cached snapshot data differed from initial build")
	}
	appendTestEvent(t, h)
	_ = fetchSnap()
	perf.close()

	lines := readPerfLines(t, filepath.Join(perfDir, "server-20261123.jsonl"))
	var payloadMS []float64
	for _, line := range lines {
		if line["kind"] == "request" && line["route"] == "GET /api/v1/snapshot" {
			phases := line["phases"].(map[string]any)
			payloadMS = append(payloadMS, phases["payload_ms"].(float64))
		}
	}
	want := []float64{500, 0, 500}
	if len(payloadMS) != len(want) {
		t.Fatalf("logged snapshot requests = %v, want %v", payloadMS, want)
	}
	for i := range want {
		if payloadMS[i] != want[i] {
			t.Fatalf("request %d payload_ms = %v, want %v (all: %v)", i, payloadMS[i], want[i], payloadMS)
		}
	}
}
