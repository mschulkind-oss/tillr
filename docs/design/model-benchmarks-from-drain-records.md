---
title: "Model benchmarks from the drain's own recorded tasks"
date: 2026-11-17
status: draft
stage: SKETCH
next: "Triage shapes this into an item block, with the questions the owner has to answer before anything is run."
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
