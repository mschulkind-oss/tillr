package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// The request side of the performance log (perflog.go) and the response compression the
// log's first measurements asked for. Both wrap the whole route set, so a route cannot be
// added without being timed.

// recorder is the ResponseWriter one request is answered through: it counts what the handler
// wrote and what reached the wire, and it still hijacks, because the live channel's upgrade
// goes through it too.
type recorder struct {
	http.ResponseWriter
	status int
	body   int64 // bytes the handler wrote
	wire   int64 // bytes that reached the connection (after compression)
	gz     *gzip.Writer
	decide bool // the compression decision is made at the first header write
	allow  bool // the request may be answered compressed
	hijack bool
	zipped bool
}

func (w *recorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if w.allow && !w.decide {
		w.decide = true
		if compressible(w.Header(), status) {
			h := w.Header()
			h.Del("Content-Length")
			h.Set("Content-Encoding", "gzip")
			h.Add("Vary", "Accept-Encoding")
			w.zipped = true
			w.gz = gzipPool.Get().(*gzip.Writer)
			w.gz.Reset(wireCounter{w})
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *recorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(http.StatusOK)
	}
	w.body += int64(len(p))
	if w.gz != nil {
		return w.gz.Write(p)
	}
	n, err := w.ResponseWriter.Write(p)
	w.wire += int64(n)
	return n, err
}

// wireCounter is where the gzip stream writes: the real connection, counted.
type wireCounter struct{ w *recorder }

func (c wireCounter) Write(p []byte) (int, error) {
	n, err := c.w.ResponseWriter.Write(p)
	c.w.wire += int64(n)
	return n, err
}

func (w *recorder) finish() {
	if w.gz != nil {
		_ = w.gz.Close()
		w.gz.Reset(io.Discard)
		gzipPool.Put(w.gz)
		w.gz = nil
	}
}

func (w *recorder) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the connection cannot be hijacked")
	}
	w.hijack = true
	return h.Hijack()
}

func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// gzipPool keeps the compressors: one is ~800 KB of state, and a page load opens a dozen.
// BestSpeed because the board is on loopback or a LAN: the bytes saved by a slower level
// are worth less than the milliseconds it costs on a multi-megabyte run timeline.
var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

// compressible answers whether a response is worth compressing: text, JSON and scripts,
// with a body, not already encoded.
func compressible(h http.Header, status int) bool {
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified || status == http.StatusPartialContent {
		return false
	}
	if h.Get("Content-Encoding") != "" {
		return false
	}
	ct := strings.ToLower(h.Get("Content-Type"))
	return strings.HasPrefix(ct, "text/") || strings.HasPrefix(ct, "application/json") ||
		strings.HasPrefix(ct, "application/javascript") || strings.HasPrefix(ct, "image/svg+xml")
}

// acceptsGzip reads Accept-Encoding the way a browser sends it. A range request is answered
// uncompressed, because the range is of the identity body.
func acceptsGzip(req *http.Request) bool {
	if req.Header.Get("Range") != "" || req.Header.Get("Upgrade") != "" {
		return false
	}
	for _, part := range strings.Split(req.Header.Get("Accept-Encoding"), ",") {
		name, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") && strings.TrimSpace(q) != "q=0" {
			return true
		}
	}
	return false
}

// instrument wraps the route set: every request gets an id (sent back as X-Request-Id, so a
// browser's timing can be joined to the server's line), a phase trace its handlers can
// write, compression when the client takes it, and one log line when it ends.
func (s *server) instrument(next http.Handler) http.Handler {
	now := time.Now
	if s.perf != nil {
		now = s.perf.now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := now()
		id := s.perf.requestID()
		ctx, trace := withReqTrace(req.Context())
		req = req.WithContext(ctx)
		if id != "" {
			w.Header().Set("X-Request-Id", id)
		}
		rec := &recorder{ResponseWriter: w, allow: acceptsGzip(req)}
		next.ServeHTTP(rec, req)
		rec.finish()
		if s.perf == nil || rec.hijack {
			// A hijacked request is the live channel: its life is logged as `ws` windows,
			// and its handshake as a request line would be a duration of the whole socket.
			return
		}
		if req.Method == http.MethodPost && req.URL.Path == clientTimingPath {
			// The browser's own report is its own record; a line per report about the
			// report would double the log for nothing a reader asks.
			return
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		ms := msSince(now, start)
		route := routeOf(req)
		fields := map[string]any{"id": id, "method": req.Method, "path": req.URL.Path, "route": route,
			"status": status, "bytes": rec.body, "wire_bytes": rec.wire, "ms": ms,
			"gzip": rec.zipped}
		if phases := trace.snapshot(); len(phases) > 0 {
			fields["phases"] = phases
		}
		if keys := queryKeys(req); keys != "" {
			// The names, never the values: a value is the caller's, and a reader needs only
			// to tell `?since=` from `?before=` apart.
			fields["query"] = keys
		}
		s.perf.emit("request", fields)
		s.perf.observe(route, ms, rec.body, status)
	})
}

// routeOf is the request's route pattern — `GET /api/v1/runs/{id}` rather than one run's
// path — so the summary groups a page's requests by what they ask, not by which id. The
// hashed assets are checked first: the web handler's catch-all pattern is `GET /`, which
// would fold every asset into the page's own route.
func routeOf(req *http.Request) string {
	if strings.HasPrefix(req.URL.Path, "/assets/") {
		return req.Method + " /assets/*"
	}
	if req.Pattern != "" {
		return req.Pattern
	}
	return req.Method + " " + req.URL.Path
}

func queryKeys(req *http.Request) string {
	if req.URL.RawQuery == "" {
		return ""
	}
	var keys []string
	for k := range req.URL.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// --- the browser's report ------------------------------------------------------------------

// clientTimingPath is where the front end posts what the browser measured. It is the one
// non-GET route, and it writes only to this log: never to the store, which stays unwritable
// from this process (store.OpenRead).
const clientTimingPath = "/api/v1/client-timing"

// clientTimingHandler appends one batch of browser timings as a `client` record. The body is
// parsed and re-encoded rather than copied, so a line in the log is always one JSON object
// whatever was posted; a page on another origin is refused for the same reason the live
// channel refuses one (wsOriginAllowed).
func (s *server) clientTimingHandler(w http.ResponseWriter, req *http.Request) {
	if !wsOriginAllowed(req) {
		http.Error(w, "client timings are accepted from this board's own origin only", http.StatusForbidden)
		return
	}
	if s.perf == nil {
		// Logging is off: the report is accepted and discarded, so a browser does not retry.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var body map[string]any
	dec := json.NewDecoder(io.LimitReader(req.Body, perfClientBodyLimit))
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "a client timing batch is one JSON object: "+err.Error(), http.StatusBadRequest)
		return
	}
	delete(body, "v")
	delete(body, "kind")
	delete(body, "rev")
	body["client_ts"] = body["ts"]
	body["user_agent"] = req.UserAgent()
	s.perf.emit("client", body)
	w.WriteHeader(http.StatusNoContent)
}
