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

**Status:** 2026-11-26, verified at `bc431c2541fd`. Four standalone records now live under `docs/design/tillr-watch/` ([`decision-record-files.md`](./decision-record-files.md), [`fixer-held-new-attempt.md`](./fixer-held-new-attempt.md), [`gate-test-cpu-idle.md`](./gate-test-cpu-idle.md), and [`windowed-ack-reopen.md`](./windowed-ack-reopen.md)); the gate enforcement of [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) is not built (`rg -n 'docs/design/tillr-watch|D-TW' tools/gate tools/tillr-gate` returns nothing), [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) and [`D-TW-DR2`](#D-TW-DR2) are updated here to answer `conflict-run-item-request-6c9e89a462-a1-n1` after commit `8059209b` ported the repository gate checks from Python to Go, and `D-TW79` through `D-TW81` still sit inline in [`../tillr-watch.md`](../tillr-watch.md) pending the follow-up items in [§4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3).

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

The specific file-naming rules, topic-derived anchor scheme, backward-only amendment convention, `documents` check predicate, Go target files, finding-line contract, rejected alternatives, and two-step migration of `D-TW79` through `D-TW81` ([`D-TW-DR1`](#D-TW-DR1) through [`D-TW-DR3`](#D-TW-DR3)) come from the 2026-11-24 and 2026-11-25 operator requests and the designer's specification (including the 2026-11-26 conflict answer on [`D-TW-DR2`](#D-TW-DR2) for `item-request-6c9e89a462`). None of [`D-TW-DR1`](#D-TW-DR1) through [`D-TW-DR3`](#D-TW-DR3) is an owner ruling, and the owner may overturn any part of them.

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

*Non-owner decision ([`D-TW-DR2`](#D-TW-DR2), from filed operator request `item-request-6c9e89a462` and the designer's 2026-11-26 answer to `conflict-run-item-request-6c9e89a462-a1-n1`; not an owner ruling, and the owner may overturn any part of this section).*

Filed operator request `item-request-6c9e89a462` implements this enforcement inside the fast `documents` change check ([`tools/gate/checks/documents.json`](../../../tools/gate/checks/documents.json)). That request was written against an earlier unlanded draft (`bc4232f3fd` `[unlanded]`, removed before `685934ce` recorded this document on trunk) and before commit `8059209b` ported the repository gate checks from Python to Go, raising three mismatches in `conflict-run-item-request-6c9e89a462-a1-n1` that [`D-TW-DR2`](#D-TW-DR2) resolves as follows:

1. **Governing section:** [Section 3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) (`D-TW-DR2`) is the specification that the earlier draft called `"4. The One Behavior of the Follow-Up Item"`. [Section 4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3) (`D-TW-DR3`) covers the separate follow-up migration of existing inline `D-TW` rows, which `item-request-6c9e89a462` explicitly marks out of scope.
2. **Go target files after `8059209b`:** Commit `8059209b` deleted `tools/gate/checks/change_common.py` (`run("documents", ...)`) and `tools/gate/checks/change_checks_test.py` and replaced them with [`tools/gate/change_common.go`](../../../tools/gate/change_common.go) (`runChangeCheck("documents", ...)`) and [`tools/gate/change_checks_test.go`](../../../tools/gate/change_checks_test.go) (`TestDocuments*`). `item-request-6c9e89a462` edits **only** [`tools/gate/change_common.go`](../../../tools/gate/change_common.go) and [`tools/gate/change_checks_test.go`](../../../tools/gate/change_checks_test.go) ([§3.3](#33-go-implementation-boundary-and-unit-tests)).
3. **Unified predicate and finding format:** The check implements the complete `base..HEAD` predicate in [§3.1](#31-pure-range-predicate-basehead) together with the one-line-per-violation finding contract in [§3.2](#32-refusal-output-and-cache-interaction), satisfying both the request's summary and the edge-case invariants of `D-TW-DR2`.

### 3.1 Pure range predicate (`base..HEAD`)

Evaluated over the committed `base..HEAD` range using `git diff --name-status --no-renames -z <base> <head>` and `git show <rev>:<path>` across non-excluded Markdown paths (`*.md`, excluding `.forks/`, `node_modules/`, and `.yolo/`), with rename detection disabled (`--no-renames`, so moving or renaming an existing file into `docs/design/tillr-watch/` is judged as an addition `A`, plus a deletion `D` when moved from a governed path), the `documents` check evaluates all three conditions without short-circuiting and records one violation per offending anchor or path:

1. **New `D-TW` anchor in the main watch design.** When [`docs/design/tillr-watch.md`](../tillr-watch.md) is added or modified (`A`, `M`, or `T`) in `base..HEAD`, each normalized `D-TW` anchor present in `docs/design/tillr-watch.md` at `HEAD` that was absent in `docs/design/tillr-watch.md` at `base` (in sorted ascending anchor order; empty set at `base` if `docs/design/tillr-watch.md` did not exist at `base`) is one violation. Anchors are extracted outside fenced code blocks (` ``` ` or `~~~`) from HTML `<a … id="…">` / `<a … name="…">` attributes (double- or single-quoted), ATX Markdown headings (`^#{1,6}\s+(.*)$`) normalized with `markdownSlug`, and Vantage question directives (`<!-- vantage: question id=…`), lowercased, where an identifier counts as a `D-TW` anchor when it starts with `d-tw`. Deleting existing `D-TW` anchors (as in [§4.2](#42-the-two-step-follow-up-move)), `# D-TW…` comments inside fenced code blocks, adding non-`D-TW` (`OQ-TW`) anchors, or editing prose in [`docs/design/tillr-watch.md`](../tillr-watch.md) while keeping the set of `D-TW` anchors a subset of `base`'s passes.
2. **Added record file combined with edits to the main design or an existing record file.** When the `base..HEAD` diff adds (`A`) at least one Markdown file under `docs/design/tillr-watch/` (`docs/design/tillr-watch/**/*.md`, absent at `base` and present at `HEAD`), each governed file that existed at `base` and is modified, type-changed, or deleted (`M`, `T`, or `D`) in `base..HEAD` — namely [`docs/design/tillr-watch.md`](../tillr-watch.md) or any existing `docs/design/tillr-watch/**/*.md` file (in sorted ascending path order) — is one violation. Adding one or more record files under `docs/design/tillr-watch/` without touching `docs/design/tillr-watch.md` or any existing `docs/design/tillr-watch/**/*.md` file passes, and modifying or deleting an existing record file or [`docs/design/tillr-watch.md`](../tillr-watch.md) when zero files are added under `docs/design/tillr-watch/` passes.
3. **Added record file missing `depends-on` on the main watch design.** For each Markdown file `p` added (`A`) under `docs/design/tillr-watch/` (`docs/design/tillr-watch/**/*.md`, in sorted ascending path order), `p` is one violation when its YAML frontmatter `depends-on` field (scalar, inline `[…]` list, or block `- …` list, stripping surrounding quotes and trailing ` #…` YAML comments) is absent or contains no entry that resolves — after stripping any `#<anchor>` fragment and resolving non-empty relative path `target` against `path.Dir(p)` (`path.Clean(path.Join(path.Dir(p), target))`) — to `docs/design/tillr-watch.md` (for example [`../tillr-watch.md`](../tillr-watch.md) or [`../tillr-watch.md#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5`](../tillr-watch.md#61-one-record-per-condition-never-per-pass-the-alert-episode-state-machine-d-tw5) from `docs/design/tillr-watch/<slug>.md`, or `../../tillr-watch.md` from a one-level nested subdirectory).

### 3.2 Refusal output and cache interaction

- **Execution order and cache-proof evaluation:** Inside `runChangeCheck("documents", ...)` in [`tools/gate/change_common.go`](../../../tools/gate/change_common.go), after `validateBaseHead` and the existing `len(documents) == 0`, `HEAD`-checked-out, and dirty-Markdown guards (preserving exit code `0` when zero surviving Markdown files changed and exit code `4` on invalid range or dirty working-tree state; whenever any of Clauses 1–3 fails, at least one governed `.md` file exists at `HEAD`, so `len(documents) > 0`), the check evaluates all three clauses of [§3.1](#31-pure-range-predicate-basehead) **before** calling `judgeDocuments` — before spawning `uvx vantage-check` and before consulting or writing per-document remembered `vantage-check` passes in `.git/tillr-gate/results/`. When one or more violations exist, `runChangeCheck` prints the finding lines to `stdout` (one line per violation) and returns exit code `1` immediately without running `judgeDocuments`; unexpected git read errors return exit code `4` via `failJudge`.
- **One finding line per violation (`stdout`, exit code `1`):** Every finding line starts with `<offending-path>: `, identifies which of the three clauses failed, states that each new `tillr-watch` decision record belongs in its own file under `docs/design/tillr-watch/<slug>.md` (with frontmatter `depends-on` naming [`../tillr-watch.md`](../tillr-watch.md) and zero edits to [`docs/design/tillr-watch.md`](../tillr-watch.md) or existing files under `docs/design/tillr-watch/`), and cites [`docs/design/tillr-watch/decision-record-files.md`](./decision-record-files.md):
  - **Clause 1 line:** starts with `docs/design/tillr-watch.md:` and names the newly introduced anchor `<anchor>` (for example, `docs/design/tillr-watch.md: introduces new D-TW anchor <anchor> inline (clause 1); put each new tillr-watch decision record in its own file under docs/design/tillr-watch/<slug>.md without editing docs/design/tillr-watch.md (see docs/design/tillr-watch/decision-record-files.md)`).
  - **Clause 2 line:** starts with `<modified-or-deleted-existing-path>:` and also names the added record path(s) under `docs/design/tillr-watch/` (for example, `<existing-path>: modified or deleted alongside added record <added-paths> (clause 2); put each new tillr-watch decision record in its own file under docs/design/tillr-watch/<slug>.md without editing docs/design/tillr-watch.md or existing files in docs/design/tillr-watch/ (see docs/design/tillr-watch/decision-record-files.md)`).
  - **Clause 3 line:** starts with `<added-record-path>:` and names the missing `depends-on` dependency (for example, `<added-path>: frontmatter depends-on does not resolve to docs/design/tillr-watch.md (clause 3); put each new tillr-watch decision record in its own file under docs/design/tillr-watch/<slug>.md with frontmatter depends-on including ../tillr-watch.md (see docs/design/tillr-watch/decision-record-files.md)`).
- **Degenerate ranges:** When `base == HEAD` or neither [`docs/design/tillr-watch.md`](../tillr-watch.md) nor any `docs/design/tillr-watch/**/*.md` path appears in `base..HEAD`, all three clauses pass with zero overhead and execution continues to `judgeDocuments` as normal.

### 3.3 Go implementation boundary and unit tests

- **`"Change nothing else"` scope (`item-request-6c9e89a462`):** Touch **only** [`tools/gate/change_common.go`](../../../tools/gate/change_common.go) and [`tools/gate/change_checks_test.go`](../../../tools/gate/change_checks_test.go). Do not recreate the deleted Python files (`tools/gate/checks/change_common.py`, `tools/gate/checks/change_checks_test.py`), and do not edit [`tools/gate/tree_common.go`](../../../tools/gate/tree_common.go) (`change_common.go` shares `package main` with `frontmatterEntries`, `frontmatterCitations`, and `markdownSlug` in `tree_common.go`, and `"tools/gate/change_common.go"` is already hashed in `documentCheckFiles`), [`tools/gate/checks/documents.json`](../../../tools/gate/checks/documents.json), [`tools/gate/checks/change-checks-test.json`](../../../tools/gate/checks/change-checks-test.json), `docs/`, `config/tillr/watch/`, or `tools/tillr*`. Out of scope (candidate-note only, never a reason to refuse the item): item ordering, conflict-repair retry caps, time limits, fallback merge paths, moving existing inline `D-TW` rows out of [`../tillr-watch.md`](../tillr-watch.md) ([§4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3)), and the design seat's prompt.
- **Unit tests in [`tools/gate/change_checks_test.go`](../../../tools/gate/change_checks_test.go):** Add `TestDocuments*` regression tests (automatically selected by [`tools/gate/checks/change-checks-test.json`](../../../tools/gate/checks/change-checks-test.json) and [`tools/gate/checks/go-test-gate.json`](../../../tools/gate/checks/go-test-gate.json)) covering:
  1. Clause 1 refusals across HTML `<a id="…">` / `<a name="…">`, ATX heading slug, and Vantage question `id=` forms (including multiple new `D-TW` anchors producing one finding line each) and passes when editing prose, deleting existing `D-TW` anchors, adding non-`D-TW` (`OQ-TW`) anchors, or writing `# D-TW…` inside a fenced code block;
  2. Clause 2 refusals when adding `docs/design/tillr-watch/<slug>.md` while modifying or deleting `docs/design/tillr-watch.md` or an existing `docs/design/tillr-watch/*.md` file (including `git mv` into `docs/design/tillr-watch/` caught via `--no-renames`), and passes when adding a valid record alone (or with an unrelated file outside `docs/design/tillr-watch*`) or editing/deleting an existing file with zero added record files;
  3. Clause 3 refusals when an added `docs/design/tillr-watch/<slug>.md` or nested `docs/design/tillr-watch/<sub-dir>/<slug>.md` lacks frontmatter or lacks a `depends-on` entry resolving to `docs/design/tillr-watch.md`, and passes across scalar, inline-list, and block-list `depends-on` forms (with and without `#<anchor>` fragments, and `../../tillr-watch.md` from a nested subdirectory);
  4. Multi-violation non-short-circuiting output (each line naming the path, the violated clause, where a new record belongs, and `docs/design/tillr-watch/decision-record-files.md`), degenerate ranges (`base == HEAD` and unrelated changes), and cache-proof refusal when individual blobs already hold remembered `vantage-check` passes.

---

## 4. Moving `D-TW79`, `D-TW80`, and `D-TW81` Out of the Main Document ([`D-TW-DR3`](#D-TW-DR3))

### 4.1 The three inline records added since 2026-11-24, and why follow-up items move them

Between the 2026-11-24 deletion of the earlier draft (`976800859e`) and `a6be4821f8`, three watch decisions were added inline at the tails of [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog) (subsections `5.16` through `5.18`) and [Section 9](../tillr-watch.md#9-decision-ledger) of [`../tillr-watch.md`](../tillr-watch.md). At `a6be4821f8` none of their three rule files existed under `config/tillr/watch/` (all three `.toml` rule files have since landed by `bc431c2541fd`, while `D-TW79` through `D-TW81` — along with `D-TW82` through `D-TW88`, added before [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) was enforced — still sit inline in [`../tillr-watch.md`](../tillr-watch.md)):

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

2. **Step 2 — One follow-up cleanup item on [`../tillr-watch.md`](../tillr-watch.md) (ordered after all three Step 1 items land).** A single item edits [`../tillr-watch.md`](../tillr-watch.md) alone — adding **zero** files under `docs/design/tillr-watch/` and adding **zero** `D-TW` anchors — to delete inline subsections `5.16`, `5.17`, and `5.18` from [Section 5](../tillr-watch.md#5-alert-rules-as-tunable-data-and-the-starting-rule-catalog) and rows `D-TW79`, `D-TW80`, and `D-TW81` from [Section 9](../tillr-watch.md#9-decision-ledger), and to update frontmatter `next:` and the opening `**Status:**` paragraph to the remaining inline rules, without adding any index or link list of the moved files. At `a6be4821f8` no other tracked file linked to `D-TW79` through `D-TW81`; at `bc431c2541fd`, [`fixer-held-new-attempt.md`](./fixer-held-new-attempt.md) links to `D-TW79` and [`gate-test-cpu-idle.md`](./gate-test-cpu-idle.md) links to `D-TW81`, so the Step 2 item re-runs `rg -n 'D-TW(79|80|81)|516-|517-|518-' docs tools config yolo-packs -g '!docs/design/tillr-watch.md'` before deleting them and repoints those references (and any introduced in the meantime) to the Step 1 record files. Earlier decisions `D-TW1` through `D-TW78` (and `D-TW82` through `D-TW88` unless separately migrated) remain in [`../tillr-watch.md`](../tillr-watch.md).

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

Rows prefixed `R-TW-DR` quote verbatim owner rulings with their dates. Rows prefixed `D-TW-DR` record decisions from the 2026-11-24 and 2026-11-25 operator requests and the designer (including the 2026-11-26 conflict answer on `D-TW-DR2` for `item-request-6c9e89a462`); none of `D-TW-DR1` through `D-TW-DR3` is an owner ruling, and the owner may overturn any of them. `Built` is checked against the tree at `bc431c2541fd`.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="R-TW-DR1"></a>**[R-TW-DR1](#R-TW-DR1)** | **Owner ruling** (`"ok, so save that"`): every new decision record about `tillr-watch` goes in its own small file under `docs/design/tillr-watch/`, and [`../tillr-watch.md`](../tillr-watch.md) is not edited when a record is added. | 2026-11-25 | [§1.1](#11-owner-rulings-and-provenance), [§2](#2-the-one-file-per-record-rule-r-tw-dr1-d-tw-dr1) | partly: four records under `docs/design/tillr-watch/`; enforcement not built |
| <a id="R-TW-DR2"></a>**[R-TW-DR2](#R-TW-DR2)** | **Owner ruling** (`"Explicitly, we NEVER want fallbacks anywhere in this project."`): no fallback merge, numbering, or conflict path is added. | 2026-11-16 | [§1.1](#11-owner-rulings-and-provenance), [§2](#2-the-one-file-per-record-rule-r-tw-dr1-d-tw-dr1), [§5](#5-rejected-alternatives) | yes |
| <a id="D-TW-DR1"></a>**[D-TW-DR1](#D-TW-DR1)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** unnumbered `docs/design/tillr-watch/<slug>.md` paths, topic-derived `D-TW-<TOPIC>1` anchors, zero edits to [`../tillr-watch.md`](../tillr-watch.md) or any shared index, backward-only amendments via prose and `depends-on`, each record a complete checked design doc naming [`../tillr-watch.md`](../tillr-watch.md) in `depends-on`, readers read the main doc plus `docs/design/tillr-watch/*.md` with newer amending records governing, same-file collisions use ordinary drain conflict repair, and no item ordering, retry count, or time limit. | 2026-11-25 | [§2](#2-the-one-file-per-record-rule-r-tw-dr1-d-tw-dr1), [§5](#5-rejected-alternatives) | partly: four records follow it; enforcement not built |
| <a id="D-TW-DR2"></a>**[D-TW-DR2](#D-TW-DR2)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** answering `conflict-run-item-request-6c9e89a462-a1-n1` for `item-request-6c9e89a462`, [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) (governing in place of section `"4. The One Behavior of the Follow-Up Item"` of deleted draft `bc4232f3fd` `[unlanded]`) extends `runChangeCheck("documents", ...)` in [`tools/gate/change_common.go`](../../../tools/gate/change_common.go) with `TestDocuments*` unit tests in [`tools/gate/change_checks_test.go`](../../../tools/gate/change_checks_test.go) (replacing the Python files removed in `8059209b`) and modifies no other file (`"Change nothing else"`). Evaluated over `base..HEAD` (`--no-renames`) before `judgeDocuments` and remembered `vantage-check` passes, `documents` exits `1` printing one `stdout` finding line per violation across all three clauses (each naming the offending path, which clause failed, and where a new record belongs while citing `docs/design/tillr-watch/decision-record-files.md`): (1) any new `d-tw*` HTML, outside-fence heading-slug, or Vantage-question anchor in [`../tillr-watch.md`](../tillr-watch.md) absent at `base`; (2) any commit adding `docs/design/tillr-watch/**/*.md` while modifying or deleting [`../tillr-watch.md`](../tillr-watch.md) or an existing `docs/design/tillr-watch/**/*.md` file; (3) any added `docs/design/tillr-watch/**/*.md` file whose scalar, inline-list, or block-list frontmatter `depends-on` (after stripping `#<anchor>`) has no entry resolving relative to that file's directory to `docs/design/tillr-watch.md`. Item ordering, repair retry caps, time limits, fallback merge paths, moving existing inline `D-TW` rows ([`D-TW-DR3`](#D-TW-DR3)), and the design seat's prompt remain out of scope (candidate-note only). | 2026-11-26 | [§3](#3-the-one-enforcement-the-gates-documents-check-d-tw-dr2) | no |
| <a id="D-TW-DR3"></a>**[D-TW-DR3](#D-TW-DR3)** | **Operator request and designer decision (not an owner ruling; owner may overturn):** inline decisions `D-TW79` (`5.16`), `D-TW80` (`5.17`), and `D-TW81` (`5.18`) move out of [`../tillr-watch.md`](../tillr-watch.md) in two follow-up steps — three independent single-file items adding `docs/design/tillr-watch/proved-fixer-gated.md` (`D-TW-PFG1`), `docs/design/tillr-watch/proved-fixer-queued.md` (`D-TW-PFQ1`), and `docs/design/tillr-watch/process-cpu-high.md` (`D-TW-CPU1`), followed by one cleanup item deleting `5.16`–`5.18` and `D-TW79`–`D-TW81` from [`../tillr-watch.md`](../tillr-watch.md). | 2026-11-25 | [§4](#4-moving-d-tw79-d-tw80-and-d-tw81-out-of-the-main-document-d-tw-dr3) | no |
