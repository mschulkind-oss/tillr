---
title: "Shared Documents Carry the Work; Private Queue State Stays Private"
status: accepted
stage: CURRENT
updated: 2026-11-15
tags:
  - tillr
  - queue
  - persistence
  - roadmap
  - design-ruling
summary: "Settles the boundary between shared Markdown documents and a private .tillr/ store: an HTML item-block directive that carries no state, rank or tombstone; the four-clause rule for which work gets a shared block; no item id in any commit, with the deleted block as the shared landing receipt; tillr item sync with its two residue counters; and the queue state that stays private."
next: "None here. Where build tasks live is governed by OQ-QM1 in tillr-queue-mirrors-designs.md."
depends-on:
  - ../reference/tillr-queue-persistence.md
  - momentum-principle.md
vantage:
  status-chip: true
---

# Shared Documents Carry the Work; Private Queue State Stays Private

> **Delivered copy.** This is `docs/design/queue-persistence-ruling.md` from this repository at commit `bf99bb8059` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/queue-persistence-ruling.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

> **Built.** `tillr item sync` reads item blocks from documents, and the lander writes no trailer.
>
> **Overruled in part.** The owner objected to the item block of `D-RS1` on 2026-11-18 and ruled [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1) in [the queue mirrors the roadmap's outstanding designs](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md): build tasks live in companion `<topic>-plan.md` files that humans never read, not in hidden item comments.

**Status:** `D-RS1` to `D-RS5` are a design agent's decisions of 2026-11-15, and the owner may overturn any of them. On commit messages the owner ruled on 2026-11-20 ([`OQ-PR1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR1)): a landed commit carries no reference to tillr, its items or its runs, which is what `D-RS3` decides.

**Needs your ruling:** nothing in this document.

---

## 1. Shared Item Block Shape

**Ruling (`D-RS1`):** The item block is [`OQ-QP2`](../reference/tillr-queue-persistence.md#why-its-this-way)'s **Option A with three rules**:

1. **Attribute name `blocked_by`, not `after`:** Matches `tools/tillr-item/item/apply.go`'s `blocked_by` field directly with zero vocabulary translation.
2. **Open work only — zero state, rank, or tombstone attributes:** `rank`, `state`, `closed`, and `resolved-by` are forbidden in the directive. When a slice lands or is dropped, its `<!-- inert copy of a tillr directive: item ... -->` comment is deleted from the document (and any architectural rejection becomes a `D-*` row in `## Decision Ledger`, never a dead item block).
3. **Multi-line attribute wrapping allowed; fenced code blocks ignored:** Whitespace and newlines may separate attributes inside `<!-- inert copy of a tillr directive: item ... -->` so multi-path fences stay hand-editable, and any scanner must skip fenced code blocks (` ``` ` / `~~~`).

### Canonical Block Grammar

```markdown
### <Title>
<!-- tillr: item
     id="<item_id>"
     paths="<prefix1>,<prefix2>"
     blocked_by="<item_id_1>,<item_id_2>" -->

- **c1:** <Judgeable criterion text.>
- **c2:** <Judgeable criterion text.>
```

*(Single-line `<!-- inert copy of a tillr directive: item id="..." paths="..." -->` is equivalent. Omit `blocked_by` when empty. For a standalone chore or defect with no design document, place the comment and `- **c1:**` bullets indented beneath `- **<Title>**` on the domain roadmap.)*

**An optional `seat` attribute.** A directive may also carry `seat="<seat>"`, naming one of the item model's seats (`design`, `build`, `review` or `scout`, the closed set in `tools/tillr-item/item/seat.go`). Omitted, the item asks for no seat; an unknown value is a finding like any other invalid attribute. Reason: a design item has no other way to ask for the design seat from a document.

**An optional `from` attribute.** A directive may carry
`from="<item id>"`, identifying the source of newly imported work. It must match
`^item-[a-z0-9-]+$`; a malformed value is a finding, but a source absent from the
store is not. On first import, a keyed source places the new item immediately after
it and any items the same mark already placed there, in block order. A landed source
still supplies its retained key. Existing items never move because of this mark.
Key choice and crowded-order respacing are shared with `tillr-item place`;
respacing preserves the other items' relative order. See
[the new-item placement rules](../reference/observatory-actions.md#first-import-placement).
The closed attribute set is `id`, `paths`, `blocked_by`, `seat`, `prose`, `workflow`,
`claims`, `judge` and `from`; none permits state, rank or tombstones.

**Exact field mapping (and nothing more):**
- `item_id`: `id` attribute (required; `^item-[a-z0-9-]+$`).
- `title`: text of the enclosing `### ` heading (or leading `**<Title>**` of the enclosing roadmap bullet).
- `allowed_paths`: comma-split, trimmed `paths` attribute (required, non-empty, repo-relative prefixes present on `main`, no globs).
- `blocked_by`: comma-split, trimmed `blocked_by` attribute (optional; defaults to `[]`).
- `seat`: the `seat` attribute (optional; defaults to no seat).
- `from`: first-import placement only, not an editable item-record field. Sync records
  the source as `placed_from` on the placement event so subsequent syncs can find
  earlier siblings even after their blocks are deleted.
- `criteria`: ordered `- **c<N>:** <text>` bullets in the same section up to the next heading or `<!-- inert copy of a tillr directive: item` directive (`criterion_id = "c<N>"`, `state = ""`; at least `c1` required).
- `body_ref` and `design_doc` are derived from the containing file path and heading slug, never written in the directive.

---

## 2. Which Work Gets a Shared Block

**Ruling (`D-RS2`):** `body_ref` is provenance, not an automatic home. A stored item gets a committed `<!-- inert copy of a tillr directive: item -->` block **if and only if all four clauses hold**:

1. **Agent-executable:** `c1` is a repository change a lane can verify, not `"Owner decision recorded in <doc>..."`.
2. **Accepted specification:** `design_doc` (or `body_ref` when `design_doc` is empty) carries `status: accepted`, or the item is a standalone code or gate defect with no unsettled design.
3. **Trunk-stageable fence:** every prefix in `allowed_paths` resolves on `main`.
4. **Unfrozen, non-conflicting criteria:** no criterion is frozen `not-done` on a specification conflict against a settled ruling.

**Where qualifying blocks live:** inside their `accepted` plan or design document when one exists; otherwise inline on [the tillr roadmap](../tillr/roadmap.md).

**Why the rest get no block:**

- **Owner rulings and host actions:** their shared truth already lives in Vantage `OQ-*` directives. Duplicating questions as item blocks violates `D-UT4` of [`board-utilities.md`](board-utilities.md#14-decision-ledger) and risks a lane mechanically stamping `c1` done.
- **Slices cut from an `in-review` draft:** pre-slicing an unaccepted draft is private staging; the design's prose already preserves the architecture.
- **Conflicted or container items:** committing broken criteria or an umbrella container turns local queue debris into a shared mandate.

---

## 3. Landing Receipts Without Commit Trailers

**Ruling (`D-RS3`):** Commits may not name Tillr items, and nothing scans commit trailers:

1. **No `Tillr-Item:` trailer:** `Trunk.Squash` in `tools/tillr-land/land/trunk.go` takes no item id and writes the head's message unchanged, and the worker brief in `tools/tillr-run/backends/brief-template.md` forbids an item id or trailer in the commit message.
2. **What the landing receipt is:**
   - **Privately (inside one developer's `.tillr/state.db`):** `items.state = 'landed'` and `landings.trunk_after` suppress re-dispatch during that developer's active epoch. A cold store on a fresh machine reconstructs **zero** `landed` rows.
   - **In the shared repository:** **absence of the open `<!-- inert copy of a tillr directive: item -->` block (or `stage: BUILT` / `CURRENT` on the document) is the receipt.** When a slice's `paths` already includes its owning `.md` file, the worker deletes the `<!-- inert copy of a tillr directive: item ... -->` comment in the landing commit itself (zero disappearance window). When sibling slices of one umbrella doc omit the `.md` file from `paths` so parallel lanes stay path-disjoint, the finished blocks are stripped (or the doc moves to `stage: BUILT`) in a short doc-reconcile commit once the wave settles.
3. **What a fresh machine loses, and why the loss is acceptable:**
   - **Lost permanently:** `landed` rows, item-to-SHA attribution, retry counts, gate and judge envelopes, and spend. `git log`, `git blame`, and the gate suites already carry every fact the shop needs about landed code.
   - **Bounded disappearance cost:** if a developer lands a parallel slice's code on `main` and vanishes before stripping its `<!-- inert copy of a tillr directive: item -->` comment from the shared plan doc, a fresh machine will sync that slice as open once, run against `HEAD` where `c1..cN` already pass, and retire the stale block in a doc-only commit. One idempotent check on rare mid-wave disappearance is strictly cheaper than polluting every repository commit with private queue IDs.

---

## 4. Sync and the Residue Counters

**Ruling (`D-RS4`):** `tillr item sync` reads the item blocks, and two counters keep private residue small and visible. Without the reader, `<!-- inert copy of a tillr directive: item -->` blocks are inert comments and new work goes straight to the store through `tillr item apply`.

`tillr item sync` (`tools/tillr-item/item/sync.go`) scans tracked documents outside fenced code blocks and materializes missing blocks: `not-started` when the block's document is linked only under a roadmap's `## Icebox` or `## Exploration` section, `in-queue` otherwise, never overwriting a stored `rank` or a `landed` state. `tillr-drain loop` runs it before its first plan. It prints two counters, which `tillr-view status` also shows:

- **`unshared_open: K (oldest Xm)`** — non-terminal store items absent from every document block: the private residue.
- **`stale_doc_blocks: M`** — document blocks whose `item_id` is already `landed` in the local `.tillr/state.db`, prompting block removal so the window between a landing and its block's deletion shrinks to zero at the end of each wave.

---

## 5. What Is Deliberately Left Private

**Ruling (`D-RS5`):** Two developers sharing this repository share documents, tests, and code — never a live queue. The following facts stay solely in `.tillr/` and are intentionally lost when a workspace disappears or moves machines:

| Private Surface | Why Leaving It Out of Git Is Right |
| :--- | :--- |
| **Personal order (stored `items.rank` keys)** | The order is private unless the repository sets `order.record`. With that opt-in, a plain text [order record](../reference/observatory-actions.md#the-order-record) carries the hand order across a rebuild; sync uses it only to place newly imported items, never to move items already held. Roadmaps still separate active work from `## Icebox` / `## Exploration`. |
| **In-flight execution (`lanes`, leases, `runs`, retries, `usage_cost_usd`)** | Ephemeral workbench state of one machine's processes; meaningless on a second developer's machine. |
| **Local `decisions` holds & frozen `not-done` flags** | Records why one developer paused a broken item locally; if the finding is architectural, it belongs in a design doc's prose or `Decision Ledger`, not as a serialized `.decisions.jsonl`. |
| **`landed` rows & item-to-commit joins** | Once code and updated docs are on `main`, the ephemeral `item-*` handle has done its job; git history documents the change in repository terms. |
| **Unpromoted scratch ideas & pre-cut `in-review` slices** | Ideas not yet distilled into a `SKETCH`/`DESIGN` doc (`unshared_open`) and slices of unaccepted designs are unfinished personal thoughts; momentum shrinks the window, and residue lost inside it is accepted. |

---

## 6. Decision Ledger

Every row is a **designer's decision of 2026-11-15; the owner may overturn it**. The word "Ruling" in the sections above is the design agent's.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-RS1` | The item block is [`OQ-QP2`](../reference/tillr-queue-persistence.md#why-its-this-way) Option A: `<!-- inert copy of a tillr directive: item id="..." paths="..." [blocked_by="..."] -->` under `### <Title>` plus `- **c1:**` bullets, multi-line attribute whitespace allowed, fenced code blocks ignored, and zero state/rank/tombstone attributes; finished or dropped blocks are deleted from the doc. **Rejected:** `after=` edge alias (unnecessary mismatch with `apply.go`), and `closed=` / `resolved-by=` Markdown tombstones (leaks permanent tillr state into prose). Overruled in part by the owner's [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1). | 2026-11-15 | [Section 1](#1-shared-item-block-shape) | ✅ (`tools/tillr-item/item/sync.go`) |
| `D-RS2` | A stored item gets a committed block only when it passes the four-clause rule: agent-executable, accepted specification, trunk-stageable fence, unfrozen criteria. **Rejected:** committing a block for every stored item via `body_ref` (duplicates open questions, freezes slices of `in-review` drafts, and enshrines conflicted items). | 2026-11-15 | [Section 2](#2-which-work-gets-a-shared-block) | — |
| `D-RS3` | No commit names an item: the lander stamps no `Tillr-Item:` trailer; `landed` is private epoch state (cold stores hold zero `landed` rows), and the shared landing receipt is deleting the `<!-- inert copy of a tillr directive: item -->` block (or advancing `stage: BUILT`). **Rejected:** `Tillr-Item:` trailers for cold rebuild. The owner ruled the same for commit messages on 2026-11-20 ([`OQ-PR1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR1)). | 2026-11-15 | [Section 3](#3-landing-receipts-without-commit-trailers) | ✅ (`Trunk.Squash` in `tools/tillr-land/land/trunk.go` takes no item id and writes the head's message unchanged; the worker brief in `tools/tillr-run/backends/brief-template.md` forbids an item id or trailer in the commit message) |
| `D-RS4` | `tillr item sync` reads item blocks and reports the `unshared_open` and `stale_doc_blocks` residue counters. **Rejected:** hand-edited blocks with no sync reader, which lets `tillr item apply` reopen the gap between the store and the documents. | 2026-11-15 | [Section 4](#4-sync-and-the-residue-counters) | ✅ (`tillr-item sync` in `tools/tillr-item/item/sync.go` with `unshared_open` and `stale_doc_blocks`, run by `tillr-drain loop` before its first plan; a whole writing sync saves the counters, their ids and time in one item-owned row, and `tillr-view status` reads that row directly (`--text` for the summary, default JSON or `--json` for the record; no saved sync is unknown, not zero)) |
| `D-RS5` | Leave the order private unless the repository sets `order.record` ([observatory actions reference](../reference/observatory-actions.md#the-order-record)); keep active runs/leases/spend, local `decisions`, `landed` rows, and unpromoted draft slices strictly private to `.tillr/`. **Rejected:** committing `.tillr/` JSON/JSONL snapshots or enforcing one repository-wide developer rank order (violates `D-QP1` and the human-shop contract). | 2026-11-15 | [Section 5](#5-what-is-deliberately-left-private) | — |