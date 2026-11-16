-- The fixture's tables. Each block is the schema its owner registers — items and
-- item_criteria from tools/tillr-item/item/schema.go (ItemDDL), lanes and runs from
-- tools/tillr-run/run/schema.go (RunDDL), candidates and landing_attempts from
-- tools/tillr-land/land/schema.go (LandDDL) — copied here so this module's tests do not
-- depend on a writer in-process. Only the columns the serve face reads are declared: a
-- column the real table gained and this one lacks shows up as a failing case here, not
-- as a silent pass.
CREATE TABLE IF NOT EXISTS items (
  item_id    TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  body_ref   TEXT,
  state      TEXT NOT NULL,
  design_doc TEXT,
  updated_at INTEGER NOT NULL,
  event_seq  INTEGER NOT NULL DEFAULT 0,
  allowed_paths TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS item_criteria (
  item_id      TEXT NOT NULL,
  criterion_id TEXT NOT NULL,
  text         TEXT NOT NULL,
  state        TEXT NOT NULL,
  PRIMARY KEY (item_id, criterion_id)
);

CREATE TABLE IF NOT EXISTS item_edges (
  item_id    TEXT NOT NULL,
  blocked_by TEXT NOT NULL,
  PRIMARY KEY (item_id, blocked_by)
);

CREATE TABLE IF NOT EXISTS lanes (
  lane_id      TEXT PRIMARY KEY,
  state        TEXT NOT NULL,
  run_id       TEXT,
  leased_until INTEGER
);

CREATE TABLE IF NOT EXISTS runs (
  run_id        TEXT PRIMARY KEY,
  item_id       TEXT,
  lane_id       TEXT NOT NULL,
  backend       TEXT NOT NULL,
  state         TEXT NOT NULL,
  exit_code     INTEGER,
  started_at    INTEGER NOT NULL,
  finished_at   INTEGER NOT NULL,
  artifact_refs TEXT NOT NULL DEFAULT '[]',
  event_seq     INTEGER NOT NULL DEFAULT 0,
  usage_tokens  INTEGER,
  usage_cost_usd REAL
);

CREATE TABLE IF NOT EXISTS candidates (
  candidate_id TEXT PRIMARY KEY,
  item_id      TEXT NOT NULL,
  base_ref     TEXT NOT NULL,
  base_sha     TEXT NOT NULL,
  head_sha     TEXT NOT NULL,
  tree_sha     TEXT NOT NULL,
  state        TEXT NOT NULL,
  enqueued_at  INTEGER NOT NULL,
  event_seq    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS landing_attempts (
  attempt_id   TEXT PRIMARY KEY,
  candidate_id TEXT NOT NULL,
  verdict_id   TEXT NOT NULL,
  result       TEXT NOT NULL,
  reason       TEXT NOT NULL DEFAULT '',
  trunk_before TEXT NOT NULL,
  trunk_after  TEXT NOT NULL,
  event_seq    INTEGER NOT NULL DEFAULT 0
);

-- The loop's decisions, as tools/tillr-drain/drain/dispatches.go (DispatchesDDL) declares
-- them. A tick writes one row per item it considered, so several rows share a tick and a
-- `dispatched_at`: the tick count is the distinct `dispatched_at`, never the row count.
CREATE TABLE IF NOT EXISTS dispatches (
  decision_id   TEXT PRIMARY KEY,
  item_id       TEXT NOT NULL,
  lane_id       TEXT,
  run_id        TEXT,
  action        TEXT NOT NULL,
  reason        TEXT NOT NULL,
  dispatched_at INTEGER NOT NULL
);

-- The loop's tick timing, as tools/tillr-drain/drain/timing.go (TickDDL) declares it. One
-- row per tick, carrying the gate's own wall time beside the phases it was measured with.
CREATE TABLE IF NOT EXISTS drain_passes (
  tick_id          TEXT PRIMARY KEY,
  started_at       INTEGER NOT NULL,
  stage_ms         INTEGER NOT NULL,
  dispatch_ms      INTEGER NOT NULL,
  gate_ms          INTEGER NOT NULL,
  land_ms          INTEGER NOT NULL,
  gate_budget_ms   INTEGER NOT NULL,
  gate_over_budget INTEGER NOT NULL
);

-- The gate's per-check measurement, as tools/tillr-drain/drain/gate_spans.go
-- (GateSpanDDL) declares it: one row per executed check span, keyed by the verdict it
-- belongs to. The board's Gate Check Waterfall reads it, and the skipped steps stay in the
-- run's `gate.spans.json` rather than becoming ~22 not-run rows here.
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

-- The append-only log, as tools/tillr/event/append.go declares it. The board reads one kind
-- from it — `run.allocate`, which is the only record a live run has — so the fixture carries
-- the table rather than a second copy of the read.
CREATE TABLE IF NOT EXISTS events (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  actor       TEXT,
  seat        TEXT,
  session_id  TEXT,
  object_type TEXT,
  object_id   TEXT,
  revision    INTEGER,
  payload     TEXT NOT NULL DEFAULT '{}'
);
