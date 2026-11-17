---
title: "The item judge in tillr: migrate the verdict, delete the turn-end judge and the shadow"
status: proposed
stage: DESIGN
updated: 2026-11-10
tags: [tillr, work-control, approver, judge, item, verdict, pi]
next: "Build ITEM-V (the `item.verdict` writer rules) on the E2 critical path; the delegated sign-off grant mechanism follows as its own chunk, off that path. The stopgap seat-gating lands before any migration work"
---

# The item judge in tillr: migrate the verdict, delete the turn-end judge and the shadow

> **Delivered copy.** This is `docs/design/work-control-judge-in-tillr.md` from this repository at commit `bdc40d858b` (2026-11-16), copied into the tillr pack by `just pack-docs`. The [source](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/work-control-judge-in-tillr.md) is authoritative and may be newer. Links to documents that were not delivered point at the repository, and the queue item directives in this copy are inert.

**Status:** 2026-11-12. Ruled; nothing built. Evidence is the current tree:
the four tillr design documents, the approver design, both work-control
reference documents, the extension source, and the tests named below.

> **In short.** One decision in the judge is load-bearing — an independent
> ruling that an item's frozen criterion is met, which is what retires the
> item — and tillr has no home for that ruling yet. Everything around it
> (the automatic turn-end pass, the in-process evaluator, the shadow
> approver, the coordinator mode that gates them) is session machinery the
> accepted designs already retire. So: **MIGRATE** the verdict into
> [`tillr item`](board-utilities.md) as a record kind, demote the approver
> seat to an ordinary agent job that produces those records on explicit
> request, and delete the rest — the shadow first, because it is visibly
> firing in sessions that never asked for it and its only consumer is a
> cutover this ruling cancels.

**Why it matters.** The owner sees judge output in sessions that never saw
a whale. That is not cosmetic: the same paths that print those messages
also spend provider calls, append verdict records to the session branch,
and invite the model to request further reviews. A mechanism that acts
uninvited in plain sessions has the wrong owner and the wrong trigger,
whatever its verdicts are worth in coordinator sessions.

**Ruled:** [Ruling: MIGRATE](#ruling-migrate), with the end-state retirement
split in [Retirement in the end state](#retirement-in-the-end-state).

**Reads with:** [`board-utilities.md`](board-utilities.md) (the seven
utilities), [`board-utilities-lanes.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/board-utilities-lanes.md) (the
build order), [`board-sanity-recovery.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/board-sanity-recovery.md) (why
the board is being rebuilt), [`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md) (what
Pi keeps; the work-control split), [`approver-agent.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/approver-agent.md)
(the approver seat's own design),
[`work-control.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/reference/work-control.md) and
[`agent-work-control.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/pi/agent-work-control.md) (why the judge
exists).

---

## Terms used in this assessment

- **Criterion** — the text an item carries saying what counts as done.
  It is **frozen**: captured when the item is added, and the coordinator
  working the item may not rewrite it. The freeze is the guard against the
  worker quietly lowering its own bar.
- **Claim** — a status write asserting completion (a todo marked
  `completed`). A claim is untrusted input; it retires nothing by itself.
- **Receipt** — verified evidence attached to an item, principally commits
  landed at `HEAD` and the paths they touched. A run's report prose is
  context, not a receipt.
- **Item judge** — the decision procedure that reads a frozen criterion
  plus receipts and records one of four **verdicts** per item: `done`
  (criterion met; the item retires), `not-done` (the item stays owed),
  `blocked` (parked on a named external event it resumes from), or
  `defective` (the criterion itself is false; the item retires with the
  false premise named). Only a verdict retires an item.
- **In-process judge** — the original implementation: a provider call made
  inside the extension (`evaluateItemsDetailed` in
  `work-control.ts`, since removed from the pack).
- **Approver seat** — the newer implementation: a dedicated subagent,
  launched with a frozen, branded evidence packet under the contract named
  by the literal `"approver-contract:v1"`, whose output passes a
  deterministic intake before it becomes a verdict.
- **Shadow approver** — the approver seat run in duplicate beside the
  in-process judge, recording comparison-only verdicts marked
  `shadow: true`. By construction it "retires, parks, or requeues
  nothing".
- **Gate verdict** — a different verdict entirely: whether the checks in a
  gate plan passed on one candidate tree. It is about a tree, never about
  whether an item's criterion is met. [`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md)
  separates these as attempt result, candidate verdict, and **sign-off** —
  a recorded judgment, by a named actor, that a claim about work may
  stand. The item verdict is a sign-off in that sense.
- **Pile** — the session-visible todo list, now backed by board snapshots.
- **Whale seat** — my shorthand, from the code's own `whaleSeat`, for a
  session whose coordinator mode is `running` or `paused` (anything but
  `off`). What mode itself means is contested territory: the work-control
  split in [`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md) rules the mode deleted
  as a state (its ledger row D1), replaced by two narrower facts — *the
  session may continue itself* and *continuation is paused*.
- **Cutover** — the planned promotion of the approver seat from shadow to
  authoritative, replacing the in-process judge, gated on the threshold
  the approver design calls AA3.

## What the judge is for

When the agent doing the work says an item is finished, somebody other
than that agent has to check the claim against the words the item was
filed under — because the worker can always, in good faith or otherwise,
drift the work and then mark itself complete. That check is the judge:
it takes the criterion as frozen before the work started, the landed
commits and document excerpts that can be read at `HEAD`, and nothing the
worker merely asserts, and it records `done`, `not-done`, `blocked`, or
`defective` — including the one finding no status field can express, that
the criterion itself was false and no landing could ever have satisfied
it. The alternative that existed before it, and that it deliberately
replaced, was self-certification: the coordinator writing
`status: completed` and the item being done because it said so. The
reference states the inversion plainly — the verdict is the finding; the
status is a claim — and the rule that the judge never answers to the
coordinator is the whole point. The decision nothing else makes is
therefore narrow and real: *independent retirement* — a recorded ruling,
by an actor other than the worker, that the frozen words are satisfied
(or void), with the evidence it read named in the record.

Two narrower jobs ride along and should not be confused with that one.
The **automatic turn-end pass** is only a trigger: at the end of a
coordinator turn it offers newly claimed items to the judge so retirement
does not wait for someone to ask. The **approver seat versus in-process
judge** question is only a sourcing question: which model call produces
the verdict. Neither is the decision itself, and both can be deleted
without touching it.

## Who owns that decision in tillr

No utility owns it today, and the tillr design as accepted does not quite
notice. Run the candidates:

| Candidate | Fit | Verdict |
| :--- | :--- | :--- |
| `gate` | Owns plans and verdicts — on candidate trees, bound to a plan, a registry digest, and a `tree_sha`. A criterion ("the finding is recorded in this document") is not a check in a registry, and its evidence is landed history and document text, not one tree under test. | No |
| `drain` | Owns dispatch decisions: dispatch, hold, retry, with reasons. It can *act* on a verdict (a `not-done` item is rework; a `blocked` one waits), but a dispatch loop retiring the items it dispatched closes the self-certification loop one level up. | Consumer, not owner |
| `run` | Owns lane and run lifecycles. An approver invocation is an agent job, so a run is how one executes — that makes `run` the vehicle, not the decider. | Vehicle |
| `item` | Sole writer of items, `item_criteria`, edges, and the decision records that already fold into it. A verdict is a statement *about one item's criterion* — the board's existing store already keys its verdict rows by criterion hash and refuses a verdict naming any other criterion (`RecordVerdict` in the board store). | **Owner of the record** |
| An eighth utility | Fails the four-question test in [`board-utilities.md`](board-utilities.md): a judge owns no state of its own (its verdicts are item state), and as a pure evaluator it is exactly the "client, not a utility" shape that design rules on for work producers. | No |

So "is this item's criterion met?" is **an `item` state transition,
effected by a verdict record** — and here is the gap: the `item` faces
the tillr design specifies carry `criteria` as `{criterion_id, text,
state}`, but no record kind, face, or writer rule says who may move a
criterion's state, on what evidence. The lane plan inherits the silence:
its ITEM lanes cover apply/show, decisions and mail, and the design-doc
read-through, and no lane in
[`board-utilities-lanes.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/board-utilities-lanes.md) mentions verdicts
at all. Left as written, `item apply` would accept criterion state from
whoever presents the record — self-certification, reintroduced by
omission at the new boundary.

One part of the decision already has a mechanical home, and it is the
common case. In the board today, a drain-dispatched item is retired
without any judge: the landing queue, on publishing the item's candidate,
records `done` citing the landed commit, and the drain records
`not-done` for a lane that left no landable candidate. The criterion is
bound by hash but never *read*; landed-plus-gate-green is the whole
finding. In tillr's composed pipeline that becomes: `land` emits its
attempt record, and the orchestrating step presents a mechanical
**`item.verdict`** *(record kind coined here, in the envelope style of
`gate.verdict` and `land.attempt`)* to `item`, citing the landed commit
and the `gate.verdict` that admitted it. That covers every item whose
criterion is, in the well-formed cases the reference documents, "this
change, pinned by that test or recorded in that document, lands." What it
does not cover is what the judge was built for: work landed outside the
queue (a foreground session's own commits), criteria that turn on a
document's *contents*, and the `defective` finding. Those need a reader
of words against evidence — an agent — and
[`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md) already rules that an agent may
hold that pen: delegated sign-off, bounded by a recorded grant, with
actor kind and evidence in every verdict record, and never by the actor
whose work is judged. The approver seat therefore survives migration
only in that reduced form — **a client of `item`** (an agent job, run
through the ordinary run machinery, whose output passes the deterministic
intake and lands as an `item.verdict` record) — which is also what the
work-control split already decided about it: the approver is "a shared
service… scheduled to move out", with two callers, and `work-control`
"does not judge or own a pile" once the pile leaves (ledger rows D4 and
D3). It does not stay a Pi-session concern, because its verdicts are
board state — they already get routed to the board through
`routeVerdictsToBoard` and read back into the ledger — and state that
outlives the session cannot have a session as its only writer.

## The shadow approver is finished, and was never going to finish

The shadow existed for one purpose: the cutover. Before trusting the
approver seat over the in-process judge, run both on the same items at
the same head and count the dangerous disagreement — the agent being
*laxer* (retiring or parking what the standing judge kept owed). The AA3
exit is mechanical: `readApproverShadowGate` passes at 50 shadow
verdicts with zero agent-laxer pairs in the trailing 25, filtered to the
approver model row in force; past it, `selectApproverPhase` returns
`primary`, the in-process call stops, and shadow launches stop because
there is nothing left to pair against. As a retirement ceremony for an
evaluator swap, that is honestly built.

Three facts say it has no remaining value. First, **the ceremony now
compares two things this assessment retires**: agreement with the
in-process judge proves parity with a judge we are deleting, not
correctness against the criterion — the shadow's evidence has no
consumer once the verdict record, not the evaluator, is the thing being
kept. Second, **the exit is per-branch and per-model-row**, so "the
cutover" is not one event that finishes; every fresh branch and every
model-row change re-enters shadow and re-earns it, which is why shadow
output is a permanent feature of ordinary sessions rather than a
migration with an end date. Third, and most damning, **the shadow's
canonical launch path does not work**: [`approver-agent.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/approver-agent.md)
records, with a test pinning it, that the rendered launch requests are
refused by the item fence in their canonical cases and were refused one
fence earlier on the wire, and the shadow notice itself now says "no
launch is needed from this session" while handing the model the branded
inputs anyway. A comparison whose runs cannot reliably be launched
cannot reach its own gate count; what it reliably produces is the notice.

What breaks if the shadow is deleted: the AA3 gate can never pass, so the
primary cutover as designed is dead (that is a consequence to accept in
the open, not a cost — the migration below does not need it); the audit
re-judgment of recorded `done`s, which rides the same branded seam, goes
with it unless separately re-homed; and the shadow, cutover, and slice-6
test files are deleted or rewritten around the surviving intake. Nothing
that reads shadow verdicts for standing state exists — the reducer
ignores them by construction — so no item's state can change when they
stop.

## Why it fires in a session that never saw the whale

The automatic path cannot. In `startAgentEnd`, the turn-end trigger and
the automatic selection are both seat-gated — the literal gate is
`mode !== "off"` — a fresh session's mode starts `"off"`, and the
leak-gate test pins exactly the owner's scenario: a plain session
completes a todo and enqueues no pass, buys no judge call, and polls no
`HEAD`. `shouldJudgeItems` is a second, narrower gate (idle, no
foreground call in flight) and no settings default arms judging — the
settings surface carries budgets and caps, not an enable.

The two messages the owner quoted arrive by other doors:

1. **An explicit `review_items` call — the only door that explains the
   pair.** The tool is registered unconditionally, with no mode check
   anywhere on its path: it enqueues with `explicit: true`, and the pass
   body treats explicit as bypassing both `shouldJudgeItems` and the
   dirty-flag debounce ("an explicit review_items request always runs").
   When the queue settles, only an explicit pass emits the wake rendered
   by `renderItemReviewReply` — the `"Review request settled (a request
   is not a verdict):"` block — so that message is a signature: somebody
   called the tool. In the overwhelmingly likely case the caller is the
   model itself, and the machinery invites it to: the tool's own
   description advertises it to every session, `renderItemReviewReply`'s
   no-verdict lines end in "retry review_items with the intended ids",
   and `renderApproverShadowLaunchRequest` — delivered to the model as a
   follow-up user message — instructs "For a real verdict, name the
   items in a review request (review_items)". The judge's output is a
   prompt to invoke the judge again. The reference documents this as
   deliberate ("explicit judge invocation stays available everywhere"),
   which is defensible for a human asking and indefensible as a standing
   invitation to every model in every session.
2. **A session that looks unarmed but is `paused`, not `off`.** At
   session start the mode is restored as `readCoordinatorState(branch) ?
   "paused" : "off"` — any branch carrying an old `"coordinator-mode-v1"`
   entry (a resumed, forked, or merely long-lived session) comes up
   paused, and paused judges by design: "Pause stops PUSHING, not
   JUDGING". Typing `/autoloop` also arms the mode through a raw-text
   input hook. A restored goal alone does **not** do it — a paused goal
   suppresses the pass — so the goal is exonerated.
3. **Shadow output specifically needs no trigger of its own.** The shadow
   launch block sits inside the pass body, gated only on the phase not
   being primary, so every pass by either door emits it; and the
   subagent-completion wakes that settle remembered shadow and primary
   runs (`settleApproverShadowWakeEntries` and its primary sibling) check
   session identity, never the seat — though they can only settle runs an
   earlier pass in that session remembered, so they are an echo, not a
   source.

**What I could not determine.** Which door the owner's actual sessions
used: that needs the tool-call and branch evidence of a specific session,
which the code cannot supply, and I did not inspect session transcripts.
Whether the AA3 population has ever passed on any live branch — the owner
seeing shadow notices proves those sessions were in shadow phase, nothing
more. And whether the approver brand survives the subagent tool boundary
in production, which [`approver-agent.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/approver-agent.md) itself
records as not established by anything in this tree. The code verdict is
narrower and I am confident in it: with mode genuinely `off` and no
`review_items` call, neither quoted message can be produced.

## Ruling: MIGRATE

**MIGRATE** — the verdict moves into tillr; the Pi extension shrinks to a
caller and then to nothing.

| Piece | Disposition |
| :--- | :--- |
| The verdict itself | An `item.verdict` record kind owned by `tillr item`: item, criterion hash, verdict, reason, landed refs, actor kind, evidence pointers, evaluated head. `item` is its sole writer and refuses a verdict naming the wrong criterion hash, as the board store already does. |
| The mechanical retirement | `land`'s landed attempt plus its `gate.verdict` produce a mechanical `item.verdict` in the composed pipeline — no agent involved, exactly as the landing queue retires drain items today. |
| The approver seat | Survives only as an agent job outside tillr and outside Pi: a client that reads an item and its evidence and presents an `item.verdict` for intake. Invoked explicitly (a request, a drain policy step for non-landing completions), never by a turn end. Bounded by the sign-off grant rules in [`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md). |
| The in-process judge | Deleted. Its durable parts were never the model call — the deterministic intake, ancestry checks, and refusal accounting move with the record; the provider call inside a session hook does not. |
| The automatic turn-end pass | Deleted. Nothing in tillr needs a verdict merely because a turn ended; a claim with no verdict is visible standing state ("claimed complete, awaiting a verdict"), and visibility was always the bounded failure. |
| The shadow approver | Deleted, first. Its only consumer was the cutover; the cutover is cancelled by this ruling. |
| `review_items` | Shrinks to a thin explicit caller of the above during transition, seat-gated like everything else coordinator-shaped; dies when the pile does (ledger row D3). |
| Coordinator mode | Already ruled deleted (ledger row D1); this assessment adds only that the judge must not be re-gated on its replacement flags — judging is not self-continuation. |

## Retirement in the end state

The end state retires pipeline completions with no human in the loop, by
two verdict paths that are never merged into one:

- **The mechanical verdict retires what the pipeline landed, unattended.**
  A landed [`land.attempt`](board-utilities.md#54-land-alone) plus the
  [`gate.verdict`](board-utilities.md#57-gate-alone) that admitted it produce
  an `item.verdict` in the composed pipeline, citing the landed commit and
  that verdict. No agent judges anything and no sign-off grant is involved:
  the evidence carries the verdict, which is the exemption
  [`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md) already states for mechanical
  verdicts. This path is the second shipped end state's (E2's) retirement
  path in [`tillr-chunking-plan.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-chunking-plan.md)
  [Section 1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-chunking-plan.md#1-the-end-state), and the writer rules
  that make it mean anything — criterion state moves only on an
  `item.verdict`, frozen criterion text cannot be rewritten through an
  edit — are on E2's critical path as chunk ITEM-V. Without those rules an
  `item.record` edit can move a criterion's state, or ease its text, with no
  verdict at all, and a mechanical verdict would then guarantee nothing.
  The writer contract itself is stated where the record is owned, in
  [`board-utilities.md`](board-utilities.md) [§5.3](board-utilities.md#53-item-alone).
- **An agent-issued verdict retires the rest only under a grant mechanism
  that does not exist yet.** Completions with no pipeline landing behind
  them — a foreground session's own commits, criteria that turn on a
  document's contents, and the `defective` finding — need a reader of words
  against evidence. That reader is the approver job above, and its verdicts
  count only under a delegated sign-off grant enforced by the store: a
  recorded grant naming its scope, its grantor and its expiry, with `item`
  refusing a verdict whose grant is absent, expired or out of scope, and
  refusing any verdict from the actor whose work is judged. The grant's
  shape and its default terms are settled in
  [`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md); what is missing is the mechanism —
  the grant record, the store refusal, and the conformance case that pins
  both. That build is its own chunk, **GRANT** (ESTIMATE M — basis: one
  grant record kind, one intake check in `tillr item`, one conformance
  case), and it sits off E2's critical path: E2 retires only what E2 lands.
  Until GRANT lands, a claim with no landing behind it stands as claimed
  complete, awaiting a verdict — visible, owed, and unretired — and a person
  may still issue that verdict explicitly, recorded with actor kind `human`.
  E2 is therefore never described as having agent sign-off; it has
  mechanical retirement, which is a different and smaller claim.

The rejected alternatives, briefly. **KEEP** contradicts three accepted
rulings (mode deleted, pile out, approver out) to preserve a trigger
whose misbehavior prompted the question. **DELETE** outright is the
serious rival — the drain already retires its own items mechanically —
but it silently reinstates self-certification for every completion that
bypasses the queue and deletes the `defective` finding, and no evidence
in the tree says that class is empty; retiring an independence guarantee
needs that measurement, not an aesthetic preference for fewer parts.
**HISTORY** (stop loading it, leave the code) strands every standing
verdict's readers — the pile guard, the prod set, the board merge in
`readItemLedger` — mid-contract.

**What has to be true before this can be executed.** (1) The immediate
stopgap needs none of the below and should not wait for them: gate
`review_items` and the shadow launch message on the same seat that gates
the automatic pass, so a session with mode `off` can neither trigger nor
be invited to trigger a review — the no-launch shadow notice in
particular has no model-facing reason to exist in any session. (2) The
tillr design gains the `item.verdict` record and its writer rules, stated
in [`board-utilities.md`](board-utilities.md) [§5.3](board-utilities.md#53-item-alone),
and ITEM-V builds them in `tillr item`; until ITEM-V lands, the judge stays
where it is, seat-gated per (1). (3) An equivalence audit in the form the
work-control split already prescribes for the pile: measure, from the
verdict journal and the board's verdict rows, how many retirements the
judge uniquely provided (no queue landing behind them) over a recent
window — that number sizes the surviving approver job, and if it is zero
the owner may revisit DELETE for the seat with evidence in hand. (4) The
delegated sign-off grant must be a mechanism before the approver job
writes authoritative tillr records: today it is a stated convention with
no store refusal behind it, and [`agent-runtime.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/agent-runtime.md)
says plainly that no completion claim may credit it until one exists. The
mechanical verdict needs no grant and waits for none of this — see
[Retirement in the end state](#retirement-in-the-end-state).
(5) Someone must name what happens to audit re-judgment and to `blocked`
parks whose `resumeOn` events (`head-move`, `run-completion`, timeout)
were previously observed by the turn-end trigger; in tillr those are
drain-visible events, and the drain's decision face is the natural reader
— but that is a sentence the drain design has to say, not an assumption
this assessment may spend.