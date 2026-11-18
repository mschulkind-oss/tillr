package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The live channel's suite. Every case asserts a frame count as well as a frame's
// contents: a socket that pushes one invalidation per change is the whole design, and a
// detector that answers on every poll would pass a contents-only assertion while
// refetching the screen four times a second.

// exec writes to the fixture store through its own connection, which is what a writer
// other than the serve face is. The fixture writes directly: this module reads a store,
// so it must not link a writer to have one to write to.
func (h *serveHarness) exec(query string, args ...any) sql.Result {
	h.t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		h.t.Fatalf("opening the store to write to it: %v", err)
	}
	defer db.Close()
	res, err := db.Exec(query, args...)
	if err != nil {
		h.t.Fatalf("writing %q to the store: %v", query, err)
	}
	return res
}

// appendEvent writes one event row. The detector reads object_type/object_id rather than
// the kind, so the kind names the utility that would have written it.
func (h *serveHarness) appendEvent(kind, objectType, objectID string) int64 {
	h.t.Helper()
	res := h.exec(
		`INSERT INTO events (ts, kind, object_type, object_id, revision, payload)
		 VALUES (?, ?, ?, ?, NULL, '{}')`,
		time.Now().UnixMilli(), kind, objectType, objectID)
	seq, err := res.LastInsertId()
	if err != nil {
		h.t.Fatalf("reading the appended event's seq: %v", err)
	}
	return seq
}

// pollOnce is one detector poll, with the "was there a frame" answer spelled out so a
// case reads as the assertion it is.
func pollOnce(t *testing.T, d *changeDetector) (invalidationFrame, bool) {
	t.Helper()
	frame, changed, err := d.poll(context.Background())
	if err != nil {
		t.Fatalf("polling the store: %v", err)
	}
	return frame, changed
}

// TestChangeDetectorEmitsOneFrameForOneEvent is the positive control: a store whose
// events.seq advances produces exactly one frame, naming the entities the events named
// and the snapshot the rest of the screen reads.
func TestChangeDetectorEmitsOneFrameForOneEvent(t *testing.T) {
	h := newServeHarness(t)
	d := newChangeDetector(h.srv)
	if _, changed := pollOnce(t, d); changed {
		t.Fatal("the first poll answered a frame: it has just read the baseline, so the client has seen everything in it")
	}

	h.appendEvent("item.record", "item", "item-seed-02")
	h.appendEvent("run.record", "run", "run-1")

	frame, changed := pollOnce(t, d)
	if !changed {
		t.Fatal("two appended events produced no invalidation: a client would never refetch")
	}
	if frame.Type != "snapshot.invalidated" {
		t.Fatalf("frame type is %q, not snapshot.invalidated", frame.Type)
	}
	if frame.EventHeadSeq != 2 {
		t.Fatalf("frame carries event_head_seq %d, not the head it read (2)", frame.EventHeadSeq)
	}
	want := []string{"items:item-seed-02", "runs:run-1", "snapshot"}
	if strings.Join(frame.Changed, ",") != strings.Join(want, ",") {
		t.Fatalf("frame names %v, not %v", frame.Changed, want)
	}

	// The negative control for the same store: the change was reported once, and a
	// second poll over an unchanged store says nothing. This is what keeps the socket an
	// invalidation channel rather than a 4Hz refetch loop.
	if _, changed := pollOnce(t, d); changed {
		t.Fatal("the poll after the change answered a second frame for the same event")
	}
}

// TestChangeDetectorNoticesACommitThatNamesNoEntity is the first of the three sources on
// its own: a commit that appends no event still moved the store, and PRAGMA data_version
// is the only signal that says so. The frame carries the unchanged head, which is what
// proves the frame came from data_version rather than from an event.
func TestChangeDetectorNoticesACommitThatNamesNoEntity(t *testing.T) {
	h := newServeHarness(t)
	d := newChangeDetector(h.srv)
	pollOnce(t, d) // the baseline

	h.exec(`UPDATE items SET title = 'renamed by a test' WHERE item_id = 'item-seed-02'`)

	frame, changed := pollOnce(t, d)
	if !changed {
		t.Fatal("a commit that appended no event produced no invalidation: the screen would show a stale title")
	}
	if strings.Join(frame.Changed, ",") != "snapshot" {
		t.Fatalf("frame names %v, not just the snapshot: no event named an entity", frame.Changed)
	}
	if frame.EventHeadSeq != 0 {
		t.Fatalf("frame reports event_head_seq %d, but no event was appended", frame.EventHeadSeq)
	}
}

// TestChangeDetectorEmitsNothingWhenNothingChanges is the negative control on its own: an
// idle store answers no frame, poll after poll.
func TestChangeDetectorEmitsNothingWhenNothingChanges(t *testing.T) {
	h := newServeHarness(t)
	d := newChangeDetector(h.srv)
	for i := 0; i < 3; i++ {
		if frame, changed := pollOnce(t, d); changed {
			t.Fatalf("poll %d answered %v over a store nothing wrote to", i+1, frame.Changed)
		}
	}
}

// TestChangeDetectorNamesAGrownRunLog is the third source: a running run's log grew, so
// the run's own payload is stale — and nothing else is. The name is the leaf and not the
// run's subtree on purpose: a log grows once per line, and refetching a run's exchanges,
// timeline and artifacts that often is what the leaf name exists to stop.
func TestChangeDetectorNamesAGrownRunLog(t *testing.T) {
	h := newServeHarness(t)
	logPath := filepath.Join(h.laneRoot, "lane-a", "run-2.log")
	if err := os.WriteFile(logPath, []byte("first line\n"), 0o644); err != nil {
		t.Fatalf("writing the run log: %v", err)
	}
	d := newChangeDetector(h.srv)
	if _, changed := pollOnce(t, d); changed {
		t.Fatal("the first poll answered a frame: the baseline it recorded is the log's current size")
	}

	if err := os.WriteFile(logPath, []byte("first line\nsecond line\n"), 0o644); err != nil {
		t.Fatalf("growing the run log: %v", err)
	}
	frame, changed := pollOnce(t, d)
	if !changed {
		t.Fatal("a log that grew produced no invalidation: the run's timeline would freeze while it talks")
	}
	if strings.Join(frame.Changed, ",") != "runlog:run-2" {
		t.Fatalf("frame names %v, not just runlog:run-2: a log line is not a board change, and it is not the whole run subtree either", frame.Changed)
	}

	if _, changed := pollOnce(t, d); changed {
		t.Fatal("the poll after the growth answered a second frame for the same bytes")
	}
}

// TestWebSocketIsSilentOverAnUnchangedStore is the wire's negative control: the socket
// primes its baseline before it can answer anything, so an idle store pushes nothing.
// It is its own case because a read deadline that expires leaves a websocket unusable —
// the negative has to be observed on a connection the positive does not share.
func TestWebSocketIsSilentOverAnUnchangedStore(t *testing.T) {
	h := newServeHarness(t)
	conn := h.dial(t)
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(3 * wsPollInterval)); err != nil {
		t.Fatalf("setting the read deadline: %v", err)
	}
	if _, raw, err := conn.ReadMessage(); err == nil {
		t.Fatalf("the socket pushed a frame over an unchanged store: %s", raw)
	}
}

// TestWebSocketPushesOneInvalidationPerChange drives the route over the wire: the
// handshake, then one frame naming what the change touched.
func TestWebSocketPushesOneInvalidationPerChange(t *testing.T) {
	h := newServeHarness(t)
	conn := h.dial(t)
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(hangGuard)); err != nil {
		t.Fatalf("setting the read deadline: %v", err)
	}
	h.appendEvent("item.record", "item", "item-seed-02")
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("reading the invalidation: %v", err)
	}
	var frame struct {
		Type         string   `json:"type"`
		EventHeadSeq int64    `json:"event_head_seq"`
		Changed      []string `json:"changed"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("the frame is not the JSON this route documents (%v): %s", err, raw)
	}
	if frame.Type != "snapshot.invalidated" || frame.EventHeadSeq != 1 {
		t.Fatalf("frame is %s at seq %d, not snapshot.invalidated at seq 1", frame.Type, frame.EventHeadSeq)
	}
	if strings.Join(frame.Changed, ",") != "items:item-seed-02,snapshot" {
		t.Fatalf("frame names %v, not [items:item-seed-02 snapshot]", frame.Changed)
	}
}

// TestWebSocketClosesWithAReasonWhenTheStoreCannotAnswer is the failure the design
// refuses: a store this process cannot read must end the socket with the reason, not take
// the server down and not leave the client guessing.
func TestWebSocketClosesWithAReasonWhenTheStoreCannotAnswer(t *testing.T) {
	h := newServeHarness(t)
	// Closing the read-only handle is the state a locked, vanished or half-migrated store
	// leaves a reader in: the route set above still answers, and the socket must not.
	if err := h.srv.store.Close(); err != nil {
		t.Fatalf("closing the store handle: %v", err)
	}
	conn := h.dial(t)
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(hangGuard)); err != nil {
		t.Fatalf("setting the read deadline: %v", err)
	}
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("reading a socket over an unreadable store answered %v, not a close frame", err)
	}
	if closeErr.Code != websocket.CloseInternalServerErr {
		t.Fatalf("the socket closed with code %d, not %d", closeErr.Code, websocket.CloseInternalServerErr)
	}
	if !strings.Contains(closeErr.Text, "store") {
		t.Fatalf("the close reason %q does not name the store as the cause", closeErr.Text)
	}
}

// TestWebSocketReleasesItsGoroutinesOnClose is the leak this shape invites: a reader
// goroutine, a poll loop and a ticker per connection. The client's own read proves the
// loop is running before the close, so a goroutine that outlives the connection is the
// failure — and the count only has to come back to where it started, never to zero, so a
// runtime that starts goroutines of its own is not this case's business.
func TestWebSocketReleasesItsGoroutinesOnClose(t *testing.T) {
	h := newServeHarness(t)
	base := settledGoroutines(t)
	for i := 0; i < 5; i++ {
		conn := h.dial(t)
		if err := conn.SetReadDeadline(time.Now().Add(hangGuard)); err != nil {
			t.Fatalf("setting the read deadline: %v", err)
		}
		// A frame is what proves the poll loop and its reader are up: a count sampled
		// against a socket that never started its goroutines cannot show the leak.
		h.appendEvent("item.record", "item", "item-seed-02")
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("reading a frame on connection %d: %v", i+1, err)
		}
		conn.Close()
	}
	if got := goroutinesBackTo(t, base); got > base {
		t.Fatalf("5 closed sockets left %d goroutines behind (%d at the start): a reader or a poll loop outlived its connection", got-base, base)
	}
}

// settledGoroutines answers the runtime's goroutine count once it has stopped moving, so
// a goroutine still on its way out from an earlier case is not this case's baseline. A
// baseline sampled a little high only makes the leak check more lenient, never stricter, so
// load cannot fail it.
func settledGoroutines(t *testing.T) int {
	t.Helper()
	prev := runtime.NumGoroutine()
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		now := runtime.NumGoroutine()
		if now == prev {
			return now
		}
		prev = now
	}
	t.Fatalf("the goroutine count never settled: %d", prev)
	return prev
}

// goroutinesBackTo waits until the count is at most want and answers the count it last saw.
// A goroutine that outlives its connection keeps the count above want however long this
// waits, so only a real leak reaches the hang guard; a slow exit on a loaded machine does not.
func goroutinesBackTo(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for {
		got := runtime.NumGoroutine()
		if got <= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dial opens one socket against the fixture's server, with the Origin header a browser
// sends: the origin check is part of the route, so the test must not skip it.
func (h *serveHarness) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(h.http.URL, "http") + "/api/v1/ws"
	header := http.Header{"Origin": []string{"http://127.0.0.1:4318"}}
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dialing %s: %v (status %d)", url, err, status)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// TestWebSocketOriginIsTheOperatorsTerminal pins the origin check: the front end's own
// origin and the Vite dev proxy's loopback port are the same operator, and a page from
// anywhere else is refused.
func TestWebSocketOriginIsTheOperatorsTerminal(t *testing.T) {
	cases := []struct {
		origin string
		allow  bool
	}{
		{"", true},                               // a non-browser client
		{"http://127.0.0.1:4318", true},          // the served front end
		{"http://localhost:5173", true},          // the Vite dev proxy
		{"http://[::1]:4318", true},              // the same terminal over v6 loopback
		{"https://evil.example", false},          // a page from the network
		{"http://127.0.0.1.evil.example", false}, // a host that merely starts like loopback
	}
	for _, c := range cases {
		req, err := http.NewRequest("GET", "http://127.0.0.1:4318/api/v1/ws", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if got := wsOriginAllowed(req); got != c.allow {
			t.Fatalf("origin %q allowed=%v, want %v", c.origin, got, c.allow)
		}
	}
}
