---
title: "A failure is diagnosed, not waited out — removing the cooldown"
status: in-review
stage: DESIGN
verified: 2026-11-03
next: "Build what landing principle 11 names as not enforced: the fleet trip, the tier-0 probes and the fingerprint store."
tags: [tillr, drain, failure-handling, diagnosis, principles]
summary: "The 30-minute cooldown is removed, not retuned. In its place: a failure is fingerprinted from its own text, repetition under unchanged inputs or recurrence across the fleet establishes determinism empirically, and a deterministic failure blocks its items on one deduped diagnosis ticket per fingerprint. Time passing is never again a reason to re-run anything; re-admission requires a named change. The ruling is recorded as landing principle 11 ([§7](#7-the-principle))."
---

# A failure is diagnosed, not waited out

**Status:** Design. The rule is recorded as [landing principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), whose *Enforced by* paragraph states what `tillr-drain` enforces and what it does not; this document is the specification of the whole mechanism. The three open questions are settled ([Open Questions](#open-questions)).

> **In short.** The owner is right, and the reason is sharper than "cooldowns are lazy." A retry, a retry budget, a cooldown and a `retry-exhausted` bench are four spellings of one mechanism: *spend another run and hope the world changed*. None of them names what will be different on the next run, because nothing checks. This design replaces all four with a single admission rule — **nothing re-runs until a named input has changed, or a clearing condition stated by the failure itself has been verified** — and a single destination for failures that cannot clear: one diagnosis ticket per failure fingerprint, with the affected items visibly blocked on it. Determinism is never guessed by a classifier; it is established three empirical ways, each of which reads evidence the system already produces.

**Why it matters.** On 2026-11-03 the cooldown converted a permanent configuration error — a lane model string, `openrouter/deepseek/deepseek-v4.1-flash-1017:morph`, that pi cannot resolve because `morph` is a routing-order value, not a model — into repeated 30-minute waits. 55 items were benched `retry-exhausted`; on expiry they were re-admitted and died 8 seconds after launch (`drain.admit item=487` 17:53:37, `drain.child-exit item=487 exit=1` 17:53:45, child log: `No API key found for openrouter`), then were benched again. 383 of 1,083 child logs carry that one error. The mechanism worked exactly as designed; the design was the defect.

**The shape.** Fingerprint every failure from its own text. Route it by three tests that measure, not infer: a literal machinery cause quoted in the failure, the same fingerprint recurring under unchanged inputs, or the same fingerprint recurring across distinct items. Any of the three makes the failure deterministic, and a deterministic failure is diagnosed — one deduped ticket per fingerprint carrying the failure packet, its items blocked-by that ticket and charged nothing. A wait survives in exactly one place: where the failure itself states a clearing condition and a time (`Retry-After`, a quota reset, a named lock holder), the system waits for *that*, stated by the source, and verifies it cleared. Elapsed time on its own admits nothing, ever.

**Cost.** One fingerprint computation per failed run, one ticket per distinct failure rather than one benching per item, and the loss of a comfortable fiction: some failures that a cooldown would have quietly recycled will now sit visibly blocked until someone diagnoses them. That visibility is the point.

**Needs your ruling:** None.

**Reads with:** [`landing-principles.md`](landing-principles.md) (principles 1, 4, 5, 6, 7 and 9 do most of this design's work; [§7](#7-the-principle) names the principle this design is recorded as), [`escalation-ladder.md`](escalation-ladder.md) (the destination for what diagnosis cannot settle).

---

## 1. What the cooldown actually was

The cooldown persisted a wall-clock bench: a lane that failed and spent its retry budget got `CooldownUntil = last failure end + 30m`, stored so a drain restart could not reset it. The persistence was the good half of that ruling and it worked. The bad half was the theory of recovery. Stated plainly, the cooldown's theory was:

> The failure might be transient. If we wait long enough, it might not be there.

Nothing in the mechanism could distinguish that case from its opposite, because nothing ever looked at the failure. The owner's word — "prayer" — is exact, and it names the general form, which [§7](#7-the-principle) rules against: **any mechanism whose only theory of recovery is that time passed or that repetition might differ.** The retry budget spent runs on the same hope, faster. `retry-exhausted` was the same hope, parked. Principle 8's measurement records a fourth spelling: a batch that "re-armed on a five-minute clock" instead of isolating the offender. Four mechanisms, one defect: each consumes a run, a budget, or an item's evening without producing a single new fact about why the work failed.

The human the owner describes does the opposite. A human reads the error, forms a claim about the cause, and only re-runs when they have changed something that bears on that cause — or when they re-run *deliberately, once, to find out* whether the failure repeats. Both of those acts produce information. A cooldown produces none; it is a re-run with the information removed and a delay added.

## 2. What already does this job

The design is built from parts the landing principles already rule, and adds three of its own:

| Existing mechanism | What it already does | What this design takes from it |
| :--- | :--- | :--- |
| **The charging rule** ([principle 5](landing-principles.md#5-only-verdicts-charge)) | A judged refusal about the work charges an attempt; words that say nothing about the work charge nothing | It is the destination for failures that are verdicts about the work ([§3](#3-the-decision-procedure--read-the-failure-do-not-classify-it), test T4). This design adds nothing there |
| **The deduped ticket** ([principle 9](landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work)) | The ruled destination for "the queue cannot proceed for a reason not about the candidate" is one deduped ticket, executed separately from the work it blocks | It is the destination for a deterministic failure ([§4](#4-diagnosis-mechanically)). This design does not invent a new ticket kind; it gives the ticket a dedupe key (the fingerprint) and a second producer (lane failure, beside landing failure) |
| **The escalation ladder** ([principle 6](landing-principles.md#6-a-budget-escalates-it-does-not-loop), [`escalation-ladder.md`](escalation-ladder.md)) | A spent budget flows up rungs to a recorded exception; the last rung is an explicit "on hold", never a silent stall | It is the destination when diagnosis itself cannot run or cannot conclude ([§4.3](#43-when-diagnosis-itself-cannot-run)) — visible, recorded, owner-reachable. Never a return to the bench |
| **The refusal vocabulary** ([principle 1](landing-principles.md#1-a-refusal-must-be-a-verdict)) | Machinery statements are separated from candidate verdicts | This design reuses that separation at lane-failure time and **no new failure words are added beside it** |
| **A durable hold** | A restart-proof "this item is held, until a stored condition" record, honored at admission | The persistence and the admission check are kept; the *condition* stored is a ticket reference, never a timestamp ([§6](#6-the-hold-is-kept-its-clock-is-not)) |

What is new in this design is only three things: a **failure fingerprint**, an **admission rule keyed on changed inputs rather than elapsed time**, and a **fleet trip** that stops a common-cause failure from burning items one at a time.

## 3. The decision procedure — read the failure, do not classify it

The owner's rejection is the constraint here: a discriminator that *guesses* transient-or-deterministic (the proposed last-green-tip test) invents machinery where the error message already answers. So the procedure below contains no classifier in that sense. It contains one extraction and four tests, in a fixed order. Three of the tests read only evidence the system itself produced — the failure's own quoted text, the failure's own recurrence, the failure's own spread across items — and the fourth is the verdict test principle 5 already applies. A failure that passes none of them is handled by [§3.2](#32-the-unknown-case-and-the-one-permitted-retry), which treats *unknown* as a reason to gather one fact, not as a licence to hope.

### 3.1 The four tests

**Extraction (always, first).** On every failed run, the drain records a *failure packet* and a *fingerprint* before any routing happens:

- the failing stage (admission, child launch, lane work, gate, landing);
- the child's final error lines, quoted verbatim from the child log, and the child log's path;
- exit code, child duration, and whether the lane produced a candidate or any judged tree;
- an *inputs hash*: the lane model string, provider, the names (never values) of relevant environment keys present or absent, the item's brief revision, the base tip, and the gate's identity;
- the *fingerprint*: a hash over the failing stage and the normalized error lines (volatile substrings — timestamps, run ids, ports, paths containing uuids — stripped before hashing). Two runs whose errors differ only in when they happened have the same fingerprint; that is the point.

Then, in order:

| # | Test | Question it asks | If yes |
| :--- | :--- | :--- | :--- |
| T1 | **Literal cause** | Does the quoted error name a precondition of running at all — provider or model resolution, authentication, a missing binary or tool, a permission, a full disk — rather than anything about this item's work? | **Machinery fault** → diagnosis ([§4](#4-diagnosis-mechanically)). The item is charged nothing (principles 1 and 5: the failure cannot name what the item did wrong) |
| T2 | **Repetition** | Has this fingerprint already occurred on this item with *no change* to the inputs hash since? | **Empirically deterministic** → diagnosis ([§4](#4-diagnosis-mechanically)). No interpretation of the error text was needed: the same inputs produced the same failure, so a third run is not a remedy, it is a repeat |
| T3 | **Fleet recurrence** | Has this fingerprint occurred on three or more *distinct items* in the current drain epoch, whatever the text says? | **Common cause** → one fleet diagnosis ([§4](#4-diagnosis-mechanically)), and admission of further lanes that would run under the same inputs hash is suspended until the ticket clears. Items already burned are charged nothing and retroactively un-benched |
| T4 | **Verdict** | Did the lane produce work that a gate or judge actually assessed and refused? | **A verdict about the work** → the existing path for a judged refusal, charged per distinct cause (principle 5), escalating per principle 6. Nothing in this design changes that path |

Three properties worth stating:

- **T1 is a quotation table, not an inference engine.** Its patterns are literal error strings, or tight literal prefixes, emitted by our own tools (`No API key found for <provider>` is the founding entry, from 2026-11-03). A row may be added only by quoting a real child log, and each row names the component that emitted it. The moment a row requires interpreting what an error *probably means*, it does not belong in T1 — that failure falls through to T2/T3, which settle the question with evidence instead of interpretation. The table is allowed to be small and incomplete precisely because falling through is safe.
- **T2 and T3 make determinism a measurement, not a label.** "Transient" and "deterministic" never appear as flags a classifier sets. A failure *is* deterministic at the moment the system has observed it repeat under unchanged inputs, or observed it strike items that share nothing but the machinery. Before that moment the honest word is *unknown* ([§3.2](#32-the-unknown-case-and-the-one-permitted-retry)); after it, no test may route the failure back to a retry while the inputs hash is unchanged.
- **T3 is the test 2026-11-03 needed.** Any single `No API key` could be argued about. The same 8-second death across items that share no brief, no branch and no files admits of exactly one reading — the common factor is the drain's own configuration — and T3 reaches it after three items, not after 383 child logs. The admission suspension is keyed on the inputs hash, not on a timer: it lifts when the ticket records a changed input ([§4.2](#42-the-product-a-cause-statement-with-a-clearing-condition)), and not before.

### 3.2 The unknown case, and the one permitted retry

A first-ever failure, on one item, whose text names no literal machinery cause and which is not a verdict: the system knows nothing yet, and this design's answer is to learn one thing cheaply rather than to wait or to guess.

**One immediate retry is permitted, explicitly as a measurement** — recorded on the run as a retry-as-test, uncharged, launched without delay, whose entire purpose is to run T2. If the same fingerprint returns, determinism is established by observation and [§4](#4-diagnosis-mechanically) takes over. If the lane succeeds, transience is established by observation, the fingerprint is recorded as observed-transient *under that inputs hash*, and the work proceeds. Either outcome is a fact the system did not have; that is what separates this retry from the budget it replaces. The retry budget is deleted: there is no number K of retries, because a second failure has already answered the only question a retry can answer, and a third run under unchanged inputs can add nothing but cost.

Two consequences of framing retry as measurement:

- **Immediacy is mandatory, not incidental.** A delay before the retry-as-test would *destroy* its evidentiary value: if a delayed retry succeeds, nothing distinguishes "the fault cleared in the interval" from "the fault is intermittent at any timescale" — and the system is back to prayer with extra steps. Run at once, the test cleanly separates *repeats under unchanged inputs* from *does not repeat*.
- **The observed-transient record is narrow.** It says: this fingerprint, under this inputs hash, once failed and then succeeded with nothing changed. It licenses future first-failures of that fingerprint to take the retry-as-test directly. It never licenses a wait, and it is voided by T3: a fingerprint that starts striking the fleet is a common cause however it behaved before.

Whether even this one retry survives is [OQ-1](#open-questions). What its two options share: in neither of them does anything wait, and in neither does anything run a third time on unchanged inputs.

### 3.3 The one place a wait remains — stated by the failure, verified on return

Time is not abolished from failure handling; it is demoted from a mechanism to a quotation. A wait is permitted only where **the failure itself states a clearing condition in time**: a `Retry-After` value, a rate-limit or quota reset timestamp quoted by the provider, a lock held by a named, live holder whose release is the condition. Then:

1. the wait's duration is the one the source stated — never a round number of ours;
2. re-admission is by *checking the stated condition* (the reset time has passed, the lock probe succeeds), recorded on the run;
3. if the same fingerprint returns after the stated condition verified clear, the source's claim is falsified and the failure is diagnosed ([§4](#4-diagnosis-mechanically)), not waited out a second time on the same say-so.

This is the whole of what survives of "keep the cooldown for genuinely transient failures": not a class of failures we judge transient, but a class of failures that *tell us* what would clear them, in writing, in the failure itself. Which sources are trusted to state a wait is [OQ-3](#open-questions).

### 3.4 Admission, restated

The drain's admission rule, replacing expiry, in one paragraph a builder can lift: *an item whose last run failed is admissible only if (a) it has never failed with its current failure's fingerprint before — the [§3.2](#32-the-unknown-case-and-the-one-permitted-retry) test — or (b) the inputs hash has changed since that failure in a field the diagnosis ticket named as the clearing condition ([§4.2](#42-the-product-a-cause-statement-with-a-clearing-condition)), or (c) a [§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return) stated condition was checked and found clear. Every other previously-failed item is held, visibly, blocked-by its fingerprint's ticket. Held is a state with a reason and an owner (principle 7), not a countdown.*

## 4. Diagnosis, mechanically

"Go find the error" needs a mechanical meaning, or it becomes the next hope. Diagnosis here is a bounded piece of work with a defined input, a defined product, and a defined failure mode. It is deliberately **cause, not fix**: a diagnosis that also applied its own fix would be a repair lane with no verdict on it, and most deterministic causes (configuration, credentials, model strings) are not a lane's to change in the first place. Principle 9 already separates the two — ticket first, mechanic lane second — and this design keeps the separation.

### 4.1 The three tiers, cheapest first

**Tier 0 — the drain's own probes, no agent.** Before any lane is admitted under an inputs hash the drain has not yet seen succeed, and again whenever T3 trips, the drain runs the preconditions a lane needs as direct checks, once per inputs hash rather than once per burned item: does the configured lane model string resolve to a model pi can bind; is the credential for that provider present in the environment the child will actually receive; do the tools a lane shells out to exist. These are yes/no questions about the drain's own configuration, answerable by the drain process itself without dispatching anything. The 2026-11-03 failure dies entirely at this tier: `:morph` does not resolve, zero items are admitted, one ticket is filed quoting the resolution failure. (Which specific probe commands a builder uses is a plan question; the design fixes only that the probe runs in the drain, per inputs hash, before admission — and that a tier-0 failure is already a complete diagnosis, since the probe's output *is* the cause.)

**Tier 1 — a diagnosis lane.** Where tier 0 passes or does not cover the failure, one lane is dispatched per fingerprint — never per item — whose brief is the [§3.1](#31-the-four-tests) failure packet: the quoted error, the child log path, the inputs hash and its fields, the count and identity of affected items, the tier-0 probe results, and the explicit instruction that its product is a cause, not a change. Its worktree and tools are read-and-probe: it may reproduce the failure in miniature (run the child command with the same model string, read the configuration the drain read), and it may not land a fix. One lane per fingerprint also bounds cost by construction: a fleet-wide failure costs one diagnosis, however many items it touched.

**Tier 2 — the ladder.** Where tier 1 cannot conclude, or concludes "cause known, remedy is a policy or scope decision", the ticket escalates (principle 6) with the packet and the diagnosis-so-far attached. This tier is not a bigger diagnosis; it is the admission that the remaining question is not diagnostic.

### 4.2 The product: a cause statement with a clearing condition

A diagnosis is complete only when it records, on the fingerprint's ticket:

| Field | Content |
| :--- | :--- |
| `cause` | One claim, in plain words: what is broken |
| `evidence` | The quoted lines or probe outputs the claim rests on — pointers into child logs and drain records, never a paraphrase of them |
| `scope` | Which inputs-hash fields the cause implicates (this item's brief / the lane model / the provider credential / the gate), which is what tells [§3.4](#34-admission-restated) what "changed" means for re-admission |
| `clearing condition` | A checkable statement of what must be true before a re-run is meaningful: a corrected model string *plus* a tier-0 probe passing under it, a credential present in the child's environment, a named file changed. "Wait a while" is not an admissible clearing condition; a [§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return) stated condition is |
| `fix route` | The mechanic-lane item, configuration change, or owner decision that would produce the clearing condition — filed or referenced, not performed by the diagnosis lane |

When a fix lands and the clearing condition verifies (tier 0 re-run under the new inputs hash, recorded), the ticket closes, every item blocked-by it becomes admissible under [§3.4](#34-admission-restated)(b), and their re-run is the *confirmation* of the diagnosis. If the same fingerprint returns anyway, the diagnosis was wrong, the ticket re-opens with that falsification attached, and the second diagnosis starts from a better packet than the first. That loop is bounded by the ladder ([§4.3](#43-when-diagnosis-itself-cannot-run)), not by a retry counter.

### 4.3 When diagnosis itself cannot run

This is the bootstrap case, and 2026-11-03 is its example: the failure was in the lane model configuration itself, so a diagnosis lane dispatched as an ordinary lane, under the same configuration, would have died in the same 8 seconds — and, under a cooldown, been benched for its trouble. Four rules, in order:

1. **Tier 0 has no agent in it, on purpose.** The most common deterministic failures are the drain's own preconditions, and the drain can state them without anything that needs a model. A broken lane-model configuration cannot prevent its own tier-0 diagnosis.
2. **A tier-1 diagnosis lane never runs under the inputs hash it is diagnosing.** It runs under a known-good configuration, recorded on the ticket. Diagnosing a configuration from inside that configuration is how 2026-11-03 would have reproduced itself one level up.
3. **A diagnosis lane that fails with the fingerprint it was sent to diagnose escalates immediately.** That outcome is itself the diagnosis at fleet scope: the fault reaches beyond lane configuration. It goes to the ladder ([§4.1](#41-the-three-tiers-cheapest-first) tier 2) at once. There is no second diagnosis lane, and under no circumstance does a failed diagnosis fall back to retrying or benching the blocked items — the failure of the finder is never treated as evidence the finding wasn't needed.
4. **Fail loud, not quiet.** If no tier can run, the ticket is open, its items are visibly blocked-by it on the health surface (principle 7), and the drain's own output says so by name — fingerprint, items held, "undiagnosed". The banned outcome is the silent one: the cooldown's great virtue, from its own point of view, was that an evening of failure looked like a queue patiently waiting. Whatever replaces it must make an undiagnosed fleet failure *more* visible on hour two than on minute two, exactly the opposite of a bench quietly expiring and re-arming.

## 5. Worked replay: tonight under this design

"Tonight" is the evening of 2026-11-03, the run measured in **Why it matters** above.

| When | What happened | What this design does instead |
| :--- | :--- | :--- |
| Drain start, model first set to `...:morph` | Lanes admitted against an unresolvable model | Tier-0 probe under the new inputs hash: model does not resolve. Zero items admitted. One ticket, cause already stated by the probe |
| *(Had tier 0 not existed)* first three items die in ~8s, same error | Nothing connects the three deaths | Fingerprints match; T3 trips on the third distinct item. Admission under that inputs hash suspended; the three items un-charged; one fleet ticket |
| Items exhaust retry budgets over the evening | 55 benchings, each spending budget on provider failures (principle 5's exact measurement, repeated) | No budget exists to exhaust on a machinery fault; T1 routes the literal `No API key…` to diagnosis uncharged |
| Cooldowns expire, item 487 re-admitted 17:53:37, dead 17:53:45, re-benched | The evening's headline event | Nothing expires. Item 487 sits blocked-by the fleet ticket, named as held, until the model string changes and a tier-0 probe passes under the new hash |
| The operator changes the model to `:floor`; drain children still report the missing key | A loose thread chased by hand, separately | The inputs hash changed, so the tier-0 probe re-runs *in the child's actual environment*. If it fails there, the probe output joins the ticket as fresh evidence — the ticket now reads "resolves when run by hand, fails in the child's environment", which is the diagnosis's next question already framed — and items stay held rather than discovering it 55 more times |

## 6. The hold is kept; its clock is not

**Ruling: keep the persistence, delete the clock.** A durable, restart-proof hold is infrastructure this design needs: without it a restarted drain re-admits a failed item at once. Keeping a cooldown "only for genuinely transient failures" is rejected: it preserves the mechanism and merely argues about its inputs, and there is no legitimate way to know a failure is genuinely transient in advance ([§3.2](#32-the-unknown-case-and-the-one-permitted-retry) is how the system *finds out*).

An item held by a stored timestamp is therefore never admitted by that timestamp passing. It is converted, once: its last failed run is fingerprinted from the stored child log; items are grouped by fingerprint; each is set blocked-by its fingerprint's diagnosis ticket; and any budget those runs charged for a machinery failure is restored, per principle 5, by a recorded recomputation over quoted run records. Items whose last failure is a T4 verdict with charged attempts are not converted; they go to the ladder as principle 6 prescribes. The conversion lists, per item, which way it went and why, so a misroute is auditable.

Measured 2026-11-03: of the 55 items benched that evening, none needed a re-run to be disposed of. They shared one fingerprint, and the cause was already known.

## 7. The principle

The ruling is recorded as [landing principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), "A failure is diagnosed, not waited out". Its *Enforced by* paragraph is the current statement of what `tillr-drain` enforces; this document is the specification it cites.

## 8. What this does not propose

- **No transient/deterministic classifier.** No heuristic, model call or tip comparison labels a failure in advance. The three tests read quotations and recurrences; everything else is *unknown* and is treated as a question to answer, not a class to assign.
- **No abolition of retry.** Section 3.2 keeps exactly one, redefined as measurement — immediate, uncharged, single. [OQ-1](#open-questions) puts even that one to the owner.
- **No new ticket kind, failure vocabulary, or escalation path.** Destination, words and ladder are principle 9's, principle 1's and principle 6's. New machinery is confined to fingerprint, inputs hash, admission rule, fleet trip.

## Open Questions

1. ✅ **OQ-1: Does the single immediate retry-as-test ([§3.2](#32-the-unknown-case-and-the-one-permitted-retry)) survive, or does every first failure go straight to diagnosis?**

   A lane fails for the first time, on one item, with an error that names no machinery cause. This design re-runs it once, immediately, explicitly to learn whether the failure repeats under unchanged inputs — the cheapest fact in the system, and the one that separates "diagnose this" from "the world hiccuped". The owner's model can be read more strictly: a human told to "go find the error" who simply presses the button again has not found anything. Against the strict reading: the retry-as-test is not a hope that it works this time, it is an experiment whose *failure* is the informative outcome, and sending every first failure to a diagnosis lane spends a lane to learn what one re-run settles. For the strict reading: any retained retry is a foothold from which budgets and benches regrow, and the fleet trip already catches common causes without it.

   - **A — One immediate retry-as-test, recorded as such, then never again on unchanged inputs.** Determinism established by observation at the cost of at most one extra run per genuinely new failure.
   - **B — No retry at all; first failures are diagnosed.** Simpler rule, no foothold. Genuinely transient faults (a network drop mid-lane) pay a diagnosis lane to conclude "did not recur on reproduction" — which is also a perfectly good diagnosis.

   _Settled:_ Neither, for a worker run. The owner's ruling of 2026-11-17 (`D-JL10` in [the judge and lifecycle design](tillr-judge-and-lifecycle.md#decision-ledger)) governs: a worker run's failure is never counted, it goes back to its own session with its evidence every time, nothing re-runs because an input changed, and only an identical repeat goes to the design seat. No automatic retry-as-test stands.

   _Leaning:_ A — one retry, as a named measurement, immediate and uncharged. It is the human's own first diagnostic act, and its result is recorded either way; B spends a lane to learn the same fact more slowly.

   **Answer:**
   > _(empty — fill in when decided)_

2. ✅ **OQ-2: Under whose configuration does the tier-1 diagnosis lane run, and who dispatches it once automated?**

   Section 4.3 forbids diagnosing a configuration from inside itself, so the diagnosis lane needs a known-good configuration — in practice the operating session's model and environment, which are neither the cheapest nor necessarily the ones the drain can reach. There is also a dispatch question underneath: dispatching it by hand needs no new authority, and automating it means the drain acquires a second launch path (lanes for work, lanes for diagnosis) with different configuration, budget and authority. Both halves touch cost and trust: a diagnosis lane at the operating session's prices on every new fingerprint is a standing expense the owner should size deliberately, and a drain that can launch lanes under that configuration unaudited is a privilege decision, not an implementation detail.

   - **A — Diagnosis lanes run on the operating session's configuration, dispatched by the drain, cost reported per ticket.** The bootstrap is fully closed; the expense is visible per fingerprint, where it can be judged against the items it unblocked.
   - **B — Diagnosis lanes are always dispatched by hand from the ticket, never drain-dispatched.** No new launch authority; a fleet failure at 3 a.m. waits, visibly, for a person to pick up its ticket — held and loud ([§4.3](#43-when-diagnosis-itself-cannot-run) rule 4), but held.

   _Settled:_ A, in tillr's terms. The loop dispatches the diagnosis itself, on the design seat, and reports its cost per failure. The owner ruled on 2026-11-15 that the loop does not wait for a person and that a refusal goes to a seat that can answer it. Settled on 2026-11-16 by the designer on recorded principle, at the owner's request; the owner may overturn it.

   _Leaning:_ B first, A once ticket volume and cost per diagnosis are measured over real incidents. Launch authority for the drain is cheap to grant later and awkward to revoke.

   **Answer:**
   > _(empty — fill in when decided)_

3. ✅ **OQ-3: Which failure sources are trusted to state a wait ([§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return))?**

   Section 3.3 permits a wait only where the failure itself states the clearing condition and its time — `Retry-After`, a provider's quota-reset timestamp, a named live lock holder. Someone must rule which sources count, because "the failure said to wait" can quietly reintroduce the cooldown through any error message generous with durations: a provider saying "retry later" with no checkable condition is the 30-minute bench wearing a citation. The strict rule admits only conditions the drain can *verify clear* before re-admission (a timestamp now passed, a lock probe now succeeding); anything unverifiable is treated as unstated and routed to diagnosis. The looser rule additionally trusts a stated duration from the lane's own provider, checked only by the clock.

   - **A — Verifiable conditions only.** A wait exists only where re-admission can check the condition, not merely the time. "Retry later" with nothing to check is diagnosed like any other failure.
   - **B — Provider-stated durations also trusted.** Fewer spurious diagnoses for rate limits; reopens a door whose width depends on provider error-text discipline we do not control.

   _Settled:_ A. A wait exists only where the loop can check the condition has cleared: a reset time that has passed, a lock that is free. The quota skip is this rule: a backend is skipped until the reset time its own stream stated. Settled on 2026-11-16 by the designer on recorded principle, at the owner's request; the owner may overturn it.

   _Leaning:_ A — verifiable only. The cooldown is exactly what a duration trusted on say-so becomes after one inattentive quarter; a condition that can be checked can also be falsified, which is the whole design.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

The owner's rejection of the cooldown is the input to this design, not its output; the rows below are this design's own settled positions beyond that rejection, recorded so a ruling can overturn them by id. What is built is stated once, in the *Enforced by* paragraph of [landing principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D1 | The defect is general: retry, retry budget, cooldown and expiring bench are one mechanism ("spend a run and hope"), and the replacement must remove the class, not the instance. A durable hold is kept; its clock is not | 2026-11-03 | [§1](#1-what-the-cooldown-actually-was), [§6](#6-the-hold-is-kept-its-clock-is-not) | — |
| D2 | Determinism is established empirically, never classified in advance: literal machinery cause quoted in the failure (T1), same fingerprint under unchanged inputs hash (T2), same fingerprint across ≥3 distinct items (T3). Judged failures (T4) continue on the existing path for a judged refusal, untouched | 2026-11-03 | [§3.1](#31-the-four-tests) | — |
| D3 | Admission after failure is keyed on a named changed input or a checked clearing condition, never on elapsed time. The retry budget is deleted; at most one immediate, uncharged retry-as-test remains, and even that is subject to [`OQ-1`](#open-questions) | 2026-11-03 | [§3.2](#32-the-unknown-case-and-the-one-permitted-retry), [§3.4](#34-admission-restated) | — |
| D4 | A wait survives only where the failure itself states a clearing condition; the duration is the source's, re-admission checks the condition, and a recurrence after a verified-clear condition falsifies the source and routes to diagnosis | 2026-11-03 | [§3.3](#33-the-one-place-a-wait-remains--stated-by-the-failure-verified-on-return) | — |
| D5 | Diagnosis is cause, not fix: three tiers (drain's own per-inputs-hash precondition probes; one read-and-probe lane per fingerprint; the ladder), producing a recorded cause statement whose clearing condition is what re-admission checks. A diagnosis never runs under the configuration it diagnoses, and its failure escalates rather than falling back to retry or bench | 2026-11-03 | [§4](#4-diagnosis-mechanically) | — |
| D6 | Destinations are existing machinery: principle 9's deduped ticket (dedupe key = fingerprint), principle 6's ladder, principle 1's machinery-vs-verdict vocabulary. New machinery is confined to fingerprinting, the inputs hash, the admission rule, and the fleet trip | 2026-11-03 | [§2](#2-what-already-does-this-job) | — |
| D7 | Items benched by a clock are not re-run and do not expire into lanes: they are converted to blocked-by their fingerprint's ticket with mischarged budget restored per principle 5; genuinely judged budget-spent items in the residue go to the ladder, and the conversion lists every item's disposition | 2026-11-03 | [§6](#6-the-hold-is-kept-its-clock-is-not) | — |
| D8 | The ruling is recorded as landing principle 11 | 2026-11-03 | [§7](#7-the-principle) | [landing principle 11](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) |