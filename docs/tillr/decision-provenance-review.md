---
title: "Who made the decisions recorded in the tillr designs, and which of them the owner should now rule on"
status: in-review
stage: DESIGN
tags: [tillr, review, provenance, decisions, design]
summary: "The owner's rulings on decisions that agents made in the tillr designs without the owner's words, the ten questions of that kind still open, and the rule for how such decisions are recorded from here on (OQ-PR21 and OQ-PR22)."
next: "The owner answers the ten open questions (OQ-PR6 to OQ-PR11, OQ-PR15, OQ-PR16, OQ-PR18 and OQ-PR19); then build the gate check and the loop rule of Section 6, which no check enforces yet."
depends-on:
  - ../design/tillr-queue-mirrors-designs.md#OQ-QM1
---

# Who made the decisions recorded in the tillr designs, and which of them the owner should now rule on

**Status:** Awaiting the owner's rulings on ten open questions. Twelve are settled in
[the ledger](#decision-ledger), whose Built column says what each ruling still owes. The rule
in [Section 6](#6-how-this-stops-happening) is not enforced by any check:
`rg 'Needs your ruling' tools/gate` returns nothing.

> **In short.** A review of 380 recorded decisions in the tillr designs found 303 that carry
> no words of the owner's, 64 of them of high significance. This page puts the ones that
> matter in front of the owner as questions, records the rulings, and states the rule that stops
> an agent's answer from being recorded as the owner's.

**Why it matters.** A decision that changes the owner's documents, repository, spend, models
or what the owner sees is the owner's to make. Several were made by agents and built on.

**The shape.** [Ten open questions](#2-questions-for-the-owner), hardest to undo first;
then [the rule from here on](#6-how-this-stops-happening).

**Cost.** Reading time. An answer that keeps today's behavior costs nothing more; one that
changes it becomes work.

**Start at:** [OQ-PR6](#OQ-PR6).

**Needs your ruling:** [OQ-PR6](#OQ-PR6), [OQ-PR7](#OQ-PR7), [OQ-PR8](#OQ-PR8),
[OQ-PR9](#OQ-PR9), [OQ-PR10](#OQ-PR10), [OQ-PR11](#OQ-PR11), [OQ-PR15](#OQ-PR15),
[OQ-PR16](#OQ-PR16), [OQ-PR18](#OQ-PR18), [OQ-PR19](#OQ-PR19) (plus the designer's decisions
`D-PR1` to `D-PR4` in [the ledger](#decision-ledger), which the owner may overturn).

**Reads with:** [the queue mirrors the roadmap's designs](../design/tillr-queue-mirrors-designs.md),
whose [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) settles the hidden item
comment in design documents; this page does not ask that again.

---

## 1. What the review found

An audit of 380 ledger rows across the tillr designs classed 70 as the owner's, 7 as delegated
by the owner for that row, 293 as an agent's and 10 as naming nobody; 64 of the last two groups
are of high significance. The classing rests on each row's own text: a row is the owner's only
when it says the owner ruled, and "an agent's" means no words of the owner's are recorded, not
that the owner would decide otherwise. The counts are one reading and are not kept current; each
design's own ledger says who made each of its decisions.

The pattern behind the counts: where the owner delegated a method ("apply a principle I have
already recorded"), the principle applied was often the designer's own earlier leaning, a design
another agent wrote, or the fact that the code already did it.

### 1.4. Words used here

- **Loop:** tillr's long-running program that picks the next piece of work, runs an agent on
  it, checks the result and lands one commit on the trunk. **Item:** one piece of work in its
  queue.
- **Seat:** a named role that tillr's settings map to a backend and a model. **Design seat:**
  the seat that writes designs and answers blocked runs.
- **Ledger row:** one line of a design's Decision Ledger.
- **Gate:** the checks a change must pass. **Lander:** the one program that puts a commit on
  the trunk. **Store:** tillr's private database under `.tillr/`, never committed.
- **Pack:** the bundle that installs tillr and its documents into a workspace.
- **Vantage:** the document viewer in which the owner reads designs and answers questions.
- **Observatory:** tillr's web page.

## 2. Questions for the owner

Each question states one situation: what you do, what tillr does today, and the choice that
leaves. They are ordered by how much is built on each and how hard it is to undo, hardest
first. Under [OQ-PR22](#OQ-PR22), a question that a dated quotation of yours answers is
closed on that quote, and any other stays open.

**Asked elsewhere, so not asked here:** the hidden item comment in documents
([OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1)), and the pack's second copy of
every tillr program ([OQ-SH2](../design/tillr-selfhosted-build.md#OQ-SH2)).

1. 💬 **OQ-PR6: Are the right models planning and verifying your work?**

   A piece of work is planned, built, tested and verified, each by a seat with a model behind
   it. You named the models for implementation, scouts and design, and the settings in
   `config/tillr/backend-policy*.json` follow that. Two picks are not in your words: an agent
   chose the model tier for the `plan` and `verify` seats.

   - **A — The `plan` and `verify` seats stay as set.** Nothing changes, and the full set of
     seats becomes yours.
   - **B — Change one or both.** You name the model. A stronger one costs more on every piece
     of work; a cheaper one risks weaker plans or missed defects.

   <!-- vantage: question id=OQ-PR6 leaning="A: every other seat follows what you said, and these two are the only picks you did not state." -->

   _Leaning:_ A: every other seat follows what you said, and these two are the only picks you did not state.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-PR7: When a model provider refuses work, should tillr send it to the next provider without asking you?**

   The loop is running overnight and a subscription-billed backend runs out of quota. Today
   tillr sends the same work to the next backend on that seat's list in the same pass, so it
   carries on and is billed there, and you are not asked. It never moves work that is under
   review. Agents wrote this rule.

   - **A — Work moves by itself.** The queue keeps draining, and you meet the cost on the next
     bill. Each move is shown on the observatory.
   - **B — Work waits.** Refused work shows as waiting on quota until the provider recovers or
     you move it. There is no surprise bill, and that seat's work stops overnight.

   <!-- vantage: question id=OQ-PR7 leaning="A: your standing ruling is that the loop never stops on one item's condition." -->

   _Leaning:_ A: your standing ruling is that the loop never stops on one item's condition.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-PR8: Should one unanswered question in a design hold all of that design's work?**

   A design lists five build steps and has one question open for you, about the fifth. Today
   tillr holds every piece of work written in a document that has a question open. You ruled
   that questions live in documents, not that they stop work.

   - **A — The whole design waits.** Nothing is built on a design you may still change. Four
     steps that have nothing to do with the question sit idle until you answer.
   - **B — Only what the question touches waits.** The steps that name the question wait, and
     the other four are built. If your answer changes something they assumed, that work is
     redone.

   <!-- vantage: question id=OQ-PR8 leaning="B: today a step waits on a question that is not about it." -->

   _Leaning:_ B: today a step waits on a question that is not about it.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-PR9: Should renaming a heading in your roadmap be able to change the queue?**

   A roadmap heading that starts with `Icebox` or `Exploration` parks the designs linked under
   it (`parkedSections` in [`sync.go`](../../tools/tillr-item/item/sync.go)), so renaming
   "Exploration" changes the queue and `tillr item sync` prints nothing about it. A design's
   `stage` and `status` fields likewise decide whether tillr reports that the design owes
   work. You ruled on the Icebox ("icebox items should be loaded too, marked blocked for
   icebox", 2026-11-18). The `Exploration` heading, and a heading acting as a switch at all,
   are agents' choices.

   - **A — All of them stay switches.** Nothing changes. A rename or a header edit goes on
     changing the queue silently.
   - **B — Fewer switches, and sync says what it did.** Only the `Icebox` heading and the
     `stage` field act. Every sync lists each design it parked or loaded and the word that
     made it.

   <!-- vantage: question id=OQ-PR9 leaning="B: your rulings of 2026-11-18 need only Icebox and the stage word, and sync should say what each word made it do." -->

   _Leaning:_ B: your rulings of 2026-11-18 need only Icebox and the stage word, and sync should say what each word made it do.

   **Answer:**

   > _(empty — fill in when decided)_

5. 💬 **OQ-PR10: Should there be a way to queue work that has no document?**

   You think of a small chore and want it in the queue without writing a design. Work enters
   only by being written in a document: `tillr item seed` refuses and points at
   `tillr item sync`. You never ruled on a document-free list.

   - **A — Documents only.** There is one place to look for where work came from. A chore
     needs at least a line in a document.
   - **B — A tracked list file.** A chore is one line and one command. Work then has two
     sources to keep in step.

   <!-- vantage: question id=OQ-PR10 leaning="A: your goal that state lives in documents and your words of 2026-11-18 on syncing the roadmap both point this way." -->

   _Leaning:_ A: your goal that state lives in documents and your words of 2026-11-18 on syncing the roadmap both point this way.

   **Answer:**

   > _(empty — fill in when decided)_

6. 💬 **OQ-PR11: When you jot down an idea, should a design agent turn it into work without checking back with you?**

   You type a half-formed idea into the box on the observatory, and your words are saved. A
   design agent then takes one turn on the idea, the result of that turn lands, and titled
   pieces of work replace the idea in the queue. Only an idea the agent thinks needs you
   stops. Saving your words was your request; what happens next was an agent's choice.

   - **A — Straight into the queue, as today.** You see the pieces once they exist.
   - **B — You approve first.** The proposed pieces wait for your OK before they replace the
     idea. Nothing is queued that you have not seen, and every idea costs you a second visit.

   <!-- vantage: question id=OQ-PR11 leaning="A: it does what you asked for, and an idea that needs you already stops for you." -->

   _Leaning:_ A: it does what you asked for, and an idea that needs you already stops for you.

   **Answer:**

   > _(empty — fill in when decided)_

7. 💬 **OQ-PR15: Should copies of your tillr designs be installed in every other workspace?**

   [`tools/tillr/cmd/pack-docs`](../../tools/tillr/cmd/pack-docs) (`tools/tillr/packdocs`) copies every design that
   [the status index](./design-status.md) marks `shipped` into the pack, so every workspace
   that installs tillr gets them. No ledger row records that decision. The copies are a
   snapshot, and removing them is a small job.

   - **A — The designs travel with tillr.** Agents elsewhere can read what tillr does. Their
     copy lags the originals until the next copy is made.
   - **B — Programs only.** Nothing stale is out there to mislead. An agent elsewhere has to
     be told what tillr does, or work it out from the programs.

   <!-- vantage: question id=OQ-PR15 leaning="A: agents in other projects read them to learn what tillr does, and the cost is a snapshot that lags." -->

   _Leaning:_ A: agents in other projects read them to learn what tillr does, and the cost is a snapshot that lags.

   **Answer:**

   > _(empty — fill in when decided)_

8. 💬 **OQ-PR16: May tillr's web page change the queue without a login?**

   You drag a piece of work up the queue on the observatory, or type an idea into its box.
   The server accepts those two kinds of change when it is started with `--write`. It checks
   that the request came from its own page and asks for no login. An agent chose that.

   - **A — Changes allowed, no login.** You drag and type with nothing in the way. So can
     anyone else who can reach the page while it runs with `--write`.
   - **B — Changes allowed behind a login.** A login has to be built and kept working.
   - **C — Read-only.** You reorder the queue and add ideas from the command line.

   <!-- vantage: question id=OQ-PR16 leaning="A: you asked for dragging, which needs the page to change things, and the server listens on your own machine." -->

   _Leaning:_ A: you asked for dragging, which needs the page to change things, and the server listens on your own machine.

   **Answer:**

   > _(empty — fill in when decided)_

9. 💬 **OQ-PR18: Should an agent's run have a time limit?**

    A run has no time limit unless one is set, a run that has committed is judged on its
    commit before any set limit is looked at, and a run stopped at a set limit before
    committing is resumed. An agent wrote those rules
    ([run wall ruling](../reference/run-wall-ruling.md)). No dated quotation of yours rules on a
    default limit or on how many times a stopped run resumes.

    - **A — No limit, and a stopped run always resumes.** A long run finishes. A run that is
      going nowhere keeps spending until something else notices.
    - **B — A default limit.** You say how long. Runs past it are stopped.

    <!-- vantage: question id=OQ-PR18 leaning="A: a fixed limit kills finished work, and a cap on resumes is an arbitrary count." -->

    _Leaning:_ A: a fixed limit kills finished work, and a cap on resumes is an arbitrary count.

    **Answer:**

    > _(empty — fill in when decided)_

10. 💬 **OQ-PR19: When a check hangs a second time, should the work be handed to you?**

    You ruled that a time budget is a target and only a stall ends a check: "target budgets,
    watch for stalls, but don't let 'load is high' cause failures that would otherwise finish
    just fine" (2026-11-17). That did not say what happens after a stall. An agent chose it:
    the check runs once more, and a second stall on the same work hands the work to you
    ("second stall on head tree" in
    [`gate_stall.go`](../../tools/tillr-drain/drain/gate_stall.go)).

    - **A — Restarted, never counted.** A stalled check is restarted while its cause is
      diagnosed and fixed, and reaches you only when an agent raises an objection that names
      you. A check that keeps stalling keeps spending until the cause is fixed.
    - **B — Twice, then you.** As today.

    <!-- vantage: question id=OQ-PR19 leaning="A: a second hang is a tool or check fault for an agent to diagnose rather than a stop count for you, while the stall threshold stays as engineering detail." -->

    _Leaning:_ A: a second hang is a tool or check fault for an agent to diagnose rather than a stop count for you, while the stall threshold stays as engineering detail.

    **Answer:**

    > _(empty — fill in when decided)_

## 6. How this stops happening

Two rules are the owner's rulings. Three more (`D-PR1` to `D-PR3`) are the designer's
decisions, labeled as such; the owner may overturn any of them.

**Owner's ruling ([OQ-PR21](#OQ-PR21)): nothing is built on unseen product or user-facing
decisions.** Any decision that touches the owner's documents, repository, money,
models or what the owner sees is put to the owner as an open question, and nothing is built on that design
until the owner answers. Engineering detail is marked as the agent's decision and may be built on
without stopping the queue.

**Owner's ruling ([OQ-PR22](#OQ-PR22)): an agent closes a question addressed to the owner
only by quoting the owner.** The ledger row carries the exact quote and its date. An agent's own
leaning, another agent's design, an unquoted paraphrase, or the fact that code already
behaves a certain way never closes a question.

**D-PR1, designer's decision: only the owner's quoted, dated words are a ruling.** A
paraphrase is recorded as a paraphrase, with who wrote it.

**D-PR2, designer's decision: a designer's decision always says so, in the row.** The row
begins "Designer's decision; the owner may overturn this". A document that holds one never
says "Needs your ruling: None" without naming how many designer's decisions it holds, and its
title never calls itself a ruling.

**D-PR3, designer's decision: a check enforces these in the gate**, on lines a change adds.
It refuses:

- a ledger row that says "ruled" or "owner" with no quotation and no date;
- a header that says "Needs your ruling: None" over a ledger holding designer's decisions it
  does not count;
- a question closed in a change whose ledger row carries no dated quotation of the owner's
  words.

**What has to be built:** the gate check of `D-PR3` and its tests; and, for
[OQ-PR21](#OQ-PR21), a rule in sync and the loop that holds building on a design with an
unruled decision of the owner's kind while engineering detail does not hold it.

## Decision Ledger

`D-PR1` to `D-PR4` are designer's decisions, and the owner may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-PR1 | **Owner ruling:** Every landed commit says what was built in lasting words that make sense without anyone knowing what tillr is, and carries no reference to tillr, tillr items, runs or other ephemeral tillr state: *"Yes, we should obviously say what was built, but I don't want any references to tiller or tiller items. Those are temporarily ephemeral state. I only want references that will persist and make sense without anybody even knowing what tiller is."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | Partly: commits omit item ids and trailers; the loop's commits that delete a landed item's block from a document end with [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1) |
| OQ-PR2 | **Owner ruling:** Every code change is reviewed before it lands by a fresh agent with no memory of making the change, and documents-only landings skip review. The owner took option A: *"A, already ruled; confirm: on 2026-11-18 you asked for a fresh agent that reviews every code change before it lands."* | 2026-11-20 | [Diff review](../design/tillr-diff-review.md) | Tracked in [diff review](../design/tillr-diff-review.md) |
| OQ-PR3 | **Owner ruling:** A piece of work may run as a series of steps laid out in workflow files under `config/tillr/workflows/`, with a seat per step and file inclusion. The owner took option A: *"A: your rulings on the item graph, made after this was built, already assume it."* | 2026-11-20 | [Item graphs](../reference/tillr-item-graphs.md) | Yes |
| OQ-PR4 | **Owner ruling:** How the loop steps through the queue is left to the engineers as engineering detail. The owner took option A: *"A: you have said engineering detail is the agent's to decide, and nothing you see changes with it."* | 2026-11-20 | [Drain successor reference](../reference/tillr-drain-successor.md) | Yes |
| OQ-PR5 | **Owner ruling:** The observatory's layout (dedicated linked pages, stepping keys, home-page grouping by who must act, and fixed queue order) stays as the base, with changes requested as the owner uses it. The owner took option A: *"A: you use it daily and already ask for changes directly, which tests it better than rereading the alternatives."* | 2026-11-20 | [Observatory reference](../reference/observatory-redesign.md) | Yes |
| OQ-PR12 | **Owner ruling:** Comments on a design in Vantage become work for that document's design agent, and while that comment work is open, unstarted steps of that design wait. The owner took option A: *"A: comment work closes quickly, and building on a design you have just objected to wastes runs."* | 2026-11-20 | [Designer sessions](../reference/tillr-designer-sessions.md) | Yes |
| OQ-PR13 | **Owner ruling:** *"database only."* The hand-dragged queue order lives only in the store (`.tillr/state.db`), never in a tracked file. | 2026-11-20 | [Observatory actions](../reference/observatory-actions.md) | Yes: no tracked order file or setting remains, and `retired_order.go` in the drain retires an in-flight order record without landing it |
| OQ-PR14 | **Owner ruling:** No cheap model rewrites wording inside the owner's human-read design documents; prose shaping happens only on tillr's side or inside the agent-only companion `<topic>-plan.md`. The owner took option B: *"B, already settled by [OQ-QM1](../design/tillr-queue-mirrors-designs.md#OQ-QM1); confirm: human-read design documents hold no build steps, so no cheap model rewrites wording inside them."* | 2026-11-20 | [Section 2](#2-questions-for-the-owner) | No: `[prose] rewrite = true` is set in `.tillr.toml` |
| OQ-PR17 | **Owner ruling:** An agent judges as the judging step of the work's flow, with that step's instructions and references; no permission is issued to it and nothing about it expires: *"It's not a temporary grant. It's not a thing that will change on a whim. It's encoded for a purpose. ... here is the flow. There is a judge at this point, and they judge based on this."* | 2026-11-20 | [The judge design](../design/tillr-judge-and-lifecycle.md#6-the-judge-is-a-step-in-the-flow) | Yes for intake: a verdict that cites a grant is rejected as `grant-retired` in [`verdict.go`](../../tools/tillr-item/item/verdict.go) |
| OQ-PR20 | **Owner ruling (closed under [OQ-PR22](#OQ-PR22) on the owner's ruling of [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7)):** There is no cap on landings while a check is failing on the trunk; work that does not touch what the failing check reads keeps landing, and work that touches it waits for the repair. The owner asked *"why does a red check hold a queue?"* and took the stated leaning: *"C, weakly: it lets through exactly the items that cannot have made the red worse, so nothing is hidden and no 20-landing stop is needed; it is weak because a red on one of the eight whole-tree checks still holds the queue, and only B moves it then."* | 2026-11-18 | [Graph is the path](../design/tillr-graph-is-the-path.md#OQ-GP7) | Tracked under [OQ-GP7](../design/tillr-graph-is-the-path.md#OQ-GP7) |
| OQ-PR21 | **Owner ruling:** A decision that touches the owner's documents, repository, money, models or what the owner sees is put to the owner as a question, and nothing is built on that design until the owner answers; engineering detail is marked as the agent's and may be built on. The owner took option A, the stated leaning: *"A: it matches what you said on 2026-11-18, that you decide product direction and trade-offs and the agent decides engineering detail."* | 2026-11-19 | [Section 6](#6-how-this-stops-happening) | No |
| OQ-PR22 | **Owner ruling:** An agent closes a question addressed to the owner only when a dated ruling of the owner's answers it in so many words, and the record carries the quote; everything else stays open for the owner, and an agent's own leaning never counts as a principle. The owner took option A, the stated leaning: *"A: it keeps what you wanted, not being asked the same thing twice, and stops an agent's own leaning from counting as a principle."* | 2026-11-19 | [Section 6](#6-how-this-stops-happening) | No |
| D-PR1 | **Designer's decision; the owner may overturn this.** Only the owner's quoted, dated words are recorded as a ruling. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | No |
| D-PR2 | **Designer's decision; the owner may overturn this.** A designer's decision says so in its row, and its document never claims that nothing needs a ruling. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | No |
| D-PR3 | **Designer's decision; the owner may overturn this.** A gate check refuses an unquoted "ruled" row, a question closed without a dated quotation of the owner's words, and a false "Needs your ruling: None", on added lines only. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | No |
| D-PR4 | **Designer's decision; the owner may overturn this.** Where two audits rate a decision differently, this review uses the higher rating. | 2026-11-18 | [Section 6](#6-how-this-stops-happening) | No |
