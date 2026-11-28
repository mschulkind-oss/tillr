---
title: "One clean way to run only the rescue work, hold the rest, and put it all back"
status: in-review
stage: DESIGN
updated: 2026-11-28
tags:
  - tillr
  - drain
  - rescue
  - holds
  - repair
  - operator
  - design
summary: "Replaces the six manual workarounds from the 2026-11-27 queue rescue (--max-agents 0, closing and refiling to switch lane class, closing started items to stop verify, off-store operator notes, one-by-one repair selection, and raw SQLite queries) with a store-recorded rescue overlay (`tillr drain rescue` and `--end`, plus automatic `--until` clearing) and non-closing item holds (`tillr drain hold` and `unhold`) in `tools/tillr-drain`. Entering a rescue admits named items, red-trunk repairs, and their transitive split/blocker children, pauses non-admitted started runs without closing items or discarding candidates, snapshots any temporary lane-limit override, surfaces active movers in `tillr drain why` and `tillr view waiting`, and restores exact pre-rescue state on exit."
next: "Rule OQ-QR1 (whether the loop or mechanic enters whole-queue rescue automatically on a red-trunk threshold), OQ-QR2 (whether owner-facing express intake waits during a rescue by default), and OQ-QR3 (whether tillr item exposes post-filing class edits for filing mistakes); build Slices 1, 2 (with explicit express flags), 3, and 4 immediately under OQ-PR8, then Slices 5 and 6 as rulings land."
depends-on:
  - ./landing-principles.md
  - ../reference/authority-not-approval.md
  - ../reference/tillr-restart.md
  - ../reference/tillr-item-states.md
  - ../reference/gate.md
  - ../reference/gate.md#D-GR2
  - ../reference/gate.md#D-GR21
  - ./tillr-mechanic-escalation.md
  - ./tillr-mechanic-escalation.md#R-MC1
  - ./trunk-repair-on-moving-trunk.md
  - ./split-broken-trunk-drain-repair.md
  - ./operator-decisions-inside-tillr.md
  - ./tillr-live-drain-config.md
  - ./tillr-judge-and-lifecycle.md
  - ./tillr-graph-is-the-path.md
  - ./tillr-per-step-question-holds.md
  - ./tillr-vocabulary.md
  - ../tillr/decision-provenance-review.md
  - ../tillr/decision-provenance-review.md#OQ-PR8
  - ../tillr/decision-provenance-review.md#decision-ledger
related:
  - ./tillr-item-split.md
  - ./trunk-proof-per-test-flaky-judgment.md
vantage:
  status-chip: true
---

# One clean way to run only the rescue work, hold the rest, and put it all back

**Status:** 2026-11-28. Proposed; nothing in this document is built at `11d7df6566ca` (`rg -n 'drain_rescue|item_holds|ReasonRescueHeld|ReasonItemHeld' tools` returns nothing). Every proposed command, table, hold rule, restoration invariant, and slice boundary below (`D-QR1` through `D-QR7` in the [Decision Ledger](#decision-ledger)) is recorded from an operator request as **the designer's decision, not an owner ruling**, which the owner may overturn ([decision provenance review](../tillr/decision-provenance-review.md#6-how-this-stops-happening), [`OQ-PR21` and `OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger)). Only dated verbatim quotations of the owner's words are cited as owner rulings. No Go code or configuration is changed in this commit.

> **In short.** Give `tillr-drain` one reversible store overlay (`drain_rescue`) for *"only these items run"* and one non-closing per-item hold (`item_holds`) for *"park this item where it stands"*, built on top of `tillr-run`'s existing session pause (`RequestPause`) and `judge.go`'s existing evaluator pause (`errJudgePaused`). Entering a rescue (`tillr drain rescue --trunk-red` or `--item <id>`) admits the named or red-trunk repairs plus any split parts and blocking defects they create, pauses started non-admitted runs cleanly without closing items or spending attempts, records an auditable evidence packet and exact prior state in `.tillr/state.db`, and clears mechanically when its recorded `--until` condition (`trunk-green` or `set-finished`) holds — or immediately on `tillr drain rescue --end` — restoring every item, paused session, candidate, and lane setting to what stood before.

**Why it matters.** On 2026-11-27, while `30` landings accumulated past `refs/tillr/green` (`895c6cb31b66` to `83580e31504c`) with `61` failing `drain` tests across `16` causes ([split broken trunk drain repair](./split-broken-trunk-drain-repair.md#11-measurement-method-and-reproduction-commands)), isolating the queue so only trunk repairs ran took six workarounds: `--max-agents 0` (which would have blocked unstarted ordinary repairs), leaving repairs ordinary to avoid closing and refiling them into express, closing four started feature items to stop their runs (interrupting one in `verify` and requiring manual reopen later), keeping restore notes in an external operator file, picking repair items one by one, and running raw SQLite queries to see what was running, held, and waiting to resume.

**The shape.** Five connected parts inside `tools/tillr-drain` and `tools/tillr-view`:

- **One command to enter, adjust, and leave a queue rescue ([Section 2](#2-entering-and-leaving-queue-rescue-mode-with-exact-restoration), [`D-QR1`](#D-QR1)–[`D-QR3`](#D-QR3)).** `tillr drain rescue` enters an allow-set overlay (`--trunk-red`, `--item <id>`, and transitive split/blocker children), `tillr item request --rescue` or `tillr drain rescue --add/--remove` adjusts it while active, and `tillr drain rescue --end` (or automatic `--until` evaluation on each pass) ends it and restores exact pre-rescue state without touching `items.state`, `items.express`, or `items.rank`.
- **Non-closing holds on started and queued items ([Section 3](#3-holding-a-started-item-without-closing-it-and-resuming-cleanly), [`D-QR4`](#D-QR4)).** `tillr drain hold --item <id>` and rescue entry pause live worker and `verify`/`judge` sessions via `run.RequestPause` and `errJudgePaused`, release concurrency slots once child processes exit, preserve item worktrees, `judge.json` receipts, `gate.verdict` bindings, and `refs/tillr/runs/<run_id>` refs untouched, and resume from the exact node and candidate on `unhold` or rescue exit.
- **No need to mutate lane class for a rescue ([Section 4](#4-lane-class-after-filing-and-why-rescue-makes-class-switching-unnecessary), [`D-QR5`](#D-QR5), [`OQ-QR3`](#OQ-QR3)).** Admitted items draw directly from the ordinary concurrency pool while non-admitted ordinary items wait, keeping `[lanes] express` reserved for owner-facing latency work rather than repair workarounds.
- **Auditable store tables, mover-true `why`/`waiting` rows, and one status face ([Section 5](#5-store-records-mover-true-why-and-waiting-views-and-the-rescue-read-face), [`D-QR6`](#D-QR6), [`D-QR7`](#D-QR7)).** `drain_rescue`, `item_holds`, and five registered `drain.*` events persist actor, reason, evidence packet, admitted set, paused runs, and restore targets across restarts; `holdrules.go`, `tillr drain why`, `tillr view waiting`, and `tillr drain rescue --status` name the active mover inside tillr ([Landing Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)).
- **Measured check on automatic entry and settled rulings ([Section 6](#6-whether-the-loop-should-enter-rescue-mode-itself) and [Section 7](#7-check-against-landing-principles-and-authority-not-approval), [`OQ-QR1`](#OQ-QR1)).** Distinguishes non-stopping `[mechanic]` escalation ([`R-MC1`](./tillr-mechanic-escalation.md#R-MC1)) from whole-queue holds, states candidate numbers from the 2026-11-27 measurements, and audits all seventeen landing principles and the authority placement tests.

**Cost.** Two additive SQLite tables (`drain_rescue`, `item_holds`) and five event kinds in `tools/tillr-drain/internal/storelog`, one internal domain package `tools/tillr-drain/internal/rescue` (respecting `TestArchitecture`), planner/continuation guards in `plan.go` and `pass.go`, four `holdrules.go` constants, `--rescue` on `tillr item request`, and read surfaces in `tillr-drain` and `tillr-view`. Zero cost when no rescue or item hold is active.

**Start at:** [Section 2](#2-entering-and-leaving-queue-rescue-mode-with-exact-restoration) for the enter/leave commands and restoration proof, [Section 3](#3-holding-a-started-item-without-closing-it-and-resuming-cleanly) for how started runs pause and resume across all six in-flight sub-states, [Section 5](#5-store-records-mover-true-why-and-waiting-views-and-the-rescue-read-face) for store records and `why`/`waiting` output, and [Section 10](#10-open-questions) for the three owner questions.

**Needs your ruling:** [`OQ-QR1`](#OQ-QR1), [`OQ-QR2`](#OQ-QR2), and [`OQ-QR3`](#OQ-QR3) (plus seven designer's decisions [`D-QR1`](#D-QR1)–[`D-QR7`](#D-QR7) in the [Decision Ledger](#decision-ledger), which the owner may overturn).

**Reads with:** [landing principles](./landing-principles.md), [authority, not approval](../reference/authority-not-approval.md), [restarting tillr and pausing sessions](../reference/tillr-restart.md#pausing-a-session), [item states and frozen criteria](../reference/tillr-item-states.md), [the mechanic escalation design](./tillr-mechanic-escalation.md), [repairing a red trunk proof on a moving trunk](./trunk-repair-on-moving-trunk.md), [splitting the 2026-11-27 broken trunk drain repair](./split-broken-trunk-drain-repair.md), [operator decisions inside tillr](./operator-decisions-inside-tillr.md), and [live drain capacity and allocation changes](./tillr-live-drain-config.md).

**Words.** **Item**, **criterion**, **candidate**, **seat**, **verdict**, **mechanic**, **revert**, **green pointer**, and **whale** (**operator**) keep their definitions in [the vocabulary](./tillr-vocabulary.md#5-the-words) and [authority, not approval](../reference/authority-not-approval.md#terms). **Clearing fact** keeps its definition in [operator decisions inside tillr](./operator-decisions-inside-tillr.md) (a machine-checked condition — a named check green or a named item finished — that ends a hold without waiting on a clock or an operator check-in). Three terms are coined here:

- **Queue rescue** *(coined here)*: a temporary, store-recorded dispatch overlay (`drain_rescue`) in `tillr-drain` under which only an admitted set of items may start or advance, non-admitted started items pause without closing, and ending the rescue restores exact pre-rescue state. *Excludes:* permanent mutations to `items.state`, `items.express`, or `items.rank`, and scoped dependency pauses (`mechanic-pause` in [`tillr-mechanic-escalation.md`](./tillr-mechanic-escalation.md)), which hold only dependents of a named broken check or path while unrelated items keep running.
- **Admitted set** *(coined here)*: the union of items allowed to run during an active queue rescue — explicitly named `--item` IDs, items matching `--trunk-red` (`fixer-main-*`, `flaky-*`, `sync-remote-*`) when enabled, repairs filed with `tillr item request --rescue`, and their open transitive split children (`item_splits`) and blocking items (`item_edges` `blocked_by`). *Excludes:* unrelated queued or newly synced items.
- **Non-closing item hold** *(coined here)*: a store-recorded hold (`item_holds`, or a rescue hold on a non-admitted item) that parks an item's next dispatch or pauses its live run into `StatePaused` (`EndRestart`) while keeping `items.state` (`in-queue` or `not-started`), its item worktree, its `judge.json` or `gate.verdict`, and its pinned candidate ref intact. *Excludes:* `tillr item state --state closed`, which terminates work as `ReasonItemAlreadyTerminal` and resets the lane.

---

## 1. The 2026-11-27 Rescue and the Six Gaps in the Current Tree

### 1.1 Owner direction and provenance

The 2026-11-27 filed request records the owner's direction after an evening spent rescuing a queue whose trunk proof stayed red (quoted verbatim from the operator-filed request; because it states the owner's spoken direction in indirect speech rather than a verbatim quotation-marked transcript, it is recorded under `D-PR1` in [`docs/tillr/decision-provenance-review.md`](../tillr/decision-provenance-review.md#6-how-this-stops-happening) as an operator-recorded statement of the owner's direction rather than a closed ledger quote):

> "The owner said on 2026-11-27, after an evening spent rescuing a queue whose trunk would not prove: add an item to make this sort of intervention cleaner and easier; we should not have to go through contortions to rescue a queue."

### 1.2 Why each of the six 2026-11-27 contortions happened at `11d7df6566ca`

| # | Contortion on 2026-11-27 | Exact mechanism in the codebase at `11d7df6566ca` | What this design replaces it with |
| ---: | :--- | :--- | :--- |
| **1** | `tillr drain scale --max-agents 0` stopped new ordinary starts, and would have stopped unstarted repairs too | `Scale` (`tools/tillr-drain/drain/live_scale.go`) sets `drain_runtime_config.ordinary_lanes = 0`. In `tools/tillr-drain/drain/plan.go`, `ordinaryAgentSlotOpen(express, isCommand, runKey)` returns `true` for already-leased `activeOrdinaryAgents[runKey]`, letting already-started runs step forward, while refusing every unstarted ordinary item — including `fixer-main-*`, `flaky-*`, and `tillr item request --ordinary` repairs (`row.Express` is auto-set only for `item-idea-*` and `sync-remote-*`) — with `ReasonNoLane` (and holding paused runs via `reserveDrainingWork` and agent-mode `ActionJudge` on `ReasonNoLane`). | `tillr drain rescue` filters dispatch by membership in the **admitted set** rather than setting `ordinary_lanes = 0`, so admitted ordinary repairs start and advance across all ordinary slots while non-admitted ordinary items hold. |
| **2** | Switching a filed repair from ordinary to express meant closing and refiling it, throwing away its runs | `tillr item request` (`tools/tillr-item/request.go`, `tools/tillr-item/item/request.go`) writes `Express: !f.Ordinary` at filing time. `tools/tillr-item/main.go` exposes no subcommand or flag to edit `items.express` afterward, and closing + refiling creates a new `item_id` with zero run history. | Admitted repairs run in ordinary slots without needing `express = 1` ([Section 4](#4-lane-class-after-filing-and-why-rescue-makes-class-switching-unnecessary)); [`OQ-QR3`](#OQ-QR3) asks whether a small `tillr item class` command should also exist for genuine filing mistakes. |
| **3** | Four started items were closed by hand to stop them (interrupting one in `verify`), requiring manual reopen later | `Close` (`tools/tillr-item/item/lifecycle.go`) sets `items.state = 'closed'`, causing `stop.go` and `terminal_item.go` (`runEndedItemClosedKind`) to send `SIGTERM`, record `drain.orphaned`, reset the lane, and settle `ReasonItemAlreadyTerminal`. Meanwhile, `RequestPause` (`tools/tillr-run/run/pause.go`) and `errJudgePaused` (`tools/tillr-drain/drain/judge.go`) *already* pause worker and `verify` sessions cleanly, except `pass.go` immediately calls `runContinuation` on `StatePaused` unless `quotaPaused` holds it. | `tillr drain hold --item <id>` and `tillr drain rescue` issue `RequestPause` and keep `StatePaused` / `judge.json` parked in `pass.go` and `plan.go` until unheld — never closing the item or discarding its candidate ([Section 3](#3-holding-a-started-item-without-closing-it-and-resuming-cleanly)). |
| **4** | Nothing in the store recorded what changed for the rescue or what to restore | `Scale` overwrites `drain_runtime_config.ordinary_lanes` in place without linking to an intervention, and `Close` records `item.update` with no machine-restorable rescue group. The prior lane limit and the list of closed items lived only in operator scratch notes. | `drain_rescue` and `item_holds` persist `started_by`, `why`, `evidence_json`, `admitted_json`, `prior_lanes_json`, `paused_runs_json`, and `until_kind` in `.tillr/state.db` ([Section 5.1](#51-store-tables-and-registered-events-d-qr6)). |
| **5** | Repairs were filed and chosen one command at a time | No predicate grouped open trunk repairs (`fixer-main-*`, `flaky-*`, `sync-remote-*`) with operator-filed repair items (`C01`–`C16` in [`split-broken-trunk-drain-repair.md`](./split-broken-trunk-drain-repair.md)) and their split/defect children. | `--trunk-red`, repeatable `--item <id>`, `tillr item request --rescue`, and automatic transitive closure over `item_splits` and `blocked_by` edges name the entire repair frontier at once ([Section 2.2](#22-how-the-admitted-set-is-named-and-what-happens-to-newly-filed-items-d-qr2)). |
| **6** | No read face answered *"what is running, what is held for the rescue, and what will resume"* | `tillr drain why` and `tillr view waiting` had no rescue state or paused-run restore summary; the operator queried `.tillr/state.db` directly. | `tillr drain rescue [--status] [--json]`, `tillr drain why`, `tillr view waiting`, `tillr view status`, and the Observatory render active rescue metadata, running/queued repairs, mid-flight paused runs, held items, and the exact restore diff ([Section 5.3](#53-the-dedicated-rescue-read-face-and-observatory-banner-d-qr7)). |

### 1.3 Module boundary (`go-edges` invariant)

`tools/tillr-item` imports only `tillr` (`tools/tillr-item/go.mod`) and owns the durable definition and terminal/blocked lifecycle of an item (`not-started`, `in-queue`, `blocked`, `landed`, `resolved-elsewhere`, `closed`; [`docs/reference/tillr-item-states.md`](../reference/tillr-item-states.md)). `tools/tillr-drain` already imports `tillr-item`, `tillr-run`, and `tillr-land` (`tools/tillr-drain/go.mod`), and owns live dispatch controls (`scale`, `why`, `retry`, `clear-fault`, `stop`, `restart`) and `holdrules`. Placing both `tillr drain rescue` and `tillr drain hold` / `unhold` in `tools/tillr-drain` preserves `go-edges` without coupling `tillr-item` to `tillr-run` session pauses or polluting `items.state` with transient dispatch holds.

---

## 2. Entering and Leaving Queue Rescue Mode with Exact Restoration

### 2.1 Commands to enter, adjust, inspect, and leave (`D-QR1`)

*Designer's decision (`D-QR1`, from an operator request); the owner may overturn this.*

One subcommand family on `tillr drain` manages the whole-queue intervention, alongside one optional filing flag on `tillr item request`:

```sh
# 1. Enter queue rescue mode ("only these items run; pause the rest where they stand")
tillr drain rescue \
  --why <text> \
  [--by <actor>] \
  [--trunk-red] \
  [--item <id> ...] \
  [--lanes <N>] \
  [--keep-running] \
  [--hold-express | --allow-express] \
  [--until trunk-green | set-finished | manual]

# 2. File a new repair item directly into the active rescue's admitted set
tillr item request --rescue [--ordinary] --why <text> <words...>

# 3. Adjust the admitted set while a rescue is active
tillr drain rescue --add <id> [--add <id> ...] [--by <actor>] [--why <text>]
tillr drain rescue --remove <id> [--remove <id> ...] [--by <actor>] [--why <text>]

# 4. Read active rescue state, held/paused items, and what leaving will restore
tillr drain rescue [--status] [--json]

# 5. Leave queue rescue mode explicitly and restore exact pre-rescue state
tillr drain rescue --end [--by <actor>] [--why <text>]
```

Behavioral rules for entry and exit:

1. **Read-only bare invocation.** Running `tillr drain rescue` with no mutating flags is an alias for `tillr drain rescue --status` (exit `0`), so an operator checking whether a rescue is active never mutates the store by accident.
2. **Entry validation (exit `2` on usage refusal).** Entering requires `--why <text>` and at least one selector (`--trunk-red` or at least one `--item <id>`). Every `--item <id>` must exist in `items` and not already be terminal (`landed`, `resolved-elsewhere`, `closed`). If `--trunk-red` is passed without `--item` when `refs/tillr/green == refs/heads/<trunk>`, no trunk proof is red, and zero `fixer-main-*` / `flaky-*` items are open, entry refuses with exit `2` (`"trunk is already green at <sha> and no trunk repair items are open"`). If a rescue is already active (`ended_at IS NULL`), a second entry command refuses with exit `2` and prints the active `rescue_id` plus `--add`, `--remove`, `--status`, and `--end`.
3. **Default `--until` clearing fact ([Landing Principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), [Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act), [Principle 16](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr)).** When `--until` is omitted:
   - If `--trunk-red` is set, `until_kind` defaults to **`trunk-green`**: the loop clears the rescue automatically at the start of the first pass where `refs/tillr/green` equals `refs/heads/<trunk>`, no active trunk proof is still running on an older commit, and every explicitly `--item`-admitted repair has also reached `landed`, `resolved-elsewhere`, or `closed`.
   - If only `--item <id>` is set (without `--trunk-red`), `until_kind` defaults to **`set-finished`**: the loop clears the rescue automatically at the start of the first pass where every admitted item (including transitive split children) is in `landed`, `resolved-elsewhere`, or `closed`.
   - `--until manual` is opt-in only when an operator intentionally wants the rescue held across multiple repair waves until `tillr drain rescue --end` is run; as specified in [Section 5.2](#52-what-tillr-drain-why-and-tillr-view-waiting-show-d-qr7), `manual` surfaces `Class = "needs you"` once all admitted items finish so nothing ever stalls silently behind a vanished operator session.
4. **Optional `--lanes <N>` override.** If `--lanes <N>` (`N >= 1`) is supplied on entry (for example, raising or lowering ordinary concurrency specifically for a heavy 16-part repair wave), `drain_rescue` snapshots the prior `drain_runtime_config` row for `ordinary_lanes` into `prior_lanes_json` and writes the rescue target with `source = 'rescue'`. If `--lanes` is omitted, `ordinary_lanes` is untouched and all existing ordinary concurrency slots remain available exclusively to admitted items.
5. **Default pause vs `--keep-running`.** By default (`keep_running = 0`), entering a rescue pauses non-admitted live runs cleanly ([Section 3](#3-holding-a-started-item-without-closing-it-and-resuming-cleanly)) so their concurrency slots open immediately for repairs and their candidates do not land onto a broken trunk mid-rescue. Passing `--keep-running` (`keep_running = 1`) lets currently running non-admitted nodes finish their current node before parking, without starting any new non-admitted items or next nodes.

### 2.2 How the admitted set is named and what happens to newly filed items (`D-QR2`)

*Designer's decision (`D-QR2`, from an operator request); the owner may overturn this.*

The request asks how the rescue set is named across three possibilities — chosen items, a mark on the item, and everything linked to the red trunk — and what happens to items filed while the rescue is in force:

| Way to name the set | Role in this design | Why |
| :--- | :--- | :--- |
| **1. Chosen items (`--item <id>`, `--add <id>`, `--remove <id>`)** | **Included (`admitted_json.explicit_items`).** Names any existing queued, blocked, or in-flight item IDs. | Covers hand-filed cause repairs (`C01`–`C16` in [`split-broken-trunk-drain-repair.md`](./split-broken-trunk-drain-repair.md)) or urgent hotfixes whose IDs do not start with `fixer-main-`. |
| **2. Everything linked to the red trunk (`--trunk-red`)** | **Included (`admitted_json.trunk_red = true`).** Dynamically admits every open `fixer-main-*` (`TrunkFixerPrefix`), `flaky-*` (`FlakyRepairPrefix`), and `sync-remote-*` (`IsRemoteSyncItem`) item, including ones the heavy runner or marker sweep files *after* rescue entry. | Eliminates hand-picking generated trunk fixers or missing a second `fixer-main-<sha>` filed when a proof finishes mid-rescue. |
| **3. Transitive split parts and blockers (automatic closure)** | **Always included.** Any open split child (`item_splits` from `FileSplit`) of an admitted item, and any open blocker (`item_edges` `blocked_by`, including `tooling` defects filed by `fileDefectItem`) of an admitted item, is admitted automatically. | Prevents deadlock when an admitted repair splits into 16 per-cause parts or files a blocking tool defect; zero manual `--add` calls needed. |
| **4. A mark on the item (`tillr item request --rescue` vs a persistent column on `items`)** | **`--rescue` flag at filing appends to `drain_rescue.admitted_json`; no persistent `items.rescue` column.** (`tillr item request --rescue` refuses with exit `2` if no rescue is active.) | A persistent boolean column on `items` would have to be scrubbed on exit and could drift if an item were closed and reopened later; appending the new `item_id` into the active `drain_rescue` row in `.tillr/state.db` keeps membership scoped strictly to that rescue episode. |

What happens to **items filed while a rescue is in force**:

- **Unrelated ordinary requests and document syncs (`tillr item request` without `--rescue`, `tillr item sync`):** Created normally in `items` (`not-started` / `in-queue`) at their normal queue rank, and held in place with `ReasonRescueHeld` (`rescue-held`) until the rescue ends. Zero filings are refused or lost.
- **Repairs filed for the rescue (`tillr item request --rescue ...`):** Created in `items` and atomically appended to the active rescue's `admitted_json.explicit_items` (`drain.rescue-updated`), dispatching immediately in queue order.
- **Generated trunk repairs (`fixer-main-*`, `flaky-*`, `sync-remote-*`) and child splits/defects of admitted items:** Admitted automatically when `--trunk-red` is active (or via transitive closure from an admitted parent) and dispatched without operator action.
- **Owner-facing express intake (`item-idea-*` idea intake and Vantage design-comment runs):** Governed by [`OQ-QR2`](#OQ-QR2), with `--hold-express` and `--allow-express` on `tillr drain rescue` overriding the default per episode.

### 2.3 Proof that leaving restores exactly what entering changed (`D-QR3`)

*Designer's decision (`D-QR3`, from an operator request); the owner may overturn this.*

Exact restoration holds by construction because **entering a rescue is a read-side planner overlay plus reversible session pauses, never a destructive rewrite of `items` rows**. Every piece of state entering can touch has a deterministic inverse on `--end` (or automatic `--until` exit):

| State surface | Before `tillr drain rescue` | Changed on rescue entry | Restored on `tillr drain rescue --end` (or `--until` clear) | Mid-rescue mutation edge case |
| :--- | :--- | :--- | :--- | :--- |
| **`items.state` (`not-started`, `in-queue`, `blocked`, `closed`, ...)** | Each item's lifecycle state | **Untouched** (never set to `closed` or `blocked`) | **Exact match** (nothing to rewrite; items that landed *during* the rescue stay `landed`) | If an operator explicitly ran `tillr item state --state closed` on an item mid-rescue, it remains `closed` (`ReasonItemAlreadyTerminal`). |
| **`items.express`, `items.rank`, `items.waiting_on`, `criteria`** | Stored class, owner queue order, blockers, frozen/unfrozen criteria | **Untouched** | **Exact match** | New items filed mid-rescue keep their filed `rank` and `express` bit. |
| **`drain_runtime_config` (`key = 'ordinary_lanes'`)** | Absent, or `{value_json, source, updated_by}` | Untouched if `--lanes` omitted; if `--lanes N` given, prior row saved in `prior_lanes_json` and row updated with `source = 'rescue'` | If `prior_lanes_json.changed == true` **and** current `source == 'rescue'`, restores exact prior `{value_json, source, updated_by}` (or deletes the row if `present == false`) and emits `drain.config-changed` | If an operator ran `tillr drain scale --max-agents M` *during* the rescue, `source` became `'operator'`; `--end` sees `source != 'rescue'`, preserves `M`, and records `"lanes_kept_operator_override": M` in `restored_json`. |
| **Pre-existing single-item holds (`item_holds`)** | Any item held individually via `tillr drain hold` before the rescue | **Untouched** (`item_holds` is a separate table from `drain_rescue`) | **Exact match** — items in `item_holds` remain held under `item-held` / `item-paused` until their own `unhold` or `--until` clears | An item unheld via `tillr drain unhold` mid-rescue drops its `item_holds` row and remains `rescue-held` only until the rescue ends. |
| **Non-admitted live runs paused on entry (`paused_runs_json`)** | Running in `runs` (`StateRunning`) on node `N`, attempt `A`, item worktree `W`, candidate `H` | Paused via `RequestPause` into `StatePaused` (`EndRestart`) or `judge.json` receipt; lane row freed to `idle`; recorded in `paused_runs_json` | Rescue overlay lifts (`ended_at` set); on the next pass (`signalPassWake`), `plan.go` and `pass.go` resume each paused run from node `N`, attempt `A`, worktree `W`, and candidate `H` in queue order | If the trunk was repaired while a gated candidate waited (`RecordedTrunkRepair`), `preRebaseRepairedTrunk` rebases/re-gates only as existing trunk-repair rules require. |
| **Active rescue record (`drain_rescue`)** | No row with `ended_at IS NULL` | Inserts 1 active row (`ended_at = NULL`) + `drain.rescue-entered` event | Sets `ended_at`, `ended_by`, `end_reason`, `restored_json` + emits `drain.rescue-ended` | Full history of past rescues remains queryable in `drain_rescue` and `events`. |

---

## 3. Holding a Started Item Without Closing It and Resuming Cleanly

### 3.1 Standalone single-item hold and unhold commands (`D-QR4`)

*Designer's decision (`D-QR4`, from an operator request); the owner may overturn this.*

Problem 3 of the 2026-11-27 rescue also occurred *before* the evening rescue: three started items had been closed and reopened earlier the same day because tillr had no command to hold one started item. The standalone pair uses the exact same non-closing pause mechanism as `drain_rescue`:

```sh
# Hold one queued or started item where it stands without closing it
tillr drain hold --item <id> --why <text> [--by <actor>] [--until manual | item:<blocker-id> | check:<check-id>]

# Release the hold and let the item resume from its saved node and candidate
tillr drain unhold --item <id> [--by <actor>] [--why <text>]
```

- When `--until item:<blocker-id>` or `--until check:<check-id>` is given, `tillr-drain` clears the `item_holds` row automatically on the first pass where `<blocker-id>` is `landed`/`resolved-elsewhere` or `<check-id>` is green on the trunk, emitting `drain.item-unheld` (`by: "loop"`, `reason: "until cleared"`).
- When `--until` is omitted on `tillr drain hold`, it defaults to `manual` (an explicit operator park of one named item, surfaced as `Class = "needs you"` in `why`/`waiting` with the exact `tillr drain unhold --item <id>` command as its mover action).
- Holding an item already in `landed`, `resolved-elsewhere`, or `closed` refuses with exit `2` naming its terminal state. Holding an already-held item updates `why` / `until` idempotently without re-signaling an already-paused run.

### 3.2 What happens to a started item's live run, lane, and candidate across all six in-flight sub-states (`D-QR4`)

In `tillr-drain`, an item is held whenever `itemHeld(itemID)` is true — either an active `item_holds` row names `itemID`, or an active `drain_rescue` row exists and `itemID` is outside its admitted set. Depending on where the started item stands at the moment the hold takes effect, `tillr-drain` handles its **live run**, **lane**, and **candidate** as follows:

| In-flight sub-state when held | What happens to the **live run** | What happens to the **lane & worktree** | What happens to the **candidate** | How it **resumes** when unheld / rescue ends |
| :--- | :--- | :--- | :--- | :--- |
| **1. Live worker, scout, or parallel writer/reader node (`!evaluatorNode`)** | `tillr-drain` calls `run.RequestPause(store, runID, by, why)` (`tools/tillr-run/run/pause.go`), writing `run.pause-request`. Per [`docs/reference/tillr-restart.md#pausing-a-session`](../reference/tillr-restart.md#pausing-a-session): `pi` ends at the next turn/tool boundary (`stops-at-a-gap`), `claude` stops with `SIGTERM` keeping its session transcript (`stops-where-it-stands`), and `agy`/`muse` stop cleanly (`cannot-resume`). The run segment ends in `StatePaused` with `EndRestart` and writes `pause-notice`. Zero attempts, refusals, or verdicts are charged ([Landing Principle 5](./landing-principles.md#5-only-verdicts-charge)). | Once OS child processes exit (`lane-run-children-live` check), the `lanes` concurrency lease transitions to `idle` so an admitted repair item can use the agent slot immediately. The item's directory (`<lane-root>/<item-id>/worktree`, plus any parallel `<lane-root>/<item-id>/nodes/<node-key>/worktree` checkouts) stays **untouched on disk** (including uncommitted edits for `stops-at-a-gap` / `stops-where-it-stands`). `collectIdleWorktrees` and `sweepRefs` exempt held/paused items. | Any committed head on `tillr/<scope>/<item-id>` (and `refs/tillr/item-nodes/<item-id>/<node-key>` for parallel pieces) stays pinned. `pass.go` checks `!l.itemHeld(itemID)` alongside `!quotaPaused` so `runContinuation` does **not** fire while held. | On release, `plan.go` re-claims an ordinary agent slot in queue order and `pass.go` runs `l.runContinuation` on the **same attempt, node, and worktree** (`restartParallel` reuses already-passed parallel siblings). `pi` and `claude` resume their saved session IDs; `agy`/`muse` start a fresh turn on the preserved worktree with `pause-notice` context. |
| **2. Live `verify`, `judge`, or compliance evaluator node (`evaluatorNode`)** *(the exact 2026-11-27 case)* | `tillr-drain` calls `run.RequestPause`. Inside `tools/tillr-drain/drain/judge.go`, the existing `errJudgePaused` branch catches `StatePaused`, writes `judge.json` (`writeJudgeReceipt`) binding the evaluator graph state and candidate SHA without recording a pass or fail verdict, and returns `errJudgePaused` to `pass.go`. (If `eval.output` already landed on disk in the race window before `RequestPause` arrived, `judge.go` records that completed verdict normally and holds at Sub-state 4/5 instead of throwing away completed work.) | `pass.go` calls `releaseExamination`, freeing the concurrency/judge slot to `idle` while keeping `<lane-root>/<item-id>/worktree` at the built candidate commit. | `refs/tillr/runs/<run_id>` and `judge.json` remain pinned to the built candidate tree. The item stays `in-queue` (never `closed`, never `eval-failed`). | On release, `plan.go` schedules `ActionJudge` from `judge.json` against the **already-built candidate commit** — never re-running `plan`, `build`, or earlier read-only evaluator steps that already completed. |
| **3. Between workflow nodes (for example `plan` passed, `build` not yet started)** | No live OS process exists; completed `node_runs` rows and node output artifacts under `<loop-dir>/<run>/artifacts` remain recorded. | Lane slot is already `idle`; `<lane-root>/<item-id>/worktree` stays at `HeadSHA` / `NodeBaseSHA`. | Intermediate commits on the item branch stay pinned. `plan.go` skips dispatching the next node and emits `ReasonRescueHeld` or `ReasonItemHeld`. | On release, `plan.go` dispatches the next workflow node directly from the saved frontier commit and predecessor artifacts. |
| **4. Detached pre-merge fast gate in flight (`work.Examining`, `tillr-gate`)** | A detached fast gate (`D-GR43`, zero LLM tokens, seconds of local CPU) is **not** killed mid-check: killing a deterministic local check seconds before `gate.done.json` wastes work already paid for. It finishes writing `gate.done.json` and `gate.verdict`, then parks before `Land`. | Examination slot releases as soon as `gate.done.json` settles; item worktree stays intact. | `gate.verdict` binds `(base_sha, head_sha^{tree})` and `refs/tillr/runs/<run_id>` pins `head_sha` ([Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)). If the gate failed on a red-trunk check, existing `settleTrunkAlreadyRed` holds it behind the fixer; if it failed on its own bug, the refusal records and holds before worker return. | On release, a passing candidate advances directly to Sub-state 5 (`Land`) without a second gate (unless a trunk-repair marker requires rebase; see Sub-state 5). A refused candidate returns to its worker session with the saved gate evidence. |
| **5. Gated, waiting to land** | No live agent run; candidate holds right before `landWithActions`. | Lane slot is `idle`. | `gate.verdict` and `refs/tillr/runs/<run_id>` remain pinned. No non-admitted commit touches `refs/heads/<trunk>` while the rescue is in force. | On release, `tools/tillr-land` (`Trunk.Squash`) three-way merges `base_sha..head_sha` onto the new trunk tip **without re-gating** ([Landing Principle 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) & [Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)). Only if `RecordedTrunkRepair` moved past `base_sha` does `preRebaseRepairedTrunk` rebase and re-gate so a pre-repair regression cannot re-enter; only on an actual `squash-conflict` does the candidate return to its worker. |
| **6. Inside `land.lock` (`Trunk.Squash` + `Trunk.Move`)** | Plumbing merge-tree and ref CAS take milliseconds with no agent process. | Not interrupted. | Finishes publishing atomically (`refs/tillr/landed/<run_id>`); `landings` receipt writes normally. | Already `landed`; nothing left to hold or resume. |

### 3.3 Bounded pause timeout when a subprocess ignores signals

If a worker subprocess inside Sub-state 1 or 2 does not exit within `roll.pause_timeout_seconds` (default `120 s` in `.tillr.toml`, [`docs/reference/tillr-restart.md#current-values`](../reference/tillr-restart.md#current-values)) after `RequestPause`, `tillr-run` escalates to process-group termination of that run's own recorded PGID (never pattern-based `pkill`), still recording `StatePaused` with `EndRestart` and `pause-notice` so the item remains `in-queue` and resumable rather than closed or faulted.

---

## 4. Lane Class After Filing, and Why Rescue Makes Class-Switching Unnecessary

### 4.1 Why rescue removes the need to switch `ordinary` to `express` (`D-QR5`)

*Designer's decision (`D-QR5`, from an operator request); the owner may overturn this.*

On 2026-11-27, the operator wanted to move filed repairs from `ordinary` to `express` **only** because `tillr drain scale --max-agents 0` shut down the ordinary pool (`ordinaryAgentSlotOpen` in `tools/tillr-drain/drain/plan.go` exempts `express == true` from `ordinary_lanes`). Using `items.express` as a rescue bypass has three structural defects:

1. **Wrong pool size and wrong purpose.** `.tillr.toml` sets `[lanes] express = 2` for low-latency owner-facing requests and ideas, whereas a multi-cause trunk rescue (`16` independent items in [`split-broken-trunk-drain-repair.md`](./split-broken-trunk-drain-repair.md)) wants all ordinary agent capacity (`--lanes` / `ordinary_lanes`), not a 2-slot side channel.
2. **State pollution if not reverted.** Mutating `items.express = 1` on ten queued items to get them past `--max-agents 0` permanently alters their stored item records unless another script flips them back.
3. **Generated items start ordinary.** Both `fixer-main-*` and `flaky-*` are created with `express = 0`; requiring class flips to rescue a red trunk means fighting tillr's own generated repair items on every incident.

Under `tillr drain rescue`, `plan.go` checks rescue admission **before** checking `ordinaryAgentSlotOpen`: admitted ordinary items use the full `ordinary_lanes` concurrency pool while non-admitted ordinary items wait in `rescue-held` / `rescue-paused`. No repair item ever needs `items.express` changed to run during a rescue.

### 4.2 Whether `tillr item` should still allow post-filing class edits outside rescues ([`OQ-QR3`](#OQ-QR3))

Outside rescues, an operator or the owner occasionally files a request with `--ordinary` and later decides it should use `[lanes] express`, or files a large batch without `--ordinary` and wants it moved to `ordinary` so express lanes stay free. In `tools/tillr-item/item/apply.go`, `carryCriteria` freezes only judged criteria ([`docs/reference/tillr-item-states.md`](../reference/tillr-item-states.md#what-freezes)), not `items.express`. Because exposing a new user-visible `tillr item` mutation touches queue priority policy, [`OQ-QR3`](#OQ-QR3) asks the owner whether to keep `items.express` fixed at filing (Option A) or add `tillr item class --id <id> --express|--ordinary --why <text>` for non-terminal items (Option B). Nothing in Slices 1–4 depends on [`OQ-QR3`](#OQ-QR3).

---

## 5. Store Records, Mover-True `why` and `waiting` Views, and the Rescue Read Face

### 5.1 Store tables and registered events (`D-QR6`)

*Designer's decision (`D-QR6`, from an operator request); the owner may overturn this.*

`tools/tillr-drain/internal/storelog` registers two additive tables via `store.RegisterDDL("drain-rescue", RescueDDL)` so a loop restart, a second operator session, or the owner can inspect the entire intervention and its restoration contract from `.tillr/state.db` alone:

```sql
CREATE TABLE IF NOT EXISTS drain_rescue (
  rescue_id         TEXT PRIMARY KEY,
  started_at        INTEGER NOT NULL,
  started_by        TEXT NOT NULL,
  why               TEXT NOT NULL,
  trigger_kind      TEXT NOT NULL CHECK (trigger_kind IN ('operator', 'mechanic', 'auto-threshold')),
  evidence_json     TEXT NOT NULL,
  admitted_json     TEXT NOT NULL,
  until_kind        TEXT NOT NULL CHECK (until_kind IN ('trunk-green', 'set-finished', 'manual')),
  until_target_json TEXT NOT NULL,
  keep_running      INTEGER NOT NULL DEFAULT 0 CHECK (keep_running IN (0, 1)),
  express_policy    TEXT NOT NULL CHECK (express_policy IN ('hold', 'allow-intake', 'allow-all')),
  prior_lanes_json  TEXT NOT NULL,
  paused_runs_json  TEXT NOT NULL,
  ended_at          INTEGER,
  ended_by          TEXT,
  end_reason        TEXT,
  restored_json     TEXT
);

-- At most one rescue can be active at any instant.
CREATE UNIQUE INDEX IF NOT EXISTS drain_rescue_one_active
  ON drain_rescue ((1)) WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS item_holds (
  item_id       TEXT PRIMARY KEY,
  held_at       INTEGER NOT NULL,
  held_by       TEXT NOT NULL,
  why           TEXT NOT NULL,
  until_kind    TEXT NOT NULL CHECK (until_kind IN ('manual', 'item', 'check')),
  until_target  TEXT NOT NULL DEFAULT '',
  paused_run_id TEXT NOT NULL DEFAULT '',
  evidence_json TEXT NOT NULL
);
```

Structured JSON payloads recorded atomically on `WriteTx` before any `RequestPause` signal is sent ([authority, not approval](../reference/authority-not-approval.md#placing-an-operation)):

- **`evidence_json` (delegated/mechanical evidence packet):**
  ```json
  {
    "actor": "whale",
    "operation": "drain.rescue.enter",
    "policy": "explicit operator red-trunk rescue",
    "records": {
      "green_sha": "895c6cb31b66f63751f8bc2cb3cce17a8b3f85e5",
      "trunk_tip_sha": "83580e31504c4033c1c50677b3c4d407dfc29465",
      "failing_checks": ["go-test-tillr-drain"],
      "admitted_items": ["item-request-c01", "item-request-c02"]
    },
    "expected_effect": "run only admitted repairs and their split/blocker children; pause 1 live non-admitted run; hold 14 queued non-admitted items",
    "reversal_path": "automatic when trunk-green holds, or `tillr drain rescue --end`"
  }
  ```
- **`admitted_json`:** `{"trunk_red": true, "explicit_items": ["..."], "removed_items": []}` (transitive split parts from `item_splits` and `blocked_by` targets from `item_edges` are computed live in SQL/planner reads against `explicit_items` + `trunk_red` minus `removed_items`, so newly filed splits are admitted on the very pass they appear).
- **`prior_lanes_json`:** `{"changed": false}` when `--lanes` was omitted, or `{"changed": true, "present": true, "value_json": "4", "source": "operator", "updated_by": "agent"}` when `--lanes` overrode `ordinary_lanes` (plus extending `RuntimeConfigSchema`'s `source` `CHECK` list to `'operator'`, `'migrated'`, `'rescue'`).
- **`paused_runs_json`:** array of `{"item_id": "...", "run_id": "...", "attempt": 1, "node_id": "verify", "seat": "verify", "candidate_sha": "...", "pause_mode": "evaluator-receipt"}` updated as each live non-admitted run settles into `StatePaused` or `judge.json`.
- **`restored_json`:** written on exit (`{"lanes_restored_to": 4, "resumed_runs": ["..."], "released_queued_items": 14, "kept_item_holds": ["..."]}`).

Five event kinds are registered in `tools/tillr-drain` (`event.Register(..., Utility)`):

1. `drain.rescue-entered` (`object_type = "drain-rescue"`, `object_id = <rescue_id>`)
2. `drain.rescue-updated` (`object_type = "drain-rescue"`, `object_id = <rescue_id>`, for `--add`, `--remove`, `tillr item request --rescue`, and `paused_runs_json` settlements)
3. `drain.rescue-ended` (`object_type = "drain-rescue"`, `object_id = <rescue_id>`, recording `ended_by`, `end_reason`, and `restored_json`)
4. `drain.item-held` (`object_type = "item"`, `object_id = <item_id>`)
5. `drain.item-unheld` (`object_type = "item"`, `object_id = <item_id>`)

### 5.2 What `tillr drain why` and `tillr view waiting` show (`D-QR7`)

*Designer's decision (`D-QR7`, from an operator request); the owner may overturn this.*

Four exported `Reason*` constants are added to `tools/tillr-drain/drain` and `tools/tillr-drain/holdrules/holdrules.go` (enforced by `TestEveryReasonConstantHasAHoldRule` in `tools/tillr-drain/drain/hold_rules_test.go` and `TestWaitingEveryDrainReasonHasARule` in `tools/tillr-view/waiting_internal_test.go`):

| Exported constant | Reason string | Default `holdrules.Rule` (`Class`, `Where`, fallback `Mover`) | When emitted by `plan.go` / `why.go` |
| :--- | :--- | :--- | :--- |
| `ReasonRescueHeld` | `"rescue-held"` | `{"waits on work", "queue", "the active rescue repairs to finish and prove the trunk green, or tillr drain rescue --end"}` | Non-admitted item with no paused run (unstarted, between nodes, or gated waiting to land) while `drain_rescue` is active. |
| `ReasonRescuePaused` | `"rescue-paused"` | `{"waits on work", "lane", "the active rescue repairs to finish and prove the trunk green; resumes its paused session on exit"}` | Non-admitted item whose live worker or `verify`/`judge` run was paused into `StatePaused` or `judge.json` by the active rescue (`Where = "judge"` when `judge.json` is held, `"gate"`/`"land"` when gated). |
| `ReasonItemHeld` | `"item-held"` | `{"needs you", "queue", "run tillr drain unhold --item <item>, or wait for its recorded --until target"}` | Queued or between-node item held by a standalone `item_holds` row. |
| `ReasonItemPaused` | `"item-paused"` | `{"needs you", "lane", "run tillr drain unhold --item <item>, or wait for its recorded --until target; resumes its paused session"}` | Started item whose live worker or `verify`/`judge` run is paused by a standalone `item_holds` row. |

**Dynamic mover accuracy under [Landing Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) and [Principle 16](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr):**

A static string in `holdrules.Rules` serves `--history` snapshots, while live `why.go` and `waiting.go` resolve each held item's concrete `Class`, `Why`, and `Mover` from the live `drain_rescue` / `item_holds` state so **no held item ever names a mover that cannot act**:

1. **Rescue with active clearing condition (`trunk-green` or `set-finished`) and moving/ready repairs:**
   - `Class`: `"waits on work"`
   - `Why`: `"held by rescue-20261128-01 (by whale: isolate 16 drain trunk repairs); node verify paused at candidate <sha>"` (or `"queued at rank 4"` for unstarted items)
   - `Mover`: names the concrete live mover inside tillr — for example, `"rescue-20261128-01: 3 admitted repairs (item-request-c01 running, 2 queued) + trunk proof to reach trunk-green"` (or `"heavy trunk proof on <tip>"` once all repairs have landed and the proof is running).
2. **Escalation when every remaining admitted item is itself blocked on `owner` (or when `until_kind = 'manual'` and zero admitted items remain active):**
   - If the admitted repair set cannot advance without the owner (for example, the sole remaining repair hit `conflict-owner` / `question-open`), or if an operator chose `--until manual` and all admitted work is already done, the rescue cannot clear itself mechanically.
   - `why.go` and `waiting.go` automatically shift the held items' `Class` from `"waits on work"` to **`"needs you"`** and rewrite `Mover` to name the real blocker: `"owner (admitted repair <id> awaits owner ruling on <question>)"` or `"run tillr drain rescue --end (all admitted items finished under --until manual)"`. Thus a disconnected operator session never leaves items masquerading as `"waits on work"` behind an actor that is gone ([Landing Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)).
3. **Standalone `item_holds` with `--until item:<blocker>` or `--until check:<check>`:**
   - When `<blocker>` is open and moving (or `<check>` has an open `fixer-main-*` / `flaky-*`), `Class` is `"waits on work"` and `Mover` names `"item <blocker> to land"` or `"repair <fixer-id> to turn <check> green"`.
   - When `until_kind == "manual"` (or the named check has no open repair), `Class` is `"needs you"` and `Mover` is `"run tillr drain unhold --item <id>"`.

### 5.3 The dedicated rescue read face and Observatory banner (`D-QR7`)

`tillr drain rescue` (or `tillr drain rescue --status [--json]`) answers in one screen the exact three questions the operator had to write SQL for on 2026-11-27 — **what is running**, **what is held**, and **what leaving will restore**:

```text
RESCUE: active (rescue-20261128-01)
  Started:    2026-11-28T01:12:04Z by whale (trigger: operator)
  Why:        let 16 split drain trunk repairs prove trunk green without feature churn
  Evidence:   green=895c6cb31b66 tip=83580e31504c failing=[go-test-tillr-drain]
  Admitted:   --trunk-red + 16 explicit items (+ transitive splits/blockers)
  Clears on:  trunk-green (refs/tillr/green == refs/heads/main and 0 open fixer-main-*)

ADMITTED WORK (2 running, 5 queued, 9 landed):
  RUNNING  item-request-c03   lane-1   build   run-item-request-c03-a1-n2
  RUNNING  item-request-c04   lane-2   build   run-item-request-c04-a1-n1
  QUEUED   item-request-c05 .. item-request-c09 (5 items)

PAUSED IN FLIGHT (2 items — will resume saved node & candidate on exit):
  PAUSED   item-request-feat1  verify (judge.json saved)  candidate=639d0c92  run-item-request-feat1-a1-n3
  PAUSED   item-request-feat2  build  (pi session saved)  worktree=dirty      run-item-request-feat2-a1-n2

HELD IN QUEUE (12 unstarted/between-node items):
  rescue-held: 12 ordinary items (ranks 18..29); pre-existing item-held: 1 (item-request-parked1)

RESTORE ON EXIT (`tillr drain rescue --end` or automatic on trunk-green):
  ordinary_lanes: restore 4 -> 2 (saved operator target before rescue --lanes 4)
  resume runs:    2 paused runs (item-request-feat1 at verify, item-request-feat2 at build)
  release queue:  12 rescue-held items (item-request-parked1 stays item-held)
```

The same structured summary appears as a top banner in `tillr drain why`, `tillr view status`, and the Observatory home/status header (`tools/tillr-view`) whenever `drain_rescue` has an active row (`ended_at IS NULL`), and prints `"rescue: none active"` on `tillr drain rescue --status` when idle.

---

## 6. Whether the Loop Should Enter Rescue Mode Itself

### 6.1 What [`R-MC1`](./tillr-mechanic-escalation.md#R-MC1) and [`OQ-PR20`](../tillr/decision-provenance-review.md#decision-ledger) already settle — and where they pull apart

Two dated verbatim owner rulings frame automatic action on a red trunk:

1. **[`R-MC1`](./tillr-mechanic-escalation.md#R-MC1) (2026-11-27, owner):**
   > *"we should also have a fixer agent with real abilities that can be in a escalation chain for fixing when we hit some thresholds. auto fixed thresholds that don't bother me and stop things are just fine. just don't want it to stop progress."*
2. **[`OQ-PR20`](../tillr/decision-provenance-review.md#decision-ledger) / [`OQ-GP7`](./tillr-graph-is-the-path.md#decision-ledger) (2026-11-18, owner):** There is no general landing cap while a check is failing on the trunk; work that does not touch what the failing check reads keeps landing, and work that touches it waits for the repair (*"why does a red check hold a queue?"*).

Notice the exact line between [`tillr-mechanic-escalation.md`](./tillr-mechanic-escalation.md) and `tillr drain rescue`:

- Escalating a stuck repair to `SeatMechanic` at fixed thresholds (`non_converging_refusals = 5`, `distinct_failing_causes = 4`, `unproved_trunk_landings = 10` in [`tillr-mechanic-escalation.md` §3](./tillr-mechanic-escalation.md#3-fixed-thresholds-in-tillrtoml-and-live-readings)) and letting the mechanic pause *only* dependents of a broken check (`mechanic-pause`) **stops no unrelated progress**, so [`R-MC1`](./tillr-mechanic-escalation.md#R-MC1) already authorizes it.
- Entering **whole-queue `drain_rescue`**, by contrast, deliberately pauses and holds *all* non-admitted ordinary items across the repository so trunk movement stops outpacing repair proofs (the 2026-11-27 moving-trunk loop in [`trunk-repair-on-moving-trunk.md` §1.1](./trunk-repair-on-moving-trunk.md#11-measured-deferrals-on-2026-11-27-01550352)). Because holding unrelated items *does* pause ordinary feature progress in exchange for faster trunk recovery, and because recognizing a new mechanical trigger class is a one-time policy decision reserved to the owner ([`docs/reference/authority-not-approval.md`](#72-check-against-authority-not-approval)), **this design does not turn on automatic whole-queue rescue without the owner's ruling in [`OQ-QR1`](#OQ-QR1)**.

### 6.2 Measured numbers from 2026-11-27 for candidate thresholds

If the owner chooses automatic or mechanic-mediated rescue entry in [`OQ-QR1`](#OQ-QR1), every number in the candidate trigger comes from the two 2026-11-27 incidents rather than an unmeasured guess:

| Observable store/git metric | Normal red-proof recovery | 2026-11-27 deferral loop ([`trunk-repair-on-moving-trunk.md`](./trunk-repair-on-moving-trunk.md#11-measured-deferrals-on-2026-11-27-01550352)) | 2026-11-27 multi-cause stall ([`split-broken-trunk-drain-repair.md`](./split-broken-trunk-drain-repair.md#11-measurement-method-and-reproduction-commands)) | Candidate `[rescue]` threshold if [`OQ-QR1`](#OQ-QR1) chooses auto-entry |
| :--- | :--- | :--- | :--- | :--- |
| **Unproved commits ahead of `refs/tillr/green`** (`git rev-list --count refs/tillr/green..HEAD`) | `1`–`6` commits (1 proof window) | Grew from **`4` to `20` landings** across `4` deferrals (`117 min`) | Reached **`30` landings** (`895c6cb31b66..83580e31504c`) over `9+ hours` | **`auto_unproved_landings = 20`** (`[mechanic]` escalates earlier at `10` without holding the queue) |
| **Non-converging repair verdicts on open `fixer-main-*` (or proof deferrals without green)** | `0`–`2` before fix or auto-revert lands | **`4` consecutive proof deferrals** with zero landed repair | **`15` failed `fixer-main-81cd910659bb` candidates** (`38` proof test failures / `61` suite failures across `16` causes) | **Compound guard: `auto_repair_refusals = 5` (or `auto_proof_deferrals = 3`) alongside `unproved_landings >= 20`** |

Why a **compound** threshold (`unproved_landings >= 20` **and** open trunk repair failing to converge for `5` verdicts or `3` proof deferrals, excluding `disabled` flaky checks per [`D-GR21`](../reference/gate.md#D-GR21)) beats a single counter: on a healthy high-throughput day where 20 fast doc/code items land during one 18-minute heavy proof that is about to pass, zero fixers have failed, so a compound rule never pauses a healthy busy drain. [`OQ-QR1`](#OQ-QR1) puts all three policy choices — manual/operator-only, mechanic-mediated, and compound auto-threshold — to the owner.

---

## 7. Check Against Landing Principles and Authority, Not Approval

### 7.1 Check against all seventeen principles in [`docs/design/landing-principles.md`](./landing-principles.md)

| Principle | What it already settles for this design | How this design conforms |
| :--- | :--- | :--- |
| **[§1. A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict)** | Holding an unrelated item for a rescue says nothing about its candidate and must never record it as failed or refused. | Pausing/holding writes `StatePaused` (`EndRestart`) or `judge.json` and emits `ReasonRescueHeld` / `ReasonRescuePaused` — never a lander or gate refusal, and never consuming standing. |
| **[§2. Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)** | When gated candidates resume after repairs advance the trunk, `Trunk.Squash` three-way merges `base..head` without comparing `git patch-id`. | Gated candidates held in Sub-state 5 land via `Trunk.Squash` onto the post-rescue tip without patch-id checks. |
| **[§3. Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once)** ([`D-GR2`](../reference/gate.md#D-GR2)) | A candidate whose pre-merge fast gate already passed is not gated a second time merely because other commits landed while it waited. | Detached fast gates in flight (Sub-state 4) finish `gate.done.json`, bind `gate.verdict`, and land via `Trunk.Squash` on rescue exit without re-gating (re-gating only if `preRebaseRepairedTrunk` rebases across a trunk repair). |
| **[§4. Every outcome is state-changing](./landing-principles.md#4-every-outcome-is-state-changing)** | A held item must not spin in repeated `(candidate, tip, reason)` passes. | Planner skips held items cleanly before dispatch and suppresses `runContinuation` until `drain_rescue` or `item_holds` changes state (`idle.go` does not replan in a loop). |
| **[§5. Only verdicts charge](./landing-principles.md#5-only-verdicts-charge)** ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | Parks and pauses never consume an item's standing or count worker endings. | `StatePaused` (`EndRestart`) resumes on the **same `attempt` number** (`runContinuation`) with zero refusal records added. |
| **[§6. A budget escalates, it does not loop](./landing-principles.md#6-a-budget-escalates-it-does-not-loop)** ([`D-JL9`](./tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9), [`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | Admitted repairs still obey `repeatedEnding` -> `design` (and `[mechanic]` escalation if built); pauses never synthesize repeat keys. | Admitted items run the normal workflow and escalation ladder; `pause-notice` segments are excluded from `repeatedEnding`. |
| **[§7. A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)** | Every item held by a rescue or single-item hold must appear as held, with its reason and mover, on `waiting` and `why` — never hidden as `closed`. | Directly fixes the 2026-11-27 false-closed workaround: all four `Reason*` constants have `holdrules.go` rules and render in `why`, `waiting`, `rescue --status`, and Observatory. |
| **[§8. A gate that cannot answer must say which](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)** | Gate `error` / `stalled` stays distinct from `fail`. | Untouched; Sub-state 4 preserves whatever `gate.done.json` outcome `tillr-gate` wrote. |
| **[§9. Fix the machinery; never hand-carry the work](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work)** | Rescue items must still pass their workflow, judge, gate, and `tillr-land` — never hand-landed past the proof. | Rescue mode restricts *which* items dispatch; it waives zero judge, compliance, gate, or lander checks on admitted items. |
| **[§10. Supersession is a verdict, not a stall](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall)** | If a rescue repair already fixed what a held item was going to change, that held item resolves cleanly as `already-on-trunk` on resume. | Untouched; when a held item resumes and squashes empty onto the repaired tip, `settleAlreadyOnTrunk` marks it `resolved-elsewhere`. |
| **[§11. A failure is diagnosed, not waited out](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)** | Neither rescue entry nor rescue exit may rely on wall-clock durations, TTLs, or cooldown timers; work resumes only against a **named change**. | Rescue and `item_holds` have **zero TTL or timer flags**; automatic exit checks named state transitions (`trunk-green`, `set-finished`, `item:<id>`, `check:<id>`). |
| **[§12. A principle cites the measurement that produced it](./landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)** | Design choices and candidate thresholds must cite observed store/git measurements. | Grounded in the 2026-11-27 six-workaround rescue, the `4`-deferral / `20`-landing loop ([`trunk-repair-on-moving-trunk.md`](./trunk-repair-on-moving-trunk.md)), and the `30`-landing / `61`-failure / `16`-cause split ([`split-broken-trunk-drain-repair.md`](./split-broken-trunk-drain-repair.md)). |
| **[§13. A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)** | A held item must never name a vanished session or non-existent repair lane as its mover; if the mover stalls on the owner, the surface must shift to say so. | Default `--until` (`trunk-green` / `set-finished`) lets the loop clear the rescue itself; `why`/`waiting` name the live admitted repairs + proof, and automatically shift `Class` to `"needs you"` (`owner`) if all remaining repairs block on `owner` ([Section 5.2](#52-what-tillr-drain-why-and-tillr-view-waiting-show-d-qr7)). |
| **[§14. Convict a run on what it did, not on what happened while it ran](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)** | Pausing a run or running rescue repairs in sibling item worktrees while a paused worktree sits on disk must never convict either run of a boundary violation. | Paused item worktrees are idle and untouched by sibling runs; `CompareRunObserved` (`tools/tillr-drain/internal/boundary/boundary.go`) convicts only writes a run itself makes into a watched idle sibling worktree. |
| **[§15. Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change)** | Each build slice must work alone and leave the trunk no worse. | Sequenced into six small, independently landable slices ([Section 9](#9-build-slices)), starting with single-item `hold`/`unhold` + pause retention in Slice 1. |
| **[§16. What the operator can settle is settled inside tillr](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr)** | Exiting a rescue once repairs prove the trunk green is deterministic and must happen inside `tillr-drain`, not wait for the operator's next check-in. | `evaluateRescueUntil` runs at the top of every loop pass and restores pre-rescue state automatically when `trunk-green` or `set-finished` holds, even if the operator session disconnected hours earlier. |
| **[§17. Disable a flaky check, file its repair, and count it neither green nor blocking](./landing-principles.md#17-disable-a-flaky-check-file-its-repair-and-count-it-neither-green-nor-blocking)** ([`D-GR21`](../reference/gate.md#D-GR21)) | Contradicted (`flaky`) checks are disabled and excluded from blocking the trunk; rescues are for deterministic red checks, while open `flaky-*` repairs still join `--trunk-red` if desired. | `trunk-green` reads `refs/tillr/green == refs/heads/<trunk>` (which `disabled` checks already allow to advance under `D-GR21`) and zero open `fixer-main-*` items — so a disabled flaky check never traps the queue inside a rescue. |

### 7.2 Check against Authority, Not Approval

| Rule in [`authority-not-approval.md`](../reference/authority-not-approval.md) | What it already settles for this design | Placement in this design |
| :--- | :--- | :--- |
| **Four owner-only classes** (irreversible loss, spending/commitment, external publication, policy/risk acceptance) | Pausing a run without deleting unpublished edits, holding queue items via a reversible overlay, and restoring saved `ordinary_lanes` destroy nothing, spend nothing, publish nothing externally, and waive no gate check. Conversely, **setting standing policy** on whether the loop may freeze unrelated ordinary items automatically is a policy/product trade-off reserved to the owner. | Entering/ending a rescue is **not** owner-only per instance; recognizing an automatic whole-queue rescue policy **is** put to the owner once in [`OQ-QR1`](#OQ-QR1), Express intake policy in [`OQ-QR2`](#OQ-QR2), and post-filing item class mutation in [`OQ-QR3`](#OQ-QR3). |
| **Test 1 — Mechanical** (outcome fixed by settled policy + checkable recorded facts, both outcomes defined, reproducible from stored IDs) | Once a rescue or `item_holds` row is active with `--until trunk-green`, `set-finished`, `item:<id>`, or `check:<id>`, checking whether that predicate holds on pass `P` is 100% deterministic over Git refs and SQLite rows. | **Mechanical:** the loop evaluates `until_kind` at the start of every pass, writes `restored_json` + `drain.rescue-ended` / `drain.item-unheld`, and resumes held work with nobody asked. |
| **Test 2 — Delegated** (reversible, bounded in damage, within policy, evidence packet written *before or atomically with* the action) | The whale (`operator`) entering, updating, or ending a rescue (or holding one item) when a deterministic red trunk stalls progress is reversible via `--end` / `unhold` and bounded to dispatch scheduling. | **Delegated:** `tillr drain rescue` and `tillr drain hold` write `evidence_json` (actor, operation, policy/why, primary record hashes/IDs, expected effect, reversal path) inside the same SQLite `WriteTx` **before** issuing `RequestPause`. |
| **Test 3 — Owner & Refusal of `"Please run X"`** | The owner is never asked *"Please run `tillr drain rescue ...`"* or *"Please reopen items X, Y, Z"*; owner questions carry at least two real options with consequences, reversibility, evidence, and a recommendation. | Every operational command runs directly by `whale` or `tillr-drain`; [`OQ-QR1`](#OQ-QR1)–[`OQ-QR3`](#OQ-QR3) ask one-time policy questions with real options and leanings. |
| **Independence of judgment (`self-judging`)** | Neither `rescue` nor `hold` records a criterion verdict or alters `checkIndependence`. | Resumed `verify`/`judge` runs keep their independent judge session and `judged_actor` check unchanged. |

---

## 8. Edge Cases, Failure Paths, and What Is Deliberately Not Done

### 8.1 Completeness walk — the holes, by name

1. **Degenerate inputs:**
   - *Zero open repairs on `--trunk-red` alone when trunk is already green:* refused at CLI entry with exit `2`. If the trunk was red with an active proof when `--trunk-red` entered and that in-flight proof passes 5 seconds later with zero open `fixer-main-*`, the very next pass sees `trunk-green` satisfied and exits the rescue cleanly (`restored_json` restores everything immediately).
   - *Admitted item closes or resolves elsewhere mid-rescue:* `set-finished` and `trunk-green` treat `landed`, `resolved-elsewhere`, and `closed` as finished terminal states, so closing a superseded repair (`fixer-main-81cd910659bb` in [`split-broken-trunk-drain-repair.md` §4](./split-broken-trunk-drain-repair.md#4-whether-fixer-main-81cd910659bb-should-continue)) never traps the rescue open.
   - *Duplicate `--add <id>` or `hold --item <id>`:* idempotent inside `WriteTx`.
2. **Crash and timeout recovery:**
   - *Process crash between committing `drain_rescue` in SQLite and calling `run.RequestPause`:* on the next loop pass (or after loop restart), `reconcileHeldRuns` scans leased runs whose `item_id` satisfies `itemHeld(itemID)` and lacks a `run.pause-request`, and calls `RequestPause` idempotently.
   - *Loop restart (`tillr-drain restart` or `keep`) while a rescue is active:* `drain_rescue` and `item_holds` live in `.tillr/state.db`. Under `resolveStartupLaneCount` (`tools/tillr-drain/drain/live_scale.go`), `drain_runtime_config` (`source = 'rescue'`) survives in-place restarts, `paused_runs_json` runs stay paused until the rescue ends, and only admitted items dispatch after restart.
3. **Concurrency and ordering interactions:**
   - *Simultaneous CLI invocations (`rescue` + `scale`, or two operator shells):* serialized by `store.WriteTx` and enforced by the `drain_rescue_one_active` unique partial index.
   - *`tillr drain scale --max-agents M` run explicitly during an active rescue:* overwrites `drain_runtime_config.ordinary_lanes` with `source = 'operator'`. On rescue exit, restoration checks `source == 'rescue'` before restoring `prior_lanes_json`, so the operator's newer `M` is preserved rather than clobbered.
   - *Quota pause (`backend_windows` / `quotaPaused`) overlapping a rescue:* both guards compose via logical OR in `pass.go` (`!quotaPaused && !l.itemHeld(itemID)`): ending a rescue does not force-resume a run whose backend quota window is still closed, and reopening a quota window does not resume a run whose item is still held.
4. **Existing state on ship day:**
   - Additive `CREATE TABLE IF NOT EXISTS` migration in `storelog` (`drain-rescue`) plus widening `drain_runtime_config`'s `source` `CHECK` constraint (via standard SQLite table-rebuild migration if an existing store already created `drain_runtime_config` with the two-value check, tested against seeded pre-migration stores). Existing stores start with zero rows in `drain_rescue` and `item_holds` and identical planner behavior.
5. **Single-writer discipline:**
   - `tillr-drain` is the sole writer of `drain_rescue`, `item_holds`, and the five `drain.rescue-*` / `drain.item-*` events (`tillr item request --rescue` delegates to the store helper or appends a `rescue_admit_request` record only when an active rescue row exists; to keep `go-edges` strict so `tillr-item` never imports `tillr-drain`, `tillr-cli` or `tillr-item` checks/updates the `drain_rescue` row through a minimal shared schema contract in `tools/tillr` or `tillr drain rescue --add` — Slice 2 keeps all `drain_rescue` SQL in `tools/tillr/rescuecontract` or `tillr-cli` orchestration so `go-edges` stays clean).

### 8.2 Alternatives considered and rejected

| Alternative | Verdict |
| :--- | :--- |
| **Keep using `tillr drain scale --max-agents 0` plus flipping repairs to `express`** | **Rejected.** Conflates concurrency capacity with queue filtering, forces `items.express` mutations on ordinary and generated repairs, caps repairs at `[lanes] express = 2`, and forgets prior state on exit. |
| **Add `"held"` or `"paused"` to `items.state` in `tillr-item`** | **Rejected.** Violates `go-edges` / lifecycle separation ([`docs/reference/tillr-item-states.md`](../reference/tillr-item-states.md)): every consumer of `items.state` (`sync`, `view`, `item`, `land`) would have to learn a transient dispatch sub-state, and holding an item would overwrite whether it was `not-started` vs `in-queue`. |
| **Close started items on rescue entry and auto-reopen them on exit** | **Rejected.** Closing records false `drain.orphaned` / `run-ended-item-closed` events, throws away live `verify` progress, resets item worktrees, and violates [Landing Principle 7](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report). |
| **Add a wall-clock `--for 2h` TTL on `tillr drain rescue` or `hold`** | **Rejected.** Directly violates [Landing Principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out): a timer either expires while the trunk is still red (restarting the pileup) or keeps holding work after the trunk is green. `--until trunk-green` and `--until set-finished` clear on the exact named fact. |

---

## 9. Build Slices

*Designer's sequencing decision (`D-QR7`, from an operator request); the owner may overturn this.* Each slice can be filed as a separate item, lands as a small self-contained functional change ([Landing Principle 15](./landing-principles.md#15-land-the-smallest-functional-change)), obeys `TestArchitecture` in `tools/tillr-drain/architecture_test.go` (placing new non-test domain `.go` files in `tools/tillr-drain/internal/rescue/` with a `// tillr-arch` `doc.go`, and keeping `tools/tillr-drain/drain/` edits inside existing files or thin `*_bridge.go` / `*_aliases.go` wrappers), and runs `just format` and `just pack-sync` (plus layer-2 `README.md` / `SKILL.md` sync where new CLI faces land) in the same commit. Under [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8):

- **Slices 1, 3, and 4 depend on zero open questions** and can be built immediately.
- **Slice 2** can be built immediately with explicit `--hold-express` / `--allow-express` flags (defaulting conservatively to `--allow-express` for `item-idea-*`/comments until [`OQ-QR2`](#OQ-QR2) is answered, or waiting on [`OQ-QR2`](#OQ-QR2) for the flagless default).
- **Slice 5** waits on [`OQ-QR1`](#OQ-QR1), and **Slice 6** waits on [`OQ-QR3`](#OQ-QR3).

1. **Slice 1 — Non-closing single-item holds (`item_holds`, `tillr drain hold` / `unhold`, worker + `verify` pause retention, and `why`/`waiting` rules; zero `OQ` blockers).**
   - Add `item_holds` DDL and `drain.item-held` / `drain.item-unheld` events in `tools/tillr-drain/internal/storelog` and `tools/tillr-drain/internal/rescue`.
   - Implement `tillr drain hold --item <id> --why <text> [--by <actor>] [--until manual|item:<id>|check:<id>]` and `tillr drain unhold --item <id>`, issuing `run.RequestPause` on live runs, suppressing `runContinuation` and new node/judge/land dispatches in `pass.go` and `plan.go` while held, preserving `judge.json` (`errJudgePaused`) and `gate.verdict`, and auto-clearing `item:<id>` / `check:<id>` targets on pass start.
   - Add `ReasonItemHeld` and `ReasonItemPaused` to `holdrules.go`, `why.go`, `waiting.go`, `tools/tillr-drain/README.md`, and `yolo-packs/tillr/skills/tillr-operating/SKILL.md`.
   - *Tests:* targeted tests in `tools/tillr-drain/drain` and `tools/tillr-view` covering holding a live worker run, holding a live `verify` run (`judge.json` preserved and resumed without re-running `build`), holding a gated candidate before `Land`, automatic `--until item:<id>` clearing, `TestEveryReasonConstantHasAHoldRule`, and `TestWaitingEveryDrainReasonHasARule`.
2. **Slice 2 — Queue rescue overlay (`drain_rescue`, `tillr drain rescue` enter / `--add` / `--remove` / `--end`, `tillr item request --rescue`, and exact state restoration; default express policy governed by [`OQ-QR2`](#OQ-QR2)).**
   - Add `drain_rescue` DDL (with `drain_rescue_one_active` unique partial index and `drain_runtime_config` `'rescue'` source migration) and `drain.rescue-entered`, `drain.rescue-updated`, `drain.rescue-ended` events.
   - Implement `tillr drain rescue` (`--trunk-red`, `--item`, `--lanes`, `--keep-running`, `--hold-express` / `--allow-express`, `--until`, `--add`, `--remove`, `--end`) and `tillr item request --rescue`, computing transitive admission over `item_splits` and `item_edges` (`blocked_by`), pausing non-admitted live runs via Slice 1's mechanism, snapshotting `prior_lanes_json`, and restoring `ordinary_lanes` (iff `source == 'rescue'`) plus paused runs on `--end` while preserving pre-existing `item_holds`.
   - Add `ReasonRescueHeld` and `ReasonRescuePaused` to `holdrules.go`, `why.go`, and `waiting.go` (including dynamic mover escalation to `"needs you"` when admitted repairs block on `owner`).
   - *Tests:* targeted tests in `tools/tillr-drain/drain` verifying exact before/after restoration of `items` columns, `ordinary_lanes` (both untouched and mid-rescue `scale` override), pre-existing `item_holds`, transitive split/blocker admission, newly filed items (`--rescue` vs unrelated), and crash recovery of `reconcileHeldRuns`.
3. **Slice 3 — Mechanical `--until trunk-green` and `--until set-finished` auto-exit on pass start (waits on Slice 2; zero `OQ` blockers).**
   - Evaluate `until_kind` at the start of every `tillr-drain` pass: exit and restore automatically when `trunk-green` (`refs/tillr/green == refs/heads/<trunk>`, no active proof on an older commit, and explicit admitted items terminal) or `set-finished` (all admitted items + transitive splits terminal) holds, ignoring `disabled` flaky checks per [`D-GR21`](../reference/gate.md#D-GR21).
   - *Tests:* targeted tests in `tools/tillr-drain/drain` proving unattended auto-restoration when repairs land and advance `refs/tillr/green` with no operator session attached, and proving `disabled` flaky markers do not hold a `trunk-green` rescue open.
4. **Slice 4 — Read face `tillr drain rescue [--status] [--json]` and rescue banners in `why`, `status`, and Observatory (waits on Slice 2; zero `OQ` blockers).**
   - Implement `tillr drain rescue [--status] [--json]` printing intervention metadata, evidence packet, running/queued admitted items, paused mid-flight runs (node, candidate SHA, resume mode), held queue counts, and the exact restore diff, plus top banners in `tillr drain why`, `tillr view status`, and the read-only Observatory status header.
   - *Tests:* targeted CLI/JSON and view tests in `tools/tillr-drain` and `tools/tillr-view`.
5. **Slice 5 — Automatic or mechanic-mediated rescue entry (conditional on [`OQ-QR1`](#OQ-QR1)).**
   - If [`OQ-QR1`](#OQ-QR1) chooses Option B or Option C, wire `[rescue]` thresholds in `.tillr.toml` (`tools/tillr/settings`) or add mediated `tillr drain rescue` authority to `SeatMechanic` with `trigger_kind` recorded on `drain_rescue`. (No work if [`OQ-QR1`](#OQ-QR1) chooses Option A.)
6. **Slice 6 — Post-filing `tillr item class` convenience command (conditional on [`OQ-QR3`](#OQ-QR3)).**
   - If [`OQ-QR3`](#OQ-QR3) chooses Option B, add `tillr item class --id <id> --express|--ordinary --why <text>` in `tools/tillr-item` (`item.update` event) for non-terminal items. (No work if [`OQ-QR3`](#OQ-QR3) chooses Option A.)

---

## 10. Open Questions

1. 💬 **OQ-QR1: Should the drain ever enter whole-queue rescue mode on its own when a red trunk is not converging, or enter only when the operator (or mechanic) invokes it?**

   On 2026-11-27, you said automatic fixed thresholds are fine when they escalate repairs without bothering you or stopping other progress. Escalating a stuck repair to a stronger repair agent keeps unrelated items moving, whereas whole-queue rescue deliberately pauses all unrelated ordinary items so new commits stop piling onto a broken trunk (on 2026-11-27, unproved landings grew to `20` across `4` deferred proofs, and later to `30` across `15` failed repair attempts).

   - **A — Explicit operator command only (`tillr drain rescue`).** Automatic thresholds escalate stuck repairs to stronger repair and split seats while keeping unrelated items running; whole-queue rescue happens only when the operator runs `tillr drain rescue`, and still clears itself automatically when the trunk turns green.
   - **B — Enter automatically on a compound red-trunk stall threshold in `.tillr.toml`, and exit automatically when the trunk is green.** When unproved commits past the green pointer reach `20` **and** an open trunk repair has failed `5` candidate verdicts (or `3` consecutive trunk proofs have deferred repair without proving green), the loop enters `trunk-red` rescue on its own, pauses unrelated ordinary runs, and restores everything automatically once `refs/tillr/green` catches up.
   - **C — Let the repair mechanic seat invoke `tillr drain rescue --trunk-red` (with `--until trunk-green`), with no count trigger in the loop itself.** Ordinary thresholds escalate to the mechanic without stopping the queue, and the mechanic enters a temporary `trunk-green` rescue only when its log diagnosis shows concurrent landings are actively breaking or deferring the trunk proof.

   <!-- vantage: question id=OQ-QR1 leaning="A (or C once the mechanic seat lands) — keep automatic thresholds non-stopping by escalating to split and mechanic first, and reserve pausing unrelated ordinary items for an explicit operator or mechanic diagnosis, with automatic restoration on trunk-green." -->

   _Leaning:_ **A** now (and **C** once the mechanic seat is built) — automatic non-stopping escalation plus fixing moving-trunk proof deferrals removes most pileups without freezing unrelated work, while keeping `tillr drain rescue` one clean command away whenever the operator or mechanic sees concurrent landings interfering with recovery. Choose **B** if you want an unattended safety valve at `20` unproved landings + `5` failed repair attempts even when no operator session is watching.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-QR2: While a queue rescue is active, should owner-facing express intake (new ideas and Vantage design comments) keep running by default or wait with the rest of the queue?**

   During a rescue, `--hold-express` and `--allow-express` let the operator override behavior per episode, and this question sets the default when neither flag is passed. Letting idea intake (`item-idea-*`) and Vantage design-comment replies run keeps your interactive document feedback responsive in seconds and touches only Markdown files, whereas holding them stops every non-repair commit on the trunk completely until the rescue clears.

   - **A — Keep owner-facing express intake (`item-idea-*` and Vantage design comments) running by default, while holding express code/build requests unless `--rescue` or `--allow-express` is passed.** Answering your comments and triaging ideas stays live during a red-trunk code rescue without running competing code builds or Go test gates; `--hold-express` still pauses everything when total trunk stillness is wanted.
   - **B — Hold every non-admitted item, including ideas and Vantage design comments, by default unless `--allow-express` is passed.** `"Only these items run"` means literally zero non-admitted commits or agent runs of any kind until the rescue ends, at the cost of your design comments waiting in `rescue-held` until the trunk is green.

   <!-- vantage: question id=OQ-QR2 leaning="A — let owner-facing Markdown intake (ideas and Vantage comments) keep answering by default while holding all non-admitted build and code requests, with --hold-express available for total trunk stillness." -->

   _Leaning:_ **A** — Vantage comment replies and idea intake touch only docs, never trigger heavy Go test deferrals, and keep the system responsive to you during a multi-hour code repair, while `--hold-express` remains available whenever a rescue targets document checks themselves.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-QR3: Outside of rescues, should `tillr item` add a command to switch an existing open item between `ordinary` and `express`?**

   Because `tillr drain rescue` runs admitted repairs directly in the ordinary pool while holding non-admitted ordinary items, rescues no longer need to flip items from `ordinary` to `express`. Outside of rescues, however, an item filed with `--ordinary` by mistake (or a batch filed as `express` by default that is crowding the express slots) still has no CLI command to flip its lane class without closing and refiling it.

   - **A — Keep lane class fixed at filing (add no new `tillr item` subcommand).** Rescues use `tillr drain rescue`, urgent blockers already inherit express standing through dependency edges, and `tillr-item`'s surface stays smaller.
   - **B — Add `tillr item class --id <id> --express | --ordinary --why <text>` for any non-terminal item.** Updates `items.express` in place and records an `item.update` event while preserving all prior runs, attempts, and criteria intact.

   <!-- vantage: question id=OQ-QR3 leaning="B — add a small tillr item class command for non-terminal items so genuine filing mistakes never tempt anyone to close and refile an item with run history." -->

   _Leaning:_ **B** — even though rescues no longer need it, a ten-line `tillr item class` mutation on non-terminal items removes the last reason anyone would ever close and refile an item just to change its lane pool.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Per [`decision-provenance-review.md` §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening), every row `D-QR1` through `D-QR7` below is **the designer's decision (from an operator request), not an owner ruling**, and the owner may overturn any of them. Pre-existing owner rulings cited in [Section 6](#6-whether-the-loop-should-enter-rescue-mode-itself) and [Section 7](#7-check-against-landing-principles-and-authority-not-approval) ([`R-MC1`](./tillr-mechanic-escalation.md#R-MC1), [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8), [`OQ-PR20`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger), [`OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger), [`D-GR2`](../reference/gate.md#D-GR2), [`D-GR21`](../reference/gate.md#D-GR21), `D-JL9`, `D-JL10`, `R-OD1`) remain in their home ledgers; [`OQ-QR1`](#OQ-QR1)–[`OQ-QR3`](#OQ-QR3) will enter this ledger only when closed with the owner's dated words verbatim ([`OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger)).

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-QR1"></a>[`D-QR1`](#D-QR1) | **Designer's decision (from an operator request); the owner may overturn this.** Place whole-queue rescue and single-item holds in `tools/tillr-drain` (`tillr drain rescue`, `tillr drain rescue --add/--remove/--status/--end`, `tillr drain hold`, `tillr drain unhold`), keeping `tools/tillr-item` free of `tillr-run` dependencies (`go-edges`) and keeping `items.state` untouched. Default `--until` to `trunk-green` when `--trunk-red` is passed and `set-finished` when only `--item` is passed, so rescues clear mechanically inside `tillr-drain` ([Landing Principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), [Principle 16](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr)); make bare `tillr drain rescue` a read-only alias for `--status`. | 2026-11-28, designer, from an operator request | [Section 1.3](#13-module-boundary-go-edges-invariant), [Section 2.1](#21-commands-to-enter-adjust-inspect-and-leave-d-qr1) | No |
| <a id="D-QR2"></a>[`D-QR2`](#D-QR2) | **Designer's decision (from an operator request); the owner may overturn this.** Define the rescue admitted set as the union of explicit `--item <id>` entries, dynamic `--trunk-red` matches (`fixer-main-*`, `flaky-*`, `sync-remote-*`) when enabled, repairs filed while active with `tillr item request --rescue`, and automatic transitive closure over open split children (`item_splits`) and open blockers (`item_edges` `blocked_by`), minus any `--remove <id>` entries. Hold newly filed unrelated items in `rescue-held` without refusing intake; govern default express-intake admission via [`OQ-QR2`](#OQ-QR2) with `--hold-express` / `--allow-express` per-rescue overrides. **Rejected:** a persistent `items.rescue` column on `items`. | 2026-11-28, designer, from an operator request | [Section 2.2](#22-how-the-admitted-set-is-named-and-what-happens-to-newly-filed-items-d-qr2) | No |
| <a id="D-QR3"></a>[`D-QR3`](#D-QR3) | **Designer's decision (from an operator request); the owner may overturn this.** Guarantee exact pre-rescue state restoration by implementing rescue as an overlay in `drain_rescue` that never mutates `items.state`, `items.express`, `items.rank`, or `items.waiting_on`, leaves pre-existing `item_holds` rows untouched, snapshots `drain_runtime_config.ordinary_lanes` into `prior_lanes_json` and restores it on exit **only** if `source == 'rescue'` (preserving any explicit `tillr drain scale` run mid-rescue), and resumes every run in `paused_runs_json` at its saved attempt, node, worktree, and candidate SHA. | 2026-11-28, designer, from an operator request | [Section 2.3](#23-proof-that-leaving-restores-exactly-what-entering-changed-d-qr3) | No |
| <a id="D-QR4"></a>[`D-QR4`](#D-QR4) | **Designer's decision (from an operator request); the owner may overturn this.** Hold started items without closing them across all six in-flight sub-states ([Section 3.2](#32-what-happens-to-a-started-items-live-run-lane-and-candidate-across-all-six-in-flight-sub-states-d-qr4)): pause live worker/parallel runs via `run.RequestPause` into `StatePaused` (`EndRestart`), pause live `verify`/`judge` runs via `judge.go`'s `errJudgePaused` (`judge.json` receipt + `releaseExamination`), let in-flight detached fast gates finish `gate.done.json` and hold before `Land` ([Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)), release concurrency slots to `idle` once child processes exit while exempting held item worktrees and `refs/tillr/runs/<run_id>` from GC, suppress `runContinuation` in `pass.go` while `itemHeld(itemID)` holds, and resume on the exact saved node/candidate with zero charged attempts or verdicts. **Rejected:** closing and reopening started items, or killing milliseconds-from-done local fast gates. | 2026-11-28, designer, from an operator request | [Section 3](#3-holding-a-started-item-without-closing-it-and-resuming-cleanly) | No |
| <a id="D-QR5"></a>[`D-QR5`](#D-QR5) | **Designer's decision (from an operator request); the owner may overturn this.** Evaluate rescue admission before `ordinaryAgentSlotOpen` in `tools/tillr-drain/drain/plan.go` so admitted ordinary items use the full `ordinary_lanes` concurrency pool while non-admitted ordinary items wait, making post-filing `ordinary -> express` switches unnecessary for rescues and keeping `[lanes] express` reserved for owner-facing latency work; put post-filing class edits for non-rescue filing mistakes to the owner in [`OQ-QR3`](#OQ-QR3). | 2026-11-28, designer, from an operator request | [Section 4](#4-lane-class-after-filing-and-why-rescue-makes-class-switching-unnecessary) | No |
| <a id="D-QR6"></a>[`D-QR6`](#D-QR6) | **Designer's decision (from an operator request); the owner may overturn this.** Persist `drain_rescue` (with partial unique index `WHERE ended_at IS NULL`) and `item_holds` in `.tillr/state.db` (`tools/tillr-drain/internal/storelog`), write the delegated-action `evidence_json` packet ([`authority-not-approval.md`](../reference/authority-not-approval.md#terms)) atomically inside `WriteTx` before signaling any pause, and emit `drain.rescue-entered`, `drain.rescue-updated`, `drain.rescue-ended`, `drain.item-held`, and `drain.item-unheld`. | 2026-11-28, designer, from an operator request | [Section 5.1](#51-store-tables-and-registered-events-d-qr6) | No |
| <a id="D-QR7"></a>[`D-QR7`](#D-QR7) | **Designer's decision (from an operator request); the owner may overturn this.** Register `ReasonRescueHeld` (`"rescue-held"`), `ReasonRescuePaused` (`"rescue-paused"`), `ReasonItemHeld` (`"item-held"`), and `ReasonItemPaused` (`"item-paused"`) in `holdrules.go`, compute dynamic per-item movers in `why.go` and `waiting.go` naming live admitted repairs/proof (and escalating `Class` to `"needs you"` / `owner` whenever remaining admitted items block on `owner` or `until_kind = 'manual'` has zero active work, satisfying [Landing Principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)), add `tillr drain rescue [--status] [--json]` plus banners in `why`, `status`, and Observatory, and build in the six ordered slices of [Section 9](#9-build-slices). | 2026-11-28, designer, from an operator request | [Section 5.2](#52-what-tillr-drain-why-and-tillr-view-waiting-show-d-qr7), [Section 5.3](#53-the-dedicated-rescue-read-face-and-observatory-banner-d-qr7), [Section 9](#9-build-slices) | No |
