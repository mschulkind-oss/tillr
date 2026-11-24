---
title: "An oversized item lands in parts: what is accepted lands, and what is objected to goes back alone"
status: accepted
stage: DECIDED
updated: 2026-11-24
tags: [tillr, drain, verify, landing, split, design]
summary: "The owner ruled on 2026-11-21 that a change too big to land whole must be broken up so the accepted parts land without the part objected to, that several seats may say so, and that each needs clear guidance, and that part of a change may land while a named case of the request is still unmet. On 2026-11-23 he asked where the 1,200-line limit came from and whether random thresholds were breaking things again, and approved filing this amendment. Answering the operator's request, a signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on. Signal S1 is measured; signals S2 to S10 are advisory, with each missing store measurement named, and each unbuilt slice states which signals it may act on. Everything beyond the owner's quoted words is the designer's decision. Slice 1 is built; slices 2 and 4 were marked built ahead of their code on 2026-11-22; slices 3 and 5–12 are unbuilt."
next: "Slices 3 and 5–12 of Section 14 are unbuilt, and zero filed-but-unlanded build items in the queue act on an advisory signal. Unbuilt slice 8 may route on measured S1 alone; slices 10 and 11 act on no signal until store measurements are recorded in Section 6."
depends-on:
  - landing-principles.md
  - tillr-judge-and-lifecycle.md
  - ../reference/tillr-item-graphs.md
vantage:
  status-chip: true
---

# An oversized item lands in parts

**Status:** 2026-11-24 — amended for the rule that **a signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on** (`D-SP15`, answering an operator's request after the owner's 2026-11-23 statement `R-SP2` in [§1](#1-the-ruling)). In [§6](#6-the-signals), `S1` is **Measured** and `S2`–`S10` are **Advisory**, each with its measurement or missing store measurement named; [§14](#14-the-build-in-slices-that-each-land-alone) states which signals each unbuilt slice may act on and carries the [2026-11-24 candidate note](#14-the-build-in-slices-that-each-land-alone). Slice status carried from 2026-11-22: slice 1 of [§14](#14-the-build-in-slices-that-each-land-alone) is built, showing file and added-line counts beside the limits recorded with each measured verify refusal, with no reading shown for unmeasured refusals and no routing effect. Slices 2 and 4 were marked built in the slice table on 2026-11-22 by the design seat ahead of their code (`D-SP14`): when those marks were written, until slice 2's commit landed no not-done verdict stated a finding's kind or place, and until slice 4's commit landed no split statement was checked or filed by the drain. Slices 3 and 5–12 are unbuilt. The measurements in [§2](#2-the-case-measured) were read from the store and the repository on 2026-11-21. The owner's statements quoted in [§1](#1-the-ruling) are the only rulings; every other decision is the designer's (or from an operator's request) and is marked so in the [Decision Ledger](#decision-ledger).

> **In short.** An item that is too big is split into **parts**, each an ordinary item with its own words, verifier, gate and single squashed commit, under a **parent** that keeps the owner's words and stays open until every part has landed. The work already built is taken from the refused candidate and not rebuilt, and what decides whether a part may land is whether it makes the trunk worse, not whether the whole request is finished.

**Why it matters.** On 2026-11-21 one item was refused by its verifier twelve times in two and a half hours, each time on a different case, while its candidate grew from 34 to 64 files. Nothing landed, and the verifier had no way to say that the rest was fine.

**The shape.** Four callers and one divider. The operator at filing, the design seat, the planner and the verifier each **call** a split only on signals whose triggers are measured from the store (unmeasured signals stay advisory: shown, never acted on); the design seat alone writes the division; the drain files the parts.

**Cost.** One more design run per split, one verify and one gate per part, and a trunk that may carry a behavior whose remaining cases are still queued. The seams between parts become a place where a defect can hide, which is what the closing check in [§7.5](#75-when-the-parent-is-finished) is for.

**Start at** [§4](#4-the-rules-a-split-keeps), the seven rules, then [§8](#8-how-a-verdict-accepts-part-of-a-candidate).

**Needs your ruling:** None. The owner ruled [OQ-SP1](#OQ-SP1) on 2026-11-21: part of a change may land.

**Awaits your review:** the designer's decisions `D-SP1` to `D-SP15` in the [Decision Ledger](#decision-ledger).

**Reads with:** [landing principles](./landing-principles.md) (the rules a split must not break), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md) (verdicts, and the rule that no count of attempts routes work), [item graphs](../reference/tillr-item-graphs.md) (pieces inside one item, which a split does not replace), and when each repair finds a different defect (the proposal of 2026-11-20 that this design absorbs; [§10.6](#106-the-proposal-of-2026-11-20) says what is kept).

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, criterion, candidate, lane and verdict. These are coined in this document:

- **Split**: replacing one item's single landing with several items that land separately. It is not the planner's division of an item into pieces, which stays inside one item and one landing.
- **Split call**: a recorded statement that an item should be split, naming who made it and the measured signals behind it. Advisory signals are displayed facts and never record a call or route an item. It decides nothing: the design seat answers it.
- **Split statement**: the design seat's answer to a call. It lists the parts, divides the criteria and says which built work each part takes. Its other possible answer is **no split**, with the reason.
- **Part**: an ordinary item filed from a split statement. It is not a piece: a piece is a step inside one item.
- **Parent**: the item that was split. It keeps the owner's words and its criteria, is never built again, and waits on its parts.
- **Donor candidate**: the parent's last candidate, kept at a Git ref, from which parts take work. It is not a verified candidate of any part.
- **Gap**, **regression**, **unasked change**: the three kinds of verifier finding, defined in [§8.1](#81-what-the-verifier-states).
- **Closing check**: the one read-only verification of the parent's own criteria against the trunk, after its last required part lands.

---

## 1. The ruling

The owner said on 2026-11-21, verbatim:

> "We need a way to protect against these mega items. We can change policies about filing them and try to protect that way, but inevitably ones will skip, ones will slip through, so we need a strategy for landing these large changes, which means that after some threshold or something, we just need to break them up. Okay. We need to like, if there's a change that's really five changes, we need to be able to land four of them without the one that's been objected against. I don't know if that just means that like one of the things the judge can say is that like this is just too big, and then we have to go back and split it somehow, and we can have multiple judged landings out of one item or something like that.I'm not really sure when the right point is to split it up. Maybe there's multiple points. Maybe like the designer can say split up, the planner can say split up, the judge can say split up at various points.But they should have clear guidance."

What I take as ruled, because the words state it without doubt:

1. Filing policy alone is not the protection. Oversized items will get through, so the drain needs a way to land them.
2. Past some threshold the change is broken up.
3. The accepted parts of a change land without the part that is objected to.
4. Whoever may say "split" has clear guidance.

Later the same day he was asked whether part of a change may land while a named case of the request is still unmet, and said, verbatim, with his spelling kept:

> "part of desgins can absoutely land. this is generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the smallest functional change possible to make reviewing, testing, velocity, all this stuff easier, and it will also make it easier for us."

What I take as ruled by those words:

5. Part of a change may land while a named case of the request is still unmet. The case follows as its own part.
6. The smaller landing is the healthier one. The aim is the smallest functional change, as a human engineer would land it, because it makes reviewing, testing and velocity easier. [The landing principles](./landing-principles.md#15-land-the-smallest-functional-change) record this as a general principle.

What the first statement leaves open, because he says so ("I don't know if", "I'm not really sure", "Maybe"): which seats say it and when, what the threshold is, and whether a split is several landings of one item or something else. Those are settled below as the designer's decisions, and he may overturn any of them.

One earlier statement bears on it. On 2026-11-20, of an item whose repairs kept finding new defects, the proposal of that date records him saying: "Split it up. That's always a good way to go."

On 2026-11-23 at about 01:05 ET, seeing `added_line_limit = 1200` in [`.tillr.toml`](../../.tillr.toml), the owner said, verbatim (`R-SP2`):

> "where'd this 1200 line limit come from? we have random thresholds again breaking things?"

Asked whether to file this design item, the owner said, verbatim: `"yes"`.

Where things stand on 2026-11-24: [`.tillr.toml`](../../.tillr.toml) `[candidate_size]` sets `file_limit = 27` and `added_line_limit = 1200`, commented `"Advisory thresholds recorded beside each verifier refusal; neither blocks landing"`, and [`tools/tillr-drain/drain/candidate_size.go`](../../tools/tillr-drain/drain/candidate_size.go) (`measureCandidateSize`, built in slice 1 at `b54e403ca5`) only records candidate size beside a refusal. Those two numbers come from the 2026-11-21 store measurement in [§2](#2-the-case-measured) (`S1`: the 95th percentile of 1,049 landings, `27` files and `1,198` added lines, rounded to `1,200` in `.tillr.toml`). At the same time, the initial [§6](#6-the-signals) table labelled its other triggers a `"first guess"` — `S2` (one new finding after a fix), `S3` (any growth in the repair), `S4` (any unexamined path), `S5`, `S6`'s cut-off, `S7`, `S8`, `S9`'s clause count and `S10` — while later slices in [§14](#14-the-build-in-slices-that-each-land-alone) would have had the drain act on them.

The owner's standing rule elsewhere is that no arbitrary time limit, retry count or attempt limit routes or stops work; a failure gets its root cause ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). Answering the operator's filed request (`D-SP15`, which is the designer's decision from an operator's request and which the owner may overturn) applies that standing rule here: **a signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on.** A threshold that routes work must come from a measurement, and one that has none stays a displayed fact.

## 2. The case, measured

The item is the gate change that disables a check which both passed and failed on one tree (`item-request-c1a6f8e4e5`, workflow `planned-build@v1`). Its request carries seven criteria: the four every filed request gets, and three of its own. All figures were read on 2026-11-21 at about 6:35 PM ET.

**What happened.** The planner proposed two pieces. The first attempt's verifier passed the candidate and the gate refused it at 3:38 PM ET. Attempts 2 to 13 each ended in a verifier refusal, twelve between 4:00 PM and 6:29 PM ET (the request that asked for this design counted eleven; one more arrived while I was reading). Source: the store's `drain.refusal` events for the item with step `verify-not-done`, and its `node_runs` rows.

**The candidate grew at every refusal.** `git diff --shortstat <base> <head>` for each refused head, excluding the derived pack copy under `yolo-packs/`:

| Refusal | Time (ET) | Files | Added lines | What the verifier named, shortened |
| ---: | :--- | ---: | ---: | :--- |
| 1 | 4:00 PM | 34 | 2,189 | A resumed heavy proof credits the check from an old checkpoint |
| 2 | 4:13 PM | 36 | 2,329 | One landing path blocks a trunk fixer and files no repair |
| 3 | 4:23 PM | 40 | 2,519 | The landing summary omits disabled checks; a cold adapter runs the check |
| 4 | 4:36 PM | 42 | 2,740 | Two attribution shortcuts credit the check on a replayed base |
| 5 | 4:50 PM | 45 | 2,971 | No repair is filed when another check's cleanup fails |
| 6 | 4:55 PM | 45 | 3,004 | A disabled batch member prevents a revert |
| 7 | 5:07 PM | 45 | 3,255 | The fast tree adapter turns a disabled declaration into an error |
| 8 | 5:24 PM | 53 | 3,869 | Replays load the old result classifiers |
| 9 | 5:36 PM | 55 | 4,212 | Light proof reuse credits the check |
| 10 | 5:59 PM | 58 | 4,892 | Proofs from before the upgrade credit the check |
| 11 | 6:15 PM | 61 | 5,220 | The resolver uses an old whole proof as a pass |
| 12 | 6:29 PM | 64 | 5,427 | The check is recorded as a pass after a fixer's rebase conflict |

With the pack copy the last candidate is 117 files and 10,493 added lines. The changed source sits in four tool directories: `tools/tillr-drain` (64% of files), `tools/tillr-gate`, `tools/tillr-loop` and `tools/gate`.

**What a normal landing looks like.** The same measurement over the trunk commit of each of the 1,049 landings the store records as landed, each excluding the pack copy:

| Measure | Median | 90th percentile | 95th | 99th |
| :--- | ---: | ---: | ---: | ---: |
| Files changed | 2 | 19 | 27 | 45 |
| Lines added | 75 | 896 | 1,198 | 2,394 |
| Distinct code areas (`tools/<module>`, `docs/<dir>`, and the like) | 1 | 4 | 5 | 8 |

**Size and refusals go together, on a small sample.** Of the 95 `planned-build@v1` items that landed since verifier refusals were first recorded, 19 landed above the 95th percentile on files or added lines. Those 19 averaged 3.3 verifier refusals and six were refused three or more times; the other 76 averaged 1.1 and ten were refused three or more times. This measures the size that landed, not the size at the first refusal.

Five observations follow, and the design rests on them:

1. **The first refused candidate was already past the 95th percentile on both measures.** Nothing reads a candidate's size.
2. **Each refusal named a case the earlier ones had not.** The existing hold fires only when the same finding repeats on unchanged evidence ([the drain's reference](../../tools/tillr-drain/README.md#a-refused-run-goes-back-to-its-worker)), so twelve different findings moved nothing.
3. **The criteria recorded none of what was accepted.** Every refusal marked the first criterion ("the change does what the request's words ask") not done, and most marked the item's own three as well. A verdict is one entry per criterion with a reason and evidence locators (`JudgeEntry` in `tools/tillr-item/item/judge.go`); it has no place to say which part of the candidate a finding is about, or that the rest is sound.
4. **The planner had no guidance on size.** `rg -n -i 'split|size|independent|large' config/tillr/plan-guidance.md` on 2026-11-21 finds nothing.
5. **The item was already a second generation.** Its words say it replaces an earlier item whose worker "built most of this", and tell the worker to start from that item's commit. Handing built work to a successor is already done by hand, in prose.

An earlier case has the same shape: the instant-start change, with seven verifier refusals naming different defects over a 39-file candidate (measured on 2026-11-20).

## 3. What exists today

| Mechanism | What it does | What it cannot do |
| :--- | :--- | :--- |
| Pieces (`planned-build@v1`, `build-pieces@v1`) | The planner divides an item into at most 12 pieces that run in order on one lane | Land separately: all pieces share one verifier, one gate and one squashed commit ([item graphs](../reference/tillr-item-graphs.md#lane-lifecycle-write-scopes-and-chained-commits)) |
| `blocked_by` edges | One item waits on another ([dependencies](../../tools/tillr-item/README.md#adding-and-removing-dependencies)) | Say that several items are one request |
| Verifier return | A not-done verdict sends the findings to the build step on the same candidate | Keep any of the candidate out of the next verdict |
| Repeated-finding hold | Holds an item when the same finding repeats on unchanged evidence | Notice findings that differ every time |
| Run pins (`refs/tillr/runs/<run>`) | Keep a run's committed head | Say which of that work a later item should take |
| The deny-list | Refuses any worker's change to the store, the fork caches or an item declaration | Limit an item to paths: the per-item path list was retired on 2026-11-19 |

No item has a parent, and no record says one item was replaced by several. An item's states are `not-started`, `in-queue`, `blocked`, `landed`, `resolved-elsewhere` and `closed` ([item states](../reference/tillr-item-states.md)).

## 4. The rules a split keeps

- **P1. A split changes the shape of the work, never its bar.** The parent keeps the owner's words and its criteria unchanged. It is finished only when every required part has landed and the closing check passes. Only the owner makes a required part optional.
- **P2. A part is an ordinary item.** It has its own words, its own criteria, one verifier, one gate and one squashed commit. The gate and the lander do not change.
- **P3. One seat writes the division.** Four callers may call a split. The design seat alone decides whether and how, because dividing criteria is writing the bar, and neither the worker that is held to a bar nor the judge that applies it may write it ([criterion repair](./criterion-repair.md#decision-ledger) rules the same for a frozen criterion).
- **P4. A measured signal calls a split; nothing here stops work.** No signal counts attempts or refusals. A call never holds the item on the owner.
- **P5. Built work is taken, not rebuilt, and no verdict is inherited.** A part takes its share from the donor candidate. It is then verified and gated on the tree that will land, like any item.
- **P6. On a part, a gap is filed and a regression is refused.** A part is refused for making the trunk worse, for changing what nobody asked for, for lacking its tests, or for not delivering the share it claims. A case of the parent's request that the part does not yet handle is recorded against the parent and built by another part. The owner ruled this on 2026-11-21: "part of desgins can absoutely land. this is generally MORE healthy."
- **P7. A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on.** A threshold that routes work must come from a measurement in the store, and one that has none stays an advisory displayed fact (`D-SP15`, from an operator's request; the owner may overturn it).

## 5. Who calls a split, and when

A split call is made at four points. Each caller states facts in fixed fields; the drain measures what can be measured ([§6](#6-the-signals)). A call that routes an item to the design seat may rest only on a **measured** signal in [§6](#6-the-signals), and names the caller, the time, and each measured signal with its value and its threshold. **Advisory** signals are recorded and shown without recording a call or changing what runs next.

| Caller | The point | How the call is made | What happens to the item meanwhile |
| :--- | :--- | :--- | :--- |
| **Operator, at filing** | When filing the request, before any run | Filing records the facts for `S6`, `S8`, `S9` and `S10`, and records a routing split call only when a **measured** filing signal holds (`S6`, `S8`, `S9` and `S10` are all **advisory** today) | When a measured filing signal holds, the item goes to the design seat first and no plan or build run starts; while all filing signals are advisory, the readings are shown and the item proceeds as today |
| **Design seat** | While writing a design that will be built, and whenever an item is in its hands for another reason (a `definition` obstacle, a question about the item) | It writes the split statement directly; no separate call is needed | For a design, the build request is filed with the statement, so the parts are filed at once |
| **Planner** | At the plan step, before any writing run | It records `S5`, `S6` and `S7` on its plan, and may end its run with a routing split call in place of a piece list (with its proposed grouping attached) only when a **measured** plan signal holds (`S5`, `S6` and `S7` are all **advisory** today) | When a measured plan signal holds, no build run starts and the item goes to the design seat; while all plan signals are advisory, the readings are shown and the piece list runs as today |
| **Verifier** | At a not-done verdict | It states each finding's kind and place, each earlier finding's fate, and any paths it did not examine ([§8.1](#81-what-the-verifier-states)); the drain compares that with its measurements, shows every reading, and records the call only when a **measured** verdict signal holds (`S1` today; `S2`, `S3` and `S4` are **advisory** and never call a split until measured from the store) | When a measured signal holds, the candidate is pinned as the donor, the lane is released, and the item goes to the design seat instead of returning findings to the build step; when only advisory signals hold, the readings are shown and the findings return to the build step as today |

Three boundaries:

- **Separate asks are separate requests, not a split.** When the owner asked for two unrelated things, the operator files two requests, each quoting its own words. A split is for one ask that is too big.
- **A verifier that accepts the whole candidate ends the matter.** Size alone never splits a candidate that was judged done; it goes to the gate as today.
- **A call during a run waits for that run to settle.** No call interrupts a run. A second call on an item that has one pending adds its signals to the first.

## 6. The signals

Each signal is a fact someone can check. The drain reads the candidate and verdict comparisons itself at the point named; a seat states the others in a fixed field of its output, with the evidence.

**Rule (`D-SP15`).** **A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on.** Every signal in this document (`S1` to `S10`; there are no others) carries one of two standings:

- **Measured**: both the quantity and the trigger cut-off come from a measurement over the store recorded here. One **Measured** signal that holds is enough for a split call once its caller slice in [§14](#14-the-build-in-slices-that-each-land-alone) is built. Today `S1` is the sole **Measured** signal.
- **Advisory**: the trigger has not been measured from the store (whether because the trigger itself is a first guess, because a landed-commit percentile has not been measured as a pre-build cut-off against refusal outcomes, because a single case was observed, or because a cap exists in a schema or CLI for another purpose). An **Advisory** signal is displayed beside its provisional reference line so operators can read it and store history can accumulate, and **never** records a split call or routes an item. Today `S2` to `S10` are **Advisory**.

| # | Signal | Read at | Read from | Trigger | Standing | Measurement or missing measurement |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| S1 | **Candidate size.** The refused candidate exceeds a size limit | A not-done verdict | `git diff --numstat` from the candidate's recorded base to its head, excluding the derived pack copy | More than 27 files, or more than 1,200 added lines | **Measured** | **Measurement:** 95th percentile of 1,049 landed commits read from the store on 2026-11-21 (`27` files and `1,198` added lines, rounded to `file_limit = 27` and `added_line_limit = 1200` in `.tillr.toml`), together with 95 landed `planned-build@v1` items where the 19 above the 95th percentile averaged 3.3 verifier refusals vs 1.1 across the other 76 ([§2](#2-the-case-measured)). Built slice 1 records and shows `S1` without routing; unbuilt slice 8 may route on it |
| S2 | **A new finding after a fix.** The verifier marks every finding it returned last time as fixed, and names a finding at a place none of them named | A not-done verdict, compared with the one before it on the same item | The verifier's statement per earlier finding, and the paths and case of each new one | One occurrence *(provisional display comparison; first guess)* | **Advisory** | **Missing measurement:** store distribution across items with consecutive not-done verdicts comparing how often `1`, `2`, or more consecutive all-prior-fixed verdicts with a new finding at a new place converge on the next repair versus fail to converge (requires slice 3 to record earlier-finding fates in the store; the twelve refusals of one item in [§2](#2-the-case-measured) observe the pattern on `n = 1` item, not the trigger) |
| S3 | **The repair is growing.** The candidate has more source files or more added lines than the last refused candidate, and S2 holds | A not-done verdict | The same `numstat`, for the two candidates | Any increase *(provisional display comparison; first guess)* | **Advisory** | **Missing measurement:** store distribution of file and added-line deltas between consecutive `drain.refusal` events across items, separating normal repairs that add a test or small fix and pass on the next verdict from non-converging growth (and `S3` compounds `S2`, whose trigger is also unmeasured; [§2](#2-the-case-measured) measured growth on `n = 1` item) |
| S4 | **Not all examined.** The verifier lists paths of the candidate it did not examine | A not-done verdict | The verifier's statement | Any path *(provisional display reading; first guess)* | **Advisory** | **Missing measurement:** store incidence of unexamined candidate paths on not-done verdicts (not recorded until slice 3 lands) and their rate of subsequent verifier refusals or gate failures across items |
| S5 | **Independent groups of pieces.** The plan's writing pieces fall into two or more groups, where no piece in one group needs a piece in another, and each group could be tested and landed alone | The plan step | The planner's piece list and the order it gives | Two groups, when the plan also has six or more writing pieces or S6 holds *(provisional display reading; first guess)* | **Advisory** | **Missing measurement:** store distribution of writing-piece counts and independent piece-dependency groups across `planned-build@v1` plans measured against verifier refusal rates and non-convergence (neither `2` groups nor `6` writing pieces is measured from the store, and `S6`'s cut-off is also unmeasured) |
| S6 | **Code areas.** The plan's writing pieces, or the request's words, name more code areas than a limit | The plan step; filing | The pieces' write scopes; the paths and modules the words name | More than 4 areas *(provisional display reading; cut-off is a first guess)* | **Advisory** | **Missing measurement:** store verifier-refusal and non-convergence rates by distinct code-area count as read from planned write scopes and filed request words (`4` is the measured 90th percentile and `5` is the 95th percentile of 1,049 *landed commits* in [§2](#2-the-case-measured), but `> 4` as a pre-build routing cut-off at planning or filing has not been measured against store refusal outcomes) |
| S7 | **More pieces than fit.** The planner needs more pieces than a piece list may hold | The plan step | The planner's statement | More than 12 *(shipped `build-pieces@v1` cap; first guess as a split trigger)* | **Advisory** | **Missing measurement:** store incidence and verifier/landing outcomes of `planned-build@v1` plans that hit or request more than `build-pieces@v1`'s `most: 12` schema limit (`12` is a static workflow-argument cap, not a store-measured split trigger) |
| S8 | **Independent behaviors.** The words name behaviors that could each be tested and landed if the others never shipped | Filing; design | The caller lists them, one line each | Three or more *(provisional display reading; first guess)* | **Advisory** | **Missing measurement:** store counts of independent behaviors per filed request or design measured against verifier refusal rates, rework, and non-convergence (`3` has no store measurement) |
| S9 | **Criteria that do not fit.** The request needs more than the four extra criteria a filing accepts, or one criterion joins several testable clauses | Filing | The criteria as written | More than four extra, or any criterion with three or more clauses *(shipped CLI cap and first-guess clause count)* | **Advisory** | **Missing measurement:** store verifier-refusal and non-convergence rates by extra-criterion count and per-criterion clause count across filed requests (`MaxRequestCriteria = 4` is a shipped CLI cap rather than a store-measured split threshold, and `3` clauses has no store measurement) |
| S10 | **Inherited candidate.** The request hands the worker a commit from an item that was refused | Filing | The words | Any *(provisional display reading; first guess from one case)* | **Advisory** | **Missing measurement:** store convergence and verifier-refusal rates across all filed requests that hand a worker a prior refused commit, compared with requests built from `HEAD` ([§2](#2-the-case-measured), observation 5, cites `n = 1` case rather than a store measurement) |

How each caller uses them:

- **Operator at filing** reads `S6`, `S8`, `S9` and `S10` from the words before filing; all four are **Advisory** today (shown once slice 11 lands, never routing an item until measured from the store).
- **Design seat** reads `S8` as advisory guidance over its own design, and reads every measured or advisory signal shown on an item already in its hands (`D-SP2`).
- **Planner** reads `S5`, `S6` and `S7` over its own plan; all three are **Advisory** today (shown on the plan once slice 10 lands, never replacing a piece list with a routing split call until measured from the store).
- **Verifier** states the facts behind `S2` and `S4`. The drain reads `S1` and `S3`, compares for `S2`, shows all four on the item page, and acts on **Measured** `S1` alone (once slice 8 lands) while `S2`, `S3` and `S4` stay **Advisory**.

**The numbers are settings, with these defaults, and configuration alone never promotes an advisory signal.** `S1`'s two limits (`file_limit = 27`, `added_line_limit = 1200` in `.tillr.toml`), `S5`'s piece count, `S6`'s area count, `S8`'s count and `S9`'s clause count live in tracked configuration so a change to a reference line is a reviewed commit. The item page shows each reading beside its configured reference line on every refusal, whether or not a signal held, so store evidence accumulates against real items. Promoting a signal from **Advisory** to **Measured** requires measuring its trigger from the store and recording that measurement in this table in a reviewed design commit before any slice routes on it.

**What is not a signal.** The number of attempts, the number of refusals, elapsed time, a gate failure, a merge conflict, an adapter fault, a growing test suite on its own, and a broad or short request. A repeated identical finding stays with the existing hold.

## 7. What a split produces

### 7.1 The split statement

The design seat answers a call with one of two things. A third answer, which keeps the item and defers named cases to requests of their own, is proposed in [the request amendment design](./tillr-request-amendment.md#62-deferring-a-case) and is not built.

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
- **The named paths are instructions, not a fence.** No per-item path list exists, and the split does not bring one back. Whether the part changed only what its share needs is the part's verifier's judgment, under the first standard criterion.
- **Derived files are regenerated, never carved.** The pack copy is rebuilt by its own command.
- **A part that follows another carves onto a trunk that already holds it.** A conflict there is the carving run's ordinary work.
- **The part that inherits the refused findings** is given them word for word, and the donor, so its worker starts from what was tried.

Where there is no donor (a split at filing, at design or at the plan step), every part takes nothing and is built in the ordinary way.

### 7.5 When the parent is finished

When the last required part has landed, the parent runs once more: a read-only verifier judges the parent's own criteria against the trunk, with the coverage table and each part's landing as its evidence. This is the closing check.

- **It passes:** the parent is finished, with no commit of its own. Its record lists the parts and their commits.
- **It finds something missing:** the finding goes to the design seat, which adds a part to the statement. The parent waits on that part. A statement only grows; a filed part is never removed from it.

### 7.6 Failure and edge cases

| Case | What happens |
| :--- | :--- |
| The design seat's run fails or ends without an answer | The item is held on the design seat with the call shown; the existing route for a design run that repeats applies. The donor stays pinned |
| The statement is malformed, has one part, or leaves a clause unowned | It is refused and returned to the design seat with the reason; nothing is filed |
| A part is refused by its verifier | It goes back to its own build step, as any item does. Its siblings are unaffected |
| A part's verifier names a regression in work taken from the donor | The same: the part is repaired or split again. The donor is never the excuse |
| A part trips a measured signal itself | It is split again (advisory signals on a part are shown without routing, as on any item) |
| The owner drops a required part | His dated words are recorded on the part, the part is closed, and the coverage table shows the clauses he released. Nobody else may do this |
| A part is closed any other way | The parent stays blocked and shows the closed part as needing the owner |
| Two parts change the same lines | The one that lands second meets a conflict at carving or at landing, and repairs it as any item does |
| The trunk moved a long way since the donor | The carving run works on the current trunk; nothing requires the donor's diff to survive unchanged |
| The split is called before any candidate exists | No donor; parts are built from nothing |
| An item is in flight on the day this ships | Nothing changes for it until its next settlement, when the signals are read like any other item's |

## 8. How a verdict accepts part of a candidate

### 8.1 What the verifier states

Today a verdict says, per criterion, done or not done, with a reason. To let accepted work land, a not-done verdict states four more things. None of them asks the verifier to design the split.

1. **Each finding's kind**, one of three:
   - **Gap**: in the case named, the trunk with the candidate behaves as the trunk does today. The request is not yet met there, and nothing is worse.
   - **Regression**: in the case named, the trunk with the candidate behaves worse than today, or something that passes today fails.
   - **Unasked change**: the candidate changes something no criterion needs.
2. **Each finding's place**: the case in one sentence, and the paths that implement it, using the file locators a verdict already carries.
3. **Each earlier finding's fate**: for every finding returned to the builder by the previous verdict on this item, fixed or not fixed.
4. **What was examined**: the whole candidate, or the paths that were not.

The verifier decides a finding's kind by the test in its definition, and gives the evidence: for a gap, that the named case behaves the same on the base. A finding it cannot classify is stated as a regression.

### 8.2 What the design seat does with it

| The verdict says | The split statement |
| :--- | :--- |
| Every finding is a gap, and the whole candidate was examined | One part takes the donor whole; its words leave the named cases to a second part, which takes nothing and inherits the findings |
| Regressions or unasked changes sit in paths the rest does not need | One part takes the donor without those paths; another inherits them and the findings |
| A regression sits in code the rest depends on | No split. The findings go back to the build step, as today |
| Some paths were not examined | Those paths go to a part that takes nothing from the donor for them, or to no split, at the design seat's judgment |

### 8.3 Why nothing is inherited, and why a part still lands

A verdict binds one criterion text to one exact tree. A part has different words and, unless it takes the donor whole, a different tree. So an acceptance stated about the donor is evidence shown to the part's verifier and never a verdict for the part.

What keeps the part's own verification from repeating the parent's twelve refusals is P6. The part's verifier is given the cases its words leave to siblings. When it finds another case of the parent's request that the part does not handle, and that case is a gap, the finding is recorded against the parent and added to the sibling part that owns the remaining cases. The part is not refused for it. The part is refused for a regression, an unasked change, a missing test, or a failure of the share it claims.

So a behavior can be on the trunk while named cases of the request are still queued. The owner ruled on 2026-11-21 that this is the healthier outcome ([§1](#1-the-ruling)): the working part is a small functional change that can be reviewed and tested by itself, and each queued case follows as its own part. The parent stays open and visible until those cases land ([§7.5](#75-when-the-parent-is-finished)).

## 9. What the queue and the status surfaces show

Nothing about a split is hidden, and every waiting state names who moves it.

| Surface | A parent | A part | A pending call |
| :--- | :--- | :--- | :--- |
| The queue's reasons (`tillr drain why`, and the Home and Items pages that read it) | One row until the closing check passes: "split into 5 parts: 3 landed, 1 working, 1 held", the held part's reason and its mover | An ordinary row, labelled with its parent's title and its part label | "split called by the verifier at 6:29 PM; waiting on the design seat", mover the design seat |
| The item page | The owner's words; the call, with each signal's value beside its threshold; the statement with its coverage table; the donor and what each part took; each part's state and landed commit; any part the owner dropped, with his words | Its parent, its share, what it leaves to which sibling, what it took from the donor, the findings it inherited | The call and the measurements |
| Counts and reports of landed work | Counted as landed only after the closing check. Until then it reads "3 of 5 required parts landed" | Each landing is reported under the parent's title | Not counted |

Two further rules:

- **Measurements are shown before any signal holds.** Every refused candidate's size, and the comparison of its findings with the previous verdict's, appear on the item page from the first refusal. Advisory signals stay here as displayed facts and never record a pending call or route the item.
- **A "no split" answer is shown**, with its reason, on the item page and in the queue's reason for the item, so the next call is read against it.

## 10. How this fits what exists

### 10.1 The planner's pieces

Pieces and parts answer different questions. A piece is a step toward one landing: pieces share a lane, a verifier, a gate and a commit, and a piece need not work alone. A part is a landing. A part's own plan may divide it into pieces as today. When the planner calls a split it may attach its grouping of pieces, which the design seat may adopt as the parts.

### 10.2 The write fence

The per-item path list was retired on 2026-11-19, and expansion and dispatch need none ([item graphs](../reference/tillr-item-graphs.md) records the change). What remains is the common deny-list, which applies to every part as to every item, and a piece's own write scope inside one plan. A split adds no fence. The paths in a statement tell a carving run what to take, and a part that changes a path its statement did not name is judged by its verifier, not refused by a check.

### 10.3 One squash per item

The owner ruled on 2026-11-15 that an item lands as one squashed commit ([`OQ-IG1`](../reference/tillr-item-graphs.md#why-its-this-way)), and the lander's invariant is one landing, one squash. A split keeps both, because each part is an item. The parent lands no commit.

The owner's words float the other shape, "multiple judged landings out of one item". I did not take it, for three reasons: it would need him to amend his own ruling; a verdict, a gate result and a landing record each bind one item to one tree, so every one of them would need a second key; and an item's criteria freeze at its first landing, so the remaining parts would be judged against text nobody can correct. Part items give him the same result, several judged landings out of one request, with none of those changes.

### 10.4 The landing principles

- **[Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once).** Each part is gated on its own tree. No gate result carries over from the donor.
- **[Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch).** A carved part is not required to match the donor's diff.
- **[A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report).** [§9](#9-what-the-queue-and-the-status-surfaces-show).
- **[A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act).** A pending call names the design seat; a blocked parent names its unlanded parts; a closed required part names the owner.

### 10.5 No count of attempts stops work

The owner ruled on 2026-11-17 that no count of worker endings determines routing ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)), and the attempt number and refusal tallies only describe history ([`D-JL21`](./tillr-judge-and-lifecycle.md#413-what-the-attempt-number-and-refusal-counts-tell-the-operator-d-jl21)). On 2026-11-20 he asked of repeated refusals, "Are the refusals changing?" On 2026-11-23 he objected again to ungrounded limits breaking runs ([§1](#1-the-ruling), `R-SP2`).

The split signals follow both rules. None reads a tally, and none acts on a first-guess threshold (`D-SP15`). `S1` measures candidate size against a 95th-percentile threshold read from 1,049 landings in the store. `S2` compares the evidence of two consecutive verdicts — making his question checkable on the item page (slice 3) — and stays **Advisory** (shown, never acted on) until store history measures what trigger separates an ordinary repair from a non-converging loop; `S3` likewise stays **Advisory**. And a split when a measured signal holds is the opposite of a stop: the work continues in a different shape, with no hold on the owner and no limit on how often a part may be repaired.

### 10.6 The proposal of 2026-11-20

When each repair finds a different defect proposed measuring three comparisons and a split by hand. This design keeps its comparisons (they are S2, S3 and the item page's measurements), its rule that the old candidate is a donor and never a verified child, and its count of required parts landed. It replaces three things: the operator filing one request per part by hand becomes the drain filing from the statement; the original recorded as "replaced, not landed" becomes a parent that waits and is closed by a check; and the old candidate's work is carved into the parts instead of being offered as reference. Its two questions for the owner are answered here as designer's decisions `D-SP11` and `D-SP12`.

## 11. What this does not do

- **It does not cap an item's size.** A large candidate its verifier accepts whole lands as one commit.
- **It does not let the drain cut code by itself.** The drain measures and files; a seat decides the division and a worker carves.
- **It does not change the gate, the lander or the squash.**
- **It does not change how pieces work**, or raise their limit of twelve.
- **It does not ask the owner to approve a split.** He sees it, and only a dropped part needs him.
- **It does not name commands, fields, events or store tables.** Those belong to the implementation plan.
- **It does not decide filing policy** beyond the signals an operator reads. Whether a filing should refuse a request outright is not proposed.

## 12. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Several judged landings recorded on one item | Rejected: it amends the owner's one-squash ruling and re-keys verdicts, gate results and landings ([§10.3](#103-one-squash-per-item)) |
| Replace the item with new items and close it | Rejected: the request disappears from the queue as one thing, and nothing checks the seams. [Criterion repair](./criterion-repair.md#9-alternatives-considered) rejected the same shape for orphaning the old record |
| Let the verifier write the split | Rejected: the judge would author the bar it applies |
| Let the planner write the split | Rejected: the planner is held to the criteria it would be dividing. It may propose a grouping |
| Split automatically along the planner's pieces | Rejected: a piece need not work alone, and in the measured case two pieces held one behavior |
| Split when a count of refusals is reached | Rejected: the owner ruled out counts on 2026-11-17 ([§10.5](#105-no-count-of-attempts-stops-work)) |
| Refuse oversized requests at filing | Rejected as the only defense by the owner's own words: "ones will slip through" |
| Carry the verifier's acceptance to the part as a verdict | Rejected: a verdict binds a text and a tree, and the part has neither in common with the parent ([§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands)) |
| Only complete behaviors land: a part must meet every word of the request about its behavior | Rejected by the owner on 2026-11-21 ([`OQ-SP1`](#OQ-SP1)). In the measured case nothing would have landed, because every finding was a case of one behavior |
| Bring back a path list for each part | Rejected: the owner retired the per-item fence on 2026-11-19 |

## 13. Risks

| Risk | Mitigation |
| :--- | :--- |
| A defect hides in the seam between two parts | The closing check judges the parent's criteria on the trunk; a criterion that needs end-to-end proof is owned by the last part or by that check |
| The trunk carries a behavior with queued cases, and they are never built | The owner accepted a part landing before its cases ([§1](#1-the-ruling)). The parent stays open and visible with its count of parts; only the owner drops a part |
| A finding is called a gap when it is a regression | The verifier must show the base behaves the same; an unclassifiable finding is a regression; the part's gate still runs every check |
| Splits happen where none is needed, and each costs a design run and extra gates | A candidate accepted whole is never split; no advisory signal calls a split; the design seat may answer "no split"; readings are shown against every refusal before any unmeasured signal is promoted |
| The design seat becomes the queue's narrow point | A call releases the lane; unrelated work continues; how long calls wait is visible in the queue's reasons |
| A carving run rebuilds instead of taking | Its brief names the donor and the paths, and its verifier sees both the donor and the part's diff |
| The thresholds are wrong | Each signal is marked **Measured** or **Advisory** in [§6](#6-the-signals); only a store-measured trigger may act on routing (`S1` today), while advisory signals (`S2`–`S10`) stay displayed facts until measured from the store |

## 14. The build, in slices that each land alone

The build follows the design's own rule. Each slice below is filed as its own request and lands as its own commit. Each one is useful on the trunk if no later slice ever lands, and none makes the trunk worse while the later ones are queued. A slice that trips a measured signal of [§6](#6-the-signals) when it is built is split again.

The slices and their order are the designer's decision (`D-SP13`, amended by `D-SP15`), and the owner may overturn them. The order puts what only shows facts first, so store history accumulates on real items without moving anything. Next comes a split made by a person, which already replaces filing parts by hand. Then comes each caller — and under `D-SP15` **an unbuilt slice may act on routing only for signals marked Measured in [§6](#6-the-signals)**. Every unbuilt slice (`3` and `5`–`12`) states in the table below which signals it may act on.

| Slice | What lands | Why it is useful alone | Signals it may act on | Follows |
| ---: | :--- | :--- | :--- | :--- |
| 1 (built) | Every refused candidate's size (files and added lines, without the pack copy) is measured and shown on its item page beside `S1`'s limits. The limits are tracked settings with the defaults of [§6](#6-the-signals). Nothing is routed | An operator sees an oversized candidate at its first refusal, and the limits can be read against real refusals | **None** (`S1` is recorded and displayed only) | — |
| 2 (marked built ahead of its code, `D-SP14`) | A not-done verdict states each finding's kind (gap, regression, unasked change) and place, and the item page shows them. Nothing is routed on them | A reader of a refusal sees whether the trunk would be worse or only unfinished, and where | **None** | — |
| 3 | A not-done verdict states each earlier finding's fate and what was examined. The item page shows the comparison with the previous verdict, unexamined paths, and the growth of the candidate (the facts behind `S2`, `S3` and `S4`). Nothing is routed | The owner's question "Are the refusals changing?" is answered on the page for every item, and the store collects the history needed to measure `S2`, `S3` and `S4` | **None** (`S2`, `S3` and `S4` are **Advisory**: shown, never acted on) | 2 |
| 4 (marked built ahead of its code, `D-SP14`) | A split statement written by the design seat is checked and filed in one transaction: the parts as items, their order, and the parent blocked on its required parts. Every part takes nothing from a donor. A malformed statement is refused whole. Until slice 7 lands, a parent whose required parts have all landed names the operator as its mover, who checks its criteria by hand | A split made by a person files correctly and at once, in place of one request per part filed by hand | **None** (files a statement written directly by the design seat) | — |
| 5 | The queue's reasons and the item page show a parent as one row with its count of parts, and a part with its parent, its share and what it leaves to which sibling ([§9](#9-what-the-queue-and-the-status-surfaces-show)) | A split is readable as one request on every surface | **None** (renders already-filed parents and parts; routes nothing) | 4 |
| 6 | A part's verifier is given the cases the part's words leave to siblings. A gap it finds in another case of the parent's request is recorded against the parent and added to the sibling that owns the remaining cases, and the part is not refused for it (P6) | Part of a change lands while a named case is still queued, which is the owner's ruling of 2026-11-21 | **None** (applies P6 gap accounting on an already-filed part; calls no split) | 2, 4 |
| 7 | The closing check: when the last required part lands, a read-only verifier judges the parent's criteria against the trunk. A pass finishes the parent, and a finding adds a part | A parent finishes without a person, and a defect in a seam between parts is found | **None** (verifies the parent's criteria after its required parts land; calls no split) | 4 |
| 8 | The verifier's call: at a not-done verdict the drain records a split call **only when a measured verdict signal holds (`S1` today)**, pins the candidate as the donor, releases the lane and sends the item to the design seat, which answers with a statement or with "no split". `S2`, `S3` and `S4` are shown alongside the refusal and never record a call or route the item while **Advisory**. A pending call and a "no split" answer are shown | An oversized item like the measured one (34 files and 2,189 added lines at refusal 1 in [§2](#2-the-case-measured)) leaves the repair loop at its first refusal exceeding `S1`. Its parts are built from nothing until slice 9 lands | **`S1` only** (`S2`, `S3` and `S4` are **Advisory** — shown, never acted on — until measured from the store and marked **Measured** in [§6](#6-the-signals)) | 1, 3, 4 |
| 9 | Carving: a part takes the donor whole, or takes named work in a carving run ([§7.4](#74-carving-the-donor)) | Work already built is taken and not rebuilt | **None** (carves the donor for parts of an already-called split; routes on no signal) | 8 |
| 10 | The planner's signals and call: the plan step records and shows `S5`, `S6` and `S7` on the plan, and may end with a split call in place of a piece list (with its grouping attached) **only when a measured plan signal holds** (`S5`, `S6` and `S7` are all **Advisory** today, so slice 10 shows them without routing until measured) | Plan-time readings are visible and stored on real runs without routing on a guess; once a trigger is measured, an oversized item is split before anything is built | **None today** (`S5`, `S6` and `S7` are all **Advisory**; may act on routing only for whichever of `S5`, `S6` or `S7` has its trigger measured from the store and marked **Measured** in [§6](#6-the-signals)) | 4 |
| 11 | Filing signals and the operator's call at filing: filing records and shows `S6`, `S8`, `S9` and `S10` on the request, and routes the item to the design seat before any plan or build run **only when a measured filing signal holds** (`S6`, `S8`, `S9` and `S10` are all **Advisory** today, so slice 11 shows them without routing until measured) | Filing-time readings are visible and stored without routing on a guess; once a trigger is measured, an oversized request goes to the design seat before any run starts | **None today** (`S6`, `S8`, `S9` and `S10` are all **Advisory**; may act on routing only for whichever of `S6`, `S8`, `S9` or `S10` has its trigger measured from the store and marked **Measured** in [§6](#6-the-signals)) | 4 |
| 12 | The owner drops a required part: his dated words are recorded on the part, the part is closed, and the coverage table shows the clauses he released. A required part closed any other way shows as needing the owner | A parent can finish without a part the owner no longer wants | **None** (records the owner's release of a required part; calls no split) | 7 |

Two slices where neither follows the other may be built at once in separate lanes. Slices 1, 2 and 4 follow nothing.

**Candidate note (2026-11-24 amendment).**

- **Filed-but-unlanded build items that would make a first-guess signal act on routing:** **None (`0`).** Checked read-only against the queue store on 2026-11-24 across all non-terminal items (`in-queue`, `not-started` and `blocked`): all three build items ever filed from this design — slice 1 (`b54e403ca5`), slice 2 (`1898d78bd2`) and slice 4 (`2080723bd6`) — have already landed on the trunk, and none routes on a split signal; slices 3 and 5–12 have no filed build items in the queue.
- **Code and configuration untouched:** [`.tillr.toml`](../../.tillr.toml) `[candidate_size]` (`file_limit = 27`, `added_line_limit = 1200`, commented `"Advisory thresholds recorded beside each verifier refusal; neither blocks landing"`) and [`tools/tillr-drain/drain/candidate_size.go`](../../tools/tillr-drain/drain/candidate_size.go) (`measureCandidateSize`) only record size beside a refusal and do not route work; per the request, no code or configuration file is changed.
- **Out-of-scope parts and adjacent gaps (listed, not changed here):**
  1. *Other parts of the split decision (who decides, how slices and parts are filed and carved):* [§5](#5-who-calls-a-split-and-when) (`D-SP2`, including the design seat writing a split statement directly when authoring a design or answering an obstacle) and [§7](#7-what-a-split-produces) (`D-SP1`, `D-SP5`, `D-SP6`, `D-SP9`) are out of scope of this request and unchanged.
  2. *`S1`'s landed-commit population and 2-line rounding:* `S1`'s `27` files and `1,198` added lines ([§2](#2-the-case-measured)) were measured across 1,049 landed commits rather than refusal-time candidates (which slice 1 now records on `drain.refusal`), and `.tillr.toml` rounds `1,198` to `added_line_limit = 1200`.
  3. *`D-SP14` ahead-of-code marks on slices 2 and 4:* slices 2 (`1898d78bd2`) and 4 (`2080723bd6`) have landed on the trunk since 2026-11-22, but sweeping their `"marked built ahead of its code"` text is outside what this request asks for and is left untouched.
  4. *Sibling documents written before `D-SP15`:* [`docs/design/operator-decisions-inside-tillr.md`](./operator-decisions-inside-tillr.md) (which describes slice 8 as leaving the repair loop "at the first changed finding") and [`docs/design/tillr-request-amendment.md`](./tillr-request-amendment.md) (which links filing flaw `F5` to [§6](#6-the-signals)) are outside this request's named document (`docs/design/tillr-item-split.md`) and are left untouched.

**Who marks a slice built** (the designer's decision `D-SP14`, which the owner may overturn). Each slice's request, written by the operator, asks for the slice to be built and for this table to mark it built. Those are two changes by two seats. A build seat does not edit a design document, so the code is the build seat's and the mark is the design seat's. The design seat writes the mark when the request reaches it, with the words "ahead of its code", and the status line at the top of this document says that the code has not landed. The build commit for the slice then changes no line of this document, and the request's sentence about the table is already answered when the build starts: a build worker raises no obstacle for it, and a verifier does not refuse the build for it. The words "ahead of its code" are removed by the design seat the next time it edits this document after the slice's commit is on the trunk. A reader who needs the fact and not the mark reads the trunk: slice 2's code is present when a not-done verdict's findings each carry a kind and a place and the item page shows them, and slice 4's code is present when the drain refuses a malformed split statement and files a well-formed one.

The marks on slice 4 and on slice 2 were written this way on 2026-11-22, each when its request reached the design seat. Slice 2's request returned to the design seat because a build worker named its sentence about the table as an obstacle while the table still showed the slice unmarked, which is the case this rule exists to answer. Slice 1 was marked by its own build commit on 2026-11-21, before this rule was written down here, and its mark is left as it is because its code is on the trunk.

## Open Questions

None. [OQ-SP1](#OQ-SP1) was ruled by the owner on 2026-11-21 and is compacted into the [Decision Ledger](#decision-ledger).

## Decision Ledger

**Who decided each row.** A row marked "owner" quotes the owner, and there are three: `R-SP1`, [`OQ-SP1`](#OQ-SP1) and `R-SP2`. Every other row is the designer's decision of 2026-11-21, made without him, and he may overturn it, except `D-SP14` (2026-11-22) and `D-SP15` (2026-11-24), which are the designer's decisions answering filed operator requests and which he may likewise overturn. The last column records what slice 1 implements and what slices 2 and 4 were marked ahead of their code as `D-SP14` describes; slices 3 and 5–12 are unbuilt.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-SP1 | **Owner ruling.** Oversized changes are broken up past some threshold; the accepted parts land without the part objected to; several seats may say "split"; each has clear guidance. His words are quoted in full in [§1](#1-the-ruling) | 2026-11-21, owner | [§1](#1-the-ruling) | — |
| <a id="OQ-SP1"></a>[`OQ-SP1`](#OQ-SP1) | **Owner ruling.** Part of a change may land while a named case of the request is still unmet; the case follows as its own part (option A, "It lands, and the case follows"). A part is refused for making the trunk worse, for an unasked change, for a missing test or for not delivering its share, and not for a case its words leave to a sibling. Asked whether part of a change may land while a named case of the request is still unmet, the owner said, verbatim: "part of desgins can absoutely land. this is generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the smallest functional change possible to make reviewing, testing, velocity, all this stuff easier, and it will also make it easier for us." **Rejected:** only complete behaviors land (option B) | 2026-11-21, owner | [§1](#1-the-ruling), [§4](#4-the-rules-a-split-keeps) (P6), [§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands) | — |
| R-SP2 | **Owner ruling.** Seeing `added_line_limit = 1200` in [`.tillr.toml`](../../.tillr.toml), the owner said on 2026-11-23 at about 01:05 ET, verbatim: "where'd this 1200 line limit come from? we have random thresholds again breaking things?" Asked whether to file this design item, the owner said, verbatim: "yes". Stands with his 2026-11-17 ruling against arbitrary attempt or retry limits ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | 2026-11-23, owner | [§1](#1-the-ruling) | — |
| D-SP1 | **Designer's decision; the owner may overturn it.** A split produces part items under a parent that stays open, not several landings of one item. **Rejected:** several landings on one item, which would amend his one-squash ruling | 2026-11-21, designer | [§7.2](#72-parts-and-the-parent), [§10.3](#103-one-squash-per-item) | — |
| D-SP2 | **Designer's decision; the owner may overturn it.** Four callers call a split: the operator at filing, the design seat, the planner at the plan step, and the verifier at a not-done verdict. The design seat alone writes the division, and may answer "no split" | 2026-11-21, designer | [§5](#5-who-calls-a-split-and-when), [§7.1](#71-the-split-statement) | — |
| D-SP3 | **Designer's decision; the owner may overturn it (amended by `D-SP15`).** The ten signals of [§6](#6-the-signals), and their reference lines. Only `S1`'s size limits (27 files, 1,200 added lines) have a store-measured trigger; `S2` to `S10` are **Advisory** until measured from the store (`D-SP15`) | 2026-11-21, designer | [§6](#6-the-signals) | Slice 1: `S1` counts and limit settings only |
| D-SP4 | **Designer's decision; the owner may overturn it.** Size alone never splits a candidate its verifier accepted whole, and no signal counts attempts or refusals | 2026-11-21, designer | [§5](#5-who-calls-a-split-and-when), [§10.5](#105-no-count-of-attempts-stops-work) | — |
| D-SP5 | **Designer's decision; the owner may overturn it.** The parent's criteria are never changed by a split. Each is owned by a part, clause by clause where needed; each part carries its own copy of the four standard criteria | 2026-11-21, designer | [§7.3](#73-how-criteria-are-divided) | — |
| D-SP6 | **Designer's decision; the owner may overturn it.** The refused candidate is a donor. A part takes it whole, takes named work in a carving run, or takes nothing. Named paths are instructions and not a fence | 2026-11-21, designer | [§7.4](#74-carving-the-donor) | — |
| D-SP7 | **Designer's decision; the owner may overturn it.** A not-done verdict states each finding's kind (gap, regression, unasked change) and place, each earlier finding's fate, and what was examined. The verifier does not propose the parts | 2026-11-21, designer | [§8.1](#81-what-the-verifier-states) | Slice 2, marked ahead of its code: each finding's kind and place only |
| D-SP8 | **Designer's decision; the owner may overturn it.** No verdict or gate result is inherited from the donor: every part is verified and gated on its own tree | 2026-11-21, designer | [§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands) | — |
| D-SP9 | **Designer's decision; the owner may overturn it.** The parent finishes by a closing check of its own criteria against the trunk; a finding there adds a part. Only the owner makes a required part optional | 2026-11-21, designer | [§7.5](#75-when-the-parent-is-finished), [§7.6](#76-failure-and-edge-cases) | — |
| D-SP10 | **Designer's decision; the owner may overturn it.** The queue shows a parent as one row with its count of parts until the closing check passes, and shows every measurement before any signal holds | 2026-11-21, designer | [§9](#9-what-the-queue-and-the-status-surfaces-show) | Slice 1: size and recorded limits on the item page only |
| D-SP11 | **Designer's decision; the owner may overturn it.** No count of verifier refusals pauses an item for review. Answers the first question of the proposal of 2026-11-20, consistent with the owner's ruling `D-JL10` | 2026-11-21, designer | [§10.5](#105-no-count-of-attempts-stops-work) | — |
| D-SP12 | **Designer's decision; the owner may overturn it.** The owner is not asked to approve or acknowledge a split. He sees it on the surfaces, and is asked only when a required part would be dropped. Answers the second question of the same proposal | 2026-11-21, designer | [§9](#9-what-the-queue-and-the-status-surfaces-show), [§11](#11-what-this-does-not-do) | — |
| D-SP13 | **Designer's decision; the owner may overturn it (amended by `D-SP15`).** The build is twelve slices that each land alone, in the order of [§14](#14-the-build-in-slices-that-each-land-alone): what only shows facts first, then a split made by a person, then each caller, with unbuilt slices constrained to act only on signals marked **Measured** in [§6](#6-the-signals) | 2026-11-21, designer | [§14](#14-the-build-in-slices-that-each-land-alone) | Slice 1; slices 2 and 4 marked ahead of their code |
| D-SP14 | **Designer's decision, answering an operator's request; the owner may overturn it.** The operator's request for slice 4 asks the build to mark the slice built in the slice table. A build seat does not edit a design document, so the mark is the design seat's and the code is the build seat's: the design seat marks the slice "built ahead of its code" when the request reaches it, the slice's build commit changes no line of this document, and neither a build worker nor a verifier treats the request's sentence about the table as unmet. The same holds for every later slice's request. **Rejected:** the build commit edits this document, which the drain's rule for build seats forbids; and leaving the table unmarked until the code lands, which returns every slice's request to the design seat with the same obstacle | 2026-11-22, designer | [§14](#14-the-build-in-slices-that-each-land-alone) | The marks on slices 2 and 4 |
| D-SP15 | **Designer's decision, answering an operator's request; the owner may overturn it.** Amends `D-SP3` and `D-SP13`. A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on. A threshold that routes work must come from a store measurement, and one that has none stays an advisory displayed fact. In [§6](#6-the-signals), `S1` (`> 27` files or `> 1,200` added lines, from the 95th percentile of 1,049 store landings) is **Measured**; `S2` to `S10` are **Advisory**, each naming the store measurement it lacks, and tracked configuration limits do not promote an advisory signal to routing without a store measurement recorded in [§6](#6-the-signals). In [§14](#14-the-build-in-slices-that-each-land-alone), unbuilt slice 8 may act on `S1` only, unbuilt slices 10 and 11 display `S5`–`S7` and `S6`/`S8`–`S10` without acting on any signal until measured, and unbuilt slices 3, 5, 6, 7, 9 and 12 act on no signals. **Rejected:** having slices 8, 10 or 11 route items on first-guess triggers (`S2` at 1 occurrence, `S3` at any increase, `S4` at any path, `S5`–`S10`) and tuning after false splits break runs; omitting `S2`–`S10` from display so the store never collects the readings needed to measure them | 2026-11-24, designer | [§1](#1-the-ruling), [§4](#4-the-rules-a-split-keeps) (P4, P7), [§5](#5-who-calls-a-split-and-when), [§6](#6-the-signals), [§10.5](#105-no-count-of-attempts-stops-work), [§13](#13-risks), [§14](#14-the-build-in-slices-that-each-land-alone) | Slice 1 already displays `S1` advisory counts without routing; no code changed in this commit |
