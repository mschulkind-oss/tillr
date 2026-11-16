---
title: "Roadmap"
status: accepted
tags: [roadmap, host, sway, yolo, cleanup]
summary: "The routing table for the non-pi repository: host and desktop configuration, the configuration manager, yolo, and the cleanup decisions waiting on a ruling. Pi has its own roadmap at docs/pi/roadmap.md."
---

# Roadmap

**Last full reconciliation: 2026-10-12.**

**Scope:** everything the [pi roadmap](pi/roadmap.md) does not cover — the shell, compositor and
desktop configs, the configuration manager and its deployment, host administration and system packages,
the yolo jail, and this repo's non-pi tooling. Pi itself, its extensions and its forks are in
[`roadmap.md`](pi/roadmap.md).

Ordering basis: **a defect live on a host outranks work that unblocks other work, which outranks a
ruling that only closes a doc; ties break toward the smallest sitting.**

1. [Media playback dropouts on laptop](audio-dropouts-2026-09-27.md) — first because it is the
   one live host defect in this area, and the analysis is waiting on a low-I/O playback comparison
   before it can name or clear the cause.

2. [rcm deployment boundary QA and host cleanup](rcm-deployment-boundary/qa.md) — next because the
   repository side passed and only the per-machine host cleanup remains, which no other doc tracks.

3. [Sway window management](sway-window-management.md) — because the patched Sway package and
   `focus tab` are live while the sway-access service is still pending, and this doc has had no
   roadmap home.

4. [Stale inventory](stale-inventory.md) — because its five delete-or-keep questions are the only
   live decisions in this area, and each is a one-click ruling that has to precede any deletion.

5. [Waybar AI-quota chip display](research/claude-usage-monitoring.md) — after the inventory
   rulings because it is a single one-click display choice (Pango bars vs the compact text
   that ships today), with the doc's leaning already recorded and only the owner's ruling
   outstanding.

## Outstanding verification

6. [Deploy tok-stats on each host and measure the wait anchors](tok-stats/qa.md) — because
   the extension, the core and both adapters pass their source gates, but no host render has
   been checked and the title's `wait` column is unmeasured on Pi's OpenRouter Chat
   Completions, Responses and Codex routes; both are recorded in the
   [qa](tok-stats/qa.md) and [plan](tok-stats/plan.md) open items.

7. [Walk the complete host inventory for bedrock-spend](bedrock-spend/tasks.md) — because the
   native accounting engine is built and gated, while the source coverage that decides
   whether a total is complete has never been walked on a host, and invoice reconciliation
   waits on it. The open task is the last item in the [task list](bedrock-spend/tasks.md).

- [Converge each host on the Ironbar bar](design/status-bar-plan.md) — because **the Ironbar bar**
  is built and gated in the repository through `ac8715d16c` (the shared Arch package repository
  strategy, the usage-card module and fixed-height fork delta, the package patch generator, and
  the Ironbar configuration, stylesheet, user service and module check that retired Waybar), while
  host convergence and visual verification — publishing and installing the patched package
  (`just pkg-deploy ironbar`, `yay -Syu`), linking the new configuration (`rcup`), and confirming
  the bar renders with no Waybar process running — remain owner-run in
  [Task 5](design/status-bar-plan.md#task-5--converge-the-host-after-3-and-4-owner-run).

## External waits

- Two engineering principles are owed promotion in the skill pack kept outside this repository
  (host-config): "runtime evidence is human-reviewable" and "missing measurements are not zero".
  [The first observatory design](design/tillr-observatory.md#10-open-questions) settled that they
  become full reference cards; the edit is made there, not here.

8. [yolo fix requests](yolo-fix-requests.md) and
   [yolo patch-series requests](yolo-patch-series-requests.md) — now only the requests still
   open, after the yolo maintainer lands them: the config-overlay `remove` form (fix 1), a
   declared per-pack minimum yolo version (fix 3), version-stamping the broadcast clause and
   giving builds an identity (fix 5), an automatic window floor for reduced-window children
   (fix 9), the clean-replay export and the staleness signal (fix 10), and pi keeping its
   thinking levels on a declared Bedrock model row (patch-series 9). Everything else in both
   docs landed by `0.13.0+40.gab9cf2f90` and was pruned; the
   [briefing and isolation handoff](yolo-briefing-guidance-handoff.md) rides with what remains.
   The living tracker of every ask — recipient, state and evidence — is
   [open asks](open-asks.md), kept in step with both docs.

## Tillr

The ordering and the detail live in [the tillr roadmap](tillr/roadmap.md); this section is the
pointer. Its next single thing is the owner's rulings on the four designs in review; the one build
that can start before that round closes is the drain's own model, entry 10.

9. **Tillr today: one supervised real drain** is done and retired: the loop
   made its first real landing on 2026-11-13
   ([the acceptance record](design/tillr-drain-acceptance-2026-11-13.md)) and has landed many
   changes since. It keeps its number so that the entries below keep theirs.

10. [Replace the drain tick with a successor function](reference/tillr-drain-successor.md) — done
    and graduated to a system reference: the loop's own model, decided and unblocked: the clock is
    inside every identity the drain writes, and the batch is a barrier. It is the per-item
    transition the judge's verdicts and the graphs' atomic candidate commit both assume, and it is
    the first tillr build that can start before the four designs' round closes.

11. **Reshaping the board backlog for tillr** is done and retired: the archive dump is committed
    at [`docs/archive/board-2026-11-12/`](archive/board-2026-11-12/README.md) and the 56 open
    items were triaged in [`tillr-seed-triage.md`](design/tillr-seed-triage.md). It keeps its
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

## Repository tooling

Filed on 2026-11-16 at the owner's request ("file an item for adding a 'just format' if
appropriate. seems useful").

### Add a format recipe that the gate also checks

The owner's standing rule for every agent is to run `just format` before a commit whenever the
repository has that recipe. This repository has none: on 2026-11-16 `just format` failed with
"justfile does not contain recipe 'format'", and an agent committing Go test changes fell back to
running the Go formatter by hand on the two files it had touched. Each agent then decides on its
own what formatting means here, and nothing checks the result.

Nothing formats or checks formatting today. The recipe file, the git hooks, the gate's check
files and the gate itself name no formatter. The Go code is already formatted, so a formatter
for Go costs nothing to add and catches drift from the first commit. Python is different: the
common default formatter would rewrite 84 of the repository's 88 Python files, so including
Python means one deliberate reformatting commit, which is the owner's call and not this item's.
Shell, TypeScript and Lua have no formatter pinned for this repository's jail at all.

So this item adds the recipe for Go, the language where it is free today, together with a check
mode that the gate runs. The recipe and the gate then use one command and cannot disagree. Two
kinds of file must never be reformatted here. Some are copies of code kept elsewhere: the vendored
Go modules and the fork checkouts. Others are files that another repository writes into this one:
the house briefing and the skills synced in from the host-config repository, and the patch series.
Reformatting either kind would be overwritten, or would break a patch.



## Exploration

Wanted and kept, but not ranked — the owner asked to explore these further, and none is queued
above.

- **dotmod, as the rcm replacement.** The owner wants to explore [dotmod](dotmod-design.md) further,
  with its [module-system vision](module-system-vision.md); the keep ruling
  ([OQ-20](stale-inventory.md#decision-ledger)) records that it is wanted and live, not paused. The
  repo still deploys with rcm, and that migration gate is not abandonment.

## Icebox

Wanted, not now. An entry here is parked by the owner's call and not abandoned: nothing is wrong
with it and nothing about it is unsettled. Picking one up means giving it a home above, not
remembering that it was here.

## What this file does not cover

- **Pi.** Patches and forks of pi, the extensions this repo ships into it, the workflows harness,
  and the usage-capture contract are in the [pi roadmap](pi/roadmap.md).
- **Cost and usage tooling.** `tok-stats` and `bedrock-spend` are routed above, for the host
  verification neither can finish in the repository. `cost-levers` and `cloud-usage` are host
  deploy and accounting work with their own `next:` steps and no live question, so neither is
  routed here.
- **Unapproved implementation.** A link here routes priority, not permission to build or delete.
