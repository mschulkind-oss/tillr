# {{ITEM_ID}} — {{TITLE}}

You are running unattended: nobody reads or answers your messages while you work. Your
first message without a tool call ends the run, so keep
working with tools until the goal in **Report shape** is met or one of the obstacles it
names holds.

<!--
The brief a lane receives; reference: docs/reference/tillr-muse-backend.md.
Instances are UNTRACKED: the driver copies this file into the lane directory as
`brief.md` and fills every double-brace placeholder. An adapter refuses a brief that
still carries an unfilled placeholder, is empty, or is missing one of the ten `##`
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

## Scope and boundaries

Change what the work needs, and nothing the work does not need. Do not change files outside the target worktree or touch denied paths:

{{OUT_OF_FENCE_PATHS}}

Running commands is permitted: tests, builds, formatters and linters run freely inside the target, and only the paths the resulting *change* includes are judged.

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
as a pack copy, and the gate compares the two. When your change touches a `tools/tillr*`
path, run `just pack-sync` in the target and commit the mirror in the same commit. It is
derived, not designed: do not hand-edit the mirror, and do not treat it as a second
concern. A change that skips it is correct work that fails the gate on `pack-sync` alone —
measured on 2026-11-14, on the first item a real drain ever dispatched.

**Format before committing.** When the target has a `just format` recipe, run `just format`
after staging new files and before `git commit` (then run `just pack-sync` if `tools/tillr*`
changed): `just format` reads tracked and staged paths, so running it before `git add` skips
brand-new Go files.

## Seats

These are seats tillr runs as nodes; a worker cannot start one.

{{SEAT_LINES}}

Give mechanical searching, editing and checking to the cheaper sub-agents your own harness offers.
Your harness's configuration chooses those sub-agents' models; tillr's policy does not.

## Settled decisions — do not redesign

{{SETTLED}}

If this brief is wrong about the tree, name the `definition` obstacle with the evidence;
do not invent a mechanism.

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

Do not run a whole module suite in a lane. The landing gate runs every suite your change
selects, once, and a failure there comes back to this run naming the test. A whole suite
run in a lane is just run again by the gate. When a failure is
handed to you (a red check, a gate log, a triage packet), it already names the failing
test: run that test, not the suite it came from.

The sharded runner is how the gate runs a suite. Each shard is an independent
test process with its own scratch and git global configuration, and tests run fresh,
without Go's test result cache. Use it yourself only when a change is so broad that no
selection covers it:

```bash
python3 tools/go-test-shards.py tools/tillr-drain -k 6
```

Do not call `go test ./...` on a whole suite directly: several lanes doing so at once
drove the load average past 60 and made `go test` hit its ten-minute timeout.

Never end processes by name or pattern: no `pkill`, no `killall`, no `kill` fed from
`pgrep`. Every lane, the landing gate and the trunk's after-merge proof run this same
runner as the same user, so a pattern that matches your suite matches theirs. On
2026-11-20 `pkill -f go-test-shards` from lane workers ended the proof's suite checks
with SIGTERM, and the trunk went unproved for eight hours. End only a process you
started, by the process ID you recorded when you started it.

Replace the module path for another tillr suite. Set `TMPDIR` to this run's scratch
or pass `--scratch` there. On each iteration, run the narrowest test selection (the
specific package and targeted `-run` pattern) that covers your change rather than a whole
module suite:

```bash
cd tools/tillr-drain && go test ./drain -run '^TestName$'
```

Before committing, run the tests for the behavior you changed and every test in a file
you edited; leave whole suites to the gate. Never run `just done` in a lane.

## Verification commands

Run each of these. Never run the full gate
(`just done`): it holds the landing lock and is the gate's job at landing time.

{{VERIFICATION}}

While working on Go code, use a targeted `-run` for the behavior being changed, and leave
the whole suite to the gate. In the drain module, drop `-count=1` for the working
loop: the tests declare the sibling sources as inputs Go's result cache can see, so a
sibling change reruns tests and an unchanged tree still caches. This is test verification,
not permission to run the landing gate (`just done`).

## Report shape

The goal of this run is one commit in the target that meets the criteria above (in the
main-checkout form, the finished change, left uncommitted). Do not end your turn until
that commit exists and its verification has run. The verifier checks the commit itself;
do not write a summary or a test inventory.

The only other ending is an **obstacle**: a fact this run cannot change, of one of the
kinds below, together with what it needs. Each kind leads to a different action, so name
the one that holds:

| `OBSTACLE:` | When | Required lines | What happens next |
|---|---|---|---|
| `definition` | The item's own words are wrong or contradict the tree | `CRITERIA:` the criteria affected; `EVIDENCE:` the contradiction, quoted | The design seat amends the item |
| `trunk` | The base is red, and this change did not cause it | `CHECKS:` the failing gate check ids; `BASE:` the base sha | The loop replays those checks on the base; if they are red there, the item waits on the trunk's repair |
| `tooling` | A tool or the harness is defective: a hung suite, a broken adapter, a missing binary | `COMMAND:` the command; `EVIDENCE:` its output; `PATHS:` the tool's paths | The loop files a defect item for the tool, and this item waits on it |
| `seat` | You made the attempt, and it shows the task exceeds what this model can do | `TRIED:` the change you made and what its tests showed; `MISSING:` what you could not do | The item moves to a stronger model, or to the design seat for a plan |
| `owner` | A decision only the owner can make | `QUESTION:` the question | The design seat answers it from the documents, or asks the owner |

These are not obstacles, and are work to continue: a patch that does not apply cleanly, a
merge conflict, a test that fails, a suite that takes long, and work not
yet done. An obstacle the loop cannot confirm (a `trunk` claim the base replay does not
reproduce, for instance) comes back to this run with what the loop found.

Your final message on success is just `RESULT: done`. For an obstacle, give its
`OBSTACLE:` line and its required fields, each on a line of its own.
In either case the last line is exactly one of:

    RESULT: done
    RESULT: blocked

Nothing after it. `RESULT: done` means the commit exists. `RESULT: blocked` means an
obstacle is named above it with every required line. Any other last line, or a blocked
report without a complete obstacle, is a missing output and comes back to this run.

For example:

    OBSTACLE: definition
    CRITERIA: c1
    EVIDENCE: the item asks for a flag that was removed
    RESULT: blocked

## Stop conditions

There is no stop other than the commit and the obstacles in **Report shape**. The
situations that used to end a run here are each one of those kinds:

- a named file is dirty with someone else's work, or, in the main-checkout form, the
  checkout's status moves for reasons that are not yours: `tooling`;
- a named symbol is gone, or the brief is otherwise wrong about the tree: `definition`;
- a verification command fails outside this task's scope: `trunk` when the same check is
  red on the base, else `tooling` when the tool itself is broken.
