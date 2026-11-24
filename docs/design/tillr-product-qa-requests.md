---
title: "Third-round reply to tillr from the Product QA pipeline"
date: 2026-11-19
status: accepted
stage: CURRENT
next: "Received on 2026-11-20 (dated 2026-11-19), with the second round kept below. tillr answers in tillr-separable-parts.md, R3-1 and R3-2 first; Section 8's decisions are the owner's."
tags: [tillr, salvage, mangle, design, requests, product-qa]
summary: "Product QA's third-round reply to tillr at snapshot 973b12eb2d0e (2026-11-18), dated 2026-11-19 and reproduced as received on 2026-11-20, with the second-round reply of 2026-11-17 kept below: the worked example and a charter of their own ran end to end with no Git and no commit; a status per second-round request and question; reactions to the core and charter split, hooks and the contract; what a product-qa charter on the built core looks like and what salvage could adopt now; ten new requests, three of them high; six corrections, withdrawals, five questions, and six decisions for the owner."
---

# Third-round reply to tillr from the Product QA pipeline

> **Received, reproduced here by tillr.** Written by the Product QA project's agent with the owner,
> dated 2026-11-19, after reading tillr's documents at snapshot `973b12eb2d0e` (2026-11-18) and
> running a hands-on trial on 2026-11-19. The owner relayed it to this repository on 2026-11-20,
> because that agent cannot write here, and it is reproduced as received. Everything below this
> note through [Section 10](#10-the-earlier-rounds) is theirs, with the link changes listed in
> [Changes made in reproducing the third round](#changes-made-in-reproducing-the-third-round).
> Where their text says "this repository" or "this workspace", it means the Product QA repository.
> This round has not yet been answered, and [Section 8](#8-what-the-owner-decides) lists decisions
> that are the owner's.
> [The second-round reply of 2026-11-17](#second-round-reply-to-tillr-from-the-product-qa-pipeline)
> is kept in full below.

**Status:** 2026-11-19. Our third round, in the form the
[tillr design index](../tillr/design-status.md#read-this-first)
asks for, against what tillr built after our [second round](#10-the-earlier-rounds). The owner's
words are marked as his; the rest is ours, and he can overrule it.

> **In short.** tillr built what we asked to try. A store that runs a charter of our own shape
> finished findings on a hook's recorded verdict, with no Git and no commit, from inside this
> repository's tree without touching it. Two things stop salvage from keeping its queue in tillr:
> a charter has one lifecycle for every item, and we have two kinds
> ([R3-1](#r3-1-two-item-kinds-in-one-charter)), and the owner's decisions between unfinished
> states carry no reason or actor ([R3-2](#r3-2-a-reason-and-an-actor-on-the-owners-moves)). The
> owner works the queue by hand for now, so requests that serve only unattended operation are low.

**Words.** Terms tillr defines keep its meaning, as
[its documents](./tillr-separable-parts.md#the-words-this-document-uses)
define them. The ones this reply leans on:

- **Product QA** *(ours)*: a product bug-finding pipeline. Its fuzzer is **Mangle**,
  which makes one run and remembers nothing. Its tool for keeping findings and bugs after a run
  is **salvage** (the owner's name), described in `salvage.md`.
- **Finding** and **bug** *(ours,
  `salvage.md`, Section 4.1)*: a finding is one failure signature
  from a fuzzer run, and it ends with a verdict. A bug is one product defect with an `FQA-` id.
  Later findings of the same defect join it, and approval and the send belong to it.
- **Charter** *(the owner's word, ruled 2026-11-18 in
  [OQ-VO4](./tillr-vocabulary.md#OQ-VO4))*:
  the layer on top of tillr's **core** of queues and graphs that describes one use. tillr's code
  and flags still call it a **definition** (`--definition`). It is a data file of states and
  moves, plus one **hook program** that the core calls by name for each judgment
  ([`D-CG1`](./tillr-core-and-git-landing.md#decision-ledger)).
  `git-landing` is the charter for tillr's own queue.
- **Finished state**: a state the charter lists as final. A move into one stores a reason, an
  actor and a pointer, and a move out of one needs a reason and an actor. **Hold**: tillr's
  reason for not dispatching an item. **Waiter**: who a `blocked` item waits on (`owner`,
  `design`, `plan` or another item).
- **Command node**: a workflow stage that runs a registered program, not an agent.
- **Trial step** *(ours)*: a label such as A5 or B11 for one logged command in this round's
  trial. Section 1 says what each series is.
- **Works for us / works with a gap / not what we asked / could not check** *(ours)*: our
  verdicts on each answer in Section 2. "Could not check" includes everything tillr itself
  lists as unbuilt.

**On the name AutoQA.** tillr's documents use "AutoQA" for
[tillr-autoqa.md](./tillr-autoqa.md),
tillr's own design for exploratory testing. We are the Product QA pipeline. Our
roadmap (`../qa/roadmap.md`) is titled "AutoQA roadmap" and says "AutoQA is this repository's
name for the work of finding product bugs automatically". The same word
names two different things in two repositories. We flag it for the owner
([Section 8](#8-what-the-owner-decides)) and rename nothing.

## 1. What we read and what we ran

- **Read** at snapshot `973b12eb2d0e36e827384a28f071ff4de088b171`, dated 2026-11-18, as the
  tillr pack delivers it:
  - the design index;
  - the direction document
    ([tillr-core-and-git-landing.md](./tillr-core-and-git-landing.md)),
    Sections 4 to 8 and its ledger;
  - the build plan
    ([tillr-definitions-plan.md](./tillr-definitions-plan.md));
  - [tillr-separable-parts.md](./tillr-separable-parts.md),
    Sections 2, 3 and 7, its open questions and its ledger;
  - the READMEs of `tillr/definition`, the worked example, `tillr-item`, `tillr-run` and
    `tillr-drain`, and the source where a README was silent.
- **Ran** on 2026-11-19, between 13:34 and 13:55 America/New_York. We used the installed `tillr`
  launchers, which build every binary from the delivered tree once per source digest. All eight
  resolved to digest `a9e94f19ccfa`. Every command, its exit code and its output went to a log
  in this workspace's durable scratch, outside the repository. Each store was a new file in a
  scratch directory. We never used salvage's live store or any other project's store, and
  installed nothing.
  - **Series A, the worked example**
    (`tillr/examples/recorded-verdict`), copied and run as its README says.
    `GIT_CEILING_DIRECTORIES` stopped Git discovery, and `git rev-parse` exited 128 there (A0).
  - **Series B, a charter of our own shape.** Its states, moves, items, hook program,
    questions program and command node are the ones [Section 4](#4-a-product-qa-charter-on-the-built-core)
    describes. One loop (B13) ran with Git discovery on, inside this repository's tree but not
    at its top level, because that is where salvage's store lives.
- **Not run:** an agent step on a real model. The stub stood in for every agent. This round has
  no per-step cost for an agent ([R2-13](#4-requests-for-this-round)).

## 2. The second round, checked

The R2 numbers are those of our second round (commit `6560b2320de`). salvage's design cites
them, so this anchor keeps its old name. "tillr's answer" is
[Section 7.1](./tillr-separable-parts.md#71-the-fourteen-requests)
of separable-parts. "Priority now" is our order, given that the owner reviews and sends bugs by
hand ([Section 5](#5-new-requests)).

| Request | tillr's answer, and the item that built it | Our check now | Priority now |
| :--- | :--- | :--- | :--- |
| R2-1 Every table at `store init` | Built, `item-def-tables-at-init` | **Works for us.** Every table count is present after init (A2, B1) | — |
| R2-2 A store that does not land | Built, five `item-def-*` items and the example | **Works with a gap.** Loops finished items with no Git (A5, B11k), and inside our repository's tree left every ref and worktree unchanged (B13). The loop still demands an agent backend policy for a graph of commands only ([R3-9](#5-new-requests)) | — |
| R2-3 Finished states keep reasons; leaving one is an event | Built, `item-def-finished-state-receipts`, `item-def-item-lifecycle` | **Works with a gap.** Reason, actor and pointer were stored (B8b, B4e), and leaving `fixed` without a reason was refused (B7a). After the item left, `item show` still printed the old reason and pointer (B7b, [R3-4](#5-new-requests)) | — |
| R2-4 Raw paths into and out of finished states | Built for moves out, `item-def-finished-states-final` | **Works with a gap.** Moves out were refused without a reason. A move *into* `fixed` with its criterion still open succeeded (B4e), the half tillr already queues as `item-jl-terminal-state-receipts` | Medium |
| R2-5 Command steps, item id passed | Built, `item-ig-command-node`, `item-def-run-item-env`, `item-def-command-node-plain-directory` | **Works for us.** Our command node read `TILLR_ITEM_ID` and recorded `spend_status: not-applicable` (B11g). `drain workflow check` on a single file fails, even on tillr's own example (A12, [R3-6](#5-new-requests)) | — |
| R2-6 History on the item page | Built, `item-def-item-page-history` | **Works with a gap.** Runs, verdicts, state moves and owner decisions are there (B-series item pages). Moves between unfinished states show an empty actor and reason ([R3-2](#r3-2-a-reason-and-an-actor-on-the-owners-moves)) | — |
| R2-7 Detail panel from `body_ref` | Half built: the `detail` hook is in the contract and no page draws it | **Could not check.** Our hook answered `detail`, and nothing called it | Medium: the owner reads findings by hand; `salvage show` covers it meanwhile |
| R2-8 Outcomes per kind, countable | Half built: finished states per charter; counts unfiled (next tranche, unit 1) | **Works with a gap.** `item list --state` counts each state (A6, A7). The item page projects a finished `confirmed` or `repeat-of` as `not_started` ([R3-3](#5-new-requests)) | Medium |
| R2-9 Graph change as a charter's policy | Unbuilt; unit 3 | **Could not check** | Low: the cases are the unattended investigator's |
| R2-10 No fixed graph size limits | Listed as unbuilt; unit 4 | **Works for us, ahead of tillr's table.** `drain workflow check` accepted 150 nodes at depth 150 and printed the size (B17). Its help says "Graph size is reported, never refused". A list argument still needs a declared `most` ([question 3](#7-corrections-and-questions-for-tillr)) | — |
| R2-11 No clock whose only consequence is a kill | Unbuilt and in no plan | **Could not check.** `LeaseGraceMS` is still in `tillr-run/run/lease.go` (read) | Low for us: our runs set no wall |
| R2-12 Uncertain outward effect blocks a retry | Unbuilt; unit 5 | **Could not check.** `blocked` with waiter `owner` works (B10) | Low: the owner sends by hand |
| R2-13 Time and tokens per step attempt | Unbuilt; unit 6 | **Works with a gap.** A command run's record says `not-applicable` (A10, B11g). The loop summary and the item page count the same runs as unreported (A5, B11k, [R3-5](#5-new-requests)). An agent step's cost: could not check (Section 1) | Medium |
| R2-14 Owner acknowledgment point | Unbuilt; unit 7 | **Could not check** | Withdrawn ([Section 6](#6-what-we-withdraw-and-what-we-do-meanwhile)) |

**Our four questions**
([tillr's answers](./tillr-separable-parts.md#73-their-four-questions)):

| Question | tillr's answer | Our check now |
| :--- | :--- | :--- |
| 1. A person as a step | A hold naming him is built; a node whose result is his answer is not | **Works with a gap.** A `blocked` hold with waiter `owner` (B10) and an open question (B14d) both read "needs you". A stage of kind `owner` is refused, `workflow-kind: expected agent, command or workflow` (B12b). Enough while he works by hand |
| 2. The concern behind the caps | Runaway expansion and its spend; the replacement is the size shown before the first node runs, plus a budget a charter may set | **Works for us**, as the check face already shows (B17) |
| 3. Leases for `run exec` | Wall plus 300 s when `timeout_ms > 0`; no expiry at 0 | **Works for us**, read. The 300 s grace is R2-11 |
| 4. The item page for an item with no workflow | Yes | **Works with a gap.** Two `run exec` runs on an item with no workflow appear on its page. Each is drawn as a node `worker` of kind `agent`, but both were command runs (B18) |

**The second round's stable-surface corrections**
([tillr's table](./tillr-separable-parts.md#72-the-stable-surface-corrections)):
we confirmed that the contract no longer lists `--format`, that `item list` exists (A6), and that an
unknown field is refused, `rule: unknown-field` (B6b). We also confirmed the scratch-directory
variable: `scratch_env` omitted set it to the run's own directory, and `false` kept ours (B15).
The waiting class could not be checked: `tillr view waiting` refused to run (B10c, B10d,
[R3-7](#r3-7-tillr-view-waiting-runs-on-the-delivered-build)). `run-link-missing` was not re-run.
The staged brief is no longer shaped for Git (A5's lane brief).

## 3. Reactions to what is new

**The core and charter split.** It matches what we asked for in round two: shape D, a lifecycle
in the charter, and completion with no landing. The example and our own charter both ran from a
plain directory, and the loop said so: "definition: product-qa; skipped Git, item sync, trunk
history, Git lane staging, rolling tier, roll and push" (B11k).

**The charter file.** It is strict, which we want. An unknown key is refused (`definition:
unknown key "kinds"`, B2), and a move it does not list is refused (`transition-not-allowed`, B6).
What it cannot say is that two kinds of item have different lifecycles in one store. The only
workaround is two stores, and then nothing joins a bug to its findings
([R3-1](#r3-1-two-item-kinds-in-one-charter)).

**Hooks.** `describe` and `complete-item` work as the contract says. A hook name the program
does not answer is refused at loop start, naming it (B11d), not at `store init` (B2b), which is
the right place. Two gaps. The contract reads hook exit 1 as a refusal, so a hook that crashed
in Python, which also exits 1, was retried and then blocked as an "identical repeat" in landing
words (B11e, [R3-8](#5-new-requests)). And the definition README still says completion hooks are
validated "but does not yet call them" ([Section 7](#7-corrections-and-questions-for-tillr)).

**The non-landing store.** Items finish once, on a recorded verdict, with
`lifecycle_by: definition:product-qa` and a `doc:` pointer (B11m). The loop's text is still
landing-shaped. It printed gate budget lines and "landed 0, rejected 4", and listed finished
items as "held … (not-ready)" (A5, B11e, B11k).

**Item graphs.** Command nodes, declared order and the item id in the environment are what
salvage's steps need (`salvage.md`, Section 6.1). We tried no graph
change (R2-9, unbuilt).

**The contract**
([Section 2](./tillr-separable-parts.md#2-the-contract-between-tillr-and-product-qa-for-now)).
The stable surface is the right size for us: commands, flags, exit codes and record kinds, with
the schema and Go packages internal. Two parts of it did not hold on the delivered build:
`tillr view waiting` exits 3 ([R3-7](#r3-7-tillr-view-waiting-runs-on-the-delivered-build)),
and `apply` makes us stamp `producer.system: "tillr"` on records salvage writes
([question 5](#7-corrections-and-questions-for-tillr)). [Section
2.3](./tillr-separable-parts.md#23-how-a-consumer-learns-of-a-breaking-change)
says no breaking-change mechanism exists yet. Its proposal, P4 (a published contract with a
dated changes list), is what we would read before each round. We want it filed.

**The extension points we need**, one Product QA case each, against
[tillr's table](./tillr-core-and-git-landing.md#5-the-extension-points):

| Extension point | Product QA case | Built for it? |
| :--- | :--- | :--- |
| Intake | `salvage import` applies one item per finding signature, with `body_ref` `salvage:finding/<signature>` | Yes (B3) |
| What a node is | `salvage step replay` and `salvage step rejudge` as command nodes | Yes (B11) |
| Where a node runs | A plain lane directory under salvage's store, inside this repository's tree | Yes (B13) |
| What completes an item | A finding: the hook answers its verdict state from the replay's result | Yes (B11m) |
| Lifecycle | A bug moves through `reporting`, `approved` and `sent`, and a finding never does | **No** ([R3-1](#r3-1-two-item-kinds-in-one-charter)) |
| Prerequisites | A finding with an open question for the owner is held `question-open` | Yes, through a per-item file and our own questions program (B14d) |
| Retention | Release a frozen evidence environment when its bug closes | Not needed now; salvage does it |
| Outcome vocabulary | The six verdicts and the five closure reasons, countable | Partly ([R3-3](#5-new-requests)) |
| Dispatch policy | Bug-record tier first, then data-loss invariants, then recency | Not needed while the owner orders by hand |
| The judge | The `complete-item` hook records the verdict from the step's result | Yes |
| After completion | A confirmed finding proposes a bug item | Not needed now; `salvage` files it with `item apply` ([OQ-FQ7](./tillr-separable-parts.md#OQ-FQ7), option B) |
| The views' vocabulary | The finding's evidence on the item page through the `detail` hook | Not built (R2-7) |
| An owner acknowledgment point | — | Withdrawn; salvage's briefing keeps its own |

**The designer's decisions awaiting the owner**, those that affect us
([the direction's ledger](./tillr-core-and-git-landing.md#decision-ledger),
[separable-parts' ledger](./tillr-separable-parts.md#decision-ledger)):

| Decision | Which way we want it | Why |
| :--- | :--- | :--- |
| `D-CG1` A data file plus one hook program | **Keep** | Both halves did real work in our trial: the file refused bad moves (B6), and the hook judged (B11m) |
| `D-CG2` The store records its charter; a store with none runs `git-landing` | **Keep** | salvage's store sits inside this repository's tree. Because the store said `product-qa`, the loop never touched Git (B13). salvage always passes `--definition`, so the default never reaches us |
| `D-CG3` The safety rule for separating the drain | **Keep** | No effect on us, and it protects the queue that builds our requests |
| `D-CG4` Reopening is each charter's policy | **Keep** | Our case is question SD5 in `salvage-decide.md`, leaning B. A fixed bug that recurs reopens, and a declined one does not. Per-state moves say exactly that (B7b) |
| `D-CG5` The judgment column of the nine "no"s | **Keep** | The protections it keeps (a closed, projected outcome set; a single completion on a receipt; a recorded outward effect) are ones we rely on. The projection is the part that is wrong today ([R3-3](#5-new-requests)) |
| `D-FQ1` File P2, P3, P4 and P6 | **Keep, P4 and P3 first** | P4 is the change feed. P3's stock adapter would replace the stub we copied for a commands-only loop (R3-9), and the example's own stub extractor refused the drain's arguments (A11). P6 serves runs no one is watching, low for us |
| `D-FQ4` Keep the name `item apply` | **Keep** | It is how salvage already writes items |
| `D-FQ5` Decline none of round two | **Keep** | — |

## 4. A product-qa charter on the built core

This is what salvage would declare, as tried in series B. Today salvage does not call tillr at
all. Its own items, histories, step runner (`salvage step`), decisions (`salvage decide`) and
briefing (`salvage brief`) do the work, and `salvage-plan.md` says "Don't apply
items to tillr yet".

**The item shapes.** Both are `item.record` envelopes and carry only what queues and joins, as
`salvage.md`, Section 4.1 rules:

| Field | A finding item | A bug item |
| :--- | :--- | :--- |
| `item_id` | `f-<signature>` | the `FQA-` id |
| `body_ref` | `salvage:finding/<signature>` | `salvage:bug/<FQA-ID>` |
| `criteria` | one: the verdict, judged from the replay | one: the owner has decided it |
| `blocked_by` | none | the finding that opened it |
| `workflow` | `replay@v1`, a command node | none while the owner drives it |
| `design_doc` | only when it has an open question for the owner | none |
| finished by | the `complete-item` hook, or the owner's move to `repeat-of` with `--resolves-via item:<FQA-ID>` (B8b) | the owner's move to a closure state, with a pointer |

**The states**, as the trial's charter declared them: `in-queue` (ready), `blocked`, `closed`,
`reporting`, `approved`, `sent`. The finished states are the verdicts `confirmed`, `rejected`,
`inconclusive`, `repeat-of`, `stale-oracle` and `tooling-fault`, and the closures `declined`,
`duplicate` and `fixed`. A real one adds salvage's other closures, `wont-fix` and `intended`
(`salvage-decide.md`). It lists `fixed → in-queue` and
`inconclusive → in-queue`, and no move out of `declined`.

**The graph**, triage → investigate → owner decision → packet → send:

| Node | Executor | Completes with no commit? |
| :--- | :--- | :--- |
| Triage: replay, then rejudge | Command nodes running `salvage step` | Yes: the finding's verdict, through the hook (B11m) |
| Investigate an inconclusive finding | An agent node, read only. The unattended investigator is iceboxed (`../qa/roadmap.md`, "Icebox: automated campaigns"), so today the owner starts it, or holds the finding for himself | Yes: a verdict |
| Owner decision: report, approve, hold, decline | The owner. No node kind exists for him (B12b), so it is a `blocked` hold with waiter `owner` (B10), then his state move | Yes, but the move records no reason or actor ([R3-2](#r3-2-a-reason-and-an-actor-on-the-owners-moves)) |
| Packet | The owner with an agent session; packets are files committed to this repository | No: the commit is made outside tillr, and the bug's pointer names it |
| Send | The owner, by hand | Yes: `sent`, then a closure state with a pointer to the upstream ticket |

The finding's graph and the bug's states are separate items joined by `blocked_by` and
`resolves_via`. The bug item was held "blocked" on its finding until that finished (B14a).
Nothing stopped the owner moving the bug's state by hand meanwhile (B4b to B4e).

**What salvage could adopt now**, a slice that serves the owner working by hand:

1. **Findings in tillr with their triage.** `salvage import` applies finding items, and a drain
   the owner starts by hand runs `replay` and `rejudge` as command nodes and finishes each
   finding on its verdict. This would retire `salvage step --ready`, salvage's own dispatcher
   (`salvage.md`, Section 7.2). It would also retire
   salvage's own check that a finished finding stays finished. salvage's histories stay the
   record of evidence, as Section 7.3 of `salvage.md` rules.
2. **Repeats as pointers.** A later finding of a known bug finishes `repeat-of` with
   `--resolves-via item:<FQA-ID>`, and an unknown id is refused, `pointer-unknown` (B8a, B8b).
3. **Counts by state** from `tillr item list --state`, beside `salvage status`.

**What waits:** bug items. They wait on R3-1 (a finding could enter `reporting`, B6e) and R3-2
(the owner's report and approve decisions would leave no reason or actor). salvage keeps
`decide`, its binding of an approval to a text hash
(question SD1 in `salvage-decide.md`), the hold that ends by date or by a new hit
(question SD3 in `salvage-decide.md`), which tillr's no-timers rule leaves to us, the briefing and
its cursor, the packet and the leak checks.

## 5. New requests

Each was checked at snapshot `973b12eb2d0e` on 2026-11-19. Priority assumes the owner works the
queue by hand ("I need to review and send out bugs manually for a while"), so a request that
serves only unattended operation is low.

<a id="r3-1-two-item-kinds-in-one-charter"></a>

1. **R3-1. Two item kinds in one charter.** *Priority: high.* Either per-kind states and moves,
   or a kind field that the charter's moves key on. *Case:* findings and bugs in one store,
   joined by edges, as question SV3 (`salvage.md`, Section 4.1) rules. *Checked:* a `kinds` key
   was refused, `definition: unknown key "kinds"`, exit 2 (B2). A `kind` field on an item was
   refused, `rule: unknown-field`, exit 1 (B6b). A finding moved from `in-queue` into the bug's
   `reporting` state, exit 0 (B6e). With two stores instead, a bug cannot point at a finding in
   the other one ([question 1](#7-corrections-and-questions-for-tillr)).

<a id="r3-2-a-reason-and-an-actor-on-the-owners-moves"></a>

2. **R3-2. A reason and an actor on the owner's moves.** *Priority: high.* Both should be
   stored on a move between unfinished states that the charter marks as a decision, or the move
   should be tied to a decision record. *Case:* the owner's report, approve and hold decisions
   on a bug (`salvage-decide.md`) are what his audit reads. *Checked:*
   `--reason "owner: report it" --by owner` on `in-queue → reporting` was refused,
   `flag-not-stored`, exit 1 (B4a). Without them the moves succeeded (B4b to B4d), and the item
   page shows them with an empty actor and reason. The decision recorded with scope `items`
   (B5) appears on the page but is joined to no move.
3. **R3-3. A charter's finished states shown as finished.** *Priority: medium; part of
   the next tranche's unit 1.* *Case:* how many findings are confirmed. *Checked:* the item page
   gives `projected_state` `{"raw_state": "confirmed", "state": "not_started"}`, and the same for
   `repeat-of`. The loop lists finished items as "held … (not-ready)" (A5, B11k).
4. **R3-4. `item show` drops the finished-state reason and pointer when an item leaves.**
   *Priority: medium.* *Case:* a fixed bug that recurs (question SD5 in `salvage-decide.md`).
   *Checked:* after `fixed → in-queue` with reason "recurred on a later build" (B7b), `item
   show` still printed `lifecycle_reason: "fixed upstream"` and the old `resolves_via`. The item
   page's history had the new reason.
5. **R3-5. Not-applicable spend kept apart from unreported spend.** *Priority: medium; part of
   unit 6.* *Case:* the owner's spend audit of a night's triage, which is all command runs.
   *Checked:* each command run's record says `spend_status: not-applicable` (A10, B11g). The
   loop summary called them "4 unreported run(s) … lower bound" (B11e), and the item page gives
   `usage: {"lower_bound": true, "unrecorded_runs": 1}`.
6. **R3-6. `tillr drain workflow check <file>` finds the command registry.** *Priority:
   medium.* *Case:* checking salvage's workflow files before a run. *Checked:* exit 1 on tillr's
   own example, `open …/config/tillr/workflows/config/tillr/commands.json: no such file or
   directory` (A12). The same happened from the workflow directory and on a directory argument
   (B12c, B12d). The drain itself reads the registry from the root, so the loop worked.

<a id="r3-7-tillr-view-waiting-runs-on-the-delivered-build"></a>

7. **R3-7. `tillr view waiting` runs on the delivered build.** *Priority: high: a face on the
   stable surface that fails outright.* *Case:* the owner's "what needs me". *Checked:* exit 3,
   "the planner … was built from no revision (digest a9e94f19ccfa), and this server from no
   revision (digest a9e94f19ccfa), so its answer would be another build's rules". The same
   happened with `--tillr-drain` naming that planner (B10c, B10d). The two digests match.
   `tillr drain why` gave the same rows (B14a), and `view serve` answered.
8. **R3-8. A hook's refusal told apart from its crash.** *Priority: medium.* Let the
   answer carry the refusal, or reserve an exit code other than 1, and word the hold without
   landing terms. *Case:* salvage's hook is a program that can fail. *Checked:* our hook raised
   an exception (exit 1). The drain re-ran the item, then blocked it with waiter `owner`:
   "Identical repeat … Change the instruction the worker builds from … a design run that commits
   nothing names its next actor" (B11e, B11f). The README documents exit 1 as the
   `candidate-no-op` refusal, so this is the contract working. The contract is what we ask to
   change.
9. **R3-9. No agent backend policy required for a graph of command nodes only.** *Priority:
   low; one stub file works around it, and P3 may cover it.* *Checked:* with no policy file the
   loop exited 2. With `{}` it said "the policy names no backends" (B11c, B11c2).
10. **R3-10. An item's open questions shown by `item show` and on the item page.** *Priority:
    low: salvage's `ask` (`salvage-decide.md`, Section 8) is not built.* *Checked:* a per-item file and our
    own questions program held a finding `question-open`, with the owner as its mover and a link to
    the question in the file (B14d), and a missing program held it `questions-unknown` (B14e).
    `item show` gives only `questions_status: known` and the file.

## 6. What we withdraw, and what we do meanwhile

**What we withdraw.** R2-14, the acknowledgment point: salvage's briefing keeps its own
cursor, and the owner reads it by hand. From round two we still withdraw the Go library,
deadlines in the plan, a second loop in one root, and free graph rewriting.

**What we do meanwhile.** Unchanged from round two: the fuzzer is run by hand, salvage keeps its
items, histories and dispatcher, and nothing is applied to tillr until the owner rules on
[Section 8](#8-what-the-owner-decides)'s first row.

## 7. Corrections and questions for tillr

**Corrections to tillr's documents**, at snapshot `973b12eb2d0e`:

- [Section 7.1](./tillr-separable-parts.md#71-the-fourteen-requests)
  lists R2-10 as unbuilt. The check face reports size and never refuses it (B17).
- [Section 2.1](./tillr-separable-parts.md#21-the-stable-surface)
  lists `tillr-view waiting`, and the direction's Section 8.2 says it prints the charter. On the
  delivered build it exits 3 (R3-7).
- Section 2.1's scratch-directory note says "not re-read since `item-def-run-item-env`
  landed". We ran it: omitted or `true` sets the run's directory, and `false` keeps the caller's
  (B15).
- The definition package's README says the completion hooks are validated "but does not yet
  call them". The loop called `complete-item` for every item (B11k).
- The worked example's `extract-report` refused the drain's arguments, "unrecognized arguments:
  --backend --exit-code 0 <log>". So its stub agent run reads as unreported for a reason other
  than being a stub (A11).
- The example's staged brief printed the first criterion with no number, and its criteria
  in an order other than the item's (A5's lane brief). *Inferred:* the template's
  `CRITERION_1` expects the template itself to number the first line.

**Questions for tillr:**

1. **Two kinds:** for R3-1, does the core prefer per-kind lifecycles in one charter, or two
   stores with a pointer that crosses them?
2. **The owner's moves:** for R3-2, should the reason and actor go on the move, or should a
   decision record name the move it authorizes?
3. **`most` on list arguments:** `drain workflow check` names the rule
   `workflow-argument-most`, and `strings` and `pieces` arguments must declare a bound. Is that
   a cap in `D-IG4`'s sense? We accept a declared bound only if exceeding it leads to a named
   next step and not a refusal (the owner's position in R2-10).
4. **Hook exit 1:** is the refusal meant to be an exit code (R3-8)?
5. **`producer.system`:** `apply` refuses any value but `tillr`, so salvage's records claim tillr
   wrote them (B9: `record from another system: "salvage"`, exit 2). Is `producer.utility` the
   place a consumer names itself?

## 8. What the owner decides

1. **Whether salvage starts applying findings to tillr now**, the slice in
   [Section 4](#4-a-product-qa-charter-on-the-built-core). It contradicts salvage-plan.md's "Don't
   apply items to tillr yet". Bug items wait on R3-1 and R3-2 either way.
2. **`D-CG1` to `D-CG5` and `D-FQ1`, `D-FQ4`, `D-FQ5`**, the designer's decisions tillr's next
   tranche waits on. We would keep all of them ([Section 3](#3-reactions-to-what-is-new)).
3. **Question QS3** in `product-qa-tool-split.md` ("How much of tillr can we build on, and
   when?") is blocked on this document. It can be unblocked, with this round as its evidence.
4. **Question QS4** in `product-qa-tool-split.md` (the owner as a step's executor). tillr has a
   hold and no owner node. A hold is enough while he works by hand.
5. **Questions SD1 to SD6** in `salvage-decide.md` bear on the charter. Each closure
   reason and decision becomes a state or a move, and question SD5 decides which finished states list
   a move out.
6. **The name AutoQA**, used here for the Product QA work and by tillr for its own exploratory
   testing. Nothing is renamed without his word.

## 9. Evidence

The trial log keeps each trial step's exact command, exit code and output. It is in this
workspace's durable scratch, outside the repository, and is not cited by path. The steps this
document names:

| Step | Command | Exit |
| :--- | :--- | :--- |
| A2 | `tillr store init --store .tillr/state.db --definition definition.json` (example) | 0 |
| A5 | `tillr drain loop --root $PWD --store $PWD/.tillr/state.db --lanes 1 --exit-when-drained` (example) | 0 |
| A6, A7 | `tillr item list --store .tillr/state.db [--state <s>]` | 0 |
| A10, A11 | `tillr run status --run run-finding-confirmed-a1-n1` (command), `…-a1-n2` (stub agent) | 0 |
| A12 | `tillr drain workflow check config/tillr/workflows/recorded-verdict@v1.json` | 1 |
| B1, B2, B2b | `tillr store init --definition` with our charter, with a `kinds` key, with an unanswered hook name | 0, 2, 0 |
| B3, B6b, B9 | `tillr item apply` of two findings and a bug; of an item with `kind`; of a record from `salvage` | 0, 1, 2 |
| B4a to B4e | `tillr item state` on the bug: with `--reason --by` to `reporting`; plain to `reporting`, `approved`, `sent`; to `fixed` with reason, actor and pointer | 1, 0, 0, 0, 0 |
| B5 | `tillr item decision record` with scope `items` | 0 |
| B6, B6e | `tillr item state` of a finding to `sent`; to `reporting` | 1, 0 |
| B7a, B7b | `tillr item state --state in-queue` on the fixed bug, without and with reason and actor | 1, 0 |
| B8a, B8b | `tillr item state --state repeat-of --resolves-via item:<unknown>`; `item:FQA-TRIAL-001` | 1, 0 |
| B10, B10c, B10d | `tillr item state --state blocked --waiter owner`; `tillr view waiting`, then with `--tillr-drain` | 0, 3, 3 |
| B11c, B11c2, B11d | `tillr drain loop --check` with no backend policy; with `{}`; on the store whose charter names `after-completion` | 2, 2, 2 |
| B11e, B11k | `tillr drain loop … --exit-when-drained --questions-cmd "python3 questions.py"`, first with a hook that crashed, then fixed | 0, 0 |
| B12b to B12d | `tillr drain workflow check` on a stage of kind `owner`; on a directory; from the workflow directory | 1, 1, 1 |
| B13 | B11k's loop with Git discovery on, inside this repository's tree; refs and worktrees compared before and after | 0 |
| B14a, B14d, B14e | `tillr drain why --json`; with our questions program; with a missing one | 0, 0, 0 |
| B15 | `tillr run exec` with `scratch_env` omitted and `false` | 0, 0 |
| B17 | `tillr drain workflow check` on a 150-node chain | 0 |
| B18 | `tillr view serve`, then the item answer for an item with no workflow | 0 |

The item pages were read from `tillr view serve` at `/api/v1/items/<id>`. No front end is built
into the delivered binary ("ui: none").

## 10. The earlier rounds

This file replaces its earlier text each round; git history keeps it.

- **First round**, 2026-11-16: 19 requests, in `34b3399f078`, revised in `8f14e9940ec` and
  `d968a01c05e`. tillr answered in
  [separable-parts Section 5](./tillr-separable-parts.md#5-the-requests-restated).
- **Second round**, 2026-11-17: R2-1 to R2-14, read at snapshot `bdc40d858b`, in
  `6560b2320de`, with the salvage naming edit in `67fa9175465`. tillr answered in
  [Section 7](./tillr-separable-parts.md#7-the-second-round-tillrs-answer)
  and by building [the plan](./tillr-definitions-plan.md).

---

## Changes made in reproducing the third round

*(tillr's, not theirs.)* The third-round text above is otherwise as received.

1. The twenty-two links to tillr's documents by GitHub URL now point to the same documents and
   sections in this repository, as relative links. One of them,
   [`OQ-FQ7`](./tillr-separable-parts.md#OQ-FQ7) in Section 3, pointed at `#open-questions` and now
   points at the question's own anchor, because this repository's checker requires a question link
   to name the question.
2. Twenty-one links to documents in the Product QA repository (`salvage.md`, `salvage-decide.md`,
   `salvage-plan.md`, `product-qa-tool-split.md` and `../qa/roadmap.md`), which are not in this
   repository, are now their names as text. Where a link named a section with `§` or a question id
   in that repository (questions SV3, SD1 to SD6, QS3 and QS4), it is written as "Section …" or
   "question …", because this repository's checker requires every `§` reference and question id to
   link to a target here.
3. The explicit `<a id="4-requests-for-this-round"></a>` anchor in Section 2 is omitted, because
   [the second-round reply](#second-round-reply-to-tillr-from-the-product-qa-pipeline) is kept below
   in this file with its own [4. Requests for this round](#4-requests-for-this-round) heading, so
   links to `#4-requests-for-this-round` still resolve to the second round's request list without a
   duplicate anchor.
4. The note at the top and this list were added, the frontmatter was updated to record the reply
   as received, and the second-round reply of 2026-11-17 is kept below with its headings shifted
   down one level under one top-level title.

---

## Second-round reply to tillr from the Product QA pipeline

> **Received, reproduced here by tillr.** Written by the Product QA project's agent with the owner,
> dated 2026-11-17, after reading tillr's documents at snapshot `bdc40d858b` and running a trial on
> 2026-11-16. The owner relayed it to this repository, because that agent cannot write here, and it
> is reproduced as received on 2026-11-17. Everything below this note is theirs, with the link
> changes listed in [Changes made in reproducing it](#changes-made-in-reproducing-it). Where their
> text says "this repository" or "this workspace", it means the Product QA repository. tillr's answer
> is in [Section 7 of the separable-parts proposal](./tillr-separable-parts.md#7-the-second-round-tillrs-answer),
> which covers R2-1 to R2-14, the stable-surface corrections and the four questions in
> [Section 7](#7-questions-for-tillr) below; the eighteen items of
> [the first tranche](./tillr-definitions-plan.md) and the finished-state evidence rule of
> [the judge and lifecycle design](./tillr-judge-and-lifecycle.md#7-what-is-built-and-what-is-owed)
> have landed, and the remaining requests sit in the plan's unfiled next tranche.

**Status:** 2026-11-17. Our second round, in the form tillr's
[design index](../tillr/design-status.md#read-this-first)
asks for. The owner's words are marked as his; the rest is ours, and he can overrule it.

> **In short.** tillr's store, items, verdicts and leased runs worked for us outside Git; six
> things stop a findings queue running for real ([R2-1 to R2-6](#4-requests-for-this-round)). The
> owner rules graph change and reopening a definition's policy the core must not preclude, and
> refuses fixed graph sizes and clocks whose only consequence is a kill.

**Words.** **Product QA** *(ours)* is a product bug-finding pipeline, with the fuzzer
Mangle as its prober (`product-qa-tool-split.md`). tillr calls us "AutoQA",
but [tillr-autoqa.md](./tillr-autoqa.md) is exploratory testing of tillr itself. **Detail store**, **event command** and **step** are ours
(Terms, in `product-qa-tool-split.md`); every other term is tillr's, as
[its documents](./tillr-separable-parts.md#the-words-this-document-uses) define it.

### 1. What we read and ran

- **Read** at snapshot `bdc40d858b50a430a2f598e2dadd71f174c3d09f` (2026-11-16): the index, the
  direction and separable-parts documents whole, [item graphs](../reference/tillr-item-graphs.md), [run-wall-ruling.md](./run-wall-ruling.md) and
  the gate design (since replaced by [the gate reference](../reference/gate.md)) in part; and the graph-runtime
  design of another graph runtime, in this repository.
- **Ran** 2026-11-16, 23:46 to 23:58 America/New_York, the installed binaries (build digest
  `33a5752220ab`), one logged command at a time, on two stores of our own in a plain directory,
  `findings/state.db` and `ideas/state.db`, beside a copy of Mangle's state.
- **Worked:** `tillr-store init` at any path; `tillr-item apply` of 31 finding and 5 idea items with
  an opaque `body_ref` (`mangle-bundle:36784f2253`); criteria as steps; `blocked_by` edges, missing
  targets and cycles refused; verdicts with grants, refusing self-judging ("a delegated verdict
  needs an actor other than the one whose work criterion "c2" judges"); owner decisions; mail;
  `close` and `reopen` from `closed`; `blocked` with waiter `owner` and a reason; `drain once` with a
  policy, writing a run request; `run exec` of a Mangle re-check (`mangle triage rejudge <sig>
  --dry-run`) through a stub adapter we wrote; one agent step on the `claude` backend, haiku, Read
  only: 7 s by its run record, 15,032 tokens, $0.001283; steps as items joined by edges, including
  injecting a step and rewiring a successor that had not started.

### 2. Reactions

**The shapes.** [OQ-CG1](./tillr-core-and-git-landing.md#OQ-CG1):
**D**, vocabulary as data plus one hook program: how we already compose tools.
[OQ-CG2](./tillr-core-and-git-landing.md#OQ-CG2):
**A**, a generic lifecycle in the core, each definition naming its finished states.
[OQ-CG5](./tillr-core-and-git-landing.md#OQ-CG5):
no preference; we need only the two things distinguishable.

**The extension points**, one case per row of
[tillr's table](./tillr-core-and-git-landing.md#5-the-extension-points):

| Extension point | Product QA fills it with |
| :--- | :--- |
| Intake | The event command a fuzzer run fires on a finding calls `tillr-item apply`. No documents; Mangle never learns what tillr is |
| What a node is | A command (re-check, replay, reduce); an agent (the investigation); a person ([question 1](#7-questions-for-tillr)) |
| Where a node runs | A plain scratch directory: no worktree, no Git |
| What completes a node | Declared outputs: a verdict record and references to evidence |
| What completes an item | A recorded, judged verdict |
| Lifecycle | Finished states of our own; leaving one by appending |
| Prerequisites | The bundle reference resolves; reduction finished or declared flaky; not a duplicate |
| Retention | Ours, tied to releasing frozen evidence environments (environments Mangle holds for a bug) |
| Outcome vocabulary | Confirmed, rejected, inconclusive, repeat of an item, tooling fault |
| Dispatch policy | Concurrency bounded by environment capacity; cheap mechanical steps before agent steps |
| The judge | An independent verifying agent, a different actor from the investigator; the no-self-judging rule fits |
| Trunk observation | Our analog: a new product version, which can reopen an item closed as fixed |
| After completion | Fire a command that proposes a report |
| The views' vocabulary | Finding, evidence, verdict, waits on whom; a panel from `body_ref` ([R2-7](#4-requests-for-this-round)) |
| An owner acknowledgment point | B9; meanwhile we keep our own cursor, as tillr suggests |

**The eight "no"s** of
[Section 1.3](./tillr-separable-parts.md#13-what-tillr-will-not-change):

| | Request | For Product QA |
| :--- | :--- | :--- |
| C1 | Outcomes per kind | **Real tension.** Inconclusive must be countable, not free text (R2-8) |
| C2 | Leaving a finished state | **Real tension.** The owner rules it a definition's policy ([Section 3](#3-graph-change)); R2-3 |
| C3 | Graph change | **Real tension, narrowed.** We withdraw "a running step rewrites the graph freely" ([Section 3](#3-graph-change)) |
| C4 | An uncertain outward effect | **Real tension.** A report send that lost its session (R2-12) |
| C5 | Clocks and deadlines | **Withdrawn.** The time box is the fuzzer run's own business; a deadline as an ordering input of dispatch policy is a later wish. Clocks themselves: R2-11 |
| C6 | A Go library | **Withdrawn.** Processes and records are enough |
| C7 | Completion with no commit | **Real tension.** A recorded, judged artifact (R2-2) |
| C8 | A second loop in one root | **Withdrawn.** We run our own store and root, which the Git refusal in R2-2 blocks today |

### 3. Graph change

**Where the owner, the other graph runtime and tillr agree.** The owner, 2026-11-17, clarifying his statement of the
day before: a step cannot replace itself, "but it can emit another node directly downstream of
itself, which consumes all of its original data that … it will pass through and has the exact same
output shape"; and "you could have a graph of 10 nodes fully executed. Then at some point in the
future, you add two nodes to make it 12. That gives you two unexecuted nodes that will then get
started the next scheduling pass." All three agree on four rules:

| Rule | The other runtime | tillr |
| :--- | :--- | :--- |
| No node replaces itself | "caller … cannot modify itself" (its determinism rules) | "No node rewrites itself" (`D-IG3`) |
| Nothing executed changes or becomes unexecuted | once started, "its contract is locked" (its graph algebra, Definitions) | a node is fixed when its run is recorded |
| Every change passes the downstream-shape check | each not-started successor "remains satisfiable" (its graph algebra, Revise) | "the outputs the rest of the graph was promised are still promised" |
| The graph grows by appending; the ordinary pass dispatches what has not run | re-entry by a sibling node (its execution model, Retries) | an append-only `graph_revisions` |

**Where tillr's text is narrower than the other runtime's:**

| | The other runtime | tillr |
| :--- | :--- | :--- |
| Who may change it | A running node may add pending nodes only it reaches (its determinism rules) | A stage declaring `may_replace`, replacing a later, not-started node (`D-IG3`, `D-IG12`) |
| What may be inserted | Any executor or sub-graph whose types fit | A tracked workflow, bounded arguments (`D-IG5`) |
| Unknown-size growth | A container grows until its exit predicate turns true, then refuses additions, `ContainerFinalized` (its exit predicates, substrate invariants) | At most 100 nodes and depth 8 (`D-IG4`) |
| Edges | Typed case-split, "exactly one fires per upstream emission" (its graph algebra, failure as dependency typing) | `when_outcome` `pass` or `fail` (`D-IG6`) |
| A finished graph | Refuses additions; re-entry by a new sibling node | `landed`, `resolved-elsewhere` terminal; reopen only from `closed` |

**The owner's ruling, 2026-11-17.** On reopening: "Reopening or not, when everything is done, I
suppose should be a policy determination for whatever your use case is. Like, we will define valid
state transitions for an item and reopening might be valid for some and not valid for others." On
the other differences: "they should be all policy things that should be possible if you want them
from core. Core should not preclude them." That is tillr's own decision rule
([Section 1, item 6](./tillr-core-and-git-landing.md#1-the-direction-in-the-owners-words))
applied, and [OQ-CG2](./tillr-core-and-git-landing.md#OQ-CG2)'s
option A extended to graphs: each definition declares its valid transitions,
including whether, and from which finished states, an item may be left.

| A definition's choice | Git landing | Product QA, and its case |
| :--- | :--- | :--- |
| A running step inserts a node directly downstream of itself, same output shape, data passed through | May forbid | **Allows.** The investigation decides a finding needs one more mechanical replay with a different seed before the verdict |
| Nodes added to a graph whose every node finished | May forbid | **Allows.** New evidence on an inconclusive finding, or a finding closed as fixed recurring on a newer product version: two nodes appended, the earlier outputs as inputs |
| What may be inserted | May require a tracked workflow | **Tracked workflows suffice**; we can name ours in advance. Anything shape-keeping is the owner's to raise, not our need |

The core keeps what tillr's [Section 6](./tillr-core-and-git-landing.md#6-what-this-changes-in-what-was-said-before)
names for C3: one writer applies each change atomically with its event, nothing started changes,
and the downstream-shape check holds.

**What Product QA uses:** child items from a step (a finding that is two bugs; a report per
recipient); a count not known in advance, which intake by `item apply` already covers, so no growing
containers; branching on a typed outcome (repeat, split, inconclusive); reopening as appended steps
fed the earlier result; and an earlier step choosing the sub-graph that replaces a not-started one
("report" or "close as repeat"), which tillr's queued proposal block fits.

### 4. Requests for this round

Each entry was checked at snapshot `bdc40d858b` on 2026-11-16 unless it says otherwise. R2-1 to
R2-6 are the smallest tranche that lets a findings queue run for real; please take them in order.

1. **R2-1. Every table created at `store init`** (tillr's A1). *Case:* our dispatcher reads lanes
   and runs before any drain runs. *Checked:* `tillr-view waiting`, `tillr-drain why` and `plan`,
   `tillr-run lanes`: exit 3, `no such table: lanes`; `run status`, `no such table: runs`. All
   answered after one `tillr-drain once`.
2. **R2-2. A store declared not to land** (B3, C7,
   [OQ-FQ5](./tillr-separable-parts.md#OQ-FQ5)):
   no fence, no Git repository, no trunk; items finish on a recorded verdict. *Case:* a finding
   never makes a commit. *Checked:* `drain why` held all 31 findings `no-fence`, and one given
   `allowed_paths` `no-policy`. `tillr-drain loop --exit-when-drained` on our root exited 4: `git
   rev-parse main in <root>: … fatal: not a git repository`.
3. **R2-3. Finished states keep their reasons, and leaving one is an event** (C2). *Case:* a repeat
   points at its original, and new evidence reopens it. *Checked:* `tillr-item state --state
   resolved-elsewhere --reason repeat-of:f-36784f2253` exited 0 and stored no reason, pointer or
   actor. `reopen` refused it (`is "resolved-elsewhere", not closed`, rule `item-not-closed`) and a
   `landed` item alike; `close` refused both (`close-state-invalid`).
4. **R2-4. The raw `state` and `apply` paths into and out of finished states closed** (A2, widened;
   [OQ-FQ6](./tillr-separable-parts.md#OQ-FQ6)).
   *Case:* nothing rewrites what already ran. *Checked:* `state --state landed` was accepted twice
   with no landing, and `state --state in-queue` moved items back out. `apply` rewrote a `landed`
   item back to `in-queue` with a new title, and rewired an item that had two runs. A2 names only
   `item state`.
5. **R2-5. Command steps in workflows, the item id passed to the adapter** (B1, A4). *Case:*
   re-check, replay and reduce are commands. *Checked:* a workflow with a command stage was held
   `workflow-invalid: workflow-kind: recheck: only agent is supported`. The drain built the command
   `run-recheck --backend recheck --model none --timeout 120`, naming no item; our adapter parsed it
   out of `TILLR_RUN_ID`.
6. **R2-6. Runs, verdict reasons, decisions and close and reopen events on the item page** (queued
   `item-ig-view-nodes` covers nodes). *Case:* the owner's morning audit of one finding.
   *Checked:* `tillr-view serve`'s answer for an item with two runs: `attempts: []`, verdicts with no
   reason or evidence, drain holds but no owner decision, no close or reopen. `/` answered "This
   binary carries no built front end"; `landings` answered `table-absent`.
7. **R2-7. A detail panel the definition supplies, resolved from `body_ref` by a hook.** *Case:* the
   owner ruled tillr is a queue, not our database: an item carries the signature and one opaque
   reference, and details live in our detail store. *Checked:* `body_ref` round-trips; the item
   answer shows it as a string beside fixed Git-landing fields (`design_doc`, `allowed_paths`).
8. **R2-8. Outcomes per kind, countable** (C1,
   [OQ-FQ2](./tillr-separable-parts.md#OQ-FQ2)).
   *Case:* how many findings a run left inconclusive. *Checked:* `state --state inconclusive`
   exited 2, `"inconclusive" is not a state`; a verdict `inconclusive` exited 2, `want one of done,
   not-done, blocked, defective`.
9. **R2-9. A core that does not preclude a definition's graph changes** (C3, [the owner's
   ruling](#3-graph-change)). *Checked:* `D-IG3`, `D-IG5`, `D-IG12`; steps as items, rewired, worked.
10. **R2-10. No fixed size limits on a graph** (the owner's position): "This 100 steps, I hate this.
    I don't want limits like this. We should certainly tell Tiller that this is not acceptable.
    Like, we'll build what we build." Product QA accepts no fixed cap on nodes, depth or fan-out.
    *Checked:* `D-IG4`, "the drain expands every stage that names a workflow, to a depth of at most 8
    and at most 100 nodes in all", and 8 scout copies. *Case:* a run whose every finding needs a
    chain of steps; a bug with a report per recipient. Please name the concern behind the cap.
11. **R2-11. No clock whose only consequence is a kill** (the owner's position): "The agent works
    until it is done, it has an event, we have a stall detector watchdog if we need something like
    that, but we don't just say, 'Okay, 30 minutes,' and then have some gate given 20 seconds and on
    a loaded machine it fails". Liveness is a stall detector's call, not elapsed time's; a clock
    leads to a named next step; a short timeout that fails on a loaded machine is a defect.
    *Checked:* tillr largely agrees: `D-RW1` removed the default run wall ("A total-run wall is a
    budget, not a fault detector"), and `D-RW2` and `D-RW3` gate or resume a walled run. Left: B5's
    lease to the wall plus 300 seconds, and the gate's "could not judge" for a fast check at its
    9,000 ms ceiling, which the owner had not ruled (removed since:
    [`D-GR15`](../reference/gate.md#why-its-this-way)). Our trial's walls were ours (`--timeout 600`).
12. **R2-12. An uncertain outward effect as a state that blocks a retry** (C4). *Case:* a report
    send that lost its session. *Checked:* `blocked` with waiter `owner` and a reason works.
13. **R2-13. Time and tokens per step attempt, with their source.** *Case:* the owner's audit of
    one finding. *Checked:* an agent run's record carries `usage`; a command step's says what its
    extractor says (ours: "tokens: unknown").
14. **R2-14. An owner acknowledgment point** (B9), later. *Case:* "new since you last looked".
    *Checked:* `tillr-view events --since` takes a cursor; mail refuses a second ack.

### 5. Stable-surface corrections

For [Section 2.1](./tillr-separable-parts.md#21-the-stable-surface):

- **`--format` is listed and no face defines it.** `tillr-view status --format json`: exit 2, `flag
  provided but not defined: -format`; on 2026-11-17 no `--help` of ten listed faces names it.
- **No face lists items**; `tillr-item show` needs `--item`. The drain's run request reaches the
  adapter without the item id (R2-5).
- **`item apply` drops an unknown field silently:** `"kind": "finding"` was accepted, exit 0, and
  not stored. Refuse it, or keep it.
- **`run exec` sets `YOLO_DURABLE_DIR`** to `<lane>/durable/<run>`, undocumented; Mangle refused
  its store ("fuzz root … is outside the durable dir …") until our adapter reset it.
- **The waiting class disagrees.** For an item blocked on the owner, `tillr-view waiting` printed
  `waits on work`; the served waiting answer said `needs you`, with the reason.
- **A run the drain requested but did not start** (written to a record sink, run by us with `run
  exec`) made the next pass hold the item `run-link-missing`, as A3 says.
- **An agent step's brief** has nine sections shaped for Git (a base sha; a fence of paths).

### 6. What we withdraw, and what we do meanwhile

**Withdrawn:** first-round request 13, the Go library (C6); deadlines inside tillr's plan (C5); a
second loop in one root (C8); "a running step rewrites the graph freely" (C3); and the money and CPU
parts of first-round request 19. The owner says dollars are iceboxed on tillr's pages only because
rates are unsettled; tokens and their source are enough for now.

**Meanwhile:** we run the fuzzer by hand, keep the graph in a small dispatcher of our own over
tillr's items and leased runs (as tillr's C3 row suggests, with no drain), and keep outcomes and our
acknowledgment cursor in our own store.

### 7. Questions for tillr

1. **A person as a step** (open on our side, question QS4 in `product-qa-tool-split.md`). The owner
   floated being the executor of his steps, and also pictures two tillr instances, in one of which
   he is what items wait on, tied by some orchestration: "I don't know that it is one answer."
   Which does your design prefer: a hold naming him between items, or a node whose result is his
   recorded answer? In the trial his answer took three writes joined only by text: a decision, a
   human verdict citing it, and a close whose reason names it.
2. **Behind the caps:** which concern does `D-IG4` guard against (R2-10)?
3. **Leases:** for `run exec` today, which holds, B5's wall plus grace or `D-RW1`'s unbounded lease?
4. **The item page:** will `item-ig-view-nodes` also show the runs of an item with no workflow?

### 8. The first round

It held 19 requests; tillr answered 1 to 13 in
[Section 5](./tillr-separable-parts.md#5-the-requests-restated),
and 14 to 19 (our wrapper, the observatory, the designs, a person as a step, open questions on an
item, cost per step) are carried in above. This file supersedes it; git history keeps its text.

---

### Changes made in reproducing it

*(tillr's, not theirs.)* The text is otherwise as received.

1. The fifteen links to tillr's documents by GitHub URL now point to the same documents and
   sections in this repository, as relative links.
2. Three links to `product-qa-tool-split.md`, a document in the Product QA repository, are now its name
   as text: in the Words paragraph, twice, and in question 1 of Section 7. There the question's id
   is written as "question QS4", because this repository's checker requires every question id to
   be a link, and that question is not in this repository.
3. Eight links to another graph runtime's documents and source, which are not here, are now a
   description as text: the graph-runtime design in Section 1, and seven cells of the two tables
   in Section 3. Each cell keeps the section name the link pointed at.
4. Three names of tillr's documents in Section 1, the item-graphs design, the run-wall ruling and
   the gate design, are now links to them, because the checker requires a named file beside this
   one to be a link.
5. This note at the top and this list at the end were added, and the frontmatter was updated to
   record the reply as received and answered.
