---
title: "An oversized item lands in parts: what is accepted lands, and what is objected to goes back alone"
status: accepted
stage: DECIDED
tags: [tillr, drain, verify, landing, split, design]
summary: "A change too big to land whole is broken up so the accepted parts land without the part objected to. Four callers may call a split and the design seat alone writes the division; each part is an ordinary item under a parent that stays open until a closing check passes. A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on. Slices 1, 2 and 4 of the build are in the tree; slices 3 and 5 to 12 are not."
next: "Build slice 3 (earlier findings' fates and what was examined), then slices 5 to 12 of Section 14. Slice 8 may route on the measured signal S1 alone."
depends-on:
  - landing-principles.md
  - tillr-judge-and-lifecycle.md
  - ../reference/tillr-item-graphs.md
vantage:
  status-chip: true
---

# An oversized item lands in parts

**Status:** Three of twelve build slices are in the tree ([§14](#14-the-build-in-slices-that-each-land-alone)): every refused candidate's size is recorded beside its limits, a not-done verdict states each finding's kind and place, and a split statement written by the design seat is checked and filed in one transaction. Nothing yet calls a split automatically. The owner's statements quoted in [§1](#1-the-ruling) are the only rulings; every other decision is the designer's (or from an operator request), which the owner may overturn, and is marked so in the [Decision Ledger](#decision-ledger).

> **In short.** An item that is too big is split into **parts**, each an ordinary item with its own words, verifier, gate and single squashed commit, under a **parent** that keeps the owner's words and stays open until every part has landed. The work already built is taken from the refused candidate and not rebuilt, and what decides whether a part may land is whether it makes the trunk worse, not whether the whole request is finished.

**Why it matters.** An oversized item can be refused by its verifier again and again, each time on a different case, while its candidate grows. Nothing lands, and the verifier has no way to say that the rest is fine ([§2](#2-the-case-measured)).

**The shape.** Four callers and one divider. The operator at filing, the design seat, the planner and the verifier each **call** a split, only on signals whose triggers are measured from the store; the design seat alone writes the division; the drain files the parts.

**Cost.** One more design run per split, one verify and one gate per part, and a trunk that may carry a behavior whose remaining cases are still queued. The seams between parts become a place where a defect can hide, which is what the closing check in [§7.5](#75-when-the-parent-is-finished) is for.

**Start at** [§4](#4-the-rules-a-split-keeps), the seven rules, then [§8](#8-how-a-verdict-accepts-part-of-a-candidate).

**Needs your ruling:** None.

**Awaits your review:** the designer's decisions `D-SP1` to `D-SP16` in the [Decision Ledger](#decision-ledger).

**Reads with:** [landing principles](./landing-principles.md) (the rules a split must not break), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md) (verdicts, and the rule that no count of attempts routes work) and [item graphs](../reference/tillr-item-graphs.md) (pieces inside one item, which a split does not replace).

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, criterion, candidate, lane and verdict. These are coined in this document:

- **Split**: replacing one item's single landing with several items that land separately. It is not the planner's division of an item into pieces, which stays inside one item and one landing.
- **Split call**: a recorded statement that an item should be split, naming who made it and the measured signals behind it. It decides nothing: the design seat answers it.
- **Split statement**: the design seat's answer to a call. It lists the parts, divides the criteria and says which built work each part takes. Its other possible answer is **no split**, with the reason.
- **Part**: an ordinary item filed from a split statement. It is not a piece: a piece is a step inside one item.
- **Parent**: the item that was split. It keeps the owner's words and its criteria, is never built again, and waits on its parts.
- **Donor candidate**: the parent's last candidate, kept at a Git ref, from which parts take work. It is not a verified candidate of any part.
- **Gap**, **regression**, **unasked change**: the three kinds of verifier finding, defined in [§8.1](#81-what-the-verifier-states).
- **Closing check**: the one read-only verification of the parent's own criteria against the trunk, after its last required part lands.

---

## 1. The ruling

The owner ruled on 2026-11-21 (`R-SP1`):

> "We need a way to protect against these mega items. We can change policies about filing them and try to protect that way, but inevitably ones will skip, ones will slip through, so we need a strategy for landing these large changes, which means that after some threshold or something, we just need to break them up. […] if there's a change that's really five changes, we need to be able to land four of them without the one that's been objected against. […] Maybe like the designer can say split up, the planner can say split up, the judge can say split up at various points. But they should have clear guidance."

What that rules:

1. Filing policy alone is not the protection. Oversized items will get through, so the drain needs a way to land them.
2. Past some threshold the change is broken up.
3. The accepted parts of a change land without the part that is objected to.
4. Whoever may say "split" has clear guidance.

Asked whether part of a change may land while a named case of the request is still unmet, the owner ruled the same day ([`OQ-SP1`](#OQ-SP1)):

> "part of designs can absolutely land. this is generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the smallest functional change possible to make reviewing, testing, velocity, all this stuff easier, and it will also make it easier for us."

5. Part of a change may land while a named case of the request is still unmet. The case follows as its own part.
6. The smaller landing is the healthier one ([landing principle 15](./landing-principles.md#15-land-the-smallest-functional-change)).

The first statement leaves open which seats say "split" and when, what the threshold is, and whether a split is several landings of one item or something else. Those are settled below as the designer's decisions.

A third ruling bounds the thresholds (`R-SP2`, 2026-11-23): the owner objected to a size limit whose origin nobody could state, "we have random thresholds again breaking things?" His standing rule is that no arbitrary time limit, retry count or attempt limit routes or stops work ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). Applied here (`D-SP15`): **a signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on.**

## 2. The case, measured

The design rests on two measurements from the store and the repository.

**One oversized item.** A `planned-build@v1` item with seven criteria was refused by its verifier twelve times in a row. `git diff --shortstat <base> <head>` for each refused head, excluding the derived pack copy under `yolo-packs/`, shows the candidate growing at every refusal: 34 files and 2,189 added lines at the first, 64 files and 5,427 added lines at the twelfth, across four tool directories. Each refusal named a case the earlier ones had not.

**What a normal landing looks like.** The same measurement over the trunk commit of each of 1,049 landings the store records:

| Measure | Median | 90th percentile | 95th | 99th |
| :--- | ---: | ---: | ---: | ---: |
| Files changed | 2 | 19 | 27 | 45 |
| Lines added | 75 | 896 | 1,198 | 2,394 |
| Distinct code areas (`tools/<module>`, `docs/<dir>`, and the like) | 1 | 4 | 5 | 8 |

**Size and refusals go together, on a small sample.** Of 95 landed `planned-build@v1` items, the 19 that landed above the 95th percentile on files or added lines averaged 3.3 verifier refusals; the other 76 averaged 1.1. This measures the size that landed, not the size at the first refusal.

Four observations follow:

1. **The first refused candidate was already past the 95th percentile on both measures.**
2. **Each refusal named a different case.** The repeated-finding hold fires only when the same finding repeats on unchanged evidence ([the drain's reference](../../tools/tillr-drain/README.md)), so twelve different findings moved nothing.
3. **The criteria recorded none of what was accepted.** Every refusal marked the first criterion not done. A verdict that is one entry per criterion has no place to say which part of the candidate a finding is about, or that the rest is sound.
4. **The planner has no guidance on size.** `rg -n -i 'split|size|independent|large' config/tillr/plan-guidance.md` finds nothing.

## 3. What exists today

| Mechanism | What it does | What it cannot do |
| :--- | :--- | :--- |
| Pieces (`planned-build@v1`, `build-pieces@v1`) | The planner divides an item into at most 12 pieces that run in order on one lane | Land separately: all pieces share one verifier, one gate and one squashed commit ([item graphs](../reference/tillr-item-graphs.md#lane-lifecycle-write-scopes-and-chained-commits)) |
| `blocked_by` edges | One item waits on another ([the item utility's guide](../../tools/tillr-item/README.md)) | Say that several items are one request |
| Verifier return | A not-done verdict sends the findings to the build step on the same candidate | Keep any of the candidate out of the next verdict |
| Repeated-finding hold | Holds an item when the same finding repeats on unchanged evidence | Notice findings that differ every time |
| Run pins (`refs/tillr/runs/<run>`) | Keep a run's committed head | Say which of that work a later item should take |
| The deny-list | Refuses any worker's change to the store, the fork caches or an item declaration | Limit an item to paths: there is no per-item path list |
| A filed split statement (`FileSplit`) | Files the parts, their order and the blocked parent in one transaction | Call a split, carve a donor, or close the parent ([§14](#14-the-build-in-slices-that-each-land-alone)) |

An item's states are listed in [item states](../reference/tillr-item-states.md).

## 4. The rules a split keeps

- **P1. A split changes the shape of the work, never its bar.** The parent keeps the owner's words and its criteria unchanged. It is finished only when every active required part has landed and the closing check passes. Only the owner drops a required part without replacement; a required part closed as defective is replaced by the design seat ([§7.6](#76-failure-and-edge-cases)).
- **P2. A part is an ordinary item.** It has its own words, its own criteria, one verifier, one gate and one squashed commit. The gate and the lander do not change.
- **P3. One seat writes the division.** Four callers may call a split. The design seat alone decides whether and how, because dividing criteria is writing the bar, and neither the worker that is held to a bar nor the judge that applies it may write it ([criterion repair](./criterion-repair.md#decision-ledger) rules the same for a frozen criterion).
- **P4. A measured signal calls a split; nothing here stops work.** No signal counts attempts or refusals. A call never holds the item on the owner.
- **P5. Built work is taken, not rebuilt, and no verdict is inherited.** A part takes its share from the donor candidate. It is then verified and gated on the tree that will land, like any item.
- **P6. On a part, a gap is filed and a regression is refused.** A part is refused for making the trunk worse, for changing what nobody asked for, for lacking its tests, or for not delivering the share it claims. A case of the parent's request that the part does not yet handle is recorded against the parent and built by another part ([`OQ-SP1`](#OQ-SP1)).
- **P7. A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on.** A threshold that routes work must come from a measurement in the store, and one that has none stays an advisory displayed fact (`D-SP15`).

## 5. Who calls a split, and when

A split call is made at four points. Each caller states facts in fixed fields; the drain measures what can be measured ([§6](#6-the-signals)). A call that routes an item to the design seat may rest only on a **measured** signal, and names the caller, the time, and each measured signal with its value and its threshold. **Advisory** signals are recorded and shown without recording a call or changing what runs next.

| Caller | The point | How the call is made | What happens to the item meanwhile |
| :--- | :--- | :--- | :--- |
| **Operator, at filing** | When filing the request, before any run | Filing records the facts for `S6`, `S8`, `S9` and `S10`, and records a call only when a measured filing signal holds | On a call, the item goes to the design seat first and no plan or build run starts; otherwise the readings are shown and the item proceeds |
| **Design seat** | While writing a design that will be built, and whenever an item is in its hands for another reason | It writes the split statement directly; no separate call is needed | For a design, the build request is filed with the statement, so the parts are filed at once |
| **Planner** | At the plan step, before any writing run | It records `S5`, `S6` and `S7` on its plan, and may end its run with a call in place of a piece list, with its proposed grouping attached, only when a measured plan signal holds | On a call, no build run starts and the item goes to the design seat; otherwise the piece list runs |
| **Verifier** | At a not-done verdict | It states each finding's kind and place, each earlier finding's fate, and any paths it did not examine ([§8.1](#81-what-the-verifier-states)); the drain compares that with its measurements, shows every reading, and records the call only when a measured verdict signal holds | On a call, the candidate is pinned as the donor, the lane is released, and the item goes to the design seat; otherwise the findings return to the build step |

Three boundaries:

- **Separate asks are separate requests, not a split.** When the owner asked for two unrelated things, the operator files two requests, each quoting its own words. A split is for one ask that is too big.
- **A verifier that accepts the whole candidate ends the matter.** Size alone never splits a candidate that was judged done; it goes to the gate.
- **A call during a run waits for that run to settle.** No call interrupts a run. A second call on an item that has one pending adds its signals to the first.

## 6. The signals

Each signal is a fact someone can check. The drain reads the candidate and verdict comparisons itself at the point named; a seat states the others in a fixed field of its output, with the evidence. Every signal carries one of two standings:

- **Measured**: both the quantity and the trigger cut-off come from a measurement over the store recorded here. One measured signal that holds is enough for a split call once its caller's slice in [§14](#14-the-build-in-slices-that-each-land-alone) is built. `S1` is the only measured signal.
- **Advisory**: the trigger has not been measured from the store. An advisory signal is displayed beside its provisional reference line so operators can read it and store history can accumulate, and it never records a split call or routes an item. `S2` to `S10` are advisory.

| # | Signal | Read at | Provisional trigger | Standing | Measurement, or what is missing |
| :--- | :--- | :--- | :--- | :--- | :--- |
| S1 | **Candidate size.** The refused candidate exceeds a size limit, by `git diff --numstat` from its recorded base to its head, excluding the derived pack copy | A not-done verdict | More than 27 files, or more than 1,200 added lines | **Measured** | The 95th percentile of 1,049 landed commits (27 files, 1,198 added lines), and the refusal rates of [§2](#2-the-case-measured) |
| S2 | **A new finding after a fix.** The verifier marks every finding it returned last time as fixed, and names a finding at a place none of them named | A not-done verdict, compared with the one before it | One occurrence | Advisory | How often such a verdict is followed by convergence, across items; needs slice 3's records |
| S3 | **The repair is growing.** The candidate has more source files or more added lines than the last refused candidate, and S2 holds | A not-done verdict | Any increase | Advisory | The distribution of size deltas between consecutive refusals, separating ordinary repairs from non-converging growth |
| S4 | **Not all examined.** The verifier lists paths of the candidate it did not examine | A not-done verdict | Any path | Advisory | How often unexamined paths are followed by a refusal or a gate failure; needs slice 3's records |
| S5 | **Independent groups of pieces.** The plan's writing pieces fall into two or more groups, where no piece in one group needs a piece in another, and each group could be tested and landed alone | The plan step | Two groups, when the plan also has six or more writing pieces or S6 holds | Advisory | Refusal rates by piece count and group count across plans |
| S6 | **Code areas.** The plan's writing pieces, or the request's words, name more code areas than a limit | The plan step; filing | More than 4 areas | Advisory | 4 is the 90th percentile of landed commits, not a cut-off measured before a build against refusal outcomes |
| S7 | **More pieces than fit.** The planner needs more pieces than a piece list may hold | The plan step | More than 12, the `build-pieces@v1` cap | Advisory | Outcomes of plans that reach the cap; the cap is a schema limit, not a measured trigger |
| S8 | **Independent behaviors.** The words name behaviors that could each be tested and landed if the others never shipped | Filing; design | Three or more | Advisory | Refusal rates by count of independent behaviors |
| S9 | **Criteria that do not fit.** The request needs more than the four extra criteria a filing accepts, or one criterion joins several testable clauses | Filing | More than four extra, or any criterion with three or more clauses | Advisory | Refusal rates by criterion and clause count; the filing cap is a command-line limit, not a measured trigger |
| S10 | **Inherited candidate.** The request hands the worker a commit from an item that was refused | Filing | Any | Advisory | Convergence of requests built on a refused commit against requests built from the trunk |

How each caller uses them:

- **Operator at filing** reads `S6`, `S8`, `S9` and `S10` from the words before filing.
- **Design seat** reads `S8` over its own design, and every signal shown on an item already in its hands.
- **Planner** reads `S5`, `S6` and `S7` over its own plan.
- **Verifier** states the facts behind `S2` and `S4`. The drain reads `S1` and `S3`, compares for `S2`, shows all four on the item page, and acts on `S1` alone.

**The numbers are settings, and configuration alone never promotes an advisory signal.** `S1`'s two limits are `file_limit = 27` and `added_line_limit = 1200` under `[candidate_size]` in [`.tillr.toml`](../../.tillr.toml), so a change to a reference line is a reviewed commit. The item page shows each reading beside its reference line on every refusal, whether or not a signal held, so store evidence accumulates against real items. Promoting a signal from advisory to measured requires measuring its trigger from the store and recording that measurement in this table before any slice routes on it.

**What is not a signal.** The number of attempts, the number of refusals, elapsed time, a gate failure, a merge conflict, an adapter fault, a growing test suite on its own, and a broad or short request. A repeated identical finding stays with the repeated-finding hold.

## 7. What a split produces

### 7.1 The split statement

The design seat answers a call with one of two things. A third answer, which keeps the item and defers named cases to requests of their own, is proposed in [the request amendment design](./tillr-request-amendment.md).

**No split**, with the reason: the invariant the pieces share, or why no part could land alone. The item returns to where it was: to the plan step, or to the build step with the verifier's findings. Signals already answered are not raised again; a later call needs evidence the answer did not see.

**A split statement**, which holds:

1. **Two or more parts.** A statement with one part is refused. There is no upper limit; the guidance is that each part should itself sit under S1's limits.
2. **For each part:** a label, a title, its **share** (what it delivers, in sentences a verifier can test), what it **leaves to named sibling parts**, its criteria, the parts it must follow, and whether it is required.
3. **A coverage table:** every criterion of the parent, clause by clause where one criterion spans parts, against the part that owns it.
4. **For each part, what it takes from the donor** ([§7.4](#74-carving-the-donor)), when there is a donor.
5. **The findings each part inherits**, word for word, when the call came from a verdict.

### 7.2 Parts and the parent

The drain files the statement in one store transaction: every part as an item, every edge, and the parent's change of standing. Either all of it is stored or none. Filing is keyed by parent and part label, so a replay after a crash files nothing twice.

- **A part** is filed like a request. Its words are the owner's words for the parent, verbatim, followed by its share and what it leaves to which siblings. It gets the four criteria every request gets, read against those words, and up to four of its own. It inherits the parent's express standing and takes the parent's place in the queue.
- **Order between parts** is a `blocked_by` edge, written only where one part builds on another. Parts with no edge between them run in separate lanes at once and land in either order.
- **The parent** becomes `blocked`, waiting on every required part. It is never dispatched to a plan or build step again. Items that waited on the parent keep waiting on it, and are released when it finishes.
- **A part may be split again**, by the same rules. It is then a parent itself, and its own parent waits on it as before.

### 7.3 How criteria are divided

- **The parent's criteria do not change.** No text is edited, weakened or retired by a split.
- **Each criterion of the parent's own is owned whole by one part where it can be.** Where one criterion joins clauses that belong to different parts, the statement quotes each clause and names its part.
- **A criterion that can only be shown end to end** is owned by the last required part, or left to the closing check, and the statement says which.
- **The four standard criteria are not divided.** Each part carries its own copy, judged against the part's words. The parent's copies are judged by the closing check.
- **Nothing is left unowned.** The design seat checks that every clause has an owner before the statement is accepted, and the closing check verifies the result on the trunk.

### 7.4 Carving the donor

When a split follows a verdict, the parent's last candidate is the donor. It stays at its run pin, and the parent's lane is released.

The statement gives each part one of three instructions:

| Instruction | When | What the part's first step is |
| :--- | :--- | :--- |
| **Takes the donor whole** | Every finding is a gap, and the verifier examined the whole candidate | None. The donor's head is the part's candidate, and its first run is its verifier |
| **Takes named work** | The part's share is a subset of the donor | A build run that starts from the current trunk, takes the named paths from the donor, separates the shared files the statement lists, and makes the share build and pass its tests |
| **Takes nothing** | The share was not built, or what was built for it is what the verifier refused | An ordinary build run, given the findings |

Rules for the carving run:

- **It takes; it does not rebuild.** Its brief names the donor and the paths. A change beyond the taken work is allowed only where the share cannot stand alone without it.
- **The named paths are instructions, not a fence.** No per-item path list exists, and the split does not add one. Whether the part changed only what its share needs is the part's verifier's judgment, under the first standard criterion.
- **Derived files are regenerated, never carved.** The pack copy is rebuilt by its own command.
- **A part that follows another carves onto a trunk that already holds it.** A conflict there is the carving run's ordinary work.
- **The part that inherits the refused findings** is given them word for word, and the donor, so its worker starts from what was tried.

Where there is no donor (a split at filing, at design or at the plan step), every part takes nothing and is built in the ordinary way.

### 7.5 When the parent is finished

When the last active required part has landed, the parent runs once more: a read-only verifier judges the parent's own criteria against the trunk, with the coverage table and each part's landing as its evidence. This is the closing check.

- **It passes:** the parent is finished, with no commit of its own. Its record lists the parts and their commits.
- **It finds something missing:** the finding goes to the design seat, which adds a part to the statement through a split extension. The parent waits on that part.

A split extension appends new part(s) without deleting earlier part items from the split's history. When an extension answers a closing-check finding, every earlier required part remains as landed; when an extension answers a required part closed as `defective` ([§7.6](#76-failure-and-edge-cases)), the corrected part(s) replace that closed part in the parent's required parts, coverage table and unlanded siblings' dependencies without the owner dropping it ([`D-SP16`](#D-SP16)).

Until the closing check is built, a parent whose required parts have all landed names the operator as its mover, who checks its criteria by hand.

### 7.6 Failure and edge cases

| Case | What happens |
| :--- | :--- |
| The design seat's run fails or ends without an answer | The item is held on the design seat with the call shown; the route for a design run that repeats applies. The donor stays pinned |
| The statement is malformed, has one part, or leaves a clause unowned | It is refused and returned to the design seat with the reason; nothing is filed |
| A part is refused by its verifier | It goes back to its own build step, as any item does. Its siblings are unaffected |
| A part's verifier names a regression in work taken from the donor | The same: the part is repaired or split again. The donor is never the excuse |
| A part trips a measured signal itself | It is split again |
| A required part is closed as `defective` by its verifier | The parent stays blocked and goes back to the design seat with the verifier's finding (`mover`: design seat), while dependent siblings wait on that repair. Through the split extension, the design seat either files a corrected required part that replaces the closed part in the parent's required parts, coverage table and siblings' dependencies, or says the decision is the owner's — the only path here that names the owner as mover ([`D-SP16`](#D-SP16)) |
| The owner drops a required part | The owner's dated words are recorded on the part, the part is closed, and the coverage table shows the clauses released. Nobody else may release a clause without replacement |
| A required part is closed any other way | The parent stays blocked and shows the closed part as needing the owner |
| Two parts change the same lines | The one that lands second meets a conflict at carving or at landing, and repairs it as any item does |
| The trunk moved a long way since the donor | The carving run works on the current trunk; nothing requires the donor's diff to survive unchanged |
| The split is called before any candidate exists | No donor; parts are built from nothing |
| An item is in flight when a slice ships | Nothing changes for it until its next settlement, when the signals are read like any other item's |

**Replacing a required part closed as `defective` ([`D-SP16`](#D-SP16), a designer's decision from an operator request, which the owner may overturn).** Measured on 2026-11-27 on the worktree-ownership split: when the required part "Move the drain loop onto item-owned worktrees" was closed as `defective` because its words forbade a test change the work needed, `tillr drain why` held the parent with mover `"owner; required part did not land"` and held the dependent final part with `"operator; remove the dependency or reopen the closed item"`, starting no design run and leaving the operator to file a corrected request and rewire the dependency by hand. Because a part's share, leaves and part criteria were written by the design seat when dividing the parent (`P3`), a `defective` closure on a required part goes to the design seat first — consistent with [`OQ-CR7`](./criterion-repair.md#decision-ledger) in criterion repair, and recorded here from a 2026-11-27 operator request as a designer's decision the owner may overturn (the request reported an unquoted 2026-11-27 owner direction, and only verbatim quoted owner statements are recorded as owner rulings). The design seat receives the parent's words, criteria, split statement, part states and the verifier's `defective` finding on the closed part, and answers in one of two ways:

- **A corrected split extension:** one or more new required parts take over the closed part's share and coverage. Filing the extension atomically records the new part(s), updates coverage so every clause of the parent remains owned, and replaces the closed `defective` part in the parent's required parts and in unlanded sibling parts' dependencies, while keeping the closed part in the split's history as replaced without the owner dropping it. If the extension is malformed or leaves a clause unowned, it is refused and returned to the design seat; if the design run fails or repeats the same refusal, the parent is held on the design seat.
- **An owner decision:** when the finding exposes a problem in the parent's own request or requires dropping or changing what the owner asked for, the design run says the decision is the owner's (with the question for the owner). Only then does the owner become the parent's mover.

## 8. How a verdict accepts part of a candidate

### 8.1 What the verifier states

A verdict says, per criterion, done or not done, with a reason. To let accepted work land, a not-done verdict states four more things. None of them asks the verifier to design the split.

1. **Each finding's kind**, one of three:
   - **Gap**: in the case named, the trunk with the candidate behaves as the trunk does without it. The request is not yet met there, and nothing is worse.
   - **Regression**: in the case named, the trunk with the candidate behaves worse, or something that passes without it fails.
   - **Unasked change**: the candidate changes something no criterion needs.
2. **Each finding's place**: the case in one sentence, and the paths that implement it, using the file locators a verdict already carries.
3. **Each earlier finding's fate**: for every finding returned to the builder by the previous verdict on this item, fixed or not fixed.
4. **What was examined**: the whole candidate, or the paths that were not.

The verifier decides a finding's kind by the test in its definition, and gives the evidence: for a gap, that the named case behaves the same on the base. A finding it cannot classify is stated as a regression. Kinds and places are built (`gap`, `regression` and `unasked-change` in `tools/tillr-item/item/verdict.go`); fates and what was examined are slice 3.

### 8.2 What the design seat does with it

| The verdict says | The split statement |
| :--- | :--- |
| Every finding is a gap, and the whole candidate was examined | One part takes the donor whole; its words leave the named cases to a second part, which takes nothing and inherits the findings |
| Regressions or unasked changes sit in paths the rest does not need | One part takes the donor without those paths; another inherits them and the findings |
| A regression sits in code the rest depends on | No split. The findings go back to the build step |
| Some paths were not examined | Those paths go to a part that takes nothing from the donor for them, or to no split, at the design seat's judgment |

### 8.3 Why nothing is inherited, and why a part still lands

A verdict binds one criterion text to one exact tree. A part has different words and, unless it takes the donor whole, a different tree. So an acceptance stated about the donor is evidence shown to the part's verifier and never a verdict for the part.

What keeps the part's own verification from repeating the parent's refusals is P6. The part's verifier is given the cases its words leave to siblings. When it finds another case of the parent's request that the part does not handle, and that case is a gap, the finding is recorded against the parent and added to the sibling part that owns the remaining cases. The part is not refused for it.

So a behavior can be on the trunk while named cases of the request are still queued. The owner ruled that this is the healthier outcome ([§1](#1-the-ruling)): the working part is a small functional change that can be reviewed and tested by itself, and each queued case follows as its own part. The parent stays open and visible until those cases land.

## 9. What the queue and the status surfaces show

Nothing about a split is hidden, and every waiting state names who moves it.

| Surface | A parent | A part | A pending call |
| :--- | :--- | :--- | :--- |
| The queue's reasons (`tillr drain why`, and the pages that read it) | One row until the closing check passes: "split into 5 parts: 3 landed, 1 working, 1 held", the held part's reason and its mover; when a required part closes as `defective`, mover the design seat until replaced (or the owner only when the design run says the decision is the owner's) | An ordinary row, labelled with its parent's title and its part label; when waiting on a sibling closed as `defective`, waiting on the design seat's replacement rather than naming the operator | "split called by the verifier; waiting on the design seat", mover the design seat |
| The item page | The owner's words; the call, with each signal's value beside its threshold; the statement with its coverage table; the donor and what each part took; each part's state and landed commit; any part the owner dropped, with the owner's words | Its parent, its share, what it leaves to which sibling, what it took from the donor, the findings it inherited | The call and the measurements |
| Counts and reports of landed work | Counted as landed only after the closing check. Until then it reads "3 of 5 required parts landed" | Each landing is reported under the parent's title | Not counted |

Two further rules:

- **Measurements are shown before any signal holds.** Every refused candidate's size, and the comparison of its findings with the previous verdict's, appear on the item page from the first refusal.
- **A "no split" answer is shown**, with its reason, on the item page and in the queue's reason for the item, so the next call is read against it.

## 10. How this fits what exists

### 10.1 The planner's pieces

Pieces and parts answer different questions. A piece is a step toward one landing: pieces share a lane, a verifier, a gate and a commit, and a piece need not work alone. A part is a landing. A part's own plan may divide it into pieces. When the planner calls a split it may attach its grouping of pieces, which the design seat may adopt as the parts.

### 10.2 The write fence

There is no per-item path list; expansion and dispatch need none ([item graphs](../reference/tillr-item-graphs.md)). What exists is the common deny-list, which applies to every part as to every item, and a piece's own write scope inside one plan. A split adds no fence. The paths in a statement tell a carving run what to take, and a part that changes a path its statement did not name is judged by its verifier, not refused by a check.

### 10.3 One squash per item

An item lands as one squashed commit ([`OQ-IG1`](../reference/tillr-item-graphs.md#why-its-this-way), an owner ruling), and the lander's invariant is one landing, one squash. A split keeps both, because each part is an item. The parent lands no commit.

The other shape, "multiple judged landings out of one item", is rejected for three reasons: it would amend that ruling; a verdict, a gate result and a landing record each bind one item to one tree, so every one of them would need a second key; and an item's criteria freeze at its first landing, so the remaining parts would be judged against text nobody can correct. Part items give the same result, several judged landings out of one request, with none of those changes.

### 10.4 The landing principles

- **[Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once).** Each part is gated on its own tree. No gate result carries over from the donor.
- **[Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch).** A carved part is not required to match the donor's diff.
- **[A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report).** [§9](#9-what-the-queue-and-the-status-surfaces-show).
- **[A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act).** A pending call names the design seat; a blocked parent names its unlanded parts; a required part closed as `defective` names the design seat until a corrected part replaces it, and names the owner only when the design run says the decision is the owner's (or when a required part was closed without a `defective` verdict or owner release).

### 10.5 No count of attempts stops work

No count of worker endings determines routing ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)), and the attempt number and refusal tallies only describe history ([`D-JL21`](./tillr-judge-and-lifecycle.md#413-what-the-attempt-number-and-refusal-counts-tell-the-operator-d-jl21)). The question the owner asks of repeated refusals is "Are the refusals changing?"

The split signals follow both rules. None reads a tally, and none acts on an unmeasured threshold. `S1` measures candidate size against a 95th-percentile threshold read from the store. `S2` compares the evidence of two consecutive verdicts, which makes the owner's question checkable on the item page, and stays advisory until store history shows what trigger separates an ordinary repair from a non-converging loop. And a split is the opposite of a stop: the work continues in a different shape, with no hold on the owner and no limit on how often a part may be repaired.

## 11. What this does not do

- **It does not cap an item's size.** A large candidate its verifier accepts whole lands as one commit.
- **It does not let the drain cut code by itself.** The drain measures and files; a seat decides the division and a worker carves.
- **It does not change the gate, the lander or the squash.**
- **It does not change how pieces work**, or raise their limit of twelve.
- **It does not ask the owner to approve a split.** The owner sees it, and is asked only when a required part would be dropped or when a design run on a `defective` required part finds that the decision is the owner's.
- **It does not name commands, fields, events or store tables.** Those belong to the implementation plan.
- **It does not decide filing policy** beyond the signals an operator reads. Whether a filing should refuse a request outright is not proposed.

## 12. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Several judged landings recorded on one item | Rejected: it amends the one-squash ruling and re-keys verdicts, gate results and landings ([§10.3](#103-one-squash-per-item)) |
| Replace the item with new items and close it | Rejected: the request disappears from the queue as one thing, and nothing checks the seams. [Criterion repair](./criterion-repair.md#9-alternatives-considered) rejected the same shape for orphaning the old record |
| Let the verifier write the split | Rejected: the judge would author the bar it applies |
| Let the planner write the split | Rejected: the planner is held to the criteria it would be dividing. It may propose a grouping |
| Split automatically along the planner's pieces | Rejected: a piece need not work alone, and in the measured case two pieces held one behavior |
| Split when a count of refusals is reached | Rejected: counts are ruled out ([§10.5](#105-no-count-of-attempts-stops-work)) |
| Refuse oversized requests at filing | Rejected as the only defense by the owner's own words: "ones will slip through" |
| Carry the verifier's acceptance to the part as a verdict | Rejected: a verdict binds a text and a tree, and the part has neither in common with the parent ([§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands)) |
| Only complete behaviors land: a part must meet every word of the request about its behavior | Rejected by the owner ([`OQ-SP1`](#OQ-SP1)). In the measured case nothing would have landed, because every finding was a case of one behavior |
| A path list for each part | Rejected: items carry no path fence, by the owner's ruling |
| Route on first-guess triggers and tune after false splits | Rejected (`D-SP15`): an unmeasured threshold that routes work is the arbitrary limit the owner ruled out |

## 13. Risks

| Risk | Mitigation |
| :--- | :--- |
| A defect hides in the seam between two parts | The closing check judges the parent's criteria on the trunk; a criterion that needs end-to-end proof is owned by the last part or by that check |
| The trunk carries a behavior with queued cases, and they are never built | The owner accepted a part landing before its cases ([§1](#1-the-ruling)). The parent stays open and visible with its count of parts; only the owner drops a part |
| A finding is called a gap when it is a regression | The verifier must show the base behaves the same; an unclassifiable finding is a regression; the part's gate still runs every check |
| Splits happen where none is needed, and each costs a design run and extra gates | A candidate accepted whole is never split; no advisory signal calls a split; the design seat may answer "no split" |
| The design seat becomes the queue's narrow point | A call releases the lane; unrelated work continues; how long calls wait is visible in the queue's reasons |
| A carving run rebuilds instead of taking | Its brief names the donor and the paths, and its verifier sees both the donor and the part's diff |
| The thresholds are wrong | Only a store-measured trigger may act on routing; advisory signals stay displayed facts until measured |

## 14. The build, in slices that each land alone

The build follows the design's own rule. Each slice is its own request and lands as its own commit. Each one is useful on the trunk if no later slice ever lands, and none makes the trunk worse while the later ones are queued.

The order puts what only shows facts first, so store history accumulates on real items without moving anything. Next comes a split made by a person. Then comes each caller, and **a slice may act on routing only for signals marked measured in [§6](#6-the-signals)**.

| Slice | What lands | Why it is useful alone | Signals it may act on | Follows | Built |
| ---: | :--- | :--- | :--- | :--- | :--- |
| 1 | Every refused candidate's size (files and added lines, without the pack copy) is measured and shown on its item page beside `S1`'s limits, which are tracked settings. Nothing is routed | An operator sees an oversized candidate at its first refusal | None | — | Yes (`measureCandidateSize`) |
| 2 | A not-done verdict states each finding's kind and place, and the item page shows them | A reader of a refusal sees whether the trunk would be worse or only unfinished, and where | None | — | Yes (`verdict.go`) |
| 3 | A not-done verdict states each earlier finding's fate and what was examined. The item page shows the comparison with the previous verdict, unexamined paths, and the growth of the candidate | "Are the refusals changing?" is answered on the page for every item, and the store collects the history needed to measure `S2`, `S3` and `S4` | None | 2 | No |
| 4 | A split statement written by the design seat is checked and filed in one transaction: the parts as items, their order, and the parent blocked on its required parts. Every part takes nothing from a donor. A malformed statement is refused whole | A split made by a person files correctly and at once, in place of one request per part filed by hand | None | — | Yes (`FileSplit`) |
| 5 | The queue's reasons and the item page show a parent as one row with its count of parts, and a part with its parent, its share and what it leaves to which sibling ([§9](#9-what-the-queue-and-the-status-surfaces-show)) | A split is readable as one request on every surface | None | 4 | No |
| 6 | A part's verifier is given the cases the part's words leave to siblings. A gap it finds in another case of the parent's request is recorded against the parent and added to the sibling that owns the remaining cases (P6) | Part of a change lands while a named case is still queued | None | 2, 4 | No |
| 7 | The closing check: when the last required part lands, a read-only verifier judges the parent's criteria against the trunk. A pass finishes the parent, and a finding adds a part | A parent finishes without a person, and a defect in a seam between parts is found | None | 4 | No |
| 8 | The verifier's call: at a not-done verdict the drain records a split call when a measured verdict signal holds, pins the candidate as the donor, releases the lane and sends the item to the design seat, which answers with a statement or with "no split" | An oversized item leaves the repair loop at its first refusal that exceeds `S1` | `S1` only | 1, 3, 4 | No |
| 9 | Carving: a part takes the donor whole, or takes named work in a carving run ([§7.4](#74-carving-the-donor)) | Work already built is taken and not rebuilt | None | 8 | No |
| 10 | The planner's signals and call: the plan step records and shows `S5`, `S6` and `S7`, and may end with a split call only when a measured plan signal holds | Plan-time readings are visible and stored without routing on a guess | None until one of `S5` to `S7` is measured | 4 | No |
| 11 | Filing signals and the operator's call: filing records and shows `S6`, `S8`, `S9` and `S10`, and routes the item to the design seat before any run only when a measured filing signal holds | Filing-time readings are visible and stored without routing on a guess | None until one of `S6`, `S8`, `S9` or `S10` is measured | 4 | No |
| 12 | The owner drops a required part: the owner's dated words are recorded on the part, the part is closed, and the coverage table shows the clauses released | A parent can finish without a part the owner does not want | None | 7 | No |

Two slices where neither follows the other may be built at once in separate lanes.

**Who marks a slice built** (`D-SP14`). A build seat does not edit a design document, so a slice's code is the build seat's and its mark in this table is the design seat's. A slice's build commit changes no line of this document, and neither a build worker nor a verifier treats a request's sentence about the table as unmet.

## Open Questions

None.

## Decision Ledger

A row marked "owner" records the owner's verbatim words ([§1](#1-the-ruling)). Every other row is the designer's decision (or from an operator request), made without the owner, who may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-SP1 | **Owner ruling.** Oversized changes are broken up past some threshold; the accepted parts land without the part objected to; several seats may say "split"; each has clear guidance | 2026-11-21, owner | [§1](#1-the-ruling) | — |
| <a id="OQ-SP1"></a>[`OQ-SP1`](#OQ-SP1) | **Owner ruling.** Part of a change may land while a named case of the request is still unmet; the case follows as its own part. A part is refused for making the trunk worse, for an unasked change, for a missing test or for not delivering its share, and not for a case its words leave to a sibling. **Rejected:** only complete behaviors land | 2026-11-21, owner | [§1](#1-the-ruling), [§4](#4-the-rules-a-split-keeps) (P6), [§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands) | — |
| R-SP2 | **Owner ruling.** A threshold whose origin nobody can state must not break work: "we have random thresholds again breaking things?" | 2026-11-23, owner | [§1](#1-the-ruling) | — |
| D-SP1 | A split produces part items under a parent that stays open, not several landings of one item. **Rejected:** several landings on one item, which would amend the one-squash ruling | 2026-11-21, designer | [§7.2](#72-parts-and-the-parent), [§10.3](#103-one-squash-per-item) | Slice 4 |
| D-SP2 | Four callers call a split: the operator at filing, the design seat, the planner at the plan step, and the verifier at a not-done verdict. The design seat alone writes the division, and may answer "no split" | 2026-11-21, designer | [§5](#5-who-calls-a-split-and-when), [§7.1](#71-the-split-statement) | — |
| D-SP3 | The ten signals of [§6](#6-the-signals), and their reference lines. Only `S1` has a store-measured trigger (`D-SP15`) | 2026-11-21, designer | [§6](#6-the-signals) | Slice 1: `S1` counts and limit settings |
| D-SP4 | Size alone never splits a candidate its verifier accepted whole, and no signal counts attempts or refusals | 2026-11-21, designer | [§5](#5-who-calls-a-split-and-when), [§10.5](#105-no-count-of-attempts-stops-work) | — |
| D-SP5 | The parent's criteria are never changed by a split. Each is owned by a part, clause by clause where needed; each part carries its own copy of the four standard criteria | 2026-11-21, designer | [§7.3](#73-how-criteria-are-divided) | Slice 4 |
| D-SP6 | The refused candidate is a donor. A part takes it whole, takes named work in a carving run, or takes nothing. Named paths are instructions and not a fence | 2026-11-21, designer | [§7.4](#74-carving-the-donor) | — |
| D-SP7 | A not-done verdict states each finding's kind and place, each earlier finding's fate, and what was examined. The verifier does not propose the parts | 2026-11-21, designer | [§8.1](#81-what-the-verifier-states) | Slice 2: kind and place |
| D-SP8 | No verdict or gate result is inherited from the donor: every part is verified and gated on its own tree | 2026-11-21, designer | [§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands) | — |
| <a id="D-SP9"></a>[`D-SP9`](#D-SP9) | **Designer's decision; the owner may overturn it** (amended by [`D-SP16`](#D-SP16)). The parent finishes by a closing check of its own criteria against the trunk; a finding there adds a part. Only the owner drops a required part without replacement; a required part closed as `defective` goes to the design seat to be replaced through a split extension unless the design run says the decision is the owner's ([`D-SP16`](#D-SP16)) | 2026-11-21, amended 2026-11-27, designer | [§7.5](#75-when-the-parent-is-finished), [§7.6](#76-failure-and-edge-cases) | — |
| D-SP10 | The queue shows a parent as one row with its count of parts until the closing check passes, and shows every measurement before any signal holds | 2026-11-21, designer | [§9](#9-what-the-queue-and-the-status-surfaces-show) | Slice 1: size and limits on the item page |
| D-SP11 | No count of verifier refusals pauses an item for review | 2026-11-21, designer | [§10.5](#105-no-count-of-attempts-stops-work) | — |
| <a id="D-SP12"></a>[`D-SP12`](#D-SP12) | **Designer's decision; the owner may overturn it** (amended by [`D-SP16`](#D-SP16)). The owner is not asked to approve or acknowledge a split, and is asked only when a required part would be dropped or when a design run on a `defective` required part says the decision is the owner's | 2026-11-21, amended 2026-11-27, designer | [§7.6](#76-failure-and-edge-cases), [§9](#9-what-the-queue-and-the-status-surfaces-show), [§11](#11-what-this-does-not-do) | — |
| D-SP13 | The build is twelve slices that each land alone, in the order of [§14](#14-the-build-in-slices-that-each-land-alone): what only shows facts first, then a split made by a person, then each caller | 2026-11-21, designer | [§14](#14-the-build-in-slices-that-each-land-alone) | Slices 1, 2 and 4 |
| D-SP14 | The design seat, not the build seat, marks a slice built in the slice table; a slice's build commit changes no line of this document. **Rejected:** a build commit that edits this document | 2026-11-22, designer | [§14](#14-the-build-in-slices-that-each-land-alone) | — |
| D-SP15 | A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on. `S1` is measured; `S2` to `S10` are advisory, and tracked configuration does not promote one. **Rejected:** routing on first-guess triggers and tuning after false splits; hiding advisory signals, so the store never collects the readings needed to measure them | 2026-11-24, designer | [§4](#4-the-rules-a-split-keeps) (P7), [§6](#6-the-signals), [§14](#14-the-build-in-slices-that-each-land-alone) | Slice 1 displays `S1` without routing |
| <a id="D-SP16"></a>[`D-SP16`](#D-SP16) | **Designer's decision, from an operator request; the owner may overturn it.** Amends [`D-SP9`](#D-SP9) and [`D-SP12`](#D-SP12), and overturns the earlier rule in [§7.6](#76-failure-and-edge-cases) and [§10.4](#104-the-landing-principles) for `defective` closures (written from a 2026-11-27 operator request that reported an unquoted 2026-11-27 owner direction that a stuck split goes to the design seat first, consistent with [`OQ-CR7`](./criterion-repair.md#decision-ledger) in criterion repair; recorded as the designer's decision from an operator request because no verbatim owner words were quoted). When a verifier closes a required part of a split as `defective`, the parent stays blocked and goes back to the design seat (`mover`: design seat) with the verifier's finding, while unlanded sibling parts that depended on the closed part wait on that repair instead of naming the operator. Through the split extension, the design seat either files a corrected required part (or parts) that replaces the closed `defective` part in the parent's required parts, coverage table and unlanded siblings' dependencies while keeping the closed part in the split's history as replaced without the owner dropping it, or states that the decision is the owner's — the only path here that makes the owner the mover. A required part closed any other way without the owner's release still names the owner. **Rejected:** holding the parent on the owner immediately when a required part closes as `defective` and leaving dependent siblings on the operator to rewire by hand (the 2026-11-27 failure on the worktree-ownership split); reopening the frozen `defective` part in place; or deleting the closed part from the split's history | 2026-11-27, designer, from an operator request | [§4](#4-the-rules-a-split-keeps) (P1), [§7.5](#75-when-the-parent-is-finished), [§7.6](#76-failure-and-edge-cases), [§9](#9-what-the-queue-and-the-status-surfaces-show), [§10.4](#104-the-landing-principles), [§11](#11-what-this-does-not-do) | — |
