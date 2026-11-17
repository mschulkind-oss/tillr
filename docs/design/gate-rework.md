---
title: "The gate lands a change in ten seconds and remembers nothing it cannot recompute"
status: accepted
stage: DECIDED
updated: 2026-11-17
tags:
  - tillr
  - gate
  - landing
  - design
summary: "Measures all 42 check kinds in tools/fork-check.py, then replaces the gate's remembered baseline with a base the caller supplies and results remembered by a fingerprint of the files each check reads. A ten-second landing budget becomes a failing test, heavy checks run after the merge in this repository, tillr deploys only from the last green trunk commit, and a new check is one new file."
next: "Build the nine slices of Section 11, starting with item-gate-check-files and item-gate-policy; then file slices for the red cap, the lane base and the runner's own changes."
depends-on:
  - landing-principles.md
  - landing-velocity.md
  - queue-persistence-ruling.md
vantage:
  status-chip: true
---

# The gate lands a change in ten seconds and remembers nothing it cannot recompute

**Status:** 2026-11-15; by 2026-11-16 six of the nine slices of [Section 11](#11-build-order) had landed and the other three are queued. Every timing is from one run made for this design at
`ebaab2794`, stated in [Section 3](#3-what-exists-today-measured); the six owner rulings of
2026-11-15 are rows `D-GR1` to `D-GR6` of the [Decision Ledger](#decision-ledger). Its three open
questions were settled the same day by the session's designer on recorded principle, at the
owner's request; the owner may overturn any of them. On 2026-11-17 the owner ruled on time limits
in the gate ([`D-GR15`](#decision-ledger)): a budget is a target, and only a stall ends a check.

> **In short.** The gate is slow and brittle for one reason: it decides what to run from a commit
> it remembers, on a machine that may not be the one that remembered it. Give every run its base
> from the caller, remember each result by what the check read, and the ten-second landing and the
> cold machine are the same fix.

**Why it matters.** Run one at a time, the gate's 71 planned checks took 351.6 seconds on
2026-11-15, and one check alone took 141. On the same day a machine with no gate state failed
`just done` on two checks that judged nothing about the change in front of them.

**The shape.** A **change check** judges a diff and gets its base from the caller. A **tree
check** judges a tree and is skipped when a pass is remembered for the same **fingerprint**. A
**fast tier** fits a tested ten-second budget and runs before the merge; the **heavy tier** runs
after it here, and moves a **green pointer** that deploys read.

**Cost.** The trunk may be red between a landing and its heavy checks. The per-branch baseline,
the shared record's planning role and, by attrition, the 10,990-line gate program and its
8,195-line suite go away. [Landing velocity](landing-velocity.md)'s thirty-second membership rule
and its clock cadence are replaced.

**Start at [Section 6](#6-the-shape)** — the six mechanisms. The measurements that force them are
[Section 3](#3-what-exists-today-measured) and
[Section 4](#4-where-the-gate-remembers-and-what-a-cold-machine-sees).

**Needs your ruling:** None. The two implementer's decisions of 2026-11-16 that were open,
[`D-GR13`](#decision-ledger) and [`D-GR14`](#decision-ledger), were ruled on by the owner on
2026-11-17 as [`D-GR15`](#decision-ledger)
([Section 6.5](#65-ten-seconds-is-a-failing-test)).

**Reads with:** [`landing-principles.md`](landing-principles.md) (the rulings this is checked
against in [Section 7](#7-checked-against-the-landing-principles)),
[`landing-velocity.md`](landing-velocity.md) (the two-tier design this amends),
[`queue-persistence-ruling.md`](queue-persistence-ruling.md) (the item-block grammar the
[build order](#11-build-order) is written in), and
[`gate-dual-run-evidence.md`](gate-dual-run-evidence.md) (the Go runner planned the same checks
as the Python gate on three real candidates).

---

## 1. The verdict

Build six things, in the order of [Section 11](#11-build-order):

1. **The caller names the base.** A check that judges a diff runs only where a true base exists:
   at landing, with the commit the lane was staged from. Nothing is ever diffed against a commit
   the gate remembered.
2. **A result is remembered by what the check read.** A check that judges a tree is skipped when
   the same inputs already passed, on any branch, in any worktree. A machine with no memory
   recomputes and reaches the same verdicts.
3. **A missing machine resource is named, not failed.** A check that needs a fork checkout or a
   local branch says it is unavailable and gives the command that provides it.
4. **A check is one file.** Adding one edits nothing another check shares.
5. **Ten seconds is a target and a test.** The fast tier's declared costs must sum inside the
   budget. A check that runs past its own declared cost at run time keeps running, keeps its
   verdict and has the overrun recorded; only a stall, a check making no progress, ends it early,
   and a stall leads to a named next step (the owner's ruling
   [`D-GR15`](#decision-ledger), 2026-11-17).
6. **Heavy checks follow the merge, and deploys follow the heavy checks.** One runner proves the
   trunk after each landing and moves a pointer to the newest commit it proved. tillr builds from
   that pointer and from nowhere else.

What this buys, at the measured prices: the proposed fast tier sums to 8.45 seconds of declared
cost against 229.5 seconds for today's landing tier run in full, and a documentation change that
gated in 2.5 seconds today stays there without depending on which machine ran the last gate.

## 2. The rulings this design is built on

The owner ruled six things on 2026-11-15. They are inputs, not proposals.

| Ruling | What it fixes |
| :--- | :--- |
| A landing gate targets **ten seconds per change** | The fast tier's budget, [Section 6.5](#65-ten-seconds-is-a-failing-test) |
| Lanes run independently, and landings must **outpace them at ten lanes and again at twenty** | Nothing a landing waits for may be shared between lanes except the squash itself, [Section 5](#5-the-arithmetic-at-ten-and-twenty-lanes) |
| The landing strategy is a **per-repository setting with two values**: heavy checks before the merge, so the trunk is always green, or after it, so the trunk is briefly red | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) |
| **This repository takes the second** | The trunk here may be red between a landing and its heavy checks |
| tillr **deploys only from the last trunk commit whose after-merge checks passed** | The green pointer, [Section 6.7](#67-deploys-read-the-green-pointer) |
| **Automatic revert** of an offending landing is a setting, **off here to start** | A red files a fixer; nothing is reverted without a person, [Section 6.8](#68-a-red-heavy-check) |

## 3. What exists today, measured

### 3.1 The run

One command, on 2026-11-15 from 21:58 to 22:04 UTC, in a lane worktree at `ebaab2794`, on the
32-core machine the drain runs on, with a load average near 6 from other lanes:

```bash
PI_FORKS_ROOT=<private root> python3 tools/fork-check.py \
  --preflight --all --cells baseline --report <report.json>
```

`--preflight` is the gate's own diagnostic: it runs the checks the plan selects, one at a time,
and writes no record. `--all` selects everything, which planned 71 checks of 39 kinds. The
private root held links to the shared fork checkouts and a candidate directory of its own, so no
shared checkout was rewritten. The run took **351.6 seconds** and ten checks did not pass; none
of the ten is a statement about this document, and three are evidence used in
[Section 4](#4-where-the-gate-remembers-and-what-a-cold-machine-sees).

### 3.2 Every check

Wall time is one serial run. A kind with several instances shows their sum and the slowest one.
*Grows with* says whether the cost follows the size of the change (`diff`) or the size of what
the repository holds (`repository`: a suite, a tree, a set of packs).

| Check | Tier today | Wall time (s) | Grows with |
| :--- | :--- | ---: | :--- |
| `structural` | landing | 0.23 | repository |
| `file-size` | landing | 0.02 | repository |
| `commit-identity` | landing | 0.05 | diff |
| `waybar` (2) | landing | 1.24, slowest 1.19 | repository |
| `documents` | landing | 10.34 over 498 files; 1.66 over 2 | diff |
| `scratch` | landing | 0.08 | repository |
| `design-citations` | landing | 0.04 | repository |
| `citations` | landing | 16.48 over 2,597 commits; 0.04 over a two-file change | diff |
| `seed-freeze` | landing | 0.01 | repository (one file) |
| `go-build` (10) | landing | 9.34, slowest 1.55 | repository |
| `go-substrate` | landing | 0.82 | repository |
| `series` (2) | landing | not answered: 0.02, see below | repository |
| `fork-construct` | landing | 9.34 | repository |
| `patch-branch-inv2` (8) | landing | 4.73, slowest 0.90 | repository |
| `pi-forks` | landing | 3.61 | repository |
| `self-suite` | landing | **141.16** | repository |
| `rank-order` | landing | 0.07 | repository |
| `session-forensics` | landing | 0.22 | repository |
| `session-budget` | landing | 0.28 | repository |
| `tillr-backends` | landing | 2.83 | repository |
| `tillr-docs` | landing | 3.62 | repository |
| `tillr-loop` | landing | 2.16 | repository |
| `pack-sync` | landing | 0.24 | repository |
| `observatory-web` | landing | 5.05 | repository |
| `meta-muse` | landing | 0.69 | repository |
| `yolo-config` | landing | 3.93 | repository |
| `matt-render` | landing | 2.05 | repository |
| `steering-timer` | landing | 0.12 | repository |
| `seat-profiles` | landing | 0.85 | repository |
| `pipeline-evidence` | landing | 1.46 | repository |
| `extension-residue` | landing | 0.08 | repository |
| `active-steering-skill` | landing | 0.02 | repository |
| `pre-push-guard` | landing | 7.28 | repository |
| `hooks-install` | landing | 1.08 | repository |
| `go-tool` (9) | rolling | 44.92, slowest **28.93** (`tools/tillr-drain`) | repository |
| `pi-inspector` | rolling | 5.68 | repository |
| `session-liveness` | rolling | 7.67 | repository |
| `fork-quality` (6) | rolling | 63.71, slowest **47.78** (pi-subagents unit suite) | repository |
| `storybook` (2) | rolling | not answered here; **27.86** and **16.24** in the gate's own run at 21:44 UTC the same day | repository |
| `forks-check`, `build`, `patch-branch-inv3` | landing | not planned: the plan answered "no series of it changed" for all eight forks | repository |

Three rows need a sentence each:

- **`series`** exited in 0.01 seconds per pack with "the patch branch is not yet resolved on this
  machine". Its answer lives on the host, so a jail cannot time it.
- **`storybook`** failed in under 0.1 seconds because it looks for the fork checkout beside the
  worktree and ignores `PI_FORKS_ROOT`. The times shown are from the gate's timing record for
  its own run at 21:44 UTC.
- **`go-tool`** for `tools/tillr-drain` took 28.93 seconds here and 0.19 seconds in the gate's
  run at 21:44 UTC, because Go's test cache had a pass for the same inputs there and not in a
  fresh worktree. That is the mechanism of [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint), already working for one tool.

The landing tier as declared today sums to 229.5 seconds and the rolling tier to 122.1.

### 3.3 What alone exceeds ten seconds

| Check | Seconds | Always, or only on a wide range |
| :--- | ---: | :--- |
| `self-suite` | 141.16 | always, when the gate program or its suite changes |
| `fork-quality`: pi-subagents unit suite | 47.78 | always |
| `go-tool`: `tools/tillr-drain` | 28.93 | always, on a cold Go test cache |
| `storybook`: roster | 27.86 | always |
| `storybook`: footer | 16.24 | always |
| `citations` | 16.48 | only over a range of thousands of commits |
| `documents` | 10.34 | only over hundreds of files |

`fork-construct` (9.34), `session-liveness` (7.67) and `pre-push-guard` (7.28) are inside ten
seconds alone and would not be beside anything else. The same day's record holds `pi-inspector`
at 12.05 seconds, so it belongs with them.

### 3.4 What a landing costs today

The gate's timing record holds three verified landing runs from 2026-11-15: 2.5 seconds for a
two-document change, 34.4 seconds for a change under `tools/tillr-drain` (28.7 of it one Go
suite), and 146.8 seconds for a change to the gate program (145.2 of it `self-suite`). Planning
alone costs 0.9 seconds (`--explain --all`). The squash is not the cost: `git merge-tree
--write-tree` over three trunk commits answered in 6 milliseconds.

## 4. Where the gate remembers, and what a cold machine sees

The gate keeps state in four places outside the tree. Each one changes what a run plans or how a
check answers, and none of them travels with a clone.

| State | Where | What reads it | A machine without it |
| :--- | :--- | :--- | :--- |
| The record: a verified commit per branch and one shared, upstream fingerprints, the yolo build and node version it last saw, run history | `yolo-fork-check/record.jsonl` under the common Git directory | Planning: the diff base is the newest remembered commit this history contains | Plans from nothing, or from whatever old commit a copied record names |
| Local `patch/*` branches | This clone's refs | `structural` asserts each pack's patch branch exists in this clone; fork steps read them | `structural` fails on every pack that names one |
| Fork checkouts | `.forks/` in the main checkout, gitignored | Every fork step, `rank-order`, the storybooks, the liveness harness | Those steps refuse the run, or fail in milliseconds |
| The yolo build on `PATH` | The machine | `series`, `yolo-config`, `meta-muse`, `matt-render` | A different answer with no path changed; today it is reported and plans nothing |

**What happened on 2026-11-15.** A machine with none of this state failed `just done` twice:
`seed-freeze` judged the change against a baseline dated 2026-11-02, and `structural` failed on
seven local branches that did not exist. Neither finding was about the change.

**The same fault, reproduced on this machine.** The record here holds no per-branch entry and one
shared baseline, `a732ffaa4` of 2026-10-12, which is 2,597 commits behind. A plain `just done
--explain` in a lane worktree therefore plans 1,673 changed paths and all 71 checks. In the run
of [Section 3.1](#31-the-run), `seed-freeze` reported 62 findings, each calling an entry
"net-new", because the file did not exist at that base. `citations` read 2,597 commits of added
lines and refused on documents nobody in this change touched. `documents` reported 338 errors
across 498 files for the same reason.

**The cause is one design choice.** The gate answers "what changed?" from its own memory. A
check that asks "did this change add an entry?" is only meaningful against the commit the change
started from, and the only actor who knows that commit is the caller. The drain already passes
it: its landing gate runs with `--since <base>`, and the usage text calls that form "NOT
completion approval". The remembered baseline exists for the hand-run `just done`, and it is the
only path that fails cold.

## 5. The arithmetic at ten and twenty lanes

A landing is a gate followed by a squash. The gate already runs outside the land lock, in the
lane's own worktree; only the squash is serialized
([principle 3](landing-principles.md#3-gate-what-will-be-the-tip-once)). So landings outpace
lanes when two things hold:

- **No lane's gate waits on another lane.** The gate may share nothing mutable between lanes: no
  record to append in order, no baseline to advance, no candidate checkout to reset.
- **The lock is held for less than one lane's cycle divided by the lane count.** The fastest run
  measured is 23 seconds. With a ten-second gate a lane's shortest cycle is 33 seconds, so ten
  lanes need the lock held under 3.3 seconds per landing and twenty lanes under 1.65. The merge
  itself measured 6 milliseconds; the whole hold is unmeasured and
  [the scale slice](#twenty-lanes-land-without-waiting-on-each-other) reports it.

Processor time is the other bound. Building all ten Go modules in parallel took 1.4 seconds of
wall time and 16 seconds of processor time on a warm build cache. Twenty lanes each paying that
inside a 33-second cycle use about ten of this machine's 32 cores, and that is the worst case:
it assumes every lane changes Go code and no result is remembered.

## 6. The shape

### 6.1 The caller names the base

Five words are used from here on, and each is coined in this document.

- A **change check** *(coined here)* judges a diff: "did this change add an entry to the frozen
  seed", "does an added line cite an unreachable commit", "is every new commit authored by the
  configured identity", "did this change add an oversized file". It is not a check that happens
  to be selected by a diff.
- A **tree check** *(coined here)* judges one tree and nothing else: a suite passes, a pack lints,
  a module builds. Its answer does not depend on where the tree came from.
- The **base** *(coined here)* is the commit a change check diffs against. It is always an
  argument, and for a lane it is the commit the lane was staged from, read from the lane's
  `base_sha` file.
- A **fingerprint** *(coined here)* names the inputs of one tree check on one tree; it is defined
  in [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint).
- The **fast tier** and the **heavy tier** *(coined here)* replace `landing` and `rolling`: fast
  is what fits the budget of [Section 6.5](#65-ten-seconds-is-a-failing-test), heavy is
  everything else. When heavy runs is the setting of
  [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer).

The rules:

- **A change check runs only with a base the caller passed.** With none, the run is a usage error
  (exit 2) that names the missing argument. There is no default and no remembered commit.
- **A change check runs once, at landing.** Its question was answered when the change landed.
  It is never re-asked over a trunk range, and it does not run in a whole-tree audit.
- **A tree check never receives a base.** What it runs on is decided by its fingerprint.
- **A whole-tree audit is the explicit absence of a base**, spelled `--tree`: every tree check,
  no change check, and the report says so in one line.

The five change checks today are `commit-identity`, `seed-freeze`, `citations`, the added-file
half of `file-size`, and `documents`, which reads the Markdown files a change touched. On a
lane-sized range the first four measured at or under 0.05 seconds each and `documents` 1.66.

### 6.2 A tree check's result is remembered by fingerprint

The fingerprint of a tree check on a tree is a SHA-256 over three things:

1. the bytes of the check's own declaration ([Section 6.4](#64-a-check-is-one-file));
2. the Git blob id and path of every tracked file under the paths the declaration lists as
   `reads`, in that tree — the whole tree when it lists none;
3. the identity of each external tool the declaration names in `tools`: the string that tool's
   version command prints, and for yolo its build commit.

Computing it costs one `git ls-tree`, which measured 6 milliseconds for the whole tree.

**The result store** *(coined here)* is a directory under the common Git directory, one file per
fingerprint, holding the check's id, verdict, seconds, time and tree. The rules:

- **Only a pass is reused.** A tree check whose fingerprint has a remembered pass does not run,
  and the report line says `remembered` with the time of the pass.
- **A fail is recorded and never reused.** It runs again on the next request. When a pass arrives
  for a fingerprint that already holds a fail, the run passes and reports the check as `flaky`,
  with both times, and the heavy runner files one item for it. A green re-run does not erase the
  red; it names it.
- **Any gate run may write.** Writes are whole-file renames, and the key is the content, so two
  runs that write the same key wrote the same inputs. There is no lock, no order and no
  per-branch entry, which is what [Section 5](#5-the-arithmetic-at-ten-and-twenty-lanes) needs.
- **An undeclared input is the safe side.** A declaration with no `reads` is fingerprinted by the
  whole tree, so it reruns on every change. Narrowing `reads` is a claim the author makes, and a
  check that reads a path it did not declare is a defect in that one file.
- **A check that cannot be a function of its inputs says so** with `remember: false`, and always
  runs. A suite decided by real time is the expected case.
- **Entries unread for 30 days are deleted** by the heavy runner at the start of a run.

**On a cold machine** the store is empty. Every tree check runs once — 351.6 seconds one at a
time on this tree, less in parallel — and reaches the verdict a warm machine remembered. Nothing
else is recomputed, because nothing else is remembered: there is no baseline to rebuild and no
record to copy. The existing record file is left in place and never read for planning again.

> [!WARNING]
> Do not share the result store between machines. A pass means "these inputs passed with this
> machine's tools", and the fingerprint carries only the tools a declaration names. A copied
> store turns an undeclared tool difference into a silent green; a recomputation costs six
> minutes once.

### 6.3 A missing machine resource is named, not failed

A declaration lists what it needs from the machine besides the tree, in `needs`: a local ref
pattern, a directory, a program. Before a check runs, each need is tested.

- **A check with an unmet need is `unavailable`.** It does not run, it is not a failure, and its
  report line gives the one command that provides the need.
- **Unavailable never blocks a landing and never reads as a pass.** The run's summary counts
  passed, remembered, unavailable and failed apart.
- **A tree check asserts only about the tree.** The assertion in `structural` that a pack's patch
  branch exists in this clone moves to the fork checks, which need those refs. On a clone with no
  `patch/*` branches `structural` passes and eight fork checks are unavailable, each naming the
  fetch that provides its branch.

Refs a check reads join its fingerprint as the commit each resolves to, so a moved patch branch
is a changed input like any other.

### 6.4 A check is one file

Today a new check edits one 10,990-line program in several places — the planner, the tier table,
the verdict-group table, the header that documents every step — and that edit selects
`self-suite`, 141 seconds, for a change that added one command.

A check becomes one declaration file in a directory of them, `tools/gate/checks/`, read by the
runner that already plans from declarations, [`tools/tillr-gate`](../../tools/tillr-gate/). The
file holds everything the tables hold now:

| Field | Meaning | Default |
| :--- | :--- | :--- |
| `id`, `label` | The name in every report line | required |
| `command` | What to run; a change check receives the base and head as arguments | required |
| `scope` | `change` or `tree` | required |
| `tier` | `fast` or `heavy` | required |
| `budget_ms` | Declared cost of a fast check, in milliseconds | required when `tier` is `fast` |
| `timeout_ms` | When a heavy check is killed and reported as no verdict | required when `tier` is `heavy` |
| `reads`, `tools`, `needs`, `remember` | The inputs of [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint) and [Section 6.3](#63-a-missing-machine-resource-is-named-not-failed) | whole tree, none, none, `true` |
| `writes`, `exclusive` | Paths the check writes, and whether it must run alone | none, `false` |

The rules:

- **No field that changes a verdict has a silent default.** A file without `scope`, `tier` or its
  cost field is refused by name, and the run is not a verdict. Today an unnamed kind defaults to
  the landing tier; here it does not load.
- **Adding a check adds files and edits none.** The declaration, the program it runs, and that
  program's own test live beside each other. The test is itself a check whose `reads` is that
  directory, so it runs when the check changes and for no other change.
- **The runner's own suite is a check like any other**, reading `tools/tillr-gate/`. It measured
  2.0 seconds.
- **The gate program retires by attrition.** Each slice that moves a check deletes it from
  [`tools/fork-check.py`](../../tools/fork-check.py) and its cases from
  [`tests/fork-check.test.py`](../../tests/fork-check.test.py). While both exist the landing
  gate runs the runner and then the remaining program, and no check is declared in both.

### 6.5 Ten seconds is a failing test

The budget is 10,000 milliseconds from the start of the landing gate to the trunk ref moving.
1,000 of it is reserved for planning, fingerprints and the squash, which leaves 9,000 for checks.
It is enforced in two places, and neither is a remembered measurement.

**At admission, by arithmetic.** One fast change check, `gate-budget`, reads every declaration
and fails when the `budget_ms` values of the fast tier sum above 9,000. It reads no clock. The
sum is over every fast check, as if all were selected and run one after another, because that is
the case that still holds on one core with twenty lanes. A change that adds a fast check which
does not fit fails on that change, and its author has three moves: make room, make it faster, or
declare it heavy.

**The owner's ruling of 2026-11-17 on time limits ([`D-GR15`](#decision-ledger)).** Asked what
"could not judge" meant for a check that was still running, the owner answered:

> "what does 'could not judge' mean. isn't this directly opposed to our principles? target
> budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise
> finish just fine."

The same position reached this design the same day through another project's notes of a
conversation with the owner, quoted as relayed: "The agent works until it is done, it has an
event, we have a stall detector watchdog if we need something like that, but we don't just say,
'Okay, 30 minutes,' and then have some gate given 20 seconds and on a loaded machine it fails."
Those notes summarize it as "no clock whose only consequence is a kill". The ruling, as this
design applies it:

- **A budget is a target.** It is measured and reported. Exceeding one is never, by itself, a
  failure, a fault or "could not judge".
- **Only a stall ends a check early.** A stall is a check making no progress, seen by a watchdog;
  elapsed time is not a liveness signal. A stall leads to a named next step, never to a silent
  kill.
- **Load must not fail work.** A check on a loaded machine that would have finished is let
  finish.

It **supersedes** two things built on 2026-11-16 under [`D-GR13`](#decision-ledger): the ceiling
as a kill, and "could not judge" (exit 4) for a check that is still making progress. Neither the
ceiling nor a heavy declaration's `timeout_ms` may stop a check that is making progress. Two
parts of this design that are not built also act on a clock alone, and the ruling governs both
where this section differs from it: carrying a fast tree check, which stops it at its budget
(suspended by [`D-GR14`](#decision-ledger) and not to be turned on as designed), and a heavy run
that is red on `gate-budget` only because a check overran there.

It **keeps** the budgets and the admission test over their sum, the 9,000 millisecond cap as the
sum the budgets must fit, the `over_budget_ms` record on every overrun, and the ten-second
landing as a target whose measured distribution per check is visible, so a check that overruns
chronically is fixed rather than tolerated. How a stall is detected and what follows it is this
design's decision [`D-GR16`](#decision-ledger): a check shows progress when its output grows or its
process group uses CPU time; one that shows neither for 60 consecutive one-second samples is
stalled and ended; the first stall re-gates the same candidate once fewer gates are in flight or
no other gate is, and a second stall on that candidate holds the item, naming the check.
`item-gate-stall-watchdog` in [Section 11](#11-build-order) builds it. Until it lands, the runner
behaves as the rest of this section describes.

**At run time, by each check's own declaration.** As designed, the runner gives a fast tree
check its `budget_ms` as a deadline. A fast tree check still running at its deadline is stopped
and **carried**: recorded on the landing as `over-budget`, and owed to the heavy tier, which runs
it with no deadline but its timeout. The landing proceeds. A fast change check cannot be carried
(the first boundary below), so its budget is not where it is stopped: it runs on and its own
answer is the verdict, with the overrun recorded against the check.

**Carrying is suspended until the heavy tier exists** ([`D-GR14`](#decision-ledger)). No heavy
runner exists yet (`item-gate-heavy-runner` is in the queue), so a carried check is not owed to
anything: the verdict stays pass and nothing runs the check again. Carrying today means dropping
the judgment, which is the "nothing is carried" situation of the second boundary below. The
review of 2026-11-16 measured `go-build` at 5.6 to 5.7 seconds against its 2,700 millisecond
budget at ten concurrent gates, so it was carried on almost every landing, and a candidate that
did not compile could land in any Go module but `tools/tillr`. So until the heavy runner lands, a
fast tree check is run like a change check: past its budget it runs on to its ceiling and keeps
its verdict, and a failing one fails the gate. The switch is the runner's `--tree-overrun` option,
`keep` by default, and the loop's gate program
[`tools/gate/change-gate.py`](../../tools/gate/change-gate.py) states it in one constant,
`TREE_OVERRUN`. The heavy runner's item was to turn that constant to `carry`. This was an
implementer's decision; the owner ruled on 2026-11-17 ([`D-GR15`](#decision-ledger)) that only a
stall ends a check early, so `keep` is the rule rather than a suspension.

So machine load makes the gate slower rather than failing the item, and the gate's wall time is
no longer bounded by the sum the admission test bounded. **The worst case**, stated plainly:
checks run serially, and every one of them may run to its ceiling. There are 11 fast checks
(six change checks and five tree checks), none declares `timeout_ms`, so each ceiling is the
9,000 millisecond cap, and 11 x 9,000 = 99,000 milliseconds, about 99 seconds of checks, against
the 8,950 milliseconds the budgets declare. The runner's own work and the landing tier of
`tools/fork-check.py`, which runs after the fast tier, come on top, and so does one warm-up
described below, once per Go version on each machine. Before 2026-11-16 the bound was the 8,950
milliseconds of budgets; with change checks alone running to the ceiling it was 6 x 9,000 + 5,050
(the tree budgets) = 59,050.

**`go-build` builds with `-trimpath`.** Without it, Go caches compiled packages by their absolute
source path, so the first gate in every new checkout compiled from cold: 2.85 seconds alone, and
past the 9 second ceiling, with every item held, when ten new checkouts gated at once. Trimmed,
a new checkout reuses what any other checkout compiled (0.84 seconds). The flag is the gate's
compile check's alone, passed in `GOFLAGS` so the builds its test binaries start are trimmed too:
that check throws its output away, so trimmed paths change nothing that is kept, and recipes that
build binaries people run keep full paths. The first trimmed build on a machine still compiles
the standard library and every module from cold, 19.9 seconds measured, which would fault the
first gate after this change on each machine. So the loop's gate program
[`tools/gate/change-gate.py`](../../tools/gate/change-gate.py) runs the check once, unbounded and
before the fast tier, for each Go version, recording that it did in the git common directory's
`tillr-gate` store, so the cost lands on that first gate as time rather than as a fault. A Go
upgrade pays it once more.

**Measured on 2026-11-16**, on a candidate that changes Go and Markdown, with nothing
remembered, each gate in its own checkout, at a machine load of 32 to 83 on 32 cores (the same
run is the source of every number in this table). "Stable" checkouts are ten fixed paths that
have each built once, as the drain's lanes live at fixed paths; "new" checkouts are paths never
built before:

| Concurrent gates | Checkouts | Fast tier wall time (s) | Checks over budget |
| ---: | :--- | ---: | :--- |
| 1 | stable | 4.0 to 4.4 | none |
| 1 | new | 4.1 to 4.2 | none |
| 2 | stable | 4.2 to 4.3 | none |
| 2 | new | 3.9 to 4.4 | none |
| 10 | stable | 7.9 to 8.7 | `go-build` in every gate, up to 5.1 s against 2.7 |
| 10 | new | 8.0 to 9.0 | `go-build` in every gate, up to 5.3 s against 2.7 |

Before `-trimpath`, ten new checkouts took 12.4 seconds and every one stopped `go-build` at its
ceiling: exit 4. Now no gate in any row reaches a ceiling. `go-build` still dominates at ten
gates, now from contention for the processors rather than from a cold cache. The next costs are
about 1.1 seconds spent outside any check (the wall time less the checks' own durations: the
runner's planning and fingerprinting) and `documents` at 0.8 to 1.2 seconds. The budgets and the
cap are unchanged.

**Rejected by the owner's ruling of 2026-11-17** ([`D-GR15`](#decision-ledger)): **one ceiling
for the whole fast run** instead of one per check, where a fast run still going when it has used
9,000 milliseconds stops the check it is running and answers "could not judge". That would have
restored a bound of about 9 seconds of checks, at the cost of load faults returning. In the
measurement above the slowest gate's checks took about 7.9 seconds (its 9.0 seconds of wall time
less about 1.1 outside the checks), so no item would have been held; before `-trimpath`, every
gate of ten new checkouts would have been. The margin is under a second, so heavier load than
measured brings `gate-harness-error` back.

**Measured again on 2026-11-17**, on a five-lane drain at a load average of 18 to 24 on 32 cores,
by the drain's operator: `go-build` took 7,317 milliseconds against its 2,700 budget and
`documents` 5,006 against 2,000 when two gates overlapped, within about 1.7 seconds of the
per-check ceiling. Of the fast-check logs the loop's run directories held that day, `go-build`
had run 26 times and overrun 9 (2,916 to 7,317 milliseconds) and `documents` had run 59 times
and overrun 12 (2,228 to 5,006 milliseconds); no log said a check was stopped at its ceiling.
`documents` alone, over a change to the [tillr roadmap](../tillr/roadmap.md), takes over 2 seconds because that one
document renders in a median of 2.16 seconds. At ten lanes, checks that would have finished
would start to be stopped at the ceiling and their items held for an operator, which is the
failure [`D-GR15`](#decision-ledger) rules out.

**What happens to a check that outgrows its budget.** It stops holding landings the first time
it overruns, and it is still judged, after the merge. The heavy runner then runs that check
alone, with nothing else of its run beside it. When it exceeds its declared budget there too, the
heavy run is red on `gate-budget`, naming the check, with the measured and declared times. The
repair is an edit to that check's own file: a larger budget if the sum still fits, or `tier:
heavy`. A red here is handled like any other heavy red
([Section 6.8](#68-a-red-heavy-check)). That red is a failure made of an overrun alone, which
[`D-GR15`](#decision-ledger) rules out: it is not built, and where the heavy runner's slice
differs, the ruling governs. A change check that overruns, and while carrying is
suspended a tree check too, is not re-measured by anything: its `over_budget_ms` on the landing's
verdict is the record, and the repair is the same edit to its own file, or making it cheaper.

Two boundaries:

- **Change checks are never carried**, because nothing after the merge has their base. A change
  check that runs past its `budget_ms` is not stopped there. It runs to its **ceiling** *(coined
  here: the point a fast check that ran past its budget is finally stopped)*, which is its own
  `timeout_ms`, or the 9,000 millisecond fast-tier cap when it declares none, clamped to lie
  between its `budget_ms` and the cap; the admission check refuses a fast `timeout_ms` outside
  that range. A check that answers by then keeps its verdict, pass or fail. The verdict entry
  records `over_budget_ms`, how far past its budget it ran; its log says so, and `tillr-gate`
  prints a line naming the check beside the run's summary. The overrun is a defect against the
  check's declared cost. It is not "could not judge" for the candidate, because the check did
  judge it ([landing principle 8](landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)).
  Only a check still running at its ceiling makes the run "could not judge" (exit 4), naming the
  check. *Decided 2026-11-16 by the implementer* ([`D-GR13`](#decision-ledger)); *the owner's
  ruling of 2026-11-17 ([`D-GR15`](#decision-ledger)) supersedes the ceiling as a kill.* It was
  decided after `documents` ran 2,001
  milliseconds against its 2,000 budget in a drain pass at load 16, and the candidate was held as
  `gate-harness-error` until an operator cleared it. The same four-document change measured 1.8
  seconds alone and up to 2.4 seconds beside nine other gates, so a fixed deadline was failing
  items for the machine's load, which [`D-GR11`](#decision-ledger) rejects. Carrying the check
  instead was weighed and refused: no heavy runner exists yet to judge a carried check, and a
  deferred change check would land a change whose diff nobody judged. What it gives up is the
  bound: the worst case above.
- **Under the before-merge strategy nothing is carried.** An overrun there runs to its ceiling
  and keeps its verdict, as every fast check does while carrying is suspended; only a check still
  running at its ceiling is "could not judge".

As built on 2026-11-16 this is the one place the gate acts on elapsed time, and it is a statement
about the gate, which is the thing the ten-second ruling bounds. Under
[`D-GR15`](#decision-ledger) the gate measures and reports elapsed time and acts only on a stall.
The existing rule that no check may assert elapsed time about the
product stands unchanged.

**The proposed fast tier**, with budgets set from [Section 3.2](#32-every-check):

| Fast check | Scope | Measured (s) | `budget_ms` |
| :--- | :--- | ---: | ---: |
| `commit-identity` | change | 0.05 | 250 |
| `seed-freeze` | change | 0.01 | 100 |
| `citations` | change | 0.04 | 500 |
| `file-size` (added file) | change | 0.02 | 100 |
| `scratch` | tree | 0.08 | 250 |
| `design-citations` | tree | 0.04 | 250 |
| `structural` | tree | 0.23 | 500 |
| `pack-sync` | tree | 0.24 | 500 |
| `documents`, over the changed files | change | 1.66 | 3,000 |
| `go-build`, every module in one parallel step | tree | 1.39 | 3,000 |
| **Sum** | | | **8,450** |

Every other check is heavy. The membership rule is short: a change check is fast because only a
landing has its base; a tree check is fast while the sum fits; the test decides, not this table.

### 6.6 The strategy setting, the heavy tier and the green pointer

One tracked file, `config/tillr/gate-policy.json`, holds the repository's settings:

| Key | Values | Default when the file or key is absent | This repository |
| :--- | :--- | :--- | :--- |
| `landing_strategy` | `before-merge`, `after-merge` | `before-merge` | `after-merge` |
| `auto_revert` | `true`, `false` | `false` | `false` |
| `budget_ms` | milliseconds | 10,000 | 10,000 |
| `red_cap` | `landings` and `minutes`, each a non-negative integer | 20 landings, 60 minutes | 20 landings, 60 minutes |
| `lane_base` | `green`, `tip` | `green` | `green` |

The absent default is the side that cannot leave a trunk red. An unknown value is refused by
name and the loop does not start.

**The red cap.** Under `after-merge`, landing stops when the trunk tip is `red_cap.landings`
landings, or `red_cap.minutes` minutes, past the green pointer, whichever comes first. A refused
landing names the failing heavy check and waits for its fixer; lanes go idle and the range a fixer
must read stays small. A zero turns that bound off, and both zero means no cap. The cap exists
because an uncapped red range is a cost that grows with trunk activity, and a mechanism whose cost
rises with trunk activity is pointed the wrong way. Under `before-merge` the trunk is never red and
the cap never fires. ([OQ-GR1](#decision-ledger).)

**The lane base.** With `lane_base` set to `green`, a new lane's worktree is cut from the green
pointer, so its worker starts on a passing tree and does not spend its run on a failure it did not
cause; its change is squashed onto the tip when it lands. An item blocked by another waits until
the green pointer contains the blocker, usually a few minutes. A repository may set `tip`, which
cuts lanes from the newest trunk commit. With no green pointer yet, lanes are cut from the tip.
([OQ-GR2](#decision-ledger).)

**The runner's own changes.** A change under the gate runner's directories — `tools/tillr-gate/`
and `tools/gate/`, and `tools/fork-check.py` with `tests/fork-check.test.py` while that program
survives — lands only after the runner's suite passes, before the merge, under either strategy. It
is outside the ten-second budget. A wrong judge is the one red a revert does not repair: every
landing it judged stays judged. This is the fence of
[escalation ladder Section 5.2](escalation-ladder.md#52-the-fence-what-a-lane-may-not-edit), where
gate definitions change only as their own item. ([OQ-GR3](#decision-ledger).)

**The green pointer** *(coined here)* is the Git ref `refs/tillr/green`: the newest trunk commit
on which every available heavy check passed. It is not a branch and nothing is committed on it.

**Under `before-merge`** the landing gate runs the fast tier and then every heavy tree check
whose fingerprint has no remembered pass, on the candidate. The lander moves the green pointer
with the trunk.

**Under `after-merge`** the landing gate runs the fast tier only, and one **heavy runner**
*(coined here)* proves the trunk:

- **Trigger.** Each landing, and loop start when the trunk tip is not the green pointer. Runs are
  coalesced: one at a time, and when one ends with the tip moved, the next starts on the new tip.
  There is no clock and no landing count.
- **Where.** A detached worktree of its own at the tip it judges, never the main checkout.
- **What.** Every heavy tree check, plus any fast check carried by a landing in the range. A
  check with a remembered pass for that tip's fingerprint does not run, so the cost of a run is
  the checks whose inputs a landing changed.
- **Green.** When nothing failed, the runner moves the pointer to the tip it judged, by a
  compare-and-swap against the value it read. The pointer only moves to a descendant of itself;
  anything else is refused and reported, because the trunk was rewritten.
- **One writer.** Only the heavy runner moves the pointer under `after-merge`, and one lock file
  beside the result store admits one runner.
- **No pointer yet.** The first run is a whole-tree run. Until it is green there is no pointer,
  and [Section 6.7](#67-deploys-read-the-green-pointer) says what waits.

A green recorded with unavailable checks lists them on the status line beside the pointer. The
store row that holds the rolling watermark today is retired when the runner ships; the ref is the
only copy.

### 6.7 Deploys read the green pointer

"Deploy" here means the programs the drain loop and its children run: the eight `tillr` programs
built by `just tillr-build`.

- **The build reads the pointer's tree**, not the working tree and not the trunk tip. The loop's
  first line names the green commit it was built from.
- **With no pointer the build refuses**, naming the heavy run that would create one. A loop that
  is already running keeps the programs it loaded.
- **A red trunk does not stop a deploy of the last green commit**, and never produces a deploy of
  a newer one.

One path is outside this rule and is named so nobody assumes otherwise: the pack's rendered copy
is written on the host from the main checkout as it stands, by a command the owner runs. The
status line shows whether the main checkout is at the green pointer, so a render from a red tree
is visible before it is made; nothing refuses it.

### 6.8 A red heavy check

When a heavy run has a failing check, the pointer does not move. For each failing check the
runner then finds the **first red landing**: it runs that one check on the landings between the
pointer and the tip, by bisection, and remembered results make repeated steps free.

| What the bisection finds | What happens |
| :--- | :--- |
| Green at the pointer, red from landing L on | L is named on the status line with the check. With `auto_revert` off, one fixer item is filed against L, deduplicated by commit as today. |
| Red at the pointer too | The inputs did not change, so no landing is at fault. It is reported as a gate fault with the check's `needs` and `tools`, and no fixer is filed against a landing. |
| Red, then green with no input change | `flaky`, per [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint). |

**With `auto_revert` on**, the lander builds a revert of L and lands it through the same fast
gate; L's item returns to the queue as rework, as
[landing velocity](landing-velocity.md#7-a-rolling-red-attribution-and-remedy) ruled. A revert
that does not apply cleanly, because a later landing touched the same lines, is not forced: the
fixer is filed instead.

A check that reaches its timeout is "no verdict": the pointer does not move, nothing is
attributed, and the line says which check and how long it ran.

### 6.9 Upstream drift is a probe, not a check

Two of today's checks answer a question about a moving upstream: `series` and
`patch-branch-inv2` fetch, and ask whether the series still merges the release a launch would
build. In the run of [Section 3.1](#31-the-run), `patch-branch-inv2` for the pi fork was red
against release `v1.5.2` with no change in this repository.

A verdict about a commit must be a function of that commit. These are not, so they leave both
tiers. They run as a **drift probe**: at loop start and after any landing that changes a series
or a pack declaration. A red probe files one item naming the fork and the release, and never
moves or holds the green pointer. A series changed by a landing is still checked against its own
base by a heavy tree check, which needs no network.

## 7. Checked against the landing principles

| Principle | How this design meets it |
| :--- | :--- |
| [1, a refusal must be a verdict](landing-principles.md#1-a-refusal-must-be-a-verdict) | A change is refused only by a check that ran on it. Unavailable, over-budget and timeout are named and are not refusals; under [`D-GR15`](#decision-ledger) a check that is making progress is never timed out, and a stall is named. |
| [3, gate what will be the tip, once](landing-principles.md#3-gate-what-will-be-the-tip-once) | The fast gate runs once, on the candidate. A moved trunk is squashed onto, not re-gated. |
| [6, a budget escalates, it does not loop](landing-principles.md#6-a-budget-escalates-it-does-not-loop) | An overrun carries the check forward once; nothing is retried. |
| [8, a gate that cannot answer must say which](landing-principles.md#8-a-gate-that-cannot-answer-must-say-which) | Unavailable names the need and its command; "could not judge" names the check, and is never the answer for a check still making progress ([`D-GR15`](#decision-ledger)). |
| [13, a held state names a mover](landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act) | A pointer that is not moving shows the failing check and either a landing with a fixer, or a gate fault with the missing resource. |

## 8. Failure, concurrency and existing state

| Case | Behavior |
| :--- | :--- |
| No base passed to a run that includes change checks | Usage error, exit 2, naming the argument |
| The base is not an ancestor of the head | Exit 2: the range is not a change |
| No fast check is selected and no heavy check lacks a pass | The run is green and says nothing ran, with the count remembered |
| Two lanes gate at once | Independent; they share only the result store, which is keyed by content |
| Two landings finish while a heavy run is in flight | One further run starts when it ends, on the newest tip |
| The heavy runner is killed | Its lock is released by the process ending; results already written stay valid; the next trigger starts a run |
| A result file is unreadable | Treated as absent, and counted in the report |
| The result store is deleted | Every tree check runs once more; no verdict changes |
| A declaration is malformed | The run is not a verdict and names the file and field; no other check runs under a partial set |
| The existing record, its baselines and its run history | Left in place, read by nothing in the new path, deleted with the last of the gate program |
| The rolling watermark row in the store | Ignored once the pointer exists; the first heavy run is a whole-tree run |

## 9. What this costs, and what it does not do

- **The trunk may be red** from a landing until its heavy run ends, and with automatic revert
  off, until a fixer lands — never past the red cap of
  [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer).
- **Fewer checks stand before the merge** than today. A Go suite, the fork suites and the
  storybooks are all after it.
- **The membership rule of [landing velocity](landing-velocity.md#43-the-membership-rule) is
  replaced**: thirty seconds per check becomes 9,000 milliseconds for the tier, and "every ten
  landings or twenty minutes" becomes "after each landing, coalesced". That document needs a
  status note pointing here once this one is accepted.
- **It does not** change the squash, the land lock, the fence check, the judge, or the item
  model. It does not make a slow suite faster; it stops a slow suite from deciding when a change
  lands.
- **It does not** publish verdicts between machines, and it adds no retry on a clock, no cooldown
  and no attempt count. The one re-gate after a stall ([`D-GR16`](#decision-ledger)) waits on a
  named change, fewer gates in flight, and does not spend a worker's run.

## 10. Alternatives and risks

| Alternative | Verdict |
| :--- | :--- |
| Keep the remembered baseline and repair its cold start | Rejected. The fault is that the base is remembered at all; `--since` already exists and is the only form that never failed cold. |
| Remember results by commit, not by inputs | Rejected. A squash makes a new commit for the same content, so nothing would ever be reused across a landing. |
| Enforce the budget by timing every landing and failing slow ones | Rejected as the only enforcement. A slow box would refuse correct changes; the arithmetic test cannot flake, and the deadline carries instead of refusing. |
| Publish the result store on a Git ref | Rejected for now, per the warning in [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint). |
| Keep adding checks to the one program and shard its suite | Rejected. The suite reruns because every check shares the file; a faster 141 seconds is still a shared file. |

| Risk | Mitigation |
| :--- | :--- |
| A check reads a file it did not declare, and a stale pass is reused | The default is the whole tree; narrowing is per file and reviewed there. A whole-tree run with an empty store is the audit, and costs six minutes. |
| Under load, fast checks overrun and more proof moves after the merge | Every carry is recorded on its landing and counted on the status line; the exclusive re-measure tells a slow box from a slow check. |
| The heavy runner falls behind twenty lanes | Runs are coalesced, so it judges the newest tip, not every landing; a long gap stops landing at the red cap of [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer). |
| Moving checks out of the gate program drops one | Each moving slice asserts the union of the two planners' check ids equals the set before the move. |

## Decision Ledger

`D-GR1` to `D-GR6` are the owner's rulings of 2026-11-15. `D-GR7` to `D-GR12` are this design's
own decisions, made to carry them out; each is the owner's to overturn by comment. `D-GR13` and
`D-GR14` are an implementer's decisions of 2026-11-16, made while building the fast tier; the
owner ruled on both on 2026-11-17, and that ruling is `D-GR15`. `D-GR16` is this design's own
decision carrying `D-GR15` out, and is the owner's to overturn. The three rows of this
document's former open questions were settled by the session's designer on recorded principle,
as each row says.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-GR1` | The landing gate targets ten seconds per change. | 2026-11-15 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | — |
| `D-GR2` | Lanes run independently, and landings must outpace them at ten lanes and again at twenty. | 2026-11-15 | [Section 5](#5-the-arithmetic-at-ten-and-twenty-lanes) | — |
| `D-GR3` | The landing strategy is a per-repository setting with two values: heavy checks before the merge (the trunk is always green) or after it (the trunk is briefly red). | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | — |
| `D-GR4` | This repository takes the second value: heavy checks after the merge. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | — |
| `D-GR5` | tillr deploys only from the last trunk commit whose after-merge checks passed. | 2026-11-15 | [Section 6.7](#67-deploys-read-the-green-pointer) | — |
| `D-GR6` | Automatic revert of an offending landing is a setting, and it is off in this repository to start. | 2026-11-15 | [Section 6.8](#68-a-red-heavy-check) | — |
| `D-GR7` | A change check gets its base from the caller, runs once at landing, and never against a remembered commit. **Rejected:** repairing the remembered baseline's cold start. Structural fix. | 2026-11-15 | [Section 6.1](#61-the-caller-names-the-base) | — (the `--since` argument exists; the default path still reads the record) |
| `D-GR8` | A tree check's pass is remembered by a fingerprint of its declaration, the files it reads and the tools it names; a fail is recorded and never reused; the store is local to a machine. **Rejected:** remembering by commit, and publishing the store. Structural fix. | 2026-11-15 | [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint) | — |
| `D-GR9` | A check with an unmet machine need is `unavailable`, named with its command, and neither fails nor passes. Structural fix. | 2026-11-15 | [Section 6.3](#63-a-missing-machine-resource-is-named-not-failed) | — |
| `D-GR10` | A check is one declaration file with no verdict-changing default, read by `tillr-gate`; the gate program retires by attrition. **Rejected:** sharding the shared suite. Structural fix. | 2026-11-15 | [Section 6.4](#64-a-check-is-one-file) | — |
| `D-GR11` | The budget is enforced by an arithmetic test over declared costs and by a per-check deadline that carries an overrun to the heavy tier; a check that overruns alone turns the heavy run red on `gate-budget`. **Rejected:** failing landings on measured wall time. Structural fix. **Amended 2026-11-16** by `D-GR13` (a fast check is not stopped at its budget but at its ceiling) and `D-GR14` (carrying is suspended until the heavy runner exists); the arithmetic test is unchanged. **Superseded in part 2026-11-17** by the owner's ruling `D-GR15`: no deadline stops a check that is making progress, so the per-check deadline and carrying at it go, and an overrun alone never turns a run red; the arithmetic test stays. | 2026-11-15 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | Arithmetic test built; the deadline as amended |
| `D-GR13` | A fast check still running at its `budget_ms` runs on to its ceiling — its `timeout_ms`, or the 9,000 millisecond cap, clamped to lie between its budget and the cap — and keeps its verdict, with `over_budget_ms` recorded; only a check still running at its ceiling is "could not judge". The worst case is 11 x 9,000 = 99,000 milliseconds of checks. Decided by the implementer after `documents` overran by 1 millisecond under load and held an item; the owner has not ruled on it and may overturn it. **Rejected:** stopping a change check at its budget (load faults the item), and carrying it (nothing after the merge has its base). **Superseded 2026-11-17** by the owner's ruling `D-GR15`: the ceiling is no longer a kill, and a check still making progress is never "could not judge"; the `over_budget_ms` record stays. The alternative of one ceiling for the whole fast run is rejected by the same ruling. | 2026-11-16 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | Built; the superseded part is replaced by `item-gate-stall-watchdog` |
| `D-GR14` | Carrying a fast tree check to the heavy tier is suspended until the heavy runner exists: until then an over-budget fast tree check is run to its ceiling and keeps its verdict, as in `D-GR13`. The switch is `tillr-gate`'s `--tree-overrun` (`keep` by default), stated once in `tools/gate/change-gate.py` as `TREE_OVERRUN`; `item-gate-heavy-runner` turns it to `carry`. Decided by the implementer because with no heavy runner a carried check's judgment is dropped, and `go-build` was carried on almost every ten-lane landing; the owner has not ruled on it and may overturn it. **Rejected:** carrying with nothing to take what is carried, and answering "could not judge" at the budget. **Ruled on 2026-11-17** by the owner's ruling `D-GR15`: only a stall ends a check early, so `keep` is the rule rather than a suspension, and carrying, which stops a check at its budget, is not turned on as designed. | 2026-11-16 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | Built |
| `D-GR15` | **The owner's ruling, 2026-11-17:** "target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine." A budget is a target that is measured and reported; exceeding one is never by itself a failure, a fault or "could not judge". What ends a check early is a stall, a check making no progress, seen by a watchdog, and a stall leads to a named next step, never a silent kill. Machine load must not turn work that would have finished into a failure. **Supersedes** the ceiling as a kill and "could not judge" for a check still making progress (`D-GR13`), and the per-check deadline of `D-GR11`. **Keeps** the budgets, the admission test against the 9,000 millisecond cap, the `over_budget_ms` record, and the ten-second landing as a target with its measured distribution visible. | 2026-11-17 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | — (`item-gate-stall-watchdog`, `item-gate-wrapper-clocks-stall`) |
| `D-GR16` | A check shows progress in a one-second sample when its output grew or its process group's CPU time rose; 60 consecutive samples with neither is a stall. The runner ends a stalled check's process group and records `stalled`, which is neither pass, fail nor over-budget, with what it observed. The drain keeps the candidate and re-gates it once, when fewer gates are in flight than at the stall or no other gate is; a second stall on the same candidate holds the item, naming the check. `gate-harness-error` stays for a check that could not start or crashed. Settled by the session's designer on 2026-11-17, on `D-GR15` and [landing principle 11](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) (a re-run waits on a named change, never on time passing); the owner may overturn it. **Rejected:** a longer fixed timeout, which fails the same way at a higher load; re-gating on the next pass, which names no change. | 2026-11-17 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | — (`item-gate-stall-watchdog`) |
| `D-GR12` | A question about a moving upstream is a drift probe that files an item and never moves or holds the green pointer. **Rejected:** keeping it as a check on a commit. Made visible where it happens. | 2026-11-15 | [Section 6.9](#69-upstream-drift-is-a-probe-not-a-check) | — |
| OQ-GR1 | The default of a per-repository setting, `red_cap`: landing stops at 20 landings or 60 minutes past the last green commit; a repository may set a different bound, and zero means no cap. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are AGENTS.md's "a mechanism whose cost rises with trunk activity is pointed the wrong way" and the owner's 2026-11-15 ruling that the landing strategy is configurable per repository and red is brief. The owner may overturn it. **Rejected:** no cap, which lets the fixer's range and the deploy lag grow without limit. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | — (no slice yet enforces it) |
| OQ-GR2 | The default of a per-repository setting, `lane_base`: a new lane starts from the last green commit; a repository may choose the trunk tip. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are that a worker does not pay for a failure it did not cause, and the same cost-direction rule. The owner may overturn it. **Rejected:** always cutting from the tip. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | — (no slice yet enforces it) |
| OQ-GR3 | A change to the gate's own runner lands only after the runner's suite passes, before the merge, under either strategy. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principle is the fence of [escalation ladder Section 5.2](escalation-ladder.md#52-the-fence-what-a-lane-may-not-edit) (gate definitions change only as their own item), for the reason the 2026-11-05 ruling gave: a wrong judge is the one red a revert does not repair. The owner may overturn it. **Rejected:** landing it in ten seconds and re-running every landing's fast checks after a red suite. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | — (no slice yet enforces it) |

## 11. Build order

Nine slices. Each lands alone and leaves the gate working; each names the slice before it only
where it needs that slice's code. A slice whose fence holds `tools/tillr*` also holds the pack
mirror, because the two are compared at landing. A slice deletes its own block from this document
in the commit that lands it. Three behaviors of
[Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) have no slice yet: the
lander's refusal at the red cap, staging a lane from `lane_base`, and the runner's own changes
paying their suite before the merge. The policy slice reads and validates their settings.
Three more slices were filed on 2026-11-17 from the owner's ruling
[`D-GR15`](#decision-ledger) and the measurements beside it in
[Section 6.5](#65-ten-seconds-is-a-failing-test); they are the last three below.

### The runner reads one file per check



### A tree check that already passed does not run


**The store keeps a pass and a fail apart, so no writer can erase the other's record.** This
refines the result store of [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint)
for this slice, and where the two differ this block governs: a fingerprint has up to two record
files, one named for a pass and one named for a fail. A run that passed renames only onto the
pass file, and a run that failed renames only onto the fail file. No run rewrites, merges into
or deletes the other verdict's file. `flaky` is not stored: it is what a reader sees when both
files exist. A pass published while a fail is being published therefore cannot lose the fail,
in either order, and the store still has no lock and no order. Two writers of the same verdict
may replace each other, and that loses nothing the rules read: either file states the verdict
and a time it happened. **Rejected:** a per-fingerprint lock around read, merge and rename,
which breaks the no-lock rule that
[Section 5](#5-the-arithmetic-at-ten-and-twenty-lanes) rests on; and re-reading before the
rename, which only narrows the gap. Settled by this design's seat on 2026-11-16, on that
no-lock rule and on "a green re-run does not erase the red; it names it"; the owner may
overturn it.

**A pass is remembered only for files the check could have read as the judged tree states
them.** The fingerprint is taken from the judged tree, and the check's command reads the files
in the worktree, so the two can differ: a run of this slice on 2026-11-16 published a pass for
edited files under the fingerprint of the committed ones, and the next run on the clean tree
reused it. The rule, which refines
[Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint) and governs where the two
differ: once per run, before any lookup, the runner asks Git whether the worktree is the judged
tree. `HEAD`'s tree must be the judged tree, and `git status --porcelain --untracked-files=all`
names every path that differs from it, untracked files included, because a new test file changes
a suite as surely as an edited one. That command measured 77 milliseconds on this whole tree on
2026-11-16. A tree check whose `reads` covers a differing path (every tree check, when `HEAD`'s
tree is not the judged tree, and any check with no `reads`, when any path differs) is
**unbound** *(coined here: its fingerprint does not describe the files it will read)*. An
unbound check always executes, its result is its real verdict for the run, and the store is
neither read nor written for it: no remembered pass is taken, no pass file and no fail file is
published. Its report line says `not remembered` and names the first differing path. The
comparison is made again after the check exits and before its result is published, and a path
that differs then withholds the result the same way. A file changed and put back between the two
comparisons is not seen; a check that does that to its own inputs is a defect in its
declaration, the same side as an undeclared input. Files Git ignores are not compared, for the
same reason: a check that reads one has an input it did not declare. **Rejected:** refusing the
whole run on a worktree that differs, which changes what every run on an edited worktree does
today and is not this slice's to decide; and fingerprinting the worktree's bytes in place of the
judged tree's blob ids, which hashes every file under `reads` on every run and stores passes for
content no commit names. Settled by this design's seat on 2026-11-16, on "a fingerprint names
the inputs that were run"; the owner may overturn it.

**This slice is verified from `tools/tillr-gate/`, and the pack mirror by comparison.** The
suite is `go test -C tools/tillr-gate ./...`; the mirror is verified by
`python3 tests/pack-sync.test.py`, which compares the two trees byte for byte. Running the
suite from the mirror's directory is not a verification of this slice and its result does not
stop the work: measured on 2026-11-16 at commit `3e6eaba1e`, before any of this slice's code,
`go test -C yolo-packs/tillr/files/local/share/tillr/tillr-gate ./...` fails six tests, because
they open repository-relative paths such as `tools/gate/checks` that do not exist beside the
mirror. Nothing in the gate runs that command (`rg -n 'files/local/share/tillr' tools/fork-check.py tools/gate/`
returns nothing).


### A change check takes its base from the caller


**The loop's adapter is repository-only, and this slice's pack mirror is `tillr-gate` alone.**
`just pack-sync` mirrors the ten Go modules its recipe names, and `tools/tillr-loop/` is not one
of them: measured on 2026-11-16 at commit `ec2be54f7`, `ls yolo-packs/tillr/files/local/share/tillr/`
lists `tillr`, `tillr-cli`, `tillr-drain`, `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-run`,
`tillr-store`, `tillr-trace` and `tillr-view`, and `rg -n 'tillr-loop' .justfile tests/pack-sync.test.py`
returns nothing. The loop's adapter runs from the repository checkout and has no pack copy, and
neither do the declarations under `tools/gate/`. So a change to `tools/tillr-loop/` or
`tools/gate/` is complete with no file under `yolo-packs/tillr/`, and a missing
`yolo-packs/tillr/files/local/share/tillr/tillr-loop/` is the expected state, not a conflict
between a brief and the tree and not a reason to stop. A brief's general rule that a
`tools/tillr*` path carries a pack mirror applies to the modules the recipe mirrors, and is
met in this slice by running `just pack-sync` once and committing what it writes under
`yolo-packs/tillr/files/local/share/tillr/tillr-gate/`. The mirror is verified by
`python3 tests/pack-sync.test.py`, and the loop's adapter by `python3 tools/tillr-loop/test.py`.
**Rejected:** adding `tillr-loop` to the recipe and to the test's module list in this slice,
which ships a program no pack entry launches and needs `.justfile` and
`tests/pack-sync.test.py`, both outside this item's paths; and copying the adapter into the
pack by hand, which the byte-for-byte comparison would not cover. Whether the loop's adapter
should ship in the pack is a separate question this slice does not open. Settled by this
design's seat on 2026-11-16, on "the mirror is derived by the recipe, and the recipe names
what ships"; the owner may overturn it.


### A machine without the forks is told what it lacks



### The fast tier is the ten checks that fit



### The policy file names the strategy

Built: this landed in commit `431d78efe`.

### One runner proves the trunk and moves the green pointer

<!-- tillr: item
     id="item-gate-heavy-runner"
     paths="tools/tillr-drain/,tools/tillr-gate/,tools/tillr-view/,yolo-packs/tillr/,docs/design/gate-rework.md"
     blocked_by=""
-->

- **c1:** Under `after-merge`, each landing triggers a heavy run in a detached worktree at the trunk tip. A test lands three fixture changes while one run is held open and asserts exactly one further run starts, on the newest tip.
- **c2:** A run with no failing check moves `refs/tillr/green` to the tip it judged by compare-and-swap. A test asserts the ref, asserts a second runner started beside the first exits without running, and asserts a pointer that is not an ancestor of the tip is refused and reported.
- **c3:** A run with a failing check leaves the pointer unmoved and names the first red landing, found by running that one check on the landings in the range. With `auto_revert` false one fixer item is filed against that landing. A check that is red at the pointer too is reported as a gate fault and files no fixer. A test covers the three cases.
- **c4:** A fast check carried by a landing in the range runs in the heavy run alone. When it exceeds its `budget_ms` there, the run is red on `gate-budget` naming the check and both times. The test supplies the measured time through the runner's injected clock.
- **c5:** `tillr-view status` prints the green commit, the number of landings since it, each failing check with its first red landing, and the unavailable checks. The rolling watermark row is no longer read.

### tillr builds from the green pointer

<!-- tillr: item
     id="item-gate-deploy-from-green"
     paths=".justfile,tools/tillr-drain/,tests/,yolo-packs/tillr/,docs/design/gate-rework.md"
     blocked_by="item-gate-heavy-runner"
-->

- **c1:** `just tillr-build` builds the eight programs from the tree of `refs/tillr/green`. A test sets the pointer one commit behind a trunk tip that changes a program's version string and asserts the built program reports the older string.
- **c2:** With no green pointer the recipe exits non-zero, names the heavy run that creates one, and builds nothing.
- **c3:** The loop's first line names the green commit its programs were built from, and `tillr-view status` says whether the main checkout is at the green pointer.

### Twenty lanes land without waiting on each other

<!-- tillr: item
     id="item-gate-scale-test"
     paths="tools/tillr-drain/,tools/tillr-land/,yolo-packs/tillr/,docs/design/gate-rework.md"
     blocked_by="item-gate-heavy-runner"
-->

- **c1:** A test drives the loop on a scratch repository with ten lanes and then with twenty, each lane's run a stub that commits one file in its own directory, until every lane has landed five changes. Every change lands.
- **c2:** The test asserts from the recorded events that the number of gates in flight at once reached the lane count, and that the land lock was held only between a squash's start and its trunk move. It asserts no duration.
- **c3:** The loop's summary prints the longest and the median land-lock hold and the number of landings per heavy run. The test asserts the fields are present.
- **c4:** No lane's gate reads or writes a file another lane's gate writes, other than the result store. The test asserts it from the declared `writes` of every check and from the files the run created.

### Prevent a gate check from being stopped while it makes progress

The owner ruled on 2026-11-17 that a time limit in the gate is a target, not a kill: "target
budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish
just fine." Today the gate's runner does the opposite. A fast check that runs past its budget is
allowed on to a ceiling of nine seconds, and one still running there is stopped and answered as
"could not judge". The drain then holds the item as a harness fault until an operator clears it by
hand, although nothing was wrong with the change or the check. On a five-lane drain that day the
compile check took 7.3 seconds and the documents check 5.0 seconds when two gates overlapped,
about 1.7 seconds short of that ceiling, so at ten lanes working checks will start to be stopped
and their items held. This item replaces the clock with a watchdog. A check runs for as long as
it is making progress, which means writing output or using CPU time. Only a check that has gone
silent and idle for a minute is stalled. A first stall is gated again by the loop once the machine
is less busy, with no operator, and only a second stall on the same candidate holds the item,
naming the check. Budgets stay, and every overrun stays visible, so a check that is chronically
slow is made faster rather than tolerated.

<!-- tillr: item id="item-gate-stall-watchdog" paths="tools/tillr-gate/,tools/gate/,tools/tillr-drain/,tools/tillr-view/,tools/tillr-loop/,tests/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** No check `tillr-gate` runs is stopped because a clock passed while it is making progress. The fast tier's ceiling, a heavy declaration's `timeout_ms` and a generated declaration's timeout stop being kill deadlines; a heavy `timeout_ms` becomes that check's target, recorded like a fast budget when exceeded, and the admission check refuses a fast declaration that names `timeout_ms`. A test runs a stub check that prints one line per sample of an injected clock for longer than its budget, its `timeout_ms` and the 9,000 ms cap, and asserts it finishes with its own verdict and its overrun recorded.
- **c2:** The runner samples each running check every 1,000 ms of an injected clock. A sample shows progress when the check's log grew by at least one byte since the last sample, so a check may print a heartbeat line, or when the CPU time of its process group rose (user plus system over every live member, plus what reaped children added). 60 consecutive samples without progress is a stall. The interval and the count are one constant each, pinned by a test. Where the group's CPU time cannot be read, the runner declares no stall and records `watchdog unavailable` in the verdict entry and on the summary line. Readings come through an injected sampler, so no test sleeps or burns CPU. Tests: silent with rising CPU, and silent with a child whose CPU rises, are not ended; no output and no CPU for 60 samples is ended; 59 samples is not.
- **c3:** On a stall the runner kills the check's process group and records the result `stalled`, distinct from pass, fail, error and over-budget, with the last output line, the group's CPU time, the number of silent samples and the run time, in the verdict entry, the check's log and a summary line. A stalled check passes and fails nothing about the candidate. The drain tells a stall from a harness error by what the gate records, through `tools/gate/change-gate.py` and `tillr-gate run --gate`. Whether that is an exit code of its own or a field of the verdict the drain already reads is the implementer's choice, and exit 4 keeps meaning a check that could not start or crashed. A test runs the drain's real gate adapter with a stalling stub check and asserts the drain reads a stall.
- **c4:** The drain does not fault an item on a first stall. It keeps the candidate (no new worker run, no attempt charged, no failing verdict, no design conflict), records the waiting reason `gate-stall-rerun` naming the check and the number of gates in flight when it stalled, and gates the same head again on the first pass at which fewer gates are in flight than that number, or no other gate is. A test with a stub gate that stalls once and then passes asserts the item lands with one worker run, and that the second gate started only after the in-flight count dropped.
- **c5:** A second stall on the same candidate, the same head tree, holds the item as an item fault with reason `gate-stalled`, naming the check and both stalls' observations, cleared by `tillr drain clear-fault` like any item fault. `gate-harness-error` remains only for a check or gate program that could not be started or crashed. A test asserts two stalls give `gate-stalled` and a check that cannot start gives `gate-harness-error`.
- **c6:** Every `budget_ms` and the 9,000 ms cap are unchanged. The cap is only the sum the admission check holds the budgets to, and `rg -n FastTierCapMS tools/tillr-gate --glob '!*_test.go'` finds no use of it as a deadline. Every fast check's duration, budget, overrun and stall reaches the store the observatory reads, so its per-check performance payload (median, p95, max) covers the fast tier and adds each check's budget and how many of its recent runs overran. A test asserts the payload for a fast check carries both.
- **c7:** Section 6.5 of this design replaces the ceiling and its worst-case arithmetic with the stall rule as built. The gate README, the drain README, the `tillr-operating` skill, the waiting view's rules for the two new reasons (`TestWaitingEveryDrainReasonHasARule` passes) and the observatory's stop-reason words name `stalled`, `gate-stall-rerun` and `gate-stalled`. `rg -n -i 'ceiling' tools/tillr-gate/README.md` finds no sentence saying a check is stopped there.
- **c8:** No test asserts elapsed time: the timing audit, case 53 of `tests/fork-check.test.py`, passes unchanged. The report records, as command, result and date, one real fast-tier run beside nine concurrent gates, with each check's duration and whether any was stopped.

**Agent details:**

- **Ref:** this document's [Decision Ledger](#decision-ledger), rows `D-GR15` (the owner's ruling) and `D-GR16` (the stall rule and its next step), and [Section 6.5](#65-ten-seconds-is-a-failing-test); [landing principle 8](landing-principles.md#8-a-gate-that-cannot-answer-must-say-which) and [landing principle 11](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), which is why the re-gate waits on a named change, fewer gates in flight, and not on time passing.
- **Where:** `runCheck`, `fastCeilingMS`, `FastTierCapMS` and `budgetNotes` in `tools/tillr-gate/run.go`; `checksFace` in `tools/tillr-gate/checks.go`, whose verdict goes to `change-gate.py`'s standard output today, so the outer verdict carries one check, `gate:change-gate.py`; `runGateProgram` and `gateResult` in `tools/tillr-gate/gate.go`; `tools/gate/change-gate.py`; `LIMIT_MS` and the fast `timeout_ms` range refusal in `tools/gate/checks/gate-budget.py`; `ProcessSteps.Gate` in `tools/tillr-drain/drain/steps.go`, the `GateErrored` branch of `verify` in `tools/tillr-drain/drain/loop.go` and `gateFault` in `tools/tillr-drain/drain/item_fault.go`; `waitingRules` in `tools/tillr-view/waiting.go`; the check performance payload in `tools/tillr-view/performance.go`, which reads `gate_spans`; the stop-reason words in `tools/tillr-view/web/src/components/Home.tsx`.
- **Evidence:** 2026-11-17, read in `runCheck`: on an expired deadline a fast check that is not carried and a heavy check are both `error` ("no verdict"), and a check from a generated registry is `fail`. The drain maps the gate's exit 4 to `gateFault`, which holds the item as `gate-harness-error` until `tillr drain clear-fault`.
- **Evidence:** 2026-11-17, `rg -n 'SPANS_OUT|spans' tools/tillr-gate/*.go -g '!*_test.go'` returns nothing: the fast tier writes no spans, so the observatory's per-check numbers do not cover it yet.
- **Evidence:** 2026-11-17, `rg -o 'check "[a-z-]+" ran [0-9]+ ms, over its budget_ms [0-9]+' --no-ignore -g '*.log'` over the loop's run directories: `go-build` ran 26 times and overran 9 times (2,916 to 7,317 ms against 2,700), `documents` ran 59 times and overran 12 times (2,228 to 5,006 ms against 2,000); `rg -l 'stopped at its ceiling'` over the same logs finds none. The drain's operator measured the 7,317 and 5,006 ms runs at a load average of 18 to 24 on 32 cores, with two gates overlapping.
- **Known remaining clocks, not in this item:** the gate program's 30-minute `--gate-timeout` and the timeouts inside `tools/fork-check.py` are `item-gate-wrapper-clocks-stall`. These already lead to a named next step, or are not checks, and stay as they are:
  - the drain's whole-gate budget, `DefaultGateBudget` of 330 s in `tools/tillr-drain/drain/timing.go`: it is measured per tick and printed on the tick line, and an overrun files one `gate-budget-<day>` regression item. It stops nothing.
  - the run wall and its lease: no backend sets a wall (`D-RW1` in [the run-wall ruling](run-wall-ruling.md), and `config/tillr/backend-policy.json` names no `timeout_seconds`). An explicit wall on a run that committed goes to the gate (`D-RW2`), and one that did not commit is resumed once (`D-RW3`). The lease is the wall plus `LeaseGraceMS`, 300,000 ms, only when a wall is set, and has no expiry otherwise.
  - the lander's `--lock-timeout`, which defaults to 0 (wait), and `tillr-loop` passes none.
  - the Vantage index's two-minute bound, `IndexTimeout`: on expiry the item is held as `questions-unknown` and the index is read again on the next pass.
  - `tillr-run`'s pause straggler bound (120 s by default): on expiry the adapter's group is killed and the run continues as a resumed segment.
  - `SpendExtractorTimeout` (60 s): on expiry the spend is recorded as unknown, with a note, and the run's outcome is unchanged.
  - SQLite's 5,000 ms busy timeout is a lock wait, not a check's limit, and is handled by keeping work out from under the store's locks.

### Route the gate program's own time limits through the stall watchdog

The owner's ruling covers every clock whose only consequence is a kill, and two remain around
the gate after the stall watchdog item. The gate program itself runs under a 30-minute bound in
`tillr-gate run`. Inside it, the older check program that still runs the landing tier, and the
rolling run after each landing, give every kind of check a fixed timeout, from one minute to an
hour. When any of these expires, the check is killed and reported as unable to answer. In a
landing that becomes "could not judge", and the item is held for an operator. In the rolling run
the trunk is left unproved. Those timeouts are generous today, so they fire rarely. They fail in
the same way as the fast tier's ceiling did, though, only later: a long suite on a loaded machine
can pass its limit while still working. This item puts the same watchdog, with the same rule for
what counts as progress, around both programs, so a working suite always finishes and a silent one
is named as stalled.

<!-- tillr: item id="item-gate-wrapper-clocks-stall" paths="tools/fork-check.py,tests/fork-check.test.py,tools/gate/,tools/tillr-gate/,tools/tillr-drain/,yolo-packs/tillr/,docs/" blocked_by="item-gate-stall-watchdog" prose="2" -->

- **c1:** In `tools/fork-check.py`'s landing and rolling tiers, a check's timeout (the per-kind constants such as `DOCUMENTS_TIMEOUT` of 300 s, `TILLR_DOCS_TIMEOUT` of 600 s and `BUILD_TIMEOUT` of 3,600 s) no longer kills a check that is making progress. Progress is the rule of `item-gate-stall-watchdog`: output grew, or the process group's CPU time rose, in a sample, with the same interval and count. A test fails when the Python and Go constants differ.
- **c2:** A stalled check there ends its process group and is reported as a stall naming the check, its last output line and how long it was silent, apart from the existing could-not-answer exits. `tools/gate/change-gate.py` carries it out as a stall, so the drain applies the stall handling of `item-gate-stall-watchdog` and not `gate-harness-error`. A test drives it with an injected sampler.
- **c3:** `tillr-gate run --gate`'s `--gate-timeout` (30 minutes by default) no longer kills a gate program that is making progress. The same watchdog watches the program's process group. The flag is removed, or kept as a target reported on the summary line when exceeded; the choice is the implementer's, stated in the gate README. A test runs a stub program that makes progress past the flag's value on the injected clock and asserts it finishes with its verdict.
- **c4:** A stalled rolling run advances no watermark and names the stalled check. A second consecutive stall of the same check over the same range files one deduplicated trunk fixer naming it, as a red rolling run does. A test asserts both.
- **c5:** The callers stop passing `--lock-timeout 30`, which `tools/fork-check.py` accepts and ignores: `tools/gate/change-gate.py` and the drain's default rolling program. The drain comment that calls the flag "not decoration" is corrected, and the rolling test that requires the flag is inverted.
- **c6:** The timing audit, case 53 of `tests/fork-check.test.py`, passes, and no new test asserts elapsed time. The fork-check docstring's statement that a timeout is an inability to answer, and the gate README, say what a stall is. The report lists every remaining wall-clock bound in `tools/fork-check.py`, `tools/gate/change-gate.py` and `tillr-gate`, with what its expiry now does.

**Agent details:**

- **Ref:** this document's [Decision Ledger](#decision-ledger), rows `D-GR15` and `D-GR16`; [landing principle 8](landing-principles.md#8-a-gate-that-cannot-answer-must-say-which).
- **Where:** `_run_cancellable`, `run_command` and `run_split` in `tools/fork-check.py`, which answer exit 124 on `subprocess.TimeoutExpired`; `COULD_NOT_ANSWER_CODES` and `Check.conflict` there; the per-kind timeout constants beside each kind; `tools/gate/change-gate.py`, which maps any fork-check exit other than 0 and 1 to 4; `runGateProgram` in `tools/tillr-gate/gate.go` and the `gate-timeout` flag in `tools/tillr-gate/main.go`; `DefaultRollingProgram` in `tools/tillr-drain/drain/trunk.go` and its test in `tools/tillr-drain/drain/rolling_test.go`.
- **Evidence:** 2026-11-17, read in the tree: an expired fork-check timeout returns 124, `COULD_NOT_ANSWER_CODES` is `(124, 125, 126, 127, 137)`, a check with one of those codes is never a verdict, and the run exits 2, which `change-gate.py` turns into 4. In the rolling run an unknown answer advances nothing. `runGateProgram` kills the program's group at `--gate-timeout` and answers `error`, "nothing was judged".
- **Evidence:** 2026-11-17, `tools/fork-check.py`'s own help says `--lock-timeout` is "ACCEPTED AND IGNORED" because the record is append-only, while `DefaultRollingProgram` and `change-gate.py` still pass `--lock-timeout 30`.

### Prevent the documents and Go build checks from overrunning their budgets

Two of the fast checks now run past their budgets even when the machine is quiet, so their
overruns cannot be blamed on load. The documents check renders every Markdown file a change
touches. The tillr roadmap is now about 149 kilobytes and changes in roughly a third of all
commits, and it alone takes over two seconds to render, against the check's two-second budget.
The check remembers passes by each document's exact content, so the one document a landing just
edited is always new to it and is rendered again every time. The Go build check compiles every
Go module in the tree on every landing that is not an exact repeat, because its declaration reads
the whole tree, so a change to one Markdown file rebuilds all ten tools. Neither budget may be
raised to make room, because the fast tier's budgets already sum to 8,950 of its 9,000
milliseconds. This item finds what each check spends its time on and removes the waste.

<!-- tillr: item id="item-gate-fast-checks-fit-budgets" paths="tools/gate/,tools/tillr-gate/,tests/,yolo-packs/tillr/,docs/" blocked_by="" prose="2" -->

- **c1:** The report states the measured cause for the documents check, as command, result and date: which documents dominate its time on recent landings, which part of the renderer's work costs that time for the tillr roadmap (start-up, a rule, link resolution or size), and why the remembered passes miss on an ordinary landing.
- **c2:** The report states the same for `go-build`: which modules compile on an ordinary landing, and why its remembered tree pass is not reused.
- **c3:** The documents check's median over a change that edits the tillr roadmap is under its 2,000 ms budget with no other gate running. The report records the median of five runs before and after. The fix is the implementer's choice among measured options, for example rendering only what the change can affect, or moving the roadmap's filed-item sections into documents that change less. If item blocks move, `tillr-item sync --dry-run` reports no item created, updated or dropped.
- **c4:** `go-build`'s median over a change to one Go module is under its 2,700 ms budget with no other gate running; the report records the median of five runs before and after. A change that touches no Go module reuses `go-build`'s remembered pass: a test with a Markdown-only change asserts the check is reported remembered and starts no compile.
- **c5:** Each check still catches what it caught before. A test plants a broken link in the roadmap and asserts the documents check fails, and plants a compile error in one module and asserts `go-build` fails.
- **c6:** No `budget_ms` changes and the 9,000 ms cap is unchanged; `gate-budget` passes on the result. No test asserts elapsed time: the timing audit, case 53 of `tests/fork-check.test.py`, passes.

**Agent details:**

- **Ref:** this document's [Section 6.5](#65-ten-seconds-is-a-failing-test), the 2026-11-17 measurement, and [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint) for how a tree check's pass is remembered.
- **Where:** `document_keys` and `judge_documents` in `tools/gate/checks/change_common.py`; `tools/gate/checks/documents.json`; `tools/gate/checks/go-build.py` and `tools/gate/checks/go-build.json`, whose `reads` is `["."]`; the fingerprint rule in `tools/tillr-gate/`.
- **Evidence:** 2026-11-17, at a load average of 6.5 to 7 on 32 cores, median of five runs: `uvx vantage-check check --format json docs/tillr/roadmap.md` 2.16 s (2.00 to 2.35); the same over `docs/design/gate-rework.md` (66,393 bytes) 0.68 s and over `docs/design/run-wall-ruling.md` (19,159 bytes) 0.37 s; `uvx vantage-check version` 0.23 s. `wc -c docs/tillr/roadmap.md` is 148,607 bytes, so the time per kilobyte is about twice that of the 66 KB document.
- **Evidence:** 2026-11-17, `git log --since=2026-11-14 --format=%h -- docs/tillr/roadmap.md | wc -l` is 112, against 365 for `git log --since=2026-11-14 --format=%h`.
- **Evidence:** 2026-11-17, a document's remembered-pass key includes its own blob (`document_keys`), and the check judges only the documents the change touches, so a remembered pass is reused only when the same content is gated again.
- **Evidence:** 2026-11-17, `python3 tools/gate/checks/go-build.py` in the main checkout: 2.72 s on the first run after the working tree's Go files changed, then 0.88 and 0.89 s with nothing changed. The drain's operator measured 2.7 to 3.3 s on warm lanes the same day. The check builds and compiles the tests of every module under ten tool directories on every run that is not remembered.
