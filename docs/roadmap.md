---
title: "Roadmap"
status: accepted
tags: [roadmap, tillr]
summary: "The top-level routing table: it points at the tillr roadmap, which owns the order and the detail."
---

# Roadmap

The work in this repository is tillr, and its order and detail live in
[the tillr roadmap](tillr/roadmap.md). This page is the pointer. A link here routes priority,
not permission to build or delete.

## Tillr

1. [The drain pass and its successor function](reference/tillr-drain-successor.md) — built, and
   described by a system reference: the loop's own model. The clock is inside every identity the
   drain writes, and the batch is a barrier. It is the per-item transition the judge's verdicts
   and the graphs' atomic candidate commit both assume.

2. [Observatory truthfulness, provenance, and shared primitives](reference/observatory-design.md) —
   built, and described by a system reference; [the observatory redesign](reference/observatory-redesign.md)
   describes the linked-page layout.

3. **Cost display, when there is a phase for it** — iceboxed. The store records
   spend per run; nothing shows it: not the header, not a queue row, not the item pane
   ([`OQ-OBD4`](reference/observatory-design.md#why-its-this-way)). The question is deferred,
   not answered, so the work is a phase of its own, including what a figure should cover
   (a run, an attempt, a night) and how it is formatted.

4. [Self-hosted tillr building, watchable in the observatory](design/tillr-selfhosted-build.md) —
   the shortest route the design found to a loop that runs the tree's own code. Long-lived
   processes restart on request, and the roll command rebuilds the front end. One question is
   open: how the pack is delivered from Git
   ([`OQ-SH4`](design/tillr-selfhosted-build.md#OQ-SH4)). It sits above the backlog
   because Steps 1–2 are what make every later item's verification mean something.

5. [The idea pipeline: from owner sentence to landed commit](design/tillr-idea-pipeline.md) —
   an idea the owner types becomes an `idea` item that spawns scouting, a design, and fenced
   builds. It has no open question; item kinds and the lineage tree are not built, and their
   order is on [the tillr roadmap](tillr/roadmap.md).
