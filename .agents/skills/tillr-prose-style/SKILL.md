---
name: tillr-prose-style
description: Use when writing or rewriting tillr queue items, titles, descriptions, criteria, or related task prose so humans can read them easily without losing technical specificity for implementing agents.
---

# Prose style guide for tillr items

Write for two audiences in two separate layers: a **Human View** for scanning and prioritizing the queue, and **Agent Details** for the implementing agent. Never compress both into one dense sentence.

## 1. Two-layer structure (no redundancy)

- **Human View** (`Theme`, `Title`, `Description`, `Outcomes`):
  - Written for a human scanning the queue.
  - Use **component and conceptual names** (*the drain planner*, *the observatory board*, *the model policy*, *the repo gate*), never raw file paths, symbol names, or shell commands.
- **Agent Details** (`Ref`, `Where`, `Verify`):
  - Holds the exact technical anchors an implementing agent needs: design-doc references, file paths, flags, commands, and test assertions.
  - Do not repeat the Human View sentences word-for-word—just list the concrete locations and checks.

## 2. Title

- Start with an **imperative verb** from this list: *Add, Move, Route, Prevent, Isolate, Hold, Test*.
- The sync check allows at most 12 words; the shorter target below is preferred.
- Keep it **short (~5–9 words)** and **single-purpose**.
- A title only needs to read nicely, point in the right direction, and jog the owner's memory—it does not need to be 100% self-contained. Leave edge cases for the description.

## 3. Description (motivation first)

- Put at least one prose paragraph in the item's own section before its directive. A list, code fence, or comment is not a prose paragraph.

- **Spend words on the motivation.** Explain *why* the work matters and how the pieces interact in plain, conversational English. We do not pay by the word.
- **No compressed jargon or logic puzzles.** Never squeeze internal terms like *"in a single drain tick"* or *"per fenced path"* into terse shorthand. Add a few extra words to spell out what actually happens in real life (for example, *"when the loop launches parallel workers at the same time..."*).
- **Lead with a principle only when a real one applies.** Start with `Principle: ...` when an actual project or design principle motivates the work. Never invent generic engineering platitudes (such as *"tests should test real behavior"*).
- **Forward-flowing clarity.** Put `Condition -> Consequence` in natural left-to-right order so the reader never has to hold a long premise in memory waiting for the payoff.

## 4. Outcomes

- Keep the directive's acceptance criteria to at most eight; the high-level outcomes below are a separate human-facing summary.
- Keep `Outcomes` to **2–3 short, high-level bullets** describing what changes for the user or system. Save hyper-specific edge cases and test mechanics for **Agent Details**.
- Use **active voice and plain verbs** (*emit, write, delete, check, block, pass*) instead of static snapshots (*"X is Y, and Z is gone"*).
- No `: today ..., so ...` run-on sentences.
- No **inanimate speakers** (*"the snapshot says"*, *"the drain says"*). Say what is recorded and where.
- No **noun-stacking or dropped `that`s** in front of a passive verb (*"the bounds the encoding already carries are handed to..."*).

## Mark the item after applying this guide

Add `prose="2"` to the item's directive after writing it to this guide. This skill is the only place in the tree that should tell an author to add that attribute; other documents must not repeat the instruction. Sync reports missing or outdated marks and shape failures as warnings, never as a refusal to create or update the item.

## 5. Before and after

**Avoid (dense spec-ese):**
> The planner never dispatches two items whose fences overlap, so one tick cannot put two writers on one directory.

**Write (two-layer, motivation first):**
- **Theme:** Drain Planner
- **Title:** Prevent dispatching parallel workers with overlapping directory fences
- **Description:** When the drain loop launches parallel workers at the same time, each worker operates in its own separate worktree. Because the worktrees are physically separated, it can look safe to run tasks concurrently even if they touch the same areas of the codebase. But if two parallel workers edit files within the same directory, their changes will collide the moment they try to land on trunk. When multiple ready tasks want to touch the same directory, the drain planner must launch only one and hold the others back until that directory is free. Any task with an empty boundary or a repository-root boundary must be refused immediately rather than quietly locking the whole queue.
- **Outcomes:**
  - Dispatch at most one worker for any given directory boundary, and record which active task deferred the others.
  - Share the same boundary parser between the planner and the runtime escape checker so planning and enforcement never disagree.
  - Refuse tasks with empty boundaries or repository-root boundaries instead of serializing the queue.
- **Agent Details:**
  - **Ref:** `docs/design/tillr-day2.md#41-per-tick-pipeline-in-tillr-drain-loop`
  - **Where:** `tools/tillr-drain/` (planner dispatch loop, `.tillr/lanes/<lane>/fence`).
  - **Verify:** Tick test with 2 lanes and 3 ready items fencing `tools/tillr-view/`: assert only 1 dispatches and the other 2 name the blocking item ID; assert empty `allowed_paths` or root path (`/` or `.`) is rejected as a configuration error.
