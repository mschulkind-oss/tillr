---
status: in-review
stage: DESIGN
updated: 2026-11-24
tags: [tillr, tillr-watch, design-docs, landing, conflict-repair, gate]
summary: Standalone per-record files under docs/design/tillr-watch/ let concurrent design-seat additions of tillr-watch decision records three-way merge without touching shared lines in docs/design/tillr-watch.md, eliminating cascading 29-minute squash-conflict repairs without ordering items or adding retry counts, time limits, or fallbacks.
next: Build the single follow-up check in tools/gate/checks/change_common.py (with unit tests in tools/gate/checks/change_checks_test.py) so documents refuses inline D-TW* additions to docs/design/tillr-watch.md or edits to docs/design/tillr-watch.md alongside a new file under docs/design/tillr-watch/.
depends-on:
  - ../tillr-watch.md
  - ../landing-principles.md#2-re-application-preserves-the-change-not-the-patch
  - ../landing-principles.md#3-gate-what-will-be-the-tip-once
  - ../landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it
  - ../landing-principles.md#15-land-the-smallest-functional-change
  - ../../tillr/decision-provenance-review.md#decision-ledger
related:
  - ../tillr-utility-contracts/tillr-watch.md
  - ../operator-decisions-inside-tillr.md
  - ../tillr-judge-and-lifecycle.md
vantage:
  status-chip: true
---

# `tillr-watch` Decision Record Placement: Standalone Per-Record Files

**Status:** Design specification (`stage: DESIGN`). Not yet built.
**Needs your ruling:** None.
**Awaits your review:** the designer's decision [`D-TW-DR1`](#D-TW-DR1) in [Section 6.2](#62-designer-decision-from-operator-request), written from the 2026-11-24 operator request; the owner has not ruled on it and may overturn it.

---

## 1. Saved Request, the 2026-11-24 Measurement, and Scope

### 1.1 The 2026-11-24 request and measurement

This record answers the operator's 2026-11-24 request on the `design` seat (quoted verbatim from the filed request record, which is an operator filing rather than a direct owner statement):

> On 2026-11-24 between 07:27 and 08:45 ET, the lander recorded eight squash conflicts on `docs/design/tillr-watch.md` (`2,637` lines: `"CONFLICT (content): Merge conflict in docs/design/tillr-watch.md"`), each answered by a conflict-fix run on the design seat. One conflict-fix run (`08:14:47` to `08:43:51 ET`, on the fixer-stays-queued alert item) took 29 minutes and 10.0 million tokens, about 21 minutes of it with no command running and 90 seconds in commands. Its base was taken at `08:14:42`; another decision record for `docs/design/tillr-watch.md` landed at `08:14:51`, nine seconds later, so the finished repair conflicted again at `08:44:51` and began a second 29-minute-class repair while a second item's repair (begun at `08:14:43`) was in the same position. `docs/design/landing-principles.md` states that a mechanism whose cost rises with landing frequency points the wrong way, and the owner ruled that same-file work is not ordered with dependencies and that the drain's own conflict repair is the path; this item does not change that.
>
> The one thing to decide and record: how two design-seat items that each add a decision record for `tillr-watch` can both land without a merge conflict between them (for example where a new record is placed so that two additions do not touch the same lines), stated as a decision record with the measurement above and the one behavior of the follow-up item. Do not order items, do not add a retry count or a time limit, and do not add a fallback.
>
> Change nothing else. Out of scope: the conflict-fix code, the lander, other design documents' layout, existing decision records' text. A gap on one of those is not a reason to refuse this item; list it in the candidate note.

Provenance note (`DesignProvenanceRule` and [`OQ-PR21`](../../tillr/decision-provenance-review.md#decision-ledger)): the request's summary that same-file work is not ordered with dependencies and that the drain's own conflict repair is the path is carried here as a boundary of the filed operator request rather than presented as a newly quoted owner statement; verbatim dated owner rulings from the repository's ledgers are quoted separately in [Section 6.1](#61-inherited-owner-rulings-verbatim-quotes).

### 1.2 Candidate note on out-of-scope gaps

Per the request's scope boundary, this commit adds only `docs/design/tillr-watch/decision-record-files.md` and leaves four adjacent areas untouched:

1. **Conflict-fix staging and execution ([`../../../tools/tillr-drain/drain/conflict.go`](../../../tools/tillr-drain/drain/conflict.go), [`../../../tools/tillr-drain/drain/stage.go`](../../../tools/tillr-drain/drain/stage.go), [`../../../tools/tillr-drain/drain/loop.go`](../../../tools/tillr-drain/drain/loop.go)):** Untouched. When a genuine same-file conflict occurs, conflict repair still stages a full model run against the trunk tip observed at staging time; and while filed requests (`c.BodyRef == ""` in [`../../../tools/tillr-drain/drain/stage.go`](../../../tools/tillr-drain/drain/stage.go)) already receive `["docs/"]` in `blockConflictPaths`, legacy document-anchored items (`c.BodyRef != ""`) that name [`../tillr-watch.md`](../tillr-watch.md) currently receive only the exact `.md` file path unless they name a path under `docs/design/tillr-watch/`.
2. **The automated squash lander ([`../../../tools/tillr-land/land/landing.go`](../../../tools/tillr-land/land/landing.go), [`../../../tools/tillr-land/land/trunk.go`](../../../tools/tillr-land/land/trunk.go)):** Untouched. `Trunk.Squash` continues to run standard three-way `git merge-tree --write-tree` with no custom Markdown or ledger-table merge driver.
3. **Other design documents' layout (`docs/design/*.md` outside `tillr-watch`):** Untouched. Other multi-decision documents in `docs/design/` retain their existing single-file `## Decision Ledger` tables unless migrated by separate items.
4. **Existing `tillr-watch` decision records' text ([`../tillr-watch.md`](../tillr-watch.md)):** Untouched (`0` lines modified in [`../tillr-watch.md`](../tillr-watch.md)). Existing records [`D-TW1`](../tillr-watch.md#9-decision-ledger) through [`D-TW78`](../tillr-watch.md#D-TW78) stay in [`../tillr-watch.md`](../tillr-watch.md) without splitting, renumbering, or header edits.

---

## 2. Why Concurrent Inline Additions Collided on `docs/design/tillr-watch.md`

When a filed `tillr-watch` build request raises a definition obstacle (or a `design`-seat request specifies a new `tillr-watch` rule), [`../../../tools/tillr-drain/drain/stage.go`](../../../tools/tillr-drain/drain/stage.go) (`briefValuesFor`, `c.TriageVersion == "" && c.BodyRef == ""`) instructs the `design` seat to update the design document under `docs/` that the request reaches, including its decision ledger. Because [`../tillr-watch.md`](../tillr-watch.md) (`2,637` lines at `07:27 ET` on `2026-11-24`, `2,753` lines through [`D-TW78`](../tillr-watch.md#D-TW78) at commit `29b79af6af`) stored every decision inline in one file, concurrent additions edited up to six identical or adjacent regions of [`../tillr-watch.md`](../tillr-watch.md) — verified directly in consecutive landed commits `1baadfcd9c` ([`D-TW76`](../tillr-watch.md#D-TW76)), `f225949403` ([`D-TW77`](../tillr-watch.md#D-TW77)), and `2d71a3b9c7` ([`D-TW78`](../tillr-watch.md#D-TW78)):

1. **Frontmatter `next:`:** replacing the single `next:` summary string at the top of [`../tillr-watch.md`](../tillr-watch.md).
2. **Opening `**Status:**` rollup:** updating the unbuilt rule list and running `D-TW1`–`D-TW<N>` count in the opening status paragraph of [`../tillr-watch.md`](../tillr-watch.md).
3. **Sequential tail of [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog):** appending the next numbered rule subsection (`### 5.13`, `### 5.14`, `### 5.15`) at the bottom of Section 5 immediately above Section 6.
4. **Introductory summary of [Section 9](../tillr-watch.md#9-decision-ledger):** updating the prose decision count and per-date `D-TW*` lists above the ledger table.
5. **Back-patched `"Amended ... by"` clauses on shared rows:** editing existing ledger rows such as [`D-TW13`](../tillr-watch.md#D-TW13), [`D-TW16`](../tillr-watch.md#D-TW16), [`D-TW42`](../tillr-watch.md#D-TW42), [`D-TW50`](../tillr-watch.md#D-TW50), [`D-TW60`](../tillr-watch.md#D-TW60), [`D-TW66`](../tillr-watch.md#D-TW66), and [`D-TW67`](../tillr-watch.md#D-TW67) in place.
6. **Sequential tail of the [Section 9](../tillr-watch.md#9-decision-ledger) table:** claiming the same next integer ID (`D-TW76`, `D-TW77`, `D-TW78`) on the final line of [`../tillr-watch.md`](../tillr-watch.md).

Under `Trunk.Squash` ([`../../../tools/tillr-land/land/trunk.go`](../../../tools/tillr-land/land/trunk.go)), landing runs `git merge-tree --write-tree --merge-base=<base> <trunk> <head>`. Any two candidates branched from the same base that append to the bottom of the same table, claim the same `D-TW<N>` number, or rewrite `next:` and `**Status:**` fail three-way merge with `"CONFLICT (content): Merge conflict in docs/design/tillr-watch.md"`.

Repairing that conflict on the `design` seat is expensive because the repair worker must re-read a `2,600+`-line specification, renumber its `D-TW<N>` identifier and `### 5.<k>` heading, update internal `#D-TW<N>` and heading links, merge single-line prose summaries, and re-verify the entire file with `uvx vantage-check`. In the 2026-11-24 measurement (`08:14:47` to `08:43:51 ET`), one repair consumed **29 minutes** and **10.0 million tokens** (~21 minutes model time, ~90 seconds of commands) — ~193x longer than the **nine-second** gap (`08:14:42` base vs `08:14:51` trunk landing) that invalidated its base before the repair had barely begun. As [`../landing-principles.md`](../landing-principles.md) ([Section 2](../landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [Section 3](../landing-principles.md#3-gate-what-will-be-the-tip-once)) establishes, a mechanism whose conflict and repair cost grows with trunk landing frequency is pointed the wrong way.

---

## 3. Standalone Per-Record Files Under `docs/design/tillr-watch/`

**Designer's decision ([`D-TW-DR1`](#D-TW-DR1), from the 2026-11-24 operator request — the owner has not ruled on this and may overturn it):** Every new `tillr-watch` decision record added after [`D-TW78`](../tillr-watch.md#D-TW78) (beginning with this record at `docs/design/tillr-watch/decision-record-files.md`) is placed in its own standalone Markdown file under `docs/design/tillr-watch/` and makes **zero** edits to [`../tillr-watch.md`](../tillr-watch.md), earlier `docs/design/tillr-watch/*.md` files, or any shared hand-maintained index.

### 3.1 One standalone file per new decision record

1. **Path:** Each new `tillr-watch` decision record lives at `docs/design/tillr-watch/<slug>.md`, following the repository's existing `docs/design/<stem>/<child>.md` subdirectory layout ([`../tillr-utility-contracts/tillr-watch.md`](../tillr-utility-contracts/tillr-watch.md) under [`../tillr-utility-contracts.md`](../tillr-utility-contracts.md)).
2. **Collision-free `<slug>`:** `<slug>` is a descriptive kebab-case stem naming both the rule or subsystem and the specific decision (for example `fixer-stays-queued-alert.md` or `drain-base-replay-overlap-rule.md`). `<slug>` never uses a sequential integer prefix (`0079-...`, which two concurrent runs would both claim), never uses a bare rule stem when recording a narrow amendment that another concurrent item on the same rule could also pick, and never embeds private `tillr` item or run IDs ([`OQ-PR1`](../../tillr/decision-provenance-review.md#decision-ledger)).
3. **Topic-scoped ledger anchors:** Inside `docs/design/tillr-watch/<slug>.md`, the file's own `## Decision Ledger` uses topic-scoped anchors derived from the record's subject (`<a id="D-TW-<TOPIC>1"></a>`, `<a id="D-TW-<TOPIC>2"></a>`, and `<a id="OQ-TW-<TOPIC>1"></a>` when an owner question is needed) rather than incrementing [`../tillr-watch.md`](../tillr-watch.md)'s frozen `D-TW1`–`D-TW78` sequence.

### 3.2 Frontmatter and forward-only amendments (zero shared-file edits)

1. **Frontmatter:** Every `docs/design/tillr-watch/<slug>.md` file is a complete Vantage-checked design document (`status`, `stage`, `updated`, `tags`, `summary`, `next`, `depends-on`, `vantage: status-chip: true`, and `## Decision Ledger`) whose `depends-on` list includes [`../tillr-watch.md`](../tillr-watch.md) together with any specific `../tillr-watch.md#D-TW...` or `./<prior-slug>.md#<id>` anchor that the record extends or amends (verified by [`../../../tools/gate/checks/design-citations.py`](../../../tools/gate/checks/design-citations.py)).
2. **Zero edits to [`../tillr-watch.md`](../tillr-watch.md) or shared lists:** Adding a `tillr-watch` decision record modifies **zero** lines of [`../tillr-watch.md`](../tillr-watch.md) — it does not touch `next:`, `related:`, `**Status:**`, [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog), the [Section 9](../tillr-watch.md#9-decision-ledger) summary paragraph, or any existing `D-TW1`–`D-TW78` row — and appends to **no** shared file list in [`../tillr-watch.md`](../tillr-watch.md), [`../../tillr/design-status.md`](../../tillr/design-status.md), or [`../../tillr/roadmap.md`](../../tillr/roadmap.md).
3. **Forward-only amendments:** When a new record narrows, supersedes, or amends an earlier decision in [`../tillr-watch.md`](../tillr-watch.md) or in an earlier `docs/design/tillr-watch/<prior-slug>.md` file, it states `Amends [D-TW42](../tillr-watch.md#D-TW42)` (or `Amends [D-TW-FOO1](./<prior-slug>.md#D-TW-FOO1)`) in its own `depends-on`, prose, and `## Decision Ledger` without editing the earlier file. In-place edits to [`../tillr-watch.md`](../tillr-watch.md) or to an existing `docs/design/tillr-watch/<slug>.md` file occur only when the task directly targets that existing record itself (for example recording a dated verbatim owner ruling on an existing row or fixing a broken link).

### 3.3 How readers and `Trunk.Squash` handle the split set

1. **Reading the `tillr-watch` specification:** Every reader (`design`, `plan`, `build`, `tricky`, `review`, `verify`, `scout`, and human reviewers) reads [`../tillr-watch.md`](../tillr-watch.md) (`D-TW1`–`D-TW78` baseline) together with the tracked files matching `docs/design/tillr-watch/*.md` (`git ls-files 'docs/design/tillr-watch/*.md'`). Where a newer dated record in `docs/design/tillr-watch/*.md` forward-amends an earlier clause, the newer record governs.
2. **Concurrent landings in `Trunk.Squash`:** When Design Item A adds `docs/design/tillr-watch/<slug-a>.md` from base $B$ and Design Item B adds `docs/design/tillr-watch/<slug-b>.md` from the same (or an older) base $B$, neither candidate modifies [`../tillr-watch.md`](../tillr-watch.md) or any other shared path. When Item A lands first (`trunk` advances to $T_1$) and Item B arrives at `Trunk.Squash` ([`../../../tools/tillr-land/land/trunk.go`](../../../tools/tillr-land/land/trunk.go)), `git merge-tree --write-tree --merge-base=<B> <T_1> <H_B>` merges two distinct tree entries under `docs/design/tillr-watch/` cleanly in one step — **zero** merge conflicts, **zero** `D-TW*` renumbering, and **zero** `29`-minute conflict-fix runs, regardless of how many `tillr-watch` decision records land per hour.
3. **Genuine same-file edits keep existing drain conflict repair:** If two concurrent items pick the exact same `docs/design/tillr-watch/<slug>.md` path or both directly edit the same single record file, Git's `add/add` or content conflict routes through the drain's existing conflict-repair path unchanged — operating on a compact single-decision file rather than a `2,753`-line shared ledger, with **no** item ordering, **no** retry count, **no** time limit, and **no** fallback.

---

## 4. The One Behavior of the Follow-Up Item

Per [`../landing-principles.md`](../landing-principles.md) ([Section 15](../landing-principles.md#15-land-the-smallest-functional-change)) and the filed request, the follow-up build item implements **one behavior** (with unit tests in the same commit) and adds **no** item ordering, **no** retry count, **no** time limit, and **no** fallback:

- **Target check and files:** Extend the fast `documents` change check in [`../../../tools/gate/checks/change_common.py`](../../../tools/gate/checks/change_common.py) (`run("documents", ...)` invoked by [`../../../tools/gate/checks/documents.py`](../../../tools/gate/checks/documents.py) under [`../../../tools/gate/checks/documents.json`](../../../tools/gate/checks/documents.json)), with permanent unit tests in [`../../../tools/gate/checks/change_checks_test.py`](../../../tools/gate/checks/change_checks_test.py).
- **Single behavior:** Given `(base, HEAD)`, before returning `0` from `run("documents", ...)`, inspect the `base..HEAD` diff across `docs/design/tillr-watch.md` and `docs/design/tillr-watch/*.md` and fail (`exit 1`, printing one finding line per violation) whenever `base..HEAD`:
  1. modifies `docs/design/tillr-watch.md` and introduces any `D-TW*` HTML anchor (`<a id="D-TW...">` or `<a name="D-TW...">`, case-insensitive, outside fenced code blocks) in `HEAD:docs/design/tillr-watch.md` that was not already present in `base:docs/design/tillr-watch.md`; or
  2. adds (`--diff-filter=A`) one or more Markdown files under `docs/design/tillr-watch/` while in the same `base..HEAD` range also modifying `docs/design/tillr-watch.md` or modifying an already-existing `docs/design/tillr-watch/*.md` file, or adds a Markdown file under `docs/design/tillr-watch/` whose frontmatter `depends-on` list does not include [`../tillr-watch.md`](../tillr-watch.md).
- **Diagnostic and allowed edits:** On refusal, the finding names the offending path and states that new `tillr-watch` decision records belong in their own standalone file `docs/design/tillr-watch/<slug>.md` (with [`../tillr-watch.md`](../tillr-watch.md) in `depends-on`) and with `docs/design/tillr-watch.md` and earlier `docs/design/tillr-watch/*.md` files left untouched (citing `docs/design/tillr-watch/decision-record-files.md`), so any `design`-seat run that attempts an inline `D-TW79` edit is redirected by the fast gate before `Trunk.Squash` ever touches `main`. Direct in-place edits to `docs/design/tillr-watch.md` that add no new `D-TW*` anchor and add no `docs/design/tillr-watch/*.md` file (such as recording an owner ruling on an existing `D-TW1`–`D-TW78` row or fixing a link), and direct edits to a single existing `docs/design/tillr-watch/<slug>.md` file, continue to pass (`exit 0`). Evaluating the diff rule in `run("documents", ...)` against `(base, HEAD)` ensures per-blob remembered `vantage-check` results never skip the `base..HEAD` check.
- **Explicit non-behaviors:** The follow-up item adds **no** `blocked_by` or path-based item ordering, **no** conflict-repair retry cap, **no** wall-clock or step timeout, and **no** fallback merge path.

---

## 5. Rejected Alternatives

The alternatives below were evaluated and rejected by the designer (the owner has not ruled on them and may overturn any of them):

| Alternative | Why rejected (designer's evaluation) |
| :--- | :--- |
| **Order or serialize `tillr-watch` design items with `blocked_by` edges or path locks** | Violates the request's explicit prohibition (*"Do not order items"*) and turns independent design items into a serial queue whose wait grows with queue depth. |
| **Cap conflict-fix retries, impose a repair time limit, or fall back when `Trunk.Squash` conflicts** | Violates the request's explicit prohibitions (*"do not add a retry count or a time limit, and do not add a fallback"*) and inherited owner rulings [`R-TW-DR1`](#R-TW-DR1) and [`R-TW-DR4`](#R-TW-DR4). |
| **Keep one `docs/design/tillr-watch.md` file and shard inline subsections inside it** | Two concurrent additions still collide on frontmatter `next:`, `**Status:**`, sequential `D-TW<N>` numbering, back-patched `"Amended by"` clauses, and the bottom of `## 9. Decision Ledger`, and every check still parses and lints a `2,750+`-line file. |
| **Put records in `docs/design/tillr-watch/<slug>.md` AND append a link row to a shared index in `docs/design/tillr-watch.md` or `docs/tillr/design-status.md`** | Reintroduces a single-line append hotspot at the bottom of the shared index table — recreating the exact `git merge-tree` conflict that caused the eight collisions on `2026-11-24`. |
| **Number filenames sequentially (`docs/design/tillr-watch/0079-....md`)** | Two concurrent `design` runs branched from the same base both pick `0079`, trading a content conflict in [`../tillr-watch.md`](../tillr-watch.md) for a file-path collision or duplicate sequence prefix. |

---

## 6. Decision Ledger

### 6.1 Inherited Owner Rulings (Verbatim Quotes)

Only dated verbatim owner statements are recorded here as owner rulings ([`OQ-PR22`](../../tillr/decision-provenance-review.md#decision-ledger)).

| ID | Date | Verbatim owner quote | Source |
| :--- | :--- | :--- | :--- |
| <a id="R-TW-DR1"></a>**R-TW-DR1** | 2026-11-16 | *"Explicitly, we NEVER want fallbacks anywhere in this project."* | [`../gate-test-capacity.md`](../gate-test-capacity.md#the-owners-rulings) (`R-GTC1`) |
| <a id="R-TW-DR2"></a>**R-TW-DR2** | 2026-11-21 | *"part of desgins can absoutely land. this is generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the smallest functional change possible to make reviewing, testing, velocity, all this stuff easier."* | [`../landing-principles.md`](../landing-principles.md#15-land-the-smallest-functional-change) |
| <a id="R-TW-DR3"></a>**R-TW-DR3** | 2026-11-22 | *"Do not put tillr references in commits."* | [`OQ-PR1`](../../tillr/decision-provenance-review.md#decision-ledger) |
| <a id="R-TW-DR4"></a>**R-TW-DR4** | 2026-11-21 | *"don't jump to different models! wait on resets if needed, surface an error for an operator. No fallbacks, including changing models!"* | [`../tillr-judge-and-lifecycle.md`](../tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) (`D-JL24`) |

### 6.2 Designer Decision (From Operator Request)

| ID | Date | Provenance | Decision |
| :--- | :--- | :--- | :--- |
| <a id="D-TW-DR1"></a>**D-TW-DR1** | 2026-11-24 | Designer's decision from the 2026-11-24 operator request; the owner has not ruled on this and may overturn it. | **Standalone forward-only `docs/design/tillr-watch/<slug>.md` decision record files and single `documents` diff guard.** Measured on `2026-11-24` between `07:27` and `08:45 ET`, the lander recorded eight squash conflicts on [`../tillr-watch.md`](../tillr-watch.md) (`2,637` lines: `"CONFLICT (content): Merge conflict in docs/design/tillr-watch.md"`), each dispatching a `design`-seat conflict-fix run; one measured repair (`08:14:47` to `08:43:51 ET`, on the fixer-stays-queued alert item) took **29 minutes** and **10.0 million tokens** (~21 minutes with no command running, ~90 seconds in commands) against a base taken at `08:14:42 ET`, conflicted again at `08:44:51 ET` because another [`../tillr-watch.md`](../tillr-watch.md) decision record had landed nine seconds after its base at `08:14:51 ET`, and entered a second 29-minute-class repair while a second item's repair (begun at `08:14:43 ET`) sat in the same position ([`../landing-principles.md`](../landing-principles.md) [Section 2](../landing-principles.md#2-re-application-preserves-the-change-not-the-patch), [Section 3](../landing-principles.md#3-gate-what-will-be-the-tip-once)). Every new `tillr-watch` decision record after [`D-TW78`](../tillr-watch.md#D-TW78) is therefore written as its own standalone Vantage-valid file `docs/design/tillr-watch/<slug>.md` (descriptive kebab-case `<slug>`, topic-scoped `D-TW-<TOPIC><N>` ledger anchors, `depends-on` naming [`../tillr-watch.md`](../tillr-watch.md) plus any amended anchor, and forward-only `Amends ...` citations) with **zero** edits to [`../tillr-watch.md`](../tillr-watch.md), earlier `docs/design/tillr-watch/*.md` files, or shared index lists, so two concurrent additions touch disjoint paths under `docs/design/tillr-watch/` and three-way merge cleanly in `Trunk.Squash` ([`../../../tools/tillr-land/land/trunk.go`](../../../tools/tillr-land/land/trunk.go)). **One follow-up item behavior:** extend `run("documents", ...)` in [`../../../tools/gate/checks/change_common.py`](../../../tools/gate/checks/change_common.py) (tested in [`../../../tools/gate/checks/change_checks_test.py`](../../../tools/gate/checks/change_checks_test.py)) to refuse (`exit 1`) any `base..HEAD` change that adds a new `D-TW*` HTML anchor to `docs/design/tillr-watch.md`, modifies `docs/design/tillr-watch.md` or an existing `docs/design/tillr-watch/*.md` file alongside adding a Markdown file under `docs/design/tillr-watch/`, or adds a `docs/design/tillr-watch/*.md` file without [`../tillr-watch.md`](../tillr-watch.md) in `depends-on`. Adds **no** item ordering, **no** retry count, **no** time limit, and **no** fallback; genuine same-file conflicts continue to use the drain's existing conflict-repair path unchanged. |
