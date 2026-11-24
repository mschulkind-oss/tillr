---
title: "Queue persistence — tracked document sync, roadmap placement, reconciliation, and store inventory"
status: accepted
stage: CURRENT
verified: 2026-11-20
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
  - ../design/board-utilities.md
tags: [tillr, reference, queue-persistence, sync, reconcile, vantage, store, inventory]
summary: "How tillr projects tracked Markdown item blocks and roadmap links into .tillr/state.db without a committed JSON queue file, places first-time imports in interleaved roadmap order, parks Icebox and Exploration documents, reads open questions through one Vantage index reader, reports unfiled staged designs via tillr item reconcile, and inventories the epoch execution record via tillr store inventory."
vantage:
  status-chip: true
---

# Queue persistence — tracked document sync, roadmap placement, reconciliation, and store inventory

> **Delivered copy.** This is `docs/reference/tillr-queue-persistence.md` from this repository at commit `2fa55241a8` (2026-11-24), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/tillr-queue-persistence.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** Verified 2026-11-20. UNMEASURED: the blocks' tests are the evidence.

Tillr splits queue state between Git-tracked Markdown documents and the private SQLite store (`.tillr/state.db`). Tracked documents under `docs/` hold design prose, open questions, decision ledgers, roadmap routing, and item definitions; `tillr item sync` projects those definitions into `.tillr/state.db` at loop startup, whenever `refs/heads/main` advances, and on demand, while `drain.Plan` reads `.tillr/state.db` plus the cached Vantage question index without reparsing Markdown on every tick. No JSON seed file is committed (`tillr-item seed` refuses and directs callers to `tillr item sync`), and landed commits carry no item IDs or `Tillr-Item:` trailers: when a document-backed item passes its worker fence check, `tillr-drain` removes its directive block in a loop-owned maintenance commit before gating and squashing.

Two read-only companion faces expose what lies outside the synced item set: `tillr item reconcile` classifies every staged document in the Vantage planning index and prints ready-to-paste blocks for uncarried designs without mutating the store or repository, and `tillr store inventory` measures disk and ref usage across `.tillr/state.db`, `.tillr/loop/`, `.tillr/lanes/`, and `refs/tillr/*` without deleting anything.

| Component | Lives in |
| :--- | :--- |
| Item-block parser, `Apply` projection, criterion-freeze check, and residue counters | [`tools/tillr-item/item/sync.go`](../../tillr-item/item/sync.go), [`sync_residue.go`](../../tillr-item/item/sync_residue.go), [`docshare/docshare.go`](../../tillr-item/docshare/docshare.go) |
| Scoped `--roadmap` reader, interleaved roadmap walk, parking, and `from` placement | [`tools/tillr-item/item/sync_scope.go`](../../tillr-item/item/sync_scope.go), [`sync_roadmap.go`](../../tillr-item/item/sync_roadmap.go), [`sync_from.go`](../../tillr-item/item/sync_from.go), [`sync_placement_report.go`](../../tillr-item/item/sync_placement_report.go) |
| Roadmap `design-without-item` warning detector and observatory waiting projection | [`tools/tillr-item/item/design_without_item.go`](../../tillr-item/item/design_without_item.go), [`tools/tillr-view/waiting_design.go`](../../tillr-view/waiting_design.go) |
| Post-worker block retirement (`RetireItemBlock` and `Loop.retireDocumentBlock`) | [`tools/tillr-item/item/retire.go`](../../tillr-item/item/retire.go), [`tools/tillr-drain/drain/retire.go`](../../tillr-drain/drain/retire.go) |
| Shared Vantage index reader and `tillr item show` question read-through | [`tools/tillr/vantage/vantage.go`](../../tillr/vantage/vantage.go), [`tools/tillr-item/item/questions.go`](../../tillr-item/item/questions.go), [`show.go`](../../tillr-item/item/show.go) |
| Loop startup/trunk sync runner and `tillr-view status` residue consumer | [`tools/tillr-drain/drain/sync.go`](../../tillr-drain/drain/sync.go), [`loop.go`](../../tillr-drain/drain/loop.go), [`tools/tillr-view/status.go`](../../tillr-view/status.go) |
| Read-only staged-document reconciliation (`tillr item reconcile`) | [`tools/tillr-item/reconcile.go`](../../tillr-item/reconcile.go), [`tools/tillr-item/item/reconcile.go`](../../tillr-item/item/reconcile.go) |
| Read-only epoch storage and Git ref report (`tillr store inventory`) | [`tools/tillr-store/inventory.go`](../../tillr-store/inventory.go) |

**Reads with:** [`tools/tillr-item/README.md`](../../tillr-item/README.md) and [`tools/tillr-store/README.md`](../../tillr-store/README.md) (module-level contracts and CLI faces), [observatory actions](observatory-actions.md) (`place`, `idea` intake, `from` chaining, and the `[order] record` rebuild file), [tillr item states](tillr-item-states.md) (the seven projected states and frozen criteria), [item graphs](tillr-item-graphs.md) (workflow references and store-declared requests), [the queue mirrors the designs](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md) (the unbuilt companion-plan replacement for hidden item comments, [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1)), and [the decision-provenance review](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md) (owner rulings [`OQ-PR1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR1), [`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13), [`OQ-PR14`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR14) and open reviews [`OQ-PR9`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR9)–[`OQ-PR11`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR11)).

---

## What lives in git vs the store

| Artifact | Tier | Lives in | Committed in git | Rebuild and retention behavior |
| :--- | :--- | :--- | :--- | :--- |
| **Design prose, `OQ-*` questions, and `## Decision Ledger` rows** | Shared source of truth | `docs/**/*.md` | **Yes** | Authoritative (`D-UT4` in [`board-utilities.md`](../design/board-utilities.md#14-decision-ledger): questions are document state, never item rows). Read through `uvx vantage-check index --format json` (`tools/tillr/vantage/vantage.go`). |
| **Open document-backed item definitions and `blocked_by` edges** | Shared source of truth | `<!-- inert copy of a tillr directive: item … -->` blocks in `docs/**/*.md` | **Yes** (while open) | Rebuilt on a cold store by `tillr item sync`. Removed from documents before landing by `Loop.retireDocumentBlock` (`tools/tillr-drain/drain/retire.go`), so landed items are absent on cold rebuild without scanning `git log` (`D-RS3`). Governed for future replacement by [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1) (unbuilt). |
| **Initial import order and parked-document routing** | Shared source of truth | `docs/roadmap.md`, `docs/<dir>/roadmap.md` (`[sync] roadmaps`) | **Yes** | `sync` places first-time imports in interleaved roadmap order and creates items in documents linked only under `## Icebox` or `## Exploration` as `not-started`. |
| **Hand-dragged queue order (`items.rank`), active leases, and terminal item rows** | Scheduling projection | `.tillr/state.db` (`items`, `lanes`, `dispatches`) | **No** (private, `D-RS5`) | Kept across restarts in `.tillr/state.db`; never written back into design documents. When `.tillr.toml` sets `[order] record` ([`observatory-actions.md`](observatory-actions.md#the-order-record)), `sync` also applies that order file (ruled out by [`OQ-PR13`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR13); the removal is not built). |
| **Store-declared requests and uncommitted idea words** | Scheduling projection | `.tillr/state.db` (`request_words`, `idea_intake`) + `<store-dir>/intake/<id>.txt` | **Idea words commit on design landing** | `tillr-item request` rows carry `storeDeclared = true` and omit document blocks (`docshare.StoreDeclared`). `tillr-item idea` writes `<store-dir>/intake/<id>.txt` and marks `GET /api/v1/items/{id}` `not yet in the tree` until committed (`tools/tillr-view/idea_words.go`); `D-QP4`'s header badge, overdue timer, and deletion lock are not built. |
| **Sync observations (`sync_residue`, `design_without_item`)** | Scheduling projection | `.tillr/state.db` | **No** | Replaced atomically by `tillr item sync` so read faces (`tillr-view status`, `GET /api/v1/waiting`) never rescan `docs/`. |
| **Runs, attempts, verdicts, spend, shell wrapper logs, lane trees, and `refs/tillr/*`** | Epoch execution record | `.tillr/state.db`, `.tillr/loop/<run_id>/`, `.tillr/lanes/`, `refs/tillr/*` | **No** | Retained across restarts ([`OQ-QP3`](#why-its-this-way), `D-QP7`); `tillr store inventory` measures every category without deleting anything, while `tillr store retire-epoch` and size caps remain unbuilt. Per-run Git Trace2 (`git-trace2.jsonl`, [`OQ-QP4`](#why-its-this-way)) is not recorded. |

---

### What stays private (`D-RS5`)

Two developers sharing a repository share documents, tests and code, never a live queue. These
facts stay solely in `.tillr/` and are lost when a workspace disappears or moves machines:

| Private surface | Why leaving it out of git is right |
| :--- | :--- |
| **Personal order (stored `items.rank` keys)** | The order is private unless the repository sets `order.record`. With that opt-in, a plain text [order record](observatory-actions.md#the-order-record) carries the hand order across a rebuild; sync uses it only to place newly imported items, never to move items already held. Roadmaps still separate active work from `## Icebox` and `## Exploration`. |
| **In-flight execution (`lanes`, leases, `runs`, retries, `usage_cost_usd`)** | Workbench state of one machine's processes; meaningless on a second developer's machine. |
| **Local `decisions` holds and frozen `not-done` flags** | They record why one developer paused a broken item locally. An architectural finding belongs in a design document's prose or ledger. |
| **`landed` rows and item-to-commit joins** | Once code and updated documents are on the trunk, the item handle has done its job; git history documents the change in repository terms. |
| **Unpromoted ideas and slices of an `in-review` draft** | Unfinished personal thoughts. They show as `unshared_open`, and residue lost inside that window is accepted. |

---

## Document item blocks and `tillr item sync`

### Block grammar and attributes

`parseItemBlocks` (`tools/tillr-item/item/sync.go`) scans tracked Markdown files line by line while `docshare.Fence` skips fenced code blocks (`` ``` `` and `~~~`). An item block consists of an enclosing Markdown heading (or an outer `- **<Title>**` roadmap bullet when the directive is indented beneath it), an HTML comment starting with `<!-- inert copy of a tillr directive: item` and ending with `-->` (single-line or multi-line), and one or more `- **c<N>:** <text>` criterion bullets before the next heading or directive:

```markdown
### Add the example report command
<!-- tillr: item id="item-example-report" paths="tools/example/,tests/example.test.sh" blocked_by="item-example-schema" -->

- **c1:** `tools/example/` holds the report command and its fixture.
- **c2:** `tests/example.test.sh` passes.
```

`docshare.Attributes` (`tools/tillr-item/docshare/docshare.go`) accepts only known `key="value"` attributes and reports unknown, duplicate, or unquoted attributes as findings:

- **Required:** `id` (`^item-[a-z0-9-]+$`) and `paths` (comma-separated repo-relative path prefixes without `..`, wildcards, or leading `/`).
- **Optional:** `blocked_by` (comma-separated item IDs, never self-referential), `seat` (`ValidSeatName`), `prose` (guide mark such as `"2"`), `workflow` (`<name>@v<N>` or `"single-worker@v1"`), `claims` (`c<N>:<claim-class>` pairs; omitted criteria default to `verification-only`, and untyped `owner decision recorded` text is refused), `judge` (`"agent"`, omitted for mechanical retirement), `from` (origin item ID for initial placement), and `express` (`"true"` or `"false"`). State, rank, and tombstone attributes (`state`, `rank`, `closed`, `resolved-by`) are forbidden (`D-RS1`).
- **Derived references:** `design_doc` is set to the declaring file's repo-relative path, and `body_ref` is `<file>#<heading-slug>` (or `<file>` when titled from a top-level bullet with no heading).

### Which work gets a shared block (`D-RS2`)

`body_ref` is provenance, not an automatic home. A stored item gets a committed item block if and
only if all four clauses hold:

1. **Agent-executable:** `c1` is a repository change a lane can verify, not an owner decision
   recorded in a document.
2. **Accepted specification:** `design_doc` (or `body_ref` when `design_doc` is empty) carries
   `status: accepted`, or the item is a standalone code or gate defect with no unsettled design.
3. **Trunk-stageable fence:** every prefix in `allowed_paths` resolves on the trunk.
4. **Unfrozen, non-conflicting criteria:** no criterion is frozen `not-done` on a specification
   conflict against a settled ruling.

A qualifying block lives inside its accepted plan or design document when one exists, and
otherwise inline on [the tillr roadmap](../tillr/roadmap.md). The rest get no block:

- **Owner rulings and host actions** live in Vantage question directives. A duplicate item block
  breaks `D-UT4` of [`board-utilities.md`](../design/board-utilities.md#14-decision-ledger) and
  lets a lane mechanically stamp `c1` done.
- **Slices cut from an `in-review` draft** are private staging; the design's prose already holds
  the architecture.
- **Conflicted or container items** would turn local queue debris into a shared mandate.

A finished or dropped block is deleted from its document. An architectural rejection becomes a
ledger row, never a dead item block.

### Read scope, per-file savepoints, and criterion freeze

1. **Whole vs scoped reads (`sync_scope.go`):** When `--roadmap` is omitted on `tillr-item sync`, `ReadRoadmapDocuments` reads `[sync] roadmaps` from `<root>/.tillr.toml` (`--roadmap` overrides `.tillr.toml`, and malformed `.tillr.toml` or invalid roadmap paths exit `2` with zero store writes). When the resolved list is empty (`[]`, the default in `tools/tillr/settings/settings.go`), `ReadRoadmapDocuments` delegates to `ReadSyncDocuments` over tracked regular `.md` files under `docs/`. When `--roadmap <path>` is repeated on the CLI or `[sync] roadmaps` lists paths in `.tillr.toml`, `Sync` runs with `Partial: true` ([`D-QP13`](#scoped-roadmaps-import-filter-and-skipped-block-report-d-qp13)): it validates the selected roadmaps and their direct links against symlink traversal, scans all tracked regular `docs/**/*.md` files, imports only blocks sitting in an unparked non-exclusion section of a selected roadmap or in a non-roadmap, non-parked document linked there while protecting unread/skipped ranks (`protectUnreadRanks`), and reports every skipped block by `file` and `item_id` (`SkippedBlocks`, `SkippedCount`).
2. **Validation and per-file savepoints (`sync.go`):** Files with any grammar finding (`duplicate-id` across files, `missing-criteria`, `blocker-unavailable`, etc.) write nothing. Valid files apply inside per-file `SAVEPOINT item_sync_file` blocks through `item.Apply` (ordering intra-file dependencies before dependents), so a store-level refusal such as a dependency cycle rolls back only that file and reports a finding (exit `1`) while clean files commit.
3. **Criterion freeze (`definition-frozen`):** When an item already exists in `.tillr/state.db`, `Sync` updates its definition (`title`, `allowed_paths`, `blocked_by`, `seat`, `prose`, `workflow`, `judge_mode`, `express`, `criteria`, `body_ref`, `design_doc`) only while every stored criterion has `state == ""`. Once any criterion carries a verdict state, non-claim-class definition edits are skipped and reported under `Frozen` (`definition-frozen`), whereas tightening `claim_class` alone is allowed because it does not alter what a prior verdict judged.
4. **Loop triggers and post-worker block retirement:** `Loop.syncDocuments` (`tools/tillr-drain/drain/loop.go`) runs `ProcessSteps.Sync` (`tillr-item sync --store … --root … --now … --json`) at loop startup and whenever `refs/heads/main` changes, after withdrawing verdicts from reverted landings. When a trunk candidate finishes its worker fence check, `Loop.retireDocumentBlock` (`tools/tillr-drain/drain/retire.go`) calls `item.RetireItemBlock` (`tools/tillr-item/item/retire.go`) across `ReadSyncDocuments(candidate.Worktree)` to strip the landed item's directive, criteria, and `blocked_by` tokens referencing it, records `drain.retirement` (`WorkerHeadSHA` and `RetirementTreeSHA`), and commits `chore: retire landed document block` before `tillr-gate` and `Trunk.Squash` (which takes the trunk commit message from `WorkerHeadSHA`).
5. **Sync residue (`sync_residue.go`, `tools/tillr-view/status.go`):** Every whole writing sync (`tx != nil && !in.Partial`) persists one `sync_residue` row recording `synced_at`, `unshared_open` (non-finished items in `.tillr/state.db` that lack a Markdown block, excluding prose-rewrite synthetic items and `storeDeclared` items from `tillr-item request` or `idea`), `oldest_unshared`, and `stale_doc_blocks` (under the `git-landing` definition, blocks whose stored item is already `landed`). `tillr-view status` reads `sync_residue` directly from SQLite without scanning `docs/`, reporting `null` counts when no whole sync has been recorded yet.

---

## Roadmap order and parking

### First-import interleaved placement

`Sync` assigns `items.rank` only to ordered items that currently hold `rank == ""` — newly created items, unparked items, and unranked rows (`keyMissingOrder`) — and never overwrites a non-empty `items.rank` except when `[order] record` applies an explicit order file or a `from` insertion respaces an exhausted sparse-key gap (`sync_from.go`).

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
- **Scoped syncs (`D-QP13`):** While HTML item comments stay in service before [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md) lands, `not-started` parking and unparking apply on unscoped whole syncs (`roadmaps` empty), whereas scoped `roadmaps` syncs skip parked blocks without creating or altering stored rows and list them in `skipped_blocks` ([`D-QP13`](#scoped-roadmaps-import-filter-and-skipped-block-report-d-qp13)).
- *(Policy amendments, unbuilt:* Icebox items are to be loaded and marked `blocked` with reason `icebox` under `D-QM2` in [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#decision-ledger), and [`OQ-PR9`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR9) reviews whether `Exploration` remains a parking heading.)*

### Scoped roadmaps import filter and skipped-block report (D-QP13)

`D-QP13` in [Why it's this way](#why-its-this-way) is a designer's decision that the owner may overturn. It is built in `sync.go`, `sync_scope.go`, `sync_roadmap.go`, `design_without_item.go`, and `tools/tillr-drain/drain/sync.go`. The roadmap setting limits what sync imports, not only its order; `D-QM2` (Icebox items load as `blocked`) and `D-QM3` (item markup is not loaded silently everywhere) in [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#decision-ledger) are the owner rulings it works under. `D-QP13` amends `D-QP5`, `D-QP9`, and `D-QP10` for HTML item-block sync until [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md) replaces HTML item blocks.

1. **Resolving selected roadmaps across CLI and `.tillr.toml` (`tools/tillr-item/main.go`, `sync_scope.go`).**
   - In `ReadRoadmapDocuments(root, roadmaps)` (called by `tillr-item sync`), one or more `--roadmap` CLI flags (`len(roadmaps) > 0`) select those roadmaps in flag order, overriding `[sync] roadmaps` in `<root>/.tillr.toml`.
   - When `--roadmap` is omitted (`len(roadmaps) == 0`) and `root != ""`, `ReadRoadmapDocuments` reads `settings.Read(root).Sync.Roadmaps` before opening or creating `--store` (returning a usage error, exit `2`, with zero store writes if `<root>/.tillr.toml` is malformed or unreadable).
   - When the resolved list is empty (`[]`), `ReadRoadmapDocuments` delegates to `ReadSyncDocuments(root)` and `Sync` runs an unscoped whole sync (`Partial: false`, `Scope: "all-documents"`), leaving existing whole-sync discovery, unlinked-document fallback, and `## Icebox` / `## Exploration` parking (`D-QP5`, `D-QP10`) unchanged. Direct Go callers of `item.Sync` pass `SyncInput.Roadmaps` explicitly (`TestSyncOverThisRepositorysDocuments` continues to call `ReadSyncDocuments(root)` with `Roadmaps: nil` so structural validation checks every block in the repository).

2. **Validating selected roadmaps and scanning tracked `docs/**/*.md` for skipped blocks (`sync_scope.go`).**
   - When the resolved roadmap list `selected` is non-empty (`Partial: true`, `Scope: selected`), every normalized, deduplicated path in `selected` must still pass `checkSyncFile` as an existing regular tracked `.md` file under `docs/` without symlink traversal (`--roadmap "<given>" must name …`, exit `2`, zero store writes), and any direct tracked Markdown link under `docs/` on a selected roadmap that resolves to a symlink is likewise refused with exit `2` and zero store writes (`TestSyncRoadmapScopeRefusesInvalidPathsWithoutWrites`, `TestSyncRoadmapScopeDoesNotReadSymlinkTargets`, `TestLoopSyncRoadmapsPathRefusal`).
   - After validating `selected` and its direct links, `ReadRoadmapDocuments` reads all tracked regular `.md` files under `docs/` (`docshare.TrackedPaths(root)`, skipping working-tree-missing files like `ReadTracked` and ignoring unreferenced symlinks outside `selected` and its links) and passes them to `item.Sync` with `Partial: true, Roadmaps: selected` so blocks outside the selected scope are inventoried rather than silently unseen.

3. **In-scope imported blocks vs skipped blocks (`sync.go`, `sync_roadmap.go`).**
   - When `len(in.Roadmaps) > 0`, `roadmapBlocks` does **not** finish by appending remaining documents' blocks:
     - **In-scope documents and imported blocks:** Each selected roadmap `r` (`isRoadmap(r, in.Roadmaps)`) is an in-scope document; an unfenced item block sitting directly in `r` is imported iff its directive line lies outside parked sections (headings starting with `"Icebox"` or `"Exploration"` until the next heading of equal or shallower depth) and outside scope-exclusion sections (headings starting with `"What this page does not cover"` or `"What this file does not cover"` until the next heading of equal or shallower depth). A tracked document `target` in `in.Documents` is an in-scope document iff it is not itself a roadmap (`!isRoadmap(target)`, i.e. neither `docs/roadmap.md` nor `docs/<dir>/roadmap.md`), is linked via `roadmapLink` on an unparked, non-exclusion line of at least one selected roadmap, and is **not** parked under `## Icebox` / `## Exploration` of any selected roadmap (`!parkedDocuments(in.Documents, in.Roadmaps)[target]`, so a passing prose reference above `## Icebox` such as [`OQ-AQ2`](../design/tillr-autoqa.md#OQ-AQ2) does not unpark an Icebox document). All unfenced blocks in such an in-scope `target` are imported at the first selected-roadmap entry reaching `target`, interleaved round-robin across `in.Roadmaps` ([`OQ-QP1`](#why-its-this-way), `D-QP9`).
     - **No prose-vs-bullet link heuristic:** Outside fenced code blocks, parked sections, and `"What this page/file does not cover"` sections, `roadmapLines` and `roadmapLink` treat every Markdown link to a non-roadmap, non-parked tracked `docs/**/*.md` file uniformly. A prose link is an ordinary link, whereas any document linked *only* from a roadmap that is not selected, linked only under `"What this page/file does not cover"` or `## Icebox` / `## Exploration`, or unlinked is out of scope.
     - **Skipped blocks and untouched store rows:** Every unfenced item directive in `in.Documents` not imported above (in unselected roadmaps or documents only they reach, in unlinked documents, inline inside parked or scope-exclusion sections of a selected roadmap, or in documents parked or linked only from scope-exclusion sections of a selected roadmap) is **skipped**: it is never created, updated, parked, unparked, ranked, rewritten (`proseRewriteWrites`), or retired, and any pre-existing row in `.tillr/state.db` for its ID keeps its stored definition, state, `items.rank`, and `updated_at` untouched (`protectUnreadRanks` and `allowed` cover only in-scope imported declarations). Out-of-scope documents and skipped blocks emit no `Findings`, `SkippedFiles`, or `design-without-item` `Warnings` (`designWithoutItemWarnings` runs only over selected roadmaps and in-scope documents), so an unreached draft finding never makes scoped `tillr-item sync` exit `1`.
     - **Relation to `D-QM2`, `D-QP5`, and `D-QP10`:** `D-QM2` governs the unbuilt mirrored-design and companion-plan sync in [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md) (`blocked` with `reason: icebox`). For HTML item blocks, `D-QP5` and `D-QP10` (`not-started` parking and whole-sync unparking) remain active on unscoped whole syncs (`len(in.Roadmaps) == 0`), while scoped syncs (`len(in.Roadmaps) > 0`) skip parked blocks and list them in `skipped_blocks` so nothing is dropped silently (`D-QM3`).

4. **`SyncReport` and `SyncText` skipped-block contract (`sync.go`, `sync_scope.go`, `tools/tillr-drain/drain/sync.go`).**
   - `SyncReport.Documents` counts the **in-scope documents** and `SyncReport.Blocks` counts the **in-scope imported blocks** (keeping `TestSyncRoadmapScopeReadsOnlyDirectTrackedDocuments`'s `documents` count and `Blocks == CreatedCount + UnchangedCount + UpdatedCount + FrozenCount` when `SkippedFiles` is empty). `Partial` remains `true` whenever `len(in.Roadmaps) > 0`, keeping `unshared_open` and `stale_doc_blocks` `null` in JSON (`"not computed; needs a sync of every document"` in text) and leaving `item_sync_residue` untouched (`TestSyncRoadmapScopePreservesUnreadItemsAndWholeObservation`).
   - `SyncReport` adds `SkippedBlocks []SyncSkippedBlock` (`json:"skipped_blocks"`, initialized to `[]SyncSkippedBlock{}` so JSON encodes `[]`, never `null`) and `SkippedCount int` (`json:"skipped_count"`, `len(SkippedBlocks)`), where `SyncSkippedBlock` has `File string` (`json:"file"`) and `ItemID string` (`json:"item_id"`, parsed via `docshare.Attributes`, or `""` when an out-of-scope directive omits `id`), ordered deterministically by `(file, line)` (lexicographic repo-relative `file`, then top-to-bottom source line).
   - `SyncText(report, at)` keeps the summary line byte-identical when `report.SkippedCount == 0` (appending `, skipped <N>` only when `report.SkippedCount > 0`) and prints one line per skipped block (`skipped: <file> <item_id>`, or `skipped: <file> (missing id)` when `ItemID == ""`). `tillr-drain`'s `syncPayload` / `SyncOutcome` decodes `skipped_blocks` and `skipped_count` without treating skipped blocks as findings.

5. **Tests.**
   - Scoped import with parked and unselected-roadmap blocks left uncreated (or untouched if already stored) and listed in `skipped_blocks`: `TestLoopSyncRoadmapsScope` in `tools/tillr-drain/drain/sync_scope_ext_test.go`, `TestSyncRoadmapScopeCustomNamesParkingAndDryRun` in `tools/tillr-item/sync_scope_edges_test.go`, and `TestPartialSyncParkingAndSourcePreviewMatchesWrite` plus `TestPartialSyncParkingSkipsMissingKeysAndPreservesUnreadItems` in `tools/tillr-item/item/sync_scope_test.go`. Whole-sync parking is pinned by `TestSyncParkingStoredItems` and `TestSyncRoadmapEntriesInterleaveAndPark`.
   - `[sync] roadmaps` in `.tillr.toml` limiting imports and reporting skipped blocks by `file` and `item_id`: `TestSyncSettingsRoadmapsLimitsImportsAndReportsSkippedBlocks` in `tools/tillr-item/sync_scope_test.go`.

**Rejected** (designer's decision; the owner may overturn): keeping `ReadRoadmapDocuments` blind to unselected `docs/**/*.md` files (cannot report skipped blocks by `file` and `item_id`); importing `## Icebox` / `## Exploration` blocks during scoped comment sync (contradicts the unparked-entry rule); inventing a bullet-vs-prose Markdown link heuristic (breaks existing paragraph-link roadmaps and `roadmapBlocks`'s first-roadmap-wins contract); and running full finding checks on out-of-scope documents (would let an unselected area's draft block fail scoped `tillr-item sync`).

### `design-without-item` warnings

`designWithoutItemWarnings` (`tools/tillr-item/item/design_without_item.go`) checks every non-parked roadmap link targeting `docs/design/*.md` (excluding `*-plan.md` files and scope-exclusion headings starting with `"What this file does not cover"`). When the linked design exists in the synced set, its frontmatter `stage` is neither `BUILT` nor `DONE`, it declares zero item blocks (and no companion `<stem>-plan.md` declares a block), and no non-`landed` row in `items` names the design in `design_doc` or `body_ref`, `Sync` emits a `design-without-item` warning (keeping exit `0`) and upserts the observation in `design_without_item` (preserving `first_seen` until cleared). `GET /api/v1/waiting` and `tillr-view waiting` (`tools/tillr-view/waiting_design.go`) project each `design_without_item` row under `class: "needs you"`, `where: "queue"`, `why: "design-without-item: <roadmap>:<line>"`, `mover: "owner: file the document's item block"`.

---

## Landing receipts without commit trailers

No commit names a tillr item, and nothing scans commit trailers (`D-RS3`).

- **No trailer.** `Trunk.Squash` in `tools/tillr-land/land` takes no item id and writes the head's
  message unchanged, and the worker brief (`tools/tillr-run/backends/brief-template.md`) forbids
  an item id or trailer in the commit message.
- **The private receipt** is `items.state = 'landed'` and `landings.trunk_after` in one
  developer's `.tillr/state.db`. A cold store on a fresh machine reconstructs zero `landed` rows.
- **The shared receipt** is the absence of the open item block, or `stage: BUILT` or `CURRENT` on
  the document. The drain strips a candidate's block before the gate
  ([block retirement](#read-scope-per-file-savepoints-and-criterion-freeze)), so the landing
  itself removes it.
- **A fresh machine loses** `landed` rows, item-to-commit attribution, retry counts, gate and
  judge envelopes, and spend. The repository's history and the gate suites carry every fact a team
  needs about landed code.
- **A block left behind** by a landing whose retirement did not reach the trunk is synced as open
  once on a fresh machine, runs against a tree where its criteria already pass, and is retired in
  a documentation-only commit. One idempotent check is cheaper than private queue ids in every
  commit.

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

`tillr item reconcile` (`tools/tillr-item/reconcile.go`, `tools/tillr-item/item/reconcile.go`) is a **read-only** diagnostic over every document in `uvx vantage-check index --format json` joined against `.tillr/state.db` (`OpenRead`, or an empty read when `--store` does not exist yet). As amended by `D-QP11`, `reconcile` writes nothing to `.tillr/state.db` or `docs/`: it partitions `index.documents` in deterministic path order and prints either a human text report or a JSON `item.reconcile` envelope (`--json`). *(Retirement, unbuilt:* `D-QM15` in [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#decision-ledger) retires `reconcile` once roadmap mirroring replaces manual item blocks.)*

| Outcome | Condition on `index.documents` + `.tillr/state.db` | What `reconcile` reports |
| :--- | :--- | :--- |
| **`unstaged`** | `stage` is empty (`null` in Vantage index: READMEs, roadmaps, runbooks, unstaged notes) | Counted in `counts.unstaged`; no action owed. |
| **`evergreen`** | `stage` is `CURRENT`, `GRADUATED`, or `SUPERSEDED` | Counted in `counts.evergreen`; no action owed. |
| **`graduating` / `built`** | `stage == "BUILT"`, **or** (`stage` in `{"DESIGN", "DECIDED"}`, `status == "accepted"`, zero unanswered questions, carried by ≥1 stored item, every carrying item in `{"landed", "closed", "resolved-elsewhere"}` with ≥1 `landed`, and zero unlanded blocks in the file) | If `items` already holds `item-graduate-<slug>` (in any state), counted as `graduating`. Otherwise emits a `built` entry (`inferred_from_landed_items: true` when inferred rather than `stage: BUILT`) with a pasteable `item-graduate-<slug>` block (`seat="design"`, `paths="docs/"`). |
| **`unfiled-blocks`** | `stage` in `{"DESIGN", "DECIDED"}`, `status == "accepted"`, zero unanswered questions, not `built`, and file declares ≥1 `<!-- inert copy of a tillr directive: item -->` block | If every block's ID is already stored (or its heading slug matches a `landed` item's `body_ref`), counted as `carried`. Otherwise emits `unfiled-blocks` naming each missing block ID and a pasteable copy of that block. |
| **`needs-blocks`** | `stage` in `{"DESIGN", "DECIDED"}`, `status == "accepted"`, zero unanswered questions, zero blocks in the file, and zero rows in `items` naming the file | Emits `needs-blocks` with a pasteable `item-design-<slug>` block (`seat="design"`, `paths` naming the document and the roadmaps). If ≥1 row in `items` already names the file, counted as `carried`. |
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
- **No tillr metadata in trunk commits:** `tillr-land` stamps no `Tillr-Item:` trailer, `sync` scans no `git log`, and post-worker block retirement removes finished `<!-- inert copy of a tillr directive: item -->` directives before gating so a cold store rebuilds only open work (`D-RS3`, [`OQ-PR1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR1)).

---

## Verification anchors

| Mechanism | Regression tests |
| :--- | :--- |
| Sync over live repository documents, block grammar, per-file savepoints, and `definition-frozen` | [`tools/tillr-item/item/sync_test.go`](../../tillr-item/item/sync_test.go) (`TestSyncOverThisRepositorysDocuments`), [`tools/tillr-item/sync_test.go`](../../tillr-item/sync_test.go) |
| Scoped `--roadmap` syncs, interleaved roadmap order, `from` chaining, and `[sync] roadmaps` | [`sync_roadmap_order_test.go`](../../tillr-item/item/sync_roadmap_order_test.go) (`TestSyncRoadmapEntriesInterleaveAndPark`, `TestSyncRoadmapAppendPreservesStoredKeys`), [`sync_scope_test.go`](../../tillr-item/item/sync_scope_test.go), [`sync_from_test.go`](../../tillr-item/item/sync_from_test.go), [`tools/tillr-drain/sync_scope_test.go`](../../tillr-drain/drain/sync_scope_ext_test.go) |
| `## Icebox` / `## Exploration` parking and whole-sync unparking | [`sync_parking_test.go`](../../tillr-item/item/sync_parking_test.go) (`TestSyncParkingStoredItems`) |
| `design-without-item` detection, `first_seen` retention, and observatory waiting rows | [`design_without_item_test.go`](../../tillr-item/item/design_without_item_test.go), [`tools/tillr-view/waiting_design_test.go`](../../tillr-view/waiting_design_test.go), [`waiting-design.test.tsx`](../../tillr-view/web/tests/waiting-design.test.tsx) |
| Persisted `sync_residue` (`unshared_open`, `stale_doc_blocks`) and `tillr-view status` | [`sync_residue_test.go`](../../tillr-item/item/sync_residue_test.go), [`tools/tillr-view/sync_residue_test.go`](../../tillr-view/sync_residue_test.go) |
| Post-worker block retirement and squash message preservation | [`retire_test.go`](../../tillr-item/item/retire_test.go), [`tools/tillr-drain/drain/retire_test.go`](../../tillr-drain/drain/retire_test.go) |
| Shared `vantage.VantageQuestions` reader and `tillr-item show` question states | [`tools/tillr/vantage/questions_test.go`](../../tillr/vantage/questions_test.go), [`tools/tillr-item/questions_test.go`](../../tillr-item/questions_test.go) |
| Read-only `tillr item reconcile` partition and CLI exit codes | [`reconcile_test.go`](../../tillr-item/item/reconcile_test.go), [`tools/tillr-item/reconcile_test.go`](../../tillr-item/reconcile_test.go) |
| Read-only `tillr store inventory` byte, row, and ref accounting | [`tools/tillr-store/inventory_test.go`](../../tillr-store/inventory_test.go) |
| Backend removal of Git Trace2 environment variables | [`tools/tillr-run/backends_trace_env_test.go`](../../tillr-run/backends_trace_env_test.go) |

---

## Current values

| Value | Setting / literal | Defined in |
| :--- | :--- | :--- |
| Default `[sync] roadmaps` setting | `[]` (empty slice: sync reads all tracked `docs/**/*.md` and discovers all roadmaps) | `SyncSettings` in [`tools/tillr/settings/settings.go`](../../tillr/settings/settings.go) |
| Discovered roadmap paths (when unscoped) | `docs/roadmap.md` first, then lexicographic `docs/<dir>/roadmap.md` | `isRoadmapPath`, `roadmapOrder` in [`tools/tillr-item/item/sync_roadmap.go`](../../tillr-item/item/sync_roadmap.go) |
| Parked roadmap section prefixes | Headings starting with `"Icebox"` or `"Exploration"`; scope exclusions starting with `"What this file does not cover"` | `isParkedHeading`, `parkedDocuments` (`sync.go`), `isIgnoredLinkHeading` (`reconcile.go`, `design_without_item.go`) |
| Item ID regex and allowed directive attributes | `^item-[a-z0-9-]+$`; `id`, `paths`, `blocked_by`, `seat`, `prose`, `workflow`, `claims`, `judge`, `from`, `express` | `ItemIDPattern`, `knownAttributes` in [`tools/tillr-item/docshare/docshare.go`](../../tillr-item/docshare/docshare.go) |
| Default Vantage index command and timeout | `uvx vantage-check index --format json`; `30s` (`defaultIndexTimeout`) | `DefaultCommand`, `defaultIndexTimeout` in [`tools/tillr/vantage/vantage.go`](../../tillr/vantage/vantage.go) |
| Sync-owned SQLite tables and record kinds | `design_without_item`, `sync_residue`; `item.sync`, `item.reconcile`, `store.inventory` | `schema.go`, `sync_residue.go`, `main.go`, `reconcile.go`, `tools/tillr-store/inventory.go` |
| Loop block-retirement commit and event kind | `"chore: retire landed document block"`; `drain.retirement` | `retireDocumentBlock` in [`tools/tillr-drain/drain/retire.go`](../../tillr-drain/drain/retire.go) |
| Unset Git Trace2 variables in run backends | `GIT_TRACE2_EVENT`, `GIT_TRACE2_PARENT_SID`, `GIT_TRACE2`, `GIT_TRACE2_PERF` | [`run-pi`](../../tillr-run/backends/pi/run-pi), [`run-claude`](../../tillr-run/backends/claude/run-claude) |

---

<a id="decision-ledger"></a>

## Why it's this way

Each row is an owner ruling or a designer's decision ([`OQ-QP1`](#why-its-this-way)–[`OQ-QP4`](#why-its-this-way), `D-RS1`–`D-RS5`, `D-QP1`–`D-QP13`). The owner may overturn any designer's decision.

| ID | Decision | Status and why |
| :--- | :--- | :--- |
| OQ-QP1 | `tillr item sync` gives a place only to items with no stored `items.rank`, appending them after existing ranked items in interleaved roadmap order (`1` of roadmap A, `1` of roadmap B, …) without rank bands or re-ranking, and callers choose one roadmap, several, or all documents *(owner ruling, 2026-11-16)* | Built (`sync_roadmap.go`, `sync_scope.go`). Preserves hand-dragged queue places across syncs while giving newly imported items a deterministic place matching roadmap priority. |
| OQ-QP2 | Author slice definitions in tracked Markdown via `<!-- inert copy of a tillr directive: item id="…" paths="…" -->` comments under `### <Title>` with `- **c<N>:**` bullets *(designer's decision, 2026-11-15, amended by `D-RS1`; **overturned by owner 2026-11-18 in [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1) and 2026-11-20 in [`OQ-PR14`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR14)**)* | Currently built in `sync.go` and `retire.go`. Overturned for future work in [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md) (unbuilt), which replaces hidden in-document comments with mirrored roadmap designs and companion `<topic>-plan.md` files so human-read designs hold no build metadata. |
| OQ-QP3 | Impose no automatic age or byte eviction on `.tillr/` run artifacts; ship a read-only `tillr store inventory` first and leave retention bounds dormant until the owner reviews the inventory *(owner ruling, 2026-11-14)* | Built (`tools/tillr-store/inventory.go`); automatic eviction and `retire-epoch` remain unbuilt so run logs and failure evidence are never silently discarded behind the owner's back. |
| OQ-QP4 | Do not record per-run Git Trace2 (`git-trace2.jsonl`) *(designer's 2026-11-15 keep-on decision **overturned by owner on 2026-11-17**)* | Built (`run-pi` and `run-claude` unset `GIT_TRACE2*`; shell wrapper `proc-exec.jsonl` stays). One run recorded 918,054 git commands (~500 MB) for zero diagnostic value. |
| D-RS1 | The item block is the `<!-- inert copy of a tillr directive: item … -->` comment of [`OQ-QP2`](#why-its-this-way) under a heading plus `- **c<N>:**` bullets, with the attribute `blocked_by`, multi-line attribute whitespace allowed, fenced code blocks ignored, and no state, rank or tombstone attribute. **Rejected:** an `after=` edge alias, and `closed=` / `resolved-by=` tombstones that leak tillr state into prose. *(designer's decision; amended by the owner's [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1))* | Built ([block grammar](#block-grammar-and-attributes)). |
| D-RS2 | A stored item gets a committed block only when it passes the four-clause rule. **Rejected:** a block for every stored item through `body_ref`. *(designer's decision)* | A convention for authors; nothing enforces it ([which work gets a shared block](#which-work-gets-a-shared-block-d-rs2)). |
| D-RS3 | No commit names an item: the lander stamps no trailer, `landed` is private state, and the shared landing receipt is the deleted block. **Rejected:** trailers for a cold rebuild. The owner's [`OQ-PR1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR1) rules the same for commit messages. *(designer's decision)* | Built ([landing receipts](#landing-receipts-without-commit-trailers)). |
| D-RS4 | `tillr item sync` reads item blocks and reports the `unshared_open` and `stale_doc_blocks` residue counters. **Rejected:** hand-edited blocks with no sync reader. *(designer's decision)* | Built ([sync residue](#read-scope-per-file-savepoints-and-criterion-freeze)). |
| D-RS5 | The order is private unless the repository sets `order.record`; active runs, leases, spend, local `decisions`, `landed` rows and unpromoted draft slices stay private to `.tillr/`. **Rejected:** committed `.tillr/` snapshots, and one repository-wide rank order. *(designer's decision)* | In force ([what stays private](#what-stays-private-d-rs5)). |
| D-QP1 | Keep no committed JSON queue file and no `tillr item seed` *(designer's decision, 2026-11-14; settled in [`OQ-TC1`](../design/tillr-convergence.md#decision-ledger); under review in [`OQ-PR10`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR10))* | Built (`tillr-item seed` exits `2`). A committed queue file causes multi-lane JSON merge conflicts and dual-source drift; work without a design enters via `tillr-item request` or `idea`. |
| D-QP2 | Keep `drain.Plan` a fast read over `.tillr/state.db` and project tracked `docs/**/*.md` into `.tillr/state.db` via `tillr item sync` at startup, after trunk advances, and on demand, sharing one `vantage-check index` reader across drain, view, and `item show` *(designer's decision, 2026-11-14; trailer scan dropped by `D-RS3` / [`OQ-PR1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR1); block source governed by [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1))* | Built (`sync.go`, `sync_residue.go`, `tools/tillr/vantage/vantage.go`, `tools/tillr-view/status.go`). Keeps planner passes pure and fast while making committed documents the shared queue definition. |
| D-QP3 | Declare child build items in tracked Markdown synced after landing rather than applying untracked `.tillr/loop/<run_id>/derived-items.json` manifests *(designer's decision, 2026-11-14, superseding `D-IP6` in [`tillr-idea-pipeline.md`](../design/tillr-idea-pipeline.md#61-settled-decisions); moving to `<topic>-plan.md` under [`OQ-QM1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#OQ-QM1))* | Built (`sync_from.go`, [`observatory-actions.md`](observatory-actions.md#idea-intake-and-the-design-turn)). Ensures child items enter the queue only when their parent commit lands on `main`. |
| D-QP4 | Bound the uncommitted intake bootstrap gap between saving raw idea words and landing the initial design commit *(designer's decision, 2026-11-14)* | **Not built as specified.** `tillr-item idea` saves `<store-dir>/intake/<id>.txt`, instructs stub-on-ambiguity, and `tillr-view` marks the Item page `not yet in the tree`, whereas the proposed `uncommitted intake: N` header badge, `15m`/`45m` `bootstrap-overdue` escalation, and `retire-epoch` lock are not built. |
| D-QP5 | Route off-spine documents under `## Icebox` / `## Exploration` as parked (`not-started`) and require every `blocked` item to name a waiter and reason *(designer's decision, 2026-11-14; waiter rule built via `D-JL8`; **amended for scoped sync by `D-QP13`**; **parking state overturned for future build by owner 2026-11-18 in `D-QM2` of [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#decision-ledger)**; `Exploration` switch under review in [`OQ-PR9`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR9))* | Built (`sync.go`, `sync_parking_test.go`). `not-started` parking applies on unscoped whole syncs while scoped `roadmaps` syncs skip parked blocks and list them in `skipped_blocks` (`D-QP13`); once [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md) is built, Icebox designs and tasks will load as `blocked` (`reason: icebox`). |
| D-QP6 | Withdrawn: no `--shadow` dual read of a JSON queue file beside the documents *(designer's decision, 2026-11-14; replaced by [`OQ-TC1`](../design/tillr-convergence.md#decision-ledger))* | Not built. Tracked documents are the only definitions `tillr item sync` reads. |
| D-QP7 | Treat `.tillr/` as an epoch-scoped execution record rather than a permanent Git archive, deferring `tillr store retire-epoch`, byte ceilings, and ref pruning until after `tillr store inventory` review *(designer's decision, 2026-11-14, constrained by [`OQ-QP3`](#why-its-this-way))* | `tillr store inventory` is built (`tools/tillr-store/inventory.go`); `tillr store retire-epoch`, byte caps, and automatic ref pruning are not built. |
| D-QP8 | Provide `tillr item reconcile` to partition `vantage-check index` documents into `unstaged`, `evergreen`, `built`, `unfiled-blocks`, `needs-blocks`, and fourth-case holds (`question-open`, `needs-design`, `unruled`) *(designer's decision, 2026-11-15, amended to read-only by `D-QP11`; under review in [`OQ-PR9`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR9); **queued for retirement by `D-QM15` in [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md#decision-ledger)**)* | Built as a read-only CLI report (`tools/tillr-item/item/reconcile.go`). Surfaces accepted designs lacking item blocks and built designs awaiting reference graduation without mutating the store. |
| D-QP9 | Implement [`OQ-QP1`](#why-its-this-way) with sparse lexicographic `items.rank` keys assigned on first import (and once for pre-existing unranked ordered rows), interleaving roadmaps one entry at a time and supporting `--roadmap` / `[sync] roadmaps` *(designer's decision, 2026-11-16; **scoped read and import filter amended by `D-QP13`**)* | Built (`sync_roadmap.go`, `sync_scope.go`, `place.go`). Avoids rank-band collisions and preserves every existing rank key; `D-QP13` extends `[sync] roadmaps` to `tillr-item sync` CLI fallback, limits scoped imports to unparked selected-roadmap entries, and inventories skipped blocks across `docs/**/*.md`. |
| D-QP10 | Move stored `in-queue` items to unranked `not-started` when their document is parked and restore `not-started` items to `in-queue` at the end of the order on a whole sync, leaving inline roadmap blocks unparked *(designer's decision, 2026-11-16; **amended for scoped sync by `D-QP13`**; amended for future build by `D-QM2`; under review in [`OQ-PR9`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR9))* | Built (`sync.go`, `sync_parking_test.go`). Parking and unparking transitions run on unscoped whole syncs, while scoped `roadmaps` syncs skip parked blocks (`skipped_blocks`) and leave unread/skipped store rows untouched (`D-QP13`). |
| D-QP11 | Make `tillr item reconcile` a standalone, read-only report (`OpenRead`) printing ready-to-paste blocks, while `tillr item sync` warns on uncarried roadmap designs via `design-without-item` *(designer's decision, 2026-11-16; under review in [`OQ-PR9`](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/decision-provenance-review.md#OQ-PR9); queued for retirement in `D-QM15`)* | Built (`reconcile.go`, `design_without_item.go`). Keeps `sync` free of synthetic item invention while surfacing unfiled roadmap designs on the waiting board. |
| D-QP12 | Share one `vantage.VantageQuestions` reader in `tools/tillr/vantage/` across `tillr-drain`, `tillr-view`, `tillr-item show`, and `tillr-item reconcile`, retiring hand-rolled Markdown question parsers *(designer's decision, 2026-11-16)* | Built (`tools/tillr/vantage/vantage.go`, `tools/tillr-item/item/questions.go`). Guarantees identical `questions-ok`, `questions-unknown`, and `design-doc-missing` answers across every CLI and HTTP surface. |
| D-QP13 | *(Designer's decision, 2026-11-23; not ruled by the owner, who may overturn the mechanism.)* In `tillr-item sync`, read `[sync] roadmaps` from `<root>/.tillr.toml` via `ReadRoadmapDocuments` whenever `--roadmap` is omitted (`--roadmap` overrides `.tillr.toml`, and invalid `.tillr.toml` or roadmap paths exit `2` with zero store writes); when `roadmaps` is set (`Partial: true`), validate selected roadmaps and their direct links against symlink traversal, read all tracked regular `docs/**/*.md` files, import only blocks sitting in an unparked non-exclusion section of a selected roadmap or in a non-roadmap, non-parked document (`!parkedDocuments(docs, selected)[target]`) linked from an unparked non-exclusion section (`"Icebox"`, `"Exploration"`, and `"What this page/file does not cover"` excluded, with no bullet-vs-prose link heuristic and no edits to HTML item comments), leave pre-existing store rows for skipped IDs untouched without emitting out-of-scope `Findings` / `Warnings` / `SkippedFiles`, keep `Documents` and `Blocks` counting the in-scope set (`unshared_open` and `stale_doc_blocks` remaining `null` / not computed), and report every skipped block deterministically in `(file, line)` order via `SkippedBlocks` (`json:"skipped_blocks"`, `{file, item_id}`) and `SkippedCount` (`json:"skipped_count"`) plus `skipped: <file> <item_id>` lines in `SyncText` (amending `D-QP5`, `D-QP9`, and `D-QP10` for HTML comment sync while leaving `D-QM2` intact for [`tillr-queue-mirrors-designs.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-queue-mirrors-designs.md)). | Built (`sync.go`, `sync_scope.go`, `sync_roadmap.go`, `design_without_item.go`, `tools/tillr-drain/drain/sync.go`, `sync_scope_test.go`; [Scoped roadmaps import filter and skipped-block report](#scoped-roadmaps-import-filter-and-skipped-block-report-d-qp13)). |
