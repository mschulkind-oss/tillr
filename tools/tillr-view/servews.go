package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The live channel of design §4.2: one socket per screen, pushing an invalidation per
// change instead of the changed contents. The client refetches through the same six
// routes it already reads, so a pushed invalidation and the manual refresh are the same
// request against the same cache keys (design §3.2, §5.2).
//
// The socket only watches. It reads the store through the handle the HTTP routes use,
// which store.OpenRead made read-only, so no path from here can write the store it
// describes (design §3.3).

const (
	// wsPollInterval is how often one socket reads its sources. Design §4.2 names 250ms:
	// fast enough that a running drain reads as live, slow enough that an idle screen
	// costs four queries a second.
	wsPollInterval = 250 * time.Millisecond
	// wsWriteTimeout bounds one frame's write, so a client that stopped reading cannot
	// pin this connection's goroutine for the life of the process.
	wsWriteTimeout = 5 * time.Second
)

// The resource names a frame's `changed` list carries. They are the client's cache key
// prefixes (web/src/models/queries.ts), not ids: `snapshot` is the whole screen,
// `items:<id>` is one item and `runs:<id>` is one run — whose exchanges, timeline and
// artifacts the client invalidates as children of that same key.
//
// `runlog:<id>` is the one exception to the subtree spelling, and it exists because a log
// that grew is the one change that repeats: a run writing its log would otherwise refetch
// its exchanges (a stat per record), its timeline and every artifact it has open, four
// times a second for the length of the run. What a log line actually changes is the run's
// own payload — its completeness and log state — so the frame names that leaf instead.
const (
	resourceSnapshot     = "snapshot"
	resourceItemPrefix   = "items:"
	resourceRunPrefix    = "runs:"
	resourceRunLogPrefix = "runlog:"
)

// invalidationFrame is the one message this socket sends. It carries the cursor the
// change was read at, so a client can tell two frames apart, and the resources that went
// stale — never their new contents, which would be a second read model beside the six
// routes (design §4.2).
type invalidationFrame struct {
	Type         string   `json:"type"`
	EventHeadSeq int64    `json:"event_head_seq"`
	Changed      []string `json:"changed"`
}

// wsUpgrader is the RFC 6455 handshake. The protocol's framing is the library's business;
// what this face decides is who may open a socket.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     wsOriginAllowed,
}

// wsHandler is `GET /api/v1/ws`: a primed baseline, an upgrade, then a frame per change
// until the client leaves or the store stops answering.
//
// The baseline is read BEFORE the handshake, and that order is the whole of it: the client
// refetches on open, so a baseline read after the upgrade would fold anything landing
// between the client's read and the server's into "already seen" — the one kind of change
// nothing would ever push. Primed first, every change after this point is compared against,
// and a client that reads newer than the baseline only ever sees one redundant refetch.
func (s *server) wsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		detector := newChangeDetector(s)
		primeErr := detector.prime(req.Context())
		conn, err := wsUpgrader.Upgrade(w, req, nil)
		if err != nil {
			// Upgrade has already answered the request with the refusal it chose.
			return
		}
		defer conn.Close()
		if primeErr != nil {
			// A baseline this process cannot read still answers the handshake — a browser
			// reports a refused upgrade as a close with no reason at all — and then ends the
			// socket with the cause, which is what the client shows.
			fmt.Fprintf(os.Stderr, "tillr-view: /api/v1/ws: %v\n", primeErr)
			closeWithReason(conn, primeErr.Error())
			return
		}
		s.streamInvalidations(req.Context(), conn, detector)
	}
}

// wsOriginAllowed answers whether a page may open this socket. The front end is served
// from the same origin, and `vite` proxies `/api` from its own port during development
// (design §5.4), so a loopback origin on any port is the same operator at a terminal. A
// page anywhere else is refused: the board carries local paths, prompts and costs, and
// a socket that ignores Origin would let any page in a browser read them.
func wsOriginAllowed(req *http.Request) bool {
	origin := req.Header.Get("Origin")
	if origin == "" {
		// No Origin is a non-browser client, which no cross-site page can forge.
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// streamInvalidations is one socket's life. A reader goroutine notices the client going
// away — a close frame, a pong, a dead connection — because a write-only loop cannot
// tell a client that left from one that is simply quiet. The poll pushes a frame per
// change; a store that cannot answer closes the socket with the reason and leaves the
// client to reconnect, so a locked or vanished store degrades the screen to its fallback
// re-read, announced in the header, rather than taking the server down with it.
func (s *server) streamInvalidations(ctx context.Context, conn *websocket.Conn, detector *changeDetector) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer cancel()
		for {
			// Nothing a client sends is read for its content: this face has no command,
			// and the read exists for the control frames it also delivers.
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(detector.interval)
	defer ticker.Stop()
	window := newWSWindow(s.perf, time.Now)
	defer window.close("client gone")
	for {
		pollStart := time.Now()
		frame, changed, err := detector.poll(ctx)
		window.poll(msSince(time.Now, pollStart))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(os.Stderr, "tillr-view: /api/v1/ws: %v\n", err)
			closeWithReason(conn, err.Error())
			window.close("store unreadable: " + err.Error())
			return
		}
		if changed {
			if err := writeFrame(conn, frame); err != nil {
				window.close("write failed")
				return
			}
			window.frame(frame.Changed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func writeFrame(conn *websocket.Conn, frame invalidationFrame) error {
	if err := conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteJSON(frame)
}

// closeWithReason ends a socket with the reason in a close frame, so the client can say
// what happened instead of showing a silent reconnect. RFC 6455 bounds a close reason to
// 123 bytes, and a sqlite error is longer than that.
func closeWithReason(conn *websocket.Conn, reason string) {
	if len(reason) > 120 {
		reason = reason[:120]
	}
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseInternalServerErr, reason),
		time.Now().Add(wsWriteTimeout))
}

// changeDetector is the poll behind one socket. It holds the cursors the client's cache
// was last known to match, and answers the resources that moved past them.
type changeDetector struct {
	srv      *server
	interval time.Duration
	// dataVersion is sqlite's PRAGMA data_version on this connection: it changes when
	// another connection commits, which is the one signal a read-only reader has for
	// "the store moved" that does not depend on what moved.
	dataVersion int64
	// eventHead is the highest events.seq the client has been told about.
	eventHead int64
	// logSizes is the size of every running run's log at the last poll, by run id.
	logSizes map[string]int64
	primed   bool
}

func newChangeDetector(srv *server) *changeDetector {
	return &changeDetector{srv: srv, interval: wsPollInterval, logSizes: map[string]int64{}}
}

// prime records the cursors the client's own read is about to match, so every change after
// this point is one the client is told about. It is one poll, and the poll answers nothing
// while it is what records the baseline.
func (d *changeDetector) prime(ctx context.Context) error {
	_, _, err := d.poll(ctx)
	return err
}

// storeProbe is one poll's read of the store: the two cursors, the resources the events
// past the last head named, and where every running run is writing its log.
type storeProbe struct {
	dataVersion int64
	eventHead   int64
	resources   []string
	logs        map[string]string
}

// probe reads everything one poll needs in one read transaction, so the cursors and the
// events they name describe the same moment.
func (d *changeDetector) probe(ctx context.Context) (storeProbe, error) {
	p := storeProbe{logs: map[string]string{}}
	err := d.srv.store.ReadTx(ctx, func(r *store.Reader) error {
		if err := r.QueryRow(`PRAGMA data_version`).Scan(&p.dataVersion); err != nil {
			return fmt.Errorf("reading the store's data_version: %w", err)
		}
		head, err := r.EventHeadSeq()
		if err != nil {
			return err
		}
		p.eventHead = head
		if head > d.eventHead {
			events, err := r.Events(d.eventHead)
			if err != nil {
				return err
			}
			p.resources = eventResources(events)
		}
		// A run the store still calls running, and a run a lane holds with no row yet: the
		// terminal write creates the row, so a live run's log is watched from the lease.
		rows, err := r.Query(`SELECT run_id, lane_id FROM runs WHERE state = 'running'
			UNION SELECT run_id, lane_id FROM lanes WHERE state = 'leased' AND run_id != ''`)
		if err != nil {
			if isMissingTable(err) {
				// A store written before the runs table existed has no live log to
				// watch, which is an answer rather than a failure.
				return nil
			}
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var runID, laneID string
			if err := rows.Scan(&runID, &laneID); err != nil {
				return err
			}
			if laneID == "" || d.srv.obs.laneRoot == "" {
				continue
			}
			// The path tillr-run writes, spelled once in transcript.go and read here.
			p.logs[runID] = runLogPath(d.srv.obs.laneRoot, laneID, runID)
		}
		return rows.Err()
	})
	return p, err
}

// poll answers the frame one interval produces, and whether there is one. The first poll
// after a connection opens only records the baseline: the screen was read over HTTP
// before the socket existed, so everything it can see is current by definition.
func (d *changeDetector) poll(ctx context.Context) (invalidationFrame, bool, error) {
	p, err := d.probe(ctx)
	if err != nil {
		return invalidationFrame{}, false, err
	}
	sizes := make(map[string]int64, len(p.logs))
	for runID, path := range p.logs {
		info, err := os.Stat(path)
		if err != nil {
			// A running run whose log is not there yet has written no byte, which is a
			// size of zero rather than a failure: the file appears on the first one.
			sizes[runID] = 0
			continue
		}
		sizes[runID] = info.Size()
	}
	if !d.primed {
		d.dataVersion, d.eventHead, d.logSizes, d.primed = p.dataVersion, p.eventHead, sizes, true
		return invalidationFrame{}, false, nil
	}
	// The store moved: the events name which entities, and the snapshot names the rest —
	// a candidate, a lane or a decision has no cache entry of its own, and every view
	// that shows one shows it through the snapshot.
	changed := p.resources
	if p.dataVersion != d.dataVersion || p.eventHead != d.eventHead {
		changed = append(changed, resourceSnapshot)
	}
	// A log that grew is a running run that said something new. It names the run's own
	// payload and not its subtree, and not the snapshot: nothing the board shows moved, and
	// a log grows repeatedly while everything else about the run does not.
	for runID, size := range sizes {
		if size > d.logSizes[runID] {
			changed = append(changed, resourceRunLogPrefix+runID)
		}
	}
	d.dataVersion, d.eventHead, d.logSizes = p.dataVersion, p.eventHead, sizes
	if len(changed) == 0 {
		return invalidationFrame{}, false, nil
	}
	return invalidationFrame{
		Type:         "snapshot.invalidated",
		EventHeadSeq: p.eventHead,
		Changed:      sortedUnique(changed),
	}, true, nil
}

// eventResources names the cache keys a batch of events touches. An event whose object is
// a candidate, a lane or a decision names nothing here: those are rendered from the
// snapshot, which every store change invalidates anyway.
func eventResources(events []event.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		if e.Kind == "land.landing" || e.Kind == "drain.refusal" {
			out = append(out, "landings")
		}
		if e.ObjectID == nil || *e.ObjectID == "" {
			continue
		}
		switch e.ObjectType {
		case "item":
			out = append(out, resourceItemPrefix+*e.ObjectID)
		case "run":
			out = append(out, resourceRunPrefix+*e.ObjectID)
			// A worker claim also changes the Item page's derived progress marks.
			// Live item queries do not poll; name that cache entry explicitly.
			if e.Kind == "run.progress" {
				var claim progressClaim
				if json.Unmarshal(e.Payload, &claim) == nil && claim.ItemID != "" {
					out = append(out, resourceItemPrefix+claim.ItemID)
				}
			}
		}
	}
	return out
}

// sortedUnique is the frame's `changed` list: one entry per resource, in a stable order,
// so a client can compare two frames and a test can assert one.
func sortedUnique(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// wsWindowEvery is how often one socket's activity becomes a `ws` line in the performance
// log. A socket polls four times a second, so a line per poll — or per frame, which a running
// lane's growing log sends up to four times a second — would be most of the log; a window
// says how many frames went out and what they named, which is what the client paid for.
const wsWindowEvery = 10 * time.Second

// wsWindow accumulates one socket's polls and frames between log lines.
type wsWindow struct {
	log     *perfLog
	now     func() time.Time
	id      string
	opened  time.Time
	start   time.Time
	polls   int
	pollSum float64
	pollMax float64
	frames  int
	changed map[string]int
	closed  bool
}

func newWSWindow(log *perfLog, now func() time.Time) *wsWindow {
	if log != nil {
		now = log.now
	}
	t := now()
	return &wsWindow{log: log, now: now, id: log.requestID(), opened: t, start: t, changed: map[string]int{}}
}

func (w *wsWindow) poll(ms float64) {
	if w.log == nil {
		return
	}
	w.polls++
	w.pollSum += ms
	if ms > w.pollMax {
		w.pollMax = ms
	}
	if w.now().Sub(w.start) >= wsWindowEvery {
		w.flush("")
	}
}

// frame counts one frame by the kinds of resource it named: `runlog:<id>` and `runs:<id>`
// are counted by their prefix, because the client's cost is per kind, not per id.
func (w *wsWindow) frame(changed []string) {
	if w.log == nil {
		return
	}
	w.frames++
	for _, name := range changed {
		kind, _, found := strings.Cut(name, ":")
		if !found {
			kind = name
		}
		w.changed[kind]++
	}
}

func (w *wsWindow) flush(closing string) {
	if w.log == nil || (w.frames == 0 && closing == "") {
		w.start, w.polls, w.pollSum, w.pollMax = w.now(), 0, 0, 0
		return
	}
	fields := map[string]any{"conn": w.id, "window_ms": msSince(w.now, w.start), "polls": w.polls,
		"poll_ms_max": round1(w.pollMax), "frames": w.frames, "changed": w.changed}
	if w.polls > 0 {
		fields["poll_ms_mean"] = round1(w.pollSum / float64(w.polls))
	}
	if closing != "" {
		fields["closed"] = closing
		fields["lifetime_ms"] = msSince(w.now, w.opened)
	}
	w.log.emit("ws", fields)
	w.start, w.polls, w.pollSum, w.pollMax, w.frames, w.changed = w.now(), 0, 0, 0, 0, map[string]int{}
}

func (w *wsWindow) close(reason string) {
	if w.closed {
		return
	}
	w.closed = true
	w.flush(reason)
}
