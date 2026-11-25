package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE obs_events (
  obs_id      TEXT PRIMARY KEY,
  seq         INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  ts_start    TEXT NOT NULL,
  ts_end      TEXT,
  duration_ms INTEGER,
  seam        TEXT,
  run_id      TEXT,
  lane_id     TEXT,
  item_id     TEXT,
  tick_id     TEXT,
  session_id  TEXT,
  route_id    TEXT,
  span_id     TEXT,
  summary     TEXT,
  payload     TEXT NOT NULL DEFAULT '{}',
  source      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX obs_events_run  ON obs_events(run_id, kind, ts_start);

CREATE TABLE runs_index (
  run_id      TEXT PRIMARY KEY,
  lane_id     TEXT,
  item_id     TEXT,
  backend     TEXT,
  store_state TEXT,
  exit_code   INTEGER,
  started_at  INTEGER,
  finished_at INTEGER,
  log_path    TEXT,
  log_state   TEXT,
  proc_path   TEXT,
  proc_state  TEXT,
  git_path    TEXT,
  git_state   TEXT,
  brief_path  TEXT,
  fence_path  TEXT,
  base_sha    TEXT,
  head_sha    TEXT,
  tree_sha    TEXT,
  detail      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE claim_checks (
  claim_id            TEXT PRIMARY KEY,
  run_id              TEXT NOT NULL,
  lane_id             TEXT,
  item_id             TEXT,
  claim_kind          TEXT NOT NULL,
  claim_text          TEXT NOT NULL,
  observed_status     TEXT NOT NULL
    CHECK (observed_status IN ('verified','disproved','unverified')),
  discrepancy_reason  TEXT NOT NULL DEFAULT '',
  evidence_span_ids   TEXT NOT NULL DEFAULT '[]',
  last_dir_edit_at    TEXT,
  observed_at         TEXT NOT NULL
);
`

type RunMeta struct {
	RunID      string  `json:"run_id"`
	LaneID     *string `json:"lane_id"`
	ItemID     *string `json:"item_id"`
	Backend    *string `json:"backend"`
	StoreState *string `json:"store_state"`
	ExitCode   *int64  `json:"exit_code"`
	StartedAt  *int64  `json:"started_at"`
	FinishedAt *int64  `json:"finished_at"`
	LogPath    *string `json:"log_path"`
	LogState   *string `json:"log_state"`
	ProcPath   *string `json:"proc_path"`
	ProcState  *string `json:"proc_state"`
	GitPath    *string `json:"git_path"`
	GitState   *string `json:"git_state"`
	BriefPath  *string `json:"brief_path"`
	FencePath  *string `json:"fence_path"`
	BaseSHA    *string `json:"base_sha"`
	HeadSHA    *string `json:"head_sha"`
	TreeSHA    *string `json:"tree_sha"`
	Detail     string  `json:"detail"`
}

type RunSources struct {
	Log         string
	Proc        string
	Manifest    string
	BaseSHA     string
	Brief       string
	Fence       string
	GatePlan    string
	GateVerdict string
}

type Corpus struct {
	TraceRoot string
	StorePath string
	LoopRoot  string
	DB        *sql.DB

	storeRunsCache map[string]StoreRun
	runDirsCache   map[string][]string
	storeState     string
	storeDetail    string
}

func newCorpus(traceRoot, storePath, loopDir string) (*Corpus, error) {
	if loopDir == "" {
		loopDir = filepath.Join(filepath.Dir(storePath), "loop")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Corpus{
		TraceRoot: traceRoot,
		StorePath: storePath,
		LoopRoot:  loopDir,
		DB:        db,
	}, nil
}

func (c *Corpus) Close() {
	if c.DB != nil {
		_ = c.DB.Close()
		c.DB = nil
	}
}

func (c *Corpus) runDirs() map[string][]string {
	if c.runDirsCache == nil {
		found := make(map[string][]string)
		for _, root := range []string{c.LoopRoot, filepath.Join(c.TraceRoot, "runs")} {
			entries, err := os.ReadDir(root)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() {
					name := entry.Name()
					found[name] = append(found[name], filepath.Join(root, name))
				}
			}
		}
		c.runDirsCache = found
	}
	return c.runDirsCache
}

func (c *Corpus) storeRuns() map[string]StoreRun {
	if c.storeRunsCache == nil {
		store := openStore(c.StorePath)
		defer store.Close()
		c.storeState = store.State
		c.storeDetail = store.Detail
		m := make(map[string]StoreRun)
		for _, r := range store.runs() {
			m[r.RunID] = r
		}
		c.storeRunsCache = m
	}
	return c.storeRunsCache
}

func (c *Corpus) runIDs(limit int) []string {
	sr := c.storeRuns()
	dirs := c.runDirs()
	seen := make(map[string]bool)
	var all []string
	for k := range sr {
		seen[k] = true
		all = append(all, k)
	}
	for k := range dirs {
		if !seen[k] {
			seen[k] = true
			all = append(all, k)
		}
	}

	started := func(runID string) int64 {
		if row, ok := sr[runID]; ok && row.StartedAt != nil && *row.StartedAt != 0 {
			return *row.StartedAt
		}
		var maxMtime int64
		for _, d := range dirs[runID] {
			if info, err := os.Stat(d); err == nil {
				ms := info.ModTime().UnixMilli()
				if ms > maxMtime {
					maxMtime = ms
				}
			}
		}
		return maxMtime
	}

	sort.Slice(all, func(i, j int) bool {
		si, sj := started(all[i]), started(all[j])
		if si != sj {
			return si > sj
		}
		return all[i] > all[j]
	})

	if limit > 0 && len(all) > limit {
		return all[:limit]
	}
	return all
}

func (c *Corpus) known(runID string) bool {
	if _, ok := c.storeRuns()[runID]; ok {
		return true
	}
	_, ok := c.runDirs()[runID]
	return ok
}

func (c *Corpus) logPath(runID string, dirs []string, storeRow *StoreRun) string {
	var named string
	if storeRow != nil {
		refs := parseArtifactRefs(storeRow.ArtifactRefs)
		named = refs["log"]
	}
	if named != "" && fileExists(named) {
		return named
	}
	var fallback string
	if len(dirs) > 0 {
		fallback = runArtifact(dirs, runID+".log")
	}
	if fallback != "" && (fileExists(fallback) || named == "") {
		return fallback
	}
	return named
}

func (c *Corpus) runSources(runID string) RunSources {
	dirs := c.runDirs()[runID]
	var storeRowPtr *StoreRun
	if sr, ok := c.storeRuns()[runID]; ok {
		storeRowPtr = &sr
	}
	loopDir := filepath.Join(c.LoopRoot, runID)
	return RunSources{
		Log:         c.logPath(runID, dirs, storeRowPtr),
		Proc:        runArtifact(dirs, "proc-exec.jsonl"),
		Manifest:    runArtifact(dirs, "manifest.json"),
		BaseSHA:     runArtifact(dirs, "base_sha"),
		Brief:       runArtifact(dirs, "brief.md"),
		Fence:       runArtifact(dirs, "fence.txt"),
		GatePlan:    filepath.Join(loopDir, "gate.plan.json"),
		GateVerdict: filepath.Join(loopDir, "gate.verdict.json"),
	}
}

func (c *Corpus) tickOf(runID string) string {
	m := readJSONMap(c.runSources(runID).Manifest)
	t, _ := asString(m["tick_id"])
	return t
}

func (c *Corpus) load(runIDs []string, kinds map[string]bool, claims bool) {
	c.storeRuns()
	c.syncUnreadableStore()
	runSet := make(map[string]bool, len(runIDs))
	for _, runID := range runIDs {
		runSet[runID] = true
		c.derive(runID, kinds, claims)
	}
	c.tickDecisions(runSet)
}

var parseRunLogFn = parseRunLog

func (c *Corpus) derive(runID string, kinds map[string]bool, claims bool) {
	var storeRow *StoreRun
	if sr, ok := c.storeRuns()[runID]; ok {
		storeRow = &sr
	}
	trace := &TraceContext{RunID: runID}
	if storeRow != nil {
		trace.LaneID = storeRow.LaneID
		trace.ItemID = storeRow.ItemID
	}
	dirs := c.runDirs()[runID]
	sources := c.runSources(runID)
	manifest := readJSONMap(sources.Manifest)
	if tickID, ok := asString(manifest["tick_id"]); ok && tickID != "" {
		trace.TickID = &tickID
	}

	turnsOnly := kinds != nil && !claims && (len(kinds) == 0 || (len(kinds) == 1 && kinds["llm.turn"]))
	var logParsed Parsed
	func() {
		defer func() {
			if r := recover(); r != nil {
				logParsed = Parsed{
					Rows:   newRows("run-log", sources.Log),
					State:  "unreadable",
					Detail: fmt.Sprintf("panic: %v", r),
				}
			}
		}()
		logParsed = parseRunLogFn(sources.Log, trace, turnsOnly)
	}()
	c.insertRows(logParsed.Rows)

	var procParsed Parsed
	if turnsOnly {
		procParsed = Parsed{Rows: newRows("skip", ""), State: "unread", Detail: "not read for this answer"}
	} else {
		procParsed = parseProcExec(sources.Proc, trace)
		c.insertRows(procParsed.Rows)
		for _, gatePath := range []string{sources.GatePlan, sources.GateVerdict} {
			if fileExists(gatePath) {
				gp := parseGateRecord(gatePath, trace)
				c.insertRows(gp.Rows)
			}
		}
	}

	if logParsed.State == "missing" || logParsed.State == "unreadable" || logParsed.State == "corrupt" {
		c.insertUnreadable(trace, "run-log", sources.Log, fmt.Sprintf("%s: %s", logParsed.State, logParsed.Detail))
	}
	if procParsed.State == "unreadable" || procParsed.State == "corrupt" {
		c.insertUnreadable(trace, "proc-exec.jsonl", sources.Proc, procParsed.State)
	}

	var baseSHA *string
	if sources.BaseSHA != "" && fileExists(sources.BaseSHA) {
		if raw, err := os.ReadFile(sources.BaseSHA); err == nil {
			s := strings.TrimSpace(string(raw))
			if s != "" {
				baseSHA = &s
			}
		}
	}
	if baseSHA == nil {
		if s, ok := asString(manifest["base_sha"]); ok && s != "" {
			baseSHA = &s
		}
	}

	var detailBits []string
	if logParsed.Detail != "" {
		detailBits = append(detailBits, logParsed.Detail)
	}
	if storeRow == nil {
		detailBits = append(detailBits, "no store row for this run")
	}

	storeState := "unrecorded"
	var backend *string
	var exitCode, startedAt, finishedAt *int64
	if storeRow != nil {
		if storeRow.State != nil && *storeRow.State != "" {
			storeState = *storeRow.State
		}
		backend = storeRow.Backend
		exitCode = storeRow.ExitCode
		startedAt = storeRow.StartedAt
		finishedAt = storeRow.FinishedAt
	}

	var logPathPtr, procPathPtr *string
	if sources.Log != "" {
		lp := sources.Log
		logPathPtr = &lp
	}
	if sources.Proc != "" {
		pp := sources.Proc
		procPathPtr = &pp
	}

	gitState := GitRetired
	logState := logParsed.State
	procState := procParsed.State

	meta := RunMeta{
		RunID:      runID,
		LaneID:     trace.LaneID,
		ItemID:     trace.ItemID,
		Backend:    backend,
		StoreState: &storeState,
		ExitCode:   exitCode,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		LogPath:    logPathPtr,
		LogState:   &logState,
		ProcPath:   procPathPtr,
		ProcState:  &procState,
		GitPath:    nil,
		GitState:   &gitState,
		BriefPath:  existingArtifact(dirs, "brief.md"),
		FencePath:  existingArtifact(dirs, "fence.txt"),
		BaseSHA:    baseSHA,
		HeadSHA:    strPtr(manifest["head_sha"]),
		TreeSHA:    strPtr(manifest["tree_sha"]),
		Detail:     strings.Join(detailBits, "; "),
	}

	_, _ = c.DB.Exec(
		"INSERT OR REPLACE INTO runs_index (run_id, lane_id, item_id, backend, store_state,"+
			" exit_code, started_at, finished_at, log_path, log_state, proc_path, proc_state,"+
			" git_path, git_state, brief_path, fence_path, base_sha, head_sha, tree_sha, detail)"+
			" VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		meta.RunID, meta.LaneID, meta.ItemID, meta.Backend, meta.StoreState,
		meta.ExitCode, meta.StartedAt, meta.FinishedAt, meta.LogPath, meta.LogState,
		meta.ProcPath, meta.ProcState, meta.GitPath, meta.GitState, meta.BriefPath,
		meta.FencePath, meta.BaseSHA, meta.HeadSHA, meta.TreeSHA, meta.Detail,
	)

	if claims {
		for _, cl := range verifyRun(c.DB, meta, logParsed.Summary) {
			_, _ = c.DB.Exec(
				"INSERT OR REPLACE INTO claim_checks (claim_id, run_id, lane_id,"+
					" item_id, claim_kind, claim_text, observed_status,"+
					" discrepancy_reason, evidence_span_ids, last_dir_edit_at,"+
					" observed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
				cl.ClaimID, cl.RunID, cl.LaneID, cl.ItemID, cl.ClaimKind, cl.ClaimText,
				cl.ObservedStatus, cl.DiscrepancyReason, cl.EvidenceSpanIDs,
				cl.LastDirEditAt, cl.ObservedAt,
			)
		}
	}

	if kinds != nil {
		wanted := make([]string, 0, len(kinds)+1)
		hasUnreadable := false
		for k := range kinds {
			wanted = append(wanted, k)
			if k == Unreadable {
				hasUnreadable = true
			}
		}
		if !hasUnreadable {
			wanted = append(wanted, Unreadable)
		}
		sort.Strings(wanted)
		marks := strings.Repeat("?,", len(wanted))
		marks = marks[:len(marks)-1]
		args := make([]any, 0, len(wanted)+1)
		args = append(args, runID)
		for _, w := range wanted {
			args = append(args, w)
		}
		_, _ = c.DB.Exec(
			fmt.Sprintf("DELETE FROM obs_events WHERE run_id = ? AND kind NOT IN (%s)", marks),
			args...,
		)
	}
}

func (c *Corpus) insertRows(rows *Rows) {
	if rows == nil || len(rows.Rows) == 0 {
		return
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare(
		"INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start, ts_end," +
			" duration_ms, seam, run_id, lane_id, item_id, tick_id, session_id," +
			" route_id, span_id, summary, payload, source)" +
			" VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
	)
	if err != nil {
		_ = tx.Rollback()
		return
	}
	defer stmt.Close()
	for _, r := range rows.Rows {
		_, _ = stmt.Exec(
			r.ObsID, r.Seq, r.Kind, r.TsStart, r.TsEnd, r.DurationMs, r.Seam,
			r.RunID, r.LaneID, r.ItemID, r.TickID, r.SessionID, r.RouteID,
			r.SpanID, r.Summary, r.Payload, r.Source,
		)
	}
	_ = tx.Commit()
}

func (c *Corpus) insertUnreadable(trace *TraceContext, artifact, path, reason string) {
	rows := newRows("unreadable", path)
	rows.addUnreadable(trace, artifact, path, reason)
	c.insertRows(rows)
}

func (c *Corpus) tickDecisions(runIDs map[string]bool) {
	store := openStore(c.StorePath)
	defer store.Close()
	if store.State != "ok" || !store.Tables["dispatches"] {
		return
	}
	for _, d := range store.dispatches() {
		if d.RunID == nil || !runIDs[*d.RunID] {
			continue
		}
		payload := marshalJSONNoEscape(map[string]any{
			"outcome": d.Action,
			"reason":  d.Reason,
		})
		sum := truncateRunes(strings.TrimSpace(d.Action+" "+d.Reason), ExcerptMax)
		_, _ = c.DB.Exec(
			"INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start,"+
				" run_id, lane_id, item_id, span_id, summary, payload, source)"+
				" VALUES (?,?,?,?,?,?,?,?,?,?,?)",
			"obs-dispatch-"+d.DecisionID, -1, "tick.decision",
			isoFromIntMs(d.DispatchedAt), *d.RunID, d.LaneID, d.ItemID,
			d.DecisionID, sum, payload, c.StorePath,
		)
	}
}

func (c *Corpus) syncUnreadableStore() {
	if fileExists(c.StorePath) {
		return
	}
	payload := marshalJSONNoEscape(map[string]any{
		"artifact": "state.db",
		"path":     c.StorePath,
		"reason":   "absent",
	})
	_, _ = c.DB.Exec(
		"INSERT OR REPLACE INTO obs_events (obs_id, seq, kind, ts_start, run_id,"+
			" span_id, summary, payload, source) VALUES (?,?,?,?,?,?,?,?,?)",
		"obs-store-"+Unreadable, 0, Unreadable, nowISO(), "unattributed",
		"unreadable-store", fmt.Sprintf("store: unreadable (no store at %s)", c.StorePath),
		payload, c.StorePath,
	)
}
