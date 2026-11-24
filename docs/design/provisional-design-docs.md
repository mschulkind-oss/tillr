---
title: "Turn an idea into a provisional design doc in minutes, then work the design later"
status: draft
stage: DESIGN
updated: 2026-11-20
tags:
  - tillr
  - design
  - intake
  - latency
  - vantage
  - matt-craft
summary: "Defines the provisional design document state — capturing the owner's verbatim prompt, bounded project context, and initial open questions on trunk within two to three minutes with zero upfront research — splits provisional capture from the later worked design pass, surfaces provisional status in tillr's queue views, and asks the owner who writes provisional docs and how matt-craft's design-doc skill codifies them."
next: "The owner rules on OQ-PD1 through OQ-PD4; follow-up worked design pass and implementation wait on those rulings."
depends-on:
  - tillr-idea-pipeline.md
  - tillr-intake-paths.md
  - tillr-early-design-landing.md
  - tillr-designer-scouts.md
vantage:
  status-chip: true
---

# Turn an idea into a provisional design doc in minutes, then work the design later

**Status:** 2026-11-20 — PROVISIONAL (at `129509992`). Captured directly in provisional form from the owner's 2026-11-20 prompt and bounded repository facts; exhaustive codebase research and the worked completeness pass are intentionally deferred, and nothing is built.

> **In short.** Today an owner's design thought reaches the SQLite store in seconds, then sits invisible to Vantage and sibling agents for 15–30+ minutes while [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) queues and runs upfront research. Splitting design into **Step 1: immediate provisional capture** (verbatim prompt + bounded context + initial `OQ-*` questions, zero new scouting) and **Step 2: the worked design pass** puts a real, marked planning document on `main` within two to three minutes without interrupting other work.

**Why it matters.** Until a file exists in `docs/design/`, the owner cannot browse or answer questions in Vantage, sibling items cannot link to it, and four ideas spoken in fifteen minutes stay trapped in private queue rows.

**The shape.** An express capture pass writes the file immediately under a no-research instruction, lands it on `main` via [early design landing](./tillr-early-design-landing.md), attaches `design_doc` on the queue item with a visible `provisional` badge, and leaves the deeper research-and-completeness pass as separate ordinary-priority work.

**Cost.** Splits a design-bound item into two landings (provisional capture, then worked design) and requires an explicit carve-out to `design-doc`'s completeness floor — plus a narrow writer-rule decision ([OQ-PD1](#OQ-PD1)) if anyone other than the `design` seat writes Step 1.

**Start at [The provisional state](#the-provisional-state)** for the document contract, and [From idea to landed file, and what the gap costs today](#from-idea-to-landed-file-and-what-the-gap-costs-today) for the measured latency and writer paths.

**Needs your ruling:** [OQ-PD1](#OQ-PD1), [OQ-PD2](#OQ-PD2), [OQ-PD3](#OQ-PD3), and [OQ-PD4](#OQ-PD4).

**Reads with:** [the idea pipeline](./tillr-idea-pipeline.md) and [intake paths](./tillr-intake-paths.md) (how ideas and requests enter the store), [early design landing](./tillr-early-design-landing.md) (landing `docs/` cuts ahead of an item), [designer scouts](./tillr-designer-scouts.md) (why two-node `scouted-design@v1` stalls cold), and [quick-request latency](../tillr/latency/2026-11-19-quick-request-latency.md) (measured single-worker vs gate wall times).

---

## Owner prompt

Recorded verbatim from the owner on 2026-11-20:

> "when I dump in design thoughts like this, we're now in this gap between me giving you the idea, it going to the Tiller database, and then it persisting as a document, and we want that gap to be as short as possible, even if we don't want it to interrupt. other work. So maybe we need the concept of ... a provisional design doc, and maybe this even needs to get into matcraft to really be official but it should be like the quickest lightweight way of turning an idea into a design doc so that we can capture it in the right form without spending time up front. Like I would say this is you would hand the idea with just a little context about the project to an agent and tell them write the design doc right now. Don't delay. Don't do more research. We will come back to it and be able to mark that as the state."

And:

> "if we know that an item will be attached to a design doc, we need to get it to that state as soon as possible, even if we're not ready to do the follow-up work immediately."

*(In the dictated transcript, "Tiller" is `tillr` and "matcraft" is the owner's `matt-craft` skill pack configured in `config/yolo-jail/config.toml`, which owns the `design-doc` skill. This document is written in the provisional form it specifies.)*

---

## The provisional state

A **provisional design document** is a valid Vantage planning file at its final `docs/design/<topic>.md` path that captures an idea before codebase scouting or exhaustive failure-mode analysis runs.

| Contract aspect | Rule |
| :--- | :--- |
| **Must contain** | 1. **Verbatim owner prompt:** an opening `## Owner prompt` section quoting the owner's dated words word-for-word.<br>2. **Bounded project context:** 2–5 sibling document or code links already in hand and a concise statement of the problem — **zero new tree-wide scouting**.<br>3. **Initial open questions (`OQ-*`):** the choices the prompt immediately raises, formatted with Vantage question directives, concrete options, and a leaning. |
| **Must not claim** | No settled rulings or `D-*` Decision Ledger rows; no `stage: DECIDED` or `stage: BUILT`; no unverified assertions about unread code; no file-by-file build plan or companion `-plan.md` seed; and an explicit temporary waiver of `design-doc`'s *Completeness / The holes, by name* floor until promoted. |
| **How status marks it** | Frontmatter sets `status: draft` and `stage: DESIGN` under today's [`.vantage.toml`](../../.vantage.toml) vocabulary (or `stage: PROVISIONAL` if [OQ-PD2](#OQ-PD2) adds an eighth word). Immediately under `# Title`, the prose status line begins `**Status:** YYYY-MM-DD — PROVISIONAL (at \`<sha>\`). ...` so both humans and [`frontmatter.go`](../../tools/tillr-view/frontmatter.go) detect it unambiguously without breaking `uvx vantage-check`. |
| **Promotion without losing the original words** | The follow-up worked `design` pass edits the **same file** in place: it runs bounded scouts ([designer scouts](./tillr-designer-scouts.md)), walks *The holes, by name*, expands body sections, refines `OQ-*` items, flips `status` to `in-review`, and removes `PROVISIONAL` from `**Status:**`. The opening verbatim **`## Owner prompt` block is permanently exempt from `design-doc` compaction** — it stays verbatim through deliberation and `DECIDED`, disappearing only when a built design graduates to `docs/reference/` (`system-doc`). |

---

## From idea to landed file, and what the gap costs today

### Measured gap on 2026-11-20

Between ~14:50 and 15:05 ET on 2026-11-20, the owner dictated four design ideas: state machines, agent alerting, briefing-change validation, and provisional design docs. Three entered `.tillr/state.db` at `20:01:38Z` (15:01:38 ET) on `express: true` under [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json), and the fourth entered at `20:06:20Z` on `express: false`.

- **18 minutes after store intake (~25–30 minutes after dictation), `0 of 4` existed in `docs/design/`.** The first two express items spent **229 s ($0.44)** and **285 s ($0.51)** in [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json)'s read-only `open` stage before any writer started; this item had just started `open`; the non-express item had `0` runs.
- **Writing itself takes under 40 seconds.** In [the 2026-11-19 latency audit](../tillr/latency/2026-11-19-quick-request-latency.md), active single-worker generation of a Markdown design change took **31.6 s–39.7 s**, whereas two-stage workflow handoffs plus non-doc gate checks stretched wall time to **10m 34s–27m 40s**. Docs-only checks ([`documents.py`](../../tools/gate/checks/documents.py) / `uvx vantage-check`) finish in seconds ([landing velocity](./landing-velocity.md)).

### The capture instruction and target latency

Every provisional capture run receives one directive alongside the owner's words and 2–5 context pointers:

> *Write the provisional design doc at `docs/design/<topic>.md` right now from the owner's words and the supplied context. Do not delay. Do not do more research. Mark `**Status:**` as `PROVISIONAL`, state only verified context, and file open questions for every unsettled choice.*

**Target latency** from the owner's message to a landed `docs/design/<topic>.md` on `main`: **<= 2–3 minutes** via an express drain lane (~35–45 s single-pass write + seconds-scale docs-only gate + immediate trunk land), or **<= 60–90 seconds** if written directly in the foreground ([OQ-PD1](#OQ-PD1)).

### Who writes it, and how that fits the standing design-seat rule

Today `DesignDocumentRule` in [`stage.go`](../../tools/tillr-drain/drain/stage.go) enforces a standing rule: *"Only an item's designer changes a design document."* Three writer paths trade latency and frontier-seat contention against that rule ([OQ-PD1](#OQ-PD1)):

| Path | Who writes Step 1 | Effect on `DesignDocumentRule` in [`stage.go`](../../tools/tillr-drain/drain/stage.go) | Expected wall time |
| :--- | :--- | :--- | :--- |
| **A — Express one-run `design` workflow (`provisional-design@v1`)** | One `design`-seat node on an express lane ([`.tillr.toml`](../../.tillr.toml) `[lanes] express = 2`), search/scout tools off | **Unchanged.** The drain's `design` seat remains the sole writer of `docs/design/*.md`. | **~2–3 min** (waits only if both express lanes are busy) |
| **B — Dedicated express `provisional` seat (mid-tier writer)** | A fast mid-tier drain seat dedicated to Step 1 capture | **Narrow carve-out:** `provisional` may **create a new** `PROVISIONAL`-marked `docs/design/*.md` file; editing any existing design doc stays strictly `design`-only. | **~1.5–2.5 min**, keeping frontier `design` quotas free |
| **C — Foreground agent at once + `tillr-land` doc cut** | The foreground conversation agent (or one local subagent) writes immediately and hands a docs-only cut ([early design landing](./tillr-early-design-landing.md)) to `tillr-land` | **Explicit exception:** permits foreground creation of **new** `PROVISIONAL` docs, serialized through `tillr-land` so `main` is never dirtied directly. | **~60–90 s** (zero pass/lane wait) |

---

## Attaching Step 1 to the item, queue visibility, and `matt-craft`

### Step 1 before and separate from Step 2

Whenever intake knows an item needs a design document — `tillr item idea` ([`serveidea.go`](../../tools/tillr-view/serveidea.go)) marked as needing design, or `tillr item request --seat design` ([`request.go`](../../tools/tillr-item/item/request.go), where `RequestDefaultWorkflow["design"]` currently hardcodes `"scouted-design@v1"`) — the item runs as two distinct steps without interrupting unrelated work:

1. **Step 1 — Provisional capture (express, immediate):** creates `docs/design/<topic>.md`, checks it with `uvx vantage-check`, lands the document on `main` via [early design landing](./tillr-early-design-landing.md), and sets `item.DesignDoc` in the store ([`plan.go`](../../tools/tillr-drain/drain/plan.go)) immediately.
2. **Step 2 — Worked design pass (ordinary priority, separate):** runs later in the background (or after owner triage; see [OQ-PD3](#OQ-PD3)) on `design` to scout the tree, complete the gap test, and promote the document out of `PROVISIONAL`. Downstream `plan`/`build` items attached to `item.DesignDoc` remain held while the document is `PROVISIONAL`.

### How the queue and Observatory show a provisional document

Today [`plan.go`](../../tools/tillr-drain/drain/plan.go) (`ReasonDesignDocMissing`, `ReasonQuestionOpen`), [`waiting.go`](../../tools/tillr-view/waiting.go), [`ItemDetailPane.tsx`](../../tools/tillr-view/web/src/components/ItemDetailPane.tsx), and [`DocumentPane.tsx`](../../tools/tillr-view/web/src/components/DocumentPane.tsx) distinguish only *missing doc* from *doc present*. Extending [`frontmatter.go`](../../tools/tillr-view/frontmatter.go) to parse the `PROVISIONAL` marker surfaces a distinct **`provisional` badge** across CLI (`tillr view status`, `tillr view waiting`) and web queue rows, separating three states cleanly:

- **`design-doc-missing`** — no file on `main` yet (Step 1 still owed, target < 3 minutes);
- **`provisional`** — `docs/design/<topic>.md` is live on `main` and readable in Vantage, worked design pass still owed (`ReasonDesignProvisional` holds any dependent build);
- **`question-open` (worked `DESIGN`)** — worked design pass finished; waiting on the owner's rulings.

### What would change in `matt-craft`'s `design-doc` skill

`design-doc` lives in the external `matt-craft` pack mounted by `config/yolo-jail/config.toml`, outside this repository's fence. Making provisional capture official there would add three rules ([OQ-PD4](#OQ-PD4)):

1. **A fifth operation, *Provisional capture*:** alongside *drafting*, *review iteration*, *compacting*, and *auditing*, codify the immediate no-research capture pass and waive *Completeness / The holes, by name* while `PROVISIONAL` holds (requiring that floor upon promotion).
2. **Provisional status & stage convention:** specify the `**Status:** YYYY-MM-DD — PROVISIONAL ...` header line (and `stage: PROVISIONAL` if [OQ-PD2](#OQ-PD2) widens the seven-word stage table).
3. **Permanent compaction exemption for `## Owner prompt`:** preserve the opening verbatim owner quote untouched across review iteration and `OQ-*` compaction until the built doc graduates to `system-doc`.

---

## What this provisional document does not claim

- **No codebase-wide inventory or companion `-plan.md` seed** — deferred to the worked design pass once [OQ-PD1](#OQ-PD1)–[OQ-PD4](#OQ-PD4) are ruled.
- **No change to non-design build requests** — ordinary code fixes keep `single-worker@v1` or `planned-build@v1` ([intake paths](./tillr-intake-paths.md)).

---

## Open Questions

1. 💬 **OQ-PD1: Who should write Step 1 provisional design documents, and should the design-seat writer rule change?**

   Today [`stage.go`](../../tools/tillr-drain/drain/stage.go) allows only the drain's `design` seat to touch `docs/design/*.md`, and [`request.go`](../../tools/tillr-item/item/request.go) routes `design` requests through two-node [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json).

   - **A — Express one-run `provisional-design@v1` workflow on the `design` seat.** Keeps the `design`-only writer rule intact; lands in ~2–3 minutes via an express lane with scouting disabled.
   - **B — Dedicated express mid-tier `provisional` seat in the drain.** Carves out creation of *new* `PROVISIONAL` files so capture never waits behind frontier `design` quota; existing design edits stay `design`-only.
   - **C — Foreground agent writes immediately and hands a docs-only cut to `tillr-land`.** Fastest (~60–90 s, zero queue wait), carving out foreground creation of new `PROVISIONAL` files landed via `tillr-land`.

   <!-- vantage: question id=OQ-PD1 leaning="A first (or C when dictated live in an interactive session) — a one-run express design workflow hits ~2 minutes with zero writer-rule erosion, and an optional foreground cut helper can later shave the remaining minute." -->

   _Leaning:_ A first (or C when dictated live in an interactive session) — a one-run express design workflow hits ~2 minutes with zero writer-rule erosion, and an optional foreground cut helper can later shave the remaining minute.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-PD2: Should `PROVISIONAL` be a prose status-line marker under `stage: DESIGN`, or a new eighth stage in `.vantage.toml`?**

   [`.vantage.toml`](../../.vantage.toml) currently declares the seven `design-doc` stages (`SKETCH`, `DESIGN`, `DECIDED`, `BUILT`, `GRADUATED`, `SUPERSEDED`, `CURRENT`). `SKETCH` means *"nobody should act on it"*, whereas a provisional doc owes a worked design pass and initial rulings.

   - **A — Keep `stage: DESIGN` + `status: draft` and mark `**Status:** YYYY-MM-DD — PROVISIONAL ...`.** Zero Vantage schema churn; [`frontmatter.go`](../../tools/tillr-view/frontmatter.go) parses the `PROVISIONAL` status prefix for queue badges.
   - **B — Add `PROVISIONAL = "open"` to `[planning.stages]` in `.vantage.toml` (and `vantage-docs`).** Makes `stage: PROVISIONAL` first-class in Vantage badges and indexes across every repo that adopts it.
   - **C — Reuse `stage: SKETCH` with `**Status:** ... PROVISIONAL`.** Uses an existing word, though `SKETCH` currently signals "nobody should act on it" and hides urgency.

   <!-- vantage: question id=OQ-PD2 leaning="B if matt-craft adopts provisional docs officially (else A locally first) — stage answers 'what does this doc owe someone?', and 'owes a worked design pass' is distinct from both SKETCH and worked DESIGN." -->

   _Leaning:_ B if matt-craft adopts provisional docs officially (else A locally first) — stage answers 'what does this doc owe someone?', and 'owes a worked design pass' is distinct from both SKETCH and worked DESIGN.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-PD3: Once Step 1 lands a provisional doc on `main`, when should Step 2 (the worked design pass) run?**

   Step 1 carries initial `OQ-*` questions, which under `ReasonQuestionOpen` in [`plan.go`](../../tools/tillr-drain/drain/plan.go) would pause an item if treated like a finished design's blocking questions before the designer has even surveyed the tree.

   - **A — Queue Step 2 automatically at ordinary (non-express) priority unless the owner marks the item held.** Step 1 gives instant visibility without interrupting express work; Step 2 later researches the tree and refines the questions before pausing for owner rulings.
   - **B — Pause after Step 1 on `provisional` until the owner answers initial `OQ-*` questions or clicks "Work design".** Saves frontier design spend on rough thoughts the owner wants to steer or drop first.

   <!-- vantage: question id=OQ-PD3 leaning="A by default, with an intake flag (`--provisional-only`) for B — most dictated ideas want both instant trunk capture now and a worked design when idle background capacity frees up." -->

   _Leaning:_ A by default, with an intake flag (`--provisional-only`) for B — most dictated ideas want both instant trunk capture now and a worked design when idle background capacity frees up.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-PD4: Should `matt-craft`'s `design-doc` skill adopt provisional capture as an official operation?**

   `design-doc` in `matt-craft` (`config/yolo-jail/config.toml`) currently requires *Completeness / The holes, by name* on every drafted design doc and compacts deliberation history aggressively.

   - **A — Add *Provisional capture* to `matt-craft`'s `design-doc` skill (and `vantage-docs` if `stage: PROVISIONAL` is chosen).** Officially waives completeness while `PROVISIONAL`, standardizes the header marker across all repos, and exempts `## Owner prompt` from compaction.
   - **B — Keep provisional docs as a `tillr`-local convention in `tillr-designs` only.** Leaves `matt-craft` untouched and scopes the waiver to `tillr` drain intake.

   <!-- vantage: question id=OQ-PD4 leaning="A — the gap between dumping a raw design thought and having a clean, marked doc in the right shape happens in interactive non-tillr repos too, and preserving the verbatim owner prompt helps every design doc." -->

   _Leaning:_ A — the gap between dumping a raw design thought and having a clean, marked doc in the right shape happens in interactive non-tillr repos too, and preserving the verbatim owner prompt helps every design doc.

   **Answer:**

   > _(empty — fill in when decided)_
