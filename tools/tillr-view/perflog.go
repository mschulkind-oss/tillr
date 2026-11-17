package main

// The observatory's own performance log: one JSON line per API request, per WebSocket
// window and per batch of browser timings, plus a startup record and a periodic summary, so
// "the board is slow" can be answered from a file instead of reproduced.
//
// It is the server's account of itself, which is a different subject from the Performance
// page (performance.go): that page reads the DRAIN's gate and item timings out of the store,
// and this file times the requests that page — and every other — makes.
//
// The log is written by one goroutine from a bounded queue. A request never waits for the
// disk: when the queue is full the record is dropped and counted, and the writer reports the
// count as a `dropped` line when it catches up — the same rule tillr's SQLite trace keeps
// (tools/tillr/store/trace.go): an observer that stalls what it observes is not an observer.
//
// The directory is the caller's (`--perf-log`); `just observatory` names one under the
// checkout's own durable scratch directory, so a log the host wrote is readable from a jail
// that mounts the same checkout (tools/observatory/run.sh). Each day gets its own file,
// `server-YYYYMMDD.jsonl` (a `.N` part when a day passes the size cap), and the file `latest`
// names the one being written, as text rather than a symlink a bind mount may not carry.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// perfLogVersion is the record shape. A reader refuses nothing on it, but a line it does not
// recognize is skipped and counted rather than mis-summarized.
const perfLogVersion = 1

const (
	// perfQueueLimit is how many encoded lines may wait for the writer. A page load is a
	// dozen requests; a thousand lines is minutes of a busy board.
	perfQueueLimit = 1024
	// perfSummaryEvery is how often the summary line is written while requests arrive.
	perfSummaryEvery = time.Minute
	// perfSampleLimit bounds the durations kept per route for the summary's percentiles. The
	// count and the maximum are exact for the whole uptime; the percentiles are over the most
	// recent samples, which the line says.
	perfSampleLimit = 4096
	// Retention: a day's file is capped, older days are deleted, and the directory as a whole
	// is capped. A day that passes the file cap rolls to a numbered part rather than stopping.
	perfFileCapBytes  = 32 << 20
	perfKeepDays      = 7
	perfTotalCapBytes = 128 << 20
	// perfClientBodyLimit bounds one POST of browser timings.
	perfClientBodyLimit = 256 << 10
)

// perfLog is one server's log. The zero of every field but the queue is usable; a nil
// *perfLog is the switched-off log, and every method on it does nothing.
type perfLog struct {
	dir   string
	host  string
	rev   string
	now   func() time.Time
	queue chan []byte
	// dropped is how many lines the queue refused; reported is how many of those the
	// writer has already written a `dropped` line for.
	dropped  atomic.Uint64
	reported uint64
	seq      atomic.Uint64

	mu    sync.Mutex
	stats map[string]*routeStats
	since time.Time

	done     chan struct{}
	stopOnce sync.Once
	stopped  chan struct{}

	// The writer's own state, touched only by the writer goroutine.
	file     *os.File
	buf      *bufio.Writer
	fileDay  string
	filePart int
	fileSize int64
	// The retention bounds, the package's constants unless a test shrinks them.
	fileCap, totalCap int64
	keepDays          int
	// failure is the last error the writer met, reported once on stderr so a full disk is
	// said rather than silently losing the log.
	failure string
}

type routeStats struct {
	Count   int
	Errors  int
	Bytes   int64
	MaxMS   float64
	samples []float64
	next    int
}

func (r *routeStats) add(ms float64, bytes int64, status int) {
	r.Count++
	r.Bytes += bytes
	if status >= 500 {
		r.Errors++
	}
	if ms > r.MaxMS {
		r.MaxMS = ms
	}
	if len(r.samples) < perfSampleLimit {
		r.samples = append(r.samples, ms)
		return
	}
	r.samples[r.next] = ms
	r.next = (r.next + 1) % perfSampleLimit
}

// percentile is the nearest-rank percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// openPerfLog starts the log in dir. An empty dir or `off` is the switched-off log. A
// directory that cannot be made is reported and switches the log off: the board serves
// either way, and a log that cannot be written must not be the reason it does not.
func openPerfLog(dir, rev string, now func() time.Time) *perfLog {
	l := newPerfLog(dir, rev, now, perfQueueLimit)
	if l != nil {
		l.start()
	}
	return l
}

// newPerfLog makes the log without starting its goroutines, so a test can fill the queue
// before anything drains it.
func newPerfLog(dir, rev string, now func() time.Time, queue int) *perfLog {
	if dir == "" || dir == "off" {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view serve: perf log: %v; serving without one\n", err)
		return nil
	}
	host, _ := os.Hostname()
	return &perfLog{dir: dir, host: host, rev: rev, now: now, queue: make(chan []byte, queue),
		stats: map[string]*routeStats{}, since: now(), done: make(chan struct{}), stopped: make(chan struct{}),
		fileCap: perfFileCapBytes, totalCap: perfTotalCapBytes, keepDays: perfKeepDays}
}

func (l *perfLog) start() {
	go l.write()
	go l.summarize(perfSummaryEvery)
}

// path is the file being written now, as the `latest` pointer names it.
func (l *perfLog) path() string {
	if l == nil {
		return ""
	}
	return filepath.Join(l.dir, l.fileName(l.now().Format("20060102"), 0))
}

func (l *perfLog) fileName(day string, part int) string {
	if part == 0 {
		return "server-" + day + ".jsonl"
	}
	return "server-" + day + "." + strconv.Itoa(part) + ".jsonl"
}

// emit queues one record. The kind, timestamp, version and revision are stamped here so no
// caller can forget them; a full queue drops the line and counts it.
func (l *perfLog) emit(kind string, fields map[string]any) {
	l.emitWithin(kind, fields, 0)
}

// emitWithin is emit that may wait up to wait for room. Only the final summary waits: at
// close nothing is serving, and losing the one line that summarizes the run to a queue the
// writer is about to drain would be the wrong economy.
func (l *perfLog) emitWithin(kind string, fields map[string]any, wait time.Duration) {
	if l == nil {
		return
	}
	fields["v"] = perfLogVersion
	fields["kind"] = kind
	fields["ts"] = l.now().Format(time.RFC3339Nano)
	fields["rev"] = l.rev
	raw, err := json.Marshal(fields)
	if err != nil {
		raw, _ = json.Marshal(map[string]any{"v": perfLogVersion, "kind": "unencodable", "of": kind, "error": err.Error()})
	}
	if wait > 0 {
		select {
		case l.queue <- raw:
		case <-time.After(wait):
			l.dropped.Add(1)
		}
		return
	}
	select {
	case l.queue <- raw:
	default:
		l.dropped.Add(1)
	}
}

// requestID is short, unique within this process and ordered: a reader correlates a
// browser's fetch with the server's line by it (the X-Request-Id header).
func (l *perfLog) requestID() string {
	if l == nil {
		return ""
	}
	return strconv.FormatInt(l.since.UnixMilli()%1e6, 36) + "-" + strconv.FormatUint(l.seq.Add(1), 10)
}

// observe records one finished request in the summary.
func (l *perfLog) observe(route string, ms float64, bytes int64, status int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.stats[route]
	if s == nil {
		s = &routeStats{}
		l.stats[route] = s
	}
	s.add(ms, bytes, status)
}

// summaryFields is the summary line: every route since start, with percentiles a reader
// does not have to compute.
func (l *perfLog) summaryFields() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	routes := map[string]any{}
	total := 0
	for route, s := range l.stats {
		sorted := append([]float64(nil), s.samples...)
		sort.Float64s(sorted)
		routes[route] = map[string]any{"count": s.Count, "errors": s.Errors, "bytes": s.Bytes,
			"p50_ms": round1(percentile(sorted, 50)), "p95_ms": round1(percentile(sorted, 95)),
			"max_ms": round1(s.MaxMS), "sampled": len(sorted)}
		total += s.Count
	}
	return map[string]any{"since": l.since.Format(time.RFC3339Nano),
		"uptime_s": round1(l.now().Sub(l.since).Seconds()), "requests": total, "routes": routes,
		"dropped": l.dropped.Load()}
}

func (l *perfLog) summarize(every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	last := -1
	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			fields := l.summaryFields()
			if n := fields["requests"].(int); n != last {
				last = n
				l.emit("summary", fields)
			}
		}
	}
}

// close writes the final summary and waits, boundedly, for the queue to drain.
func (l *perfLog) close() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() {
		l.emitWithin("summary", l.summaryFields(), time.Second)
		close(l.done)
		select {
		case <-l.stopped:
		case <-time.After(2 * time.Second):
		}
	})
}

// write is the one goroutine that touches the files.
func (l *perfLog) write() {
	defer close(l.stopped)
	for {
		select {
		case line := <-l.queue:
			l.writeLine(line)
		case <-l.done:
			for {
				select {
				case line := <-l.queue:
					l.writeLine(line)
				default:
					l.reportDrops()
					l.flush()
					if l.file != nil {
						l.file.Close()
					}
					return
				}
			}
		}
		if len(l.queue) == 0 {
			l.reportDrops()
			l.flush()
		}
	}
}

func (l *perfLog) reportDrops() {
	if n := l.dropped.Load(); n > l.reported {
		raw, _ := json.Marshal(map[string]any{"v": perfLogVersion, "kind": "dropped", "ts": l.now().Format(time.RFC3339Nano),
			"rev": l.rev, "count": n - l.reported, "total": n})
		l.reported = n
		l.writeLine(raw)
	}
}

func (l *perfLog) flush() {
	if l.buf != nil {
		if err := l.buf.Flush(); err != nil {
			l.fail(err)
		}
	}
}

func (l *perfLog) fail(err error) {
	if msg := err.Error(); msg != l.failure {
		l.failure = msg
		fmt.Fprintf(os.Stderr, "tillr-view serve: perf log: %v\n", err)
	}
}

// writeLine appends one line to today's file, rolling to a new day or part first.
func (l *perfLog) writeLine(line []byte) {
	day := l.now().Format("20060102")
	if l.file == nil || day != l.fileDay || l.fileSize+int64(len(line))+1 > l.fileCap {
		part := 0
		if day == l.fileDay {
			part = l.filePart + 1
		}
		if err := l.open(day, part); err != nil {
			l.fail(err)
			return
		}
	}
	n, err := l.buf.Write(append(line, '\n'))
	l.fileSize += int64(n)
	if err != nil {
		l.fail(err)
	}
}

func (l *perfLog) open(day string, part int) error {
	l.flush()
	if l.file != nil {
		l.file.Close()
		l.file, l.buf = nil, nil
	}
	for {
		path := filepath.Join(l.dir, l.fileName(day, part))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		// A restart the same day appends to the day's file, unless that file is full.
		if info.Size() >= l.fileCap {
			f.Close()
			part++
			continue
		}
		l.file, l.buf, l.fileDay, l.filePart, l.fileSize = f, bufio.NewWriterSize(f, 64<<10), day, part, info.Size()
		l.pointLatest(path)
		l.prune()
		return nil
	}
}

// pointLatest writes `latest`: the path of the file being written, replaced atomically.
func (l *perfLog) pointLatest(path string) {
	tmp := filepath.Join(l.dir, ".latest."+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, []byte(path+"\n"), 0o644); err != nil {
		l.fail(err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(l.dir, "latest")); err != nil {
		l.fail(err)
	}
}

// prunePerfLogs deletes the directory's logs dated more than keepDays before today, then
// the oldest until the directory is under totalCap bytes. The file being written is never
// deleted, and nothing but `server-YYYYMMDD[.N].jsonl` is touched.
func prunePerfLogs(dir string, today time.Time, current string, keepDays int, totalCap int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type logFile struct {
		path string
		day  string
		part int
		size int64
	}
	var files []logFile
	cutoff := today.AddDate(0, 0, -keepDays).Format("20060102")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "server-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(name, "server-"), ".jsonl")
		day, partText, _ := strings.Cut(stem, ".")
		if len(day) != 8 {
			continue
		}
		part, _ := strconv.Atoi(partText)
		path := filepath.Join(dir, name)
		if path == current {
			continue
		}
		if day < cutoff {
			_ = os.Remove(path)
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, logFile{path, day, part, info.Size()})
	}
	var total int64
	if info, err := os.Stat(current); err == nil {
		total = info.Size()
	}
	for _, f := range files {
		total += f.size
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].day != files[j].day {
			return files[i].day < files[j].day
		}
		return files[i].part < files[j].part
	})
	for _, f := range files {
		if total <= totalCap {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

func (l *perfLog) prune() {
	prunePerfLogs(l.dir, l.now(), filepath.Join(l.dir, l.fileName(l.fileDay, l.filePart)), l.keepDays, l.totalCap)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func msSince(now func() time.Time, start time.Time) float64 {
	return round1(float64(now().Sub(start).Microseconds()) / 1000)
}

// --- one request's phases ---------------------------------------------------------------

// reqTrace is what one request's handlers say about where its time went. Handlers write
// it through phase(), which is a no-op on a request with no trace.
type reqTrace struct {
	mu     sync.Mutex
	phases map[string]any
}

type reqTraceKey struct{}

func withReqTrace(ctx context.Context) (context.Context, *reqTrace) {
	t := &reqTrace{phases: map[string]any{}}
	return context.WithValue(ctx, reqTraceKey{}, t), t
}

// phase records one named phase of the request in ctx: a duration in milliseconds, or a
// small object (the planner's duration, exit and bytes).
func phase(ctx context.Context, name string, value any) {
	t, _ := ctx.Value(reqTraceKey{}).(*reqTrace)
	if t == nil {
		return
	}
	t.mu.Lock()
	t.phases[name] = value
	t.mu.Unlock()
}

func (t *reqTrace) snapshot() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]any, len(t.phases))
	for k, v := range t.phases {
		out[k] = v
	}
	return out
}
