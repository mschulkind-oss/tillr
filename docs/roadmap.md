---
title: "Roadmap"
status: accepted
tags: [roadmap, tillr]
summary: "The top-level routing table: it points at the tillr roadmap, which owns the order and the detail, and keeps the numbered entries other documents cite."
---

# Roadmap

The work in this repository is tillr, and its order and detail live in
[the tillr roadmap](tillr/roadmap.md). This page is the pointer, and it keeps the numbered
entries that other documents cite. A link here routes priority, not permission to build or
delete.

## Tillr

The ordering and the detail live in [the tillr roadmap](tillr/roadmap.md); this section is the
pointer. Its next single thing is the owner's rulings on the four designs in review; the one build
that can start before that round closes is the drain's own model, entry 10.

9. **Tillr today: one supervised real drain** is done and retired: the loop
   made its first real landing on 2026-11-13
   (the acceptance record) and has landed many
   changes since. It keeps its number so that the entries below keep theirs.

10. [Replace the drain tick with a successor function](reference/tillr-drain-successor.md) — done
    and graduated to a system reference: the loop's own model, decided and unblocked: the clock is
    inside every identity the drain writes, and the batch is a barrier. It is the per-item
    transition the judge's verdicts and the graphs' atomic candidate commit both assume, and it is
    the first tillr build that can start before the four designs' round closes.

11. **Reshaping the board backlog for tillr** is done and retired: the archive dump is committed
    at `docs/archive/board-2026-11-12/` and the 56 open
    items were triaged in `tillr-seed-triage.md`. It keeps its
    number so that the entries below keep theirs.

12. [Observatory truthfulness, provenance, and shared primitives](reference/observatory-design.md) —
    done and graduated to a system reference: all six questions were ruled on 2026-11-13 and all
    seven seeded slices (`item-obs-cli-counts`, `item-obs-refusal`, `item-obs-state-glyph`,
    `item-obs-archive`, `item-obs-now-band`, `item-obs-pause` and `item-obs-doc-link`) landed,
    retiring the companion implementation plan on graduation while [the observatory redesign](reference/observatory-redesign.md)
    replaced the four-region layout. It keeps its number so that the entries below keep theirs.

13. **Cost display, when there is a phase for it** — iceboxed 2026-11-13. The store records
    spend per run; nothing shows it: not the header, not a queue row, not the item pane
    ([`OQ-OBD4`](reference/observatory-design.md#why-its-this-way)). Asked and deliberately
    deferred rather than answered, so the work is a phase of its own — including what a figure
    should cover (a run, an attempt, a night) and how it is formatted.

14. [Self-hosted tillr building, watchable in the observatory](design/tillr-selfhosted-build.md) —
    the shortest route the design found to a loop that runs the tree's own code. Two of its three
    questions were settled on 2026-11-15: long-lived processes restart on request, and the roll
    command rebuilds the front end. One is left: what becomes of `just pack-sync`
    ([`OQ-SH2`](design/tillr-selfhosted-build.md#OQ-SH2)). It sits above the backlog
    because Steps 1–2 are what make every later item's verification mean something.

15. [The idea pipeline: from owner sentence to landed commit](design/tillr-idea-pipeline.md) —
    the flow the owner asked for: an idea dumped in chat becomes a top-ranked `idea` item, spawns
    scouting, a design, and fenced builds. All four of its questions are settled as of
    2026-11-16; what it owes is a build order, which waits on the design of the observatory's
    add-an-idea action on [the tillr roadmap](tillr/roadmap.md).
