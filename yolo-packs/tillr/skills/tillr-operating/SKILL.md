---
name: tillr-operating
description: Compose the eight `tillr*` binaries across the seven-step loop — plan, stage, run, judge, land, verify, audit — in a repository that has a tillr store. Covers the order, the one command per step, and which exit code means what; every face's flags are learned from the binary, not from this skill.
---

# Operating the tillr loop

> [!WARNING]
> **Known Gaps (verified 2026-11-12 at `32aed3dc31`).** `tillr` has landed a live
> candidate on an unattended tick, but three parts are unfinished:
> 1. **Item-state moves just landed** — queue item-state transitions are newly wired and have minimal production mileage.
> 2. **Two-item seed** — the seed currently holds only two dispatchable items.
> 3. **Recording emitters unbuilt** — the recording/event schema exists in `tools/tillr`, but the binaries' event emitters are not yet wired (`events` faces see only what is currently emitted).
>
> **Do not wire `tillr` into another repository's gate while the emitters are unbuilt.**
> The toolset ships early so it can be reviewed, not so it can be depended on.

`tillr` is eight binaries that act on one store. `tillr` is the umbrella dispatcher; the
seven `tillr-<noun>` binaries are the tools it dispatches to, and each also runs on its
own. **Learn a face's flags from the binary**: a face invoked without its required flags
names them in its refusal, and the flags are the tool's own — this skill never restates
one.

Every face takes the store it acts on, and a face invoked without one says so. `tillr store
init` creates a store; nothing here initializes a repository on your behalf, and no binary
does it implicitly.

`tillr drain loop` is a daemon by default: it keeps running when nothing is
dispatchable, waiting for a new item, a changed hold, or a trunk move. Its waiting
line names the held count and the first five items with their reasons, then stays
quiet until something changes. SIGINT or SIGTERM stops it with exit code 0 and a
final line saying it was stopped by the operator. The runs are left alone on a
signal, a fault, or any other ordinary exit. A restarted loop settles and judges
those runs once, including ones that finished while it was absent.

Use the stop face for an explicit operator request:

```sh
tillr drain stop --root <repository>
tillr drain stop --root <repository> --end-runs
```

Without the flag, stop ends supervision only. With it, the drain records the
interruption before sending SIGTERM to each in-flight run face. It does not ask the
runs to pause, so those items start over on the next pass without
spending a retry, failover, or automatic re-admission allowance. Repeated stops
need no authorization.

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

## Keep the repository loop running

In this repository, `just tillr-keep` runs the **keeper**: the process that starts
an absent loop, waits for its exit, and starts it again. It does not authorize or
retry an item. It composes the repository's gate, land, and verdict adapters and
leaves the loop's after-merge check enabled. Learn its options from
`just tillr-keep --help`; additional loop options pass through after `--`.

The keeper runs in the foreground. On Linux it finds a loop by its own process
name, `tillr-drain` (including the pack's source-digest suffix), owned by the same
user and running in the same checkout.
It never searches a command line. More than one matching process is a refusal,
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

`1` and `4` are the pair that matters unattended: `1` is a verdict about the candidate,
`4` is the tool saying it could not judge. Retrying a `1` is not a fix.

## 1. Plan — preview dispatchable work
`tillr drain plan`

What the loop would dispatch, and why each item is or is not dispatchable. Read it before
starting a pass: `1` here means there is nothing to dispatch, which is an answer.

## 2. Stage — sync item state from the documents
`tillr item sync`

The item state a pass will act on, read from the item blocks in the repository's tracked
documents: sync is how an item enters a store from a document, and `tillr drain loop` runs it
before its first plan. A second run over unchanged documents writes nothing. `1` names a
block with a finding, and every other document was still written. `tillr item` also carries
the apply, decision, mail, grant and verdict faces; staging is what the loop needs before it
runs anything.

## 3. Run — execute a candidate in an isolated lane
`tillr run exec`

`run pause --run <id>` requests a pause without judging or retrying the attempt.
`run exec --resume` continues that run in the same lane; the adapter declaration
determines whether it reopens the session or starts a fresh one with the prior
transcript and worktree named in its first prompt. The drain continues a paused
run on its next settlement pass and judges only its completed candidate.

One candidate, one lane, one record of what the run cost. `tillr run lanes`, `status` and
`release` inspect and clean up the lanes a pass left behind; a lane released is not a
candidate retried.

## 4. Judge — evaluate the candidate against the gate
`tillr gate run`

The verdict about the candidate. `tillr-gate dualrun` runs two gates and compares them,
which is the evidence a disputed verdict is settled with.

## 5. Land — squash the passing candidate onto the trunk
`tillr land pass`

The trunk moves only through this step, and only for a candidate that passed. One landing
is one stateless squashed commit. A land that refuses records the item's verdict and is
not retried by the loop. A land that conflicts — `squash-conflict`, because another
landing moved the trunk first — is not a refusal: the loop stages one conflict-fix run for
the same candidate, carrying the conflicting paths and the three-way markers in its brief,
and judges that run like any other change. A second conflict on the same candidate holds
the item as `conflict-exhausted` instead of spending another run. If repair staging
fails, the affected item holds as `conflict-repair-staging-failed` without stopping
siblings. Its original candidate and conflict evidence remain retained across restarts.
Use the waiting view's cause to correct the item, backend policy, brief template, or
build; the next pass retries the retained repair only when those inputs change.
Queue rank, timestamps, and advancing trunk do not authorize that retry.

## 6. Verify — check store and invariant integrity
`tillr store check`

Whether the store still satisfies its own invariants. Run it after a pass and before
trusting anything `tillr view` says.

## 7. Audit — inspect recorded status and events
`tillr view events`

The record of what happened, and `tillr view status` for where the store stands now.
Where a record was never emitted (see Known Gaps), the absence is the finding — not a
reason to reconstruct the history from memory.

## Recovery

Retry a failed item under its own id with `tillr drain retry`. Read the failed
run's record, correct the cause, and authorize the retry through that face; learn
its required inputs from `tillr drain retry --help`. Keep the original item so
its run history and other items' dependencies remain attached to the same id.
Do not file a replacement item to escape a hold or mark the original resolved
elsewhere when its intent has not been satisfied.

Sync reports `clone-of-held-item` when a new id appends a numeric version suffix
to an unfinished item already in the store. The finding names both ids and the
retry command, and that document writes nothing. Final items and ids already in
the store are not refused by this rule.

## Stop conditions

Stop the loop and read the record, rather than retrying, when:

- the gate answers `4` — it could not judge, and a retry does not make it able to;
- `tillr store check` answers non-zero — nothing built on that store is trustworthy;
- a land refuses — the loop records it and moves on; a second attempt is a decision for
  a human, not for the loop. A squash conflict is the one negative land answer that is not
  a stop: the loop spends its one conflict-fix run and holds the item only if that run
  conflicts too.
