---
title: "Distribution: one source-built pack with eleven PATH binaries"
status: accepted
stage: CURRENT
verified: 2026-11-25
covers:
  - yolo-packs/tillr/pack.json
  - yolo-packs/tillr/skills/
  - tools/tillr/packsync/
  - tools/tillr/tillrdocs/
tags: [tillr, pack, distribution, documentation, reference]
summary: "tillr is distributed through one pack in a source-plus-launcher pattern: the module source is mirrored into the pack, eleven program entries on PATH each build and cache one binary from that source, and documentation is split into three layers where --help alone owns flags."
---

# Distribution: one source-built pack with eleven `PATH` binaries

**Status:** Verified 2026-11-25 against the tree. UNMEASURED here: the checks that hold it true are [`tools/tillr/packsync`](../../tools/tillr/packsync) (the mirror and the launchers) and [`tools/tillr/tillrdocs`](../../tools/tillr/tillrdocs) (the documentation split).

`tillr` is distributed through one pack, [`yolo-packs/tillr`](../../yolo-packs/tillr/pack.json), in a **source-plus-launcher** pattern: the module source is mirrored into the pack, eleven `program` entries on `PATH` each build and cache one binary from that source, and documentation is split into three layers where `--help` alone owns flags.

| Component | Lives in |
| :--- | :--- |
| Pack declaration: eleven `program` launchers, one `files` tree, the skills | [`yolo-packs/tillr`](../../yolo-packs/tillr/pack.json) |
| Module source and its mirror | [`tools/`](../../tools), copied by `just pack-sync` to `yolo-packs/tillr/files/` |
| Mirror and launcher check | [`tools/tillr/packsync`](../../tools/tillr/packsync) |
| Documentation split check | [`tools/tillr/tillrdocs`](../../tools/tillr/tillrdocs) |
| Loop composition skill | [`tillr-operating`](../../yolo-packs/tillr/skills/tillr-operating/SKILL.md) |

**Cost.** The module source exists twice: under [`tools/`](../../tools) and, mirrored by `just pack-sync`, under [`yolo-packs/`](../../yolo-packs). Eleven `program` entries put eleven launchers in `~/.local/bin/`.

---

## Pack programs on `PATH`

One pack at `yolo-packs/tillr` named `tillr` declares **eleven `program` contributions** (the umbrella dispatcher `tillr`, nine standalone `tillr-<noun>` binaries, and the `tillr-loop` Git-landing hook program), **one `files` contribution**, and **one `skills` contribution**.

| Module directory | Binary on `PATH` |
| :--- | :--- |
| [`tools/tillr`](../../tools/tillr) | *(none: the record, store and event library the binaries import)* |
| [`tools/tillr-cli`](../../tools/tillr-cli) | `tillr` |
| [`tools/tillr-drain`](../../tools/tillr-drain) | `tillr-drain` |
| [`tools/tillr-gate`](../../tools/tillr-gate) | `tillr-gate` |
| [`tools/tillr-item`](../../tools/tillr-item) | `tillr-item` |
| [`tools/tillr-land`](../../tools/tillr-land) | `tillr-land` |
| [`tools/tillr-loop`](../../tools/tillr-loop) | `tillr-loop` |
| [`tools/tillr-roll`](../../tools/tillr-roll) | *(none: source-only rolling-release helper)* |
| [`tools/tillr-run`](../../tools/tillr-run) | `tillr-run` |
| [`tools/tillr-store`](../../tools/tillr-store) | `tillr-store` |
| [`tools/tillr-trace`](../../tools/tillr-trace) | `tillr-trace` |
| [`tools/tillr-view`](../../tools/tillr-view) | `tillr-view` |
| [`tools/tillr-watch`](../../tools/tillr-watch) | `tillr-watch` |

Each binary's `--help` owns its face list; this document does not enumerate faces.

Three constraints require separate `program` declarations rather than a single `tillr` entry:

1. **The dispatcher resolves its siblings through `PATH`.** [`tools/tillr-cli`](../../tools/tillr-cli) refuses a noun whose `tillr-<noun>` binary is not on `PATH`.
2. **`yolo` owns `~/.local/bin` by declaration.** `yolo` refuses a `files` contribution into `~/.local/bin`; every executable on `PATH` is a `program` contribution (`via: "pack"`).
3. **Standalone composition requires direct binary names.** An external agent composes individual tools (`tillr-gate`, `tillr-view`, `tillr-item`) without routing through the drain loop.

---

## Build and delivery

The pack delivers source as `files`, and each `program` is a launcher that builds and caches its binary.

- **Source delivery (`files`):** `just pack-sync` copies the module directories named in `Modules` of [`tools/tillr/packsync/packsync.go`](../../tools/tillr/packsync/packsync.go) into the pack's `files/local/share/tillr/<module>/`, which `yolo` renders to `~/.local/share/tillr/<module>/`. That test guards the copy against drift. [`tools/tillr`](../../tools/tillr) is part of the tree because the binary modules resolve the shared library through relative `replace` paths in `go.mod`.
- **Launcher mechanics (`program`):** The launchers share one body that differs in two assignments: the binary's name and its module (`tillr` builds from `tillr-cli`; `tillr-<noun>` builds from the module of the same name). The launcher never changes its own working directory (`go build` runs in a subshell) and ends with `exec`, so a store named relative to the caller's directory resolves in the caller's repository.
- **Warm cache fast path:** A `current-<bin>-<root-hash>` symlink under `${XDG_CACHE_HOME:-$HOME/.cache}/tillr/bin/` names the last build. On each call the launcher checks whether any non-test `.go`, `go.mod` or `go.sum` file anywhere under the source root is newer than the symlink. When nothing is newer it `exec`s the cached binary with no `go` invocation.
- **A source change mid-session:** When a file under the source root becomes newer, the next call hashes every non-test `.go`, `go.mod` and `go.sum` file of the whole tree into a digest, builds `<bin>-<digest>` with `CGO_ENABLED=0` to a temporary name, moves it into place (or drops it when a concurrent caller won the race), repoints the symlink, and `exec`s it. In-flight processes keep the binary they loaded.
- **A machine with no Go toolchain:**
  - *Warm cache:* runs normally; `go` is never looked for.
  - *Cold or invalidated cache:* fails with exit `1` and this line on stderr:
    ```text
    <bin>: no cached binary for <digest> and no go toolchain to build it
    ```
    Because the build happens in the launcher rather than at container launch, a machine without Go still launches cleanly and fails only when a caller invokes an uncached binary.

---

## Documentation split and the honesty rule

Every fact about `tillr` has one home across three layers:

| Layer | Location | Owns | Must never contain |
| :--- | :--- | :--- | :--- |
| **1. CLI surface** | `<bin> --help`, `<bin> <face> --help` | Nouns, faces, flags, defaults, argument shapes, exit codes | Multi-binary workflow prose |
| **2. Component judgment** | One `README.md` per module under [`tools/`](../../tools) (shipped to `~/.local/share/tillr/<mod>/`) | What the module owns, state invariants, standalone invocation for external callers, refusal conditions, **Known Gaps** | Flag tables, copied `--help` blocks, full loop orchestration |
| **3. Loop composition** | The pack skill [`tillr-operating`](../../yolo-packs/tillr/skills/tillr-operating/SKILL.md) | How the eight binaries compose across the seven-step loop ([the composition skill](#the-composition-skill-headings-and-commands)) and the operator's stop conditions | Per-flag reference tables or internal Go package layout |

### The rule that prevents disagreement (One-Home Rule + Mechanical Gate)

1. **Prose cites `<bin> <face> --help` instead of listing flags.** Neither a module `README.md` nor the composition skill states flag names beyond the bare `<bin> <face>` verb needed to show control flow.
2. **Mechanical `--help` conformance check.** [`tools/tillr/tillrdocs`](../../tools/tillr/tillrdocs) extracts every `tillr` and `tillr-<noun>` invocation from the module `README.md` files and the skill, runs the binary built from the tree with `--help`, and fails when a documented noun or face does not exist or when a document carries a flag table (a table whose header names a flag, or an option-list line).

### How a reader knows which is current — and what is unfinished

- **The binary on `PATH` wins on surface syntax** because the launcher compiles it from the current source digest.
- **Every module `README.md` opens with a Known gaps callout** listing the gaps a caller will meet. The same test fails a README without it.

---

## The composition skill: headings and commands

The `tillr-operating` skill's loop body has seven sections, in order (`plan → stage → run → judge → land → verify → audit`), each pairing one step with one binary noun:

```markdown
## 1. Plan — preview dispatchable work
## 2. Stage — sync documents or file store-declared work
## 3. Run — execute a candidate in an isolated lane
## 4. Judge — evaluate the candidate against the gate
## 5. Land — squash the passing candidate onto the trunk
## 6. Verify — check store and invariant integrity
## 7. Audit — inspect recorded status and events
```

The skill names one command per step and which exit code means what; every flag is learned from the binary.

---

## The AutoQA handoff through the pack

The pack ships no AutoQA skill and no briefing. [The AutoQA design](../design/tillr-autoqa.md) rules it (`D-AQ1` and `D-AQ7` there): the tillr pack ships its own skills only, and AutoQA ships in a pack of its own.

Two rules stand:

- **Not a `briefing` contribution.** A briefing is injected into every agent turn in every workspace that selects the pack, which spends context on a one-time review prompt and tells an agent sent to build something to review instead.
- **A handoff is a task, handed to one lane.** The remaining handoff text is [`tasks/autoqa-handoff.md`](../../yolo-packs/tillr/tasks/autoqa-handoff.md), outside any contribution, given to a review lane by whoever dispatches it. `D-AQ7` rules its deletion.

---

## Machine scoping

The pack ships identical source, launchers and skills on every machine, and carries no provider or account configuration, so `yolo-packs/tillr` is machine-agnostic. Which workspaces load it is the operator's `yolo` configuration.

---

## What makes this fail, and what prevents each

| Failure mode | What prevents it |
| :--- | :--- |
| **Docs drifting from `--help`** | Flags are banned from `README` and skill files ([the documentation split](#documentation-split-and-the-honesty-rule)); the documentation test checks every documented `tillr* <face>` call against live `--help` output and rejects flag tables. |
| **Launcher breaks without a Go toolchain** | The warm path uses only file tests and no `go` call; a cold cache without `go` exits `1` naming the missing digest and toolchain ([Build and delivery](#build-and-delivery)), and the launcher never runs at jail startup. |
| **The pack runs `tillr` in a repository that did not ask for it** | No `briefing` and no configuration overlay runs `tillr`; the pack only puts the binaries and skills in reach. |
| **An edit to one module leaves another module's binary stale** | The fast-path check and the digest both walk the whole source tree ([Build and delivery](#build-and-delivery)), so any source change invalidates all cached binaries on their next call. |

---

## Running a tree the pack has not rendered

**Requirement.** An agent working in this repository can build `tillr` from [`tools/tillr*`](../../tools) and run it against a store **without the owner relaunching anything**.

**What the launcher does not give.** The launcher's source root is the pack's **rendered** tree, written by `yolo` at launch. `just pack-sync` rewrites only the repository's `yolo-packs/tillr/files/` copy, and a host render is what puts that copy in the launch's home. A change to `tools/tillr*` therefore reaches the binaries on `PATH` only after the pack is mirrored and rendered, and a loop that is running keeps the binary it loaded.

**The in-launch handoff is a `PATH` prepend, not a launcher change.** `just tillr-build` builds the eight programs into one output directory from `refs/tillr/green`, the last trunk commit the after-merge checks proved. Prepend that directory to `PATH` for the loop and its children:

```sh
just tillr-build
PATH="$YOLO_DURABLE_DIR/bin:$PATH" tillr drain loop ...
```

- **The eight are built together.** `tillr` resolves `tillr-<noun>` through `PATH`, and the loop resolves `tillr-run` and `tillr-item` through `PATH`; a directory holding only `tillr` would hand the loop the launched siblings.
- **The loop says what it runs.** Its `loop: build` line names the revision, so a fix is never called closed from a tree the process never loaded.
- **Use a scratch store.** A newer binary that opens a store migrates it forward, and the launched binaries then refuse it. An experiment runs against a store copied from the live one or initialized fresh.

**A schema-compatibility policy is not decided.** It is needed only if two versions must share one store: the schema version would become a floor plus named capabilities, and a binary would refuse only what it cannot read. It trades a loud refusal for a quiet semantic mismatch, so it is the last thing to rule on.

## Why it's this way

| ID | Ruling | Why |
| :--- | :--- | :--- |
| `OQ-TD1` | The pack sits in user-scope configuration, listed for every workspace | The pack carries no provider or account configuration, and nothing in it runs `tillr` unasked |
| `OQ-TD2` | The AutoQA handoff lives in no skill of this pack | `D-AQ1` and `D-AQ7` in [the AutoQA design](../design/tillr-autoqa.md): AutoQA ships in a pack of its own ([the handoff](#the-autoqa-handoff-through-the-pack)) |
| `OQ-TD3` | On a cold or invalidated cache the launcher compiles only the invoked binary, once per source digest | A caller pays for the binary it runs ([Build and delivery](#build-and-delivery)) |
| `OQ-TD4` | The pack ships the digest-caching source launcher, never a launch-time build | A machine without Go still launches cleanly; a single `tillr` launcher with internal subcommands and a `program` with `build`/`produces` are both refused |
