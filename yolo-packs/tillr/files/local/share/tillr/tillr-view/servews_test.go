package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/record"
)

// The live channel's suite. Every case asserts a frame count as well as a frame's
// contents: a socket that pushes one invalidation per change is the whole design, and a
// detector that answers on every poll would pass a contents-only assertion while
// refetching the screen repeatedly.

// execRaw writes to the fixture store through its own connection without emitting a
// local notification frame, simulating a dropped wake hint or an unnotified commit.
func (h *serveHarness) execRaw(query string, args ...any) sql.Result {
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

// exec writes to the fixture store through its own connection and publishes a post-commit
// wake hint when a relay socket is present, matching store.WriteTx.
func (h *serveHarness) exec(query string, args ...any) sql.Result {
	h.t.Helper()
	res := h.execRaw(query, args...)
	_ = notify.Publish(h.store, notify.Frame{Topic: notify.TopicQueueChanged})
	return res
}

// appendEventRaw writes one event row without publishing a notification frame.
func (h *serveHarness) appendEventRaw(kind, objectType, objectID string) int64 {
	h.t.Helper()
	res := h.execRaw(
		`INSERT INTO events (ts, kind, object_type, object_id, revision, payload)
		 VALUES (?, ?, ?, ?, NULL, '{}')`,
		time.Now().UnixMilli(), kind, objectType, objectID)
	seq, err := res.LastInsertId()
	if err != nil {
		h.t.Fatalf("reading the appended event's seq: %v", err)
	}
	return seq
}

// startRelay starts an in-process notification relay for h.store and returns an
// idempotent stop function. Cleanup closes the relay and removes its lock file.
func (h *serveHarness) startRelay(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- notify.Serve(ctx, notify.ServeConfig{
			StorePath: h.store,
			IdleExit:  hangGuard,
			Ready:     ready,
		})
	}()
	var sockPath string
	select {
	case sockPath = <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("starting notify relay: %v", err)
	case <-time.After(hangGuard):
		cancel()
		t.Fatal("timed out waiting for notify relay ready")
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
			_ = os.Remove(notify.LockPath(sockPath))
		})
	}
	t.Cleanup(stop)
	return stop
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
// primes its baseline before it can answer anything, so neither relayed wake hints nor
// fallback reconciliation ticks over an unchanged store push a frame.
func TestWebSocketIsSilentOverAnUnchangedStore(t *testing.T) {
	h := newServeHarness(t)
	h.startRelay(t)
	h.srv.fallbackPoll = 50 * time.Millisecond
	conn := h.dial(t)
	defer conn.Close()
	_ = notify.Publish(h.store, notify.Frame{Topic: notify.TopicQueueChanged})
	if err := conn.SetReadDeadline(time.Now().Add(3 * h.srv.fallbackPoll)); err != nil {
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
	h.startRelay(t)
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
// goroutine, a relay subscriber goroutine, a poll loop and a ticker per connection. The
// client's own read proves the loop is running before the close, so a goroutine that
// outlives the connection is the failure — and the count only has to come back to where
// it started, never to zero, so a runtime that starts goroutines of its own is not this
// case's business.
func TestWebSocketReleasesItsGoroutinesOnClose(t *testing.T) {
	h := newServeHarness(t)
	h.startRelay(t)
	base := settledGoroutines(t)
	for i := 0; i < 5; i++ {
		conn := h.dial(t)
		if err := conn.SetReadDeadline(time.Now().Add(hangGuard)); err != nil {
			t.Fatalf("setting the read deadline: %v", err)
		}
		// A frame is what proves the poll loop, subscriber and reader are up: a count
		// sampled against a socket that never started its goroutines cannot show the leak.
		h.appendEvent("item.record", "item", "item-seed-02")
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("reading a frame on connection %d: %v", i+1, err)
		}
		conn.Close()
	}
	if got := goroutinesBackTo(t, base); got > base {
		t.Fatalf("5 closed sockets left %d goroutines behind (%d at the start): a reader, subscriber or poll loop outlived its connection", got-base, base)
	}
	if got := h.srv.wsSubscribed.Load(); got != 0 {
		t.Fatalf("wsSubscribed = %d after closing all sockets, want 0", got)
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

type fakeWSTicker struct {
	mu      sync.Mutex
	ch      chan time.Time
	history []time.Duration
	stopped bool
}

func newFakeWSTicker() *fakeWSTicker {
	return &fakeWSTicker{ch: make(chan time.Time, 4)}
}

func (f *fakeWSTicker) factory(d time.Duration) wsTicker {
	f.mu.Lock()
	f.history = append(f.history, d)
	f.mu.Unlock()
	return f
}

func (f *fakeWSTicker) Chan() <-chan time.Time { return f.ch }

func (f *fakeWSTicker) Reset(d time.Duration) {
	f.mu.Lock()
	f.history = append(f.history, d)
	f.mu.Unlock()
}

func (f *fakeWSTicker) Stop() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
}

func (f *fakeWSTicker) tick() {
	f.ch <- time.Now()
}

func (f *fakeWSTicker) intervals() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.history...)
}

func waitForCondition(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func readInvalidation(t *testing.T, conn *websocket.Conn, timeout time.Duration) invalidationFrame {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("setting read deadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("reading invalidation frame: %v", err)
	}
	var frame invalidationFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("decoding invalidation frame %s: %v", raw, err)
	}
	return frame
}

// TestWebSocketSubscribedRetires250msSteadyPollAndWakesOnRelayedFrame pins Criterion 5
// and D-SN6: while subscribed to the notification relay, the 250ms steady poll is gone
// (store reads wait for a relayed wake hint or the 15s fallback), and any relayed frame
// (including resync or a publish through a symlinked store path) immediately wakes the
// store read.
func TestWebSocketSubscribedRetires250msSteadyPollAndWakesOnRelayedFrame(t *testing.T) {
	if wsFallbackPollInterval != 15*time.Second {
		t.Fatalf("wsFallbackPollInterval = %v, want 15s", wsFallbackPollInterval)
	}
	if wsDisconnectedPollInterval != time.Second {
		t.Fatalf("wsDisconnectedPollInterval = %v, want 1s", wsDisconnectedPollInterval)
	}

	h := newServeHarness(t)
	h.startRelay(t)

	// Negative wire + poll-count proof on the real ticker: while subscribed, an unnotified
	// commit makes zero store polls and pushes zero frames across 650ms (> 2.5x the retired
	// 250ms steady poll), because steady polling is gone until the 15-second fallback.
	silentConn := h.dial(t)
	waitForCondition(t, "initial post-handshake poll and subscription", func() bool {
		return h.srv.wsSubscribed.Load() == 1 && h.srv.wsPolls.Load() >= 1
	})
	pollsAtRest := h.srv.wsPolls.Load()
	h.appendEventRaw("item.record", "item", "item-seed-02")
	if err := silentConn.SetReadDeadline(time.Now().Add(650 * time.Millisecond)); err != nil {
		t.Fatalf("setting read deadline: %v", err)
	}
	if _, raw, err := silentConn.ReadMessage(); err == nil {
		t.Fatalf("subscribed socket pushed %s without a relayed frame before the 15s fallback: the 250ms steady poll is still running", raw)
	}
	if got := h.srv.wsPolls.Load(); got != pollsAtRest {
		t.Fatalf("subscribed socket ran %d extra store polls while idle (got %d, want %d)", got-pollsAtRest, got, pollsAtRest)
	}
	_ = silentConn.Close()
	waitForCondition(t, "silentConn unsubscribe", func() bool {
		return h.srv.wsSubscribed.Load() == 0
	})

	// Positive wire proof: on a subscribed socket, a relayed frame (including one published
	// through a symlink to state.db and a resync frame) wakes the store read immediately.
	linkedStore := filepath.Join(t.TempDir(), "linked-state.db")
	if err := os.Symlink(h.store, linkedStore); err != nil {
		t.Fatalf("creating symlinked store path: %v", err)
	}
	liveConn := h.dial(t)
	defer liveConn.Close()
	waitForCondition(t, "liveConn initial poll and subscription", func() bool {
		return h.srv.wsSubscribed.Load() == 1 && h.srv.wsPolls.Load() > pollsAtRest
	})

	seq1 := h.appendEventRaw("item.record", "item", "item-seed-04")
	if err := notify.Publish(linkedStore, notify.Frame{Topic: notify.TopicQueueChanged, Seq: seq1}); err != nil {
		t.Fatalf("publishing queue.changed notification via symlink: %v", err)
	}
	frame1 := readInvalidation(t, liveConn, hangGuard)
	if frame1.EventHeadSeq != seq1 || strings.Join(frame1.Changed, ",") != "items:item-seed-04,snapshot" {
		t.Fatalf("frame1 = %+v, want seq=%d changed=[items:item-seed-04 snapshot]", frame1, seq1)
	}

	seq2 := h.appendEventRaw("item.record", "item", "item-seed-05")
	if err := notify.Publish(h.store, notify.Frame{Topic: notify.TopicResync}); err != nil {
		t.Fatalf("publishing resync notification: %v", err)
	}
	frame2 := readInvalidation(t, liveConn, hangGuard)
	if frame2.EventHeadSeq != seq2 || strings.Join(frame2.Changed, ",") != "items:item-seed-05,snapshot" {
		t.Fatalf("frame2 = %+v, want seq=%d changed=[items:item-seed-05 snapshot]", frame2, seq2)
	}
}

// TestWebSocketReconcilesMissedNotificationOnFallbackPoll verifies Section 4.3 / D-SN6:
// while subscribed, streamInvalidations arms a 15-second fallback ticker and reconciles
// any commit whose wake hint was dropped on the fallback tick.
func TestWebSocketReconcilesMissedNotificationOnFallbackPoll(t *testing.T) {
	h := newServeHarness(t)
	h.startRelay(t)
	ft := newFakeWSTicker()
	h.srv.newWSTicker = ft.factory

	conn := h.dial(t)
	defer conn.Close()
	waitForCondition(t, "initial poll and subscription", func() bool {
		return h.srv.wsSubscribed.Load() == 1 && h.srv.wsPolls.Load() >= 1 && len(ft.intervals()) >= 1
	})
	if got := ft.intervals(); !reflect.DeepEqual(got, []time.Duration{15 * time.Second}) {
		t.Fatalf("subscribed ticker intervals = %v, want [15s]", got)
	}

	pollsBefore := h.srv.wsPolls.Load()
	seq := h.appendEventRaw("item.record", "item", "item-seed-02")
	if got := h.srv.wsPolls.Load(); got != pollsBefore {
		t.Fatalf("wsPolls advanced before fallback tick: got %d, want %d", got, pollsBefore)
	}

	ft.tick()
	frame := readInvalidation(t, conn, hangGuard)
	if frame.EventHeadSeq != seq || strings.Join(frame.Changed, ",") != "items:item-seed-02,snapshot" {
		t.Fatalf("fallback frame = %+v, want seq=%d changed=[items:item-seed-02 snapshot]", frame, seq)
	}
}

// TestWebSocketDisconnectedPollsEverySecondAndResyncsImmediatelyOnReconnect verifies both
// disconnect and reconnect rules in Sections 4.2 and 4.3:
//  1. While disconnected from the relay, tillr-view configures a 1s poll (not 15s) and
//     reads the store on its ticks.
//  2. Connecting to the relay switches the ticker to 15s; losing the relay reverts to 1s;
//     and reconnecting switches back to 15s while immediately reconciling changes committed
//     during the disconnect window without waiting for any ticker tick.
func TestWebSocketDisconnectedPollsEverySecondAndResyncsImmediatelyOnReconnect(t *testing.T) {
	h := newServeHarness(t)
	ft := newFakeWSTicker()
	h.srv.newWSTicker = ft.factory
	h.srv.reconnectInitial = 20 * time.Millisecond
	h.srv.reconnectMax = 80 * time.Millisecond

	conn := h.dial(t)
	defer conn.Close()
	waitForCondition(t, "initial poll while disconnected", func() bool {
		return h.srv.wsPolls.Load() >= 1 && len(ft.intervals()) >= 1
	})
	if got := h.srv.wsSubscribed.Load(); got != 0 {
		t.Fatalf("wsSubscribed = %d with no relay running, want 0", got)
	}
	if got := ft.intervals(); !reflect.DeepEqual(got, []time.Duration{time.Second}) {
		t.Fatalf("initial disconnected ticker intervals = %v, want [1s]", got)
	}

	// Disconnected poll tick delivers unnotified commit.
	seq1 := h.appendEventRaw("item.record", "item", "item-seed-02")
	ft.tick()
	frame1 := readInvalidation(t, conn, hangGuard)
	if frame1.EventHeadSeq != seq1 {
		t.Fatalf("disconnected poll frame seq = %d, want %d", frame1.EventHeadSeq, seq1)
	}

	// Relay 1 comes up -> subscriber connects and resets ticker from 1s to 15s.
	stopRelay1 := h.startRelay(t)
	waitForCondition(t, "subscription to relay 1 and 15s ticker reset", func() bool {
		return h.srv.wsSubscribed.Load() == 1 &&
			reflect.DeepEqual(ft.intervals(), []time.Duration{time.Second, 15 * time.Second})
	})

	// Relay 1 stops -> subscriber detects disconnect and reverts ticker to 1s.
	stopRelay1()
	waitForCondition(t, "disconnect detection and 1s ticker reset", func() bool {
		return h.srv.wsSubscribed.Load() == 0 &&
			reflect.DeepEqual(ft.intervals(), []time.Duration{time.Second, 15 * time.Second, time.Second})
	})

	// Commit while disconnected, keep fake ticker frozen, and bring Relay 2 up:
	// reconnect must reconcile immediately and deliver seq2 without any timer tick.
	seq2 := h.appendEventRaw("item.record", "item", "item-seed-04")
	h.startRelay(t)
	frame2 := readInvalidation(t, conn, hangGuard)
	if frame2.EventHeadSeq != seq2 || strings.Join(frame2.Changed, ",") != "items:item-seed-04,snapshot" {
		t.Fatalf("reconnect resync frame = %+v, want seq=%d changed=[items:item-seed-04 snapshot]", frame2, seq2)
	}
	waitForCondition(t, "reconnect subscription and 15s ticker reset", func() bool {
		return h.srv.wsSubscribed.Load() == 1 &&
			reflect.DeepEqual(ft.intervals(), []time.Duration{time.Second, 15 * time.Second, time.Second, 15 * time.Second})
	})
}

// TestWebSocketNoNotifyAndParity verifies D-SN5, D-SN6, and D-SN7:
//  1. serveFace refuses non-positive --fallback-poll values.
//  2. --no-notify skips relay subscription even when a relay is running, configures the
//     fallback interval (not the 1s disconnected interval), and produces identical
//     invalidation frames on fallback ticks as a live-notified socket.
func TestWebSocketNoNotifyAndParity(t *testing.T) {
	checkHarness := newServeHarness(t)
	for _, bad := range []string{"0s", "-1s"} {
		if _, code := serveFace([]string{"--store", checkHarness.store, "--fallback-poll", bad, "--check"}); code == record.ExitOK {
			t.Fatalf("serveFace accepted --fallback-poll %s; want non-zero usage exit", bad)
		}
	}

	runScenario := func(t *testing.T, noNotify bool) []invalidationFrame {
		t.Helper()
		h := newServeHarness(t)
		h.startRelay(t)
		ft := newFakeWSTicker()
		h.srv.newWSTicker = ft.factory
		h.srv.noNotify = noNotify
		h.srv.autoSpawnNotify = !noNotify

		conn := h.dial(t)
		defer conn.Close()
		waitForCondition(t, "initial post-handshake poll", func() bool {
			if noNotify {
				return h.srv.wsPolls.Load() >= 1 && len(ft.intervals()) >= 1
			}
			return h.srv.wsSubscribed.Load() == 1 && h.srv.wsPolls.Load() >= 1 && len(ft.intervals()) >= 1
		})
		if noNotify && h.srv.wsSubscribed.Load() != 0 {
			t.Fatalf("wsSubscribed = %d under --no-notify, want 0", h.srv.wsSubscribed.Load())
		}
		if got := ft.intervals(); !reflect.DeepEqual(got, []time.Duration{15 * time.Second}) {
			t.Fatalf("ticker intervals (noNotify=%v) = %v, want [15s]", noNotify, got)
		}

		var got []invalidationFrame
		advance := func() {
			if noNotify {
				ft.tick()
			}
			got = append(got, readInvalidation(t, conn, hangGuard))
		}

		h.appendEvent("item.record", "item", "item-seed-02")
		advance()

		h.exec(`UPDATE items SET title = 'parity-title' WHERE item_id = 'item-seed-02'`)
		advance()

		logPath := filepath.Join(h.laneRoot, "lane-a", "run-2.log")
		if err := os.WriteFile(logPath, []byte("grown log line\n"), 0o644); err != nil {
			t.Fatalf("writing run log: %v", err)
		}
		if !noNotify {
			_ = notify.Publish(h.store, notify.Frame{Topic: notify.TopicResync})
		}
		advance()
		return got
	}

	withNotify := runScenario(t, false)
	withoutNotify := runScenario(t, true)
	if !reflect.DeepEqual(withNotify, withoutNotify) {
		t.Fatalf("invalidation parity mismatch:\nwith notify:    %+v\nwithout notify: %+v", withNotify, withoutNotify)
	}
}

// TestWebSocketAutoSpawnsDetachedRelayOnENOENTAndStaleSocket verifies Section 4.2's
// on-demand relay startup: when `tillr view serve` sees ENOENT (no socket) or
// ECONNREFUSED (stale socket file), it acquires `<sock>.lock`, spawns a detached
// `tillr notify serve`, subscribes, and delivers wake hints immediately.
func TestWebSocketAutoSpawnsDetachedRelayOnENOENTAndStaleSocket(t *testing.T) {
	tillrBin := filepath.Join(t.TempDir(), "tillr")
	buildCmd := exec.Command("go", "-C", "../tillr-cli", "build", "-o", tillrBin, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("building tillr-cli for auto-spawn test: %v\n%s", err, out)
	}

	runCase := func(t *testing.T, plantStaleSocket bool) {
		h := newServeHarness(t)
		h.srv.autoSpawnNotify = true
		h.srv.tillrFlag = tillrBin
		h.srv.notifyIdleExit = 150 * time.Millisecond

		sockPath, err := notify.SocketPath(h.store)
		if err != nil {
			t.Fatalf("computing socket path: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Remove(sockPath)
			_ = os.Remove(notify.LockPath(sockPath))
		})

		if plantStaleSocket {
			if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
				t.Fatalf("mkdir socket dir: %v", err)
			}
			ul, err := net.Listen("unix", sockPath)
			if err != nil {
				t.Fatalf("creating stale unix socket: %v", err)
			}
			ul.(*net.UnixListener).SetUnlinkOnClose(false)
			_ = ul.Close()
		}

		conn := h.dial(t)
		waitForCondition(t, "auto-spawned relay subscription", func() bool {
			return h.srv.wsSubscribed.Load() == 1 && h.srv.wsPolls.Load() >= 1
		})

		seq := h.appendEvent("item.record", "item", "item-seed-02")
		frame := readInvalidation(t, conn, hangGuard)
		if frame.EventHeadSeq != seq {
			t.Fatalf("frame seq = %d, want %d", frame.EventHeadSeq, seq)
		}

		_ = conn.Close()
		// Wait passively for the spawned relay to observe 0 subscribers, exit on
		// notifyIdleExit, and unlink sockPath (calling Subscribe here would reset the
		// relay's idle timer).
		waitForCondition(t, "spawned relay idle-exit cleanup", func() bool {
			_, err := os.Stat(sockPath)
			return errors.Is(err, os.ErrNotExist)
		})
	}

	t.Run("ENOENT", func(t *testing.T) {
		runCase(t, false)
	})
	t.Run("ECONNREFUSED_stale_socket", func(t *testing.T) {
		runCase(t, true)
	})
}
