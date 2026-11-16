// Package item is the item utility's contract: its tables, its event kinds and its one
// record→row mapping. It is a package rather than part of main because a main package
// cannot be imported, and a materializer registered only from main would be
// unreachable from any test process — which is the whole of OQ-L2's one-mapping claim.
package item

import (
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The record kinds and event kinds item owns. The record kinds are stdout records and
// are deliberately not registered as events: a rejection asserts no state transition,
// and registering it as appendable would claim one.
const (
	// RecordKind is the record apply emits and the seed materializer consumes.
	RecordKind = "item.record"
	// RejectionKind is the record a negative decision carries.
	RejectionKind = "item.rejection"
	// AddKind is the event appended in the transaction that first writes an item.
	AddKind = "item.add"
	// UpdateKind is the event appended in the transaction that rewrites an item.
	UpdateKind = "item.update"
	// DecisionRecordKind is the record a decision face emits, and the event appended
	// in the transaction that writes a decision row.
	DecisionRecordKind = "decision.record"
	// DecisionRevokeKind is the event appended in the transaction that revokes one.
	DecisionRevokeKind = "decision.revoke"
	// MailRecordKind is the record a mail face emits.
	MailRecordKind = "mail.delivery"
	// MailSendKind is the event appended in the transaction that records a delivery.
	MailSendKind = "mail.send"
	// MailAckKind is the event appended in the transaction that acknowledges one.
	MailAckKind = "mail.ack"
	// VerdictRecordKind is the record the verdict face emits, and the event appended in
	// the transaction that moves a criterion's state: the verdict is the one writer of
	// that state, so the record and the event that records it are one kind.
	VerdictRecordKind = "item.verdict"
	// GrantRecordKind is the record the grant face emits, and the event appended in the
	// transaction that records a delegated sign-off grant.
	GrantRecordKind = "item.grant"
)

// Utility is the producer name every record this utility emits carries.
const Utility = "item"

// Version is this utility's producer version. The version set is released together
// (design §11), so this moves with the set rather than on its own.
const Version = "0.1.0"

// ItemDDL is item's tables: the five design §5.1 gives the utility — items, criteria,
// edges, decisions and mail deliveries — plus the request ledger that makes an
// idempotency key replayable.
//
// The foreign keys are the invariant a future face cannot forget: a criterion or an
// edge cannot name an item that does not exist, whatever the caller checked first.
//
// item_requests is bookkeeping rather than board state: design §5.1 makes
// idempotency_key a write-request field whose replay "returns the original result
// instead of writing twice", and the substrate holds no such ledger, so the utility
// that accepts the key keeps it. It stores the echo the first request produced, which
// is what a replay answers.
const ItemDDL = `
CREATE TABLE IF NOT EXISTS items (
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
  workflow   TEXT NOT NULL DEFAULT '',
  waiting_on TEXT NOT NULL DEFAULT '',
  underlying_state TEXT NOT NULL DEFAULT '',
  close_reason TEXT NOT NULL DEFAULT '',
  closed_at TEXT NOT NULL DEFAULT '',
  closed_by TEXT NOT NULL DEFAULT '',
  closed_from TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS item_criteria (
  item_id      TEXT NOT NULL REFERENCES items(item_id),
  criterion_id TEXT NOT NULL,
  text         TEXT NOT NULL,
  state        TEXT NOT NULL,
  PRIMARY KEY (item_id, criterion_id)
);

CREATE TABLE IF NOT EXISTS item_edges (
  item_id    TEXT NOT NULL REFERENCES items(item_id),
  blocked_by TEXT NOT NULL REFERENCES items(item_id),
  PRIMARY KEY (item_id, blocked_by)
);

CREATE TABLE IF NOT EXISTS item_requests (
  idempotency_key TEXT PRIMARY KEY,
  record          TEXT NOT NULL
);

-- decisions and mail_deliveries are design §5.1's logical ownership: decisions attach
-- to items and mail is how their records are delivered, so both fold into item rather
-- than becoming a utility of their own. The CHECK constraints are the monolith's
-- closed vocabularies, carried over so a row written here is readable by the reader
-- that already exists.
--
-- Keep the historical 'autoqa' member of decided_by for existing rows. Design D-JL7
-- retires it only in decodeDecision for new records; narrowing this CHECK would
-- require rebuilding the table and would reject those historical rows. Opening a
-- store must neither rewrite them nor change their attribution.
CREATE TABLE IF NOT EXISTS decisions (
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

CREATE INDEX IF NOT EXISTS decisions_status ON decisions (status);

-- item_grants is the recorded delegation a verdict is recorded under: the claim classes
-- it covers, its grantor and its expiry (agent-runtime.md §12 "Bounds on a delegated
-- sign-off"). It is a table rather than an event-only record because the intake check
-- reads it at write time, so a grant that no reader could find would be a delegation
-- nothing can honour.
CREATE TABLE IF NOT EXISTS item_grants (
  grant_id      TEXT PRIMARY KEY,
  claim_classes TEXT NOT NULL,
  grantor       TEXT NOT NULL,
  issued_at     INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS mail_deliveries (
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

CREATE INDEX IF NOT EXISTS mail_deliveries_target ON mail_deliveries (to_target, acked_at);
`

// init registers item's contract from item's own package: its DDL, its event kinds and
// its one record→row mapping. Registration is at package load because the store
// composes its migration list from what was registered, and a registration a face
// remembers to make is one a face can forget.
//
// item.record is registered as a materializer and NOT as an event kind: the mapping
// appends item.add or item.update itself, and registering the record kind as appendable
// would claim a transition that does not exist. item.rejection is registered as
// neither: a rejection asserts no state transition and writes no row.
func init() {
	if err := store.RegisterDDL("item", ItemDDL); err != nil {
		panic(err)
	}
	// items.AllowedPaths is the one column added to a table that predates it, so it lands
	// through the store's additive-column hook: CREATE TABLE IF NOT EXISTS is a no-op
	// against an existing store, and a store written before the fence existed would
	// otherwise fail the first read that names the column.
	if err := store.RegisterColumn("item", "items", "allowed_paths", "TEXT NOT NULL DEFAULT ''"); err != nil {
		panic(err)
	}
	// items.Rank is the queue's order (rank.go): the column an item's place is stored in,
	// added to a table that predates it by the same hook, and defaulted to '' so a store
	// that has never been ranked reads every item as unranked.
	if err := store.RegisterColumn("item", "items", "rank", "TEXT NOT NULL DEFAULT ''"); err != nil {
		panic(err)
	}
	// items.Seat is the role the item asks for (seat.go): the column the seat vocabulary is
	// stored in, added to a table that predates it by the same hook and defaulted to '' so an
	// item that names no seat reads as one that asks for none.
	if err := store.RegisterColumn("item", "items", "seat", "TEXT NOT NULL DEFAULT ''"); err != nil {
		panic(err)
	}
	// Older stores have no guide mark; preserve that as an empty value.
	if err := store.RegisterColumn("item", "items", "prose", "TEXT NOT NULL DEFAULT ''"); err != nil {
		panic(err)
	}
	// Blocked metadata is additive; older rows retain their unrecorded waiter.
	for _, column := range []string{"workflow", "waiting_on", "underlying_state", "close_reason", "closed_at", "closed_by", "closed_from"} {
		if err := store.RegisterColumn("item", "items", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			panic(err)
		}
	}
	for _, kind := range []string{
		AddKind, UpdateKind, DecisionRecordKind, DecisionRevokeKind, MailSendKind, MailAckKind,
		VerdictRecordKind, GrantRecordKind,
	} {
		if err := event.Register(kind, "item"); err != nil {
			panic(err)
		}
	}
	if err := store.RegisterMaterializer(RecordKind, "item", Apply); err != nil {
		panic(err)
	}
}
