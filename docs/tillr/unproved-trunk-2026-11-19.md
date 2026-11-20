---
title: "Why the red trunk went unrepaired on 2026-11-19"
date: 2026-11-20
status: accepted
stage: CURRENT
tags: [tillr, drain, incident, rolling-tier, bisection]
summary: "Root-cause record from the drain's logs and store events explaining why a red trunk introduced on 2026-11-19 went undetected and unrepaired until after midnight on 2026-11-20, evaluating the 20-restart hypothesis, and documenting why no landing record matched after the 00:07 rebase."
---

# Why the red trunk went unrepaired on 2026-11-19

**Status:** 2026-11-20. Incident record reconstructed from the **drain** (the `tillr-drain` loop daemon) logs, Git history, and **store** (SQLite database) event rows.

## Summary

On 2026-11-19, `refs/tillr/green` (the **green ref** pointing to the latest commit proved green by the **rolling tier**, the post-landing heavy verification suite) stayed at `d94406b82639` from 15:56:56 ET until after midnight on 2026-11-20. During that eight-hour window, a commit introducing a failing test (`TestSignalVsCrashEndCauses/direct-sigint` in `go-tool/tools-tillr-run-s-whole-suite-passes`) landed on `main` (the **trunk**), yet neither an automatic revert nor an automated **fixer** (`Fix`, a repair item filed for an un-revertible red commit) ran before midnight.

Three interacting faults caused the gap:

1. **An unconfirmed cleanup guard blocked every heavy run from 16:29 ET to 00:10 ET.** An abrupt loop crash at 16:29:29 ET left `.git/tillr-gate/heavy.cleanup-pending` (the **cleanup guard** preventing a new heavy run while a prior run's cleanup is unconfirmed) on disk, causing every subsequent `HeavyRunner.Run` call to abort in under 10 ms.
2. **A binary-update deadlock prevented the fix from reaching the running loop.** Dead-supervisor recovery landed at 18:02:13 ET, but `just tillr-roll` failed silently on a dirty main checkout and `just tillr-build` built from `refs/tillr/green`, leaving the running binary without the recovery code needed to advance `refs/tillr/green`.
3. **Four post-midnight 1-tick restarts repeatedly aborted bisection, and a 00:07 rebase invalidated landing-record matching.** The 16 restarts before 00:09 ET did not interrupt an active check because no heavy check was running. However, once `d33ed9f01256` cleared the guard at 00:10:04 ET, four consecutive 1-tick crashes (`drain-4935-4935` through `drain-4938-4938`) cut the after-merge run and its **culprit bisection** (binary search between the green ref and the failing tip) short until `355cd5966` landed at 00:21 ET. When bisection finally identified `d8817ff2eebe` at 00:23:51 ET, its SHA no longer matched any **landing record** (`landings.trunk_after` in the store) because `git pull --autostash` at 00:07:46 ET had rebased `main`.

## Verified timeline

All times below are Eastern Time (ET) on 2026-11-19 and 2026-11-20.

### 1. Last green proof and the 16:29 crash

- **15:45:12 ET** — Commit `d94406b82639` was authored.
- **15:56:56 ET (`20:56:56Z`)** — `d94406b82639` was proved green and advanced `refs/tillr/green`.
- **16:29:29 ET (`21:29:29Z`)** — Loop process `2894912` died abruptly without writing a `drain-*.json` stop record while the **heavy supervisor** (the detached process executing rolling-tier checks, PID `389493`, `start_ticks=9276691`) was mid-run. Because the supervisor did not confirm teardown, `.git/tillr-gate/heavy.cleanup-pending` remained on disk.

### 2. Guarded aborts and suppressed retries (16:33 ET – 00:09 ET)

From 16:33 ET through 00:09 ET, across 17 loop starts (`drain-4230-4232` through `drain-4934-4934`), the running `tillr-drain` binary (`713e9bf7c599` at 16:33 ET, then `d94406b82639` from 18:23 ET onward) failed every `HeavyRunner.Run` call in under 10 ms before judging any check:

> `loop: heavy runner fault: prior cleanup is unconfirmed; checkout retained; independent process recovery must inspect and clear .../.git/tillr-gate/heavy.cleanup-pending`

Two additional behaviors compounded the stall:

- Any non-`cleanupLost` runner error set `l.heavyAttempted = tip`, suppressing retries on that tip for the remainder of that loop process's lifetime.
- Five 1-**tick** (single scheduling pass) starts (`drain-4630-4630` through `drain-4634-4634`, 21:01–21:25 ET) faulted in `reconcileLandingCalls` before even reaching `maybeRolling`.

### 3. Deadlock on the dead-supervisor recovery fix

Dead-supervisor recovery landed at **18:02:13 ET** as `4322d04c6c47` [unlanded] (later rebased at 00:07:46 ET to `f8eed6a0b`). That fix landed *before* the commit that introduced the failing test `TestSignalVsCrashEndCauses/direct-sigint` in `go-tool/tools-tillr-run-s-whole-suite-passes` (authored **17:55:26 ET**, landed **21:32:08 ET** as `be3471aefb90` [unlanded], later rebased at 00:07:46 ET to `d8817ff2eebe`).

Yet `f8eed6a0b` never ran in the loop before midnight for two reasons:

1. **Silent `tillr-roll` refusals:** Every post-landing roll (`just tillr-roll --no-request`, recorded in store `drain.post-landing-action` events `1307168`..`1308879`) failed with `tillr-roll: refusing a dirty checkout` because of uncommitted edits in the main checkout. `post_landing.go` wrote those failures only to SQLite without logging them in the loop report.
2. **Green-ref build deadlock:** `just tillr-build` at 18:23 ET built from `refs/tillr/green` (`d94406b82639`), creating a deadlock in which the recovery fix needed to advance `refs/tillr/green` was itself waiting behind `refs/tillr/green`.

Because no heavy check executed between 16:29 ET and 00:10 ET, the red trunk was never detected before midnight, and neither auto-revert nor `Fix` ran while more than 25 commits landed.

### 4. Evaluating the 20-restart hypothesis

The hypothesis that 20 restarts kept cutting the after-merge run short splits into two distinct periods:

- **Before 00:09 ET (16 restarts): hypothesis rejected.** Those restarts did not interrupt a running check. Heavy runs either never started because of `reconcileLandingCalls` faults or aborted immediately on `heavy.cleanup-pending`.
- **After 00:09 ET (4 restarts): hypothesis confirmed.** At 00:09 ET, `just tillr-restart` built `d33ed9f01256` from `HEAD`, which recovered `heavy.cleanup-pending` at 00:10:04 ET. Immediately afterward, **four consecutive 1-tick loop restarts (`drain-4935-4935` through `drain-4938-4938`, 00:10–00:20 ET, faulting on overlong run-ref pins)** repeatedly cut the after-merge run and its culprit bisection short:
  - Moving `main` from `d33ed9f01256` to `c1247865b` wiped all saved `heavy-progress.json` and `heavy-legacy-progress.json` check passes because reuse requires `prior.TipSHA == tip`.
  - Completed culprit-bisection replays (`HeavyRunner.replay`) were not persisted across restarts, forcing bisection from `d94406b82639` (through `5d52a8b0af74` and subsequent commits) to restart from scratch on each restart until `355cd5966` fixed overlong **run-ref pins** (Git refs pinning a run's commit) at 00:21 ET.

### 5. Why no landing record matched after the 00:07 rebase

At **00:23:51 ET**, culprit bisection identified `d8817ff2eebe` as the first red commit. That commit had no matching `landings.trunk_after` row in the store because `git pull --autostash` at **00:07:46 ET** had rebased `main` off `d6927d19ab3a` [unlanded] and rewritten `be3471aefb90` [unlanded] to `d8817ff2eebe`. Because the landing record held the pre-rebase SHA, automatic revert declined to act and reported that the commit had no landing record.

The failing test itself was subsequently fixed in `68580a463`.
