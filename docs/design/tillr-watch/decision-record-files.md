---
title: "Each new tillr-watch decision record lives in its own file, and adding one edits nothing shared"
author: "Agent"
date: 2026-11-25
status: in-review
stage: DECIDED
stage-since: 2026-11-25
tags: [tillr, watch, design, documents, landing, gate]
summary: "Every new decision record for tillr-watch goes in its own topic-named file under docs/design/tillr-watch/ with topic-derived anchors and backward-only amendments, touching zero lines of docs/design/tillr-watch.md or any shared index; the gate's documents check enforces the rule."
next: "Land the queued documents check item enforcing Section 3, then run the two-step follow-up items in Section 4 to move D-TW79 through D-TW81 out of docs/design/tillr-watch.md; the owner may review D-TW-DR1 through D-TW-DR3."
depends-on:
  - ../tillr-watch.md
  - ../landing-principles.md#2-re-application-preserves-the-change-not-the-patch
  - ../landing-principles.md#3-gate-what-will-be-the-tip-once
  - ../landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# Each new `tillr-watch` decision record lives in its own file, and adding one edits nothing shared

**Status:** 2026-11-25, verified at `a6be4821f8`. This file is the first standalone record under `docs/design/tillr-watch/`; the gate enforcement of [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) is not built (`rg -n 'docs/design/tillr-watch|D-TW' tools/gate tools/tillr-gate` returns nothing), and `D-TW79` through `D-TW81` still sit inline in [`../tillr-watch.md`](../tillr-watch.md) pending the follow-up items in [§4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3).

> **In short.** Appending alert-rule decisions to one shared `tillr-watch` document forces concurrent lanes to edit the same lines and fail three-way squash merges. Giving every new decision record its own unnumbered file under `docs/design/tillr-watch/` with zero edits to any shared file makes concurrent watch designs merge cleanly regardless of trunk velocity.

**Why it matters.** On 2026-11-24, eight squash conflicts hit [`../tillr-watch.md`](../tillr-watch.md) in 78 minutes; one repair run spent 29 minutes and 10.0 million tokens re-reading and renumbering the file ([§1.2](#12-why-inline-records-collide-and-the-2026-11-24-measurement)).

**The shape.** Each new record is a complete checked design document at `docs/design/tillr-watch/<slug>.md` naming [`../tillr-watch.md`](../tillr-watch.md) in `depends-on`, using topic-derived anchors, and amending older decisions backward only; the gate's `documents` check refuses inline `D-TW` additions and mixed edits.

**Cost.** Readers read [`../tillr-watch.md`](../tillr-watch.md) together with every tracked file under `docs/design/tillr-watch/` rather than scanning one ledger table.

**Start at [§2](#2-the-one-file-per-record-rule-r-tw-dr1-d-tw-dr1)** for the placement and amendment rules, and [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) for the gate check.

**Needs your ruling:** None. Owner rulings [`R-TW-DR1`](#R-TW-DR1) and [`R-TW-DR2`](#R-TW-DR2) are settled; [`D-TW-DR1`](#D-TW-DR1) through [`D-TW-DR3`](#D-TW-DR3) come from the operator request and the designer and may be overturned by the owner.

**Reads with:** [`../tillr-watch.md`](../tillr-watch.md) (the baseline watch design), [`../landing-principles.md`](../landing-principles.md) (three-way squash landing and smallest-change rules), and [`../tillr-utility-contracts/tillr-watch.md`](../tillr-utility-contracts/tillr-watch.md) (the `tillr-watch` CLI contract).

---

## 1. Rulings, Terms, and the 2026-11-24 Measurement

### 1.1 Owner rulings and provenance

Two owner rulings govern this design:

- **One file per new watch decision record ([`R-TW-DR1`](#R-TW-DR1), 2026-11-25).** The owner ruled on 2026-11-25 (`"ok, so save that"`): every new decision record about `tillr-watch` goes in its own small file under `docs/design/tillr-watch/`, and [`../tillr-watch.md`](../tillr-watch.md) is not edited when a record is added. An earlier designer-only draft at `docs/design/tillr-watch/decision-record-files.md` (`51d6d43649`) was deleted on 2026-11-24 in `976800859e` because nothing enforced it and nothing linked it; it is written here against the current tree under the owner's ruling and paired with a queued gate check.
- **No fallbacks ([`R-TW-DR2`](#R-TW-DR2), 2026-11-16).** The owner ruled on 2026-11-16: `"Explicitly, we NEVER want fallbacks anywhere in this project."`

The specific file-naming rules, topic-derived anchor scheme, backward-only amendment convention, `documents` check clauses, rejected alternatives, and two-step migration of `D-TW79` through `D-TW81` ([`D-TW-DR1`](#D-TW-DR1) through [`D-TW-DR3`](#D-TW-DR3)) come from the 2026-11-25 operator request and the designer's specification. None of [`D-TW-DR1`](#D-TW-DR1) through [`D-TW-DR3`](#D-TW-DR3) is an owner ruling, and the owner may overturn any part of them.

### 1.2 Why inline records collide and the 2026-11-24 measurement

[`../tillr-watch.md`](../tillr-watch.md) (753 lines at `a6be4821f8`, after `c65c0004c4` condensed its built rules to present tense) keeps the baseline watch architecture, rule catalog, and decision table in a single file. Adding a new alert rule or engine decision inline touches up to five shared locations in that file:

1. the frontmatter `next:` line, which enumerates the unbuilt rule subsections;
2. the opening `**Status:**` paragraph, which counts built and unbuilt rules;
3. the tail of [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog), where the next numbered subsection is appended;
4. the tail of [Section 9](../tillr-watch.md#9-decision-ledger), where a row is appended and claims the next sequential `D-TW<N>` anchor (and where, prior to `c65c0004c4`, a summary count line above the table was also updated on every addition);
5. existing ledger rows or subsections when a new decision amends an older one in place.

At landing time, `Trunk.Squash` in [`trunk.go`](../../../tools/tillr-land/land/trunk.go) three-way merges a gated candidate's `base..head` diff onto the current trunk tip without re-gating ([`landing-principles.md` §2](../landing-principles.md#2-re-application-preserves-the-change-not-the-patch), [§3](../landing-principles.md#3-gate-what-will-be-the-tip-once)). Two design runs branched from the same base that both append to [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog), claim the next `D-TW<N>` integer in [Section 9](../tillr-watch.md#9-decision-ledger), or edit `next:` and `**Status:**` cannot both three-way merge: the second candidate records `result = "squash-conflict"` and returns to the `design` seat.

**Measurement (2026-11-24, `07:27` to `08:45 ET`).** In a 78-minute window on 2026-11-24, the lander recorded **eight** squash conflicts on [`../tillr-watch.md`](../tillr-watch.md), each answered by a conflict-fix run on the `design` seat. One of those conflict-fix runs took **29 minutes** and **10.0 million tokens**, because repairing an inline collision requires re-reading the entire watch design, renumbering the collided `D-TW<N>` decision and subsection heading, updating every internal link, and re-checking the file. Because repair wall time exceeds the interval between landings on an active drain, conflict cost rises with trunk activity — the inverted polarity ruled out by [`landing-principles.md` §2](../landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [§3](../landing-principles.md#3-gate-what-will-be-the-tip-once) ([§12](../landing-principles.md#12-a-principle-cites-the-measurement-that-produced-it)).

### 1.3 Terms

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Decision record** *(coined here)* | One standalone checked design document at `docs/design/tillr-watch/<slug>.md` that settles or proposes a new `tillr-watch` rule, engine capability, CLI behavior, or amendment after [`../tillr-watch.md`](../tillr-watch.md). | Not a row appended to [`../tillr-watch.md`](../tillr-watch.md), not a runtime TOML rule file under `config/tillr/watch/`, and not an implementation plan seed. |
| **Main watch design** *(used here for [`../tillr-watch.md`](../tillr-watch.md))* | The baseline `tillr-watch` design document holding [`OQ-TW1`](../tillr-watch.md#OQ-TW1) through [`OQ-TW5`](../tillr-watch.md#OQ-TW5) and `D-TW1` through `D-TW78` (plus `D-TW79` through `D-TW81` until [§4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3) moves them). | Not an index of `docs/design/tillr-watch/*.md` files, and not a target for new `D-TW` anchors. |
| **Topic prefix** *(coined here)* | A short uppercase identifier derived from a record's subject (such as `DR` in `D-TW-DR1` or `CPU` in `D-TW-CPU1`) used to namespace anchors inside one record file. | Not a global integer counter across `docs/design/tillr-watch/`. |

---

## 2. The One-File-Per-Record Rule ([`R-TW-DR1`](#R-TW-DR1), [`D-TW-DR1`](#D-TW-DR1))

Under owner ruling [`R-TW-DR1`](#R-TW-DR1) and the operator-request / designer rules in [`D-TW-DR1`](#D-TW-DR1) (which the owner may overturn), every new `tillr-watch` decision record obeys seven invariants:

| Aspect | Requirement |
| :--- | :--- |
| **File path** | `docs/design/tillr-watch/<slug>.md`, where `<slug>` is lowercase kebab-case naming the alert rule or subsystem and the specific decision (for example [`decision-record-files.md`](./decision-record-files.md) or slug `process-cpu-high`). The filename carries **no sequence number** (two concurrent runs from one base would claim the same number) and no queue item or run ID. |
| **Topic-derived anchors** | Decision, ruling, and open-question anchors inside the file derive from a topic prefix (`D-TW-<TOPIC>1`, `R-TW-<TOPIC>1`, `OQ-TW-<TOPIC>1`) and **never** continue the integer `D-TW<N>` or `OQ-TW<N>` sequence of [`../tillr-watch.md`](../tillr-watch.md). |
| **Zero shared edits** | Adding a record creates its one file under `docs/design/tillr-watch/` and changes **zero lines** of [`../tillr-watch.md`](../tillr-watch.md), zero lines of any existing file under `docs/design/tillr-watch/`, and zero lines of any shared index or status document. **No shared list of record files is kept anywhere**, because appending to a list recreates the same tail-line collision in a new place. |
| **Backward-only amendments** | A record that narrows, extends, or supersedes an earlier decision names the amended decision in its own prose and in its frontmatter `depends-on` list (for example [`../tillr-watch.md#D-TW63`](../tillr-watch.md#D-TW63) or `./<earlier-slug>.md#D-TW-<TOPIC>1`). The earlier row and earlier file are **not** edited. |
| **Complete checked design document** | Each record file is a self-contained design document passing `uvx vantage-check` and `design-citations`, with complete planning frontmatter (`title`, `status`, `stage`, `summary`, `next`, `depends-on`) whose `depends-on` names [`../tillr-watch.md`](../tillr-watch.md). |
| **How the set is read** | Readers read [`../tillr-watch.md`](../tillr-watch.md) together with every tracked Markdown file matching `docs/design/tillr-watch/*.md` (`git ls-files 'docs/design/tillr-watch/*.md'`). Where a newer dated record names an older decision as amended, the newer record governs; if two undated or same-day records ever amend the same decision inconsistently without naming one another, a follow-up record resolves both explicitly. |
| **Same-file edits and conflicts** | Editing [`../tillr-watch.md`](../tillr-watch.md) or an existing record file in place remains valid when that existing document is itself the subject of the work (such as recording an owner ruling on an open question in that file, graduating built text, removing migrated rows as in [§4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3), or fixing a broken link), without adding a new record file or a new `D-TW` anchor in [`../tillr-watch.md`](../tillr-watch.md). Two concurrent items that pick the same `<slug>.md` path (or both edit the same existing record file) collide in `Trunk.Squash` as any same-path edit does and go through the drain's ordinary conflict repair on that small file. |

### Non-goals and forbidden mechanisms

The following are explicitly **not** part of this design ([`R-TW-DR2`](#R-TW-DR2), [`D-TW-DR1`](#D-TW-DR1)):

- **No ordering or serialization of items:** independent `tillr-watch` design items are never chained with synthetic dependencies or held behind shared-path locks.
- **No retry count or repair cap:** conflict repairs are never counted or capped ([`landing-principles.md` §5](../landing-principles.md#5-only-verdicts-charge), [§6](../landing-principles.md#6-a-budget-escalates-it-does-not-loop)).
- **No time limit:** no wall-clock timeout stops a design or repair run that is making progress ([`landing-principles.md` §8](../landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)).
- **No fallback:** no secondary merge strategy, automatic filename suffixing, or fallback numbering exists when two items pick the same `<slug>.md` ([`R-TW-DR2`](#R-TW-DR2)).

---

## 3. The One Enforcement: the Gate's `documents` Check ([`D-TW-DR2`](#D-TW-DR2))

A separate queued item implements enforcement inside the gate's `documents` check. Because the gate checks are being ported from Python to Go, this design specifies only the check's observable behavior and names no source file for it ([`D-TW-DR2`](#D-TW-DR2), an operator-request / designer decision the owner may overturn).

### 3.1 Pure range predicate (`base..HEAD`)

Evaluated over the committed `base..HEAD` range with rename detection disabled (`--no-renames`, so moving or renaming a file into `docs/design/tillr-watch/` is judged as an addition), the `documents` check refuses the candidate when any of three conditions holds:

1. **New `D-TW` anchor in the main watch design.** [`docs/design/tillr-watch.md`](../tillr-watch.md) at `HEAD` contains a `D-TW` anchor (an HTML `<a id="…">` or `<a name="…">` anchor, heading slug, or Vantage question directive `id=` whose normalized identifier starts with `d-tw`) that was not present in [`docs/design/tillr-watch.md`](../tillr-watch.md) at `base`. Deleting existing `D-TW` anchors (as in [§4.2](#42-the-two-step-follow-up-move)) or editing prose in [`docs/design/tillr-watch.md`](../tillr-watch.md) while keeping the set of `D-TW` anchors a subset of `base`'s passes.
2. **Added record file combined with edits to the main design or an existing record file.** The `base..HEAD` diff adds at least one Markdown file under `docs/design/tillr-watch/` (`docs/design/tillr-watch/*.md` absent at `base` and present at `HEAD`) **and** modifies or deletes [`docs/design/tillr-watch.md`](../tillr-watch.md) or any `docs/design/tillr-watch/*.md` file that existed at `base`. Modifying an existing record file or [`docs/design/tillr-watch.md`](../tillr-watch.md) when zero files are added under `docs/design/tillr-watch/` passes.
3. **Added record file missing `depends-on` on the main watch design.** The `base..HEAD` diff adds a Markdown file under `docs/design/tillr-watch/` whose YAML frontmatter `depends-on` field (scalar, inline list, or block list) contains no entry that resolves — after stripping any `#<anchor>` fragment and resolving relative to the record file's directory — to `docs/design/tillr-watch.md` (for example [`../tillr-watch.md`](../tillr-watch.md) or [`../tillr-watch.md#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5`](../tillr-watch.md#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5)).

### 3.2 Refusal output and cache interaction

- **Cache-proof evaluation:** Clauses 1 and 2 are properties of the `base..HEAD` commit range rather than of one file's blob in isolation, so the `documents` check evaluates all three clauses on every run that touches [`docs/design/tillr-watch.md`](../tillr-watch.md) or `docs/design/tillr-watch/*.md` before consulting per-document remembered `vantage-check` passes.
- **Actionable diagnostics:** Each refusal prints the offending repo-relative path and which of the three clauses failed, so a returned worker or designer run receives exact evidence without reading gate internals.
- **Degenerate ranges:** When `base == HEAD` or neither [`docs/design/tillr-watch.md`](../tillr-watch.md) nor any `docs/design/tillr-watch/*.md` path appears in `base..HEAD`, all three clauses pass with zero overhead. Subdirectories deeper than `docs/design/tillr-watch/` are governed if they match `docs/design/tillr-watch/**/*.md`.

---

## 4. Moving `D-TW79`, `D-TW80`, and `D-TW81` Out of the Main Document ([`D-TW-DR3`](#D-TW-DR3))

### 4.1 The three inline records added since 2026-11-24, and why follow-up items move them

Between the 2026-11-24 deletion of the earlier draft (`976800859e`) and `HEAD` (`a6be4821f8`), three watch decisions were added inline at the tails of [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog) (subsections `5.16` through `5.18`) and [Section 9](../tillr-watch.md#9-decision-ledger) of [`../tillr-watch.md`](../tillr-watch.md). None of their three rule files exists yet under `config/tillr/watch/` (`rg -n 'drain-proved-fixer-gated|drain-trunk-fixer-proved-queued|drain-process-cpu-high' config/tillr/watch` returns nothing):

1. **`D-TW79` (subsection `5.16`):** `drain-proved-fixer-gated` — alert when `gate.started` is recorded for a trunk fixer after `drain.fixer-proved`, adding first-present `group_by = { any = [...] }`, `after` and `reset` on `predicate_match`, and `keep_on_terminal_item = true`.
2. **`D-TW80` (subsection `5.17`):** `drain-trunk-fixer-proved-queued` — projection-only `predicate_match` alerting when a `fixer-main-*` item remains `in-queue` after `refs/tillr/green` proves its checks, adding `item.id`, `fixer_checks`, `green_proof:<ref>`, and `item.fixer_proved("<ref>")`.
3. **`D-TW81` (subsection `5.18`):** `drain-process-cpu-high` — per-process alert when `tillr watch wait` or `tillr-view serve` sustains over 50% CPU across ten consecutive 1-minute `drain.process-cpu` samples, adding `process_key` on `drain.process-cpu` entries and `for_each` array fan-out with absent-subject reset on `consecutive_count`.

`D-TW79` through `D-TW81` are the first existing records this rule applies to. They move in **follow-up items**, not in this commit, for two reasons:

- **Compliance with Clause 2 of the `documents` check ([§3.1](#31-pure-range-predicate-basehead)):** Moving `D-TW79` through `D-TW81` in the same commit that adds `docs/design/tillr-watch/decision-record-files.md` would add files under `docs/design/tillr-watch/` while also editing [`../tillr-watch.md`](../tillr-watch.md) — violating Clause 2 of the check this design specifies, failing the gate if the queued `documents` check item lands first, and touching the shared conflict lines of [`../tillr-watch.md`](../tillr-watch.md).
- **Scope and recovered detail ([`landing-principles.md` §15](../landing-principles.md#15-land-the-smallest-functional-change)):** Commit `c65c0004c4` condensed subsections `5.16` through `5.18` in [`../tillr-watch.md`](../tillr-watch.md) to three-sentence summaries while their full schemas, TOML rules, and engine mechanics live in `c65c0004c4^:docs/design/tillr-watch.md`. Reconstituting three standalone checked design documents and cleaning up [`../tillr-watch.md`](../tillr-watch.md) is four deliverables beyond recording the placement rule itself.

### 4.2 The two-step follow-up move

Under [`D-TW-DR3`](#D-TW-DR3) (an operator-request / designer decision the owner may overturn), `D-TW79` through `D-TW81` move in two steps so that **every** commit satisfies all three clauses of [§3.1](#31-pure-range-predicate-basehead):

1. **Step 1 — Three independent follow-up record items (one file per item, zero shared edits).** Each item adds one complete checked design document under `docs/design/tillr-watch/` naming [`../tillr-watch.md`](../tillr-watch.md) in `depends-on`, using its own topic-derived anchor, carrying the complete rule and engine specification from `c65c0004c4^:docs/design/tillr-watch.md` verified against current `HEAD`, and stating in prose that it replaces its inline `D-TW79`–`D-TW81` entry without editing [`../tillr-watch.md`](../tillr-watch.md) or any existing file under `docs/design/tillr-watch/`:

   | Inline record | New standalone record path | Topic anchor | Rule and engine scope carried over |
   | :--- | :--- | :--- | :--- |
   | `D-TW79` (`5.16`) | `docs/design/tillr-watch/proved-fixer-gated.md` | `D-TW-PFG1` | `drain-proved-fixer-gated`; `group_by = { any = [...] }`, ordered `predicate_match` (`after`, `reset`), `keep_on_terminal_item = true` |
   | `D-TW80` (`5.17`) | `docs/design/tillr-watch/proved-fixer-queued.md` | `D-TW-PFQ1` | `drain-trunk-fixer-proved-queued`; `item.id`, `fixer_checks`, `green_proof:<ref>`, `item.fixer_proved("<ref>")`, projection-only `predicate_match` |
   | `D-TW81` (`5.18`) | `docs/design/tillr-watch/process-cpu-high.md` | `D-TW-CPU1` | `drain-process-cpu-high`; `process_key` on `drain.process-cpu` entries and `for_each` array fan-out with absent-subject reset on `consecutive_count` |

   Because each Step 1 item adds a single file under `docs/design/tillr-watch/` and touches no existing file, all three items can run in parallel on separate lanes and merge cleanly under `Trunk.Squash`.

2. **Step 2 — One follow-up cleanup item on [`../tillr-watch.md`](../tillr-watch.md) (ordered after all three Step 1 items land).** A single item edits [`../tillr-watch.md`](../tillr-watch.md) alone — adding **zero** files under `docs/design/tillr-watch/` and adding **zero** `D-TW` anchors — to delete inline subsections `5.16`, `5.17`, and `5.18` from [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog) and rows `D-TW79`, `D-TW80`, and `D-TW81` from [Section 9](../tillr-watch.md#9-decision-ledger), and to update frontmatter `next:` and the opening `**Status:**` paragraph to the remaining inline rules (`5.14` / `D-TW77` and `5.15` / `D-TW78`), without adding any index or link list of the moved files. Verified at `a6be4821f8`: `rg -n 'D-TW(79|80|81)|516-|517-|518-' docs tools config yolo-packs -g '!docs/design/tillr-watch.md'` returns nothing, so no other tracked file links to those anchors today; the Step 2 item re-runs that search before deleting them and repoints any reference introduced in the meantime to the Step 1 record files. Earlier decisions `D-TW1` through `D-TW78` remain in [`../tillr-watch.md`](../tillr-watch.md).

---

## 5. Rejected Alternatives

Each alternative below was considered by the designer and the operator request and set aside (the owner may overturn any of these dispositions except [`R-TW-DR1`](#R-TW-DR1) and [`R-TW-DR2`](#R-TW-DR2)):

| Alternative | Why it fails | Verdict |
| :--- | :--- | :--- |
| **Serialize `tillr-watch` design items** (via item dependencies or shared-path locks) | Turns independent rule designs into a single-file queue whose wait time grows linearly with queue depth, stalling parallel lanes against [`../momentum-principle.md`](../momentum-principle.md). | Rejected. |
| **Cap conflict repairs** (with a retry count, a wall-clock timeout, or a fallback merge path) | Treats the symptom while leaving the shared-line collision intact; violates the no-count and progress-based rules of [`../landing-principles.md`](../landing-principles.md) ([§5](../landing-principles.md#5-only-verdicts-charge), [§6](../landing-principles.md#6-a-budget-escalates-it-does-not-loop), [§8](../landing-principles.md#8-a-gate-that-cannot-answer-must-say-which)) and the owner's no-fallback ruling ([`R-TW-DR2`](#R-TW-DR2)). | Rejected. |
| **Shard inside the one file** (separate subsection groups or multiple ledger tables inside [`../tillr-watch.md`](../tillr-watch.md)) | Frontmatter `next:`, the opening `**Status:**` paragraph, sequential `D-TW<N>` numbering, and adjacent diff context inside one file remain shared collision sites under `Trunk.Squash`. | Rejected. |
| **Separate record files plus a shared index of record files** (in [`../tillr-watch.md`](../tillr-watch.md), a directory index, or a status table) | Appending one link row per new record moves the exact same tail-of-table three-way merge conflict from the decision ledger to the shared index. | Rejected. |
| **Sequentially numbered filenames** (such as `0079-proved-fixer-gated.md` or `d-tw82.md`) | Two concurrent runs branched from the same trunk base both claim the next number, trading an inline ledger conflict for a path collision or duplicate number. | Rejected. |

---

## 6. Open Questions

None.

---

## 7. Decision Ledger

Rows prefixed `R-TW-DR` quote verbatim owner rulings with their dates. Rows prefixed `D-TW-DR` record decisions from the 2026-11-25 operator request and the designer; none of `D-TW-DR1` through `D-TW-DR3` is an owner ruling, and the owner may overturn any of them. `Built` is checked against the tree at `a6be4821f8`.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="R-TW-DR1"></a>**[R-TW-DR1](#R-TW-DR1)** | **Owner ruling** (`"ok, so save that"`): every new decision record about `tillr-watch` goes in its own small file under `docs/design/tillr-watch/`, and [`../tillr-watch.md`](../tillr-watch.md) is not edited when a record is added. | 2026-11-25 | [§1.1](#11-owner-rulings-and-provenance), [§2](#2-the-one-file-per-record-rule-r-tw-dr1-d-tw-dr1) | partly: this first record only |
| <a id="R-TW-DR2"></a>**[R-TW-DR2](#R-TW-DR2)** | **Owner ruling** (`"Explicitly, we NEVER want fallbacks anywhere in this project."`): no fallback merge, numbering, or conflict path is added. | 2026-11-16 | [§1.1](#11-owner-rulings-and-provenance), [§2](#2-the-one-file-per-record-rule-r-tw-dr1-d-tw-dr1), [§5](#5-rejected-alternatives) | yes |
| <a id="D-TW-DR1"></a>**[D-TW-DR1](#D-TW-DR1)** | **Operator request and designer decision (owner may overturn):** unnumbered `docs/design/tillr-watch/<slug>.md` paths, topic-derived `D-TW-<TOPIC>1` anchors, zero edits to [`../tillr-watch.md`](../tillr-watch.md) or any shared index, backward-only amendments via prose and `depends-on`, each record a complete checked design doc naming [`../tillr-watch.md`](../tillr-watch.md) in `depends-on`, readers read the main doc plus `docs/design/tillr-watch/*.md` with newer amending records governing, same-file collisions use ordinary drain conflict repair, and no item ordering, retry count, or time limit. | 2026-11-25 | [§2](#2-the-one-file-per-record-rule-r-tw-dr1-d-tw-dr1), [§5](#5-rejected-alternatives) | partly: this record follows it; enforcement not built |
| <a id="D-TW-DR2"></a>**[D-TW-DR2](#D-TW-DR2)** | **Operator request and designer decision (owner may overturn):** a separate queued item extends the gate's `documents` check over `base..HEAD` (`--no-renames`) to refuse any new `D-TW` anchor in [`../tillr-watch.md`](../tillr-watch.md), any commit adding a file under `docs/design/tillr-watch/` while modifying [`../tillr-watch.md`](../tillr-watch.md) or an existing record file, or any added file under `docs/design/tillr-watch/` whose `depends-on` does not resolve to `docs/design/tillr-watch.md`. | 2026-11-25 | [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) | no |
| <a id="D-TW-DR3"></a>**[D-TW-DR3](#D-TW-DR3)** | **Operator request and designer decision (owner may overturn):** inline decisions `D-TW79` (`5.16`), `D-TW80` (`5.17`), and `D-TW81` (`5.18`) move out of [`../tillr-watch.md`](../tillr-watch.md) in two follow-up steps — three independent single-file items adding `docs/design/tillr-watch/proved-fixer-gated.md` (`D-TW-PFG1`), `docs/design/tillr-watch/proved-fixer-queued.md` (`D-TW-PFQ1`), and `docs/design/tillr-watch/process-cpu-high.md` (`D-TW-CPU1`), followed by one cleanup item deleting `5.16`–`5.18` and `D-TW79`–`D-TW81` from [`../tillr-watch.md`](../tillr-watch.md). | 2026-11-25 | [§4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3) | no |
