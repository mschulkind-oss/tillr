---
title: "Roadmap — tillr"
status: accepted
tags: [roadmap, tillr, drain, gate, observatory]
summary: "The unfinished work on tillr, in the order it should be done and why: a clone that builds and tests, a core that carries only its own code, a gate that names what failed, and the designs behind them."
---

# Roadmap — tillr

**Reconciled:** 2026-11-24, against the tree and `uvx vantage-check index --roadmap docs/tillr/roadmap.md`.

This page holds unfinished work and its order. Each linked design owns its own state, next step
and questions; work with no design has its only home here, with its outcome, reason and
dependencies written under the entry. What is built is described by the references under
[`docs/reference/`](../reference/) and by each module's README, and what changed is in Git history.
The [repository roadmap](../roadmap.md) points here.

Three words recur. A **seat** is a role in an item's workflow (design, build, review), which the
backend policy maps to a model. The **drain** is the loop that plans, runs, gates and lands queued
items ([`tools/tillr-drain`](../../tools/tillr-drain/)). The **gate** is the set of checks a change
passes before and after it lands ([`tools/tillr-gate`](../../tools/tillr-gate/)).

## Ordering basis

The first rule that separates two entries decides. Document order is priority order.

1. **A decision only the owner can make comes before the work it blocks**, because nobody else can
   make it. The decisions are collected under
   [Open decisions that block entries](#open-decisions-that-block-entries).
2. **A defect that stops the running loop, or leaves the trunk red, outranks any build.**
3. **A substrate outranks what sits on it.** A clone that cannot build or test is the lowest
   substrate there is, so that work is first.
4. **Ties break toward the smaller change.**

## 1. A clone that builds and tests

First by rule 3: every other entry is built and verified by someone who starts from a clone.

1. **Establish the first proved commit of a fresh clone.** The README's build command,
   `just tillr-build`, builds from `refs/tillr/green`, the newest commit the full suite has passed
   on. A fresh clone has no such ref, so the documented build fails until the reader sets
   `TILLR_BUILD_UNPROVED`.
   - **Outcome:** a reader who clones the repository and follows the README gets a built tillr
     and a way to run its suites, with no ref created by hand.
   - **Depends on:** [the decision on how the first proved commit is established](#open-decisions-that-block-entries).
   - **Where:** [`README.md`](../../README.md), [`tests/tillr-build.test.py`](../../tests/tillr-build.test.py),
     [the build-commit reference](../reference/tillr-build-commit.md).

2. **Make the test suites independent of the machine.** Second because a suite that is red on a
   clean machine hides every real failure behind it.
   - **Outcome:** every suite passes on a clean machine and in continuous integration, with no
     dependence on the length of a directory path, on a program outside the tree, or on timing.
   - **Known failures:**
     - Unix socket paths that exceed the kernel's limit when the scratch directory path is long:
       the `TestWaitForTrigger*` tests, `TestRefreshSessionHeartbeatDoesNotWakeTrigger`,
       `TestDrainNotifyAndNoNotifyParityUnderSteppedClock` and `TestItemRequestToLoopWakeLatency`
       in [`trigger_notify_test.go`](../../tools/tillr-drain/drain/trigger_notify_test.go).
     - Two tests that need a fork build program the tree does not have:
       `TestForkBuildDeliveryAndSuccessiveLaneBases` and
       `TestPostLandingPiRollMovedBranchBuildsNewTipAndWorkerRecordsKey`. Entry 4 removes them
       with the code they cover.
     - [`definition_startup_test.go`](../../tools/tillr-drain/drain/definition_startup_test.go)
       puts a failing `git` stand-in on `PATH` before the shared drain binary is built, so the
       build of that binary fails when this test runs first.
     - [`detached_gate_restart_test.go`](../../tools/tillr-drain/drain/detached_gate_restart_test.go)
       races its gate fixture for a process-id file.
     - [`wake_test.go`](../../tools/tillr-watch/wake_test.go) gives a wake three seconds
       (`time.After(3 * time.Second)`), which a loaded machine exceeds.
   - **Depends on:** nothing.

## 2. A core that carries only its own code

Second by rule 3. tillr's core still handles two things that belong to no repository that adopts
it: patch-series forks of an outside tool, and the report format of a judging program that no
longer exists. Both are removed, and the first needs a seam to move behind.

3. **Let a repository declare its own landing steps and item kinds.** The only extension seam
   today is a declared gate check: one JSON file under [`tools/gate/checks/`](../../tools/gate/checks/).
   A repository cannot declare a step that runs as part of a landing, or a kind of item with its
   own handling, so anything of that sort has to be written into the lander and the drain.
   - **Outcome:** a repository adds a landing step or an item kind by declaring it in its own
     files, and tillr's core names none of them.
   - **Next step:** a design. None exists. [The core and the landing charter](../design/tillr-core-and-git-landing.md)
     and the unbuilt item kinds of [the idea pipeline](../design/tillr-idea-pipeline.md) are its
     starting points.
   - **Depends on:** nothing. Entry 4 depends on it.

4. **Remove fork handling from the lander and the drain.** The core carries handling for
   patch-series forks of an outside tool that this repository does not own, and it calls a program,
   `bin/pi-forks`, that is not in the tree (`fd pi-forks bin` returns nothing).
   - **Outcome:** none of it remains. A repository that needs such a step declares it through
     entry 3.
   - **Where:**
     - The lander: `fork_rebase.go`, `fork_pin.go` and `fork_cache.go` in
       [`tools/tillr-land/land/`](../../tools/tillr-land/land/), and the check name `fork-build`
       that `landing.go` and `fork_rebase.go` treat specially.
     - The drain: [`fork-gate.py`](../../tools/tillr-drain/fork-gate.py),
       [`held_fork.go`](../../tools/tillr-drain/remotesync/held_fork.go), and the `.forks/lanes/`
       paths that [`workflow_arguments.go`](../../tools/tillr-drain/workflow/workflow_arguments.go)
       rewrites.
     - The roll tool: the `--pi` option of [`tillr-roll.py`](../../tools/tillr-roll/tillr-roll.py).
   - **Depends on:** entry 3.

5. **Remove the drain's rolling-program protocol.** The drain still parses the report format of a
   judging program that has been retired. Declared checks are the protocol now, and two readers of
   one answer disagree: `goAffectedCheckID` in the drain's `trunk.go` is
   `go-affected/affected-tillr-go-test-packages-pass`, which does not match the declared check id
   `go-affected` ([`go-affected.json`](../../tools/gate/checks/go-affected.json)).
   - **Outcome:** declared checks are the only protocol, and replay scoping recognizes the
     declared `go-affected` id.
   - **Where:** [`fork_check_triage.go`](../../tools/tillr-drain/remotesync/fork_check_triage.go),
     [`legacy_watches.go`](../../tools/tillr-drain/drain/legacy_watches.go),
     `heavy_legacy_progress.go` and `heavy_legacy_progress.py` in
     [`tools/tillr-drain/heavyrun/`](../../tools/tillr-drain/heavyrun/), `runHeavyLegacy` in
     [`heavy_steps.go`](../../tools/tillr-drain/drain/heavy_steps.go), and the `go-tool/`,
     `go-substrate/` and `gate-runner/` id prefixes in
     [`replay_go_affected.go`](../../tools/tillr-drain/drain/replay_go_affected.go).
   - **Depends on:** nothing. It is behind entry 4 only because both edit the drain's
     heavy-check code and the fork removal deletes more of it.

6. **Give the pi run adapter a compatibility mode.** The adapter
   ([`tools/tillr-run/backends/pi/`](../../tools/tillr-run/backends/pi/)) is written for a pi that
   can leave a tool call unanswered and answer it when the session is opened again (a *parked*
   tool call), and that prints its feature names for `pi --features`. Stock pi does neither.
   The drain's `probePiEndingCapability` already asks for the features and falls back to the
   report footer, without saying so to the person who started it.
   - **Outcome:** the adapter probes `pi --features`; where a required feature is missing it
     refuses with a message that names the feature; and a compatibility mode, chosen
     deliberately, runs without those features and states what is reduced.
   - **Also:** [`build-agent-dir`](../../tools/tillr-run/backends/pi/build-agent-dir), which
     builds the directory pi loads its extensions from, reads three things this repository does
     not have: `yolo-packs/matt/pack.json`, the extensions under `yolo-packs/matt/files/extensions`,
     and `bin/pi-forks.list`. It raises `missing yolo-packs/matt/pack.json on trunk` without the
     first. The outcome includes an agent directory built from what the tree holds.
   - **Depends on:** entry 4 for the fork paths the builder reads.
     [The landing call](../design/tillr-landing-call.md) owns the call form the features serve.

## 3. A gate that names what failed

Third by rule 2: each of these is a place where a red check costs more than the fault behind it.

7. **Attribute a red Go suite to its failing tests.** A declared Go suite (the `go-test-*.json`
   files under [`tools/gate/checks/`](../../tools/gate/checks/)) reports one verdict for a whole
   module.
   - **Outcome:** a red suite names the tests that failed, so a repair item and a replay can
     target them.
   - **Depends on:** nothing.

8. **Replay a change-scope check.** A *change-scope check* compares two commits and so has no
   verdict on a single revision; `tillr-loop replay --only <id>` refuses one for that reason
   ([`tillr-loop`](../../tools/tillr-loop/tillr-loop)).
   - **Outcome:** a red change-scope check can be replayed on the base, so a failure that is
     already red there is acquitted instead of charged to the change.
   - **Depends on:** nothing.

9. **Say why a fixer's named check could not be judged.** When a named heavy check cannot be
   judged, for example because a tool it declares is missing, the gate log says only
   `could not judge`.
   - **Outcome:** the log names the check, the cause and what would make it judgeable.
   - **Depends on:** nothing.

10. [The gate](../reference/gate.md): finish the single classifier of check results and the
    bounded runtime of affected tests. After entries 7 to 9 because those are defects and this is
    a build.

11. [Gate test capacity](../design/gate-test-capacity.md): how many test processes run at once
    and which class of work gets the processors first. Behind its four open questions.

12. Landing velocity: a landing tier that runs before the trunk moves and a rolling tier that runs
    on the trunk, built in shadow first. It has no design document; the gate it would change is
    described in [the gate reference](../reference/gate.md).

13. [A failure is diagnosed, not waited out](../design/failure-diagnosed-not-waited-out.md): a
    failure is fingerprinted from its own text, and a deterministic one blocks its items on one
    diagnosis ticket. After landing velocity because it reads that tier's results.

14. [The trunk fixer's gate](../design/trunk-fixer-gate.md): the fixer gets the gate any candidate
    gets plus the checks it names. Its fork-release reporting leaves with entry 4.

## 4. A drain that runs unattended

Fourth by rule 2. Each design here removes a way one item's trouble stops the loop or reaches a
person who did not need to be asked.

15. [Why the drain cannot yet run a night alone](../design/unattended-drain.md): eight structural
    gaps, each with a rule and a permanent scenario. First because the others fill in its rules.
    Its next step is its implementation plan.

16. [Preventative command shims and run boundaries for workers](../design/tillr-lane-shims.md):
    a worker is stopped before it kills another lane's process, moves a shared ref or runs a
    whole module suite, not after.

17. [One evidence registry, and no whole-suite runs in lanes](../design/tillr-evidence-registry-and-suite-guardrail.md)
    ([plan sketch](../design/tillr-evidence-registry-and-suite-guardrail-plan.md)): the gate, the
    trunk proof and a worker's brief trust one recorded result instead of running a check again.

18. [Refused pushes](../design/tillr-refused-push.md): when the remote branch has moved, replay
    unpublished commits onto it instead of holding every later publication.

19. [Shared-path serialization](../design/tillr-shared-path-serialization.md): two items that
    edit the same file stop meeting as a squash conflict at landing.

20. [A check on a designer's answer to a conflict](../design/tillr-conflict-answer-check.md): an
    answer that names a symbol or key the tree does not have is caught before it lands.

21. [A probe for a closed backend](../design/tillr-closed-backend-probe.md): one failed run stops
    closing a backend for every seat until a person probes it.

22. [What the operator settles is settled inside tillr](../design/operator-decisions-inside-tillr.md):
    each kind of hand action gets a taker inside tillr. Ten of its slices wait on no question.

23. [Convergence](../design/tillr-convergence.md): a cold machine rebuilds the queue from the
    documents, drains it and shows every stopped item with the act that moves it.

24. [Every fallback in the drain, the run adapters and the gate](../design/tillr-fallback-inventory.md):
    each path that runs because the intended one failed is removed with a loud failure, or kept
    on the owner's acceptance. Behind its eight questions.

25. [The judge and the item lifecycle](../design/tillr-judge-and-lifecycle.md): remove the
    automatic move of a failed run to another model, and build the `delivery` obstacle.

26. [The escalation ladder](../design/escalation-ladder.md): a worker whose plan disagrees with
    the tree records the conflict and it is routed to a seat with authority to answer.

27. [Storage](../design/tillr-storage.md): fix the store's usage faults, then compare today's
    store with the proposed structures on one workload. Last here because its trials decide
    whether any change is worth making.

## 5. Items: how work enters, is sized and is repaired

Fifth by rule 3: the item model is the substrate of sections 6 and 7.

28. [The item graph is the whole path](../design/tillr-graph-is-the-path.md) and
    [the vocabulary](../design/tillr-vocabulary.md): every act on an item is a node of its graph,
    and worktrees and scratch belong to items. First because the entries below assume it.

29. [Scheduling and notifications](../design/tillr-scheduling-and-notifications.md): the
    scheduling pass decides only which ready node runs next. Six slices, in order.

30. [An oversized item lands in parts](../design/tillr-item-split.md): the accepted parts of a
    change land without the part objected to.

31. [A request that is hard to deliver is amended once](../design/tillr-request-amendment.md):
    the design seat writes an amendment beside the saved words. Eight slices wait on no question.

32. [Criterion repair](../design/criterion-repair.md): a criterion that was wrong when written is
    superseded with a reason, instead of closing its item.

33. [A design with no open questions moves straight to planning](../design/tillr-design-to-planning-transition.md):
    five slices wait on neither of its questions.

34. [The queue mirrors the roadmap's outstanding designs](../design/tillr-queue-mirrors-designs.md):
    every unfinished design a roadmap routes has exactly one item in the queue, and build steps
    live in a companion plan.

35. [Early design landing](../design/tillr-early-design-landing.md): a question and the design
    it is about land ahead of the item that raised them.

36. [The idea pipeline](../design/tillr-idea-pipeline.md): item kinds, the typed lineage edge and
    the lineage view. Its item kinds wait on entry 3.

37. [Work reaches tillr through several doors](../design/tillr-intake-paths.md): each way work
    enters runs its own small graph and ends in one item shape.

38. [Provisional design documents](../design/provisional-design-docs.md): capture a prompt and
    its first questions on the trunk before the worked design pass.

39. [Scheduled tasks](../design/scheduled-tasks.md): a due time and a recurrence on an item.
    Settled and unbuilt; the substrate of the goal monitor in the [Icebox](#icebox).

## 6. Review before landing

Sixth: no agent reads a change before tillr lands it, and these designs add that reading. Behind
section 5 because a review returns to an item through the graph that section builds.

40. [A review before every code landing](../design/tillr-diff-review.md): an agent that did not
    write the change reads the diff as it will land.

41. [One compliance check on what an item is about to commit](../design/tillr-design-document-check.md):
    a cheap agent holds a commit to written policies, never to logic.

42. [Review and compliance routing by kind of file](../design/tillr-review-rules.md): which
    changed files trigger entries 40 and 41, and which rulebook each is checked against.

43. [Review comments on a design](../design/tillr-review-comments.md): the fast track that sends
    a comment to the session that wrote the document.

## 7. Seats and models

Seventh because it lowers the cost of every run and stops nothing when it is missing.

44. [Who chooses an item's seat, and what the plan step is for](../design/tillr-plan-step-and-seat-selection.md):
    replaces the upfront plan step and the restart after a failed verify.

45. [What a designer reads itself and what goes to scouts](../design/tillr-designer-scouts.md):
    design and plan sessions use cheap scouts for search.

46. [Planning and model tiering](../design/tillr-planning-node-model-tiering.md): run more build
    items through the planned workflow so its cost can be read. After entries 44 and 45, which
    change that workflow.

47. [Seat profiles and one-command switching](../design/tillr-seat-profiles.md) and
    [live changes to drain capacity](../design/tillr-live-drain-config.md)
    ([plan sketch](../design/tillr-live-drain-config-plan.md)): named policies an operator
    switches while the drain runs.

48. [The `claude` run adapter](../design/tillr-claude-backend.md) and
    [the landing call](../design/tillr-landing-call.md): the adapter's call-suspending ending
    is the landing call's next build step.

49. [Replaying recorded tasks against several models](../design/model-benchmarks-from-drain-records.md):
    its first step, copying the shortlisted task records, runs no model.

## 8. Watching and alerts

Eighth: these report what sections 3 and 4 repair.

50. [The alert workflow](../design/tillr-watch-alert-workflow.md), then
    [the alert triage seat](../design/tillr-alert-triage-seat.md): every alert gets an
    investigation and one recorded outcome, and triage runs in a seat inside tillr. The triage
    slices are chained behind the workflow's.

51. [The watch rules not yet shipped](../design/tillr-watch.md): the designed rules that have no
    file under [`config/tillr/watch/`](../../config/tillr/watch/).

52. [Read-face timing](../design/tillr-read-face-timing.md): a slow read command is measured and
    alerted on without the read writing to the store.

53. [Session recording](../design/session-recording.md): its second and third slices.

54. [A watch any agent can use without tillr](../design/general-agent-watch.md): lower priority
    than the drain's own work by the owner's instruction. Seven slices wait on no question.

## 9. Packaging and contracts

Last because it changes how tillr is delivered and nothing about what it does.

55. [Every tillr utility has a written contract](../design/tillr-utility-contracts.md): what each
    provides and expects, kept by fast tests. Five slices wait on no question.

56. [Self-hosted building](../design/tillr-selfhosted-build.md): tillr builds from what Git
    holds, so the pack's source copy can go. Related to entry 1, which decides the first build.

57. [What tillr changes for a consuming pipeline](../design/tillr-separable-parts.md) and
    [the definitions build plan](../design/tillr-definitions-plan.md): four small pieces of work
    and the plan's next tranche are unfiled.

58. Graduate two built designs into references:
    [the utilities](../design/board-utilities.md) and
    [the observatory server](../design/tillr-observatory.md).

59. [Who made the decisions recorded in the designs](./decision-provenance-review.md): the owner
    keeps, overturns or reopens the decisions a designer made. It is last in build order because
    it builds nothing; its questions are first under the heading below.

## Open decisions that block entries

Each question is stated, with its options, in the document that owns it. This list says only
which entry waits. A question that blocks nothing yet is reached through its design's link above.

| Decision | Owner of the question | Blocks |
| :--- | :--- | :--- |
| How a fresh clone gets its first proved commit: the build accepts an unproved commit when no proved one exists, the first full-suite pass creates the ref, or the ref is published with the repository | Unfiled; no design owns it yet, and [the build-commit reference](../reference/tillr-build-commit.md) describes the ref it concerns | Entry 1 |
| What a repository may declare: landing steps, item kinds, or both, and in which file | Unfiled; the design of entry 3 opens with it | Entries 3, 4 and 36 |
| Whether a run inside a lane may record evidence the gate trusts | [`OQ-ER1`](../design/tillr-evidence-registry-and-suite-guardrail.md#OQ-ER1) | Entry 17 |
| How the landing judge weighs a recorded bypass | [`OQ-LS5`](../design/tillr-lane-shims.md#OQ-LS5) | The last slice of entry 16 |
| Which fallbacks are kept | [`OQ-FB1`](../design/tillr-fallback-inventory.md#OQ-FB1) to [`OQ-FB8`](../design/tillr-fallback-inventory.md#OQ-FB8) | Entry 24 |
| Whether a worker's `seat` obstacle still moves an item to a stronger model | [`OQ-JL5`](../design/tillr-judge-and-lifecycle.md#OQ-JL5) | Part of entry 25; the removal does not wait |
| How a closed backend is probed, and where a failed probe is routed | [`OQ-BP1`](../design/tillr-closed-backend-probe.md#OQ-BP1) to [`OQ-BP3`](../design/tillr-closed-backend-probe.md#OQ-BP3) | Entry 21 |
| Whether a program may repair its own stuck state, what tillr does with no ordered work left, and which seat takes the operator's judgments | [`OQ-OD1`](../design/operator-decisions-inside-tillr.md#OQ-OD1) to [`OQ-OD3`](../design/operator-decisions-inside-tillr.md#OQ-OD3) | Three slices of entry 22 |
| Limits and priority among concurrent test processes | [`OQ-GTC1`](../design/gate-test-capacity.md#OQ-GTC1) to [`OQ-GTC4`](../design/gate-test-capacity.md#OQ-GTC4) | Entry 11 |
| Whether every new request is read for flaws before its first worker | [`OQ-RA1`](../design/tillr-request-amendment.md#OQ-RA1) | The last slices of entry 31 |
| What happens to running slices when a review comment arrives, and how the existing backlog is treated | [`OQ-DT1`](../design/tillr-design-to-planning-transition.md#OQ-DT1), [`OQ-DT2`](../design/tillr-design-to-planning-transition.md#OQ-DT2) | The later slices of entry 33 |
| Who writes a provisional design, and how it is marked | [`OQ-PD1`](../design/provisional-design-docs.md#OQ-PD1) to [`OQ-PD4`](../design/provisional-design-docs.md#OQ-PD4) | Entry 38 |
| Where the companion-plan rulebook belongs | [`OQ-RJ5`](../design/tillr-review-rules.md#OQ-RJ5) | Part of entry 42 |
| Whether a build run has a strict ceiling at the build tier | [`OQ-PSS3`](../design/tillr-plan-step-and-seat-selection.md#OQ-PSS3) | Entry 44 |
| How a design item starts, and how strictly a designer is pushed to scouts | [`OQ-RD1`](../design/tillr-designer-scouts.md#OQ-RD1) to [`OQ-RD3`](../design/tillr-designer-scouts.md#OQ-RD3) | Entry 45 |
| Which models the held subscription profile uses, and what it does at its reserve | [`OQ-DSP1`](../design/tillr-seat-profiles.md#OQ-DSP1) to [`OQ-DSP3`](../design/tillr-seat-profiles.md#OQ-DSP3) | Entry 47 |
| The watcher's command name, whether a rule may run a probe, and where its episodes live | [`OQ-GW1`](../design/general-agent-watch.md#OQ-GW1) to [`OQ-GW3`](../design/general-agent-watch.md#OQ-GW3) | The later slices of entry 54 |
| Whether a Go package may be the contract between two utilities | [`OQ-UC1`](../design/tillr-utility-contracts.md#OQ-UC1) | The later slices of entry 55 |
| How tillr builds from Git | [`OQ-SH4`](../design/tillr-selfhosted-build.md#OQ-SH4) | Entry 56 |
| Which recorded decisions the owner keeps | [The provenance review](./decision-provenance-review.md) | Nothing is held; a decision overturned there reopens the design that rests on it |

[Declared state machines for tillr](../design/tillr-state-machines.md) blocks no entry above. Its
questions on who authorizes a change to an item's allowed paths, and on what a fault quarantines,
take their place in this order here, after the table.

## Icebox

Wanted, not now, by the owner's call. Picking one up means giving it a place above.

- **Fetching pending design comments from a Vantage server.** A server poller, its quiet window
  per document and its settings are outside
  [the review comments design](../design/tillr-review-comments.md#OQ-RC4).

- **Prices on the observatory.** The pages show usage and never a price
  ([usage without dollar prices](../reference/observatory-redesign.md#usage-without-dollar-prices)).
  Picking it up means settling how a price is derived per backend, including a subscription that
  is not billed per run, cached tokens, and models whose price changes.

- **The `muse` run adapter** ([reference](../reference/tillr-muse-backend.md#adapter-contract-and-write-boundary)).
  The adapter stays in the tree and no backend policy names it. Picking it up means naming it in a
  policy and running its live sandbox canary.

- **AutoQA, tillr's exploratory testing of itself** ([design](../design/tillr-autoqa.md)). No
  AutoQA code exists. Its question, [`OQ-AQ2`](../design/tillr-autoqa.md#OQ-AQ2), is blocked until
  the design is picked up, and so are
  [`OQ-FQ7`](../design/tillr-separable-parts.md#OQ-FQ7) and
  [`OQ-FQ8`](../design/tillr-separable-parts.md#OQ-FQ8).

- **The goal monitor** ([design](../design/tillr-goal-monitor.md)): a goal as a recurring item
  whose criteria are a property of the system. Nothing is built. Picking it up means a use for a
  scheduled check-in, ruling [`OQ-GM1`](../design/tillr-goal-monitor.md#OQ-GM1) and
  [`OQ-GM2`](../design/tillr-goal-monitor.md#OQ-GM2), and building
  [scheduled tasks](../design/scheduled-tasks.md) first.

- **Several backends for one seat, and spillover from a capped backend**
  ([`OQ-LD2`](../design/tillr-live-drain-config.md#OQ-LD2)). A seat runs on one backend and
  nothing moves work to another by itself; an operator switches the policy. Picking it up means a
  small design that says whether work waits on a capped backend or spills to another, ruled
  before it is built.

## What this page does not cover

- **What is built.** The references under [`docs/reference/`](../reference/) and each module's
  README describe it; [the design status table](./design-status.md) lists every design.
- **Which item runs next.** That is the queue's answer, from `tillr-view waiting`.
