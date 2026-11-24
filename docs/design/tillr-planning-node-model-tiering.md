---
title: "Plan first, then spend each model where it is enough"
status: in-review
stage: DESIGN
updated: 2026-11-20
tags:
  - tillr
  - graphs
  - routing
  - cost
  - models
  - design
summary: "A build item can be planned first: a planning node cuts it into pieces, each piece names a seat, the backend policy puts each seat on a model, and a verify node checks the joined result against the item's criteria before the gate. The owner ruled on 2026-11-18 that implementation runs on Terra and scouting on Luna, and the planning stage was built that day. Filed build requests default to planned-build@v1 (`5a5c459de`), and one planned build item has landed on the Copilot policy; document-declared build items still name none, so the ten-item planned median OQ-PT8 waits on is still unmeasured, while the run history of 2026-11-17 says the larger savings are in failed runs and in context written to the cache twice."
next: "Run nine more build items through planned-build@v1 (choosing document-declared build items on the roadmap or running queued build requests) so that the cost report prints the ten-item planned median OQ-PT8 waits on alongside the last-20 build median."
depends-on:
  - ../reference/tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
---

# Plan first, then spend each model where it is enough

**Status:** 2026-11-19. Partly built: the plan seat, the ten seats in every backend policy, the piece list, the `planned-build@v1` workflow, its verify node, the seat list in the worker's brief and the cost report all landed on 2026-11-18 ([Section 5](#5-the-planning-stage-as-built)). No document-declared item selects the workflow and `.tillr.toml` sets no default workflow, while `tillr item request` defaults `build` filings to `planned-build@v1` (`5a5c459de`): one planned request has landed (without a piece split) and one queued request has split into pieces, on the Copilot policy rather than Bedrock ([Section 5.7](#57-what-nothing-selects-and-what-is-not-built)). The size rule and per-piece command checks of [Section 6](#6-what-planning-costs-and-what-would-choose-it) are not built. The [Decision Ledger](#decision-ledger) holds designer's decisions the owner has not reviewed, each labeled as one; the stage stays open until he has. The measurements of [Section 1](#1-what-the-run-history-says) are dated 2026-11-17 and were read against the tree at `424cc9db0`.

> **In short.** What an item costs depends more on how its failures are caught than on which model runs it. So a large item is planned first and cut into pieces, each piece goes to the cheapest model that is enough for its kind of work, and one stronger model checks the joined result before the gate. Only one build request has landed through the workflow (without a piece split), so whether planning pays across ten planned builds is a measurement still to take.

**Why it matters.** In the history read on 2026-11-17, 41% of spend went to runs that landed, and the median landed item ran three times (measured). The owner asked on 2026-11-18 for work to be split by kind across seats and for cheaper models wherever they can do the work.

**The shape.** Three nodes inside one item: a planner that proposes pieces, the pieces on the seats they name, and a verify node. Seats are names the backend policy maps to models, so a provider change is one policy edit.

**Cost.** Every planned item pays for a planner and a verify node on the mid model before any saving. That cost is unmeasured: the estimate of 2026-11-17 was about $0.35 for the plan alone, at prices and effort that have since changed.

**Start at** [Section 5](#5-the-planning-stage-as-built), what is built and what nothing selects yet.

**Needs your ruling:** None today. Two questions remain blocked until planned items with splits have landed: [OQ-PT7](#OQ-PT7) (until a planned item with a split has landed and storage's [`OQ-TS1`](./tillr-storage.md#OQ-TS1) is answered) and [OQ-PT8](#OQ-PT8) (until ten planned items have landed).

**Reads with:**

- [`tillr-item-graphs.md`](../reference/tillr-item-graphs.md): workflows, nodes, proposals and fences.
- [`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md): what a worker may end with, which is how a planner reports a wrong criterion.
- [`tillr-graph-is-the-path.md`](./tillr-graph-is-the-path.md): the rule against counts, and seats declared in the policy.
- `seat-profiles.md`: the October rulings on Bedrock models.
- [`model-benchmarks-from-drain-records.md`](./model-benchmarks-from-drain-records.md): replaying recorded tasks, the other way to compare models.
- [The roadmap section that filed the planning stage](../tillr/roadmap.md#filed-2026-11-18-plan-first-then-spend-each-model-where-it-is-enough).

No implementation plan exists, and none is owed until [OQ-PT8](#OQ-PT8) can be asked.

---

## 1. What the run history says

Every figure in this section is **measured**, and dated 2026-11-17, when every build ran as one session on Sol 6.1 and every design ruling as one session on Fable 5.2. The build seat has since moved to Terra 6.1 ([Section 4](#4-kinds-of-work)), so these are the costs planning was designed against, not today's. The two sources:

- `tillr view events --since 0` at event 4343, generated 2026-11-17 03:33 UTC;
- the 334 pi run logs in the lanes, read the same day, with each turn priced by pi's own usage record.

Wall times cap each wait between turns at 20 minutes and set the wait after a final turn to zero. Without that, a session that ended while saying "I'll commit after the tests" counts days of idle time.

### 1.1 Where the money went

The store records 338 runs and $455.56. That total double-counts, for two reasons:

- **Duplicated usage.** 49 runs carry exactly another run's tokens and cost, $133.65 in all. Most are killed runs of one or two seconds. For example, 21 runs of `item-land-retires-block` each record $3.134482 and 16,061,350 tokens. The cause was a report file shared between attempts on one lane, fixed by commit `263173aed` on 2026-11-15. All 49 started before that commit, and none of the 285 runs started after it repeats another run's usage (measured at event 5494, 2026-11-17).
- **Missing usage.** 83 runs record no cost, and the logs of 79 of them price to $89.11.

With duplicates removed and the missing runs priced from their logs, the best total is about **$417**.

| Backend | Runs | Recorded | Note |
| :--- | ---: | ---: | :--- |
| `pi`: Sol 6.1, medium | 276 | $335.06 | 71 record no cost; 118 ended by a signal |
| `pi-designer`: Fable 5.2, medium | 39 | $102.03 | Logs price to $132.21 |
| `claude-designer` | 19 | $18.47 | API-equivalent; 13 exited on the weekly limit |

Outcomes:

- 95 runs landed. There were 139 landing events in all: 97 landed and 42 failed on a squash conflict.
- Refusals: 139 no-op, 15 conflict, 11 gate, 4 fence.
- **$171.98 of $416.93 (41%) was spent on runs that landed.**
- Of 92 items, 72 have a landing and only 21 landed on their first run. The median landed item took three runs, and each landed item cost $5.79.

Spend is concentrated in large items. Items whose total is over $3 hold 87% of spend:

| Item total | Items | Share of spend |
| :--- | ---: | ---: |
| Under $1 | 20 | 2% |
| $1–3 | 26 | 11% |
| $3–6 | 25 | 25% |
| $6–15 | 18 | 44% |
| $15 and over | 3 | 18% |

**Sol runs, by what they did:**

| Outcome | Runs | Cost |
| :--- | ---: | ---: |
| Edited, committed, exited normally | 67 | $120.85 |
| **Edited, did not commit, exited normally** | **39** | **$50.47** |
| **Edited, then killed by a signal** | **34** | **$47.45** |
| Never edited, exited normally | 30 | $5.90 |
| Never edited, then killed | 28 | $3.43 |

A run that never edits is a scout by accident and costs about $0.20. **39 of the 140 Sol runs that edited (28%) ended with their edits uncommitted**: the defect that [ending with passing work uncommitted](../tillr/roadmap.md#prevent-a-build-run-from-ending-with-passing-work-uncommitted) fixed.

### 1.2 Where a Sol run's money goes

The sample is 227 Sol logs: $266.25 by pi's prices, 11,427 turns and 23.9 hours of wall time. A classifier assigned each turn to a phase *(the phase names are coined here)*:

- **orient**: reading before the first edit;
- **edit**: changing files;
- **diagnose**: reading after the first edit;
- **test**: running tests and reading their output;
- **review**, **commit** and **final**: the turns their names say;
- **subagent**: a turn that starts a child session.

| Phase | Cost share | Wall share |
| :--- | ---: | ---: |
| Edit | 28.2% | 32.2% |
| Diagnose | 26.0% | 17.0% |
| Test | 21.8% | 33.3%: 221 min of model time and 257 min of tool wait, a lower bound |
| Orient | 12.7% | 5.9% |
| Other | 4.3% | 3.7% |
| Review | 2.0% | 1.9% |
| Final | 1.8% | 3.4% |
| Commit | 1.8% | 2.0% |
| Subagent | 1.3% | 0.7% |

**The Sol token mix.**

| Token kind | Tokens | Share of cost |
| :--- | ---: | ---: |
| Cache write | 50.4M | 47.3% |
| Cache read | 1,040M | 39.1% |
| Output | 3.6M | 13.6% |
| Uncached input | about 0 | about 0 |

The mean context per turn is about 91k tokens, and a turn costs $0.0233 on average.

**Context size.** The largest context per run has a median of 97k and a p90 of 175k. The highest was 266.5k, against a 272k window. Large items run near the window: two runs of `item-comments-first-delivery` each reached 255k.

### 1.3 Orientation: the price of starting a new node

Orientation is the reading a run does before its first edit *(coined here as a measured phase)*. It is the cost a split pays again for every node it adds.

| | Sol, 163 runs that edited | Fable, 39 runs |
| :--- | :--- | :--- |
| Orientation, median | **$0.219** (p25 $0.179, p75 $0.271, p90 $0.314) | **$2.12** |
| Orientation, size | 485k tokens (p90 878k), 11 turns, 40 s (p90 71 s) | 66% of the run |
| Share of the run | 19.5% at the median, 15.3% in aggregate | Orient 45%, diagnose 23%, edit 17%, test 11% |
| First turn, median | **$0.063** at 25k context | **$0.807** at 40k context |

The Fable first turn works out to about $20 per million tokens. That is the one-hour cache-write price: the matt pack sets `PI_CACHE_RETENTION=long` for every pi session (commit `c5f4a82f7`), and pi charges an Anthropic one-hour write at twice the input price. By the price ratio (estimated), a Terra first turn costs about $0.03 and a Luna first turn about $0.003.

### 1.4 The four models' prices

These are dollars per million tokens, from `tag-work/config/yolo-jail/config.d/work.toml`:

| Model | Input | Output | Cache read | Cache write | Window | Score in the repository |
| :--- | ---: | ---: | ---: | ---: | ---: | :--- |
| Fable 5.2 | 10 | 50 | 0.25 | 12.5 (one-hour: 20) | 1M | Fable 5.1 as a stand-in: arch 84.0, impl 81.9, scout 90.7 |
| Sol 6.1 | 2 | 10 | 0.10 | 2.5 | 272k | arch 85.3, impl 74.3, scout 84.7, leaf 95.8 |
| Terra 6.1 | 1 | 5 | 0.05 | 1.25 | 272k | **none** |
| Luna 6 | 0.1 | 0.5 | 0.01 | 0.125 | 272k | impl 54.8 "single-turn infilling only", scout 60.1 "shallow search only", leaf 81.2 |

The scores come from `seat-profiles.md`. Sol 6.2, which the work policy uses since 2026-11-18, is priced the same as Sol 6.1 in that file.

Repricing the same tokens shows what a model change alone is worth (measured tokens, priced from the table):

- The Sol logs' tokens cost $266.25 on Sol, **$133.12 on Terra** (exactly half), **$18.51 on Luna** (7%) and $1,071 on Fable.
- The Fable logs' tokens cost $99.36 at the table price, and $22.46 at Sol's prices, a ratio of 0.226.

All of this assumes a cheaper model needs the same turns. Nothing in this repository measures that.

### 1.5 What the history cannot say

| Unmeasured | Why | Consequence |
| :--- | :--- | :--- |
| Terra's and Luna's failure rate and turn count on this repository's work | No run had used either when this was measured on 2026-11-17. Both have run since ([Section 7](#7-how-the-owner-learns-whether-it-pays)), without a comparison against Sol on equivalent work | Every saving from a cheaper model is still an estimate |
| What child sessions spend | A run's recorded cost leaves it out: one log sums to its recorded $7.61 and its sub-agent results carry no usage | Spend is understated by an unknown amount |
| The exact test wait | Background-task waits are capped and partly hidden | The 33% test wall share is a lower bound |
| Why runs write their context to the cache twice | Inferred from totals here; investigated since in the cache rewrites finding | [Section 1.6](#16-what-the-history-means-three-levers-ranked), lever 2 |
| `claude-designer` runs | Their logs have a different format, and the classifier parsed zero turns | They are left out of the phase figures |

### 1.6 What the history means: three levers, ranked

1. **Failed and repeated runs are the biggest lever.**
   - 59% of spend did not land.
   - Edited-and-uncommitted plus edited-and-killed runs are 43% of what the Sol runs above cost.
   - The median landed item ran three times.
   - A lever that makes each failure cheaper, or catches it earlier, beats any price ratio.
2. **Context written to the cache more than once is the second lever.**
   - Sol logs wrote 50.4M tokens to the cache, but the largest contexts of those runs sum to 22.8M. So 27.6M tokens, about **$69 (26% of Sol spend)**, were written more than once (measured totals; the cause is not diagnosed).
   - It is not idle expiry: turns that started five or more minutes after the previous turn carry under 2% of the writes (measured).
   - In the $40.07 Fable run of `item-design-gate-rework`, 1.695M tokens were written to the cache against a largest context of 256k. At the one-hour rate those writes cost $33.90, 85% of the run. Its cache reads (6.1M tokens) cost $1.53, and its output (92.7k tokens) cost $4.64.
3. **Routing to cheaper models is the third lever.**
   - It halves whatever moves to Terra, provided Terra does not fail more often or take more turns.
   - It is real but conditional, and it is the only lever that needs a planning node: a session cannot change models without losing its cache ([Section 9](#9-alternatives-considered)).

The design follows from this order. A planning node is worth having mainly because it makes failures cheap and early (lever 1) and keeps contexts small (part of lever 2). Giving pieces to cheaper models (lever 3) rides on the same structure.

---

## 2. Principles

- **P1. Each kind of work goes to the cheapest model that is enough for it.** This is the owner's rule, and the planner reads it in its brief from [`plan-guidance.md`](../../config/tillr/plan-guidance.md): "Use cheap models for well-specified implementation and mechanical work", "Have a mid model verify the work", "Keep the expensive model for root cause, design and risky core changes." (Under `D-PT8` in [Section 5.4](#54-when-the-planner-cannot-plan), the third paragraph of [`plan-guidance.md`](../../config/tillr/plan-guidance.md) — *"Send `pack-sync` to an `edit` piece"* — is overruled for `build-pieces@v1` agent pieces: `fencePaths` automatically adds `yolo-packs/tillr/` to every `tools/tillr*` writer's `write_scope` and [`brief-template.md`](../../tools/tillr-run/backends/brief-template.md) requires each `tools/tillr*` writer to run `just pack-sync` in its own commit, so `plan` must never emit a standalone `pack-sync` writing piece.)
- **P2. Kinds of work name seats, and models live only in the policy.** An item, a workflow and a plan name seats, never a model, so a provider change is one policy edit ([D-CB3](./tillr-claude-backend.md), and the owner's habit of switching providers often).
- **P3. One item outside.** Planning changes only the inside of an item. The queue, the gate and the landing still see one item ([`D-IG2`](../reference/tillr-item-graphs.md#why-its-this-way)).
- **P4. A split pays for its seams.** Each extra node pays a first turn and a partial re-orientation ([Section 1.3](#13-orientation-the-price-of-starting-a-new-node)). A split is made only where it pays more than that.
- **P5. Failures come first.** Failed and repeated runs are the largest lever in the history ([Section 1.6](#16-what-the-history-means-three-levers-ranked)), so planning is judged first by whether a stop loses one node instead of a whole run.
- **P6. A planner never edits what it is held to.** It names criteria by id. It never rewrites a criterion's text; when a criterion is wrong it says so and the design seat acts ([Section 5.4](#54-when-the-planner-cannot-plan)).
- **P7. Nothing counts attempts.** No node has a retry budget, no item has a number of plans it may use up, and no time limit fails work ([rule R1 of the graph design](./tillr-graph-is-the-path.md#1-the-direction-and-the-rules)). A node that cannot do its piece names who acts next.

---

## 3. The words

[Item graphs](../reference/tillr-item-graphs.md#the-words) defines workflow, stage, graph, node, node run and child session, and this document uses them unchanged.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Seat** | The name of a role a node asks for, such as `build` or `verify`. The backend policy in force maps each seat to a backend, and a backend names its harness, model and effort. Any well-formed name the policy maps is a seat | Not a model, and not a fixed list in code |
| **Read-only seat** | A seat the policy lists as one whose nodes may not write. A workflow that gives such a seat a writing node is refused when it loads | Not a promise about the model, only about what the node may do |
| **Piece** | One node's share of a planned item, as the planner proposes it: an id, a seat, whether it writes, a prompt, and the outputs it leaves. The word is the built workflow's | Not an item, and it has no place in the queue. Not a trunk commit: the item still lands as one |
| **Kind of work** *(coined here)* | What a node is for, named by what can tell whether it was done right: plan, design ruling, implement, settled edit, run a check, verify | Not a model and not a size. Each kind maps to one seat ([Section 4](#4-kinds-of-work)) |
| **Seam cost** *(coined here)* | What a new node pays before its first useful edit: its first turn and the orientation it repeats | Not the node's work |
| **Mid model** | The owner's guidance names three levels. On the work machine today they are Luna 6 and Terra 6.1 (cheap), Sol 6.2 (mid) and Fable 5.2 (expensive) | Not fixed: the policy file decides, and other machines' policies name other models |

---

## 4. Kinds of work

Each kind is defined by what the work needs and what can tell whether it is right. The seat is where the policy places it. The model column is the work machine's policy, [`backend-policy.bedrock.json`](../../config/tillr/backend-policy.bedrock.json), read on 2026-11-18; every backend row there states medium effort.

| Kind | What it needs | What tells whether it is right | Seat | Model today |
| :--- | :--- | :--- | :--- | :--- |
| **Plan** | Judgment over the whole item; reads the tree; writes the piece list | The loader's refusals, then the verify node | `plan` | Sol 6.2 |
| **Design ruling** | Judgment that no test can check; writes prose that changes what gets built | The owner, or a judge | `design` | Fable 5.2 |
| **Conflict repair** | A fix to an item's own definition: correct a criterion that contradicts the tree | The next build run and the documentation checks | `design` | Fable 5.2 |
| **Implement** | Code against criteria | The gate; in a planned item, the verify node first | `build` | Terra 6.1 |
| **Settled edit** | Apply an edit the plan already decided | The same | `edit` | Terra 6.1 |
| **Write tests** | Tests written from the criteria | The same | `test-write` | Terra 6.1 |
| **Docs** | READMEs and documents | `vantage-check` and the documentation tests, which check structure and not substance | `docs` | Terra 6.1 |
| **Run a check** | Run a named check and report its result without fixing it | Its output existing; it may not write | `test-run` | Luna 6 |
| **Scout** | Read and report; change nothing | The read-only rule and its output existing | `scout` | Luna 6 |
| **Verify** | Join the pieces and check the result against every criterion | Nothing checks it but the gate that follows | `verify` | Sol 6.2 |
| **Review** | An independent verdict on a candidate | Nothing: the reviewer is the check | `review` | Sol 6.2 |
| **Command** | No model | The exit code | none: a command node | none |

Four facts about that table:

- **Luna never writes.** The work policy puts it only on the two read-only seats, and the item that declared the seats made that one of its criteria. This keeps the October verdict that Luna is not a worker ([Section 12](#12-rulings-this-builds-on)).
- **A stronger model is reached by a diagnosis, never a count.** A build worker that has made and tested an attempt may report that the task exceeds its model; the item then moves to the next backend the policy lists for that seat, Terra then Sol 6.2 today.
- **A failed check has no seat of its own.** It goes back to the session of the worker that made the change.
- **Work that exists only for landing is never planned.** Conflict repair and the fixers for a broken trunk or a landing conflict run as one node; planning is for build items.

The model names and prices in this document apply to the work machine alone. The policy for personal machines maps every seat except `design` to one backend, so there is nothing to save there by splitting.

---

## 5. The planning stage as built

Seven items landed on 2026-11-18, cut from [the roadmap's planning block](../tillr/roadmap.md#add-a-planning-stage-that-fans-implementation-out-by-seat). This section states what they built; the drain's [README](../../tools/tillr-drain/README.md) is the reference.

### 5.1 The workflow

[`planned-build@v1`](../../config/tillr/workflows/planned-build@v1.json) is a tracked workflow of three stages:

```text
plan    (seat plan, read-only)     may propose replacing `work` with a list of pieces
work    (seat build, writes)       the single node every item runs today
verify  (seat verify, read-only)   checks the joined result against the item's criteria
```

- **An accepted proposal** replaces `work` with the pieces, one node each, through [`build-pieces@v1`](../../config/tillr/workflows/build-pieces@v1.json).
- **A planner that proposes nothing** leaves `work` as it is, and the item is built by one worker.
- **A refused proposal** is recorded with its reason and leaves the graph unchanged.
- **Planning can make an item cheaper, and it never stops one for want of a plan.**

### 5.2 What a piece is, and what refuses one

A proposal holds at most 12 pieces. Each has exactly an id, a seat, an access (`read_only` or `write`), a prompt and optional named outputs. A writing piece needs no list of allowed paths.

| The proposal | Recorded as |
| :--- | :--- |
| names a seat the policy in force does not map | `proposal-seat-unmapped` |
| gives a read-only seat a writing piece | `proposal-read-only-seat-writes` |

Writers run one at a time, in the plan's order. Read-only pieces that are ready together run together. A later piece reads an earlier one's named outputs from the piece that produced them. Every writing piece (`access: "write"`) must own real source or documentation edits — running `just pack-sync` in its own commit whenever it changes `tools/tillr*` — and `plan` must never propose a standalone `pack-sync` (`piece-pack-sync`) or verification-only `access: "write"` piece, overriding the stale *"Send `pack-sync` to an `edit` piece"* instruction in [`plan-guidance.md`](../../config/tillr/plan-guidance.md) (`D-PT8`, [Section 5.4](#54-when-the-planner-cannot-plan)).

### 5.3 The verify node

The verify node runs after the pieces and before the gate. It reads the criteria, the joined diff and the pieces' outputs, and for each criterion it calls satisfied it names the piece that satisfied it.

- **A criterion no piece covers** is marked not done, and the item goes back to the plan node with that criterion named. Nothing counts these returns.
- **The same finding twice** raises a conflict for the design seat. The finding is identified by the set of criteria not done, so a planner that keeps missing the same thing cannot loop.
- **The verifier is a model, not a command.** Whether a named piece really satisfies a criterion is its judgment; the gate still runs after it.

[The diff-review design](./tillr-diff-review.md) proposes that this node become the one review every code landing gets, run after the gate (its designer's decision `D-DR12`, which awaits the owner's review; not built).

### 5.4 When the planner cannot plan

A planner that cannot cut a piece without a design decision proposes nothing and ends its report with an obstacle naming the owner and its one question. The design seat receives it as a conflict, answers from the documents, or passes the question to the owner in a document.

A worker reports `definition` when the item's words are wrong or contradict the tree. Needing a file not predicted by the planner is not an obstacle. That is [what a worker may end with](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11), and in a workflow of several nodes every blocked report takes the design-seat route. Who may change a frozen criterion is ruled in [criterion repair](./criterion-repair.md#decision-ledger).

> [!NOTE]
> The planner's prompt names only the owner obstacle today. Naming the `definition` kind there too is a prompt edit with no decision in it, and it is not made yet.

**Why a piece fence did not work (`D-PT8`; former behavior).** When `plan` splits a filed request
into `build-pieces@v1` pieces, staging narrows each piece's `fence.txt` to that piece's
`write_scope` while still appending the request's full saved words — including all item-level
criteria — to the piece's brief. On 2026-11-20, on the sharded-runner hang repair whose item fence
already covered `tools/tillr-drain/`, `tools/go-test-shards.py`, and `tests/` ([`D-GR17`](./gate-rework.md#decision-ledger)),
a piece narrowed to `tools/tillr-drain/` read the runner-lifetime criterion in those saved words
and raised `OBSTACLE: fence` for `tools/go-test-shards.py` and `tests/go-test-shards.test.py`.

On 2026-11-19 the owner asked, "didn't we already rule that this is basically impossible and wasteful?" and answered the operator's proposal, "yes, file that." Under the operator request (which the owner may overturn), the piece retry and the item-fence hold are both superseded. Assign a writer a result, not a path list. All writers follow the [same deny-list](./tillr-vocabulary.md#51-the-work-and-its-statement); verification still checks each piece's result. A coupled diagnosis can stay with one writer.

`plan` never appends a trailing verification-only or
`pack-sync`-only `access: "write"` piece after writing pieces have already made the edits and synced
`yolo-packs/tillr/` — overriding the stale *"Send `pack-sync` to an `edit` piece"* line in
[`config/tillr/plan-guidance.md`](../../config/tillr/plan-guidance.md), because
[`brief-template.md`](../../tools/tillr-run/backends/brief-template.md) requires each `tools/tillr*`
writer to commit `just pack-sync` alongside its edit
([`D-JL13`](./tillr-judge-and-lifecycle.md#46-a-held-item-never-occupies-a-lane-d-jl13),
[`D-JL14`](./tillr-judge-and-lifecycle.md#47-offered-commits-own-candidate-lanes-unlaunched-claims-and-named-hold-movers-d-jl14),
[`D-JL16`](./tillr-judge-and-lifecycle.md#49-keeping-a-live-trunk-fixers-work-when-a-rerun-passes-d-jl16),
[`D-JL18`](./tillr-judge-and-lifecycle.md#411-instant-start-decisions-from-a-background-maintained-queue-view-d-jl18),
[Section 5.9](#59-deciding-fork-versus-repository-delivery-at-plan-time)),
because such a piece either starts on its own workflow branch with nothing left to commit for
[`keep-going.ts`](../../tools/tillr-run/backends/pi/keep-going.ts) and falsely raises
`OBSTACLE: definition` or `OBSTACLE: tooling`, or — like a trailing documentation-only
`piece-docs` writer narrowed away from the implementation package (`D-JL16`) — is resumed as the
last writer (`judgeWriterRun`) on a `verify-not-done` or `holdJudge` return that needs repairs
under `tools/tillr*`. When `plan` splits a code-and-docs request, leave a writer responsible for repairing the joined candidate.

**Restart reporting (operator request, 2026-11-20).** At 3:12 PM ET, `tillr drain why --lanes 10` reported `no-lane` for workers still running on `express-1` and `lane-3`. Their last events, `run.finish paused/restart` and `run.resume` at 19:27 UTC (2:27 PM ET), preceded the three later restarts. The workers continued; no later takeover was recorded. The operator saw the same lane-reporting error on `lane-8` and `lane-5`.

**Designer’s decision; the owner may overturn it.** Show the live worker’s run ID and leased lane. The worker records its finish; the drain acts on that result. A live process and a lease prove neither attachment by a later loop nor a decision by that loop not to attach. Attribute a takeover or a decision not to take over only to a loop whose actions are evidenced. Reserve `no-lane` for a run actually waiting for a lane.

**Designer’s decision; the owner may overturn it.** In this plan, `work/piece-reproduce` and `work/piece-report` each run `just pack-sync` and commit the generated copies with their own test or source changes. The piece instruction not to edit the mirror forbids hand edits, not regeneration. On 2026-11-21, the mirror writer ran `just pack-sync` on its staged candidate; `git status --short` returned no changes because the drain source and test already matched their pack copies. That satisfied the mirror requirement on that candidate, not the behavioral check or proof of landing. Omit `work/piece-mirror` on replanning rather than require an empty commit. Have the read-only verifier check both mirror equality and restart reporting on the joined candidate it receives. This leaves the report writer responsible for explaining who acts next, not for changing how runs survive a restart.

### 5.5 A worker that still works alone

The worker's brief lists every seat the policy in force maps, one line each with its purpose, and marks the read-only ones. A fixed paragraph tells the worker to give mechanical searching, editing and checking to the cheaper sub-agents its own harness offers. Those sub-agents' models are chosen by the harness's configuration, not by tillr's policy, and their spend is inside the run's.

### 5.6 The cost report

`tillr drain why`, with no item named, prints:

- the recorded cost of each landed item, and the sum for each seat;
- the median cost of the last twenty landed build items;
- once ten items have landed through `planned-build@v1`, the same median for those items.

A run with no recorded cost makes every sum it belongs to print `unknown`, never zero. Read on 2026-11-18, the build and design seat sums and the build median all printed `unknown`, because older landed items included runs with no cost. Read on 2026-11-19 after twenty priced build items had landed, the build and design all-time seat sums still print `unknown`, while `plan` prints `$0.58`, `scout` `$1.29`, `verify` `$0.17`, and the last-20 landed build median prints `$2.91`; with one landed `planned-build@v1` item, the ten-item planned median is not yet printed.

### 5.7 What nothing selects, and what is not built

| Thing | State on 2026-11-19 | Evidence |
| :--- | :--- | :--- |
| An item that runs through `planned-build@v1` | No document-declared item names it (`rg -n --hidden 'workflow="planned-build@v1"' docs config/tillr` finds only the historical example in [`roadmap.md`](../tillr/roadmap.md)). `tillr item request` defaults `build` filings to `planned-build@v1` ([`request.go`](../../tools/tillr-item/item/request.go), `5a5c459de`): read from the store on 2026-11-19, six filed build requests carry `planned-build@v1`, five have selected it, one (`item-request-6c27d14b15`, commit `90f68aa7b`) has landed on the Copilot policy without a piece split, and one (`item-request-e1016f3a23`) has split `work` into two pieces via `build-pieces@v1`. Document triage (`doc-triage@v1`, `bf59b5be1`) and scouted design (`scouted-design@v1`) also run multi-node workflows | Store `workflow_selections`, `graph_revisions` and `landings`, read 2026-11-19; [`request.go`](../../tools/tillr-item/item/request.go) |
| A default workflow for the build seat | The settings table that would hold it in `.tillr.toml` is built and empty (only `tillr item request` sets a filing default for CLI requests) | `rg workflows .tillr.toml` finds nothing |
| A rule that plans only large items | Not built ([Section 6.2](#62-a-size-rule-tested-on-the-history)) | The settings reader accepts a workflows table with the one key `default` |
| A command that checks each piece the moment it finishes | Not built. The registry of commands a node may run holds one entry, the pack sync | [`commands.json`](../../config/tillr/commands.json) |
| A size, a file count or an interface sketch on a piece | Not built; a piece has the fields of [Section 5.2](#52-what-a-piece-is-and-what-refuses-one) and no others | The drain's README |
| A Luna run of any kind | Luna has run on `scout` in `doc-triage@v1` (47 `node_runs` rows across 44 priced runs totaling $1.12 on `item-doc-triage-2026-11-18-tillr-roadmap-designs`, read 2026-11-19); no run has used `test-run`, and no `planned-build@v1` piece has run on Luna | Store `node_runs` and `runs`, read 2026-11-19 |

### 5.8 One writer for each piece of state

| State | Its one writer |
| :--- | :--- |
| The item, its criteria and place in the queue | The item utility, from the documents or from a store-declared CLI filing (`doc-triage`, `request`). Planning writes none of it |
| The proposal | The planning node's run, as an output the drain checks and records |
| The graph and its revisions | The drain, in the one transaction that applies or refuses a proposal ([`D-IG3`](../reference/tillr-item-graphs.md#proposals-and-graph-revisions)) |
| A piece's commits | That piece's node run, in the item's worktree, one writer at a time |
| A node's seat, model, outcome and usage | The drain, when the node starts and settles |

### 5.9 Deciding fork versus repository delivery at plan time

On 2026-11-19, after a request to change the `pi` fork was filed with repository paths (`yolo-packs/pi-fork/`, `docs/pi/`) as its fence, the owner ruled (`D-PT9`): *"There should not be a way to mis-file an item such that it leads to bad downstream work. That should be decided later in the process."* Until this change lands, only a fence entry of the form `.forks/lanes/<lane>/<fork>/` makes staging give the lane a fork checkout, tell the worker not to deliver, run the fork gate (`fork-build` and `fork-quality`), and have the lander move `patch/<fork>`; filed with repository paths instead, a worker following the interactive fork workflow would run `bin/pi-forks` `export` itself and move the shared delivery branch with no gate while the drain landed only repository files, and the filer also had to know a lane name to write that fence entry.

The filed operator request specifies how to apply that ruling (`D-PT9`; the owner may overturn these implementation choices):

1. **File new work from its words.** The operator request retains `--path` only for compatibility: ignore it with a notice. Never infer fork delivery from a legacy list. For pending fork work the owner said on 2026-11-20, "let's just like go edit the items. That seems easy, right?" Before resuming the queue, the operator lists unfinished items (`tillr item list --store .tillr/state.db`), reads their saved requests or source documents to identify fork work, and edits each pending fork item to name its explicit fork target. This is not an automatic migration of path lists.
2. **Planning decides the delivery target and piece order.** At the `plan` step of `planned-build@v1` — or, for a single-worker item, in a short read-only first step with the same job — tillr decides from the item's words and the tree whether the change is delivered through repository files, one named fork from the forks `bin/pi-forks` (`bin/pi-forks.list`) knows, or both as two ordered pieces (the fork piece first, then the repository files that depend on it). The drain stages each piece accordingly, giving every fork piece an isolated fork checkout starting from the fork head, non-delivery worker instructions, fork gating (`fork-build` and `fork-quality`), and lander-owned `patch/<fork>` advancement before any dependent repository piece dispatches or lands.
3. **Mid-run `delivery` obstacle restaging without an operator.** A worker that discovers mid-run that its change belongs to a fork it was not staged for ends with `OBSTACLE: delivery` naming `FORK` across both [`tools/tillr-drain/drain/obstacle.go`](../../tools/tillr-drain/drain/obstacle.go) and the shared worker ending contract in [`tools/tillr-run/backends/`](../../tools/tillr-run/backends/) (`brief-template.md`, `pi/keep-going.ts`, `pi/obstacle.ts`, and `pi/testdata/obstacle-cases.json`; [`D-JL15`](./tillr-judge-and-lifecycle.md#48-planning-time-fork-delivery-the-delivery-obstacle-and-patch-branch-refusal-d-jl15)); the drain restages the same item as a fork piece and continues it rather than blocking for the operator.
4. **Workers can never move a delivery branch.** Alongside the sibling guard making `bin/pi-forks` `export` and `rebase` refuse when `TILLR_RUN_ID` is set (`f7bf6eba8`), the drain checks every candidate before the gate and refuses any run (or attempt) that moved a `refs/heads/patch/<fork>` branch (excluding lander-marked deliveries), with `docs/pi/forks.md`, [`tools/tillr-item/README.md`](../../tools/tillr-item/README.md), [`tools/tillr-drain/README.md`](../../tools/tillr-drain/README.md), [`yolo-packs/tillr/skills/tillr-operating/SKILL.md`](../../yolo-packs/tillr/skills/tillr-operating/SKILL.md), [`tests/tillr-docs.test.py`](../../tests/tillr-docs.test.py), and [`config/tillr/plan-guidance.md`](../../config/tillr/plan-guidance.md) updated where they describe filing and piece guidance, and `yolo-packs/tillr/` mirrored via `just pack-sync`.

*(Answering the 2026-11-20 `tooling` and `definition` conflicts on this request: first, three `build-pieces@v1` writing pieces committed the change and `just pack-sync` on their workflow branch (`66a89dff3`, `14fc24165`, and `f4f12fe37` [unlanded]) before a trailing `work/piece-pack-sync` `access: "write"` piece raised `OBSTACLE: tooling` against [`keep-going.ts`](../../tools/tillr-run/backends/pi/keep-going.ts); second, after `d4f3c1aa5` answered that conflict, the returned `plan` node again followed the stale "Send `pack-sync` to an `edit` piece" line in [`config/tillr/plan-guidance.md`](../../config/tillr/plan-guidance.md), five writing pieces (`813f72d58`, `1d5c7804e`, `e8fd57086`, `b2857a65a`, and `31cc7b4b0` [unlanded]) already mirrored `tools/tillr*` into `yolo-packs/tillr/` in their own commits, and a sixth `work/piece-pack-sync` (`access: "write"`, `write_scope: ["yolo-packs/tillr/"]`) piece started on `31cc7b4b0` [unlanded] with a clean working tree, had no non-empty commit to satisfy `keep-going.ts`, and raised `OBSTACLE: definition`. None of those branch commits is on the trunk (`9e64c59bf`), so all four requirements of `D-PT9` stand in full; on the returned `planned-build@v1` run, `plan` must ignore `plan-guidance.md`'s standalone `pack-sync` instruction — either proposing no split (`write_scope: "inherit"`, whose `plan` report now carries directly into `work`'s brief after `11dec7eb2`) or ensuring every `access: "write"` piece owns real source or documentation edits with `just pack-sync` included where `tools/tillr*` changes and **no** standalone `pack-sync` piece per `D-PT8`, while also retiring that stale line in `config/tillr/plan-guidance.md` inside `config/tillr/`.)*

**Filing piece before the split (designer's decision, 2026-11-20; the owner may overturn it).** The filing worker starts from its staged repository base. It changes intake and its tests in `tools/tillr-item/`, runs `just pack-sync`, and commits the generated `yolo-packs/tillr/files/local/share/tillr/tillr-item/` changes with the source and tests. “Do not edit documentation or mirrors in this piece” excludes hand edits, not that required generated output. The filing documentation remains assigned separately; there is no mirror-only follow-up.

The operator's instruction to continue partial work applies to the piece that owns it. On 2026-11-20, `git show --stat 969e84a84` showed only `tools/tillr-drain/drain/delivery.go`, its test, and their pack copies [unlanded]. That work belonged to the drain delivery piece, not the filing piece.

**Validation-only retry (operator split, 2026-11-21; the owner may overturn it).** For `item-request-ed0e11d000`, verify and offer the existing validator, its test and its generated mirror. The validator checks a decision already made: `repo` names no fork; `fork` and `both` require one known fork; `both` returns the fork first and the repository second. Returning two targets in order does not stage or land either piece. If staged at trunk, use the validation candidate `099595afe` [unlanded]; if the lane already contains it, verify it there rather than reimplementing it.

`item-request-3b892e528e` owns planner and single-worker classification, staging from the recorded target, and continuing the repository piece after the fork lands. Drop the stale delivery-obstacle node from the validation plan. Automatic restaging remains part of the delivery contract, but its implementation must follow durable target and continuation support; rewriting `items.allowed_paths` is not a substitute. Neither obstacle handling nor pre-gate branch checks are acceptance criteria for the validator.

---

## 6. What planning costs, and what would choose it

### 6.1 The estimate of 2026-11-17

These are estimates, made before any of this was built. The model starts from a successful single Sol run of cost *C* and splits it into three pieces, using the phase shares of [Section 1.2](#12-where-a-sol-runs-money-goes). It assumed a plan at $0.35, a seam of $0.17 per piece on Sol and $0.085 on Terra, turns 15% cheaper because each piece's context is smaller, and a failed cheap piece run again in full on Sol.

| *C*, one Sol run | One Terra run, at half the price | Planned, all on Sol | Planned, Terra pieces, none fail | 15% fail | 30% fail |
| ---: | ---: | ---: | ---: | ---: | ---: |
| $0.50 | $0.25 | $1.29 | $0.82 | $0.95 | $1.07 |
| $1.00 | $0.50 | $1.63 | $0.99 | $1.16 | $1.34 |
| $2.00 | $1.00 | $2.30 | $1.33 | $1.60 | $1.87 |
| $3.00 | $1.50 | $2.98 | $1.66 | $2.03 | $2.40 |
| $6.00 | $3.00 | $5.00 | $2.68 | $3.34 | $4.00 |

What the table says now that Terra is the ordinary builder:

- **Against one Terra run that succeeds, planning costs more on every item under about $4 of Sol-priced work** (estimated: the second and fourth columns cross between the $3 and $6 rows). The second column assumes Terra needs the same turns as Sol, which nothing has measured.
- **Planning's case is failure, not price.** The table ignores retries. The items a size rule would pick landed on their first run 16% of the time ([Section 6.2](#62-a-size-rule-tested-on-the-history)), and under a split a stop loses one piece instead of a run.
- **The table leaves out the verify node**, which did not exist when it was made, and prices the planner at an effort the policy no longer uses. Both move the planned columns, in opposite directions.

So the estimate cannot settle whether planning pays. Ten real items can ([Section 7](#7-how-the-owner-learns-whether-it-pays)).

### 6.2 A size rule, tested on the history

No rule decides which items are planned. One was drafted and tested against the history of 2026-11-17 (measured; 85 build items, $237.84 of deduplicated recorded spend): plan a build item when it has many criteria or its paths span three or more module directories.

| Rule at first dispatch | Items selected | Share of spend | Mean spend per item: selected, others | Landed on the first run: selected, others |
| :--- | ---: | ---: | :--- | :--- |
| 5+ criteria, or 3+ modules | 56 | 81% | $3.44, $1.56 | 9 of 56 (16%), 12 of 29 (41%) |
| 6+ criteria, or 3+ modules | 40 | 57% | $3.41, $2.25 | 5 of 40, 16 of 45 |
| 4+ criteria, or 3+ modules | 75 | 97% | $3.08, $0.69 | 15 of 75, 6 of 10 |

The rule is a weak predictor of cost: it separates means by a factor of two. It is a better predictor of trouble. Criteria count alone barely discriminates, because 80 of 92 items have four or more. Whether such a rule is built at all is [OQ-PT8](#OQ-PT8).

### 6.3 How many pieces

A proposal is capped at 12 pieces. The estimate above put each added node's seam at $0.17 on Sol, so many small pieces spend their saving on seams, and every handoff loses context that a piece's outputs only partly restore. The smallest useful split is one package of code plus its documents. The planner's guidance does not yet say this; it is advice for whoever edits that guidance, not a rule the loader enforces.

---

## 7. How the owner learns whether it pays

1. **Keep the build baseline priced (and price older runs if all-time seat sums are wanted).** Once twenty priced build items have landed, the last-20 build median prints a dollar figure (`$2.91` on 2026-11-19); older unpriced runs still keep the all-time `build` and `design` seat sums at `unknown`.
2. **Land ten build items through `planned-build@v1`.** One filed build request has landed so far (`item-request-6c27d14b15`, without a piece split), and `tillr item request` defaults `build` filings to `planned-build@v1`; choosing document-declared build items still takes naming the workflow on each item. The 16%-against-41% split of [Section 6.2](#62-a-size-rule-tested-on-the-history) says where to look: items with five or more criteria, or paths in three or more modules.
3. **Read the two medians** that `tillr drain why` then prints, and the per-seat sums beside them.
4. **Ask [OQ-PT8](#OQ-PT8).**

What exists before then, read on 2026-11-19:

- **Terra has built real work.** On 2026-11-18, 46 run requests named the Terra model across 25 items and 16 of those items had landed (five of them items of the planning stage itself, which edit the drain), before run records carried summed dollar figures; read from the store on 2026-11-19, 63 runs across 35 items name Terra (62 priced, totaling $151.99, and 1 without recorded cost). Turn counts and failure rates against Sol on equivalent work are still unmeasured.
- **Luna has run on scouting, not in a planned build.** Read from the store on 2026-11-19, 47 `node_runs` rows on the `scout` seat name `gpt-6-luna` in `item-doc-triage-2026-11-18-tillr-roadmap-designs` (`doc-triage@v1`), with 44 priced runs totaling $1.12. No run has used the `test-run` seat, and no `planned-build@v1` item has run a piece on Luna.
- **A second way to compare models exists on paper:** replaying recorded tasks for several models side by side, in [the model benchmarks design](./model-benchmarks-from-drain-records.md). The cost report compares what landed items cost in production; a replay compares models on the identical task. Neither replaces the other.

One figure is computed inside tillr: the cost medians above. The owner's ruling of 2026-11-15 puts comparisons between backends in a separate project of his, computed from the run records ([the seat-share item](../tillr/roadmap.md#add-backend-shares-to-distribute-seat-work)); that still holds for every rate other than these medians (`D-PT5` in the [Decision Ledger](#decision-ledger)).

---

## 8. Worked examples

Each figure is measured from the logs read on 2026-11-17. They are kept for what each item's cost turned out to be made of.

| Item | Kind | Cost | Runs | What the money went on |
| :--- | :--- | ---: | ---: | :--- |
| `item-agent-default-bmad-superpowers-skills` | Documents-only ruling | $4.42 | 6 | Two build runs ($0.64) each found a blocker outside the item's fence, and three design runs answered them |
| `item-jl-refuse-clone-ids` | Small change in one Go package | $0.46 | 1 | One clean run: orientation $0.18, tests $0.11 |
| `item-comments-first-delivery` | Seven criteria over three Go modules | $15.04 | 7 | Two runs killed or stopped after 113 and 222 turns, with contexts of 255k tokens; the last was landed by hand as `895409b38` |
| `item-obs2-stop-says-what-and-next` | Failed again and again | $11.77 | 6 | Three killed runs, and one of 152 turns ($7.61) that never committed |
| `item-design-gate-rework` | One Fable design run | $40.07 | 1 | $33.90 of cache writes: 1.695M tokens written against a largest context of 256k |

The lessons:

- **A planner cannot see a blocker outside the fence before trying.** The first item's real cost was a capability its fence did not include. A planner would have found it for about what the two build runs did.
- **A small item should never be planned.** A plan costs more than the whole $0.46 item.
- **Large items fail by stopping, and a stop costs the whole run.** The two large items spent most of their money on runs that ended without a commit. This is the case planning is for: under a split, each stop would have cost one piece.
- **The largest single cost is neither routing nor planning.** If each token of the design run had been written to the cache once, it would have cost about $11.65 (estimated). The investigation is in the cache rewrites finding.

---

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A cheap model only behind a command that checks it the moment it finishes**, with each piece carrying its own registered check | **Not adopted.** The owner's rule puts cheap models on well-specified work and has a mid model verify; the build seat is Terra with no per-piece command. A per-piece command check could still be added on top of the verify node, and it is not proposed until planned items show what the verifier misses |
| **A ladder of models for each seat**, climbed one rung per failed check, with two rungs per piece and one re-plan per item | **Withdrawn.** Each of those is a count, and the owner ruled counts out. A stronger model is reached when a worker's tested attempt shows the task exceeds its model |
| **A trial of Terra on five named items before it builds anything else** | **Overtaken.** The owner put Terra on the build seat on 2026-11-18. The five items had already landed on Sol |
| **One session that switches model mid-run** | **Rejected.** A new model has none of the old one's cache, so a switch writes the whole context again: about $0.24 at a median 97k context on Sol, and about $1.94 on Fable at the one-hour rate. A node is one invocation on one route ([`D-IG12`](../reference/tillr-item-graphs.md#why-its-this-way)) |
| **Many tiny nodes** | **Rejected as a goal; the cap is 12 pieces.** Each node pays a seam, and every handoff loses context |
| **Cheaper sub-agents inside one worker** | **Adopted beside planning, not in place of it.** The worker's brief encourages it ([Section 5.5](#55-a-worker-that-still-works-alone)). tillr cannot fence, check or count a sub-agent apart, which is what a piece is for |
| **Planning on the expensive model** | **Rejected.** Fable's median orientation, $2.12, is more than most build items cost in total |
| **Committing the plan as a document** | **Open, and blocked:** [OQ-PT7](#OQ-PT7). Today the plan is kept with its run and shown on the item's page |

---

## 10. What this design does not do

- It does not split one item into several items, and does not change the queue, the gate or the landing (P3).
- It does not let any node edit criteria (P6).
- It does not count attempts, plans or fixes, and it sets no spending limit (P7; [the graph design's ruling that there is no spend target](./tillr-graph-is-the-path.md#decision-ledger)).
- It does not turn planning on by default in `.tillr.toml` or on document-declared items (`tillr item request` defaults CLI `build` requests to `planned-build@v1`). Defaulting or sizing all build items is [OQ-PT8](#OQ-PT8).
- It does not route design rulings below the design seat.
- It does not run writing pieces in parallel ([`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way)).
- It does not touch the pi sub-agent seat profiles. They govern child sessions, and this design governs nodes.

---

## 11. Risks

| Risk | What limits it |
| :--- | :--- |
| Terra takes many more turns than Sol, so its real price ratio is near 1 | No side-by-side comparison exists yet ([Section 7](#7-how-the-owner-learns-whether-it-pays)); the cost report compares medians once ten planned items have landed |
| A cheap piece is wrong in a way the verifier misses | The gate still runs, and automatic revert is on for a landing that turns the trunk red. A command check per piece is the unbuilt remedy ([Section 9](#9-alternatives-considered)) |
| A cheap model's change breaks the loop's own code | The same: the module's suite, the gate and automatic revert, as for any model ([`OQ-PT5`](#OQ-PT5) in the Decision Ledger) |
| The planner splits along the wrong seams, so pieces fight | The verify node returns the item to the planner with the uncovered criteria named; the same finding twice goes to the design seat |
| Planning costs more than it saves on most items | It is not the `.tillr.toml` default for document-declared items until ten planned items have been measured ([OQ-PT8](#OQ-PT8)) |
| The before-and-after comparison is made against `unknown` | Step 1 of [Section 7](#7-how-the-owner-learns-whether-it-pays) (the last-20 build median prints `$2.91` on 2026-11-19; all-time `build` and `design` seat sums remain `unknown`) |

---

## 12. Rulings this builds on

| Ruling | Date | Does it still hold? |
| :--- | :--- | :--- |
| retired question SP5: on Bedrock only Sol and Luna were worth encoding | 2026-10-20 | **Overtaken for the work machine's seats** by the owner's instruction of 2026-11-18, which put Terra on implementation ([`OQ-PT1`](#OQ-PT1) in the Decision Ledger) |
| retired question SP6, the Luna-worker verdict: "replace, do not keep" | 2026-10-20 | **Holds.** Luna fills only read-only seats |
| retired question CP1: the planner row is Sol at high | 2026-10-21 | That ruling is about pi's sub-agent profiles. tillr's `plan` seat runs Sol 6.2 at medium, the effort every backend row in the work policy states |
| [`D-IG3`](../reference/tillr-item-graphs.md#why-its-this-way), [`D-IG4`](../reference/tillr-item-graphs.md#why-its-this-way), [`D-IG12`](../reference/tillr-item-graphs.md#why-its-this-way) and [`OQ-IG3`](../reference/tillr-item-graphs.md#why-its-this-way) | 2026-11-15 | **Hold.** Planning is a proposal; nothing loops; writers run one at a time |
| No attempt counts, retry budgets or exhausted states ([rule R1](./tillr-graph-is-the-path.md#1-the-direction-and-the-rules)); seats are declared in the policy, with no rungs ([`D-GP6`](./tillr-graph-is-the-path.md#decision-ledger)) | 2026-11-17 | **Hold**, and withdraw this design's ladders and caps |
| A worker ends with its commit or a named obstacle ([`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)) | 2026-11-18 | **Holds** for a wrong criterion; the `fence` kind is retired |
| Comparisons between backends are computed in the owner's own project | 2026-11-15 | **Holds**, except the cost medians of [Section 5.6](#56-the-cost-report) |
| The observatory shows usage, never a price | 2026-11-15 | **Holds** for the observatory. The cost report is the drain's own text output |
| The one-hour cache retention (`c5f4a82f7`) | 2026-11 | Unchanged here |

---

## Decision Ledger

Three kinds of row, and each says which it is. An **owner ruling** quotes or cites words the owner said. **Closed as stale** means the question no longer exists, and says what replaced it. A **designer's decision** was made by a design seat from the evidence named; the owner has not reviewed it, and the owner may overturn this.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-PT1 | **Owner ruling.** Cheaper models do build work: Terra implements, Luna scouts, and Sol 6.2 is used "where appropriate". Recorded in commit `09760605f` as "The owner's instruction of 2026-11-18: use cheaper models, Terra for implementation and Luna for scouts", and in the roadmap as his ask to "split different types of work into different seats as we see fit". The question's three options are all overtaken: none of them was "Terra writes the code with a mid model verifying", which is what he ordered | 2026-11-18 | [Section 4](#4-kinds-of-work) | ✅ the work policy |
| OQ-PT2 | **Closed as stale.** A five-item Terra trial before a ramp cannot be run: Terra became the build seat for seat-tagged work in `09760605f` and for every ordinary item in `0b21890da`, both on 2026-11-18, and the five named items had landed on Sol | 2026-11-18 | [Section 7](#7-how-the-owner-learns-whether-it-pays) | — nothing to build |
| OQ-PT3 | **Closed: ruled elsewhere and built.** A planner that finds a criterion it cannot meet reports it and the design seat acts; it never rewrites the criterion. This is the `definition` obstacle of [`D-JL11`](./tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11), which records the owner's ruling of 2026-11-18 that a worker ends with its commit or a named obstacle. Who may change a criterion is the owner's ruling [`OQ-CR2`](./criterion-repair.md#decision-ledger) of 2026-11-17 | 2026-11-18 | [Section 5.4](#54-when-the-planner-cannot-plan) | ✅ the route; the planner's prompt does not name the kind yet |
| OQ-PT4 | **Superseded.** Operator request (the owner may overturn it): a planner need not predict paths; a needed path absent from an old list neither invalidates the proposal nor calls for a design answer. | 2026-11-19 | [Section 5.4](#54-when-the-planner-cannot-plan) | Replacement requested |
| OQ-PT5 | **Designer's decision; the owner may overturn this.** A cheaper model may change the loop's own code, with no special rule for it. **Evidence:** the owner's instruction put Terra on implementation without an exception; every ordinary item has run on Terra since `0b21890da`, and 16 Terra-built items have landed, among them five that edit the drain; automatic revert, which the owner approved, has been on since `a00e8a600`. **Rejected:** starting loop-code work on the stronger model until records justify otherwise, which has no mechanism built, and by this design's own count would keep most of the queue off the cheaper model | 2026-11-18 | [Section 11](#11-risks) | ✅ nothing to build |
| OQ-PT6 | **Owner ruling.** No item has a number of plans it may use. The owner's standing rule is "No attempt counts, retry budgets or 'exhausted' states" ([rule R1](./tillr-graph-is-the-path.md#1-the-direction-and-the-rules), 2026-11-17), and the roadmap block that filed the planning stage says of it "There are no rungs, no re-plan caps and no time limits that fail work." A failed verify returns the item to the planner uncounted, and the same finding twice goes to the design seat | 2026-11-17 | [Section 5.3](#53-the-verify-node) | ✅ |
| D-PT1 | **Designer's decision, amended to the tree.** Each kind of work is a seat. The seat list is open: any name the policy maps, as [`D-GP6`](./tillr-graph-is-the-path.md#decision-ledger) says and as the owner asked ("a few more seat names to slot into"). Ten seats are declared; the `fix`, `prose` and `mechanical` seats first proposed were not built | 2026-11-16, amended 2026-11-18 | [Section 4](#4-kinds-of-work) | ✅ |
| D-PT2 | **Designer's decision, amended to the tree.** A planner's list of pieces is a bounded workflow argument. Built as `pieces`, at most 12 entries, in place of the 2-to-6 slice list first proposed | 2026-11-16, amended 2026-11-18 | [Section 5.2](#52-what-a-piece-is-and-what-refuses-one) | ✅ |
| D-PT3 | **Withdrawn.** Ladders and two rungs per piece are counts, which the owner ruled out. The policy's `escalate` list moves an item to a stronger backend on a worker's diagnosis | 2026-11-16, withdrawn 2026-11-18 | [Section 9](#9-alternatives-considered) | — |
| D-PT4 | **Designer's decision, amended to the tree.** The planner is the first node of the workflow, and a planner that proposes nothing leaves the item to one worker. Built as `planned-build@v1` | 2026-11-16, amended 2026-11-18 | [Section 5.1](#51-the-workflow) | ✅ |
| D-PT5 | **Designer's decision; the owner may overturn this.** tillr records each node's seat, model and cost, and comparisons between backends stay in the owner's project. One exception was built on 2026-11-18 by the design seat that cut the planning stage, with no owner ruling recorded for it: `tillr drain why` computes cost medians, in dollars | 2026-11-16, amended 2026-11-18 | [Section 5.6](#56-the-cost-report) | ✅ |
| D-PT6 | **Withdrawn.** "One re-plan, as the next attempt" is a count. A re-plan is the verify node's return to the planner | 2026-11-16, withdrawn 2026-11-18 | [Section 5.3](#53-the-verify-node) | — |
| D-PT7 | **Superseded.** A fix does not continue a session under a 150k-token bound; a refused run goes back to its worker's own session with no bound ([how a worker run's ending moves its item](./tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)) | 2026-11-16, superseded 2026-11-18 | [Section 4](#4-kinds-of-work) | ✅ elsewhere |
| D-PT8 | **Designer's decision; the owner may overturn this.** When a filed request's criteria share one coupled repair and verification run across a module, its test runner, and `tests/` (such as `tools/tillr-drain/`, `tools/go-test-shards.py`, and `tests/` in [`D-GR17`](./gate-rework.md#decision-ledger), or across `tools/tillr-item/`, `tools/tillr-run/`, `tools/tillr-drain/`, `config/tillr/`, `docs/pi/`, and `tests/` in `D-PT9`), `plan` in `planned-build@v1` may leave `work` unsplit (`write_scope: "inherit"`, carrying `plan`'s report into `work`'s brief since `11dec7eb2`). If `plan` splits the work, each writing piece owns real source or documentation edits. A piece touching `tools/tillr*` runs `just pack-sync` and includes the matching mirror in its own commit. A piece prompt forbidding mirror edits does not defer regeneration to a later writer. For the restart report, a clean `just pack-sync` on the joined candidate needs no mirror commit. Verify separately that `why` identifies the continuing run on its own lane and distinguishes a recorded takeover decision from the absence of one. The piece retry and item-fence hold formerly specified here are superseded by the operator request (the owner may overturn it). Each writer satisfies its own prompt's slice. Plan for the final writer to repair the joined candidate after `verify-not-done` or `holdJudge` (`judgeWriterRun`); leave verification-only steps to `read_only` seats rather than a trailing no-op or `pack-sync`-only `write` piece — explicitly overriding the stale *"Send `pack-sync` to an `edit` piece"* line in [`config/tillr/plan-guidance.md`](../../config/tillr/plan-guidance.md) ([Section 2](#2-principles), [Section 5.2](#52-what-a-piece-is-and-what-refuses-one), [Section 5.4](#54-when-the-planner-cannot-plan), [Section 5.9](#59-deciding-fork-versus-repository-delivery-at-plan-time), [`D-JL13`](./tillr-judge-and-lifecycle.md#46-a-held-item-never-occupies-a-lane-d-jl13), [`D-JL14`](./tillr-judge-and-lifecycle.md#47-offered-commits-own-candidate-lanes-unlaunched-claims-and-named-hold-movers-d-jl14), [`D-JL16`](./tillr-judge-and-lifecycle.md#49-keeping-a-live-trunk-fixers-work-when-a-rerun-passes-d-jl16), [`D-JL18`](./tillr-judge-and-lifecycle.md#411-instant-start-decisions-from-a-background-maintained-queue-view-d-jl18)). **Rejected:** splitting coupled cross-module work into narrower pieces whose briefs carry item-wide criteria against a single piece's `write_scope`; following `plan-guidance.md` to append a standalone `pack-sync` (`piece-pack-sync`), verification-only, or trailing docs-only (`piece-docs`) `access: "write"` piece narrowed away from `tools/tillr*` implementation paths | 2026-11-20 | [Section 2](#2-principles), [Section 5.2](#52-what-a-piece-is-and-what-refuses-one), [Section 5.4](#54-when-the-planner-cannot-plan) | Guidance recorded |
| D-PT9 | **Owner, 2026-11-19:** *"There should not be a way to mis-file an item such that it leads to bad downstream work. That should be decided later in the process."* **Operator request and designer's implementation decisions; the owner may overturn them.** The operator's 2026-11-21 split supersedes the earlier piece plan: `item-request-ed0e11d000` is decision validation, test and mirror only. Omit its stale obstacle node. `item-request-3b892e528e` owns classification, staging and ordered mixed delivery; obstacle restaging depends on that work. Delivery contract: (1) filing takes words; ignore `--path` with notice. The operator edits pending fork items to name their target before resumption, rather than inferring it from old lists; (2) `plan` in `planned-build@v1` — and, on a single-worker item, a short read-only first step with the same job — decides from the words and tree whether delivery is repository files, one named fork known to `bin/pi-forks`, or both in fork-then-repository order, and the drain stages, gates, and lands each piece accordingly; (3) a worker discovering mid-run that its change belongs to an unstaged fork ends with `OBSTACLE: delivery` (`FORK`) across both `tools/tillr-drain` and `tools/tillr-run/backends/` (`keep-going.ts`, `obstacle.ts`, `obstacle-cases.json`, `brief-template.md`), and the drain restages and continues the item as a fork piece without blocking for the operator ([`D-JL15`](./tillr-judge-and-lifecycle.md#48-planning-time-fork-delivery-the-delivery-obstacle-and-patch-branch-refusal-d-jl15)); and (4) any candidate whose run (or attempt) moved a `patch/<fork>` branch is refused before the gate alongside `bin/pi-forks` refusing `export` and `rebase` under `TILLR_RUN_ID` (`f7bf6eba8`). **Rejected:** requiring `.forks/lanes/<lane>/<fork>/` fence entries from filers to trigger fork staging; omitting the single-worker first classification step or `keep-going.ts` `delivery` obstacle parity; allowing a drain worker to deliver or advance `patch/<fork>` itself; blocking for the operator when a worker reports `OBSTACLE: delivery`; scheduling a standalone `pack-sync` `access: "write"` piece (`piece-pack-sync`) after writing pieces that already run `just pack-sync` | 2026-11-19 | [Section 5.9](#59-deciding-fork-versus-repository-delivery-at-plan-time) | Queued |

---

## Open Questions

1. 🔒 **OQ-PT7: Do you want an item's plan committed as a document, as well as shown on the item's page?**

   You open a planned item's page to see how the planner split it. The page shows the pieces, any refusal and each revision, read from the run's record. Nothing about the plan is committed. A committed copy could be read in Vantage and would outlive the store; it would also add a file to every planned item, including plans that were abandoned.

   - **A — The item's page only**, as built. Nothing extra lands with the item.
   - **B — Committed too.** A short plan document rides in the item's one commit.

   <!-- vantage: question id=OQ-PT7 blocked-on="first-planned-item-with-pieces-landed, ./tillr-storage.md#OQ-TS1" leaning="The item's page only (A): a plan records how one attempt went, and the documents hold what must be built." -->

   _Leaning:_ The item's page only (A): a plan records how one attempt went, and the documents hold what must be built.

   Blocked until a planned item that actually split into pieces has landed, so there is a landed plan to look at (`item-request-6c27d14b15` landed without a split, while `item-request-e1016f3a23` split into two pieces and has not landed yet), and until [the storage design's first question](./tillr-storage.md#OQ-TS1) says where an item's record lives.

   **Answer:**
   > _(empty — fill in when decided)_

2. 🔒 **OQ-PT8: Should every build item be planned first, or only the ones that ask for it?**

   An author files a document-declared build item with five criteria across two Go modules. The loop gives it to one Terra worker, because `.tillr.toml` sets no default workflow. A second way is built: a planner hands pieces to cheaper workers, and a verifier checks the result (`tillr item request` already defaults CLI `build` requests to `planned-build@v1`, and one has landed without a piece split). Fewer than ten planned items have landed, so the planned-workflow median cost is still unknown.

   - **A — Only when the author asks**, by naming the workflow on the item (with CLI `build` requests defaulting to it unless overridden). Nothing to build. Large document-declared items nobody marked keep losing whole runs.
   - **B — Every build item.** One settings line. Small items pay for a planner and a verifier that cost more than they do.
   - **C — Only large items.** Needs a rule built. The drafted one picked 56 of 85 past items, which held 81% of spend.

   <!-- vantage: question id=OQ-PT8 blocked-on="ten-planned-items-landed" leaning="Only when the author asks (A) until the figures exist; then C if planning is cheaper only on large items, B if it is cheaper overall." -->

   _Leaning:_ Only when the author asks (A) until the figures exist; then C if planning is cheaper only on large items, B if it is cheaper overall.

   Blocked until ten items have landed through the planned workflow (one has landed on 2026-11-19). `tillr drain why` then prints their median cost beside the ordinary build median ([Section 7](#7-how-the-owner-learns-whether-it-pays)).

   **Answer:**
   > _(empty — fill in when decided)_
