# tillr-loop

`tillr-loop` is the Git-landing definition's **hook program**: one executable that
answers judgments the core cannot make, following the
[hook program contract](../tillr/definition/README.md#hook-program-contract).
It answers `describe`, `gate`, `land`, `verdict`, `fixer-gate`, `replay`, and `rolling`.
It judges nothing itself: candidate steps derive the records the real utilities need
and translate their exit codes; revision checks run the repository's declared whole-tree
checks.

The embedded Git-landing definition keeps `hooks` unset. A store can opt in by setting
its definition's `hooks` to the following object (the path is relative to the repository
where the loop starts):

```json
{"argv":["tillr-loop"],"names":["gate","land","verdict","fixer-gate","replay","rolling"]}
```

With that declaration, the drain selects all six steps without step flags and checks
`describe` before the first pass. An explicit step flag still wins. Without hooks,
`--gate`, `--land`, and `--verdict` must name programs:

```sh
tillr drain loop --root <repo> --lanes 1 \
  --gate    "tillr-loop gate --root <repo>" \
  --land    "tillr-loop land" \
  --verdict "tillr-loop verdict"

tillr-loop self-check \
  --item <id> --lane <id> --run <id> --worktree <lane-worktree> --base <sha> --head <sha>
```

## Revision checks and the fixer gate

`fixer-gate` takes the candidate flags and `--only <check>`. After the ordinary gate
accepts a trunk fixer's diff, the drain calls this face for each named check.
It verifies that the candidate head is checked out (exit 4 when it is not) and runs the
named check in the candidate's worktree.

`replay --only <check>` and `rolling (--all | --since <sha>)` judge the revision checked
out in the caller's working directory. All three faces run declarations from
`[gate] checks_dir` in `.tillr.toml`, defaulting to `tools/gate/checks`
(`revision_check` in the adapter):

| Face | What runs |
|---|---|
| `rolling` | every heavy whole-tree declaration: `tillr-gate checks --heavy-tree --base HEAD --head HEAD --worktree . --checks <checks_dir> --logs <logs>` |
| `replay`, `fixer-gate`, naming a heavy declaration | the same command with `--only <check>` |
| `replay`, `fixer-gate`, naming a fast whole-tree declaration | `tillr-gate tree-check --only <check> --base HEAD --head HEAD --runner <tillr-gate> --checks <checks_dir>` |

A name that is not a whole-tree declaration is refused with exit 4 and runs nothing: a
change check compares two commits, so it has no verdict on a single revision. A missing
`tillr-gate` on PATH is exit 4 too.

Each face replaces itself with the judge, so the judge keeps the caller's working
directory, environment (including `TILLR_GATE_SPANS_OUT`) and process id, and its exit
status reaches the caller untranslated: an unknown result is never turned into a
judgment. The hook name selects the operation; the only selector is `--only`.

In flag mode the drain defaults `--replay` to `tillr-loop replay --only {only}` and
`--fixer-gate` to `tillr-loop fixer-gate`. It runs no rolling program unless `--rolling`
names one, because the declared heavy checks are its after-merge proof.

## Step programs belong to the running build

When a configured command names `tillr-loop`, `builtin:tillr-loop`, or the legacy
`tools/tillr-loop/tillr-loop` path under the loop's `--root`, the drain runs its own
compiled-in Go adapter package ([`loop`](./loop)), not an external executable from the
live working tree. This applies to gate, fixer gate, land and verdict. Startup prints one
line per step identifying the build's compiled-in adapter or an unchanged caller-configured
program. Other programs run exactly as configured; naming a different adapter path does
not select the compiled-in adapter. Definition hooks also run the exact declared
executable, not the build's compiled-in adapter, even when the declaration names
`tillr-loop`.

Because the Go adapter package is compiled directly into `tillr-drain`, a landing that edits
its source does not change a running loop's next step. Rebuild the tillr programs together
and restart the loop to change its standard step programs. Direct invocations of
`tillr-loop` from a shell run the installed or built binary. The underlying repository
checks are not embedded by this mechanism.

After editing `tools/tillr-loop`, mirror the pack and commit both:

```sh
just pack-sync
```

`just tillr-build` compiles the adapter from the same revision as the other Go programs.

The drain supplies its build stamp through `TILLR_LOOP_ADAPTER_BUILD`. If `tillr-item
show` omits a criterion's `claim_class` field, verdict refuses with exit 4, says the
`tillr-item` build is older than the adapter, and names both builds. Revisions identify
builds when available; otherwise paths and SHA-256 digests identify them. A pack launcher
is named as that launcher, without executing it to discover its cached binary. A present
but unknown class is refused with exit 4 as an invalid stored class.

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
`queue-drained` and code 0 instead of waiting.
The default wait prints the held count and at most five items with their reasons,
then stays quiet until something changes.

Between those stops sits one more case: **the machinery is broken and the work is
fine**. A staging step that cannot render a brief, an adapter that refuses before
running: the loop reports the failure, waits, and tries the same plan again, because the
pile still holds the item and the fault is not the item's. The wait doubles from
`--backoff-base` (default 1s) up to 5 minutes and resets as soon as a candidate is worked.
It is conditional: it applies only to a pass that **dispatched nothing** and
failed in the infrastructure class, so it never rations work the loop could be doing. An
operator stop interrupts it rather than sitting out the wait.

What the loop does instead of a budget is **measure**. Every run's record carries the tokens
and cost its backend's extractor reported (`tools/tillr-run/backends`), the loop prints one
line per run and a total at the end, and a run whose report carried no number is named
rather than counted as free. Measurement reports what the work cost; it never refuses to spend it.

A red gate, a refused fence, or a landing that did not take records a run-level
`drain.refusal`, not an `item.verdict`. Criteria are unchanged and unjudged text stays
editable. An ordinary refused landing is held as `land-refused`, while a squash conflict
gets a conflict-fix run and, when that is exhausted, the `conflict-exhausted` hold.
Recorded `not-done` criteria remain unchanged. See [the landing refusal contract](../../docs/design/tillr-judge-and-lifecycle.md#prevent-a-failed-landing-from-freezing-the-items-criteria).

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
| `gate` | `tillr-gate change --item I --lane L --run U --worktree W --base B --head H --now T --output <run>/gate.change.json`, then `tillr-gate plan --registry R --input gate.change.json --output gate.plan.json --root <worktree>`, then `tillr-gate run --input gate.plan.json --worktree <worktree> --output gate.verdict.json --logs <run>/logs --gate <tillr-gate> --gate-arg change-gate --gate-arg --base --gate-arg <base> --gate-arg --head --gate-arg <head> --gate-arg --runner --gate-arg <tillr-gate> --gate-arg --logs --gate-arg <run>/change-logs` (`--gate-program P` replaces the program's words) | the candidate's argv for `tillr-gate change`, which derives the `gate.change` record itself: base, `head^{tree}` and `git diff --name-only base...head`, all read in the candidate worktree. Which checks the change owes stays `plan`'s decision. The registry `R` is `--registry`, else `$TILLR_LOOP_REGISTRY`, else the candidate worktree's `[gate] checks_dir` from `.tillr.toml` (defaulting to `tools/gate/checks`), so a lane that adds or changes a declaration is judged by it. |
| `land` | `tillr-land pass --store S --repo R --item I --run U --base B --head H --verdict <run>/gate.verdict.json --base-ref <branch> --now T --run-dir <run> [--message-from M] [--observed-trunk O]` | `--observed-trunk` is the loop's last observed trunk tip, passed through unchanged: the lander refuses with `trunk-rewritten` under the land lock if the trunk no longer contains it, and records it. `--repo` = root, `--store` = `<root>/.tillr/state.db`, `--base-ref` = the branch the root is checked out on. There is no queue record and no derived candidate id: the gate's verdict is the only input, and the candidate's own four fields name the landing. |
| `verdict` | `tillr-item show --store S --item <id> --root R` to read the frozen criteria, then one `tillr-item verdict record --store S --input <record> --now T` per criterion | one `item.verdict` per criterion whose stored `claim_class` is `verification-only`: the store's frozen text hashed, `done` on `--verdict pass` after a landing, `actor_kind: mechanical`, and the gate verdict, run and head as its evidence. A criterion of any other class is left for a non-mechanical verdict. |

A candidate's terminal outcome reaches the store through a fourth step this directory does
**not** supply: after the verdict, the loop runs `tillr-item state --store S --item <id>
--state landed --now T` (`--item-bin`, default `tillr-item`). A landed candidate's
item becomes `landed`; a failed attempt leaves the item's state and criteria alone.
The next pass holds it under the refusal step's reason. See
[the item-states reference](../../docs/reference/tillr-item-states.md).
It is bookkeeping rather than judgment, which is why it is a
sibling of `run exec` and not a fourth adapter face: the verdict retires criteria, and this
says where the item's work stands.

The default judgment is `tillr-gate change-gate`, run by
`tillr-gate run --gate`. It runs `tillr-gate checks --fast-tree` once with the candidate's
base and head: the declared change checks and the fast whole-tree checks, scoped to the
change when the runner supports `--scope-to-change`. The base must be an ancestor of the
head, and the head must be the commit checked out in the worktree; otherwise the judge
answers exit 4. An unavailable check answers "could not judge," not "candidate rejected."

`tillr-gate` keeps the outer plan and verdict record because `tillr-land pass` binds that
record to the candidate tree. Both `change-gate` and `tree-check` run directly from the
resolved `tillr-gate` binary, never from the candidate worktree's `tools/gate/`.
Change-check logs live in `<run>/change-logs`, beside the run's other records.

`--root` defaults to the checkout the adapter runs in (`git rev-parse --show-toplevel`),
`--store` to `<root>/.tillr/state.db`, `--work-dir` to `<root>/.tillr/loop`, `--now` to the
wall clock. The per-run records land in `<work-dir>/<run>/`, which is the loop's scratch,
never the candidate worktree: a file there would be untracked work the candidate's own
`git add` could commit.

## Exit codes

The loop's contract is the whole job, so each adapter translates rather than passing a
code through. `tillr-gate run` answers 0/1/4/6 and `tillr-land pass` answers
0/1, but both also answer 2/3/5 for a plan, store or invocation they could not use, and that is
not a verdict about the candidate.

| Adapter | Underlying | Adapter answers | The loop does |
|---|---|---|---|
| `gate` | `tillr-gate run`: 0 pass, 1 fail, 6 stalled, 2/3/4/5 no verdict | 0, 1, 4, or 6 | 0 → `pass`; 1 → `fail`; 6 → `stalled` (retained as `gate-stall-rerun`, or second stall `gate-stalled`); 4 → `error`, retained as `gate-no-verdict` and gated again on the next pass |
| `land` | `tillr-land pass`: 0 landed, 1 not landed, 2/3/4/5 no attempt | 0, 1, or 4 | 0 → `landed`; 1 → `refused` (isolated/declined candidate, a completed negative answer); 4 → step failure, halt |
| `verdict` | `tillr-item verdict record`: 0 recorded, non-zero refused | 0, or the refused code | 0 → recorded; non-zero → step failure, halt (a landed candidate whose item was not retired is landed work the board cannot see) |
| item state | `tillr-item state`: 0 moved or already there, 1 refused, 2/3/5 no answer | the code itself | 0 → the item moved; non-zero → step failure, halt (a landed candidate whose item is still queued is the store disagreeing with the trunk) |

The gate's `plan` step is a preparation, not a judgment: a failure there means no verdict
exists, so it also leaves as 4. The land face has no preparation at all -- the gate's verdict
is its only input, and a missing one is refused before anything runs.

## What it refuses, and why

Every refusal is exit 2 (bad argv, a value that cannot be derived) or exit 4 (the judgment
cannot be made). Never 0 and never 1: a candidate nobody judged is not a candidate that
failed, and a wrapper that turned a broken judge into a pass is how a loop's gate quietly
becomes `true`.

- **No root** — no `--root` and a working directory that is not a checkout.
- **A plan that cannot be made** (gate) — a gate with no registry would pass everything,
  so with none named (`--registry`, else `$TILLR_LOOP_REGISTRY`) the plan reads the
  candidate's `[gate] checks_dir` declarations from `.tillr.toml` (default `tools/gate/checks`). A `tillr-gate change` or `plan` that
  fails is exit 4: no verdict, never a pass, and the drain gates the same candidate again.
- **A revision check that cannot judge** (fixer-gate, replay) — a head that is not checked
  out, or a name that is not a whole-tree declaration: exit 4.
- **No gate verdict** (land) — a landing against no verdict record.
- **Detached root** (land) — no branch to land onto, and `--base-ref` was not named.
- **No criteria** (verdict) — an item whose store holds no criterion retires nothing.
- **A missing or non-runnable program** — `tillr-gate`, `tillr-land`, `tillr-item` or `git`
  not on PATH: exit 4.

## The self-check

`tillr-loop self-check` composes one candidate's steps and prints them without running anything: the
candidate it was given, then for each of the three faces the program string the loop would
be handed and the argv that face would pass, with the working directory, then the
`tillr drain loop` command that wires the three together. A face that would refuse prints
`REFUSED (exit N): <reason>` instead of its argv and the self-check exits 4, so an operator
sees the refusal before starting the loop.

## A trunk fixer's gate

The drain uses `--gate` for a trunk fixer just as for any candidate. Only after that gate
accepts the change does it run the checks named in the fixer's stored criterion. It
returns a named failure to the worker and holds a candidate whose named check could not
judge. A fixer-gate program that exits 77 has dropped the named check, which the drain
reads as neither a pass nor a failure. With no names, the ordinary gate is enough.
The drain records the combined judgment before it hands the verdict to the lander.

## Test

`just go-test-shards tools/tillr-loop` (or `cd tools/tillr-loop && go test ./...`) runs the
Go suite for the three adapters against stub stand-ins for the real programs (a temp directory
on PATH, one per face, each answering a chosen exit code and logging its argv). It pins the
gate and land and verdict mappings above, a missing
judge as exit 4, a change that cannot be derived as exit 4, the candidate argv the gate
hands `tillr-gate change` and that the adapter writes no change record of its own, the exact
argv the gate hands `tillr-gate run` (the `--gate`/`--gate-arg` program and `--logs`) and
the program's own argv as the stand-in gate executes it, the `--registry` the plan reads
(the candidate's declarations directory when none is named), the commands the revision
checks run, and that the self-check names the composed argv — including the derived
verdict path, the registry and the candidate head — while invoking no stub at all. No test
runs a real gate, a real store or a model.

[drain-argv.json](./drain-argv.json) is every argument list `tillr drain loop` sends these faces, one per
optional branch: the ordinary gate, the fixer's named check, the land step with each of its
optional flags, and the verdict. The drain's own suite writes and checks it from its
`ProcessSteps` code paths, with the real adapter as the program
(`TestEveryArgvTheDrainSendsIsAcceptedByTheTillrLoopAdapter` in
`tools/tillr-drain/drain/step_argv_contract_test.go`; regenerate with
`TILLR_UPDATE_DRAIN_ARGV=1`). This suite replays each list against the face it reaches. A
flag added to the drain fails the drain's suite until this adapter accepts it and the file is
regenerated, and a flag this adapter stops accepting fails this suite.

It runs every case and reports all failures before exiting non-zero, so one stale assertion
cannot hide the rest. The gate runs it too, as the `go-test-tillr-loop` check
([`tools/gate/checks/go-test-tillr-loop.json`](../gate/checks/go-test-tillr-loop.json)): a
heavy whole-tree declaration whose reads include this directory, so `just done` and the
drain's after-merge proof run it, and a case that stops describing the adapter is caught
without anyone running the suite by hand.
