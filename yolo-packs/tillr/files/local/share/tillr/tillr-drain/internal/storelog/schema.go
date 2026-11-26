package storelog

import (
	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"

	// The blank imports are the ITEM-PKG precondition made structural. plan writes
	// nothing, but it reads items, lanes and runs, and a store drain opens must carry
	// those tables: the substrate composes its migration list from what was registered
	// at package load, so a store created in drain's process has item's and run's tables
	// only because their packages are linked here. The same imports register their
	// record→row mappings, which is what lets a fixture be seeded in drain's test
	// process. A face that remembered to import them would be a face that could forget.
	_ "github.com/mschulkind-oss/tillr-item/item"
	_ "github.com/mschulkind-oss/tillr-run/run"
)

// GraphRevisionDDL is the append-only table of runtime workflow graph revision decisions.
const GraphRevisionDDL = `
CREATE TABLE IF NOT EXISTS graph_revisions (
 revision_id INTEGER PRIMARY KEY AUTOINCREMENT,
 item_id TEXT NOT NULL,
 attempt INTEGER NOT NULL,
 proposing_run TEXT NOT NULL UNIQUE,
 proposing_node TEXT NOT NULL,
 target TEXT NOT NULL,
 workflow TEXT NOT NULL,
 rule TEXT NOT NULL,
 detail TEXT NOT NULL,
 before_graph TEXT NOT NULL,
 after_graph TEXT NOT NULL,
 before_nodes INTEGER NOT NULL,
 after_nodes INTEGER NOT NULL,
 recorded_at INTEGER NOT NULL
);
CREATE TRIGGER IF NOT EXISTS graph_revisions_no_update BEFORE UPDATE ON graph_revisions
BEGIN SELECT RAISE(ABORT,'graph_revisions is append-only'); END;
CREATE TRIGGER IF NOT EXISTS graph_revisions_no_delete BEFORE DELETE ON graph_revisions
BEGIN SELECT RAISE(ABORT,'graph_revisions is append-only'); END;
`

// SessionDDL is the table of live and ended drain loop sessions.
const SessionDDL = `
CREATE TABLE IF NOT EXISTS drain_sessions (
 session_id TEXT PRIMARY KEY,
 started_at INTEGER NOT NULL,
 refreshed_at INTEGER NOT NULL,
 next_pass_at INTEGER NOT NULL,
 ended_at INTEGER
);
`

// RetryDDL is the table of recorded retry authorizations.
const RetryDDL = `
CREATE TABLE IF NOT EXISTS retries (
  retry_id          TEXT PRIMARY KEY,
  item_id           TEXT NOT NULL,
  supersedes_run_id TEXT NOT NULL,
  requested_by      TEXT NOT NULL,
  reason            TEXT NOT NULL,
  requested_at      INTEGER NOT NULL
);
`

// GateSpanDDL is the table of recorded gate check spans.
const GateSpanDDL = `
CREATE TABLE IF NOT EXISTS gate_spans (
  span_id          TEXT PRIMARY KEY,
  trace_id         TEXT NOT NULL,
  parent_span_id   TEXT NOT NULL,
  verdict_id       TEXT NOT NULL,
  run_id           TEXT NOT NULL,
  tick_id          TEXT NOT NULL,
  check_id         TEXT NOT NULL,
  kind             TEXT NOT NULL,
  label            TEXT NOT NULL,
  status           TEXT NOT NULL,
  start_unix_nano  INTEGER NOT NULL,
  end_unix_nano    INTEGER NOT NULL,
  waited_ms_json   TEXT NOT NULL,
  attributes_json  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS gate_spans_run ON gate_spans (run_id);
`

// RollingDDL is drain's rolling proof watermark state and run log.
const RollingDDL = `
CREATE TABLE IF NOT EXISTS rolling_state (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  watermark_sha TEXT NOT NULL DEFAULT '',
  landings_since INTEGER NOT NULL DEFAULT 0,
  last_run_at   INTEGER NOT NULL DEFAULT 0,
  last_tip_sha  TEXT NOT NULL DEFAULT '',
  last_outcome  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS rolling_runs (
  run_id         TEXT PRIMARY KEY,
  base_sha       TEXT NOT NULL,
  tip_sha        TEXT NOT NULL,
  outcome        TEXT NOT NULL,
  failing_checks TEXT NOT NULL,
  at             INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS rolling_coverage (
  run_id         TEXT PRIMARY KEY REFERENCES rolling_runs(run_id),
  spans_state    TEXT NOT NULL,
  ran            INTEGER NOT NULL,
  cached         INTEGER NOT NULL,
  skipped_checks TEXT NOT NULL
);
`

// BackendWindowDDL is the table of persisted backend quota/refusal windows.
const BackendWindowDDL = `CREATE TABLE IF NOT EXISTS backend_windows (
 backend TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 fraction REAL NOT NULL,
 status TEXT NOT NULL,
 reset_at INTEGER NOT NULL,
 run_id TEXT NOT NULL
);`

// BackendOutcomesDDL is the table of classified terminal run outcomes per backend.
const BackendOutcomesDDL = `CREATE TABLE IF NOT EXISTS backend_outcomes (
 terminal_seq INTEGER PRIMARY KEY REFERENCES events(seq),
 class TEXT NOT NULL,
 closes INTEGER NOT NULL DEFAULT 0
);`

// RuntimeConfigSchema is the table of operator-configured runtime settings.
const RuntimeConfigSchema = `CREATE TABLE IF NOT EXISTS drain_runtime_config (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('operator', 'migrated')),
  updated_at TEXT NOT NULL,
  updated_by TEXT NOT NULL
)`

func init() {
	if err := store.RegisterDDL("drain", DispatchDDL+GraphRevisionDDL+drainmodel.PassDDL+PassSpendDDL+drainmodel.LegacyPassMigrationDDL+SessionDDL+RetryDDL+GateSpanDDL+RollingDDL+fixer.TrunkHistoryDDL+heavy.RewritesDDL+drainmodel.GenerationDDL+BackendWindowDDL+quota.DDL+BackendOutcomesDDL); err != nil {
		panic(err)
	}
	for name, decl := range map[string]string{"judge_graph": "TEXT NOT NULL DEFAULT ''", "judge_attempt": "INTEGER NOT NULL DEFAULT 0"} {
		if err := store.RegisterColumn("drain", "workflow_selections", name, decl); err != nil {
			panic(err)
		}
	}
	for _, name := range []string{"seat", "model"} {
		if err := store.RegisterColumn("drain", "node_runs", name, "TEXT"); err != nil {
			panic(err)
		}
	}
	if err := store.RegisterColumn("drain", "node_runs", "quick_way", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		panic(err)
	}
	for name, definition := range map[string]string{"loop_owner": "TEXT", "stop_requested": "INTEGER", "lanes": "TEXT", "process_priority": "TEXT"} {
		if err := store.RegisterColumn("drain", "drain_sessions", name, definition); err != nil {
			panic(err)
		}
	}
	for name, definition := range map[string]string{
		"claim_ms":          "INTEGER",
		"ended_at":          "INTEGER",
		"decision_counts":   "TEXT",
		"applied_count":     "INTEGER",
		"gate_count":        "INTEGER",
		"gate_max_ms":       "INTEGER NOT NULL DEFAULT 0",
		"runner_gate_count": "INTEGER NOT NULL DEFAULT 0",
		"runner_gate_ms":    "INTEGER NOT NULL DEFAULT 0",
		"runner_land_ms":    "INTEGER NOT NULL DEFAULT 0",
	} {
		for _, table := range []string{"drain_ticks", "drain_passes"} {
			if err := store.RegisterColumn("drain", table, name, definition); err != nil {
				panic(err)
			}
		}
	}
	if err := store.RegisterColumn("drain", "dispatches", "pass_id", "TEXT"); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn("drain", "dispatches", "deferred_by", "TEXT"); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn("drain", "dispatches", "backend", "TEXT"); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn("drain", "dispatches", "seat", "TEXT"); err != nil {
		panic(err)
	}

	if err := store.RegisterDDL("drain-comments", `CREATE TABLE IF NOT EXISTS last_designers (
 document TEXT PRIMARY KEY, record TEXT NOT NULL
 );
 CREATE TABLE IF NOT EXISTS comment_runs (
 run_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, session_id TEXT NOT NULL,
 reason TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT '', delivered INTEGER NOT NULL DEFAULT 0
 );
 CREATE TABLE IF NOT EXISTS comment_deliveries (
 run_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, document TEXT NOT NULL,
 blob_id TEXT NOT NULL, body TEXT NOT NULL, written INTEGER NOT NULL DEFAULT 0
 );
 CREATE TABLE IF NOT EXISTS comment_arrivals (
 item_id TEXT NOT NULL, comment_id TEXT NOT NULL, session_id TEXT NOT NULL,
 reason TEXT NOT NULL, cache_lifetime_seconds INTEGER, seconds_since_request REAL,
 PRIMARY KEY(item_id,comment_id)
 );
 CREATE TABLE IF NOT EXISTS comment_reply_states (
 run_id TEXT NOT NULL, comment_id TEXT NOT NULL, state TEXT NOT NULL,
 PRIMARY KEY(run_id,comment_id)
 );
 CREATE TABLE IF NOT EXISTS comment_confirmation (
 id INTEGER PRIMARY KEY CHECK(id=1), configured INTEGER NOT NULL
 );`); err != nil {
		panic(err)
	}

	if err := store.RegisterDDL("drain-worker-returns", `CREATE TABLE IF NOT EXISTS node_reused (
 item_id TEXT NOT NULL, attempt INTEGER NOT NULL, node_id TEXT NOT NULL,
 source_attempt INTEGER NOT NULL, PRIMARY KEY(item_id,attempt,node_id)
 );`); err != nil {
		panic(err)
	}

	if err := store.RegisterDDL("drain-runtime-config", RuntimeConfigSchema); err != nil {
		panic(err)
	}
}
