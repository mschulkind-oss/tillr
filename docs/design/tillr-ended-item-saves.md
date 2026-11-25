---
title: "When a saved ended-item ref ends, and bounding untracked files in lane saves"
status: in-review
stage: DESIGN
updated: 2026-11-25
tags: [tillr, drain, refs, watch, preservation, design]
summary: "Defines when refs/tillr/ended/<run> is removed by SweepRefs, how every ended-item save opens a run-grouped watch episode and is disposed of with a commit-bound receipt, how reopened items recover unpruned saves, and how snapshotWork bounds non-ignored untracked files before Git hashes blobs into .git/objects/ or bloats .tillr/state.db. Nothing here is built. Three questions await the owner (OQ-EW1 to OQ-EW3), and D-EW1 to D-EW6 are the designer's decisions."
next: "Rule OQ-EW1, OQ-EW2 and OQ-EW3; once ruled under OQ-PR21, file and build slices 1 to 5 of Section 8."
depends-on:
  - landing-principles.md
  - operator-decisions-inside-tillr.md
  - tillr-judge-and-lifecycle.md
  - tillr-watch.md
  - tillr-watch-alert-workflow.md
  - tillr-alert-triage-seat.md
vantage:
  status-chip: true
---

# When a saved ended-item ref ends, and bounding untracked files in lane saves

**Status:** Not built. Today `planRefSweep` in [`tools/tillr-drain/drain/refs.go`](../../tools/tillr-drain/drain/refs.go) retains every `refs/tillr/ended/<run>` ref unconditionally, no watch rule reads `drain.ended-work-saved`, and `snapshotWork` in [`tools/tillr-drain/drain/preserved.go`](../../tools/tillr-drain/drain/preserved.go) runs `git add -A -- .` over every non-ignored untracked file with no bound. Only the dated statements quoted in [§2](#2-governing-rulings) are owner rulings; the 2026-11-25 consumer-repository case in [§1](#1-the-case-and-what-the-tree-does-today) comes from a filed command-line request, and `D-EW1` to `D-EW6` in the [Decision Ledger](#decision-ledger) are the designer's decisions, which the owner may overturn.

> **In short.** A `refs/tillr/ended/<run>` ref ends in `SweepRefs` under `land.lock` — never on item closure alone, never on reading alone, and never inside `tillr-watch` — when the item is still finished and one of three recorded facts holds for that exact commit: the trunk already holds every changed path's bytes, custody moved to an operator ref or a landed follow-up item, or an authorized actor recorded a commit-bound `drop` disposition with evidence. Before staging untracked files, `snapshotWork` enumerates and `lstat`s non-ignored untracked entries so thousands of build-output files are never hashed into `.git/objects/` or serialized whole into `.tillr/state.db`.

**Why it matters.** Without an ending rule, an alert, or an untracked-file bound, `refs/tillr/ended/*` accumulates invisibly across closed items, forces hand cleanup by the operator, pins thousands of loose build-output blobs in `.git/objects/`, and writes multi-thousand-entry file lists into `.tillr/state.db`.

**The shape.** Evaluate the candidate ending triggers ([§3](#3-evaluating-the-candidate-triggers-for-ending-a-ref)), define the three `SweepRefs` ending predicates, disposition receipt, and `run_id`-grouped watch rule ([§4](#4-when-refstillrendedrun-ends)), bound untracked files before `git add` and cap the SQLite event list ([§5](#5-bounding-untracked-files-before-git-hashes-them)), and reconcile idle ended-lane reclamation with `D-JL33` ([§6](#6-reclaiming-ended-item-lanes-versus-d-jl33)).

**Cost.** One `git ls-files --others --exclude-standard -z` plus `lstat` pass when saving a dirty lane, one tree-diff comparison per retained `refs/tillr/ended/<run>` during `sweep-refs`, and one watch episode per unexpected ended-item save.

**Start at:** [§3](#3-evaluating-the-candidate-triggers-for-ending-a-ref) and [§4](#4-when-refstillrendedrun-ends), then [§5](#5-bounding-untracked-files-before-git-hashes-them).

**Needs your ruling:** [OQ-EW1](#OQ-EW1) (who may authorize `drop` when saved bytes are absent from the trunk), [OQ-EW2](#OQ-EW2) (what happens when non-ignored untracked files exceed the save bound), and [OQ-EW3](#OQ-EW3) (whether idle ended-item lanes keep bounded save-and-reset or hold under `D-JL33`).

**Awaits your review:** the designer's decisions `D-EW1` to `D-EW6` in the [Decision Ledger](#decision-ledger).

**Reads with:** [judge and lifecycle §4.20](./tillr-judge-and-lifecycle.md#420-leftover-work-in-a-lane-is-an-alert-never-an-automatic-sweeper-d-jl33) (`D-JL33` on leftover work in lanes), [operator decisions inside tillr](./operator-decisions-inside-tillr.md#1-the-ruling) (`R-OD1`), [authority, not approval](../reference/authority-not-approval.md#what-owner-only-is-for) (irreversible loss versus mechanical redundancy), [tillr-watch](./tillr-watch.md#57-leftover-work-holding-a-lane-out-of-rotation-d-tw48-d-tw49-d-tw50), and [the alert workflow](./tillr-watch-alert-workflow.md#3-the-alert-workflow-from-opened-to-investigated-to-settled).

---

## 1. The case and what the tree does today

A command-line request filed on 2026-11-25 reported that a consumer repository held `13` refs under `refs/tillr/ended/<run>`, all belonging to closed items: `9` of them were junk that the operator deleted by hand, and `2` of them held roughly `7,190` files of build output each. Four facts in the tree at `1ffbe3b665` explain why.

### 1.1 Where `refs/tillr/ended/<run>` is written

`saveEndedItemWork` in [`tools/tillr-drain/drain/ended_item_lane.go`](../../tools/tillr-drain/drain/ended_item_lane.go) (introduced in `a34334e21b` on 2026-11-22 and updated in `b2b5407048` and `9cb58a6279`) snapshots uncommitted files left in a lane whose item has already reached a finished state (`closed`, `resolved-elsewhere`, `landed`, or `"removed"` when absent from `items`). Four call sites invoke it:

1. `reclaimEndedItemLanes` (`ended_item_lane.go`, called during each pass from `pass.go`), scanning idle on-disk lanes whose most recent run belonged to an ended item.
2. `settleTerminalItem` (`terminal_item.go`), when a pending run settles as `item-already-terminal`.
3. Orphaned workflow release (`pass.go`, with reason `"workflow released after item ended (" + state + ")"`).
4. `abandonFromBase` (`loop.go`), when settling a run whose item is already terminal.

When `git --no-optional-locks status --porcelain --untracked-files=all` is non-empty, `saveEndedItemWork` calls `snapshotWork` (`preserved.go`), writes a commit at `refs/tillr/ended/<run>` (with a second parent `refs/tillr/ended/<run>^2` when staged index contents differ from both `HEAD` and the working tree) whose commit message is the JSON `EndedWorkSaved` record, appends `drain.ended-work-saved` (`object_type = "run"`, `object_id = <run>`), pins `HEAD` at `refs/tillr/runs/<run>`, and resets the lane (`git reset --hard` and `git clean -fd`).

### 1.2 Why `refs/tillr/ended/<run>` never ends today

In [`tools/tillr-drain/drain/refs.go`](../../tools/tillr-drain/drain/refs.go):

- `planRefSweep` matches `endedWorkPrefix` (`"refs/tillr/ended/"`) with one unconditional arm: `ref.Reason = "ended item's saved uncommitted work; retained"` and `Remove = false`.
- `pruneRunRefs` checks `"refs/tillr/runs"`, `"refs/tillr/landed"`, `preservedPrefix`, and `remoteSyncRefPrefix` with `git for-each-ref --count=1`, omitting `endedWorkPrefix`.

Unlike `refs/tillr/preserved/<run>` — which is written while an item can still run again and deleted by `planRefSweep` once that item finishes (`drain.work-pruned`) — `refs/tillr/ended/<run>` is written *after* the item has already finished. Copying `preserved/`'s rule onto `ended/` would delete every `refs/tillr/ended/<run>` on the next pass. Meanwhile, a `closed` or `landed` item can re-enter active work through `Reopen` in [`tools/tillr-item/item/lifecycle.go`](../../tools/tillr-item/item/lifecycle.go) or `ReturnLanded` on revert ([the item-states reference](../reference/tillr-item-states.md#blocking-and-closure)), yet `preservedFacts` (`preserved.go`) cites only `refs/tillr/preserved/` and ignores `refs/tillr/ended/`.

### 1.3 Why saves accumulate invisibly

Running `rg -n 'ended-work|ended_work|refs/tillr/ended' config/ tools/ docs/` matches only `tools/tillr-drain/drain/`:

- No rule under `config/tillr/watch/` reads `drain.ended-work-saved`.
- `tillr-view` renders only `refs/tillr/preserved/`.
- `tillr store inventory` ([the queue-persistence reference](../reference/tillr-queue-persistence.md#epoch-record-and-store-inventory-tillr-store-inventory)) counts `refs/tillr/runs/*`, `refs/tillr/lanes/*`, and `refs/tillr/preserved/*`, lumping `refs/tillr/ended/*` into `other`.
- [The drain README's ref table](../../tools/tillr-drain/README.md#ref-lifetimes-and-backlog-cleanup) omits `ended/<run>`, and [its uncommitted-work section](../../tools/tillr-drain/README.md#earlier-uncommitted-work) still describes the pre-`a34334e21b` `drain.lane-discarded` path.

### 1.4 Why untracked build output bloats `.git/objects/` and `.tillr/state.db`

`snapshotWork` in [`tools/tillr-drain/drain/preserved.go`](../../tools/tillr-drain/drain/preserved.go) creates a temporary `GIT_INDEX_FILE`, runs `git read-tree <HEAD>`, and runs `git add -A -- .` followed by `git write-tree`. That command skips `.gitignore`-matched paths and hashes **every** non-ignored untracked file in the worktree with no bound on file count, single-file size, or total bytes. When a build or test step writes ~`7,190` files into an unignored directory:

1. `git add -A -- .` hashes ~`7,190` blobs into `.git/objects/`, which stay pinned as long as `refs/tillr/ended/<run>` exists.
2. `snapshotSummary` builds ~`7,190` `PreservedFile` structs, and `saveEndedItemWork` writes the full JSON slice twice: into the `refs/tillr/ended/<run>` commit message and into `.tillr/state.db`'s append-only `events` table (`drain.ended-work-saved`).
3. `preserveWork` (`refs/tillr/preserved/<run>`) calls the same `snapshotWork` helper, so an active item's lane preservation has the same unbounded `git add -A -- .` exposure.

---

## 2. Governing rulings

Only dated owner rulings govern this design as settled policy:

1. **Leftover work in a lane is an alert, never a silent sweeper (`D-JL33` / `D-TW48`, 2026-11-21).** Recorded in [judge and lifecycle §4.20](./tillr-judge-and-lifecycle.md#420-leftover-work-in-a-lane-is-an-alert-never-an-automatic-sweeper-d-jl33) and [tillr-watch §5.7](./tillr-watch.md#57-leftover-work-holding-a-lane-out-of-rotation-d-tw48-d-tw49-d-tw50): leftover work in a lane raises an alert to trace to its root cause rather than disappearing into an unmonitored sweeper. Commit `9cb58a6279` (2026-11-24) noted that `reclaimEndedItemLanes` already reclaims ended-item lanes despite `D-JL33`; [§6](#6-reclaiming-ended-item-lanes-versus-d-jl33) and [OQ-EW3](#OQ-EW3) put that boundary directly to the owner.
2. **Watching applies no remedy ([`OQ-TW2`](./tillr-watch.md#OQ-TW2) / [`R-AW2`](./tillr-watch-alert-workflow.md#R-AW2), 2026-11-21), and every alert gets an investigation and a closed outcome ([`R-AW1`](./tillr-watch-alert-workflow.md#R-AW1), [`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2), 2026-11-22 and 2026-11-23).** Recorded in [the alert workflow §1](./tillr-watch-alert-workflow.md#1-what-the-owner-said) and [§3](./tillr-watch-alert-workflow.md#3-the-alert-workflow-from-opened-to-investigated-to-settled) and in [tillr-watch §6.3](./tillr-watch.md#63-mode-a-mechanical-answers-inside-tillr-and-non-wasting-drain-triage-d-tw7-oq-tw2-oq-tw4): `tillr-watch` never mutates Git state or applies automatic remedies, and an alert episode whose condition clears automatically still requires an investigation and one of the three recorded outcomes (`silence`, `fix-rule`, or `fix-fault`).
3. **Problems the operator could settle without the owner move inside tillr (`R-OD1`, 2026-11-21).** In [operator decisions inside tillr §1](./operator-decisions-inside-tillr.md#1-the-ruling), the owner ruled:
   > "I guess a working principle should be that if you ever feel that you as [the operator] could solve some problem, and it wouldn't need to be escalated to me, then that problem should escalate to... a similarly responsible agent, process, ticket, whatever, within Tiller, rather than popping up to you."
   Alongside [authority, not approval](../reference/authority-not-approval.md#what-owner-only-is-for), deleting a ref whose bytes are already on the trunk or held elsewhere destroys nothing and is mechanical bookkeeping, whereas discarding the last copy of unpublished work is irreversible loss and belongs to the owner unless settled policy classifies it cleanly.
4. **No timers, TTLs, or arbitrary count caps ([Landing Principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), `R-AW3`).** A saved ref never expires by age or because `N` newer saves arrived.
5. **Decision provenance ([provenance review §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening)).** Under [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), decisions that touch irreversible work loss or repository safety wait on the owner's ruling ([OQ-EW1](#OQ-EW1) to [OQ-EW3](#OQ-EW3)); under `D-PR2`, every engineering and mechanism choice proposed here (`D-EW1` to `D-EW6`) is marked as the designer's decision for the owner to keep or overturn.

---

## 3. Evaluating the candidate triggers for ending a ref

The request names two example triggers — *"when the operator resolves the alert about it, or when its item closes and the save has been read"* — alongside the status quo of retaining forever. By the designer's decision (`D-EW1`, which the owner may overturn), neither example suffices on its own, and separating **alert root-cause settlement** from **byte-custody disposition** resolves every failure mode.

| Candidate trigger | Why it fails alone | How this design handles it (`D-EW1`, `D-EW2`, `D-EW3`) |
| :--- | :--- | :--- |
| **Retain forever** (today) | Accumulates junk saves invisibly and forces manual `git update-ref -d` by the operator. | Replaced by three deterministic `SweepRefs` predicates ([§4.2](#42-the-three-ending-predicates-d-ew2)). |
| **Item is closed (or finished) alone** | `refs/tillr/ended/<run>` is created *only* when its item is already finished (`closed`, `resolved-elsewhere`, `landed`, or `removed`). Deleting on finished state alone deletes every save on the next pass. | Finished item state is a *necessary precondition* for deletion, never a sufficient one; reopening the item immediately blocks deletion ([§3.4](#34-reopened-and-reverted-items)). |
| **Item is closed and the save has been read** | Reading (`git show`, an Observatory page view, or a triage inspection) is observation, not a custody decision. If a reader inspects the ref, sees substantive unpublished edits, and pauses, crashes, or escalates before pinning `refs/tillr/held/<name>` or filing a follow-up item, a read-triggered sweep deletes the ref underneath them. Conversely, a save whose bytes are already on `main` needs no read at all. | Inspection is required *before* disposing of non-redundant bytes, enforced by requiring the exact commit SHA on the disposition command, and deletion waits for the recorded *disposition* (`drop`, `promote`, or `hold`), never a bare read receipt. |
| **Operator resolves (`settles`) the watch alert alone** | First, `tillr-watch` applies no remedies ([`OQ-TW2`](./tillr-watch.md#OQ-TW2), [`R-AW2`](./tillr-watch-alert-workflow.md#R-AW2)). Second, `tillr watch settle` answers *why the ended lane was dirty* (for example, `fix-fault` for a missing `.gitignore` rule or a runner leak), which is independent of whether the saved bytes should be dropped or kept until a follow-up item lands. Third, reading `alert_episodes` from `planRefSweep` would couple `tillr-drain` ref cleanup to `tillr-watch`'s schema and break standalone `tillr-drain` stores. | Reversed: `SweepRefs` reads only Git and `drain.*` / `items` state; recording `drain.ended-work-disposed` or `drain.ended-work-pruned` clears the watch episode's *condition*, while [`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2) still requires investigating and settling the root cause ([§4.4](#44-alerting-triage-and-read-surfaces-d-ew3)). |
| **Age TTL or newest-`N` cap** | Violates [Landing Principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out): `9` build-output saves can push out `1` real unpublished source edit. | Rejected; `RetryPinLimit` stays scoped to examined retry pins on active items. |

### 3.4 Reopened and reverted items

`closed` and `landed` are not irreversible states: `tillr item reopen` returns a closed item to `in-queue`, and automatic revert (`ReturnLanded`) returns a landed item to `in-queue`. Under the designer's decision (`D-EW1`):

1. Whenever the owning item's current state is not finished (`!finishedState(declared.Parsed, state)` and not `"removed"`), `planRefSweep` retains `refs/tillr/ended/<run>` with reason `"ended-work save; item reopened and active"`, even if an earlier `drop` disposition was recorded before the sweep ran.
2. `preservedFacts` in [`tools/tillr-drain/drain/preserved.go`](../../tools/tillr-drain/drain/preserved.go) reads unpruned `refs/tillr/ended/<run>` records for the item alongside `refs/tillr/preserved/<run>` and cites them in the next worker's brief as recovery evidence.

---

## 4. When `refs/tillr/ended/<run>` ends

### 4.1 One deleter: `SweepRefs` under `land.lock` (`D-EW2`)

By the designer's decision (`D-EW2`), `SweepRefs` in [`tools/tillr-drain/drain/refs.go`](../../tools/tillr-drain/drain/refs.go) — invoked both by `tillr-drain sweep-refs` and opportunistically by `pruneRunRefs` — is the **sole** code path that deletes `refs/tillr/ended/<run>`:

- `pruneRunRefs` adds `endedWorkPrefix` to its `git for-each-ref --count=1` check, and repository discovery in `prunePreservedRepositories` includes repositories named in unpruned `drain.ended-work-saved` records.
- `SweepRefs` acquires `land.lock` before sampling `refs/tillr`, runs `planRefSweep` inside the store transaction, deletes eligible refs through `deleteSettledRefs` with the expected-old SHA guard (`option no-deref` and `delete refs/tillr/ended/<run> <sampled_sha>`), and appends `drain.ended-work-pruned` (`object_type = "run"`, `object_id = <run>`) carrying `run_id`, `item_id`, `ref`, `sha`, and `prune_reason`.
- If a crash occurs after Git deletes `refs/tillr/ended/<run>` and before the store transaction commits `drain.ended-work-pruned`, the next `SweepRefs` pass replays the missing `drain.ended-work-pruned` receipt, matching how `drain.work-pruned` replays today.

### 4.2 The three ending predicates (`D-EW2`)

`planRefSweep` marks `refs/tillr/ended/<run>` at sampled commit `S` for removal (`Remove = true`) if and only if `!live[run]`, `!needed[run]`, the owning item is in a finished state or `"removed"`, and at least one of three checkable facts holds for `S`:

| # | Predicate | Checkable fact over Git and store state | `RefDisposition.Reason` |
| ---: | :--- | :--- | :--- |
| 1 | **`trunk-contains`** (mechanical redundancy) | `S^{tree} == TrunkRef^{tree}`, or every path changed between `S^1` and `S` (and between `S^1` and `S^2` when `S` has a staged-index second parent) has the identical blob OID — or confirmed absence for a deleted path — in `TrunkRef^{tree}` (batched outside the store write transaction and bound to sampled `(S, TrunkRef)`). | `"ended item's saved work already in trunk"` |
| 2 | **`custody-transferred`** (kept elsewhere or landed via follow-up) | Either an operator-owned ref (`refs/tillr/held/*` or `refs/tillr/recovery/*`) resolves to `S` (`len(operators[S]) > 0`), or a `drain.ended-work-disposed` event for `(run_id, ref)` with `sha == S` recorded `disposition = "promote"` naming `target_item_id` and `target_item_id` is now `landed` or `resolved-elsewhere`. | `"ended item's saved work held by <operator-ref>"` or `"ended item's saved work promoted to finished item <target_item_id>"` |
| 3 | **`disposed-drop`** (explicit commit-bound discard) | A `drain.ended-work-disposed` event for `(run_id, ref)` records `disposition = "drop"` with `sha == S` and a complete evidence packet (`actor`, `reason`, `evidence`), written by an actor authorized under [OQ-EW1](#OQ-EW1). | `"ended item's saved work disposed as drop by <actor>"` |

Why `trunk-contains` compares changed-path blobs against `TrunkRef^{tree}` rather than `git merge-base --is-ancestor S TrunkRef`: `saveEndedItemWork` writes a synthetic `commit-tree` commit on `refs/tillr/ended/<run>` that is never merged by commit ancestry, and tillr lands work by squash ([Landing Principle 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)). Comparing the changed paths' blobs against `TrunkRef^{tree}` detects work that already landed via another run or item without false positives, and deleting the redundant ref destroys zero unique bytes ([authority, not approval](../reference/authority-not-approval.md#what-owner-only-is-for)).

Why `promote` requires `target_item_id` to reach `landed` or `resolved-elsewhere` (never `closed` or `removed`): if a promoted follow-up item is closed without landing, `refs/tillr/ended/<run>` stays retained (`"promoted item <id> closed without landing; awaiting new disposition"`) so closing the follow-up item cannot silently discard the saved source bytes.

### 4.3 Recording a disposition (`tillr drain dispose-ended`) (`D-EW2`)

By the designer's decision (`D-EW2`), a dedicated drain face (`tillr drain dispose-ended`, whose exact flags `--help` will own) records `drain.ended-work-disposed` (`object_type = "run"`, `object_id = <run>`) with one of three dispositions:

1. **`drop`:** requires the caller to pass `run_id`, the expected commit `sha` `S`, `actor`, `reason`, and `evidence` (pointers to what was read: `S`, file summary, and why the bytes are build output, test residue, or superseded work). The command verifies that `refs/tillr/ended/<run>` exists and currently resolves to `S` before writing the event; a missing ref or mismatched SHA exits `1` and writes nothing. Who may invoke `drop` when `trunk-contains` is false is [OQ-EW1](#OQ-EW1).
2. **`promote`:** binds `(run_id, ref, sha = S)` to an existing `target_item_id` (`in-queue` or finished via `landed` / `resolved-elsewhere`). While `target_item_id` is open, `SweepRefs` retains `refs/tillr/ended/<run>` and `preservedFacts` cites `refs/tillr/ended/<run>` in `target_item_id`'s worker brief so the worker can cherry-pick or inspect the saved diff; once `target_item_id` reaches `landed` or `resolved-elsewhere`, `SweepRefs` prunes the ref under `custody-transferred`.
3. **`hold`:** creates `refs/tillr/held/<name>` pointing at `S` and appends `drain.ended-work-disposed` (`disposition = "hold"`, `held_ref = "refs/tillr/held/<name>"`), moving custody into the operator-owned `refs/tillr/held/` namespace so `SweepRefs` prunes `refs/tillr/ended/<run>` on the same pass while keeping the commit reachable until the owner or operator deletes the `held/` ref.

After recording any disposition, `dispose-ended` runs an opportunistic `SweepRefs` pass (`sweepIfUnlocked`) so eligible refs disappear immediately without waiting for the next loop tick.

### 4.4 Alerting, triage, and read surfaces (`D-EW3`)

By the designer's decision (`D-EW3`), ended-item saves become first-class across watch and read faces:

1. **Standing watch rule `config/tillr/watch/drain-ended-work-saved.toml`:**
   - Pattern: `predicate_match` on `drain.ended-work-saved`.
   - Grouping: **`group_by = ["run_id"]`**, never `item_id`. Under [`D-TW63`](./tillr-watch.md#D-TW63), item-subject episodes auto-close immediately when their item is `landed` or `closed`, and `drain.ended-work-saved` fires *only* for ended items. Grouping by `run_id` keeps one episode open per saved run, with `run_id`, `item_id`, `item_state`, `lane_id`, `ref`, and `summary` exposed in variables and guidance.
   - Auto-clear (`clear_when`): clears condition state (`resolved_reason = "condition-cleared"`) when `drain.ended-work-disposed` or `drain.ended-work-pruned` is recorded for that `run_id`.
   - Investigation and settlement: under [`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2), `condition-cleared` never settles the episode. Even when `trunk-contains` prunes a redundant save automatically, triage ([the alert triage seat](./tillr-alert-triage-seat.md)) or the operator investigates *why* the ended item left uncommitted files in its lane and settles the episode with `fix-fault` (for example, filing a `.gitignore` or runner-cleanup item), `fix-rule`, or a bounded `silence`.
2. **Inventory, view, and README surfaces:**
   - `tillr store inventory` reports `refs/tillr/ended/*` as its own ref bucket alongside `runs/*`, `lanes/*`, and `preserved/*` instead of hiding it inside `other`.
   - `tillr view` item pages list unpruned `refs/tillr/ended/<run>` saves (and their disposition status) alongside `refs/tillr/preserved/<run>`.
   - `tillr drain sweep-refs --dry-run` reports each retained `refs/tillr/ended/<run>` with its item state, summary, and specific hold reason (`awaiting disposition`, `promoted to <item_id>`, or `item reopened and active`).
   - [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md) updates both its uncommitted-work prose and its `refs/tillr/` lifetime table when the build lands.

### 4.5 Existing refs on upgrade (`D-EW6`)

By the designer's decision (`D-EW6`), upgrading does not delete unexamined historical refs in bulk:

- On the first `SweepRefs` pass after deployment, existing `refs/tillr/ended/<run>` refs that satisfy `trunk-contains` or `operator-ref-holds` are pruned mechanically and emit `drain.ended-work-pruned`.
- Every other existing `refs/tillr/ended/<run>` ref stays retained with reason `"ended item's saved work awaiting disposition"` and is surfaced in `tillr store inventory` and `tillr drain sweep-refs --dry-run` with its commit SHA and summary until disposed of.
- Consistent with rule-load baselines ([`D-TW66`](./tillr-watch.md#69-rule-load-baselines-judging-only-events-from-a-rules-first-load-onward-d-tw66)), `drain-ended-work-saved.toml` alerts on saves from its first load onward rather than opening historical alert storms on upgrade, while `store inventory` and `sweep-refs --dry-run` keep pre-existing refs visible ([Landing Principle 7](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)).

---

## 5. Bounding untracked files before Git hashes them

### 5.1 Pre-add untracked enumeration (`D-EW4`)

A lane save must **not** stage every non-ignored untracked file with no bound. Checking file counts *after* `git add -A -- .` is too late: `git add` has already hashed all `7,190` build outputs into `.git/objects/` before `write-tree` or `diff --numstat` runs.

By the designer's decision (`D-EW4`, which the owner may overturn), `snapshotWork` replaces unconditional `git add -A -- .` with a two-step staging pass over the temporary `GIT_INDEX_FILE`:

1. **Always stage tracked modifications and deletions** with `git add -u -- .` (and capture the staged index copy via `stagedSnapshot` as today). Tracked files are already bounded by the repository's own tracked tree.
2. **Enumerate and `lstat` non-ignored untracked entries before hashing:** run `git -C <target> --no-optional-locks ls-files --others --exclude-standard -z` and call `os.Lstat` on each path:
   - Skip non-regular special files (UNIX sockets, FIFOs, device nodes; symlinks and regular files are kept) and record skipped paths in `skipped_special` so `git add` never hangs or errors on a stray test socket.
   - Sum regular-file count (`untracked_files`), maximum single-file byte size (`max_untracked_file_bytes`), total regular-file bytes (`untracked_bytes`), and per-top-directory counts and byte totals (for example, `build/: 7190 files, 148 MiB`).
   - Compare against three configurable defaults in `.tillr.toml` under `[preservation]`:
     - `max_untracked_files = 256` files;
     - `max_untracked_file_bytes = 2097152` (`2 MiB`) for any single untracked regular file;
     - `max_untracked_total_bytes = 16777216` (`16 MiB`) across all untracked regular files in the worktree.
   - When all three bounds hold, stage those exact untracked paths with `git --literal-pathspecs add --pathspec-from-file=- --pathspec-file-nul` into the temporary index and write the tree.

Because `preserveWork` (`refs/tillr/preserved/<run>`) shares `snapshotWork`, applying the same pre-add inspection to both `ended/` and `preserved/` protects `.git/objects/` regardless of whether the item was still active or already terminal when the unignored build directory was left behind.

### 5.2 When untracked files exceed the bound ([`OQ-EW2`](#OQ-EW2))

When non-ignored untracked files exceed the `D-EW4` bound (such as the `7,190`-file build-output directories in the 2026-11-25 case), the drain faces a genuine trade-off between **preventing irreversible deletion of untracked source files** and **reclaiming lane capacity immediately**:

- **Holding the lane without `git clean -fd` (Option A of [OQ-EW2](#OQ-EW2), designer's leaning):** `saveEndedItemWork` saves tracked and staged edits (`git add -u`) to `refs/tillr/ended/<run>`, pins `HEAD` at `refs/tillr/runs/<run>`, skips hashing the oversized untracked tree, **does not** run `git clean -fd`, and holds that one lane out of rotation with `drain.lane-refused` (`reason = "untracked-save-bound-exceeded"`, naming the item, run, untracked count, total bytes, and top directory prefixes). `drain-lane-refused` alerts immediately; zero untracked source files are ever deleted silently, `.git/objects/` is untouched, sibling lanes keep draining, and triage files the missing `.gitignore` or build-directory defect ([`D-JL33`](./tillr-judge-and-lifecycle.md#420-leftover-work-in-a-lane-is-an-alert-never-an-automatic-sweeper-d-jl33)).
- **Omitting untracked files and resetting the lane (Option B of [OQ-EW2](#OQ-EW2)):** save tracked and staged edits to `refs/tillr/ended/<run>`, record the omitted untracked counts and top directory prefixes in `drain.ended-work-saved`, and run `git clean -fd` so the lane stays in rotation. Keeps full lane capacity unattended, at the cost of irreversibly deleting any new untracked source files that shared the worktree with the oversized build output.
- **Never saving untracked files in ended-item saves (Option C of [OQ-EW2](#OQ-EW2)):** `saveEndedItemWork` saves tracked and staged edits only and cleans every untracked file. Simplest, and silently discards every brand-new file left uncommitted when an item ends.

Because Options B and C can irreversibly delete unpublished untracked files while Option A temporarily holds one lane until cleaned, this choice belongs to the owner as [OQ-EW2](#OQ-EW2).

### 5.3 Bounding the SQLite event payload while keeping the full manifest on the commit (`D-EW5`)

Even when a save stays within bounds (or modifies hundreds of tracked files), `.tillr/state.db` and `8,192`-byte `tillr-watch` timeline bundles should not carry thousands of per-file diffstat entries. By the designer's decision (`D-EW5`):

- The commit message on `refs/tillr/ended/<run>` (and `refs/tillr/preserved/<run>`) continues to hold the complete JSON `EndedWorkSaved` / `PreservedWork` manifest, because that commit lives in Git alongside the saved tree and becomes unreachable for `git gc` the moment `SweepRefs` deletes the ref.
- In `.tillr/state.db`, `drain.ended-work-saved` and `drain.work-preserved` cap `files` and `index_files` at `64` entries each and add `total_files`, `truncated_files`, `untracked_files`, `untracked_bytes`, and `top_untracked_dirs` (up to `8` directory prefixes with file and byte counts) so queries, Observatory views, and watch bundles stay small and constant-bounded.

---

## 6. Reclaiming ended-item lanes versus `D-JL33`

Commit `9cb58a6279` (2026-11-24) recorded an open tension between [`D-JL33`](./tillr-judge-and-lifecycle.md#420-leftover-work-in-a-lane-is-an-alert-never-an-automatic-sweeper-d-jl33) (2026-11-21: leftover work in a lane is an alert, never swept automatically) and `reclaimEndedItemLanes` (`a34334e21b`, 2026-11-22), which silently saved dirty idle ended-item lanes into `refs/tillr/ended/<run>` and reset them with no alert at all.

Dirty ended-item lanes arise in two distinct ways:

1. **Known item-ending settlement transitions** (`settleTerminalItem` in `terminal_item.go`, `abandonFromBase` in `loop.go`, and orphaned workflow release in `pass.go`): the item closed or landed elsewhere *while* a run or workflow was in flight, so the drain itself is ending that run and releasing its lease.
2. **Unexpected dirty idle lanes** (`reclaimEndedItemLanes` in `ended_item_lane.go`): no run is leased or pending on the lane, its previous owner item is already finished, and uncommitted files are still sitting in the worktree because an earlier settlement or external command left them behind.

Once `drain-ended-work-saved` opens an alert episode requiring investigation on every save (`D-EW3`) and `snapshotWork` refuses unbounded untracked trees (`D-EW4`), saving into `refs/tillr/ended/<run>` no longer hides the defect. Whether `reclaimEndedItemLanes` should continue saving-and-resetting within-bound idle ended-item lanes (preserving the bytes, freeing the lane, and alerting via `drain-ended-work-saved`) or hold unexpected dirty idle lanes in place via `drain.lane-refused` and reserve `saveEndedItemWork` for known settlement transitions is left to the owner in [OQ-EW3](#OQ-EW3).

---

## 7. Failure and edge cases

| Case | Behavior |
| :--- | :--- |
| Saved edits later landed on `main` via squash commit | `trunk-contains` compares every path changed in `S^1..S` (and `S^1..S^2`) against `TrunkRef^{tree}`; matching blob OIDs (and absences) mark `refs/tillr/ended/<run>` for removal with no hand action. |
| Item is closed, `drop` is recorded, and `tillr item reopen` runs before `SweepRefs` | `planRefSweep` checks live item state first: because the item is active again, the ref is retained and `preservedFacts` cites it in the reopened worker's brief (`D-EW1`). |
| Save is `promote`d to a follow-up item, and that follow-up item is `closed` without landing | `custody-transferred` requires `target_item_id` to reach `landed` or `resolved-elsewhere`; `closed` leaves `refs/tillr/ended/<run>` retained (`awaiting new disposition`) so closing the follow-up item never silently drops the source bytes. |
| Operator pins `refs/tillr/held/<name>` at `S` | `custody-transferred` deletes `refs/tillr/ended/<run>` (`drain.ended-work-pruned`) while `refs/tillr/held/<name>` keeps commit `S` reachable in Git until removed by hand. |
| Crash after `git update-ref` deletes `refs/tillr/ended/<run>` and before `drain.ended-work-pruned` commits | Next `SweepRefs` sees an unpruned `drain.ended-work-saved` record whose ref is confirmed absent by `refAbsent` and appends `drain.ended-work-pruned`. |
| Caller runs `dispose-ended` with a stale or guessed SHA | `dispose-ended` checks `git rev-parse --verify refs/tillr/ended/<run>` against `--sha` and exits `1` without writing an event if they differ. |
| Test server left a UNIX domain socket or FIFO in the worktree | `os.Lstat` skips non-regular special files before `git add` runs and records their paths in `skipped_special` (`D-EW4`). |
| Non-Git charter (`completes: "hook"`, `l.nonLanding == true`) | `saveEndedItemWork` already returns `(false, nil)` immediately; none of the Git ref rules apply. |

---

## 8. The build, in slices that each land alone

Nothing in this document is built in this commit. Under [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21), slices wait on the owner's rulings on [OQ-EW1](#OQ-EW1), [OQ-EW2](#OQ-EW2), and [OQ-EW3](#OQ-EW3), and then land independently in this order (`D-EW6`):

| Slice | What lands | Depends on |
| ---: | :--- | :--- |
| 1 | Pre-add untracked enumeration and bounds in `snapshotWork` (`D-EW4`), the over-bound behavior ruled in [OQ-EW2](#OQ-EW2), idle-lane behavior ruled in [OQ-EW3](#OQ-EW3), and the `64`-entry SQLite event payload cap (`D-EW5`). | [`OQ-EW2`](#OQ-EW2), [`OQ-EW3`](#OQ-EW3) |
| 2 | `drain.ended-work-disposed`, `drain.ended-work-pruned`, `tillr drain dispose-ended` with SHA verification and actor enforcement from [OQ-EW1](#OQ-EW1), `SweepRefs` / `pruneRunRefs` ending predicates (`trunk-contains`, `custody-transferred`, `disposed-drop`), and `tools/tillr-drain/README.md` updates (`D-EW1`, `D-EW2`, `D-EW6`). | [`OQ-EW1`](#OQ-EW1) |
| 3 | Reopened-item recovery: `preservedFacts` cites unpruned `refs/tillr/ended/<run>` saves on active items and `promote`-linked target items (`D-EW1`, `D-EW2`). | Slice 2 |
| 4 | Standing watch rule `config/tillr/watch/drain-ended-work-saved.toml` grouped by `run_id` with `clear_when` on disposition or prune, plus watch rule tests (`D-EW3`). | Slice 2 |
| 5 | `tillr store inventory` `refs/tillr/ended/*` bucket and `tillr view` item-page rendering of ended-item saves and dispositions (`D-EW3`). | Slice 2 |

---

## 9. What this does not do

- **It does not run `git gc` or `git prune` inside `SweepRefs`.** Deleting `refs/tillr/ended/<run>` releases reachability so Git's own maintenance collects unreachable loose blobs without holding `land.lock` across object-pack repacking.
- **It does not let `tillr-watch` delete refs or clean worktrees.** All ref deletion stays in `tillr-drain` (`SweepRefs`).
- **It does not expire saves by age or count.**
- **It does not build any code, rule file, or CLI flag in this design commit.**

---

## 10. Alternatives considered and risks

| Alternative | Verdict |
| :--- | :--- |
| Delete `refs/tillr/ended/<run>` as soon as `tillr watch settle` settles the alert | Rejected (`D-EW1`): violates [`OQ-TW2`](./tillr-watch.md#OQ-TW2) / [`R-AW2`](./tillr-watch-alert-workflow.md#R-AW2), couples `tillr-drain` to `tillr-watch` tables, and conflates filing a root-cause defect item with discarding the saved work bytes. |
| Delete `refs/tillr/ended/<run>` once the item is closed and any read face has shown the save | Rejected (`D-EW1`): makes read-only inspection destructive if the reader pauses or escalates before pinning `refs/tillr/held/<name>` or promoting the diff to a follow-up item. |
| Measure snapshot size after `git add -A -- .` and skip `update-ref` if too large | Rejected (`D-EW4`): `git add -A -- .` has already hashed all `7,190` build files into `.git/objects/` before size is checked. |
| Group `drain-ended-work-saved` by `item_id` | Rejected (`D-EW3`): [`D-TW63`](./tillr-watch.md#D-TW63) auto-closes item-subject episodes on `closed` and `landed` items, which would auto-close every episode on the pass it opened. |

| Risk | Mitigation |
| :--- | :--- |
| A worker created legitimate new source files alongside `7,190` unignored build outputs | Pre-add `lstat` detects the bound violation before `git clean -fd`; under [OQ-EW2](#OQ-EW2) Option A the lane is held intact with `drain.lane-refused` so zero source files are wiped. |
| `trunk-contains` runs slow `git diff` calls inside the store write transaction | Changed-path blob comparisons against `TrunkRef^{tree}` run outside `s.WriteTx` while `land.lock` is held, bound to sampled `(S, TrunkRef)`, matching how `for-each-ref --merged` runs in `SweepRefs` today. |
| Truncating `files` at `64` in SQLite hides which directory held `7,190` files | `top_untracked_dirs` summarizes counts and bytes per top directory in the event, and the full JSON list remains in `git show -s --format=%B refs/tillr/ended/<run>` (`D-EW5`). |

---

## Open Questions

1. 💬 **OQ-EW1: Who may authorize `drop` on a `refs/tillr/ended/<run>` save whose bytes are not on the trunk?**

   When `trunk-contains` holds, `SweepRefs` deletes the redundant ref mechanically. When saved bytes are *not* on `main` — as with the `9` junk saves for closed items in the 2026-11-25 consumer repository — dropping the ref deletes the last copy of those bytes once `git gc` runs.

   - **A — Delegated inside tillr and to the operator with a commit-bound evidence packet; escalate ambiguous source work to the owner.** Triage / the design seat inside tillr (under [`R-OD1`](./operator-decisions-inside-tillr.md#1-the-ruling)) and the operator may record `dispose-ended --disposition drop --sha <S>` with `cause` and `evidence` when inspection establishes the save is build/test residue, outside-the-item scratch, or superseded by an explicit item closure; if inspection finds substantive unpublished source work whose value is ambiguous, the actor pins `refs/tillr/held/<name>` and asks you.
   - **B — Operator or owner only.** Seats inside tillr may inspect saves, file root-cause `.gitignore` / cleanup items, `promote` a save to a follow-up item, or `hold` it, and never record `drop`; only the operator or you may drop non-trunk saves.
   - **C — Owner only whenever bytes are absent from `main`.** Every non-redundant `refs/tillr/ended/<run>` waits for your explicit ruling before `drop`.

   <!-- vantage: question id=OQ-EW1 leaning="A: nine of thirteen saves in the 2026-11-25 case were routine junk on closed items that R-OD1 says should not require hand cleanup, while requiring --sha plus an evidence packet and escalating ambiguous source edits behind refs/tillr/held/ protects real work." -->

   _Leaning:_ A: nine of thirteen saves in the 2026-11-25 case were routine junk on closed items that `R-OD1` says should not require hand cleanup, while requiring `--sha` plus an evidence packet and escalating ambiguous source edits behind `refs/tillr/held/` protects real work.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-EW2: What should the drain do when non-ignored untracked files exceed the pre-add save bound (`256` files, `2 MiB` single file, or `16 MiB` total)?**

   Two saves in the consumer repository held ~`7,190` untracked build-output files each because a build wrote into an unignored directory. Pre-add `lstat` catches that before `git add` writes any loose blobs, leaving the choice of what to do with the dirty lane.

   - **A — Save tracked and staged edits, leave the untracked files on disk, and hold that lane (`drain.lane-refused`).** Tracked/staged changes are saved to `refs/tillr/ended/<run>`, `HEAD` is pinned, neither `.git/objects/` nor `.tillr/state.db` ingests the `7,190` files, `git clean -fd` does **not** run, and `drain-lane-refused` wakes triage to file the missing `.gitignore` or build-output fix and clean the lane. One lane sits out of rotation until cleaned; zero untracked source files can ever be silently wiped.
   - **B — Save tracked and staged edits, omit the oversized untracked set with a recorded directory summary, and `git clean -fd` the lane immediately.** Full lane capacity continues unattended and `.git/objects/` stays clean, at the risk of irreversibly deleting new untracked source files that happened to sit in the same worktree alongside unignored build output.
   - **C — Never include untracked files in `refs/tillr/ended/<run>` saves.** Save tracked and staged edits only and `git clean -fd` every ended-item lane. Simplest, and discards every new untracked file a worker created before its item ended.

   <!-- vantage: question id=OQ-EW2 leaning="A: D-JL33 rules that unexpected leftover work is an alert rather than a silent wipe, holding one lane leaves sibling lanes running, and any brand-new untracked source file sitting beside build output survives until inspected." -->

   _Leaning:_ A: `D-JL33` rules that unexpected leftover work is an alert rather than a silent wipe, holding one lane leaves sibling lanes running, and any brand-new untracked source file sitting beside build output survives until inspected.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-EW3: Should `reclaimEndedItemLanes` keep saving and resetting within-bound idle ended-item lanes once `drain-ended-work-saved` alerts on every save, or hold unexpected dirty idle lanes under `D-JL33`?**

   Commit `9cb58a6279` noted that `reclaimEndedItemLanes` sweeps idle ended-item lanes despite `D-JL33`. Once every `drain.ended-work-saved` event opens a `drain-ended-work-saved` watch episode (`D-EW3`) and respects the untracked bound (`D-EW4`), two policies are coherent:

   - **A — Keep bounded save-and-reset for ended items, and open `drain-ended-work-saved` on every save.** Within `D-EW4` bounds, the drain saves the bytes under `refs/tillr/ended/<run>`, frees the lane for the next item, and opens `drain-ended-work-saved` so the root cause is still investigated and settled under [`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2).
   - **B — Save-and-reset only on known item-ending transitions; hold unexpected dirty idle lanes.** `settleTerminalItem`, `abandonFromBase`, and orphaned workflow release save to `refs/tillr/ended/<run>` and reset the lane because the drain itself is ending that run; `reclaimEndedItemLanes` stops resetting unexpected dirty idle lanes and instead records `drain.lane-refused` until cleaned, matching `D-JL33` literally.

   <!-- vantage: question id=OQ-EW3 leaning="A: saving within-bound bytes into refs/tillr/ended/<run> and opening a mandatory-investigation watch episode preserves every byte, avoids stranding lanes on closed items, and still forces root-cause diagnosis instead of covering the fault." -->

   _Leaning:_ A: saving within-bound bytes into `refs/tillr/ended/<run>` and opening a mandatory-investigation watch episode preserves every byte, avoids stranding lanes on closed items, and still forces root-cause diagnosis instead of covering the fault.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Every row in this ledger is the designer's decision, made in response to the 2026-11-25 command-line request without new words from the owner, and the owner may overturn any of them ([`D-PR2`](../tillr/decision-provenance-review.md#6-how-this-stops-happening)). Governing owner rulings made in earlier documents (`D-JL33`, `D-TW48`, [`OQ-TW2`](./tillr-watch.md#OQ-TW2), `R-AW1` to `R-AW3`, [`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2), `R-OD1`, [`OQ-PR21`](../tillr/decision-provenance-review.md#OQ-PR21)) remain owned by those documents' ledgers and are cited in [§2](#2-governing-rulings).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-EW1 | **Designer's decision; the owner may overturn this.** Neither item closure alone, a bare read receipt, nor `tillr watch settle` alone deletes `refs/tillr/ended/<run>`; reopening an item (`Reopen` or `ReturnLanded`) blocks deletion of any surviving `refs/tillr/ended/<run>` and `preservedFacts` cites unpruned ended-item saves in the reopened item's brief. **Rejected:** delete on item closed, delete on read, delete inside `tillr-watch`, and age/count TTLs. | 2026-11-25, designer | [§3](#3-evaluating-the-candidate-triggers-for-ending-a-ref) | No |
| D-EW2 | **Designer's decision; the owner may overturn this.** `SweepRefs` (and `pruneRunRefs`) under `land.lock` is the sole deleter of `refs/tillr/ended/<run>`, deleting at expected-old SHA `S` and appending `drain.ended-work-pruned` when `!live[run]`, `!needed[run]`, the item is still finished or removed, and one of three facts holds: `trunk-contains` (changed paths match `TrunkRef^{tree}`), `custody-transferred` (`refs/tillr/held/*` or `refs/tillr/recovery/*` holds `S`, or a `promote`d `target_item_id` reached `landed` / `resolved-elsewhere`), or `disposed-drop` (`drain.ended-work-disposed` with `disposition = "drop"` and `sha == S` via `tillr drain dispose-ended`). | 2026-11-25, designer | [§4.1](#41-one-deleter-sweeprefs-under-landlock-d-ew2)–[§4.3](#43-recording-a-disposition-tillr-drain-dispose-ended-d-ew2) | No |
| D-EW3 | **Designer's decision; the owner may overturn this.** Ship standing watch rule `config/tillr/watch/drain-ended-work-saved.toml` grouped by `run_id` (never `item_id`, avoiding `D-TW63` immediate closure) with `clear_when` on `drain.ended-work-disposed` or `drain.ended-work-pruned` while preserving mandatory root-cause settlement under [`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2); report `refs/tillr/ended/*` explicitly in `tillr store inventory`, `tillr view`, `tillr drain sweep-refs --dry-run`, and `tools/tillr-drain/README.md`. | 2026-11-25, designer | [§4.4](#44-alerting-triage-and-read-surfaces-d-ew3) | No |
| D-EW4 | **Designer's decision; the owner may overturn this.** Before staging untracked files in `snapshotWork` (for both `ended/` and `preserved/`), enumerate `git ls-files --others --exclude-standard -z` and `lstat` entries without hashing blobs: skip non-regular special files (`skipped_special`) and enforce default configurable bounds of `256` untracked regular files, `2 MiB` (`2,097,152` bytes) per untracked file, and `16 MiB` (`16,777,216` bytes) total untracked bytes, while staging tracked modifications via `git add -u`. **Rejected:** post-`git add -A` size checks. | 2026-11-25, designer | [§5.1](#51-pre-add-untracked-enumeration-d-ew4) | No |
| D-EW5 | **Designer's decision; the owner may overturn this.** Keep the complete JSON file manifest in the `refs/tillr/ended/<run>` (and `refs/tillr/preserved/<run>`) commit message, and cap `files` and `index_files` in the SQLite event payload (`drain.ended-work-saved`, `drain.work-preserved`) at `64` entries plus `total_files`, `truncated_files`, `untracked_files`, `untracked_bytes`, and up to `8` `top_untracked_dirs` summaries. | 2026-11-25, designer | [§5.3](#53-bounding-the-sqlite-event-payload-while-keeping-the-full-manifest-on-the-commit-d-ew5) | No |
| D-EW6 | **Designer's decision; the owner may overturn this.** On upgrade, `SweepRefs` prunes existing `refs/tillr/ended/<run>` refs that match `trunk-contains` or `operator-ref-holds` and retains all other existing refs as `awaiting disposition` in `store inventory` and `sweep-refs --dry-run`; build lands in the five independent slices of [§8](#8-the-build-in-slices-that-each-land-alone) after [`OQ-EW1`](#OQ-EW1) to [`OQ-EW3`](#OQ-EW3) are ruled. | 2026-11-25, designer | [§4.5](#45-existing-refs-on-upgrade-d-ew6), [§8](#8-the-build-in-slices-that-each-land-alone) | No |
