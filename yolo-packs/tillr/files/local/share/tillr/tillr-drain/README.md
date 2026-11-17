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
operator's authorization to re-dispatch a run the harness refused; `clear-fault`
acknowledges a diagnosed item-local fault so its retained work may advance; `boundary` records the
write boundary before and after a run and compares the pair; `resume` continues a run that
died without a verdict from its own harness session; `loop` is the unattended supervisor.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/design/tillr-distribution.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-distribution.md).

## Opted-in judgment before the gate

An item directive with `judge="agent"` asks for an independent, read-only review
of its committed candidate. Before dispatching the worker, planning requires one
unexpired grant covering every criterion's claim class; otherwise the item waits
as `judge-grant-absent`. The policy must map the `review` seat. Items without that
attribute keep the existing mechanical retirement path and start no review run.

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
`judge-unreadable`, attributed to the review run rather than the worker; it does
not consume the worker's automatic re-admission or second-failure allowance. A valid
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

With no pointer yet, dispatch uses the tip and the loop reports that choice once.
An invalid pointer or one outside trunk history is an error, not that bootstrap
case. Existing workflow stages retain their recorded base. An idle lane already
at a newer trunk commit can be reset to green because trunk retains its work;
a commit neither the selected base nor trunk contains is still refused as
unlanded work.

The [lane-base regression tests](./drain/lane_base_test.go) cover the selected
worktree commit, recorded base, blocker proof wait, fixer exception, and bootstrap
notice.

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
  dispatches to the list's first backend, and a failover walks that seat's list forward and
  stops at its end, so design work falls back to a design backend and never onto the build
  seat's. A step along the list is an ordinary `failover`, not `seat-degraded`: every
  backend on it was named for that seat. A seat with one backend, and an item with no seat,
  walk the policy's `preference` as before. Every name in a list must be one of the policy's
  backends; the check refuses the policy otherwise and names the seat
  ([the claude backend design](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-claude-backend.md#4-falling-back-when-the-window-is-spent)).

## Document-fed workflow prerequisite

**An item needs a document block before it is built.** This prerequisite belongs
to the document-fed Git landing workflow, not to the queue’s general readiness rules.
A store is document-fed when at least one stored item, in any state, has a block in
the tracked documents that sync reads. A store with no such item keeps its ordinary
seat and workflow routing. Closed, landed, and otherwise terminal items are never
counted as unshared or sent to triage.

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
those fields permits one new run; changing rank or state does not. Waiting output
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

Supervision requires Linux child adoption and stable kernel process handles. Adoption
makes the supervisor the parent of abandoned grandchildren, including legacy commands
in separate sessions; the handles prevent signaling a reused process ID. It waits for
the kernel to confirm every descendant has exited before acknowledging cleanup, with
no cancellation timeout on that acknowledgement. `heavy.cleanup-pending` remains beside
the result store until confirmation. A remaining guard reports unconfirmed cleanup and
blocks checkout removal and recovery until an operator independently recovers those
processes and clears it; a released lock alone is not treated as cleanup evidence.

The runner judges every heavy tree declaration, reusing the gate's remembered results,
and the legacy rolling checks that have not yet migrated to declarations. `--heavy-gate`
names the declaration judge; `--rolling` still names the legacy program. An explicitly
empty `--rolling` disables that legacy coverage, not the declaration checks. Missing
programs or missing verdict evidence are reported as faults, never silently passed.

A run with no failing or unjudged check advances `refs/tillr/green` with a guarded Git
update: the value read before judging must still be the current value, and it must be
an ancestor of the judged tip. Unavailable checks are listed, not counted as passes.
A failing check holds the pointer and is run alone at the pointer and by bisection on
recorded landings in the range. Legacy span IDs retain their kind and label so replay
uses the old program's selector vocabulary rather than confusing its stable report ID
with a selector. Red at the pointer is a gate fault, naming the check's needs and tools;
otherwise the first red landing receives a fixer: an item that repairs the broken trunk,
containing every check attributed to it. Its fence (the paths it may change) combines the
checks' declared sources and tests with the paths touched by every commit since the last
green through the current tip, even paths later reverted. Legacy checks obtain their read
paths from the gate's declaration export, not their recorded output writes. The gate's own
code is excluded, including directories that would admit it indirectly.

A later red tip for the same check attaches to an unresolved fixer rather than filing a
second repair. The attachment records the new observation and combines its paths without
clearing an existing owner hold. A blocked fixer report goes directly to the owner; a
worker whose fix needs paths outside its fence must name those paths in its report. The
waiting face names the fixer, its failing checks and whether it is waiting on the owner.
With no pointer yet the report says there is no proved baseline to accuse a landing; it
does not claim that a nonexistent pointer was judged red. Automatic revert
is not implemented: selecting `auto_revert` reports that limitation and holds the pointer
instead of silently filing a fixer in its place.

Budgets are reported targets, never deadlines. The summary names each overrun's measured
and declared times without changing the verdict. A sampled stall is retried once on the
same tip; a second stall is red and files a fixer. A loop stop ends check processes before
removing their checkout. `tillr-view status --store <store> --root <repository>` reports
the green commit, recorded landings since it, first-red attribution, unavailable checks,
and overruns. The shared report lives beside the result store in the Git common directory;
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

## Automatic second attempts after an input changes

A held item's next pass compares its current item definition (including the referenced
body section), allowed paths, backend policy, and running tillr build with the inputs
recorded when its failed run was dispatched. A change permits one automatic dispatch;
its recorded decision names every changed input. This is the
[design's re-admission rule](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-convergence.md#32-every-hold-names-a-mover-that-can-act),
not a retry of unchanged work. Queue rank, timestamps, criterion verdicts, and an advancing
base commit do not grant another attempt.

If that automatic attempt fails, later input changes do not dispatch it again.
`tillr-view waiting` says that the automatic attempt failed and names `tillr drain retry`
as the owner's next act. An authorized retry begins a fresh allowance, but unchanged
inputs still hold. Older runs with no recorded inputs need an owner retry rather than a
guessed comparison.

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
The interruption is recorded before signaling. It is no attempt outcome: no retry,
failover, or automatic re-admission allowance is spent, regardless of how often it
happens. The next pass dispatches the item again on its interrupted backend.

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
own; the stop's cancellation kills that whole group, a normal exit kills whatever the
program left in it, and the step waits (up to five seconds) for the group to be gone before
it removes its scratch worktree. A pin (`refs/tillr/runs/<run>`) that a stop interrupts is
finished rather than cut off, bounded at thirty seconds, because the pin is what keeps a
candidate reachable after its lane is reset.

For an unbounded claim with no terminal record, replacement loops compare the recorded
process ID and start time within the same Linux boot, process namespace, and user
identity, then check that no worker carrying that run and store identity survives.
Only a proven-dead owner with no live worker is reclaimed. Foreign or legacy claims
without that evidence remain protected. A normal offline finish is still judged;
a worker signal without evidence of supervisor interruption is not exempted.

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
stay as they are. The pass pins the run's head under `refs/tillr/runs/<run>`, resets and
releases the lane, and records one `drain.item-already-terminal` event that names the
item's state. No retry or re-admission is spent.

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
The loop keeps waiting even with `--exit-when-drained`. There is no command here
to accept a deliberate rewrite. The drain also passes its observed tip to the
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
Clearing a
sync fault forces document sync again even at the same trunk commit, so a definition
that is still broken is held again rather than run from stale store content.

A dispatch whose staging failed retracts its dispatch row and releases its claim
in one transaction. Its aborted staging remains in the append-only event history,
but a later plan may assign the same unstarted attempt to another lane without
colliding with that row.

## Lanes that staging refuses

Some staging refusals are about the lane, not the item it was offered. The next item
offered that lane would be refused the same way. There are two such conditions:

| Reason | What the lane holds | Remedy |
| :--- | :--- | :--- |
| `lane-holds-unlanded-change` | a commit the base does not contain, which staging will not reset | land the change, or reset the lane as described below |
| `lane-branch-held` | its branch, checked out by another worktree | remove the holding worktree, or stage a different lane |

The loop takes such a lane **out of rotation** for the session. That means no plan offers
it an item, and the item it was offered is offered the next free lane in the same pass.
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

Item-owned staging failures keep their behavior: they count toward the streak. Each item
and lane prints its line once per distinct error, not once per pass.

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
records completed staging. Restart recovery finishes incomplete staging, including
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
spends at most the existing one conflict-fix run. The waiting view names the item,
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

Preservation finishes before reset, including a workflow restart and a conflict
repair; Git runs outside SQLite write transactions. Ordinary lane reset leaves
untracked files in place. A workflow restart saves nonignored untracked files
before its cleanup.

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
`refs/tillr/runs/` have a separate lifetime and are not deleted by this rule.

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
prints rank, item id, position, reason, and mover for every item not stored as
`landed`, and ends with a summary. Naming an item prints its full explanation,
including the planner decision. `--json` gives rows and summary as one object,
or the selected item's record alone. The JSON answer also carries the event
sequence at which the planner read the store.

A mover is the question, blocking item, run, lane, or operator action that can
advance the item. The summary partitions the rows into running items, items the
next pass could advance, and holds counted by reason. Busy lanes include unfinished
candidate examinations, not just live leases. Capacity is consumed in rank order,
so an earlier predicted dispatch can leave a later item waiting on a lane.
Closed and resolved-elsewhere items remain visible with no further work required.

Use the loop's policy and lane offers to reproduce its next decision: those inputs
are not retained in the store. With no policy, readiness is held as `no-policy`,
not assigned a guessed backend. With no lane list, the planner offers every lane
in the store. The default clock is the current time; `--now` supplies a fixed
observation time. Document questions use the same root and index command as `plan`.

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

A normal build exit with no commit and uncommitted fenced work gets one automatic
continuation before a `no-op` refusal. It preserves the worktree and asks the worker to
finish verification and commit, using the same attempt and the existing resume bracket.
A committed continuation is settled normally; a still-dirty refusal names the remaining
paths. Clean exits, design/comment work and outside-fence-only edits do not trigger it.
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
- **A seat it cannot route** — an unmapped seat, or a seat outside the item model's closed
  vocabulary, refuses the whole plan with the seat and the mapped seats named; it is never
  silently replaced by the pass's default backend.

## Test

`go test ./...` in this directory builds the binary and drives each face against a scratch
store and scratch lane roots; the drain package's own suite pins the decision rules.

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
