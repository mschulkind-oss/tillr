---
title: "Plan first, then give each node the cheapest seat whose check it can pass"
status: in-review
stage: DESIGN
updated: 2026-11-16
tags:
  - tillr
  - graphs
  - routing
  - cost
  - models
  - design
summary: "A planning node splits a large build item into slices, and each slice carries its own registered check. Each kind of work is a seat, and the backend policy puts each seat on a ladder of models, cheapest first. A node runs below the top rung only when a deterministic check right after it decides whether it passed. Measured on the loop's history: the larger savings are in failed runs and in context written to the cache twice, and routing to cheaper models is worth 5–35% of build spend depending on how often the cheaper model fails."
next: "The owner rules OQ-PT1 and OQ-PT2. The Terra trial is a policy edit that can run before the ten-lane ramp; nothing else here should."
depends-on:
  - tillr-item-graphs.md
  - tillr-judge-and-lifecycle.md
---

# Plan first, then give each node the cheapest seat whose check it can pass

**Status:** 2026-11-16. This is a proposal, and none of it is built. The evidence is the loop's event log at event 4343 (generated 2026-11-17 03:33 UTC) and 334 pi run logs, read against the tree at `424cc9db0`. Every figure is marked **measured**, with its source, or **estimated**, with its assumption.

> **In short.** What a node costs depends on how its failures are caught, not on which model runs it. A cheap model saves money only where something deterministic catches its mistakes before they spread. So the planning node's job is to cut a large item into slices that each carry their own registered check. The backend policy, never the item, puts each kind of slice on the cheapest model that stays cheaper than the model above it once its failures are paid for.

**Why it matters.** Before going to ten lanes the owner wants Fable 5.2, Sol, Terra and Luna each used where they pay. Today every build is one Sol session and every ruling is one Fable session. In that history only 41% of spend went to runs that landed (measured).

**The shape.** Four parts:

- a `plan` seat, the first node of a tracked workflow;
- a new bounded argument, the slice list;
- a fixed per-slice topology (build, check, fix, check, escalated fix, check);
- `ladders` in the backend policy.

There is no new program. The graph mechanics are [item graphs](./tillr-item-graphs.md)' own.

**Cost.**

- The seat vocabulary grows from four seats to eight.
- The workflow format gains one argument type.
- The policy gains one key, and each stage gains one field.
- Every planned item pays about $0.35 for its plan and about $0.08–0.17 for each extra node (estimated).

**Start at** [Section 1.6](#16-what-the-history-means-three-levers-ranked), which explains why routing is the third-largest lever and not the first. The design falls out of that ranking.

**Needs your ruling:** [OQ-PT1](#OQ-PT1), [OQ-PT2](#OQ-PT2), [OQ-PT3](#OQ-PT3), [OQ-PT4](#OQ-PT4), [OQ-PT5](#OQ-PT5), [OQ-PT6](#OQ-PT6), [OQ-PT7](#OQ-PT7), [OQ-PT8](#OQ-PT8).

**Reads with:**

- [`tillr-item-graphs.md`](./tillr-item-graphs.md): workflows, nodes, proposals, fences. This design adds to it and overrides none of it.
- [`tillr-judge-and-lifecycle.md`](./tillr-judge-and-lifecycle.md): the evaluator, verdicts and criteria.
- [`seat-profiles.md`](./seat-profiles.md): the October rulings on which Bedrock models are worth using.
- [`criterion-repair.md`](./criterion-repair.md): who may change a frozen criterion.
- The [tillr roadmap](../tillr/roadmap.md): where these questions are routed.

No implementation-plan seed exists yet. The sketches in [Section 7](#7-routing-is-policy-not-code) are at design altitude.

---

## Summary for the owner

**The proposal in ten lines.**

1. Route by verifier. A node runs on a cheaper model only when a registered command checks it the moment it finishes.
2. Each kind of work is a seat: `plan`, `design`, `build`, `fix`, `prose`, `mechanical`, `scout` and `review`. Items name seats, and only the policy names models.
3. Each seat climbs a ladder of backends, cheapest first, and escalates one rung when its check fails. The cap is two rungs for each slice.
4. A build item over the planning threshold is slotted into `plan-then-build@v1`. Its first node, Sol at high, reads the item and proposes slices.
5. A slice has four parts: one write scope inside the fence, the criterion ids it serves, one registered check, and a size. A plan has at most six slices.
6. A deterministic checker accepts or refuses the plan. A refused plan runs the item as one node, as today, so planning can never block an item.
7. A failed attempt is planned again, once, on Fable. After that the item runs as one Sol node, and then it is held.
8. Slices hand over context through the plan, an interface sketch, and a notes file from each slice. A fix on the same backend continues the session it repairs.
9. The gate still runs once and the landing is still one commit. The outside world still sees one item.
10. The owner learns whether a placement is wrong from per-node records: seat, rung, check outcome and usage. The rates are computed in the owner's own project, as ruled on 2026-11-15.

**Expected saving, with its uncertainty.** These are estimates, because no Terra run has ever been measured in this repository.

| Cheaper model's failure rate | Saving on build spend from routing |
| :--- | :--- |
| about 15% | 20–35% |
| 30% | 5–20% |
| half the work Sol does today | none: the break-even for Terra against Sol |

Routing saves nothing on design spend. Planning alone, still on Sol, costs more than it saves on any item under about $3 (estimated). Two levers that are not routing are larger:

- runs that edited files and then ended uncommitted or were killed are $98 of $228 (43%) of Sol spend (measured);
- context written to the cache more than once is an estimated $69 of $266 (26%) of Sol spend.

**What can be on before the ten-lane ramp, in hours:**

- a Terra trial on five named single-package items, which is a policy edit using the existing `overrides`;
- a filed defect for duplicated run usage;
- a filed investigation into the doubled cache writes.

Planning needs eight queued item-graph blocks and three new items, so it is days of drain time, not hours. Do not switch seats wholesale to cheaper models, turn on planning, or allow parallel writers before the ramp.

**Decisions needed:** [OQ-PT1](#OQ-PT1) (cheaper models for build work at all) and [OQ-PT2](#OQ-PT2) (the trial before the ramp) first. The other six shape stages 1 to 3.

---

## 1. What the run history says

Every figure in this section is **measured**, from one of two sources:

- `tillr view events --since 0` at event 4343, generated 2026-11-17 03:33 UTC;
- the 334 pi run logs in the lanes, read the same day, with each turn priced by pi's own usage record.

Wall times cap each wait between turns at 20 minutes and set the wait after a final turn to zero. Without that, a session that ended while saying "I'll commit after the tests" counts days of idle time.

### 1.1 Where the money went

The store records 338 runs and $455.56. That total double-counts, for two reasons:

- **Duplicated usage.** 49 runs carry exactly another run's tokens and cost, $133.65 in all. Most are killed runs of one or two seconds. For example, 21 runs of `item-land-retires-block` each record $3.134482 and 16,061,350 tokens.
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

A run that never edits is a scout by accident and costs about $0.20. **39 of the 140 Sol runs that edited (28%) ended with their edits uncommitted**: the defect that [ending with passing work uncommitted](../tillr/roadmap.md#prevent-a-build-run-from-ending-with-passing-work-uncommitted) files.

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

The scores come from [`seat-profiles.md`](./seat-profiles.md#25-bedrock-work-aws--approved-as-narrowed-2026-10-20-only-solluna-rows-encode).

Repricing the same tokens shows what a model change alone is worth (measured tokens, priced from the table):

- The Sol logs' tokens cost $266.25 on Sol, **$133.12 on Terra** (exactly half), **$18.51 on Luna** (7%) and $1,071 on Fable.
- The Fable logs' tokens cost $99.36 at the table price, and $22.46 at Sol's prices, a ratio of 0.226.

All of this assumes a cheaper model needs the same turns. Nothing in this repository measures that.

### 1.5 What the history cannot say

| Unmeasured | Why | Consequence |
| :--- | :--- | :--- |
| Terra's and Luna's failure rate and turn count on this repository's work | No run has used either | Every routing saving is an estimate until the trial in [Section 10.1](#101-stage-0-measure) runs |
| What child sessions spend | A run's recorded cost leaves it out: one log sums to its recorded $7.61 and its sub-agent results carry no usage | Spend is understated by an unknown amount |
| The exact test wait | Background-task waits are capped and partly hidden | The 33% test wall share is a lower bound |
| Why runs write their context to the cache twice | Inferred from totals, not traced | [Section 1.6](#16-what-the-history-means-three-levers-ranked), lever 2 |
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
   - It is real but conditional, and it is the only lever that needs a planning node: a session cannot change models without losing its cache ([Section 11](#11-alternatives-considered)).

The design follows from this order. The planning node is worth building mainly because it makes failures cheap and early (lever 1) and keeps contexts small (part of lever 2). Routing slices to cheaper models (lever 3) rides on the same structure.

---

## 2. Principles

- **P1. Route by verifier.** A node may run below the top rung of its seat's ladder only when a registered command checks it right after it finishes. Where only judgment can tell whether the work is right, the top rung does it.
- **P2. Kinds of work name seats, and models live only in the policy.** An item, a workflow and a plan name seats, never a model, so a provider change is one policy edit ([D-CB3](./tillr-claude-backend.md), and the owner's habit of switching providers often).
- **P3. One item outside.** Planning changes only the inside of an item. The queue, the fence, the gate and the landing still see one item ([`D-IG2`](./tillr-item-graphs.md#21-intra-item-execution-graphs-vs-inter-item-lineage)).
- **P4. A split pays for its seams.** Each extra node pays a first turn and a partial re-orientation. A split is made only where a check, a cheaper seat or a bounded stop pays more than that.
- **P5. Failures come first.** A cheap failure is caught at once, a stop loses one node and not a run, and a node goes to a cheaper rung only after the defects that fail runs regardless of model are fixed.
- **P6. A planner never edits what it is held to.** It names criteria by id and paths inside the fence. It never rewrites a criterion's text and never widens the fence. What it does when one of them is wrong is [OQ-PT3](#OQ-PT3) and [OQ-PT4](#OQ-PT4). Who may supersede a criterion at all is [`OQ-CR2`](./criterion-repair.md#OQ-CR2).

---

## 3. The words

[Item graphs](./tillr-item-graphs.md#the-words) defines workflow, stage, graph, node, node run, child session and attempt, and this document uses them unchanged. A **registered check** is an entry of the tracked `config/tillr/commands.json` that a command node runs; that file comes from [the command-node block](./tillr-item-graphs.md#add-command-nodes-that-run-a-registered-program). A **seat** is tillr's existing name for a role, mapped to a backend by the backend policy (`tools/tillr-item/item/seat.go`). The terms below are coined here.

| Term | Meaning | What it is not |
| :--- | :--- | :--- |
| **Kind of work** | What a node is for, named by the thing that checks it: plan, design ruling, conflict repair, implement, test-fix, prose, mechanical, scout, judge, command | Not a model, and not a size. Each kind maps to one seat ([Section 4](#4-kinds-of-work)) |
| **Slice** | One writing node's share of an item: a write scope inside the fence, the criterion ids it serves, one registered check, a kind and a size | Not an item, and it has no place in the queue. Not a trunk commit: the item still lands as one |
| **Plan** | The planning node's one output, of type `graph.proposal`: it replaces the stage `work` with `split-build@v1` and a list of slices | Not a document that is landed ([OQ-PT7](#OQ-PT7)) |
| **Ladder** and **rung** | A ladder is the ordered list of backends a seat climbs when its node's check fails; a rung is a position on it | Not failover. Failover moves to another backend when a backend breaks or runs out of quota, at the same rung |
| **Seam cost** | What a new node pays before its first useful edit: its first turn and the orientation it repeats | Not the node's work |
| **Planning threshold** | A deterministic rule that decides whether a build item is slotted into the planning workflow | Not a judgment any model makes |

---

## 4. Kinds of work

Each kind is defined by what the work needs and what can tell whether it is right. The seat is where the policy places it.

| Kind | What it needs | What checks it | Evidence | Seat |
| :--- | :--- | :--- | :--- | :--- |
| **Plan** | Judgment over the whole item; reads the tree; writes a small JSON file | The plan checker ([Section 5.5](#55-deterministic-validation)), which is deterministic | Sol orientation median $0.22; the planner row is Sol at high ([`OQ-CP1`](./coordinator-pyramid.md#decision-ledger)) | `plan` *(new)* |
| **Design ruling** | Judgment that no test can check; writes prose that changes what gets built | The owner, or a judge | Fable median $1.41 a run; design runs are about 32% of spend | `design` |
| **Conflict repair** | A mechanical fix to an item's ruling: widen a fence, reorder, fix a stale path, release | The next build run and the documentation checks | All 13 design conflicts triaged on 2026-11-16 were mechanical | `design`, at a lower rung ([Section 7.4](#74-an-example-placement-of-the-four-models)) |
| **Implement** | Code against criteria, mostly in one package | The package's registered check, then the gate | Edit and diagnose are 54% of Sol cost | `build` |
| **Test-fix** | Read a failing check's report and repair the cause | The same check, run again | Test is 21.8% of cost and 33% of wall time | `fix` *(new)* |
| **Prose** | READMEs and documents written to the prose guide | `vantage-check` and the documentation tests, which check structure but not substance | Not separated by the classifier | `prose` *(new)* |
| **Mechanical** | Apply a settled edit or regenerate a copy | A command and a diff check; usually no model at all | The pack mirror doubles every tillr change ([`OQ-SH2`](./tillr-selfhosted-build.md#OQ-SH2)) | `mechanical` *(new)*, or a command node |
| **Scout** | Read and report; change nothing | The read-only rule (head unchanged, worktree clean) and its output existing | A Sol run that never edits costs about $0.20 | `scout` |
| **Judge** | An independent verdict on criteria no check covers | Nothing: the judge is the check | [The judge-before-gate block](./tillr-judge-and-lifecycle.md#route-an-opted-in-item-through-a-judge-before-the-gate) | `review` |
| **Command** | No model | The exit code | $0 | none: a command node |

A conflict repair stays on the `design` seat, because the loop routes a conflict to that seat today. It reaches the lower rung only when the conflict record names a mechanical kind ([Section 7.3](#73-the-escalation-ladder-and-its-cap)).

---

## 5. The planning node

### 5.1 Where it sits

`plan-then-build@v1` is a tracked workflow of two stages, and both edges leave `plan` whatever its outcome:

```text
plan   (agent, seat plan,  read_only, may_replace: work → [split-build@v1], timeout 600 s)
  │ pass ─┐
  │ fail ─┴─► work   (agent, seat build, write)    the single node every item runs today
```

The planner proposes, as [`D-IG3`](./tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection) permits, that `work` (a later node that has not started) be replaced by `split-build@v1` with the slice list as its argument. Four results are possible:

- An accepted proposal turns `work` into the slices' nodes.
- A refused proposal is recorded and the graph runs `work` unchanged, as [the proposal block](./tillr-item-graphs.md#add-a-planning-nodes-proposal-to-replace-a-later-node) already specifies (its fifth criterion).
- A planner that fails, times out, or writes no proposal also runs `work`.
- **Planning can make an item cheaper, but it can never stop one.**

### 5.2 What the planner reads

Staging gives the planner:

- the item's title, description, criteria (ids and text, read from the store) and fence;
- the design section the item's `body_ref` names;
- the names and descriptions of every registered check;
- the read-only tree at the item's base;
- the size table in [Section 6.2](#62-the-unit-of-a-node).

On an item's second attempt it also gets the first attempt's node list, each node's outcome, the failing check's report, and each slice's notes.

### 5.3 What the planner writes

One output, `plan`, of type `graph.proposal`. It is shown here as a sketch of shape, not a schema:

```json
{
  "target": "work",
  "workflow": "split-build@v1",
  "args": {
    "slices": [
      {
        "id": "store",
        "kind": "implement",
        "write_scope": ["tools/tillr-item/item/"],
        "criteria": ["c1", "c2"],
        "check": { "command_ref": "go-test", "flags": { "dir": "tools/tillr-item" } },
        "size": "m",
        "prompt": "Add the comment record and its sync path ...",
        "interface": "type Comment struct { ID, Doc, Anchor, Body string } ..."
      }
    ],
    "judge_criteria": ["c7"]
  }
}
```

- **Order is dependency.** Slices run in list order, because writers run one at a time ([`OQ-IG3`](./tillr-item-graphs.md#71-decision-ledger)). Each slice reads the `interface` and `notes` of every slice before it. A plan has no other dependency field.
- **Criteria are named by id only.** The planner never writes a criterion's text (P6). Each slice's brief gets the text from the store.
- **`judge_criteria`** lists the criteria that no registered check covers. Only these send the item to an evaluator ([Section 7.6](#76-tiers-of-review-and-judging)).

The slice list is a new kind of bounded workflow argument. [Item graphs](./tillr-item-graphs.md#31-named-versioned-graph-templates) allows three kinds today: a subset of the fence, a list of up to eight scout prompts that copies one read-only stage, and a registered command id. A list of up to six writing slices, each holding those same kinds of values, is the fourth (`D-PT2` in the [decision ledger](#decision-ledger)). An argument is still data: nothing in it reaches a shell, and it cannot change the exit condition or the evaluator.

### 5.4 The graph a plan becomes

`split-build@v1` expands each slice into a fixed chain, followed by one tail. Edges marked fail are followed only when their producer fails ([the exit-condition block](./tillr-item-graphs.md#add-an-exit-condition-that-decides-when-an-items-graph-is-done)):

```text
for each slice i, in order:
  i/build   (agent, seat by kind, rung 1, write_scope i)
  i/check   (command: the slice's registered check, read-only)
     └ fail ► i/fix    (agent, seat fix, rung 1; continues i/build's session when it may)
              i/check2 (command)
                 └ fail ► i/fix2   (agent, seat fix, rung 2: a fresh session)
                          i/check3 (command)
                             └ fail ► the item's attempt fails as node-failed
tail:
  sync      (command pack-sync, only when the fence includes the pack copy)
  full      (command: the gate's own check selection for the paths the candidate changed)
     └ fail ► full/fix (agent, seat fix, rung 2), then full/check2
  evaluate  (agent, seat review, read-only; only when judge_criteria is not empty)
  exit:     every slice's last check passed, full passed, a commit is required
```

- **The node count is bounded.** Six slices of up to six nodes each, plus up to five tail nodes, is 41 nodes, inside the cap of 100 ([`D-IG4`](./tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)). Nodes on edges that are not followed never start, so a clean slice runs two nodes.
- **An empty slice passes.** A slice whose check passes with nothing committed is recorded as an empty slice. That is not a failure.
- **The `full` check runs the gate's own checks before the gate**, so a fix is a node and not a whole new attempt. If the gate's result memory serves the identical tree, the gate's second run may cost nothing (not measured).

### 5.5 Deterministic validation

The plan checker runs in the one transaction that applies a proposal ([`D-IG3`](./tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)). It refuses a plan under a named rule, records the refusal, and the item runs `work`. The proposal block's own rules come first: the target, the workflow, bounded arguments, and the fence. Then these:

| Rule | Refused when |
| :--- | :--- |
| `plan-schema` | The output does not parse, has a duplicate key, or has a field the format lacks |
| `plan-slice-count` | There are fewer than 2 slices or more than 6. A one-slice plan is no split, so it should write no proposal |
| `plan-scope-outside-fence` | A write scope leaves the item's fence. The scope is expanded with the pack copy, as `fencePaths` does today |
| `plan-scope-empty` | A slice names no path |
| `plan-criterion-uncovered` | One of the item's criteria is in no slice's `criteria` and not in `judge_criteria` |
| `plan-criterion-unknown` | A criterion id is not one of the item's |
| `plan-check-unregistered` | A check names no entry of `commands.json`, or uses a flag that entry does not list |
| `plan-kind-unknown` | A slice's kind is not `implement`, `prose` or `mechanical` |
| `plan-size` | A size is not `s`, `m` or `l`; or every slice is `s`, which means too small to split |
| `plan-text-bound` | A prompt is over 2,000 characters, or an interface sketch is over 4,000 |

Write scopes may overlap. Slices run in order, so an overlap is ordered rather than a race, and refusing it would refuse the common case where every slice touches `docs/`.

### 5.6 Failure, escalation and re-planning

| What failed | What happens | Bound |
| :--- | :--- | :--- |
| The planner faults (not a clean failure) | It fails over to the next backend at the same rung, as any run does. If that fails too, `work` runs | The policy's `max_failovers` (1 today) |
| The plan is refused | `work` runs as one node; the refusal is on the item page | None needed |
| A slice's check fails | `i/fix` runs on rung 1 | One fix on each rung |
| The rung-1 fix fails its check | `i/fix2` runs on rung 2 | Two rungs for each slice (`D-PT3`) |
| The rung-2 fix fails its check | The attempt ends as `node-failed`, naming the slice | Ends the attempt |
| `full` fails | `full/fix` runs on rung 2, then the check runs again; a second failure ends the attempt | One fix |
| An attempt ends with no landing | The next attempt plans again on rung 2 of `plan`, with the failed attempt's reports | One re-plan ([OQ-PT6](#OQ-PT6)) |
| The re-planned attempt fails | The next attempt runs `work` alone on `build`'s top rung, then the item is held for the design seat | Per [OQ-PT6](#OQ-PT6) |

A re-plan is a new attempt, never a loop inside the graph. The graph stays cycle-free ([Section 5 of item graphs](./tillr-item-graphs.md#5-what-a-node-may-never-be)). The new attempt starts from the item's base: slices that passed in the failed attempt are paid for again, and that is the price of the cap.

### 5.7 When planning is not worth it

The **planning threshold** is evaluated in Go when an item's graph is first built. A build-seat item with no `workflow=` in its block is slotted into `plan-then-build@v1` when any of these holds:

- it has **5 or more criteria**;
- its fence spans **3 or more module directories**, counting each `tools/<name>/` and each other top-level directory, and leaving out `docs/`, `config/` and `yolo-packs/`;
- it has **an earlier attempt that edited files and did not land**.

An author overrides the rule either way, with `workflow="single-worker@v1"` or `workflow="plan-then-build@v1"`. Design items, the fixers for a broken trunk and for landing conflicts, and work on express lanes are never planned.

How good the rule is, tested against the history (measured; 85 build items, $237.84 of deduplicated recorded spend):

| Rule at first dispatch | Items selected | Share of spend | Mean spend per item: selected, others | Landed on the first run: selected, others |
| :--- | ---: | ---: | :--- | :--- |
| 5+ criteria, or 3+ modules | 56 | 81% | $3.44, $1.56 | 9 of 56 (16%), 12 of 29 (41%) |
| 6+ criteria, or 3+ modules | 40 | 57% | $3.41, $2.25 | 5 of 40, 16 of 45 |
| 4+ criteria, or 3+ modules | 75 | 97% | $3.08, $0.69 | 15 of 75, 6 of 10 |

The rule is a weak predictor of cost: it separates means by a factor of two. It is a better predictor of trouble, with a first-run landing rate of 16% against 41%. Criteria count alone barely discriminates, because 80 of 92 items have four or more. The proposed default is the first row. Who sets it is [OQ-PT8](#OQ-PT8).

### 5.8 One writer for each piece of state

| State | Its one writer |
| :--- | :--- |
| The item, its criteria, fence and place in the queue | The item utility, from the documents. Planning writes none of it |
| The plan | The planning node's run, as an output file the drain checks and records |
| The graph and its revisions | The drain, in the proposal transaction ([`D-IG3`](./tillr-item-graphs.md#22-in-fog-refinement-and-the-flattening-projection)) |
| A slice's commits | That slice's node run, on the item's lane, one writer at a time |
| A node's rung, outcome and usage | The drain, when the node settles |

---

## 6. Granularity

### 6.1 The honest trade

These are estimates. The model starts from a successful single Sol run of cost *C* and splits it into *k* slices, using the phase shares of [Section 1.2](#12-where-a-sol-runs-money-goes). It assumes:

- **Plan:** $0.35 (Sol at high).
- **Seam:** $0.17 per slice on Sol, $0.085 on Terra (one first turn plus half an orientation).
- **Work:** edit, diagnose, targeted tests and the rest, done with 15% cheaper turns because each slice's context is smaller.
- **Full suite:** in a command node, at no model cost.
- **Fix:** a Terra fix half the time.
- **Failure:** a failed cheap slice is run again in full on Sol.

| *C*, the single Sol run | Split, all on Sol | Split, Terra slices, none fail | 15% fail | 30% fail |
| ---: | ---: | ---: | ---: | ---: |
| $0.50 | $1.29 | $0.82 | $0.95 | $1.07 |
| $1.00 | $1.63 | $0.99 | $1.16 | $1.34 |
| $2.00 | $2.30 | $1.33 | $1.60 | $1.87 |
| $3.00 | $2.98 | $1.66 | $2.03 | $2.40 |
| $6.00 | $5.00 | $2.68 | $3.34 | $4.00 |

**The break-even item size** with three slices is *C* ≈ $1.00 if none fail, $1.30 at 15% failure and **$1.73 at 30%**. With two slices it is $0.86 and $1.43. Splitting while staying on Sol breaks even only near $3. A single unsplit item routed whole to Terra saves 50% minus its failure rate: 20% at 30%. So the trial needs no planner at all ([Section 10.1](#101-stage-0-measure)).

**What the table leaves out cuts both ways.**

- In favor: it ignores retries. The selected items landed first time in 16% of cases, and under a split a stop loses one node instead of a run. The worked examples in [Section 8](#8-worked-examples) include that.
- Against: it ignores turn inflation. If Terra needs 30% more turns, its price ratio is 0.65, not 0.5, and its 30%-failure break-even rises to about $2.60 (estimated).

### 6.2 The unit of a node

**A slice is one package's change with one check that covers it.** In practice that means one Go module directory and its tests, one document and its checks, or one regenerated copy.

| Size | Files it is expected to change |
| :--- | :--- |
| `s` | Up to 3 |
| `m` | Up to 8 |
| `l` | Up to 15 |

When a slice settles, the drain records how many files it actually changed beside the size it declared. That is calibration data, not a rule: no slice fails for exceeding its size.

The cap is six slices. With four or more, seam cost ($0.17 on Sol each) takes most of the saving on a $3 item ([Section 11](#11-alternatives-considered)). The smallest useful split is one package of code plus its documents.

### 6.3 How handoffs carry context

1. **The plan.** Each slice's brief holds the item's title, the text of its own criteria, its write scope, its prompt, and the whole slice list in summary. That way it knows what the other slices own.
2. **The interface sketch.** This is the planner's statement of the types, functions, flags or file shapes that a later slice depends on. Every later slice reads it. It is the contract between slices, and it is what lets slice 2 start without re-reading slice 1's package.
3. **Notes.** Every slice writes a `notes` output: what it did, what it changed from the plan, and what a later slice must know. Later slices and fix nodes read it from its producer ([`D-IG6`](./tillr-item-graphs.md#33-producer-bound-artifacts-exit-conditions-and-the-evaluator-slot)).
4. **Session continuation.** A fix node on the same backend as the node it repairs continues that node's session, with the check's report as the next message. The mechanism is the resume already built for pause and continue (`637458006`). It applies only while that session's context is under 150k tokens, about 55% of the window. Otherwise, and always on a higher rung (a different model has none of the cache), the fix starts fresh and reads the notes, the diff and the report.
   - A warm continuation costs about one turn: roughly $0.03 at 150k on Sol, against $0.17 for a fresh start (estimated).
   - It is recorded on the fix node's run as continuing the earlier run (`D-PT7`).

A planned item still uses child sessions inside a node where [`D-IG12`](./tillr-item-graphs.md#34-several-nodes-or-one-node-with-child-sessions) says they are enough. A split is for parts that must be planned, fenced or judged apart, and a cheaper seat is "planned apart".

---

## 7. Routing is policy, not code

### 7.1 From kind of work to seat to model

The item and the workflow name a seat. The policy maps the seat to a ladder of backends, and each backend row names its model and effort, as it does today. This sketch adds `ladders` to the shape of `config/tillr/backend-policy.bedrock.json`:

```json
{
  "seats":   { "plan": "pi-sol-high", "design": "pi-designer", "build": "pi",
               "fix": "pi-terra", "prose": "pi-terra-low", "mechanical": "pi-luna-low",
               "scout": "pi-terra-low", "review": "pi-designer-high" },
  "ladders": { "plan": ["pi-sol-high", "pi-designer"],
               "design": ["pi-designer"],
               "fix": ["pi-terra", "pi"],
               "prose": ["pi-terra-low", "pi"],
               "mechanical": ["pi-luna-low", "pi-terra-low"] },
  "backends": {
    "pi-terra": { "adapter": "tools/tillr-run/backends/pi/run-pi",
                  "model": "amazon-bedrock/global.openai.gpt-6.1-terra", "effort": "medium" }
  }
}
```

- **Rung 1 is the seat's own backend.** A ladder's first entry must equal it, and the policy check refuses one that does not. A seat with no ladder has one rung.
- **A stage may set `rung`.** It is an integer from 1, defaulting to 1. A rung past the ladder's end means its last rung.
- **Ladders and failover stay apart.** A seat whose value is a list of backends keeps its meaning: failover for a broken or exhausted backend, at the same rung (`D-PT3`).
- **The new seats join `seat.go`'s closed vocabulary:** `plan`, `fix`, `prose` and `mechanical` (`D-PT1`). The pi-subagents seat profiles are a separate vocabulary and do not change.

### 7.2 Effort

Effort is the reasoning the model spends on every output token. Output is 13.6% of Sol's cost (measured), so effort moves cost less than the choice of model does.

| Kind | Effort | Why |
| :--- | :--- | :--- |
| Plan | high | A missed seam costs a whole attempt; the planner row is Sol at high ([`OQ-CP1`](./coordinator-pyramid.md#decision-ledger)) |
| Judge | high | Only verify and judge stages earn high (the work rules) |
| Design, implement, test-fix | medium | Today's default |
| Prose, mechanical, scout | low | Mechanical by the work rules' own definition |

### 7.3 The escalation ladder and its cap

- **What triggers it.** A rung is climbed only when a node's registered check fails, which is a fact about the work. A broken or exhausted backend fails over at the same rung instead, which is a fact about the backend.
- **The cap.** Two rungs for each slice, one fix for the full check, and one re-plan for each item ([Section 5.6](#56-failure-escalation-and-re-planning)). An attempt can spend at most its plan plus six slices times one build and two fixes, plus three tail nodes. The spend ceiling still applies ([Section 9](#9-work-already-in-flight)).
- **A conflict repair starts at rung 1, Sol at high, only when the conflict record names a mechanical kind**: fence widening, ordering, a stale path, or a release. Any other conflict starts at Fable. Recording that kind is part of [stage 3](#104-stage-3-the-full-split).

### 7.4 An example placement of the four models

Prices are dollars per million tokens: input / output / cache read / cache write ([Section 1.4](#14-the-four-models-prices)).

| Kind | Rung 1 | Rung 2 | Why |
| :--- | :--- | :--- | :--- |
| Plan | **Sol, high**: 2 / 10 / 0.10 / 2.5 | Fable, medium (re-plan only) | The plan's output is checked deterministically, so judgment has to be good but not the best. Fable's orientation alone ($2.12 median) costs more than most items, so it is kept for the attempt that already proved the item hard |
| Design ruling | **Fable, medium**: 10 / 50 / 0.25 / 12.5 (written at 20) | none | No test checks a ruling, so P1 puts it on the top rung. Fable 5.1's scores stand in for 5.2: arch 84.0, scout 90.7 |
| Conflict repair | **Sol, high** | Fable, medium | All 13 triaged conflicts were mechanical, and Fable's tokens cost 0.226 of themselves at Sol's prices |
| Implement | **Sol, medium**, with Terra trialled by name | none: Sol is the top | Sol impl 74.3 is measured; Terra has no score. Routing implement to Terra waits on the trial ([OQ-PT2](#OQ-PT2)) |
| Test-fix | **Terra, medium**: 1 / 5 / 0.05 / 1.25 | Sol, medium | Narrow context and a check that decides: P1's best case. Break-even is 50% escalation |
| Prose | **Terra, low** | Sol, medium | The checks are structural only, so a cheap rung is safe for form and weak on substance. A judge covers substance where a criterion asks for it |
| Mechanical | **a command**, else **Luna, low**: 0.1 / 0.5 / 0.01 / 0.125 | Terra, low | Luna's own annotation, "single-turn infilling only", describes this kind exactly. Nothing else goes to Luna |
| Scout | **Terra, low** | none | Luna's scout score is 60.1, "shallow search only", which rules it out |
| Judge | **Fable, high** | none | A different family from the OpenAI-family writers it judges, and used only for criteria no check covers |

This puts Luna only behind a deterministic check on single-turn edits. That keeps the October verdict against Luna as a *worker* and narrows it, rather than overturning it ([OQ-PT1](#OQ-PT1)).

### 7.5 How the owner learns a placement is wrong

Visibility comes first, and tillr records facts, not statistics. The owner's ruling of 2026-11-15 puts comparison statistics in a separate project ([the seat-share item](../tillr/roadmap.md#split-a-seats-work-between-backends-by-a-simple-share)), and that ruling holds here: per-kind rates are computed there, not in tillr (`D-PT5`).

- **What tillr records on each node run:** its seat, rung, backend, model and effort; whether it was an escalation and from which run; its check's outcome; its usage; its declared size and the files it actually changed; and whether it continued a session.
- **What the item page shows:** the nodes, each with its seat, rung and outcome ([the node-view block](./tillr-item-graphs.md#add-an-items-nodes-and-their-runs-to-the-item-page)), and the plan with any refusal. It shows usage, never a price ([the observatory ruling](./observatory-redesign.md)).
- **The rule the owner's project applies:** a cheap rung pays while its escalation rate stays below one minus its price ratio times its turn inflation. For Terra under Sol at the same turns that is 50%; at 1.3 times the turns it is 35%. A kind whose rung-1 escalation rate crosses that line is placed wrong, and the fix is one line of the policy.

### 7.6 Tiers of review and judging

1. **A slice's own check**, a registered command, on every slice, at no model cost.
2. **The full check**, the gate's own selection, run once in the lane at no model cost.
3. **The gate**, once, as today.
4. **An evaluator, only for `judge_criteria`.** That means criteria the planner marked as having no registered check, or an item that opts into a judge ([the evaluator block](./tillr-item-graphs.md#route-a-workflows-evaluator-node-through-the-judges-intake)). A Fable judge costs about its orientation, $2 (estimated from Fable's $2.12 median), so it is never placed on every item.

---

## 8. Worked examples

Each "before" figure is measured from the logs. Each "after" figure is estimated with the model in [Section 6.1](#61-the-honest-trade), assuming that the stops and failures the item actually had happen again and that each costs one slice instead of one run.

| Item | Kind | Before (measured) | After, estimated: Sol only / Terra, none fail / Terra, 30% fail | What carries the change |
| :--- | :--- | ---: | :--- | :--- |
| `item-agent-default-bmad-superpowers-skills` | Documents-only ruling, six runs | $4.42 | $3.96 for all three | Conflict repair on Sol at high. Planning adds nothing |
| `item-jl-refuse-clone-ids` | Small change in one Go package, one run | $0.46 | $0.46 / $0.23 / $0.37 | The whole item on Terra. It is below the threshold and never planned |
| `item-comments-first-delivery` | Seven criteria over three Go modules, seven runs, landed by hand | $15.04 | $8.36 / $5.29 / $7.07 | A stop costs a slice; Terra slices |
| `item-obs2-stop-says-what-and-next` | Failed again and again, six runs | $11.77 | $7.89 / $4.12 / $6.33 | A stop costs a slice; Terra slices |
| `item-design-gate-rework` | One Fable design run | $40.07 | $11.65 for all three | Each context token written to the cache once. Routing adds nothing |

**The documents-only ruling.** This is the BMAD skills item.

- **Runs.** a1 Sol $0.15; a2 Fable $1.41 (the ruling, orientation $1.14); a3 Sol $0.22 (blocked on a stale upstream path); a4 Fable $0.70 (fixed the path and rewrote criterion c2 on its own authority); a5 Sol $0.42 (blocked on yolo's pack loader); a6 Fable $1.52 (sent the item to the owner).
- **After.** The path fix is a mechanical conflict: Sol at high, about $0.24, or $0.70 × 0.226 × 1.5 for high effort. The total becomes $3.96.
- **Lesson.** The item's real cost is a capability outside its fence, and a planner cannot see that before trying. The two build runs that found the two blockers cost $0.64, about what a planner would. Run a4's rewrite of c2 is the case [OQ-PT3](#OQ-PT3) and [`OQ-CR2`](./criterion-repair.md#OQ-CR2) exist for.

**The small Go change.**

- **Before.** One run: $0.46, 246 s, 29 turns, orientation $0.18, tests $0.11. A similar item, `item-fresh-store-sync-preview`, cost $0.28.
- **After.** On Terra, $0.23, or $0.37 at a 30% failure rate. A plan would cost $0.82 here, so the threshold keeps this item out of planning.

**The multi-package change.**

- **Before.** a1 Sol $0.14; a2 Fable $1.24 (a ruling that landed); a3 Sol $2.89 (killed after 113 turns); a4 killed after 15 s; a5 Sol $5.59 (222 turns, 32.9M tokens, largest context 255k, 1,267 s of tests, stopped on a heartbeat flake); a6 `claude-designer` $0.48; a7 Sol $4.70 (187 turns, largest context 255k). The loop stopped before a7 committed, and its work was landed by hand as `895409b38`.
- **After.** The clean cost *C* = $4.70 is a7's, measured, with three slices and the two stops (a3 and a5) each costing one slice. The $1.86 of rulings is unchanged.

**The item that failed again and again.**

- **Before.** a1 $1.25 (killed); a3 $0.39 (killed); a4 $0.96; a5 $1.31; a6 $0.25 (killed); a7 $7.61 (152 turns, 18.2M tokens, $4.02 of it diagnosing, never committed).
- **After.** *C* = $4.00 (an assumption: no run of this item finished), with two slices and three stops each costing a slice. The commit-before-ending failure of a7 costs one slice once nodes must leave a clean worktree.

**The large design run.**

- **Before.** 48 turns, 1,398 s, largest context 256k. It wrote 1.695M tokens to the cache, $33.90 at the one-hour rate, and that was 85% of the run.
- **After.** If each token were written once (256k), the run costs $5.12 for writes, $1.89 for reads (6.1M plus the 1.44M that turn from writes into reads) and $4.64 for output: **$11.65**. With five-minute writes it would be $9.73.
- **Lesson.** Neither model routing nor planning touches this. It is lever 2 ([Section 1.6](#16-what-the-history-means-three-levers-ranked)).

**Assumptions behind every "after" figure.**

- Terra does the same turns as Sol.
- Seams cost what [Section 6.1](#61-the-honest-trade) says.
- A failed cheap slice is detected at its end and redone in full on Sol.
- The full suite fails half the time on the first run.
- Stops recur as they did.

**At a 30% cheap-model failure rate**, the two planned items still come out 46–53% under their measured history. Most of that comes from stops costing a slice and not a run, not from Terra. Against a single clean Sol run, the split on Terra at 30% failure costs about 8% more on `item-comments-first-delivery` ($7.07 against $6.56) and 58% more on `item-obs2-stop-says-what-and-next` ($6.33 against $4.00). Planning pays where items fail, which is where 81% of build spend is.

---

## 9. Work already in flight

| In flight | How this design meets it |
| :--- | :--- |
| **Ten lanes** | Planning adds nodes, not lanes. One item still holds one lane ([`D-IG8`](./tillr-item-graphs.md#42-worktree-sequencing-at-single-lane-concurrency)). A planned item holds its lane for more nodes, but each node is shorter. More sessions at once against Bedrock may throttle, which is not measured |
| **The gate** | Unchanged and run once. The `full` command runs the gate's own selection first, so a red result becomes a fix node rather than a new attempt |
| **[The spend ceiling](../tillr/roadmap.md#stop-dispatching-when-the-spend-ceiling-is-reached)** | Sums every node run of an item, plan included. It cannot see child sessions until [the observatory block](./observatory-redesign.md#record-child-sessions-and-the-criteria-a-node-says-it-has-finished) lands, and [the killed-run item](../tillr/roadmap.md#add-the-spend-of-killed-runs-to-the-record) fixes the missing usage |
| **[Commit before ending](../tillr/roadmap.md#prevent-a-build-run-from-ending-with-passing-work-uncommitted)** | 28% of Sol runs that edited ended uncommitted. A cheaper rung would carry the same defect and look like a weaker model, so stage 2 waits on this item (P5) |
| **The test-suite wait** | Test is 33% of wall time. `tools/tillr-drain`'s suite took 63.4 s on an idle machine (rolling run, 2026-11-17 02:36 UTC), and one run spent 1,267 s in tests. Slices run their own package's check, and the full suite runs once as a command, so no model session sits through it. Ten lanes compete for CPU, which is not measured |
| **[The design seat's quota](../tillr/roadmap.md#route-design-work-past-a-design-seat-that-is-out-of-quota)** | Planning runs on Sol and adds no design load. The re-plan rung and the judge use Fable. 13 of 19 `claude-designer` runs exited on the weekly limit |
| **Resumable sessions** | Pause and continue (`637458006`) is the mechanism a warm fix continuation reuses. Resuming mid-graph (`6ee7e1d04`) is why a stop costs one node |
| **[Express lanes](../tillr/roadmap.md#run-owner-facing-work-on-lanes-outside-the-cap)** | Never planned, so work the owner is waiting on pays no planning delay |

---

## 10. The staged path

Each stage lands alone and is measured before the next one starts. Proposed items are written as plain prose here, not as queue directives. They become blocks once the owner has ruled.

### 10.1 Stage 0: measure

This stage can run before the ramp.

**Trial Terra on five named build items.** This is a policy edit, not an item, and it takes hours. Add a `pi-terra` backend row, Terra 6.1 at medium. Then name five single-package items in the policy's `overrides`.

- An override applies to every attempt of its item, retries included (`firstBackend` in `tools/tillr-drain/drain/policy.go`). So the trial removes an item's override after its first attempt that does not land, and the retry runs on Sol. That is a ladder of one rung, worked by hand.
- Compare each item's Terra usage, turns and outcome against the history of similar Sol items.
- This depends on [OQ-PT2](#OQ-PT2).

**A run's recorded usage is its own.** 49 runs carry exactly another run's tokens and cost, $133.65 in all, and every rate built on run records inherits the error. Criteria:

- c1: a run whose session never started records zero tokens and zero cost;
- c2: no two runs record the same usage unless one continues the other's session;
- c3: a test kills a run in its first second and asserts that its usage is its own.

**Find why build runs write their context to the cache twice.** This is a research item for the scout or design seat, and its output is a document in `docs/research/`. Sol runs wrote 2.2 times the sum of their largest contexts (an estimated $69), and the largest Fable run wrote 6.6 times its largest context. Criteria:

- c1: name the cause, measured on at least three logs;
- c2: price a fix;
- c3: say whether the one-hour retention the matt pack sets is still worth it for runs that are never resumed, given that resumed designer sessions need it.

### 10.2 Stage 1: plan, then build with the checker, all on Sol

Stage 1 proves the split's overhead and its effect on stops with no change of model. It needs eight queued item-graph blocks:

- `item-ig-workflow-check`, `item-ig-command-node`, `item-ig-node-write-scope`, `item-ig-node-outputs`, `item-ig-exit-condition`;
- `item-ig-stage-names-workflow`, `item-ig-propose-replacement`, `item-ig-view-nodes`;
- plus `item-ig-seat-default-workflow` for the threshold.

**Add a plan seat and a workflow whose planner can split a build item.**

- c1: `plan` joins the seat vocabulary, and the policy maps it.
- c2: the workflow format accepts a slice-list argument of 2 to 6 entries, each with a write scope, criterion ids, a registered check, a kind, a size, a prompt and an interface sketch.
- c3: `plan-then-build@v1` and `split-build@v1` are tracked and pass the workflow check.
- c4: every rule of [Section 5.5](#55-deterministic-validation) refuses its case under its own name, with a fixture for each.
- c5: a loop test with stub harnesses runs a three-slice plan to one landed commit, and a refused plan to one `work` run.

**Register the checks a slice may name.**

- c1: `commands.json` holds a package test for each Go module, `vantage-check` on named files, the documentation tests, `pack-sync`, and the gate's own selection for a candidate's changed paths.
- c2: a test asserts that each entry's argv runs and that a failing fixture exits 1.

**Slot build items over the planning threshold into the planning workflow.**

- c1: the settings file holds the rule of [Section 5.7](#57-when-planning-is-not-worth-it) and its numbers.
- c2: an item's `workflow=` overrides it either way.
- c3: design items, the fixers and express lanes are never planned.
- c4: the item page says whether the rule or the author chose the workflow.

### 10.3 Stage 2: route one kind, test-fix, with a ladder

This stage needs stage 1 and [commit before ending](../tillr/roadmap.md#prevent-a-build-run-from-ending-with-passing-work-uncommitted).

**Add escalation ladders to the backend policy.**

- c1: `ladders` maps a seat to an ordered list whose first entry is the seat's backend, and the check refuses any other.
- c2: a stage's `rung` picks the entry, and a rung past the end means the last one.
- c3: each node run records its rung and the run it escalated from.
- c4: failover still walks the seat's failover list at the same rung, and a test keeps the two apart.

**Run the fix seat on Terra, escalating to Sol.**

- c1: the `fix` seat exists, and `split-build@v1`'s fix stages use rungs 1 and 2.
- c2: a fix on the same backend continues the repaired session under the 150k-token bound, and records that it did.
- c3: the item page shows each fix with its rung and outcome.

### 10.4 Stage 3: the full split

Only after stage 2's records show test-fix escalating less than its break-even ([Section 7.5](#75-how-the-owner-learns-a-placement-is-wrong)):

- **Route prose and mechanical slices to their own seats**, `prose` on Terra and `mechanical` on Luna, each behind its registered check. A slice under `tools/tillr-*` follows [OQ-PT5](#OQ-PT5).
- **Judge only the criteria no check covers.** This needs `item-ig-evaluator-node` and `item-jl-judge-before-gate`.
- **Re-plan a failed attempt on the design model**, with the cap ruled in [OQ-PT6](#OQ-PT6).
- **Record a conflict's kind**, so a mechanical conflict starts at Sol at high.
- **Run implement slices on Terra by default**, if the trial and stage 2 support it. That is one line of the policy.

### 10.5 Before the ramp, and what not to do

- **Hours, before the ramp:** the Terra trial (if [OQ-PT2](#OQ-PT2) says so), filing the usage-duplication item, and filing the cache-write investigation.
- **Days, after the ramp:** stage 1 (eight item-graph blocks and three items), then stage 2.
- **Not before the ramp:**
  - switching any seat wholesale to Terra or Luna, which would confound the ramp's own measurements;
  - turning planning on;
  - parallel writers;
  - changing the cache retention, which resumed designer sessions depend on.

---

## 11. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Route whole items by size** to cheaper models | **Rejected as the mechanism; kept as the stage-0 trial.** Size predicts cost weakly (a factor of two, [Section 5.7](#57-when-planning-is-not-worth-it)). It cannot put a plan, an implementation and a test-fix of one item on different rungs, and it gives no early check, so a cheap model's failure costs a whole run. A one-node item is still placed by its kind |
| **One session that switches model mid-run** | **Rejected.** A new model has none of the old one's cache, so a switch writes the whole context again: about $0.24 at a median 97k context on Sol, and about $1.94 on Fable at the one-hour rate. tillr cannot plan, fence or count what a harness switches by itself, and a node is one invocation on one route ([`D-IG12`](./tillr-item-graphs.md#34-several-nodes-or-one-node-with-child-sessions)) |
| **Many tiny nodes** | **Rejected; capped at six slices.** Each node pays $0.17 of seam on Sol, and orientation is repeated; ten nodes would cost $1.70 in seams. Every handoff loses context that notes only partly restore. The 100-node cap is a bound for safety, not a target |
| **Cheaper child sessions inside one node**, for example the unlisted `luna-worker` pack | **Rejected for routing; allowed inside a node.** Their spend is not recorded (measured), tillr cannot fence or check them apart, and the Luna-worker verdict stands |
| **Planning on Fable** | **Rejected as the default; kept as the re-plan rung.** Fable's median orientation, $2.12, is more than most build items cost in total |
| **Planning every build item** | **Rejected.** A plan costs more than a $0.46 item ([Section 8](#8-worked-examples)). The threshold keeps small items out |

---

## 12. What this design does not do

- It does not split one item into several items, and does not change the queue, the fence, the gate or the landing (P3).
- It does not let any node edit criteria or widen the fence (P6).
- It does not compute rates or prices in tillr ([Section 7.5](#75-how-the-owner-learns-a-placement-is-wrong)).
- It does not route design rulings below Fable, except mechanical conflicts.
- It does not run writing slices in parallel ([`OQ-IG3`](./tillr-item-graphs.md#71-decision-ledger)).
- It does not touch the pi-subagents seat profiles. They govern child sessions, and this design governs nodes.

---

## 13. Risks

| Risk | Mitigation |
| :--- | :--- |
| Terra takes many more turns, so its real price ratio is near 1 | The stage-0 trial measures turns before any seat moves. The owner's rule in [Section 7.5](#75-how-the-owner-learns-a-placement-is-wrong) includes turn inflation |
| A cheap slice passes its check but is wrong in ways the check misses | P1 allows only kinds whose check decides. Prose is weak here, so a judge covers substantive criteria. The gate still runs |
| The planner splits along the wrong seams, so slices fight | Interface sketches and notes; one re-plan on Fable; then one node on Sol. Refusals and re-plans are visible on the item page |
| Seams cost more than estimated | Stage 1 runs on Sol only and measures seam cost before any routing |
| A cheap slice breaks the drain's own code and stops the loop for everyone | [OQ-PT5](#OQ-PT5); the gate; a stopped loop is today's recovery |
| Duplicated usage records make every rate wrong | The stage-0 item comes first |

---

## 14. Rulings this builds on

| Ruling | Date | Does it still hold? |
| :--- | :--- | :--- |
| [`OQ-SP5`](./seat-profiles.md#decision-ledger): on Bedrock only Sol and Luna were worth encoding | 2026-10-20 | **Asked again** in [OQ-PT1](#OQ-PT1). It predates any per-kind check, and Terra was not considered |
| [`OQ-SP6`](./seat-profiles.md#decision-ledger), the Luna-worker verdict: "replace, do not keep" | 2026-10-20 | **Holds** for workers and scouts. This design uses Luna only for single-turn edits behind a check, which is its annotated strength |
| [`OQ-CP1`](./coordinator-pyramid.md#decision-ledger): the planner row is Sol at high | 2026-10-21 | **Holds.** The `plan` seat's rung 1 |
| [`D-IG3`](./tillr-item-graphs.md#71-decision-ledger), [`D-IG4`](./tillr-item-graphs.md#71-decision-ledger), [`D-IG9`](./tillr-item-graphs.md#71-decision-ledger), [`D-IG12`](./tillr-item-graphs.md#71-decision-ledger) and [`OQ-IG3`](./tillr-item-graphs.md#71-decision-ledger) | 2026-11-15 | **Hold.** Planning is a proposal; repairs are nodes; nothing loops; writers run one at a time |
| Comparison statistics live in the owner's own project | 2026-11-15 | **Holds** (`D-PT5`) |
| The observatory shows usage, never a price | 2026-11-15 | **Holds** |
| The one-hour cache retention (`c5f4a82f7`) | 2026-11 | Unchanged here. The stage-0 research asks whether runs that are never resumed need it |

---

## Decision Ledger

These are the designer's decisions of 2026-11-16, made so that the design is complete. Each is open to the owner's overturn. None is built.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D-PT1 | Each kind of work is a seat. The closed seat vocabulary gains `plan`, `fix`, `prose` and `mechanical` | 2026-11-16 | [Section 7.1](#71-from-kind-of-work-to-seat-to-model) | — |
| D-PT2 | The slice list, 2 to 6 entries, is a fourth kind of bounded workflow argument | 2026-11-16 | [Section 5.3](#53-what-the-planner-writes) | — |
| D-PT3 | Ladders are a policy key apart from failover lists; two rungs for each slice | 2026-11-16 | [Section 7.3](#73-the-escalation-ladder-and-its-cap) | — |
| D-PT4 | The planner is the first node of `plan-then-build@v1`; a refused or failed plan runs `work` | 2026-11-16 | [Section 5.1](#51-where-it-sits) | — |
| D-PT5 | tillr records per-node facts; rates are computed in the owner's project | 2026-11-16 | [Section 7.5](#75-how-the-owner-learns-a-placement-is-wrong) | — |
| D-PT6 | A re-plan is the next attempt, never a cycle inside the graph | 2026-11-16 | [Section 5.6](#56-failure-escalation-and-re-planning) | — |
| D-PT7 | A same-backend fix continues the session it repairs while that session's context is under 150k tokens | 2026-11-16 | [Section 6.3](#63-how-handoffs-carry-context) | — |

---

## Open Questions

1. 💬 **OQ-PT1: Should cheaper models than Sol do build work at all?**

   The owner opens the backend policy to put Terra on test fixes. In October he ruled that on Bedrock only Sol and Luna were worth setting up, and that Luna should not be a worker. Terra was never considered, and no check stood between a cheap model and the trunk then. Now every slice is checked by a command the moment it finishes, so a failed cheap fix costs a few cents and goes up to Sol.

   - **A — Keep the October ruling.** Every build node stays on Sol. Planning still pays where items fail, and the bill moves only by that.
   - **B — Cheaper models behind a check.** Terra fixes tests, writes prose and scouts, and climbs to Sol when its check fails. Luna gets only one-turn edits that a command checks. Sol still writes the code.
   - **C — B, and Terra writes code too**, before any trial has measured it.

   <!-- vantage: question id=OQ-PT1 leaning="Cheaper models behind a check (B): the check is what the October ruling lacked, and B keeps its Luna verdict for every seat that has no check." -->

   _Leaning:_ Cheaper models behind a check (B): the check is what the October ruling lacked, and B keeps its Luna verdict for every seat that has no check.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-PT2: Should Terra be tried on five real items before the ramp to ten lanes?**

   The owner is about to raise the loop to ten lanes. A trial needs one new backend row in the policy and five item ids under its `overrides`. An override holds for every attempt, so an item that fails on Terra is moved back to Sol by deleting its line. No Terra run has ever been measured here, so every saving in this proposal is a guess until one is.

   - **A — Before the ramp.** About an hour of setup. Five items tell us Terra's turns and failures while the loop is still small enough to watch.
   - **B — After the ramp.** The ramp's own numbers stay clean, and every routing decision waits until then.
   - **C — Only in stage 2**, on test fixes, inside the full ladder machinery.

   <!-- vantage: question id=OQ-PT2 leaning="Before the ramp (A): it is the only measurement available in hours, and five named items cannot confound ten lanes' throughput." -->

   _Leaning:_ Before the ramp (A): it is the only measurement available in hours, and five named items cannot confound ten lanes' throughput.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-PT3: What should a planner do when it finds a criterion that cannot be met as written?**

   A planner reads the BMAD item and finds that criterion c2 names a path upstream no longer has. It may never rewrite the criterion. Today a build run discovers this, blocks, and the design seat fixes the criterion; in the observed pass of 2026-11-17 (UTC) the design seat rewrote it on its own authority. Who may change a frozen criterion at all is already an open question in the criterion-repair design.

   - **A — Refuse the plan.** The item runs as one build node, which will hit the same wall.
   - **B — Report it.** The planner writes a finding instead of a plan, and the item goes to the design seat as a conflict before any build is paid for. The design seat's power to change the criterion is whatever criterion repair rules.
   - **C — The planner proposes new criterion text** for the design seat to accept.

   <!-- vantage: question id=OQ-PT3 leaning="Report it (B): it costs one plan instead of a failed build, and it leaves criterion authority where criterion repair puts it." -->

   _Leaning:_ Report it (B): it costs one plan instead of a failed build, and it leaves criterion authority where criterion repair puts it.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-PT4: What should a planner do when the work needs a file outside the item's fence?**

   The planner for a delivery item sees that the loop's adapter in `tools/tillr-loop/` must change too, and that path is not in the fence. A plan that names it is refused. Today the build run finds this at the end and stops. All 13 conflicts triaged on 2026-11-16 were of this mechanical sort.

   - **A — Refuse and build anyway.** The item runs as one node and probably stops at the fence, as today.
   - **B — Report it.** The planner writes a fence finding, and the item goes to the conflict path, where Sol at high widens the fence, before any build runs.
   - **C — Let the planner widen the fence itself**, within a list the item's block allows.

   <!-- vantage: question id=OQ-PT4 leaning="Report it (B): the fence keeps one owner, and the widening is the cheap mechanical repair the triage showed these conflicts are." -->

   _Leaning:_ Report it (B): the fence keeps one owner, and the widening is the cheap mechanical repair the triage showed these conflicts are.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-PT5: May a cheaper model change the loop's own code?**

   A planned slice edits `tools/tillr-drain/`, the program that runs every lane. Its check is that module's test suite, which took 63 seconds on an idle machine. If a cheaper model's change passes the suite but breaks the loop, every lane stops until someone repairs it by hand.

   - **A — Yes.** The suite and the gate are the defence, as they are for Sol.
   - **B — Not yet.** Any slice that writes under `tools/tillr-` or `yolo-packs/tillr/` starts on its seat's top rung, until stage 2's records show the cheaper rung is safe.
   - **C — Yes, but always with a judge**, which costs about $2 per item.

   <!-- vantage: question id=OQ-PT5 leaning="Not yet (B): a broken loop costs every lane, and the records that would justify A do not exist yet." -->

   _Leaning:_ Not yet (B): a broken loop costs every lane, and the records that would justify A do not exist yet.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-PT6: How many times may an item be planned again before a person looks at it?**

   A planned item's second slice fails its check on Terra, then on Sol, and the attempt ends. The next attempt can plan again, using the first attempt's reports, but it starts from the item's base, so the slices that passed are paid for again.

   - **A — Once, on Fable.** About $2 more. If that attempt fails, one plain Sol run, then the item is held for the design seat.
   - **B — Never.** A failed attempt falls back at once to one plain Sol run.
   - **C — Twice.** The held state comes later, and every item has a larger worst case.

   <!-- vantage: question id=OQ-PT6 leaning="Once, on Fable (A): a plan that failed is evidence the item is harder than it looked, which is exactly when the stronger model pays." -->

   _Leaning:_ Once, on Fable (A): a plan that failed is evidence the item is harder than it looked, which is exactly when the stronger model pays.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-PT7: Should a plan be committed as a document, or kept with the run that made it?**

   The owner opens a planned item's page to see how it was split. The plan is one JSON file that the planning run wrote. The owner has ruled that state lives in documents: a machine with no store rebuilds the queue from them. A plan, though, is how one attempt carried out one item, like a run's transcript.

   - **A — Kept with the run.** The item page shows the plan, its refusal if any, and the graph's revisions. Nothing extra lands.
   - **B — Committed too.** A short plan document rides in the item's one landed commit. It is readable in Vantage, adds a file to every planned item, and records a plan that may have been abandoned.

   <!-- vantage: question id=OQ-PT7 leaning="Kept with the run (A): the documents' ruling covers which items exist and what they must do, and a plan is a record of one attempt." -->

   _Leaning:_ Kept with the run (A): the documents' ruling covers which items exist and what they must do, and a plan is a record of one attempt.

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 **OQ-PT8: Who decides whether an item is planned, and where is the rule kept?**

   An author files a build item with five criteria across two Go modules. Planning costs about $0.35, and on an item that would have cost under about $1.50 it costs more than it saves. Measured on the history, the rule "five or more criteria, or three or more modules" picks 56 of 85 build items holding 81% of the spend.

   - **A — A rule in the repository's settings**, applied when the item's graph is built. Its numbers are edited in one place, and an author can override it either way in the item's block.
   - **B — Only the author**, by naming the workflow. Nothing is planned unless someone remembers.
   - **C — Always plan**, and let the planner answer "no split". Every item pays $0.35.

   <!-- vantage: question id=OQ-PT8 leaning="A rule in the repository's settings (A): the rule is deterministic and visible, and the author keeps the last word." -->

   _Leaning:_ A rule in the repository's settings (A): the rule is deterministic and visible, and the author keeps the last word.

   **Answer:**
   > _(empty — fill in when decided)_
