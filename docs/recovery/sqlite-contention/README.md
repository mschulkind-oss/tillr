---
title: SQLite contention recovery
status: implemented, pending independent review
---

# SQLite contention recovery

This recovery shortens a known SQLite writer span without changing the owner's
close/land ordering or pretending a heartbeat was persisted. It does **not** identify
the overnight writer or establish that every remaining source of contention is cured.

Read [research](research.md), [approved plan](plan.md), [task record](tasks.md), and
[QA](qa.md) in that order. The governing constraints remain the
[landing principles](../../design/landing-principles.md) and the
[lander's lifecycle invariants](../../../tools/tillr-land/README.md#state-invariants).

## Implemented boundary

Under the repository's `land.lock`, the lander reads a short database snapshot, then
prepares ancestry, squash, worktree clashes, range preservation and head retention with
no SQLite transaction held across Git. Its final write transaction checks disposition
again, checks the prepared trunk tip and applicable history snapshot, then performs the
final Move and writes the receipt. Close can win during preparation; after final Move
starts, it waits for the receipt and refuses an already-landed item. Changed history or
trunk inputs produce an explicit error without a Move. A retry prepares again from the
start. If this run's landing is already on the trunk, the retry records it and does not
squash it a second time (see the residual below).

Each Loop still performs full owner-aware initialization on its first successful session
refresh. Only that Loop's later heartbeat/end opens omit redundant migration. New Loops
and other create-open entry points retain registered schema and legacy-data migration.
A failed heartbeat is still a fault, never false liveness.

## Observing remaining contention

Set `TILLR_SQLITE_TRACE=1` on the diagnostic process to emit stderr spans, not database
rows. A span (this document's word for one traced store operation, from its `begin` line
to its `end` line) is recorded for a write transaction, for opening a store (ping and WAL
selection) and for a migration. Tracing is off unless the variable is `1`; off, behavior
and allocations match an untraced build. The lander's Git commands print matching
PID/time/repository/subcommand begin/end spans; commit messages and environment values
are omitted, and no SQL values appear anywhere in trace output.

Line kinds, all on the process's stderr:

- `tillr-sqlite ... phase=begin|acquired|end`: UTC nanosecond timestamp, PID, transaction
  ID, store path, operation, elapsed time and error. `begin` is the request, `acquired`
  is the moment the writer was granted, and `end` carries the outcome. A `begin` with no
  `acquired` followed by a busy `end` identifies a waiter, not a proven holder. A `begin`
  with no `end` means the span was in flight when output stopped: the process was killed,
  crashed, or its `end` record was dropped.
- `tillr-sqlite-stack ...`: one all-goroutine stack sample, taken once a span has run for
  2 s, framed per line. It shows what every goroutine in that process was doing; it does
  not name the process holding a lock. A sample over 1 MiB is cut and marked
  `truncated=true`. Taking it pauses the whole process briefly.
- `tillr-sqlite ... dropped spans=N records=N sink_errors=N`: the capture is incomplete.
  Records are queued and written by one delivery goroutine, so a stalled stderr never
  blocks a lock holder; when the queue is full, or more than 32 spans are open, the loss
  is counted and reported here rather than hidden. No such line means nothing was dropped,
  provided the exit flush completed.

Each store program flushes the queue on its way out (a bounded wait of 500 ms), and the
drain forwards the trace of its `sync` child to its own stderr. Correlate a waiter with a
writer's `acquired`-to-`end` interval and the Git command intervals.

What the trace cannot see:

- A holder in a process that did not run with `TILLR_SQLITE_TRACE=1` (`sqlite3`, Python,
  another checkout's binaries): the capture shows only the waiter.
- Writers that are not write transactions or migrations: `Seed`, `WriteMeta`,
  `ForgetMigrations` and `Recover` produce no span. Read transactions and a reader
  occupying a store's only connection are also untraced, so a wait inside the connection
  pool looks like a lock wait with no `acquired` and no SQLite error.
- Children of the drain other than `sync`: their trace lines are discarded, and trace
  volume can push a child's own error out of the 4 KiB stderr tail that fault messages
  quote.
- The lander's `tillr-git` lines are written synchronously; whether any runs under a lock
  has not been traced.
- A process killed by SIGKILL, the out-of-memory killer, or a panic off the main goroutine
  loses its queued records; a stderr stalled at exit loses the remainder and the `dropped`
  line with it.
- Stack samples can show frames of a span that already finished, do not record that
  `migrate` ran inside `open`, and have no measured pause cost.

## Exact residual limitation

The final writer still spans trunk `rev-parse`, Move's worktree discovery, tip checks and
`git merge --ff-only` (or `update-ref`), optional history-retention `update-ref` commands,
and SQLite receipt/event commit. Hooks, storage or scheduling can make these operations
exceed SQLite's unchanged five-second busy timeout. Releasing this exclusion requires a
new close/land serialization design; it is not part of this repair. Git Move and SQLite
receipt commit remain non-atomic: an error, a killed process, or an OS crash after Move can
leave a moved trunk without a receipt. The first failure is reported. The drain then retries
the gated candidate, and that retry is idempotent rather than a second squash:

- Before Move, the lander points `refs/tillr/landed/<run_id>` at the squash commit. That ref
  is the lander's own marker. It is local and never pushed, and the commit itself still
  carries only the head's message.
- On a retry, the marker's commit must be in the trunk's history and must be exactly this
  head's squash onto its own parent. When both hold, the lander writes the missing receipt
  for that commit and does not move the trunk.
- A retry of a landing whose receipt was committed answers that receipt and writes nothing.
  The final writer rechecks for a receipt before it records a recovered landing, so a
  receipt committed during preparation is not recorded twice.
- A squash that would only reproduce the trunk's own tree is refused as `already-on-trunk`.
  It is never published as an empty commit. That refusal is the fallback when the marker
  itself was lost. The drain settles that refusal as `resolved-elsewhere` rather than holding
  the item for a person.
- The drain's per-run boundary comparison excuses the lander's own `refs/tillr/landed/` and
  `refs/tillr/runs/` refs, so a landing during a sibling run convicts nothing.
- The reverse loss is a store that recorded a landing whose commit Git no longer has, because
  Git's defaults flush neither refs nor loose objects. When the drain's `trunk_history` row
  names such a commit, the next landing fails as an external error that names the commit,
  and nothing moves. A store with no `trunk_history` row does not detect it.

The lander's
[state invariants](../../../tools/tillr-land/README.md#state-invariants) state the same
rule, and its
[known cases](../../../tools/tillr-land/README.md#known-cases-the-marker-does-not-settle)
name where the marker's answer is not the trunk's current content.

Pack delivery requires `just pack-sync`, `yolo pack lint yolo-packs/tillr`, then the
human's `yolo host apply --assert` and relaunch. This recovery does not perform deployment
or touch the live database, processes, lanes or refs.
