# tillr-loop

`tillr-loop` is the Git-landing definition's **hook program**: one executable that
answers judgments the core cannot make, following the
[hook program contract](../tillr/definition/README.md#hook-program-contract).
It answers `describe`, `gate`, `land`, `verdict`, `fixer-gate`, `replay`, and `rolling`.
It judges nothing itself: candidate steps derive the records the real utilities need
and translate their exit codes; revision checks run the repository's existing checks.

The embedded Git-landing definition keeps `hooks` unset. A store can opt in by setting
its definition's `hooks` to the following object (the path is relative to the repository
where the loop starts):

```json
{"argv":["python3","tools/tillr-loop/tillr-loop"],"names":["gate","land","verdict","fixer-gate","replay","rolling"]}
```

With that declaration, the drain selects all six steps without step flags and checks
`describe` before the first pass. An explicit step flag still wins. Without hooks,
`--gate`, `--land`, and `--verdict` must name programs as before; the keeper's existing
command is unchanged.

```sh
tillr drain loop --root /workspace --lanes 1 \
  --gate    "tools/tillr-loop/tillr-loop gate --root /workspace" \
  --land    "tools/tillr-loop/tillr-loop land" \
  --verdict "tools/tillr-loop/tillr-loop verdict"

tools/tillr-loop/tillr-loop self-check \
  --item <id> --lane <id> --run <id> --worktree <lane-worktree> --base <sha> --head <sha>
```

## Revision checks and the fixer gate

`fixer-gate` uses the gate adapter with a required full-check range: either `--all`
or `--since` the last proved commit. The legacy `gate` face still accepts these flags,
so the drain's existing fixer-gate default keeps working.

`replay` runs `python3 tools/fork-check.py --replay --only <check>`; `rolling` runs
`python3 tools/fork-check.py --tier rolling` with either `--all` or `--since <sha>`.
These are the programs the drain defaults to in flag mode. Both hooks retain the
caller's working directory and environment, including `PI_FORKS_ROOT` and
`TILLR_GATE_SPANS_OUT`, and return the check program's exit status without translating
an unknown result into a judgment. The hook name selects the operation, so callers
do not pass `--replay` or `--tier` to these faces.

## Step programs belong to the running build

When a configured command names this repository's standard adapter under the loop's
`--root`, the drain runs its own compiled-in copy, not the live working-tree file. This
applies to gate, fixer gate, land and verdict, including the keeper's `python3` commands
and direct script commands. Startup prints one line per step identifying the build's
embedded adapter or an unchanged caller-configured program. Other scripts run exactly
as configured; naming a different adapter path does not select the embedded copy.
Definition hooks also run the exact declared executable, not the build's embedded
adapter, even when the declaration names the standard adapter path.

The copy is committed as [tillr-loop.py](../tillr-drain/drain/tillr-loop.py) and embedded
in the drain binary. A landing that edits the source therefore does not change a running
loop's next step. Rebuild the tillr programs together and restart the loop to change its
standard step programs. Direct invocations from a shell still read the file named on
that command line. The underlying repository checks are not embedded by this mechanism.

After editing the adapter, regenerate its copy, then mirror the pack and commit both:

```sh
python3 tools/tillr-loop/sync-adapter.py
just pack-sync
```

Both the drain suite and this adapter's suite refuse drift between source and copy.
The green-commit build recipe compiles the committed copy from the same revision as its
Go programs; it does not regenerate from the current checkout.

The drain supplies its build stamp through `TILLR_LOOP_ADAPTER_BUILD`. If `tillr-item
show` omits a criterion's `claim_class` field, verdict refuses with exit 4, says the
`tillr-item` build is older than the adapter, and names both builds. Revisions identify
builds when available; otherwise paths and SHA-256 digests identify them. A pack launcher
is named as that launcher, without executing it to discover its cached binary. A present
but unknown class still receives the existing invalid-stored-class refusal.

## No budget: the pile is the only throttle

`tillr drain loop` has no tick budget, no dollar ceiling and no token ceiling. That is a
ruling, not a default: [the momentum principle](../../docs/design/momentum-principle.md).
The loop is a daemon by default: it runs while the pile holds work it may dispatch and
waits on its triggers when nothing is dispatchable and no run is in flight. A new item,
a changed hold, or a trunk move wakes it without a restart. It stops when an operator
sends SIGINT or SIGTERM (`operator-stop`, exit code 0), or on a fault it cannot route
around (`host-boundary-violation`, `gate-harness-error`). If work should not be done,
it does not go in the pile; if it is in the pile, the loop's job is to finish it.

Use `--exit-when-drained` only when the caller wants the loop to exit with
`queue-drained` and code 0 instead of waiting. The former `--stay` flag is removed.
The default wait prints the held count and at most five items with their reasons,
then stays quiet until something changes.

Between those stops sits the case that actually happens — **the machinery is broken and the
work is fine**. A staging step that cannot render a brief, an adapter that refuses before
running: the loop reports the failure, waits, and tries the same plan again, because the
pile still holds the item and the fault is not the item's. The wait doubles from
`--backoff-base` (default 1s) up to 5 minutes and resets as soon as a candidate is worked.
It is deliberately conditional: it applies only to a tick that **dispatched nothing** and
failed in the infrastructure class, so it never rations work the loop could be doing. An
operator stop interrupts it rather than sitting out the wait.

What the loop does instead of a budget is **measure**. Every run's record carries the tokens
and cost its backend's extractor reported (`tools/tillr-run/backends`), the loop prints one
line per run and a total at the end, and a run whose report carried no number is named
rather than counted as free. Measurement is introspection — what a night cost — and it is a
different thing from refusing to spend it.

A red gate, a refused fence, or a landing that did not take records a run-level
`drain.refusal`, not an `item.verdict`. Criteria are unchanged and unjudged text stays
editable. An ordinary refused landing is held as `land-refused`, while a squash conflict
keeps its existing conflict-fix run and `conflict-exhausted` hold. Earlier `not-done`
criteria remain unchanged. See [the landing refusal contract](../../docs/design/tillr-judge-and-lifecycle.md#prevent-a-failed-landing-from-freezing-the-items-criteria).

The loop appends the candidate to whatever program string it is given — one flag per
field, `--item --lane --run --worktree --base --head --root` — and then the flags each
step's branches add: `--observed-trunk`, `--base-ref` and `--message-from` on the land
step, `--store` and `--verdict pass` on the verdict step after a landing. `--verdict fail` is a usage error naming that contract, not a way to grade
an unsuccessful attempt. Every flag in the string above is the adapter's own and the
candidate arrives after it.

The adapter also supports startup argument validation under the drain's
[step-program argument contract](../tillr-drain/README.md#step-program-argument-contract).
That mode is separate from `self-check`, which composes and displays real invocations.

## The three adapters

| Face | Real invocation | What it derives |
|---|---|---|
| `gate` | `tillr-gate change --item I --lane L --run U --worktree W --base B --head H --now T --output <run>/gate.change.json`, then `sh -c <registry export> > <run>/registry.json` when no registry is named, then `tillr-gate plan --registry R --input gate.change.json --output gate.plan.json --root <worktree>`, then `PI_FORKS_ROOT=<root> tillr-gate run --input gate.plan.json --worktree <worktree> --output gate.verdict.json --logs <run>/logs --gate python3 --gate-arg tools/gate/change-gate.py --gate-arg --base --gate-arg <base> --gate-arg --head --gate-arg <head> --gate-arg --runner --gate-arg tillr-gate --gate-arg --logs --gate-arg <run>/change-logs` (`--gate-program P` replaces the program's words; `--since S` or `--all`, which a trunk fixer's gate adds, are handed to that program so its remaining checks run untiered over the fixer's range) | the candidate's argv for `tillr-gate change`, which derives the `gate.change` record itself: base, `head^{tree}` and `git diff --name-only base...head`, all read in the candidate worktree. Which checks the change owes stays `plan`'s decision. The export runs `python3 tools/fork-check.py --registry-json` with the root as its working directory, so its path is relative for the same reason the gate program's is. |
| `land` | `tillr-land pass --store S --repo R --item I --run U --base B --head H --verdict <run>/gate.verdict.json --base-ref <branch> --now T --run-dir <run> [--message-from M] [--observed-trunk O]` | `--observed-trunk` is the loop's last observed trunk tip, passed through unchanged: the lander refuses with `trunk-rewritten` under the land lock if the trunk no longer contains it, and records it. `--repo` = root, `--store` = `<root>/.tillr/state.db`, `--base-ref` = the branch the root is checked out on. There is no queue record and no derived candidate id: the gate's verdict is the only input, and the candidate's own four fields name the landing. |
| `verdict` | `tillr-item show --store S --item <id> --root R` to read the frozen criteria, then one `tillr-item verdict record --store S --input <record> --now T` per criterion | one `item.verdict` per criterion: the store's frozen text hashed, `done` on `--verdict pass` after a landing, `actor_kind: mechanical`, and the gate verdict, run and head as its evidence |

A candidate's terminal outcome reaches the board through a fourth step this directory does
**not** supply: after the verdict, the loop runs `tillr-item state --store S --item <id>
--state landed --now T` (`--item-bin`, default `tillr-item`). A landed candidate's
item becomes `landed`; a failed attempt leaves the item's state and criteria alone.
The next pass holds it under the refusal step's reason. See
[board-item-state-model.md](../../docs/design/board-item-state-model.md)
[Section 2](../../docs/design/board-item-state-model.md#2-classification-of-every-status-the-board-can-show).
It is bookkeeping rather than judgment, which is why it is a
sibling of `run exec` and not a fourth adapter face: the verdict retires criteria, and this
says where the item's work stands.

The default judgment is the [two-program judge](../gate/change-gate.py), run by
`tillr-gate run --gate`. It runs `tillr-gate checks` with the candidate’s base and head,
then the remaining repository checks. The change declarations never read a remembered
baseline; the base must be an ancestor of the head. A rejection stops the judge, and an
unavailable check answers “could not judge,” not “candidate rejected.”

`tillr-gate` keeps the outer plan and verdict record because `tillr-land pass` binds that
record to the candidate tree. The programs are spelled **relative** so they resolve inside
the candidate worktree. The remaining repository checks still use the candidate’s range,
run the landing tier, and wait for the record-save lock rather than failing on the first
concurrent gate. Change-check logs live beside the run’s other records.
`--root` names the repository the registry is exported from and the one `PI_FORKS_ROOT`
names; pass the main checkout there when a check needs the shared fork checkouts.

`--root` defaults to the checkout the adapter runs in (`git rev-parse --show-toplevel`),
`--store` to `<root>/.tillr/state.db`, `--work-dir` to `<root>/.tillr/loop`, `--now` to the
wall clock. The per-tick records land in `<work-dir>/<run>/`, which is the loop's scratch,
never the candidate worktree: a file there would be untracked work the candidate's own
`git add` could commit.

## Exit codes

The loop's contract is the whole job, so each adapter translates rather than passing a
code through. `tillr-gate run` already answers 0/1/4 and `tillr land pass` already answers
0/1, but both answer 2/3/5 for a plan, store or invocation they could not use, and that is
not a verdict about the candidate.

| Adapter | Underlying | Adapter answers | The loop does |
|---|---|---|---|
| `gate` | `tillr-gate run`: 0 pass, 1 fail, 6 stalled, 2/3/4/5 no verdict | 0, 1, 4, or 6 | 0 → `pass`; 1 → `fail`; 6 → `stalled` (retained as `gate-stall-rerun`, or second stall `gate-stalled`); 4 → `error`, retained as `gate-no-verdict` and gated again on the next pass |
| `land` | `tillr-land pass`: 0 landed, 1 not landed, 2/3/4/5 no attempt | 0, 1, or 4 | 0 → `landed`; 1 → `refused` (isolated/declined candidate, a completed negative answer); 4 → step failure, halt |
| `verdict` | `tillr-item verdict record`: 0 recorded, non-zero refused | 0, or the refused code | 0 → recorded; non-zero → step failure, halt (a landed candidate whose item was not retired is landed work the board cannot see) |
| item state | `tillr-item state`: 0 moved or already there, 1 refused, 2/3/5 no answer | the code itself | 0 → the board moved; non-zero → step failure, halt (a landed candidate whose item is still queued is the board disagreeing with the trunk) |

The gate's `plan` step is a preparation, not a judgment: a failure there means no verdict
exists, so it also leaves as 4. The land face has no preparation at all -- the gate's verdict
is its only input, and a missing one is refused before anything runs.

## What it refuses, and why

Every refusal is exit 2 (bad argv, a value that cannot be derived) or exit 4 (the judgment
cannot be made). Never 0 and never 1: a candidate nobody judged is not a candidate that
failed, and a wrapper that turned a broken judge into a pass is how a loop's gate quietly
becomes `true`.

- **No root** — no `--root` and a working directory that is not a checkout.
- **A registry that cannot be exported** (gate) — a gate with no registry would pass
  everything, so with none named (`--registry`, else `$TILLR_LOOP_REGISTRY`) the adapter
  exports one from the tree's own `tools/fork-check.py`. A tree where that cannot run is exit
  4: no verdict, never a pass, and the drain gates the same candidate again.
- **No gate verdict** (land) — a landing against no verdict record.
- **Detached root** (land) — no branch to land onto, and `--base-ref` was not named.
- **No criteria** (verdict) — an item whose store holds no criterion retires nothing.
- **A missing or non-runnable program** — `tillr-gate`, `tillr-land`, `tillr-item` or `git`
  not on PATH: exit 4.

## The self-check

`tillr-loop self-check` composes one tick and prints it without running anything: the
candidate it was given, then for each of the three faces the program string the loop would
be handed and the argv that face would pass, with the working directory, then the
`tillr drain loop` command that wires the three together. A face that would refuse prints
`REFUSED (exit N): <reason>` instead of its argv and the self-check exits 4, so an operator
sees the refusal before spending a night on ticks rather than after one.

## A trunk fixer's gate

A trunk fixer, the item the loop files when the rolling tier finds the trunk red, is gated
by the loop's `--fixer-gate` program instead of `--gate`. Its default is this adapter's
gate face with `{green}` expanded: `tools/tillr-loop/tillr-loop gate --since <last green>`,
or `--all` when no rolling run has proved a trunk commit. The loop appends the candidate's
flags to it as it does to `--gate`. The land step after it reads the `gate.verdict` record
that only `tillr-gate` writes, which is why the default is this face and not
`tools/fork-check.py` on its own.

## Test

`python3 tools/tillr-loop/test.py` runs the three adapters against stub stand-ins for the
real programs (a temp directory on PATH, one per face, each answering a chosen exit code
and logging its argv). It pins the gate and land and verdict mappings above, a missing
judge as exit 4, a registry that cannot be exported as exit 4, the candidate argv the gate
hands `tillr-gate change` and that the adapter writes no change record of its own, the exact
argv the gate hands
`tillr-gate run` (the `--gate`/`--gate-arg` program, its `--since` range, `--logs` and
`PI_FORKS_ROOT`) and the program's own argv as the stand-in gate executes it, the `--registry`
the plan reads after an export, and that the self-check names the composed argv — including
the derived verdict path, the generated registry and the candidate head — while invoking no
stub at all. No test runs a real gate, a real store or a model.

[drain-argv.json](./drain-argv.json) is every argument list `tillr drain loop` sends these faces, one per
optional branch: the ordinary gate, the fixer's two ranges, the land step with each of its
optional flags, and the verdict. The drain's own suite writes and checks it from its
`ProcessSteps` code paths, with the real adapter as the program
(`TestEveryArgvTheDrainSendsIsAcceptedByTheTillrLoopAdapter` in
`tools/tillr-drain/drain/step_argv_contract_test.go`; regenerate with
`TILLR_UPDATE_DRAIN_ARGV=1`). This suite replays each list against the face it reaches. A
flag added to the drain fails the drain's suite until this adapter accepts it and the file is
regenerated, and a flag this adapter stops accepting fails this suite.

It runs every case and reports all failures before exiting non-zero, so one stale assertion
cannot hide the rest. The gate runs it too, as the `tillr-loop` check: a change to this
directory plans the suite at the landing tier, so a case that no longer describes the adapter
fails the change that broke it instead of waiting for someone to run it by hand.
