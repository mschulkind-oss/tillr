---
title: "Self-Hosted Tillr Building, Watchable in the Observatory"
status: in-review
stage: DESIGN
updated: 2026-11-13
tags:
  - tillr
  - self-hosting
  - observatory
  - distribution
  - design
summary: "How tillr moves from launch-pinned pack binaries to running its own main-tree source safely across unattended drains, sequenced so every stage is truthful and checkable in the observatory and CLI."
next: "Rule the three open questions in Section 6.2 (process reload, pack mirror synchronization, and embedded web rebuild), then land Step 1 and perform the single cutover relaunch."
---

# Self-Hosted Tillr Building, Watchable in the Observatory

> **Delivered copy.** This is `docs/design/tillr-selfhosted-build.md` from this repository at commit `b8eac4989e` (2026-11-17), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-selfhosted-build.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-13. Nothing built. Every fact below was measured against the tree and the
live store on 2026-11-13; the eight launchers still come from the pack, and the seven
observatory items this sequence promotes are queued at ranks `p`–`v`.

> **In short.** Point the eight `PATH` launchers at `/workspace/tools` instead of a
> launch-time pack build, resolve sibling binaries from the executable's own directory, verify
> the store on every read, repair the header's truth defects, and promote the seven unbuilt
> observatory items ahead of the general queue — so the first self-hosted drain upgrades the
> board the owner is watching.

**Why it matters.** A source build that the launcher cannot see cost a wrong conclusion in one
session (the coordinator read a stale binary as current), and a source build that *opens* the
live store migrates it forward and locks out every launched binary — a one-way door taken on the
way to the thing the owner actually wants, which is a loop that runs the tree's own code.

**The shape.** A digest-caching source launcher per program (already specified in
[`tillr-distribution.md`](tillr-distribution.md) [§2](tillr-distribution.md#2-build-and-delivery)), a sibling-resolution rule that refuses
partial sets, a per-read store check, and one binary cohort pinned per drain tick.

**Cost.** One final relaunch, a 1–3 s cold rebuild whenever `tools/tillr` changes, and two
`tools/tillr-view` slices spent on truth before any feature work. It forecloses editing
`/workspace/tools` dirty while the loop runs.

**Start at [§4](#4-sequence)** — the five-step sequence with an observable check per step. The
mechanism in [§3](#3-mechanism-and-one-writer-rules) is what makes it survivable; the board's
view in [§5](#5-what-the-owner-sees-on-the-board) is what makes it followable.

**Needs your ruling:** [OQ-SH2](#OQ-SH2).

**Reads with:** [observatory-design.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md) (the board this sequence
upgrades, `accepted`), [tillr-distribution.md](tillr-distribution.md) (the launcher this
implements, [§2](tillr-distribution.md#2-build-and-delivery) and
[§9](tillr-distribution.md#9-the-agents-tillr-must-not-wait-for-a-launch)), [observatory-qa-2026-11-13.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-qa-2026-11-13.md) (the
findings Steps 2 and 3 close), and [tillr-day2.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-day2.md) (the loop's end state).

## 1. Goal and Testable End State

Self-hosted, watchable building holds when three behaviors are simultaneously checkable:

1. **Tree code is live without relaunch.** After a candidate touching `tools/tillr*` passes `just done` and `tillr land pass` advances `main` in `/workspace`, the next CLI invocation and the next tick of an active `tillr-drain loop` execute the new code with no jail restart and no manual `PATH` edit. Warm calls invoke `go` zero times.
2. **Candidate builds cannot poison trunk or the live store.** Worker lanes in their scratch worktrees never open `/workspace/.tillr/state.db` and never replace coordinator binaries while iterating; a broken build fails inside its lane or at `tillr-gate` before `main` moves.
3. **Both read faces tell the same truth.** At every tick, `tillr view status --store /workspace/.tillr/state.db` and the observatory (`BoardHeader`, `CountStrip`, `QueueRail`, `NowBand`, `ItemPane`, `EvidencePane`) agree on `counts`, real `ticks`, `rejected`, `last_stop_reason`, `build_digest`, and why each non-moving item is held; a vanished, replaced, or forward-migrated store renders `RefusalState` immediately instead of `present`.

## 2. Why Now

Every obstacle below was measured on 2026-11-13:

- **Launch-pinned binaries block queue work and create a one-way store door.** The eight `tillr*` executables on `PATH` (`via: "pack"`) are built at launch and pinned. A source-built `tillr-item` accepted `rank` while the launched binary refused `json: unknown field "rank"`, and opening `/workspace/.tillr/state.db` with a newer binary migrates its schema forward, locking out the launched binaries (including `tillr-view serve`).
- **Sibling lookup via `PATH` defeated `just tillr-build`.** Building nine modules into the durable scratch `bin/` left `tillr` (`tools/tillr-cli`) executing `tillr-item` from `~/.yolo/bin/launch` via `PATH`, causing the coordinator to misread stale behavior as current source.
- **Manual pack mirroring breaks commits and lane fences.** `just pack-sync` mirrors ten `tools/tillr*` directories into `yolo-packs/tillr/files/local/share/tillr/<module>`; its gate check failed four times in one day, and narrow-fenced `tools/tillr*` items cannot pass `just done` without either automating or decoupling that mirror ([OQ-SH2](#OQ-SH2)).
- **The board currently hides loop failures.** In [observatory-qa-2026-11-13.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-qa-2026-11-13.md), a swapped store still reports `present` with an advancing clock (`F3`), `ticks: 10` counts dispatch rows across two ticks (`F2`), `last_stop_reason` is `null` despite `.tillr/loop/drain-*.json` on disk (`F4`), a `1800 s` timeout leaves `rejected: 0` (`F7`), six `in-queue` items sit silently on open design questions (`F5`), and the seven settled UI items sit at the tail of the 48-item store (`ranks` `p` through `v`).

## 3. Mechanism and One-Writer Rules

- **Binaries from the tree, by copying yolo-jail's launcher rather than inventing one.** Three mechanisms, and tillr takes all three. **The launcher slot is a pointer, and it is classified before anything is written:** `just deploy-dev` makes the launcher a symlink to a wrapper in the checkout that builds (or reuses) the tree as it stands, `just deploy` installs a real binary instead, and the slot is inspected first — `absent`, `deployed` (a binary whose embedded build info names the expected main package), `link`, `dev` (a link into *this* checkout), `other-dev` (a link into a **different** checkout, refused), `legacy`, `dangling`, `foreign` (refused, untouched) — and every command says what it replaced. `other-dev` is the one tillr most needs: a lane worktree's launcher must never silently run un-gated lane code. **A build is a matched bundle keyed by a bounded input set:** nine named files and four directories, hashed as sha256 over each path's length, path, kind and content, so one identity describes one source state; a later run reuses it, a reused bundle is touched so a reaper cannot evict it, and publication is atomic (per-PID temporary output, then a move). **The bundle records which tree it came from**, because the identity hashes source content only and a dirty tree can produce the same identity as a clean one. For tillr the set is `tools/tillr/*` plus every `tools/tillr-*/` module and `tools/tillr-view/web/src`, and the eight `program` entries in `yolo-packs/tillr` all resolve to the one bundle. The checkout that feeds them is `<git-main-worktree>/tools` (via the parent of `git rev-parse --git-common-dir`) when `tools/tillr/go.mod` exists there, else an explicit `TILLR_SOURCE_ROOT`, else the installed copy — so a linked lane worktree resolves back to `/workspace` and runs gated `main` code rather than its own half-edited files. Both `tillr` and `tillr-drain` resolve sibling `tillr-*` binaries in `dirname(os.Executable())` first and refuse a partial set instead of falling back to `PATH`, and every launcher prints its provenance — tree, digest, reused-or-built — the way yolo-jail prints `Flake source:`.
- **Pack mirror role.** `/workspace` stops depending on `yolo-packs/tillr/files/` at runtime; the mirror exists solely for external workspaces and `yolo host apply` ([OQ-SH2](#OQ-SH2)). The eight launchers yolo already installs under `~/.yolo/bin/launch/` change once, with one final host apply and jail relaunch; subsequent `tools/tillr*` commits need none.
- **Store versions and mid-drain failed builds.** Multi-version store sharing ([tillr-distribution.md](tillr-distribution.md) Step 3) is deferred: one `main` tree feeds every `/workspace` process, so monotone `SchemaVersion` refusal stays intact as a safety check. Each drain tick pins its starting binary cohort (`$cache/set-<digest>/`) across `stage -> run -> gate -> land -> verdict -> state` and reloads only at a quiescent tick boundary (`runs_live == 0`, [OQ-SH1](#61-settled-decisions)), so an additive migration never splits one item's lifecycle. A candidate whose Go or web build fails exits `1` in `tillr-gate` (`just done` scopes `go test ./...` across `tools/tillr*` whenever `tools/tillr` changes) before `tillr land pass` runs, leaving `/workspace/tools` untouched.

| Surface | Single Writer / Authority | Failure Path |
| :--- | :--- | :--- |
| `/workspace/.tillr/state.db` | Coordinator `tillr-*` CLI writers running on `/workspace` (`main`); lanes use scratch stores of their own. | `tillr-view` opens `mode=ro` (`PRAGMA query_only=ON`) and checks file `dev`/`ino` plus `PRAGMA schema_version` per read; missing, swapped, or newer stores return `store-absent` or `store-unreadable`, never cached `present` rows. |
| `/workspace` and `refs/heads/main` | `tillr land pass` inside the loop's serial landing mutex. | Dirty `/workspace` trips `tillr-drain boundary` before dispatch; failed candidate builds exit `1` at `tillr-gate` (`candidate-gate-fail`), record `refs/tillr/failed/<run_id>`, mark the item `blocked`, and leave `main` unmoved. |
| `${XDG_CACHE_HOME:-$HOME/.cache}/tillr/bin/` | Per-PID temp output `<bin>-<digest>.$$` moved atomically (`mv`) before `ln -sfn` flips `current`. | Compiler error or missing `go` deletes temp output, leaves prior cache untouched without silent fallback to stale digests, and exits `1` with `stderr` captured in `.tillr/loop/<run-id>/steps.jsonl`. |
| Lane worktree and `.tillr/loop/<run-id>/` | One leased `run_id` per `lane_id`; immutable per-run stage files. | `ReapExpiredLeases` reclaims timed-out lanes before planning; staging writes once per `run_id` so later ticks never overwrite prior `brief.md` or `fence.txt`. |

## 4. Sequence

The seven queued observatory items move out of ranks `p` through `v` into two waves: truth and parity (`item-obs-cli-counts`, `item-obs-refusal`) land in Step 2 before unattended burning starts, and the five watchability items (`item-obs-now-band`, `item-obs-pause`, `item-obs-archive`, `item-obs-state-glyph`, `item-obs-doc-link`) move to the front of Step 4 because they edit `tools/tillr-view/` (proving self-hosted compilation) while visibly upgrading the board after each tick.

| Step | What It Makes True | Observable Check (CLI + Board) | Cost / Forecloses |
| :--- | :--- | :--- | :--- |
| **1. Hermetic Siblings & Source Launcher** | The launcher slot is a classified pointer (yolo-jail's `deploy`/`deploy-dev` model, including the `other-dev` refusal); one matched bundle per source identity, reused and atomically published; siblings resolve in `dirname(os.Executable())` and a partial set is refused; every launcher prints tree and digest; `snapshot` carries `build_digest`; the mirror rule ([OQ-SH2](#OQ-SH2)) lands. | Copy the tree to a scratch checkout, edit one string in `tools/tillr-view`, and run `tillr view status` there: it prints a new digest and a `built` line, a second call prints `reused` and runs `0` `go` processes, and `BoardHeader` shows the same digest. Then point the slot at *another* checkout and confirm the launcher refuses rather than running it. | One final relaunch to install the launchers; a 1–3 s cold build whenever the hashed set changes; forbids editing `/workspace/tools` dirty while the loop runs. |
| **2. Honest Sensors & CLI Parity** | Fixes `F3` (per-read store check), `F2` (real `ticks`), `F4` (`last_stop_reason` + `detail` from `.tillr/loop/drain-*.json`), `F7` (timeout failure counts), hides spend ([OQ-OBD4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger)), and lands `item-obs-cli-counts` ([OQ-OBD3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger)) + `item-obs-refusal`. | `diff <(tillr view status --store /workspace/.tillr/state.db \| jq -S .payload.counts) <(curl -s http://127.0.0.1:4319/api/v1/snapshot \| jq -S .data.counts)` exits `0`; replacing a copied store flips the board to `RefusalState` (`store-absent`) on the next poll. | Delays feature queue by two `tools/tillr-view` slices before overnight burning. |
| **3. Question-Hold & Queue Re-Rank** | The planner reads open questions from **Vantage's own index** (`vantage-check index --format json`), not from a parser of this repository's; `item-drain-question-hold` (rank `1`) holds items with `open_questions > 0` as `question-open` (`F5`), surfaces distinct blocker reasons (`F6`), and re-ranks the five remaining `item-obs-*` items ahead of rank `0`. | `tillr drain plan --store /workspace/.tillr/state.db` holds the six un-ruled items (`item-tillr-stop-reason`, `item-trace-correlation`, etc.) with `question-open` and lists `item-obs-now-band` first; `QueueRail` shows the new top five. | Six items with open design questions cannot dispatch until ruled in Vantage. |
| **4. Self-Hosted Canary Burn (5 UI Items)** | `tillr drain loop` burns `item-obs-now-band`, `item-obs-pause`, `item-obs-archive`, `item-obs-state-glyph`, and `item-obs-doc-link` ([OQ-OBD1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger), [OQ-OBD2](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger), [OQ-OBD5](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger), [OQ-OBD6](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger)), reloading `tillr-view` per [OQ-SH1](#61-settled-decisions) and [OQ-SH3](#61-settled-decisions). | `tillr view status` and `CountStrip` move `landed` from `2` to `7`; each landed item alters the live board (`NowBand`, `Pause`, spine archive filter, state glyphs, Vantage metadata card). | All five share `tools/tillr-view/`, so they run serially on `lane-1` while `lane-2` takes at most disjoint items. |
| **5. Sustained Self-Hosted Queue Burn** | With all seven [observatory-design.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md) ledger gaps closed, `tillr drain loop --lanes 2` burns `item-pipe-real-registry`, `item-lane-branch-scope`, `item-drain-2a`, `item-or-routing`, and the remaining dispatchable spine, reloading `tillr-drain` between ticks. | `tillr view status` matches `BoardHeader` on `counts`, `ticks`, `dispatched`, `landed`, `rejected`, and `last_stop_reason`; `tillr run lanes` reports `0` leaked leases on stop. | Non-additive schema breaks still require quiescent cutover. |

## 5. What the Owner Sees on the Board

| Step | Region / Component | Healthy Numbers and States | What Silent Breakage Would Look Like |
| :--- | :--- | :--- | :--- |
| **1** | `BoardHeader` | `sources.store.state: present`, `schema_version: 5`, `build_digest` matching current `/workspace/tools`. | `build_digest` stays frozen after a `tools/tillr*` commit lands, exposing `PATH` shadowing or a missed cache invalidation. |
| **2** | `BoardHeader`, `CountStrip`, `RefusalState` | `ticks: 1`, `dispatched: 2`, `landed: 1`, `rejected` / `infra_failures` non-zero for the `1800 s` timeout, `last_stop_reason: queue-drained`, no `$` spend; `CountStrip` matches `tillr view status`. | `ticks: 10`, `last_stop_reason: null` (`ABSENT`), `$0.16` still visible, or `Snapshot:` advancing while a moved store still says `present`. |
| **3** | `QueueRail`, `ItemPane` | Top five rows are `item-obs-now-band` through `item-obs-doc-link`; the six un-ruled items show `hold: question-open` and non-zero `open_questions` in `ItemPane`. | All 32 queued rows wear identical `in_queue` badges with `open_questions: 0` on numbered-question docs, or `item-tillr-stop-reason` dispatches prematurely. |
| **4** | `NowBand`, `BoardHeader`, `QueueRail`, `DocumentPane` | Active run appears in `NowBand` with viewer-clock elapsed; `Pause` freezes updates while keeping selection (`#/item/<id>`); `landed: 7` leaves the spine for the archive filter; `DocumentPane` shows frontmatter, question count, path, and Vantage link. | `landed` climbs in `CountStrip` while `DocumentPane` still renders full Markdown prose or `QueueRail` still lists landed rows (proving `tillr-view` or `web/dist` did not reload). |
| **5** | `NowBand`, `EvidencePane`, `StageStrip` | Up to `2` disjoint runs in `NowBand`; failed attempts move items to `blocked` with stage provenance (`observed`, `derived_clock`, `unobserved_seam`) and `gate.verdict` / `steps.jsonl` `stderr` in `EvidencePane`. | An item sits `working` with `runs_live: 0` (`F9`), or a timed-out run reports `complete: yes` with `rejected: 0`. |

## 6. Decisions and Open Questions

### 6.1 Settled Decisions

| ID | Ruling | Why |
| :--- | :--- | :--- |
| `D-SH1` | `tillr` and `tillr-drain` resolve sibling `tillr-*` binaries in `dirname(os.Executable())` first and fail closed on missing siblings. | Eliminates the 2026-11-13 trap where a source-built `bin/tillr` silently executed `~/.yolo/bin/launch/tillr-item` via `PATH`. |
| `D-SH2` | The pack launcher resolves `$root` from `TILLR_SOURCE_ROOT`, else `<git-main-worktree>/tools`, else `~/.local/share/tillr`. | Runs `/workspace`'s own `main` code without relaunch while shielding linked worker worktrees from executing their own un-gated edits. |
| `D-SH3` | Each tick pins one binary cohort through `verdict` + `state`, and build failures fail closed without updating `current` or falling back to older digests. | Prevents mid-item `SchemaVersion` splits and ensures broken `tools/tillr*` source fails loudly with `stderr` instead of masquerading as the prior build. |
| `D-SH4` | Defer multi-version schema sharing ([tillr-distribution.md](tillr-distribution.md) Step 3); enforce per-read `dev`/`ino` + `PRAGMA schema_version` checks in `tillr-view`. | One tree feeds all `/workspace` binaries, so strict version refusal catches stale servers cleanly instead of risking quiet semantic drift. |
| `D-SH5` | Promote `item-obs-cli-counts` and `item-obs-refusal` (with `F2`, `F3`, `F4`, `F7`) before the loop, and promote the remaining five `item-obs-*` items to the Step 4 canary. | Gives the owner a truthful, feature-complete board before burning the general 32-item spine, while testing self-hosting on `tools/tillr-view/` first. |
| OQ-SH1 | Neither process watches for new code. Each restarts on request: the loop replaces itself in place without waiting for runs, the observatory server between two requests, and this repository files that request after a landing that touched tillr. **Rejected:** restarting whenever the tools directory changes, and pinning until a manual restart. | Decided in [the restart design](tillr-restart.md#decision-ledger) by the owner on 2026-11-15. Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |
| OQ-SH3 | The front end is rebuilt by the roll command, before the Go build, whenever its inputs changed since the running build; no launcher computes a digest and no command-line call pays for a Node build. **Rejected:** a digest inside every launcher, and following the front end only through the dev server. | [The restart design](tillr-restart.md#the-developers-command-in-this-repository) makes one command responsible for turning the trunk into running binaries, so the front end is one more thing it builds. Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |

### 6.2 Open Questions

2. 💬 **[OQ-SH2](#OQ-SH2): What becomes of `just pack-sync` and the `pack-sync` gate check once `/workspace` builds directly from `tools/tillr*`?**

   `just pack-sync` failed `just done` four times on 2026-11-13, and worker lanes fenced to `tools/tillr-<mod>/` cannot write `yolo-packs/tillr/` unless the mirror rule changes.

   What the recipe is for, what it costs today, and which options are still live are in
   [§6.3](#63-what-just-pack-sync-is-for-and-which-options-are-still-live).

   - **Option A — Mechanical per-module mirror in `tillr-loop` and commit recipes:** Before gating, `tillr-loop` syncs `tools/<mod>/` into `yolo-packs/tillr/files/local/share/tillr/<mod>/` and treats that module-disjoint mirror path as implied by `tools/<mod>/` in fence checks. Keeps every `main` commit ready for `yolo host apply` while preserving 2-lane parallelism.
   - **Option B — Demote the mirror to a publish snapshot (remove per-commit `pack-sync` from `just done`):** Keep `yolo pack lint` on pack edits, drop `tools/`-to-pack byte parity from `just done`, and run `just pack-sync` only when exporting pack updates to hosts. Cuts ~10-module mirror churn from git commits, at the cost that `yolo-packs/tillr` on `main` lags `tools/` between syncs.

   <!-- vantage: question id=OQ-SH2 leaning="Option A — mechanical per-module mirror in tillr-loop with module-disjoint implied fence paths, keeping main always host-apply ready with zero human or worker toil." -->

   _Leaning:_ Option A — mechanical per-module sync in `tillr-loop` (plus local commit helper) with module-disjoint implied fence paths, because `yolo-packs/tillr` stays continuously deployable without manual `just pack-sync` failures or shared-path lane serialization.

   **Answer:**
   > _(empty — fill in when decided)_

### 6.3 What `just pack-sync` is for, and which options are still live

Read against the tree on 2026-11-17.

**What it is.** `just pack-sync` is a recipe in [`.justfile`](https://github.com/mschulkind-oss/tillr/blob/main/.justfile). It copies the Go
source of the eleven tillr modules under `tools/` into
`yolo-packs/tillr/files/local/share/tillr/<module>/`, the tree the `tillr` pack ships.
[`tests/pack-sync.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/pack-sync.test.py) fails when a copy differs from its
source, and the gate runs that test as its fast `pack-sync` check
([`tools/gate/checks/pack-sync.json`](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/checks/pack-sync.json)). Since 2026-11-16 a
second recipe, `just pack-docs`, ships the design documents the same way, as a dated snapshot that
is refreshed by hand and not on every landing.

**Why it exists.** A pack ships source and never a compiled artifact
([`yolo-packs/tillr/pack.json`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/pack.json),
[its README](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/README.md)). Each of the eight launchers builds its binary from
the copy, once per source digest. `yolo-packs/` is deployed by `yolo host apply`, not by rcm
([AGENTS.md](https://github.com/mschulkind-oss/tillr/blob/main/AGENTS.md)), and a pack reads real files from the repository, so the copy is
committed. It is what puts `tillr` on PATH in a workspace that has no `tools/`.

**What it costs today.**

- **Every change is made twice.** From 2026-11-10 to 2026-11-17, commits added 167,274 lines under
  `tools/tillr*` and 163,390 under the copy (`git log --numstat` over each path).
- **Every worker must run it.** The build brief tells a lane that touches `tools/tillr*` to "run
  `just pack-sync` in the target and commit the mirror in the same commit"
  ([`tools/tillr-run/backends/brief-template.md`](../../tillr-run/backends/brief-template.md)).
  Before that line and the fence rule below existed, the check failed four times on 2026-11-13,
  and on 2026-11-14 it cost `item-obs-archive`, whose lane ran the recipe and was refused for
  writing outside its fence.
- **No kept run shows it failing.** On 2026-11-17, 101 of the gate reports the drain keeps name
  the check: 61 ran it and all 61 passed, and 40 skipped it because nothing it reads changed. It
  takes about 0.24 s.

**Is it still relevant?** Yes, but the question's premise has moved, and so have its options.

- **The premise is half true.** The drain in this repository runs binaries built from `tools/`
  (`just tillr-build`, and the roll command of [the restart design](tillr-restart.md)), so it
  does not depend on the copy. A plain `tillr` on this jail's PATH still runs the copy: the
  launchers in `yolo-packs/tillr/programs/` build from `~/.local/share/tillr` only, because the
  source launcher of Step 1 (`D-SH2`) is not built.
- **Option A is half built.** The implied fence path exists: the drain adds each named module's
  copy to a lane's fence (`fencePaths` in `tools/tillr-drain/drain/stage.go`, since 2026-11-14),
  and that is what stopped the failures. The mechanical sync inside `tillr-loop` does not exist;
  each lane runs the recipe because its brief says so. What A still offers is moving that step out
  of the worker. It does not remove the doubled diff.
- **Option B is still live, and it is the only option that removes the doubled diff.** Its cost
  is the one it names: between syncs, the pack on `main` lags `tools/`. Another workspace that
  relaunches gets the last published copy, and so does this jail's PATH `tillr`. `just pack-docs`
  already works this way for the documents.
- **Separating tillr's core from its Git-landing use** ([the core design](tillr-core-and-git-landing.md))
  divides definitions inside this repository. It changes nothing here.

**This document as a whole is partly overtaken.** Step 3 is built: the planner holds an item as
`question-open` from Vantage's index (`questionState` in `tools/tillr-drain/drain/plan.go`).
[OQ-SH1](#61-settled-decisions) and [OQ-SH3](#61-settled-decisions) were answered by the restart design, which is accepted and owns the roll
command. The status line, which still says nothing is built, and the item ranks in
[§4](#4-sequence) are out of date. Step 1 (`D-SH1`, `D-SH2`) is not built: sibling binaries still
resolve through PATH. The recommendation is to rule [OQ-SH2](#OQ-SH2) here, then reconcile this
document against the tree before anything is built from it. What is left of Step 1 may belong in
the restart design. Its stage is unchanged.

## 7. Out of Scope

- **Multi-version read/write store sharing** ([tillr-distribution.md](tillr-distribution.md) Step 3): unnecessary while all `/workspace` binaries compile from `main`.
- **Spend and burn-rate UI**: iceboxed on the roadmap per [OQ-OBD4](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger); the store continues recording `usage_cost_usd` only.
- **Browser write or retry actions**: `D-OBS2` in [observatory-design.md](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-design.md#11-decision-ledger) stands; every mutation remains a `tillr-*` CLI face, and `candidate-gate-fail` items stay `blocked` rather than auto-retrying.
- **Ruling the thirteen owner-blocked queue items**: open design questions in [tillr-observatory.md](tillr-observatory.md#10-open-questions) and sibling specs, the Claude-whale counterpart, the host-render row, and seed rows remain held until the owner rules them.