# tillr-drain — the pass, and the loop that advances items

> [!WARNING]
> **Known gaps.** Two things about `tillr` are not what a first reading assumes:
> 1. **The observatory front end is a separate build** — a `tillr-view` built by a plain
>    `go build` answers 404 for the UI. The front end under `tools/tillr-view/web` is a
>    Node build, embedded only under the `observatory` build tag (`just observatory` does
>    both) or served from a directory named with `--web-dist`.
> 2. **A `tillr` installed from the pack is not this working tree** — the pack under
>    `yolo-packs/tillr` builds from its own mirrored copy of the source, which
>    `just pack-sync` regenerates. To run an edit under `tools/tillr*`, build the working
>    tree with `just tillr-build`.

## What this module owns

`tillr-drain` turns store state into per-item successor decisions. A **pass** visits
items in queue order and applies at most one transition per item; dispatched runs and
candidate examinations proceed independently, without waiting for a batch to finish.
`plan` computes one pass's positions and transitions and writes them to the file the caller named, and writes nothing else anywhere;
`once` does the same planning and records the decisions in the store's own dispatch table,
handing each dispatch's run request to a caller-named runner; `retry` records one
operator's authorization to re-dispatch a run the harness refused, or to raise an item's
conflict again for one more design run; `clear-fault`
acknowledges a diagnosed item-local fault so its retained work may advance; `boundary` records the
write boundary before and after a run and compares the pair; `resume` continues a run that
died without a verdict from its own harness session; `loop` is the unattended supervisor.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

## Moving a running loop onto new code

Start the drain and observatory from the `just tillr-build` bin directory.
After a tillr landing the loop runs `just tillr-roll --no-request`, then requests restarts; under after-merge proof, the proof runner owns the suites.
The drain starts work on free lanes while an in-place restart waits for a gate or landing. Replacement leaves live workers on their original leases; a refusal leaves the old loop running.
After a manual commit, run `just tillr-roll` (in-place) or `just tillr-restart` (rebuild HEAD, restart keeper and loop).
Pack launchers use a separate install: run `yolo host apply --assert` before requesting a restart.

## Opted-in judgment before the gate

An item directive with `judge="agent"` asks for an independent, read-only review
of its committed candidate. For an opted-in item with no evaluator slot, the drain
persists a fixed `review` evaluator before the worker starts. The policy must map
the selected evaluator's seat. Items without an opt-in or an evaluator keep the
mechanical retirement path and start no review run.

A workflow may set `exit.evaluator_slot` to the node that evaluates its candidate.
That node must be a read-only agent, required by the exit, after every writer,
and declare a nonempty `judge_classes` list. The valid entries are `verification-only`,
`red-gate-triage`, and `direction`; none may repeat. Both the shipped `verify` node
and the fixed `review` node list only `verification-only`. Intake checks
the stored selection and criterion class, not the workflow file on disk. An item
that also opts into agent review still gets only the workflow's evaluator.
The evaluator brief also carries its stage prompt and, for each preceding producer,
its node id, seat, access, prompt and output directory. The joined candidate diff
is the retained `candidate.diff`.

The evaluator's retained judgment records every writing node's run, session,
backend, and route beside its own. The intake checks independence against all
those writer identities. In a graph with only read-only producers, their actual
run and session identities are retained instead; the evaluator never names itself
as its producer. Evaluators run alone rather than sharing a parallel reader
group, because the judge step owns the lane's brief and session inputs.
A later read-only node carries the checked judgment to the graph's final candidate;
it does not start another judge. The
[evaluator-routing tests](./drain/workflow_evaluator_test.go) cover these guarantees.

After the worker passes the common deny-list check, the review runs in the same lane with
its own run and session identities. Its brief includes the criteria, their hashes,
the candidate's tree identity, the path to its exact retained diff, and the path
for its written verdict. Keeping the raw diff outside the brief lets ordinary code
containing literal brace pairs pass the adapter's template preflight.
The verdict file stays in the review run's durable records. The worker's brief
and session inputs are restored afterward. A review that commits or dirties the
worktree is refused; the drain restores the original committed candidate.

The [item judge intake](../tillr-item/README.md#written-judge-verdicts) checks the written
file without recording verdicts. A missing or refused file holds the item as
`judge-unreadable`, attributed to the review run rather than the worker, not to
the worker's own endings. A valid
`not-done` finding from an ordinary judge holds a document item as `eval-failed`, without a gate,
landing, or criterion verdict. The waiting view includes the controlling criterion's
reason. A workflow evaluator's `not-done` instead refuses the candidate as `verify-not-done`
and returns it to the workflow's build step (for both document items and filed requests),
without a gate or criterion verdict (see
[the verify return below](#a-refused-run-goes-back-to-its-worker)). On a
[filed request](#filed-requests), `not-done` from an ordinary judge also returns all
`not-done` findings to the builder's same session with no operator hold.

A valid `blocked` finding moves the item to blocked with the entry's reason and
its structured `waiting_on.waiter`; it records no criterion verdict. A valid
`defective` finding records the file through the same intake and closes the item
with `defective: <criterion>: <reason>`, attributed to the judge. Intake and closure
share one transaction. Neither route gates or lands the candidate, and settlement
frees the lane. Missing or invalid blocker metadata holds as `judge-unreadable`
without changing the item's disposition. For mixed failures, `defective` takes
precedence over `blocked`, which takes precedence over `not-done`, regardless of
file order. The first entry of the controlling kind supplies the reason.
A judge's blocker is not a failed attempt: second-attempt settlement preserves
its waiter and reason rather than replacing them with a generic design conflict.

A pass permits the ordinary gate and landing. Only a successful landing records
the checked agent verdicts, bound to the landed commit, and marks the item landed
in the same store transaction. The mechanical verdict program is skipped. A gate
or landing refusal records no judge verdict and keeps that step's own hold reason.
The checked file and caller-owned provenance travel with a retained gated candidate,
so a later landing attempt does not start a second review.

Judgment does not expire during gating or landing. After publication the drain
retains the exact landed commit until it records the checked file and item state.
Recovery does not repeat the worker, review, gate, or landing. An unfinished
review handoff is recovered before the worker's recorded-head guard, including
restoration when a judge committed before its terminal record was lost.

The [judge-routing tests](./drain/judge_route_test.go) cover ordering, authority,
read-only restoration, failure attribution, and mechanical-path compatibility.
The [disposition tests](./drain/judge_disposition_test.go) cover blocked metadata,
criterion intake and closure, the three failure pairs in both file orders, lane
release, and an unchanged trunk. The
[blocker tests](./drain/judge_blocker_test.go) cover unreadable blocked metadata.

## Express lanes

The [saved-idea rule](../../docs/reference/observatory-actions.md#the-saved-idea-item)
names **express work**: a saved idea, extended here to a filed request (unless filed
with `--ordinary`) and an item whose block sets `express="true"`.
Express lanes are reserved for that work and do not consume the ordinary `--lanes` cap.
Configure their count in the repository settings file (default: zero):

```toml
[lanes]
express = 2
```

The loop names these lanes `express-1`, `express-2`, and so on. Express work prefers
one of them, then an available ordinary lane; if neither is free it waits. Free express
lanes go in queue order among express items, so a returned worker session or design
continuation further back in the queue does not reserve a free express lane ahead of an
express item nearer the front, and a pass does not claim a free express lane for a later
item once an earlier express item enters the queue ahead of it. Ordinary work never uses
an express lane, and no running work is stopped to make room.
Eligibility comes from the stored item definition, not its workflow's first seat.
Saved ideas use the `item-idea-` ID convention from
[idea intake](../../docs/reference/observatory-actions.md#the-saved-idea-item).

Review-comment work stays at its document's
[desk](../../docs/reference/tillr-designer-sessions.md#the-desk), a lane assigned to that
document, and is not express work even if its block names the attribute. Waiting
output and the observatory lane strip count ordinary and express lanes separately;
desks remain visible but count in neither pool.

Lane rows are retained for run history, so lowering `lanes.express` can leave
registered express lanes in the displayed total. Allocation uses only the
configured pool.

## Designer retirement

The [keep-warm rule](../../docs/reference/tillr-designer-sessions.md#the-keep-warm-rule)
ends a designer's session when its document is deleted or its stage is done.
Deletion requires absence from both the trunk commit captured by the pass and the
main checkout. An uncommitted removal or a file briefly missing during landing
therefore keeps the designer's row. Retirement deletes only that row, not its transcript.

## Pass observations and report identity

The [drain successor reference](../../docs/reference/tillr-drain-successor.md#one-pass-and-compare-and-swap-application)
defines the item's positions and transitions. Each plan record prints `position` and
`transition`: `dispatch`, `retry`, `settle`, or `judge` when work can advance, and `none`
with a `reason` otherwise. Item holds and resource deferrals (`no-lane`, `fork-held`)
are separate groups in the loop's stop detail. A missing usable policy is a fault,
not an item hold.

The input kind is `drain.pass`. Its payload needs no `now` or `available_lanes`:
the envelope's creation time supplies observation metadata, and registered lanes are
offered in lane-id order. Callers may still supply those fields for a simulation.
`max_dispatches` is an optional simulation cap; reaching it simulates `no-lane`.
The live loop supplies no cap: lane leases bound dispatches.

The live start coordinator wakes on queue changes and lane releases. It claims
fresh work in queue order as soon as an eligible lane is free, then stages the
claimed work asynchronously. The pass and its completion handoff still advance
unfinished attempts. A settling attempt keeps its lane until release; it does
not stop another free lane from taking work. Loop shutdown cancels the
coordinator and joins staging before returning.

An applied pass receives a store-counted **generation**, the ordinal defined by the
[trigger and generation model](../../docs/reference/tillr-drain-successor.md#generations-timing-and-the-trigger-loop).
An empty pass allocates none. Requests carry that generation as their correlation.
Timing measurements live in `drain_passes`, whose `tick_id` column
carries the generation. For a coordinator start, `claim_ms` measures from the
later of observed item readiness and lane availability to completion of the
claim, before worktree staging. Reranking alone does not reset readiness.
`claim_ms` stays null when the writer did not measure a claim and preserves a
measured zero. `stage_ms` records staging
separately and includes any asynchronous staging reported for that pass.

A change to the gate runner or its wrapper, or to the rolling program and its audit,
proves its own suite before merging, regardless of `landing_strategy`. The
[runner-change rule](../../docs/reference/gate.md#after-merge-proof-and-the-green-pointer)
keeps that entire landing out of the ordinary time distribution: `gate_ms`,
`land_ms`, and `gate_count` measure ordinary candidates only. `runner_gate_ms` and
`runner_land_ms` record the exceptional gate and landing times, and
`runner_gate_count` counts those gate attempts. The loop reports these separately,
including slow proofs. A measured duration does not fail a test or a landing.

Loop reports are named by their inclusive first and last generations, not by the
clock. A loop that allocated none uses zero endpoints, so a startup fault remains
readable. When `drain-0-0.json` already exists on a consecutive zero-generation exit,
the prior record is archived to `drain-0-0-<unixNano>.json` before writing the new
`drain-0-0.json`. For example:

```text
drain-1-3.json  -> record id loop-1-3
drain-4-6.json  -> record id loop-4-6
drain-0-0.json  -> no applied generation
```

## The commit a new lane starts from

The repository's `lane_base` gate setting defaults to `green`. A new dispatch
records and stages the commit at `refs/tillr/green`, the
[green pointer](../../docs/reference/gate.md#after-merge-proof-and-the-green-pointer)
that names the last proved trunk commit. With `tip`, it records and stages the
current trunk tip instead. A trunk-fixer item always starts from the tip: its
failure is not in the older proved commit.

A finished blocker's landing must be contained in the green pointer before a
new dependent lane dispatches under `green`. The hold reason is `trunk-unproved`,
not `blocked`: the work is finished, and the heavy runner must prove the trunk.
A landed blocker without a recorded landing also waits rather than guessing
that its work is in the selected base. Advancing the pointer releases the hold
on the next pass; `tip` does not apply this proof requirement. A fork blocker
instead needs its judged landing retained by its delivered patch branch: fork
commits are not part of main's history, so main's green pointer cannot prove them.

A new lane must also carry the item's own block as sync read it, at the tip. When
the green pointer holds another version of the block, or none, the claim cuts the
lane from the oldest trunk commit after green that carries the tip's version, and
reports `<item>'s block is newer than green <green>; cutting its lane from <commit>`.
The item does not wait for a proof of its own block, and the lane takes only the
part of the unproved range up to that commit. The decision is recorded in the
dispatch's `base_sha`, as for any other base.

A worker whose tree lacks what made its item ready stops and reports `blocked`, as
its brief says. That report is about the staging, so it never becomes a design or
owner conflict. Before raising one, the loop recomputes the run's recorded
`item_block` input over the block at the run's base. It names a missing commit only
on evidence: a trunk commit after the base that carries the dispatched version, or
a blocker landing recorded before the dispatch that the base does not contain. Such
a run gets a `drain.stale-base` event naming the base and what it lacks, and the
planner treats it like an interrupted run. The next dispatch has the reason
`restaged: the earlier run's base lacked what made the item ready`, and the claim
selects its base by the rules above. A design run that reports `blocked` on a
conflict raised from a stale base answers that conflict as a restage instead of
sending it to the owner. At each pass, an existing owner conflict whose raising
run's base lacked those commits is answered the same way, once per loop process,
with the finding appended to the conflict's text. An owner conflict raised on a
base that carried the block is left to the owner.

With no pointer yet, dispatch uses the tip and the loop reports that choice once.
An invalid pointer or one outside trunk history is an error, not that bootstrap
case. Existing workflow stages retain their recorded base. An idle lane already
at a newer trunk commit can be reset to green because trunk retains its work;
a commit neither the selected base nor trunk contains is still refused as
unlanded work.

The [lane-base regression tests](./drain/lane_base_test.go) cover the selected
worktree commit, recorded base, blocker proof wait, fixer exception, and bootstrap
notice. The [stale-base tests](./drain/stale_base_test.go) cover the lane cut from the
commit carrying the block, the stale-base report, the design run on a stale-base
conflict, and the release of an existing owner conflict.

## Node write scopes

A writing workflow stage may set `write_scope` to `"inherit"` (also the default
when omitted) or a nonempty list of repository-relative path prefixes. A list
limits only that stage; an omitted scope does not inherit an item's path
list. Explicit scopes expand to required pack copies and documentation companions.
The loader refuses invalid values, empty lists, and any scope on a read-only stage.

Each explicitly scoped node receives its scope in its staged metadata and brief. Writing nodes
run serially in the item's single git target, including a fork checkout; the next
node starts at the preceding node's final HEAD. At each writing node's exit, the
drain checks only that node's changed paths, from its starting HEAD to its final
HEAD. An escape is refused as `boundary-scope-escape`, naming the node and path;
no successor starts. A dirty writing target on a writing node that ended with a
readable answer is also refused as `boundary-scope-escape`; a writing node ended
for a repeating call, by a signal, or by a machinery fault preserves its
uncommitted work under `refs/tillr/preserved/<run>` and restarts on its route
instead of refusing as an escape. Passing every node
does not waive the final deny-list check of the whole committed change.
The staged node base and scope are kept separately from the item's original base
so the final candidate includes all nodes and lands once.

A resumed node keeps the scope in its saved workflow; reloading an edited workflow
cannot broaden its authority. A held candidate preserves that staging metadata and
its original base when it releases a lane.

## Stages that name workflows

A `workflow` stage names a tracked file in
[`config/tillr/workflows/`](../../config/tillr/workflows/). Before dispatch, the
loader replaces it with the executable nodes inside that file. Their IDs are
`<stage>/<inner id>`; `parent` records the containing stage, including nested
parents. The containing stage never has a run. Cycles among files, untracked
references, and outputs not exported by an inner exit are refused; depth and
node count are measurements, not limits.

A workflow's top-level `inputs` declares named file types. An incoming edge to
its containing stage feeds every inner node with that same unfed input name.
The inner `exit.outputs` maps each exported name to its producing node and
output. An outgoing edge carries that producer's file and also waits for every
node in the inner exit's `requires_nodes`. For example:

```json
{
  "inputs": {"source": "markdown"},
  "exit": {
    "requires_nodes": ["read", "write"],
    "outputs": {"result": {"node": "write", "output": "result"}}
  }
}
```

The top-level `args` declares required parameters. A containing stage passes
values in its own `args`. Paths are repository-relative and cannot traverse
outside it; strings and pieces have a declared `most`; commands name registered
entries, never command lines. Missing, unknown, wrongly typed, and oversized arguments
are refused before dispatch. A whole value spelled `${args.name}` substitutes
that parameter's typed value, including when forwarding to another workflow.
No expression is evaluated. Example declarations and a read-only repeated stage:

```json
{
  "args": {
    "prompts": {"type": "strings", "most": 8},
    "paths": {"type": "paths"},
    "check": {"type": "command"}
  },
  "stages": [{
    "id": "scout", "kind": "agent", "seat": "scout",
    "access": "read_only", "repeat": "prompts", "prompt": "${item}"
  }]
}
```

`repeat` makes one read-only run per string, numbered `scout-1` through
`scout-N`; `${item}` in its prompt becomes that string. Empty repetitions make
no runs and preserve ordering between the steps around them. A workflow must
still contain executable nodes. A named output crossing a repeated stage must
have a single producer, rather than silently choosing one copy's file.

A `pieces` argument is a bounded list of objects with exactly `id`, `seat`,
`access`, `prompt`, optional `outputs`, and, for a writer, a nonempty
`write_scope` list. IDs follow output-name rules and must be unique; access is
`write` or `read_only`, and readers must omit the scope. A stage repeating over
pieces becomes one agent per entry in list order, with those fields copied into
the graph. Ready writers run one at a time in that order; ready readers share
the existing parallel scheduler. Named outputs are declared per piece.

[`planned-build@v1`](../../config/tillr/workflows/planned-build@v1.json) starts
with a read-only planner, falls back to a build node whose brief includes the
planner's report when it proposes nothing, and ends at the verify evaluator. Its optional `graph.proposal` may replace
`work` with [`build-pieces@v1`](../../config/tillr/workflows/build-pieces@v1.json)
and up to 12 pieces. The proposal and its graph revision are settled together;
refusals leave the old graph intact. An unmapped seat records
`proposal-seat-unmapped`, a writing piece on a policy read-only seat records
`proposal-read-only-seat-writes`, and a scope outside the containing stage's scope records
`proposal-scope-outside-fence`. The planner's brief includes the owner-editable
[`config/tillr/plan-guidance.md`](../../config/tillr/plan-guidance.md) when present.
If a piece needs a design decision, the planner proposes nothing and reports an
`owner` obstacle with its specific question; the design seat receives the conflict.
When the request's words amend or overturn a decision recorded in a design document's
ledger, the planner writes neither `do-it-now` nor a proposal and ends with a
`definition` obstacle naming the document and decision so the item routes to the
design seat to update the document before any build.
The verifier asks for `node:<id>` evidence for each covered criterion and a
`not-done` reason beginning `uncovered:` for each uncovered one. The judge intake
does not enforce the node locator; the verifier makes that judgment. A
`verify-not-done` refusal returns to the build step, and to `plan` only when the plan
itself was wrong (see
[the verify return below](#a-refused-run-goes-back-to-its-worker)).
Document-declared
items do not select this workflow unless named on the block or in `[workflows.default]`,
whereas `tillr item request` files `build` requests (its default seat) with
`planned-build@v1` by default and `design` requests with
[`scouted-design@v1`](../../config/tillr/workflows/scouted-design@v1.json) by default
(other seats have no request default and must name a workflow).

A containing stage is not the only source of `args`. An item that names a workflow
in its own `workflow` field may also carry `workflow_args`
([item record](../tillr-item/README.md#workflow-arguments-and-store-declared-items)).
Selection binds the workflow's top-level `args` from those values and validates them
by the same rules. A workflow that declares `args` and is named by an item without
them is refused at selection with `workflow-argument-missing`, which holds the item.

### Document triage

[`doc-triage@v1`](../../config/tillr/workflows/doc-triage@v1.json) checks a list of
documents for relevance and accuracy, and is not tied to any design document.
`tillr-item doc-triage` files the item and supplies the list
([how](../tillr-item/README.md#filing-a-document-triage)). The workflow has two stages:

1. `read` repeats over `args.docs`, with a declared `most` of 48. It makes one
   read-only node per document on the `scout` seat, numbered `read-1` through
   `read-N`, and the readers run in parallel under the default `max_parallel`. Each
   reader writes a `finding` output in Markdown that answers five questions with
   evidence: is the document still relevant, is it accurate, is its status line true,
   are its open questions still askable, and what action is recommended. The action is
   one of keep as is, correct facts, mark built or graduate, supersede by another
   document, retire, or merge into another document. Like every reader, it changes no
   file.
2. `report` is the one writing node, on the `design` seat. It receives every finding,
   and inherits the item's fence, which is exactly the report path. It writes one
   report: a table with one row per document (the document, a verdict of `current`,
   `stale` or `obsolete`, the recommended action, and one sentence of evidence), then
   the detail for each document. It edits no other file. The gate and judge then treat
   the item like any other.

The triage item is a store-declared item, so the
[document-fed prerequisite](#document-fed-workflow-prerequisite) does not apply to it.

### Filed requests

`tillr-item request` files a piece of work straight into the queue
([how](../tillr-item/README.md#filing-a-request)). Unless filed with `--ordinary`, the
stored item carries `express`, so it is eligible for the [express lanes](#express-lanes),
preferring a free `express-*` lane and falling back to an ordinary lane. The item names
an existing workflow, and its words, held in the store's `request_words` table, are its whole
statement. It is a store-declared item, so the
[document-fed prerequisite](#document-fed-workflow-prerequisite) does not apply, and it
never has a document block. The drain runs it like any other item, with four additions:

- **Brief.** Every node's brief, a read-only reader's included, states the task as the
  request's, says the item has no block and must not get one, and ends with the words in
  a byte-counted suffix, `## Saved request words (verbatim; N bytes)`. That is the form a
  saved idea's words take. The adapter's preflight excludes the suffix from its template
  checks, so the words may hold braces or headings. A conflict-fix or design-answer run
  keeps its own task and still carries the words.
- **Candidate check.** Before the gate, the loop compares every Markdown file the
  candidate changes at its base and its head. A candidate that adds, removes or edits an
  item block is refused as a fence refusal whose detail starts `request-item-block:`.
  Like every fence refusal, it goes back to the item's worker with that detail.
- **Judge.** An agent judge or a workflow's `evaluator_slot` node reads the same words
  suffix at the end of its brief, because a request's criteria judge the work against
  them.
- **Owner questions, retries, and identical repeats.** Because a filed request never has a
  document block, an `owner` obstacle or an identical-repeat hold names `tillr drain retry`
  on the held run (and `tillr item close` only while the item has no landed row) instead of
  asking for a block amendment or document triage. Authorizing that retry releases the owner
  hold back to the queue and delivers the recorded text into subsequent worker and judge
  briefs under `## Answered owner questions` (pairing the worker's question with its answer,
  who recorded it, and when, so the question is not asked again) or `## Retry reasons` (with
  who recorded it and when).

Completion is the normal gate and landing, plus whatever the workflow's own verify or
judge stage decides. `planned-build@v1`, the default workflow for `build` requests, has a
`verify` evaluator for `verification-only` criteria. `scouted-design@v1`, the default
workflow for `design` requests, and `single-worker@v1` have no evaluator of their own. When a filed
request raises an obstacle for the design seat, its design-answer run updates the relevant
design document without adding an item block (or objects
with `NEXT-ACTOR`), and returns the request to its workflow once the answer lands.

Every writing node inside a containing stage inherits that stage's
[write scope](#node-write-scopes) unless it declares a narrower one. A scope outside its container is refused before any run. Settlement checks each writing node's own
change against its resolved scope; it does not judge earlier nodes' changes
against the last node's scope.

The stored graph contains the expanded nodes, parents, scopes, and SHA-256
hashes of all contributing workflow files. Continuation and restart use this
snapshot, not edited templates. The file check uses the same expansion and
refusals, printing executable node count, dependency depth, outgoing edge count,
and expansion depth. For a parameterized library checked on its own, it uses
one representative string, piece, or registered command per parameter. Path parameters
remain symbolic: their unknown scope is constrained by enclosing stages rather
than rejected against an invented path. Each real invocation validates its
concrete paths and reports its actual expansion.

### Filing a design-seat split

Submit the design seat's statement with `tillr drain split --item <parent-id> < split.json`.
The parent is an existing queued or blocked request with no attempt in flight.
Filing does not waive an existing owner approval hold, an outstanding owner
ruling, or a named prerequisite or plan hold. Settle the existing hold before
filing. For example, if the parent's only custom criterion is `c5`, the
statement can read:

```json
{
  "parts": [
    {"label":"core", "title":"Build the core", "share":"Implement the core behavior", "leaves":{"cases":"Add the edge cases"}, "criteria":["The core cases pass"], "required":true},
    {"label":"cases", "title":"Cover the edge cases", "share":"Add the edge cases", "follows":["core"], "criteria":["The edge cases pass"], "required":true}
  ],
  "coverage": {"c5":[{"part":"cases"}]}
}
```

`leaves` maps sibling labels to work left to them, and `follows` lists sibling
labels that must finish first. A coverage entry without `clause` assigns the
whole criterion. To divide a criterion, list its verbatim clauses in text order,
each with its `part`; include all the text except whitespace between clauses.
End each nonfinal quote at terminal punctuation followed by whitespace, at a
line break, or at a semicolon or non-ASCII sentence terminator such as `。`
without requiring a space. JSON field names are case-sensitive.
Use `{"closing_check":true}` instead of `part` for an end-to-end criterion left
to the final check. Do not assign the four standard request criteria or supply
donor instructions: all parts start from scratch.

The command returns the part IDs by label. Replaying the same statement changes
nothing; a refused statement files nothing. Once all required parts have landed,
the parent stays blocked and `tillr drain why` names the operator to check its
criteria by hand. A required part that was resolved elsewhere still blocks the
parent.

## Parallel read-only nodes

Ready read-only [nodes](../../docs/reference/tillr-item-graphs.md#the-words)
of one item start together on its existing lane and worktree. A workflow's
`max_parallel` sets how many may run together: omission means 4, and the
maximum is 8. Writing nodes always run alone; neither a reader nor another
writer starts until the writing node finishes.

Each reader has its own run id, staged brief, harness session, log, report,
and recorded spend. Adapter metadata lives beneath the lane's node directory;
it does not consume another lane. The item's lane stays leased until every
reader has a terminal record. The waiting view and Home's lane line report the
number of running nodes, not additional lanes.

The loop checks the shared worktree only after all readers finish. A changed
HEAD or dirty tree holds the item as `boundary-scope-escape`, naming every node
that ran together rather than blaming one reader. A failed reader does not
cancel its siblings: their reports and declared outputs are retained, and the
item follows its failure edges or holds after they finish. On restart,
finished nodes retain their results; only interrupted executions run again.
Continuing any held reader with `tillr-drain resume` reopens its whole set. The
loop waits for that continuation, retains finished siblings without rerunning
or charging them again, and advances to the following node once. Shutdown
accounting includes every reader's latest recorded spend, not just the reader
that owns the set's dispatch record.

A node's answer is the word after its report's last `RESULT:`, read through
Markdown emphasis, code markers and a closing full stop, so `**RESULT: done**`
answers `done`. An agent node whose footer is missing, or names no ending
(`done`, `failed`, `blocked`), gave no answer the loop can read: that node
waits for its own backend to reopen before it runs again. Its finished siblings are kept.

A node record that ended `blocked` with no cause and an abandoned set holds as
`node-answer-unread: <node>`, read from the run's own report. Its mover is a
retry of the item's latest finished run; a build retry naming any other run
is refused with the accepted command, since the plan would ignore it. A retry
of a workflow item whose
attempt ran no writing node keeps every read-only node that passed (recorded
in `node_reused`) and starts the new attempt at the first node not kept,
staged fresh on the current trunk like a first node. A
retry after a writing node ran keeps nothing, because the readers read the tree
for the candidate the retry replaces.

## Registered command nodes

A workflow stage with `kind: command` runs a registered program, not a model.
Its `command_ref` names an entry in
[`config/tillr/commands.json`](../../config/tillr/commands.json). Each entry supplies
`argv`, the fixed list of program arguments (program first), and a `flags` map from
accepted long flag names to `string`, `number`, or `path`. A path value is relative
to the repository, cannot contain `..` components, and cannot escape through an
existing symlink.
For example, a registry entry and its stage can use these shapes:

```json
{"check": {"argv": ["python3", "tools/check.py"], "flags": {"--target": "path"}}}
```

```json
{"id": "check", "kind": "command", "command_ref": "check", "access": "read_only", "flags": {"--target": "tools/result.json"}}
```

Flags become separate `--name=value` arguments in sorted flag-name order; no
workflow value is interpreted by a shell. Direct shell programs are refused in
the registry. The loader checks every command stage before staging any node,
and holds an invalid workflow as `workflow-invalid` with the finding. Workflows
without command stages do not need the registry file.

The command runs through `tillr run exec` in the item's existing worktree with
backend `command`, no model route, no model spend, and no backend quota window.
Exit 0 passes and exit 1 fails, irrespective of any report footer. Other exits,
a program that cannot start, and an explicit `timeout_ms` wall hold the item as
`command-fault`, without backend failover. Repair the command process and
explicitly retry the node. An omitted wall imposes none.
A failed command follows a matching `when_outcome: fail` edge if one is ready;
otherwise the item is held as `node-failed: <node>`, later nodes and the gate do
not run, and the work is preserved for inspection.

A `read_only` command must leave both HEAD and the worktree unchanged. A writing
command must commit its changes or leave the worktree clean. The final candidate
includes the earlier builder's commits and any command commits; the item still
gets one gate and one squashed landing.

## Settled harnesses

A worker can answer and commit without its harness exiting. The
[run face's completed-session rule](../tillr-run/README.md#ending-a-settled-harness-that-lingers)
ends only the harness's private process group after a settlement event, a final
`RESULT:` report footer, and five consecutive one-second samples with neither
output nor CPU activity. Output or CPU activity restarts that count. Silence alone
never ends unfinished work, and unrelated processes in the lane remain alive.

The terminal record remains `exited`, with `end_cause: settled-idle`, the observed
signal exit code, `settled_at`, and `harness_ended_at`. The finish event preserves
both observation timestamps and the run's status exposes them. Because the report
answered, the attempt is classed as `answered`: the normal candidate examination
and gate follow, not backend failover or an infrastructure retry. A `blocked` or
`failed` footer still follows the ordinary handling for that answer. Self-exiting
harnesses keep their existing records.

A Pi run whose adapter ended its settled session has a different record: an
ordinary exit 0 (`end_cause: normal-exit`) that carries an `ending_exception`
([Pi's settled-session ending](../tillr-run/backends/pi/README.md#ending-a-settled-session-pi-does-not-exit)).
The drain judges it on its answer like any exit 0. It must not treat the run as
killed, re-dispatch it or resume its session.

## A session pi ended for a repeating call

Pi ends a session when the model keeps making one identical tool call. The runner
records such a run with
[`end_cause: repeating-call`](../tillr-run/README.md#a-session-pi-ended-for-a-repeating-call),
along with the call. The drain does not judge it, does not charge it to the backend,
and never continues the conversation that looped. It records a `drain.repeating-call`
event with the call and the route it chose. It keeps the run's work the way an
interrupted run's is kept: its commit is pinned at `refs/tillr/runs/<run>` and any
uncommitted work is preserved. Then it takes one of two routes:

1. **A new session on the same backend** (only for a pi without the `action` field on
   `loop_detected`, before in-run loop recovery). For a first such ending with neither
   recorded `loop_recovery` nor `action: "ended"`, the brief gains a "Your previous
   session" section that names the repeated call, says this is a new session that is not
   counted against the item, and points at the kept work.
2. **A hold for the operator** (`repeating-call`). If pi already recovered in-run, or
   the replacement session also repeats, the drain holds the item without starting on
   another backend. Read the call in the run record before running `tillr drain retry`.

The dispatch decision carries the ending it answers as `repeated_call`, so there is no
counter. A run dispatched for any other reason starts the chain again. A workflow node
takes the same routes: it is dispatched again at once on its lane and base, like an
infrastructure restart. On a hold, its node row is marked `repeating-call` and the item
is held. Design and desk-document runs keep their own routes for an unanswered run.
The [repeating-call tests](./drain/repeating_call_test.go) cover item and node paths
with and without recorded in-run recovery.

## Landing calls

The drain owns one store record per landing call ID. `call.offered` binds the offer to its
run, session, worktree, staged base and committed head. `call.answered` stores the one
immutable answer. Delivery and closure are separate from choosing an answer; a repeated
call ID does not create a second offer or authorize a second judgment.

The harness publishes `landing-offer.json` in the offering run's durable directory. The
drain reads it only after the run and its children are gone and treats a valid offer as a
completed submission of that commit. `land` takes no arguments; a footer worker can end
with just `RESULT: done`. Neither supplies a summary or test inventory. The verifier
reads the candidate and the item's words and criteria, and runs its own checks. A verdict
that cites a worker report is returned to the verifier for correction, not used to refuse
the commit. The named obstacle ending remains available.

For an opening that delivers an answer, the drain
publishes `landing-answer.json` in the **new** run's durable directory before the harness
starts. Both files are written whole and renamed into place. Neither belongs in the loop
record directory.

The drain records the adapters' effective `ending-capability` declarations at loop start.
Muse and Agy ship `footer`: a run with only a footer follows the existing path. Pi ships
`call-suspends` and the drain probes the installed `pi` for `parked-tool-calls`; Claude
ships `call-suspends` and the drain probes the installed `claude` once
(`probeClaudeEndingCapability`): `claude -p --setting-sources "" --continue` in an empty
directory, with `CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME` unset, starts no model turn, and
a binary that supports deferred-tool resume names `No deferred tool marker found in the
resumed session` on standard error. An absent binary, an older one, or that variable set
records `footer`. Both suspending backends offer through the same `landing-offer.json` and
receive the same answer text as the suspended call's own result;
[`backends/claude`](../tillr-run/backends/claude/README.md#the-delivery-call) explains how
Claude Code does it. `landingResultPersisted` reads either backend's session file, and
`reconcileLandingDelivery` records `call.delivered` from it. See the
[landing-call design](../../docs/design/tillr-landing-call.md) for the full protocol.

## Backend policy changes

Each seat names one backend unless it gives explicit shares to every backend
it lists. A singleton array is still accepted. An unshared multi-backend list
is refused with the seat's name; keep the intended backend, not a fallback.
A `preference` key is accepted and ignored; its value does not affect routing.

The optional `read_only_seats` list and `seat_purposes` map reference seats
already mapped in `seats`. A purpose describes the seat's job in one sentence.
When a policy is supplied, workflow loading rejects an `access: write` node
on a read-only seat with `workflow-read-only-seat-writes`.

At the start of each pass, `loop` reads its `--policy` file and compares its
SHA-256 content hash with the previous reading. The same decoding and checks
used at startup must pass before the new policy becomes the policy in force
(the last policy accepted by this loop). Every dispatch planned from that pass
uses it; a request already running keeps its original backend and command.

Invalid or unreadable content prints the startup refusal and leaves the
accepted policy unchanged. `tillr view waiting --root <repository>` adds one
policy warning row explaining why the disk policy is not in force, including
with `--json` or `--history`. The warning clears on a valid reading. `why`
uses the accepted policy when inspecting that repository, rather than trying
to plan with rejected disk content; an explicit different policy file still
means a standalone preview.

Each changed reading, accepted or refused, is appended with the previous disk
hash, new disk hash and accepted hash. A missing file has an empty disk hash.
The loop also prints the complete seat mapping in force on one line. These
records and the atomic accepted-policy snapshot live under the repository's
loop directory:

```text
.tillr/loop/backend-policy-changes.jsonl
.tillr/loop/backend-policy.json
```

Inspect the accepted snapshot, including seat mappings and shares, all backend
models and efforts, and item overrides, without activating disk edits:

```sh
tillr drain policy --root <repository>
tillr drain policy --root <repository> --json
```

Before a loop has recorded a policy, this face refuses explicitly. After the
loop stops, it shows the last accepted policy; it does not claim the loop is
still running.

## State invariants

- **`plan` writes nothing.** A preview that moved state would make "what would happen" and
  "what happened" the same question.
- **The loop owns the order and the stopping conditions, never a step's judgment.** The
  gate, the land and the verdict are programs the caller names.
- **There is no pass, spend or token budget.** The pile is the only throttle: if work should
  not be done, it does not go in the pile. The loop is a daemon by default: when
  nothing is dispatchable and no run is in flight, it waits for a new item, a changed
  hold, or a trunk move. It prints the held count and the first five items with their
  reasons once, then stays quiet until something changes. SIGINT or SIGTERM stops it
  with exit code 0 and a final operator-stop line. `--exit-when-drained` opts into
  stopping with `queue-drained` and exit code 0.
- **The loop drains the queue the documents declare.** Before deciding anything at a
  new trunk commit, `loop` runs `tillr-item sync` (the `--item-bin` binary) against its
  `--root` and `--store`. The result's `synced_sha` and a report line name that commit.
  Passes at the same commit reuse both the sync and the lazily read open-question index;
  a new commit refreshes them. An item block committed during a trigger wait is therefore
  dispatchable without restarting the loop. A block with a finding is reported on the
  loop's output and in its result record. A finding or frozen definition attributed to
  an item holds that item instead of running its stale stored definition; other items
  continue. A sync that cannot run at all is a fault before any decision from that commit.
- **A loop that cannot start leaves a record.** Four lines on a stderr nobody kept is the
  failure this exists to prevent.
- **A held lane is never reported as a drained queue.** The two stop reasons are different
  answers and the loop says which it is.
- **A seat is a role, not a model.** The policy's `seats` map is where a role reaches a
  backend, and the item names the seat and never a model id. A plan whose items ask for a
  seat the policy does not map is refused before it builds a decision, so a seat is never
  defaulted to the pass's backend; the decision and the store's dispatch row record the seat
  beside the backend that answered. If the seat's backend is closed, the item waits for it.
- **An adapter's refusal of a brief is the brief's fault, not the backend's.** An adapter
  that refuses before any model runs prints `run-<adapter>: refused: <reason>` and exits 2.
  That run classifies as `refused`, not `infrastructure`: no backend failure is counted, no
  backend is closed, and nothing fails over. The item is held as `adapter-refused`, an item
  fault whose detail is the adapter's words and whose mover is the operator
  (`tillr drain clear-fault` once the cause is fixed). `tillr view lanes` prints the words on
  the lane's row. An exit 2 with no such words keeps the infrastructure class. An item's own
  text may contain two open braces in a row: staging carries it intact and writes the count of
  such pairs to the lane's `brace-literals` file, which the adapters' shared preflight reads.
- **A run id carries one counter and a bounded stem.** The second and later
  restart, fresh session or interruption of one node is `<id>-<count>-<kind>`, with the count
  padded to at least three digits and advancing past 999 without clamping or repeating words.
  A digest bounds the stem (preserving trailing `-judge` and `-cfix` tags) so every run id
  stays strictly under 120 characters.
- **Shares choose the backend for new work.** A shared seat can select a backend at
  admission. Neither shares nor item overrides send an infrastructure-failed run elsewhere.

## Backend quota admission

A backend row can name `quota`, an argument list for a command that reads remaining
quota without starting work, and `reserve`, the fraction of every window kept for
the owner (default 0, valid range 0 through 1). A row without `quota` is never
probed or held by a reading or reserve; a run's refusal still holds it until reset
or a successful operator probe. With reserve 0.2, a window at 0.8 used closes
admission; a window at 0.79 remains open. Closing a window does not change which backend runs the work.

```json
"quota": ["tools/tillr-run/backends/claude/quota"],
"reserve": 0.2
```

The command runs from the drained repository root and answers one JSON document:

```json
{"windows":[{"kind":"five_hour","fraction":0.8,"reset_at":"2026-11-19T13:00:00Z"}]}
```

Windows must have unique kinds, nonnegative fractions, and future reset times.
An idle window with fraction 0 may have a null reset. Before planning dispatches
and quota continuations, the loop probes readings older than 60 seconds, changed
commands, and reached resets. Missing, malformed, failed, or stale readings hold
that backend instead of assuming unlimited quota. Every probe is stored in
`backend_quota_readings`; `backend_quota` holds the latest observation for planning
and the waiting face. A restart reuses a fresh reading. Commands are limited to
15 seconds and 64 KiB of output; stderr is not retained. Reserve changes apply
without a new probe. Removing a command clears its latest observation on the next
pass, not its history. Checks happen while choosing and claiming a backend, never
after staging; a claimed dispatch always starts. Reserve is a threshold on the
last reading, not a guarantee against runs overshooting it within one interval.
Readings and reserves do not stop already-running work. A failed designer resume
remains unsettled until its own backend is open, before staging its new session.

To report an early provider-side reset, run `tillr drain probe-backend` for the
same backend with a smoke check that exercises that route
([the probe](#recorded-backend-availability)). A success recorded after the refusal removes its active window so the next pass
can dispatch or resume held work. A `drain.backend-quota-reopened` event links the
refusal and the probe. Settling that refusal again does not undo the probe. A
failed probe leaves the window in place; a new refusal closes the backend again.
Quota readings and reserve checks still apply.

For a single-worker item, a recorded quota refusal stops other live runs on that
backend through the normal pause request. If the refusing run changed its head or
left uncommitted paths, it continues under the same run id and attempt in the same
lane and worktree, without resetting files or spending a retry. Only the completed
candidate is judged. A first-message refusal that left no work waits for the same
backend's window and starts a new run, without a continuation message. A refused
conflict-design run or a judge that returned no verdict also starts again only
on its recorded backend, even if the seat shares work with an open backend.

When the backend is closed, the loop records the paused run in `quota_pauses`, keeps
its lane and `running` position, and waits rather than exiting. The same backend
resumes the session when it reopens. The executor removes the pause row in the
continuation's claim transaction.

The [Claude quota command](../tillr-run/backends/claude/quota) uses one authenticated
HTTP GET, not a Claude model session. Its authentication, live measurement, and
zero-model-token cost are recorded in the
[Claude backend design](../../docs/design/tillr-claude-backend.md#direct-readings-and-the-owners-reserve).
`tillr view waiting` shows each probed backend, both fractions, reserve, and when
it reopens, including open backends and explicit failed/stale readings.

## Seat backend shares and selection

A seat is a workflow role; a backend is the execution service that fills it. Most
seats name one backend. To spread fresh dispatches across several backends, give
every entry a positive numeric `share`:

```json
{
  "seats": {
    "build": [
      {"backend": "claude", "share": 3},
      {"backend": "pi", "share": 1}
    ],
    "review": {
      "backends": [
        {"backend": "claude", "share": 1},
        {"backend": "pi", "share": 1},
        {"backend": "muse", "share": 1}
      ],
      "pick": "round-robin"
    }
  }
}
```

This is the seat portion of a policy; each named service still needs a row in the
policy's `backends` map. Equal shares alternate; shares of three and one assign three
of every four dispatches to the first service when both remain open. The policy check
names the seat when refusing zero, negative, nonnumeric, or missing shares in a list
that otherwise gives shares.

A seat object has `backends` (the same list syntax) and an optional `pick`:

- **`in-order`**, the default: choose the backend whose next dispatch is earliest in
  the proportional count order; ties follow list order.
- **`round-robin`**: move forward through the list from the seat's last recorded
  dispatch. This gives equal turns even if entries name unequal shares.
- **`random`**: draw once per fresh dispatch with odds proportional to shares.

The choice is made inside the store transaction that claims the dispatch, not by
redrawing during execution. The dispatch row records `backend`; the complete request
records the adapter and model arguments. The `seat_dispatches` table records each
seat/run/backend choice once, including parallel workflow nodes and independent
agent reviews. Two loops using the same store and a restarted loop read the same
history. Failed transactions and canceled, never-started staging claims contribute no dispatch
count; completed parallel readers keep their history. A read-only `plan` retains
its ordered capacity preview; its proposed backend is not a committed draw.

The claim chooses before checking availability. If the choice is closed, the item
waits for that backend; the choice is saved separately from dispatch counts.
An item override still wins for a worker's fresh dispatch. Interrupted runs retain
their backend, and workflow restarts retain their request. A candidate waits for its
chosen independent review backend without being refused, discarded, or rebuilt.
Selection is implemented by `selectSeatDispatch`; `selectReviewDispatch` applies the
same stored history to an agent review outside an explicit workflow.

## Document-fed workflow prerequisite

**An item needs a document block before it is built.** This prerequisite belongs
to the document-fed Git landing workflow, not to the queue’s general readiness rules.
A store is document-fed when at least one stored item, in any state, has a block in
the tracked documents that sync reads. A store with no such item keeps its ordinary
seat and workflow routing. Closed, landed, and otherwise terminal items are never
counted as unshared or sent to triage. Neither is a generated item (`IsGeneratedItem`
in [`drain/defect.go`](./drain/defect.go), the one list): a trunk fixer (`fixer-main-*`), a tooling-defect
item (`defect-*`), a prose rewrite (`rewrite-*`), a revert's landing id
(`revert-main-*`) or a gate-budget item (`gate-budget-*`). The
loop or sync generates it, its criteria are its whole brief, and it has no block of its
own to be declared in. A generated item parked on a triage conflict has
that conflict closed before the next plan and returns to its worker; a gate-budget
item is closed instead, because a budget is a reported target and the loop files no
item for it.

A **store-declared item** is also exempt. The term is coined in
[tillr-item](../tillr-item/README.md#workflow-arguments-and-store-declared-items) for
an item whose record is its declaration: one whose record carries `workflow_args`, such
as a [document triage](#document-triage), or a [filed request](#filed-requests), whose
words only the store holds. No block can carry either, and sync does not count such an
item as unshared. Sending it to
triage would add a block for it to a document, a marker the owner did not ask for.

An open item without a block goes to **triage**, the design-seat run that shapes a
stored request into a document declaration, with reason `triage-no-block`. It runs
before the item’s own seat or workflow. Its fence admits only the Markdown document
its body reference names. The brief carries the stored title, reference, criteria,
and the [prose guide](../../.agents/skills/tillr-prose-style/SKILL.md), and offers
exactly three results:

1. A ready block with the same item id, committed for the normal gate and landing.
2. That block plus a numbered owner question explaining what is missing; after
   landing, the existing `question-open` rule holds the item.
3. A report naming a duplicate item or explaining why the work is already done.

Before the gate, the loop checks the pinned candidate with the same sync parser.
Any finding or warning on the triage block refuses it, including `prose-unmarked`
and every `prose-shape` warning. A landed block binds to the stored item by id,
and the item then returns to its own seat without an operator command.

A report with no block, a blocked run, or a request whose reference names no
repository-relative Markdown document under `docs/` becomes an owner `item.conflict`.
Sync reads only that document scope. Its report
remains visible under `conflict-owner`. The owner accepts a duplicate or finished
verdict by closing the item, or rejects it by answering the conflict. Answering
does not authorize another triage run for unchanged text: each version of the
stored title, body reference, and criteria gets at most one run. Changing any of
those fields permits one new run; changing rank or state does not. A triage run
that never ran does not count as that one run: one a loop stop interrupted (the
next loop marks it `drain.orphaned`) is no attempt, so the request is triaged
again, even when an owner hold was already recorded from it, and a claim that
staging cancelled before the run started is released before the next plan (see
"Lanes that staging refuses"). Waiting output
names the design seat while triage is due or running, then the owner and the
question or conflict. Startup sync output includes the `unshared_open` count.

## Prose rewrites before build

[Opt-in sync rewrites](../tillr-item/README.md#automatic-prose-rewrites) run on the
build seat rather than the design seat. Queue planning places trunk fixers first,
then rewrites, then ordinary items, retaining rank order within each group. This
keeps a rewrite ahead even when an ordinary item's rank is already the smallest
possible token; it does not renumber the owner's queue.

The original waits with `awaits-rewrite`, naming its rewrite as the next mover,
until the rewrite lands or the current document block passes the prose check.
When the original has more than eight criteria, its rewrite waits on the owner with
`prose-criteria-count`: the guide's limit cannot be met while preserving every
criterion. The owner must reconcile those original criteria first; planning reads
the current block again and releases the rewrite when the contradiction is gone.
Generated rewrite items bypass the missing-document triage prerequisite. Their
brief includes the entire [prose guide](../../.agents/skills/tillr-prose-style/SKILL.md)
and the source block's findings; an unreadable guide refuses staging.

Before the gate, the loop compares the original block in the pinned base and
candidate blobs using sync's parser. A changed `id`, `paths`, `blocked_by` or `seat`,
or added, removed or renumbered criterion, refuses the rewrite and names the change.
A missing block or a remaining prose finding also refuses it. Rewording a criterion
may not change its meaning; that obligation still requires review. A valid rewrite
lands without retiring the original block, so the original can be built afterward.

## After-merge heavy runs

With `landing_strategy` set to `after-merge`, each landing and loop startup trigger
one [heavy runner](../../docs/reference/gate.md#after-merge-proof-and-the-green-pointer).
It judges a detached checkout of the trunk tip, independently of dispatch. Workers
run at a lower CPU priority than the proof, which keeps the drain's priority;
this adjustment adds no time limit and stops no process. Landings
while it works owe one further run on the newest tip, not one run per landing. A
nonblocking process lock beside the gate's result store admits one writer across loops.
A busy lock does not consume the pending trigger. Judges inherit that lock through a
small supervising process: losing the drain owner closes a pipe, so that process ends
and reaps its judge before releasing ownership. A replacement can then safely recover
the detached checkout left by a killed runner.

Worktree creation, removal, pruning, and listing briefly share the lander's lock,
including its guard for lock acquisition and recovery of a dead holder. This keeps
Git from listing a registration whose files are still being written. A desk release
that also writes the store takes this lock before its SQLite writer, matching landing's
order. The lock is released before checks or judges run, so heavy judging and landing
remain concurrent.

Supervision requires Linux child adoption and stable kernel process handles. Adoption
makes the supervisor the parent of abandoned grandchildren, including checks a judging
program starts in separate sessions; the handles prevent signaling a reused process ID. It waits for
the kernel to confirm every descendant has exited before acknowledging cleanup, with
no cancellation timeout on that acknowledgement. When a declaration check fails or a
rolling program reports a failure on exit, recovery searches for the culprit and offers a
revert in a separate replay worktree while the proof continues. Recovery handles one failure at a
time and files any fixers at the end of the run. Attribution stays on the commit the
proof judged, even if trunk advances during the proof or a replay. Before filing, the
runner samples the trunk once: a check whose declared inputs were touched by newer
commits waits for the new proof. A named Go test has no declared read set, so the
runner replays just that test on the sampled tip; a pass defers the repair, while a
failure or no verdict leaves the finished red actionable. The runner does not restart
attribution just because another landing arrives. Automatic reverts are not attempted
from a proof already known to be behind the tip.
A check still red on a descendant tip can reuse its recorded first-red commit only if
that commit is in the first-parent range being proved, the green baseline and declared
reads are unchanged, and the recorded failure output matches. A known pass, changed
failure, or missing evidence prevents reuse. The green pointer still moves only on a
whole green run.

`heavy.cleanup-pending` and `heavy.replay-cleanup-pending` guard the proof and recovery
respectively and remain beside the result store until confirmation, recording the
supervisor's PID, start ticks, and checkout path. When a remaining guard names a
supervisor incarnation (`supervisor_pid` and `start_ticks`) that is no longer running,
the next run removes that checkout and clears the guard before continuing; while the
recorded supervisor is still running, or when a guard cannot be confirmed, it holds the
run, reports unconfirmed cleanup, and retains the checkout. A released lock alone is not
treated as cleanup evidence.

A stop does not carry an in-flight heavy trunk proof across the exit, and this is
deliberate. Worker runs survive a loop stop because each is its own `tillr-run` process
with a durable record that the next loop re-reads. A proof check is not: it runs under
this loop's subreaper supervisor, so ending the loop ends the check tree, and nothing
records how far a single check had got. What carries over is every check that had already
passed, saved with the exact tip, which the next loop's first-tick proof reuses
(`reused checks=`); only the check in flight starts over. A loop that exits repeatedly
therefore stalls the proved commit only by the length of the check it keeps interrupting,
which is why a fault that exits the loop on one item's leftovers (see the surviving
processes section) is a defect and not a cost of doing business.

An in-place build replacement is not a stop. The loop defers exec while the active
tip proof and any replay it owns reach a checkpoint. It records the verdict and
replays before releasing ownership, and a pending replacement prevents it from
starting another tip. On adoption, the replacement uses the saved green pointer
or a completed red verdict for the exact tip and baseline, rather than judging that
tip again. The loop still honors an operator stop during a proof.

Because the supervisor adopts every orphan its command leaves, it also reaps them as
they exit, on each `SIGCHLD`, rather than only after the command ends. It looks before
it reaps (`waitid` with `WNOWAIT`), so it never takes the exit status of its own direct
child, which is the command's verdict.
`TestASubreaperReapsTheOrphansItAdoptsAndLeavesItsOwnChild` in
[`heavyrun/heavy_reap_linux_test.go`](./heavyrun/heavy_reap_linux_test.go) covers both halves.

The runner judges every heavy whole-tree declaration in
[`tools/gate/checks/`](../gate/checks/), reusing the gate's remembered results.
`--heavy-gate` names the declaration judge (default `tillr-gate`). `--rolling` names an
optional rolling program whose checks the runner judges as well; it is empty by default
(`DefaultRollingProgram` in [`drain/trunk.go`](./drain/trunk.go)), so the declarations
are the whole proof. Missing programs or missing verdict evidence are reported as faults,
never silently passed.

The green pointer means every heavy declaration, and every check of a named rolling
program, passed at that commit. A landing gate's verdict covers only the checks its own
diff selected, and a commit an operator made directly carries none. A check a rolling
program reports as unavailable makes no claim and does not hold the pointer.

A rolling program's check that reports `STALE` judged nothing, so it holds the run at
unknown rather than passing it.

The proof compares its commit to the green pointer. It starts declaration checks whose
reads match changed files or contain them under a directory read ending in `/`, in name
order, then the remaining declarations in name order. Without a green pointer or any
affected check, all declarations run in name order. A named rolling program runs last,
or between the two groups when a changed path matches one of its watches. This changes
only the order, not the checks selected or the order inside the rolling program.

Completed passing checks are saved with the exact trunk tip under the heavy runner's
lock, before the next check starts. A replacement loop starts proof at its first tick,
even without a new landing, and logs `reused checks=` for passes it retains. A different
tip can retain a pass only when the check's inputs are unchanged. Failed, unavailable,
and interrupted checks do not count as passes.
Declarations are judged individually. A stalled check's successful retry is also saved
before the next retry starts, and declaration checks read those passes after restart.
A rolling program keeps its batch/spans contract and must supply its own incremental
persistence if it needs reuse inside an interrupted batch.

A run with no failing or unjudged check advances `refs/tillr/green` with a guarded Git
update: the value read before judging must still be the current value, and it must be
an ancestor of the judged tip. Unavailable checks are listed, not counted as passes.

A proof that judged no checks is never green: an empty gate program is refused with a
message that names it, and the pointer stays. Only the after-merge proof of the loop that
holds the repository's loop lock (`tillr-loop.lock` in the git common directory, beside
`tillr-gate/`) may move the pointer or write under `tillr-gate/`. A proof started from a
linked worktree, or by a process without that lock, refuses before judging anything,
because every worktree shares the one pointer and the one set of proof files. The
drain package's tests install a guard that fails any proof, loop lock or fixer path that
resolves to the real repository's common directory.

With `[push] what = "green"` in `.tillr.toml`, the advance of the pointer is also what the
loop pushes: it sends the pointer's commit to the trunk's upstream branch (`<green sha>:<ref>`,
never forced), so the remote never receives a commit the proof has not passed. Landings push
nothing in this mode. A failed push logs its outcome kind,
shows in `tillr view waiting`, holds nothing, and is retried at the next green advance (and once
at loop start) because "remote is behind green" is recomputed each time. `what = "tip"` pushes
the trunk tip after each landing instead; `"none"` pushes nothing. The repository's `pre-push` hook
runs as for any push, and one that publishes fork patch branches publishes them at their local
tips whatever the pushed commit is; see
[Pushing what the proof has passed](../../docs/reference/tillr-restart.md#pushing-what-the-proof-has-passed).

A push the remote refuses because it has moved (porcelain status `[rejected]` with `fetch first`,
`non-fast-forward` or `stale info`) is told apart from every other failure (a server decline,
a hook failure, an unreachable remote). Only a refusal triggers a second fetch and possible
replay. If the remote passed the ancestry check, the drain replays the unpublished trunk
commits one by one onto the fetched tip under the landing lock: linear, no merge commit,
never a forced push, authors and messages kept. It then moves the
main checkout with `git reset --keep`, so a path the move changes that holds owner content
stops it without changing the trunk or checkout. The loop tries to match replaced commits by
patch first. If upstream context changes a patch or upstream already contains the change, the loop
can use the replay's own old-to-new pairs instead. A superseded commit maps to the tip it would have
followed, without creating an empty commit. The loop records the mapping in `trunk_rewrites`,
moves matching `refs/tillr/landed/<run_id>` markers and `refs/tillr/green`, and raises no
`trunk-rewritten` hold. In green mode a clean replay records `remote-sync-proving` with
`rebased_tip` and names the heavy runner as mover. While that tip is on `main` but is not
contained in `refs/tillr/green`, the drain neither publishes the older green commit nor
records `push-ok` for it. A failed retry records its failure; an already-published retry
records that outcome in the hold's detail. Neither clears the hold. After proof reaches
the rebased tip, the next green push can publish and record `push-ok`. In tip mode the
push runs again at once. A checkout off `main` or a refused replay reset records
`remote-sync-worktree-dirty` and names the operator as mover. The loop retries once
`main` is checked out and the reset no longer clashes, or once a `push-patch-move-held`
hold clears, and only if commits remain unpublished in the configured push mode.
Unrelated edits do not have to be removed. A merge commit among the unpublished commits
still requires operator reconciliation. When the replay instead stops at a conflict,
the drain leaves `main`, green and the checkout unchanged and files a
[fenced repair](#remote-sync-conflict-repair). The drain never forces a push.
Before a due push and again after a refusal, the drain checks that the fetched remote tip
contains `refs/tillr/published/<remote>/<branch>` (or the remote-tracking tip if there is no
published pointer yet). Without either ref, a due push skips the guard fetch. A rewind or
divergent rewrite records `remote-rewritten` with `mover: operator`; the drain does not push,
replay, or move any refs. The drain records each accepted fetch even when no push or replay
follows or a subsequent push fails. If the fetched tip already contains green, the drain skips
that push. A successful push records the exact commit pushed. The pointer moves only forward:
a compare-and-swap keeps a late confirmation from overwriting a newer one. Worker boundary checks
ignore these pointer updates. The pointer is not consulted for commit identity.

Tillr stamps its own trunk landings with the configured Git identity from a scope a lane
cannot write. It enforces no identity policy on anyone else's commits, whether made locally
or fetched from another machine. Repositories that want stricter checks should run them in
their own gate.

Each `drain.post-landing-action` push event records a `kind` alongside the existing `detail`.
`push-remote-declined` means the server returned `[remote rejected]`. A failed push that
emits no ref status while a patch move awaits `tillr drain acknowledge-patch-move` records
`push-patch-move-held`. Other failures record `push-failed`. The event keeps `since_ts` across
failures until `push-ok`, and includes the last known `remote_tip` and unpublished green and
trunk counts when available. `remote-rewritten` gives the fetched `remote_tip` and the old
baseline as `previous_remote_tip`. The waiting view still reads `detail`.

A failing check holds the pointer and is run alone at the pointer and by bisection on
recorded landings in the range. A rolling program's span IDs retain their kind and label,
so a replay hands the program its own selector rather than its stable report ID. When the range cannot identify a single culprit (the base is red,
there are no recorded landings, or the last landing is green), a fixer names both full
commit IDs of the observed range and says attribution is unavailable, including the
check's needs and tools. The last recorded landing is checked before narrowing the
search, so an earlier red landing is not blamed when the last one passed. Otherwise the first
red landing receives a fixer: an item that repairs the broken trunk, containing every
check attributed to it. Its path hints combine the checks' declared sources and tests
with the paths touched by every commit since the last green through the current tip, even
paths later reverted. A rolling program's checks take their read paths from the reads and
watches its spans declare, not from their recorded output writes. The hints exclude the gate's own code. The
fixer follows the same deny-list as any other worker.

A red that names no landing still gets a mover. When the check is red at the pointer, when
there is no pointer, or when attribution itself answers no verdict, the runner files one
fixer against the tip. Its brief says why no landing is named, in the runner's words, with
the judge's recorded output. The run stays `red` and the pointer holds; such a red is never
`unknown`, and the fixer is filed against the tip. Attribution replays the check with
the replay program (`--replay`), which judges the one named check on the whole tree at
that revision. A stopped loop is not an
attribution failure: it files nothing, and the next run judges again.

A later red tip for the same check attaches to an unresolved fixer rather than filing a
second repair, matching an open fixer by the checks its criterion
names, because its id carries the earlier red commit. The attachment records the new observation and combines its paths without
clearing an existing owner hold. Both filing and attachment snapshot each failing check's
output and triage packet under `<lane-root>/.fixers/` and record the paths in a
`drain.fixer-logs` event, whether the red check came from an after-merge declaration or
rolling-program proof, a before-merge rolling run, a candidate gate acquittal, or a `trunk` obstacle
replay. When `LaneStager` stages a trunk fixer, it copies each recorded check log (along
with any whole-log `.log` sibling of a `.triage.log`) into
`<root>/.tillr/loop/<run-id>/fixer.logs/` and populates `## Facts carried` with the failing
cases, file and line locations, assertions, single-case reproduce commands (or bounded log
excerpts when no case was parsed), and the per-check log paths kept with the run, so the
fixer runs the failing test directly instead of rerunning the whole suite. A blocked fixer reaches the owner only by a complete
obstacle (see [A worker ends with its commit or a named obstacle](#a-worker-ends-with-its-commit-or-a-named-obstacle)):
a `definition`, `owner` or `tooling` obstacle goes to the owner with a question the
loop writes from it, because a generated item has no design block to amend. A
`NEXT-ACTOR: owner` line is read the same way. Any other blocked report is answered back to the
fixer's own worker, whose brief states this rule; it never reaches the owner by default. `tillr drain why` shows such a
hold with the loop's question as its reason and the owner as its mover (answer the conflict,
or close the item); a generated item has no document, so its hold never says to name or
triage one.

A green proof releases the trunk whatever the fixer's own state. A green run records
the checks it passed beside its proof time. Before each plan, an open fixer is resolved
(`resolved-elsewhere`, with a `drain.fixer-proved` event naming the proof) when the
proved commit is at or past the fixer's red commit (by ancestry, a recorded rewrite, or
its patch-matched replacement after an accepted rebase), the proof was recorded after the
fixer's last red observation (its filing or a later attached tip), and every check the
fixer names is among the proof's passed checks. The `trunk-broken` holds behind it are
then gone on that plan, and an acquittal waiting on it is released once the selected lane
base contains the proved commit. A proof record that lists no checks
resolves nothing. The tip run's own passes are proof too, check by check: the runner
records when each check passed on the tip (`heavy-progress.json`), so a stale fixer, one whose named
checks already pass on a tip containing its red commit, is resolved by those passes
while the rest of the run is still going or is red for another check. A fixer that names
a check both by its kind (`tillr-docs`, the replay's selector) and by a labelled id of that
kind (`tillr-docs/every-face-...`, the only spelling a proof records) is covered by the
labelled passes. A passing Go suite covers the named tests from that suite; a failed or
unavailable suite does not establish a pass for a test it does not name. Closing a fixer
(for instance, after landing its fix by hand) releases
every item held behind it on the next pass without an operator retry; a fixer resolved
without a landing while items are still acquitted behind it gets the proof recorded (its
state is left alone), and those items are released once the selected base contains that
proof. A fixer that names no check needs a whole green run. The
waiting face names the fixer, its failing checks and whether it is waiting on the owner.
With no pointer yet the report says there is no proved baseline to accuse a landing; it
does not claim that a nonexistent pointer was judged red.

With `auto_revert` on, a red that the bisection pins on landing L is reverted before any
fixer is filed
([`OQ-GP1`](../../docs/design/tillr-graph-is-the-path.md#OQ-GP1) of the graph design). The runner builds
the inverse of L on the tip in `<lane-root>/.revert/worktree`, gates it with the fast gate as
item `revert-main-<sha12>`, replays the failing check on it, and lands it through `tillr-land`
only when all three pass. L's item moves from `landed` back to `in-queue` with a reason, and
its run carries a returned `reverted` ending with the check's output, so the next pass hands it
back to its worker (a fresh session on the same backend). Its next gate runs that check as a
required one until it lands again. L is not reverted when it is a revert, a fixer's landing,
or a landed item's dependency. If the attributed check still fails with L removed, even
when the fast gate also refused the revert, the loop records that the attribution did not
explain the red, drops the remembered culprit, and searches again with fresh replays. It
offers a revert for a different verified landing; if the fresh search cannot explain the red,
it files a fixer against the tip without blaming L. Other revert refusals file the fixer with
the reason in its brief. The loop log reads, for a reverted red:

```text
loop: heavy unit red from landing 1a2b3c4d5e6f (item item-x); reverting it on tip 7a8b9c0d1e2f
loop: reverted 1a2b3c4d5e6f as 3c4d5e6f7a8b; unit passes on the tip with it
loop: reverted 1a2b3c4d5e6f, item-x back with its author
loop: heavy run green at 3c4d5e6f7a8b
```

and, when it is not possible,
`loop: heavy unit red from 1a2b3c4d5e6f: automatic revert not possible: <reason>; filing a fixer`.
Each attempt is a `drain.revert` event; the landings history of `tillr-view serve` (`/api/v1/landings`) shows it as a `revert` row, and
the item waits as `landing-reverted` until the pass that returns it.
The revert also withdraws what the reverted landing settled: the item's criterion states
are cleared with an `item.verdicts-withdrawn` event naming the landing and the revert
(`tools/tillr-drain/drain/revert_withdraw.go`), so the item is as amendable and
dispatchable as before it landed and a design answer that amends its block syncs. The
rule is applied to the records before each document sync too, so an item a revert
returned with its verdicts standing is repaired
at the next sync, which every loop start runs. The log reads
`loop: withdrew the verdicts of reverted landings: item-x (1a2b3c4d5e6f)`.

A heavy run logs as it goes, not only when it ends. Each declaration check's verdict is a
line as the check ends (`loop: heavy <check> <result> in <time>`), and a rolling program
gives one line with its failing checks. A failing check starts the culprit search with
`loop: heavy <check> failed at tip <sha>; searching for the first red landing since green
<sha>`, and each replay is two lines: where and why it replays (the green base, the last
candidate landing, a bisection step, or a candidate's first parent), then its verdict and
time, or `no verdict after <time>, because <reason>`. A landing is named only when the
check passes on its first parent and fails on the landing itself. If the watch-filtered
search skips over a failing parent, the runner searches the full first-parent prefix.
A replay with no verdict is not retried: the search ends there, says so, and files the
fixer against the tip.

A rolling program's whole output (stdout and stderr) is also written to a file under
the proof's log directory for the commit, `<lane root>/.rolling/logs/<commit>/legacy-proof.log`
for the proof and `legacy-replay-<check>.log` for a replay of one check. When a check
fails, the log line ends `; output: <file>` and a trunk fixer's brief names the file and
quotes its failing lines (`FAIL`, `panic:`) and its last lines. A failed check whose
program printed nothing is logged as `loop: heavy DEFECT: failed legacy check <check> left
no output`, and a replay that fails with no output says the same, instead of staying silent.

A generated repair item (a trunk fixer or a tooling-defect item) that reports `blocked`
is sent back to its worker, except on an identical repeat. Its ending is keyed by the
obstacle, not the prose: the trunk tip it was reported against and the obstacle kind its
report named (`drain.generated-block`). A second blocked ending with the same key goes to
the owner with the loop's own question instead of to the worker a third time. A moved
trunk is a new key. A `NEXT-ACTOR:` line followed by a reason (`NEXT-ACTOR: owner —
...`) still names its actor.

Budgets are reported targets, never deadlines. The summary names each overrun's measured
and declared times without changing the verdict. The pass gate budget (`DefaultGateBudget`)
bounds one gate run: a pass is compared by its longest run (`drain_passes.gate_max_ms`),
with the sum of its runs (`gate_ms`) reported beside it as cost. An overrun is a log line
and the observatory's `over_budget` flag; it files no item. A sampled stall is retried once on the
same tip; a second stall is red and files a fixer. A loop stop ends check processes before
removing their checkout. `tillr-view status --store <store> --root <repository>` reports
the green commit and its recorded proof time, recorded landings since it, first-red
attribution, unavailable checks, and overruns. The loop log and both waiting views show
the green commit's proof age. Proof times are stored by commit before the guarded
pointer update; a cancelled or refused update leaves the previous green commit's time
unchanged. With no green commit yet, the waiting view adds no proof row or reader fault.
A pointer without a recorded proof time explicitly says the age is unknown; the
commit's author date is never substituted. The shared report lives beside the result
store in the Git common directory;
the rolling watermark is neither read nor written on this path.

## Rolling trunk audits on the before-merge path

A rolling run happens only with `landing_strategy` `before-merge` and a rolling program
named by `--rolling` or by the definition's `rolling` hook. With none named, which is the
default, the loop asks for no run and writes no rolling state (`RollingConfigured` in
[`drain/steps.go`](./drain/steps.go)). A named program is due every eight landings, every
twenty minutes, and once before the queue drains.

The run judges a detached worktree of the trunk tip. With no watermark (the tip a rolling
run last judged green) the program's `{since}` is `--all`; later runs hand it
`--since <watermark>`. The program writes its check spans to the file
`TILLR_GATE_SPANS_OUT` names. The repository's own rolling program,
`python3 tools/tillr-loop/tillr-loop rolling {since}`, judges every heavy whole-tree
declaration at that revision with `tillr-gate checks --heavy-tree`, whichever range flag
it is handed; a matching remembered result may answer a check without executing it again.
The landing gate, `just done` and that program share one set of declarations and one
`tillr-gate checks` runner. The directory
[`tools/gate/checks/`](../gate/checks/) owns the set; there is no separate allowlist.

A green run advances the watermark, but that alone does not claim full coverage:
`rolling_runs` binds the outcome to its base and tip, and `rolling_coverage`, joined
by `run_id`, retains the fresh execution count (`ran`), reused verdict count (`cached`),
and every skipped check's identity and reason (`skipped_checks`). Skips include
checks assigned to the other tier as well as unchanged or unavailable checks.
`spans_state` says whether the program supplied readable evidence; a run with
no coverage row, or with absent or unreadable evidence, is not an empty audit.
The loop's rolling line prints separate ran, cached, and skipped counts, or explicitly
says counts are unavailable when the program supplies no readable evidence.
Red or unknown runs do not advance the watermark. A red tree check, like a red slower
suite, files one deduplicated trunk fixer naming the failing check ids. A declaration's
whole-tree read becomes the tracked file set at the red commit, excluding the gate's own
code; it never grants an unrestricted root edit permission.

While a trunk check is red and a fixer is open, every non-fixer item may still start and
run (there is no blanket `trunk-broken` hold on dispatch, per [`OQ-GP7`](../../docs/design/tillr-graph-is-the-path.md#OQ-GP7)); a non-fixer run
staged while a fixer is open carries a trunk-state notice in its brief naming the red
checks and open fixer so the worker does not spend its run on a failure it did not cause.
A trunk fixer runs the same gate as any candidate, over its own diff onto the tip.
After that gate accepts the diff, the named checks in its stored criterion must pass on
its tree, even if they are normally deferred to proof. A named check the fixer-gate
program drops (exit 77) is neither a pass nor a failure. A named failure returns to the worker; a check that
could not judge holds the candidate on the tooling-defect path.

Only landing waits, based on path intersection:

A candidate refused only by checks that also fail on its base and on the trunk tip is
checked against each red check's declared `reads` (defaulting to `["."]` — whole-tree —
where undeclared, matching `tools/tillr-gate/checkfiles.go`). When the candidate's changed
paths do not intersect any red check's declared `reads`, its verdict is promoted to
`pass-inherited` ("passed, with inherited failures", recording the inherited failures)
and it lands. When its changed paths
do intersect a red check's declared `reads` (including any whole-tree `"."` check) and
that check does not pass with it, the loop records `trunk-already-red`, naming the red
commit, checks, fixer, and intersecting paths, and blocks the item on `item:<fixer id>`
without a failing item verdict or a design conflict. The planner reports `trunk-broken`
and names that fixer. Once the fixer has landed and the selected lane base contains its
repair, the next pass restores the item's underlying state and clears its waiter. With the
`green` lane-base policy, an older verified pointer keeps the waiter until the heavy
runner proves the repair. A fresh run starts from the repaired trunk, without an
authorized retry; acquitted runs do not consume the failure walk. A fixer closed without
landing releases nothing until a proof passes its checks (see
[After-merge heavy runs](#after-merge-heavy-runs)). A check green on the base, or a
candidate with an additional failure of its own, keeps the ordinary refusal. There is no
landing cap (a `red_cap` policy key is ignored, per [`OQ-GP7`](../../docs/design/tillr-graph-is-the-path.md#OQ-GP7)).

A fixer can inherit only failures it does not name. A refusal containing even one named
failure returns whole to its worker. If an intersecting, unnamed failure would make the
fixer wait on itself or form a cycle of fixers, the check is attached to this fixer and
the refusal goes back to its worker instead. The next gate must judge the new name too.

The red base is only half the question when the trunk has moved past it. Work is always
built against the latest trunk, so the failing checks
are also judged on the current trunk tip. At the base and at the tip, the loop skips a
replay only when the after-merge proof or its progress record lists a pass for that exact
check and commit. A missing or red record still requires replay. The refusal line names
which evidence was used. When every check is red on the tip too, the fixer is filed for the tip. When any check passes
on the tip, the trunk is already repaired there: no fixer is filed, the loop rebases the
worker's commits onto the tip and records the tip as the lane's base, and the ordinary
refusal goes back to the worker saying the trunk is repaired at the tip. A tip the replay
could not judge confirms nothing. A fork-series candidate, whose base is the fork's own
HEAD, is judged on that base alone.

With `landing_strategy` `after-merge`, a confirmed red is then aimed the way the proof aims
one, unless an open fixer already names every failing check (the red attaches to it): the
checks are replayed at the green pointer, at the last recorded landing since, and by
bisection over those landings, and the fixer is filed for the first red landing. A red at
the green pointer, no recorded landing in the range (an operator's direct commit is not
one), a green last landing, or a replay with no verdict names no landing; the fixer stays
on the commit the red was observed on and its brief says why. Its fence holds the paths of
every commit from the green pointer through that commit, the failing checks' watches and
the candidate's diff.

## Nothing re-runs because an input changed

Nothing re-runs, is invalidated or is reset because a
hash, a document, a policy, a build or the trunk changed. A pass does not compare a held
item's block (including the referenced body
section), workflow file, allowed paths, backend policy or running tillr build with the
inputs recorded at its dispatch, so an edit to any of them does nothing to the item's
work. A prose-only edit to an item's block re-dispatches nothing, and an edit to the
block or fence of an item whose workflow is in flight does not settle that workflow
(`TestAChangedInputReDispatchesNothing` and `TestADefinitionProseEditReadmitsNothing` in
[`drain/readmit_test.go`](./drain/readmit_test.go),
`TestAnInFlightWorkflowIsNotInvalidatedByABlockOrFenceEdit` in
[`drain/worker_ruling_test.go`](./drain/worker_ruling_test.go)).

An item moves on an event that names it: the operator's `tillr drain retry`, a design
answer for the item, or the recovery of a killed run. The recorded inputs stay on each
dispatch. They are the claim's check that the pass which planned a dispatch read the
definition the claim applies, and they are evidence a reader can inspect.

A retry authorization that names a run other than the item's latest run does not
speak. Either the retry it authorized consumed it, or a newer run ended after it
was written. The item then routes by its latest run, as it would with no authorization.

A loop stop or fault leaves in-flight runs alone. `tillr drain stop` waits for the
supervisor to exit, not for those runs to finish. Their terminal records survive the
supervisor, and the next loop settles and judges each normally, including runs that
finished while no loop was running.

```sh
tillr drain stop --root <repository>                 # stop supervision only
tillr drain stop --root <repository> --end-runs      # also end in-flight runs
```

The explicit flag is the drain's only run-ending path. It sends SIGTERM to each run
face, not a `tillr run pause` request; the face ends its adapter and writes
its terminal record. Ending a run this way starts the item over, not the session.
The interruption is recorded before signaling. It is no attempt outcome: no retry or
failover is spent, regardless of how often it happens. The next pass dispatches the item again on its interrupted backend.

A stop acts on what this process domain (boot, PID namespace and user) can see and
reports the rest on stderr, one `loop: stop left alone:` line each. A session from
another domain whose heartbeat is more than five seconds past its recorded next pass
belongs to a loop that died without ending it, such as one killed with its container,
and is left alone. One whose heartbeat is current is a live loop this domain cannot
signal: the stop exits non-zero naming it, after stopping the local loops and, with
`--end-runs`, ending the local runs. A run owned by another domain is reported and left as it is. A run whose
run face is gone but whose worker survives is ended like any other: the interruption is
recorded, then every process of this user carrying that run and store identity gets SIGTERM.

A stop that arrives during a pass or during the loop's own checks takes effect without
waiting for an in-flight gate or trunk proof to finish, and ends the check tree before its
worktree goes. The loop observes both SIGTERM and `drain_sessions.stop_requested` (on its
session heartbeat, between pass steps, in trigger waits and during startup recovery),
cancels its run context, and exits `operator-stop` without recording a gate verdict,
rolling run or harness fault on the interrupted work. Unlaunched claims and unfinished
candidate examinations release their leases and examiner ownership on exit so the next loop
resumes the candidate or trunk proof (reusing heavy checks already completed). Candidate
gate steps and unowned heavy checks run under a subreaper supervisor so descendants placed
in separate process groups or sessions are terminated and reaped on cancellation rather
than holding inherited pipes open. A rolling program and the trunk-already-red
replay's program run in a process group of their own: cancellation first sends SIGTERM to the
program so it can end checks it started in separate sessions, allows
five seconds for that cleanup before escalating to SIGKILL, and kills what remains in the
program's own group before removing the scratch worktree. A rolling or replay program that
starts separate sessions must own their SIGTERM cleanup, because killing only the program's
group cannot reach those sessions (`TestStopDuringGateRunEndsPromptlyAndNextLoopResumesCandidate`,
`TestProcessStepsGateStopEndsSeparateSessionDescendantsAndRecordsNoVerdict`,
`TestStopDuringRollingProofEndsPromptlyAndNextLoopResumesProof`,
`TestStopDuringHeavyProofEndsPromptlyAndNextLoopReusesCompletedChecks`, and
`TestStopMidPassBetweenStepsEndsBeforeLaunchingNextStep` in
[`drain/stop_prompt_test.go`](./drain/stop_prompt_test.go)).

A pin (`refs/tillr/runs/<run>`) that a stop interrupts is finished rather than cut off,
bounded at thirty seconds, because the pin is what keeps a
candidate reachable after its lane is reset.

For an unbounded claim with no terminal record, replacement loops check owner and
stream evidence rather than elapsed time. Dead-owner detection covers an earlier Linux
boot, an ended latest drain session (including across in-place binary restarts), dead
local processes (comparing the recorded process ID and start time within the same Linux
boot, process namespace, and user identity, then verifying no worker carrying that run
and store identity survives), and claims from another process domain of the same user,
such as one a container restart left behind. Because a new container has a fresh PID
namespace where recorded PIDs name nothing, foreign claims are judged from files on the
shared mount: the loop is proven gone when its latest drain session has ended or when
its presence file (`<store>.loops/*.present`, opened for writing before the session is
recorded and held until exit) has no writer, and the run is live only while a process
holds `run.record.json` or `run.stderr` open for writing (checked with a Linux read
lease, which needs no timeout; absent files mean the face never started). Resumed
continuations (`run.resume`) carry the resuming loop's owner and are closed as
interrupted (`timed-out` / `lease-reclaimed`), with open `node_runs` marked
`interrupted`. Both single-worker runs and workflow nodes preserve committed and
uncommitted work under `refs/tillr/preserved/<run-id>`, return their lanes to rotation,
and offer the items again in the starting loop's first pass. Only a proven-dead owner
with no live worker is reclaimed; a claim without that evidence, including any whose
files cannot be judged, remains protected. A normal offline finish is still judged;
an external `SIGTERM`/`SIGINT` (`end_cause == "signal"`, or exit 130/143 without an
ending exception or readable `RESULT:` footer) classifies as `interrupted` on its own,
while a dead owner does not mark an already-terminal run orphaned or exempt a non-signal
crash from `infrastructure`.

### A worker ends with its commit or a named obstacle

A worker has no sanctioned way to end without
delivering (`D-JL11` in
[the judge and lifecycle design](../../docs/design/tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)).
A single-worker build run has two endings: its commit, or `RESULT: blocked` with a line
`OBSTACLE: <kind>` and the kind's required fields. The kinds route as follows:

- `definition` (`CRITERIA`, `EVIDENCE`): a design-seat conflict.
- `owner` (`QUESTION`): a design-seat conflict carrying the question. The design seat
  answers from the documents or objects `NEXT-ACTOR: owner`.
  (A report naming `fence` is read as a missing output.)
- `trunk` (`CHECKS`, `BASE`): a replay of the named checks on the run's base, and on the
  trunk tip when the trunk has moved past it. When every one is red there, the trunk
  fixer is filed and the item waits on it, as a red gate's acquittal does. When they are
  red only on the old base, the trunk is repaired at the tip: the lane is rebased onto it
  and the same session continues, told so. Otherwise the run is returned with what the
  replay found.
- `tooling` (`COMMAND`, `EVIDENCE`, `PATHS`): a defect item, `defect-<digest of the
  command>`, ranked with fixers. The item is held `tooling-defect` behind it and released
  when the defect item lands in the selected base, or when someone closes it. A second
  report of the same command attaches to the same defect item.
- `seat` (`TRIED`, `MISSING`, and an attempt in the worktree: a commit or an uncommitted
  change; a seat obstacle on an unchanged tree is a missing output): a `drain.seat-escalated`
  event moving the item to the next
  backend on the policy's `escalate` list for its seat. Every later run of the item uses
  that backend, starting fresh because the seat changed. Past the list's end, a design-seat
  conflict asks for a plan or a split.

`RESULT: failed`, a blocked report without a complete obstacle, and a report with no
footer are a missing output. With no commit, the run is refused as `no-op`, with the
missing part and the two endings in its detail. It goes back to its worker by the rule
below, and an identical repeat goes to the design seat. The code is
[`drain/obstacle.go`](./drain/obstacle.go) and
[`drain/obstacle_route.go`](./drain/obstacle_route.go), with tests in
[`drain/obstacle_test.go`](./drain/obstacle_test.go) and
[`drain/obstacle_route_test.go`](./drain/obstacle_route_test.go). Workflow nodes with a
document block still send a blocked report to the design seat.

When a filed request (`tillr item request`) ends `RESULT: blocked` with an `OBSTACLE`
line (other than a `definition` obstacle routed to a mapped `design` seat), the loop
records `drain.obstacle-hold`
([`drain/obstacle_hold.go`](./drain/obstacle_hold.go),
[`obstacles/read.go`](./obstacles/read.go)) instead of a missing-document conflict:
only `owner` blocks on `owner` with the `QUESTION` text; `trunk` holds on `CHECKS`
and returns automatically when a green proof
covers those checks; and `tooling`, `seat` (and `definition` when no `design` seat is
mapped) hold for `operator` with the evidence line. Both `tillr drain why` and
`tillr view waiting` display that reason and mover.

A writing node that reports a complete obstacle may leave uncommitted work. The drain
preserves the staged and working changes and points the next worker at them in its brief.
For a filed request held on `trunk`, that happens after a green trunk proof covers the
named checks. An explicit node scope still applies to both committed and uncommitted
paths; a completed candidate needs a clean worktree.

### A refused run goes back to its worker

A worker run's failure is not counted and does not park
its item (`D-JL10` in
[the judge and lifecycle design](../../docs/design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)).
When the gate refuses a candidate, a run ends with no commit, a candidate escapes its
fence, a landing is refused, a squash conflicts, or a landed change is reverted after the
full suite fails, the item returns to its worker with evidence, every time. Except for an
immediate conflict-fix run, the next pass dispatches it as `returned-with-evidence`. The
new run's brief carries a section on the previous attempt: the refusal's own words, each failing gate check with its log, and the two refs
that keep the earlier run's work (`refs/tillr/runs/<run>` for its committed head and
`refs/tillr/preserved/<run>` for uncommitted work). It starts on the item's own backend.
A gate refusal opens with the gate's answer on both backends (a parked `land` call's result
on pi, the first prompt of the resumed turn on Claude): each failing check, each failing
case's test name, file and line, its assertion and the command that reproduces only that
case, then the instruction to run that test and not the whole suite. It is read from the
triage block the gate wrote into this run's own log when the refusal is recorded, and says
"no failing case was recorded" when the log has none; the log path stays beside it.
The worker keeps the same lane and session when the adapter can resume it. The refusal
records that session and the original base before the lane is released; planning reserves
that lane for the returned worker, including after a loop restart. A workflow returns to
the producing node, with its original node boundary and accepted predecessor inputs;
predecessor records are referenced, not rerun or copied into fictional runs. The node and
its successors settle again before the candidate is admitted. The next turn has a
distinct run record, not an overwrite of the refused run. Its prompt carries the same
evidence as the fresh brief, plus the current run's identity and requirements. It must make
a new commit: the earlier refused head alone is not a new candidate. For a retained
checkout, the complete change from the original base is checked against the fence and sent
through the gate and landing again. Loop-owned item-block retirement is removed from the
retained checkout before the worker returns; the prepared head remains pinned as evidence.

A workflow evaluator's `verify-not-done` return carries each not-done criterion id
and its reason word for word (`<criterion id>: <reason>`, in criterion order, no
truncation). After `verify-not-done` (for both document items and filed requests),
the loop pins the candidate commit the verifier read and dispatches the build step
(`work` on the `build` seat) on that candidate commit with the verifier's
`not-done` findings in its brief word for word: the candidate head is pinned
(for a fork series, pinned out of the fork checkout first), the lane keeps it,
and the run resumes the builder's session when available on the original lane
(`resumed-worker`), or stages a fresh builder on that candidate commit on any
available lane, including fork checkouts (earlier nodes are carried as reused).
When `plan` expanded `work` into pieces (`build-pieces@v1`), the return inserts a
`work` build stage on the `build` seat before the evaluator, whose prompt
summarizes the planned pieces, and the evaluator runs again after it. The
whole-item diff, gate, and landing still measure from the item's own base;
per-node checks measure the repair commit atop the candidate.

The `plan` step runs again only in two cases: (a) a verifier's `not-done` reason
begins with `plan-wrong:` (any case) because the plan itself was wrong, which is
recorded as a replan return, or (b) the builder reports it cannot meet the findings
within the plan (`RESULT: blocked` with `OBSTACLE: definition` and
`EVIDENCE: plan-wrong: <reason>`, or a report line beginning `PLAN-WRONG:`), recorded
as `plan-wrong`.

Each verifier refusal returns all unanswered findings together, with the criterion
and full reason, including any reproduction commands and results. A `done` verdict
answers earlier findings for that criterion. The number of refusals never holds an
item. The drain holds for the operator only if the verifier repeats a finding given
to the worker and the worker returns a candidate with unchanged evidence for it.
It compares cited file contents for a file-scoped finding and the judged tree
otherwise; a new commit alone is not a repair. The hold names the repeated finding,
not the number of refusals, and gives the `tillr drain retry` command. Settlement
pins the candidate and releases the lane. An authorized retry restores the candidate
and resets repeat detection without dropping the feedback. `clear-fault` does not
release a repeated-finding hold.

For an opted-in ordinary item judge, `eval-failed` still holds a document item for
the operator, whereas on a filed request an ordinary judge's `not-done` retains the
builder's session and returns all `not-done` findings to the builder without an
operator hold.

The recorded decision's `worker_session_reason` explains the path: `resumed-worker` for
that continuation, `cannot-resume` for an adapter without a positive resume declaration,
`session-missing` when the lane's handle is missing or does not match, or `seat-changed`
when the backend or model does not match. A retained lane occupied by another run or
removed from the offered set uses an offered lane and records `lane-unavailable`. A workflow
whose producing node cannot resume restarts at its entry; `workflow-restarted` distinguishes a fresh graph from a continued node when
the entry and producing seats happen to share the same backend. These fresh paths carry
the evidence in the brief.
A harness that refuses to open the retained session before producing an answer is replaced
by a fresh run, with `resume-failed` recorded and the same evidence carried forward. Pi
opens an existing session explicitly rather than using the session-id option that creates
one when the id is gone. The behavior and its regression tests live in
[`drain/worker_session.go`](./drain/worker_session.go) and
[`drain/worker_session_execution_test.go`](./drain/worker_session_execution_test.go).

What stops endless repetition is an identical repeat, not a count. A refusal records its
evidence when it is written: the failing gate checks, each with a digest of its normalized
log, or for a run with no commit the uncommitted paths and a digest of their content, or
otherwise the refusal's normalized words. Normalizing drops the run id, timestamps,
durations, full hashes and temporary directories, and keeps messages and line numbers.
When an ending's producer (the seat it ran for), cause and evidence equal those of any
earlier ending of the same item, the item goes to the design seat with both runs' reports,
gate verdicts and refusals, as a run that reports blocked does. Two endings that differ are
never a repeat, however many there are, and a refusal with no recorded evidence key
repeats nothing. An item with no design document still goes to the owner on a
repeat, because there is no document for the design seat to amend, and so does an item
whose backend policy names no design seat (a design route there would stop the loop for
every item). When that item is a [filed request](#filed-requests), the identical-repeat
hold names `tillr drain retry` to deliver a revised instruction in the next run's brief
rather than asking to amend a nonexistent block. Every recorded retry reason and answered
owner question (from `tillr drain retry`, a settled document question, or an item-scoped
owner decision) appears in subsequent briefs under `## Answered owner questions` or
`## Retry reasons`, with who recorded it and when
([`drain/retry_brief_test.go`](./drain/retry_brief_test.go)). The run named as repeated is the latest earlier match, so a repeat after the
owner put the item back and authorized a retry raises its own conflict on the new pair;
the older conflict does not hold the item and is not a reason to skip. The rule is the pure
function `repeatedEnding` in [`drain/returned.go`](./drain/returned.go), with table tests
in [`drain/returned_test.go`](./drain/returned_test.go); the loop's side is in
[`drain/worker_ruling_test.go`](./drain/worker_ruling_test.go).

A squash conflict returns to the candidate's session and branch when they remain available.
On a moved trunk the loop stages a conflict-fix run at once with the conflicting paths and
three-way markers; otherwise the next pass returns it with the conflict lines. A fresh fallback
starts from the latest trunk.

The second squash conflict with the same paths and hunks digest within the current retry cycle
(the runs since the item's latest `tillr drain retry`) is not sent back again. `restartConflicted`
(in [`drain/conflict_restart.go`](./drain/conflict_restart.go)) pins the candidate, resets the
lane to the current trunk, closes any open landing call on it, records the conflict as a restart
(`drain.refusal` with `restart: true`) and a `drain.candidate-discarded` effect that clears the
run's dirty and repair holds. The planner then dispatches a fresh attempt of the item's workflow
from its first stage with no session; its brief carries the item's source reference, says the
earlier attempt conflicted twice, names the conflicting paths, and holds no conflict-fix
instruction. A conflict of that fresh attempt parks the item for the operator (`conflict-owner`)
whether or not its paths or hunks changed, and an explicit retry starts a new cycle. Design
ruling `D-JL12` in [`docs/design/tillr-judge-and-lifecycle.md`](../../docs/design/tillr-judge-and-lifecycle.md).

A conflict-fix run is judged, fenced and landed against the trunk tip the fix was made on,
never against the base the worker started from. The fence check diffs the recorded base to
`HEAD`, and the lander runs `git merge-tree --merge-base=<recorded base>`; with the old base
the first would show the trunk's own intervening changes as the worker's, and the second
would conflict again on any line the trunk also changed, whatever the worker wrote. So
`conflictFix` (in [`drain/conflict.go`](./drain/conflict.go)) replays the worker's commits
onto the tip (`moveLaneOntoTip`) and resumes the session only when that replay is clean;
the staged base is then the tip. A squash conflict usually means the replay conflicts too, so
the usual case restages the lane on the tip and starts a fresh fix run (decision reason
`trunk-conflict`) whose brief carries the conflict markers; the worker's head stays pinned.
Restaging keeps the
fence check a plain diff of the item's own change from a base that is an ancestor of
`HEAD`, and no merge commit or half-resolved merge reaches the fix run. A fork or workflow-node
lane keeps its own base. Pinned by
[`drain/conflict_base_test.go`](./drain/conflict_base_test.go). For a reverted landing, the gated candidate's snapshot records
the landing run's session before its lane is released; a continuation uses the current trunk
and receives the failing check's output. A landing without a snapshot starts fresh.
Nothing counts these endings. A conflict's evidence key (`conflictEndingKey` in
[`drain/returned.go`](./drain/returned.go)) is its sorted conflicting paths and the digest
of its normalized hunks, so the same conflict against another trunk sha repeats and a trunk
that moved again inside the hunk does not. A workflow node's refusal keeps its node rule.
Machinery failures follow [recorded backend availability](#recorded-backend-availability),
not a dispatch count.

### Recorded backend availability

An infrastructure failure means the harness could not produce a judgable answer:
it failed to start, refused before running, or died without a report. The latest
terminal run on a backend closes that backend only in this class. A later run
ending in any other non-interrupted class reopens it. An external `SIGTERM` or
`SIGINT` (`end_cause == "signal"`, or exit 130/143 without an ending exception or
readable `RESULT:` footer) is recorded as `interrupted` and leaves backend
availability unchanged (neither closing an open backend nor reopening a closed
backend); single-run, sequential workflow, parallel read-only workflow, agent judge,
and workflow evaluator runs restart on the same backend without counting against
infrastructure failures or failing over. Three infrastructure endings are answered
by another rule and do not close a backend: a run the loop interrupted itself
(a stop, an orphaned run or a stale base), a refusal with a stated reset time
(the quota window closes and reopens that backend), and a resumed session that
is gone (a fresh session replaces it). The loop records the class once, from the
terminal event and its pinned evidence, in `backend_outcomes` and a
`drain.backend-outcome` event. It orders terminal events, not mutable run-row
metadata: spend corrections do not count as new outcomes, and deleting or changing
a report after classification cannot change availability. These facts include runs
from other items and judge runs. A restart, policy edit, elapsed time, or successful
quota reading is not evidence that a broken harness works again.

The loop saves the class where it settles a run, and when a loop starts for
endings that arrived while none was running. A reader that finds an ending with
no saved class (`plan`, `why` or the view, before the next save) classifies it
from the record and the log as they are, and does not save it. Once a class is
saved it wins over the report.

An infrastructure failure holds the affected item as `backend-closed`, even if
an alternate backend is open. The hold names the backend in `deferred_by`, with
`backend_hold_cause` and `backend_hold_mover` also shown by the loop and `why`.
The cause records the run, backend, exit or end cause, and last error words when
available. The operator repairs the backend and records a successful explicit probe;
a later non-infrastructure ending can also reopen it. Recovery admits the same
work only on the original backend.

If that run repeats the same infrastructure failure (same backend, end cause,
and normalized error words), the drain holds the item as `infrastructure-repeat`
with both runs' evidence. Reopening the backend does not clear this item hold.
Once the cause is fixed, the operator clears it with `tillr drain clear-fault`.
A successful explicit probe also clears an active refusal window for that backend;
ordinary run outcomes and quota readings do not clear that window. No automatic
probe or timer spends work on a closed backend. A `max_failovers` policy key
is accepted and ignored.

An operator can exercise the repaired backend with a smoke-check command and
record the result:

```bash
tillr-drain probe-backend --store .tillr/store.sqlite3 --backend <name> -- <smoke-check-command> <args>
```

Use a command that actually exercises that named backend's harness and route,
not a quota query or a command that merely exits zero. Exit zero records a
successful `drain.backend-probe` event and clears the closures described above;
every other exit records a failed probe and leaves those closures in place. Output
is discarded to keep credentials out of recorded state. The probe does not dispatch an item or consume its criteria.
Its cost is the command's cost: a harness/model smoke check can spend provider tokens
and quota, while the event write itself spends no model tokens. It is explicit,
never automatic. A retry the owner authorized is the only dispatch to a closed
backend: it starts once on the item's own backend, reopens that backend if it
ends in any other class, and leaves it closed if it fails in the infrastructure
class again. A probe completed before a later failed run cannot reopen it.
A report changed or deleted before the drain first reads the ended run can affect
that first classification; capturing the class at run completion is outside this
mechanism, and a successful probe or a later recorded run corrects availability.

A workflow failure retains the same node, attempt, base, and completed siblings.
Its ready continuation waits on the same backend facts, then walks the same order
when a backend reopens rather than faulting the graph or restarting earlier nodes.
The implementation lives in `readInfrastructureClosed`, `ProbeBackend`, and the
workflow continuation planner in the drain package.

### Owner decisions in documents

The owner-question regression set is `TestOwnerReportQuestionGateHoldAnswerAndRelease`
(publication through dispatch), `TestOwnerWaitingEqualsCommittedIndex` (waiting rows
before and after removal), and `TestOwnerQuestionMigrationPublishesOnceAndSkipsStagingReports`
(conversion of report conflicts).

Answer sync indexes the trunk commit, not the checkout. A question counts as
answered when the index marks it answered, or when it is absent from the
question list and its ID survives in the document's decision ledger. An open
entry takes precedence over the ledger. If the ID is in neither place, the conflict
stays open and the item is held `question-removed`. `tillr drain why` names the
missing question and counts the operator, not the owner, as next to act. Restore
the entry under the item's block with the same run marker and land it. The next
sync returns an open entry to `question-open` without publishing a duplicate;
a restored entry with the ruling releases the item.

An owner decision is a report's question together with at least two alternatives,
not merely an actor line. Design briefs ask for a sentence ending in a question mark
and bulleted or numbered alternatives under an Options heading. Explicitly lettered
alternatives and reports stating a ruling between two options are also recognized.
A diagnostic question followed by a file list is not a decision. Generated ids use the OC prefix (owner conflict) and a run-derived number, so
independent reports on one document base and compacted rulings do not share an
anchor. The document retains the original report without interpreting or answering it. Its numbered `vantage: question` directive is read through the same
shared Vantage index reader as ordinary design questions; settlement adds no second
question-state parser.

The owner answers in that document, by an edit or the Vantage review path. Mark the
question answered and save the ruling. After it lands, the next document sync and
planning pass release the item; the worker's dispatch closes the answered conflict.
An uncommitted document edit does not record an answer. If settlement restarts after
its question commit but before recording the candidate, the loop-written HTML
comment naming the item, run, and question recovers that same candidate and report
provenance without another commit.
There is no store command for the answer. Triage can declare a stored request and
its question in the document it names. If no document exists, the report and waiting
view name the item and ask the operator to supply or triage its document; settlement
never invents a document or a ruling.

To convert owner decision reports held as conflicts into document questions, run the
loop with its normal root, store, policy, and step programs, adding the conversion
switch. Learn those inputs from `tillr drain loop --help`:

```bash
tillr drain loop --root . --migrate-owner-questions
```

The switch runs only these conversions, not the rest of the queue. It replays each
eligible report as a command run, claims an ordinary lane, and publishes through the
same design settlement and gate. It prints each item and document written, or the
reason a conflict was left unchanged. Reports without a question and alternatives,
including stale-base reports, are not converted. Conversion leaves the conflict open in status `question`, just like settling a report.
The sync closes it as answered only after the owner’s ruling lands. A published question
holds only the item it was published for. With unchanged reports and
documents, a second conversion writes no new questions or conflict transitions. A
refusal is explicit and leaves the conflict unanswered; it never clears a hold before
publication.

Both `tillr drain why` and the waiting view count owner-waiting document questions
as one class and name their document and question id. Other holds identify the next
actor as the drain, the design seat, or the operator, with the action beside it. A
report conflict awaiting conversion is the drain's work, not an owner question that
the planning index cannot display.

### A design run that ends without answering

A raised conflict gets one design-seat dispatch, and the claim marks the conflict
`awaiting` that design run. Every ending of that run
moves the conflict, and no ending is counted: there is no attempt cap, no comparison of
what changed since the dispatch, and no timer. An item reaches the owner only through an
objection that names the owner. The rule is recorded in
[the judge and lifecycle design, Section 4.3](../../docs/design/tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9).

1. **A commit that passes the gate and lands is the answer**, whatever it changed: the
   item's block, its design document, or a sibling. The conflict is `answered` and the item
   goes back to its worker. A commit that removes or
   renames the block leaves no item for a worker, so it goes back to the design seat,
   naming the commit.
2. **A commit the gate refuses** returns to the same designer session with the failing
   checks and their output. Each repair is a new run record, with the refused commit
   restored from `refs/tillr/runs/<run>` on the original lane. Keeping that worktree
   path lets Pi open its existing session rather than fork it. A revised commit goes
   through the same fence, gate, and landing checks. Even identical gate refusals
   return to design without a limit. If the recorded session is gone or its adapter
   cannot resume it, the conflict explains why the repair starts a fresh session.
3. **An interrupted or killed run** — a loop stop (`drain.orphaned`), a lease reclaimed
   after its loop died, a signal — continues its recorded session on the same lane
   when available. Otherwise the conflict explains why a fresh session starts.
4. **An objection.** A design run that commits nothing names who acts next with a report
   line `NEXT-ACTOR: <actor>`. `NEXT-ACTOR: build` says the block stands as written: the
   conflict is `answered` and the item goes back to its worker. `NEXT-ACTOR: owner` says
   only the owner can rule. When the report states a question and alternatives,
   settlement writes a numbered Vantage question directly below the item's block,
   with the report's wording, run id, and date. This document commit passes the same
   fence, gate, and landing path as a design answer. After landing the live conflict
   is `question`: its document reference and publication commit are recorded, but
   its answer commit remains empty. The shared index supplies the `question-open`
   hold. The next sync after the owner's answer lands records `answered` and the
   answer commit separately; the worker dispatch then closes the conflict. A
   gate refusal leaves the conflict unanswered. A report about a stale base alone
   writes no question: staging remains the drain's responsibility.
5. **A run that committed nothing and named no actor**, even one that ends `RESULT:
   blocked`, is a missing output, not an objection: the design seat is dispatched again,
   and the conflict's text states the contract. It never defaults to the owner.

6. **An identical repeat goes to the owner, with a question.** Except for gate refusals,
   an ending that exactly repeats an earlier design ending does not return to design,
   because it would be sent the same thing. The comparison is the worker path's: the same
   cause and the same evidence key as any earlier ending, and no count. The key is the
   conflict plus a digest of the item's block on the trunk for a run that named no actor
   or removed the block, and the refusal's words for a fence escape or a refused landing.
   An interrupted or killed run never repeats. The loop raises an objection naming the owner. It is recorded
   as an owner conflict, and the waiting view names the action needed to put the
   decision in its document rather than presenting an owner with nowhere to answer.
   The conflict's text names both runs and the command that moves it:
   change what the design seat is given, then `tillr drain retry --item <id> --run <the
   later run>`, or close the item. The [design repeat tests](./drain/design_repeat_test.go)
   cover it.

A change to the item's block, its design document or the trunk raises nothing. A
design run's gate refusal that the trunk caused is the gate's to recognize; until it can
(the trunk-already-red replay has no verdict for a change check, which compares two commits), such a refusal goes back
to the design seat on each re-raise, and a run staged after the trunk is fixed passes.

Each re-raise except an interrupted one, and each repeat, writes one
`drain.design-unanswered` event naming the conflict, the run, its outcome, its base and
failing checks, its `cause` and evidence `key`, and, on a repeat, `repeat` true with the
earlier run in `repeat_of`.

The owner's command for a conflict held on an objection is `tillr drain retry --item <id>
--run <design run> --by <who> --why <reason>`, naming the design run the hold names. That
design retry records a `drain.retry.design` event with who asked and why, adds both to the
conflict's text, and writes no `retries` row, so the plan does not read it as a build retry.
The design run it earns is settled by the rules above. A design run the conflict does not
name is refused, and the refusal names the current one; a conflict the loop is still
settling, or has already raised, is refused as the loop's. The
[design retry tests](./drain/design_retry_test.go) cover the re-raise and both refusals.
Every `tillr drain retry` refusal names the exact command the face would accept, or says
that none exists and who moves instead (`TestEveryRetryRefusalNamesTheAcceptedCommandOrTheMover`
in [`drain/main_cli_test.go`](./drain/main_cli_test.go)). A triage conflict keeps its own rule, one triage run per
request version. The [unanswered-design tests](./drain/design_unanswered_test.go) cover
rules 2, 3 and 5; the [objection tests](./drain/design_objection_test.go)
cover rule 4 and the removed block; `TestADesignCommitThatLeavesTheBlockUnchangedIsGatedAsTheAnswer`
in [`drain/design_quota_test.go`](./drain/design_quota_test.go) covers rule 1.

The settlement reads a design run's block by its id. A rewrite item's block carries the
original item's id (the id without `rewrite-`), so an answer that retitles the block's
section is still that block. When the item's `body_ref` heading is missing on the run's
base, because the run was staged before the heading was renamed, the settlement compares
the block's own section between base and head. The
[design answer tests](./drain/design_answer_test.go) cover both readings.

An examination (the loop's settle or judge of a finished run) leases the run's lane with
no expiry while it works, and releases that lease once it ends, on every path. When
settling yields a candidate ready for the gate on its own lane, the same examination
records the `judge` decision and continues straight through gate and land unless a hold on
the item or a waiting interactive request for that lane returns control to the planner
([`drain/settle_judge_test.go`](./drain/settle_judge_test.go)). The write
that records the run examined (`drain.examined`) also releases the lane. Only a live run
face keeps the lease, because a face is a continuation that releases the lane itself when
it finishes. A run face is the `tillr-run exec` process the loop started. A process that
merely carries the run's markers, such as an MCP server the adapter left behind, does not
keep the lease. The loop reports such strays on one `loop: run <id> ended, but
process(es) ...` line and leaves them running, because `tillr drain stop --end-runs` is the
drain's only run-ending path. Each release writes one `drain.lane-released` event, for
every run and not only a workflow node. The event names the lane and its cause:
`examined`, `examination-ended`, or `reap`. The last cause is the safety net. A pass's reap
reclaims a lane that is leased with no expiry to a run whose examination is recorded done,
when no examination in this loop holds the lane, its examiner is absent, dead or this loop,
and its run face is gone. The reap prints one `loop: reclaimed lane ...` line for it.

The same reap covers a paused run whose continuation face died while its owning loop is
still live (or when a `run.resume` event cleared the dispatch owner) and no
`run.finish` was recorded. When the run's last `run.resume` or `run.finish` event is a
resume, no examination in this loop holds the lane, and its run face is gone (in this
process namespace or by its stream files in another), a pass's reap reclaims that lane
with the cause `reap` and leaves the paused record as it was, so the next plan settles
the run and resumes it again.
`TestAResumedRunWhoseFaceDiedWithoutAnEndIsSettledAndResumedByTheNextPass` builds that
state. (When the owning loop session itself is proven gone across a relaunch, reboot, or
exit, Pass-1 dead-loop recovery instead closes the resumed run as interrupted, preserves
its work under `refs/tillr/preserved/<run-id>`, returns its lane to rotation, and offers
the item again.)

An examination that ended in a refusal (a `drain.refusal` at the land, gate, fence, no-op
or node-output step, after the run's latest candidate, assignment or retirement record)
has already released and recut its lane. When the loop stops between that refusal and the
`drain.examined` record, the next examination replays the ending: it reports `loop: <item>
run <run> already ended in a refusal; its examination concludes without a second gate`,
records the run examined, and neither gates again nor compares the recut lane with the
candidate it no longer holds. `TestARefusedRunExaminedAgainAfterItsLaneWasRecutIsNotConvicted`
in [`drain/refused_reexamination_test.go`](./drain/refused_reexamination_test.go) covers it.

## Owner edits and waiting landings

Uncommitted content in the main checkout belongs to the owner, including edits
made while runs are in flight. Boundary snapshots do not attribute that content
to any run. Writes into another lane's worktree still convict the writing run.

When the lander predicts that the owner's content would conflict with the landed result
(or finds the checkout in the middle of a merge, rebase or cherry-pick), the
drain retains the already-gated candidate and verdict, and releases an ordinary
lane after verifying its run pin, following [held candidate capacity](#held-candidate-capacity).
It records `trunk-path-dirty: ` followed by the sorted conflicting paths as an
ordinary item decision; the advice is "commit, restore, or change your edit on
<paths>; the landing proceeds as soon as it no longer conflicts". Dirty paths that
restore cleanly, and paths the landing does not touch, never delay a landing: the lander
carries the owner's content across, as [`git pull --autostash` does](../tillr-land/README.md#the-owners-working-tree).

Change the named paths. The next pass retries only the landing with
the original candidate and verdict, including after a supervisor restart; it
neither dispatches a replacement run nor reruns the gate. Each wait check uses the
lander's own prediction for the change reapplied onto the current trunk, so an
already-applied path or an edit that no longer conflicts does not keep blocking; a merge conflict reaches the lander's conflict handling.
When the owner commits the candidate's whole change, the lander finds it already
there and refuses the landing as `already-on-trunk`. The drain records the passing
verdict and moves the item to `resolved-elsewhere`; it does not hold the item as
`land-refused`.

An item can also reach a terminal state while one of its runs is still pending: it is
landed or closed by hand, or resolved elsewhere, after a loop that was stopped or killed
left the run unexamined. The terminal states are `landed`, `closed` and
`resolved-elsewhere`. The next pass that can take the run's lane settles it with the decision
reason `item-already-terminal` and judges nothing. It does not gate or land the candidate,
and it records no fault, refusal or conflict against the item. The item's state and verdicts
stay as they are. The pass pins the head while resetting and releasing the lane,
records one `drain.item-already-terminal` event naming the item's state, then removes
the finished item's run pins. No retry or re-admission is spent.

An item can also be landed by hand while its run is being examined. Every step that would
record something against the item reads the item's state again first: the gate, the land,
the blocked report, the conflict repair, and each fault, refusal and hold. The steps that
write in their own transaction read it inside that transaction. If the lander then answers
`already-on-trunk`, a hand-landed item stays `landed` instead of becoming
`resolved-elsewhere`. If this run's own landing finishes after the item was closed, the
landing is counted and the item stays closed.
An unchanged wait is not a candidate refusal or a failing verdict. Even
`--exit-when-drained` keeps
waiting while this retained landing can reopen. If the dirty path set changes,
the decision changes with it and the waiting clock restarts.

## Git movement and run convictions

Fetching, pulling, tagging, and committing on the trunk are ordinary activity by
other Git users, not evidence against a run. A trunk that advances keeps accepting
already-gated candidates: the lander reapplies each change onto the new tip without
another gate, following the [landing principles](../../docs/design/landing-principles.md).

Each pass compares the trunk with `trunk_history`, which stores the last observed
tip and a lost tip when that observation is no longer an ancestor of the current
trunk. After initializing object retention, an unchanged observation is read-only:
it does not compete with runs for the store's writer lock. New tips and rewrites
are persisted even when no candidate is waiting, so replacing the loop cannot erase an observed
rewrite. The loop retains those commits under `refs/tillr/trunk-history/observed`
and `refs/tillr/trunk-history/lost`, so reflog expiration and Git garbage collection
cannot erase the ancestry evidence. A rewrite holds only landings. Runs continue, candidates can be gated,
and already-gated candidates retain their commits and verdicts, not ordinary lanes
whose run pins have been verified. The hold decision is
`trunk-rewritten: <lost-tip> -> <current-tip>`, using full commit identifiers. The
waiting face names both tips and the mover `restore the trunk's history`.

Restoring history means making the lost tip an ancestor of the trunk again. The
next pass clears the lost tip and retries retained landings without another gate.
The loop keeps waiting even with `--exit-when-drained`.

**A `git pull` that rebases main** (the owner's `pull.rebase=true`) replays the
commits main holds and the remote lacks, and gives each a new sha, while the store's
landing rows, run pins and lane bases still name the old ones. The loop absorbs that
itself, at start and at each pass. When the recorded tip is no longer an ancestor of the
trunk, it finds the point where the old and new histories diverge and matches each
commit from there to the old tip with a commit on the new trunk by
`git patch-id --stable`, one to one and in order. When every commit matches it records
each old sha and its new sha in the store's `trunk_rewrites` table, with the time and
the evidence (the patch-id match and the trunk's reflog entries, which name the pull),
pins the old tip at `refs/tillr/trunk-history/accepted/<old-tip>`, moves
`refs/tillr/green` to its new sha if the pull moved it, appends a
`drain.trunk-rewrite-absorbed` event, and goes on with no hold. After that:

- A landing recorded under an old sha is found from its new sha (the first red commit's
  landing record, and so the automatic revert), and the heavy status counts it.
- Every ancestry question the drain asks (a lane's base, a run's head, a blocker's
  landing, whether the trunk contains a commit) follows an old sha to its new one when git
  no longer reaches it, so an idle lane based on the old trunk is reset and reused, and a
  released run's old head counts as on the trunk.
- `tillr drain why` and the heavy status (`rewrite`) say in one line when a rewrite was
  absorbed and how many commits it mapped:
  `trunk: absorbed a rewrite at <time>; <n> commits mapped to their new shas`.

A rewrite the loop cannot prove is not absorbed. If any commit was dropped, changed,
merged or squashed, it has no match; the loop records those commits and raises the
`trunk-rewritten` hold above, whose reason ends
`(no match on the trunk for <sha>, <sha>)`. Landings stay held and nothing is mapped. The
operator then restores the lost history (reset or re-apply what the pull dropped, so the
old tip is an ancestor again), or, if the change really was meant, accepts it with
`tillr drain accept-rewrite` as below. The loop tries again whenever the trunk tip moves.
The pull is safe whether or not the loop was running: the comparison is with the last
recorded tip, not with an event the loop had to witness.

A deliberate rewrite that kept the content, such as re-authoring commits or editing
their messages, is accepted instead of restored, with `tillr drain accept-rewrite`
(its `--help` lists the flags). It names the lost tip, the commit on the trunk that
replaces it, who accepts, and why. The command accepts only a rewrite it
can prove kept the content. The lost tip must be the one the hold names. The
replacing commit must be on the trunk (the tip or one of its ancestors). The two
commits must have the same tree, and the commits only the lost tip reaches must
number the same as the commits only the replacing commit reaches, so the rewrite replaced commits one for one rather than
adding, dropping or squashing any. A second shape is accepted too: a rebase onto a moved base, which is what
`git pull --rebase` does to landings that were not yet pushed. There the trees differ,
because the new base brought commits of its own, and the command accepts the rewrite when
every commit only the lost tip reaches has a commit with the same patch (`git cherry`) under
the replacing commit. In that shape `refs/tillr/green` must already be on the trunk; the
command does not carry it. Anything else is refused with a message naming
what differs, including each lost commit that has no match, and nothing changes. The command also works when no pass has recorded
the rewrite yet: an observed tip that the trunk no longer contains is the same
rewrite, unrecorded. Accepting it pins the lost tip at
`refs/tillr/trunk-history/accepted/<lost-tip>`, so the replaced history stays
readable. It clears the lost tip, makes the replacing commit the observed tip, maps the replaced
commits to their new shas in `trunk_rewrites`, and records a
`drain.trunk-rewrite-accepted` event that names who accepted the rewrite, why, and
both tips. When the rewrite moved `refs/tillr/green` off the trunk, the command moves
it to the commit that replaced it: the one as far below the replacing commit as the
proved commit was below the lost tip, refused unless it has the same tree and as many
commits beneath it. The heavy run cannot do that itself, because it refuses a proof
pointer the trunk no longer contains, and lanes stage from that pointer. The next pass retries retained landings without another gate, as after
a restoration: a gate verdict binds the candidate's base and tree, not the trunk.
Run it while no landing is in flight, after stopping the loop. A lander that was
already waiting with the old observed tip would record that tip as lost again.

An earlier rewrite that left a landed trunk commit unmapped in `trunk_rewrites` is
reconciled automatically at pass start when no trunk-tip rewrite is active: for each
unmapped non-fork `landings.trunk_after` commit that shares a merge base with `main`,
the drain matches it by unique `git patch-id --stable` in `merge-base(old, main)..main`,
writes the mapping into `trunk_rewrites`, moves any matching `refs/tillr/landed/<run_id>`
marker, and appends a `drain.trunk-rewrite-absorbed` event without changing proof
admission under `refs/tillr/green`. When no unique patch match exists, a dependent item
holds on `blocker-landing-not-on-trunk` with mover `the owner; restore the named landing
to main, or map its replacement with tillr drain accept-rewrite`. When no trunk-tip
rewrite is active, `tillr drain accept-rewrite` also accepts an unmapped interior trunk
landing (`--lost`) and its replacement ancestor on `main` (`--current`), refusing unless
both commits carry the same non-empty `git patch-id --stable`; accepting pins the lost
landing at `refs/tillr/trunk-history/accepted/<lost>`, updates any matching
`refs/tillr/landed/<run_id>` marker, records `(lost, current)` in `trunk_rewrites` with
evidence `accept-rewrite:landing`, and appends `drain.trunk-rewrite-accepted` without
moving `trunk_history` or `refs/tillr/green`.

The drain also passes its observed tip to the
lander with `--observed-trunk`; under the landing lock the lander refuses with
`trunk-rewritten` if the trunk no longer contains it. The lander also reconciles
the current durable observation and lost tip inside its locked store transaction:
a request that was already waiting for the lock cannot bypass a hold a later pass
recorded. This covers movement between the pass's observation and the landing transaction.

The run bracket compares snapshots from before and after execution. It convicts a run
on attribution, never on correlation in time. The owner and
other agents use ordinary Git on this repository while the drain runs, and none of it
convicts a run or holds an item: commit, pull, branch, tag, stash, fetch, gc, `git
worktree add` and `remove`, a fork export, or an operator's hand-written pin. Two snapshots
cannot tell that use from an escaping worker. A movement that the bracket cannot attribute
to the run is therefore *observed*. It is counted and named (up to 20) on the run's
`boundary.check` event as `observed` and `observations`, and the loop prints one `loop:
the repository changed outside lane ...` line for it. The check stays clean. Each movement
lands in one of three outcomes:

| Movement | Outcome | Why |
| :--- | :--- | :--- |
| Remote-tracking refs, tags, `refs/stash`, notes, and branches outside `refs/heads/tillr/`, `refs/heads/patch/`, and `refs/tillr/` | Ignored | Ordinary Git users move them all day. |
| Trunk commits or rewritten trunk history | Ignored | Trunk ancestry is a durable landing hold, not run guilt. |
| Target's own checked-out branch, registered sibling branches, `refs/tillr/runs/` pins, saved edits under `refs/tillr/preserved/`, the lander's `refs/tillr/landed/` markers, the lander's recovery copy of the owner's content under `refs/tillr/owner-recovery/`, the heavy runner's proof ref `refs/tillr/green`, and the two trunk-history refs (`refs/tillr/trunk-history/observed` and `refs/tillr/trunk-history/lost`) | Ignored | These are deliveries or concurrent loop work. |
| `refs/heads/patch/<fork>` moved to a commit that a `refs/tillr/landed/` marker written during the bracket names | Ignored | That is a fork-series landing: the lander marks the built fork head before it moves the patch branch. |
| The loop's own scratch worktrees under the lane root: `<lane root>/.rolling/worktree` and `<lane root>/.replay/<sha>` while detached, and `<lane root>/desk-<doc>/worktree` on that desk's own lane branch `tillr/<scope>/desk-<doc>` (which is excused too) | Ignored | The loop creates and removes them itself, for the rolling tier, the trunk-already-red replay and the review-comment desk lanes. |
| A registration that was already `prunable` (its directory gone) when the bracket opened, pruned during it | Ignored | `git worktree prune` removes it; the directory was gone before the run began. |
| Main-checkout uncommitted content, and hand commits on the trunk | Ignored | Content belongs to the owner, regardless of when it appeared. |
| Any other ref inside tillr's namespaces appearing, moving or going away (an operator's `refs/tillr/held/` pin, a fork export moving `refs/heads/patch/<fork>`, a hand-made `tillr/` branch, or a worker's rogue ref) | Observed | Snapshots cannot say who wrote it. |
| Any other worktree appearing or going away, anywhere outside a sibling's own lane directory | Observed | Agents and the owner add and remove worktrees at will. |
| The main checkout's checked-out branch changing | Observed | Checking out another branch in the main checkout is ordinary owner use. |
| Changed status of a path in a watched, idle sibling lane worktree | Convicted | That worktree is another item's candidate under the drain's own lane root, where ordinary use of the repository does not write. Siblings that became active are removed from the comparison. |
| The run's own lane worktree going away | Convicted | The lane is the drain's, and the run was its only assigned user. |
| Candidate changes a deny-list entry, or leaves uncommitted work past its candidate | Refused by the separate pre-gate checks | The deliverable must be a clean, committed change that respects the common protections. |
| A named store file changing in an adapter-only bracket | Convicted, in the standalone boundary check | The adapter does not own the store. Run brackets omit it because the run face writes its own records. |

Unexpected patch branch observations also hold publication, not the observing run.
The boundary event stores every such change in `patch_moves`, with `ref`, `old`, and
`new` commits; an empty endpoint means the ref appeared or was deleted. This list is
not capped with the diagnostic observations. Moves to a landing marked during the
bracket remain ignored.

`tillr view waiting` shows a `needs you` publication row naming the fork, observing
run, commits, and observation event sequence. After reviewing the change, the owner
acknowledges that sequence and fork:

```bash
tillr drain acknowledge-patch-move --store .tillr/state.db --observation 123 --fork pi --by owner
```

The acknowledgment records the owner and UTC time without erasing the observation.
It clears only that observation's fork: a later move or another run's observation
still needs review. A boundary event
without `patch_moves` is a diagnostic record, not a publication hold.

What this gives up is the stop of a worker whose only escape lies outside the lanes, such
as a rogue ref or a stray worktree. That escape does not hold its item, and the
observation names it for an operator. The separate deny-list check reads only what the
worker committed. It does not turn an observation into a refusal, and it runs before the
gate. A resumed segment is judged by the same run boundary rule.

## Faults that hold one item

A gate stall is not a candidate failure. The first `stalled` answer (gate exit 6)
retains the head with `gate-stall-rerun`, naming the check and how many gates were
in flight. The next planning pass with fewer gates, or no other gate, gates that
same candidate: no worker run, charged attempt or failing verdict. A second stall
on the same head tree holds the item as `gate-stalled`, with both observations.
`tillr drain clear-fault` appends `drain.item-fault-cleared` (`ClearItemFault`),
which clears `work.GateStall` in `readPendingHistoryState` while keeping
`work.Candidate` and `work.Done == false` and advances `gateJudgmentEpoch`
([`drain/events.go`](./drain/events.go)), so `plan` emits `ActionJudge` on the retained candidate and
re-gates the exact same head tree. When a trunk fix resolves a stall in a script the gate reads from the lane's worktree,
re-gating the same tree cannot pick up that fix; instead, supersede the held run
with `tillr drain retry --item <id> --run <run> --by <who> --why <reason> --acknowledge gate-failed`
so the candidate is rebuilt on the current trunk. Its retained evidence and
rerun state survive restarts: the first-stall transition and observations commit
with its judgment and measurements. Each gate starts with fresh measurement
artifacts, so a fast-tier stall cannot import remaining checks from a prior run.

A gate that ran and reached no verdict is not a candidate failure either. Its answer is
"error" (the gate's no-verdict exits), and the loop
records the wait as `gate-no-verdict` -- "the
gate judged nothing; the loop gates this candidate again" -- releases an eligible lane
after verifying the candidate pin, and gates the same commit again when a lane is available
([`drain/gate_no_verdict.go`](./drain/gate_no_verdict.go)). Nothing is counted, no fault is
written, and no retry or acknowledgment is asked for, so the hold's mover is the loop and
never a person. The `gate.verdict` with result `error` stays as the evidence of each
answer. A no-verdict answer that would repeat on every machine is a classification defect
in the gate, fixed there: a fork build's `FAILED: build` and npm's
lockfile refusal are verdicts, so such a red goes back to the worker as a gate refusal.

A gate program that refuses its own arguments is the one no-verdict answer that is not
re-gated. When the gate's stderr says `flag provided but not defined`, `unrecognized
arguments` or another usage refusal ([`drain/gate_usage_error.go`](./drain/gate_usage_error.go)),
the same program, arguments and installed binary will answer the same words on every pass,
so the loop holds the item as `gate-harness-error` after the first answer, with the gate's
first error lines as the reason, and releases the lane as it does for any held candidate.
There is no attempt count or time limit: one such answer is enough. The operator fixes the
gate and clears the fault, or runs `tillr drain retry` naming the held run.

Which `tillr-gate` judges a candidate: always the **installed** one, built from the trunk,
never the candidate's own build, because a candidate that built its own judge would certify
itself. A candidate that changes both a gate script (`tools/gate/`) and the binary it calls
(`tools/tillr-gate/`) must therefore keep the script compatible with the installed binary,
or land the binary change first and the script change after. When such a candidate's gate
answers with a usage refusal, the hold's reason says so in as many words.

A gate step that could not be run at all (`gate-harness-error`): the drain could not start
the gate program or read or record what it answered, so the same step would fail the same
way until someone repairs it. That, a recorded boundary conviction
(`host-boundary-violation`), a lane whose HEAD differs from the recorded candidate
(`candidate-record-mismatch`), or an item definition that cannot be synced
(`definition-sync-failed`) holds only the affected item. Other items keep running
and landing. A sync finding skips its entire document, so every stored item whose
definition comes from that file is held, not just the item named by the finding.
A landed design answer whose document, as the trunk holds it, does not declare its
item is held the same way (`definition-sync-failed`), never a loop stop; a
prose-rewrite item (`rewrite-<item>`) is read on its original item's block. Only that
item's own frozen definition refuses its answer: a sibling block frozen in the same
document does not. A `definition-sync-failed` hold ends by itself when the trunk and the
store let the document apply (`tools/tillr-drain/drain/definition_hold.go`): after each
whole-document sync, a held landed design answer (its conflict still awaiting the design
run that landed and was examined) is recorded again, and when it applies the conflict is
answered for the build seat and the hold cleared in one transaction; a hold the
whole-document sync wrote is cleared once a whole sync applies that item's block. Its
mover is the correction of the block on the trunk, never a hand `clear-fault`.
Three holds of this kind stand for per-item loop or staging faults, held on the one
item they touch so the loop goes on. The loop never stops on one
item's condition. `stage-failed` holds an item whose run could not be staged on its
lane or run directory (`ENAMETOOLONG`, `RunDir` or per-run staging preparation
failures, or `stageParallel`), retracting its unlaunched claim, freeing the lane,
and counting zero infrastructure failures. The mover in `tillr drain why` and
`tillr view waiting` tells the operator to fix the staging fault, then gives the
complete `tillr drain clear-fault` command with the store, item ID and fault sequence.
Fresh fork staging can move a clean lane checkout's `main` back to the delivered tip
when a run pin or preserved-work ref in that checkout still reaches its old tip.
After a landed release rebase, the lander's `refs/tillr/patch-history/<fork>/` pins
also prove whether that fork previously delivered the lane's entire history. Staging
then saves the old lane tip on `kept/pre-rebase-<sha>` and switches `main` to the
current delivery. If neither check proves it safe, staging leaves the fork work in
place and holds the item. The operator saves the commits on a branch, resets the
lane's fork checkout to the delivered tip, and clears the fault. A classifier's
pending writer keeps its selected delivery, but does not reserve the lane while
held: no writer has started, so other items can use the lane and the writer can
claim a free lane after clearance.
`decision-collision` holds an item whose planned decision could not be
recorded, because its decision id is already recorded with other columns.
`settle-failed` holds an item whose claimed examination hit an item-local error
while settling or judging its candidate, naming the step and the error. A mirror
regeneration failure has its own `pack-sync-failed` hold. The operator fixes the
cause and clears the fault; other items continue. Configuration errors, failures
to record a gate judgment, an unreadable trunk, or a store that cannot record the
hold still end the loop. If a completed workflow's final state move fails after
its completion was recorded, the next loop replays settlement without running
its nodes again. A run whose face could not start can also be held as `settle-failed`.
A repository candidate offered before the fork part of a mixed delivery is a
`delivery-order` refusal instead: the worker gets the required order in the next
brief. A closed item's pending run is cleaned up without judging or delivering its
candidate.
When the loop does end on an error, it appends a `drain.loop-ended` event (session,
stop, detail, exit code) to the store before it writes its result file; a clean stop
appends none.

The loop does not starve its own runs of the store's write lock
([`drain/gathered.go`](./drain/gathered.go), [`drain/claim_gate.go`](./drain/claim_gate.go)). A gathered write that misses a fact
rolls back, fetches the fact with no lock held, and waits as long as that attempt held the
lock before it begins again, so consecutive attempts hold the lock at most half the
time and another process's busy wait meets it free. A miss itself still takes the lock,
because the write's callback takes a `*store.Tx` and only a write transaction supplies
one: making a miss lock-free needs a read-only transaction the callback can run in,
which is a change to the store package. While a run the loop just started is claiming
its lane, gathered writes and the session heartbeat wait for it (at most two seconds,
and not at all once the lease shows its expiry or the face exits).
These holds survive a supervisor restart and do not record a failing
criterion verdict or spend a new model attempt. A retained candidate releases its
ordinary lane once its pinned commit is read back; worktree-dependent holds keep
the lane, as listed under [held candidate capacity](#held-candidate-capacity).

The waiting face shows the fault, its evidence, any run and lane involved, and one
complete command to clear it. Diagnose and correct the cause first: fix the gate,
inspect the boundary violation, restore the recorded candidate, or fix the item
block. Then use the command shown by waiting, for example:

```sh
tillr view waiting --store /path/to/state.db
tillr drain clear-fault --store /path/to/state.db --item item-example --fault 123
```

The fault number is the event sequence displayed in that command. A stale command
is refused if newer evidence now holds the item. Clearing one fault does not clear
another kind of fault on the same item or erase the original evidence. Gate recovery
rejudges the retained candidate; it does not dispatch another model run. When the loop
rebases a fixer onto a newer trunk, it records the prepared candidate before gating,
so clearing a gate fault rejudges that bound head instead of convicting its own rebase.
A fixer whose rebase conflicts is not a loop fault: the rebase is aborted in the same
step, so the lane is back on the fixer's head and never left mid-rebase. If every check
the fixer names already passes on the trunk tip (a recorded pass of the tip, or a replay
there), another commit repaired the trunk: the fixer is resolved elsewhere with a
`drain.fixer-proved` event, which releases its waiters, and its run settles as one whose
item is terminal. Otherwise the conflict is a landing conflict like any other, recorded
with its paths for the producer.
Clearing a
sync fault forces document sync again even at the same trunk commit, so a definition
that is still broken is held again rather than run from stale store content.

Clearing a fault re-examines the same recorded candidate, so it cannot help when the
candidate itself is what fails: a retained retirement candidate whose tree pins a stale fork
commit answers `gate-harness-error` again, a candidate record that names a checkout no
lane has re-raises `candidate-record-mismatch` at once, and a
`gate-stalled` candidate whose stall was fixed on the trunk after dispatch re-gates the
same tree and stalls again. The release for those is an operator's retry naming the held run,
`tillr drain retry --item <id> --run <run> --by <who> --why <reason>` (with
`--acknowledge gate-failed` when a `gate.verdict` was recorded, including `gate-stalled`).
Before each plan, a candidate held by `gate-harness-error`, `gate-stalled`,
`candidate-record-mismatch` or `land-harness-error` for its own run, whose item's latest
retry names that run and still speaks (the run is the item's latest and the acknowledgment
matches, as the plan requires), is superseded
([`drain/retry_supersede.go`](./drain/retry_supersede.go)): the candidate is pinned under
`refs/tillr/runs/<run>` (an existing pin is kept as it is), a lane HEAD that is neither the
candidate nor on the trunk, such as the loop's retirement commit, is pinned under
`refs/tillr/runs/<run>-superseded-head`, the lane's uncommitted work is preserved and the
lane is reset to the trunk unless another run holds it, and one transaction records
`drain.candidate-superseded`, ends the run's examination and clears the fault with the retry
named as its reason. Superseded pins are never pruned. The plan then dispatches the item
fresh on the latest trunk as the retry. The log reads
`loop: retry <retry> supersedes <run>'s candidate <sha> (<reason>): kept at <ref>; lane
<lane> reset to <sha>; <item> is dispatched fresh`. A boundary conviction is not superseded:
it is acquitted (`tillr drain acquit`).

A dispatch whose staging failed retracts its dispatch row and releases its claim
in one transaction. Its aborted staging remains in the append-only event history,
but a later plan may assign the same unstarted attempt to another lane without
colliding with that row.

## Staging without Git landing

A store's [definition](../tillr/definition/README.md#keys-types-defaults-and-readers)
sets `completes`. When it is not `landing`, planning names `work` instead of the
Git working-directory default, and staging creates an ordinary directory there.
An explicitly configured different relative working directory is retained. Staging
writes the brief and session identity beside that directory and keeps a per-run
copy, but creates no worktree, base sha, fence, or Git target metadata. A lane that
still contains Git staging metadata is refused rather than reset or silently reused.

The definition's `brief_template` is required for this staging. A relative template
path resolves under the drain's root; an absolute path is read as supplied. It does
not fall back to the Git `--brief-template`. Supported placeholders are `ITEM_ID`,
`TITLE`, `TASK`, `CRITERION_1` (the numbered criteria), `RUN_ID`, `BACKEND`, `SEAT_LINES`, `TARGET`,
`TARGET_KIND` (`plain directory`), `DATE`, `TIMEOUT_SECONDS`, `DURABLE_DIR`, `SETTLED`,
`FACTS`, `METHOD`, and `VERIFICATION`. Staging supplies no Git base, fence, or trunk
values; a template asking for an unsupported placeholder is refused before creating
the working directory. A missing item title or criteria remains an item-content
refusal, but an item needs no allowed paths for plain staging.

For Git landing, staging still requires `working_dir` to be `worktree` and uses the
existing Git brief template and boundary files. This rule belongs to staging, not
to `tillr run exec`: the runner continues to accept relative working directories
and refuse absolute ones. The [plain-staging tests](./drain/plain_stage_test.go) cover
the directory, planned request, rendered values, and Git-staging refusal.

## Lanes that staging refuses

Some staging refusals are about the lane, not the item it was offered. The next item
offered that lane would be refused the same way. There are three such conditions:

| Reason | What the lane holds | Remedy |
| :--- | :--- | :--- |
| `lane-holds-unlanded-change` | a commit that neither the base, the trunk, nor a run pin contains, and that no recorded landing names, which staging will not reset | land the change, or reset the lane as described below |
| `lane-branch-held` | its branch, checked out by another worktree | remove the holding worktree, or stage a different lane |
| `lane-holds-uncommitted-work` | uncommitted or untracked files when a pinned candidate is staged onto it, which staging will not overwrite | commit or remove the files, or reset the lane as described below |

The loop takes such a lane **out of rotation** for the session. That means no plan offers
it an item, and the item it was offered is offered the next free lane in the same pass.
That lane may have been idle when the pass began, and so be one the pass watches. The run's
own boundary bracket never watches its own lane, so the re-planned run is checked like any
other.
The loop writes one `drain.lane-refused` event, whose object is the lane, and prints one
line naming the lane, the reason and the remedy. A lane-owned refusal does not count
toward the infrastructure-failure streak that stops the loop.

While the condition persists, the loop writes nothing per pass. It re-checks the lane on
the next pass after the lane's HEAD, status or branch changes, and otherwise every ten
minutes on its clock. For `lane-holds-unlanded-change` the re-check is a probe: the
stager answers whether the lane still holds that change, with no claim, no run and no
write, so no item is allocated to a lane only to learn that it still refuses. A probe
that finds the same refusal keeps the lane out and writes nothing. For the other reasons,
or a stager that cannot probe, a re-check is one staging attempt: if it is refused the same
way, nothing is written. When the lane stages, or the probe finds it clear, the loop writes
one `drain.lane-restored` event and prints one line. A restarted loop reads the recorded
condition and does not record an unchanged one again. It prints the line once more for its
own operator. A lane is first found refusing by the one staging that meets the refusal;
that item's claim is cancelled with its node row, so it costs no attempt number (see
[What the attempt number counts](#what-the-attempt-number-counts)).

### What the attempt number counts

The attempt number names an item's next dispatch: `run-<item>-a<N>` and the `attempt`
field of a decision. It is one more than the highest attempt among the item's recorded
node runs, or one more than its count of terminal runs when the store has no node runs.
A dispatch that ends before a worker starts does not raise it. That covers a lane that
refuses staging and any other cancelled claim: each leaves
no node row behind, because cancelling a claim deletes the node rows that claim wrote for
runs that never started, so the next dispatch reuses the number. An attempt number is
therefore not a count of failed builds. It reads one higher than the number of retries
only after a worker has started in each earlier attempt.

A claim refused by lane staging leaves no `running` node row behind: the cancel deletes
it. For a count of work done, read `tillr view lanes` and the item entry in
the snapshot (`work`): the runs that started a worker, the attempts that reached a
verify-seat worker, and the verify refusals (`drain.refusal` with step `verify-not-done`).

`tillr view waiting` shows each lane out of rotation as a row named `lane:<id>`, with the
remedy as its mover. `tillr view lanes` prints the condition under the lane's row.

An ended run's surviving processes can also keep a lane out of rotation, with reason
`lane-run-children-live`. If marked processes (`TILLR_RUN_ID` and `TILLR_LOOP_STORE`)
still survive when an ended run is settled or examined, the loop retains that lane
without resetting or reading its landing files, holds only that run's item with
`run-processes-live` and the list of surviving processes (`<pid> (<command>)`), and
continues every other item and lane; if the item is already terminal, the lane is held
out of rotation with `lane-run-children-live` without touching the worktree. The store lease is released, but after any release or expiry
reaping and before offering a configured lane, the loop checks processes carrying both
`TILLR_RUN_ID` and `TILLR_LOOP_STORE` (the canonical store path) in their inherited
environment. Successful release events also identify ended runs when an allocation
left no terminal run row. A paused run continued as the same attempt is not ended: its
`run.resume` event is newer than its paused `runs` row and every release, so the
continuation's own processes do not hold its lane, and `tillr view running` lists it as
live rather than as a paused finished run. Once the continuation's terminal record lands,
any process it left behind holds the lane as before. Reading a landing offer or reconciling a landing answer takes the same path: both refuse
while a marked process survives and raise a typed error that settlement turns into the
hold above, never an error that exits the loop. A run's markers are swept again when the
run is executed: `tillr-run` ends every process carrying the run's own `TILLR_RUN_ID` and
the `TILLR_LOOP_STORE` it was started under before it starts a resumed execution, because
an earlier execution whose runner was killed before cleaning up left processes under its
own execution id, which the later execution's cleanup never looked for. The runner and its
ancestors are never ended by that sweep. This check applies to both landing and hook definitions. Only processes whose current working directory is inside that
run's lane directory hold it; a process elsewhere does not. The waiting row names the
process ids and a `kill` command. This condition is checked on every pass, including after
a restart, and clears with `drain.lane-restored` once none remain. Worktree changes and the
slow staging retry interval never bypass a live-process hold. The executor ends the
worker's remaining process group on normal exit; children that leave that group are not
ended by that cleanup and instead hold the lane if they remain inside it.

Item-owned staging failures keep their behavior: they count toward the streak. Each item
and lane prints its line once per distinct error, not once per pass.

A refused staging cancels the claim, and a cancelled claim is no attempt. The run never
started, so it has no terminal record, and the next dispatch reuses its run id. The claim
can also have moved the item's conflict: a design-seat dispatch marks the conflict
`awaiting` that design run, and a build dispatched against an answer closes the answer.
The cancellation undoes that move in the same write, so the re-plan offers the item the
next lane, as it does for an item with no conflict. A record whose move was left in
place reads as an awaiting conflict (`conflict-design`) with no design run alive, or as a
closed answer holding its item as `run-link-missing`. At
each pass the loop recognizes those records: the item's latest dispatch is examined
(`drain.examined`), its run has no terminal record, and the conflict is still as the claim
left it. It undoes the move, prints one `loop: <item> run <run> was claimed but never
started` line, and the item is dispatched again with no operator command. The
[unlaunched-claim tests](./drain/unlaunched_claim_test.go) cover the design run and the
build re-offered in the same pass, and the recovery of such records.

A **lane copy** is our term for a lane commit that is kept somewhere else. Staging resets a
lane copy rather than refusing the lane. Two things make a commit a lane copy. A run pin
(`refs/tillr/runs/<run>`, retained while the commit is absent from the trunk
under [ref retention](#ref-lifetimes-and-backlog-cleanup)) may contain it. Or a recorded
landing may name it as the landed head: a `land.landing` event with result `landed`. A
squash landing puts the change on the trunk under a different sha, so the trunk's history
never contains the candidate itself. Each reset writes one `drain.lane-copy-cleared` event.
Its object is the lane. Its payload names the commit, the retaining pins, whether the run
landed, and that run. An unlanded copy's change stays with its item, and the item restages
it from the pin. A lane holding a commit that is not a lane copy is still refused.

A held candidate re-staged on a newly assigned lane can be refused by that lane's state.
That lane then leaves rotation, as above. The candidate is released again, still pinned,
and is offered a lane in rotation. The examination does not stop the loop.

### Resetting a lane

Reset a lane only after you have decided its change should not land from that lane. The
change must be kept somewhere: on the trunk, or at a pin under `refs/tillr/runs/<run>`.
Then:

1. Confirm the lane is idle. `tillr view lanes` must show no run on it.
2. Confirm the commit is kept: `git merge-base --is-ancestor <sha> main`, or
   `git for-each-ref refs/tillr/runs` lists a ref at it.
3. Point the lane's worktree and branch at the trunk:
   `git -C <lane root>/<lane>/worktree checkout --force -B <lane branch> main`.
   The lane branch has the form `tillr/<scope>/<lane>`. Read it before the reset with
   `git -C <lane root>/<lane>/worktree branch --show-current`.

The next pass re-checks the lane, because its HEAD changed. No drain command runs these
checks and the reset for you.

## Where step programs come from

For gate, fixer gate, land and verdict, a configured command naming the standard
adapter at `tools/tillr-loop/tillr-loop` under `--root` runs the copy embedded in the
drain binary. The live working-tree file is not read at the next step, even after a
landing changes or removes it. Absolute script paths and relative paths under the root
select the same copy. Other configured programs run unchanged. Definition-hook invocations always run
the exact declared executable, even at this standard path. Automatic fork-gate
selection applies only to configurations without a hook program. Startup names that choice
for each step; command spans show `-` for the adapter supplied to Python on stdin.

The committed [adapter copy](./drain/tillr-loop.py) is compiled from the same sources
as the drain. Rebuild the tillr programs together and restart the loop to change its
standard step programs; an adapter-only landing cannot change a running loop's steps.
The [adapter README](../tillr-loop/README.md#step-programs-belong-to-the-running-build)
names the copy-regeneration command and the missing-field build-mismatch refusal.
The repository checks the adapter invokes still come from the judged tree, not this copy.

## Completion without landing

For a definition with `completes: "hook"`, an ended run goes directly to
`complete-item`. No candidate, boundary bracket, gate, lander, Git cleanup, or
Git command is needed. The hook receives the item, run, store, and root and
records criterion verdicts before answering. Its single JSON answer names a
finished `state`, a nonblank `reason`, and an optional `resolves_via` pointer.

The drain moves the item through the item state face with `--require-verdicts`,
`--reason`, the optional `--resolves-via`, and `--by definition:<name>`. The item
utility checks every criterion against a recorded verdict for its stored text
in the move's own write transaction. Missing verdicts hold the item as
`criteria-unjudged`; other invalid answers or hook errors hold it as
`complete-item-fault`. The waiting face shows those faults. Exit 1 leaves the
item unchanged under the existing `candidate-no-op` refusal rule.

Startup checks the hook's description and the item state flags, including
`--require-verdicts`. After a restart, an already finished item is the durable
receipt: the drain writes missing end-of-run records without calling the hook
again or entering Git. If the item is unfinished, completion starts again at
the hook. The hook and item subprocesses run outside store transactions.
Definitions completing on `landing` keep the Git-landing path.

## Step-program argument contract

A store definition may instead name one hook program. Its selected steps supply
command-line defaults, with explicit step flags (including empty values) taking
precedence. Startup checks the program's `describe` record, not synthetic candidate
calls. Unlisted hooks are never called; unlisted steps default to empty. The
[definition package's hook contract](../tillr/definition/README.md#hook-program-contract)
names the flags, records, and exits for all hooks. A definition without a hook
program retains the existing defaults and requirements.

A **step program** is the configured gate, fixer gate, land or verdict command that
receives the candidate's flags. For explicit command-line programs and definitions
without hook programs, its help invocation (`<command> --help`) must judge
nothing. Programs supporting startup argument validation advertise the literal
`tillr-step-argv-check-v1` in that help output and accept `--check-argv` alongside
normal arguments. This is the drain's contract, not the adapter's broader self-check.

With `--check-argv`, parse all arguments with the normal step's parser and return
0 if accepted or 2 with a diagnostic naming the refused flag if not. Do not read
candidate artifacts, open the store, invoke judges, write records or move refs.
Synthetic candidate values need not name existing files or commits: this mode
checks the argument interface, not the candidate's validity.

Both `tillr drain loop --check` and normal startup negotiate support using help,
then pass every candidate flag through this mode. The land check includes
`--observed-trunk`, `--base-ref` and `--message-from`; verdict checks include
`--store` and both verdict values. The fixer's named-check step checks `--only`
alongside the candidate flags. The [tillr-loop adapter](../tillr-loop/README.md)
implements the mode.
No run is dispatched before these checks finish.

A command that does not advertise support is named once as `unchecked` at startup
and receives no synthetic candidate invocation; the loop still starts. Help
failure also means unchecked, never evidence of supported parsing. A supported
command that refuses the check stops startup with a configuration fault (exit 2),
naming the command, its arguments and its diagnostic. At run time, a step's exit 2
likewise stops the loop as a configuration fault, not an external failure or an
item's failing judgment. Other step exit meanings remain unchanged.

## Faults that stop the whole loop

Fault stops are for failures that cannot be attributed to one item:

- The store cannot be opened, read, or written, including failure to persist a
  judgment, hold, or pass measurement.
- The trunk cannot be read, so no pass can select a trustworthy base.
- Settings are invalid or required shared machinery cannot start, such as a missing
  usable backend policy, an unmapped seat, or a document sync program that cannot run.

An item-local fault is not one of these stops. Operator cancellation and the explicit
`--exit-when-drained` setting remain normal ways to end the loop; an indefinite foreign
lane lease is still reported separately as `lanes-leased`, not a drained queue.

## Held candidate capacity

A held candidate is pinned independently of its lane. Gate stalls, ordinary item
faults, held conflict-repair staging, dirty-trunk waits, trunk-broken waits for a
fixer, and rewritten-trunk waits can release a lane only after the drain reads
back the exact candidate commit. Ordinary candidates use `refs/tillr/runs/<run>`;
fork candidates use `refs/tillr/held-forks/`, keyed by run, delivery ref and base.
The drain saves the
original fence, preserves uncommitted work, and records
`drain.candidate-release-started` with the exact reset destination before resetting
the lane. `drain.candidate-lane` records completed release. A restart finishes an
interrupted reset from that intent even if the hold was cleared in the meantime.
A failed pin, readback, preservation, fence read, or reset
keeps the lane and records the reason. Before changing a checkout, idle-hold
reconciliation revalidates its reservation under the store writer and claims a
no-expiry examination lease with the releasing process's identity. Operator
examination cannot take that lease. The reset and release receipt run under the
same writer transaction; interrupted releases use the existing dead-examiner
recovery before finishing their saved reset intent. Older idle holds are
reconciled before planning, so a ready sibling can take the released lane in that
same pass.

Clearing the hold claims any free offered lane. `drain.candidate-assigned`
records that claim before staging the pinned commit there; `drain.candidate-staged`
records completed staging. The assignment ends the earlier release, so a replay reads the
newly assigned lane as occupied. That is how a second hold on that lane
releases and resets it, like the first hold did on the original lane, so the candidate
leaves no copy on a lane it passed through. Restart recovery finishes incomplete staging, including
a checkout materialized before its event. The candidate's head, base, run identity,
original fence, and any passing verdict survive lane reuse. This starts no new worker
and spends no new model attempt. A retained passing verdict retries landing, not gating.
Staging checks cleanliness even when the checkout already matches the pin, so
inspection edits cannot be discarded by a landing. Operator examination uses the
same assignment and staging protocol, without requiring the released old lane;
setting aside a released candidate records its pin and does not touch a sibling
now occupying that lane.

For a workflow candidate, the drain restores the node base and fence separately
from the item's. Accepted node artifacts and run reports remain in their original run
directories; reassigning the candidate does not move or rerun the producers. For a
fork candidate, it resets the old fork checkout to its recorded base without
moving the delivery ref. Restaging uses the pinned fork commit, upstream refs and
tags, and binds both fences to the new lane's fork path. A dirty or unverified
workflow or fork checkout stays in place rather than releasing work that the
drain cannot restore.

These classes still keep their lanes:

| Class | Why the worktree is needed |
| :--- | :--- |
| Conflict repair owning the pending transition | The repair request owns the staged checkout; settlement must finish that request, not release the original candidate's lane. |
| Hold before a candidate is recorded | There is no recorded commit to restore; settlement or continuation still needs the original checkout and session. |
| Live or unsettled workflow member | Release waits for the member to finish and its claim to settle. |
| Host-boundary violation | The original checkout remains available for operator inspection. |
| Candidate-record mismatch | The unexpected HEAD must remain available for diagnosis rather than being reset to the recorded commit. |
| Unverified pin or unsuccessful preservation/reset | Release cannot yet prove the candidate and remaining work survive lane reuse; the recorded error names the remedy. |

A lane HEAD a release can restore is never a reason to keep the lane: the candidate head,
a failed repair's recorded reset, or an interrupted release's destination. The loop's own
document-retirement commit, whose parent and tree the candidate's retirement binding names,
is recorded but not pinned, so its lane is kept as `lane holds the loop's retirement
commit; the retry gates it in place`: the held item's own mover (its fault's remedy, then
`tillr drain retry`) examines that lane and gates the retired head. A lane whose HEAD is
none of these is kept as `unrecorded lane HEAD needs inspection`.
That keep is recorded once: a later pass re-reads the HEAD before claiming anything, and
when the same keep still stands it writes nothing.

`tillr drain why` and both waiting views say `lane released` or `lane kept`, with
the reason for a kept lane. The summary distinguishes `reserved` idle lanes,
`workers` with live finite worker leases, and `examining` lanes claimed for
settlement or judgment. The existing `running` count describes planner positions,
not a count of worker processes.

### Changing the ordinary lane count with work in flight

Run `tillr drain scale --root . --lanes 3` to set the ordinary lane count. The
running loop applies the saved target on its next pass without signaling workers.
The target is stored per checkout and used on unflagged starts and in-place
restarts; passing an explicit `--lanes` flag on a fresh start replaces the saved
target, and the loop's startup line states the effective ordinary lane count and
where it came from (`--lanes` flag, saved target, or default). Reserved express
lanes are unchanged.

Lowering the target stops fresh dispatch to lanes outside the new rotation;
`--lanes 0` takes all ordinary lanes out of rotation. Those lanes keep serving
work they already hold until it lands or the lane is released. `tillr drain why`
lists them as draining, with the item each holds. Once released, a lane outside
the rotation stays unused until it is offered again.

## Conflict repair that cannot be staged

A conflict-repair staging failure holds only its affected item as
`conflict-repair-staging-failed`; it does not cancel sibling workers. The candidate
commit is pinned before the lane is reset. A `drain.repair-held` event retains the
candidate, original conflict reports, exact staging error, input comparison, and next
action. An ordinary lane is released after the candidate's run pin is read back;
corrected inputs retry repair on any free lane. Worktree-dependent candidates
follow [held candidate capacity](#held-candidate-capacity).

Unchanged item definition, fence, backend policy, brief template, and running build
keep that repair held across passes and supervisor restarts. Queue rank, timestamps,
and trunk movement do not reopen it. Correct the reported staging cause in one of
those inputs; the next pass stages repair of the retained candidate against the current
trunk, using the current configuration of its original backend. It does not rerun the
original model attempt or rejudge the same original conflict. A successful staging still
stages one conflict-fix run for that conflict. The waiting view names the item,
staging cause, and this correction as the next action.

Successful staging transfers pending ownership to the repair request and its new base
in one store transaction before execution starts. A loop stop or crash leaves the
repair running; the next loop settles that existing repair's terminal record and
commit, and never starts the same repair ID cold or truncates its transcript.
Settlement and judgment use the ordinary pass transitions, so spend is counted once
and the launching examination cannot race the repair's settlement.

On exit the loop reports only the spend it accumulated while running, without
querying the store for a final snapshot. It distinguishes work started in this loop
from earlier loops' runs settled here. The dispatch records the launching loop's
identity, so a replacement loop does not claim the cost of a surviving worker as new
work. Pass records preserve those two groups separately. Each sum counts unreported
runs and missing token or price measurements; a sum that omits a measurement is
labeled a lower bound, not an estimate of the missing spend. The separate startup
line reports the recorded cost of runs started in the last 24 hours, regardless of
their exit state, including runs ended with `--end-runs`.

A repair ended by an explicit `tillr drain stop --end-runs` is recorded as
interrupted, so the next loop pins its head (`refs/tillr/runs/<run-id>`), resets the
lane and dispatches again without judging it, even when it left a commit. That commit
is kept under the pin but never gated; this is the cost of an end that spends no
attempt.

A staging error is contained to its own item: it does not reach the loop's deferred
cancellation or interrupt active siblings. The
[repair-staging regression](./drain/conflict_hold_test.go) requires both siblings to land, two unchanged restarts to stage nothing,
and a corrected policy input to recover the original repair. The original failure event
and candidate commit remain available. The
[interrupted-recovery regression](./drain/conflict_recovery_restart_test.go) also stops the
loop after a recovered worker commits, leaves that worker running until it is released,
then requires an unchanged restart to land that commit with zero new dispatches and a
repair transcript that only grew: everything written before the stop is still its prefix.

## Earlier uncommitted work

Before resetting a settled lane, the loop saves its uncommitted edits at
`refs/tillr/preserved/<run-id>`. This includes tracked edits and nonignored untracked
files, whether or not they are within an explicit node scope.
When an interrupted run is reset back to its base, `refs/tillr/preserved/<run-id>` also
preserves commits already made past that base even when the working tree is clean.
Files outside that fence are marked individually in the `drain.work-preserved`
settlement event. The event names the item, run, ref, repository, stopped reason,
and summary: file count, added and deleted text lines, binary-file count and
outside-fence count. Git reports a dash instead of line counts for binary files.
A clean lane with no unlanded commits past its base creates no preservation ref or event.

The loop builds this commit with a separate [Git index](https://git-scm.com/docs/gitglossary#def_index),
the file Git uses to prepare a commit's contents. It does not move the lane's branch
or change the contents staged in its real index. When staged bytes differ from both
HEAD and the working copy, a second parent of the saved commit keeps that staged
version. The event and briefs name its revision, `refs/tillr/preserved/<run-id>^2`,
and report its files and line counts separately from the working copy.

The saved commit is recovery evidence only: it is never put on a candidate ref,
gated or landed. If saving the commit or its event fails, the loop retains the
lane instead of resetting away its work. A restart recovers the event from the
saved commit's message if the process stopped between creating the ref and
recording the event. Git reads the record on standard input, so the file list is
not limited by command-line argument size. Before resetting on a replay, the loop
checks that every current dirty path is covered by the saved working or staged version. Newer uncovered
edits retain the lane; the loop does not overwrite the older evidence or discard
the newer work. A recorded pruning prevents replay from recreating the ref.

A retained lane is that lane's condition, not the loop's: its item is held
(`preserve-failed`) and every other lane goes on. The hold names the actual failure;
a failed snapshot commit reports the ref and file counts, not the file list. After
correcting the cause, run the `tillr drain clear-fault` command in the hold detail,
which includes the fault's sequence. When the item is already terminal
(landed, closed or resolved elsewhere), there is nothing to retain the lane for. The
loop records the lane's `git status --porcelain` in a `drain.lane-discarded` event,
aborts any rebase, merge or cherry-pick left in progress, pins the head, and resets
the lane back into rotation. Only a store that cannot record the hold or the discard
stops the loop. Every lane reset aborts an operation left in progress first, because
a checkout keeps that state and the next rebase there would refuse.

Preservation finishes before reset, including a workflow restart and a conflict
repair; Git runs outside SQLite write transactions. An ordinary lane reset then
removes the nonignored untracked files the preservation saved, so a released lane
is clean for its next run and a pinned candidate can be staged onto it (a lane that
holds such files is refused as `lane-holds-uncommitted-work`, see
"Lanes that staging refuses"). The removal runs before the reset, under the ignore
rules the snapshot read, so a file the run's own ignore edit hid stays in place
rather than being deleted unsaved. Ignored files always stay. A workflow restart
saves nonignored untracked files before its cleanup.

Both snapshots are written from temporary indexes, never the lane's own index. The
working snapshot reads HEAD and adds the working copy. The staged snapshot copies the
lane's index first, so a lock on that index cannot block it. An unmerged entry in the
copy (a conflict the worker resolved in the working copy and never added) is taken from the working copy, and the record names it under `unmerged`
and in its summary. An index that cannot be read keeps only the working snapshot, and
the summary says why. A stale `index.lock` is a lock no process holds open, in a lane no
process has as its working directory, read from `/proc`. Git holds the lock open until it
renames it, so such a lock has no writer left. That happens when a killed git leaves the
lock behind. Preservation and every lane reset remove a stale
lock first and record a `drain.stale-index-lock-removed` event (`index_lock.go`). The
age of the lock is never read. A lock that a live process holds stays, and the
`preserve-failed` detail names that PID. The holder must release that lock before
clearing this fault.

The next attempt's brief and the design seat's conflict-answer brief say that
earlier work exists, name the ref and summarize why the earlier run stopped.
The run view and waiting view show the same evidence to the owner.

**Retention:** saved-edit refs are removed when the loop observes their item as
landed, resolved elsewhere, closed (dropped from active work), or removed from the
store. A loop-owned landing cleans them up during its examination; an owner's
closure is cleaned up on the next loop pass. The immutable settlement event remains
in history, with a `drain.work-pruned` event marking that its ref is gone. Later
briefs do not recommend a removed ref. The existing committed-head pins under
`refs/tillr/runs/` follow [their own retention rule](#ref-lifetimes-and-backlog-cleanup).

## Ref lifetimes and backlog cleanup

A **run pin** is the commit pointer for a worker attempt; a **landing marker** is
the lander's crash-recovery pointer. Both names are defined by the
[landing contract](../tillr-land/README.md#known-cases-the-marker-does-not-settle).
A run pin is eligible for cleanup only if the trunk contains the pinned commit.
An earlier landing of the same item proves nothing about a reopened attempt's
head. Even a squash landing can leave the original worker commit outside trunk
history; in that case the run pin stays.

| Namespace under `refs/tillr/` | Writer and lifetime |
| :--- | :--- |
| `runs/<run>` | The loop or lander pins an attempt. Once the trunk contains the pinned commit, settlement can remove the pin if it names that run's recorded landed head or the item is finished or removed. Live lanes and pending examinations still protect their pins. For retryable items, the newest eight examined attempt pins per item are kept, ordered by attempt time and run ID. Unexamined pins and pins outside trunk history are not removed by this limit. |
| `landed/<run>` | The lander writes before publication. Cleanup requires that run's durable `landed` row and a matching published commit. Ordinary markers leave in settlement; fork-delivery markers wait until no pending examination or leased lane remains, so every overlapping boundary comparison has finished. Missing receipts keep markers for crash recovery. |
| `preserved/<run>` | The loop saves uncommitted edits. The [saved-edit retention rule](#earlier-uncommitted-work) removes them when the item finishes or is removed; the pruning event remains. |
| `sync-remote/<item>` | The repair filer pins the replay base for the current conflict at a remote tip. The pin advances to the next conflict's base and stays until the item is finished or removed, even if the base is outside trunk history. |
| `held/<name>` | An operator writes this by hand. No tillr program writes or deletes it; no age or count bound applies. |
| `recovery/<name>` | An operator writes this by hand. No tillr program writes or deletes it; no age or count bound applies. |
| `green` | The proof process advances this one pointer to proved trunk history. Cleanup never deletes it. |
| `trunk-history/observed`, `trunk-history/lost`, `trunk-history/accepted/<lost-tip>` | The loop tracks the observed trunk and retains unresolved lost history. Accepting a rewrite removes `lost` and keeps its accepted-history pointer indefinitely. This cleanup deletes none of them. |

### Remote-sync conflict repair

The drain records `remote-sync-conflict` and files or reuses one express repair item
`sync-remote-<short(R)>` per fetched remote tip. Its lane starts from the pinned replay
prefix. Before the gate, the drain checks that the repair is one linear commit on that
base and matches the mechanical merge outside the paths Git reported conflicted. A
conflict involving protected gate code goes to the owner. Once dispatched, the repair
follows generated-item obstacle routing. An identical repeated refusal holds the repair
for the operator to reconcile `main` with the staged remote tip; there is no item
block to amend. While the repair is open, push turns report its current mover
as `remote-sync-conflict`, including failed fetches and already-published green
commits. If the operator reconciles `main` by hand, the next sweep closes the
repair and records `remote-sync-cleared` once without publishing or recording a
landing. The filer does not move main, green or the main checkout. A newer
conflicting tip supersedes an open repair for an older tip on the same destination,
after any interrupted landing has been settled.

The drain routes a gated repair to linear replay instead of the ordinary squash.
Repair landing rechecks the pin and the gate binding. If replay hits
another conflict, landing advances the pinned base and records that step's conflict paths
without moving `main`. A candidate gated
for an earlier step is refused at landing. Once replay is clean, landing records
a `drain.remote-sync-replay` intent under `land.lock` before using `git reset --keep`
to install the resolutions and replayed suffix. The next pass or a manual
`tillr drain push` finishes an interrupted landing if `main` contains the intended
replay tip, recording the rewrite and landing receipt and settling the item as
landed. A manual push recovering a repair can file a conflict at a newer remote
tip without closing the landed repair. Tip mode pushes the new tip; green mode
keeps green behind the first resolution and records
`remote-sync-proving` until proof reaches the replayed tip. The
[refused-push design](../../docs/design/tillr-refused-push.md) explains the replay
and its recovery guarantees.

### Set-aside and recovery pointers

Set-aside also writes `runs/<run>-set-aside-base`, a pointer to the lane's reset
destination so an interrupted reset can resume. It follows its owning run's
retention rule; closing the item can remove the reset pointer if the trunk contains it, without removing the candidate's unlanded pin.

A held or recovery pointer naming the same commit does **not** extend a run pin's
lifetime. The operator's pointer alone retains that commit. Boundary checks still
observe changes to operator refs without convicting the run.

The loop cleans refs after each successful settlement and on later passes, so an
operator's closure needs no special cleanup command. For accumulated refs, use
`tillr-drain sweep-refs`: first inspect its dry-run plan, then apply it.

```sh
tillr-drain sweep-refs --root . --store .tillr/state.db --dry-run
tillr-drain sweep-refs --root . --store .tillr/state.db
```

The sweep reports each ref as removed (or would remove) or kept, with its reason.
Unknown run ownership, symbolic refs, unrecorded landings and mismatched markers are
kept rather than guessed safe. An expired lease is also kept until the drain reaps
and releases it; a live conflict repair may still need an earlier attempt's pin. Held and recovery refs are listed with their item's state when
known and whether the trunk contains their commit. A removed pin also names any
operator ref that still holds its commit. Saved edits use the same terminal-state
retention as the loop. A second sweep removes nothing unless the refs, trunk or lifecycle state changed.

The sweep is the only automatic deleter of saved edits, including in separate lane
repositories. It takes the repository's common-directory `land.lock` before
sampling, so two sweeps (including a manual `sweep-refs`) cannot overlap there.
Git deletion intentionally runs inside the store write transaction: it excludes
lane allocation and item state changes between the eligibility check and delete.
Ref enumeration and ancestry checks run outside that transaction. The transaction
records prune receipts, not item state changes.

Deletion is guarded by each ref's observed commit and never follows symbolic refs
to their targets. If Git rejects the batch and a ref is confirmed absent, the sweep
counts it as pruned and retries the remaining refs with their original guards.
A changed ref or other Git error is not proof of absence. A failed sweep warns the
loop without stopping it; an explicit `sweep-refs` still returns the error. Run
this against the repository's own store; run ids must not be reused across stores
sharing the repository.

Only the operator removes a held or recovery ref, after deciding it is no longer
needed:

```sh
git update-ref -d refs/tillr/held/<name>
git update-ref -d refs/tillr/recovery/<name>
```

## Conflict repair of a retired candidate

Before the gate, the loop adds its own commit to a candidate, `chore: retire landed
document block`. This **retirement** commit removes the item's block from its document,
so the block is gone once the change lands. A conflict-fix run of such a candidate
starts from the worker's own commit. Its brief names that commit to re-apply, and its
conflict markers are computed from it. The brief also names the pinned head, the
worker's commit plus the retirement, and tells the worker not to re-apply it. After the
fix, the loop retires the block again.

A fix that carries the retirement anyway is not refused as the worker editing its own
item block. The fence check compares the fix's block against exactly what the loop's
retirement makes of the fix's base, and only for a run whose original candidate has a
recorded `drain.retirement`. A fix that changes its own block in any other way is still
refused.

## Filing fork work

File with `tillr item request`. Name one fork from the repository's fork list (the
repository-provided `bin/pi-forks.list` under the root, which the drain reads), describe the
change in the words on standard input and supply `--title`. The ignored `--path`
flag does not select a fork; do not name a lane checkout path.
The `plan` step of `planned-build@v1` records the delivery as repository, fork, or
both. A new `single-worker@v1` item with no recorded target instead uses the
read-only `classify` seat, which routes to the same backend as `scout` in the
shipped policies. A path list does not select a fork. Before resuming
pending fork work, make sure the item's words identify its fork explicitly.

For a fork piece, staging materializes the dispatched lane's checkout and starts it at
the delivered patch tip. The worker commits only in that checkout and does not publish
it. The lander alone moves the fork's `refs/heads/patch/<fork>` branch, marks the delivery
for sibling boundaries, and refreshes the shared cache's `main`; two items for the same
fork remain serialized. For a mixed item, the drain records the fork publication and
only then starts a separate run for the repository part.

Run pins for fork commits live in the lane's fork repository. Before releasing an
unsuccessful fork run's checkout, the drain saves its staged and working changes under
`refs/tillr/preserved/<run-id>` in that same repository. If a rebase was in progress,
it also keeps the replayed head and the original tip under run pins before quitting the
rebase. It then restores `main` to the current delivered patch tip. A workflow node that
is being restarted, rather than released, restages at its recorded node base.

The fork gate is [fork-gate.py](fork-gate.py), whose `fork-build` check runs through
`tillr-gate` on a clean copy of the exact candidate head with the pack's declared
build command. When `fork-build` passes in that same clean clone, `fork-quality`
runs after it from `FORK_QUALITY`. For `pi`, it runs `npx tsc --noEmit` at the root
and `npm test` in each `packages/` directory changed by the candidate. When the
candidate's base commit is an ancestor of its head (an incremental fork change),
the diff runs from that base so unrelated packages touched only by earlier fork
commits are not re-run. When the base is divergent (a rebase), the diff runs from
the merge base of the candidate head and the upstream ref the pack follows,
excluding packages changed only by upstream. Each package runs its own
`scripts.test` (for example, `packages/tui` uses `node --test test/*.test.ts`);
when no package is touched, only the root check runs. Quality commands use an
isolated home and environment with `VITEST_MAX_WORKERS` and `GOMAXPROCS` bounded
to avoid saturating the host beside concurrent gates. A
missing test script or unavailable runner leaves the gate unable to judge. The
log names the affected package and script for a runner error, and names the tool
when mise cannot resolve a shim. An actual test failure refuses the candidate.
`pi-subagents` and `pi-session-search`
install devDependencies and run their `FORK_QUALITY` scripts; forks absent from
`FORK_QUALITY` keep a build-only verdict and report it. The ordinary repository gate's candidate-build step reconstructs the
currently delivered branch, not this unlanded head, so it cannot certify a fork item.
A pack with no build command explicitly receives a source-only check instead. Build
output stays beside the run's gate records, never in the worker's checkout. A failed
build or failed quality check leaves the patch branch unmoved, and a passing verdict lacking `fork-build` is
not a fork-delivery verdict. The lander publishes the built commit itself rather than
squashing fork history. Dirty or divergent cache and writer checkouts are refused,
not reset. A missing shared cache is prepared from the gated fork's upstream metadata
and published only when complete; recovery of an earlier receipt keeps a later delivered
tip in that cache. The `TestFork`, `TestAForkLane` and `TestPatchBranchRetirement`
tests in this directory cover it; the integration case exercises failed build refusal,
two overlapping lane boundaries, existing and freshly created caches, and two successive
fork landings.

## The deny-list and the session

Per-item path fences do not control which files a candidate can change. Before the gate,
the drain reads [`config/tillr/deny-list.json`](../../config/tillr/deny-list.json) from the
committed trunk and checks the worker's pinned base-to-head change against it.
A refusal returns the matched entry, the path or declaration, and its reason to the worker. The judge receives the
changed paths and the diff and checks that the request accounts for the change.

For trunk candidates, the drain checks the worker's original commit against the deny-list,
then uses its embedded `pack-sync` renderer for the shipped modules the candidate changed.
It does not run the candidate's recipe. A stale mirror is regenerated even if the worker
committed an earlier copy. The drain records the worker parent and the intended maintenance
tree before committing the generated files alongside any document retirement. The gate and
lander receive that committed tree, not an unstaged copy. If regeneration fails, only that
item is held, with the renderer's output and no gate verdict. If the mirror already matches,
there is no mirror diff to commit. Patch-branch deliveries are not rewritten.

**A pool lane is not a session.** Staging mints a fresh harness session id for an ordinary
dispatch and writes it beside the brief as `session-id`; it never treats that file as the
next item's session. `resume` explicitly names the id it continues. Review-comment items
instead reuse their document's recorded designer at a fixed
[desk](../../docs/reference/tillr-designer-sessions.md#the-desk), outside the numbered pool.
A design-seat item also uses that desk when its fence names exactly one Markdown document
under `comments.paths`; its completed run records the designer for later comments, even if
the run failed. A directory fence does not identify a document. Zero or multiple eligible
documents, or disabled comments settings, keep a design-seat item in the numbered pool and
write no designer row.

Desk reservations include live runs, unfinished candidates, and this pass's dispatches.
`comments.max_running` bounds occupied document desks independently of the numbered pool.
An item beyond that limit waits with `waits on a desk` and its document as the mover; an
item whose own desk is occupied instead waits with `waits on the document's desk` and the
occupying item as the mover. Hand-taken comment batches retain their desk when automatic
intake is disabled.

## Hand-driven comment delivery

An item created by `tillr item comments take` dispatches on the design seat to its document's
desk. The loop registers the lane before execution, adds its worktree for the run, and
removes that worktree after settlement. New build dispatches for the document wait with
`design-under-comment` until its batch settles; already-running workers are not interrupted.
The staged task carries the complete review threads,
the four-line answering rule, and the document's changes since the preceding turn.

The loop records the last designer from the completed stream, plus the document's blob and
the session's dispatch reason. Unreported provider measurements remain absent rather than
being replaced by a configured lifetime. Run spend stays in the ordinary run record. This
path chooses a session by the reference's ordered
[dispatch table](../../docs/reference/tillr-designer-sessions.md#choosing-at-dispatch).
Warm means the last request plus its reported cache lifetime; unknown retention is expired.
Only an expired session over 40,000 tokens is compacted, and only when its adapter ships an
executable compact step. Otherwise it resumes with `resumed-expired`. Nothing runs at expiry.
Compaction receipts record before/after token measurements and reported cost against the item;
missing cost stays unknown. Automatic quiet-period intake runs before planning when `[comments]`
is configured in `.tillr.toml` ([comment intake](../../docs/reference/tillr-designer-sessions.md#comment-intake-and-batch-items)).

A nonzero resumed run with no model response records `resume-failed` and dispatches once more
to a new session in the same settlement pass. Partial answers are not replayed. A replacement
brief names the previous transcript, and the replacement becomes the recorded designer.
Each pass removes designer rows for deleted documents and stages Vantage maps to the `done`
role, never their harness-owned transcript files. The stage index is shared with question
reads and cached at the current repository revision.

Compaction keeps a durable session/checkpoint journal, where a checkpoint is the previous
run's last request. After interruption, a completed transcript checkpoint or saved receipt
is reconciled without another model request. An ambiguous completion explicitly refuses
another paid compaction. The receipt stays charged to the original comment run, even when
another dispatch recovers it.

A successful committed revision delivers replies only against its recorded landing, once
the served document matches the landed blob. A reply-only `RESULT: done` records `answered`,
skips gate and landing, and closes the item through the item utility's lifecycle API. Missing,
duplicate, wrong-turn, or foreign replies fail criterion 1. Blocked, failed, and gate-refused
runs deliver nothing.

Delivery is durable: the loop records immutable reply bytes, writes one scratch file, and
renames it onto a JSON-lines inbox name. Each reply echoes Vantage's `id`, `round` (the reaction
count at intake), and root text version, with a stable item-and-comment nonce. A later pass
retries pending deliveries from the store, including those waiting on an owner's unsaved
edit; it never needs the desk worktree or the original reply file again.

## Ask the planner why

```sh
tillr drain why --store /path/to/.tillr/state.db --policy /path/to/policy.json --lanes lane-1,lane-2
tillr drain why <item-id> --store /path/to/.tillr/state.db --policy /path/to/policy.json
```

`why` runs the same planner as `plan` without a pass input file, writes nothing,
prints place, item id, position, reason, and mover for every item not stored as
`landed`, and ends with a summary. Naming an item prints its full explanation,
including the planner decision. `--json` gives rows and summary as one object,
or the selected item's record alone. The JSON answer also carries the event
sequence at which the planner read the store.

`--lanes` takes either a comma-separated list of lane ids or a count. A count is
the loop's own `--lanes` spelling and offers the loop's lanes: `lane-1` to
`lane-N` and the express lanes the repository's settings configure.

The planner gives a free ordinary lane to every item that can start. A lane a
returned worker's session or a design continuation sits on is reserved for that
item only while it starts in the same plan; when it cannot (it is held, blocked,
retried without its session, or already running elsewhere) and another item
found no free lane, the plan is made again without that reservation. Items the
loop itself holds after a staging refusal are held before any lane is offered.

The unfiltered text face also lists each landed item's recorded run cost and the
sum per seat. It joins node seats and models to the run's backend and usage;
node rows without a seat use recorded seat dispatches where available, otherwise
`unknown`. Runs outside a node use their recorded seat dispatch or the item's
seat (default `build`). Missing run cost makes its item and seat sums `unknown`,
not zero. Runs started after the landing are excluded when the landing event's
time is available. Retries are included, once per run. The last-twenty median
uses landed items on the `build` or default seat, plus planned builds; if any
of those items has an unknown sum the median is unknown. After ten landed
`planned-build@v1` items, a separate median uses their last twenty (or all if
fewer than twenty). With no eligible items the median is unknown. JSON and
single-item reports omit the cost summary.

A mover is the question, blocking item, run, lane, or operator action that can
advance the item. The summary partitions the rows into running items, items the
next pass could advance, and holds counted by reason. Busy lanes include unfinished
candidate examinations, not just live leases. Capacity is consumed in queue order,
so an earlier predicted dispatch can leave a later item waiting on a lane.
An authorized operator retry waiting for a lane lists the eligible offered
lanes and what holds each one: unfinished work, an active lease, or an item
that took the lane earlier in the plan. Express retries can use express or
ordinary lanes; ordinary retries can use only ordinary lanes. If the plan
can start the retry, the report names the planned lane and says it is free.
Ordinary work needs no item path list: the [deny-list](#the-deny-list-and-the-session)
checks the committed change before the gate.

Closed and resolved-elsewhere items remain visible with no further work required.
Landed items are omitted from the queue-wide report. `tillr drain why <item>`
reports a named landed item as done, with its unreverted landing reference when
one exists. When an operator [finishes an existing landing](../tillr-item/README.md#finish-an-item-that-already-landed),
the drain stops offering that item lanes, interrupts its own unfinished work,
and settles the runs without judging or landing another candidate.

Use the loop's policy and lane offers to reproduce its next decision: those inputs
are not retained in the store. With no policy, readiness is held as `no-policy`,
not assigned a guessed backend. With no lane list, the planner offers every lane
in the store. The default clock is the current time; `--now` supplies a fixed
observation time. Document questions use the same root and index command as `plan`.

### Adding a hold reason

A hold reason is an exported `Reason...` string constant in the `drain` package. How
a recorded reason is shown (class, where it sits, what moves it) is one table,
`Rules` in [holdrules](holdrules/holdrules.go), a leaf package with no store
dependency so the read-only `tillr view` face can import it. To add a reason:

1. Declare the `Reason...` constant, with a string literal value.
2. Add its row to `holdrules.Rules`: class, where, mover. A reason that carries
   detail after a colon (`node-failed: <node>`) also needs its prefix handled in
   `holdrules.For`.
3. Run `go test -run TestEveryReasonConstantHasAHoldRule ./drain` here. It parses the
   package for every exported `Reason` constant and fails for one with no row, or a
   row with an unknown class, so the omission is caught in this module and in the
   fast gate's change checks, not by the view's suite after landing.

The view's `waiting` face and its lane conditions read the table; they keep only
presentation. `tillr drain why` builds its mover from the live planner decision
(deferrals, conflicts, workflow nodes), so its `whyPresentation` is a per-decision
computation, not a second copy of this table. A reason it has no case for is shown by
its row here, so adding a row also gives `why` its mover; only a reason with no row reads
`diagnose the run; tillr drain retry --help`. A fault hold's row also carries the fault's
recorded detail after its reason, because a mover such as "the process the detail names"
is read from it (`TestAFaultHoldShowsItsRecordedMoverAndDetail` in
[`drain/why_fault_mover_test.go`](./drain/why_fault_mover_test.go)).

## Standalone invocation

```sh
tillr-drain plan
tillr-drain push --root <repository> [--store <path>] [--json]
tillr-drain resume --run <run-id>
tillr-drain loop
```

`plan` is the read-only half an external caller composes against its own reporter; `loop` is
the whole service and needs the three judging programs named.

`tillr drain push` runs one publication attempt using the repository's `[push]` settings.
It defaults to `<root>/.tillr/state.db` and uses the same trunk-history lock as the loop
before replaying a refused push. Its exit status and text or JSON report reflect the stored
`drain.post-landing-action` event, including the kind and any named mover. A proof hold
exits non-zero and names the heavy runner even when the old green is already on the remote.
In green mode, a replay by the loop or by `tillr drain push` records the imported range as
`drain.remote-roll-pending`. On startup or a green advance, the loop claims covered ranges
with `drain.remote-roll-claimed` and checks their upstream changes against the roll paths.
This survives a drain restart and does not require a successful push.
`acknowledge-patch-move` also attempts publication if the configured tip has unpublished
commits; an unsuccessful push does not undo the acknowledgment.

`resume` is one attempt continued and not a second attempt: the run id, the lane and the brief
are the ones the run already had, the session the lane carries is continued so the provider's
prefix cache is warm, and the store gains a `run.resume` event on the same run rather than a
second `runs` row. It re-takes the boundary bracket and restores saved workflow scope.
The deny-list still checks the committed delta from the original base, because the killed
segment may have committed before it died. It refuses a run the gate judged: continuing judged work is a
retry, which a person authorizes.

A normal build exit with no commit and uncommitted work is continued in its own
session, uncounted, for as long as each continuation changes that work. Each continuation preserves the worktree and asks the
worker to finish verification and commit, using the same attempt and the existing resume
bracket. A committed continuation is settled normally. A continuation that leaves the
uncommitted paths and their content exactly as the previous one did is refused as `no-op`,
naming the remaining paths, and goes back to the worker as
[a refused run](#a-refused-run-goes-back-to-its-worker) does. A replacement loop continues
a continuation a stop killed again
(`TestDirtyExitContinuesForAsLongAsEachContinuationChangesTheWork` and
`TestDirtyExitContinuesAKilledContinuationAgain` in
[`drain/commit_continuation_test.go`](./drain/commit_continuation_test.go)). Clean exits and design/comment work do not trigger it.
Missing brief/session state is named rather than replaced by a cold build. See the
[build-commit continuation reference](../../docs/reference/tillr-build-commit.md).

Interrupting `resume` or `once` (Ctrl-C, SIGTERM) stops that command, not its worker:
the run face is started in its own process group, outside the command's context, so
it finishes and writes its terminal record, and the next loop settles it.
`tillr drain stop --end-runs` is the only drain command that ends a run.

A real runner continuation reopens an unjudged examination even when the loop already
examined an interrupted or no-op segment. It supersedes that segment's orphan marker and
candidate while retaining the original request, base and attempt. Replaying the same
continuation does not reopen it again; a verdict or landing prevents later reopening.
Boundary faults still require explicit clearance.
[`drain/resume_settlement_test.go`](./drain/resume_settlement_test.go) drives
signal/no-op examination through `Resume`, real `run.Exec` events and one eventual
fixture gate/landing, including repeated interruptions and replay. Live harness recovery
and lane restoration are separate operator actions, not performed by this test.

## What it refuses

- **A loop with no gate, land or verdict program** — it judges nothing itself, so it cannot
  start.
- **A root it cannot write a record under** — the record is the answer, not the exit code.
- **A re-dispatch of a held run nobody authorized** — a changed input re-dispatches
  nothing; a retry of a held run is an operator's act (`tillr drain retry`).
- **A resume with no session to continue, or of a judged run** — the first would pay for the
  whole transcript again while recording itself as a continuation, and the second spends a
  second attempt on work a verdict already answered.
- **A seat it cannot route** — an unmapped seat refuses the whole plan. The refusal names
  the requested seat, the mapped seats, and the backend policy file where seats are declared.
  An unmapped seat is never silently replaced by the pass's default backend.

## Test

`go test ./...` in this directory builds the binary and drives each face against a scratch
store and scratch lane roots; the drain package's own suite pins the decision rules. Pass
`-timeout 60m`: the drain package can run for ten minutes or more on a loaded machine, and Go's
default 10-minute per-package timeout would kill it with no failing test. The 60 minutes is
a hang guard, not a budget. The gate and `tools/go-test-shards.py` already disable the
timeout (`-timeout 0`).

The test binary declares the sibling sources as inputs that Go's test-result cache can
see. A child `go build` does not record its reads in the parent's test log, and Go ignores
logged file reads outside the test's module. The tests therefore open the sibling Go
sources and module files through an ignored, module-local symlink to the parent directory.
They read each directory too, so adding or removing a source or package invalidates the
saved result. These reads happen inside tests, not before `m.Run()` in `TestMain`, where
Go has not started recording inputs. The symlink stays after the test exits so Go can
check those inputs; it is not a copied source tree or a private build cache. An existing
path that is not the expected symlink is an error, never silently replaced.

The one sibling list lives in [siblings.txt](internal/testinputs/siblings.txt). A permanent
test checks it against the tests' child-build calls and rejects each omitted program.
The [cache regression check](cache_test.py) uses an isolated clone, warms the result with
`go test ./...`, confirms an unchanged tree reports `(cached)`, then inserts an immediate
exit into each sibling's main function and requires a noncached failure. It also checks
that the `drain` package with child builds reruns while root stays `(cached)` and that a restored tree caches again.

```bash
python3 tools/tillr-drain/cache_test.py  # from the repository root; uses YOLO_DURABLE_DIR
```

Without `--run` every pass is the whole module's suite, repeated for each sibling, so
that form is for a Go upgrade. The gate's `go-shard-helpers-test` check passes
`--run TestTheComposedPipelineRunsEndToEndOnAFixture`, the case that builds every
sibling, which proves the same invalidation at a fraction of the cost.

This check exercises Go's actual file-input handling, rather than assuming that it sees
sibling builds. Keep it when upgrading Go. It shares the normal Go build cache and never
uses `-count=1`; ordinary Go source compilation still controls the compiled-code cache.
