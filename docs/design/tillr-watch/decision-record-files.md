---
status: in-review
stage: DESIGN
tags: [tillr, tillr-watch, design-docs, landing, conflict-repair, gate]
summary: A new tillr-watch decision record goes in its own file under docs/design/tillr-watch/, so two records written at the same time merge without touching a shared line.
next: Build the documents check of Section 3, then move the records that were added inline to docs/design/tillr-watch.md after this design into their own files.
depends-on:
  - ../tillr-watch.md
  - ../landing-principles.md#2-re-application-preserves-the-change-not-the-patch
  - ../landing-principles.md#3-gate-what-will-be-the-tip-once
  - ../landing-principles.md#15-land-the-smallest-functional-change
related:
  - ../tillr-utility-contracts/tillr-watch.md
vantage:
  status-chip: true
---

# One file per `tillr-watch` decision record

**Status:** Not built. The gate check of [§3](#3-the-one-check-that-enforces-it) is not in
[`change_common.py`](../../../tools/gate/checks/change_common.py)
(`rg -n 'tillr-watch' tools/gate/checks/change_common.py` returns nothing), this document is the only
file under `docs/design/tillr-watch/`, and [the `tillr-watch` design](../tillr-watch.md) still takes
new records inline.

> **In short.** Two design changes that each add a decision record to one long document edit the
> same lines and cannot both land. Putting each new record in its own file makes them disjoint, so
> the lander's three-way merge takes both.

**Needs your ruling:** None. [D-TW-DR1](#D-TW-DR1) is the designer's decision; the owner has not
ruled on it and may overturn it.

---

## 1. Why inline records collide

[The `tillr-watch` design](../tillr-watch.md) holds every decision in one file. Adding a record
there edits up to six shared regions:

1. the frontmatter `next:` line;
2. the opening status paragraph and its list of unbuilt rules;
3. the tail of its rule catalog, where the next numbered subsection is appended;
4. the summary above its decision ledger;
5. existing ledger rows, when the new record amends one in place;
6. the last line of the ledger table, where the next sequential id is claimed.

The lander's `Trunk.Squash` ([`trunk.go`](../../../tools/tillr-land/land/trunk.go)) three-way
merges a candidate onto the trunk. Two candidates from one base that both append to the same table
or both claim the same next id fail that merge with a content conflict, and each conflict costs a
full repair run on the design seat: re-read the document, renumber the id and the subsection,
repair the internal links, re-run the document check.

**Measurement.** In one 78-minute window the lander recorded eight squash conflicts on that file.
One repair run took 29 minutes; a second record landed nine seconds after the repair took its
base, so the finished repair conflicted again. The repair costs far more than the gap between
landings, so the cost rises with landing frequency, which
[the landing principles](../landing-principles.md) rule out
([§2](../landing-principles.md#2-re-application-preserves-the-change-not-the-patch),
[§3](../landing-principles.md#3-gate-what-will-be-the-tip-once)).

## 2. The placement rule

A new `tillr-watch` decision record is one new file and no other edit.

| Rule | What it requires |
| :--- | :--- |
| **Path** | `docs/design/tillr-watch/<slug>.md`, where the slug is descriptive kebab-case naming the rule and the specific decision. No sequential number prefix (two concurrent runs would claim the same one) and no item or run id. |
| **Ids** | The file's own decision ledger uses topic-scoped anchors (`D-TW-<TOPIC>1`, `OQ-TW-<TOPIC>1`) and never continues the sequential `D-TW<N>` series of the main document. |
| **Frontmatter** | A complete design document header whose `depends-on` names [the `tillr-watch` design](../tillr-watch.md) and any anchor the record extends or amends. |
| **No shared edits** | The change touches no line of the main document, no earlier record file, and no hand-kept index. |
| **Amendments point forward** | A record that narrows or replaces an earlier decision says so in its own text and `depends-on`; the earlier file is not edited. Where two records disagree, the one that names the other as amended governs. |

An in-place edit to the main document or to an existing record file is still correct when that
record itself is the subject, for example recording an owner ruling on an existing row or fixing a
link.

**Reading the set.** A reader takes the main document together with every tracked file matching
`docs/design/tillr-watch/*.md`.

**What the merge sees.** Two candidates that each add a different file under one directory touch
disjoint tree entries, so `Trunk.Squash` merges them with no conflict however many records land
per hour. Two candidates that pick the same path, or both edit one record, conflict as any
same-file edit does and go through the drain's ordinary conflict repair, on a short file.

## 3. The one check that enforces it

One behavior, with its unit tests in the same change
([§15 of the landing principles](../landing-principles.md#15-land-the-smallest-functional-change)).
The `documents` change check in
[`change_common.py`](../../../tools/gate/checks/change_common.py), tested in
[`change_checks_test.py`](../../../tools/gate/checks/change_checks_test.py), fails a `base..HEAD`
range that does any of:

1. adds a `D-TW` anchor to `docs/design/tillr-watch.md` that the base did not have;
2. adds a Markdown file under `docs/design/tillr-watch/` while also modifying the main document
   or an existing file in that directory;
3. adds a Markdown file under `docs/design/tillr-watch/` whose `depends-on` does not name the main
   document.

The finding names the path and states the placement rule. The check reads the diff of the range,
so a remembered per-file check result cannot skip it.

It adds no ordering between items, no retry count, no time limit and no second merge path.

## 4. Rejected alternatives

| Alternative | Why not |
| :--- | :--- |
| Order same-document design items with dependencies or path locks | Turns independent items into a serial queue whose wait grows with its depth. |
| Cap repair attempts, time-limit a repair, or merge another way on conflict | Hides the cause; the repository takes no fallbacks ([`D-JL24`](../tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27)). |
| Keep one file and split it into subsections | The frontmatter, the status paragraph, the sequential id and the ledger tail are still shared. |
| Separate files plus a link row in a shared index | The index tail becomes the new shared line. |
| Sequentially numbered filenames | Two runs from one base pick the same number. |

## 5. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-TW-DR1"></a>D-TW-DR1 | Designer's decision, not an owner ruling. Each new `tillr-watch` decision record is its own file under `docs/design/tillr-watch/` with topic-scoped ids, forward-only amendments and no edit to a shared file; one `documents` check refuses the inline form. No item ordering, retry count, time limit or fallback. | 2026-11-24 | [§2](#2-the-placement-rule), [§3](#3-the-one-check-that-enforces-it) | No |
