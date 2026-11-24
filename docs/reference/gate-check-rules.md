---
title: "Gate check rules — a red is read once, a check asserts what was recorded, a quarantine is named and counted"
status: accepted
stage: CURRENT
verified: 2026-11-21
verified_commit: e167325c21f77151e9eeab22df542b3837e0f631
covers:
  - tools/fork-check.py
  - tools/gate/checks/elapsed-time.py
  - tools/gate-timing-declarations.json
  - tests/fork-check.test.py
tags: [gate, checks, flake, timing, quarantine, reference]
summary: "Three standing rules for the gate's checks, each with the code that enforces it today: a failed check is diagnosed from its one run and never re-run to see whether it passes; a check asserts counts, orders and equalities over what the product recorded and never an elapsed duration; and a quarantined case is narrow, carries the item that repairs it, applies only to the file content it was declared against, and is counted apart from passes."
---

# Gate check rules

**Status:** Source-verified 2026-11-21 against `e167325c21f77151e9eeab22df542b3837e0f631`.
UNMEASURED: this reference was written by reading the code, not by observing a gate run.

**The gate** is the program that decides whether a change may land: `tools/fork-check.py`,
together with the per-change checks under `tools/gate/checks/`. A **check** is one step the
gate runs, such as a test suite, a build or a guard over the tree. A check is **red** when it
ran and failed, which is a verdict about the change. This page holds the rules for what a
check may assert and for what a red means. How a red candidate travels back to its worker is
the dispatch side's subject, in
[landing principle 11](../design/landing-principles.md#11-a-failure-is-diagnosed-not-waited-out).

| Component | Lives in |
| :--- | :--- |
| Running a check once, and the triage packet | `tools/fork-check.py` (`execute_attempt`, `triage_packet`) |
| The timing rule and its scan | `tools/fork-check.py` (`GATE_TIMING_RULE`), `tools/gate/checks/elapsed-time.py` |
| Recorded exceptions to the timing rule | `tools/gate-timing-declarations.json` |
| Quarantine declarations and their application | `tools/fork-check.py` (`Quarantine`, `QUARANTINED_FORK_QUALITY`, `applied_quarantine`) |

**Reads with:** [Preflight, explained](../preflight-explained.md) (the argument for the timing
rule, in plain words) and the fork workflow (the checks that build and test
the patched forks).

---

## A red is read once, never re-run

**There is no automatic retry of a red gate.** A person who gets a red does not blindly run
it again; they read the failure and decide whose failure it is. The gate does the same. A
failed check costs exactly one run of that check, and what the gate produces from it is a
**triage packet**: a machine-readable record, read out of that run's own output, of the
failing case's name and file, the assertion text, the paths the assertion names, the commits
that touched those paths, and the one-file command that reproduces the failure.

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
> **A check that was killed from outside was not judged.** When a check's process is ended by
> a signal the gate did not send, the gate runs it again a small fixed number of times and
> then reports that nothing was judged. That is not a re-run of a red: a killed process
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

## A quarantined case is narrow, ticketed and counted

A **quarantine** is a declaration, committed in the gate's own source, that named failing
cases of one test file stop making their check red while they stay in place. It is not a
verdict that the cases pass.

- **It applies only when the declared cases are the run's only failures.** Any other failing
  case in that check, a case of the same name in another file, or a failure count that
  disagrees with the list of failing names keeps the check red.
- **No item, no quarantine.** A declaration names the queue item that repairs the case. A
  declaration whose item is blank suppresses nothing: the check stays red and the reason is
  printed beside it. The gate checks that an item is named; it cannot check from a checkout
  that the item is still open.
- **It applies only to the content it was declared against.** A declaration records the git
  blob id of the test file it names: the identifier git derives from the file's bytes, which
  `git hash-object <file>` prints. The gate computes that id for the file in the fork's
  candidate checkout, the clean checkout under `.forks/candidate/` that the check ran in.
  While the two ids match, the declaration applies. When they differ, or the file cannot be
  read, it suppresses nothing: a changed file is a new subject that the declaration's
  evidence does not cover, so the check stays red and the reason is printed beside it with
  both ids. A declaration has no expiry date, and the gate reads no clock to decide whether
  one applies. What ends a quarantine is its item's work: the patch that repairs the case
  changes the file, and the declaration is then deleted.
- **No count refuses a declaration.** The gate holds no limit on how many declarations exist,
  per repository or per check. What keeps quarantines from accumulating unseen is the item
  each one names and the report line below.
- **A classification says why the case fails, and "could not answer" is not one.** A
  declaration is classified `upstream` (the failure reproduces on the unpatched upstream),
  `flaky` (a proven flake) or `unknown`. A quarantined case ran and failed. A check that could
  not run at all, because a tool or checkout is missing, is reported separately as one that
  could not answer, and is never a quarantine.
- **A quarantined case is named and counted in every gate report, apart from passes.** The
  report prints the check, the cases, the item, the declared blob id, the classification and
  the reason, and its summary counts quarantined cases separately and says that a quarantined case is not a passing one. The
  run's evidence record carries the same block.

The gate's run-alone slot was removed on 2026-11-21. A check whose result depends on what
runs beside it is repaired at its cause, not isolated. The `pi-subagents` quarantine
declaration was also removed; there are no live quarantine declarations.

## What enforces each rule today

| Rule | Enforced by |
| :--- | :--- |
| A failed check is run once and read from its own output | `execute_attempt` and `triage_packet` in `tools/fork-check.py` |
| A red candidate goes back to its worker with the failure named, with no wait and no count | `tools/tillr-drain`, described under [landing principle 11](../design/landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) |
| No check asserts elapsed time | `tools/gate/checks/elapsed-time.py` for the test files a change adds or edits, and the scan in `tests/fork-check.test.py` that reads `GATE_TIMING_RULE` and the whole check corpus |
| An existing timing assertion is declared with its reason and repair | The same two scans, against `tools/gate-timing-declarations.json` |
| A deadline's bound is far past expectation; a missing observable is recorded in the product | Nothing. These are review rules |
| A quarantine applies only to its declared cases as the run's only failures | `applied_quarantine` and `failing_tests` in `tools/fork-check.py` |
| A quarantine names its item | `Quarantine.lapse` in `tools/fork-check.py` |
| A quarantine applies only while its file has the declared blob id | `Quarantine.lapse` and `file_blob_id` in `tools/fork-check.py` |
| A quarantined case is reported and counted apart from passes | `check_status` and the report in `tools/fork-check.py` |

## Current values

Verified at `e167325c21f77151e9eeab22df542b3837e0f631`, except the quarantine row. On
2026-11-21, `rg 'QUARANTINED_FORK_QUALITY: tuple' tools/fork-check.py` showed the
quarantine declarations tuple is empty.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Kinds a timing declaration may carry | `liveness`, `backlog` | `tools/gate/checks/elapsed-time.py` (`parse_timing_declarations`) |
| Runs given to a check ended by an outside signal | `OUTSIDE_SIGNAL_RERUNS` | `tools/fork-check.py` |
| Live quarantine declarations | none | `tools/fork-check.py` (`QUARANTINED_FORK_QUALITY`) |
