---
title: "Store contention — the lander's write boundary and tracing a writer"
status: accepted
stage: CURRENT
tags: [tillr, store, sqlite, reference]
summary: "How tillr-land keeps Git work outside SQLite write transactions, how TILLR_SQLITE_TRACE=1 shows who waits for the store's writer and who holds it, and the one span the final landing write holds across Git."
---

# Store contention — the lander's write boundary and tracing a writer

> **Delivered copy.** This is `docs/reference/tillr-store-contention.md` from this repository at commit `282386b687` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-store-contention.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

The store is one SQLite file with one writer at a time. This page says how long the lander
holds that writer, how to see a writer and its waiters, and which span is held across
Git. The governing rules are the [landing principles](../design/landing-principles.md) and the
[lander's lifecycle invariants](../../tillr-land/README.md#state-invariants).

## The lander's write boundary

Under the repository's `land.lock`, the lander reads a short database snapshot, then
prepares ancestry, squash, worktree clashes, range preservation and head retention with
no SQLite transaction held across Git. Its final write transaction checks disposition
again, checks the prepared trunk tip and applicable history snapshot, then performs the
final Move and writes the receipt. Close can win during preparation; after final Move
starts, it waits for the receipt and refuses an already-landed item. Changed history or
trunk inputs produce an explicit error without a Move. A retry prepares again from the
start. If this run's landing is already on the trunk, the retry records it and does not
squash it a second time (see [the residual limitation](#the-residual-limitation)).

Each Loop performs full owner-aware initialization on its first successful session
refresh. Only that Loop's later heartbeat and end opens omit the migration. New Loops
and other create-open entry points run the registered schema and data migrations.
A failed heartbeat is a fault, never false liveness.

## Observing remaining contention

Set `TILLR_SQLITE_TRACE=1` on the diagnostic process to emit stderr spans, not database
rows. A span (this document's word for one traced store operation, from its `begin` line
to its `end` line) is recorded for a write transaction, for opening a store (ping, WAL
selection and the sidecar), for a writable handle's close, and for a migration. Tracing
is off unless the variable is `1`; off, behavior and allocations match an untraced build.
The lander's Git commands print matching PID/time/repository/subcommand begin/end spans; commit messages and environment values
are omitted, and no SQL values appear anywhere in trace output.

Line kinds, all on the process's stderr:

- `tillr-sqlite ... phase=begin|acquired|end`: UTC nanosecond timestamp, PID, transaction
  ID, store path, operation, elapsed time and error. `begin` is the request, `acquired`
  is the moment the writer was granted, and `end` carries the outcome. A `begin` with no
  `acquired` followed by a busy `end` identifies a waiter, not a proven holder. A `begin`
  with no `end` means the span was in flight when output stopped: the process was killed,
  crashed, or its `end` record was dropped.
- An open records `sidecar.begin` and `sidecar.end` around its *sidecar*, the read-only
  connection every writable handle holds so its close is never the store's last (a term
  defined in
  [tillr-store's Durability and checkpoints](../../tillr-store/README.md#durability-and-checkpoints)).
  `sidecar.unavailable` means the handle has none, so its close may checkpoint and delete
  the log like any SQLite connection's last close when it is the store's last, and its close runs
  no cycle (`reason=no-sidecar`).
- `phase=owner-lock.unavailable`, inside an open: the process could not open or create the
  *checkpoint owner*'s lock file (a term defined in the same section), so it sees no owner.
- `operation="close"` is a writable handle's close, its own span. Its first phase is the
  checkpoint decision: `log-checkpoint.skipped` (`reason=owner-writer`, `no-sidecar`,
  `owner-or-cycle-running` (a loop owns the store, or another close is running its cycle
  and holds the owner's lock file exclusively for it), or `cycled-at-this-log` with
  `log_frames=`: a cycle already ran on this log and it has not grown 1000 frames since), `log-checkpoint.not-due` (`log_frames=`,
  `known=`), or `log-checkpoint.due`. Then `writer.close.begin`/`end` and `sidecar.close.begin`/`end`.
  A close holds no SQLite lock and flushes nothing unless it is due.
- A due close is followed by a `log-checkpoint` span: one *checkpoint cycle* (defined in
  the same section), with write spans `log-checkpoint.pre-write` and
  `log-checkpoint.restart-write`.
- The owner's own work is three plainly named operations; `tillr-store checkpoint` adds a
  fourth, and `tillr-store checkpoint --truncate` a fifth.
  - `checkpoint-owner.checkpoint` is the loop's cycle. Its phases are `pin.begin`,
    `pin.end`, `checkpoint.begin`, then `checkpoint.complete`, `.partial` or `.busy`. It
    ends with the cycle's outcome: `log.restarted`, `log.not-restarted`, `writer.busy`
    when one of the cycle's writes waited out the busy timeout, or `pin.busy` when the pin's
    read did (another connection held SQLite's exclusive lock, as a recovery does); a busy
    pin skips the checkpoint, so its span has no `checkpoint.*` phase. The outcome line carries
    `log_frames=`, `copied_frames=` and `log_bytes=` after the error field.
    `checkpoint.complete` alone does not mean the log restarted. The same phases appear in
    `log-checkpoint` and in `checkpoint-owner.close`.
  - The cycle's own phases hold no writer, so their flushes delay only the cycle's caller.
    The span contains two write spans, `checkpoint-owner.pre-write` and
    `checkpoint-owner.restart-write`, which are ordinary write spans. They are the only
    writes meant to pay a log restart's header flush while holding the writer: the restart
    write in each cycle, and the pre-write when a checkpoint from outside tillr left the
    log fully copied.
  - `checkpoint-owner.close` is the owner's closing cycle, then `owner-lock.released`,
    `close.begin` and `close.end`, traced before the exit flush.
  - `store.checkpoint` is `tillr-store checkpoint`: `owner.absent` and the owner's own spans
    inside it, or `owner.running` and the refusal.
  - `store.truncate` is `tillr-store checkpoint --truncate`: `owner.absent` or
    `owner.running`, then `in-use` (with `pids=`) and the refusal, or `not-in-use`,
    `truncate.begin`, `truncate.end` and the write span `store.truncate.write`.
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

## The residual limitation

The final writer spans trunk `rev-parse`, Move's worktree discovery, tip checks and
`git merge --ff-only` (or `update-ref`), optional history-retention `update-ref` commands,
and SQLite receipt/event commit. Hooks, storage or scheduling can make these operations
exceed SQLite's five-second busy timeout. Releasing this exclusion requires a
new close/land serialization design. Git Move and SQLite
receipt commit are not atomic: an error, a killed process, or an OS crash after Move can
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
[state invariants](../../tillr-land/README.md#state-invariants) state the same
rule, and its
[known cases](../../tillr-land/README.md#known-cases-the-marker-does-not-settle)
name where the marker's answer is not the trunk's current content.
