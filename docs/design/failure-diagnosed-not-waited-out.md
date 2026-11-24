---
title: "A failure is diagnosed, not waited out"
status: accepted
stage: DECIDED
next: "Build what landing principle 11 names as not enforced: the fleet trip, the tier-0 probes and the fingerprint store."
tags: [tillr, drain, failure-handling, diagnosis, principles]
summary: "Elapsed time is never a reason to re-run work. A failure is fingerprinted from its own text; repetition under unchanged inputs or recurrence across items establishes determinism by observation; a deterministic failure blocks its items on one diagnosis ticket per fingerprint. The rule is landing principle 11; the worker-run path is built, and the fleet trip, the tier-0 probes and the fingerprint store are not."
---

# A failure is diagnosed, not waited out

**Status:** The rule is [landing principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), whose *Enforced by* paragraph is the statement of record for what `tillr-drain` enforces. This document specifies the whole mechanism. [What is built](#what-is-built) says which parts exist.

> **In short.** A retry, a retry budget, a cooldown and a bench that expires are four spellings of one mechanism: *spend another run and hope the world changed*. None names what will be different on the next run, because nothing checks. This design replaces all four with one admission rule — **nothing re-runs until a named change has happened, or a clearing condition stated by the failure itself has been verified** — and one destination for failures that cannot clear: one diagnosis ticket per failure fingerprint, with the affected items visibly blocked on it.

**The shape.** Fingerprint every failure from its own text. Route it by tests that measure rather than infer: a literal machinery cause quoted in the failure, the same fingerprint recurring under unchanged inputs, or the same fingerprint recurring across distinct items. Any of the three makes the failure deterministic, and a deterministic failure is diagnosed. A wait survives in exactly one place: where the failure itself states a clearing condition the loop can check.

**Cost.** One fingerprint computation per failed run, one ticket per distinct failure, and visibility: a failure that a cooldown would have quietly recycled sits blocked, by name, until someone diagnoses it.

**Needs your ruling:** None.

**Reads with:** [`landing-principles.md`](landing-principles.md) (principles 1, 5, 6, 7 and 9 do most of this design's work), [`escalation-ladder.md`](escalation-ladder.md) (the destination for what diagnosis cannot settle), [`tillr-judge-and-lifecycle.md`](tillr-judge-and-lifecycle.md) (how a worker run's ending moves its item).

---

## What is built

- **Built — the worker-run path.** `tillr-drain` holds no refused worker run on a clock. A refusal goes back to the worker with its evidence, and only an identical repeat — an ending whose producer, cause and normalized evidence equal an earlier ending of the item — goes to the design seat to be diagnosed (`repeatedEnding` and `conflictEndingKey` in `tools/tillr-drain/drain/returned.go`, `designRepeatOf` in `tools/tillr-drain/drain/design_repeat.go`). This is [`D-JL10`](tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10). An infrastructure failure is handled by [recorded backend availability](../../tools/tillr-drain/README.md#recorded-backend-availability).
- **Not built.** The fingerprint store and inputs hash ([§3.1](#31-the-four-tests)), the fleet trip (test T3), the tier-0 probes ([§4.1](#41-the-three-tiers-cheapest-first)) and the per-fingerprint diagnosis ticket ([§4.2](#42-the-product-a-cause-statement-with-a-clearing-condition)).

On the worker-run path, `D-JL10` narrows two things this specification would otherwise allow: no automatic retry runs as a test ([§3.2](#32-the-unknown-case-and-the-one-permitted-retry)), and a changed input hash on its own re-runs nothing. A named change there is an event that names the item: an operator's retry, a design answer, or the recovery of a killed run.

## 1. What the cooldown actually was

A **cooldown** is a stored wall-clock hold: an item whose run failed is held until a timestamp, then re-admitted. Its theory of recovery is that the failure might be transient and might be gone after a wait. Nothing in it can tell that case from its opposite, because nothing reads the failure.

The general form, which [§7](#7-the-principle) rules against, is **any mechanism whose only theory of recovery is that time passed or that repetition might differ.** A retry budget spends runs on the same hope, faster. A bench that expires parks the same hope. Each consumes a run, a budget or an item's time without producing a new fact about why the work failed.

Measured basis: under a persisted 30-minute cooldown, 55 items were held behind one lane model string that could not resolve; 383 of 1,083 child logs carried the same error line, and a re-admitted item died 8 seconds after launch.

The acts that do produce information are the ones a person performs: read the error, form a claim about the cause, and re-run only after changing something that bears on that cause.

## 2. What already does this job

The design is built from parts the landing principles already rule:

| Existing mechanism | What this design takes from it |
| :--- | :--- |
| **The charging rule** ([principle 5](landing-principles.md#5-only-verdicts-charge)) | The destination for a failure that is a verdict about the work (test T4). Nothing is added there |
| **The deduped ticket** ([principle 9](landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work)) | The destination for a deterministic failure ([§4](#4-diagnosis-mechanically)). This design gives the ticket a dedupe key, the fingerprint, and a second producer: lane failure, beside landing failure |
| **The escalation ladder** ([principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop), [`escalation-ladder.md`](escalation-ladder.md)) | The destination when diagnosis cannot run or cannot conclude ([§4.3](#43-when-diagnosis-itself-cannot-run)) |
| **The refusal vocabulary** ([principle 1](landing-principles.md#1-a-refusal-must-be-a-verdict)) | The separation of machinery statements from candidate verdicts, reused at lane-failure time. No failure words are added |
| **A durable hold** | A restart-proof record that an item is held. The condition stored is a ticket reference, never a timestamp ([§6](#6-the-hold-is-kept-its-clock-is-not)) |

Three things are new: a **failure fingerprint**, an **admission rule keyed on a named change rather than elapsed time**, and a **fleet trip** that stops a common-cause failure from burning items one at a time.

## 3. The decision procedure — read the failure, do not classify it

The procedure contains no classifier that guesses transient-or-deterministic. It contains one extraction and four tests, in a fixed order. Three tests read only evidence the system itself produced — the failure's quoted text, its recurrence, its spread across items — and the fourth is the verdict test principle 5 already applies. A failure that passes none of them is *unknown* ([§3.2](#32-the-unknown-case-and-the-one-permitted-retry)).

### 3.1 The four tests

**Extraction (always, first).** On every failed run, the drain records a *failure packet* and a *fingerprint* before any routing:

- the failing stage (admission, child launch, lane work, gate, landing);
- the child's final error lines, quoted verbatim from the child log, and the log's path;
- exit code, child duration, and whether the lane produced a candidate or any judged tree;
- an **inputs hash** *(coined here)*: a hash over the lane model string, the provider, the names (never values) of relevant environment keys present or absent, the item's brief revision, the base tip and the gate's identity;
- the **fingerprint** *(coined here)*: a hash over the failing stage and the normalized error lines, with volatile substrings (timestamps, run ids, ports, paths containing uuids) stripped first. Two runs whose errors differ only in when they happened have the same fingerprint.

Then, in order:

| # | Test | Question it asks | If yes |
| :--- | :--- | :--- | :--- |
| T1 | **Literal cause** | Does the quoted error name a precondition of running at all — provider or model resolution, authentication, a missing binary, a permission, a full disk — rather than anything about this item's work? | **Machinery fault** → diagnosis ([§4](#4-diagnosis-mechanically)). The item is charged nothing |
| T2 | **Repetition** | Has this fingerprint already occurred on this item with no change to the inputs hash since? | **Deterministic by observation** → diagnosis |
| T3 | **Fleet recurrence** | Has this fingerprint occurred on three or more distinct items in the current drain epoch? | **Common cause** → one fleet diagnosis, and admission of further lanes under the same inputs hash is suspended until the ticket clears. Items already burned are charged nothing |
| T4 | **Verdict** | Did the lane produce work that a gate or judge assessed and refused? | **A verdict about the work** → the existing path for a judged refusal. Nothing here changes that path |

- **T1 is a quotation table, not an inference engine.** Its patterns are literal error strings emitted by our own tools. A row is added only by quoting a real child log, and names the component that emitted it. A row that needs interpreting what an error probably means does not belong; that failure falls through to T2 and T3, which settle it with evidence.
- **T2 and T3 make determinism a measurement.** "Transient" and "deterministic" are never flags a classifier sets. A failure is deterministic once the system has observed it repeat under unchanged inputs, or strike items that share nothing but the machinery.
- **The T3 suspension is keyed on the inputs hash, not a timer.** It lifts when the ticket records a changed input ([§4.2](#42-the-product-a-cause-statement-with-a-clearing-condition)).

### 3.2 The unknown case, and the one permitted retry

A first failure, on one item, whose text names no literal machinery cause and which is not a verdict, is *unknown*.

**No automatic retry is permitted.** The ruling on [`OQ-1`](#OQ-1) is that a worker run's failure is never counted and never re-run as a test: it goes back to its own session with its evidence, and only an identical repeat goes to the design seat. The identical-repeat comparison is test T2 performed on the item's recorded endings.

> [!WARNING]
> A delayed re-run proves nothing. If it succeeds, nothing distinguishes "the fault cleared in the interval" from "the fault is intermittent", so a wait before a re-run is the cooldown again.

### 3.3 The one place a wait remains — stated by the failure, verified on return

A wait is permitted only where **the failure itself states a clearing condition the loop can check**: a `Retry-After` value, a quota reset timestamp quoted by the provider, a lock held by a named live holder. Then:

1. the wait's duration is the one the source stated;
2. re-admission is by checking the stated condition (the reset time has passed, the lock probe succeeds), recorded on the run;
3. if the same fingerprint returns after the stated condition verified clear, the source's claim is falsified and the failure is diagnosed ([§4](#4-diagnosis-mechanically)), not waited out a second time on the same say-so.

A stated duration with nothing to check ("retry later") is treated as unstated and diagnosed. The quota skip is this rule: a backend is skipped until the reset time its own stream stated.

### 3.4 Admission, restated

An item whose last run failed is admissible only if one of these holds:

- a named change has happened since that failure, in a field the diagnosis ticket named as the clearing condition ([§4.2](#42-the-product-a-cause-statement-with-a-clearing-condition));
- a stated condition of [§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return) was checked and found clear.

Every other failed item is held, visibly, blocked by its fingerprint's ticket. Held is a state with a reason and an owner ([principle 7](landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)), not a countdown.

## 4. Diagnosis, mechanically

Diagnosis is a bounded piece of work with a defined input, product and failure mode. It is **cause, not fix**: a diagnosis that applied its own fix would be a repair lane with no verdict on it, and most deterministic causes (configuration, credentials, model strings) are not a lane's to change.

### 4.1 The three tiers, cheapest first

- **Tier 0 — the drain's own probes, no agent.** Before any lane is admitted under an inputs hash the drain has not seen succeed, and whenever T3 trips, the drain checks the preconditions a lane needs, once per inputs hash: does the lane model string resolve, is the provider credential present in the environment the child will receive, do the tools a lane shells out to exist. A tier-0 failure is a complete diagnosis, since the probe's output is the cause. Which probe commands are used is the implementer's choice.
- **Tier 1 — a diagnosis run.** Where tier 0 passes or does not cover the failure, one run is dispatched per fingerprint, never per item. Its brief is the failure packet, the affected items and the tier-0 results, and its product is a cause, not a change: it may reproduce the failure, and it may not land a fix. The loop dispatches it on the design seat and reports its cost per failure ([`OQ-2`](#OQ-2)).
- **Tier 2 — the ladder.** Where tier 1 cannot conclude, or concludes that the remedy is a policy or scope decision, the ticket escalates ([principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop)) with the packet and the diagnosis so far.

### 4.2 The product: a cause statement with a clearing condition

A diagnosis is complete only when the fingerprint's ticket records:

| Field | Content |
| :--- | :--- |
| `cause` | One claim, in plain words: what is broken |
| `evidence` | The quoted lines or probe outputs the claim rests on |
| `scope` | Which inputs-hash fields the cause implicates, which tells [§3.4](#34-admission-restated) what "changed" means |
| `clearing condition` | A checkable statement of what must be true before a re-run is meaningful. "Wait a while" is not admissible; a [§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return) stated condition is |
| `fix route` | The item, configuration change or owner decision that would produce the clearing condition — filed or referenced, not performed by the diagnosis |

When the clearing condition verifies, the ticket closes and every item blocked by it becomes admissible; their re-run confirms the diagnosis. If the same fingerprint returns, the diagnosis was wrong and the ticket re-opens with that falsification attached. The loop is bounded by the ladder, not by a counter.

### 4.3 When diagnosis itself cannot run

The bootstrap case is a fault in the lane configuration itself: a diagnosis dispatched under that configuration fails the same way. Four rules:

1. **Tier 0 has no agent in it.** A broken lane-model configuration cannot prevent its own tier-0 diagnosis.
2. **A tier-1 diagnosis never runs under the inputs hash it is diagnosing.** It runs under a known-good configuration, recorded on the ticket.
3. **A diagnosis that fails with the fingerprint it was sent to diagnose escalates immediately** to tier 2. There is no second diagnosis run, and a failed diagnosis never falls back to retrying or benching the blocked items.
4. **Fail loud.** If no tier can run, the ticket is open, its items are visibly blocked by it, and the drain's output names the fingerprint, the items held and "undiagnosed".

## 5. Worked replay: tonight under this design

An example: the lane model string is set to a value the adapter cannot resolve, and every lane dies seconds after launch with the same credential error.

| Under a cooldown | Under this design |
| :--- | :--- |
| Lanes are admitted against the unresolvable model | The tier-0 probe under the new inputs hash fails. Zero items are admitted; one ticket, cause stated by the probe |
| *(Without tier 0)* the first three items die with the same error and nothing connects them | The fingerprints match; T3 trips on the third distinct item. Admission under that inputs hash is suspended and the three items are charged nothing |
| Each item spends its retry budget on a provider failure and is benched | No budget exists; T1 routes the literal error to diagnosis uncharged |
| Holds expire, an item is re-admitted, dies in seconds and is benched again | Nothing expires. The item is blocked by the fleet ticket until the model string changes and a tier-0 probe passes under the new hash |
| The model string is corrected, and children still report the missing credential | The inputs hash changed, so the tier-0 probe re-runs in the child's actual environment. If it fails there, the ticket reads "resolves when run by hand, fails in the child's environment" and the items stay held |

## 6. The hold is kept; its clock is not

A durable, restart-proof hold is kept: without it a restarted drain re-admits a failed item at once. Its stored condition is a ticket reference. Keeping a cooldown "only for genuinely transient failures" is rejected: there is no way to know in advance that a failure is transient.

An item found held by a stored timestamp is never admitted by that timestamp passing. It is converted once: its last failed run is fingerprinted from the stored child log, it is set blocked by its fingerprint's ticket, and any budget charged for a machinery failure is restored per principle 5. An item whose last failure is a T4 verdict is not converted and goes to the ladder. The conversion lists each item's disposition.

## 7. The principle

The ruling is recorded as [landing principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), "A failure is diagnosed, not waited out".

## 8. What this does not propose

- **No transient/deterministic classifier.** No heuristic, model call or tip comparison labels a failure in advance.
- **No automatic retry**, immediate or delayed.
- **No new ticket kind, failure vocabulary or escalation path.** New machinery is confined to the fingerprint, the inputs hash, the admission rule and the fleet trip.

## Open Questions

None open. The three questions this design raised are ruled, and their rulings are rows [`OQ-1`](#OQ-1) to [`OQ-3`](#OQ-3) of the [Decision Ledger](#decision-ledger).

## Decision Ledger

The `Built` column is filled from the tree; [What is built](#what-is-built) names the symbols.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D1 | Retry, retry budget, cooldown and expiring bench are one mechanism, and the replacement removes the class. A durable hold is kept; its clock is not | 2026-11-03 | [§1](#1-what-the-cooldown-actually-was), [§6](#6-the-hold-is-kept-its-clock-is-not) | Partly: no worker run is held on a clock |
| D2 | Determinism is established by observation, never classified in advance: a literal machinery cause (T1), the same fingerprint under an unchanged inputs hash (T2), the same fingerprint across three or more distinct items (T3). Judged failures (T4) stay on the existing path | 2026-11-03 | [§3.1](#31-the-four-tests) | Partly: the identical-repeat comparison; T1 and T3 are not built |
| D3 | Admission after failure is keyed on a named change or a checked clearing condition, never on elapsed time. The retry budget is deleted | 2026-11-03 | [§3.4](#34-admission-restated) | Partly: the worker-run path |
| D4 | A wait survives only where the failure states a clearing condition; re-admission checks the condition, and a recurrence after a verified-clear condition goes to diagnosis | 2026-11-03 | [§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return) | Partly: the quota skip |
| D5 | Diagnosis is cause, not fix, in three tiers. A diagnosis never runs under the configuration it diagnoses, and its failure escalates | 2026-11-03 | [§4](#4-diagnosis-mechanically) | — |
| D6 | Destinations are existing machinery: principle 9's deduped ticket keyed by fingerprint, principle 6's ladder, principle 1's vocabulary | 2026-11-03 | [§2](#2-what-already-does-this-job) | — |
| D7 | An item held by a clock is converted to blocked by its fingerprint's ticket, with mischarged budget restored; the conversion lists every item's disposition | 2026-11-03 | [§6](#6-the-hold-is-kept-its-clock-is-not) | — |
| D8 | The ruling is recorded as landing principle 11 | 2026-11-03 | [§7](#7-the-principle) | ✅ |
| <a id="OQ-1"></a>[OQ-1](#OQ-1) | No automatic retry-as-test. A worker run's failure is never counted; it goes back to its own session with its evidence, and only an identical repeat goes to the design seat (owner ruling, `D-JL10`) | 2026-11-17 | [§3.2](#32-the-unknown-case-and-the-one-permitted-retry) | ✅ |
| <a id="OQ-2"></a>[OQ-2](#OQ-2) | The loop dispatches the diagnosis itself, on the design seat, and reports its cost per failure (designer ruling on recorded principle; the owner may overturn it) | 2026-11-16 | [§4.1](#41-the-three-tiers-cheapest-first) | Partly: an identical repeat goes to the design seat |
| <a id="OQ-3"></a>[OQ-3](#OQ-3) | A wait exists only where the loop can check that the condition has cleared (designer ruling on recorded principle; the owner may overturn it) | 2026-11-16 | [§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return) | Partly: the quota skip |
