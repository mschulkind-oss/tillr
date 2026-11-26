---
title: "Cancelling the tip-watcher during an automatic revert never orphans packed-refs.lock"
author: "Agent"
date: 2026-11-26
status: in-review
stage: DECIDED
stage-since: 2026-11-26
tags: [tillr, drain, gate, revert, git, design]
summary: "Establishes why TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed failed on 2026-11-26 with Unable to create '<fixture>/.git/packed-refs.lock': File exists during revertLanding: cancelling watchCtx when Steps.Gate returned sent SIGKILL via exec.CommandContext in heavy.GitOutput to watchWG's git checkout --force --detach while it held packed-refs.lock, leaving a stale lockfile that later failed land.Land's git update-ref -d refs/tillr/trunk-history/lost after watchWG.Wait() had already finished. Specifies process-group SIGTERM + 5s WaitDelay cancellation in heavy.GitOutput (and detachedWorktree) so Git's tempfile handler unlinks .lock files before watchWG.Wait() returns, answering the definition conflict on Criteria 5 and 6 with zero retries."
next: "Land the single-commit fix and deterministic regression test in tools/tillr-drain (plus just pack-sync), then fold the verified behavior into docs/reference/gate.md under R-NOQ1; the owner may review D-HRPL1."
depends-on:
  - ../reference/gate.md
  - ../reference/gate.md#a-red-proof
  - ../reference/gate.md#D-GR6
  - ../reference/gate.md#D-GR47
  - ./no-open-questions-in-references.md#R-NOQ1
  - ./tillr-graph-is-the-path.md#decision-ledger
  - ./landing-principles.md#15-land-the-smallest-functional-change
vantage:
  status-chip: true
---

# Cancelling the tip-watcher during an automatic revert never orphans `packed-refs.lock`

**Status:** 2026-11-26, verified against `3c992995794d`. Not built: `rg -n 'cmd\.Cancel|WaitDelay' tools/tillr-drain/heavy/status.go` returns nothing, and `detachedWorktree` in [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go) runs `exec.CommandContext(ctx, "git", "-C", s.Root, "worktree", "add", "--detach", "--force", dir, rev)` with Go's default `SIGKILL` cancellation. Following owner ruling [`R-NOQ1`](./no-open-questions-in-references.md#R-NOQ1), this unbuilt specification lives in a standalone design document under `docs/design/` linking [`docs/reference/gate.md`](../reference/gate.md) rather than adding an unbuilt decision to [`docs/reference/gate.md`](../reference/gate.md). Decision [`D-HRPL1`](#D-HRPL1) in the [Decision Ledger](#4-decision-ledger) is **the designer's decision answering a 2026-11-26 filed operator request — not an owner ruling — and the owner may overturn any part of it**.

> **In short.** `TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed` failed on 2026-11-26 not because two Git processes ran at the same moment during `land.Land`, but because `cancelWatch()` in `revertLanding` cancelled `watchCtx` while the `watchWG` tip-watcher goroutine was inside `heavy.GitOutput` running `git checkout --force --detach`, sending `SIGKILL` to `git` while it held `<root>/.git/packed-refs.lock`. Replacing `exec.CommandContext`'s immediate `SIGKILL` with process-group `SIGTERM` and a `5s` `WaitDelay` in `heavy.GitOutput` (and `detachedWorktree`) lets Git's `tempfile.c` signal handler unlink `<root>/.git/packed-refs.lock` before `watchWG.Wait()` returns, ordering lock release strictly before post-gate `replay` and `land.Land` with zero retries.

**Why it matters.** Under heavy machine load, `SIGKILL`ing `watchWG`'s proof-worktree Git command mid-lock leaves `<root>/.git/packed-refs.lock` orphaned on disk, turning a valid automatic revert ([`D-GR6`](../reference/gate.md#D-GR6)) into `result: "not-possible"` (`the lander could not land the revert`) and filing a trunk fixer instead of reverting the culprit landing ([§1](#1-forensics-of-the-2026-11-26-failure-and-the-conflict-on-criteria-5-and-6)).

**The shape.** Configure process-group `SIGTERM` cancellation (`Setpgid: true`, `syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)`, `WaitDelay = 5 * time.Second`) on `git` subprocesses in `heavy.GitOutput` / `heavy.Green` ([`tools/tillr-drain/heavy/status.go`](../../tools/tillr-drain/heavy/status.go)) and `detachedWorktree` ([`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go)), mirror via `just pack-sync`, and prove it with a deterministic regression test in [`tools/tillr-drain/drain/heavy_revert_tip_passes_test.go`](../../tools/tillr-drain/drain/heavy_revert_tip_passes_test.go) ([§2](#2-graceful-git-cancellation-and-conflict-resolution-d-hrpl1)).

**Cost.** One small helper in `tools/tillr-drain/heavy/status.go`, the matching cancellation setup in `detachedWorktree` (`tools/tillr-drain/drain/steps.go`), `just pack-sync`, and one deterministic FIFO-synchronized regression test (`~2 ms` cancellation teardown, zero retries, zero sleeps on the assertion path).

**Start at [§1](#1-forensics-of-the-2026-11-26-failure-and-the-conflict-on-criteria-5-and-6)** for the root cause and why Criteria 5 and 6 conflicted with the tree, and [§2](#2-graceful-git-cancellation-and-conflict-resolution-d-hrpl1) for the exact fix and test contract.

**Needs your ruling:** None. Owner rulings [`R-NOQ1`](./no-open-questions-in-references.md#R-NOQ1) and [`OQ-GP1`](./tillr-graph-is-the-path.md#decision-ledger) ([`D-GR6`](../reference/gate.md#D-GR6)) are already settled; [`D-HRPL1`](#D-HRPL1) is the designer's decision from an operator request and may be overturned by the owner.

**Reads with:** [`docs/reference/gate.md`](../reference/gate.md) ([A red proof](../reference/gate.md#a-red-proof), [`D-GR6`](../reference/gate.md#D-GR6), [`D-GR47`](../reference/gate.md#D-GR47)), [`docs/design/no-open-questions-in-references.md`](./no-open-questions-in-references.md) ([`R-NOQ1`](./no-open-questions-in-references.md#R-NOQ1)), [`docs/design/tillr-graph-is-the-path.md`](./tillr-graph-is-the-path.md) ([`OQ-GP1`](./tillr-graph-is-the-path.md#decision-ledger)), and [`docs/design/landing-principles.md`](./landing-principles.md) ([§15](./landing-principles.md#15-land-the-smallest-functional-change)).

---

## 1. Forensics of the 2026-11-26 Failure and the Conflict on Criteria 5 and 6

### 1.1 What failed on 2026-11-26 and why the request inferred two concurrent commands

On 2026-11-26 at ~03:15 ET (load average near `110`), the after-merge trunk proof's `go-test-tillr-drain` check failed once in `TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed` ([`tools/tillr-drain/drain/heavy_revert_tip_passes_test.go`](../../tools/tillr-drain/drain/heavy_revert_tip_passes_test.go), added in `b1809482`). Instead of recording one `drain.revert` event with `result: "reverted"`, `revertLanding` recorded `result: "not-possible"` with:

```text
the lander could not land the revert: error: Unable to create '<fixture>/.git/packed-refs.lock': File exists.
Another git process seems to be running in this repository, or the lock file may be stale
```

Git's `unable_to_lock_message()` prints that identical two-part message whenever `open("<path>.lock", O_RDWR|O_CREAT|O_EXCL)` fails with `EEXIST` — both when another live Git process holds the lock and when an earlier killed Git process left a stale `.lock` file behind. Reading the first half of Git's message, the filed operator request inferred that a second Git command of the loop was writing refs in the fixture repository *at the same moment* the lander was landing the automatic revert, and worded custom Criteria 5 and 6 around two Git commands running at once during `land.Land`.

### 1.2 Established root cause from the code and reproduction

Tracing [`revertLanding`](../../tools/tillr-drain/drain/heavy_revert.go) and reproducing `TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed` under `GIT_TRACE2_EVENT` (and verifying Git 2.56.0 lockfile syscalls with `strace -f -e trace=openat,unlink,unlinkat` on 2026-11-26) establishes the exact two Git operations of the loop that collided over `<root>/.git/packed-refs.lock`:

1. **`watchWG` exits before post-gate `replay` and `land.Land` start.** In `revertLanding` ([`tools/tillr-drain/drain/heavy_revert.go`](../../tools/tillr-drain/drain/heavy_revert.go)), `cancelWatch()` and `watchWG.Wait()` run immediately after `l.cfg.Steps.Gate(gateCtx, candidate)` returns, strictly before post-gate `replay(ctx, revert, id)` and `l.landWithActions(ctx, candidate)` (`land.Land`) execute. Because `cmd.CombinedOutput()` waits for its child `git` process to exit before `watchWG.Done()` runs, **no second Git process is running concurrently when `land.Land` executes**.
2. **First Git command (killed with `SIGKILL` while holding `<root>/.git/packed-refs.lock`).** While `Steps.Gate` runs on `.revert/worktree`, `revertLanding`'s `watchWG` goroutine polls `l.trunkRef()` under `watchCtx`. When the trunk tip moves (`h.landRemoval("item-partial", "a")` in `TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed`, or a concurrent landing on `main` in the live loop), `watchWG` calls `tipChecksPass(watchCtx, cur, ids, replay)` -> `HeavyRunner.one` -> `HeavyRunner.OneLogged` -> `HeavyRunner.inTree` -> `tryReuseProofWorktree(watchCtx, r.Work, tip)` in [`tools/tillr-drain/heavyrun/heavy.go`](../../tools/tillr-drain/heavyrun/heavy.go) (or the fallback `worktree add --detach` in `inTree`; and on the `revertFromGateReplay` path, `ProcessSteps.Replay` -> `replayWorktree` -> `detachedWorktree` in [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go)).
   - In Git 2.56.0, `git checkout --force --detach <wantTip>`, `git reset --hard <wantTip>`, and `git worktree add --detach`'s child `git` each delete the worktree's `AUTO_MERGE` pseudoref through Git's ref-transaction layer, which acquires `<common-git-dir>/packed-refs.lock` in the shared repository (alongside worktree `.lock` files such as `index.lock`, `HEAD.lock`, `ORIG_HEAD.lock`, and `AUTO_MERGE.lock`).
   - In `TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed`, `"unit-a"` passes on `partial`, so `tipChecksPass` advances to `"unit-b"` and enters `tryReuseProofWorktree` a second time. Under load average ~`110`, `Steps.Gate` finishes its `80ms` wait while `watchWG` is inside `heavy.GitOutput(watchCtx, work, "checkout", "--force", "--detach", wantTip)` (or `reset --hard` / `worktree add --detach`).
   - `revertLanding` calls `cancelWatch()`. Because `heavy.GitOutput` ([`tools/tillr-drain/heavy/status.go`](../../tools/tillr-drain/heavy/status.go)) calls `exec.CommandContext(ctx, "git", ...)` with `cmd.Cancel == nil`, Go's `os/exec` sends **`SIGKILL`** directly to `git` while it holds `<root>/.git/packed-refs.lock`. Unlike `SIGTERM` — which Git catches via `tempfile.c`'s `remove_tempfiles_on_signal` to unlink every active `.lock` file before exiting — `SIGKILL` terminates `git` immediately and leaves `<root>/.git/packed-refs.lock` orphaned on disk. (Commit `03abd315` fixed the sibling `exec.CommandContext` `SIGKILL` bug on `git update-ref` in `(*Loop).git`, which left `observed.lock` stale across loop stops.)
3. **Second Git command (refused by the orphaned `<root>/.git/packed-refs.lock`).** After `watchWG.Wait()` returns, intermediate `checkout --force --detach`, `reset --hard`, and `worktree add --detach` calls in post-gate `replay` and pre-land `tipChecksPass` hit the stale `<root>/.git/packed-refs.lock` on their non-fatal `AUTO_MERGE` deletes (waiting `core.packedRefsTimeout` = `1,000 ms` per lock attempt and still exiting `0`). Then `l.landWithActions(ctx, candidate)` calls `land.Land` -> `pinHistory` ([`tools/tillr-land/land/trunk_history.go`](../../tools/tillr-land/land/trunk_history.go)), which runs `git -C <root> update-ref -d refs/tillr/trunk-history/lost`. Deleting a ref requires `<root>/.git/packed-refs.lock`, waits `1,000 ms`, and exits `1` with `Unable to create '<fixture>/.git/packed-refs.lock': File exists`, causing `revertLanding` to record `result: "not-possible"` instead of `"reverted"`.

---

## 2. Graceful Git Cancellation and Conflict Resolution ([`D-HRPL1`](#D-HRPL1))

*Designer's decision answering the 2026-11-26 filed operator request — not an owner ruling — which the owner may overturn.*

### 2.1 Process-group `SIGTERM` + `WaitDelay` before `watchWG.Wait()` returns

1. **`heavy.GitOutput` and `heavy.Green` in [`tools/tillr-drain/heavy/status.go`](../../tools/tillr-drain/heavy/status.go).** Route context-bound `git` invocations in `tools/tillr-drain/heavy/status.go` through a package-private helper (for example `gitCommandContext(ctx, root, args...)`) that configures:
   - `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` so `git` and any child `git` process it spawns (such as `git worktree add --detach`'s child `git`) share one dedicated process group;
   - `cmd.Cancel` sending `syscall.SIGTERM` to `-cmd.Process.Pid` (`syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)`, returning `os.ErrProcessDone` when `errors.Is(err, syscall.ESRCH)` and falling back to `cmd.Process.Signal(syscall.SIGTERM)` on any other error);
   - `cmd.WaitDelay = 5 * time.Second` (matching the `5s` `CheckGroupGrace` value in [`tools/tillr-drain/heavyrun/errors.go`](../../tools/tillr-drain/heavyrun/errors.go) as a local constant in `heavy`, never importing `heavyrun` into `heavy` per [`D-GR47`](../reference/gate.md#D-GR47)).

   In Git's `tempfile.c`, `prepare_tempfile_object` registers `remove_tempfiles_on_signal` before `open(..., O_RDWR|O_CREAT|O_EXCL)` creates any `.lock` file. Delivering `SIGTERM` to `-cmd.Process.Pid` therefore unlinks `<common-git-dir>/packed-refs.lock` and every active worktree `.lock` file (`index.lock`, `HEAD.lock`, `ORIG_HEAD.lock`, `AUTO_MERGE.lock`) before `git` exits (`~2 ms` measured on 2026-11-26 with `git 2.56.0` holding `packed-refs.lock` in `git update-ref --stdin` at `prepare: ok`), while `WaitDelay` still bounds a wedged subprocess. Because `cmd.CombinedOutput()` waits for `git` to exit after handling `SIGTERM`, `watchWG.Wait()` in `revertLanding` strictly orders lock release before post-gate `replay` and `land.Land` begin.
2. **`detachedWorktree` in [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go).** Configure the same process-group `SIGTERM` + `5 * time.Second` `WaitDelay` cancellation on `exec.CommandContext(ctx, "git", "-C", s.Root, "worktree", "add", "--detach", "--force", dir, rev)` inside `detachedWorktree` so cancelling `watchCtx` on the `revertFromGateReplay` path (`ProcessSteps.Replay` -> `replayWorktree` -> `detachedWorktree`) likewise never `SIGKILL`s `git worktree add`'s child `git` while it holds `<root>/.git/packed-refs.lock`.
3. **Pack mirror.** Run `just pack-sync` and commit the synced mirror under `yolo-packs/tillr/files/local/share/tillr/tillr-drain/` in the same commit.

### 2.2 Reading of the filed request and Criteria 5 and 6

Because the filed request's saved words stay byte-for-byte unchanged in the store, both the builder and the `verify` evaluator read the request's words and Criteria 5 and 6 together with [`D-HRPL1`](#D-HRPL1) as follows:

- **Criterion 5 (naming the two colliding Git commands from the code and reproduction, not guessed):** Satisfied when the commit body and regression test comment name the two Git commands of the loop established in [§1.2](#12-established-root-cause-from-the-code-and-reproduction):
  1. `watchWG`'s `tipChecksPass` -> `tryReuseProofWorktree` `git checkout --force --detach` / `git reset --hard` (and fallback / replay `git worktree add --detach`) invoked through `heavy.GitOutput` / `detachedWorktree`, which `cancelWatch()` killed with `SIGKILL` while holding `<root>/.git/packed-refs.lock`; and
  2. `land.Land` (`pinHistory`)'s `git update-ref -d refs/tillr/trunk-history/lost` in the same repository, which failed with `Unable to create '<fixture>/.git/packed-refs.lock': File exists` on that orphaned `packed-refs.lock` after `watchWG.Wait()` returned.
- **Criterion 6 (ordered at the cause so neither holds or orphans `packed-refs.lock` across `watchWG.Wait()`, with no retry and no wait-and-try-again, proved by a test that fails before the change):** Satisfied by the process-group `SIGTERM` + `WaitDelay` cancellation in `heavy.GitOutput` and `detachedWorktree` (which ensures `watchWG`'s `git` releases `<root>/.git/packed-refs.lock` before `watchWG.Wait()` returns and `land.Land` starts, with zero retries, zero backoff loops, and zero lockfile unlinking by Tillr) together with a deterministic regression test in [`tools/tillr-drain/drain/heavy_revert_tip_passes_test.go`](../../tools/tillr-drain/drain/heavy_revert_tip_passes_test.go):
  - Following the FIFO pattern of `TestStopMidPassWhileUpdateRefHoldsLockLeavesNextLoopClean` in [`tools/tillr-drain/drain/stop_prompt_test.go`](../../tools/tillr-drain/drain/stop_prompt_test.go), a `PATH` wrapper `git` script armed once after `h.landRemoval` intercepts `watchWG`'s `checkout` on the proof worktree and `exec`s `realGit -C <h.root> update-ref --stdin` driven to `prepare: ok` on a `delete` transaction so real `git` holds `<h.root>/.git/packed-refs.lock`.
  - Once `prepare: ok` is observed (guarded only by `hangGuard`, never a `< 5m` failure deadline), `onGateCtx` returns `GateOutcome{Outcome: GatePassed}`, firing `cancelWatch()` while `<h.root>/.git/packed-refs.lock` is held.
  - Before the change (`SIGKILL`), `<h.root>/.git/packed-refs.lock` remains on disk after `watchWG.Wait()`, `land.Land` fails on `git update-ref -d refs/tillr/trunk-history/lost`, and `h.reverts()` records `result: "not-possible"` (failing the test). After the change (`SIGTERM`), `git` unlinks `<h.root>/.git/packed-refs.lock` on `SIGTERM` before `watchWG.Wait()` returns, `land.Land` succeeds, and `h.reverts()` records one `result: "reverted"` event for `culprit`.

### 2.3 Rejected alternatives

- **Retrying or sleeping-and-retrying on `packed-refs.lock` in `tillr-land` or `revertLanding`:** Forbidden by the request (`"A flake is a defect: find the cause, do not add a retry"`, Criterion 6), and useless against an orphaned `packed-refs.lock` that never clears on its own.
- **Having Tillr `os.Remove` `<root>/.git/packed-refs.lock`:** Unsafe — Tillr cannot distinguish an orphaned `packed-refs.lock` from a live ref transaction held by a concurrent `tillr-land` or `git` process in the same repository.
- **Adding an in-process Go mutex around Git calls without fixing `SIGKILL` cancellation:** Does not stop `cancelWatch()` from `SIGKILL`ing `git` mid-lockfile, so `packed-refs.lock` would still be orphaned on disk for `land.Land` and external `tillr-land` processes.
- **Running `heavy.GitOutput` with uncancellable `exec.Command` (`context.Background()`):** Would let a wedged `git` subprocess block `watchWG.Wait()` indefinitely; process-group `SIGTERM` + `5s` `WaitDelay` lets Git unlink its `.lock` files immediately (`~2 ms`) while keeping shutdown bounded.
- **Importing `heavyrun` from `heavy`:** Violates [`D-GR47`](../reference/gate.md#D-GR47), under which `heavy` stays a leaf package imported by `view`, `heavyrun`, and `drain`.

---

## 3. Scope and Graduation

- **Single-piece build scope:** Edit [`tools/tillr-drain/heavy/status.go`](../../tools/tillr-drain/heavy/status.go) (plus a unit test in `tools/tillr-drain/heavy/` if helpful), [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go), and [`tools/tillr-drain/drain/heavy_revert_tip_passes_test.go`](../../tools/tillr-drain/drain/heavy_revert_tip_passes_test.go), then run `just format` and `just pack-sync` in the same piece so the `yolo-packs/tillr/` mirror lands in one commit without cross-piece write-fence overlap.
- **Reference graduation ([`R-NOQ1`](./no-open-questions-in-references.md#R-NOQ1)):** Once the build item lands and is proved green on `main`, fold the verified `SIGTERM` lock-cleanup guarantee into [`docs/reference/gate.md`](../reference/gate.md#a-red-proof) and mark this document `GRADUATED`.

---

## 4. Decision Ledger

Only rows quoting the owner's dated words verbatim are owner rulings. [`D-HRPL1`](#D-HRPL1) is **the designer's decision answering a filed operator request — not an owner ruling — and the owner may overturn it**.

| ID | Decision | Date / Authority | Where | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-HRPL1"></a>**[D-HRPL1](#D-HRPL1)** | **Designer's decision from a filed operator request (not an owner ruling; the owner may overturn it).** Resolves the definition conflict on the 2026-11-26 `TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed` `packed-refs.lock` request: the two colliding Git commands in one repository are (1) `watchWG`'s `tipChecksPass` -> `tryReuseProofWorktree` `git checkout --force --detach` / `git reset --hard` (and fallback / replay `git worktree add --detach`), which `cancelWatch()` killed with `SIGKILL` via `exec.CommandContext` in `heavy.GitOutput` while holding `<root>/.git/packed-refs.lock`, and (2) `land.Land` (`pinHistory`)'s `git update-ref -d refs/tillr/trunk-history/lost`, which failed after `watchWG.Wait()` on that orphaned `packed-refs.lock`. `heavy.GitOutput` / `heavy.Green` (`tools/tillr-drain/heavy/status.go`) and `detachedWorktree` (`tools/tillr-drain/drain/steps.go`) run `git` in its own process group (`Setpgid: true`) with `cmd.Cancel` sending `SIGTERM` to `-cmd.Process.Pid` and `cmd.WaitDelay = 5 * time.Second` (plus `just pack-sync`), ordering Git's `tempfile.c` lock cleanup before `watchWG.Wait()` returns with zero retries and zero wait-and-try-again, verified by a deterministic FIFO-synchronized regression test in `tools/tillr-drain/drain/heavy_revert_tip_passes_test.go` that fails before the change and passes after it; Criteria 5 and 6 are read against this established cause. **Rejected:** retrying or sleeping on `packed-refs.lock`; unlinking `packed-refs.lock` from Tillr; an in-process Go mutex leaving `SIGKILL` intact; uncancellable `exec.Command` without `WaitDelay`; importing `heavyrun` into `heavy` ([`D-GR47`](../reference/gate.md#D-GR47)). | 2026-11-26, designer, from a filed operator request | [§1](#1-forensics-of-the-2026-11-26-failure-and-the-conflict-on-criteria-5-and-6), [§2](#2-graceful-git-cancellation-and-conflict-resolution-d-hrpl1), [§3](#3-scope-and-graduation) | No |
