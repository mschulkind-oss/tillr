# tillr-gate — the verdict about a candidate

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

`tillr-gate` derives a change record from a candidate, plans it into the checks a gate would
run, runs them, selects and runs affected Go test packages (`go-affected`), and measures the
landing tier in shadow. `change`, `plan`, `go-affected` and `shadow` need no store and no
other utility's binary, which is what lets an external reviewer adopt them on their own.

The design that splits this surface into `--help`, this README and the composition
skill is [`docs/reference/tillr-distribution.md`](../../docs/reference/tillr-distribution.md).

## State invariants

- **The gate's exit is the verdict.** Pass, fail, or an answer that says it could not judge:
  a gate that could not run is never a fail, because a broken judge and a red tree are
  different facts.
- **The plan is the authority for what ran.** A check that was not in the plan did not run,
  and the record names the difference rather than leaving it to a log.
- **`shadow` measures and never lands.** A shadow red is a measurement, not a verdict about
  the tree, and the face says so in the record it writes.
- **A refused judgment is a distinct answer.** "The harness could not judge" is not "the
  code is bad", and collapsing the two is how a red gate stops being believed.

## Standalone invocation

```sh
tillr-gate run
tillr-gate shadow
tillr-gate go-affected
```

`run` is the verdict a landing needs; `shadow` is the measurement an operator takes before
changing the tier. Both answer in a record on stdout when no output path is named.

## Declaration directories

`plan --registry` accepts either a generated registry file or a directory whose direct
JSON files each declare one check. Programs and tests can live beside those files; only
filenames ending in `.json` are declarations. Adding a check adds its files without
editing a shared list. `checks`, `classify`, [`change-gate.py`](./change-gate.py), and [`tree-check.py`](./tree-check.py) read
the directory from `[gate] checks_dir` in `.tillr.toml` (defaulting to
`tools/gate/checks`, overridden by `--checks` when supplied). The complete field
contract and defaults are in
[the gate reference](../../docs/reference/gate.md#a-check-is-one-declaration-file).

```sh
tillr-gate plan --registry tools/gate/checks/ --input gate.change.json
```

The loader validates every declaration before emitting a plan. A missing scope, tier,
or tier-specific cost names the file and field and answers “could not judge” (exit 4);
it never emits a partial plan. The directory's filenames and bytes determine its digest,
so the same directory content copied elsewhere produces the same plan identity.

A change check receives the base and head as its final two command arguments. Such a
check needs a change record carrying `head_sha`, produced by `tillr-gate change`; a
record without that field is refused rather than substituting a tree hash for a
commit. A tree check is selected by its declared read paths (directory paths end in `/`),
with omitted or empty `reads` selecting the whole tree. A heavy `timeout_ms` is a
measured target, not a kill deadline. Generated declarations follow the same rule.

[The budget declaration](../../tools/gate/checks/gate-budget.json) runs arithmetic admission:
fast budgets may total at most 9,000 milliseconds. Its
[fixture test declaration](../../tools/gate/checks/gate-budget-test.json) reads only the check
directory. The admission check's own budget counts toward the total.
Fast declarations may not name `timeout_ms`; `budget_ms` is their only target.

Checks keep their own verdict past their target. `budget_ms`, duration and
`over_budget_ms` record each overrun in the verdict, log and summary. The cap is
only the admission sum, never a deadline. `--tree-overrun` accepts only `keep`;
any other value is refused because stopping at a budget would discard a working judgment.

The watchdog (the progress observer defined in
[the gate reference](../../docs/reference/gate.md#budgets-and-the-stall-watchdog))
samples every 1,000 milliseconds. A byte of log growth or CPU progress resets
its silent-sample count. Sixty consecutive samples with neither ends the group.
The result is `stalled`, exit 6, not pass, fail, error or over-budget. The
verdict entry, check log and summary carry the last output line, group CPU time,
silent samples and duration.

Linux prefers an
[inherited kernel task clock](https://man7.org/linux/man-pages/man2/perf_event_open.2.html),
attached before exec so it retains descendant CPU even when parents discard exit
status. Go opens a disabled, inherited counter on the launching thread, enabled
only when the child executes its program. That thread stays reserved until the
counter closes, so another check cannot inherit it. Python attaches to a child
waiting for an exec-release byte. Neither counts the observer's CPU as the check's
progress.

A refused counter selects **process accounting**, read-only observations of the
check's existing process group. Each member is identified by PID and start time in
[process stat](https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html).
The sample sums its threads' runtime from
[Linux scheduler accounting](https://www.kernel.org/doc/html/latest/scheduler/sched-stats.html)
in nanoseconds. Any member's runtime changing, the membership changing, or a
member disappearing while read is progress, even below the previously reported
total. A short-lived child that disappears between samples still requires a
starter to run, or to disappear; following starters back reaches a member of the
previous sample whose runtime or presence changes.

The reported `cpu_time_ms` is the highest observed sum of member runtime plus
reaped-child CPU (`cutime` and `cstime` in process stat). Every member's child
CPU is captured before any thread runtime is read, so a child reaped during a
sample cannot count in both its own runtime and its parent's child CPU. It is a
lower bound: children whose exit status is discarded can be absent from the total but still
count as progress. Processes that leave the check's group are outside this
observation. Nested watchers read their own groups independently; there is no
shared service or supervisor, and the fallback never delays, stops or signals a
check. Both sources observe CPU at finer resolution than reported milliseconds.

The check log prints `watchdog CPU source: kernel task-clock counter` or
`watchdog CPU source: process accounting`; the verdict carries `cpu_source`.
The Python checker uses the same source names and sampling rule, and exports
`cpu_source` in stall observations. If neither source is readable, including an
unreadable process directory or thread runtime, `watchdog unavailable` is printed
immediately and detection is off; the check still runs. The source field is absent
when no sample succeeded. A selected source becoming unreadable also disables
detection rather than inferring a stall; a kernel counter decrease is likewise
unavailable. Exit 4 is a program that could not start or crashed.

The real gate adapter preserves exit 6 and the check observations. The first stall
retains the candidate with `gate-stall-rerun`; the drain waits for fewer gates in
flight, or none other, before gating that same head. A second stall on its head tree
holds the item as `gate-stalled`; diagnose it and use `tillr drain clear-fault`.
Fast per-check measurements reach the observatory alongside remaining-check spans,
including their budgets, overruns and stalls.

The `go-build` check builds with `-trimpath`, in `GOFLAGS`, so new checkouts reuse
the shared Go build cache. The adapter warms it once per Go version before the fast
tier. The gate program wrapper and the remaining Python checks use the same
stall rule: output grew or the selected source showed CPU progress in a one-second sample;
sixty consecutive samples with neither end the group as `stalled`, exit 6.
The `--gate-timeout` flag, 30 minutes by default, is a
reporting target only. An overrun is recorded in the verdict and summary; the
program keeps its own verdict. The Python per-kind timeout values likewise do
not kill checks; Go test commands disable Go's nested deadline with
`-timeout 0` so it cannot kill progress inside the watched group. A Python stall
records its check name, last output line, group CPU time and silence duration, and the adapter carries its observations
instead of mapping it to a harness error.

A rolling stall advances no watermark. The next rolling observation on the same
range is compared with the preceding recorded run, across drain restarts. A second
consecutive stall of the same check files one deduplicated trunk fixer naming that
check; an intervening different range, check or non-stall does not qualify. The
append-only gate record needs no lock wait.
See [budgets and the stall watchdog](../../docs/reference/gate.md#budgets-and-the-stall-watchdog).

The `documents` change check remembers a pass for each changed document in the
same result store as tree checks, under a key over the document's blob and every
input the renderer reads for it: the files and directories it links to, the names
beside it, the `.vantage.toml` and `.vantageignore` files, and the check's own
code. A pass recorded by another renderer version is judged again, and a key that
ever recorded a fail is always judged again. Because the renderer reads the
working tree and the key reads the commit, no pass is remembered for a document
whose inputs differ on disk from the commit (a link target that is ignored or
untracked but present, an edited tracked input, a config the commit lacks), and a
failing run remembers nothing unless every error finding names a document it was
given. The documents still to judge go to
one `uvx vantage-check` run. The inputs are listed in
[`change_common.go`](../../tools/gate/change_common.go).

## Optional local refs

A declaration names an optional local Git ref in `needs` with the prefix `ref:`:

```json
{"needs": ["ref:refs/heads/patch/pi"]}
```

The name must be a full ref; a single `*` wildcard can also match a set of refs.
Git's fetch-refspec grammar validates the name, so `?`, bracket patterns and malformed
refs are refused rather than reported with a fetch command that cannot work.
Before command execution or remembered-result lookup, a missing ref makes that check
`unavailable`. Its verdict entry lists `missing_needs`, each with the need and the
`git fetch origin` command that creates the local ref. The runner never fetches it.
An unavailable check is neither passed nor failed, and a run with only passed,
remembered and unavailable checks exits 0. The summary prints those three counts and
failed checks separately. A need without the `ref:` prefix names a checkout path, and a
missing one is an error rather than an unavailable check.

Every matched ref's name and resolved commit enters the tree check's
[fingerprint](../../docs/reference/gate.md#remembered-passes),
the identifier of its declared inputs. Moving a ref reruns the check; removing it makes
the check unavailable even if a pass was remembered. A ref changing while its command
runs prevents that result from being remembered under the original inputs.

## Caller-owned change checks

`checks` runs the change declarations in a directory, optionally including its fast
tree declarations. Heavy tree checks are left to the heavy runner. A change check judges the
caller’s commit range, not the tree alone; the
[gate reference defines that distinction](../../docs/reference/gate.md#change-checks-and-tree-checks).
The base and head are required, and the base must be an ancestor of the head. Missing or
unreadable revisions answer “could not judge” (exit 4) before any check runs. Each program
receives both commits and reads no remembered baseline. Its exit 4 is also no judgment,
not a rejection; an unavailable judgment outranks a rejection elsewhere in the same run.

```sh
tillr-gate checks --base <lane-base> --head <candidate-head> --logs <run>/change-logs
```

The repository’s change declarations cover commit identity, commit citations, rendering
changed Markdown, elapsed-time waits in tests, the fast-budget total, and refusing newly
added TypeScript files over 3,000 lines. The [loop’s default judge](change-gate.py)
runs the change declarations and the fast tree checks, and nothing else; it exits with the
runner's verdict.

For a lane candidate, the judge passes `--scope-to-change` to `checks` if the installed
binary advertises it. Checks with declared reads run only when a changed path matches
one of those reads; `.` declares the whole tree. Checks without declared reads always
run and are named in the output. The verdict records disjoint checks as
`skipped: no changed path is read`, not as passes. A binary that does not advertise the
flag runs the unscoped fast checks, and trunk proof never opts in to this scoping.

The fast tree checks cover Markdown conventions (formatting, scratch citations and design
citations), the pack mirror, and parallel Go builds.

## Judging heavy declarations

The [after-merge runner](../tillr-drain/README.md#after-merge-heavy-runs) calls `checks`
with `--heavy-tree` to select every heavy tree declaration, even when the supplied range
is empty. Change and fast tree checks are excluded. `--only <check-id>` selects exactly
one heavy declaration for attribution; a missing or non-heavy name refuses judgment.
The fingerprint result memory determines whether a selected check runs.
An explicit `budget_ms` is the reported target; a heavy declaration without it uses
`timeout_ms` as its target. Neither target kills a working check. A termination signal
cancels and reaps each declaration's process group before this face returns. Successful
completion also ends background children before the judged checkout can be removed;
cleanup observes process exit rather than imposing a deadline on the check.

```sh
tillr-gate checks --heavy-tree --base <tip> --head <tip> --worktree <detached-checkout> --logs <logs>
tillr-gate checks --heavy-tree --only <check-id> --base <tip> --head <tip> --worktree <detached-checkout> --logs <logs>
```

## Classifying observations and re-enabling checks

`classify` reads check observations as JSON from standard input and prints their classified
outcomes (and an optional run-level aggregate) on standard output, as specified in
[the gate reference](../../docs/reference/gate.md#result-classification).
Without a repository or result store it is pure and touches no disk state; when backed by a
repository or result store it resolves declaration selectors, computes committed
declaration fingerprints, and writes a marker with copied logs under `disabled/` when a
fingerprint contradicts itself.

```sh
tillr-gate classify
tillr-gate enable
```

`enable` records a repaired disabling episode under `enabled/`, moves that episode's
contradicting records and copied logs into `resolved/`, and removes the active marker when
it still holds the named episode.

## Sharded Go test runs

`shards` builds each Go package in a module once, balances independent test processes on
recorded durations, and isolates each shard's scratch and Git configuration. It refuses to
choose a scratch directory itself when no scratch root is configured, and refuses
cached runs on repository modules inside a lane worker.

```sh
tillr-gate shards -k 6 --scratch "$TMPDIR" tools/tillr-drain
```

## What it refuses

- **A plan with no registry** — a gate with no registry would pass everything.
- **A run with no input record** — there is no verdict against a plan that is not there.
- **A change whose candidate and worktree disagree** — the record names the mismatch rather
  than gating the wrong tree.

## Test

`go test ./...` in this directory builds the binary and drives the faces against scratch
repositories, fake gate programs and the real check registry.
