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
| [`testdata/complete.jsonl`](./testdata/complete.jsonl) | A complete stream, written by hand to the measured line shapes, ending in a `result` line whose text ends in the `RESULT:` footer. |
| [`testdata/truncated.jsonl`](./testdata/truncated.jsonl) | The same stream cut off before its footer and its `result` line — the negative control. |

The fixtures carry invented ids and no account data. They were written from the line
shapes measured on Claude Code 2.1.325, not captured from a run.

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
  `BASH_ENV`, `GIT_TRACE2_EVENT`, `GIT_TRACE2_PARENT_SID`, `TILLR_TRACE_DIR` and
  `TILLR_REPO_ROOT` are exported, all fail-open. The table is in
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

## Declared capabilities

- **writes-tree:** yes.
- **resumable:** no. Re-brief in the same worktree, as
  [the muse design's section 5](../../../../docs/design/tillr-muse-backend.md#5-steering) rules.
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
