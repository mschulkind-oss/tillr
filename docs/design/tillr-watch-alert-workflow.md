---
title: "Every alert gets an investigation and one recorded outcome, and silences bind a rule, variable, item, and bound"
status: accepted
stage: BUILT
stage-since: 2026-11-23
created: 2026-11-22
updated: 2026-11-27
tags: [watch, alerting, triage, observatory, view, tillr, drain, design]
summary: "Acknowledging an alert covers one instance only. Every episode, including one that resolved on its own, needs a recorded investigation and settles through a silence, a fix-rule item, a fix-fault item, or (for a fix that landed outside the local store) an external-fix settlement naming a repository and commit or external reference, who settled, and why. A silence is scoped by rule and variable binding, names an open queue item, carries a bound, and lifts when the item is in or the bound expires. Durable alert-to-item links and read-only Observatory pages show every episode, transition, linked item and external fix."
next: "Build slice 2 (D-AW3 rule variables) and D-AW14 (external-fix settlement on tillr watch settle); slices 1 and 3 to 8 of section 8 are built."
depends-on:
  - tillr-watch.md
  - tillr-alert-triage-seat.md
  - general-agent-watch.md
  - ../reference/observatory-redesign.md
  - tillr-utility-contracts.md
  - tillr-utility-contracts/tillr-watch.md
  - tillr-utility-contracts/tillr-view.md
  - tillr-utility-contracts/tillr-item.md
---

# Every alert gets an investigation and one recorded outcome, and silences bind a rule, variable, item, and bound

**Status:** Every owner question is ruled. Slices 1 and 3 to 8 of [§8](#8-the-build-in-slices-that-each-land-alone) are built across [`tools/tillr/watch/`](../../tools/tillr/watch/), [`tools/tillr-watch/`](../../tools/tillr-watch/), [`tools/tillr-item/`](../../tools/tillr-item/), [`tools/tillr-drain/`](../../tools/tillr-drain/) and [`tools/tillr-view/`](../../tools/tillr-view/). Two designer decisions remain unbuilt: Slice 2 (`D-AW3`, optional `[rule.variables]` declarations) and [`D-AW14`](#D-AW14) (`--outcome external-fix` on `tillr watch settle`: `rg -n 'external-fix|settled_repo' tools` returns nothing).

> **In short.** An alert has two independent facts: whether its condition is firing, and whether anyone has established why and recorded what settles it. Keeping them apart is the design. `ack` marks one instance seen; only a recorded investigation plus a silence, a `fix-rule` or `fix-fault` item, or a recorded `external-fix` when the fix landed outside the local store ([`D-AW14`](#D-AW14)) settles an episode.

**The shape.** Condition state (`open`, `acknowledged`, `silenced`, `resolved`) is tracked apart from settlement (`uninvestigated`, `investigating`, `investigated`, `settled`). Continuing suppression lives in a `watch_silences` row scoped by `(rule_id, vars_json)` that names an open item and a bound. Every local alert-to-item link is kept in `alert_item_links` and shown in both directions in the Observatory, while an external fix (`external-fix`) is recorded on the episode and in `watch.alert.settled` without fabricating a local item ([§3.5](#35-settling-a-fix-that-landed-outside-the-store-d-aw14)).

**Start at** [§3](#3-the-alert-workflow-from-opened-to-investigated-to-settled).

**Needs your ruling:** None (every open question is ruled; `D-AW1` to `D-AW14` in the [Decision Ledger](#decision-ledger) are designer's decisions, which the owner may overturn).

**Reads with:** [the observatory reference](../reference/observatory-redesign.md) (the principles every `tillr-view` alert surface follows), [the alert triage seat design](./tillr-alert-triage-seat.md) (which shares the investigation record, the outcome set and `tracking_item_id`, and may not silence), [the tillr-watch design](./tillr-watch.md) ([Section 6.8](./tillr-watch.md#68-alert-investigation-mandatory-outcomes-tracking-item-links-and-variable-scoped-silences-d-tw64-d-tw65), [D-TW64](./tillr-watch.md#D-TW64), [D-TW65](./tillr-watch.md#D-TW65), [D-TW67](./tillr-watch.md#D-TW67)), [the general agent watch design](./general-agent-watch.md), and the contracts for [`tillr-watch`](./tillr-utility-contracts/tillr-watch.md), [`tillr-view`](./tillr-utility-contracts/tillr-view.md) and [`tillr-item`](./tillr-utility-contracts/tillr-item.md).

---

## 1. What the owner said

The owner rulings this design rests on, each recorded in the [Decision Ledger](#decision-ledger):

- Every alert gets an investigation and an action. The action is one of three: silence this parameterized instance, file work to make the rule better, or file work to fix the fault. There is no "known about, chose not to file" disposition. Rules are parameterized by variables, and a silence binds one variable value of a rule, never the whole rule ([R-AW1](#R-AW1)).
- Watching finds and fixes issues; it applies no remedy ([R-AW2](#R-AW2)).
- No silent fallbacks, and an alert never caps, benches or fails queue work ([R-AW3](#R-AW3)).
- Acknowledging covers one instance and never ignores a later one ([R-AW4](#R-AW4)).
- Continuing suppression is parameterized, linked to an open queue item, and lifted automatically once that item is in ([R-AW5](#R-AW5)).
- Alerts, their status, when they fired and the items filed because of them are visible in the Observatory; the linked items are the record of action, and there is no other action log ([R-AW6](#R-AW6)).
- Every silence also carries an explicit bound ([OQ-AW1](#OQ-AW1)).
- An episode that resolves on its own still owes an investigation and an outcome ([OQ-AW2](#OQ-AW2)).
- A rule with no variables is silenced through the same path as a parameterized one ([OQ-AW3](#OQ-AW3)).

Every mechanism, command shape, table and slice boundary below is the designer's decision (`D-AW1` to `D-AW14`), which the owner may overturn.

---

## 2. What `tillr watch` does today — and where alerts slip through

| Surface | Today | Gap | This design |
| :--- | :--- | :--- | :--- |
| **`ack`** | Built as designed: `tillr watch ack <alert-id> --by <actor>` records the acknowledged instance (`acked_seq`, `acked_fingerprint`, `acked_lag_pair`), refuses `--item`, and a newer instance reopens the episode with `cause = "new-instance"` (or `"reopen-when"` on a `lag_behind` rule that declares `reopen_when`, [D-TW75](./tillr-watch.md#D-TW75)). | `ack` records no investigation and settles nothing. | Unchanged; `ack` additionally moves settlement to `investigating`. |
| **`track`** | `tillr watch track <alert-id> --item <id>` sets `tracking_item_id` and mutes repeats, with no bound, until the item finishes ([D-TW67](./tillr-watch.md#D-TW67)). | An unbounded mute across instances: a new fault can hide behind an open item. | Retired in favor of `settle` (one instance) and `silence` (bounded suppression). |
| **Alert-to-item links** | One scalar `tracking_item_id` per episode, cleared on recurrence; `tillr item request` has no alert flag. | Earlier links are lost; a resolved episode cannot be linked; an item has no back-link. | `alert_item_links` and `tillr item request --alert` ([§5.2](#52-why-scalar-tracking_item_id-alone-is-not-enough--and-durable-bidirectional-alert_item_links)). |
| **`silence`** | Per `alert_id`, with `--until` or `--until-change` and an optional `--reason`. | Not scoped by variable, linked to no item, and lost when the episode resolves in a quiet window. | The only continuing suppression: a durable, item-linked, bounded `watch_silences` row ([§4.2](#42-durable-watch_silences-and-how-one-variable-is-muted-without-muting-others)). |
| **`resolve`** | `tillr watch resolve <alert-id> --reason <text>` closes an episode on prose alone (`resolved_reason = 'manual'`). | This is the forbidden "understood, not filed" dismissal. | Removed. `resolved` is reserved for the engine's own endings, and settlement goes through `settle`. |
| **Investigation** | None recorded. | An alert can be muted or closed on a guess. | `tillr watch investigate`, required before any settlement ([§3.2](#32-step-1--mandatory-investigation-before-any-settlement)). |
| **`wait`, `status`, `drain why`** | Once `seen_seq` passes an episode it never wakes `wait` again; neither `status` nor `tillr drain why` counts unsettled alerts. | A seen alert can sit unacted on indefinitely. | Unsettled reminders, `status --unsettled`, and a standing drain alarm ([§6.1](#61-cli-status-wait-reminders-and-the-standing-drain-alarm)). |
| **The Observatory** | No alert command, route or page. | The owner cannot see alerts or what was done about them. | Read-only alert faces and pages ([§6.2](#62-observatory-surfaces-what-is-built-today-versus-what-is-new)). |

---

## 3. The alert workflow: from opened to investigated to settled

### 3.1 Separating condition state, single-instance `ack`, and workflow settlement

An episode carries two independent states (`D-AW1`, `D-AW11`):

1. **Condition state (`state`).** `open` (firing, current instance unacknowledged), `acknowledged` (firing, the current instance seen), `silenced` (firing under an active silence), or `resolved` (the condition ended: `condition-cleared`, or the subject item reached a final state, [D-TW63](./tillr-watch.md#D-TW63)).
2. **Settlement (`settlement_state`).** `uninvestigated`, then optionally `investigating`, then `investigated`, then `settled` with an outcome of `silence`, `fix-rule`, `fix-fault` or `external-fix` ([`D-AW14`](#D-AW14)).

Resolution never settles: an unsettled episode that resolves stays unsettled until it is investigated and given an outcome ([OQ-AW2](#OQ-AW2)).

```mermaid
stateDiagram-v2
    [*] --> Uninvestigated: opened / reopened
    Uninvestigated --> Investigating: ack (one instance) or triage claim
    Investigating --> Uninvestigated: new instance arrives
    Uninvestigated --> Investigated: investigate
    Investigating --> Investigated: investigate
    Investigated --> Settled: silence, or settle fix-rule / fix-fault / external-fix
    Uninvestigated --> Settled: silence or settle with inline cause and evidence
    Settled --> Uninvestigated: silence lifts while firing, or new instance with no silence
```

### 3.2 Step 1 — Mandatory investigation before any settlement

An alert whose cause is unknown cannot be settled (`D-AW1`, `D-AW2`).

```text
tillr watch investigate <alert-id> --store <path> --by <actor> --cause <text> --evidence <text>
```

- The command records who investigated, when, the cause and the evidence on the episode, sets `settlement_state = 'investigated'`, and appends `watch.alert.investigated`.
- `--evidence` names the concrete facts read (event sequences, timing records, run logs, verdict paths). `--cause` states the mechanism established from them; `"unknown"` and blank text are refused.
- `settle` and `silence` accept `--cause` and `--evidence` inline, recording the investigation and the outcome in one transaction. A settlement on an episode with no recorded investigation and no inline flags is refused with exit `2` and changes nothing.
- When the cause is not yet clear, the actor claims the episode (`ack`, or the triage claim) and keeps reading, or escalates. An unknown cause never settles an episode.

### 3.3 Step 2 — The closed set of three outcomes

| Outcome | Meaning | Command | Preconditions |
| :--- | :--- | :--- | :--- |
| **`silence`** | The rule is useful, and this variable binding should be suppressed while an open item is in flight. | `tillr watch silence [<alert-id> \| --rule <id> [--var <k>=<v>...]] --by <actor> --item <item-id> --reason <text> (--until <dur\|time> \| --until-change)` | A recorded investigation; non-empty `--by` and `--reason`; the actor is not the triage seat; `<item-id>` is open; a variable binding on a parameterized rule; an explicit bound. |
| **`fix-rule`** | The rule fired falsely, too eagerly, or with the wrong grouping. | `tillr watch settle <alert-id> --by <actor> --outcome fix-rule --item <item-id>` | A recorded investigation; non-empty `--by`; `<item-id>` exists and is not closed; if it already finished, it finished after the episode's last observation. |
| **`fix-fault`** | The episode found a real defect in code, tests, configuration or tooling tracked in this store. | `tillr watch settle <alert-id> --by <actor> --outcome fix-fault --item <item-id>` | Same as `fix-rule`. |
| **`external-fix`** | The episode's fix landed outside this store (for example, a commit in upstream `tillr` for a consumer repository running from the installed pack), so there is no local queue item to name ([`D-AW14`](#D-AW14)). | `tillr watch settle <alert-id> --by <actor> --outcome external-fix (--repo <repo> --commit <commit> \| --ref <text>) --reason <text>` | A recorded investigation; non-empty `--by` and `--reason` (not `"unknown"`); either both `--repo` and `--commit` non-empty or `--ref` non-empty (mutually exclusive); `--item` and `--link-kind` omitted. |

**Filed or linked.** Both item-linked outcomes (`fix-rule` and `fix-fault`) name a real local item and reject `--repo`, `--commit`, `--ref` and `--reason`. `item_link_kind` is `"filed"` when the investigation created the item and `"linked"` when the item was already open for the same cause. `external-fix` rejects `--item` and `--link-kind` ([§3.5](#35-settling-a-fix-that-landed-outside-the-store-d-aw14)).

**Finished items.** `settle` refuses a closed item. For a finished item it compares the episode's last observed sequence with the item's final event: an item that finished after the observation settles the episode; an item that finished before it did not stop the condition and is refused, with a message telling the actor to file a new item. `silence` requires an item that is open now, because the silence lasts until that item lands or closes.

### 3.4 Alignment with the alert triage seat and benign triggers

This workflow and [the alert triage seat](./tillr-alert-triage-seat.md) share one schema and one actor-neutral evaluator interface ([D-TW65](./tillr-watch.md#D-TW65), `D-AW2`):

1. **Same operations, different `--by`.** The operating session runs `investigate`, `settle` and `silence` as `operator-session`; the triage runner performs the same `investigate` and `settle` store operations as `tillr-triage`.
2. **One outcome mapping.** Triage's `filed` and `linked` outcomes ([§5.3 of the triage seat design](./tillr-alert-triage-seat.md#53-the-structured-triage-outcomes)) supply `item_link_kind` together with `fix-fault` or `fix-rule`, and write the same `tracking_item_id`. Triage's `escalated` outcome records the investigation; the episode is settled only if an item is attached, and otherwise stays investigated and unsettled until the operator or owner records an outcome.
3. **Triage never silences** ([D-AT6](./tillr-alert-triage-seat.md#decision-ledger)).
4. **No `expected` outcome** ([OQ-AT2](./tillr-alert-triage-seat.md#OQ-AT2)). A benign operation that fires a rule is settled by a `fix-rule` item.
5. **Auto-resolution leaves settlement untouched** ([OQ-AW2](#OQ-AW2)). Rows already `resolved` when the settlement columns are added are marked settled once, by the schema migration, so the upgrade does not create a backlog.

### 3.5 Settling a fix that landed outside the store (`D-AW14`)

*Designer's decision from a 2026-11-27 filed request from a consumer repository, answering a build run's conflict against `D-AW2`, `D-AW5`, `D-AW9` and `D-AW10`; not an owner ruling, and the owner may overturn any part of it.*

In a consumer repository running `tillr` from the installed pack, `.tillr/state.db` carries that repository's own `items` table. Under `D-AW2` and `D-AW10` as first written, `validateStoreSettleItem` in [`lifecycle.go`](../../tools/tillr/watch/lifecycle.go) required every `tillr watch settle` inside a `tillr` store to pass `--item <item-id>` matching a row in that store's `items` table. When a consumer-repository alert (such as a stuck proof or slow gate pass) was fixed by a commit in another repository (such as upstream `tillr`), work had fixed the fault ([R-AW1](#R-AW1)), yet no local item existed to name, leaving investigated alerts permanently `unsettled` and re-emitting `UNSETTLED` reminders on every check-in.

`D-AW14` amends `D-AW2`, `D-AW5`, `D-AW9` and `D-AW10` by adding `--outcome external-fix` on `tillr watch settle` while keeping the item-linked outcomes (`fix-rule`, `fix-fault` and `silence`) unchanged:

```text
tillr watch settle <alert-id> --store <path> --by <actor> --outcome external-fix (--repo <repo> --commit <commit> | --ref <text>) --reason <text> [--cause <text> --evidence <text>]
```

1. **Preconditions and flag validation (`SettleOptions` and `validateSettleOptions` in [`lifecycle.go`](../../tools/tillr/watch/lifecycle.go), CLI flags in [`tools/tillr-watch/lifecycle.go`](../../tools/tillr-watch/lifecycle.go)).**
   - Mandatory investigation (`D-AW1`) applies unchanged: the episode must already be `investigated` or supply non-empty inline `--cause` and `--evidence` (`--cause` not `"unknown"`), otherwise `ErrInvestigationRequired` (exit `2`) is returned before any write.
   - `--by <actor>` and `--reason <text>` (why this external fix settles the episode) are required and non-empty after trimming whitespace; `--reason` also refuses `"unknown"`.
   - Exactly one external target shape is required: either `--repo <repo> --commit <commit>` (both non-empty after trimming whitespace; supplying `--repo` without `--commit` or `--commit` without `--repo` is refused) or `--ref <text>` (a non-empty free-text external reference; supplying `--ref` alongside `--repo` or `--commit`, or supplying neither target shape, is refused).
   - `--outcome external-fix` refuses `--item` and `--link-kind` before any write and skips `validateStoreSettleItem` / `validateSourceSettleItem`, leaving `tracking_item_id` and `item_link_kind` `NULL` and writing no row to `alert_item_links`. Conversely, `--outcome fix-rule` and `--outcome fix-fault` refuse `--repo`, `--commit`, `--ref` and `--reason` and keep local item verification unchanged. `--outcome expected` and `--outcome silence` remain refused.
   - `tillr watch settle` prints `alert <id> settled (external-fix -> <repo>@<commit>)` when `--repo` and `--commit` are given, or `alert <id> settled (external-fix -> <ref>)` when `--ref` is given, and `tillr watch help` and `printAlertActions` in [`wait.go`](../../tools/tillr-watch/wait.go) document both `settle` forms.
2. **Episode columns, migration, event, and idempotency.**
   - `alert_episodes` adds four nullable `TEXT` columns (`settled_repo`, `settled_commit`, `settled_ref`, `settled_reason`) in [`eval.go`](../../tools/tillr/watch/eval.go) via `store.RegisterColumn("watch", "alert_episodes", ...)` (and on `memEpisode` in [`source.go`](../../tools/tillr/watch/source.go)), checked through `hasExternalSettleColumns` separate from `hasSettlementColumns` so existing Slice-3 stores continue reading `settlement_state` read-only before migration, while `NeedsSettleMigration` requests `store.OpenMigrated` when `opts.Outcome == "external-fix"` and those columns are absent.
   - `watch.alert.settled` records `alert_id`, `rule_id`, `subject_key`, `severity`, `by`, `outcome: "external-fix"`, `reason`, `settled_seq`, and either `repo` and `commit` or `ref`, omitting `item_id` and `item_link_kind`.
   - Repeating `settle --outcome external-fix` on an already-settled episode is a no-op (`exit 0`) when `outcome`, external target (`repo`+`commit` or `ref`) and `reason` match the stored settlement, and exits `1` on any mismatch (`checkSettleConflict`).
3. **Stopping reminders, alarms, and stale escalation state (`lifecycle.go`, `delivery.go`, `status.go`, `alarms/unsettled_watch_alerts.go`, `AlertsPage.tsx`).**
   - Every `Evaluator.Settle` transition (`fix-rule`, `fix-fault` and `external-fix`) sets `settlement_state = 'settled'`, records the single-instance watermarks (`settled_seq`, `settled_fingerprint`, `settled_lag_pair`), clears `escalated_to = NULL` and `active_triage_run_id = NULL` when those columns exist, and sets `triage_state = 'settled'` when triage columns exist; `Pending` (`delivery.go`) and `OwnerStatus.readPending` (`status.go`) also skip `watch.alert.escalated` notices when `settlement_state == 'settled'`. Settling an episode—including one previously escalated to `operator-session` by `triage` because its fix lived in another repository—stops `UNSETTLED` reminders in `tillr watch wait`, removes it from `tillr watch status --unsettled` and `unsettled-watch-alerts` in `tillr drain why`, and clears its escalation state in Observatory.
   - Like item-linked `settle` (`D-AW5`), `external-fix` covers only the settled instance (`R-AW4`, `R-AW5`). Re-evaluating the settled window wakes nobody; a newer instance (`newest.Seq > settled_seq`, `reopen_when` after `settled_seq`, changed fingerprint or changed lag pair) with no active `watch_silences` row reopens the episode as `open` and `uninvestigated` (`cause = "new-instance"` or `"reopen-when"`), clearing `outcome`, `settled_*` watermarks and `settled_repo`, `settled_commit`, `settled_ref` and `settled_reason`.
   - Automated triage (`Evaluator.RecordTriage` in [`triage.go`](../../tools/tillr/watch/triage.go)) keeps its three outcomes (`filed`, `linked`, `escalated`) unchanged: when a fix belongs outside the local store, `triage` escalates to `operator-session` with its investigation finding, and the operator settles via `tillr watch settle --outcome external-fix` once the external fix exists.
4. **Read surfaces and pack mirror (`status.go`, `links.go`, `tillr-view/alerts.go`, `AlertsPage.tsx`).**
   - `AlertStatus` (`tillr watch status`), `AlertEpisodeRecord` (`ReadAlertEpisodes` via `colOrNull` in [`links.go`](../../tools/tillr/watch/links.go)), and `AlertEpisodeView` (`tillr-view alerts` and `/api/v1/alerts` in [`alerts.go`](../../tools/tillr-view/alerts.go)) expose `settled_repo`, `settled_commit`, `settled_ref` and `settled_reason` (plus `outcome` and `settled_by` on `AlertStatus`); human `tillr watch status` formats external settlements as `external-fix (<repo>@<commit>)` or `external-fix (<ref>)`, Observatory (`AlertsPage.tsx`) renders `external-fix` and displays the external target, `settled_by` and `settled_reason` in the episode detail drawer's Settlement section ([R-AW6](#R-AW6)), and `just pack-sync` mirrors the `tools/tillr*` modules into `yolo-packs/tillr/`.

---

## 4. Parameterizing rules by variables and scoping silences to `(rule_id, variable_binding)`

### 4.1 How rules declare and expose named variables

Variables come from two places, and no existing rule file needs an edit (`D-AW3`):

1. **Derived from `[rule.when].group_by`.** A single non-global path exposes one variable named after the path's last segment (`group_by = "slowest_check"` exposes `slowest_check`). A composite `group_by` ([D-TW50](./tillr-watch.md#D-TW50)) exposes its ordered variables (`drain-lane-refused` exposes `lane_id` and `reason`). `group_by = "global"` exposes none. Of the 36 rules in `config/tillr/watch/`, 25 expose variables this way and 11 are global (`rg -N 'group_by' config/tillr/watch`).
2. **Declared in an optional `[rule.variables]` table**, mapping an identifier to an event field path:

   ```toml
   [rule.variables]
   package = "detail.package"
   suite = "detail.suite"

   [rule.when]
   group_by = ["detail.package"]

   [rule.guidance]
   summary = "Package {{var.package}} failed {{count}} times (suite {{var.suite}})."
   ```

   A declared name for a `group_by` path aliases the derived variable. A declared name for another string field of the newest counted event adds that field. The full map is stored on the episode as `variables_json`.

`tillr watch lint` checks that declared names are unique valid identifiers, that every path is a valid event path, that a `lag_behind` rule (which reads no events) declares none, and that each `{{var.<name>}}` placeholder names a variable of its rule.

### 4.2 Durable `watch_silences` and how one variable is muted without muting others

A silence kept on an episode row disappears when that episode resolves in a quiet window. Silences therefore live in their own table (`D-AW4`):

```sql
CREATE TABLE IF NOT EXISTS watch_silences (
    silence_id             TEXT PRIMARY KEY,
    rule_id                TEXT NOT NULL,
    vars_json              TEXT NOT NULL,   -- canonical sorted JSON object; "{}" on a global rule
    tracking_item_id       TEXT NOT NULL,   -- open queue item owning the fix
    created_by             TEXT NOT NULL,
    created_at             INTEGER NOT NULL,
    reason                 TEXT NOT NULL,
    investigation_cause    TEXT NOT NULL,
    investigation_evidence TEXT NOT NULL,
    source_alert_id        TEXT,
    silence_until_ms       INTEGER,         -- --until
    silence_on_fingerprint TEXT,            -- --until-change
    lifted_at              INTEGER,
    lifted_by              TEXT,
    lifted_reason          TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS watch_silences_active_scope
    ON watch_silences(rule_id, vars_json) WHERE lifted_at IS NULL;
```

1. **Binding.** On a parameterized rule, `silence` requires at least one `--var <name>=<value>`, or an alert id whose `variables_json` becomes the full binding. No binding, or an unknown variable name, is refused with exit `2`. On a global rule the same path is used with `vars_json = "{}"` ([OQ-AW3](#OQ-AW3)).
2. **Item and bound.** Every silence names an open item and carries `--until` or `--until-change` ([R-AW5](#R-AW5), [OQ-AW1](#OQ-AW1)). There is no standing silence.
3. **Matching.** A silence matches an episode of its rule when it is not lifted, its item is still open, its bound still holds, and every entry of `vars_json` equals the episode's variable of that name. Silencing `package=tools/flaky` leaves every other package live. Silencing an alert of a composite rule binds the full tuple by default; an explicit partial `--var` matches any value of the unnamed variables.
4. **Quiet gaps.** While a silence holds, a matching episode is `silenced` and `settled` with outcome `silence`, and keeps counting observations. If it resolves and the condition fires again under the same silence, the next episode opens directly as silenced and wakes nobody.
5. **Lifting.** Whichever comes first lifts the silence: the linked item lands or closes (`lifted_reason = "item-landed"` or `"item-closed"`), or the bound expires (`"silence-expired"` or `"fingerprint-changed"`). The lift appends `watch.silence.lifted`; an episode still firing reopens as `open` and `uninvestigated`. `tillr watch silence list` shows silences, and `tillr watch silence lift <silence-id> --by <actor> --reason <text>` lifts one by hand.
6. **`--until-change`** needs a rule that declares a fingerprint ([D-TW32](./tillr-watch.md#D-TW32)) and an active episode to take the fingerprint from.

### 4.3 Preserving domain neutrality for standalone `--state` users

The watch engine imports neither `tillr-item` nor `tillr-drain` (`D-GW9` of [the general agent watch design](./general-agent-watch.md)). Variables, silences, links, investigation and settlement are plain tables that work the same in a standalone watch state file. Only the check that `--item` names a real queue item on `fix-rule`, `fix-fault` and `silence` is injected, through an optional seam provided by `TillrStoreSource`; without a `tillr` store, `--item` accepts any non-empty external reference (`D-AW10`), and in both modes `--outcome external-fix` records an external repository/commit or reference with `--reason` and no `--item` ([`D-AW14`](#D-AW14)).

---

## 5. Shared tracking-item binding and repeat counting without waking

### 5.1 Single-instance watermarks on `ack` and `settle` versus active silence suppression

`ack`, `settle` and silences share one episode schema and one `tracking_item_id` column ([D-TW65](./tillr-watch.md#D-TW65), `D-AW5`, `D-AW11`). The `acked_*` columns exist; the rest are additions:

```sql
variables_json         TEXT NOT NULL DEFAULT '{}',
settlement_state       TEXT NOT NULL DEFAULT 'uninvestigated',
investigated_by        TEXT,
investigated_at        INTEGER,
investigation_cause    TEXT,
investigation_evidence TEXT,
outcome                TEXT,      -- 'silence' | 'fix-rule' | 'fix-fault' | 'external-fix'
item_link_kind         TEXT,      -- 'filed' | 'linked'
silence_id             TEXT,
settled_by             TEXT,
settled_at             INTEGER,
settled_seq            INTEGER,   -- event seq covered by settle
settled_fingerprint    TEXT,
settled_lag_pair       TEXT,
settled_repo           TEXT,      -- external-fix repository (paired with settled_commit)
settled_commit         TEXT,      -- external-fix commit SHA/revision
settled_ref            TEXT,      -- external-fix free-text reference
settled_reason         TEXT,      -- external-fix explanation of why the external fix settles it
reopen_count_for_item  INTEGER NOT NULL DEFAULT 0,
escalated_to           TEXT,      -- 'operator-session' | 'owner'
last_reminded_at       INTEGER
```

1. **`ack` covers one observed instance** ([R-AW4](#R-AW4), `D-AW11`). While evaluation re-reads the same instance the episode stays `acknowledged` and unsettled. A later instance (a newer matching event, a `reopen_when` match, a changed fingerprint or a changed lag pair) with no matching silence reopens it and wakes the receiver. `acknowledged` never blocks `condition-cleared` ([D-TW72](./tillr-watch.md#D-TW72)).
2. **`settle` covers one observed instance** (`D-AW5`, [`D-AW14`](#D-AW14)). It records the outcome (`fix-rule` or `fix-fault` with the tracking item, or `external-fix` with `--repo`+`--commit` or `--ref` and `--reason`), clears any active escalation (`escalated_to`, `active_triage_run_id`, setting `triage_state = 'settled'` when triage columns exist), and records the settled sequence, fingerprint and lag pair. Re-reading the same settled window does not wake anyone. A new instance with no active silence reopens the episode:
   - if `tracking_item_id` is set and the tracking item is still open, the repeat count rises and the item id is kept for context (on a `triage` rule, the seat checks whether the new instance shares the item's cause and escalates at the third reopen, [D-AT11](./tillr-alert-triage-seat.md#decision-ledger));
   - if `tracking_item_id` is set and the tracking item has landed or closed, the episode reopens with `cause = "recurred-after-item-<state>"`: the item did not remove the fault;
   - if settled via `external-fix` (`tracking_item_id IS NULL`), the episode reopens as `open` and `uninvestigated` with `cause = "new-instance"` (or `"reopen-when"`) and clears the `settled_*` columns.
3. **Suppression across instances needs a silence** ([R-AW5](#R-AW5)).

### 5.2 Why scalar `tracking_item_id` alone is not enough — and durable bidirectional `alert_item_links`

One scalar column cannot answer "which items were filed because of this alert" ([R-AW6](#R-AW6)): it is cleared on recurrence, it holds one item where an episode may need two, it cannot be set on a resolved episode without reopening it, and it gives an item no indexed way back to its alerts. The scalar stays as the episode's current item pointer, and every link is also kept in a join table owned by the watch package (`D-AW12`):

```sql
CREATE TABLE IF NOT EXISTS alert_item_links (
    alert_id   TEXT NOT NULL REFERENCES alert_episodes(alert_id),
    item_id    TEXT NOT NULL,
    relation   TEXT NOT NULL,   -- 'filed' | 'silence' | 'fix-fault' | 'fix-rule' | 'tracked'
    linked_at  INTEGER NOT NULL,
    linked_by  TEXT,
    note       TEXT,
    event_seq  INTEGER NOT NULL,
    PRIMARY KEY (alert_id, item_id)
);
CREATE INDEX IF NOT EXISTS alert_item_links_by_item
    ON alert_item_links(item_id, linked_at, alert_id);
```

- **Filing from an alert.** `tillr item request` takes a repeatable `--alert <alert-id>`. The link is written in the same transaction as the item; an unknown alert id refuses and rolls the filing back. It works on an episode in any state, including `resolved`, appends `watch.alert.item-linked`, and neither mutes later instances nor replaces `settle`.
- **Settling or silencing.** Item-linked `settle` (`fix-rule` or `fix-fault`) upserts the link with relation `fix-fault` or `fix-rule`; `silence` upserts `silence` unless a fix relation is already recorded (`external-fix` writes no `alert_item_links` row because there is no local item, [`D-AW14`](#D-AW14)). A later recurrence leaves every earlier link in place.
- **Existing stores.** Opening a store for writing creates the table and backfills it from `tracking_item_id` and the stored `watch.alert.*` events, with relation `tracked` for links made by `track`. A read-only caller on a store without the table projects the same links from those sources and writes nothing.

---

## 6. Making unsettled alerts unmistakable — and showing every alert, transition, and linked item in the Observatory

### 6.1 CLI `status`, `wait` reminders, and the standing drain alarm

Unsettled episodes, including resolved ones, are surfaced in three places (`D-AW6`):

1. **`tillr watch status`** stays read-only ([D-TW34](./tillr-watch.md#D-TW34)) and leads with a summary of unsettled, settled and silenced counts. `--unsettled` lists every episode that still owes a decision.

   ```text
   summary unsettled=2 (uninvestigated=1 investigating=0 investigated=1) settled=2 active_silences=1
   alert <alert-id> rule=fast-gate-run-slow vars={"slowest_check":"go-build"} state=open settlement=uninvestigated unsettled_for=14m
   silence <silence-id> rule=package-frequent-red vars={"package":"tools/flaky"} item="<item-id>" by="operator-session" until=<time> matched_alerts=3
   ```

2. **`tillr watch wait`.** Advancing `seen_seq` confirms delivery of the first notice; it does not retire an unsettled episode. An episode routed or escalated to the operating session that is still unsettled after `unsettled_remind_after` (default `15m`) is re-emitted with an `UNSETTLED` banner. Episodes escalated to the owner are excluded. The notice footer prints the `investigate`, `settle`, `silence` and `ack` command templates.
3. **`tillr drain why`** raises a standing `unsettled-watch-alerts` alarm with the count, the oldest age and the alert ids, excluding episodes under a live triage claim or waiting on the owner. The timed drain check-in skill (an operating skill kept outside this repository) treats each such alert as work to finish before the check-in report.

### 6.2 Observatory surfaces: what is built today versus what is new

Built: the `alert_episodes` table and the `watch.alert.*` events (`opened`, `acknowledged`, `tracked`, `silenced`, `reopened`, `resolved`), item-subject auto-closure ([D-TW63](./tillr-watch.md#D-TW63)) and rule load baselines ([D-TW66](./tillr-watch.md#D-TW66)). `tillr-view` and `tillr-item` read none of it.

New, all read-only and following the principles of [the observatory reference](../reference/observatory-redesign.md) (`D-AW13`):

| Surface | Address | Role |
| :--- | :--- | :--- |
| **Alerts ledger** | `#/alerts`, filterable by state, rule, severity and settlement | Every active and resolved episode, plus active silences. |
| **Alert page** | `#/alert/<alert-id>` | One episode: its metadata, the full transition history with times and actors, linked items with their live states, the captured bundle, and the other episodes of the same rule and subject. |
| **Home panel** | `#/` | Counts and the first few unsettled or active alerts; promoted to the top when a high-severity, unsettled or owner-escalated alert is active. |
| **Item page section** | `#/item/<item-id>` | Every alert linked to the item, or an explicit statement that there is none. |
| **CLI, HTTP and WebSocket** | `tillr-view alerts`, `GET /api/v1/alerts`, `GET /api/v1/alerts/{id}`, `linked_alerts` on `GET /api/v1/items/{id}`, invalidation topics for alerts and items | The same Go projections behind every face; a store with no alert table returns an empty list flagged as such rather than an error. |

### 6.3 What each alert row shows on `#/alerts` and `Home`

Each row shows the alert id with its state mark, the rule (clickable as a filter), the subject and its variables, the severity, the condition and settlement states with the unsettled age, when it fired with its episode number and observation count, the latest transition with its time and actor, and every linked item with that item's current state and link relation (plus the external repository/commit or reference, actor and reason in the episode detail drawer when settled via `external-fix`, [`D-AW14`](#D-AW14)). Rows default to all states, unsettled and active first.

### 6.4 Full state-change history on `#/alert/<id>` with time and actor

The alert page rebuilds the episode's history from its stored events in sequence order. Each row shows the sequence, the timestamp, the state change, the actor and the detail:

| State change | Stored event | Actor |
| :--- | :--- | :--- |
| `fired` | `watch.alert.opened`, first episode | The evaluator; never a person. |
| `acknowledged` | `watch.alert.acknowledged` | The `--by` value. |
| `investigated`, `settled` | `watch.alert.investigated`, `watch.alert.settled`, `watch.alert.item-linked`, `watch.alert.tracked` | The `--by` value. |
| `silenced`, `silence-lifted` | `watch.alert.silenced`, `watch.silence.lifted` | The `--by` value, or the evaluator on an automatic lift. |
| `resolved` | `watch.alert.resolved` | The evaluator for `condition-cleared` and item endings. |
| `re-fired` | `watch.alert.reopened`, or `watch.alert.opened` on a later episode | The evaluator. |

A stored event that carries no actor (a `track` or `silence` without `--by`, a manual `resolve`) is shown as "not recorded", never with an invented actor. An episode with no events shows that explicitly beside its stored timestamps. Triage events appear in the same table once the triage seat is built.

### 6.5 Bidirectional alert and item links across `#/alert/<id>`, `#/item/<id>`, and CLI faces

- **Alert to items.** Each alert carries its linked items, ordered by link time, with each item's title, current state ([item states](../reference/tillr-item-states.md)), relation, whether an active silence links it, and who linked it.
- **Item to alerts.** `GET /api/v1/items/{id}`, `tillr item show` and the item page list every linked alert with its rule, subject, states and relation.
- **No separate action log** ([R-AW6](#R-AW6)). The linked items, the silences, any `external-fix` settlement fields on `alert_episodes` ([`D-AW14`](#D-AW14)) and the transition history are the complete record of what was done because of an alert.

---

## 7. Edge cases, concurrency, and failure modes

| Case | Behavior (`D-AW7`) |
| :--- | :--- |
| **A second matching event arrives after `ack`** | The episode reopens as `open` and `uninvestigated` with `cause = "new-instance"` and wakes the receiver. |
| **Two actors settle the same alert** | Both run in a write transaction. The second sees the episode settled and exits `1`, reporting who settled it and how; it exits `0` only if the outcome and target (item and link kind, or external target and reason on `external-fix`) match. |
| **One rule fires on a silenced and an unsilenced binding in one pass** | The silenced binding counts quietly; the other opens and wakes the receiver. |
| **A silenced binding goes quiet, then fires again while the silence holds** | The first episode resolves; the next opens directly as silenced. |
| **The silence's item lands before `--until` expires** | The silence lifts at once. A later firing opens unsilenced with `cause = "silence-item-landed"`. |
| **An episode resolves before anyone investigates** | It stays unsettled in `status --unsettled`, reminders, `tillr drain why` and triage dispatch; `tillr item request --alert` links an item without reopening it. |
| **An unknown alert id, a missing item, a closed item on `silence`, `--outcome expected`, or invalid `external-fix` flags** | Refused before any row or event is written (including `--item`/`--link-kind` passed with `external-fix`, `--repo`/`--commit`/`--ref`/`--reason` passed with `fix-rule` or `fix-fault`, missing `--reason`, or mixing/omitting `--repo`+`--commit` and `--ref`). |
| **Several items for one alert across recurrences** | `tracking_item_id` holds the latest; `alert_item_links` keeps all. |
| **The triage seat attempts `silence`** | Refused twice: by the command shim ([§5.2 of the triage seat design](./tillr-alert-triage-seat.md#52-forbidden-actions-hard-mechanical-boundary)) and by the evaluator on the actor. |
| **Rows that exist when the columns are added** | `open` and `acknowledged` rows default to `uninvestigated`; `resolved` rows are marked settled by the migration; links are backfilled. |

---

## 8. The build, in slices that each land alone

Eight slices in order (`D-AW8`), followed by the seven slices of [the alert triage seat](./tillr-alert-triage-seat.md#9-the-build-in-slices-that-each-land-alone), whose last slice completes the Observatory panels for triage outcomes and silences.

| Slice | What lands | Done when | Built |
| :--- | :--- | :--- | :--- |
| **1** | `ack` covers one instance: the `acked_*` watermarks, `--item` refused on `ack`, reopen on any newer instance ([R-AW4](#R-AW4), `D-AW11`). | Tests show `ack` mutes only the acknowledged observation and never blocks `condition-cleared`. | Yes |
| **2** | Rule variables: derivation from `group_by`, `[rule.variables]`, `variables_json`, guidance placeholders and lint checks (`D-AW3`). | Tests cover derivation for every rule in `config/tillr/watch/`, declared mappings and lint refusals. | No |
| **3** | Mandatory investigation and item-verified `settle`; `resolve` and `track` retired; auto-resolution left unsettled (`D-AW1`, `D-AW2`, `D-AW5`). | Tests show an uninvestigated `settle` fails, a missing, closed or stale-finished item fails, and a new instance after `settled_seq` reopens. | Yes |
| **4** | Item-linked, bounded `watch_silences` for parameterized and global rules, with `list`, `lift` and automatic lift (`D-AW4`). | Tests show one binding muted across episode clears while another wakes, and the lift on item completion or bound expiry. | Yes |
| **5** | Unsettled alerts in `status --unsettled`, `wait` reminders, `tillr drain why` and the check-in skill (`D-AW6`). | Tests show `seen_seq` alone does not suppress a reminder and the drain alarm stands until settlement. | Yes |
| **6** | `alert_item_links`, `tillr item request --alert`, `linked_alerts` on `tillr item show`, and the watch read helpers (`D-AW12`). | Tests show several items per alert survive recurrences and an unknown alert id rolls the filing back. | Yes |
| **7** | `tillr-view alerts`, the alert HTTP routes, `linked_alerts` on the item route, and WebSocket invalidation (`D-AW13`). | Tests show CLI and HTTP agree, every transition row renders with a recorded or "not recorded" actor, and an empty store is safe. | Yes |
| **8** | The Observatory pages: the ledger, the alert page, the Home panel and the item page section (`D-AW13`). | UI tests cover filtering, the transition history, and navigation between alert and item pages. | Yes |

---

## 9. What this does not do

- It builds no separate free-text action table apart from queue items (`alert_item_links`), silences (`watch_silences`) and the structured episode settlement fields (`D-AW12`, [`D-AW14`](#D-AW14)).
- It opens no writable store handle in `tillr-view`.
- It executes no remedy: recording an outcome changes watch and item records only.
- It never lets `ack` ignore a later instance, or a silence stand without an open item and a bound.
- It never lets automated triage silence an alert.
- It adds no `tillr-item` or `tillr-drain` import to the watch engine.
- It changes no rule file; a rule that fires falsely is fixed by its own `fix-rule` item.

---

## 10. Alternatives considered

| Alternative | Verdict (`D-AW9`) |
| :--- | :--- |
| Keep `resolve --reason`, or add an `expected` outcome | Rejected: it is the "understood, not filed" disposition ([R-AW1](#R-AW1), [OQ-AT2](./tillr-alert-triage-seat.md#OQ-AT2)). |
| Keep `track` as an unbounded mute while an item is open | Rejected: a new fault hides behind the open item ([R-AW4](#R-AW4), [R-AW5](#R-AW5)). |
| Rely on scalar `tracking_item_id`, or scan events on every request | Rejected: the scalar loses history, and a scan per item request does not scale. |
| A separate table of unguided free-text actions | Rejected: contradicts [R-AW6](#R-AW6); local work links through `alert_item_links`, while cross-repository fixes record structured `settled_repo`/`settled_commit` or `settled_ref` plus `settled_reason` directly on `alert_episodes` ([`D-AW14`](#D-AW14)). |
| Keep silences as episode columns | Rejected: fails when no episode is open and when a silenced condition clears and fires again. |
| Silence by rewriting the rule's `match` expression | Rejected: hides silenced observations, cannot express a bound or an item lift, and conflicts with rule tuning. |
| Separate item columns for triage and for the foreground workflow | Rejected: two sources of truth for which item owns an episode ([D-TW65](./tillr-watch.md#D-TW65)). |
| Refuse to advance `seen_seq` on an unsettled alert | Rejected: a non-following `wait` would exit in a tight loop while the agent is investigating. Reminders after `unsettled_remind_after` give time without dropping the alert. |

---

## 11. Risks

| Risk | Mitigation |
| :--- | :--- |
| A noisy alert is settled with a vague placeholder item or an over-broad silence | Investigation requires concrete cause and evidence; a silence needs a binding, an open item and a bound, lifts when the item finishes, and stays visible in status and the Observatory. |
| A partial binding on a composite rule mutes an unrelated subject | Silencing an alert binds its full tuple; a partial binding happens only when written explicitly, and the exact binding is displayed. |
| Leaving auto-resolved episodes unsettled creates a backlog on rollout | Rows already resolved at migration are marked settled once. |
| Stored events carry no actor | The page shows "not recorded"; every new transition records `--by`. |
| Reminders repeat while an alert waits on the owner | Episodes escalated to the owner are excluded from reminders. |

---

## Open Questions

None.

---

## Decision Ledger

Rows `R-AW1` to `R-AW6` and [OQ-AW1](#OQ-AW1) to [OQ-AW3](#OQ-AW3) are owner rulings. Every `D-AW` row is the designer's decision, and the owner may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="R-AW1"></a>**R-AW1** | **Owner ruling ([D-TW64](./tillr-watch.md#D-TW64)).** Every alert gets an investigation that establishes why it fired and one of three actions: silence this parameterized instance, file work to improve the rule, or file work to fix the fault. Rules are parameterized by variables so a silence binds one variable value. There is no "known about, not filed" disposition. | 2026-11-22 | [§3](#3-the-alert-workflow-from-opened-to-investigated-to-settled), [§4](#4-parameterizing-rules-by-variables-and-scoping-silences-to-rule_id-variable_binding) | No |
| <a id="R-AW2"></a>**R-AW2** | **Owner ruling ([OQ-TW2](./tillr-watch.md#OQ-TW2)).** Watch rules and alert handling apply no automatic remedies. | 2026-11-21 | [§9](#9-what-this-does-not-do) | — |
| <a id="R-AW3"></a>**R-AW3** | **Owner ruling ([the judge and lifecycle design](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27), [D-TW2](./tillr-watch.md#D-TW2)).** No silent fallbacks; alerts never fail, bench or cap queue work. | 2026-11-20, 2026-11-21 | [§3](#3-the-alert-workflow-from-opened-to-investigated-to-settled) | — |
| <a id="R-AW4"></a>**R-AW4** | **Owner ruling.** Acknowledging an alert covers one instance only and never ignores later instances. | 2026-11-23 | [§5.1](#51-single-instance-watermarks-on-ack-and-settle-versus-active-silence-suppression) | Yes |
| <a id="R-AW5"></a>**R-AW5** | **Owner ruling.** Continuing suppression is parameterized by what is ignored, linked to an open queue item, and lifted automatically once that item is in. | 2026-11-23 | [§3.3](#33-step-2--the-closed-set-of-three-outcomes), [§4.2](#42-durable-watch_silences-and-how-one-variable-is-muted-without-muting-others) | Yes |
| <a id="R-AW6"></a>**R-AW6** | **Owner ruling.** Watch alerts are visible in the Observatory with their current status, when they fired and what was done; the queue items linked to an alert are the record of action, and no other action tracking exists. | 2026-11-22 | [§5.2](#52-why-scalar-tracking_item_id-alone-is-not-enough--and-durable-bidirectional-alert_item_links), [§6](#6-making-unsettled-alerts-unmistakable--and-showing-every-alert-transition-and-linked-item-in-the-observatory) | Yes |
| <a id="OQ-AW1"></a>[OQ-AW1](#OQ-AW1) | **Owner ruling.** Every silence carries `--until` or `--until-change` in addition to its open item; whichever comes first lifts it. There is no standing silence. | 2026-11-23 | [§3.3](#33-step-2--the-closed-set-of-three-outcomes), [§4.2](#42-durable-watch_silences-and-how-one-variable-is-muted-without-muting-others) | Yes |
| <a id="OQ-AW2"></a>[OQ-AW2](#OQ-AW2) | **Owner ruling.** An episode that resolves on its own (`condition-cleared`, or its subject item finishing or closing) stays unsettled until it is investigated and given one of the three outcomes. | 2026-11-23 | [§3.1](#31-separating-condition-state-single-instance-ack-and-workflow-settlement), [§3.4](#34-alignment-with-the-alert-triage-seat-and-benign-triggers) | Yes |
| <a id="OQ-AW3"></a>[OQ-AW3](#OQ-AW3) | **Owner ruling.** A zero-variable global rule uses the same item-linked, bounded silence path, because a rule that cannot be silenced trains operators to ignore alerts. | 2026-11-23 | [§4.1](#41-how-rules-declare-and-expose-named-variables), [§4.2](#42-durable-watch_silences-and-how-one-variable-is-muted-without-muting-others) | Yes |
| <a id="D-AW1"></a>**D-AW1** | Track settlement apart from condition state, add `tillr watch investigate`, refuse any settlement without a recorded investigation, and keep auto-resolved episodes unsettled. | 2026-11-23 | [§3.1](#31-separating-condition-state-single-instance-ack-and-workflow-settlement), [§3.2](#32-step-1--mandatory-investigation-before-any-settlement) | Yes |
| <a id="D-AW2"></a>**D-AW2** | Retire prose-only `resolve` and `track`; enforce the closed outcome set through actor-neutral `silence` and `settle` that verify local items on `silence`, `fix-rule` and `fix-fault` (and, as amended by [`D-AW14`](#D-AW14), accept `--outcome external-fix` without `--item` when the fix landed outside the store), sharing one store seam with the triage seat. | 2026-11-23, amended 2026-11-27 | [§3.3](#33-step-2--the-closed-set-of-three-outcomes), [§3.4](#34-alignment-with-the-alert-triage-seat-and-benign-triggers), [§3.5](#35-settling-a-fix-that-landed-outside-the-store-d-aw14) | Yes (`external-fix` tracked in `D-AW14`) |
| <a id="D-AW3"></a>**D-AW3** | Derive named variables from `group_by`, support an optional `[rule.variables]` table, and store the resulting map in `variables_json`. | 2026-11-23 | [§4.1](#41-how-rules-declare-and-expose-named-variables) | No |
| <a id="D-AW4"></a>**D-AW4** | Persist silences in `watch_silences` keyed by rule and canonical variable binding, with a required investigation, actor, reason, open item and bound; keep them across quiet gaps; lift them on item completion or bound expiry. | 2026-11-23 | [§4.2](#42-durable-watch_silences-and-how-one-variable-is-muted-without-muting-others) | Yes |
| <a id="D-AW5"></a>**D-AW5** | Share one `tracking_item_id` across item-linked `settle`, silences and triage outcomes (`NULL` on `external-fix`, [`D-AW14`](#D-AW14)), and bound every `settle` to the settled instance so it never acts as a mute across instances. | 2026-11-23, amended 2026-11-27 | [§5.1](#51-single-instance-watermarks-on-ack-and-settle-versus-active-silence-suppression) | Yes (`external-fix` tracked in `D-AW14`) |
| <a id="D-AW6"></a>**D-AW6** | Surface unsettled alerts in `tillr watch status`, `tillr watch wait` reminders (default `15m`), a standing `tillr drain why` alarm, and the Observatory. | 2026-11-23 | [§6.1](#61-cli-status-wait-reminders-and-the-standing-drain-alarm) | Yes |
| <a id="D-AW7"></a>**D-AW7** | Enforce the concurrency, reopen, auto-lift, refusal and migration behavior of [§7](#7-edge-cases-concurrency-and-failure-modes). | 2026-11-23, amended 2026-11-27 | [§7](#7-edge-cases-concurrency-and-failure-modes) | Yes (`external-fix` tracked in `D-AW14`) |
| <a id="D-AW8"></a>**D-AW8** | Build in the eight slices of [§8](#8-the-build-in-slices-that-each-land-alone), single-instance `ack` first, followed by the triage-seat slices. | 2026-11-23 | [§8](#8-the-build-in-slices-that-each-land-alone) | Slices 1, 3, 4, 5, 6, 7, 8 |
| <a id="D-AW9"></a>**D-AW9** | Reject the alternatives of [§10](#10-alternatives-considered) (amended by [`D-AW14`](#D-AW14) to distinguish structured `external-fix` columns on `alert_episodes` from a separate free-text action table). | 2026-11-23, amended 2026-11-27 | [§10](#10-alternatives-considered) | — |
| <a id="D-AW10"></a>**D-AW10** | Inject queue-item verification for item-linked outcomes through an optional seam on `TillrStoreSource` rather than importing `tillr-item` or `tillr-drain` into the watch engine, while allowing `--outcome external-fix` without `--item` both inside and outside a `tillr` store ([`D-AW14`](#D-AW14)). | 2026-11-22, amended 2026-11-27 | [§3.5](#35-settling-a-fix-that-landed-outside-the-store-d-aw14), [§4.3](#43-preserving-domain-neutrality-for-standalone---state-users) | Yes (`external-fix` tracked in `D-AW14`) |
| <a id="D-AW11"></a>**D-AW11** | Implement single-instance acknowledgement with the `acked_seq`, `acked_fingerprint` and `acked_lag_pair` watermarks: `ack` refuses `--item`, never blocks `condition-cleared` ([D-TW72](./tillr-watch.md#D-TW72)), and a newer instance reopens the episode, keeping `cause = "reopen-when"` where a `lag_behind` rule's `reopen_when` matched ([D-TW75](./tillr-watch.md#D-TW75)). | 2026-11-24 | [§5.1](#51-single-instance-watermarks-on-ack-and-settle-versus-active-silence-suppression) | Yes |
| <a id="D-AW12"></a>**D-AW12** | Persist alert-to-item links in `alert_item_links`, written by item-linked `settle`, `silence` and `tillr item request --alert` (`external-fix` writes none, [`D-AW14`](#D-AW14)), with a backfill and a read-only projection for stores without the table. | 2026-11-23, amended 2026-11-27 | [§5.2](#52-why-scalar-tracking_item_id-alone-is-not-enough--and-durable-bidirectional-alert_item_links), [§6.5](#65-bidirectional-alert-and-item-links-across-alertid-itemid-and-cli-faces) | Yes |
| <a id="D-AW13"></a>**D-AW13** | Expose read-only Observatory alert faces: the `tillr-view alerts` command, the alert HTTP routes and WebSocket topics, the alerts ledger, the alert page, the Home panel and the item page section. | 2026-11-23 | [§6.2](#62-observatory-surfaces-what-is-built-today-versus-what-is-new) | Yes |
| <a id="D-AW14"></a>**D-AW14** | **Designer's decision from a 2026-11-27 filed request from a consumer repository, answering a build run's conflict against `D-AW2`, `D-AW5`, `D-AW9` and `D-AW10` (not an owner ruling; the owner may overturn any part of it).** Amend `D-AW2`, `D-AW5`, `D-AW9` and `D-AW10` by adding `--outcome external-fix` to `tillr watch settle` (`Evaluator.Settle` in `tools/tillr/watch/lifecycle.go` and CLI in `tools/tillr-watch/lifecycle.go`) for fixes that landed outside the local store: require a recorded or inline investigation (`--cause` and `--evidence`), non-empty `--by <actor>` and `--reason <text>` (not `"unknown"`), and exactly one external target (`--repo <repo> --commit <commit>` with both non-empty, or non-empty `--ref <text>`, mutually exclusive), while refusing `--item` and `--link-kind` on `external-fix` and refusing `--repo`, `--commit`, `--ref` and `--reason` on `fix-rule` and `fix-fault` so existing item-linked outcomes stay unchanged. Persist `settled_repo`, `settled_commit`, `settled_ref` and `settled_reason` on `alert_episodes` (`hasExternalSettleColumns` / `NeedsSettleMigration` and `memEpisode`), emit `watch.alert.settled` with `outcome: "external-fix"`, `by`, `reason`, `settled_seq` and `repo`+`commit` or `ref` (no `alert_item_links` row), clear `escalated_to` and `active_triage_run_id` and set `triage_state = 'settled'` on `Settle` (skipping `watch.alert.escalated` in `Pending` / `readPending` once `settlement_state == 'settled'`) so settled alerts stop reminding and leave escalated views, reopen on any newer instance past `settled_seq` / `settled_fingerprint` / `settled_lag_pair` clearing `settled_*` columns, surface the external fix fields in `tillr watch status`, `ReadAlertEpisodes`, `tillr-view alerts` and Observatory (`AlertsPage.tsx`), and run `just pack-sync`. **Rejected:** allowing `--outcome expected` or settling without investigation/reason/target; fabricating placeholder local queue items for upstream fixes; or adding a separate free-text action table. | 2026-11-27, designer | [§3.3](#33-step-2--the-closed-set-of-three-outcomes), [§3.5](#35-settling-a-fix-that-landed-outside-the-store-d-aw14), [§5.1](#51-single-instance-watermarks-on-ack-and-settle-versus-active-silence-suppression) | No |
