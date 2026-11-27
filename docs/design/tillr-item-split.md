---
title: "An oversized item lands in parts: what is accepted lands, and what is objected to goes back alone"
status: in-review
stage: DESIGN
tags: [tillr, drain, verify, landing, split, design]
summary: "A change too big to land whole is broken up so the accepted parts land without the part objected to, and a broken trunk or other verifier-less item with several causes lands one cause at a time. Five callers may call a split — the operator at filing, the design seat, the planner, the verifier and the gate — and the design seat alone writes the division; each part is an ordinary item under a parent that stays open until a closing check passes. A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on. S1 is measured; S11 (a new failing set at a gate refusal) is measured over 102 gate refusals and awaits OQ-SP2 on promotion. Slices 1, 2 and 4 of the build are in the tree; slices 3 and 5 to 15 are not."
next: "Rule OQ-SP2 (whether gate-refusal signal S11 stays advisory until more test-level refusals accumulate or promotes now). Slices 3 and 5 to 14 of Section 14 do not wait on it; Slice 8 may route on the measured signal S1 alone, and Slice 15 waits on OQ-SP2."
depends-on:
  - landing-principles.md
  - tillr-judge-and-lifecycle.md
  - ../reference/tillr-item-graphs.md
vantage:
  status-chip: true
---

# An oversized item lands in parts

**Status:** Three of fifteen build slices are in the tree ([§14](#14-the-build-in-slices-that-each-land-alone)): every refused candidate's size is recorded beside its limits, a not-done verdict states each finding's kind and place, and a split statement written by the design seat is checked and filed in one transaction. Nothing yet calls a split automatically on an item with no verifier, and gate-refusal signal `S11` awaits [`OQ-SP2`](#OQ-SP2). The owner's statements quoted in [§1](#1-the-ruling) are the only rulings; every other decision is the designer's (or from an operator request), which the owner may overturn, and is marked so in the [Decision Ledger](#decision-ledger).

> **In short.** An item that is too big is split into **parts**, each an ordinary item with its own words, criteria, gate and single squashed commit (and its own verifier when its workflow carries one), under a **parent** that keeps its bar unchanged and stays open until every part has landed. On an item with no verifier and no owner words — a trunk fixer first — each part owns one cause of failure and its assigned failing checks and tests, and the parent closes when the full fixer gate passes on the trunk ([§7.7](#77-items-with-no-verifier-trunk-fixers-first)). The work already built is taken from the refused candidate and not rebuilt, and what decides whether a part may land is whether it makes the trunk worse, not whether the whole item is finished.

**Why it matters.** An oversized item can be refused by its verifier again and again, each time on a different case, while its candidate grows — and an item with no verifier, such as a trunk fixer, can be refused by the gate on a different set of failing tests each time while the trunk stays unproved ([§2](#2-the-case-measured)).

**The shape.** Five callers and one divider. The operator at filing, the design seat, the planner, the verifier and the gate each **call** a split, only on signals whose triggers are measured from the store; the design seat alone writes the division; the drain files the parts.

**Cost.** One more design run per split, one gate (and one verify where the workflow has one) per part, and a trunk that may carry a behavior whose remaining cases — or a broken trunk whose remaining causes — are still queued. The seams between parts become a place where a defect can hide, which is what the closing check in [§7.5](#75-when-the-parent-is-finished) and [§7.7](#77-items-with-no-verifier-trunk-fixers-first) is for.

**Start at** [§4](#4-the-rules-a-split-keeps), the seven rules, [§7.7](#77-items-with-no-verifier-trunk-fixers-first), how items with no verifier split, and [§8](#8-how-a-verdict-accepts-part-of-a-candidate).

**Needs your ruling:** [`OQ-SP2`](#OQ-SP2).

**Awaits your review:** the designer's decisions `D-SP1` to [`D-SP18`](#D-SP18) in the [Decision Ledger](#decision-ledger).

**Reads with:** [landing principles](./landing-principles.md) (the rules a split must not break), [the judge and lifecycle design](./tillr-judge-and-lifecycle.md) (verdicts, and the rule that no count of attempts routes work) and [item graphs](../reference/tillr-item-graphs.md) (pieces inside one item, which a split does not replace).

**Words.** [The vocabulary](./tillr-vocabulary.md#5-the-words) defines item, criterion, candidate, lane and verdict. These are coined in this document:

- **Split**: replacing one item's single landing with several items that land separately. It is not the planner's division of an item into pieces, which stays inside one item and one landing.
- **Split call**: a recorded statement that an item should be split, naming who made it and the measured signals behind it. It decides nothing: the design seat answers it.
- **Split statement**: the design seat's answer to a call. It lists the parts, divides the criteria and says which built work each part takes. Its other possible answer is **no split**, with the reason.
- **Part**: an ordinary item filed from a split statement. It is not a piece: a piece is a step inside one item.
- **Parent**: the item that was split. It keeps its bar — the owner's words on a request, or the generated criterion on a repair — and its criteria, is never built again, and waits on its parts.
- **Donor candidate**: the parent's last candidate, kept at a Git ref, from which parts take work. It is not a verified candidate of any part.
- **Gap**, **regression**, **unasked change**: the three kinds of verifier finding, defined in [§8.1](#81-what-the-verifier-states).
- **Closing check**: the check of the parent's own bar against the trunk after its last required part lands — a read-only verification of the parent's criteria on a worded request ([§7.5](#75-when-the-parent-is-finished)), or the full fixer gate on the trunk tip for a trunk fixer ([§7.7](#77-items-with-no-verifier-trunk-fixers-first)).

---

## 1. The ruling

The owner ruled on 2026-11-21 (`R-SP1`):

> "We need a way to protect against these mega items. We can change policies about filing them and try to protect that way, but inevitably ones will skip, ones will slip through, so we need a strategy for landing these large changes, which means that after some threshold or something, we just need to break them up. […] if there's a change that's really five changes, we need to be able to land four of them without the one that's been objected against. […] Maybe like the designer can say split up, the planner can say split up, the judge can say split up at various points. But they should have clear guidance."

What that rules:

1. Filing policy alone is not the protection. Oversized items will get through, so the drain needs a way to land them.
2. Past some threshold the change is broken up.
3. The accepted parts of a change land without the part that is objected to.
4. Whoever may say "split" has clear guidance.

Asked whether part of a change may land while a named case of the request is still unmet, the owner ruled the same day ([`OQ-SP1`](#OQ-SP1)):

> "part of designs can absolutely land. this is generally MORE healthy. Remember, we're trying to mirror humans here. Humans desire to land the smallest functional change possible to make reviewing, testing, velocity, all this stuff easier, and it will also make it easier for us."

5. Part of a change may land while a named case of the request is still unmet. The case follows as its own part.
6. The smaller landing is the healthier one ([landing principle 15](./landing-principles.md#15-land-the-smallest-functional-change)).

The first statement leaves open which seats say "split" and when, what the threshold is, and whether a split is several landings of one item or something else. Those are settled below as the designer's decisions.

A third ruling bounds the thresholds (`R-SP2`, 2026-11-23): the owner objected to a size limit whose origin nobody could state, "we have random thresholds again breaking things?" His standing rule is that no arbitrary time limit, retry count or attempt limit routes or stops work ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)). Applied here (`D-SP15`): **a signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on.**

## 2. The case, measured

The design rests on four measurements from the store and the repository: two non-converging items — one with a verifier and one without — and two population measurements over the store's landings and gate refusals.

**One oversized item.** A `planned-build@v1` item with seven criteria was refused by its verifier twelve times in a row. `git diff --shortstat <base> <head>` for each refused head, excluding the derived pack copy under `yolo-packs/`, shows the candidate growing at every refusal: 34 files and 2,189 added lines at the first, 64 files and 5,427 added lines at the twelfth, across four tool directories. Each refusal named a case the earlier ones had not.

**What a normal landing looks like.** The same measurement over the trunk commit of each of 1,049 landings the store records:

| Measure | Median | 90th percentile | 95th | 99th |
| :--- | ---: | ---: | ---: | ---: |
| Files changed | 2 | 19 | 27 | 45 |
| Lines added | 75 | 896 | 1,198 | 2,394 |
| Distinct code areas (`tools/<module>`, `docs/<dir>`, and the like) | 1 | 4 | 5 | 8 |

**Size and refusals go together, on a small sample.** Of 95 landed `planned-build@v1` items, the 19 that landed above the 95th percentile on files or added lines averaged 3.3 verifier refusals; the other 76 averaged 1.1. This measures the size that landed, not the size at the first refusal.

**One trunk fixer with no verifier (`2026-11-27`).** On 2026-11-27, the trunk fixer for red commit `81cd910659bb` (`single-worker@v1`, which has no verifier) built 15 candidates without converging while the trunk proof failed 38 to 42 tests in `tools/tillr-drain`, and the operator had to file the split by hand after the owner asked why the trunk was still unproved. The store recorded 14 refusals on the item (10 at the gate, 3 merge conflicts and 1 no-op) and no split call or split event: `S1` to `S4` are read only at a verifier's verdict, `FileSplit` refused an item with no saved request words, and a gate failure was listed as not a signal. Across its 10 gate refusals (9 consecutive gate-refusal pairs), reading leaf failing cases from triage blocks and Go test logs (`--- FAIL:`) gives failing-set sizes `8 -> 12 -> 1 -> 16 -> 4 -> 1 -> 1 -> 34 -> 3 -> 5`: attempt 13 failed 34 leaf cases across 17 shards (truncated at 4,000 bytes in the returned summary, where the live triage view showed 8 tests with 1 in common with attempt 14), attempt 14 shrank to 3 of those cases, and attempt 15 failed 5 completely different tests with none in common with attempt 14. Eight of the nine consecutive gate-refusal pairs had no test in common. Yet in the store's refusal payload alone (`checks[].failing`, populated only from `fork-check: triage --` blocks), 7 of those 9 pairs collapsed to the bare check ID `go-test-tillr-drain` because the fixer's named-check log wrote `go-test-shards` and `--- FAIL:` lines with no triage block.

**Gate refusals across the store (`2026-11-27`, `.tillr/state.db` through `events.seq = 118561`).** Measured read-only from the repository root over every candidate gate refusal (`kind = 'drain.refusal'`, `step = 'gate'`) and every landed commit (`kind = 'land.landing'`, `result = 'landed'`), unioning structured triage cases in `checks[].failing` with leaf `--- FAIL:` test names in `checks[].log_ref` (and falling back to a non-wrapper `check_id` only when a check names no test cases):

```sh
python3 - <<'PY'
import json, os, re, sqlite3

con = sqlite3.connect("file:.tillr/state.db?mode=ro", uri=True)
max_seq = 118561
gate_rows = con.execute("""
  SELECT seq, json_extract(payload, '$.item_id'), payload FROM events
  WHERE seq <= ? AND kind = 'drain.refusal' AND json_extract(payload, '$.step') = 'gate'
  ORDER BY seq
""", (max_seq,)).fetchall()
landings = {}
for seq, item_id in con.execute("""
  SELECT seq, json_extract(payload, '$.item_id') FROM events
  WHERE seq <= ? AND kind = 'land.landing' AND json_extract(payload, '$.result') = 'landed'
  ORDER BY seq
""", (max_seq,)):
    landings.setdefault(item_id, []).append(seq)

by_item = {}
for seq, item_id, raw in gate_rows:
    p = json.loads(raw)
    cases = set()
    for ch in p.get("checks") or []:
        cid = ch.get("check_id", "")
        added = False
        for fc in ch.get("failing") or []:
            fid = fc.get("id") or cid
            for cs in fc.get("cases") or []:
                if cs.get("name"):
                    cases.add(f"{fid}:{cs['name']}"); added = True
            if not (fc.get("cases") or []) and fid and not fid.startswith("gate:"):
                cases.add(fid); added = True
        if not added:
            lr = ch.get("log_ref", "")
            whole = lr[:-len(".triage.log")] + ".log" if lr.endswith(".triage.log") else lr
            path = whole if os.path.exists(whole) else (lr if os.path.exists(lr) else "")
            if path:
                fails = set(re.findall(r"---\s+FAIL:\s+(\S+)", open(path, errors="replace").read()))
                for t in {t for t in fails if not any(o != t and o.startswith(t + "/") for o in fails)}:
                    cases.add(f"{cid}:{t}"); added = True
            if not added and cid and not cid.startswith("gate:"):
                cases.add(cid)
    if not cases:
        cases = {rc["cause"] for rc in (p.get("refusal_causes") or []) if rc.get("cause")}
    by_item.setdefault(item_id, []).append((seq, cases))

single = [it for it, rs in by_item.items() if len(rs) == 1]
single_landed = [it for it in single if any(l > by_item[it][0][0] for l in landings.get(it, []))]
print(f"gate_refusals={len(gate_rows)} items={len(by_item)} single={len(single_landed)}/{len(single)}")
buckets = {"disjoint_new": [], "unchanged_or_shrinking": [], "partial_overlap_new": []}
for it, rs in by_item.items():
    lseqs = landings.get(it, [])
    for (s1, f1), (s2, f2) in zip(rs, rs[1:]):
        if any(s1 < l < s2 for l in lseqs):
            continue
        landed = any(l > s2 for l in lseqs)
        key = "disjoint_new" if (f1 and f2 and f1.isdisjoint(f2)) else (
              "unchanged_or_shrinking" if f2.issubset(f1) else "partial_overlap_new")
        buckets[key].append((it, len(f1), len(f2), landed))
for k, rows in buckets.items():
    items = {r[0] for r in rows}
    items_landed = {r[0] for r in rows if r[3]}
    print(f"{k}: pairs_landed={sum(r[3] for r in rows)}/{len(rows)} items_landed={len(items_landed)}/{len(items)}")
PY
```

The store through `seq = 118561` holds **102 gate refusals across 77 items**: 67 items had a single gate refusal and **60 of 67 (89.6%) landed** afterward; 10 items had two or more gate refusals (25 raw consecutive pairs, or **23 consecutive pairs within a landing cycle across 8 items** once pairs separated by an intervening landing on 2 reopened items are not compared across cycles):

| Consecutive gate-refusal relation within a landing cycle | Pairs | Followed by a landing | Items | Items that landed |
| :--- | ---: | ---: | ---: | ---: |
| Changed: every earlier failure gone and new ones present (`S11`) | 22 | 13 (59.1%) | 8 | 6 (75.0%) |
| Unchanged or shrinking (every current failure was in the previous set) | 1 | 0 (0.0%) | 1 | 0 (0.0%) |
| Partial overlap with new failures | 0 | 0 (—) | 0 | 0 (—) |

Without the landing-cycle reset across all 25 raw pairs on all 10 multi-refusal items, 23 pairs were disjoint with new failures (14, or 60.9%, followed by a landing), 1 shrank (0 landed), and 1 partially overlapped with new failures across an intervening landing (12 to 15 with 5 shared; 0 landed). In the store's refusal payload alone, before reading `--- FAIL:` from the check logs, the 23 within-cycle pairs looked like 11 disjoint-with-new pairs (9, or 81.8%, followed by a landing) against 12 check-identical pairs (4, or 33.3%, followed by a landing) — an artifact of 7 of the 2026-11-27 trunk fixer's 9 pairs collapsing to bare `go-test-tillr-drain`. Requiring three or more failing cases in both consecutive sets isolated the non-converging 2026-11-27 trunk fixer (3 pairs: `8 -> 12`, `16 -> 4` and `3 -> 5`; 0 of 3 landed) from all 6 landed items (0 such pairs), on a single non-converging item.

Six observations follow:

1. **The first verifier-refused candidate was already past the 95th percentile on both measures.**
2. **Each refusal named a different case or a different set of tests.** The repeated-finding hold fires only when the same finding repeats on unchanged evidence ([the drain's reference](../../tools/tillr-drain/README.md)), so twelve different verifier findings and ten changing gate refusals moved nothing.
3. **The criteria recorded none of what was accepted.** Every verifier refusal marked the first criterion not done, and a trunk fixer's single `trunk-green` criterion fails the same way whether 34 tests fail or 3 fail.
4. **The planner has no guidance on size.** `rg -n -i 'split|size|independent|large' config/tillr/plan-guidance.md` finds nothing.
5. **The store's gate-refusal payload hid changing test sets behind suite check IDs.** Until `drain.refusal` extracts failing Go test cases from check logs as well as triage blocks, a trunk fixer swapping failing tests inside one suite check looks unchanged in SQLite.
6. **Bare one-occurrence `S11` fires on ordinary two-step convergence, while the multi-failure separation rests on one item.** Fixing a failing test often uncovers a single fast check (`go-vet`, `documents`, `elapsed-time` or `TestArchitecture`) on the next gate run before landing (`6` of `8` items landed, including an earlier trunk fixer that landed after three consecutive disjoint gate refusals), whereas the three-or-more-failures cut-off rests on `n = 1` non-converging item and a `1`-pair shrinking baseline. Under P7, `S11` enters as advisory and [`OQ-SP2`](#OQ-SP2) puts its promotion to the owner.

## 3. What exists today

| Mechanism | What it does | What it cannot do |
| :--- | :--- | :--- |
| Pieces (`planned-build@v1`, `build-pieces@v1`) | The planner divides an item into at most 12 pieces that run in order on one lane | Land separately: all pieces share one verifier, one gate and one squashed commit ([item graphs](../reference/tillr-item-graphs.md#lane-lifecycle-write-scopes-and-chained-commits)) |
| `blocked_by` edges | One item waits on another ([the item utility's guide](../../tools/tillr-item/README.md)) | Say that several items are one request |
| Verifier return | A not-done verdict sends the findings to the build step on the same candidate | Keep any of the candidate out of the next verdict |
| Repeated-finding hold | Holds an item when the same finding repeats on unchanged evidence | Notice findings that differ every time |
| Run pins (`refs/tillr/runs/<run>`) | Keep a run's committed head | Say which of that work a later item should take |
| The deny-list | Refuses any worker's change to the store, the fork caches or an item declaration | Limit an item to paths: there is no per-item path list |
| A filed split statement (`FileSplit`) | Files the parts, their order and the blocked parent in one transaction | Call a split on a verifier-less item, split an item that has no saved request words, carve a donor, or close a trunk-fixer parent ([§7.7](#77-items-with-no-verifier-trunk-fixers-first), [§14](#14-the-build-in-slices-that-each-land-alone)) |
| Trunk fixers and `single-worker@v1` | Run one `build` step with no verifier; trunk fixers carry no saved owner words, only a generated `trunk-green` criterion | Ever reach a verifier, so `S1` to `S4` are never read on them |
| Gate refusals (`step = "gate"`) | Return a candidate's failing checks to its worker | Persist failing Go test cases when a check log has no triage block, or compare consecutive failing sets |

An item's states are listed in [item states](../reference/tillr-item-states.md).

## 4. The rules a split keeps

- **P1. A split changes the shape of the work, never its bar.** The parent keeps its statement — the owner's words on a request, or the generated criterion on a repair — and its criteria unchanged. It is finished only when every active required part has landed and the closing check passes. Only the owner drops a required part without replacement; a required part closed as defective is replaced by the design seat ([§7.6](#76-failure-and-edge-cases)).
- **P2. A part is an ordinary item.** It has its own words, its own criteria, its gate and one squashed commit, plus its own verifier when its workflow carries one. The fast candidate gate and the lander do not change.
- **P3. One seat writes the division.** Five callers may call a split. The design seat alone decides whether and how, because dividing criteria or failure causes is writing the bar, and neither the worker that is held to a bar nor the judge that applies it may write it ([criterion repair](./criterion-repair.md#decision-ledger) rules the same for a frozen criterion).
- **P4. A measured signal calls a split; nothing here stops work.** No signal counts attempts or refusals. A call never holds the item on the owner.
- **P5. Built work is taken, not rebuilt, and no verdict is inherited.** A part takes its share from the donor candidate. It is then verified and gated on the tree that will land, like any item.
- **P6. On a part, a gap is filed and a regression is refused.** A part is refused for making the trunk worse, for changing what nobody asked for, for lacking its tests, or for not delivering the share it claims. A case of the parent's request that the part does not yet handle is recorded against the parent and built by another part ([`OQ-SP1`](#OQ-SP1)).
- **P7. A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on.** A threshold that routes work must come from a measurement in the store, and one that has none stays an advisory displayed fact (`D-SP15`).

## 5. Who calls a split, and when

A split call is made at five points. Each caller states facts in fixed fields; the drain measures what can be measured ([§6](#6-the-signals)). A call that routes an item to the design seat may rest only on a **measured** signal, and names the caller, the time, and each measured signal with its value and its threshold. **Advisory** signals are recorded and shown without recording a call or changing what runs next.

| Caller | The point | How the call is made | What happens to the item meanwhile |
| :--- | :--- | :--- | :--- |
| **Operator, at filing** | When filing the request, before any run | Filing records the facts for `S6`, `S8`, `S9` and `S10`, and records a call only when a measured filing signal holds | On a call, the item goes to the design seat first and no plan or build run starts; otherwise the readings are shown and the item proceeds |
| **Design seat** | While writing a design that will be built, and whenever an item is in its hands for another reason | It writes the split statement directly; no separate call is needed | For a design, the build request is filed with the statement, so the parts are filed at once |
| **Planner** | At the plan step, before any writing run | It records `S5`, `S6` and `S7` on its plan, and may end its run with a call in place of a piece list, with its proposed grouping attached, only when a measured plan signal holds | On a call, no build run starts and the item goes to the design seat; otherwise the piece list runs |
| **Verifier** | At a not-done verdict | It states each finding's kind and place, each earlier finding's fate, and any paths it did not examine ([§8.1](#81-what-the-verifier-states)); the drain compares that with its measurements, shows every reading, and records the call only when a measured verdict signal holds | On a call, the candidate is pinned as the donor, the lane is released, and the item goes to the design seat; otherwise the findings return to the build step |
| **Gate** | At a candidate gate refusal (after base replay rules out `trunk-already-red` on an ordinary item, and directly on a fixer's gate refusal) | The drain extracts the failing checks and leaf failing tests from triage blocks and test logs, compares that set with the previous gate refusal's set since the item's last landing (`S11`), shows both on the item page, and records a call only when a measured gate signal holds ([`D-SP17`](#D-SP17), [`OQ-SP2`](#OQ-SP2)) | On a call, the candidate is pinned as the donor, the lane is released, and the item goes to the design seat; otherwise the gate refusal returns to the worker as today |

Three boundaries:

- **Separate asks are separate requests, not a split.** When the owner asked for two unrelated things, the operator files two requests, each quoting its own words. A split is for one ask that is too big.
- **A verifier that accepts the whole candidate ends the matter.** Size alone never splits a candidate that was judged done; it goes to the gate.
- **A call during a run waits for that run to settle.** No call interrupts a run. A second call on an item that has one pending adds its signals to the first.

## 6. The signals

Each signal is a fact someone can check. The drain reads the candidate and verdict comparisons itself at the point named; a seat states the others in a fixed field of its output, with the evidence. Every signal carries one of two standings:

- **Measured**: both the quantity and the trigger cut-off come from a measurement over the store recorded here. One measured signal that holds is enough for a split call once its caller's slice in [§14](#14-the-build-in-slices-that-each-land-alone) is built. `S1` is the only measured signal.
- **Advisory**: the trigger has not been measured from the store. An advisory signal is displayed beside its provisional reference line so operators can read it and store history can accumulate, and it never records a split call or routes an item. `S2` to `S11` are advisory ([`OQ-SP2`](#OQ-SP2) asks whether `S11` stays advisory or promotes).

| # | Signal | Read at | Provisional trigger | Standing | Measurement, or what is missing |
| :--- | :--- | :--- | :--- | :--- | :--- |
| S1 | **Candidate size.** The refused candidate exceeds a size limit, by `git diff --numstat` from its recorded base to its head, excluding the derived pack copy | A not-done verdict | More than 27 files, or more than 1,200 added lines | **Measured** | The 95th percentile of 1,049 landed commits (27 files, 1,198 added lines), and the refusal rates of [§2](#2-the-case-measured) |
| S2 | **A new finding after a fix.** The verifier marks every finding it returned last time as fixed, and names a finding at a place none of them named | A not-done verdict, compared with the one before it | One occurrence | Advisory | How often such a verdict is followed by convergence, across items; needs slice 3's records |
| S3 | **The repair is growing.** The candidate has more source files or more added lines than the last refused candidate, and S2 holds | A not-done verdict | Any increase | Advisory | The distribution of size deltas between consecutive refusals, separating ordinary repairs from non-converging growth |
| S4 | **Not all examined.** The verifier lists paths of the candidate it did not examine | A not-done verdict | Any path | Advisory | How often unexamined paths are followed by a refusal or a gate failure; needs slice 3's records |
| S5 | **Independent groups of pieces.** The plan's writing pieces fall into two or more groups, where no piece in one group needs a piece in another, and each group could be tested and landed alone | The plan step | Two groups, when the plan also has six or more writing pieces or S6 holds | Advisory | Refusal rates by piece count and group count across plans |
| S6 | **Code areas.** The plan's writing pieces, or the request's words, name more code areas than a limit | The plan step; filing | More than 4 areas | Advisory | 4 is the 90th percentile of landed commits, not a cut-off measured before a build against refusal outcomes |
| S7 | **More pieces than fit.** The planner needs more pieces than a piece list may hold | The plan step | More than 12, the `build-pieces@v1` cap | Advisory | Outcomes of plans that reach the cap; the cap is a schema limit, not a measured trigger |
| S8 | **Independent behaviors.** The words name behaviors that could each be tested and landed if the others never shipped | Filing; design | Three or more | Advisory | Refusal rates by count of independent behaviors |
| S9 | **Criteria that do not fit.** The request needs more than the four extra criteria a filing accepts, or one criterion joins several testable clauses | Filing | More than four extra, or any criterion with three or more clauses | Advisory | Refusal rates by criterion and clause count; the filing cap is a command-line limit, not a measured trigger |
| S10 | **Inherited candidate.** The request hands the worker a commit from an item that was refused | Filing | Any | Advisory | Convergence of requests built on a refused commit against requests built from the trunk |
| S11 | **A new failing set at the gate.** The non-empty set of failing checks and leaf failing tests differs from the previous gate refusal's non-empty set since the item's last landing, with every earlier failure gone and new ones present. The gate's form of `S2`; needs no verifier | A candidate gate refusal (`step = "gate"`), compared with the one before it in the same landing cycle | One occurrence (or three or more failures in both sets; [`OQ-SP2`](#OQ-SP2)) | Advisory ([`OQ-SP2`](#OQ-SP2)) | Over 102 gate refusals across 77 items ([§2](#2-the-case-measured)): 13 of 22 within-cycle disjoint-new pairs (59.1%), across 6 of 8 items (75.0%), went on to land, against 0 of 1 shrinking pair and 0 identical pairs; requiring three or more failures in both sets matched only the non-converging 2026-11-27 trunk fixer (3 pairs, 0 landed vs 0 on landed items), on `n = 1` item |

How each caller uses them:

- **Operator at filing** reads `S6`, `S8`, `S9` and `S10` from the words before filing.
- **Design seat** reads `S8` over its own design, and every signal shown on an item already in its hands.
- **Planner** reads `S5`, `S6` and `S7` over its own plan.
- **Verifier** states the facts behind `S2` and `S4`. The drain reads `S1` and `S3`, compares for `S2`, shows all four on the item page, and acts on `S1` alone.
- **Gate** extracts the failing checks and leaf failing tests (from both triage blocks and Go test logs) at every candidate gate refusal, compares that set with the previous gate refusal since the item's last landing for `S11`, shows the set and comparison on the item page from the first gate refusal, and records no split call unless [`OQ-SP2`](#OQ-SP2) promotes `S11` ([`D-SP17`](#D-SP17)).

**The numbers are settings, and configuration alone never promotes an advisory signal.** `S1`'s two limits are `file_limit = 27` and `added_line_limit = 1200` under `[candidate_size]` in [`.tillr.toml`](../../.tillr.toml), so a change to a reference line is a reviewed commit. The item page shows each reading beside its reference line on every refusal, whether or not a signal held, so store evidence accumulates against real items. Promoting a signal from advisory to measured requires measuring its trigger from the store and recording that measurement in this table before any slice routes on it.

**What is not a signal.** The number of attempts, the number of refusals, elapsed time, a single gate failure on its own, a `trunk-already-red` acquittal, a gate failure whose failing set repeats or shrinks, a merge conflict, an adapter fault, a growing test suite on its own, and a broad or short request. A repeated identical finding stays with the repeated-finding hold.

## 7. What a split produces

### 7.1 The split statement

The design seat answers a call with one of two things. A third answer, which keeps the item and defers named cases to requests of their own, is proposed in [the request amendment design](./tillr-request-amendment.md).

**No split**, with the reason: the invariant the pieces share, or why no part could land alone. The item returns to where it was: to the plan step, or to the build step with the verifier's findings. Signals already answered are not raised again; a later call needs evidence the answer did not see.

**A split statement**, which holds:

1. **Two or more parts.** A statement with one part is refused. There is no upper limit; the guidance is that each part should itself sit under S1's limits.
2. **For each part:** a label, a title, its **share** (what it delivers, in sentences a verifier can test), what it **leaves to named sibling parts**, its criteria, the parts it must follow, and whether it is required.
3. **A coverage table:** every criterion of the parent, clause by clause where one criterion spans parts, against the part that owns it.
4. **For each part, what it takes from the donor** ([§7.4](#74-carving-the-donor)), when there is a donor.
5. **The findings each part inherits**, word for word, when the call came from a verdict.

### 7.2 Parts and the parent

The drain files the statement in one store transaction: every part as an item, every edge, and the parent's change of standing. Either all of it is stored or none. Filing is keyed by parent and part label, so a replay after a crash files nothing twice.

- **A part** is filed like a request. Its words are the owner's words for the parent, verbatim, followed by its share and what it leaves to which siblings. It gets the four criteria every request gets, read against those words, and up to four of its own. It inherits the parent's express standing and takes the parent's place in the queue.
- **Order between parts** is a `blocked_by` edge, written only where one part builds on another. Parts with no edge between them run in separate lanes at once and land in either order.
- **The parent** becomes `blocked`, waiting on every required part. It is never dispatched to a plan or build step again. Items that waited on the parent keep waiting on it, and are released when it finishes.
- **A part may be split again**, by the same rules. It is then a parent itself, and its own parent waits on it as before.

### 7.3 How criteria are divided

- **The parent's criteria do not change.** No text is edited, weakened or retired by a split.
- **Each criterion of the parent's own is owned whole by one part where it can be.** Where one criterion joins clauses that belong to different parts, the statement quotes each clause and names its part.
- **A criterion that can only be shown end to end** is owned by the last required part, or left to the closing check, and the statement says which.
- **The four standard criteria are not divided.** Each part carries its own copy, judged against the part's words. The parent's copies are judged by the closing check.
- **Nothing is left unowned.** The design seat checks that every clause has an owner before the statement is accepted, and the closing check verifies the result on the trunk.

### 7.4 Carving the donor

When a split follows a verdict or a gate refusal, the parent's last candidate is the donor. It stays at its run pin, and the parent's lane is released.

The statement gives each part one of three instructions:

| Instruction | When | What the part's first step is |
| :--- | :--- | :--- |
| **Takes the donor whole** | Every finding is a gap, and a verifier examined the whole candidate (never after a gate refusal or on an item with no verifier) | None. The donor's head is the part's candidate, and its first run is its verifier |
| **Takes named work** | The part's share is a subset of the donor | A build run that starts from the current trunk, takes the named paths from the donor, separates the shared files the statement lists, and makes the share build and pass its tests |
| **Takes nothing** | The share was not built, or what was built for it is what the verifier or gate refused | An ordinary build run, given the findings or failing set |

Rules for the carving run:

- **It takes; it does not rebuild.** Its brief names the donor and the paths. A change beyond the taken work is allowed only where the share cannot stand alone without it.
- **The named paths are instructions, not a fence.** No per-item path list exists, and the split does not add one. Whether the part changed only what its share needs is the part's verifier's judgment, under the first standard criterion.
- **Derived files are regenerated, never carved.** The pack copy is rebuilt by its own command.
- **A part that follows another carves onto a trunk that already holds it.** A conflict there is the carving run's ordinary work.
- **The part that inherits the refused findings** is given them word for word, and the donor, so its worker starts from what was tried.

Where there is no donor (a split at filing, at design or at the plan step), every part takes nothing and is built in the ordinary way.

### 7.5 When the parent is finished

When the last active required part has landed, the parent runs once more: a read-only verifier judges the parent's own criteria against the trunk, with the coverage table and each part's landing as its evidence. This is the closing check.

- **It passes:** the parent is finished, with no commit of its own. Its record lists the parts and their commits.
- **It finds something missing:** the finding goes to the design seat, which adds a part to the statement through a split extension. The parent waits on that part.

A split extension appends new part(s) without deleting earlier part items from the split's history. When an extension answers a closing-check finding, every earlier required part remains as landed; when an extension answers a required part closed as `defective` ([§7.6](#76-failure-and-edge-cases)), the corrected part(s) replace that closed part in the parent's required parts, coverage table and unlanded siblings' dependencies without the owner dropping it ([`D-SP16`](#D-SP16)).

Until the closing check is built, a parent whose required parts have all landed names the operator as its mover, who checks its criteria by hand.

### 7.6 Failure and edge cases

| Case | What happens |
| :--- | :--- |
| The design seat's run fails or ends without an answer | The item is held on the design seat with the call shown; the route for a design run that repeats applies. The donor stays pinned |
| The statement is malformed, has one part, or leaves a clause unowned | It is refused and returned to the design seat with the reason; nothing is filed |
| A part is refused by its verifier | It goes back to its own build step, as any item does. Its siblings are unaffected |
| A part's verifier names a regression in work taken from the donor | The same: the part is repaired or split again. The donor is never the excuse |
| A part trips a measured signal itself | It is split again |
| A required part is closed as `defective` by its verifier | The parent stays blocked and goes back to the design seat with the verifier's finding (`mover`: design seat), while dependent siblings wait on that repair. Through the split extension, the design seat either files a corrected required part that replaces the closed part in the parent's required parts, coverage table and siblings' dependencies, or says the decision is the owner's — the only path here that names the owner as mover ([`D-SP16`](#D-SP16)) |
| The owner drops a required part | The owner's dated words are recorded on the part, the part is closed, and the coverage table shows the clauses released. Nobody else may release a clause without replacement |
| A required part is closed any other way | The parent stays blocked and shows the closed part as needing the owner |
| Two parts change the same lines | The one that lands second meets a conflict at carving or at landing, and repairs it as any item does |
| The trunk moved a long way since the donor | The carving run works on the current trunk; nothing requires the donor's diff to survive unchanged |
| The split is called before any candidate exists | No donor; parts are built from nothing |
| An item is in flight when a slice ships | Nothing changes for it until its next settlement, when the signals are read like any other item's |

**Replacing a required part closed as `defective` ([`D-SP16`](#D-SP16), a designer's decision from an operator request, which the owner may overturn).** Measured on 2026-11-27 on the worktree-ownership split: when the required part "Move the drain loop onto item-owned worktrees" was closed as `defective` because its words forbade a test change the work needed, `tillr drain why` held the parent with mover `"owner; required part did not land"` and held the dependent final part with `"operator; remove the dependency or reopen the closed item"`, starting no design run and leaving the operator to file a corrected request and rewire the dependency by hand. Because a part's share, leaves and part criteria were written by the design seat when dividing the parent (`P3`), a `defective` closure on a required part goes to the design seat first — consistent with [`OQ-CR7`](./criterion-repair.md#decision-ledger) in criterion repair, and recorded here from a 2026-11-27 operator request as a designer's decision the owner may overturn (the request reported an unquoted 2026-11-27 owner direction, and only verbatim quoted owner statements are recorded as owner rulings). The design seat receives the parent's words, criteria, split statement, part states and the verifier's `defective` finding on the closed part, and answers in one of two ways:

- **A corrected split extension:** one or more new required parts take over the closed part's share and coverage. Filing the extension atomically records the new part(s), updates coverage so every clause of the parent remains owned, and replaces the closed `defective` part in the parent's required parts and in unlanded sibling parts' dependencies, while keeping the closed part in the split's history as replaced without the owner dropping it. If the extension is malformed or leaves a clause unowned, it is refused and returned to the design seat; if the design run fails or repeats the same refusal, the parent is held on the design seat.
- **An owner decision:** when the finding exposes a problem in the parent's own request or requires dropping or changing what the owner asked for, the design run says the decision is the owner's (with the question for the owner). Only then does the owner become the parent's mover.

### 7.7 Items with no verifier, trunk fixers first

Two kinds of item run without a verifier ([`D-SP18`](#D-SP18), a designer's decision from an operator request, which the owner may overturn). Neither reaches `S1` to `S4`; both reach the design seat from an operator or design call, or from the gate if [`OQ-SP2`](#OQ-SP2) promotes `S11`.

**A filed request on a verifier-less workflow (`single-worker@v1`).** The item already has saved request words and criteria, so a split divides its words and criteria by the rules of [§7.1](#71-the-split-statement) to [§7.5](#75-when-the-parent-is-finished). When split after a gate refusal, the refused candidate is pinned as the donor and the gate's failing checks and tests are handed to the design seat as the inherited findings. The one difference is donor carving: **no part may take the donor whole**, because no verifier examined the whole candidate and the gate refused the donor's tree. Every part takes named work or takes nothing, and the parent still finishes by the read-only closing check of its criteria on the trunk.

**A trunk fixer — and, by the same shape, the other loop-generated repairs that have neither a verifier nor saved owner words.** A trunk fixer (`fixer-main-<sha12>`) is filed by the drain on `single-worker@v1` (`build` seat, head-of-queue rank `000`) with no saved request words and a single generated `trunk-green` criterion: make the full gate green on the named red checks and advance the proved-green pointer. Its request is "make the trunk green", not an owner's words with clauses to divide. When 38 tests in one package fail across several broken invariants, a single worker fixing one cause unmasks or disturbs another ([§2](#2-the-case-measured)).

When a split of a trunk fixer reaches the design seat, the design seat reads the trunk proof's failing checks and tests, the fixer's gate-refusal failing sets, and the pinned donor candidate, and answers in one of two ways:

- **No split**, with the reason, when the failing checks and tests share one cause of failure. The fixer returns to its build step with its gate refusal as today.
- **A split statement with one required part per cause of failure** (two or more parts):
  - **Part words and criteria.** Because the parent has no owner words to quote, each part gets generated words naming the parent trunk fixer and red trunk commit, **this part's cause of failure** (`share`), the **assigned failing checks and tests** this part must turn green, and the **failing checks and tests left to named sibling parts** (`leaves`). Its criteria require its assigned checks and tests to pass with no new failures introduced, and forbid adding or touching any item declaration block.
  - **Coverage over the failing set.** In place of quoting owner clauses ([§7.3](#73-how-criteria-are-divided)), the coverage table assigns every failing check and test from the trunk proof and the parent's latest gate refusal to the part whose cause owns it (or marks a cross-cause symptom for the parent's closing gate once the causes land), while the parent keeps its `trunk-green` criterion unchanged (P1). A statement that leaves a known failing check or test unowned is refused.
  - **Donor, queue rank and gate-code rule.** The parent fixer's last refused candidate is pinned as the donor, and each part **takes named work** or **takes nothing** — never **takes the donor whole**. Each part inherits the trunk fixer's head-of-queue rank (`000`) and express standing so items waiting on the broken trunk do not wait behind ordinary work, and inherits the trunk fixer's rule that it may never edit the gate's own code or check declarations to make a red check disappear.
  - **Gating a cause part on a still-red trunk.** Gating a cause part as an ordinary item would acquit it on `trunk-already-red` behind its own blocked parent (a circular wait), while requiring the parent's entire named suite to pass would refuse Part 1 for Part 2's unfixed cause. A part of a trunk fixer is therefore **never acquitted behind its parent fixer**: it runs the ordinary fast candidate gate in full, and its post-gate named-check proof passes when every check and test assigned to that part passes on the candidate and no check or test fails outside the failures already red on the part's base and assigned to unlanded sibling parts of the same fixer. This is P6 ([`OQ-SP1`](#OQ-SP1)) at the fixer gate: a part lands when it cures its cause without making the trunk worse, while sibling causes are still queued.
  - **Finishing the parent trunk fixer.** A trunk fixer has no owner prose for a read-only verifier to judge; its bar is the gate itself. When the last active required cause part has landed, the parent's closing check runs **the full fixer gate on the trunk tip** — the fast gate plus every named red check in full, with no sibling exclusions. If every named check passes, the parent finishes with no commit of its own, records the trunk repair, and releases every item waiting on it, and the next heavy proof advances the green pointer. If any failure remains on the trunk tip, the parent stays open and the remaining failing set goes to the design seat to add a part through a split extension ([§7.5](#75-when-the-parent-is-finished)). If the heavy trunk proof advances the proved-green pointer past the red commit earlier, the parent and any unlanded parts resolve as `resolved-elsewhere`, as trunk fixers do today.

**Other verifier-less loop-generated repairs** (tooling-defect items, flaky-check repairs and remote-sync conflict repairs) have the same shape — `single-worker@v1`, no verifier and a generated criterion rather than owner words. Most carry one defect and receive **no split**; when one bundles distinct causes of failure, it splits by the same contract: one required part per cause with generated words, coverage over the failing checks or cases, `takes named work` or `takes nothing`, head-of-queue standing, and a closing check that re-runs the parent's mechanical check or proof rather than a prose verifier.

## 8. How a verdict accepts part of a candidate

### 8.1 What the verifier states

A verdict says, per criterion, done or not done, with a reason. To let accepted work land, a not-done verdict states four more things. None of them asks the verifier to design the split.

1. **Each finding's kind**, one of three:
   - **Gap**: in the case named, the trunk with the candidate behaves as the trunk does without it. The request is not yet met there, and nothing is worse.
   - **Regression**: in the case named, the trunk with the candidate behaves worse, or something that passes without it fails.
   - **Unasked change**: the candidate changes something no criterion needs.
2. **Each finding's place**: the case in one sentence, and the paths that implement it, using the file locators a verdict already carries.
3. **Each earlier finding's fate**: for every finding returned to the builder by the previous verdict on this item, fixed or not fixed.
4. **What was examined**: the whole candidate, or the paths that were not.

The verifier decides a finding's kind by the test in its definition, and gives the evidence: for a gap, that the named case behaves the same on the base. A finding it cannot classify is stated as a regression. Kinds and places are built (`gap`, `regression` and `unasked-change` in `tools/tillr-item/item/verdict.go`); fates and what was examined are slice 3.

### 8.2 What the design seat does with it

| The verdict says | The split statement |
| :--- | :--- |
| Every finding is a gap, and the whole candidate was examined | One part takes the donor whole; its words leave the named cases to a second part, which takes nothing and inherits the findings |
| Regressions or unasked changes sit in paths the rest does not need | One part takes the donor without those paths; another inherits them and the findings |
| A regression sits in code the rest depends on | No split. The findings go back to the build step |
| Some paths were not examined | Those paths go to a part that takes nothing from the donor for them, or to no split, at the design seat's judgment |

### 8.3 Why nothing is inherited, and why a part still lands

A verdict binds one criterion text to one exact tree. A part has different words and, unless it takes the donor whole, a different tree. So an acceptance stated about the donor is evidence shown to the part's verifier and never a verdict for the part.

What keeps the part's own verification from repeating the parent's refusals is P6. The part's verifier is given the cases its words leave to siblings. When it finds another case of the parent's request that the part does not handle, and that case is a gap, the finding is recorded against the parent and added to the sibling part that owns the remaining cases. The part is not refused for it.

So a behavior can be on the trunk while named cases of the request are still queued. The owner ruled that this is the healthier outcome ([§1](#1-the-ruling)): the working part is a small functional change that can be reviewed and tested by itself, and each queued case follows as its own part. The parent stays open and visible until those cases land.

## 9. What the queue and the status surfaces show

Nothing about a split is hidden, and every waiting state names who moves it.

| Surface | A parent | A part | A pending call |
| :--- | :--- | :--- | :--- |
| The queue's reasons (`tillr drain why`, and the pages that read it) | One row until the closing check passes: "split into 5 parts: 3 landed, 1 working, 1 held", the held part's reason and its mover; when a required part closes as `defective`, mover the design seat until replaced (or the owner only when the design run says the decision is the owner's) | An ordinary row, labelled with its parent's title and its part label; when waiting on a sibling closed as `defective`, waiting on the design seat's replacement rather than naming the operator | "split called by the verifier (or the gate); waiting on the design seat", mover the design seat |
| The item page | The parent's words or generated criterion; the call, with each signal's value beside its threshold; the statement with its coverage table; the donor and what each part took; each part's state and landed commit; any part the owner dropped, with the owner's words | Its parent, its share, what it leaves to which sibling, what it took from the donor, the findings or failing set it inherited | The call and the measurements |
| Counts and reports of landed work | Counted as landed only after the closing check. Until then it reads "3 of 5 required parts landed" | Each landing is reported under the parent's title | Not counted |

Two further rules:

- **Measurements are shown before any signal holds.** Every refused candidate's size, the comparison of its findings with the previous verdict's, and on a gate refusal the extracted failing checks and tests beside the comparison with the previous gate refusal's set (`S11`), appear on the item page from the first refusal.
- **A "no split" answer is shown**, with its reason, on the item page and in the queue's reason for the item, so the next call is read against it.

## 10. How this fits what exists

### 10.1 The planner's pieces

Pieces and parts answer different questions. A piece is a step toward one landing: pieces share a lane, a verifier, a gate and a commit, and a piece need not work alone. A part is a landing. A part's own plan may divide it into pieces. When the planner calls a split it may attach its grouping of pieces, which the design seat may adopt as the parts.

### 10.2 The write fence

There is no per-item path list; expansion and dispatch need none ([item graphs](../reference/tillr-item-graphs.md)). What exists is the common deny-list, which applies to every part as to every item, and a piece's own write scope inside one plan. A split adds no fence. The paths in a statement tell a carving run what to take, and a part that changes a path its statement did not name is judged by its verifier, not refused by a check.

### 10.3 One squash per item

An item lands as one squashed commit ([`OQ-IG1`](../reference/tillr-item-graphs.md#why-its-this-way), an owner ruling), and the lander's invariant is one landing, one squash. A split keeps both, because each part is an item. The parent lands no commit.

The other shape, "multiple judged landings out of one item", is rejected for three reasons: it would amend that ruling; a verdict, a gate result and a landing record each bind one item to one tree, so every one of them would need a second key; and an item's criteria freeze at its first landing, so the remaining parts would be judged against text nobody can correct. Part items give the same result, several judged landings out of one request, with none of those changes.

### 10.4 The landing principles

- **[Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once).** Each part is gated on its own tree. No gate result carries over from the donor.
- **[Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch).** A carved part is not required to match the donor's diff.
- **[A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report).** [§9](#9-what-the-queue-and-the-status-surfaces-show).
- **[A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act).** A pending call names the design seat; a blocked parent names its unlanded parts; a required part closed as `defective` names the design seat until a corrected part replaces it, and names the owner only when the design run says the decision is the owner's (or when a required part was closed without a `defective` verdict or owner release).

### 10.5 No count of attempts stops work

No count of worker endings determines routing ([`D-JL10`](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)), and the attempt number and refusal tallies only describe history ([`D-JL21`](./tillr-judge-and-lifecycle.md#413-what-the-attempt-number-and-refusal-counts-tell-the-operator-d-jl21)). The question the owner asks of repeated refusals is "Are the refusals changing?"

The split signals follow both rules. None reads a tally of attempts, refusals or elapsed time, and none acts on an unmeasured threshold. `S1` measures candidate size against a 95th-percentile threshold read from the store. `S2` compares the evidence of two consecutive verdicts, and `S11` compares the failing check-and-test sets of two consecutive gate refusals in the same landing cycle — never whether the refusal is the second or the fifteenth — answering the owner's question at both the verifier and the gate on the item page while staying advisory until store history settles a cut-off ([`OQ-SP2`](#OQ-SP2)). And a split is the opposite of a stop: the work continues in a different shape, with no hold on the owner and no limit on how often a part may be repaired.

## 11. What this does not do

- **It does not cap an item's size.** A large candidate its verifier accepts whole lands as one commit.
- **It does not let the drain cut code by itself.** The drain measures and files; a seat decides the division and a worker carves.
- **It does not change the fast candidate gate, the lander or the squash.** Only the drain's post-gate named-check proof on a split trunk fixer scopes a part to its assigned failures and runs the full proof as the parent's closing check ([§7.7](#77-items-with-no-verifier-trunk-fixers-first)).
- **It does not change how pieces work**, or raise their limit of twelve.
- **It does not ask the owner to approve a split.** The owner sees it, and is asked only when a required part would be dropped or when a design run on a `defective` required part finds that the decision is the owner's.
- **It does not specify new commands, fields, events or store tables.** Those belong to the implementation plan.
- **It does not decide filing policy** beyond the signals an operator reads. Whether a filing should refuse a request outright is not proposed.

## 12. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Several judged landings recorded on one item | Rejected: it amends the one-squash ruling and re-keys verdicts, gate results and landings ([§10.3](#103-one-squash-per-item)) |
| Replace the item with new items and close it | Rejected: the request disappears from the queue as one thing, and nothing checks the seams. [Criterion repair](./criterion-repair.md#9-alternatives-considered) rejected the same shape for orphaning the old record |
| Let the verifier write the split | Rejected: the judge would author the bar it applies |
| Let the planner write the split | Rejected: the planner is held to the criteria it would be dividing. It may propose a grouping |
| Split automatically along the planner's pieces | Rejected: a piece need not work alone, and in the measured case two pieces held one behavior |
| Split when a count of refusals is reached | Rejected: counts are ruled out ([§10.5](#105-no-count-of-attempts-stops-work)) |
| Refuse oversized requests at filing | Rejected as the only defense by the owner's own words: "ones will slip through" |
| Carry the verifier's acceptance to the part as a verdict | Rejected: a verdict binds a text and a tree, and the part has neither in common with the parent ([§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands)) |
| Only complete behaviors land: a part must meet every word of the request about its behavior | Rejected by the owner ([`OQ-SP1`](#OQ-SP1)). In the measured case nothing would have landed, because every finding was a case of one behavior |
| A path list for each part | Rejected: items carry no path fence, by the owner's ruling |
| Route on first-guess triggers and tune after false splits | Rejected (`D-SP15`): an unmeasured threshold that routes work is the arbitrary limit the owner ruled out |
| Require saved owner words on every split, or gate a trunk-fixer cause part like an ordinary item | Rejected ([`D-SP18`](#D-SP18)): a trunk fixer has no owner words, acquitting its part behind the parent fixer deadlocks both, and requiring the whole named suite on one part refuses it for sibling causes |

## 13. Risks

| Risk | Mitigation |
| :--- | :--- |
| A defect hides in the seam between two parts | The closing check judges the parent's criteria on the trunk; a criterion that needs end-to-end proof is owned by the last part or by that check |
| The trunk carries a behavior with queued cases, and they are never built | The owner accepted a part landing before its cases ([§1](#1-the-ruling)). The parent stays open and visible with its count of parts; only the owner drops a part |
| A finding is called a gap when it is a regression | The verifier must show the base behaves the same; an unclassifiable finding is a regression; the part's gate still runs every check |
| Splits happen where none is needed, and each costs a design run and extra gates | A candidate accepted whole is never split; no advisory signal calls a split; the design seat may answer "no split" |
| The design seat becomes the queue's narrow point | A call releases the lane; unrelated work continues; how long calls wait is visible in the queue's reasons |
| A carving run rebuilds instead of taking | Its brief names the donor and the paths, and its verifier sees both the donor and the part's diff |
| The thresholds are wrong | Only a store-measured trigger may act on routing; advisory signals stay displayed facts until measured |

## 14. The build, in slices that each land alone

The build follows the design's own rule. Each slice is its own request and lands as its own commit. Each one is useful on the trunk if no later slice ever lands, and none makes the trunk worse while the later ones are queued.

The order puts what only shows facts first, so store history accumulates on real items without moving anything. Next comes a split made by a person. Then comes each caller, and **a slice may act on routing only for signals marked measured in [§6](#6-the-signals)**.

| Slice | What lands | Why it is useful alone | Signals it may act on | Follows | Built |
| ---: | :--- | :--- | :--- | :--- | :--- |
| 1 | Every refused candidate's size (files and added lines, without the pack copy) is measured and shown on its item page beside `S1`'s limits, which are tracked settings. Nothing is routed | An operator sees an oversized candidate at its first refusal | None | — | Yes (`measureCandidateSize`) |
| 2 | A not-done verdict states each finding's kind and place, and the item page shows them | A reader of a refusal sees whether the trunk would be worse or only unfinished, and where | None | — | Yes (`verdict.go`) |
| 3 | A not-done verdict states each earlier finding's fate and what was examined. The item page shows the comparison with the previous verdict, unexamined paths, and the growth of the candidate | "Are the refusals changing?" is answered on the page for every item, and the store collects the history needed to measure `S2`, `S3` and `S4` | None | 2 | No |
| 4 | A split statement written by the design seat is checked and filed in one transaction: the parts as items, their order, and the parent blocked on its required parts. Every part takes nothing from a donor. A malformed statement is refused whole | A split made by a person files correctly and at once, in place of one request per part filed by hand | None | — | Yes (`FileSplit`) |
| 5 | The queue's reasons and the item page show a parent as one row with its count of parts, and a part with its parent, its share and what it leaves to which sibling ([§9](#9-what-the-queue-and-the-status-surfaces-show)) | A split is readable as one request on every surface | None | 4 | No |
| 6 | A part's verifier is given the cases the part's words leave to siblings. A gap it finds in another case of the parent's request is recorded against the parent and added to the sibling that owns the remaining cases (P6) | Part of a change lands while a named case is still queued | None | 2, 4 | No |
| 7 | The closing check: when the last required part lands, a read-only verifier judges the parent's criteria against the trunk. A pass finishes the parent, and a finding adds a part | A parent finishes without a person, and a defect in a seam between parts is found | None | 4 | No |
| 8 | The verifier's call: at a not-done verdict the drain records a split call when a measured verdict signal holds, pins the candidate as the donor, releases the lane and sends the item to the design seat, which answers with a statement or with "no split" | An oversized item leaves the repair loop at its first refusal that exceeds `S1` | `S1` only | 1, 3, 4 | No |
| 9 | Carving: a part takes the donor whole, or takes named work in a carving run ([§7.4](#74-carving-the-donor)) | Work already built is taken and not rebuilt | None | 8 | No |
| 10 | The planner's signals and call: the plan step records and shows `S5`, `S6` and `S7`, and may end with a split call only when a measured plan signal holds | Plan-time readings are visible and stored without routing on a guess | None until one of `S5` to `S7` is measured | 4 | No |
| 11 | Filing signals and the operator's call: filing records and shows `S6`, `S8`, `S9` and `S10`, and routes the item to the design seat before any run only when a measured filing signal holds | Filing-time readings are visible and stored without routing on a guess | None until one of `S6`, `S8`, `S9` or `S10` is measured | 4 | No |
| 12 | The owner drops a required part: the owner's dated words are recorded on the part, the part is closed, and the coverage table shows the clauses released | A parent can finish without a part the owner does not want | None | 7 | No |
| 13 | Every candidate gate refusal extracts failing checks and leaf failing tests from both triage blocks and Go test logs, compares that set with the previous gate refusal since the item's last landing (`S11`), and shows the set and comparison on the item page from the first gate refusal. Nothing is routed | "Are the refusals changing?" is answered at the gate in the store and on the item page, without collapsing named suite checks to a bare check ID | None | — | No |
| 14 | Splitting an item with no verifier, trunk fixers first ([§7.7](#77-items-with-no-verifier-trunk-fixers-first)): a split statement may divide a trunk fixer or other verifier-less item into one required part per cause of failure, with generated part words, coverage over the failing checks and tests, donor carving without taking the donor whole, part-scoped named-check gating on a still-red trunk, and full fixer-gate closure of the parent | A multi-cause trunk fixer lands one cause at a time without hand-filing or circular `trunk-already-red` waits | None | 4, 9 | No |
| 15 | The gate's call: when `S11` holds under a measured trigger ([`OQ-SP2`](#OQ-SP2)), a candidate gate refusal records a split call, pins the refused candidate as the donor, releases the lane and sends the item to the design seat | An item whose gate fails on a new set of tests leaves the loop without waiting on a verifier | `S11`, once measured ([`OQ-SP2`](#OQ-SP2)) | 8, 13, 14 | No |

Two slices where neither follows the other may be built at once in separate lanes.

**Who marks a slice built** (`D-SP14`). A build seat does not edit a design document, so a slice's code is the build seat's and its mark in this table is the design seat's. A slice's build commit changes no line of this document, and neither a build worker nor a verifier treats a request's sentence about the table as unmet.

## Open Questions

1. 💬 **OQ-SP2: Should `S11` stay advisory for now, or promote to a measured trigger?**

   In [§2](#2-the-case-measured), bare `S11` preceded a landing on `6` of `8` items (`13` of `22` pairs), against `1` shrinking pair (`0` landed). Requiring three or more failures in both sets matched only the non-converging trunk fixer (`3` pairs vs `0` on landed items), on one item.

   - **A — Keep `S11` advisory.** Ship slice 13's test-level records first; promote once a larger sample settles a cut-off.
   - **B — Promote `S11` when both sets have three or more failures.** Route multi-failure swaps now; ignore single-check follow-ups.
   - **C — Promote bare `S11` on one occurrence.** Route every disjoint consecutive refusal; let the design seat answer "no split".

   <!-- vantage: question id=OQ-SP2 leaning="A (Keep S11 advisory) — P7 and R-SP2 require a measured trigger: bare S11 converged on 6 of 8 items, and the three-failure cut-off rests on one non-converging item and one shrinking baseline pair." -->

   _Leaning:_ A (Keep `S11` advisory) — P7 and `R-SP2` require a measured trigger: bare `S11` converged on `6` of `8` items, and the three-failure cut-off rests on one non-converging item and one shrinking baseline pair.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

A row marked "owner" records the owner's verbatim words ([§1](#1-the-ruling)). Every other row is the designer's decision (or from an operator request), made without the owner, who may overturn it.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| R-SP1 | **Owner ruling.** Oversized changes are broken up past some threshold; the accepted parts land without the part objected to; several seats may say "split"; each has clear guidance | 2026-11-21, owner | [§1](#1-the-ruling) | — |
| <a id="OQ-SP1"></a>[`OQ-SP1`](#OQ-SP1) | **Owner ruling.** Part of a change may land while a named case of the request is still unmet; the case follows as its own part. A part is refused for making the trunk worse, for an unasked change, for a missing test or for not delivering its share, and not for a case its words leave to a sibling. **Rejected:** only complete behaviors land | 2026-11-21, owner | [§1](#1-the-ruling), [§4](#4-the-rules-a-split-keeps) (P6), [§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands) | — |
| R-SP2 | **Owner ruling.** A threshold whose origin nobody can state must not break work: "we have random thresholds again breaking things?" | 2026-11-23, owner | [§1](#1-the-ruling) | — |
| D-SP1 | A split produces part items under a parent that stays open, not several landings of one item. **Rejected:** several landings on one item, which would amend the one-squash ruling | 2026-11-21, designer | [§7.2](#72-parts-and-the-parent), [§10.3](#103-one-squash-per-item) | Slice 4 |
| D-SP2 | Five callers call a split (amended by [`D-SP17`](#D-SP17)): the operator at filing, the design seat, the planner at the plan step, the verifier at a not-done verdict, and the gate at a candidate gate refusal. The design seat alone writes the division, and may answer "no split" | 2026-11-21, amended 2026-11-27, designer | [§5](#5-who-calls-a-split-and-when), [§7.1](#71-the-split-statement) | — |
| D-SP3 | The eleven signals of [§6](#6-the-signals), and their reference lines (amended by [`D-SP17`](#D-SP17)). Only `S1` has a store-measured trigger (`D-SP15`); `S11` awaits [`OQ-SP2`](#OQ-SP2) | 2026-11-21, amended 2026-11-27, designer | [§6](#6-the-signals) | Slice 1: `S1` counts and limit settings |
| D-SP4 | Size alone never splits a candidate its verifier accepted whole, and no signal counts attempts or refusals | 2026-11-21, designer | [§5](#5-who-calls-a-split-and-when), [§10.5](#105-no-count-of-attempts-stops-work) | — |
| D-SP5 | The parent's criteria are never changed by a split. Each is owned by a part, clause by clause where needed; each part carries its own copy of the four standard criteria | 2026-11-21, designer | [§7.3](#73-how-criteria-are-divided) | Slice 4 |
| D-SP6 | The refused candidate is a donor. A part takes it whole, takes named work in a carving run, or takes nothing. Named paths are instructions and not a fence | 2026-11-21, designer | [§7.4](#74-carving-the-donor) | — |
| D-SP7 | A not-done verdict states each finding's kind and place, each earlier finding's fate, and what was examined. The verifier does not propose the parts | 2026-11-21, designer | [§8.1](#81-what-the-verifier-states) | Slice 2: kind and place |
| D-SP8 | No verdict or gate result is inherited from the donor: every part is verified and gated on its own tree | 2026-11-21, designer | [§8.3](#83-why-nothing-is-inherited-and-why-a-part-still-lands) | — |
| <a id="D-SP9"></a>[`D-SP9`](#D-SP9) | **Designer's decision; the owner may overturn it** (amended by [`D-SP16`](#D-SP16) and [`D-SP18`](#D-SP18)). The parent finishes by a closing check of its own bar against the trunk (a read-only verifier on a worded request, or the full fixer gate on a trunk fixer); a finding or remaining failure there adds a part. Only the owner drops a required part without replacement; a required part closed as `defective` goes to the design seat to be replaced through a split extension unless the design run says the decision is the owner's ([`D-SP16`](#D-SP16)) | 2026-11-21, amended 2026-11-27, designer | [§7.5](#75-when-the-parent-is-finished), [§7.6](#76-failure-and-edge-cases), [§7.7](#77-items-with-no-verifier-trunk-fixers-first) | — |
| D-SP10 | The queue shows a parent as one row with its count of parts until the closing check passes, and shows every measurement before any signal holds | 2026-11-21, designer | [§9](#9-what-the-queue-and-the-status-surfaces-show) | Slice 1: size and limits on the item page |
| D-SP11 | No count of verifier refusals pauses an item for review | 2026-11-21, designer | [§10.5](#105-no-count-of-attempts-stops-work) | — |
| <a id="D-SP12"></a>[`D-SP12`](#D-SP12) | **Designer's decision; the owner may overturn it** (amended by [`D-SP16`](#D-SP16)). The owner is not asked to approve or acknowledge a split, and is asked only when a required part would be dropped or when a design run on a `defective` required part says the decision is the owner's | 2026-11-21, amended 2026-11-27, designer | [§7.6](#76-failure-and-edge-cases), [§9](#9-what-the-queue-and-the-status-surfaces-show), [§11](#11-what-this-does-not-do) | — |
| D-SP13 | The build is fifteen slices that each land alone (amended by [`D-SP17`](#D-SP17) and [`D-SP18`](#D-SP18)), in the order of [§14](#14-the-build-in-slices-that-each-land-alone): what only shows facts first, then a split made by a person, then each caller | 2026-11-21, amended 2026-11-27, designer | [§14](#14-the-build-in-slices-that-each-land-alone) | Slices 1, 2 and 4 |
| D-SP14 | The design seat, not the build seat, marks a slice built in the slice table; a slice's build commit changes no line of this document. **Rejected:** a build commit that edits this document | 2026-11-22, designer | [§14](#14-the-build-in-slices-that-each-land-alone) | — |
| D-SP15 | A signal acts on routing only once its trigger is measured from the store; until then it is shown, never acted on. `S1` is measured; `S2` to `S11` are advisory (with `S11` put to the owner in [`OQ-SP2`](#OQ-SP2)), and tracked configuration does not promote one. **Rejected:** routing on first-guess triggers and tuning after false splits; hiding advisory signals, so the store never collects the readings needed to measure them | 2026-11-24, amended 2026-11-27, designer | [§4](#4-the-rules-a-split-keeps) (P7), [§6](#6-the-signals), [§14](#14-the-build-in-slices-that-each-land-alone) | Slice 1 displays `S1` without routing |
| <a id="D-SP16"></a>[`D-SP16`](#D-SP16) | **Designer's decision, from an operator request; the owner may overturn it.** Amends [`D-SP9`](#D-SP9) and [`D-SP12`](#D-SP12), and overturns the earlier rule in [§7.6](#76-failure-and-edge-cases) and [§10.4](#104-the-landing-principles) for `defective` closures (written from a 2026-11-27 operator request that reported an unquoted 2026-11-27 owner direction that a stuck split goes to the design seat first, consistent with [`OQ-CR7`](./criterion-repair.md#decision-ledger) in criterion repair; recorded as the designer's decision from an operator request because no verbatim owner words were quoted). When a verifier closes a required part of a split as `defective`, the parent stays blocked and goes back to the design seat (`mover`: design seat) with the verifier's finding, while unlanded sibling parts that depended on the closed part wait on that repair instead of naming the operator. Through the split extension, the design seat either files a corrected required part (or parts) that replaces the closed `defective` part in the parent's required parts, coverage table and unlanded siblings' dependencies while keeping the closed part in the split's history as replaced without the owner dropping it, or states that the decision is the owner's — the only path here that makes the owner the mover. A required part closed any other way without the owner's release still names the owner. **Rejected:** holding the parent on the owner immediately when a required part closes as `defective` and leaving dependent siblings on the operator to rewire by hand (the 2026-11-27 failure on the worktree-ownership split); reopening the frozen `defective` part in place; or deleting the closed part from the split's history | 2026-11-27, designer, from an operator request | [§4](#4-the-rules-a-split-keeps) (P1), [§7.5](#75-when-the-parent-is-finished), [§7.6](#76-failure-and-edge-cases), [§9](#9-what-the-queue-and-the-status-surfaces-show), [§10.4](#104-the-landing-principles), [§11](#11-what-this-does-not-do) | — |
| <a id="D-SP17"></a>[`D-SP17`](#D-SP17) | **Designer's decision, from an operator request; the owner may overturn it.** Amends `D-SP2`, `D-SP3`, `D-SP13` and `D-SP15`. Adds the gate as the fifth caller point ([§5](#5-who-calls-a-split-and-when)) and adds gate-refusal signal `S11` ([§6](#6-the-signals)): at a candidate gate refusal (`step = "gate"`), the non-empty set of failing checks and leaf failing tests (extracted from both triage blocks and Go test logs and stored on the refusal) has no element in common with the previous gate refusal's non-empty set since the item's last landing. Reads no tally of attempts, refusals or elapsed time ([§10.5](#105-no-count-of-attempts-stops-work)). Enters as **Advisory** under P7 and `R-SP2` on the 2026-11-27 store measurement ([§2](#2-the-case-measured)), with promotion put to the owner in [`OQ-SP2`](#OQ-SP2). **Rejected:** counting gate refusals or attempts; comparing check IDs alone without Go test log cases (which hid 7 of 9 pairs on the 2026-11-27 trunk fixer); comparing refusals across an intervening landing; routing on an unproved cut-off without the owner's ruling | 2026-11-27, designer, from an operator request | [§2](#2-the-case-measured), [§5](#5-who-calls-a-split-and-when), [§6](#6-the-signals), [§9](#9-what-the-queue-and-the-status-surfaces-show), [§10.5](#105-no-count-of-attempts-stops-work), [§14](#14-the-build-in-slices-that-each-land-alone), [`OQ-SP2`](#OQ-SP2) | — |
| <a id="D-SP18"></a>[`D-SP18`](#D-SP18) | **Designer's decision, from an operator request; the owner may overturn it.** Amends [`D-SP9`](#D-SP9) and `D-SP13` to cover items with no verifier, trunk fixers first ([§7.7](#77-items-with-no-verifier-trunk-fixers-first)). A verifier-less request splits by its saved words and criteria like any request, except that no part may take the donor whole. A trunk fixer (and, by the same shape, other verifier-less loop-generated repairs that have no owner words) splits into **one required part per cause of failure** (or answers "no split" when all failures share one cause): each part gets generated words naming its cause (`share`), its assigned failing checks and tests, and the failures left to sibling parts (`leaves`), with coverage over the failing checks and tests, donor carving via `takes named work` or `takes nothing`, head-of-queue rank (`000`) and express standing, and the fixer's rule against editing gate code or check declarations. A trunk-fixer part is never acquitted behind its parent fixer on `trunk-already-red` and passes its post-gate named-check proof when its assigned failures pass and no failure appears outside those already red on its base and assigned to unlanded siblings; the parent trunk fixer's closing check runs the full fixer gate on the trunk tip (extending the split if any failure remains). **Rejected:** requiring owner words on a trunk fixer; allowing `takes the donor whole` without a verifier; acquitting a trunk-fixer part behind its own blocked parent or requiring one cause part to turn sibling causes green; closing a split trunk fixer with a prose verifier instead of the full fixer gate | 2026-11-27, designer, from an operator request | [§4](#4-the-rules-a-split-keeps) (P1, P2, P3), [§7.4](#74-carving-the-donor), [§7.7](#77-items-with-no-verifier-trunk-fixers-first), [§11](#11-what-this-does-not-do), [§12](#12-alternatives-considered), [§14](#14-the-build-in-slices-that-each-land-alone) | — |
