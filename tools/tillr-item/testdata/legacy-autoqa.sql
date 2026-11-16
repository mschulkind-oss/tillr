-- Captured from the pre-retirement tillr-item writers on 2026-11-16.
BEGIN TRANSACTION;
CREATE TABLE decisions (
  decision_id TEXT PRIMARY KEY,
  text        TEXT NOT NULL,
  scope_kind  TEXT NOT NULL CHECK (scope_kind IN ('global','subsystem','items')),
  scope_ref   TEXT NOT NULL DEFAULT '',
  decided_by  TEXT NOT NULL CHECK (decided_by IN ('owner','whale','autoqa')),
  decided_at  INTEGER NOT NULL,
  status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','superseded','revoked')),
  supersedes  TEXT REFERENCES decisions(decision_id),
  session_id  TEXT NOT NULL DEFAULT ''
);
INSERT INTO "decisions" VALUES('dec-1','the harness ruled the check green','global','','autoqa',1794268800000,'active',NULL,'s-1');
CREATE TABLE events (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  -- Retained but never written: one writer, no reader, and who acted
  -- human-versus-agent lives in lane steer's --actor-kind.
  actor       TEXT,
  seat        TEXT,
  session_id  TEXT,
  object_type TEXT,
  object_id   TEXT,
  revision    INTEGER,
  payload     TEXT NOT NULL DEFAULT '{}'
);
INSERT INTO "events" VALUES(1,1794268800000,'item.add',NULL,NULL,NULL,'item','item_042',NULL,'{"item_id":"item_042","title":"the item face","body_ref":"briefs/item_042.md","state":"not-started","criteria":[{"criterion_id":"c1","text":"the first criterion","state":"open"}],"blocked_by":[],"allowed_paths":null,"rank":"","updated_at":"2026-11-10T00:00:00Z"}');
INSERT INTO "events" VALUES(2,1794268800000,'item.verdict',NULL,NULL,NULL,'item','item_042',NULL,'{"item_id":"item_042","criterion_id":"c1","criterion_hash":"00cf50717100b32b0e92e665ccbaeb641d0b4e9eec4b4973b01f0430806fa52d","verdict":"done","claim_class":"qa-verification","reason":"the criterion''s predicate holds on the landed candidate","evidence":["run-record-9","gate-verdict-7"],"evaluated_head":"0f1e2d","actor_kind":"autoqa","actor":"autoqa-1","judged_actor":"lane-7","recorded_at":"2026-11-10T00:00:00Z"}');
INSERT INTO "events" VALUES(3,1794268800000,'item.grant',NULL,NULL,NULL,'grant','grant_1',NULL,'{"grant_id":"grant_1","claim_classes":["qa-verification"],"grantor":"owner","issued_at":"2026-11-10T00:00:00Z","expires_at":"2026-11-11T00:00:00Z"}');
INSERT INTO "events" VALUES(4,1794268800000,'decision.record',NULL,NULL,NULL,'decision','dec-1',NULL,'{"decision_id":"dec-1","text":"the harness ruled the check green","scope":{"kind":"global"},"decided_by":"autoqa","decided_at":"2026-11-10T00:00:00Z","status":"active","session_id":"s-1"}');
CREATE TABLE item_criteria (
  item_id      TEXT NOT NULL REFERENCES items(item_id),
  criterion_id TEXT NOT NULL,
  text         TEXT NOT NULL,
  state        TEXT NOT NULL,
  PRIMARY KEY (item_id, criterion_id)
);
INSERT INTO "item_criteria" VALUES('item_042','c1','the first criterion','done');
CREATE TABLE item_edges (
  item_id    TEXT NOT NULL REFERENCES items(item_id),
  blocked_by TEXT NOT NULL REFERENCES items(item_id),
  PRIMARY KEY (item_id, blocked_by)
);
CREATE TABLE item_grants (
  grant_id      TEXT PRIMARY KEY,
  claim_classes TEXT NOT NULL,
  grantor       TEXT NOT NULL,
  issued_at     INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL
);
INSERT INTO "item_grants" VALUES('grant_1','qa-verification','owner',1794268800000,1794355200000);
CREATE TABLE item_requests (
  idempotency_key TEXT PRIMARY KEY,
  record          TEXT NOT NULL
);
CREATE TABLE items (
  item_id    TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  body_ref   TEXT,
  state      TEXT NOT NULL,
  design_doc TEXT,
  updated_at INTEGER NOT NULL,
  event_seq  INTEGER NOT NULL DEFAULT 0,
  allowed_paths TEXT NOT NULL DEFAULT '',
  rank       TEXT NOT NULL DEFAULT '',
  seat       TEXT NOT NULL DEFAULT '',
  prose      TEXT NOT NULL DEFAULT '',
  waiting_on TEXT NOT NULL DEFAULT '',
  underlying_state TEXT NOT NULL DEFAULT '',
  close_reason TEXT NOT NULL DEFAULT '',
  closed_at TEXT NOT NULL DEFAULT '',
  closed_by TEXT NOT NULL DEFAULT '',
  closed_from TEXT NOT NULL DEFAULT ''
);
INSERT INTO "items" VALUES('item_042','the item face','briefs/item_042.md','not-started','',1794268800000,1,'','','','','','','','','','');
CREATE TABLE mail_deliveries (
  mail_id      TEXT PRIMARY KEY,
  from_session TEXT NOT NULL DEFAULT '',
  to_target    TEXT NOT NULL,
  subject      TEXT NOT NULL,
  body         TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  acked_at     INTEGER,
  acked_by     TEXT,
  revision     INTEGER NOT NULL DEFAULT 1,
  delivery     TEXT NOT NULL DEFAULT 'intent' CHECK (delivery IN ('intent','sent'))
);
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);
INSERT INTO "schema_migrations" VALUES(5,1794813231863);
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
CREATE INDEX decisions_status ON decisions (status);
CREATE INDEX mail_deliveries_target ON mail_deliveries (to_target, acked_at);
DELETE FROM "sqlite_sequence";
INSERT INTO "sqlite_sequence" VALUES('events',4);
COMMIT;
