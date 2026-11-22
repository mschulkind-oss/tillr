---
title: "Contract: tillr-land"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, land]
summary: "What tillr-land provides and expects, read from the code at dab5a2da0e: two faces, the `land.landing` record, the `land` package, two live tables, three results and nine refusal reasons, six ref prefixes and the land lock. It expects the gate's verdict, the item utility's states, and two of the drain's tables, one of which it writes; and its module and the drain's require each other."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-land

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-land` with one package, `land`. It puts a judged candidate on the trunk as one commit, under a lock, and records the landing.

## 1. What it provides

### 1.1 Faces

| Face | Flags others pass | What it prints |
| :--- | :--- | :--- |
| `pass` | `--store --repo --item --run --base --head --verdict`, and `--base-ref --message-from --run-dir --observed-trunk --now --lock --lock-timeout` | `land.landing`, also when it refuses |
| `one` | `--store --repo --branch --gate`, and `--trunk --item --run --now --lock --lock-timeout` | `land.landing` |

| Exit | Meaning |
| :--- | :--- |
| 0 | Landed |
| 1 | Not landed: a squash conflict or a refusal. The record says which |
| 2 | Usage, or a verdict it cannot read |
| 3 | A store this build cannot open |
| 4 | An external failure, such as Git |
| 5 | Internal |

`pass` is run by `tillr-loop`, as the drain's land step. `one` is for a person.

### 1.2 Go packages

One: `land`. Only the drain uses names from it.

| Names | Used for |
| :--- | :--- |
| `OpenTrunk Trunk` | Trunk and Git operations |
| `OpenLock` | The land lock |
| `DecodeVerdict Verdict VerdictPass VerdictPassInherited` | Reading a `gate.verdict` |
| `RecordReplayLanding` | Recording a landing made by replaying commits |
| `LandedRefPrefix PublishedRefPrefix` | Ref names |
| In the drain's tests: `Land LandingRequest LandingKind OwnerHooks ResetOwnerHooks RecoveryPrefix` | Driving a landing in process |

### 1.3 Records and event kinds

`land.landing` is both the record printed and the event appended, in the same write as the `landings` row. Its payload: `landing_id item_id run_id verdict_id result reason base_sha head_sha trunk_before trunk_after gated_tree landed_tree conflict_paths gate_skipped_reason`, and optionally `base_ref replay_tip lock_observation red_cap faults`. A lock observation is `run_id` and `events`, each with `kind` and `at_ns`.

`land.fault` is the second event kind.

### 1.4 Tables

| Table | Read from outside by |
| :--- | :--- |
| `landings` | drain (about fifteen files), view (three), item (two), the roller |
| `landing_claims` | item |
| `candidates`, `landing_attempts`: nothing writes them | view |

### 1.5 Refs and files

| Place | Read by |
| :--- | :--- |
| `refs/tillr/landed/<run>` | drain |
| `refs/tillr/runs/<run>`, the pin of a run's head | drain |
| `refs/tillr/owner-recovery/…` | drain |
| `refs/tillr/patch-history/<fork>/…` | drain |
| `refs/tillr/trunk-history/observed` and `lost`, also written by the drain | drain |
| `refs/tillr/published/<remote>/<branch>`: the name is land's constant; the only writer is the drain | drain, view |
| `<git common directory>/land.lock` and `land.lock.guard` | drain |
| `TILLR_LAND_OBSERVE`, which the drain sets and land reads | — |

### 1.6 Names others match on

- **Results.** `landed`, `squash-conflict`, `refused`.
- **Refusal reasons.** `verdict-mismatch`, `verdict-failed`, `verdict-error`, `item-not-landable`, `patch-rewind`, `patch-at-head`, `trunk-rewritten`, `trunk-worktree-uncommitted`, `already-on-trunk`. The drain branches on the last three.
- **Lock observation kinds.** `acquired`, `released`, `squash`, `trunk-move`, `receipt`. The drain requires the first to be `acquired` and the last `released`.
- **Verdict results it accepts.** `pass`, `pass-inherited`.

For `trunk-worktree-uncommitted`, `conflict_paths` holds the paths with uncommitted content; the drain reads it that way.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, exit codes, store opener, DDL and kind registries | [tillr](./tillr.md) | Go import | throughout |
| The `gate.verdict` record: `verdict_id plan_id registry_digest base_sha tree_sha result checks`, and that a pass binds a base and a tree | [tillr-gate](./tillr-gate.md) | files (`--verdict`) | `land/verdict.go` |
| `gate.change` with the id `change-<run>`, and `gate.verdict.json` and `fork.registry.json` in the run directory; the registry's digest rule; a declaration with the id `fork-build` | [tillr-gate](./tillr-gate.md), the repository's gate scripts | files | `land/fork_rebase.go` |
| For `one`: a gate program that prints a `gate.verdict`, exit 0 for a pass and 1 for a fail | the caller | subprocess | `land/one.go` |
| Rows of `items`: whether an item's state is `closed` or `resolved-elsewhere` | [tillr-item](./tillr-item.md) | SQL | `land/landing.go` |
| `trunk_history` (`observed_tip`, `lost_tip`), read and written; `trunk_rewrites` (`old_sha`, `new_sha`), read | [tillr-drain](./tillr-drain.md) | SQL | `land/trunk_history.go`, `land/landing.go` |
| The type `redcap.Hold`, for one payload field | [tillr-drain](./tillr-drain.md) | Go import | `land/landing.go` |
| The run directory `.tillr/loop/<run>/` | [tillr-drain](./tillr-drain.md) | files | `land/fork_rebase.go` |
| `bin/pi-forks patch-branch target`: one tab-separated line of four fields | the repository's fork tool, outside tillr | subprocess | `land/fork_rebase.go` |
| `user.name` and `user.email` in Git config, not overridden locally | the machine | Git | `land/trunk.go` |

## 3. Contract and private

**Contract:**

- `pass` with its flags, and the six exit codes.
- `land.landing` with every field of [§1.3](#13-records-and-event-kinds) except `red_cap`, which goes with the import it needs.
- The names of [§1.6](#16-names-others-match-on).
- The `land` names in [§1.2](#12-go-packages), and readers to add: the current landing of an item, whether an item has landed, the landings of a run, and the landed trunk commits. These replace the SQL in the drain, the view, the item utility and the roller.
- `landings` for the columns the readers return, until the readers exist; `landing_claims` behind one function for the item utility.
- The ref prefixes it writes, and the lock file with `OpenLock` as the only way in.

**Private:**

- `one`, which no program runs.
- `candidates` and `landing_attempts`, to be dropped.
- `land.fault`'s payload; the commits file under the Git common directory.
- Every sentence, the missing-identity refusal included.
- `PublishedRefPrefix`, which moves to the drain, its only writer.

## 4. How the contract is pinned

In `tools/tillr-land`, in its fast suite.

- **Exists today.** `TestTheRefusalReasonsAreTheRegisteredSet`, `TestTheOutcomesAreTheRecordedSet`, `TestTheLandingEventCarriesTheWholeLanding`, `TestPassLandsAJudgedHead`, `TestAVerdictThatDoesNotBindTheHeadsTreeIsRefused`, `TestDecodeVerdictAcceptsPassInherited`.
- **Examples.** One `land.landing` per result and per reason, made by `Land` in process on a fixture repository, compared whole. The drain's unnamed decode structs and the view's literal are replaced by reading these.
- **Name sets.** The two existing tests gain the files; the lock observation kinds get the same.
- **Readers.** Each new reader runs on the example stores, and its answer is compared.
- **Builders.** `land` publishes a builder that records a landing of each result through `Land`'s own write path. About 25 test files in other utilities type `landings` rows today and use it instead.
- **Its expectation of the gate.** Land's tests read the gate's verdict examples. `fork_rebase_test.go` stops building the gate from source.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As provider: RI-29 (the drain implements the lock a second time), RI-30 (results and reasons typed again; unnamed decodes), RI-31 (ref prefixes typed as literals), RI-32 (`landings` read with raw SQL from about twenty files), RI-33 (the item utility reads `landing_claims` and `landings`), RI-39 (the view models two retired records), RI-51 (the roller reads `landings`).
- As consumer: RI-27 (the module cycle with the drain), RI-28 (it reads and writes drain tables), RI-33 (it reads `items`), RI-34 (its own copy of the gate's verdict type), RI-35 (it recomputes the gate's registry digest).
