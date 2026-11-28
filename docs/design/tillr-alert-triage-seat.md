---
title: "Alert triage happens in a seat inside tillr, and only what it cannot settle reaches the operating session"
status: accepted
stage: BUILT
stage-since: 2026-11-28
updated: 2026-11-28
tags: [watch, alerting, triage, tillr, drain, design]
summary: "Standing tillr alerts route to a dedicated read-only triage seat on the cheap scout tier, dispatched by tillr-drain. Each triage run claims one unsettled episode instance, reads the evidence, and either settles it by filing or linking a fix-fault or fix-rule item, or escalates it to the operating session or the owner. Loop-liveness rules bypass the seat and route straight to the operating session."
next: "Build the seven slices of section 9 in order, after the alert-workflow slices they depend on."
depends-on:
  - tillr-watch.md
  - tillr-watch-alert-workflow.md
  - general-agent-watch.md
  - operator-decisions-inside-tillr.md
  - tillr-utility-contracts.md
vantage:
  status-chip: true
---

# Alert triage happens in a seat inside tillr, and only what it cannot settle reaches the operating session

**Status:** Every question is ruled; all seven slices of [§9](#9-the-build-in-slices-that-each-land-alone) (atomic triage claim, three-outcome `record-triage`, triage events, escalated bundle `triage` header, `tillr watch status --recent`, the `triage` and `operator-session` receivers, per-owner delivery for both, `watch-lint-liveness-receiver`, triage provenance and `dedup_key` idempotence on `tillr item request`, `linked_alerts` on `tillr item show` and `tillr drain why`, `SeatTriage`, the `triage` backend policy declaration and validation, `tools/tillr-run/backends/brief-template-triage.md`, the self-contained `tillr-run` triage guard, transition-driven alert triage dispatch in `tillr-drain` with `max_concurrent_triage`, stale claim recovery, failure escalation with `triage_outcome = "triage-failed"`, repeat-flood escalation, the standing watch rule cutover in `tools/tillr-watch/rules/` plus `--owner operator-session` in `yolo-packs/tillr/skills/tillr-operating/SKILL.md`, and the Observatory panels for unsettled and escalated alerts, settlements inside `tillr`, active silences, triage transitions, and linked-alert badges on item pages) are built. The 24 Group A and B rules in `tools/tillr-watch/rules/` set `receiver = "triage"`, the 2 Group C loop-liveness rules set `receiver = "operator-session"`, and the remaining unsorted rules keep `receiver = "foreground"`.

> **In short.** A standing `tillr-watch` episode goes first to a dedicated, remedy-free `triage` seat on the cheap `scout` tier. The seat reads the evidence and settles a clear single instance by linking an existing item or filing one deduplicated `fix-fault` or `fix-rule` item. It escalates to the operating session or the owner whenever the cause or the item match is uncertain, when repeats need a silence, or when a host action or an owner ruling is required.

**Why it matters.** Most alert wakeups are mechanical: read the event fields and logs, check whether an item already tracks the cause, file or link one. That work costs foreground context in the operating session and can be done by a cheap seat inside `tillr`.

**The shape.** A consumer in `tillr-drain` claims each unsettled episode instance at most once through `active_triage_run_id`, dispatches one `triage` run for it outside the build lanes, and enforces a read-and-file-only boundary on that run. The consumer sits above the domain-neutral watch engine of [the general agent watch design](./general-agent-watch.md) and adds nothing to it.

**Cost.** One short `scout`-tier run per newly opened or reopened instance, plus triage columns, escalation wiring and status views so settlements made inside `tillr` stay auditable.

**Start at** [§4](#4-who-reads-each-new-alert-inside-tillr) to [§6](#6-what-must-be-handed-upward-to-whom-and-how).

**Needs your ruling:** None (every open question is ruled; `D-AT1` to `D-AT12` in the [Decision Ledger](#decision-ledger) are designer's decisions, which the owner may overturn).

**Reads with:** [the alert investigation and outcome workflow](./tillr-watch-alert-workflow.md) (the single-instance `ack` and `settle`, the investigation record, the closed outcome set and the item-linked silences this seat calls), [the general agent watch design](./general-agent-watch.md) (the engine and per-owner delivery cursors), [the tillr-watch design](./tillr-watch.md) (whose deferred seat routing, [OQ-TW4](./tillr-watch.md#OQ-TW4), this carries out under the no-remedy rule of [OQ-TW2](./tillr-watch.md#OQ-TW2)), and [operator decisions inside tillr](./operator-decisions-inside-tillr.md) (what stays reserved for the owner).

**Words.** **Item**, **criterion**, **candidate**, **lane**, **seat** and **verdict** keep their meanings from [the vocabulary](./tillr-vocabulary.md#5-the-words). **Episode**, **subject key**, **condition fingerprint** and **`TimelineBundle`** keep their meanings from [the tillr-watch design](./tillr-watch.md#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5). **The operating session** is the foreground agent session that watches the drain and acts for the owner. Three terms are coined here:

- **Alert triage** *(coined here)*: the remedy-free investigation of one newly opened, reopened or unsettled auto-resolved alert instance inside `tillr`. It is not a repair run: it never changes worktrees, processes, holds, silences or rules.
- **Triage settlement** *(coined here)*: recording on an episode that `tillr` has accounted for its current observed instance by linking an existing item or filing a new one, without waking the operating session. It is not a dismissal: there is no settlement without an item.
- **Upward escalation** *(coined here)*: handing an episode that triage cannot or should not settle alone to the operating session or the owner, with the triage finding and any filed or linked item attached.

---

## 1. What the owner said

The owner rulings this design rests on, each recorded in the [Decision Ledger](#decision-ledger):

- A problem the operating session could settle without the owner belongs to a similarly responsible agent, process or ticket inside `tillr` ([R-AT1](#R-AT1)).
- Watching finds and fixes root causes; it applies no remedies ([R-AT2](#R-AT2), [OQ-TW2](./tillr-watch.md#OQ-TW2)).
- Seat routing for alerts is designed from the record of foreground triage rather than guessed beforehand ([R-AT3](#R-AT3), [OQ-TW4](./tillr-watch.md#OQ-TW4)).
- Leftover work in a lane is an alert for manual cleanup plus a filed root cause, never an automatic sweeper ([R-AT4](#R-AT4), [D-TW48](./tillr-watch.md#D-TW48)).
- No silent fallbacks, and an alert never caps, benches or fails work ([R-AT5](#R-AT5), [D-TW2](./tillr-watch.md#D-TW2)).
- Every alert gets an investigation and one of three outcomes: a parameterized silence, `fix-rule` or `fix-fault` ([R-AT6](#R-AT6), [R-AW1](./tillr-watch-alert-workflow.md#R-AW1)).
- Acknowledging covers one instance; continuing suppression is a parameterized, item-linked silence that lifts when the item is in ([R-AT7](#R-AT7)).
- The seat is a dedicated `triage` seat on the `scout` tier dispatched by `tillr-drain`, with conservative escalation ([OQ-AT1](#OQ-AT1)).
- There is no evidence-only `expected` settlement ([OQ-AT2](#OQ-AT2)).
- Loop-liveness rules route explicitly to the operating session, and no timer promotes untriaged alerts ([OQ-AT3](#OQ-AT3)).

Everything else in this document is the designer's decision (`D-AT1` to `D-AT12`), which the owner may overturn.

---

## 2. How this relates to the general agent watch design

[The general agent watch design](./general-agent-watch.md) keeps the watch engine free of `tillr` imports and `tillr` concepts (`D-GW9`). Alert triage is built strictly above that engine, on the `tillr` side (`D-AT1`):

```mermaid
flowchart TB
    subgraph Engine["General watch engine (no tillr imports)"]
        Src["TillrStoreSource"] --> Eval["Pattern and episode evaluator"]
        Eval --> Ep["alert_episodes + per-owner delivery cursors"]
    end

    subgraph TillrSide["Tillr alert triage consumer"]
        Ep -->|"receiver = triage"| Claim["Atomic episode claim\n(active_triage_run_id)"]
        Claim --> Seat["Read-only triage seat"]
        Seat -->|"file or link root cause"| Item["tillr-item"]
        Seat -->|"record outcome"| Settle{"Settled inside tillr?"}
    end

    Settle -->|"yes: filed or linked"| Quiet["Instance settled; visible in status,\nitem show and the Observatory; no wake"]
    Settle -->|"no: uncertainty, repeat flood,\nhost action"| OpSession["operating session"]
    Settle -->|"no: policy or scope choice"| Owner["owner"]
    Ep -->|"receiver = operator-session\n(loop liveness)"| OpSession
```

Three seams connect the two designs:

1. **The observation source.** `ObservationSource` and `TillrStoreSource` ([`source.go`](../../tools/tillr/watch/source.go), [`tillr_source.go`](../../tools/tillr/watch/tillr_source.go)) are built. Triage adds nothing to them; it consumes the episodes and `TimelineBundle` records (at most 8 KiB each) the evaluator already produces.
2. **Per-owner delivery.** [`delivery.go`](../../tools/tillr/watch/delivery.go) tracks `seen_seq` and `shown_seq` per owner handle in `foreground_delivery`, and delivers only rules whose receiver is `"foreground"`. Once delivery cursors are generalized so each `--owner <handle>` receives the episodes matching its receiver ([§6 of the general agent watch design](./general-agent-watch.md#6-where-episodes-cursors-and-overrides-live)), two receivers exist with no `tillr`-specific code in the engine: `--owner tillr-triage` for `receiver = "triage"`, and `--owner operator-session` for `receiver = "operator-session"` plus escalated episodes.
3. **Where watch tables live.** Triage reads and writes episodes through the contracted faces of [`tillr-watch`](./tillr-utility-contracts/tillr-watch.md) and files items through `tillr-item`, so either answer to [OQ-GW3](./general-agent-watch.md#OQ-GW3) (a sidecar watch database or `.tillr/state.db`) leaves triage behavior unchanged.

---

## 3. Sorting the 26 live rules by what settles them

Each rule gets an explicit receiver (`D-AT2`). The table sorts 26 rules. `config/tillr/watch/` holds 36 rules in 33 files (`rg -c '^id *=' config/tillr/watch`); the ten not sorted here are listed under the table and keep `receiver = "foreground"` until they are sorted.

| Group | Rules | What triage does | Reaches the operating session or the owner? |
| :--- | :--- | :--- | :--- |
| **A. Settleable inside `tillr`** (`receiver = "triage"`, 15 rules) | `fast-gate-run-slow`, `fast-gate-median-slow`, `gate-repeated-stall`, `land-squash-conflict`, `drain-repeated-item-conflicts`, `drain-repeated-verifier-refusals`, `drain-plan-output-unparseable`, `drain-repeated-step-dispatches`, `drain-dispatch-declined`, `drain-item-fault`, `drain-judge-unreadable`, `drain-store-busy`, `drain-trunk-fixer-filed`, `drain-landing-reverted`, `run-model-timeout` | Reads the bundle, `tillr drain why` and the named logs; checks open and recently landed items; links the instance to the item that owns the exact cause or files one deduplicated request; settles the instance. | No, when cause and item ownership are unambiguous. Escalates when either is uncertain, when repeats need a silence, or when a live blockage (a wedged run, an external store lock) needs hand intervention. |
| **B. Diagnose and file inside `tillr`, then escalate the live action** (`receiver = "triage"`, 9 rules) | `drain-lane-refused`, `drain-trunk-core-worktree`, `drain-revert-not-possible`, `drain-heavy-fault`, `trunk-proof-lag`, `trunk-proof-stalled`, `drain-obstacle-hold`, `drain-backend-closed`, `drain-unrequested-start` | Reads the fault, proof directory, stacks, backend error or hold record; files or links the root-cause item first; settles quietly only when a mover inside `tillr` already owns the condition and no host action is needed. | Yes, whenever a live intervention, a backend or credential recovery, or an owner ruling is needed. The operating session receives the diagnosis and the item, and performs only the physical step. |
| **C. Loop liveness** (`receiver = "operator-session"`, 2 rules) | `drain-loop-no-pass`, `drain-loop-fault` | Nothing: these fire when the drain loop is stopped or faulted, so a seat dispatched by the drain cannot run ([§4.3](#43-explicit-loop-liveness-routing-with-no-timer-promotion)). | Directly to the operating session. |

Not yet sorted: `drain-checkpoint-not-restarted`, `drain-item-fifth-attempt`, `drain-lane-kept`, `drain-pi-build-stale`, `drain-repeated-refusal`, `drain-restart-unanswered`, `drain-test-slots-free-with-waiters`, `gate-drain-stalls`, `gate-verdict-overdue`, `land-repeated-squash-conflicts`.

---

## 4. Who reads each new alert inside tillr

### 4.1 The `triage` seat on the `scout` tier and its isolation

Alert triage runs on a dedicated `triage` seat mapped to the `scout` tier and dispatched by `tillr-drain` outside the build lanes ([OQ-AT1](#OQ-AT1), `D-AT3`, `D-AT11`, `D-AT12`). The concrete file locations, backend key, brief template and isolation plumbing below are the designer's decisions, which the owner may overturn:

1. **Seat declaration and `scout`-tier mapping (`D-AT3`, `D-AT11`).** Charter definitions ([`tools/tillr/definition/landing.json`](../../tools/tillr/definition/landing.json)) declare item lifecycle states and transitions and carry no `seats` key, so `landing.json` is unchanged. Instead, [`tools/tillr-item/item/seat.go`](../../tools/tillr-item/item/seat.go) adds `SeatTriage = "triage"` (excluded from the design-document and design-provenance brief rules), and every backend policy file in `config/tillr/` (`backend-policy*.json`, including [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json)) declares `"triage"` in `seats` mapped to that policy's `scout`-tier backend (`"pi-scout"` in the tracked example, matching the backend assigned to `"scout"` rather than naming the seat `"scout"` as a backend), lists `"triage"` in `read_only_seats`, gives `seat_purposes["triage"]` a one-line purpose, and sets `"escalate"` with `"triage": []`. Policy validation in [`tools/tillr-drain/internal/policy/seat_escalation.go`](../../tools/tillr-drain/internal/policy/seat_escalation.go) (`tillr drain policy`) and [`tools/tillr/watch/lint_workflow.go`](../../tools/tillr/watch/lint_workflow.go) refuses (exit `2`) any policy that declares `"triage"` in `seats` with a non-empty `escalate["triage"]` or omits `"triage"` from `read_only_seats`: there is no automatic move to a higher model tier ([R-AT5](#R-AT5)). An uncertain run, an unreadable input or a closed `scout`-tier backend escalates the episode to the operating session instead.
2. **No lane worktree (`D-AT3`, `D-AT6`).** Triage edits no tracked file and produces no candidate. Each run executes in `<lane_dir>/work` (`working_dir = "work"` on a dedicated `triage-1`..`triage-<N>` slot, never an ordinary or express lane) against a read-only view of the repository root and writes files only under its own `<lane_dir>/durable/<run_id>` run scratch directory (`run.DurableDir` and `TMPDIR`), plus adapter-managed `<lane_dir>/stream.err` and trace files.
3. **Bounded concurrency (`D-AT3`).** Up to `max_concurrent_triage` runs (default `2`) run in parallel, independent of how busy the build lanes are.
4. **One instance per run and the triage brief (`D-AT12`).** Staging renders `tools/tillr-run/backends/brief-template-triage.md` (mirrored into the `tillr` pack by `just pack-sync`). The template carries all ten `##` headings required by [`tools/tillr-run/backends/preflight.sh`](../../tools/tillr-run/backends/preflight.sh) (`Header`, `Task`, `Scope and boundaries`, `Seats`, `Settled decisions`, `Facts carried`, `Method`, `Verification commands`, `Report shape`, `Stop conditions`) and sets `- **Target:** {{TARGET}} (read-only root view)` in `Header` so built-in adapters pass preflight without enabling lane-worktree `keep-going` or landing-call hooks. Staging fills `{{ALERT_ID}}`, `{{RULE_ID}}`, `{{RUN_ID}}`, `{{BACKEND}}`, `{{STORE_PATH}}`, `{{REPO_ROOT}}`, `{{TARGET}}`, `{{DURABLE_DIR}}`, `{{DATE}}`, `{{TIMEOUT_SECONDS}}`, `{{SEAT_LINES}}`, `{{EPISODE_METADATA}}` (ids, subject key, state, fingerprint, repeat count, `reopen_count_for_item` and variables), `{{TIMELINE_BUNDLE}}`, and `{{CORRELATION_CONTEXT}}` (recent active episodes and active silences). Each run must record one structured triage outcome for `{{ALERT_ID}}` via `tillr watch record-triage` and end with `RESULT: done`.

### 4.2 Transition-driven trigger and the `active_triage_run_id` invariant

Triage is driven by episode transitions, never by polling (`D-AT4`).

- **Eligible.** An episode is eligible when its rule's receiver is `"triage"`, its `triage_state` is `'untriaged'`, `active_triage_run_id` is null, and it is either `open` or `resolved` with `settlement_state = 'uninvestigated'` (an auto-resolved episode still owes an investigation under [OQ-AW2](./tillr-watch-alert-workflow.md#OQ-AW2)).
- **Claim.** `tillr-drain` claims the episode in one `IMMEDIATE` transaction that sets `active_triage_run_id` and `triage_state = 'triaging'` only where both still hold their unclaimed values. Two passes racing on one episode cannot both claim it.
- **No repeat on a settled instance.** While evaluation re-reads the same settled event sequence, fingerprint and lag pair, the episode stays `settled` or `escalated`.
- **Re-triage on a new instance.** A newer matching event, a changed fingerprint or a changed lag pair, with no active silence, reopens the episode and resets `triage_state = 'untriaged'` ([R-AW4](./tillr-watch-alert-workflow.md#R-AW4), [D-AW5](./tillr-watch-alert-workflow.md#D-AW5)).

### 4.3 Explicit loop-liveness routing with no timer promotion

Routing `drain-loop-no-pass` or `drain-loop-fault` to the triage seat would deadlock: when the loop is down, nothing in `tillr-drain` is alive to triage the alert that says so. Routing is therefore explicit on the rules ([OQ-AT3](#OQ-AT3), `D-AT5`):

- Rules whose subject is the liveness of `tillr-drain` set `receiver = "operator-session"`.
- `tillr watch lint` rejects any rule that watches the absence of `drain.loop-pass`, or `drain.loop-ended`, with `receiver = "triage"` (check id `watch-lint-liveness-receiver`).
- No timer promotes an untriaged episode. Other `triage` episodes wait for `tillr-drain` to claim them.

---

## 5. What the triage seat may do — and what it must never do

The no-remedy ruling ([OQ-TW2](./tillr-watch.md#OQ-TW2)) is enforced on the seat as a command and filesystem boundary, not as prompt advice (`D-AT6`).

### 5.1 Permitted actions

1. **Read.** The episode bundle; the read-only faces of `tillr view` (every query face except `serve`), `tillr item` (`list`, `show`), `tillr run` (`status`, `lanes`), `tillr drain` (`why`, `workers`, `policy`, `workflow`), `tillr watch` (`status`, `lint`), `tillr store` (`inventory`, `definition`, `check`, `events`) and `tillr trace` (`tick`, `audit`, `run`, `cost`, `discrepancies`); run logs, gate timing records and proof verdicts; read-only `git` inspection.
2. **Deduplicate.** Search open and recently landed items for one that already owns the root cause. A match needs evidence of the same cause (the same failing check, conflicting paths, fault reason and step, or adapter error), never title similarity.
3. **File or link one item.** Link the alert to the existing item, or file one request via `tillr item request` that states what happened with its evidence and the single defect to fix. The request must carry `--filed-by tillr-triage`, `--alert <alert_id>`, and `--dedup-key <rule_id>:<cause_key>` so two runs diagnosing one bug land on one item. Where store events establish that a held item waits on the defect, triage may record that edge with `tillr item dep --add` (never `--remove`). Express or ordinary filing follows the repository's standing rules for defects.
4. **Record the outcome** on the episode through `tillr watch record-triage` ([§5.3](#53-the-structured-triage-outcomes)).

### 5.2 Forbidden actions (hard mechanical boundary)

| Category | Forbidden to the triage seat | Why |
| :--- | :--- | :--- |
| **Worktree and Git state** | Editing or deleting files outside run scratch; `git commit`, `add`, `rm`, `mv`, `reset`, `checkout`, `switch`, `restore`, `clean`, `stash`, `cherry-pick`, `rebase`, `merge`, `apply`, `push`, `fetch`, `pull`, `update-ref`, `symbolic-ref`; any `git worktree` or `git config` write; `tillr land`, `tillr gate`, `just`. | Cleaning a lane or editing code from triage destroys evidence or bypasses the gated item workflow ([D-TW48](./tillr-watch.md#D-TW48)). |
| **Process and loop control** | `kill` (binary and shell builtin), `pkill`, `killall`, `systemctl`, `service`; `tillr drain stop`, `loop`, `once`, `plan`, `restart`, `resume`, `scale`, `boundary`; `tillr-keep`; `tillr run exec`; `tillr view serve`. | Operational control is not finding and filing a root cause. |
| **Hold, fault and item lifecycle** | `tillr drain retry`, `clear-fault`, `accept-rewrite`, `acknowledge-patch-move`, `acquit`, `examine`, `set-aside`, `split`, `push`, `sweep-refs`, `keep`, `fork-gate`, `probe-backend`; `tillr item` outside `list`, `show`, triage-tagged `request`, and `dep --add`; `tillr store init`, `seed`, `recover`, `checkpoint`; `sqlite3`. | These belong to the takers and closing checks of [operator decisions inside tillr](./operator-decisions-inside-tillr.md#5-the-seven-kinds). |
| **Watch suppression and foreground settlement** | `tillr watch silence`, `rule`, `ack`, `investigate`, `settle`, `claim-triage`, `eval`, `daemon`, `wait`, `seen`, and legacy or unknown verbs (`resolve`, `drop`). | A seat that can mute, retune or settle outside `record-triage` can silence its own alerts instead of fixing their causes. |

**Enforcement (designer's decision, which the owner may overturn).** [The lane shims design](./tillr-lane-shims.md) is still unbuilt (`stage: DESIGN`), targets lane writers with worker bypass variables, and uses exit `1` (`D-LS3`). Slice 4 therefore builds a self-contained triage guard in `tools/tillr-run/run/` activated whenever `Request.Seat == "triage"`, extending the per-run `PATH`-wrapper pattern of [`tools/tillr-run/run/git_guard.go`](../../tools/tillr-run/run/git_guard.go) with no bypass variable:

1. **Allowlist command shim (exit `2`).** Before starting the adapter, `tillr-run` prepends `<durable_dir>/triage-bin` to `PATH`, sets `GIT_OPTIONAL_LOCKS=0` and `TILLR_PI_AGENT_DIRS_DIR=""`, and points both `BASH_ENV` and `TILLR_SEAT_BASH_ENV` at `<durable_dir>/triage-bash-env.sh` (sourced at the end of [`tools/tillr-trace/bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh) when an adapter resets `BASH_ENV`). Wrappers in `triage-bin` cover `tillr` and every `tillr-*` binary, `git`, `just`, `sqlite3`, `kill`, `pkill`, `killall`, `systemctl`, `service`, and file-mutating utilities (`rm`, `mv`, `cp`, `chmod`, `chown`, `ln`, `touch`, `truncate`, `tee` when any target resolves outside `<durable_dir>` or `TMPDIR`), while `triage-bash-env.sh` overrides the `bash` `kill` builtin. Only the faces of [§5.1](#51-permitted-actions) (plus `-h`/`--help`/`--version` and internal `tillr-run` adapter helpers `pi-build-agent-dir`, `pi-stop-at-gap`, `pi-compact`, `extract-report`) pass through to resolved real binaries; every other invocation—including `tillr item request` missing `--filed-by tillr-triage`, `--alert` or `--dedup-key`, and `tillr item dep --remove`—prints a one-line refusal naming the `triage` boundary to stderr and exits `2` ([`record.ExitUsage`](../../tools/tillr/record/exit.go)).
2. **Read-only root view.** On Linux when `CLONE_NEWNS` is permitted (including `yolo-jail`), `tillr-run` launches the run in a private mount namespace that bind-mounts the repository root read-only (`MS_RDONLY`) onto `<lane_dir>/work` and remounts the checkout read-only outside `<durable_dir>`, `<lane_dir>`'s adapter files, the run's trace directory and the store directory mediated by `tillr-item` and `tillr-watch` (keeping sibling lanes read-only). Where `CLONE_NEWNS` is unavailable (`EPERM` or non-Linux), `<lane_dir>/work` exposes top-level checkout symlinks (excluding `.tillr` and `.yolo`) inside a `0555` directory while the command shim enforces the boundary.

### 5.3 The structured triage outcomes

Every run ends by recording one of three outcomes (`D-AT7`), written into the shared investigation and `tracking_item_id` schema of [the alert workflow](./tillr-watch-alert-workflow.md#33-step-2--the-closed-set-of-three-outcomes) ([D-TW65](./tillr-watch.md#D-TW65)). There is no `expected` outcome ([OQ-AT2](#OQ-AT2)).

| Outcome | Required fields | Effect on the episode | Wakes the operating session? |
| :--- | :--- | :--- | :--- |
| **`filed`** | The new item's id, `outcome_kind` (`fix-fault` or `fix-rule`), a `finding` that cites event sequences or log evidence | The observed instance is settled against the item; the claim is released. | No |
| **`linked`** | The existing item's id, `outcome_kind`, a `finding` that proves the item covers this instance | Same as `filed`. | No, unless a repeat flood also escalates ([§6.1](#61-handed-to-the-operating-session-escalate_to--operator-session)) |
| **`escalated`** | `escalate_to` (`operator-session` or `owner`), `finding`, `action_needed` (or `question` and `options` for the owner); an item id and `outcome_kind` whenever a concrete defect was identified | `triage_state = 'escalated'`; settled if an item is attached, otherwise investigated and still unsettled; appends `watch.alert.escalated`. | Yes |

A benign operation that fires a rule shows the rule lacks the predicate to tell the benign case apart. Triage links or files a `fix-rule` item for it; it never dismisses the alert in prose. When a defect's fix lives outside the local store (for example, a commit in upstream `tillr` for a repository running from the installed pack), triage escalates to `operator-session` with its finding rather than inventing a local item, and the operating session settles the episode via `tillr watch settle --outcome external-fix` ([`D-AW14`](./tillr-watch-alert-workflow.md#D-AW14)).

---

## 6. What must be handed upward, to whom, and how

The seat settles only clear, high-confidence single-instance matches and escalates whenever confidence, authority or host reach is missing (`D-AT8`, `D-AT11`).

### 6.1 Handed to the operating session (`escalate_to = "operator-session"`)

1. **Uncertainty.** The evidence is incomplete, two causes remain plausible, or it is unclear whether a new instance shares the cause of an open item. Triage records its finding and escalates rather than guessing.
2. **A silence decision.** Suppressing a noisy binding needs `tillr watch silence`, which triage may not run. That includes the repeat flood: once an open tracking item has reopened three times (`reopen_count_for_item >= 3`) with no active silence, triage links the instance and escalates with the exact silence command for the operator to decide on.
3. **Manual preservation and workspace hygiene.** For `drain-lane-refused`, triage reads who left the work, files or links the root-cause item, then escalates the cleanup ([D-TW48](./tillr-watch.md#D-TW48)). For `drain-trunk-core-worktree`, it identifies the run that set `core.worktree`, files or links the defect, then escalates the config repair.
4. **A live blockage.** A closed core backend, an unexpected second loop process, a proof or revert that nothing inside `tillr` is advancing, or a run or lock holder still wedging a lane or the store. Triage files or links the defect first and escalates the live details.
5. **A hold addressed to the operator.** If `drain-obstacle-hold` names `mover == "operator"`, triage gathers the context and escalates it.
6. **Triage itself failed.** If the run exits non-zero, times out, records no valid outcome, or the `scout` backend is closed, `tillr-drain` clears the claim, records `triage_outcome = "triage-failed"`, and escalates immediately with the original bundle and the run's error. There is no retry and no silent drop.
7. **A fix outside the local store ([`D-AW14`](./tillr-watch-alert-workflow.md#D-AW14)).** When the root cause belongs to another repository (such as upstream `tillr` in a consumer repository running from the installed pack) so no local queue item should be filed, triage escalates with its investigation finding, and the operating session settles the episode via `tillr watch settle <alert-id> --by <actor> --outcome external-fix (--repo <repo> --commit <commit> | --ref <text>) --reason <text>`, clearing `escalated_to` and `active_triage_run_id`, marking `triage_state = 'settled'`, and stopping unsettled reminders.

### 6.2 Handed to the owner (`escalate_to = "owner"`)

Triage escalates to the owner only for the decisions [operator decisions inside tillr](./operator-decisions-inside-tillr.md#6-what-is-the-owners) reserves: weakening or dropping what the owner asked for, a contradiction between two owner statements, reordering queue work beyond a recorded blocker, choosing new work when ordered work runs out ([OQ-OD2](./operator-decisions-inside-tillr.md#OQ-OD2)), credentials or spend outside the repository, an irreversible or externally published act, and seat or model policy (including `drain-obstacle-hold` with `mover == "owner"`).

An escalation to the owner carries a structured `question` and concrete `options`, or links the open owner hold that already carries them. Neither triage nor the operating session guesses an owner decision.

### 6.3 The upward delivery wire

Escalation reuses the `wait` and `seen` protocol of [the general agent watch design](./general-agent-watch.md#7-how-an-agent-in-any-harness-is-woken) (`D-AT8`):

1. The operating session runs one `tillr watch wait ... --owner operator-session` watcher (the timed drain check-in skill (an operating skill kept outside this repository)).
2. For that owner, pending notices are the unsettled episodes of `receiver = "operator-session"` rules, the episodes of `triage` rules with a `watch.alert.escalated` event newer than the owner's `seen_seq`, and the unsettled reminders of [the alert workflow](./tillr-watch-alert-workflow.md#6-making-unsettled-alerts-unmistakable--and-showing-every-alert-transition-and-linked-item-in-the-observatory). Episodes still untriaged, being triaged, or settled inside `tillr` do not wake it.
3. An escalated notice leads with a `triage` block (run id, outcome, `escalated_to`, item id, finding, and `action_needed` or `question` with `options`), placed ahead of raw event lines so truncation to 8 KiB never drops the triage conclusion.
4. Printing a notice advances `shown_seq` only. `seen_seq` advances when the operating session re-arms with `--seen-through` or runs `tillr watch seen` ([D-TW11](./tillr-watch.md#D-TW11)), so a lost turn never drops an escalated alert.

---

## 7. How every triage outcome is visible

Settling alerts inside `tillr` removes wakeups, not visibility (`D-AT9`):

1. **`tillr watch status`.** Each episode row carries a `triage` object (`state`, `outcome`, `outcome_kind`, `run_id`, `item_id`, `finding`), the text output a one-line roll-up of triage states, and `--recent <window>` shows episodes that opened, were triaged and resolved within the window.
2. **Events.** `watch.alert.triage-started`, `watch.alert.triaged` and `watch.alert.escalated` record every claim, settlement, failure and escalation.
3. **Item cross-reference.** `tillr item show` and `tillr drain why` list the alerts linked to an item (`linked_alerts`).
4. **The Observatory.** Unsettled and escalated episodes lead; episodes settled inside `tillr` and active silences follow, each linking to the triage run and the tracking item ([§6 of the alert workflow](./tillr-watch-alert-workflow.md#6-making-unsettled-alerts-unmistakable--and-showing-every-alert-transition-and-linked-item-in-the-observatory)).

---

## 8. Edge cases, concurrency, and failure modes

| Case | Behavior |
| :--- | :--- |
| **No open or unsettled alerts** | No triage run is dispatched and no model is called. |
| **A burst of episodes in one pass** | Up to `max_concurrent_triage` are claimed in `opened_seq` order; the rest stay visibly `untriaged` and dispatch as runs finish. |
| **Two alerts for one underlying bug** | `tillr item request` checks open triage-filed items by `dedup_key` in an `IMMEDIATE` transaction; the second run links to the item the first created. |
| **Episode auto-resolves before or during triage** | Triage still records `filed`, `linked` or `escalated` on the resolved episode, without reopening it. |
| **New instance while the tracking item is open and no silence exists** | The episode reopens as `untriaged` and the repeat count rises. Triage checks whether the new instance shares the item's cause, and escalates at the third reopen. |
| **Cause or item match is ambiguous** | Triage records its evidence and escalates to the operating session. |
| **The linked item lands or closes and the condition recurs** | The episode reopens with the prior item in its history: the fix was incomplete. |
| **Triage run fails or records nothing** | The claim is cleared and the episode escalates immediately ([§6.1](#61-handed-to-the-operating-session-escalate_to--operator-session)), with one deduplicated defect item if a tool fault caused it. |
| **`tillr-drain` dies holding a claim** | At startup and at pass settlement, each non-null `active_triage_run_id` is checked against `tillr run status`; a dead run escalates the episode and files one defect item. |
| **Rules not yet cut over** | A rule that keeps `receiver = "foreground"` behaves as it does today, so the cutover needs no flag day. |

---

## 9. The build, in slices that each land alone

Seven slices, in order, after the eight slices of [the alert workflow](./tillr-watch-alert-workflow.md#8-the-build-in-slices-that-each-land-alone) (`D-AT10`). All seven slices are built.

| Slice | What lands | Done when | Built |
| :--- | :--- | :--- | :--- |
| **1** | The atomic triage claim, the three-outcome record face (refusing `expected`), the triage events, the `triage` header in escalated bundles, and `tillr watch status --recent` (`D-AT4`, `D-AT7`). | Tests show single-claim serialization across open and auto-resolved episodes, the refusal of `expected`, that a settled instance does not wake the operating session while an escalated one does, and header priority under truncation. | Yes |
| **2** | The `triage` and `operator-session` receivers, per-owner delivery for both, and the `watch-lint-liveness-receiver` check (`D-AT2`, `D-AT5`). | Tests show each owner receives only its episodes, no timer promotes an untriaged alert, and lint rejects a liveness rule routed to `triage`. | Yes |
| **3** | Triage provenance and `dedup_key` idempotence on `tillr item request`, and `linked_alerts` on `tillr item show` and `tillr drain why` (`D-AT6`, `D-AT9`). | Tests show concurrent filings for one cause land on one item carrying both alert ids. | Yes |
| **4** | `SeatTriage` and the `triage` policy declaration in `config/tillr/backend-policy*.json` (`"pi-scout"` `scout`-tier mapping, `read_only_seats`, `"escalate": {"triage": []}` validation), `tools/tillr-run/backends/brief-template-triage.md`, and the self-contained `tillr-run` triage guard (`D-AT3`, `D-AT6`, `D-AT11`, `D-AT12`). | Policy validation refuses a non-empty `escalate["triage"]` or non-read-only `triage` with exit `2`, rendered `brief-template-triage.md` passes `preflight.sh`, and triage guard tests show permitted read/file commands succeed while each forbidden command category and out-of-scratch write is refused with exit `2`. | Yes |
| **5** | Transition-driven dispatch in `tillr-drain` with the concurrency cap, and escalation on failure, a stale claim or a repeat flood (`D-AT4`, `D-AT8`, `D-AT11`). | Tests show the cap holds while build lanes are busy, no repeat dispatch on an unchanged settled instance, and immediate escalation on failure with no model retry. | Yes |
| **6** | The rules in `config/tillr/watch/` cut over to their receivers (`D-AT2`), with the check-in skill updated. | `tillr watch lint` and the rule tests pass; the operating session wakes only on loop-liveness alerts, escalations and unsettled reminders. | Yes |
| **7** | The Observatory panels for unsettled alerts, settlements inside `tillr` and active silences, and linked-alert badges on item pages (`D-AT9`, [D-AW6](./tillr-watch-alert-workflow.md#D-AW6)). | `tillr-view` contract and UI tests cover each panel. | Yes |

---

## 10. What this does not do

- It puts no `tillr` concept into the general watch engine.
- It applies no remedy and creates no silence.
- It allows no evidence-only dismissal and adds no untriaged-timeout timer.
- It guesses no owner decision and does not gamble on a low-confidence `scout` call.
- It does not replace the takers of [operator decisions inside tillr](./operator-decisions-inside-tillr.md): a worker's reported obstacle or a fault cleared on a recorded fact is handled by the drain and the design seat. Triage watches cross-event anomalies and checks that each has an owning item or an escalation.

---

## 11. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Teach the general watch engine about seats, lanes and queue items | Rejected: breaks `D-GW9` of [the general agent watch design](./general-agent-watch.md#8-how-the-general-watcher-and-tillr-depend-on-each-other). |
| A fourth `expected` outcome for benign triggers | Rejected by the owner ([OQ-AT2](#OQ-AT2)): a benign action that fires an alert shows the rule needs fixing. |
| A timer promoting untriaged alerts to the operating session | Rejected by the owner ([OQ-AT3](#OQ-AT3)): explicit routing on loop-liveness rules covers the stopped-loop case without an arbitrary threshold. |
| Move a failed or uncertain triage run to a higher model tier | Rejected (`D-AT11`): it is an automatic fallback ([R-AT5](#R-AT5)). |
| Let the seat run safe-looking cleanup commands | Rejected: violates [OQ-TW2](./tillr-watch.md#OQ-TW2) and [D-TW48](./tillr-watch.md#D-TW48). |
| Run triage on the build lanes | Rejected: triage would queue behind long worker turns, including when the alert is about a wedged run on those lanes. |
| Poll `tillr watch status` from a recurring model turn | Rejected: spends tokens when nothing is wrong and reacts late when something is. |

---

## 12. Risks

| Risk | Mitigation |
| :--- | :--- |
| The `scout` seat links two distinct bugs to one item | A link must cite the matching check, path or fault signature; uncertainty escalates; every `linked` decision is visible in status and the Observatory; a recurrence after landing reopens the episode. |
| Single-instance settlement causes repeat triage runs while a slow fix is queued | The third reopen escalates with a silence command for the operator. |
| Triage files duplicate requests during a burst | Episodes deduplicate per rule and subject, concurrency is capped, and `tillr item request` deduplicates by `dedup_key`. |
| `tillr-drain` stops, stranding triage | Loop-liveness rules wake the operating session directly ([§4.3](#43-explicit-loop-liveness-routing-with-no-timer-promotion)). |
| Triage writes contend with the drain or the lander on the store | Triage writes only on episode transitions, never per event. |

---

## Open Questions

None.

---

## Decision Ledger

Rows `R-AT1` to `R-AT7` and [OQ-AT1](#OQ-AT1) to [OQ-AT3](#OQ-AT3) are owner rulings. Every `D-AT` row is the designer's decision, and the owner may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="R-AT1"></a>**R-AT1** | **Owner ruling.** A problem the operating session could solve without the owner escalates to a similarly responsible agent, process or ticket inside `tillr`, not to the operating session. | 2026-11-21 | [§1](#1-what-the-owner-said) | No |
| <a id="R-AT2"></a>**R-AT2** | **Owner ruling ([OQ-TW2](./tillr-watch.md#OQ-TW2)).** Watch rules and watch triage apply no remedies: watching finds issues so they are fixed, it does not cover them. | 2026-11-21 | [§5](#5-what-the-triage-seat-may-do--and-what-it-must-never-do) | No |
| <a id="R-AT3"></a>**R-AT3** | **Owner ruling ([OQ-TW4](./tillr-watch.md#OQ-TW4)).** Routing alerts to a seat inside `tillr` is designed from the record of foreground triage. | 2026-11-21 | [§3](#3-sorting-the-26-live-rules-by-what-settles-them) | No |
| <a id="R-AT4"></a>**R-AT4** | **Owner ruling ([D-TW48](./tillr-watch.md#D-TW48)).** Leftover work holding a lane out of rotation is an alert for manual preservation and cleanup plus a filed root cause, never an automatic sweeper. | 2026-11-21 | [§6.1](#61-handed-to-the-operating-session-escalate_to--operator-session) | No |
| <a id="R-AT5"></a>**R-AT5** | **Owner ruling.** No silent fallbacks ([the judge and lifecycle design](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27)), and alerts never cap, bench or fail work ([D-TW2](./tillr-watch.md#D-TW2), [the momentum principle](./momentum-principle.md)). | 2026-11-20, 2026-11-21 | [§6.1](#61-handed-to-the-operating-session-escalate_to--operator-session) | No |
| <a id="R-AT6"></a>**R-AT6** | **Owner ruling ([R-AW1](./tillr-watch-alert-workflow.md#R-AW1)).** Every alert gets an investigation and settles through a parameterized silence, `fix-rule` or `fix-fault`; there is no "known about, not filed" disposition. | 2026-11-22 | [§5.3](#53-the-structured-triage-outcomes) | No |
| <a id="R-AT7"></a>**R-AT7** | **Owner ruling ([R-AW4](./tillr-watch-alert-workflow.md#R-AW4), [R-AW5](./tillr-watch-alert-workflow.md#R-AW5)).** Acknowledging covers one instance and never ignores later ones; continuing suppression is parameterized, linked to an open item, and lifted automatically once that item is in. | 2026-11-23 | [§4.2](#42-transition-driven-trigger-and-the-active_triage_run_id-invariant), [§6.1](#61-handed-to-the-operating-session-escalate_to--operator-session) | No |
| <a id="OQ-AT1"></a>[OQ-AT1](#OQ-AT1) | **Owner ruling.** A dedicated `triage` seat on the `scout` tier, dispatched by `tillr-drain` outside the build lanes, with conservative upward escalation. | 2026-11-23 | [§4.1](#41-the-triage-seat-on-the-scout-tier-and-its-isolation), [§6.1](#61-handed-to-the-operating-session-escalate_to--operator-session) | No |
| <a id="OQ-AT2"></a>[OQ-AT2](#OQ-AT2) | **Owner ruling.** No `expected` or evidence-only settlement: a benign event that fires an alert is settled by filing or linking a `fix-rule` item, or escalated when a silence is needed. | 2026-11-23 | [§5.3](#53-the-structured-triage-outcomes) | Yes |
| <a id="OQ-AT3"></a>[OQ-AT3](#OQ-AT3) | **Owner ruling.** Loop-liveness rules route explicitly to `operator-session`; no timer promotes untriaged alerts. | 2026-11-23 | [§4.3](#43-explicit-loop-liveness-routing-with-no-timer-promotion) | Yes |
| <a id="D-AT1"></a>**D-AT1** | Build triage above the general watch engine as a `tillr`-side consumer, with no `tillr` imports or concepts in the engine, compatible with either answer to [OQ-GW3](./general-agent-watch.md#OQ-GW3). | 2026-11-22 | [§2](#2-how-this-relates-to-the-general-agent-watch-design) | No |
| <a id="D-AT2"></a>**D-AT2** | Split receivers into `triage` (Groups A and B) and `operator-session` (Group C loop liveness), sorting each rule by whether it settles inside `tillr`. | 2026-11-23 | [§3](#3-sorting-the-26-live-rules-by-what-settles-them) | Yes |
| <a id="D-AT3"></a>**D-AT3** | **Designer decision (owner may overturn).** Declare `SeatTriage = "triage"` in `tools/tillr-item/item/seat.go` and in `config/tillr/backend-policy*.json` (`seats["triage"]` mapped to the policy's `scout`-tier backend `"pi-scout"`, `"triage"` in `read_only_seats`, `seat_purposes["triage"]`, and `escalate["triage"] = []` enforced by policy validation; `landing.json` untouched), running in `<lane_dir>/work` with no Git worktree and a concurrency cap of `2` independent of the build lanes. | 2026-11-26 | [§4.1](#41-the-triage-seat-on-the-scout-tier-and-its-isolation) | Yes |
| <a id="D-AT4"></a>**D-AT4** | Use `active_triage_run_id` as an atomic claim on unsettled `triage` episodes, open or auto-resolved, at most once per observed instance. Rejected: polling. | 2026-11-23 | [§4.2](#42-transition-driven-trigger-and-the-active_triage_run_id-invariant) | Yes |
| <a id="D-AT5"></a>**D-AT5** | Enforce `watch-lint-liveness-receiver` so loop-liveness rules cannot route to the in-drain seat. | 2026-11-23 | [§4.3](#43-explicit-loop-liveness-routing-with-no-timer-promotion) | Yes |
| <a id="D-AT6"></a>**D-AT6** | **Designer decision (owner may overturn).** Enforce the boundary when `Request.Seat == "triage"` via a self-contained guard in `tools/tillr-run/run/` (independent of unbuilt [`tillr-lane-shims.md`](./tillr-lane-shims.md)): a `PATH` + `BASH_ENV` allowlist shim with no bypass variable that permits only read-only inspection, `tillr item request --filed-by tillr-triage --alert ... --dedup-key ...`, `tillr item dep --add`, and `tillr watch record-triage`, refusing every other `tillr`/`git`/process/DB/out-of-scratch command with exit `2` (`record.ExitUsage`), paired with a read-only root view (`CLONE_NEWNS` read-only bind mount where permitted, `0555` symlink view otherwise). | 2026-11-26 | [§5.1](#51-permitted-actions), [§5.2](#52-forbidden-actions-hard-mechanical-boundary) | Yes |
| <a id="D-AT7"></a>**D-AT7** | Require one of three structured outcomes per run (`filed`, `linked`, `escalated`); `filed` and `linked` settle the observed instance without a wake. | 2026-11-23 | [§5.3](#53-the-structured-triage-outcomes) | Yes |
| <a id="D-AT8"></a>**D-AT8** | Escalate only to `operator-session` or `owner`, delivered over `tillr watch wait` with the triage summary and item id leading the bundle; any failed triage run escalates. | 2026-11-23 | [§6](#6-what-must-be-handed-upward-to-whom-and-how) | Yes |
| <a id="D-AT9"></a>**D-AT9** | Make every settled and escalated outcome visible in `tillr watch status`, the triage events, item cross-references and the Observatory. | 2026-11-23 | [§7](#7-how-every-triage-outcome-is-visible) | Yes |
| <a id="D-AT10"></a>**D-AT10** | Build in the seven slices of [§9](#9-the-build-in-slices-that-each-land-alone), after the alert-workflow slices. | 2026-11-23 | [§9](#9-the-build-in-slices-that-each-land-alone) | Yes |
| <a id="D-AT11"></a>**D-AT11** | Conservative escalation: no model-tier retry for `triage`; escalate on an ambiguous cause or item match, on a choice between `fix-rule` and a silence, at the third reopen of an open tracking item without a silence, and on any failed triage attempt. | 2026-11-23 | [§4.1](#41-the-triage-seat-on-the-scout-tier-and-its-isolation), [§6.1](#61-handed-to-the-operating-session-escalate_to--operator-session) | Yes |
| <a id="D-AT12"></a>**D-AT12** | **Designer decision (owner may overturn).** Ship the triage brief template at `tools/tillr-run/backends/brief-template-triage.md` (pack-synced) with all ten `preflight.sh` headings, `- **Target:** {{TARGET}} (read-only root view)`, and the episode, timeline-bundle and correlation placeholders of [§4.1](#41-the-triage-seat-on-the-scout-tier-and-its-isolation). | 2026-11-26 | [§4.1](#41-the-triage-seat-on-the-scout-tier-and-its-isolation), [§9](#9-the-build-in-slices-that-each-land-alone) | Yes |
