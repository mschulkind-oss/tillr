---
title: "What a designer reads, checks, and measures itself, and what goes to cheaper scouts"
status: in-review
stage: DESIGN
tags:
  - tillr
  - drain
  - design
  - scouts
  - subagents
  - planning
  - seats
summary: "Defines what a drain designer does directly (drafting, reading named files, checking its own page) and what goes to cheap read-only scouts (broad search, test suites, store and log measurement), with the measurement behind the split, tool limits that stop long shell searches on the most expensive model without blocking small tasks, and the same question answered for the plan seat."
next: "The owner rules on OQ-RD1, OQ-RD2, and OQ-RD3 and reviews D-RD1 through D-RD5. Of the five, only D-RD4 is built."
depends-on:
  - tillr-planning-node-model-tiering.md
  - tillr-seat-profiles.md
  - ../reference/tillr-designer-sessions.md
  - tillr-design-document-check.md
  - tillr-graph-is-the-path.md
  - ../tillr/decision-provenance-review.md
vantage:
  status-chip: true
---

# Designers draft, read named files, and check their own page; broad search, test runs, and store measurements go to cheap scouts

**Status:** Proposal. `D-RD4` is built (`workflowProposalOnlyPredecessors` in
[`loop.go`](../../tools/tillr-drain/drain/loop.go) forwards a predecessor's report). The rest
is not: `rg 'allowedAgents|modelScope|child_cost' tools/tillr-run tools/tillr-drain` returns
nothing, and [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) still passes `--no-skills`.

> **In short.** A drain designer starts with unrestricted shell and search tools and no
> scout defaults, so it almost never delegates and does its searching at the price of the
> most expensive model. A designer should hold one warm session that reads named files,
> writes Markdown and checks its own file directly, and should send broad search, test suites
> and store or log measurement to cheap scouts. Tool limits enforce that; a total lockout of
> reading does not.

**Why it matters.** A scout's whole bounded investigation costs less than half of one design
turn ([Section 1.3](#13-how-often-scouts-ran-and-what-each-unit-cost)), and every line of
raw search output read into the design session is billed again on each later turn.

**The shape.** One warm `design` session with direct `read`, `write`, `edit` and narrow check
and commit commands, plus parallel read-only `scout` subagents pinned to the cheapest tier of
the active seat policy (`D-RD1`), with the designer's own search bounded
([OQ-RD1](#OQ-RD1), [OQ-RD2](#OQ-RD2)).

**Cost.** Retires or bypasses the separate `open` step of
[`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json), restricts
the designer's tools and subagents in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi)
and [`run-claude`](../../tools/tillr-run/backends/claude/run-claude), and adds child-session
cost to the run record.

**Start at [Section 3](#3-division-of-labor-what-the-designer-does-itself-and-what-goes-to-scouts).**

**Needs your ruling:** [OQ-RD1](#OQ-RD1), [OQ-RD2](#OQ-RD2), [OQ-RD3](#OQ-RD3).

**Reads with:** [planning and model tiering](./tillr-planning-node-model-tiering.md),
[named drain seat profiles](./tillr-seat-profiles.md),
[designer sessions](../reference/tillr-designer-sessions.md#the-desk) (warm document desks),
[the design document check](./tillr-design-document-check.md), and
[plan step and seat selection](./tillr-plan-step-and-seat-selection.md).

**Terms.** A **scout** is a read-only subagent on the cheapest model tier that answers a
bounded question and returns locators. **Tier 1, 2 and 3** are the cheap, mid and most
expensive model tiers a seat policy assigns; this document names tiers, never models.

---

## 1. What the 2026-11-18 and 2026-11-19 run records show

The split rests on one measurement: every `design`, `scout` and `plan` run in the store over
a two-day window, with tool calls counted from each run's event stream.

### 1.1 Sample windows and seat totals

| Seat | Runs | Parent tool calls | Runs that called a subagent | Runs that proposed a graph |
| :--- | ---: | ---: | ---: | ---: |
| `design` | 178 | 8,653 | 1 | 1 |
| `scout` | 50 | 1,417 | 0 | 0 |
| `plan` | 99 | 12,545 | 0 | 22 |

### 1.2 Where `design` runs and tool calls went

Of the 178 `design` runs, 159 were single-run dispatches (97 of them `conflict-design`), and
those carried 89% of the seat's spend. Only 19 ran inside a multi-node design workflow. The
seat made 4,548 shell calls and 3,787 file reads in the parent session. Classified by intent,
the shell calls were:

- **Search and history inspection:** 76%.
- **Document checks (`uvx vantage-check`):** 12%.
- **Code tests and gate scripts:** 11%, nearly all in `conflict-design` runs reproducing a
  worker's failure.
- **Staging and commits:** 4%.
- **Store and log measurement scripts:** 3%.

(Compound shell lines put a few calls in two categories.)

### 1.3 How often scouts ran, and what each unit cost

1. **Workflow scouts:** 1 of 9 `open` runs proposed scouts. The writer that followed still
   made 139 read and shell calls of its own.
2. **In-session scouts:** 1 of 178 `design` runs called a subagent. Its scouts ran on the
   interactive default model, a tier above the scout tier, because nothing pinned them.
3. **Unit cost:** a bounded scout run averaged $0.029 for about 13 turns; a `design` turn
   averaged $0.066. Per turn, the designer cost about 30 times the scout.

### 1.4 What could not be measured

1. **Child-session spend.** [`extract-report`](../../tools/tillr-run/backends/pi/extract-report)
   sums only the parent session's turns, so subagent spend is absent from the store's totals
   (`D-RD3`).
2. **Tool counts on runs that left no event stream.**
   [`bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh) traces only shell subprocesses.
3. **Billing.** Run costs are a client-side estimate at list prices.
4. **Document quality.** With one workflow-scouted and one subagent-scouted item, the records
   show that scouts go unused and what each unit costs; they cannot compare the quality of
   the resulting documents.

---

## 2. Today's drain workflow compared with interactive design

On 2026-10-22 the owner ruled that the designer writes and edits its own Markdown directly
and that delegation is for read-only scouts only; delegating prose to a writer and checks to
a verifier spends turns on coordination and flattens the author's judgment. An interactive
design session follows that ruling. The drain differs in five ways:

| Dimension | Interactive design | The drain |
| :--- | :--- | :--- |
| **Session continuity** | One warm session holds framing, trade-offs, scout questions and drafting. | Two cold sessions, `open` then `write`, separated by a drain pass. |
| **When scouts are dispatched** | On demand: the designer reads a few named documents, finds specific unknowns, and dispatches parallel scouts. | Up front: `open` must commit to a scout graph before drafting begins. |
| **Parent tools** | Direct `read`, `write`, `edit` and subagents; no open-ended shell search. | `run-pi` passes no tool restriction, so shell, search and scripting tools are all present. |
| **Authoring skills** | The design and Markdown style skills are loaded. | `run-pi` passes `--no-skills`. |
| **Scout model** | The cheap read-only tier. | The interactive default, unpinned. |

The drain's two-node workflow also covers only a minority of design spend
([Section 1.2](#12-where-design-runs-and-tool-calls-went)), so a fix must reach single-run
and `conflict-design` dispatches too.

---

## 3. Division of labor: what the designer does itself and what goes to scouts

Four principles:

- **P1: keep bulk intermediate text out of the designer's context.** Scouts absorb search
  noise in disposable contexts and return compact, cited tables.
- **P2: never delegate synthesis, prose, or the framing of an open question.**
- **P3: keep a no-delegation path for named files and single-file checks.** A subagent adds
  30 to 90 seconds of wall time; reading a file the brief names, or checking the file just
  written, is cheaper and more accurate in the session.
- **P4: scouts return locators, never bare assertions:** repo-relative paths, heading or
  symbol names, commands run and sample counts, so the designer can read any claim that
  carries weight.

### 3.1 The five activities, split cleanly

| Activity | The designer does itself | Goes to read-only scouts | Why the boundary sits here |
| :--- | :--- | :--- | :--- |
| **Drafting and revising** | All of it: design documents, ledgers, open questions, conflict resolutions. | None. | Prose and trade-off framing are inseparable from the design judgment. |
| **Reading the tree** | Named files, typically one to ten: the target, files the brief names, direct `depends-on` designs, and passages a scout flagged. | Broad surveys of six or more unknown files: behavior tables across packages, sweeps of other ledgers, call chains across modules. | A direct read of a known file keeps exact wording; a twenty-file survey fills the expensive context with facts a scout summarizes. |
| **Searching** | A handful of quick status or single-symbol checks, if [OQ-RD2](#OQ-RD2) allows them. | All broad discovery and negative-claim proofs: repo-wide searches, id collision checks, history archaeology, directory walks. | Search is three quarters of the designer's shell use; one parallel scout batch replaces many serial turns. |
| **Measuring** | Specifying the metric (population, window, denominator, caveats) and reading the table; a one-command spot check. | Writing and iterating on measurement scripts over the store and logs. | Working out schemas and log shapes takes many turns, which cost far less on a scout. |
| **Running checks** | Checks on its own diff: `uvx vantage-check` on the target, the fast document gate checks, status, add, commit, land. | Heavy or repo-wide suites, and reproducing a code or test failure during `conflict-design`. | Delegating a single-file check recreates the verifier loop the owner rejected; running suites in the session is the measured waste. |

### 3.2 Applying the split to `conflict-design` (60% of `design` spend)

`conflict-design` runs carried 60% of the seat's measured spend, made no scout calls, and ran
484 of the seat's 501 test and gate commands. Under `D-RD5` they get the same scouts and a
two-step triage:

1. Read the conflict brief, the item's design document and up to four named files in the
   session. If those settle the conflict, resolve it with no subagent.
2. If diagnosis needs a test reproduced, callers checked across packages, or other ledgers
   audited, launch one parallel scout batch, then write the resolution in the session.

---

## 4. Enforcement versus instruction, specific limits, and avoiding delegation handcuffing

### 4.1 Why instruction alone is not enough, and why a lockout is worse

1. **Instruction alone yields almost no delegation.** The design workflow already tells
   designers to use scouts; with unrestricted search tools beside them, 176 of 178 runs did
   not.
2. **Delegation handcuffing** *(coined here)*: restricting a high-judgment agent's direct
   read or check tools so tightly that it spends more turns and wall time briefing subagents
   than the small task would have cost directly. It is not healthy parallel fan-out on a
   broad search. Removing `read` forces a round trip to read a short file, and because scouts
   summarize, a designer that cannot read the passage where an edge case lives asks several
   follow-up rounds instead.

### 4.2 Comparing the enforcement postures ([OQ-RD1](#OQ-RD1), [OQ-RD2](#OQ-RD2))

**Session topology ([OQ-RD1](#OQ-RD1)).** Retiring the separate read-only `open` node lets a
design item start in one warm writing session that calls scouts when a gap appears.

**Tool limits on the `design` seat ([OQ-RD2](#OQ-RD2)).** Under every option, `D-RD1` limits
the designer's subagents to read-only kinds with the scout pinned to Tier 1, and `D-RD2`
loads the authoring skills. The options differ in how tightly the parent's tools are bounded:

| Posture | Parent tools | Limits | Risk of the designer searching itself | Risk of handcuffing |
| :--- | :--- | :--- | :--- | :--- |
| **A: a check-and-commit tool, no raw shell** | `read`, `write`, `edit`, subagents, landing and obstacle calls, and one narrow tool for document checks and commits | No arbitrary shell. A warning at 15 direct reads without a scout call; a cap of 30 until a scout batch completes. | Lowest | Moderate: even a one-pattern search needs a scout. |
| **B: shell kept, search and script tools removed, a 10-call search budget** *(leaning)* | `read`, `write`, `edit`, shell, subagents, landing and obstacle calls | Document checks, status, diff, add and commit are uncapped. Every other shell call warns at 6 and is refused after 10, with a message saying how to batch a scout call. Direct reads warn at 15, cap at 30, plus 15 per completed scout batch. | Low | Very low: small edits and quick conflict checks finish with no scout. |
| **C: instructions and Tier-1 scout defaults only** | Unchanged | None; relies on guidance and the run record (`D-RD3`). | High | None |

### 4.3 Edge cases, invariants, and observable completion

- **Small items.** An item naming one to three files with no measurement owed completes with
  no scout call. The brief discourages scouts when nothing is unknown.
- **Scout failure.** Each subagent call carries a 300-second timeout. If a scout errors or
  returns nothing after one retry on the seat's fallback chain, the guard of option A or B
  unlocks the parent's own read and shell for that run and records `scout_fallback: true` on
  the run record, so the designer never deadlocks.
- **One writer.** Every child of a `design` run is read-only. The parent is the sole writer
  of the lane worktree and its progress file. Scouts launch in one parallel call, at most
  four per batch.
- **Runs in flight.** Tool settings apply when the harness process starts. A live run
  finishes under the settings it started with; a resumed
  [designer desk](../reference/tillr-designer-sessions.md#the-desk) session takes the new
  tools on its next turn.
- **Forbidden.** A `design` run never spawns a writing subagent or a Tier-3 child, never
  drops child totals from the run record, and never runs the full local gate inside a lane.
- **What done looks like (`D-RD3`).** The run record and `tillr drain why` show parent and
  child token and cost totals, the number of child runs, and the parent's read and non-check
  shell counts, so both too little and too much delegation are visible.

---

## 5. How this fits the plan-step and seat-selection design, and the rule for the `plan` seat

### 5.1 Alignment with plan step and seat selection

[Plan step and seat selection](./tillr-plan-step-and-seat-selection.md) reaches the same
shape for `planned-build@v1`:

1. **The same two-session handoff.** A `plan` node that proposes no split and an `open` node
   that proposes no scouts are the same case. Retiring the upfront node in each gives one
   warm session that fans out only when the code warrants it. For any multi-stage workflow
   that remains, `D-RD4` carries the predecessor's report forward.
2. **One child-policy source.** Build fan-out and design scouting both use one child-policy
   file that the drain compiles at staging from the active
   [seat policy](./tillr-seat-profiles.md) and that both adapters enforce. No adapter
   hard-codes a model name.
3. **More `conflict-design` traffic.** Letting a filed request escalate from `build` to
   `design` replaces build retries with `conflict-design` runs, which is why `D-RD5` matters.

### 5.2 Answering the same question for the `plan` seat ([OQ-RD3](#OQ-RD3))

The 99 measured `plan` runs made 12,545 parent tool calls, about 151 per logged run, and no
subagent call. What a planner reads itself depends on its model tier:

| Planning context | Tier | Does itself | Goes to Tier-1 scouts | Enforcement |
| :--- | :--- | :--- | :--- | :--- |
| A companion-plan writer, or a builder orienting on its first turn | Tier 2 | Reads the target packages, verifies symbols, runs targeted tests, writes the plan or the code. | Parallel surveys only when a design spans three or more packages. | Instructions and the model ceiling of `D-RD1`; no read or shell cap. The price gap to Tier 1 is narrow and exact code mapping is the deliverable. |
| An upfront `plan` node kept in [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) | Tier 3 | Reads the item, its design and up to ten boundary files; chooses the split. | All broad file discovery, caller enumeration and test mapping, in one scout batch. | The same bounds as `design` ([OQ-RD2](#OQ-RD2)). |

---

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| Lock the designer out of reading and writing; delegate prose to a writer and checks to a verifier | Rejected: overturns the owner's 2026-10-22 ruling. |
| Keep the `open` node and force it to spawn scouts every time | Rejected: adds a pass of latency per stage, forces decomposition before the designer knows the questions, and leaves the writer cold when follow-up questions arise. |
| Let `design` spawn writing subagents or nested scouts | Rejected: breaks single-writer ownership of the lane worktree and blurs design with implementation. |
| Cap total `design` turns | Rejected: the owner's standing rule is against arbitrary retry and turn limits ([the graph design](./tillr-graph-is-the-path.md)), and a cap would stop a long document mid-write. |

---

## 7. What this does not cover

- Which model a policy assigns to `design`, `plan` or `scout`
  ([named drain seat profiles](./tillr-seat-profiles.md)).
- Style compliance or review of a design after commit
  ([the design document check](./tillr-design-document-check.md),
  [diff review before landing](./tillr-diff-review.md)).
- Whether `planned-build@v1` is kept
  ([plan step and seat selection](./tillr-plan-step-and-seat-selection.md)).
- Interactive design sessions outside the drain; `D-RD2` keeps the two tool surfaces
  consistent.

---

## 8. Sequencing and amendments owed

1. **Child-session totals in the run record (`D-RD3`)**, first, so every later change is
   measured with child spend included.
2. **Run-scoped subagent tiers and authoring skills (`D-RD1`, `D-RD2`)** in both adapters.
3. **Workflow topology ([OQ-RD1](#OQ-RD1)) and the `conflict-design` scout brief (`D-RD5`).**
4. **The chosen tool limits on `design` and a Tier-3 `plan`
   ([OQ-RD2](#OQ-RD2), [OQ-RD3](#OQ-RD3))**, with the fallback unlock.

When built, [planning and model tiering](./tillr-planning-node-model-tiering.md) links
[Section 5.2](#52-answering-the-same-question-for-the-plan-seat-oq-rd3).

---

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| A designer hits the search cap mid-investigation and writes a thin design | The refusal names the scout call to make; reads, document checks and commits stay uncapped; a failed scout unlocks the parent's shell. |
| A scout misses an invariant or misreads a control path | Scouts return locators (`P4`), and the designer reads every passage that carries weight. |
| Designers over-delegate small edits | The no-scout path of [Section 3.1](#31-the-five-activities-split-cleanly) and option B. |
| A provider rate-limits parallel scout bursts | Batches are capped at four and use the seat's fallback chain. |
| Child spend grows unseen | `D-RD3` ships before the tool limits. |

---

## Decision Ledger

The design inherits the owner's 2026-10-22 rulings: the designer reads, writes and edits its
own Markdown, and delegation is for read-only scouts. `D-RD1` to `D-RD5` are engineering
decisions marked as the designer's under the rule recorded in
[the decision provenance review](../tillr/decision-provenance-review.md#decision-ledger); the
owner may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-RD1` | The drain compiles run-scoped subagent settings from the active seat policy and both adapters enforce them: a `design` run's children are read-only kinds, and the scout runs on the policy's Tier-1 model. | 2026-11-20, designer | [Section 4.2](#42-comparing-the-enforcement-postures-oq-rd1-oq-rd2), [Section 5.1](#51-alignment-with-plan-step-and-seat-selection) | No |
| `D-RD2` | `design`-seat runs load the design and Markdown authoring skills explicitly instead of running with no skills. | 2026-11-20, designer | [Section 2](#2-todays-drain-workflow-compared-with-interactive-design), [Section 4.2](#42-comparing-the-enforcement-postures-oq-rd1-oq-rd2) | No |
| `D-RD3` | Child-session token counts, models, tool-call counts and estimated cost are rolled into the run record and the store, so subagent spend is visible. | 2026-11-20, designer | [Section 1.4](#14-what-could-not-be-measured), [Section 4.3](#43-edge-cases-invariants-and-observable-completion) | No |
| `D-RD4` | A stage with no wired inputs receives its predecessor's report when that predecessor proposed no graph replacement. | 2026-11-20, designer | [Section 5.1](#51-alignment-with-plan-step-and-seat-selection) | Yes |
| `D-RD5` | `conflict-design` runs get the same Tier-1 scouts and the two-step triage. | 2026-11-20, designer | [Section 3.2](#32-applying-the-split-to-conflict-design-60-of-design-spend) | No |

---

## Open Questions

1. 💬 **OQ-RD1: Should a design item start directly in one writing designer session instead of running a separate scout-planning session first?**

   A design workflow item first wakes a separate read-only session that decides whether to
   schedule scout runs before the writer starts. Measured over nine such runs, eight
   scheduled no scouts; the one that did was followed by a writer that still made 139 file
   and shell calls.

   - **A — One warm writing session that calls cheap scouts mid-session.** Starts in the
     writer and fans out when real gaps appear.
   - **B — Keep the separate upfront session.** Keeps graph-scheduled scout runs before
     writing, and pays for two cold sessions per item.

   <!-- vantage: question id=OQ-RD1 leaning="A — one warm designer session holds the whole argument and dispatches scouts when real gaps appear, whereas the upfront session scheduled no scouts in 8 of 9 runs." -->

   _Leaning:_ A — one warm designer session holds the whole argument and dispatches scouts when real gaps appear, whereas the upfront session scheduled no scouts in 8 of 9 runs.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-RD2: How strictly should tool limits push a designer to use cheap scouts instead of searching and scripting itself?**

   With unrestricted shell and search tools, one design run in 178 called a scout, while a
   design turn costs more than twice a whole scout run. Yet blocking file reads or quick
   checks traps small tasks in slow delegation.

   - **A — Direct reading and editing, plus narrow check-and-commit commands only.** Every
     search, test suite and measurement script needs a scout.
   - **B — Keep the shell, remove dedicated search and script tools, and cap shell searches
     at 10.** Document checks and commits stay uncapped, so a two-file check needs no scout.
   - **C — Instructions and cheap scout defaults only.** Relies on the model's judgment.

   <!-- vantage: question id=OQ-RD2 leaning="B — stripping search and script tools plus a 10-call shell search cap stops 40-turn frontier shell crawls mechanically without forcing a 60-second scout round-trip on a two-file question." -->

   _Leaning:_ B — stripping search and script tools plus a 10-call shell search cap stops 40-turn frontier shell crawls mechanically without forcing a 60-second scout round-trip on a two-file question.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-RD3: What reading and scout rules should apply to planning work once the upfront planning step's fate is settled?**

   Planners on the most expensive tier average about 151 file, shell and script calls per run
   with no scout call. Planning may move to mid-tier builders and companion-plan writers, or
   keep an upfront node as an opt-in.

   - **A — Match the rules to the model tier.** Mid-tier planners read code and run targeted
     tests directly and call scouts only for surveys across three or more packages; a
     retained Tier-3 planner gets the search cap and scouts.
   - **B — Apply the cap and scouts to mid-tier planners too.** Trades extra delegation for
     smaller contexts.
   - **C — Leave planning runs unrestricted.**

   <!-- vantage: question id=OQ-RD3 leaning="A — delegation pays when the parent-to-scout price gap is steep: frontier planners crawling code waste dollars per run, whereas handcuffing a mid-tier planner whose whole job is mapping code files adds churn for pennies." -->

   _Leaning:_ A — delegation pays when the parent-to-scout price gap is steep: frontier planners crawling code waste dollars per run, whereas handcuffing a mid-tier planner whose whole job is mapping code files adds churn for pennies.

   **Answer:**

   > _(empty — fill in when decided)_
