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
  do not copy just the database while a writer is active. Read-only opens preserve the
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
