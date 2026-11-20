---
title: "The Idea Pipeline: From Owner Sentence to Landed Commit"
status: accepted
stage: DECIDED
updated: 2026-11-19
tags:
  - tillr
  - pipeline
  - observatory
  - design
  - queue
summary: "Partly built design for turning an owner sentence into ranked work, routing open design questions, and drawing item lineage in the observatory: idea intake, the question-open planner hold, express lanes, and the idea's design-seat turn are built, answer return and derived-items.json are superseded, and item kinds, the typed derived_from edge, and the lineage tree view remain unbuilt."
next: "Build the two units of Section 8, written with no item directive until the owner rules on how documents declare work (OQ-QM1 and D-QM4..D-QM22 in tillr-queue-mirrors-designs.md): Slice 1b (item kinds and the derived_from edge), then Slice 3 (the lineage tree)."
---

# The Idea Pipeline: From Owner Sentence to Landed Commit

**Status:** 2026-11-13; reconciled against the tree on 2026-11-19 (earlier at `dcb76fe41` on
2026-11-18), and partly built. [Slice reconciliation](#slice-reconciliation-2026-11-18) under
Section 5 names each slice's state, and [Section 8](#8-remaining-work) writes out the two that
remain. Sections 1 to 5 preserve the 2026-11-13 design and 2026-11-15 workflow amendment, with
notes in place where later work built a different shape, superseded a mechanism, or left the
lineage slices unbuilt. In short:
- **Built:** clean intake (Slice 1a, `tillr-item idea` and the observatory's `POST /api/v1/ideas`),
  the numbered question hold (Slice 2), reserved express lanes (`[lanes] express = 2` in
  [`.tillr.toml`](../../.tillr.toml), [OQ-IP4](#61-settled-decisions)), the
  [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json) workflow template
  ([OQ-IP1](#61-settled-decisions)), and the idea's design-seat turn and pre-gate checks
  (Slice 5a).
- **Superseded:** answer return via `tillr item answer` / `tillr item mail` / preflight harvesting
  (Slice 4, `D-IP5`; replaced by [designer sessions](../reference/tillr-designer-sessions.md)) and
  `.tillr/loop/<run_id>/derived-items.json` (Slice 5b, `D-IP6`; replaced by `D-QP3` of
  [queue persistence](./tillr-queue-persistence.md#91-decision-ledger), with a further move to
  companion `<topic>-plan.md` files ruled in [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1)
  and not yet built).
- **Not built:** the `items.kind` column and the typed `derived_from` edge (Slice 1b), and the
  lineage tree on the Item page, Dependency Graph page and `tillr view status --tree` (Slice 3).

The first measurement on 2026-11-13 was taken against `/workspace/.tillr/state.db` (49 items:
33 `in-queue`, 13 `blocked`, 2 `landed`, 1 `closed`), the `tillr-*` CLI faces, and the served
`/api/v1/*` observatory wire.

> **In short** *(2026-11-13 pitch; see **Status** above for what is built, superseded and unbuilt on `HEAD`).* Record every owner idea immediately as an `idea` item in `.tillr/state.db` without dirtying `/workspace`, evolve it through `derived_from` edges into `design` items and fenced `build` items under the closed seven-state model, route open questions through Vantage back into designer revisions, and render that item-and-edge graph as a lineage tree in the observatory.

**Why it matters** *(2026-11-13 problem statement).* When this document was drafted on 2026-11-13, the owner's chat was outside the store: ideas lived in conversation memory until manually seeded, `item_edges` recorded only `blocked_by`, the drain ignored open questions in design docs, and `tillr item mail` was unread by both the planner and the board (the pre-redesign observatory queue screen, never the retired work board). Capturing intake, decomposition, and question turnarounds in the store and tracked documents eliminates coordinator retelling and keeps momentum continuous from raw thought to landed commit.

**The shape** *(as drafted, with current tree standing).* An additive migration on `tillr-item`'s tables (`items.kind` and typed `item_edges` distinguishing `blocked_by` from single-parent `derived_from` — **not built**; `items.resolves_via` landed separately as a single string column for terminal evidence receipts), a clean `.tillr/intake/` writer (`tillr-item idea` — **built**), an autonomous idea design turn and `scouted-design@v1` workflow (**built**, though saved ideas currently run as single-node `design`-seat turns unless `scouted-design@v1` is selected), `tillr item mail` answer return (**superseded** by [designer sessions](../reference/tillr-designer-sessions.md)), and a `LineageTree` view in `tillr-view` (**not built**).

**Cost** *(2026-11-13 estimate).* Additive table changes on `.tillr/state.db`, five single-lane slices across `tools/tillr*`, one checkpoint commit per design doc that has open questions so Vantage can serve it from `main`, and reserved express lanes outside the ordinary lane cap (`[lanes] express = 2` in [`.tillr.toml`](../../.tillr.toml); [OQ-IP4](#61-settled-decisions)).

**Start at [§5](#5-the-fastest-path-from-the-49-item-store)** for the original five-slice rollout and its [reconciliation against the tree](#slice-reconciliation-2026-11-18), and [§8](#8-remaining-work) for the two unbuilt lineage units; read [§1](#1-item-kinds-states-and-edges) and [§2](#2-the-pipeline-and-one-writer-rules) for the store invariants.

**Needs your ruling:** None in this document (every `OQ-IP*` row is closed in [§6.1](#61-settled-decisions); agent-settled decisions `D-IP1`, `D-IP2`, `D-IP4`, `D-IP5`, `D-IP6` and [`OQ-IP1`](#61-settled-decisions) remain overturnable by the owner and are catalogued in [the decision-provenance review](../tillr/decision-provenance-review.md)).

**Reads with:** [observatory-actions.md](../reference/observatory-actions.md#idea-intake-and-the-design-turn) (terminal and browser intake, verbatim text files, and owner-selected place; takes precedence over this pipeline for saving ideas), [observatory-redesign.md](../reference/observatory-redesign.md) (the built linked-page observatory that replaced [observatory-design.md](../reference/observatory-design.md)'s four-region board), [tillr-designer-sessions.md](../reference/tillr-designer-sessions.md) (Vantage review-comment delivery to resumed designer sessions, which superseded `tillr item answer`), [tillr-queue-persistence.md](./tillr-queue-persistence.md) and [tillr-queue-mirrors-designs.md](./tillr-queue-mirrors-designs.md) (tracked child definitions replacing `derived-items.json`, and the 2026-11-18 companion-plan ruling [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1)), [board-item-state-model.md](./board-item-state-model.md) (the ruled seven-state projector), [tillr-selfhosted-build.md](./tillr-selfhosted-build.md) (restart and build substrate), [tillr-day2.md](./tillr-day2.md) (loop failure classes), and [tillr-intake-paths.md](./tillr-intake-paths.md) (the other doors work enters by).

## 1. Item Kinds, States, and Edges

> **Tree state (2026-11-19):** **Not built as a schema column or edge kind** ([Slice 1b](#slice-reconciliation-2026-11-18), [Section 8](#add-item-kinds-and-a-typed-derived-from-edge-to-the-store)). `tools/tillr-item/item/schema.go` defines `items` with no `kind` column and `item_edges(item_id, blocked_by)` with no `target_id` or `kind` column (`items.resolves_via` exists as a single `TEXT` column for terminal evidence pointers). Today `tillr-view` marks `kind: "idea"` on the wire by checking `idea_intake` (`tools/tillr-view/serve.go`, `tools/tillr-view/idea_words.go`), an idea's own item lands once `<intake.dir>/<item_id>.txt` and at least one `from`-marked block commit (`tools/tillr-drain/drain/idea.go`), sync records `from` only as an immutable `placed_from` receipt on `item.update` (`tools/tillr-item/item/place.go`), and `question-open` holds are recorded as planner `hold` decisions in `dispatches` (`tools/tillr-drain/drain/plan.go`) rather than checkpointing `items.state` to `blocked` on `main`. The store has no global schema-6 counter; each package registers its own DDL via `RegisterDDL` and `RegisterColumn`.

Per [board-item-state-model.md](./board-item-state-model.md) ([§7](./board-item-state-model.md#7-the-structural-rule-one-projector-a-closed-enum)) and [observatory-design.md](../reference/observatory-design.md) ([state encoding](../reference/observatory-design.md#state-encoding-and-shared-refusal-contract)), **no new lifecycle status strings are added**. Every row projects into the closed seven-state enum (`not_started`, `in_queue`, `working`, `blocked`, `landed`, `resolved_elsewhere`, `closed`). Pipeline stages are distinguished instead by a new `items.kind` column (`'idea' | 'design' | 'build'`, defaulting existing rows to `'build'`).

| `items.kind` | Artifact Owned (`1:1`) | Entry (`in-queue`) | Active (`working`) & Hold (`blocked`) | Terminal State |
| :--- | :--- | :--- | :--- | :--- |
| **`idea`** | `.tillr/intake/<item_id>.txt` (immutable verbatim owner dump) | Saved at the owner-selected place, with no reserved rank prefix; `/workspace` stays git-clean. | Expediter + parallel scouts + scope/draft pass ([OQ-IP1](#61-settled-decisions)). Vague dumps write a 1-question stub doc and hold on `owner: 1 open question`. | `resolved-elsewhere` (`resolves_via` naming its derived `design` or `build` IDs), or `landed` when a trivial single-doc idea commits an accepted doc with 0 questions directly. |
| **`design`** | `docs/design/<slug>.md` (`allowed_paths` fenced to that doc) | Seeded next to its idea; owner answers make it express work without a reserved rank prefix. | Drafts or revises `docs/design/<slug>.md`. Landing a draft with `open_questions > 0` commits the doc to `main` (so Vantage serves it) and checkpoints the row to `blocked` (`waiting_on = "owner: N open questions"`). | `landed` **only** once `open_questions == 0` (`status: accepted`, `stage: DECIDED`) lands on `main` and its derived `build` (or sub-`design`) items are seeded; leaves active spine per [OQ-OBD5](../reference/observatory-design.md#why-its-this-way). |
| **`build`** | Disjoint code/doc `allowed_paths` + `item_criteria` | Seeded when its parent `design` reaches `0` open questions (or held via `blocked_by` if pre-seeded). | Worker edits inside a lane worktree; `tillr-gate` runs `just done`; failures move to `blocked` (`candidate-gate-fail`), never blind retry. | `landed` when `tillr-land pass` advances `refs/heads/main`; `resolved-elsewhere` (with pointer) or `closed` (with reason) if superseded or dropped. |

**Schema-6 edges (`item_edges`).** One column (`blocked_by`) cannot represent both execution order and lineage: sibling `build` rows chained sequentially via `blocked_by` would masquerade as parent-child, and cross-tree prerequisites would create multi-parent diamonds. Schema 6 migrates `item_edges(item_id, blocked_by)` to `item_edges(item_id, target_id, kind)` with `PRIMARY KEY (item_id, target_id, kind)` and `kind IN ('blocked_by', 'derived_from')` (existing rows migrate with `target_id = blocked_by, kind = 'blocked_by'`), plus a partial unique index `CREATE UNIQUE INDEX idx_item_derived_parent ON item_edges(item_id) WHERE kind = 'derived_from'`.
- **What a `derived_from` edge means:** `item_id` (child) was spawned from `target_id` (parent), enforcing a strict cycle-free forest of trees.
- **Who writes it:** Only `tillr-item` — in the current tree architecture (`D-QP3` of [queue persistence](./tillr-queue-persistence.md#91-decision-ledger) and [Section 8](#add-item-kinds-and-a-typed-derived-from-edge-to-the-store)), `tillr-item sync` when it first imports a child definition (the earlier `seed --derived-from` and post-land `derived-items.json` applier were retired with `seed-items.json` and `D-IP6`) — never inferred by `tillr-view` from ID prefixes or paths.
- **Does `tillr-drain` read it?** **No** for gating: `tillr-drain plan` blocks strictly on `kind = 'blocked_by'` plus `question-open` holds, and orders strictly by `items.rank` (`tillr-item` places child items adjacent to their source item at first import).

## 2. The Pipeline and One-Writer Rules

> **Tree state (2026-11-19):** **Partly built; manifest and mail steps superseded.** Stage 1 is built in `tools/tillr-item/item/idea.go` and `tools/tillr-view/serveidea.go`. Stages 2–3's [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json) and [`design-from-scouts@v1`](../../config/tillr/workflows/design-from-scouts@v1.json) workflows are built (`tools/tillr-drain/drain/workflow.go`, `TestScoutedDesignWorkflowLoop` in `tools/tillr-drain/drain/scouted_design_test.go`) and are the default for `design`-seat `tillr-item request` filings (`tools/tillr-item/item/request.go`), while `tillr-item idea` currently leaves `workflow` unset so saved ideas run as a single `design`-seat turn unless `workflows.default.design` is configured (`tools/tillr-drain/drain/idea.go`, `tools/tillr-drain/drain/workflow_defaults.go`). In the Stage 2–5 and single-writer tables below, both **unacked `tillr item mail` answers** and **`.tillr/loop/<run_id>/derived-items.json` post-land application** are **superseded historical draft mechanics**: child items are declared in tracked Markdown (`D-QP3` in [queue persistence](./tillr-queue-persistence.md#91-decision-ledger), moving to companion `<topic>-plan.md` files under [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1)), and Vantage review comments reach resumed designer sessions via [designer sessions](../reference/tillr-designer-sessions.md).

Scouting and designing inside one idea are nodes of one item's workflow, not separate items. A single-document design workflow uses [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json): its read-only `open` node on the design seat proposes scouts only when their work must be planned, fenced, or judged apart. Otherwise it proposes nothing, and `write` may use sub-agents of its own. A proposal replaces the unstarted `write` node with [`design-from-scouts@v1`](../../config/tillr/workflows/design-from-scouts@v1.json), whose read-only scout nodes each produce a findings file; its writing `join` node reads every file from its recorded producer. The [item-graph rule](../reference/tillr-item-graphs.md#the-words) separates workflow nodes from child sessions. Lineage between items remains for separate deliverables: several design documents or the build work derived from a design.

| Stage | Actor & Model Tier | Authority & Action | Failure or Ambiguity Disposition |
| :--- | :--- | :--- | :--- |
| **1. Intake** | Owner via `tillr-item idea`, or the observatory with writes enabled | Flushes `.tillr/intake/<id>.txt`, then creates the idea and intake record together at the selected place with zero pre-scouting. | A store failure leaves the words file and names it in the refusal; `/workspace` tracked files are untouched so `tillr-drain boundary` stays green. |
| **2. Open & Scout** | `tillr-run exec` (`open` on the design seat; any proposed scouts on the scout seat) | Reads intake + any unacked `tillr item mail` answers. May replace `write` with up to eight bounded read-only scouts and `join`; each scout writes its own `findings` output under its run directory. | No proposal leaves `write` in place. A scout that commits holds the item as `boundary-scope-escape`; a missing findings file holds it as `node-output-missing`. Neither releases `join`. |
| **3. Paired Design** | `tillr-run exec` (`write` or `write/join` on the design seat) | Consumes verbatim intake + every wired findings file + owner answers; writes `docs/design/<slug>.md` and `.tillr/loop/<run_id>/derived-items.json` (naming separate `design` or `build` deliverables with fences and criteria; [OQ-IP1](#61-settled-decisions)). | `vantage-check` or `citations` failure fails `tillr-gate` (`candidate-gate-fail`), records `refs/tillr/failed/<run_id>`, and leaves `main` and `.tillr/state.db` child rows uncreated. |
| **4. Build** | `tillr-run exec` (`worker` seat, fenced) | Runs in an isolated lane worktree against `allowed_paths` and `item_criteria` once `open_questions == 0` and all `blocked_by` targets are terminal. | Fence violation or `just done` failure marks item `blocked` with `gate.verdict` and `steps.jsonl` indexed in `EvidencePane`. |
| **5. Gate & Land** | `tillr-gate` -> `tillr-land pass` -> `tillr-drain` | Verifies clean commit via `just done`, fast-forwards `main` under the serial landing mutex, then atomically applies `derived-items.json`, `ack`s consumed mail, and sets item state (`blocked` checkpoint if `design` with `open_questions > 0`, else `landed` / `resolved-elsewhere`). | Merge conflict or landing check failure aborts before `main` moves and before `derived-items.json` is applied, preventing orphan child rows. |

| State Surface | Single Writer | Why |
| :--- | :--- | :--- |
| `/workspace/.tillr/state.db` (`items`, `item_edges`, `mail`) | `/workspace` coordinator & `tillr-drain` via `tillr-item` | Lanes never open the live store; child items are declared in `.tillr/loop/<run_id>/derived-items.json` and committed to SQLite only after `tillr-land pass` succeeds. |
| `.tillr/intake/<item_id>.txt` | `tillr-item idea` (write-once) | Preserves the owner's exact words outside tracked git paths so intake never trips `tillr-drain boundary` or `just done`. |
| `docs/design/<slug>.md` & code fences | Owning item's lane via `tillr-land pass` | Keeps `refs/heads/main` gated by `just done` (`vantage-check`, `citations`, `pack-sync`, tests) on every revision ([OQ-IP2](#61-settled-decisions)). |

## 3. The Questions Path

> **Tree state (2026-11-19):** **Point 1 and the planner hold of Point 2 are built; Points 3 and 4 are superseded.** Questions remain canonical in `docs/design/<slug>.md` and are read from Vantage's planning index (`tools/tillr/vantage/vantage.go`). When an item's `design_doc` has unanswered questions, `tillr-drain` records a `hold` decision with `reason: "question-open"` naming the first open question (`tools/tillr-drain/drain/plan.go`), without rewriting `items.state` to `blocked`, and the rebuilt observatory renders that hold on Home, Items, Item and Questions (`tools/tillr-view/waiting.go`, [docs/reference/observatory-redesign.md](../reference/observatory-redesign.md)). Point 3's `tillr item answer` / `tillr item mail` wrapper and Vantage preflight harvesting were never built ([OQ-IP2](#61-settled-decisions) was withdrawn because Vantage never writes repository files), and Point 4's mail-injected turnaround and `derived-items.json` emission were superseded by [designer sessions](../reference/tillr-designer-sessions.md) (`tools/tillr-drain/drain/comment_intake.go`, `comments.go`), express lanes (`[lanes] express = 2` in [`.tillr.toml`](../../.tillr.toml)), and tracked child definitions (`D-QP3` in [queue persistence](./tillr-queue-persistence.md#91-decision-ledger)). Steps 1–4 below are kept as the 2026-11-13 design record.

1. **Canonical source, zero duplication.** Per [OQ-OBD2](../reference/observatory-design.md#why-its-this-way) and [OQ-OBD6](../reference/observatory-design.md#why-its-this-way), questions and rulings live in `docs/design/<slug>.md`, never in a SQLite `questions` table. Extending `tools/tillr`'s question parser (`item-drain-question-hold`) to read numbered Vantage directives (`1. 💬 **[OQ-...]` + `<!-- vantage: question id=... -->`) and inspect each `**Answer:**` blockquote lets `tillr-drain` compute `open_questions` accurately.
2. **How the board shows `"blocked on you · 7 questions"` and links to Vantage.** When Pass 1 of a `design` item lands `docs/design/<slug>.md` with 7 unanswered questions, `tillr-drain` transitions the item to `blocked` with `waiting_on = "owner: 7 open questions"` and records a `hold` decision (`reason: "question-open"`). `QueueRow` renders `blocked · owner: 7 open questions` directly from `waiting_on` with zero per-row disk reads; selecting the row opens `DocumentPane` (`item-obs-doc-link`), showing `open_questions: 7` and linking straight to `<vantage_base>/#/docs/design/<slug>.md#open-questions`.
3. **How answers return without stalling `tillr-drain boundary` ([OQ-IP2](#61-settled-decisions)).** `tillr item mail` (`send`, `list`, `ack`) already exists in `tillr-item` and has no reader; `tillr item answer <id>` wraps it. When the owner drops answers in chat, the whale runs `tillr item answer <id> --body "<verbatim>"`, which writes a `mail` row, clears the `question-open` block, and sets `state = 'in-queue'` as express work at its selected place. When the owner types answers directly into Vantage on disk, preflight harvesting captures answer-only diffs on `docs/design/<slug>.md` into `tillr item answer` and restores `/workspace` git-clean so `boundary` passes.
4. **Same-minute turnaround to implementation.** On the next tick (within seconds on a reserved interactive lane, [OQ-IP4](#61-settled-decisions)), `tillr-run` injects unacked `mail` answers into the `designer` revision brief. The designer writes the owner's rulings into `**Answer:**` blockquotes and the Decision Ledger (`open_questions == 0`, `status: accepted`, `stage: DECIDED`), emits `derived-items.json` for the fenced `build` items in their idea's place, passes `just done`, lands on `main`, `ack`s the mail rows, marks the `design` item `landed`, and the next tick immediately dispatches the new `build` items.

## 4. The Tree View in the Observatory

> **Tree state (2026-11-19):** **Not built** ([Slice 3](#slice-reconciliation-2026-11-18), [Section 8](#show-an-ideas-lineage-tree-on-the-item-page-and-in-status)), and the host UI has changed. [The observatory redesign](../reference/observatory-redesign.md) replaced [observatory-design.md](../reference/observatory-design.md)'s four-region board and `QueueRail` (`?view=rank` / `?view=tree` in the second bullet below was withdrawn in [OQ-IP3](#61-settled-decisions)) with linked entity pages (`ItemRunPages.tsx`, `DependencyGraph.tsx` in `tools/tillr-view/web/src/components/`). Today `tools/tillr-view/serve.go` queries only `blocked_by` from `item_edges`, `tools/tillr-view/status.go` has no `--tree` flag, the Item page lists flat `idea.outcomes` derived from `placed_from` event receipts (`tools/tillr-view/idea_words.go`), and `pending_answers` went away with Slice 4. Slice 3 in [Section 8](#show-an-ideas-lineage-tree-on-the-item-page-and-in-status) carries `LineageTree` onto the Item page, `derived_from` edges onto `DependencyGraph.tsx`, and `--tree` onto `tillr view status`.

In the 2026-11-13 draft against [observatory-design.md](../reference/observatory-design.md), all four regions stayed intact with no fifth pane added:

- **`ItemPane` Overview (`LineageTree`, always rendered when `derived_from` or `derived_children` is non-empty):** Walks `derived_from` up to the root `idea` and draws the full descendant tree (`idea -> design* -> build*`) with connector guides, `kind` chip, three-way state (`colour + word + glyph`), `rank`, inline `waiting_on` + Vantage link on held `design` nodes, and a leaf completion counter (`2 / 5 build items landed`). Completed ancestors (`resolved elsewhere`, `landed`) stay visible inside `LineageTree` so provenance never disappears as work finishes; clicking any node navigates `#/item/<id>`.
- **`QueueRail` spine vs tree lens ([OQ-IP3](#61-settled-decisions)):** Default `?view=rank` preserves strict `items.rank` order and terminal archiving ([OQ-OBD5](../reference/observatory-design.md#why-its-this-way)), adding a muted 11 px `↳ <parent_id>` breadcrumb on derived rows. Toggling `?view=tree` (`t` key) groups active ideas as collapsible trees ordered by each tree's highest active descendant `rank`, retaining terminal parent and sibling nodes in context until **every** node in that tree is terminal, when the whole tree graduates to Archive.

| Wire / CLI Surface | Added Fields | Partial-Data & Truthfulness Contract ([observatory-design.md](../reference/observatory-design.md#truthfulness-and-provenance-contract)) |
| :--- | :--- | :--- |
| `/api/v1/snapshot` (`items[]`), `/api/v1/items/{id}`, `tillr view status --tree`, `tillr item show` | `kind` (`"idea" \| "design" \| "build"`), `derived_from` (`string \| null`), `derived_children` (`string[]`), `resolves_via` (`string[]`), `pending_answers` (`number`), `question_summary` (`{open, epistemic_state} \| null`) | **Standalone rows (48 existing):** `derived_from: null, derived_children: []` render with no empty tree chrome. **Missing parent ID:** renders an explicit `unknown parent: <id>` (`absent` badge) root stub, never dropped. **Live split (`working`, `derived_children: []`):** renders `deriving items (run live)`. **Missing/unreadable `design_doc`:** `question_summary.epistemic_state = "unreadable"` (defect badge), never `0 questions`. CLI and HTTP counts match ([OQ-OBD3](../reference/observatory-design.md#why-its-this-way)). |

## 5. The Fastest Path from the 49-Item Store

*Historical rollout table (drafted 2026-11-13 against the 49-item store; see [Slice reconciliation](#slice-reconciliation-2026-11-18) immediately below for what was built, superseded, or left to [Section 8](#8-remaining-work)).*

Per the owner's staged preference — **capture and browse in the observatory first while the whale drives paired design, then automate the drain** — Slices 1–3 required no autonomous `idea` runner and built atop [tillr-selfhosted-build.md](./tillr-selfhosted-build.md) Step 1–2 (source launchers + `item-obs-cli-counts` + `item-obs-refusal`) and `item-obs-doc-link` / `item-obs-archive` (ready items in the 2026-11-13 store).

| Slice (One Lane Fence) | 2026-11-13 Readiness | Proving Check (CLI + Gate) | Intended Outcome (2026-11-13 Draft) |
| :--- | :--- | :--- | :--- |
| **1. Schema-6 Lineage & Clean Intake** (`tools/tillr`, `tools/tillr-item`) | Drafted as ready (settled in [§1](#1-item-kinds-states-and-edges); **1a built, 1b not built**) | `printf probe \| tillr-item idea --store .tillr/state.db --to first` leaves `git status --porcelain` empty; `go test ./...` verifies `derived_from` cycle/multi-parent refusal and schema-5->6 migration. | Saved ideas appear at their selected place as `idea` rows; whale-run paired designs record real `idea -> design -> build` edges. |
| **2. Numbered Question Hold** (`tools/tillr`, `tools/tillr-drain`, `tools/tillr-view`) | Filed as `item-drain-question-hold` (**built**) | `tillr drain plan` holds the 6 un-ruled `in-queue` items as `question-open`; parser count equals `GET /api/v1/items/{id}/doc` `open_questions`. | Question-blocked rows show `blocked · owner: N open questions` on the row and click straight into Vantage via `DocumentPane`. |
| **3. Observatory `LineageTree` & `--tree` Parity** (`tools/tillr-view`) | Drafted as ready ([OQ-IP3](#61-settled-decisions) withdrew rail lens; **not built**) | `diff` between `tillr view status --tree` and `/api/v1/snapshot` tree counts exits `0`; frontend tests pin dangling-parent refusal and archive graduation. | Browsable milestone (unbuilt): clicking any item shows its full `idea -> design -> build` tree and progress from dump to landed. |
| **4. Instant Answer Return** (`tools/tillr-item`, `tools/tillr-drain` preflight) | Needed [OQ-IP2](#61-settled-decisions) (**superseded**) | `tillr item answer <id>` (and Vantage blockquote edit test) leaves `/workspace` clean, unblocks `<id>` as express work, and injects answers into staged `brief.md`. | Answering 7 questions moves the `design` item from `blocked` to top `in-queue` (`pending_answers: 1`) within one poll with zero manual re-ranking. |
| **5. Autonomous `idea` & `design` Drain Lanes** (`tools/tillr-run`, `tools/tillr-drain`) | Needed [OQ-IP1](#61-settled-decisions), [OQ-IP4](#61-settled-decisions) (**5a built, 5b superseded**) | Integration test seeds one `idea` under `tillr drain loop --lanes 2`, autonomously lands a design doc, applies `derived-items.json`, and lands a `build` child. | Unattended momentum from sentence to landed commit, with live scouting/designing runs in `NowBand` and `scout-dossier` in `EvidencePane`. |

#### Slice reconciliation, 2026-11-18

Read in the tree at `dcb76fe41` on 2026-11-18 and re-verified on `HEAD` on 2026-11-19. The store
has no single schema number to move to 6. Each module owns its own tables (`RegisterDDL`).
`item_edges` in `tools/tillr-item/item/schema.go` is still `(item_id, blocked_by)`. So "schema 6"
above means the additive change to the `tillr-item` tables that Slice 1b makes.

- **Slice 1a, clean intake: built.** [Observatory actions](../reference/observatory-actions.md#idea-intake-and-the-design-turn)
  replaced this slice's intake (`D-IP3`). `tillr-item idea` saves the words and the place
  (`tools/tillr-item/item/idea.go`, `TestIdeaUniqueIDAndTitle` in
  `tools/tillr-item/item/idea_test.go`), and the observatory saves an idea through it
  (`tools/tillr-view/serveidea.go`, `TestIdeasHTTPSavesWordsAndShowsIntake` in
  `tools/tillr-view/idea_internal_test.go`). The wire shows `kind: "idea"`, but the view derives
  it from the `idea_intake` table (`tools/tillr-view/serve.go`): no `items.kind` column exists.
- **Slice 1b, the `items.kind` column and a typed `derived_from` edge: not built.** Sync reads a
  block's `from` attribute ([observatory actions, outcomes](../reference/observatory-actions.md#staging-gate-refusals-and-outcomes))
  but keeps it only as a `placed_from` receipt on the item's update event
  (`tools/tillr-item/item/place.go`, whose comment says it is not this edge). The idea page
  finds an idea's outcomes through that receipt (`tools/tillr-view/idea_words.go`).
- **Slice 2, the numbered question hold: built.** The `question-open` hold and its Vantage index
  reader are in `tools/tillr-drain/drain/plan.go`, `tools/tillr-drain/drain/schema.go` and
  `tools/tillr/vantage/vantage.go`, and `tools/tillr-view/waiting.go` surfaces the held question.
- **Slice 3, the lineage tree and `--tree`: not built.** It needs Slice 1b's edge. The queue
  rail lens of [OQ-IP3](#61-settled-decisions) is withdrawn, so the tree belongs on the item
  page and the dependency graph page of [the observatory reference](../reference/observatory-redesign.md),
  and `--tree` belongs on `tillr view status` (`tools/tillr-view/status.go`).
- **Slice 4, instant answer return: superseded.** [OQ-IP2](#61-settled-decisions) was
  withdrawn. An owner's answer reaches a design as a review comment, which the loop delivers to
  the document's last designer session
  ([designer sessions](../reference/tillr-designer-sessions.md)). No `tillr item answer` face is wanted.
- **Slice 5a, autonomous idea and design runs: built.** [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json)
  and [`design-from-scouts@v1`](../../config/tillr/workflows/design-from-scouts@v1.json) are
  built (`TestScoutedDesignWorkflowLoop` in `tools/tillr-drain/drain/scouted_design_test.go`) and
  default for `design`-seat requests (`tools/tillr-item/item/request.go`), reserved express lanes
  are live (`[lanes] express = 2` in [`.tillr.toml`](../../.tillr.toml), `tools/tillr-drain/drain/express_test.go`),
  and saved ideas stage on the `design` seat (currently as a single-node run unless a default
  design workflow is configured) and pass pre-gate candidate checks (`words-missing`,
  `words-altered`, `idea-no-outcome` in `tools/tillr-drain/drain/idea.go`,
  `TestIdeaStageCarriesWordsGuideAndInstructions` and `TestIdeaCandidateChecksBeforeGate` in
  `tools/tillr-drain/drain/idea_test.go`).
- **Slice 5b, `derived-items.json`: superseded.** `D-QP3` of
  [queue persistence](./tillr-queue-persistence.md#91-decision-ledger) replaced it: a child
  item exists only in tracked Markdown (`from` names its idea), and on 2026-11-18
  [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1) ruled that build tasks move from hidden
  comments into companion `<topic>-plan.md` files (not yet built). Nothing reads
  `derived-items.json`.

**Historical accounting of the 2026-11-13 store's 48 non-idea items** *(snapshot at the time of drafting; `seed-items.json` has since been retired):*
- **Covered directly as substrate or absorbed (14 `in-queue`):** The **7 `item-obs-*` items** (`ranks` `p`–`v`, ready today, supplying `DocumentPane`'s Vantage link, `Archive`, `NowBand`, `Pause`, state glyphs, refusal, and CLI counts), **`item-drain-question-hold`** (absorbed as Slice 2), and the **6 `in-queue` items with un-ruled design questions** (`item-tillr-stop-reason`, `item-trace-correlation`, and four siblings, which Slice 2 converts into visible Vantage question holds).
- **Covered by the questions & decomposition mechanics (11 of the 13 `blocked`):** The **ten open-question ruling items** and **three seed rows** (overlapping to 11 rows among the 13 owner-blocked items) gain Vantage click-through, `tillr item answer` unblocking, and automated `derived-items.json` emission in place of manual seed cutting.
- **Not covered / unchanged in scope (23 items):** The **18 remaining dispatchable `in-queue` items** (`item-pipe-real-registry`, `item-lane-branch-scope`, `item-drain-2a`, `item-or-routing`, and sibling engineering rows — these burn unchanged as standalone `kind = 'build'` rows), **2 owner-blocked external scope items** (the Claude-whale counterpart and pi-roadmap host-render row), and the **3 already-terminal items** (`2 landed`, `1 closed`, retained in Archive).

## 6. Decisions and Open Questions

### 6.1 Settled Decisions

| ID | Ruling | Why |
| :--- | :--- | :--- |
| `D-IP1` | Keep the closed seven-state item model; add `items.kind` (`idea`, `design`, `build`, default `build`). *(Not built: `items` has no `kind` column yet; see Slice 1b in [Section 8](#8-remaining-work).)* | Preserves [board-item-state-model.md](./board-item-state-model.md)'s single-projector guarantee while distinguishing intake, design, and code stages. |
| `D-IP2` | Migrate `item_edges` to `(item_id, target_id, kind)` (`blocked_by` \| `derived_from`) with a single-parent partial unique index on `derived_from`; `tillr-drain` gates only on `blocked_by`. *(Not built: `item_edges` is still `(item_id, blocked_by)` in `tools/tillr-item/item/schema.go`; see Slice 1b in [Section 8](#8-remaining-work).)* | Separates strict tree genealogy from DAG scheduling dependencies in one event-logged table. |
| `D-IP3` | Original intake ruling: Markdown words files and a reserved rank prefix. Replaced by [observatory-actions](../reference/observatory-actions.md#idea-intake-and-the-design-turn): `.txt` words files, terminal and browser saving, and owner-selected place; the `00..` prefix ends. *(Built in `tools/tillr-item/item/idea.go` and `tools/tillr-view/serveidea.go`.)* | Keeps the original ruling visible without prescribing the superseded intake mechanism. |
| `D-IP4` | Each `design` item owns one `docs/design/<slug>.md`; draft commits with `open_questions > 0` land on `main` and hold the item until `open_questions == 0`. *(Partly built in a different shape: the planner holds `in-queue` items with `reason: "question-open"` from Vantage's live index in `tools/tillr-drain/drain/plan.go` without rewriting `items.state` to `blocked`; reviewed in [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) and narrowed in `D-QM10` of [`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md#decision-ledger).)* | Makes drafts immediately readable in Vantage while keeping unruled designs visible instead of prematurely archiving them as `landed`. |
| `D-IP5` | Keep questions canonical in `docs/design/<slug>.md` (**built**); **superseded on answer delivery** by [designer sessions](../reference/tillr-designer-sessions.md): pending owner answers do not travel through `tillr item mail` (`tillr item answer` was never built), and instead arrive as Vantage review comments delivered to the document's resumed designer session (`tools/tillr-drain/drain/comment_intake.go`, `comments.go`). | Avoids duplicating question prose in SQLite ([OQ-OBD2](../reference/observatory-design.md#why-its-this-way)) while delivering review comments straight to the session that holds the design context. |
| `D-IP6` | **Superseded by `D-QP3` of [queue persistence](./tillr-queue-persistence.md#91-decision-ledger) (and further amended by [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1) of [the queue mirrors the designs](./tillr-queue-mirrors-designs.md)):** a child item is declared in tracked Markdown rather than `.tillr/loop/<run_id>/derived-items.json` (moving from in-document blocks to companion `<topic>-plan.md` files once [that design](./tillr-queue-mirrors-designs.md) is built). The original ruling: lanes emit `.tillr/loop/<run_id>/derived-items.json`; only `tillr-drain` applies child items to `.tillr/state.db` after `tillr-land pass` succeeds. | Preserves single-writer store safety and prevents failed or rejected runs from leaking orphan child items onto the board. |
| OQ-IP1 | Scouting and designing inside one idea are nodes of one item's workflow, [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json): the read-only `open` designer may propose replacing the unstarted `write` node with scouts and `join`, as [item graphs](../reference/tillr-item-graphs.md#bounded-graph-proposals-and-the-planner-quick-way) allow. Lineage between items is for separate deliverables; a separate design item is created only when the idea splits into several documents. **Rejected:** a second item for every idea. *(Workflow built in `config/tillr/workflows/scouted-design@v1.json` and `tools/tillr-drain/drain/scouted_design_test.go`, and default for `design` requests in `tools/tillr-item/item/request.go`; `tillr-item idea` does not yet set `workflow` on saved ideas.)* | The owner described this flow on 2026-11-15 (a rambling ticket becomes an item whose first node is a designer that may fan out to scouts), and the item graphs design already resolves the choice as a template inside one item. Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |
| OQ-IP3 | Withdrawn: the queue rail it asked about is removed by [the observatory redesign](../reference/observatory-redesign.md). Items are listed in rank order on the Items page, and lineage and dependencies are drawn on the dependency graph page. | The screen the question concerned no longer exists in the accepted design. Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |
| OQ-IP4 | Work the owner is waiting on, a new idea or answers to a design, runs on lanes that do not count against the lane cap. Nothing running is stopped to make room. **Rejected:** a shared pool, and stopping a build run. *(Built: `[lanes] express = 2` in [`.tillr.toml`](../../.tillr.toml) (`35526a762`), `tools/tillr-drain/drain/express_test.go`, and `175479931` for filed requests.)* | The owner ruled this on 2026-11-15; it is recorded as `D-IG10` in [item graphs](../reference/tillr-item-graphs.md#why-its-this-way). Settled on 2026-11-15 by the session's designer on recorded principle, at the owner's request to answer what principle can answer; the owner may overturn it. |
| OQ-IP2 | Withdrawn: its premise was false. Vantage never writes to the repository; the only thing it changes is deleting the response files an agent leaves in its inbox, which git ignores. An owner's answer or comment reaches the tree only when an agent records it in the document, so nothing saved in Vantage can dirty the checkout or stop the loop, and no harvest or watcher is needed. | Owner's statement, 2026-11-16: "Vantage never writes the repository at all in any way. The only thing it does is delete the responses you sent it." |

### 6.2 Open Questions

None. Every question this design carried is a row of the ledger above.

## 7. What This Design Does Not Cover

- **Browser intake:** Governed by [observatory-actions](../reference/observatory-actions.md#browser-writes), not excluded. With `--write`, the observatory runs the terminal writer for saving ideas and moving items; its own store handle remains read-only. Vantage remains a separate reading and review surface.
- **Spend display, token budgets, or cost-based stopping:** Spend UI remains iceboxed per [OQ-OBD4](../reference/observatory-design.md#why-its-this-way), and `.tillr/state.db` continues recording `usage_cost_usd` without gating dispatch.
- **Self-hosted binary compilation and reload mechanics:** Governed by [tillr-selfhosted-build.md](./tillr-selfhosted-build.md) ([OQ-SH1](./tillr-selfhosted-build.md#decision-ledger) through [OQ-SH3](./tillr-selfhosted-build.md#decision-ledger)), which Slice 1 assumes as its launcher substrate.
- **Ruling the external owner-blocked queue items:** The Claude-whale counterpart, the pi-roadmap host-render row, and existing open design rulings in sibling specs remain owner decisions rather than automated dispositions.
## 8. Remaining work

Two units are left of [the five-slice rollout](#5-the-fastest-path-from-the-49-item-store), and
the second waits on the first. [Slice reconciliation](#slice-reconciliation-2026-11-18) gives
the state of every other slice. **Neither unit is filed yet.** On the owner's rulings of
2026-11-18 (`D-QM3` and [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1) in
[`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md)), no design document gains
a hidden item directive while the companion `<topic>-plan.md` mechanism awaits owner review of
`D-QM4`–`D-QM22`. Each unit is written to become a task however that review lands.

### Add item kinds and a typed derived-from edge to the store

The store cannot yet say what kind of work an item is, or which item it came from. The
observatory shows an idea as an idea only because it finds the idea's intake record. The
idea page finds an idea's outcomes only through a placement receipt, which sync writes when it
first places an item next to the idea it names. That receipt is a note about ordering, not a
fact about lineage, so nothing else can ask "where did this item come from". `D-IP1` and `D-IP2`
settle the fix. Every item gets a kind (`idea`, `design` or `build`). The edge table gains a kind
of its own, so that a `derived_from` edge, an item's single parent, sits beside the existing
`blocked_by` edges without being mistaken for one. Sync writes the edge from a block's `from`
attribute when it first imports the block, replacing the interim `placed_from` event receipt documented in
[observatory actions](../reference/observatory-actions.md#staging-gate-refusals-and-outcomes)
once the store has the table.

**Outcomes:**

- Record each item's kind in the store, with `build` for every existing item.
- Record which item a new item was derived from, once and for good, when sync first imports it.
- Keep the planner's dependency checks reading only `blocked_by` edges.

- **c1:** `tillr-item`'s tables give each item a kind of `idea`, `design` or `build`. Existing
  rows read as `build`, and an idea saved through `tillr-item idea` is stored as `idea`.
- **c2:** The edge table holds a kind for each edge, `blocked_by` or `derived_from`. Existing
  rows migrate as `blocked_by` with their target unchanged, and a store opened before the change
  opens after it with every dependency intact.
- **c3:** An item can have at most one `derived_from` edge. A second edge, or one that would make
  a cycle, is refused with a message that names both items.
- **c4:** When sync first imports a block whose `from` names an item the store holds, it writes
  that item as the block's `derived_from` edge. A later sync never rewrites the edge, and a
  `from` naming an item the store does not hold writes nothing and is not a finding.
- **c5:** The planner, the waiting face and every other reader of dependencies read only
  `blocked_by` edges, and each module's suite passes unchanged against a migrated store.
- **c6:** The observatory reads an item's kind from the store, not from the intake record, and
  this document's Slice 1b entry names where the kind and the edge are built.

**Agent Details:**

- **Proposed id:** `item-ip-kinds-and-derived-edge`.
- **Paths:** `tools/tillr/`, `tools/tillr-item/`, `tools/tillr-store/`, `tools/tillr-drain/`,
  `tools/tillr-view/`, the mirror of each under `yolo-packs/tillr/files/local/share/tillr/`
  (`tillr/`, `tillr-item/`, `tillr-store/`, `tillr-drain/`, `tillr-view/`), and this document.
- **Ref:** [Section 1](#1-item-kinds-states-and-edges), `D-IP1` and `D-IP2` in
  [Section 6.1](#61-settled-decisions).
- **Where:** the item tables are in `tools/tillr-item/item/schema.go`, registered through
  `RegisterDDL`. `item_edges` is read in `tools/tillr-drain/drain/`, `tools/tillr-view/`,
  `tools/tillr-store/` and `tools/tillr/store/`, and every one of those queries must name the
  edge kind. The `from` receipt is written in `tools/tillr-item/item/place.go`. The view's
  derived kind is set in `tools/tillr-view/serve.go`. Fixtures that build `item_edges` are
  `tools/tillr-view/testdata/serve-fixture.sql` and `tools/tillr-item/testdata/legacy-autoqa.sql`.
  Run `just pack-sync` after the last source edit and commit the regenerated mirror.
- **Verify:** Add `tillr-item` tests for the migration, the single-parent and cycle refusals,
  and sync's first-import edge. Add a test that a migrated fixture store keeps every dependency.
  Run `go test ./...` in each of the five modules.

### Show an idea's lineage tree on the item page and in status

Once the store records where each item came from, the owner should be able to open any item
and see the whole tree it belongs to: the idea it started as, the designs cut from it, the
builds cut from those, and how many have landed. [Section 4](#4-the-tree-view-in-the-observatory)
specifies that tree and its truthfulness rules. The queue rail lens that section also asked
about is withdrawn by [OQ-IP3](#61-settled-decisions), because the redesign removed the rail.
So the tree goes on the item page and on the dependency graph page of
[the observatory reference](../reference/observatory-redesign.md), and a terminal reader gets the same
counts from the status face.

**Outcomes:**

- Draw an item's full lineage on its page, with finished ancestors kept in view.
- Draw lineage edges on the dependency graph page, distinct from dependency edges.
- Give the terminal the same tree and the same counts as the page.

- **c1:** The item snapshot and item page carry `kind`, `derived_from` (an id or null) and
  `derived_children` (a list of ids) for every item.
- **c2:** An item with a parent or children shows its whole tree on its page, from the root idea
  down. Each node shows its kind, its state in word and glyph, and links to its own page. The
  tree also shows a count of landed leaf items.
- **c3:** A parent id the store does not hold is drawn as an `unknown parent: <id>` root, and is
  never dropped. An item with neither parent nor children shows no tree.
- **c4:** The dependency graph page draws `derived_from` edges in a style distinct from
  `blocked_by` edges, and its legend names both.
- **c5:** `tillr view status --tree` prints the same trees, and its counts equal the snapshot's
  for the same store.

**Agent Details:**

- **Proposed id:** `item-ip-lineage-tree`.
- **Waits on:** `item-ip-kinds-and-derived-edge`, the unit above.
- **Paths:** `tools/tillr-view/` (its Go server and `web/`, with `web/tests/`),
  `yolo-packs/tillr/files/local/share/tillr/tillr-view/`, and this document.
- **Ref:** [Section 4](#4-the-tree-view-in-the-observatory) and its wire table, without the
  `pending_answers` field, which went with Slice 4.
- **Where:** the snapshot and item page are served from `tools/tillr-view/serve.go`. The item
  page and the dependency graph are `tools/tillr-view/web/src/components/` (`DependencyGraph.tsx`).
  The status face is `tools/tillr-view/status.go`. Run `just pack-sync` after the last source
  edit and commit the regenerated mirror.
- **Verify:** Add Go tests for the wire fields, the unknown-parent root and the `--tree` counts
  against a fixture store. Add frontend tests for the tree, the hidden empty tree and the two edge
  styles. Run `go test ./...` and the web test suite in `tools/tillr-view`.
