---
title: "What a workflow on a store that does not land still lacks"
status: in-review
stage: DESIGN
updated: 2026-11-26
tags:
  - tillr
  - drain
  - workflows
  - judge
  - boundaries
  - store
  - design
summary: "Covers four workflow gaps reported on 2026-11-25 from a consumer's non-landing store: an independent judge stage before the completion hook, a stage skipped cleanly when its precondition is false, a hook that runs when a run dies to release external resources, and a before-and-after boundary check for a plain-directory lane. Separates what belongs in tillr's core from what belongs in the consumer's own programs for each gap, leaves OQ-NLW1 and OQ-NLW2 to the owner, records D-NLW1 to D-NLW5 as the designer's decisions, and ends in five core build items."
next: "Rule OQ-NLW1 (static skip outcome vs replacement-only conditional stages) and OQ-NLW2 (charter release-run hook vs consumer-only cleanup), review D-NLW1 to D-NLW5 in the Decision Ledger, and build the five core items of Section 7."
depends-on:
  - tillr-core-and-git-landing.md
  - tillr-separable-parts.md
  - tillr-judge-and-lifecycle.md
  - ../reference/tillr-item-graphs.md
  - ../reference/authority-not-approval.md
  - ../tillr/decision-provenance-review.md
vantage:
  status-chip: true
---

# What a workflow on a store that does not land still lacks

**Status:** Not built: `rg -n 'evaluated_digest|TakePlainBoundary|release-run|ExitSkipped' tools/` returns nothing, and no Go code, workflow schema, or CLI flag changes in this commit. Only the dated statements quoted in [§2](#2-governing-rulings-and-the-core-boundary-rule) are owner rulings; the 2026-11-25 observations in [§1](#1-the-2026-11-25-consumer-report-and-overview-of-the-four-gaps) come from a filed command-line request; [`OQ-NLW1`](#OQ-NLW1) and [`OQ-NLW2`](#OQ-NLW2) await the owner's ruling; and `D-NLW1` through `D-NLW5` in the [Decision Ledger](#decision-ledger) are the designer's decisions, which the owner may overturn ([decision provenance review §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening)).

> **In short.** A store whose charter sets `completes: "hook"` already runs multi-node workflows in plain directories and finishes items on a recorded verdict, yet four pieces of core workflow machinery still stop at `if l.nonLanding` or two-valued edges: `exit.evaluator_slot` is refused by the planner, stages have no clean precondition skip, dead runs reclaim lanes and retry before external resources are released, and plain-directory lanes take no before-and-after boundary snapshot even though `run-pi` relies on that snapshot as its sole confinement. Core queue, graph, verdict, and lane invariants belong in tillr's core; domain citation checks, domain precondition probes, external lock protocols, and OS-level host sandboxing belong in the consumer's own programs.

**Why it matters.** On 2026-11-25 a consumer running a non-landing tillr instance hit all four gaps at once: refusing `evaluator_slot` forced review onto an ordinary read-only node where two of five agent verdicts failed operator review (one right conclusion for a wrong reason, one bad citation nothing caught), two-valued edges could not skip inapplicable stages cleanly, dead runs left external leases dangling across retries, and plain-directory runs executed `run-pi` with zero filesystem boundary check.

**The shape.** State the four observations and the core-vs-consumer split in [§1](#1-the-2026-11-25-consumer-report-and-overview-of-the-four-gaps), ground the split in the owner's charter and item-graph rulings in [§2](#2-governing-rulings-and-the-core-boundary-rule), specify each gap's boundary and recommendation in [§3](#3-gap-1-an-independent-judge-stage-before-the-completion-hook) through [§6](#6-gap-4-a-boundary-check-for-a-plain-directory-lane), and end in [§7](#7-build-items-for-what-belongs-in-the-core) with five self-contained build items for the core.

**Cost.** Five additive core slices across `tools/tillr-drain`, `tools/tillr-item`, `tools/tillr-run`, and `tools/tillr/definition`, governed by the `D-CG3` safety rule so `git-landing` behaves identically.

**Start at:** [§1.2](#12-summary-matrix-core-vs-consumers-own-programs), then [§3](#3-gap-1-an-independent-judge-stage-before-the-completion-hook) through [§7](#7-build-items-for-what-belongs-in-the-core).

**Needs your ruling:** [`OQ-NLW1`](#OQ-NLW1) (whether workflows gain a finite `"skip"` outcome driven by registered command exit `77` or express conditional stages solely through `may_replace` proposals) and [`OQ-NLW2`](#OQ-NLW2) (whether `definition.Hooks` gains an idempotent `release-run` hook called by the drain before lane reclamation and retry, or leaves run-death cleanup wholly to consumer wrappers and lease TTLs).

**Awaits your review:** the five designer's decisions `D-NLW1` to `D-NLW5` in the [Decision Ledger](#decision-ledger). You may overturn any of them.

**Reads with:** [the core and the `git-landing` charter](./tillr-core-and-git-landing.md) (charter architecture and `complete-item`), [what tillr promises a consumer](./tillr-separable-parts.md) (stable CLI/record contract and consumer requests), [the item judge and lifecycle](./tillr-judge-and-lifecycle.md) (`CheckJudge`, `EvaluatorBinding`, and run endings), [item graphs](../reference/tillr-item-graphs.md) (workflow expansion, `evaluator_slot`, `node_artifacts`, and `D-IG9`), [authority, not approval](../reference/authority-not-approval.md) (judge independence), and [lane shims](./tillr-lane-shims.md) (preventive command boundaries).

**Words.** Item, node, run, session, seat, criterion, verdict, hold, and lane keep their meanings in [the vocabulary](./tillr-vocabulary.md#5-the-words); workflow, stage, graph, and evaluator slot keep their meanings in [item graphs](../reference/tillr-item-graphs.md#the-words); charter ("definition" in code), store that does not land, and completion hook (`complete-item`) keep their meanings in [the core and `git-landing`](./tillr-core-and-git-landing.md#the-words-this-document-uses); consumer keeps its meaning in [separable parts](./tillr-separable-parts.md#the-words-this-document-uses). Three terms are used with exact meanings here:

- **Plain-directory lane** *(coined here)*: a lane staged by `LaneStager.stagePlain` ([`plain_stage.go`](../../tools/tillr-drain/drain/plain_stage.go)) for a store whose charter sets `completes: "hook"`, placing the worker's target at `<lane-root>/<lane>/work` (`PlainDir`) with no Git worktree or base commit.
- **Evaluated digest** *(coined here)*: the caller-computed SHA-256 manifest digest (`sha256:<64-hex>`) over an attempt's accepted `node_artifacts` rows and `<lane>/<working_dir>` regular/symlink entries that binds an agent verdict on a non-landing store in place of Git's `evaluated_head` and `evaluated_tree`.
- **Stage precondition** *(coined here)*: a deterministic, side-effect-free test run before a stage's work to decide whether that stage applies to the current item (`0` = run, `77` = skip cleanly).

---

## 1. The 2026-11-25 consumer report and overview of the four gaps

### 1.1 What the consumer observed

Reported on 2026-11-25 from a consumer repository's non-landing tillr instance against the pack of 2026-11-24, re-read in this tree at commit `555c0414` on 2026-11-25, and still present at `93cfa137`:

1. **No independent evaluator before the completion hook.** `Plan` in [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) refuses any workflow with `exit.evaluator_slot` when `def.Completes != "landing"` (`"workflow-evaluator-requires-landing: evaluator_slot judges a Git candidate; use an ordinary agent node and the definition's completion hook instead"`). On the consumer's workaround — an ordinary `read_only` agent node followed by `complete-item` — the operator reviewed five agent verdicts and found one right conclusion reached for a wrong reason and one wrong citation that nothing caught.
2. **Edges branch only on `pass` or `fail`.** `WorkflowEdge.WhenOutcome` in [`tools/tillr-drain/workflow/workflow.go`](../../tools/tillr-drain/workflow/workflow.go) accepts only `"pass"`, `"fail"`, or omission (`workflow-outcome`), leaving no clean way to skip a stage whose precondition is false without either paying for an agent run or faking a pass/failure.
3. **No hook runs when a run dies.** None of the nine hooks in [`tools/tillr/definition/README.md`](../../tools/tillr/definition/README.md) fires when a run exits abnormally or is reaped after `SIGKILL`, leaving resources leased outside tillr held while the drain reclaims the lane and launches a replacement run.
4. **A plain-directory lane takes no before-and-after snapshot.** [`tools/tillr-run/backends/pi/run-pi`](../../tools/tillr-run/backends/pi/run-pi) states that nothing mechanically confines `pi` beyond the driver's `boundary.before`/`boundary.after` snapshot pair, yet `Loop.snapshot` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) returns `nil, nil` whenever `l.nonLanding` is true, and `settleNodeFormat` in [`tools/tillr-drain/drain/workflow.go`](../../tools/tillr-drain/drain/workflow.go) skips `read_only` target checks on non-landing stores.

### 1.2 Summary matrix: core vs. consumer's own programs

| Gap | Belongs in tillr's core (and why) | Belongs in the consumer's own programs (and why) | Recommendation | Owner ruling needed? |
| :--- | :--- | :--- | :--- | :--- |
| **1. Independent judge stage before `complete-item`** ([§3](#3-gap-1-an-independent-judge-stage-before-the-completion-hook)) | `exit.evaluator_slot` on `completes: "hook"` stores, `checkIndependence`, `EvaluatorBinding` + `judge_classes`, single-token locator check, `evaluated_digest` binding, `RefusalVerifyNotDone` return, and atomic verdict recording on hook acceptance — **because** judge independence and digest-bound verdict provenance are core integrity rules every charter shares | Deterministic registered `command` nodes before `evaluator_slot` that verify citations against domain corpora/targets, the domain reasoning rubric in the evaluator's prompt, and finished-state mapping in `complete-item` — **because** tillr knows artifact digests and criterion hashes, not external domain corpora or domain outcome states | [`D-NLW1`](#decision-ledger): lift `workflow-evaluator-requires-landing`, bind non-landing verdicts to `evaluated_digest`, run `evaluator_slot` after deterministic citation checks and before `complete-item`, and commit checked `done` verdicts only when `complete-item` exits `0` | None ([`OQ-PR17`](../tillr/decision-provenance-review.md#decision-ledger) and [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) ruled: open questions hold only dependent steps; [per-step question holds](./tillr-per-step-question-holds.md)) |
| **2. Stage skipped cleanly when precondition is false** ([§4](#4-gap-2-a-stage-that-is-skipped-cleanly-when-its-precondition-is-false)) | Scheduler support for `'skipped'` stages without leasing an agent backend, ordering-edge pass-through on `'skipped'`, `"optional": true` consumer inputs, and `requires_nodes` distinguishing `'skipped'` from `'failed'` — **because** only the core graph runner schedules nodes, writes `node_runs`, checks `node_artifacts`, and evaluates `WorkflowExit` | Computing the precondition itself inside a registered deterministic `command` (`config/tillr/commands.json`) exiting `0` (run) or `77` (skip) — **because** domain predicates belong in versioned commands, never in a workflow JSON expression language (`D-IG9`) | [`D-NLW2`](#decision-ledger): allow `skippable: true` stages gated by command exit `77`, `when_outcome: "skip"` edges, and `"optional": true` inputs, with zero edge expression DSL | **Yes:** [`OQ-NLW1`](#OQ-NLW1) (finite `"skip"` outcome vs `may_replace`-only) |
| **3. Hook when a run dies to release external state** ([§5](#5-gap-3-a-hook-that-runs-when-a-run-dies-for-releasing-something-held-outside-tillr)) | Charter-level `release-run` hook in `definition.Hooks` called synchronously by `tillr-drain` on reap/settlement *before* freeing the lane or launching `-restart`, holding on failure (`C4`), plus dead-PID reporting in `tillr run status` (`P6`) — **because** only the drain orders reap, lane reclamation, and retry on its store (`A3`) | Acquiring, recording (by `TILLR_RUN_ID`), and idempotently releasing the external lease inside `release-run`, kernel-tied handles (`flock`) for instant `SIGKILL` release of local locks, and server-side TTLs for permanent host loss — **because** tillr knows nothing of the external resource and no user-space hook runs at the instant of `SIGKILL` | [`D-NLW3`](#decision-ledger): add optional idempotent `release-run` to `definition.Hooks` gated by durable `drain.run-released`, hold as `run-release-failed` on error, and ship `P6` in `tillr-run` | **Yes:** [`OQ-NLW2`](#OQ-NLW2) (charter `release-run` hook vs consumer-only wrappers + TTLs) |
| **4. Boundary check for a plain-directory lane** ([§6](#6-gap-4-a-boundary-check-for-a-plain-directory-lane)) | Git-free `TakePlainBoundary` before/after snapshots over `<root>`, watched idle `<lane-root>` siblings, and `<lane>/<working_dir>` (for `"read_only"` and explicit `write_scope` stages) — **because** `stagePlain`, `access: "read_only"`, lane isolation, and `run-pi`'s snapshot boundary are tillr's own contracts | Preventive OS/container/mount sandboxing, network confinement, and host paths outside `<root>` and `<lane-root>` — **because** a user-space before/after walk is post-hoc and bounded to named directory trees, never the whole host filesystem or network | [`D-NLW4`](#decision-ledger): wire `TakePlainBoundary` into `Loop.snapshot`, `settleNodeFormat`, `settleHookGroup`, `judgeCandidate`, and `tillr-drain boundary` | None ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) ruled: open questions hold only dependent steps; [per-step question holds](./tillr-per-step-question-holds.md)) |

---

## 2. Governing rulings and the core boundary rule

Five settled rulings from other documents govern this design; none is re-ruled here:

1. **The core-and-charter decision rule (owner ruling, 2026-11-16, restated in [core and `git-landing` §1](./tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words), [`OQ-CG2`](./tillr-core-and-git-landing.md#decision-ledger), and [`OQ-CG5`](./tillr-core-and-git-landing.md#decision-ledger)).** Tillr is the general core of queues and graphs plus one charter ("definition" in code) per use; when a choice suits Git landing and chafes against another use, it belongs to each charter, while queue, graph, lease, and verdict protections every charter needs stay in the core ([core and `git-landing` §6](./tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before)). Under `D-CG3`, every core change must leave `git-landing` behaving identically, existing suites green, and older stores openable.
2. **A judge is a workflow step with its own rulebook, and never judges its own work (owner ruling, 2026-11-20, [`OQ-PR17`](../tillr/decision-provenance-review.md#decision-ledger) and [judge and lifecycle §6](./tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow)).** Quoted verbatim: *"It's not a temporary grant. It's not a thing that will change on a whim. It's encoded for a purpose. ... here is the flow. There is a judge at this point, and they judge based on this."* Delegated verdicts also require actor/session independence from every producer whose work is judged ([authority, not approval](../reference/authority-not-approval.md#a-judge-never-judges-its-own-work)).
3. **Finite item-graph bounds (confirmed by owner, 2026-11-20, [`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3) / [item graphs](../reference/tillr-item-graphs.md#why-its-this-way)).** `D-IG1`, `D-IG5`, and `D-IG9` keep workflows as finite, cycle-free graphs expanded from tracked files and registered shell-free commands, refusing cycles, edge expression languages, timers, undo hooks inside the graph, in-process nodes, and self-rewriting stages.
4. **Graph-change policy and outside effects (owner ruling, 2026-11-17, [separable parts §7.2](./tillr-separable-parts.md#72-graph-change); `C4` in [core and `git-landing` §6](./tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before)).** Once a node starts, it is fixed; unstarted parts of a graph are governed by charter policy within core invariants; and an outside effect a charter declares must block retry until reconciled (`C4`, Unit 5 of [core and `git-landing` §8.5](./tillr-core-and-git-landing.md#85-the-unbuilt-units)).
5. **Provenance and owner-only questions (owner rulings, 2026-11-19, [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger) and [`OQ-PR22`](../tillr/decision-provenance-review.md#decision-ledger)).** Quoted verbatim: *"A: it matches what you said on 2026-11-18, that you decide product direction and trade-offs and the agent decides engineering detail"*, and *"A: it keeps what you wanted, not being asked the same thing twice, and stops an agent's own leaning from counting as a principle."* Where Gap 2 and Gap 3 touch `D-IG9` or the charter hook surface, [`OQ-NLW1`](#OQ-NLW1) and [`OQ-NLW2`](#OQ-NLW2) leave those choices to the owner.

---

## 3. Gap 1: An independent judge stage before the completion hook

### 3.1 What today's code does, and why two of five verdicts failed silently

Today a non-landing store (`completes: "hook"`) cannot use tillr's judge pipeline:

1. **Planner refusal.** In `Plan` ([`plan.go`](../../tools/tillr-drain/drain/plan.go)), if `def.Completes != "landing"` and `evaluatorSlot(w) != ""`, workflow loading fails with `workflow-evaluator-requires-landing` (pinned by `TestHookCompletionRefusesEvaluatorWorkflow` in [`hook_completion_test.go`](../../tools/tillr-drain/drain/hook_completion_test.go)).
2. **Git-SHA coupling in `evaluatorCandidate` and `CheckJudge`.** `Loop.evaluatorCandidate` ([`workflow_evaluator.go`](../../tools/tillr-drain/drain/workflow_evaluator.go)) resolves `HEAD` with `l.revParse(ctx, c.Worktree, "HEAD")`, and `CheckJudge` ([`judge.go`](../../tools/tillr-item/item/judge.go)) plus `checkEvaluator` ([`evaluator.go`](../../tools/tillr-item/item/evaluator.go)) require `evaluated_head` and `evaluated_tree` to match `gitObjectSHA` (`40` or `64` lowercase hex characters).
3. **What the consumer's workaround lost.** Replacing `evaluator_slot` with an ordinary `read_only` agent stage followed by `complete-item` ([`hook_completion.go`](../../tools/tillr-drain/drain/hook_completion.go)) bypasses `judgeCandidate` and `CheckJudge` completely:
   - Neither `checkIndependence` (`verdict.go`, verifying disjoint run IDs and session IDs across all upstream `Producers`) nor `checkEvaluator` (`EvaluatorBinding` + `judge_classes` + criterion text hash) runs; to satisfy `tillr item state --require-verdicts`, a `complete-item` script has to stamp `actor_kind: "mechanical"` as [`tools/tillr/examples/recorded-verdict/hook.go`](../../tools/tillr/examples/recorded-verdict/hook.go) does.
   - Nothing checks that `evidence` entries are non-prose locators, nothing resumes the reviewer when its verdict JSON is malformed (`judgeCandidate` in [`judge.go`](../../tools/tillr-drain/drain/judge.go)), and a negative review cannot emit `RefusalVerifyNotDone` returning structured per-criterion `findings` to the producer (`settleHook` turns `complete-item` exit `1` into `RefusalNoOp`).
   - Without an enforced `command -> evaluator_slot -> complete-item` pipeline, the reviewer read unverified prose citations directly and graded conclusion polarity rather than step-by-step support — producing one verdict right for a wrong reason and one verdict carrying a fabricated or stale citation.

### 3.2 Core vs. consumer's own programs, and why

- **Core:** Supporting `exit.evaluator_slot` on `completes: "hook"` stores (`plan.go`, `workflow_evaluator.go`, `judge.go`, `hook_completion.go`, and `tools/tillr-item/item/`), binding agent verdicts to a caller-computed `evaluated_digest` over accepted producer outputs and plain-lane files instead of Git SHAs, enforcing `checkIndependence` across all `Producers` and `checkEvaluator` (`EvaluatorBinding`, `judge_classes`, criterion hash, single-token `evidence` locators), routing `not-done`, `blocked`, and `defective` before `complete-item` runs, and recording `done` verdicts atomically only when `complete-item` exits `0`.
  - *Why core:* Judge independence ([authority, not approval](../reference/authority-not-approval.md)), the workflow judging-step rulebook ([`OQ-PR17`](../tillr/decision-provenance-review.md#decision-ledger)), and digest-bound verdict provenance ([separable parts §6](./tillr-separable-parts.md#6-why-the-boundary-is-where-it-is)) are core queue-and-verdict invariants. Requiring a Git commit SHA in `CheckJudge` was a `git-landing` assumption wired into `tillr-item` and `tillr-drain` ([core and `git-landing` §3](./tillr-core-and-git-landing.md#3-where-the-git-landing-assumption-is-wired-in-today)).
- **Consumer's own programs:**
  1. *Citation verification:* A registered deterministic `command` stage (`config/tillr/commands.json`) placed between the producing stage(s) and `evaluator_slot` that parses producer artifacts, resolves every cited file, symbol, log span, or external finding ID against the consumer's corpus, and writes a typed `json` verification artifact wired into the evaluator's `inputs` (failing early or flagging unresolved citations mechanically before the evaluator model reads them).
  2. *Domain reasoning rubric and outcome mapping:* The evaluator stage's `prompt` (requiring each criterion's `reason` and `evidence` to verify the *derivation* as well as the conclusion, rejecting "right answer from invalid premise" as `not-done`), and the charter's `complete-item` hook mapping the judged artifact's domain conclusion onto the charter's finished states (`confirmed`, `rejected`, `inconclusive`, `repeat-of`).
  - *Why consumer:* Tillr's core validates locator *syntax* and `node_artifacts` SHA-256 digests, but only the consumer's tools know how to dereference domain citations against an external target repository, log store, or issue tracker, or which domain finished state a verified finding maps to.

### 3.3 Recommendation: digest-bound `evaluator_slot` before `complete-item` (`D-NLW1`)

*Designer's decision; the owner may overturn this.*

1. **Allow `exit.evaluator_slot` when `def.Completes != "landing"`.** Remove the `workflow-evaluator-requires-landing` guard in `plan.go`. All static `validateEvaluator` invariants (`access: "read_only"`, listed in `requires_nodes`, topologically after every `"write"` stage, non-empty `judge_classes` excluding `owner-only`, at most one per graph) apply unchanged.
2. **Caller-computed `evaluated_digest` for non-landing candidates.** Add `EvaluatedDigest string` (`json:"evaluated_digest,omitempty"`) to `JudgeOptions`, `JudgeOutput`, and `verdictPayload` (`tools/tillr-item/item/judge.go`, `verdict.go`, `evaluator.go`):
   - **Git candidates (`def.Completes == "landing"`):** unchanged — `Head` and `Tree` (`gitObjectSHA`) are required and `EvaluatedDigest` must be empty.
   - **Non-landing candidates (`def.Completes != "landing"`):** `Head` and `Tree` must be empty, and `EvaluatedDigest` must match `^sha256:[0-9a-f]{64}$` computed by `evaluatorCandidate` (`workflow_evaluator.go`) — never trusted from the model's file — as the SHA-256 of canonical newline-delimited UTF-8 records over:
     1. every accepted row in `node_artifacts` for `(item_id, attempt)` (resolving `node_reused` `source_attempt`), sorted by `(node_id, name)`: `artifact\t<node_id>\t<name>\t<type>\t<sha256>\n`; and
     2. every regular file and symlink under `<lane>/<working_dir>` (`PlainDir`), sorted by slash-normalized relative path: `work\t<rel_path>\t<mode>\t<sha256-or-link-target>\n`.
   - `stageJudge` injects `evaluated_digest` into the evaluator's brief in place of `evaluated_head` and `evaluated_tree`, and `CheckJudge` rejects mismatches as `evaluated-digest`.
   - Locator validation in `CheckJudge` additionally verifies that any `evidence` entry prefixed `artifact:<node>/<name>` names an actual accepted `node_artifacts` row of the attempt, and any `work:<rel_path>` entry names an existing path in the `<lane>/<working_dir>` manifest, refusing dangling local artifact/work citations at intake (`verdict-evidence`) and resuming the evaluator session once to fix form errors just as `judgeCandidate` does on Git lanes.
3. **Topology before `complete-item`, preserving `D-JL5`.** On a non-landing workflow with `evaluator_slot`:

   ```text
   [producer stage(s)] ──► [optional consumer citation-check command] ──► [evaluator_slot, read-only]
                                                                                   │ writes eval.output
                                     ┌─────────────────────────────────────────────┴────────────────────┐
                               outcome "fail"                                                     outcome "pass"
                        (complete-item is NOT called)                                        (checked judge.json saved)
                  • not-done  → RefusalVerifyNotDone returns findings to producer                       │
                  • blocked   → item moves to blocked on named waiter                                   ▼
                  • defective → verdict recorded, item closed                                    complete-item hook
                                                                                                 exits 0: record JudgeReceipt
                                                                                                 done verdicts + move state
   ```

   - When the evaluator ends `not-done`, `blocked`, or `defective`, `settleEvaluator` settles the outcome directly (`RefusalVerifyNotDone`, `ReasonJudgeBlocked`, or `ReasonJudgeDefective`) and **never** calls `complete-item`.
   - When the evaluator passes (`checked.Outcome == "pass"`), `judgeCandidate` writes the checked `JudgeReceipt` (`<run-dir>/judge.json`) without yet writing `done` rows to `verdicts`, so an unfinished attempt never freezes criterion text ([`D-JL5`](./tillr-judge-and-lifecycle.md#41-attempt-outcomes-are-not-criterion-verdicts-d-jl5)).
   - At workflow exit, `settleHook` (`hook_completion.go`) calls `complete-item` (`--item`, `--run`, `--store`, `--root`, keeping its CLI contract unchanged). When `complete-item` exits `0` naming `{state, reason, resolves_via}` and a checked `JudgeReceipt` exists for the attempt's `evaluator_slot`, the drain records `item.RecordJudge` from that receipt and moves the item to `answer.State` with `--require-verdicts` in one atomic store write (so `complete-item` no longer needs to fabricate `actor_kind: "mechanical"` verdicts when an `evaluator_slot` ran). When no `evaluator_slot` exists in the workflow, `complete-item` continues to record its own verdicts beforehand exactly as `examples/recorded-verdict/hook.py` does today.
   - **Rejected alternatives:**
     - Letting `complete-item` invoke an LLM judge itself — hides token spend, sessions, and `EvaluatorBinding` outside `tillr-run` and `tillr-view`, and cannot enforce `checkIndependence` against upstream `Producers`.
     - Recording `done` verdicts *before* calling `complete-item` — freezes criteria even if `complete-item` exits `1` or faults, violating `D-JL5`.

---

## 4. Gap 2: A stage that is skipped cleanly when its precondition is false

### 4.1 What today's code does, and why `pass`/`fail` edges cannot express a false precondition

Today static workflows have only two branch outcomes and treat every non-`0`/`1` command exit as a machinery fault:

1. **Two-valued `when_outcome`.** In `DecodeWorkflow` ([`tools/tillr-drain/workflow/workflow.go`](../../tools/tillr-drain/workflow/workflow.go)) and `workflow_check.go`, `when_outcome` accepts only `"pass"`, `"fail"`, or `""` (`workflow-outcome`).
2. **Command exit codes.** In `settleNodeFormat` ([`tools/tillr-drain/drain/workflow.go`](../../tools/tillr-drain/drain/workflow.go)), a `"command"` stage exiting `0` is `'passed'`, exiting `1` is `'handled-fail'` when a `when_outcome: "fail"` edge leaves it (else holding as `node-failed: <node>`), and **any other exit code** — including `77`, which `fixer-gate` in [`tools/tillr/definition/README.md`](../../tools/tillr/definition/README.md) already uses for a dropped check — marks `'fault'` (`command-fault`).
3. **Four failure modes when a consumer tries to skip stage `B` in `A -> B -> C`:**
   - *Wrapping the check inside agent stage `B`:* `tillr-drain` still claims a backend slot, mints a session, stages a brief, and pays for an LLM turn just for `B` to report nothing to do.
   - *Overloading `when_outcome: "fail"` on a probe `A`:* mislabels a normal false precondition as `'handled-fail'` in Observatory and `node_runs`, and leaves nowhere to route a *genuine* failure of `A`.
   - *Downstream join stall and missing artifacts:* if `C` has an edge `B -> C` (for example, `replay_trace -> analyze_trace -> summarize`) and `B` is bypassed, `C` never becomes ready because `B` is not `'passed'`, and if `C` declares `inputs` from `B`, `nodeInputPaths` ([`node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go)) refuses with `node-input-missing`.
   - *`exit.requires_nodes` Catch-22:* `workflowDone` ([`workflow_exit.go`](../../tools/tillr-drain/drain/workflow_exit.go)) requires every listed node to have `outcome == 'passed'`. Listing `B` fails workflow exit whenever `B` is skipped; omitting `B` lets the graph finish even when `B`'s precondition *was* true and `B` needed to pass.

### 4.2 Core vs. consumer's own programs, and why

- **Core:** Scheduling, recording, artifact-wiring, and exit evaluation for skipped stages in `tools/tillr-drain/workflow/` and `tools/tillr-drain/drain/` — recording `node_runs.outcome = 'skipped'` without launching an agent run, propagating skip readiness across ordering edges, permitting declared optional consumer inputs when their producer was `'skipped'`, and distinguishing `'skipped'` (precondition false) from `'failed'` (precondition true, stage ran and failed) in `requires_nodes`.
  - *Why core:* Only the core workflow scheduler decides which node is ready, leases backends, validates `node_artifacts`, and evaluates `WorkflowExit`. No consumer wrapper can skip an `"agent"` node without `tillr-drain` having already dispatched it, and returning exit `0` with dummy empty output files falsifies provenance in `node_artifacts`.
- **Consumer's own programs:** Computing the domain precondition itself as a registered, shell-free `"command"` entry in `config/tillr/commands.json` (`D-IG5`) that reads the item's staged inputs, upstream `node_artifacts`, or `body_ref` and exits `0` (precondition true) or `77` (precondition false).
  - *Why consumer:* Domain predicates ("does this finding include a core dump?", "did triage classify this item as needing browser replay?") belong in testable programs owned by the consumer. Putting predicate expressions onto workflow JSON edges would violate `D-IG9`'s ban on an edge expression language ([item graphs](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be)).

### 4.3 Recommendation: `skippable` command preconditions, `when_outcome: "skip"`, and optional inputs (`D-NLW2`, gated on [`OQ-NLW1`](#OQ-NLW1))

*Designer's decision; the owner may overturn this, and [`OQ-NLW1`](#OQ-NLW1) asks the owner whether static workflows gain this finite `"skip"` outcome (Option A, designer's leaning) or express conditional stages solely through `may_replace` proposals (Option B).*

Under [`OQ-NLW1`](#OQ-NLW1) **Option A** (`D-NLW2`):

1. **Command exit `77` (`ExitSkipped`) and stage `precondition` / `skippable`:**
   - A `"command"` stage setting `"skippable": true` treats exit `77` as `node_runs.outcome = 'skipped'` (emitting `drain.node-advanced` with `outcome: "skipped"`), exit `0` as `'passed'`, exit `1` as `'failed'` / `'handled-fail'`, and any other exit code as `'fault'`. On a stage without `"skippable": true`, exit `77` remains `'fault'` so an unexpected `77` never silently skips required work.
   - Any `"agent"` or `"command"` stage (except `exit.evaluator_slot`, which may never be skipped on a candidate exit) may also set `"precondition": {"command_ref": "<name>", "flags": {...}}` naming a `"read_only"` command in `config/tillr/commands.json`. Before claiming the stage's seat/backend, the drain runs that registered command (`tillr run exec` with backend `"command"`): exit `0` proceeds to stage and launch the node; exit `77` records `node_runs.outcome = 'skipped'` directly with zero agent launch and zero token spend; exit `1` or other marks `'failed'` / `'fault'`.
2. **Closed three-value `when_outcome` and join propagation (zero expression DSL):**
   - `when_outcome` accepts `"pass"`, `"fail"`, `"skip"`, or omission (`""`).
   - An edge with `when_outcome: "pass"` activates only when `from_node` is `'passed'`; `"fail"` only on `'failed'` / `'handled-fail'`; `"skip"` only on `'skipped'`; and an omitted `when_outcome` (plain ordering edge `B -> C`) is satisfied once `B` is either `'passed'` or `'skipped'`.
   - When a stage `B` has incoming `when_outcome` edges (for instance `probe --(when_outcome: "pass")--> B`) and every incoming edge to `B` is permanently eliminated because `probe` ended `'skipped'` (or took another exclusive branch), `B` itself transitions immediately to `node_runs.outcome = 'skipped'`, unblocking any plain ordering join `B -> C` downstream.
3. **Explicit `"optional": true` consumer inputs:**
   - A stage `inputs` entry may use the expanded object form `{"type": "json", "optional": true}` (keeping the string shorthand `"json"` equivalent to `optional: false`). `workflow check` refuses `optional: true` unless every feeding producer stage can be `'skipped'` (`skippable`, `precondition`, or gated behind a conditional edge).
   - When the feeding producer ended `'passed'`, `checkNodeOutputs` and `nodeInputPaths` still require the artifact file and verify its SHA-256. When the feeding producer ended `'skipped'`, `nodeInputPaths` omits the path without raising `node-input-missing`, and `workflowNodeBrief` renders `<port>: skipped (producer <node> precondition false)` so the consumer stage sees an explicit absence rather than guessing.
4. **`WorkflowExit.requires_nodes` semantics:**
   - A node in `requires_nodes` is satisfied by `'passed'`, or by `'skipped'` **if and only if** static validation proved that node is skippable (`skippable: true`, `precondition` set, or reachable only through conditional edges with an alternative path). If its precondition was true and the node ran and ended `'failed'`, `'refused'`, or `'fault'`, workflow exit holds the item (`node-failed: <node>`).

*(If the owner instead chooses **Option B** in [`OQ-NLW1`](#OQ-NLW1), static workflows keep two-valued `when_outcome`, and Core Build Item 5 is replaced by Unit 3 of [core and `git-landing` §8.5](./tillr-core-and-git-landing.md#85-the-unbuilt-units), allowing a `"command"` probe stage to emit a bounded `graph.proposal` that splices in or omits a tracked optional sub-workflow.)*

- **Rejected alternatives:**
  - Edge predicate expressions (`when: "inputs.foo.count > 0"`) — explicitly forbidden by `D-IG9`.
  - Treating exit `0` with missing outputs as `'skipped'` — turns accidental producer bugs (`RefusalNodeOutput`) into silent skips.

---

## 5. Gap 3: A hook that runs when a run dies, for releasing something held outside tillr

### 5.1 What today's code does, and the OS boundary on a dead run

Today tillr defines nine charter hooks (`gate`, `land`, `verdict`, `fixer-gate`, `replay`, `rolling`, `complete-item`, `after-completion`, and `detail` in [`tools/tillr/definition/README.md`](../../tools/tillr/definition/README.md)), none of which runs when a worker or command run terminates or dies.

Two facts from the code and Unix process semantics bound any honest design here:

1. **No user-space process runs at the instant of `SIGKILL` or host loss.**
   - On an *observed* termination (`normal-exit`, `error-exit`, `signal-exit`, `wall-timeout`, `repeating-call`), `tillr run exec` ([`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go)) outlives its child and writes `run.finish`.
   - On an *unobserved* death (`SIGKILL` to `tillr run exec`, OOM-kill, or loop/machine crash), nothing executes at the moment of death. Instead, `tillr-drain` discovers that the run's PID is gone on a subsequent pass via `/proc` (`liveRunProcessIDs` and `reapOrphanedRuns` in [`tools/tillr-drain/internal/procguard/orphan.go`](../../tools/tillr-drain/internal/procguard/orphan.go)), synthesizes an `interrupted` `run.Record` and `lane.released` (`laneReleaseReap`), and immediately schedules `-restart` or `-r1` on the next pass. Meanwhile, standalone `tillr run status` without `tillr-drain` does not yet detect a vanished PID (`P6` in [separable parts §3](./tillr-separable-parts.md#3-proposed-work)).
2. **Why an external event-log poller races `tillr-drain` (`A3`, `C4`).** A consumer running `tillr-drain` on its store cannot run a second dispatcher (`A3` in [separable parts §1.1](./tillr-separable-parts.md#11-now-or-queued-at-once)). If the consumer instead runs a sidecar polling `tillr view events --since` to release external testbeds, browser VMs, target locks, or API leases when a run dies:
   - `orphan.go` reaps the dead run, frees the lane, and launches the replacement run (`-restart`) in milliseconds — *before* the asynchronous event poller observes the death — so `-restart` either collides with the still-held external lock or has its newly acquired lock revoked late by the poller; and
   - if releasing the external resource fails (leaving the external target dirty or unreachable), an external poller cannot stop `tillr-drain` from dispatching retries, violating `C4` ([core and `git-landing` §6](./tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before): an uncertain outside effect blocks retry until reconciled).

### 5.2 Core vs. consumer's own programs, and why

- **Core:**
  1. *Synchronous charter `release-run` hook before lane reclamation and retry (gated on [`OQ-NLW2`](#OQ-NLW2) Option A):* Calling the charter's hook program whenever `tillr-drain` reaps a dead run (`orphan.go`) or settles a finished/aborted/interrupted run — **before** freeing the lane lease or scheduling a replacement/retry run — recording a durable receipt per `run_id`, and holding the item (`run-release-failed`) if the hook reports that external cleanup failed (`C4`).
  2. *Shared dead-PID detection in `tillr-run` (`P6` of [separable parts §3](./tillr-separable-parts.md#3-proposed-work)):* Moving `liveRunProcessIDs` into `tools/tillr-run/run/` so standalone `tillr run status` callers (stores that do not run `tillr-drain`) see `interrupted` / dead-process state directly before calling their own cleanup and `tillr run release`.
  - *Why core:* Only `tillr-drain` controls the barrier between "run `R` died" and "lane `L` is freed and replacement `R-restart` starts" on a drain-managed store. Placing `release-run` on the **charter** (`definition.Hooks`) rather than inside workflow JSON stages preserves `D-IG9` (no undo nodes or compensations in the item graph) and leaves `git-landing` untouched (`D-CG3`).
- **Consumer's own programs:**
  1. *External lease acquisition, tracking, and idempotent release:* Recording what a run acquired outside tillr (keyed by `TILLR_RUN_ID`, in `<lane>/durable/<run-id>/` or the consumer's external lease service) and releasing it idempotently inside the charter's `release-run` hook.
  2. *Kernel-tied local lifetimes and remote lease TTLs:* Holding any host-local lock via an OS-kernel handle (`flock(2)` on an inherited FD, Unix domain socket, or process group) so the kernel releases it instantaneously on `SIGKILL`, and putting a server-side TTL on remote leases so permanent loss of the `tillr-drain` host itself eventually expires the lease without manual intervention.
  - *Why consumer:* Tillr neither knows the external protocol nor can override OS process physics when a host powers off or a process receives `SIGKILL` while `tillr-drain` is stopped.

### 5.3 Recommendation: charter-level `release-run` hook before lane reclamation and retry, plus `P6` (`D-NLW3`, gated on [`OQ-NLW2`](#OQ-NLW2))

*Designer's decision; the owner may overturn this, and [`OQ-NLW2`](#OQ-NLW2) asks the owner whether `definition.Hooks` gains `release-run` (Option A, designer's leaning) or leaves cleanup wholly in consumer wrappers, kernel handles, and lease TTLs alongside `P6` (Option B).*

Under [`OQ-NLW2`](#OQ-NLW2) **Option A** (`D-NLW3`):

1. **Hook contract in `definition.Hooks` ([`tools/tillr/definition/README.md`](../../tools/tillr/definition/README.md)):**
   - Optional hook name `"release-run"`, validated at loop startup via `<argv> describe` alongside `complete-item` and `after-completion`. Charters that do not list `"release-run"` (including `git-landing`) incur zero subprocess calls.
   - Invoked outside every SQLite write transaction as:

     ```text
     <hooks.argv> release-run --item <id> --run <run-id> --node <node-id> --lane <lane-id> --end-cause <cause> --store <abs-db> --root <abs-root>
     ```

     with `TILLR_NON_LANDING=1` (when `def.Completes != "landing"`) and `TILLR_LANE_DIR=<abs-lane-dir>` in the environment so the hook can read any lease receipt the run wrote under `<lane-dir>/durable/<run-id>/`.
   - **Idempotence requirement and trigger:** Called once for every ended or reaped run on a charter declaring `"release-run"` — both abnormal/dead runs (`interrupted`, `lease-reclaimed`, `error-exit`, `signal-exit`, `wall-timeout`, `repeating-call`) and normal exits (in case a worker exited `0` having forgotten to release an external handle) — **before** `reapOrphanedRuns` (`orphan.go`), `restartInterruptedNode` (`workflow_failover.go`), or normal settlement frees the lane lease or schedules a successor/replacement run.
2. **Exit codes, durability, and `C4` hold on uncertain release:**
   - A durable event `drain.run-released` (`object_type: "run"`, `object_id: <run-id>`) records completed release. On loop recovery, if `drain.run-released` already exists for `<run-id>`, the drain skips calling `release-run` again; if the loop crashed mid-hook, it calls the idempotent hook again before freeing the lane.
   - **Exit `0`:** external resources held by `<run-id>` are released (or `<run-id>` held none); the drain appends `drain.run-released` and proceeds to free/reuse the lane and advance or retry the node.
   - **Exit `1` or any other non-zero exit:** release failed or external state is uncertain. Following `C4`, the drain does **not** append `drain.run-released`, keeps the lane quarantined (or withholds retry dispatch), and records `drain.item-fault` with reason `run-release-failed` naming `<run-id>` and stderr detail. `tillr drain retry` (or `clear-fault` after manual cleanup) re-runs `release-run` for `<run-id>` and unblocks the item once it exits `0`.
3. **Standalone `tillr run status` dead-PID check (`P6`):**
   - Extract `liveRunProcessIDs` from `tools/tillr-drain/drain/orphan.go` into `tools/tillr-run/run/` so `tillr run status` reports `interrupted` when a leased run's PID is gone with no `run.finish` event, giving drain-less `tillr run exec` consumers ([separable parts §3](./tillr-separable-parts.md#3-proposed-work) `P6`) the same liveness truth.
- **Rejected alternatives:**
  - Per-stage `on_death` or `undo` nodes inside workflow JSON — violates `D-IG9` and misses single-worker items.
  - Calling `release-run` only inside `tillr run exec` — never fires when `tillr run exec` itself dies via `SIGKILL` or OOM, which is the exact failure mode that leaks external locks.
  - Firing `release-run` asynchronously after freeing the lane — races `-restart` on the same item.

---

## 6. Gap 4: A boundary check for a plain-directory lane

### 6.1 What today's code does, and why plain lanes are unguarded

[`tools/tillr-run/backends/pi/run-pi`](../../tools/tillr-run/backends/pi/run-pi) states the backend contract plainly:

> `What is mechanically confining: NOTHING beyond the boundary snapshot. Pi offers no OS sandbox for its shell tool... it is why the driver's boundary.before/boundary.after pair, not this file, is Pi's boundary.`

On a non-landing store (`l.nonLanding == true`), three guards in `tools/tillr-drain/drain/` are bypassed at once:

1. **Host boundary bracket skipped.** `Loop.snapshot` ([`loop.go`](../../tools/tillr-drain/drain/loop.go)) begins with `if l.nonLanding { return nil, nil }`, because `TakeBoundary` ([`boundary.go`](../../tools/tillr-drain/internal/boundary/boundary.go)) shells out to `git worktree list`, `git for-each-ref`, and `git status --porcelain`. No `boundary.before.json` or `boundary.after.json` is written or compared.
2. **Per-node `read_only` check skipped.** `settleNodeFormat` ([`workflow.go`](../../tools/tillr-drain/drain/workflow.go)), `settleHookGroup` ([`hook_completion.go`](../../tools/tillr-drain/drain/hook_completion.go)), and `judgeCandidate` guard their `read_only` worktree checks behind `if !l.nonLanding`.
3. **Resulting exposure.** A plain-directory worker — even a `"read_only"` scout, a parallel reader batch, or an `"evaluator_slot"` judge — can overwrite `<root>/definition.json`, `<root>/hook.py`, `<root>/config/tillr/workflows/`, sibling lanes under `<lane-root>/lane-*/`, or `<lane>/work` itself, and the drain settles the run as clean.

### 6.2 Core vs. consumer's own programs, and why

- **Core:** A pure-Go, Git-free before-and-after filesystem snapshot (`TakePlainBoundary` in `tools/tillr-drain/drain/`) around every non-landing run and across every `"read_only"` and `write_scope`-constrained plain-lane node, covering `<root>`, watched idle sibling lanes under `<lane-root>`, and `<lane>/<working_dir>`.
  - *Why core:* `stagePlain` (`plain_stage.go`), `access: "read_only"`, `write_scope`, sibling-lane isolation, and `run-pi`'s reliance on `boundary.before`/`boundary.after` are tillr's own promises. Enforcing them over tillr's own directories requires zero Git commands and belongs in the core ([core and `git-landing` §8.1](./tillr-core-and-git-landing.md#81-what-a-store-that-does-not-land-needs-from-the-core)).
- **Consumer's own programs (and host environment):** Preventive OS-level confinement (Linux mount/PID/network namespaces, `bwrap`/containers, read-only bind mounts for external corpora), command shims ([lane shims](./tillr-lane-shims.md)), and protection of arbitrary host directories *outside* `<root>` and `<lane-root>`.
  - *Why consumer:* A user-space before-and-after directory snapshot is **detective** (checked after the process exits) and can walk only bounded, named directory roots — never `/` or `$HOME` across the whole machine — nor can it block network egress or undo writes to `/tmp` or external services.

### 6.3 Recommendation: `TakePlainBoundary` around runs and per-node plain target checks (`D-NLW4`)

*Designer's decision; the owner may overturn this.*

1. **Git-free `PlainBoundarySpec` and `TakePlainBoundary` (`tools/tillr-drain/drain/plain_boundary.go`, CLI `tillr-drain boundary --plain`):**
   - Walk watched trees with `os.Lstat` (never following symlinks; symlinks record `kind: "symlink"` and their `os.Readlink` target string; regular files record `kind: "regular"`, permission mode bits, byte size, and SHA-256 hex digest; directories record `kind: "dir"` and mode bits; FIFOs/sockets/devices record `kind: "special"`).
   - **Fail-closed error rule (matching `TakeBoundary` in [`boundary.go`](../../tools/tillr-drain/internal/boundary/boundary.go)):** An unreadable directory/file or exceeding a configured walk safety bound (default `100,000` entries or `2 GiB` hashed under `<root>` / watched lanes) returns an explicit error and **never** an empty snapshot that would compare clean.
   - **Watched host trees on `Loop.snapshot` when `l.nonLanding` is true:**
     1. `<root>` recursively, **excluding** `<root>/.tillr/loop/` (where run records, streams, and `artifacts/` are written during the bracket), the store SQLite path plus `-wal`, `-shm`, and `-journal` siblings (matching `BoundarySpec.Store`'s rule in `boundary.go`, because `tillr run exec` writes the store inside the bracket), and `<lane-root>` when `<lane-root>` is nested inside `<root>` (covered by rule 2). Any mutation to `<root>/definition.json`, `<root>/config/`, hook scripts, or consumer files under `<root>` produces a `Violation{Kind: "path", Where: root, Detail: ...}` and stops the loop with `StopHostBoundaryViolation`.
     2. `<lane-root>`: top-level entries under `<lane-root>` plus recursive walks of idle watched lanes (`spec.Lanes`), **excluding** this bracket's target lane(s) (`spec.Targets`, except staging metadata files `<lane>/brief.md` and `<lane>/session-id` which the worker may not rewrite) and active sibling lane directories (`spec.SiblingDirs`, whose own brackets answer for them, matching `loop.go`). Writes into `<lane>/durable/<run-id>/` and `<lane>/nodes/<run-id>/` of the active run are excused.
   - Persist `boundary.before.json` and `boundary.after.json` in `<root>/.tillr/loop/<run-id>/` on plain-directory runs just as Git lanes do.
2. **Per-node `"read_only"` and `write_scope` enforcement on `<lane>/<working_dir>` (`<lane>/work`):**
   - Before launching a non-landing node (or parallel reader batch in `claimParallel`, or `evaluator_slot` in `judgeCandidate`), snapshot `<lane>/<working_dir>` (`target_before.json` in `<run-dir>/`).
   - At settlement (`settleNodeFormat` in `workflow.go`, `settleHookGroup` in `hook_completion.go`, and `judgeCandidate` in `judge.go`):
     - If `effectiveAccess == "read_only"` and `<lane>/<working_dir>` has any path violation relative to `target_before.json`, mark the node (or parallel reader batch) `'refused'` and emit `RefusalFence` (`boundary-scope-escape` / `ReasonJudgeUnreadable` for an evaluator), restoring `<lane>/<working_dir>` from the pre-reader snapshot so a rogue reader never poisons a retained lane.
     - If `effectiveAccess == "write"` and the stage declared an explicit `write_scope` (relative path prefixes), check every changed path in `<lane>/<working_dir>` against `write_scope` and the common deny-list (`config/tillr/deny-list.json`), and refuse any symlink in `<lane>/<working_dir>` whose resolved target escapes `<lane>/<working_dir>`, holding escapes as `RefusalFence` (`boundary-scope-escape`).
- **Rejected alternatives:**
  - Initializing a throwaway Git repository inside `<root>` and `<lane>/work` just to reuse `git status` — violates the non-landing promise ([separable parts §1.1](./tillr-separable-parts.md#11-now-or-queued-at-once) `A1` and `examples/recorded-verdict` running with no Git repository) and interacts badly with `GIT_CEILING_DIRECTORIES`.
  - Checking `mtime` and `size` without SHA-256 — misses same-size in-place edits within the same filesystem timestamp quantum.

---

## 7. Build items for what belongs in the core

*Designer's sequencing decision (`D-NLW5`); the owner may overturn this.*

Each item below is scoped to tillr's core, carries its own tests in the same commit (plus `just pack-sync` whenever `tools/tillr*` changes), and obeys the `D-CG3` safety rule (`git-landing` behaves identically, existing suites pass unchanged, and older stores open and drain). Under [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger) and the 2026-11-27 ruling on [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) ([per-step question holds](./tillr-per-step-question-holds.md)), only Items 4 and 5 wait on [`OQ-NLW2`](#OQ-NLW2) and [`OQ-NLW1`](#OQ-NLW1); Items 1, 2, and 3 depend on neither question and build without waiting on [`OQ-NLW1`](#OQ-NLW1) or [`OQ-NLW2`](#OQ-NLW2) once per-step question holds land.

| # | Core build item | Gap & decisions | Depends on | Modules and files touched (`+ just pack-sync`) | Verification tests in the same commit |
| ---: | :--- | :--- | :--- | :--- | :--- |
| **1** | **Plain-directory host boundary bracket and per-node `read_only` / `write_scope` target check** | Gap 4 ([§6](#6-gap-4-a-boundary-check-for-a-plain-directory-lane), `D-NLW4`) | None ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) ruled 2026-11-27: open questions hold only dependent steps; [per-step question holds](./tillr-per-step-question-holds.md)) | `tools/tillr-drain/drain/plain_boundary.go` (new), `tools/tillr-drain/boundary.go`, `tools/tillr-drain/drain/loop.go` (`snapshot`), `workflow.go` (`settleNodeFormat`), `hook_completion.go` (`settleHookGroup`), `judge.go` | Unit and loop tests in `tools/tillr-drain/drain/plain_boundary_test.go`: (1) plain worker mutating `<root>/definition.json` or an idle sibling lane stops with `StopHostBoundaryViolation` while active `SiblingDirs`, `.tillr/loop/<run>/`, and SQLite WAL writes pass; (2) `"read_only"` single node and parallel reader group mutating `<lane>/work` are refused with `RefusalFence` and `<lane>/work` is restored; (3) `"write"` node escaping `write_scope` or planting an escaping symlink is refused; (4) unreadable root fails closed rather than returning an empty snapshot |
| **2** | **Digest-bound `exit.evaluator_slot` before `complete-item` on non-landing stores** | Gap 1 ([§3](#3-gap-1-an-independent-judge-stage-before-the-completion-hook), `D-NLW1`) | Item 1 (so `"read_only"` evaluator target confinement is active on plain lanes) | `tools/tillr-item/item/judge.go`, `verdict.go`, `evaluator.go`; `tools/tillr-drain/drain/plan.go`, `workflow_evaluator.go`, `judge.go`, `hook_completion.go`; `tools/tillr/examples/recorded-verdict/` | Tests in `tools/tillr-item/item/evaluator_test.go` and `tools/tillr-drain/drain/hook_completion_test.go`: (1) `CheckJudge` accepts `evaluated_digest` when `Head`/`Tree` are empty, enforces `checkIndependence` + `EvaluatorBinding` + `judge_classes`, and rejects mismatched `evaluated_digest` or dangling `artifact:` / `work:` locators; (2) `not-done` emits `RefusalVerifyNotDone` without calling `complete-item`; (3) `defective` closes and `blocked` blocks without calling `complete-item`; (4) `pass` calls `complete-item` and records checked `done` agent verdicts atomically only when `complete-item` exits `0` (`exit 1` leaves criteria unfrozen); (5) workflows without `evaluator_slot` behave identically to `examples/recorded-verdict` |
| **3** | **Shared dead-process liveness helper in `tillr-run` and `tillr run status` reporting (`P6`)** | Gap 3 substrate ([§5](#5-gap-3-a-hook-that-runs-when-a-run-dies-for-releasing-something-held-outside-tillr), `D-NLW3`, [separable parts `P6`](./tillr-separable-parts.md#3-proposed-work)) | None | `tools/tillr-run/run/` (shared `/proc` live-run scanner and `status` projection), `tools/tillr-drain/drain/orphan.go`, `why.go`, `pass.go` | Tests in `tools/tillr-run/run/` and `tools/tillr-drain/drain/`: spawn a stub `tillr run exec` child, terminate it with `SIGKILL` so no `run.finish` is written, and assert standalone `tillr run status` reports `interrupted` without `tillr-drain` running while `orphan.go` reaps identically |
| **4** | **Charter `release-run` hook before lane reclamation and retry, with `run-release-failed` hold** | Gap 3 ([§5](#5-gap-3-a-hook-that-runs-when-a-run-dies-for-releasing-something-held-outside-tillr), `D-NLW3`) | Item 3 and **owner ruling on [`OQ-NLW2`](#OQ-NLW2)** (Option A) | `tools/tillr/definition/definition.go` and `README.md`, `tools/tillr-drain/drain/steps.go`, `orphan.go`, `workflow.go`, `workflow_failover.go`, `loop.go`, `retries.go`, `tools/tillr-drain/holdrules/holdrules.go`, `tools/tillr-view/waiting.go` | Tests in `tools/tillr-drain/drain/`: (1) `describe` startup check validates `release-run` flags; (2) `SIGKILL`ed run reaped by `orphan.go` runs `release-run` *before* lane release and before `-restart` launches; (3) `drain.run-released` prevents duplicate calls after loop recovery; (4) `release-run` exit `1` holds item as `run-release-failed` without launching `-restart` until `tillr drain retry` sees `release-run` exit `0`; (5) `git-landing` (no `release-run` declared) spawns zero extra processes |
| **5** | **Skippable command preconditions (exit `77`), `when_outcome: "skip"`, and `"optional": true` consumer inputs** | Gap 2 ([§4](#4-gap-2-a-stage-that-is-skipped-cleanly-when-its-precondition-is-false), `D-NLW2`) | **Owner ruling on [`OQ-NLW1`](#OQ-NLW1)** (Option A) | `tools/tillr-drain/workflow/workflow.go`, `workflow_check.go`; `tools/tillr-drain/drain/workflow.go`, `node_outputs.go`, `workflow_exit.go`, `workflow_brief.go`; `tools/tillr-view/item_nodes.go` | Tests in `tools/tillr-drain/workflow/workflow_test.go` and `tools/tillr-drain/drain/command_loop_test.go`: (1) `skippable: true` command exiting `77` and stage `precondition` exiting `77` record `node_runs.outcome = 'skipped'` with zero agent runs, while exit `77` without `skippable` still faults; (2) `A -> B -> C` advances `C` cleanly when `B` is `'skipped'` and omits `"optional": true` inputs in `C`'s brief without `node-input-missing`; (3) `requires_nodes` passes when a skippable stage is `'skipped'` and holds as `node-failed` when its precondition was `0` and the stage exited `1`; (4) `evaluator_slot` cannot set `skippable` or `precondition` |

---

## Open Questions

1. 💬 **OQ-NLW1: How should a workflow skip a stage cleanly when its precondition is false?**

   On 2026-11-20 ([`OQ-PR3`](../tillr/decision-provenance-review.md#OQ-PR3)), you confirmed the item-graph model (`D-IG1`, `D-IG5`, `D-IG9`), which refuses edge expression languages and currently restricts `when_outcome` to `"pass"` and `"fail"`. On 2026-11-17 ([separable parts §7.2](./tillr-separable-parts.md#72-graph-change)), you also ruled that the core must not preclude a charter's graph policy inside core invariants. Today a workflow `A -> B -> C` cannot skip `B` when a deterministic check says `B` does not apply without either billing an agent turn on `B`, mislabeling the skip as a failure, stalling `C` on missing `node_artifacts`, or weakening `exit.requires_nodes`.

   - **A — Add a finite `"skip"` outcome to static graphs (`skippable: true` / `precondition` command exit `77`, `when_outcome: "skip"`, and `"optional": true` inputs), keeping zero expression language on edges.** A registered deterministic command (`config/tillr/commands.json`) computes the precondition and exits `77` when false (matching `fixer-gate` in [`tools/tillr/definition/README.md`](../../tools/tillr/definition/README.md)); the core records `node_runs.outcome = 'skipped'` with zero agent runs, satisfies plain downstream ordering edges, omits `"optional": true` inputs cleanly, and lets `requires_nodes` accept `'skipped'` on declared skippable stages while still holding on `'failed'`.
   - **B — Keep `when_outcome` strictly two-valued (`"pass"` / `"fail"`) and express conditional stages only through runtime `may_replace` graph proposals.** Static edges do not gain `"skip"` or `"optional": true` inputs; instead, a probe stage proposes replacing a downstream stage subtree with a smaller tracked workflow file when a stage is unneeded ([core and `git-landing` §8.5](./tillr-core-and-git-landing.md#85-the-unbuilt-units), Unit 3). Every combination of optional stages then needs its own tracked workflow template, and command probes must emit `graph.proposal` JSON.

   <!-- vantage: question id=OQ-NLW1 leaning="A: exit 77 on a registered command keeps domain predicates in testable programs with no edge expression language (honoring D-IG5 and D-IG9), avoids template combinatorics for simple optional stages, and distinguishes skipped from failed in requires_nodes." -->

   _Leaning:_ A: exit `77` on a registered command keeps domain predicates in testable programs with no edge expression language (honoring `D-IG5` and `D-IG9`), avoids template combinatorics for simple optional stages, and distinguishes `'skipped'` from `'failed'` in `requires_nodes`.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-NLW2: Should a charter's hook program (`definition.Hooks`) gain a `release-run` hook called by `tillr-drain` when a run ends or dies?**

   `D-IG9` ([item graphs](../reference/tillr-item-graphs.md#why-its-this-way)) keeps undo hooks and compensations outside the item graph, while `C4` ([core and `git-landing` §6](./tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before) and Unit 5 of [§8.5](./tillr-core-and-git-landing.md#85-the-unbuilt-units)) places outside effects in the charter and requires an uncertain outside effect to block retry until reconciled. When a run holding an external lock, VM, or testbed dies with `SIGKILL`, `orphan.go` reaps the run and launches `-restart` on the next pass, racing any external consumer process that tails `tillr view events --since`.

   - **A — Add an optional, idempotent `release-run` hook to `definition.Hooks` (outside workflow JSON), called by `tillr-drain` before freeing the lane or launching a retry, plus `P6` (`tillr run status` dead-PID reporting).** When a charter declares `"release-run"`, the drain calls it on reap and settlement before reclaiming the lane or dispatching `-restart`; exit `0` records `drain.run-released`, and a non-zero exit holds the item as `run-release-failed` (`C4`) until reconciled. Workflow graphs remain free of undo nodes (`D-IG9`), and `git-landing` is untouched (`D-CG3`).
   - **B — Add no `release-run` hook to `definition.Hooks`; ship only `P6` (`tillr run status` dead-PID reporting) and leave external resource release wholly to consumer adapter wrappers, OS-kernel handles, and remote lease TTLs.** Keeps `tillr-drain` completely unaware of per-run external leases, accepting that on a `tillr-drain`-managed store an `interrupted` (`SIGKILL`) run's `-restart` replacement can start before an external event-log watcher releases the dead run's remote lock.

   <!-- vantage: question id=OQ-NLW2 leaning="A: putting release-run on the charter hook program (outside workflow JSON) respects D-IG9 while closing the orphan-reap-to-retry race that no external event poller can close on a single-dispatcher store, and honors C4 when release fails." -->

   _Leaning:_ A: putting `release-run` on the charter hook program (outside workflow JSON) respects `D-IG9` while closing the orphan-reap-to-retry race that no external event poller can close on a single-dispatcher store, and honors `C4` when release fails.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Every row in this ledger is the designer's decision, written in response to the 2026-11-25 filed command-line request without new words from the owner, and the owner may overturn any of them ([`D-PR2`](../tillr/decision-provenance-review.md#decision-ledger)). Inherited owner rulings remain owned by their home ledgers and are cited in [§2](#2-governing-rulings-and-the-core-boundary-rule).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-NLW1` | **Designer's decision; the owner may overturn this.** Remove `workflow-evaluator-requires-landing` in `plan.go`; support `exit.evaluator_slot` on `completes: "hook"` stores by binding `CheckJudge` and `checkEvaluator` to a caller-computed `evaluated_digest` (`sha256:<64-hex>` over canonical `node_artifacts` rows and `<lane>/<working_dir>` entries, mutually exclusive with Git `evaluated_head`/`evaluated_tree`), validating `artifact:` and `work:` locators, settling `not-done`/`blocked`/`defective` before `complete-item`, and recording checked `done` verdicts atomically only when `complete-item` exits `0` (`D-JL5`). Domain citation resolution against external corpora stays in registered consumer `command` stages before `evaluator_slot`. **Rejected:** LLM judging inside `complete-item`; recording `done` verdicts before `complete-item` accepts. | 2026-11-26, designer | [§3.2](#32-core-vs-consumers-own-programs-and-why), [§3.3](#33-recommendation-digest-bound-evaluator_slot-before-complete-item-d-nlw1) | No |
| `D-NLW2` | **Designer's decision; the owner may overturn this.** Subject to [`OQ-NLW1`](#OQ-NLW1) Option A, add clean precondition skip support to the core scheduler without an edge expression language: registered `"command"` exit `77` (`skippable: true` or stage `precondition`) records `node_runs.outcome = 'skipped'` with zero agent launch, `when_outcome` accepts `"skip"` while plain ordering edges pass across `'skipped'`, consumer `inputs` accept `"optional": true` when fed by skippable producers, and `WorkflowExit.requires_nodes` accepts `'skipped'` only on statically skippable non-evaluator stages while still holding on `'failed'`. Domain precondition logic lives solely in registered consumer commands (`config/tillr/commands.json`). **Rejected:** edge predicate DSLs; treating exit `0` with missing outputs as `'skipped'`. | 2026-11-26, designer | [§4.2](#42-core-vs-consumers-own-programs-and-why), [§4.3](#43-recommendation-skippable-command-preconditions-when_outcome-skip-and-optional-inputs-d-nlw2-gated-on-oq-nlw1), [`OQ-NLW1`](#OQ-NLW1) | No |
| `D-NLW3` | **Designer's decision; the owner may overturn this.** Subject to [`OQ-NLW2`](#OQ-NLW2) Option A, add an optional idempotent `release-run` hook to `definition.Hooks` invoked by `tillr-drain` outside store transactions on run settlement and `orphan.go` reap *before* freeing the lane or launching `-restart`/`-r1`, gated by durable `drain.run-released` and holding as `run-release-failed` on non-zero exit (`C4`), alongside extracting `liveRunProcessIDs` into `tools/tillr-run/run/` for `tillr run status` (`P6`). External lease protocols, kernel-bound local handles (`flock`), and remote lease TTLs remain in the consumer's programs. **Rejected:** workflow undo nodes (`D-IG9`); cleanup only inside `tillr run exec` (missed on `SIGKILL`); asynchronous post-reap cleanup (races `-restart`). | 2026-11-26, designer | [§5.2](#52-core-vs-consumers-own-programs-and-why), [§5.3](#53-recommendation-charter-level-release-run-hook-before-lane-reclamation-and-retry-plus-p6-d-nlw3-gated-on-oq-nlw2), [`OQ-NLW2`](#OQ-NLW2) | No |
| `D-NLW4` | **Designer's decision; the owner may overturn this.** Add pure-Go, symlink-no-follow, SHA-256 `TakePlainBoundary` (`plain_boundary.go` and `tillr-drain boundary --plain`) failing closed on read/bound errors; run it in `Loop.snapshot` over `<root>` (excluding `.tillr/loop/` and SQLite store/WAL files) and watched idle lanes under `<lane-root>` (excluding active `SiblingDirs`), and enforce per-node `<lane>/<working_dir>` snapshots on `"read_only"` nodes (restoring `<lane>/<working_dir>` on `RefusalFence`) and explicit `write_scope` writers. Preventive OS/container/network sandboxing outside `<root>` and `<lane-root>` stays in the consumer's host environment. **Rejected:** initializing synthetic Git repositories in plain lanes; `mtime`-only checks without SHA-256. | 2026-11-26, designer | [§6.2](#62-core-vs-consumers-own-programs-and-why), [§6.3](#63-recommendation-takeplainboundary-around-runs-and-per-node-plain-target-checks-d-nlw4) | No |
| `D-NLW5` | **Designer's decision; the owner may overturn this.** Sequence the core work as the five self-contained build items of [§7](#7-build-items-for-what-belongs-in-the-core): (1) plain-directory boundary check (`D-NLW4`), (2) non-landing `evaluator_slot` before `complete-item` (`D-NLW1`), (3) shared dead-PID liveness in `tillr-run` (`P6`), (4) charter `release-run` hook (`D-NLW3`, after [`OQ-NLW2`](#OQ-NLW2)), and (5) skippable command preconditions and optional inputs (`D-NLW2`, after [`OQ-NLW1`](#OQ-NLW1)), keeping `git-landing` invariant under `D-CG3`. | 2026-11-26, designer | [§7](#7-build-items-for-what-belongs-in-the-core) | No |
