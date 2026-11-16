---
title: "Shared Documents Carry the Work; Private Queue State Stays Private"
status: accepted
stage: DECIDED
updated: 2026-11-15
tags:
  - tillr
  - queue
  - persistence
  - roadmap
  - design-ruling
summary: "Settles the human-shop boundary between shared Markdown documents and private .tillr/ workbenches: adopts an amended HTML item-block directive with zero tombstone or rank state, classifies the 29 open queue rows into 7 shared blocks plus 1 prose diagnosis and 21 private/non-item omissions, deletes Tillr-Item commit trailers in favour of document-level block retirement, and sequences the three-commit path from immediate rescue to mechanical sync."
next: "Surface unshared_open and stale_doc_blocks in tillr-view status; the trailer deletion and tillr-item sync are built."
depends-on:
  - tillr-queue-persistence.md
  - momentum-principle.md
  - ../pi/pile-roadmap-boundary.md
vantage:
  status-chip: true
---

# Shared Documents Carry the Work; Private Queue State Stays Private

**Status:** 2026-11-15. Ruled and executable today; nothing below waits on [`OQ-QP1`](tillr-queue-persistence.md#91-decision-ledger), [`OQ-QP2`](tillr-queue-persistence.md#91-decision-ledger), or [a closed question](tillr-queue-persistence.md).

**Needs your ruling:** None.

**Survey of what already exists in the tree:**
- **Markdown item intake (`tools/tillr-item/`):** Nothing exists. `faceList` in `tools/tillr-item/main.go` exposes only `apply, seed, show, state, decision, mail, grant or verdict`. `tools/tillr-item/item/seed.go` (`Seed`, `planSeed`, `sameDefinition`) reads JSON only and refuses changed definitions; `tools/tillr-item/item/questions.go` (`ReadQuestions`) and `tools/tillr-drain/vantage/vantage.go` (`VantageQuestions`) read open questions only. Zero `<!-- tillr: item` blocks exist outside the example in [`tillr-queue-persistence.md`](tillr-queue-persistence.md).
- **Commit trailers (`tools/tillr-land/`):** `tools/tillr-land/land/trunk.go` (`(*Trunk).Squash`) appends `Tillr-Item: <itemID>` on squash, pinned by `tools/tillr-land/land/trunk_test.go` and `tools/tillr-land/main_test.go`. Nothing in `tools/` reads that trailer, and only 14 of 72 `landed` rows carry it on `main`.
- **Gate enforcement (`tools/fork-check.py`):** `legacy_seed_findings` (`seed-freeze`, step 8b) already enforces [`tillr-queue-persistence.md`](tillr-queue-persistence.md) `D-QP1` on `config/tillr/seed-items.json` (net-new IDs and field edits refused; pure removals allowed, shrinking the file from 79 to 64 rows). Committing a second JSON store dump reinstated `D-QP1`'s rejected alternative under a new filename and is reverted as a premise of this ruling.

---

## 1. Shared Item Block Shape

**Ruling (`D-RS1`):** Adopt [`OQ-QP2`](tillr-queue-persistence.md#91-decision-ledger) **Option A with three amendments**, effective immediately:

1. **Attribute name `blocked_by`, not `after`:** Matches `tools/tillr-item/item/apply.go`'s `blocked_by` field directly with zero vocabulary translation.
2. **Open work only — zero state, rank, or tombstone attributes:** `rank`, `state`, `closed`, and `resolved-by` are forbidden in the directive, amending [`tillr-queue-persistence.md`](tillr-queue-persistence.md) Sections 3.2 and 8.2. When a slice lands or is dropped, its `<!-- tillr: item ... -->` comment is deleted from the document (and any architectural rejection becomes a `D-*` row in `## Decision Ledger`, never a dead item block).
3. **Multi-line attribute wrapping allowed; fenced code blocks ignored:** Whitespace and newlines may separate attributes inside `<!-- tillr: item ... -->` so multi-path fences stay hand-editable, and any scanner must skip fenced code blocks (` ``` ` / `~~~`).

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

*(Single-line `<!-- tillr: item id="..." paths="..." -->` is equivalent. Omit `blocked_by` when empty. For a standalone chore or defect with no design document, place the comment and `- **c1:**` bullets indented beneath `- **<Title>**` on the domain roadmap.)*

**Amendment (2026-11-15): an optional `seat` attribute.** A directive may also carry `seat="<seat>"`, naming one of the item model's seats (`design`, `build`, `review` or `scout`, the closed set in `tools/tillr-item/item/seat.go`). Omitted, the item asks for no seat, which is the default a seeded row without one gets; an unknown value is a finding like any other invalid attribute. The allowed attribute set is therefore `id`, `paths`, `blocked_by` and `seat`. Reason: a design item has no other way to ask for the design seat from a document.

**Exact field mapping (and nothing more):**
- `item_id`: `id` attribute (required; `^item-[a-z0-9-]+$`).
- `title`: text of the enclosing `### ` heading (or leading `**<Title>**` of the enclosing roadmap bullet).
- `allowed_paths`: comma-split, trimmed `paths` attribute (required, non-empty, repo-relative prefixes present on `main`, no globs).
- `blocked_by`: comma-split, trimmed `blocked_by` attribute (optional; defaults to `[]`).
- `seat`: the `seat` attribute (optional, per the amendment above; defaults to no seat).
- `criteria`: ordered `- **c<N>:** <text>` bullets in the same section up to the next heading or `<!-- tillr: item` directive (`criterion_id = "c<N>"`, `state = ""`; at least `c1` required).
- `body_ref` and `design_doc` are derived from the containing file path and heading slug, never written in the directive.

---

## 2. Disposition of the 29 Open Rows

**Ruling (`D-RS2`):** `body_ref` is provenance, not an automatic home. Over the 29 open rows, commit a `<!-- tillr: item -->` block **if and only if all four clauses hold**:

1. **Agent-executable:** `c1` is a repository change a jailed lane can verify, not `"Owner decision recorded in <doc>..."` (`10` rows excluded).
2. **Accepted specification:** `design_doc` (or `body_ref` when `design_doc` is empty) carries `status: accepted`, or the row is a standalone code/gate defect with no unsettled design (`9` remaining rows excluded because their governing doc is `status: in-review` or an unstaged handoff note).
3. **Trunk-stageable fence:** every prefix in `allowed_paths` resolves on `main` without requiring `.forks/lanes/<lane>/<fork>` binding (`5` pi-fork rows excluded until fork-block staging exists).
4. **Unfrozen, non-conflicting criteria:** no criterion is frozen `not-done` on a specification conflict against a settled ruling.

**Where qualifying blocks live:** inside their `accepted` plan/design document when one exists; otherwise inline on [the tillr roadmap](../tillr/roadmap.md) or [the pi roadmap](../pi/roadmap.md) — never inside an archive-triage report such as [`tillr-seed-triage.md`](tillr-seed-triage.md).

### Exceptions — The Only Three Rows the Rule Gets Wrong

The four-clause rule accepts **5** rows (`item-bar-ironbar-config-v2`, `item-view-board-timeline-api`, `item-view-board-timeline-ui`, `item-gate-substrate-go-landing-tier`, `item-trace-incremental-ingest`) and rejects **24**. It gets **3** of the 24 wrong:

1. **`item-store-crash-recovery` (override → commit block on [`docs/tillr/roadmap.md`](../tillr/roadmap.md)):** Clause 2 rejects it only because `design_doc` pointed to `in-review` [`tillr-observatory.md`](tillr-observatory.md) (whose prose falsely claimed SQLite WAL was enabled). The bug (`SQLITE_READONLY_ROLLBACK` error 776 locking out read-only faces after a killed writer) is live on `main`, depends on zero open questions, and exists in no committed file.
2. **`item-gate-observatory-dev-check` (override → commit block on [`docs/tillr/roadmap.md`](../tillr/roadmap.md)):** Clause 2 rejects it for the same false `design_doc` pointer. `tests/observatory-dev.test.py` already ships on `main`, runs in 0.9s, and wiring it into `tools/fork-check.py` depends on zero open questions.
3. **`item-output-cap-gate-wireid` (partial override → commit prose diagnosis only in [`docs/pi/connection-error-turn-death-2026-10-18.md`](../pi/connection-error-turn-death-2026-10-18.md)):** Clause 3 rightly withholds a dispatchable `<!-- tillr: item -->` block because `packages/coding-agent/...` is not on `main` and `yolo-packs/pi-fork/` alone trips `host-boundary-violation` on patch export, while the item remains in `config/tillr/seed-items.json`. However, its 2026-11-14 root cause (`_modelForMessage` comparing `model.id` against wire ID `message.model`) is absent from [`connection-error-turn-death-2026-10-18.md`](../pi/connection-error-turn-death-2026-10-18.md) and must be recorded there in prose so the diagnosis survives disappearance.

### Why Not Committing Blocks for the Other 22 Rows Is the Principle Operating

- **10 owner-ruling and host-action rows** (`item-claude-whale-counterpart`, `item-host-render-pi-roadmap`, the 6 `item-oq-*` rows, `item-ruling-approver-job`, `item-ruling-blocked-resume`): their shared truth already lives in Vantage `OQ-*` directives, [`docs/open-asks.md`](../open-asks.md), and [`docs/pi/roadmap.md`](../pi/roadmap.md). Duplicating questions as item blocks violates `D-UT4` and risks jailed lanes mechanically stamping `c1` done.
- **5 pre-cut slices on `in-review` drafts** (`item-tillr-scheduled-items`, `item-view-scheduled-due`, `item-tillr-goal-checkin-graph`, `item-retire-steering-timer` under [`tillr-goal-monitor.md`](tillr-goal-monitor.md), held on [`OQ-GM1`](tillr-goal-monitor.md#OQ-GM1)–[`OQ-GM2`](tillr-goal-monitor.md#OQ-GM2); plus `item-design-doc-reconcile-frontier-pass` on [`tillr-queue-persistence.md`](tillr-queue-persistence.md)): pre-slicing an unaccepted draft or tracking a coordinator's review pass is private workbench staging; the design prose in [`tillr-goal-monitor.md`](tillr-goal-monitor.md) already preserves the architecture.
- **3 conflicted or container rows** (`item-or-routing` whose substance is already on `main` and whose grep-count criterion contradicts pinned upstream routing in [`pi-pile-recovery-2026-11-13.md`](pi-pile-recovery-2026-11-13.md); `item-seat-bounds-wiring` which collides with settled test `worker-tool-budget.test.mjs` outside [`seat-profiles.md`](seat-profiles.md)'s fence; `item-pi-ui-latency-campaign` which wraps open-ended profiling notes in [`live-ui-performance-handoff.md`](../pi/live-ui-performance-handoff.md)): enshrining broken criteria or umbrella containers in git turns local queue debris into shop mandates.
- **4 unstageable pi-fork rows** (`item-seed-01`, `item-seed-03`, `item-seed-05`, `item-output-cap-gate-wireid`): already carried in shrinking `config/tillr/seed-items.json` until fork-aware staging lands; authoring unstageable blocks in Markdown today would break cold sync invariants.

---

## 3. Landing Receipts Without Commit Trailers

**Ruling (`D-RS3`):** Commits may not name Tillr items. This supersedes [`tillr-queue-persistence.md`](tillr-queue-persistence.md) Section 3.2 item 4, Section 8.2 (`landed` row), and `D-QP2`'s trailer scan:

1. **Stop stamping `Tillr-Item:`:** Delete the `Tillr-Item:` append in `tools/tillr-land/land/trunk.go` (`(*Trunk).Squash`), update `tools/tillr-land/land/landing.go`, `trunk_test.go`, and `main_test.go`, and forbid `Tillr-Item:` trailers or `item-*` IDs in `tools/tillr-run` worker commit instructions. Existing history on `main` is not rewritten and nothing reads the 14 historical trailers.
2. **What the landing receipt is:**
   - **Privately (inside one developer's `.tillr/state.db`):** `items.state = 'landed'` and `landings.trunk_after` suppress re-dispatch during that developer's active epoch. A cold store on a fresh machine reconstructs **zero** `landed` rows.
   - **In the shared repository:** **absence of the open `<!-- tillr: item -->` block (or `stage: BUILT` / `CURRENT` on the document) is the receipt.** When a slice's `paths` already includes its owning `.md` file, the worker deletes the `<!-- tillr: item ... -->` comment in the landing commit itself (zero disappearance window). When sibling slices of one umbrella doc omit the `.md` file from `paths` so parallel lanes stay path-disjoint, the finished blocks are stripped (or the doc moves to `stage: BUILT`) in a short doc-reconcile commit once the wave settles.
3. **What a fresh machine loses, and why the loss is acceptable:**
   - **Lost permanently:** the 72 historical `landed` rows, item-to-SHA attribution (4 of the 72 were already unrecoverable except by manual `git log` inspection, and 58 lacked trailers), retry counts, gate/judge envelopes, and spend. `git log`, `git blame`, and the gate suites already carry every fact the shop needs about landed code.
   - **Bounded disappearance cost:** if a developer lands a parallel slice's code on `main` and vanishes before stripping its `<!-- tillr: item -->` comment from the shared plan doc, a fresh machine will sync that slice as open once, run against `HEAD` where `c1..cN` already pass, and retire the stale block in a doc-only commit. One idempotent check on rare mid-wave disappearance is strictly cheaper than polluting every repository commit with private queue IDs.

---

## 4. The Momentum Mechanism and the First Commit

**Ruling (`D-RS4`):** Spend the **narrowed 7-block hand edit today** (not 29), **and** build the minimal mechanical slice of `D-QP2` (`tillr-item sync` + two residue counters) immediately behind it.

- **Why both are required:** Hand-authoring the 7 blocks today rescues the 6 unseeded shared items that exist nowhere in git before the machine switch, with zero wait on Go code. Without `tillr-item sync` behind it, however, `<!-- tillr: item -->` blocks remain inert comments to `tools/tillr-item/` and tomorrow's filings will again go straight to SQLite via `tillr-item apply`.
- **Smallest mechanical slice (`tillr-item sync`):** ~150 lines in `tools/tillr-item/item/sync.go` (`SchemaVersion = 5` unchanged, zero [`OQ-QP1`](tillr-queue-persistence.md#91-decision-ledger) cross-roadmap rank interleaving, zero `git log` scan). It scans tracked `docs/**/*.md` outside fenced code blocks, materializes missing blocks via `item.Apply` (`not-started` if linked only under `## Icebox` / `## Exploration` in the Vantage index, `in-queue` otherwise, never overwriting local `rank` or `landed`), runs automatically at `tillr-drain` preflight, and prints two live momentum counters (also surfaced in `tillr-view status`):
  - **`unshared_open: K (oldest Xm)`** — non-terminal store items absent from both `docs/**/*.md` blocks and legacy `seed-items.json` (making private residue small and visible).
  - **`stale_doc_blocks: M`** — `docs/**/*.md` blocks whose `item_id` is already `landed` in local `.tillr/state.db` (prompting immediate block removal so the `[code_landed, block_retired]` window shrinks to zero at the end of each wave).

### Commit 1 (Smallest First Commit — Foreground, Immediately After Reverting the Snapshot)

**Files, in authoring order:**
1. [`docs/design/status-bar-plan.md`](status-bar-plan.md) — add the `item-bar-ironbar-config-v2` block (`c1..c3`, including `tools/fork-check.py` and `tests/test_waybar_cpu.py` in `paths`) under Task 4; [`docs/roadmap.md`](../roadmap.md) `## Icebox` already keeps it parked.
2. `config/tillr/seed-items.json` — remove `item-bar-ironbar-config` and `item-bar-ironbar-config-v2` (pure deletion allowed by `seed-freeze`).
3. [`docs/design/observatory-design.md`](observatory-design.md) — add blocks for `item-view-board-timeline-api` and `item-view-board-timeline-ui` (`blocked_by="item-view-board-timeline-api"`).
4. [`docs/design/landing-velocity-plan.md`](landing-velocity-plan.md) — add the block for `item-gate-substrate-go-landing-tier`.
5. [`docs/tillr/roadmap.md`](../tillr/roadmap.md) — add standalone defect blocks for `item-store-crash-recovery`, `item-gate-observatory-dev-check`, and `item-trace-incremental-ingest`.
6. [`docs/pi/connection-error-turn-death-2026-10-18.md`](../pi/connection-error-turn-death-2026-10-18.md) — record the `_modelForMessage` `wireId` vs `model.id` root cause in prose (no item block).

**What a reviewer checks:**
- Every `<!-- tillr: item -->` directive carries only `id`, `paths`, and optional `blocked_by`, and every path prefix in `paths` exists on `main`.
- `config/tillr/seed-items.json` shrinks by exactly 2 entries with zero edits to remaining entries.
- `uvx vantage-check` and `just done` (`documents`, `citations`, `scratch-citations`, `design-citations`, `seed-freeze`) pass.
- Commit message cites the updated documents and contains **no** `Tillr-Item:` trailer and **no** `item-*` ID.

**What is deliberately NOT in Commit 1:**
- Any of the 22 excluded rows as item blocks; any `.tillr/` export or `.decisions.jsonl`; any `rank` or `criteria[].state` field; and any Go edits.
- **Commit 2 (before restarting `tillr-drain`):** remove `Tillr-Item:` stamping in `tools/tillr-land/land/trunk.go`, `landing.go`, `trunk_test.go`, and `main_test.go`, plus worker commit-message instructions in `tools/tillr-run/`.
- **Commit 3:** add `tools/tillr-item/item/sync.go` + `sync_test.go` (asserting the 7 live blocks on `main`), wire `sync` into `tillr-drain` preflight, and surface `unshared_open` and `stale_doc_blocks`.

---

## 5. What Is Deliberately Left Private

**Ruling (`D-RS5`):** Two developers sharing this repository share documents, tests, and code — never a live queue. The following facts stay solely in `.tillr/` and are intentionally lost when a workspace disappears or moves machines:

| Private Surface | Why Leaving It Out of Git Is Right |
| :--- | :--- |
| **Personal priority (`items.rank` overrides)** | Two developers rank work differently; roadmaps separate active work from `## Icebox` / `## Exploration`, and each developer orders their own `.tillr/state.db` without merge conflicts or [`OQ-QP1`](tillr-queue-persistence.md#91-decision-ledger). |
| **In-flight execution (`lanes`, leases, `runs`, retries, `usage_cost_usd`)** | Ephemeral workbench state of one machine's processes; meaningless on a second developer's machine. |
| **Local `decisions` holds & frozen `not-done` flags** | Records why one developer paused a broken item locally; if the finding is architectural, it belongs in a design doc's prose or `Decision Ledger`, not as a serialized `.decisions.jsonl`. |
| **Historical `landed` rows & item-to-commit joins** | Once code and updated docs are on `main`, the ephemeral `item-*` handle has done its job; git history documents the change in repository terms. |
| **Unpromoted scratch ideas & pre-cut `in-review` slices** | Ideas not yet distilled into a `SKETCH`/`DESIGN` doc (`unshared_open`) and slices of unaccepted designs are unfinished personal thoughts; momentum shrinks the window, and residue lost inside it is accepted. |

---

## 6. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-RS1` | Adopt [`OQ-QP2`](tillr-queue-persistence.md#91-decision-ledger) Option A amended: `<!-- tillr: item id="..." paths="..." [blocked_by="..."] -->` under `### <Title>` plus `- **c1:**` bullets, multi-line attribute whitespace allowed, fenced code blocks ignored, and zero state/rank/tombstone attributes; finished or dropped blocks are deleted from the doc. **Rejected:** `after=` edge alias (unnecessary mismatch with `apply.go`), and `closed=` / `resolved-by=` Markdown tombstones (leaks permanent tillr state into prose). | 2026-11-15 | [Section 1](#1-shared-item-block-shape) | — |
| `D-RS2` | Commit item blocks for 7 of the 29 open rows (5 passing the 4-clause accepted/executable/trunk-stageable/unfrozen rule plus overrides `item-store-crash-recovery` and `item-gate-observatory-dev-check`), record `item-output-cap-gate-wireid` as prose in [`connection-error-turn-death-2026-10-18.md`](../pi/connection-error-turn-death-2026-10-18.md), and omit blocks for the remaining 21. **Rejected:** committing blocks for all 29 via `body_ref` (duplicates 10 open questions, freezes 5 `in-review` draft slices, enshrines 3 conflicted/campaign rows, and adds 4 unstageable fork fences). | 2026-11-15 | [Section 2](#2-disposition-of-the-29-open-rows) | — |
| `D-RS3` | Delete `Tillr-Item:` commit-trailer stamping in `tools/tillr-land/land/trunk.go` and forbid item IDs in commits; `landed` is private epoch state (cold stores hold zero `landed` rows), and the shared landing receipt is deleting the `<!-- tillr: item -->` block (or advancing `stage: BUILT`). **Rejected:** keeping `Tillr-Item:` trailers for cold rebuild (violates the owner's no-tillr-in-commits rule and covered only 14 of 72 landed rows). | 2026-11-15 | [Section 3](#3-landing-receipts-without-commit-trailers) | ✅ (`Trunk.Squash` in `tools/tillr-land/land/trunk.go` takes no item id and writes the head's message unchanged; the worker brief in `tools/tillr-run/backends/brief-template.md` forbids an item id or trailer in the commit message) |
| `D-RS4` | Spend the 7-block Markdown commit immediately (Commit 1), delete `Tillr-Item:` stamping before the next drain run (Commit 2), and build a minimal `tillr-item sync` block reader plus `unshared_open` and `stale_doc_blocks` residue counters at `SchemaVersion = 5` (Commit 3). **Rejected:** waiting for full `D-QP2` / [`OQ-QP1`](tillr-queue-persistence.md#91-decision-ledger) before rescuing the 7 rows, or doing a one-time hand edit with no sync reader and letting `tillr-item apply` reopen the gap. | 2026-11-15 | [Section 4](#4-the-momentum-mechanism-and-the-first-commit) | Partial (`tillr-item sync` in `tools/tillr-item/item/sync.go` with `unshared_open` and `stale_doc_blocks`, run by `tillr-drain loop` before its first plan; the counters are not yet in `tillr-view status`, and `unshared_open` counts against document blocks only, since sync does not read `seed-items.json`) |
| `D-RS5` | Leave `rank` priority order, active runs/leases/spend, local `decisions`, historical `landed` rows, and unpromoted draft slices strictly private to `.tillr/`, bypassing [`OQ-QP1`](tillr-queue-persistence.md#91-decision-ledger) for cold sync. **Rejected:** committing `.tillr/` JSON/JSONL snapshots or enforcing one repository-wide developer rank order (violates `D-QP1` and the human-shop contract). | 2026-11-15 | [Section 5](#5-what-is-deliberately-left-private) | — |