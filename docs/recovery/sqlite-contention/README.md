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
trunk inputs produce an explicit error without a Move, not automatic replay.

Each Loop still performs full owner-aware initialization on its first successful session
refresh. Only that Loop's later heartbeat/end opens omit redundant migration. New Loops
and other create-open entry points retain registered schema and legacy-data migration.
A failed heartbeat is still a fault, never false liveness.

## Observing remaining contention

Set `TILLR_SQLITE_TRACE=1` on the diagnostic process to emit stderr spans, not database
rows. SQLite migrations and write transactions print begin/acquired/end with UTC
nanosecond timestamps, PID, transaction ID, store path, operation, elapsed time and error.
The lander's Git commands print matching PID/time/repository/subcommand begin/end spans;
commit messages and environment values are omitted. A missing acquired phase followed by
a busy error identifies a waiter, not a proven lock holder. Correlate it with a writer's
acquired-to-end interval and Git command intervals. Uninstrumented/external writers and
WAL-mode selection are not covered by these spans.

## Exact residual limitation

The final writer still spans trunk `rev-parse`, Move's worktree discovery, tip checks and
`git merge --ff-only` (or `update-ref`), optional history-retention `update-ref` commands,
and SQLite receipt/event commit. Hooks, storage or scheduling can make these operations
exceed SQLite's unchanged five-second busy timeout. Releasing this exclusion requires a
new close/land serialization design; it is not part of this repair. Git Move and SQLite
receipt commit remain non-atomic: an error after Move can leave a moved trunk without a
receipt and is reported, never automatically replayed.

Pack delivery requires `just pack-sync`, `yolo pack lint yolo-packs/tillr`, then the
human's `yolo host apply --assert` and relaunch. This recovery does not perform deployment
or touch the live database, processes, lanes or refs.
