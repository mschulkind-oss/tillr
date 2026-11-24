---
title: "Gate check rules — a red is read once, a check asserts what was recorded, a set-aside check is named and counted"
status: accepted
stage: CURRENT
verified: 2026-11-24
verified_commit: ed085c74c4e77df9d79e66d0009b7a609445fe71
covers:
  - tools/tillr-gate/run.go
  - tools/tillr-gate/gate.go
  - tools/tillr-gate/enable.go
  - tools/tillr/checkresult/classify.go
  - tools/tillr-drain/drain/flaky_repair.go
  - tools/tillr-drain/drain/gate_stall.go
  - tools/gate/checks/elapsed-time.py
  - tools/gate-timing-declarations.json
tags: [gate, checks, flake, timing, quarantine, reference]
summary: "Three standing rules for the gate's checks, each with the code that enforces it today: a failed check is diagnosed from its one run and never re-run to see whether it passes; a check asserts counts, orders and equalities over what the product recorded and never an elapsed duration; and a check set aside from the verdict is narrow, carries the item that repairs it, applies only to the inputs it was recorded against, and is counted apart from passes."
---

# Gate check rules

**Status:** Source-verified 2026-11-24 against `ed085c74c4e77df9d79e66d0009b7a609445fe71`.
UNMEASURED: this reference was written by reading the code, not by observing a gate run.

**The gate** is the program that decides whether a change may land: `tillr-gate`
([`tools/tillr-gate`](../../tools/tillr-gate/README.md)), running the checks declared one file
each under `tools/gate/checks/`, together with the Go test suites. A **check** is one step the
gate runs, such as a test suite, a build or a guard over the tree. A check is **red** when it
ran and failed, which is a verdict about the change. This page holds the rules for what a
check may assert and for what a red means. How a red candidate travels back to its worker is
the dispatch side's subject, in
[landing principle 11](../design/landing-principles.md#11-a-failure-is-diagnosed-not-waited-out).

| Component | Lives in |
| :--- | :--- |
| Running a check once, and the log its verdict names | `tools/tillr-gate/run.go` (`runPlanChecks`, `runCheck`, `VerdictCheck.LogRef`), `tools/tillr-gate/gate.go` (`RunGate`, `writeTriage`) |
| The timing rule and its scan | `tools/gate/checks/elapsed-time.py`, declared in `tools/gate/checks/elapsed-time.json` |
| Recorded exceptions to the timing rule | `tools/gate-timing-declarations.json` |
| Disabling a contradicted check, and re-enabling it | `tools/tillr/checkresult/classify.go` (`Classify`), `tools/tillr-gate/enable.go`, `tools/tillr-drain/drain/flaky_repair.go` |

---

## A red is read once, never re-run

**There is no automatic retry of a red gate.** A person who gets a red does not blindly run
it again; they read the failure and decide whose failure it is. The gate does the same. A
failed check costs exactly one run of that check. The verdict record names, for each check,
the file that holds that run's own output (`log_ref`), so a red is read from the run that
produced it. When the gate is one named program, `tillr-gate` also writes a bounded **triage**
*(the code's name for the file)*: the program's own status lines and the tail of its output,
headed by what ran, where, how it ended and where the whole output is.

A re-run that passes says only that something differed between two runs. It cannot say what,
so it turns one unexplained red into an equally unexplained pass.

**Retry with a budget is rejected as a design.** A budget (a fixed number of permitted
re-runs) does not distinguish a flaky test from a real failure. It spends re-runs on both,
which delays the repair of the real failures and absorbs the flaky ones, and each absorbed
flake raises the rate of red the next budget is tuned against. A budget turns a failure
nobody can attribute into a failure everybody tolerates. A retry in the gate is an assertion
that the first red meant nothing.

Two related rules follow.

- **A flake is a defect.** A **flake** is a test that fails on some runs and passes on others
  with no change to the code. A later pass never erases the red that came before it.
- **"Flaky" is a finding that needs evidence, not the default for an unexplained red.** It is
  the one explanation that clears a change cheaply, so it is the one an agent under pressure
  to proceed reaches for first.

The person or agent repairing a red may run the one failing file, or one named case in it, as
often as needed to find the cause. That is diagnosis, not a verdict, and it does not run the
gate or the whole suite.

> [!NOTE]
> **A check that did not answer was not judged.** A check whose process was killed by a signal,
> or could not be started, is recorded as `error`: nothing was judged, and the candidate is
> neither passed nor failed. A check that stops making progress is ended by the gate's watchdog
> and recorded as `stalled` (exit 6), which is also not a verdict. The drain gates that same
> head once more after the first stall, with fewer gates in flight, and holds the item as
> `gate-stalled` on the second. That is not a re-run of a red: a process that did not answer
> produced no verdict to re-test.

## A check asserts what was recorded, never elapsed time

An assertion on elapsed time measures the machine and not the code, so its verdict depends on
what else was running. The standing rule has four parts.

1. A check may assert **counts, orderings and equalities over recorded observables**: the
   fields, rows and sequences the product itself writes.
2. **Elapsed durations are diagnostics, never pass or fail.** A duration may be printed
   beside a count assertion; it may not be the assertion.
3. **A deadline is for liveness, not correctness.** A wait may fail only on "this never
   happened", with a bound far past the expected time, so that reaching it never means "this
   happened slowly".
4. If a behavior has no recorded observable, the fix is to record one in the product, as one
   added field. It is not to time the behavior from outside. A case for which even that is
   impossible leaves the gate for a benchmark, where duration is the subject.

Savings in gate time are never bought by checking less: not by dropping a check from the
plan, widening a bound until it passes, turning an assertion into a diagnostic with no
recorded-observable assertion left in its place, or adding a retry. A "temporary" duration
assertion is refused like any other.

The scan that enforces part 2 looks for one shape: a wall-clock difference (now minus a start
taken before the work) compared to a bound. A wait against an absolute deadline is not that
shape, and part 3 permits it. Every existing hit is recorded in the declarations file with a
kind, a reason and its repair. A hit with no declaration fails the gate, and so does a
declaration that no longer matches a hit, because a declaration that outlives its defect only
silences the scan.

## A check set aside from the verdict is narrow, ticketed and counted

A check may stop counting toward the verdict while it stays in the tree only under a record
that says which check, on what evidence, and which item repairs it. Setting a check aside is
never a verdict that it passes.

The one mechanism that does this is the **disabled check**, defined in
[the gate design](./gate.md#a-contradicted-check-is-disabled): a check whose
result store holds both a pass and a fail for the same fingerprint of its declared inputs.

- **It is narrow.** A marker names one check and one episode: the fingerprint, and the time
  and the copied output of both the pass and the fail. A check with no such contradiction is
  judged by its raw result.
- **It carries the item that repairs it.** The drain files one repair item per disabled
  check, whose work is to fix the check at its cause and re-enable it, without a retry, a
  sleep or a skip. `tillr-gate enable` clears the episode once that item has landed.
- **It applies only to the inputs it was recorded against.** The marker is bound to its
  episode. A contradiction that matches an episode already resolved does not disable the
  check again.
- **It is named and counted in every gate report, apart from passes.** A disabled check's
  result is `disabled`, never `pass`. The run summary counts passed, remembered, unavailable,
  disabled and failed checks separately.
- **"Could not answer" is not a classification of failure.** A check that could not run at
  all, because a tool or a declared machine resource is missing, is reported as `unavailable`
  or `error`, and is never a disabled check.

**Setting aside single named cases of a test file** (a quarantine of cases rather than of a
whole check) is not enforced by code: no declaration format for it exists. The rule for one,
if one is added, is the list above, with the declaration bound to the test file's git blob id
(`git hash-object <file>`) so that it lapses when the file changes. The classifier reads a
check reported with the word `QUARANTINED` as `unavailable`, never as a pass.

A check whose result depends on what runs beside it is repaired at its cause, not isolated.

## What enforces each rule today

| Rule | Enforced by |
| :--- | :--- |
| A failed check is run once and read from its own output | `runPlanChecks` and `runCheck` in `tools/tillr-gate/run.go` run each planned check once and record its `log_ref`; `RunGate` and `writeTriage` in `tools/tillr-gate/gate.go` do the same for a named gate program |
| A check that did not answer is not judged, and a stall is gated once more | `gateResult` in `tools/tillr-gate/gate.go`, the watchdog in `tools/tillr-gate/watchdog.go`, and `tools/tillr-drain/drain/gate_stall.go` |
| A red candidate goes back to its worker with the failure named, with no wait and no count | `tools/tillr-drain`, described under [landing principle 11](../design/landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) |
| No check asserts elapsed time | `tools/gate/checks/elapsed-time.py`, for the test files a change adds or edits. A scan of the whole test corpus is not enforced by code |
| An existing timing assertion is declared with its reason and repair | The same check, against `tools/gate-timing-declarations.json`; a declaration that no longer matches a hit fails it |
| A deadline's bound is far past expectation; a missing observable is recorded in the product | Not enforced by code. These are review rules |
| A check is disabled only on a contradicted fingerprint, per episode | `Classify` and `classifyContradiction` in `tools/tillr/checkresult/classify.go`; the marker is written by `tools/tillr-gate/enable.go` |
| A disabled check names its repair item | `sweepDisabledMarkers` in `tools/tillr-drain/drain/flaky_repair.go` |
| A disabled check is reported and counted apart from passes | `writeRunSummary` in `tools/tillr-gate`, held by `TestDisabledCheckExcludedFromPassAndFailCountsAndListedAsDisabled` |
| A set-aside of single named cases is narrow, ticketed and bound to file content | Not enforced by code |

## Current values

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Kinds a timing declaration may carry | `liveness`, `backlog` | `tools/gate/checks/elapsed-time.py` (`parse_timing_declarations`) |
| Result words that are not a verdict on the candidate | `error`, `stalled`, `unavailable`, `disabled`, `over-budget` | `tools/tillr-gate/run.go` |
| Prefix of a filed repair item for a disabled check | `flaky-` | `tools/tillr-drain/drain/flaky_repair.go` (`FlakyRepairPrefix`) |
