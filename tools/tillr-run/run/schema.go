// Package run is the run utility's contract: its tables, its event kinds and its one
// record→row mapping. It is a package rather than part of main because a main package
// cannot be imported, and a materializer registered only from main would be
// unreachable from any test process — which is the whole of OQ-L2's one-mapping claim.
package run

import (
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The record and event kinds this utility owns.
const (
	// RequestKind is the input record exec consumes.
	RequestKind = "run.request"
	// RecordKind is the terminal record exec emits and the seed materializer consumes.
	RecordKind = "run.record"
	// AllocateKind is the event appended in the transaction that allocates the lane and
	// records the run's start: the claim, and the record of the lease it took.
	AllocateKind = "run.allocate"
	// FinishKind is the event appended in the transaction that writes the terminal
	// state.
	FinishKind = "run.finish"
	// UsageKind is the event that records a run whose spend could not be read: the
	// backend's extractor failed, or wrote a report this utility could not parse, so the
	// record says "unknown" and the failure is in the log as its own event rather than
	// folded into the finish. A failed extraction never changes the run's own outcome.
	UsageKind = "run.usage"
	// CopiedUsageKind preserves the old tokens, cost, source run and operator reason
	// when a correction removes usage copied from an earlier run.
	CopiedUsageKind = "run.usage.copied"
	// ReleaseKind is the event a lane release appends, and the record `run release`
	// answers with: a lane freed, by its run finishing (no event of its own) or by the
	// reaper and the operator (this one), and the reason recorded either way.
	ReleaseKind = "run.release"
	// ResumeKind is the event one resume appends to the run it continues. A resume is one
	// attempt continued, not a second attempt: the run id is unchanged, so the store gains a
	// span on the same run and never a second runs row.
	ResumeKind = "run.resume"
)

// Utility is the producer name every record this utility emits carries.
const Utility = "run"

// Version is this utility's producer version. The version set is released together
// (design §11), so this moves with the set rather than on its own.
const Version = "0.1.0"

// RunDDL is run's tables: design §5.1's logical ownership, lanes and runs.
//
// The foreign key is the invariant a future face cannot forget: a run cannot name a
// lane that does not exist, whatever the caller checked first.
//
// lanes.leased_until is the lease's expiry on the store host's clock (§5.6), written at
// claim and cleared by the release; NULL means the lane holds no lease. The lane row is
// the lease as well as the lane: there is one claim on a lane at a time, and a second
// row for the same lane would be a second claim.
//
// runs.item_id is the item the run was dispatched for, materialized from the record
// (§5.6, D-UT19). It is nullable because a run can be dispatched for no item at all:
// NULL says that, where an id the record could not name would be a value the store
// invented.
//
// backend is NOT NULL because the record refuses a run without one, so the row can only
// ever be written from a record that carries it.
//
// runs.route is the model id that backend's adapter was handed, copied from the request's
// argv (see Record.Route). It is nullable for the same reason item_id is: a request whose
// argv names no model — a fixture run, a request written by hand — has no route to name,
// and NULL says so where an empty string would look like a route the record failed to
// fill.
const RunDDL = `
CREATE TABLE IF NOT EXISTS lanes (
  lane_id      TEXT PRIMARY KEY,
  state        TEXT NOT NULL,
  run_id       TEXT,
  leased_until INTEGER
);

CREATE TABLE IF NOT EXISTS read_only_runs (
 run_id TEXT PRIMARY KEY,
 group_id TEXT NOT NULL,
 item_id TEXT NOT NULL,
 lane_id TEXT NOT NULL REFERENCES lanes(lane_id),
 settled INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS read_only_group_members ON read_only_runs(group_id,settled);
CREATE INDEX IF NOT EXISTS read_only_active_lanes ON read_only_runs(lane_id,run_id) WHERE settled=0;

-- Drain writes quota pauses; the executor removes them atomically with resume.
-- requested distinguishes a peer still stopping from a terminal run waiting to resume.
CREATE TABLE IF NOT EXISTS quota_pauses (
 run_id TEXT PRIMARY KEY,
 item_id TEXT NOT NULL,
 lane_id TEXT NOT NULL,
 backend TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('requested','waiting')),
 paused_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
  run_id        TEXT PRIMARY KEY,
  item_id       TEXT,
  lane_id       TEXT NOT NULL REFERENCES lanes(lane_id),
  backend       TEXT NOT NULL,
  route         TEXT,
  harness       TEXT,
  state         TEXT NOT NULL,
  exit_code     INTEGER,
  end_cause     TEXT,
  started_at    INTEGER NOT NULL,
  finished_at   INTEGER NOT NULL,
  artifact_refs TEXT NOT NULL DEFAULT '[]',
  event_seq     INTEGER NOT NULL DEFAULT 0,
  extension_set TEXT
);
`

// init registers run's contract from run's own package: its DDL, its event kinds and
// its one record→row mapping. Registration is at package load because the store
// composes its migration list from what was registered, and a registration a face
// remembers to make is one a face can forget.
//
// run.record is registered as an event kind as well as a record kind because
// store.Seed appends a record's event through event.Append, which refuses an
// unregistered kind: without it the seed path fails with `kind "run.record" is not
// registered`. run.request is deliberately NOT registered: it is an input record kind,
// never an event, and registering it would claim a state transition that does not
// exist.
func init() {
	if err := store.RegisterDDL("run", RunDDL); err != nil {
		panic(err)
	}
	// item_id was added to a table that already shipped, so declaring it in RunDDL is
	// not enough: CREATE TABLE IF NOT EXISTS leaves an existing runs table's shape
	// alone, and the first write naming the column fails on such a store. The
	// declaration is the one RunDDL writes, so a migrated store and a fresh one have
	// the same shape.
	if err := store.RegisterColumn(Utility, "runs", "item_id", "TEXT"); err != nil {
		panic(err)
	}
	// route joined the table after it shipped too, for the same reason item_id did: the
	// declaration above is what a fresh store gets, and this is what a store written
	// before route existed gets on its next write-open.
	if err := store.RegisterColumn(Utility, "runs", "route", "TEXT"); err != nil {
		panic(err)
	}
	// The run's spend joined the table after it shipped, for the same reason item_id did.
	// Both columns are nullable and stay nullable: a run whose backend reports no cost has
	// no number to write, and a 0 there would be a measurement nobody made.
	if err := store.RegisterColumn(Utility, "runs", "usage_tokens", "INTEGER"); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn(Utility, "runs", "usage_cost_usd", "REAL"); err != nil {
		panic(err)
	}
	// Legacy rows stay NULL: their termination cannot be inferred from state.
	if err := store.RegisterColumn(Utility, "runs", "end_cause", "TEXT"); err != nil {
		panic(err)
	}
	// harness records which binary built or installed served the run (for pi: rebuilt
	// path and series key, or installed path and version). Nullable for other backends
	// and legacy rows.
	if err := store.RegisterColumn(Utility, "runs", "harness", "TEXT"); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn(Utility, "runs", "extension_set", "TEXT"); err != nil {
		panic(err)
	}
	for _, kind := range []string{RecordKind, AllocateKind, FinishKind, UsageKind, CopiedUsageKind, ReleaseKind, ResumeKind, PauseRequestKind, "run.progress", "run.child-session", "run.children-recorded", "run.observation-error"} {
		if err := event.Register(kind, "run"); err != nil {
			panic(err)
		}
	}
	if err := store.RegisterMaterializer(RecordKind, "run", Apply); err != nil {
		panic(err)
	}
}
