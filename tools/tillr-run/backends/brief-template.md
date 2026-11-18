# {{ITEM_ID}} — {{TITLE}}

<!--
The brief a lane receives, from design §3 of docs/design/tillr-muse-backend.md.
Instances are UNTRACKED: the driver copies this file into the lane directory as
`brief.md` and fills every double-brace placeholder. An adapter refuses a brief that
still carries an unfilled placeholder, is empty, or is missing one of the nine `##`
sections below — that preflight is the only machine-checkable part of a brief, and it is
what makes the convention load-bearing rather than decorative.

Lane layout the adapters read (fixed, so no adapter needs run-specific knowledge):

  <lane-dir>/brief.md        this file, filled
  <lane-dir>/fence.txt       one path per line: the fence
  <lane-dir>/base_sha        the sha pinned before launch
  <lane-dir>/session-id      a stager-minted UUID (opaque to tillr)
  <lane-dir>/worktree/       the named target, the default; the adapter's cwd
  <lane-dir>/stream.err      written by the adapter: the harness's stderr
  <lane-dir>/<run-id>.log    the raw stream: run exec's log, or the driver's redirect
  <lane-dir>/report.md       written by the extractor
  <lane-dir>/boundary.before, boundary.after, diff.patch, diff.names   the driver's
-->

## Header

- **Item:** {{ITEM_ID}}
- **Run:** {{RUN_ID}}
- **Backend:** {{BACKEND}}
- **Base sha:** {{BASE_SHA}}
- **Target:** {{TARGET}} ({{TARGET_KIND}})
- **Date:** {{DATE}}
- **Timeout:** {{TIMEOUT_SECONDS}}

<!--
TARGET_KIND is `lane worktree` in the default form. The other form names the main
checkout's path, states the driver's recorded reason for skipping the worktree, and
carries the before/after main-checkout status as its fence evidence — a weaker check,
labelled weaker here rather than dressed up as equivalent:

- **Target:** /workspace (main checkout)
- **Why not a worktree:** {{DRIVER_REASON}}
- **Main-checkout status before launch:** {{MAIN_STATUS_BEFORE}}
-->

## Task

{{TASK}}

The item's criteria, quoted verbatim. Every criterion maps to a verification line in
**Verification commands** below, or is marked here as not command-verifiable.

1. {{CRITERION_1}}

## Fence

The change may include exactly these paths, and no others:

{{FENCE_PATHS}}

The change must not include:

{{OUT_OF_FENCE_PATHS}}

**Outside the fence means stop and report.** Running commands is not touching the fence:
tests, builds, formatters and linters run freely inside the target, and only the paths
the resulting *change* includes are fenced.

Writes land only inside the target named in the header. State outside it — the store,
git refs, other lanes, and the main checkout whenever it is not the named target — is
never yours to touch, whatever command you choose.

When the work is done, **commit it in the target worktree** (the lane worktree form): one
commit on the lane's branch, a conventional message, no attribution trailers. The message
describes the change in repository terms and never names the queue item: no item id and no
`Tillr-Item:` trailer, since the item is private queue state. The loop's
candidate is the lane's HEAD, so an uncommitted change is no candidate — the tick records
`candidate-no-op` and a failing verdict against work that was in fact done. One commit: a
second commit in the same lane is a second candidate the loop did not ask for. In the
main-checkout form, commit nothing at all: the trunk is not a run's to move.

**The pack mirror is part of the change.** The tree under `tools/tillr*` is also delivered
as a pack copy, and the gate compares the two. When the fence above names a `tools/tillr*`
path, run `just pack-sync` in the target and commit the mirror in the same commit. It is
derived, not designed: do not hand-edit the mirror, and do not treat it as a second
concern. A change that skips it is correct work that fails the gate on `pack-sync` alone —
measured on 2026-11-14, on the first item a real drain ever dispatched.

## Settled decisions — do not redesign

{{SETTLED}}

If this brief is wrong about the tree, stop and report the conflict; do not invent a
mechanism.

## Facts carried

{{FACTS}}

## Method

{{METHOD}}

When you believe a criterion is met, append one line naming its criterion ID
(for example, `c1`) to the progress file named by `TILLR_PROGRESS_FILE` in this
run's scratch directory. Append; do not replace earlier lines. This is a worker
claim, not a verdict: the Lane and Item pages label it "claimed", never done.
The runner records every line as a `run.progress` event while you work.

```bash
printf '%s\n' c1 >> "$TILLR_PROGRESS_FILE"
```

For the full suite before committing, use the sharded runner on modules the gate
runs through it, as listed in the repository's verification report
`docs/pi/tillr-go-shards-2026-11-17.md`. Each shard is an independent test process
with its own scratch and git global configuration. The runner executes tests fresh,
without Go's test result cache:

```bash
python3 tools/go-test-shards.py tools/tillr-drain -k 6
```

Replace the module path for another tillr suite. Set `TMPDIR` to this run's scratch
or pass `--scratch` there. During a focused edit, prefer a targeted test first:

```bash
cd tools/tillr-drain && go test ./... -run '^TestName$'
```

Run the full applicable suite before committing; never run `just done` in a lane.
If this repository has no runner, use its documented test command instead.

## Verification commands

Run each of these and paste its output in your report. Never run the full gate
(`just done`): it holds the landing lock and is the gate's job at landing time.

{{VERIFICATION}}

While working on Go code, use a targeted `-run` for the behavior being changed. Run the
full suite once before committing. In the drain module, drop `-count=1` for the working
loop: the tests declare the sibling sources as inputs Go's result cache can see, so a
sibling change reruns tests and an unchanged tree still caches. This is test verification,
not permission to run the landing gate (`just done`).

## Report shape

Your final message contains, in this order:

1. paths changed;
2. each verification command with its output;
3. anything attempted outside the fence;
4. blockers.

Its last line is exactly one of:

    RESULT: done
    RESULT: blocked
    RESULT: failed
    RESULT: trunk-broken

Nothing after it. The extractor refuses a report without that footer, whatever your
exit code was, so a run that ends without one is recorded as incomplete.

`RESULT: trunk-broken` is not a synonym for `failed`. Use it only when you believe the
base you were staged from is itself red — the failure is in the trunk, not in the change
you were asked to make — and name the failing checks and the base sha in the body. It is a
suspect, not a conviction: the driver replays those checks on the base before anything is
recorded against the candidate, and a failure that is red on the base acquits the item and
files a fixer for the trunk. A claim the replay does not reproduce stays the candidate's
problem, so name what you saw rather than guessing.

## Stop conditions

Stop and report when:

- a named file is dirty with someone else's work;
- a named symbol is gone;
- the fix needs a file outside the fence;
- a verification command fails outside this task's scope;
- in the main-checkout form, the checkout's status moves for reasons that are not yours.
