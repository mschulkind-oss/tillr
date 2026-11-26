package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/store"
)

// The live channel of design §4.2: one socket per screen, pushing an invalidation per
// change instead of the changed contents. The client refetches through the same six
// routes it already reads, so a pushed invalidation and the manual refresh are the same
// request against the same cache keys (design §3.2, §5.2).
//
// The socket only watches. It subscribes to the local wake-hint relay (`tillr notify serve`)
// and reads the store through the handle the HTTP routes use, which store.OpenRead made
// read-only, so no path from here can write the store it describes (design §3.3).

const (
	// wsFallbackPollInterval is how often a subscribed socket reconciles its sources
	// when no notification frame arrives (docs/design/tillr-scheduling-and-notifications.md
	// Section 4.3, D-SN6: 15s steady-state fallback, retiring the 250ms poll).
	wsFallbackPollInterval = 15 * time.Second
	// wsDisconnectedPollInterval is how often a socket reads its sources while
	// disconnected from the local notification relay (Section 4.3: 1s while disconnected).
	wsDisconnectedPollInterval = 1 * time.Second
	// wsReconnectInitial and wsReconnectMax bound the jittered reconnect backoff when a
	// subscriber's relay connection closes or cannot be established (Section 4.2: 100ms
	// initial, 2x up to 1s cap).
	wsReconnectInitial = 100 * time.Millisecond
	wsReconnectMax     = 1 * time.Second
	// wsSpawnWaitTimeout bounds how long on-demand relay auto-start waits for a newly
	// spawned `tillr notify serve` process to bind <sock>.
	wsSpawnWaitTimeout = 2 * time.Second
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
// its exchanges (a stat per record), its timeline and every artifact it has open
// repeatedly for the length of the run. What a log line actually changes is the run's
// own payload — its completeness and log state — so the frame names that leaf instead.
const (
	resourceSnapshot     = "snapshot"
	resourceItemPrefix   = "items:"
	resourceRunPrefix    = "runs:"
	resourceRunLogPrefix = "runlog:"
	resourceAlerts       = "alerts"
	resourceAlertPrefix  = "alerts:"
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
		// An upgrade that begins while a restart drains is refused, and one that began is
		// counted until its socket is tracked, so the closing sweep cannot miss it.
		if !s.beginUpgrade() {
			http.Error(w, "the server is restarting", http.StatusServiceUnavailable)
			return
		}
		detector := newChangeDetector(s)
		primeErr := detector.prime(req.Context())
		conn, err := wsUpgrader.Upgrade(w, req, nil)
		if err != nil {
			s.endUpgrade()
			// Upgrade has already answered the request with the refusal it chose.
			return
		}
		defer conn.Close()
		s.trackSocket(conn)
		defer s.untrackSocket(conn)
		s.endUpgrade()
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

type wsTicker interface {
	Chan() <-chan time.Time
	Reset(time.Duration)
	Stop()
}

type realWSTicker struct{ *time.Ticker }

func (t realWSTicker) Chan() <-chan time.Time { return t.C }

func (s *server) makeWSTicker(d time.Duration) wsTicker {
	if s.newWSTicker != nil {
		return s.newWSTicker(d)
	}
	return realWSTicker{time.NewTicker(d)}
}

// streamInvalidations is one socket's life. A reader goroutine notices the client going
// away — a close frame, a pong, a dead connection — because a write-only loop cannot
// tell a client that left from one that is simply quiet. While subscribed to the local
// notification relay, the loop wakes on relayed frames and reconciles every 15 seconds
// (reverting to 1 second only while disconnected from the relay socket). A store that
// cannot answer closes the socket with the reason and leaves the client to reconnect, so
// a locked or vanished store degrades the screen to its fallback re-read, announced in
// the header, rather than taking the server down with it.
func (s *server) streamInvalidations(ctx context.Context, conn *websocket.Conn, detector *changeDetector) {
	var wg sync.WaitGroup
	defer wg.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			// Nothing a client sends is read for its content: this face has no command,
			// and the read exists for the control frames it also delivers.
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	var subscribed atomic.Bool
	wake := make(chan struct{}, 1)
	stateChanged := make(chan struct{}, 1)
	if !s.noNotify {
		subCtx, subCancel := context.WithTimeout(ctx, wsSpawnWaitTimeout)
		initialSub, _ := s.subscribeNotify(subCtx)
		subCancel()
		if ctx.Err() != nil {
			if initialSub != nil {
				_ = initialSub.Close()
			}
			return
		}
		if initialSub != nil {
			subscribed.Store(true)
			s.wsSubscribed.Add(1)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runNotifySubscriber(ctx, initialSub, &subscribed, wake, stateChanged)
		}()
	}

	activeInterval := s.wsInterval(subscribed.Load())
	ticker := s.makeWSTicker(activeInterval)
	defer ticker.Stop()
	window := newWSWindow(s.perf, time.Now)
	defer window.close("client gone")

	pollNow := true
	for {
		if pollNow {
			if !s.pollAndSend(ctx, conn, detector, window) {
				return
			}
		}
		if want := s.wsInterval(subscribed.Load()); want != activeInterval {
			activeInterval = want
			ticker.Reset(activeInterval)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
			pollNow = true
		case <-ticker.Chan():
			pollNow = true
		case <-stateChanged:
			pollNow = false
		}
	}
}

func (s *server) pollAndSend(ctx context.Context, conn *websocket.Conn, detector *changeDetector, window *wsWindow) bool {
	pollStart := time.Now()
	frame, changed, err := detector.poll(ctx)
	window.poll(msSince(time.Now, pollStart))
	s.wsPolls.Add(1)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		fmt.Fprintf(os.Stderr, "tillr-view: /api/v1/ws: %v\n", err)
		closeWithReason(conn, err.Error())
		window.close("store unreadable: " + err.Error())
		return false
	}
	if changed {
		if err := writeFrame(conn, frame); err != nil {
			window.close("write failed")
			return false
		}
		window.frame(frame.Changed)
	}
	return true
}

func notifySignal(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *server) runNotifySubscriber(ctx context.Context, initialSub *notify.Subscription, subscribed *atomic.Bool, wake, stateChanged chan<- struct{}) {
	sub := initialSub
	counted := sub != nil
	defer func() {
		if sub != nil {
			_ = sub.Close()
		}
		if counted {
			subscribed.Store(false)
			s.wsSubscribed.Add(-1)
		}
	}()

	backoff := s.effectiveReconnectInitial()
	maxBackoff := s.effectiveReconnectMax()
	for {
		if sub != nil {
			for {
				if _, err := sub.Next(ctx); err != nil {
					_ = sub.Close()
					sub = nil
					break
				}
				notifySignal(wake)
			}
			if ctx.Err() != nil {
				return
			}
			if counted {
				counted = false
				subscribed.Store(false)
				s.wsSubscribed.Add(-1)
				notifySignal(stateChanged)
			}
			backoff = s.effectiveReconnectInitial()
		}

		timer := time.NewTimer(jitterReconnect(backoff, maxBackoff))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		subCtx, subCancel := context.WithTimeout(ctx, wsSpawnWaitTimeout)
		nextSub, err := s.subscribeNotify(subCtx)
		subCancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		sub = nextSub
		backoff = s.effectiveReconnectInitial()
		counted = true
		subscribed.Store(true)
		s.wsSubscribed.Add(1)
		notifySignal(stateChanged)
		// Reconcile immediately on reconnect so changes committed during the
		// disconnect window do not wait for the 15-second fallback (Section 4.2).
		notifySignal(wake)
	}
}

func jitterReconnect(base, maxDelay time.Duration) time.Duration {
	if base > maxDelay {
		base = maxDelay
	}
	if base <= 0 {
		return wsReconnectInitial
	}
	quarter := int64(base) / 4
	if quarter <= 0 {
		return base
	}
	return time.Duration(int64(base) - quarter + rand.Int64N(quarter+1))
}

func (s *server) effectiveFallbackPoll() time.Duration {
	if s.fallbackPoll > 0 {
		return s.fallbackPoll
	}
	return wsFallbackPollInterval
}

func (s *server) effectiveDisconnectedPoll() time.Duration {
	if s.disconnectedPoll > 0 {
		return s.disconnectedPoll
	}
	return wsDisconnectedPollInterval
}

func (s *server) effectiveReconnectInitial() time.Duration {
	if s.reconnectInitial > 0 {
		return s.reconnectInitial
	}
	return wsReconnectInitial
}

func (s *server) effectiveReconnectMax() time.Duration {
	if s.reconnectMax > 0 {
		return s.reconnectMax
	}
	return wsReconnectMax
}

func (s *server) effectiveNotifyIdleExit() time.Duration {
	if s.notifyIdleExit > 0 {
		return s.notifyIdleExit
	}
	return notify.DefaultIdleExit
}

func (s *server) wsInterval(subscribed bool) time.Duration {
	if s.noNotify || subscribed {
		return s.effectiveFallbackPoll()
	}
	return s.effectiveDisconnectedPoll()
}

func isRelayUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

func (s *server) subscribeNotify(ctx context.Context) (*notify.Subscription, error) {
	sub, err := notify.Subscribe(ctx, s.storePath)
	if err == nil || !s.autoSpawnNotify || !isRelayUnavailable(err) || ctx.Err() != nil {
		return sub, err
	}

	s.notifySpawnMu.Lock()
	defer s.notifySpawnMu.Unlock()

	if sub, err = notify.Subscribe(ctx, s.storePath); err == nil {
		return sub, nil
	}
	if !isRelayUnavailable(err) || ctx.Err() != nil {
		return nil, err
	}

	program, resolveErr := resolveTillrCLI(s.tillrFlag)
	if resolveErr != nil {
		return nil, resolveErr
	}
	sockPath, pathErr := notify.SocketPath(s.storePath)
	if pathErr != nil {
		return nil, pathErr
	}
	if mkErr := os.MkdirAll(filepath.Dir(sockPath), 0o700); mkErr != nil {
		return nil, mkErr
	}
	lockFile, openErr := os.OpenFile(notify.LockPath(sockPath), os.O_CREATE|os.O_RDWR, 0o600)
	if openErr != nil {
		return nil, openErr
	}
	flockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if flockErr == nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		if spawnErr := s.startDetachedRelay(program); spawnErr != nil {
			return nil, spawnErr
		}
	} else {
		_ = lockFile.Close()
		if !errors.Is(flockErr, syscall.EWOULDBLOCK) && !errors.Is(flockErr, syscall.EAGAIN) {
			return nil, flockErr
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, wsSpawnWaitTimeout)
	defer cancel()
	for {
		if sub, err = notify.Subscribe(waitCtx, s.storePath); err == nil {
			return sub, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		case <-timer.C:
		}
	}
}

func resolveTillrCLI(explicit string) (string, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err == nil {
			err = executableFile(path)
		}
		return path, err
	}
	exe, _ := os.Executable()
	for _, path := range siblingCandidates(exe, "tillr") {
		if executableFile(path) == nil {
			return path, nil
		}
	}
	return exec.LookPath("tillr")
}

func (s *server) startDetachedRelay(program string) error {
	storeArg := s.storePath
	if abs, err := filepath.Abs(storeArg); err == nil {
		storeArg = abs
	}
	cmd := exec.Command(program, "notify", "serve", "--store", storeArg, "--idle-exit", s.effectiveNotifyIdleExit().String())
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
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
	srv *server
	// dataVersion is sqlite's PRAGMA data_version on this connection: it changes when
	// another connection commits, which is the one signal a read-only reader has for
	// "the store moved" that does not depend on what moved.
	dataVersion int64
	// eventHead is the highest events.seq the client has been told about.
	eventHead int64
	// logs is the running run log paths at eventHead, reused until an event moves runs/lanes.
	logs map[string]string
	// logSizes is the size of every running run's log at the last poll, by run id.
	logSizes map[string]int64
	primed   bool
}

func newChangeDetector(srv *server) *changeDetector {
	return &changeDetector{srv: srv, logSizes: map[string]int64{}}
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
		var err error
		if p.dataVersion, err = r.DataVersion(); err != nil {
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
		if d.primed && head == d.eventHead {
			p.logs = d.logs
			return nil
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
		d.dataVersion, d.eventHead, d.logs, d.logSizes, d.primed = p.dataVersion, p.eventHead, p.logs, sizes, true
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
	d.dataVersion, d.eventHead, d.logs, d.logSizes = p.dataVersion, p.eventHead, p.logs, sizes
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
		isWatchAlert := strings.HasPrefix(e.Kind, "watch.alert.") || strings.HasPrefix(e.Kind, "watch.silence.")
		if isWatchAlert {
			out = append(out, resourceAlerts)
			if len(e.Payload) > 0 {
				var body struct {
					AlertID        string `json:"alert_id"`
					SourceAlertID  string `json:"source_alert_id"`
					ItemID         string `json:"item_id"`
					TrackingItemID string `json:"tracking_item_id"`
				}
				if json.Unmarshal(e.Payload, &body) == nil {
					if body.AlertID != "" {
						out = append(out, resourceAlertPrefix+body.AlertID)
					}
					if body.SourceAlertID != "" {
						out = append(out, resourceAlertPrefix+body.SourceAlertID)
					}
					if body.ItemID != "" {
						out = append(out, resourceItemPrefix+body.ItemID)
					}
					if body.TrackingItemID != "" {
						out = append(out, resourceItemPrefix+body.TrackingItemID)
					}
				}
			}
		}
		if e.ObjectID == nil || *e.ObjectID == "" {
			continue
		}
		switch e.ObjectType {
		case "item":
			out = append(out, resourceItemPrefix+*e.ObjectID)
		case "alert":
			out = append(out, resourceAlerts, resourceAlertPrefix+*e.ObjectID)
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
// log. A socket wakes on relayed notifications and reconciles on its fallback interval, so
// a line per wake or per frame during a burst would flood the log; a window says how many
// frames went out and what they named, which is what the client paid for.
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

// trackSocket registers a live channel so a restart can close it: Shutdown does not wait for
// hijacked connections and would otherwise leave them to die with the process.
func (s *server) trackSocket(conn *websocket.Conn) {
	s.socketMu.Lock()
	defer s.socketMu.Unlock()
	if s.sockets == nil {
		s.sockets = map[*websocket.Conn]struct{}{}
	}
	s.sockets[conn] = struct{}{}
}

func (s *server) untrackSocket(conn *websocket.Conn) {
	s.socketMu.Lock()
	defer s.socketMu.Unlock()
	delete(s.sockets, conn)
}

// beginUpgrade admits one WebSocket upgrade unless a restart is draining. Every admitted
// upgrade must reach endUpgrade, after Upgrade has failed or the socket is tracked.
func (s *server) beginUpgrade() bool {
	s.socketMu.Lock()
	defer s.socketMu.Unlock()
	if s.draining {
		return false
	}
	s.upgrading++
	return true
}

func (s *server) endUpgrade() {
	s.socketMu.Lock()
	s.upgrading--
	s.socketMu.Unlock()
}

// openUpgrades admits upgrades again after a restart that did not replace the process.
func (s *server) openUpgrades() {
	s.socketMu.Lock()
	s.draining = false
	s.socketMu.Unlock()
}

// closeSockets stops admitting upgrades, waits for the ones already past admission to be
// tracked, and ends every live channel with "going away"; a browser reconnects on its own.
func (s *server) closeSockets() {
	s.socketMu.Lock()
	s.draining = true
	for s.upgrading > 0 {
		s.socketMu.Unlock()
		time.Sleep(5 * time.Millisecond)
		s.socketMu.Lock()
	}
	conns := make([]*websocket.Conn, 0, len(s.sockets))
	for conn := range s.sockets {
		conns = append(conns, conn)
	}
	s.socketMu.Unlock()
	for _, conn := range conns {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "the server is restarting"), time.Now().Add(time.Second))
		_ = conn.Close()
	}
}
