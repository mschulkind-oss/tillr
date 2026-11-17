package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	sqliteTraceSlowSpan    = 2 * time.Second // Observation only, never cancellation.
	sqliteTraceActiveLimit = 32
	// One queue slot holds one phase record or one span's stack sample. A span queues
	// each phase as it happens, so a WriteTx costs three slots: begin, acquired and end.
	sqliteTraceQueueLimit = 1024
	sqliteTraceStackLimit = 1 << 20
	// The bound on a program's exit flush: long enough for a healthy stderr to take a
	// stack sample, short enough that a stalled one cannot hold a process at exit.
	sqliteTraceExitFlush = 500 * time.Millisecond
)

var sqliteTrace = newSQLiteTraceObserver(sqliteTraceQueueLimit)
var writerSequence atomic.Uint64

// SQLiteTraceStatus is cumulative for this process. Complete is truthful only
// at a caller-established quiescent boundary: no active/snapshot/pending work,
// no admission/queue drops, and no sink failures since process start.
// Ordinary process exit without an explicit flush is an incomplete capture.
type SQLiteTraceStatus struct {
	Active, SnapshotCollections                                  int
	Pending, Delivered, DroppedSpans, DroppedRecords, SinkErrors uint64
	// LossReported is false while the delivery goroutine has not yet printed the
	// current loss counters as a `tillr-sqlite ... dropped` line.
	LossReported bool
	Complete     bool
}

var ErrIncompleteSQLiteTrace = errors.New("SQLite trace capture is incomplete")

// SQLiteTraceDeliveryStatus observes bounded in-memory counters, never the sink.
func SQLiteTraceDeliveryStatus() SQLiteTraceStatus { return sqliteTrace.status() }

// FlushSQLiteTrace is for a quiescent CLI/test capture boundary OUTSIDE all
// application locks. No Store operation or Close calls it. It never starts SQL,
// cancels a span, or joins a blocked sink after its caller's context expires.
// It waits for every queued record and for the report of any loss; active spans
// and all historical loss remain explicit in the returned status.
func FlushSQLiteTrace(ctx context.Context) (SQLiteTraceStatus, error) {
	o := sqliteTrace
	if !o.started.Load() {
		return o.status(), nil
	}
	var ticker *time.Ticker
	for {
		status := o.status()
		if status.Pending == 0 && status.SnapshotCollections == 0 && status.LossReported {
			if !status.Complete {
				return status, ErrIncompleteSQLiteTrace
			}
			return status, nil
		}
		if ticker == nil {
			ticker = time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
		}
		select {
		case <-ctx.Done():
			return o.status(), ctx.Err()
		case <-ticker.C:
		}
	}
}

// FlushSQLiteTraceAtExit is a traced program's last step before it exits: call it
// from main after all work has returned, outside every lock, on every exit route.
// Records are delivered by a background goroutine, so a process that exits straight
// after a failure would otherwise lose the failing span's own outcome. The wait is
// bounded (sqliteTraceExitFlush), so a stalled stderr delays exit but cannot hold it.
// With tracing off for the whole process it is one atomic load. The result is
// FlushSQLiteTrace's; a program has nowhere left to report it, so main ignores it.
func FlushSQLiteTraceAtExit() (SQLiteTraceStatus, error) {
	if !sqliteTrace.started.Load() {
		return SQLiteTraceStatus{LossReported: true, Complete: true}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteTraceExitFlush)
	defer cancel()
	return FlushSQLiteTrace(ctx)
}

// sqliteTraceRecord is one queued line group: a phase (snapshot empty) or a span's
// stack sample (snapshot set; at is when the sample was taken).
type sqliteTraceRecord struct {
	id              uint64
	path, operation string
	started, at     time.Time
	sink            io.Writer
	phase, detail   string
	err             error
	snapshot, stage string
	truncated       bool
}
type sqliteTraceSpan struct {
	observer        *sqliteTraceObserver
	id              uint64
	path, operation string
	started         time.Time
	sink            io.Writer
	stage           string // Guarded by observer.mu: the sampler reads it.
	sampled         bool
}
type sqliteTraceSink struct{ w io.Writer }
type sqliteTraceObserver struct {
	once                                                         sync.Once
	started                                                      atomic.Bool
	mu                                                           sync.Mutex // Only bounded memory copying; no stack collection or delivery.
	active                                                       map[uint64]*sqliteTraceSpan
	collections                                                  int
	queue                                                        chan sqliteTraceRecord
	wake                                                         chan struct{} // Capacity 1: a loss to report.
	lossSink                                                     atomic.Pointer[sqliteTraceSink]
	reportedLoss                                                 atomic.Uint64
	pending, delivered, droppedSpans, droppedRecords, sinkErrors atomic.Uint64
	buffer                                                       *bufio.Writer // Owned by deliver.
}

func newSQLiteTraceObserver(queue int) *sqliteTraceObserver {
	return &sqliteTraceObserver{active: make(map[uint64]*sqliteTraceSpan), queue: make(chan sqliteTraceRecord, queue), wake: make(chan struct{}, 1)}
}

func startSQLiteTrace(path, operation string) *sqliteTraceSpan {
	if os.Getenv("TILLR_SQLITE_TRACE") != "1" {
		return nil
	}
	o := sqliteTrace
	o.once.Do(func() {
		o.buffer = bufio.NewWriterSize(io.Discard, 64<<10)
		o.started.Store(true)
		go o.deliver()
		go o.sample()
	})
	sink := io.Writer(os.Stderr)
	o.mu.Lock()
	if len(o.active) == sqliteTraceActiveLimit {
		o.mu.Unlock()
		o.noteLoss(sink, &o.droppedSpans)
		return nil
	}
	s := &sqliteTraceSpan{observer: o, id: writerSequence.Add(1), path: path, operation: operation, started: time.Now(), sink: sink, stage: "begin"}
	o.active[s.id] = s
	o.pending.Add(1)
	o.mu.Unlock()
	o.enqueue(s.phaseRecord(s.started, "begin", nil))
	return s
}

// record queues one phase now, so a span still in flight when its process exits or
// is killed has already handed over everything up to its current phase.
func (s *sqliteTraceSpan) record(phase string, err error) { s.recordDetail(phase, "", err) }

// recordDetail is record with key=value fields after the error, such as a log's frame count.
// A caller builds detail only when the span is not nil, so tracing off formats nothing.
func (s *sqliteTraceSpan) recordDetail(phase, detail string, err error) {
	if s == nil {
		return
	}
	at := time.Now()
	o := s.observer
	o.mu.Lock()
	s.stage = phase
	o.pending.Add(1)
	o.mu.Unlock()
	r := s.phaseRecord(at, phase, err)
	r.detail = detail
	o.enqueue(r)
}

func (s *sqliteTraceSpan) finish(err error) {
	if s == nil {
		return
	}
	at := time.Now()
	o := s.observer
	o.mu.Lock()
	delete(o.active, s.id)
	// Count the pending handoff before removing the active span becomes visible.
	// Flush can never mistake this handoff window for a complete capture.
	o.pending.Add(1)
	o.mu.Unlock()
	o.enqueue(s.phaseRecord(at, "end", err))
}
func (s *sqliteTraceSpan) phaseRecord(at time.Time, phase string, err error) sqliteTraceRecord {
	return sqliteTraceRecord{id: s.id, path: s.path, operation: s.operation, started: s.started, at: at, sink: s.sink, phase: phase, err: err}
}

// enqueue hands one record, already counted in pending, to the delivery goroutine.
// It never waits: a span's caller may hold the SQLite writer or an application
// mutex, so a full queue is a counted, later-reported loss and never a stall.
func (o *sqliteTraceObserver) enqueue(r sqliteTraceRecord) {
	select {
	case o.queue <- r:
	default:
		o.pending.Add(^uint64(0))
		o.noteLoss(r.sink, &o.droppedRecords)
	}
}

// noteLoss counts a loss and wakes the delivery goroutine to report it, without
// waiting: the sink is stored before the count so the report has somewhere to go.
func (o *sqliteTraceObserver) noteLoss(sink io.Writer, counter *atomic.Uint64) {
	o.lossSink.Store(&sqliteTraceSink{sink})
	counter.Add(1)
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *sqliteTraceObserver) sample() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for now := range ticker.C {
		var due []sqliteTraceRecord
		o.mu.Lock()
		for _, s := range o.active {
			if s.sampled || now.Sub(s.started) < sqliteTraceSlowSpan {
				continue
			}
			s.sampled = true
			due = append(due, sqliteTraceRecord{id: s.id, path: s.path, operation: s.operation, started: s.started, sink: s.sink, stage: s.stage})
		}
		if len(due) > 0 {
			o.collections++
		}
		o.mu.Unlock()
		if len(due) == 0 {
			continue
		}
		// One collection serves every span that crossed the threshold this tick, and
		// neither application nor observer mutex is held while it runs. It is still not
		// free for a lock holder: runtime.Stack(buf, true) stops the world while it
		// formats every goroutine, so the holder (and everyone else) pauses for the
		// length of the dump. That happens at most once per tick, and only on a tick
		// where some span first passes sqliteTraceSlowSpan, so each span triggers at
		// most one pause in its life.
		buf := make([]byte, sqliteTraceStackLimit)
		n := runtime.Stack(buf, true)
		snapshot, at := string(buf[:n]), time.Now()
		for _, r := range due {
			r.snapshot, r.at, r.truncated = snapshot, at, n == len(buf)
			o.pending.Add(1)
			o.enqueue(r)
		}
		o.mu.Lock()
		o.collections--
		o.mu.Unlock()
	}
}

// deliver is the only goroutine that writes trace output. Each record goes through
// one buffered writer, so a stack sample of thousands of frames is a few syscalls,
// not one per frame, and loss is reported here, never from a span's caller.
func (o *sqliteTraceObserver) deliver() {
	for {
		select {
		case r := <-o.queue:
			if o.write(r) != nil {
				o.lossSink.Store(&sqliteTraceSink{r.sink})
				o.sinkErrors.Add(1)
			} else {
				o.delivered.Add(1)
			}
			o.reportLoss()
			o.pending.Add(^uint64(0))
		case <-o.wake:
			o.reportLoss()
		}
	}
}

func (o *sqliteTraceObserver) write(r sqliteTraceRecord) error {
	w := o.buffer
	w.Reset(r.sink)
	if r.snapshot == "" {
		fmt.Fprintf(w, "tillr-sqlite at=%s pid=%d tx=%d store=%q operation=%q phase=%s elapsed=%s error=%q", r.at.UTC().Format(time.RFC3339Nano), os.Getpid(), r.id, r.path, r.operation, r.phase, r.at.Sub(r.started), errorText(r.err))
		if r.detail != "" {
			fmt.Fprintf(w, " %s", r.detail)
		}
		fmt.Fprintln(w)
		return w.Flush()
	}
	for _, line := range strings.Split(strings.TrimSuffix(r.snapshot, "\n"), "\n") {
		fmt.Fprintf(w, "tillr-sqlite-stack at=%s pid=%d tx=%d store=%q operation=%q elapsed=%s stage=%q frame=%q\n", r.at.UTC().Format(time.RFC3339Nano), os.Getpid(), r.id, r.path, r.operation, r.at.Sub(r.started), r.stage, line)
	}
	if r.truncated {
		fmt.Fprintf(w, "tillr-sqlite-stack pid=%d tx=%d operation=%q truncated=true\n", os.Getpid(), r.id, r.operation)
	}
	return w.Flush()
}

// reportLoss prints the loss counters whenever they have changed since the last
// report, so a capture that lost anything says so in its own output. A failed
// report is not itself counted, or a broken sink would report forever.
func (o *sqliteTraceObserver) reportLoss() {
	spans, records, sinkErrors := o.droppedSpans.Load(), o.droppedRecords.Load(), o.sinkErrors.Load()
	total := spans + records + sinkErrors
	if total == o.reportedLoss.Load() {
		return
	}
	if sink := o.lossSink.Load(); sink != nil {
		w := o.buffer
		w.Reset(sink.w)
		fmt.Fprintf(w, "tillr-sqlite at=%s pid=%d dropped spans=%d records=%d sink_errors=%d\n", time.Now().UTC().Format(time.RFC3339Nano), os.Getpid(), spans, records, sinkErrors)
		_ = w.Flush()
	}
	o.reportedLoss.Store(total)
}

func (o *sqliteTraceObserver) status() SQLiteTraceStatus {
	o.mu.Lock()
	active, collections := len(o.active), o.collections
	o.mu.Unlock()
	status := SQLiteTraceStatus{Active: active, SnapshotCollections: collections, Pending: o.pending.Load(), Delivered: o.delivered.Load(), DroppedSpans: o.droppedSpans.Load(), DroppedRecords: o.droppedRecords.Load(), SinkErrors: o.sinkErrors.Load()}
	status.LossReported = o.reportedLoss.Load() == status.DroppedSpans+status.DroppedRecords+status.SinkErrors
	status.Complete = status.Active == 0 && status.SnapshotCollections == 0 && status.Pending == 0 && status.DroppedSpans == 0 && status.DroppedRecords == 0 && status.SinkErrors == 0
	return status
}

// The original error still returns to the caller. Arbitrary messages can expose
// SQL values or callback secrets, so only the delivery worker classifies them.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) {
		return fmt.Sprintf("sqlite_code=%d", sqliteError.Code())
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline_exceeded"
	}
	return fmt.Sprintf("error_type=%T", err)
}
