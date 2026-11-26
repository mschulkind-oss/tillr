---
title: "Observatory redesign competition — static HTML mockups and two-round judging"
status: accepted
stage: CURRENT
tags: [tillr, observatory, view, ui, mockups, design]
summary: "Nine standalone static HTML mockups for the tillr Observatory produced in a two-round blind competition across independent agents and rated by separate judge agents: five Round 1 originals and four Round 2 variations of the top two, ordered best first with 1-to-5 star ratings, per-round judge reasons, lineage, and direct links."
---

# Observatory redesign competition — static HTML mockups

**Origin:** Produced on 2026-11-26 in response to an owner request for static HTML redesign mockups of the Observatory ([`tools/tillr-view/web`](../../../tools/tillr-view/web/README.md)), run as a multi-agent competition with no functional prototype and no changes to Observatory code. All design directions, judging criteria, star ratings, and rankings on this page are agent competition outputs for the owner to inspect, not owner rulings; selecting, combining, or discarding any direction is the owner's decision.

Each entry below is a self-contained static HTML page with inline CSS, inline sample data (318 to 460 items and 14 lanes across ordinary lanes, express lanes, and document desks), no build step, and zero network requests. Open any mockup directly from disk via its link.

---

## Master ranking (best first)

**Ordering basis:** Entries are ordered best first by tournament finish. All six Round 2 finalists appear first in their head-to-head Round 2 order (`5/5` down to `3/5`), followed by the three designs eliminated after Round 1 in their Round 1 order. Both advancing Round 1 originals ([Dispatch Deck](./r1-dispatch-deck/index.html) at `5/5` and [Meridian](./r1-meridian/index.html) at `4/5`) finished ahead of the eliminated field in Round 1; the fresh Round 2 judge then recalibrated the 1-to-5 scale across the stronger finalist pool once the four Round 2 variations resolved the Round 1 gaps.

| Rank | Mockup | Open | Round | Varies | Round 1 rating | Round 2 rating |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 1 | **Meridian — Vector** | [`./r2-meridian-vector/index.html`](./r2-meridian-vector/index.html) | Round 2 variation | [Meridian](./r1-meridian/index.html) | — | ★★★★★ (`5/5`, #1 of 6) |
| 2 | **Dispatch Deck — Flight Strip** | [`./r2-dispatch-flight-strip/index.html`](./r2-dispatch-flight-strip/index.html) | Round 2 variation | [Dispatch Deck](./r1-dispatch-deck/index.html) | — | ★★★★★ (`5/5`, #2 of 6) |
| 3 | **Meridian — Signal Folio** | [`./r2-meridian-signal-folio/index.html`](./r2-meridian-signal-folio/index.html) | Round 2 variation | [Meridian](./r1-meridian/index.html) | — | ★★★★☆ (`4/5`, #3 of 6) |
| 4 | **Dispatch Deck — Tactical Shift** | [`./r2-dispatch-tactical-shift/index.html`](./r2-dispatch-tactical-shift/index.html) | Round 2 variation | [Dispatch Deck](./r1-dispatch-deck/index.html) | — | ★★★★☆ (`4/5`, #4 of 6) |
| 5 | **Dispatch Deck** | [`./r1-dispatch-deck/index.html`](./r1-dispatch-deck/index.html) | Round 1 original | Original design | ★★★★★ (`5/5`, #1 of 5) | ★★★☆☆ (`3/5`, #5 of 6) |
| 6 | **Meridian** | [`./r1-meridian/index.html`](./r1-meridian/index.html) | Round 1 original | Original design | ★★★★☆ (`4/5`, #2 of 5) | ★★★☆☆ (`3/5`, #6 of 6) |
| 7 | **Helmsman** | [`./r1-helmsman/index.html`](./r1-helmsman/index.html) | Round 1 original | Original design | ★★★★☆ (`4/5`, #3 of 5) | Did not advance |
| 8 | **Watchfloor** | [`./r1-watchfloor/index.html`](./r1-watchfloor/index.html) | Round 1 original | Original design | ★★★☆☆ (`3/5`, #4 of 5) | Did not advance |
| 9 | **RailDeck Observatory** | [`./r1-raildeck/index.html`](./r1-raildeck/index.html) | Round 1 original | Original design | ★★☆☆☆ (`2/5`, #5 of 5) | Did not advance |

---

## Mockup entries and judge reasons by round

### 1. Meridian — Vector — ★★★★★ (`5/5` in Round 2)

- **Open mockup:** [`./r2-meridian-vector/index.html`](./r2-meridian-vector/index.html)
- **Round:** Round 2 variation (ranked #1 of 6 finalists)
- **Varies:** [Meridian](./r1-meridian/index.html) (Round 1 #2)
- **Design summary:** High-contrast daylight "Signal Paper" interface over 333 items and 14 lanes. Splits the Home screen into Tier 1 escalated operator rows (`alarm`, `needs you`, `needs a cause`) and Tier 2 root-cause-folded bulk waiting buckets with inline `Peek` expanders, pairs them with a filterable 14-lane Fleet Matrix, and routes all eight entity kinds (`item`, `run`, `pass`, `lane`, `check`, `landing`, `doc`, `question`) to dedicated hash views including a full-width `#/run/:id` span waterfall.
- **Round 2 rating & judge's reason — ★★★★★ (`5/5`):**
  > Fixes `orig-meridian`'s Round 1 gaps completely while preserving its crisp Signal Paper hierarchy: every entity kind (`item`, `run`, `pass`, `lane`, `check`, `landing`, `doc`, `question`) is a real `<a href>` link with dedicated pages, and the Home view cleanly separates escalated Tier 1 rows from folded Tier 2 root-cause buckets. Its dedicated `#/run/:id` page delivers a true shared-axis span waterfall with bespoke per-run traces, zoom, horizontal pan, fit, critical-chain filtering, keyboard navigation, and zero runtime errors even when `localStorage` throws.
  - **Noted tradeoff:** Light-only Signal Paper palette without a dark theme toggle, and `J`/`K` keyboard reorder steps by contiguous place numbers rather than across sparse filtered gaps.

### 2. Dispatch Deck — Flight Strip — ★★★★★ (`5/5` in Round 2)

- **Open mockup:** [`./r2-dispatch-flight-strip/index.html`](./r2-dispatch-flight-strip/index.html)
- **Round:** Round 2 variation (ranked #2 of 6 finalists)
- **Varies:** [Dispatch Deck](./r1-dispatch-deck/index.html) (Round 1 #1)
- **Design summary:** Rail-telemetry mission-control interface with a three-way `Sys | Dark | Light` theme switcher, sticky backend-alarm and loop-heartbeat bands, percentage-annotated 7-class triage strip, root-cause-folded bulk waiting buckets over 318 items, 14 grouped lanes with idle collapse and quiet-worker warnings, sparse-place `J`/`K` queue reordering under `--write`, and 16 distinct per-run span waterfalls.
- **Round 2 rating & judge's reason — ★★★★★ (`5/5`):**
  > Elevates `orig-dispatch` into a complete, zero-error operational control plane: it adds Dark/Light/System theme switching, distinct per-run waterfalls across `RUN_DETAILS` with zoom/fit/critical-chain/span selection, folded Home buckets for `waits-on-work` and `ready`/`waits-on-the-loop`, and a working P7 list-stability freeze banner in Queue. All keyboard shortcuts (`j`/`k`/`Enter`/`[`/`]`/`u`/`/`/`J`/`K`), `--write` sparse-place reordering, and New Idea intake run cleanly with zero runtime errors under missing or throwing `localStorage`.
  - **Noted tradeoff:** Waterfall supports zoom and fit without horizontal pan buttons, and secondary `doc`, `check`, and `landing` identifiers render as inline code rather than dedicated entity routes.

### 3. Meridian — Signal Folio — ★★★★☆ (`4/5` in Round 2)

- **Open mockup:** [`./r2-meridian-signal-folio/index.html`](./r2-meridian-signal-folio/index.html)
- **Round:** Round 2 variation (ranked #3 of 6 finalists)
- **Varies:** [Meridian](./r1-meridian/index.html) (Round 1 #2)
- **Design summary:** Evolves Meridian's Signal Paper layout with real `<a href>` entity links across all eight entity kinds, an "Escalated — Act Now" versus "High-Volume Background — Folded by Root Cause" Home split (highlighting stuck `closed-without-landing` blockers), sparse-place `J`/`K` queue reordering, and an interactive zoomable/pannable per-run waterfall inside a wide slide-over inspector drawer.
- **Round 2 rating & judge's reason — ★★★★☆ (`4/5`):**
  > Replaces `orig-meridian`'s button-only navigation with 98 `<a href>` entity links across all 8 entity kinds, folds high-volume Tier 2 Home buckets by root cause, and adds bespoke per-run waterfalls with zoom, pan, fit, and span inspection inside a slide-over drawer. It also supports sparse-place `J`/`K` reordering and runs cleanly without `localStorage`, falling just behind `var-meridian-1` because the drawer-bound waterfall is tighter than a dedicated run page and it remains light-only.

### 4. Dispatch Deck — Tactical Shift — ★★★★☆ (`4/5` in Round 2)

- **Open mockup:** [`./r2-dispatch-tactical-shift/index.html`](./r2-dispatch-tactical-shift/index.html)
- **Round:** Round 2 variation (ranked #4 of 6 finalists)
- **Varies:** [Dispatch Deck](./r1-dispatch-deck/index.html) (Round 1 #1)
- **Design summary:** Extends Dispatch Deck with `Dark` / `Light` / `Auto` theme switching, proportional root-cause distribution bars inside folded waiting buckets, actor ownership tags on triage tiles, vertical tick gridlines and a `★ Critical only` filter in the Run Waterfall, `[`/`]` run-to-run stepping across distinct run records, and inline `--write` queue reordering controls.
- **Round 2 rating & judge's reason — ★★★★☆ (`4/5`):**
  > Makes major Round 2 strides over `orig-dispatch` with a Dark/Light theme toggle, 9 distinct `RUN_DETAILS` waterfalls with Prev/Next Run (`[`/`]`) switching, folded Home root-cause buckets with proportional bars, and a Queue P7 stability banner. However, it trails `var-dispatch-2` due to an unguarded `CSS.escape()` call in `scrollFocusedQueueRow()` that throws in JSDOM on `j`/`k` Queue navigation, no horizontal waterfall pan, and non-link `doc`/`check`/`landing` text.

### 5. Dispatch Deck — ★★★★★ (`5/5` in Round 1) · ★★★☆☆ (`3/5` in Round 2)

- **Open mockup:** [`./r1-dispatch-deck/index.html`](./r1-dispatch-deck/index.html)
- **Round:** Round 1 original (ranked #1 of 5 in Round 1; advanced to Round 2 and ranked #5 of 6 finalists)
- **Varies:** Original design (parent of [Dispatch Deck — Flight Strip](./r2-dispatch-flight-strip/index.html) and [Dispatch Deck — Tactical Shift](./r2-dispatch-tactical-shift/index.html))
- **Design summary:** Dark-slate mission-control Observatory over 318 items and 14 lanes. Introduced the escalated-versus-folded-bucket Home triage layout (with clickable `blocked_by`, `path_fence`, and `lane_cap` root-cause pills), hash-routed `<a href>` entity links, folded pass streaks (`184` passes into `5` transition groups), and a shared-clock Run Waterfall with `unobserved_seam` boundary rows and skipped-check reasons.
- **Round 1 rating & judge's reason — ★★★★★ (`5/5`):**
  > Best-in-class operational triage and scale discipline: separates escalated work (`alarm`, `needs you`, `needs a cause`) from high-volume waiting buckets folded by reason pills so 312+ items never bury urgent operator action. Delivers authentic tillr semantics throughout — real hash-based `<a>` entity links, all seven epistemic provenance marks, `~claimed` vs judge verdicts, a zoomable shared-axis run waterfall with uncovered self-time and skipped checks, and zero errors offline with or without `localStorage`.
  - **Feedback passed to Round 2 variation agents:** Wire full Queue keyboard focus/reordering handlers to match the key hints; add a high-craft light/system theme alongside dark mode; load distinct run traces per `run_id` rather than a single showcase run (`run-88419`).
- **Round 2 rating & judge's reason — ★★★☆☆ (`3/5`):**
  > Establishes a strong dark control-plane backbone with 14 lanes, 312+ items, 3-way status encoding, explicit provenance glossary, working `j`/`k`/`Enter` Queue navigation, and clean offline execution. Compared to Round 2 variations, it is held back by a hardcoded single-run waterfall (`run-88419` for all `#/run/*` links), no Light theme toggle, and no Queue P7 stability freeze banner.

### 6. Meridian — ★★★★☆ (`4/5` in Round 1) · ★★★☆☆ (`3/5` in Round 2)

- **Open mockup:** [`./r1-meridian/index.html`](./r1-meridian/index.html)
- **Round:** Round 1 original (ranked #2 of 5 in Round 1; advanced to Round 2 and ranked #6 of 6 finalists)
- **Varies:** Original design (parent of [Meridian — Vector](./r2-meridian-vector/index.html) and [Meridian — Signal Folio](./r2-meridian-signal-folio/index.html))
- **Design summary:** High-contrast "Signal Paper" daylight Observatory over 328 items and 14 lanes. Pairs a two-band alarm/heartbeat header and 8-card KPI strip with an asymmetric two-zone Home screen ("Who Must Act Next" on the left, filterable 14-lane Fleet Matrix and folded passes on the right) plus slide-over item and run inspection drawers.
- **Round 1 rating & judge's reason — ★★★★☆ (`4/5`):**
  > Crisp, high-density 'Signal Paper' light interface with immediate alarm, loop heartbeat, `tillr-watch` alert, and 8-class KPI strip above a balanced two-column Home view. Handles 328 items and 14 lanes cleanly with lane state filters, P7 freeze banner, working `j`/`k`/`Enter` keyboard navigation, and strong item/run slide-over drawers, falling just short of 5 stars due to button-based entity links and a static non-zoomable waterfall.
  - **Feedback passed to Round 2 variation agents:** Replace `<button class="entity-link">` elements with real `<a href>` links across every entity kind; upgrade the static single-trace drawer waterfall to an interactive zoomable/pannable per-run waterfall with span inspection; fold high-volume `waits on work` and `ready` buckets on Home so background previews do not dilute escalated items.
- **Round 2 rating & judge's reason — ★★★☆☆ (`3/5`):**
  > Provides a clean Signal Paper layout with clear alarm/loop/watch liveness, 14 lanes, 328 items, and P7/write-mode scaffolding. However, it uses 0 `<a href>` entity links (102 `<button>` elements instead), lacks any `#/run/...` shared-axis span waterfall view, and leaves Queue `Enter` navigation unimplemented.

### 7. Helmsman — ★★★★☆ (`4/5` in Round 1)

- **Open mockup:** [`./r1-helmsman/index.html`](./r1-helmsman/index.html)
- **Round:** Round 1 original (ranked #3 of 5 in Round 1; did not advance past the top-two cut)
- **Varies:** Original design
- **Design summary:** Dark/light-capable operational console over 460 items (342 unlanded, 118 landed) and 14 lanes. Features an interactive 7-bucket Triage Pulse Strip that filters Home triage groups in place, a proportional queue-distribution bar, P7 list-order pinning (`Simulate arrival`), folded planning-pass streaks (`412` rounds into `7` groups), and a zoomable/pannable Run Waterfall.
- **Round 1 rating & judge's reason — ★★★★☆ (`4/5`):**
  > Disciplined dark/light-capable operational console with a 7-card Pulse strip that filters Home triage groups in place, a proportional 342-item scale bar, and an interactive zoomable Run Waterfall. Edges behind Meridian due to hardcoded run-link routing, flat ungrouped lane list on Home, and fewer Queue/Passes filter controls.

### 8. Watchfloor — ★★★☆☆ (`3/5` in Round 1)

- **Open mockup:** [`./r1-watchfloor/index.html`](./r1-watchfloor/index.html)
- **Round:** Round 1 original (ranked #4 of 5 in Round 1; did not advance)
- **Varies:** Original design
- **Design summary:** Charcoal command-floor layout over 430 items (342 unlanded, 88 landed) and 14 lanes. Leads with three dedicated urgent-triage cards (`alarm`, `needs you`, `needs a cause`) above a clickable Unlanded Queue Topology bar, a 2-column 14-lane tile matrix grouped into Ordinary, Express, and Document Desks, an item detail drawer, and a zoomable Run Waterfall.
- **Round 1 rating & judge's reason — ★★★☆☆ (`3/5`):**
  > Readable Watchfloor layout with a 3-card urgent triage header (`alarm`, `needs you`, `needs a cause`), proportional distribution bar, and a clean zoomable Run Waterfall. However, it omits middle/bulk waiting-class previews from Home, relies on non-semantic `<span>` click targets for entity links, and lacks keyboard navigation in the Queue.

### 9. RailDeck Observatory — ★★☆☆☆ (`2/5` in Round 1)

- **Open mockup:** [`./r1-raildeck/index.html`](./r1-raildeck/index.html)
- **Round:** Round 1 original (ranked #5 of 5 in Round 1; did not advance)
- **Varies:** Original design
- **Design summary:** Warm stone-paper Swiss telemetry aesthetic (`#F3F2EE` with dark-mode support) modeling 342 items and 14 lanes across seven views, with a proportional 7-class bar, actor-split summary, provenance pills, and a zoomable Run Waterfall in source, but blocked on strict `file://` origins where `localStorage` is unavailable.
- **Round 1 rating & judge's reason — ★★☆☆☆ (`2/5`):**
  > Strong underlying design, 342-item/14-lane dataset, and authentic provenance modeling in source, but fails critically on standalone `file://` execution: an unguarded `localStorage.getItem()` call at startup (`ideaDraft: localStorage.getItem("tillr_raildeck_idea_draft") || ""`) throws a `ReferenceError`/`SecurityError` when `localStorage` is unavailable, leaving the entire page blank.

---

## Judging criteria (written before inspection)

Before opening any mockup, the Round 1 judge established four evaluation dimensions and a 1-to-5 star rubric. The fresh Round 2 judge read and applied that identical rubric before inspecting the six Round 2 candidates.

1. **At-a-glance operational triage:** Whether an operator can see within seconds what is running right now, what is stuck and why, and what needs *them* — pinning backend/quota alarms and loop liveness/stop explanations above item rows, surfacing `tillr-watch` alerts and quiet-lane warnings, organizing unlanded work by waiting class (`alarm`, `needs you`, `needs a cause`, `waits on work`, `waits on the loop`, `ready`, `moving`), encoding status three ways (symbol + word + semantic color), and separating `~claimed` worker self-reports from judge verdicts alongside explicit provenance marks (`observed`, `~` derived clocks, `≥` lower bounds, `absent`, `unobserved_seam`, `unreadable`).
2. **Density without clutter:** Signal-to-space ratio, scannable hierarchy between urgent operator action and background queue state, monospace identifiers and aligned `tabular-nums` data columns, and absence of gratuitous card chrome or decorative noise.
3. **Scale under real load (hundreds of items and 12+ lanes):** Whether 12+ lanes across ordinary lanes, express lanes, and document desks (`running`, `settling`, `idle`, `kept`/`held`) and hundreds of queued items stay usable via root-cause folding, faceted filters, pagination, and list-order freeze on scroll/focus without burying escalated items.
4. **Visual quality and standalone HTML execution:** Cohesive visual craft, navigable `<a href>` entity links across entity kinds, a shared-axis run span waterfall, zero dollar prices (per [the Observatory reference](../../reference/observatory-redesign.md#usage-without-dollar-prices)), and zero-error offline `file://` execution with or without `localStorage`.

**Star scale:**

- **5 stars:** Excels across all four dimensions with authentic domain semantics, strong visual craft, credible 300+-item / 14-lane scale mechanics, and error-free offline `file://` execution.
- **4 stars:** Strong contender across most dimensions with minor, contained gaps (such as drawer-constrained waterfall width, missing dark/light toggle, or minor keyboard edge cases).
- **3 stars:** Recognizable layout and domain structure held back by notable navigation, scale-folding, or missing-surface gaps compared against the field.
- **2 stars:** Major triage, provenance, or standalone `file://` execution defect (such as crashing when `localStorage` is unavailable).
- **1 star:** Broken rendering, off-brief generic dashboard, or unusable clutter.

---

## Competition structure

1. **Round 1 — Five independent designers:** Five isolated worker subagents received the identical brief — reading [`tools/tillr-view/README.md`](../../../tools/tillr-view/README.md), the current front end under [`tools/tillr-view/web/`](../../../tools/tillr-view/web/README.md), and the Observatory reference documents ([observatory redesign](../../reference/observatory-redesign.md), [observatory truthfulness](../../reference/observatory-design.md), [observatory actions](../../reference/observatory-actions.md), and [item states](../../reference/tillr-item-states.md)) — with no house style imposed and no visibility into one another's work. They produced [Dispatch Deck](./r1-dispatch-deck/index.html), [Meridian](./r1-meridian/index.html), [Helmsman](./r1-helmsman/index.html), [Watchfloor](./r1-watchfloor/index.html), and [RailDeck Observatory](./r1-raildeck/index.html).
2. **First judging:** A separate fresh judge agent wrote its rubric before reading any mockup, inspected all five source files and exercised each under `file://` JSDOM simulation (with and without `localStorage`), rated all five from 1 to 5 stars, and selected [Dispatch Deck](./r1-dispatch-deck/index.html) (`5/5`) and [Meridian](./r1-meridian/index.html) (`4/5`) as the top two.
3. **Round 2 — Four independent variations:** Four isolated worker subagents (two varying [Dispatch Deck](./r1-dispatch-deck/index.html) and two varying [Meridian](./r1-meridian/index.html)) each received their parent mockup together with what the Round 1 judge liked and disliked, producing [Meridian — Vector](./r2-meridian-vector/index.html), [Meridian — Signal Folio](./r2-meridian-signal-folio/index.html), [Dispatch Deck — Flight Strip](./r2-dispatch-flight-strip/index.html), and [Dispatch Deck — Tactical Shift](./r2-dispatch-tactical-shift/index.html).
4. **Second judging:** A fresh judge agent, given the exact written criteria from Round 1 and none of Round 1's numeric scores, inspected and exercised the four variations and the two originals together under `file://` (including normal, missing, and throwing `localStorage` checks) and rated all six from 1 to 5 stars.
