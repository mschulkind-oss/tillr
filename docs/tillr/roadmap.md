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
   - **Where:** [`README.md`](../../README.md), [`tests/tillr_build_test.go`](../../tests/tillr_build_test.go),
     [the build-commit reference](../reference/tillr-build-commit.md).

2. **Make the test suites independent of the machine.** Second because a suite that is red on a
   clean machine hides every real failure behind it.
   - **Outcome:** every suite passes on a clean machine and in continuous integration, with no
     dependence on the length of a directory path, on a program outside the tree, or on timing.
   - **Known failures:**
     - [`detached_gate_restart_test.go`](../../tools/tillr-drain/drain/detached_gate_restart_test.go)
       races its gate fixture for a process-id file.
     - `TestLandingRollWithFailedStoreMigrationAlarmsAndKeepsOldBuild` in
       [`restart_lost_worker_test.go`](../../tools/tillr-drain/drain/restart_lost_worker_test.go)
       failed once on 2026-11-24 with `database is locked (5) (SQLITE_BUSY)` in its
       `store_event` case while other suites ran beside it, and passed four times run alone
       (`go test ./drain -run TestLandingRollWithFailedStoreMigration -count=4`).
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
     - The drain: [`forkgate/`](../../tools/tillr-drain/forkgate/) (the `tillr-drain fork-gate` face),
       [`held_fork.go`](../../tools/tillr-drain/remotesync/held_fork.go), and the `.forks/lanes/`
       paths that [`workflow_arguments.go`](../../tools/tillr-drain/workflow/workflow_arguments.go)
       rewrites.
     - The roll tool: the `--pi` option of [tools/tillr-roll](../../tools/tillr-roll/).
   - **Depends on:** entry 3.

5. **Remove the drain's rolling-program protocol.** The drain still parses the report format of a
   judging program that has been retired. Declared checks are the protocol now, and two readers of
   one answer disagree: `goAffectedCheckID` in the drain's `trunk.go` is
   `go-affected/affected-tillr-go-test-packages-pass`, which does not match the declared check id
   `go-affected` ([`go-affected.json`](../../tools/gate/checks/go-affected.json)).
   - **Outcome:** declared checks are the only protocol, and replay scoping recognizes the
     declared `go-affected` id.
   - **Where:** [`fork_check_triage.go`](../../tools/tillr-drain/remotesync/fork_check_triage.go),
     [`legacy_watches.go`](../../tools/tillr-drain/internal/gatespans/legacy_watches.go),
     `heavy_legacy_progress.go` in
     [`tools/tillr-drain/heavyrun/`](../../tools/tillr-drain/heavyrun/), `runHeavyLegacy` in
     [`heavy_steps.go`](../../tools/tillr-drain/internal/gatespans/heavy_steps.go), and the `go-tool/`,
     `go-substrate/` and `gate-runner/` id prefixes in
     [`replay_go_affected.go`](../../tools/tillr-drain/internal/gatespans/replay_go_affected.go).
   - **Depends on:** nothing. It is behind entry 4 only because both edit the drain's
     heavy-check code and the fork removal deletes more of it.

6. **Give the pi run adapter a compatibility mode.** The adapter
   ([`tools/tillr-run/backends/pi/`](../../tools/tillr-run/backends/pi/)) requires a pi that
   can leave a tool call unanswered and answer it when the session is opened again (a *parked*
   tool call). It asks `pi --features` and
   [refuses](../../tools/tillr-run/backends/pi/README.md#required-pi-features) a pi that does
   not list `parked-tool-calls`. Upstream pi does not list it; the fork
   [mschulkind-oss/tillr-pi](https://github.com/mschulkind-oss/tillr-pi) does, and carries only
   what the adapter requires. The drain's `probePiEndingCapability` asks the same question and
   chooses the report footer for a pi without the feature, a choice the adapter's refusal now
   makes unreachable.
   - **Outcome:** a compatibility mode, chosen deliberately, runs a pi without the feature on
     the report footer and states what is reduced; the refusal stays the default.
   - **Shrink the fork:** every behavior tillr can provide itself leaves the fork, and what
     remains is offered upstream. Three things the adapter uses are in neither upstream pi nor
     the fork, and each needs one of: a named required feature, a replacement inside tillr, or
     a place in the fork.
     - The stop signal: [pausing](../../tools/tillr-run/backends/pi/README.md#pausing-and-continuing)
       sends SIGUSR1 and expects pi to stop at the next gap. A pi without that handler ignores
       it and is killed at the straggler bound. The candidate replacement is tillr stopping the
       worker at a tool-result boundary and reopening the session.
     - The sub-agent extension: the adapter relies on `PI_SUBAGENTS_TEMP_ROOT`, the
       `pi-subagents-v1` marker and `sessionFile` in `status.json`.
     - The background-task extension: [`land.ts`](../../tools/tillr-run/backends/pi/land.ts)
       reads the journal named by `PI_BACKGROUND_TASK_JOURNAL`.
   - **Unverified:** whether upstream pi's `turn_end` event carries the usage fields
     [`extract-report`](../../tools/tillr-run/backends/pi/extract-report) reads.
   - **Also:** [`pi-build-agent-dir`](../../tools/tillr-run/pihelper/build_agent_dir.go), which
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
   ([`tillr-loop`](../../tools/tillr-loop/)).
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

12. [Repairing a red trunk proof while newer landings advance the trunk](../design/trunk-repair-on-moving-trunk.md):
    first among these proof and gate designs because unverified input-touch deferrals leave a red
    trunk proof unrepaired while newer landings keep advancing the trunk. The build waits on the
    owner's choice of repair rule.

13. [Catching same-tool documentation and contract regressions in the pre-merge gate](../design/fast-gate-same-tool-proof-misses.md):
    ahead of the larger test-caching and flaky-judgment builds because wiring static documentation
    checks and fast drain contract tests into pre-merge selection is a small change that stops
    landings from turning the trunk proof red in the tool they edited. The build waits on the
    owner's pre-merge gate choice.

14. [Skipping test work whose result is proved unchanged](../design/gate-test-caching.md):
    after gate test capacity and ahead of per-test flaky judgment because its content-keyed input
    digests and read sandboxing are the substrate that cross-commit test skipping and flaky-test
    fingerprinting build on. The build waits on the owner's soundness and caching rulings.

15. [Judging flaky tests one test at a time in the trunk proof](../design/trunk-proof-per-test-flaky-judgment.md):
    after moving-trunk repair and test caching, which it depends on, and ahead of landing velocity
    because one flaky test otherwise disables a whole module suite check. The owner ruled on
    2026-11-28 that a failed trunk test is disabled without a rerun, listed in the repository and
    sent to the mechanic; no question is open, so every slice may start.

16. [Keeping the trunk proof alive across a loop restart](../design/trunk-proof-survives-restart.md):
    ahead of landing velocity because an in-process trunk proof or culprit search holds pending
    loop restarts or starts over when the loop restarts. The build waits on the owner's choice of
    restart handoff.

17. Landing velocity: a landing tier that runs before the trunk moves and a rolling tier that runs
    on the trunk, built in shadow first. It has no design document; the gate it would change is
    described in [the gate reference](../reference/gate.md).

18. [A failure is diagnosed, not waited out](../design/tillr-judge-and-lifecycle.md#424-diagnosing-a-repeated-failure-the-unbuilt-mechanism): a
    failure is fingerprinted from its own text, and a deterministic one blocks its items on one
    diagnosis ticket. After landing velocity because it reads that tier's results.

19. [The trunk fixer's gate](../reference/gate.md#the-trunk-fixers-gate): the fixer gets the gate any candidate
    gets plus the checks it names. Its fork-release reporting leaves with entry 4.

## 4. A drain that runs unattended

Fourth by rule 2. Each design here removes a way one item's trouble stops the loop or reaches a
person who did not need to be asked.

20. [Why the drain cannot yet run a night alone](../design/unattended-drain.md): eight structural
    gaps, each with a rule and a permanent scenario. First because the others fill in its rules.
    Its next step is its implementation plan.

21. [Preventative command shims and run boundaries for workers](../design/tillr-lane-shims.md):
    a worker is stopped before it kills another lane's process, moves a shared ref or runs a
    whole module suite, not after.

22. [One evidence registry, and no whole-suite runs in lanes](../design/tillr-evidence-registry-and-suite-guardrail.md)
    ([build sketch](../design/tillr-evidence-registry-and-suite-guardrail.md#9-build-phases-sketch)): the gate, the
    trunk proof and a worker's brief trust one recorded result instead of running a check again.

23. [Refused pushes](../reference/tillr-refused-push.md): when the remote branch has moved, replay
    unpublished commits onto it instead of holding every later publication.

24. [Shared-path serialization](../design/tillr-shared-path-serialization.md): two items that
    edit the same file stop meeting as a squash conflict at landing.

25. [Telling a running worker when the trunk goes green and letting it rebase before landing](../design/tillr-green-trunk-worker-rebase.md):
    after preventative command shims and shared-path serialization, and ahead of conflict
    avoidance and repair because that design's in-run overlap notice builds on this turn-gap
    notice and bounded rebase helper. The build waits on the owner's trigger and rebase rulings.

26. [Heading off landing conflicts early and repairing each at the lowest cost](../design/tillr-conflict-avoidance-and-repair.md):
    after worker rebase, which its turn-gap notice depends on, and ahead of conflict-answer
    checking because avoiding squash conflicts and repairing them in warm sessions prevents wasted
    gate runs before a conflict reaches a designer. Standalone rebase, verdict-reuse and
    path-serialization items proceed while its hot-file, gate-entry and overlap-notice rulings
    hold the rest.

27. [A check on a designer's answer to a conflict](../design/tillr-conflict-answer-check.md): an
    answer that names a symbol or key the tree does not have is caught before it lands.

28. [A probe for a closed backend](../design/tillr-closed-backend-probe.md): one failed run stops
    closing a backend for every seat until a person probes it.

29. [What the operator settles is settled inside tillr](../design/operator-decisions-inside-tillr.md)
    and [ended-item saves](../design/tillr-ended-item-saves.md): each kind of hand action gets a
    taker inside tillr, `refs/tillr/ended/<run>` ends without hand deletion, and lane saves bound
    untracked files before Git hashes them. Ten slices of the first design wait on no question.

30. [A conflict that cites a settled decision goes to the operator or owner first](../design/tillr-settled-decision-conflicts.md):
    a blind request collision with a settled decision skips the design seat on the first report,
    alerts at once, and shows the request words beside the broken decisions. Two slices wait on
    neither of its questions.

31. [RAM-backed `/tmp` for tillr's workspace, with `/tmp` use measured and shrunk to fit](../design/workspace-tmpfs-scratch.md):
    bound each check and run to a short private `/tmp` directory, compile one stripped test binary
    per sharded Go package, disable Node's `/tmp/node-compile-cache` in ephemeral runs, stop
    leaking notify `.sock.lock` files, and watch ten-lane `/tmp` occupancy against a `2 GiB`
    ceiling on `drain.tmp-usage`. Four slices wait on neither of its questions.

32. [Convergence](../design/tillr-convergence.md): a cold machine rebuilds the queue from the
    documents, drains it and shows every stopped item with the act that moves it.

33. [Every fallback in the drain, the run adapters and the gate](../design/tillr-fallback-inventory.md):
    each path that runs because the intended one failed is removed with a loud failure, or kept
    on the owner's acceptance. Behind its eight questions.

34. [The judge and the item lifecycle](../design/tillr-judge-and-lifecycle.md): remove the
    automatic move of a failed run to another model, and build the `delivery` obstacle.

35. [The escalation ladder](../design/escalation-ladder.md): a worker whose plan disagrees with
    the tree records the conflict and it is routed to a seat with authority to answer.

36. [The mechanic seat and automatic threshold escalation](../design/tillr-mechanic-escalation.md):
    after the escalation ladder and operator decisions inside tillr because it fits the dedicated
    mechanic seat and threshold chain onto them, and ahead of storage trials because
    non-converging items and red trunks otherwise wait on a person. Core slices proceed while
    queue-reorder, revert-scope and profile rulings hold the rest.

37. [Carrying the store, unlanded candidates and the proved pointer across a trunk history rewrite](../design/tillr-trunk-history-rewrite.md):
    after mechanic escalation because a history rewrite is an explicit pre-publish operation
    rather than an unattended loop fault, and ahead of storage trials because a rewrite without
    commit-map carry-over or queue export wipes the live store and waiting candidates. The build
    waits on the owner's carry-over and ref-pruning rulings.

38. [Storage](../design/tillr-storage.md): fix the store's usage faults, then compare today's
    store with the proposed structures on one workload. Last here because its trials decide
    whether any change is worth making.

## 5. Items: how work enters, is sized and is repaired

Fifth by rule 3: the item model is the substrate of sections 6 and 7.

39. [The item graph is the whole path](../design/tillr-graph-is-the-path.md) and
    [the vocabulary](../design/tillr-vocabulary.md): every act on an item is a node of its graph,
    and worktrees and scratch belong to items. First because the entries below assume it.

40. [Scheduling and notifications](../design/tillr-scheduling-and-notifications.md): the
    scheduling pass decides only which ready node runs next. Six slices, in order.

41. [An oversized item lands in parts](../design/tillr-item-split.md): the accepted parts of a
    change land without the part objected to.

42. [A request that is hard to deliver is amended once](../design/tillr-request-amendment.md):
    the design seat writes an amendment beside the saved words. Eight slices wait on no question.

43. [Criterion repair](../design/criterion-repair.md): a criterion that was wrong when written is
    superseded with a reason, instead of closing its item.

44. [A design with no open questions moves straight to planning](../design/tillr-design-to-planning-transition.md):
    five slices wait on neither of its questions.

45. [The queue mirrors the roadmap's outstanding designs](../design/tillr-queue-mirrors-designs.md)
    and [per-step question holds](../design/tillr-per-step-question-holds.md):
    every unfinished design a roadmap routes has one item in the queue, build steps live in a
    companion plan, and an unanswered question holds only the steps that depend on its answer.

46. [Early design landing](../design/tillr-early-design-landing.md): a question and the design
    it is about land ahead of the item that raised them.

47. [The idea pipeline](../design/tillr-idea-pipeline.md): item kinds, the typed lineage edge and
    the lineage view. Its item kinds wait on entry 3.

48. [Work reaches tillr through several doors](../design/tillr-intake-paths.md): each way work
    enters ends in one stored item record.

49. [Provisional design documents](../design/provisional-design-docs.md): capture a prompt and
    its first questions on the trunk before the worked design pass.

50. [Scheduled tasks](../design/scheduled-tasks.md): a due time and a recurrence on an item.
    Settled and unbuilt; the substrate of the goal monitor in the [Icebox](#icebox).

51. [Multi-agent competition workflows with parallel entries and star judging](../design/tillr-competition-workflow.md):
    last in this section after the item graph and core intake and repair designs because
    multi-round entrant fan-out and comparative judging build on item graphs to add an optional
    tournament workflow rather than unblocking ordinary queue items. The build waits on the
    owner's rulings on round bounds, retention, checkpoints, backend selection, presentation and
    run-count confirmation.

## 6. Review before landing

Sixth: no agent reads a change before tillr lands it, and these designs add that reading. Behind
section 5 because a review returns to an item through the graph that section builds.

52. [A review before every code landing](../design/tillr-diff-review.md): an agent that did not
    write the change reads the diff as it will land.

53. [One compliance check on what an item is about to commit](../design/tillr-design-document-check.md):
    a cheap agent holds a commit to written policies, never to logic.

54. [Review and compliance routing by kind of file](../design/tillr-review-rules.md): which
    changed files trigger entries 52 and 53, and which rulebook each is checked against.

55. [Review comments on a design](../design/tillr-review-comments.md): the fast track that sends
    a comment to the session that wrote the document.

## 7. Seats and models

Seventh because it lowers the cost of every run and stops nothing when it is missing.

56. [Who chooses an item's seat, and what the plan step is for](../design/tillr-plan-step-and-seat-selection.md):
    replaces the upfront plan step and the restart after a failed verify.

57. [What a designer reads itself and what goes to scouts](../design/tillr-designer-scouts.md):
    design and plan sessions use cheap scouts for search.

58. [Planning and model tiering](../design/tillr-planning-node-model-tiering.md): run more build
    items through the planned workflow so its cost can be read. After entries 56 and 57, which
    change that workflow.

59. [Seat profiles and one-command switching](../design/tillr-seat-profiles.md) and
    [live changes to drain capacity](../design/tillr-live-drain-config.md)
    ([build sketch](../design/tillr-live-drain-config.md#8-build-phases-sketch)): named policies an operator
    switches while the drain runs.

60. [Automatic seat-profile switching when subscription quota closes and reopens](../design/tillr-quota-profile-switching.md):
    immediately after seat profiles and live drain capacity because it switches an ordered list
    of those profiles, and ahead of single-adapter and benchmark work because an exhausted
    subscription window otherwise pauses every seat on that account. Core profile-list and pool
    slices proceed while paused-run handoff and reopen-headroom rulings hold the rest.

61. [The `claude` run adapter](../reference/tillr-claude-backend.md) and
    [the landing call](../design/tillr-landing-call.md): the adapter's call-suspending ending
    is the landing call's next build step.

62. [Replaying recorded tasks against several models](../design/model-benchmarks-from-drain-records.md):
    its first step, copying the shortlisted task records, runs no model.

63. [Driving all of tillr with any backend through scripted fakes](../design/tillr-scripted-fakes.md):
    last in this section after the landing call, seat profiles and quota profile switching
    because its offline scripted harness exercises every run adapter and profile switch end to
    end without live credentials. Core fake and scenario slices proceed while command exposure
    and gate-matrix scope wait on the owner's rulings.

## 8. Watching and alerts

Eighth: these report what sections 3 and 4 repair.

64. [The alert workflow](../design/tillr-watch-alert-workflow.md), then
    [the alert triage seat](../design/tillr-alert-triage-seat.md): every alert gets an
    investigation and one recorded outcome, and triage runs in a seat inside tillr. The triage
    slices are chained behind the workflow's.

65. [The watch rules not yet shipped](../design/tillr-watch.md): the designed rules that have no
    file under [`config/tillr/watch/`](../../tools/tillr-watch/rules/).

66. [Watching the hosted continuous-integration run of each published commit](../design/tillr-hosted-ci-watch.md):
    after the alert workflow, alert triage seat and watch rules because it routes distinct
    hosted failures through them, and ahead of read-face timing, session recording and the
    general non-tillr watch because published commits that fail hosted checks otherwise go
    unwatched until a person checks the remote. Observation and event slices proceed ahead of
    its routing and priority rulings.

67. [Read-face timing](../design/tillr-read-face-timing.md): a slow read command is measured and
    alerted on without the read writing to the store.

68. [Session recording](../design/session-recording.md): four owed slices, first the reader
    that reads a Claude run's log and every backend's run record.

69. [A watch any agent can use without tillr](../design/general-agent-watch.md): lower priority
    than the drain's own work by the owner's instruction. Seven slices wait on no question.

## 9. Packaging and contracts

Last because it changes how tillr is delivered and nothing about what it does.

70. [Every tillr utility has a written contract](../design/tillr-utility-contracts.md): what each
    provides and expects, kept by fast tests. Five slices wait on no question.

71. [Self-hosted building](../design/tillr-selfhosted-build.md): tillr builds from what Git
    holds, so the pack's source copy can go. Related to entry 1, which decides the first build.

72. [What tillr changes for a consuming pipeline](../design/tillr-separable-parts.md) and
    [the unbuilt units of the core and charter split](../design/tillr-core-and-git-landing.md#85-the-unbuilt-units):
    four small pieces of work and ten units are not queued.

73. [Per-kind charter lifecycles, recorded decision moves and owner handoffs](../design/tillr-charter-kinds-moves-and-owner-handoff.md):
    after the consuming-pipeline and core-and-charter split entry because it resolves a
    consuming pipeline's item-kind, move-reason and completion-handoff gaps on top of them, and
    ahead of non-landing workflow gaps because charter item kinds and state transitions sit
    beneath workflow execution. The build items wait on the owner's charter-kind, move-reason
    and handoff-state rulings.

74. [Workflow gaps on a store that does not land](../design/tillr-non-landing-workflow-gaps.md):
    after the consuming-pipeline entry and charter kinds because its non-landing evaluator,
    precondition skip, dead-run release hook and plain-directory boundary check extend
    non-landing charter workflows, and ahead of host installation packaging. Unconditional core
    slices proceed while stage-skip and release-hook rulings hold the rest.

75. [Breaking the drain package into single-job internal packages](../design/tillr-drain-package-decomposition.md):
    after the utility-contract and consumer-boundary designs and ahead of host installation
    packaging because splitting the drain package into single-job internal packages reshapes
    internal module structure in conflict-free waves without changing external behavior.
    Remaining extraction waves proceed alongside the owner's review and pre-merge selection
    ruling.

76. [Installing tillr without a yolo jail](../design/installing-tillr-outside-yolo.md): install
    prebuilt release archives on a Linux or macOS host through Homebrew or a POSIX install script
    into `<prefix>/bin` and `<prefix>/share/tillr`, with executable-relative sidecar discovery and
    embedded default workflows. One slice waits on no question; public archives and installers wait
    on Python removal, embedded watch rules, and its three questions.

77. Graduate a built design into a reference:
    [the utilities](../design/board-utilities.md).

78. [Who made the decisions recorded in the designs](./decision-provenance-review.md): the owner
    keeps, overturns or reopens the decisions a designer made. It is last in build order because
    it builds nothing; its questions are first under the heading below.

## Open decisions that block entries

Each question is stated, with its options, in the document that owns it. This list says only
which entry waits. A question that blocks nothing yet is reached through its design's link above.

| Decision | Owner of the question | Blocks |
| :--- | :--- | :--- |
| How a fresh clone gets its first proved commit: the build accepts an unproved commit when no proved one exists, the first full-suite pass creates the ref, or the ref is published with the repository | Unfiled; no design owns it yet, and [the build-commit reference](../reference/tillr-build-commit.md) describes the ref it concerns | Entry 1 |
| What a repository may declare: landing steps, item kinds, or both, and in which file | Unfiled; the design of entry 3 opens with it | Entries 3, 4 and 47 |
| Whether a run inside a lane may record evidence the gate trusts | [`OQ-ER1`](../design/tillr-evidence-registry-and-suite-guardrail.md#OQ-ER1) | Entry 22 |
| How the landing judge weighs a recorded bypass | [`OQ-LS5`](../design/tillr-lane-shims.md#OQ-LS5) | The last slice of entry 21 |
| Which fallbacks are kept | [`OQ-FB1`](../design/tillr-fallback-inventory.md#OQ-FB1) to [`OQ-FB8`](../design/tillr-fallback-inventory.md#OQ-FB8) | Entry 33 |
| Whether a worker's `seat` obstacle still moves an item to a stronger model | [`OQ-JL5`](../design/tillr-judge-and-lifecycle.md#OQ-JL5) | Part of entry 34; the removal does not wait |
| How a closed backend is probed, and where a failed probe is routed | [`OQ-BP1`](../design/tillr-closed-backend-probe.md#OQ-BP1) to [`OQ-BP3`](../design/tillr-closed-backend-probe.md#OQ-BP3) | Entry 28 |
| Whether a program may repair its own stuck state, what tillr does with no ordered work left, and which seat takes the operator's judgments | [`OQ-OD1`](../design/operator-decisions-inside-tillr.md#OQ-OD1) to [`OQ-OD3`](../design/operator-decisions-inside-tillr.md#OQ-OD3) | Three slices of entry 29 |
| Who may drop an ended-item save absent from trunk, what happens when untracked files exceed the save bound, and whether idle ended-item lanes save-and-reset or hold | [`OQ-EW1`](../design/tillr-ended-item-saves.md#OQ-EW1) to [`OQ-EW3`](../design/tillr-ended-item-saves.md#OQ-EW3) | The ended-item-saves part of entry 29 |
| Whether a blind non-owner filing holds on the operator first or goes to the owner, and who moves an operator hold when unattended | [`OQ-SDC1`](../design/tillr-settled-decision-conflicts.md#OQ-SDC1), [`OQ-SDC2`](../design/tillr-settled-decision-conflicts.md#OQ-SDC2) | The later slices of entry 30 |
| Accepted ten-lane `/tmp` ceiling and mount cap, and whether workspace-backed check and suite scratch moves to `tmpfs` | [`OQ-WT1`](../design/workspace-tmpfs-scratch.md#OQ-WT1), [`OQ-WT2`](../design/workspace-tmpfs-scratch.md#OQ-WT2) | Slices 5 and 6 of entry 31 |
| Limits and priority among concurrent test processes | [`OQ-GTC1`](../design/gate-test-capacity.md#OQ-GTC1) to [`OQ-GTC4`](../design/gate-test-capacity.md#OQ-GTC4) | Entry 11 |
| Whether every new request is read for flaws before its first worker | [`OQ-RA1`](../design/tillr-request-amendment.md#OQ-RA1) | The last slices of entry 42 |
| Whether this repository's queue still accepts a bare `tillr item apply` once the missing-block triage route is deleted | [`OQ-IN1`](../design/tillr-intake-paths.md#OQ-IN1) | The last slice of entry 48 |
| What happens to running slices when a review comment arrives, and how the existing backlog is treated | [`OQ-DT1`](../design/tillr-design-to-planning-transition.md#OQ-DT1), [`OQ-DT2`](../design/tillr-design-to-planning-transition.md#OQ-DT2) | The later slices of entry 44 |
| What tillr does with unmarked prose questions in a design, and whether `SKETCH` and `draft` designs with open questions stay held before planning | [`OQ-PSH1`](../design/tillr-per-step-question-holds.md#OQ-PSH1), [`OQ-PSH2`](../design/tillr-per-step-question-holds.md#OQ-PSH2) | Unmarked-question and draft-boundary slices of entry 45; marked-question per-step holds do not wait |
| Who writes a provisional design, and how it is marked | [`OQ-PD1`](../design/provisional-design-docs.md#OQ-PD1) to [`OQ-PD4`](../design/provisional-design-docs.md#OQ-PD4) | Entry 49 |
| Where the companion-plan rulebook belongs | [`OQ-RJ5`](../design/tillr-review-rules.md#OQ-RJ5) | Part of entry 54 |
| Whether a build run has a strict ceiling at the build tier | [`OQ-PSS3`](../design/tillr-plan-step-and-seat-selection.md#OQ-PSS3) | Entry 56 |
| How a design item starts, and how strictly a designer is pushed to scouts | [`OQ-RD1`](../design/tillr-designer-scouts.md#OQ-RD1) to [`OQ-RD3`](../design/tillr-designer-scouts.md#OQ-RD3) | Entry 57 |
| Which models the held subscription profile uses, and what it does at its reserve | [`OQ-DSP1`](../design/tillr-seat-profiles.md#OQ-DSP1) and [`OQ-DSP2`](../design/tillr-seat-profiles.md#OQ-DSP2) | Entry 59 |
| The watcher's command name, whether a rule may run a probe, and where its episodes live | [`OQ-GW1`](../design/general-agent-watch.md#OQ-GW1) to [`OQ-GW3`](../design/general-agent-watch.md#OQ-GW3) | The later slices of entry 69 |
| Whether a Go package may be the contract between two utilities | [`OQ-UC1`](../design/tillr-utility-contracts.md#OQ-UC1) | The later slices of entry 70 |
| How tillr builds from Git | [`OQ-SH4`](../design/tillr-selfhosted-build.md#OQ-SH4) | Entry 71 |
| How public releases and the Homebrew formula are published, whether `tillr-pi` ships prebuilt packages, and whether macOS bottles wait on Darwin loop supervision | [`OQ-HI1`](../design/installing-tillr-outside-yolo.md#OQ-HI1) to [`OQ-HI3`](../design/installing-tillr-outside-yolo.md#OQ-HI3) | Slices 2 to 5 of entry 76 |
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

- **One clean way to run only the rescue work, hold the rest, and put it all back**
  ([design](../design/tillr-queue-rescue.md)). Listed here unplaced until the owner sets its
  roadmap order above. Four slices of `tillr drain rescue` and non-closing `tillr drain hold` wait
  on no question once placed; automatic red-trunk rescue entry, default express intake during a
  rescue, and post-filing lane-class edits wait on
  [`OQ-QR1`](../design/tillr-queue-rescue.md#OQ-QR1) to
  [`OQ-QR3`](../design/tillr-queue-rescue.md#OQ-QR3).

## What this page does not cover

- **What is built.** The references under [`docs/reference/`](../reference/) and each module's
  README describe it; [the design status table](./design-status.md) lists every design.
- **Which item runs next.** That is the queue's answer, from `tillr-view waiting`.
