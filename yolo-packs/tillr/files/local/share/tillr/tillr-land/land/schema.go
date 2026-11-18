// Package land is the land utility's contract: its tables, its record kind, and the one
// landing transaction the trunk moves through. It is a package rather than part of main
// because a main package cannot be imported, and the landing transaction is what every
// face composes rather than reimplements.
package land

import (
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The record kinds this utility owns or consumes.
const (
	// LandingKind is the record `pass` and `one` emit, and the event the landing
	// transaction appends in the same write as the row. One spelling for both carriers
	// because they name the same fact: the row is what a reader queries, and the event is
	// what a reader follows in stream order.
	LandingKind = "land.landing"
	// VerdictKind is the record `pass` consumes. It is gate's kind, not land's: land reads
	// it from a file and never appends it, so it is deliberately not registered here —
	// registering it would claim a transition land does not make, and would collide with
	// gate's own registration in any process linking both.
	VerdictKind = "gate.verdict"
)

// Utility is the producer name every record this utility emits carries.
const Utility = "land"

// Version is this utility's producer version. The version set is released together
// (design §11), so this moves with the set rather than on its own.
const Version = "0.1.0"

// LandDDL is land's tables: design §5.1's logical ownership.
//
// `landings` is the landing record, and it is queue-free: a landing names the item and the
// run it is for, never a candidate row, so a landing is one stateless squash of one
// judged head and there is no queue entry between the gate and the trunk. It carries no
// foreign key at all — items and runs are other utilities' tables, and land's standalone
// faces must not fail in a process where their DDL was never registered, which is exactly
// the independence §5.11 measures.
//
// `candidates` and `landing_attempts` are the retired queue's tables. They are kept, and
// only for a reader: `serveread.go` reads both and the drain reads the attempt chain, and
// a store created before this change hands them a shape they already understand. Nothing
// in this utility writes either one, so a fresh store is created with them empty and a
// reader treats them as history rather than as state. They are retired with the epoch.
const LandDDL = `
CREATE TABLE IF NOT EXISTS landings (
  landing_id          TEXT PRIMARY KEY,
  item_id             TEXT NOT NULL,
  run_id              TEXT NOT NULL,
  verdict_id          TEXT NOT NULL,
  result              TEXT NOT NULL,
  reason              TEXT NOT NULL DEFAULT '',
  base_sha            TEXT NOT NULL,
  head_sha            TEXT NOT NULL,
  trunk_before        TEXT NOT NULL,
  trunk_after         TEXT NOT NULL,
  gated_tree          TEXT NOT NULL DEFAULT '',
  landed_tree         TEXT NOT NULL DEFAULT '',
  conflict_paths      TEXT NOT NULL DEFAULT '[]',
  gate_skipped_reason TEXT NOT NULL DEFAULT '',
  event_seq           INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS landings_item_run ON landings (item_id, run_id);
CREATE INDEX IF NOT EXISTS landings_trunk ON landings (trunk_before, trunk_after);

-- Legacy: the retired candidate queue. Read-only; nothing writes it.
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

-- Legacy: the retired landing attempt ledger, which serveread.go and the drain still
-- read. Read-only; nothing writes it.
CREATE TABLE IF NOT EXISTS landing_attempts (
  attempt_id   TEXT PRIMARY KEY,
  candidate_id TEXT NOT NULL,
  verdict_id   TEXT NOT NULL,
  result       TEXT NOT NULL,
  reason       TEXT NOT NULL DEFAULT '',
  trunk_before TEXT NOT NULL,
  trunk_after  TEXT NOT NULL,
  gated_tree   TEXT NOT NULL DEFAULT '',
  landed_tree  TEXT NOT NULL DEFAULT '',
  carried      INTEGER NOT NULL DEFAULT 0,
  event_seq    INTEGER NOT NULL DEFAULT 0
);
`

// init registers land's contract from land's own package: its DDL and its event kind.
// Registration is at package load because the store composes its migration list from what
// was registered, and a registration a face remembers to make is one a face can forget.
//
// There is no seed materializer: a landing is a transaction the lander performs against a
// repository, so a fixture that seeded one would be describing a trunk move that never
// happened. A store a reader inspects is written by a real landing or not at all.
func init() {
	if err := store.RegisterDDL("land", LandDDL+identityDDL); err != nil {
		panic(err)
	}
	if err := event.Register(LandingKind, "land"); err != nil {
		panic(err)
	}
}
