---
title: "tillr is a core of queues and graphs, and landing changes in Git is one definition on top of it"
status: draft
stage: DESIGN
updated: 2026-11-17
tags:
  - tillr
  - design
  - boundaries
  - product-qa
summary: "The owner's direction for tillr, written down before anything is built: general machinery of queues and graphs whose nodes are agents and commands, with its tracking, and one small, separate definition that turns it into machinery for landing changes in a Git repository. It records the workflow that definition wraps today, measures from the code where the Git-landing assumption is wired in, lays out three shapes the one file could take, and lists the extension points a second use would fill. Round two adds the Product QA pipeline's positions, the owner's words as they reported them and as he stated them directly, his go-ahead to build of 2026-11-17, and the working choices the build plan rests on."
next: "The owner confirms or changes the statements of Section 1.1 and the working choices of Section 8. The build plan in tillr-definitions-plan.md is queued; Product QA's agent writes round three against what it builds."
depends-on:
  - tillr-item-graphs.md
  - tillr-separable-parts.md
  - tillr-product-qa-requests.md
  - tillr-definitions-plan.md
---

# tillr is a core of queues and graphs, and landing in Git is one definition on top of it

> **Delivered copy.** This is `docs/design/tillr-core-and-git-landing.md` from this repository at commit `20c3847b83` (2026-11-17), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-core-and-git-landing.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-17. A draft that writes down a direction. Round two with the Product QA pipeline
is folded in, with the owner's words as they reported them and as he stated them directly
([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)). On 2026-11-17 the owner said to build;
the working choices that build rests on are in
[Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices), and the items are in
[the build plan](tillr-definitions-plan.md). The inventory was measured on the tree at `6c2af0b32` with `rg` over non-test sources,
commands in [Evidence](#evidence). Claims are marked **[ran]**, **[read]** (file named) or
**[inferred]**.

> **In short.** Most of tillr would exist with no Git and no landing. The landing workflow is spread
> across most of the programs and is densest in the drain. One adapter file already injects three
> of its seventeen steps, the gate, the land and the verdict; the rest is Go that would have to move
> behind extension points before "land a change in Git" can be one small definition among others.

**Why it matters.** Product QA wants tillr's queues, runs and tracking without its landing. If the
landing part is one plainly separate definition, Product QA writes a second definition instead of
asking tillr for exceptions.

**Start at [Section 3](#3-where-the-git-landing-assumption-is-wired-in-today).** It is measured;
the shapes in [Section 4](#4-what-one-file-could-mean) are for both sides to react to.

**Needs your ruling:** none before the build continues. Please confirm or correct the statements
Product QA reported in your name ([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)) and the
working choices of [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices), after
the next round. [OQ-CG1](#OQ-CG1), [OQ-CG2](#OQ-CG2), [OQ-CG3](#OQ-CG3) and [OQ-CG5](#OQ-CG5) each
have a working choice in the meantime; [OQ-CG4](#OQ-CG4) can wait.

**Reads with:** [the tillr design index](../tillr/design-status.md) (what every design's status
is), [the build plan](tillr-definitions-plan.md) (the queued items),
[tillr-product-qa-requests.md](tillr-product-qa-requests.md) (Product QA's second-round reply, as
received), [tillr-separable-parts.md](tillr-separable-parts.md) (what can change now, under this
direction), [tillr-item-graphs.md](tillr-item-graphs.md) (graphs inside one item),
[board-utilities.md §5.9](board-utilities.md#59-the-composed-pipeline) (utilities meet only
through processes and records).

---

## The words this document uses

Three terms are coined here, for this document. The last two are **placeholders** until the owner
names them ([OQ-CG5](#OQ-CG5)).

- **The core:** the general machinery. Queues of items, graphs of nodes inside an item where each
  node is an agent or a command, and the tracking behind them: the store, items, criteria, runs,
  leases, events and views.
- **A definition** *(placeholder for the kind)*: the layer that turns the core into one use case:
  its workflow, lifecycle, prerequisites, retention, completion rule and vocabulary.
- **The landing definition** *(placeholder for this instance)*: the definition that lands changes
  in a Git repository, from item blocks in documents through a gate. It does not exist yet as one
  file; today its content is spread across the programs Section 3 names. Product QA's findings store
  would be a second definition. In code the instance's placeholder name is `git-landing`
  ([Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices)).

Two names of other projects. **Product QA** is a product bug-finding pipeline, with the
fuzzer Mangle as its prober; its agent wrote the second round
([tillr-product-qa-requests.md](tillr-product-qa-requests.md)). **AutoQA** is something else:
[tillr-autoqa.md](tillr-autoqa.md), tillr's own design for exploratory testing of tillr. This
document's first draft called Product QA "AutoQA", following the owner's word for it; round two
corrected that, and so does this revision.

Everything else is tillr's existing vocabulary, defined in
[the design index](../tillr/design-status.md#read-this-first): store, item, drain, lane, gate,
lander, fence, criteria. A **step program** is a program the drain runs for one step and reads by
its exit code ([steps.go](../../tillr-drain/drain/steps.go)).

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

By "the AutoQA agent" the owner means the Product QA project's agent (see the words above).

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
([their Section 3](tillr-product-qa-requests.md#3-graph-change) and
[Section 4](tillr-product-qa-requests.md#4-requests-for-this-round)). These are his words **as the
other project's agent reported them**, not heard here; the owner can confirm or correct each.

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

The direct statement agrees with the reported ones, so this document treats all of them as the
owner's direction: no fixed graph caps; no clock whose only consequence is a kill or a failure;
reopening and graph change are a definition's policy, which the core must not preclude. They are
still listed for him to confirm. Statements 4 and 5 cut against accepted designs,
[Section 7](#7-where-the-documents-disagree) says where, and the work for clocks is filed separately
from this document.

---

## 2. The workflow the landing definition wraps, as it runs today

Start to finish, one line each, with the tool and file that owns the step **[read]**.

1. **Intake.** An item block (an HTML comment, `<!-- inert copy of a tillr directive: item … -->`, under a heading of a
   tracked document) is read from the files `git ls-files docs` lists, and `tillr-item sync`
   writes it to the store ([sync.go](../../tillr-item/item/sync.go)). The drain syncs at each
   new trunk commit (`Steps.Sync` in [loop.go](../../tillr-drain/drain/loop.go)).
2. **Plan.** The drain's planner picks ready items in rank order and names a hold for the rest
   ([plan.go](../../tillr-drain/drain/plan.go), [schema.go](../../tillr-drain/drain/schema.go)).
3. **Fence.** The block's `paths` become the item's `allowed_paths`; an item without one is held
   `no-fence` ([item schema.go](../../tillr-item/item/schema.go)).
4. **Lane.** A lane is a leased directory holding a git worktree based on the trunk; staging writes
   the worktree, the brief, the fence and the base sha ([stage.go](../../tillr-drain/drain/stage.go),
   `WorktreeDir` in [request.go](../../tillr-run/run/request.go)).
5. **Run.** `tillr run exec` runs a worker session through a backend adapter the backend policy
   picks by seat ([exec.go](../../tillr-run/run/exec.go),
   [backend-policy.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/backend-policy.json)).
6. **Candidate.** A run whose lane head moved off its base is a candidate; one that did not is held
   `candidate-no-op` (`settle` in [loop.go](../../tillr-drain/drain/loop.go)).
7. **Boundary check.** Refs and worktrees outside the lane's own are snapshotted around the run, and
   any movement stops it; a diff outside the fence is `boundary-scope-escape`
   ([boundary.go](../../tillr-drain/drain/boundary.go)).
8. **Block retirement.** The loop commits the removal of the item's own block before the gate
   ([retire.go](../../tillr-drain/drain/retire.go)).
9. **Gate.** The gate program `tillr-loop gate` runs [change-gate.py](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/change-gate.py):
   fast tree checks through `tillr-gate checks`, then the landing tier of `tools/fork-check.py`,
   within the budget and strategy of [gate-policy.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/gate-policy.json). A red
   check is replayed on the base before the candidate is blamed (`Replay` in
   [steps.go](../../tillr-drain/drain/steps.go)).
10. **Land.** `tillr-loop land` runs `tillr-land`, which takes the land lock, squashes `base..head`
    onto the trunk, writes the marker ref `refs/tillr/landed/<run_id>` before moving the trunk, pins
    `refs/tillr/runs/<run_id>`, and writes a `landings` row binding the gate verdict to
    `(base_sha, head tree)`; a retry finds the marker and does not land twice
    ([landing.go](../../tillr-land/land/landing.go), [trunk.go](../../tillr-land/land/trunk.go)).
11. **Verdict.** Only after the landing, `tillr-loop verdict` records each criterion `done`
    (`Verdict` in [loop.go](../../tillr-drain/drain/loop.go)).
12. **End state.** The item moves to `landed`; other ends are `resolved-elsewhere` with a pointer,
    `closed` with a reason, and `blocked` with a waiter
    ([state.go](../../tillr-item/item/state.go)).
13. **Rolling tier and trunk fixer.** After landings, a rolling check runs over the trunk range; a
    red trunk files a fixer item `fixer-main-<sha>` that runs the full gate, and holds the rest as
    `trunk-broken` ([trunk.go](../../tillr-drain/drain/trunk.go)).
14. **Conflicts and the design seat.** A squash conflict is routed to a repair run on the `design`
    seat, then to the owner (`conflict-fix`, `conflict-design`, `conflict-owner` in
    [blocked.go](../../tillr-drain/drain/blocked.go) and [conflict.go](../../tillr-drain/drain/conflict.go)).
15. **Trunk observation.** The drain records each observed trunk tip under
    `refs/tillr/trunk-history/`, holds on a rewritten trunk, and settles an uncertain landing by
    reading the trunk ([trunk_history.go](../../tillr-drain/drain/trunk_history.go),
    [already_on_trunk.go](../../tillr-drain/drain/already_on_trunk.go)).
16. **The owner's checkout.** The trunk moves in the checked-out worktree by fast-forward only;
    nothing is reset, stashed or discarded, and a path holding the owner's uncommitted content
    stops the move before it starts, holding the item `trunk-path-dirty`
    (`ClashingPaths` in [trunk.go](../../tillr-land/land/trunk.go),
    [dirty_hold.go](../../tillr-drain/drain/dirty_hold.go)).
17. **After the landing.** Optional roll and push actions read from the settings file
    ([settings.go](../../tillr/settings/settings.go)); the keeper restarts the loop with the
    three step programs named ([tillr-keep](../../tillr-roll/tillr-keep), `loop_command`).

**A principle of this workflow, from the owner:** no item waits without a named next step and an
actor who takes it. An under-specified item, such as one with no document block, is not a dead
end: it goes to a triage or design seat, whose output is the missing prerequisite. Another agent
is rewriting the queued [unshared-item hold](../tillr/roadmap.md#hold-an-item-that-has-no-document-block)
along these lines now. It is a candidate principle for the core too, and this document reads it as
one **[inferred]**: "waits on whom, for what" is a property of any queue, and only *which* seat
takes an item is the definition's.

---

## 3. Where the Git-landing assumption is wired in today

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
| Store (`tools/tillr`) | store, composed schema, records, events, exit codes | roll and push after a landing ([settings.go](../../tillr/settings/settings.go)) | hard-coded settings keys | small |
| `tillr-store` | init, check, recover, checkpoint, events | none | — | nothing |
| `tillr-item` | items, criteria, edges, decisions, mail, grants, verdicts, close and reopen | the states `landed` and `resolved-elsewhere` ([state.go](../../tillr-item/item/state.go)); the `allowed_paths` column; sync reading `git ls-files docs` ([sync.go](../../tillr-item/item/sync.go)) | hard-coded state names; a schema column; a git call | a design round for states; small for the rest |
| `tillr-run` | lane leases, `run exec`, adapters, end causes, usage | the run's only working directory is the lane's git worktree (`WorktreeDir`, [request.go](../../tillr-run/run/request.go)); the adapters' directory found from the git common dir | a constant; a flag default | small |
| `tillr-drain` | planner, general holds, dispatch, backend policy and fail-over, quotas, retries, passes, workflows and `node_runs`, designer sessions, open-question holds | the `Candidate` of worktree, base and head; staging a worktree; the boundary bracket; block retirement; the rolling tier and fixer; trunk history; conflicts; the dirty-checkout hold; 14 of its 47 hold reasons ([schema.go](../../tillr-drain/drain/schema.go) and neighbors) | six step programs on its argv (gate, land, verdict, replay, rolling, fixer gate); the rest is Go | a design round |
| `tillr-gate` | a check runner: registry, plan, run, tiers, budgets, result memory | change checks read `git diff base..head` ([change.go](../../tillr-gate/change.go)); memory is keyed by tree | check registry files and flags | small |
| `tools/gate` | — | this repository's own gate ([change-gate.py](https://github.com/mschulkind-oss/tillr/blob/main/tools/gate/change-gate.py), `checks/*.json`) | the gate step program | nothing: it is already definition content |
| `tillr-land` | the land lock | all of it: squash, marker and pin refs, `landings`, `candidates`, `landing_attempts`, author identity, the owner-checkout clash | the land step program | nothing: already a separate program |
| `tillr-loop` | the adapter pattern | gate, land and verdict, translated onto the loop's exit contract | named on the drain's argv by the keeper | nothing |
| `tillr-view` | status, events, passes, lanes, running, waiting, trace, serve | the landings and trunk pages, gate spans, waiting-face rules for landing holds | Go and the web front end | small to a design round |
| `tillr-trace` | indexing run records | indexing gate and land records | hard-coded | small |
| Keeper (`tillr-roll`) | restart a loop, one lock per root | the roll after a landing; `loop_command` names `tillr-loop`'s three faces | hard-coded | small |
| Item block and sync | `id`, `blocked_by`, `seat`, `workflow`, `prose` | `paths` as the fence; documents are git-tracked files under `docs/`; a landed item's block is retired | hard-coded grammar | small |
| Policy files | [backend-policy.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/backend-policy.json): seats to backends | [gate-policy.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/gate-policy.json): landing strategy, red cap, lane base | policy entries | nothing |
| Workflows (`config/tillr/workflows/`) | stages, seats, access, edges ([scout-then-build@v1.json](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows/scout-then-build@v1.json)) | none in the file; the landing runs after the exit, by rule ([item-graphs §5](tillr-item-graphs.md#5-what-a-node-may-never-be)) | — | a design round, if landing becomes nodes |
| `tillr-operating` skill and the pack | store, item, run and view faces | the seven-step loop ends in land, verify, audit ([SKILL.md](https://github.com/mschulkind-oss/tillr/blob/main/yolo-packs/tillr/skills/tillr-operating/SKILL.md)) | prose | small |

**Lifecycle, prerequisites and retention, classified by the owner's rule** **[read]**:

- **Lifecycle belongs to the definition.** The writable states (not started, in queue, blocked,
  landed, resolved elsewhere, closed), the rule that `landed` and `resolved-elsewhere` are final,
  reopen-only-from-`closed`, and the verdict words `done`, `not-done`, `blocked`, `defective` are
  hard-coded in [apply.go](../../tillr-item/item/apply.go),
  [lifecycle.go](../../tillr-item/item/lifecycle.go) and
  [verdict.go](../../tillr-item/item/verdict.go). The core keeps only that an item has a
  state from a closed set and that each move is an event.
- **Prerequisites of Git landing**, today holds in the planner: `no-fence` (no allowed paths),
  `question-open`, `questions-unknown` and `design-doc-missing` (the design document must exist
  and have no open question), `not-ready` (the item is not in the queue), `trunk-path-dirty`,
  `conflict-design` and `conflict-owner`, the fork and trunk holds, and the queued
  `no-document-block`. **General queue mechanics:** `no-lane`, `run-live`, `no-policy`,
  `backend-window`, `seat-degraded`, `failover`, the retry and readmission holds, and `blocked` by
  an unfinished dependency. The dependency edge is the core's; what counts as finished (landed) is
  the definition's.
- **Retention has no rule today.** Run pins `refs/tillr/runs/<run_id>` and landing markers
  `refs/tillr/landed/<run_id>` are written and never deleted; the only ref tillr deletes is
  `refs/tillr/trunk-history/lost` ([trunk_history.go](../../tillr-drain/drain/trunk_history.go)).
  The event log is append-only, and each run's record directory and log are kept. No code prunes
  any of them (`rg 'update-ref", "-d"|DELETE FROM'` finds only those and row rewrites), so they are
  kept forever by nobody's rule. [Bounded agent storage](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/bounded-agent-storage.md) designs
  reclaiming, unbuilt.

### 3.3 How close `tillr-loop` already is to the one file

[tillr-loop](https://github.com/mschulkind-oss/tillr/blob/main/tools/tillr-loop/tillr-loop) is one 769-line Python file that supplies the
gate, land and verdict step programs, and the drain's defaults name it again for the fixer gate
(`DefaultFixerGateProgram` in [trunk.go](../../tillr-drain/drain/trunk.go)) **[read]**. It
already injects the three judgments, so the drain "owns the order and the stopping conditions and
never a step's judgment", as its header says.

What it does not cover: intake, the fence, staging a worktree, the candidate rule, the boundary
bracket, block retirement, the replay and rolling programs (which default to `fork-check.py`), trunk
observation, conflicts, the item's end states, the hold vocabulary, and the views. Those are Go in
the drain, the item face and the view. By the step count of Section 2, it covers three of seventeen
steps, and those three are the densest by line **[inferred]**.

---

## 4. What "one file" could mean

Three shapes, laid out for reaction. None is chosen.

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

**(b) One program that answers the core's hooks**, the adapter grown up, still a process
([board-utilities.md, the composed pipeline](board-utilities.md#59-the-composed-pipeline)):

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

**(c) A workflow graph in the item-graphs design's own terms**, where landing is one workflow:

```text
git-landing@v1:  work (agent, seat build, write)
                 → gate (command, gate)  → land (command, land)  → verdict (command)
                 exit: requires_candidate commit_required
findings@v1:     recheck (command) → investigate (agent, read_only) → verify (agent, evaluator)
                 exit: no_commit
```

It makes "land a change" one workflow among others, and Product QA's finding pipeline another, with
command nodes ([the queued block](tillr-item-graphs.md#add-command-nodes-that-run-a-registered-program))
and exit conditions doing the work. It runs into
[item-graphs §5](tillr-item-graphs.md#5-what-a-node-may-never-be), which refuses "a landing or a
ref move inside the graph", and it leaves intake, staging and trunk observation outside any graph.
[The planning-node design](tillr-planning-node-model-tiering.md#2-principles) builds on the same
graphs and keeps the landing outside them ("the queue, the fence, the gate and the landing still see
one item", its principle P3).

**Which way the existing designs lean.** The step programs and the process-and-record seam lean toward (b): judgments
are processes the caller names. The item-graph design leans toward a partial (c): a workflow names
its own completion (`commit_required`, `commit_optional`, `no_commit`), while the landing stays one
act after the exit **[read]**. Nothing leans toward (a) alone; it reads as the vocabulary half of
(b) **[inferred]**.

**The second party's position.** Product QA chose option D of [OQ-CG1](#OQ-CG1), a data file plus
one hook program, as "how we already compose tools", and option A of [OQ-CG2](#OQ-CG2)
([their Section 2](tillr-product-qa-requests.md#2-reactions)). Both match this document's leanings,
and both are now working choices ([Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices)).

**What each implies for the eight "no"s** of
[the separable-parts proposal](tillr-separable-parts.md#13-what-tillr-will-not-change), as a
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
[their Section 2](tillr-product-qa-requests.md#2-reactions), and none needed a row this table does
not have.

| Extension point | Git landing fills it with | Status |
| :--- | :--- | :--- |
| Intake | item blocks in tracked documents, through sync | exists; `tillr item apply` also exists; a store not fed by documents is not held for lacking blocks, queued as `item-sync-hold-unshared-items` (its c1 and c5) |
| What a node is | an agent | agent exists; command queued as `item-ig-command-node` |
| Where a node runs | a git worktree in a lane | exists, as a constant; "no worktree" would be new |
| What completes a node | the run ends and passes | exists; declared outputs queued as `item-ig-node-outputs` |
| What completes an item | a landing | exists; finishing with no commit queued as `item-ig-exit-condition`; "an artifact recorded and judged" would be new |
| Lifecycle | the seven states, terminal `landed` and `resolved-elsewhere`, reopen only from `closed` | exists, hard-coded in the item face; suppliable by the definition would be new |
| Prerequisites | a document block, a fence, a design document with no open question, finished dependencies, a clean owner path | exists as planner holds in Go; the definition naming them, and a next step for each, would be new |
| Retention | nothing is pruned | would be new: what is kept, for how long, by the definition's rule |
| Outcome vocabulary | seven states, close reasons and pointers | exists; per-definition words would be new |
| Dispatch policy | lane count, seats, backend policy | exists; seat shares queued as `item-policy-seat-shares`, lanes outside the cap as `item-drain-express-lanes`, a spend ceiling as `item-drain-spend-ceiling`; deadlines would be new |
| The judge | the gate program and the verdict program | exist; a judge before the gate queued as `item-jl-judge-before-gate`, an evaluator node as `item-ig-evaluator-node` |
| Trunk observation | the rolling program, trunk history | exists, as step programs and Go |
| After completion | roll and push settings | exists |
| The views' vocabulary | landings and trunk pages, landing holds | exists, hard-coded; from a definition would be new |
| An owner acknowledgment point | none | would be new |

**An item with no document block** is, in the idea pipeline's terms, an idea that entered by a
side door: [the idea pipeline](tillr-idea-pipeline.md#2-the-pipeline-and-one-writer-rules)
already routes a rough request through intake, scouting and design to a queued item, so the natural
next step for an undeclared item is that pipeline's design stage, whose output is the block
**[inferred]**.

---

## 6. What this changes in what was said before

[The separable-parts proposal](tillr-separable-parts.md) listed eight requests tillr would not
take, each against a settled ruling, plus the undeclared-item hold. The owner's decision rule
([Section 1](#1-the-direction-in-the-owners-words), item 6) reads each "no" as a tension between
Git landing and Product QA, and so as a candidate for what a definition supplies. Two parts are the
owner's direction, not a reading: outcomes defined per item kind (Product QA's first-round request 3)
and reopening from a finished state (its request 4) belong to the definition, because the owner put
lifecycle there, and his reopening statement as Product QA reports it
([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)) says the same.

The "Judgment" column is **this document's judgment**, for the owner to confirm: where a "no" is
not a real tension but a protection every definition needs, it says why. The last column is
**Product QA's answer in round two**: four withdrawn and five real, with C3 narrowed. No ruling is
rewritten here.

| "No" | Ruling it rested on | A tension? | Judgment | Product QA, round two |
| :--- | :--- | :--- | :--- | :--- |
| C1 Outcomes per kind | The closed seven states, one projector ([D-IP1](tillr-idea-pipeline.md#61-settled-decisions)) | Yes | The definition's, by the owner's direction. The protection that stays in the core: each definition's set is closed and projected one way | Real: an inconclusive finding must be countable |
| C2 Reopening a finished item | Terminal `landed` and `resolved-elsewhere` ([lifecycle §3.2](tillr-judge-and-lifecycle.md#32-transition-preconditions-and-attempt-holds-d-jl4)) | Yes | The definition's, by the owner's direction. Stays true inside Git landing. Core protection: a move out of a finished state is an event, never silent | Real: the owner, as reported, rules it a definition's policy |
| C3 Steps reshaping the graph | Bounded replacement only; no model-written wiring (`D-IG3`, `D-IG5`, `D-IG9`) | Partly | Which reshaping operations exist is the definition's. That each is applied by one writer, atomically with its event, and never to a node that has started, is a protection every use needs: without it the record lies | Real, narrowed to the four rules all three designs share: one writer applies each change atomically with its event, no node replaces itself, nothing started changes, and every change passes the downstream-shape check. Free rewriting is withdrawn |
| C4 An uncertain outside effect as a state | No node changes anything outside its worktree ([item-graphs §5](tillr-item-graphs.md#5-what-a-node-may-never-be)) | Yes | The worktree rule is Git landing's. Core protection: an effect a definition declares is recorded before and after, so a retry can see it | Real: a report send that lost its session |
| C5 Clocks and deadlines in the plan | No timers ([agent-runtime.md §6](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#6-the-task-graph)) | Partly | A deadline as an ordering input of dispatch policy is a tension and the definition's. The planner staying a function of the store, replayable, is a protection | Withdrawn: the time box is the fuzzer run's own |
| C6 A Go library as the connection | Processes and records ([board-utilities.md §5.9](board-utilities.md#59-the-composed-pipeline)) | No | A protection: it is how a definition injects anything without linking the core | Withdrawn: processes and records are enough |
| C7 Completion that is not a landing | One item, one atomic change (`D-IG2`) | Yes | The completion rule is the definition's; Git landing's is a landing. Core protection: an item completes once, on a recorded receipt | Real: completion on a recorded, judged verdict |
| C8 A second loop in one root | None; how the keeper finds a loop | No | Not a tension. One writer per store and at-most-once launch of a run are protections; a second use runs its own store and root | Withdrawn: it runs its own store and root |
| The undeclared-item hold | Sync is the only intake ([tillr-convergence.md §3](tillr-convergence.md#3-the-target)) | Yes | A prerequisite, so the definition's, and already narrowed to stores documents feed. The owner's next-step principle applies in every definition | Withdrawn: its stores are fed by `apply`, which the rewritten hold leaves alone |

---

## 7. Where the documents disagree

1. **Shape (c) against [item-graphs §5](tillr-item-graphs.md#5-what-a-node-may-never-be).** The
   design refuses "a landing or a ref move inside the graph", and the planning-node design's P3
   relies on it. That is a fork in the direction, not an error in either document.
2. **[The planning-node design §4](tillr-planning-node-model-tiering.md#4-kinds-of-work)** puts
   kinds of work that exist only for landing (conflict repair; the fixers for a broken trunk,
   "never planned") into one seat vocabulary. Under this direction those come from the landing
   definition. A reading, not a contradiction of the code.
3. **[The design index, Read this first](../tillr/design-status.md#read-this-first)** describes
   tillr as a loop that "puts one commit per item on the trunk with the **lander**". That is true of
   the code today; this direction describes it as the core plus one definition.
4. **[agent-runtime.md §6](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md#6-the-task-graph)** warns against workflow-engine
   creep, and the owner describes the core as "queues and DAGs that have agents and other commands".
   Read together, the warnings are about features (cycles, timers, model-written wiring), not about
   generality **[inferred]**.
5. **`D-IG4` in [tillr-item-graphs.md](tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)**
   caps an expanded graph at 100 nodes and a depth of 8, and four queued criteria carry a cap. The
   owner, as Product QA reports him, refuses fixed caps (statement 4 of
   [Section 1.1](#11-the-owners-words-as-reported-by-product-qa)). The build plan's next tranche
   replaces them ([item 4 there](tillr-definitions-plan.md#3-the-next-tranche-in-prose)).
6. **`D-GR13` in [gate-rework.md](gate-rework.md#decision-ledger)** stops a fast check still
   running at its ceiling and answers "could not judge", which holds the item until an operator
   clears it; and the lane lease in
   [board-utilities.md §5.6](board-utilities.md#56-run-alone) lets a reaper free a lane 300
   seconds past a run's wall. Both are clocks whose consequence is a kill or a failure, against the
   owner's direct principle and statement 5. That work is filed separately.
7. **This document's first draft** treated [tillr-autoqa.md](tillr-autoqa.md) as Product QA's
   design, through the word "AutoQA", and its shape (c) used that design's `qa-probe@v1` as Product
   QA's graph. Corrected on 2026-11-17: the sketch names a findings workflow instead.

---

## 8. Building it: the owner's go-ahead and the working choices

On 2026-11-17 the owner told the coordinator: "file item(s) to build as much of the new tillr as
possible so autoqa can play with the real thing and comment for real the next round. rather than
your responses right now." By "autoqa" he means the Product QA pipeline. The items are in
[the build plan](tillr-definitions-plan.md), a separate document so that this one's open
questions do not hold them.

Code needs answers to questions the owner has not ruled. Each choice below is a **working choice,
to be confirmed or changed by the owner after the next round**:

1. **The one file is shape D** ([OQ-CG1](#OQ-CG1)): the vocabulary and rules as data, in a
   definition file, plus one hook program the core calls for each judgment. Recommended here and
   chosen by Product QA.
2. **The lifecycle is option A** ([OQ-CG2](#OQ-CG2)): the core runs a generic lifecycle, and each
   definition names its states, its ready state, its finished states and its valid moves, including
   whether an item may leave a finished state and from which. Leaving one is always an event with a
   reason and an actor. Recommended here and chosen by Product QA.
3. **The words are placeholders** ([OQ-CG5](#OQ-CG5)): "definition" for the kind and `git-landing`
   for this instance, in file names, flags and record fields. They were chosen so that a later
   rename is mechanical, and every item keeps each word in as few places as possible: one package
   and one constant.
4. **The store records the definition it runs**, at `tillr store init --definition`, and a store
   with none recorded runs `git-landing`. Every utility opens the store, so they cannot disagree.
5. **Separation starts now** ([OQ-CG3](#OQ-CG3)), under a safety rule every item carries as a
   criterion: today's Git-landing queue behaves identically, the existing suites pass unchanged,
   and a store made before the change opens and drains. The drain runs on this code while it is
   rebuilt.
6. **The owner's reported statements are direction** ([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)):
   no fixed graph caps, no clock whose only consequence is a kill, and reopening and graph change
   as a definition's policy. They are still listed for him to confirm.

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
policy and quotas, workflows and their nodes, and the views. The build plan's items take each of
the four lists above in turn, and its last item runs a definition that lands nothing end to end.

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
are in [the separable-parts Evidence](tillr-separable-parts.md#evidence):

```text
tillr-drain loop --exit-when-drained, root outside any repository   exit 4 at git rev-parse main
  root inside a repository but not its top level                    exit 2: tillr-item sync refuses it
tillr-drain why on a store with an item that has no paths           held no-fence
rg -n 'only agent is supported' tools/tillr-drain/drain/workflow.go  the loader refuses command stages
```

**Not determined:** how many lines of the drain move behind hooks; whether the views' landing pages
are separable without a design round; anything about Product QA's code beyond what its reply
reports.

---

## Open Questions

The second round with Product QA's agent has happened, and the owner said to build. Each question
below either has a working choice the build rests on
([Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices)) or can wait; none holds
the build, whose items live in [the build plan](tillr-definitions-plan.md).

1. 💬 **OQ-CG1: Which shape does the one file take?**

   Three shapes are laid out in [Section 4](#4-what-one-file-could-mean): a data file the core
   reads, one program that answers the core's hooks, or a workflow graph in which landing is nodes.
   Each separates a different part, and they combine.

   - **A — A declarative definition** for the vocabulary only.
   - **B — One hook program**, the adapter grown up.
   - **C — A workflow graph**, which reopens
     [what a node may never be](tillr-item-graphs.md#5-what-a-node-may-never-be).
   - **D — A data file plus one hook program**, with the item graphs left as the inside of an item.

   Product QA's position in round two: D. **Working choice: D**, to be confirmed or changed by the
   owner after the next round.

   <!-- vantage: question id=OQ-CG1 leaning="D — the vocabulary is data and the judgments are processes, which is the seam the design already enforces, and graphs stay inside items." -->

   _Leaning:_ D — the vocabulary is data and the judgments are processes, which is the seam the
   design already enforces, and graphs stay inside items.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-CG2: Where does the line between the core and the landing definition fall for item states?**

   `landed` and `resolved-elsewhere` are landing words, hard-coded in the item face. Product QA's
   finished findings are confirmed, rejected, inconclusive, a repeat of another, or a tooling fault.

   - **A — The core owns a generic lifecycle** (not started, queued, blocked, finished, closed) and
     each definition names its finished states and which are terminal.
   - **B — The seven states stay core-wide**, and other definitions map onto them.

   Product QA's position in round two: A, and the owner's reopening statement as they report it
   ([Section 1.1](#11-the-owners-words-as-reported-by-product-qa)) extends A to which moves are
   valid. **Working choice: A**, to be confirmed or changed by the owner after the next round.

   <!-- vantage: question id=OQ-CG2 leaning="A — it keeps one projector and a closed set per definition, which is what D-IP1 protects." -->

   _Leaning:_ A — it keeps one projector and a closed set per definition, which is what D-IP1
   protects.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-CG3: Does the drain proof on the existing queue come first, and what may be built meanwhile?**

   The code that would be separated is the code the drain proof exercises: the drain holds 1758 of
   the coupling's occurrences, 375 of them in `loop.go`.

   - **A — The drain proof first.** Meanwhile: documentation, the small changes of the
     separable-parts proposal, and queued items in queue order, but no move of drain code behind
     hooks.
   - **B — Start separating now**, beside the drain proof.

   The owner's go-ahead of 2026-11-17, "build as much of the new tillr as possible", reads as B.
   **Working choice: B**, with the safety rule of
   [Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices): every item leaves
   today's queue behaving identically.

   <!-- vantage: question id=OQ-CG3 leaning="A — refactoring the loop while it drains the queue changes the thing under test, and the queued command nodes and exit conditions already move toward the split." -->

   _Leaning:_ A — refactoring the loop while it drains the queue changes the thing under test, and
   the queued command nodes and exit conditions already move toward the split.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-CG4: What should Product QA's agent be asked to react to?**

   Its reply starts the second round, and it will answer what it is asked.

   - **A — Four things:** the three shapes, the extension points of
     [Section 5](#5-the-extension-points) with one concrete case from its prototype for each it
     needs, the provisional "no"s, and the stable surface.
   - **B — An open review** of every tillr design.

   Round two answered option A's four things. **Can wait**: the next round reacts to what is built.

   <!-- vantage: question id=OQ-CG4 leaning="A — a concrete case per extension point is what turns a shape into a choice." -->

   _Leaning:_ A — a concrete case per extension point is what turns a shape into a choice.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-CG5: What are the names for the kind of layer, and for the Git-landing instance?**

   There will be more than one use, so both need a word. "Workflow" is taken by the item-graph
   design, "policy" by the backend policy, "pack" and "profile" by yolo, and "mode" by the autonomy
   and recovery modes. `rg -w -i` over the tillr tools, the tillr designs and their configuration
   finds no use of rig, charter, berth or keel. For the kind:

   - **A — rig:** a ship's rig is what sets one hull up for one purpose; could be read as a test rig.
   - **B — charter:** the stated purpose and terms of a voyage; could be read as a legal document.
   - **C — definition:** plain, the placeholder; collides with "item definition" in sync messages.
   - **D — practice:** a way of working; too soft to search for.

   For the instance:

   - **E — git-landing:** says what it does; plain beside a future `findings`.
   - **F — trunk-landing:** names the trunk; less clear to a reader who does not know tillr's words.
   - **G — docs-to-trunk:** names both ends; long.

   Product QA has no preference, only that the kind and the instance be told apart. **Working
   choice for code:** the placeholders `definition` and `git-landing`, kept in one constant each so
   that the owner's ruling is a mechanical rename
   ([Section 8](#8-building-it-the-owners-go-ahead-and-the-working-choices)).

   <!-- vantage: question id=OQ-CG5 leaning="A and E — rig sits beside lane, seat and gate without colliding, and git-landing says what the instance does." -->

   _Leaning:_ A and E — rig sits beside lane, seat and gate without colliding, and git-landing says
   what the instance does.

   **Answer:**
   > _(empty — fill in when decided)_
