---
title: "Model benchmarks from the drain's own recorded tasks"
date: 2026-11-17
status: draft
stage: SKETCH
next: "A design run writes the replay design below the request and numbers the questions the owner has to answer before anything is run."
tags: [tillr, models, benchmarks, idea]
summary: "The owner's unshaped idea, as he said it: replay the bounded, fully recorded tasks the drain has already run against several models from the same starting state, look at the results side by side, and only then work out a rubric."
---

# Model benchmarks from the drain's own recorded tasks

**Status:** 2026-11-17. An idea, not a design. Nothing here is decided and nothing is to be run
yet. The owner asked for it to go into the queue as a half-formed request, so that the intake
path described in [intake paths](./tillr-intake-paths.md) shapes it.

## The request, as the owner said it

Dictated on 2026-11-17 and reproduced as received. Only transcription slips are corrected.

> I've been wanting to do some sort of model benchmarks for a long time. I don't really know
> where this is going, but we now have lots of very bounded tasks where there's completely known
> inputs: Git tree, machine state, task, whatever, parameterized completely. And we run them and
> we have historical records of them. So this seems like the perfect opportunity to capture some
> of this stuff and rerun it. We have both design type things as well as more mechanical coding
> tasks, and I would love to run the exact same tasks isolated, so they appear to be starting
> from the exact same point, repository state, everything, and then run the same thing against
> multiple models and see how it differs.
>
> I think it's going to start off with: I don't know the rubric to evaluate these things yet. I
> would like to see the different implementations and judge them subjectively before we make
> some sort of objective rubric here. And then for the designs, I would love to give the two
> different design models the same sort of information, and then see the design docs side by
> side, what was generated, and then try to figure out some sort of rubric there as well.
>
> Not ready to run these just yet, but I want to start mining ideas here.

## What the request contains

These are restatements of the owner's words, not additions.

- **The material exists already.** Each drain run starts from a recorded commit with a recorded
  brief, and its result and spend are recorded.
- **Replay from the same start.** The same task is run in isolation, from the same repository
  state, against several models.
- **Two kinds of task.** Mechanical coding tasks, and design tasks where two design models get
  the same information.
- **Judge by eye first.** The owner reads the implementations, and the design documents side by
  side, before any rubric exists. The rubric comes from that reading.
- **Not yet.** This is for collecting ideas. Nothing is to be run until the owner says so.

## Related documents

The [planning node and model tiering](./tillr-planning-node-model-tiering.md) design asks which
model should do which class of work, and its open questions are still with the owner. A benchmark
of this kind would be evidence for that choice. Whether the two belong together is for triage and
the owner to say.

## The work this request becomes

### Add a design for replaying recorded drain tasks against several models

The owner wants to compare models on work this repository has really done, and the drain has been
quietly building the material for that. Every run it dispatches starts from a pinned commit, is
given a written brief and a list of paths it may change, and leaves behind its report, its change
and what it cost. That is very close to a benchmark task with known inputs. What nobody has
checked is how close: whether a finished run can be started again from the same point, for a
different model, without the second attempt seeing the first one's result or touching the live
queue.

The request is deliberately early. The owner does not have a rubric and does not want one invented
for him. He wants to read several models' implementations of the same task, and several design
models' documents for the same question, and let the rubric come out of that reading. So the next
step is a design, not a harness. The design has to say what is recorded today and what is missing,
how a replay would be kept apart from real work, which past tasks are worth replaying first, and
what the owner would be handed to read. Wherever the design needs a choice only the owner can make,
it asks him instead of choosing.

Nothing is replayed by this work, and no model is run for comparison. The owner said he is not
ready for that, and the design is what he will use to decide when he is.

**Outcomes:**

- Record what a finished drain run keeps, and what it would take to start it again from the same point.
- Propose how replays stay isolated and what the owner reads side by side, for coding and for design tasks.
- Ask the owner the numbered questions he must answer before any replay is run.

**Agent Details:**

- **Ref:** [The request, as the owner said it](#the-request-as-the-owner-said-it), and
  [planning node and model tiering](./tillr-planning-node-model-tiering.md) for the neighboring
  question of which model does which class of work.
- **Where:** this document only. Read, without changing them, a few finished lane directories
  under `.tillr/lanes/`, the store through `tillr item show` and the other read verbs, and the
  lane layout in `docs/design/tillr-muse-backend.md`.
- **Verify:** `uvx vantage-check docs/design/model-benchmarks-from-drain-records.md` passes, and
  `git diff --name-only` against the base names this document and nothing else.

<!-- tillr: item id="item-model-benchmarks-from-drain-records" seat="design" paths="docs/design/model-benchmarks-from-drain-records.md" blocked_by="" prose="2" -->

- **c1:** The document lists what a finished drain run keeps (starting commit, brief, allowed paths, report, change, spend, session log) and where each is kept, each entry read from a real finished run with the command and the date. It names every input a replay would need that is not kept or does not survive, such as fork checkouts, store contents or machine state.
- **c2:** The document proposes how a replay is isolated: where its worktree and output live, how it is kept from landing, from writing to the live queue and from seeing the original run's result, and how the model is varied while the brief and starting commit stay fixed. Each rejected alternative is named with its reason.
- **c3:** The document proposes a first shortlist of recorded tasks, with at least three mechanical coding tasks and three design tasks, and states the rule that selected them, including why each is still replayable.
- **c4:** The document describes what the owner is handed to read for a coding task and for a design task, so that several models' results for one task sit side by side with their cost and gate result, and no score is computed.
- **c5:** The document states how this work relates to the planning-node and model-tiering design: what evidence a replay could give that design, and whether the two should stay separate documents.
- **c6:** Every choice that belongs to the owner is a numbered question in this document with the options and a recommendation, and none is decided in the text. The questions cover at least which models to compare, how many tasks, the spending limit, and whether results are shown with the model's name hidden.
- **c7:** The change contains this document only. No task is replayed and no model is run for comparison, and the document says so in its status line.
