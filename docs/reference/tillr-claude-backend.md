---
title: "The Claude Code backend: headless runs on a subscription, and waiting when its window is spent"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/tillr-run/backends/claude/
  - tools/tillr-run/run/request.go
  - tools/tillr-drain/drain/backend_availability.go
  - config/tillr/backend-policy.claude.json
  - config/tillr/backend-policy.mixed.json
tags:
  - tillr
  - backend
  - reference
summary: "The run-claude adapter lets the backend policy put any seat on the Claude Code command line run headless (claude -p), which is the only way to spend a Claude subscription from a drain. This reference states what a headless run loads by default, what the adapter always passes, how a backend row carries harness arguments for tools, servers and prompt text, and how a subscription quota command with an optional owner reserve closes a backend to new dispatches."
vantage:
  status-chip: true
---

# The Claude Code backend: headless runs on a subscription, and waiting when its window is spent

**Status:** Verified 2026-11-24 against the tree. MEASURED: the adapter's defaults
([What a headless run loads](#what-a-headless-run-loads)), a dispatch and landing in a scratch
clone ([Scratch drain measurements](#scratch-drain-measurements)), and the quota command
against a live subscription
([Direct readings and the owner's reserve](#direct-readings-and-the-owners-reserve)).

A **backend** is an adapter program plus a row in the backend policy
([the backend adapter reference](./tillr-muse-backend.md#adapter-contract-and-write-boundary)).
This backend is the adapter for Claude Code run headless, plus two things a policy row
carries for it: extra arguments for the harness, and a quota command whose reading closes
the backend to new work before the subscription refuses. A Claude subscription cannot be
spent through pi; the headless Claude Code command line is the only way to spend it from a
drain. The subscription is spent in windows, so the drain knows when a window is shut and
waits for it.

| Component | Lives in |
| :--- | :--- |
| Run adapter, report extractor, fixtures | [`tools/tillr-run/backends/claude/`](../../tools/tillr-run/backends/claude/) (`run-claude`, `extract-report`) |
| Delivery call server and hooks | `tools/tillr-run/backends/claude/landing` |
| Subscription quota command | `tools/tillr-run/backends/claude/quota` |
| Harness arguments on a run request | `tools/tillr-run/run` (`harness_args`) |
| Quota admission and the waiting view | [the drain's reference](../../tools/tillr-drain/README.md#backend-quota-admission) |
| Policies that route seats here | [`config/tillr/backend-policy.claude.json`](../../config/tillr/backend-policy.claude.json), [`config/tillr/backend-policy.mixed.json`](../../config/tillr/backend-policy.mixed.json) |

Neither policy file sets `quota` or `reserve`; opting a row in is the owner's configuration
choice.

**Not built:** `harness_args` for the pi adapter. `tools/tillr-run/backends/pi/run-pi` refuses
an argument it does not know, as every adapter does.

**Reads with:** [the backend adapter reference](./tillr-muse-backend.md) (what a
backend is and the report shape), [the convergence design](../design/tillr-convergence.md) (where
models are named), [the judge and lifecycle design](../design/tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27)
(why a refused run waits on its own backend), and
[the adapter's README](../../tools/tillr-run/backends/claude/README.md).

---

## What a headless run loads

Each row is one headless run of Claude Code 2.1.325, model alias `haiku`, effort `low`, brief
on standard input, output as a stream of JSON lines (`--output-format stream-json --verbose`).

| Run | What it loaded | Prompt tokens on the first turn |
| :--- | :--- | :--- |
| Built-in tools removed (`--tools ""`), nothing else | 20 connector servers from the account and the environment, all their tools, 0 skills | 92,263 |
| Connector servers off (`--strict-mcp-config`) | 26 built-in tools, 0 servers, 53 skills | 29,084 |

Five facts the adapter rests on:

1. **Removing built-in tools does not remove connector tools.** Only `--strict-mcp-config` does,
   and it cuts the prompt by about two thirds.
2. **The stream ends with one `result` line** carrying the final text, the session id,
   `total_cost_usd`, and one `usage` object for the whole run. Each `assistant` line also carries
   usage, and a message that arrives in two lines repeats it, so summing `assistant` lines counts
   twice.
3. **The stream reports the subscription's windows.** A `rate_limit_event` line carries the
   five-hour and seven-day use as fractions and the time each resets.
4. **A headless run cannot ask for permission.** Without `--dangerously-skip-permissions` every
   write is refused.
5. **Account-level administrator instructions reach a headless run.** No flag removes them, so a
   worker on this backend works under them and a worker on pi does not.

## Scratch drain measurements

The dispatch-and-landing path is measured in a scratch clone with its own store and one small
build item whose committed test fails before the work and passes after it. The loop runs the
Claude-first policy with the real gate adapter and the real lander, and with rolling checks and
base replay off, so it is not the repository's full landing gate. With `ROOT` naming the
scratch clone, `STORE` its store, and `SCRATCH` a directory holding an executable `probe-gate`
that runs the item's test:

```sh
tillr-drain loop --exit-when-drained --root "$ROOT" --store "$STORE" \
  --policy config/tillr/backend-policy.claude.json --lanes 1 \
  --gate "tools/tillr-loop/tillr-loop gate --root $ROOT --store $STORE --gate-program $SCRATCH/probe-gate" \
  --land "tools/tillr-loop/tillr-loop land --root $ROOT --store $STORE" \
  --verdict "tools/tillr-loop/tillr-loop verdict --root $ROOT --store $STORE" \
  --questions-cmd "uvx vantage-check index --format json" \
  --rolling '' --replay ''
```

The `--gate` string is split on whitespace, not by shell quoting, which is why the gate
program is one executable path.

Result: one Claude run on policy alias `opus` builds and lands the item, the loop exits 0
with `queue-drained`, and `tillr-store check` reports `integrity: ok`. The archived report
names the resolved model, the token total, the harness's cost estimate, the rate-limit
window with its fraction and reset, `complete: yes` and `result: done`. With a `claude` on
`PATH` that exits 1 and writes nothing, the extractor's report says exit code 1, zero stream
lines, `complete: no` and `result: missing`, and the drain classes the run as an
infrastructure ending.

## The adapter

The [Claude adapter directory](../../tools/tillr-run/backends/claude/) holds the run adapter,
extractor, fixtures, and a separate subscription quota command. Its directory is named
for the harness, as the other adapters are; policy rows own model choices.

**What it runs,** from the lane's worktree:

```
claude -p --output-format stream-json --verbose \
    --model <policy model> --effort <policy effort> \
    --session-id <the lane's session id> \
    --strict-mcp-config --dangerously-skip-permissions \
    <the row's harness arguments> < brief.md
```

- **The brief travels on standard input**, so its size is not bounded by the argument list.
- **`--strict-mcp-config` is always passed.** A run sees only the servers the adapter or its row
  names with `--mcp-config`. A worker with an account's mail, calendar and chat connectors is a
  worker that can act outside the repository, and nothing in a brief needs them.
- **`YOLO_NO_LAUNCH_FLAGS=1` is exported and the permission flag is passed by the adapter**, so
  the argument list is the same whether or not a launcher wraps `claude`.
- **The lane markers are exported** exactly as `run-pi` exports them, along with the recording
  variables, so an extension can tell a lane's worker from a person's session and the run's
  shell and git calls are recorded.
- **Refusals** (exit 2, nothing started) are the shared ones: wrong backend name, no model, no
  effort, a brief that fails preflight, a target that is not the working directory, no `claude` on
  `PATH`. `claude` and `claude-<role>` are both accepted as the backend name, as with pi.
- **Write confinement: nothing beyond the boundary snapshot**, the same statement the pi adapter
  makes. The harness has a restricted mode, but it removes the shell tool a builder needs.

**What the extractor reads.** The text is the `result` line's `result` when the stream has one,
otherwise the text blocks of the `assistant` lines in order, so a stream cut off before its end
still yields what arrived. Tokens are the `result` line's usage (input, output, cache read and
cache write added together); without a `result` line they are the `assistant` lines' usage counted
once per message id. Cost is `total_cost_usd`, and it is an estimate at list prices: a subscription
is not billed per run. The session id is the `result` line's, or the `init` line's.

## Controlling what a seat may use

A Claude seat is controlled the way a pi seat is: which tools exist, which are refused, what
custom tools it gets, which skills, what standing text. Pi takes all of that from the machine's
pi profile. Claude Code takes it from arguments, so the policy row carries them.

**A backend row carries `harness_args`, a list of strings staging appends to the
adapter's argument list after a `--` separator, and the adapter hands them to the harness
unchanged.** The policy holds data and the adapter holds spelling: tillr does not invent one
permission vocabulary for four harnesses.

| Wanted | Harness argument |
| :--- | :--- |
| Remove built-in tools | `--tools "Read,Edit,Bash"` (only these exist) |
| Refuse one tool or one command shape | `--disallowed-tools "WebSearch" "Bash(git push *)"` |
| Add custom tools | `--mcp-config <file in the repository>` |
| No skills or slash commands | `--disable-slash-commands` |
| Extra skills | `--plugin-dir <directory in the repository>` |
| Standing text for the seat | `--append-system-prompt <text>` |
| No user or project settings | `--setting-sources ""` |

Paths in these arguments are relative to the lane's worktree, which is a checkout of the
repository, so a file the row names is versioned with the row. An adapter that takes no harness
arguments refuses them, as every adapter refuses an argument it does not know.

**Content is prepared the same way.** The brief is the same template every backend gets. The
worktree carries the repository's own instruction files, and Claude Code reads them as pi reads its
own; nothing is copied into a second place.

## When the window is spent

A run the subscription refuses exits nonzero and writes no `RESULT:` footer. **The run waits
for its own backend's window, and no other backend is tried**
([`D-JL24` and `D-JL26`](../design/tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27)).
A seat names one backend, and moving a seat to another backend is a policy edit by a person.

- **A run that did work continues in place.** For a single-worker item whose refusing run
  changed its head or left uncommitted paths, the loop records the pause in `quota_pauses`,
  keeps the lane and worktree, and resumes under the same run id and attempt when the backend
  reopens.
- **A first-message refusal that left no work** waits for the same backend's window and starts
  a new run.
- **A recorded refusal pauses sibling live runs on that backend.** Quota readings and reserves
  do not stop running sessions.

[The drain's reference](../../tools/tillr-drain/README.md#backend-quota-admission) owns the
full behavior.

### Direct readings and the owner's reserve

A backend can opt into quota admission with `quota`, a command argument list, and
`reserve`, the fraction of each window left unused for the owner's sessions. Reserve
defaults to 0 and accepts 0 through 1. A row without a quota command is never probed
and is never held by a reading or reserve. A run's refusal still holds its backend
until reset, whether or not it has a quota command.

```json
{
  "adapter": "tools/tillr-run/backends/claude/run-claude",
  "model": "opus",
  "effort": "medium",
  "quota": ["tools/tillr-run/backends/claude/quota"],
  "reserve": 0.2
}
```

The quota command runs from the drained repository root, not a lane worktree. It
answers one JSON document with nonempty `windows`: each has a unique `kind`, a
nonnegative fraction used, and an absolute `reset_at`. Fractions above 1 are accepted
as overuse, not clipped. Only an idle window with fraction 0 may have a null reset.

Before planning new work, the loop refreshes readings older than **60 seconds**.
Changing a command or reaching any reported reset also forces a refresh. The planner
only reads stored observations; it never runs commands. A configured backend without a
fresh successful reading is held. When any fraction reaches `1 - reserve`, new
dispatches for that backend wait. With reserve 0.2, 0.8 is closed and 0.79 is open.
Changing reserve takes effect on the next pass without another HTTP request.
Admission is checked while choosing and claiming a backend, never after staging.
A claimed dispatch always starts. The reserve is a threshold on the last reading,
not a guarantee: work started within one reading interval may overshoot it.

Each probe, successful or failed, appends a dated reading to the store. A separate
latest-reading row supplies planning and the waiting view. Restarting the loop reuses
a fresh reading. Errors hold only the affected backend until a successful retry;
they never mean unlimited quota. Commands have a 15-second deadline and a
64-KiB output cap, run without a store transaction held, and do not retain stderr.
The waiting face shows both window fractions, reserve, and the latest reopening time
among closed windows, or an explicit unknown time for a failed or stale reading. Open
backends are shown too. Removing a command removes its live observation on the next
pass and keeps its history.

**How the Claude reading works and costs.** The command makes one authenticated
`GET https://api.anthropic.com/api/oauth/usage`, using the issued subscription
access token and the `anthropic-beta: oauth-2025-04-20` header. It reads the token from
`CLAUDE_CODE_OAUTH_TOKEN`, or Claude Code's secure-storage directory (then
its configuration directory, then its default home directory). It does not refresh
credentials or invoke a model. HTTP failures and redirects are refused.
The response's utilization percentages become fractions; its five-hour and seven-day
reset times are normalized to UTC.

Measured against a live subscription: one reading is one HTTP GET and starts no Claude
process, so it costs **0 model tokens and no model inference charge**. An idle window
answers fraction 0 with a null reset, and the command preserves the null. A missing or
expired token is an explicit error; the credential's owner signs in or refreshes through
Claude Code before a later probe succeeds.

## Why it's this way

| ID | Ruling | Why |
| :--- | :--- | :--- |
| `D-CB1` | The adapter always passes `--strict-mcp-config` | Measured: connector servers triple the prompt and let a worker act outside the repository |
| `D-CB2` | A backend row carries `harness_args`, passed unchanged after `--` | The policy holds data and the adapter holds spelling; one permission vocabulary across harnesses is not designed |
| `D-CB3` | A seat names one backend and nothing walks a list; [`D-JL24`](../design/tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) owns the rule | A failure is fixed, not routed around |
| `D-CB4` | A run the subscription refuses waits for that backend's own window; [`D-JL26`](../design/tillr-judge-and-lifecycle.md#415-no-automatic-move-to-another-model-d-jl24-to-d-jl27) owns the rule | Quota exhaustion is not an exception to `D-JL24` |
| `D-CB5` | Cost on this backend is recorded as the harness's estimate | The harness reports list-price dollars; a subscription is not billed per run |
