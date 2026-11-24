---
title: "A closed backend is probed and reopened inside tillr, and only a failed probe escalates"
status: in-review
stage: DESIGN
tags: [tillr, drain, backends, alarms, watch, triage, design]
summary: "When an infrastructure failure closes a backend, the drain loop itself tests that same backend once with a minimal single-turn prompt in the backend's own adapter environment. A passing probe reopens the backend; a failing one records scrubbed evidence, names a truthful mover, and routes to a repair item, the operating session or the owner. No model fallback and no timer retry. Automatic probing is not built; the watch rule that alerts on every closure is. Three decisions are open for the owner."
next: "Rule OQ-BP1 (when a probe runs on a closure), OQ-BP2 (how the smoke check is derived) and OQ-BP3 (who receives a failed probe)."
depends-on:
  - landing-principles.md
  - operator-decisions-inside-tillr.md
  - tillr-alert-triage-seat.md
  - failure-diagnosed-not-waited-out.md
  - tillr-judge-and-lifecycle.md
  - tillr-watch.md
vantage:
  status-chip: true
---

# A closed backend is probed and reopened inside tillr, and only a failed probe escalates

**Status:** Automatic probing (`D-BP1` to `D-BP8`) is not built, and [`OQ-BP1`](#OQ-BP1) to [`OQ-BP3`](#OQ-BP3) are open. What is built is the watch rule of `D-BP9`: [`config/tillr/watch/drain-backend-closed.toml`](../../config/tillr/watch/drain-backend-closed.toml) opens a high-severity alert as soon as a backend closes. The owner's statements in [§1](#1-the-rulings-and-the-case) (`R-BP1` to `R-BP5`) are the only owner rulings; every mechanism here is the designer's decision, and the owner may overturn any of them.

> **In short.** When an infrastructure outcome closes a backend, `tillr-drain loop` tests that same backend once with a minimal single-turn prompt inside the backend's own adapter environment. If the probe passes, the drain records it and reopens the backend without waking anyone. If it fails, or a reopened backend closes again at once, the backend stays closed on an alarm, the scrubbed failure evidence is recorded, and tillr either files one repair item or escalates.

**Why it matters.** A single run that exits with an infrastructure failure closes its backend, and every seat mapped to that backend holds. In the measured case one such exit closed a backend serving three seats, and a one-word prompt run by hand six minutes later reopened it with nothing changed in between. That step is mechanical, and a mechanical step belongs inside tillr (`R-BP1`).

**Cost.** One minimal model turn per closing event, and one per change of the backend's inputs while it is closed. No worktree, no lane, and no tokens in normal operation.

**Reads with:** [what the operator settled without the owner is settled inside tillr](./operator-decisions-inside-tillr.md), [the alert triage seat](./tillr-alert-triage-seat.md), [a failure is diagnosed, not waited out](./failure-diagnosed-not-waited-out.md), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) and [the watch design](./tillr-watch.md).

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, lane, seat and verdict. **The operating session** is the foreground agent session that watches the drain. Three terms are used with exact meanings here:

- **Backend smoke probe**: a bounded, tool-free, sessionless single-turn model call against one named backend, in that backend's adapter environment and model configuration, recorded as a `drain.backend-probe` event.
- **Inputs digest** (coined here): a digest of the non-secret inputs that decide whether a closed backend's probe can behave differently: its policy entry, its adapter scripts, and for the `pi` adapter its harness build and extension set.
- **Self-repair reachability** (coined here): whether every seat needed to plan, build, review and verify a repair item is mapped to an open backend. When the closed backend serves one of them, tillr cannot land its own repair.

---

## 1. The rulings and the case

Five owner rulings bind the design:

1. **`R-BP1`.** What the operating session can settle without the owner belongs inside tillr ([landing principle 16](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr)).
2. **`R-BP2`.** No fallback to another model or backend: "I hate fallbacks. Fallbacks are almost always wrong. We need deliberate choices, not fallbacks. We need to fix what's wrong."
3. **`R-BP3`.** An unavailable backend or judge raises a visible alarm and is fixed.
4. **`R-BP4`.** The watcher and alert triage apply no remedies ([`OQ-TW2`](./tillr-watch.md#OQ-TW2)).
5. **`R-BP5`.** Every alert gets an investigation and a recorded action.

Probing the same backend is neither a fallback nor a cooldown:

- **No fallback.** No item, step or judge moves off its configured backend. The closed backend is the one probed. If it answers, held work resumes on it; if not, it stays closed on an alarm.
- **No timer, cooldown or retry count.** A closing run was a full worker session. One minimal prompt against the same adapter and model is an immediate measurement of whether the backend can answer at all ([landing principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)). Once a probe fails under a given inputs digest, no second automatic probe runs until the digest changes. Elapsed time never triggers one.

This amends [`D-JL24`](./tillr-judge-and-lifecycle.md#a-failed-run-holds-with-its-cause-d-jl24), which names the operator as the only mover of a closed backend.

## 2. What exists today

| Surface | Behavior | Gap |
| :--- | :--- | :--- |
| Closure recording (`RecordBackendOutcomes` in [`backend_outcomes.go`](../../tools/tillr-drain/drain/backend_outcomes.go)) | Classifies each run ending and records whether it closes its backend | Nothing inside tillr then tests whether the backend is down |
| Manual probe (`ProbeBackend` in [`backend_availability.go`](../../tools/tillr-drain/drain/backend_availability.go); `tillr drain probe-backend`) | Runs a caller-supplied command, records `drain.backend-probe` with `success` only, and reopens the backend on exit 0 | The caller invents the command; output is discarded, so a failed probe leaves no evidence; the event names no actor, trigger or inputs |
| Adapters ([`run-pi`](../../tools/tillr-run/backends/pi/run-pi), [`run-claude`](../../tools/tillr-run/backends/claude/run-claude), [`run-agy`](../../tools/tillr-run/backends/agy/run-agy), [`run-muse`](../../tools/tillr-run/backends/muse/run-muse)) | Each resolves its own harness binary, agent directory, environment and flags for a worker run | A bare command on the ambient `PATH` tests a different environment and can pass while lane workers still fail |
| Movers ([`holdrules.go`](../../tools/tillr-drain/holdrules/holdrules.go)) | A closed backend names `operator` as its mover | The operator is not watching between check-ins ([landing principle 13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)) |
| Watch rule (`drain-backend-closed.toml`, `D-BP9`) | Opens an alert on a closing outcome and on a failed probe; clears on a passing probe or a non-closing outcome | It wakes the operating session for every closure, including one a single probe would reopen |

## 3. Who inside tillr probes a closed backend

**`D-BP1`.** The drain loop itself, as a deterministic process step beside its quota refresh, serialized per backend and outside store write transactions.

1. **A recorded fact decides it.** Whether a backend answers a minimal prompt is an empirical fact, not a judgment of prose and not yet a confirmed defect, so the drain acts and records the fact it acted on.
2. **The watcher and the triage seat apply no remedies (`R-BP4`).** Neither may run a state-changing probe.
3. **No bootstrap deadlock.** A model seat sent to test a closed backend could itself be mapped to it. The loop is a process and needs no open seat and no lane.
4. **The manual command stays.** `tillr drain probe-backend` remains for explicit use, such as after refreshing a credential that lives outside the repository, and records that a person ran it.

## 4. What smoke check runs

**`D-BP2`.** A smoke check tests the same binary, agent directory, environment, model route, effort, account and harness flags that a worker run on that backend uses, writes nothing to the repository, and spends minimal tokens.

A local lookup is not enough: a profile or quota query can succeed while every model call is refused, and each adapter changes the child's environment in ways a bare command does not reproduce. Only one real completed turn proves the backend can run work.

Each adapter gains a `--probe` mode on the same resolution path as a worker run, differing in five bounded ways:

1. **A fixed minimal prompt**, in place of a worker brief.
2. **Working directory at the repository root, tools disabled**, so the adapter resolves what lane workers use and no tracked file can change.
3. **Sessionless**, so no paused worker or designer session is read or advanced.
4. **A bounded wall time.** A one-word turn has a known tiny scope, so a timeout keeps a hung connection from stalling the step.
5. **Exit 0 only with a non-error completion**, so a harness that prints an error envelope and exits 0 does not pass.

The probe uses the closing run's model route where the backend rotates among several. The manual command derives the same check when no command is supplied, so manual and automatic probes are identical. How the derivation is configured is [`OQ-BP2`](#OQ-BP2).

## 5. When the smoke probe runs, and when it refuses to repeat

Every trigger is a function of recorded store events and the current inputs digest, never a timer or a counter (`D-BP3`).

### 5.1 The three triggers

| Trigger | Precondition | Why it runs once |
| :--- | :--- | :--- |
| **Closing outcome** | The backend is closed by an outcome, no probe has been recorded after it, no quota window is active, and the closure is not falsified ([§5.2](#52-the-back-to-back-guard)) | Recording the probe, pass or fail, makes the precondition false |
| **Input change** | A failed probe exists after the closing outcome, and the current inputs digest differs from every digest already probed | It runs when a relevant input changed, such as a landed repair or a policy edit, and each digest is probed once |
| **Manual** | A person or the operating session runs the command | Always runs when asked |

### 5.2 The back-to-back guard

A provider can answer a ten-token prompt while failing every real run. Without a guard, each closure would be reopened by a passing probe and the drain would burn one queued item after another.

**`D-BP4`.** Before a closing-outcome probe, the drain reads the backend's previous outcome. If that also closed the backend, was reopened by an automatic probe under the same inputs digest, and no run on the backend ended outside infrastructure in between, the probe's verdict is falsified. The drain runs no subprocess, records a failed probe with the trigger `falsified-by-repeat` naming both runs, leaves the backend closed, and escalates. This compares two adjacent records; it is not a count.

### 5.3 What never triggers an automatic probe

1. Elapsed time.
2. A loop restart: outcomes and probes are durable, so a restart sees the failed probe and runs none.
3. A quota window: a refusal with a stated reset is governed by [`D-JL26`](./tillr-judge-and-lifecycle.md#quota-exhaustion-is-not-an-exception-d-jl26).
4. An unrelated landing, which leaves the inputs digest unchanged.

## 6. How the probe result is recorded and shown

**`D-BP5`.** The `drain.backend-probe` event keeps its top-level `success` so existing readers work, and adds who ran it, the trigger, the closing outcome and run, the inputs digest, the route, the exit code, the duration and a one-line summary.

- **Evidence without leaked credentials.** A failed probe's output goes, size-capped, to an owner-only file under the loop's scratch directory. Only a short summary enters the store, after a redactor strips authorization headers, key prefixes, URL credentials, cookies and secret assignments.
- **A truthful mover on every surface.** The fixed `operator` mover is replaced by the actual one, projected from store events:

| Phase | Mover shown |
| :--- | :--- |
| Probe pending or running | The drain |
| Probe passed | No hold; held items are ready on the same backend |
| Probe failed, triage pending | Triage |
| Probe failed, tracked by an open repair item | The drain, naming the item; the backend is probed again when the fix lands |
| Probe failed, escalated | The operating session with the action needed, or the owner with the question |

## 7. What is handed upward when the probe fails

### 7.1 The self-repair reachability rule

**`D-BP6`.** Before settling a failed probe inside tillr, two facts are checked:

1. **Can triage run?** If the closed backend hosts the triage seat, the alert is escalated to the operating session at once.
2. **Can a filed repair land?** A repair item needs its plan, build, review and verify seats on open backends. If the closed backend serves any of them, filing an item and settling quietly would deadlock, so the item is still filed or linked and the alert is escalated.

### 7.2 Routing the three failure classes

**`D-BP7`.**

| Failure class | Destination |
| :--- | :--- |
| A defect in the repository's adapter or configuration, with self-repair reachable | Settled inside tillr: one deduplicated express repair item is filed or linked. When it lands, the inputs digest changes and the backend is probed again |
| Live host or process state, a falsified probe, or a repository defect whose repair seats are blocked | The operating session, with the failed-probe packet |
| Credentials, billing, a subscription, or an outage that would need a change of model or policy | The owner, with a question and options. Nothing switches models by itself (`R-BP2`) |

### 7.3 The failed-probe escalation packet

Every upward notice carries what its reader needs without searching logs: the closed backend, its route and the seats and held items it affects; the closing run and its cause; the probe's trigger, digest, exit code, duration, summary and evidence file; the triage run, any linked item and which repair seats are blocked; and the action needed or the owner's question.

## 8. Worked cases

| Case | Without automatic probing | With it |
| :--- | :--- | :--- |
| A transient single failure | The backend stays closed until someone probes it by hand | One probe passes within seconds, the backend reopens, and nobody is woken |
| An outage, or a broken harness build | The manual probe discards its output | The probe fails, its evidence is kept, and the operating session is woken with the packet |
| An adapter defect on a secondary backend while the core seats are open | A person reads logs, files a defect, waits and probes | Triage files one repair item; when it lands the backend is probed and reopens unattended |
| An expired credential | A person investigates and then asks the owner | One failed probe with a redacted summary escalates to the owner with options |
| A provider that passes tiny prompts and fails real runs | Each manual probe reopens a backend that fails again | The guard of [§5.2](#52-the-back-to-back-guard) refuses the second reopen and escalates with both runs |

## 9. Edge cases

| Case | Behavior |
| :--- | :--- |
| Several runs fail on one backend at once | One probe runs for the newest closure; a probe older than a newer closure cannot reopen it |
| The probe hangs | The timeout ends it as a failed probe; other backends and store readers are not blocked |
| The loop stops during a probe | No event is recorded, and the next start runs the one probe still owed |
| Policy is reloaded while the backend is closed | The inputs digest changes and one input-change probe runs |
| The review seat's backend closes | Reopening the backend clears both its alarm and the judge-unavailable alarm derived from it, with the candidate's lane intact ([`D-JL27`](./tillr-judge-and-lifecycle.md#the-lane-release-for-judge-waits-is-dropped-d-jl27)) |
| A credential is refreshed with no file changed | The digest never includes secret values, so nothing fires; the manual command tests and reopens the backend |

## 10. The build, in slices that each land alone

`D-BP8`. No slice below is built.

| Slice | What lands | Follows |
| ---: | :--- | :--- |
| 1 | The structured probe event, the redactor and the evidence file | — |
| 2 | The adapters' `--probe` mode and the derived smoke check, usable from the manual command without a supplied command | [`OQ-BP2`](#OQ-BP2) |
| 3 | Automatic probing in the loop on a closing outcome and on an input change, with the back-to-back guard | 1, 2, [`OQ-BP1`](#OQ-BP1) |
| 4 | Truthful movers and probe status across the hold rules, the alarms, `tillr drain why` and the Observatory | 1, 3 |
| 5 | The watch rule moves from every closure to failed probes only, so a passing probe wakes nobody. Under option C of [`OQ-BP1`](#OQ-BP1) the existing rule stays | 1, 3, [`OQ-BP1`](#OQ-BP1) |
| 6 | The reachability check, routing through alert triage or directly, and the escalation packet | 3, 4, 5, [`OQ-BP3`](#OQ-BP3), and the [alert triage seat](./tillr-alert-triage-seat.md) |

## 11. What this does not do

- It never moves work to another model or backend (`R-BP2`).
- It never polls on a timer, and never repeats a failed probe under unchanged inputs.
- It never probes into an active quota window.
- It gives neither the watcher nor the triage seat a remedy (`R-BP4`).
- It never guesses a choice reserved to the owner: credentials, subscriptions, spend limits and policy changes escalate with options.

## 12. Alternatives considered and risks

| Alternative | Verdict |
| :--- | :--- |
| The watcher or the triage seat runs the probe | Rejected: they apply no remedies (`R-BP4`), and triage may itself run on the closed backend |
| Dispatch a queued run on a lane to test the backend | Rejected: it deadlocks when that run's seat uses the closed backend, and wastes a lane on a check that writes nothing |
| Probe again every few minutes | Rejected: time passing is not a changed input ([landing principle 11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out)) |
| Call the harness directly, bypassing the adapter | Rejected: it tests an environment the workers do not run in |
| Use a quota or profile query as the check | Rejected: such a query can succeed while every model call is refused |

| Risk | Mitigation |
| :--- | :--- |
| A minimal prompt passes while real runs fail | The back-to-back guard ([§5.2](#52-the-back-to-back-guard)) |
| Probe output leaks a credential into the store | Full output goes only to an owner-only file; the stored summary is short and redacted |
| A repair item for a core backend deadlocks silently | The reachability rule ([§7.1](#71-the-self-repair-reachability-rule)) |
| A probe at the repository root edits tracked files | Tools are disabled at the harness, and the prompt carries no repository instruction |

---

## Open Questions

1. 💬 **OQ-BP1: Should the drain run one immediate smoke probe on every closing outcome, skip closures whose cause is already literal, or probe automatically only after the backend's inputs change?**

   Some closing outcomes are transient single failures; others quote a literal cause, such as a binary that failed to start or a missing key.

   - **A — Probe once on every closing outcome (unless falsified), and once whenever the inputs digest changes while closed.** Settles transient closures with no table of error strings; a literal failure fails the probe in milliseconds and escalates once.
   - **B — As A, except a closure whose run failed to start or quoted a literal credential or configuration error goes straight to a failed probe.** Saves one doomed turn, at the cost of maintaining a table of literal errors.
   - **C — Never probe on a closing outcome; escalate every closure and probe only when the inputs digest changes.** The strictest reading of "no retry as a test", at the cost of waking the operating session for every transient failure.

   <!-- vantage: question id=OQ-BP1 leaning="A — one minimal turn in the adapter's real environment settles transient closures without an error-string classifier, and the back-to-back guard and one-probe-per-digest rule prevent loops." -->

   _Leaning:_ A — one minimal turn in the adapter's real environment settles transient closures without an error-string classifier, and the back-to-back guard and one-probe-per-digest rule prevent loops.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-BP2: How should a backend's smoke check be configured and derived?**

   A backend's policy entry already names its adapter, model, effort, account and harness arguments, and each adapter resolves its own environment.

   - **A — An adapter `--probe` mode derived from the policy entry, with an optional command override in policy.** No duplicated configuration and no drift from worker runs, while test fixtures may supply a direct command.
   - **B — An adapter `--probe` mode derived from the policy entry only.** One path, which cannot be overridden with a weak command; test fixtures implement `--probe` on their fake adapters.
   - **C — An explicit probe command on each backend's policy entry.** Leaves the adapters untouched, at the cost of duplicating model and environment setup in policy.

   <!-- vantage: question id=OQ-BP2 leaning="A, or B for no override surface — deriving the probe from the policy entry reuses the adapter's own resolution, so a smoke check cannot drift from real worker runs." -->

   _Leaning:_ A, or B for no override surface — deriving the probe from the policy entry reuses the adapter's own resolution, so a smoke check cannot drift from real worker runs.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-BP3: When the drain's smoke probe fails, should the alert go through the alert-triage seat first or wake the operating session directly every time?**

   A failed probe has already ruled out a transient failure.

   - **A — Through the triage seat when triage and the repair seats are open, bypassing to the operating session whenever the closed backend blocks either.** A defect on a secondary backend is repaired inside tillr; a core closure or a credential failure still escalates at once.
   - **B — Straight to the operating session, every time.** Treats a backend that failed its own probe as urgent, and spends no triage tokens.

   <!-- vantage: question id=OQ-BP3 leaning="A — tillr files and repairs secondary adapter defects itself when the core seats are open, and the reachability bypass guarantees an immediate wake-up with no deadlock when they are not." -->

   _Leaning:_ A — tillr files and repairs secondary adapter defects itself when the core seats are open, and the reachability bypass guarantees an immediate wake-up with no deadlock when they are not.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

Rows `R-BP1` to `R-BP5` are the owner's rulings. Rows `D-BP1` to `D-BP9` are the designer's decisions, and the owner may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-BP1 | **Owner ruling.** A problem the operating session could solve without the owner goes to a responsible agent, process or item inside tillr | 2026-11-21, owner | [§1](#1-the-rulings-and-the-case) | — |
| R-BP2 | **Owner ruling.** No fallback to another model or backend | 2026-11-20, owner | [§1](#1-the-rulings-and-the-case) | — |
| R-BP3 | **Owner ruling.** An unavailable backend or judge raises a visible alarm and is fixed | 2026-11-20, owner | [§1](#1-the-rulings-and-the-case) | The alarm surfaces |
| R-BP4 | **Owner ruling.** The watcher and alert triage apply no remedies | 2026-11-21, owner | [§1](#1-the-rulings-and-the-case), [§3](#3-who-inside-tillr-probes-a-closed-backend) | — |
| R-BP5 | **Owner ruling.** Every alert gets an investigation and a recorded action | 2026-11-22, owner | [§1](#1-the-rulings-and-the-case), [§7](#7-what-is-handed-upward-when-the-probe-fails) | — |
| D-BP1 | The drain loop probes and reopens a closed backend; the manual command stays. **Rejected:** probing from the watcher, from the triage seat, or by a queued run on a lane | 2026-11-22, designer | [§3](#3-who-inside-tillr-probes-a-closed-backend) | — |
| D-BP2 | A smoke check is a bounded, tool-free, sessionless single turn in the backend's own adapter environment on the closing run's route. **Rejected:** bare harness calls, quota probes and local profile queries | 2026-11-22, designer | [§4](#4-what-smoke-check-runs) | — |
| D-BP3 | Automatic probing is driven by events and inputs: at most one probe per closing outcome and one per new inputs digest. **Rejected:** periodic re-probing | 2026-11-22, designer | [§5.1](#51-the-three-triggers), [§5.3](#53-what-never-triggers-an-automatic-probe) | — |
| D-BP4 | The back-to-back guard: a closure that follows a probe-reopened closure with no good run between is recorded as falsified, without a second probe | 2026-11-22, designer | [§5.2](#52-the-back-to-back-guard) | — |
| D-BP5 | The probe event carries its provenance and a redacted summary; full output goes to an owner-only file; the mover shown is the actual one | 2026-11-22, designer | [§6](#6-how-the-probe-result-is-recorded-and-shown) | — |
| D-BP6 | Self-repair reachability is checked before a failed probe is settled inside tillr | 2026-11-22, designer | [§7.1](#71-the-self-repair-reachability-rule) | — |
| D-BP7 | A failed probe routes to a repair item, the operating session or the owner by failure class | 2026-11-22, designer | [§7.2](#72-routing-the-three-failure-classes), [§7.3](#73-the-failed-probe-escalation-packet) | — |
| D-BP8 | The build is the six slices of [§10](#10-the-build-in-slices-that-each-land-alone) | 2026-11-22, designer | [§10](#10-the-build-in-slices-that-each-land-alone) | — |
| D-BP9 | Until automatic probing is built, and permanently under option C of [`OQ-BP1`](#OQ-BP1), a watch rule alerts on every closing outcome and clears on a passing probe or a non-closing outcome | 2026-11-22, designer | [§2](#2-what-exists-today) | Yes: `config/tillr/watch/drain-backend-closed.toml` |
