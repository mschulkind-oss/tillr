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
reads the event log back, records the store's definition, inventories the store directory,
and checkpoints the store on demand.
It is the utility every other utility's fixture starts from, so its report is a contract
rather than a convenience: one `store.report` record per face describing the store as it
stands after the face ran, except `definition`, which emits `store.definition`, and
`inventory`, which prints text or one `store.inventory` record.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## Read-only inventory

```bash
tillr store inventory --root <repo>
tillr store inventory --root <repo> --json
```

`inventory` reports what the repository's store directory holds; it never retires a
run, caps a file, prunes a reference, creates a store, migrates it, or checkpoints it.
The repository root must contain Git metadata and an existing regular database file.
Missing or unreadable inputs are errors, not an empty inventory. A failure prints a
diagnostic on standard error without a partial report on standard output.

The default output is tab-separated, with quoted names so a tab or newline in a
name cannot create a false row. Every row has a part, name, **grain** (what one count
means), count, bytes when it measures files, and a one-line description:

- **Database tables:** every table, including SQLite's internal tables, gets its
  committed row count in one read transaction. Table rows have no bytes: the database
  file and its coordination files appear separately, avoiding double-counting.
- **Loop files:** files anywhere inside a run are grouped by their final filename,
  even in nested directories. The count is the number of distinct runs containing
  that name, not the number of files. Bytes include every occurrence of the name.
  Loose entries outside a run are separate file rows.
- **Lanes:** each file is named beneath its lane's worktree or other category. Counts
  here are files, not runs. A separate lane totals section has a worktree summary row
  and an other summary row for every lane, even when either is empty. These summarize
  the file rows, not additional bytes: the worktree summary measures the source
  checkout and build outputs, and the other summary measures remaining lane storage.
- **Git references:** references beneath the tillr namespace are grouped by their
  first path segment. Counts are references, not the size of reachable Git objects.
- **Other files:** every remaining file beneath the store directory is named relative
  to that directory. Unknown filenames, table names, and reference groups are printed
  with `undescribed`; they are never excluded from counts or byte sums. This includes
  unknown files in lane worktrees and in the other lane category. Loose loop or lane
  files use a leading `./` in their names to distinguish them from grouped run files
  and lane categories.

Directory totals for the whole store, loop records, and lanes print both the measured
bytes and the sum of their byte-bearing rows. Bytes mean apparent file lengths, not
allocated disk blocks: directory metadata is excluded, hard links are counted at each
path, and symbolic links contribute their own length without following their targets.
The ten largest run directories are listed by descending bytes, with each run id and
its largest file's relative path and bytes. Ties are resolved by name; an empty run has
no largest file.

`--json` emits the same information as one `store.inventory` record. Its payload has
`rows` (part, name, grain, count, optional bytes, description), `totals` (directory,
bytes, row_bytes), and `largest_runs` (run_id, bytes, largest_file, largest_file_bytes).
`lane_totals` holds the worktree and other summary rows, in the same row shape. Only
`rows` contribute to directory byte sums; `lane_totals` and `largest_runs` summarize
those bytes rather than adding more. All lists have deterministic order. No extra
report is appended to the JSON record.

Database reads use SQLite's read-only mode and read-only shared-memory option. This
also prevents reader coordination from changing the shared-memory file's bytes or
modification time while another connection holds a write transaction; committed
changes still in the write-ahead log remain visible. The inventory does not use
SQLite's immutable mode, which would ignore that log. Files and references are
observed separately from the database transaction, so this is not an atomic snapshot
of a changing repository. A file that disappears or becomes unreadable during the
walk is an error, not silently omitted. Nothing changes the existing retention policy.

## State invariants

- **`init` creates the whole schema.** It creates every utility's tables and registered
  columns, not just the store's own tables. A fresh store is ready for `drain why`,
  `view waiting`, `view status`, and `run lanes` without running the loop first.
  `run status --run <id>` reports an absent run with its usual record and exit 1.
  Read faces still never create or migrate tables; writable opens retain the existing
  migration path for older stores.
- **The store uses SQLite WAL.** Every normal writable open selects and verifies
  SQLite's write-ahead log (WAL), so a killed writer's uncommitted changes are invisible
  to readers. The database and its `-wal`/`-shm` coordination files form the live store,
  and the `-wal` file normally holds recent work the database file does not: no program's
  own writes copy it back (see [Durability and checkpoints](#durability-and-checkpoints)).
  Never copy the database file alone. To back up or inspect the store, use one of
  [the supported ways](#backing-up-or-copying-the-store). Read-only opens preserve the
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

## The store's definition

A [definition](https://github.com/mschulkind-oss/tillr/blob/main/tillr/definition/README.md) states one use's vocabulary and rules.
The store records the declaration so utilities can share one answer; it does not yet
make the item utility or drain use that vocabulary in place of their existing rules.

```bash
tillr store init --store checks.sqlite3 --definition checks.json
tillr store definition --store checks.sqlite3
tillr store definition --store checks.sqlite3 --set revised-checks.json
```

`init --definition` reads and validates the file before creating any store or directory.
An invalid or unreadable file is refused with exit 2. Without the flag, initialization
keeps its existing behavior: no declaration or definition event is written. An existing
store's declaration is preserved unless the flag explicitly supplies a replacement.

`definition` emits one `store.definition` record. Its payload contains `definition`
(the name), `version`, `content` (the exact JSON file bytes as a string), `sha256`
(the lowercase SHA-256 hash of those bytes), and `default`. A store with no declaration
returns the embedded Git-landing definition with `default: true`, including stores created
before this face existed. Reading never creates or migrates a store. Missing stores and
corrupt declarations are errors, not requests to create a store or use the default.

`definition --set` validates the new file before opening a writer. It refuses with exit 2
if any item's current state is absent from the new definition and names every affected
item and state. The refusal leaves both the declaration and events unchanged. This check
and the replacement share SQLite's write transaction, so another writer cannot move an
item between them. The declaration uses the existing `meta` table and needs no schema
migration. A successful replacement, including `init --definition`, appends
`store.definition.set` with `old_sha256` and `new_sha256` in the same transaction.
When replacing an implicit default, the old hash names the embedded content. Git, file
reading, and process execution never occur inside that transaction.

The shared Go reader is `definition.ForStore`; it returns the same payload plus a
validated `Parsed` definition for utilities. The core package owns the content parser,
so consumers do not parse stored JSON themselves.

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

**No program's own writes checkpoint.** A *checkpoint* copies the log's frames back into
the database file. After a checkpoint that copied every frame, the next writer *restarts*
the log at the top of the file. Before writing any frame there, it must flush the new log
header, and on a newly opened handle it flushes the store's directory too. Both flushes
happen while that writer holds SQLite's single write lock, so every writer queued behind it
waits them out against its five-second busy timeout. That flush cannot be removed without
giving up SQLite's protection against power loss, so the store decides where it happens:

- Every writable handle runs with `wal_autocheckpoint=0`: no commit checkpoints.
- Every writable handle also holds a *sidecar*, a term this package coins: a read-only
  SQLite connection to the same store, opened before the handle's writer and closed after
  it. SQLite's last connection to close a store checkpoints the log and deletes it, under
  an exclusive lock that a program starting at that moment queues behind; a read-only
  connection's close cannot. So no handle's close is ever the store's last, and no program
  deletes the log. The log is created once, with the store, and stays. The one exception
  is a handle whose sidecar could not be attached ([Known limits](#known-limits)).
- A checkpoint happens in exactly three places, each as one *checkpoint cycle* (below):
  a running drain loop's heartbeat, about once a minute and when the loop stops; the close
  of a writable handle that finds the log grown past 1000 frames (SQLite's own default
  threshold) while no loop runs; and `tillr-store checkpoint`, on demand.

*Checkpoint owner* is a term this package coins. It means a process, a running
`tillr-drain` loop, that checkpoints the store while it runs. Its presence is a shared
`flock` on a file beside the store, `<store>-checkpoint-owner`. The kernel drops the lock
when the process dies, so a crashed loop leaves no owner behind. The file is created with
the database file's permissions, is harmless when left over, and need not be copied with
the store. The loop takes it before its first write, and so before that write's migration,
and holds it until it stops. While it is held, no close checkpoints and
`tillr-store checkpoint` refuses. A close that runs a cycle takes the same file
exclusively, without waiting, for as long as its cycle runs, because two cycles at once
defeat each other's restart. A second close that finds the file held, by a loop or by
another close's cycle, skips its own (`reason=owner-or-cycle-running`) and leaves the
retry to a later close; `tillr-store checkpoint` refuses then too, and a loop that starts
meanwhile waits for that cycle to end before it takes its shared lock. A process that
cannot open or create the file sees no owner; with tracing on, that shows as
`owner-lock.unavailable`.

A *checkpoint cycle*, a term this package coins, is four steps:

1. A small write of the cycle's own, so the log holds a frame no checkpoint has copied.
2. A read transaction begun and held on a read-only connection; this coined term is the
   *pin*. SQLite restarts the log only when it can take reader slots 1 to 4, and a pin
   begun while some frame is uncopied holds one of them, so while it is held no other
   writer can restart the log.
3. A `PASSIVE` checkpoint, which never takes the write lock.
4. A second small write that releases the pin, and that commit restarts the log: one
   header flush, in the cycle's own writer.

The cycle leaves the log holding that second write's frame, uncopied. The database file
therefore holds everything written before the cycle, and no later writer finds a fully
copied log to restart. A cycle can miss the restart when a writer commits between the pin
and the second write; it reports that with the log's length, and the loop tries again at
its next heartbeat. A close does not: the second write also records the log it found (its
salt and its length, in the meta row `checkpoint.last-cycle`), and a later close runs
another cycle only once the log has restarted since or grown another 1000 frames. Without
that record, a log left long by a missed restart made every close run a cycle. A cycle
whose own write waits out the busy timeout behind another writer is skipped and reported,
and so is one whose pin's read does (`pin.busy`: another connection held SQLite's
exclusive lock, as a recovery does); neither faults the loop, because a checkpoint is
maintenance, and the loop tries again at its next heartbeat. Just before its cycle, the loop's heartbeat announces its next pass 30
seconds ahead, so a slow checkpoint does not make the loop look dead to `tillr-drain stop`
or the observatory.

### Who waits behind each flush

These are the flushes left inside a lock, and who can be queued behind each one:

- **A cycle's restart write: one log-header flush, inside SQLite's writer.** A writer that
  arrives during it waits for that one flush. The cycle runs in the loop's heartbeat, in a
  `tillr-store checkpoint`, or in the close of a handle that found the log past 1000 frames,
  after that handle's own work has finished.
- **A cycle's `PASSIVE` checkpoint: a log flush, a database flush and, on a connection's
  first log flush, a directory flush, inside no SQLite lock but the checkpoint lock.** A
  second `PASSIVE` checkpoint that meets that lock answers busy at once rather than
  waiting, so only the caller waits. When a close runs the cycle, any lock the caller still
  holds of its own waits too; a program closes its store handle after its own work, so the
  cycle's flushes delay that program's exit, not another writer.
- **A writer that restarts a fully copied log: up to two flushes (the header and, on a new
  handle, the directory), inside SQLite's writer.** A cycle that finishes leaves one frame
  uncopied, so it leaves no such log. One whose restart write waits out the busy timeout
  does (see [Known limits](#known-limits)), and so does a checkpoint from outside tillr, such
  as the `sqlite3` command-line tool, or an older tillr build.
- **The log's first creation: two flushes (the log and its directory), inside SQLite's
  writer.** It happens once, when the store is created, by `tillr-store init` or by a
  loop's first write after the loop has taken the owner. It happens again only if a program
  outside tillr deleted the log. A tillr program cannot, because its sidecar keeps its
  close from being the store's last.
- **A handle's close: no flush.**

With `TILLR_SQLITE_TRACE=1`, each of these is its own span or phase. The line format is in
[Observing remaining contention](https://github.com/mschulkind-oss/tillr/blob/main/docs/recovery/sqlite-contention/README.md#observing-remaining-contention).

### Backing up or copying the store

The database file alone is current only straight after a checkpoint cycle and before the
next write. Pick one of these:

- **Copy a consistent snapshot through SQLite**, with anything writing or not:
  `sqlite3 'file:/path/to/.tillr/state.db?mode=ro' ".backup /path/to/copy.db"`, or
  `VACUUM INTO '/path/to/copy.db'` on a read-only connection. The copy is one file, with
  everything committed at that moment.
- **Make the database file alone current, then copy it**: with no drain loop running, run
  `tillr-store checkpoint --store /path/to/.tillr/state.db`. Exit 0 means the database
  file holds everything written before it. It stays current until the next write.
- **Copy the three files together** (`state.db`, `state.db-wal`, `state.db-shm`) only
  while nothing has the store open.

`tillr-store check` reports the log's size (`log.bytes`, and `log.frames` written since it
last restarted), so its growth is visible without a tool that reads SQLite's files.

### Known limits

- **One flush remains inside a writer.** Each restart flushes the new log header in the
  cycle's write, while it holds SQLite's writer. A writer queued behind it times out only if
  that single flush takes longer than about 5 seconds. The worst measured on this disk was
  2.1 seconds, and 3.0 seconds under heavier load, but btrfs has recorded a 6.7-second
  transaction commit here. So a `database is locked` behind it is rare, not impossible.
- **The log can grow while writers never pause.** A restart needs a gap with no commit that
  is as long as the checkpoint itself. Writers that never leave such a gap keep the log
  growing, and the loop can only report it and keep trying. A strict bound would need the
  cycle to hold SQLite's writer across the checkpoint's own flushes, which is the same
  shape as the wait this design removes. The owner ruled on 2026-11-16 to accept this
  growth with no loop running as a known limit. Measured on 2026-11-16 with no loop
  running: four programs opening, writing and closing back to back, about 300 a second,
  never let a close's cycle restart the log, and it reached 412 MB in 90 seconds; the
  review's own run of that shape reached 170 to 185 MB in 90 seconds. The first program
  after they stopped restarted it.
- **The log file does not shrink by itself.** A restart writes from the top of the file
  again but keeps its length (no `journal_size_limit` is set, so no writer ever pays to
  truncate it), and the file stays as large as the longest log it has held. To shrink it,
  run `tillr-store checkpoint --truncate --store <path>` while nothing else has the store
  open. It copies the whole log, truncates the file and writes one frame, so the new log's
  header is flushed by that command and not by the next program's writer. It refuses,
  exit 1 and naming the processes, while any other process holds a lock on the store
  (read from `/proc/locks`, so on Linux only), and it never waits for one; a program that
  opens the store in the instant after that check makes the truncate answer busy instead.
  A program that opens the store when no other connection is open rebuilds SQLite's index
  of the log under SQLite's recovery lock, reading only the current log, not the file's
  length. Measured on 2026-11-16 with a 432 MB valid log and nothing else open, on a
  32-core machine whose load average was 37 from other work: that first open read in
  0.11 seconds with the files in the page cache and 0.12 seconds after evicting them, and a
  writer opening 20 ms behind it took 0.10 seconds to open. The review measured 0.10
  seconds at 419 MB. An earlier figure here, 0.9 seconds, was a single reading taken
  without recording the machine's load, and these replace it.
- **The close-time threshold is SQLite's default, 1000 frames.** It was not tuned. A
  smaller one would checkpoint more often at closes; a larger one lets the log, and the
  work the next checkpoint copies, grow further between loops.
- **A dead loop can read as alive for up to 35 seconds after each checkpoint.** The 30-second
  announcement is not shortened when the checkpoint finishes quickly. In that window,
  `tillr-drain stop` from another process domain refuses ("live in another process domain;
  stop it there"), and the observatory shows a current heartbeat.
- **Two loops on one store are two owners.** Nothing makes the owner unique. Each loop's pin
  can delay the other's restart.
- **A cycle whose restart write times out leaves a fully copied log.** The cycle's
  `PASSIVE` checkpoint has already copied every frame when its restart write waits out the
  five-second busy timeout behind another writer. The next writer to arrive then restarts
  the log inside SQLite's writer: one header flush, and on a newly opened handle a
  directory flush, which is the wait the cycle exists to avoid. It needs a writer that held
  SQLite's writer for five seconds or more first. A loop's next heartbeat retries at once
  and restarts the log in its own first write if it comes first; a close does not retry.
  There is no sound fix: the checkpoint would have to copy the last frame only while the
  cycle holds the writer, which puts the checkpoint's flushes inside the writer.
- **A handle without a sidecar can delete the log at its close.** A writable handle whose
  sidecar cannot be attached runs without one, traced as `sidecar.unavailable`. That takes a
  read that waits out the busy timeout during another process's recovery, or a damaged
  store. Its commits still never checkpoint, and its close runs no cycle
  (`reason=no-sidecar`). But if it is the store's last connection, its close is SQLite's own
  last close, which checkpoints and deletes the log under the exclusive lock that a program
  starting at that moment waits behind. The SQLite driver tillr uses (modernc.org/sqlite)
  offers no way to turn that close-time checkpoint off: `SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE` is
  not reachable through it. It does offer `SQLITE_FCNTL_PERSIST_WAL`, which keeps the file
  but not the frames, so the next writer would pay a restart's flushes instead of the
  creation's, which is no better.
- **A close that runs the cycle can delay its own program's exit by seconds on a slow
  disk.** The review measured it on 2026-11-16 with every flush delayed 2.6 seconds: one
  program that opened the store, wrote once and closed it, on an 8 MB log, took 10.86
  seconds to close, with four flushes (the directory, the log, the database and the log
  header). Only the header flush is inside SQLite's writer, so only the closing program
  waits for the rest. It happens at most once per 1000 frames, and only with no loop
  running. No time-budgeted gate check opens the store (`tools/gate` and `tillr-gate` do
  not import the store package), so this is interactive latency, not a gate timeout.

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
- **A `checkpoint` while a drain loop owns the store's checkpoints** — exit 1 with the
  reason on stderr; the loop checkpoints every minute and when it stops.

## Test

`go test ./...` in this directory builds the binary and drives it against a scratch store:
the report's contract keys, the schema refusal on every face, the fixture/live split, the
event cursor, the log's size in `check`, and the `checkpoint` face and its refusal.
