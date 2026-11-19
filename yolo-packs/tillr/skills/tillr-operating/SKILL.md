---
name: tillr-operating
description: Compose the eight `tillr*` binaries across the seven-step loop — plan, stage, run, judge, land, verify, audit — in a repository that has a tillr store. Covers the order, the one command per step, and which exit code means what; every face's flags are learned from the binary, not from this skill.
---

# Operating the tillr loop

> [!NOTE]
> **Operating boundaries (verified 2026-11-19 at `303ad3fcf`).**
> 1. **Pack launchers vs live source:** the `tillr*` binaries on `PATH` run the rendered pack copy under `~/.local/share/tillr/<module>/`, whereas this repository's daemon runs from `just tillr-build`. Edits under `tools/tillr*` reach pack launchers only after `just pack-sync` and `yolo host apply --assert`.
> 2. **Supported seam:** external callers compose `tillr` through processes, exit codes, explicit `--store` paths, and versioned JSON records (`record_version: 1`). Go packages, SQLite tables, and `.tillr/loop` layout are internal and move continuously.

`tillr` is eight binaries that act on one store. `tillr` is the umbrella dispatcher; the
seven `tillr-<noun>` binaries are the tools it dispatches to, and each also runs on its
own. **Learn a face's flags from the binary**: a face invoked without its required flags
names them in its refusal, and the flags are the tool's own — this skill never restates
one.

Every face takes the store it acts on, and a face invoked without one says so.
`tillr store init` creates a store with every module's tables and binds its charter
(`git-landing` by default, or a custom charter file for workflows that finish through a hook
without Git landing; `tillr store definition` inspects it). Nothing here initializes a
repository on your behalf, and no binary does it implicitly.

`tillr drain loop` is a daemon by default: it keeps running when nothing is
dispatchable, waiting for a new item, a changed hold, or a trunk move. Its waiting
line names the held count and the first five items with their reasons, then stays
quiet until something changes. SIGINT or SIGTERM stops it with exit code 0 and a
final line saying it was stopped by the operator. The runs are left alone on a
signal, a fault, or any other ordinary exit. A restarted loop settles and judges
those runs once, including ones that finished while it was absent.

New lanes normally start at the last proved trunk commit, selected by the
repository's `lane_base` gate setting. A `trunk-unproved` hold means a finished
blocker's landing still needs proof from the heavy runner, not more work from
the blocker. A lane whose item's block is newer than that commit starts at the
oldest trunk commit carrying the block instead, and a worker's `blocked` report
from a base that lacked what made its item ready is restaged by the loop, never
sent to the owner. The `tip` setting opts out; a trunk fixer always starts at the tip.
With no proved pointer yet, the loop uses the tip and says so once. The
[lane-base contract](../../../../tools/tillr-drain/README.md#the-commit-a-new-lane-starts-from)
explains recorded bases and safe reuse of idle lanes.

An item may ask for an independent review with `judge="agent"` in its directive, and a
workflow may declare a read-only evaluator node in `exit.evaluator_slot` (such as
`planned-build@v1`, which runs `plan`, `work`, and `verify` by default on build work;
naming `single-worker@v1` skips planning and that evaluator when one worker run is enough).
Both require a covering unexpired grant (`tillr item grant`) before dispatch and a mapped
seat. The read-only judge runs after the fence check and before the gate; only a
successful landing records the checked agent verdicts, without the mechanical
verdict program. A workflow evaluator's `not-done` returns the named criteria to the
workflow's first node as `verify-not-done`, whereas an opted-in item judge holds
`not-done` as `eval-failed`. `judge-grant-absent`, `judge-unreadable`, and `eval-failed`
are holds, not criterion verdicts. The
[opted-in judge contract](../../../../tools/tillr-drain/README.md#opted-in-judgment-before-the-gate)
explains the retained file, failure reasons, and candidate restoration.

Repository settings in `.tillr.toml` can also reserve `[lanes] express` slots outside the
ordinary worker cap (`express-1`, `express-2`, and so on) so saved ideas, CLI requests
(unless filed ordinary), and express-marked blocks do not wait behind background builds.

Use the stop face for an explicit operator request:

```sh
tillr drain stop --root <repository>
tillr drain stop --root <repository> --end-runs
```

To move a running loop onto the binary now on disk, file a restart request. It is a
store fact, so it also works while no loop runs: the next loop answers it.

```sh
tillr drain restart --root <repository> --wait
tillr view restart --store <store> --wait
```

The loop reads the request at the top of a pass, starts no work in that pass, checks
the new binary with `--check`, and replaces itself with the same process id and arguments
without signaling workers. On its next pass, the new loop requests a turn-gap pause (with
no kill timeout) for live workers started on an older recorded build whose adapter declares
`stops-at-gap`, so they continue on the new build as the same attempt; workers whose
adapter cannot stop at a gap finish under the build they started with.
The answer is `restarted`, `already-current`, `refused` (the old loop keeps running)
or `superseded`. Nothing restarts without a request.

Without `--end-runs`, stop ends supervision only: it cancels in-flight gate and
trunk-proof checks without waiting for them to finish, leaves worker runs alone,
and leaves unjudged candidates and proofs for the next loop to resume. With it, the drain records the
interruption before sending SIGTERM to each in-flight run face. It does not ask the
runs to pause, so those items start over on the next pass without
spending a retry or a failover. Repeated stops
need no authorization.

After a jail relaunch, a new loop recovers the old container's runs on its own: a
run whose loop ended its session (or left its presence file unheld) and whose
`run.record.json` and `run.stderr` no process holds open for writing is reclaimed
and dispatched again, with no operator action. The tillr-drain README describes the
evidence.

Stop acts on what its own process domain can see and says what it left alone on
stderr, one `loop: stop left alone: ...` line each: a session in another process
domain (stale heartbeat: treated as dead; current heartbeat: a live loop to stop
there), a session whose loop process is gone, and a run owned by another domain. Its
exit code: 0 when a loop was stopped, or with `--end-runs` when the runs were ended
(it prints "in-flight runs were ended" only then); 4 when a live loop in another
process domain was left running, after the local loops were stopped and, with
`--end-runs`, the local runs ended; 2 when no live loop is registered and `--end-runs`
was not given. With `--end-runs`, a worker whose run face has died is ended too. Stop the keeper first if supervision must stay stopped.

A caller that needs a finite
drain instead of a waiting service should learn the exit option from `loop --help`.
The former stay option is removed; waiting no longer needs an option.

## Change an item's place without changing its work

A [place](../../../../docs/design/observatory-actions.md#3-principles-and-words)
is an item's position in the full order, counted from 1. Read faces show places,
not stored ordering keys. Parked and finished items have no place. Filters keep
true places, including gaps.

Use `tillr item place` to move an existing item; learn its destinations from the
binary. It preserves state, workflow, criteria, dependency edges, fence and runs.
Sync never changes a nonempty key. It fills empty keys after the last keyed item,
in item id order, in its own transaction; readers count the same order before
that sync. New items and items leaving a parked section enter at the end unless
they already have a key. Opening or migrating a store never supplies a key.
Existing item records still accept `rank` through apply and fixture seeding.

## Declare document-fed work before building it

In a store fed by documents, an item needs a document block before it is built.
This is a prerequisite of the repository landing workflow, not a queue rule.
The [drain’s prerequisite reference](../../../../tools/tillr-drain/README.md#document-fed-workflow-prerequisite)
defines the comparison, the one-document triage run on the design seat, and the
three outcomes: a ready declaration, a declaration with an owner question, or a
duplicate/finished report waiting on the owner. Stores with no declared item
continue to route items to their own seats.

## Update a running drain

Start the drain and observatory from the `just tillr-build` bin directory.
After a tillr landing the loop runs `just tillr-roll --no-request`, then requests restarts.
After a manual commit, run `just tillr-roll` (in-place) or `just tillr-restart` (rebuild HEAD, restart keeper and loop).
A refused restart leaves the current process running; inspect the command's per-part report.
Pack launchers use a separate install: run `yolo host apply --assert` before requesting a restart.

## Keep the repository loop running

In this repository, `just tillr-keep` runs the **keeper**: the process that starts
an absent loop, waits for its exit, and starts it again. It does not authorize or
retry an item. It composes the repository's gate, land, and verdict adapters and
leaves the loop's after-merge check enabled. Learn its options from
`just tillr-keep --help`; additional loop options pass through after `--`.

The keeper runs in the foreground. On Linux it finds a loop by its own process
name, `tillr-drain` (including the pack's source-digest suffix), owned by the same
user and running in the same checkout. Its arguments must be the `loop` face for that
checkout: a `tillr-drain why`, `plan` or other face beside the loop is not the loop. The
keeper parses those arguments rather than searching them. More than one matching process
is a refusal,
not a reason to choose one. Start the loop in its repository checkout, not from
another directory with a different root named in its arguments.

After a loop runs for less than five minutes, the waits are 60 seconds, 120,
240, and so on, capped at thirty minutes. A run of at least five minutes resets
the wait to 60 seconds. For an adopted loop, time is measured from adoption.
The wait and each start are printed; startup errors stop the keeper explicitly.
The keeper's per-checkout lock prevents two keepers from starting duplicate loops.

`just tillr-keep --stop` stops only the keeper; the loop stays running. Ctrl-C
on the keeper does the same. Stopping the loop itself is a separate operator
action: stop the keeper first if the loop must stay stopped.

Use ordinary Git on the repository while the loop runs. That covers commits, pulls,
branches, tags, stashes, fetches, `git worktree add` and `remove`, fork exports, and
hand-written pins. None of it convicts a run. A run's boundary check convicts only what it
can attribute to the run: a write into another lane's worktree, or the run's own lane
worktree going away. Every other movement it sees is recorded as an observation on the
run's `boundary.check`. Leave the lane root's own worktrees alone, because they belong to
the loop.

A lane whose own state refuses staging, such as a change nobody landed or a branch another
worktree holds, leaves rotation for the session. A lane commit that a run pin keeps, or that a
recorded landing names, is not refused. Staging resets that lane and records
`drain.lane-copy-cleared`. The loop records it once and offers the
item the next lane. `tillr view waiting` shows it as `lane:<id>` with its remedy. Reset such
a lane only as the tillr-drain README's "Resetting a lane" describes.

An idle lane also stays out of rotation with `lane-run-children-live` while an ended
run's processes carry its run/store markers and have working directories inside the
lane. Its waiting row names the process ids and a `kill` command; wait for them to exit
or end them deliberately. The loop checks this on every pass and restores rotation
once none remain. A marked process outside the lane does not hold it. `run exec` ends
the worker's remaining process group on normal exit too, but children that created
another group can survive and are covered by this lane hold.

## Exit codes, once

Every binary answers with the same contract, so a caller reads a decision rather than a
failure:

| Code | Meaning |
| :--- | :--- |
| `0` | completed; where the face decides, the decision is positive |
| `1` | completed with a negative decision — the record says why |
| `2` | usage error, or a malformed/unsupported input record; nothing ran |
| `3` | store, schema or record version incompatible, or the store stayed locked past the bound |
| `4` | an external dependency prevented a decision (distinct from a failed check) |
| `5` | a bug, not an outcome; any write transaction was rolled back |
| `6` | gate only: `stalled`, no verdict about the candidate |

`1` and `4` are the pair that matters unattended: `1` is a verdict about the candidate,
`4` is the tool saying it could not judge. Retrying a `1` is not a fix.

## 1. Plan — preview dispatchable work
`tillr drain why` and `tillr drain plan`

`tillr drain why` asks the planner why each unlanded item is in its current place, naming
its hold reason, mover, and landed-item costs. `tillr drain plan` reads a pass input record
and writes the resulting `drain.decision` records without mutating the store (answering `0`
even when zero items are dispatchable; `tillr drain once` records and dispatches one pass).
`tillr drain policy` and `tillr drain workflow` validate and print the effective backend
policy and workflow definitions.

## 2. Stage — sync documents or file store-declared work
`tillr item sync` and `tillr item request`

The item state a pass will act on, read from the item blocks in the repository's tracked
documents: sync is how a document-declared item enters a store, and `tillr drain loop` runs
it before its first plan and whenever the trunk moves. A second run over unchanged documents
writes nothing. `1` names a block with a finding, and every other document was still
written (`tillr item reconcile` reports tracked documents that still owe queue work).

For work that needs no Markdown block, `tillr item request` files an item straight from
standard input with a title, path fence, and optional dependencies: it enters the express
pool by default unless marked ordinary, carries four fixed criteria plus optional additions,
and refuses any candidate diff that touches item blocks. `tillr item dep` adds or removes
dependencies on an existing item without rewriting the rest of its record or changing its
place in the queue. `tillr item idea` saves an owner idea,
`tillr item doc-triage` files a one-document reconciliation run, and `tillr item` also
provides `apply`, `list`, `show`, `order`, `state`, `close`, `reopen`, `decision`, `mail`,
`grant`, `verdict`, and `judge`.

### Take a review batch by hand

`tillr item comments take`

This reads one document's pending review threads from Vantage and prints the new item id,
or nothing when none is pending. The loop runs that batch on the design seat at a fixed
[document desk](../../../../docs/design/tillr-designer-sessions.md#the-desk), reusing its last
recorded designer rather than a numbered lane's unrelated session. A desk has a worktree
only during its run. Session selection follows the ordered
[dispatch table](../../../../docs/design/tillr-designer-sessions.md#choosing-at-dispatch):
warm sessions resume unchanged; expired ones over 40,000 tokens compact only when the adapter
provides that step. The compaction receipt records reported cost against the comment item.
A resume that fails before any model response is dispatched once more to a new session,
with the old transcript named in its brief. Deleted or done-role documents lose their rows,
not their transcripts. Automatic intake remains a later build slice.

A committed revision delivers replies after its recorded landing and after the served
checkout matches it. An answer-only run records `answered`, skips gate and landing, delivers
its replies, and closes the item. Delivery waiting on an owner's edit remains in the store
for a later pass; repeated delivery retains the same nonce.

To file fork work, name a checkout in the fence, for example
`.forks/lanes/lane-1/pi/` (or a subdirectory of it). The filed lane is a placeholder;
staging materializes the dispatched lane's checkout and names it in the brief and
fence, starting from the delivered patch tip. The worker commits there only, never
runs `bin/pi-forks export`, and never edits the shared cache. The fork gate's `fork-build` check builds a clean copy of that exact head with the pack
command, and `fork-quality` runs after a passing `fork-build` in the same clean clone
for forks in `FORK_QUALITY`; only then does the lander move `refs/heads/patch/pi` to the
built head, write its landed marker, and refresh the shared cache's `main`. Items for the same fork are serialized.

## 3. Run — execute a candidate in an isolated lane
`tillr run exec`

`tillr run pause` requests a pause without judging or retrying the attempt.
`tillr run exec` with resume continues that run in the same lane; the adapter declaration
determines whether it reopens the session or starts a fresh one with the prior
transcript and worktree named in its first prompt. The drain continues a paused
run on its next settlement pass and judges only its completed candidate.

When the installed `pi` binary advertises `parked-tool-calls`, the drain runs workers in
`call-suspends` mode with the `land` and `obstacle` extension tools: `land` verifies a clean
worktree ahead of `base_sha` and suspends the tool call, `obstacle` validates structured
blocker fields and suspends, and a gate, land, or revert refusal reopens that session
without a new prompt to deliver the refusal as the suspended call's result. Where
`parked-tool-calls` is absent, workers end with `RESULT:` text footers.

If `pi` aborts a runaway worker for repeating the same tool call (`repeating-call`), the
drain pins uncommitted work, never resumes the looping session, starts a fresh session on
the same backend naming the repeated call, fails over to the seat's next backend on a
second repeat, and holds the item as `repeating-call` once the fallback chain wraps. A
settled harness that lingers past its idle grace is ended as `settled-idle`. Seat policies
route across Copilot, Bedrock, and headless Claude backends, reloading on change without a
loop restart (`tillr drain probe-backend` checks availability).

One candidate, one lane, one record of what the run cost (`tillr-trace` records the
commands a run executes, and `tillr run copied-usage` / `tillr run mark-copied-usage` audit
historical copied usage). `tillr run lanes`, `tillr run status` and `tillr run release`
inspect and clean up the lanes a pass left behind; a lane released is not a candidate
retried.

### Worker verification in this repository

While working on Go code, use a targeted `-run` for the behavior being changed. Run the
full suite once before committing. In the drain module, drop `-count=1` for the working
loop: the tests declare the sibling sources as inputs Go's result cache can see, so a
sibling change reruns tests and an unchanged tree still caches. The drain README explains
the mechanism and names the permanent cache regression check. A worker runs tests, not
the landing gate (`just done`); the gate owns its landing lock.

For the full suite before committing, use the repository's runner on modules the
gate runs through it. The measured module list is in the
[verification report](../../../../docs/pi/tillr-go-shards-2026-11-17.md).
The runner always executes tests fresh; Go's test result cache does not apply.
It builds each package's test binary once and runs up to six independent test
processes per package, balancing recorded durations. Tests without a duration are
spread across shards using the mean recorded cost (or a unit cost when none is
recorded); no duration entry is required to run a test. Each process has its own
scratch directory and git global configuration file. A shard has a 480-second
limit without top-level test starts or completions, including during test setup;
ordinary output does not reset it. On timeout the runner kills its process group,
reports the package, shard, active and unfinished tests, and preserves partial
`--events`. For a genuinely longer test, set `--shard-timeout SECONDS`; `--timeout`
separately controls Go's own test deadline.

```bash
python3 tools/go-test-shards.py tools/tillr-drain -k 6 --scratch "$TMPDIR"
```

Replace the module path for another tillr module and point `--scratch` at the run's
scratch directory. For a focused edit, prefer a targeted test before the full suite:

```bash
cd tools/tillr-drain && go test ./... -run '^TestName$'
# a full run: add -timeout 60m, because Go's default 10m can kill the drain package under load
```

Before committing, run the full suite for the modules the change reaches. Workers
never run `just done`: the landing gate owns that check. In another repository,
use that repository's documented test command, not this repository-specific runner.

## 4. Judge — evaluate the candidate against the gate
`tillr gate run`

A working check keeps its own verdict past its measured target. The gate samples
output and process-group CPU every second; sixty samples with neither is `stalled`,
not pass, fail or a harness error. The first stall retains the candidate as
`gate-stall-rerun`, waiting for fewer gates in flight, or none other, then gates
the same head without another worker or charged attempt. A second stall on the
same head tree holds it as `gate-stalled`, naming both observations. Diagnose it
and use `tillr drain clear-fault`. `gate-harness-error` remains a program that
could not start or crashed. If group CPU cannot be read, `watchdog unavailable`
is reported and no stall is declared.

The landing gate runs the change checks and every fast whole-tree declaration
(`tillr-gate checks` and `tools/gate/checks/`; `tillr-gate change`, `tillr-gate plan`, and
`tillr-gate shadow` run without a store). `just done` runs those fast tree declarations
from the last verified commit through HEAD. Under `after-merge` landing, a background heavy
runner proves the full declaration and legacy suites on a detached trunk checkout, advancing
`refs/tillr/green` (and pushing proved commits when `[push] what = "green"`). With
`auto_revert` enabled, a red heavy check bisects the culprit landing, gates and lands its
inverse (`revert-main-<sha12>`), withdraws the item's verdicts, returns the item to
`in-queue`, and hands the failure back to its worker before filing a deduplicated trunk
fixer when automatic revert is not possible. Legacy rolling watermarks remain only on the
`before-merge` compatibility path.

While a trunk check is red and a fixer is open, non-fixer items still dispatch with a
trunk-state notice in their brief and no landing cap (`red_cap` is retired per
[`OQ-GP7`](../../../../docs/design/tillr-graph-is-the-path.md#OQ-GP7)). When a candidate
fails only checks already red on its base and the trunk tip, the loop compares its changed
paths against each red check's declared `reads`: if they do not intersect, the verdict
promotes to `pass-inherited` and the candidate lands; if they intersect, the candidate is
acquitted without charge (`trunk-already-red`) and blocked behind the fixer (`trunk-broken`
in waiting views). Once the fixer lands and the selected lane base contains the repair — or
once a proof passes the named checks even if the fixer was closed without landing — the
next pass clears the waiter and starts a fresh run from the repaired trunk without an
authorized retry. A check green on the base, or a failure of the candidate's own, keeps the
ordinary refusal.

The verdict about the candidate. `tillr-gate dualrun` runs two gates and compares them,
which is the evidence a disputed verdict is settled with.

## 5. Land — squash the passing candidate onto the trunk
`tillr land pass`

The trunk moves only through this step (`tillr land one` gates and lands a branch in one
call; non-landing charters skip `tillr-land` and finish through their `complete-item`
hook). One landing is one stateless squashed commit for ordinary repository work; a fork
delivery keeps the built fork head and refreshes its shared cache instead. A refused
landing returns to the worker session with the lander's answer, uncounted
(`returned-with-evidence`), except `already-on-trunk` (`resolved-elsewhere`) and boundary or
author holds waiting on the operator. A land that conflicts — `squash-conflict`, because another
landing moved the trunk first — is not a refusal: the loop stages a conflict-fix run for
the same candidate, carrying the conflicting paths and the three-way markers in its brief,
and judges that run like any other change. Conflicts are not counted: each one goes back
to the worker with its paths on the latest trunk, and only a conflict with the same paths
and the same hunks as an earlier one of the item stops that, going to the design seat with
both records. `conflict-exhausted` is history: an item parked under it is returned at
the next loop start. If repair staging
fails, the affected item holds as `conflict-repair-staging-failed` without stopping
siblings. Its original candidate and conflict evidence remain retained across restarts.
Use the waiting view's cause to correct the item, backend policy, brief template, or
build; the next pass retries the retained repair only when those inputs change.
Queue rank, timestamps, and advancing trunk do not authorize that retry.

The owner's uncommitted edits in the main checkout do not delay a landing: the lander
carries them across as `git pull --autostash` does, leaves untouched paths alone, and keeps
a recovery copy under `refs/tillr/owner-recovery/` while it works. The only pause the owner's
tree causes is a predicted conflict (or a merge, rebase or cherry-pick in progress), held as
`trunk-path-dirty` with the conflicting paths; the advice is to commit, restore, or change
the edit, and the landing proceeds as soon as it no longer conflicts. A landing fault names a
path and a `refs/tillr/owner-recovery/` ref that holds bytes the owner saved.

## 6. Verify — check store and invariant integrity
`tillr store check`

Whether the store still satisfies its own invariants (`tillr store inventory` summarizes its
contents read-only, and `tillr store recover` repairs an interrupted writer). Run
`tillr store check` after a pass and before trusting anything `tillr view` says. Its
report's `log` says how large the write-ahead log (SQLite's `state.db-wal`) has grown.

The store's recent work lives in `state.db-wal`, not in `state.db`: never copy, hash or
hand over `state.db` alone. To make that one file current, run `tillr store checkpoint`
with no drain loop running (it refuses while one runs, because the loop checkpoints on
its own). A log file left large by a long run without a loop shrinks only with
`tillr store checkpoint --truncate`, which refuses while any other process has the store
open. Otherwise copy through SQLite (`.backup` or `VACUUM INTO` on a read-only
connection). The supported ways are listed in tillr-store's README, under "Backing up or
copying the store".

## 7. Audit — inspect recorded status and events
`tillr view events`

The record of what happened (`tillr store events` exports raw event envelopes by cursor),
alongside `tillr view status` for current board and green-proof state, `tillr view waiting`
for held items and lanes, `tillr view passes` for per-pass decisions and timings,
`tillr view lanes` and `tillr view running` for active work and leases, `tillr view trace`
and `tillr view perf-log` for run spans and observatory latency logs, and `tillr view serve`
for the read-only web observatory. Where an event was never recorded, the absence is the
finding — never reconstruct history from memory.

## Recovery

Use `tillr drain acquit` when a run's recorded fault was not its doing. It closes
that conviction with the operator's explanation, keeps the evidence, and makes
the retained candidate eligible for examination again. A run with no conviction
is refused; acquittal does not erase a failing gate verdict. For external Git
movements that pause landings until the owner confirms them, use
`tillr drain accept-rewrite`, `tillr drain accept-identity`, or
`tillr drain acknowledge-patch-move`; `tillr drain sweep-refs` prunes retired run refs.

Use `tillr drain examine` to judge one retained candidate through the loop's own
gate, landing and verdict steps. Supply the same step programs and backend policy
as the loop. It refuses a lane that is held: stop its loop first, then examine the
run. Nothing new is dispatched merely to choose the candidate.

Use `tillr drain set-aside` when a committed candidate cannot land and needs
rework. It retains the head at the run's named Git reference, releases the lane,
and holds the original item with the operator's reason. The command prints the
reference. Uncommitted lane work and patch-branch candidates are refused. After
correcting the cause, clear the recorded hold with `tillr drain clear-fault` and
authorize rework through `tillr drain retry`, still under the same item id.
Learn each face's required inputs from its `--help`.

A worker run the gate refused, that committed nothing, that escaped its fence or whose
landing was refused needs no retry: the next pass sends it back to its worker with the
refusal's evidence in the brief (`returned-with-evidence`), every time and uncounted. Only
an identical repeat of an earlier ending (same seat, cause and normalized evidence) goes to
the design seat instead, with both records. An edit to an item's block, fence or policy
re-runs nothing.

Retry a failed item under its own id with `tillr drain retry`. Read the failed
run's record, correct the cause, and authorize the retry through that face; learn
its required inputs from `tillr drain retry --help`. Keep the original item so
its run history and other items' dependencies remain attached to the same id.
Do not file a replacement item to escape a hold or mark the original resolved
elsewhere when its intent has not been satisfied.

An item held `conflict-owner` names the design run whose objection put the question to
the owner: its report named `NEXT-ACTOR: owner`. A report naming no actor, even one
ending `RESULT: blocked`, goes back to the design seat to state it. No count of design runs ever sends a conflict there: a gate refusal, a killed run
or a run that committed nothing goes back to the design seat, and a gated design commit
is the answer whatever it changed. The one exception is an identical repeat: a design
ending with the same cause and evidence as an earlier one of the item (a second run that
names no actor for the same conflict and block, or a second gate refusal with the same
failing checks and output). The loop then holds it `conflict-owner` itself, with a
question in the conflict's text naming both runs. Change what the design seat is given
before answering it. To give the design seat another turn instead of
ruling, name the item and that design run to the same face: `tillr drain retry --item
<id> --run <design run> --by <who> --why <reason>`. The conflict is raised for one more
design run, and the face records who asked and why. A design run the conflict no longer names is refused, naming
the current one. Every retry refusal names the exact command the face would accept, or
says that none exists and who moves, so read the refusal before trying another spelling.

Sync reports `clone-of-held-item` when a new id appends a numeric version suffix
to an unfinished item already in the store. The finding names both ids and the
retry command, and that document writes nothing. Final items and ids already in
the store are not refused by this rule.

## Stop conditions

Stop the loop and read the record, rather than retrying, when:

- the gate answers `4` — it could not judge, and a retry does not make it able to;
- `tillr store check` answers non-zero — nothing built on that store is trustworthy;
- a land refuses — the loop records it and sends the item back to its worker with the
  lander's answer, as it does a gate refusal. Two negative land answers are not stops. A squash
  conflict goes back to the worker with its paths on the latest trunk, uncounted; an
  identical repeat (same paths, same hunks) goes to the design seat. On `already-on-trunk` (the trunk already carries the whole change) the
  loop records the verdict and settles the item as `resolved-elsewhere`. A pending run whose
  item was already landed or closed by hand is not a stop either: the loop settles it as
  `item-already-terminal`, which judges nothing and releases the lane.
