# tillr-drain — the pass, and the loop that advances items

> [!WARNING]
> **Known Gaps (verified 2026-11-14 at `04957a8a83`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **The AutoQA handoff skill is not shipped** — `yolo-packs/tillr/skills/` holds
>    `tillr-operating` only, so the design's `tillr-autoqa` handoff has no skill to dispatch.
> 2. **The observatory front end is not built** — `tillr view serve` answers 404 for the UI
>    until the front end under `tools/tillr-view/web` is built and embedded, which is a
>    separate Node step and not part of a Go build.
> 3. **The `tillr` on `PATH` is the pack's rendered tree, not this working tree** — an edit
>    under `tools/tillr*` reaches the launcher only after the pack is mirrored and rendered,
>    so a session that must run its own change builds the working tree first.
>
> **Do not wire `tillr` into another repository's gate while these stand.** The toolset
> ships early so it can be reviewed, not so it can be depended on.

## What this module owns

`tillr-drain` turns board state into per-item successor decisions. A **pass** visits
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
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## Moving a running loop onto new code

Start the drain and observatory from the `just tillr-build` bin directory.
After a tillr landing the loop runs `just tillr-roll --no-request`, then requests restarts.
After a manual commit, run `just tillr-roll` to test, build, swap and request both restarts.
A refused restart leaves the current process running; replacing files alone does not restart it.
Pack launchers use a separate install: run `yolo host apply --assert` before requesting a restart.

## Opted-in judgment before the gate

An item directive with `judge="agent"` asks for an independent, read-only review
of its committed candidate. Before dispatching the worker, planning requires one
unexpired grant covering every criterion's claim class; otherwise the item waits
as `judge-grant-absent`. The policy must map the `review` seat. Items without that
attribute or a workflow evaluator keep the existing mechanical retirement path
and start no review run.

A workflow may set `exit.evaluator_slot` to the node that evaluates its candidate.
That node must be read-only, required by the exit, and ordered after every writing
node. The loader and workflow check report `workflow-evaluator-access`,
`workflow-evaluator-required`, or `workflow-evaluator-order` when these guarantees
are absent. The evaluator uses the same judge step, brief, verdict location, and
intake as an opted-in item, using the evaluator node's configured route. An item
that also asks for an agent judge still gets only this one evaluating run.
Planning requires the same covering grant before the graph's first node starts.

The evaluator's retained judgment records every writing node's run, session,
backend, and route beside its own. The intake checks independence against all
those writer identities. In a graph with only read-only producers, their actual
run and session identities are retained instead; the evaluator never names itself
as its producer. Evaluators run alone rather than sharing a parallel reader
group, because the judge step owns the lane's brief and session inputs.
A later read-only node carries the checked judgment to the graph's final candidate;
it does not start another judge. The
[evaluator-routing tests](./drain/workflow_evaluator_test.go) cover these guarantees.

After the worker passes its fence check, the review runs in the same lane with
its own run and session identities. Its brief includes the criteria, their hashes,
the candidate's tree identity, the path to its exact retained diff, and the path
for its written verdict. Keeping the raw diff outside the brief lets ordinary code
containing literal brace pairs pass the adapter's template preflight.
The verdict file stays in the review run's durable records. The worker's brief
and session inputs are restored afterward. A review that commits or dirties the
worktree is refused; the drain restores the original committed candidate.

The [item judge intake](https://github.com/mschulkind-oss/tillr/blob/main/tillr-item/README.md#written-judge-verdicts) checks the written
file without recording verdicts. A missing or refused file holds the item as
`judge-unreadable`, attributed to the review run rather than the worker, not to
the worker's own endings. A valid
`not-done` finding holds it as `eval-failed`, without a gate, landing, or criterion
verdict. The waiting view includes the controlling criterion's reason.

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

If authority expires during the gate, landing waits for a new covering grant. If
it expires during publication, the drain retains the exact landed commit before
settlement. A new covering grant then records the checked file and item state
without repeating the worker, review, gate, or landing. An unfinished review
handoff is recovered before the worker's recorded-head guard, including restoration
when a judge committed before its terminal record was lost.

The [judge-routing tests](./drain/judge_route_test.go) cover ordering, authority,
read-only restoration, failure attribution, and mechanical-path compatibility.
The [disposition tests](./drain/judge_disposition_test.go) cover blocked metadata,
criterion intake and closure, the three failure pairs in both file orders, lane
release, and an unchanged trunk. The
[blocker tests](./drain/judge_blocker_test.go) cover unreadable blocked metadata.

## Express lanes

The [saved-idea rule](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-actions.md#53-the-item-that-is-created)
names **express work**: a saved idea, extended here to an item whose block sets `express="true"`.
Express lanes are reserved for that work and do not consume the ordinary `--lanes` cap.
Configure their count in the repository settings file (default: zero):

```toml
[lanes]
express = 2
```

The loop names these lanes `express-1`, `express-2`, and so on. Express work prefers
one of them, then an available ordinary lane; if neither is free it waits. Ordinary
work never uses an express lane, and no running work is stopped to make room.
Eligibility comes from the stored item definition, not its workflow's first seat.
Saved ideas use the `item-idea-` ID convention from
[idea intake](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/observatory-actions.md#53-the-item-that-is-created);
this allocation support does not implement the intake command itself.

Review-comment work stays at its document's
[desk](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-designer-sessions.md#the-desk), a lane assigned to that
document, and is not express work even if its block names the attribute. Waiting
output and the observatory lane strip count ordinary and express lanes separately;
desks remain visible but count in neither pool.

Display backlog: lane rows are retained for run history. Lowering `lanes.express`
can leave previously registered express lanes in the displayed total; allocation
still uses only the currently configured pool. Reconcile that retained history
with the offered-capacity display without deleting rows referenced by past runs.

## Designer retirement

The [keep-warm rule](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-designer-sessions.md#the-keep-warm-rule)
ends a designer's session when its document is deleted or its stage is done.
Deletion requires absence from both the trunk commit captured by the pass and the
main checkout. An uncommitted removal or a file briefly missing during landing
therefore keeps the designer's row. Retirement deletes only that row, not its transcript.

## Pass observations and report identity

The [successor design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-drain-successor.md#25-one-pass-what-it-advances-and-what-bounds-it)
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

An applied pass receives a store-counted **generation**, the ordinal defined by the
[trigger design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-drain-successor.md#44-s4--the-trigger-wait-replaces-the-ticks-clock).
An empty pass allocates none. Requests carry that generation as their correlation.
Timing measurements live in `drain_passes`, with the existing `tick_id` column
carrying the generation. On write-open, historical `drain_ticks` rows are copied
without replacing new measurements or inventing missing historical values. The old
table remains a migration source; new measurements never write it.

A change to the gate runner or its wrapper, or to the legacy checker and its audit,
proves its own suite before merging, regardless of `landing_strategy`. The
[runner-change rule](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer)
keeps that entire landing out of the ordinary time distribution: `gate_ms`,
`land_ms`, and `gate_count` measure ordinary candidates only. `runner_gate_ms` and
`runner_land_ms` record the exceptional gate and landing times, and
`runner_gate_count` counts those gate attempts. The loop reports these separately,
including slow proofs. A measured duration does not fail a test or a landing.

Loop reports are named by their inclusive first and last generations, not by the
clock. A loop that allocated none uses zero endpoints, so a startup fault remains
readable. For example:

```text
drain-1-3.json  -> record id loop-1-3
drain-4-6.json  -> record id loop-4-6
drain-0-0.json  -> no applied generation
```

## The commit a new lane starts from

The repository's `lane_base` gate setting defaults to `green`. A new dispatch
records and stages the commit at `refs/tillr/green`, the
[green pointer](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer)
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
narrows the item's allowed paths; it cannot widen them. Both lists receive the
same expansion to required pack copies and documentation companions. The loader
refuses invalid values, empty lists, and any scope on a read-only stage under
separate rules. A scope outside the expanded item fence holds the item as
`node-scope-outside-fence`, naming the node and path before any node starts.

Each node receives its own scope in its staged fence and brief. Writing nodes
run serially in the item's single git target, including a fork checkout; the next
node starts at the preceding node's final HEAD. At each writing node's exit, the
drain checks only that node's changed paths, from its starting HEAD to its final
HEAD. An escape is refused as `boundary-scope-escape`, naming the node and path;
no successor starts. A dirty writing target is also refused. Passing every node
does not waive the final check of the whole change against the item's fence.
The staged node base and scope are kept separately from the item's original base
and full fence so the final candidate includes all nodes and lands once.

A resumed node keeps the scope in its saved workflow, checked against the item's
current allowed paths; reloading an edited workflow cannot broaden its authority.
A held candidate saves the full item fence when it releases a lane. Restaging it
restores that fence and its original base, replacing metadata left by a later item.

## Stages that name workflows

A `workflow` stage names a tracked file in
[`config/tillr/workflows/`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/workflows). Before dispatch, the
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
outside it; strings have a declared `most`; commands name registered entries,
never command lines. Missing, unknown, wrongly typed, and oversized arguments
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

`write_scope` is either `"inherit"` (also the default) or a nonempty list of
path prefixes. Every writing node inside a containing stage inherits its scope
unless it declares a narrower one. A scope outside its container or the item's
fence is refused before any run. Settlement checks each writing node's own
change against its resolved scope; it does not judge earlier nodes' changes
against the last node's scope.

The stored graph contains the expanded nodes, parents, scopes, and SHA-256
hashes of all contributing workflow files. Continuation and restart use this
snapshot, not edited templates. The file check uses the same expansion and
refusals, printing executable node count, dependency depth, outgoing edge count,
and expansion depth. For a parameterized library checked on its own, it uses
one representative string or registered command per parameter. Path parameters
remain symbolic: their unknown scope is constrained by enclosing stages rather
than rejected against an invented path. Each real invocation validates its
concrete paths and reports its actual expansion.

## Parallel read-only nodes

Ready read-only [nodes](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-item-graphs.md#the-words)
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

## Registered command nodes

A workflow stage with `kind: command` runs a registered program, not a model.
Its `command_ref` names an entry in
[`config/tillr/commands.json`](https://github.com/mschulkind-oss/tillr/blob/main/config/tillr/commands.json). Each entry supplies
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
`run-infra-exhausted`, without backend failover. An omitted wall imposes none.
A failed command follows a matching `when_outcome: fail` edge if one is ready;
otherwise the item is held as `node-failed: <node>`, later nodes and the gate do
not run, and the work is preserved for inspection.

A `read_only` command must leave both HEAD and the worktree unchanged. A writing
command must commit its changes or leave the worktree clean. The final candidate
includes the earlier builder's commits and any command commits; the item still
gets one gate and one squashed landing.

## Settled harnesses

A worker can answer and commit without its harness exiting. The
[run face's completed-session rule](https://github.com/mschulkind-oss/tillr/blob/main/tillr-run/README.md#ending-a-settled-harness-that-lingers)
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
([Pi's settled-session ending](https://github.com/mschulkind-oss/tillr/blob/main/tillr-run/backends/pi/README.md#ending-a-settled-session-pi-does-not-exit)).
The drain judges it on its answer like any exit 0. It must not treat the run as
killed, re-dispatch it or resume its session.

## Backend policy changes

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

Inspect the accepted snapshot, including ordered seat backends, all backend
models and efforts, and item overrides, without activating disk edits:

```sh
tillr drain policy --root <repository>
tillr drain policy --root <repository> --json
```

Before a loop has recorded a policy, this face refuses explicitly. After the
loop stops, it shows the last accepted policy; it does not claim the loop is
still running. Deploy this pack change with `yolo host apply --assert`, then
relaunch the jail; the new loop can subsequently accept policy edits without
another restart.

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
  stopping with `queue-drained` and exit code 0; the former `--stay` flag is removed.
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
  beside the backend that answered. A failover that leaves the backend the item's seat names
  is a model switch, so it is dispatched with the reason `seat-degraded` and both halves in
  the text — the seat that fell back and the backend that answered — rather than reading as
  an ordinary failover.
- **A seat may name its own fallback order.** A seat's value is one backend name or an
  ordered list of them, such as `"design": ["claude-designer", "pi-designer"]`. The item
  dispatches to the first open backend unless shares or a selection mode request a split.
  A failover walks that seat's list forward and
  stops at its end, so design work falls back to a design backend and never onto the build
  seat's. A step along the list is an ordinary `failover`, not `seat-degraded`: every
  backend on it was named for that seat. A seat with one backend, and an item with no seat,
  walk the policy's `preference` as before. Every name in a list must be one of the policy's
  backends; the check refuses the policy otherwise and names the seat
  ([the claude backend design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-claude-backend.md#4-falling-back-when-the-window-is-spent)).

## Backend quota admission

A backend row can name `quota`, an argument list for a command that reads remaining
quota without starting work, and `reserve`, the fraction of every window kept for
the owner (default 0, valid range 0 through 1). A row without `quota` is never
probed or held by a reading or reserve; a run's refusal still holds it until reset.
With reserve 0.2, a window at 0.8 used closes admission;
a window at 0.79 remains open. The loop walks the seat's normal fallback order.

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
and choosing a failover, the loop probes readings older than 60 seconds, changed
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
remains unsettled until a replacement backend is open, before staging its new session.

For a single-worker item, a recorded quota refusal stops other live runs on that
backend through the normal pause request. If the refusing run changed its head or
left uncommitted paths, it continues under the same run id and attempt in the same
lane and worktree, without resetting files or spending a retry. The next open
backend in the seat's list receives a fresh session whose first message names the
quota interruption, earlier transcript, report when present, and uncommitted paths.
Only the completed candidate is judged. A first-message refusal that left no work
keeps the existing fresh-dispatch failover, without a continuation message.

When no backend is open, the loop records the run in `quota_pauses`, keeps its lane
and `running` position, and waits rather than exiting. Each pass checks the seat's
list again, preferring the original backend when it reopens. That backend resumes
its own session; another backend starts a fresh one. The executor removes the pause
row in the continuation's claim transaction. Workflow nodes, parallel readers, and
conflict-design runs retain their existing refusal handling.

The [Claude quota command](https://github.com/mschulkind-oss/tillr/blob/main/tillr-run/backends/claude/quota) uses one authenticated
HTTP GET, not a Claude model session. Its authentication, live measurement, and
zero-model-token cost are recorded in the
[Claude backend design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-claude-backend.md#direct-readings-and-the-owners-reserve).
`tillr view waiting` shows each probed backend, both fractions, reserve, and when
it reopens, including open backends and explicit failed/stale readings.

## Seat backend shares and selection

A seat is a workflow role; a backend is the execution service that fills it. Existing
backend-name strings and ordered lists keep choosing the first open backend. To
spread fresh dispatches by count, give every entry a positive numeric `share`:

```json
{
  "seats": {
    "build": [
      {"backend": "claude", "share": 3},
      {"backend": "pi", "share": 1}
    ],
    "review": {
      "backends": ["claude", "pi", "muse"],
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

- **`in-order`**, the default: without shares, choose the first open backend. With
  shares, choose the backend whose next dispatch is earliest in the proportional
  count order; ties follow list order.
- **`round-robin`**: move forward through the list from the seat's last recorded
  dispatch, skipping closed services. This gives equal turns even if entries name
  unequal shares.
- **`random`**: draw once per fresh dispatch among open services, with equal odds
  without shares and odds proportional to shares otherwise.

The choice is made inside the store transaction that claims the dispatch, not by
redrawing during execution. The dispatch row records `backend`; the complete request
records the adapter and model arguments. The `seat_dispatches` table records each
seat/run/backend choice once, including parallel workflow nodes and independent
agent reviews. Two loops using the same store and a restarted loop read the same
history. Failed transactions and canceled, never-started staging claims contribute no dispatch
count; completed parallel readers keep their history. A read-only `plan` retains
its ordered capacity preview; its proposed backend is not a committed draw.

Quota and reserve closures remove services from the choice. Their share goes to the
open services in proportion. Counts are cumulative: when a service reopens, count-based
selection lets it catch up against the seat's history. Ordered refusal fail-over still
walks forward from the refused backend through the original seat list; it never makes
another share or random draw. An item override still wins for a worker's fresh dispatch.
Interrupted runs retain their backend; workflow restarts retain their request rather
than selecting again. If every independent review backend is closed, the candidate waits
for an open service without being refused, discarded, or rebuilt.
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
(`revert-main-*`) or a gate-budget alarm an older build filed (`gate-budget-*`). The
loop or sync generates it, its criteria are its whole brief, and it has no block of its
own to be declared in. A generated item an older build parked on a triage conflict has
that conflict closed before the next plan and returns to its worker; a gate-budget
alarm is closed instead, because a budget is a reported target and the loop files no
item for it.

An open item without a block goes to **triage**, the design-seat run that shapes a
stored request into a document declaration, with reason `triage-no-block`. It runs
before the item’s own seat or workflow. Its fence admits only the Markdown document
its body reference names. The brief carries the stored title, reference, criteria,
and the [prose guide](https://github.com/mschulkind-oss/tillr/blob/main/.agents/skills/tillr-prose-style/SKILL.md), and offers
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

[Opt-in sync rewrites](https://github.com/mschulkind-oss/tillr/blob/main/tillr-item/README.md#automatic-prose-rewrites) run on the
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
brief includes the entire [prose guide](https://github.com/mschulkind-oss/tillr/blob/main/.agents/skills/tillr-prose-style/SKILL.md)
and the source block's findings; an unreadable guide refuses staging.

Before the gate, the loop compares the original block in the pinned base and
candidate blobs using sync's parser. A changed `id`, `paths`, `blocked_by` or `seat`,
or added, removed or renumbered criterion, refuses the rewrite and names the change.
A missing block or a remaining prose finding also refuses it. Rewording a criterion
may not change its meaning; that obligation still requires review. A valid rewrite
lands without retiring the original block, so the original can be built afterward.

## After-merge heavy runs

With `landing_strategy` set to `after-merge`, each landing and loop startup trigger
one [heavy runner](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/gate-rework.md#66-the-strategy-setting-the-heavy-tier-and-the-green-pointer).
It judges a detached checkout of the trunk tip, independently of dispatch. Landings
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
makes the supervisor the parent of abandoned grandchildren, including legacy commands
in separate sessions; the handles prevent signaling a reused process ID. It waits for
the kernel to confirm every descendant has exited before acknowledging cleanup, with
no cancellation timeout on that acknowledgement. `heavy.cleanup-pending` remains beside
the result store until confirmation. A remaining guard reports unconfirmed cleanup and
blocks checkout removal and recovery until an operator independently recovers those
processes and clears it; a released lock alone is not treated as cleanup evidence.

Because the supervisor adopts every orphan its command leaves, it also reaps them as
they exit, on each `SIGCHLD`, rather than only after the command ends. It looks before
it reaps (`waitid` with `WNOWAIT`), so it never takes the exit status of its own direct
child, which is the command's verdict. Before this, one supervisor of a heavy gate run on
2026-11-18 held 6,408 zombies, nearly all `git`.
`TestASubreaperReapsTheOrphansItAdoptsAndLeavesItsOwnChild` in
[`drain/heavy_reap_linux_test.go`](./drain/heavy_reap_linux_test.go) covers both halves.

The runner judges every heavy tree declaration, reusing the gate's remembered results,
and the legacy rolling checks that have not yet migrated to declarations. `--heavy-gate`
names the declaration judge; `--rolling` still names the legacy program. An explicitly
empty `--rolling` disables that legacy coverage, not the declaration checks. Missing
programs or missing verdict evidence are reported as faults, never silently passed.

Completed passing checks are saved with the exact trunk tip under the heavy runner's
lock, before the next check starts. A replacement loop starts proof at its first tick,
even without a new landing, and logs `reused checks=` for passes it retains. A different
tip starts fresh; failed, unavailable, and interrupted checks are not reusable proof.
Declarations are judged individually. The default Python legacy gate keeps its own
planner and scheduler; an embedded observer saves each completed pass atomically.
A stalled check's successful retry is also saved before the next retry starts, and
both declaration checks and the default legacy gate read those passes after restart.
Custom legacy programs keep their batch/spans contract and must supply their own
incremental persistence if they need reuse inside an interrupted batch.

A run with no failing or unjudged check advances `refs/tillr/green` with a guarded Git
update: the value read before judging must still be the current value, and it must be
an ancestor of the judged tip. Unavailable checks are listed, not counted as passes.
A failing check holds the pointer and is run alone at the pointer and by bisection on
recorded landings in the range. Legacy span IDs retain their kind and label so replay
uses the old program's selector vocabulary rather than confusing its stable report ID
with a selector. When the range cannot identify a single culprit (the base is red,
there are no recorded landings, or the last landing is green), a fixer names both full
commit IDs of the observed range and says attribution is unavailable, including the
check's needs and tools. The last recorded landing is checked before narrowing the
search, so an earlier red landing is not blamed when the last one passed. Otherwise the first
red landing receives a fixer: an item that repairs the broken trunk, containing every
check attributed to it. Its fence (the paths it may change) combines the
checks' declared sources and tests with the paths touched by every commit since the last
green through the current tip, even paths later reverted. Legacy checks obtain their read
paths from the gate's declaration export, not their recorded output writes. The gate's own
code is excluded, including directories that would admit it indirectly.

A red that names no landing still gets a mover. When the check is red at the pointer, when
there is no pointer, or when attribution itself answers no verdict, the runner files one
fixer against the tip. Its brief says why no landing is named, in the runner's words, with
the judge's recorded output. The run stays `red` and the pointer holds; such a red is never
`unknown`, and the red-cap hold names that fixer. Attribution replays the check with
`fork-check.py --replay`, which plans over every tracked path; it once planned from the
record's baseline and found nothing to replay on a verified pointer. A stopped loop is not an
attribution failure: it files nothing, and the next run judges again.

A later red tip for the same check attaches to an unresolved fixer rather than filing a
second repair. The red-cap hold finds an attached fixer by the checks its criterion
names, because its id carries the earlier red commit. The attachment records the new observation and combines its paths without
clearing an existing owner hold. A blocked fixer reaches the owner only by a complete
obstacle (see [A worker ends with its commit or a named obstacle](#a-worker-ends-with-its-commit-or-a-named-obstacle)):
a `fence`, `definition`, `owner` or `tooling` obstacle goes to the owner with a question the
loop writes from it, because a generated item has no design block to amend. For `fence`,
the question names the paths and asks whether the fixer's `allowed_paths` may grow. A legacy
`NEXT-ACTOR: owner` line is still read. Any other blocked report is answered back to the
fixer's own worker, whose brief states this rule; it never reaches the owner by default.

A green proof releases the trunk whatever the fixer's own state. A green run records
the checks it passed beside its proof time. Before each plan, an open fixer is resolved
(`resolved-elsewhere`, with a `drain.fixer-proved` event naming the proof) when the
proved commit contains the fixer's red commit, the proof was recorded after the fixer's
last red observation (its filing or a later attached tip), and every check the fixer
names is among the proof's passed checks. The `trunk-broken` holds behind it are then
gone on that plan, and an acquittal waiting on it is released once the selected lane
base contains the proved commit. A proof from before this rule, which lists no checks,
resolves nothing. The tip run's own passes are proof too, check by check: the runner
and the legacy program's adapter record when each check passed on the tip
(`heavy-progress.json`, `heavy-legacy-progress.json`), so a stale fixer, one whose named
checks already pass on a tip containing its red commit, is resolved by those passes
while the rest of the run is still going or is red for another check. A fixer that names
no check needs a whole green run. The
waiting face names the fixer, its failing checks and whether it is waiting on the owner.
With no pointer yet the report says there is no proved baseline to accuse a landing; it
does not claim that a nonexistent pointer was judged red.

With `auto_revert` on, a red that the bisection pins on landing L is reverted before any
fixer is filed (the owner's ruling
[`OQ-GP1`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md#OQ-GP1) of the graph design). The runner builds
the inverse of L on the tip in `<lane-root>/.revert/worktree`, gates it with the fast gate as
item `revert-main-<sha12>`, replays the failing check on it, and lands it through `tillr-land`
only when all three pass. L's item moves from `landed` back to `in-queue` with a reason, and
its run carries a returned `reverted` ending with the check's output, so the next pass hands it
back to its worker (a fresh session on the same backend). Its next gate runs that check as a
required one until it lands again. L is not reverted when it is a revert, a fixer's landing,
or a landed item's dependency; a revert that does not apply, does not pass the fast gate, or
leaves the check red ends "not possible" and files the fixer with that reason in its brief
(against the tip when the check is still red without L). The loop log reads, for a reverted
red:

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
returned with its verdicts standing (a build before the withdrawal existed) is repaired
at the next sync, which every loop start runs. The log reads
`loop: withdrew the verdicts of reverted landings: item-x (1a2b3c4d5e6f)`.

A heavy run logs as it goes, not only when it ends. Each declaration check's verdict is a
line as the check ends (`loop: heavy <check> <result> in <time>`), and the legacy program
gives one line with its failing checks. A failing check starts the culprit search with
`loop: heavy <check> failed at tip <sha>; searching for the first red landing since green
<sha>`, and each replay is two lines: where and why it replays (the green base, the last
recorded landing, or a bisection step), then its verdict and time, or `no verdict after
<time>, because <reason>`. A replay with no verdict is not retried: the search ends there,
says so, and files the fixer against the tip.

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
and the board's `over_budget` flag; it files no item. A sampled stall is retried once on the
same tip; a second stall is red and files a fixer. A loop stop ends check processes before
removing their checkout. `tillr-view status --store <store> --root <repository>` reports
the green commit and its recorded proof time, recorded landings since it, first-red
attribution, unavailable checks, and overruns. The loop log and both waiting views show
the green commit's proof age. Proof times are stored by commit before the guarded
pointer update; a cancelled or refused update leaves the previous green commit's time
unchanged. With no green commit yet, the waiting view adds no proof row or reader fault.
Older pointers without a recorded proof time explicitly say the age is unknown; the
commit's author date is never substituted. The shared report lives beside the result
store in the Git common directory;
the retired rolling watermark is neither read nor written on this path.

## Legacy rolling trunk audits

The following cadence remains only on the `before-merge` compatibility path.
With no watermark (the tip last judged green by the rolling checks), the default rolling
run audits every tracked path at the trunk tip with every applicable rolling-tier check,
regardless of the repository gate's remembered baseline.
The rolling tier is the group of slower checks deferred from individual landings;
a matching cached verdict may answer a check without executing it again.
Later runs select checks over the range since the watermark. Every rolling run also
runs the gate's fast whole-tree declarations, even when the range selects no slower suite.
The landing gate, `just done` (over the last verified commit through HEAD), and rolling
(over its watermark through the judged tip) share those declarations and the same
`tillr-gate checks` runner. The directory
[`tools/gate/checks/`](https://github.com/mschulkind-oss/tillr/blob/main/gate/checks) owns the set; there is no separate allowlist.

A green run advances the watermark, but that alone does not claim full coverage:
`rolling_runs` binds the outcome to its base and tip, and `rolling_coverage`, joined
by `run_id`, retains the fresh execution count (`ran`), reused verdict count (`cached`),
and every skipped check's identity and reason (`skipped_checks`). Skips include
checks assigned to the other tier as well as unchanged or unavailable checks.
`spans_state` says whether the program supplied readable evidence; older runs have
no coverage row, and absent or unreadable evidence is not an empty audit.
The loop's rolling line prints separate ran, cached, and skipped counts, or explicitly
says counts are unavailable when the program supplies no readable evidence.
Red or unknown runs do not advance the watermark. A red tree check, like a red slower
suite, files one deduplicated trunk fixer naming the failing check ids. A declaration's
whole-tree read becomes the tracked file set at the red commit, excluding the gate's own
code; it never grants an unrestricted root edit permission.

A candidate refused only by checks that also fail on its base is acquitted: the loop
records `trunk-already-red`, naming the red commit and checks, and blocks the item on
`item:<fixer id>` without a failing item verdict or a design conflict. The planner reports
`trunk-broken` and names that fixer. Once the fixer has landed and the selected lane base
contains its repair, the next pass restores the item's underlying state and clears its
waiter. With the `green` lane-base policy, an older verified pointer keeps the waiter
until the heavy runner proves the repair. A fresh run starts from the repaired trunk,
without an authorized retry; acquitted runs do not consume the failure walk. A fixer
closed without landing releases nothing. A check green on the base, or a candidate with
an additional failure of its own, keeps the ordinary refusal.

The red base is only half the question when the trunk has moved past it. Work is always
built against the latest trunk (the owner's ruling of 2026-11-18), so the failing checks
are also judged on the current trunk tip. A check the tip run's recorded progress already
passed on that tip counts as green there, and only the rest are replayed on the tip. When
every check is red on the tip too, the fixer is filed for the tip. When any check passes
on the tip, the trunk is already repaired there: no fixer is filed, the loop rebases the
worker's commits onto the tip and records the tip as the lane's base, and the ordinary
refusal goes back to the worker saying the trunk is repaired at the tip. A tip the replay
could not judge confirms nothing. A fork-series candidate, whose base is the fork's own
HEAD, is judged on that base alone. The rule exists because a run cut nine hours before
it was judged once filed a fixer for a check the trunk had long since repaired, and an
open fixer holds every ordinary item.

## Nothing re-runs because an input changed

The owner ruled on 2026-11-17 that nothing re-runs, is invalidated or is reset because a
hash, a document, a policy, a build or the trunk changed without knowing what the change
means. A pass no longer compares a held item's block (including the referenced body
section), workflow file, allowed paths, backend policy or running tillr build with the
inputs recorded at its dispatch, so an edit to any of them does nothing to the item's
work. A prose-only edit to an item's block re-dispatches nothing, and an edit to the
block or fence of an item whose workflow is in flight does not settle that workflow
(`TestAChangedInputReDispatchesNothing` and `TestADefinitionProseEditReadmitsNothing` in
[`drain/readmit_test.go`](./drain/readmit_test.go),
`TestAnInFlightWorkflowIsNotInvalidatedByABlockOrFenceEdit` in
[`drain/worker_ruling_test.go`](./drain/worker_ruling_test.go)). This removes audit
findings F-01 and F-07, as mapped in
[the graph design's audit table](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-graph-is-the-path.md#124-audit-findings).

An item moves on an event that names it: the operator's `tillr drain retry`, a design
answer for the item, or the recovery of a killed run. The recorded inputs stay on each
dispatch. They are the claim's check that the pass which planned a dispatch read the
definition the claim applies, and they are evidence a reader can inspect. Neither
`inputs-changed` nor `readmission-exhausted` is written any more. A recorded decision that
carries one is history, and `tillr-view waiting --history` still reads it.

A retry authorization that names a run which is no longer the item's latest run no
longer speaks. Either the retry it authorized consumed it, or a newer run ended after it
was written. The item then routes by its latest run, as it would with no authorization;
the `retry-stale` hold is gone.

A loop stop or fault leaves in-flight runs alone. Their terminal records survive the
supervisor, and the next loop settles and judges each normally, including runs that
finished while no loop was running.

```sh
tillr drain stop --root <repository>                 # stop supervision only
tillr drain stop --root <repository> --end-runs      # also end in-flight runs
```

The explicit flag is the drain's only run-ending path. It sends SIGTERM to each run
face, not a `tillr run pause` request: the flag was built before the pause command
existed, and no item has changed it since; the face ends its adapter and writes
its terminal record. Ending a run this way starts the item over, not the session.
The interruption is recorded before signaling. It is no attempt outcome: no retry or
failover is spent, regardless of how often it happens. The next pass dispatches the item again on its interrupted backend.

A stop acts on what this process domain (boot, PID namespace and user) can see and
reports the rest on stderr, one `loop: stop left alone:` line each. A session from
another domain whose heartbeat is more than five seconds past its recorded next pass
belongs to a loop that died without ending it, such as one killed by a jail relaunch,
and is left alone. One whose heartbeat is current is a live loop this domain cannot
signal: the stop exits non-zero naming it, after stopping the local loops and, with
`--end-runs`, ending the local runs. A run owned by another domain is reported and left as it is. A run whose
run face is gone but whose worker survives is ended like any other: the interruption is
recorded, then every process of this user carrying that run and store identity gets SIGTERM.

A stop that arrives during the loop's own checks ends them before their worktrees go. The
rolling tier's program and the trunk-already-red replay's run in a process group of their
own. Cancellation first sends SIGTERM to the program so it can end checks it started in
separate sessions; `fork-check` handles SIGTERM by canceling its registered checks and
exiting 143 without recording a verdict. The loop allows five seconds for that cleanup
before escalating to SIGKILL. On any exit, the step kills what remains in the program's
own group and waits briefly for it to be gone before removing the scratch worktree.
A check program that starts separate sessions must own their SIGTERM cleanup: killing
only the program's group cannot reach those sessions.

A pin (`refs/tillr/runs/<run>`) that a stop interrupts is finished rather than cut off,
bounded at thirty seconds, because the pin is what keeps a
candidate reachable after its lane is reset.

For an unbounded claim with no terminal record, replacement loops compare the recorded
process ID and start time within the same Linux boot, process namespace, and user
identity, then check that no worker carrying that run and store identity survives.
Only a proven-dead owner with no live worker is reclaimed. A claim from another
process domain of the same user, such as one a jail relaunch left behind (the new
container has a fresh PID namespace, so recorded PIDs name nothing), is judged from
files instead, because every container that mounts the repository sees the same
inodes. Its loop is proven gone when every drain session it recorded is ended, or when
its presence file (one per loop process under `<store>.loops/`, opened for writing
before the session is recorded and held until the process exits) has no writer. Its
run is live while any process still holds the run's `run.record.json` or `run.stderr`
open for writing; the face and every child that inherited those streams hold them for
as long as they live, and absent files mean the face never started. The writer check
is a Linux read lease, granted only on a file nobody has open for writing, so it needs
no timeout. A proven-gone loop's run with no writer is reclaimed like a local one on the
next pass. A claim without that evidence, including any whose files cannot be judged,
remains protected. A normal offline finish is still judged;
a worker signal without evidence of supervisor interruption is not exempted.

### A worker ends with its commit or a named obstacle

The owner ruled on 2026-11-18 that a worker gets no sanctioned way to end without
delivering (`D-JL11` in
[the judge and lifecycle design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#45-what-a-worker-may-end-with-d-jl11)).
A single-worker build run has two endings: its commit, or `RESULT: blocked` with a line
`OBSTACLE: <kind>` and the kind's required fields. The kinds route as follows:

- `definition` (`CRITERIA`, `EVIDENCE`): a design-seat conflict.
- `fence` (`PATHS`, `EVIDENCE`): a design-seat conflict told to widen the block's paths or
  split the item.
- `owner` (`QUESTION`): a design-seat conflict carrying the question. The design seat
  answers from the documents or objects `NEXT-ACTOR: owner`.
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
[`drain/obstacle_route_test.go`](./drain/obstacle_route_test.go). Workflow nodes still
send any blocked report to the design seat.

### A refused run goes back to its worker

The owner ruled on 2026-11-17 that a worker run's failure is not counted and does not park
its item (audit findings F-02, F-03 and F-09; the ruling is `D-JL10` in
[the judge and lifecycle design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#44-how-a-worker-runs-ending-moves-its-item-d-jl10)).
When the gate refuses a candidate, a run ends with no commit, a candidate escapes its
fence, or a landing is refused, the next pass dispatches the item again with the reason
`returned-with-evidence`, every time. The new run's brief carries a section on the previous
attempt: the refusal's own words, each failing gate check with its log, and the two refs
that keep the earlier run's work (`refs/tillr/runs/<run>` for its committed head and
`refs/tillr/preserved/<run>` for uncommitted work). It starts on the item's own backend.
The worker keeps the same lane and session when the adapter can resume it. The refusal
records that session and the original base before the lane is released; planning reserves
that lane for the returned worker, including after a loop restart. A workflow returns to
the producing node, with its original node boundary and accepted predecessor inputs;
predecessor records are referenced, not rerun or copied into fictional runs. The node and
its successors settle again before the candidate is admitted. The next turn has a
distinct run record, not an overwrite of the refused run. Its prompt carries the same
evidence as the fresh brief, plus the current run's identity and requirements. It must make
a new commit: the earlier refused head alone is not a new candidate. The complete change
from the original base is checked against the fence and sent through the gate and landing
again. Loop-owned item-block retirement is removed from the retained checkout before the
worker returns; the prepared head remains pinned as evidence.

The recorded decision's `worker_session_reason` explains the path: `resumed-worker` for
that continuation, `cannot-resume` for an adapter without a positive resume declaration,
`session-missing` for a missing session handle, or `seat-changed` when the backend or model
no longer matches. A retained lane removed from the offered set uses an offered lane and
records `lane-unavailable`. A workflow whose producing node cannot resume restarts at
its entry; `workflow-restarted` distinguishes a fresh graph from a continued node when
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
never a repeat, however many there are, and a refusal recorded before evidence keys
existed repeats nothing. An item with no design document still goes to the owner on a
repeat, because there is no document for the design seat to amend, and so does an item
whose backend policy names no design seat (a design route there would stop the loop for
every item). The run named as repeated is the latest earlier match, so a repeat after the
owner put the item back and authorized a retry raises its own conflict on the new pair;
the older conflict no longer holds the item and is not a reason to skip. The rule is the pure
function `repeatedEnding` in [`drain/returned.go`](./drain/returned.go), with table tests
in [`drain/returned_test.go`](./drain/returned_test.go); the loop's side is in
[`drain/worker_ruling_test.go`](./drain/worker_ruling_test.go), including the items the
removed rules held being planned again at the next pass with no repair.

A squash conflict is one of these returned endings too: on a moved trunk the loop stages a
conflict-fix run at once with the conflicting paths and three-way markers, and otherwise the
next pass returns the item on the latest trunk with the conflict lines in its brief. Nothing
counts them; the removed rule spent one fix run per candidate and then held the item as
`conflict-exhausted`, a reason a pass now writes only as history, and an item it parked is
returned at the next loop start. A conflict's evidence key (`conflictEndingKey` in
[`drain/returned.go`](./drain/returned.go)) is its sorted conflicting paths and the digest
of its normalized hunks, so the same conflict against another trunk sha repeats and a trunk
that moved again inside the hunk does not. A workflow node's refusal keeps its node rule. Machinery failures keep the one failover and `run-infra-exhausted`: the owner ruled
that each should move to the next open backend and wait when none is open, but the drain
has no fact that says a backend is open again except a quota window, so a walk with no
budget would re-dispatch a broken adapter at once and forever. That is the queue item
`item-backend-open-is-a-fact` in
[the tillr roadmap](https://github.com/mschulkind-oss/tillr/blob/main/docs/tillr/roadmap.md#route-an-infrastructure-failure-to-the-next-open-backend).

### Owner decisions in documents

The owner-question regression set is `TestOwnerReportQuestionGateHoldAnswerAndRelease`
(publication through dispatch), `TestOwnerWaitingEqualsCommittedIndex` (waiting rows
before and after removal), and `TestOwnerQuestionMigrationPublishesOnceAndSkipsStagingReports`
(legacy conflicts).

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
alternatives and legacy reports stating a ruling between two options are also recognized.
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

To convert existing owner decision reports once, run the loop with its normal root,
store, policy, and step programs, adding the migration switch. Learn those inputs
from `tillr drain loop --help`:

```bash
tillr drain loop --root . --migrate-owner-questions
```

Migration runs only these conversions, not the rest of the queue. It replays each
eligible report as a command run, claims an ordinary lane, and publishes through the
same design settlement and gate. It prints each item and document written, or the
reason a conflict was left unchanged. Reports without a question and alternatives,
including stale-base reports, are not converted. Migration leaves the conflict open in status `question`, just like settling a report.
The sync closes it as answered only after the owner’s ruling lands. A published question
holds only the item it was published for. With unchanged reports and
documents, a second conversion writes no new questions or conflict transitions. A
refusal is explicit and leaves the conflict unanswered; it never clears a hold before
publication.

Both `tillr drain why` and the waiting view count owner-waiting document questions
as one class and name their document and question id. Other holds identify the next
actor as the drain, the design seat, or the operator, with the action beside it. A
legacy conflict awaiting conversion is the drain's work, not an owner question that
the planning index cannot display.

### A design run that ends without answering

A raised conflict gets one design-seat dispatch, and the claim marks the conflict
`awaiting` that design run. The owner ruled on 2026-11-17 how every ending of that run
moves the conflict, and no ending is counted: there is no attempt cap, no comparison of
what changed since the dispatch, and no timer. An item reaches the owner only through an
objection that names the owner. The ruling is recorded in
[the judge and lifecycle design, Section 4.3](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-judge-and-lifecycle.md#43-how-a-design-runs-ending-moves-its-conflict-d-jl9).

1. **A commit that passes the gate and lands is the answer**, whatever it changed: the
   item's block, its design document, or a sibling. The conflict is `answered` and the item
   goes back to its worker. Before the ruling a commit that left the block unchanged was
   called "did not amend" and handed to the owner without a gate. A commit that removes or
   renames the block leaves no item for a worker, so it goes back to the design seat,
   naming the commit.
2. **A commit the gate refuses** is no attempt. The conflict is raised again for the design
   seat with the failing checks and the pinned commit (`refs/tillr/runs/<run>`) in its text,
   as many times as it takes. The ruling sends the refusal to the same designer session;
   until `item-design-refusal-resumes-session` is built, a fresh design run is dispatched
   instead. Any other refusal (the fence, the lander) goes back the same way.
3. **An interrupted or killed run** — a loop stop (`drain.orphaned`), a lease reclaimed
   after its loop died, a signal — is no attempt: the design seat is dispatched again.
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

6. **An identical repeat goes to the owner, with a question.** An ending that repeats an
   earlier design ending of the same item exactly is not sent to the design seat again,
   because it would be sent the same thing. The comparison is the worker path's: the same
   cause and the same evidence key as any earlier ending, and no count. The key is the
   conflict plus a digest of the item's block on the trunk for a run that named no actor
   or removed the block; the failing checks and their output digests for a gate refusal;
   and the refusal's words for a fence escape or a refused landing. An interrupted or
   killed run never repeats. The loop raises an objection naming the owner. It is recorded
   as an owner conflict, and the waiting view names the action needed to put the
   decision in its document rather than presenting an owner with nowhere to answer.
   The conflict's text names both runs and the command that moves it:
   change what the design seat is given, then `tillr drain retry --item <id> --run <the
   later run>`, or close the item. Before this rule a design run that named no actor was
   dispatched again without end. The [design repeat tests](./drain/design_repeat_test.go)
   cover it.

A change to the item's block, its design document or the trunk raises nothing. The rules
that re-raised on such a change (one automatic attempt per input change, and the
trunk-suspect re-raise after a proven trunk commit) were removed by the same ruling. A
design run's gate refusal that the trunk caused is the gate's to recognize; until it can
(the trunk-already-red replay cannot replay fast-gate check ids), such a refusal goes back
to the design seat on each re-raise, and a run staged after the trunk is fixed passes.

Each re-raise except an interrupted one, and each repeat, writes one
`drain.design-unanswered` event naming the conflict, the run, its outcome, its base and
failing checks, its `cause` and evidence `key`, and, on a repeat, `repeat` true with the
earlier run in `repeat_of`. An owner hold the removed
count wrote (its last such record names the design run the hold names) is raised again for
the design seat once, recorded with `uncounted` true. A hold written before 2026-11-17 whose
last line says the design run "did not amend" or "removed or renamed" its block is raised
again too, naming the run's commit, recorded with `misread` true.

The owner's command for a conflict held on an objection is `tillr drain retry --item <id>
--run <design run> --by <who> --why <reason>`, naming the design run the hold names. That
design retry records a `drain.retry.design` event with who asked and why, adds both to the
conflict's text, and writes no `retries` row, so the plan does not read it as a build retry.
The design run it earns is settled by the rules above. A design run the conflict no longer
names is refused, and the refusal names the current one; a conflict the loop is still
settling, or has already raised, is refused as the loop's. The
[design retry tests](./drain/design_retry_test.go) cover the re-raise and both refusals.
Every `tillr drain retry` refusal names the exact command the face would accept, or says
that none exists and who moves instead (`TestEveryRetryRefusalNamesTheAcceptedCommandOrTheMover`
in [`main_test.go`](./main_test.go)). A triage conflict keeps its own rule, one triage run per
request version. The [unanswered-design tests](./drain/design_unanswered_test.go) cover
rules 2, 3 and 5 and the two migrations; the [objection tests](./drain/design_objection_test.go)
cover rule 4 and the removed block; `TestADesignCommitThatLeavesTheBlockUnchangedIsGatedAsTheAnswer`
in [`drain/design_quota_test.go`](./drain/design_quota_test.go) covers rule 1.

The settlement reads a design run's block by its id. A rewrite item's block carries the
original item's id (the id without `rewrite-`), so an answer that retitles the block's
section is still that block. When the item's `body_ref` heading is missing on the run's
base, because the run was staged before the heading was renamed, the settlement compares
the block's own section between base and head. The
[design answer tests](./drain/design_answer_test.go) cover both readings and the migration.

An examination (the loop's settle or judge of a finished run) leases the run's lane with
no expiry while it works, and releases that lease once it ends, on every path. The write
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

When the lander reports uncommitted content on a path its change touches, the
drain retains the already-gated candidate and verdict, and releases an ordinary
lane after verifying its run pin, following [held candidate capacity](#held-candidate-capacity).
It records `trunk-path-dirty: ` followed by the sorted dirty paths as an
ordinary item decision. Unrelated dirty paths do not delay a landing.

Commit or restore the named paths. The next pass retries only the landing with
the original candidate and verdict, including after a supervisor restart; it
neither dispatches a replacement run nor reruns the gate. Each wait check uses the
change reapplied onto the current trunk, so an already-applied path does not keep
blocking the remainder; a merge conflict reaches the lander's conflict handling.
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
another gate, following the [landing principles](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/landing-principles.md).

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

A deliberate rewrite that kept the content, such as re-authoring commits or editing
their messages, is accepted instead of restored, with `tillr drain accept-rewrite`
(its `--help` lists the flags). It names the lost tip, the commit on the trunk that
replaces it, who accepts, and why. The command accepts only a rewrite it
can prove kept the content. The lost tip must be the one the hold names. The
replacing commit must be on the trunk (the tip or one of its ancestors). The two
commits must have the same tree, and the commits only the lost tip reaches must
number the same as the commits only the replacing commit reaches, so the rewrite replaced commits one for one rather than
adding, dropping or squashing any. Anything else is refused with a message naming
what differs, and nothing changes. The command also works when no pass has recorded
the rewrite yet: an observed tip that the trunk no longer contains is the same
rewrite, unrecorded. Accepting it pins the lost tip at
`refs/tillr/trunk-history/accepted/<lost-tip>`, so the replaced history stays
readable. It clears the lost tip, makes the replacing commit the observed tip, and records a
`drain.trunk-rewrite-accepted` event that names who accepted the rewrite, why, and
both tips. When the rewrite moved `refs/tillr/green` off the trunk, the command moves
it to the commit that replaced it: the one as far below the replacing commit as the
proved commit was below the lost tip, refused unless it has the same tree and as many
commits beneath it. The heavy run cannot do that itself, because it refuses a proof
pointer the trunk no longer contains, and lanes stage from that pointer. The next pass retries retained landings without another gate, as after
a restoration: a gate verdict binds the candidate's base and tree, not the trunk.
Run it while no landing is in flight, after stopping the loop. A lander that was
already waiting with the old observed tip would record that tip as lost again.

The drain also passes its observed tip to the
lander with `--observed-trunk`; under the landing lock the lander refuses with
`trunk-rewritten` if the trunk no longer contains it. The lander also reconciles
the current durable observation and lost tip inside its locked store transaction:
a request that was already waiting for the lock cannot bypass a hold a later pass
recorded. This covers movement between the pass's observation and the landing transaction.

The run bracket compares snapshots from before and after execution. It convicts a run
on attribution, never on correlation in time. The owner ruled on 2026-11-17: "why do we
convict anyone for removing worktrees? that sounds stupid? we should be free to act with
normal tools without breaking the drain, goes along with ad hoc commits". So the owner and
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
| Target's own checked-out branch, assigned patch refs, registered sibling branches, `refs/tillr/runs/` pins, saved edits under `refs/tillr/preserved/`, the lander's `refs/tillr/landed/` markers, and the two trunk-history refs | Ignored | These are deliveries or concurrent loop work. |
| `refs/heads/patch/<fork>` moved to a commit that a `refs/tillr/landed/` marker written during the bracket names | Ignored | That is a sibling's fork-series landing: the lander marks the built fork head before it moves the patch branch. |
| The loop's own scratch worktrees under the lane root: `<lane root>/.rolling/worktree` and `<lane root>/.replay/<sha>` while detached, and `<lane root>/desk-<doc>/worktree` on that desk's own lane branch `tillr/<scope>/desk-<doc>` (which is excused too) | Ignored | The loop creates and removes them itself, for the rolling tier, the trunk-already-red replay and the review-comment desk lanes. |
| A registration that was already `prunable` (its directory gone) when the bracket opened, pruned during it | Ignored | `git worktree prune` removes it; the directory was gone before the run began. |
| Main-checkout uncommitted content, and hand commits on the trunk | Ignored | Content belongs to the owner, regardless of when it appeared. |
| Any other ref inside tillr's namespaces appearing, moving or going away (an operator's `refs/tillr/held/` pin, a fork export moving `refs/heads/patch/<fork>`, a hand-made `tillr/` branch, or a worker's rogue ref) | Observed | Snapshots cannot say who wrote it. |
| Any other worktree appearing or going away, anywhere outside a sibling's own lane directory | Observed | Agents and the owner add and remove worktrees at will. |
| The main checkout's checked-out branch changing | Observed | Checking out another branch in the main checkout is ordinary owner use. |
| Changed status of a path in a watched, idle sibling lane worktree | Convicted | That worktree is another item's candidate under the drain's own lane root, where ordinary use of the repository does not write. Siblings that became active are removed from the comparison. |
| The run's own lane worktree going away | Convicted | The lane is the drain's, and the run was its only assigned user. |
| Candidate changes outside its fence, or uncommitted work past its candidate | Convicted, by the separate fence check | The deliverable must be the committed change inside the assigned paths. |
| A named store file changing in an adapter-only bracket | Convicted, in the standalone boundary check | The adapter does not own the store. Run brackets omit it because the run face writes its own records. |

Unexpected patch branch observations also hold publication, not the observing run.
The boundary event stores every such change in `patch_moves`, with `ref`, `old`, and
`new` commits; an empty endpoint means the ref appeared or was deleted. This list is
not capped with the diagnostic observations. Assigned deliveries and moves to a
landing marked during the bracket remain ignored.

`tillr view waiting` shows a `needs you` publication row naming the fork, observing
run, commits, and observation event sequence. After reviewing the change, the owner
acknowledges that sequence and fork:

```bash
tillr drain acknowledge-patch-move --store .tillr/state.db --observation 123 --fork pi --by owner
```

The acknowledgment records the owner and UTC time without erasing the observation.
It clears only that observation's fork: a later move or another run's observation
still needs review. [The patch branch publisher](https://github.com/mschulkind-oss/tillr/blob/main/bin/patch-branches-publish)
reads the same outstanding observations and refuses the affected branches, including
refs named by `--skip-ref` or `--force-rewritten`; unaffected branches can publish.
Its default store is the main repository's store, also when invoked from a worktree;
`--store` selects an explicitly named store. A clone without that default store has no
observations, but an existing unreadable store refuses publication. Historical
boundary events written without `patch_moves` remain diagnostic records, not
retroactively created publication holds.

What this gives up is the stop of a worker whose only escape lies outside the lanes, such
as a rogue ref or a stray worktree. That escape no longer holds its item, and the
observation names it for an operator. What still catches an escape that matters is the
fence check: it judges what lands, and the deliverable must be the committed change inside
the fence. The gate runs after it, and so does the conviction of any write into another
lane. A resumed segment is judged by the same run rule.

## Faults that hold one item

A gate stall is not a candidate failure. The first `stalled` answer (gate exit 6)
retains the head with `gate-stall-rerun`, naming the check and how many gates were
in flight. The next planning pass with fewer gates, or no other gate, gates that
same candidate: no worker run, charged attempt or failing verdict. A second stall
on the same head tree holds the item as `gate-stalled`, with both observations;
diagnose the check and use `tillr drain clear-fault`. Its retained evidence and
rerun state survive restarts: the first-stall transition and observations commit
with its judgment and measurements. Each gate starts with fresh measurement
artifacts, so a fast-tier stall cannot import remaining checks from a prior run.

A gate that could not start or crashed (`gate-harness-error`), a recorded boundary conviction
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
Two holds of this kind stand for the loop's own defects, held on the one item they
touch so the loop goes on. The owner ruled that the loop never stops on one item's
condition. `decision-collision` holds an item whose planned decision could not be
recorded, because its decision id is already recorded with other columns.
`settle-failed` holds an item whose run could not be settled because of such a
collision, naming the step and the error. Other settle errors keep their own rules: a
step's usage refusal is a configuration fault and ends the loop, a judgment the loop
cannot record fails the step, and a store that refuses the hold itself ends the loop.
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

A dispatch whose staging failed retracts its dispatch row and releases its claim
in one transaction. Its aborted staging remains in the append-only event history,
but a later plan may assign the same unstarted attempt to another lane without
colliding with that row.

## Staging without Git landing

A store's [definition](https://github.com/mschulkind-oss/tillr/blob/main/tillr/definition/README.md#keys-types-defaults-and-readers)
sets `completes`. When it is not `landing`, planning names `work` instead of the
Git working-directory default, and staging creates an ordinary directory there.
An explicitly configured different relative working directory is retained. Staging
writes the brief and session identity beside that directory and keeps a per-run
copy, but creates no worktree, base sha, fence, or Git target metadata. A lane that
still contains Git staging metadata is refused rather than reset or silently reused.

The definition's `brief_template` is required for this staging. A relative template
path resolves under the drain's root; an absolute path is read as supplied. It does
not fall back to the Git `--brief-template`. Supported placeholders are `ITEM_ID`,
`TITLE`, `TASK`, `CRITERION_1` (the numbered criteria), `RUN_ID`, `BACKEND`, `TARGET`,
`TARGET_KIND` (`plain directory`), `DATE`, `TIMEOUT_SECONDS`, `DURABLE_DIR`, `SETTLED`,
`FACTS`, `METHOD`, and `VERIFICATION`. Staging supplies no Git base, fence, or trunk
values; a template asking for an unsupported placeholder is refused before creating
the working directory. A missing item title or criteria remains an item-content
refusal, but an item needs no allowed paths for plain staging.

For Git landing, staging still requires `working_dir` to be `worktree` and uses the
existing Git brief template and boundary files. This rule belongs to staging, not
to `tillr run exec`: the runner continues to accept relative working directories
and refuse absolute ones. The [plain-staging tests](./plain_stage_test.go) cover
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
minutes on its clock. A re-check is one staging attempt. If it is refused the same way,
nothing is written. If it stages, the loop writes one `drain.lane-restored` event and
prints one line. A restarted loop reads the recorded condition and does not record an
unchanged one again. It prints the line once more for its own operator.

`tillr view waiting` shows each lane out of rotation as a row named `lane:<id>`, with the
remedy as its mover. `tillr view lanes` prints the condition under the lane's row.

An ended run's surviving processes can also keep a lane out of rotation, with reason
`lane-run-children-live`. The store lease is released, but after any release or expiry
reaping and before offering a configured lane, the loop checks processes carrying both
`TILLR_RUN_ID` and `TILLR_LOOP_STORE` (the canonical store path) in their inherited
environment. Successful release events also identify ended runs when a legacy allocation
left no terminal run row. This check applies to both landing and hook definitions. Only processes whose current working directory is inside that
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
next lane, as it does for an item with no conflict. Before 2026-11-17 the move was left in
place. An awaiting conflict then read `conflict-design` with no design run alive, and a
closed answer held its item as `run-link-missing` or `retry-stale` for an operator. At
each pass the loop recognizes those records: the item's latest dispatch is examined
(`drain.examined`), its run has no terminal record, and the conflict is still as the claim
left it. It undoes the move, prints one `loop: <item> run <run> was claimed but never
started` line, and the item is dispatched again with no operator command. The
[unlaunched-claim tests](./drain/unlaunched_claim_test.go) cover the design run and the
build re-offered in the same pass, and the recovery of the old records.

A **lane copy** is our term for a lane commit that is kept somewhere else. Staging resets a
lane copy rather than refusing the lane. Two things make a commit a lane copy. A run pin
(`refs/tillr/runs/<run>`, retained until the item finishes or the attempt ages out
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
checks and the reset for you yet; one is queued work.

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
The [adapter README](https://github.com/mschulkind-oss/tillr/blob/main/tillr-loop/README.md#step-programs-belong-to-the-running-build)
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
[definition package's hook contract](https://github.com/mschulkind-oss/tillr/blob/main/tillr/definition/README.md#hook-program-contract)
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
`--store` and both verdict values. Fixer gates check both `{green}` expansions
(`--all` and `--since <sha>`) separately, because those options are mutually
exclusive. The [tillr-loop adapter](https://github.com/mschulkind-oss/tillr/blob/main/tillr-loop/README.md) implements the mode.
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

A held candidate is retained under `refs/tillr/runs/<run>`, independently of its
lane. Gate stalls, ordinary item faults, held conflict-repair staging, dirty-trunk
waits, red-cap waits for green proof, and rewritten-trunk waits release an ordinary
lane only after the drain
writes that pin and reads it back as the exact candidate commit. It saves the
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
releases and resets it, like the first hold did on the original lane. Before 2026-11-17,
a replay kept the earlier `released` flag. A second hold then freed the lease without the
reset, and the pin moved on to another lane. That left a copy of the candidate on every
lane it passed through. Restart recovery finishes incomplete staging, including
a checkout materialized before its event. The candidate's head, base, run identity,
original fence, and any passing verdict survive lane reuse. This starts no new worker
and spends no new model attempt. A retained passing verdict retries landing, not gating.
Staging checks cleanliness even when the checkout already matches the pin, so
inspection edits cannot be discarded by a landing. Operator examination uses the
same assignment and staging protocol, without requiring the released old lane;
setting aside a released candidate records its pin and does not touch a sibling
now occupying that lane.

These classes still keep their lanes:

| Class | Why the worktree is needed |
| :--- | :--- |
| Conflict repair owning the pending transition | The repair request owns the staged checkout; settlement must finish that request, not release the original candidate's lane. |
| Fork-delivery candidate | Its lane-specific fork checkout and patch branch are delivery state outside the ordinary repository checkout. |
| Workflow candidate or unfinished settlement/continuation | Staged node inputs, working files, brief, and session belong to that lane. |
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

Spend reports distinguish work started in this loop from earlier loops' runs settled
here. The dispatch records the launching loop's identity, so a replacement loop does
not claim the cost of a surviving worker as new work. Pass records preserve those
two groups separately. The loop also prints the sum of all recorded runs in this
store, including runs ended with `--end-runs`, regardless of their exit state.
Each sum counts unreported runs and missing token or price measurements; a sum that
omits a measurement is labeled a lower bound, not an estimate of the missing spend.
A final snapshot never waits for or signals a surviving worker: a terminal record
written after the loop exits appears in the next loop's recorded-spend total. A repair ended by
an explicit `tillr drain stop --end-runs` is different: it is recorded as
interrupted, so the next loop pins its head (`refs/tillr/runs/<run-id>`), resets the
lane and dispatches again without judging it, even when it left a commit. That commit
is kept under the pin but never gated; this is the cost of an end that spends no
attempt.

Measured 2026-11-16 with the
[repair-staging regression](./conflict_hold_test.go): before containment, a staging
error propagated through examination completion to the loop's deferred cancellation,
interrupting two active siblings. The stored candidate lacked a completed-examination
event, so a replacement loop selected the same candidate for judgment again. The
regression now requires both siblings to land, two unchanged restarts to stage nothing,
and a corrected policy input to recover the original repair. The original failure event
and candidate commit remain available. The
[interrupted-recovery regression](./conflict_recovery_restart_test.go) also stops the
loop after a recovered worker commits, leaves that worker running until it is released,
then requires an unchanged restart to land that commit with zero new dispatches and a
repair transcript that only grew: everything written before the stop is still its prefix.

## Earlier uncommitted work

Before resetting a settled lane, the loop saves its uncommitted edits at
`refs/tillr/preserved/<run-id>`. This includes tracked edits and nonignored untracked
files, both inside and outside the item's fence (the paths the worker may change).
Files outside that fence are marked individually in the `drain.work-preserved`
settlement event. The event names the item, run, ref, repository, stopped reason,
and summary: file count, added and deleted text lines, binary-file count and
outside-fence count. Git reports a dash instead of line counts for binary files.
A clean lane creates no preservation ref or event.

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
recording the event. Before resetting on a replay, it checks that every current
dirty path is covered by the saved working or staged version. Newer uncovered
edits retain the lane; the loop does not overwrite the older evidence or discard
the newer work. A recorded pruning prevents replay from recreating the ref.

A retained lane is that lane's condition, not the loop's: its item is held
(`preserve-failed`) and every other lane goes on. When the item is already terminal
(landed, closed or resolved elsewhere), there is nothing to retain the lane for. The
loop records the lane's `git status --porcelain` in a `drain.lane-discarded` event,
aborts any rebase, merge or cherry-pick left in progress, pins the head, and resets
the lane back into rotation. Only a store that cannot record the hold or the discard
stops the loop. Every lane reset aborts an operation left in progress first, because
a checkout keeps that state and the next rebase there would refuse.

Preservation finishes before reset, including a workflow restart and a conflict
repair; Git runs outside SQLite write transactions. An ordinary lane reset then
removes the nonignored untracked files the preservation saved, so a released lane
is clean for its next run; before 2026-11-17 it left them in place, and a pinned
candidate staged onto such a lane was refused (`lane-holds-uncommitted-work`, see
"Lanes that staging refuses"). The removal runs before the reset, under the ignore
rules the snapshot read, so a file the run's own ignore edit hid stays in place
rather than being deleted unsaved. Ignored files always stay. A workflow restart
saves nonignored untracked files before its cleanup.

Both snapshots are written from temporary indexes, never the lane's own index. The
working snapshot reads HEAD and adds the working copy. The staged snapshot copies the
lane's index first, so a lock on that index cannot block it. An unmerged entry in the
copy (a conflict the worker resolved in the working copy and never added, lane-14 on
2026-11-18) is taken from the working copy, and the record names it under `unmerged`
and in its summary. An index that cannot be read keeps only the working snapshot, and
the summary says why. A stale `index.lock` is a lock no process holds open, in a lane no
process has as its working directory, read from `/proc`. Git holds the lock open until it
renames it, so such a lock has no writer left. That happens when a killed git leaves the
lock behind (lane-18 on 2026-11-18). Preservation and every lane reset remove a stale
lock first and record a `drain.stale-index-lock-removed` event (`index_lock.go`). The
age of the lock is never read. A lock that a live process holds stays, and the
`preserve-failed` detail names that PID, which is the hold's mover.

> [!WARNING]
> **Landing remains blocked.** A dirty tracked ignore file can hide work from the
> snapshot, then resetting that file changes the ignore rules and the subsequent
> workflow cleanup deletes the hidden work. Cleanup must run after preservation
> but before resetting the ignore rules, with a regression for that sequence.
> The independent review reproduced this on 2026-11-17. The separate
> [operator-stop regression](./document_wait_test.go) also fails on base
> `f4bbfaf26949fd3770110467a9f47bb6c01e4428` with `GOMAXPROCS=1`: queued SQLite
> stderr can follow the final explanation and break the combined-output suffix
> assertion. This run stopped under its out-of-scope-failure rule; neither blocker
> is waived by passing reruns.

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
[landing contract](https://github.com/mschulkind-oss/tillr/blob/main/tillr-land/README.md#known-cases-the-marker-does-not-settle).
Cleanup uses the store's durable landing records and declared terminal item states,
not candidate ancestry: a squash need not retain the worker commit on the trunk.

| Namespace under `refs/tillr/` | Writer and lifetime |
| :--- | :--- |
| `runs/<run>` | The loop or lander pins an attempt. Settlement removes every attempt of a landed, closed, resolved-elsewhere or removed item, including conflict repairs, after any live lane for that item releases it and its pending examination settles. For retryable items, all live and unexamined/retained attempts stay; at most eight older examined attempt pins per item stay, ordered by attempt time and run id. There is no age limit on a live or retained candidate. |
| `landed/<run>` | The lander writes before publication. Cleanup requires that run's durable `landed` row and a matching published commit. Ordinary markers leave in settlement; fork-delivery markers wait until no pending examination or leased lane remains, so every overlapping boundary comparison has finished. Missing receipts keep markers for crash recovery. |
| `preserved/<run>` | The loop saves uncommitted edits. The [saved-edit retention rule](#earlier-uncommitted-work) removes them when the item finishes or is removed; the pruning event remains. |
| `held/<name>` | An operator writes this by hand. No tillr program writes or deletes it; no age or count bound applies. |
| `recovery/<name>` | An operator writes this by hand. No tillr program writes or deletes it; no age or count bound applies. |
| `green` | The proof process advances this one pointer to proved trunk history. Cleanup never deletes it. |
| `trunk-history/observed`, `trunk-history/lost`, `trunk-history/accepted/<lost-tip>` | The loop tracks the observed trunk and retains unresolved lost history. Accepting a rewrite removes `lost` and keeps its accepted-history pointer indefinitely. This cleanup deletes none of them. |

Set-aside also writes `runs/<run>-set-aside-base`, a pointer to the lane's reset
destination so an interrupted reset can resume. It follows its owning run's
retention rule and is removed with the finished item's other pins.

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
retention as the loop. A second sweep removes nothing unless lifecycle state changed.

Deletion is guarded by each ref's observed commit and never follows symbolic refs
to their targets. A short store write transaction
excludes lane allocation and state changes while Git atomically deletes the planned
refs. It writes no item state. If Git rejects a changed ref, the command reports an
error rather than deleting a newer pointer. Run this against the repository's own
store; run ids must not be reused across stores sharing the repository.

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

File a fork item with a fence entry such as `.forks/lanes/lane-1/pi/`, optionally
restricted to a subdirectory. The filed lane id is a placeholder, not a reserved
checkout: staging rewrites it to the dispatched lane in both the brief and the fence,
materializes that checkout, and starts it at the delivered patch tip. The worker
commits only in that checkout and must not run `bin/pi-forks export`. The lander alone
moves `refs/heads/patch/pi`, marks the delivery for sibling boundaries, and refreshes
the shared cache's `main`; two items for the same fork remain serialized.

The fork gate is [fork-gate.py](fork-gate.py), whose `fork-build` check runs through
`tillr-gate` on a clean copy of the exact candidate head with the pack's declared
build command. The ordinary repository gate's candidate-build step reconstructs the
currently delivered branch, not this unlanded head, so it cannot certify a fork item.
A pack with no build command explicitly receives a source-only check instead. Build
output stays beside the run's gate records, never in the worker's checkout. A failed
build leaves the patch branch unmoved, and a passing verdict lacking `fork-build` is
not a fork-delivery verdict. The lander publishes the built commit itself rather than
squashing fork history. Dirty or divergent cache and writer checkouts are refused,
not reset. A missing shared cache is prepared from the gated fork's upstream metadata
and published only when complete; recovery of an earlier receipt keeps a later delivered
tip in that cache. On 2026-11-17,
`go test . -run 'TestFork|TestAForkLane|TestPatchBranchRetirement' -count=1` in this
directory passed. The integration case exercises failed build refusal, two overlapping
lane boundaries, the main checkout's `bin/pi-forks check` with existing and freshly
created caches, and two successive fork landings.

## The fence, and the session

A run's fence is derived from the item's `allowed_paths`, in one function, and every reader
of it — staging, the planner, the escape check, a resume — reads that derivation rather than
repeating a rule. The item names the paths its change is about; the derivation adds the ones
the change is *obliged* to touch, because a fence that omits them refuses compliant work
after it is done:

- the pack's copy of every tillr module the fence names
  (`yolo-packs/tillr/files/local/share/tillr/<module>/`), which `just pack-sync` rewrites;
- for a tillr module, the `tillr-operating` skill and the documentation suite, plus the
  module's own `README.md` when the fence names a sub-path inside the module rather than its
  directory;
- `tools/tillr-loop/` as a whole when the fence reaches that module, whose `test.py` and
  `README.md` sit beside the program, plus the drain's embedded adapter copy and that
  file's pack mirror so a later adapter-only change can commit all required copies;
- `tests/fork-check.test.py` for `tools/fork-check.py`, and
  `tests/tillr-muse-backend.test.py` for `tools/tillr-run/backends/`.

A fence that names none of those derives nothing: this is a rule about the paths given, not a
widening of every fence.

**A pool lane is not a session.** Staging mints a fresh harness session id for an ordinary
dispatch and writes it beside the brief as `session-id`; it never treats that file as the
next item's session. `resume` explicitly names the id it continues. Review-comment items
instead reuse their document's recorded designer at a fixed
[desk](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-designer-sessions.md#the-desk), outside the numbered pool.
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
path chooses a session by the design's ordered
[dispatch table](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-designer-sessions.md#choosing-at-dispatch).
Warm means the last request plus its reported cache lifetime; unknown retention is expired.
Only an expired session over 40,000 tokens is compacted, and only when its adapter ships an
executable compact step. Otherwise it resumes with `resumed-expired`. Nothing runs at expiry.
Compaction receipts record before/after token measurements and reported cost against the item;
missing cost stays unknown. Automatic intake remains a subsequent build item.

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

A mover is the question, blocking item, run, lane, or operator action that can
advance the item. The summary partitions the rows into running items, items the
next pass could advance, and holds counted by reason. Busy lanes include unfinished
candidate examinations, not just live leases. Capacity is consumed in queue order,
so an earlier predicted dispatch can leave a later item waiting on a lane.
Closed and resolved-elsewhere items remain visible with no further work required.

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
tillr-drain resume --run <run-id>
tillr-drain loop
```

`plan` is the read-only half an external caller composes against its own reporter; `loop` is
the whole service and needs the three judging programs named.

`resume` is one attempt continued and not a second attempt: the run id, the lane and the brief
are the ones the run already had, the session the lane carries is continued so the provider's
prefix cache is warm, and the store gains a `run.resume` event on the same run rather than a
second `runs` row. It re-takes the boundary bracket at the resume and
re-derives the fence from the item's `allowed_paths`, because the killed segment may have
committed before it died. It refuses a run the gate judged: continuing judged work is a
retry, which a person authorizes.

A normal build exit with no commit and uncommitted fenced work is continued in its own
session, uncounted, for as long as each continuation changes that work (the owner's ruling
of 2026-11-17, audit finding F-09). Each continuation preserves the worktree and asks the
worker to finish verification and commit, using the same attempt and the existing resume
bracket. A committed continuation is settled normally. A continuation that leaves the
uncommitted paths and their content exactly as the previous one did is refused as `no-op`,
naming the remaining paths, and goes back to the worker as
[a refused run](#a-refused-run-goes-back-to-its-worker) does. A replacement loop continues
a continuation a stop killed again
(`TestDirtyExitContinuesForAsLongAsEachContinuationChangesTheWork` and
`TestDirtyExitContinuesAKilledContinuationAgain` in
[`drain/commit_continuation_test.go`](./drain/commit_continuation_test.go)). Clean exits, design/comment work and outside-fence-only edits do not trigger it.
Missing brief/session state is named rather than replaced by a cold build. See the
[design choice and limits](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-build-commit.md).

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
- **An unchanged re-dispatch nobody authorized** — automatic dispatch needs a recorded
  input change and an unspent allowance; otherwise a retry is an operator's act.
- **A resume with no session to continue, or of a judged run** — the first would pay for the
  whole transcript again while recording itself as a continuation, and the second spends a
  second attempt on work a verdict already answered.
- **A seat it cannot route** — an unmapped seat refuses the whole plan. The refusal names
  the requested seat, the mapped seats, and the backend policy file where seats are declared.
  An unmapped seat is never silently replaced by the pass's default backend.

## Test

`go test ./...` in this directory builds the binary and drives each face against a scratch
store and scratch lane roots; the drain package's own suite pins the decision rules. Pass
`-timeout 60m`: the drain package takes 276 to 562 seconds on a loaded machine, and Go's
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
that both packages with child builds rerun and that a restored tree caches again.

```bash
python3 tools/tillr-drain/cache_test.py  # from the repository root; uses YOLO_DURABLE_DIR
```

This check exercises Go's actual file-input handling, rather than assuming that it sees
sibling builds. Keep it when upgrading Go. It shares the normal Go build cache and never
uses `-count=1`; ordinary Go source compilation still controls the compiled-code cache.
