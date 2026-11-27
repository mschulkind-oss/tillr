---
title: "The queue mirrors the roadmap's outstanding designs: one item per design, and its companion plan holds the work"
status: in-review
stage: DESIGN
tags:
  - tillr
  - queue
  - sync
  - roadmap
  - icebox
  - implementation-plan
  - design
summary: "After a sync, every design document a mirrored roadmap routes that is not finished has exactly one item in the queue, so an empty queue means no design is outstanding. Following the owner's 2026-11-25 ruling (D-QM23) and 2026-11-27 rulings (OQ-QM2, OQ-QM3), Slice 1 is built first without waiting on the rest of this design: tillr item sync gives each roadmap-linked design with no item block one item (a planning item when accepted, a design item when open or holding open questions, and a blocked icebox item when parked), unparked zero-question designs dispatch immediately regardless of stage or status (only Icebox holds a linked design back), and accepted designs run planned-build@v1 (building single-commit designs and stamping BUILT, ending no_change and BUILT when HEAD already satisfies them, or holding multi-commit designs for Slice 2). Companion plans (<topic>-plan.md, OQ-QM1), reopening, piece handling, and retiring hidden <!-- tillr: item --> comments follow in Slices 2 to 5."
next: "Build Slices 2 to 5 (companion plans, reopening, piece handling, and HTML-comment removal); OQ-DT2 remains open with the owner for zero-question designs that no roadmap links."
depends-on:
  - tillr-design-to-planning-transition.md
  - ../tillr/decision-provenance-review.md#6-how-this-stops-happening
---

# The queue mirrors the roadmap's outstanding designs: one item per design, and its companion plan holds the work

**Status:** In review; Slice 1 is built (`tools/tillr/vantage/vantage.go`, `tools/tillr-item/item/sync_design.go`, `tools/tillr-drain/drain/plan.go`), while Slices 2 to 5 are not built (`rg -n 'waits-on-pieces' tools -g '*.go'` prints nothing). The owner's rulings are `D-QM1` to `D-QM3`, [OQ-QM1](#OQ-QM1), [OQ-QM2](#OQ-QM2), [OQ-QM3](#OQ-QM3), and `D-QM23` in [the ledger](#decision-ledger), together with [`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling) and [`OQ-PR21`](../tillr/decision-provenance-review.md#6-how-this-stops-happening). Under `D-QM23` and [`OQ-PR21`](../tillr/decision-provenance-review.md#6-how-this-stops-happening), [Slice 1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item) stops waiting on the whole design and leaves companion plans, reopening, piece handling, and HTML-comment removal to later slices; `D-QM4` to `D-QM8` and `D-QM10` to `D-QM22` are designer decisions that the owner may overturn.

> **In short.** Every unfinished design a mirrored roadmap routes gets one item in the queue, its state says what the design owes, and nothing about a question is copied into the store. **Slice 1** builds that roadmap import on its own: a mirrored roadmap's link to a design with no item block yields one item — a planning item when the design is accepted, a design item when it still has open questions, and a `blocked` (`icebox`) item when parked. Later slices put multi-commit build steps and pass conditions into the companion implementation plan (`<topic>-plan.md`) that humans never read, add reopening and piece waits, and delete hidden `<!-- tillr: item … -->` comments.

**Why it matters.** Today an item exists only where an agent wrote a hidden `<!-- tillr: item … -->` comment into a document, and `tillr item sync` scans every tracked Markdown file silently. A design the roadmap routes that holds no such comment has no item, so the queue can be empty while designs are outstanding. A built design keeps saying `DECIDED` until someone edits it by hand, because nothing moves the stage word. Meanwhile, putting step fences and pass conditions inside design documents mixes human-facing decisions with agent execution plumbing.

**The shape.** Five parts:
- a **mirror** rule in sync that creates, finishes and reopens one **design item** per routed design, and skips companion `-plan.md` files as designs of their own;
- a pure function that reads the design's stage role, its questions, its place on the roadmap and its pieces, and says what the design item owes;
- a **companion implementation plan** (`<topic>-plan.md` beside `<topic>.md`) that the designer seeds as `stage: SKETCH` with research and notes, and that the plan run completes as `stage: DECIDED` with structured `## Tasks` entries carrying dependencies, question holds and pass conditions in plain Markdown that sync reads directly;
- two runs the design item starts — a **design run** over the design and its plan seed, and a **plan run** that either writes the companion plan's tasks (which sync loads as pieces) or stamps both files `BUILT` when nothing remains unbuilt;
- a question hold that reaches only the design item and the pieces that name the question.

**Cost.** Syncing a mirrored roadmap creates queue items nobody filed by hand, replacing `design-without-item` warnings with real items in roadmap order (`blocked` for `icebox` when parked). Under [OQ-QM2](#OQ-QM2), every unparked zero-question design dispatches immediately regardless of stage or status, making `## Icebox` the only way to keep a linked design untouched; under [OQ-QM3](#OQ-QM3), Slice 1's accepted planning items run [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) to build single-commit designs (or end `no_change` and stamp `BUILT` when `HEAD` already satisfies them) and hold multi-commit designs for Slice 2. In Slice 5, the hidden `<!-- tillr: item … -->` comment, its scanner across `docs/`, its post-landing retirement commit and its supporting checks are deleted; work without a design enters through `tillr item request`.

**Start at [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item)** for the smallest sync slice and which recorded decisions it rests on, then [Section 4](#4-the-life-of-a-design-item-and-its-pieces) and [Section 7](#7-the-companion-implementation-plan) for the later companion-plan and piece slices.

**Needs your ruling:** None in this document ([OQ-QM1](#OQ-QM1), [OQ-QM2](#OQ-QM2), and [OQ-QM3](#OQ-QM3) are ruled in [the ledger](#decision-ledger)); 18 designer's decisions in [the ledger](#decision-ledger) — `D-QM4` to `D-QM8` and `D-QM10` to `D-QM22` — may be overturned by the owner; adjacent edges remain open in [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9) and [`OQ-DT2`](./tillr-design-to-planning-transition.md#OQ-DT2).

**Reads with:** [queue persistence](../reference/tillr-queue-persistence.md) (what this amends, with its rulings `D-RS1` to `D-RS5`; [Section 12](#12-what-this-supersedes-or-amends) lists the amendments owed), [the design-to-planning transition](./tillr-design-to-planning-transition.md) (whose owner ruling `R-DT1` moves zero-question designs straight to planning, and whose [`OQ-DT2`](./tillr-design-to-planning-transition.md#OQ-DT2) covers the existing zero-question backlog), [the decision-provenance review](../tillr/decision-provenance-review.md) (whose rulings on commit wording, the comment hold, prose rewriting, and [`OQ-PR21` / `OQ-PR22`](../tillr/decision-provenance-review.md#6-how-this-stops-happening) this design relies on), [the graph design](./tillr-graph-is-the-path.md) (every act is a node; a pure function adds the nodes that are needed), [planning and model tiering](./tillr-planning-node-model-tiering.md) (the plan seat this design's plan run uses), [early design landing](./tillr-early-design-landing.md) (how a changed design reaches the trunk ahead of its build), [the item module's guide](../../tools/tillr-item/README.md) and [the drain's guide](../../tools/tillr-drain/README.md) (what is built today).

---

## 1. The direction and what exists today

The owner's rulings set the direction ([Decision Ledger](#decision-ledger)):

- **The queue mirrors outstanding designs** (`D-QM1`). A sync creates an item for every design a roadmap routes that is still out for any reason. Some complete and some wait on open questions, and an empty queue means no design is outstanding. Open questions are not copied into a store.
- **Icebox entries are loaded too**, marked blocked for the Icebox (`D-QM2`).
- **tillr reads no markup of its own from documents** (`D-QM3`). A hidden comment that is silently loaded from every Markdown file is rejected.
- **Work definitions live in the companion implementation plan** ([OQ-QM1](#OQ-QM1)). The plan grows alongside the design, holds research and planning a human never needs to read, and its form may be adapted so tillr reads work from it.
- **Syncing a configured roadmap picks up linked designs directly, without waiting on the rest of this design** (`D-QM23`). On 2026-11-25, after adding a second roadmap to `[sync] roadmaps` in another repository and seeing `tillr item sync` create nothing from it — the roadmap linked designs, five had no item block, and each produced only a `design-without-item` warning (just as `tillr view waiting` in this repository lists ~44 `design-without-item` rows for designs [`docs/tillr/roadmap.md`](../tillr/roadmap.md) links) — the owner said: *"I thought the import should pick up any design docs linked and not much more complicated than that?"* Paired with [`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling) (2026-11-22: *"once a design has no more open questions, it should immediately move to the planning stage"*), that makes the roadmap-link import a standalone first slice ([Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item)) that does not wait on companion plans, reopening, or piece handling.

The `design-doc` and `implementation-plan` skills already make the companion-plan split for later slices. The design document is short, argues choices and trade-offs for a human reader, and stops at what and why; the companion implementation plan (`<topic>-plan.md` beside `<topic>.md`) holds the research, symbol maps, traps and ordered build tasks that agents write for agents. Adapting the companion plan's form gives tillr a tracked file from which to read pieces and pass conditions while keeping every design document clean and reading nothing from arbitrary Markdown files.

Standing rulings of the owner's bind this design throughout: nothing re-runs on a guess about what a change means; no attempt counts and no clocks that fail work; a held state names who moves it; the owner is reached only by a question written in a document; he sees "draining" or "needs you"; an item's fate and engineering detail are the agents' to decide.

**What sync does today:**

| Fact | Where it comes from |
| :--- | :--- |
| Only a hidden comment creates a document-backed item. Sync scans every tracked `.md` file under `docs/` for a comment opening with `<!-- tillr: item`, and a document without one yields nothing | `docshare.ReadTracked` in `tools/tillr-item/docshare/docshare.go` and `parseItemBlocks` in `tools/tillr-item/item/sync.go` |
| After each piece lands, the loop commits a second change, `chore: retire landed document block`, deleting the comment and its `- **cN:**` lines from the document | `RetireItemBlock` in `tools/tillr-item/item/retire.go`, called from `retireDocumentBlock` in `tools/tillr-drain/drain/retire.go` |
| A design the roadmap links that holds no such comment gets a warning row, not an item. The row holds a document path and the roadmap line that links it | `designWithoutItems` in `tools/tillr-item/item/design_without_item.go`; shown by `tillr view waiting` |
| A design counts as covered when it holds any comment at all, landed or not, so one landed step hides a design whose other work was never filed | `designWithoutItems`, same file |
| An open question holds every unfinished item whose comment sits in the same file, related or not, and no item in any other file | Sync sets an item's `design_doc` to the file holding its comment; `tools/tillr-drain/drain/plan.go` holds on that document's first unanswered question |
| A design linked under a roadmap's Icebox heading and holding no comment creates nothing in the store | `tools/tillr-item/item/sync_roadmap.go` and `D-QP5` in [queue persistence](../reference/tillr-queue-persistence.md#roadmap-order-and-parking) |
| Nobody moves a stage word. The loop reads a stage for one purpose, to retire a designer's session when its document is done | `VantageQuestions` in `tools/tillr/vantage/vantage.go` and `retireDesigners` in `tools/tillr-drain/drain/designer.go` |
| `tillr item reconcile` classifies every indexed document as a read-only report, treats any document with an open question as owing no item, and is never called by the loop | `tools/tillr-item/item/reconcile.go` and `tools/tillr-item/README.md` |

---

## 2. The words

| Word | Meaning | What it is not |
| :--- | :--- | :--- |
| **Sync** | `tillr item sync`: the one command that reads tracked roadmaps, routed designs and their companion plans, and brings the store's items into line with them. | Not the loop's planning pass, which reads the store and the live question index and decides what to start. |
| **Routes** | Vantage's word: a roadmap routes a document when it links it outside a `"What this page/file does not cover"` section. | Not "mentions": a document named in prose with no link is not routed. |
| **Stage role** | One of `open`, `ready`, `built`, `done`: what [`.vantage.toml`](../../.vantage.toml) says a document's `stage` word means (`SKETCH` and `DESIGN` are `open`; `DECIDED` is `ready`; `BUILT` is `built`; `CURRENT`, `GRADUATED` and `SUPERSEDED` are `done`). | Not the literal stage word. tillr reads roles so a repository can rename its words. |
| **Mirrored roadmap** *(coined here)* | A roadmap `tillr item sync` reads: `--roadmap` when given, else `[sync] roadmaps` in `.tillr.toml`, else discovered `docs/**/roadmap.md` on an unscoped sync (`D-QM5`, `D-QM23`). | Not a separate `mirror_roadmaps` setting. |
| **Design** | A tracked Markdown document under `docs/` that a mirrored roadmap routes, is not another roadmap or a paired companion plan (`*-plan.md`), and either carries a `stage` or lives under `docs/design/`. | Not a companion `-plan.md` file, a module README, a research note or another roadmap. |
| **Companion plan** | The file `<dir>/<stem>-plan.md` beside a design `<dir>/<stem>.md`. Humans do not read it; agents write research into it during design (`stage: SKETCH`) and structured build tasks once the design is decided (`stage: DECIDED`). | Not a design of its own, even if prose links to it: its work belongs to `<dir>/<stem>.md` (`D-QM16`). |
| **Outstanding** *(from the owner's "still out")* | A design whose stage role is `open` or `ready`, or that has no `stage`. | Not "has open questions": a decided, unbuilt design is outstanding too. |
| **Finished** | A design whose stage role is `built` or `done`, or that no mirrored roadmap routes any longer. | Not "all its pieces landed", which is an inference from the store and is never used alone. |
| **Design item** *(coined here)* | The one queue item (`item-design-<slug>`) that stands for an outstanding design across its whole life. | Not a build slice; it never edits code outside its design and companion plan. |
| **Piece** | One buildable task read from a decided companion plan's `## Tasks` section (`item-<plan-slug>-<task-id>`): an ordinary item in the store with its own dependencies and criteria, landing one commit. | Not a hidden comment in a design doc, and not an ephemeral in-memory sub-step. |
| **Request** | A standalone item filed straight into the store via `tillr item request` when work has no design document. | Not read from Markdown comments on a roadmap. |
| **Parked** | Linked under a roadmap heading that starts with `Icebox` (and nowhere on the roadmap's spine). | Not dropped, and not `not-started`. |

---

## 3. The invariant

After every whole sync that succeeds, for every mirrored roadmap:

1. **Every outstanding design has exactly one unfinished design item**, and that item names the design as its document.
2. **Every unfinished design item has an outstanding design.** A design whose stage role becomes `built` or `done`, or that leaves the mirrored roadmaps, has no unfinished design item.
3. **Once Slice 2 lands, every task in a `DECIDED` companion plan of an outstanding, unparked design has its piece in the store**, linked to that design item; a companion plan stamped `SKETCH` or `BUILT` contributes zero new pieces.
4. **So a queue with no unfinished item means no design is outstanding** on any mirrored roadmap, regardless of open questions.

The invariant is checkable from two inputs: Vantage's planning index over the tracked tree and the store's `items` table.

**What makes it hold:**

- **Deterministic IDs, no collisions.** A design item's ID is derived from its design path by stripping `docs/` and `.md`, lowercasing, turning every non-alphanumeric run into `-`, and prefixing `item-design-` (for example, `docs/design/tillr-storage.md` becomes `item-design-design-tillr-storage`, matching `tillr item reconcile`'s path-injective slug). Two paths that derive the same ID fail sync before any write.
- **Companion plans never double-count as designs (`D-QM16`).** A file ending in `-plan.md` whose sibling `<stem>.md` exists (or whose frontmatter `depends-on` names `<stem>.md` as its parent design) is paired with that design and never receives its own `item-design-*` row, even when a roadmap links to the plan in passing. A standalone `*-plan.md` with no parent design is treated on its own merits; when `<stem>.md` exists, `<stem>.md` is always the design.
- **Sync is the sole writer of design items and plan-derived pieces.** Sync creates, finishes and (from Slice 3 on) reopens design items, and (from Slice 2 on) imports or updates pieces from `DECIDED` companion plans, in one SQLite transaction. Sync never edits any Markdown file.
- **Unstaged means outstanding.** A routed design without a `stage:` key is outstanding, never skipped as finished; its first design run gives it a stage.
- **Fail-closed on index errors.** When `uvx vantage-check index --format json` fails or refuses, sync creates nothing and finishes nothing, records the error, and `tillr view` reports the queue state as unknown rather than empty ([Section 10](#10-edge-and-failure-behavior)).

**Which roadmaps mirror (`D-QM5`, `D-QM23`).** `tillr item sync` mirrors the roadmaps selected by `--roadmap` when passed, else `[sync] roadmaps` in `.tillr.toml` (this repository sets `roadmaps = ["docs/tillr/roadmap.md"]`), else discovered `docs/**/roadmap.md` on an unscoped sync — the same roadmap selection `ReadRoadmapDocuments` and `designWithoutItems` already use, with no second configuration key. Once companion plans arrive in Slice 2, companion `-plan.md` files carry `planning-actions: none` in frontmatter (`D-QM17`) so they never clutter the owner's Vantage planning page.

### 3.1. Slice 1: the smallest sync that turns a linked design into one item

*(Slicing from the filed operator request applying the owner's 2026-11-25 statement `D-QM23`; engineering mechanisms below are the designer's and may be overturned by the owner.)*

Slice 1 builds the owner's 2026-11-25 expectation first, on its own, without waiting on companion plans, reopening, piece handling, or HTML-comment deletion:

1. **Eligible designs.** During `tillr item sync`, walk each mirrored roadmap (`--roadmap`, else `[sync] roadmaps` in `.tillr.toml`, else discovered `docs/**/roadmap.md`) outside code fences and `"What this page/file does not cover"` sections. Each linked tracked Markdown design under `docs/` (excluding roadmaps and any `<dir>/<stem>-plan.md` paired with an existing `<dir>/<stem>.md`, and requiring either `docs/design/` or a `stage:` key) whose stage role is neither `built` nor `done` is checked against existing coverage:
   - If the design already holds an unfenced `<!-- tillr: item … -->` comment block, or another non-`landed` item in `.tillr/state.db` already names it as `design_doc`, Slice 1 leaves that coverage untouched and creates no second item.
   - Otherwise (the exact designs that previously emitted `design-without-item` warnings on the spine, plus uncovered parked designs), sync materializes **exactly one** item `item-design-<slug>` in `.tillr/state.db` (`design_doc = <topic>.md`, `body_ref = <topic>.md`, sync-declared so `CheckDocumentSharing` and `documentPrerequisites` never mark it `unshared_open` or route it to `triage-no-block`).
2. **State, seat, and order of that one item:**
   - **Parked link** (linked only under a parked heading such as `## Icebox`, and nowhere on the spine; whether `## Exploration` stays a parking heading is [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9)): `state = "blocked"` with `WaitingOn{Waiter: "owner", Reason: "icebox"}`, unranked (`rank = ""`), `seat = "plan"` when accepted with zero unanswered questions else `seat = "design"`. Moving the link onto the spine unparks `blocked` (`reason: icebox`) to `in-queue` in roadmap order; moving a spine link into `## Icebox` parks `in-queue` to `blocked` (`reason: icebox`). Documentless Icebox bullets create no item and are counted in sync's summary (`D-QM12`).
   - **Accepted design** (unparked, stage role `ready` / `DECIDED`, and zero unanswered questions in Vantage's live index on `<topic>.md` and any `#OQ-*` fragments in `<topic>.md`'s `depends-on`): one **planning item** (`state = "in-queue"`, `seat = "plan"`, ranked in roadmap order).
   - **Open or unstaged design, or design with open questions** (unparked, at least one unanswered question on `<topic>.md` or its `depends-on` `#OQ-*` fragments, or stage role `open` / unstaged): one **design item** (`state = "in-queue"`, `seat = "design"`, ranked in roadmap order). Whenever `<topic>.md` (or a `#OQ-*` fragment in its `depends-on`) has an unanswered question in Vantage's live index, the planner holds `item-design-<slug>` as `question-open` without storing questions in `.tillr/state.db`. When it has zero unanswered questions, the planner dispatches it immediately regardless of stage or status ([OQ-QM2](#OQ-QM2)).
3. **Updates, finishing, and retired warnings in Slice 1.** If an unjudged `item-design-<slug>` row is still open when `<topic>.md` moves between open-questions/`DESIGN` and accepted/`DECIDED`, the next sync updates `seat` (`"design"` vs `"plan"`). If `<topic>.md` reaches role `built` or `done` outside the item's own landing, sync moves an unfinished `item-design-<slug>` row to `resolved-elsewhere` (`--resolves-via doc:<topic>.md`); if no mirrored roadmap routes `<topic>.md` any longer, sync closes it (`closed`). Finished `item-design-<slug>` rows are left untouched in Slice 1 (reopening is Slice 3). `design-without-item` warnings and `design_without_item` rows are no longer emitted for mirrored roadmaps (`D-QM15`).

**Recorded decisions Slice 1 rests on** (all following from the owner's statements, so Slice 1 does not wait on a review of the later-slice rows):

- **Verbatim owner rulings:** `D-QM1` (2026-11-18, one item per outstanding design and live question reads), `D-QM2` (2026-11-18, Icebox entries load as `blocked` for `icebox`), `D-QM3` (2026-11-18, direction that items come from roadmap sync rather than hidden Markdown comments), `D-QM23` (2026-11-25, `[sync] roadmaps` picks up linked designs directly without waiting on more machinery), [OQ-QM2](#OQ-QM2) (2026-11-27, every unparked zero-question design dispatches regardless of stage or status; `## Icebox` is the only way to hold a linked design back), [OQ-QM3](#OQ-QM3) (2026-11-27, accepted planning items dispatch [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) to build single-commit designs and stamp `BUILT`, end `no_change` + `BUILT` when `HEAD` already satisfies them, or hold multi-commit designs with nothing committed for Slice 2), [`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling) (2026-11-22, zero-question designs move to planning automatically), and [`OQ-PR21`](../tillr/decision-provenance-review.md#6-how-this-stops-happening) (2026-11-19, engineering detail does not hold building while owner-facing choices become open questions).
- **Existing designer decisions (or Slice-1 halves thereof) that follow as engineering detail from those owner statements** *(each remains a designer's decision in [the ledger](#decision-ledger) that the owner may overturn)*: `D-QM4` (stage roles `open`/`ready`/unstaged vs `built`/`done`), `D-QM5` (amended to use `[sync] roadmaps` / `--roadmap` / discovered `docs/**/roadmap.md`), `D-QM6` *(deterministic `item-design-<slug>` ID only; reopening is Slice 3)*, `D-QM7` *(sync creates and finishes design items in `.tillr/state.db` without editing Markdown; companion-plan piece import and reopening are Slices 2–3)*, `D-QM10` *(live `question-open` hold on `<topic>.md` and `#OQ-*` `depends-on` fragments only; piece `Waits on questions` is Slice 2)*, `D-QM11` *(design-item `blocked: icebox` and unparking only; piece parking is Slice 2)*, `D-QM12` (documentless Icebox bullets), `D-QM14` *(design-item waiting, `why`, and sync output only; companion-plan counts and `waits-on-pieces` are Slices 2 and 4)*, `D-QM15` *(retiring `design-without-item` warnings on mirrored roadmaps only; deleting `reconcile` is Slice 5)*, and `D-QM16` *(skipping paired `<stem>-plan.md` files as designs only; frontmatter validation is Slice 2)*.

**Live drain dispatch for Slice 1 ([OQ-QM2](#OQ-QM2), [OQ-QM3](#OQ-QM3)):** The planner holds no unparked zero-question `item-design-*` row for unruled dispatch choices:
- **Zero-question open or unstaged design (`seat = "design"`, [OQ-QM2](#OQ-QM2)):** dispatches immediately whatever its `stage` (`SKETCH`, `DESIGN`, or unstaged) or `status` (`draft`, `in-review`). Parking the link only under `## Icebox` is the sole way to keep a linked design untouched; [`.vantage.toml`](../../.vantage.toml)'s `SKETCH` note no longer holds a design back.
- **Zero-question accepted design (`seat = "plan"`, [OQ-QM3](#OQ-QM3)):** dispatches [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) (`plan` -> `work` -> `verify`). A design that fits one commit is built and stamped `stage: BUILT`; a design whose work is already satisfied on `HEAD` ends `no_change` (skipping `work`) and is stamped `stage: BUILT`; when `plan` finds the design needs several commits, nothing is committed and the item holds (`needs-companion-plan-slice`) for Slice 2's companion-plan piece reader.

**Left to later slices that Slice 1 does not wait on:** companion plans and `## Tasks` piece import ([OQ-QM1](#OQ-QM1), `D-QM16`'s frontmatter check, `D-QM17` to `D-QM19` — Slice 2); reopening finished `item-design-<slug>` rows (`D-QM6`'s reopen half — Slice 3); piece-scoped question holds, `waits-on-pieces`, and two-run stage stamping (`D-QM8`, `D-QM10`'s piece half, `D-QM13`, `D-QM14`'s `waits-on-pieces` half, `D-QM20` — Slices 2 and 4); and migrating live `<!-- tillr: item … -->` comments and deleting the comment scanner and `reconcile` (`D-QM15`'s `reconcile` deletion, `D-QM21`, `D-QM22` — Slice 5).

---

## 4. The life of a design item, and its pieces

### 4.1. How one design item relates to the work in its companion plan

A design has **one design item** (`item-design-<slug>`) for its entire life, and **zero or more piece items** (`item-<plan-slug>-<task-id>`) read from its companion `<topic>-plan.md` once the design is decided and planned.

They do different jobs and never compete for the same edit:

1. **While the design is still being decided (`SKETCH`, `DESIGN`, or unstaged),** only the design item exists. No pieces exist in the store even if `<topic>-plan.md` already sits beside `<topic>.md` at `stage: SKETCH` holding research notes from design rounds.
2. **When the design reaches `stage: DECIDED` (`status: accepted`, zero unanswered questions),** the design item runs its **plan run** on the `plan` seat. That run inspects the codebase at `HEAD`, completes `<topic>-plan.md` at `stage: DECIDED` with ordered tasks under `## Tasks` (or finds nothing unbuilt and stamps `<topic>.md` `stage: BUILT`), and commits.
3. **When that commit lands, `tillr item sync` reads `<topic>-plan.md`** and materializes one piece item per task in `.tillr/state.db`, setting each piece's `design_doc` to `<topic>.md`, its `body_ref` to `<topic>-plan.md#<task-heading-slug>`, and its `from` / parent link to `item-design-<slug>`.
4. **While any piece of `<topic>.md` is unfinished,** the design item stays in the store and is **held waiting on its pieces** (`waits-on-pieces`), while the pieces themselves dispatch, gate, review and land as ordinary build items in their own worktrees.
5. **When every piece has reached a finished state (`landed` or `resolved-elsewhere`),** the hold on the design item clears and the design item becomes dispatchable for a **plan run** again. That verification plan run checks the tree against `<topic>.md`'s ledger and `<topic>-plan.md`'s tasks:
   - If every promised piece is built on `main`, it updates `<topic>.md`'s `Built` column and moves `<topic>.md` (and `<topic>-plan.md`, if present) to `stage: BUILT` with the date, commit SHA and `MEASURED:` / `UNMEASURED:` note. When that commit lands, sync sees role `built` and finishes the design item.
   - If the tree still owes work (for example, a task was split or a follow-up piece surfaced), it appends the new task(s) to `<topic>-plan.md`, sync loads the new piece(s), and the design item waits on them again.
   - If a piece was `closed` unbuilt, the plan run never re-files the closed task ID automatically; if only closed work remains unbuilt, it raises an objection to the `design` seat because `<topic>.md` now promises something that was declined.

```mermaid
flowchart TD
    R["Mirrored roadmap links <topic>.md"] --> S["tillr item sync"]
    S --> DI["Design item: item-design-<slug>"]
    DI -->|"Icebox"| P["blocked: icebox (Parked)"]
    DI -->|"Open OQ-*"| Q["held: question-open (Needs you)"]
    DI -->|"Role open / unstaged, 0 OQ"| DR["Design run (design seat)<br/>updates <topic>.md + <topic>-plan.md"]
    DR -->|"Adds OQ-* or moves to DECIDED"| S
    DI -->|"Role ready, 0 unfinished pieces"| PR["Plan run (plan seat)<br/>updates <topic>.md + <topic>-plan.md"]
    PR -->|"Writes DECIDED <topic>-plan.md tasks"| S
    S -->|"Reads ## Tasks from DECIDED plan"| PC["Piece items: item-<plan-slug>-t1..tN<br/>each delivers its task"]
    PC -->|"Piecesunfinished"| W["Design item held: waits-on-pieces"]
    PC -->|"All pieces landed"| PR
    PR -->|"Nothing unbuilt -> stamps stage: BUILT"| S
    S -->|"Role built"| F["Design item finished"]
```

### 4.2. The state table for a design item

One pure function computes what a design item is on each pass from four recorded inputs: the design's stage role, the design's unanswered questions (from Vantage's index), whether the mirrored roadmap parks the design, and the design's pieces in the store. First matching row wins:

| # | Recorded facts | The design item | Shown as | Who moves it |
| :--- | :--- | :--- | :--- | :--- |
| 1 | Role `built` or `done`, or no mirrored roadmap routes it | Finished by sync | Not in the queue | Nobody; nothing is owed |
| 2 | The roadmap parks it under `Icebox` | `blocked`, reason `icebox` | Parked | `owner`; move the entry out of the Icebox |
| 3 | The planning index cannot be read | Held, reason `questions-unknown` | Needs a cause | Whoever repairs the index read |
| 4 | At least one unanswered question on `<topic>.md` or in `<topic>.md`'s `depends-on` `#OQ-*` fragments | Held, reason `question-open`, naming every one | Needs you (or Blocked if all are 🔒) | `owner` (or upstream blocker); answer in the document |
| 5 | Role `open` or unstaged, zero unanswered questions | Dispatchable: **design run** ([OQ-QM2](#OQ-QM2)) | Draining | `drain` |
| 6 | Role `ready`, and ≥1 piece of `<topic>.md` is unfinished *(from Slice 2)* | Held, reason `waits-on-pieces` | Draining (waits on work) | `drain`, as the pieces finish |
| 7 | Role `ready`, zero unfinished pieces | Dispatchable: **plan run** (before Slice 2 lands, dispatches [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) per [OQ-QM3](#OQ-QM3)) | Draining | `drain` |

Three consequences follow:

- **Zero question state in `.tillr/state.db`.** Row 4 is evaluated live from `uvx vantage-check index --format json` on every pass. No `questions` table, no mirrored question rows, and no `<!-- tillr-owner-question -->` HTML tags exist.
- **Icebox outranks open questions.** Parking a design in `## Icebox` moves both the design item and (from Slice 2 on) any unfinished pieces of its plan to `blocked` with reason `icebox`, so a parked design stops asking the owner for rulings until he unparks it.
- **Every unparked zero-question design dispatches automatically (`R-DT1`, [OQ-QM2](#OQ-QM2)).** Once a design on a mirrored roadmap outside `## Icebox` has zero unanswered questions, row 5 dispatches a `design` run regardless of `stage` (`SKETCH`, `DESIGN`, or unstaged) or `status`, and row 7 moves an accepted (`DECIDED`) design to planning without anyone hand-authoring an item block; `## Icebox` is the only way to keep a linked design untouched.

### 4.3. The two runs of a design item

Both runs execute under the design item's ID. Their deliverables are `<dir>/<stem>.md` and its companion plan, not a mechanical two-file permission list (`D-QM13`). The judge checks the diff and changed paths against the assigned work.

- **The design run** (row 5, `design` seat): reads `<topic>.md` (and `<topic>-plan.md` if one already exists). As the `design-doc` and `implementation-plan` skills specify, any codebase findings, symbol names, file maps or task sketches that surface while arguing the design are saved into `<topic>-plan.md` at `stage: SKETCH` (`planning-actions: none`), keeping `<topic>.md` concise and human-readable. The run ends in one of three ways:
  1. `<topic>.md` has every owner choice settled, its frontmatter moves to `status: accepted` and `stage: DECIDED`, and `uvx vantage-check` passes on both files;
  2. `<topic>.md` gains one or more `OQ-*` questions with `question` directives for choices only the owner can make, putting the item in row 4 on the next pass; or
  3. an explicit obstacle naming the next actor.
- **The plan run** (row 7, `plan` seat): reads the accepted `<topic>.md`, any `SKETCH` or prior `DECIDED` `<topic>-plan.md`, the store's completed piece history for `<topic>.md`, and the current repository tree at `HEAD`. It ends in one of two ways:
  1. **Work remains unbuilt:** it writes or updates `<topic>-plan.md` at `status: accepted`, `stage: DECIDED`, `planning-actions: none`, stamped with the `HEAD` commit SHA it surveyed, containing the prose map/traps and the structured `## Tasks` entries specified in [Section 7](#7-the-companion-implementation-plan). On landing, sync imports the new tasks as pieces and the design item enters row 6 (`waits-on-pieces`).
  2. **Nothing remains unbuilt:** it updates `<topic>.md`'s Decision Ledger `Built` column from tree evidence, sets `stage: BUILT` on `<topic>.md` (and on `<topic>-plan.md` if one exists) with date, commit SHA and a `MEASURED:` or `UNMEASURED:` clause, and commits. On landing, sync finishes the design item (row 1).

### 4.4. Finishing and reopening a design item

| What sync observes | What sync does to `item-design-<slug>` |
| :--- | :--- |
| `<topic>.md` has role `built` or `done`, and the design item is unfinished | Moves the design item to `resolved-elsewhere` (`--resolves-via doc:<topic>.md`, actor `tillr-item/sync`): a stage change landed outside the item's own final run (or before the mirror existed) |
| The design item's own plan run landed the `stage: BUILT` commit | Nothing; normal landing settlement already moved the item to `landed` (with its landing receipt) |
| No mirrored roadmap routes `<topic>.md` any longer, or `<topic>.md` was deleted | Closes the design item (`closed`) and closes any unstarted pieces of `<topic>-plan.md` |
| `<topic>.md` is outstanding again (for example, someone moved `stage: BUILT` back to `DESIGN` or `DECIDED`) and `item-design-<slug>` is in a finished state *(Slice 3)* | Reopens `item-design-<slug>` to `in-queue` (or `blocked: icebox` if parked) with a sync receipt (`--by tillr-item/sync --reason "design outstanding again"`), ranked after existing ranked items (`D-QM6`) |

*Store note (`D-QM6`, deferred to Slice 3):* `tillr item reopen` and `SetStateWithReceipt` in `tools/tillr-item/item/lifecycle.go` and `state.go` already support reopening `closed` items and returning `landed` items with a receipt; Slice 3 extends `SetStateWithReceipt` so sync's receipt reopens any finished `item-design-*` row when its design returns to an outstanding role, avoiding `-v2` clone IDs. Slice 1 leaves finished `item-design-*` rows untouched.

---

## 5. A question holds only what names it

The comment reader holds every item in a file on any open question in that file, and nothing outside it. With pieces living in `<topic>-plan.md` and questions living in `<topic>.md` (or in an upstream design), file identity is the wrong join.

The rule (`D-QM10`):

- **A design item (`item-design-<slug>`) is held by every unanswered question in `<topic>.md`**, and by every unanswered question named as a `#OQ-*` fragment in `<topic>.md`'s frontmatter `depends-on` list.
- **A companion plan never carries owner questions.** `vantage-check` / sync refuses a `-plan.md` file that declares a `question` or `oq` directive (`plan-has-question`): if the planner hits a choice only the owner can make, it files an obstacle to the `design` seat, which writes the `OQ-*` question into `<topic>.md` and moves `<topic>.md` back to `stage: DESIGN`.
- **A piece is held only by the questions it explicitly names** in its task metadata (`Waits on questions:` in `<topic>-plan.md`, stored as `(doc_path, question_id)` references on the piece). A piece that names no question runs even if `<topic>.md` later gains an open question about a different slice, unless the owner parks the design in the Icebox or comments on `<topic>.md`, which hold that design's unstarted steps ([OQ-PR12](../tillr/decision-provenance-review.md#decision-ledger)).
- **A plan run waits for every question on `<topic>.md`.** While `<topic>.md` has any unanswered question, row 4 precedes row 7 (and `vantage-check`'s `planning/stage-disagrees` forbids `DECIDED` with open questions anyway).

Question references are resolved live against Vantage's index on each pass: an answered question in `## Open Questions` (`✅`) or compacted into `## Decision Ledger` holds nothing; a referenced ID absent from both holds the piece with `question-removed`.

---

## 6. The Icebox

**What changes (`D-QM2`, `D-QM11`, `D-QM12`).** A design linked only under a mirrored roadmap's `## Icebox` heading gets its design item like any other outstanding design, and sync puts that design item — together with (from Slice 2 on) any unfinished pieces from its companion plan — into state `blocked` with reason `icebox`. *(Today `parkedSections` in `tools/tillr-item/item/sync.go` matches both `Icebox` and `Exploration`; whether `Exploration` remains a parking heading or only `Icebox` acts is already before the owner in [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9).)*

- **Reverses two points of `D-QP5`:** parked designs with no pieces still materialize their `item-design-<slug>` row, and parked items use `blocked` (with waiter `owner` and reason `icebox`) rather than `not-started`.
- **Hold-rule row:** reason `icebox`, location `queue`, class `parked`, mover `owner; move "<title>" out of the Icebox in <roadmap>`. `parked` counts neither in `draining` nor in `needs you`; `tillr view waiting` shows its count separately (`parked N`).
- **Unparking:** when the owner moves the link out of `## Icebox` onto the roadmap's spine, the next whole sync moves `item-design-<slug>` (and any `blocked: icebox` pieces of its plan) back to `in-queue` and assigns a rank if unranked. Only the owner edits `## Icebox`; no agent may park or unpark work.
- **Finished designs in the Icebox stay finished.** Row 1 of [the state table](#42-the-state-table-for-a-design-item) precedes row 2, so a document whose stage role is `done` gets no open row.
- **Documentless Icebox bullets** create no item and are reported in sync's summary line (`icebox_without_doc: 1`) so nothing disappears silently.

---

## 7. The companion implementation plan

This section specifies the concrete form of `<topic>-plan.md` that follows from the owner's [OQ-QM1](#OQ-QM1) ruling (`D-QM16` to `D-QM20`).

### 7.1. Pairing, visibility and lifecycle

1. **Path convention.** For any design `<dir>/<stem>.md`, its companion implementation plan is `<dir>/<stem>-plan.md` (for example, `docs/design/tillr-queue-mirrors-designs.md` pairs with `docs/design/tillr-queue-mirrors-designs-plan.md`). Frontmatter of `<stem>-plan.md` carries `depends-on: [<stem>.md]`, `status: draft | in-review | accepted`, `stage: SKETCH | DECIDED | BUILT | GRADUATED | SUPERSEDED`, and `planning-actions: none`.
2. **Humans never need to read it.** `planning-actions: none` (see `uvx vantage-check style-guide`) keeps `<stem>-plan.md` out of Vantage's Ready, graduation and compaction lists. Roadmaps link to `<stem>.md`, never to `<stem>-plan.md`. Design documents never contain `<!-- tillr: item … -->` comments, file-fence lists, or `- **c1:**` criterion blocks; a design's own migration/rollout section, when it has one, stays high-level architectural sequencing when order is load-bearing for risk or compatibility, and omits it otherwise.
3. **Two authoring phases, matching the `implementation-plan` skill:**
   - **During design (`stage: SKETCH`):** while `<stem>.md` is in `SKETCH` or `DESIGN`, the design agent creates or appends to `<stem>-plan.md` (`stage: SKETCH`) whenever research, measurements, symbol locations, edge-case notes or candidate task boundaries come up. Sync ignores `SKETCH` plans completely — free-form notes cannot trigger findings or premature items.
   - **After design acceptance (`stage: DECIDED`):** once `<stem>.md` is `DECIDED` with zero unanswered questions, the design item's **plan run** (`plan` seat) surveys the tree at `HEAD`, writes the prose orientation sections (`Ground truth`, `Map`, `Reuse`, `Traps`, `Don't`) for build workers, and writes the `## Tasks` section in the canonical grammar below, stamping `stage: DECIDED`.
   - **Completion (`stage: BUILT`):** when all tasks have landed and the verification plan run stamps `<stem>.md` `stage: BUILT`, it stamps `<stem>-plan.md` `stage: BUILT` in the same commit. Sync reads `## Tasks` only from companion plans of routed designs (or standalone `-plan.md` files explicitly listed) whose `stage` role is `ready` (`DECIDED`).

### 7.2. Canonical `## Tasks` grammar in `<topic>-plan.md`

A task carries no path list: it describes the result and how to verify it. Sync admits a task without `Paths` and ignores one that is present.

Instead of hidden HTML comments anywhere in `docs/`, `<topic>-plan.md` expresses work as ordinary Markdown headings and a structured metadata list under `## Tasks` (`D-QM18`). Because humans never read `<topic>-plan.md`, its `## Tasks` section can be strict, unambiguous and validated by `tillr item sync` without hiding anything inside comments:

```markdown
---
title: "Queue mirrors designs — implementation plan"
status: accepted
stage: DECIDED
planning-actions: none
depends-on:
  - tillr-queue-mirrors-designs.md
summary: "Completed against the tree at <sha>: six tasks across tillr, tillr-item, tillr-drain and tillr-view."
---

# Queue mirrors designs — implementation plan

**Design:** [`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md)
**Tree stamped at:** `<sha>`

## Map and traps

- `tools/tillr/vantage/vantage.go` (`parseVantageIndex`) currently drops stage roles down to a boolean `Done`; expose the four-role enum (`open`, `ready`, `built`, `done`).
- Every edit under `tools/tillr*` must run `just pack-sync` in the same commit (`tools/tillr/packsync` / `tools/tillr/cmd/pack-sync-check`).

## Tasks

### Task t1: Expose four-valued stage roles in the shared Vantage reader

The shared Vantage reader collapses stages to `Done bool`, forcing `design_without_item.go` and `reconcile.go` to parse `.vantage.toml` or literal stage words on their own. Exposing the four roles in one place lets sync and the planner agree on every repository's vocabulary.

- **Seat:** `build`
- **After:** none
- **Verify:** `python3 tools/go-test-shards.py tools/tillr -k 6 && python3 tools/go-test-shards.py tools/tillr-item -k 6 && just pack-sync`
- **Criteria:**
  - **c1:** `vantage.Document` exposes `StageRole` in `{"", "open", "ready", "built", "done"}` from the index payload, and a repository that renames `DECIDED` in `.vantage.toml` yields role `ready`.
  - **c2:** No Go file under `tools/tillr*` compares literal stage strings (`"SKETCH"`, `"DESIGN"`, `"DECIDED"`, `"BUILT"`, `"CURRENT"`, `"GRADUATED"`, `"SUPERSEDED"`).

### Task t2: Mirror routed designs into design items during sync

Sync needs to materialize, finish and reopen `item-design-<slug>` rows for every outstanding design on a mirrored roadmap so an empty queue means no design is outstanding.

- **After:** `t1`
- **Workflow:** `planned-build@v1`
- **Criteria:**
  - **c1:** After `tillr item sync` on a fixture with one design per role plus one unstaged, one iceboxed and one with open questions, every outstanding design has one unfinished `item-design-<slug>` row and finished designs have none.
  - **c2:** Companion `*-plan.md` files paired with a `<stem>.md` design never receive their own `item-design-*` row even when linked from the roadmap.
  - **c3:** When the planning index is unreadable, sync creates and finishes zero design items and exits non-zero with the reader's error.
```

### 7.3. Exact field mapping and validation rules (`D-QM18`, `D-QM19`)

Under `## Tasks` in a `DECIDED` companion plan `<dir>/<stem>-plan.md`:

| Plan element | How `tillr item sync` reads and validates it |
| :--- | :--- |
| **Heading `### Task <local_id>: <Imperative title>`** | `<local_id>` matches `^[a-z0-9][a-z0-9-]*$` (unique within the plan, e.g. `t1`, `sync-mirror`). Derived store `item_id` is `item-<plan-slug>-<local_id>` where `<plan-slug>` is `<dir>/<stem>` stripped of `docs/` and `design/` noise and slugified (or an optional `- **ID:** item-...` key when carrying an existing item ID over verbatim). `<Imperative title>` is the item's `title` (imperative verb, ≤12 words, checked directly without a `prose="2"` shibboleth). |
| **Why paragraph(s) + worker notes** | Prose between the `### Task` heading and the metadata bullets (plus any `**Notes:**` / `**Preconditions:**` blocks after `- **Criteria:**`) becomes the piece's body context at `<dir>/<stem>-plan.md#<heading-slug>`, read by the build worker alongside `<dir>/<stem>.md`. |
| **`- **Paths:** <prefix>, ...`** | Not read. Ignore if present; do not validate it. |
| **`- **After:** <ref>, ...` or `none`** | Optional (defaults to `none`). Each `<ref>` is either a sibling `<local_id>` in the same plan (expanded to that sibling's `item_id`) or a full `item-...` ID in the store. Mapped to `blocked_by`. |
| **`- **Seat:** <seat>`** | Optional (defaults to `build`). Validated as a seat the backend policy file declares. |
| **`- **Workflow:** <name>@v<N>`** | Optional (defaults to the charter's default build workflow, e.g. `planned-build@v1` or `single-worker@v1`). Note how the two planning layers compose (`D-QM20`): `<topic>-plan.md` cuts a **design** into **pieces** (each one commit, one queue item); within a complex piece, `planned-build@v1`'s runtime `plan` node may further split that single commit's work across cheap `edit` / `test-writing` / `docs` sub-nodes (`build-pieces@v1`) on the same worktree. |
| **`- **Waits on questions:** <doc>#OQ-..., ...`** | Optional (defaults to none). Stored as question references for [Section 5](#5-a-question-holds-only-what-names-it). |
| **`- **Claims:** c2=direction, ...`** | Optional (defaults to `verification-only` for every criterion). Validated against `D-JL2`'s claim classes. |
| **`- **Judge:** agent \| mechanical`** / **`- **Express:** true`** | Optional overrides matching the item schema. |
| **`- **Verify:** <command>`** | Optional targeted verification command surfaced in the worker's brief. |
| **`- **Criteria:**` with nested `- **c<N>:** <text>`** | Required (`c1..cK`, 1 to 8 non-empty judgeable conditions). Mapped to `item_criteria`. |

**No post-landing file deletion commit (`D-QM19`).** When a piece lands:
- `.tillr/state.db` records the `landings` receipt, the `done` criterion verdicts, and `state = landed`.
- **The loop does NOT run `RetireItemBlock` or make a second `chore: retire landed document block` commit.** `<topic>-plan.md` stays untouched as each piece lands — eliminating one extra trunk commit per piece and the race between that commit and sibling lanes.
- On the same machine, sync sees `item-<plan-slug>-<local_id>` already `landed` in `.tillr/state.db` and leaves it finished. Once the final piece lands, the design item's verification plan run stamps `<topic>.md` and `<topic>-plan.md` `stage: BUILT` in one clean commit, after which a cold machine rebuilding `.tillr/state.db` from git skips both files because their stage role is `built`.
- A machine that loses `.tillr/state.db` mid-wave, after some pieces landed and before `<topic>-plan.md` is stamped `BUILT`, re-imports every task on its cold rebuild. Landed commits carry no item reference ([OQ-PR1](../tillr/decision-provenance-review.md#decision-ledger)), so the worker checks the tree against `c1..cN` and resolves a piece that is already present via `resolved-elsewhere` (`commit:<sha>`). No post-landing deletion commit is needed.

**Prose rewriting ([OQ-PR14](../tillr/decision-provenance-review.md#decision-ledger)).** Under this split:
- Human-read documents (`<topic>.md` and `roadmap.md`) carry **no** item blocks or build steps, so no cheap model ever edits wording inside a design document, and no `prose="2"` shibboleth comment exists anywhere.
- Companion plans (`<topic>-plan.md`) are written by the `plan` seat and checked directly by `tillr item sync` for imperative task titles and non-empty criteria; a malformed task in `<topic>-plan.md` refuses the plan run at the gate before it lands.
- Standalone requests (`tillr item request`) live only in `.tillr/state.db`, so any triage/prose shaping of a raw request happens on tillr's side (or by spawning a new `<topic>.md` design when the request needs design).

---

## 8. Retiring the hidden comments, and what is deleted

### 8.1. Fate of the existing `<!-- tillr: item … -->` comments in `docs/`

`rg '^\s*<!-- tillr: ?item' docs` lists the comments in `docs/`. Fenced specimens in [`docs/reference/tillr-queue-persistence.md`](../reference/tillr-queue-persistence.md) are documentation and stay until that document is amended. Each live, unfenced comment meets one of three fates in the cutover (`D-QM21`):

- **Its item has landed:** the comment is stripped.
- **Its item is unfinished and a design owns it:** the task moves into that design's companion plan, keeping its exact `item-*` ID through the `- **ID:**` key so store history and `blocked_by` edges stay intact.
- **Its item is unfinished and no design owns it:** it keeps its live definition in `.tillr/state.db` as a standalone request, and the comment is stripped.

A comment whose item only asks for a design to be cut into build items is closed: the mirrored design item replaces it. The comments are moved and stripped in the cutover build task, once the companion-plan reader is in place.

How work with **no design document** enters from then on (`D-QM21`):
- Small chores, bug fixes and ad-hoc requests with no design enter through `tillr item request` (or the observatory's idea box, which creates a design when the work needs one and a request when it does not).
- Roadmaps hold ordered links and short priority rationale, never build specs or HTML comments.

### 8.2. What is deleted (`D-QM22`)

Once sync reads mirrored roadmaps and `## Tasks` in companion `-plan.md` files and the comments above are moved, all of the following are deleted:

1. **The hidden HTML item-comment scanner across `docs/`:** `tools/tillr-item/docshare/` (`Start`, `AttributePattern`, `Declared`, `Sharing`, `ReadTracked` scanning every `docs/**/*.md` file) and `parseItemBlocks` in `tools/tillr-item/item/sync.go`. Replaced by a parser that reads **only** configured roadmaps and `## Tasks` inside `DECIDED` `*-plan.md` companion files, plus a gate check (in `tools/tillr/tillrdocs`, or a check declared under `tools/gate/checks/`) that **refuses** any unfenced `<!-- tillr:` comment in tracked Markdown so silent tags can never creep back in.
2. **Post-landing block-retirement commits:** `tools/tillr-item/item/retire.go` (`RetireItemBlock`, `retire` CLI face), the retirement step in `tools/tillr-drain/drain/retire.go`, and the retirement-commit exemptions in `tools/tillr-drain/drain/`.
3. **The hidden owner-question HTML comment:** `tools/tillr-item/item/owner_question.go` (`<!-- tillr-owner-question … -->`).
4. **The `prose="2"` shibboleth and document-rewriting item spawner:** `prose.go` and `prose_rewrite.go` in `tools/tillr-item/item/` (items that rewrite an author's Markdown block in place) and the `prose="2"` requirement. Task headings and criteria in `<topic>-plan.md` are checked structurally when the plan run commits `<topic>-plan.md`.
5. **Comment-block guards in request validation and design answers:** `validateRequest`'s `DefinitionBlockIDs` diff in `tools/tillr-drain/drain/request.go` (replaced by the repo-wide refusal of `<!-- tillr:` comments) and `blockSection` comment-marker lookup in `tools/tillr-drain/drain/design_answer.go` (replaced by companion-plan task section lookup by heading `### Task <id>:`).
6. **Comment-neutralizing shims in pack delivery and gate checks:** the step in `tools/tillr/packdocs` (`tools/tillr/cmd/pack-docs`) that makes `<!-- tillr: … -->` directives inert (pack snapshots also exclude `*-plan.md` files, since external workspaces read designs and READMEs, never internal build plans) and the `<!-- tillr:.* -->` allowance in `tools/gate/checks/roadmap_sections.py`.
7. **Warning and residue plumbing made obsolete by the mirror and companion plans:** `tools/tillr-item/item/design_without_item.go` and the `design_without_item` table for mirrored roadmaps; `tools/tillr-item/item/reconcile.go` (`tillr item reconcile`, whose partition contradicts the owner's open-question rule); and `stale_doc_blocks` / `unshared_open` counts in `tillr view status`.

---

## 9. What the owner sees

**In Vantage (the human surface).**
- Design documents (`docs/design/<topic>.md`) contain problem statements, choices, trade-offs, Decision Ledgers and `OQ-*` Open Questions — zero HTML item tags, zero path fences, zero `- **c1:**` checklists.
- Roadmaps (`docs/tillr/roadmap.md`) contain ordered links to designs and one-clause reasons for their order.
- Companion plans (`docs/design/<topic>-plan.md`) carry `planning-actions: none`, so they never appear in Vantage's Ready or graduation queues; a human never has to open one.

**In `tillr view waiting` and the observatory (the operational surface).**
One row per outstanding design item, with its pieces nested under it (or shown with `from: item-design-<slug>`). Warning rows with empty item IDs disappear for mirrored roadmaps:

```text
summary: owner-waiting 12; parked 2; next [drain=14]

 #  item_id                                 class          where  why
 1  item-design-design-tillr-diff-review    needs you      queue  question-open: 2 unanswered in docs/design/tillr-diff-review.md: OQ-DR1, OQ-DR2
 2  item-design-design-gate-rework          ready          queue  plan run: decided, companion plan not written yet
 3  item-design-design-tillr-landing-call   waits on work  queue  waits-on-pieces: 3 unfinished in its companion plan
 4  item-design-design-example             parked         queue  icebox: "Example" in docs/tillr/roadmap.md
```

**`tillr drain why <item>`.** For a design item, document facts precede loop-wide holds: `icebox`, then `question-open` / `questions-unknown`, then `waits-on-pieces`, then loop-wide holds such as `trunk-broken`. Movers always read `<who>; <act>` with `owner` spelled `owner`:

```text
question-open    owner; answer OQ-DR1, OQ-DR2 in docs/design/tillr-diff-review.md
icebox           owner; move "Example" out of the Icebox in docs/tillr/roadmap.md
waits-on-pieces  drain; finish item-<plan-slug>-t1, item-<plan-slug>-t2, item-<plan-slug>-t3
```

**Sync output (never silent).** Every `tillr item sync` prints what it read and changed: mirrored roadmaps, design items created/reopened/finished/parked, companion plans read (`DECIDED` count vs `SKETCH` skipped), pieces imported/updated, and any documentless Icebox entries.

**The three quiet states:**

```text
queue empty: every design docs/tillr/roadmap.md routes is finished (synced 5:54 PM ET). Nothing needs you.
next: link a new design from docs/tillr/roadmap.md.

nothing draining, nothing needs you; 2 designs parked in the Icebox of docs/tillr/roadmap.md.
next: move an entry out of the Icebox to start it.

queue state unknown: the last sync could not read the planning index (<reader error>).
next: tillr item sync
```

---

## 10. Edge and failure behavior

| Case | What happens |
| :--- | :--- |
| A design has a `<topic>-plan.md` at `stage: SKETCH` while `<topic>.md` is `DESIGN` | Sync creates/updates `item-design-<slug>` from `<topic>.md` and ignores the `SKETCH` plan completely; zero pieces are loaded |
| `<topic>-plan.md` is `DECIDED` while `<topic>.md` is moved back to `DESIGN` with a new `OQ-*` | The design item is held `question-open`; sync imports no *new* pieces while `<topic>.md` has role `open`, and any unstarted pieces wait if they name the question (or if `<topic>.md` is parked) |
| A roadmap links both `<topic>.md` and `<topic>-plan.md` | `<topic>-plan.md` is recognized as `<topic>.md`'s companion (`D-QM16`) and gets no `item-design-*` row of its own |
| `<topic>-plan.md` contains an `OQ-*` `question` directive | Refused by sync and `tools/tillr/tillrdocs` (`plan-has-question`): owner questions belong only in `<topic>.md`, where humans read them |
| A task in a `DECIDED` `<topic>-plan.md` has an unknown seat, cycle in `After:`, or zero criteria | Sync reports a structured finding (`plan-task-invalid`) naming the file and `### Task` heading and writes no pieces from that plan; the plan run's gate check runs `tillr item sync --dry-run` so malformed plans cannot land |
| The planning index cannot be read at sync | Sync creates, finishes and reopens nothing, reports the error, and leaves existing store rows untouched |
| Two syncs run concurrently | One SQLite write transaction + deterministic IDs; the second is a no-op |
| A person edits `<topic>.md`'s `stage` by hand in either direction | Honored at the next sync (finishing as `resolved-elsewhere` or reopening `item-design-<slug>`) |
| Someone adds a `<!-- tillr: item … -->` comment to any Markdown file after cutover | Refused at the gate and ignored by sync |

**Never:** sync never edits a Markdown file; the loop never makes a post-landing commit to delete tasks from `<topic>-plan.md`; humans are never asked to read or rule on `<topic>-plan.md`; questions are never copied into `.tillr/state.db`.

---

## 11. Rollout sequence (high-level)

Slice 1 stands on its own and waits on none of Slices 2 to 5 (`D-QM23`). Detailed file maps, function signatures and test names for later slices belong in the companion `docs/design/tillr-queue-mirrors-designs-plan.md`. The architectural order across the five slices is load-bearing for one reason: the roadmap mirror and companion-plan reader must land *before* existing HTML comments are stripped and their scanner is deleted, so active work in `.tillr/state.db` never drops during the transition.

1. **Slice 1 — Smallest roadmap-link sync ([Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item))** — expose four-valued stage roles (`open`, `ready`, `built`, `done`) from the shared Vantage index reader, mirror every uncovered outstanding design on `[sync] roadmaps` / `--roadmap` / discovered `docs/**/roadmap.md` (skipping paired `*-plan.md` files) into one `item-design-<slug>` item (`seat = "plan"` when accepted, `seat = "design"` when open or holding unanswered questions, and `blocked` with reason `icebox` when parked), keep live `question-open` holds on `<topic>.md` and `#OQ-*` `depends-on` fragments, finish `item-design-<slug>` rows whose design becomes `built`/`done` or unrouted, and retire `design-without-item` warnings for mirrored roadmaps while leaving existing `<!-- tillr: item … -->` blocks untouched. Under [OQ-QM2](#OQ-QM2) and [OQ-QM3](#OQ-QM3), unparked zero-question designs dispatch immediately regardless of stage or status, and accepted planning items dispatch [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) (building single-commit designs and stamping `BUILT`, ending `no_change` + `BUILT` when `HEAD` already satisfies them, or holding multi-commit designs with nothing committed for Slice 2).
2. **Slice 2 — Companion-plan `## Tasks` reader, piece-scoped question holds, and dry-run gate check** — read pieces from `DECIDED` `<topic>-plan.md` files (`D-QM16` to `D-QM19`) alongside the still-working comment reader, park companion-plan pieces under `blocked: icebox` when their design is parked, scope piece question holds to explicit `Waits on questions` (`D-QM10`), and turn off `RetireItemBlock` for plan-backed pieces.
3. **Slice 3 — Reopening finished design items** — extend `SetStateWithReceipt` so sync reopens a finished `item-design-<slug>` row with a receipt when `<topic>.md` returns to an outstanding stage role (`D-QM6`).
4. **Slice 4 — Two-run design/plan lifecycle and `waits-on-pieces`** — wire the companion-plan design run and verification plan run deliverables (`<topic>.md`, `<topic>-plan.md`), `waits-on-pieces` (`D-QM8`, `D-QM13`, `D-QM20`), and the full companion-plan `tillr view waiting` / `why` / `status` surfaces (`D-QM14`).
5. **Slice 5 — Move remaining live comments and delete the HTML comment machinery** — move active roadmap/design blocks into companion `-plan.md` files (preserving item IDs), strip landed leftover comments, delete `docshare`, `retire.go`, `owner_question.go`, `reconcile.go`, `design_without_item.go` and `prose="2"` rewriting, and add the gate check refusing `<!-- tillr:` comments in `docs/` (`D-QM15`, `D-QM21`, `D-QM22`).

---

## 12. What this supersedes or amends

As each slice lands, its build tasks update these documents to match:

- **[Queue persistence](../reference/tillr-queue-persistence.md), [`OQ-QP2`](../reference/tillr-queue-persistence.md#why-its-this-way), `D-QP2`, `D-QP3`, `D-QP5`, `D-QP8`, `D-QP9`, `D-QP10`, `D-QP11`, and [`D-QP13`](../reference/tillr-queue-persistence.md#scoped-roadmaps-import-filter-and-skipped-block-report-d-qp13).** In **Slice 1**, `D-QP5`, `D-QP10`, and [`D-QP13`](../reference/tillr-queue-persistence.md#scoped-roadmaps-import-filter-and-skipped-block-report-d-qp13) are amended so `[sync] roadmaps` mirrors uncovered outstanding designs into `item-design-<slug>` rows and loads parked Icebox designs as `blocked` (`reason: icebox`, `D-QM2`, `D-QM23`) while keeping [`D-QP13`](../reference/tillr-queue-persistence.md#scoped-roadmaps-import-filter-and-skipped-block-report-d-qp13)'s scoped HTML-block filter intact for documents that still carry `<!-- tillr: item … -->` blocks. In **Slices 2 to 5**, [`OQ-QP2`](../reference/tillr-queue-persistence.md#why-its-this-way) is overturned in code per [OQ-QM1](#OQ-QM1) (`## Tasks` in companion `<topic>-plan.md` files replace HTML item blocks), and `D-QP8` and `D-QP11` (`tillr item reconcile`) are retired and deleted (`D-QM15`, `D-QM22`).
- **[Queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way), `D-RS1` to `D-RS5`.** `D-RS1`, `D-RS2`, `D-RS4` and `D-RS5` (the HTML item-block grammar, where blocks are placed, and post-landing block deletion as the shared receipt) are superseded: companion `-plan.md` files carry the tasks, tasks stay in `<topic>-plan.md` without deletion commits, and `<topic>.md` + `<topic>-plan.md` moving to `stage: BUILT` is the shared completion mark. `D-RS3` (no `Tillr-Item:` line in a commit) stands, by the owner's ruling on [OQ-PR1](../tillr/decision-provenance-review.md#decision-ledger).
- **[Decision-provenance review](../tillr/decision-provenance-review.md), [OQ-PR14](../tillr/decision-provenance-review.md#decision-ledger).** Human-read documents carry no build steps or item blocks, so no cheap model rewrites wording inside a design document, and `prose="2"` is retired.
- **[Convergence](./tillr-convergence.md), `D-TC1` and `D-TC2`.** The cold-rebuild invariant ("delete `.tillr/`, run sync, the queue is the same queue") stands, backed by mirrored roadmaps and companion `-plan.md` files in place of HTML comments.
- **[Intake paths](./tillr-intake-paths.md).** The `prose="2"` door is retired; design-backed work enters through `<topic>.md` and `<topic>-plan.md`, and standalone work without a design enters through `tillr item request`.

---

## 13. Risks

| Risk | What limits it |
| :--- | :--- |
| Turning on the mirror makes every unparked, zero-question outstanding design on `docs/tillr/roadmap.md` dispatchable in roadmap order | They queue in roadmap order behind existing ranked work, and `## Icebox` is the owner's one-line lever to park any design he does not want worked yet |
| A plan run writes weak criteria in `<topic>-plan.md`, since humans do not read `<topic>-plan.md` | `tillr item sync` validates criteria before admitting pieces, the compliance officer and landing diff reviewer ([`tillr-diff-review.md`](./tillr-diff-review.md)) see every code diff against `<topic>.md` as well as `<topic>-plan.md`, and `<topic>-plan.md` is still in git for forensic inspection whenever something goes wrong |
| An agent puts an owner decision inside `<topic>-plan.md` where the owner never looks | Sync and the gate refuse any `question` / `oq` directive in `*-plan.md` (`plan-has-question`), and `planning-actions: none` hides `*-plan.md` from Ready; owner questions can only live in `<topic>.md` |
| Cold rebuild of `.tillr/state.db` mid-wave (some tasks in a `DECIDED` plan have landed, before `stage: BUILT`) re-imports every task | The verification or build check sees the landed tasks' criteria already satisfied on `HEAD` and resolves them via `resolved-elsewhere` (`commit:<sha>`) without re-editing code |

---

## 14. What this does not cover

- **Graduating a `BUILT` design into evergreen reference docs.** A `BUILT` design owes nothing to the build queue; graduation remains a separate doc-hygiene pass.
- **Cross-design build ordering beyond `depends-on: [<other>.md#OQ-*]` and roadmap rank.** Piece-level `After:` can name an item ID in another plan when needed; design-to-design build barriers without open questions are not added here.
- **What a landed commit's message says.** The owner ruled that separately: it carries no item reference ([OQ-PR1](../tillr/decision-provenance-review.md#decision-ledger)).

---

## Decision Ledger

Rows `D-QM1` to `D-QM3`, [OQ-QM1](#OQ-QM1), [OQ-QM2](#OQ-QM2), [OQ-QM3](#OQ-QM3), and `D-QM23` record the owner's own dated words. `D-QM9` is superseded by [`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling) and [OQ-QM2](#OQ-QM2). Every other row (`D-QM4` to `D-QM8` and `D-QM10` to `D-QM22`) is the designer's decision, and the owner may overturn any of them. Under `D-QM23` and [`OQ-PR21`](../tillr/decision-provenance-review.md#6-how-this-stops-happening), the rows marked **Slice 1** follow as engineering detail from the owner's statements and do not wait on a pre-build review of the whole ledger; every remaining row is left to Slices 2 to 5, which Slice 1 does not wait on.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-QM1 | **The owner's ruling** *(Slice 1 rests on this)*: "when we sync a roadmap into tillr, we create an item for every design doc that is still out for any reason … if the queue is empty, that means there are no design docs [outstanding], regardless of open questions." Open questions are read live and never mirrored into `.tillr/state.db`. | 2026-11-18 | [Section 3](#3-the-invariant), [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item) | Slice 1 (`tools/tillr-item/item/sync_design.go`) |
| D-QM2 | **The owner's ruling** *(Slice 1 rests on this)*: "Icebox items should be loaded too, marked blocked for icebox." | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 6](#6-the-icebox) | Slice 1 (`tools/tillr-item/item/sync_design.go`) |
| D-QM3 | **The owner's ruling** *(Slice 1 rests on this direction; scanner deleted in Slice 5)*: "tillr decided on its own markdown tags and is silently loading them everywhere? I don't want that." Silent repo-wide scanning of custom `<!-- tillr: item … -->` tags is rejected. | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 8](#8-retiring-the-hidden-comments-and-what-is-deleted) | Slice 1 import built; Slice 5 deletion unbuilt |
| <a id="OQ-QM1"></a>[`OQ-QM1`](#OQ-QM1) | **The owner's ruling** *(Slices 2 to 5; Slice 1 does not wait on this)*: Build planning, research and machine-readable work definitions belong in the companion implementation plan (`<topic>-plan.md`) that grows alongside `<topic>.md` and that humans never look at, keeping the design document free of implementation plumbing; the form of `<topic>-plan.md` may be adapted so tillr reads work from it. *"This is the idea of the design doc versus the implementation plan split. I think the implementation plan is intended to be a document that grows alongside the design doc that saves a research and stuff, but a human would never look at it. And it's the perfect place for something like this, we can change the form of that implementation plan … See what you can do with that."* | 2026-11-18 | [Section 7](#7-the-companion-implementation-plan), [Section 8](#8-retiring-the-hidden-comments-and-what-is-deleted) | — |
| D-QM23 | **The owner's ruling** *(Slice 1 rests on this)*: After adding a second roadmap to `[sync] roadmaps` in another repository and seeing `tillr item sync` create nothing from it (five linked designs had no item block, and each produced only a `design-without-item` warning), the owner said: *"I thought the import should pick up any design docs linked and not much more complicated than that?"* | 2026-11-25 | [Section 1](#1-the-direction-and-what-exists-today), [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 11](#11-rollout-sequence-high-level) | Slice 1 (`tools/tillr-item/item/sync_design.go`) |
| <a id="OQ-QM2"></a>[`OQ-QM2`](#OQ-QM2) | **The owner's ruling** *(Slice 1 rests on this)*: *"I'm not sure \" not accepted\" is a status I even want. if I don't want to build it, it shoudl be in the icebox, otherwise, if it has no OQs, it's ready to build."* Put to the owner as option C — every unparked zero-question design is dispatched regardless of stage or status, the roadmap's `## Icebox` is the only way to keep a linked design untouched, and the `SKETCH` comment in [`.vantage.toml`](../../.vantage.toml) no longer holds a design back — the owner answered *"yes"*. Agrees with [`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling). | 2026-11-27 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 4.2](#42-the-state-table-for-a-design-item) | Slice 1 (`tools/tillr-drain/drain/plan.go`, `.vantage.toml`) |
| <a id="OQ-QM3"></a>[`OQ-QM3`](#OQ-QM3) | **The owner's ruling** *(Slice 1 rests on this)*: On option A as written, the owner answered *"yes"*: dispatch [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) for Slice 1's accepted-design planning items; a design that fits one commit is built and stamped `BUILT` (or ends `no_change` and `BUILT` when `HEAD` already satisfies it); when the `plan` step finds the design needs several commits, nothing is committed and the item holds (`needs-companion-plan-slice`) for Slice 2's companion-plan piece reader. | 2026-11-27 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 4.2](#42-the-state-table-for-a-design-item) | Slice 1 (`tools/tillr-drain/drain/workflow_defaults.go`, `node_brief.go`, `workflow.go`, `workflow_evaluator.go`) |
| D-QM4 | **Designer's decision; the owner may overturn this.** *(Slice 1 rests on this; follows from the owner's statements in `D-QM1` and `D-QM23`.)* Outstanding is stage role `open` or `ready`, or no `stage`; finished is role `built` or `done`, or no longer routed by a mirrored roadmap. | 2026-11-18 | [Section 2](#2-the-words), [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item) | Slice 1 (`tools/tillr/vantage/vantage.go`, `tools/tillr-item/item/sync_design.go`) |
| D-QM5 | **Designer's decision; the owner may overturn this.** *(Slice 1 rests on this; amended 2026-11-25 to follow from the owner's statements in `D-QM1` and `D-QM23`.)* `tillr item sync` mirrors the roadmaps selected by `--roadmap` when given, else `[sync] roadmaps` in `.tillr.toml` (this repository sets `["docs/tillr/roadmap.md"]`), else discovered `docs/**/roadmap.md` on an unscoped sync; no separate `mirror_roadmaps` setting exists. | 2026-11-25 | [Section 3](#3-the-invariant), [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item) | Slice 1 (`tools/tillr-item/item/sync_design.go`) |
| D-QM6 | **Designer's decision; the owner may overturn this.** *(ID derivation in Slice 1, following from `D-QM1` and `D-QM23`; reopening deferred to Slice 3, which Slice 1 does not wait on.)* A design item's ID (`item-design-<slug>`) is derived from `<topic>.md`'s path, and a design keeps that single design item for life: in Slice 3, sync reopens a finished row with a receipt if the design becomes outstanding again, rather than cloning `-v2`. | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 4.4](#44-finishing-and-reopening-a-design-item) | Slice 1 ID derivation built; Slice 3 reopening unbuilt |
| D-QM7 | **Designer's decision; the owner may overturn this.** *(Creating and finishing design items with zero Markdown edits in Slice 1, following from `D-QM1`, `D-QM3`, and `D-QM23`; companion-plan piece import and reopening deferred to Slices 2 and 3.)* Sync alone creates, finishes and reopens design items and imports companion-plan pieces; sync never edits a Markdown file. | 2026-11-18 | [Section 3](#3-the-invariant), [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 4.4](#44-finishing-and-reopening-a-design-item) | Slice 1 create/finish built; Slices 2–3 unbuilt |
| D-QM8 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 4; Slice 1 does not wait on this.)* Stage words are moved by the design item's own runs: a design run moves `<topic>.md` to `DECIDED` (while seeding `<topic>-plan.md` at `SKETCH`), and a plan run stamps `<topic>.md` and `<topic>-plan.md` `BUILT` once all work is verified on `main`. | 2026-11-18 | [Section 4.3](#43-the-two-runs-of-a-design-item) | — |
| D-QM9 | **Superseded by [`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling) and [OQ-QM2](#OQ-QM2).** Every unparked zero-question design dispatches automatically regardless of stage or status ([OQ-QM2](#OQ-QM2)), and accepted (`DECIDED`) zero-question designs outside `## Icebox` move to planning automatically ([`R-DT1`](./tillr-design-to-planning-transition.md#11-the-2026-11-22-ruling)); `## Icebox` is the only way to keep a linked design untouched. | 2026-11-27 | [Section 4.2](#42-the-state-table-for-a-design-item), [OQ-QM2](#OQ-QM2) | Superseded by [OQ-QM2](#OQ-QM2) |
| D-QM10 | **Designer's decision; the owner may overturn this.** *(Design-item live question hold in Slice 1, following from `D-QM1` and `D-QM23`; piece-level `Waits on questions` and `-plan.md` check deferred to Slice 2.)* Unanswered questions in `<topic>.md` (and `#OQ-*` entries in `<topic>.md`'s `depends-on`) hold `item-design-<slug>`; pieces in `<topic>-plan.md` are held only by questions listed in their `Waits on questions` field; companion plans themselves may never contain `OQ-*` directives. | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 5](#5-a-question-holds-only-what-names-it) | Slice 1 design-item hold built; Slice 2 piece hold unbuilt |
| D-QM11 | **Designer's decision; the owner may overturn this.** *(Design-item Icebox `blocked` state and unparking in Slice 1, following from `D-QM2` and `D-QM23`; piece parking deferred to Slice 2.)* Parked designs and their unfinished pieces sit in state `blocked` with reason `icebox` and class `parked`, counted neither as draining nor as needing the owner. | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 6](#6-the-icebox) | Slice 1 design-item Icebox built; Slice 2 piece parking unbuilt |
| D-QM12 | **Designer's decision; the owner may overturn this.** *(Slice 1 rests on this; follows from `D-QM2` and `D-QM23`.)* An Icebox bullet that links no document creates no item and is surfaced in sync's summary line. | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 6](#6-the-icebox) | Slice 1 (`icebox_without_doc`) |
| D-QM13 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 4; Slice 1 does not wait on this.)* Both the design run and the plan run of `item-design-<slug>` deliver `<dir>/<stem>.md` and its plan. Neither run is confined to a two-file path list; the fixed per-run-kind criteria in tillr's configuration still apply. | 2026-11-18 | [Section 4.3](#43-the-two-runs-of-a-design-item) | — |
| D-QM14 | **Designer's decision; the owner may overturn this.** *(Design-item waiting, `why`, and roadmap sync output in Slice 1, following from `D-QM1`, `D-QM2`, and `D-QM23`; companion-plan counts and `waits-on-pieces` deferred to Slices 2 and 4.)* `tillr view waiting` shows one row per outstanding design, `tillr drain why` orders document holds (`icebox`, `question-open`, `waits-on-pieces`) ahead of loop-wide holds, movers read `<who>; <act>`, and `tillr item sync` prints every roadmap and companion plan it read. | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 9](#9-what-the-owner-sees) | Slice 1 design-item sync/holds built; Slices 2, 4 unbuilt |
| D-QM15 | **Designer's decision; the owner may overturn this.** *(Retiring `design-without-item` warnings on mirrored roadmaps in Slice 1, following from `D-QM23`; deleting `tillr item reconcile` and `design_without_item.go` deferred to Slice 5.)* `design_without_item` warnings (for mirrored roadmaps) and `tillr item reconcile` are retired and deleted. | 2026-11-18 | [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 8.2](#82-what-is-deleted-d-qm22), [Section 12](#12-what-this-supersedes-or-amends) | Slice 1 warning retirement built; Slice 5 deletion unbuilt |
| D-QM16 | **Designer's decision; the owner may overturn this.** *(Skipping paired `*-plan.md` files as designs in Slice 1, following from [OQ-QM1](#OQ-QM1) and `D-QM23`; frontmatter `depends-on` pairing validation deferred to Slice 2.)* `<dir>/<stem>-plan.md` pairs with `<dir>/<stem>.md` by filename convention + `depends-on: [<stem>.md]`; sync never creates a separate `item-design-*` row for a paired `-plan.md`, even if a roadmap links to it. | 2026-11-18 | [Section 3](#3-the-invariant), [Section 3.1](#31-slice-1-the-smallest-sync-that-turns-a-linked-design-into-one-item), [Section 7.1](#71-pairing-visibility-and-lifecycle) | Slice 1 paired-plan skip built; Slice 2 validation unbuilt |
| D-QM17 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 2; Slice 1 does not wait on this.)* Companion `-plan.md` files carry `planning-actions: none` in YAML frontmatter so Vantage keeps them out of Ready, graduation and compaction lists, and `tools/tillr/packdocs` omits `*-plan.md` from external workspace snapshots. | 2026-11-18 | [Section 7.1](#71-pairing-visibility-and-lifecycle), [Section 8.2](#82-what-is-deleted-d-qm22) | — |
| D-QM18 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 2; Slice 1 does not wait on this.)* Sync reads pieces only from `## Tasks` in companion plans whose `stage` role is `ready` (`DECIDED`), using plain Markdown `### Task <local_id>: <Title>` sections with structured metadata bullets (`After`, `Seat`, `Workflow`, `Waits on questions`, `Claims`, `Judge`, `Express`, `Verify`, `Criteria` -> `c1..cN`) and zero HTML comments. No `Paths` field is required; sync ignores one that is present. `SKETCH` and `BUILT` plans yield zero new pieces. | 2026-11-18 | [Section 7.2](#72-canonical--tasks-grammar-in-topic-planmd), [Section 7.3](#73-exact-field-mapping-and-validation-rules-d-qm18-d-qm19) | — |
| D-QM19 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 2; Slice 1 does not wait on this.)* Landing a piece makes no second `chore: retire landed document block` commit; tasks stay in `<topic>-plan.md`, `.tillr/state.db` tracks landed pieces during the build wave, and the final plan run stamps `<topic>.md` and `<topic>-plan.md` `stage: BUILT`. | 2026-11-18 | [Section 7.3](#73-exact-field-mapping-and-validation-rules-d-qm18-d-qm19) | — |
| D-QM20 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 4; Slice 1 does not wait on this.)* While any piece from `<topic>-plan.md` is unfinished, `item-design-<slug>` sits held as `waits-on-pieces`; when all pieces finish, `item-design-<slug>` runs its verification plan run on the `plan` seat; within a single piece, `planned-build@v1` remains available to split one commit's edits across cheaper sub-seats. | 2026-11-18 | [Section 4.1](#41-how-one-design-item-relates-to-the-work-in-its-companion-plan), [Section 7.3](#73-exact-field-mapping-and-validation-rules-d-qm18-d-qm19) | — |
| D-QM21 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 5; Slice 1 does not wait on this.)* At cutover, unlanded items in `docs/` move their task specs into their design's `<topic>-plan.md` (preserving existing `item-*` IDs via `- **ID:**`) or remain in `.tillr/state.db` as standalone requests; leftover landed comments and items that only ask for a design to be cut into build items are removed; future design-less work enters via `tillr item request`, never Markdown comments. | 2026-11-18 | [Section 8.1](#81-fate-of-the-existing----tillr-item-----comments-in-docs) | — |
| D-QM22 | **Designer's decision; the owner may overturn this.** *(Deferred to Slice 5; Slice 1 does not wait on this.)* Delete `tools/tillr-item/docshare/`, `parseItemBlocks`, `retire.go` (and the drain's post-land retirement step), `owner_question.go`, `prose="2"` shibboleth checking and in-document rewrite item spawning, `tools/tillr/packdocs` directive neutralization, and `roadmap_sections.py`'s `<!-- tillr:` allowance, and enforce a gate check refusing unfenced `<!-- tillr:` comments in `docs/`. | 2026-11-18 | [Section 8.2](#82-what-is-deleted-d-qm22) | — |

---

## Open Questions

None open in this document. [OQ-QM1](#OQ-QM1), [OQ-QM2](#OQ-QM2), and [OQ-QM3](#OQ-QM3) are compacted into the [Decision Ledger](#decision-ledger). Adjacent questions already open in sibling documents are not duplicated here: [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9) covers whether `## Exploration` remains a parking heading alongside `## Icebox`, and [`OQ-DT2`](./tillr-design-to-planning-transition.md#OQ-DT2) covers how rollout handles the existing backlog of zero-question designs that no roadmap links. Questions specific to companion plans, reopening, or piece handling wait for Slices 2 to 5.
