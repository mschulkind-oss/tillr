package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// `tillr-view perf-log`: the one command that answers "why is the board slow" from the
// performance log serve writes (perflog.go). It reads a log file, a server's log directory,
// or the directory of every server's directories, and prints per-route and per-page
// percentiles, the slowest requests with their phase breakdown, the gap between what the
// browser waited and what the server spent, the live channel's traffic, and how long each
// start took. It reads only the files it is pointed at.

type perfRecord struct {
	Kind      string                     `json:"kind"`
	TS        string                     `json:"ts"`
	Rev       string                     `json:"rev"`
	ID        string                     `json:"id"`
	Method    string                     `json:"method"`
	Path      string                     `json:"path"`
	Route     string                     `json:"route"`
	Status    int                        `json:"status"`
	Bytes     int64                      `json:"bytes"`
	WireBytes int64                      `json:"wire_bytes"`
	MS        float64                    `json:"ms"`
	Gzip      bool                       `json:"gzip"`
	Phases    map[string]json.RawMessage `json:"phases"`
	Query     string                     `json:"query"`
	// start
	Host         string           `json:"host"`
	Addr         string           `json:"addr"`
	Root         string           `json:"root"`
	ListeningMS  *int64           `json:"listening_ms"`
	RecipePhases map[string]int64 `json:"recipe_phases_ms"`
	// dropped
	Count int `json:"count"`
	// ws
	Frames   int            `json:"frames"`
	WindowMS float64        `json:"window_ms"`
	Changed  map[string]int `json:"changed"`
	PollMax  float64        `json:"poll_ms_max"`
	// client
	PageViews []clientPageView `json:"page_views"`
	Fetches   []clientFetch    `json:"fetches"`
	LongTasks []clientDuration `json:"long_tasks"`
	Renders   []clientDuration `json:"renders"`
}

type clientPageView struct {
	Page        string   `json:"page"`
	Nav         string   `json:"nav"`
	FirstDataMS *float64 `json:"first_data_ms"`
	SettledMS   *float64 `json:"settled_ms"`
	Requests    int      `json:"requests"`
}

type clientFetch struct {
	Path      string   `json:"path"`
	RequestID string   `json:"request_id"`
	MS        float64  `json:"ms"`
	TTFBMS    *float64 `json:"ttfb_ms"`
	Bytes     int64    `json:"bytes"`
	Transfer  *int64   `json:"transfer_bytes"`
	Status    int      `json:"status"`
	Page      string   `json:"page"`
}

type clientDuration struct {
	ID   string  `json:"id"`
	MS   float64 `json:"ms"`
	Page string  `json:"page"`
}

func perfLogFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("perf-log")
	since := fs.Duration("since", 24*time.Hour, "only records newer than this (0: all)")
	top := fs.Int("top", 10, "how many of the slowest requests to list")
	asJSON := fs.Bool("json", false, "print the summary as one JSON object instead of tables")
	logs := fs.String("logs", defaultPerfLogRoot(), "what to read when no file or directory is named: every server's log directory under it")
	from := fs.String("from", "", "the directory a relative log path is named from (`just observatory-perf` passes the one it was run in)")
	target, flags := perfLogArgs(args)
	if code := parseFlags(fs, flags); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if target == "" {
		target = *logs
	} else if !filepath.IsAbs(target) && *from != "" {
		target = filepath.Join(*from, target)
	}
	files, err := perfLogFiles(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view perf-log: %v\n", err)
		return record.Envelope{}, record.ExitNegative
	}
	var cutoff time.Time
	if *since > 0 {
		cutoff = time.Now().Add(-*since)
	}
	summary, err := summarizePerfLogs(files, cutoff, *top)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view perf-log: %v\n", err)
		return record.Envelope{}, record.ExitInternal
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(summary)
	} else {
		printPerfSummary(os.Stdout, summary)
	}
	return record.Envelope{}, record.ExitOK
}

// perfLogArgs separates the one log path a caller may name from the flags, in any order:
// `perf-log --top 5 <dir>` and `perf-log <dir> --top 5` are the same request. A value-taking
// flag written as two words keeps its value.
func perfLogArgs(args []string) (target string, flags []string) {
	valued := map[string]bool{"since": true, "top": true, "logs": true, "from": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			flags = append(flags, args[i:]...)
			break
		}
		if !strings.HasPrefix(a, "-") && target == "" {
			target = a
			continue
		}
		flags = append(flags, a)
		if name := strings.TrimLeft(a, "-"); valued[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return target, flags
}

// defaultPerfLogRoot is where `just observatory` writes, seen from here: the durable
// scratch directory's logs when this is a jail, the checkout's own otherwise.
func defaultPerfLogRoot() string {
	if d := os.Getenv("YOLO_DURABLE_DIR"); d != "" {
		return filepath.Join(d, "tillr-observatory", "logs")
	}
	return filepath.Join(".yolo", "durable", "tillr-observatory", "logs")
}

// perfLogFiles answers the log files under target, oldest first: the file itself, the
// `server-*.jsonl` files of one server's directory, or those of every directory under it.
func perfLogFiles(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("%v: name a log file, a server's log directory, or the logs directory `just observatory` prints at startup", err)
	}
	if !info.IsDir() {
		return []string{target}, nil
	}
	var files []string
	err = filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if !d.IsDir() && strings.HasPrefix(name, "server-") && strings.HasSuffix(name, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s holds no server-*.jsonl: start the board with `just observatory`, which logs there by default", target)
	}
	sort.Strings(files)
	return files, nil
}

type perfDist struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	Max   float64 `json:"max_ms"`
	Total float64 `json:"total_ms"`
}

func distOf(values []float64) perfDist {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	d := perfDist{Count: len(sorted)}
	for _, v := range sorted {
		d.Total += v
	}
	d.P50, d.P95, d.Total = percentile(sorted, 50), percentile(sorted, 95), round1(d.Total)
	if len(sorted) > 0 {
		d.Max = sorted[len(sorted)-1]
	}
	return d
}

type perfRouteSummary struct {
	Route     string   `json:"route"`
	Time      perfDist `json:"time"`
	BytesP50  int64    `json:"bytes_p50"`
	WireP50   int64    `json:"wire_bytes_p50"`
	Errors    int      `json:"errors"`
	GapP50    *float64 `json:"browser_gap_p50_ms,omitempty"`
	GapP95    *float64 `json:"browser_gap_p95_ms,omitempty"`
	TopPhases []string `json:"phases_p50_p95,omitempty"`
}

type perfSlow struct {
	TS     string                     `json:"ts"`
	ID     string                     `json:"id"`
	Path   string                     `json:"path"`
	Status int                        `json:"status"`
	MS     float64                    `json:"ms"`
	Bytes  int64                      `json:"bytes"`
	Phases map[string]json.RawMessage `json:"phases,omitempty"`
}

type perfStart struct {
	TS          string           `json:"ts"`
	Host        string           `json:"host"`
	Addr        string           `json:"addr"`
	Root        string           `json:"root"`
	Rev         string           `json:"rev"`
	ListeningMS *int64           `json:"listening_ms"`
	Phases      map[string]int64 `json:"recipe_phases_ms,omitempty"`
}

type perfSummary struct {
	Files     []string            `json:"files"`
	From      string              `json:"from"`
	To        string              `json:"to"`
	Records   map[string]int      `json:"records"`
	Skipped   int                 `json:"skipped_lines"`
	Dropped   int                 `json:"dropped"`
	Starts    []perfStart         `json:"starts"`
	Routes    []perfRouteSummary  `json:"routes"`
	Slowest   []perfSlow          `json:"slowest"`
	Pages     map[string]perfPage `json:"pages"`
	Browser   map[string]perfDist `json:"browser_fetches"`
	WS        perfWS              `json:"ws"`
	LongTasks perfDist            `json:"long_tasks"`
	Renders   map[string]perfDist `json:"slow_renders"`
	Gap       *perfDist           `json:"browser_gap"`
	Phases    map[string]perfDist `json:"phases"`
}

type perfPage struct {
	FirstData perfDist `json:"first_data"`
	Settled   perfDist `json:"settled"`
}

type perfWS struct {
	Windows   int            `json:"windows"`
	Frames    int            `json:"frames"`
	Seconds   float64        `json:"seconds"`
	Changed   map[string]int `json:"changed"`
	PollMaxMS float64        `json:"poll_ms_max"`
}

// phaseMS reads one phase's duration: a bare number, or an object's `ms`.
func phaseMS(raw json.RawMessage) (float64, bool) {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var obj struct {
		MS *float64 `json:"ms"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.MS != nil {
		return *obj.MS, true
	}
	return 0, false
}

func summarizePerfLogs(files []string, cutoff time.Time, top int) (perfSummary, error) {
	out := perfSummary{Files: files, Records: map[string]int{}, Pages: map[string]perfPage{},
		Browser: map[string]perfDist{}, Renders: map[string]perfDist{}, Phases: map[string]perfDist{},
		WS: perfWS{Changed: map[string]int{}}}
	type routeAcc struct {
		ms     []float64
		bytes  []float64
		wire   []float64
		errors int
		gaps   []float64
		phases map[string][]float64
	}
	routes := map[string]*routeAcc{}
	byID := map[string]perfRecord{}
	var requests []perfRecord
	var fetches []clientFetch
	pageFirst, pageSettled := map[string][]float64{}, map[string][]float64{}
	browser := map[string][]float64{}
	renders := map[string][]float64{}
	var longTasks []float64
	phases := map[string][]float64{}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return out, err
		}
		r := bufio.NewReaderSize(f, 1<<20)
		for {
			line, err := r.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				var rec perfRecord
				if json.Unmarshal(line, &rec) != nil || rec.Kind == "" {
					out.Skipped++
				} else if ts, terr := time.Parse(time.RFC3339Nano, rec.TS); terr == nil && (cutoff.IsZero() || !ts.Before(cutoff)) {
					if out.From == "" || rec.TS < out.From {
						out.From = rec.TS
					}
					if rec.TS > out.To {
						out.To = rec.TS
					}
					out.Records[rec.Kind]++
					switch rec.Kind {
					case "request":
						requests = append(requests, rec)
						if rec.ID != "" {
							byID[rec.ID] = rec
						}
					case "start":
						out.Starts = append(out.Starts, perfStart{rec.TS, rec.Host, rec.Addr, rec.Root, rec.Rev, rec.ListeningMS, rec.RecipePhases})
					case "dropped":
						out.Dropped += rec.Count
					case "ws":
						out.WS.Windows++
						out.WS.Frames += rec.Frames
						out.WS.Seconds += rec.WindowMS / 1000
						for k, v := range rec.Changed {
							out.WS.Changed[k] += v
						}
						if rec.PollMax > out.WS.PollMaxMS {
							out.WS.PollMaxMS = rec.PollMax
						}
					case "client":
						for _, pv := range rec.PageViews {
							if pv.FirstDataMS != nil {
								pageFirst[pv.Page] = append(pageFirst[pv.Page], *pv.FirstDataMS)
							}
							if pv.SettledMS != nil {
								pageSettled[pv.Page] = append(pageSettled[pv.Page], *pv.SettledMS)
							}
						}
						fetches = append(fetches, rec.Fetches...)
						for _, lt := range rec.LongTasks {
							longTasks = append(longTasks, lt.MS)
						}
						for _, rd := range rec.Renders {
							renders[rd.Page+" "+rd.ID] = append(renders[rd.Page+" "+rd.ID], rd.MS)
						}
					}
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return out, err
			}
		}
		f.Close()
	}
	for _, rec := range requests {
		route := rec.Route
		if route == "" {
			route = rec.Method + " " + rec.Path
		}
		acc := routes[route]
		if acc == nil {
			acc = &routeAcc{phases: map[string][]float64{}}
			routes[route] = acc
		}
		acc.ms = append(acc.ms, rec.MS)
		acc.bytes = append(acc.bytes, float64(rec.Bytes))
		acc.wire = append(acc.wire, float64(rec.WireBytes))
		if rec.Status >= 500 {
			acc.errors++
		}
		for name, raw := range rec.Phases {
			if ms, ok := phaseMS(raw); ok {
				acc.phases[name] = append(acc.phases[name], ms)
				phases[name] = append(phases[name], ms)
			}
		}
	}
	var gaps []float64
	for _, f := range fetches {
		key := pathRoute(f.Path)
		browser[key] = append(browser[key], f.MS)
		if server, ok := byID[f.RequestID]; ok && f.RequestID != "" {
			gap := round1(f.MS - server.MS)
			gaps = append(gaps, gap)
			if acc := routes[server.Route]; acc != nil {
				acc.gaps = append(acc.gaps, gap)
			}
		}
	}
	for route, acc := range routes {
		rs := perfRouteSummary{Route: route, Time: distOf(acc.ms), Errors: acc.errors,
			BytesP50: int64(distOf(acc.bytes).P50), WireP50: int64(distOf(acc.wire).P50)}
		if len(acc.gaps) > 0 {
			g := distOf(acc.gaps)
			rs.GapP50, rs.GapP95 = &g.P50, &g.P95
		}
		var names []string
		for name := range acc.phases {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			d := distOf(acc.phases[name])
			rs.TopPhases = append(rs.TopPhases, fmt.Sprintf("%s %.1f/%.1f", name, d.P50, d.P95))
		}
		out.Routes = append(out.Routes, rs)
	}
	// The order a reader wants: where the server's time went in total, most first.
	sort.Slice(out.Routes, func(i, j int) bool { return out.Routes[i].Time.Total > out.Routes[j].Time.Total })
	sort.SliceStable(requests, func(i, j int) bool { return requests[i].MS > requests[j].MS })
	for i := 0; i < len(requests) && i < top; i++ {
		rec := requests[i]
		out.Slowest = append(out.Slowest, perfSlow{rec.TS, rec.ID, rec.Path, rec.Status, rec.MS, rec.Bytes, rec.Phases})
	}
	for page, v := range pageFirst {
		out.Pages[page] = perfPage{FirstData: distOf(v), Settled: distOf(pageSettled[page])}
	}
	for page, v := range pageSettled {
		if _, ok := out.Pages[page]; !ok {
			out.Pages[page] = perfPage{Settled: distOf(v)}
		}
	}
	for k, v := range browser {
		out.Browser[k] = distOf(v)
	}
	for k, v := range renders {
		out.Renders[k] = distOf(v)
	}
	for k, v := range phases {
		out.Phases[k] = distOf(v)
	}
	out.LongTasks = distOf(longTasks)
	if len(gaps) > 0 {
		g := distOf(gaps)
		out.Gap = &g
	}
	return out, nil
}

// pathRoute folds a browser's fetch path onto a route-like key, so one run's id does not
// make every fetch its own row: the id segments after items/, runs/, landings/ and checks/
// become {id}.
func pathRoute(path string) string {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		switch parts[i-1] {
		case "items", "runs", "landings", "checks", "transcript":
			if parts[i] != "" {
				parts[i] = "{id}"
			}
		}
	}
	return strings.Join(parts, "/")
}

func printPerfSummary(w io.Writer, s perfSummary) {
	fmt.Fprintf(w, "observatory performance log: %d file(s), %s .. %s\n", len(s.Files), s.From, s.To)
	var kinds []string
	for k := range s.Records {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var counts []string
	for _, k := range kinds {
		counts = append(counts, fmt.Sprintf("%s %d", k, s.Records[k]))
	}
	fmt.Fprintf(w, "records: %s; dropped %d; unreadable lines %d\n", strings.Join(counts, ", "), s.Dropped, s.Skipped)
	if len(s.Starts) > 0 {
		fmt.Fprintln(w, "\nstarts (newest last):")
		for _, st := range s.Starts {
			listening := "?"
			if st.ListeningMS != nil {
				listening = fmt.Sprintf("%d ms", *st.ListeningMS)
			}
			var ph []string
			for _, name := range sortedKeys(st.Phases) {
				ph = append(ph, fmt.Sprintf("%s %d", name, st.Phases[name]))
			}
			fmt.Fprintf(w, "  %s %s %s rev %.12s: recipe to listening %s [%s]\n", st.TS, st.Host, st.Addr, st.Rev, listening, strings.Join(ph, ", "))
		}
	}
	fmt.Fprintln(w, "\nserver time per route (ms), most total first:")
	fmt.Fprintf(w, "  %-42s %6s %8s %8s %8s %9s %10s %10s  %s\n", "route", "count", "p50", "p95", "max", "total_s", "bytes_p50", "wire_p50", "phases p50/p95 (ms)")
	for _, r := range s.Routes {
		gap := ""
		if r.GapP50 != nil {
			gap = fmt.Sprintf("  browser gap p50 %.1f p95 %.1f", *r.GapP50, *r.GapP95)
		}
		fmt.Fprintf(w, "  %-42s %6d %8.1f %8.1f %8.1f %9.1f %10d %10d  %s%s\n", r.Route, r.Time.Count, r.Time.P50, r.Time.P95, r.Time.Max,
			r.Time.Total/1000, r.BytesP50, r.WireP50, strings.Join(r.TopPhases, ", "), gap)
	}
	if len(s.Slowest) > 0 {
		fmt.Fprintln(w, "\nslowest requests:")
		for _, r := range s.Slowest {
			var ph []string
			for _, name := range sortedRawKeys(r.Phases) {
				ph = append(ph, name+"="+compactJSON(r.Phases[name]))
			}
			fmt.Fprintf(w, "  %8.1f ms %s %s %d %d B  %s\n", r.MS, r.TS, r.Path, r.Status, r.Bytes, strings.Join(ph, " "))
		}
	}
	if len(s.Pages) > 0 {
		fmt.Fprintln(w, "\nbrowser page views (ms from navigation or hash change):")
		fmt.Fprintf(w, "  %-24s %6s %10s %10s %10s %10s %10s\n", "page", "views", "data p50", "data p95", "data max", "settle p50", "settle p95")
		for _, page := range sortedPageKeys(s.Pages) {
			p := s.Pages[page]
			fmt.Fprintf(w, "  %-24s %6d %10.1f %10.1f %10.1f %10.1f %10.1f\n", page, max(p.FirstData.Count, p.Settled.Count), p.FirstData.P50, p.FirstData.P95, p.FirstData.Max, p.Settled.P50, p.Settled.P95)
		}
	}
	if len(s.Browser) > 0 {
		fmt.Fprintln(w, "\nbrowser fetches (ms as the browser waited):")
		for _, k := range sortedDistKeys(s.Browser) {
			d := s.Browser[k]
			fmt.Fprintf(w, "  %-42s %6d p50 %8.1f p95 %8.1f max %8.1f\n", k, d.Count, d.P50, d.P95, d.Max)
		}
		if s.Gap != nil {
			fmt.Fprintf(w, "  browser minus server, joined by request id: p50 %.1f p95 %.1f max %.1f ms over %d fetches\n", s.Gap.P50, s.Gap.P95, s.Gap.Max, s.Gap.Count)
		}
	}
	if s.LongTasks.Count > 0 || len(s.Renders) > 0 {
		fmt.Fprintf(w, "\nbrowser main thread: %d long tasks (p50 %.1f, max %.1f ms)\n", s.LongTasks.Count, s.LongTasks.P50, s.LongTasks.Max)
		for _, k := range sortedDistKeys(s.Renders) {
			d := s.Renders[k]
			fmt.Fprintf(w, "  slow render %-30s %4d p50 %8.1f max %8.1f ms\n", k, d.Count, d.P50, d.Max)
		}
	}
	if s.WS.Windows > 0 {
		var ch []string
		for _, k := range sortedKeysInt(s.WS.Changed) {
			ch = append(ch, fmt.Sprintf("%s %d", k, s.WS.Changed[k]))
		}
		perMin := 0.0
		if s.WS.Seconds > 0 {
			perMin = float64(s.WS.Frames) / s.WS.Seconds * 60
		}
		fmt.Fprintf(w, "\nlive channel: %d frames in %.0f s of active windows (%.1f/min); named %s; slowest poll %.1f ms\n",
			s.WS.Frames, s.WS.Seconds, perMin, strings.Join(ch, ", "), s.WS.PollMaxMS)
	}
}

func compactJSON(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func sortedKeys(m map[string]int64) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysInt(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedPageKeys(m map[string]perfPage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedDistKeys(m map[string]perfDist) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
