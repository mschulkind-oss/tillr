# The Pi proof backend — `pi`

The proof that the adapter contract is not `muse`-shaped. Pi differs at both seams the
contract leaves adapter-private: the brief arrives on stdin rather than through a
prompt-file flag, and effort is Pi's own `--thinking` level rather than the muse's
`--reasoning-effort`. Design:
[`docs/design/tillr-muse-backend.md`](../../../../docs/design/tillr-muse-backend.md),
sections 7 and 9.

| File | What it is |
| :--- | :--- |
| `run-pi` | The adapter. A `run.request`'s `Command[0]` names it. |
| `extract-report` | The dialect: Pi's event stream in, `report.md` out. |
| [`compact`](./compact) | Compact an existing session by id, without a user prompt. |
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
the template's nine sections; the target is not the directory the adapter runs in; `pi` is
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
[`docs/design/session-recording.md`](../../../../docs/design/session-recording.md)
[§2.2](../../../../docs/design/session-recording.md#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy)
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
([§7.1](../../../../docs/design/session-recording.md#71-four-mechanical-fail-open-guarantees)):
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

## Pausing and continuing

Pause kind: `stops-where-it-stands`, declared in [`pause-kind`](./pause-kind).
The runner sends SIGTERM, waits ten seconds, then kills any surviving process group.
The same session receives a restart notice as its first continuation message.

**Measured 2026-11-16:** Pi entered a bash tool call whose command was `sleep 60`;
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
