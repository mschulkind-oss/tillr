# The Claude Code backend — `claude`

The adapter that runs the Claude Code command line headless (`claude -p`). It is the only
way a drain can spend a Claude subscription, which pi cannot reach. Design:
[`docs/design/tillr-claude-backend.md`](../../../../docs/design/tillr-claude-backend.md),
sections 1 to 4; the adapter contract every backend shares is
[`docs/design/tillr-muse-backend.md`](../../../../docs/design/tillr-muse-backend.md).

| File | What it is |
| :--- | :--- |
| `run-claude` | The adapter. A `run.request`'s `Command[0]` names it. |
| `extract-report` | The dialect: the harness's stream of JSON lines in, `report.md` out. |
| [`quota`](./quota) | One authenticated usage GET, no model tokens; five-hour and seven-day fractions used and UTC reset times. |
| [`testdata/complete.jsonl`](./testdata/complete.jsonl) | A complete stream, written by hand to the measured line shapes, ending in a `result` line whose text ends in the `RESULT:` footer. |
| [`testdata/truncated.jsonl`](./testdata/truncated.jsonl) | The same stream cut off before its footer and its `result` line — the negative control. |

The fixtures carry invented ids and no account data. They were written from the line
shapes measured on Claude Code 2.1.325, not captured from a run.

## Reading quota without starting work

Set a backend row's `quota` to the command's argument list and optionally set
`reserve` (default 0) to the fraction kept for the owner. The loop refreshes readings
older than 60 seconds before choosing and claiming work; it never cancels a claimed
dispatch after staging. A refusal still holds a backend without a quota command.
The [quota admission contract](../../../tillr-drain/README.md#backend-quota-admission)
explains errors, recorded readings, and the waiting view.

The command uses `CLAUDE_CODE_OAUTH_TOKEN` or Claude Code's existing credentials,
never a model prompt or credential refresh as a fallback. The
[reading method and measured cost](../../../../docs/design/tillr-claude-backend.md#direct-readings-and-the-owners-reserve)
record the endpoint and live verification.

## What the adapter does

```
run-claude --backend claude --model MODEL --effort LEVEL
           [--brief PATH] [--target PATH] [--timeout SECONDS] [--session-id UUID]
           [-- HARNESS-ARGUMENTS...]
```

It runs, from the target as its working directory:

```
timeout <wall> claude -p --output-format stream-json --verbose \
    --model <policy model> --effort <policy effort> \
    [--session-id <lane/session-id>] \
    --strict-mcp-config --dangerously-skip-permissions \
    <harness arguments> < brief.md
```

- **The brief travels on standard input**, so its size is not bounded by the argument list.
- **`--strict-mcp-config` is always passed.** Measured: without it a run loads every
  connector server the account and the jail know — about three times the prompt — and a
  worker that holds mail, calendar and chat tools can act outside the repository. With it
  the run sees only the servers its row names with `--mcp-config`.
- **`--dangerously-skip-permissions` is always passed, and `YOLO_NO_LAUNCH_FLAGS=1` is
  exported.** A headless run cannot ask, so without the flag every write is refused. The
  jail's launcher would add the same flag itself; switching the launcher's flags off and
  passing it here keeps the argument list the same inside a jail and outside one.
- **The lane markers** `BOARD_DRAIN_BRIEF`, `BOARD_DRAIN_RUN` and `BOARD_DRAIN_SESSION`
  are exported exactly as [`run-pi`](../pi/README.md) exports them, so a hook can tell a
  lane's worker from a person's session.
- **The recording seam** is the same as `run-pi`'s: with a run id in the environment,
  `BASH_ENV`, `TILLR_TRACE_DIR` and `TILLR_REPO_ROOT` are exported, all fail-open. No
  `GIT_TRACE2_*` variable is exported, and any the loop carries are unset (git trace2
  recording was removed on 2026-11-17). The table is in
  [the pi README](../pi/README.md#the-recording-seam).

Refusals (exit 2, nothing started): the `backend` field names neither `claude` nor a seat
alias `claude-<role>`; no model; no effort; the brief is missing, empty, unfilled or
missing one of the template's nine sections; the target is not the directory the adapter
runs in; `claude` is not on `PATH`; an argument before `--` that the adapter does not know.

Exit: the harness's own code, 124 from the wall, 2 from a refusal.

## Harness arguments

A backend row in the policy may carry `harness_args`, a list of strings. Staging appends
them to the adapter's argument list after a `--`, and this adapter passes everything after
that `--` to the harness unchanged, after its own fixed flags. The policy holds the data
and the harness holds the spelling, so tillr has no permission vocabulary of its own.

| Wanted | Harness argument |
| :--- | :--- |
| Only some built-in tools | `--tools "Read,Edit,Bash"` |
| Refuse one tool or one command shape | `--disallowed-tools "WebSearch" "Bash(git push *)"` |
| Add custom tools | `--mcp-config <file in the repository>` |
| No skills or slash commands | `--disable-slash-commands` |
| Extra skills | `--plugin-dir <directory in the repository>` |
| Standing text for the seat | `--append-system-prompt <text>` |
| No user or project settings | `--setting-sources ""` |

Paths are relative to the lane's worktree, a checkout of the repository, so a file a row
names is versioned with the row. The pi, muse and agy adapters take no harness arguments
and refuse the `--`, as they refuse any argument they do not know.

## Pausing and continuing

Pause kind: `stops-where-it-stands`, declared in [`pause-kind`](./pause-kind).
The runner sends SIGTERM, waits ten seconds, then kills any surviving process group.
The continuation uses `--resume`, not a second `--session-id` creation, and its first
message says the session was interrupted by a restart.

**Owner-recorded measurement, 2026-11-16:** a Claude session was sent SIGTERM during
`sleep 60`, exited 143, and the resumed session answered with the marker given
before the signal. The accepted evidence is the owner's ruling in the
[restart criteria](../../../../docs/design/tillr-restart.md#pause-a-worker-between-tool-calls-and-resume-it-where-it-stopped).
This lane did not re-run Claude or recover the original exact argv; the command
shape below describes how to reproduce the ruling, not an invented raw transcript.

```sh
claude -p --output-format stream-json --verbose --session-id "$UUID" \
    --strict-mcp-config --dangerously-skip-permissions < "$MARKER_AND_SLEEP_PROMPT"
# Once the harness enters sleep 60:
kill -TERM "$HARNESS_PID"
claude -p --output-format stream-json --verbose --resume "$UUID" \
    --strict-mcp-config --dangerously-skip-permissions < "$ASK_FOR_PRIOR_MARKER_PROMPT"
```

## Declared capabilities

- **writes-tree:** yes.
- **resumable:** yes. A continuation uses `--resume` with the lane's session id;
  `--session-id` is used only to create the first session.
- **write confinement: NOTHING beyond the boundary snapshot**, the same statement the pi
  adapter makes. The harness has a restricted mode, but it removes the shell tool a builder
  needs, so this adapter does not use it.
- **structured report:** the `RESULT:` footer is plain text at the end of the final answer.
- **usage:** the stream's own; cost is an estimate (below).
- **standing instructions:** an account administrator's organization-level instructions
  reach a headless run, and no flag removes them, so a worker on this backend works under
  them and a worker on pi does not.

## The dialect

The stream is one JSON object per line:

| Line | What the extractor reads from it |
| :--- | :--- |
| `{"type":"system","subtype":"init",...}` | `session_id` and `model`, used when there is no `result` line |
| `{"type":"assistant","message":{"id":...,"content":[...],"usage":{...}},"timestamp":...}` | the `text` blocks, the usage once per message id, the timestamps |
| `{"type":"user",...}`, `{"type":"rate_limit_event",...}`, other `system` lines | counted as events, nothing else |
| `{"type":"result","result":...,"session_id":...,"total_cost_usd":...,"usage":{...}}` | the final text, the session id, the cost, the run's usage |

- **Text** is the `result` line's `result`. A stream cut off before that line yields the
  `assistant` lines' text blocks in order, so what arrived is still in the report.
- **Tokens** are the `result` line's usage: input, output, cache read and cache write
  added together. Without a `result` line they are the `assistant` lines' usage counted
  once per message id, because one message can arrive on several lines and each repeats
  the same usage.
- **Cost** is `total_cost_usd`. It is the harness's estimate at list prices; a
  subscription is not billed per run. A stream with no `result` line says `unknown`.

## Falling back when the subscription refuses

A run the subscription refuses exits non-zero and writes no `RESULT:` footer, which the
drain classes as an infrastructure failure, the one class a failover fires on. A seat that
names a list of backends in the policy, such as `"build": ["claude", "pi"]`, then re-runs
the item on the next backend in its own list in the same pass. The work machine's
Claude-first policy,
[`config/tillr/backend-policy.claude.json`](../../../../config/tillr/backend-policy.claude.json),
is set up that way. Its Bedrock-only policy, `backend-policy.bedrock.json`, names no claude
backend, so a drain reaches this adapter only when it is started with the Claude-first file.

## What a live run needs

A machine with `claude` installed and signed in to the subscription, a model and effort in
the backend policy, and a lane staged as [the pi README](../pi/README.md#stage-1-end-to-end)
describes. Then, from `$LANE/worktree`:

```
tools/tillr-run/backends/claude/run-claude \
    --backend claude \
    --model "$(jq -r '.backends.claude.model' config/tillr/backend-policy.claude.json)" \
    --effort "$(jq -r '.backends.claude.effort' config/tillr/backend-policy.claude.json)" \
    --brief "$LANE/brief.md" --target "$LANE/worktree" \
    > "$LANE/<run-id>.log"
```
