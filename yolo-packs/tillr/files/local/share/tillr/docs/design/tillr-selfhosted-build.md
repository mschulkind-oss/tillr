---
title: "Self-Hosted Tillr Building, Watchable in the Observatory"
status: in-review
stage: DESIGN
updated: 2026-11-18
tags:
  - tillr
  - self-hosting
  - observatory
  - distribution
  - design
summary: "How tillr runs the source its own trunk holds, with no relaunch between a landing and its use. The restart, the front-end rebuild and the question hold are built; the source launcher is not; one question is open, about the copy of tillr's source that the pack ships."
next: "Rule OQ-SH2: whether the tillr pack's copy of the source follows every landing or is refreshed when published. The source launcher of Section 4 waits on nothing."
---

# Self-Hosted Tillr Building, Watchable in the Observatory

> **Delivered copy.** This is `docs/design/tillr-selfhosted-build.md` from this repository at commit `973b12eb2d` (2026-11-18), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-selfhosted-build.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-18. Partly built ([§2](#2-what-is-built-and-what-is-left)). The loop in this repository runs binaries built from `tools/`, restarts on request and rebuilds the front end, all through [the restart design](tillr-restart.md). The source launcher of [§4](#4-what-is-left-to-build-the-source-launcher) is not built: a plain `tillr` on this jail's PATH still builds from the pack's copy. Two ledger rows ([OQ-SH1](#OQ-SH1), [OQ-SH3](#OQ-SH3)) are designer's decisions the owner has not ruled on.

> **In short.** The loop should run the code its own trunk holds, with nothing to relaunch between a landing and its use. What is left of that is one rule for the launchers: every tillr program is built from the main checkout's source and finds its sibling programs beside itself, never through `PATH`.

**Why it matters.** A source build that the launcher cannot see cost a wrong conclusion in one session: the coordinator read a stale binary's behavior as the current source's. And a source build that opens the live store migrates it forward and locks out every launched binary, a one-way door.

**The shape.** One source launcher per program, a rule that sibling programs are resolved from the executable's own directory and a partial set is refused, and a store check on every read.

**Cost.** One final relaunch to install the launchers, and a cold build of one to three seconds whenever the tillr source changes. It forecloses editing `/workspace/tools` uncommitted while the loop runs.

**Start at [§3](#3-mechanism-and-one-writer-rules)**, the mechanism. [§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs) is the background to the open question.

**Needs your ruling:** [OQ-SH2](#OQ-SH2).

**Reads with:** [tillr-restart.md](tillr-restart.md) (owns restarting and the roll command that turns the trunk into running binaries), [tillr-distribution.md](tillr-distribution.md) (the launcher this implements, [§2](tillr-distribution.md#2-build-and-delivery)), [tillr-convergence.md](tillr-convergence.md) (its decision `D-TC5` on copies a pipeline derives), and [observatory-redesign.md](observatory-redesign.md) (the board, which this design no longer sequences).

**Words.** A **pack** is a directory under `yolo-packs/` that yolo renders into an agent's home; the tillr pack is what puts `tillr` on `PATH` in a workspace ([AGENTS.md](https://github.com/mschulkind-oss/tillr/blob/main/AGENTS.md)). The **pack's copy** is the copy of tillr's Go source the pack ships, which the code and the recipes call the mirror. A **launcher** is the small program on `PATH` that builds a tillr binary on first use and then runs it. [The vocabulary](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-vocabulary.md#5-the-words) defines fence, gate and lane.

## 1. Goal and Testable End State

Self-hosted building holds when two behaviors can be checked:

1. **Tree code is live without a relaunch.** After a change to `tools/tillr*` lands on `main`, the next command-line call runs the new code with no jail restart and no `PATH` edit. A call that reuses a build starts no Go process.
2. **A candidate build cannot poison the trunk or the live store.** A worker in its own worktree never opens `/workspace/.tillr/state.db` and never replaces the binaries the loop runs. A broken build fails inside its lane or at the gate, before `main` moves.

## 2. What Is Built and What Is Left

Read from the tree on 2026-11-18.

| Part | State |
| :--- | :--- |
| **The source launcher and sibling resolution** ([§4](#4-what-is-left-to-build-the-source-launcher)) | Not built. The launchers in `yolo-packs/tillr/programs/` build from the pack's copy only. |
| **Restart on request** | Built, by [the restart design](tillr-restart.md): neither the loop nor the observatory server watches for new code; each restarts when asked, and this repository asks after a landing that touched tillr. |
| **Front-end rebuild** | Built: the roll command of the restart design rebuilds the front end before the Go build. |
| **Holding an item on an open question** | Built: the planner reads questions from Vantage's own index and holds an item whose design has one open, with the reason `question-open`. |
| **The fence covers the pack's copy** | Built: a lane fenced to a tillr module may also write that module's copy. |
| **The read faces refuse a missing or unreadable store** | Built in `tools/tillr-view`. |
| **The observatory sequence** this design once set out, which promoted seven queue items by rank | Left this design. Ranks no longer exist, and the observatory was built and then redesigned ([observatory-redesign.md](observatory-redesign.md)), which owns its own order. |

## 3. Mechanism and One-Writer Rules

Tillr copies the launcher of the yolo-jail project and does not invent one. Three rules:

- **The launcher slot is a pointer, and it is classified before anything is written.** A slot is one of: absent; a deployed binary; a link into this checkout, which builds or reuses the tree as it stands; a link into a *different* checkout; or something foreign. A link into a different checkout is refused, and that is the case tillr most needs: a worker's worktree must never silently run its own ungated code. Every command says what it replaced.
- **A build is a matched bundle keyed by its inputs.** The inputs are `tools/tillr/*`, every `tools/tillr-*/` module and the front end's source, hashed by path, kind and content. A later call with the same hash reuses the bundle; publication is a write to a temporary name and then a move. The bundle also records which tree it came from, because an uncommitted tree can hash the same as a clean one.
- **The source is the main checkout's.** The launcher takes `tools/` from the main worktree of the repository it is run in, else from an explicit `TILLR_SOURCE_ROOT`, else from the installed copy. A linked worktree therefore resolves back to `/workspace` and runs gated code. `tillr` and `tillr-drain` look for their sibling programs in their own directory first and refuse a partial set; they never fall back to `PATH`. Every launcher prints the tree, the hash, and whether it built or reused.

One `main` tree feeds every process in `/workspace`, so sharing a store between versions is not needed: a binary older than the store's schema is refused, as today. A candidate whose Go or front-end build fails is refused at the gate, before the landing.

| Surface | The one writer | When it fails |
| :--- | :--- | :--- |
| `/workspace/.tillr/state.db` | The loop's own `tillr-*` programs, running on `main`. Lanes use stores of their own. | The read faces open it read-only and check on every read that it is the same file at a schema they know; a missing, replaced or newer store is refused, never shown from memory. |
| `/workspace` and `refs/heads/main` | The lander, one landing at a time. | An uncommitted change in `/workspace` stops dispatch. A failed candidate build is refused at the gate and `main` does not move. |
| The build cache | The launcher: a temporary output per process, moved into place, and then the `current` link is switched. | A compiler error deletes the temporary output, leaves the previous build in place, does not fall back to it silently, and exits non-zero with the compiler's output. |

## 4. What Is Left to Build: the Source Launcher

One step. It makes these true: the launcher slot is classified, including the refusal of a link into another checkout; one bundle per source hash, reused and published atomically; siblings resolved beside the executable, a partial set refused; and every launcher prints tree and hash.

**How to check it.** Copy the tree to a scratch checkout, edit one string in `tools/tillr-view`, and run `tillr view status` there. It prints a new hash and that it built. A second call prints that it reused the build and starts no Go process. Then point the slot at another checkout and confirm the launcher refuses.

**What it costs.** One final host apply and jail relaunch to install the launchers. After it, a change to tillr needs neither.

## 5. The Pack's Copy of the Source: What It Is For and What It Costs

Read against the tree on 2026-11-18. This is the background to [OQ-SH2](#OQ-SH2).

**What it is.** `just pack-sync`, a recipe in [`.justfile`](https://github.com/mschulkind-oss/tillr/blob/main/.justfile), copies the Go source of the twelve tillr modules under `tools/` into `yolo-packs/tillr/files/local/share/tillr/<module>/`. [`tests/pack-sync.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/pack-sync.test.py) fails when a copy differs from its source, and the gate runs that test as its fast `pack-sync` check ([`tools/gate/checks/pack-sync.json`](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/pack-sync.json)).

**Why it exists.** A pack ships source and never a compiled program ([its README](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/README.md)), and each launcher builds from the copy once per source hash. A pack reads real files from the repository, so the copy is committed. It is what puts `tillr` on `PATH` in a workspace that has no `tools/`, and, until the source launcher is built, in this jail too.

**What it costs today.**

- **Every change is written twice.** From 2026-11-10 to 2026-11-18, 1,113 commits added 283,927 lines under `tools/tillr*` and 304,304 under the copy (`git log --since=2026-11-10 --numstat` over each path).
- **Every worker runs the recipe.** The worker's brief tells a run that touches `tools/tillr*` to run `just pack-sync` and commit the copy in the same commit ([`brief-template.md`](../../tillr-run/backends/brief-template.md)).
- **The check no longer fails.** It failed four times on 2026-11-13, before the fence covered the copy and before the brief carried that line. On 2026-11-17, of 101 gate reports the drain kept, 61 ran the check and all passed, and 40 skipped it because nothing it reads had changed. It takes about a quarter of a second.

**What is already decided, and by whom.** [The convergence design](tillr-convergence.md#decision-ledger) decided on 2026-11-15 that a pipeline regenerates its derived copies between the commit and the gate and never asks the worker to (`D-TC5`). That is a designer's decision, not an owner's ruling, and its step is not built. It moves the recipe out of the worker. It does not remove the doubled history.

**What is not offered.** Shipping no copy at all, with the pack reading `tools/` directly, would remove the question. Whether yolo accepts a pack that names files outside its own directory is not known: it refuses a pack whose manifest is a link out of the pack ([AGENTS.md](https://github.com/mschulkind-oss/tillr/blob/main/AGENTS.md)), and no one has tried the other. It is a thing to measure, not an option to choose yet.

**The precedent for a dated snapshot.** `just pack-docs` ships the design documents the same way but on purpose not on every landing: it is refreshed by hand, and the copy is dated.

## 6. Open Questions

1. 💬 **OQ-SH2: Should the copy of tillr that other workspaces run follow every landing, or be refreshed only when someone publishes it?**

   Every tillr source file is kept twice in this repository: under `tools/`, where it is edited, and inside the tillr pack, which is what puts `tillr` on `PATH` in any other workspace, such as the Product QA pipeline's, and today in this jail. Each worker that changes tillr runs `just pack-sync` and commits both. In the eight days to 2026-11-18 the copy added about 304,000 lines to history beside the source's 284,000. A check refuses any landing whose copy differs; it passed all 61 times it ran in the reports kept on 2026-11-17.

   - **A — Follow every landing.** The loop regenerates the copy itself before the gate, so no worker runs the recipe. Another workspace gets the newest tillr at its next relaunch. Every change is still written twice in git.
   - **B — Refresh on publish.** A landing changes only `tools/`. Someone runs one command when tillr should be published, as is already done for the shipped design documents, and history stops doubling. Until it is run, other workspaces and this jail's `tillr` run the last published version, and nothing tells them it is stale.

   <!-- vantage: question id=OQ-SH2 leaning="A — follow every landing: a stale tillr in another workspace fails silently, while the doubled history is a cost only machines pay." -->

   _Leaning:_ A — follow every landing: a stale tillr in another workspace fails silently, while the doubled history is a cost only machines pay.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

**Who decided each row.** The owner has ruled on none of these rows directly. `D-SH1` to `D-SH5` are the design as written on 2026-11-13. He may overturn any row.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-SH1` | `tillr` and `tillr-drain` resolve sibling `tillr-*` programs in their own directory first and refuse a partial set. Reason: on 2026-11-13 a source-built `tillr` silently ran the launched `tillr-item` through `PATH`. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | — |
| `D-SH2` | The launcher takes its source from `TILLR_SOURCE_ROOT`, else the main worktree's `tools/`, else the installed copy. Reason: it runs the trunk's code with no relaunch and keeps a linked worktree from running its own ungated edits. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | — |
| `D-SH3` | A failed build never falls back to an older one. The other half of this row, pinning one set of binaries for each pass of the loop, is replaced by the restart design's owner rulings of 2026-11-15: the loop runs one build until it is asked to restart, and checks the new binary before giving up its place. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | Partly: the launcher half is not built |
| `D-SH4` | Sharing a store between versions is deferred, and the read faces check the store on every read. Reason: one tree feeds every binary in `/workspace`, so a strict refusal catches a stale server. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | ✅ |
| `D-SH5` | Withdrawn. It promoted seven observatory items by queue rank; ranks no longer exist and the observatory's own designs order that work. | 2026-11-13, designer | [§2](#2-what-is-built-and-what-is-left) | — |
| OQ-SH1 | **Written by the designer on 2026-11-15 from the owner's rulings of that day in [the restart design](tillr-restart.md#decision-ledger) (`D-RT1`, `D-RT3`); the owner did not rule this row itself and may overturn it.** Neither process watches for new code. Each restarts on request: the loop replaces itself in place without waiting for runs, the observatory server between two requests, and this repository files that request after a landing that touched tillr. **Rejected:** restarting whenever the tools directory changes, and pinning until a manual restart. | 2026-11-15, designer | [§2](#2-what-is-built-and-what-is-left) | ✅ |
| OQ-SH3 | **Designer's decision, 2026-11-15; not ruled by the owner; the owner may overturn it.** The front end is rebuilt by the roll command, before the Go build, whenever its inputs changed since the running build; no launcher computes a hash of it and no command-line call pays for a Node build. **Rejected:** a hash inside every launcher, and following the front end only through the dev server. The reason given: [the restart design](tillr-restart.md#the-developers-command-in-this-repository) makes one command responsible for turning the trunk into running binaries. | 2026-11-15, designer | [§2](#2-what-is-built-and-what-is-left) | ✅ |

## 7. Out of Scope

- **Sharing one store between versions** ([tillr-distribution.md](tillr-distribution.md) Step 3): unnecessary while every binary in `/workspace` is built from `main`.
- **The observatory's screens and their order**: [observatory-redesign.md](observatory-redesign.md).
- **Restarting and the roll command**: [tillr-restart.md](tillr-restart.md).
- **Spend and burn-rate display**: iceboxed on [the tillr roadmap](../tillr/roadmap.md#icebox).
