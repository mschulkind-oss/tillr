---
title: "Gate dual-run evidence — the Go gate and the Python gate on the same candidates"
status: draft
stage: CURRENT
updated: 2026-11-11
depends-on: [board-utilities.md, board-utilities-lanes.md, tillr-next-steps.md]
tags: [gate, tillr, evidence, dual-run, parity]
summary: "Three real candidates run through both gates. The two gates planned the same command checks on all three, and disagreed on exactly one thing: the Python gate runs `file-size` as its own code, the Go plan carries it as in-process, and `tillr-gate run` refuses the whole plan (verdict `error`, exit 4) rather than omit it. Every Go verdict in this evidence is therefore `error` while the Python gate verified the same candidates at exit 0. The Go gate's planning agrees; its judging is unmeasured because it never executed a check."
---

# Gate dual-run evidence

**Status:** 2026-11-11, read against the tree at `353832593c` [unlanded] — the lane tip carrying
the harness commit. That commit's content landed as `b8b34fed86`, and the only difference between
the two trees is this evidence pair, but the shas below stay the lane's own: a candidate range is
`base..HEAD` of the checkout that ran, so those ranges are reproduced from that lane, not from
HEAD. This is evidence, not a ruling: whether `tools/fork-check.py` is retired is a later decision
made on this record, and nothing here makes it.

## 1. What was run

The harness is `tillr-gate dualrun` ([`tools/tillr-gate/dualrun.go`](../../tools/tillr-gate/dualrun.go)).
For each candidate it drives both gates as the binaries a caller would run, because the exit
code is part of the evidence:

- **Go gate** — `tillr-gate plan --registry <export> --input <gate.change> --root <checkout>`,
  then `tillr-gate run --input <plan> --worktree <checkout>`.
- **Python gate** — `python3 tools/fork-check.py --explain --since <base> --no-wait` for the
  plan, then `python3 tools/fork-check.py --since <base> --no-wait` for the verdict.

The registry both sides are compared through is the Python gate's own export,
`python3 tools/fork-check.py --registry-json`, taken at the same commit: 72 command
declarations and 3 in-process checks, digest
`2da14bbf49195e44bc85921272dad58e477f4bbed7622eecf09d282482c1cb69`.

The one command that produced this record:

```bash
tillr-gate dualrun --registry <registry.json> --report <report.json> --work <work> \
  --root <checkout> --self <tillr-gate> --python <checkout>/tools/fork-check.py \
  --candidate empty=HEAD --candidate harness=16a8430f6a --candidate gate-own-change=2569c8a625
```

It answered 0 in 4m52s and wrote the report committed beside this document as
[`gate-dual-run-evidence.report.json`](./gate-dual-run-evidence.report.json). The harness
answers 0 when it produced its report: a disagreement is a finding about the two gates, not a
failure of the harness.

## 2. The candidates

A candidate is a base commit against the checkout's HEAD, because the Python gate plans
`base..HEAD` of the tree it runs in and has no flag for an arbitrary head. All three are real
changes in this repository's history; none is a fixture built to agree.

Every row's head is the lane tip, which is not on HEAD; each row says so with the marker.

| Candidate | Base | Head | Paths | What it is |
| :--- | :--- | :--- | :--- | :--- |
| `empty` | `353832593c` | `353832593c` | 0 | the checkout's own HEAD against itself: the control that plans nothing [unlanded] |
| `harness` | `16a8430f6a` | `353832593c` | 3 | this lane's own commit: `tools/tillr-gate/dualrun.go`, `dualrun_test.go`, `main.go` [unlanded] |
| `gate-own-change` | `2569c8a625` | `353832593c` | 5 | the landed `tools/fork-check.py` fix plus this lane's commit [unlanded] |

## 3. What each gate planned and judged

Kinds, not steps: a kind is what both gates select by, and several steps can share one
(`go-tool` is one per directory).

| Candidate | Go plan | Go verdict | Python plan | Python verdict |
| :--- | :--- | :--- | :--- | :--- |
| `empty` | `commit-identity`, `structural` | `error`, exit 4 | `commit-identity`, `file-size`, `structural` | `pass`, exit 0 |
| `harness` | `commit-identity`, `go-build`, `go-tool`, `structural` | `error`, exit 4 | `commit-identity`, `file-size`, `go-build`, `go-tool`, `structural` | `pass`, exit 0 |
| `gate-own-change` | `commit-identity`, `go-build`, `go-tool`, `pipeline-evidence`, `self-suite`, `structural` | `error`, exit 4 | `commit-identity`, `file-size`, `go-build`, `go-tool`, `pipeline-evidence`, `self-suite`, `structural` | `pass`, exit 0 |

Both plan faces answered 0 on all three candidates. The Go plan's `in_process` list is
`file-size` on all three.

## 4. The disagreements, stated plainly

All three candidates are classified `disagree`, and the same two disagreements account for
every one of them.

**`file-size` is planned by one gate and refused by the other.** The Python gate runs it as
its own code. The Go plan carries it in `in_process`, and `tillr-gate run` refuses the whole
plan rather than produce a verdict that omits it — verdict `error`, exit 4. This is
A-INPROCESS in [`tillr-next-steps.md`](tillr-next-steps.md) working as ruled, and it is not a
defect in either gate: it is the parity gap that ruling exists to keep visible.

**The verdicts differ because of it.** `error` at exit 4 against `pass` at exit 0, on all
three candidates. `file-size` declares no selecting path, so it is owed by every change: the
Go gate cannot judge *any* candidate today, and the `empty` control shows the refusal is not
about the change at all.

**Everything else agreed.** Every command check kind the Python gate planned, the Go plan
planned too, on all three candidates — `structural`, `commit-identity`, `go-build`, `go-tool`,
`pipeline-evidence`, `self-suite`. The two gates selected the same steps for a change that
touches a Go module, for a change that touches the gate's own suite, and for a change that
touches nothing.

## 5. What this evidence does not establish

- **The Go gate's execution is unmeasured.** Every plan owed `file-size`, so `run` refused
  before executing anything. This record compares the two gates' *planning* and their
  *verdicts*; it says nothing about whether the Go gate would run a check correctly.
- **No full gate run was made.** The Python verdicts are `--since` runs: a targeted
  diagnostic that plans the candidate's own range, skips the upstream-drift probes, and does
  not advance the recorded baseline. The Go registry export is built with the upstream audit
  off for the same reason. A full `just done` plan names more checks than either side here,
  and this lane must not run it.
- **The Python gate's plan face takes the record lock.** `--no-wait` was passed so a run
  refuses rather than queues behind another lane's gate; a refusal would have been recorded
  as `error` at exit 2. None occurred, and each run recorded a targeted run entry in the
  shared record.
- **Three candidates, one branch's history.** They exercise a Go module change, the gate's
  own suite, and an empty change. They do not exercise `documents`, the storybook steps, or
  the fork steps, which is where the two gates' selection rules are most likely to diverge.
- **A candidate's head is the checkout's HEAD.** Comparing a lane branch's commits needs that
  branch checked out, so a wider dual run is a sequence of checkouts, not one command.

## 6. What a retirement decision would still need

- An answer to Q1 in [`tillr-next-steps.md`](tillr-next-steps.md): who executes the in-process
  checks in `tillr-gate run` — a Go port, a shell-out, or nobody. Until then the Go gate
  cannot produce a `pass` for any real candidate, and this evidence cannot price a
  replacement.
- A dual run over candidates whose plans differ more, which needs a checkout per candidate
  head and a Python plan face that does not take the record lock.
- A comparison of the two gates' *execution* of the same check, which needs the in-process
  gap closed first.

## 7. Reproducing

The harness's own cases are in [`tools/tillr-gate/dualrun_test.go`](../../tools/tillr-gate/dualrun_test.go):
one candidate both gates agree on, one where the Python gate plans an extra kind and fails a
check, and one where an in-process check makes the Go verdict `error`. They run under
`go test ./...` in `tools/tillr-gate` and drive the built binary, so the harness's contract
with both gates is pinned rather than described.

The evidence run itself is the command in [Section 1](#1-what-was-run), with the registry
exported first by `python3 tools/fork-check.py --registry-json`. The report it wrote is
committed beside this document, so the classification can be read without re-running either
gate.
