---
title: SQLite contention QA
status: fixture verification, independent review pending
---

# SQLite contention QA

Measured 2026-11-16 against the recovery implementation. Commands run in the named
module directories with bounded fixture scratch and TMPDIR; no live database access.
Standard shared Go caches were preserved: GOCACHE `/home/agent/.cache/go-build`,
GOMODCACHE `/home/agent/go/pkg/mod`, GOENV `/home/agent/.config/go/env`.

## Regressions

`go test . -run 'Test(GitPreparationAllowsHeartbeatAndClose|FinalMoveExcludesCloseUntilReceipt)$' -count=1 -v`
in `tools/tillr-land` before production edits:

- `TestGitPreparationAllowsHeartbeatAndClose`: **RED**, 5.43s, independent heartbeat
  failed with `database is locked (5) (SQLITE_BUSY)` while merge-tree was stalled.
- `TestFinalMoveExcludesCloseUntilReceipt`: **GREEN**, 0.75s; Close waited for final Move
  and refused `already-landed` after receipt commit.

After repair, `go test . -count=1 -run 'Test(CanceledGit|FinalMoveFailure|ReceiptFailure|GitPreparation|FinalMoveExcludes|PreparationRevalidates)' -v`:
**GREEN**, all six tests, including the three stale-input subcases; 9.285s total.
Preparation stays stalled beyond five seconds while heartbeat and actual item Close finish;
release records `item-not-landable` without moving trunk. Cancellation writes no receipt;
changed trunk/history/table inputs cause explicit no-Move errors; a late worktree clash
rolls back the receipt; a receipt error after Move is reported without replay.

`go test ./drain -run TestInitializedSessionHeartbeatDoesNotRemigrate -count=1 -v`
in `tools/tillr-drain`: **RED** before optimization (two repeated migration attempts),
**GREEN** afterward, including persisted session end. Full drain tests retain startup
heartbeat, owner/column migration, legacy pass-copy idempotence and history lifecycle cases.

`TestWriterTraceCorrelatesMigrationAndRollbackWithoutChangingOutcome` checks opt-in
migration and writer phases, rollback/error preservation and disabled-by-default logging.

## Full validation

For each of the nine Go modules (`tillr`, `tillr-cli`, `tillr-drain`, `tillr-gate`,
`tillr-item`, `tillr-land`, `tillr-run`, `tillr-store`, `tillr-view`) under `tools/`:
`go test ./... -count=1` and `go vet ./...`: **PASS**. No failing baseline is waived.

`go test -race ./store -run TestWriterTraceCorrelatesMigrationAndRollbackWithoutChangingOutcome -count=1 -v`
in `tools/tillr`: **PASS**. Running the primary preparation/Close, final Move/Close and
cancellation regressions with `TILLR_SQLITE_TRACE=1`: **PASS**. The trace fixture measured
merge-tree stalled for 5.149s while the heartbeat acquired its independent writer in
188.988 microseconds; Land acquired its final writer only after preparation ended.

Pack delivery checks: `just pack-sync`, `python3 tests/pack-sync.test.py`, and
`yolo pack lint yolo-packs/tillr`: **PASS**. Lint checks the installed binary's pack
acceptance only; no host render, deployment or restart was verified.

Document render check: `uvx vantage-check docs/recovery/sqlite-contention/README.md docs/recovery/sqlite-contention/research.md docs/recovery/sqlite-contention/plan.md docs/recovery/sqlite-contention/tasks.md docs/recovery/sqlite-contention/qa.md tools/tillr-land/README.md`: **PASS**.

## Not claimed

The [remaining final writer span](README.md#exact-residual-limitation) can still block
heartbeats beyond their unchanged timeout. The overnight holder remains unidentified.
No live recovery actions, policy changes, ref moves, deployment, staging or commits were
performed by this writer. Independent review is still required.
