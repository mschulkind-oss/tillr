---
title: "Self-Hosted Tillr Building, Watchable in the Observatory"
status: in-review
stage: DESIGN
updated: 2026-11-20
tags:
  - tillr
  - self-hosting
  - observatory
  - distribution
  - design
summary: "How tillr runs the source its own trunk holds, with no relaunch between a landing and its use. Restart on request, the front-end rebuild, the question hold and planner sibling resolution are built; OQ-SH2 is ruled to follow every landing and D-SH6 directs reading from Git like pi-fork; OQ-SH4 is open on the concrete Git delivery mechanism, and Git-based pack delivery, the main-tree source launcher and full sibling resolution are not built."
next: "OQ-SH2 is decided (follow every landing, 2026-11-20) with owner direction D-SH6 to read from Git like pi-fork; rule OQ-SH4 on the concrete Git delivery mechanism; Git-based pack delivery, the Section 4 source launcher and full D-SH1 sibling resolution remain unbuilt."
---

# Self-Hosted Tillr Building, Watchable in the Observatory

**Status:** 2026-11-20. Partly built ([§2](#2-what-is-built-and-what-is-left)). The loop in this repository runs binaries built from `tools/`, restarts on request and rebuilds the front end, all through [the restart design](./tillr-restart.md). `tools/tillr-view` resolves its `tillr-drain` sibling beside itself or by same-digest pack sibling before `PATH` (`tools/tillr-view/siblings.go`). On 2026-11-20 the owner ruled [OQ-SH2](#OQ-SH2) to follow every landing and directed delivering tillr from Git like `pi-fork` (`D-SH6`), leaving [OQ-SH4](#OQ-SH4) open on the concrete Git delivery mechanism. Git-based pack delivery, the main-tree source launcher of [§4](#4-what-is-left-to-build-the-source-launcher) and full `D-SH1` sibling resolution are not built: `yolo-packs/tillr` still ships the `just pack-sync` copy (`~/.local/share/tillr`), its launchers build from that copy rather than the main checkout's `tools/`, and `tools/tillr-cli/main.go` still dispatches via `exec.LookPath`. Five `D-SH*` rows and two `OQ-SH*` rows ([OQ-SH1](#OQ-SH1), [OQ-SH3](#OQ-SH3)) are designer's decisions the owner has not ruled on.

> **In short.** The loop should run the code its own trunk holds, with nothing to relaunch between a landing and its use. What is left of that is one rule for the launchers: every tillr program is built from the main checkout's source and finds its sibling programs beside itself, never through `PATH`.

**Why it matters.** A source build that the launcher cannot see cost a wrong conclusion in one session: the coordinator read a stale binary's behavior as the current source's. And a source build that opens the live store migrates it forward and locks out every launched binary, a one-way door.

**The shape.** One source launcher per program, a rule that sibling programs are resolved from the executable's own directory and a partial set is refused, and a store check on every read.

**Cost.** One final relaunch to install the launchers, and a cold build of one to three seconds whenever the tillr source changes. It forecloses editing `/workspace/tools` uncommitted while the loop runs.

**Start at [§3](#3-mechanism-and-one-writer-rules)**, the mechanism. [§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs) records the [OQ-SH2](#OQ-SH2) ruling and frames [OQ-SH4](#OQ-SH4).

**Needs your ruling:** [OQ-SH4](#OQ-SH4).

**Reads with:** [tillr-restart.md](./tillr-restart.md) (owns restarting and the roll command that turns the trunk into running binaries), [tillr-distribution.md](./tillr-distribution.md) (the launcher this implements, [§2](./tillr-distribution.md#2-build-and-delivery)), [tillr-convergence.md](./tillr-convergence.md) (its decision `D-TC5` on copies a pipeline derives), and [observatory-redesign.md](../reference/observatory-redesign.md) (the observatory reference, which this design does not sequence).

**Words.** A **pack** is a directory under `yolo-packs/` that yolo renders into an agent's home; the tillr pack is what puts `tillr` on `PATH` in a workspace ([AGENTS.md](../../AGENTS.md)). The **pack's copy** is the copy of tillr's Go source the pack ships, which the code and the recipes call the mirror. A **launcher** is the small program on `PATH` that builds a tillr binary on first use and then runs it. [The vocabulary](./tillr-vocabulary.md#5-the-words) defines fence, gate and lane.

## 1. Goal and Testable End State

Self-hosted building holds when two behaviors can be checked:

1. **Tree code is live without a relaunch.** After a change to `tools/tillr*` lands on `main`, the next command-line call runs the new code with no jail restart and no `PATH` edit. A call that reuses a build starts no Go process.
2. **A candidate build cannot poison the trunk or the live store.** A worker in its own worktree never opens `/workspace/.tillr/state.db` and never replaces the binaries the loop runs. A broken build fails inside its lane or at the gate, before `main` moves.

## 2. What Is Built and What Is Left

Read from the tree on 2026-11-19.

| Part | State |
| :--- | :--- |
| **The main-tree source launcher and sibling resolution** ([§4](#4-what-is-left-to-build-the-source-launcher)) | Partly built. Sibling resolution is built for the observatory server's planner (`tools/tillr-view/siblings.go`, covered by `TestPlannerResolutionPrefersTheSiblingOverPATH` in `tools/tillr-view/planner_internal_test.go`), which resolves `tillr-drain` beside itself or by same-digest pack sibling before falling back to `PATH`. Full `D-SH1` sibling resolution is not built: `tools/tillr-cli/main.go` still dispatches with `exec.LookPath` and does not refuse a partial sibling set. The main-tree source launcher is not built: the launchers in `yolo-packs/tillr/programs/` build from the installed pack copy (`~/.local/share/tillr`) rather than selecting the main checkout's `tools/`, and `.justfile` `tillr-build` builds explicitly from `refs/tillr/green` into a durable scratch `bin` directory that must be prepended to `PATH`. |
| **Restart on request** | Built, by [the restart design](./tillr-restart.md): neither the loop nor the observatory server watches for new code; each restarts when asked, and this repository asks after a landing that touched tillr. |
| **Front-end rebuild** | Built: the roll command of the restart design rebuilds the front end before the Go build. |
| **Holding an item on an open question** | Built: the planner reads questions from Vantage's own index and holds an item whose design has one open, with the reason `question-open`. |
| **The fence covers the pack's copy** | Built: a lane fenced to a tillr module may also write that module's copy. |
| **Delivering the pack from Git instead of a committed copy** ([§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs), [OQ-SH2](#OQ-SH2), `D-SH6`) | Not built, and awaiting [OQ-SH4](#OQ-SH4). Other workspaces follow every landing today only because workers run `just pack-sync` and commit `yolo-packs/tillr/files/local/share/tillr/`. |
| **The read faces refuse a missing or unreadable store** | Built in `tools/tillr-view`. |
| **The observatory sequence** this design once set out, which promoted seven queue items by rank | Left this design. Ranks no longer exist, and the observatory was built and redesigned ([docs/reference/observatory-redesign.md](../reference/observatory-redesign.md)). |

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

Read against the tree on 2026-11-18 and updated on 2026-11-20 for the owner's ruling on [OQ-SH2](#OQ-SH2) and delivery direction (`D-SH6`).

**What is ruled ([OQ-SH2](#OQ-SH2), `D-SH6`).** On 2026-11-20 the owner ruled that other workspaces' `tillr` follows every landing rather than waiting for a manual publish command ([OQ-SH2](#OQ-SH2), Option A), because a stale `tillr` in another workspace fails silently, and rejected refreshing on publish (`just pack-docs` stays a separate precedent for documentation only). With that ruling he added `" it should jsut read from git, like pi fork does"` (`D-SH6`).

**What the pack copy is today (unbuilt replacement).** `just pack-sync`, a recipe in [`.justfile`](../../.justfile), copies the eleven tillr modules under `tools/` (nine Go modules plus source-only `tillr-trace` and `tillr-roll`) into `yolo-packs/tillr/files/local/share/tillr/<module>/`. [`tests/pack-sync.test.py`](../../tests/pack-sync.test.py) fails when a copy differs from its source, and the gate runs that test as its fast `pack-sync` check ([`tools/gate/checks/pack-sync.json`](../../tools/gate/checks/pack-sync.json)). A pack reads real files from the repository ([its README](../../yolo-packs/tillr/README.md)), and each launcher builds from the copy once per source hash; that copy is what puts `tillr` on `PATH` in a workspace that has no `tools/`, and, until the source launcher is built, in this jail too.

**What that copy costs today.**

- **Every change is written twice.** From 2026-11-10 to 2026-11-18, 1,113 commits added 283,927 lines under `tools/tillr*` and 304,304 under the copy (`git log --since=2026-11-10 --numstat` over each path).
- **Every worker runs the recipe.** The worker's brief tells a run that touches `tools/tillr*` to run `just pack-sync` and commit the copy in the same commit ([`brief-template.md`](../../tools/tillr-run/backends/brief-template.md)).
- **The check no longer fails.** It failed four times on 2026-11-13, before the fence covered the copy and before the brief carried that line. On 2026-11-17, of 101 gate reports the drain kept, 61 ran the check and all passed, and 40 skipped it because nothing it reads had changed. It takes about a quarter of a second.

**How `D-SH6` differs from Option A as this document wrote it.** Option A here built on [the convergence design](./tillr-convergence.md#decision-ledger)'s `D-TC5` (a designer's decision of 2026-11-15, unbuilt): keep the committed duplicate directory under `yolo-packs/tillr/files/local/share/tillr/` and have the loop regenerate it before the gate so workers stop running `just pack-sync`, while every `tools/tillr*` change is still written twice on `main` and compiled lazily inside each jail. By contrast, `pi-fork` ([`docs/pi/forks.md`](../pi/forks.md), [`yolo-packs/pi-fork/pack.json`](../../yolo-packs/pi-fork/pack.json)) commits no copy of the source in `yolo-packs/`: on launch, `yolo` fetches a branch in this repository (`patch/pi`) on the host, builds it once in a sealed host build, holds the last good build when a newer commit fails, and delivers the built result into every jail. Reading from Git gives [OQ-SH2](#OQ-SH2)'s policy — other workspaces follow landings automatically — while removing `yolo-packs/tillr/files/local/share/tillr/`, `just pack-sync`, the `pack-sync` module check, and the doubled history altogether (making `D-TC5`'s pipeline `pack-sync` step unnecessary for tillr's source).

**Why `D-SH6` still leaves a concrete choice ([OQ-SH4](#OQ-SH4)).** Applying `pi-fork`'s delivery to `tillr` hits two differences that admit more than one concrete design:

- **How `yolo` builds `tillr` from Git.** In `yolo` (`0.18.0`), building a program from a Git URL (`via: "source"`) requires `fork_of` naming a base pack over an external upstream, whereas `tillr` has no external upstream — its source is `tools/tillr*` on `main` in this repository (where [`.tillr.toml`](../../.tillr.toml) `[push] what = "green"` already advances `origin/main` only as heavy-proved commits pass), and the pack delivers eight binaries plus source-only `tillr-trace` and module READMEs (`via: "pack"` builds only from a directory inside the pack; `yolo pack lint` in the jail accepts `fork_of: "tillr"` on a second pack, while a host launch of that workaround is not verified).
- **What `PATH` runs inside `/workspace`.** In `/workspace`, `pi-fork` leaves `PATH` on the host's launch-time build and rebuilds from Git in durable storage only for drain workers (`tillr-roll --pi`, [`docs/pi/forks.md`](../pi/forks.md#drain-only-rebuilt-pi)), whereas [§1](#1-goal-and-testable-end-state) and [§4](#4-what-is-left-to-build-the-source-launcher) here specify a source launcher on `PATH` that runs newly landed `main` code on the next CLI call with no relaunch.

Until [OQ-SH4](#OQ-SH4) is ruled and built, nothing in `yolo-packs/tillr/` changes: workers keep running `just pack-sync` so other workspaces continue following every landing.

## 6. Open Questions

1. 💬 **OQ-SH4: How should tillr build from Git, and should this workspace still rebuild on command calls?**

   You said tillr should read from Git like [the pi fork](../pi/forks.md) ([§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs)). Today `yolo` builds from Git only for forks, and here the pi fork keeps `PATH` on the launch build while [§4](#4-what-is-left-to-build-the-source-launcher) rebuilds on the next command.

   - **A — Add Git-branch builds to `yolo`, and keep [§4](#4-what-is-left-to-build-the-source-launcher).** Host launches build `origin/main` and drop the pack copy once `yolo` supports it; commands here run new landings without relaunching.
   - **B — Split tillr into base and fork packs now.** Works today, but maintains two packs.
   - **C — Same as A, without [§4](#4-what-is-left-to-build-the-source-launcher).** `PATH` runs the launch build everywhere while the loop rolls; commands here wait for relaunch.

   <!-- vantage: question id=OQ-SH4 leaning="A — add direct Git-branch builds to yolo and keep Section 4's launcher: one pack with host builds for other workspaces, and no relaunch needed for commands in this workspace." -->

   _Leaning:_ A — add direct Git-branch builds to `yolo` and keep [§4](#4-what-is-left-to-build-the-source-launcher)'s launcher: one pack with host builds for other workspaces, and no relaunch needed for commands in this workspace.

   **Answer:**

   > _(empty — fill in when decided)_

## Decision Ledger

**Who decided each row.** [OQ-SH2](#OQ-SH2) and `D-SH6` record the owner's 2026-11-20 review comment and typed direction in his own words. `D-SH1` to `D-SH5` (2026-11-13) and [OQ-SH1](#OQ-SH1) and [OQ-SH3](#OQ-SH3) (2026-11-15) are designer's decisions; the owner has not ruled on them directly and may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-SH1` | `tillr` and `tillr-drain` resolve sibling `tillr-*` programs in their own directory first and refuse a partial set. Reason: on 2026-11-13 a source-built `tillr` silently ran the launched `tillr-item` through `PATH`. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | Partly: `tools/tillr-view/siblings.go` resolves `tillr-drain` beside itself or by same-digest pack sibling before `PATH`; `tools/tillr-cli/main.go` still uses `exec.LookPath` |
| `D-SH2` | The launcher takes its source from `TILLR_SOURCE_ROOT`, else the main worktree's `tools/`, else the installed copy. Reason: it runs the trunk's code with no relaunch and keeps a linked worktree from running its own ungated edits. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | — |
| `D-SH3` | A failed build never falls back to an older one. The other half of this row, pinning one set of binaries for each pass of the loop, is replaced by the restart design's owner rulings of 2026-11-15: the loop runs one build until it is asked to restart, and checks the new binary before giving up its place. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | Partly: the launcher half is not built |
| `D-SH4` | Sharing a store between versions is deferred, and the read faces check the store on every read. Reason: one tree feeds every binary in `/workspace`, so a strict refusal catches a stale server. | 2026-11-13, designer | [§3](#3-mechanism-and-one-writer-rules) | ✅ |
| `D-SH5` | Withdrawn. It promoted seven observatory items by queue rank; ranks no longer exist and the observatory's own designs order that work. | 2026-11-13, designer | [§2](#2-what-is-built-and-what-is-left) | — |
| `D-SH6` | **Owner direction (delivery from Git):** Deliver tillr by reading and building from what Git holds, the way `pi-fork` is delivered from a branch in this repository ([`docs/pi/forks.md`](../pi/forks.md)), rather than maintaining a committed source copy under `yolo-packs/tillr/files/` via `just pack-sync`. Typed with the [OQ-SH2](#OQ-SH2) ruling on 2026-11-20: `" it should jsut read from git, like pi fork does"`. Because Option A in this document kept the committed copy and `just pack-sync`, and applying `pi-fork`'s delivery to tillr admits more than one concrete design, the choice between those mechanisms is left open in [OQ-SH4](#OQ-SH4). | 2026-11-20, owner | [§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs), [OQ-SH4](#OQ-SH4) | — |
| OQ-SH1 | **Written by the designer on 2026-11-15 from the owner's rulings of that day in [the restart design](./tillr-restart.md#decision-ledger) (`D-RT1`, `D-RT3`); the owner did not rule this row itself and may overturn it.** Neither process watches for new code. Each restarts on request: the loop replaces itself in place without waiting for runs, the observatory server between two requests, and this repository files that request after a landing that touched tillr. **Rejected:** restarting whenever the tools directory changes, and pinning until a manual restart. | 2026-11-15, designer | [§2](#2-what-is-built-and-what-is-left) | ✅ |
| OQ-SH2 | **Owner ruling (Option A):** Other workspaces' tillr follows every landing rather than refreshing only when someone publishes it, so another workspace never silently runs a stale tillr. Ruled on 2026-11-20 as a review comment: `"A — follow every landing: a stale tillr in another workspace fails silently, while the doubled history is a cost only machines pay."` Together with this comment he typed `" it should jsut read from git, like pi fork does"` (`D-SH6`), replacing Option A's `just pack-sync` copy with Git delivery ([OQ-SH4](#OQ-SH4)). | 2026-11-20, owner | [§5](#5-the-packs-copy-of-the-source-what-it-is-for-and-what-it-costs) | Partly: workers currently run `just pack-sync` on every landing; Git delivery ([OQ-SH4](#OQ-SH4)) is not built |
| OQ-SH3 | **Designer's decision, 2026-11-15; not ruled by the owner; the owner may overturn it.** The front end is rebuilt by the roll command, before the Go build, whenever its inputs changed since the running build; no launcher computes a hash of it and no command-line call pays for a Node build. **Rejected:** a hash inside every launcher, and following the front end only through the dev server. The reason given: [the restart design](./tillr-restart.md#the-developers-command-in-this-repository) makes one command responsible for turning the trunk into running binaries. | 2026-11-15, designer | [§2](#2-what-is-built-and-what-is-left) | ✅ |

## 7. Out of Scope

- **Sharing one store between versions** ([tillr-distribution.md](./tillr-distribution.md) Step 3): unnecessary while every binary in `/workspace` is built from `main`.
- **The observatory's screens and their order**: [docs/reference/observatory-redesign.md](../reference/observatory-redesign.md).
- **Restarting and the roll command**: [tillr-restart.md](./tillr-restart.md).
- **Spend and burn-rate display**: iceboxed on [the tillr roadmap](../tillr/roadmap.md#icebox).
