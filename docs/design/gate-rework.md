---
title: "The gate lands a change in ten seconds and remembers nothing it cannot recompute"
status: accepted
stage: DECIDED
updated: 2026-11-22
tags:
  - tillr
  - gate
  - landing
  - design
summary: "Measures all 42 check kinds in tools/fork-check.py, then replaces the gate's remembered baseline with a base the caller supplies and results remembered by a fingerprint of the files each check reads. A ten-second landing budget becomes a failing test, heavy checks run after the merge in this repository, tillr deploys only from the last green trunk commit, and a new check is one new file."
next: "Two units of Section 11 are written with no item directive and become items once the owner rules on how documents declare work: the single check-result classifier of D-GR23 and D-GR41, in thirteen slices (slices 1a and 1e landed), and the drift probe of D-GR12, after item-gate-pinned-fork-inputs."
depends-on:
  - landing-principles.md
  - landing-velocity.md
  - queue-persistence-ruling.md
vantage:
  status-chip: true
---

# The gate lands a change in ten seconds and remembers nothing it cannot recompute

**Status:** 2026-11-15; reconciled against the tree at `5196f5f12` on 2026-11-19. Every slice of
[Section 11](#11-build-order) has landed, and so have the three behaviors that had no slice there
(the red cap, the lane base and the runner's own changes paying first, built from
[the tillr roadmap](../tillr/roadmap.md#filed-2026-11-15-the-three-gate-rulings-no-slice-builds)); the
[Decision Ledger](#decision-ledger)'s `Built` column names the code for each. One decision is not
built: the drift probe of `D-GR12`, because `series` and `patch-branch-inv2` are still
landing-tier checks (`TIER_LANDING` in `tools/fork-check.py`). That remainder is written as [the last unit of the build order](#move-the-upstream-drift-checks-out-of-the-gate-into-a-probe). The disabled-check rule of `D-GR21` and `D-GR22` is in progress: on 2026-11-21 the designer listed the 70 places a check result is classified and decided that one function classifies for all of them (`D-GR23`, amended by `D-GR28`, `D-GR32` to `D-GR34`, `D-GR36`, `D-GR37` and `D-GR41`, each the owner's to overturn), with [its build order in thirteen slices](#classify-every-check-result-in-one-place) (slices 1a and 1e landed in `020cf29eca` and `0bcc3275a2`). Every timing is from one run made for this design at
`ebaab2794`, stated in [Section 3](#3-what-exists-today-measured); the six owner rulings of
2026-11-15 are rows `D-GR1` to `D-GR6` of the [Decision Ledger](#decision-ledger) (`D-GR6` was
overturned by the owner on 2026-11-17: automatic revert is on here). Its three former open
questions ([`OQ-GR1`](#OQ-GR1) to [`OQ-GR3`](#OQ-GR3)) were initially settled the same day by the
session's designer on recorded principle: [`OQ-GR3`](#OQ-GR3) stands, while the owner overturned
[`OQ-GR2`](#OQ-GR2) on 2026-11-17 (lanes start on `tip`), then ruled on 2026-11-21 that new work
starts from the last proved trunk commit and only after its blocker's landing is proved
([`D-GR24`](#decision-ledger); this repository sets `green`), and
ruled [`OQ-GR1`](#OQ-GR1) out on
2026-11-18 ([`OQ-GP7`](tillr-graph-is-the-path.md#OQ-GP7); the 20-landing cap stays live until
that waiting rule is built). On 2026-11-17 the owner ruled on time limits in the gate
([`D-GR15`](#decision-ledger)): a budget is a target, nothing is carried at a budget, and only a
stall ends a check. On 2026-11-19 and 2026-11-20 the owner's orphaned-shard request added
[`D-GR17`](#decision-ledger): a test that fails or hangs under load is fixed at its cause without
adding a timeout, retry, or longer wait, and a test run started by a worker must not outlive the
command that started it. On 2026-11-21 the owner ruled that ten seconds was aspirational and
accepted about 40 seconds for the tests a drain change selects, with performance monitoring
([`D-GR38`](#decision-ledger)); the check that runs those tests before the merge is the
designer's ([`D-GR39`](#decision-ledger),
[Section 6.12](#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands))
and is not built.

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
| A landing gate targets **ten seconds per change** (the owner on 2026-11-21: "10 seconds was apsirational", [`D-GR38`](#decision-ledger)) | The fast tier's budget, [Section 6.5](#65-ten-seconds-is-a-failing-test) |
| Lanes run independently, and landings must **outpace them at ten lanes and again at twenty** | Nothing a landing waits for may be shared between lanes except the squash itself, [Section 5](#5-the-arithmetic-at-ten-and-twenty-lanes) |
| The landing strategy is a **per-repository setting with two values**: heavy checks before the merge, so the trunk is always green, or after it, so the trunk is briefly red | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) |
| **This repository takes the second** | The trunk here may be red between a landing and its heavy checks |
| tillr **deploys only from the last trunk commit whose after-merge checks passed** | The green pointer, [Section 6.7](#67-deploys-read-the-green-pointer) |
| **Automatic revert** of an offending landing is a setting, **off here to start** (overturned 2026-11-17: on here) | A red the bisection pins on one landing is reverted by tillr; a fixer is filed only when the revert is not possible, [Section 6.8](#68-a-red-heavy-check) |

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
  seed", "does an added line cite an unreachable commit", "are the author and committer of each
  added commit allowed by this repository", "did this change add an oversized file". It is not a check that happens
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

#### Which identities this repository accepts

The owner's 2026-11-20 ruling (`D-GR18` in the [Decision Ledger](#decision-ledger)) is about
**checking a commit**, not choosing the identity to use when making one. On the operator's
request, the designer specifies the following build; the owner may overturn these details.
For each commit in the existing range, check the author and committer separately against one
tracked list of exactly two `(name, email)` pairs:

- Personal: `Matthew Schulkind <owner@example.invalid>`.
- Professional: `Matthew Schulkind <owner@work.example.invalid>`.

The name comes from [`gitconfig`](../../gitconfig); the addresses come from the
[personal](../../tag-personal/gitconfig.local) and [work](../../tag-work/gitconfig.local) configs.
Either pair passes in either role, regardless of which machine runs the check. Name the offending
commit if either role has any other pair. Still refuse `user.name` or `user.email` in local or
worktree config, even when the value matches the list.

The filed request names the [standalone check](../../tests/git-identity.test.py). Changing that
alone would leave the live gate comparing both roles to the current machine's identity. The
build fence must also include `tools/gate/checks/`: the [`commit-identity` declaration](../../tools/gate/checks/commit-identity.json)
runs [this entry point](../../tools/gate/checks/commit-identity.py), which calls `identity_findings`
in [the change checks](../../tools/gate/checks/change_common.py). Use the same tracked list for both
checks; keep the gate's caller-supplied `base..head` and the standalone script's existing base
selection and `base..HEAD`. Exercise both allowed identities, including different allowed pairs
on one commit, a third identity in either role, and config overrides in the [gate tests](../../tools/gate/checks/change_checks_test.py)
and standalone tests.

Update [the repository instructions](../../AGENTS.md) to distinguish this filter from authorship:
when creating a commit, still read the identity from Git config; never type it. Leave the
pre-commit hook, tillr's lander and its upstream-history checks unchanged. The pair list is a
policy of this repository, not a tillr setting or default.

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
- **A fail is recorded and never reused.** It runs again on the next request.
- **A check with a pass and a fail on one fingerprint is disabled** (`D-GR21`, the owner's ruling
  of 2026-11-21; the mechanism is `D-GR22`). The owner's words: "The correct move for a flaky test
  is disable it, file an item to fix it, and don't let it block anything. It is not considered
  green, but it also doesn't let you block other things." The rule is stated in full under
  [A contradicted check is disabled](#a-contradicted-check-is-disabled) below. It replaces this
  section's earlier rule, built in `annotateMemory` of `tools/tillr-gate/memory.go`, that such a
  run "passes and reports the check as `flaky`": that rule counted the check as passed, which let
  a later pass stand in for the earlier fail.
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


#### A contradicted check is disabled

The owner ruled on 2026-11-21, in two statements quoted verbatim: "The correct move for a flaky
test is disable it, file an item to fix it, and don't let it block anything. It is not considered
green, but it also doesn't let you block other things." and "And the principle here is you make
the trunk green by disabling the flaky test. The test is not green because it's flaky, but the
trunk is green because the test is no longer included.And yes, the item that is filed should be
to re-enable it correctly, the test that was flaky." Together those two statements are `D-GR21`.
It replaces three earlier answers: this section's `flaky` pass, the live re-run of
[`OQ-ER3`](./tillr-evidence-registry-and-suite-guardrail.md#OQ-ER3) option A, and a filed
operator request of 2026-11-20 that asked for a red verdict.

A fingerprint is **contradicted** *(the term is from
[the evidence registry design](./tillr-evidence-registry-and-suite-guardrail.md#42-when-an-entry-is-contradicted-on-the-same-fingerprint-oq-er3))*
when the result store holds both a pass record and a fail record for it: the same inputs gave two
verdicts. A **disabled check** *(coined here)* is one the gate does not run for a verdict.

The operator's reading of the first statement, sent with it on 2026-11-21, is recorded as the
operator's and not as the owner's words (its third and fifth points — making the trunk green by
excluding the disabled check while the check itself is not green, and filing the item to
re-enable the flaky check correctly — are now directly ruled in the owner's second statement of
2026-11-21 above; the owner may overturn any remaining part of the reading):

- a check whose fingerprint is contradicted is disabled, and is not run for a verdict;
- it is reported as disabled, and never as passed or green;
- it refuses no landing, and it does not turn the trunk red;
- one deduplicated repair item is filed, carrying both times and both output locations;
- the check is enabled again when that repair lands.

What follows is the designer's mechanism for that reading (`D-GR22`, decided on 2026-11-21). None
of it is the owner's ruling, and the owner may overturn it.

- **The verdict has a result of its own.** A disabled check's result is `disabled`. It is neither
  `pass` nor `fail`: the run's summary counts it apart ("1 disabled"), it is left out of the
  passed and remembered counts, and the run's overall verdict is computed from the other checks.
  The report line names the check, the time and output location of the pass, the time and output
  location of the fail, and the repair item. This holds for the run that writes the second
  record too: the run that discovers the contradiction reports `disabled`, whichever of the two
  verdicts it produced itself.
- **Each stored record keeps its output location.** A record gains the path of the check's output
  log (`log_ref`, the name the verdict already uses). A record written before this field exists
  has none, and the report says "output location not recorded" for it instead of omitting the
  check.
- **Disabling is by check, not by fingerprint.** When a run finds a contradicted fingerprint for a
  check, it writes one marker file for that check id under `disabled/` in the result store,
  holding the fingerprint, both times and both output locations. While the marker exists the
  check is disabled on every fingerprint. Deriving the state from the fingerprint alone was
  rejected: an unrelated change to the files the check reads gives a new fingerprint, the
  unrepaired check would run for a verdict again, and its next intermittent fail would refuse
  that landing, which is the blocking the ruling forbids.
- **The repair item is filed from the marker.** The gate has no access to the queue, so the drain
  files the item. It files from the marker file, not from any one run's verdict (`D-GR23`, which
  replaces "from the verdict" in `D-GR22`): the marker is written at the moment a run sees the
  contradiction, so it exists even when that run is cancelled, errors on another check or never
  writes a verdict. The item's id is `flaky-<check-id>`, and an open item with that id is not
  filed twice. Its task is to fix the flaky check and re-enable it correctly (`D-GR21`), and its
  text carries both times, both output locations and the fingerprint. The sweep that files it is
  specified under
  [One classifier, five outcomes](#one-classifier-five-outcomes).
- **A finished repair does not stand in for a new one.** The lookup that prevents a second
  item matches only an item that is not yet finished. Once a repair has landed and the check
  is enabled, a contradiction found later is a new defect: the drain files a new item carrying
  the new pair's times, output locations and fingerprint. Because the first id is taken, the
  second item for a check is `flaky-<check-id>-2`, the third `-3`, and so on. A lookup that
  matched any item with the id, finished or not, was rejected: the check would be disabled
  again with no repair item, which the ruling's "file an item to fix it" forbids.
- **The repair is proved on its own candidate, then the check is enabled.** The gate run for the
  repair item's candidate is told to run the check despite the marker (`--prove-disabled
  <check-id>`), and there the check is an ordinary one whose fail refuses that candidate. When
  the repair lands, `tillr-gate enable --check <check-id>` removes the marker and moves the two
  contradicting records out of the lookup directory into `resolved/`, where they stay readable
  and no longer count as a contradiction. Without that move a repair that left the check's
  inputs unchanged would be disabled again by the old pair on the next run.
- **No retry, count or clock.** One contradicted fingerprint disables the check; nothing re-runs
  it to decide, and no time ends the disabling. The filed request asked for the same three
  refusals, and they stand.
- **This is not the case quarantine.** [The gate check rules](../reference/gate-check-rules.md)
  describe a declared quarantine of one named failing case, which still runs. A disabled check
  is a whole check, is set by the store's own evidence, and does not run.

The build is not a change to `annotateMemory` and the summary loop alone. Seventy sites in four
programs decide what a check's result means, and each one that is not taught the rule
credits a contradicted check green, reports it as an error or blocks on it. They are listed under
[Every place a check result is classified](#every-place-a-check-result-is-classified); the one
function they call is under [One classifier, five outcomes](#one-classifier-five-outcomes); the
order to build it in is [its own unit of the build order](#classify-every-check-result-in-one-place).
The two tests that pin the `flaky` pass (`TestMemoryFailRetriesAndFlakyPass` and
`TestMemoryOppositeWritersNeverErase` in `tools/tillr-gate/memory_test.go`) change with slice 2d,
which turns on live `disabled` reporting in `tillr-gate run` and `tillr-gate checks` only after
the drain's marker sweep (slice 2b), consumer handling (slice 2c) and cached-evidence filtering
(slice 3a) have landed (`D-GR41`), so a live run never emits `disabled` before consumers accept
it and its repair item can be filed.

**Where the filed request and this section differ, this section governs the build.** The
operator request of 2026-11-20 says the check "is red in the verdict", and a build criterion
derived from those words asks for it to be counted as failed in the summary line. Both were
written before the owner's ruling of 2026-11-21 and are replaced by it (`D-GR21`): the build
counts the check as disabled, apart from passed and failed ("1 disabled, 0 failed"), and a
run whose only non-passing checks are disabled exits 0. This reconciliation is the designer's,
made on 2026-11-21 from the owner's quoted words and the operator's reading; the owner may
overturn it. What the request and the ruling share is kept: both times and both output
locations are named, and there is no retry, count threshold or time window.

#### Every place a check result is classified

A **classification site** *(coined here)* is a function that reads a check's raw result and
decides from it whether the check passed, failed, gave no verdict or does not count. A **raw
result** is what the site was handed: a process exit code, a result word another program wrote,
a stored record, or a verdict cached by an earlier run. A site is not the check itself: a check
script that exits 1 produces a raw result and classifies nothing.

The rule above was first built as one change that taught sites about `disabled` as each was
found. On 2026-11-21 that change stood at 111 files and 10,088 added lines (`git diff
--shortstat` of the kept candidate against its merge base), and its verifier had refused it
eleven times. Each refusal named another site that still credited a contradicted check green,
turned it into an error, or lost its repair item. The table is the inventory that build did not
have. It was read from the tree on 2026-11-21 by searching the four programs for every
comparison of a result word, a span status or a judging program's exit code, and then opening
each function found. `tools/fork-check.py` is listed with `tools/gate` because
`tools/gate/change-gate.py` runs it as the legacy part of the landing gate, and the drain runs
it for replay and for the rolling tier.

Each site has one kind:

- **fresh**: it classifies a result the check just produced by running.
- **cached**: it reuses a result stored earlier, from the result store, a proof record, a
  checkpoint or an attribution record, and the check does not run.
- **relay**: it translates another program's answer into its own words.
- **aggregate**: it computes one result for a run from the results of its checks.
- **settle**: it acts on a classified result (holds, releases, files, reverts, rewrites a
  verdict) and compares result words to do so.

**In `tools/tillr-gate`** (the runner):

| Site | Kind | Function, in file | Raw result it reads | What it decides today | Consumed by |
| :--- | :--- | :--- | :--- | :--- | :--- |
| G1 | fresh | `runCheck`, `run.go` | The check command's exit code and wait error; the watchdog's count of silent samples | `pass` on exit 0; `error` on exit 4, a signal or a wait error; `fail` on any other exit; `stalled` after 60 silent samples | `(*treeMemory).run`; `Shadow` |
| G2 | cached | `(*treeMemory).run`, with `(resultStore).read` and `(resultStore).publish`, `memory.go` | The pass and fail records for the fingerprint; the declaration's `ref:` needs; whether the worktree is the judged tree | A stored pass is returned as `pass` without running, also when a fail record exists; a missing `ref:` need is `unavailable`; an unreadable record is `error`; only `pass` and `fail` are stored | `runPlanChecks`, then G4 |
| G3 | cached | `annotateMemory`, `memory.go` | The result and both records | The memory word: `flaky`, `remembered`, `passed` or `failed`. `flaky` leaves the result `pass` | The verdict's `memory` field; G7 |
| G4 | aggregate | `RunWith`, with `runPlanChecks`, `run.go` | Per-check results; cancellation | The run's result and exit code: `error` (4) over `stalled` (6) over `fail` (1) over `pass` (0). `unavailable` and skipped checks do not count. A check not started when the run is cancelled is `error` | The verdict's `result`; the exit code read by L1, P1, P2 and D20 |
| G5 | relay | `gateResult`, `runGateProgram`, `RunGate`, `gate.go` | The gate program's exit code | One wrapper check: 0 `pass`, 1 `fail`, 6 `stalled`, anything else `error`. The program's own per-check results are read only after a stall | The verdict file D1 reads |
| G6 | relay | `readFastVerdict`, `watchdog.go` | The fast verdict file | Accepts only `stalled` checks with exactly 60 silent samples; any other content is an error | G5 |
| G7 | aggregate | `runFace`, `main.go` | Per-check `result` and `memory` | The summary counts: passed, remembered, unavailable, inherited, skipped. Every other word counts as failed, and `flaky` counts as passed | The `checks:` summary line |
| G8 | relay | `checksFace`, `checks.go` | Changed paths against each declaration's reads | `skipped: no changed path is read`, added after the run's result is fixed | The verdict on stdout, read by P1, P2 and D20 |
| G9 | settle | `ApplyInheritedFailures`, `run.go` | Per-check `fail` and a list of inherited failures | `fail` becomes `inherited` and the run becomes `pass-inherited` | Nothing outside tests: `rg -n 'ApplyInheritedFailures' tools/tillr-gate --glob '!*_test.go'` finds the definition only |
| G10 | aggregate | `Shadow`, `shadowResult`, `shadow.go` | G1's results | `error` over `fail` over `pass`; `stalled` counts as a pass | The `gate.shadow` record |
| G11 | relay | `pythonVerdict`, `verdictResult`, `compare`, `dualrun.go` | The Python gate's exit code; the Go verdict file | 0 pass, 1 fail, anything else error; a verdict with no result is `none` | The dual-run report |
| G12 | relay | `budgetNotes`, `run.go` | `stalled` and `over-budget` results | Note text only | Standard error |

**In `tools/gate` and `tools/fork-check.py`** (the legacy gate):

| Site | Kind | Function, in file | Raw result it reads | What it decides today | Consumed by |
| :--- | :--- | :--- | :--- | :--- | :--- |
| P1 | relay | `main`, `tools/gate/tree-check.py` | The runner's exit code and the one check's `result` word | Accepts exit 0 with `pass`, 1 with `fail` and 6 with `stalled`; any other pairing is exit 4. It hands the runner a re-serialized copy of the declaration, so the same check gets a different fingerprint than the landing gate gives it, and it keeps the logs in a temporary directory that is deleted on return | F1 |
| P2 | relay | `main`, `tools/gate/change-gate.py` | The runner's exit code for the fast tree checks; `fork-check.py`'s exit code for the rest | The runner's exit code is passed through and its verdict is copied unread; the legacy exit is kept when 0, 1 or 6 and is 4 otherwise | G5; D2 reads the copied verdict |
| P3 | fresh | `warm_trimmed_go_cache`, `change-gate.py` | The exit code of `go-build.py`, run outside the runner | Exit 0 or 1 counts as warmed | The warm stamp under the common Git directory |
| P4 | cached | `stored_result`, `store_result`, `tools/gate/checks/change_common.py` | Result store files, read and written directly, not through the runner | A record or none; writes `pass` or `fail` | P5, P6 |
| P5 | cached | `judge_document_set`, `change_common.py` | Per-document pass and fail records; the renderer's exit code and report | A pass with no fail record is remembered; a document with both runs again; exit 0 or 1 is the verdict and anything else is "could not judge" | P7 |
| P6 | cached | `judge` inside `main`, `tools/gate/checks/go-build.py` | Per-module pass and fail records; `go` exit codes | A pass with no fail record is remembered, as in P5. A module that runs is a pass on exit 0 and a fail on every other exit, exit 4 included, which differs from P5; the script exits 1 when any module failed, and 4 only when the script itself raised an error | G1; P3 |
| P7 | fresh | `run`, `change_common.py`, and the `main` of each script in `tools/gate/checks/` | Findings | Exit 0, 1 or 4. This is the raw result G1 reads, not a classification of one | G1 |
| F1 | fresh | `execute_attempt`, `tools/fork-check.py` | A check command's exit code; `tree-check.py`'s exit code | For a fast tree check, 6 is stalled and any exit other than 0, 1 or 6 is "refused"; a stale candidate is exit 2; a cancelled check is given exit 0 and marked not run | F3, F4 |
| F2 | cached | `cached_verdict`, `VerdictCache.lookup` | A verdict cache entry | An entry with `rc` 0 is `CACHED` and the check does not run; only passes are cached | F4 |
| F3 | fresh | `Check.conflict`, `Check.failed`, `Check.unanswered`, `Check.verdict`, `candidate_stale` | Exit code, check kind and output | A verdict against the tree, or not a verdict, by the kind's group | F4, F6, F9 |
| F4 | relay | `check_status` | F1 to F3 and F5 | One status word: `ok`, `FAIL`, `ERR`, `skip`, `STALE`, `STALLED`, `CACHED`, `QUARANTINED` or `not-run` | F6 to F8; the run record; the printed report |
| F5 | settle | `sentinel_skip`, `fork_quality_skip`, `applied_quarantine` | A red `fork-quality` or `waybar` check | A declared quarantine or a skip; the check is removed before the run's verdict | `run_gate` |
| F6 | aggregate | `verdict_of`, `run_gate` | F3 | `verified` (exit 0), `failed` (1), `unproven` (2) or `stalled` (6), in the order stalled, then failed, then unproven; `dirty` and `refused` are exit 2 | P2, D17, D21 |
| F7 | relay | `write_gate_spans` | F4; stall observations | The spans file, and a `stalled` verdict file; both are written once, when the run ends | D2, D5, D17, D19, D21 |
| F8 | relay | `evidence_records` | F4 | `ok`, `failed`, `aborted` or `skipped` | The evidence file |
| F9 | aggregate | `run_replay` | F3 on the replayed checks | Exit 0 green, 1 red, 2 could not judge, 77 dropped | D8, D12, D21 |
| F10 | fresh | `run_preflight`, `preflight_report` | Per-cell exit codes | `pass`, `declared`, `fail` or `unanswerable` | The manual preflight report |
| F11 | settle | `read_series_transcript`, `observe_fork_conflict` | Check output | Exit 0 forced when a series is declared elsewhere; exit 4 when a fork target cannot be confirmed | F3 |
| F12 | settle | `apply_tier`, `apply_reuse` | No result: the plan | A check is not planned, owed or reused; these produce the `skipped` and `reused` words with no result behind them | F7; the run record |
| F13 | fresh | `run_shard`, `main`, `tools/go-test-shards.py` | Go test events and the shard's exit code | Exit 0 or 1. Like P7, a raw result | F1, G1 |

**In `tools/tillr-loop`** (`tools/tillr-drain/drain/tillr-loop.py` is the same file, byte for
byte: `diff -q` on 2026-11-21):

| Site | Kind | Function, in file | Raw result it reads | What it decides today | Consumed by |
| :--- | :--- | :--- | :--- | :--- | :--- |
| L1 | relay | `execute`, `tillr-loop` | The exit code of `tillr-gate run`, `tillr-land pass` or `tillr-item verdict record` | For the gate: 0, 1 and 6 are kept and anything else is 4 | D1 |
| L2 | relay | `revision_check` | None of its own | It replaces itself with `python3 tools/fork-check.py` in the current directory. For a replay or a rolling run that directory is the replayed checkout, so that checkout's own copy of F1 to F9 classifies | D8, D12, D17 |
| L3 | settle | `compose_verdict` | The `--verdict` word | `pass` and `pass-inherited` record verification criteria as done; `fail` is refused | Item verdict records |
| L4 | settle | `compose_land` | Whether the verdict file exists | A missing file is exit 4 | `tillr-land pass` |

**In `tools/tillr-drain`** (files are under `drain/` unless a directory is given):

| Site | Kind | Function, in file | Raw result it reads | What it decides today | Consumed by |
| :--- | :--- | :--- | :--- | :--- | :--- |
| D1 | relay | `(*ProcessSteps) Gate`, `steps.go` | The gate program's exit code; the verdict's `result` | `GatePassed`, `GatePassedInherited`, `GateStalled`, `GateFailed` or `GateErrored`. It reads the verdict only after the program returns | D3 |
| D2 | relay | `gateFailureContext` (`steps.go`), `gateFailingChecks` (`returned.go`), `readFastGateSpans`, `readFailingSpans`, `readSpansWithWatches` (`gate_spans.go`) | Per-check `result` equal to `fail`; span status equal to `FAIL` | The failing-check list. Every other word is dropped without a record | `GateOutcome.FailingChecks`, read by D6, D10 and D13; `endingEvidence`; `designFailingChecks` |
| D3 | settle | `(*Loop) verify`, `loop.go` | D1 | Land, refuse, hold for a stall, or hold for no verdict | The lander; item holds |
| D4 | relay | `gateStallContext` (`gate_stall.go`), `foldPendingHistory` (`pass.go`) | `stalled` checks in a verdict | Stall observations | `holdGateStall` |
| D5 | relay | `readForkConflicts`, `fork_drift.go` | Span statuses `not-run`, `ERR`, `STALE` and `FAIL` | Fork conflicts, unfinished checks and unanswered forks | D1, D6 |
| D6 | settle | `(*Loop) settleGateFailure`, `markForkGateUnanswered`, `fork_drift.go` | D2; D11 on the base | Per check: regular, unknown, refused, waiting or inherited; writes `error` into the verdict | D3 |
| D7 | fresh | `(*Loop) judgeRequired`, `(*ProcessSteps) JudgeRequired`, `heavy_revert.go` | A required heavy check's result | `pass`; `fail` refuses the candidate; any other word is "not a verdict" | D3 |
| D8 | relay | `(*ProcessSteps) fixerCheck`, `(*Loop) settleFixerGate`, `pendingFixerVerdict`, `fixer_gate.go` | For each check a trunk fixer names: D7's result, or the fixer-gate program's exit code | `pass`, `fail`, `dropped` (exit 77) or `absent`; any other answer becomes `error` and holds the candidate; the verdict file is rewritten with the named checks | D3 |
| D9 | settle | `gateRefusalDetail` (`events.go`), `readRunHistory`, `(*planner) retryHoldReason` (`plan.go`), `(*Loop) landRemoteSync` (`remote_sync_landing.go`) | The verdict's run-level `result` | Refusal text; the hold reason after a failed gate; whether a remote sync may land | Events; planning; the lander |
| D10 | settle | `(*Loop) trunkAlreadyRed`, `redOnTip` (`trunk.go`); `(*Loop) routeObstacle` (`obstacle_route.go`) | D2's list, or the checks a worker's trunk obstacle names; D11 | The candidate's own failure, a red confirmed on the base or the tip, or a red already repaired at the tip | D13 |
| D11 | cached | `(*Loop) judgeTrunkChecks`, `trunk.go` | D31's proofs; otherwise D12 | A check that a proof record lists as passed is credited green with no replay | D6, D10 |
| D12 | relay | `(*ProcessSteps) Replay`, `steps.go` | The replay program's exit code for one check, run in a worktree of the replayed commit | 0 green, 1 red, anything else unknown. The default program is a relative path, so the copy that runs is the replayed checkout's | D11, D15, D29 |
| D13 | settle | `(*Loop) settleTrunkAlreadyRed`, `intersectingRedPaths`, `acquitRedWithPaths`, `trunk.go` | The red checks, their reads and the candidate's changed paths | `pass-inherited` when no red check reads what the candidate changed; otherwise the item is held behind a fixer | D3; item state |
| D14 | settle | `(*Loop) promoteVerdictPassInherited`, `trunk.go` | The verdict file | Rewrites each `fail` to `inherited` and the run to `pass-inherited` | The verdict file; the lander |
| D15 | relay | `(*Loop) culpritLanding`, `trunk.go` | D12 over the landings since the last green commit | The first red landing | Fixer filing |
| D16 | settle | `(*Loop) openFixerFor`, `routeTrunkFixer` (`trunk.go`); `fixerChecks` (`fixer_wait.go`); `fixerCheckOverlaps` (`fixer_gate.go`); `forkCheckDeclID` (`fork_check_triage.go`) | The check selectors in a fixer's `(red: …)` label | A match by exact id, or by kind and label | Attaching a red to an open fixer, or filing a new one |
| D17 | relay | `(*ProcessSteps) Rolling`, `steps.go` | The rolling program's exit code; spans with status `FAIL` or `STALLED` | Green, stalled, red with named checks, or unknown | D18 |
| D18 | settle | `(*Loop) maybeRolling`, `recordRollingRun`, `repeatedRollingStalls`, `rollingUnknownStreak`, `trunk.go` | D17 | `green`, `red`, `red-unfiled`, `stalled` or `unknown`; files a trunk fixer on red | The store; fixer filing |
| D19 | relay | `readRollingCoverage`, `readGateSpans` (`gate_spans.go`); `readFailingSpanMeta` (`fork_check_triage.go`) | Span status and the `cached` attribute | Coverage counts, span rows and which logs to keep | Store tables; kept logs |
| D20 | relay | `(*ProcessSteps) JudgeHeavy`, `decodeHeavyVerdict`, `heavy_steps.go` | The verdict of the installed `tillr-gate checks --heavy-tree` | Each check's result word as given | D7, D23 |
| D21 | relay | `(*ProcessSteps) runHeavyLegacy`, `heavy_steps.go` | Legacy span statuses; the program's exit code | `PASS`, `ok` and `CACHED` are `pass`; `skip` and `QUARANTINED` are `unavailable`; `FAIL` is `fail`; `STALLED` is `stalled`; anything else is `error`; an exit other than 0 or 1 adds an `error` check named `legacy-runner` | D20 |
| D22 | fresh | `(*ProcessSteps) judgeHeavyGo`, `heavy_go.go` | Go test events | `pass`, `fail`, or no verdict | D8, D20 |
| D23 | aggregate | `(HeavyRunner) Run`, `judgeTip`, `heavy.go` | D20's results | `fail` makes the run red; `pass` and `unavailable` change nothing; any other word makes it unknown; a second stall is `fail`. The green proof lists the passed checks. Repairs are filed after attribution and reverts finish | The green pointer; `green-<sha>.json`; `heavy.json`; fixer filing |
| D24 | cached | `loadHeavyProgress`, `reuseHeavyCheck`, `saveHeavyCheck`, `heavy_progress.go` | Checkpointed passes for a tip | A checkpointed pass is reused without judging | D20 |
| D25 | cached | `completedHeavyRed`, `heavy_attribution.go` | A finished red run in `heavy.json` | The red run is reused after a restart | D23 |
| D26 | cached | `(HeavyRunner) oneLogged`, `replay`, with `readHeavyReplay`, `heavy.go` and `heavy_progress.go` | One check judged at one commit; the replay cache | Red or green; any other word is an error; a cached answer is reused | D27, D28, D29 |
| D27 | cached | `(HeavyRunner) findFirstRed`, `searchFirstRed`, `attribute`; `(*heavyEarly) offer`, `recover` (`heavy.go`, `heavy_attribution.go`, `heavy_early.go`) | The attribution cache; D26 | The first red landing, reused when the failure, base and reads match | D29; fixer filing |
| D28 | settle | `(HeavyRunner) retainRedRepairs`, `forgetGreenAttributions`, `heavy_attribution.go` | D26 on a newer tip; passes | A repair is deferred or kept; a passed check's attribution is dropped | D23 |
| D29 | settle | `(HeavyRunner) revert`, `offerRevert`, `resolveRevert` (`heavy.go`); `(*Loop) revertLanding` (`heavy_revert.go`) | D1 and D12 on the inverse of a landing | Reverted, refused, or still red | The lander; the `drain.revert` event |
| D30 | settle | `(*Loop) startHeavy`, `heavyStatusLine` (`heavy_loop.go`); `ReadStatus` (`heavy/status.go`) | A run's checks with result `fail` or `unavailable` | The failing and unavailable lists | Status views; fixer filing |
| D31 | cached | `readGreenProof`, `readGreenProofFile`, `readCheckProofs`, `readProgressCheckProof`, `(checkProof) passed`, `fixer_resolve.go` | A green proof's passed list; checkpointed passes | A check is proved green at a commit, matched by id or by a `kind/` prefix | D11, D32, D33 |
| D32 | settle | `(*Loop) resolveProvedFixers`, `fixer_resolve.go` | D31 | A trunk fixer is finished as resolved elsewhere | Item state; the `drain.fixer-proved` event |
| D33 | settle | `obstacleProofPassed`, `(*Loop) releaseTrunkObstacleHolds` (`obstacle_hold.go`); `(*Loop) repairedTrunkWaiter`, `releaseTrunkAcquittals` (`trunk_release.go`) | D31; fixer proof events | An item held behind a red check is released | Item state |
| D34 | settle | `openTrunkRedNotice`, `trunk.go` | The labels of open trunk fixers | The "trunk check red" notice | New worker briefs |

The eleven refusals map onto the table as follows. A path that appears here was reached by a
real run in a verifier's scratch repository; the row says which sites it went through.

| What the verifier found | Sites |
| :--- | :--- |
| The landing wrapper's summary left the disabled check out | G5, G7 |
| A resumed proof credited a checkpointed pass, or reused a finished red | D24, D25 |
| A named or required heavy check that came back disabled held the candidate as an error | D7, D8 |
| The cold-cache warm-up ran the contradicted check | P3 |
| A cached replay or a cached first-red attribution used the contradicted base as green | D26, D27 |
| The repair item was lost when another check errored, a checkout could not be removed, or the run was cancelled | D1, D23, F7 |
| A disabled member of a grouped revert stopped the revert for an independent red | D29 |
| The fast tree adapter reported disabled as an error, under a different fingerprint, with logs already deleted | P1, F1, F4 |
| A replay or a rolling run of an older commit used that commit's classifiers | L2, D12, D17, D21 |
| Light attribution credited a proof record's pass | D11, D31 |
| A check disabled late in attribution still counted in the paths the candidate was held on | D13 |
| A pass recorded under the declaration's id no longer satisfied a fixer that names the labelled selector | D16, D31 |
| An item held only on the disabled check stayed held, and new briefs still called the check red | D33, D34 |
| The manual preflight had no disabled case | F10 |

#### One classifier, five outcomes

What follows is the designer's decision of 2026-11-21 (`D-GR23`). None of it is the owner's
ruling, and the owner may overturn any part of it.

**Every site in the table gets its answer from one function, and compares no result word
itself.** The function is `Classify`, in a new package `checkresult` of the shared module
`tools/tillr`, which the runner and the drain both import already. It is pure: it reads no file
and runs nothing. The Python sites, and any site that needs the result store, reach the same
function through one new face of the runner, `tillr-gate classify`, which reads the store and
then calls it. There is no second implementation in Python.

> [!WARNING]
> Do not give the Python programs their own copy of the rule, even one generated from a shared
> table. A replay and a rolling run execute the judged checkout's copy of `tools/fork-check.py`
> and `tools/gate/tree-check.py` (L2, D12), so a rule that lives in those files is the rule as
> it stood at the replayed commit. The installed `tillr-gate` is the only classifier that is
> the same for every commit a run judges.

An **observation** *(coined here)* is what a site hands the function about one check:

- the check, by the selector the site holds;
- the raw result and its source, one of: the exit code of the check's own command; the exit
  code of a judging program (the runner, the gate program, the legacy gate, the replay program);
  a result word or span status another program wrote; a cached record. A site whose rule today
  differs from these four names its own **protocol** instead, as
  [the protocols section](#each-site-keeps-its-raw-result-protocol-and-aggregate-order) lists;
- whether this run is **proving** the check: the gate run for the candidate of the check's own
  repair item, which `--prove-disabled` names;
- the store's facts for the check, when it is store-backed: whether a marker exists for its id,
  and whether a pass record and a fail record exist for its fingerprint on the judged tree.

A check is **store-backed** *(coined here)* when its results are kept in the result store: a
tree-scope declaration that is remembered, and the two checks that write the store themselves
(P5 and P6). A legacy check kind of `tools/fork-check.py`, a `go-test:` suite and a required
check with no declaration have no store facts. They can never be `disabled`, because nothing
records a pass and a fail for the same inputs of theirs; they are classified from the raw result
alone.

The function returns one **outcome** and a **detail**. The outcome is all that a site may use to
decide whether a check blocks or is credited. The detail is the finer word the tree uses today,
kept so that reports and the two existing kinds of no-verdict handling do not change.

| Outcome | It means | Blocks a landing or turns the trunk red | May be credited green | Detail words |
| :--- | :--- | :--- | :--- | :--- |
| `pass` | The check ran, or a stored pass stands, and nothing contradicts it | No | Yes | `passed`, `remembered`, `cached` |
| `fail` | A verdict against the judged tree | Yes | No | `failed` |
| `unknown` | No verdict: the check could not run, stalled, was cancelled or was not reached, or its record cannot be read | Yes, as "could not judge", by the existing stall and no-verdict rules | No | `stalled`, `error`, `not-run`, `stale` |
| `unavailable` | The check does not apply to this machine or this change: a missing resource ([Section 6.3](#63-a-missing-machine-resource-is-named-not-failed)), no changed path read, a declared quarantine skip, a check a fixer names that the commit does not have | No | No | `unavailable`, `skipped`, `quarantined`, `dropped`, `absent` |
| `disabled` | A marker exists for the check, or its fingerprint is contradicted | No | No | `disabled`, with the fingerprint, both times and both output locations |

`disabled` and `unavailable` answer the two questions the same way and differ in what follows:
a disabled check gets a repair item and an unavailable one gets nothing. `unknown` is the word
the request's "unknown" names; it is the union of what the tree calls `error`, `stalled`, `ERR`,
`STALE` and `not-run`.

The function applies these rules in order and stops at the first that answers:

1. **A proving run judges the check.** If the run is proving this check, rules 2 and 3 are
   skipped. A fail there is a `fail`.
2. **A marker disables.** If a marker exists for the check's id, the outcome is `disabled`,
   whatever the raw result is and on whatever tree.
3. **A contradiction disables.** If the store holds a pass record and a fail record for the
   check's fingerprint on the judged tree, the outcome is `disabled`. This includes the pair that
   the observation itself completes: a fresh fail against a stored pass, or a fresh pass against
   a stored fail.
4. **Otherwise the raw result decides**, by the protocol the observation names. This table holds
   the general protocols; the ones that differ are in
   [the protocols section](#each-site-keeps-its-raw-result-protocol-and-aggregate-order):

| Source | Raw result | Outcome and detail |
| :--- | :--- | :--- |
| The check's own command | Exit 0 | `pass` |
| The check's own command | Exit 4, a signal, or a wait error | `unknown`, `error` |
| The check's own command | Any other exit | `fail` |
| The check's own command | 60 silent samples | `unknown`, `stalled` |
| A judging program | Exit 0 | `pass` |
| A judging program | Exit 1 | `fail` |
| A judging program | Exit 6 | `unknown`, `stalled` |
| A judging program | Exit 77 | `unavailable`, `dropped` |
| A judging program | Any other exit, or it did not start | `unknown`, `error` |
| A word or status | `pass`, `PASS`, `ok` | `pass` |
| A word or status | `CACHED`; memory `remembered` | `pass`, `cached` or `remembered` |
| A word or status | `fail`, `FAIL` | `fail` |
| A word or status | `stalled`, `STALLED` | `unknown`, `stalled` |
| A word or status | `unavailable`; `skip`; `QUARANTINED`; `skipped: no changed path is read`; `dropped`; `absent` | `unavailable`, with that detail |
| A word or status | `disabled` | `disabled` |
| A word or status | `error`, `ERR`, `ERROR`, `STALE`, `not-run`, `over-budget`, an empty word, or a word not in this table | `unknown`, `error` |
| A cached record | A pass in a proof, a checkpoint, the verdict cache or the result store | `pass`, `cached` |
| A cached record | A red in the replay cache, the attribution cache or a finished red run | `fail` |
| A cached record | A record that cannot be read | `unknown`, `error` |

Every row is a decision some site in the inventory already makes, and the two exit-code sources
are kept apart because they differ today: a check command that exits 6 has failed (G1), and a
judging program that exits 6 has reported a stall (G5, L1). The table is not the whole of
today's behavior. This section first said it was, and a build of the first slice measured three
sites that decide differently (`D-GR28`, below).

**A cached result is a raw result, and it is classified when it is used, not when it was
stored.** A pass in a proof record, a checkpoint, the replay cache or the attribution cache goes
through the function with the store's facts as they are now. That is what makes the cached sites
(G2, P5, P6, D11, D24 to D27, D31) correct without a rule of their own: a record written before a
contradiction was known, or before this rule was built, is not credited once a marker or a pair
exists. A record that carries no fingerprint needs none: the face computes the fingerprint from
the check and the commit the record names.

**Two words are settlements, not results, and the function is not asked about them.** `inherited`
and `pass-inherited` are written by the drain after attribution (D14, G9): they record that a
`fail` was acquitted, and they stay what they are. The sites that read them (D1, D9, G7, L3)
read a decision, not a result.

**One aggregate function, with a named order.** `Aggregate`, in the same package, computes a
run's outcome from its checks' outcomes and details, in the order the aggregate site names. The
runner's order (G4) is `unknown` over `fail` over `pass`, where an `unknown` with detail `error`
wins over one with detail `stalled`. The other aggregate sites do not use that order today, and
each keeps its own
([the protocols section](#each-site-keeps-its-raw-result-protocol-and-aggregate-order)). In
every order, `unavailable` and `disabled` checks do not contribute, and a run with no other
checks passes. A run's summary counts the five outcomes apart.

**The face.** `tillr-gate classify` takes a list of observations as JSON on standard input, each
naming a check, a commit (or the worktree), the raw result and its source, and prints one answer
per observation in the same order. It runs no check. For a store-backed check it does three
things the pure function cannot:

- **It resolves the selector.** A site may hold the declaration's own id, or a labelled selector
  the legacy gate uses (`<id>:<label>` or the legacy span id `<id>/<slug(label)>` for a fast
  declaration). The face reads the declarations of the judged commit and resolves the selector to
  exactly one declaration id, by exact match on those forms and never by bare `<id>/` prefix. A
  selector that resolves to no declaration is not store-backed. A proof that lists a
  declaration's id satisfies a fixer that names the labelled selector only when the selector
  resolves to that id on the proof's commit.
- **It computes the one fingerprint.** When the caller does not already hold a per-unit key in
  the result store (as `documents` P5 and `go-build` P6 do), the fingerprint is taken from the
  declaration file's bytes as committed in the judged tree, as
  [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint) defines it. No site
  computes a fingerprint from a copy it wrote (P1).
- **It writes the marker.** When rule 3 answers and no marker exists, the face writes it before
  it prints the answer, and copies both output logs into the marker's directory, so the two
  output locations the marker names outlive the run that produced them. The runner does the
  same when one of its own checks completes a contradiction.

The answer for a `disabled` check carries the marker's content. An observation with no raw
result is legal: it asks only whether the check is disabled, and a site asks it before running
anything, so a disabled check is not run for a verdict.

**The repair item is filed by a sweep of the markers, at the start of every drain pass and after
every gate, replay, rolling or heavy step returns — whether it returned a result, an error or a
cancellation, and before attribution, pending reverts or dispatch.** The sweep is the only filer,
and no site files from a verdict. For each marker:

- no repair item names the marker's episode: file one, asking to fix the flaky check and
  re-enable it correctly (`D-GR21`);
- an unfinished repair item names it: nothing;
- a landed repair item names it: run `tillr-gate enable` for that episode again.

An **episode** *(coined here)* is one disabling of one check: the check's id with the
fingerprint and the two record times in its marker. `enable` names the episode and removes the
marker only when the marker still holds it, so a repair that lands late cannot clear a newer
contradiction. Two runs that see the same contradiction write the same marker; two that see
different contradictions of one check write one marker each in turn, the last write stands, and
either is a true episode for the one item. A sweep that fails to file leaves the marker, and the
next sweep files. A repair item filed this way needs no document block, and the drain's check
for one does not apply to it.

**What each kind of site does with `disabled`:**

- **fresh and relay sites** pass the outcome on with its evidence and never turn it into an
  error, a pass or a failure.
- **aggregate sites** leave the check out of the result and count it apart.
- **cached sites** do not credit it, and drop any attribution, replay answer or checkpoint that
  rests on it.
- **settle sites** treat the check as absent from every red list:
  - it is not in a candidate's failing checks, so it refuses nothing (D2, D3, D7, D8);
  - it is removed from a red reading before the reading is compared with what the candidate
    changed, and the comparison is made again if the check is disabled later in the same
    settlement (D10, D13);
  - no trunk fixer is filed for it, and it is taken out of an open fixer's label; a fixer left
    naming no check is finished as resolved elsewhere, naming the repair item, with no proof
    recorded and no pass claimed (D16, D18, D32). A run already working on that fixer is not
    stopped;
  - an item held only on disabled checks is released, without waiting for the repair to land
    (D33), and the red notice does not name the check (D34);
  - it neither authorizes a revert nor stops one: the other checks of a grouped revert are
    still judged, and a passing inverse of an enabled check may revert (D29).

**A commit that is being replayed supplies the declaration and the command, never the
classification.** For a store-backed check on any commit, the drain asks the face before it runs
anything and again with the raw result after, and uses the face's answer. Where the drain can
judge the declaration with the installed runner directly, as it already does for heavy
declarations (D20), it does that for fast declarations too and does not ask the replayed
checkout's programs about them. An older checkout whose rolling program still runs a
store-backed declaration itself is allowed to: the drain classifies that check's span through
the face and uses the face's answer in place of the span's. The rolling program's exit code then
counts only when the classified spans do not explain it: an exit that reports a failure when no
remaining check is `fail`, or one that reports no verdict when no remaining check is `unknown`,
makes the run `unknown` as it does today, unless every span that could explain it classified as
`disabled`.

**State that exists on the day this is built.** A store that already holds a pass and a fail for
one fingerprint with no marker gets its marker from the first observation that touches the
fingerprint. Proof records, checkpoints and attribution records written earlier are read as they
are and classified at use. A stored record with no output location is reported as "output
location not recorded", as above. Nothing is migrated.

**What an operator reads.** The runner's summary line counts disabled checks apart, on the
landing wrapper too (G5). The drain's trunk status lists disabled checks beside failing and
unavailable ones (D30), each with its repair item. `tillr-gate classify` on one check prints
why: the marker, or the two records.

**What this does not do:**

- It does not change which checks exist, their tiers or their fingerprints.
- It does not make a legacy check kind disableable. That would need those kinds to record a
  pass and a fail by fingerprint, which is the work of moving them to declarations.
- It does not change the quarantine of one named failing case, or the settlement words.
- It does not decide what a repair must contain. One consequence of `D-GR22` is left as it is
  and named here because the inventory exposes it: the documents check (P5) keeps one record per
  document, so one document that renders two ways disables the whole check until its repair
  lands.

**Alternatives, each rejected:**

- **Teach each site the rule as it is found.** This is the build that was refused eleven times.
  The rule has 70 sites, and the next site added to the tree would need to be taught again.
- **A classifier per program, with a shared conformance table.** Four copies agree on the day
  they are written. The replayed checkout runs the copy from its own commit, which is the
  failure the verifier found in rounds 9 and 10.
- **Keep filing from the verdict, and add a final filing step to every exit path.** The verifier
  found a new exit path in rounds 4, 6, 7, 9 and 10. A marker that the sweep reads has no exit
  path to miss.
- **Only four outcomes, with `unavailable` as a detail of `unknown`.** A site would then need
  the detail to know whether the check blocks, which is the comparison of words this design
  removes.

#### Each site keeps its raw-result protocol and aggregate order

What follows is the designer's decision of 2026-11-21 (`D-GR28`), made to answer a build
worker's report on the first slice. None of it is the owner's ruling, and the owner may overturn
any part of it. It amends `D-GR23` in one respect: the function is still the only place a raw
result is classified, but it holds more than one rule for raw results, each named.

The operator request that files the first slice says: "No outcome changes for any input:
existing tests pass unchanged." The classifier section above gave one raw-result table and one
aggregate order and called them today's behavior. The worker's build of that table, measured on
2026-11-21 against the trunk's own functions, changed five outcomes at three sites:

| Site | Input | The tree decides | The single table decided |
| :--- | :--- | :--- | :--- |
| P6 | One module's `go` command exits 4 | `fail`: `main` in `tools/gate/checks/go-build.py` stores a fail record for every non-zero module exit and exits 1 | `unknown`, `error` |
| F3 | A legacy check of an ordinary kind exits 4 | `FAIL`: `Check.conflict` in `tools/fork-check.py` reads any non-zero exit of a verdict kind as a verdict | `unknown`, `error` |
| F3 | The same check exits 124 | `ERR`: 124 is one of `COULD_NOT_ANSWER_CODES` | `fail` |
| F6 | One red check and one check with no answer | `failed`, exit 1 (`verdict_of`) | `unknown`, `error` |
| F6 | The same, and one stalled check | `stalled`, exit 6 | `unknown`, `error` |

The request governs: the first group of slices changes no outcome. So the function is told
which rule to apply.

A **protocol** *(coined here)* is one named rule, inside `Classify`, that turns one kind of raw
result into an outcome and a detail. An **aggregate order** *(coined here)* is one named
precedence, inside `Aggregate`, among the outcomes of a run's checks. An observation names its
protocol and an aggregate call names its order. The site chooses the name, which is a constant
for that site; it compares no exit code and no result word.

**The protocols.** The four sources of the classifier section are the general protocols and
keep the rows of its table. Four sites have their own:

| Protocol | Sites | The site hands over | Rule, in order |
| :--- | :--- | :--- | :--- |
| the check's own command | G1, G5 | The exit code, the wait error, the silent-sample count | The first four rows of the classifier section's table |
| a judging program | G5, G11, L1, P1, P2, D1, D8, D12, D17, D21 | The program's exit code | The next five rows |
| a word or status; a cached record | The relay, cached and settle sites | The word, or the record | The remaining rows |
| a budget note | G12 | The check's `result` word | `over-budget` is `unknown`, `over-budget`; every other word uses the word-or-status rows |
| a Go build of one module | P6 | The `go` command's exit code for the module | Exit 0 is `pass`. Every other exit is `fail` |
| a document render | P5 | The renderer's exit code; whether a report was read; whether it holds a finding | Exit 0 with a report is `pass`. Exit 1 with at least one finding is `fail`. Anything else is `unknown`, `error` |
| a legacy check | F1, F3, F4 | The exit code and the facts listed below | The eight steps below |

The legacy-check protocol is `check_status` and `Check.conflict` of `tools/fork-check.py`, read
on 2026-11-21, moved into the function. It stops at the first step that answers:

1. The run stopped before the check finished or started: `unknown`, `not-run`.
2. The result came from the verdict cache: `pass`, `cached`.
3. A declared quarantine applies: `unavailable`, `quarantined`.
4. The check's own result names a case this machine cannot answer (the sentinel skip):
   `unavailable`, `skipped`.
5. The candidate is stale: `unknown`, `stale`.
6. The gate stopped the check as stalled: `unknown`, `stalled`.
7. Exit 0: `pass`.
8. Any other exit is `fail` when it is a verdict and `unknown`, `error` when it is not. It is
   not a verdict when any of these holds, and otherwise the kind's group decides:
   - the exit code is 124, 125, 126, 127 or 137, or the process was ended by a signal the gate
     did not send;
   - the exit code is 4 and the kind is one that reserves 4 for "could not answer"
     (`patch-branch-inv2`, `patch-branch-inv3`, `forks-check`, `build`, and `fork-quality`
     without `--prepared`);
   - the check is a fast tree check, or a kind of the `unable` group, that refused before
     running its command;
   - the check is the fork driver's candidate preparation and its output holds the driver's
     own refusal to run from a linked worktree;
   - the kind's group is `named` and the output names no failing case;
   - the kind's group is `invariant` and the output names none of the kind's invariants.

   A kind of the `verdict` group, a kind of the `unable` group that ran, and a kind the group
   table does not list are verdicts on any exit this list does not exclude. That is why exit 4
   from an ordinary kind is a `fail` here and an `unknown` from the runner's own command.

The Python side still reads the check's output, because only it has the output and the kind
table. It hands over **facts** and decides nothing from them: the kind's group, whether the
kind reserves exit 4, whether the output names a failing case, an invariant or the stale
marker, whether the check refused before running, and whether a quarantine, a sentinel skip or
a cache entry applies. Each fact is a yes or no about the output or the plan. Pairing a fact
with an exit code to reach a result word is classification and is done only in the function.
The kind table and the phrase lists are declarations, like a check's declaration file, so a
replayed commit supplies its own, as it supplies its own command.

**The aggregate orders.** Each is listed highest first; the first that any check matches is the
run's result.

| Order | Site | Precedence | Run result and exit code |
| :--- | :--- | :--- | :--- |
| the runner | G4 | `unknown` with detail `error`; `unknown` with detail `stalled`; `fail`; `pass` | `error` (4), `stalled` (6), `fail` (1), `pass` (0) |
| the shadow run | G10 | `unknown` with detail `error`; `fail`; `pass`. A stalled check and a check that did not run do not count | `error`, `fail`, `pass` |
| the legacy gate | F6 | `unknown` with detail `stalled`; `fail`; any other `unknown` except `not-run`; `pass`. A check that did not run does not count | `stalled` (6), `failed` (1), `unproven` (2), `verified` (0) |
| the legacy replay | F9 | Any `unknown`, a sentinel skip or a cancelled check; `fail`; `pass` | 2, 1, 0 |

The legacy gate's order puts a red above a missing answer, and the runner's puts a missing
answer above a red. Both stand. Which of the two is right is a question this decision does not
take up, because answering it changes an outcome.

**A difference found later is handled the same way, by the builder.** The inventory has 70
sites and this section measured the ones the worker reported. When a slice of the first group
finds a site whose decision for some input matches no protocol or order listed here, the tree
at the slice's base is the specification for that input. The builder adds a protocol, an order
or a row that reproduces it, with a table test, and adds the row to this section in the same
commit. That is not a definition obstacle and needs no design run. What the builder may not do
in the first group is change an outcome to fit the table, or leave the decision in the site.

**What this changes in the slices.** Slice 1a builds the protocols and orders listed here with
the two functions, and its table tests have one case per row and step of this section too.
Slices 1b to 1e pass each site's protocol or order by name (the two legacy aggregate sites
pass theirs in slice 1g, by `D-GR33`). Slices 2a to 3b keep the same protocols and orders:
rules 1 to 3 run before any protocol, a legacy check kind has no store facts and so is never
`disabled`, and `disabled` does not count in any order.

**Alternatives, each rejected by the designer:**

- **Keep the single table and allow the five outcome changes by name.** The request's words
  forbid any outcome change in this slice, and the second and fifth rows would turn a red
  landing gate into a "could not judge".
- **Have the Python sites turn their exit codes into `pass` and `fail` words and send the
  words.** The disputed decisions (exit 4, exit 124) would then be made in
  `tools/fork-check.py` and `go-build.py`, outside the function, in the copy a replayed
  checkout runs.
- **Merge the orders into the runner's now.** It is the simpler end state, and it is an outcome
  change for the legacy gate. It can be decided on its own after the first group lands.

#### The first classifier landing is the function and its face, with no caller

What follows is the designer's decision of 2026-11-21 (`D-GR32`), made from an operator's answer
to a build worker's report. None of it is the owner's ruling except the one quoted sentence, and
the owner may overturn any part of it.

The operator request that files the first slice asks for all of the first group at once. Its
words name the sites ("the landing gate, light and heavy attribution, base replay, named fixer
replay, the fast tree adapter, proof reuse and the legacy rolling adapters"), and add two cases:
"classification always runs from current code, never from classifiers in a replayed checkout",
and "a test per site shows it calls the shared function". A candidate that held only the
function and its face was refused on those two cases. A candidate that also routed the callers
could not be verified, for the reason the operator gave on 2026-11-21 (8:57 PM Eastern),
verbatim:

> "the installed tillr-gate cannot have the classify face until a change containing it lands and is proved green, so split here. Offer as this item's landing only the classify face in tillr-gate with its tests, and nothing that calls the installed face. Owner ruling 2026-11-21: part of a change may land while a named case is unmet. Name the adapters that must call the installed face as the unmet case in the candidate note; they are filed as the next item, which runs after the green build installs the face."

The Python sites and a replayed checkout reach the function only through the installed
`tillr-gate classify`, and the installed program is built from the green pointer (`D-GR5`). So
the face has to be on a proved trunk commit before anything can call it.

The owner's ruling the operator cites is [`OQ-SP1`](./tillr-item-split.md#OQ-SP1) of the split
design. Asked on 2026-11-21 whether part of a change may land while a named case of the request
is still unmet, the owner said, verbatim, with his spelling kept:

> "part of desgins can absoutely land. this is generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the smallest functional change possible to make reviewing, testing, velocity, all this stuff easier, and it will also make it easier for us."

Applying that ruling to this request is the operator's and the designer's reading, not the
owner's words about this request.

**What the request's landing is.** It is slice 1a of
[the build order](#classify-every-check-result-in-one-place), and nothing more:

- the `checkresult` package of the shared `tools/tillr` module, with `Classify` and `Aggregate`
  holding rule 4's table and
  [every protocol and aggregate order](#each-site-keeps-its-raw-result-protocol-and-aggregate-order);
- the `tillr-gate classify` face, which takes observations and returns outcomes in the same
  order and starts no check command;
- the table tests of the package and the face test;
- the pack mirror from `just pack-sync`.

No file outside those calls the package or the face, no existing test is edited, and no existing
function changes. The request's sentence "No outcome changes for any input" holds because
nothing that decides an outcome today is touched.

**The cases this landing leaves unmet, by name.** Each follows as its own later change, and the
candidate's commit message lists them:

| Unmet case of the request | Where it is built |
| :--- | :--- |
| Every site calls the shared function, and a test per site shows it | Slices 1b to 1e, with the scan test |
| The adapters call the installed face: the fast tree adapter, the change gate, the Go build and documents checks, the legacy check program with its gate and replay aggregates, and the two copies of `tillr-loop` | Slice 1e, after a proved trunk commit holds the face |
| Classification on base replay and named fixer replay runs from current code, never from the replayed checkout's classifiers | Slice 1e for the adapters named above; slice 3b for a replayed commit whose own programs are older than slice 1e |

The operator's answer says these are filed as the next item. This document files nothing.

**What a verifier of this landing judges.** It judges the four bullets above: the tests named
there pass, the tests that existed before pass unedited, and a search of `tools/` for the
package's import path finds only the package, the face and their tests. A missing caller, a
missing per-site test, and a replay that still runs the replayed checkout's
`tools/fork-check.py` are the unmet cases of the table. They are not grounds to refuse this
landing, and they are not definition obstacles for its builder. The landing is refused for what
[the split design](./tillr-item-split.md#OQ-SP1) lists: making the trunk worse, an unasked
change, a missing test of its own share, or not delivering its share.

**What this changes in the slices.** The scan test leaves slice 1a. With every file exempt it
asserts nothing, and it reads the callers this landing does not touch. The first of slices 1b
to 1e to land adds it, with the files not yet moved on its list.

**Alternatives, each rejected by the designer:**

- **Land the function, the face and the callers in one change.** The callers of the face cannot
  be tested against an installed program that lacks it. The earlier candidates that tried this
  were 88 and 93 files, and each ended blocked.
- **Have the Python sites call a `tillr-gate` built from the candidate's own tree.** Then a
  replayed checkout would classify with its own code, which `D-GR23` forbids.
- **Hold the face until its first caller is ready.** It keeps the first landing large, against
  the owner's quoted words on the smallest functional change.

#### The face aggregates a run in its own landing, before the legacy aggregates call it

What follows is the designer's decision of 2026-11-21 (`D-GR33`), made to answer a build
worker's report against an operator request. None of it is the owner's ruling, and the owner may
overturn any part of it. It amends `D-GR28` and `D-GR32` in one respect: slice 1e no longer
moves the two legacy aggregate sites, which wait for a face that can aggregate.

The operator request that follows the first landing says, in part: "once the installed
tillr-gate has classify, change the gate adapters the classification-sites design names so they
call that installed face." `D-GR28` puts each aggregate site's precedence in `Aggregate` and
has the site pass the order's name, and the Python sites reach the package only through the
installed face (`D-GR32`). The face that landed in `020cf29eca` classifies each observation and
aggregates nothing. Measured on 2026-11-21 against the installed program:

| Command | Result |
| :--- | :--- |
| `echo '[]' \| tillr-gate classify` | Prints `[]`, exit 0: the face is installed |
| `echo '[]' \| tillr-gate classify --aggregate legacy-gate` | `flag provided but not defined: -aggregate`, exit 2 |
| `rg -n 'Aggregate' tools/tillr-gate/classify.go` | No line: the face never calls `Aggregate` |

So `verdict_of` (F6) and `run_replay` (F9) of `tools/fork-check.py` have no installed program
to hand their order's name to. A worker that moved them would have to keep the precedence in
Python, which `D-GR28` forbids, or call a `tillr-gate` built from the judged checkout, which
`D-GR32` rejected. The worker's report is right about the tree, and the fix is the one `D-GR32`
already used for the face itself: the missing part of the face lands first, alone, and is proved
green before anything calls it.

**The face's aggregate form.** `tillr-gate classify --aggregate <order>` reads the same JSON
array of observations on standard input as the plain form. It classifies each one and then
calls `Aggregate` with the named order on those classifications. It prints one JSON object with
two fields:

- `checks`: the array the plain form prints for the same input, in the same order;
- `run`: the run's result, with the fields `outcome`, `detail` and `exit_code`, as the
  [aggregate orders table](#each-site-keeps-its-raw-result-protocol-and-aggregate-order) lists
  them for that order.

The order is one of the four names the package holds: `runner`, `shadow`, `legacy-gate` and
`legacy-replay`. Any other name is a usage error: exit 2, a message on standard error that
lists the four names, and nothing on standard output. An empty array is legal and gives the
order's result for a run with no checks. Without the flag the face's output is unchanged, byte
for byte. The face's own exit code reports whether it answered (0) and never carries the run's
result; a caller reads `run.exit_code`. The flag name, the field names and the exit codes here
are the designer's choice.

The aggregate form takes observations and not classifications. A site that needs each check's
answer while the run is going (the legacy gate prints a status as each check finishes) asks the
plain form then, keeps the observations it sent, and sends all of them again with the flag when
the run ends. Without store-backed flags the face is pure, so the two answers for one observation
agree; when store-backed flags are passed (from slice 2a on), the answer of the aggregate call is
the one the run's result uses.

**The slices.** Slice 1e is narrowed, and two slices follow it:

| Slice | What it holds | Needs |
| :--- | :--- | :--- |
| 1e | The Python sites that classify one check call the installed face: the fast tree adapter, the change gate, the Go build and documents checks, `check_status` and the functions it reads in the legacy check program, and the two copies of `tillr-loop`. `verdict_of` and `run_replay` keep the code they have and change no outcome | The installed face of slice 1a, which is installed today |
| 1f | The face gains `--aggregate`, with its tests and the pack mirror. Nothing calls it | Slice 1a |
| 1g | `verdict_of` calls the face with `legacy-gate` and `run_replay` with `legacy-replay`, and neither compares an exit code or a result word afterward | A proved trunk commit that holds slice 1f, so that the installed `tillr-gate` has the flag; slice 1e |

Slice 1f touches only `tools/tillr-gate`, so it may land before, after or beside slice 1e.

**What the request's landing is.** It is slice 1e as narrowed here. The request's own
precondition ("confirm `tillr-gate classify` exists on PATH") holds, by the first row of the
measurement above. Under the owner's ruling [`OQ-SP1`](./tillr-item-split.md#OQ-SP1), quoted in
[the section above](#the-first-classifier-landing-is-the-function-and-its-face-with-no-caller),
the builder may land a smaller part of slice 1e that works alone (one adapter with its test,
for instance) and name the rest as unmet. Applying that ruling here is the designer's reading,
not the owner's words about this request.

**The cases this landing leaves unmet, by name.** The candidate's commit message lists them:

| Unmet case of the request | Where it is built |
| :--- | :--- |
| The legacy gate's aggregate (F6) and the legacy replay's aggregate (F9) take their run result from the installed face | Slice 1f, then slice 1g after 1f is proved green |
| A replay's run result is decided by current code. Until slice 1g, `run_replay` in the replayed checkout still orders the classified checks itself | Slice 1g; slice 3b for a replayed commit older than slice 1g |
| Any adapter of slice 1e that the builder leaves for a later part | Named in the candidate's commit message |

This document files nothing. Slices 1f and 1g become requests or items the way the other
slices do.

**What a verifier of this landing judges.** Each adapter the candidate moves sends its
observation to the `tillr-gate` on PATH and uses the answer, with a test of that call; the
tests that existed before pass unedited (their assertions, by
[`D-GR34`](#a-stand-in-for-the-installed-program-is-fixture-setup-and-it-answers-classify);
a fixture's setup may change in two named ways); and no outcome changes. `verdict_of` and `run_replay`
still holding their precedence is the first unmet case of the table. It is not grounds to
refuse the landing and it is not a definition obstacle for its builder. A candidate that edits
either function's precedence, or that reaches `checkresult` from Python by any route other
than the installed face, is refused.

**What this changes elsewhere in the build order.** The scan test's list keeps
`tools/fork-check.py` after slice 1e and is empty after slice 1g. Slice 3b needs slice 1g.
Slices 2a to 2d and 3a are unchanged as to aggregate orders: a legacy check kind has no store
facts, and `disabled` counts in no order.

**Alternatives, each rejected by the designer:**

- **Hold every adapter until the face can aggregate.** The per-check sites need nothing the
  installed face lacks, and holding them makes one large landing out of two small ones.
- **Give the request both the flag and its callers.** The callers cannot be tested against an
  installed program without the flag. This is the first alternative `D-GR32` rejected.
- **Have `verdict_of` and `run_replay` order the face's per-check answers themselves.** Their
  precedence would then be compared in Python, in the copy a replayed checkout runs, which is
  what `D-GR28` put in `Aggregate` to end.
- **An aggregate form that takes classifications.** A site could then send words it reached on
  its own. `D-GR28` rejected sending words from Python for the same reason.
- **A second face, `tillr-gate aggregate`.** One more program surface for one more function
  call; the flag keeps the observations and the run result in one answer.

#### A stand-in for the installed program is fixture setup, and it answers classify

What follows is the designer's decision of 2026-11-21 (`D-GR34`), made to answer a second build
worker's report against the same operator request. None of it is the owner's ruling, and the
owner may overturn any part of it. It amends one phrase of `D-GR33` and of slices 1e and 1g:
"the tests that existed before pass unedited" and "no test edited".

The worker's report is right about the tree. Read on 2026-11-21 at `6aad1b6834`, the test
`test_just_done_and_rolling_refuse_a_red_tree_and_replay_names_it` of
`tests/fast-tree-in-done.test.py`, which `tests/fork-check.test.py` runs, builds its own tree
and does two things a moved adapter cannot live with:

| What the fixture does | Why the moved adapter fails there |
| :--- | :--- |
| Copies exactly two programs into its tree, `tools/fork-check.py` and `tools/gate/tree-check.py` | A file the adapter newly reads to make the call is absent from the fixture |
| Writes its own `tillr-gate` first on PATH, whose first statement is `assert args[1:3]==['checks','--fast-tree']` | `tillr-gate classify` reaches that program, which exits 1 on the assertion and prints nothing. The adapter then has no classification and must answer exit 4, where the test asserts exit 1 |

The worker reproduced the second row without running the gate. So the fixture cannot pass
unedited once the fast tree adapter (P1) calls the installed face, and editing it was outside
what slice 1e allowed.

A **stand-in** *(coined here)* is a program a test writes under the name `tillr-gate` and puts
ahead of the installed one on PATH, so that the test controls what the runner reports.

**The rule.** A test has two parts, and slice 1e's rule binds only one of them:

- **Assertions** are every `assert` statement and every `self.assert…` call, with the commands
  they judge, the exit codes and the text they expect. Slices 1e and 1g edit none of them.
- **Fixture setup** is what a test builds before it asserts: the files it copies into its tree
  and the stand-in it writes. Slices 1e and 1g may edit it, in the two ways below and no other.

The two permitted setup edits:

1. **The fixture copies what the adapter now reads.** When a moved adapter reads a second file
   to make its call to the face, a fixture that copies the adapter copies that file too.
2. **The stand-in hands `classify` to the installed program.** When its first argument is
   `classify`, the stand-in runs the next `tillr-gate` on PATH after its own directory, with
   the same arguments, standard input, environment and working directory, and exits with that
   program's exit code and output. Every other invocation keeps the code it has, including the
   stand-in's own assertions about `checks --fast-tree`. With no installed program to hand to,
   the stand-in exits nonzero and prints nothing, so the adapter answers exit 4 and the test is
   red: the failure names a missing installed face, which is true.

The stand-in never answers `classify` itself. A table of exit codes and words written into a
test's stand-in is a second copy of the rule, which `D-GR23` forbids, and the test would then
pass on a classifier that is not the one a landing uses.

**The file a moved adapter may share.** The Python sites may hold the call to the face in one
file under `tools/gate` instead of repeating it. That file builds the observation, starts
`tillr-gate classify`, and returns the answer or reports that there was none. It compares no
exit code and no result word, and the scan test of slice 1e reads it like any other file. The
builder chooses whether to have one and what to name it. Because a replay runs the judged
checkout's copies of the adapters, the file is read from the same checkout as its caller.

**What a verifier of the request's landing judges, restated.** `D-GR33`'s paragraph stands with
one change: where it says the tests that existed before pass unedited, read that their
assertions are unedited and their setup is edited only in the two ways above. A candidate that
changes an expected exit code or expected text, removes an assertion, has a stand-in classify
by itself, or gives an adapter a way to choose a classifier other than the `tillr-gate` on PATH
is refused.

**What this changes elsewhere in the build order.** Nothing but the two rows' wording. Slices
1b to 1d keep "no test edited" as written: no report has shown a fixture there that stands in
for the installed program, and the same rule applies to one when a report does.

**Alternatives, each rejected by the designer:**

- **Hold the fast tree adapter out of slice 1e until some other slice may edit the fixture.**
  The edit is the same edit whoever makes it, and the adapter with its own fixture is one
  functional change.
- **Have the stand-in print a classification itself.** The reason is in the rule above.
- **Give the adapter an option or an environment variable that names the classifier, and have
  the test set it.** A replayed checkout could then be pointed at a classifier other than the
  installed one, which is what `D-GR32` rejected.
- **Have the adapter keep its own pairing of exit codes and words when the face does not
  answer.** That keeps the copy of the rule slice 1e removes, and a missing face would go
  unseen.
- **Rewrite the fixture around a real runner with no stand-in.** A larger change to the test
  than the request needs; the stand-in exists to control the runner's report.

#### A fixture that copies a moved adapter is in the mover's change, in any language

What follows is the designer's decision of 2026-11-22 (`D-GR36`), made to answer a third build
worker's report against the same operator request. None of it is the owner's ruling, and the
owner may overturn any part of it. It changes no rule of `D-GR34`. It states which files that
rule reaches, because the worker's plan allowed writes only under `tools/gate/` and to
`tests/fast-tree-in-done.test.py`, and one fixture the rule covers is outside both.

The worker's report is right about the tree. Read on 2026-11-22 at `f260349cb8`, the Go test
`TestFixerGateJudgesOwnDiffThenNamedChecksAndLands` in
`tools/tillr-drain/drain/fixer_gate_test.go` builds a tree with this line and then runs the
real change gate in it:

```text
for _, name := range []string{"tools/fork-check.py", "tools/gate/change-gate.py"} {
```

The worker's candidate has `tools/gate/change-gate.py` read a shared file, `tools/gate/classify.py`,
to make its call. The worker reported that the subtest `no name` then fails with
`landed=false want true`, that the copied adapter alone fails with `ModuleNotFoundError: No
module named 'classify'`, and that copying the shared file beside it makes the same command
pass. That is the first permitted setup edit of `D-GR34`, in a Go test instead of a Python one.

**What the rule reaches.** The two permitted setup edits of `D-GR34` apply to every test in the
repository whose fixture copies a moved adapter or writes a stand-in, whatever language the
test is written in and whatever directory it is in. For the adapters' landing (slice 1e) and
for slice 1g, the files the builder may write are therefore:

- the adapters the slice moves and the shared file, under `tools/gate/` and `tools/fork-check.py`
  as the slice's row says;
- the tests of the calls, beside the adapters;
- each test file whose fixture needs one of the two setup edits, for those edits only. This
  includes Go test files under `tools/tillr-drain`, `tools/tillr-gate` and `tools/tillr-loop`;
- the pack mirror of any file above that has one, regenerated by `just pack-sync` and never
  edited by hand.

A plan for the slice that lists write paths lists these. A fixture found after the plan was
written is added to the plan's paths by the same rule, without a further decision: the test of
whether a file belongs is whether the edit to it is one of the two setup edits.

**How the builder finds the fixtures.** Measured on 2026-11-22 at `f260349cb8`, this command
lists 30 test files that name an adapter or `tools/fork-check.py`:

```bash
rg -l 'change-gate\.py|tree-check\.py|go-build\.py|change_common|fork-check\.py' \
  --glob '*_test.go' --glob '*.test.py' --glob '!yolo-packs/**' .
```

Most of them run the adapter from the repository's own tree and need no edit. The builder runs
the command, reads each file for a copy of a moved adapter into a tree the test builds, and
runs the test that copies. A list of the copying fixtures is not written here, because it would
go stale with the next test added; the command and the rule are what last.

**The Go fixture's `tillr-gate` is not a stand-in.** `TestFixerGateJudgesOwnDiffThenNamedChecksAndLands`
builds `tools/tillr-gate` from the repository's source and puts that binary first on PATH. It
is the real program, built from the tree under test, so it answers `classify` itself and the
second setup edit does not apply. The adapter still calls whatever `tillr-gate` is on PATH and
has no option that names one, which is all `D-GR32` and `D-GR34` require of it.

**What a verifier of the request's landing judges, restated.** `D-GR34`'s paragraph stands. A
change to a test file outside `tools/gate/` and `tests/` is not grounds to refuse the landing
when it is one of the two setup edits. A change there that edits an assertion is refused, as
anywhere.

**Alternatives, each rejected by the designer:**

- **Keep the adapter's call inside `tools/gate/change-gate.py`, so that no fixture needs a
  second file.** It repeats the call in every adapter, which `D-GR34` let the builder avoid,
  and the next fixture that copies a different adapter raises the same report.
- **Leave `tools/gate/change-gate.py` for a later part.** `D-GR33` lets the builder name a left
  adapter in the commit message, and the builder may still do so for another reason. A fixture
  that needs one copied file is not that reason: the edit is the same whoever makes it.
- **Have the Go fixture copy all of `tools/gate/`.** It copies declarations the test does not
  mean to run, and changes what the fixture's gate selects.
- **List every copying fixture in this document.** The reason is given above. Overturned by
  `D-GR37`, which lists them as a dated measurement after a second plan left one out.

#### A fixture that copies the legacy gate is reached too, and the plan lists the fixtures measured

What follows is the designer's decision of 2026-11-22 (`D-GR37`), made to answer a fourth build
worker's report against the same operator request. None of it is the owner's ruling, and the
owner may overturn any part of it. It changes no rule of `D-GR34` or `D-GR36`. It overturns one
alternative `D-GR36` rejected: this document now holds a dated list of the fixtures that copy.

The worker's report is right about the tree. The worker confirmed that `tillr-gate classify` is
on PATH and answers `[]` for `[]`. Its plan allowed writes to
`tools/tillr-drain/drain/fixer_gate_test.go` and not to
`tools/tillr-drain/drain/heavy_legacy_restart_test.go`. Read on 2026-11-22 at `417d7d8df4`, the
helper `testHeavyLegacyRestart` in that second file builds a tree with these lines, appends a
`main` of its own that calls `check_status(c)` for each check, and writes the result as the
tree's `tools/fork-check.py`:

```text
writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", ".keep"), "")
source, err := os.ReadFile(filepath.Join("..", "..", "fork-check.py"))
```

The tree it builds holds nothing else under `tools/gate/`. The worker's candidate has
`check_status` read the shared file `tools/gate/classify.py`, and the worker reported that the
same file layout in a scratch tree fails with `ModuleNotFoundError: No module named
'gate.classify'`, and that copying the shared file into it resolves the import. Loading the
shared file only when `check_status` is first called does not help, because the fixture calls
`check_status`.

**The legacy gate is a moved file under the fixture rule.** Slice 1e moves `check_status` in
`tools/fork-check.py`, so a fixture that copies `tools/fork-check.py` into a tree it builds, and
then runs a moved function there, is a fixture that copies a moved adapter. The first permitted
setup edit of `D-GR34` applies: the fixture copies the shared file too. `D-GR36` already listed
`tools/fork-check.py` among the files the slice moves and put it in the search command; this
states the consequence so that a plan does not read "adapter" as the files under `tools/gate/`
alone.

**The edit to this fixture.** `testHeavyLegacyRestart` copies the one shared file to the same
path under the tree it builds, beside the `.keep` file it already writes. It copies nothing
else from `tools/gate/`, and no assertion in the file changes. The fixture writes no stand-in
for `tillr-gate`, so the second setup edit does not apply, and the `tillr-gate` on PATH answers.

**The fixtures that copy, as measured.** Read on 2026-11-22 at `417d7d8df4`, the command of
`D-GR36` lists 30 files, and four of them copy a moved file into a tree the test builds:

| Test file | What its fixture copies | Setup edit, if the copied file reads the shared file |
| :--- | :--- | :--- |
| `tests/fast-tree-in-done.test.py` | `tools/fork-check.py` and `tools/gate/tree-check.py`, and writes a stand-in for `tillr-gate` | Both (`D-GR34`) |
| `tests/fork-check.test.py` | The text of `tools/fork-check.py`, written to the tree's `tools/fork-check.py` | The first |
| `tools/tillr-drain/drain/fixer_gate_test.go` | `tools/fork-check.py` and `tools/gate/change-gate.py` | The first (`D-GR36`) |
| `tools/tillr-drain/drain/heavy_legacy_restart_test.go` | `tools/fork-check.py`, with its own `main` appended | The first (this decision) |

Two more files read `tools/fork-check.py` without copying it: `heavy_legacy_test.go` and
`heavy_ownership_test.go`, both in `tools/tillr-drain/drain/`, pass the repository's own path
to a driver that takes named functions out of the file. Neither takes `check_status`, so
neither needs an edit while that holds. The other 24 files run the adapters from the
repository's own tree or only name them.

**What a plan for the slice does with the list.** A plan that lists write paths for slice 1e or
1g lists all four files in the table and the pack mirror of each that has one, whether or not
the planner expects each to need an edit. An unused permission costs nothing; a missing one
costs a run, and has now cost two. The list is a measurement and not the rule. The rule of
`D-GR36` still decides: the builder runs the command again, and a fixture that is not in the
table is added to the plan's paths without a further decision.

**What a verifier of the request's landing judges, restated.** `D-GR36`'s paragraph stands. A
copy of the shared file added to `testHeavyLegacyRestart`'s tree is one of the two setup edits
and is not grounds to refuse the landing.

**Alternatives, each rejected by the designer:**

- **Keep the list out of the document, as `D-GR36` did.** `D-GR36` gave the rule and the
  command and no list, and the next plan still left a copying fixture out. A dated list that
  the rule overrides is cheaper than a third report of the same kind.
- **Have `tools/fork-check.py` make its call without the shared file.** The legacy gate and the
  adapters would then each hold the call, which `D-GR34` let the builder avoid, and the
  fixture's edit is one copied file.
- **Have the fixture's appended `main` write status words itself and not call `check_status`.**
  That edits what the test exercises: it exists to keep the real gate's scheduler and status
  words in the spans it writes.
- **Have the fixture copy all of `tools/gate/`.** Rejected in `D-GR36` for the other Go
  fixture, for the same reason: the `.keep` file is there so that the tree has no declarations.

#### The second flaky-check request lands the rule in the classifier and its gate faces

What follows is the designer's decision of 2026-11-22 (`D-GR41`), from the operator request for
"Slice 2 of the flaky-check rule" and made to answer a build worker's report against it (at
`0450fedb34`). None of it is the owner's ruling except the two quoted 2026-11-21 statements of `D-GR21` and the split ruling
[`OQ-SP1`](./tillr-item-split.md#OQ-SP1) quoted in `D-GR32`; the owner may overturn any part of
the designer's decision.

**The report.** The request quotes both 2026-11-21 owner statements of `D-GR21` and asks for the
whole of slice 2: add `disabled` to the one classifier slice 1 created; on a single contradicting
observation on the same tree, disable the check and file its repair item (to re-enable it
correctly) before any other work; treat a disabled check as neither green nor an error and let it
block nothing; let nothing later undo or skip the disabling or filing; keep an enabled check's
pass satisfying a fixer that names a labelled selector; and take only what this slice needs from
`refs/tillr/runs/run-item-request-c1a6f8e4e5-a14-n5` `[unlanded]`. The worker reported three
contradictions between those words and the repository at `0450fedb34`:

1. **Five of slice 1's seven slices are not yet on the trunk, and the request's scope spans six
   later slices.** At `0450fedb34`, only slices 1a (`020cf29eca`, `tools/tillr/checkresult` and
   `tools/tillr-gate/classify.go`) and 1e (`0bcc3275a2`, per-check Python adapters in
   `tools/gate/` calling `tillr-gate classify`) have landed. `rg 'checkresult' tools/` at
   `0450fedb34` matches only `tools/tillr/checkresult/`, `tools/tillr-gate/classify.go` and
   `tools/gate/classification-scan.test.py`, and `UNMOVED_PYTHON` in
   `tools/gate/classification-scan.test.py` still holds `("tools/fork-check.py",)`: slices 1b,
   1c, 1d, 1f and 1g are unbuilt, and the request's criteria span slices 2a, 2b, 2c, 3a and 3b
   across `tools/tillr`, `tools/tillr-gate`, `tools/tillr-drain`, `tools/tillr-land`,
   `tools/gate` and `tools/fork-check.py`.
2. **The owner's second 2026-11-21 statement was not recorded in `D-GR21`.** The request quotes
   that statement verbatim; it is now recorded in `D-GR21` under
   [A contradicted check is disabled](#a-contradicted-check-is-disabled) and in the ledger.
3. **Filing at the observation versus the marker sweep, and the installed `tillr-gate` boundary.**
   `tillr-gate` has no access to the SQLite queue (`D-GR23`), and Python adapters (`tools/gate/`)
   and replays call the installed `tillr-gate` on `PATH`, which deploys only from the green
   pointer (`D-GR5`, [Section 6.7](#67-deploys-read-the-green-pointer)). Moreover, if slice 2a
   made `tillr-gate run` and `tillr-gate checks` emit `disabled` on live runs before slices 2b,
   2c and 3a landed, existing callers on `0450fedb34` would treat `disabled` as a failure or
   corruption (`parseTreeCheckJSON` in `tools/tillr-drain/drain/heavy.go` rejects any result
   outside `pass`, `fail` and `unavailable`; `allChecksPassed` in
   `tools/tillr-drain/drain/heavy_revert.go` and `fixerNamedChecksPass` in
   `tools/tillr-drain/drain/fixer_gate.go` require every check to equal `pass`) and no sweep
   would exist yet to file the repair item.

**How "at that observation ... before any other work" and the marker sweep fit together.** The
rule has two halves across the process boundary (`D-GR23`), and neither waits on a verdict, a
retry or later cleanup:

- **At the observation (slice 2a):** the moment a single opposite record completes a
  contradiction on one fingerprint (one pass and one fail, in either order, with no count or
  retry), `tillr-gate` atomically writes `disabled/<sha256(check_id)>.json` and copies any
  recorded pass and fail output logs into `disabled/` before it prints its output or returns. No
  cleanup failure, sibling check failure or context cancellation after that write removes the
  marker or its copied logs.
- **Before any other drain work (slice 2b):** `tillr-drain` sweeps `disabled/*.json` at the start
  of every drain pass and immediately when every gate, replay, rolling or heavy step returns —
  whether that step returned a result, an error or a cancellation, and before attribution,
  pending reverts (`revertPendingHeavy`), fixer settlement or worker dispatch. Each unfiled
  episode gets one deduplicated repair item (`flaky-<check-id>`, `-2`, `-3`, ...) asking to fix
  the flaky check and re-enable it correctly (`D-GR21`), carrying the fingerprint, both times and
  both retained output locations.

**Why slice 2a splits from live runner emission (slice 2d).** For the same two reasons slices 1a
(`D-GR32`) and 1f (`D-GR33`) landed before their callers — the installed-binary boundary of
`D-GR5` and [`OQ-SP1`](./tillr-item-split.md#OQ-SP1) — the rule lands in the classifier and its
`tillr-gate` faces first (slice 2a), while turning on live `disabled` emission in `tillr-gate
run`, `tillr-gate checks` (`G2`, `G3`, `G4`, `G7`) and the Python store-backed adapters (`P4` to
`P6`) moves to slice 2d, after slice 1b, a proved trunk commit holding slice 2a, slice 2b (the
drain's marker sweep), slice 2c (every Go consumer handling `disabled`) and slice 3a
(cached-evidence filtering) have landed. That way no live gate or proof run emits `disabled`
while `tillr-drain` would misread it as an error or drop its repair item.

**What this request's landing (slice 2a) builds:**

- **Rules 1 to 3 in `tools/tillr/checkresult`.** `Classify` evaluates rules 1 to 4 in order:
  1. when the observation marks the check as under proof for its own repair (`proving` true), skip
     rules 2 and 3 and classify the raw result by rule 4;
  2. when the observation carries an active `disabled` marker for the check, answer `disabled`
     with the marker's evidence (`fingerprint`, `pass_at`, `pass_log_ref`, `fail_at`,
     `fail_log_ref` and any `repair_item`), whether or not a raw result was supplied;
  3. when the fingerprint has both a pass and a fail (either both already stored or one stored and
     the other supplied by this observation's rule-4 `pass` or `fail` raw result, and not matching
     the check's resolved episode in `enabled/`), answer `disabled` with both times and both log
     locations (`""` when unrecorded);
  4. otherwise classify the raw result under its named protocol (`D-GR28`), or answer `unknown`
     with detail `unobserved` when no raw result was supplied (a pre-run query on an enabled
     check).
  `Aggregate` already leaves `disabled` out of every order (`020cf29eca`); unit tests in
  `tools/tillr/checkresult` pin `disabled` exclusion across all four orders (`runner`, `shadow`,
  `legacy-gate`, `legacy-replay`), including an all-`disabled` run passing with 0 passed and 1+
  disabled.
- **Store-backed mode on `tillr-gate classify`.** Without `--repo` or `--store`, `tillr-gate
  classify` stays pure: it touches no Git repository and no file on disk, so every caller from
  slice 1e (`0bcc3275a2`, including tests that run in a temporary directory outside Git such as
  `tools/gate/change-gate.test.py`) keeps working unchanged. With `--repo <dir>` (and optional
  `--store <dir>`, defaulting to the worktree's common-dir `tillr-gate/results` under `--repo`,
  plus repeatable `--prove-disabled <check-id>`), `tillr-gate classify` resolves each
  observation's `check` against `tools/gate/checks/*.json` on `commit` (defaulting to `HEAD`, or
  from the worktree when `worktree` is true) by exact match on `<id>`, `<id>:<label>` or the
  legacy span id `<id>/<slug(label)>` for a fast declaration (never by bare `<id>/` prefix, and
  failing on an ambiguous match); uses the observation's explicit `fingerprint` when a per-unit
  caller (`documents` P5 or `go-build` P6) supplies one, or else computes the canonical
  declaration fingerprint from the declaration bytes committed in that tree; reads `disabled/`,
  `enabled/` and `<fingerprint>.pass.json` / `<fingerprint>.fail.json`; calls
  `checkresult.Classify`; and when rule 3 fires with no marker present, atomically writes
  `disabled/<sha256(check_id)>.json` after copying any existing pass and fail logs into
  `disabled/`, before printing the JSON array. The classification output carries the canonical
  declaration id and, when `disabled`, the episode and evidence fields. An observation with no
  raw result is accepted as a pre-run query.
- **`tillr-gate enable`.** `tillr-gate enable --check <check-id> --episode <episode> [--worktree
  <dir>] [--store <dir>]` validates the episode string (`<fingerprint>|<pass_at>|<fail_at>` in
  RFC3339Nano UTC), checks `disabled/<sha256(check_id)>.json`, records the resolved episode in
  `enabled/<sha256(check_id)>.json` so a stale cached proof from before the repair cannot
  re-disable the check on the old pair, moves the contradicting `<fingerprint>.pass.json` and
  `<fingerprint>.fail.json` records (and copied logs) into `resolved/`, and removes the marker
  only when the marker's episode matches `--episode` (leaving a newer episode's marker untouched,
  and succeeding idempotently when the episode is already enabled and no marker holds another
  episode).
- **`log_ref` on stored records.** `storedResult` in `tools/tillr-gate/memory.go` gains
  `log_ref` (`omitempty`) populated when `tillr-gate` publishes a pass or fail record; older
  records with no `log_ref` remain valid. `tillr-gate run` and `tillr-gate checks` keep their
  existing live `pass`/`fail` behavior in slice 2a (and `TestMemoryFailRetriesAndFlakyPass` and
  `TestMemoryOppositeWritersNeverErase` stay green until slice 2d replaces them).
- **What may be taken from `refs/tillr/runs/run-item-request-c1a6f8e4e5-a14-n5` `[unlanded]`.**
  Only the marker, episode, `enabled/` fence, `resolved/` archive and `tillr-gate enable`
  store mechanics that fit `checkresult` and `tillr-gate` may be adapted from
  `refs/tillr/runs/run-item-request-c1a6f8e4e5-a14-n5` `[unlanded]`. Nothing in
  `tools/tillr-drain`, `tools/tillr-land`, `tools/fork-check.py` or `tools/gate/*.py`, no raw
  `== "disabled"` consumer branch, and no `TILLR_DISABLED_OBSERVATIONS` side-channel variable may
  be taken from that ref. `just pack-sync` mirrors the `tools/tillr` and `tools/tillr-gate` edits
  into `yolo-packs/tillr/`.

**Unmet cases of the request, left to later slices and named in the candidate's commit message:**

| Request case | Slice that meets it | Why it is outside slice 2a |
| :--- | :--- | :--- |
| Remaining slice-1 callers (`G1`–`G12`, `D1`–`D34`, `--aggregate`, `F6`/`F9`) | Slices 1b, 1c, 1d, 1f, 1g | Prerequisite single-classifier routing (`D-GR23`, `D-GR28`, `D-GR33`) |
| Filing `flaky-<check-id>` (and `-2`, `-3`, ...) to re-enable the check, before attribution, pending reverts or dispatch | Slice 2b | Lives in `tillr-drain`, which owns the SQLite queue (`D-GR23`) |
| Drain and lander sites (`D1`–`D34`, `L3`) treating `disabled` as non-green, non-error and non-blocking, dropping cached `disabled` proofs, and passing `--prove-disabled` for the repair item | Slices 2c and 3a | Must land in `tillr-drain` / `tools/tillr-land` before live gate runs emit `disabled` |
| Live `tillr-gate run` / `tillr-gate checks` (`G2`, `G3`, `G4`, `G7`) and Python store-backed adapters (`P4` to `P6`) emitting `disabled` and `--prove-disabled` | Slice 2d | Waits on a proved trunk commit holding 2a (`D-GR5`) and on slices 1b, 2b, 2c and 3a so consumers and the sweep are live first |
| Committed declaration bytes (`P1`), retained output logs (`P3`), legacy gate/replay `DISABLED` handling (`tools/fork-check.py`), replaying older commits through current classification, and resolving labelled fixer selectors (`D16`, `D31`) through the installed face | Slice 3b | Waits on slices 1g and 2d |

**What a verifier of this landing judges.** The candidate builds slice 2a only: rules 1 to 3 in
`tools/tillr/checkresult`, store-backed `--repo` / `--store` / `--prove-disabled` on `tillr-gate
classify` (including exact `<id>`, `<id>:<label>` and `<id>/<slug(label)>` selector resolution,
committed-declaration fingerprinting, optional per-unit `fingerprint`, pre-run query with no raw
result, and atomic marker + log-copy creation on a single contradiction), `tillr-gate enable`
with episode matching and `enabled/` + `resolved/` transitions, `log_ref` on stored records, Go
tests covering each of those behaviors, and `yolo-packs/tillr/` synced via `just pack-sync`.
Plain `tillr-gate classify` with no flags stays pure, `tillr-gate run` and `tillr-gate checks`
do not yet emit `disabled`, and a verifier does not refuse this landing for the unmet cases in
the table above, which the candidate's commit message lists.

**Alternatives, each rejected by the designer:**

- **Build slices 1b–1d, 1f–1g and 2a–3b in one commit.** Violates [`OQ-SP1`](./tillr-item-split.md#OQ-SP1)
  and breaks on `D-GR5` because Python adapters and replays invoke the installed `tillr-gate` on
  `PATH`, which lacks the new flags until a trunk commit holding slice 2a is proved green.
- **Have `tillr-gate run` and `tillr-gate checks` emit `disabled` in slice 2a before slices 2b,
  2c and 3a land.** Every live caller in `tillr-drain` at `0450fedb34` rejects `disabled` or
  treats non-`pass` as failure, and no marker sweep exists yet to file the repair item.
- **Open the SQLite queue inside `tillr-gate` so `tillr-gate` files the repair item itself.**
  Rejected in `D-GR23`: `tillr-gate` runs inside isolated worktrees and tests with no queue, and
  marker writing at the observation plus the drain's step-boundary sweep meets "at that
  observation ... before any other work" without coupling the runner to the queue.
- **Read the repository and result store on every `tillr-gate classify` call with no flag.**
  Breaks existing pure callers from slice 1e (`0bcc3275a2`) that run outside a Git worktree or
  assert zero filesystem side effects.
- **Carry `TILLR_DISABLED_OBSERVATIONS` or scattered `== "disabled"` checks from
  `refs/tillr/runs/run-item-request-c1a6f8e4e5-a14-n5` `[unlanded]`.** Rejected in `D-GR23`: that
  side-channel architecture was refused eleven times for missing sites.

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

The landing target is 10,000 milliseconds: 1,000 for planning, fingerprints and the
squash, leaving 9,000 for checks. A target measures cost; it never kills a working
check or rejects a candidate for machine load. The governing rulings are
[`D-GR15` and `D-GR16`](#decision-ledger).

**Admission is arithmetic.** The `gate-budget` change check sums every fast
`budget_ms` against the unchanged 9,000 millisecond cap, as if all checks ran
serially. A declaration that does not fit must make room, become cheaper or move to
the heavy tier. Fast declarations may not name `timeout_ms`. A heavy declaration
and a generated declaration use their timeout as a measured target, never a kill
deadline.

**What the cap covers.** The cap bounds the declared cost of the checks every
candidate runs, which are the fast declarations under `tools/gate/checks/`. A check
planned only for some candidates is outside the sum: `go-substrate`, `gate-runner`
and the affected Go test packages of
[Section 6.12](#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands).
The owner called the ten seconds aspirational on 2026-11-21 and accepted about 40
seconds for the tests a drain change selects ([`D-GR38`](#decision-ledger)); stating
the cap's scope this way is the designer's ([`D-GR39`](#decision-ledger)).

**Only a stall ends a check early.** The watchdog (coined here: the observer of a
running check’s progress, not an elapsed-time limit) samples every 1,000 milliseconds.
Progress is at least one new log byte, including a heartbeat, or CPU progress
from the selected source. Sixty consecutive samples with neither is a stall.
The runner kills the process group and records `stalled`, exit 6, distinct
from pass, fail, error and over-budget. Its verdict entry, log and summary name
the check, last output line, group CPU time, silent samples and run time.

Linux prefers an
[inherited kernel task clock](https://man7.org/linux/man-pages/man2/perf_event_open.2.html)
that retains descendant CPU independently of reaping or discarded child status.
Attachment precedes exec and any children: Go's disabled launching-thread counter
is inherited and enabled only at the child's exec, and its launching thread is
reserved until the counter closes so no other check inherits it. Python's child
waits for an exec-release byte while the counter attaches. Observer CPU is
excluded.

A refused counter selects **process accounting**: read-only samples of the
check's existing process group, never a supervisor that can delay, stop or signal
the check. Members are identified by PID and start time from
[process stat](https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html).
Each member's runtime is the sum of its threads' scheduled CPU in nanoseconds from
[Linux scheduler accounting](https://www.kernel.org/doc/html/latest/scheduler/sched-stats.html).
CPU progress is a change in any member's runtime, a change in the member set, or
a member disappearing while read. This rule remains effective when a total does
not increase: a child that runs and exits between samples requires a starter that
runs or exits; following starters back reaches a member of the previous sample
whose runtime or presence changes. Nested watchers read their own groups without
a shared service; the watcher's CPU is not progress by its idle child.

The fallback's reported `cpu_time_ms` is the highest observed sum of members'
runtime plus their reaped children's CPU, which process stat carries in `cutime`
and `cstime`. The sampler captures all members' reaped-child CPU before reading
any thread runtime, preventing a child reaped during a sample from being counted
both directly and through its parent. It is a lower bound, not a claim to retain
every child's total: children whose exit status is discarded may be absent from the total, but still
count as progress by the rule above. A process leaving the group is not observed.
The kernel source compares unrounded nanoseconds; the fallback compares runtime
and membership independently of its reported total.

The log names `watchdog CPU source: kernel task-clock counter` or
`watchdog CPU source: process accounting`, and `cpu_source` in the verdict
names which supplied the last successful measurement. The Python checker uses
the same source names and rule and exports the source in stall observations.
If neither source is readable, including unreadable process or thread runtime
files, no stall is declared: `watchdog unavailable` is printed immediately
and recorded, the source field is absent, and the check runs without detection.
Unreadable observations from a selected source likewise disable detection rather
than inferring a stall; a kernel counter decrease also disables it. A check that
could not start or crashed remains `error`, exit 4.

**Overruns are observations, not verdicts.** Checks run as long as they make
progress, retaining their own pass or fail. Their declared target, measured
duration and `over_budget_ms` reach the store and observatory, whose per-check
performance payload reports median, p95, maximum, budget, overrun count and stall
count. Nothing is carried at its budget; `--tree-overrun carry` is refused.
There is no finite worst-case duration derived from the budgets: the admission
cap bounds declared cost, not running work.

**A stall has a named next step.** The first stall retains the candidate without
a worker run, attempt charge, failing verdict or design conflict. Its waiting
reason is `gate-stall-rerun`, naming the check and the number of gates in flight
at the stall. The first planning pass with fewer gates, or no other gate, judges
that same head again. A second stall on the same head tree holds it as the item
fault `gate-stalled`, retaining both observations. The operator diagnoses and
clears it with `tillr drain clear-fault`. `gate-harness-error` stays for a check
or gate program that could not start or crashed. The retained candidate, first
stall and second-stall recovery survive a drain restart. The first-stall wait
and observations commit in the same transaction as its judgment and spans.

The runner and drain implement this rule. The gate program’s `--gate-timeout` and
the remaining Python gate clocks still have their old bounds; the
[wrapper watchdog slice](#route-the-gate-programs-own-time-limits-through-the-stall-watchdog)
replaces those separately. `go-build` uses `-trimpath` and the standard shared Go
cache to avoid rebuilding identical packages at every lane path. No product test
may assert elapsed time; the timing audit’s existing rule remains unchanged.

**Shard lifetime and broken tests under load (`D-GR17`).** On 2026-11-19, with thirty lanes active,
fifteen `go tool test2json` shard processes from [`tools/go-test-shards.py`](../../tools/go-test-shards.py)
([sharded runner contract](../pi/tillr-go-shards-2026-11-17.md#runner-contract)) were found
orphaned under PID 1 for 20 to 55 minutes after the worker commands that started them had returned.
The owner stated two rules: *"Rule of this repository: a test that fails or hangs under load is a
broken test; do not add a timeout, a retry or a longer wait as the fix"*, and a test run started by
a worker cannot outlive the command that started it (unless a documented reason requires it, which
none does for test shards). Because `run_shard` starts each `test2json` child in its own session
(`start_new_session=True`), ending or killing `tools/go-test-shards.py` leaves shard process groups
alive unless the runner binds every shard process group's lifetime to the `go-test-shards.py`
invocation and keeps the temporary binary directory intact until every shard exits. Under high
concurrency, tests in `tools/tillr-drain/drain` also hung or failed at synchronization causes
(polling indefinitely for a fixture marker when the producer had exited or failed to start, or
relying on a single manual heartbeat tick that `beat` drops when SQLite is busy). Answering the
2026-11-20 fence conflict on that repair: the build touches `tools/tillr-drain/` (with
`yolo-packs/tillr/` mirrored via `just pack-sync`, keeping `runTests` in
`tools/tillr-drain/main_test.go` self-contained or paired with `tests/tillr-testmain_test.go`),
`tools/go-test-shards.py`, and `tests/` (`tests/go-test-shards.test.py` and
`tests/tillr-testmain_test.go`).

**Completions lost after cancel in drain tests (`D-GR29`).** This is the designer's decision of
2026-11-21 on an operator request, not the owner's ruling, and the owner may overturn it. The
request reports that the heavy run of trunk commit `63349620da` stalled on 2026-11-21 in
`TestPolicyReloadAtPassBoundaryKeepsLiveRequest` (at 8:26 PM Eastern its single-test replay had
been at 0% CPU for 352 seconds), and asks for the repair of commit `4790d2ad43`
in four test files of `tools/tillr-drain/drain`, saying all four cancel the context and then
receive from the loop's completions channel. Read at `86e2549392`, one of the four does:

| Test file | What waits on the completions channel | Context at that point | Can lose the completion |
| :--- | :--- | :--- | :--- |
| `policy_reload_test.go` | the deferred cleanup of `TestPolicyReloadAtPassBoundaryKeepsLiveRequest`, after it calls `cancel()` | canceled | yes |
| `fixer_intermittent_test.go` | `runPassAndDrain` | `context.Background()` in every caller | no |
| `pass_measurement_test.go` | `TestDispatchWithoutAGateRecordsAPassAndDecision` | `context.Background()` | no |
| `lane_dirty_test.go` | the receive after `launchExamination` | `context.Background()` | no |

The cause is in `sendCompletion` (`tools/tillr-drain/drain/completion_handoff.go`): a worker
sends its completion or, when its context is done, returns without sending. Once the context is
canceled both branches are ready and Go picks one at random, so a test that cancels and then
waits for one completion per active run can wait forever. A context that is never canceled has
only the send branch, so the three other files cannot lose a completion this way, and the
request's words about them do not match the tree. The decision:

- **Only `policy_reload_test.go` is repaired.** Its cleanup must not receive from the
  completions channel after the context is canceled. The worker in that test (`policyRunSteps.Run`)
  returns only when its context is done, so moving `cancel()` after the receive is not enough: the
  worker gets another way to be released (a channel the cleanup closes, as `triggerSteps.release`
  is in `trigger_test.go`), the cleanup releases the workers and receives every active run's
  completion while the context is live, and `cancel()` runs last. `drainOwned` in `pass_test.go`
  is the helper to use if the released worker's completion passes through `takeCompletion` without
  a stop or an error in this fixture; otherwise the cleanup keeps its present receive-and-delete
  loop, moved before the cancel. A released worker returns an error, as a canceled one does
  today, so the loop does not read the release as a finished run. The receive in the repaired
  cleanup waits under `hangGuard`, the five-minute deadlock detector in `pass_test.go` that
  `drainOwned` already uses: the detector names the test if the repair is wrong, and it is not
  the time limit this decision rejects, which is one put in place of the repair. What the test
  asserts does not change.
- **The three other files are not changed.** A build criterion that names all four files is met
  by the one repair together with a commit body that names the three and gives the reason above.
  Adding a time limit to their receives is not the repair
  ([`D-GR17`](#decision-ledger): a hang is fixed at its cause). A second draft of this decision,
  written the same day against the same request and not landed, put each of the three receives
  under `hangGuard` so that a hang from some later cause would fail one test by name. It is not
  adopted: by that draft's own account it repairs no known stall, the request asks for the repair
  of a cancel order these files do not have, and a guard on a receive that cannot lose its
  completion is a separate change that needs a request of its own.
- **The mirror is regenerated**, not edited: `just pack-sync` copies the file to
  `yolo-packs/tillr/files/local/share/tillr/tillr-drain/drain/`, in the same commit.
- **The commit body answers whether the real loop can lose a completion after cancel.** The
  designer's reading at `86e2549392`, for the builder to confirm and state: yes, by design, and
  the loop does not wait on what it drops. `Loop.Run` cancels on its way out and then joins the
  examinations and the heavy runs without receiving from the channel; the comment above
  `sendCompletion` says a run's durable outcome belongs to the successor loop, which reads it
  from the store. One place does receive without a guard: `Loop.Examine` in `recovery.go` receives
  one completion after `launchExamination` with the caller's context, so a caller that cancels
  between the launch and the send could leave it waiting. That is named in the commit body and is
  not repaired in this change: it is production code outside the request, this document files
  nothing for it, and it needs a request of its own.
- **Verification** is the one test, repeated, never the suite:
  `cd tools/tillr-drain && go test ./drain -run '^TestPolicyReloadAtPassBoundaryKeepsLiveRequest$' -count=50`.
  The loss is random, so the same command on the base may or may not hang; the commit body says
  which it did.

**Rejected:** rewriting the three other files to match the request's words, since they have no
cancel to reorder; a time limit on the receive as the repair; a `hangGuard` on the three
receives that have no fault, as the second draft proposed; repairing `Loop.Examine` in the same
change; changing `sendCompletion` so tests
may cancel first, since dropping the notification after cancel is what lets the real loop exit.

**What a third draft added, and what was built.** These are the designer's notes of 2026-11-21,
not the owner's ruling, and the owner may overturn them. A third draft of this decision was
written the same day against the same build worker's report and did not land, because the
decision above was already on the trunk. It reached the same answer (one file repaired, three
unchanged) and is not adopted as a separate text. One fact from it is kept, because it explains
how the request came to name four files: the request's search,
`rg 'takeCompletion\(<-l\.completions' tools/tillr-drain/drain`, finds a receive and says nothing
about a cancel before it. Run at `c6a01f6e84`, it lists `fixer_intermittent_test.go`,
`lane_dirty_test.go` and `pass_measurement_test.go`, the three files without the fault, and not
`policy_reload_test.go`, whose receive is spelled `c := <-l.completions`.

The repair is built in commit `c6a01f6e84`. The cleanup of
`TestPolicyReloadAtPassBoundaryKeepsLiveRequest` closes a `release` channel on `policyRunSteps`,
receives every active run's completion while the context is live, and cancels last; a released
worker returns an error; the mirror under `yolo-packs/tillr/` is in the same commit; and the
commit body names the three unchanged files and says the real loop can drop a notification after
cancel by design. The built cleanup differs from the decision in one point: its receive is a bare
`c := <-l.completions`, not one waiting under `hangGuard`. The designer accepts it as built: the
context is live at that receive, so it cannot lose a completion in the way this decision
repairs, which is the reason the three other receives are left without the detector. The commit
body does not record a repeated run of the test or name `Loop.Examine` in `recovery.go`; both
were asked for above, neither changes the repair, and the unguarded receive in `Loop.Examine`
still needs a request of its own.

**A fourth draft, and the rule the repair keeps.** These are the designer's notes of 2026-11-22,
not the owner's ruling, and the owner may overturn them. A fourth draft of this decision was
written on 2026-11-21 against the same build worker's report (the request says four files cancel
and then receive; at `86e2549392` only `policy_reload_test.go` does). It could not be merged
onto the trunk, which by then held the decision above and the repair in commit `c6a01f6e84`. It
reached the same answer and is not adopted as a separate text. The worker's report is answered
by what is already recorded: the build is narrowed to `policy_reload_test.go` and its mirror,
that build has landed, and no build work remains under this request. Checked again on
2026-11-22 at `f034817abd`: `rg 'cancel\(\)' tools/tillr-drain/drain/fixer_intermittent_test.go
tools/tillr-drain/drain/pass_measurement_test.go tools/tillr-drain/drain/lane_dirty_test.go`
lists one line, in `TestCanceledSessionStartupIsAnOperatorStop` of `pass_measurement_test.go`,
which cancels before `l.Run` and receives from no channel. One sentence of the fourth draft is
kept, because it states the repair as a rule a later test can be checked against: no test
cleanup makes a blocking receive from the loop's completions channel after it cancels the
context its workers send under.

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
| `auto_revert` | `true`, `false` | `false` | `true` (since 2026-11-18) |
| `budget_ms` | milliseconds | 10,000 | 10,000 |
| `red_cap` | `landings` and `minutes`, each a non-negative integer | 20 landings, 60 minutes | 20 landings; minutes 0, so no time bound (since 2026-11-17) |
| `lane_base` | `green`, `tip` | `green` | `green`, by the owner's ruling of 2026-11-21 ([`D-GR24`](#decision-ledger)) |

The absent default is the side that cannot leave a trunk red. An unknown value is refused by
name and the loop does not start.

**The red cap.** Under `after-merge`, landing stops when the trunk tip is `red_cap.landings`
landings, or `red_cap.minutes` minutes, past the green pointer, whichever comes first. A refused
landing names the failing heavy check and waits for its fixer; lanes go idle and the range a fixer
must read stays small. A zero turns that bound off, and both zero means no cap. The cap exists
because an uncapped red range is a cost that grows with trunk activity, and a mechanism whose cost
rises with trunk activity is pointed the wrong way. Under `before-merge` the trunk is never red and
the cap never fires. ([OQ-GR1](#decision-ledger).) The owner ruled the cap out on 2026-11-18
([OQ-GP7](tillr-graph-is-the-path.md#OQ-GP7) of the graph design): under a red check, work lands
only if it does not touch what that check reads, with no landing cap. The cap stays live in the
tree until that rule is built.

**The lane base.** With `lane_base` set to `green`, a new lane's worktree is cut from the green
pointer, so its worker starts on a passing tree and does not spend its run on a failure it did not
cause; its change is squashed onto the tip when it lands. An item blocked by another waits until
the green pointer contains the blocker, usually a few minutes. A repository may set `tip`, which
cuts lanes from the newest trunk commit. With no green pointer yet, lanes are cut from the tip.
([OQ-GR2](#decision-ledger).) The owner overturned the `green` default on 2026-11-17: work is
always built against the latest trunk, and this repository set `tip` (`1fa2c069c`).

**The owner's ruling of 2026-11-21 replaces the 2026-11-17 one for new work**
([`D-GR24`](#decision-ledger)). His words, verbatim:

> So I think the real rule should be you can only start a new item if the previous item's trunk landing went green because you don't want to start it until the other one has landed on the trunk and it's okay and then you always just base an item on the most recent green on the trunk, and I think that satisfies everything.

> And yes, of course, a trunk fixer is meant to only be based on a red change, so of course it's based on a red one.

This repository therefore sets `lane_base` to `green`. Three things follow, and all three are the
`green` behavior the drain already has
([the drain's README](../../tools/tillr-drain/README.md#the-commit-a-new-lane-starts-from)):

- A new lane is cut from the green pointer, the last proved trunk commit.
- An item whose blocker has landed does not start until the heavy runner has proved a trunk commit
  that contains that landing. The item is held with the reason `trunk-unproved`, not `blocked`: the
  blocker's work is finished, and the proof of the trunk is what is pending.
- A trunk fixer (the item filed to repair a red heavy check) starts from the trunk tip, because the
  failure it repairs is not in the older proved commit.

The request that carried the ruling reads "the previous item" as the item's blocker, not as
whichever item the queue started last. That reading is the operator's, not the owner's, and the
owner may overturn it.

**Where `green` as built differs from the owner's words.** The owner said "always" base an item on
the most recent green, and that an item starts only once the other one's landing "went green". The
code read on 2026-11-21 (`tools/tillr-drain/drain/lane_base.go` and the README section above) does
something else in the five cases below. The designer has changed none of them and decided none of
them: each is listed for the owner to rule on ([`D-GR25`](#decision-ledger)), and each stays as
built until he does.

| Case | What `green` does today | How it differs from the owner's words |
| :--- | :--- | :--- |
| The item's own block (its definition in a document) is newer than the green pointer | The lane is cut from the oldest trunk commit after green that carries the tip's version of the block, and the claim reports `<item>'s block is newer than green <green>; cutting its lane from <commit>`. The item does not wait for that commit to be proved | The base is an unproved commit, later than the most recent green |
| The item's design answer (a design seat's commit to a decision ledger) landed after the green pointer | The returned worker's lane is cut from the commit that holds the answer, when that commit is after green | The same: an unproved base |
| No green pointer exists yet | Lanes are cut from the tip, and no item waits for a proof | Nothing has been proved, so there is no "most recent green" to start from |
| The blocker is a fork item (its commits are delivered on a `patch/<fork>` branch, not on the trunk) | The dependent item starts once the delivered patch branch retains the blocker's judged landing. The green pointer cannot contain a fork commit | The blocker's landing is not proved by a green trunk commit |
| The blocker landed, but no landing is recorded for it | The dependent item waits (`trunk-unproved`) | Stricter than the words rather than looser: it waits for a fact the heavy runner alone cannot supply |

The 2026-11-17 overturn had one recorded cause: a lane cut from the green pointer was handed a
tree older than its own item's definition. The first two rows are how the code avoids that
today, at the price of an unproved base.

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
- **What.** Every heavy tree check (nothing is carried from the fast tier; `tillr-gate` refuses
  `--tree-overrun carry` under [`D-GR15`](#decision-ledger)). A check with a remembered pass for
  that tip's fingerprint does not run, so the cost of a run is the checks whose inputs a landing
  changed.
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

#### The proof worktree's registration is left locked

*Not the owner's ruling; the designer's decision, from an operator request, which the owner may
overturn ([`D-GR35`](#decision-ledger)).*

**What happened.** The operator request of 2026-11-21 reports that Git's registration for the
heavy runner's proof worktree was left holding a `locked` file that read `initializing`, created
at 9:40:29 PM ET, with no directory at the path. Every later heavy run failed at once, 20 times,
on `git worktree add --detach` with `is a missing but locked worktree`. The trunk could not be
proved, and no item could start from it, until the operator ran `git worktree unlock` and
`git worktree prune` at 9:43 PM ET. `initializing` is the reason Git itself writes while
`git worktree add` is at work, and removes when the add finishes.

**Why the runner did not recover.** Read at `27f91f6852`, `HeavyRunner.inTree` in
`tools/tillr-drain/drain/heavy.go` prepares its path in three steps under the worktree registry
lock: `git worktree remove --force <path>` with the result ignored, then deleting the directory,
then `git worktree add --detach <path> <tip>`. Measured on 2026-11-21 with Git 2.56.0 in a scratch
repository, against a registration whose `locked` file reads `initializing`:

| Command | Directory present | Directory missing |
|---|---|---|
| `git worktree remove --force <path>` | refused, exit 128: `cannot remove a locked working tree` | refused, the same |
| `git worktree add --detach <path> <tip>` after the directory is deleted | refused, exit 128: `missing but locked` | refused, the same |
| `git worktree unlock <path>`, then `git worktree remove --force <path>` | registration gone, exit 0 | registration gone, exit 0 |

So the first step is refused, the second deletes whatever directory the interrupted add left, and
the third is refused on every run after. The missing directory is therefore what the runner's own
second step produces, and is no evidence that the interrupted add never created one.

**The rule.** The proof worktree's path is the runner's own, and the lock that admits one runner
is already held when the path is prepared, so no other process has a claim on the registration.
Before its existing remove, and under the same registry lock, the runner reads whether the
registration for its own path is locked. When it is, the runner:

1. reads the lock's reason and the `locked` file's modification time;
2. runs `git worktree unlock <path>`;
3. reports one line in the loop log that names the path, the reason and that time, so the
   interruption is dated by the runner and not by an operator reading the directory afterwards;
4. continues with the remove, the directory deletion and the add as today.

The runner does not run `git worktree prune`: prune acts on every stale registration in the
repository, and the comment on this step already says recovery must not touch another worktree's
registration. The operator's `unlock` and `prune` cleared the same state by hand; `unlock` and
the path's own `remove` clear it for this one path. A test holds the two columns of the table: a
locked registration with the directory missing, and one with the directory present, each ending
in a created worktree at the tip.

**What interrupted the add is not established, and the change does not claim it.** The request
says the loop restarted in place onto build `27f91f685232` while the runner was creating the
worktree. The tree does not support naming the restart as the cause:

- `beginHeavyProof` sets `heavyProofActive` before `HeavyRunner.Run`, and `gateRestart` returns
  before the exec whenever `restartDeferred` sees that flag, both before and after it settles the
  request. That deferral has been in the tree since `edaa4fdf4b` (2026-11-20), so a build of
  2026-11-21 does not replace itself across a proof by this path.
- The request gives "about 9:40 PM" for the restart and 9:40:29 PM for the lock. Those two times
  do not order the events, so the add may have been started by the new process after the restart.
- The loop log of the outgoing process and the event rows for those seconds were not available to
  the design seat. Without them no code path can be named as what ran.

One path in the tree does produce this state without any restart, and is recorded as a
hypothesis, not a finding: the runner's Git commands are started with `exec.CommandContext`
(`heavy.GitOutput`), which ends the command with SIGKILL when the runner's context is canceled,
and a killed `git worktree add` cannot remove the lock it wrote. The commit that builds the rule
above therefore states the cause as unconfirmed, cites the deferral above as the reason the
restart is not named, and names the report line of step 3 as what will date the next occurrence.
Finding the cause is separate work that starts from that line or from the incident's logs.

### 6.7 Deploys read the green pointer

"Deploy" here means the programs the drain loop and its children run: the eight `tillr` programs
built by `just tillr-build`.

- **The build reads the pointer's tree**, not the working tree and not the trunk tip. The loop's
  first line names the green commit it was built from.
- **With no pointer the build refuses**, naming the heavy run that would create one. A loop that
  is already running keeps the programs it loaded.
- **A red trunk does not stop a deploy of the last green commit**, and never produces a deploy of
  a newer one.

**Pushing reads the pointer too.** Under `push.what = "green"` the drain loop pushes the pointer's
commit to the remote branch when the pointer advances, so the remote never receives a commit the
proof has not passed. [Pushing what the proof has passed](../reference/tillr-restart.md#pushing-what-the-proof-has-passed)
is the reference.

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
| Red at the pointer too | The inputs did not change, so no landing is at fault. It is reported as a gate fault with the check's `needs` and `tools`, and no fixer is filed against a landing. One fixer is filed against the tip instead, so the red has a mover (built 2026-11-18). |
| Attribution answers no verdict | No landing is named. As built on 2026-11-18, one fixer is filed against the tip, and its brief says why attribution failed. A loop stop is not this row: nothing is filed. |
| Red, then green with no input change | The check is disabled and one repair item is filed, per [A contradicted check is disabled](#a-contradicted-check-is-disabled) (`D-GR21`). The trunk is not red on it, and the check is not reported green. |

**With `auto_revert` on** (built 2026-11-18, on in this repository), the heavy runner builds
the inverse of L on the tip, as one commit in its own checkout, and lands it only when three
things hold: it applies cleanly, the fast gate passes with it, and the failing check, replayed
on the tip with the revert, passes. It lands through the same lander as any change, as the item
`revert-main-<L's first twelve hex digits>`, which the red cap never holds. L's item then moves
from `landed` back to `in-queue` (the one move out of `landed`, which needs a reason and an
actor), and its run carries a returned ending, `reverted`, with the check's output and log. The
item's next gate runs that check as a required one until the item lands again: a fail refuses
the candidate with the output, and a check that answers no verdict does not refuse, because
unproven is never red. The moved trunk is then proved again, so a green proof releases every
hold. Nothing is counted: every red that names one landing is offered for revert.

The revert is not attempted when L is itself a revert or a trunk fixer's landing, or when a
landed item declares a dependency on L's item. It ends "not possible" when it does not apply
cleanly, when nothing is left to revert, when the fast gate does not pass, when the check still
fails with L reverted, or when the lander does not land it. In each of those cases the fixer is
filed as with `auto_revert` off, and its brief names the reason; when the check still fails
with L reverted, the fixer is filed against the tip, because L alone is not the cause. Each
attempt is recorded as a `drain.revert` event, which the landings history in `tillr-view` shows
as a `revert` row. An unattributed red is never reverted.

A check that reaches its timeout is "no verdict": the pointer does not move, nothing is
attributed, and the line says which check and how long it ran.

#### A Go test that did not finish is not replayed

What follows is the designer's decision of 2026-11-21 (`D-GR31`), made on an operator request
and to answer a build obstacle; it is not the owner's ruling, and the owner may overturn it.

A **focused replay** *(coined here)* is the culprit search running one named Go test alone, as
`go test -json -count=1 -run '^<name>$' <package>`, on a landing between the pointer and the
tip. It is worth its time only when the test can end. A test that hung in the whole proof hangs
again alone, and each replay then lasts until Go's own ten-minute test limit.

The fix of 2026-11-21 7:46 PM Eastern (`5c78981400`) skips the focused replay for a test the
first proof names in Go's test-timeout panic (`panic: test timed out after`, followed by the
`running tests:` list). It did not stop the replays the operator request reports, and the
retained output of the two proofs before them says why (read on 2026-11-21 with
`rg -n 'panic: test timed out after|fatal error: all goroutines are asleep'` over the proofs'
retained case logs):

| Proof ended (Eastern) | What its case log for `TestPolicyReloadAtPassBoundaryKeepsLiveRequest` holds | Focused replay |
| :--- | :--- | :--- |
| 8:20 PM | `fatal error: all goroutines are asleep - deadlock!`; no timeout panic | Seen running at 8:26 PM |
| 8:48 PM | The same line; no timeout panic | Started about 8:48 PM; 503 seconds old at 0% CPU at 8:57 PM |

Both proofs ran the suite as `tools/go-test-shards.py tools/tillr-drain -k 6 --slot-class proof
--timeout 0`, and the shard line named the test (`drain shard 1 failed (exit 1):
TestPolicyReloadAtPassBoundaryKeepsLiveRequest`). In the whole shard the Go runtime saw every
goroutine blocked and ended the process at once with its deadlock error, so no timeout panic
was ever printed. The case therefore reached the runner as an ordinary named failure with an
empty `timeout_panic`, `GoTimedOut` stayed false, and `HeavyRunner.attribute` took the ordinary
path: `findFirstRed`, then `judgeHeavyGo` through the supervisor in `declaration` mode. Run
alone, the test was not ended by the runtime: the operator saw the replay idle at 0% CPU.

**The rule.** A named Go test is not replayed when the first proof's output for it holds
either of the two lines by which Go reports that a test did not finish:

| Line in the first proof's output for the case | Before `D-GR31` | With `D-GR31` |
| :--- | :--- | :--- |
| `panic: test timed out after`, with the test in the `running tests:` list | Not replayed (`5c78981400`) | Not replayed |
| `fatal error: all goroutines are asleep - deadlock!` | Replayed alone on each landing searched | Not replayed |
| Any other failure output | Replayed | Replayed, unchanged |

A case marked this way is handled exactly as the timeout case already is, at the same three
places and no others:

1. `HeavyRunner.attribute` names no landing and starts no culprit search; one fixer is filed
   against the tip, and the check's detail quotes the line and says it was not replayed.
2. `retainRedRepairs` starts no focused replay to decide whether that repair is kept; the next
   whole proof supersedes it.
3. When the same suite also has an unnamed failure, the suite check is not replayed either,
   because replaying the suite reruns the test.

Because no landing is named, no revert is offered, as for every unattributed red. The parser
that reads the proof's output (`heavy_legacy_progress.py`) recognizes the second line beside
the first and hands the runner the same yes-or-no fact; whether the existing `timeout_panic`
and `GoTimedOut` fields carry both lines or are renamed is the builder's choice. The build
names this path in its commit body and carries a test that drives a first proof whose named
case holds the deadlock line through `attribute` and asserts that no replay starts.

**No time limit is added**, to the proof, the replay or the supervisor: the operator request
forbids one, and under `D-GR15` exceeding a budget is never by itself a failure. The test's own
deadlock is not repaired by this decision; the fixer filed against the tip carries it.

**Rejected** (the designer's, with the rule):

- **Skipping the replay for every Go `fatal error:`.** Other runtime errors (a concurrent map
  write, an exhausted stack) end the process and can be replayed to a verdict; only the
  deadlock line says the test could not end.
- **A time limit on a focused replay.** Forbidden by the request, and a limit would report a
  hang as a slow pass-or-fail question it cannot answer.
- **Never replaying a named Go test.** It would give up the first red landing, and so the
  revert, for every ordinary assertion failure.

**Time to a revert (measured 2026-11-18).** The heavy runner starts searching for the
culprit and can offer a revert when a failed check ends, while the proof continues. Over the
recorded heavy runs read on 2026-11-18, `go-test-shards-test` alone took a median of 14.4
minutes and at most 52.7. The legacy check `go-tool/tools-tillr-drain` runs the `tillr-drain`
suite sharded. The parity case used to run it twice more; now it only lists and plans the suite,
so each proof runs it once. Three changes shorten the time from a landing to its revert:

1. **A verdict per check.** When a declaration check fails or the legacy program reports a
   failure on exit, the runner queues it for culprit search and a revert offer. Recovery handles
   one failure at a time in a separate checkout while the proof continues. Fixers are filed at
   the end of the run, and the pointer still moves only on a whole green run.
2. **Checks the change reaches go first.** The runner starts affected declaration checks in
   name order, then the legacy program if any of its watches match a changed path, then the
   remaining declarations in name order. If no legacy watch matches, the legacy program runs
   last. A declaration is affected when a path changed between the green pointer and the
   proved commit matches a file read or is under a directory read ending in `/`. Without a
   pointer or a matching check, declarations run in name order and the legacy program runs
   last. The set of checks is unchanged.
3. **The proof has the CPU first.** At startup, `tillr-run` sets each worker process group's
   niceness to 10, whether or not a proof is running. Later children inherit the lower
   priority, while the proof keeps the drain's priority. This uses no control group weight
   and adds no time limit.

The four roadmap items start at [faster recovery from red landings](../tillr/roadmap.md#add-faster-recovery-from-red-landings).

#### Bound concurrent tests, not just shards

On 2026-11-20 the operator observed a load average of 73 on 32 cores with five workers active.
The runner reported 16 ordinary and six proof slots occupied; `ps` showed test binaries with
`-test.parallel=48`. Counting only shards allowed 22 × 48 concurrent tests. The twenty-lane
case passed alone in 64 seconds at the same revision where the proof's shard watchdog fired
after 480 seconds without progress. Neither duration is the wall time of the proof.

**Designer's decision on the operator request (the owner may overturn it):** use one test
budget across proof, lane and operator runs, including tests started outside the shard runner.
State the budget as a share of available cores and charge each active test binary its permitted
parallelism. Proof priority allocates capacity *within* that budget, not on top of it. Report a
wait for capacity as a wait; the no-progress watchdog counts only time holding capacity, not
queue time. Do not retry a failed test or extend its timeout to compensate for excess load.

**Who verifies what:** the build lane demonstrates the bound and watchdog accounting with
targeted tests, including the 22-shard, 32-core case. It neither runs the full gate nor requests
`--slot-class proof`. The proof operator owns criterion 7 separately: record the full proof's
elapsed wall time and measured load average before and after the change on the same machine
with five lanes working and comparable workloads. Use the same load-average interval and
sampling point for both. Recover the before measurement from proof and host records if
available; otherwise collect a controlled baseline on the old revision. Until both observations
exist, report the comparison as pending operator verification, not as a lane failure or a pass.

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

### 6.10 What passed the fast gate and failed the heavy run

The owner asked on 2026-11-21, verbatim: *"Can we tell from our records when an item passed the
light gate and then failed the heavy gate? Because that's evidence we're going to want to look
at and decide something. Probably just making the light tests better if possible."* That is a
question and a leaning, not a ruling. An operator request of the same day answers it in two
parts, recorded as [`D-GR26`](#decision-ledger): a read-only report, and this section, which
reads that report over one period and ends in a proposal for the owner
([`D-GR27`](#decision-ledger)). Nothing here changes which checks the fast tier runs.

**The report.** `tillr view heavy-misses` *(the face name is the designer's)* opens the store
read-only and takes two required bounds, `--from` and `--to`. It lists each landing whose fast
gate passed and which a heavy failure later named: the landing commit, the item, the time, the
failing checks, whether an automatic revert landed or a trunk fixer repaired it, and the paths
the landing changed, with two summaries that count landings by check and by changed Go module.
Its reference is [`tools/tillr-view/README.md`](../../tools/tillr-view/README.md). Each failing
check carries one of four attributions *(coined here)*, because a failure record that names a
commit is not proof that the commit caused the failure:

| Attribution | What the records show |
| :--- | :--- |
| `confirmed` | An automatic revert of the landing landed, after the failing check passed on the trunk tip with the landing reverted |
| `offered` | The heavy runner named the landing, and the revert could not be done, so nothing tested the naming |
| `disproved` | The check still failed with the landing reverted, so the landing alone is not the cause |
| `named` | Only a trunk fixer's record names the commit. A fixer attaches to whatever commit is the tip while the trunk is red, so this row says the landing arrived on a red trunk, not that it broke it |

Three more terms are used below. An **association** *(coined here)* is one failing check recorded
against one landing; a landing can carry several. A landing's **bound verdict** *(coined here)* is
the `gate.verdict` event whose verdict ID, base commit and tree the landing record carries: the
fast-gate run that let that landing through. A **paired timing** *(coined here)* runs the same
candidate twice from the same warmed state, once with the fast gate alone and once with the
added suites, and reports the difference.

**The period and the reading.** The period is 2026-11-19 3:55 PM (included) to 2026-11-21
6:35 PM (excluded) Eastern. The report was read twice on 2026-11-21 with the command below, each
time from a private copy of the store taken through a read-only connection:

| Reading | Read at (Eastern) | Last event in the copy | What it lists |
| :--- | :--- | ---: | :--- |
| First, and the one every count below is taken from | 9:38 PM | 1361746 | 50 landings, three failure records not joined, no landing without a path list |
| Second, by the report's own build step for this change | 10:20 PM | 1363276 | the same 50 landings, with the same checks, attributions, outcomes and both summaries; only the copy's last event differs |

The period closed before either reading, and the 1,530 events between the two changed no row. So
no attributed landing needed reconciling, and the counts below hold for both readings.

```sh
tillr view heavy-misses --root '/path/to/repository' --store '/path/to/copy of state.db' --from 2026-11-19T15:55:00-05:00 --to 2026-11-21T18:35:00-05:00
```

The store holds 472 `land.landing` events with the result `landed` in the period (a read-only
query of the live store on 2026-11-21; the request counted 471 with `tillr view events`, and the
difference of one was not traced). The report lists 50 of them:

| Strongest attribution on the landing | Landings | Associations of that kind, over all 50 |
| :--- | ---: | ---: |
| `confirmed` | 11 | 17 |
| `offered` | 2 | 2 |
| `disproved` | 1 | 1 |
| `named` only | 36 | 76 |

- **Not joined.** Three failure records name two commits, `d8817ff2eebe` and `2127fe124d8c`,
  that no earlier landing in the store produced. The report lists them apart and counts them
  nowhere.
- **Not closed in the period.** 52 associations of a check with a fixer have neither a fixer
  landing nor a later green proof inside the period.
- **By changed module, all 50.** `tools/tillr-drain` 40, paths outside any Go module 27,
  `tools/tillr-item` 10, `tools/tillr-view` 7, `tools/tillr-run` 6, `tools/tillr` 4,
  `tools/tillr-land` 4, `tools/tillr-store` 1. A landing counts once under each module it
  changed.

The 13 `confirmed` and `offered` landings are the evidence: 2.8 percent of 472. The other 37 are
left out of every count below, for two different reasons. The 36 `named` landings carry no
evidence that they caused anything: one red drain test is recorded against every landing that
arrives while it stays red (`TestCommentsClosedBeforeDispatch/resolved` against six landings,
`TestTrunkObservationWriteCallbackContainsOnlyStoreWork` against five). The one `disproved`
landing, `3537272f7a`, carries evidence against itself: event 1336351 records that the drain suite
still failed on the tip with it reverted. The request's reading, that 33 of the 38 checks the
fixers name are tests in `tools/tillr-drain`, is true of the fixer records and overstates the
drain suite's share of the attributed landings, which is 4 of 13.

**The 13, and what became of each.** Times are Eastern and every commit named is on the trunk.
A module is named without its `tools/tillr-` prefix (`tillr` is the shared module `tools/tillr`).
The changed modules are read from the candidate the fast gate judged (the bound verdict's base
and head); the paths the landing changed on the trunk are the same list for all 13. All 13 changed
the drain: drain 13, item 7, run 3, view 3, `tillr` 3, land 1, store 1, and 7 also changed
paths outside any Go module.

| Landing | Landed | Modules the candidate changed | Check the heavy run failed | Attribution, with its event | What happened next |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `9628978cfc` | 11-20 2:15 PM | drain | the `tillr-view` suite | `confirmed`, 1319129 | reverted by `eace636684` after 31 minutes |
| `9cadb24d06` | 11-20 2:17 PM | drain, run | `go-test-shards-test` | `confirmed`, 1319011 | reverted by `1cd2f9b9d1` after 12 minutes |
| `129509992c` | 11-20 3:18 PM | drain, land, view | the `tillr-drain` suite | `confirmed`, 1320037 | reverted by `22e4ab208f` after 37 minutes |
| `68fb25f77d` | 11-20 7:21 PM | drain, item | the `tillr-view` suite | `confirmed`, 1327361 | reverted by `16edac08c0` after 5 minutes |
| `315a213fe4` | 11-20 7:31 PM | drain, item (the same item, landed again) | the `tillr-drain` suite | `confirmed`, 1327943 | reverted by `f23a3087c5` after 24 minutes; a fixer filed for the same check landed nothing, and the check was proved green at `a75095d06b` (1328345) |
| `a0b415fc3d` | 11-21 5:16 AM | drain, item | `TestObstacleHoldsShowRealReasonAndMoverInWaitingAndAgreeWithPlan` in `tillr-view` | `confirmed`, 1338324 | reverted by `9702855aee` after 26 minutes |
| `9298e19241` | 11-21 6:11 AM | drain, item | `TestEveryFoldReadsTheKindsItSelects` in the drain's `drain` package | `confirmed`, 1340326 | reverted by `1d83f5a28f` after 43 minutes; a fixer filed before the revert was still pending at the reading |
| `cc31e10d8b` | 11-21 9:54 AM | drain | `change-checks-test` | `offered`, 1341858 | revert not possible: the trunk advanced while the revert was being checked. No fixer landing and no green proof is joined to it in the period |
| `2838315c85` | 11-21 10:47 AM | `tillr`, drain, item, store, view | the backend adapters check | `offered`, 1343156 and 1343231 | revert not possible, twice: the fast gate failed with the revert, on this same check. Repaired by the fixer landing `4df97785cb` (1343266) |
| `dcd11d187a` | 11-21 11:20 AM | `tillr`, drain | seven subtests of `TestLoopSettingsRefusedBeforeStartingOrChecking` in the drain's main package | `confirmed`, 1344906 | reverted by `11f85efd77` after 14 minutes |
| `9434198857` | 11-21 11:41 AM | drain | the roll and keeper suites | `confirmed`, 1345188 | reverted by `e8a556e75d` after 10 minutes |
| `5514f6eda3` | 11-21 12:35 PM | drain, item, run | `TestFinalReportFromCommitOffer/commit` in `tillr-run` | `confirmed`, 1346827 | reverted by `9bad3b8700` after 8 minutes |
| `a427c165a3` | 11-21 3:34 PM | `tillr`, drain, item, run, view | `TestBlocksFromUsesDeclarationParser` in `tillr-item` | `confirmed`, 1351147 | reverted by `b96dbc1a99` after 6 minutes |

So the loop reverted 11 of the 13 itself, between 5 and 43 minutes after they landed (median 14).
One was repaired by a fixer landing. One, `cc31e10d8b`, has no recorded repair in the period. The
13 landings carry 19 associations, because the seven subtests of `dcd11d187a` are seven.

**What the fast gate ran for each.** On 2026-11-21 each landing's bound verdict was read from the
live store, opened read-only, and matched on verdict ID, base and tree. The gate program then was
[`tools/gate/change-gate.py`](../../tools/gate/change-gate.py), and its log states the plan in two
lines, `fork-check: selection` and `fork-check: plan`, with one `fork-check: ok` line per check
that ran. Each failing check is in one of three states in that plan: **not selected** (the plan
says why), **selected and passed** (an `ok` line), or **unknown**. None of the 19 is unknown.
The audit took its 13 landings from a reading of the same report at event 1359581; the reading
at event 1361746 lists the same 50 landings with the same checks and attributions.

| Landing | Verdict and landing events | Checks the fast gate ran, and its wall time | Suite or check that holds the failure | In the bound plan |
| :--- | :--- | :--- | :--- | :--- |
| `9628978cfc` | 1318790, 1318792 | `file-size`, `tillr-docs`; 81 s | the `tillr-view` suite | not selected: deferred to the heavy run |
| `9cadb24d06` | 1318842, 1318844 | `file-size`, `tillr-backends`, `tillr-docs`; 90 s | [`tests/go-test-shards.test.py`](../../tests/go-test-shards.test.py) | not selected: a heavy-only check |
| `129509992c` | 1319599, 1319601 | `file-size`, `observatory-web`, `tillr-docs`; 148 s | the `tillr-drain` suite | not selected: deferred to the heavy run |
| `68fb25f77d` | 1327168, 1327174 | `file-size`, `tillr-docs`; 51 s | the `tillr-view` suite | not selected: deferred to the heavy run |
| `315a213fe4` | 1327542, 1327569 | `file-size`, `tillr-docs`; 70 s | the `tillr-drain` suite | not selected: deferred to the heavy run |
| `a0b415fc3d` | 1337988, 1337991 | `file-size`, `tillr-docs`; 57 s | the `tillr-view` suite | not selected: deferred to the heavy run |
| `9298e19241` | 1339242, 1339244 | `file-size`, `tillr-docs`; 65 s | the `tillr-drain` suite | not selected: deferred to the heavy run |
| `cc31e10d8b` | 1341435, 1341443 | `file-size`, `gate-runner`, `pipeline-evidence`, `self-suite`, `tillr-docs`, `tillr-loop`; 275 s | [`tools/gate/checks/change_checks_test.py`](../../tools/gate/checks/change_checks_test.py) | not selected: a heavy-only check |
| `2838315c85` | 1343001, 1343003 | `file-size`, `gate-runner`, `go-substrate`, `self-suite`, `tillr-backends`, `tillr-docs`; 259 s | [`tests/tillr-muse-backend.test.py`](../../tests/tillr-muse-backend.test.py), the `tillr-backends` check | **selected and passed**, in 5.7 s; `go-substrate`, the `tools/tillr` suite, also passed, in 3.9 s |
| `dcd11d187a` | 1344126, 1344129 | `file-size`, `gate-runner`, `go-substrate`, `tillr-docs`, `tillr-roll`; 75 s | the `tillr-drain` suite | not selected: deferred to the heavy run |
| `9434198857` | 1345023, 1345027 | `file-size`, `tillr-docs`; 77 s | [`tests/tillr-roll.test.py`](../../tests/tillr-roll.test.py) and [`tests/tillr-keep.test.py`](../../tests/tillr-keep.test.py), the `tillr-roll` check | not selected: "no changed path is read" |
| `5514f6eda3` | 1346525, 1346530 | `file-size`, `tillr-backends`, `tillr-docs`; 83 s | the `tillr-run` suite | not selected: deferred to the heavy run |
| `a427c165a3` | 1351010, 1351012 | `file-size`, `gate-runner`, `go-substrate`, `tillr-backends`, `tillr-docs`; 87 s | the `tillr-item` suite | not selected: deferred to the heavy run |

The plan's words for a deferred suite are `owed: true, planned: false`, with the reason "the
landing tier defers it to the rolling tier". Twelve of the 13 are therefore a check the fast gate
did not run, and they are three kinds, not one:

- **Nine are a Go module's whole suite, deferred by rule.** Drain four times, view three times,
  run once, item once. The fast gate did run Go tests when its selection named them: the shared
  module's suite ran and passed on three of these candidates.
- **Two are checks that exist only in the heavy run** (`go-test-shards-test` and
  `change-checks-test`).
- **One is a fast-tier check that selection passed over.** `tillr-roll` ran on `dcd11d187a`
  and was skipped on `9434198857`, whose six changed source paths were all in the drain, because
  none of them is a path that check declares it reads. It then failed in the heavy run and passed
  with the landing reverted.

The thirteenth, `2838315c85`, is not a miss by the gate that judged it. Its fast gate ran the
check that later failed, and the check passed. The heavy failure's own output is `FAIL:
tools/tillr-drain/drain/push_state.go names the model id 'opus'`. That file is
[`tools/tillr-drain/drain/push_state.go`](../../tools/tillr-drain/drain/push_state.go), the candidate
did not change it, and the fixer's repair `4df97785cb` is one changed string in it. `git log -S`
on that string, run on 2026-11-21, shows that `1c646fded4` added it. That commit landed at
10:40 AM (events 1342762 and 1342764), after the base `2838315c85` was judged on and before it
landed, so the tree the fast gate judged did not hold the string and the tree that landed did.
The fast gate that judged `1c646fded4` lists `tillr-backends` as not run: that candidate changed
only the drain, and the check's declared paths do not include the drain's sources, which its scan
reads. So the red was a selection miss on a landing the report does not list, because the heavy
failure named a later commit. The report cannot see this case, and its count of 13 is neither an
upper nor a lower bound on what the fast gate let through.

**Attribution is not tested cause, and the judged tree is not the landed tree.** Three limits on
reading the tables as "the fast gate would have caught this":

- **`confirmed` is a test on the trunk, not on the candidate.** It shows the check passed on the
  tip once the landed commit was reverted. It does not show the check failing on the tree the
  fast gate judged.
- **For all 13, the judged tree and the landed tree differ.** The trunk moved between the
  candidate's base and its landing, and the lander replayed the change onto the new tip
  ([`landing-principles.md`](landing-principles.md)
  [§2](landing-principles.md#2-re-application-preserves-the-change-not-the-patch)).
  A suite added to the fast gate runs on the judged tree. Where the red arrives with a
  concurrent landing, as it did for `2838315c85`, or from the change meeting one, that suite
  passes and the red lands anyway.
- **No replay of a judged candidate exists.** The one replay on record ran the landed commit
  `9628978cfc` against its parent, twice, on 2026-11-21 in a private clone: the view suite failed
  both times, in `TestStatusPrintsGreenRangeAttributionUnavailableAndOverruns`. That shows the
  landed tree was red in the view suite. In one of the two runs the drain suite also failed, in
  three tests the attribution does not name, and in the other it passed. Those failures were
  intermittent on one landed tree, and their cause was not looked for. The run that passed does
  not show that the landed change was unrelated to them, and the run that failed does not show
  that it caused them.

So the counts below are of landings whose failing suite an option **would have run**. They are an
upper bound on refusals. The number of the 13 proved to be refused before the merge is zero.

**The options compared.** Each row adds existing whole-suite checks to the fast gate, selected by
the candidate's changed paths. "Changed plus requirers" is the changed tillr Go modules and each
module whose `go.mod` requires one, read from the `go.mod` files at each candidate's head.

| Selection | Runs when | Suites added | Of 13 landings, failing suite would have run | Of 19 associations |
| :--- | :--- | :--- | ---: | ---: |
| No change | never | none | 0 | 0 |
| Drain suite alone | the candidate changes `tools/tillr-drain` | drain | 4 | 10 |
| View suite alone | the candidate changes `tools/tillr-drain` | view | 3 | 3 |
| Changed modules only | the candidate changes a tillr Go module | each changed module's | 6 | 12 |
| **Drain and view pair** | the candidate changes `tools/tillr-drain` | drain and view | **7** | 13 |
| Changed plus requirers | the candidate changes a tillr Go module | four to nine suites on these 13 | 9 | 15 |

None of them selects the four that are not a Go suite: the two heavy-only checks, the roll and
keeper suites, and the backend adapters check.

**Why the pair is the smallest that catches most.** Seven is the least that is more than half of
13. No single existing check reaches it: the drain suite holds four of the failures and nothing
else holds more than three. Of every two existing checks, only drain with view reaches seven, and
dropping either leaves four or three. "Changed modules only" reaches six, because three of the
failures were in the view suite on candidates that did not change the view. "Changed plus
requirers" reaches nine by adding the run and item suites, and it selects all nine tillr modules'
suites whenever the shared module changes, as three of these 13 candidates did.

The trigger is the whole `tools/tillr-drain` directory, and it is not narrowed. All 13 candidates
changed paths in it, so the period's evidence cannot tell a narrower trigger from this one; any
subdirectory or file-type rule drawn from seven failures would be fitted to those seven. What
justifies the view suite on a drain change is structure, not the count: `tools/tillr-view`'s
`go.mod` requires the drain. The pair is not triggered by a change to the view alone, because no
attributed landing in the period was one.

**What the pair costs, measured.** Two paired timings on 2026-11-21, 9:38 to 9:50 PM Eastern, on
the trunk commit `81ea2675de` against its parent `5514f6eda3`: a green change to six drain source
files and their pack copies. The fixture was a private clone with its own gate runner, check
memory and logs, warmed once, on a 32-core machine with a one-minute load average between 14 and
22 from the lanes then running. The fast gate was that commit's own `tools/gate/change-gate.py`.
The second arm then ran both suites at once from the ordinary test-slot pool and waited for both:

```sh
python3 tools/go-test-shards.py tools/tillr-drain -k 6
python3 tools/go-test-shards.py tools/tillr-view -k 6
```

| Pair | Fast gate alone | Fast gate, then the two suites | Added | The fast gate's part of the second arm | Drain suite | View suite |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 41.17 s | 276.23 s | **235.06 s** | 48.16 s | 228.01 s | 22.37 s |
| 2 | 46.97 s | 365.53 s | **318.56 s** | 52.52 s | 312.97 s | 42.09 s |

Every arm passed. The two suites ran at once, and the drain suite was the longer of the two both
times: the view suite finished inside it. The added time is not the drain suite's time alone. The
fast gate itself took 6.990 and 5.556 seconds longer in the second arm than in the first arm of
its pair, and that is part of each difference.

These are two green measurements of one historical candidate. They do not show any other
candidate, a quiet machine, a cold one, or a candidate whose suite fails. A candidate that does
not change the drain adds no suite, so its added time is zero by rule; that was not timed as a
pair.

"Changed plus requirers" was timed the same way earlier that day on the same commit, where it
selects four suites (drain, land, store, view): 277.71 and 308.62 seconds added to a fast gate of 41.50
and 40.15 seconds, both green. Those two runs used a different private fixture under different
load, so they are not a comparison with the pair's two: which option adds more wall time on this
candidate, and by how much, is undetermined. Its cost on a candidate that changes the shared
module, where it runs nine suites, was not measured either.

**How often the trigger fires.** Of the 472 landings in the period, 245 were judged on a candidate
that changed a path under `tools/tillr-drain`: 52 percent (`git diff --name-only` between the
base and head each landing record carries, run on 2026-11-21; the landed commits give the same
245). That count is how often the trigger would have fired. It is not a measured delay: the two
timings above are of one candidate, and what the pair would have added to any of the other
gates was not measured.

**Proposal for the owner (the designer's; not a ruling, and not built).** When a candidate changes
a path under `tools/tillr-drain`, the landing gate also runs, before the merge, the sharded
`tools/tillr-drain` and `tools/tillr-view` suites, at once, after the fast checks pass. Every
other candidate is gated exactly as today. It is the same shape as the exception this design
already makes for the gate's own runner ([`OQ-GR3`](#OQ-GR3)). What is known about it:

- It would have run the failing suite for 7 of the period's 13 attributed landings. None of the
  seven is proved refused, for the reasons above.
- It added 235.06 and 318.56 seconds to a fast gate of 41.17 and 46.97 seconds, in two green
  runs of one candidate (`81ea2675de` against `5514f6eda3`), beside running lanes. No other
  candidate was timed. The gate targets ten seconds ([`D-GR1`](#decision-ledger)).
- Its trigger matched 245 of the period's 472 landings, 52 percent. That is how often it would
  have run. What it would have added to those gates is not measured.
- In one of two replays of a landed tree the drain suite failed in three tests the attribution
  does not name, and in the other it passed. The cause of that intermittent failure is not
  established, so whether a suite run before the merge would refuse candidates for a failure
  like it is not known.
- Whether the heavy runner could reuse a suite pass from before the merge, and so run less after
  it, was not measured. The judged tree is not the landed tree, so a pass on one is not a pass on
  the other.

**Question for the owner:** should a candidate that changes `tools/tillr-drain` run the drain and
view suites before it lands?

**Options.**

1. **Yes, the pair, as proposed.** Seven of the period's 13 attributed landings would have had
   their failing suite run before the merge. The trigger matched 52 percent of the
   period's landings. The one candidate timed waited 235 and 319 seconds longer; the added time
   on any other candidate is not measured. `D-GR1` and `D-GR4` then hold only for candidates that
   leave the drain alone.
2. **Yes, and wider: the changed modules and the modules that require them.** Nine of 13. Whether
   it adds more or less time than the pair on a drain change is undetermined, because its two
   timings used another fixture and load. It runs up to nine suites when the shared module
   changes, at a cost not measured.
3. **No; keep the fast tier as it is.** About 3 percent of landings were attributed a heavy
   failure, and the loop reverted 11 of the 13 in a median of 14 minutes. The work goes into what
   makes that slow or leaves a red trunk: the proof's running time, and the two that could not be
   reverted.
4. **Not yet; a faster and steadier drain suite first.** The same suite took 40.31 seconds on a
   quiet machine on 2026-11-17 ([shard report](../pi/tillr-go-shards-2026-11-17.md)) and 228 to
   313 seconds in the two timings here, and it failed intermittently in one replay for a cause
   not established. The proposal is measured again when the suite's time beside running lanes is
   near the quiet one and that failure is explained.

Separately from these four, two of the misses found here are a fast-tier check whose declared
paths are narrower than what its test reads: `tillr-roll` on `9434198857`, and `tillr-backends`
on `1c646fded4`. Widening those declarations adds no suite. It is a selection change, so it is
not made or measured here, and it is the owner's to ask for.

The designer's leaning is option 3 with option 4 as the follow-up, on three facts that are kept
apart here because they were measured apart. The one candidate timed waited 235 and 319 seconds
longer, against a ten-second target. The trigger matched about half of the period's landings,
and the added time on those was not measured. The benefit is an upper bound of seven landings
in two days, each of which the loop reverted without a person. The owner's leaning quoted above
is toward better fast tests, so this is the designer's reading of the numbers and the owner's to
overturn; the choice among the four options is the owner's.

**Ruled on 2026-11-21.** The owner answered the question: Go tests run before the merge, about
40 seconds is accepted for the tests a drain change selects, and the ten seconds was
aspirational ([`D-GR38`](#decision-ledger), quoted in
[Section 6.12](#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands)).
That overturns the designer's leaning above. What is built is not option 1 as written:
an operator request asks for the affected test packages with Go's result cache on, not whole
sharded suites, and Section 6.12 specifies it (`D-GR39`, the designer's). The proposal, the
options and the leaning above are kept as the record of what the owner was asked.

### 6.11 The fast gate before a landing records its time and its Go packages

This section is the designer's, written on 2026-11-21 from an operator request and to answer a
build worker's report against it. None of it is the owner's ruling, and the owner may overturn
any of it. The request says the owner accepted about 40 seconds for the fast gate on drain
changes on condition of "perf monitoring all over it"; those quoted words are the request's
account of the owner, and no dated owner statement is on file here. A later request of the
same day carries the owner's words verbatim; they are quoted in
[Section 6.12](#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands)
and recorded as [`D-GR38`](#decision-ledger).

**What is recorded.** Each run of the fast gate that judges a candidate before it lands, on its
ordinary path, leaves one durable record holding:

- the wall time of the whole gate run, measured once around the run, not summed from its checks
  and not including time spent waiting to start;
- for each check, its elapsed time in this run and whether it ran, was reused from a remembered
  pass, or was not run;
- for each Go check on that path, the Go packages it selected, by import path.

A run that does not pass leaves the same record, and a later attempt does not replace an
earlier one's. Nothing here changes which checks run, how a result is classified, a budget, the
stall rule or what lands (`D-GR1`, `D-GR7`, `D-GR8`, `D-GR15`, `D-GR16`).

**Scope, from the request.** Only that ordinary path is covered:
`ProcessSteps.Gate` in the drain, then `tools/gate/change-gate.py`, which runs
`tillr-gate checks --fast-tree` and `tools/fork-check.py --tier landing`. Six paths are left
unmeasured by this work, each to be its own request: the heavy trunk proof, the legacy
tree-check bridge, a relanding candidate, the automatic revert candidate, named replays, and
counting fast-passed landings later named by a heavy failure. The candidate's commit message
lists them. An earlier request asked for timing on every gate run and was closed after eight
verifier refusals, each on a different path; its last candidate is kept at
`refs/keep/fast-gate-timing`, and this work takes from it only the hunks the ordinary path
needs.

**Three Go checks are on that path, and each has its own source of packages.** Read at
`86e2549392`:

| Check | When it runs | Command | Where its packages come from |
| :--- | :--- | :--- | :--- |
| `go-build` | every fast gate, as a fast tree check | `tools/gate/checks/go-build.py` | the `go list` the check already runs, written to the file named by `TILLR_GO_BUILD_EVIDENCE_OUT`; a module reused from memory lists no packages |
| `go-substrate` | a suite input of `tools/tillr` changed | `go test ./...` in `tools/tillr` | the package lines `go test` prints, read from the check's own output |
| `gate-runner` | the gate runner or its suite inputs changed | `tools/go-test-shards.py tools/tillr-gate -k 6 --slot-class proof` | the shard runner's own package list, as decided below |

Research on choosing the packages and tests a change affects, added by the designer as a
pointer and deciding nothing here:
[Build tools and affected-test selection for tillr](../research/build-tools.md).

**The report this section answers.** The build worker given the Go package piece could write
`tools/gate/checks/go-build.py`, `tools/fork-check.py` and their tests, and stopped on
`gate-runner`. Its evidence holds at `86e2549392`: `go_tool_command` in `tools/fork-check.py`
gives `tools/tillr-gate` the sharded command, because that directory is in `GO_SHARD_DIRS`; the
shard runner prints no `go test` package lines; `run` in `tools/go-test-shards.py` keeps its
`go list -json ./...` result to itself; and the `--events` file holds only test events, so it
omits a package with no tests, and `main` writes it only after `run` returns, so a failed
compile leaves none. No reading of the check's output can therefore name `gate-runner`'s
packages.

**Decision (`D-GR30`): the shard runner writes its package list when asked.** `run` in
`tools/go-test-shards.py` writes the packages its `go list -json ./...` returned to the file
named by the environment variable `TILLR_GO_SHARDS_EVIDENCE_OUT`. The rules:

- With the variable unset the runner behaves exactly as it does today. It gains no command-line
  flag and prints nothing new.
- The file is written before any package is compiled, so a compile failure or a failed shard
  still leaves the full list.
- When `go list` itself fails, the file holds the complete package records that preceded the
  failure, marked `"discovery": "partial"`, and the runner then fails as it does today without
  building anything.
- Each entry names the package by `import_path`. The kept candidate's `test_cache` value may
  stay as it is written there; nothing further about caches or compilers is added.
- A failure to write the file is printed to standard error and does not change the runner's
  exit code.
- `tools/fork-check.py` sets the variable to a fresh temporary file only for a landing-tier
  check whose module is in `GO_SHARD_DIRS`, which on the ordinary path is `gate-runner` alone,
  reads the file after the command ends, puts the packages on that check's record and removes
  the file. The heavy proof, the rolling `go-tool` suites, lanes and named replays do not set it.

That file is a contract between two utilities, the shard runner that writes it and
`tools/fork-check.py` that reads it. Research on defining and checking such contracts, added by
the designer as a pointer and deciding nothing here:
[Contracts between command-line utilities](../research/utility-interface-contracts.md).

The producer and its two fixture tests exist in the kept candidate
(`git diff 86e2549392...refs/keep/fast-gate-timing -- tools/go-test-shards.py tests/go-test-shards.test.py`,
65 added lines); the tests replace `go list` and the compile step with fixtures and run no
suite. So the Go package piece's write scope gains `tools/go-test-shards.py` and
`tests/go-test-shards.test.py`, for that producer and those tests only. The shard runner's
slot pool, shard lifetime, timeouts and event output are not this work's to change (`D-GR17`,
`D-GR20`). `tools/go-test-shards.py` is a suite input of every sharded module
(`GO_SHARD_INPUTS`), so the candidate's own fast gate runs `gate-runner`; that is the
runner-first rule of [`OQ-GR3`](#OQ-GR3) working as intended.

| Alternative | Verdict |
| :--- | :--- |
| Leave `gate-runner` out of this work and give its packages to a later request | Rejected. The request's title names selected Go packages for the fast gate, `gate-runner` is on its ordinary path, and it runs exactly when the gate's own runner changed, the landings where a missing record costs most. |
| Read the packages from the `--events` file | Rejected. It omits packages without tests and is absent after a failed compile, as the report shows. |
| Have `tools/fork-check.py` run its own `go list` in `tools/tillr-gate` | Rejected. It is a second discovery that can differ from what the runner selected, and it adds a `go list` to a gate that targets ten seconds. |
| Run `tools/tillr-gate` unsharded at landing so `go test` prints package lines | Rejected. It changes how a check runs, which this work may not do. |

### 6.12 A change to a tillr Go module runs its affected test packages before it lands

**The owner's ruling, 2026-11-21, verbatim** (`D-GR38`): *"if you think drain suite selected
tests are 40 seconds, let's do that, and monitor how it goes, just make sure we have perf
monitoring all over it. 10 seconds was apsirational"*. The words reached this document in an
operator request of 2026-11-21, which states them as verbatim. They answer the question
[Section 6.10](#610-what-passed-the-fast-gate-and-failed-the-heavy-run) put to the owner: Go
tests run before the merge.

Everything else in this section is the designer's, written on 2026-11-22 from that operator
request and to answer a build worker's report against it (`D-GR39`). None of it is the owner's
ruling, and the owner may overturn any of it. The worker's report was right: the ledger still
said ten seconds without qualification, kept the 9,000 millisecond cap without saying what the
cap covers, and held `D-GR27` open in a form the request does not build.

**What the ruling changes, and what it leaves.** Three statements, the first two read from the
owner's words and the third the designer's reading of them:

- Ten seconds is an aspiration, not a rule a design must hold to. `D-GR1` is amended to say so.
- A candidate that changes the drain may take about 40 seconds in the gate for its selected
  tests. That figure is an estimate: it is the drain suite's quiet-machine time in the gate's
  cost table (`GO_TOOL_COST_ESTIMATE` in `tools/fork-check.py`, 40.31 seconds, from
  [the shard report](../pi/tillr-go-shards-2026-11-17.md)). Nobody has measured the selected
  packages.
- The acceptance depends on monitoring. So the measurement is part of this work and not a
  follow-up: the check may not land without the record described under
  [What is recorded](#what-the-check-records).

No time limit fails a gate. That is `D-GR15`, unchanged: the 40 seconds is not a budget that
stops, carries or refuses anything, and the stall watchdog of `D-GR16` is the only thing that
ends the check early.

**What `D-GR27` proposed and what is built instead.** `D-GR27` proposed two whole sharded suites,
run fresh, the drain's and the view's, on every change to the drain. The request builds a
narrower thing: the test packages a change can affect, with Go's result cache left on. `D-GR27`
is closed by the ruling and is not built in its own form. The designer's leaning there
(no test before the merge) is overturned by the owner.

#### The check

An **affected test package** *(the research's term,
[defined there](../research/build-tools.md))* is a Go test package selected because a changed
input can reach it. The check that runs them is named `go-affected` here *(the name is the
designer's)*.

`go-affected` is a landing-tier kind in `tools/fork-check.py`, planned on the ordinary path the
fast gate takes before a landing (`tools/gate/change-gate.py`, then
`tools/fork-check.py --tier landing`). That is where `go-substrate` and `gate-runner` already
run, and it is the path the timing record of
[Section 6.11](#611-the-fast-gate-before-a-landing-records-its-time-and-its-go-packages)
covers. It is planned only when the candidate changes a path under one of the modules below or
a path a declared edge names. Every other candidate is gated exactly as today.

**The modules** are `tools/tillr` and every `tools/tillr-*` directory that holds a `go.mod`,
found by listing the tree, not from a kept list. Read at `5f3c021bec` that is ten modules:
`tillr`, `tillr-cli`, `tillr-drain`, `tillr-gate`, `tillr-item`, `tillr-land`, `tillr-run`,
`tillr-store`, `tillr-view` and `tillr-watch`. The request excludes the `yolo-packs` mirrors,
`tools/dotmod` and `tools/bedrock-spend`. `tools/pi-profiler` is not a tillr module and is not
included. `tillr-watch` is included although the `go-build` check's inventory does not list
it, because the request says every tillr module.

**Selection** takes the changed paths between the caller's base and the head (`D-GR7`) and
adds packages in four steps. Each selected package keeps the reason it was selected.

1. **Changed.** A changed path under a package's directory selects that package. A changed
   path in a directory that holds no package (a `testdata` tree, for instance) belongs to the
   nearest enclosing package.
2. **Importers.** The check reads each module's packages, imports and test imports with
   `go list -deps -test -json ./...` and reverses the graph across all the modules. Every test
   package whose imports reach a changed package, directly or through other packages, is
   selected. The research measured this listing once, on 2026-11-21: 0.86 seconds for ten
   modules, 112 packages and 29 test binaries.
3. **Declared edges.** A test that builds or runs a sibling program does not import it, so the
   import graph cannot see the dependency. The research measured exactly this: a change to
   `tillr-item/item` did not select `tillr-view`, whose tests build the drain. One tracked
   file, `tools/gate/go-test-edges.json` *(the name and the two field names are the
   designer's)*, lists for a test package the sibling main packages it builds or runs
   (`builds`) and the files outside Go's graph that it reads (`reads`, as paths or directory
   prefixes). A package with a `builds` edge is selected when the built program's own package
   or anything that program imports changed. A package with a `reads` edge is selected when a
   changed path matches.
4. **The floor.** The modules' `replace` directives are today's selection
   (`go_module_dependencies` in `tools/fork-check.py`) and stay as the answer whenever the
   package graph cannot place a change. A changed path under a module that belongs to no
   package (`go.mod`, `go.sum`, a file beside them), a changed path whose package no longer
   exists at the head, and a module whose `go list` fails each select every test package of
   that module and of every module that replaces it. Nothing is left unselected because the
   graph could not be read.

The floor at module grain for unplaceable changes is the designer's reading of the request's
"keeps the existing replace-directive module edges as a floor". The other reading, that any
change to a module selects the whole suite of every module replacing it, would run four whole
suites on every drain change and contradicts the request's own sentence that a view run
follows from a change to packages "that tillr-view depends on". The owner may overturn the
reading.

Packages of `tools/tillr` are dropped from the selection when `go-substrate` is planned in the
same gate, and packages of `tools/tillr-gate` when `gate-runner` is, because those two checks
already run the whole suite. Neither check changes.

**Sibling builds measured.** Read on 2026-11-22 at `5f3c021bec` with
`rg '"go", *"(build|run)"|go build' tools/tillr*/ -g '*_test.go'`, keeping the calls that name
a directory or package outside the test's own:

| Test package | Sibling it builds | Where |
| :--- | :--- | :--- |
| `tools/tillr-view` | `tillr-drain` | `buildPairInto` in `planner_internal_test.go` |
| `tools/tillr-view` | `tillr-item` | `order_places_test.go`, `sync_residue_test.go` |
| `tools/tillr-land` | `tillr-item` | `lifecycle_test.go`, `contention_test.go` |
| `tools/tillr-land` | `tillr-gate` | `fork_rebase_test.go` |
| `tools/tillr-drain` (the root and `drain` packages) | the seven programs in `internal/testinputs/siblings.txt` | `internal/testinputs/inputs.go` |

That list is a dated measurement and the start of the edges file, not its definition. The
builder runs the search again, and also looks for tests that run a program by another route
(a Python child, a built binary found on `PATH`). The file is kept honest by a test, not by
memory: a test in the gate's suite scans the modules' `_test.go` files for a `go build` or
`go run` that names a path outside the test's own module, and fails when the edges file has no
entry for it.

**Non-Go inputs.** The builder lists them in `reads` from the tests themselves. Two kinds are
already known: a fixture file a test opens outside its package directory, and a script a test
runs (`tools/tillr-drain/drain/shard_balance_test.go` opens `tools/go-test-shards.py` for this
reason). A file under the test package's own directory needs no entry, because step 1 covers
it.

#### How the selected packages run

For each module with selected packages the check runs `go test` on exactly those packages, with
`CGO_ENABLED=0`, Go's own deadline off (`-timeout 0`, as the gate's native suites already run)
and no `-count=1`. A package Go answers from its result cache counts as passed.

**A cached pass is trusted only where Go's cache can see the input that changed.** For steps 1
and 2 it always can: the changed code is compiled into the test binary, and the binary is the
cache's key. For a declared edge it cannot by default, since a child process's file reads are
not in the test's input log (stated in `tools/tillr-drain/internal/testinputs/inputs.go`). The
drain's tests solve this by opening the sibling sources through `testinputs.Track`, and
`tools/tillr-drain/cache_test.py` proves a sibling change reruns them. So:

- A package selected only through a declared edge, in a module whose tests do not declare
  those inputs to the cache the way the drain's do, is run with `-count=1` for that run.
- This is the one exception to the request's "no -count=1", and it is the designer's. Without
  it a drain change would be answered by a cached view pass from before the change, which is
  three of the thirteen misses this work exists to stop. It ends for a module when that
  module's tests declare their inputs, which is a later change and not this one.

Whether a pass cached in one lane's worktree is reused in another's is not known; the research
marks it unverified. The check's verdict never depends on a cache hit, so this affects only
time, and the record below is what measures it.

The check's test processes count against the machine-wide test slot pool at the gate's class,
so a gate does not add unbounded parallelism beside the lanes (`D-GR20`). Time spent waiting
for a slot is recorded apart from the check's running time.

The heavy tier is unchanged: after the merge it still runs each whole suite fresh. Whether the
heavy runner could reuse a pass from this check is not decided here.

#### What the check records

The timing record of `D-GR30` (one line per ordinary fast gate run, built in `5f3c021bec`)
gains, for `go-affected`:

- the time spent computing the selection, apart from the time spent running tests;
- the time spent waiting for a test slot;
- each selected package by import path, with the reason it was selected (`changed`, `import`,
  `edge` or `floor`), its elapsed time, whether Go answered it from the cache, and whether it
  was run with `-count=1` under the exception above.

A run that fails leaves the same record. This is the monitoring the owner's acceptance depends
on, and it is enough to answer the three questions that decide whether the ruling holds up:
how long a drain change's gate takes, how much of it the cache saves, and which packages the
time goes to.

**What the records hold today.** Read on 2026-11-22 from the eight timing records then in the
live store (one per ordinary fast gate run since `5f3c021bec` landed): the whole gate's wall
time ran from 12.5 to 123.3 seconds, with a median of 77.1 seconds, and the one run in which
`go-build` executed took 5.96 seconds for that check. Eight runs is a small sample, and it
does not say which checks the time went to in the other seven. It does say the gate was
already well past ten seconds before any Go test ran in it. The builder reports the measured
times again from the records at the time of the build, in the commit body, together with the
time of each run of the new check it made, and states plainly that the 40 seconds was an
estimate.

#### How it fits admission

The `gate-budget` check sums the `budget_ms` of the fast declarations under
`tools/gate/checks/` against the 9,000 millisecond cap. Those are the checks every candidate
runs. `go-affected` is not one of them: it is a landing-tier kind planned only for a candidate
that changes a tillr Go module, the same standing `go-substrate` and `gate-runner` have. So:

- The cap and its arithmetic test are unchanged, and no fast declaration's budget moves.
- The cap now has a stated scope: it bounds the declared cost of the checks every landing
  pays. It is not a bound on a landing that changes a tillr Go module.
- `go-affected` gets a row in the gate's cost table for scheduling, like every landing-tier
  kind. The row is an estimate and stops nothing.

#### What it would have caught

Of the thirteen attributed landings in Section 6.10, nine failed in a Go module's suite that
the fast gate deferred: the drain's four times, the view's three times, the run's once and the
item's once. Section 6.10's limits apply here too: the judged tree is not the landed tree, so
nine is an upper bound and none is proved refused. The selection was not replayed against
those nine landings, so whether it picks the failing package in each is unverified; the builder's tests cover the two shapes the request names (a drain change runs
the affected drain packages, and a change to a drain or item package the view depends on runs
the view suite). The four Python suites are not reached by this work.

**What a verifier of the request's landing judges.** The check exists on the ordinary path and
is planned only for the modules above. A drain change selects the affected drain packages. A
change to a drain or item package that the view imports, or that a program the view builds
imports, selects the view's test packages. An unplaceable change falls to the floor. The edges
file exists with the scan test beside it. The record holds the fields above. No budget, cap or
time limit is added or changed, and the commit body reports measured times. A verifier does not
refuse the landing because a run took longer than 40 seconds.

| Alternative | Verdict |
| :--- | :--- |
| The whole sharded drain and view suites on every drain change, run fresh (`D-GR27` as proposed), or the suites of the changed modules and the modules that require them (option 2 of Section 6.10) | Rejected by the request. Each pays the whole drain suite on every drain change and reuses nothing between runs; Section 6.10 measured 235 to 319 seconds added for the pair. |
| A fast declaration under `tools/gate/checks/` with a 40,000 millisecond budget, and the cap raised to fit | Rejected. The cap would then admit 40 seconds of always-run checks, and the arithmetic test would stop protecting the landings that change no Go. |
| Select only the tests a change adds or edits | Rejected on the evidence of Section 6.10: where a record names the failing test, the landing never edited it. |
| The import graph alone, with no declared edges | Rejected. It does not select the view for an item change (measured in the research), which is three of the thirteen misses. |
| The cache on for every package, declared edges included | Rejected. A child build is invisible to Go's cache, so the pass would be one from before the change. |
| `-count=1` for every package | Rejected by the request. It gives up the reuse the owner's 40 seconds assumes. |
| A build system (Bazel, Pants) or per-test coverage maps as the selector | Rejected for now, on the [research](../research/build-tools.md)'s recommendation: the cost is high and each still needs declared subprocess edges. |

## 7. Checked against the landing principles

| Principle | How this design meets it |
| :--- | :--- |
| [1, a refusal must be a verdict](landing-principles.md#1-a-refusal-must-be-a-verdict) | A change is refused only by a check that ran on it. Unavailable, over-budget and timeout are named and are not refusals; under [`D-GR15`](#decision-ledger) a check that is making progress is never timed out, and a stall is named. |
| [3, gate what will be the tip, once](landing-principles.md#3-gate-what-will-be-the-tip-once) | The fast gate runs once, on the candidate. A moved trunk is squashed onto, not re-gated. |
| [6, a budget escalates, it does not loop](landing-principles.md#6-a-budget-escalates-it-does-not-loop) | An overrun is recorded as `over_budget_ms` while the check keeps its verdict (nothing is carried at a budget under [`D-GR15`](#decision-ledger)). A stall re-gates the candidate once when fewer gates are in flight, and a second stall on the same head tree holds the item as `gate-stalled` ([`D-GR16`](#decision-ledger)); nothing loops. |
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
| The heavy runner's `git worktree add` is interrupted and leaves its registration locked | The next run unlocks the registration of its own path, reports the lock's reason and time, and creates the worktree ([the rule](#the-proof-worktrees-registration-is-left-locked), `D-GR35`) |
| A result file is unreadable | Treated as absent, and counted in the report |
| The result store is deleted | Every tree check runs once more; no verdict changes |
| A declaration is malformed | The run is not a verdict and names the file and field; no other check runs under a partial set |
| The existing record, its baselines and its run history | Left in place, read by nothing in the new path, deleted with the last of the gate program |
| The rolling watermark row in the store | Ignored once the pointer exists; the first heavy run is a whole-tree run |

## 9. What this costs, and what it does not do

- **The trunk may be red** from a landing until its heavy run ends and either automatic revert
  (`auto_revert` is `true` here since 2026-11-18; [`D-GR6`](#decision-ledger)) or a fixer repairs
  it — never past the red cap of
  [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) while that cap
  remains live.
- **Fewer checks stand before the merge** than today. A Go suite, the fork suites and the
  storybooks are all after it. Once
  [Section 6.12](#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands)
  is built, the Go test packages a change affects run before the merge, and the whole suites
  still run after it.
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
| Enforce the budget by timing every landing and failing slow ones | Rejected. A slow box would refuse correct changes; the arithmetic test cannot flake, and under [`D-GR15`](#decision-ledger) an overrun is recorded without stopping or carrying the check while only a stall ends it early. |
| Publish the result store on a Git ref | Rejected for now, per the warning in [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint). |
| Keep adding checks to the one program and shard its suite | Rejected. The suite reruns because every check shares the file; a faster 141 seconds is still a shared file. |

| Risk | Mitigation |
| :--- | :--- |
| A check reads a file it did not declare, and a stale pass is reused | The default is the whole tree; narrowing is per file and reviewed there. A whole-tree run with an empty store is the audit, and costs six minutes. |
| Under load, fast checks overrun their declared budgets | Under [`D-GR15`](#decision-ledger) a working check runs to completion and keeps its verdict (carrying is refused in `tools/tillr-gate/run.go`); `over_budget_ms` is recorded on the verdict and surfaced in the per-check performance stats, while the watchdog stops only a stalled check. |
| The heavy runner falls behind twenty lanes | Runs are coalesced, so it judges the newest tip, not every landing; a long gap stops landing at the red cap of [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer). |
| Moving checks out of the gate program drops one | Each moving slice asserts the union of the two planners' check ids equals the set before the move. |

## Decision Ledger

`D-GR1` to `D-GR6` are the owner's rulings of 2026-11-15. `D-GR7` to `D-GR12` are this design's
own decisions, made to carry them out; each is the owner's to overturn by comment. `D-GR13` and
`D-GR14` are an implementer's decisions of 2026-11-16, made while building the fast tier; the
owner ruled on both on 2026-11-17, and that ruling is `D-GR15`. `D-GR16` is this design's own
decision carrying `D-GR15` out, and is the owner's to overturn. `D-GR17` records the owner's
2026-11-19 ruling on shard lifetime and tests that hang or fail under load, together with the
2026-11-20 scope ruling for its build. `D-GR21` is the owner's ruling of 2026-11-21 on a check
with a pass and a fail on one fingerprint, recorded in two verbatim statements from that day;
`D-GR22` is the operator's reading of the first statement together with this design's mechanism,
neither of which is the owner's ruling (except the two points the owner's second statement ruled
directly), and the rest is the owner's to overturn. `D-GR23` is the designer's decision of 2026-11-21 on where that
mechanism lives, and is also the owner's to overturn. `D-GR31` is the designer's decision of
2026-11-21, from an operator request, on which Go tests the culprit search does not replay, and
is the owner's to overturn. The rows `D-GR21` and `D-GR22` were first numbered `D-GR18` and `D-GR19`, which the
identity rows of 2026-11-20 already held; they were renumbered on 2026-11-21, so a record
written that day that cites `D-GR18` for a disabled check means `D-GR21`. The three rows of this
document's former open questions ([`OQ-GR1`](#OQ-GR1) to [`OQ-GR3`](#OQ-GR3)) were initially
settled by the session's designer on recorded principle: [`OQ-GR3`](#OQ-GR3) remains in force,
while [`OQ-GR1`](#OQ-GR1) and [`OQ-GR2`](#OQ-GR2) were later withdrawn/overturned by the owner
([`OQ-GR1`](#OQ-GR1) ruled out by [`OQ-GP7`](tillr-graph-is-the-path.md#OQ-GP7), with the
20-landing cap kept live until its replacement waiting rule is built; [`OQ-GR2`](#OQ-GR2)
overturned in `1fa2c069c` and withdrawn in
[the graph design Section 12.2](tillr-graph-is-the-path.md#122-decisions-in-other-documents)). `D-GR24` is the owner's ruling of 2026-11-21 that puts the `green` lane base back for new work, with trunk fixers on the tip; it replaces the 2026-11-17 overturn of [`OQ-GR2`](#OQ-GR2). `D-GR25` is the designer's list of where the built `green` behavior differs from those words; it decides nothing and each case is the owner's to rule on. `D-GR26` is the designer's decision on an operator request of 2026-11-21 that carries a question the owner asked that day, quoted verbatim; the owner may overturn it. `D-GR27` is the designer's proposal from the evidence `D-GR26` produced; it decides nothing, is not built, and is the owner's to rule on. `D-GR28` is the designer's decision of 2026-11-21, made to answer a build worker's report against an operator request; it amends `D-GR23` and is the owner's to overturn. `D-GR29` is the designer's decision of 2026-11-21 on an operator request to repair four drain test files that wait on completions; it narrows the request to the one file the tree shows at fault and is the owner's to overturn. `D-GR30` is the designer's decision of 2026-11-21 on an operator request for
fast-gate timing, made to answer a build worker's report; the owner may overturn it. `D-GR32` is
the designer's decision of 2026-11-21, from an operator's answer to a build worker's report on
the first classifier slice; it quotes the owner's ruling [`OQ-SP1`](./tillr-item-split.md#OQ-SP1)
and applies it, and the application is the owner's to overturn. A draft of that row was numbered `D-GR31` before the trunk gave that number to the culprit-search row, so a run record that cites `D-GR31` for the first classifier landing means `D-GR32`. `D-GR33` is
the designer's decision of 2026-11-21, made to answer a build worker's report against the operator
request that follows that landing; it amends `D-GR28` and `D-GR32` and is the owner's to overturn.
`D-GR34` is the designer's decision of 2026-11-21, made to answer a second build worker's report
against the same request; it amends the "no test edited" wording of `D-GR33` and of slices 1e and
1g, and is the owner's to overturn. `D-GR36` is the designer's decision of 2026-11-22, made to
answer a third build worker's report against the same request; it states which test files the
fixture rule of `D-GR34` reaches, and is the owner's to overturn. `D-GR37` is the designer's
decision of 2026-11-22, made to answer a fourth build worker's report against the same request;
it states that a fixture copying `tools/fork-check.py` is reached by that rule, lists the
fixtures measured, and is the owner's to overturn. `D-GR38` is the owner's ruling of 2026-11-21
on the gate's time for a drain change, quoted verbatim from the operator request that carried
it; it amends `D-GR1` and answers `D-GR27`. `D-GR39` is the designer's decision of 2026-11-22,
from that operator request and made to answer a build worker's report against it, on the check
that runs the affected Go test packages before the merge; it is the owner's to overturn. `D-GR40`
is the designer's decision of 2026-11-22, answering a second worker report on that request by
keeping Go's result cache on for every selected package; it amends `D-GR39` and is the owner's to
overturn. `D-GR41` is the designer's decision of 2026-11-22, from the operator request for
"Slice 2 of the flaky-check rule" and made to answer a build worker's report against it; it
splits live `disabled` emission into slice 2d after the drain's sweep and consumer slices, narrows
this request's landing to slice 2a, and is the owner's to overturn.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-GR1` | The landing gate targets ten seconds per change. **Amended by the owner on 2026-11-21** (`D-GR38`): "10 seconds was apsirational". The ten seconds stays as the target of the checks every candidate runs, held by the arithmetic test; it is not a target for a candidate that changes a tillr Go module, which also runs its affected test packages before the merge. | 2026-11-15; amended 2026-11-21 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | ✅ (the budget tests `tools/tillr-gate/fastbudget_test.go` and `fastceiling_test.go`) |
| `D-GR2` | Lanes run independently, and landings must outpace them at ten lanes and again at twenty. | 2026-11-15 | [Section 5](#5-the-arithmetic-at-ten-and-twenty-lanes) | ✅ (`tools/tillr-drain/scale_test.go`, `scale_race_test.go`) |
| `D-GR3` | The landing strategy is a per-repository setting with two values: heavy checks before the merge (the trunk is always green) or after it (the trunk is briefly red). | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | ✅ (`tools/tillr-drain/drain/gate_policy.go`) |
| `D-GR4` | This repository takes the second value: heavy checks after the merge. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | ✅ (`landing_strategy` is `after-merge` in `config/tillr/gate-policy.json`) |
| `D-GR5` | tillr deploys only from the last trunk commit whose after-merge checks passed. | 2026-11-15 | [Section 6.7](#67-deploys-read-the-green-pointer) | ✅ (`tools/tillr-drain/drain/trunk_release.go`) |
| `D-GR6` | Automatic revert of an offending landing is a setting, and it is off in this repository to start. **Overturned by the owner on 2026-11-17** ([OQ-GP1](tillr-graph-is-the-path.md#OQ-GP1)): tillr reverts by itself when the full suite proves a landing broke the trunk, and a fixer is filed only when the revert is unsafe, does not repair, or the red is unattributed. | 2026-11-15, amended 2026-11-17 | [Section 6.8](#68-a-red-heavy-check) | 2026-11-18 (`auto_revert` true in `config/tillr/gate-policy.json`) |
| `D-GR7` | A change check gets its base from the caller, runs once at landing, and never against a remembered commit. **Rejected:** repairing the remembered baseline's cold start. Structural fix. | 2026-11-15 | [Section 6.1](#61-the-caller-names-the-base) | ✅ (`DeriveChange` in `tools/tillr-gate/change.go` takes the base from the caller) |
| `D-GR8` | A tree check's pass is remembered by a fingerprint of its declaration, the files it reads and the tools it names; a fail is recorded and never reused; the store is local to a machine. **Rejected:** remembering by commit, and publishing the store. Structural fix. **Amended 2026-11-21 by `D-GR21`:** this row's Section 6.2 rule that a pass arriving on a fingerprint with a fail "passes and reports the check as `flaky`" is replaced; the check is disabled. | 2026-11-15; amended 2026-11-21 | [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint) | ✅ (`tools/tillr-gate/memory.go`) |
| `D-GR9` | A check with an unmet machine need is `unavailable`, named with its command, and neither fails nor passes. Structural fix. | 2026-11-15 | [Section 6.3](#63-a-missing-machine-resource-is-named-not-failed) | ✅ (`tools/tillr-gate/needs.go`) |
| `D-GR10` | A check is one declaration file with no verdict-changing default, read by `tillr-gate`; the gate program retires by attrition. **Rejected:** sharding the shared suite. Structural fix. | 2026-11-15 | [Section 6.4](#64-a-check-is-one-file) | ✅ (`tools/tillr-gate/checkfiles.go`; one declaration per check in `tools/gate/checks/`) |
| `D-GR11` | The budget is enforced by an arithmetic test over declared costs and by a per-check deadline that carries an overrun to the heavy tier; a check that overruns alone turns the heavy run red on `gate-budget`. **Rejected:** failing landings on measured wall time. Structural fix. **Amended 2026-11-16** by `D-GR13` (a fast check is not stopped at its budget but at its ceiling) and `D-GR14` (carrying is suspended until the heavy runner exists); the arithmetic test is unchanged. **Superseded in part 2026-11-17** by the owner's ruling `D-GR15`: no deadline stops a check that is making progress, so the per-check deadline and carrying at it go, and an overrun alone never turns a run red; the arithmetic test stays. | 2026-11-15 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | Arithmetic test built; the deadline as amended |
| `D-GR13` | A fast check still running at its `budget_ms` runs on to its ceiling — its `timeout_ms`, or the 9,000 millisecond cap, clamped to lie between its budget and the cap — and keeps its verdict, with `over_budget_ms` recorded; only a check still running at its ceiling is "could not judge". The worst case is 11 x 9,000 = 99,000 milliseconds of checks. Decided by the implementer after `documents` overran by 1 millisecond under load and held an item; the owner has not ruled on it and may overturn it. **Rejected:** stopping a change check at its budget (load faults the item), and carrying it (nothing after the merge has its base). **Superseded 2026-11-17** by the owner's ruling `D-GR15`: the ceiling is no longer a kill, and a check still making progress is never "could not judge"; the `over_budget_ms` record stays. The alternative of one ceiling for the whole fast run is rejected by the same ruling. | 2026-11-16 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | Built; the superseded part is replaced by the stall watchdog |
| `D-GR14` | Carrying a fast tree check to the heavy tier is suspended until the heavy runner exists: until then an over-budget fast tree check is run to its ceiling and keeps its verdict, as in `D-GR13`. The switch is `tillr-gate`'s `--tree-overrun` (`keep` by default), stated once in `tools/gate/change-gate.py` as `TREE_OVERRUN`; `item-gate-heavy-runner` turns it to `carry`. Decided by the implementer because with no heavy runner a carried check's judgment is dropped, and `go-build` was carried on almost every ten-lane landing; the owner has not ruled on it and may overturn it. **Rejected:** carrying with nothing to take what is carried, and answering "could not judge" at the budget. **Ruled on 2026-11-17** by the owner's ruling `D-GR15`: only a stall ends a check early, so `keep` is the rule rather than a suspension, and carrying, which stops a check at its budget, is not turned on as designed. | 2026-11-16 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | Built |
| `D-GR15` | **The owner's ruling, 2026-11-17:** "target budgets, watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just fine." A budget is a target that is measured and reported; exceeding one is never by itself a failure, a fault or "could not judge". What ends a check early is a stall, a check making no progress, seen by a watchdog, and a stall leads to a named next step, never a silent kill. Machine load must not turn work that would have finished into a failure. **Supersedes** the ceiling as a kill and "could not judge" for a check still making progress (`D-GR13`), and the per-check deadline of `D-GR11`. **Keeps** the budgets, the admission test against the 9,000 millisecond cap, the `over_budget_ms` record, and the ten-second landing as a target with its measured distribution visible. **Recorded 2026-11-22 by the designer** (`D-GR39`, the owner's to overturn): the 9,000 millisecond cap bounds the fast declarations every candidate runs; a check planned only for some candidates, the affected Go test packages among them, is outside the sum, and this ruling's "exceeding one is never by itself a failure" applies to the owner's 40 seconds of `D-GR38` as it does to any budget. | 2026-11-17 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | ✅ (`tools/tillr-gate/watchdog.go`; `item-gate-stall-watchdog` and `item-gate-wrapper-clocks-stall` landed) |
| `D-GR16` | A check shows progress in a one-second sample when its output grew or its process group's CPU time rose; 60 consecutive samples with neither is a stall. The runner ends a stalled check's process group and records `stalled`, which is neither pass, fail nor over-budget, with what it observed. The drain keeps the candidate and re-gates it once, when fewer gates are in flight than at the stall or no other gate is; a second stall on the same candidate holds the item, naming the check. `gate-harness-error` stays for a check that could not start or crashed. Settled by the session's designer on 2026-11-17, on `D-GR15` and [landing principle 11](landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) (a re-run waits on a named change, never on time passing); the owner may overturn it. **Rejected:** a longer fixed timeout, which fails the same way at a higher load; re-gating on the next pass, which names no change. | 2026-11-17 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | ✅ (`tools/tillr-gate/watchdog.go`) |
| `D-GR17` | **Owner ruling (2026-11-19) and scope answer (2026-11-20).** *"Rule of this repository: a test that fails or hangs under load is a broken test; do not add a timeout, a retry or a longer wait as the fix"*, and a test run started by a worker must not outlive the command that started it. `tools/go-test-shards.py` must tie every `go tool test2json` and compiled shard process group (`start_new_session=True`) to the invoking runner command's lifetime and keep the compiled binary directory valid until every shard exits; hanging or load-sensitive tests in `tools/tillr-drain` are fixed at their synchronization cause so multiple concurrent suite runs pass. The build scope covers `tools/tillr-drain/` (mirrored to `yolo-packs/tillr/` via `just pack-sync`, keeping `runTests` in `tools/tillr-drain/main_test.go` self-contained or paired with `tests/tillr-testmain_test.go`), `tools/go-test-shards.py`, and `tests/` (`tests/go-test-shards.test.py`, `tests/tillr-testmain_test.go`). **Rejected:** adding timeouts, retries, or longer sleeps to mask load hangs; leaving detached `test2json` sessions alive after `go-test-shards.py` exits or is killed. | 2026-11-19 / 2026-11-20, owner | [Section 6.5](#65-ten-seconds-is-a-failing-test) | Queued |
| `D-GR18` | **Owner ruling, 2026-11-20:** "for this repository, I want a personal filter that says the commits may only be one of the two identities, my personal or professional identity. But that is for this repository, not for Tiller." | 2026-11-20 | [Which identities this repository accepts](#which-identities-this-repository-accepts) | Not yet built |
| `D-GR20` | **Designer's decision on the operator request; the owner may overturn it.** Bound aggregate test parallelism across runs. Lanes test the bound and exclusion of queue time; the proof operator measures before/after proof wall time and load with five active lanes. | 2026-11-20 | [Bound concurrent tests, not just shards](#bound-concurrent-tests-not-just-shards) | Not yet verified |
| `D-GR19` | **Designer's decision on the operator request; the owner may overturn it.** Apply one tracked list of exact pairs to author and committer in both the standalone check and the live `commit-identity` gate. Preserve their ranges and refusal of repository identity overrides; do not change commit creation or tillr. | 2026-11-20 | [Which identities this repository accepts](#which-identities-this-repository-accepts) | Not yet built |
| `D-GR21` | **Owner ruling (2026-11-21), verbatim (two statements):** *"The correct move for a flaky test is disable it, file an item to fix it, and don't let it block anything. It is not considered green, but it also doesn't let you block other things."* and *"And the principle here is you make the trunk green by disabling the flaky test. The test is not green because it's flaky, but the trunk is green because the test is no longer included.And yes, the item that is filed should be to re-enable it correctly, the test that was flaky."* It governs a check whose result store holds both a pass and a fail for one fingerprint, rules that disabling excludes the flaky check so the trunk is green while the check itself is not green and that the filed item is to re-enable the flaky check correctly, and replaces the `flaky` pass of `D-GR8`, the live re-run of [`OQ-ER3`](./tillr-evidence-registry-and-suite-guardrail.md#OQ-ER3) option A, and the red verdict a filed operator request of 2026-11-20 asked for. | 2026-11-21 | [A contradicted check is disabled](#a-contradicted-check-is-disabled) | — |
| `D-GR22` | **Not the owner's ruling (except the two points ruled directly in `D-GR21`'s second statement); the owner may overturn the rest.** The operator's reading of `D-GR21` (2026-11-21): the check is disabled and not run for a verdict, is reported as disabled and never as passed or green, refuses no landing, does not turn the trunk red (ruled directly in `D-GR21`), gets one deduplicated repair item with both times and both output locations, and is enabled again when the repair lands (filing the item to re-enable the flaky check correctly is ruled directly in `D-GR21`). The designer's mechanism for it (2026-11-21): a `disabled` result counted apart from passes; `log_ref` on each stored record; a marker per check id, so a new fingerprint does not re-arm an unrepaired check; the drain files `flaky-<check-id>` from the marker (`D-GR23`, replacing "from the verdict"), matching only an unfinished item, so a contradiction found after a landed repair files a new item with the new evidence; the repair's own candidate runs the check, and `tillr-gate enable` then removes the marker and sets the contradicting pair aside. Amended by `D-GR41` so live `disabled` emission turns on in slice 2d after the sweep (2b) and consumer handling (2c, 3a) land. **Rejected:** a red verdict (the filed request; replaced by `D-GR21`); a live re-run whose pass is green ([`OQ-ER3`](./tillr-evidence-registry-and-suite-guardrail.md#OQ-ER3) option A; replaced by `D-GR21`); disabling by fingerprint alone; any retry, count or time window. | 2026-11-21; amended 2026-11-21 and 2026-11-22 | [A contradicted check is disabled](#a-contradicted-check-is-disabled) | — |
| `D-GR23` | **Not the owner's ruling; the designer's decision, which the owner may overturn.** Every site that classifies a check result (70, listed by function and consumer) calls one function, `Classify` in a `checkresult` package of the shared `tools/tillr` module, reached from Python and for store reads through one face, `tillr-gate classify`. It has five outcomes: `pass`, `fail`, `unknown`, `unavailable` and `disabled`; a site decides blocking and credit from the outcome alone. A cached result is classified when it is used. A replayed commit supplies the declaration and the command, never the classification. The repair item is filed by a sweep of the marker files, which replaces "from the verdict" in `D-GR22`; a generated repair item needs no document block. **Rejected:** teaching each site the rule as it is found (refused by its verifier eleven times); a classifier per program held together by a shared table; filing from the verdict with a filing step on every exit path; four outcomes with `unavailable` folded into `unknown`. | 2026-11-21 | [One classifier, five outcomes](#one-classifier-five-outcomes) | — (not built; [build order](#classify-every-check-result-in-one-place)) |
| `D-GR24` | **Owner ruling (2026-11-21), verbatim:** *"So I think the real rule should be you can only start a new item if the previous item's trunk landing went green because you don't want to start it until the other one has landed on the trunk and it's okay and then you always just base an item on the most recent green on the trunk, and I think that satisfies everything."* and *"And yes, of course, a trunk fixer is meant to only be based on a red change, so of course it's based on a red one."* It replaces the owner's 2026-11-17 overturn of [`OQ-GR2`](#OQ-GR2) for new work. From an operator request of the same day, which reads the ruling as the `green` value of `lane_base` and reads "the previous item" as the item's blocker (the operator's reading, which the owner may overturn): a new lane is cut from the green pointer; an item whose blocker has landed is held with the reason `trunk-unproved` until the heavy runner has proved a trunk commit containing that landing; a trunk fixer starts from the tip; this repository sets `lane_base` to `green`. | 2026-11-21 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | ✅ [`config/tillr/gate-policy.json`](../../config/tillr/gate-policy.json) selects `green`, pinned by `TestRepositoryGatePolicyUsesGreenLaneBase` in [`gate_policy_test.go`](../../tools/tillr-drain/gate_policy_test.go). The [lane-base tests](../../tools/tillr-drain/drain/lane_base_test.go) verify the blocker-proof wait and the fixer base. |
| `D-GR25` | **Not the owner's ruling and not a decision: the designer's list, for the owner to rule on.** The built `green` behavior differs from the words of `D-GR24` in five cases, tabled in Section 6.6: a lane cut from an unproved commit after green when the item's own block is newer than green; the same when the item's design answer landed after green; lanes cut from the tip when no green pointer exists; a fork blocker proved by its delivered patch branch, not by the green pointer; and a landed blocker with no recorded landing, which waits. Each stays as built until the owner rules. **Rejected:** changing any of them to match the words, which the request that carried the ruling forbids. | 2026-11-21 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | ✅ as built (`tools/tillr-drain/drain/lane_base.go`); no ruling yet |
| `D-GR26` | **Not the owner's ruling; the designer's decision on an operator request, which the owner may overturn.** The owner asked on 2026-11-21, verbatim: *"Can we tell from our records when an item passed the light gate and then failed the heavy gate? Because that's evidence we're going to want to look at and decide something. Probably just making the light tests better if possible."* The request answers with a read-only report, the `tillr view heavy-misses` face (the name is the designer's), listing each landing that passed its fast gate and was later named by a heavy failure, with its commit, item, time, failing checks, revert or fixer outcome and changed paths, summarized by check and by changed module; and with Section 6.10, which reads that report from 2026-11-19 3:55 PM to 2026-11-21 6:35 PM Eastern and belongs to this document, so its designer writes it. Each failing check carries an attribution (`confirmed`, `offered`, `disproved` or `named`), and only the first two are counted as evidence. The request forbids changing the fast tier's selection. **Rejected:** counting every landing a fixer record names, which counts landings that only arrived on a red trunk (36 of the 50 listed). | 2026-11-21 | [Section 6.10](#610-what-passed-the-fast-gate-and-failed-the-heavy-run) | Built: the face is in [`tools/tillr-view`](../../tools/tillr-view/README.md); evidence read 2026-11-21 |
| `D-GR27` | **Not the owner's ruling and not a decision: the designer's proposal, for the owner to rule on.** A candidate that changes a path under `tools/tillr-drain` also runs, before the merge, the sharded `tools/tillr-drain` and `tools/tillr-view` suites; every other candidate is gated as today. It is the smallest selection of existing whole-suite checks that would have run the failing suite for more than half of the period's attributed landings: 7 of 13, against 4 for the drain suite alone, 3 for the view suite alone, 6 for the changed modules only and 9 for the changed modules with the modules that require them. That is an upper bound; none of the seven is proved refused, because the judged tree is not the landed tree and no judged candidate was replayed. Two paired timings of one green drain change (`81ea2675de` against `5514f6eda3`), beside running lanes, added 235.06 and 318.56 seconds to a fast gate of 41.17 and 46.97 seconds, against the ten seconds of `D-GR1`; no other candidate was timed. The trigger matched 245 of the period's 472 landings, which is how often it would have run and not a measured delay. The designer leans against building it now (keep the fast tier; make the drain suite faster, explain its intermittent failure, and make the revert quicker, first); the owner's quoted leaning is toward better fast tests. Nothing is built or changed until the owner rules. **Ruled on by the owner on 2026-11-21** (`D-GR38`): tests run before the merge, so the designer's leaning is overturned. The proposal's own form, whole sharded suites run fresh, is not built; the affected test packages of `D-GR39` are. | 2026-11-21 | [Section 6.10](#610-what-passed-the-fast-gate-and-failed-the-heavy-run) | — (closed by `D-GR38`; replaced by `D-GR39`) |
| `D-GR28` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** The operator request for the first classifier slice says "No outcome changes for any input". The single raw-result table and single aggregate order of `D-GR23` changed five measured outcomes at three sites (P6, F3, F6), so `D-GR23` is amended: `Classify` holds a named protocol per kind of raw result and `Aggregate` a named order per aggregate site, each reproducing the tree, and a site passes the name and compares nothing. A Go module build is `fail` on every non-zero exit; a legacy check follows `check_status` and `Check.conflict` (exit 4 of an ordinary kind is `fail`, exit 124 is `unknown`); the legacy gate's order is stalled, then failed, then unproven. The Python side hands over yes-or-no facts about a check's output and decides nothing from them. A difference a later slice finds is added the same way by its builder, with a test and a row in the section, and is not a definition obstacle. **Rejected:** allowing the outcome changes by name; sending pass and fail words from Python; merging the orders into the runner's in this group. | 2026-11-21 | [Each site keeps its raw-result protocol and aggregate order](#each-site-keeps-its-raw-result-protocol-and-aggregate-order) | — (not built; [build order](#classify-every-check-result-in-one-place)) |
| `D-GR29` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** The operator request of 2026-11-21 asks for the repair of commit `4790d2ad43` (receive a test's completions before canceling) in `policy_reload_test.go`, `fixer_intermittent_test.go`, `pass_measurement_test.go` and `lane_dirty_test.go`, saying all four cancel and then receive. At `86e2549392` only `policy_reload_test.go` does; the other three receive under `context.Background()`, where `sendCompletion` cannot drop a completion. So only `policy_reload_test.go` is repaired: its workers are released by a channel and their completions received while the context is live, with `cancel()` last; the repaired receive waits under the `hangGuard` deadlock detector and a released worker returns an error. The three others are unchanged and named with the reason in the commit body, which also states whether the real loop can lose a completion after cancel. The mirror comes from `just pack-sync`. **Rejected:** editing the three files that have no cancel; a time limit on the receive in place of the repair; a `hangGuard` on the three receives that have no fault (a second, unlanded draft of this decision proposed it); repairing `Loop.Examine` in `recovery.go` in the same change, which needs its own request; changing `sendCompletion`. **Recorded 2026-11-21 by the designer:** a third unlanded draft reached the same answer and is not adopted as a separate text; the built cleanup's receive is a bare receive on a live context, not one under `hangGuard`, and is accepted as built. **Recorded 2026-11-22 by the designer:** a fourth unlanded draft, written against the same worker report, reached the same answer and is not adopted; that report is answered by this row, the repair is built, and no build work remains under the request. The rule kept from it: no test cleanup makes a blocking receive from the loop's completions channel after it cancels the context its workers send under. | 2026-11-21 | [Section 6.5](#65-ten-seconds-is-a-failing-test) | ✅ (commit `c6a01f6e84`: [`policy_reload_test.go`](../../tools/tillr-drain/drain/policy_reload_test.go) and its mirror) |
| `D-GR30` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** Each run of the fast gate that judges a candidate before it lands, on its ordinary path, leaves a durable record of its wall time, each check's elapsed time and whether it ran, was reused or was not run, and the Go packages each Go check selected. The packages come from `go-build`'s own `go list`, from `go-substrate`'s `go test` output, and for `gate-runner`, which runs sharded and prints no package lines, from `tools/go-test-shards.py` writing its `go list` result to the file named by `TILLR_GO_SHARDS_EVIDENCE_OUT`, before compiling, and only when that variable is set. The Go package piece may therefore edit `tools/go-test-shards.py` and `tests/go-test-shards.test.py` for that producer and its fixture tests only. The heavy trunk proof, the legacy tree-check bridge, relanding, the automatic revert candidate, named replays and the heavy-failure count are out of scope and listed in the candidate's commit message. **Rejected:** leaving `gate-runner` out; reading the `--events` file; a second `go list` in `tools/fork-check.py`; running the suite unsharded. | 2026-11-21 | [Section 6.11](#611-the-fast-gate-before-a-landing-records-its-time-and-its-go-packages) | — (not built) |
| `D-GR31` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** The operator request of 2026-11-21 reports the heavy runner still replaying a deadlocked Go test alone after `5c78981400`, and asks to find the path and stop it there with no time limit. The path is `HeavyRunner.attribute` to `findFirstRed` to `judgeHeavyGo` in `declaration` mode, for a named case whose first proof recorded Go's runtime deadlock error (`fatal error: all goroutines are asleep - deadlock!`) and no timeout panic; `5c78981400` skips the replay only for the timeout panic. The rule is widened: a named Go test whose first-proof output holds either line is not replayed, at the same three places as the timeout case (`attribute`, `retainRedRepairs`, and the suite check beside an unnamed failure), and one fixer is filed against the tip. This amends the request's sixth criterion, which names only the test-timeout panic, to cover the deadlock line the first proof actually recorded. **Rejected:** skipping the replay for every Go `fatal error:`; a time limit on a focused replay; never replaying a named Go test. | 2026-11-21 | [A Go test that did not finish is not replayed](#a-go-test-that-did-not-finish-is-not-replayed) | — (not built; the timeout half is `5c78981400`, `tools/tillr-drain/drain/heavy_go.go`) |
| `D-GR32` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** The operator request for the first classifier slice names every site and adds that classification on replay runs from current code and that a test per site shows the call. The operator answered a build worker's report on 2026-11-21: "the installed tillr-gate cannot have the classify face until a change containing it lands and is proved green, so split here. Offer as this item's landing only the classify face in tillr-gate with its tests, and nothing that calls the installed face." The request's landing is therefore slice 1a alone: `checkresult` with `Classify` and `Aggregate`, the `tillr-gate classify` face, their tests and the pack mirror, with no caller and no existing test edited. The unmet cases are named in the candidate's commit message and follow as later changes: every site calling the function with a test per site (slices 1b to 1e), the adapters calling the installed face (slice 1e), and replay classified from current code (slices 1e and 3b). A verifier does not refuse this landing for them. The scan test moves from slice 1a to the first of slices 1b to 1e to land. The ruling this rests on is the owner's [`OQ-SP1`](./tillr-item-split.md#OQ-SP1) of 2026-11-21, verbatim: *"part of desgins can absoutely land. this is generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the smallest functional change possible to make reviewing, testing, velocity, all this stuff easier, and it will also make it easier for us."* **Rejected:** one change holding the face and its callers; Python sites calling a `tillr-gate` built from the candidate's tree; holding the face until a caller is ready. | 2026-11-21 | [The first classifier landing is the function and its face, with no caller](#the-first-classifier-landing-is-the-function-and-its-face-with-no-caller) | ✅ (`020cf29eca`) |
| `D-GR33` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** The operator request that follows the first classifier landing asks that the gate adapters call the installed `tillr-gate classify`. The installed face classifies each observation and aggregates nothing (measured 2026-11-21: `--aggregate legacy-gate` is refused as an undefined flag, exit 2), so the legacy gate's and the legacy replay's aggregates (F6, F9) cannot hand over their order's name as `D-GR28` requires. The face gains `--aggregate <order>`: the same observations in, one object out with `checks` (the plain form's array) and `run` (`outcome`, `detail`, `exit_code`), for the orders `runner`, `shadow`, `legacy-gate` and `legacy-replay`; another name is exit 2 with no output, and the plain form is unchanged. Slice 1e is narrowed to the Python sites that classify one check and is the request's landing; slice 1f adds the flag with no caller; slice 1g moves `verdict_of` and `run_replay` onto it after a proved trunk commit holds 1f. Until then the two functions keep their code and change no outcome, and a verifier does not refuse the request's landing for them. Landing a part while a named case is unmet rests on the owner's [`OQ-SP1`](./tillr-item-split.md#OQ-SP1), quoted in `D-GR32`; applying it here is the designer's reading. **Rejected:** holding every adapter until the face aggregates; one change holding the flag and its callers; ordering the face's per-check answers in Python; an aggregate form that takes classifications; a second `aggregate` face. | 2026-11-21 | [The face aggregates a run in its own landing, before the legacy aggregates call it](#the-face-aggregates-a-run-in-its-own-landing-before-the-legacy-aggregates-call-it) | Slice 1e ✅ (`0bcc3275a2`); 1f and 1g unbuilt ([build order](#classify-every-check-result-in-one-place)) |
| `D-GR34` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** The request's landing (slice 1e) was to pass every existing test unedited. One existing test cannot: `tests/fast-tree-in-done.test.py` copies only `tools/fork-check.py` and `tools/gate/tree-check.py` into its tree and writes its own `tillr-gate` first on PATH, which asserts that it was called as `checks --fast-tree`, so the moved fast tree adapter's `classify` call gets exit 1 and no output, the adapter answers exit 4, and the test's expected exit 1 fails (a build worker's reproduction, 2026-11-21). The rule is split: a test's assertions are edited by no slice; its fixture setup may be edited in two ways only. The fixture copies any file a moved adapter newly reads, and a program a test writes to stand in for `tillr-gate` hands `classify` to the next `tillr-gate` on PATH, unchanged, and never classifies by itself. The Python sites may share one file under `tools/gate` that makes the call and compares nothing. **Rejected:** holding the fast tree adapter back; a stand-in that prints a classification; an option or environment variable naming the classifier; an adapter that falls back to its own pairing; rewriting the fixture with no stand-in. | 2026-11-21 | [A stand-in for the installed program is fixture setup, and it answers classify](#a-stand-in-for-the-installed-program-is-fixture-setup-and-it-answers-classify) | — (not built; [build order](#classify-every-check-result-in-one-place)) |
| `D-GR35` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** The operator request of 2026-11-21 reports the proof worktree's registration left locked with reason `initializing` and no directory, 20 failed heavy runs on `missing but locked`, and a trunk that could not be proved until the operator ran `git worktree unlock` and `git worktree prune`; it asks that the heavy runner recover by itself and that the change say what interrupted the add. Recovery: when the registration of the runner's own proof path is locked, the runner, under the registry lock it already holds, unlocks that path, reports one loop-log line with the path, the lock's reason and the `locked` file's time, and continues with its existing remove and add; a test covers the directory missing and the directory present. The cause: not established. `gateRestart` defers the exec while `heavyProofActive` is set, since `edaa4fdf4b`, so the tree does not show an in-place restart crossing the add, and the incident's logs were not available to the design seat; the building commit states the cause as unconfirmed and does not name the restart. Cancellation ending Git with SIGKILL is recorded as a hypothesis only. **Rejected:** `git worktree prune` in the runner, because it acts on other worktrees' registrations; `git worktree add --force --force`, because it overrides a lock without recording it; naming the restart as the cause from the request's words alone, because the code at that build contradicts it. | 2026-11-21 | [The proof worktree's registration is left locked](#the-proof-worktrees-registration-is-left-locked) | — (not built) |
| `D-GR36` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** A build worker's plan for the adapters' landing (slice 1e) allowed writes only under `tools/gate/` and to `tests/fast-tree-in-done.test.py`. The Go test `TestFixerGateJudgesOwnDiffThenNamedChecksAndLands` in `tools/tillr-drain/drain/fixer_gate_test.go` copies only `tools/fork-check.py` and `tools/gate/change-gate.py` into its tree and runs the real change gate, so an adapter that reads a shared file fails there with `ModuleNotFoundError` until the fixture copies that file too (a build worker's reproduction, 2026-11-21). The two setup edits of `D-GR34` apply to every test whose fixture copies a moved adapter or writes a stand-in, in any language and directory, Go tests under `tools/tillr-drain`, `tools/tillr-gate` and `tools/tillr-loop` included. Those test files, for those edits only, and their pack mirrors regenerated by `just pack-sync`, are among the files slices 1e and 1g may write. A `tillr-gate` a test builds from the repository's source is the real program and not a stand-in. **Rejected:** repeating the call in each adapter; leaving `change-gate.py` out for this reason; copying all of `tools/gate/` into the fixture; listing every copying fixture in the document. | 2026-11-22 | [A fixture that copies a moved adapter is in the mover's change, in any language](#a-fixture-that-copies-a-moved-adapter-is-in-the-movers-change-in-any-language) | — (not built; [build order](#classify-every-check-result-in-one-place)) |
| `D-GR37` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** A build worker's plan for the adapters' landing (slice 1e) allowed writes to `tools/tillr-drain/drain/fixer_gate_test.go` and not to `tools/tillr-drain/drain/heavy_legacy_restart_test.go`. The helper `testHeavyLegacyRestart` in the second file copies `tools/fork-check.py` into a tree whose `tools/gate/` holds only `checks/.keep`, appends a `main` that calls `check_status`, and runs it, so a `check_status` that reads a shared file fails there with `ModuleNotFoundError` until the fixture copies that file too (a build worker's reproduction, 2026-11-22). `tools/fork-check.py` is a moved file under the fixture rule of `D-GR34` and `D-GR36`: this fixture copies the one shared file into its tree, changes no assertion and copies nothing else. The document now lists the four test files measured to copy a moved file at `417d7d8df4`; a plan for slice 1e or 1g lists all four and their pack mirrors among its write paths, and the rule of `D-GR36` still adds any fixture the list lacks. **Overturns** `D-GR36`'s rejection of a list in the document. **Rejected:** keeping the list out; a call in `tools/fork-check.py` that does not use the shared file; a fixture `main` that writes status words itself; copying all of `tools/gate/` into the fixture. | 2026-11-22 | [A fixture that copies the legacy gate is reached too, and the plan lists the fixtures measured](#a-fixture-that-copies-the-legacy-gate-is-reached-too-and-the-plan-lists-the-fixtures-measured) | — (not built; [build order](#classify-every-check-result-in-one-place)) |
| `D-GR38` | **Owner ruling (2026-11-21), verbatim:** *"if you think drain suite selected tests are 40 seconds, let's do that, and monitor how it goes, just make sure we have perf monitoring all over it. 10 seconds was apsirational"*. The words are quoted from the operator request of 2026-11-21 that carried them. It amends `D-GR1` (ten seconds is an aspiration) and answers the question of `D-GR27` (Go tests run before the merge). The 40 seconds is an estimate from the gate's cost table, not a measurement and not a limit. | 2026-11-21 | [Section 6.12](#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands) | — (ruling; carried out by `D-GR39`) |
| `D-GR39` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** A candidate that changes a tillr Go module (`tools/tillr` and each `tools/tillr-*` with a `go.mod`; not the `yolo-packs` mirrors, `tools/dotmod` or `tools/bedrock-spend`) also runs `go-affected` (the designer's name) before the merge: a landing-tier kind in `tools/fork-check.py` on the ordinary path, beside `go-substrate` and `gate-runner`. It selects the changed packages, every test package whose imports reach one (the Go import graph reversed across the modules), the test packages a tracked edges file ties to a sibling program they build or run or to a non-Go file they read, and, for a change the graph cannot place, every test package of the module and of the modules that replace it (the designer's reading of the request's "floor"). It runs `go test` on those packages with Go's result cache on. One exception, the designer's: a package selected only through a declared edge, whose tests do not declare those inputs to the cache, runs with `-count=1`, because a child build is invisible to the cache. The check is outside the `gate-budget` sum, so the 9,000 millisecond cap and every fast budget are unchanged, and no time limit fails it (`D-GR15`). The timing record of `D-GR30` gains its selection time, slot wait and, per package, the reason, elapsed time and whether it was cached. The builder reports measured times from those records. **Rejected:** whole sharded suites run fresh (`D-GR27` as proposed); a 40,000 millisecond fast declaration with the cap raised; selecting only edited tests; the import graph with no declared edges; the cache on for declared edges; `-count=1` everywhere; a build system or coverage maps as the selector. | 2026-11-22 | [Section 6.12](#612-a-change-to-a-tillr-go-module-runs-its-affected-test-packages-before-it-lands) | — (not built) |
| `D-GR12` | A question about a moving upstream is a drift probe that files an item and never moves or holds the green pointer. **Rejected:** keeping it as a check on a commit. Made visible where it happens. | 2026-11-15 | [Section 6.9](#69-upstream-drift-is-a-probe-not-a-check) | — (`series` and `patch-branch-inv2` are `TIER_LANDING` in `tools/fork-check.py`; no probe exists) |
| OQ-GR1 | The default of a per-repository setting, `red_cap`: landing stops at 20 landings or 60 minutes past the last green commit; a repository may set a different bound, and zero means no cap. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are AGENTS.md's "a mechanism whose cost rises with trunk activity is pointed the wrong way" and the owner's 2026-11-15 ruling that the landing strategy is configurable per repository and red is brief. The owner may overturn it. **Rejected:** no cap, which lets the fixer's range and the deploy lag grow without limit. **Ruled out by the owner on 2026-11-18** ([OQ-GP7](tillr-graph-is-the-path.md#OQ-GP7)): under a red check, work lands only if it does not touch what that check reads, and there is no landing cap. The cap is still live in the tree (20 landings; its time bound was turned off on 2026-11-17, `d53580bfd`) until the graph design's waiting rule is built. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | ✅ (`tools/tillr-drain/redcap/cap.go`, `drain/red_cap.go`) |
| OQ-GR2 | The default of a per-repository setting, `lane_base`: a new lane starts from the last green commit; a repository may choose the trunk tip. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principles are that a worker does not pay for a failure it did not cause, and the same cost-direction rule. The owner may overturn it. **Rejected:** always cutting from the tip. **Overturned by the owner on 2026-11-17** (`1fa2c069c`): work is always built against the latest trunk, never a past commit; a lane cut from the green pointer was handed a tree older than its own item's definition and produced false conflicts. This repository set `tip`; the code's default when the key is absent is still `green`. **Replaced by the owner's ruling of 2026-11-21** (`D-GR24`): new work starts from the last proved trunk commit again, and this repository sets `green`. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | ✅ (`tools/tillr-drain/drain/lane_base.go`; the repository setting follows `D-GR24`) |
| OQ-GR3 | A change to the gate's own runner lands only after the runner's suite passes, before the merge, under either strategy. Settled by the session's designer on recorded principle at the owner's request of 2026-11-15 ("fill them in through principles"); the principle is the fence of [escalation ladder Section 5.2](escalation-ladder.md#52-the-fence-what-a-lane-may-not-edit) (gate definitions change only as their own item), for the reason the 2026-11-05 ruling gave: a wrong judge is the one red a revert does not repair. The owner may overturn it. **Rejected:** landing it in ten seconds and re-running every landing's fast checks after a red suite. | 2026-11-15 | [Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) | ✅ (`tools/gate/runner-first.test.py`) |
| `D-GR40` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** It amends `D-GR39` to match the operator request of 2026-11-21, whose words are "runs go test on the selected packages with Go's test cache left on (no -count=1)". The one exception `D-GR39` made is withdrawn: no package the affected-packages check selects is run with `-count=1`, whatever the reason it was selected. The stale pass that exception guarded against is stopped another way. The check passes each test package that has an entry in the edges file a digest of that entry's inputs in an environment variable, the package's tests read the variable, and Go's result cache then reruns the package when the digest changes and answers from the cache when it does not. The timing record no longer holds a `-count=1` field. Every other part of `D-GR39` stands. **Rejected:** `-count=1` for packages selected only through a declared edge (`D-GR39`'s exception; the request excludes it); the cache on for those packages with their inputs left invisible to it (a pass from before the change); the drain's symlink alias copied into every module with an edge (it covers Go sources only, and a second copy of it per module). | 2026-11-22 | [The result cache stays on for every affected package](#the-result-cache-stays-on-for-every-affected-package) | — (not built) |
| `D-GR41` | **Not the owner's ruling; the designer's decision, from an operator request, which the owner may overturn.** Answers a build worker's report on the operator request for "Slice 2 of the flaky-check rule" at `0450fedb34`, where only slices 1a (`020cf29eca`) and 1e (`0bcc3275a2`) have landed. Records the owner's second 2026-11-21 statement in `D-GR21`. Reconciles "at that observation ... before any other work" with `D-GR23`: `tillr-gate` atomically writes the `disabled/` marker and copies both logs at the contradicting observation before printing or returning, and `tillr-drain`'s marker sweep (slice 2b) files `flaky-<check-id>` at pass start and immediately when every gate, replay, rolling or heavy step returns, before attribution, pending reverts or dispatch. Splits live `disabled` emission in `tillr-gate run` / `tillr-gate checks` (`G2`, `G3`, `G4`, `G7`) and the Python store-backed adapters (`P4` to `P6`) into slice 2d (after 1b, a proved trunk commit holding 2a, 2b, 2c and 3a), so live runs never emit `disabled` before consumers accept it and the sweep can file its repair item. Under [`OQ-SP1`](./tillr-item-split.md#OQ-SP1) and `D-GR5`, narrows this request's landing to slice 2a: rules 1 to 3 in `tools/tillr/checkresult`; store-backed `--repo` / `--store` / `--prove-disabled` on `tillr-gate classify` (exact `<id>`, `<id>:<label>` and `<id>/<slug(label)>` selector resolution, committed-declaration fingerprinting, optional per-unit `fingerprint`, pre-run query, marker + log-copy write, while plain `tillr-gate classify` stays pure); `tillr-gate enable` with episode matching, `enabled/` fence and `resolved/` archive; `log_ref` on stored records; tests and `just pack-sync` mirror; taking only those store mechanics from `refs/tillr/runs/run-item-request-c1a6f8e4e5-a14-n5` `[unlanded]`. Slices 1b–1d, 1f, 1g, 2b–2d, 3a and 3b are named unmet cases listed in the candidate's commit message, and a verifier does not refuse slice 2a for them. **Rejected:** building all remaining slices in one commit; emitting `disabled` from live runner faces before 2b, 2c and 3a; opening the SQLite queue in `tillr-gate`; touching the repo or store on plain `tillr-gate classify` with no flag; taking `TILLR_DISABLED_OBSERVATIONS` or raw string checks from the donor ref. | 2026-11-22 | [The second flaky-check request lands the rule in the classifier and its gate faces](#the-second-flaky-check-request-lands-the-rule-in-the-classifier-and-its-gate-faces) | — (not built; [build order](#classify-every-check-result-in-one-place)) |

### The result cache stays on for every affected package

What follows is the designer's decision of 2026-11-22 (`D-GR40`), made to answer a build worker's
report against the operator request of 2026-11-21 for the check that runs the affected Go test
packages before the merge. None of it is the owner's ruling, and the owner may overturn any part
of it. It amends `D-GR39` and the part of this document that `D-GR39` settles, the section
numbered 6.12 and titled "A change to a tillr Go module runs its affected test packages before
it lands". Where that section and this one differ, this one is the decision. The terms used here
(affected test package, declared edge, the edges file and its `builds` and `reads` fields, the
`go-affected` check) are defined in that section.

**The report was right.** The request's words are: "runs go test on the selected packages with
Go's test cache left on (no -count=1)". `D-GR39` kept the cache on and then made one exception,
the designer's own: a package selected only through a declared edge, in a module whose tests do
not declare the edge's inputs to Go's result cache, was to run with `-count=1`. A builder cannot
follow both. The request is the later and the more specific statement, and the exception was
never the owner's, so the exception goes.

**What is decided.**

- `go-affected` never passes `-count=1` to `go test`, for any package, whatever the reason the
  package was selected. A package Go answers from its result cache counts as passed, as before.
- The fault the exception guarded against is real and is stopped another way. A test that builds
  or runs a sibling program does so in a child process, and Go's cache does not see what a child
  reads. Go's cache also ignores a file the test itself opens outside its own module (stated in
  `tools/tillr-drain/internal/testinputs/inputs.go`). So a cached pass for such a package could
  date from before the change that selected it. The remedy is to make the inputs visible to the
  cache, not to turn the cache off.
- **The edge digest** *(the term and the variable name below are the designer's)* is one hash
  over the contents, in the candidate's tree, of everything a test package's entry in the edges
  file names: for each program under `builds`, the files of that program's package and of every
  package it imports inside the tillr modules, with those modules' `go.mod` and `go.sum`; for
  each path under `reads`, the file, or every file under the directory prefix. The package list
  comes from the import graph the selection already read.
- The check runs each selected package that has an entry in the edges file with the environment
  variable `TILLR_GATE_EDGE_INPUTS` set to that package's edge digest. Such a package is run in
  its own `go test` call, so that it sees its own digest and no other package's.
- Each test package with an entry in the edges file reads that variable once, in its `TestMain`.
  Go's result cache records the environment variables a test reads and their values. A changed
  digest therefore reruns the package, and an unchanged one is answered from the cache. Adding
  that read to the packages listed in the edges file is part of the request's build.
- The drain's own declaration (`testinputs.Track`) is left as it is. It serves runs outside the
  gate, where the variable is unset.
- Outside the gate the variable is unset, so its value is the same on every run and a cached
  pass there can be stale, as it can today. This decision does not change that. The heavy tier
  still runs each whole suite fresh after the merge.

**What keeps it true.** Two tests, both part of the request's build:

- The test that scans the modules' `_test.go` files against the edges file also fails when a
  package with an entry there does not read `TILLR_GATE_EDGE_INPUTS`.
- A test in the shape of `tools/tillr-drain/cache_test.py` proves the mechanism on one package
  with a `builds` edge and one with a `reads` edge: a second run with the same digest is
  answered from the cache, and a run after a change to an edge input is not.

If that second test cannot be made to pass, the mechanism is wrong and the builder reports it as
a fault in this decision. The builder does not fall back to `-count=1`.

**What is recorded.** The timing record's per-package fields are the reason the package was
selected, its elapsed time and whether Go answered it from the cache. The field `D-GR39` listed
for whether a package was run with `-count=1` is not recorded, because no package is.

**What a verifier of the request's landing judges, restated.** Everything `D-GR39`'s paragraph
lists, with one change: the check's `go test` commands carry no `-count=1`, and the two tests
above exist. A verifier does not refuse the landing because a package selected through a
declared edge was answered from the cache.

| Alternative | Verdict |
| :--- | :--- |
| `-count=1` for a package selected only through a declared edge (`D-GR39`'s exception) | Rejected. The request says "no -count=1", and the exception was the designer's own. |
| The cache on for those packages with nothing added | Rejected. The pass could be one from before the change, which is the miss this check exists to stop. |
| Copy the drain's `testinputs.Track` into every module that has an edge | Rejected. It needs a symlink alias kept in each module, it declares Go sources only, and it would be a second and third copy of the same code. |
| Have the check delete the cached result of an edge package before running it | Rejected. It is `-count=1` by another name: the package would run fresh on every gate, changed or not. |

## 11. Build order

Nine slices. Each lands alone and leaves the gate working; each names the slice before it only
where it needs that slice's code. A slice whose fence holds `tools/tillr*` also holds the pack
mirror, because the two are compared at landing. A slice deletes its own block from this document
in the commit that lands it. Three behaviors of
[Section 6.6](#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer) had no slice here: the
lander's refusal at the red cap, staging a lane from `lane_base`, and the runner's own changes
paying their suite before the merge. The policy slice reads and validates their settings, and
all three were built from [the tillr roadmap](../tillr/roadmap.md#filed-2026-11-15-the-three-gate-rulings-no-slice-builds)
as `item-gate-red-cap`, `item-gate-lane-base` and `item-gate-runner-pays-first`. As of 2026-11-18
every slice below has landed.
Three more slices were filed on 2026-11-17 from the owner's ruling
[`D-GR15`](#decision-ledger) and the measurements beside it in
[Section 6.5](#65-ten-seconds-is-a-failing-test); they are the last three filed slices below.
Two units after them are written and not filed: the single check-result classifier
(`D-GR23`, 2026-11-21) and the drift probe.

### The runner reads one file per check



### A tree check that already passed does not run


**The store keeps a pass and a fail apart, so no writer can erase the other's record.** This
refines the result store of [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint)
for this slice, and where the two differ this block governs: a fingerprint has up to two record
files, one named for a pass and one named for a fail. A run that passed renames only onto the
pass file, and a run that failed renames only onto the fail file. No run rewrites, merges into
or deletes the other verdict's file. A contradiction is not stored in either record: it is what a
reader sees when both files exist, and since 2026-11-21 it disables the check
([A contradicted check is disabled](#a-contradicted-check-is-disabled)); the one command that
moves the pair is `tillr-gate enable`, after the repair lands. A pass published while a fail is being published therefore cannot lose the fail,
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

After a landing the trunk holds changes that only the heavy checks have judged, so one runner proves the tip and moves the green pointer only when nothing failed. Criterion c4 was aligned on 2026-11-17 with the owner's ruling on time limits ([`D-GR15`](#decision-ledger), [Section 6.5](#65-ten-seconds-is-a-failing-test)): a budget overrun in the heavy run is measured and reported and fails nothing, and nothing here carries a fast check from the fast tier.



### tillr builds from the green pointer



### Twenty lanes land without waiting on each other



**The land lock's hold is the whole landing transaction, and this item leaves that boundary where
it is.** Criterion c2 first said the lock was held "only between a squash's start and its trunk
move". That is narrower than the lander, and a run stopped on it on 2026-11-17. Read that day in
`Land` in `tools/tillr-land/land/landing.go`: the lock is acquired before `prepareLanding`, which
reads the trunk tip and squashes onto it, and it is released by a deferred call after the store
transaction that moves the trunk and writes the receipt has committed. A landing that is refused
takes the same lock to pin the head and write its row. [Section 9](#9-what-this-costs-and-what-it-does-not-do)
says this work does not change the land lock, and that stands, so c2 now states the boundary the
lander has. What the criterion protects is unchanged: no gate runs under the lock, and the lock
serializes one landing at a time. The hold c3 reports is acquisition to release, which is the
quantity [Section 5](#5-the-arithmetic-at-ten-and-twenty-lanes) calls "the whole hold" and leaves
unmeasured. If the events recorded today do not carry the lock's acquisition and release, this
item adds them. Recording the hold observes the lock and does not move its boundary.
**Rejected:** narrowing the hold to the squash and the move, which would let a second landing
read the trunk before the first one's receipt is committed, and which changes the lander in a
slice that is a measurement; and keeping the first wording with a test that treats the receipt
as outside the hold, which asserts something the tree does not do. Whether the hold should be
shorter is a question for the numbers c3 prints, not for this slice. Settled by this design's
seat on 2026-11-17; the owner may overturn it.

**The store's lock wait under the race detector is a finding this item reports, and the repair
is not this item's.** A second run stopped on 2026-11-17 with its work complete and uncommitted.
The ordinary build passed, and the twenty-lane case under `go test -race` ended with
`store: beginning the migration: database is locked (5) (SQLITE_BUSY)`. The store is
`tools/tillr/store/`, outside this item's paths. Measured that day on that run's tree, with the
store's trace (`TILLR_SQLITE_TRACE=1`) read from the test's output:

| Twenty-lane case | Runs | Result | Longest wait for the store's write lock at an open | Median hold of that lock by an open |
| :--- | :--- | :--- | :--- | :--- |
| Ordinary build | 4 | 4 passed, 100 landings each | 2.03 s (one run traced) | 1.2 ms |
| `-race` | 4 | 4 failed, none reaching 100 landings | 5.29 s, the failed open (one run traced) | 34 ms |

The cause is in `Open` in `tools/tillr/store/open.go`: an open that may create the store runs the
migration, and the migration begins with `BEGIN IMMEDIATE`, so every such open takes the store's
write lock, whether or not the schema is already current. In the traced ordinary run the loop's
own process did that 1,956 times for 100 landings. The race detector makes the test process
slower, each hold became about 28 times longer, and one open waited out SQLite's 5,000 ms busy
timeout. The ordinary build's longest wait was already two fifths of that timeout. This is the
kind of fact the scale test was written to find, and it is a fact about the store rather than
about the gate: c4 already names the store as the one thing the lanes share.

So the item is finished as follows.

- The criteria are judged on the ordinary build, as c1 now says. No gate step runs the race
  detector: `rg -n -e '-race' .justfile tools/gate` returned nothing on 2026-11-17.
- The scale test skips under the race detector, and the skip's message names this finding and the
  store's open as the cause, so `go test -race ./...` in the module is not red for a reason
  outside the module and nobody reads the skip as a pass. The skip is removed by the store
  repair, which then runs the twenty-lane case under `-race` as its own evidence.
- The item's report states the table above, or its own re-measurement of it.
- If the ordinary build fails the same way, that is this same finding: the run stops and reports
  it, and does not change the test to get past it.

The repair belongs to an item whose paths include `tools/tillr/store/`, and none is filed yet;
this amendment may edit only this block. Its shape is for that item to design. The direction this
design already states is "keeping work out from under the store's locks", so the open of a store
whose schema is current should not take the write lock at all.
**Rejected:** raising the busy timeout, which
[the contention repair plan](../recovery/sqlite-contention/plan.md#constraints) refused and which
lengthens the wait without shortening the queue; retrying a busy open in the loop or in the test,
which hides the number the test exists to show; widening this item's paths to the store, which
turns a measurement into a change to the one file every program shares; lowering the lane count
under the race detector, which keeps a test that proves less than its name; and leaving the test
red under `-race` with no skip, which makes the next reader rediscover this. Settled by this
design's seat on 2026-11-17; the owner may overturn it.

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



**Agent details:**

- **Ref:** this document's [Section 6.5](#65-ten-seconds-is-a-failing-test), the 2026-11-17 measurement, and [Section 6.2](#62-a-tree-checks-result-is-remembered-by-fingerprint) for how a tree check's pass is remembered.
- **Where:** `document_keys` and `judge_documents` in `tools/gate/checks/change_common.py`; `tools/gate/checks/documents.json`; `tools/gate/checks/go-build.py` and `tools/gate/checks/go-build.json`, whose `reads` is `["."]`; the fingerprint rule in `tools/tillr-gate/`.
- **Evidence:** 2026-11-17, at a load average of 6.5 to 7 on 32 cores, median of five runs: `uvx vantage-check check --format json docs/tillr/roadmap.md` 2.16 s (2.00 to 2.35); the same over `docs/design/gate-rework.md` (66,393 bytes) 0.68 s and over `docs/design/run-wall-ruling.md` (19,159 bytes) 0.37 s; `uvx vantage-check version` 0.23 s. `wc -c docs/tillr/roadmap.md` is 148,607 bytes, so the time per kilobyte is about twice that of the 66 KB document.
- **Evidence:** 2026-11-17, `git log --since=2026-11-14 --format=%h -- docs/tillr/roadmap.md | wc -l` is 112, against 365 for `git log --since=2026-11-14 --format=%h`.
- **Evidence:** 2026-11-17, a document's remembered-pass key includes its own blob (`document_keys`), and the check judges only the documents the change touches, so a remembered pass is reused only when the same content is gated again.
- **Evidence:** 2026-11-17, `python3 tools/gate/checks/go-build.py` in the main checkout: 2.72 s on the first run after the working tree's Go files changed, then 0.88 and 0.89 s with nothing changed. The drain's operator measured 2.7 to 3.3 s on warm lanes the same day. The check builds and compiles the tests of every module under ten tool directories on every run that is not remembered.

### Classify every check result in one place

The disabled-check rule (`D-GR21`, `D-GR22`) is built as thirteen slices, in three groups, in the
order the table gives. The first group moves every
[classification site](#every-place-a-check-result-is-classified) onto
[the one function](#one-classifier-five-outcomes) and changes no behavior. The second adds the
rule inside that function and gives each consumer and live runner face its handling of `disabled`.
The third covers evidence stored before the rule and commits older than it. Each slice lands
alone, leaves the gate and the drain working, and is verified by the tests named in its row
without any later slice. A slice in the first group is a refactor: its proof is that the module's
existing tests pass unchanged, plus the scan test below.

The **scan test** *(coined here)* is one test, added by the first of slices 1b to 1e to land
(`D-GR32`; landed in `0bcc3275a2`), that reads the source
of the four programs and fails when a file outside `checkresult` compares a result word, a span
status or a judging program's exit code. It carries a list of files not yet moved; each slice of
the first group removes its files from that list, and the list is empty when the group is done.
It is what keeps a site added later from classifying on its own.

| Slice | What it changes | Sites | Verified alone by |
| :--- | :--- | :--- | :--- |
| 1a | Add `checkresult` with `Classify` and `Aggregate` holding rule 4's table and [every protocol and aggregate order](#each-site-keeps-its-raw-result-protocol-and-aggregate-order) only, and the `tillr-gate classify` face. Nothing calls them. [This slice is the whole landing of the first filed request](#the-first-classifier-landing-is-the-function-and-its-face-with-no-caller) (**built in `020cf29eca`**) | none | Table tests in the package: one case per row of the raw-result table, per row and step of each protocol, and per pair of each aggregate order. A face test: observations in, outcomes out in the same order, and no check command started |
| 1b | Route the runner's sites through the function | G1 to G12 | `go test ./...` in `tools/tillr-gate` passes with no test edited; the scan list loses the runner's files |
| 1c | Route the drain's landing-gate and light-attribution sites | D1 to D16, D34 | `go test ./...` in `tools/tillr-drain` passes with no test edited; the scan list loses those files |
| 1d | Route the drain's rolling, heavy and proof sites, and the lander site | D17 to D33, L3 | `go test ./...` in `tools/tillr-drain` and `tools/tillr-land` passes with no test edited; the scan list loses the rest of the drain and lander |
| 1e | Route the Python sites that classify one check through the installed face: `check_status` takes its words from it under the legacy-check protocol, `go-build.py` and the documents check under theirs, and the adapters stop pairing exit codes with words themselves. `verdict_of` and `run_replay` are not touched ([`D-GR33`](#the-face-aggregates-a-run-in-its-own-landing-before-the-legacy-aggregates-call-it); **built in `0bcc3275a2`**) | P1 to P7, F1 to F5, F7, F8, F10 to F13, L1 to L4 | `python3 tests/fork-check.test.py` and the tests under `tools/gate` pass with no assertion edited; a fixture's setup changes only as [`D-GR34`](#a-stand-in-for-the-installed-program-is-fixture-setup-and-it-answers-classify) allows, in whichever test file holds the fixture, Go tests included ([`D-GR36`](#a-fixture-that-copies-a-moved-adapter-is-in-the-movers-change-in-any-language)), and a plan's write paths list the four fixtures measured ([`D-GR37`](#a-fixture-that-copies-the-legacy-gate-is-reached-too-and-the-plan-lists-the-fixtures-measured)); the scan list holds only `tools/fork-check.py`. The two copies of `tillr-loop` stay byte-identical |
| 1f | The face gains `--aggregate <order>`: observations in, the classifications and the run's result out. Nothing calls it | none | A face test per order: the `run` object equals `Aggregate` on the same classifications, `checks` equals the plain form's output, an unknown order is exit 2 with no output, and the plain form's output is unchanged |
| 1g | `verdict_of` takes the run's result from the face under the legacy gate's order and `run_replay` under the legacy replay's | F6, F9 | `python3 tests/fork-check.test.py` passes with no assertion edited (`D-GR34`); the scan list is empty |
| 2a | Add rules 1 to 3 in `checkresult`, store-backed `--repo` / `--store` / `--prove-disabled` on `tillr-gate classify` (exact `<id>`, `<id>:<label>` and `<id>/<slug(label)>` selector resolution, committed-declaration fingerprinting, optional per-unit `fingerprint`, pre-run query, and atomic marker + log-copy creation on a single contradiction, while plain `tillr-gate classify` stays pure), `tillr-gate enable` (episode match, `enabled/` fence and `resolved/` archive), and `log_ref` on stored records, while keeping live `tillr-gate run` and `tillr-gate checks` output unchanged until `2d`. [This slice is the whole landing of the flaky-check slice-2 request](#the-second-flaky-check-request-lands-the-rule-in-the-classifier-and-its-gate-faces) (`D-GR41`) | none (faces only) | Package and face tests in `tools/tillr/checkresult` and `tools/tillr-gate`: stored pass + fresh fail and stored fail + fresh pass each give `disabled` with marker and copied logs; a marker disables a new fingerprint; `--prove-disabled` evaluates rule 4; `enable` removes only the episode it names and records `enabled/` + `resolved/`; plain `tillr-gate classify` with no flags stays pure |
| 2b | The drain's marker sweep files the repair item (asking to fix the flaky check and re-enable it correctly, `D-GR21`) at pass start and on every gate, replay, rolling or heavy step return before attribution, pending reverts or dispatch, and runs `enable` after the repair lands. A generated repair item needs no document block | a new step; no listed site changes | Drain tests with a marker file written by the test and no gate run: one item per episode; none while one is unfinished; a new item after a landed repair and a new episode; a sweep after a cancelled step and after a step that errored still files before pending reverts or dispatch |
| 2c | Every drain and lander consumer handles `disabled` as the classifier section lists, and the drain passes `--prove-disabled <check-id>` when gating a `flaky-*` repair item's candidate | D2, D3, D7, D8, D10, D13, D16, D18, D23, D29, D30, D32 to D34, L3 | Drain and lander tests, one per bullet of that list, each with a stub gate that returns `disabled`: no refusal, no red trunk, no fixer, the held item released, the notice silent, the grouped revert still judging its other members, and `--prove-disabled` passed for `flaky-*` |
| 3a | Cached evidence is classified at use | D11, D24 to D28, D31, F2 | Drain tests that write a proof, a checkpoint, a replay answer and an attribution record as the code before slice 2a wrote them, add a marker, and find none of them credited or reused |
| 2d | Turn on live `disabled` classification, pre-run skip, marker + log-copy creation and `--prove-disabled` in `tillr-gate run` and `tillr-gate checks`, and pass `--repo` from the Python store-backed checks (`P4` to `P6`) to the installed `tillr-gate classify` (`D-GR41`) | G2, G3, G4, G7, P4 to P6 | Runner and adapter tests: `TestMemoryFailRetriesAndFlakyPass` and `TestMemoryOppositeWritersNeverErase` are rewritten to `disabled`, exit 0, marker + copied logs; end-to-end `flaky-*` proving and re-enabling on land passes |
| 3b | The installed runner judges store-backed declarations on a replayed commit; the fast tree adapter passes the committed declaration bytes and keeps its logs; the legacy status words gain `DISABLED`; labelled selectors resolve through the face | P1, P3, F1, F4, F7, L2, D12, D16, D17, D21 | A test that replays a commit whose own `tools/fork-check.py` and `tools/gate/tree-check.py` predate slice 1e, with a marker present: the replay reports the check disabled, the rolling run is not `unknown`, and the fingerprint equals the landing gate's for the same declaration. A proof under a declaration's id satisfies a fixer that names its labelled selector |

**Outcomes:**

- Let a contradicted check stop blocking landings and stop turning the trunk red, on every path.
- Never credit a contradicted check as green, from a fresh run or from stored evidence.
- File one repair item per disabling, whatever happens to the run that found it.

**Not yet filed.** On the owner's ruling of 2026-11-18, no document gains a new item directive
while the owner reviews how documents declare work. The slices are written to become items
however that ruling lands. The candidate that was refused eleven times is evidence for the site
table and is not a base for these slices: slice 1a starts from the trunk.

An operator filed the first group as one request on 2026-11-21, outside any document. Its
landing is slice 1a alone (`D-GR32`, built in `020cf29eca`); slices 1b to 1g are the cases it
leaves unmet. A second operator request of 2026-11-21 asks for the adapters' calls to the
installed face; its landing is slice 1e (`D-GR33`, built in `0bcc3275a2`), with slices 1f and 1g
as the cases it leaves unmet; a test's stand-in for the installed program may be set up to pass
`classify` on (`D-GR34`), and a fixture that copies a moved adapter is edited in the same landing
wherever it is, Go tests included (`D-GR36`). A fixture that copies `tools/fork-check.py` is one
of those, and a plan for the landing lists the four copying fixtures measured among its write
paths (`D-GR37`). A third operator request ("Slice 2 of the flaky-check rule") lands slice 2a
alone (`D-GR41`), with slices 1b to 1d, 1f, 1g, 2b, 2c, 3a, 2d and 3b as the cases it leaves
unmet.

**Agent Details:**

- **Paths:** `tools/tillr/checkresult/` (new), `tools/tillr-gate/`, `tools/tillr-drain/`,
  `tools/tillr-land/`, `tools/tillr-loop/`, `tools/gate/`, `tools/fork-check.py`,
  `tools/go-test-shards.py`, their pack mirrors, and this document. Run `just pack-sync` after
  the last source edit of a slice and commit the regenerated mirror.
- **Ref:** `D-GR21` to `D-GR23`, `D-GR28`, `D-GR32` to `D-GR34`, `D-GR36`, `D-GR37` and `D-GR41` in the [Decision Ledger](#decision-ledger).
- **Order:** 1a first, alone, and proved green so that the installed `tillr-gate` has the face
  (`D-GR32`; landed in `020cf29eca`). 1b to 1f need only 1a and may land in any order (1e landed
  in `0bcc3275a2` and added the scan test). 1g needs 1e, and 1f proved green so that the
  installed `tillr-gate` has the flag (`D-GR33`). 2a needs only 1a (`D-GR41`). 2b needs 2a. 2c
  needs 1c, 1d and 2b. 3a needs 2c. 2d needs 1b, 2c, 3a, and 2a proved green so that the
  installed `tillr-gate` has store-backed `classify` (`D-GR41`). 3b needs 2d and 1g.
- **Forbidden in every slice:** a result word compared outside `checkresult`; a second
  implementation of the rule; a retry or a rerun added to make a contradicted check agree with
  itself.

### Move the upstream drift checks out of the gate into a probe

Two gate checks ask about an upstream, not about the change under review. One asks whether
each patch series still applies. The other asks whether each fork's patch branch still merges
the release a launch would build. Both fetch from the network, so a new upstream release can
turn them red with no change in this repository. When that happens, every landing waits on a
repair that none of them caused. [Section 6.9](#69-upstream-drift-is-a-probe-not-a-check) rules
that a verdict on a commit must depend only on that commit. So these two questions leave both
tiers of the gate and become a **drift probe**: a run, outside the gate, of the same two
questions against the release a launch would build. The loop runs the probe when it starts and
after any landing that changes a series or a pack declaration. A red probe files one repair
item that names the fork and the release. It never holds the green pointer or moves it. A
series that a landing changes is still checked against its own recorded base by a heavy tree
check that needs no network.

**Outcomes:**

- Let an upstream release no longer stop landings; report it as a repair item instead.
- Keep checking a changed series against its own base, as part of the landing that changed it.
- Show each probe run's result and the item it filed.

**Not yet filed.** On the owner's ruling of 2026-11-18, no document gains a new item
directive while the owner reviews how documents declare work. This unit is written to become
an item however that ruling lands. It waits on `item-gate-pinned-fork-inputs` on
[the tillr roadmap](../tillr/roadmap.md), which is queued and changes the same checks in the
same files.

- **c1:** With no tree change and a fork whose upstream has a new release the series does not
  merge, the landing gate and the heavy tier both pass. Neither tier plans the series check or
  the patch-branch merge check.
- **c2:** The drift probe asks both questions for every fork and series a pack delivers. The
  loop runs it once at start and once after each landing that changes a series or a pack
  declaration, and at no other time.
- **c3:** A red probe files one repair item through the loop's defect path. The item names the
  fork, the release and the failing question. A second red probe for the same fork and release
  files nothing new.
- **c4:** A red probe does not move or hold the green pointer, and a landing in progress
  finishes as it would have without it.
- **c5:** A landing that changes a series still runs a heavy tree check of that series against
  its own recorded base, and that check makes no network call.
- **c6:** The probe's last result for each fork, and the item it filed, can be read from the
  loop's status, and the drain README states when the probe runs.

**Agent Details:**

- **Proposed id:** `item-gate-drift-probe`.
- **Paths:** `tools/fork-check.py`, `tests/fork-check.test.py`, `tools/gate/`,
  `tools/tillr-drain/`, `yolo-packs/tillr/files/local/share/tillr/tillr-drain/`, and this
  document.
- **Ref:** [Section 6.9](#69-upstream-drift-is-a-probe-not-a-check) and `D-GR12` in the
  [Decision Ledger](#decision-ledger).
- **Where:** `series` and `patch-branch-inv2` are `TIER_LANDING` in the tier table of
  `tools/fork-check.py`; the probe can be a fork-check mode that plans only those two kinds. The
  defect path is `fileDefectItem` in `tools/tillr-drain/drain/defect.go`. The loop's start and
  post-landing hooks are in `tools/tillr-drain/drain/loop.go`. Run `just pack-sync` after the
  last source edit and commit the regenerated mirror.
- **Verify:** Run `python3 tests/fork-check.test.py` with a fixture fork whose upstream gains a
  release the series does not merge, and check that both tiers stay green and the probe goes
  red. Add drain tests that use a stub probe and check the filing, the deduplication, the
  untouched green pointer and the two run times. Run `go test ./...` in `tools/tillr-drain`.
