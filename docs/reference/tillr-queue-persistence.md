---
title: "Queue persistence — tracked document sync, roadmap placement, reconciliation, and store inventory"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: ba0fe8f647c57fb7ceb3ddd36d81e08c367726c3
covers:
  - tools/tillr-item/item/sync.go
  - tools/tillr-item/item/sync_roadmap.go
  - tools/tillr-item/item/sync_scope.go
  - tools/tillr-item/item/sync_residue.go
  - tools/tillr-item/item/sync_from.go
  - tools/tillr-item/item/sync_placement_report.go
  - tools/tillr-item/item/design_without_item.go
  - tools/tillr-item/item/reconcile.go
  - tools/tillr-item/item/retire.go
  - tools/tillr-item/item/questions.go
  - tools/tillr-item/docshare/docshare.go
  - tools/tillr-item/reconcile.go
  - tools/tillr-store/inventory.go
  - tools/tillr/vantage/vantage.go
  - tools/tillr-drain/drain/sync.go
  - tools/tillr-drain/drain/retire.go
  - tools/tillr-view/status.go
  - tools/tillr-view/waiting_design.go
depends-on:
  - ./observatory-actions.md
  - ./tillr-item-states.md
  - ../design/queue-persistence-ruling.md
  - ../design/board-utilities.md
tags: [tillr, reference, queue-persistence, sync, reconcile, vantage, store, inventory]
summary: "How tillr projects tracked Markdown item blocks and roadmap links into .tillr/state.db without a committed JSON queue file, places first-time imports in interleaved roadmap order, parks Icebox and Exploration documents, reads open questions through one Vantage index reader, reports unfiled staged designs via tillr item reconcile, and inventories the epoch execution record via tillr store inventory."
vantage:
  status-chip: true
---

# Queue persistence — tracked document sync, roadmap placement, reconciliation, and store inventory

**Status:** Verified 2026-11-20 against `ba0fe8f647c57fb7ceb3ddd36d81e08c367726c3`. UNMEASURED: the blocks' tests are the evidence.

Tillr splits queue state between Git-tracked Markdown documents and the private SQLite store (`.tillr/state.db`). Tracked documents under `docs/` hold design prose, open questions, decision ledgers, roadmap routing, and item definitions; `tillr item sync` projects those definitions into `.tillr/state.db` at loop startup, whenever `refs/heads/main` advances, and on demand, while `drain.Plan` reads `.tillr/state.db` plus the cached Vantage question index without reparsing Markdown on every tick. No JSON seed file is committed (`tillr-item seed` refuses and directs callers to `tillr item sync`), and landed commits carry no item IDs or `Tillr-Item:` trailers: when a document-backed item passes its worker fence check, `tillr-drain` removes its directive block in a loop-owned maintenance commit before gating and squashing.

Two read-only companion faces expose what lies outside the synced item set: `tillr item reconcile` classifies every staged document in the Vantage planning index and prints ready-to-paste blocks for uncarried designs without mutating the store or repository, and `tillr store inventory` measures disk and ref usage across `.tillr/state.db`, `.tillr/loop/`, `.tillr/lanes/`, and `refs/tillr/*` without deleting anything.

| Component | Lives in |
| :--- | :--- |
| Item-block parser, `Apply` projection, criterion-freeze check, and residue counters | [`tools/tillr-item/item/sync.go`](../../tools/tillr-item/item/sync.go), [`sync_residue.go`](../../tools/tillr-item/item/sync_residue.go), [`docshare/docshare.go`](../../tools/tillr-item/docshare/docshare.go) |
| Scoped `--roadmap` reader, interleaved roadmap walk, parking, and `from` placement | [`tools/tillr-item/item/sync_scope.go`](../../tools/tillr-item/item/sync_scope.go), [`sync_roadmap.go`](../../tools/tillr-item/item/sync_roadmap.go), [`sync_from.go`](../../tools/tillr-item/item/sync_from.go), [`sync_placement_report.go`](../../tools/tillr-item/item/sync_placement_report.go) |
| Roadmap `design-without-item` warning detector and observatory waiting projection | [`tools/tillr-item/item/design_without_item.go`](../../tools/tillr-item/item/design_without_item.go), [`tools/tillr-view/waiting_design.go`](../../tools/tillr-view/waiting_design.go) |
| Post-worker block retirement (`RetireItemBlock` and `Loop.retireDocumentBlock`) | [`tools/tillr-item/item/retire.go`](../../tools/tillr-item/item/retire.go), [`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go) |
| Shared Vantage index reader and `tillr item show` question read-through | [`tools/tillr/vantage/vantage.go`](../../tools/tillr/vantage/vantage.go), [`tools/tillr-item/item/questions.go`](../../tools/tillr-item/item/questions.go), [`show.go`](../../tools/tillr-item/item/show.go) |
| Loop startup/trunk sync runner and `tillr-view status` residue consumer | [`tools/tillr-drain/drain/sync.go`](../../tools/tillr-drain/drain/sync.go), [`loop.go`](../../tools/tillr-drain/drain/loop.go), [`tools/tillr-view/status.go`](../../tools/tillr-view/status.go) |
| Read-only staged-document reconciliation (`tillr item reconcile`) | [`tools/tillr-item/reconcile.go`](../../tools/tillr-item/reconcile.go), [`tools/tillr-item/item/reconcile.go`](../../tools/tillr-item/item/reconcile.go) |
| Read-only epoch storage and Git ref report (`tillr store inventory`) | [`tools/tillr-store/inventory.go`](../../tools/tillr-store/inventory.go) |

**Reads with:** [`tools/tillr-item/README.md`](../../tools/tillr-item/README.md) and [`tools/tillr-store/README.md`](../../tools/tillr-store/README.md) (module-level contracts and CLI faces), [the queue persistence ruling](../design/queue-persistence-ruling.md) (`D-RS1`–`D-RS5` on the open-work block grammar, trailer ban, and private store boundary), [observatory actions](./observatory-actions.md) (`place`, `idea` intake, `from` chaining, and the `[order] record` rebuild file), [tillr item states](./tillr-item-states.md) (the seven projected states and frozen criteria), [item graphs](./tillr-item-graphs.md) (workflow references and store-declared requests), [the queue mirrors the designs](../design/tillr-queue-mirrors-designs.md) (the unbuilt 2026-11-18 companion-plan replacement for hidden item comments, [`OQ-QM1`](../design/tillr-queue-mirrors-designs.md#OQ-QM1)), and [the decision-provenance review](../tillr/decision-provenance-review.md) (owner rulings [`OQ-PR1`](../tillr/decision-provenance-review.md#OQ-PR1), [`OQ-PR13`](../tillr/decision-provenance-review.md#OQ-PR13), [`OQ-PR14`](../tillr/decision-provenance-review.md#OQ-PR14) and open reviews [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9)–[`OQ-PR11`](../tillr/decision-provenance-review.md#OQ-PR11)).

---

## What lives in git vs the store

| Artifact | Tier | Lives in | Committed in git | Rebuild and retention behavior |
| :--- | :--- | :--- | :--- | :--- |
| **Design prose, `OQ-*` questions, and `## Decision Ledger` rows** | Shared source of truth | `docs/**/*.md` | **Yes** | Authoritative (`D-UT4` in [`board-utilities.md`](../design/board-utilities.md#14-decision-ledger): questions are document state, never item rows). Read through `uvx vantage-check index --format json` (`tools/tillr/vantage/vantage.go`). |
| **Open document-backed item definitions and `blocked_by` edges** | Shared source of truth | `<!-- tillr: item … -->` blocks in `docs/**/*.md` | **Yes** (while open) | Rebuilt on a cold store by `tillr item sync`. Removed from documents before landing by `Loop.retireDocumentBlock` (`tools/tillr-drain/drain/retire.go`), so landed items are absent on cold rebuild without scanning `git log` (`D-RS3`). Governed for future replacement by [`OQ-QM1`](../design/tillr-queue-mirrors-designs.md#OQ-QM1) (unbuilt). |
| **Initial import order and parked-document routing** | Shared source of truth | `docs/roadmap.md`, `docs/<dir>/roadmap.md` (`[sync] roadmaps`) | **Yes** | `sync` places first-time imports in interleaved roadmap order and creates items in documents linked only under `## Icebox` or `## Exploration` as `not-started`. |
| **Hand-dragged queue order (`items.rank`), active leases, and terminal item rows** | Scheduling projection | `.tillr/state.db` (`items`, `lanes`, `dispatches`) | **No** (private, `D-RS5`) | Kept across restarts in `.tillr/state.db`; never written back into design documents. When `.tillr.toml` sets `[order] record` ([`observatory-actions.md`](./observatory-actions.md#the-order-record)), `sync` also applies that order file (ruled out 2026-11-20 by [`OQ-PR13`](../tillr/decision-provenance-review.md#OQ-PR13), removal unbuilt). |
| **Store-declared requests and uncommitted idea words** | Scheduling projection | `.tillr/state.db` (`request_words`, `idea_intake`) + `<store-dir>/intake/<id>.txt` | **Idea words commit on design landing** | `tillr-item request` rows carry `storeDeclared = true` and omit document blocks (`docshare.StoreDeclared`). `tillr-item idea` writes `<store-dir>/intake/<id>.txt` and marks `GET /api/v1/items/{id}` `not yet in the tree` until committed (`tools/tillr-view/idea_words.go`); `D-QP4`'s header badge, overdue timer, and deletion lock are not built. |
| **Sync observations (`sync_residue`, `design_without_item`)** | Scheduling projection | `.tillr/state.db` | **No** | Replaced atomically by `tillr item sync` so read faces (`tillr-view status`, `GET /api/v1/waiting`) never rescan `docs/`. |
| **Runs, attempts, verdicts, spend, shell wrapper logs, lane trees, and `refs/tillr/*`** | Epoch execution record | `.tillr/state.db`, `.tillr/loop/<run_id>/`, `.tillr/lanes/`, `refs/tillr/*` | **No** | Retained across restarts ([`OQ-QP3`](#why-its-this-way), `D-QP7`); `tillr store inventory` measures every category without deleting anything, while `tillr store retire-epoch` and size caps remain unbuilt. Per-run Git Trace2 (`git-trace2.jsonl`, [`OQ-QP4`](#why-its-this-way)) was removed at `d09016bba`. |

---

## Document item blocks and `tillr item sync`

### Block grammar and attributes

`parseItemBlocks` (`tools/tillr-item/item/sync.go`) scans tracked Markdown files line by line while `docshare.Fence` skips fenced code blocks (`` ``` `` and `~~~`). An item block consists of an enclosing Markdown heading (or an outer `- **<Title>**` roadmap bullet when the directive is indented beneath it), an HTML comment starting with `<!-- tillr: item` and ending with `-->` (single-line or multi-line), and one or more `- **c<N>:** <text>` criterion bullets before the next heading or directive:

```markdown
### Swap Waybar for the Ironbar config, style, and user unit
<!-- tillr: item id="item-bar-ironbar-config-v2" paths="config/ironbar/,config/waybar/,tests/ironbar-modules.test.sh" blocked_by="item-ironbar-pkg-patch" -->

- **c1:** `config/ironbar/` holds the bar config and `style.css`, and `config/waybar/` no longer exists.
- **c2:** `tests/ironbar-modules.test.sh` replaces `tests/waybar-ai-modules.test.sh`.
```

`docshare.Attributes` (`tools/tillr-item/docshare/docshare.go`) accepts only known `key="value"` attributes and reports unknown, duplicate, or unquoted attributes as findings:

- **Required:** `id` (`^item-[a-z0-9-]+$`) and `paths` (comma-separated repo-relative path prefixes without `..`, wildcards, or leading `/`).
- **Optional:** `blocked_by` (comma-separated item IDs, never self-referential), `seat` (`ValidSeatName`), `prose` (guide mark such as `"2"`), `workflow` (`<name>@v<N>` or `"single-worker@v1"`), `claims` (`c<N>:<claim-class>` pairs; omitted criteria default to `verification-only`, and untyped `owner decision recorded` text is refused), `judge` (`"agent"`, omitted for mechanical retirement), `from` (origin item ID for initial placement), and `express` (`"true"` or `"false"`). State, rank, and tombstone attributes (`state`, `rank`, `closed`, `resolved-by`) are forbidden (`D-RS1`).
- **Derived references:** `design_doc` is set to the declaring file's repo-relative path, and `body_ref` is `<file>#<heading-slug>` (or `<file>` when titled from a top-level bullet with no heading).

### Read scope, per-file savepoints, and criterion freeze

1. **Whole vs scoped reads (`sync_scope.go`):** With no `--roadmap` flags (and `[sync] roadmaps = []`, the default in `tools/tillr/settings/settings.go`), `ReadRoadmapDocuments` delegates to `ReadSyncDocuments`: `git ls-files -z -- docs` selects tracked regular `.md` files under `docs/`, skipping worktree-modified or missing files (`SkippedFiles`) so uncommitted edits never enter `.tillr/state.db`. When `--roadmap <path>` is repeated on the CLI or `[sync] roadmaps` lists paths in `.tillr.toml`, `Sync` runs with `Partial: true`, reading only those roadmaps and the tracked `docs/**/*.md` files they directly link while protecting the ranks of unread items (`protectUnreadRanks`).
2. **Validation and per-file savepoints (`sync.go`):** Files with any grammar finding (`duplicate-id` across files, `missing-criteria`, `blocker-unavailable`, etc.) write nothing. Valid files apply inside per-file `SAVEPOINT item_sync_file` blocks through `item.Apply` (ordering intra-file dependencies before dependents), so a store-level refusal such as a dependency cycle rolls back only that file and reports a finding (exit `1`) while clean files commit.
3. **Criterion freeze (`definition-frozen`):** When an item already exists in `.tillr/state.db`, `Sync` updates its definition (`title`, `allowed_paths`, `blocked_by`, `seat`, `prose`, `workflow`, `judge_mode`, `express`, `criteria`, `body_ref`, `design_doc`) only while every stored criterion has `state == ""`. Once any criterion carries a verdict state, non-claim-class definition edits are skipped and reported under `Frozen` (`definition-frozen`), whereas tightening `claim_class` alone is allowed because it does not alter what a prior verdict judged.
4. **Loop triggers and post-worker block retirement:** `Loop.syncDocuments` (`tools/tillr-drain/drain/loop.go`) runs `ProcessSteps.Sync` (`tillr-item sync --store … --root … --now … --json`) at loop startup and whenever `refs/heads/main` changes, after withdrawing verdicts from reverted landings. When a trunk candidate finishes its worker fence check, `Loop.retireDocumentBlock` (`tools/tillr-drain/drain/retire.go`) calls `item.RetireItemBlock` (`tools/tillr-item/item/retire.go`) across `ReadSyncDocuments(candidate.Worktree)` to strip the landed item's directive, criteria, and `blocked_by` tokens referencing it, records `drain.retirement` (`WorkerHeadSHA` and `RetirementTreeSHA`), and commits `chore: retire landed document block` before `tillr-gate` and `Trunk.Squash` (which takes the trunk commit message from `WorkerHeadSHA`).
5. **Sync residue (`sync_residue.go`, `tools/tillr-view/status.go`):** Every whole writing sync (`tx != nil && !in.Partial`) persists one `sync_residue` row recording `synced_at`, `unshared_open` (non-finished items in `.tillr/state.db` that lack a Markdown block, excluding prose-rewrite synthetic items and `storeDeclared` items from `tillr-item request` or `idea`), `oldest_unshared`, and `stale_doc_blocks` (under the `git-landing` definition, blocks whose stored item is already `landed`). `tillr-view status` reads `sync_residue` directly from SQLite without scanning `docs/`, reporting `null` counts when no whole sync has been recorded yet.

---

## Roadmap order and parking

### First-import interleaved placement

`Sync` assigns `items.rank` only to ordered items that currently hold `rank == ""` — newly created items, unparked items, and legacy unranked rows (`keyMissingOrder`) — and never overwrites a non-empty `items.rank` except when `[order] record` applies an explicit order file or a `from` insertion respaces an exhausted sparse-key gap (`sync_from.go`).

`roadmapOrder` (`tools/tillr-item/item/sync_roadmap.go`) determines initial import order across roadmaps:

1. **Roadmap set:** Uses `in.Roadmaps` in caller-supplied order when scoped, or discovers all synced documents matching `docs/roadmap.md` or `docs/<dir>/roadmap.md` (`docs/roadmap.md` first, then lexicographic).
2. **Entry extraction (`roadmapEntries`):** Walks each roadmap outside fenced code blocks and parked sections. Each non-parked inline item block is an entry of `[id]`; each Markdown link to another non-parked synced document contributes that target document's declared item IDs in file order on first appearance.
3. **Round-robin interleaving:** Interleaves entries across roadmaps one entry at a time (`roadmap 0 entry 0`, `roadmap 1 entry 0`, …), deduplicating item IDs seen earlier. In a whole sync (`!in.Partial`), blocks in documents not linked by any roadmap follow afterward in path-then-file order.
4. **Placement and `from` chaining:** Items without `from` append after `MAX(items.rank)` in that interleaved order (`appendKeys`). New items carrying `from="<origin-id>"` (`applyFromPlacements` in `sync_from.go`) insert immediately after `<origin-id>` (or at the front of the active order when `<origin-id>` is unranked or absent), chaining siblings that share the same `from` in block order before deleting `<origin-id>`'s `idea_intake` row and marking an open `<origin-id>` `resolved-elsewhere` (`by: "sync"`).

### `## Icebox` and `## Exploration` parking

`parkedDocuments` (`tools/tillr-item/item/sync.go`) marks a target document parked when a roadmap links to it under a heading whose title starts with `"Icebox"` or `"Exploration"` (until the next heading of equal or shallower depth). Inline blocks written directly inside a roadmap file itself are never marked parked (`roadmapLink` ignores self-links).

- **New items in a parked document:** Created with `state = "not-started"` and `rank = ""` whenever the store's definition includes `not-started`.
- **Existing items when a document moves into `## Icebox` / `## Exploration`:** `Sync` moves `in-queue` items in that document to `state = "not-started"` with `rank = ""` (`Report.Parked`), leaving items in `blocked`, `conflict`, or terminal states untouched.
- **Existing items when a document leaves `## Icebox` / `## Exploration`:** Only a whole sync (`!in.Partial`) can prove no roadmap still parks the document; it moves `not-started` items back to `in-queue` (`Report.Unparked`) and appends fresh `items.rank` keys after `MAX(items.rank)`.
- *(Queued policy amendment, unbuilt:* on 2026-11-18 the owner stated that Icebox items should be loaded and marked `blocked` with reason `icebox`, recorded as `D-QM2` in [`tillr-queue-mirrors-designs.md`](../design/tillr-queue-mirrors-designs.md#decision-ledger), and [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9) reviews whether `Exploration` remains a parking heading.)*

### `design-without-item` warnings

`designWithoutItemWarnings` (`tools/tillr-item/item/design_without_item.go`) checks every non-parked roadmap link targeting `docs/design/*.md` (excluding `*-plan.md` files and scope-exclusion headings starting with `"What this file does not cover"`). When the linked design exists in the synced set, its frontmatter `stage` is neither `BUILT` nor `DONE`, it declares zero item blocks (and no companion `<stem>-plan.md` declares a block), and no non-`landed` row in `items` names the design in `design_doc` or `body_ref`, `Sync` emits a `design-without-item` warning (keeping exit `0`) and upserts the observation in `design_without_item` (preserving `first_seen` until cleared). `GET /api/v1/waiting` and `tillr-view waiting` (`tools/tillr-view/waiting_design.go`) project each `design_without_item` row under `class: "needs you"`, `where: "queue"`, `why: "design-without-item: <roadmap>:<line>"`, `mover: "owner: file the document's item block"`.

---

## Open questions and Vantage read-through

Open questions live only in Markdown documents (`D-UT4`). `tillr-drain`, `tillr-view`, and `tillr-item show` all read them through `vantage.VantageQuestions` (`tools/tillr/vantage/vantage.go`), which executes `uvx vantage-check index --format json` once per `QuestionSource` instance (with a 30-second default timeout when caller context carries no deadline) and caches the parsed document map (`questions`, `ledgerIds`, `stage`, `status`, `roadmaps`, and document `links`).

Every query for a `design_doc` returns one of three explicit `QuestionsState` values — never conflating a missing file or broken index tool with zero open questions:

| `QuestionsState` | Condition | Planner (`drain/plan.go`) and observatory (`tillr-view`) behavior |
| :--- | :--- | :--- |
| `questions-ok` | File exists inside `<root>` and `vantage-check index` succeeded | Lists open questions (`state` not `"answered"`, `"resolved"`, `"closed"`, or `"settled"`, dropping questions published for another item's open conflict via `vantage.WithoutQuestions`). If `FirstUnanswered` is non-empty, the planner holds the item with `reason: "question-open"` (`needs you`) without mutating `items.state`. |
| `questions-unknown` | File exists, and `vantage-check` is absent, exited non-zero, timed out, or printed invalid JSON | Fail-closed: planner holds with `reason: "questions-unknown"` (`needs a cause`), `tillr-item show` exits `4` (`ExternalError`), and the observatory Document pane renders `null` (`question count unknown: the index could not be read`) rather than `0 open questions`. |
| `design-doc-missing` | `design_doc` climbs outside `<root>` or does not exist as a regular file on disk | Fail-closed: planner holds with `reason: "design-doc-missing"` (`needs you`), and `GET /api/v1/items/{id}/doc` refuses with `design-doc-missing` or `design-doc-outside-root`. |

When `Loop.syncDocuments` advances to a new trunk commit, `Loop.settleOwnerAnswers` (`tools/tillr-drain/drain/owner_answer.go`) exports that committed trunk tree and indexes it via `VantageQuestions` so owner conflict questions close as `answered` only when a `✅` question marker or `## Decision Ledger` `ledgerIds` entry has landed on `main` (or hold `question-removed` if deleted without a ruling).

---

## Document reconciliation (`tillr item reconcile`)

`tillr item reconcile` (`tools/tillr-item/reconcile.go`, `tools/tillr-item/item/reconcile.go`) is a **read-only** diagnostic over every document in `uvx vantage-check index --format json` joined against `.tillr/state.db` (`OpenRead`, or an empty read when `--store` does not exist yet). As amended by `D-QP11`, `reconcile` writes nothing to `.tillr/state.db` or `docs/`: it partitions `index.documents` in deterministic path order and prints either a human text report or a JSON `item.reconcile` envelope (`--json`). *(Queued retirement, unbuilt:* `D-QM15` in [`tillr-queue-mirrors-designs.md`](../design/tillr-queue-mirrors-designs.md#decision-ledger) retires `reconcile` once roadmap mirroring replaces manual item blocks.)*

| Outcome | Condition on `index.documents` + `.tillr/state.db` | What `reconcile` reports |
| :--- | :--- | :--- |
| **`unstaged`** | `stage` is empty (`null` in Vantage index: READMEs, roadmaps, runbooks, unstaged notes) | Counted in `counts.unstaged`; no action owed. |
| **`evergreen`** | `stage` is `CURRENT`, `GRADUATED`, or `SUPERSEDED` | Counted in `counts.evergreen`; no action owed. |
| **`graduating` / `built`** | `stage == "BUILT"`, **or** (`stage` in `{"DESIGN", "DECIDED"}`, `status == "accepted"`, zero unanswered questions, carried by ≥1 stored item, every carrying item in `{"landed", "closed", "resolved-elsewhere"}` with ≥1 `landed`, and zero unlanded blocks in the file) | If `items` already holds `item-graduate-<slug>` (in any state), counted as `graduating`. Otherwise emits a `built` entry (`inferred_from_landed_items: true` when inferred rather than `stage: BUILT`) with a pasteable `item-graduate-<slug>` block (`seat="design"`, `paths="docs/"`). |
| **`unfiled-blocks`** | `stage` in `{"DESIGN", "DECIDED"}`, `status == "accepted"`, zero unanswered questions, not `built`, and file declares ≥1 `<!-- tillr: item -->` block | If every block's ID is already stored (or its heading slug matches a `landed` item's `body_ref`), counted as `carried`. Otherwise emits `unfiled-blocks` naming each missing block ID and a pasteable copy of that block. |
| **`needs-blocks`** | `stage` in `{"DESIGN", "DECIDED"}`, `status == "accepted"`, zero unanswered questions, zero blocks in the file, and zero rows in `items` naming the file | Emits `needs-blocks` with a pasteable `item-design-<slug>` block (`seat="design"`, `paths="<doc>,docs/roadmap.md,docs/tillr/roadmap.md,docs/pi/roadmap.md"`). If ≥1 row in `items` already names the file, counted as `carried`. |
| **Fourth case (`question-open`, `needs-design`, `unruled`)** | Every remaining staged document | Emits a hold entry with no block: `question-open` (with unanswered `OQ-*` IDs whenever `FirstUnanswered` is true), `needs-design` (`stage == "SKETCH"` with zero unanswered questions), or `unruled` (`stage` in `{"DESIGN", "DECIDED"}`, zero unanswered questions, and `status != "accepted"`). |

For routing metadata (`on-spine`, `parked`, `unrouted`), `reconcile` joins `sections.roadmaps[].path` to each roadmap's `index.documents[].links`, ignoring headings starting with `"What this file does not cover"` and classifying links under headings starting with `"Icebox"` or `"Exploration"` as `parked`. Exit codes follow the `tillr-item` contract: `0` when `built + unfiled-blocks + needs-blocks == 0`, `1` when at least one document owes a block (still printing the full report), `2` on flag usage error, `3` on incompatible/unopenable store, and `4` when `vantage-check` or `git` fails.

---

## Epoch record and store inventory (`tillr store inventory`)

`tillr store inventory` (`tools/tillr-store/inventory.go`) is a read-only inspection face (`store.OpenRead`) that emits a `store.inventory` JSON envelope (or `--text` summary) without creating, checkpointing, or pruning anything:

- **`store`:** Byte sizes of `<store>`, `<store>-wal`, and `<store>-shm`, plus per-table row counts across every base SQLite table (`items`, `events`, `runs`, `attempts`, `dispatches`, `landings`, `verdicts`, etc.) and item counts grouped by `state`.
- **`loop` (`<root>/.tillr/loop`):** Run directory count, recursive regular-file byte total (`total_bytes`), byte breakdown by artifact category (`logs/` vs top-level `*.json` / `*.jsonl` files vs `other`), and the oldest and newest run directories (`oldest_run`, `newest_run` by `runs.updated_at` when present in `runs`, falling back to directory mtime) with `landed` membership cross-referenced against `landings` and `items`.
- **`lanes` (`<root>/.tillr/lanes`):** Lane directory count and recursive regular-file byte total (using `os.Lstat` without following symlinks).
- **`refs` (`git for-each-ref refs/tillr`):** Total ref count and breakdown across `refs/tillr/runs/*`, `refs/tillr/lanes/*`, `refs/tillr/preserved/*`, and `other`, plus how many `refs/tillr/runs/<run_id>` pins belong to runs whose item is already `landed` (`runs_for_landed_items`).

---

## Key invariants

- **Single intake from documents into `Apply`:** Both `tillr item sync` and CLI write faces (`apply`, `request`, `idea`) materialize items exclusively through `item.Apply` (`tools/tillr-item/item/apply.go`), preserving one schema, foreign-key graph, and event-stream contract (`item.add`, `item.update`).
- **Judged criteria never silently drift:** Once any criterion of an item carries a recorded verdict state in `item_criteria`, `tillr item sync` refuses to overwrite the item's prose or criterion text (`definition-frozen`), preventing a landed or failed verdict from pointing at altered requirements.
- **Worktree-dirty Markdown never pollutes `.tillr/state.db`:** `ReadSyncDocuments` compares worktree status against `HEAD` (`git status --porcelain -z -- docs`) and skips dirty or untracked files (`SkippedFiles`), so only committed Markdown definitions sync into the store.
- **Stored ranks survive ordinary syncs:** `Sync` assigns `items.rank` only when an ordered item has `rank == ""` (or when `[order] record` / `from` respacing applies), so hand-dragged places (`tillr-item place`) are never clobbered by subsequent document syncs ([`OQ-QP1`](#why-its-this-way)).
- **Fail-closed question reading:** Missing design files (`design-doc-missing`) and failed or absent `vantage-check` runs (`questions-unknown`) hold dispatch and surface explicit states in `tillr-item show` and `tillr-view`, never defaulting to `0` open questions.
- **No tillr metadata in trunk commits:** `tillr-land` stamps no `Tillr-Item:` trailer, `sync` scans no `git log`, and post-worker block retirement removes finished `<!-- tillr: item -->` directives before gating so a cold store rebuilds only open work (`D-RS3`, [`OQ-PR1`](../tillr/decision-provenance-review.md#OQ-PR1)).

---

## Verification anchors

| Mechanism | Regression tests |
| :--- | :--- |
| Sync over live repository documents, block grammar, per-file savepoints, and `definition-frozen` | [`tools/tillr-item/item/sync_test.go`](../../tools/tillr-item/item/sync_test.go) (`TestSyncOverThisRepositorysDocuments`), [`tools/tillr-item/sync_test.go`](../../tools/tillr-item/sync_test.go) |
| Scoped `--roadmap` syncs, interleaved roadmap order, `from` chaining, and `[sync] roadmaps` | [`sync_roadmap_order_test.go`](../../tools/tillr-item/item/sync_roadmap_order_test.go) (`TestSyncRoadmapEntriesInterleaveAndPark`, `TestSyncRoadmapAppendPreservesStoredKeys`), [`sync_scope_test.go`](../../tools/tillr-item/item/sync_scope_test.go), [`sync_from_test.go`](../../tools/tillr-item/item/sync_from_test.go), [`tools/tillr-drain/sync_scope_test.go`](../../tools/tillr-drain/sync_scope_test.go) |
| `## Icebox` / `## Exploration` parking and whole-sync unparking | [`sync_parking_test.go`](../../tools/tillr-item/item/sync_parking_test.go) (`TestSyncParkingStoredItems`) |
| `design-without-item` detection, `first_seen` retention, and observatory waiting rows | [`design_without_item_test.go`](../../tools/tillr-item/item/design_without_item_test.go), [`tools/tillr-view/waiting_design_test.go`](../../tools/tillr-view/waiting_design_test.go), [`waiting-design.test.tsx`](../../tools/tillr-view/web/tests/waiting-design.test.tsx) |
| Persisted `sync_residue` (`unshared_open`, `stale_doc_blocks`) and `tillr-view status` | [`sync_residue_test.go`](../../tools/tillr-item/item/sync_residue_test.go), [`tools/tillr-view/sync_residue_test.go`](../../tools/tillr-view/sync_residue_test.go) |
| Post-worker block retirement and squash message preservation | [`retire_test.go`](../../tools/tillr-item/item/retire_test.go), [`tools/tillr-drain/drain/retire_test.go`](../../tools/tillr-drain/drain/retire_test.go) |
| Shared `vantage.VantageQuestions` reader and `tillr-item show` question states | [`tools/tillr/vantage/questions_test.go`](../../tools/tillr/vantage/questions_test.go), [`tools/tillr-item/questions_test.go`](../../tools/tillr-item/questions_test.go) |
| Read-only `tillr item reconcile` partition and CLI exit codes | [`reconcile_test.go`](../../tools/tillr-item/item/reconcile_test.go), [`tools/tillr-item/reconcile_test.go`](../../tools/tillr-item/reconcile_test.go) |
| Read-only `tillr store inventory` byte, row, and ref accounting | [`tools/tillr-store/inventory_test.go`](../../tools/tillr-store/inventory_test.go) |
| Backend removal of Git Trace2 environment variables | [`tools/tillr-run/backends_trace_env_test.go`](../../tools/tillr-run/backends_trace_env_test.go) |

---

## Current values

Verified at `ba0fe8f647c57fb7ceb3ddd36d81e08c367726c3`.

| Value | Setting / literal | Defined in |
| :--- | :--- | :--- |
| Default `[sync] roadmaps` setting | `[]` (empty slice: sync reads all tracked `docs/**/*.md` and discovers all roadmaps) | `SyncSettings` in [`tools/tillr/settings/settings.go`](../../tools/tillr/settings/settings.go) |
| Discovered roadmap paths (when unscoped) | `docs/roadmap.md` first, then lexicographic `docs/<dir>/roadmap.md` | `isRoadmapPath`, `roadmapOrder` in [`tools/tillr-item/item/sync_roadmap.go`](../../tools/tillr-item/item/sync_roadmap.go) |
| Parked roadmap section prefixes | Headings starting with `"Icebox"` or `"Exploration"`; scope exclusions starting with `"What this file does not cover"` | `isParkedHeading`, `parkedDocuments` (`sync.go`), `isIgnoredLinkHeading` (`reconcile.go`, `design_without_item.go`) |
| Item ID regex and allowed directive attributes | `^item-[a-z0-9-]+$`; `id`, `paths`, `blocked_by`, `seat`, `prose`, `workflow`, `claims`, `judge`, `from`, `express` | `ItemIDPattern`, `knownAttributes` in [`tools/tillr-item/docshare/docshare.go`](../../tools/tillr-item/docshare/docshare.go) |
| Default Vantage index command and timeout | `uvx vantage-check index --format json`; `30s` (`defaultIndexTimeout`) | `DefaultCommand`, `defaultIndexTimeout` in [`tools/tillr/vantage/vantage.go`](../../tools/tillr/vantage/vantage.go) |
| Sync-owned SQLite tables and record kinds | `design_without_item`, `sync_residue`; `item.sync`, `item.reconcile`, `store.inventory` | `schema.go`, `sync_residue.go`, `main.go`, `reconcile.go`, `tools/tillr-store/inventory.go` |
| Loop block-retirement commit and event kind | `"chore: retire landed document block"`; `drain.retirement` | `retireDocumentBlock` in [`tools/tillr-drain/drain/retire.go`](../../tools/tillr-drain/drain/retire.go) |
| Unset Git Trace2 variables in run backends | `GIT_TRACE2_EVENT`, `GIT_TRACE2_PARENT_SID`, `GIT_TRACE2`, `GIT_TRACE2_PERF` | [`run-pi`](../../tools/tillr-run/backends/pi/run-pi), [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) |

---

<a id="decision-ledger"></a>

## Why it's this way

Each row preserves an owner ruling or designer decision from the graduated queue-persistence design ledger ([`OQ-QP1`](#why-its-this-way)–[`OQ-QP4`](#why-its-this-way), `D-QP1`–`D-QP12`), together with subsequent owner rulings and queued amendments.

| ID | Decision | Status and why it stays (or what superseded it) |
| :--- | :--- | :--- |
| OQ-QP1 | `tillr item sync` gives a place only to items with no stored `items.rank`, appending them after existing ranked items in interleaved roadmap order (`1` of roadmap A, `1` of roadmap B, …) without rank bands or re-ranking, and callers choose one roadmap, several, or all documents *(owner ruling, 2026-11-16)* | Built (`sync_roadmap.go`, `sync_scope.go`). Preserves hand-dragged queue places across syncs while giving newly imported items a deterministic place matching roadmap priority. |
| OQ-QP2 | Author slice definitions in tracked Markdown via `<!-- tillr: item id="…" paths="…" -->` comments under `### <Title>` with `- **c<N>:**` bullets *(designer's decision, 2026-11-15, amended by `D-RS1`; **overturned by owner 2026-11-18 in [`OQ-QM1`](../design/tillr-queue-mirrors-designs.md#OQ-QM1) and 2026-11-20 in [`OQ-PR14`](../tillr/decision-provenance-review.md#OQ-PR14)**)* | Currently built in `sync.go` and `retire.go`. Overturned for future work in [`tillr-queue-mirrors-designs.md`](../design/tillr-queue-mirrors-designs.md) (unbuilt), which replaces hidden in-document comments with mirrored roadmap designs and companion `<topic>-plan.md` files so human-read designs hold no build metadata. |
| OQ-QP3 | Impose no automatic age or byte eviction on `.tillr/` run artifacts; ship a read-only `tillr store inventory` first and leave retention bounds dormant until the owner reviews the inventory *(owner ruling, 2026-11-14)* | Built (`tools/tillr-store/inventory.go`); automatic eviction and `retire-epoch` remain unbuilt so run logs and failure evidence are never silently discarded behind the owner's back. |
| OQ-QP4 | Do not record per-run Git Trace2 (`git-trace2.jsonl`) *(designer's 2026-11-15 keep-on decision **overturned by owner on 2026-11-17** at `d09016bba`)* | Built (`run-pi` and `run-claude` unset `GIT_TRACE2*`; shell wrapper `proc-exec.jsonl` stays). One run recorded 918,054 git commands (~500 MB) for zero diagnostic value. |
| D-QP1 | Retire `config/tillr/seed-items.json` and `tillr item seed` outright rather than maintaining or regenerating a committed JSON queue file *(designer's decision, 2026-11-14; completed via [`OQ-TC1`](../design/tillr-convergence.md#decision-ledger); under review in [`OQ-PR10`](../tillr/decision-provenance-review.md#OQ-PR10))* | Built (`seed-items.json` deleted; `tillr-item seed` exits `2`). Eliminated multi-lane JSON merge conflicts and dual-source drift; work without a design enters via `tillr-item request` or `idea`. |
| D-QP2 | Keep `drain.Plan` a fast read over `.tillr/state.db` and project tracked `docs/**/*.md` into `.tillr/state.db` via `tillr item sync` at startup, after trunk advances, and on demand, sharing one `vantage-check index` reader across drain, view, and `item show` *(designer's decision, 2026-11-14; trailer scan dropped by `D-RS3` / [`OQ-PR1`](../tillr/decision-provenance-review.md#OQ-PR1); block source governed by [`OQ-QM1`](../design/tillr-queue-mirrors-designs.md#OQ-QM1))* | Built (`sync.go`, `sync_residue.go`, `tools/tillr/vantage/vantage.go`, `tools/tillr-view/status.go`). Keeps planner passes pure and fast while making committed documents the shared queue definition. |
| D-QP3 | Declare child build items in tracked Markdown synced after landing rather than applying untracked `.tillr/loop/<run_id>/derived-items.json` manifests *(designer's decision, 2026-11-14, superseding `D-IP6` in [`tillr-idea-pipeline.md`](../design/tillr-idea-pipeline.md#61-settled-decisions); moving to `<topic>-plan.md` under [`OQ-QM1`](../design/tillr-queue-mirrors-designs.md#OQ-QM1))* | Built (`sync_from.go`, [`observatory-actions.md`](./observatory-actions.md#idea-intake-and-the-design-turn)). Ensures child items enter the queue only when their parent commit lands on `main`. |
| D-QP4 | Bound the uncommitted intake bootstrap gap between saving raw idea words and landing the initial design commit *(designer's decision, 2026-11-14)* | **Not built as specified.** `tillr-item idea` saves `<store-dir>/intake/<id>.txt`, instructs stub-on-ambiguity, and `tillr-view` marks the Item page `not yet in the tree`, whereas the proposed `uncommitted intake: N` header badge, `15m`/`45m` `bootstrap-overdue` escalation, and `retire-epoch` lock were never built. |
| D-QP5 | Route off-spine documents under `## Icebox` / `## Exploration` as parked (`not-started`) and require every `blocked` item to name a waiter and reason *(designer's decision, 2026-11-14; waiter rule built via `D-JL8`; **parking state overturned for future build by owner 2026-11-18 in `D-QM2` of [`tillr-queue-mirrors-designs.md`](../design/tillr-queue-mirrors-designs.md#decision-ledger)**; `Exploration` switch under review in [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9))* | Current code parks items in `not-started` (`sync.go`, `sync_parking_test.go`) and requires waiters on `blocked`. Once [`tillr-queue-mirrors-designs.md`](../design/tillr-queue-mirrors-designs.md) is built, Icebox designs and tasks will load as `blocked` (`reason: icebox`). |
| D-QP6 | Migrate `seed-items.json` by intake-freeze and attrition behind `--shadow` *(designer's decision, 2026-11-14; **superseded** by [`OQ-TC1`](../design/tillr-convergence.md#decision-ledger))* | Superseded: remaining open seed items were homed into documents and `seed-items.json` was deleted in one cut rather than building dual-read `--shadow` attrition. |
| D-QP7 | Treat `.tillr/` as an epoch-scoped execution record rather than a permanent Git archive, deferring `tillr store retire-epoch`, byte ceilings, and ref pruning until after `tillr store inventory` review *(designer's decision, 2026-11-14, constrained by [`OQ-QP3`](#why-its-this-way))* | `tillr store inventory` is built (`tools/tillr-store/inventory.go`); `tillr store retire-epoch`, byte caps, and automatic ref pruning are not built. |
| D-QP8 | Provide `tillr item reconcile` to partition `vantage-check index` documents into `unstaged`, `evergreen`, `built`, `unfiled-blocks`, `needs-blocks`, and fourth-case holds (`question-open`, `needs-design`, `unruled`) *(designer's decision, 2026-11-15, amended to read-only by `D-QP11`; under review in [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9); **queued for retirement by `D-QM15` in [`tillr-queue-mirrors-designs.md`](../design/tillr-queue-mirrors-designs.md#decision-ledger)**)* | Built as a read-only CLI report (`tools/tillr-item/item/reconcile.go`). Surfaces accepted designs lacking item blocks and built designs awaiting reference graduation without mutating the store. |
| D-QP9 | Implement [`OQ-QP1`](#why-its-this-way) with sparse lexicographic `items.rank` keys assigned on first import (and once for pre-existing unranked ordered rows), interleaving roadmaps one entry at a time and supporting `--roadmap` / `[sync] roadmaps` *(designer's decision, 2026-11-16)* | Built (`sync_roadmap.go`, `sync_scope.go`, `place.go`). Avoids rank-band collisions and preserves every existing rank key. |
| D-QP10 | Move stored `in-queue` items to unranked `not-started` when their document is parked and restore `not-started` items to `in-queue` at the end of the order on a whole sync, leaving inline roadmap blocks unparked *(designer's decision, 2026-11-16; amended for future build by `D-QM2`; under review in [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9))* | Built (`sync.go`, `sync_parking_test.go`). Prevents partial `--roadmap` syncs from falsely unparking documents parked by another roadmap. |
| D-QP11 | Make `tillr item reconcile` a standalone, read-only report (`OpenRead`) printing ready-to-paste blocks, while `tillr item sync` warns on uncarried roadmap designs via `design-without-item` *(designer's decision, 2026-11-16; under review in [`OQ-PR9`](../tillr/decision-provenance-review.md#OQ-PR9); queued for retirement in `D-QM15`)* | Built (`reconcile.go`, `design_without_item.go`). Keeps `sync` free of synthetic item invention while surfacing unfiled roadmap designs on the waiting board. |
| D-QP12 | Share one `vantage.VantageQuestions` reader in `tools/tillr/vantage/` across `tillr-drain`, `tillr-view`, `tillr-item show`, and `tillr-item reconcile`, retiring hand-rolled Markdown question parsers *(designer's decision, 2026-11-16)* | Built (`tools/tillr/vantage/vantage.go`, `tools/tillr-item/item/questions.go`). Guarantees identical `questions-ok`, `questions-unknown`, and `design-doc-missing` answers across every CLI and HTTP surface. |
