package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// The performance log and the reader of it. Every duration below is a recorded number or an
// injected clock's: nothing asserts on how long anything really took.

// fakeClock answers the time a test sets.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func readPerfLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("a line is not one JSON object: %v: %s", err, sc.Text())
		}
		out = append(out, rec)
	}
	return out
}

func perfKinds(lines []map[string]any) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l["kind"].(string))
	}
	return out
}

func TestPerfLogWritesDailyFilesAndPointsLatestAtTheCurrentOne(t *testing.T) {
	dir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 23, 59, 0, 0, time.Local)}
	l := openPerfLog(dir, "rev-1", clock.now)
	l.emit("start", map[string]any{"port": "4318"})
	l.observe("GET /api/v1/why", 200, 10, 200)
	l.emit("request", map[string]any{"route": "GET /api/v1/why", "ms": 200.0})
	// A write is asynchronous; wait for the first file to exist before moving the clock.
	first := filepath.Join(dir, "server-20261117.jsonl")
	waitFor(t, func() bool { return len(readPerfLinesIfAny(first)) == 2 })
	clock.set(time.Date(2026, 11, 18, 0, 1, 0, 0, time.Local))
	l.emit("request", map[string]any{"route": "GET /api/v1/why", "ms": 220.0})
	l.close()

	lines := readPerfLines(t, first)
	if got := perfKinds(lines); strings.Join(got, ",") != "start,request" {
		t.Fatalf("the first day's file holds %v", got)
	}
	for _, line := range lines {
		if line["v"] != float64(perfLogVersion) || line["rev"] != "rev-1" || line["ts"] == "" {
			t.Fatalf("a line lacks its stamp: %v", line)
		}
	}
	second := filepath.Join(dir, "server-20261118.jsonl")
	got := perfKinds(readPerfLines(t, second))
	if strings.Join(got, ",") != "request,summary" {
		t.Fatalf("the second day's file holds %v", got)
	}
	latest, err := os.ReadFile(filepath.Join(dir, "latest"))
	if err != nil || strings.TrimSpace(string(latest)) != second {
		t.Fatalf("latest names %q (%v), want %s", latest, err, second)
	}
}

func readPerfLinesIfAny(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// waitFor polls a condition the writer goroutine will make true; it bounds the wait but
// asserts nothing about how long the write took.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("the log writer never wrote the line")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPerfLogRollsAFullDayToAPartAndPrunesByAgeAndSize(t *testing.T) {
	dir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	// Old and oversized files a previous server left: two past the age cutoff, and two
	// recent ones that together pass the total cap.
	write := func(name string, size int) {
		if err := os.WriteFile(filepath.Join(dir, name), bytes.Repeat([]byte("x"), size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("server-20261101.jsonl", 10)
	write("server-20261109.2.jsonl", 10)
	write("server-20261115.jsonl", 600)
	write("server-20261116.jsonl", 600)
	write("notes.txt", 5)
	l := newPerfLog(dir, "rev", clock.now, 16)
	l.fileCap, l.totalCap, l.keepDays = 200, 1000, 7
	l.start()
	for i := 0; i < 6; i++ {
		l.emit("request", map[string]any{"pad": strings.Repeat("y", 60)})
	}
	l.close()
	names := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, gone := range []string{"server-20261101.jsonl", "server-20261109.2.jsonl", "server-20261115.jsonl"} {
		if names[gone] {
			t.Fatalf("%s survived the pruning: %v", gone, names)
		}
	}
	if !names["notes.txt"] || !names["server-20261117.jsonl"] || !names["server-20261117.1.jsonl"] {
		t.Fatalf("the day did not roll to a part, or a foreign file was touched: %v", names)
	}
	for name := range names {
		if strings.HasPrefix(name, "server-20261117") {
			if info, _ := os.Stat(filepath.Join(dir, name)); info.Size() > 200 {
				t.Fatalf("%s is %d bytes, past the 200-byte cap", name, info.Size())
			}
		}
	}
}

func TestPerfLogDropsRatherThanBlocksAndSaysHowMany(t *testing.T) {
	dir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	l := newPerfLog(dir, "rev", clock.now, 2)
	// Nothing drains yet: the third and fourth lines must be refused, not waited on.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 4; i++ {
			l.emit("request", map[string]any{"n": i})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("emit blocked on a full queue")
	}
	if l.dropped.Load() != 2 {
		t.Fatalf("dropped %d, want 2", l.dropped.Load())
	}
	l.start()
	l.close()
	lines := readPerfLines(t, filepath.Join(dir, "server-20261117.jsonl"))
	var dropped map[string]any
	for _, line := range lines {
		if line["kind"] == "dropped" {
			dropped = line
		}
	}
	if dropped == nil || dropped["count"] != float64(2) {
		t.Fatalf("no dropped line counting 2: %v", perfKinds(lines))
	}
}

func TestPerfLogOffIsANilLogThatDoesNothing(t *testing.T) {
	for _, dir := range []string{"", "off"} {
		l := openPerfLog(dir, "rev", nil)
		if l != nil {
			t.Fatalf("%q opened a log", dir)
		}
		// Every method is safe on the switched-off log.
		l.emit("request", map[string]any{})
		l.observe("GET /", 1, 1, 200)
		if l.requestID() != "" || l.path() != "" {
			t.Fatal("a switched-off log answered an id or a path")
		}
		l.close()
	}
}

func TestPerfSummaryPercentilesAreNearestRank(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	l := newPerfLog(t.TempDir(), "rev", clock.now, 4)
	for i := 1; i <= 20; i++ {
		l.observe("GET /api/v1/why", float64(i*10), 100, 200)
	}
	l.observe("GET /api/v1/why", 5000, 100, 502)
	clock.advance(90 * time.Second)
	s := l.summaryFields()
	route := s["routes"].(map[string]any)["GET /api/v1/why"].(map[string]any)
	if route["count"] != 21 || route["errors"] != 1 || route["p50_ms"] != 110.0 || route["p95_ms"] != 200.0 || route["max_ms"] != 5000.0 {
		t.Fatalf("summary %v", route)
	}
	if s["uptime_s"] != 90.0 {
		t.Fatalf("uptime %v", s["uptime_s"])
	}
}

// instrumentedServer is a server with no store: the instrumented routes under test here are
// the web assets and the client-timing POST, which never open one.
func instrumentedServer(t *testing.T, perf *perfLog) *httptest.Server {
	t.Helper()
	js := strings.Repeat("console.log('observatory');\n", 400)
	s := &server{perf: perf, web: fstest.MapFS{
		"index.html":          {Data: []byte(`<html><script src="/assets/index-abc.js"></script></html>`)},
		"assets/index-abc.js": {Data: []byte(js)},
	}}
	srv := httptest.NewServer(s.instrument(s.routesWithoutStore()))
	t.Cleanup(srv.Close)
	return srv
}

// routesWithoutStore is the part of routes() these cases reach.
func (s *server) routesWithoutStore() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+clientTimingPath, s.clientTimingHandler)
	mux.HandleFunc("GET /api/v1/slow", func(w http.ResponseWriter, req *http.Request) {
		phase(req.Context(), "planner", map[string]any{"ms": 1234.5, "exit": 0})
		writeJSON(w, http.StatusOK, map[string]any{"rows": strings.Repeat("r", 4000)})
	})
	mux.Handle("GET /", s.webHandler())
	return mux
}

func TestInstrumentLogsEachRequestWithItsPhasesAndCompresses(t *testing.T) {
	dir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	l := openPerfLog(dir, "rev", clock.now)
	srv := instrumentedServer(t, l)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/slow?since=1&before=2", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("X-Request-Id") == "" {
		t.Fatalf("headers %v", resp.Header)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	resp.Body.Close()
	if !strings.Contains(string(body), strings.Repeat("r", 4000)) {
		t.Fatal("the compressed body is not the answer")
	}
	id := resp.Header.Get("X-Request-Id")

	// The asset is cacheable forever; the page that names it is revalidated.
	resp, err = http.Get(srv.URL + "/assets/index-abc.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control %q", got)
	}
	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index Cache-Control %q", got)
	}
	l.close()

	lines := readPerfLines(t, filepath.Join(dir, "server-20261117.jsonl"))
	var slow map[string]any
	for _, line := range lines {
		if line["id"] == id {
			slow = line
		}
	}
	if slow == nil {
		t.Fatalf("no line for request %s: %v", id, perfKinds(lines))
	}
	// Every hashed asset is one route, not a row per file nor folded into the page's route.
	routes := map[string]string{}
	for _, line := range lines {
		if path, _ := line["path"].(string); path != "" {
			routes[path], _ = line["route"].(string)
		}
	}
	if routes["/assets/index-abc.js"] != "GET /assets/*" || routes["/"] != "GET /" {
		t.Fatalf("asset and page routes %v", routes)
	}
	phases := slow["phases"].(map[string]any)
	planner := phases["planner"].(map[string]any)
	if slow["route"] != "GET /api/v1/slow" || slow["status"] != float64(200) || slow["gzip"] != true ||
		slow["query"] != "before,since" || planner["ms"] != 1234.5 {
		t.Fatalf("request line %v", slow)
	}
	if slow["bytes"].(float64) <= slow["wire_bytes"].(float64) {
		t.Fatalf("a compressed answer reached the wire no smaller: %v", slow)
	}
	if strings.Contains(slow["path"].(string), "since") {
		t.Fatalf("the path carries the query's values: %v", slow["path"])
	}
}

func TestClientTimingIsAppendedAsAClientRecordFromThisOriginOnly(t *testing.T) {
	dir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	l := openPerfLog(dir, "rev", clock.now)
	srv := instrumentedServer(t, l)
	post := func(origin, body string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+clientTimingPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	batch := `{"ts":"2026-11-17T17:00:00Z","kind":"request","session":"s1","page_views":[{"page":"#/","nav":"load","first_data_ms":420}],"fetches":[{"path":"/api/v1/why","request_id":"x-1","ms":250}]}`
	if code := post("http://127.0.0.1:4318", batch); code != http.StatusNoContent {
		t.Fatalf("loopback origin answered %d", code)
	}
	if code := post("https://elsewhere.example", batch); code != http.StatusForbidden {
		t.Fatalf("a foreign origin answered %d", code)
	}
	if code := post("", "not json\n"); code != http.StatusBadRequest {
		t.Fatalf("a body that is not JSON answered %d", code)
	}
	l.close()
	lines := readPerfLines(t, filepath.Join(dir, "server-20261117.jsonl"))
	var clients []map[string]any
	for _, line := range lines {
		if line["kind"] == "client" {
			clients = append(clients, line)
		}
		if line["kind"] == "request" {
			t.Fatalf("the report about the report was logged: %v", line)
		}
	}
	// The posted `kind` cannot pass for a server record: the server stamps its own.
	if len(clients) != 1 || clients[0]["session"] != "s1" || clients[0]["client_ts"] != "2026-11-17T17:00:00Z" {
		t.Fatalf("client records %v", clients)
	}

	// Logging off: the report is accepted and discarded, and nothing is written.
	offDir := t.TempDir()
	off := instrumentedServer(t, nil)
	req, _ := http.NewRequest(http.MethodPost, off.URL+clientTimingPath, strings.NewReader(batch))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("X-Request-Id") != "" {
		t.Fatalf("logging off answered %d with id %q", resp.StatusCode, resp.Header.Get("X-Request-Id"))
	}
	if entries, _ := os.ReadDir(offDir); len(entries) != 0 {
		t.Fatalf("logging off wrote %v", entries)
	}
}

func TestStartRecordCarriesTheRecipePhasesAndTimeToListening(t *testing.T) {
	clock := &fakeClock{t: time.UnixMilli(1_794_890_000_000)}
	l := newPerfLog(t.TempDir(), "rev", clock.now, 4)
	s := &server{perf: l, root: "/home/user/code/tillr", storePath: "/home/user/code/tillr/.tillr/state.db", plannerFlag: filepath.Join(t.TempDir(), "absent")}
	rec := s.startRecord("127.0.0.1:4318", "embedded in this binary", "npm_ci=812,web_build=2300,go_build_view=900,bogus=x,recipe_start_ms=1794889995000")
	phases := rec["recipe_phases_ms"].(map[string]int64)
	if phases["npm_ci"] != 812 || phases["web_build"] != 2300 || phases["go_build_view"] != 900 || len(phases) != 3 {
		t.Fatalf("phases %v", phases)
	}
	if rec["listening_ms"] != int64(5000) || rec["port"] != "4318" || rec["root"] != "/home/user/code/tillr" {
		t.Fatalf("start record %v", rec)
	}
}

func TestWSWindowCountsFramesByKindAndSkipsIdleWindows(t *testing.T) {
	dir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	l := openPerfLog(dir, "rev", clock.now)
	w := newWSWindow(l, nil)
	// An idle window — polls and no frames — writes nothing.
	for i := 0; i < 41; i++ {
		clock.advance(250 * time.Millisecond)
		w.poll(2)
	}
	w.frame([]string{"runlog:run-1", "snapshot"})
	w.frame([]string{"runlog:run-2"})
	clock.advance(3 * time.Second)
	w.poll(7)
	w.close("client gone")
	w.close("again")
	l.close()
	var windows []map[string]any
	for _, line := range readPerfLines(t, filepath.Join(dir, "server-20261117.jsonl")) {
		if line["kind"] == "ws" {
			windows = append(windows, line)
		}
	}
	if len(windows) != 1 {
		t.Fatalf("ws lines %v", windows)
	}
	changed := windows[0]["changed"].(map[string]any)
	if windows[0]["frames"] != float64(2) || changed["runlog"] != float64(2) || changed["snapshot"] != float64(1) ||
		windows[0]["closed"] != "client gone" || windows[0]["poll_ms_max"] != float64(7) || windows[0]["lifetime_ms"] != float64(13250) {
		t.Fatalf("ws window %v", windows[0])
	}
}

func TestVantageCacheServesOneWalkPerFingerprint(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	var runs atomic.Int32
	fp := "tree-1"
	var fpMu sync.Mutex
	index := `{"index":{"refused":false,"config":{"stages":{}},"documents":[{"path":"docs/a.md","questions":[{"id":"OQ-1","state":"open"}]}]}}`
	release := make(chan struct{})
	c := &vantageCache{root: "/r", now: clock.now, maxAge: 10 * time.Minute,
		fingerprint: func(string) (string, error) { fpMu.Lock(); defer fpMu.Unlock(); return fp, nil },
		run: func(context.Context, string, []string) ([]byte, error) {
			runs.Add(1)
			<-release
			return []byte(index), nil
		}}
	// Three concurrent requests share one walk.
	var wg sync.WaitGroup
	caches := make(chan string, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, look, err := c.get(context.Background())
			if err != nil {
				t.Error(err)
			}
			caches <- look.Cache
		}()
	}
	waitFor(t, func() bool { return runs.Load() == 1 })
	close(release)
	wg.Wait()
	close(caches)
	seen := map[string]int{}
	for c := range caches {
		seen[c]++
	}
	if runs.Load() != 1 || seen["miss"] != 1 || seen["shared"]+seen["hit"] != 2 {
		t.Fatalf("runs %d, lookups %v", runs.Load(), seen)
	}
	// Same tree: a hit. A changed tree: a new walk.
	if _, look, _ := c.get(context.Background()); look.Cache != "hit" {
		t.Fatalf("same fingerprint answered %s", look.Cache)
	}
	fpMu.Lock()
	fp = "tree-2"
	fpMu.Unlock()
	if _, look, _ := c.get(context.Background()); look.Cache != "miss" || runs.Load() != 2 {
		t.Fatalf("a changed tree answered %s after %d runs", look.Cache, runs.Load())
	}
	// Past the age bound the same tree is walked again.
	clock.advance(11 * time.Minute)
	if _, look, _ := c.get(context.Background()); look.Cache != "miss" || runs.Load() != 3 {
		t.Fatalf("an old index answered %s after %d runs", look.Cache, runs.Load())
	}
	// The source answers the cached index's questions.
	answer := readQuestions(c.source(context.Background()), "docs/a.md")
	if answer.State != questionsOK && answer.State != designDocMissing {
		t.Fatalf("source answered %v", answer)
	}
}

func TestVantageCacheNeverKeepsAFailureAndNeverReusesACallersCommand(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	var runs atomic.Int32
	fail := true
	c := &vantageCache{root: "/r", now: clock.now, maxAge: time.Hour,
		fingerprint: func(string) (string, error) { return "same", nil },
		run: func(context.Context, string, []string) ([]byte, error) {
			runs.Add(1)
			if fail {
				return nil, errors.New("uvx: not found")
			}
			return []byte(`{"index":{}}`), nil
		}}
	if _, look, err := c.get(context.Background()); err == nil || look.Cache != "error" {
		t.Fatalf("a failing walk answered %v %s", err, look.Cache)
	}
	fail = false
	// A document with no index section is not an index (vantage refuses it), so it is not
	// kept either.
	if _, _, err := c.get(context.Background()); err == nil {
		t.Fatal("a non-index was accepted")
	}
	if runs.Load() != 2 {
		t.Fatalf("runs %d", runs.Load())
	}
	custom := &vantageCache{root: "/r", command: []string{"cat", "stub.json"}, now: clock.now, maxAge: time.Hour,
		fingerprint: func(string) (string, error) { return "same", nil },
		run: func(context.Context, string, []string) ([]byte, error) {
			runs.Add(1)
			return []byte(`{"index":{"refused":false,"documents":[{"path":"a.md","questions":[]}]}}`), nil
		}}
	before := runs.Load()
	custom.get(context.Background())
	custom.get(context.Background())
	if runs.Load()-before != 2 {
		t.Fatalf("a caller-named command was reused: %d runs for 2 requests", runs.Load()-before)
	}
}

func TestVantageFingerprintMovesWithEveryDocumentAndIgnoresHiddenTrees(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/a.md", "# a")
	write("tools/x.go", "package x")
	write(".tillr/lanes/lane-1/notes.md", "hidden")
	fp := func() string {
		got, err := vantageFingerprint(root)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	base := fp()
	write("tools/x.go", "package x // edited")
	write(".tillr/lanes/lane-1/notes.md", "hidden, edited")
	if fp() != base {
		t.Fatal("a non-document or hidden edit moved the fingerprint")
	}
	write("docs/a.md", "# a, answered")
	edited := fp()
	if edited == base {
		t.Fatal("a document edit did not move the fingerprint")
	}
	write("docs/b.md", "# b")
	if fp() == edited {
		t.Fatal("a new document did not move the fingerprint")
	}
	write(".vantage.toml", "[planning]")
	withConfig := fp()
	write(".vantage.toml", "[planning]\nexclude = []")
	if fp() == withConfig {
		t.Fatal("a Vantage configuration edit did not move the fingerprint")
	}
}

func TestPerfLogReaderSummarizesRoutesPagesAndTheBrowserGap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "host-4318")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"v":1,"kind":"start","ts":"2026-11-17T12:00:00Z","host":"desk","addr":"127.0.0.1:4318","root":"/home/user/code/tillr","listening_ms":4958,"recipe_phases_ms":{"go_build_view":816}}`,
		`{"v":1,"kind":"request","ts":"2026-11-17T12:00:01Z","id":"a-1","method":"GET","path":"/api/v1/why","route":"GET /api/v1/why","status":200,"bytes":84000,"wire_bytes":8200,"ms":10467.4,"phases":{"payload_ms":10467.1,"vantage":{"cache":"miss","ms":10223.7},"planner":{"ms":234.3}}}`,
		`{"v":1,"kind":"request","ts":"2026-11-17T12:00:02Z","id":"a-2","method":"GET","path":"/api/v1/why","route":"GET /api/v1/why","status":200,"bytes":84000,"wire_bytes":8200,"ms":226.9,"phases":{"vantage":{"cache":"hit","ms":9.4},"planner":{"ms":208.3}}}`,
		`{"v":1,"kind":"request","ts":"2026-11-17T12:00:02Z","id":"a-3","method":"GET","path":"/api/v1/snapshot","route":"GET /api/v1/snapshot","status":200,"bytes":139000,"wire_bytes":23000,"ms":20}`,
		`not a record`,
		`{"v":1,"kind":"client","ts":"2026-11-17T12:00:03Z","page_views":[{"page":"#/","nav":"load","first_data_ms":300,"settled_ms":10600}],"fetches":[{"path":"/api/v1/why","request_id":"a-1","ms":10500},{"path":"/api/v1/runs/run-1/timeline","request_id":"zz","ms":900}],"long_tasks":[{"id":"longtask","ms":120}],"renders":[{"id":"screen","ms":80,"page":"#/run/{id}"}]}`,
		`{"v":1,"kind":"ws","ts":"2026-11-17T12:00:10Z","frames":15,"window_ms":10000,"changed":{"runlog":9,"snapshot":6},"poll_ms_max":4}`,
		`{"v":1,"kind":"dropped","ts":"2026-11-17T12:00:11Z","count":3}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "server-20261117.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := perfLogFiles(filepath.Dir(dir))
	if err != nil || len(files) != 1 {
		t.Fatalf("files %v %v", files, err)
	}
	s, err := summarizePerfLogs(files, time.Time{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if s.Skipped != 1 || s.Dropped != 3 || s.Records["request"] != 3 || len(s.Starts) != 1 || *s.Starts[0].ListeningMS != 4958 {
		t.Fatalf("summary %+v", s)
	}
	if s.Routes[0].Route != "GET /api/v1/why" || s.Routes[0].Time.Count != 2 || s.Routes[0].Time.Max != 10467.4 || s.Routes[0].WireP50 != 8200 {
		t.Fatalf("routes %+v", s.Routes)
	}
	if s.Slowest[0].ID != "a-1" || len(s.Slowest) != 2 {
		t.Fatalf("slowest %+v", s.Slowest)
	}
	if s.Gap == nil || s.Gap.Count != 1 || s.Gap.Max != round1(10500-10467.4) {
		t.Fatalf("gap %+v", s.Gap)
	}
	if p := s.Pages["#/"]; p.FirstData.P50 != 300 || p.Settled.P50 != 10600 {
		t.Fatalf("pages %+v", s.Pages)
	}
	if _, ok := s.Browser["/api/v1/runs/{id}/timeline"]; !ok {
		t.Fatalf("browser fetches not folded by id: %v", s.Browser)
	}
	if s.WS.Frames != 15 || s.WS.Changed["runlog"] != 9 || s.LongTasks.Count != 1 || s.Renders["#/run/{id} screen"].Max != 80 {
		t.Fatalf("ws %+v long %+v renders %+v", s.WS, s.LongTasks, s.Renders)
	}
	var out bytes.Buffer
	printPerfSummary(&out, s)
	for _, want := range []string{"GET /api/v1/why", "recipe to listening 4958 ms", "browser minus server", "live channel: 15 frames", "vantage 9.4/10223.7"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the printed summary lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := perfLogFiles(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("an absent directory was read")
	}
}

// The server's store handle has ONE connection (store.OpenRead), so whatever a route does
// while its read transaction is open, every other route waits for. A Vantage walk (about 11 s
// on the real tree) and the /why planner (a few hundred ms) must therefore run outside it:
// with either stalled, the snapshot still answers. Each stub stalls until the test releases
// it, so the case is decided by order — the snapshot answered while the slow route had not —
// and never by how long anything took.
func TestASlowIndexOrPlannerDoesNotHoldTheStoreFromOtherRoutes(t *testing.T) {
	stalling := func(t *testing.T, name, answer string) (script, started, release string) {
		dir := t.TempDir()
		started, release = filepath.Join(dir, "started"), filepath.Join(dir, "release")
		body := filepath.Join(dir, "answer.json")
		if err := os.WriteFile(body, []byte(answer), 0o644); err != nil {
			t.Fatal(err)
		}
		script = filepath.Join(dir, name)
		writeScript(t, script, "touch "+started+"\nwhile [ ! -e "+release+" ]; do sleep 0.02; done\ncat "+body+"\n")
		return script, started, release
	}
	whileStalled := func(t *testing.T, h *serveHarness, path, started, release string) {
		t.Helper()
		done := make(chan int, 1)
		go func() {
			resp, err := http.Get(h.http.URL + path)
			if err != nil {
				done <- -1
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			done <- resp.StatusCode
		}()
		waitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
		// The bound is a deadlock's, not a measurement: a snapshot queued behind the stalled
		// route never answers until the release below, which this goroutine has not written.
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(h.http.URL + "/api/v1/snapshot")
		if err != nil {
			os.WriteFile(release, nil, 0o644)
			<-done
			t.Fatalf("the snapshot waited behind the stalled %s: %v", path, err)
		}
		resp.Body.Close()
		select {
		case code := <-done:
			t.Fatalf("%s answered (%d) before its stub was released, so the case proved nothing", path, code)
		default:
		}
		if err := os.WriteFile(release, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if code := <-done; code != http.StatusOK {
			t.Fatalf("%s answered %d once released", path, code)
		}
	}
	t.Run("vantage index", func(t *testing.T) {
		script, started, release := stalling(t, "index.sh", `{"documents":[]}`)
		h := newServeHarnessWithQuestions(t, "", []string{script})
		whileStalled(t, h, "/api/v1/questions", started, release)
	})
	t.Run("why planner", func(t *testing.T) {
		script, started, release := stalling(t, "tillr-drain", `{"event_head_seq":1,"rows":[],"summary":{"held":{}}}`)
		h := newServeHarness(t)
		h.srv.plannerFlag = script
		whileStalled(t, h, "/api/v1/why", started, release)
	})
}

// The reader takes one log path anywhere among its flags, and a relative one is read from
// where the recipe was run rather than from the module directory `go run` works in.
func TestPerfLogTakesAPathAmongItsFlags(t *testing.T) {
	for _, args := range [][]string{
		{"logs/a", "--top", "5", "--json"},
		{"--top", "5", "logs/a", "--json"},
		{"--top=5", "--json", "logs/a"},
	} {
		target, flags := perfLogArgs(args)
		if target != "logs/a" || strings.Join(flags, " ") == "" || strings.Contains(strings.Join(flags, " "), "logs/a") {
			t.Fatalf("%v: target %q flags %v", args, target, flags)
		}
	}
	if target, flags := perfLogArgs([]string{"--since", "1h", "--logs", "/x"}); target != "" || len(flags) != 4 {
		t.Fatalf("flag values taken as the path: %q %v", target, flags)
	}
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs", "host-1")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"v":1,"kind":"request","ts":"2026-11-17T00:00:00Z","route":"GET /api/v1/why","ms":12.5,"status":200}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "server-20261117.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := perfLogFace([]string{"logs/host-1", "--since", "0", "--json", "--from", dir}); code != record.ExitOK {
		t.Fatalf("a relative path named from --from answered %d", code)
	}
	if _, code := perfLogFace([]string{"no-such-dir", "--from", dir}); code != record.ExitNegative {
		t.Fatalf("a missing path answered %d, want the negative exit", code)
	}
}
