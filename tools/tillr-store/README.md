# tillr-store — the store every other utility's fixture starts from

> [!WARNING]
> **Known Gaps (verified 2026-11-14 at `04957a8a83`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **The AutoQA handoff skill is not shipped** — `yolo-packs/tillr/skills/` holds
>    `tillr-operating` only, so the design's `tillr-autoqa` handoff has no skill to dispatch.
> 2. **The observatory front end is not built** — `tillr view serve` answers 404 for the UI
>    until the front end under `tools/tillr-view/web` is built and embedded, which is a
>    separate Node step and not part of a Go build.
> 3. **The `tillr` on `PATH` is the pack's rendered tree, not this working tree** — an edit
>    under `tools/tillr*` reaches the launcher only after the pack is mirrored and rendered,
>    so a session that must run its own change builds the working tree first.
>
> **Do not wire `tillr` into another repository's gate while these stand.** The toolset
> ships early so it can be reviewed, not so it can be depended on.

## What this module owns

`tillr-store` creates a store, seeds one from a bundle, verifies the store's own invariants,
and reads the event log back. It is the utility every other utility's fixture starts from,
so its report is a contract rather than a convenience: one `store.report` record per face,
describing the store as it stands after the face ran.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

## State invariants

- **The store uses SQLite WAL.** Every normal writable open selects and verifies
  SQLite's write-ahead log (WAL), so a killed writer's uncommitted changes are invisible
  to readers. The database and its `-wal`/`-shm` coordination files form the live store;
  do not copy just the database while a writer is active, and above all not while a drain
  loop runs, because no other process copies the log into the database then (see
  [Durability and checkpoints](#durability-and-checkpoints)). Read-only opens preserve the
  journal mode, never migrating or repairing it. A schema version this binary does not
  support is refused rather than migrated in place.
- **A fixture store is seedable and a live store is not.** `init` decides which one it
  made; a live store refuses `seed` outright, because a store that has been drained is not
  a store to replace.
- **Events are append-only.** Two triggers refuse an update and a delete, and a refused
  write takes the whole transaction with it, so no row is left describing an event that
  did not happen.
- **`check` reads the store, it does not repair it.** A non-zero answer means nothing built
  on that store is trustworthy until a person looks.
- **`events` writes back the cursor it read to**, so a caller resumes from the last record
  it saw instead of re-reading the log.

## Durability and checkpoints

**Commits do not wait for the disk.** Every writable open runs SQLite with
`synchronous=NORMAL`: a commit is appended to the write-ahead log and returns without
flushing it to disk, and the operating system's writeback puts it on disk, typically
within 30 to 60 seconds. Only an operating-system crash or a power cut can lose the most
recent commits, and it rolls them back cleanly rather than corrupting the store. Killing a
process, a container or a jail loses nothing, because the commit is already in the kernel's
page cache. The owner ruled on 2026-11-16 that `NORMAL` stays. The default, `FULL`, flushed
the disk inside every commit while holding SQLite's single write lock, so one slow flush
became a wait for every other writer, against its five-second busy timeout.

**While a drain loop runs, only the loop checkpoints.** A checkpoint copies the log's frames
back into the database file. After a checkpoint that copied every frame, the next writer
restarts the log at the top of the file. Before writing any frame there, it must flush the
new log header, and on a newly opened handle it flushes the store's directory too. Both
flushes happen while that writer holds the write lock. That flush cannot be removed
without giving up SQLite's protection against power loss, so the loop decides who pays
it:

- *Checkpoint owner* is a term this package coins. It means a process, a running
  `tillr-drain` loop, that checkpoints the store while it runs. Its presence is a shared
  `flock` on a file beside the store, `<store>-checkpoint-owner`. The kernel drops the lock
  when the process dies, so a crashed loop leaves no owner behind. The file is created
  with the database file's permissions, is harmless when left over, and need not be copied
  with the store. A process that cannot open or create it sees no owner and checkpoints by
  SQLite's default; with tracing on, that shows as `owner-lock.unavailable`.
- While an owner holds that lock, every other writable handle sets
  `wal_autocheckpoint=0` before each write transaction, so no other commit checkpoints.
- About once a minute, on its heartbeat, the loop runs one *checkpoint cycle*, a term this
  package coins for the following steps. First the loop makes a small write of its own,
  so the log holds a frame no checkpoint has copied. Then it begins a read transaction and
  holds it; this coined term is the *pin*. Then it runs a `PASSIVE` checkpoint, which never
  takes the write lock. SQLite restarts the log only when it can take reader slots 1 to 4.
  A pin begun while some frame is uncopied holds one of those slots, so while it is held,
  no other writer can restart the log. (A pin begun on a log that is already fully copied
  takes slot 0, which a restart does not need, and protects nothing. The loop's first write
  is what rules that out. Such a log is left by a checkpoint from outside this policy: an
  older `tillr` build, the `sqlite3` command-line tool, or another loop's closing
  checkpoint. The first write then restarts the log itself, in the loop's writer; in the
  loop's first cycle that restart can flush the directory too.) Last, the loop releases the pin inside a second small write, and that commit
  restarts the log: one header flush, in the loop's writer, never the directory's.
- The restart needs no commit between the pin and the loop's second write. A writer that
  commits in that gap keeps the log appending. The cycle then reports that the log did not
  restart, with its length, and the loop tries again at its next heartbeat rather than a
  minute later. The loop's own log says so on the first miss, when the log passes 64 MiB,
  at each doubling after that, and when a restart lands again.
- A cycle whose own write waits out SQLite's busy timeout behind another writer is skipped
  and tried again at the next heartbeat. It is reported, and it never faults the loop: a
  checkpoint is maintenance.
- Just before checkpointing, the heartbeat announces its next pass 30 seconds ahead, so
  a slow checkpoint does not make the loop look dead to `tillr-drain stop` or the
  observatory.
- When the loop stops, it checkpoints the log. The database file alone then holds
  everything, including the session's end, provided no reader held frames back (the loop
  reports a closing checkpoint that copied only part of the log). That stays true only until
  the next write by any program.

With no loop running, every handle checkpoints as SQLite does by default, at 1000 log
pages. With `TILLR_SQLITE_TRACE=1`, every write records which policy it ran under, and the
owner's cycle, its two writes and its closing checkpoint are their own spans. The line
format is in
[Observing remaining contention](../../docs/recovery/sqlite-contention/README.md#observing-remaining-contention).

### Known limits

- **One flush remains inside a writer.** Each restart flushes the new log header in the
  loop's write, while it holds SQLite's writer. A writer queued behind it times out only if
  that single flush takes longer than about 5 seconds. The worst measured on this disk was
  2.1 seconds, and 3.0 seconds under heavier load, but btrfs has recorded a 6.7-second
  transaction commit here. So the heartbeat's `database is locked` (U1) is rare now, not
  impossible.
- **The log can grow while writers never pause.** A restart needs a gap with no commit that
  is as long as the checkpoint itself, which flushes the log and then the database. Writers
  that never leave such a gap keep the log growing, and the loop can only report it and keep
  trying. A strict bound would need the loop to hold SQLite's writer across the last part of
  the checkpoint and the restart. Every other writer would then wait up to three flushes, 6.3
  seconds at this disk's worst measured flush, which is past the 5-second timeout and the
  same shape as U1. That has not been built; the owner has not ruled on it.
- **A dead loop can read as alive for up to 35 seconds after each checkpoint.** The 30-second
  announcement is not shortened when the checkpoint finishes quickly. In that window,
  `tillr-drain stop` from another process domain refuses ("live in another process domain;
  stop it there"), and the observatory shows a current heartbeat.
- **Two loops on one store are two owners.** Nothing makes the owner unique. Each loop's pin
  can delay the other's restart, and one loop's closing checkpoint can leave a fully copied
  log that the next writer restarts with two flushes while the other loop still owns.

## Standalone invocation

```sh
tillr-store init
tillr-store check
```

An external caller needs no other binary for these two: `init` and `check` compose on their
own, and `check` is the one command that says whether a store is worth reading at all.

## Recovering an interrupted writer

An older rollback-mode store can carry a **hot rollback journal**, SQLite's saved pages
needed to undo a dead writer's uncommitted transaction. A read-only open then fails with
`SQLITE_READONLY_ROLLBACK` (extended code 776), not a file-permission failure. Run:

```sh
tillr-store recover --store /path/to/.tillr/state.db
tillr-store check --store /path/to/.tillr/state.db
```

`recover` acquires SQLite's write lock before reading the schema, so a live writer is
waited for, never interrupted or mistaken for a dead one. SQLite performs any needed
rollback; the command never deletes journals itself, migrates, or changes journal mode.
Its `store.report` names `journal_found` when a rollback journal existed and gives the
last committed `event_head_seq`. With no rollback journal it opens read-only and writes
nothing; repeating recovery therefore leaves the store unchanged. The next normal
writable open selects WAL. After repair, the observatory's **Read it again** retries the
failed open without restarting the server.

## What it refuses

- **A store path it cannot open** — a missing file or one it may not read is the caller's
  mistake about the path, not a schema mismatch.
- **A schema version this binary does not support** — reported as a version, not as a
  broken store.
- **A `seed` against a live store** — refused before anything is written.
- **A face it does not have** — refused, naming the faces it does.

## Test

`go test ./...` in this directory builds the binary and drives it against a scratch store:
the report's contract keys, the schema refusal on every face, the fixture/live split, and
the event cursor.
