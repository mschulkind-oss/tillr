package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The server's answer envelope: every response carries the cursor the moment it was read
// at (design §4.1), so a client can tell two responses apart and never renders one
// request's items beside another request's runs.
type serverSnapshot struct {
	StorePath        string    `json:"store_path"`
	SchemaVersion    int       `json:"schema_version"`
	EventHeadSeq     int64     `json:"event_head_seq"`
	TraceWatermarkNS *int64    `json:"trace_watermark_ns"`
	GeneratedAt      time.Time `json:"generated_at"`
	// Build is the program that answered. It is on every response, refusal included,
	// because "is this the tree I am reading" is a question a stale server is the least
	// likely one to answer any other way (build.go).
	Build buildStamp `json:"build"`
	// VantageBaseURL is the configured Vantage base URL, and absent when the operator
	// configured none — which is a board that draws the link and says it goes nowhere,
	// not a board that hides it (design §8.2).
	VantageBaseURL string `json:"vantage_base_url,omitempty"`
}

// apiResponse is one successful answer.
type apiResponse struct {
	Snapshot serverSnapshot `json:"snapshot"`
	Data     any            `json:"data"`
}

// apiFailure is a refusal: the same cursor, and an error a client can branch on. The body
// is the same shape whatever went wrong — `{"error": {"code", "message"}}` beside the
// cursor — so a client reads one body and switches on the code, which is where its
// remedy lives (apierror.go).
type apiFailure struct {
	Snapshot serverSnapshot `json:"snapshot"`
	Error    apiErrorBody   `json:"error"`
	// status is the HTTP status this refusal answers with. It is not serialized: the code
	// is what a client branches on, and this is its status's one implementation.
	status int
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// server is the read-only face of the store. It holds one read-only handle and one
// trace index handle for its lifetime: opening per request would open a second reader of
// a live store on every refresh, and neither handle can write.
type server struct {
	openMu    sync.Mutex
	storePath string
	store     *store.Store
	schema    int
	// vantageBase is the configured base URL of the Vantage that serves this repository,
	// or empty when the operator configured none. It is on the response envelope rather
	// than in a document payload because the link belongs to the board, not to one route:
	// a second item that links into Vantage reads the same setting (design §8.2).
	vantageBase string
	// problem is the store's condition when it is not one this face can read, and nil
	// when it is. Every API route answers it rather than the route's own payload, so a
	// client is told which condition it is looking at instead of seeing an empty board.
	problem *storeVerdict
	obs     *observations
	root    string
	// questionsCmd is the program that answers Vantage's index. The document route reads
	// the index through a fresh reader per request, so an answer written while the board
	// runs is seen on the next read rather than after a restart.
	questionsCmd []string
	policyRef    string
	offeredLanes string
	// plannerFlag is the `--tillr-drain` the caller named, or "" to resolve the planner
	// beside this executable (siblings.go). It is re-resolved per request, so a planner
	// built after the server started is found without a restart.
	plannerFlag string
	// web is the built front end, or nil when this binary carries none. It is served at
	// `/` and never at `/api/v1/`, so the API's route set stays closed.
	web fs.FS
	// build is this program's stamp, reported on every response (build.go).
	build buildStamp
	// checkout compares that build with the served checkout's HEAD (build.go).
	checkout *checkoutWatch
	// perf is the performance log (perflog.go), nil when it is off.
	perf *perfLog
	// vantage is the one cached walk of Vantage's index every question-reading route and
	// the planner share (vantagecache.go).
	vantage *vantageCache
}

// openWebAssets answers the front end to serve: an explicit `--web-dist` directory wins,
// and otherwise the assets embedded under the observatory build tag. A directory that
// does not exist is refused rather than silently serving no UI, because the operator
// asked for it by name.
func openWebAssets(webDist, uiAt string) (fs.FS, string, int) {
	if webDist != "" {
		info, err := os.Stat(webDist)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-view: --web-dist %s: %v\n", webDist, err)
			return nil, "", record.ExitUsage
		}
		if !info.IsDir() {
			fmt.Fprintf(os.Stderr, "tillr-view: --web-dist %s is not a directory\n", webDist)
			return nil, "", record.ExitUsage
		}
		return os.DirFS(webDist), webDist, record.ExitOK
	}
	if embedded := webAssets(); embedded != nil {
		return embedded, "embedded in this binary", record.ExitOK
	}
	if uiAt != "" {
		// The UI is real and this process is not serving it: `just observatory-dev`'s Vite
		// server proxies every /api call here and serves the board itself. Answering "none"
		// would be true of this process and false of the board the reader is looking at.
		return nil, "ui served by " + uiAt, record.ExitOK
	}
	return nil, "none: build the front end and pass --web-dist, or build with -tags observatory", record.ExitOK
}

// frontEndLabel is the stamp's short spelling of where the UI came from: `none`, `embedded`
// or `dir:<path>`. The sentence openWebAssets returns is for a log line and a remedy, not a
// field: a stamp that repeats a whole remedy is a stamp nobody can compare.
func frontEndLabel(web fs.FS, note string) string {
	// The note outranks the handle: a `--ui-at` server deliberately holds no fs (its UI is
	// somebody else's process), so "no fs" is not the same answer as "no UI anywhere".
	if strings.HasPrefix(note, "ui served by ") {
		return strings.TrimPrefix(note, "ui served by ")
	}
	if web == nil {
		return "none"
	}
	if note == "embedded in this binary" {
		return "embedded"
	}
	return "dir:" + note
}

// serveFace is `tillr-view serve`: the read-only HTTP API over one store.
func serveFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("serve")
	storePath := fs.String("store", "", "the store to serve (required)")
	loopDir := fs.String("loop-dir", "", "the loop's record directory (default: <store dir>/../loop)")
	laneRoot := fs.String("lane-root", "", "where the lane directories live: each run's stream log and lane-copied inputs (default: <store dir>/lanes)")
	index := fs.String("trace-index", defaultTraceIndex(), "the tillr-trace index to read (default: $TILLR_TRACE_ROOT or the state home)")
	addr := fs.String("addr", "127.0.0.1:4318", "the address to listen on")
	root := fs.String("root", ".", "the directory a relative design_doc resolves against")
	vantageURL := fs.String("vantage-url", "", "the Vantage base URL the board's document link is built from (default: $TILLR_VANTAGE_BASE_URL)")
	webDist := fs.String("web-dist", "", "a directory of built front-end assets to serve (default: the assets embedded under the observatory build tag, if any)")
	uiAt := fs.String("ui-at", "", "this process serves the API only and another one has the UI; a short name for it, so the board can say where its own interface is (e.g. vite)")
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	policyRef := fs.String("policy", "", "the loop's policy file for /why; absent means no-policy")
	lanesArg := fs.String("lanes", "", "comma-separated lanes offered by the loop for /why (default: all store lanes)")
	perfDir := fs.String("perf-log", os.Getenv("TILLR_VIEW_PERF_LOG"), "a directory for the performance log: a JSON line per request, WebSocket window and browser report, a startup record and a periodic summary (`off` or empty: no log; default $TILLR_VIEW_PERF_LOG). `just observatory` names one under the checkout; `tillr-view perf-log` reads it")
	perfStartup := fs.String("perf-startup", os.Getenv("TILLR_VIEW_PERF_STARTUP"), "the recipe's startup phases for the log's startup record, as name=milliseconds pairs separated by commas; `recipe_start_ms=<unix ms>` dates the recipe's start, so the record can say how long the recipe took to listen")
	plannerFlag := fs.String("tillr-drain", "", "the tillr-drain whose `why` answers /why (default: the one beside this executable, then PATH); it must be this server's build")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *storePath == "" {
		return record.Envelope{}, usagef("serve needs --store <path>")
	}
	ctx := context.Background()
	srv := newServer(ctx, *storePath, *loopDir, *laneRoot, *index, *root, vantageBaseURL(*vantageURL), commandFields(*questionsCmd))
	defer srv.close()
	srv.policyRef = *policyRef
	srv.offeredLanes = *lanesArg
	srv.plannerFlag = *plannerFlag
	web, webNote, code := openWebAssets(*webDist, *uiAt)
	if code != record.ExitOK {
		return record.Envelope{}, code
	}
	srv.web = web
	srv.build = buildInfo(frontEndLabel(web, webNote))
	srv.perf = openPerfLog(*perfDir, srv.build.Revision, time.Now)
	defer srv.perf.close()
	fmt.Fprintf(os.Stderr, "tillr-view serve: ui: %s\n", webNote)
	fmt.Fprintf(os.Stderr, "tillr-view serve: build: %s\n", srv.build)
	if program, err := resolvePlanner(srv.plannerFlag); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view serve: planner: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "tillr-view serve: planner: %s\n", program)
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		if addrInUse(err) {
			// The holder is asked who it is, because "address already in use" is the line
			// that left an older process's board on the owner's screen (holder.go).
			verdict := describeHolder(ctx, *addr, srv.build, srv.storePath)
			fmt.Fprintf(os.Stderr, "tillr-view serve: %s\n", verdict.Message)
			if verdict.Kind == "same" {
				return record.Envelope{}, record.ExitOK
			}
			return record.Envelope{}, record.ExitUsage
		}
		fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
		return record.Envelope{}, record.ExitInternal
	}
	// Printed once the port is this process's: before, a refused bind printed it too.
	fmt.Fprintf(os.Stderr, "tillr-view serve: %s on http://%s/api/v1/snapshot\n", srv.storePath, *addr)
	if srv.perf != nil {
		srv.perf.emit("start", srv.startRecord(*addr, webNote, *perfStartup))
		fmt.Fprintf(os.Stderr, "tillr-view serve: perf log: %s (read it with `tillr-view perf-log %s`)\n", srv.perf.path(), srv.perf.dir)
	} else {
		fmt.Fprintf(os.Stderr, "tillr-view serve: perf log: off\n")
	}
	httpServer := &http.Server{Handler: srv.routes(), ReadHeaderTimeout: 5 * time.Second}
	stopOnSignal(httpServer)
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
		return record.Envelope{}, record.ExitInternal
	}
	return record.Envelope{}, record.ExitOK
}

// defaultTraceIndex is where tillr-trace keeps its index: its own default, not a path
// this face invents.
func defaultTraceIndex() string {
	if root := os.Getenv("TILLR_TRACE_ROOT"); root != "" {
		return filepath.Join(root, "index.db")
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "tillr-trace", "v1", "index.db")
}

// newServer opens the store once successfully, retrying failed opens on API requests.
// The store is opened through store.OpenRead, so the
// process cannot write to it even if a handler tried: this whole face exists because a
// second writer of the store is the failure to prevent (design §3.3).
//
// A store it cannot read is a verdict the routes answer rather than a start-up refusal: the
// client's whole complaint about these states is that they look alike, and the only place
// they can be told apart is in an answer. The diagnosis is printed to stderr as well, so an
// operator who started the face by hand still reads which condition they named.
func newServer(ctx context.Context, storePath, loopDir, laneRoot, index, root, vantageBase string, questionsCmd []string) *server {
	srv := &server{storePath: storePath, root: root, vantageBase: vantageBase, questionsCmd: questionsCmd,
		checkout: &checkoutWatch{root: root, git: runGit, now: time.Now}, vantage: newVantageCache(root, questionsCmd)}
	srv.openStore(ctx)
	if srv.problem != nil {
		fmt.Fprintf(os.Stderr, "tillr-view serve: %s; every API route answers %d %s\n",
			srv.problem.note, statusFor(srv.problem.code), srv.problem.code)
	}
	if loopDir == "" {
		loopDir = filepath.Join(filepath.Dir(storePath), "loop")
	}
	// The lane root defaults beside the store, exactly as the lanes and running faces
	// default it. Until 2026-11-17 serve alone defaulted it to "", so every lane's stream
	// log was opened at the empty path: the Lanes panel answered `open : no such file or
	// directory` and "action unrecorded" for a drain whose logs were all there. Both
	// defaults are relative to the store the caller named, so the same tree read from
	// another mount (the host's path for a container's `/workspace`) resolves the same files.
	if laneRoot == "" {
		laneRoot = filepath.Join(filepath.Dir(storePath), "lanes")
	}
	srv.obs = &observations{loopDir: loopDir, laneRoot: laneRoot, trace: openTraceIndex(ctx, index)}
	return srv
}

// currentBuild is the build stamp with its comparison against the served checkout.
func (s *server) currentBuild() buildStamp {
	b := s.build
	b.Checkout = s.checkout.read(s.build)
	return b
}

// openStore is called at startup or under openMu exclusively. Only a successful
// read-only handle is retained; a refusal is re-measured on the next API request.
func (s *server) openStore(ctx context.Context) {
	r, err := store.OpenRead(ctx, s.storePath)
	if err != nil {
		s.problem = storeVerdictFor(s.storePath, err)
		return
	}
	version, err := r.SchemaVersion()
	if err != nil {
		r.Close()
		s.problem = storeVerdictFor(s.storePath, err)
		return
	}
	if !schemaSupported(version) {
		r.Close()
		s.problem = schemaVerdict(version)
		return
	}
	s.store, s.schema, s.problem = r, version, nil
}

func (s *server) close() {
	if s.obs != nil && s.obs.trace != nil {
		s.obs.trace.Close()
	}
	if s.store != nil {
		s.store.Close()
	}
}

// routes is the closed route set of design §4.1. Everything else is a 404, and every
// route is a GET: this face has no write path to expose.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/snapshot", s.get(s.snapshotPayload))
	mux.HandleFunc("GET /api/v1/why", s.vantage.prefetch(s.getOutsideTx(s.whyPayload)))
	mux.HandleFunc("GET /api/v1/waiting", s.vantage.prefetch(s.get(s.waitingPayload)))
	mux.HandleFunc("GET /api/v1/passes", s.get(s.passesPayload))
	mux.HandleFunc("GET /api/v1/landings", s.get(s.landingsPayload))
	mux.HandleFunc("GET /api/v1/lanes", s.get(s.lanesPayload))
	mux.HandleFunc("GET /api/v1/landings/{id}", s.get(s.landingsPayload))
	mux.HandleFunc("GET /api/v1/perf/checks", s.get(s.performanceChecksPayload))
	mux.HandleFunc("GET /api/v1/perf/items", s.get(s.performanceItemsPayload))
	mux.HandleFunc("GET /api/v1/checks/{id}", s.get(s.checkExecutionsPayload))
	mux.HandleFunc("GET /api/v1/items/{id}", s.get(s.itemPayload))
	// The item's design document as metadata: its frontmatter, the count of open questions
	// a parse found, its path, and the section the item names. It carries no prose — the
	// board does not render the document, and Vantage owns that rendering (design §8.3).
	// It is a read of one file the store names, not a second source of truth.
	mux.HandleFunc("GET /api/v1/items/{id}/doc", s.vantage.prefetch(s.get(s.documentPayload)))
	// The board's one place for the questions waiting on the owner: every item whose design
	// document still asks something, with the question ids and the document each link is
	// built from. Vantage's index is reused only while the tree's Markdown is unchanged
	// (vantagecache.go), so an answer written there leaves the list on the next read rather
	// than after a restart.
	mux.HandleFunc("GET /api/v1/questions", s.vantage.prefetch(s.get(s.questionsPayload)))
	mux.HandleFunc("GET /api/v1/designs", s.get(s.designsPayload))
	mux.HandleFunc("GET /api/v1/runs/{id}", s.get(s.runPayload))
	mux.HandleFunc("GET /api/v1/runs/{id}/exchanges", s.get(s.exchangesPayload))
	mux.HandleFunc("GET /api/v1/runs/{id}/timeline", s.get(s.timelinePayload))
	mux.HandleFunc("GET /api/v1/runs/{id}/trace", s.get(s.tracePayload))
	mux.HandleFunc("GET /api/v1/runs/{id}/gate", s.get(s.gatePayload))
	// The transcript: what the run's own harness stream and subprocess record say it did,
	// paged. It is not in the snapshot for the same reason it is paged at all — a run's log
	// is megabytes — and the single-entry child answers one entry of it whole, which is what
	// a truncated body points at.
	mux.HandleFunc("GET /api/v1/runs/{id}/transcript", s.get(s.transcriptPayload))
	mux.HandleFunc("GET /api/v1/runs/{id}/transcript/{seq}", s.get(s.transcriptEntryPayload))
	mux.HandleFunc("GET /api/v1/runs/{id}/artifacts/{kind}", s.get(s.artifactPayload))
	// The live channel: an upgrade, then an invalidation frame per change (design §4.2).
	// It reads through the same read-only handle as the routes above, so watching costs
	// the store nothing a refresh does not.
	mux.HandleFunc("GET /api/v1/ws", s.wsHandler())
	// The browser's account of the page loads it timed (perfhttp.go). It is the one route
	// that is not a GET, and it writes only to the performance log, never to the store.
	mux.HandleFunc("POST "+clientTimingPath, s.clientTimingHandler)
	// An unknown `/api/v1/` path is a caller's mistake, and it must not fall through to
	// the index page: a client that receives HTML where it expected JSON would read a
	// routing bug as a data bug.
	mux.HandleFunc("GET /api/v1/", func(w http.ResponseWriter, req *http.Request) {
		f := failure(codeUnknownRoute, "%q is not a route this face serves: the routes are /api/v1/lanes, /api/v1/why, /api/v1/snapshot, /api/v1/waiting, /api/v1/passes, /api/v1/questions, /api/v1/perf/checks, /api/v1/perf/items, /api/v1/checks/{id}, /api/v1/items/{id}, /api/v1/runs/{id} and its children, /api/v1/landings and /api/v1/landings/{id}, and /api/v1/ws", req.URL.Path)
		f.Snapshot = serverSnapshot{StorePath: s.storePath, SchemaVersion: s.schema, GeneratedAt: time.Now().UTC(), Build: s.build, VantageBaseURL: s.vantageBase}
		writeJSON(w, f.status, *f)
	})
	mux.Handle("GET /", s.webHandler())
	return s.instrument(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if (req.Method != http.MethodGet && req.Method != http.MethodHead) || !strings.HasPrefix(req.URL.Path, "/api/v1/") {
			mux.ServeHTTP(w, req)
			return
		}
		s.openMu.Lock()
		if s.store == nil {
			s.openStore(req.Context())
		}
		if s.problem != nil {
			s.storeProblem(w, req)
			s.openMu.Unlock()
			return
		}
		// A successful handle and its schema never change. Release the publication
		// lock before serving, especially before a long-lived WebSocket upgrade.
		s.openMu.Unlock()
		mux.ServeHTTP(w, req)
	}))
}

// storeProblem answers every API route while the store is not one this face can read.
func (s *server) storeProblem(w http.ResponseWriter, req *http.Request) {
	f := failure(s.problem.code, "%s", s.problem.note)
	f.Snapshot = serverSnapshot{StorePath: s.storePath, SchemaVersion: s.schema, GeneratedAt: time.Now().UTC(), Build: s.build, VantageBaseURL: s.vantageBase}
	writeJSON(w, f.status, *f)
}

// noFrontEndNote is what this face answers when it has no UI to serve. It names both
// remedies and both recipes, because the failure it reports is almost always a binary
// built the other way rather than a missing file: `just observatory <port>` embeds the
// front end, `just tillr-build` embeds it too, and a view built with neither has none.
const noFrontEndNote = "this binary carries no built front end: " +
	"pass --web-dist <dir> pointing at a `npm run build` output, " +
	"or serve the embedded form with `just observatory <port>` " +
	"(a `-tags observatory` build), and note that `just tillr-build` embeds it only when " +
	"tools/tillr-view/web/dist already exists"

// noFrontEndNoteFor is noFrontEndNote plus the two facts that decide which remedy applies:
// the program answering, and the revision it was built from. The pack delivers `tillr-view`
// as Go source with no built front end, so a run from PATH answers this every time — the
// message is correct and the reader still cannot tell that from a checkout whose `dist/`
// was never built, which is what the executable and the build stamp are for.
func (s *server) noFrontEndNoteFor() string {
	if s.build.FrontEnd != "none" {
		return "this process serves the API only; the board's UI is served by " + s.build.FrontEnd +
			"\n\nserving: " + s.build.String()
	}
	return noFrontEndNote + "\n\nserving: " + s.build.String() +
		"\nthe pack-delivered `tillr-view` on PATH never carries a front end: the UI comes from `just observatory <port>` (or `just observatory-dev <port>`) in the checkout"
}

// webHandler serves the built front end. Every path it does not carry an asset for
// answers index.html, so a view URL is a URL an operator can paste; an asset path that
// genuinely does not exist is still a 404, because inventing HTML for a missing script is
// a build bug rendered as a page.
func (s *server) webHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if s.web == nil {
			http.Error(w, s.noFrontEndNoteFor(), http.StatusNotFound)
			return
		}
		files := http.FileServerFS(s.web)
		// Vite names every built asset by its content hash, so an asset URL never changes
		// meaning and the browser may keep it; the page that names them must be revalidated
		// on every load, or a rebuilt board would load the previous build's scripts.
		w.Header().Set("Cache-Control", "no-cache")
		if req.URL.Path == "/" {
			files.ServeHTTP(w, req)
			return
		}
		if _, err := fs.Stat(s.web, strings.TrimPrefix(path.Clean(req.URL.Path), "/")); err == nil {
			if strings.HasPrefix(req.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, req)
			return
		}
		if path.Ext(req.URL.Path) != "" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		index, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, s.noFrontEndNoteFor(), http.StatusNotFound)
			return
		}
		_, _ = w.Write(index)
	})
}

// payloadFunc answers one route's data. It runs inside a single read transaction, and its
// answer is wrapped in the cursor that transaction was taken at.
type payloadFunc func(r *store.Reader, req *http.Request) (any, *apiFailure)

// get wraps one payload in the snapshot envelope: the read transaction, the cursor and
// the JSON. Pinning one ReadTx per request is what makes "the two items and their runs"
// one moment rather than two (design §3.3).
func (s *server) get(fn payloadFunc) http.HandlerFunc { return s.serveRead(fn, false) }

// getOutsideTx is get for a route whose answer is not read through this server's
// transaction: /why, which the drain plans in its own process and its own pinned read. The
// wrapper's transaction reads only the cursor and is closed before fn runs, so the planner's
// few hundred milliseconds do not hold the server's one store connection (store.OpenRead)
// and every other pane's read does not wait for them. fn is handed a nil Reader.
func (s *server) getOutsideTx(fn payloadFunc) http.HandlerFunc { return s.serveRead(fn, true) }

func (s *server) serveRead(fn payloadFunc, outside bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		var (
			data   any
			cursor serverSnapshot
			fail   *apiFailure
		)
		start := time.Now()
		err := s.store.ReadTx(ctx, func(r *store.Reader) error {
			head, err := r.EventHeadSeq()
			if err != nil {
				return err
			}
			cursor = serverSnapshot{
				StorePath:        s.storePath,
				SchemaVersion:    s.schema,
				EventHeadSeq:     head,
				TraceWatermarkNS: s.obs.trace.watermarkNS(),
				GeneratedAt:      time.Now().UTC(),
				Build:            s.currentBuild(),
				VantageBaseURL:   s.vantageBase,
			}
			if !outside {
				data, fail = fn(r, req)
			}
			return nil
		})
		if outside && err == nil {
			data, fail = fn(nil, req)
		}
		// /why is planned in the drain process's own pinned read transaction.
		// Its cursor belongs to that answer, not this earlier wrapper read.
		if planned, ok := data.(whyPayloadData); ok {
			cursor.EventHeadSeq = planned.head
			data = planned.raw
		}
		// payload is the read transaction and everything the route assembled inside it,
		// which for /why includes the planner it waited on (its own phase says how long).
		phase(ctx, "payload_ms", msSince(time.Now, start))
		encodeStart := time.Now()
		defer func() { phase(ctx, "encode_ms", msSince(time.Now, encodeStart)) }()
		if err != nil {
			f := failure(codeStoreUnreadable, "the store could not be read: %v", err)
			f.Snapshot = cursor
			writeJSON(w, f.status, *f)
			return
		}
		if fail != nil {
			if fail.Snapshot.GeneratedAt.IsZero() {
				fail.Snapshot = cursor
			}
			writeJSON(w, fail.status, *fail)
			return
		}
		if doc, ok := data.(traceDocument); ok {
			// Downloads are standard trace documents, not the view's record envelope.
			writeJSON(w, http.StatusOK, doc.Body)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{Snapshot: cursor, Data: data})
	}
}

// writeJSON writes one answer. A failure to encode would be a bug in a struct this
// package defines, so it is answered as a 500 rather than a truncated body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	clean, err := withoutPrices(body)
	if err != nil {
		http.Error(w, "encoding the response failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	if err := enc.Encode(clean); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: encoding the response: %v\n", err)
	}
}

// sourceState is one read source as this response found it.
type sourceState struct {
	Path  string `json:"path"`
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
}

// sourceStates says which sources a response was assembled from, because "no runs" and
// "no index to read runs from" are different answers.
type sourceStates struct {
	Store      sourceState `json:"store"`
	LoopDir    sourceState `json:"loop_dir"`
	TraceIndex sourceState `json:"trace_index"`
}

func (s *server) sources() sourceStates {
	loop := sourceState{Path: s.obs.loopDir, State: "present"}
	if _, err := os.Stat(s.obs.loopDir); err != nil {
		loop.State = "absent"
		loop.Note = "no tick has written record files here"
	}
	trace := sourceState{Path: s.obs.trace.path, State: s.obs.trace.state, Note: s.obs.trace.note}
	store := sourceState{Path: s.storePath, State: "present"}
	if s.problem != nil {
		// The store is the one source a response cannot be assembled without, so its
		// condition is the same verdict the API answers, spelled for a client that renders
		// a source panel rather than switching on an error code.
		store.State = s.problem.state
		store.Note = s.problem.note
	}
	return sourceStates{
		Store:      store,
		LoopDir:    loop,
		TraceIndex: trace,
	}
}

// drainSummary is the drain's own account of itself.
type drainSummary struct {
	Active             bool          `json:"active"`
	Session            *loopSession  `json:"session"`
	SessionNote        string        `json:"session_note"`
	SessionGraceMS     int64         `json:"session_grace_ms"`
	RunningRuns        int           `json:"running_runs"`
	Ticks              int           `json:"ticks"`
	Dispatched         int           `json:"dispatched"`
	Landed             int           `json:"landed"`
	Rejected           int           `json:"rejected"`
	InfraFailures      *int          `json:"infra_failures"`
	TotalCostUSD       *float64      `json:"total_cost_usd"`
	UnreportedRuns     int           `json:"unreported_runs"`
	CostUnreportedRuns int           `json:"cost_unreported_runs"`
	LastStopReason     *string       `json:"last_stop_reason"`
	StopNote           string        `json:"stop_note"`
	LastDecision       *dispatchView `json:"last_decision"`
	// Build is the drain build that wrote the newest loop result, and BuildNote says why
	// there is none. A record written before the stamp existed is a build that cannot be
	// named, which the board draws as such rather than as a current build (build.go in
	// tillr-drain, and the same distinction the server's own build stamp makes).
	Build     *drainBuild `json:"build"`
	BuildNote string      `json:"build_note"`
	// GateMS is the most recent tick's gate wall time and GateBudgetMS the budget it was
	// measured against. They are pointers because a store that has measured no gate is a
	// different answer from a gate that took zero milliseconds, and the board says so in
	// words rather than drawing a zero-length bar.
	GateMS       *int64 `json:"gate_ms"`
	GateBudgetMS *int64 `json:"gate_budget_ms"`
	// GateTrend is the last few ticks' gate times, oldest first: "the gate got slower" as a
	// number with a date rather than an impression. It is the same table GateMS is read
	// from, bounded so one board request does not carry a month of ticks.
	GateTrend []gateTick `json:"gate_trend"`
}

// gateTick is one tick's gate time in the board's trend.
type gateTick struct {
	TickID     string `json:"tick_id"`
	AtMS       int64  `json:"at_ms"`
	GateMS     int64  `json:"gate_ms"`
	BudgetMS   int64  `json:"budget_ms"`
	OverBudget bool   `json:"over_budget"`
}

// gateTrendLimit bounds the trend the board carries. Twenty ticks is more than a screen of
// bars and less than a night's drain, and the whole table is still there for a reader who
// wants it: the board shows the recent shape, not the archive.
const gateTrendLimit = 20

// laneView is one leased lane.
type laneView struct {
	LaneID        string `json:"lane_id"`
	State         string `json:"state"`
	RunID         string `json:"run_id"`
	LeasedUntilMS *int64 `json:"leased_until_ms"`
}

// dispatchView is one loop decision: what the tick chose and why.
type dispatchView struct {
	DecisionID   string `json:"decision_id"`
	ItemID       string `json:"item_id"`
	LaneID       string `json:"lane_id"`
	RunID        string `json:"run_id"`
	Action       string `json:"action"`
	Reason       string `json:"reason"`
	DispatchedMS int64  `json:"dispatched_at_ms"`
	PassID       string `json:"pass_id,omitempty"`
}

func dispatchViews(rows []storeDispatch) []dispatchView {
	out := make([]dispatchView, 0, len(rows))
	for _, d := range rows {
		out = append(out, dispatchView{d.DecisionID, d.ItemID, d.LaneID, d.RunID, d.Action, d.Reason, d.DispatchedMS, d.PassID})
	}
	return out
}

// runSummary is a run as the item list carries it: the row, and what the store knows of
// its spend.
type runSummary struct {
	RunID       string   `json:"run_id"`
	ItemID      string   `json:"item_id"`
	LaneID      string   `json:"lane_id"`
	Backend     string   `json:"backend"`
	State       string   `json:"state"`
	ExitCode    *int     `json:"exit_code"`
	StartedMS   int64    `json:"started_at_ms"`
	FinishedMS  int64    `json:"finished_at_ms"`
	CostUSD     *float64 `json:"cost_usd"`
	UsageTokens *int64   `json:"usage_tokens"`
}

func summarizeRun(r storeRun) runSummary {
	return runSummary{r.RunID, r.ItemID, r.LaneID, r.Backend, r.State, r.ExitCode, r.StartedAtMS,
		r.FinishedAtMS, r.CostUSD, r.UsageTokens}
}

// itemView is one item as the snapshot lists it: the projected state an operator reads,
// with the machinery subordinated to the attempt history. Rank is the queue's own token,
// carried beside the order it produced: a client told the order and not the token could
// not say where the next item goes.
type itemView struct {
	ItemID         string         `json:"item_id"`
	Title          string         `json:"title"`
	Rank           string         `json:"rank"`
	Projected      itemProjection `json:"projected_state"`
	UpdatedAtMS    int64          `json:"updated_at_ms"`
	CostUSD        *float64       `json:"cost_usd"`
	LatestDecision *dispatchView  `json:"latest_decision"`
	LatestRun      *runSummary    `json:"latest_run"`
}

// snapshotData is the two-item drain view: every item projected, with its runs and the
// stage each run reached.
type snapshotData struct {
	Sources    sourceStates `json:"sources"`
	Counts     boardCounts  `json:"counts"`
	Drain      drainSummary `json:"drain_summary"`
	Lanes      []laneView   `json:"lanes"`
	Items      []itemView   `json:"items"`
	RecentRuns []runSummary `json:"recent_runs"`
}

// boardCounts is the header's numbers: the board's own tallies, computed once here so
// every client does not recount the same records per frame. ByState is keyed by the same
// seven spellings itemProjection.State answers; Dispatchable is an in-queue item with no
// unmet blocker, by the rule the drain planner decides on; Blocked is the items whose
// projected state is blocked; and RunsLive is the runs the store still calls running.
type boardCounts struct {
	ByState      map[string]int `json:"by_state"`
	Dispatchable int            `json:"dispatchable"`
	Blocked      int            `json:"blocked"`
	RunsLive     int            `json:"runs_live"`
	// QuestionHolds is the items whose latest decision is a question hold: the count the
	// header's Questions control shows. It is the planner's own answer, read from the store's
	// dispatch rows, so the header and the rail cannot disagree about what is waiting on a
	// ruling. The questions route answers the index's list; this count is the planner's.
	QuestionHolds int `json:"question_holds"`
}

// terminalItemStates are the states an item waits no longer on: an edge naming one of
// them is a met blocker, not an unmet one. They are the store's spellings, because the
// edge names an item and the projection is what reads it.
var terminalItemStates = map[string]bool{
	"landed": true, "closed": true, "resolved-elsewhere": true,
}

// liveRunState is the projected state of a run whose lane is still leased. It is the
// `runs` state, not the lane's: a reader asking "which runs are live" is asking a question
// about runs, and `running` is the spelling every other read of that table uses.
const liveRunState = "running"

// laneStateLeased is `run.LaneStateLeased`, spelled here for the same reason
// allocateKind is: this module reads lanes, it does not lease them.
const laneStateLeased = "leased"

// liveRuns projects the runs the store holds a lease on but no terminal row for, oldest
// first. A lane with no run id, a lane that is not leased, and a run that already has a row
// are all skipped: the row is authoritative whenever it exists, because only the terminal
// write knows a run's exit code and finish time.
func liveRuns(lanes []storeLane, known []storeRun, allocations map[string]allocation) []storeRun {
	have := make(map[string]bool, len(known))
	for _, run := range known {
		have[run.RunID] = true
	}
	var out []storeRun
	for _, lane := range lanes {
		if lane.State != laneStateLeased || lane.RunID == "" || have[lane.RunID] {
			continue
		}
		allocated, ok := allocations[lane.RunID]
		if !ok {
			// The lease exists and the allocation event does not: the item and the start
			// are unknown, so there is no run to project. The lane row still renders as
			// leased, which is the honest half of the answer.
			continue
		}
		out = append(out, storeRun{
			RunID:       lane.RunID,
			ItemID:      allocated.ItemID,
			LaneID:      lane.LaneID,
			Backend:     allocated.Backend,
			State:       liveRunState,
			StartedAtMS: allocated.StartedAtMS,
		})
	}
	return out
}

// snapshotPayload is the initial screen load: the whole board as one moment.
func (s *server) snapshotPayload(r *store.Reader, _ *http.Request) (any, *apiFailure) {
	out := snapshotData{Sources: s.sources()}
	items, err := readItems(r)
	if err != nil {
		return nil, unreadable(err)
	}
	laneRows, err := readLanes(r)
	if err != nil {
		laneRows = nil
	}
	runs, _ := readRuns(r)
	// The attempt history is the planner's, and it is read from the table rather than from
	// the leases below: a retry is a second attempt at finished work, so a run a lease
	// projects is not evidence the item has run before.
	history, err := readDispatchHistory(r, runs)
	if err != nil {
		return nil, unreadable(err)
	}
	// A run that is still going has no `runs` row — the terminal write creates it — so the
	// board used to say "the store records no run" about a lane that was working at that
	// moment, and counted zero running. The lane's claim is the live record, and it is
	// already in hand; the run's start is the run.allocate event the same transaction
	// appended. Projecting one from the other is what makes the surface agree with the
	// lease. Measured 2026-11-14 during the first real drain: lane-1 `leased` on
	// run-item-obs-filter-20261114T063154Z, `runs_live: 0`, `running_runs: 0`.
	if allocations, err := readAllocations(r); err == nil {
		runs = append(runs, liveRuns(laneRows, runs, allocations)...)
	}
	candidates, _ := readCandidates(r)
	attempts, _ := readAllAttempts(r)
	dispatches, _ := readAllDispatches(r)
	byResult := map[string]int{}
	for _, a := range attempts {
		byResult[a.Result]++
	}
	actionCounts := map[string]int{}
	for _, d := range dispatches {
		actionCounts[d.Action]++
	}

	out.Lanes = make([]laneView, 0, len(laneRows))
	for _, l := range laneRows {
		out.Lanes = append(out.Lanes, laneView{l.LaneID, l.State, l.RunID, l.LeasedUntilMS})
	}
	runsByItem := map[string][]storeRun{}
	var totalCost float64
	var unreported, costUnreported int
	costSeen := false
	for _, run := range runs {
		if run.CostUSD == nil {
			costUnreported++
		}
		if run.CostUSD == nil && run.UsageTokens == nil {
			unreported++
		}
		runsByItem[run.ItemID] = append(runsByItem[run.ItemID], run)
		if run.CostUSD != nil {
			totalCost += *run.CostUSD
			costSeen = true
		}
	}
	enqueued := enqueuedCandidates(candidates, attempts)
	lastDispatch := map[string]dispatchView{}
	for _, d := range dispatchViews(dispatches) {
		lastDispatch[d.ItemID] = d
	}
	// The loop's latest question hold per item. The store records the decision, so the
	// board can say "held for a ruling" without reading Vantage's index on every snapshot;
	// the index itself is the questions route's read, on demand.
	holds := questionHolds(dispatches)
	// The blocked_by edges are what a blocked item waits on, so they are read here and
	// handed to the projection: an item shown as blocked with nothing it waits on would
	// be a state with no cause.
	waiting := map[string][]string{}
	if rows, err := r.Query(`SELECT item_id, blocked_by FROM item_edges ORDER BY item_id, blocked_by`); err == nil {
		for rows.Next() {
			var id, blockedBy string
			if rows.Scan(&id, &blockedBy) == nil {
				waiting[id] = append(waiting[id], blockedBy)
			}
		}
		rows.Close()
	}

	// The store answers its rows by item_id; the board is ordered by the queue's rank.
	sort.SliceStable(items, func(i, j int) bool {
		return rankBefore(items[i].Rank, items[i].ItemID, items[j].Rank, items[j].ItemID)
	})
	// The four figures are boardCountsOf's, not this loop's: the same function answers
	// `tillr view status --store`, so the two faces cannot disagree (§5.6).
	counts := boardCountsOf(items, runs, candidates, attempts, waiting, holds, history)
	out.Items = make([]itemView, 0, len(items))
	for _, it := range items {
		itemRuns := runsByItem[it.ItemID]
		var active *storeRun
		var latest *storeRun
		var cost float64
		costSeenItem := false
		for i := range itemRuns {
			run := itemRuns[i]
			if run.State == "running" {
				active = &itemRuns[i]
			}
			latest = &itemRuns[i]
			if run.CostUSD != nil {
				cost += *run.CostUSD
				costSeenItem = true
			}
		}
		var enqueuedCandidate *storeCandidate
		if c, ok := enqueued[it.ItemID]; ok {
			enqueuedCandidate = &c
		}
		view := itemView{
			ItemID:      it.ItemID,
			Title:       it.Title,
			Rank:        it.Rank,
			UpdatedAtMS: it.UpdatedAtMS,
			Projected:   ProjectItemState(it, enqueuedCandidate, active, waiting[it.ItemID], holds[it.ItemID]),
		}
		if costSeenItem {
			view.CostUSD = &cost
		}
		if d, ok := lastDispatch[it.ItemID]; ok {
			view.LatestDecision = &d
		}
		if latest != nil {
			summary := summarizeRun(*latest)
			view.LatestRun = &summary
		}
		out.Items = append(out.Items, view)
	}
	out.Counts = counts
	running := counts.RunsLive

	// The summary reports only counts the store actually holds. A tick is not a dispatch
	// row: one tick writes a row per item it considered, so a tick that held two items and
	// dispatched one wrote three. The stop reason is the loop's own result file beside those
	// records; a loop that has not written one answers an absence with a note, never a
	// guessed reason.
	stopReason, stopNote, loopBuild, buildNote := s.obs.readLoopResult()
	tickCount, _ := countTicks(r)
	session, active, sessionNote, err := readLoopSession(r, time.Now())
	if err != nil {
		return nil, unreadable(err)
	}
	// A current session is the loop's present state; the file describes a previous
	// stop and must not advise restarting a loop that is already running.
	if active {
		stopReason, stopNote = "", ""
	}
	out.Drain = drainSummary{
		UnreportedRuns:     unreported,
		CostUnreportedRuns: costUnreported,
		Active:             active,
		Session:            session,
		SessionNote:        sessionNote,
		SessionGraceMS:     SessionGraceMS,
		RunningRuns:        running,
		Ticks:              tickCount,
		Dispatched:         actionCounts["dispatch"],
		Landed:             byResult["landed"],
		Rejected:           byResult["isolated"] + byResult["refused"],
		StopNote:           stopNote,
		Build:              loopBuild,
		BuildNote:          buildNote,
	}
	if stopReason != "" {
		out.Drain.LastStopReason = &stopReason
	}
	if costSeen {
		out.Drain.TotalCostUSD = &totalCost
	}
	if len(dispatches) > 0 {
		last := dispatchViews(dispatches[len(dispatches)-1:])[0]
		out.Drain.LastDecision = &last
	}
	// The gate's own wall time, per tick and as a trend. A store written by a drain that had
	// no tick table answers no rows, and the board says the gate has not been measured rather
	// than drawing a zero.
	if ticks, err := readDrainTicks(r); err == nil {
		if gate := gateSummaryOf(ticks); gate != nil {
			out.Drain.GateMS = gate.LastMS
			out.Drain.GateBudgetMS = gate.BudgetMS
			out.Drain.GateTrend = gate.Trend
		}
	}

	out.RecentRuns = make([]runSummary, 0, len(runs))
	start := 0
	if len(runs) > 10 {
		start = len(runs) - 10
	}
	for _, run := range runs[start:] {
		out.RecentRuns = append(out.RecentRuns, summarizeRun(run))
	}
	return out, nil
}

// unreadable answers a store read that failed for a reason other than a missing subject:
// the section cannot be answered, and saying so is the answer. A table the store does not
// carry is its own code — a store written by a program that never linked that utility's
// package answers a question with an absence, and the remedy is a different writer, not a
// readable store.
func unreadable(err error) *apiFailure {
	if isMissingTable(err) {
		return failure(codeTableAbsent, "this store does not carry a table this route reads: %v", err)
	}
	return failure(codeStoreUnreadable, "the store could not be read: %v", err)
}

// documentData is one item's design document as metadata, never as text: the pane shows a
// frontmatter card, the count of open questions a parse found, the path and a link into
// Vantage, which owns the rendering (design §8.3, ledger `OQ-OBD2`). The document's prose
// is served by the tool that authored the dialect, so this face carries none of it.
type documentData struct {
	LastDesigner *designerView `json:"last_designer"`
	ItemID       string        `json:"item_id"`
	// Path is the document's path relative to the served root, slash-separated — the
	// spelling Vantage's `#/` route takes, and the one the pane shows. The resolved
	// absolute path is deliberately not the answer: a link built from it would be a path
	// the viewer cannot resolve, and two spellings of one path on one screen is how a
	// reader learns to distrust both.
	Path string `json:"path"`
	// Frontmatter is the document's metadata card, and null when the document opens with
	// no frontmatter block — a state the pane names rather than drawing as five blanks.
	Frontmatter *documentFrontmatter `json:"frontmatter"`
	// Anchor is the fragment of the item's body_ref — the section a reader is owed — and
	// empty when body_ref names some other document than the one answered here. An anchor
	// into a different file is a lie, and silence is the honest answer (design §8.3).
	Anchor string `json:"anchor"`
	// OpenQuestions is the count of open questions Vantage's index reports for the
	// document, and null when the index could not answer. It is a pointer so "unknown" is
	// a null rather than a zero a reader would read as a finished design (design §8.3).
	OpenQuestions *int `json:"open_questions"`
	// QuestionsState names which answer this is: questions-ok (the tool's list, possibly
	// empty), questions-unknown (the tool was absent, failed or timed out), or
	// design-doc-missing (the tree does not hold the file; the route refuses that case
	// above, so it is the status face's answer to make).
	QuestionsState string `json:"questions_state"`
	// Bytes is the document's size, so a reader can tell a stub from a document without
	// opening it.
	Bytes int `json:"bytes"`
}

// documentLinkPath answers the path a Vantage link spells: the document's path relative to
// the served root, with forward slashes because a link is a URL. A document outside the
// root — an absolute design_doc the operator configured, which this face takes as given —
// answers its absolute path, because there is no relative spelling of it to offer.
func documentLinkPath(root, resolved string) string {
	base, err := filepath.Abs(root)
	if err != nil {
		return filepath.ToSlash(resolved)
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(resolved)
	}
	return filepath.ToSlash(rel)
}

// vantageBaseEnv is the user-scope setting the board's Vantage link is built from. It is an
// environment variable rather than a workspace file because one value serves every jail: the
// operator's Vantage runs once, on the host, and every board links to the same place
// (design §8.2).
const vantageBaseEnv = "TILLR_VANTAGE_BASE_URL"

// vantageBaseURL answers the configured base URL: the flag when it names one, and otherwise
// the user-scope environment setting. A trailing slash is removed so the link has one slash
// where the base and the path meet, and an unset value stays empty — the board draws the
// link and says it goes nowhere rather than guessing an address.
func vantageBaseURL(flagValue string) string {
	base := strings.TrimSpace(flagValue)
	if base == "" {
		base = strings.TrimSpace(os.Getenv(vantageBaseEnv))
	}
	return strings.TrimRight(base, "/")
}

// documentAnchor answers the fragment body_ref names, but only when body_ref and design_doc
// name the same file. Both references go through resolveDocument, so "the same document" here
// is the doc route's own answer to that question rather than a second, string-level guess:
// `./docs/design.md` and `docs/design.md` are one file, and an absolute reference that lands
// on it is too.
func documentAnchor(root, bodyRef, designDoc string) string {
	document, fragment, hasFragment := strings.Cut(bodyRef, "#")
	if !hasFragment || fragment == "" || document == "" || designDoc == "" {
		return ""
	}
	reference, err := resolveDocument(root, document)
	if err != nil {
		return ""
	}
	design, err := resolveDocument(root, designDoc)
	if err != nil {
		return ""
	}
	if reference != design {
		return ""
	}
	return fragment
}

// documentPayload answers the design document the item names. Each refusal is its own code,
// because a client tells four different situations apart to say anything useful: the item is
// not there, the item names no document, the document is not there, or the document is
// outside the root this face was given.
func (s *server) documentPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	it, found, err := readItem(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, itemNotFound(id)
	}
	if it.DesignDoc == "" {
		return nil, failure(codeNoDesignDoc,
			"item %q names no design document: its criteria are the whole commitment", id)
	}
	raw, path, err := readDocument(s.root, it.DesignDoc)
	if err != nil {
		code := codeDesignDocUnreadable
		switch {
		case errors.Is(err, errDocumentOutsideRoot):
			code = codeDesignDocOutsideRoot
		case errors.Is(err, errDesignDocMissing):
			code = codeDesignDocMissing
		}
		return nil, failure(code, "%s", err.Error())
	}
	// The index is reused only while the tree's Markdown is unchanged (vantagecache.go), so
	// a question answered in Vantage while the board runs leaves the count on the next read
	// rather than after a restart.
	answer := readQuestions(s.vantage.source(req.Context()), it.DesignDoc)
	designer, err := readLastDesigner(r, documentLinkPath(s.root, path))
	if err != nil {
		return nil, unreadable(err)
	}
	return documentData{
		LastDesigner:   designer,
		ItemID:         id,
		Path:           documentLinkPath(s.root, path),
		Frontmatter:    parseFrontmatter(string(raw)),
		Anchor:         documentAnchor(s.root, it.BodyRef, it.DesignDoc),
		OpenQuestions:  openQuestionCount(answer),
		QuestionsState: answer.State,
		Bytes:          len(raw),
	}, nil
}

// itemData is one item's dossier. It carries no question list: the board does not track
// question state, and the one question fact it shows — the count — belongs to the document
// route, which owns the parse (design §8.1, ledger `D-OBS6`).
type itemData struct {
	Usage          usageSum            `json:"usage"`
	Comments       *commentItemView    `json:"comments,omitempty"`
	Item           itemDetail          `json:"item"`
	Criteria       []criterionView     `json:"criteria"`
	BlockedBy      []string            `json:"blocked_by"`
	Decisions      []dispatchView      `json:"decisions"`
	Attempts       []attemptView       `json:"attempts"`
	Verdicts       []verdictView       `json:"verdicts"`
	ClosedBlockers []string            `json:"closed_blockers"`
	Runs           []itemRunView       `json:"runs"`
	OwnerDecisions []ownerDecisionView `json:"owner_decisions"`
	StateMoves     []stateMoveView     `json:"state_moves"`
}

// verdictView preserves each recorded judgment, not just the criterion's current state.
type verdictView struct {
	CriterionID string   `json:"criterion_id"`
	Verdict     string   `json:"verdict"`
	Actor       string   `json:"actor"`
	ActorKind   string   `json:"actor_kind"`
	ClaimClass  string   `json:"claim_class"`
	RecordedAt  string   `json:"recorded_at"`
	Reason      string   `json:"reason"`
	Evidence    []string `json:"evidence"`
}

type itemDetail struct {
	ItemID       string   `json:"item_id"`
	Title        string   `json:"title"`
	BodyRef      string   `json:"body_ref"`
	DesignDoc    string   `json:"design_doc"`
	AllowedPaths []string `json:"allowed_paths"`
	// Rank is the item's place in the queue's order, the same token the board carries and
	// sorts by: an item's dossier has to say which item comes first.
	Rank      string         `json:"rank"`
	Projected itemProjection `json:"projected_state"`
}

type criterionView struct {
	CriterionID string `json:"criterion_id"`
	Text        string `json:"text"`
	State       string `json:"state"`
}

// attemptView is one candidate with everything that judged it.
type attemptView struct {
	Candidate  storeCandidate   `json:"candidate"`
	Landings   []landingView    `json:"landings"`
	GateRecord *artifactPayload `json:"gate_record"`
	Verdicts   *artifactPayload `json:"item_verdicts"`
}

type landingView struct {
	AttemptID   string `json:"attempt_id"`
	VerdictID   string `json:"verdict_id"`
	Result      string `json:"result"`
	Reason      string `json:"reason"`
	TrunkBefore string `json:"trunk_before"`
	TrunkAfter  string `json:"trunk_after"`
}

// itemPayload is `GET /api/v1/items/:id`.
func (s *server) itemPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	it, found, err := readItem(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, itemNotFound(id)
	}
	out := itemData{
		Item: itemDetail{ItemID: it.ItemID, Title: it.Title, BodyRef: it.BodyRef,
			DesignDoc: it.DesignDoc, AllowedPaths: it.AllowedPaths, Rank: it.Rank},
		Criteria:       []criterionView{},
		BlockedBy:      []string{},
		Decisions:      []dispatchView{},
		Attempts:       []attemptView{},
		ClosedBlockers: []string{},
	}
	out.Comments, err = readCommentItem(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	out.Verdicts, err = readItemVerdicts(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	out.ClosedBlockers, err = readClosedBlockers(r, id, it.Waiter)
	if err != nil {
		return nil, unreadable(err)
	}
	criteria, _ := readCriteria(r, id)
	for _, c := range criteria {
		out.Criteria = append(out.Criteria, criterionView{c.CriterionID, c.Text, c.State})
	}
	edges, _ := readEdges(r, id)
	for _, e := range edges {
		out.BlockedBy = append(out.BlockedBy, e.BlockedBy)
	}
	dispatchRows, _ := readItemDispatches(r, id)
	out.Decisions = dispatchViews(dispatchRows)
	hold := questionHoldOf(dispatchRows)
	candidates, _ := readItemCandidates(r, id)
	for _, c := range candidates {
		attempt := attemptView{Candidate: c, Landings: []landingView{}}
		landings, _ := readAttempts(r, c.CandidateID)
		for _, l := range landings {
			attempt.Landings = append(attempt.Landings, landingView{l.AttemptID, l.VerdictID, l.Result,
				l.Reason, l.TrunkBefore, l.TrunkAfter})
		}
		runID := ""
		if rows := dispatchRows; len(rows) > 0 {
			runID = rows[len(rows)-1].RunID
		}
		if runID != "" {
			gate := s.obs.readRunArtifact(runID, "gate.verdict", storeRun{}, false)
			attempt.GateRecord = &gate
			verdicts := s.obs.readVerdictArtifacts(runID)
			attempt.Verdicts = &verdicts
		}
		out.Attempts = append(out.Attempts, attempt)
	}
	out.OwnerDecisions, err = readOwnerDecisions(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	out.StateMoves, err = readStateMoves(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	var active *storeRun
	itemRuns, err := readItemRuns(r, id)
	if err != nil && !isMissingTable(err) {
		return nil, unreadable(err)
	}
	lanes, err := readLanes(r)
	if err != nil && !isMissingTable(err) {
		return nil, unreadable(err)
	}
	allocations, err := readAllocations(r)
	if err != nil {
		return nil, unreadable(err)
	}
	for _, run := range liveRuns(lanes, itemRuns, allocations) {
		if run.ItemID == id {
			itemRuns = append(itemRuns, run)
		}
	}
	// Preserve the usage summary's unfinished allocations, including lost leases.
	known := make(map[string]bool, len(itemRuns))
	for _, run := range itemRuns {
		known[run.RunID] = true
	}
	for runID, a := range allocations {
		if a.ItemID == id && !known[runID] {
			itemRuns = append(itemRuns, storeRun{RunID: runID, ItemID: id, LaneID: a.LaneID,
				Backend: a.Backend, StartedAtMS: a.StartedAtMS})
		}
	}
	out.Usage, err = sumUsage(r, itemRuns)
	if err != nil {
		return nil, unreadable(err)
	}
	sort.SliceStable(itemRuns, func(i, j int) bool {
		if itemRuns[i].StartedAtMS != itemRuns[j].StartedAtMS {
			return itemRuns[i].StartedAtMS < itemRuns[j].StartedAtMS
		}
		return itemRuns[i].RunID < itemRuns[j].RunID
	})
	out.Runs, err = readItemRunHistory(r, itemRuns)
	if err != nil {
		return nil, unreadable(err)
	}
	for i := range itemRuns {
		if itemRuns[i].State == "running" {
			active = &itemRuns[i]
		}
	}
	var enqueuedCandidate *storeCandidate
	for i := range candidates {
		if candidates[i].State == "enqueued" {
			enqueuedCandidate = &candidates[i]
		}
	}
	out.Item.Projected = ProjectItemState(it, enqueuedCandidate, active, out.BlockedBy, hold)
	return out, nil
}

// stageView is one step of the seven-stage pipeline (design §4.1).
type stageView struct {
	Stage          string  `json:"stage"`
	Status         string  `json:"status"`
	StartedAtMS    *int64  `json:"started_at_ms"`
	FinishedAtMS   *int64  `json:"finished_at_ms"`
	EpistemicState string  `json:"epistemic_state"`
	Summary        string  `json:"summary"`
	RecordRef      *string `json:"record_ref"`
}

// runData is one run's header, its pipeline, its claims and its spend.
type runData struct {
	Run           runDetail                 `json:"run"`
	Stages        []stageView               `json:"stages"`
	ClaimChecks   []claimView               `json:"claim_checks"`
	Cost          costView                  `json:"-"`
	Usage         runUsage                  `json:"usage"`
	Completeness  completenessView          `json:"completeness"`
	PreservedWork []preserved.PreservedWork `json:"preserved_work,omitempty"`
}

type runDetail struct {
	RunID       string   `json:"run_id"`
	ItemID      string   `json:"item_id"`
	LaneID      string   `json:"lane_id"`
	Backend     string   `json:"backend"`
	State       string   `json:"state"`
	ExitCode    *int     `json:"exit_code"`
	StartedMS   int64    `json:"started_at_ms"`
	FinishedMS  int64    `json:"finished_at_ms"`
	UsageTokens *int64   `json:"usage_tokens"`
	CostUSD     *float64 `json:"cost_usd"`
}

type claimView struct {
	Claim          string `json:"claim"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	EpistemicState string `json:"epistemic_state"`
	ObservedAt     string `json:"observed_at"`
}

// costView is a run's spend. Each field is a pointer because a number the source did not
// report is absent, not zero.
type costView struct {
	Turns        *int     `json:"turns"`
	Tokens       *int64   `json:"tokens"`
	CostTotalUSD *float64 `json:"cost_total_usd"`
	Note         string   `json:"note"`
}

// completenessView says which of the run's four evidence streams exist. A missing stream
// is a named gap, so a client cannot render it as "nothing happened".
type completenessView struct {
	LogState   string `json:"log_state"`
	ProcState  string `json:"proc_state"`
	GitState   string `json:"git_state"`
	BriefState string `json:"brief_state"`
	Note       string `json:"note"`
}

// runPayload is `GET /api/v1/runs/:id`.
func (s *server) runPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	run, found, err := readRun(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, runNotFound(id)
	}
	out := runData{
		Run: runDetail{run.RunID, run.ItemID, run.LaneID, run.Backend, run.State, run.ExitCode,
			run.StartedAtMS, run.FinishedAtMS, run.UsageTokens, run.CostUSD},
		ClaimChecks: []claimView{},
	}
	saved, err := preserved.ReadPreservedWork(r)
	if err != nil {
		return nil, unreadable(err)
	}
	for _, p := range saved {
		if p.RunID == id {
			out.PreservedWork = append(out.PreservedWork, p)
		}
	}
	out.Usage, err = readUsage(r, run)
	if err != nil {
		return nil, unreadable(err)
	}
	out.Stages = s.stages(r, run)
	claims, _ := s.obs.trace.runClaims(id)
	for _, c := range claims {
		state := stateObserved
		if c.ObservedStatus == "unverified" {
			state = stateUnobservedSeam
		}
		out.ClaimChecks = append(out.ClaimChecks, claimView{c.ClaimText, c.ObservedStatus,
			c.DiscrepancyReason, state, c.ObservedAt})
	}
	out.Cost = costView{Tokens: run.UsageTokens, CostTotalUSD: run.CostUSD,
		Note: "the store holds the run's total tokens and cost; a turn count comes from the trace index"}
	if obs := s.obs.trace; obs.db != nil {
		if events, err := obs.runEvents(id); err == nil {
			turns := 0
			for _, e := range events {
				if e.Kind == "llm.turn" {
					turns++
				}
			}
			out.Cost.Turns = &turns
		}
	}
	row, indexed, _ := s.obs.trace.runRow(id)
	out.Completeness = completenessView{LogState: "unindexed", ProcState: "unindexed",
		GitState: "unindexed", BriefState: "absent",
		Note: "no trace index: the run's log, subprocess and git streams are unobserved, not empty"}
	if s.obs.trace.db != nil && indexed {
		out.Completeness = completenessView{LogState: row.LogState, ProcState: row.ProcState,
			GitState: row.GitState, BriefState: "absent",
			Note: "stream states are tillr-trace's index of this run's artifacts"}
	}
	brief := s.obs.readRunArtifact(id, "brief", run, false)
	if brief.Present {
		out.Completeness.BriefState = brief.EpistemicState
		if brief.ClobberWarning != nil {
			out.Completeness.BriefState = "lane_copy"
		}
	}
	return out, nil
}

// stages answers the seven-stage pipeline. A stage with no record is a gap that names
// itself: `absent` and `unobserved`, never a pass.
func (s *server) stages(r *store.Reader, run storeRun) []stageView {
	stages := []stageView{}
	add := func(stage, status, state, summary string, start, end *int64, ref *string) {
		stages = append(stages, stageView{Stage: stage, Status: status, StartedAtMS: start,
			FinishedAtMS: end, EpistemicState: state, Summary: summary, RecordRef: ref})
	}
	brief := s.obs.readRunArtifact(run.RunID, "brief", run, false)
	fence := s.obs.readRunArtifact(run.RunID, "fence", run, false)
	base := s.obs.readRunArtifact(run.RunID, "base_sha", run, false)
	stageSummary, stageState := "the run's staging inputs are not preserved", "absent"
	if brief.Present && fence.Present && base.Present {
		stageSummary, stageState = "brief, fence and base_sha are preserved for this run", brief.EpistemicState
	} else if brief.Present || fence.Present || base.Present {
		stageSummary, stageState = "some of the run's staging inputs are preserved", brief.EpistemicState
		if brief.ClobberWarning != nil {
			stageSummary = "the staging inputs are the lane's copies, which the next tick overwrites"
		}
	}
	add("stage", stageState, stageState, stageSummary, nil, nil, nil)

	add("run", run.State, stateObserved, "the store's run row", &run.StartedAtMS, &run.FinishedAtMS, nil)

	// The boundary and the fence have no per-run record yet: the loop checks them around
	// a tick and writes no file a run can be read from.
	add("boundary", "unobserved", stateUnobservedSeam,
		"the loop's boundary check is a tick-level bracket, and no per-run record of it is written", nil, nil, nil)
	fenceStatus := fence.EpistemicState
	add("fence", fenceStatus, fenceStatus, summaryOf(fence, "the run's fence"), nil, nil, pathRef(fence))

	gate := s.obs.readRunArtifact(run.RunID, "gate.verdict", run, false)
	gateSummary := "no gate record for this run"
	if gate.Present {
		gateSummary = "gate.verdict.json is there; its result is in the artifact body"
	}
	add("gate", presenceOr(gate, "recorded", "absent"), gate.EpistemicState, gateSummary, nil, nil, pathRef(gate))

	candidates, _ := readItemCandidates(r, run.ItemID)
	var landed *landingView
	for i := range candidates {
		attempts, _ := readAttempts(r, candidates[i].CandidateID)
		if len(attempts) > 0 {
			last := attempts[len(attempts)-1]
			landed = &landingView{last.AttemptID, last.VerdictID, last.Result, last.Reason,
				last.TrunkBefore, last.TrunkAfter}
		}
	}
	if landed != nil {
		add("land", landed.Result, stateObserved, "landing attempt "+landed.AttemptID, nil, nil, nil)
	} else {
		add("land", "absent", "absent", "no landing attempt names this item's candidates", nil, nil, nil)
	}

	verdicts := s.obs.readVerdictArtifacts(run.RunID)
	add("verdict", presenceOr(verdicts, "recorded", "absent"), verdicts.EpistemicState,
		"the item verdict records this run wrote", nil, nil, pathRef(verdicts))
	return stages
}

func summaryOf(p artifactPayload, fallback string) string {
	if p.Present {
		if p.Note != "" {
			return p.Note
		}
		return fallback + " is there"
	}
	if p.Note != "" {
		return p.Note
	}
	return fallback + " is absent"
}

func presenceOr(p artifactPayload, yes, no string) string {
	if p.Present {
		return yes
	}
	return no
}

func pathRef(p artifactPayload) *string {
	if p.Path == "" {
		return nil
	}
	path := p.Path
	return &path
}

// exchangesData is the inter-tool record list of one run.
type exchangesData struct {
	Run       string       `json:"run"`
	Exchanges []exchange   `json:"exchanges"`
	Sources   sourceStates `json:"sources"`
	Note      string       `json:"note,omitempty"`
}

// exchangesPayload is `GET /api/v1/runs/:id/exchanges`.
func (s *server) exchangesPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	if _, found, err := readRun(r, id); err != nil {
		return nil, unreadable(err)
	} else if !found {
		return nil, runNotFound(id)
	}
	out := exchangesData{Run: id, Exchanges: s.obs.exchanges(id), Sources: s.sources()}
	if s.obs.trace.db == nil {
		out.Note = "only the supervisor's layer is observable: " + s.obs.trace.note
	}
	return out, nil
}

// timelineData is the waterfall of one run.
type timelineData struct {
	Run     string       `json:"run"`
	Spans   []span       `json:"spans"`
	Sources sourceStates `json:"sources"`
	Note    string       `json:"note,omitempty"`
}

// timelinePayload is `GET /api/v1/runs/:id/timeline`. With no trace index it answers what
// the store and the tick's records alone can, and says the index is missing.
func (s *server) timelinePayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	run, found, err := readRun(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, runNotFound(id)
	}
	spans, err := s.runSpans(r, run)
	if err != nil {
		return nil, unreadable(err)
	}
	usage, err := readUsage(r, run)
	if err != nil {
		return nil, unreadable(err)
	}
	if len(usage.Turns) > 0 {
		kept := spans[:0]
		for _, sp := range spans {
			if sp.Category != "llm_turn" {
				kept = append(kept, sp)
			}
		}
		spans = kept
		parent := id + "/run"
		for i, turn := range usage.Turns {
			spans = append(spans, span{SpanID: fmt.Sprintf("%s/usage-turn/%d", id, i+1), ParentSpanID: &parent,
				LaneID: run.LaneID, Category: "llm_turn", Label: "turn " + turn.ID,
				StartedAtMS: usageClock(turn.StartedMS), FinishedAtMS: usageClock(turn.FinishedMS),
				TSSource: "harness_stream", EpistemicState: stateObserved, Status: "recorded",
				FirstOutputMS: turn.FirstOutputMS, OutputTPS: turn.OutputTPS, Model: turn.Model, Backend: run.Backend})
		}
	}
	out := timelineData{Run: id, Spans: spans, Sources: s.sources()}
	if s.obs.trace.db == nil {
		out.Note = "the timeline is the store's run row plus the tick's record files; " + s.obs.trace.note
	}
	return out, nil
}

// gateSpanView is one bar of the Gate Check Waterfall. The nanosecond bounds cross the
// wire as decimal STRINGS: a Unix nanosecond value around 1.79e18 is above JavaScript's
// `Number.MAX_SAFE_INTEGER` (~9.007e15), and a JSON number that large silently loses its
// low digits in the browser. `started_at_ms` and `finished_at_ms` are the same instants
// as computed floats, which is all the bar's geometry needs — the exact value is beside
// them, and a test pins the round trip.
type gateSpanView struct {
	SpanID         string   `json:"span_id"`
	TraceID        string   `json:"trace_id"`
	ParentSpanID   string   `json:"parent_span_id"`
	CheckID        string   `json:"check_id"`
	Kind           string   `json:"kind"`
	Label          string   `json:"label"`
	Status         string   `json:"status"`
	StartUnixNano  string   `json:"start_unix_nano"`
	EndUnixNano    string   `json:"end_unix_nano"`
	StartedAtMS    *float64 `json:"started_at_ms"`
	FinishedAtMS   *float64 `json:"finished_at_ms"`
	WaitedMSJSON   string   `json:"waited_ms_json"`
	AttributesJSON string   `json:"attributes_json"`
	EpistemicState string   `json:"epistemic_state"`
}

// gateSkipView is one step the gate did not put a verdict on. It stays in the file rather
// than in the table: a landed gate skips ~22 steps, and one row per skip would make
// `gate_spans` mostly silence.
type gateSkipView struct {
	CheckID string `json:"check_id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Reason  string `json:"reason"`
	Planned bool   `json:"planned"`
	Owed    bool   `json:"owed"`
}

// gateData is the Gate Check Waterfall of one run: the plan's own span, one span per
// executed check, and the steps the run did not ask.
type gateData struct {
	Run       string         `json:"run"`
	VerdictID string         `json:"verdict_id"`
	PlanSpan  *gateSpanView  `json:"plan_span"`
	Spans     []gateSpanView `json:"spans"`
	Skipped   []gateSkipView `json:"skipped_checks"`
	Summary   map[string]any `json:"summary,omitempty"`
	Sources   sourceStates   `json:"sources"`
	Note      string         `json:"note,omitempty"`
}

// gatePayload is `GET /api/v1/runs/:id/gate`. The executed spans come from the store's
// `gate_spans` table; the skipped steps come from the run's `gate.spans.json`, because
// they are the file's account of what the run did not ask and not rows in the table.
func (s *server) gatePayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	if _, found, err := readRun(r, id); err != nil {
		return nil, unreadable(err)
	} else if !found {
		return nil, runNotFound(id)
	}
	rows, err := readGateSpans(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	out := gateData{Run: id, Spans: []gateSpanView{}, Skipped: []gateSkipView{}, Sources: s.sources()}
	for _, row := range rows {
		view := gateSpanViewOf(row)
		if row.CheckID == "plan" {
			out.PlanSpan = &view
			if out.VerdictID == "" {
				out.VerdictID = row.VerdictID
			}
			continue
		}
		out.Spans = append(out.Spans, view)
		if out.VerdictID == "" {
			out.VerdictID = row.VerdictID
		}
	}
	skipped, summary, note := s.readGateSpansFile(id)
	out.Skipped = skipped
	out.Summary = summary
	out.Note = note
	if len(rows) == 0 && out.Note == "" {
		out.Note = "the store holds no gate spans for this run: the gate wrote none, or the spans predate the table"
	}
	return out, nil
}

// gateSpanViewOf converts one stored row to the wire shape, in one place so the string and
// float forms of the same instant cannot disagree.
func gateSpanViewOf(row storeGateSpan) gateSpanView {
	return gateSpanView{
		SpanID: row.SpanID, TraceID: row.TraceID, ParentSpanID: row.ParentSpanID,
		CheckID: row.CheckID, Kind: row.Kind, Label: row.Label, Status: row.Status,
		StartUnixNano: strconv.FormatInt(row.StartUnixNano, 10),
		EndUnixNano:   strconv.FormatInt(row.EndUnixNano, 10),
		StartedAtMS:   epochNSMillis(row.StartUnixNano),
		FinishedAtMS:  epochNSMillis(row.EndUnixNano),
		WaitedMSJSON:  row.WaitedMSJSON, AttributesJSON: row.AttributesJSON,
		EpistemicState: stateObserved,
	}
}

// epochNSMillis converts a Unix nanosecond instant to a millisecond float, and answers nil
// for a span that recorded no clock at all. A zero is not an instant 1970-01-01 here: it
// is the gate's "no clock", and the waterfall draws no bar for it (the same refusal
// `TimelineWaterfall` applies to a null).
func epochNSMillis(ns int64) *float64 {
	if ns == 0 {
		return nil
	}
	value := float64(ns) / 1e6
	return &value
}

// readGateSpansFile reads the skipped checks and summary from the run's own
// `gate.spans.json`. A missing file is not a refusal: the table's rows are the waterfall,
// and the file only adds the steps that did not run.
func (s *server) readGateSpansFile(runID string) ([]gateSkipView, map[string]any, string) {
	path := filepath.Join(s.obs.runDir(runID), "gate.spans.json")
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []gateSkipView{}, nil, ""
		}
		return []gateSkipView{}, nil, "the gate spans file could not be read: " + err.Error()
	}
	var file struct {
		SkippedChecks []gateSkipView `json:"skipped_checks"`
		Summary       map[string]any `json:"summary"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return []gateSkipView{}, nil, "the gate spans file is not JSON: " + err.Error()
	}
	if file.SkippedChecks == nil {
		file.SkippedChecks = []gateSkipView{}
	}
	return file.SkippedChecks, file.Summary, ""
}

// transcriptPayload is `GET /api/v1/runs/:id/transcript`: one page of the run's own
// record of what the agent did. A run a lane is still holding has no `runs` row yet, and the
// transcript is exactly the read that must answer while it works, so the run is resolved
// from the store's row or from the lease that holds it. With no cursor it answers the tail — the page a reader
// starts from while the run is still working — and `before`/`after` walk back and forward
// from there. The page is bounded twice (entries and bytes), so the cost of reading a run's
// transcript does not grow with the run's log.
func (s *server) transcriptPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	run, found, err := readRunOrLive(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, runNotFound(id)
	}
	query, fail := parseTranscriptQuery(req)
	if fail != nil {
		return nil, fail
	}
	return s.obs.transcript(run, query), nil
}

// transcriptEntryData is one entry of a run's transcript, answered whole.
type transcriptEntryData struct {
	Run     string            `json:"run"`
	Entries []transcriptEntry `json:"entries"`
	Sources transcriptSources `json:"sources"`
	// EpistemicState is the entry's own: observed, derived_clock, unreadable.
	EpistemicState string `json:"epistemic_state"`
	Note           string `json:"note,omitempty"`
}

// transcriptEntryPayload is `GET /api/v1/runs/:id/transcript/:seq`: one entry with no body
// budget, which is what a truncated entry in a page points at. The entry's sequence number
// is the page's own, so a click on `seq` answers that entry and not a re-scan of the log.
func (s *server) transcriptEntryPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	run, found, err := readRunOrLive(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, runNotFound(id)
	}
	seq, err := strconv.Atoi(req.PathValue("seq"))
	if err != nil || seq < 0 {
		return nil, failure(codeBadQuery, "%q is not an entry of this run's transcript: ask for the entry's seq, a non-negative integer",
			req.PathValue("seq"))
	}
	page := s.obs.transcript(run, transcriptQuery{Entry: &seq, Untruncated: true, Limit: 1})
	if len(page.Entries) == 0 {
		return nil, failure(codeEntryNotFound,
			"run %q has no transcript entry %d: read /api/v1/runs/%s/transcript for the entries it does have",
			id, seq, id)
	}
	entry := page.Entries[0]
	return transcriptEntryData{Run: page.Run, Entries: page.Entries, Sources: page.Sources,
		EpistemicState: entry.EpistemicState, Note: page.Note}, nil
}

// parseTranscriptQuery reads the page parameters. A parameter outside the closed set this
// face accepts is answered as a bad query rather than ignored: a client that asked for a
// page and silently received another would page forever.
func parseTranscriptQuery(req *http.Request) (transcriptQuery, *apiFailure) {
	query := transcriptQuery{Limit: transcriptPageLimit}
	values := req.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > transcriptPageMax {
			return query, failure(codeBadQuery, "limit is 1..%d, and %q is not", transcriptPageMax, raw)
		}
		query.Limit = n
	}
	before, fail := transcriptCursorParam(values, "before")
	if fail != nil {
		return query, fail
	}
	after, fail := transcriptCursorParam(values, "after")
	if fail != nil {
		return query, fail
	}
	if before != nil && after != nil {
		return query, failure(codeBadQuery, "before and after name two different pages: ask for one of them")
	}
	query.Before, query.After = before, after
	return query, nil
}

// transcriptCursorParam reads one cursor parameter.
func transcriptCursorParam(values url.Values, name string) (*transcriptCursor, *apiFailure) {
	raw := values.Get(name)
	if raw == "" {
		return nil, nil
	}
	cursor, ok := decodeTranscriptCursor(raw)
	if !ok {
		return nil, failure(codeBadQuery, "%s=%q is not a cursor this face issued: pass back the cursor a page answered with", name, raw)
	}
	return &cursor, nil
}

// artifactData is one artifact's answer, wrapped so the kind is always present.
type artifactData struct {
	Run      string          `json:"run"`
	Artifact artifactPayload `json:"artifact"`
}

// artifactPayload is `GET /api/v1/runs/:id/artifacts/:kind`. A kind outside the closed
// set is a caller's mistake and answers 400-shaped data at 404's status; a known kind no
// writer produced answers 200 with present=false and the paths searched.
func (s *server) artifactPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	kind := req.PathValue("kind")
	run, found, err := readRun(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, runNotFound(id)
	}
	if kind != "diff" && kind != "turn" && kind != "tool" && kind != "item.verdict" && kind != "gate.log" {
		if _, known := artifactKinds[kind]; !known {
			return nil, failure(codeUnknownKind, "%q is not one of the artifact kinds this face serves", kind)
		}
	}
	return artifactData{Run: id, Artifact: s.obs.readRunArtifact(id, kind, run, true)}, nil
}
