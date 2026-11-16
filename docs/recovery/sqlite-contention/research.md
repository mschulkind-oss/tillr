---
title: SQLite contention findings
status: source-checked and fixture-measured
---

# SQLite contention findings

Source check and fixture measurements: 2026-11-16. No live database experiment was used.

## Findings

- SQLite admits one writer. `Store.WriteTx` begins IMMEDIATE; WAL readers can continue
  but independent heartbeat writers still wait for that writer.
- Before this repair, `Land` held the writer across history ancestry, squash, clash
  checks, range preservation, Move and receipt. A deliberately stalled `merge-tree`
  caused the independent heartbeat-equivalent write to fail after about five seconds
  with `store: beginning the write transaction: database is locked (5) (SQLITE_BUSY)`.
- Full create-open migration applies registrations from linked owners, including columns,
  indexes, triggers and legacy data. The shared core schema version does not prove all
  these have run. Version-only skipping is rejected.
- Each session refresh and end formerly migrated again. A fixture trigger counted two
  redundant migration attempts after the first successful session refresh.
- Close consults successful landing receipts under the same writer as Land. Moving Git
  outside that final exclusion would allow the owner to close an item that then lands.

## Verdict

Adopt preparation outside the writer, retaining land.lock and final disposition/Move/
receipt exclusion. Snapshot history inputs and refuse their concurrent change. Omit
migration only after successful initialization in the same Loop. Reject timeout increases,
heartbeat suppression and automatic replay of external side effects.

The historical overnight lock holder remains unknown. Healthy integrity after recovery
cannot identify it. [Remaining observation gaps](README.md#observing-remaining-contention)
are explicit rather than evidence of a complete cure.

## Source anchors

- [`store`](../../../tools/tillr/store/): `Open`, `migrate`, `WriteTx`, registrations —
  migration and lock acquisition semantics.
- [`land`](../../../tools/tillr-land/land/): `Land`, `Trunk.Move`, history reconciliation —
  external work and final lifecycle exclusion.
- [`item`](../../../tools/tillr-item/item/lifecycle.go): `Close` — receipt check under the writer.
- [`drain`](../../../tools/tillr-drain/drain/pass_observation.go): `refreshSession`,
  `keepSessionAlive`, `endSession` — initialization and honest heartbeat failures.
