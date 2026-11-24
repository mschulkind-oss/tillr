// Package notify implements the local Unix-socket wake-hint relay and client
// primitives specified in docs/design/tillr-scheduling-and-notifications.md
// (D-SN5, D-SN7).
//
// Notifications are best-effort wake hints only: SQLite remains the single
// authority for state, publishers bound dial and write time to PublishTimeout
// and never spawn a relay, and a subscriber whose 256-frame buffer overflows
// receives one {"topic":"resync"} frame when its socket becomes writable.
package notify

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultIdleExit is the default idle duration after which a relay with
	// zero connected clients unlinks its socket under the lock and exits.
	DefaultIdleExit = 30 * time.Second

	// PublishTimeout caps how long Publish spends dialing and writing a hint.
	PublishTimeout = 5 * time.Millisecond

	// MaxFrameBytes is the maximum wire length of a newline-delimited JSON frame,
	// including the trailing '\n'.
	MaxFrameBytes = 4096

	// SubscriberBufferSize is the bounded per-subscriber frame ring capacity.
	SubscriberBufferSize = 256

	handshakeTimeout = 2 * time.Second
)

// Notification topics named in Section 4.2 of the scheduling and notifications design.
const (
	TopicQueueChanged   = "queue.changed"
	TopicAgentSlotFreed = "agent.slot-freed"
	TopicBackendChanged = "backend.changed"
	TopicRunAssigned    = "run.assigned"
	TopicRunStaged      = "run.staged"
	TopicRunStarted     = "run.started"
	TopicRunExited      = "run.exited"
	TopicCandidateReady = "candidate.ready"
	TopicGatePassed     = "gate.passed"
	TopicGateFinished   = "gate.finished"
	TopicTrunkMoved     = "trunk.moved"
	TopicProofGreen     = "proof.green"
	TopicItemEnded      = "item.ended"
	TopicDocsChanged    = "docs.changed"
	TopicResync         = "resync"
)

var (
	// ErrAlreadyRunning indicates another relay process already holds <sock>.lock.
	ErrAlreadyRunning = errors.New("notify: relay already running for store")

	// ErrFrameTooLarge indicates a frame exceeded MaxFrameBytes (4 KiB).
	ErrFrameTooLarge = errors.New("notify: frame exceeds 4 KiB limit")

	// ErrEmptyTopic indicates a frame was missing its required topic field.
	ErrEmptyTopic = errors.New("notify: publish frame requires non-empty topic")

	resyncLine = []byte("{\"topic\":\"resync\"}\n")
	ackLine    = []byte("{\"op\":\"ack\"}\n")
)

// Frame is a single wake-hint message delivered to subscribers.
type Frame struct {
	Topic  string `json:"topic"`
	ItemID string `json:"item_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	Seq    int64  `json:"seq,omitempty"`
	AtMS   int64  `json:"at_ms,omitempty"`
}

type wireMsg struct {
	Op     string   `json:"op"`
	Topics []string `json:"topics,omitempty"`
	Ack    bool     `json:"ack,omitempty"`
	Topic  string   `json:"topic,omitempty"`
	ItemID string   `json:"item_id,omitempty"`
	RunID  string   `json:"run_id,omitempty"`
	Seq    int64    `json:"seq,omitempty"`
	AtMS   int64    `json:"at_ms,omitempty"`
}

// SocketPath returns the deterministic AF_UNIX socket path for storePath:
// $XDG_RUNTIME_DIR/tillr/<sha256[:16]>.sock, or /tmp/tillr-<uid>/<sha256[:16]>.sock
// when XDG_RUNTIME_DIR is unset.
func SocketPath(storePath string) (string, error) {
	canonical, err := canonicalStorePath(storePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	short := hex.EncodeToString(sum[:])[:16]
	var dir string
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		dir = filepath.Join(xdg, "tillr")
	} else {
		dir = filepath.Join("/tmp", fmt.Sprintf("tillr-%d", os.Getuid()))
	}
	return filepath.Join(dir, short+".sock"), nil
}

// LockPath returns the flock companion file path (<sock>.lock) for sockPath.
func LockPath(sockPath string) string {
	return sockPath + ".lock"
}

// ActiveSocketPath reports the resolved socket path for storePath and whether a
// socket file currently exists on disk. Callers such as store.WriteTx use this
// to skip post-commit frame derivation when no relay is running.
func ActiveSocketPath(storePath string) (string, bool) {
	sockPath, err := SocketPath(storePath)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(sockPath); err != nil {
		return "", false
	}
	return sockPath, true
}

func canonicalStorePath(storePath string) (string, error) {
	if strings.TrimSpace(storePath) == "" {
		return "", errors.New("notify: empty store path")
	}
	abs, err := filepath.Abs(storePath)
	if err != nil {
		return "", fmt.Errorf("notify: resolving store path: %w", err)
	}
	return evalSymlinksMissingOK(abs)
}

func evalSymlinksMissingOK(abs string) (string, error) {
	cur := filepath.Clean(abs)
	var tail []string
	for hops := 0; hops < 40; hops++ {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("notify: evaluating store symlinks: %w", err)
		}
		if info, lerr := os.Lstat(cur); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, rerr := os.Readlink(cur)
			if rerr != nil {
				return "", fmt.Errorf("notify: reading store symlink: %w", rerr)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			cur = filepath.Clean(target)
			continue
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(abs), nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
	return "", errors.New("notify: too many symlink evaluations")
}

// Publish sends one or more wake-hint frames to the relay for storePath using a
// bounded 5 ms deadline. If no relay is listening or the socket cannot be
// reached within PublishTimeout, Publish drops the hint and returns nil without
// spawning any process.
func Publish(storePath string, frames ...Frame) error {
	if len(frames) == 0 {
		return nil
	}
	payload, err := encodePubBatch(frames)
	if err != nil {
		return err
	}
	sockPath, err := SocketPath(storePath)
	if err != nil {
		return nil
	}
	// Fast path when no relay is running: a single stat avoids timer, socket,
	// and netpoller setup in unit tests and offline CLI invocations.
	if _, err := os.Stat(sockPath); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), PublishTimeout)
	defer cancel()
	_ = publishEncoded(ctx, sockPath, payload)
	return nil
}

// PublishToSocket sends frames to an already-located relay socket path using
// PublishTimeout, dropping delivery errors if the relay disconnected or timed out.
func PublishToSocket(sockPath string, frames ...Frame) error {
	if len(frames) == 0 || sockPath == "" {
		return nil
	}
	payload, err := encodePubBatch(frames)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), PublishTimeout)
	defer cancel()
	_ = publishEncoded(ctx, sockPath, payload)
	return nil
}

// PublishContext sends frames to an already-running relay for storePath bounded
// by ctx, waiting for the relay to enqueue the batch before returning.
func PublishContext(ctx context.Context, storePath string, frames ...Frame) error {
	if len(frames) == 0 {
		return nil
	}
	payload, err := encodePubBatch(frames)
	if err != nil {
		return err
	}
	sockPath, err := SocketPath(storePath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(sockPath); err != nil {
		return err
	}
	return publishEncoded(ctx, sockPath, payload)
}

func encodePubBatch(frames []Frame) ([]byte, error) {
	var payload []byte
	for i, f := range frames {
		if f.Topic == "" {
			return nil, ErrEmptyTopic
		}
		line, err := encodePubLine(f, i == len(frames)-1)
		if err != nil {
			return nil, err
		}
		payload = append(payload, line...)
	}
	return payload, nil
}

func publishEncoded(ctx context.Context, sockPath string, payload []byte) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stop()
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	br := bufio.NewReaderSize(conn, 64)
	ackRaw, err := readBoundedLine(br)
	if err != nil {
		return err
	}
	var ack wireMsg
	if err := json.Unmarshal(ackRaw, &ack); err != nil || ack.Op != "ack" {
		return errors.New("notify: invalid publish acknowledgement")
	}
	return nil
}

func encodePubLine(f Frame, ack bool) ([]byte, error) {
	msg := wireMsg{
		Op:     "pub",
		Ack:    ack,
		Topic:  f.Topic,
		ItemID: f.ItemID,
		RunID:  f.RunID,
		Seq:    f.Seq,
		AtMS:   f.AtMS,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > MaxFrameBytes {
		return nil, ErrFrameTooLarge
	}
	return raw, nil
}

func encodeFrameLine(f Frame) ([]byte, error) {
	if f.Topic == "" {
		return nil, ErrEmptyTopic
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > MaxFrameBytes {
		return nil, ErrFrameTooLarge
	}
	return raw, nil
}

// MatchTopic reports whether topic matches any subscription pattern in patterns.
// An empty pattern list matches all topics, "*" matches all topics, "<prefix>.*"
// matches any topic starting with "<prefix>.", and TopicResync always matches.
func MatchTopic(patterns []string, topic string) bool {
	if topic == TopicResync || len(patterns) == 0 {
		return true
	}
	for _, pat := range patterns {
		if pat == "*" || pat == topic {
			return true
		}
		if strings.HasSuffix(pat, ".*") {
			prefix := strings.TrimSuffix(pat, "*")
			if strings.HasPrefix(topic, prefix) {
				return true
			}
		}
	}
	return false
}

// subscriberQueue is a bounded 256-frame ring buffer for one subscriber.
// Overflowing drops queued frames and latches resync so one {"topic":"resync"}
// frame is emitted next as soon as the subscriber's socket drains.
type subscriberQueue struct {
	mu     sync.Mutex
	buf    [SubscriberBufferSize][]byte
	head   int
	count  int
	resync bool
	closed bool
	wake   chan struct{}
	done   chan struct{}
}

func newSubscriberQueue() *subscriberQueue {
	return &subscriberQueue{
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

func (q *subscriberQueue) push(line []byte) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	if q.resync {
		q.mu.Unlock()
		q.signal()
		return
	}
	if q.count == SubscriberBufferSize {
		for i := range q.buf {
			q.buf[i] = nil
		}
		q.head = 0
		q.count = 0
		q.resync = true
	} else {
		idx := (q.head + q.count) % SubscriberBufferSize
		q.buf[idx] = line
		q.count++
	}
	q.mu.Unlock()
	q.signal()
}

func (q *subscriberQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *subscriberQueue) pop() ([]byte, bool) {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return nil, false
		}
		if q.resync {
			q.resync = false
			q.mu.Unlock()
			return resyncLine, true
		}
		if q.count > 0 {
			line := q.buf[q.head]
			q.buf[q.head] = nil
			q.head = (q.head + 1) % SubscriberBufferSize
			q.count--
			q.mu.Unlock()
			return line, true
		}
		q.mu.Unlock()
		select {
		case <-q.wake:
		case <-q.done:
			return nil, false
		}
	}
}

func (q *subscriberQueue) close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		for i := range q.buf {
			q.buf[i] = nil
		}
		close(q.done)
	}
	q.mu.Unlock()
}

// ServeConfig configures a relay server instance.
type ServeConfig struct {
	StorePath string
	IdleExit  time.Duration
	// Ready, when non-nil, receives the bound socket path once the relay is
	// listening under <sock>.lock.
	Ready chan<- string
}

type subscriber struct {
	topics []string
	queue  *subscriberQueue
	conn   net.Conn
}

type relay struct {
	idleExit    time.Duration
	ln          *net.UnixListener
	sockPath    string
	mu          sync.Mutex
	activeConns int
	closing     bool
	idleTimer   *time.Timer
	idleFired   chan struct{}
	subs        map[*subscriber]struct{}
	conns       map[net.Conn]struct{}
	wg          sync.WaitGroup
}

// Serve runs the single-instance Unix-socket notification relay for cfg.StorePath
// until ctx is cancelled or zero clients remain connected for cfg.IdleExit.
func Serve(ctx context.Context, cfg ServeConfig) error {
	sockPath, err := SocketPath(cfg.StorePath)
	if err != nil {
		return err
	}
	idleExit := cfg.IdleExit
	if idleExit <= 0 {
		idleExit = DefaultIdleExit
	}

	dir := filepath.Dir(sockPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("notify: creating runtime dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil && !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("notify: setting mode 0700 on %s: %w", dir, err)
	}

	lockFile, err := os.OpenFile(LockPath(sockPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("notify: opening lock %s: %w", LockPath(sockPath), err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("notify: locking %s: %w", LockPath(sockPath), err)
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	// Safe under <sock>.lock: no live relay owns sockPath.
	if err := os.Remove(sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("notify: removing stale socket %s: %w", sockPath, err)
	}

	addr := &net.UnixAddr{Name: sockPath, Net: "unix"}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("notify: listening on %s: %w", sockPath, err)
	}
	ln.SetUnlinkOnClose(true)
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		return fmt.Errorf("notify: setting mode 0600 on %s: %w", sockPath, err)
	}

	r := &relay{
		idleExit:  idleExit,
		ln:        ln,
		sockPath:  sockPath,
		idleFired: make(chan struct{}, 1),
		subs:      make(map[*subscriber]struct{}),
		conns:     make(map[net.Conn]struct{}),
	}
	r.idleTimer = time.AfterFunc(idleExit, r.onIdleTimer)

	if cfg.Ready != nil {
		cfg.Ready <- sockPath
	}

	stopDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			r.shutdown()
		case <-r.idleFired:
			r.shutdown()
		case <-stopDone:
		}
	}()

	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if r.isClosing() {
				break
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Temporary() {
				continue
			}
			r.shutdown()
			close(stopDone)
			r.wg.Wait()
			_ = os.Remove(sockPath)
			return fmt.Errorf("notify: accept on %s: %w", sockPath, err)
		}
		if !r.trackConn(conn) {
			_ = conn.Close()
			continue
		}
		r.wg.Add(1)
		go r.handleConn(conn)
	}

	close(stopDone)
	r.wg.Wait()
	// Ensure <sock> is unlinked while <sock>.lock is still held.
	_ = os.Remove(sockPath)
	return nil
}

func (r *relay) onIdleTimer() {
	r.mu.Lock()
	if !r.closing && r.activeConns == 0 {
		r.closing = true
		select {
		case r.idleFired <- struct{}{}:
		default:
		}
	}
	r.mu.Unlock()
}

func (r *relay) isClosing() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closing
}

func (r *relay) trackConn(conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	r.activeConns++
	r.conns[conn] = struct{}{}
	r.idleTimer.Stop()
	return true
}

func (r *relay) releaseConn(conn net.Conn, sub *subscriber) {
	r.mu.Lock()
	if sub != nil {
		delete(r.subs, sub)
	}
	if _, ok := r.conns[conn]; ok {
		delete(r.conns, conn)
		r.activeConns--
		if r.activeConns == 0 && !r.closing {
			r.idleTimer.Reset(r.idleExit)
		}
	}
	r.mu.Unlock()
}

func (r *relay) shutdown() {
	r.mu.Lock()
	r.closing = true
	r.idleTimer.Stop()
	subs := make([]*subscriber, 0, len(r.subs))
	for sub := range r.subs {
		subs = append(subs, sub)
	}
	conns := make([]net.Conn, 0, len(r.conns))
	for conn := range r.conns {
		conns = append(conns, conn)
	}
	r.mu.Unlock()

	_ = r.ln.Close()
	for _, sub := range subs {
		sub.queue.close()
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (r *relay) handleConn(conn *net.UnixConn) {
	defer r.wg.Done()
	var regSub *subscriber
	defer func() {
		if regSub != nil {
			regSub.queue.close()
		}
		_ = conn.Close()
		r.releaseConn(conn, regSub)
	}()

	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	br := bufio.NewReaderSize(conn, MaxFrameBytes)
	line, err := readBoundedLine(br)
	if err != nil {
		return
	}

	var first wireMsg
	if err := json.Unmarshal(line, &first); err != nil {
		return
	}

	switch first.Op {
	case "pub":
		if !r.handlePubMsg(conn, first) {
			return
		}
		for {
			_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
			next, err := readBoundedLine(br)
			if err != nil {
				return
			}
			var msg wireMsg
			if err := json.Unmarshal(next, &msg); err != nil || msg.Op != "pub" {
				return
			}
			if !r.handlePubMsg(conn, msg) {
				return
			}
		}

	case "sub":
		_ = conn.SetReadDeadline(time.Time{})
		// Keep kernel socket buffer tight (SOCK_MIN_SNDBUF >= MaxFrameBytes) so
		// stalled readers backpressure into the 256-frame user-space ring buffer
		// rather than accumulating hundreds of stale frames in kernel memory.
		_ = conn.SetWriteBuffer(MaxFrameBytes)

		sub := &subscriber{
			topics: append([]string(nil), first.Topics...),
			queue:  newSubscriberQueue(),
			conn:   conn,
		}
		r.mu.Lock()
		if r.closing {
			r.mu.Unlock()
			return
		}
		r.subs[sub] = struct{}{}
		regSub = sub
		r.mu.Unlock()

		if first.Ack {
			_ = conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
			if _, err := conn.Write(ackLine); err != nil {
				return
			}
			_ = conn.SetWriteDeadline(time.Time{})
		}

		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			for {
				out, ok := sub.queue.pop()
				if !ok {
					return
				}
				if _, err := conn.Write(out); err != nil {
					_ = conn.Close()
					return
				}
			}
		}()

		// Block until client disconnects (EOF) or relay shuts down.
		var scratch [64]byte
		for {
			if _, err := conn.Read(scratch[:]); err != nil {
				break
			}
		}
		sub.queue.close()
		_ = conn.Close()
		<-writerDone
	}
}

func (r *relay) handlePubMsg(conn net.Conn, msg wireMsg) bool {
	r.dispatchPub(msg)
	if msg.Ack {
		_ = conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
		if _, err := conn.Write(ackLine); err != nil {
			return false
		}
	}
	return true
}

func (r *relay) dispatchPub(msg wireMsg) {
	if msg.Topic == "" {
		return
	}
	frame := Frame{
		Topic:  msg.Topic,
		ItemID: msg.ItemID,
		RunID:  msg.RunID,
		Seq:    msg.Seq,
		AtMS:   msg.AtMS,
	}
	line, err := encodeFrameLine(frame)
	if err != nil {
		return
	}

	r.mu.Lock()
	subs := make([]*subscriber, 0, len(r.subs))
	for sub := range r.subs {
		if MatchTopic(sub.topics, frame.Topic) {
			subs = append(subs, sub)
		}
	}
	r.mu.Unlock()

	for _, sub := range subs {
		sub.queue.push(line)
	}
}

func readBoundedLine(br *bufio.Reader) ([]byte, error) {
	slice, err := br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, ErrFrameTooLarge
	}
	if err != nil {
		if errors.Is(err, io.EOF) && len(slice) > 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return bytes.Clone(bytes.TrimRight(slice, "\r\n")), nil
}

// Subscription reads wake-hint frames from a connected relay socket.
type Subscription struct {
	conn    net.Conn
	br      *bufio.Reader
	pending []byte
}

// Subscribe connects to an already-running relay for storePath, registers a
// subscription for topics, and waits for registration acknowledgement before
// returning. It never spawns a relay process.
func Subscribe(ctx context.Context, storePath string, topics ...string) (*Subscription, error) {
	sockPath, err := SocketPath(storePath)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		return nil, err
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.SetReadBuffer(MaxFrameBytes)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	}
	subMsg, err := json.Marshal(wireMsg{
		Op:     "sub",
		Topics: topics,
		Ack:    true,
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	subMsg = append(subMsg, '\n')
	if len(subMsg) > MaxFrameBytes {
		_ = conn.Close()
		return nil, ErrFrameTooLarge
	}
	if _, err := conn.Write(subMsg); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, MaxFrameBytes)
	ackRaw, err := readBoundedLine(br)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	var ack wireMsg
	if err := json.Unmarshal(ackRaw, &ack); err != nil || ack.Op != "ack" {
		_ = conn.Close()
		return nil, errors.New("notify: invalid subscribe acknowledgement")
	}
	_ = conn.SetDeadline(time.Time{})
	return &Subscription{conn: conn, br: br}, nil
}

// Next reads the next Frame from the relay, or returns an error if ctx ends or
// the connection closes.
func (s *Subscription) Next(ctx context.Context) (Frame, error) {
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = s.conn.SetReadDeadline(time.Now())
		close(cancelled)
	})
	defer func() {
		if !stop() {
			<-cancelled
			_ = s.conn.SetReadDeadline(time.Time{})
		}
	}()
	line, err := s.readLine()
	if err != nil {
		if ctx.Err() != nil {
			return Frame{}, ctx.Err()
		}
		return Frame{}, err
	}
	var f Frame
	if err := json.Unmarshal(line, &f); err != nil {
		return Frame{}, err
	}
	return f, nil
}

func (s *Subscription) readLine() ([]byte, error) {
	for {
		slice, err := s.br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			s.pending = nil
			return nil, ErrFrameTooLarge
		}
		if err != nil {
			if len(slice) > 0 {
				if len(s.pending)+len(slice) > MaxFrameBytes {
					s.pending = nil
					return nil, ErrFrameTooLarge
				}
				s.pending = append(s.pending, slice...)
			}
			if errors.Is(err, io.EOF) && len(s.pending) > 0 {
				s.pending = nil
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if len(s.pending) > 0 {
			if len(s.pending)+len(slice) > MaxFrameBytes {
				s.pending = nil
				return nil, ErrFrameTooLarge
			}
			full := append(s.pending, slice...)
			s.pending = s.pending[:0]
			return bytes.Clone(bytes.TrimRight(full, "\r\n")), nil
		}
		return bytes.Clone(bytes.TrimRight(slice, "\r\n")), nil
	}
}

// Close closes the underlying Unix socket subscription.
func (s *Subscription) Close() error {
	return s.conn.Close()
}
