# The Pi proof backend — `pi`

The proof that the adapter contract is not `muse`-shaped. Pi differs at both seams the
contract leaves adapter-private: the brief arrives on stdin rather than through a
prompt-file flag, and effort is Pi's own `--thinking` level rather than the muse's
`--reasoning-effort`. Design:
[`docs/design/tillr-muse-backend.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-muse-backend.md),
sections 7 and 9.

| File | What it is |
| :--- | :--- |
| `run-pi` | The adapter. A `run.request`'s `Command[0]` names it. |
| `extract-report` | The dialect: Pi's event stream in, `report.md` out. |
| [`compact`](./compact) | Compact an existing session by id, without a user prompt. |
| [`keep-going.ts`](./keep-going.ts) | The pi extension that answers an ending that misses the goal, in the same session. |
| [`land.ts`](./land.ts) | Records a landing offer and parks the call. |
| [`testdata/land.test.mjs`](./testdata/land.test.mjs) | Exercises offer validation, parking and adapter arguments. |
| [`testdata/keep-going.test.mjs`](./testdata/keep-going.test.mjs) | Checks that a parked call is not nudged. |
| [`testdata/extract-report-park.test.py`](./testdata/extract-report-park.test.py) | Checks report extraction for parked calls. |
| [`testdata/parked-exit.test.py`](./testdata/parked-exit.test.py) | Checks the watcher leaves parked sessions alone. |
| [`testdata/keep-going-driver.mjs`](./testdata/keep-going-driver.mjs) | Drives [`keep-going.ts`](./keep-going.ts) through scripted endings without a model, for `pi_keep_going_test.go`. |
| [`testdata/complete.jsonl`](./testdata/complete.jsonl) | A complete event stream, ending in the `RESULT:` footer. |
| [`testdata/truncated.jsonl`](./testdata/truncated.jsonl) | The same stream cut off before its footer — the negative control. |

## What the adapter does

```
run-pi --backend pi --model PATTERN --effort LEVEL
       [--brief PATH] [--target PATH] [--timeout SECONDS] [--session-id UUID]
```

It runs, from the target as its working directory:

```
timeout <wall> pi --print --mode json --model <policy model> --thinking <policy effort> \
    [--session-id <lane/session-id>] < <brief-path>
```

Pi prepends the redirected input to its first prompt. The brief's contents never travel
in a command-line argument or an environment value: conflict evidence can exceed the
operating system's argument-size limit.

with `BOARD_DRAIN_BRIEF` (the brief's absolute path) and `BOARD_DRAIN_SESSION` exported
to the harness. Those two names are the seam the retired monolith fed Pi through; that
spelling lives and dies inside `run-pi`, and no policy, brief or `run.request` learns it.

Refusals (exit 2, nothing started): the `backend` field names neither this adapter nor a
seat alias of it (`pi` or `pi-<role>`, as `pi-designer` runs this adapter on the design
seat's model); no model; no effort; the brief is missing, empty, unfilled or missing one of
the template's ten sections; the target is not the directory the adapter runs in; `pi` is
not on `PATH`.

Exit: the harness's own code, 124 from the wall, 2 from a refusal.

### The level asked for is not always the level run

`--thinking <level>` reaches `pi` and `pi` clamps it to the routed model's own
`thinkingLevelMap` before any request is made: `clampThinkingLevel` (pi-ai) is applied by
`ModelRuntime.resolveModel` when a virtual model routes to the physical one it names.
The first live run asked for `medium` and every settled assistant message in its stream
carries `"thinkingLevel":"high"` — four turns, each in its `message_end` and again in
`agent_end`; only the `message_start` snapshots precede the level being attached. The
`deepseek/deepseek-v4.1-flash-1017` row in the harness's own provider catalog
(`pi-ai/dist/providers/data/openrouter.json`) maps `medium: null` — that family offers
off, low, high and max — so `medium` clamps forward to `high`:

```
getSupportedThinkingLevels(row) -> off,low,high,max
clampThinkingLevel(row, "medium") -> high
```

That is a measured fact about the harness and the model, not a defect here, so the
adapter keeps passing its argument verbatim: the report's `effort:` is the level asked
for, the stream's `thinkingLevel` is the level run, and on this family the two differ.
An eval that compares routes by effort is comparing what was asked; the level a route
actually ran at is only in the stream.

## The recording seam

The adapter is also the parent of everything a run spawns, so it is where
[`docs/design/session-recording.md`](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/session-recording.md)
[§2.2](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/session-recording.md#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy)
puts the emitters. When `TILLR_RUN_ID` is in the environment (staging's run id,
which `tillr-run exec` puts there) and the record directory can be created, `run-pi`
exports, for `pi` and every child it starts:

| Variable | Value |
| :--- | :--- |
| `TILLR_TRACE_DIR` | `<root>/.tillr/loop/<run-id>/` — the directory staging keeps this run's brief, fence and base sha in |
| `BASH_ENV` | `tools/tillr-trace/bash-shim.sh`, which appends one `proc.exec` line per shell command to `$TILLR_TRACE_DIR/proc-exec.jsonl` |
| `TILLR_REPO_ROOT` | the repository root, so a shim row can name a repo-relative cwd |

`run-pi` records no git commands. Git trace2 recording was removed on 2026-11-17 by the
owner's ruling: the `GIT_TRACE2_EVENT` variable it set is inherited by every test suite the
run starts, and one run recorded 918,054 git commands. `run-pi` unsets `GIT_TRACE2_EVENT`,
`GIT_TRACE2_PARENT_SID`, `GIT_TRACE2` and `GIT_TRACE2_PERF` so a run does not inherit them
from the loop's environment either; `tools/tillr-run/backends_trace_env_test.go` pins that.

`<root>` is derived from the lane worktree the way `tillr-loop` derives it — a linked
worktree's git common directory is the main checkout's `.git` — so the adapter, the gate
and the lander agree on one `.tillr/loop`.

All of it is fail-open
([§7.1](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/session-recording.md#71-four-mechanical-fail-open-guarantees)):
no run id, no shim, or a record directory that cannot be created means the emitter is
silently absent. The run's exit code, its streams and its candidate are never a function
of whether recording worked.

## Compacting before an expired resume

The executable compact step uses Pi's JSON-lines RPC protocol: verify the opened session id,
then request compaction in the document desk's original working directory. It prints one JSON
receipt containing `tokens_before`, `tokens_after`, `tokens`, and `cost_usd`. The after count is
Pi's estimate of the rebuilt context, not a provider-exact count; unreported usage and cost
are null. A missing session, failed request, mismatched id, or missing token measurements
fails explicitly rather than creating a session or continuing without compaction.

The drain invokes this step only at an expired, over-40,000-token dispatch. It records the
receipt against that run's item before resuming. Before the model request, the adapter updates
the drain's journal with the actual transcript path and append position. If the response is
lost after Pi saves its checkpoint, the drain recovers its token and cost measurements from
that checkpoint instead of compacting twice. An intent without an identifiable completed
checkpoint is ambiguous and refuses another paid request. No process or request keeps a
session warm.

## Keeping a writer going until it commits

A worker's goal is one commit in its lane worktree. [`keep-going.ts`](./keep-going.ts)
is a pi extension, and `run-pi` loads it with `-e` only for a run that has that goal: the
brief's first `**Target:**` line ends `(lane worktree)`, staging's `base_sha` file is
beside the brief, and `TILLR_READ_ONLY_GROUP` is empty. The adapter exports the base as
`TILLR_GOAL_BASE`. A judge, a fork checkout, a plain directory and a read-only node get
neither the extension nor the variable.

At pi's `agent_before_settle` boundary, the extension compares the worktree with the goal.
If the worker ended its turn without meeting the goal and without naming a blocker, the
extension appends a `tillr-goal` custom message that says what is missing: no commit,
the uncommitted paths, or no `RESULT:` footer. It then asks pi to continue. The run
carries on in the same session, so nothing counts it as a failure, starts a new run, or
reports it as ended. These are the only endings it lets settle:

| Ending | Why it settles |
| :--- | :--- |
| HEAD is off the base, the worktree is clean, and the last message has a footer | The goal is met. |
| `RESULT: blocked` with a complete obstacle: an `OBSTACLE: <kind>` line and that kind's fields | The worker named an obstacle, and the drain routes it by kind. The kinds and their fields are listed in the drain's [obstacle contract](https://github.com/mschulkind-oss/tillr/blob/main/tillr-drain/README.md#a-worker-ends-with-its-commit-or-a-named-obstacle). |
| The tree and the report (whitespace aside) match the last nudge exactly | An identical repeat. Another nudge would change nothing, so the drain's own repeat rule takes over. A different report on an unchanged tree is nudged again. |
| An error, an abort, a stop that is not `stop`, or a requested pause | These endings were not the model's choice, and each has its own route. |

The extension keeps no count and no time limit of its own. The run's wall, if staging
set one, still bounds the whole process. `RESULT: failed`, and a blocked report whose
obstacle is missing its kind or a field, are not endings it accepts. The nudge names what
is missing. The extension and the drain read obstacles against the same case file,
[`testdata/obstacle-cases.json`](./testdata/obstacle-cases.json). On 2026-11-18, Sol 6.2 at medium ended 19 of 61 drain runs with that
footer and no blocker, after one to five read-only commands, and nothing answered them.

A keep-going writer's session is also trimmed. `run-pi` starts pi with
`--no-extensions --no-skills --no-context-files --no-prompt-templates`. It then loads back
the subagent and background-task packages and every single-file extension in the agent's
own `extensions/` directory except `steering-timer.ts`, because the provider, billing-tier,
usage-ledger and quota extensions live there. It appends the worktree's `AGENTS.md` as the
system prompt's project text. The worker no longer gets the house briefing, the memory
package, session search, web tools, the todo list, code mode, steering tools or skills. On 2026-11-18 the first turn's prompt dropped from 23354 to
9350 tokens on Luna. Judges and read-only runs are not trimmed.

The claude, muse and agy adapters have no such hook yet. Each needs the same check at its
harness's last actionable boundary, with the same three settling endings. Claude Code's
`Stop` hook can block a stop with a reason. The muse and agy CLIs would need either a
comparable hook or an adapter loop that re-opens the session with the missing-goal
message.

## Pausing and continuing

Pause kind: `stops-at-a-gap`, declared in [`pause-kind`](./pause-kind).
When the runner writes `TILLR_PAUSE_REQUEST`, the adapter's
[`stop-at-gap`](./stop-at-gap) watcher sends **SIGUSR1 to the pi process itself**,
never its process group or a tool child, even when an explicit wall uses `timeout`.
The watcher forwards JSON output unchanged and waits for the session header before
signaling: the fork emits that header after installing its SIGUSR1 handler, so a
pause during startup is not lost to Node's debugger handler. Startup without a header
is covered by the same straggler bound, not a separate timeout or build probe.
Fork commit `a763610b` (`feat(agent): stop print workers safely on SIGUSR1`),
delivered in this repository's `patch/pi`, defines the request: finish already-started
calls and a streaming model response, save results, start no further tool or model
request, flush output, and exit 0. Duplicate requests have the same effect as one.
Session persistence stays enabled; the adapter does not pass `--no-session`.

The runner enforces the [straggler bound](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/tillr-restart.md#pausing-a-session):
120 seconds by default, supplied by `pause_timeout_seconds`. At that bound it kills
any surviving process group, including tools. Continuing opens the same session with
a restart notice; a gap stop does not claim an interruption, but a bound kill tells
the session the command was interrupted by a restart and may be run again.

An older pi without this fork change ignores the stop request: Node reserves SIGUSR1
for its debugger, announces it on stderr, and continues. The runner then applies the
same straggler bound and interruption notice. Even patched pi may announce the debugger
in `stream.err`; that line is not a failure. The adapter does not probe the installed
build or send a second kind of request. Publishing the patch branch and rebuilding
pi on the host are separate from this adapter change.

`TestPiPauseAtGapAndStraggler` runs the real adapter against stand-in harnesses, with
no model or real pi: one honors SIGUSR1 after saving a tool result, and one ignores it
until the injected 120-second bound kills the group. Both resume with the appropriate
notice, and a tool child checks that it did not receive SIGUSR1. The pause is requested
before print-mode readiness to cover startup, and split header writes plus startup
diagnostics verify that the JSON stream is forwarded unchanged.

## Ending a settled session pi does not exit

Pi's print mode exits once its agent settles and no extension holds the session
open. Only one extension takes a hold. pi-background-tasks takes one for each
`completionPolicy: "wake"` task, and it releases the hold when the completion's turn
settles. On 2026-11-18 that release was missed for completions that the extension's
`context` hook injected as a fallback into a model call already in progress. The
journal records those as `fallback-injected` and `observed via fallback`, with no
`enqueued`. Print mode then waited forever, and `session_shutdown` never ran, so
every extension timer stayed armed. Three workers had committed, and their settled
reports said `RESULT: done`, but they sat for seven hours. The fix belongs in that
extension's patch series. Until it ships, and for any other way pi can fail to exit,
the [`stop-at-gap`](./stop-at-gap) watcher ends the session on pi's own statements:

1. The stream carries a top-level `agent_settled`, and no `agent_start` follows it.
   keep-going acts at `agent_before_settle`, so a settle is already its verdict.
2. The extension's journal (`PI_BACKGROUND_TASK_JOURNAL`, else
   `$XDG_STATE_HOME/pi-background-tasks/journal.sqlite`) has an instance row for this
   pi process since its launch, the row is not disabled, and the journal shows no wake
   still owed. A wake is owed by a wake-policy task that has not ended. It is also owed
   by an ended one whose completion was neither observed by the session, nor stopped
   (`wake_requested` 0), nor failed to enqueue. A wake turn after a settle is ordinary
   work: on 2026-11-18, 16 of about 760 footer-bearing settles were followed by one in
   the same process. A journal that cannot answer never ends a session.

Then the watcher sends pi its documented shutdown, SIGTERM. Pi disposes the runtime,
emits `session_shutdown` and exits 143. The watcher forwards what pi flushed, appends
`{"type":"tillr_harness_ended","after":"agent_settled",…,"actions":[…],"harness_exit":143}`
and exits 0. The executor records that as the run's
[`ending_exception`](../../README.md#ending-a-settled-harness-that-lingers).

Two waits are involved. Neither decides that work is over:

- **The grace.** The journal is first read 30 seconds after the settle
  (`TILLR_SETTLED_EXIT_GRACE_SECONDS`). A healthy pi exits by itself within that
  time, and that exit is recorded as ordinary, not as an exception.
- **The exit bound.** A pi that is still alive 120 seconds after its shutdown
  (`TILLR_HARNESS_EXIT_BOUND_SECONDS`) gets a second SIGTERM, which pi's handler
  turns into an immediate exit. After a second bound, only pi gets SIGKILL.

The watcher never signals its own process group, a tool child or the runner.

`TestPiSettledHarnessThatNeverExitsIsEndedByTheAdapter` runs the real adapter and
runner against a stand-in pi that settles with a footer and a leaked-hold journal,
then never exits. The settled-idle samples are withheld, so only this ending can end
the run. `TestPiSettledOwedWorkReadsTheBackgroundTaskJournal` covers the journal
shapes, and `TestPiSettledShutdownEscalates` covers the escalation with no clock.

**Historical persistence measurement, 2026-11-16 (not the current pause kind):**
Pi entered a bash tool call whose command was `sleep 60`;
SIGTERM to the harness returned 143. Opening the same session returned 0 and answered
`cedar-pause-6149`, the marker given before the signal, without that marker appearing
in the continuation prompt. This measures the harness, not a model guarantee.
The configured Bedrock Haiku model reached the tool; the earlier endpoint failure
before a tool call measured nothing. Extensions, MCP, skills and context discovery
were disabled to isolate the harness's persistence behavior.

The command sequence, with a fresh UUID and writable session directory:

```sh
pi --offline --no-extensions --no-mcp --no-skills --no-context-files \
    --no-prompt-templates --no-themes --print --mode json \
    --model amazon-bedrock/global.anthropic.claude-haiku-5-5 --thinking low \
    --tools bash --session-dir "$DIR" --session-id "$UUID" \
    'Remember the exact marker cedar-pause-6149. First call bash with the command sleep 60. After that command ends answer only READY. Do not use any other tools.'
# Once tool_execution_start names bash with command sleep 60:
kill -TERM "$HARNESS_PID"
# Re-run the same flags, directory and UUID with this prompt instead:
# The command was interrupted by a restart. Do not run tools. What exact marker
# did I give you before the interruption? Answer only that marker.
```

## Declared capabilities

- **writes-tree:** yes.
- **resumable:** yes. `--session-id` opens the existing session in the same worktree.
- **write confinement: NOTHING beyond the boundary snapshot.** Pi offers no OS sandbox
  for its shell tool, so this adapter claims none and does not borrow the muse
  adapter's claim. `boundary.before`/`boundary.after` is Pi's whole boundary, and the
  canary below is a measurement of that, not a proof of confinement.
- **structured report:** no schema flag; the `RESULT:` footer is plain text in the
  assistant's answer, which is exactly why the footer is a brief convention.
- **usage:** whatever the stream carries, recorded as an estimate.

## The dialect, and why it is read in three ways

Pi streams agent events as one JSON object per line: `session_start`, `message_start`,
`message_update` (carrying `assistantMessageEvent`, whose `text_delta` carries `delta`),
`message_end`, `tool_execution_*`, `agent_end` (carrying the turn's whole `messages`
list). The extractor reads the assistant's text from the first of these it finds:

1. the `text_delta` deltas — the streaming form, and the only one a truncation leaves
   intact;
2. `agent_end`'s `messages` — the settled form;
3. a message snapshot's `content` blocks of `type: text` — the session-file form.

Snapshots are keyed and replaced rather than appended: `message_start` and
`message_update` carry the message *so far*, so appending them would repeat every token
once per update. The committed test asserts the footer appears exactly once, which is
that rule failing loudly if it is ever broken.

`--mode json` writes the session file's own header as the stream's first line —
`{"type":"session","version":3,"id":"…"}` — and that header is the only place the
session id appears, so it is what the dialect reads for the report's `session:`. It is
the lane's stager-minted id when `--session-id` was passed. A stream that does not open
with that header yields `session: unknown` rather than a guess.

## Silent-failure signatures

- **Exit 0 with nothing written.** `diff.names` empty while the report claims changes is
  a false-green report; the driver's check catches it because every element of that check
  is produced from the tree afterwards.
- **A report with no `RESULT:` footer**, whatever the exit code. The extractor refuses it
  with exit 4 and the report says `complete: no`.
- **No model-resolution preflight is possible.** Pi has no cheap equivalent of
  `muse model-profile show`, so a wrong model pattern fails at the harness instead of
  before it. The unset-model refusal is the guard this adapter can offer; the policy is
  the one file a bad id is fixed in.

## Stage 1, end to end

1. **Stage the lane.** `<lane>/worktree` is a `git worktree add --lock` at the base sha;
   `<lane>/` holds `brief.md`, `fence.txt`, `base_sha` and `session-id`. The worktree is
   the default target. Naming the main checkout instead is allowed and weaker: record the
   driver's reason in the brief's header and carry the checkout's before-status as the
   fence evidence.
2. **Take the boundary snapshot** into `boundary.before`: the store file's size and
   mtime, `git for-each-ref`, `git worktree list`, and the main checkout's `git status`.
3. **Write the brief** from [`../brief-template.md`](../brief-template.md), filling every
   placeholder. An unfilled one is refused, so a template nobody filled cannot dispatch.
4. **Run the adapter** with the worktree as the cwd and the policy's model data on argv:
   `run-pi … > <lane>/<run-id>.log`.
5. **Extract** `report.md`, naming what staging asked for:
   `extract-report --backend pi --model <policy model> --route <the model id's route suffix> --effort <policy effort> --exit-code $? < <run-id>.log`.
   `--route` is a staging fact and not a stream one: a routed model id loses its
   `:<route>` suffix on the wire, so the stream names only the wire model and nothing in
   it distinguishes the four policy routes. The header carries the route staging asked
   for, and `route: unknown` when staging named none.
6. **Check from the tree, not from the report:** `boundary.after` diffs empty against
   `boundary.before`; `git -C <target> diff <base_sha> > diff.patch` and every name in it
   is inside `fence.txt`; every path the report claims is in the diff and every path in
   the diff is claimed or explained; and the brief's verification commands are re-run by
   the driver, never read off the report. On Pi this step is the *only* boundary there
   is.
7. **Land through the gate** (`just done`) after the attempt result is assigned.

## The canary a live run owes

The first real Pi run carries the same instructed write just outside the worktree. Here
it is a measurement, not a proof: the README's claim is that nothing confines Pi, so the
canary's outcome — denial or success — is recorded here as the measured statement of what
this adapter's boundary is.

## What a live run needs

A machine with `pi` installed and authenticated, a model pattern and thinking level in
the backend policy, and a lane staged as above. Then:

```
tools/tillr-run/backends/pi/run-pi \
    --backend pi \
    --model "$(jq -r '.backends.pi.model' config/tillr/backend-policy.json)" \
    --effort "$(jq -r '.backends.pi.effort' config/tillr/backend-policy.json)" \
    --brief "$LANE/brief.md" --target "$LANE/worktree" \
    > "$LANE/<run-id>.log"
```

Run it from `$LANE/worktree`.
