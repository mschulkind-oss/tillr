---
title: "Self-Hosted Tillr Building, Watchable in the Observatory"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - self-hosting
  - observatory
  - distribution
  - design
summary: "How tillr runs the source its own trunk holds, with no relaunch between a landing and its use. Restart on request, the question hold and the observatory server's sibling resolution are built; the roll command embeds a front end that is already built and does not rebuild it. The main-tree source launcher, full sibling resolution and delivery of the pack from Git are not built, and OQ-SH4 is open on how tillr builds from Git."
next: "The owner rules OQ-SH4 on how tillr builds from Git. The source launcher of Section 4 waits on nothing."
---

# Self-Hosted Tillr Building, Watchable in the Observatory

**Status:** 2026-11-24. Partly built; [§2](#2-what-is-built-and-what-is-left) says which parts, each checked against the tree on that date. Other workspaces' tillr follows every landing ([OQ-SH2](#OQ-SH2)) and is to be delivered from what Git holds (`D-SH6`); how that is built is open in [OQ-SH4](#OQ-SH4).

> **In short.** The loop should run the code its own trunk holds, with nothing to relaunch between a landing and its use. What is left of that is one rule for the launchers: every tillr program is built from the main checkout's source and finds its sibling programs beside itself, never through `PATH`.

**Why it matters.** A source build that the launcher cannot see produces wrong conclusions: a stale binary's behavior is read as the current source's. And a source build that opens the live store migrates it forward and locks out every launched binary, a one-way door.

**The shape.** One source launcher per program, a rule that sibling programs are resolved from the executable's own directory and a partial set is refused, and a store check on every read.

**Cost.** One final relaunch to install the launchers, and a cold build of one to three seconds whenever the tillr source changes. It forecloses editing the main checkout's `tools/` uncommitted while the loop runs.

**Start at [§3](#3-mechanism-and-one-writer-rules)**, the mechanism. [§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs) frames [OQ-SH4](#OQ-SH4).

**Needs your ruling:** [OQ-SH4](#OQ-SH4).

**Reads with:** [tillr-restart.md](../reference/tillr-restart.md) (owns restarting and the roll command that turns the trunk into running binaries), [tillr-distribution.md](./tillr-distribution.md) (the launcher this implements, [§2](./tillr-distribution.md#2-build-and-delivery)), [tillr-convergence.md](./tillr-convergence.md) (its decision `D-TC5` on copies a pipeline derives), and [observatory-redesign.md](../reference/observatory-redesign.md) (the observatory reference, which this design does not sequence).

**Words.** A **pack** is a directory under `yolo-packs/` that yolo renders into an agent's home; the tillr pack is what puts `tillr` on `PATH` in a workspace ([its README](../../yolo-packs/tillr/README.md)). The **pack's copy** is the copy of tillr's source the pack ships, which the code and the recipes call the mirror. A **launcher** is the small program on `PATH` that builds a tillr binary on first use and then runs it. The **main checkout** is the repository's main worktree, as opposed to a lane's linked worktree. [The vocabulary](./tillr-vocabulary.md#5-the-words) defines fence, gate and lane.

## 1. Goal and Testable End State

Self-hosted building holds when two behaviors can be checked:

1. **Tree code is live without a relaunch.** After a change to `tools/tillr*` lands on `main`, the next command-line call runs the new code with no workspace relaunch and no `PATH` edit. A call that reuses a build starts no Go process.
2. **A candidate build cannot poison the trunk or the live store.** A worker in its own worktree never opens the main checkout's `.tillr/state.db` and never replaces the binaries the loop runs. A broken build fails inside its lane or at the gate, before `main` moves.

## 2. What Is Built and What Is Left

| Part | State |
| :--- | :--- |
| **The main-tree source launcher and sibling resolution** ([§4](#4-what-is-left-to-build-the-source-launcher)) | Partly built. The observatory server resolves `tillr-drain` beside itself or by same-digest pack sibling before `PATH` (`tools/tillr-view/siblings.go`, covered by `TestPlannerResolutionPrefersTheSiblingOverPATH`). `resolve` in `tools/tillr-cli/main.go` dispatches with `exec.LookPath` and does not refuse a partial sibling set. The source launcher is not built: `rg -n 'TILLR_SOURCE_ROOT' -g '!*.md' .` finds nothing, the launchers in `yolo-packs/tillr/programs/` build from the installed pack copy under `~/.local/share/tillr`, and the `tillr-build` recipe in [`.justfile`](../../.justfile) builds from `refs/tillr/green` into a directory that has to be put on `PATH` by hand. |
| **Restart on request** | Built, by [the restart reference](../reference/tillr-restart.md): neither the loop nor the observatory server watches for new code; each restarts when asked, and this repository asks after a landing that touched tillr. |
| **The front end in a rolled build** | Partly built. The roll command (`clean_checkout` in `tools/tillr-roll/tillr-roll.py`) copies an already-built `tools/tillr-view/web/dist` into its build checkout and embeds it in `tillr-view`. It does not run the Node build: `rg -n -i 'npm\|vite' tools/tillr-roll/tillr-roll.py` finds nothing. |
| **Holding an item on an open question** | Built: the planner reads questions from Vantage's own index and holds an item whose design has one open, with the reason `question-open`. |
| **The fence covers the pack's copy** | Built: a lane fenced to a tillr module may also write that module's copy (`TestFencePathsDeriveTheGateCompanions`). |
| **Delivering the pack from Git instead of a committed copy** ([§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs), `D-SH6`) | Not built, and awaiting [OQ-SH4](#OQ-SH4). Other workspaces follow every landing because workers run `just pack-sync` and commit `yolo-packs/tillr/files/local/share/tillr/`. |
| **The read faces refuse a missing or unreadable store** | Built in `tools/tillr-view`: `storeVerdictFor` names a missing store, an unreadable one and one whose schema is newer than the binary. |

## 3. Mechanism and One-Writer Rules

Three rules:

- **The launcher slot is a pointer, and it is classified before anything is written.** A slot is one of: absent; a deployed binary; a link into this checkout, which builds or reuses the tree as it stands; a link into a *different* checkout; or something foreign. A link into a different checkout is refused, and that is the case tillr most needs: a worker's worktree must never silently run its own ungated code. Every command says what it replaced.
- **A build is a matched bundle keyed by its inputs.** The inputs are `tools/tillr/*`, every `tools/tillr-*/` module and the front end's source, hashed by path, kind and content. A later call with the same hash reuses the bundle; publication is a write to a temporary name and then a move. The bundle also records which tree it came from, because an uncommitted tree can hash the same as a clean one.
- **The source is the main checkout's.** The launcher takes `tools/` from the main worktree of the repository it is run in, else from an explicit `TILLR_SOURCE_ROOT`, else from the installed copy. A linked worktree therefore resolves back to the main checkout and runs gated code. `tillr` and `tillr-drain` look for their sibling programs in their own directory first and refuse a partial set; they never fall back to `PATH`, because a source-built `tillr` that does runs whatever `tillr-item` was launched earlier. Every launcher prints the tree, the hash, and whether it built or reused.

One `main` tree feeds every process in the main checkout, so sharing a store between versions is not needed: a binary older than the store's schema is refused. A candidate whose Go or front-end build fails is refused at the gate, before the landing.

| Surface | The one writer | When it fails |
| :--- | :--- | :--- |
| The main checkout's `.tillr/state.db` | The loop's own `tillr-*` programs, running on `main`. Lanes use stores of their own. | The read faces open it read-only and check it on every read; a missing, unreadable or newer store is refused, never shown from memory. |
| The main checkout and `refs/heads/main` | The lander, one landing at a time. | An uncommitted change in the main checkout stops dispatch. A failed candidate build is refused at the gate and `main` does not move. |
| The build cache | The launcher: a temporary output per process, moved into place, and then the `current` link is switched. | A compiler error deletes the temporary output, leaves the previous build in place, does not fall back to it silently, and exits non-zero with the compiler's output. |

## 4. What Is Left to Build: the Source Launcher

One step. It makes these true: the launcher slot is classified, including the refusal of a link into another checkout; one bundle per source hash, reused and published atomically; siblings resolved beside the executable, a partial set refused; and every launcher prints tree and hash.

**How to check it.** Copy the tree to a scratch checkout, edit one string in `tools/tillr-view`, and run `tillr view status` there. It prints a new hash and that it built. A second call prints that it reused the build and starts no Go process. Then point the slot at another checkout and confirm the launcher refuses.

**What it costs.** One final relaunch of the workspace to install the launchers. After it, a change to tillr needs none.

## 5. The Pack's Copy of the Source: What It Is For and What It Costs

**What is ruled ([OQ-SH2](#OQ-SH2), `D-SH6`).** Other workspaces' `tillr` follows every landing and does not wait for a manual publish command, because a stale `tillr` in another workspace fails silently. It is delivered by reading and building from what Git holds, not from a committed copy of the source. Refreshing on publish is rejected for the source; `just pack-docs` stays a publish step for documentation only.

**What the pack copy is today.** `just pack-sync`, a recipe in [`.justfile`](../../.justfile), copies the eleven tillr modules under `tools/` that the pack ships (nine Go modules plus the source-only `tillr-trace` and `tillr-roll`) into `yolo-packs/tillr/files/local/share/tillr/<module>/`. [`tests/pack-sync.test.py`](../../tests/pack-sync.test.py) fails when a copy differs from its source, and the gate runs that test as its fast `pack-sync` check ([`tools/gate/checks/pack-sync.json`](../../tools/gate/checks/pack-sync.json)). Each launcher builds from the copy once per source hash; that copy is what puts `tillr` on `PATH` in a workspace that has no `tools/`, and, until the source launcher is built, in this repository's own workspace too.

**What that copy costs.**

- **Every change is written twice.** Over one nine-day span, `git log --numstat` counted 283,927 lines added under `tools/tillr*` and 304,304 under the copy.
- **Every worker runs the recipe.** The worker's brief tells a run that touches `tools/tillr*` to run `just pack-sync` and commit the copy in the same commit ([`brief-template.md`](../../tools/tillr-run/backends/brief-template.md)).
- **The check rarely fails.** The fence covers the copy and the brief carries that line. In a sample of 101 gate reports, 61 ran the check and all passed, and 40 skipped it because nothing it reads had changed. It takes about a quarter of a second.

**How delivery from Git differs from regenerating the copy.** [The convergence design](./tillr-convergence.md#decision-ledger)'s `D-TC5` keeps the committed directory under `yolo-packs/tillr/files/local/share/tillr/` and has the loop regenerate it before the gate, so workers stop running `just pack-sync` while every `tools/tillr*` change is still written twice on `main` and compiled lazily in each workspace. Delivery from Git commits no copy of the source in `yolo-packs/`: on launch, yolo fetches a named branch on the host, builds it once, holds the last good build when a newer commit fails, and delivers the built result into every workspace. That gives the policy of [OQ-SH2](#OQ-SH2) while removing the committed copy, `just pack-sync`, the `pack-sync` check and the doubled history, and it makes `D-TC5`'s regeneration step unnecessary for tillr's source.

**Why `D-SH6` leaves a concrete choice ([OQ-SH4](#OQ-SH4)).** Two facts admit more than one design:

- **How yolo builds `tillr` from Git.** A yolo program built from a Git URL (`via: "source"`) has to name a base pack over an external upstream (`fork_of`). Tillr has no external upstream: its source is `tools/tillr*` on `main` in this repository, where [`.tillr.toml`](../../.tillr.toml) `[push] what = "green"` advances `origin/main` only to commits the heavy proof has passed. The pack delivers eight binaries plus the source-only `tillr-trace` and the module READMEs, and `via: "pack"` builds only from a directory inside the pack. A second pack declaring `fork_of: "tillr"` passes `yolo pack lint`; a host launch of it is not verified.
- **What `PATH` runs in this repository's workspace.** A pack built from Git on the host leaves `PATH` on the launch-time build until the next launch, whereas [§1](#1-goal-and-testable-end-state) and [§4](#4-what-is-left-to-build-the-source-launcher) specify a source launcher that runs newly landed `main` code on the next call with no relaunch.

Until [OQ-SH4](#OQ-SH4) is ruled and built, nothing in `yolo-packs/tillr/` changes: workers keep running `just pack-sync`, so other workspaces keep following every landing.

## 6. Open Questions

1. 💬 **OQ-SH4: How should tillr build from Git, and should this workspace still rebuild on command calls?**

   You said tillr should read from Git ([§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs)). Today `yolo` builds from Git only for a fork pack, and a pack built that way keeps `PATH` on the launch build while [§4](#4-what-is-left-to-build-the-source-launcher) rebuilds on the next command.

   - **A — Add Git-branch builds to `yolo`, and keep [§4](#4-what-is-left-to-build-the-source-launcher).** Host launches build `origin/main` and drop the pack copy once `yolo` supports it; commands here run new landings without relaunching.
   - **B — Divide tillr into base and fork packs now.** Works today, but maintains two packs.
   - **C — Same as A, without [§4](#4-what-is-left-to-build-the-source-launcher).** `PATH` runs the launch build everywhere while the loop rolls; commands here wait for relaunch.

   <!-- vantage: question id=OQ-SH4 leaning="A — add direct Git-branch builds to yolo and keep Section 4's launcher: one pack with host builds for other workspaces, and no relaunch needed for commands in this workspace." -->

   _Leaning:_ A — add direct Git-branch builds to `yolo` and keep [§4](#4-what-is-left-to-build-the-source-launcher)'s launcher: one pack with host builds for other workspaces, and no relaunch needed for commands in this workspace.

   **Answer:**

   > _(empty — fill in when decided)_

## Decision Ledger

[OQ-SH2](#OQ-SH2) and `D-SH6` are the owner's. Every other row is the designer's decision; the owner has not ruled on it and may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-SH1` | `tillr` and `tillr-drain` resolve sibling `tillr-*` programs in their own directory first and refuse a partial set. Reason: a source-built `tillr` that resolves through `PATH` silently runs a `tillr-item` from another build. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | Partly: `tools/tillr-view/siblings.go` resolves `tillr-drain` beside itself or by same-digest pack sibling before `PATH`; `tools/tillr-cli/main.go` uses `exec.LookPath` |
| `D-SH2` | The launcher takes its source from `TILLR_SOURCE_ROOT`, else the main worktree's `tools/`, else the installed copy. Reason: it runs the trunk's code with no relaunch and keeps a linked worktree from running its own ungated edits. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | — |
| `D-SH3` | A failed build never falls back to an older one. The loop runs one build until it is asked to restart, and checks the new binary before giving up its place, as [the restart reference](../reference/tillr-restart.md) states. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | Partly: the launcher half is not built |
| `D-SH4` | Sharing a store between versions is deferred, and the read faces check the store on every read. Reason: one tree feeds every binary in the main checkout, so a strict refusal catches a stale server. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | ✅ |
| `D-SH5` | Withdrawn. This design does not order the observatory's work; the observatory's own designs do. | 2026-11-13, designer | [§7](#7-out-of-scope) | — |
| `D-SH6` | **Owner direction.** Deliver tillr by reading and building from what Git holds: a branch fetched and built once on the host, with the built result delivered into every workspace. No committed source copy under `yolo-packs/tillr/files/` kept by `just pack-sync`. The choice between the concrete mechanisms is open in [OQ-SH4](#OQ-SH4). | 2026-11-20, owner | [§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs) | — |
| OQ-SH1 | Neither process watches for new code. Each restarts on request: the loop replaces itself in place without waiting for runs, the observatory server between two requests, and this repository files that request after a landing that touched tillr. Derived by the designer from the owner's rulings `D-RT1` and `D-RT3` in [the restart reference](../reference/tillr-restart.md#why-its-this-way). **Rejected:** restarting whenever the tools directory changes, and pinning until a manual restart. | 2026-11-15, designer | [§2](#2-what-is-built-and-what-is-left) | ✅ |
| OQ-SH2 | **Owner ruling (Option A).** Other workspaces' tillr follows every landing and does not refresh only when someone publishes it, so another workspace never silently runs a stale tillr; the doubled history is a cost only machines pay. `D-SH6` replaces Option A's committed copy with delivery from Git. | 2026-11-20, owner | [§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs) | Partly: workers run `just pack-sync` on every landing; delivery from Git is not built |
| OQ-SH3 | The front end is built by the roll command, before the Go build, whenever its inputs changed since the running build; no launcher computes a hash of it and no command-line call pays for a Node build. **Rejected:** a hash inside every launcher, and following the front end only through the dev server. Reason: [the restart reference](../reference/tillr-restart.md#the-developers-command-in-this-repository) makes one command responsible for turning the trunk into running binaries. | 2026-11-15, designer | [§2](#2-what-is-built-and-what-is-left) | Partly: the roll command embeds an already-built front end and runs no Node build |

## 7. Out of Scope

- **Sharing one store between versions**: unnecessary while every binary in the main checkout is built from `main`.
- **The observatory's screens and their order**: [observatory-redesign.md](../reference/observatory-redesign.md).
- **Restarting and the roll command**: [tillr-restart.md](../reference/tillr-restart.md).
- **Spend and burn-rate display**: iceboxed on [the tillr roadmap](../tillr/roadmap.md#icebox).
