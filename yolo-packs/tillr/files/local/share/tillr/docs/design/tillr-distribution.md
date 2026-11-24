---
status: in-review
stage: BUILT
next: "Nothing owed. The schema-compatibility policy of Section 9 is undecided and is written only if two versions must share one store."
---

# Ship `tillr` as one source-built pack with eight `PATH` binaries

> **Delivered copy.** This is `docs/design/tillr-distribution.md` from this repository at commit `0275c51d52` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Built. [`yolo-packs/tillr`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/pack.json) ships the source tree, the eight launchers and two skills. UNMEASURED here: the checks that hold it true are [`tests/pack-sync.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/pack-sync.test.py) (the mirror and the launchers) and [`tests/tillr-docs.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/tillr-docs.test.py) (the documentation split).

> **In short.** `tillr` is distributed through one pack in a source-plus-launcher pattern: the module source is mirrored into the pack, eight `program` entries on `PATH` each build and cache one binary from that source, and documentation is split into three layers where `--help` alone owns flags.

**The shape.** One `files` tree (`~/.local/share/tillr/`), eight `program` launchers on `PATH` (`tillr` plus the seven `tillr-<noun>` binaries) backed by a digest-keyed build cache (`~/.cache/tillr/bin/`), a `README.md` per module for standalone judgment, and the pack's skills for how the binaries compose.

**Cost.** The module source exists twice: under [`tools/`](https://github.com/mschulkind-oss/tillr/blob/main/tools) and, mirrored by `just pack-sync`, under [`yolo-packs/`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs). Eight `program` entries put eight launchers in `~/.local/bin/`.

**Start at [§1](#1-one-pack-eight-programs-and-what-path-sees)** — the eight-binary `PATH` contract drives the launcher and the documentation split.

**Needs your ruling:** None. The four questions are settled by what is built ([Open Questions](#open-questions)).

---

## 1. One pack, eight programs, and what `PATH` sees

**Verdict:** One pack at `yolo-packs/tillr` named `tillr`, declaring **eight `program` contributions** (the umbrella dispatcher `tillr` plus the seven standalone `tillr-<noun>` binaries), **one `files` contribution**, and **one `skills` contribution**. Rejected: a single `tillr` launcher with internal subcommands.

| Module directory | Binary on `PATH` |
| :--- | :--- |
| [`tools/tillr`](../../tillr) | *(none: the record, store and event library the binaries import)* |
| [`tools/tillr-cli`](../../tillr-cli) | `tillr` |
| [`tools/tillr-drain`](../../tillr-drain) | `tillr-drain` |
| [`tools/tillr-gate`](../../tillr-gate) | `tillr-gate` |
| [`tools/tillr-item`](../../tillr-item) | `tillr-item` |
| [`tools/tillr-land`](../../tillr-land) | `tillr-land` |
| [`tools/tillr-run`](../../tillr-run) | `tillr-run` |
| [`tools/tillr-store`](../../tillr-store) | `tillr-store` |
| [`tools/tillr-view`](../../tillr-view) | `tillr-view` |

Each binary's `--help` owns its face list; this document does not enumerate faces.

Three constraints force eight `program` declarations rather than a single `tillr` entry:

1. **The dispatcher resolves its siblings through `PATH`.** [`tools/tillr-cli`](../../tillr-cli) refuses a noun whose `tillr-<noun>` binary is not on `PATH`.
2. **`yolo` owns `~/.local/bin` by declaration.** `yolo` refuses a `files` contribution into `~/.local/bin`; every executable on `PATH` is a `program` contribution (`via: "pack"`).
3. **Standalone composition requires direct binary names.** An external agent composes individual tools (`tillr-gate`, `tillr-view`, `tillr-item`) without routing through the drain loop.

---

## 2. Build and delivery

**Verdict:** The **source-plus-launcher** pattern: the pack delivers source as `files`, and each `program` is a launcher that builds and caches its binary. Rejected: `program` with `build`/`produces`.

- **Source delivery (`files`):** `just pack-sync` copies the module directories named in `MODULES` of [`tests/pack-sync.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/pack-sync.test.py) into the pack's `files/local/share/tillr/<module>/`, which `yolo` renders to `~/.local/share/tillr/<module>/`. That test guards the copy against drift. [`tools/tillr`](../../tillr) is part of the tree because the binary modules resolve the shared library through relative `replace` paths in `go.mod`.
- **Launcher mechanics (`program`):** The eight launchers are one body that differs in two assignments: the binary's name and its module (`tillr` builds from `tillr-cli`; `tillr-<noun>` builds from the module of the same name). The launcher never changes its own working directory (`go build` runs in a subshell) and ends with `exec`, so a store named relative to the caller's directory resolves in the caller's repository.
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

## 3. Documentation split and the honesty rule

Every fact about `tillr` has one home across three layers:

| Layer | Location | Owns | Must never contain |
| :--- | :--- | :--- | :--- |
| **1. CLI surface** | `<bin> --help`, `<bin> <face> --help` | Nouns, faces, flags, defaults, argument shapes, exit codes | Multi-binary workflow prose |
| **2. Component judgment** | One `README.md` per module under [`tools/`](https://github.com/mschulkind-oss/tillr/blob/main/tools) (shipped to `~/.local/share/tillr/<mod>/`) | What the module owns, state invariants, standalone invocation for external callers, refusal conditions, **Known Gaps** | Flag tables, copied `--help` blocks, full loop orchestration |
| **3. Loop composition** | The pack skill [`tillr-operating`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/skills/tillr-operating/SKILL.md) | How the eight binaries compose across the seven-step loop ([§4](#4-the-composition-skill-headings-and-commands)) and the operator's stop conditions | Per-flag reference tables or internal Go package layout |

### The rule that prevents disagreement (One-Home Rule + Mechanical Gate)

1. **Prose cites `<bin> <face> --help` instead of listing flags.** Neither a module `README.md` nor the composition skill states flag names beyond the bare `<bin> <face>` verb needed to show control flow.
2. **Mechanical `--help` conformance check.** [`tests/tillr-docs.test.py`](https://github.com/mschulkind-oss/tillr/blob/main/tests/tillr-docs.test.py) extracts every `tillr` and `tillr-<noun>` invocation from the module `README.md` files and the skill, runs the binary built from the tree with `--help`, and fails when a documented noun or face does not exist or when a document carries a flag table (a table whose header names a flag, or an option-list line).

### How a reader knows which is current — and what is unfinished

- **The binary on `PATH` wins on surface syntax** because the launcher compiles it from the current source digest.
- **Every module `README.md` opens with a stamped Known Gaps callout** naming the date and commit it was verified at and the gaps a caller will meet. The same test fails a README without it.

---

## 4. The composition skill: headings and commands

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

## 5. The AutoQA handoff through the pack

The pack ships no AutoQA skill and no briefing. [The AutoQA design](tillr-autoqa.md) supersedes a handoff skill in this pack (`D-AQ1` and `D-AQ7` there): the tillr pack ships its own skills only, and AutoQA ships in a pack of its own.

Two rules stand:

- **Not a `briefing` contribution.** A briefing is injected into every agent turn in every workspace that selects the pack, which spends context on a one-time review prompt and tells an agent sent to build something to review instead.
- **A handoff is a task, handed to one lane.** The remaining handoff text is [`tasks/autoqa-handoff.md`](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/tasks/autoqa-handoff.md), outside any contribution, given to a review lane by whoever dispatches it. `D-AQ7` rules its deletion.

---

## 6. Machine scoping

The pack ships identical source, launchers and skills on every machine, and carries no provider or account configuration, so `yolo-packs/tillr` is machine-agnostic. Which workspaces load it is the operator's `yolo` configuration.

---

## 8. What makes this fail — and what prevents each

| Failure mode | What in this design prevents it |
| :--- | :--- |
| **Docs drifting from `--help`** | Flags are banned from `README` and skill files ([§3](#3-documentation-split-and-the-honesty-rule)); the documentation test checks every documented `tillr* <face>` call against live `--help` output and rejects flag tables. |
| **Launcher breaks without a Go toolchain** | The warm path uses only file tests and no `go` call; a cold cache without `go` exits `1` naming the missing digest and toolchain ([§2](#2-build-and-delivery)), and the launcher never runs at jail startup. |
| **The pack runs `tillr` in a repository that did not ask for it** | No `briefing` and no configuration overlay runs `tillr`; the pack only puts the binaries and skills in reach. |
| **An edit to one module leaves another module's binary stale** | The fast-path check and the digest both walk the whole source tree ([§2](#2-build-and-delivery)), so any source change invalidates all eight cached binaries on their next call. |

---

## 9. The agent's `tillr` must not wait for a launch

**Requirement (owner, 2026-11-13).** An agent working in this repository can build `tillr` from [`tools/tillr*`](https://github.com/mschulkind-oss/tillr/blob/main/tools) and run it against a store **without the owner relaunching anything**.

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

## Open Questions

All four are settled by what is built.

1. ✅ **OQ-TD1: Does `yolo-packs/tillr` sit in user-scope configuration for every workspace, or stay opt-in per workspace?**

   **Answer:**
   > User scope: the pack is meant to be listed for every workspace.

2. ✅ **OQ-TD2: Does the AutoQA handoff live in its own skill or inside the main skill?**

   **Answer:**
   > Neither. `D-AQ1` and `D-AQ7` in [the AutoQA design](tillr-autoqa.md) settle it: the tillr pack ships no AutoQA skill ([§5](#5-the-autoqa-handoff-through-the-pack)).

3. ✅ **OQ-TD3: On a cold or invalidated cache, does the launcher compile only the invoked binary or all eight?**

   **Answer:**
   > Only the invoked binary, once per source digest ([§2](#2-build-and-delivery)).

4. ✅ **OQ-TD4: Does the pack ship the digest-caching source launcher, or a launch-time build?**

   **Answer:**
   > The digest-caching launcher ([§2](#2-build-and-delivery)). What it leaves open, running a tree the pack has not rendered, is [§9](#9-the-agents-tillr-must-not-wait-for-a-launch).
