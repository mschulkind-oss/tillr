---
status: in-review
stage: DESIGN
next: "Rule OQ-TD1, OQ-TD2, OQ-TD3 and OQ-TD4 — pack layout, machine config, and whether the pack ships the source launcher that would let an agent build and run tillr without a relaunch (§9)"
---

# Ship `tillr` as one source-built pack with eight `PATH` binaries and a two-skill contract

> **Delivered copy.** This is `docs/design/tillr-distribution.md` from this repository at commit `b3690673a0` (2026-11-16), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-12, when the pack was unbuilt; it is built as of 2026-11-16 ([`yolo-packs/tillr`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/pack.json) ships the source tree and the eight launchers). Then, the nine Go modules under [`tools/`](https://github.com/mschulkind-oss/tillr/blob/main/tools) work and have landed a live candidate on an unattended tick. Evidence verified at `32aed3dc31`.

> **In short.** Distribute `tillr` through a single pack (`yolo-packs/tillr`) following the `../../yolo-packs/merge-queue/pack.json` source-plus-launcher pattern: nine Go modules synced as source, eight `program` entries on `PATH`, a three-tier documentation split where `--help` alone owns flags, and an official pack skill that hands the unfinished toolset to AutoQA for design review.

**Why it matters.** The eight binaries already work together, but until they ship through a pack with self-documenting boundaries and an explicit known-gaps banner, neither daily agentic development nor an external AutoQA agent can invoke or critique them through standard workspace tooling.

**The shape.** One `files` tree (`~/.local/share/tillr/`), eight `program` launchers on `PATH` (`tillr` plus the seven `tillr-<noun>` binaries) backed by a digest-keyed build cache (`~/.cache/tillr/bin/`), per-module `README` files for standalone judgment, and two pack skills (`tillr` for the seven-step loop, `tillr-autoqa` for the AutoQA design-comment handoff).

**Cost.** Syncing nine Go modules into [`yolo-packs/`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs) duplicates source under `just pack-sync` (~18 files/module, `ESTIMATE`), and declaring eight `program` entries adds eight launchers to `~/.local/bin/`.

**Start at [§1](#1-one-pack-eight-programs-and-what-path-sees)** — the eight-binary `PATH` contract drives the build launcher, the documentation split, and the AutoQA handoff.

**Needs your ruling:** None. All four questions were answered by what was built; each says so in place.

---

## 1. One pack, eight programs, and what `PATH` sees

**Verdict:** Ship one pack at `yolo-packs/tillr` named `tillr`, declaring **eight `program` contributions** (the umbrella dispatcher `tillr` plus the seven standalone `tillr-<noun>` binaries), **one `files` contribution**, and **one `skills` contribution**. Reject a single `tillr` launcher with internal subcommands.

Measured `--help` / no-arg first-line output across the nine modules in [`tools/`](https://github.com/mschulkind-oss/tillr/blob/main/tools) on 2026-11-12 at `32aed3dc31`:

| Module directory | Binary on `PATH` | Measured first-line output (2026-11-12) |
| :--- | :--- | :--- |
| [`tools/tillr`](../../tillr) | *(none — library)* | Record, store, event, and conformance library imported by the eight binaries |
| [`tools/tillr-cli`](../../tillr-cli) | `tillr` | `tillr: a noun is required; name one of: store, item, land, drain, run, gate, view` |
| [`tools/tillr-drain`](../../tillr-drain) | `tillr-drain` | `a face is required: plan, once, boundary or loop` |
| [`tools/tillr-gate`](../../tillr-gate) | `tillr-gate` | `tillr-gate: name a face: dualrun, plan, run` |
| [`tools/tillr-item`](../../tillr-item) | `tillr-item` | `tillr-item: a face is required: apply, seed, show, decision, mail, grant or verdict` |
| [`tools/tillr-land`](../../tillr-land) | `tillr-land` | `tillr-land: a face is required: enqueue, check, pass` |
| [`tools/tillr-run`](../../tillr-run) | `tillr-run` | `tillr-run: a face is required: exec, lanes, status or release` |
| [`tools/tillr-store`](../../tillr-store) | `tillr-store` | `tillr-store: a face is required: init, seed, check or events` |
| [`tools/tillr-view`](../../tillr-view) | `tillr-view` | `tillr-view: a face is required: status or events` |

The table is the 2026-11-12 measurement, not today's command enumeration. As of
2026-11-15, `tillr-store` also provides `recover` for an interrupted rollback-mode
writer; [its README](../../tillr-store/README.md#recovering-an-interrupted-writer)
states the recovery contract, and the binary's `--help` owns the current face list.

Three constraints force eight `program` declarations rather than a single `tillr` entry:

1. **The dispatcher enforces sibling presence on `PATH`.** Measured behavior of [`tools/tillr-cli`](../../tillr-cli): it refuses to run unless all seven `tillr-<noun>` binaries are on `PATH` (`"tillr-drain is not on PATH; the seven binaries install together"`).
2. **`yolo` owns `~/.local/bin` by declaration.** As recorded in `../../yolo-packs/merge-queue/pack.json`, `yolo` refuses a `files` contribution into `~/.local/bin`; every executable on `PATH` must be a `program` contribution (`via: "pack"`).
3. **Standalone composition requires direct binary names.** An external agent (such as AutoQA) picks individual tools apart (`tillr-gate`, `tillr-view`, `tillr-item`) without routing through the full drain loop.

**What an operator's `PATH` sees:** eight executables in `~/.local/bin/`: `tillr`, `tillr-drain`, `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-run`, `tillr-store`, and `tillr-view`.

---

## 2. Build and delivery

**Verdict:** Use the **source-plus-launcher** pattern from `../../yolo-packs/merge-queue/pack.json` and `../../yolo-packs/merge-queue/programs/mq/mq`. Reject `program` with `build`/`produces`.

- **Source delivery (`files`):** `just pack-sync` copies all nine module directories ([`tools/tillr`](../../tillr) through [`tools/tillr-view`](../../tillr-view)) into the pack's `files/local/share/tillr/<module>/`, which `yolo` renders to `~/.local/share/tillr/<module>/`. Guarded against drift by [`../../tests/pack-sync.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/pack-sync.test.py). Including [`tools/tillr`](../../tillr) is mandatory because the eight binary modules resolve the shared library via relative `replace` paths in `go.mod`.
- **Launcher mechanics (`program`):** All eight `program` contributions invoke the same launcher logic, dispatching on `${0##*/}` (`tillr` maps to module `tillr-cli`; `tillr-<noun>` maps to module `tillr-<noun>`). The launcher never changes its own working directory (`go build` runs in a subshell `(cd "$root/$mod" && ...)`) and ends with `exec "$bin" "$@"`, so repository-relative `.tillr` state discovery works from the caller's directory.
- **Warm cache fast path:** Keyed per binary at `${XDG_CACHE_HOME:-$HOME/.cache}/tillr/bin/current-<bin>-<root-hash>`. On each call, the launcher checks whether any non-`*_test.go` `.go`, `go.mod`, or `go.sum` file in either `$root/$mod` or the shared library `$root/tillr` is newer (`-nt`) than the `current` symlink. When nothing is newer, it `exec`s `$current` immediately with zero `go` invocations (see [OQ-TD3](#open-questions) for single-binary versus batch cold builds).
- **Mid-session source digest change:** When `just pack-sync` or a host pack render updates `~/.local/share/tillr/` mid-session, a source file in `$root/$mod` or `$root/tillr` becomes newer than `$current`. The next invocation misses the fast path, hashes all non-test `.go`, `go.mod`, and `go.sum` files across `$root/$mod` and `$root/tillr` into `<digest>`, builds `$cache/<bin>-<digest>.$$` with `CGO_ENABLED=0`, atomically moves it to `$cache/<bin>-<digest>` (or drops the temp file if a concurrent caller won the race), flips `ln -sfn "$bin" "$current"`, and `exec`s it. In-flight processes keep their open file descriptor; the very next command runs the new build without a jail restart.
- **Machine with no Go toolchain:**
  - *Warm cache (`$cache/<bin>-<digest>` exists and is executable):* Runs normally via `exec`; `go` is never checked.
  - *Cold or invalidated cache (`$bin` missing) and `command -v go` absent:* Fails immediately (`exit 1`) on stderr with:
    ```text
    <bin>: no cached binary for <digest> and no go toolchain to build it
    ```
    Because building happens in the `via: "pack"` runtime launcher rather than at container launch (`build`/`produces`), a machine without Go still launches its jail cleanly and only fails if a caller invokes an uncached `tillr*` binary.

---

## 3. Documentation split and the honesty rule

Every fact about `tillr` has one home across three layers:

| Layer | Location | Owns | Must never contain |
| :--- | :--- | :--- | :--- |
| **1. CLI surface** | `<bin> --help`, `<bin> <face> --help` | Nouns, faces, flags, defaults, argument shapes, exit codes | Multi-binary workflow prose |
| **2. Component judgment** | One `README.md` per module under [`tools/`](https://github.com/mschulkind-oss/tillr/blob/main/tools) (shipped to `~/.local/share/tillr/<mod>/`) | What the module owns, state invariants, standalone invocation for external callers (AutoQA), refusal conditions, **Known Gaps** | Flag tables, copied `--help` blocks, full loop orchestration |
| **3. Loop composition** | Pack skill `tillr` (`~/.<agent>/skills/tillr/SKILL.md`) | How the eight binaries compose across the seven-step loop ([§4](#4-the-composition-skill-headings-and-commands)), operator stop conditions, **Known Gaps** | Per-flag reference tables or internal Go package layout |

### The rule that prevents disagreement (One-Home Rule + Mechanical Gate)

1. **Prose cites `<bin> <face> --help` instead of listing flags.** Neither per-module `README.md` files nor the composition skill may state flag names beyond the bare `<bin> <face>` verb needed to show control flow.
2. **Mechanical `--help` conformance check.** A repository test extracts every `tillr` and `tillr-<noun>` invocation from fenced blocks in the module `README.md` files and pack skills, runs the live binary's `--help`, and fails if a documented `<noun>` or `<face>` does not exist or if any Markdown file contains a flag table (`| Flag |` or `--` option list).

### How a reader knows which is current — and what is unfinished

- **The binary on `PATH` wins on surface syntax** because the launcher compiles it directly from the current source digest.
- **Every `README.md` and skill opens with a stamped Known Gaps callout** naming the verification date, commit, and the three live gaps an operator or AutoQA agent will hit today:

```markdown
> [!WARNING]
> **Known Gaps (verified 2026-11-12 at `32aed3dc31`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **Item-state moves just landed** — queue item-state transitions are newly wired and have minimal production mileage.
> 2. **Two-item seed** — the seed currently holds only two dispatchable items.
> 3. **Recording emitters unbuilt** — the recording/event schema exists in `tools/tillr`, but the binaries' event emitters are not yet wired (`events` faces see only what is currently emitted).
```

---

## 4. The composition skill: headings and commands

The `tillr` skill's loop body uses these exact seven section headings, in order (`plan → stage → run → judge → land → verify → audit`), each pairing one step with one binary noun:

```markdown
## 1. Plan — preview dispatchable work
tillr drain plan

## 2. Stage — seed and apply item state
tillr item seed

## 3. Run — execute a candidate in an isolated lane
tillr run exec

## 4. Judge — evaluate the candidate against the gate
tillr gate run

## 5. Land — enqueue the passing candidate for trunk merge
tillr land enqueue

## 6. Verify — check store and invariant integrity
tillr store check

## 7. Audit — inspect recorded status and events
tillr view events
```

---

## 5. The AutoQA handoff through the pack

**Mechanism:** Ship the handoff as a **pack skill** (`tillr-autoqa` under `yolo-packs/tillr`'s `{"kind": "skills"}` contribution; see [OQ-TD2](#open-questions)).

- Why not a `briefing` contribution: a briefing is injected into every agent turn in every workspace that selects the pack, spending context on a one-time design-review prompt.
- Why not a side channel: `{"kind": "skills"}` is a standard `yolo` pack primitive; any workspace that includes `yolo-packs/tillr` receives the skill automatically via `yolo host apply --assert`, and the coordinator or human invokes it by naming the `tillr-autoqa` skill.

### Verbatim text delivered to the AutoQA agent

```markdown
---
name: tillr-autoqa
description: Handoff brief for the AutoQA agent to inspect the tillr toolset and return design comments on standalone QA composition.
---

# AutoQA Review Handoff — `tillr` Component Seams

You are reviewing `tillr` from the AutoQA perspective. **Your deliverable is design comments only — do not edit source, do not write tests, and do not commit.**

## Current State (Not Finished)

A live unattended tick has landed a real candidate through these eight binaries, but the toolset is intentionally shipped early with three known gaps:
1. **Queue item-state moves just landed** and have minimal operational mileage.
2. **The seed holds only two dispatchable items.**
3. **The recording layer's emitters (`tools/tillr`) are unbuilt** — the library types exist, but the binaries do not yet emit the full event stream.

## How to Inspect the Pieces

All eight binaries are on your `PATH`, and their source + per-module `README` files are installed at `~/.local/share/tillr/`:
- Umbrella dispatcher: `tillr --help` (`store`, `item`, `land`, `drain`, `run`, `gate`, `view`)
- Gate execution & comparison: `tillr-gate --help` (`dualrun`, `plan`, `run`) and `~/.local/share/tillr/tillr-gate/README.md`
- Lane execution & lifecycle: `tillr-run --help` (`exec`, `lanes`, `status`, `release`) and `~/.local/share/tillr/tillr-run/README.md`
- Item verdicts & grants: `tillr-item --help` (`apply`, `seed`, `show`, `decision`, `mail`, `grant`, `verdict`) and `~/.local/share/tillr/tillr-item/README.md`
- Store & read views: `tillr-store --help` (`init`, `seed`, `check`, `events`) and `tillr-view --help` (`status`, `events`)
- Loop orchestration reference: read the `tillr` skill (`SKILL.md`).

## What to Return

Inspect `--help` and the `README` files above, then return structured design comments answering:
1. **Standalone invocation:** Can AutoQA drive `tillr-gate` (`plan`, `run`, `dualrun`) and `tillr-run` (`exec`, `status`, `release`) independently without running `tillr-drain`? Which required flags or state preconditions couple them to the drain loop today?
2. **Verdict & decision payloads:** What input/output schema does AutoQA need from `tillr-item verdict` and `tillr-item decision` to post a QA pass/fail or request human review?
3. **Missing event emitters:** Before the `tools/tillr` recording emitters are built, which exact events, fields, and correlation IDs must `tillr-gate`, `tillr-run`, and `tillr-land` emit so AutoQA can reconstruct a run from `tillr-view events`?
4. **Refusal & exit contracts:** Where do the current binaries lack machine-readable output or distinct exit codes that an unattended QA harness requires?
```

---

## 6. Machine scoping

- **Which `packs` lists gain the entry:** Add `"yolo-packs/tillr"` to the shared `packs` array in [`config/yolo-jail/config.toml`](https://github.com/mschulkind-oss/tillr/blob/main/config/yolo-jail/config.toml) immediately after `"yolo-packs/matt-agentwork"` (and before `"yolo-packs/seat-profiles"`), so every machine on both `tag-personal` (two machines) and `tag-work` (two machines) receives the pack (see [OQ-TD1](#open-questions)). Because array keys in `config.d/*.toml` replace rather than append, if [`tag-personal/config/yolo-jail/config.d/personal.toml`](https://github.com/mschulkind-oss/tillr/blob/main/tag-personal/config/yolo-jail/config.d/personal.toml) or [`tag-work/config/yolo-jail/config.d/work.toml`](https://github.com/mschulkind-oss/tillr/blob/main/tag-work/config/yolo-jail/config.d/work.toml) redeclares `packs`, restate `"yolo-packs/tillr"` there too.
- **What a work machine gets that a personal one does not:** From `yolo-packs/tillr` itself, **nothing differs** — the pack ships identical Go source, launchers, and skills everywhere, and carries zero provider or account configuration. The difference comes entirely from the host's rcm tag layer: a `tag-work` machine layers [`tag-work/config/yolo-jail/config.d/work.toml`](https://github.com/mschulkind-oss/tillr/blob/main/tag-work/config/yolo-jail/config.d/work.toml) (`bedrock` and `aws-auth` packs, AWS Bedrock profiles, and no `agy` pack), whereas a `tag-personal` machine layers [`tag-personal/config/yolo-jail/config.d/personal.toml`](https://github.com/mschulkind-oss/tillr/blob/main/tag-personal/config/yolo-jail/config.d/personal.toml) (personal provider packs including `agy` and `codex`). Because `tillr` delegates model execution to the workspace's active agent environment rather than naming a provider or `agy`, `yolo-packs/tillr` is tag-agnostic.

---

## 7. Order of work and first three actions

Each step is independently landable and leaves `yolo pack lint` green:

1. **Step 1 (`ESTIMATE`: 1h) — Pack manifest, digest launcher, and source sync.** Create `yolo-packs/tillr` with its manifest and shared launcher script, wire `just pack-sync` and [`../../tests/pack-sync.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/pack-sync.test.py) to sync the nine [`tools/`](https://github.com/mschulkind-oss/tillr/blob/main/tools) modules into `yolo-packs/tillr/files/local/share/tillr/`, and pass `yolo pack lint yolo-packs/tillr`.
2. **Step 2 (`ESTIMATE`: 1h) — Module `README`s and `--help` conformance test.** Add a concise `README` with the Known Gaps banner to each of the nine [`tools/`](https://github.com/mschulkind-oss/tillr/blob/main/tools) modules, re-run `just pack-sync`, and land the test checking documented `<noun> <face>` commands against live `--help`.
3. **Step 3 (`ESTIMATE`: 45m) — Composition skill (`tillr`) and AutoQA handoff skill (`tillr-autoqa`).** Add both skills under `yolo-packs/tillr/skills/` and verify with `yolo pack lint yolo-packs/tillr`.
4. **Step 4 (`ESTIMATE`: 30m) — Machine selection and AutoQA dispatch.** Add `"yolo-packs/tillr"` to [`config/yolo-jail/config.toml`](https://github.com/mschulkind-oss/tillr/blob/main/config/yolo-jail/config.toml), have the human run `yolo host apply --assert` on the host and relaunch the jail, and dispatch AutoQA against `tillr-autoqa`.

**First three actions:**
1. Author the `yolo-packs/tillr` manifest declaring the `.local/share/tillr` `files` tree, the eight `program` entries (`tillr` and `tillr-{drain,gate,item,land,run,store,view}`), and `{"kind": "skills"}`.
2. Write the digest-caching launcher script that hashes `$root/$mod` plus `$root/tillr` and preserves the caller's working directory across `exec`.
3. Extend `just pack-sync` and [`../../tests/pack-sync.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/pack-sync.test.py) to mirror all nine `tools/tillr*` directories and run `yolo pack lint yolo-packs/tillr`.

---

## 8. What makes this fail — and what prevents each

| Failure mode | What in this design prevents it |
| :--- | :--- |
| **Docs drifting from `--help`** | Flags are banned from `README` and skill files ([§3](#3-documentation-split-and-the-honesty-rule)); a gate test checks every fenced `tillr* <face>` call against live `--help` output and rejects Markdown flag tables. |
| **Launcher breaks without a Go toolchain** | Warm cache uses only POSIX `[ -x ]` and `-nt` file stats with zero `go` calls; cold cache without `go` exits `1` naming the missing digest and toolchain ([§2](#2-build-and-delivery)), and `via: "pack"` never runs at jail startup. |
| **Pack teaches other repos to depend on a half-working tool** | `tillr` binaries refuse to operate in any repo lacking an explicit `.tillr/` store (`tillr store init`); no `briefing` or `config-overlay` auto-runs `tillr`; the skill's **Known Gaps** callout explicitly forbids wiring `tillr` into another repo's gate while emitters are unbuilt. |
| **AutoQA handoff reads as finished** | The `tillr-autoqa` skill ([§5](#5-the-autoqa-handoff-through-the-pack)) opens with the three unbuilt/newly-landed gaps and scopes AutoQA strictly to returning design comments with zero code edits. |
| **Edit to shared [`tools/tillr`](../../tillr) library leaves stale binaries cached** | Every binary's fast-path `stale()` check and SHA-256 digest walk both `$root/$mod` and `$root/tillr` ([§2](#2-build-and-delivery)), so a library change invalidates all eight cached binaries on the next call. |

---

## 9. The agent's `tillr` must not wait for a launch

**Requirement (owner, 2026-11-13).** An agent working in this repository must be able to build the
current [`tools/tillr*`](https://github.com/mschulkind-oss/tillr/blob/main/tools) and run it against a store **without the owner relaunching
anything**. Today it cannot, and the obstacle is delivery rather than the toolchain.

**What already works.** Every module builds from the tree, and a scratch store at any version is
readable by the binary that created it. The eight programs on `PATH` are the pack's `program`
launchers, and each one is a **source launcher**: it builds its module from `~/.local/share/tillr`
on the first call after the source changes, keys that build by a digest of the tree, and execs the
cached binary on a warm call with no `go` invocation. That is [§2](#2-build-and-delivery)'s
mechanism, and it ships.

What the launcher's source root is not is the working tree. `~/.local/share/tillr` is the pack's
**rendered** tree, written by yolo at launch: `just pack-sync` rewrites only the repository's
`yolo-packs/tillr/files/` copy, and a host render (`yolo host apply`) is what puts it in the
launch's home. A change to `tools/tillr*` therefore reaches the launcher's source root only after
the pack is mirrored and rendered, and a loop that is already running keeps the binary it loaded.
Two consequences were measured on 2026-11-13:

- A source-built `tillr-item` accepted a `rank` field that the launched binary refused
  (`tillr-item: seed entry 4: json: unknown field "rank"`), so the new queue could not be seeded into
  the live store at all.
- A source-built binary that opens the live store **migrates** it, after which the launched binaries
  refuse it — the same class as the board store that read `store schema version 11 is newer than this
  board (10)`. Building forward is therefore a one-way door for that store, which is why the queue
  landed in the seed file tonight and not in the database.

**The in-launch handoff is a `PATH` prepend, not a launcher change.** `just tillr-build` builds the
eight programs from the working tree into `$YOLO_DURABLE_DIR/bin`; prepend that directory to `PATH`
for the loop and its children, and the whole cohort comes from the tree:

```sh
just tillr-build
PATH="$YOLO_DURABLE_DIR/bin:$PATH" tillr drain loop ...
```

The eight must be built together, because `tillr` resolves `tillr-<noun>` through `PATH` and
`ProcessSteps` resolves `tillr-run` and `tillr-item` through `PATH`; a directory holding only
`tillr` would hand the loop the launched siblings. The loop's first line and every `drain-*.json`
result it writes name the build it is running, revision and digest, so a fix is never called closed
from a tree the process never loaded.

**The remaining steps, in the order worth taking them:**

1. **Build and prepend, and use scratch stores by default.** `just tillr-build` builds the eight
   programs into `$YOLO_DURABLE_DIR/bin`; prepend that directory to `PATH` for the loop and its
   children. Every experiment runs against a store copied from the live one or seeded fresh: a
   source-built binary that opens the live store migrates it forward, and the launched binaries then
   refuse it.
2. **A schema-compatibility policy, only if two versions must share one store.** `SchemaVersion` would
   become a floor plus the capabilities `addMissingColumns` already guarantees, and a binary would
   refuse only what it cannot read. It is the riskiest step and the one to rule on last, because it
   trades a loud refusal for a quiet semantic mismatch.

## Open Questions

1. ✅ **OQ-TD1: Should `yolo-packs/tillr` sit in user-scope configuration for every workspace now, or stay opt-in per workspace?**

   An operator opens a second repository or an AutoQA workspace and runs `tillr view status`. Today [`config/yolo-jail/config.toml`](https://github.com/mschulkind-oss/tillr/blob/main/config/yolo-jail/config.toml) includes [`yolo-packs/matt-agentwork`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/matt-agentwork) globally while `../../yolo-packs/merge-queue` is selected per workspace. We must choose whether every workspace gets the eight `tillr` binaries on `PATH` by default.

   - **Option A — User-scope in [`config/yolo-jail/config.toml`](https://github.com/mschulkind-oss/tillr/blob/main/config/yolo-jail/config.toml):** Every workspace gets the binaries and skills immediately; uninitialized repos remain unaffected because commands refuse without `.tillr/`.
   - **Option B — Workspace opt-in only:** Only repos whose workspace config lists `yolo-packs/tillr` get the binaries; running AutoQA elsewhere requires editing that repo's config first.

   _Settled:_ option A, by what was built: `config/yolo-jail/config.toml` lists `yolo-packs/tillr` at user scope.

   _Leaning:_ Option A — add `yolo-packs/tillr` to user-scope [`config/yolo-jail/config.toml`](https://github.com/mschulkind-oss/tillr/blob/main/config/yolo-jail/config.toml) so operators and AutoQA have the binaries and skills in every workspace, guarded by explicit `.tillr/` initialization.

   **Answer:**
   > _(empty — fill in when decided)_

2. ✅ **OQ-TD2: Should the AutoQA handoff live in its own `tillr-autoqa` skill or as a section inside the main `tillr` skill?**

   The owner launches the AutoQA agent in a workspace that includes `yolo-packs/tillr` and asks for design comments on missing QA seams. The pack delivers skills via `{"kind": "skills"}`. That leaves whether the review brief is a second skill directory or a section of the main loop skill.

   - **Option A — Separate `tillr-autoqa` skill in the same pack:** Ordinary loop sessions loading `tillr` never pay tokens for the one-off QA review prompt, and retiring the handoff deletes one directory.
   - **Option B — Trailing section inside the `tillr` skill:** One file to maintain, but every agent loading `tillr` reads the AutoQA review prompt on every session.

   _Settled:_ superseded by `D-AQ1` and `D-AQ7` in [the AutoQA design](tillr-autoqa.md); the pack ships one skill.

   _Leaning:_ Option A — ship `tillr-autoqa` as a separate skill inside `yolo-packs/tillr` so the design-review prompt stays out of daily operator turns and deletes cleanly once comments land.

   **Answer:**
   > _(empty — fill in when decided)_

3. ✅ **OQ-TD3: On a cold or invalidated cache, should the launcher compile only the invoked binary or all eight binaries at once?**

   After `just pack-sync` updates [`tools/tillr`](../../tillr), an operator runs `tillr view status`, which invokes `tillr` (`tillr-cli`) and then `tillr-view`. The launcher in `~/.local/bin/` detects that `$root/tillr` is newer than its `current` symlink and must run `go build`.

   - **Option A — Lazy per-binary build:** Each launcher builds only its own module (~1–2s per binary called, `ESTIMATE`), leaving uncalled binaries until needed.
   - **Option B — Batch build all eight on any miss:** First call after a source change pays ~6–8s (`ESTIMATE`) once and warms all eight binaries together.

   _Settled:_ option A, by what was built: the pack launcher builds one binary per source digest (commit `3004ceffe`).

   _Leaning:_ Option A — build lazily per invoked binary, matching `../../yolo-packs/merge-queue/programs/mq/mq` and keeping single-command inspection fast after edits.

   **Answer:**
   > _(empty — fill in when decided)_

4. ✅ **OQ-TD4: Does the pack ship the digest-caching source launcher, or keep the launch-time build?**

   [§9](#9-the-agents-tillr-must-not-wait-for-a-launch) states the requirement; this is the choice that
   meets it. The launch-time build is what runs today: a change to `tools/tillr*` needs
   `just pack-sync` and then a relaunch before `PATH` sees it, and a source-built binary cannot touch
   the live store without migrating it past the launched binaries.

   - **A — Ship the [source launcher](#2-build-and-delivery) for the eight programs.** The binary on `PATH` is built from
     the tree on first call after a change, cached by digest, and rebuilt with zero `go` calls when
     nothing is newer. No relaunch, one version at a time, and the cost is a cold build inside a call
     ([`OQ-TD3`](#open-questions)'s single-versus-batch question).
   - **B — Keep the launch-time build, and require `just tillr-build` plus scratch stores for agent
     work.** No delivery change; the live store stays the launched binaries' alone, and an agent that
     wants a new feature in the live store waits for a launch.

   _Settled:_ option A, by what was built: the digest-caching launcher shipped in commit `3004ceffe`.

   _Leaning:_ **A.** The launcher is already designed; the only reason it is not what ships is that
   the launch-time build landed first.

   **Answer:**
   > _(empty — fill in when decided)_
