---
title: "tillr is a core of queues and graphs, and landing changes in Git is one charter on top of it"
status: in-review
stage: DESIGN
updated: 2026-11-19
tags:
  - tillr
  - design
  - boundaries
  - product-qa
summary: "The owner's direction for tillr: general machinery of queues and graphs whose nodes are agents and commands, with its tracking, and one small, separate charter ('definition' in shipped code) that turns it into machinery for landing changes in a Git repository. It records the workflow that charter wraps, the baseline measurement of where the Git-landing assumption was wired in, the shapes the one file could take and the one that was built (a data file plus one hook program), the extension points a second use fills, and what is built and unbuilt on 2026-11-19."
next: "The owner reviews the designer's decisions D-CG1 to D-CG5 in the ledger. He named the two words on 2026-11-18 in tillr-vocabulary.md: a charter, and git-landing. The ten units of the build plan's next tranche are unfiled, and Product QA's third round has not arrived."
depends-on:
  - ../reference/tillr-item-graphs.md
  - tillr-product-qa-requests.md
---

# tillr is a core of queues and graphs, and landing in Git is one charter on top of it

**Status:** 2026-11-19. The direction is the owner's, in his words ([Section 1](#1-the-direction-in-the-owners-words)).
He said to build on 2026-11-17, and the first tranche of [the build plan](./tillr-definitions-plan.md)
is built: all eighteen of its items have landed, and a second charter (`definition` in shipped code)
that lands nothing runs end to end ([Section 8.2](#82-what-is-built-and-what-is-not)). The owner
named the layer a **charter** and the Git instance **`git-landing`** on 2026-11-18
([OQ-VO4](./tillr-vocabulary.md#OQ-VO4), [OQ-VO5](./tillr-vocabulary.md#OQ-VO5)). The plan's next
tranche of ten units is unfiled. The inventory of
[Section 3](#3-where-the-git-landing-assumption-is-wired-in-today) is the baseline measured on
2026-11-16 at `6c2af0b32`, before that build. Five decisions the build rests on were the designer's,
not the owner's; they are in [the ledger](#decision-ledger), they await his review, and he may
overturn any of them. Claims are marked **[ran]**, **[read]** (file named) or **[inferred]**.

> **In short.** tillr is a core of queues and graphs plus one charter per use (`definition` in
> shipped code), and a charter is a data file and one hook program. Landing changes in Git is the
> first charter (`git-landing`); a second one that lands nothing already runs. Most of the landing
> workflow's behavior is still Go inside the drain, and moving it behind the charter is the work
> that remains.

**Why it matters.** Product QA wants tillr's queues, runs and tracking without its landing. With the
landing part a plainly separate charter, Product QA writes a second charter instead of asking tillr
for exceptions.

**Cost.** Every rule about items that suits Git landing and chafes against another use leaves the
core: lifecycle, prerequisites, retention and the completion rule become each charter's to state.

**Start at [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices)** for what was
decided and built; [Section 3](#3-where-the-git-landing-assumption-is-wired-in-today) is the
measurement it started from.

**Needs your ruling:** None in this document. The two names it left open, for the kind of layer and
for the Git-landing instance, were asked in the vocabulary and ruled by the owner on 2026-11-18:
the layer is a **charter** ([OQ-VO4](./tillr-vocabulary.md#OQ-VO4)) and the first use is
**`git-landing`** ([OQ-VO5](./tillr-vocabulary.md#OQ-VO5)). This document was written before those
rulings, so its body still says "definition" where he now says charter; shipped code says
"definition" too and is not renamed.

**Awaits your review:** the designer's decisions `D-CG1` to `D-CG5` in
[the ledger](#decision-ledger).

**Reads with:** [the tillr design index](../tillr/design-status.md) (what every design's status
is), [the build plan](./tillr-definitions-plan.md) (the eighteen built items and the unfiled next
tranche), [tillr-product-qa-requests.md](./tillr-product-qa-requests.md) (Product QA's second-round
reply, as received), [tillr-separable-parts.md](./tillr-separable-parts.md) (tillr's answer to
Product QA and the contract), [tillr-item-graphs.md](../reference/tillr-item-graphs.md) (graphs inside one
item), [the graph design](./tillr-graph-is-the-path.md) (landing as nodes of an item's graph),
[the vocabulary](./tillr-vocabulary.md) (one meaning per word, and the naming rulings),
[board-utilities.md §5.9](./board-utilities.md#59-the-composed-pipeline) (utilities meet only
through processes and records).

---

## The words this document uses

Three terms. The last two were coined here as **stand-ins** and shipped in code; the owner named
both on 2026-11-18 in the vocabulary ([OQ-VO4](./tillr-vocabulary.md#OQ-VO4),
[OQ-VO5](./tillr-vocabulary.md#OQ-VO5)). In this document's body "a definition" and "the landing
definition" are the older spellings of his words.

- **The core:** the general machinery. Queues of items, graphs of nodes inside an item where each
  node is an agent or a command, and the tracking behind them: the store, items, criteria, runs,
  leases, events and views. Not anything that knows what Git is.
- **A charter** *(the owner's word, [OQ-VO4](./tillr-vocabulary.md#OQ-VO4); "a definition" in this
  document's body and in shipped code)*: the layer that turns the core into one use case, stating
  that use's purpose, from which its rules follow:
  its lifecycle, prerequisites, retention, completion rule and vocabulary, as a data file, with one
  hook program for its judgments. Not an item's definition, which is its fence and criteria.
- **The `git-landing` charter** *(the owner's name, [OQ-VO5](./tillr-vocabulary.md#OQ-VO5); "the landing
  definition" in this document's body, `git-landing` in code)*: the charter
  that lands changes in a Git repository, from item blocks in documents through a gate. Its data
  file is [landing.json](../../tools/tillr/definition/landing.json) and its hook program is
  [tillr-loop](../../tools/tillr-loop/tillr-loop); most of its behavior is still Go in the programs
  [Section 3](#3-where-the-git-landing-assumption-is-wired-in-today) names. Product QA's findings
  store is a second charter.

Two names of other projects. **Product QA** is a product bug-finding pipeline, with the
fuzzer Mangle as its prober; its agent wrote the second round
([tillr-product-qa-requests.md](./tillr-product-qa-requests.md)). **AutoQA** is something else:
[tillr-autoqa.md](./tillr-autoqa.md), tillr's own design for exploratory testing of tillr. In the
quotes below the owner says "AutoQA" and means Product QA.

Everything else is tillr's existing vocabulary, defined in
[the vocabulary](./tillr-vocabulary.md#5-the-words): store, item, drain, gate, lander, fence,
criteria, candidate, hold. A **lane** is today's leased slot with its own git worktree, a word that
document retires. A **step program** is a program the drain runs for one step and reads by
its exit code ([steps.go](../../tools/tillr-drain/drain/steps.go)).

---

## 1. The direction, in the owner's words

The owner, 2026-11-16 (relayed by the coordinator as 2026-11-17):

> I guess what I'm hoping we can do here is, which will just help the design and help reuse, is
> we've got a set of machinery which is basically queues and DAGs that have agents and other
> commands mixed in with them in some glorious dance. and that's like a whole bunch of machinery
> and the tracking behind it. I want you to be able to describe the thing that turns that into
> machinery to land changes in a Git repository. I want that to be described in as small of a file
> as possible, as separate as possible, and it will inject everything it needs across the Tiller
> landscape. from that one file so it's just very clear what the workflow is you're wrapping up
> with this and it's very clear what all the extension points are.
> Before I answer too many open questions, I want you to take this, write this all down, get the
> pack documentation shipped so that we can then have the AutoQA agent answer something more up to
> date. Then we can talk about truly what we're going to build here.

Three more statements, 2026-11-16 (relayed as 2026-11-17). On where rules about items belong:

> I guess retention policy, item lifecycle, item prerequisites, that sort of thing should be part of
> this custom, this layer that describes your workflow use case, not the actual core.

On an item that reaches the queue with no document block, as one filed with `tillr item apply` did
on 2026-11-16 and was dispatched to a build worker that could do nothing:

> if it ends up like this, it's probably that's some half-baked request for me and it should be
> able to go to ... the next step, which is a design agent or a triage agent or something like
> that. Like there should always be a next step that's clear for these items and we should handle
> them somehow.

On deciding what goes in the core:

> If there's tension between the core Git landing use case, which we should probably give a name in
> Tiller because there's going to be more of them, given how we're abstracting this. ... if there's
> tension between these, that's very good indication that we shouldn't make the decision in core
> and it should be customizable.

Restated:

1. tillr is two things: the core, and the landing definition.
2. The landing definition should be one file, as small and as separate as possible, and it should
   inject what it needs into every part of tillr from that one place.
3. Reading that file should show the whole landing workflow and every extension point.
4. An item's lifecycle (its states and transitions), its prerequisites (what it must have before
   it may be dispatched) and retention (how long finished things are kept) belong to the
   definition, not the core.
5. No item waits without a named next step and an actor who takes it.
6. When a choice suits Git landing and chafes against another use, or the reverse, that tension is
   the signal that the choice does not belong in the core: each definition supplies it. This is
   the owner's decision rule; [Section 6](#6-what-this-changes-in-what-was-said-before) applies it.
7. Nothing is built from this until Product QA's agent has reacted and both sides have talked. Its
   agent reacted on 2026-11-17, and the same day the owner said to build
   ([Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices)).

### 1.1 The owner's words, as reported by Product QA

Product QA's reply of 2026-11-17 quotes the owner five times
([their Section 3](./tillr-product-qa-requests.md#3-graph-change) and
[Section 4](./tillr-product-qa-requests.md#4-requests-for-this-round)). These are his words **as the
other project's agent reported them**, not heard here. How each stands against what he has said
directly is below the list.

1. **On graph change**, clarifying a statement of the day before: a step cannot replace itself,
   "but it can emit another node directly downstream of itself, which consumes all of its original
   data that … it will pass through and has the exact same output shape"; and "you could have a
   graph of 10 nodes fully executed. Then at some point in the future, you add two nodes to make it
   12. That gives you two unexecuted nodes that will then get started the next scheduling pass."
2. **On reopening:** "Reopening or not, when everything is done, I suppose should be a policy
   determination for whatever your use case is. Like, we will define valid state transitions for
   an item and reopening might be valid for some and not valid for others."
3. **On the other differences** between Guild's graph runtime and tillr's item graphs: "they should
   be all policy things that should be possible if you want them from core. Core should not
   preclude them."
4. **On fixed graph size limits** (`D-IG4`, at most 100 nodes and a depth of 8): "This 100 steps, I
   hate this. I don't want limits like this. We should certainly tell Tiller that this is not
   acceptable. Like, we'll build what we build."
5. **On clocks:** "The agent works until it is done, it has an event, we have a stall detector
   watchdog if we need something like that, but we don't just say, 'Okay, 30 minutes,' and then
   have some gate given 20 seconds and on a loaded machine it fails".

The owner, directly to the coordinator on 2026-11-17, on the gate's "could not judge", beside
statement 5:

> what does 'could not judge' mean. isn't this directly opposed to our principles? target budgets,
> watch for stalls, but don't let 'load is high' cause failures that would otherwise finish just
> fine.

How each reported statement stands:

| Statement | Standing |
| :--- | :--- |
| 1, graph change, and 3, the core must not preclude a definition's policy | Covered by what he ruled directly on 2026-11-17, recorded in [the graph design](./tillr-graph-is-the-path.md): started nodes are fixed, and anything not started may be changed |
| 2, reopening is a definition's policy | **Reported only.** No direct statement of his says it. It is built as each definition's list of valid moves, on the designer's decision `D-CG4` |
| 4, no fixed graph size limits | Covered: the graph design's ledger amends `D-IG4`, "the depth and size caps go, as the owner already directed" |
| 5, no clock that fails work | Covered by the direct statement above and by his "we said there are no timeouts" |

Statements 4 and 5 cut against accepted designs; [Section 7](#7-where-the-documents-disagree) says
where.

---

## 2. The workflow the landing definition wraps, as it runs today

Start to finish, one line each, with the tool and file that owns the step **[read]**.

1. **Intake.** An item block (an HTML comment, `<!-- tillr: item … -->`, under a heading of a
   tracked document) is read from the files `git ls-files docs` lists, and `tillr-item sync`
   writes it to the store ([sync.go](../../tools/tillr-item/item/sync.go)). The drain syncs at each
   new trunk commit (`Steps.Sync` in [loop.go](../../tools/tillr-drain/drain/loop.go)).
2. **Plan.** The drain's planner picks ready items in rank order and names a hold for the rest
   ([plan.go](../../tools/tillr-drain/drain/plan.go), [schema.go](../../tools/tillr-drain/drain/schema.go)).
3. **Fence.** The block's `paths` become the item's `allowed_paths`; an item without one is held
   `no-fence` ([item schema.go](../../tools/tillr-item/item/schema.go)).
4. **Lane.** A lane is a leased directory holding a git worktree based on the trunk; staging writes
   the worktree, the brief, the fence and the base sha ([stage.go](../../tools/tillr-drain/drain/stage.go),
   `WorktreeDir` in [request.go](../../tools/tillr-run/run/request.go)).
5. **Run.** `tillr run exec` runs a worker session through a backend adapter the backend policy
   picks by seat ([exec.go](../../tools/tillr-run/run/exec.go),
   [backend-policy.json](../../config/tillr/backend-policy.json), or its twin for the work machines,
   [backend-policy.bedrock.json](../../config/tillr/backend-policy.bedrock.json)).
6. **Candidate.** A run whose lane head moved off its base is a candidate; one that did not is held
   `candidate-no-op` (`settle` in [loop.go](../../tools/tillr-drain/drain/loop.go)).
7. **Boundary check.** Refs and worktrees outside the lane's own are snapshotted around the run, and
   any movement stops it; a diff outside the fence is `boundary-scope-escape`
   ([boundary.go](../../tools/tillr-drain/drain/boundary.go)).
8. **Block retirement.** The loop commits the removal of the item's own block before the gate
   ([retire.go](../../tools/tillr-drain/drain/retire.go)).
9. **Gate.** The gate program `tillr-loop gate` runs [change-gate.py](../../tools/gate/change-gate.py):
   fast tree checks through `tillr-gate checks`, then the landing tier of `tools/fork-check.py`,
   within the budget and strategy of [gate-policy.json](../../config/tillr/gate-policy.json). A red
   check is replayed on the base before the candidate is blamed (`Replay` in
   [steps.go](../../tools/tillr-drain/drain/steps.go)).
10. **Land.** `tillr-loop land` runs `tillr-land`, which takes the land lock, squashes `base..head`
    onto the trunk, writes the marker ref `refs/tillr/landed/<run_id>` before moving the trunk, pins
    `refs/tillr/runs/<run_id>`, and writes a `landings` row binding the gate verdict to
    `(base_sha, head tree)`; a retry finds the marker and does not land twice
    ([landing.go](../../tools/tillr-land/land/landing.go), [trunk.go](../../tools/tillr-land/land/trunk.go)).
11. **Verdict.** Only after the landing, `tillr-loop verdict` records each criterion `done`
    (`Verdict` in [loop.go](../../tools/tillr-drain/drain/loop.go)).
12. **End state.** The item moves to `landed`; other ends are `resolved-elsewhere` with a pointer,
    `closed` with a reason, and `blocked` with a waiter
    ([state.go](../../tools/tillr-item/item/state.go)).
13. **Rolling tier and trunk fixer.** After landings, a rolling check runs over the trunk range; a
    red trunk files a fixer item `fixer-main-<sha>` that runs the full gate, and holds the rest as
    `trunk-broken` ([trunk.go](../../tools/tillr-drain/drain/trunk.go)).
14. **Conflicts and the design seat.** A squash conflict is routed to a repair run on the `design`
    seat, then to the owner (`conflict-fix`, `conflict-design`, `conflict-owner` in
    [blocked.go](../../tools/tillr-drain/drain/blocked.go) and [conflict.go](../../tools/tillr-drain/drain/conflict.go)).
15. **Trunk observation.** The drain records each observed trunk tip under
    `refs/tillr/trunk-history/`, holds on a rewritten trunk, and settles an uncertain landing by
    reading the trunk ([trunk_history.go](../../tools/tillr-drain/drain/trunk_history.go),
    [already_on_trunk.go](../../tools/tillr-drain/drain/already_on_trunk.go)).
16. **The owner's checkout.** The trunk moves in the checked-out worktree by fast-forward only;
    nothing is reset, stashed or discarded, and a path holding the owner's uncommitted content
    stops the move before it starts, holding the item `trunk-path-dirty`
    (`ClashingPaths` in [trunk.go](../../tools/tillr-land/land/trunk.go),
    [dirty_hold.go](../../tools/tillr-drain/drain/dirty_hold.go)).
17. **After the landing.** Optional roll and push actions read from the settings file
    ([settings.go](../../tools/tillr/settings/settings.go)); the keeper restarts the loop with the
    three step programs named ([tillr-keep](../../tools/tillr-roll/tillr-keep), `loop_command`).

**A principle of this workflow, from the owner:** no item waits without a named next step and an
actor who takes it. An under-specified item, such as one with no document block, is not a dead
end: it goes to a triage or design seat, whose output is the missing prerequisite. The
unshared-item hold was rewritten along these lines and has landed (`item-sync-hold-unshared-items`).
It is a candidate principle for the core too, and this document reads it as one **[inferred]**:
"waits on whom, for what" is a property of any queue, and only *which* seat takes an item is the
definition's.

---

## 3. Where the Git-landing assumption is wired in today

> [!NOTE]
> This section is the baseline: measured on 2026-11-16 at `6c2af0b32`, before the build of
> [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices). It says where the work
> started. The counts have grown since and are not re-measured here;
> [Section 8.2](#82-what-is-built-and-what-is-not) says what has moved.

### 3.1 How dense it is

Occurrences of `git`, `worktree`, `trunk`, `landed`, `landing`, `gate`, `fence`, `allowed_paths`,
`refs/tillr`, `commit`, `squash`, `candidate` and `rev-parse`, case-insensitive, in non-test
sources **[ran]**:

| Component | Hits | Where they gather |
| :--- | ---: | :--- |
| `tools/tillr-drain` | 1758 | `loop.go` 375, `trunk.go` 193, `stage.go` 159, `boundary.go` 141, `steps.go` 135 |
| `tools/tillr-land` | 656 | `landing.go` 250, `trunk.go` 245 |
| `tools/tillr-view` (Go) | 434 | `serve.go` 82, `serveread.go` 64, `landings.go` 38 |
| `tools/tillr-gate` | 399 | `main.go` 95, `dualrun.go` 76, `change.go` 62 |
| `tools/gate` | 202 | this repository's checks |
| `tools/tillr-loop` | 160 | the one adapter file |
| `tools/tillr-item` | 112 | `sync.go` 35, `apply.go` 21 |
| `tools/tillr` | 57 | mostly SQLite "commit"; the real hits are `settings.go` 10 |
| `tools/tillr-trace` | 52 | indexing gate and land records |
| `tools/tillr-run` | 31 | `exec.go` 9, `main.go` 9 |
| `tools/tillr-store` | 2 | none that matter |

The coupling is dense in the drain and the lander, moderate in the gate and the views, and a
handful of lines in the store, the item face and the run face **[inferred]** from the counts and the
files read below.

### 3.2 General part and landing part, per component

"From one definition" is what it would take to supply the landing part from one place instead of
where it is: **nothing**, **small**, or **a design round**.

| Component | General part | Landing-specific part | How it gets there today | From one definition |
| :--- | :--- | :--- | :--- | :--- |
| Store (`tools/tillr`) | store, composed schema, records, events, exit codes | roll and push after a landing ([settings.go](../../tools/tillr/settings/settings.go)) | hard-coded settings keys | small |
| `tillr-store` | init, check, recover, checkpoint, events | none | — | nothing |
| `tillr-item` | items, criteria, edges, decisions, mail, grants, verdicts, close and reopen | the states `landed` and `resolved-elsewhere` ([state.go](../../tools/tillr-item/item/state.go)); the `allowed_paths` column; sync reading `git ls-files docs` ([sync.go](../../tools/tillr-item/item/sync.go)) | hard-coded state names; a schema column; a git call | a design round for states; small for the rest |
| `tillr-run` | lane leases, `run exec`, adapters, end causes, usage | the run's only working directory is the lane's git worktree (`WorktreeDir`, [request.go](../../tools/tillr-run/run/request.go)); the adapters' directory found from the git common dir | a constant; a flag default | small |
| `tillr-drain` | planner, general holds, dispatch, backend policy and fail-over, quotas, retries, passes, workflows and `node_runs`, designer sessions, open-question holds | the `Candidate` of worktree, base and head; staging a worktree; the boundary bracket; block retirement; the rolling tier and fixer; trunk history; conflicts; the dirty-checkout hold; 14 of its 47 hold reasons ([schema.go](../../tools/tillr-drain/drain/schema.go) and neighbors) | six step programs on its argv (gate, land, verdict, replay, rolling, fixer gate); the rest is Go | a design round |
| `tillr-gate` | a check runner: registry, plan, run, tiers, budgets, result memory | change checks read `git diff base..head` ([change.go](../../tools/tillr-gate/change.go)); memory is keyed by tree | check registry files and flags | small |
| `tools/gate` | — | this repository's own gate ([change-gate.py](../../tools/gate/change-gate.py), `checks/*.json`) | the gate step program | nothing: it is already definition content |
| `tillr-land` | the land lock | all of it: squash, marker and pin refs, `landings`, `candidates`, `landing_attempts`, author identity, the owner-checkout clash | the land step program | nothing: already a separate program |
| `tillr-loop` | the adapter pattern | gate, land and verdict, translated onto the loop's exit contract | named on the drain's argv by the keeper | nothing |
| `tillr-view` | status, events, passes, lanes, running, waiting, trace, serve | the landings and trunk pages, gate spans, waiting-face rules for landing holds | Go and the web front end | small to a design round |
| `tillr-trace` | reading run records | reading gate records (until commit `a9c095846` on 2026-11-18, indexing them into a persistent SQLite index) | hard-coded | small |
| Keeper (`tillr-roll`) | restart a loop, one lock per root | the roll after a landing; `loop_command` names `tillr-loop`'s three faces | hard-coded | small |
| Item block and sync | `id`, `blocked_by`, `seat`, `workflow`, `prose` | `paths` as the fence; documents are git-tracked files under `docs/`; a landed item's block is retired | hard-coded grammar | small |
| Policy files | [backend-policy.json](../../config/tillr/backend-policy.json): seats to backends | [gate-policy.json](../../config/tillr/gate-policy.json): landing strategy, red cap, lane base | policy entries | nothing |
| Workflows (`config/tillr/workflows/`) | stages, seats, access, edges ([scout-then-build@v1.json](../../config/tillr/workflows/scout-then-build@v1.json)) | none in the file; the landing runs after the exit, by rule ([what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be)) | — | a design round, if landing becomes nodes |
| `tillr-operating` skill and the pack | store, item, run and view faces | the seven-step loop ends in land, verify, audit ([SKILL.md](../../yolo-packs/tillr/skills/tillr-operating/SKILL.md)) | prose | small |

**Lifecycle, prerequisites and retention, classified by the owner's rule** **[read]**:

- **Lifecycle belongs to the definition.** At the baseline the writable states (not started, in
  queue, blocked, landed, resolved elsewhere, closed), the rule that `landed` and
  `resolved-elsewhere` are final, reopen-only-from-`closed`, and the verdict words `done`,
  `not-done`, `blocked`, `defective` were hard-coded in
  [apply.go](../../tools/tillr-item/item/apply.go),
  [lifecycle.go](../../tools/tillr-item/item/lifecycle.go) and
  [verdict.go](../../tools/tillr-item/item/verdict.go). The states, the ready state, the finished
  states and the valid moves now come from the definition file. The core keeps only that an item
  has a state from a closed set and that each move is an event.
- **Prerequisites of Git landing**, today holds in the planner: `no-fence` (no allowed paths),
  `question-open`, `questions-unknown` and `design-doc-missing` (the design document must exist
  and have no open question), `not-ready` (the item is not in the queue), `trunk-path-dirty`,
  `conflict-design` and `conflict-owner`, the fork and trunk holds, and routing an undeclared item
  with no document block to triage (`item-sync-hold-unshared-items`, landed). **General queue mechanics:** `no-lane`, `run-live`, `no-policy`,
  `backend-window`, `seat-degraded`, `failover`, the retry and readmission holds, and `blocked` by
  an unfinished dependency. The dependency edge is the core's; what counts as finished (landed) is
  the definition's.
- **Retention had no rule at the baseline.** The definition file now has a `retention` key, and
  `keep` is the only value it accepts. The run pins `refs/tillr/runs/<run_id>` of a landed item are
  dropped when it settles, and `tillr-drain sweep-refs` sweeps refs on request. The event log is
  append-only, and each run's record directory and log are kept. Per-run scratch is never deleted,
  which is where the disk goes ([the vocabulary, Section 3.1](./tillr-vocabulary.md#31-where-the-disk-goes)).
  [Bounded agent storage](./bounded-agent-storage.md) designed reclaiming for the board that tillr
  replaced, and is unbuilt.

### 3.3 How close `tillr-loop` already is to the one file

[tillr-loop](../../tools/tillr-loop/tillr-loop) was one 769-line Python file (885 lines on 2026-11-18, now also the landing definition's hook program) that supplies the
gate, land and verdict step programs, and the drain's defaults name it again for the fixer gate
(`DefaultFixerGateProgram` in [trunk.go](../../tools/tillr-drain/drain/trunk.go)) **[read]**. It
already injects the three judgments, so the drain "owns the order and the stopping conditions and
never a step's judgment", as its header says.

What it does not cover: intake, the fence, staging a worktree, the candidate rule, the boundary
bracket, block retirement, the replay and rolling programs (which default to `fork-check.py`), trunk
observation, conflicts, the item's end states, the hold vocabulary, and the views. Those are Go in
the drain, the item face and the view. By the step count of Section 2, it covers three of seventeen
steps, and those three are the densest by line **[inferred]**.

---

## 4. What "one file" could mean

Three shapes were laid out for reaction. What was built is a fourth, **D: the data file of (a)
plus the hook program of (b)**, with item graphs left as the inside of an item (`D-CG1`). Each
shape ends in its verdict.

**(a) A declarative definition**, read by a core that knows nothing of Git:

```text
{ "definition": "git-landing", "version": 1,
  "intake":    { "from": "documents", "where": "docs/**/*.md", "tracked_by": "git" },
  "workspace": { "kind": "git-worktree", "base": "trunk" },
  "fence":     "item.paths",
  "candidate": "workspace head moved off base",
  "steps":     { "gate": "tillr-loop gate", "land": "tillr-loop land",
                 "verdict": "tillr-loop verdict", "replay": "…", "rolling": "…" },
  "completes": { "state": "landed", "receipt": "land.landing" },
  "end_states":[ "landed", "resolved-elsewhere", "closed" ],
  "holds":     [ "candidate-no-op", "gate-failed", "land-refused", "trunk-broken", "…" ],
  "views":     { "pages": [ "landings", "trunk" ] } }
```

It makes the vocabulary separable: states, holds, view pages, which programs run. It does not
make the behavior behind a hold separable; a rule such as "a rewritten trunk holds everything"
stays Go unless the core grows a generic hook for it.
**Verdict: adopted as the data half of D.** Alone it separates words and no behavior.

**(b) One program that answers the core's hooks**, the adapter grown up, still a process
([board-utilities.md, the composed pipeline](./board-utilities.md#59-the-composed-pipeline)):

```text
tillr-landing describe            # prints its states, holds, view pages, as JSON
tillr-landing intake   --root R   # prints item.records to apply
tillr-landing stage    <item>     # prepares a workspace, prints its path and base
tillr-landing candidate <run>     # exit 0 candidate / 1 nothing to judge
tillr-landing gate|land|verdict <candidate>
tillr-landing observe  --since S  # trunk tips, rewrites, red ranges
tillr-landing after    <landing>  # roll, push
```

It makes behavior separable, because every landing decision is the program's. It costs a hook
protocol the core must keep stable, and a second definition is a second such program.
**Verdict: adopted as the hook half of D.** Alone it hides the vocabulary inside a program, where
no view or planner can read it without running it.

**(c) A workflow graph in the item-graphs design's own terms**, where landing is one workflow:

```text
git-landing@v1:  work (agent, seat build, write)
                 → gate (command, gate)  → land (command, land)  → verdict (command)
                 exit: requires_candidate commit_required
findings@v1:     recheck (command) → investigate (agent, read_only) → verify (agent, evaluator)
                 exit: no_commit
```

It makes "land a change" one workflow among others, and Product QA's finding pipeline another, with
command nodes and exit conditions doing the work. When it was written it ran into
[the item-graphs node boundary](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be), which refused "a landing or a
ref move inside the graph", and it leaves intake, staging and trunk observation outside any graph.
**Verdict: not an alternative to D.** The owner's direction of 2026-11-17 makes the gate and the
landing nodes of an item's graph, and [the graph design](./tillr-graph-is-the-path.md) amends that
refusal. So D is how the core sees a definition, and landing as nodes is how one item's path runs
inside it; the two combine.

**Which way the existing designs lean.** The step programs and the process-and-record seam lean toward (b): judgments
are processes the caller names. The item-graph design leans toward a partial (c): a workflow names
its own completion (`commit_required`, `commit_optional`, `no_commit`), while the landing stays one
act after the exit **[read]**. Nothing leans toward (a) alone; it reads as the vocabulary half of
(b) **[inferred]**.

**The second party's position.** Product QA chose D, as "how we already compose tools", and a
generic lifecycle in the core with each definition naming its own states
([their Section 2](./tillr-product-qa-requests.md#2-reactions)). Both are what was built.

**What each implies for the eight "no"s** of
[the separable-parts proposal](./tillr-separable-parts.md#13-what-tillr-will-not-change), as a
reading **[inferred]**:

| | Request | (a) | (b) | (c) |
| :--- | :--- | :--- | :--- | :--- |
| C1 | Outcomes per kind | opens: end states are the definition's | opens | opens per workflow exit |
| C2 | Reopening a finished item | opens: terminality is the definition's | opens | unchanged |
| C3 | Free graph rewriting | unchanged | unchanged | unchanged |
| C4 | Uncertain outside effect | opens, if the definition declares it | opens | stays refused inside a graph |
| C5 | Clocks in the plan | unchanged | unchanged | unchanged |
| C6 | A Go library | unchanged: still processes | unchanged | unchanged |
| C7 | Completion by an artifact judged | opens | opens | opens through `no_commit` |
| C8 | A second loop in one root | unchanged | unchanged | unchanged |

---

## 5. The extension points

The places a second use, Product QA's findings store or its ideas store, would plug in something
other than Git landing. Product QA gave a concrete case for each row in
[their Section 2](./tillr-product-qa-requests.md#2-reactions), and none needed a row this table does
not have.

| Extension point | Git landing fills it with | Status |
| :--- | :--- | :--- |
| Intake | item blocks in tracked documents, through sync | built; `tillr item apply` also exists; a store not fed by documents is not held for lacking blocks (`item-sync-hold-unshared-items`) |
| What a node is | an agent | agent and command are built (`item-ig-command-node`) |
| Where a node runs | a git worktree in a lane | built; a plain directory, for a definition that does not land, is built too (`item-def-stage-plain-directory`) |
| What completes a node | the run ends and passes | built; declared outputs are built (`item-ig-node-outputs`) |
| What completes an item | a landing | built; finishing with no commit (`item-ig-exit-condition`) and finishing on a hook's recorded verdict (`item-def-finish-on-hook`) are built |
| Lifecycle | six states, final `landed` and `resolved-elsewhere`, reopen only from `closed` | built as data: each definition names its states, its ready state, its finished states and its valid moves (`item-def-item-lifecycle`, `item-def-planner-lifecycle`) |
| Prerequisites | a fence, a design document with no open question, finished dependencies, a clean owner path | named in the definition file; a next step for each would be new |
| Retention | nothing is pruned | a `retention` key exists and accepts only `keep`; a rule beyond it is in the plan's next tranche |
| Outcome vocabulary | the finished states, close reasons and pointers | finished states per definition are built; counting them in the views is in the next tranche |
| Dispatch policy | lane count, seats, backend policy | built, with seat shares (`item-policy-seat-shares`), lanes outside the cap (`item-drain-express-lanes`) and a spend ceiling (`item-drain-spend-ceiling`); deadlines would be new |
| The judge | the gate program and the verdict program | built, with a judge before the gate (`item-jl-judge-before-gate`) and an evaluator node (`item-ig-evaluator-node`) |
| Trunk observation | the rolling program, trunk history | built, as step programs and Go |
| After completion | roll and push settings | built as settings; an `after-completion` hook is named in the contract and is in the next tranche |
| The views' vocabulary | landings and trunk pages, landing holds | hard-coded; the `detail` hook's name is accepted and no page draws it yet |
| An owner acknowledgment point | none | would be new; in the next tranche |

**An item with no document block** is, in the idea pipeline's terms, an idea that entered by a
side door: [the idea pipeline](./tillr-idea-pipeline.md#2-the-pipeline-and-one-writer-rules)
already routes a rough request through intake, scouting and design to a queued item, so the natural
next step for an undeclared item is that pipeline's design stage, whose output is the block
**[inferred]**.

---

## 6. What this changes in what was said before

[The separable-parts proposal](./tillr-separable-parts.md) listed eight requests tillr would not
take, each against a settled ruling, plus the undeclared-item hold. The owner's decision rule
([Section 1](#1-the-direction-in-the-owners-words), item 6) reads each "no" as a tension between
Git landing and Product QA, and so as a candidate for what a definition supplies. Two parts are the
owner's direction, not a reading: outcomes defined per item kind (Product QA's first-round request 3)
and reopening from a finished state (its request 4) belong to the definition, because the owner put
lifecycle there, and his reopening statement as Product QA reports it
([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)) says the same.

The "Judgment" column is **the designer's judgment** (`D-CG5`), which the owner may overturn row
by row: where a "no" is not a real tension but a protection every definition needs, it says why. The last column is
**Product QA's answer in round two**: four withdrawn and five real, with C3 narrowed. No ruling is
rewritten here.

| "No" | Ruling it rested on | A tension? | Judgment | Product QA, round two |
| :--- | :--- | :--- | :--- | :--- |
| C1 Outcomes per kind | The closed seven states, one projector ([D-IP1](./tillr-idea-pipeline.md#61-settled-decisions)) | Yes | The definition's, by the owner's direction. The protection that stays in the core: each definition's set is closed and projected one way | Real: an inconclusive finding must be countable |
| C2 Reopening a finished item | Terminal `landed` and `resolved-elsewhere` ([lifecycle §3.2](./tillr-judge-and-lifecycle.md#32-transition-preconditions-and-attempt-holds-d-jl4)) | Yes | The definition's, by the owner's direction. Stays true inside Git landing. Core protection: a move out of a finished state is an event, never silent | Real: the owner, as reported, rules it a definition's policy |
| C3 Steps reshaping the graph | Bounded replacement only; no model-written wiring (`D-IG3`, `D-IG5`, `D-IG9`) | Partly | Which reshaping operations exist is the definition's. That each is applied by one writer, atomically with its event, and never to a node that has started, is a protection every use needs: without it the record lies. The owner's direction of 2026-11-17 is wider than the old ruling: anything not started may be changed | Real, narrowed to the four rules all three designs share: one writer applies each change atomically with its event, no node replaces itself, nothing started changes, and every change passes the downstream-shape check. Free rewriting is withdrawn |
| C4 An uncertain outside effect as a state | No node changes anything outside its worktree ([what a workflow node may never be](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be)) | Yes | The worktree rule is Git landing's. Core protection: an effect a definition declares is recorded before and after, so a retry can see it | Real: a report send that lost its session |
| C5 Clocks and deadlines in the plan | No timers ([agent-runtime.md §6](./agent-runtime.md#6-the-task-graph)) | Partly | A deadline as an ordering input of dispatch policy is a tension and the definition's. The planner staying a function of the store, replayable, is a protection | Withdrawn: the time box is the fuzzer run's own |
| C6 A Go library as the connection | Processes and records ([board-utilities.md §5.9](./board-utilities.md#59-the-composed-pipeline)) | No | A protection: it is how a definition injects anything without linking the core | Withdrawn: processes and records are enough |
| C7 Completion that is not a landing | One item, one atomic change (`D-IG2`) | Yes | The completion rule is the definition's; Git landing's is a landing. Core protection: an item completes once, on a recorded receipt | Real: completion on a recorded, judged verdict |
| C8 A second loop in one root | None; how the keeper finds a loop | No | Not a tension. One writer per store and at-most-once launch of a run are protections; a second use runs its own store and root | Withdrawn: it runs its own store and root |
| The undeclared-item hold | Sync is the only intake ([tillr-convergence.md §3](./tillr-convergence.md#3-the-target)) | Yes | A prerequisite, so the definition's, and already narrowed to stores documents feed. The owner's next-step principle applies in every definition | Withdrawn: its stores are fed by `apply`, which the rewritten hold leaves alone |

---

## 7. Where the documents disagree

1. **Shape (c) against [the item-graphs node boundary](../reference/tillr-item-graphs.md#what-a-workflow-node-may-never-be).** That
   design refused "a landing or a ref move inside the graph", and the planning-node design's P3
   relies on it. [The graph design](./tillr-graph-is-the-path.md) amends the refusal on the owner's
   direction of 2026-11-17; item graphs and the planning-node design still carry the old text.
2. **[The planning-node design §4](./tillr-planning-node-model-tiering.md#4-kinds-of-work)** puts
   kinds of work that exist only for landing (conflict repair; the fixers for a broken trunk,
   "never planned") into one seat vocabulary. Under this direction those come from the landing
   definition. A reading, not a contradiction of the code.
3. **[The design index, Read this first](../tillr/design-status.md#read-this-first)** describes
   tillr as a loop that "puts one commit per item on the trunk with the **lander**". That is true of
   the code today; this direction describes it as the core plus one definition.
4. **[agent-runtime.md §6](./agent-runtime.md#6-the-task-graph)** warns against workflow-engine
   creep, and the owner describes the core as "queues and DAGs that have agents and other commands".
   Read together, the warnings are about features (cycles, timers, model-written wiring), not about
   generality **[inferred]**.
5. **`D-IG4` in [tillr-item-graphs.md](../reference/tillr-item-graphs.md#why-its-this-way)**
   caps an expanded graph at 100 nodes and a depth of 8, and four criteria carry a cap. The graph
   design's ledger amends it: the caps go. Replacing the four criteria is
   [item 4 of the build plan's next tranche](./tillr-definitions-plan.md#3-the-next-tranche-in-prose),
   which is unfiled.
6. **`D-GR13` in [gate-rework.md](./gate-rework.md#decision-ledger)** stops a fast check still
   running at its ceiling and answers "could not judge", which holds the item until an operator
   clears it; and the lane lease in
   [board-utilities.md §5.6](./board-utilities.md#56-run-alone) lets a reaper free a lane 300
   seconds past a run's wall. Both are clocks whose consequence is a kill or a failure, against the
   owner's direct principle. Both are still in the tree on 2026-11-18 (`LeaseGraceMS` in
   [lease.go](../../tools/tillr-run/run/lease.go), and the gate's "could not judge" exit), and
   removing them is not part of this document or of the build plan.

---

## 8. Building it: the owner's go-ahead and the working choices

On 2026-11-17 the owner told the coordinator: "file item(s) to build as much of the new tillr as
possible so autoqa can play with the real thing and comment for real the next round. rather than
your responses right now." By "autoqa" he means the Product QA pipeline. The items are in
[the build plan](./tillr-definitions-plan.md), and all eighteen have landed.

The build needed six choices. Two are the owner's words; the rest were made by the designer so the
build could proceed, and are his to overturn. The heading says "working choices" because that is
what they were called while the build ran.

| # | Choice | Whose | Ledger |
| :--- | :--- | :--- | :--- |
| 1 | **The one file is shape D:** the vocabulary and rules as data, in a definition file, plus one hook program the core calls for each judgment | The designer's; Product QA chose the same | `D-CG1` |
| 2 | **The lifecycle is the definition's:** the core runs a generic lifecycle, and each definition names its states, its ready state, its finished states and its valid moves, including whether an item may leave a finished state. Leaving one is always an event with a reason and an actor | The owner's: "item lifecycle ... should be part of this ... layer that describes your workflow use case, not the actual core" | [OQ-CG2](#OQ-CG2) |
| 3 | **The words are stand-ins:** "definition" for the kind and `git-landing` for this instance, each in one package and one constant so a rename is mechanical | The owner's since 2026-11-18: "charter" for the kind and `git-landing` for this instance, ruled in the vocabulary; the code keeps "definition" | [OQ-CG5](#OQ-CG5) |
| 4 | **The store records the definition it runs,** at `tillr store init --definition`, and a store with none recorded runs `git-landing`. Every utility opens the store, so they cannot disagree | The designer's | `D-CG2` |
| 5 | **Separation ran beside the drain,** under a safety rule every item carried as a criterion: today's Git-landing queue behaves identically, the existing suites pass unchanged, and a store made before the change opens and drains | The owner's go-ahead; the safety rule is the designer's | [OQ-CG3](#OQ-CG3), `D-CG3` |
| 6 | **Reopening is a definition's policy,** as is graph change; no fixed graph caps; no clock whose only consequence is a kill | The owner's, directly, except reopening, which is reported only ([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)) | `D-CG4` |

### 8.1 What a store that does not land needs from the core

Product QA's request R2-2, a store declared not to land, is the first real slice of the split. The
inventory of [Section 3](#3-where-the-git-landing-assumption-is-wired-in-today) says what it has to
avoid **[read]**:

- **Holds only landing raises.** 14 of the drain's 47 hold reasons: `candidate-no-op`,
  `boundary-scope-escape`, `gate-failed`, `land-refused`, `no-fence`, `fork-held`, `trunk-broken`,
  `conflict-fix`, `conflict-exhausted`, `conflict-design`, `conflict-owner`, `trunk-path-dirty`,
  `trunk-rewritten` and `candidate-record-mismatch`.
- **Git at the loop's start.** The trunk read (`git rev-parse main`), the startup `tillr item
  sync`, which refuses a root that is not a repository's top level, trunk history, the rolling tier,
  and roll and push after a landing.
- **Staging.** A worktree, a base sha and a fence file; `tillr run exec` accepts only the worktree
  as a working directory; and the brief's values name a sha, a fence and a target.
- **The end of a run.** The candidate rule, the boundary bracket, the gate, the lander and the
  move to `landed`.

What stays in the core for every definition: the store and its events, items, criteria, verdicts,
edges and their `blocked_by`, lanes and leases, `run exec`, the planner's general holds, backend
policy and quotas, workflows and their nodes, and the views. The build plan's items took each of
the four lists above in turn, and its last item runs a definition that lands nothing end to end.

### 8.2 What is built, and what is not

Read from the store and the tree on 2026-11-18, with finished-state evidence updated against the
tree on 2026-11-19 **[ran]**, **[read]**, commands in [Evidence](#evidence).

**Built:**

- **The definition file and the store that records it.** [landing.json](../../tools/tillr/definition/landing.json)
  names the states, the ready state, the finished states, the valid moves, the prerequisites, the
  completion rule and retention. `tillr store init --definition <file>` records it, and
  `tillr view waiting` prints `definition: git-landing`.
- **The hook program contract,** in [the package's README](../../tools/tillr/definition/README.md),
  with `tillr-loop` as the landing definition's hook program.
- **A definition that lands nothing,** in [examples/recorded-verdict](../../tools/tillr/examples/recorded-verdict/README.md):
  it runs from an empty directory with no Git repository, and its items finish as `confirmed`,
  `rejected`, `inconclusive`, `repeat-of` or `tooling-fault` on a hook's recorded verdict.
- **A fresh store answers every read face.** After `tillr store init` alone, `tillr view waiting`,
  `tillr drain why` and `tillr run lanes` exit 0.
- **Finished-state evidence and raw-write refusals.** A raw write that would move a landed or
  resolved item is rejected, and moving an item into `landed` or `resolved-elsewhere` requires
  completed criteria and a current landing or graph-completion receipt, or a `--resolves-via`
  pointer (`item-jl-terminal-state-receipts`, landed in `56750a9e0`;
  [terminal_evidence.go](../../tools/tillr-item/item/terminal_evidence.go)).

**Not built:**

- **The ten units of the plan's next tranche:** outcome counts in the views, the detail panel,
  graph change as a definition's policy, the graph caps replaced (the caps themselves are already
  dropped in [workflow_check.go](../../tools/tillr-drain/drain/workflow_check.go), which reports
  graph size and refuses none), a state for an uncertain outside effect, time and tokens per step,
  an owner acknowledgment point, one waiting classifier, the `run-link-missing` test, and the
  `after-completion` hook with a retention rule beyond `keep`. No item exists for any of them: a
  search of `docs` for an item block whose id starts `item-def` finds none, and every such item in
  the store has landed.
- **Most of the landing behavior behind the hook.** Intake, staging, the candidate rule, the
  boundary check, trunk observation and conflicts are still Go in the drain.
- **Product QA's third round** has not arrived.

---

## Evidence

Measured on 2026-11-16 at `6c2af0b32`.

```text
rg -c -i -t go -g '!*_test.go' '<the thirteen terms>' tools/<package>   counts in Section 3.1
  (tools/gate, tools/tillr-loop, tools/tillr-trace counted without -t go, excluding tests and *.md)
rg -o 'Reason… = "…"' tools/tillr-drain/drain (non-test)               47 hold reasons
rg 'CREATE TABLE' per utility                                           land: candidates, landing_attempts, landings;
                                                                        drain: dispatches, node_runs, rolling_state,
                                                                        rolling_runs, trunk_history, gate_spans, …
rg -n -A40 'def loop_command' tools/tillr-roll/tillr-keep               names tillr-loop gate, land, verdict
rg 'Default(Replay|Rolling|FixerGate)Program' tools/tillr-drain/drain   fork-check.py, fork-check.py, tillr-loop gate
wc -l tools/tillr-loop/tillr-loop                                       769
```

Added on 2026-11-17 for round two, on binaries built from `2a35749f6`; the commands and results
are in [the separable-parts Evidence](./tillr-separable-parts.md#evidence):

```text
tillr-drain loop --exit-when-drained, root outside any repository   exit 4 at git rev-parse main
  root inside a repository but not its top level                    exit 2: tillr-item sync refuses it
tillr-drain why on a store with an item that has no paths           held no-fence
rg -n 'only agent is supported' tools/tillr-drain/drain/workflow.go  the loader refuses command stages
```

Read on 2026-11-18 at `068774045`, with the installed binaries, the experimental store a new file
outside this repository:

```text
tillr view status (this repository's store)                     280 items; every item-def-* item landed
tillr store init --store $S; tillr view waiting --store $S      "definition: git-landing ... held []", exit 0
tillr drain why / tillr run lanes --store $S                    exit 0
tillr item state --state landed (criterion open, no landing)    exit 0 (since closed by 56750a9e0)
tillr item state --state in-queue (on that landed item)         rejected: raw writes cannot change a finished item
rg for an item block whose id starts item-def, over docs      nothing: the next tranche is unfiled
wc -l tools/tillr-loop/tillr-loop                               885
git log --oneline -- docs/design/tillr-product-qa-requests.md     newest is the second round; no third
```

**Not determined:** how many lines of the drain move behind hooks; whether the views' landing pages
are separable without a design round; anything about Product QA's code beyond what its reply
reports.

---

## Decision Ledger

Three kinds of row, kept apart on purpose. No question is open in this document.

### The owner's rulings

Each row quotes words the owner said. These are the only rulings in this document.

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-CG2 | The line for item states falls in the definition: the core runs a generic lifecycle and each definition names its own states and moves. The owner: "I guess retention policy, item lifecycle, item prerequisites, that sort of thing should be part of this custom, this layer that describes your workflow use case, not the actual core." | 2026-11-16 | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| OQ-CG3 | Separation was not held behind a proof of the drain on the existing queue: it started at once. The owner: "file item(s) to build as much of the new tillr as possible so autoqa can play with the real thing and comment for real the next round. rather than your responses right now." | 2026-11-17 | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |

### The designer's decisions, awaiting the owner's review

Each row is a decision the designer made so the build of 2026-11-17 could proceed. None is the
owner's ruling, the owner may overturn any of them, and all but the last are already built on.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-CG1 | "Which shape does the one file take?" Closed by the designer's `D-CG1`, not by the owner. | 2026-11-17 | [Section 4](#4-what-one-file-could-mean) | ✅ |
| `D-CG1` | The one file is a data file plus one hook program, and item graphs stay the inside of an item. **Evidence:** it is the seam tillr already enforces, processes and records; Product QA chose the same; and a second definition built this way runs end to end. **Rejected:** the data file alone, which separates words and no behavior; the hook program alone, which hides the vocabulary from the planner and the views; and a workflow graph as a rival shape, which the graph design made a complement instead. The owner may overturn this. | 2026-11-17 | [Section 4](#4-what-one-file-could-mean) | ✅ |
| `D-CG2` | The store records the definition it runs, at `tillr store init --definition`, and a store with none recorded runs `git-landing`. **Evidence:** every utility opens the store, so none can disagree with another. **Rejected:** a flag on each utility. The owner may overturn this. | 2026-11-17 | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| `D-CG3` | The safety rule for separating code the drain runs on: every item carries, as a criterion, that today's Git-landing queue behaves identically, that the existing suites pass unchanged, and that a store made before the change opens and drains. **Rejected:** a freeze on the drain while it is rebuilt, which the owner's go-ahead ruled out. The owner may overturn this. | 2026-11-17 | [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices) | ✅ |
| `D-CG4` | Whether an item may leave a finished state is each definition's policy, stated in its list of valid moves. **Evidence:** the owner's words on this are reported by Product QA's agent and were not heard here ([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)); his direct statement puts lifecycle in the definition, which this follows from. **Rejected:** reopening refused core-wide, as the lifecycle design had it. The owner may overturn this. | 2026-11-17 | [Section 1.1](#11-the-owners-words-as-reported-by-product-qa) | ✅ |
| `D-CG5` | The "Judgment" column of [Section 6](#6-what-this-changes-in-what-was-said-before): which of the nine earlier "no"s are tensions a definition settles and which are protections every definition keeps. The owner may overturn this, row by row. | 2026-11-17 | [Section 6](#6-what-this-changes-in-what-was-said-before) | — |

### Closed without a decision

| ID | What happened | Date | Now lives in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-CG4 | "What should Product QA's agent be asked to react to?" Stale: its second-round reply answered the four things the leaning named, and the question was about briefing another project's agent, not about this design. Nobody ruled on it. | 2026-11-17 | [tillr-product-qa-requests.md](./tillr-product-qa-requests.md) | — |
| OQ-CG5 | "What are the names for the kind of layer, and for the Git-landing instance?" Two decisions in one question, and the first was also being asked in the vocabulary. Not answered here: both were asked there, one each, with "rig" and "charter" carried over as options, and the owner ruled both there on 2026-11-18 (a charter; `git-landing`). "Practice" was dropped; this document's own note on it was "too soft to search for". | 2026-11-18 | [OQ-VO4](./tillr-vocabulary.md#OQ-VO4), [OQ-VO5](./tillr-vocabulary.md#OQ-VO5) | — |
