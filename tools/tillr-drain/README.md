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
skill is [`docs/design/tillr-distribution.md`](../../docs/design/tillr-distribution.md).

## Pass observations and report identity

The [successor design](../../docs/design/tillr-drain-successor.md#25-one-pass-what-it-advances-and-what-bounds-it)
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
[trigger design](../../docs/design/tillr-drain-successor.md#44-s4--the-trigger-wait-replaces-the-ticks-clock).
An empty pass allocates none. Requests carry that generation as their correlation.
Timing measurements live in `drain_passes`, with the existing `tick_id` column
carrying the generation. On write-open, historical `drain_ticks` rows are copied
without replacing new measurements or inventing missing historical values. The old
table remains a migration source; new measurements never write it.

Loop reports are named by their inclusive first and last generations, not by the
clock. A loop that allocated none uses zero endpoints, so a startup fault remains
readable. For example:

```text
drain-1-3.json  -> record id loop-1-3
drain-4-6.json  -> record id loop-4-6
drain-0-0.json  -> no applied generation
```

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
  ([the claude backend design](../../docs/design/tillr-claude-backend.md#4-falling-back-when-the-window-is-spent)).

## Rolling trunk audits

With no watermark (the tip last judged green by the rolling checks), the default rolling
run audits every tracked path at the trunk tip with every applicable rolling-tier check,
regardless of the repository gate's remembered baseline.
The rolling tier is the group of slower checks deferred from individual landings;
a matching cached verdict may answer a check without executing it again.
Later runs select checks over the range since the watermark.

A green run advances the watermark, but that alone does not claim full coverage:
`rolling_runs` binds the outcome to its base and tip, and `rolling_coverage`, joined
by `run_id`, retains the fresh execution count (`ran`), reused verdict count (`cached`),
and every skipped check's identity and reason (`skipped_checks`). Skips include
checks assigned to the other tier as well as unchanged or unavailable checks.
`spans_state` says whether the program supplied readable evidence; older runs have
no coverage row, and absent or unreadable evidence is not an empty audit.
The loop's rolling line prints separate ran, cached, and skipped counts, or explicitly
says counts are unavailable when the program supplies no readable evidence.
Red or unknown runs do not advance the watermark.

## Automatic second attempts after an input changes

A held item's next pass compares its current item definition (including the referenced
body section), allowed paths, backend policy, and running tillr build with the inputs
recorded when its failed run was dispatched. A change permits one automatic dispatch;
its recorded decision names every changed input. This is the
[design's re-admission rule](../../docs/design/tillr-convergence.md#32-every-hold-names-a-mover-that-can-act),
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
drain retains the already-gated candidate and its lane without a live worker
lease. It records `trunk-path-dirty: ` followed by the sorted dirty paths as an
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
another gate, following the [landing principles](../../docs/design/landing-principles.md).

Each pass compares the trunk with `trunk_history`, which stores the last observed
tip and a lost tip when that observation is no longer an ancestor of the current
trunk. After initializing object retention, an unchanged observation is read-only:
it does not compete with runs for the store's writer lock. New tips and rewrites
are persisted even when no candidate is waiting, so replacing the loop cannot erase an observed
rewrite. The loop retains those commits under `refs/tillr/trunk-history/observed`
and `refs/tillr/trunk-history/lost`, so reflog expiration and Git garbage collection
cannot erase the ancestry evidence. A rewrite holds only landings. Runs continue, candidates can be gated,
and already-gated candidates retain their lanes and verdicts. The hold decision is
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
| Target's own checked-out branch, assigned patch refs, registered sibling branches, `refs/tillr/runs/` pins, the lander's `refs/tillr/landed/` markers, and the two trunk-history refs | Ignored | These are deliveries or concurrent loop work. |
| `refs/heads/patch/<fork>` moved to a commit that a `refs/tillr/landed/` marker written during the bracket names | Ignored | That is a sibling's fork-series landing: the lander marks the squash commit before it moves the patch branch. |
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

A gate that cannot judge (`gate-harness-error`), a recorded boundary conviction
(`host-boundary-violation`), a lane whose HEAD differs from the recorded candidate
(`candidate-record-mismatch`), or an item definition that cannot be synced
(`definition-sync-failed`) holds only the affected item. Other items keep running
and landing. A sync finding skips its entire document, so every stored item whose
definition comes from that file is held, not just the item named by the finding.
These holds survive a supervisor restart and do not record a failing
criterion verdict or spend a new model attempt. A retained candidate keeps its lane
reserved without a live worker lease.

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

## Step-program argument contract

A **step program** is the configured gate, fixer gate, land or verdict command that
receives the candidate's flags. Its help invocation (`<command> --help`) must judge
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
exclusive. The [tillr-loop adapter](../tillr-loop/README.md) implements the mode.
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

## Conflict repair that cannot be staged

A conflict-repair staging failure holds only its affected item as
`conflict-repair-staging-failed`; it does not cancel sibling workers. The candidate
commit is pinned before the lane is reset. A `drain.repair-held` event retains the
candidate, original conflict reports, exact staging error, input comparison, and next
action. The lane remains reserved for that candidate, without a live worker lease.

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
  `README.md` sit beside the program;
- `tests/fork-check.test.py` for `tools/fork-check.py`, and
  `tests/tillr-muse-backend.test.py` for `tools/tillr-run/backends/`.

A fence that names none of those derives nothing: this is a rule about the paths given, not a
widening of every fence.

**A pool lane is not a session.** Staging mints a fresh harness session id for an ordinary
dispatch and writes it beside the brief as `session-id`; it never treats that file as the
next item's session. `resume` explicitly names the id it continues. Review-comment items
instead reuse their document's recorded designer at a fixed
[desk](../../docs/design/tillr-designer-sessions.md#the-desk), outside the numbered pool.

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
[dispatch table](../../docs/design/tillr-designer-sessions.md#choosing-at-dispatch).
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
[design choice and limits](../../docs/design/tillr-build-commit.md).

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
