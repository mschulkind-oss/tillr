---
title: "Restarting the store's write-ahead log under dense in-process loop reads"
status: accepted
stage: DECIDED
created: 2026-11-28
updated: 2026-11-28
depends-on:
  - ../reference/tillr-store-contention.md
  - ./no-open-questions-in-references.md
---

# Restarting the store's write-ahead log under dense in-process loop reads

## Summary

The drain loop owns periodic checkpoints of the SQLite store through `CheckpointOwner.Checkpoint` in [`tools/tillr/store/checkpoint.go`](../../tools/tillr/store/checkpoint.go). Its current three-step cycle (`pre-write`, `pin`, `PRAGMA wal_checkpoint(PASSIVE)`, `restart-write`) relies on SQLite's passive checkpoint and `_walRestartLog` finding two brief reader-free windows on its own: no reader holding a snapshot older than `pre-write` when `PASSIVE` copies frames, and no reader holding any of SQLite's four write-ahead log (WAL) reader slots (`WAL_READ_LOCK(1..4)`) at the instant `restart-write` commits. When a single `*Loop` process issues dense, overlapping short `store.OpenRead` transactions — even with zero external processes touching the store and every read closing promptly — reader slots remain occupied across one-minute cycles, `drain.checkpoint-not-restarted` fires in long streaks, and `.tillr/state.db-wal` never resets to frame 1.

This document owns the design of the store's checkpoint-restart cycle while the SQLite store remains in service alongside the longer-horizon per-item log migration in [`docs/design/tillr-storage.md`](./tillr-storage.md). Shipped reference prose lives in [`tools/tillr-store/README.md`](../../tools/tillr-store/README.md#durability-and-checkpoints) and [`docs/reference/tillr-store-contention.md`](../reference/tillr-store-contention.md#observing-remaining-contention); per [`R-NOQ1`](./no-open-questions-in-references.md#R-NOQ1), unbuilt checkpoint changes live here under `docs/design/` and graduate into those references as slices land. Every mechanism, payload field, test contract, and rejected alternative settled below (`D-SCR1` through `D-SCR5` in [§8](#8-decision-ledger)) is the **design seat's decision** (arising from an operator-filed request, not an owner ruling), which the owner may overturn.

---

## 1. Measured Failure and SQLite WAL Restart Mechanics

### 1.1 What was measured in live loops and prior attempts

1. **Sustained non-restart streaks with no external reader.** In a consumer repository, a running `tillr-drain` loop recorded `drain.checkpoint-not-restarted` on `69` of `69` consecutive one-minute cycles, growing `.tillr/state.db-wal` to `14,981` frames (`61.8 MB`) with no other process holding the store. In this repository, every one of `29` loop sessions after `2026-11-27T21:40` ET recorded `drain.checkpoint-not-restarted` in streaks of `3` to `14` cycles, leaving `.tillr/state.db-wal` at `123.7 MB`.
2. **Dense short in-process holds, not leaked transactions.** A read-only sample of this repository's live loop found the loop holding a SQLite reader slot `86%` of the time across `107` short holds rather than holding any single transaction across cycles. A static audit of `tools/tillr/store` and `tools/tillr-drain` found every read transaction rolled back, every `*sql.Rows` closed, and no read callback waiting on a channel or lock.
3. **Why static history size alone did not reproduce the streak.** A synthetic fixture of `300` items and `200,000` events restarted `6` of `8` cycles both on the previous published build and on the current trunk (`ed16ce2ca62b`), confirming that history volume alone does not cause `69` of `69` misses: concurrent short reads across the loop's pass, poll-reader, coordinator, and background goroutines do. Notice that `rg 'WithReadConnections' tools/tillr-drain` returns nothing on `ed16ce2ca62b`: `*Loop` does not share a single pooled read handle, and instead opens independent `store.OpenRead(ctx, l.cfg.Store)` handles (`tools/tillr-drain/drain/pass.go`, `restart_work.go`, `push_state.go`) that contend across SQLite's four shared WAL reader slots in `.db-shm`.
4. **Why the prior unlanded build attempt was rejected.** The closed build item's last run (kept at `refs/tillr/preserved/run-item-request-6632147629-a4-n6`, commit `ec96ac20d975` `[unlanded]`, following initial instrumentation in `396dd08bc4f2` `[unlanded]`) registered in-process reads and made `CheckpointOwner.Checkpoint` call an unbounded `awaitReadsClosed(ctx)` before `pre-write` and `restart-write` without holding new reads back. Verification rejected that change because `awaitReadsClosed(ctx)` waited only on `ctx.Done()`: a single in-process read left open on any goroutine stalled `(*Loop).checkpointIfDue` forever on the `keepSessionAlive` goroutine, preventing `(*Loop).beat` from refreshing `drain_sessions.refreshed_at` and expiring the loop's own `180 s` session lease (`SessionGrace`).
5. **Measured 60-second cadence collision with process central processing unit (CPU) telemetry.** In `(*Loop).keepSessionAlive` ([`tools/tillr-drain/drain/pass_observation.go`](../../tools/tillr-drain/drain/pass_observation.go)), `l.recordProcessCPUIfDue(ctx)` runs immediately ahead of `l.checkpointIfDue(ctx)`, and both share a `time.Minute` interval (`processCPUInterval` and `CheckpointInterval`). Appending `drain.process-cpu` enters the `default` branch of `topicsForEvent` in [`tools/tillr/store/notify.go`](../../tools/tillr/store/notify.go) and publishes `queue.changed`, waking `(*Loop)`'s own wildcard notify subscriber (`notify.Subscribe(ctx, l.cfg.Store, "*")` in `tools/tillr-drain/drain/trigger.go`) into a pass read at the exact moment `CheckpointOwner.Checkpoint` starts.

### 1.2 Exact SQLite conditions for `_walRestartLog` in `modernc.org/sqlite`

Today's checkpoint cycle (`CheckpointOwner.ownerCycle` in [`tools/tillr/store/checkpoint.go`](../../tools/tillr/store/checkpoint.go), introduced in `76645cc6` and `27c98dfa`) disables automatic commit-time checkpoints (`wal_autocheckpoint = 0` and `SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE` in [`tools/tillr/store/open.go`](../../tools/tillr/store/open.go)) and runs four steps on `o.writer` and `o.keeper`:

1. `pre-write`: updates `meta(key = 'checkpoint.restarts')` so `mxFrame > nBackfill` (`at least one uncopied frame`).
2. `pin`: opens a read-only transaction on `o.keeper.db` (`SELECT count(*) FROM sqlite_master`). In `_walTryBeginRead` (`modernc.org/sqlite` `v1.61.0`), because `nBackfill < mxFrame`, SQLite skips `WAL_READ_LOCK(0)` and places `pin` on `WAL_READ_LOCK(1..4)` with `aReadMark[i] = mxFrame`, preventing any external writer that commits before `restart-write` from stealing the WAL restart and paying a synchronous header flush.
3. `checkpoint`: runs `PRAGMA wal_checkpoint(PASSIVE)` on `o.writer.db` outside any write transaction. In `_walCheckpoint`, `PASSIVE` clamps `mxSafeFrame` down to any occupied `WAL_READ_LOCK(1..4)` slot whose `aReadMark[i] < mxFrame`, copies frames `nBackfill+1 .. mxSafeFrame` into the main database file, and syncs the WAL and main database files while writers and readers run freely.
4. `restart-write`: begins `BEGIN IMMEDIATE` on `o.writer.db`, rolls `pin` back inside the write lock, and updates `meta(key = 'checkpoint.last-cycle')` and `checkpoint.restarts`.

Inside `modernc.org/sqlite` `v1.61.0` (`_walFrames` -> `_walRestartLog`), step 4 resets the WAL header to frame 1 with a new salt **if and only if both conditions hold simultaneously**:

- **Condition A (`pWal->readLock == 0`, meaning `nBackfill == mxFrame` when `restart-write` began):** `PASSIVE` in step 3 copied **every** frame up to `mxFrame` (no reader held a snapshot older than `pre-write` during step 3), **and** no other writer committed a new frame between step 3 (`PASSIVE`) and step 4 (`restart-write`'s `BEGIN IMMEDIATE`). Otherwise `restart-write` itself takes `WAL_READ_LOCK(1..4)` instead of `WAL_READ_LOCK(0)`, and `_walRestartLog` returns `SQLITE_OK` immediately without attempting a restart.
- **Condition B (`_walLockExclusive(pWal, WAL_READ_LOCK(1), WAL_NREADER-1)` succeeds on one non-blocking try):** at the instant `restart-write` commits (`FnBackfill > 0`), **zero** connections hold a shared lock on any of `WAL_READ_LOCK(1..4)` — meaning every reader that started before `PASSIVE` finished updating `nBackfill` (and therefore bound to `WAL_READ_LOCK(1..4)` rather than `WAL_READ_LOCK(0)`) has already closed its transaction or row set. Any single active reader on `WAL_READ_LOCK(1..4)` makes `_walLockExclusive` return `SQLITE_BUSY`, which `_walFrames` masks (`rc == SQLITE_BUSY` -> `rc = SQLITE_OK`), appending `restart-write` at the tail of the growing WAL instead of restarting frame 1.

Under dense in-process short reads (`86%` aggregate slot occupancy across concurrent loop goroutines), either a short read started before `pre-write` overlaps `PASSIVE` (breaking Condition A), a loop write commits between `PASSIVE` and `restart-write` (breaking Condition A), or a short read started during `PASSIVE` overlaps `restart-write` (breaking Condition B).

---

## 2. Weighed Candidates ([`D-SCR1`](#D-SCR1), [`D-SCR2`](#D-SCR2))

The design seat weighed four candidates against three invariants:

- **Writer-latency invariant (`76645cc6`):** bulk WAL-to-database copying and `fsync` calls must never run while holding `WAL_WRITE_LOCK` or blocking `writeQueue`.
- **Heartbeat liveness invariant (Criterion 6):** an in-process read that stays open indefinitely must never stall `(*Loop).keepSessionAlive` or `(*Loop).beat`.
- **Restart determinism under short reads (Criterion 5):** dense self-completing in-process reads (`~1–20 ms` each) and concurrent in-process writes must reliably yield `Restarted == true` without requiring an idle loop.

| Candidate | Restart under dense short reads | Effect on writers during bulk flush | Effect when an in-process read stays open | Verdict (design seat) |
|---|---|---|---|---|
| **Option A (chosen): Bounded two-beat in-process `readFence` + `writeQueue` tail catch-up + checkpoint-before-CPU ordering** | **Guaranteed** whenever active reads finish within the `250 ms` cycle fence budget | **None:** bulk `PASSIVE` runs with `readFence` open and `writeQueue` unheld | **Bounded to `250 ms` per cycle** on an independent timer; fence re-opens immediately on timeout | **Selected ([`D-SCR1`](#D-SCR1), [`D-SCR2`](#D-SCR2), [`D-SCR3`](#D-SCR3))** |
| **Option B: Single continuous read gate held across the entire checkpoint cycle** | Guaranteed when reads finish within budget | Holds every in-process reader back across bulk `PASSIVE` disk flushes (tens to hundreds of milliseconds on a large WAL) | Bounded if timed, else stalls readers and heartbeat across slow disk I/O | **Rejected:** blocks readers unnecessarily during bulk `PASSIVE`, when new snapshots after `pre-write` are already safe |
| **Option C: `PRAGMA wal_checkpoint(RESTART)` with `busy_timeout`** | Blocks on `WAL_READ_LOCK(1..4)` up to `busy_timeout` | **Violates `76645cc6`:** `_walCheckpoint(RESTART)` acquires `WAL_WRITE_LOCK` up front and holds it across frame copying, WAL/DB `fsync`s, and reader busy-waits; also leaves WAL-header `fsync` to the next writer | Holds `WAL_WRITE_LOCK` for full `busy_timeout` (`15 s` on loop handles), stalling heartbeats, closes, and landings with `SQLITE_BUSY` | **Rejected** |
| **Option D: Reducing per-pass read density / sharing one read snapshot alone** | **Not guaranteed:** lowers mean occupancy without coordinating background pollers, coordinators, trigger wakes, or concurrent passes | None | None | **Rejected as sole mechanism** (compatible as future optimization; cannot satisfy `_walRestartLog` deterministically) |

### 2.1 Why Option C (`PRAGMA wal_checkpoint(RESTART)` with `busy_timeout`) is rejected

In `modernc.org/sqlite` `v1.61.0` (`_walCheckpoint` and `_walSehCheckpoint`), `eMode != SQLITE_CHECKPOINT_PASSIVE` (`FULL`, `RESTART`, `TRUNCATE`) executes `_walLockExclusive(pWal, WAL_WRITE_LOCK, 1)` **before** inspecting reader marks or copying a single page, and holds `WAL_WRITE_LOCK` continuously across:

1. busy-waiting via `xBusy` (`busy_timeout`) for older readers before copying frames;
2. copying every unbackfilled frame into the database file and running `sqlite3OsSync` on both the WAL and the main database file; and
3. for `RESTART` and `TRUNCATE`, busy-waiting a second time via `xBusy` for all four `WAL_READ_LOCK(1..4)` slots to clear.

Worse, `PRAGMA wal_checkpoint(RESTART)` does not even write or `fsync` the new 32-byte WAL header itself: it only resets `nBackfill` and reader marks, pushing `_walRestartHdr`'s synchronous header `fsync` onto whichever ordinary `WriteTx` commits next — the exact multi-second writer stall commit `76645cc6` eliminated (`tools/tillr-store/README.md`, [Durability and checkpoints](../../tools/tillr-store/README.md#durability-and-checkpoints)). And if any reader stays open, `RESTART` holds `WAL_WRITE_LOCK` across the full `busy_timeout` (`15 s` on writable stores, `5 s` on checkpoint writer), blocking every other writer in the process and outside it.

### 2.2 Why Option D (reducing per-pass read density alone) is rejected as the restart guarantee

Sharing one `*store.Reader` snapshot across repeated queries within a pass reduces query count and CPU cost, and remains worthwhile for pass efficiency, yet it cannot make `CheckpointOwner.Checkpoint` deterministic:

1. `*Loop` runs multiple independent actors concurrently with `passMode`: `keepSessionAlive`, stop/restart pollers (`withPollReader`), examination/judge goroutines, `trigger.go` wake handlers, and post-landing remote-sync readers.
2. `_walRestartLog` makes a **single non-waiting** `_walLockExclusive` probe at `restart-write` commit. Even if pass snapshot sharing cut aggregate slot occupancy from `86%` to `30%`, concurrent goroutines still overlap `pre-write` or `restart-write` stochastically with no upper bound on streak length under load.
3. Conversely, once a bounded two-beat in-process fence holds new reads back only across the two micro-windows where SQLite inspects reader marks (`< 10 ms` under short reads), bulk `PASSIVE` still runs concurrently with readers, and restart succeeds even at `99%` short-read duty cycle.

### 2.3 Why Option B (holding one continuous fence across bulk `PASSIVE`) is inferior to Option A's two beats

SQLite's `_walCheckpoint(PASSIVE)` only stops at reader marks strictly older than `mxFrame` (`mxSafeFrame > aReadMark[i]`). Any reader that begins **after** `pre-write` commits sees `mxFrame >= mxFrame_preWrite`, so its snapshot never prevents `PASSIVE` from copying `1..mxFrame_preWrite` to the main database file. Keeping the in-process fence closed during bulk `PASSIVE` would stall loop reads for the entire disk copy and two full `fsync` calls (`state.db-wal` and `state.db`) for zero correctness benefit. Splitting the fence into two brief beats — Beat 1 around `pre-write` + `pin`, Beat 2 around `restart-write` — keeps bulk disk I/O completely unfenced.

---

## 3. Chosen Mechanism: Two-Beat Bounded `readFence`, Tail Catch-Up, and Heartbeat Ordering ([`D-SCR1`](#D-SCR1), [`D-SCR2`](#D-SCR2), [`D-SCR3`](#D-SCR3))

### 3.1 Process-wide per-store `readFence` at the SQLite connector boundary

Every `*sql.DB` opened by `tools/tillr/store` already passes through `openSQLite` and `countingConn` in [`tools/tillr/store/read_counters.go`](../../tools/tillr/store/read_counters.go). `tools/tillr/store` adds a process-wide registry of `*readFence` instances keyed by canonical store path (`canonicalStorePath(path)`: `filepath.Abs` + `filepath.EvalSymlinks` when resolvable + `filepath.Clean`, matching `writeQueueFor(path)` in [`tools/tillr/store/writequeue.go`](../../tools/tillr/store/writequeue.go) and `lockPath(path)` in [`tools/tillr/store/checkpoint.go`](../../tools/tillr/store/checkpoint.go)).

`openSQLite` accepts the store's canonical path and connection role (`ordinaryConn`, `stampConn`, or `checkpointInternalConn`):

1. **Connections governed by `readFence` (`ordinaryConn` and `stampConn`):**
   - **Read-only transactions (`countingConn.BeginTx` with `opts.ReadOnly == true`):** acquires one `readFence` lease (`kind = "read_tx"`) before calling the underlying SQLite `BeginTx`, marks `c.inTx = true`, and releases the lease inside `trackedTx.Commit()` / `trackedTx.Rollback()` **strictly after** the underlying `driver.Tx` `Commit` or `Rollback` returns (with `ResetSession` and `Close` as idempotent `sync.Once` fallbacks). Because `modernc.org/sqlite` unlocks `WAL_READ_LOCK(1..4)` synchronously inside `Rollback`/`Commit` (`_sqlite3WalEndReadTransaction`), `activeReads` reaches `0` only after `.db-shm` reader locks are already free.
   - **Top-level autocommit queries (`countingConn.QueryContext`, `countingStmt.QueryContext`, `countingStmt.Query` when `!c.inTx`):** acquires one `readFence` lease (`kind = "query"`) before running the query and releases it inside `countingRows.Close()` **strictly after** `r.Rows.Close()` returns (or immediately if `QueryContext` errors). Queries executed inside an already-open `ReadTx` or `WriteTx` see `c.inTx == true` and run without touching `readFence`. `stampConn` (`s.stampDB` in `(*Store).readTx`) participates in fence admission and active-read draining, and suppresses duplicate `read_starts` counting because the immediately following `db.BeginTx(ReadOnly: true)` records that `ReadTx`'s call site.
2. **Connections and contexts that never wait at `readFence` (self-deadlock prevention):**
   - **Checkpoint-owner connections (`checkpointInternalConn`):** `o.writer.db`, `o.writer.sidecar`, and `o.keeper.db` opened by `ownCheckpoints` bypass `readFence` completely so `pre-write`, `pin`, `PASSIVE`, and `restart-write` never block on or count themselves.
   - **Write transactions:** `countingConn.BeginTx` with `opts.ReadOnly == false` (`WriteTx`, `CheckMigration`) and raw `BEGIN IMMEDIATE` in `(*Store).migrate` (`countingConn.ExecContext` tracking `BEGIN` through `COMMIT`/`ROLLBACK`) set `c.inTx = true` without waiting on `readFence`, and `HoldsWrite()` (`tools/tillr/store/ioguard.go`) also bypasses fence waiting so a goroutine holding `WAL_WRITE_LOCK` can never wait behind `readFence`.
   - **Same-goroutine nested reads:** `readFence` tracks active lease count per `goroutineID()` (using the stack-header helper already in `writequeue.go` and `ioguard.go`). If a goroutine that already holds an active lease on `f` opens a second `OpenRead` and starts an inner `ReadTx` while `f` is closed, `f` admits the inner read immediately without waiting on `f.gate` (still counting it in `f.activeReads` until release) so the outer read can finish instead of waiting on itself.
   - **Unowned handle close (`(*Store).Close`):** `readFence` runs only on `CheckpointOwner` cycles (`CheckpointOwner.Checkpoint` and `CheckpointOwner.Close` via `o.ownerCycle()`), never on short-lived CLI `(*Store).Close()` (`log-checkpoint`).

### 3.2 Exact two-beat `CheckpointOwner` cycle with `writeQueue` tail catch-up

`CheckpointOwner`'s cycle (`o.ownerCycle()`) shares a single per-cycle fence wait budget `checkpointFenceBudget = 250 * time.Millisecond` across both beats:

1. **Cycle observation start:** `obs := f.beginCycle(now)` enables per-call-site capture of raw caller program counters (`runtime.Callers`, skipping `runtime`, `database/sql`, and `github.com/mschulkind-oss/tillr/store` frames) on every top-level `readFence` admission until `defer f.endCycle(obs)` runs. `defer f.reopen()` guarantees no exit path leaves `f` closed.
2. **Beat 1 (`pre-write` + `pin`):**
   - `f.holdAndDrain(ctx, &budget)` closes `f.gate` to new top-level reads from other goroutines and waits until `f.activeReads == 0`, `ctx.Done()`, or a local timer of `budget` fires (subtracting elapsed wait from `budget`).
   - If `budget` expires while `f.activeReads > 0`, `f` sets `c.FenceTimedOut = true`, snapshots currently held leases (`f.snapshotOpenReads(now)`), and calls `f.reopen()` **immediately** so waiting readers proceed at once.
   - While `f` remains closed (in the normal case where `f.activeReads` drained to `0`), the cycle commits `pre-write` (`mxFrame_1 > nBackfill`), opens `pin` on `o.keeper.db` (`aReadMark = mxFrame_1` on `WAL_READ_LOCK(1..4)`), records `writesBeforePassive := writeQueueFor(path).committedSeq()` (an `atomic.Uint64` incremented inside `writeTx` after `tx.Commit()`, outside `checkpointWork`), and calls `f.reopen()`.
3. **Unfenced bulk `PASSIVE`:**
   - `o.writer.checkpoint(ctx)` runs `PRAGMA wal_checkpoint(PASSIVE)` with `readFence` open and `writeQueue` free. Every pre-cycle reader drained in Beat 1 is gone, and every reader admitted after Beat 1's `f.reopen()` has `aReadMark >= mxFrame_1`, so `PASSIVE` copies all frames through `mxFrame_1` (and any newer frames up to `mxSafeFrame`) while readers and writers run concurrently.
   - Existing test seam `afterCheckpoint` runs immediately after bulk `PASSIVE`, unchanged.
4. **Beat 2 (`writeQueue` serialization + optional delta `PASSIVE` + `restart-write`):**
   - If `!c.Busy && !c.FenceTimedOut && budget > 0`, `f.holdAndDrain(ctx, &budget)` closes `f.gate` and waits up to the remaining `budget` for short reads opened during bulk `PASSIVE` to close. If `budget` expires, `c.FenceTimedOut = true` and `f.snapshotOpenReads(now)` are recorded and `f.reopen()` runs immediately.
   - The cycle enters `writeQueueFor(path).enter(ctx, o.writer.busyTimeout)` (`defer leaveQueue()`) so no new in-process `WriteTx` can commit between delta catch-up and `restart-write`. Because `writeQueue.enter` is re-entrant for the holding goroutine (`q.holder.Load() == me`), `restart-write` reuses the turn.
   - **Tail delta catch-up ([`D-SCR2`](#D-SCR2)):** if `!c.FenceTimedOut && !c.Busy && (!c.Complete() || writeQueueFor(path).committedSeq() != writesBeforePassive)`, an in-process write committed frames after `mxFrame_1` while bulk `PASSIVE` was flushing. Because Beat 1's `pin` holds `aReadMark = mxFrame_1`, which would clamp a second `PASSIVE` to `mxFrame_1`, the cycle rolls Beat 1's `pin` back, immediately opens a fresh `pin` on `o.keeper.db` at the current `mxFrame_2`, and runs a delta `o.writer.checkpoint(ctx)` to copy only `(c.CopiedFrames .. mxFrame_2]` (`c.LogFrames`, `c.CopiedFrames`, `c.Busy` updated from the delta run).
   - `restart-write` runs under `writeQueue`: `BEGIN IMMEDIATE` sees `nBackfill == mxFrame` and claims `WAL_READ_LOCK(0)` + `WAL_WRITE_LOCK`, `pin.Rollback()` frees the sole remaining `WAL_READ_LOCK(1..4)` slot, and `tx.Commit()` executes `_walRestartLog`, resetting the WAL to frame 1 with a new salt.
   - `leaveQueue()` and `f.reopen()` release writers and readers.

### 3.3 Ordering `checkpointIfDue` ahead of `recordProcessCPUIfDue` ([`D-SCR3`](#D-SCR3))

In `(*Loop).keepSessionAlive` ([`tools/tillr-drain/drain/pass_observation.go`](../../tools/tillr-drain/drain/pass_observation.go)), the heartbeat tick reorders the two one-minute tasks so `l.checkpointIfDue(ctx)` executes **before** `l.recordProcessCPUIfDue(ctx)`:

```go
err := l.beat(ctx, l.cfg.Now().Add(PassInterval))
if err == nil {
	err = l.checkpointIfDue(ctx)
	if err == nil {
		l.recordProcessCPUIfDue(ctx)
	}
}
```

At every 60-second boundary, the checkpoint restarts the WAL first; only afterwards does `drain.process-cpu` commit and publish `queue.changed`, keeping [`tools/tillr/store/notify.go`](../../tools/tillr/store/notify.go) unchanged so `tillr-watch` (`tools/tillr-watch/wake.go`) and `tillr-view` (`tools/tillr-view/servews.go`) continue waking on `drain.process-cpu` and `drain.checkpoint-not-restarted` events.

---

## 4. Why an Open In-Process Read Cannot Stall the Heartbeat (Criterion 6)

The mechanism guarantees `(*Loop).keepSessionAlive` liveness even if a defective caller holds an in-process `ReadTx` or `*sql.Rows` open forever:

1. **Independent `250 ms` per-cycle timer, never `ctx.Done()`.** `holdAndDrain` bounds its wait with a local `time.NewTimer(budget)` (`checkpointFenceBudget = 250 * time.Millisecond` total across Beat 1 and Beat 2 of one cycle), not with the loop's long-lived `ctx`. When an in-process read stays open past `250 ms` in Beat 1, the timer fires, `budget` becomes `0`, `c.FenceTimedOut = true` is recorded, and `f.reopen()` immediately unblocks all queued readers.
2. **Zero second timeout in Beat 2.** Once Beat 1 exhausts `budget` (`c.FenceTimedOut == true`), Beat 2 skips `holdAndDrain` completely (`0 ms` spent waiting in Beat 2). Bulk `PASSIVE` (`eMode = PASSIVE`, `xBusy = 0`) and `restart-write` (`_walLockExclusive` non-blocking try) never block on reader slots in SQLite.
3. **Arithmetic margin inside session leases.** On a checkpoint tick with a permanently stuck read, `Owner.Checkpoint` adds at most `250 ms` of fence wait — one quarter of `PassInterval` (`1 s`), `< 1%` of `CheckpointAllowance` (`30 s`, refreshed immediately ahead of `Owner.Checkpoint` in `checkpointIfDue`), and `< 0.2%` of `SessionGrace` (`180 s`). Subsequent missed-restart retries back off exponentially (`1 s, 2 s, 4 s, 8 s, 16 s, 32 s, 60 s` in `storelog.WALPromise`), so across an entire minute at steady retry cadence the fence is closed for `250 ms` out of `60,000 ms` (`0.4%`), and `l.beat` refreshes `drain_sessions.refreshed_at` on every `1 s` tick.
4. **No reader-writer or nested-read deadlock.** Writers (`WriteTx`, `migrate`, `refreshSession`), checkpoint-owner connections, and same-goroutine nested reads bypass `f.gate` ([§3.1](#31-process-wide-per-store-readfence-at-the-sqlite-connector-boundary)), so neither `l.beat` nor a reader holding `WAL_WRITE_LOCK` or an outer `ReadTx` can wait on itself.

---

## 5. What a Failed Cycle Records (`drain.checkpoint-not-restarted`, [`D-SCR4`](#D-SCR4))

When a cycle still returns `!c.Restarted && !c.WriterBusy && !c.PinBusy` — whether from a stuck in-process read exceeding `250 ms`, a sudden unthrottled read storm, or an external process holding `WAL_READ_LOCK(1..4)` — `f.endCycle(obs)` resolves raw program counters via `runtime.CallersFrames` (paying zero symbolication or allocation cost on the common `Restarted == true` path) and populates `store.Checkpointed` and `storelog.CheckpointNotRestartedFact` ([`tools/tillr-drain/internal/storelog/wal_keeper.go`](../../tools/tillr-drain/internal/storelog/wal_keeper.go)):

```go
type ReadStartSite struct {
	Site   string `json:"site"`   // first non-store frame: "<pkg>.<Func> (<file>:<line>)"
	Starts int    `json:"starts"` // top-level reads started at Site during this cycle
}

type OpenReadSite struct {
	Site     string `json:"site"`       // first non-store frame holding the open read
	Kind     string `json:"kind"`       // "read_tx" or "query"
	Count    int    `json:"count"`      // concurrent open handles at Site when sampled
	MaxAgeMs int64  `json:"max_age_ms"` // oldest open handle age at Site in milliseconds
}
```

`drain.checkpoint-not-restarted` carries these fields in `events.payload` alongside the existing frame counts:

| JSON key | Type | Meaning |
|---|---|---|
| `log_frames` | `int` | `mxFrame` observed by the final `PASSIVE` pass of the missed cycle |
| `copied_frames` | `int` | `nBackfill` reached by `PASSIVE` (`copied_frames < log_frames` isolates Condition A; `copied_frames == log_frames` isolates Condition B) |
| `busy` | `bool` | Whether `PASSIVE` saw a writer holding `WAL_WRITE_LOCK` |
| `restarted` | `bool` | Always `false` on `drain.checkpoint-not-restarted` |
| `fence_timed_out` | `bool` | `true` when `holdAndDrain` hit the `250 ms` cycle budget with `activeReads > 0` |
| `read_starts_total` | `int` | Total top-level in-process reads started across the missed cycle (`beginCycle` to `endCycle`), before top-site truncation |
| `read_starts` | `[]ReadStartSite` | Per-call-site top-level read starts for the missed cycle, sorted by descending `starts` then ascending `site`, capped at the top `16` call sites |
| `open_reads` | `[]OpenReadSite` | In-process reads still open when a fence timed out (or at `restart-write` if none timed out), sorted by descending `max_age_ms` then `site`, capped at `16` call sites |

How operators and watch rules read a missed cycle from one event payload:

- `fence_timed_out == true` with non-empty `open_reads`: names the exact in-process Go call site and age (`max_age_ms >= 250`) of the slow or leaked read that outlived the fence.
- `fence_timed_out == false` with `read_starts_total == 0` and empty `open_reads`: proves zero in-process read activity interfered, isolating an **external reader process** (`tillr-view`, `tillr-watch`, or an ad-hoc `tillr` CLI handle).

---

## 6. Loop-Level Test Specification ([`D-SCR5`](#D-SCR5))

### 6.1 Primary loop-level test that fails on the current trunk (`TestDenseInProcessLoopReadsDoNotPreventCheckpointRestart`)

Location: [`tools/tillr-drain/drain/checkpoint_retry_test.go`](../../tools/tillr-drain/drain/checkpoint_retry_test.go).

Setup and execution (**zero external store handles** — every connection belongs to the `*Loop` under test):

1. Construct a `*Loop` via `NewLoop` with `keepWALWhileRunning()` (`storelog.OpenWALPromise`) on a migrated store in `t.TempDir()`, and grow the WAL past `store.CheckpointFrames` (`1,000` frames) through loop/store writes so a checkpoint cycle is due.
2. Spawn `8` in-process worker goroutines calling real `*Loop` read entrypoints (`l.stopRequested(ctx)` and `l.restarterPendingWith(ctx, nil)`, alongside `store.OpenRead(ctx, l.cfg.Store)` + `ReadTx` holding a `SELECT count(*) FROM meta` snapshot for `4 ms` via `time.Sleep(4 * time.Millisecond)` before rolling back and closing, then looping immediately until `stopReads` closes). Wait on a `ready` barrier until all `8` goroutines have completed at least one read and re-entered active rotation, saturating SQLite's four shared WAL reader slots (`WAL_READ_LOCK(1..4)`) with short self-completing in-process reads and zero channel waits on the checkpoint path.
3. Concurrently run one background in-process loop writer goroutine committing periodic small `meta` writes every `5 ms` (exercising Condition A's post-`PASSIVE` write race as well as reader density).
4. Drive `3` consecutive due `l.checkpointIfDue(ctx)` cycles by advancing the loop's fake clock (`now`) past `CheckpointInterval` (`time.Minute`) and re-growing the WAL past `store.CheckpointFrames` before each cycle.

Expected outcome:

- **On the current trunk (`ed16ce2ca62b`):** **fails** — with `8` concurrent goroutines each holding a `4 ms` read snapshot at `> 95%` per-goroutine duty cycle (`(1 - 0.95)^8 < 10^-10` chance all `8` are simultaneously between transactions), `WAL_READ_LOCK(1..4)` never clears on its own; `CheckpointOwner.Checkpoint` returns `Restarted == false` and appends `drain.checkpoint-not-restarted` events.
- **With [§3](#3-chosen-mechanism-two-beat-bounded-readfence-tail-catch-up-and-heartbeat-ordering-d-scr1-d-scr2-d-scr3) implemented:** **passes** — every one of the `3` cycles drains active `4 ms` reads in Beat 1 and Beat 2 well inside `250 ms`, catches up any concurrent write under `writeQueue`, records `Restarted == true` and `FenceTimedOut == false` on all `3` cycles, advances the WAL header salt `3` times, and appends `0` `drain.checkpoint-not-restarted` events.

### 6.2 Paired non-stall and attribution loop-level test (`TestOpenInProcessReadDoesNotStallHeartbeatOrCheckpoint`)

In the same file ([`tools/tillr-drain/drain/checkpoint_retry_test.go`](../../tools/tillr-drain/drain/checkpoint_retry_test.go)), verify Criterion 6 and Criterion 7 without elapsed-time assertions (obeying [`docs/reference/gate-check-rules.md`](../reference/gate-check-rules.md#a-check-asserts-what-was-recorded-never-elapsed-time), using only `hangGuard = 5 * time.Minute` for liveness guards):

1. Open an in-process `ReadTx` on `l.cfg.Store` from a named helper function `holdStuckReadForTest`, execute `SELECT count(*) FROM meta` so it holds `WAL_READ_LOCK(1..4)`, signal `started`, and block inside the callback on `<-releaseStuckRead` (kept **unclosed** throughout the checkpoint and subsequent heartbeats). Also issue `5` short `l.stopRequested(ctx)` reads during the cycle via `afterCheckpoint` so `read_starts` has a deterministic non-zero call-site count.
2. Trigger a due `l.checkpointIfDue(ctx)` and two subsequent `l.beat` ticks on `keepSessionAlive` **before** closing `releaseStuckRead`.
3. Assert recorded state (never `time.Since`):
   - `l.checkpointIfDue(ctx)` returned `nil` while `releaseStuckRead` was still unclosed;
   - `drain_sessions.refreshed_at` in SQLite advanced to the timestamp of the subsequent `l.beat` ticks while `holdStuckReadForTest` remained open;
   - exactly one `drain.checkpoint-not-restarted` event was recorded with `fence_timed_out == true`, `open_reads` containing `holdStuckReadForTest` (`kind == "read_tx"`, `count == 1`), and `read_starts` containing `(*Loop).stopRequestedWith` (`starts >= 5`, `read_starts_total >= 5`); and
   - after closing `releaseStuckRead` and advancing past the `1 s` retry backoff, the next `l.checkpointIfDue(ctx)` restarts the WAL (`Restarted == true`).

---

## 7. Build Slices (Criterion 8)

Each slice is independently buildable, green on targeted tests, and leaves no half-wired state. Any change under `tools/tillr*` runs `just format` and `just pack-sync` in the same commit.

### 7.1 Slice 1: Bounded two-beat `readFence` and `writeQueue` tail catch-up in `tools/tillr/store`

- **Files:** `tools/tillr/store/read_fence.go` (new), `tools/tillr/store/read_counters.go`, `tools/tillr/store/open.go`, `tools/tillr/store/read.go`, `tools/tillr/store/write.go`, `tools/tillr/store/writequeue.go`, `tools/tillr/store/checkpoint.go`, `tools/tillr/store/checkpoint_cycle_test.go`, plus `just pack-sync` mirror under `yolo-packs/tillr/`.
- **Change:**
  - Add per-canonical-path `readFence` wired into `openSQLite` / `countingConn` (`ReadOnly` `BeginTx` and top-level `!inTx` `QueryContext`/`Query`), with exemptions for `checkpointInternalConn` (`o.writer`, `o.keeper`), write transactions (`!opts.ReadOnly`, raw `BEGIN` in `migrate`, `HoldsWrite()`), same-goroutine nested reads, and unowned `(*Store).Close`.
  - Add `committedSeq` (`atomic.Uint64`) on `writeQueue` incremented on write commit without touching `checkpointWork` (`TestAWriteTransactionDoesNoCheckpointWork` stays green).
  - Implement Beat 1 (`holdAndDrain` -> `pre-write` -> `pin` -> `reopen`), unfenced bulk `PASSIVE`, and Beat 2 (`holdAndDrain` -> `writeQueue.enter` -> re-pin + delta `PASSIVE` when `!c.Complete() || committedSeq != writesBeforePassive` -> `restart-write` -> `leaveQueue` + `reopen`) in `CheckpointOwner.ownerCycle()`, sharing `checkpointFenceBudget = 250 * time.Millisecond` (`FenceTimedOut bool` on `Checkpointed`).
- **Verification:** targeted `go test ./store -run 'Test.*Checkpoint|TestAWriteTransactionDoesNoCheckpointWork'` in `tools/tillr`, including new unit tests for dense short reads + concurrent writers restarting every cycle, a stuck `ReadTx` returning `FenceTimedOut == true` while queued readers proceed once the `250 ms` timer fires, and nested same-goroutine `ReadTx` completing without fence timeout.

### 7.2 Slice 2: Per-call-site `read_starts` and `open_reads` attribution on `drain.checkpoint-not-restarted`

- **Files:** `tools/tillr/store/read_fence.go`, `tools/tillr/store/checkpoint.go`, `tools/tillr/store/checkpoint_cycle_test.go`, `tools/tillr-drain/internal/storelog/wal_keeper.go`, `tools/tillr-drain/drain/storelog_bridge.go`, `tools/tillr-drain/drain/checkpoint_retry_test.go`, plus `just pack-sync` mirror.
- **Change:**
  - Capture raw caller PCs (`runtime.Callers`) on top-level `readFence` admissions during an active owner cycle and on open leases, resolving frames via `runtime.CallersFrames` only when `!c.Restarted && !c.WriterBusy && !c.PinBusy`.
  - Add `ReadStartSite`, `OpenReadSite`, `FenceTimedOut`, `ReadStartsTotal`, `ReadStarts`, and `OpenReads` to `store.Checkpointed` and forward them as `fence_timed_out`, `read_starts_total`, `read_starts`, and `open_reads` in `storelog.CheckpointNotRestartedFact` / `RecordCheckpointNotRestarted`.
- **Verification:** targeted tests in `tools/tillr/store` and `tools/tillr-drain/drain` asserting exact JSON payload keys, call-site names, zero symbolication on restarted cycles, and empty `read_starts`/`open_reads` when an external process holds a reader slot.

### 7.3 Slice 3: Heartbeat checkpoint-before-CPU ordering, loop-level dense-read and non-stall tests, and reference graduation

- **Files:** `tools/tillr-drain/drain/pass_observation.go`, `tools/tillr-drain/drain/checkpoint_retry_test.go`, `tools/tillr-store/README.md`, `docs/reference/tillr-store-contention.md`, `docs/design/store-checkpoint-restart.md`, plus `just pack-sync` mirror.
- **Change:**
  - Reorder `(*Loop).keepSessionAlive` so `l.checkpointIfDue(ctx)` runs before `l.recordProcessCPUIfDue(ctx)` on shared one-minute ticks.
  - Add `TestDenseInProcessLoopReadsDoNotPreventCheckpointRestart` ([§6.1](#61-primary-loop-level-test-that-fails-on-the-current-trunk-testdenseinprocessloopreadsdonotpreventcheckpointrestart)) and `TestOpenInProcessReadDoesNotStallHeartbeatOrCheckpoint` ([§6.2](#62-paired-non-stall-and-attribution-loop-level-test-testopeninprocessreaddoesnotstallheartbeatorcheckpoint)), plus a `keepSessionAlive` ordering test verifying `CheckpointOwner.Checkpoint` runs before `drain.process-cpu` is appended on a due tick.
  - Update [`tools/tillr-store/README.md`](../../tools/tillr-store/README.md#durability-and-checkpoints) and [`docs/reference/tillr-store-contention.md`](../reference/tillr-store-contention.md#observing-remaining-contention) to document the shipped two-beat fence and `drain.checkpoint-not-restarted` attribution fields.
- **Verification:** `cd tools/tillr-drain && go test ./drain -run 'Test.*Checkpoint|TestDenseInProcessLoopReads|TestOpenInProcessRead'`, `uvx vantage-check` on changed Markdown files, and `just pack-sync`.

---

## 8. Decision Ledger

Every entry below is settled by the **design seat** (`design`, not an owner ruling) in response to an operator-filed design request; the owner may overturn any entry.

| ID | Settled | Provenance | Decision |
|---|---|---|---|
| <a id="D-SCR1"></a>`D-SCR1` | 2026-11-28 | Design seat (`design`, operator-filed request; owner may overturn) | `CheckpointOwner` restarts the SQLite WAL under dense in-process reads using a per-canonical-store-path two-beat `readFence` at `tools/tillr/store`'s `countingConn` boundary (Beat 1 around `pre-write` + `pin`, Beat 2 around `restart-write`, leaving bulk `PRAGMA wal_checkpoint(PASSIVE)` unfenced), bounded by a shared `250 ms` per-cycle local timer (`checkpointFenceBudget`) that immediately re-opens the fence on expiry so an open read never stalls `keepSessionAlive` or `beat`. `PRAGMA wal_checkpoint(RESTART)` with `busy_timeout`, a continuous whole-cycle read fence, and snapshot-sharing alone are rejected ([§2](#2-weighed-candidates-d-scr1-d-scr2), [§3](#3-chosen-mechanism-two-beat-bounded-readfence-tail-catch-up-and-heartbeat-ordering-d-scr1-d-scr2-d-scr3), [§4](#4-why-an-open-in-process-read-cannot-stall-the-heartbeat-criterion-6)). |
| <a id="D-SCR2"></a>`D-SCR2` | 2026-11-28 | Design seat (`design`, operator-filed request; owner may overturn) | Beat 2 enters the store's in-process `writeQueue` before `restart-write` and, whenever `!c.Complete()` or `writeQueue.committedSeq()` advanced during bulk `PASSIVE` (without `FenceTimedOut` or `Busy`), rolls Beat 1's `pin` back, re-pins `o.keeper.db` at the new `mxFrame`, and runs a delta `PASSIVE` checkpoint so in-process writes during bulk `PASSIVE` cannot defeat `_walRestartLog` ([§3.2](#32-exact-two-beat-checkpointowner-cycle-with-writequeue-tail-catch-up)). |
| <a id="D-SCR3"></a>`D-SCR3` | 2026-11-28 | Design seat (`design`, operator-filed request; owner may overturn) | `(*Loop).keepSessionAlive` runs `l.checkpointIfDue(ctx)` ahead of `l.recordProcessCPUIfDue(ctx)` on their shared 60-second cadence so `drain.process-cpu`'s `queue.changed` notification does not wake a pass read into the start of the checkpoint cycle, while `tools/tillr/store/notify.go` keeps publishing `drain.process-cpu` and `drain.checkpoint-not-restarted` for `tillr-watch` and `tillr-view` ([§3.3](#33-ordering-checkpointifdue-ahead-of-recordprocesscpuifdue-d-scr3)). |
| <a id="D-SCR4"></a>`D-SCR4` | 2026-11-28 | Design seat (`design`, operator-filed request; owner may overturn) | When a checkpoint cycle misses a restart (`!Restarted && !WriterBusy && !PinBusy`), `store.Checkpointed` and `drain.checkpoint-not-restarted` symbolize raw `runtime.Callers` PCs captured during the cycle and record `fence_timed_out`, `read_starts_total`, `read_starts` (top `16` per-call-site read start counts), and `open_reads` (top `16` open in-process read sites with `kind`, `count`, and `max_age_ms`), with zero symbolication cost on restarted cycles ([§5](#5-what-a-failed-cycle-records-draincheckpoint-not-restarted-d-scr4)). |
| <a id="D-SCR5"></a>`D-SCR5` | 2026-11-28 | Design seat (`design`, operator-filed request; owner may overturn) | Verification requires `TestDenseInProcessLoopReadsDoNotPreventCheckpointRestart` (a loop-level test in `tools/tillr-drain/drain/checkpoint_retry_test.go` with zero external store handles and `8` concurrent short-read loop goroutines plus concurrent loop writes, which fails on `ed16ce2ca62b` and restarts every cycle with `D-SCR1`–`D-SCR3`) paired with `TestOpenInProcessReadDoesNotStallHeartbeatOrCheckpoint` asserting recorded session refresh advancement and `drain.checkpoint-not-restarted` call-site attribution without elapsed-time assertions ([§6](#6-loop-level-test-specification-d-scr5)). |
