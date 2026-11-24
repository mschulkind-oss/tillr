---
title: "What a designer reads, checks, and measures itself, and what goes to cheaper scouts"
status: in-review
stage: DESIGN
updated: 2026-11-24
tags:
  - tillr
  - drain
  - design
  - scouts
  - subagents
  - planning
  - seats
summary: "Audits all 2026-11-18 and 2026-11-19 design, scout, and plan runs in the tillr store (145 design runs / $345.16 in Eastern time up to filing; 178 runs / $394.56 in UTC; 8,653 parent tool calls, 1 subagent scout call, and 1 workflow scout proposal), compares scouted-design@v1 and run-pi against the paired-design skill and the owner's 2026-10-22 rulings on what a designer does itself, defines what a designer does directly versus what goes to Tier-1 scouts across reading, searching, checking, measuring, and drafting, proposes asymmetric tool limits that stop 40-turn frontier shell crawls without handcuffing small tasks, and answers the same question for the plan seat alongside item-request-05ac7dd8a0."
next: "The owner rules on OQ-RD1, OQ-RD2, and OQ-RD3 and reviews D-RD1 through D-RD5; nothing is built."
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

**Status:** Proposal; nothing here is built. Run-record audit measured across all 2026-11-18 and 2026-11-19 runs ([Section 1](#1-what-the-2026-11-18-and-2026-11-19-run-records-show)).

> **In short.** Drain designers currently launch with unrestricted shell and search tools plus broken subagent defaults, so across 178 runs ($394.56) they delegated to scouts twice while executing 4,548 shell commands and 3,787 file reads at frontier-model cache prices. A designer should hold one warm session that reads named files, writes Markdown, and runs single-file document checks directly, while delegating broad search, test suites, and store/log measurement scripts to tier-pinned cheap scouts enforced by asymmetric tool bounds rather than total read lockout.

**Why it matters.** Over 2026-11-18 and 2026-11-19, a `design` turn averaged **$0.066** ($394.56 across 5,955 turns) while a bounded cheap `scout` run averaged **$0.029** across an entire 13-turn investigation — yet only **1 of 9** [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) `open` runs proposed workflow scouts (discarding `open`'s report on the other 8), and only **1 of 178** UTC `design` runs called `subagent` ([Section 1.3](#13-how-often-scouts-ran-and-what-each-unit-cost)).

**The shape.** One warm `design` session (matching `paired-design`) with direct `read`, `write`, `edit`, and narrow check/commit execution, paired with parallel Tier-1 `scout` subagents pinned from the active drain seat policy (`D-RD1`) and guarded by stripping dedicated search/script tools plus bounding parent shell searches ([OQ-RD1](#OQ-RD1), [OQ-RD2](#OQ-RD2)).

**Cost.** Retires or bypasses [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json)'s upfront `open` step ([OQ-RD1](#OQ-RD1)), restricts `pi-designer` and `claude-designer` tool and subagent settings in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) and [`run-claude`](../../tools/tillr-run/backends/claude/run-claude), and adds child-session cost rollup to [`extract-report`](../../tools/tillr-run/backends/pi/extract-report) and [`progress.go`](../../tools/tillr-run/run/progress.go).

**Start at [Section 1](#1-what-the-2026-11-18-and-2026-11-19-run-records-show)** for the two-day empirical audit, [Section 2](#2-todays-drain-workflow-compared-with-paired-design) for the comparison with `paired-design`, and [Section 3](#3-division-of-labor-what-the-designer-does-itself-and-what-goes-to-scouts) for the operational split.

**Needs your ruling:** [OQ-RD1](#OQ-RD1), [OQ-RD2](#OQ-RD2), and [OQ-RD3](#OQ-RD3) (five engineering choices, `D-RD1` through `D-RD5`, await review in the [Decision Ledger](#decision-ledger)).

**Reads with:** [planning and model tiering](./tillr-planning-node-model-tiering.md) (orientation cost and piece fan-out), [named drain seat profiles](./tillr-seat-profiles.md) (model sets and child bounds), [designer sessions](../reference/tillr-designer-sessions.md#the-desk) (warm document desks), and [the compliance officer](./tillr-design-document-check.md) (post-gate style enforcement). No companion plan exists while [OQ-RD1](#OQ-RD1)–[OQ-RD3](#OQ-RD3) are open.

---

## 1. What the 2026-11-18 and 2026-11-19 run records show

Before proposing changes, every run record, lane JSONL stream, graph revision, and child-session event in the repository's live SQLite store and loop artifacts was audited across `2026-11-18` and `2026-11-19`.

### 1.1 Sample windows and seat totals

Because store timestamps in `runs.started_at` are UTC epoch milliseconds while the owner works in `America/New_York` (ET, filing `item-request-b92ccc0180` on `2026-11-19` at ~20:35 ET / `2026-11-20T01:35Z`), both windows were measured so sample sizes are exact:

| Seat | ET up to filing (`[2026-11-18 00:00 ET, 2026-11-19 20:35 ET)`) | UTC two-day window (`[2026-11-18T00:00Z, 2026-11-20T00:00Z)`) | Parent tool calls (ET / UTC logged runs) | Runs calling `subagent` (ET / UTC) | Runs writing `graph.proposal` (ET / UTC) |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`design`** | **145 runs** (142 logged), **$345.16**, 638.8M tokens | **178 runs** (175 logged), **$394.56**, 657.5M tokens | **8,426 / 8,653** | **1 / 1** (3 child scouts) | **1 / 1** (4 scout nodes) |
| **`scout`** | **50 runs** (50 logged, 50 `rc=0`), **$4.88**, 73.9M tokens | **50 runs** (50 logged, 50 `rc=0`), **$4.88**, 73.9M tokens | **1,417 / 1,417** | **0 / 0** | **0 / 0** |
| **`plan`** | **105 runs** (89 logged), **$199.54**, 581.5M tokens | **99 runs** (83 logged), **$184.75**, 535.5M tokens | **13,512 / 12,545** | **0 / 0** | **23 / 22** |

*(Extending ET through midnight `2026-11-20 00:00 ET` adds 4 `design` runs to `n = 149` / `$348.24` — including sibling request `item-request-05ac7dd8a0`, which became the second `design` run to spawn 3 `scout` subagents — plus our own active run `run-item-request-b92ccc0180-a1-n2`, whose 3 `gemini-3.8-flash` scouts cost `$0.9714` across 508,705 tokens. The earlier midday 2026-11-19 audit of 55 completed `plan` runs at ~$129 reflects completed `exit_code = 0` `plan` runs before late-evening retries, timeouts, and `pi-build` fallback dispatches accumulated to 52 `rc=0` of 105 ET runs at filing and 57 `rc=0` of 114 by ET midnight.)*

### 1.2 Where `design` runs and tool calls went

Across the **178 UTC `design` runs** (175 with stream logs on disk; 98 on Bedrock `claude-fable-5-2`, 65 on Copilot `gemini-4-carbon`, 10 fallback runs on `gemini-4-hydrogen`, 1 on Bedrock `gpt-6.1-terra`, 3 timed-out without model summary, and 1 non-zero Fable exit), **89.3% of spend ($352.51)** occurred outside [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) multi-node execution:

| `design` dispatch role (UTC window; ET up-to-filing in parentheses) | Runs (`logged`) | Parent cost | Turns | `bash` | `read` | `edit` | `write` | `codemode` | `subagent` |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Single-run dispatches** — led by **97 `conflict-design` runs ($238.09)** and **53 standalone design items ($104.01)**, plus 9 unworkflowed/aborted dispatches *(ET: 124 runs, $299.53; 62 `conflict-design` at $185.11)* | **159** (`156`) | **$352.51** | 5,077 | 3,960 | 2,829 | 191 | 5 | 0 | **0** |
| **[`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) `open`** (read-only orientation) *(ET: 10 runs, $8.50)* | **9** (`9`) | **$7.72** | 184 | 151 | 296 | 0 | 1 | 55 | **0** |
| **[`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) `write`** (no-scout writer) *(ET: 9 runs, $32.28)* | **8** (`8`) | **$29.48** | 575 | 354 | 515 | 26 | 8 | 0 | **1** *(3 children)* |
| **[`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) `write/join`** (after 4 workflow scouts) *(ET: 1 run, $3.28)* | **1** (`1`) | **$3.28** | 75 | 57 | 82 | 4 | 2 | 0 | **0** |
| **[`doc-triage@v1.json`](../../config/tillr/workflows/doc-triage@v1.json) `report`** (synthesizing 43 scout reports) *(ET: 1 run, $1.57)* | **1** (`1`) | **$1.57** | 44 | 26 | 65 | 2 | 0 | 0 | **0** |
| **Total UTC `design` seat** *( plus 13 `background_task` and 7 `bg_wait` calls on single-run dispatches)* | **178** (`175`) | **$394.56** | **5,955** | **4,548** | **3,787** | **223** | **16** | **55** | **1** *(3 children)* |

Classifying the **4,548 parent `bash` calls** on `design` by command intent shows what designers used the shell for:

- **Repository search and Git inspection (`rg`, `grep`, `find`, `git log`, `git show`, `ls`):** **3,459 calls (76.1% of `bash`)** — broad tree discovery executed directly inside the frontier model's prompt cache.
- **Document rendering checks (`uvx vantage-check`):** **553 calls (12.2%)** — checking target and sibling Markdown files.
- **Code tests and gate scripts (`go test`, `pytest`, `tools/gate/checks/*`):** **501 calls (11.0%)** — concentrated in `conflict-design` runs reproducing worker failures (484 of the 501 calls).
- **Staging and commits (`git add`, `git commit`):** **189 calls (4.2%)**.
- **Store and log measurements (`sqlite3`, ad-hoc `python3` scripts over `state.db` and `.log` files):** **143 calls (3.1%)** — multi-turn debugging of SQL schemas and JSONL parsers on Fable and Carbon. *(Categories overlap slightly where compound shell lines chained commands.)*

### 1.3 How often scouts ran, and what each unit cost

1. **Workflow scouts via [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json): 1 of 9 UTC `open` runs (1 of 10 ET).** Only `run-item-request-8008c05e5c-a1-n1` wrote a [`design-from-scouts@v1.json`](../../config/tillr/workflows/design-from-scouts@v1.json) proposal. Its 4 `scout` nodes (`write/scout-1..4`) ran 49–91 turns each for **$2.94 total** ($0.50–$1.03 per scout), after which `write/join` spent **$3.28** and still executed **82 `read` and 57 `bash` calls** itself. On the other **8 of 9 UTC `open` runs** (and 9 of 10 ET, including our own item's `n1` run at 46 tool calls and $0.41), `open` read the tree, proposed **no** scouts, and had its entire `node-report.md` dropped before `write` because `(*Workflow).hasNamedFiles()` in [`node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go) suppressed `PreviousReport` in [`loop.go`](../../tools/tillr-drain/drain/loop.go).
2. **In-session `subagent` scouts: 1 of 178 UTC `design` runs (1 of 145 ET up to filing).** Only `run-item-request-8126862ae5-a1-n2` invoked `subagent`, launching 3 parallel `scout` children (35 child turns, 109 child tool calls, **$0.7025** total child cost). Because [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) inherited interactive `subagents.defaultModel`, those 3 scouts ran on Tier-2 `gemini-4-hydrogen` instead of Tier-1 `gemini-3.8-flash`. In our own active run (`b92ccc0180`), explicitly requesting `github-copilot/gemini-3.8-flash` ran 3 parallel scouts across 508,705 tokens for **$0.9714** total.
3. **Unit-cost ratio (30x per turn on bounded scouts):** Across all 50 `scout`-seat runs ($4.88 total), the **45 bounded [`doc-triage@v1.json`](../../config/tillr/workflows/doc-triage@v1.json) scouts** on `gpt-6-luna` (44) and `gemini-3.8-flash` (1) averaged **$0.029 per run** (~13 turns, ~21 tool calls, **$0.0022 per turn**), versus **$2.22 per run** and **$0.066 per turn** on `design`. Even open-ended 70-turn workflow scouts ($0.74 mean) cost a third of a `design` run while keeping 5–9M tokens of raw grep output out of the designer's prompt cache.

### 1.4 What could not be measured

1. **Unrolled child-session spend in `state.db` and `run.record.json`.** [`extract-report`](../../tools/tillr-run/backends/pi/extract-report) sums only top-level parent `turn_end` events into `runs.usage_tokens` and `runs.usage_cost_usd`. Child subagent spend ($0.7025 on `8126862ae5`, $0.9714 on `b92ccc0180`, and any child spend on `05ac7dd8a0`) is omitted from SQLite totals and had to be read from `tool_execution_end.result.details.totalCost` in parent `.log` files and `pi-subagents` status receipts.
2. **Tool breakdowns on 3 unlogged `design` runs and 16 unlogged `plan` runs.** Timed-out or terminated runs without a surviving lane `.log` stream could not contribute in-process Pi tool counts (`read`, `edit`, `write`, `codemode`, `subagent`), because [`bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh) (`proc-exec.jsonl`) traces only `bash` subprocesses.
3. **Per-tool histograms inside past child sessions.** `run.child-session` events record container-local session paths inside `/home/agent/` rather than shared `/workspace/.tillr/` artifacts; `0 of 3` child JSONL transcripts from `8126862ae5` remain mounted in this container, leaving only aggregate child turn/tool/cost receipts in the parent log.
4. **Actual provider billing versus list-price estimates.** `runs.usage_cost_usd` reflects Pi's client-side token-rate estimator across mixed Bedrock (`claude-fable-5-2`) and Copilot (`gemini-4-carbon`) runs; Copilot bills premium requests rather than list-price dollars, and 4 timed-out `design` runs recorded `NULL`/`$0.00` despite partial execution.
5. **Matched document-quality counterfactuals.** With `n = 1` workflow-scouted item and `n = 1` subagent-scouted item in the pre-filing window, the observational logs prove adoption failure and cache-cost ratios but cannot measure prose or decision quality deltas on matched prompts without controlled A/B runs.

---

## 2. Today's drain workflow compared with `paired-design`

Comparing [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) and [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) against the interactive `paired-design` skill and the owner's 2026-10-22 rulings exposes four concrete divergences.

The owner's **2026-10-22 rulings** settle the extreme case: a designer that delegates prose to a cheaper writer and checks to an LLM verifier burns turns in coordination loops and flattens authorial judgment. The designer therefore has direct Markdown `read`, `write`, and `edit`, and **subagent delegation is strictly for read-only scouts** (`scout`, `researcher`, `evidence-auditor`).

| Dimension | Interactive `paired-design` + the 2026-10-22 rulings | Drain today: [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) + [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) | Consequence measured on 2026-11-18..19 |
| :--- | :--- | :--- | :--- |
| **Session continuity** | **One warm frontier session** holds framing, trade-offs, scout questions, and drafting end-to-end. | **Two cold frontier sessions** (`open` then `write`) separated by a drain pass, with `open`'s report dropped whenever `open` proposes no scouts (`(*Workflow).hasNamedFiles()`). | 8 of 9 UTC `open` runs wasted their entire reading pass; `write` started cold. |
| **When scouts are dispatched** | **On demand mid-thought:** designer reads 2–4 named docs, spots exact codebase or data unknowns, and dispatches parallel scouts in-session. | **Forced upfront (`open`) or unprompted in `write`:** `open` must pre-commit a static scout DAG before drafting begins. | Designers skipped upfront DAGs 89% of the time; the 1 `write/join` run still made 139 read/bash calls as follow-up questions arose. |
| **Parent tool surface** | Direct `read`, `write`, `edit` on Markdown + `subagent`; **no ambient `grep`/`find`/`codemode` or open-ended shell crawling**. | [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) passes **no `--tools` flag** and no designer agent profile, exposing unrestricted `bash`, `grep`, `find`, `ls`, `codemode`, and `background_task`. | Zero friction on local `bash`/`read` led to 4,548 `bash` calls, 3,787 `read` calls, and 99.4% non-use of `subagent`. |
| **Authoring skills loaded** | Loads `design-doc` and `vantage-docs` in system context. | [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) passes `--no-skills`, hiding `design-doc`, `vantage-docs`, and `tillr-designs` unless the designer hunts for them on disk. | Designers miss house style rules that [the compliance officer](./tillr-design-document-check.md) checks at commit time. |
| **Scout model routing** | Cheap read-only scout tier. | Inherits interactive `subagents.defaultModel` (`gemini-4-hydrogen`) and 14-model `modelScope.allow`. | Unpinned `scout` children ran on Tier-2 `Hydrogen 4` instead of Tier-1 `Flash 3.8`. |
| **Coverage of `design` spend** | Governs all design sessions. | Applies only to `scouted-design@v1` items; **159 of 178 UTC runs ($352.51)** were single-run `conflict-design` or standalone design dispatches. | Fixing `scouted-design@v1` alone leaves 89% of `design` spend untouched unless `run-pi` and `conflict-design` share the same scout posture. |

---

## 3. Division of labor: what the designer does itself and what goes to scouts

Four principles govern where frontier tokens earn their price and where they waste it:

- **P1 — Protect the frontier prompt cache from bulk intermediate text.** Every kilobyte of raw `rg` hits, Go test logs, or SQLite schema trial-and-error read into a `design` session is re-billed on every subsequent drafting turn. Cheap scouts absorb search noise in disposable contexts and return compact, cited tables.
- **P2 — Never delegate synthesis, prose, or open-question framing.** Per the owner's 2026-10-22 ruling, the designer writes and edits its own Markdown directly.
- **P3 — Keep a zero-delegation fast path for named files and single-file checks.** Spawning a subagent adds 30–90 seconds of wall time plus prompt-marshaling turns. Reading a file the brief already names or running `uvx vantage-check` on the file just written is cheaper and more accurate in-session than through a scout.
- **P4 — Scouts return verifiable locators, never bare assertions.** Every scout brief requires exact repo-relative paths, heading or symbol names, commands run, and sample counts so the designer can spot-read any load-bearing claim directly.

### 3.1 The five activities, split cleanly

| Activity | Designer does itself (Tier 3: `design` seat) | Goes to cheaper read-only scouts (Tier 1: `scout` subagents) | Why the boundary sits here |
| :--- | :--- | :--- | :--- |
| **Drafting & revising** | **All of it:** writing and editing `docs/design/*.md`, Decision Ledgers, walkthrough Open Questions, and `conflict-design` resolutions (`write`, `edit`). | **None.** Scouts never draft design prose, choose trade-offs, or formulate owner questions. | Preserves the 2026-10-22 owner ruling retiring `writer`: prose and trade-off framing are inseparable from architectural judgment. |
| **Reading the tree** | **Named and load-bearing files (typically 1–10 files):** the target doc, files named in the item/conflict brief, direct `depends-on` sibling designs, and specific sections/functions flagged by a scout where exact wording matters. | **Broad multi-file surveys (6+ unknown files):** scanning packages to build a behavior table, sweeping `docs/design/` Decision Ledgers for prior rulings, or tracing call chains across modules. | Direct `read` on a known file takes <1 s and preserves exact nuance; surveying 20 files in-session bloats the frontier cache for facts a $0.03 scout summarizes cleanly. |
| **Searching** | **Zero-turn or surgical checks only** (at most a handful of quick `git status`/`git log -n 5` or single-symbol anchor checks when allowed under [OQ-RD2](#OQ-RD2) Option B). | **All broad discovery and negative-claim proofs:** repo-wide `rg` symbol/citation searches, `OQ-*` collision checks, `git log`/`git blame` archaeology, and directory walks. | 76% of parent `bash` calls (3,459) were search/git reads; batching 3–4 questions into one parallel `scout` workflow replaces 25+ serial frontier turns. |
| **Measuring** | **Specifying the metric and interpreting the table:** naming the exact population, time window, denominator, and caveats, plus optional 1-command spot-verification if a number looks inconsistent. | **Writing and iterating on measurement scripts:** querying `state.db` read-only, parsing `.log` JSONL streams or benchmark artifacts, reconciling schemas, and saving the reproducible script in durable scratch alongside summary tables. | Debugging SQLite column names and JSONL shapes cost 5–15 frontier turns per audit run; scouts iterate at $0.002/turn and hand back verified counts. |
| **Running checks** | **Narrow closeout checks on its own diff:** `uvx vantage-check <target.md>`, fast changed-doc gate checks (`documents.py`, `citations.py`, `design-citations.py`), and `git status` / `git add` / `git commit` / `land`. | **Heavy or repo-wide suites:** `go test ./...`, `pytest`, multi-directory `vantage-check` sweeps, and reproducing code/test failures during `conflict-design` triage. | Delegating single-file `vantage-check` recreates the rejected 2026-10-22 `verifier` loop; running `go test` suites in-session burned 484 frontier `bash` calls on `conflict-design`. |

### 3.2 Applying the split to `conflict-design` (60% of `design` spend)

In the UTC audit, **97 `conflict-design` runs spent $238.09** (62 runs / $185.11 in ET up to filing) — more than double all `scouted-design@v1` runs combined — with **zero** scout calls and **484 test/gate `bash` executions**. Under `D-RD5`, `conflict-design` dispatches receive the same Tier-1 `scout` subagents and follow a two-step triage:

1. Read the conflict brief, the item's governing design doc, and up to 4 directly named files in-session. If those settle the conflict (for example, a clear specification gap or fence omission), resolve immediately with zero subagent calls.
2. If diagnosis requires reproducing a test failure, checking callers across packages, or auditing sibling Decision Ledgers, launch **one parallel `scout` batch** to run the tests and searches, then write the resolution or design amendment in-session.

---

## 4. Enforcement versus instruction, specific limits, and avoiding delegation handcuffing

### 4.1 Why instruction alone failed — and why total read/shell lockout fails on the other side

The two failure modes sit at opposite extremes, and this repository has now observed both:

1. **Pure instruction yields <1% delegation.** Both [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) and `pi-subagents` already instructed designers to use scouts. When unrestricted `bash`, `grep`, `find`, and `read` sit in the same tool list with zero friction, frontier models default to calling `rg` and `read` directly (176 of 178 runs).
2. **Delegation handcuffing** *(coined here: restricting a high-judgment agent's direct read or check tools so tightly that it spends more turns, wall time, and tokens marshaling subagent briefs and playing twenty questions than doing a small task directly — distinct from healthy parallel fan-out on broad searches)*. Stripping `read` or blocking single-file verification commands forces a 45–90 second subagent round-trip to read a 120-line config file or run `uvx vantage-check` after fixing one broken anchor. Worse, scouts summarize: when a designer cannot `read` the 80 lines where a state machine's edge case lives, it fires 3–4 follow-up scout rounds asking narrow clarifying questions, spending more frontier output tokens writing prompts than reading the file once would have cost.

### 4.2 Comparing the enforcement postures ([OQ-RD1](#OQ-RD1), [OQ-RD2](#OQ-RD2))

First, **session topology ([OQ-RD1](#OQ-RD1)):** retiring [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json)'s separate read-only `open` node so design items start directly in one warm writing `design` session with in-session parallel `scout` subagents eliminates the 89% wasted `open` run and matches `paired-design`. (If `open` is kept for any workflow, `D-RD4` fixes [`loop.go`](../../tools/tillr-drain/drain/loop.go) so `open`'s `PreviousReport` is always forwarded when no `graph.proposal` is written.)

Second, **tool and budget enforcement on the `design` seat ([OQ-RD2](#OQ-RD2)):** across all options, `D-RD1` pins `allowedAgents` on `design` to read-only `["scout", "researcher", "evidence-auditor"]` with `scout` defaulted and model-scoped to Tier 1 (`gemini-3.8-flash` on Copilot, `gpt-6-luna` on Bedrock, `claude-haiku-5-5` on Claude subscription), and `D-RD2` loads `design-doc`, `vantage-docs`, and `tillr-designs`. What differs is how tightly parent tools are bounded:

| Posture | Parent tools on `design` | Specific mechanical limits | Risk of self-crawling | Risk of delegation handcuffing |
| :--- | :--- | :--- | :--- | :--- |
| **Option A — Guarded check-and-commit wrapper, no raw `bash`** | `read`, `write`, `edit`, `subagent`, `subagent_supervisor`, `land`, `obstacle`, plus a narrow `design_check` tool (`uvx vantage-check`, doc gate checks, `git status/diff/add/commit`). **No `bash`, `grep`, `find`, `ls`, `codemode`, `background_task`.** | **0** arbitrary shell searches/scripts; **soft warning at 15 direct `read` calls** without a `subagent` call (hard cap **30 `read` calls** unless refreshed after a `subagent` batch). | **Lowest:** impossible to run `rg`, `go test`, or `sqlite3` in the parent session. | **Moderate on small/conflict runs:** even a 2-command `git log` or 1-pattern `rg` check requires a 45 s `scout` spawn. |
| **Option B — Strip search/script tools + 10-call parent shell search budget** *(recommended leaning)* | `read`, `write`, `edit`, `bash`, `subagent`, `subagent_supervisor`, `land`, `obstacle`. **Strips `grep`, `find`, `ls`, `codemode`, `background_task`.** | Extension guard in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) classifies parent `bash` calls: `uvx vantage-check`, doc gate checks, and `git status/diff/add/commit` are **uncapped**; all other `bash` calls (search, `git log/show`, tests, `python3`/`sqlite3`) warn at **6 calls** and **refuse after 10 calls** with a message directing the designer to batch a `scout` subagent call. Direct `read` warns at **15 calls** before any scout (cap **30**; +15 per completed `scout` batch). | **Low:** caps exploratory shell spend at <=10 turns while eliminating `codemode` and dedicated grep tools. | **Very low:** 2-file edits and quick `conflict-design` checks finish in 1–4 commands with zero subagent round-trip, while 40-command shell crawls hit a clean mechanical wall at call 11. |
| **Option C — Instructions + Tier-1 scout defaults only** | Unchanged ambient tools, plus Tier-1 `scout` defaults (`D-RD1`) and prompt table from [Section 3.1](#31-the-five-activities-split-cleanly). | **No mechanical tool removal or call cap;** relies on prompt guidance and post-run observability (`D-RD3`). | **High:** models under deadline pressure routinely revert to local `bash` when unguarded. | **Zero.** |

### 4.3 Edge cases, invariants, and observable completion

- **Degenerate inputs (tiny items and zero-unknown conflicts).** An item naming 1–3 files with no empirical audit owed completes with **0 `scout` calls**, direct `read` + `edit`/`write`, `uvx vantage-check`, and `land`. Spawning scouts when nothing is unknown is explicitly discouraged in the brief.
- **Scout failure or timeout.** Each `subagent` call on `design` carries a **300 s timeout** (`timeoutMs: 300000`). If a Tier-1 scout backend errors, hits rate limits, or returns empty output after one retry on the profile's `scout` fallback chain, the guard in Option A/B unlocks parent `read`/`bash` fallback for that run and records `scout_fallback: true` on the run receipt so the designer never deadlocks.
- **Concurrency and one-writer rule.** All `scout`, `researcher`, and `evidence-auditor` children on `design` run strictly **read-only** (`worktree: false`, write/edit tools absent); the parent `design` session is the **sole writer** of the lane worktree and durable progress file. Parallel scouts launch in a single `{ workflow: "parallel", args: { tasks: [...] } }` call (default cap: **4 concurrent children per batch**, matching `pi-subagents` defaults).
- **Existing sessions and runs in flight.** Tool flags and extension guards apply at process spawn in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) and [`run-claude`](../../tools/tillr-run/backends/claude/run-claude); live runs finish under the config they launched with, and resumed [designer desk](../reference/tillr-designer-sessions.md#the-desk) sessions pick up the updated tool surface on their next turn without invalidating session JSONL history.
- **Forbidden behavior.** A `design` run must never spawn a writing subagent (`worker`, `delegate`), never spawn a Tier-3 child (`designer`, `oracle`), never drop child token/cost totals from `run.record.json`, and never execute `just done` inside a lane.
- **What done looks like (`D-RD3`).** `run.record.json` and `tillr drain why` display both parent and child token/USD totals (`parent_cost_usd`, `child_cost_usd`, `child_runs`), plus parent `read` and non-check `bash` counts, making both under-delegation (>10 search `bash` calls) and over-delegation (>3 sequential scout batches on one run) immediately visible.

---

## 5. How this fits the plan-step and seat-selection design, and the rule for the `plan` seat

### 5.1 Alignment with `item-request-05ac7dd8a0`

The concurrent sibling request `item-request-05ac7dd8a0` (*"Decide what the plan step is for, and who picks an item's seat"*) audits [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) over the same two days and reaches the exact structural twin of this design:

1. **Same two-session cold-handoff bug:** In [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json), `plan` proposed no split 82% of the time and `(*Workflow).hasNamedFiles()` discarded its report before `work`; in [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json), `open` proposed no scouts 89% of the time and the exact same `hasNamedFiles()` check discarded its report before `write`. Retiring upfront `open` ([OQ-RD1](#OQ-RD1) Option A) mirrors retiring upfront `plan`: one warm parent session starts immediately and fans out via in-session harness subagents only when the live code warrants it, while `D-RD4` repairs `PreviousReport` for any multi-stage workflow that remains.
2. **One shared subagent tier compiler:** Both `build` fan-out and `design` scouting use one staging-time child-policy file compiled by `tillr-drain` from the active [drain seat profile](./tillr-seat-profiles.md) (`config/tillr/profiles/<name>.json` or `config/tillr/backend-policy*.json`) and enforced by [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) (`subagents.modelScope` + `allowedAgents`) and [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) (`--agents`). No model names are hardcoded in adapters.
3. **Escalating misfiled requests increases `conflict-design` traffic:** Allowing blockless CLI requests (`tillr item request`) to escalate from `build` to `design` on `definition`, `fence`, or exhausted `seat` obstacles replaces 34-run build loops with `conflict-design` runs — making `D-RD5` (Tier-1 scout triage on `conflict-design`) essential so newly routed escalations do not run `go test` suites on the frontier `design` model.

### 5.2 Answering the same question for the `plan` seat ([OQ-RD3](#OQ-RD3))

Across 2026-11-18 and 2026-11-19, `plan` spent **$184.75** in UTC ($199.54 in ET up to filing) across **12,545 parent tool calls** (`read`: 7,829, `bash`: 2,906, `codemode`: 1,782 — **151 tool calls per logged run**) and **0 `subagent` calls**. What a planner should read itself versus send to scouts depends directly on **which model tier runs the planning work** once `item-request-05ac7dd8a0` is ruled:

| Planning context | Model tier | What it does itself | What goes to Tier-1 `scout` subagents | Enforcement rule |
| :--- | :--- | :--- | :--- | :--- |
| **1. Mid-tier companion-plan writer (`docs/design/*-plan.md`) or builder first-turn orientation** *(the primary path if upfront `plan` is removed)* | **Tier 2** (`gemini-4-hydrogen` / `gpt-6.1-terra` / `claude-sonnet-5-5`) | **Reads target packages directly (`read`, targeted `rg`), verifies symbols/traps, runs targeted package tests, and writes `-plan.md` or code.** | **Parallel 3+-package surveys only:** repo-wide caller censuses or cross-module test inventories when an accepted design spans 3 or more packages. | **Instructions + Tier-1/Tier-2 `modelScope` ceiling (`D-RD1`); no parent `read`/`bash` cap.** On Tier 2, the price gap to Tier 1 is narrow (~2–3x vs 15–30x on Tier 3) and exact line-level code mapping *is* the deliverable; capping `read` or `bash` here would cause delegation handcuffing for negligible savings. |
| **2. Retained or opt-in upfront `plan` node in [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json)** *(if kept as explicit opt-in or by owner ruling)* | **Tier 3** (`gemini-4-carbon` / `gpt-6.2-sol`) | **Reads the item, governing design doc, and <=10 module boundary files; chooses piece seams, seat tags, and `allowed_paths`; writes `graph.proposal` or handoff report.** | **All broad file discovery, caller enumeration, and test-surface mapping** via 1 parallel Tier-1 `scout` batch (`allowedAgents: ["scout"]`). | **Same mechanical bounds as `design` ([OQ-RD2](#OQ-RD2)):** strip `codemode` (which burned 1,782 calls across 83 runs), `grep`, `find`, and `ls`; cap parent search `bash` at **10 calls**; pin `scout` to Tier 1. |

Whether `plan` follows this tier-matched split (Option A), enforces scout caps even on Tier-2 companion planners (Option B), or leaves planning tools unrestricted (Option C) is put to the owner in [OQ-RD3](#OQ-RD3).

---

## 6. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Lock the designer out of `write`, `edit`, and `read`, delegating prose to a writer agent and checks to a verifier agent** | **Rejected.** Overturns the owner's 2026-10-22 ruling: delegating prose and verification causes heavy turn churn and loss of design coherence. |
| **Keep [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json)'s `open` node and force `open` to always spawn 2–4 workflow `scout` nodes** | **Rejected.** Adds ~120 s of pass-handoff latency per stage, forces pre-read decomposition before the designer knows what questions matter, and still leaves `write/join` cold when follow-up questions arise mid-draft (as seen on `8008c05e5c`'s 139 follow-up calls). |
| **Allow `design` to spawn Tier-2 `worker`/`delegate` writers or nested scouts-of-scouts** | **Rejected.** Violates single-writer ownership in the lane worktree and blurs design with implementation; `pi-subagents` already caps child depth, and `design` outputs are Markdown decisions rather than multi-package code edits. |
| **Hard-cap total `design` turns instead of bounding exploratory search tools** | **Rejected.** Violates the owner's standing rule against arbitrary retry/turn kill counts ([the graph design Section 7](./tillr-graph-is-the-path.md#7-what-stops-repetition)) and would kill legitimate long design documents mid-write rather than steering search work to cheap scouts. |

---

## 7. What this does not cover

- **Changing which frontier or cheap model each profile assigns to `design`, `plan`, or `scout`** — governed by [named drain seat profiles](./tillr-seat-profiles.md) and `config/tillr/backend-policy*.json`.
- **Post-commit style compliance or substantive review of design documents** — governed by [the compliance officer](./tillr-design-document-check.md) and [diff review before landing](./tillr-diff-review.md).
- **Retiring or keeping [`planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json) itself, or changing `tillr item request` flags** — owned by sibling request `item-request-05ac7dd8a0`; [Section 5](#5-how-this-fits-the-plan-step-and-seat-selection-design-and-the-rule-for-the-plan-seat) specifies how scout and tool rules plug into whichever option the owner chooses there.
- **Interactive `/design` sessions outside `tillr-drain`** — interactive paired design already runs inside one warm session under `paired-design`; `D-RD2` keeps the interactive and drain tool surfaces consistent.

---

## 8. Sequencing and amendments owed

Once the owner rules on [OQ-RD1](#OQ-RD1)–[OQ-RD3](#OQ-RD3) and reviews `D-RD1`–`D-RD5`, implementation proceeds in four steps (remembering to run `just pack-sync` whenever `tools/tillr*` changes):

1. **Roll up child-session telemetry and persist child receipts (`D-RD3`).** Update [`extract-report`](../../tools/tillr-run/backends/pi/extract-report) and [`progress.go`](../../tools/tillr-run/run/progress.go) to record per-child model, tokens, tool calls, and estimated USD into `run.record.json` and shared loop artifacts so every subsequent change is measured accurately.
2. **Compile and enforce run-scoped subagent tiers plus authoring skills in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) and [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) (`D-RD1`, `D-RD2`).** Stage child-role model mappings from the active drain seat policy (`scout` -> Tier 1, read-only `allowedAgents` on `design`) and mount `design-doc`, `vantage-docs`, and `tillr-designs` on `design` runs.
3. **Update workflow topology and `PreviousReport` handoff ([OQ-RD1](#OQ-RD1), `D-RD4`, `D-RD5`).** Collapse [`scouted-design@v1.json`](../../config/tillr/workflows/scouted-design@v1.json) to a single warm `design` stage with in-session scout briefing (if [OQ-RD1](#OQ-RD1) takes Option A), fix `(*Workflow).hasNamedFiles()` in [`node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go) / [`loop.go`](../../tools/tillr-drain/drain/loop.go) (`D-RD4`), and add the `conflict-design` scout brief (`D-RD5`).
4. **Enable the chosen tool surface and shell-search guard on `design` and Tier-3 `plan` ([OQ-RD2](#OQ-RD2), [OQ-RD3](#OQ-RD3)).** Apply the selected tool flags and extension budget in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) (with automatic fallback unlock if Tier-1 scouts fail).

**Amendments owed to other files when built:**

- **[Planning and model tiering](./tillr-planning-node-model-tiering.md):** link [OQ-PT8](./tillr-planning-node-model-tiering.md#OQ-PT8) and [Section 5](./tillr-planning-node-model-tiering.md#5-the-planning-stage-as-built) to [Section 5.2](#52-answering-the-same-question-for-the-plan-seat-oq-rd3) once [OQ-RD3](#OQ-RD3) is ruled.
- **`docs/tillr/roadmap.md` and `docs/tillr/design-status.md`:** route this document and [OQ-RD1](#OQ-RD1)–[OQ-RD3](#OQ-RD3) once an item fenced to `docs/tillr/` updates the roadmap index.

---

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| Designers hit the 10-call shell-search cap mid-investigation and stall or write thin designs | The refusal message gives the exact parallel `subagent` syntax for `scout`; direct `read`, `vantage-check`, and `git commit` remain uncapped; and if the `scout` backend errors out, the guard unlocks fallback parent shell access automatically |
| Cheap Tier-1 scouts miss a subtle code invariant or misread a Go/Python control path | Scouts return exact file and symbol locators (`P4`), and the designer retains direct `read` to inspect every load-bearing function or Decision Ledger row itself before ruling |
| Designers over-delegate trivial 2-file edits, adding 60 s of scout startup overhead | [Section 3.1](#31-the-five-activities-split-cleanly) and [OQ-RD2](#OQ-RD2) Option B preserve a zero-scout fast path (up to 15 direct `read` calls and 10 shell calls) so small designs and simple conflicts finish in-session |
| Copilot or Bedrock rate-limits parallel `scout` bursts when multiple lanes fan out at once | Parallel scout batches stay capped at 4 children per call and inherit `tillr-drain`'s seat fallback chain (`Flash 3.8` -> `Luna 6` or `Hydrogen 4` read-only fallback) |
| Child subagent costs grow unnoticed while parent `runs.usage_cost_usd` looks smaller | Step 1 (`D-RD3`) ships child-cost rollup in `run.record.json` and `tillr drain why` *before* tool bounds turn on, so total parent + child spend is compared honestly |

---

## Decision Ledger

This design inherits the owner's 2026-10-22 rulings (the designer has direct Markdown `read`/`write`/`edit`, and delegation is for read-only scouts) and the rule that the Tier-1 scout model is read-only and never writes. Rows `D-RD1` through `D-RD5` below are engineering decisions marked as the designer's under [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger); the owner may overturn any of them, and nothing is built until he answers [OQ-RD1](#OQ-RD1), [OQ-RD2](#OQ-RD2), and [OQ-RD3](#OQ-RD3).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `D-RD1` | **Designer's decision; the owner may overturn this.** Compile run-scoped subagent settings from the active drain seat policy in `tillr-drain` and enforce them in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) (`subagents.agentOverrides`, `allowedAgents: ["scout", "researcher", "evidence-auditor"]`, and strict per-role `subagents.modelScope`) and [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) (`--agents`), pinning `scout` to the policy's Tier-1 `scout` model (`Flash 3.8` / `Luna 6` / `Haiku 5.5`) instead of leaking interactive `Hydrogen 4` defaults. | 2026-11-20, designer | [Section 4.2](#42-comparing-the-enforcement-postures-oq-rd1-oq-rd2), [Section 5.1](#51-alignment-with-item-request-05ac7dd8a0) | — |
| `D-RD2` | **Designer's decision; the owner may overturn this.** Expose `design-doc`, `vantage-docs`, and `tillr-designs` via explicit `--skill` flags on `design`-seat runs in [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) instead of hiding all skills behind blanket `--no-skills`, so drain designers author to the exact rulebooks enforced by [the compliance officer](./tillr-design-document-check.md). | 2026-11-20, designer | [Section 2](#2-todays-drain-workflow-compared-with-paired-design), [Section 4.2](#42-comparing-the-enforcement-postures-oq-rd1-oq-rd2) | — |
| `D-RD3` | **Designer's decision; the owner may overturn this.** Roll child-session token counts, model IDs, tool-call counts, and estimated USD from `TILLR_CHILD_SESSIONS_ROOT` and `pi-subagents` receipts into [`extract-report`](../../tools/tillr-run/backends/pi/extract-report), [`progress.go`](../../tools/tillr-run/run/progress.go), `run.record.json`, and shared `/workspace/.tillr/loop/<run_id>/` summaries so subagent spend is visible in `state.db` and survives container rotation. | 2026-11-20, designer | [Section 1.4](#14-what-could-not-be-measured), [Section 4.3](#43-edge-cases-invariants-and-observable-completion) | — |
| `D-RD4` | **Designer's decision; the owner may overturn this.** In [`node_outputs.go`](../../tools/tillr-drain/drain/node_outputs.go) and [`loop.go`](../../tools/tillr-drain/drain/loop.go), forward `PreviousReport` to any stage with no wired `Inputs` whenever its predecessor produced no output artifact, fixing the cold handoff where optional `graph.proposal` stages drop their analysis when no split is proposed. | 2026-11-20, designer | [Section 1.3](#13-how-often-scouts-ran-and-what-each-unit-cost), [Section 5.1](#51-alignment-with-item-request-05ac7dd8a0) | — |
| `D-RD5` | **Designer's decision; the owner may overturn this.** Apply the same Tier-1 `scout` subagent policy and two-step triage brief to `conflict-design` runs (read named conflict/design files directly; batch multi-package search and `go test`/`pytest` reproduction into one parallel `scout` call), addressing the 60% majority of `design` spend ($238.09 of $394.56 in UTC). | 2026-11-20, designer | [Section 3.2](#32-applying-the-split-to-conflict-design-60-of-design-spend) | — |

---

## Open Questions

1. 💬 **OQ-RD1: Should a design item start directly in one writing designer session instead of running a separate scout-planning session first?**

   Today every design workflow item wakes a separate read-only frontier session first to decide whether to schedule scout runs before the writer starts. Across nine such runs, eight scheduled no scouts, lost their reading report at handoff, and paid two cold frontier sessions; the one item that scheduled four scout runs still made 139 file and shell calls while writing.

   - **A — One warm writing session that calls cheap scouts mid-session.** Starts directly in the writer, fans out parallel scouts when real gaps appear, and pays one frontier session.
   - **B — Keep the separate upfront session and fix report handoff.** Keeps graph-scheduled scout runs before writing, still paying two cold frontier sessions per item.

   <!-- vantage: question id=OQ-RD1 leaning="A — interactive paired design works because one warm designer session holds the whole argument and dispatches scouts when real gaps appear, whereas the upfront session wasted 8 of 9 runs." -->

   _Leaning:_ A — interactive paired design works because one warm designer session holds the whole argument and dispatches scouts when real gaps appear, whereas the upfront session wasted 8 of 9 runs.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-RD2: How strictly should tool limits push a designer to use cheap scouts instead of searching and scripting itself?**

   Across 178 design runs ($395), designers had unrestricted shell and search tools: only one run called subagent scouts while designers ran 4,548 shell commands and 3,787 file reads (~$0.07 per frontier turn vs ~$0.03 for a whole cheap scout run). Yet blocking file reads or quick checks altogether traps small tasks in slow delegation loops.

   - **A — Direct file reading and editing, plus narrow check-and-commit commands only.** Every search, test suite, and measurement script must spawn a scout.
   - **B — Keep shell access, strip dedicated search/script tools, and cap shell searches at 10.** Refuses parent shell searches or scripts past 10 calls (leaving document checks and commits uncapped) so two-file checks need no scout.
   - **C — Instructions and cheap scout defaults only, with no tool caps.** Relies on model judgment alone.

   <!-- vantage: question id=OQ-RD2 leaning="B — stripping search and script tools plus a 10-call shell search cap stops 40-turn frontier shell crawls mechanically without forcing a 60-second scout round-trip on a two-file question." -->

   _Leaning:_ B — stripping search and script tools plus a 10-call shell search cap stops 40-turn frontier shell crawls mechanically without forcing a 60-second scout round-trip on a two-file question.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-RD3: What reading and scout rules should apply to planning work once the upfront planning step's fate is settled?**

   Across 99 planning runs ($185), planners made 12,545 file, shell, and script calls on the frontier judgement model with zero scout calls. A sibling design proposes retiring that upfront step so mid-tier builders orient directly (and mid-tier planners write companion task plans after a design is accepted), or keeping it only as an opt-in.

   - **A — Match tool rules to the model tier.** Mid-tier planners and builders read code and run targeted tests directly, calling cheap scouts only for 3+-package parallel surveys; any retained frontier planner gets the 10-call search cap and mandatory scouts.
   - **B — Enforce search caps and mandatory scouts on mid-tier planners too.** Trades extra delegation turns for smaller contexts.
   - **C — Leave all planning runs unrestricted.** Keeps full search and script tools even on frontier planning runs.

   <!-- vantage: question id=OQ-RD3 leaning="A — delegation pays when the parent-to-scout price gap is steep: frontier planners crawling code waste dollars per run, whereas handcuffing a mid-tier planner whose whole job is mapping code files adds churn for pennies." -->

   _Leaning:_ A — delegation pays when the parent-to-scout price gap is steep: frontier planners crawling code waste dollars per run, whereas handcuffing a mid-tier planner whose whole job is mapping code files adds churn for pennies.

   **Answer:**

   > _(empty — fill in when decided)_
