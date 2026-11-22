---
title: "Agent Alerting and Process Watchdog (tillr-watch)"
author: "Agent"
date: 2026-11-20
status: in-review
stage: DESIGN
stage-since: 2026-11-20
tags: [tillr, watch, alerting, lint, workflows, charter, foreground]
summary: "A dedicated tillr-watch utility and shared core watch engine that observes emergent cross-utility behavior, raises deduplicated episode alerts with attached item timelines, supports one-command persistent foreground agent watches with zero-polling wakeups, and lints charters, workflows, and rules before execution."
next: "Add the ten first rules of Section 5.4 under config/tillr/watch with their loader test; build the second build of Section 6.7 (the lag_behind and duration_without_event patterns, the items, charter and git_refs projections, named scopes, and the rest of status, daemon, ack, silence, resolve and rule); then agent watches (watch add, list, edit, drop), wait --follow, subscribe and the operator procedure of Section 6.4; then tillr watch lint and its gate check, pack delivery, and the rest of the rule catalog"
related:
  - ./landing-principles.md
  - ./run-wall-ruling.md
  - ./failure-diagnosed-not-waited-out.md
  - ./tillr-judge-and-lifecycle.md
  - ./handoff-items-and-steering-timer.md
  - ./tillr-goal-monitor.md
  - ./tillr-live-drain-config.md
vantage:
  status-chip: true
---

# Agent Alerting and Process Watchdog (`tillr-watch`)

**Status:** 2026-11-21 — the owner ruled all five questions on 2026-11-21 ([the Decision Ledger](#9-decision-ledger)). The first build of [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) is built: it landed on 2026-11-21 as commit `e85ae973d9`, with the standalone binary, the rule loader, the evaluator for rules that read events only, `tillr watch eval --once`, `wait` and `seen`, and the skipped-event count of `status`. Foreground delivery through background watchers landed the same day as commit `d3f2c8cf96`. Nothing else is built. No rule is configured yet: an operator request of 2026-11-21 moves the first ten rules ahead of the second build, and [Section 5.4](#54-the-first-rules-shipped-ten-conditions-seen-on-2026-11-21-d-tw39) records them ([D-TW39](#D-TW39)). The other next action is the second build, whose contents [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) fixes: the two remaining patterns, the projections a rule can read besides events, named scopes, and the rest of `status`, `daemon`, `ack`, `silence`, `resolve` and `rule`. No design is owed before it. Agent watches, `subscribe`, lint, pack delivery and the rest of the rule catalog follow it. Thirty-nine decisions of the designer or the operator ([D-TW1](#D-TW1) through [D-TW39](#D-TW39)) await the owner's review; D-TW7 is set aside by the ruling on [OQ-TW4](#OQ-TW4).

---

## 1. The Owner's Direction and What Runs by Hand Today

On 2026-11-20, reflecting on the single-entity state machine design (`item-request-b1d51332cd`, `docs/design/tillr-state-machines.md`), the owner stated the problem across two linked observations:

> "we have like this emergent design that is born out of the confluence of these multiple utilities that we're putting together. And nowhere is there any carefully thought out design or enforcement of things moving through that. And I suppose this may be a combination of tiller core stuff as well as The Charter can talk about the larger emergent behavior would be nice to have some sort of enforcement layer here, it can even be an entirely new tiller utility that's there as just like a watchdog for various things that can be interesting. Ultimately we're never going to get these things perfect and agents are going to be developing workflows here so, even if we have a week of tiller get landing working well the next tiller use may be different and agent needs to be able to debug this thing. both Git landing as well as whatever the new thing is they're making. So let's think real hard about what utility we could build to help enforce Lint, whatever setup, configure, think about the overall emergent process that is the confluence of these things working together."
>
> "The sort of thing I'm imagining is even if it can't be fully mechanical, if you could decide to find some sort of criteria like your looping request criteria or an item going back and forth between two states four times or who knows what, like a typical engineering alerting system where you have thresholds and guidance and you will constantly adjust these things, but the alerts go to either an agent in the drain system or to the foreground agent whale type of thing. but they should be able to be acted upon, watched on, and then easily acted upon without wasting lots of runs and stuff. So basically this is like an alerting system for agents or something."

Later on 2026-11-20, the owner clarified that the utility must serve two distinct agent needs — standing rules with optional mechanical answers inside `tillr`, and ad-hoc conditions a foreground agent registers for its own session:

> "there could be two ways to go here. One, we detect and then we have a mechanical answer within the Tiller system, but the other thing here is we can actually just be alerts that are meant to be read by an agent and not have some fixed process or even any intention of what to do with these. Like, this is you, the foreground session. you're like waiting for a condition to happen and it's hard for you to notice that and especially if you there's 10 conditions you care about persisting what those conditions are across sessions as well as noticing them efficiently is hard"

### 1.1 What exists today by hand

Today, cross-utility observation is manual polling. Every 15 minutes the foreground operator session exports the SQLite event stream (`tillr view events`), pipes it through three private ad-hoc scripts, and reads item and run timelines by hand to spot stalls, loops, silent rebuild failures, and orphaned leases. Between those 15-minute sweeps — or overnight and across foreground session handoffs — nobody watches the confluence:

- On 2026-11-20, an item bounced `plan -> design -> plan` **four times over 80 minutes** because its `allowed_paths` fence was too narrow, discovered only when the owner asked about it.
- On 2026-11-20, the post-landing binary rebuild (`tillr-roll`) failed **eight times in a row over 90 minutes** with a failure record naming no test, leaving **29 landed commits** un-deployed in the live `tillr-*` binaries.
- On 2026-11-20, trunk (`refs/heads/main`) stayed unproved (`refs/tillr/green` lagging behind) for **25 minutes and 7 landings** after a landed change broke another module's test (following an earlier overnight stall documented in [the 2026-11-19 unproved-trunk postmortem](../tillr/unproved-trunk-2026-11-19.md)).
- On 2026-11-20, **four items accumulated 18 to 62 attempts each** while each individual pass looked locally legal to the scheduler.

### 1.2 Terms used in this document

| Term | Meaning |
| :--- | :--- |
| **Standing rule** | A declarative, tunable threshold rule (`config/tillr/watch/*.toml` plus live store overrides) that continuously evaluates store events and projections, carrying structured `check`/`do` guidance, a target `receiver`. |
| **Agent watch** | A lightweight, ad-hoc condition registered in one command by an agent (`tillr watch add --when ... --note ...`) carrying a note to its future self and **no** prescribed workflow or fixed action, persisted in `.tillr/state.db` across session restarts and compactions. |
| **Alert episode** | One lifecycle instance (`open -> acknowledged -> silenced -> resolved`) keyed by `(rule_id, subject_key)`. While the condition remains true across arbitrarily many drain passes, the episode updates in place rather than opening a second alert. |
| **Condition fingerprint** | A deterministic digest of the specific evidence causing an episode (for example, the failing step + error signature on `tillr-roll`, or the colliding path outside `allowed_paths`). `--until-change` silences wake again only if this fingerprint changes. |
| **Timeline bundle** | A bounded (`<= 8 KiB`), read-only digest of the subject's recent state moves, runs, obstacles, verify/gate refusals, landings, and git/roll pointers attached directly to every delivered alert or fired watch so the receiver acts without running `tillr view events` or spawning an exploratory worker run. |
| **Charter** | The process-wide definition document ([`tools/tillr/definition/definition.go`](../../tools/tillr/definition/definition.go), such as [`landing.json`](../../tools/tillr/definition/landing.json) for `git-landing` or [`recorded-verdict/definition.json`](../../tools/tillr/examples/recorded-verdict/definition.json) for hook-completed workflows) governing item states, prerequisites, and completion semantics. |

---

## 2. The Whole Process Today and Where Nothing Owns the Hand-Off

Across the `tillr` suite, ten distinct steps move work from queue intake to deployed, pushed code: **`plan`**, **`stage`**, **`run`**, **`judge`**, **`gate`**, **`land`**, **`verify`**, **`the drain loop`**, **`the roll`**, and **`the push`**. Each utility enforces local invariants on its own inputs and outputs, yet no single component watches whether the hand-offs *between* them are converging.

```mermaid
flowchart LR
    plan["1. plan\n(tillr-drain + plan seat)"] --> stage["2. stage\n(tillr-drain stage.go)"]
    stage --> run["3. run\n(tillr-run exec/lease)"]
    run --> verify["7. verify\n(verify seat + verify_return.go)"]
    verify -->|"plan-wrong:"| plan
    verify -->|"uncovered:"| stage
    verify -->|"done"| judge["4. judge\n(tillr-item + drain/judge.go)"]
    judge --> gate["5. gate\n(tillr-gate plan/run)"]
    gate -->|"refused"| stage
    gate -->|"gate.verdict pass"| land["6. land\n(tillr-land Squash)"]
    land --> loop["8. the drain loop\n(loop.go + HeavyRunner)"]
    loop -->|"afterLanding"| roll["9. the roll\n(tillr-roll.py + tillr-keep)"]
    loop -->|"refs/tillr/green"| push["10. the push\n(post_landing.go + pre-push)"]

    watch["tillr-watch\n(cursor evaluator + agent watches + lint)"] -.->|"reads events, store projections, git refs"| loop
    watch -.->|"timeline bundle + wake / triage"| recv["Foreground Whale / Drain Seat / Subscribers"]
```

### 2.1 Step-by-step ownership and unowned seams

Read at `HEAD` (`74106648b1f4`) on 2026-11-20:

| Step | Owning utility & code entrypoints | Records read & written | Where nothing owns the hand-off today |
| :--- | :--- | :--- | :--- |
| **1. `plan`** | **`tillr-drain`** ([`drain/plan.go`](../../tools/tillr-drain/drain/plan.go) `Plan()`, `planner.decide()`) for queue-level dispatch planning; **`plan` / `open` agent stages** in [`config/tillr/workflows/planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) and [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) for item decomposition (`graph.proposal`). | Reads `items`, `lane_leases`, `retries`, `item_faults`, `work_nodes`, `definition.Definition`, and `Policy`. Emits `drain.decision` (`record.Envelope`) with `run.Request` or `graph.proposal` (`drain.graph-changed`). | **Cross-seat bounce blindness:** When `plan` exits `OBSTACLE: definition` (`RESULT: blocked`), `drain/blocked.go` routes to `design`; when `design` finishes without widening `allowed_paths` (because the fence itself forbade editing the needed path or item metadata), `plan` runs again and hits the same wall. Each pass is locally valid; nothing notices the `plan <-> design` ping-pong. |
| **2. `stage`** | **`tillr-drain`** ([`drain/stage.go`](../../tools/tillr-drain/drain/stage.go) `LaneStager.Stage()`, `ensureWorktree()`, `ensureForkCheckout()`, `renderBriefWithEnding()`; [`drain/loop.go`](../../tools/tillr-drain/drain/loop.go) `stageDispatches()`). | Reads `items`, `allowed_paths`, `base_sha`, brief template. Writes `<LaneRoot>/<lane>/` (`worktree/`, `brief.md`, `fence.txt`, `base_sha`, `item_base_sha`, `item_fence.txt`, `session_id`, fork `target`). | **Fence & lane-rot seam:** `stage` stamps `fence.txt` faithfully from the item's `allowed_paths`, even when previous runs already proved that fence omits a required file. If per-item staging fails, `lane_rotation.go` (`drain.lane-refused`) rotates the lane out, and nobody correlates repeated staging or boundary refusals across runs. |
| **3. `run`** | **`tillr-run`** ([`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go) `Exec()`, [`lease.go`](../../tools/tillr-run/run/lease.go) `ReleaseLane()`); invoked by **`tillr-drain`** ([`drain/loop.go`](../../tools/tillr-drain/drain/loop.go) `runLane()`, `resumeAfterWall()`, and [`drain/failover.go`](../../tools/tillr-drain/drain/failover.go) `ClassifyAttempt()`). | Writes `runs` rows, `lane_leases` locks, `run.allocate`/`run.resume`/`run.progress` events, adapter transcripts, and commits in the lane worktree. | **Dead-process lease & instant-crash seam:** If `tillr-run exec` or its parent dies hard outside normal bracket close, `lane_leases` stays held by a non-live run until startup recovery (`end_unfinished_runs.go`) or manual `tillr run release`. Runs crashing in `< 5s` or failing over with backend failover suffixes (`drain.backend-outcome`) quietly burn capacity without alerting an operator. |
| **4. `judge`** | **`tillr-item`** ([`tools/tillr-item/judge.go`](../../tools/tillr-item/judge.go) `judgeFace()`, `judgeCommit()`) orchestrated by **`tillr-drain`** ([`drain/judge.go`](../../tools/tillr-drain/drain/judge.go) `judgeCandidate()`, `settleJudgeFailure()`, `recordLandedJudge()`). | Reads candidate commit, `JudgeOptions`, evaluator artifacts, and `gate.verdict`. Writes criterion verdicts (`done` / `not-done`), `drain.judge.request` / `drain.judge.landed` events, and `JudgeReceipt`. | **Pre-landing vs post-landing split:** `judgeCandidate()` evaluates criteria before `land`, while `recordLandedJudge()` commits final criterion state only after `land` succeeds. Repeated `not-done` returns whose reasons are empty or vague cycle back into build without surfacing degraded evaluator output. |
| **5. `gate`** | **`tillr-gate`** ([`tools/tillr-gate/change.go`](../../tools/tillr-gate/change.go), [`plan.go`](../../tools/tillr-gate/plan.go) `Plan()`, [`run.go`](../../tools/tillr-gate/run.go) `RunWith()`, [`gate.go`](../../tools/tillr-gate/gate.go) `RunGate()`). | Reads `base..head` diff, check manifests (`tools/gate/checks/`), and worktree blobs. Writes `gate.plan` and `gate.verdict` (bound to `(planID, treeSHA)`) plus check logs. | **Candidate-vs-trunk scope gap:** To keep landing latency low, `tillr-gate` runs change-scoped and fast checks on `(base_sha, head_tree)`, deferring cross-module heavy suites on the merged trunk tree to `HeavyRunner` *after* landing. And when `boundary.check` or `gate.verdict` refuses a candidate (`drain.refusal`), nothing aggregates recurring refusals across runs or items. |
| **6. `land`** | **`tillr-land`** ([`tools/tillr-land/land/landing.go`](../../tools/tillr-land/land/landing.go) `Land()`, [`land/trunk.go`](../../tools/tillr-land/land/trunk.go) `Trunk.Squash()`, `Trunk.LandLock()`, `Trunk.Move()`); called by **`tillr-drain`** (`landCandidate()`, `landWithActions()`). | Acquires `<git-common-dir>/land.lock`, verifies `gate.verdict`, squashes `base..head` onto `refs/heads/<trunk>`, writes `refs/tillr/landed/<runID>`, and inserts a `landings` row (`gated_tree`, `landed_tree`, `land.landing`). | **Multi-landing / unlanded-item drift:** Because design answers, doc updates, or reverted-and-retried commits can record `land.landing` rows referencing an item while the item's own build remains `in-queue`, `landings` can accumulate several commits for one item whose build still has not landed — invisible to `tillr-land`, which only checks one candidate at a time. |
| **7. `verify`** | **`tillr-drain`** ([`drain/verify_return.go`](../../tools/tillr-drain/drain/verify_return.go) `verifyBuildSession()`, `builderPlanWrong()`; [`drain/loop.go`](../../tools/tillr-drain/drain/loop.go) `Verify()`) running the **`verify` agent seat** in [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json). | Reads joined piece diffs, producer outputs, and item criteria; pins candidate head (`pinCandidateHead()`) and writes `done` (`node:<id>`) or `not-done` (`uncovered:...` or `plan-wrong:...`). | **Unbounded `plan -> work -> verify -> plan` cycles:** `uncovered:` returns to `work`, while `plan-wrong:` returns all the way to `plan` to re-cut pieces. Each return works as designed for a single mistake, but nothing notices when an item completes multiple full `plan -> work -> verify` cycles without converging. |
| **8. `the drain loop`** | **`tillr-drain`** ([`drain/loop.go`](../../tools/tillr-drain/drain/loop.go) `Loop.Run()`, `settle()`; [`drain/heavy.go`](../../tools/tillr-drain/drain/heavy.go) `HeavyRunner.Run()`, `judgeTip()`, `attribute()`, `revert()`). | Coordinates `drain.pass` ticks, dispatches lanes, settles endings, and runs `HeavyRunner` asynchronously to advance `refs/tillr/green` or bisect and `drain.revert` offending commits. | **Trunk-ahead-of-green drift & zero-yield burn:** `Loop.Run()` purposefully keeps landing new commits onto `main` while `HeavyRunner` proves earlier trunk tips asynchronously. If `HeavyRunner` stalls (on a cleanup marker, lock, killed test shard, or cross-module breakage), landings pile up ahead of `refs/tillr/green` with nothing alerting until a human checks. Likewise, lanes can burn >3 worker-hours with zero landings while every individual tick succeeds. |
| **9. `the roll`** | **`tillr-roll`** ([`tools/tillr-roll/tillr-roll.py`](../../tools/tillr-roll/tillr-roll.py), [`tools/tillr-roll/tillr-keep`](../../tools/tillr-roll/tillr-keep)); triggered asynchronously by **`tillr-drain`** ([`drain/post_landing.go`](../../tools/tillr-drain/drain/post_landing.go) `submitPiRoll()`, [`drain/restart_rolling.go`](../../tools/tillr-drain/drain/restart_rolling.go) `rollingOwnership()`). | Reads `drain.build-started`, `refs/tillr/green`, and `landings`; builds the eight Go binaries (`MODULES`), updates `tillr-roll.commit`, handshakes `heavy.lock` for clean `tillr-keep` restart, or inserts `drain.roll-failed` (`record_failure()`). | **Fire-and-forget stale-binary gap:** `afterLandingCandidate()` submits `tillr-roll.py` asynchronously so landings never block on compilation. When `tillr-roll.py` fails, `record_failure()` appends `drain.roll-failed` (often with collapsed stderr naming no specific test) and exits; `tillr-drain` keeps running its **old** in-memory binary across 29 subsequent commits without pausing or raising an alarm. |
| **10. `the push`** | **`tillr-drain`** ([`drain/post_landing.go`](../../tools/tillr-drain/drain/post_landing.go) `pushGreen()`, `pushTrunk()`, `recordPush()`; [`drain/push_refused.go`](../../tools/tillr-drain/drain/push_refused.go) `healRefusedPush()`) plus **[`hooks/pre-push`](../../hooks/pre-push)** (`bin/patch-branches-publish`). | Reads `refs/tillr/green` (or `TrunkRef`), `yolo-packs/*/pack.json` `patch_branch` locators, and remote refs; runs non-forcing `git push` and logs `drain.post-landing-action`. | **Downstream starvation & unhealed refusals:** Because `pushGreen()` only publishes commits proved by `refs/tillr/green`, any stall in Step 8 silently freezes remote publication too; and if `hooks/pre-push` or a diverged remote refuses the push beyond what `healRefusedPush()` can repair, local commits diverge silently from the remote. |

### 2.2 Why every failure above escaped local error handling

Every utility in the table above behaved **within its local specification** during the 2026-11-20 incidents:

1. **Asynchronous decoupling hides lag.** Decoupling `HeavyRunner` (Step 8), `tillr-roll` (Step 9), and `pushGreen` (Step 10) from synchronous `land` (Step 6) is what lets `tillr-land` finish in milliseconds instead of minutes. The price of asynchronous decoupling is that **lag and repeated background failure are invisible to the synchronous caller**.
2. **Valid local retries compose into invalid global loops.** Returning `OBSTACLE: definition` from `plan` to `design`, or `plan-wrong:` from `verify` to `plan`, is the exact recovery move `planned-build@v1` was built to make. The defect only appears in the **trajectory** (`plan -> design -> plan -> design` four times on an unchanging `allowed_paths` fence).
3. **Silent degradation in metadata starves diagnosis.** When `tillr-roll.py` writes `drain.roll-failed` whose `detail` names no failing test, or a refusal/hold record carries an empty `reason`, downstream tools and operators lose the literal cause required by [failure diagnosed, not waited out](./failure-diagnosed-not-waited-out.md#31-the-four-tests) (`T1`).

---

## 3. Relation to State Machines, Existing Monitors, and the No-Cap Ruling

### 3.1 Prevention vs. detection: relation to `docs/design/tillr-state-machines.md`

`docs/design/tillr-state-machines.md` (`item-request-b1d51332cd`) and `tillr-watch` form complementary halves of process governance — **synchronous single-move prevention** versus **asynchronous multi-move detection**:

| Dimension | State machines (`docs/design/tillr-state-machines.md`) | Process watchdog & alerting (`tillr-watch`) |
| :--- | :--- | :--- |
| **Question answered** | *"Is this single state transition legal right now?"* | *"Is this sequence of legal transitions, elapsed silence, or cross-utility lag healthy?"* |
| **Where it runs** | Synchronously inside the mutating transaction (`store`, `item`, `run`, `land`, `drain`). | Asynchronously over the committed `events` cursor, store read projections, and git refs (plus statically at `lint` time). |
| **What happens when triggered** | **Refuses the write** before commit; the illegal state cannot exist in `.tillr/state.db`. | **Opens or updates one alert episode** (or fires an agent watch) with a timeline bundle, guidance; **never** rolls back or fails an item. |
| **2026-11-20 example** | Forbidding an item transition `in-queue -> landed` without a matching `land.landing` receipt or criterion verdict; forbidding two live runs from holding the same `lane_leases` row. | Catching `plan -> design -> plan` 4 times over 80m; catching `refs/tillr/green` lagging 7 landings / 25m behind `main`; catching 8 consecutive `drain.roll-failed` events leaving 29 commits un-rolled. |

**Boundary rule (`D-TW2`):** Neither layer may impersonate the other. If a condition is universally illegal at a single transition boundary (for example, writing a refusal record with an empty `reason` once the writer schema forbids it), it should become a synchronous refusal in `docs/design/tillr-state-machines.md`, keeping the corresponding `tillr-watch` rule (`empty-reason-record`) only as a defense-in-depth canary for raw external writers like Python scripts. Conversely, state machines must **never** grow numeric transition counters ("refuse `plan -> design` on the 4th transition"), because turning a count into a hard state-machine refusal violates the owner's standing no-cap ruling below.

### 3.2 Standing ruling: an alert never fails, benches, or caps work (`D-TW2`)

Four settled repository rulings forbid failing or stopping work on attempt counts, retry budgets, or wall timers:

1. **[Run-wall ruling (`D-RW1`–`D-RW4`)](./run-wall-ruling.md#1-should-the-wall-exist-at-all):** Removed default 1800-second run walls; a wall-clock duration is a resource signal, never a fault verdict, and committed candidates are always judged before wall status.
2. **[Failure diagnosed, not waited out](./failure-diagnosed-not-waited-out.md#34-admission-restated):** Eliminated the 30-minute cooldown, numeric retry budget, and `retry-exhausted` bench. Re-admission is governed by literal cause and input change ([Section 3.1](./failure-diagnosed-not-waited-out.md#31-the-four-tests)), never by counting failures.
3. **[Landing principles §5](./landing-principles.md#5-only-verdicts-charge) and [§6](./landing-principles.md#6-a-budget-escalates-it-does-not-loop):** Only verdicts charge, and repetition escalates to diagnosis rather than failing the item.
4. **[Judge and lifecycle (`D-JL9` and `D-JL10`)](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10):** Worker endings and design runs are **never counted** to fail or bench an item; identical endings escalate to `design`, and only objections naming the owner hold for the owner.

`tillr-watch` obeys this ruling as an inviolable architectural constraint (**[D-TW2](#D-TW2)**):

- **Alerts notify; they never fail, close, cancel, or hold an item.** `tillr-watch` holds no capability to transition an item into `blocked`, `closed`, or any terminal failure state when a count (`>= 4` oscillations, `>= 8` attempts, `> 1` plan-verify cycle) or duration (`> 3` worker-hours, `> 25m` green lag) crosses a threshold.
- **How this handles the 18-to-62-attempt items from 2026-11-20:** Under a naive retry cap, an item at attempt 8 would have been benched as `retry-exhausted`, stranding legitimate complex items alongside stuck ones. Under `tillr-watch`, crossing `>= 8` attempts (`high-attempt-diagnostic`) leaves the item's execution untouched while delivering one deduplicated alert + compact timeline to the foreground whale (or one read-only diagnostic scout), making the trajectory visible in minutes instead of letting four items silently reach 18 to 62 attempts unnoticed.
- **Enforced statically in `tillr watch lint`:** Any rule file that declares a remedy of any kind is rejected ([OQ-TW2](#OQ-TW2)) at `lint` and load time (`watch-lint-forbidden-remedy`).

### 3.3 Relation to One-Breakage Dedup, Steering Timers, and Goal Monitors

- **Generalizes [the retired agent runtime design Section 20 ("One breakage, one investigation")](./agent-runtime-retirement.md#what-happened-to-each-section):** `D-OC1`–`D-OC8` established mechanical signature deduplication, transition-driven invocation, and the `D-OC5` precondition (*never dispatch a second investigation while an open breakage already has a non-terminal attempt*). `tillr-watch` lifts that exact episode discipline from gate check breakages to **all** cross-utility rules (`D-TW5`, `D-TW7`).
- **Replaces time-polling in [`steering-timer`](../reference/steering-timer.md) when waiting on state:** `steering-timer.ts` ([`handoff-items-and-steering-timer.md` Section 4.2](./handoff-items-and-steering-timer.md#42-the-mechanism-a-pi-extension-owning-a-durable-steering-record)) injects periodic clock-based check-ins (`every N minutes`) so a foreground whale keeps nudging a long goal. When an agent is instead waiting on *specific store/git conditions* ("tell me when `refs/tillr/green` catches up, or when `item-X` reaches `verify`, or if `tillr-roll` fails"), waking every 10 minutes on a timer wastes turns when nothing happened and lags up to 10 minutes when it did. `tillr-watch` provides **event-driven, zero-polling wakeups** from a durable record in the store, delivered by a background watcher the foreground agent runs ([Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3), [D-TW11](#D-TW11)); it adds no Pi extension.
- **Complements [`tillr-goal-monitor.md`](./tillr-goal-monitor.md):** The iceboxed goal monitor evaluates high-level prose roadmap goals on a scheduled `due_at` cadence via an LLM node. `tillr-watch` is a **0-LLM-token mechanical evaluator** running continuously on event commits, invoking an agent *only* when a declared threshold or agent watch actually fires.

---

## 4. Utility Boundary and the Three-Layer Split: Core, Workflow, Charter

### 4.1 New utility (`tillr-watch`) vs. face of an existing utility ([D-TW1](#D-TW1), [OQ-TW1](#OQ-TW1))

The suite today splits responsibilities along sharp UNIX boundaries dispatched by [`tools/tillr-cli/main.go`](../../tools/tillr-cli/main.go): `store`, `item`, `run`, `gate`, `land`, `drain`, and `view` (plus read-only `tillr-trace` and supervisor scripts `tillr-roll` / `tillr-keep`).

Putting alerting and cross-system linting inside an existing binary has two structural flaws:

1. **A watchdog inside `tillr-drain` dies or stalls with the process it watches.** On 2026-11-19/2026-11-20, `tillr-drain` suffered 1-tick post-midnight crashes ([`unproved-trunk-2026-11-19.md`](../tillr/unproved-trunk-2026-11-19.md#verified-timeline)), ran 29 commits behind trunk while `tillr-roll` failed, and can be paused or stopped while foreground agents are still waiting on `item` or `store` events. A watchdog must stay alive and trustworthy when `tillr-drain` is wedged, restarting, or running stale code.
2. **`tillr-view` carries the Observatory HTTP/WebSocket server and embedded web bundle, while static linting belongs in CI/gate and headless agent shells.** Tying `watch` and `lint` to `tillr-view` couples headless rule evaluation and pre-flight linting to the UI server, whereas splitting `watch` into `tillr-view` and `lint` into `tillr-drain` scatters one rule schema across two binaries.

**Design choice ([D-TW1](#D-TW1), subject to owner confirmation in [OQ-TW1](#OQ-TW1)):** Build **`tools/tillr-watch`** (`tillr-watch`, registered as `"watch"` in `tools/tillr-cli/main.go` `nouns` and `tools/tillr-roll/tillr-roll.py` `MODULES` so `tillr watch <face>` works identically to `tillr gate` and `tillr view`), backed by a pure shared library package **`tools/tillr/watch`** that registers the watch SQLite DDL (`store.RegisterDDL`) and event kinds (`event.Register`) at `init()` time.

`tillr watch` exposes four cohesive command groups:

```text
# 1. Continuous or one-shot rule evaluation & status
tillr watch status [--json]                     # rules, unresolved alert episodes, watchers + (later) armed agent watches
tillr watch eval [--once]                       # advance cursor once, update episodes, exit 0
tillr watch daemon                              # zero-polling follower supervised beside tillr-keep

# 2. Alert episode actions & live rule tuning (Mode A)
tillr watch ack <alert-id> --by <actor> [--note <text>]
tillr watch silence <alert-id> (--until <dur|ts> | --until-change) [--reason <text>]
tillr watch resolve <alert-id> --reason <text>
tillr watch rule list | show <id> | add <file> --by <actor> | tune <id> --set <key>=<value>... --by <actor> | reset <id> --by <actor> | export

# 3. Persistent foreground agent watches (Mode B — one command, no prescribed action)
tillr watch add --when <expr> --note <text> [--owner <id>] [--once | --recurring]
tillr watch list [--owner <id>] [--all]
tillr watch edit <watch-id> [--when <expr>] [--note <text>]
tillr watch drop <watch-id>
tillr watch wait [--owner <id>] [--timeout <dur>] [--seen-through <seq>] [--follow]  # blocks at 0% CPU until a notice is pending
tillr watch seen --through <seq> [--owner <id>]    # confirm notices were read (Section 6.4)

# 4. External pub/sub & pre-flight configuration verification
tillr watch subscribe [--since-seq <n>] [--consumer <id>] [--kind alert|watch|all] [--receiver <r>]
tillr watch lint [<repo-or-dir>] [--charter <path>] [--workflows <dir>] [--rules <dir>]
```

### 4.2 What belongs in `tillr` core, in a workflow definition, and in the charter ([D-TW3](#D-TW3))

To support both `git-landing` today and arbitrary agent-authored workflows tomorrow (`recorded-verdict`, `AutoQA`, benchmark harnesses, synthesis pipelines), responsibilities divide cleanly across three layers:

| Layer | Where it lives | What belongs here (and nowhere else) | What must NOT leak into this layer |
| :--- | :--- | :--- | :--- |
| **1. `tillr` Core** | `tools/tillr/watch/`, `tools/tillr/store/`, `tools/tillr/event/`, `config/tillr/watch/core.toml` | - Incremental `events.seq` cursor engine & zero-polling `inotify` wakeup (`watch.wakeup` / WAL).<br>- `alert_episodes`, `agent_watches`, `foreground_delivery`, `watch_rule_overrides`, and `watch_consumers` SQLite tables.<br>- Generic predicate primitives (`count_in_window`, `consecutive_count`, `alternating_pair`, `lag_behind`, `duration_without_event`, `predicate_match`).<br>- `TimelineBundle` builder & NDJSON `subscribe` stream.<br>- `tillr watch lint` cross-artifact engine.<br>- **Core infrastructure rules** true of *every* `tillr` store: `< 5s` run crashes, failover suffixes, dead-PID lane leases, `SQLITE_BUSY` lock refusals, empty-reason records, operator holds, high-attempt diagnostics. | No hardcoded stage names (`"plan"`, `"verify"`), no Git ref names (`refs/tillr/green`), and no assumptions about `tillr-roll` or `git push`. |
| **2. Workflow Definition** | `config/tillr/workflows/*.json` (plus optional `watch` block per workflow) | - Stage graph topology (`stages`, `edges`, `exit`, `may_replace`, `outputs`/`inputs`, `seat`, `access`, `write_scope`).<br>- Optional declarative **`watch` metadata** on the workflow JSON: expected return pairs (`oscillation_pairs: [["plan", "design"], ["work", "verify"]]`), expected cycle bounds (`max_plan_verify_cycles: 1`), and stage-specific `guidance` hints telling receivers which stage output or fence caused a bounce. | No global trunk/git assumptions and no item state-machine definitions (`in-queue`, `landed`, `confirmed`). |
| **3. Charter Document** | `tools/tillr/definition/*.json` (e.g. `landing.json`, `recorded-verdict/definition.json`) + charter rule pack (`config/tillr/watch/charters/<name>.toml`) | - Whole-process item lifecycle (`states`, `ready`, `blocked`, `closed`, `finished`, `transitions`, `prerequisites`, `completes`: `"landing"` vs `"hook"`, `hooks`).<br>- **Charter observability bindings** (`observability` block on `Definition`): what event marks real forward throughput (`terminal_progress_kind`: `"land.landing"` vs `"item.verdict"`), plus charter-owned health invariants (`git-landing`: `refs/tillr/green` lag, `tillr-roll` rebuild parity, `git push` liveness, multiple landings on unlanded items; `recorded-verdict`: `complete-item` hook error rate). | No per-workflow stage topology (`planned-build` vs `scout-then-build` vs `doc-triage` can all run under the same charter). |

Concretely, a workflow JSON may carry an optional, backward-compatible `watch` object (validated by both `tillr drain workflow check` and `tillr watch lint`):

```json
{
  "name": "planned-build",
  "version": 1,
  "stages": ["..."],
  "edges": ["..."],
  "exit": { "requires_nodes": ["plan", "work", "verify"], "requires_candidate": "commit_required", "evaluator_slot": "verify" },
  "watch": {
    "cycle_anchor": { "from": "verify", "to": "plan", "max_cycles": 1 },
    "oscillation_pairs": [
      { "a": "plan", "b": "design", "threshold": 4, "hint": "Check whether item allowed_paths omits a path plan/work needs, or whether an open design question lacks a ruling." }
    ]
  }
}
```

And a charter `definition.json` declares its process-level progress signal in two optional lines:

```json
{
  "definition": "git-landing",
  "completes": "landing",
  "observability": {
    "progress_event": "land.landing",
    "rule_pack": "charters/git-landing.toml"
  }
}
```

---

## 5. Alert Rules as Tunable Data and the Starting Rule Catalog

### 5.1 Declarative rule schema ([D-TW4](#D-TW4), [OQ-TW5](#OQ-TW5))

An operator or foreground agent can add, threshold-tune, re-route, or silence any standing rule **without touching Go code or recompiling `tillr-watch`**. Rules are plain TOML documents loaded from three merged scopes in precedence order:

1. **Charter & workflow defaults:** `config/tillr/watch/core.toml`, `config/tillr/watch/charters/<definition>.toml`, and workflow `watch` blocks.
2. **Repository rule files:** `config/tillr/watch/rules.d/*.toml`.
3. **Live store overrides ([OQ-TW5](#OQ-TW5)):** `watch_rule_overrides` in `.tillr/state.db`, written via `tillr watch rule tune` so an agent adjusting a noisy threshold mid-incident does not dirty tracked files in the main checkout (which would cause `tillr-roll.py` to refuse post-landing rebuilds, as occurred on 2026-11-19). `tillr watch rule export` materializes live overrides back into TOML when ready to commit.

Every standing rule has five explicit data sections — **identity/scope**, **`reads`**, **`when` (subject + threshold)**, **`guidance` (`check` + `do`)**, and **delivery (`receiver`)**:

```toml
[[rule]]
id = "step-oscillation"
enabled = true
scope = "core"                                  # "core" | "charter:<name>" | "workflow:<name>"
severity = "warning"                            # "info" | "warning" | "high"

[rule.reads]
events = ["drain.node-advanced", "drain.refusal", "boundary.check"]   # registered event kinds only; drain.decision is a stored record, read as a projection
projections = ["items", "charter"]            # the subject's items row and the store's charter (D-TW30)

[rule.when]
group_by = "item_id"                            # one episode per item
pattern = "alternating_pair"                    # A -> B -> A -> B ...
field = "seat"
pairs = [["plan", "design"], ["work", "verify"]] # extended automatically by workflow.watch.oscillation_pairs
threshold = 4                                   # 4 consecutive observations alternating between the pair (plan, design, plan, design): fires on the 4th
window = "4h"
clear_when = "item.state in charter.finished"   # judged with the item's state at the evaluation (D-TW30)

[rule.guidance]
summary = "Item {{subject}} bounced between {{pair_a}} and {{pair_b}} {{count}} times over {{elapsed}}."
check = [
  "Inspect the attached TimelineBundle obstacles and boundary.check / OBSTACLE: fence or OBSTACLE: definition lines.",
  "Compare the paths named in the planner/builder obstacle against the item's allowed_paths fence.",
  "Check whether the designer updated a design doc without widening the item's fence (which design runs cannot edit unless fenced)."
]
do = [
  "If allowed_paths is too narrow, widen the item fence (`tillr item update {{subject}} --add-path <path>`) or split a prerequisite item.",
  "If a design decision needs the owner, hold with the exact owner question rather than re-dispatching plan.",
  "Acknowledge this alert (`tillr watch ack {{alert_id}} --by <you>`) while acting."
]

[rule.delivery]
receiver = "foreground"                         # "foreground" | "subscribers"
also_notify = ["subscribers"]
```

A second example shows a `charter:git-landing` rule that reads Git refs, with the keys [D-TW28](#D-TW28) fixes for `lag_behind`:

```toml
[[rule]]
id = "unproved-trunk-lag"
enabled = true
scope = "charter:git-landing"
severity = "high"

[rule.reads]
events = ["land.landing", "drain.revert", "drain.fixer-proved"]   # kinds are checked; a lag_behind rule reads no event (D-TW28)
projections = ["git_refs:refs/heads/main,refs/tillr/green"]

[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_commits = 3                           # >= 3 unproved landings ahead of green
threshold_duration = "15m"                      # OR >= 15m with >= 1 unproved commit
mode = "any"                                    # fires when either threshold crosses

[rule.guidance]
summary = "{{follower}} lags {{leader}} by {{count}} landings ({{elapsed}} elapsed; green={{follower_short}}, main={{leader_short}})."
check = [
  "Check HeavyRunner status in the attached bundle: is .git/tillr-gate/heavy.cleanup-pending present, is heavy.lock held by a dead PID, or did a proof shard die on SIGTERM?",
  "Inspect the newest failing proof log or active fixer-main-* item in the bundle."
]
do = [
  "If heavy.cleanup-pending or a stale lock is blocking HeavyRunner with no live holder, clear the stale marker.",
  "If a cross-module test is red without an active fixer-main item, verify bisection/revert progress or dispatch targeted repair."
]

[rule.delivery]
receiver = "foreground"
```

**What `alternating_pair` counts ([D-TW12](#D-TW12)):** `threshold` counts observations, not moves between them. This is the designer's decision of 2026-11-21, following an operator request; the owner has not ruled on it and may overturn it.

- An **observation** is one event named in `reads.events`, for one `group_by` subject, that carries the rule's `field`. Consecutive observations with the same `field` value count as one, dated at the first of them, because two event types can report the same step. Only observations inside `window` are compared, as [D-TW23](#D-TW23) below states.
- The **count** is the number of most recent observations whose values alternate strictly between the two members of one entry in `pairs`. An observation whose value is outside that pair ends the alternation. [D-TW18](#D-TW18) below states the count that observation leaves when `pairs` has more than one entry.
- The rule **fires** on the observation that brings the count to `threshold`, provided the oldest counted observation is no older than `window`. Observations older than `window` are not counted.
- With `threshold = 4`, the sequence `plan, design, plan, design` fires on the fourth observation. That is three moves; the rule does not wait for a fifth observation.
- `{{count}}` in `guidance.summary` is that count of observations.

**The window is applied before anything else ([D-TW23](#D-TW23)).** This is the operator's design answer of 2026-11-21, given to a build worker's question; the owner has not ruled on it and may overturn it. The worker's case was a rule with `window = "60m"` and `threshold = 2` whose subject has `plan` 61 minutes before the evaluation, `plan` 59 minutes before it and `design` 1 minute before it. Read one way, the older `plan` absorbs the newer one as a repeat, the single observation is dated outside `window`, and the count is 1. Read the other way, the count is 2.

- The evaluator first takes the read events whose `ts` is within `window` of the time of evaluation, and **nothing outside `window` is ever consulted**, for any pattern and for any purpose. That is the rule [D-TW13](#D-TW13) already states for counts, and it governs here.
- Repeats are therefore collapsed only among the events inside `window`. A run of observations with the same value begins anew at the edge of `window`: the first of them inside `window` is the observation, and it is dated at that event, whatever came before the edge.
- In the worker's case the event at 61 minutes is not read. The observations are `plan` (dated 59 minutes before) and `design`, the count is 2, and the rule fires.
- The same holds for what ends a run ([D-TW18](#D-TW18)), for a resetting event and for `clear_when`: an event older than `window` neither counts, nor collapses a repeat, nor resets, nor clears.

**Which entry of `pairs` is counted, and what ends its run ([D-TW18](#D-TW18)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. The worker's case was a rule with `pairs = [["a", "b"], ["c", "d"]]` and `threshold = 4` that opens an episode on `a, b, a, b` and then reads `c, d, c, d` before the next evaluation. [D-TW12](#D-TW12) counts the second run to 4, while [D-TW17](#D-TW17) read each of the four new observations as "outside the pair", which left no counted observation after the newest clearing event. The count is always taken from the newest observations, whichever entry they belong to, and no entry is remembered from an earlier run or from an episode. Three terms are used:

- The **run** is the subject's most recent observations, taken back from the newest for as long as they alternate between the two members of one entry in `pairs`. The count is the number of observations in the run.
- The **matched entry** is the entry the run alternates between. When the run holds two or more observations, it is the first entry, in the order the rule file writes them, whose two members are the values of the two newest observations. When the run holds one observation, it is the first entry that contains that value.
- An observation **ends a run** when the count after it is not one more than the count before it. That observation is the pattern's clearing event for [D-TW17](#D-TW17), and its `seq` is that of the first event of the observation.

The count after each observation follows from those terms:

| The newest observation's value | The observation before it | Count |
| :--- | :--- | :--- |
| In no entry of `pairs` | Anything | 0 |
| In an entry | None inside `window`, or a value that shares no entry with the newest | 1 |
| In an entry | A value that shares an entry with the newest | The length of the run: 2, or more when older observations go on alternating between the same two values |

- An observation that ends one run can be the first observation of the next. In the worker's case `c` ends the `a, b` run and has count 1, the following `d, c, d` bring the count to 4, and the rule fires on the fourth observation of the second run, as [D-TW12](#D-TW12) requires of any run.
- Only `c` is a clearing event there. `d, c, d` each raise the count by one, so they end nothing. The evaluation that reads all four finds a clearing event after the episode's `opened_seq` and a rule that fires, which is the last row of the table in [D-TW17](#D-TW17): it resolves the `a, b` episode with `resolved_seq` at `c` and opens a new episode whose `{{pair_a}}` and `{{pair_b}}` are `c` and `d`.
- When two entries share a value, the run can begin one observation before the clearing event. With `pairs = [["a", "b"], ["b", "c"]]`, the sequence `a, b, a, b, c` has count 2 after `c`, the run being `b, c`. The `c` did not raise the count from 4 to 5, so it ends the `a, b` run and is a clearing event, and the `b` before it is counted in both runs.
- A subject has one count and at most one unresolved episode per rule ([D-TW5](#D-TW5)), however many entries `pairs` has. Two alternatives were set aside by the designer: keeping a separate count and episode for each entry, which would need the entry in the episode's key and would let one item hold several alerts from one rule; and measuring a clearing event against the entry of the open episode, which is the reading that left the worker's second run with nothing to count.

**What the other three event patterns count ([D-TW13](#D-TW13)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. It fixes how a rule declares which events count and what sets a count back to zero. Three terms are used:

- A **read event** is an event whose kind is listed in the rule's `reads.events`.
- A rule's **subject** for a read event is the value of the name in `group_by`, or the one subject `global` when `group_by = "global"`. Only a JSON string is a subject. A read event whose value for that name is missing or is not a string belongs to no subject, is ignored by the rule and is counted as skipped ([D-TW22](#D-TW22), below).
- A **name** (in `group_by`, in `field`, and after `event.` in an expression) is one of the `events` table's columns `seq`, `ts`, `kind`, `seat`, `object_type`, `object_id` and `revision`, or otherwise a key of the event's JSON payload, with dots descending into nested objects (`event.detail.step`). A name that is absent or null has no value.

Two keys in `[rule.when]` select events, and both are expressions in the grammar below:

- `match` says which read events **count**. It is optional for `count_in_window` and `predicate_match`, where leaving it out makes every read event count. It is required for `consecutive_count`.
- `reset` is used only by `consecutive_count`. It says which read events set the count to zero.

| Pattern | Keys in `[rule.when]` | What is counted | What sets the count to zero | Fires when |
| :--- | :--- | :--- | :--- | :--- |
| `predicate_match` | `group_by`, optional `match`, `window` (required, longer than zero) | The same as `count_in_window`, with the threshold fixed at 1 | Nothing | The subject has a counting event inside `window`, so the episode stays unresolved for `window` after the newest one |
| `count_in_window` | `group_by`, optional `match`, `threshold` (integer, 1 or more), `window` (required, longer than zero) | The subject's counting events whose `ts` is within `window` of the time of evaluation | Nothing. An event stops counting only by becoming older than `window` | The count is `threshold` or more |
| `consecutive_count` | `group_by`, `match` (required), optional `reset`, `threshold` (integer, 1 or more), `window` (required, longer than zero) | The subject's counting events since the most recent resetting event, counting only those whose `ts` is within `window` of the time of evaluation | With `reset` given: a read event for the subject on which `reset` holds; a read event on which neither `match` nor `reset` holds changes nothing. With `reset` left out: any read event for the subject on which `match` does not hold | The count is `threshold` or more |
| `alternating_pair` | `group_by`, `field`, `pairs`, `threshold`, `window`, as [D-TW12](#D-TW12) states | As [D-TW12](#D-TW12) and [D-TW18](#D-TW18) state | As [D-TW18](#D-TW18) states | As [D-TW12](#D-TW12) states |

An event on which both `match` and `reset` hold resets the count and is not counted. For every pattern in the table, `{{count}}` in `guidance.summary` is the count the table defines.

Two worked examples, with illustrative payload names. A `consecutive_count` rule with `match = 'event.kind == "gate.verdict" and event.pass == false'`, `reset = 'event.kind == "gate.verdict" and event.pass == true'` and `threshold = 2` reads the sequence fail, fail and fires on the second event; it reads fail, pass, fail and does not fire, because the pass set the count to zero. The same rule with `reset` left out and `reads.events = ["gate.verdict", "drain.refusal"]` reads fail, `drain.refusal`, fail and does not fire, because the `drain.refusal` event is a read event on which `match` does not hold.

**Which values `group_by` groups on, and the count of skipped events ([D-TW22](#D-TW22)).** This is the operator's design answer of 2026-11-21, given to a build worker's question; the owner has not ruled on it and may overturn it. The definition of a subject above spoke of "the value of the name in `group_by`" and did not say what the subject is when that value is a number, a Boolean, a list or an object.

- A rule groups on **JSON string values only**. A read event whose `group_by` value is a string belongs to the subject named by that string, taken as written: no trimming and no change of case. The empty string is a string, and names a subject.
- A read event whose `group_by` value is missing, is null, or is a Boolean, a number, a list or an object is a **skipped event** for that rule. The rule does not count it: it is not an observation, `match`, `reset` and `clear_when` are not judged against it, and it neither resets nor clears any subject's count. No value of another type is encoded into a string.
- The store's own columns follow the same test on the value the column holds. `seq` and any other column holding a number is never a string, so a rule that groups by one skips every event it reads. Such a rule loads; saying so before it runs is `tillr watch lint`'s work ([Section 7.2](#72-tillr-watch-lint-checking-a-workflows-whole-configuration-before-it-runs-d-tw10)).
- With `group_by = "global"` no value is read, so no event is skipped.
- The evaluator counts skipped events **per rule**. The count is derived like every other count: it is the number of the rule's read events inside `window` at that evaluation that are skipped events ([D-TW23](#D-TW23)). It is not stored, and it falls as those events grow older than `window`.
- `tillr watch status` shows the count for each evaluated rule, and `tillr watch eval --once` names each rule whose count is above zero on standard error. [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) gives both outputs.

One example. A `count_in_window` rule with `group_by = "item_id"` and `threshold = 2` reads three events inside its `window`, whose `item_id` values are `"item-a"`, `7` and `"item-a"`, and a fourth that has no `item_id`. The subject `item-a` has count 2 and the rule fires for it. No subject `7` exists, and the rule's skipped count is 2.

The designer set aside one alternative: refusing at load a `group_by` name whose values are not strings. The loader cannot know the type, because the name is usually a key of an event's payload and the payload's types are not registered.

**How these rules are evaluated, and when an episode ends ([D-TW13](#D-TW13)).** The evaluator keeps no count between evaluations. At each evaluation it reads, for each rule, the read events whose `ts` is within the rule's `window` of the time of evaluation, in `seq` order, and derives each subject's count from them. A restarted evaluator therefore reaches the same counts as one that never stopped. The time of evaluation is the clock's time when the evaluation starts, and the clock is an input the tests set. In an episode row, `opened_seq` is the `seq` of the newest counted event at the evaluation that opened it, `last_observed_seq` is the `seq` of the newest counted event seen so far, and `observation_count` goes up by one at each evaluation that moves `last_observed_seq`. An evaluation that finds no new counted event and no episode to open or resolve writes nothing to the store.

- A subject whose rule fires and that has no episode other than resolved ones gets a new `open` episode and one `watch.alert.opened` event. A subject that already has one is updated in place ([Section 6.1](#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5)).
- A subject with an unresolved episode whose rule no longer fires at an evaluation (the count fell below `threshold` through a reset, through a value outside the pair, or through events growing older than `window`) is resolved with `resolved_reason = "condition-cleared"` and one `watch.alert.resolved` event.
- `clear_when`, when given, is an expression judged against each read event for the subject. An event on which it holds sets the pattern's count to zero, exactly as a resetting event does, so the episode resolves at that evaluation. It does so for every pattern, including `predicate_match` and `count_in_window`, whose rows in the table above name nothing else that sets the count to zero. `clear_when` is optional.

**When a condition clears and returns between two evaluations ([D-TW17](#D-TW17)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. The worker's case was an open `predicate_match` episode whose subject receives an event on which `clear_when` holds and then a new counting event, both before the next evaluation: the count is 1 again and the rule still fires, so the second bullet above (resolve when the rule no longer fires) and the third (a `clear_when` event resolves the episode) disagree. The third governs, and the returned condition is a new episode with its own notice. One term is used:

- A **clearing event** is a read event for the subject that sets the pattern's count to zero: an event on which `clear_when` holds, a resetting event of a `consecutive_count` rule, or, for an `alternating_pair` rule, an observation that ends a run ([D-TW18](#D-TW18)). For that pattern the count after a clearing event is 0, 1 or 2, not always zero, because the observation that ends one run can begin the next.

At each evaluation, for a subject that has an unresolved episode, the evaluator looks among the events it read for a clearing event whose `seq` is greater than the episode's `opened_seq`:

| A clearing event after `opened_seq` was read | The rule fires at this evaluation | What the evaluator does |
| :--- | :--- | :--- |
| No | Yes | Updates the episode in place. No event is appended and no notice is given |
| No | No | Resolves the episode. Its counted events grew older than `window` |
| Yes | No | Resolves the episode |
| Yes | Yes | Resolves the episode, then opens a new one for the same subject, in the same evaluation |

- In the last row the evaluator appends `watch.alert.resolved` for the old episode and then `watch.alert.opened` for the new one, in that order and in one store transaction. The new episode has a new `alert_id` and an `episode_num` one greater, and its `opened_seq`, `last_observed_seq` and `observation_count` are set as for any newly opened episode, from the counted events after the newest clearing event. For an `alternating_pair` rule they are set from the run as it stands at that evaluation, which can include the newest clearing event itself and the observation before it ([D-TW18](#D-TW18)). The old episode's `last_observed_seq` and `observation_count` are not changed by that evaluation. `tillr watch wait` prints the new episode as a pending notice ([D-TW14](#D-TW14)), because its opening event is later than any cursor confirmed before it.
- One evaluation opens at most one episode for a subject. When the condition cleared and returned several times between two evaluations, the old episode is resolved once and one new episode is opened, from the count as it stands at that evaluation.
- A resolved episode's `resolved_reason` is `"condition-cleared"` in every row. Its `resolved_seq` is the `seq` of the oldest clearing event after `opened_seq` when one was read, and otherwise the greatest `events.seq` in the store when the evaluation started. Its `resolved_at` is the time of evaluation.
- The test is exact for clearing events, because an episode opens only when no clearing event follows its newest counted event, so any clearing event after `opened_seq` means the count reached zero, or for `alternating_pair` that the run the episode reported ended, while the episode was unresolved. An evaluator that ran after every event and one that ran once at the end therefore leave the same unresolved episode behind.
- Age is the one case that depends on when evaluations ran. Events older than `window` are not read, and the evaluator does not work out what an evaluation it never ran would have found. A subject whose count fell below `threshold` only because events aged, and rose again before the next evaluation, keeps its episode and is updated in place. So does one whose clearing event is itself older than `window` by the time of the evaluation.

Three worked examples. A `predicate_match` rule with `clear_when` has an open episode for a subject; an event on which `clear_when` holds arrives, then a counting event, then an evaluation runs. The evaluator resolves the episode, opens a second one with `episode_num` one greater, and `wait` prints it. A `consecutive_count` rule with `threshold = 2` reads fail, fail and opens an episode; before the next evaluation it reads pass, fail. The evaluator resolves the episode and opens none, because the count is 1. An `alternating_pair` rule with `pairs = [["a", "b"], ["c", "d"]]` and `threshold = 4` reads `a, b, a, b` and opens an episode; before the next evaluation it reads `c, d, c, d`. The evaluator resolves the episode and opens a second one for the pair `c`, `d`, because `c` ended the first run and the count is 4 again ([D-TW18](#D-TW18)).

**The expression grammar ([D-TW13](#D-TW13)).** `match`, `reset` and `clear_when` are written in this grammar, and so is everything else [D-TW4](#D-TW4) calls a predicate:

| Part | Forms |
| :--- | :--- |
| Operands | `event.<name>`; a string in double quotes; an integer; `true` and `false`; a list `[a, b]` of those |
| Comparisons | `==`, `!=`, and for two integers `<`, `<=`, `>`, `>=` |
| Membership and text | `x in [a, b]` (x equals a member); `x contains y` (a string holds a substring, or a list holds a value); `x matches "re"` (the string matches the RE2 regular expression) |
| Functions | `empty(x)`: true when x has no value or is `""`, an empty list or an empty object |
| Connectives | `not`, `and`, `or` and parentheses. Comparisons bind tightest, then `not`, then `and`, then `or` |

An expression always yields true or false. A comparison, membership or text test in which an operand has no value, or in which the two sides have different types, is false; that includes `!=`. An expression that does not parse is refused when the rule file is loaded. `age()` and the operands that read a projection (`item(...)`, `git.ref(...)`, `charter.<name>`, and bare names such as `seat` in the examples above) are part of the full grammar and are not evaluated by the first build ([Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15)).

**Every key of a rule, and what the loader checks ([D-TW16](#D-TW16)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question about what a `duration_without_event` rule must contain to load; the owner has not ruled on it and may overturn it. The table lists every key a rule may carry. A key that is not in it is refused, except where the last row says otherwise.

| Table | Key | Required | Value the loader accepts |
| :--- | :--- | :--- | :--- |
| `[[rule]]` | `id` | Yes | A string that is not empty, is at most 128 bytes long ([D-TW19](#D-TW19)), and that no other loaded rule has |
| `[[rule]]` | `enabled` | No | `true` or `false`; `true` when left out |
| `[[rule]]` | `scope` | Yes | `core`, or `charter:` or `workflow:` followed by a name that is not empty (`*` is a name). Which scopes the first build evaluates is in [D-TW21](#D-TW21) |
| `[[rule]]` | `severity` | Yes | `info`, `warning` or `high` |
| `[rule.reads]` | `events` | Yes | A list of one or more strings, each a kind that `event.Kinds()` lists |
| `[rule.reads]` | `projections` | No | A list of strings. The first build checks nothing more about them |
| `[rule.when]` | `group_by` | Yes | `global`, or a name as [D-TW13](#D-TW13) defines one. Only string values of that name form subjects ([D-TW22](#D-TW22)) |
| `[rule.when]` | `pattern` | Yes | One of the six pattern names |
| `[rule.when]` | `clear_when` | No | An expression |
| `[rule.when]` | `match`, `reset` | By pattern | An expression |
| `[rule.when]` | `threshold` | By pattern | An integer, 1 or more |
| `[rule.when]` | `window` | By pattern | A string holding a whole number followed by `s`, `m` or `h`, longer than zero |
| `[rule.when]` | `field` | By pattern | A name |
| `[rule.when]` | `pairs` | By pattern | A list of one or more lists, each holding two different strings |
| `[rule.guidance]` | `summary` | Yes | A string that is not empty |
| `[rule.guidance]` | `check`, `do` | Yes | A list of one or more strings, none empty |
| `[rule.delivery]` | `receiver` | Yes | `foreground` or `subscribers` |
| `[rule.delivery]` | `also_notify` | No | A list of strings, each `foreground` or `subscribers` |
| `[rule.when]` | Any other key | See below | See below |

"By pattern" means the pattern table above decides. For `predicate_match`, `count_in_window`, `consecutive_count` and `alternating_pair`, the keys that table lists for the pattern are the only ones allowed in `[rule.when]` besides `clear_when`. One it marks optional may be left out, every other one is required, and a key that belongs to a different pattern (`reset` in a `count_in_window` rule, `threshold` in a `predicate_match` rule) is refused.

**The two patterns that are loaded and not checked.** `lag_behind` and `duration_without_event` have no evaluator yet, and this document does not fix their keys: the `lag_behind` example above shows a likely set (`leader`, `follower`, `threshold_commits`, `threshold_duration`, `mode`), and `duration_without_event` has no example at all. For a rule with either pattern, the loader checks every key outside `[rule.when]` and the three shared keys `group_by`, `pattern` and `clear_when` exactly as the table states, and accepts every other key in `[rule.when]` with any value, unread. No key is required beyond the shared ones. The build that adds each pattern's evaluator fixes that pattern's keys and makes the loader check them; until then such a rule loads, is not evaluated, and is named on standard error ([Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15)). The one refusal that still reaches inside these rules is a key named `remedy`, which is refused wherever it appears ([OQ-TW2](#OQ-TW2)). Two alternatives were set aside by the designer: inventing a key set for `duration_without_event` now, which would fix a schema nothing has evaluated; and refusing both patterns at load, which would refuse the `unproved-trunk-lag` example above.

**The second build fixes both key sets.** The paragraph above describes the first build, which is the one that is built. [D-TW28](#D-TW28) fixes the keys of `lag_behind` and [D-TW29](#D-TW29) those of `duration_without_event`, both in [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27), and from that build the loader checks them like any other pattern's: a key they do not list is refused. The same section adds one optional key to `[rule.when]`, `fingerprint` ([D-TW32](#D-TW32)), fixes what an entry of `reads.projections` may be ([D-TW30](#D-TW30)), and lets a `lag_behind` rule leave `reads.events` out ([D-TW28](#D-TW28)). The `lag_behind` example above is written with the keys of [D-TW28](#D-TW28), and the first build loads it unread.

**What the loader parses and the first build does not evaluate ([D-TW16](#D-TW16)).** The loader parses every expression, including those of rules it will not evaluate, so the two example rules above must parse. The parser therefore accepts three forms beyond the grammar table, and a rule that uses any of them is not evaluated by the first build:

- a **bare name**: one or more identifiers joined by dots, not beginning with `event.` (`seat`, `pair`, `item.state`, `charter.finished`, `unproved_commits`);
- a **call**: an identifier or dotted name, then a parenthesized list of operands separated by commas, then optionally more dotted identifiers (`age(event.ts)`, `git.ref("refs/heads/main")`, `item("item-1").state`). `empty(x)` is the one call the first build evaluates;
- `x not in y`, which means `not (x in y)`. The right side of `in` and `not in` may be a list, a bare name or a call.

An identifier is a letter or underscore followed by letters, digits, underscores or hyphens. Anything else that the grammar table and these three forms do not cover does not parse, and the rule file is refused.

**What the first build puts into the guidance text ([D-TW16](#D-TW16)).** `guidance.summary` and each line of `guidance.check` and `guidance.do` are rendered when an episode opens. `{{subject}}` is the subject, `{{rule_id}}` the rule's `id`, `{{alert_id}}` the episode's `alert_id`, and `{{count}}` the count. `{{elapsed}}` is the time from the oldest counted event to the newest, in whole minutes (`80m`), or in whole seconds when under one minute (`45s`). For `alternating_pair`, `{{pair_a}}` and `{{pair_b}}` are the two members of the matched entry of `pairs` ([D-TW18](#D-TW18)), in the order the rule file writes them. Any other placeholder is left in the text as written; checking placeholders is `tillr watch lint`'s work ([Section 7.2](#72-tillr-watch-lint-checking-a-workflows-whole-configuration-before-it-runs-d-tw10)).

**No arbitrary code execution in rules (`D-TW4`):** Predicates use a small, total, typed expression grammar over registered event payloads and store read-views (`==`, `!=`, `<`, `<=`, `>`, `>=`, `in`, `contains`, `matches`, `empty()`, `age()`, plus the six stateful window aggregators `predicate_match`, `count_in_window`, `consecutive_count`, `alternating_pair`, `lag_behind`, `duration_without_event`). Because rules are pure data with no shell or Turing-complete script execution, agents can create and edit rules safely, and `tillr watch lint` can statically type-check every field reference before the rule runs.

### 5.2 The 16 starting rules and the 2026-11-20 cases they catch

The catalog ships **all 15 rules** from the operator's manual script list plus **1 diagnostic milestone rule** (`high-attempt-diagnostic`) directly addressing the fourth 2026-11-20 test case (`18 to 62 attempts` without capping work):

| # | Rule ID & scope | Reads (events / projections) | Default threshold & subject | Receiver | Guidance (`check` -> `do`) | 2026-11-20 case caught & time before a person noticed |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **1** | **`verify-refusal`**<br>`workflow:*` | `events`: `drain.node-advanced`, `drain.refusal`, `item_criteria` (`not-done` from `verify` seat) | `>= 1` verify refusal (`uncovered:` or `plan-wrong:`) per `(item_id, candidate_sha)` | `foreground` (or `subscribers`; builder already gets inline return) | **Check:** Read the attached `verify` criterion findings (`uncovered:` vs `plan-wrong:`).<br>**Do:** Verify whether `work` missed a criterion or `plan` cut narrow piece `write_scope`s; watch if a second refusal follows. | Verify refusals (`uncovered:` / `plan-wrong:`) routing items back into `work`/`plan` on 2026-11-20; previously invisible until the **up-to-15-minute** manual `tillr view events` script sweep (or longer when cascading into multi-attempt loops). |
| **2** | **`plan-verify-cycles`**<br>`workflow:*` | `events`: `drain.node-advanced`, `drain.graph-changed` (`verify -> plan` returns) | `> 1` full `plan -> ... -> verify` cycle (`>= 2` verify-to-plan cycles) per `item_id` | `foreground` | **Check:** Compare `graph.proposal` piece lists across cycle 1 and cycle 2 and read the `plan-wrong:` evidence.<br>**Do:** Fix ambiguous item criteria/design doc or widen piece scopes so `plan` stops re-proposing an unworkable split. | Multi-cycle `planned-build` items on 2026-11-20 that looped `plan -> work -> verify -> plan` repeatedly on the road to **18–62 attempts**, running **hours** before human timeline review (**>= 15m** per manual sweep). |
| **3** | **`worker-hours-without-completion`**<br>`charter:*` | `projections`: `runs` cumulative `duration_ms`; `events`: charter `progress_event` (`land.landing` or `item.verdict`) | `> 3` cumulative worker-hours across active lanes with `0` charter completions (`global`) | `foreground` | **Check:** Inspect top worker-hour consumers in bundle (slot-wait starvation, red trunk blocking landings, or looping items).<br>**Do:** Unblock the shared bottleneck (shard slot contention, gate stall, or broken trunk) burning fleet compute without throughput. | 2026-11-20 slot-contention & stall window where workers burned **> 3 worker-hours** (one `fixer-main` run alone spent **43 of 47m** waiting on test slots) with nothing landing; noticed only after **> 3 worker-hours** + **15m** script lag. |
| **4** | **`short-run-under-5s`**<br>`core` | `events`: `run.record` (`state == "exited"`, `ended_at - started_at < 5000ms`) | `>= 1` run `< 5s` (escalates to `high` at `>= 3` in `15m`) per `backend` / `item_id` | `foreground` | **Check:** Read adapter `exit_code`, stderr tail, and preflight failure in bundle (missing binary, bad CLI flag, expired auth token, broken worktree).<br>**Do:** Fix the broken adapter/auth/staging input before fast crashes spin through the queue. | Instant adapter/staging exits on 2026-11-20 and the four post-midnight `< 5s` 1-tick crashes in [`unproved-trunk-2026-11-19.md`](../tillr/unproved-trunk-2026-11-19.md#verified-timeline); went **15 minutes** on daytime script sweeps and **hours** overnight before a person noticed. |
| **5** | **`run-failover-suffix`**<br>`core` | `events`: `run.allocate`, `drain.backend-outcome` (run ID matching failover suffix or `ClassifyAttempt == AttemptInfrastructure`) | `>= 1` failover-suffixed run (or `>= 3` in `30m` fleet-wide) per `backend` | `foreground` | **Check:** Inspect primary backend quota/rate-limit window (`backend_quota`, `backend_window`) and HTTP/CLI error in bundle.<br>**Do:** Verify fallback model quality/cost for the seat (`tillr-seat-profiles`) or refresh degraded provider credentials. | Primary backend quota/infrastructure failovers on 2026-11-20 shifting runs onto failover-suffixed IDs; noticed up to **15 minutes** later on the manual `tillr view events` pass. |
| **6** | **`outside-allowed-paths-refusal`**<br>`core` | `events`: `boundary.check` (fail), `drain.refusal` (fence violation), or run ending `OBSTACLE: fence` | `>= 1` out-of-fence refusal or `OBSTACLE: fence` per `item_id` | `foreground` | **Check:** Read the exact forbidden path(s) attempted (`PATHS:` or `boundary.check` diff) vs `item_fence.txt` (e.g. forgotten `just pack-sync` mirror vs genuinely narrow item fence).<br>**Do:** Widen `allowed_paths` on the item or split a prerequisite item for the outside path. | Out-of-fence refusals on 2026-11-20, directly underlying the narrow-fence item that cycled for **80 minutes** before the owner asked (and up to **15 minutes** on ordinary script sweeps). |
| **7** | **`gate-refusal`**<br>`charter:git-landing` | `events`: `gate.verdict` (`pass == false`), `drain.refusal` (`stage == "gate"`) | `>= 2` consecutive gate refusals on one `item_id` (or `>= 1` on a `design` / `fixer` seat; tunable to `>= 1` globally) | `foreground` (single refusal already returns to warm worker session) | **Check:** Read named failing check IDs (`pack-sync`, `documents`, `citations`, or Go suite `FailingCase`) in the bundle.<br>**Do:** Confirm whether failure is candidate-caused (warm session fixing) or an unrecorded base trunk regression (`OBSTACLE: trunk`). | Repeated `gate.verdict` refusals on 2026-11-20 (including `pack-sync` and cross-module breaks); operator visibility lagged up to **15 minutes** per manual `tillr view events` sweep. |
| **8** | **`operator-hold`**<br>`core` | `events`: `drain.obstacle-hold` (`kind == "owner"`), `questions` (`answered_at IS NULL`), `holdrules` `"needs you"` | `>= 1` active hold naming the operator per `item_id` | `foreground` | **Check:** Read the exact `QUESTION:` and options extracted in the bundle (plus any design doc `OQ-*` anchor).<br>**Do:** Present the question concisely to the owner (or answer from an existing dated owner ruling per [`OQ-PR22`](../tillr/decision-provenance-review.md#OQ-PR22)), then record the answer to unblock. | Items entering `OBSTACLE: owner` / `"needs you"` holds on 2026-11-20 sat waiting silently until the next **15-minute** manual script check (or until the operator manually ran `tillr view waiting`). |
| **9** | **`unproved-trunk-lag`**<br>`charter:git-landing` | `events`: `land.landing`, `drain.revert`; `projections`: `refs/heads/main` vs `refs/tillr/green`, `heavy.cleanup-pending` | `>= 3` unproved landings ahead of `refs/tillr/green` OR `>= 1` unproved landing for `>= 15m` (`global`) | `foreground` | **Check:** Check `HeavyRunner` state, `heavy.cleanup-pending`, shard slot contention, or killed proof subtrees (`SIGTERM`), and inspect failing cross-module test.<br>**Do:** Unblock `HeavyRunner` or verify bisection/auto-revert (`drain.revert`) and `fixer-main` progress. | **2026-11-20 headline case:** Trunk stayed unproved for **25 minutes and 7 landings** after a change broke another module's test (and **8 hours** when worker `pkill -f go-test-shards` killed proof suites / overnight in `unproved-trunk-2026-11-19.md`). Caught at **landing #3 (~10m)** or **15m**. |
| **10** | **`post-landing-roll-failing`**<br>`charter:git-landing` | `events`: `drain.roll-failed`, `drain.build-started`, `land.landing`; `projections`: `tillr-roll.commit` vs `main` (`tools/tillr*` commits) | `>= 2` consecutive `drain.roll-failed` events OR `>= 5` `tools/tillr*` commits un-rolled for `> 15m` (`global`) | `foreground` | **Check:** Read `drain.roll-failed` `step` and `detail`, check `git status` on main checkout for dirty files refusing roll, and check live binary digest vs `tillr-roll.commit`.<br>**Do:** Fix the failing build/test or clean the dirty checkout, then run/trigger `tillr-roll` so live binaries catch up to `main`. | **2026-11-20 headline case:** `tillr-roll` failed **8 times in a row over 90 minutes**, leaving **29 landed commits** not running in live binaries before anyone noticed. Caught on **failure #2 (~20m)** while `<= 4` commits behind. |
| **11** | **`step-oscillation`**<br>`core` + `workflow:*` | `events`: `drain.node-advanced` | `>= 4` consecutive observations alternating (`A, B, A, B`: fires on the fourth, which is three moves) between the same two seats/stages (e.g. `plan <-> design`) per `item_id` within `4h` | `foreground` | **Check:** Diff item `allowed_paths`, criteria, and design ledger across the 4 observations in the bundle — what input failed to change between bounces?<br>**Do:** Widen `allowed_paths` (`tillr item update`), split an upstream item, or escalate to `owner` if fence/scope policy requires a ruling. | **2026-11-20 headline case:** An item went `plan -> design -> plan` **4 times over 80 minutes** because its `allowed_paths` were too narrow, found **only when the owner asked at 80m**. Caught on the fourth observation, which is the third move (and even earlier if paired with rule #6). |
| **12** | **`multiple-landings-build-unlanded`**<br>`charter:git-landing` | `projections`: `landings` (`result == 'landed'`) joined with `items` (`state NOT IN finished`) | `>= 2` recorded `landings` rows for one `item_id` while `item.state` is still non-terminal | `foreground` | **Check:** List the landed commits for `item_id` in the bundle (design amendments, reverts + retries, or partial commits that failed to advance item state).<br>**Do:** Verify why `recordLandedJudge` / item completion did not settle the item, or whether repeated trunk reverts (`drain.revert`) are undoing its landings. | 2026-11-20 items accumulating multiple `landings` rows (design/prep commits or reverted landings) while the item's build remained unlanded in queue; previously noticed only during manual timeline audits (**15m to hours**). |
| **13** | **`dead-run-holding-lane`**<br>`core` | `projections`: `lane_leases` joined with `runs` and OS process / run-bracket liveness (`kill(pid, 0)` + `runs.state`) | `>= 1` `lane_leases` row whose `run_id` is terminal in `runs` or whose local executor PID is dead for `> 30s` per `lane` | `foreground` | **Check:** Confirm `run_id` has exited (`runs.state` / PID gone) and inspect worktree cleanliness on `<lane>`.<br>**Do:** Release the orphaned lease (`tillr run release <lane>`) so the planner stops starving on a phantom busy lane. | Orphaned `lane_leases` rows on 2026-11-20 after abrupt worker/harness deaths starved lane capacity silently until the **15-minute** manual operator sweep noticed a lane stuck with no live process. |
| **14** | **`store-write-locked`**<br>`core` | `events` / sidecar telemetry: `watch.DiagLockRefused` / `SQLITE_BUSY` errors logged in `.tillr/loop/` or stderr streams | `>= 1` `SQLITE_BUSY` / `"database is locked"` write refusal (`global`, `10m` window) | `foreground` | **Check:** Identify which process bypassed `store.Open` `writeQueue` or held a long transaction on `.tillr/state.db` (e.g. raw `sqlite3` CLI or Python helper without `timeout` / read-only URI).<br>**Do:** Move reader to `?mode=ro` URI and route writes through `tillr store` / `busy_timeout` serialized transactions. | Store lock refusals on 2026-11-20 when external scripts/helpers contended with `.tillr/state.db` WAL writers, dropping best-effort records silently until manual log inspection (**15m+** lag). |
| **15** | **`empty-reason-record`**<br>`core` | `events`: `drain.refusal`, `drain.obstacle-hold`, `drain.roll-failed`, `gate.verdict`, `item.rejection`, `item_criteria` (`not-done`) | `>= 1` failure/hold/refusal record whose `reason` / `detail` is empty or omits a check/test identifier (`global` / `event_seq`) | `foreground` | **Check:** Inspect the raw event `seq` and producer binary/script named in the bundle (`tillr-roll.py`, judge, gate check, or obstacle parser).<br>**Do:** Inspect the underlying raw log immediately so the hidden failure is not missed, and file a defect item against the producer emitting blank reasons. | **2026-11-20 headline case:** `tillr-roll` failed 8 times over **90 minutes** with a record naming **no failing test**, hiding 29 un-rebuilt commits for **90 minutes** before human discovery. Caught on **failure #1 (~11m)**. |
| **16** | **`high-attempt-diagnostic`**<br>`core` | `projections`: `runs` / `drain.decision` attempt count per `item_id` (`item.state NOT IN finished`) | `>= 8` attempts on one `item_id` (re-notifies every `+8` if silenced; **never** caps or holds per [D-TW2](#D-TW2)) | `foreground` | **Check:** Read the compressed attempt history in the bundle (are attempts failing on distinct real sub-problems, slot/infra churn, or subtle non-identical repeats escaping `repeatedEnding`?).<br>**Do:** Let healthy multi-step progress run; if churning on an unstated obstacle or bad fence, intervene on the root cause. | **2026-11-20 headline case:** **Four items reached 18 to 62 attempts each** before human inspection. Caught at **attempt 8** (saving 10 to 54 uninspected attempts per item) while strictly honoring the no-cap ruling. |

### 5.3 Replay against the four 2026-11-20 benchmark incidents

Testing the catalog above against the four concrete 2026-11-20 failures from the request confirms both detection latency and run-spend discipline:

1. **Planner <-> designer 4x over 80 minutes (narrow `allowed_paths`):**
   - *What happened:* `plan` saw it needed a path outside `allowed_paths`, reported `OBSTACLE: definition`, `design` ran and returned without changing the item's `allowed_paths`, and the cycle repeated 4 times over 80 minutes until the owner asked.
   - *With `tillr-watch`:* Rule #6 (`outside-allowed-paths-refusal`) fires on the first fence collision (~15–20m), and Rule #11 (`step-oscillation`) fires deterministically on the fourth observation (`plan, design, plan, design`, which is three moves), attaching the item's `allowed_paths` alongside the exact path named in `plan`'s obstacle and telling the receiver: *"widen `allowed_paths` via `tillr item update`."* Total extra worker runs spent to detect: **0**.
2. **`tillr-roll` failing 8 times over 90 minutes with no test named (29 commits un-running):**
   - *What happened:* `tillr-roll.py` inserted `drain.roll-failed` with no test name; `tillr-drain` kept running old binaries across 29 commits for 90 minutes.
   - *With `tillr-watch`:* Rule #15 (`empty-reason-record`) fires on **failure #1 (~11m)** because `drain.roll-failed` named no test/check, and Rule #10 (`post-landing-roll-failing`) escalates to `high` on **failure #2 (~22m)**, waking the foreground whale 68–79 minutes earlier with the commit range and `tillr-roll` stderr log path.
3. **Trunk unproved for 25 minutes and 7 landings:**
   - *What happened:* A landed change broke another module's test; `refs/tillr/green` froze while 7 more commits landed over 25 minutes.
   - *With `tillr-watch`:* Rule #9 (`unproved-trunk-lag`) fires at **landing #3 (~10m)** (and no later than `15m`), attaching `green` SHA, `main` SHA, the 3 unproved commits, and `HeavyRunner`'s current check status.
4. **Four items with 18 to 62 attempts each:**
   - *What happened:* Four items churned across 18–62 attempts without tripping identical-string `repeatedEnding`.
   - *With `tillr-watch`:* Rule #16 (`high-attempt-diagnostic`), Rule #2 (`plan-verify-cycles`), and Rule #12 (`multiple-landings-build-unlanded`) open one episode per item at attempt 8 / cycle 2, handing the whale a single 8 KiB timeline digest without ever failing, benching, or stopping the items.

### 5.4 The first rules shipped: ten conditions seen on 2026-11-21 ([D-TW39](#D-TW39))

This section records an operator request filed on 2026-11-21, and the designer's reading of it. Neither is the owner's ruling, and the owner may overturn any of it. The request amends three earlier statements of this document: that the rule catalog waits for a later build ([D-TW27](#D-TW27)), that no build before the catalog adds a rule file ([D-TW36](#D-TW36)), and that the first rules are the sixteen of [Section 5.2](#52-the-16-starting-rules-and-the-2026-11-20-cases-they-catch) ([D-TW4](#D-TW4)). A build worker stopped on the request on 2026-11-21 because those three statements contradicted it, and asked whether the ten rules add to the sixteen or replace them. This section is the answer.

**Why now.** The first build landed as commit `e85ae973d9` and foreground delivery as commit `d3f2c8cf96`, both on 2026-11-21, and `ls config/tillr` on that day listed no `watch` directory. With no rules directory, every session defers attachment ([D-TW36](#D-TW36)), so the landed watcher reports nothing. The request quotes the owner asking on 2026-11-21: "so tillr watch is set up to send you alerts now? what alerts do we have configured?" The honest answer was none. The operator's request is to give the watcher rules that the landed evaluator can run, without waiting for the second build.

**What is delivered.** The build creates the directory `config/tillr/watch` in this repository and puts ten rules in it, as TOML files in the schema of [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5). It changes no evaluator, loader or command. Each rule comes from a condition the operator saw in the drain on 2026-11-21:

| # | Condition the rule reports | Incident of 2026-11-21 named by the request | Designer's expectation: evaluated today? |
| :--- | :--- | :--- | :--- |
| 1 | The drain loop exited on a fault. | The exits between 2:05 PM and 2:27 PM ET, and the exit at 5:51 PM ET. | Yes, an event rule. |
| 2 | One item was refused by its verifier many times in a row. | The build of the flaky-check work, refused twelve times. | Yes, an event rule. |
| 3 | One item moved back and forth between a build obstacle and a design answer repeatedly. | The build of the `tillr-watch` core. | Yes, an event rule. |
| 4 | An item is held for the operator. | Holds for verify triage, for a conflict awaiting the owner, and for a trunk obstacle, which sat up to 15 minutes between the operator's check-ins. | Yes, an event rule that fires when the hold is recorded. |
| 5 | A landing was reverted, or a revert was not possible. The heavy trunk failure the request also names gets no rule of its own ([D-TW40](#D-TW40), below). | The request names the three conditions and no single incident. | Yes, two event rules. |
| 6 | A trunk fixer was filed. | The request names the condition and no single incident. | Yes, an event rule. |
| 7 | A squash conflict stopped a landing. | The request names the condition and no single incident. | Yes, an event rule. |
| 8 | An item fault was recorded. | The request names the condition and no single incident. | Yes, an event rule. |
| 9 | The trunk has been unproved for longer than a stated time. | The request names the condition and no single incident. | No. It needs `lag_behind` or `duration_without_event` ([D-TW28](#D-TW28), [D-TW29](#D-TW29)). |
| 10 | An ordinary lane is idle while items wait only for a lane. | The request names the condition and no single incident. | No. It needs a fact about lanes and waiting items that no event carries alone. |

The numbers are the request's. A numbered condition may be written as more than one rule: condition 5 is delivered as two rules, one per recorded revert outcome, and one rule per fact gives one episode per fact. The last column is the designer's expectation and not a requirement. What decides it is the landed loader: a rule is evaluated when `UnevaluatedReason` in `tools/tillr/watch/rules.go` returns the empty string for it, which is the four-part test of [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15).

**Condition 5 is two rules, and a heavy trunk failure has no rule of its own ([D-TW40](#D-TW40)).** This is the designer's decision, and the owner may overturn it. It answers a second build worker, who stopped on 2026-11-21 because the request asks for a rule on a heavy trunk failure and also allows registered event kinds only, and the tree records no event for that failure. A heavy trunk check is one of the slow checks the drain runs on the trunk after a landing, to prove the trunk and move `refs/tillr/green`. What the worker found, read again by the designer on 2026-11-21:

- The verdict of a heavy run is written to a file and not to the event stream. `publishHeavy` in `tools/tillr-drain/drain/heavy.go` writes `heavy.json`; `rg -n 'tx.Append' tools/tillr-drain/drain/heavy.go tools/tillr-drain/drain/heavy_loop.go tools/tillr-drain/drain/heavy_progress.go` returned nothing.
- The automatic revert of a landing is recorded. `drain.revert` (`RevertKind` in `tools/tillr-drain/drain/heavy_revert.go`) is a registered kind, and its payload field `result` is `reverted` or `not-possible`.
- That event is written only when a revert is attempted. `HeavyRunner.revert` returns without attempting one when automatic revert is off (the loop sets `runner.Revert` only under the gate policy's `auto_revert`), and a failure that the culprit search could not attribute to one landing is never offered to it.

So condition 5 is delivered as two rules, both reading `drain.revert`: one for `result == "reverted"` and one for `result == "not-possible"`. No third rule is written for the failure itself. The request's two requirements cannot both hold for it, and the designer keeps the one that protects every other rule: registered kinds only.

A heavy failure with no revert attempt is not left unreported. `HeavyRunner` files a trunk fixer for every failing landing it did not revert, and for a failure it could not attribute (the comment on its `Revert` field: "A landing it reverted gets no fixer; one it could not revert gets the fixer"; with no `Revert` it "files fixers only"). Condition 6 reports a trunk fixer filed. Between them, the two rules of condition 5 and the rule of condition 6 fire once for each heavy failure that the drain answers with a remedy. What they do not report is the failure as a fact of its own: which checks failed, and a failure whose fixer was already filed by an earlier run. Condition 9 covers the lasting form of that, a trunk that stays unproved, once the second build evaluates it. The guidance of the two revert rules and of the condition 6 rule tells the reader to open the heavy run's record for the failing checks.

Three alternatives were set aside by the designer. An event written for each heavy verdict would let a rule report the failure directly, but it is a change to the drain, and this delivery changes no evaluator, loader or command and only adds rule files and a test; it is separate work that the request does not ask for. A third rule written against a kind that is not registered would be refused by the loader, and a refusal of one file returns no rules at all. A rule that reads `heavy.json` through a projection would load and never be evaluated, because that file is outside the store and outside the three projections of [D-TW30](#D-TW30), and a rule that can never fire would read in the directory as coverage that does not exist.

**The rules add to the sixteen; they do not replace them.** The sixteen rules of [Section 5.2](#52-the-16-starting-rules-and-the-2026-11-20-cases-they-catch) stay the planned catalog, and the build that writes the catalog still owes each of them. That build starts from the rules already in `config/tillr/watch`. For each of the sixteen it either adds the rule or records, in this document, which shipped rule already reports the same condition. Some conditions overlap: condition 4 here and `operator-hold`, condition 9 and `unproved-trunk-lag`, condition 3 and `step-oscillation`, condition 2 and `verify-refusal` or `gate-refusal`. A shipped rule takes a catalog rule's `id` only when it is that rule as [Section 5.2](#52-the-16-starting-rules-and-the-2026-11-20-cases-they-catch) states it, with the same condition and threshold. Otherwise it takes a new `id`, so that an episode opened today is never read later as an episode of a different rule. Two alternatives were set aside by the designer. Replacing the sixteen would drop conditions the 2026-11-20 incidents showed and the 2026-11-21 incidents did not, such as a run that ends in under five seconds. Waiting for the catalog build would leave the landed watcher with nothing to report through the second build and three builds after it.

**What each rule must satisfy.** These are the request's requirements, with the designer's reading where one is marked:

- **Registered event kinds only.** Every entry in `reads.events` is a kind in the event registry of the process that loads the rule. The loader already refuses any other (`unregistered event kind` in `tools/tillr/watch/rules.go`). The registry of the `tillr-watch` binary is what its `main.go` links, which on 2026-11-21 was the drain package and what that package imports. This document names no event kind for any of the ten: the build reads the registry and each event's real payload, and writes each rule against them, as [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) already requires of catalog rules. A condition for which no registered event exists is not given an invented kind. It is reported by the build as a `definition` obstacle that names the condition.
- **The incident and the kinds are stated in the rule file.** Each rule carries, in a TOML comment above it, the incident of 2026-11-21 it comes from and the event kinds it reads.
- **Guidance says what to look at, and no rule applies a remedy.** The owner ruled on 2026-11-21: "We don't cover issues. We use this to find the issues and fix them" ([OQ-TW2](#OQ-TW2)). The loader requires both `guidance.check` and `guidance.do`. The designer's reading: `check` names the records to read, and `do` names what to establish about the cause. Neither holds a command that changes an item, a run, a lane or the trunk.
- **A rule meant to be evaluated today uses a scope the first build evaluates.** That is `core`, `workflow:*` or `charter:*` ([D-TW21](#D-TW21)). A rule scoped to a named charter, such as `charter:git-landing`, loads today and is not evaluated, so a condition of the Git landing charter is scoped `core` in this delivery. This is the designer's reading.
- **Every rule's receiver is `foreground`.** This is the designer's reading: the request's purpose is that the foreground agent is told.
- **Thresholds are the build's first values.** The request states no count for "many times in a row" or "repeatedly", and no time for condition 9. The build picks values the named incident would have fired, states each in the rule's comment, and they are tuned afterwards as any rule is ([OQ-TW5](#OQ-TW5)). This is the designer's reading.

**Rules that wait for the second build.** A rule that needs `lag_behind`, `duration_without_event` or a projection is written now and loads now. The landed binary names it as not evaluated: `eval` and `wait` print one line per such rule on standard error when they start, with the reason ([Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15)). The landed `status` prints skipped-event counts only ([D-TW22](#D-TW22)); a line per rule in `status` arrives with the second build ([D-TW34](#D-TW34)). Such a rule is written to the keys the second build fixes ([D-TW28](#D-TW28) for `lag_behind`, [D-TW29](#D-TW29) for `duration_without_event`, [D-TW30](#D-TW30) for projections), although the landed loader accepts those keys unread ([D-TW16](#D-TW16)). The reason is that a refusal of one file returns no rules at all, so a rule the second build's stricter loader refused would stop every watcher in this repository. When a condition needs a projection outside the three of [D-TW30](#D-TW30), the rule is still written; it stays unevaluated after the second build, named with its reason, until a later build adds that projection. Condition 10 is the likely case.

**Every shipped rule lists at least one event kind, the rules that wait included.** A build worker stopped a second time on 2026-11-21. Its plan required the rule for condition 9 to carry `reads.events = []`, and the loader test failed with `reads.events: expected a non-empty list of strings`. The plan took that empty list from [D-TW28](#D-TW28), which says that in a `lag_behind` rule `reads.events` "may be left out or empty". That sentence states what the second build's loader will allow. It is not a requirement on a rule, and the landed loader does not allow it yet: `tools/tillr/watch/rules.go` requires a non-empty `reads.events` in every rule, whatever its pattern. This delivery changes no loader, so the designer's answer is that the rule changes and the loader does not:

- Every rule in `config/tillr/watch` has a `reads.events` list with at least one registered kind. That holds for a `lag_behind` rule, a `duration_without_event` rule and a rule that reads a projection.
- Such a list is valid under both loaders. [D-TW28](#D-TW28) says of a `lag_behind` rule that when `reads.events` is given, "each kind is still checked against `event.Kinds()`, and the events are not read". The example rule of [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) is written this way.
- For a rule whose pattern reads no event, the build lists the registered kinds whose events change the condition the rule reports. For condition 9 those are the kinds recorded when a commit lands on the trunk and when a landing is reverted; the heavy verdict that proves the trunk is written to no event ([D-TW40](#D-TW40)), so no kind is listed for it. The build takes the names from the registry, as it does for every other rule, and states them in the rule's comment. The list is a statement for the reader until a build evaluates the rule.
- An empty or absent `reads.events` is not written in this delivery, for any rule. Allowing it stays with the second build, as [D-TW28](#D-TW28) states.

Changing the landed loader in this delivery to accept an empty list was set aside by the designer: the request says the rules are ones "the landed evaluator can run today", and a loader change is second-build work with its own tests. This is the designer's reading, and the owner may overturn it.

**The test.** One test loads `config/tillr/watch` with the real loader, `watch.LoadRules`, in a process that links the same event owners as the `tillr-watch` binary. It fails when the directory does not load, and for each rule it asserts one of two outcomes by `id`: the rule is evaluated (`UnevaluatedReason` is empty), or it is unevaluated with the stated reason. The list of rules awaiting the second build is written in the test, so a rule that silently stops being evaluated fails it. The designer's reading: the test belongs to the `tools/tillr-watch` module, because that module links the binary's event registry and has no pack copy ([D-TW24](#D-TW24)), so a path from the test to the repository's `config/` directory holds wherever the test runs. Because the same test runs in the gate when the second build lands, that build must keep the directory loading and move its rules from the awaiting list to the evaluated one.

**What changes for a session.** Once `config/tillr/watch` exists, the rule of [D-TW36](#D-TW36) no longer defers attachment in this repository: a session that also finds `.tillr/state.db` starts its watcher. Nothing else in [D-TW36](#D-TW36) changes. The rules are tracked files in this repository and are read from the working tree; this delivery adds no pack copy and no deploy step.

---

## 6. Delivery, Lifecycle, and the Two Operating Modes

### 6.1 One record per condition, never per pass: the Alert Episode state machine ([D-TW5](#D-TW5))

If `tillr-drain` runs a pass every few seconds while `refs/tillr/green` lags for 25 minutes, a naive checker would emit hundreds of duplicate alerts and trigger hundreds of agent turns. Extending `D-OC1`–`D-OC5` in [the retired agent runtime design Section 20](./agent-runtime-retirement.md#what-happened-to-each-section), `tillr-watch` enforces **one active episode per `(rule_id, subject_key)`** in `.tillr/state.db`:

```sql
CREATE TABLE IF NOT EXISTS alert_episodes (
    alert_id               TEXT PRIMARY KEY,          -- e.g. "alrt-01948f..."
    rule_id                TEXT NOT NULL,
    subject_key            TEXT NOT NULL,             -- item_id, lane, backend, or "global"
    episode_num            INTEGER NOT NULL,          -- monotonic per (rule_id, subject_key)
    state                  TEXT NOT NULL CHECK (state IN ('open', 'acknowledged', 'silenced', 'resolved')),
    severity               TEXT NOT NULL,
    condition_fingerprint  TEXT NOT NULL,             -- SHA-256 of canonical cause facts
    opened_seq             INTEGER NOT NULL,          -- events.seq that crossed threshold
    opened_at              INTEGER NOT NULL,
    last_observed_seq      INTEGER NOT NULL,
    last_observed_at       INTEGER NOT NULL,
    observation_count      INTEGER NOT NULL DEFAULT 1,
    acked_by               TEXT,
    acked_note             TEXT,
    acked_at               INTEGER,
    silence_until_ms       INTEGER,                   -- NULL unless time-silenced
    silence_on_fingerprint TEXT,                      -- non-NULL when --until-change
    active_triage_run_id   TEXT,                      -- at most 1 drain triage run while active
    resolved_seq           INTEGER,
    resolved_at            INTEGER,
    resolved_reason        TEXT,
    bundle_json            TEXT NOT NULL              -- latest TimelineBundle snapshot
);

CREATE UNIQUE INDEX IF NOT EXISTS alert_episodes_one_active
    ON alert_episodes(rule_id, subject_key)
    WHERE state != 'resolved';
```

```mermaid
stateDiagram-v2
    [*] --> open : threshold crossed\n(emit watch.alert.opened)
    open --> open : subsequent pass still true\n(update last_observed_seq & count only — NO new event, NO wake)
    open --> acknowledged : tillr watch ack <id> --by <actor>\n(emit watch.alert.acknowledged)
    open --> silenced : tillr watch silence <id>\n(emit watch.alert.silenced)
    acknowledged --> silenced : tillr watch silence <id>
    silenced --> open : silence_until_ms expired OR\ncondition_fingerprint != silence_on_fingerprint\n(emit watch.alert.reopened)
    open --> resolved : predicate clears at new seq\nOR manual resolve on edge rule\n(emit watch.alert.resolved)
    acknowledged --> resolved : predicate clears at new seq
    silenced --> resolved : predicate clears at new seq
    resolved --> [*]
```

The four lifecycle operations behave deterministically:

1. **Deduplicated observation (`open -> open`):** On every subsequent cursor advance where `(rule_id, subject_key)` still satisfies `when`, `tillr-watch` increments `observation_count` and updates `last_observed_seq`/`last_observed_at` in `alert_episodes` **without** appending a `watch.alert.opened` event, waking `foreground`, or dispatching `drain:<seat>`.
2. **Acknowledge (`tillr watch ack <alert-id> --by <actor> [--note "..."]`):** Transitions `open -> acknowledged`, recording ownership and suppressing any pending wakeup or triage dispatch while keeping the episode visible on `tillr watch status` and Observatory.
3. **Silence (`tillr watch silence <alert-id> (--until <duration|timestamp> | --until-change)`):** Transitions `open|acknowledged -> silenced`. A time-bounded silence (`--until 1h`) re-opens only if the predicate is still true after expiry; a fingerprint-bounded silence (`--until-change`) stays muted while the *same* `condition_fingerprint` holds and immediately re-opens (`watch.alert.reopened`) if a *different* failure signature appears under the same rule and subject.
4. **Resolve (automatic and explicit):** Level-triggered rules (`unproved-trunk-lag`, `post-landing-roll-failing`, `dead-run-holding-lane`, `operator-hold`, `step-oscillation` once the item advances or finishes) transition `* -> resolved` **automatically** (`resolved_reason = "condition-cleared"`) on the exact `events.seq` / ref state that clears `clear_when`. Edge-triggered point events (`short-run-under-5s`, `empty-reason-record`) auto-resolve after their window expires with zero recurrences or immediately via `tillr watch resolve <alert-id> --reason "..."`. If the condition crosses the threshold again *after* `resolved`, `episode_num + 1` opens cleanly as a fresh episode; [D-TW17](#D-TW17) in [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) states what happens when a condition clears and returns between two evaluations.

### 6.2 How the receiver gets the item's timeline with the alert ([D-TW6](#D-TW6))

To prevent receivers from running `tillr view events` scripts or spawning worker sessions just to gather context, `tillr-watch` builds a self-contained **`TimelineBundle`** (`<= 8 KiB` JSON + rendered Markdown summary) in one read-only sidecar query (sharing projection semantics with [`tools/tillr-view/item_history.go`](../../tools/tillr-view/item_history.go) and `tools/tillr-trace`) at the moment an episode opens (or re-opens on fingerprint change):

- **Subject header:** `item_id`, title, charter `state`, `workflow` + current stage, `attempt` count, cumulative worker minutes, and exact `allowed_paths` fence (or `global` pointers: `main` SHA, `refs/tillr/green` SHA, `tillr-roll.commit` SHA).
- **Ordered state & stage transitions:** Compact chronological spine (`ts`, `seq`, `from -> to`, `actor`/`seat`, `reason`) showing the exact bounce or stall trajectory.
- **Recent runs table (last 10):** `run_id`, `node`/`seat`, `backend`, `duration`, `exit_code`, `result` (`done` / `blocked`), and parsed `OBSTACLE:` + `PATHS:` / `EVIDENCE:` / `QUESTION:` lines.
- **Refusal & failure digest:** Latest `verify` `not-done` reasons, `gate.verdict` failing check IDs + named `FailingCase` reproduce commands, `boundary.check` out-of-fence paths, or `drain.roll-failed` step/detail.
- **Landings & reverts for this subject:** Every `landings` row (`trunk_after`, `gated_tree == landed_tree`, `result`) and any `drain.revert` referencing it.
- **Rule guidance + ready-to-run commands:** The rule's `guidance.check` and `guidance.do` rendered with subject bindings, plus one-line `tillr watch ack` / `silence` commands.

### 6.3 Mode A: Mechanical answers inside `tillr` and non-wasting drain triage ([D-TW7](#D-TW7), [OQ-TW2](#OQ-TW2), [OQ-TW4](#OQ-TW4))

**Ruled by the owner on 2026-11-21: neither path of Mode A is built.**

1. **No automatic remedy ([OQ-TW2](#OQ-TW2)).** `tillr-watch` executes no action when a rule fires. It writes `watch.*` records and nothing else. The owner's words: "Every single one of these sounds like a fallback to cover an issue. We don't cover issues. We use this to find the issues and fix them." A rule has no `remedy` key, and `tillr watch lint` rejects a rule file that declares one. A condition such as a lease held by a dead process is an alert whose guidance names the defect to file; the fix is a change to the code that left the lease.
2. **No dispatch to a drain seat yet ([OQ-TW4](#OQ-TW4)).** Every alert goes to the foreground agent. `receiver = "drain:<seat>"` is not a legal value, and `tillr watch lint` rejects it. On each alert the foreground agent acts and records whether a better path existed; routing to a seat is designed from that record once the system is in use, not before. [D-TW7](#D-TW7) is set aside until then.

### 6.4 Mode B: Persistent, self-registered foreground agent watches ([D-TW8](#D-TW8), [OQ-TW3](#OQ-TW3))

Mode B solves the second problem the owner described on 2026-11-20: a foreground whale session is juggling 10 live concerns at once — waiting for one item to land before starting a follow-up, waiting for `refs/tillr/green` to prove a fix, watching whether `lane-4` finishes its long run, and watching if a flaky check fails again — and today it neither remembers all 10 conditions across context compactions / `/handoff` / session restarts nor notices them without polling `tillr view` by hand.

An agent registers, inspects, edits, or drops its own watches in one command, with **no fixed process and no prescribed action** — just a condition and a note to its future self:

```bash
# Register in one command (defaults to --once and --owner foreground)
tillr watch add \
  --when 'item("item-request-b1d51332cd").state in charter.finished' \
  --note 'State-machines design landed; check if docs/design/tillr-watch.md links can be promoted from inline code.'

tillr watch add \
  --when 'git.ref("refs/tillr/green") == git.ref("refs/heads/main")' \
  --note 'Trunk is proved green again; safe to run host binary rollout check.'

tillr watch add --recurring \
  --when 'event.kind == "drain.revert"' \
  --note 'HeavyRunner just auto-reverted a trunk commit; inspect whether it was one of today’s gate changes.'

# Inspect, edit, or drop across any session
tillr watch list
tillr watch edit wtch-01948f2a --note 'Updated note after scope change'
tillr watch drop wtch-01948f2a
```

#### Durable cross-session storage (`agent_watches` in `.tillr/state.db`)

Agent watches live in `.tillr/state.db` (`agent_watches` table), keyed by `watch_id` and `owner_handle` (default `"foreground"` for the workspace's foreground operator seat, or a named session/agent handle):

```sql
CREATE TABLE IF NOT EXISTS agent_watches (
    watch_id            TEXT PRIMARY KEY,             -- e.g. "wtch-01948f..."
    owner_handle        TEXT NOT NULL DEFAULT 'foreground',
    when_expr           TEXT NOT NULL,                -- validated predicate expression
    note                TEXT NOT NULL,                -- free-form note to future self
    repeat_mode         TEXT NOT NULL DEFAULT 'once' CHECK (repeat_mode IN ('once', 'recurring')),
    state               TEXT NOT NULL DEFAULT 'armed' CHECK (state IN ('armed', 'fired', 'dropped')),
    armed_at_seq        INTEGER NOT NULL,             -- events.seq at registration
    last_evaluated_seq  INTEGER NOT NULL,
    last_fired_seq      INTEGER,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
```

Watches are rows in the store, not facts held in a model's context or in a scratch file, so a new foreground session inherits every armed watch for `owner_handle = "foreground"`: they survive a context compaction, a `/handoff`, a crash and a jail relaunch. A `--once` watch (the default, for *"tell me when X happens"*) moves `armed -> fired` the moment `when_expr` evaluates true, so it fires one time; a `--recurring` watch fires once per distinct matching event or episode transition until `tillr watch drop` removes it.

#### How a notice reaches the foreground agent ([D-TW11](#D-TW11), [OQ-TW3](#OQ-TW3))

The owner ruled on 2026-11-21 that delivery ties into the background-watcher machinery he already runs for a CI watcher, and that no new Pi extension is built: "for Claude, you can easily do a background watcher that pings things. It does have to be restarted every two hours", and for Pi the same "without the two-hour limit". Everything below this paragraph is the designer's decision ([D-TW11](#D-TW11)), which the owner may overturn. It leaves no question open.

Four terms are used here:

| Term | Meaning |
| :--- | :--- |
| **Notice** | What the foreground agent is shown for one firing: a fired agent watch (its note) or a `foreground` alert episode that opened or reopened (its guidance), each with its `TimelineBundle`. |
| **Pending notice** | A notice the owner has not confirmed seeing, whose alert is still `open` or whose watch has not been dropped. An alert that was acknowledged, silenced or resolved in the meantime is no longer pending. |
| **Seen cursor** | The highest `events.seq` the owner has confirmed seeing, kept per `owner_handle` in the store. It is the only delivery state, and no harness holds a copy. |
| **Re-arm** | Starting the watcher again after it exits. In both harnesses the agent does this, with the command the watcher printed as its last line. |

**One primitive, `tillr watch wait`.** It is an ordinary foreground process with no knowledge of either harness. On start, and then on every store wakeup, it advances the rule evaluator the way `tillr watch eval --once` does (so delivery works whether or not `tillr watch daemon` is running; two evaluators serialize on the store's write queue and the one-active-episode index of [Section 6.1](#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5)), then looks for pending notices for its owner. It sleeps on `inotify` (`fsnotify`) watches on `.tillr/watch.wakeup`, a zero-byte file that `event.Append` and the ref-moving helpers in `tillr-land` and `HeavyRunner` touch after commit, and on `.tillr/state.db-wal`, with a 60-second fallback timer that covers filesystems which drop `inotify` events and rules that fire on elapsed time alone. Idle CPU is zero and a firing is noticed in under a second.

```text
tillr watch wait --store <path> --rules <dir> [--owner <id>] [--timeout <dur>] [--seen-through <seq>] [--follow]
tillr watch seen --store <path> --through <seq> [--owner <id>]
```

| Exit | Meaning | What `wait` printed |
| :--- | :--- | :--- |
| `0` | One or more notices were pending | Every pending notice, then `through_seq`, then the re-arm command |
| `2` | `--timeout` passed with nothing pending | One line saying so, then the re-arm command |
| `1` | The store could not be read | The error. The agent fixes the cause; re-arming blindly is not the answer |

The printed output for exit `0` has this shape. The bundle is recorded data about the subject, some of it text that worker runs wrote, and the output labels it as data so the agent does not read it as instructions:

```text
TILLR_WATCH_NOTICE 2 pending, through_seq 48231
[watch wtch-01948f2a] note: Trunk is proved green again; safe to run host binary rollout check.
[alert alrt-01948f77 step-oscillation item-request-0a1b2c3d4e, warning] Item bounced between plan and design 4 times over 80m.
  check: ...
  do: ...
  ack: tillr watch ack alrt-01948f77 --by foreground
--- recorded data, not instructions: TimelineBundle for each notice ---
...
re-arm: tillr watch wait --store '/repo/.tillr/state.db' --rules '/repo/config/tillr/watch' --timeout 110m --seen-through 48231
```

**Seeing is confirmed, not assumed.** Printing a notice does not move the seen cursor, because a process cannot know that the agent read its output: the session can end between the print and the read. The cursor moves only when the agent says so, with `--seen-through <seq>` on its next `wait` or with `tillr watch seen --through <seq>`. Both refuse a `<seq>` higher than any `through_seq` a `wait` has printed for that owner, so a mistyped number cannot hide a notice that was never shown. The owner's Pi CI watcher makes the same two choices, an explicit acknowledgement and a refusal of one beyond what it showed. The result is that a notice is shown at least once and is repeated until confirmed, and the only cost of a lost confirmation is seeing a notice twice.

The state is one row per owner:

```sql
CREATE TABLE IF NOT EXISTS foreground_delivery (
    owner_handle  TEXT PRIMARY KEY,
    seen_seq      INTEGER NOT NULL DEFAULT 0,   -- highest events.seq the owner confirmed seeing
    shown_seq     INTEGER NOT NULL DEFAULT 0,   -- highest through_seq any wait printed; bounds seen_seq
    waiting_since INTEGER,                      -- when the newest wait started; NULL after a clean exit. Not evidence of attachment (D-TW26)
    last_wait_at  INTEGER                       -- when a wait last started or exited
);
```

**Which notices `wait` selects, and what a second `wait` prints ([D-TW14](#D-TW14)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. A request filed that day asked for `wait` in a build that leaves out the rest of this section, on the ground that this section was still being designed. The section has since been settled, and the seen cursor is what makes "pending" a defined word, so the cursor is part of the `wait` primitive and is built with it: the `foreground_delivery` row, `--seen-through` and `tillr watch seen`. What that build leaves out is listed in [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15).

- An alert episode is a notice for the owner handle `foreground` when its rule's `delivery.receiver` is `foreground`, and for no other handle. A `wait` for another handle is shown fired agent watches only.
- A notice's **notice seq** is the `events.seq` of the `watch.alert.opened` or `watch.alert.reopened` event that last opened the episode, or of the `watch.fired` event for a watch.
- An alert notice is **pending** for an owner when the episode's state is `open` and its notice seq is greater than the owner's `seen_seq`.
- `wait` prints every pending notice in notice seq order. `through_seq` is the greatest notice seq printed. Printing sets `shown_seq` to `through_seq` when that is greater, and never changes `seen_seq`.
- A `wait` that finds notices pending when it starts prints them and exits `0` at once, before sleeping.
- A second `wait` with no confirmation between the two prints the same notices again, for as long as their episodes are `open`. A second `wait` started with `--seen-through <seq>`, or after `tillr watch seen --through <seq>`, does not print a notice whose notice seq is `<seq>` or lower; it blocks until a later one is pending.
- An episode that resolves and later opens again is a new episode with a new `watch.alert.opened` event, so its notice seq is greater than a cursor confirmed before it, and it is printed.
- Without `--timeout`, `wait` blocks until a notice is pending. `--seen-through` is applied before the first evaluation, and a refused `--seen-through` (one above `shown_seq`) ends `wait` with exit `1` and prints nothing else.

Seeing a notice is not acting on it. `tillr watch ack` still means "I own this alert" ([Section 6.1](#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5)), and an alert the agent has seen but not acknowledged stays `open` on `tillr watch status` without being shown again by `wait`.

**Where the watcher reads its store and its rules ([D-TW36](#D-TW36)).** This paragraph records an operator's decision of 2026-11-21, given to the build as a retry reason after a build worker reported that the two launch examples below named neither `--store` nor `--rules`, both of which `wait` requires ([Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15)). The owner has not ruled on it and may overturn it. The decision is the operator's; the two sentences marked as the designer's reading are the designer's and may be overturned as well.

- **Both arguments are explicit, and both come from the repository the session is working in.** In either harness the watcher is started with `--store <repository root>/.tillr/state.db` and `--rules <repository root>/config/tillr/watch`. Nothing is read from a home directory or from an environment default. The designer's reading: the **repository root** is the top directory of the working tree the session runs in, written as an absolute path, so a session running in a linked worktree that holds no `.tillr/state.db` has a missing store and falls under the next rule.
- **A missing path defers attachment.** When the store file or the rules directory does not exist, the watcher is not started, and one line is printed that names the missing path. Attachment is tried again at the next session start, compaction or handoff, which are the moments step 1 of the procedure below already names. The designer's reading: "the harness" in the operator's words is the foreground agent following that procedure in Claude Code or in Pi, since the owner ruled that no new Pi extension is built and nothing here adds code to either harness; the agent tests both paths before starting the watcher, and the line it prints is `tillr watch: not started, missing <path>`.
- **An empty rules directory is valid.** When the directory exists and holds no rule file, the watcher is started, attaches, and waits with zero rules. It still shows fired agent watches once those are built.
- **No rule is added by the delivery build.** The rule catalog stays out, as [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) says. An operator request of 2026-11-21 has since asked for a separate build that creates the directory with ten rules ([Section 5.4](#54-the-first-rules-shipped-ten-conditions-seen-on-2026-11-21-d-tw39), [D-TW39](#D-TW39)). On 2026-11-21 `ls config/tillr` in this repository listed no `watch` directory, so until a build or the operator creates it, every session in this repository defers attachment under the rule above.
- **The re-arm command already carries both.** The command `wait` prints as its last line repeats the `--store` and `--rules` it was started with, each quoted as one shell word (`rearm` in `tools/tillr-watch/wait.go`, in the first build), so a re-arm needs no second lookup. `tillr watch seen` takes `--store` and no `--rules`.

**In Claude Code: exit and re-arm, inside the two-hour limit.** The agent starts the watcher with its Bash tool as a background command:

```text
command:            tillr watch wait --store <repository root>/.tillr/state.db --rules <repository root>/config/tillr/watch --timeout 110m
run_in_background:  true
timeout:            7200000
```

Claude Code stops a background command at the `timeout` it was started with, and re-invokes the agent when the command exits or is stopped. The limit is 7,200,000 ms (two hours) at most and 600,000 ms (ten minutes) when none is given, so the `timeout` argument must be passed; both figures were read from the Bash tool's own description in a Claude Code session on 2026-11-21. The cycle is:

1. A notice becomes pending. `wait` prints it and exits `0`. The exit is what wakes the agent, with the output in front of it.
2. The agent reads the notices and decides what to do. Nothing is done for it ([OQ-TW2](#OQ-TW2)).
3. The agent re-arms with the printed command, which carries `--seen-through`, as a background command again. Re-arming comes before any long piece of work, so that a firing during that work wakes the agent too.
4. If nothing fires for 110 minutes, `wait` exits `2` by itself, ten minutes inside the harness limit, and the agent re-arms. This is the restart the owner described, and it costs one short turn every 110 minutes.

Nothing depends on the watcher ending cleanly. If the harness stops it at two hours, or anything else ends it, no state is lost, because the watcher holds none: the next `wait` shows whatever is pending. A watch that fires while no watcher is attached (between an exit and the re-arm, or between two sessions) is shown the moment the next `wait` starts.

**In Pi: one watcher for the life of the session.** Pi's `background_task` tool (start, status, logs, stop and watch; [the background-work research](../research/pi-background-work.md)) sets no time limit on a task, so the agent starts the watcher once, with no `--timeout`:

```text
tillr watch wait --store <repository root>/.tillr/state.db --rules <repository root>/config/tillr/watch --follow
```

With `--follow` the process does not exit on a notice. It prints the same output, beginning with the `TILLR_WATCH_NOTICE` line, and keeps running. The agent puts an output watch for that marker on the task; the watch wakes the agent, which reads the notices, sets the output watch again, and then confirms with `tillr watch seen --store <repository root>/.tillr/state.db --through <seq>`, in that order. While anything is pending, `--follow` repeats the one-line marker (not the bundles) every 60 seconds, so a notice that arrives between a wake and the next output watch still wakes the agent. That is how the owner's Pi CI watcher uses the same tool. The delivery slice confirms against the installed `pi-background-tasks` that an output watch fires once and must be set again, before relying on it.

A `background_task` ends with its Pi session. The next session starts the same command again and is shown everything pending at once.

**What the foreground agent does, in either harness.** This is procedure, written into the skill a foreground operator loads, and it is three lines:

1. At session start, and after any compaction or handoff, start the watcher with the repository's own `--store` and `--rules` ([D-TW36](#D-TW36)). If either path is missing, print the one line naming it and try again at the next of those moments. Anything that fired while no session was open is shown immediately.
2. When woken, read the notices, re-arm, then act. Acknowledge an alert you take on; record with `--note` on `tillr watch ack` or `--reason` on `tillr watch resolve` whether a better path existed ([OQ-TW4](#OQ-TW4)).
3. Never end a turn of waiting without a watcher attached.

**When the procedure is not followed.** An agent that forgets to re-arm is the one failure the store cannot prevent, since only the harness can wake a session. It is made visible instead. `tillr watch status` prints, per owner, whether a watcher is attached (asked of the kernel, as the next paragraph states, and not read from `waiting_since`), when one last ran (`last_wait_at`) and how many notices are pending; Observatory shows the same three facts and learns attachment the same way. The timed check-in a foreground operator already runs calls `tillr watch status` first, which bounds the gap at one check-in interval.

**How `status` knows a watcher is attached ([D-TW26](#D-TW26)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. The worker's case: a `wait` sets `waiting_since` when it starts and clears it when it returns, but a watcher that is killed (Claude Code stopping it at its limit, a session ending, `SIGKILL`) never runs that cleanup, so the row it leaves is the same as the row of a healthy Pi watcher that has been idle for hours. No value in the row can tell the two apart. Attachment is a fact about a live process, so it is asked of the kernel, which releases a process's file locks however the process ends.

- **The attachment file.** Each owner handle has one file, `<store path>.watch-attached/<name>.lock`, where `<store path>` is the path given to `--store` and `<name>` is the lowercase hexadecimal SHA-256 of the handle's bytes. It is empty, holds no state, and is never deleted. It is not part of the store: nothing reads its contents or its times.
- **What `wait` does.** Before it writes `waiting_since`, `wait` creates the directory and the file when they are absent, opens the file, and takes a shared POSIX record lock on byte 0 (`fcntl` with `F_SETLK` and `F_RDLCK`). It keeps that descriptor open until the process ends and opens the file nowhere else in the process, because closing any descriptor of a file drops every record lock the process holds on it. If the file cannot be created or the lock cannot be taken, `wait` prints the error with the file's path and exits `1` without evaluating; it never runs unlocked.
- **What "attached" means.** An owner's watcher is **attached** when some live process holds that lock. `status` opens the file without creating it and asks, with `F_GETLK` and `F_WRLCK` on byte 0, whether a write lock would be refused. A refusal means attached. A missing file, or no refusal, means unattached. The question takes no lock, so two `status` commands at once do not affect each other or a `wait`. `status` still writes nothing.
- **Several watchers for one owner.** Each holds the shared lock, and the owner is attached for as long as any of them lives.
- **What the row means now.** `waiting_since` is the time the newest `wait` for that owner started, and is NULL after a clean exit. It is shown as the time of attachment only when the lock says a watcher is attached. A row with `waiting_since` set and no lock held is a watcher that ended without cleanup: `status` reports it as unattached, changes nothing, and the next `wait` overwrites the value. `last_wait_at` keeps its meaning; after an unclean exit it is the time that watcher started.
- **What `status` prints.** One line per row of `foreground_delivery`, in order of `owner_handle`, after the lines for rules. Times are RFC 3339 in UTC, and `-` stands for a NULL `last_wait_at`:

  ```text
  owner <owner_handle> watcher=attached since=<waiting_since> last_wait=<last_wait_at> pending=<n>
  owner <owner_handle> watcher=unattached last_wait=<last_wait_at> pending=<n>
  ```

  `pending` is the number of notices pending for that owner as [D-TW14](#D-TW14) defines the word, counted from the stored episodes and watches without evaluating. `--json` carries the same facts under the keys `owner`, `attached` (a Boolean), `since`, `last_wait` and `pending`.
- **An attached owner with no start time ([D-TW37](#D-TW37)).** This is the designer's decision of 2026-11-21, made to answer a build worker's definition obstacle on the second build; the owner has not ruled on it and may overturn it. The worker's case: two watchers run for one owner, the newer one returns and clears `waiting_since`, and the older one still holds the lock. The owner is attached and the row holds no start time. `status` reports exactly that and invents no time:

  ```text
  owner <owner_handle> watcher=attached since=- last_wait=<last_wait_at> pending=<n>
  ```

  - The line keeps the `since=` field, with `-` standing for a NULL `waiting_since`, as `-` already stands for a NULL `last_wait_at`.
  - In `--json`, `since` and `last_wait` are each an RFC 3339 string in UTC or JSON `null`, and both keys are always present. `since` is `null` when `waiting_since` is NULL and whenever `attached` is `false`, whatever the row holds. `last_wait` is `null` when `last_wait_at` is NULL.
  - `since` is therefore the start of the newest `wait` for that owner, when that `wait` has not returned. It is not the start of the oldest watcher still alive, which no row records. When the newest `wait` was killed and an older one lives, `since` is the killed one's start time.
  - `wait` is not changed: it writes and clears `waiting_since` as the first build does, and a watcher that returns clears the value only when it is still its own start time.
  - Two alternatives were set aside by the designer. Having a returning `wait` put back the start time of a surviving watcher needs one stored row per watcher process, and a row a killed watcher leaves behind would be the stale state [D-TW26](#D-TW26) removed. Writing each watcher's start time into the attachment file gives that file contents, when it holds none so that nothing has to trust them.
- **What it does not say.** The lock shows that a watcher process is alive, not that the agent will be woken by it: a stopped process still holds its lock, and a Pi agent that has not set its output watch again has a live watcher too. The 60-second repeat of `--follow` covers the second case. The lock is seen by every process on the same kernel that reaches the same file, which is what a jail and its host sharing the workspace are; that case is argued, not measured. The process ID that `F_GETLK` reports is not used, because a jail and its host number processes differently.
- **What the build proves.** A test starts a `wait` as a child process, sees `status` report it attached, ends it with `SIGKILL`, and sees `status` report it unattached with `waiting_since` still set in the row.

The mechanism was measured on 2026-11-21 on Linux 7.3.4 with a Go program using `syscall.FcntlFlock`: with two processes each holding the shared lock the question answered attached; after `kill -9` of one it still answered attached; after `kill -9` of the other it answered unattached; and before any holder, unattached. The same program passed `GOOS=darwin go vet`, so the calls exist on macOS, where they were not run.

Three alternatives were set aside, by the designer. A heartbeat column that `wait` rewrites on its 60-second timer makes every idle watcher write to the store, wakes every other watcher through `.tillr/state.db-wal`, and leaves `status` wrong for one staleness threshold after a kill. A process ID in the row cannot be checked from the other side of a jail boundary and can be reused by an unrelated process. Clearing the row from a signal handler does nothing for `SIGKILL`, which is how a harness ends a background command that does not stop when asked.

| Event | What happens | What is lost |
| :--- | :--- | :--- |
| Claude Code stops the watcher at its limit | The agent is re-invoked and re-arms | Nothing |
| The session ends, compacts or hands off | The next session's first `wait` shows everything pending | Nothing |
| A watch fires with no watcher attached | It stays pending and is shown by the next `wait` | Time, up to the next re-arm or check-in |
| The agent is woken but its turn dies before it reads the output | The seen cursor did not move; the next `wait` shows the notice again | Nothing |
| The agent confirms and re-arms twice | The second confirmation is a no-op | Nothing |
| `tillr watch daemon` is not running | `wait` evaluates the rules itself | Nothing |
| The store file or the rules directory does not exist | The watcher is not started and one line names the missing path; attachment is tried again at the next session start, compaction or handoff ([D-TW36](#D-TW36)) | Time, until the path exists and one of those moments comes |
| The agent never re-arms | `tillr watch status` and the timed check-in show an unattached watcher and the pending count | Time, up to one check-in interval |
| The watcher is killed and its cleanup never runs | Its lock is released by the kernel, so `tillr watch status` shows the owner unattached although `waiting_since` is still set ([D-TW26](#D-TW26)) | Nothing |

**Alternatives set aside, by the designer.**

- **Posting into the session instead of exiting.** A Claude Code background process can send a message into the session that started it through the session's messaging socket (`CLAUDE_CODE_MESSAGING_SOCKET`), and the owner's CI watcher has a mode that does. It is not used here: the process is still stopped at two hours, so the re-arm is needed anyway, and a second delivery path would then need its own failure handling. Exit and re-arm uses only the harness behavior described above.
- **A Pi extension that injects a turn** (the earlier `tillr-watch-bridge.ts` proposal). Ruled out by the owner on 2026-11-21.
- **Marking a notice seen when it is printed.** One command fewer, but a notice printed into a session that then dies is lost without trace.
- **A timer that polls `tillr watch status`.** It wakes the agent when nothing happened and lags when something did, which is the cost [Section 3.3](#33-relation-to-one-breakage-dedup-steering-timers-and-goal-monitors) set out to remove. The timed check-in remains only as the backstop above.

### 6.5 How other mechanisms subscribe to the same alerts ([D-TW9](#D-TW9))

Every episode state change and watch firing appends a typed `events` row (`watch.alert.opened`, `watch.alert.reopened`, `watch.alert.acknowledged`, `watch.alert.silenced`, `watch.alert.resolved`, `watch.fired`) and streams over `tillr watch subscribe`:

```bash
tillr watch subscribe --consumer observatory --kind all --since-seq 48210
```

Each line on stdout is a self-contained NDJSON envelope carrying `seq`, `ts`, `kind`, `alert_id` or `watch_id`, `rule_id`, `subject_key`, `severity`, `receiver`, `note` / `guidance`, and `bundle`:

- **Observatory (`tools/tillr-view/servews.go`):** Subscribes (or reads `alert_episodes` + `watch.alert.*` events already flowing through `.tillr/state.db` invalidation in [Observatory's WebSocket stream](../reference/observatory-design.md#websocket-invalidation-stream-servewsgo-and-socketts)) to surface active alerts, silence controls, and timeline bundles in the UI without polling.
- **`steering-timer` & supervisor hooks:** Can subscribe with `--severity high` or query `tillr watch status --json` during scheduled check-ins instead of running custom event-export scripts.
- **External webhooks, desktop notifications, or Slack/pager bridges:** Pipe `tillr watch subscribe --consumer desktop-notify --kind alert` into `jq` / `notify-send` / `curl` with lossless cursor resumption via `watch_consumers(consumer_id, last_seq)` across restarts.

### 6.6 The first build: event rules, episodes and `wait` ([D-TW15](#D-TW15))

This section is the designer's decision of 2026-11-21; the owner has not ruled on it and may overturn it. It fixes what the first build contains, so that a builder has a stated answer for each part of the document the first build does not reach. It follows a request filed on 2026-11-21 that names four things to build: the standalone binary ([OQ-TW1](#OQ-TW1)), the rule schema, the evaluator, and `tillr watch wait`.

**What the first build contains.**

| Part | Contents |
| :--- | :--- |
| The binary | `tools/tillr-watch`, reached as `tillr watch`, with the library in `tools/tillr/watch` ([D-TW1](#D-TW1)). It is added to `nouns` in `tools/tillr-cli/main.go` and to `MODULES` in `tools/tillr-roll/tillr-roll.py`, and to no other list: the first build has no pack launcher and no pack copy of `tools/tillr-watch` ([D-TW24](#D-TW24)). A roll to a revision that has no `tools/tillr-watch` removes the installed binary ([D-TW25](#D-TW25)) |
| Faces | `tillr watch eval --once`, `tillr watch wait`, `tillr watch seen`, and `tillr watch status` in the one-part form the paragraph on skipped events below states ([D-TW22](#D-TW22)). Each takes `--store <path>` (required). `eval`, `wait` and `status` take `--rules <dir>` (required), and read every `*.toml` file under that directory, at any depth |
| Tables | `alert_episodes` ([Section 6.1](#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5), with `condition_fingerprint` left empty as [D-TW20](#D-TW20) states) and `foreground_delivery` ([Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3)). No cursor table is needed, because counts are derived from the events inside each rule's `window` ([D-TW13](#D-TW13)). They are registered with `store.RegisterDDL` |
| Event kinds | `watch.alert.opened` and `watch.alert.resolved`, registered with `event.Register` |
| The loader | Reads the whole schema of [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5), checks each key as the key table there states ([D-TW16](#D-TW16)), and refuses a file as the list below states |
| The evaluator | Evaluates the rules the paragraph below calls evaluated, as [D-TW12](#D-TW12), [D-TW13](#D-TW13), [D-TW17](#D-TW17), [D-TW18](#D-TW18), [D-TW22](#D-TW22) and [D-TW23](#D-TW23) state |
| Episodes | Opening, updating in place and automatic resolution, including resolving one episode and opening the next in a single evaluation ([D-TW17](#D-TW17)). The other transitions of [Section 6.1](#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5) come with the commands that cause them |

**What the loader refuses.** One refusal names the file, the rule's `id` and the key, and the binary exits `1` without evaluating anything:

- a key named `remedy` anywhere in a rule ([OQ-TW2](#OQ-TW2));
- a `delivery.receiver`, or a member of `delivery.also_notify`, that begins `drain:` ([OQ-TW4](#OQ-TW4)), or that is anything other than `foreground` or `subscribers`;
- a key the key table of [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) does not name, a missing `id`, `scope`, `severity`, `reads.events`, `when.group_by`, `when.pattern`, `guidance.summary` or `delivery.receiver`, an empty `guidance.check` or `guidance.do`, or a value outside the set its key allows;
- an `id` that two rules share, or one longer than 128 bytes ([D-TW19](#D-TW19));
- a kind in `reads.events` that `event.Kinds()` does not list;
- a pattern's required key that is missing, a `threshold` below 1, a `window` that is not a duration (`"90s"`, `"15m"`, `"4h"`), or an expression that does not parse.

The third and sixth refusals do not reach the pattern's own keys in a `lag_behind` or `duration_without_event` rule: beyond `group_by`, `pattern` and `clear_when`, the keys of `[rule.when]` in such a rule are accepted unread, and none is required ([D-TW16](#D-TW16)).

`enabled` defaults to `true`, and a rule with `enabled = false` is loaded and not evaluated.

**Which rules are evaluated.** A rule is evaluated by the first build when all four of these hold: its `scope` is `core`, `workflow:*` or `charter:*` ([D-TW21](#D-TW21)); its `pattern` is `predicate_match`, `count_in_window`, `consecutive_count` or `alternating_pair`; it has no `reads.projections`, or an empty one; and every operand in its expressions is one the grammar table of [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) lists. Every other rule that loads (a rule scoped to a named workflow or charter, a `lag_behind` or `duration_without_event` rule, one that reads a projection, one whose expression uses `age()`, a projection operand, a bare name or any call other than `empty()`) is not evaluated, and the binary says so: `eval` and `wait` print one line per such rule on standard error when they start, naming the rule and the reason. Both example rules in [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) are in that set as written, because each reads projections.

**How the first build shows skipped events ([D-TW22](#D-TW22)).** That status shows each rule's count of skipped events is the operator's design answer of 2026-11-21. The command form and the output lines below are the designer's, written so the builder has them stated. The owner has ruled on neither and may overturn both.

- `tillr watch status --store <path> --rules <dir>` loads the rules exactly as `eval` does, with the same refusals and the same lines on standard error for rules it does not evaluate. It derives each evaluated rule's skipped count at the clock's time, as an evaluation would, and **writes nothing to the store**: it opens, updates and resolves no episode and appends no event.
- It prints one line on standard output for each evaluated rule, in order of `id`, and exits `0`: `rule <id> skipped=<n>`. A rule with `group_by = "global"` prints `skipped=0`. With no evaluated rule it prints nothing.
- `tillr watch eval --once` prints one line on standard error for each evaluated rule whose skipped count at that evaluation is above zero: `tillr watch: rule <id> skipped <n> events: group_by <name> missing or not a string`. A skipped event changes neither its exit code nor what it writes to the store.
- Everything else [Section 4.1](#41-new-utility-tillr-watch-vs-face-of-an-existing-utility-d-tw1-oq-tw1) and [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) say `status` shows (alert episodes, agent watches, whether a watcher is attached, `--json`) comes with the later build, which keeps these lines. The first build's `wait` takes no attachment lock; the build that makes `status` report attachment adds the lock to `wait` in the same change ([D-TW26](#D-TW26)).

**What `wait` prints in the first build.** The output of [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3), with alert notices only. The `TimelineBundle` of the first build holds the rule's `id`, the subject, the severity, the rendered `guidance`, and the counted events (`seq`, `ts`, `kind`, and the value of `field` where the pattern has one), newest first and cut to 8 KiB as [D-TW19](#D-TW19) below states; the item metadata, runs, refusals and landings of [Section 6.2](#62-how-the-receiver-gets-the-items-timeline-with-the-alert-d-tw6) are added later. The `ack:` line is left out until `tillr watch ack` exists. `wait` sleeps on `.tillr/state.db-wal` and the 60-second timer; a build that also watches `.tillr/watch.wakeup` is correct, and nothing touches that file yet.

**What the 8 KiB covers, and what is cut when a bundle does not fit ([D-TW19](#D-TW19)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. The worker's case was a rule that loads under [D-TW16](#D-TW16) with a `guidance.summary` of 8,193 bytes: when it fires, leaving out every counted event still does not bring the bundle under 8 KiB, and this document named no other part to cut. The budget covers the whole bundle, guidance included, and the bundle is cut in a fixed order until it fits. Events go first, because a reader can fetch them again from the store by `seq`, while the guidance is what the receiver acts on.

- The **budget** is 8,192 bytes, measured on the text stored in `alert_episodes.bundle_json`: one JSON object, in UTF-8, with no whitespace between its parts. Whatever escaping the encoder applies is counted, because the stored bytes are what is measured. No stored bundle is longer.
- The first build's bundle is an object with these keys, always all present: `rule_id`, `subject`, `severity`, `guidance` (an object holding `summary`, a string, and `check` and `do`, lists of strings, all rendered as [D-TW16](#D-TW16) states), `events` (a list, newest first, each member holding `seq`, `ts`, `kind`, and `value` where the pattern has a `field`), and `omitted` (an object holding the integers `events`, `check` and `do` and the booleans `summary_cut` and `subject_cut`).
- The bundle is built whole. While it is over the budget, the builder applies the first of these steps that can still remove something, and measures again:

| Step | What is removed | What `omitted` records |
| :--- | :--- | :--- |
| 1 | The oldest member of `events`, down to an empty list | `events` goes up by one |
| 2 | The last line of `guidance.do`, down to an empty list | `do` goes up by one |
| 3 | The last line of `guidance.check`, down to an empty list | `check` goes up by one |
| 4 | The end of `guidance.summary`, leaving the longest prefix that ends on a character boundary and with which the bundle fits, which can be the empty string | `summary_cut` is `true` |
| 5 | The end of the bundle's `subject`, by the same rule | `subject_cut` is `true` |

- A step removes whole things, except steps 4 and 5: an event or a guidance line is kept whole or left out, never shortened. Step 1 is finished before step 2 starts, and so on down the table, so a bundle that has lost a `do` line holds no events.
- The steps always end with a bundle that fits. After step 5 the bundle holds the rule's `id`, a severity, empty strings and lists, and the `omitted` object, and the loader refuses an `id` longer than 128 bytes for that reason. That refusal is the one change this decision makes to [D-TW16](#D-TW16); a rule whose guidance is long still loads, and the worker's rule opens its episode with no events, its `check` and `do` lines as far as they fit after the summary is cut, and `summary_cut` set.
- Only the bundle's copy is cut. The episode's `subject_key` column holds the whole subject, and the rule file holds the whole guidance.
- The bundle is built when the episode opens ([Section 6.2](#62-how-the-receiver-gets-the-items-timeline-with-the-alert-d-tw6)). An evaluation that updates the episode in place does not rebuild it.
- `wait` prints a notice's summary, `check` and `do` lines from the stored bundle, so it prints what was kept. For a notice whose bundle has any `omitted` value other than zero and `false`, it prints one more line under the `do` lines, naming all five values and the rule that holds the full text: `  omitted: events=3 check=0 do=2 summary_cut=false subject_cut=false (full guidance: rule <id>)`.

Three alternatives were set aside by the designer. Refusing at load a rule whose guidance is over some size does not bound the rendered text, because `{{subject}}` is a value read from an event. Refusing to open the episode would drop an alert because its text is long. Letting the budget cover the events only would leave the bundle with no bound at all, and the bound is why the receiver can take a bundle into its context without checking its size first.

**What the first build stores in `condition_fingerprint` ([D-TW20](#D-TW20)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. The column is declared `NOT NULL` in [Section 6.1](#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5) as a digest of the facts that caused the episode, and this document does not say which facts those are for any pattern, or how they are written out before hashing. The first build computes no fingerprint: it stores the empty string in `condition_fingerprint` for every episode it opens, and never changes it.

- The empty string means that no fingerprint was computed. It is not a digest and is not compared with anything.
- Nothing in the first build reads the column. Its one reader is `tillr watch silence --until-change`, which, with the `silence_on_fingerprint` column and the `watch.alert.reopened` event, belongs to a later build ([the list below](#66-the-first-build-event-rules-episodes-and-wait-d-tw15)).
- The build that adds `silence --until-change` fixes which facts are hashed, how a rule names them, and their encoding, and says what that command does with an episode whose fingerprint is empty. The cause facts differ by rule (a failing step and its error text for one, a path outside the fence for another), and the rule schema has no key that names them yet.
- The column and its `NOT NULL` stay as declared, so the later build adds no column.

Two alternatives were set aside by the designer. Hashing the rule's `id`, the subject and the matched pair now would give every episode of one rule and subject the same digest, which a later reader would take for a real fingerprint although it can never change. Fixing the cause facts for the four event patterns now would settle a schema that nothing reads or tests.

**What a rule's `scope` does in the first build ([D-TW21](#D-TW21)).** This is the designer's decision of 2026-11-21, made to answer a build worker's question; the owner has not ruled on it and may overturn it. The worker's case was a `predicate_match` rule scoped `workflow:planned-build@v1` that reads `drain.refusal` and groups by `item_id`. The refusal event written by `recordItemRefusal` in [`tools/tillr-drain/drain/events.go`](../../tools/tillr-drain/drain/events.go) carries the step, the item, the reason and the detail, and nothing that says which workflow the item is on, so the events alone cannot tell whether that rule applies.

- The first build **never filters events by scope**. It reads no workflow membership and no active charter, at event time or at evaluation time. Both are facts held outside the events a rule reads, which makes them projections, and the first build leaves every projection out.
- A rule scoped `core`, `workflow:*` or `charter:*` applies to every item whatever its workflow and whatever the store's charter. Its scope needs no fact to decide, and it is evaluated when the other three conditions above hold.
- A rule scoped to a **named** workflow or charter (`workflow:planned-build@v1`, `charter:git-landing`) loads and is **not evaluated**. It is named on standard error with the other unevaluated rules, and the reason says that a named scope is not evaluated by this build. It opens no episode, and `wait` prints nothing for it.
- The scope is not treated as a label that the evaluator ignores. That reading would make a rule written for one workflow fire for items on every other workflow, and a notice the receiver cannot trust is worse than a rule that says plainly it did not run.
- The build that adds projections owns the filter: which fact names an item's workflow and the store's charter, whether membership is read as of the event or as of the evaluation, and what happens when the fact is absent. Nothing in the first build may depend on an answer to those.

One example. A rules directory holds two `predicate_match` rules that read `drain.refusal`, one scoped `workflow:*` and one scoped `workflow:planned-build@v1`. `eval --once` prints one line on standard error naming the second rule, evaluates the first against every refusal in its `window`, and opens episodes for the first only.

**How the first build is delivered ([D-TW24](#D-TW24)).** This is the designer's decision of 2026-11-21, made to answer a build worker's obstacle; the owner has not ruled on it and may overturn it. The pack is `yolo-packs/tillr`, the yolo pack that puts the tillr programs on `PATH` in a workspace that has no `tools/` directory; a launcher is the pack's `programs/<name>/run` script, declared by a `program` entry in `yolo-packs/tillr/pack.json`. The worker's build added a `tillr-watch` launcher and `program` entry, and `python3 tests/pack-sync.test.py` then failed in `case_launchers`, because that suite's `MODULES` list, from which it derives the launchers it expects, does not name `tillr-watch`. The suite is outside the paths the build may write, and this document had not said whether the pack delivers the binary.

The first build is delivered from the source tree only, by `tillr-roll`. It is not delivered by the pack:

- The build adds `"watch"` to `nouns` in `tools/tillr-cli/main.go` and to `MODULES` in `tools/tillr-roll/tillr-roll.py`, and updates the wording of both files and the Go tests of `tools/tillr-cli`. `tillr-roll` then builds `tillr-watch` beside the other binaries, which is how `tillr watch` is reached in this repository.
- The build adds no `program` entry to `yolo-packs/tillr/pack.json` and no `yolo-packs/tillr/programs/tillr-watch/` directory.
- The build does not add `tillr-watch` to any list of mirrored modules: `SHIPPED` in `tools/pack-mirror-links.py` and in `tools/tillr-drain/drain/pack-mirror.py`, `modules` in `tools/tillr-drain/drain/pack_mirror.go`, `MIRRORED_MODULES` in `tools/pack-docs.py`, and `MODULES` in `tests/pack-sync.test.py` and `tests/tillr-docs.test.py` all stay as they are. So `just pack-sync` writes no `yolo-packs/tillr/files/local/share/tillr/tillr-watch/`, and the build commits none. It also leaves the module list of the `tillr-build` recipe in `.justfile` alone.
- The library `tools/tillr/watch` is part of the `tools/tillr` module, which is already mirrored, so `just pack-sync` copies it into the pack with the rest of that module, and the build commits that copy. The changed `tools/tillr-cli` and `tools/tillr-roll` files are mirrored the same way.
- In a workspace served by the pack, `tillr watch` therefore ends with the dispatcher's existing refusal that `tillr-watch` is not on `PATH`. That is the stated behavior of the first build, not a defect in it.
- The build writes no test under `tests/`. Its tests are the Go tests of `tools/tillr/watch`, `tools/tillr-watch` and `tools/tillr-cli`, and the Go tests of `tools/tillr-drain` that drive the roller ([D-TW25](#D-TW25)).

This shape was measured on 2026-11-21 against the worker's preserved tree with the pack launcher, the `program` entry, the two list additions it had made (in `pack-mirror.py` and `pack_mirror.go`) and the pack copy of `tools/tillr-watch` taken out and `just pack-sync` run again: `python3 tests/pack-sync.test.py` passed its tree, bytes, exec-bit and launcher cases ("11 modules", "8 entries"), and `python3 tests/tillr-docs.test.py` and `python3 tests/tillr-roll.test.py` passed. With the launcher in, the same `case_launchers` refusal the worker reported was reproduced. Pack delivery (the launcher, the `program` entry, the pack copy and every list above) is one later change, made by an item whose allowed paths include `tests/pack-sync.test.py` and `tests/tillr-docs.test.py`.

**What a roll does with the installed binary ([D-TW25](#D-TW25)).** Four things here are from the operator's request of 2026-11-21, given to the build as a retry reason: a successful roll to a revision that has no `tools/tillr-watch` removes the installed `tillr-watch`, on a deferred installation as well; a failed roll removes nothing; both directions are tested; and `watch` stays in `MODULES` with no test added under `tests/`. The mechanism below (which program does the removing, the file it keeps and the signal it reads) is the designer's decision of 2026-11-21, made to answer a build worker's obstacle. The owner has ruled on neither and may overturn both.

The worker's obstacle was that [D-TW24](#D-TW24) named nobody to remove the binary after a deferred installation. Three terms, as this paragraph uses them:

- The **install directory** is the directory `tillr-roll` installs the binaries into (`live` in `tools/tillr-roll/tillr-roll.py`). The **installed binary** is the one file `tillr-watch` in it. The roller removes no other copy of `tillr-watch`, anywhere.
- An **immediate installation** is a roll in which the roller itself moves every built binary into the install directory.
- A **deferred installation** is a roll whose staged `tillr-drain loop --check` prints `tillr-drain: schema migration required`. The roller then installs only `tillr-drain` and leaves the other binaries in a staging directory, `.tillr-schema-roll-<digest>` inside the install directory. The staged revision's own drain, once it is the running loop, migrates the store, moves the staged binaries into the install directory, writes the rolled commit to `tillr-roll.commit` beside the install directory, and deletes the staging directory, in that order (`activateSchemaRoll` in `tools/tillr-drain/drain/schema_roll.go`). This document calls that drain the **activator**.

On a deferred installation of a revision that predates `tillr-watch`, the activator is that older revision's executable. Read at commit `9d0013a7db`, it moves a fixed list of seven programs and knows nothing of `tillr-watch`, and no change made now can alter it. With `--no-request` the roller returns after staging, before the activator has run. So neither the activator nor that one roller invocation can do the removal.

**The roller does every removal.** `tools/tillr-roll/tillr-roll.py` is run from the repository's checkout, not from the revision being rolled, so it is the one program that is still the current one after a roll to an older revision. It works as follows.

1. **Building.** The roller builds and smoke-tests `tillr-watch` only when the checked-out revision has a directory `tools/tillr-watch`. Every other entry of `MODULES` stays required. A revision **has the binary** when that directory exists, and **lacks** it otherwise.
2. **Immediate installation.** After every binary has been moved into the install directory, and before `tillr-roll.commit` is written, the roller removes the installed binary when the revision lacks it. A file that is already absent is not an error. It then deletes the marker of step 3, if there is one.
3. **Deferred installation.** After the staging step has succeeded, the roller writes a **marker** when the revision lacks the binary, and deletes any marker when the revision has it. The marker is the file `tillr-watch.retire`, beside `tillr-roll.commit`. It holds one JSON object with two string members: `commit`, the full commit being rolled, and `stage`, the name of the staging directory (`.tillr-schema-roll-<digest>`). The roller removes nothing at this step.
4. **Settling the marker.** The roller settles a marker at two moments: at the start of every roll, as its first act after it holds the roll lock; and, in a roll that requested restarts, after the restart answers are in. To settle, it reads the marker and then:
   - when `tillr-roll.commit` holds the marker's `commit` and the directory named by `stage` no longer exists in the install directory, the deferred roll succeeded: the roller removes the installed binary (absent is not an error), then deletes the marker;
   - when the directory named by `stage` still exists, the deferred roll has not finished or has failed: the roller leaves the binary and the marker as they are;
   - in any other state, the roller deletes the marker and removes nothing.
5. **The activator of a revision that has the binary.** The build adds `tillr-watch` to the list of programs `activateSchemaRoll` moves, so a deferred installation of such a revision installs it. That loop already passes over a program that is not in the staging directory.

The success signal is the pair in step 4 because the activator writes `tillr-roll.commit` only after the migration and the moves have succeeded, and deletes the staging directory last. Before the activator finishes, the staging directory exists; if the migration fails, it stays. Naming the staging directory in the marker keeps an older `tillr-roll.commit` that happens to hold the same commit from being read as success.

What follows from those steps:

- **A failed roll removes nothing.** A roll that fails before installation or staging reaches no step that removes. A deferred roll whose migration fails leaves its staging directory, so the marker is never settled as a success. The next roll that reaches installation or staging replaces the marker with its own state (steps 2 and 3).
- **A drain restart is acknowledged only after the activator has finished** (`Run` in `tools/tillr-drain/drain/loop.go` calls `activateSchemaRoll` before it adopts the restart). So a deferred roll that requested restarts, and was answered `restarted`, removes the binary before it returns.
- **With `--no-request` on a deferred installation, the installed binary stays until the next roll starts.** The roller has returned before the activator runs, so the removal is made by step 4 at the start of the next `tillr-roll` invocation, whatever that roll then does. Until then `tillr watch` still runs the newer binary. This delay is the designer's choice: the alternative, refusing a deferred roll to a revision that lacks the binary, would stop an automatic roll after a landing, which runs with `--no-request`.
- The roller prints no new line for any of this, and a roll's exit status is unchanged by it.

The build's tests for this are Go tests in `tools/tillr-drain` that run the real roller with its build and smoke steps replaced, as `TestAfterMergeLandingRollInstallsWithoutRepeatingProof` does in `tools/tillr-drain/drain/post_landing_roll_test.go`. No test is added under `tests/`. They cover, with the installed binary present beforehand unless stated:

| Case | Expected |
|---|---|
| Immediate installation of a revision that lacks the binary (new to old) | The installed binary is gone; the other binaries are installed |
| Immediate installation of a revision that has the binary, with none installed beforehand (old to new) | `tillr-watch` is installed |
| A roll to a revision that lacks the binary fails at a build step | The installed binary is still there |
| Deferred installation of a revision that lacks the binary, restarts requested and the drain answers `restarted` | The installed binary is gone and no marker remains |
| Deferred installation of a revision that lacks the binary, with `--no-request` | When the roller returns, the installed binary and the marker are both there. After `tillr-roll.commit` holds the commit and the staging directory is gone, the next roll removes the binary and the marker |
| The same, but the staging directory is still there when the next roll starts | The next roll's settling removes nothing |
| Deferred installation of a revision that has the binary (old to new) | The activator moves `tillr-watch` into the install directory |

The build also brings [the restart reference](../reference/tillr-restart.md) into line with steps 1 to 5, in its sections on the roller and on store schema upgrades.

**What the first build leaves out,** each for the later build that owns it:

- delivery by the pack: the `tillr-watch` launcher, its `program` entry, the pack copy of `tools/tillr-watch`, and the module lists that go with them ([D-TW24](#D-TW24));
- the `lag_behind` and `duration_without_event` patterns, every projection, and `age()`;
- every part of `tillr watch status` other than the skipped-event count ([D-TW22](#D-TW22)), and `daemon`, `ack`, `silence`, `resolve`, `rule` and `subscribe`, the `watch_rule_overrides` and `watch_consumers` tables, the event kinds for acknowledging, silencing and reopening, and any computed `condition_fingerprint` ([D-TW20](#D-TW20));
- agent watches (`tillr watch add`, `list`, `edit`, `drop`, the `agent_watches` table and `watch.fired`), `wait --follow`, and the operator procedure of [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3);
- filtering by a named workflow or charter scope ([D-TW21](#D-TW21)), which is how the bindings of [Section 7.1](#71-running-the-same-rules-against-a-non-git-workflow-recorded-verdict-autoqa-custom-loops) come about;
- `watch` blocks in workflow files, `observability` blocks in charters, and `tillr watch lint`;
- the rule catalog of [Section 5.2](#52-the-16-starting-rules-and-the-2026-11-20-cases-they-catch). The first build ships no rule file under `config/tillr/watch/`; its tests carry their own rules. Each catalog rule is written against the payload its events really carry, in the build that can evaluate it. Amended 2026-11-21 by [D-TW39](#D-TW39): ten rules are delivered ahead of the catalog, by a build of their own ([Section 5.4](#54-the-first-rules-shipped-ten-conditions-seen-on-2026-11-21-d-tw39)).

Nothing in the first build acts on an alert ([OQ-TW2](#OQ-TW2), [OQ-TW4](#OQ-TW4)): it writes `watch.*` events and its own two tables, and prints.

### 6.7 The second build: the remaining patterns, projections and the alert commands ([D-TW27](#D-TW27))

This section, and [D-TW27](#D-TW27) through [D-TW35](#D-TW35), [D-TW37](#D-TW37) and [D-TW38](#D-TW38), are the designer's decisions of 2026-11-21; the owner has not ruled on them and may overturn any of them. They follow an operator request filed on 2026-11-21 that names the second build: the remaining rule patterns, the projections rules can read besides events, and the rest of `status`, `daemon`, `ack`, `silence`, `resolve` and `rule`. A build worker stopped on that request with three gaps in this document, and each paragraph below that closes one says so: the keys of `lag_behind` and `duration_without_event` ([D-TW16](#D-TW16)), what a named scope reads and when ([D-TW21](#D-TW21)), and what a condition fingerprint is made of ([D-TW20](#D-TW20)). A second worker stopped with two more: what `status` reports for an attached owner whose row holds no start time ([D-TW37](#D-TW37), in [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3)), and what an evaluation does when a rule stored while it runs needs a Git fact it has not read ([D-TW38](#D-TW38), below). Where this section and the text on the first build disagree about the second build, this section governs.

Two words are used throughout:

- A **projection** is a fact the evaluator reads from somewhere other than the events a rule lists: a row of a store table, the store's charter, or a Git ref.
- A **level pattern** is a pattern whose condition is read from projections alone and counts no event. `lag_behind` is the only one.

**What the second build contains.**

| Part | Contents |
| :--- | :--- |
| Patterns | `lag_behind` ([D-TW28](#D-TW28)) and `duration_without_event` ([D-TW29](#D-TW29)), with the loader checking their keys |
| Projections | `items`, `charter` and `git_refs`, the operands that read them, and `age()` ([D-TW30](#D-TW30)) |
| Scopes | Rules scoped to a named charter or a named workflow are evaluated ([D-TW31](#D-TW31)) |
| Fingerprint | The `fingerprint` key and a computed `condition_fingerprint` ([D-TW32](#D-TW32)) |
| Faces | `tillr watch ack`, `silence` and `resolve` ([D-TW33](#D-TW33)); the rest of `status`, and `daemon` ([D-TW34](#D-TW34)); `tillr watch rule list`, `show`, `add`, `tune`, `reset` and `export` ([D-TW35](#D-TW35)) |
| Tables | `watch_rule_overrides` ([D-TW35](#D-TW35)). No column is added to `alert_episodes` or `foreground_delivery` |
| Event kinds | `watch.alert.acknowledged`, `watch.alert.silenced`, `watch.alert.reopened`, `watch.rule.set` and `watch.rule.reset`, registered with `event.Register` |
| `wait` | Takes the attachment lock of [D-TW26](#D-TW26), prints the `ack:` line of [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3), and evaluates with everything in this table |

- The `ack:` line is `  ack: tillr watch ack <alert_id> --by <owner> --store <path>`, where `<owner>` is the handle `wait` runs for and `<path>` is the `--store` argument as `wait` was given it.
- `eval`, `wait`, `status` and `daemon` take one new option, `--root <dir>`: the Git repository that `git_refs` is read from. It has no default. When the rules to be evaluated include an enabled rule that lists a `git_refs` projection and `--root` was not given, the command prints a refusal naming the rule and the option and exits `1` without evaluating.
- The `TimelineBundle` keeps the first build's keys and its cutting order ([D-TW19](#D-TW19)). The item metadata, runs, refusals and landings of [Section 6.2](#62-how-the-receiver-gets-the-items-timeline-with-the-alert-d-tw6) are still not added.
- Delivery is unchanged: `tillr-roll` only, with no pack launcher ([D-TW24](#D-TW24)). The new files of `tools/tillr/watch` are mirrored by `just pack-sync` with the rest of that module.
- The second build ships no rule file under `config/tillr/watch/`. Its tests carry their own rules, as the first build's do.

**Which rules the second build evaluates.** This replaces the four conditions of [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) for the second build. A rule that loads is evaluated when all three of these hold:

1. every entry of its `reads.projections` is `items`, `charter` or a `git_refs:` entry ([D-TW30](#D-TW30));
2. every operand in its expressions is one the grammar table of [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) lists, or `age(x)`, or one of the projection operands [D-TW30](#D-TW30) lists;
3. it is in scope at that evaluation ([D-TW31](#D-TW31)), and none of its projections is unreadable ([D-TW30](#D-TW30)).

A rule that fails the first or second condition is named on standard error when the command starts, as in the first build, with the reason. A bare name (`seat`, `pair`), a call other than `empty()`, `age()` and `git.ref()`, and a projection entry outside the three are the remaining reasons. The third condition is judged at each evaluation, and [D-TW30](#D-TW30) and [D-TW31](#D-TW31) say what is printed.

**The order of one evaluation.** An evaluation does these in order, and a rule sees the same projection values for every event it reads:

1. It reads the rules stored in the store ([D-TW35](#D-TW35)) with a plain read that holds no transaction open afterwards, and forms the effective rules. It then reads every Git fact those rules need from `--root`, before it opens the write transaction, so no `git` process runs while the store's write lock is held.
2. It opens one write transaction. Inside it, it reads the stored rules again, the store's charter, and for each rule the `items` and `workflow_selections` rows it needs. The effective rules formed from this second read are the ones the evaluation uses.
3. It derives every rule's condition, then reconciles episodes, as the first build does.

**A rule stored between the two reads ([D-TW38](#D-TW38)).** This is the designer's decision of 2026-11-21, made to answer a build worker's definition obstacle; the owner has not ruled on it and may overturn it. The worker's case: a `rule tune` or `rule add` commits after step 1 read Git and before step 2 opens its transaction, and the rule it stores names a ref step 1 did not read. The evaluation starts again:

- The **Git facts** a set of rules needs are the commit of each ref that an enabled rule to be evaluated lists in a `git_refs:` entry, and the lag and oldest time ([D-TW28](#D-TW28)) of each `leader` and `follower` pair of an enabled `lag_behind` rule to be evaluated.
- In step 2, when the effective rules need a Git fact that step 1 did not read, the evaluation rolls its transaction back with nothing written and starts again at step 1. No rule is evaluated, and no episode is opened, updated or resolved, by the pass that was rolled back.
- When step 1 read every fact the effective rules need, the evaluation goes on with the values step 1 read, even when the stored rules differ between the two reads in other ways (a threshold, a receiver, a rule that was reset). A fact step 1 read that no rule now needs is not used.
- A fact that step 1 found unreadable counts as read. Its rule is left unevaluated as [D-TW30](#D-TW30) states, and that does not start the evaluation again.
- There is no limit on the number of starts and no wait between them. Each new start needs another stored rule, naming another ref, to be committed inside the gap between one evaluation's two reads, so the evaluation ends as soon as the rules stop changing under it.
- The two refusals are made on whichever read meets them, in step 1 or step 2: a stored rule the loader refuses ([D-TW35](#D-TW35)), and an enabled rule that lists a `git_refs` projection when `--root` was not given. Both exit `1` as already stated, for a running `wait` or `daemon` too.
- "As of the evaluation" ([D-TW30](#D-TW30)) means, for a Git fact, as of step 1 of the pass that was not rolled back. A ref that moves after step 1 is seen at the next evaluation.
- `status` opens no write transaction and reads the stored rules once, before it reads Git, so this case does not arise for it.
- The build proves it with a test that stores a rule naming a second ref after step 1's Git read and before step 2's transaction, and sees that one `eval --once` evaluates the stored rule with that ref's commit.

Two alternatives were set aside by the designer. Leaving the new rule unevaluated until the next evaluation gives `eval --once` no next evaluation, so the command would exit `0` having silently not evaluated a rule that was effective when it wrote. Putting the stored rule off, and evaluating the rule it replaced, would evaluate a rule that is no longer effective. Reading Git inside the transaction is what step 1 exists to prevent.

#### The `lag_behind` pattern ([D-TW28](#D-TW28))

This closes the first half of the worker's first gap. `lag_behind` compares two Git refs. A leader that is not a Git ref (a file that holds a commit, such as `tillr-roll.commit`) is not covered, and belongs to the build that writes the catalog rule which needs it.

| Key in `[rule.when]` | Required | Value the loader accepts |
| :--- | :--- | :--- |
| `group_by` | Yes | `global`, and nothing else |
| `pattern` | Yes | `lag_behind` |
| `leader`, `follower` | Yes | Two different strings, each a full ref name that begins `refs/`, holds no space or comma and is at most 255 bytes long. Both must be listed in a `git_refs:` entry of the rule's `reads.projections` |
| `threshold_commits` | One of the two thresholds | An integer, 1 or more |
| `threshold_duration` | One of the two thresholds | A duration as `window` is written (`"15m"`), longer than zero |
| `mode` | No | `any` or `all`. Allowed only when both thresholds are given; `any` when left out |

- Every other key in `[rule.when]` is refused, including `clear_when`, `match`, `reset`, `window`, `threshold` and `fingerprint`. The pattern reads no event, so nothing could be judged against one.
- In a `lag_behind` rule `reads.events` may be left out or empty. When it is given, each kind is still checked against `event.Kinds()`, and the events are not read. This is the one change to the `reads.events` row of the key table in [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5).

Three facts are read at each evaluation:

- The **lag** is the number of commits reachable from the commit `leader` points to and not from the commit `follower` points to (what `git rev-list --count <follower>..<leader>` prints). It is used as counted even when the follower is not an ancestor of the leader.
- The **oldest time** is the least committer time among those commits.
- The **age** is the time of evaluation less the oldest time, and zero when that is negative or the lag is zero.

The rule fires as this table states. With a lag of zero it never fires.

| Thresholds given | Fires when |
| :--- | :--- |
| `threshold_commits` only | The lag is `threshold_commits` or more |
| `threshold_duration` only | The lag is 1 or more and the age is `threshold_duration` or more |
| Both, `mode = "any"` | Either row above holds |
| Both, `mode = "all"` | Both rows above hold |

- The one subject is `global`. A new episode's `opened_seq` and `last_observed_seq` are the greatest `events.seq` in the store when the evaluation started (zero in a store with no event), and its `observation_count` is 1.
- An unresolved episode whose rule still fires is left exactly as it is: nothing is written, and no count goes up. It is resolved, with `resolved_reason = "condition-cleared"`, at the first evaluation where the rule does not fire. There is no clearing event for this pattern, so of the table in [D-TW17](#D-TW17) only the two rows without one apply.
- In the guidance text, `{{count}}` is the lag, `{{elapsed}}` is the age (whole minutes, or whole seconds under one minute), `{{leader}}` and `{{follower}}` are the two ref names, and `{{leader_short}}` and `{{follower_short}}` are the first 12 characters of each commit's object name. `{{subject}}` is `global`.
- The bundle's `events` list is empty, and its guidance carries the values above.
- The build may use any `git` commands that give these three facts. Its tests build a repository whose commits have committer times the test sets, and set the clock.

One example, with the example rule of [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5). `refs/heads/main` is two commits ahead of `refs/tillr/green`, and the older of the two was committed 16 minutes before the evaluation. The lag is 2, which is under `threshold_commits = 3`; the age is 16 minutes, which is over `threshold_duration = "15m"`; `mode` is `any`, so the rule fires and one episode opens for `global`. When `refs/tillr/green` reaches `refs/heads/main`, the lag is zero and the next evaluation resolves the episode.

#### The `duration_without_event` pattern ([D-TW29](#D-TW29))

This closes the second half of the worker's first gap. The pattern reports that something started and the event that should have followed has not arrived for a stated time.

| Key in `[rule.when]` | Required | Value the loader accepts |
| :--- | :--- | :--- |
| `group_by` | Yes | As for every event pattern ([D-TW13](#D-TW13), [D-TW22](#D-TW22)) |
| `pattern` | Yes | `duration_without_event` |
| `after` | Yes | An expression |
| `match` | No | An expression |
| `threshold_duration` | Yes | A duration, longer than zero |
| `window` | Yes | A duration longer than `threshold_duration` |
| `clear_when` | No | An expression |
| `fingerprint` | No | As [D-TW32](#D-TW32) states |

Every other key in `[rule.when]` is refused. Three terms are used:

- A **start event** is a read event for the subject on which `after` holds and `clear_when` does not.
- An **end event** is a read event for the subject on which `clear_when` holds, or on which `match` holds and `after` does not. With `match` left out, only `clear_when` makes an end event.
- The subject's **waiting event** is the newest of its start and end events inside `window`, when that newest one is a start event. A subject whose newest such event is an end event, or that has neither inside `window`, has no waiting event.

The rule fires for a subject when it has a waiting event and the time of evaluation less that event's `ts` is `threshold_duration` or more.

- A later start event replaces an earlier one, so the time is always measured from the newest. That makes the one-kind form work: with `after = 'event.kind == "drain.pass"'` (an illustrative kind) and no `match`, the rule fires when the newest `drain.pass` is older than `threshold_duration`.
- The waiting event is the one counted event. `{{count}}` is 1, `{{elapsed}}` is the time of evaluation less the waiting event's `ts`, and the bundle's `events` list holds that one event. A new episode's `opened_seq` and `last_observed_seq` are its `seq`.
- A **clearing event**, for [D-TW17](#D-TW17), is any start event or end event for the subject. A start event later than an unresolved episode's `opened_seq` therefore resolves that episode, and when the new wait has itself lasted `threshold_duration` by that evaluation, a new episode opens in the same evaluation, as the last row of the table in [D-TW17](#D-TW17) states.
- The window rule of [D-TW23](#D-TW23) holds here too, and it sets two limits that are part of the design. A wait that has lasted longer than `window` stops firing, and its episode is resolved, because its start event is no longer read. A subject that has never had a start event cannot fire. A rule that must report a silence of any length needs a `window` at least that long.
- Skipped events are counted as [D-TW22](#D-TW22) states.
- Nothing wakes an evaluator when a time passes with no event, so such a rule fires at the first evaluation after the threshold is crossed. `wait` and `daemon` evaluate at least every 60 seconds ([Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3)).

One example, with illustrative payload names. A rule reads `run.allocate` and `run.record`, groups by `run_id`, and has `after = 'event.kind == "run.allocate"'`, `match = 'event.kind == "run.record"'`, `threshold_duration = "30m"` and `window = "6h"`. A run allocated 31 minutes before the evaluation with no `run.record` since has a waiting event, and the rule fires for it. When its `run.record` arrives, that is an end event later than `opened_seq`, and the next evaluation resolves the episode.

#### Projections, their operands and `age()` ([D-TW30](#D-TW30))

This closes the part of the worker's second gap that is about where a projection's value comes from, when it is read and what a missing one means. The second build reads three projections, and a rule must list each one it uses in `reads.projections`.

| Entry in `reads.projections` | What is read | Operands it allows | Their values |
| :--- | :--- | :--- | :--- |
| `items` | The row of the `items` table whose `item_id` equals the rule's subject | `item.state`, `item.seat`, `item.waiting_on` | The column's text |
| `charter` | The store's charter, as `definition.ForReader` returns it; a store with no recorded charter gives the built-in `git-landing` | `charter.name`, `charter.ready`, `charter.blocked`, `charter.closed`; `charter.states`, `charter.finished` | A string for the first four; a list of strings for the last two |
| `git_refs:<ref>[,<ref>...]` | The commit each named ref points to in the repository given by `--root` | `git.ref("<ref>")` | The commit's full object name, as a string |

**What the loader checks.**

- A `git_refs:` entry is the text `git_refs:` followed by one or more ref names separated by commas, each as [D-TW28](#D-TW28) accepts a ref name. Anything else beginning `git_refs:` is refused.
- An operand is refused when the rule does not list its projection, when it names a column or member the table above does not list (`item.title`, `charter.hooks`), or, for `git.ref`, when its argument is not one string that a `git_refs:` entry of the rule lists.
- An `item.` operand is refused in a rule with `group_by = "global"`, which has no item for a subject.
- A listed projection that no operand uses is allowed.
- An entry outside the three (`work_nodes`, `heavy_lock_state`) is not refused. The rule loads and is not evaluated, and the reason on standard error names the entry.
- The call forms `item("<id>").state` and `git.ref()` with anything other than a listed string stay outside this build. `item("<id>")` comes with agent watches, which are its only user.

**When a value is read.** Every projection value is read once per evaluation and is the value as of that evaluation. The store keeps no history of an item's state, of the charter or of a ref, so the value as of an event's time cannot be read.

- `match`, `reset`, `after` and `clear_when` are still judged against each read event, and a projection operand has the same value for every one of them. So `clear_when = 'item.state in charter.finished'` holds on every read event of an item once the item is finished: the count is zero, and the episode resolves at the next evaluation.
- One sentence of [D-TW17](#D-TW17) does not hold for a rule that reads a projection: an evaluator that ran after every event and one that ran once at the end can leave different episodes behind, because the two read the projection at different times. For a rule that reads events only, the sentence still holds.

**What a missing value means.** Two cases are kept apart:

- **No value.** The subject has no row in `items`, or the column is NULL. The operand then has no value, and a comparison with it is false, as the grammar already states for an absent name. This is ordinary: a subject that is a run or a backend has no item.
- **Unreadable.** The projection itself could not be read: the `items` or `workflow_selections` table does not exist in the store, the store's charter does not parse, `git` failed or `--root` is not a repository, or a ref the rule lists does not point to a commit. A ref that does not exist is unreadable, not a value: a rule about a ref that is not there has nothing to compare.

A rule with an unreadable projection is **not evaluated at that evaluation**. It reads nothing, and its episodes are neither opened, updated nor resolved, so an alert does not resolve because the fact behind it went missing.

- `eval --once` prints `tillr watch: rule <id> not evaluated: <reason>` on standard error, and its exit code is unchanged. `wait` and `daemon` print that line at the first evaluation where it applies, and again whenever the reason changes; they print nothing when the rule becomes readable.
- `status` prints `rule <id> unreadable=<reason>` in place of the rule's `skipped` line ([D-TW34](#D-TW34)), with the reason written as a JSON string.

**`age()`.** `age(x)` takes one operand that is an integer of milliseconds since the Unix epoch, which is what `event.ts` holds. It yields the whole seconds from that time to the time of evaluation, as an integer, and can be negative. With any other operand it has no value. `age(event.ts) > 900` holds on an event more than fifteen minutes old. Like a projection, it is read as of the evaluation.

#### Named scopes ([D-TW31](#D-TW31))

This closes the rest of the worker's second gap: which fact names an item's workflow and the store's charter, whether it is read as of the event or the evaluation, and what its absence means. It replaces the third bullet of [D-TW21](#D-TW21) for the second build; rules scoped `core`, `workflow:*` and `charter:*` still apply to everything.

**A rule scoped `charter:<name>`** is in scope at an evaluation when the `name` of the store's charter, read as the `charter` projection is read, equals `<name>` exactly.

- When the store's charter has another name, the rule is not evaluated at that evaluation: it reads nothing and its episodes are left as they are. `eval`, `wait` and `daemon` print `tillr watch: rule <id> not evaluated: the store's charter is <name>` on the terms [D-TW30](#D-TW30) gives for an unreadable rule, and `status` prints `rule <id> out-of-scope=<the charter's name as a JSON string>`.
- A charter that does not parse is unreadable, as [D-TW30](#D-TW30) states.
- The rule need not list the `charter` projection. The scope reads the charter by itself.

**A rule scoped `workflow:<name>`** reads only the events of items on that workflow.

- The fact is the `workflow` column of the drain's `workflow_selections` table, which holds one row per item, written when the drain selects the item's workflow, in the form `planned-build@v1`. The `workflow` column of `items` is not used: it is empty for an item that left the choice to the drain.
- A read event whose subject is a string is **in scope** when `workflow_selections` has a row whose `item_id` equals the subject and whose `workflow` equals `<name>` exactly. Every other read event with a string subject is **out of scope**: the rule does not read it. It is not an observation, it is not counted, it is not a skipped event, and it neither resets nor clears.
- A read event whose subject is not a string is a skipped event before scope is looked at ([D-TW22](#D-TW22)).
- The loader refuses a `workflow:<name>` rule whose `group_by` is `global`, which rules out `lag_behind` as well: such a rule has no item to look up. The subject of such a rule must be an item's id, and the loader cannot check that. A rule grouped by a name that holds something else finds no row and reads no event.
- Membership is read once per evaluation, as of the evaluation, for the same reason as every projection ([D-TW30](#D-TW30)). An item's events from before its workflow was selected, when they are still inside `window`, are read once the row exists.
- An item with no row has no workflow yet, so no named-workflow rule reads its events. That is an absent value, not an unreadable projection. A store with no `workflow_selections` table is unreadable.
- `tools/tillr-watch` already links the drain package, so a store it migrates has the table.

One example, the case of [D-TW21](#D-TW21). A `predicate_match` rule scoped `workflow:planned-build@v1` reads `drain.refusal` and groups by `item_id`. Two refusals are inside its `window`, for `item-a`, whose row in `workflow_selections` says `planned-build@v1`, and for `item-b`, whose row says `doc-triage@v1`. The rule opens one episode, for `item-a`, and its skipped count is zero.

#### The condition fingerprint ([D-TW32](#D-TW32))

This closes the worker's third gap: which facts are hashed, how a rule names them, how they are written out before hashing, and what `silence --until-change` does with an episode that has no fingerprint.

**How a rule names the facts.** `[rule.when]` gains one optional key, `fingerprint`: a list of one or more names, each as [D-TW13](#D-TW13) defines a name and written as `group_by` and `field` are, without `event.` in front (`fingerprint = ["step", "detail.check"]`). It is allowed in every pattern except `lag_behind`. A rule without the key has no fingerprint.

**Which event the facts are read from.** The **fingerprint event** is the subject's newest counted event at the evaluation. For `alternating_pair` it is the first event of the newest counted observation, and for `duration_without_event` it is the waiting event.

**How the facts are written out and hashed.** The fingerprint is the SHA-256, in lowercase hexadecimal, of these bytes, for each name of `fingerprint` in the order the rule file writes them:

| The name's value on the fingerprint event | Bytes written |
| :--- | :--- |
| A JSON string | The byte `0x01`, then the string's length in bytes as an 8-byte big-endian integer, then the string's UTF-8 bytes |
| Anything else: missing, null, a number, a Boolean, a list or an object | The byte `0x00` |

Only strings are hashed, for the reason [D-TW22](#D-TW22) gives for subjects: no value of another type is encoded into a string. The rule's `id` and the subject are not hashed, because a fingerprint is only ever compared with another of the same episode.

**When it is stored.** `alert_episodes.condition_fingerprint` is written when an episode opens, at each update in place that moves `last_observed_seq`, and when an episode reopens ([D-TW33](#D-TW33)). An episode of a rule without the key stores the empty string, which still means that no fingerprint was computed and is compared with nothing ([D-TW20](#D-TW20)).

**What `silence --until-change` does.** It copies the episode's `condition_fingerprint` into `silence_on_fingerprint`. For an episode whose `condition_fingerprint` is the empty string it is refused: the command exits `1`, changes nothing, and says that the episode has no fingerprint and that `--until` is the silence it can take. That covers every episode the first build opened, every episode of a rule without the key, and every `lag_behind` episode. Nothing is hashed in place of the missing fingerprint.

One example. A `consecutive_count` rule reads `drain.roll-failed`, groups by `global` and has `fingerprint = ["step"]`. Its episode opens on a failure whose `step` is `"build"` and is silenced with `--until-change`. Two more failures with `step = "build"` leave the fingerprint as it was, and the episode stays silenced. A failure with `step = "smoke"` changes it, and the next evaluation reopens the episode.

Two alternatives were set aside by the designer. Hashing the JSON text of each value needs one canonical JSON encoding for numbers and objects, which nothing here defines. Hashing every counted event's values, and not only the newest, would change the fingerprint each time an old event left `window`, and reopen a silenced alert whose cause had not changed.

#### `ack`, `silence` and `resolve` ([D-TW33](#D-TW33))

Each command takes `--store <path>` (required) and the `alert_id` as its one argument, and does its work in one store transaction. On success it prints `alert <alert_id> <state>` on standard output, with the state it left the episode in, and exits `0`. An `alert_id` that no episode has, a refusal below, and a store error each print the reason on standard error, change nothing and exit `1`. None of the three evaluates rules, and none takes `--rules`.

| Command | Allowed when the episode is | What it writes | Event appended |
| :--- | :--- | :--- | :--- |
| `ack <alert-id> --by <actor> [--note <text>]` | `open` | `state = 'acknowledged'`, `acked_by`, `acked_note` (NULL without `--note`), `acked_at` | `watch.alert.acknowledged`, with `by` and `note` |
| `silence <alert-id> --until <duration or time> [--reason <text>]` | `open`, `acknowledged` or `silenced` | `state = 'silenced'`, `silence_until_ms`, and `silence_on_fingerprint` set to NULL | `watch.alert.silenced`, with `until` (RFC 3339, UTC) and `reason` |
| `silence <alert-id> --until-change [--reason <text>]` | The same, and `condition_fingerprint` is not empty ([D-TW32](#D-TW32)) | `state = 'silenced'`, `silence_on_fingerprint`, and `silence_until_ms` set to NULL | `watch.alert.silenced`, with `until_change` set to `true` and `reason` |
| `resolve <alert-id> --reason <text>` | `open`, `acknowledged` or `silenced` | `state = 'resolved'`, `resolved_reason = 'manual'`, `resolved_seq`, `resolved_at` | `watch.alert.resolved`, with `reason` set to `manual` and `note` holding the text |

- `--by` and `--reason` on `resolve` are required and may not be empty. Exactly one of `--until` and `--until-change` is given.
- A refusal names the state the episode is in, and for an `acknowledged` episode it names `acked_by`. Acknowledging an episode that is already acknowledged is refused, so the second of two agents learns who has it.
- `--until` takes a duration as `window` is written (`"90m"`), measured from the clock's time, or an RFC 3339 time. A time that is not later than the clock's time is refused.
- Silencing a `silenced` episode replaces its terms and appends another `watch.alert.silenced` event. Silencing an `acknowledged` episode keeps `acked_by`, `acked_note` and `acked_at`.
- The text of `--reason` on `silence` and on `resolve` is kept in the event's payload only. No column holds it.
- Every event the three commands append carries `alert_id`, `rule_id`, `subject_key` and `severity`, read from the episode row. It does not carry the opening event's `receiver`, which is a fact of the rule, and these commands load no rule. `resolved_seq` for a manual resolve is the greatest `events.seq` in the store before the command's own event.

**What the evaluator does with an episode that is not `open`.** An `acknowledged` or `silenced` episode is still the subject's one unresolved episode. It is updated in place and resolved exactly as an `open` one is, and the table in [D-TW17](#D-TW17) applies to it in every row: when a clearing event is read and the rule fires again, the old episode is resolved and the new one is `open`, with its own notice.

**When a silenced episode reopens.** At an evaluation where the rule fires for the subject and no clearing event later than `opened_seq` was read, a `silenced` episode reopens when either of these holds:

- `silence_until_ms` is set and the time of evaluation is that time or later;
- `silence_on_fingerprint` is set and the fingerprint computed at that evaluation differs from it.

Reopening sets `state = 'open'`, sets `silence_until_ms`, `silence_on_fingerprint`, `acked_by`, `acked_note` and `acked_at` to NULL, stores the new fingerprint, rebuilds the bundle from the condition at that evaluation, and appends `watch.alert.reopened`, whose payload carries `cause`: `silence-expired` or `fingerprint-changed`. `opened_seq` and `episode_num` are unchanged. The reopening event's `seq` is the notice seq, so `wait` prints the episode again ([D-TW14](#D-TW14)). A silenced episode whose rule no longer fires is resolved, not reopened.

**When a manual resolve holds.** A subject whose rule fires and that has no unresolved episode gets a new one ([D-TW13](#D-TW13)), with one exception that makes `resolve` mean something: when the subject's episode with the greatest `episode_num` was resolved with `resolved_reason = 'manual'`, a new episode opens only if its `opened_seq` would be greater than that episode's `resolved_seq`. For an event pattern that means a counted event newer than the resolve. The events that were already counted when the agent resolved the episode do not open it again; one more does.

- For `lag_behind`, `opened_seq` is the store's newest event, so a manual resolve holds only until any event at all is appended. `silence --until` is the command for a lag that the agent has seen and is waiting on.
- No state is added for this: the evaluator reads the subject's latest episode row, which it already reads to number the next one.

Two alternatives were set aside by the designer. Refusing `resolve` unless the rule no longer fires would make it a command that can only do what the evaluator is about to do. A stored "resolved until" time would be a second silence under another name.

#### The rest of `status`, and `daemon` ([D-TW34](#D-TW34))

**`status`.** `tillr watch status --store <path> --rules <dir> [--root <dir>] [--json]` still writes nothing to the store. It prints three groups of lines on standard output, in this order, and exits `0`:

1. One line for each rule that would be evaluated, in order of `id`: `rule <id> skipped=<n>` as the first build prints it, or `rule <id> unreadable=<reason>` ([D-TW30](#D-TW30)), or `rule <id> out-of-scope=<charter>` ([D-TW31](#D-TW31)).
2. One line for each unresolved episode, in order of `opened_seq` and then `alert_id`:

   ```text
   alert <alert_id> rule=<rule_id> subject=<subject_key as a JSON string> state=<state> severity=<severity> opened=<opened_at> observed=<observation_count>
   ```

   An `acknowledged` episode's line ends with ` by=<acked_by as a JSON string>`. A `silenced` episode's line ends with ` until=<silence_until_ms as a time>` or ` until=change`. Times are RFC 3339 in UTC. Episodes of rules that are no longer loaded are printed too, because the line is read from the row.
3. One line for each row of `foreground_delivery`, as [D-TW26](#D-TW26) states.

- A table that does not exist in the store (a store no watch command has migrated) is read as having no rows, so groups 2 and 3 are empty. `status` opens the store read-only and creates nothing.
- `--json` prints one JSON object in place of the lines, with three keys: `rules`, a list of objects holding `id` and one of `skipped` (an integer), `unreadable` (a string) or `out_of_scope` (a string); `alerts`, a list of objects holding `alert_id`, `rule_id`, `subject`, `state`, `severity`, `opened`, `observed`, and `by` or `until` where the line has them (`until` is a time or the string `change`); and `owners`, the list [D-TW26](#D-TW26) states. Each list is in the order of its lines.
- Armed agent watches are added to `status` by the build that adds agent watches.

**`daemon`.** `tillr watch daemon --store <path> --rules <dir> [--root <dir>]` is the evaluator of `wait` without the delivery: it evaluates when it starts and then on every wakeup, sleeping on `.tillr/state.db-wal` and the 60-second timer exactly as `wait` does.

- It prints nothing on standard output. On standard error it prints what `eval --once` prints, except that a rule's skipped-event line is printed only when its count differs from the count at the previous evaluation in that process.
- It reads no seen cursor, prints no notice and takes no attachment lock. It runs until it receives `SIGINT` or `SIGTERM`, and then exits `0`. A store error, or a stored rule the loader refuses ([D-TW35](#D-TW35)), ends it with exit `1`.
- Rule files are loaded once, when it starts; a changed file needs a restart. Stored rules are read at every evaluation.
- Nothing starts `daemon` in this build, and nothing depends on it: `wait` evaluates the rules itself ([Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3)). Supervising it beside `tillr-keep` is a later change to `tools/tillr-roll`.
- `tillr watch eval` still requires `--once`. `daemon` is the form that keeps running.

#### Live rules in the store: `tillr watch rule` ([D-TW35](#D-TW35))

The owner ruled that live tuning is stored in `watch_rule_overrides` over the tracked rule files, with `tillr watch rule export` to promote settled values ([OQ-TW5](#OQ-TW5)). The table's shape and the commands below are the designer's.

**A stored rule is a whole rule.** The table holds one complete rule per row, and a stored rule replaces the rule of the same `id` in the rule files entirely. A stored rule whose `id` no file has is an added rule. The **effective rules** are the file rules without those that a stored rule replaces, plus the stored rules, and they are what every command that takes `--rules` evaluates and reports.

```sql
CREATE TABLE IF NOT EXISTS watch_rule_overrides (
    rule_id    TEXT PRIMARY KEY,
    rule_toml  TEXT NOT NULL,     -- one [[rule]] table, complete
    set_by     TEXT NOT NULL,
    set_at     INTEGER NOT NULL
);
```

- `rule_toml` is one `[[rule]]` table as the TOML encoder writes it. Its bytes are not fixed by this document; what is required is that loading it gives the rule that was stored.
- A stored rule is checked by the same loader as a file rule, at the command that stores it and again each time it is read. One that is refused when it is read (it was stored by another version of the binary, or names an event kind that is no longer registered) is a refusal like a file's, naming `store` as the file: the command exits `1` without evaluating.
- `eval`, `wait` and `daemon` read the stored rules inside each evaluation, so a tuned rule takes effect at a running watcher's next evaluation. `status` reads them once. In a store without the table there are none.
- An episode of a rule that was tuned is treated at the next evaluation like any other: it is updated in place if the tuned rule fires for the subject and resolved if it does not.

Every `rule` command takes `--store <path>` and `--rules <dir>` (both required), and exits `1`, changing nothing, on a refusal.

| Command | What it does |
| :--- | :--- |
| `rule list` | Prints one line for each effective rule, in order of `id`: `rule <id> source=<file or store> enabled=<true or false> scope=<scope> pattern=<pattern>` |
| `rule show <id>` | Prints the effective rule of that `id` as one `[[rule]]` TOML table. An `id` no effective rule has is refused |
| `rule add <file> --by <actor>` | Loads `<file>` with the loader, and stores each rule in it, replacing a stored rule of the same `id` |
| `rule tune <id> --set <key>=<value>... --by <actor>` | Takes the effective rule of that `id`, sets each named key, checks the result with the loader, and stores it |
| `rule reset <id> --by <actor>` | Deletes the stored rule of that `id`, so the file rule, if there is one, is effective again. An `id` with no stored rule is refused |
| `rule export` | Prints every stored rule as one TOML document of `[[rule]]` tables, in order of `id`, and changes nothing. With no stored rule it prints nothing |

- In `--set <key>=<value>`, `<key>` is a path of keys joined by dots, from the rule's own table (`enabled`, `severity`, `when.threshold`, `when.window`, `delivery.receiver`, `guidance.summary`), and `<value>` is a TOML value (`6`, `"30m"`, `false`, `["foreground"]`). `--set` may be given several times, and all of them are applied before the one check. `id` cannot be set. A key the loader does not allow in that rule is refused by the loader's own check, as it would be in a file.
- `rule reset` was not in the command list of [Section 4.1](#41-new-utility-tillr-watch-vs-face-of-an-existing-utility-d-tw1-oq-tw1). It is added because without it a stored rule could never be removed, and `export` could not be followed by a return to the file.
- `add` and `tune` each append one `watch.rule.set` event per rule stored, and `reset` appends one `watch.rule.reset`. The payload carries `rule_id` and `by`; the event's object is the rule (`object_type` `rule`, `object_id` the rule's `id`). So a change of threshold is in the same event stream as the alerts it changes.
- Promoting a tuned rule is two steps the operator takes: write the output of `rule export` into a file under `config/tillr/watch/rules.d/` and land it, then `rule reset` the `id`. `export` does not write files and does not reset.
- While a stored rule exists, a change to the file rule of the same `id` has no effect. `rule list` shows `source=store` for it, which is how that is seen.

Two alternatives were set aside by the designer. Storing one row per overridden key leaves `rule add` with nowhere to put a new rule and makes the effective rule a merge that must be re-checked whenever either side changes. One `--threshold` and one `--receiver` flag, as [Section 4.1](#41-new-utility-tillr-watch-vs-face-of-an-existing-utility-d-tw1-oq-tw1) first listed, would need a flag for each key of each pattern.

#### What the second build leaves out

Each of these stays with the later build that owns it:

- agent watches (`tillr watch add`, `list`, `edit`, `drop`, the `agent_watches` table and `watch.fired`), `wait --follow`, the `item("<id>")` call, and the operator procedure of [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3);
- `tillr watch subscribe` and the `watch_consumers` table ([Section 6.5](#65-how-other-mechanisms-subscribe-to-the-same-alerts-d-tw9));
- every projection other than `items`, `charter` and `git_refs`, and a `lag_behind` leader that is not a Git ref;
- the item metadata, runs, refusals and landings of the `TimelineBundle` ([Section 6.2](#62-how-the-receiver-gets-the-items-timeline-with-the-alert-d-tw6));
- `watch` blocks in workflow files, `observability` blocks in charters, and `tillr watch lint`;
- delivery by the pack ([D-TW24](#D-TW24)), and supervising `daemon`;
- the rule catalog of [Section 5.2](#52-the-16-starting-rules-and-the-2026-11-20-cases-they-catch). The ten rules of [Section 5.4](#54-the-first-rules-shipped-ten-conditions-seen-on-2026-11-21-d-tw39) are not part of this build either; they are delivered by a build of their own ([D-TW39](#D-TW39)), and the second build keeps them loading.

Nothing in the second build acts on an alert. The owner ruled on 2026-11-21: "We don't cover issues. We use this to find the issues and fix them" ([OQ-TW2](#OQ-TW2)). `ack`, `silence` and `resolve` change only the episode's own row, a tuned rule changes only what is reported, and no command here runs a remedy, dispatches a run or moves an item.

---

## 7. Non-Git Workflows and `tillr watch lint`

### 7.1 Running the same rules against a non-Git workflow (`recorded-verdict`, `AutoQA`, custom loops)

As the owner noted, *"even if we have a week of tiller get landing working well the next tiller use may be different and agent needs to be able to debug this thing. both Git landing as well as whatever the new thing is they're making."*

Consider [`tools/tillr/examples/recorded-verdict/definition.json`](../../tools/tillr/examples/recorded-verdict/definition.json) and [`hook.py`](../../tools/tillr/examples/recorded-verdict/hook.py) — a non-Git investigation/AutoQA charter where `completes` is `"hook"` rather than `"landing"`, `finished` is `["confirmed", "rejected", "inconclusive", "repeat-of", "tooling-fault"]`, and completion invokes `hook.py complete-item` to record `item.verdict` with **no Git commits, no `tillr-gate`, no `refs/tillr/green`, and no `tillr-roll`**.

Because `tillr-watch` separates `core`, `workflow:*`, and `charter:*` scopes ([Section 4.2](#42-what-belongs-in-tillr-core-in-a-workflow-definition-and-in-the-charter-d-tw3)), switching a store's active definition from `git-landing` (`store.definition.set`) to `recorded-verdict` adapts the entire alerting system **with zero code changes**:

1. **All 9 `core` rules stay active unchanged:** `short-run-under-5s`, `run-failover-suffix`, `outside-allowed-paths-refusal`, `operator-hold`, `step-oscillation`, `dead-run-holding-lane`, `store-write-locked`, `empty-reason-record`, and `high-attempt-diagnostic` read `runs`, `lane_leases`, `drain.decision`, `drain.refusal`, and `questions` — all charter-independent.
2. **Workflow rules (`verify-refusal`, `plan-verify-cycles`, custom stage oscillation pairs) bind to whichever workflow JSON the non-Git project uses** (for instance `doc-triage@v1` or an `autoqa-investigate@v1` graph).
3. **Charter-parameterized rules rebind through `observability.progress_event`:** Rule #3 (`worker-hours-without-completion`) reads `charter.observability.progress_event` (`"item.verdict"` entering `definition.finished` instead of `"land.landing"`), immediately alerting if 3 worker-hours burn with zero verdicts recorded.
4. **Git-only rules deactivate cleanly, and charter-specific hook rules activate:** Rules scoped `charter:git-landing` (`unproved-trunk-lag`, `post-landing-roll-failing`, `gate-refusal`, `multiple-landings-build-unlanded`) do not run on `recorded-verdict`, while `charters/recorded-verdict.toml` activates `hook-completion-failing` (catching non-zero exits or invalid JSON/state transitions from `complete-item` hooks).
5. **Foreground agent watches (`tillr watch add`) work identically:** A foreground agent debugging a brand-new workflow registers `--when 'item("qa-42").state == "confirmed" or item("qa-42").state == "tooling-fault"' --note "Inspect hook output on first AutoQA trial"` in one command.

### 7.2 `tillr watch lint`: checking a workflow's whole configuration before it runs ([D-TW10](#D-TW10))

Today [`tools/tillr-drain/drain/workflow_check.go`](../../tools/tillr-drain/drain/workflow_check.go) (`tillr drain workflow check`, invoked by [`tools/gate/checks/workflows.py`](../../tools/gate/checks/workflows.py)) checks an individual workflow JSON file's schema, stage IDs, DAG acyclicity, argument expansion, and `outputs`/`inputs` wiring (`WorkflowRules`). What it **cannot** see is whether the charter, the workflow graph, the seat policy, the completion hook, and the alert rules actually fit together as a working process.

`tillr watch lint` loads the **four-part configuration tuple** `(definition.json, config/tillr/workflows/*.json, config/tillr/backend-policy.json, config/tillr/watch/*.toml)` without mutating `.tillr/state.db` or starting a run, and reports actionable refusal codes (`exit 1` on error, `exit 0` when clean):

| Lint rule ID | What it checks across the tuple | Defect caught before runtime |
| :--- | :--- | :--- |
| **`watch-lint-charter-candidate`** | Cross-checks `definition.completes` (`"landing"` vs `"hook"`) against workflow `exit.requires_candidate` (`"commit_required"` vs `"no_commit"` / `"commit_optional"`). | Agent authors a hook-completed `recorded-verdict` workflow that leaves `requires_candidate: "commit_required"` (every run fails candidate settlement) or a `git-landing` build workflow with `no_commit` (reaches `land` with nothing to squash). |
| **`watch-lint-hook-contract`** | When `definition.completes == "hook"`, executes `<hooks.argv> describe` (`tools/tillr/examples/recorded-verdict/hook.py`) read-only and verifies every named hook (`complete-item`) exists, accepts required `--item`/`--run`/`--store`/`--root` flags, and any transition states it declares belong to `definition.states`. | Missing executable bit, wrong hook path, or typoed terminal state in `hook.py` that would otherwise fail only at the end of a 20-minute worker run. |
| **`watch-lint-seat-unmapped`** | Verifies every `stage.seat` in workflows resolves to a non-empty backend list in `backend-policy.json`, and that `access: "write"` stages never target read-only seats (`scout`, `test-run`, `verify`, `review`). | Alert rule or new workflow routes to a seat with zero configured backends, silently dropping triage or stranding items at stage transition. |
| **`watch-lint-unbounded-return`** | Traces cross-stage and obstacle return edges (`verify -> plan` on `plan-wrong:`, `verify -> work` on `uncovered:`, `plan -> design` on `OBSTACLE: definition`) and warns/fails if a return cycle has no active `step-oscillation` / `plan-verify-cycles` watch rule covering its seats/stages. | Agent adds a new two-seat loop (`triage <-> reproduce`) with neither fence-escalation capability nor an oscillation rule watching it. |
| **`watch-lint-unknown-signal`** | Validates every event kind in `rule.reads.events` against `event.Kinds()` (plus charter hook events), every projection in `rule.reads.projections`, and every field in `rule.when` / `agent_watches` expressions; flags Git projections (`refs/tillr/green`, `tillr-roll`) used outside `scope = "charter:git-landing"`. | Typoed event kind (`"drain.roll_failed"` with underscore instead of `"drain.roll-failed"`) that would otherwise create a dead alert rule that **never fires** during an outage. |
| **`watch-lint-unknown-stage`** | Verifies every stage or seat name referenced in `workflow:<name>` rules and `workflow.watch` blocks exists in `<name>.json`. | Workflow renames stage `"work"` to `"implement"`, leaving its cycle/oscillation rule silently dead. |
| **`watch-lint-empty-guidance`** | Requires every standing rule to carry non-empty `guidance.summary`, at least one `guidance.check` step, and at least one `guidance.do` step, and validates `{{template}}` placeholders against the rule's output variables. | Half-written rule fires to an agent with blank instructions (`empty-reason-record` prevented at rule-authoring time). |
| **`watch-lint-forbidden-remedy`** | Rejects any rule that declares a `remedy` key at all ([OQ-TW2](#OQ-TW2)) or a `receiver` of `drain:<seat>` ([OQ-TW4](#OQ-TW4)) or whose `threshold` / `window` cannot be satisfied (e.g. `count_in_window` threshold `5` with `window = "0s"`). | Prevents retry/attempt kill switches from sneaking back into configuration disguised as alert remedies. |

Integrating `tillr watch lint` alongside `tillr drain workflow check` in [`tools/gate/checks/workflows.py`](../../tools/gate/checks/workflows.py) ensures neither an agent nor a human can land a broken workflow/charter/rule combination onto `main`.

---

## 8. Open Questions

None. The owner ruled all five on 2026-11-21; each is a row in [the Decision Ledger](#9-decision-ledger). The delivery design that the ruling on [OQ-TW3](#OQ-TW3) left to the designer is settled in [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) as [D-TW11](#D-TW11) and leaves no question for the owner.

---

## 9. Decision Ledger

The `OQ-TW` rows are the owner's rulings of 2026-11-21, given as Vantage review comments. Every `D-TW` row is a designer's engineering decision, made on 2026-11-20 except [D-TW11](#D-TW11) through [D-TW39](#D-TW39) (2026-11-21), of which [D-TW22](#D-TW22) and [D-TW23](#D-TW23) are the operator's design answers, [D-TW25](#D-TW25) is the designer's mechanism for an operator's request and [D-TW36](#D-TW36) is an operator's decision, and [D-TW39](#D-TW39) is from an operator request with the designer's reading; none has been ruled by the owner, and he may overturn any of them. The first build ([D-TW15](#D-TW15)) is built, and landed on 2026-11-21 as commit `e85ae973d9`; nothing else in either kind of row is built.

| ID | Decision | Provenance & Date | Governing section |
| :--- | :--- | :--- | :--- |
| <a id="OQ-TW1"></a>**[OQ-TW1](#OQ-TW1)** | The watchdog and linter ship as a standalone `tillr-watch` binary (`tillr watch ...`) backed by `tools/tillr/watch`, so it keeps working when `tillr-drain` stalls or runs a stale build. | Owner ruling; Vantage `eb108def` (2026-11-21) | [Section 4.1](#41-new-utility-tillr-watch-vs-face-of-an-existing-utility-d-tw1-oq-tw1) |
| <a id="OQ-TW2"></a>**[OQ-TW2](#OQ-TW2)** | No standing rule executes any remedy. The utility finds issues so they are fixed at the cause; it never covers one. Neither offered option was taken. | Owner ruling; Vantage `6e60dbac` (2026-11-21) | [Section 6.3](#63-mode-a-mechanical-answers-inside-tillr-and-non-wasting-drain-triage-d-tw7-oq-tw2-oq-tw4) |
| <a id="OQ-TW3"></a>**[OQ-TW3](#OQ-TW3)** | Foreground delivery ties into the owner's existing CI-watcher machinery, and no new Pi extension is built. The owner's words: "for Claude, you can easily do a background watcher that pings things. It does have to be restarted every two hours", and for Pi the same "without the two-hour limit". How it ties in is the designer's [D-TW11](#D-TW11). | Owner ruling; Vantage `342ea999` (2026-11-21) | [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) |
| <a id="OQ-TW4"></a>**[OQ-TW4](#OQ-TW4)** | Deferred. Every alert goes to the foreground agent, which records each time whether a better path existed. Dispatch to a drain seat is designed after the system is in use. | Owner ruling; Vantage `d1209edf` (2026-11-21) | [Section 6.3](#63-mode-a-mechanical-answers-inside-tillr-and-non-wasting-drain-triage-d-tw7-oq-tw2-oq-tw4) |
| <a id="OQ-TW5"></a>**[OQ-TW5](#OQ-TW5)** | Live tuning is stored as `watch_rule_overrides` in `.tillr/state.db` over git-tracked `config/tillr/watch/*.toml` baselines, with `tillr watch rule export` to promote settled values. | Owner ruling; Vantage `2e9a4d00` (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) |
| <a id="D-TW1"></a>**D-TW1** | Package the core schema, cursor evaluator, timeline bundler, and lint engine in `tools/tillr/watch`, exposed through a standalone `tools/tillr-watch` CLI binary (`tillr watch ...`, registered in `tillr-cli` and `tillr-roll.py` `MODULES`, subject to [OQ-TW1](#OQ-TW1)) so the watchdog stays independent of `tillr-drain` stalls/crashes and headless of `tillr-view`. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 4.1](#41-new-utility-tillr-watch-vs-face-of-an-existing-utility-d-tw1-oq-tw1) |
| <a id="D-TW2"></a>**D-TW2** | Enforce a hard boundary with `docs/design/tillr-state-machines.md` and the standing no-cap rulings ([`run-wall-ruling.md`](./run-wall-ruling.md), [`failure-diagnosed-not-waited-out.md`](./failure-diagnosed-not-waited-out.md), [`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md) `D-JL9`/`D-JL10`): synchronous single-move illegality belongs in state machines, emergent multi-step/cross-utility anomalies belong in `tillr-watch`, and an alert **never** fails, closes, benches, cancels, or caps an item. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 3.1](#31-prevention-vs-detection-relation-to-docsdesigntillr-state-machinesmd), [Section 3.2](#32-standing-ruling-an-alert-never-fails-benches-or-caps-work-d-tw2) |
| <a id="D-TW3"></a>**D-TW3** | Split observability cleanly across three layers: (1) `tillr` core owns the domain-agnostic cursor evaluator, `alert_episodes`, `agent_watches`, `TimelineBundle`, `subscribe`/`wait`, `lint`, and infrastructure rules; (2) workflow JSON (`config/tillr/workflows/*.json`) owns stage topology and optional `watch` cycle/oscillation hints; (3) charter `definition.json` + charter rule packs own completion semantics (`observability.progress_event`) and charter-specific health rules (`git-landing` vs `recorded-verdict`). | Designer's decision; the owner may overturn this (2026-11-20) | [Section 4.2](#42-what-belongs-in-tillr-core-in-a-workflow-definition-and-in-the-charter-d-tw3) |
| <a id="D-TW4"></a>**D-TW4** | Represent standing alert rules as pure declarative TOML/JSON data (`id`, `scope`, `reads`, `when`, `guidance.check`, `guidance.do`, `delivery.receiver`) evaluated over a total, non-Turing-complete typed predicate and window-aggregator grammar (no shell eval), shipping the 16-rule starting catalog covering all 15 seed conditions plus high-attempt diagnostics. Amended 2026-11-21 by [D-TW39](#D-TW39): ten rules from 2026-11-21 are shipped first and add to the sixteen. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5), [Section 5.2](#52-the-16-starting-rules-and-the-2026-11-20-cases-they-catch) |
| <a id="D-TW5"></a>**D-TW5** | Deduplicate standing-rule alerts to at most one non-resolved `alert_episodes` row per `(rule_id, subject_key)` (`open -> acknowledged -> silenced -> resolved`), updating `last_observed_seq` and `observation_count` silently on repeat passes, supporting both `--until <time>` and `--until-change` (`condition_fingerprint`) silences, and resolving automatically when `clear_when` holds. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 6.1](#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5) |
| <a id="D-TW6"></a>**D-TW6** | Attach a deterministic `<= 8 KiB` `TimelineBundle` (assembled in one read-only SQLite sidecar query over item metadata, fence, stage moves, last 10 runs + obstacles, verify/gate refusals, landings, and git/roll refs) to every opened alert and fired watch so receivers act immediately without running `tillr view events` or exploratory worker runs. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 6.2](#62-how-the-receiver-gets-the-items-timeline-with-the-alert-d-tw6) |
| <a id="D-TW7"></a>**D-TW7** | *Set aside on 2026-11-21 by the ruling on [OQ-TW4](#OQ-TW4); kept for when seat dispatch is designed.* Keep `tillr-drain` as the sole run dispatcher (`D-OC4`) and enforce a hard `active_triage_run_id IS NULL` precondition per `(alert_id, condition_fingerprint)` (`D-OC5`) whenever a rule routes to `drain:<seat>` ([OQ-TW4](#OQ-TW4)), guaranteeing at most one triage run per episode fingerprint. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 6.3](#63-mode-a-mechanical-answers-inside-tillr-and-non-wasting-drain-triage-d-tw7-oq-tw2-oq-tw4) |
| <a id="D-TW8"></a>**D-TW8** | Provide one-command persistent self-registered agent watches (`tillr watch add|list|edit|drop|wait`) stored in `.tillr/state.db` (`agent_watches`, `--once` default, and no prescribed workflow) backed by `inotify` wakeups on `.tillr/watch.wakeup` / `.tillr/state.db-wal` so a waiting process sits at 0% idle CPU and a new foreground session inherits every armed watch across compactions, handoffs, and restarts. Amended 2026-11-21: delivery state moved from a per-watch `last_delivered_seq` column to the per-owner seen cursor of [D-TW11](#D-TW11). | Designer's decision; the owner may overturn this (2026-11-20) | [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) |
| <a id="D-TW9"></a>**D-TW9** | Expose a cursor-resumable NDJSON pub/sub stream (`tillr watch subscribe --since-seq --consumer`) backed by `watch_consumers` checkpoints and `watch.*` store events so Observatory WebSockets, `steering-timer`, webhooks, and external supervisors subscribe uniformly without polling. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 6.5](#65-how-other-mechanisms-subscribe-to-the-same-alerts-d-tw9) |
| <a id="D-TW10"></a>**D-TW10** | Provide `tillr watch lint` (wired into `tools/gate/checks/workflows.py` alongside `tillr drain workflow check`) to validate the combined `(charter, workflows, backend-policy, watch-rules)` tuple pre-flight, including `completes: "hook"` `describe` contract checks, seat mappings, unguarded oscillation cycles, signal/stage type-checking, non-empty guidance, and static rejection of item-failing remedies. | Designer's decision; the owner may overturn this (2026-11-20) | [Section 7.1](#71-running-the-same-rules-against-a-non-git-workflow-recorded-verdict-autoqa-custom-loops), [Section 7.2](#72-tillr-watch-lint-checking-a-workflows-whole-configuration-before-it-runs-d-tw10) |
| <a id="D-TW11"></a>**D-TW11** | Deliver notices to the foreground agent with one harness-neutral primitive, `tillr watch wait`, run as a background watcher by the agent itself. In Claude Code it is a background Bash command started with `--timeout 110m` under the harness's two-hour limit, which exits when a notice is pending or the timeout passes; the exit wakes the agent, which re-arms it. In Pi it is one `background_task` running `tillr watch wait --follow` with no limit, waking the agent through an output watch. The only delivery state is a per-owner seen cursor in the store (`foreground_delivery`), moved only by the agent's confirmation (`--seen-through`, `tillr watch seen`), so a notice is repeated until confirmed and a stopped watcher loses nothing. The watcher executes no remedy ([OQ-TW2](#OQ-TW2)); the agent decides. Amended 2026-11-21 by [D-TW26](#D-TW26): whether a watcher is attached is read from a file lock, not from `waiting_since`. Amended 2026-11-21 by [D-TW36](#D-TW36), from an operator request: the watcher is started with explicit `--store` and `--rules` from the session's repository, and is not started while either path is missing. | Designer's decision, made under the owner's ruling on [OQ-TW3](#OQ-TW3); the owner may overturn this (2026-11-21) | [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) |
| <a id="D-TW12"></a>**D-TW12** | The `alternating_pair` `threshold` counts consecutive observations, not moves: `threshold = 4` fires on the fourth observation (`plan, design, plan, design`), which is three moves. Consecutive observations with the same value count as one, an observation outside the pair ends the alternation ([D-TW18](#D-TW18) states the count it leaves), and only observations inside `window` count. Repeats are collapsed only among events inside `window` ([D-TW23](#D-TW23)). | Designer's decision, following an operator request of 2026-11-21 that answered a build worker's question about which count governs; the owner may overturn this (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) |
| <a id="D-TW13"></a>**D-TW13** | A rule says which events count with `match` and, for `consecutive_count`, which events set the count to zero with `reset`; with `reset` left out, any read event for the subject on which `match` does not hold sets it to zero. `count_in_window` counts counting events inside `window` and nothing but age removes one; `predicate_match` is the same with the threshold fixed at 1. Counts are derived from the events inside `window` at each evaluation and are not stored. An episode resolves at the evaluation where its rule stops firing. The expression grammar for `match`, `reset` and `clear_when` is the table in Section 5.1. Amended 2026-11-21 by [D-TW22](#D-TW22): only a string value of the `group_by` name is a subject. Amended 2026-11-21 by [D-TW33](#D-TW33): after a manual resolve, a new episode opens only on a counted event newer than the resolve. | Designer's decision, answering a build worker's question of 2026-11-21 about how a rule declares what counts and what resets a consecutive count; the owner may overturn this (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) |
| <a id="D-TW14"></a>**D-TW14** | `tillr watch wait` prints every pending notice for its owner: an alert episode whose rule's receiver is `foreground`, whose state is `open`, and whose opening event is later than the owner's seen cursor. A second `wait` with no confirmation prints the same notices again; one that confirms with `--seen-through` does not. The seen cursor (`foreground_delivery`, `--seen-through`, `tillr watch seen`) is part of the `wait` primitive and is built with it. | Designer's decision, following an operator request of 2026-11-21 that asked for `wait` ahead of the rest of Section 6.4, and answering a build worker's question of that day about which alerts `wait` selects; the owner may overturn this (2026-11-21) | [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) |
| <a id="D-TW15"></a>**D-TW15** | The first build is the standalone binary, the loader for the whole rule schema, the evaluator for rules that read events only (`predicate_match`, `count_in_window`, `consecutive_count`, `alternating_pair`), episode opening and automatic resolution, and `tillr watch eval --once`, `wait` and `seen`. A loaded rule the first build cannot evaluate is named on standard error, not skipped silently. It ships no catalog rule and acts on no alert. Amended 2026-11-21 by [D-TW22](#D-TW22): it also ships `tillr watch status` in the form that prints each rule's skipped-event count and nothing else. Delivery of the first build is [D-TW24](#D-TW24). Built: it landed on 2026-11-21 as commit `e85ae973d9`. What follows it is [D-TW27](#D-TW27). | Designer's decision, following an operator request of 2026-11-21 that named the binary, the rule schema, the evaluator and `wait` as the part to build; the owner may overturn this (2026-11-21) | [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW16"></a>**D-TW16** | The key table of Section 5.1 lists every key a rule may carry, whether it is required and what value the loader accepts. For the four evaluated patterns, the pattern's listed keys are the only ones allowed in `[rule.when]` besides `clear_when`. A `lag_behind` or `duration_without_event` rule loads with only `group_by`, `pattern` and `clear_when` checked in `[rule.when]`: its other keys there are accepted unread and none is required, because this document does not fix them, and the build that adds each pattern's evaluator does. The loader's parser also accepts bare names, calls and `not in`, so the document's example rules load; a rule using them is not evaluated. The first build renders `{{subject}}`, `{{rule_id}}`, `{{alert_id}}`, `{{count}}`, `{{elapsed}}`, `{{pair_a}}` and `{{pair_b}}` and leaves any other placeholder as written. Amended 2026-11-21 by [D-TW19](#D-TW19): an `id` longer than 128 bytes is refused. Amended 2026-11-21 for the second build: [D-TW28](#D-TW28) and [D-TW29](#D-TW29) fix the keys of the two patterns and the loader then checks them, [D-TW30](#D-TW30) fixes the entries of `reads.projections`, and [D-TW32](#D-TW32) adds the `fingerprint` key. | Designer's decision, answering a build worker's question of 2026-11-21 about which keys a `duration_without_event` rule needs in order to load; the owner may overturn this (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5), [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW17"></a>**D-TW17** | A clearing event (one on which `clear_when` holds, a resetting event, or an `alternating_pair` observation that ends a run, as [D-TW18](#D-TW18) defines it) ends the episode that was unresolved when it arrived, even when the rule fires again by the next evaluation. An evaluation that reads a clearing event later than an unresolved episode's `opened_seq` resolves that episode, and when the rule fires at that evaluation it also opens a new episode with a new `watch.alert.opened` event, which `tillr watch wait` prints. With no such clearing event the episode is updated in place with no notice. A count that fell only through age and rose again between evaluations keeps its episode. | Designer's decision, answering a build worker's question of 2026-11-21 about an open episode that receives a clearing event and then a new matching event before the next evaluation; the owner may overturn this (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) |
| <a id="D-TW18"></a>**D-TW18** | An `alternating_pair` rule with several entries in `pairs` keeps one count per subject, taken from the newest observations whichever entry they belong to: the run is the most recent observations that alternate between the two members of one entry, and no entry is remembered from an earlier run or episode. An observation ends a run, and is the pattern's clearing event, when the count after it is not one more than the count before it. That observation can be the first of the next run, so `a, b, a, b, c, d, c, d` with `threshold = 4` resolves the first episode at `c` and opens a second for `c`, `d` on the fourth observation of the second run. The matched entry is the first in file order whose members are the two newest values. | Designer's decision, answering a build worker's question of 2026-11-21 about which pair sets the clearing boundary when a second pair fires after the first, and whether the observation that clears one pair counts toward the next; the owner may overturn this (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) |
| <a id="D-TW19"></a>**D-TW19** | The 8 KiB budget of a `TimelineBundle` is 8,192 bytes of the JSON text stored in `bundle_json`, and it covers the whole bundle, guidance included. A bundle over the budget is cut in a fixed order until it fits: counted events oldest first, then `guidance.do` lines from the last, then `guidance.check` lines from the last, then the end of `guidance.summary`, then the end of the bundle's copy of the subject. The bundle's `omitted` object records what was cut, and `wait` prints it. The loader refuses a rule `id` longer than 128 bytes, so the cutting always ends with a bundle that fits; no rule is refused for long guidance and no episode fails to open. | Designer's decision, answering a build worker's question of 2026-11-21 about a loadable rule whose rendered guidance alone exceeds 8 KiB; the owner may overturn this (2026-11-21) | [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW20"></a>**D-TW20** | The first build computes no condition fingerprint. It stores the empty string in `alert_episodes.condition_fingerprint`, which means "not computed" and is compared with nothing. The build that adds `tillr watch silence --until-change`, the column's one reader, fixes which cause facts are hashed, how a rule names them and their encoding. Those are fixed by [D-TW32](#D-TW32), under which the empty string keeps this meaning and `--until-change` is refused for an episode that has it. | Designer's decision, answering a build worker's question of 2026-11-21 about which facts and serialization the digest takes for the four event patterns; the owner may overturn this (2026-11-21) | [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW21"></a>**D-TW21** | The first build never filters events by a rule's scope, and reads no workflow membership or active charter. A rule scoped `core`, `workflow:*` or `charter:*` applies to every item and is evaluated. A rule scoped to a named workflow or charter loads, is not evaluated, and is named on standard error with that reason; it opens no episode. The build that adds projections fixes the fact that names an item's workflow and the store's charter, whether it is read as of the event or the evaluation, and what an absent fact means. Those are fixed by [D-TW31](#D-TW31), which replaces the rule on named scopes from the second build on. | Designer's decision, answering a build worker's question of 2026-11-21 about a `workflow:planned-build@v1` rule reading `drain.refusal`, an event that does not carry the item's workflow; the owner may overturn this (2026-11-21) | [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW22"></a>**D-TW22** | `group_by` groups on JSON string values only. A read event whose `group_by` value is missing, null, or a Boolean, number, list or object belongs to no subject and is not counted by that rule, and no value of another type is encoded into a string. The evaluator counts such skipped events per rule, derived from the rule's read events inside `window` at each evaluation and not stored, and `tillr watch status` shows the count. The first build ships `status` with that one output (`rule <id> skipped=<n>`, writing nothing to the store), and `eval --once` names each rule with a count above zero on standard error. | Operator's design answer of 2026-11-21 to a build worker's question about a `group_by` value that is not a string; the command form and output lines are the designer's. The owner has not ruled and may overturn this (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5), [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW23"></a>**D-TW23** | The window is applied first, and nothing outside it is ever consulted, for any pattern. Repeats of one `field` value are collapsed only among events inside `window`, so a run of equal observations begins anew at the edge of `window` and is dated at its first event inside. With `plan` 61 minutes before the evaluation, `plan` 59 minutes before and `design` 1 minute before, `window = "60m"` and `threshold = 2`, the count is 2 and the rule fires. An event older than `window` neither counts, nor collapses a repeat, nor resets, nor clears. | Operator's design answer of 2026-11-21 to a build worker's question about a repeat that straddles the edge of the window, applying [D-TW13](#D-TW13); the owner has not ruled and may overturn this (2026-11-21) | [Section 5.1](#51-declarative-rule-schema-d-tw4-oq-tw5) |
| <a id="D-TW24"></a>**D-TW24** | The first build is delivered from the source tree by `tillr-roll` only. It registers `watch` in `nouns` (`tools/tillr-cli/main.go`) and in `MODULES` (`tools/tillr-roll/tillr-roll.py`) and nowhere else: it adds no pack launcher, no `program` entry in `yolo-packs/tillr/pack.json`, no pack copy of `tools/tillr-watch`, and no entry in any mirrored-module list or in `tests/pack-sync.test.py`. The library `tools/tillr/watch` is mirrored with the `tools/tillr` module it belongs to. In a workspace served by the pack, `tillr watch` ends with the dispatcher's refusal that `tillr-watch` is not on `PATH`. Pack delivery is one later change by an item whose allowed paths include `tests/pack-sync.test.py` and `tests/tillr-docs.test.py`. What a roll to a revision without `tools/tillr-watch` does with the installed binary is [D-TW25](#D-TW25). | Designer's decision, answering a build worker's obstacle of 2026-11-21 that a pack launcher fails `tests/pack-sync.test.py`, which the build may not write; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW25"></a>**D-TW25** | A successful roll to a revision that has no `tools/tillr-watch` removes the installed `tillr-watch` from the install directory, on a deferred installation as well, and a failed roll removes nothing. The roller, `tools/tillr-roll/tillr-roll.py`, does every removal, because it runs from the repository's checkout and so survives a roll to an older revision. It builds `tillr-watch` only when the revision has `tools/tillr-watch`. On an immediate installation it removes the binary after the other binaries are installed. On a deferred installation it writes the marker `tillr-watch.retire` (`commit` and `stage`) beside `tillr-roll.commit`, and removes the binary when it settles the marker: at the start of every roll and after a requested restart is answered, once `tillr-roll.commit` holds that commit and the staging directory is gone. With `--no-request` on a deferred installation the binary therefore stays until the next roll starts. The build adds `tillr-watch` to the programs `activateSchemaRoll` moves. `watch` stays in `MODULES`, and the tests are Go tests in `tools/tillr-drain` that drive the real roller, new to old and old to new, with none added under `tests/`. | From an operator request of 2026-11-21 (the removal on success, nothing removed on failure, both directions tested, `watch` kept in `MODULES`, no test under `tests/`); the mechanism is the designer's decision, answering a build worker's obstacle of 2026-11-21 that nothing named who removes the binary after a deferred roll to a revision that predates it. The owner has not ruled and may overturn either (2026-11-21) | [Section 6.6](#66-the-first-build-event-rules-episodes-and-wait-d-tw15) |
| <a id="D-TW26"></a>**D-TW26** | Whether an owner's watcher is attached is asked of the kernel, not read from the store. `tillr watch wait` holds a shared POSIX record lock on byte 0 of `<store path>.watch-attached/<SHA-256 of the owner handle, in hexadecimal>.lock` for the life of its process, and exits `1` when it cannot take it. `tillr watch status` asks with `F_GETLK` whether a write lock would be refused, taking no lock and writing nothing: a refusal is attached, anything else is unattached. A killed watcher's lock is released by the kernel, so it reads as unattached although its cleanup never ran. `waiting_since` becomes the start time of the newest `wait`, shown only when the lock says attached, and is never evidence of attachment. `status` prints one `owner <handle> watcher=attached|unattached ... pending=<n>` line per owner. A heartbeat column, a process ID in the row and a signal handler were set aside. Amended 2026-11-21 by [D-TW37](#D-TW37): what `status` reports for an attached owner whose `waiting_since` is NULL. | Designer's decision, answering a build worker's question of 2026-11-21 about a watcher ended by force leaving the same row as a healthy idle one; the owner may overturn this (2026-11-21) | [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) |
| <a id="D-TW27"></a>**D-TW27** | The second build is the `lag_behind` and `duration_without_event` patterns; the `items`, `charter` and `git_refs` projections with their operands and `age()`; rules scoped to a named charter or workflow; a computed condition fingerprint; `tillr watch ack`, `silence` and `resolve`; the rest of `status`; `daemon`; and `tillr watch rule` with the `watch_rule_overrides` table. `eval`, `wait`, `status` and `daemon` gain `--root`, the repository Git refs are read from, and refuse to run without it when an enabled rule lists a `git_refs` projection. An evaluation reads Git before it opens its store transaction, and starts again when a rule stored meanwhile needs a Git fact it did not read ([D-TW38](#D-TW38)). It ships no rule file, is delivered by `tillr-roll` only, and acts on no alert. Agent watches, `subscribe`, lint, pack delivery and the catalog stay with later builds. Amended 2026-11-21 by [D-TW39](#D-TW39): ten rules are delivered ahead of the catalog by a build of their own, which may land before this one; this build keeps them loading. | Designer's decision, following an operator request of 2026-11-21 that names the remaining patterns, the projections and the rest of `status`, `daemon`, `ack`, `silence`, `resolve` and `rule` as the second build; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW28"></a>**D-TW28** | `lag_behind` compares two Git refs. Its keys are `group_by` (only `global`), `leader` and `follower` (full ref names listed in the rule's `git_refs:` projection), `threshold_commits`, `threshold_duration` (at least one of the two) and `mode` (`any` or `all`, only with both); every other key, `clear_when` included, is refused, and `reads.events` may be left out. The lag is the number of commits reachable from the leader and not from the follower, and the age is the time of evaluation less the least committer time among them. An unresolved episode whose rule still fires is left untouched, and it resolves at the first evaluation where the rule does not fire. A leader that is not a Git ref is not covered. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request for the second build, which named the keys of `lag_behind` as a gap; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW29"></a>**D-TW29** | `duration_without_event` fires for a subject whose newest start or end event inside `window` is a start event at least `threshold_duration` old. Its keys are `group_by`, `after` (required: which read events start the wait), `match` (optional: which end it), `threshold_duration`, `window` (longer than `threshold_duration`), and optionally `clear_when` and `fingerprint`. A later start event replaces an earlier one, and every start or end event is a clearing event for [D-TW17](#D-TW17). The waiting event is the one counted event. A wait longer than `window` stops firing, and a subject with no start event cannot fire. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request for the second build, which named the keys of `duration_without_event` as a gap; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW30"></a>**D-TW30** | A rule reads three projections, each listed in `reads.projections`: `items` (the subject's row: `item.state`, `item.seat`, `item.waiting_on`), `charter` (the store's charter: `charter.name`, `ready`, `blocked`, `closed`, `states`, `finished`) and `git_refs:<ref>,...` (`git.ref("<ref>")`, the commit's object name). The loader refuses an operand whose projection the rule does not list. Every value is read once per evaluation, as of the evaluation, and is the same for every event the rule reads. A subject with no row gives no value, so comparisons are false. A projection that cannot be read (a missing table, a charter that does not parse, a failing `git`, a ref that points to no commit) leaves the rule unevaluated at that evaluation with its episodes untouched, named on standard error and in `status`. `age(x)` is the whole seconds from a millisecond time to the evaluation. Any other projection entry loads and is not evaluated. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request for the second build, which named projection sources, evaluation timing and missing-data behavior as a gap; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW31"></a>**D-TW31** | A rule scoped `charter:<name>` is evaluated when the store's charter has that name, and is otherwise left unevaluated at that evaluation with its episodes untouched. A rule scoped `workflow:<name>` reads a read event only when the drain's `workflow_selections` table has a row whose `item_id` is the event's subject and whose `workflow` is `<name>` exactly; every other event with a string subject is out of scope and is neither counted nor skipped. Membership is read as of the evaluation. An item with no row is on no workflow yet; a store with no such table is unreadable. A `workflow:<name>` rule grouped by `global` is refused at load. This replaces the named-scope rule of [D-TW21](#D-TW21) from the second build on. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request for the second build, which named the source, timing and missing-data behavior of a named scope as a gap; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW32"></a>**D-TW32** | A rule names its cause facts with an optional `[rule.when]` key `fingerprint`, a list of names, allowed in every pattern except `lag_behind`. The fingerprint is the SHA-256, in lowercase hexadecimal, of one record per name in file order, read from the subject's newest counted event: the byte `0x01`, an 8-byte big-endian length and the UTF-8 bytes for a JSON string, and the byte `0x00` for anything else. It is stored when an episode opens, at each update in place that moves `last_observed_seq`, and at a reopening. `tillr watch silence --until-change` is refused for an episode whose stored fingerprint is the empty string, which is every episode of a rule without the key, every `lag_behind` episode and every episode the first build opened; nothing is hashed in its place. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request for the second build, which named which facts are hashed, their encoding, and what `silence --until-change` does with an empty fingerprint as a gap; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW33"></a>**D-TW33** | `ack` moves an `open` episode to `acknowledged` and is refused in any other state, naming who holds it. `silence` moves an `open`, `acknowledged` or `silenced` episode to `silenced`, with `--until` a duration or an RFC 3339 time, or with `--until-change`. `resolve` moves any unresolved episode to `resolved` with `resolved_reason = 'manual'`. Each takes `--store` only, appends `watch.alert.acknowledged`, `watch.alert.silenced` or `watch.alert.resolved`, and exits `1` on a refusal. The evaluator updates and resolves an acknowledged or silenced episode as it does an open one. A silenced episode whose rule still fires reopens, with `watch.alert.reopened` and a new notice, when its time has passed or its fingerprint differs. After a manual resolve, a new episode opens only when its `opened_seq` would be greater than the resolved episode's `resolved_seq`. | Designer's decision, made so the second build's three alert commands have stated behavior; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW34"></a>**D-TW34** | `status` prints, in order, one line per evaluated rule (`skipped=<n>`, `unreadable=<reason>` or `out-of-scope=<charter>`), one `alert ...` line per unresolved episode in order of `opened_seq`, and the owner lines of [D-TW26](#D-TW26); `--json` carries the same under `rules`, `alerts` and `owners`. It writes nothing, and reads a table that does not exist as empty. `daemon` evaluates at start and on every wakeup, as `wait` does, with no delivery: it prints no notice, takes no attachment lock, runs until `SIGINT` or `SIGTERM`, and nothing starts it or depends on it in this build. `eval` still requires `--once`. | Designer's decision, made so the second build's `status` and `daemon` have stated output and behavior; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW35"></a>**D-TW35** | `watch_rule_overrides` holds one complete rule per row (`rule_id`, `rule_toml`, `set_by`, `set_at`), and a stored rule replaces the file rule of the same `id` entirely. The effective rules are what every command evaluates; running watchers read stored rules at each evaluation. `rule list` and `rule show` print the effective rules, `rule add <file>` stores a file's rules, `rule tune <id> --set <key>=<value>` stores the effective rule with those keys changed, `rule reset <id>` deletes a stored rule, and `rule export` prints every stored rule as TOML and changes nothing. Each takes `--store` and `--rules`, each change checks the result with the loader, and `add`, `tune` and `reset` take `--by` and append `watch.rule.set` or `watch.rule.reset`. `rule reset` and `--set` replace the command list first written in Section 4.1. Amended 2026-11-21 by [D-TW38](#D-TW38): an evaluation reads the stored rules twice, before it reads Git and again inside its transaction. | Designer's decision on the table's shape and the commands, made under the owner's ruling on [OQ-TW5](#OQ-TW5); the owner may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW36"></a>**D-TW36** | In both harnesses the foreground watcher is started with explicit arguments taken from the repository the session is working in: `--store <repository root>/.tillr/state.db` and `--rules <repository root>/config/tillr/watch`. Nothing is read from a home directory or an environment default. When the store file or the rules directory does not exist, the watcher is not started and one line names the missing path; attachment is deferred and tried again at the next session start, compaction or handoff. A rules directory that exists and is empty is valid: the watcher attaches and waits with zero rules. The delivery build adds no rule; the rule catalog stays deferred. Amends [D-TW11](#D-TW11), whose two launch examples named neither argument. Amended 2026-11-21 by [D-TW39](#D-TW39): a separate build creates the rules directory with ten rules, after which attachment is no longer deferred in this repository for want of it. | From an operator request of 2026-11-21, given to the build as a retry reason and answering a build worker's obstacle of the same day; the reading of "repository root" and of who prints the line is the designer's. The owner has not ruled and may overturn either (2026-11-21) | [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) |
| <a id="D-TW37"></a>**D-TW37** | An owner whose attachment lock is held while `waiting_since` is NULL (two watchers, and the newer one returned) is reported as attached with no start time: the text line is `owner <handle> watcher=attached since=- last_wait=<last_wait_at> pending=<n>`, and `--json` gives `since` as `null`. In `--json`, `since` and `last_wait` are always present, each an RFC 3339 string or `null`, and `since` is `null` whenever `attached` is `false`. `since` is the start of the newest `wait` that has not returned, not of the oldest watcher alive. `wait` is not changed. One stored row per watcher process, and a start time written into the attachment file, were set aside. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request for the second build, which named the text and JSON for an attached owner with no timestamp as a gap; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.4](#64-mode-b-persistent-self-registered-foreground-agent-watches-d-tw8-oq-tw3) |
| <a id="D-TW38"></a>**D-TW38** | An evaluation reads the stored rules twice: with a plain read before it reads Git, and again inside its write transaction, and it uses the rules of the second read. When those rules need a Git fact the first step did not read (the commit of a listed ref, or the lag and oldest time of a `lag_behind` pair), the evaluation rolls back with nothing written and starts again from the first step, with no limit on starts. Otherwise it goes on with the Git values the first step read. A fact found unreadable counts as read. A stored rule the loader refuses, and a `git_refs` rule with no `--root`, are refused on whichever read meets them. Leaving the new rule unevaluated until the next evaluation, and evaluating the rule it replaced, were set aside. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request for the second build, which named a `rule tune` that introduces a new Git ref between the Git read and the transaction as a gap; the owner has not ruled and may overturn this (2026-11-21) | [Section 6.7](#67-the-second-build-the-remaining-patterns-projections-and-the-alert-commands-d-tw27) |
| <a id="D-TW39"></a>**D-TW39** | Ten rules are delivered to `config/tillr/watch` ahead of the second build and ahead of the catalog, by a build that changes no evaluator, loader or command. Each reports one condition seen in the drain on 2026-11-21: a loop exit on a fault; one item refused by its verifier many times in a row; one item moving between a build obstacle and a design answer repeatedly; an item held for the operator; a heavy trunk failure, a revert, and a revert that was not possible (delivered as the two revert rules only, [D-TW40](#D-TW40)); a trunk fixer filed; a squash conflict at landing; an item fault; the trunk unproved for longer than a stated time; and an ordinary lane idle while items wait only for a lane. They add to the sixteen rules of [D-TW4](#D-TW4) and replace none; the catalog build starts from them, and a shipped rule takes a catalog `id` only when it is that catalog rule as stated. Each rule reads registered event kinds only, states its incident and its kinds in a comment, says in its guidance what to look at, and applies no remedy. A rule that needs `lag_behind`, `duration_without_event` or a projection is written now to the keys of [D-TW28](#D-TW28), [D-TW29](#D-TW29) and [D-TW30](#D-TW30), loads, and is named as not evaluated until the build that can evaluate it. Every one of the rules, those included, lists at least one registered kind in `reads.events`, because the landed loader refuses an empty or absent list and this delivery changes no loader; the empty list [D-TW28](#D-TW28) allows a `lag_behind` rule arrives with the second build's loader and is not written before it. One test loads the directory with the real loader and asserts, per rule, that it is evaluated or is unevaluated for a stated reason. Amends [D-TW4](#D-TW4), [D-TW27](#D-TW27) and [D-TW36](#D-TW36). Replacing the sixteen, waiting for the catalog build, and changing the landed loader to accept an empty `reads.events` were set aside. | From an operator request of 2026-11-21, answering a build worker's definition obstacle of the same day, which named the conflict with [D-TW4](#D-TW4) and [D-TW27](#D-TW27). That the ten add to the sixteen, the rule for `id`s, the scope, receiver and threshold readings, the keys of rules awaiting the second build, the non-empty `reads.events` of every rule (the designer's answer to a second definition obstacle of the same day, which found an empty list refused by the landed loader), and the home of the test are the designer's. The owner has not ruled and may overturn any of it (2026-11-21) | [Section 5.4](#54-the-first-rules-shipped-ten-conditions-seen-on-2026-11-21-d-tw39) |
| <a id="D-TW40"></a>**D-TW40** | Condition 5 of [D-TW39](#D-TW39) is delivered as two rules that read the registered event `drain.revert`: one for a landing reverted (`result` is `reverted`) and one for a revert that was not possible (`result` is `not-possible`). No rule is written for a heavy trunk failure itself, because the heavy verdict is written to the file `heavy.json` and to no event, and `drain.revert` is written only when a revert is attempted. A heavy failure with no revert attempt (automatic revert off, or no landing attributed) is reported by the rule of condition 6, since the heavy runner files a trunk fixer for every failure it did not revert; condition 9 reports a trunk that stays unproved once the second build evaluates it. The failing checks themselves, and a failure whose fixer was filed by an earlier run, are not reported by any of the ten rules. Amends [D-TW39](#D-TW39). An event for each heavy verdict (a change to the drain, outside this delivery), a third rule on an unregistered kind, and a rule reading `heavy.json` through a projection that no build evaluates, were set aside. | Designer's decision, answering a build worker's definition obstacle of 2026-11-21 on the operator's request of the same day, which asked for a rule on a heavy trunk failure and for registered event kinds only; the owner has not ruled and may overturn this (2026-11-21) | [Section 5.4](#54-the-first-rules-shipped-ten-conditions-seen-on-2026-11-21-d-tw39) |
