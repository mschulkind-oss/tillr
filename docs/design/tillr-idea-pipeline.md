---
title: "The Idea Pipeline: From Owner Sentence to Landed Commit"
status: accepted
stage: DECIDED
tags:
  - tillr
  - pipeline
  - observatory
  - design
  - queue
summary: "Partly built design for turning an owner sentence into ranked work, routing open design questions, ordering items across prerequisite, finish-only, and lineage edges, and drawing item lineage in the observatory: idea intake, the question-open planner hold, express lanes, and the idea's design-seat turn are built; item kinds, typed item_edges (ordering-only dependencies and derived_from lineage), and the lineage tree view are unbuilt."
next: "Build ordering-only item_edges dependencies (D-IP7) and the two units of Section 8: item kinds and the derived_from edge, then the lineage tree."
---

# The Idea Pipeline: From Owner Sentence to Landed Commit

**Status:** Partly built. [Section 5](#5-what-is-built-and-what-is-not) names each part's state, and
[Section 8](#8-remaining-work) writes out the two units that remain.

- **Built:** clean intake (`tillr-item idea` and the observatory's `POST /api/v1/ideas`), the
  numbered question hold, reserved express lanes (`[lanes] express` in
  [`.tillr.toml`](../../.tillr.toml), [OQ-IP4](#61-settled-decisions)), the
  [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json) workflow template
  ([OQ-IP1](#61-settled-decisions)), and the idea's design-seat turn and pre-gate checks.
- **Not built:** the `items.kind` column, typed `item_edges` distinguishing prerequisite
  (`blocked_by`), ordering-only ("after this item finishes, however it finishes"; `D-IP7`), and
  single-parent lineage (`derived_from`) edges, and the lineage tree on the Item page, Dependency
  Graph page and `tillr view status --tree`.

> **In short.** Record every owner idea immediately as an idea item in `.tillr/state.db` without dirtying `/workspace`, evolve it through `derived_from` edges into design items and fenced build items under the closed seven-state model, route open questions through Vantage back into designer revisions, and render that item-and-edge graph as a lineage tree in the observatory.

**Why it matters.** An idea that lives only in a conversation has to be retold by whoever coordinates the work. Capturing intake, decomposition, and question turnarounds in the store and tracked documents removes that retelling and keeps momentum continuous from raw thought to landed commit.

**The shape.** A clean `.tillr/intake/` writer (`tillr-item idea` — **built**), an autonomous idea design turn and `scouted-design@v1` workflow (**built**; saved ideas run as single-node `design`-seat turns unless `scouted-design@v1` is selected), answers that reach a design as review comments delivered to its designer session ([designer sessions](../reference/tillr-designer-sessions.md), **built**), an additive change to `tillr-item`'s tables (`items.kind` and typed `item_edges` distinguishing prerequisite `blocked_by`, ordering-only dependencies, and single-parent `derived_from` — **not built**), and a `LineageTree` view in `tillr-view` (**not built**).

**Start at [§5](#5-what-is-built-and-what-is-not)** for the state of each part, and [§8](#8-remaining-work) for the two unbuilt lineage units; read [§1](#1-item-kinds-states-and-edges) and [§2](#2-the-pipeline-and-one-writer-rules) for the store invariants.

**Needs your ruling:** Zero open questions in this document (every `OQ-IP*` row is closed in [§6.1](#61-settled-decisions); 8 designer's decisions — `D-IP1`, `D-IP2`, `D-IP4`, `D-IP5`, `D-IP6`, `D-IP7`, [`OQ-IP1`](#61-settled-decisions), and [`OQ-IP3`](#61-settled-decisions) — are not owner rulings and remain overturnable by the owner; the earlier ones are catalogued in [the decision-provenance review](../tillr/decision-provenance-review.md)).

**Reads with:** [observatory-actions.md](../reference/observatory-actions.md#idea-intake-and-the-design-turn) (terminal and browser intake, verbatim text files, and owner-selected place; takes precedence over this pipeline for saving ideas), [observatory-redesign.md](../reference/observatory-redesign.md) (the observatory's linked pages), [tillr-designer-sessions.md](../reference/tillr-designer-sessions.md) (Vantage review-comment delivery to resumed designer sessions), [tillr-queue-persistence.md](../reference/tillr-queue-persistence.md) and [tillr-queue-mirrors-designs.md](./tillr-queue-mirrors-designs.md) (child items declared in tracked documents, and the companion-plan ruling [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1)), [the item-states reference](../reference/tillr-item-states.md) (the seven-state projector), [tillr-selfhosted-build.md](./tillr-selfhosted-build.md) (restart and build substrate), and [tillr-intake-paths.md](./tillr-intake-paths.md) (the other doors work enters by).

## 1. Item Kinds, States, and Edges

> **Tree state:** **Not built as a schema column or edge kind** ([Section 8](#add-item-kinds-and-a-typed-derived-from-edge-to-the-store)). `tools/tillr-item/item/schema.go` defines `items` with no `kind` column and `item_edges(item_id, blocked_by)` with no `target_id` or `kind` column (`items.resolves_via` exists as a single `TEXT` column for terminal evidence pointers). `tillr-view` marks `kind: "idea"` on the wire by checking `idea_intake` (`tools/tillr-view/serve.go`, `tools/tillr-view/idea_words.go`), an idea's own item lands once `<intake.dir>/<item_id>.txt` and at least one `from`-marked block commit (`tools/tillr-drain/drain/idea.go`), sync records `from` only as an immutable `placed_from` receipt on `item.update` (`tools/tillr-item/item/place.go`), and `question-open` holds are recorded as planner `hold` decisions in `dispatches` (`tools/tillr-drain/drain/plan.go`) rather than by moving `items.state` to `blocked`. Each package registers its own DDL via `RegisterDDL` and `RegisterColumn`.

Per [the item-states reference](../reference/tillr-item-states.md), **no new lifecycle status strings are added**. Every row projects into the closed seven-state enum (`not_started`, `in_queue`, `working`, `blocked`, `landed`, `resolved_elsewhere`, `closed`). Pipeline stages are distinguished instead by an `items.kind` column (`'idea' | 'design' | 'build'`, defaulting existing rows to `'build'`).

| `items.kind` | Artifact owned (`1:1`) | Entry | Active and held | Terminal state |
| :--- | :--- | :--- | :--- | :--- |
| **`idea`** | `.tillr/intake/<item_id>.txt` (immutable verbatim owner words) | Saved at the owner-selected place; `/workspace` stays git-clean. | A design-seat turn, with scouts when the workflow proposes them ([OQ-IP1](#61-settled-decisions)). | `resolved-elsewhere` (`resolves_via` naming its derived `design` or `build` ids), or `landed` when a single-document idea commits its document directly. |
| **`design`** | `docs/design/<slug>.md` | Declared next to its idea; owner answers make it express work. | Drafts or revises `docs/design/<slug>.md`. A draft with open questions lands on `main` (so Vantage serves it) and the item is held on `question-open`. | `landed` once the document has no open question and its derived items are declared. |
| **`build`** | Code and document paths plus `item_criteria` | Declared when its parent `design` has no open question (or held via a `blocked_by` or ordering-only dependency edge). | A worker edits inside a lane worktree; the gate judges the candidate. | `landed` when the lander advances `refs/heads/main`; `resolved-elsewhere` (with pointer) or `closed` (with reason) if dropped. |

**Typed edges (`item_edges`).** One untyped `blocked_by` list fails two ways: first (`D-IP2`), it cannot separate execution order from lineage (sibling `build` rows chained sequentially would masquerade as parent-child, and cross-tree prerequisites would create multi-parent diamonds); second (`D-IP7`, **designer's decision from an operator request of 2026-11-26, not an owner ruling; the owner may overturn it**), it treats every scheduling edge as a prerequisite for landed work, leaving dependents held as `"needs you: remove the dependency or reopen the closed item"` whenever an earlier item closes with no change (`no_change` or `tillr item close`), even when the edge existed only to keep two writers apart.

The full typed table shape is `item_edges(item_id, target_id, kind)` with `PRIMARY KEY (item_id, target_id, kind)`, covering three edge kinds — prerequisite (`'blocked_by'`), ordering-only (`'after'`, or the ordering-only kind token named by the build item's criteria if different), and lineage (`'derived_from'`, with `CREATE UNIQUE INDEX idx_item_derived_parent ON item_edges(item_id) WHERE kind = 'derived_from'`). Existing `(item_id, blocked_by)` rows carry over as `kind = 'blocked_by'` with their target unchanged. Because an item holds at most one scheduling edge to any target (`blocked_by` or ordering-only, never both on the same `(item_id, target)` pair), landing `D-IP7` before [Section 8](#add-item-kinds-and-a-typed-derived-from-edge-to-the-store) may either add `kind TEXT NOT NULL DEFAULT 'blocked_by'` to `item_edges` via `store.RegisterColumn` or migrate `item_edges` directly to `(item_id, target_id, kind)`; [Section 8](#add-item-kinds-and-a-typed-derived-from-edge-to-the-store) preserves both `blocked_by` and ordering-only rows when `derived_from` is added.

- **Prerequisite edge (`blocked_by`):** `item_id` needs `target_id`'s delivered change. Satisfied only by a non-`closed` finished state (`!closedState(d, state) && finishedState(d, state)`: `landed` or `resolved-elsewhere`). Holds `item_id` while `target_id` is unfinished or missing, and keeps holding `item_id` when `target_id` finishes as `closed` without landing ([`OQ-OBD5`](../reference/observatory-design.md#why-its-this-way)), populating `closed_blockers` and surfacing `"needs you: remove the dependency or reopen the closed item"`.
- **Ordering-only edge ("after this item finishes, however it finishes", `D-IP7`):** `item_id` waits only for `target_id` to stop running so two writers stay apart or an investigation finishes first, without requiring `target_id` to land a change. Holds `item_id` (`reason: "blocked"`, `waits on work`) while `target_id` is unfinished or missing, and releases immediately once `target_id` reaches **any** finished state in `d.Finished` (`finishedState(d, state)`), including `landed`, `resolved-elsewhere`, and `closed` (whether closed by an operator or via `no_change`). A `closed` ordering-only target never enters `closed_blockers` and never holds `item_id` as `"needs you: remove the dependency or reopen the closed item"`. When an ordering-only target *does* finish as `landed`, `tools/tillr-drain/drain/lane_base.go` (`hasLandedBlocker`, `unprovedBlockers`) and `tools/tillr-drain/drain/stale_base.go` (`runBaseLacks`) still require its landed commit in `item_id`'s base (`trunk-unproved` / `blocker-landing-not-on-trunk`) so serialized writers see the landed change; automatic heavy-proof revert (`landedDependents` in `tools/tillr-drain/drain/heavy_revert.go`) treats only `blocked_by` as a semantic prerequisite while Git's clean-revert check guards patch overlap.
- **Lineage edge (`derived_from`):** `item_id` (child) was spawned from `target_id` (parent), enforcing a strict cycle-free forest of trees. Written once by `tillr-item sync` when it first imports a child definition (`D-QP3` of [queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way)) — never inferred by `tillr-view` from id prefixes or paths — and **never** gated on by `tillr-drain` (the planner gates on `blocked_by` and ordering-only edges plus `question-open` holds, and orders by `items.rank`, with `tillr-item` placing child items adjacent to their source item at first import).
- **Validation and surfaces across `tillr-item`, `tillr-drain`, and `tillr-view` (`D-IP7`):** `tillr-item` refuses self-edges (`edge-self`), missing targets (`edge-target-missing`), and cycles across the combined scheduling DAG of `blocked_by` and ordering-only edges (`edge-cycle`), keeping at most one scheduling edge per `(item_id, target)`. `tillr-item request` (keeping `--after <item>` for queue-order placement, `Destination.After`, and using a distinct repeatable flag such as `--after-finish <id>`, or the flag named by the filing's criteria) and `tillr-item dep` write and remove ordering-only edges alongside `blocked_by`, hash them in `requestInputHash`, and carry them on `item.record`, `item.add`, `item.update`, `item.list`, `tillr-item show`, `tillr-view` (`waiting`, `status`, `/api/v1/snapshot`, `/api/v1/items/{id}`, Item page, and Dependency Graph), and `tillr-drain`'s store-signature and readiness queries (`tools/tillr-drain/drain/start_version.go`, `tools/tillr-drain/drain/start_item_sync.go`, `tools/tillr-drain/drain/start_view.go`, `tools/tillr-drain/drain/workflow.go`).

## 2. The Pipeline and One-Writer Rules

Scouting and designing inside one idea are nodes of one item's workflow, not separate items. A single-document design workflow uses [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json): its read-only `open` node on the design seat proposes scouts only when their work must be planned, fenced, or judged apart. Otherwise it proposes nothing, and `write` may use sub-agents of its own. A proposal replaces the unstarted `write` node with [`design-from-scouts@v1`](../../config/tillr/workflows/design-from-scouts@v1.json), whose read-only scout nodes each produce a findings file; its writing `join` node reads every file from its recorded producer. The [item-graph rule](../reference/tillr-item-graphs.md#the-words) separates workflow nodes from child sessions. Lineage between items remains for separate deliverables: several design documents or the build work derived from a design.

`scouted-design@v1` is the default for `design`-seat `tillr-item request` filings (`tools/tillr-item/item/request.go`); `tillr-item idea` leaves `workflow` unset, so a saved idea runs as a single `design`-seat turn unless `workflows.default.design` is configured (`tools/tillr-drain/drain/idea.go`, `tools/tillr-drain/drain/workflow_defaults.go`).

| Stage | Actor | Authority and action | Failure or ambiguity |
| :--- | :--- | :--- | :--- |
| **1. Intake** | Owner via `tillr-item idea`, or the observatory with writes enabled | Flushes `.tillr/intake/<id>.txt`, then creates the idea and intake record together at the selected place with zero pre-scouting. | A store failure leaves the words file and names it in the refusal; `/workspace` tracked files are untouched. |
| **2. Open and scout** | `tillr-run exec` (`open` on the design seat; any proposed scouts on the scout seat) | Reads the intake words. May replace `write` with up to eight bounded read-only scouts and `join`; each scout writes its own `findings` output under its run directory. | No proposal leaves `write` in place. A scout that commits holds the item as `boundary-scope-escape`; a missing findings file holds it as `node-output-missing`. Neither releases `join`. |
| **3. Paired design** | `tillr-run exec` (`write` or `write/join` on the design seat) | Consumes the verbatim intake and every wired findings file; writes `docs/design/<slug>.md`. Separate `design` or `build` deliverables are declared in tracked documents, each naming its idea in `from` ([OQ-IP1](#61-settled-decisions), `D-IP6`). | A document check failure fails the gate, and `main` does not move. |
| **4. Build** | `tillr-run exec` (a build seat) | Runs in an isolated lane worktree against the item's criteria once its design has no open question, every `blocked_by` target has landed or resolved elsewhere, and every ordering-only dependency target has finished in any terminal state (including `closed`). | A refused candidate goes back to its worker with the evidence. |
| **5. Gate and land** | `tillr-gate`, then `tillr-land`, driven by `tillr-drain` | Judges the candidate, lands one commit on `main` under the serial landing lock, and sets the item's state. | A merge conflict or landing check failure aborts before `main` moves. |

| State surface | Single writer | Why |
| :--- | :--- | :--- |
| `/workspace/.tillr/state.db` (`items`, `item_edges`) | `tillr-drain` and the operator, through `tillr-item` | Lanes never open the live store; a child item enters it only when sync reads its declaration from a landed document. |
| `.tillr/intake/<item_id>.txt` | `tillr-item idea` (write-once) | Preserves the owner's exact words outside tracked paths, so intake never dirties the checkout. |
| `docs/design/<slug>.md` and code | The owning item's lane, through the lander | Keeps `refs/heads/main` gated on every revision. |

## 3. The Questions Path

1. **Canonical source, zero duplication.** Per [OQ-OBD2](../reference/observatory-design.md#why-its-this-way) and [OQ-OBD6](../reference/observatory-design.md#why-its-this-way), questions and rulings live in `docs/design/<slug>.md`, never in a SQLite `questions` table. They are read from Vantage's planning index (`tools/tillr/vantage/vantage.go`).
2. **How the observatory shows a question hold.** When an item's `design_doc` has unanswered questions, `tillr-drain` records a `hold` decision with `reason: "question-open"` naming the first open question (`tools/tillr-drain/drain/plan.go`), without rewriting `items.state`, and the observatory renders that hold on Home, Items, Item and Questions (`tools/tillr-view/waiting.go`, [observatory-redesign.md](../reference/observatory-redesign.md)).
3. **How answers return.** An owner's answer reaches a design as a Vantage review comment, which the loop delivers to the document's last designer session ([designer sessions](../reference/tillr-designer-sessions.md); `tools/tillr-drain/drain/comment_intake.go`, `comments.go`). Vantage never writes repository files, so nothing saved there can dirty the checkout or stop the loop ([OQ-IP2](#61-settled-decisions)).
4. **Turnaround.** Work the owner is waiting on — a new idea, or answers to a design — runs on express lanes that do not count against the lane cap ([OQ-IP4](#61-settled-decisions)). The designer writes the owner's rulings into the document; once it has no open question, the build items it declares are dispatchable.

## 4. The Tree View in the Observatory

> **Tree state:** **Not built** ([Section 8](#show-an-ideas-lineage-tree-on-the-item-page-and-in-status)). `tools/tillr-view/serve.go` queries only `blocked_by` from `item_edges`, `tools/tillr-view/status.go` has no `--tree` flag, and the Item page lists flat `idea.outcomes` derived from `placed_from` event receipts (`tools/tillr-view/idea_words.go`).

The tree belongs on the Item page and the Dependency Graph page of [the observatory reference](../reference/observatory-redesign.md) (`ItemRunPages.tsx`, `DependencyGraph.tsx` in `tools/tillr-view/web/src/components/`), and `--tree` belongs on `tillr view status`.

- **`LineageTree` on the Item page (rendered when `derived_from` or `derived_children` is non-empty):** Walks `derived_from` up to the root idea and draws the full descendant tree (`idea -> design* -> build*`) with connector guides, `kind` chip, three-way state (color, word and glyph), `rank`, the held question and its Vantage link on held `design` nodes, and a leaf completion counter (`2 / 5 build items landed`). Completed ancestors (`resolved elsewhere`, `landed`) stay visible inside `LineageTree` so provenance never disappears as work finishes; clicking any node opens that item's page.
- **Dependency Graph page:** draws `derived_from` edges in a style distinct from scheduling dependency edges (`blocked_by` and ordering-only).

| Wire / CLI surface | Added fields | Partial-data and truthfulness contract ([observatory-design.md](../reference/observatory-design.md#truthfulness-and-provenance-contract)) |
| :--- | :--- | :--- |
| `/api/v1/snapshot` (`items[]`), `/api/v1/items/{id}`, `tillr view status --tree`, `tillr item show` | `kind` (`"idea" \| "design" \| "build"`), `derived_from` (`string \| null`), `derived_children` (`string[]`), `resolves_via` (`string[]`), `question_summary` (`{open, epistemic_state} \| null`) | **Standalone rows:** `derived_from: null, derived_children: []` render with no empty tree chrome. **Missing parent id:** renders an explicit `unknown parent: <id>` (`absent` badge) root stub, never dropped. **Live split (`working`, `derived_children: []`):** renders `deriving items (run live)`. **Missing or unreadable `design_doc`:** `question_summary.epistemic_state = "unreadable"` (defect badge), never `0 questions`. CLI and HTTP counts match ([OQ-OBD3](../reference/observatory-design.md#why-its-this-way)). |

## 5. What is built and what is not

Each module owns its own tables (`RegisterDDL`); there is no single schema number.

- **Clean intake: built.** `tillr-item idea` saves the words and the place
  (`tools/tillr-item/item/idea.go`, `TestIdeaUniqueIDAndTitle` in
  `tools/tillr-item/item/idea_test.go`), and the observatory saves an idea through it
  (`tools/tillr-view/serveidea.go`, `TestIdeasHTTPSavesWordsAndShowsIntake` in
  `tools/tillr-view/idea_internal_test.go`). The wire shows `kind: "idea"`, but the view derives
  it from the `idea_intake` table (`tools/tillr-view/serve.go`): no `items.kind` column exists.
- **The `items.kind` column, the ordering-only dependency edge (`D-IP7`), and a typed `derived_from` edge: not built.** `tools/tillr-item/item/schema.go` still defines `item_edges(item_id, blocked_by)` with one untyped dependency list. Sync reads a
  block's `from` attribute ([observatory actions, outcomes](../reference/observatory-actions.md#staging-gate-refusals-and-outcomes))
  but keeps it only as a `placed_from` receipt on the item's update event
  (`tools/tillr-item/item/place.go`, whose comment says it is not this edge). The idea page
  finds an idea's outcomes through that receipt (`tools/tillr-view/idea_words.go`).
- **The numbered question hold: built.** The `question-open` hold and its Vantage index
  reader are in `tools/tillr-drain/drain/plan.go`, `tools/tillr-drain/drain/schema.go` and
  `tools/tillr/vantage/vantage.go`, and `tools/tillr-view/waiting.go` surfaces the held question.
- **The lineage tree and `--tree`: not built.** It needs the typed edge.
- **Answer return: built as review comments.** An owner's answer reaches a design as a review
  comment, which the loop delivers to the document's last designer session
  ([designer sessions](../reference/tillr-designer-sessions.md)).
- **Autonomous idea and design runs: built.** [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json)
  and [`design-from-scouts@v1`](../../config/tillr/workflows/design-from-scouts@v1.json) are
  built (`TestScoutedDesignWorkflowLoop` in `tools/tillr-drain/drain/scouted_design_test.go`) and
  default for `design`-seat requests (`tools/tillr-item/item/request.go`), reserved express lanes
  are live (`[lanes] express` in [`.tillr.toml`](../../.tillr.toml), `tools/tillr-drain/drain/express_test.go`),
  and saved ideas stage on the `design` seat (as a single-node run unless a default
  design workflow is configured) and pass pre-gate candidate checks (`words-missing`,
  `words-altered`, `idea-no-outcome` in `tools/tillr-drain/drain/idea.go`,
  `TestIdeaStageCarriesWordsGuideAndInstructions` and `TestIdeaCandidateChecksBeforeGate` in
  `tools/tillr-drain/drain/idea_test.go`).
- **Child items: declared in tracked Markdown.** `D-QP3` of
  [queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way): a child
  item exists only in tracked Markdown (`from` names its idea).
  [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1) rules that build tasks move from hidden
  comments into companion `<topic>-plan.md` files (not yet built).

## 6. Decisions and Open Questions

### 6.1 Settled Decisions

| ID | Ruling | Why |
| :--- | :--- | :--- |
| `D-IP1` | Keep the closed seven-state item model; add `items.kind` (`idea`, `design`, `build`, default `build`). *(Not built: `items` has no `kind` column; see [Section 8](#8-remaining-work).)* | Preserves the single-projector guarantee while distinguishing intake, design, and code stages. |
| `D-IP2` | **Designer's decision; the owner may overturn this.** Change `item_edges` to `(item_id, target_id, kind)` covering prerequisite dependencies (`blocked_by`), ordering-only dependencies ("after this item finishes, however it finishes"; added 2026-11-26 by `D-IP7` from an operator request), and single-parent lineage (`derived_from`, with a partial unique index on `WHERE kind = 'derived_from'`); `tillr-drain` gates on scheduling dependencies (`blocked_by` and ordering-only) and never on `derived_from`. *(Not built: `item_edges` is `(item_id, blocked_by)` in `tools/tillr-item/item/schema.go`; see `D-IP7` and [Section 8](#8-remaining-work).)* | Separates strict tree genealogy from DAG scheduling dependencies in one event-logged table while distinguishing prerequisite landings from writer-serialization waits (`D-IP7`). |
| `D-IP3` | Intake is governed by [observatory-actions](../reference/observatory-actions.md#idea-intake-and-the-design-turn): `.txt` words files, terminal and browser saving, and an owner-selected place, with no reserved rank prefix. *(Built in `tools/tillr-item/item/idea.go` and `tools/tillr-view/serveidea.go`.)* | One intake rule, stated where the observatory's write actions are specified. |
| `D-IP4` | Each `design` item owns one `docs/design/<slug>.md`; a draft with open questions lands on `main` and the item is held until none is open. *(Built as a planner hold: the planner holds `in-queue` items with `reason: "question-open"` from Vantage's live index in `tools/tillr-drain/drain/plan.go` without rewriting `items.state`; reviewed in [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) and narrowed in `D-QM10` of [`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md#decision-ledger).)* | Makes drafts immediately readable in Vantage while keeping unruled designs visible instead of prematurely archiving them as `landed`. |
| `D-IP5` | Keep questions canonical in `docs/design/<slug>.md`. Owner answers arrive as Vantage review comments delivered to the document's resumed designer session ([designer sessions](../reference/tillr-designer-sessions.md); `tools/tillr-drain/drain/comment_intake.go`, `comments.go`). **Rejected:** carrying answers through a store mail row. *(Built.)* | Avoids duplicating question prose in SQLite ([OQ-OBD2](../reference/observatory-design.md#why-its-this-way)) while delivering review comments straight to the session that holds the design context. |
| `D-IP6` | A child item is declared in tracked Markdown (`D-QP3` of [queue persistence](../reference/tillr-queue-persistence.md#why-its-this-way)), moving from in-document blocks to companion `<topic>-plan.md` files once [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1) of [the queue mirrors the designs](./tillr-queue-mirrors-designs.md) is built. **Rejected:** a run-emitted manifest of child items applied to the store after landing. | Preserves single-writer store safety and prevents failed or rejected runs from leaking orphan child items onto the board. |
| `D-IP7` | **Designer's decision from an operator request (2026-11-26); not an owner ruling, and the owner may overturn this.** Amends `D-IP2` to add an ordering-only scheduling dependency edge ("after this item finishes, however it finishes", e.g. `kind = 'after'`) alongside `blocked_by`: `tillr-item` (`request` — keeping `--after <item>` for queue placement and taking a distinct repeatable flag such as `--after-finish <id>` — `dep`, `apply`, `show`, `list`, `order`, and `requestInputHash`) records and validates ordering-only edges in the same acyclic scheduling DAG as `blocked_by` (refusing `edge-self`, `edge-target-missing`, and mixed `edge-cycle`, keeping at most one scheduling edge per `(item_id, target)`, and migrating existing `item_edges` rows as `blocked_by` via `store.RegisterColumn` on `item_edges.kind` or direct `(item_id, target_id, kind)` migration); `tillr-drain` holds an item while any ordering-only target is unfinished or missing, requires a `landed` ordering-only target's commit in the dependent's base (`tools/tillr-drain/drain/lane_base.go`, `tools/tillr-drain/drain/stale_base.go`) when it did land, and releases the dependent immediately when an ordering-only target reaches any finished state including `closed` (including `no_change`), while `blocked_by` keeps holding on `closed` items; `tillr drain why` and `tillr-view` (`waiting`, `status`, snapshot, Item page, Dependency Graph) show unfinished ordering-only waits as `waits on work` and never put a `closed` ordering-only target in `closed_blockers` or `"needs you: remove the dependency or reopen the closed item"`. **Rejected:** changing `blocked_by` to release on `closed` (breaks prerequisites that require landed code under [`OQ-OBD5`](../reference/observatory-design.md#why-its-this-way)); overloading `request --after <item>` (already `Destination.After`); gating on `derived_from`. *(Not built.)* | Keeps two writers apart or orders work after an investigation without trapping dependents in an operator hold when the earlier item closes with no change. |
| OQ-IP1 | Scouting and designing inside one idea are nodes of one item's workflow, [`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json): the read-only `open` designer may propose replacing the unstarted `write` node with scouts and `join`, as [item graphs](../reference/tillr-item-graphs.md#bounded-graph-proposals-and-the-planner-quick-way) allow. Lineage between items is for separate deliverables; a separate design item is created only when the idea splits into several documents. **Rejected:** a second item for every idea. *(Workflow built in `config/tillr/workflows/scouted-design@v1.json` and `tools/tillr-drain/drain/scouted_design_test.go`, and default for `design` requests in `tools/tillr-item/item/request.go`; `tillr-item idea` does not set `workflow` on saved ideas.)* | The owner's stated flow is one item whose first node is a designer that may fan out to scouts, and item graphs express that as a template inside one item. Designer's decision, 2026-11-15; the owner may overturn it. |
| OQ-IP3 | Withdrawn. Items are listed in rank order on the Items page, and lineage and dependencies are drawn on the dependency graph page of [the observatory redesign](../reference/observatory-redesign.md); there is no separate tree lens on a queue list. | Designer's decision, 2026-11-15; the owner may overturn it. |
| OQ-IP4 | Work the owner is waiting on, a new idea or answers to a design, runs on lanes that do not count against the lane cap. Nothing running is stopped to make room. **Rejected:** a shared pool, and stopping a build run. *(Built: `[lanes] express` in [`.tillr.toml`](../../.tillr.toml), `tools/tillr-drain/drain/express_test.go`.)* | The owner ruled this on 2026-11-15; it is recorded as `D-IG10` in [item graphs](../reference/tillr-item-graphs.md#why-its-this-way). |
| OQ-IP2 | Withdrawn. Vantage never writes to the repository; the only thing it changes is deleting the response files an agent leaves in its inbox, which git ignores. An owner's answer or comment reaches the tree only when an agent records it in the document, so nothing saved in Vantage can dirty the checkout or stop the loop, and no harvest or watcher is needed. | Owner ruling, 2026-11-16. |

### 6.2 Open Questions

None. Every question this design carried is a row of the ledger above.

## 7. What This Design Does Not Cover

- **Browser intake:** Governed by [observatory-actions](../reference/observatory-actions.md#browser-writes), not excluded. With `--write`, the observatory runs the terminal writer for saving ideas and moving items; its own store handle remains read-only. Vantage remains a separate reading and review surface.
- **Spend display, token budgets, or cost-based stopping:** Spend UI is iceboxed per [OQ-OBD4](../reference/observatory-design.md#why-its-this-way), and `.tillr/state.db` records `usage_cost_usd` without gating dispatch.
- **Self-hosted binary compilation and reload mechanics:** Governed by [tillr-selfhosted-build.md](./tillr-selfhosted-build.md) ([OQ-SH1](./tillr-selfhosted-build.md#decision-ledger) through [OQ-SH3](./tillr-selfhosted-build.md#decision-ledger)).

## 8. Remaining work

Two units are left, and the second waits on the first. **Neither unit is an item in the
queue.** [`OQ-QM1`](./tillr-queue-mirrors-designs.md#OQ-QM1) in
[`tillr-queue-mirrors-designs.md`](./tillr-queue-mirrors-designs.md) rules that build tasks
are declared in a companion `<topic>-plan.md`, a mechanism that is not built, so this
document carries no hidden item directive.

### Add item kinds and a typed derived-from edge to the store

The store cannot yet say what kind of work an item is, or which item it came from. The
observatory shows an idea as an idea only because it finds the idea's intake record. The
idea page finds an idea's outcomes only through a placement receipt, which sync writes when it
first places an item next to the idea it names. That receipt is a note about ordering, not a
fact about lineage, so nothing else can ask "where did this item come from". `D-IP1` and `D-IP2`
settle the fix. Every item gets a kind (`idea`, `design` or `build`). The edge table gains a
`derived_from` kind alongside the scheduling dependency kinds (`blocked_by` and ordering-only
`D-IP7`), so an item's single parent sits beside scheduling edges without being mistaken for one.
Sync writes the edge from a block's `from`
attribute when it first imports the block, in place of the `placed_from` event receipt documented in
[observatory actions](../reference/observatory-actions.md#staging-gate-refusals-and-outcomes)
once the store has the table.

**Outcomes:**

- Record each item's kind in the store, with `build` for every existing item.
- Record which item a new item was derived from, once and for good, when sync first imports it.
- Keep the planner's dependency checks reading only scheduling edges (`blocked_by` and ordering-only `D-IP7`), never `derived_from`.

- **c1:** `tillr-item`'s tables give each item a kind of `idea`, `design` or `build`. Existing
  rows read as `build`, and an idea saved through `tillr-item idea` is stored as `idea`.
- **c2:** The edge table holds `derived_from` alongside scheduling dependency kinds (`blocked_by`,
  and ordering-only `D-IP7` when present). Untyped rows carry over as `blocked_by` with their
  target unchanged, existing ordering-only rows stay intact, and a store opened before the change
  opens after it with every dependency preserved.
- **c3:** An item can have at most one `derived_from` edge. A second edge, or one that would make
  a cycle, is refused with a message that names both items.
- **c4:** When sync first imports a block whose `from` names an item the store holds, it writes
  that item as the block's `derived_from` edge. A later sync never rewrites the edge, and a
  `from` naming an item the store does not hold writes nothing and is not a finding.
- **c5:** The planner, the waiting face and every other reader of scheduling dependencies ignore
  `derived_from` edges (reading only `blocked_by` and ordering-only scheduling edges), and each
  module's suite passes unchanged against a store that has been through the change.
- **c6:** The observatory reads an item's kind from the store, not from the intake record, and
  this document's [Section 5](#5-what-is-built-and-what-is-not) names where the kind and the edge
  are built.

**Agent Details:**

- **Paths:** `tools/tillr/`, `tools/tillr-item/`, `tools/tillr-store/`, `tools/tillr-drain/`,
  `tools/tillr-view/`, the mirror of each under `yolo-packs/tillr/files/local/share/tillr/`
  (`tillr/`, `tillr-item/`, `tillr-store/`, `tillr-drain/`, `tillr-view/`), and this document.
- **Ref:** [Section 1](#1-item-kinds-states-and-edges), `D-IP1`, `D-IP2`, and `D-IP7` in
  [Section 6.1](#61-settled-decisions).
- **Where:** the item tables are in `tools/tillr-item/item/schema.go`, registered through
  `RegisterDDL`. `item_edges` is read in `tools/tillr-drain/drain/`, `tools/tillr-view/`,
  `tools/tillr-store/` and `tools/tillr/store/`, and every one of those queries must name the
  edge kind. The `from` receipt is written in `tools/tillr-item/item/place.go`. The view's
  derived kind is set in `tools/tillr-view/serve.go`. Fixtures that build `item_edges` are
  `tools/tillr-view/testdata/serve-fixture.sql` and `tools/tillr-item/testdata/legacy-autoqa.sql`.
  Run `just pack-sync` after the last source edit and commit the regenerated mirror.
- **Verify:** Add `tillr-item` tests for the schema change, the single-parent and cycle refusals,
  and sync's first-import edge. Add a test that a fixture store taken through the change keeps
  every dependency. Run the Go suites of each of the five modules.

### Show an idea's lineage tree on the item page and in status

Once the store records where each item came from, the owner should be able to open any item
and see the whole tree it belongs to: the idea it started as, the designs cut from it, the
builds cut from those, and how many have landed. [Section 4](#4-the-tree-view-in-the-observatory)
specifies that tree and its truthfulness rules. The tree goes on the item page and on the
dependency graph page of [the observatory reference](../reference/observatory-redesign.md), and
a terminal reader gets the same counts from the status face.

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
  scheduling dependency edges (`blocked_by` and ordering-only `D-IP7`), and its legend names each kind.
- **c5:** `tillr view status --tree` prints the same trees, and its counts equal the snapshot's
  for the same store.

**Agent Details:**

- **Waits on:** the unit above.
- **Paths:** `tools/tillr-view/` (its Go server and `web/`, with `web/tests/`),
  `yolo-packs/tillr/files/local/share/tillr/tillr-view/`, and this document.
- **Ref:** [Section 4](#4-the-tree-view-in-the-observatory) and its wire table.
- **Where:** the snapshot and item page are served from `tools/tillr-view/serve.go`. The item
  page and the dependency graph are `tools/tillr-view/web/src/components/` (`DependencyGraph.tsx`).
  The status face is `tools/tillr-view/status.go`. Run `just pack-sync` after the last source
  edit and commit the regenerated mirror.
- **Verify:** Add Go tests for the wire fields, the unknown-parent root and the `--tree` counts
  against a fixture store. Add frontend tests for the tree, the hidden empty tree and the two edge
  styles. Run the Go suite and the web test suite in `tools/tillr-view`.
