---
title: "Tillr's native agent backend"
status: in-review
stage: DESIGN
updated: 2026-11-26
tags:
  - tillr
  - run
  - backends
  - agent
  - design
summary: "Checks the tracked tree and Git history for an existing native agent backend design (none exists), and specifies a first-party Go agent backend under tools/tillr-run/backends/native/ and tools/tillr-run/native/ that runs model conversations and tools directly over HTTPS without an external agent CLI. Covers what the backend is and is not, how it meets every part of the tillr-run adapter contract, which provider interface to speak first (OQ-NAB1), the eight-tool set and three-layer lane-fence and read-only enforcement, call-time credentials in and out of yolo jails (OQ-NAB2), what tillr can drop from the pi fork (OQ-NAB3), rejected alternatives, and six ordered build slices starting with a single-item adapter-and-loop slice."
next: "Rule OQ-NAB1 (first provider wire protocol), OQ-NAB2 (how provider credentials reach native runs inside a yolo jail), and OQ-NAB3 (whether run-pi retires after native parity), review D-NAB1 to D-NAB6 in the Decision Ledger, and file Slice 1 of Section 8."
depends-on:
  - ../reference/tillr-muse-backend.md
  - ../reference/tillr-claude-backend.md
vantage:
  status-chip: true
---

# Tillr's native agent backend

**Status:** Not built on 2026-11-26: no existing design for a first-party agent backend exists in tracked documents or Git history ([§1.1](#11-search-of-tracked-documents-and-git-history)), `ls tools/tillr-run/backends` lists only `agy`, `claude`, `muse`, and `pi`, and no Go or shell files change in this commit. Only `R-NAB1` in the [Decision Ledger](#decision-ledger) quotes the owner's words; the remaining scope in [§1](#1-search-result-standing-request-and-what-the-backend-is-and-is-not) comes from the 2026-11-26 operator request; [`OQ-NAB1`](#OQ-NAB1), [`OQ-NAB2`](#OQ-NAB2), and [`OQ-NAB3`](#OQ-NAB3) await the owner's ruling; and `D-NAB1` through `D-NAB6` are the designer's decisions, which the owner may overturn ([decision provenance review §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening)).

> **In short.** Give [`tools/tillr-run`](../../tools/tillr-run/README.md) a native backend (`tools/tillr-run/backends/native/`) compiled into `tillr-run` (`CGO_ENABLED=0`, standard-library `net/http` + SSE) that runs the model conversation, session transcript, and worker tools directly against model providers without `pi`, Node.js, or an external CLI harness in between. Keeping `run-native` behind the same `tillr-run exec` process-and-record seam lets seat profiles mix `native` and `claude` per seat, turns `land` / `obstacle` / `no_change` suspension and turn-gap pauses into plain Go exits with no `--features` probe or `SIGUSR1` wrapper, enforces `fence.txt` and `read_only` seats *before* bytes touch disk, and lets tillr drop `mschulkind-oss/tillr-pi` and its rolling and extension machinery.

**Why it matters.** Today every API-key seat runs through [`run-pi`](../../tools/tillr-run/backends/pi/run-pi), which requires a custom Node.js fork (`mschulkind-oss/tillr-pi`) for `parked-tool-calls`, probes `pi --features`, loads four TypeScript extensions, stages per-revision agent directories (`pi-build-agent-dir`), coordinates turn-gap pauses over `SIGUSR1` (`pi-stop-at-gap`), and offers zero pre-write confinement inside its file tools. Owning the loop in Go deletes that entire bridge while keeping external CLI adapters (`run-claude` for flat-rate Claude Code subscriptions) side by side.

**The shape.** Confirm the document search and define the boundary in [§1](#1-search-result-standing-request-and-what-the-backend-is-and-is-not), map every piece of the `tillr-run` adapter contract in [§2](#2-how-it-fits-the-adapter-contract), compare first provider wire protocols in [§3](#3-which-provider-interface-it-speaks-first), specify the eight-tool set and three-layer confinement in [§4](#4-tool-set-lane-fence-and-read-only-seats-without-an-outside-harness), address credentials in [§5](#5-credentials-which-tillr-does-not-hold-in-this-environment), list what tillr drops in [§6](#6-what-it-lets-tillr-drop) and what is rejected in [§7](#7-what-is-rejected-and-why), and sequence six build slices in [§8](#8-build-slices-smallest-first).

**Cost.** Six additive slices centered in `tools/tillr-run/native/`, `tools/tillr-run/extract/`, and `tools/tillr-run/backends/native/` (`CGO_ENABLED=0`, zero third-party SDK dependencies), leaving `tillr-drain` settlement, `run-claude`, and existing backend policies unchanged.

**Start at:** [§2](#2-how-it-fits-the-adapter-contract) for the adapter seam, [§4](#4-tool-set-lane-fence-and-read-only-seats-without-an-outside-harness) for fenced tools and read-only seats, and [§8](#8-build-slices-smallest-first) for the build slices.

**Needs your ruling:** [`OQ-NAB1`](#OQ-NAB1) (which provider wire interface `run-native` speaks first), [`OQ-NAB2`](#OQ-NAB2) (how provider credentials reach `run-native` inside a `yolo` jail), and [`OQ-NAB3`](#OQ-NAB3) (whether `run-pi` retires once `run-native` proves parity across seats or stays as an optional stock-`pi` adapter).

**Awaits your review:** the six designer's decisions `D-NAB1` to `D-NAB6` in the [Decision Ledger](#decision-ledger). You may overturn any of them.

**Reads with:** [the multi-model adapter reference](../reference/tillr-muse-backend.md) (adapter contract, report keys, and failover classes), [the Claude backend reference](../reference/tillr-claude-backend.md) (why Claude Code subscriptions require `run-claude` and how `claude/landing.go` parks calls), [the landing call](./tillr-landing-call.md) (`call-suspends`, `landing-offer.json`, and `landing-answer.json`), [lane shims](./tillr-lane-shims.md) (preventative command shims and `CLONE_NEWNS` mounts), [seat profiles](./tillr-seat-profiles.md) (per-user backend routing across seats), [installing tillr without a `yolo` jail](./installing-tillr-outside-yolo.md) (`<prefix>/share/tillr` layout and [`OQ-HI2`](./installing-tillr-outside-yolo.md#OQ-HI2)), and [utility contracts](./tillr-utility-contracts.md) (`D-UC21` and `D-UC22`).

**Words.** Item, node, run, session, seat, lane, fence, verdict, and charter keep their meanings in [the vocabulary](./tillr-vocabulary.md#5-the-words); adapter, dialect, and attempt class keep their meanings in [the multi-model adapter reference](../reference/tillr-muse-backend.md); seat profile keeps its meaning in [seat profiles](./tillr-seat-profiles.md#2-boundary-and-vocabulary-what-a-seat-profile-is-and-is-not). Three terms are used with exact meanings here:

- **Native agent backend** *(coined here)*: the built-in adapter at `tools/tillr-run/backends/native/run-native` and its Go engine in `tools/tillr-run/native/` (`tillr-run native-agent`), which streams model turns directly over HTTPS and executes worker tools in-process or in supervised child processes with no external coding-agent CLI.
- **Provider wire adapter** *(coined here)*: a small `net/http` + SSE implementation of `native.Provider` in `tools/tillr-run/native/` that translates tillr's provider-neutral turn and tool messages to and from one HTTP API (`POST /v1/messages`, `POST /v1/chat/completions`, or Bedrock `ConverseStream`).
- **Pre-write target gate** *(coined here)*: the physical-path check (`CheckWriteTarget`) inside the native `write_file` and `edit_file` tools that validates `<workDir>`, `fence.txt`, `config/tillr/deny-list.json`, `read_only` access, and wired output directories *before* opening or creating a file on disk.

---

## 1. Search result, standing request, and what the backend is and is not

### 1.1 Search of tracked documents and Git history

On 2026-11-26 the owner asked (`R-NAB1`):

> *"start building our native agent backend that we should have in a doc somewhere."*

Searching tracked documents, module READMEs, and full repository history at `2527c9ca9a42` confirms **no prior design document exists under any name**:

- `rg -i 'native.{0,20}(agent|backend|harness)|first-party.{0,20}(agent|backend|harness)|own agent|provider adapter|built-in.{0,15}(agent|harness)' docs/ tools/ README.md AGENTS.md` returns no matches.
- `git log --all --full-history --stat -- 'docs/**'` contains no active, graduated, or deleted file describing a native or first-party agent loop in `tillr`.
- Every existing backend document — [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md), [`tillr-claude-backend.md`](../reference/tillr-claude-backend.md), [`tillr-utility-contracts/tillr-run.md`](./tillr-utility-contracts/tillr-run.md), [`installing-tillr-outside-yolo.md`](./installing-tillr-outside-yolo.md#33-the-patched-pi-fork-and-coding-agent-harnesses), and [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) (entries 4 and 6) — assumes an external CLI harness (`pi`, `claude`, `agy`, `muse`) invoked by a POSIX shell wrapper.

This document supplies that missing design and stops short of code changes, per the 2026-11-26 operator request.

### 1.2 What the native agent backend is

*Designer's decision (`D-NAB1`); the owner may overturn this.*

1. **A sibling backend under [`tools/tillr-run/backends/`](../../tools/tillr-run/backends/), preserving the process-and-record boundary.** `tillr-drain` stages `brief.md`, `fence.txt`, `base_sha`, `session-id`, and `ending-capability` exactly as today ([`stage.go`](../../tools/tillr-drain/drain/stage.go)), and calls `tillr-run exec` ([`exec.go`](../../tools/tillr-run/run/exec.go)). `Exec` spawns `tools/tillr-run/backends/native/run-native` in its own process group (`Setpgid: true`) with `TILLR_RUN_EXEC_ID`, which runs `preflight_brief` ([`preflight.sh`](../../tools/tillr-run/backends/preflight.sh)) and `exec`s `"$TILLR_RUN_BIN" native-agent`. When the child exits, `Exec` runs `tools/tillr-run/backends/native/extract-report` (`"$TILLR_RUN_BIN" extract-report --dialect native`, honoring `D-UC22` in [`tillr-utility-contracts.md`](./tillr-utility-contracts.md#87-the-backend-report-extractors-move-to-go)) and writes one `run.record`.
2. **A pure-Go (`CGO_ENABLED=0`) turn-and-tool loop inside `tools/tillr-run/native/`.** It reads the staged brief (or resume prompt), sends conversation history and JSON Schema tool definitions directly to a model provider over HTTPS SSE, executes file/search/shell/progress/ending tools itself, appends canonical JSONL state to `<artifactDir>/sessions/<session-id>.jsonl` and live JSONL events to stdout (`<run-id>.log`), and exits cleanly when settled, parked, or paused.
3. **Seat-selectable alongside other backends.** A seat profile ([`tillr-seat-profiles.md`](./tillr-seat-profiles.md)) can route `scout`, `classify`, `verify`, or `build` to `run-native` while keeping another seat on [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) or [`run-pi`](../../tools/tillr-run/backends/pi/run-pi), and seat failover ([`failover.go`](../../tools/tillr-drain/internal/policy/failover.go)) works unchanged across them.

### 1.3 What the native agent backend is not

- **Not an interactive terminal UI or human chat client.** Interactive whale and operator sessions continue to use whatever interactive CLI (`pi`, `claude`) the operator prefers. `run-native` runs non-interactive brief-driven worker, scout, planner, reviewer, evaluator, and triage turns.
- **Not an in-process goroutine inside `tillr-drain`.** Running HTTP streams or `bash` tools inside `tillr-drain` would cross the process-and-record boundary ([`board-utilities.md`](./board-utilities.md)) and give up per-run process groups, `/proc` child cleanup, and Linux `CLONE_NEWNS` mount isolation ([§7](#7-what-is-rejected-and-why)).
- **Not a way to spend a flat-rate Claude Code subscription.** As documented in [`tillr-claude-backend.md`](../reference/tillr-claude-backend.md), Anthropic's subscription quota can only be spent through the headless `claude` CLI; `run-claude` stays for subscription seats, while `run-native` speaks direct provider APIs.
- **Not a secret store, plugin host, or subagent orchestrator.** Tillr holds no credentials in `.tillr/` or tracked files ([§5](#5-credentials-which-tillr-does-not-hold-in-this-environment)), loads no `.ts`/`.js` extensions or MCP servers, and delegates multi-step decomposition to tillr's own workflow graphs ([`config/tillr/workflows/planned-build@v1.json`](../../config/tillr/workflows/planned-build@v1.json)) rather than hidden nested subagents inside one worker process.

---

## 2. How it fits the adapter contract

*Designer's decision (`D-NAB1`); the owner may overturn this.*

Every file and flag [`Backend.Request`](../../tools/tillr-run/run/request.go), [`Exec`](../../tools/tillr-run/run/exec.go), [`extract.RunCLI`](../../tools/tillr-run/extract/report.go), and `tillr-drain` expect from an adapter under `tools/tillr-run/backends/<name>/` is met without special-casing `tillr-drain`:

| Contract surface | How `pi` / `claude` meet it today | How `backends/native/` meets it |
| :--- | :--- | :--- |
| **Adapter argv** ([`request.go`](../../tools/tillr-run/run/request.go)) | `run-pi --backend … --model … [--effort …] [--account …] [--timeout …] [-- …] <brief>` | `run-native` parses the identical flags via [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh), validates `preflight_brief`, and `exec`s `"$TILLR_RUN_BIN" native-agent` |
| **Run record** ([`exec.go`](../../tools/tillr-run/run/exec.go)) | `Record` + `$TILLR_HARNESS_RECORD_FILE` (`pi/…`) + `$TILLR_EXTENSION_SET_RECORD_FILE` | Writes `tillr-native/<revision>` to `$TILLR_HARNESS_RECORD_FILE`; leaves `extension-set` absent (`Record.ExtensionSet == ""`) because no external extensions exist |
| **Session resume** ([`exec.go`](../../tools/tillr-run/run/exec.go), [`pause.go`](../../tools/tillr-run/run/pause.go)) | `<artifactDir>/session-id` UUID + `TILLR_SESSION_MODE` + `TILLR_RESUME_PROMPT_FILE` | Stores provider-neutral JSONL at `<artifactDir>/sessions/<session-id>.jsonl` (outside `<workDir>`); reopens on `resume`, repairs any interrupted tool call, and appends resume/refusal input |
| **Parked tool calls & `--features`** ([`landing_call.go`](../../tools/tillr-drain/drain/landing_call.go), [`ending_capability.go`](../../tools/tillr-run/run/ending_capability.go)) | `pi` requires `mschulkind-oss/tillr-pi` + `probePiEndingCapability` (`pi --features`); `claude` probes `claude -p --continue` | `backends/native/ending-capability` declares `call-suspends`; `Loop.recordEndingCapabilities` reads that file directly with **zero subprocess `--features` probe**, and `native-agent` parks in Go |
| **Stop at a turn gap** ([`pause.go`](../../tools/tillr-run/run/pause.go)) | `pi-stop-at-gap` polls `$TILLR_PAUSE_REQUEST` + SQLite task journal and sends `SIGUSR1` | `backends/native/pause-kind` declares `stops-at-a-gap`; `native-agent` checks `$TILLR_PAUSE_REQUEST` directly after tool results flush and before the next HTTP turn, exiting `0` cleanly |
| **Usage & cost** ([`exec.go`](../../tools/tillr-run/run/exec.go), [`report.go`](../../tools/tillr-run/extract/report.go), [`usage.go`](../../tools/tillr-run/extract/usage.go)) | `<adapter>/extract-report` writes 19-key `report.md` + `report.md.usage.json` | `backends/native/extract-report` calls `tillr-run extract-report --dialect native`, reading `NativeDialect` token/cache/thinking/latency/cost receipts into the identical 19-key `report.md` and `.usage.json` |
| **Quota & timeouts** ([`quota_window.go`](../../tools/tillr-drain/internal/policy/quota_window.go), [`progress.go`](../../tools/tillr-run/run/progress.go)) | `rate_limit_event` -> `report.md` header; `message_end` `stopReason: "error"` -> `run.model-timeout`; `Exec` `runCtx` wall | Maps HTTP `429` window headers directly to `rate_limit_event` (`status: "rejected"`, `reset` epoch); emits `message_end` timeout errors on SSE stalls; relies on `Exec` `runCtx` for `--timeout` walls |

### 2.1 Run record and stream events

`native-agent` writes structured one-JSON-object-per-line events to stdout (`<artifactDir>/<run-id>.log`), which `Exec` opens in truncate mode on fresh runs and append mode on `Resume` (`openRunLog` in [`exec.go`](../../tools/tillr-run/run/exec.go)). Using the event names [`settled.go`](../../tools/tillr-run/run/settled.go) and [`progress.go`](../../tools/tillr-run/run/progress.go) already observe (`session`, `agent_start`, `message_start`, `message_end`, `tool_execution_start`, `tool_execution_end`, `parked`, `loop_detected`, `rate_limit_event`, and `agent_settled`) lets `settledMonitor` and `workerRecords` work with zero forks in `tools/tillr-run/run/`. Unlike `pi`, `native-agent` exits `0` immediately after emitting `agent_settled` or `parked`, so `settledMonitor`'s five-sample `SIGKILL` straggler reaper never has to fire on healthy runs.

### 2.2 Session transcript and resume

Staging mints a UUID in `<artifactDir>/session-id` (`<laneDir>/session-id` for ordinary runs, `<laneDir>/nodes/<run_id>/session-id` for parallel `ReadOnlyGroup` readers in [`exec.go`](../../tools/tillr-run/run/exec.go)). `native-agent` keeps its canonical transcript at `<artifactDir>/sessions/<session-id>.jsonl`, outside `<workDir>` so `git status` stays clean:

- **`TILLR_SESSION_MODE=new`:** fails closed if `<session-id>` is missing or non-UUID; creates `<session-id>.jsonl` with a session header (`session_id`, `started_at`, `backend`, `model`, `effort`) and the initial user message (`$TILLR_RESUME_PROMPT_FILE` when `FreshSession` minted a handoff session in `prepareContinuationSession` of [`pause.go`](../../tools/tillr-run/run/pause.go), otherwise `brief.md`).
- **`TILLR_SESSION_MODE=resume` (covering both `Request.Resume` and `Request.SessionTurn`):** loads `<session-id>.jsonl` (refusing with exit `2` if missing or corrupt). Three cases follow:
  1. *Parked call answer:* when `landing-answer.json` exists in the run's scratch directory and matches the unanswered trailing `tool_use` `call_id`, `native-agent` appends the refusal `tool_result` (`is_error: true`, containing the gate/verify refusal plus any `SessionTurn` continuation text from `$TILLR_RESUME_PROMPT_FILE`), deletes `landing-answer.json`, and starts the next model turn.
  2. *Interrupted tool call (crash or straggler `SIGKILL`):* if the transcript ends with `tool_use` blocks lacking matching `tool_result` blocks and no `landing-answer.json` exists, `native-agent` appends synthetic `is_error: true` `tool_result` blocks (`"Tool execution was interrupted before completion; inspect state before retrying."`) so strict provider `tool_use -> tool_result` alternation never fails with HTTP `400`, then appends `$TILLR_RESUME_PROMPT_FILE` as a `user` message.
  3. *Clean turn-gap resume:* appends `$TILLR_RESUME_PROMPT_FILE` as a `user` message and continues.
- **Designer session compaction ([`designer.go`](../../tools/tillr-drain/drain/designer.go)):** `compactCommand` checks `backends/<name>/compact` and otherwise falls back to `resumed-expired`. Omitting `backends/native/compact` in early slices safely uses `resumed-expired`; a thin `backends/native/compact` summarization wrapper can be added in Slice 6 with zero `tillr-drain` edits.

### 2.3 Parked tool calls without `--features`

[`Loop.recordEndingCapabilities`](../../tools/tillr-drain/drain/landing_call.go) runs `probePiEndingCapability` (`pi --features`) *only* when `filepath.Base(filepath.Dir(path)) == "pi"` and `probeClaudeEndingCapability` *only* when `filepath.Base(filepath.Dir(path)) == "claude"`. For every other adapter directory, it reads `<adapter_dir>/ending-capability` via [`ReadEndingCapability`](../../tools/tillr-run/run/ending_capability.go).

Because `native-agent` is compiled into `tillr-run` itself, `tools/tillr-run/backends/native/ending-capability` statically contains `call-suspends`:

- No external binary can drift behind `tillr-run`, so **no `--features` flag or startup subprocess probe exists**.
- When staging writes `footer` into `<laneDir>/ending-capability` (for `read_only` stages, plain-directory lanes in `plain_stage.go`, or `judgeCandidate` in `judge.go`), `native-agent` reads `<artifactDir>/ending-capability`, omits the suspending `land` tool, and requires a `RESULT:` footer instead — matching `run-pi` and `run-claude`.
- When `<artifactDir>/ending-capability` is `call-suspends`, calling `land`, `obstacle`, or `no_change` validates inputs locally (returning an immediate in-turn `is_error: true` tool result on local mistakes such as a dirty worktree or `HEAD == base_sha`), writes `landing-offer.json` and `landing-state.json` into the run's scratch directory using shared Go logic extracted from [`tools/tillr-run/claude/landing.go`](../../tools/tillr-run/claude/landing.go), flushes the unanswered `tool_use` to `<session-id>.jsonl`, emits `{"type":"parked","toolCallId":…}`, and exits `0`.

### 2.4 Stop at a turn gap, keep-going checks, and repeating-call recovery

- **Stop at a turn gap (`stops-at-a-gap`):** `waitForPause` ([`pause.go`](../../tools/tillr-run/run/pause.go)) writes `"pause\n"` to `$TILLR_PAUSE_REQUEST` when `run.pause-request` arrives. `native-agent` checks `os.Stat(os.Getenv("TILLR_PAUSE_REQUEST"))` after persisting all `tool_result` blocks for the current assistant message and again before opening the next HTTP request. When present, it flushes `<session-id>.jsonl` and exits `0` without emitting `agent_settled`, so `Exec` records `StatePaused` / `EndRestart` and writes `pause-notice` for seamless continuation. Because `native-agent` spawns no background shell daemons, it needs neither `SIGUSR1` nor `PI_BACKGROUND_TASK_JOURNAL`.
- **Keep-going & repeating-call loop detection (replacing [`keep-going.ts`](../../tools/tillr-run/backends/pi/keep-going.ts)):**
  - When an assistant turn ends with `stop_reason == "end_turn"` (no tool calls) and no parked offer: on a landing `write` run (`TILLR_NON_LANDING` unset and access `write`), `native-agent` checks `git status --porcelain` and `HEAD != base_sha` (unless the unchanged-piece marker is present), and in `footer` mode checks `extract.FooterResult(text) != ""`. Up to three bounded nudges are appended as user messages when work is uncommitted or lacks a footer before letting the run end.
  - Across consecutive tool calls, `native-agent` hashes `(toolName, canonicalJSON(args))`. At a streak threshold (default `4` identical calls), it injects one recovery warning and emits `{"type":"loop_detected","action":"recovered",…}`; if the model repeats the same call past the hard cap (default `6`), it emits `{"type":"loop_detected","action":"ended",…}` and exits `0`, which [`settled.go`](../../tools/tillr-run/run/settled.go) records as `EndRepeatingCall` (`AttemptRepeating` in [`failover.go`](../../tools/tillr-drain/internal/policy/failover.go)).

### 2.5 Usage, cost, quota windows, and timeouts

- **Usage and cost (`NativeDialect` in [`tools/tillr-run/extract/`](../../tools/tillr-run/extract/report.go)):** Each `message_end` stream event records `input`, `output`, `cacheRead`, `cacheWrite`, `reasoning`, `ttftMs`, `durationMs`, and `cost.total` (taking provider-reported USD when present, such as OpenRouter `usage.cost`, or computing from a versioned per-model rate table in [`usage.go`](../../tools/tillr-run/extract/usage.go); an unrecognized model with no provider-reported USD leaves `cost` unset so `report.md` writes `cost usd: unknown` rather than a false `0.000000`). `tillr-run extract-report --dialect native` renders the exact nineteen-key `report.md` and `report.md.usage.json` sidecar read by `readSpend` ([`exec.go`](../../tools/tillr-run/run/exec.go)).
- **Quota and rate-limit events:** Because `native-agent` owns the HTTPS response directly, it inspects HTTP `429` headers and structured error bodies first-hand:
  - Short burst throttling (`Retry-After <= 30s` with remaining window quota) sleeps with jittered backoff (honoring `ctx` and `$TILLR_PAUSE_REQUEST`) up to three retries inside the turn.
  - Window or spending-limit exhaustion (`anthropic-ratelimit-*-status: rejected`, OpenAI/OpenRouter `insufficient_quota` / `quota_exceeded`, or a reset timestamp more than 60 s in the future) emits `{"type":"rate_limit_event","rate_limit_info":{"rateLimitType":…,"utilization":1,"status":"rejected","resetsAt":<unix_sec>}}` and exits `1`. `extract-report` populates `rate limit kind`, `fraction: 1`, `status: rejected`, and `reset: <unix_sec>` in `report.md`, which [`RefusedWindow`](../../tools/tillr-drain/internal/policy/quota_window.go) reads to record `backend_windows`, pause peer runs in `quota_pauses`, and fail over open seats.
- **Model timeouts and run walls:**
  - Each provider HTTP call arms a response-header timeout (default `60s`) and an SSE chunk-idle timer (default `90s` without bytes or SSE keepalive comments). On a stall, `native-agent` cancels that request, emits `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"request body timeout"}}` — which [`observeModelTimeouts`](../../tools/tillr-run/run/progress.go) records as `run.model-timeout` — retries up to twice with backoff, and exits `1` (`AttemptInfrastructure`) if all retries stall.
  - Whole-run `--timeout` walls are enforced by `Exec`'s `runCtx` ([`exec.go`](../../tools/tillr-run/run/exec.go), matching `D-HI6` in [`installing-tillr-outside-yolo.md`](./installing-tillr-outside-yolo.md#35-remaining-external-interpreters-and-os-tools)), requiring no GNU `timeout` binary.

---

## 3. Which provider interface it speaks first

### 3.1 One internal turn interface, thin wire adapters (`D-NAB2`)

*Designer's decision (`D-NAB2`); the owner may overturn this.*

`tools/tillr-run/native/` separates the agent loop, session transcript, and tools from HTTP wire dialects behind one small Go interface:

```go
type Provider interface {
    StreamTurn(ctx context.Context, req TurnRequest, onDelta func(StreamDelta)) (TurnResponse, error)
}
```

`TurnRequest` and `TurnResponse` carry provider-neutral content blocks (`Text`, `Thinking` with opaque `Signature`/`Redacted` bytes so multi-turn reasoning round-trips losslessly, `ToolCall`, and `ToolResult` with `IsError`), explicit cache-breakpoint hints, `--effort` level (`minimal`, `low`, `medium`, `high`, `xhigh`, `max`), normalized token/cost receipts, and optional `WindowInfo` rate-limit facts. Every wire adapter is implemented directly on Go's standard `net/http`, `crypto/tls`, and a small UTF-8 SSE line scanner (`CGO_ENABLED=0`, zero vendor SDKs).

### 3.2 Choosing the first live wire protocol ([`OQ-NAB1`](#OQ-NAB1))

As noted in [`tillr-seat-profiles.md` §1](./tillr-seat-profiles.md#1-context-and-goal), the owner's seat profiles span four provider families: OpenRouter, AWS Bedrock, GitHub Copilot, and Claude/Anthropic. Which wire interface `run-native` speaks *first* decides which models and accounts the first live trial spends on, so [`OQ-NAB1`](#OQ-NAB1) leaves that choice to the owner:

| Candidate first wire interface | Endpoints reached immediately | Prompt caching, thinking & tool calls | Jail & host credential fit |
| :--- | :--- | :--- | :--- |
| **A. Anthropic Messages API (`POST /v1/messages` over SSE, configurable base URL)** | Direct Anthropic (`api.anthropic.com`), OpenRouter's Anthropic-compatible `/api/v1/messages` endpoint (reaching Claude, GPT, Gemini, DeepSeek, and Qwen with one key), and Anthropic-compatible proxies | **Strongest agent ergonomics:** explicit `cache_control: {"type":"ephemeral"}` on system + tools + rolling history prefix (`cacheRead`/`cacheWrite`), native `thinking` + `signature` blocks, `tool_result` `is_error: true`, and `anthropic-ratelimit-*` headers | Standard `ANTHROPIC_API_KEY` / `OPENROUTER_API_KEY` on hosts; needs jail credential wiring per [`OQ-NAB2`](#OQ-NAB2) (does not spend flat-rate Claude Code OAuth subscriptions) |
| **B. OpenAI Chat Completions (`POST /v1/chat/completions` over SSE, configurable base URL)** | OpenRouter's primary `/api/v1/chat/completions` endpoint (with `usage.cost` USD in stream), OpenAI direct (`api.openai.com`), GitHub Copilot chat endpoint (after token exchange), and local vLLM/LiteLLM servers | Universal aggregator support and inline USD cost on OpenRouter; caching is implicit on OpenAI (requires vendor-specific `cache_control` extensions for Anthropic-on-OpenRouter), and multi-turn reasoning blocks vary across vendors | Standard `OPENROUTER_API_KEY` / `OPENAI_API_KEY` on hosts; inside jails can pair with `openai-auth-broker` or [`OQ-NAB2`](#OQ-NAB2) |
| **C. AWS Bedrock `ConverseStream` (`POST /model/{id}/converse-stream` with SigV4)** | AWS Bedrock models (Claude, Nova, DeepSeek, Llama on Bedrock) | Clean unified tool/thinking/cache blocks across Bedrock models; requires pure-Go AWS SigV4 signing and `application/vnd.amazon.eventstream` binary framing (~350 extra LoC) | **Already credentialed in this jail today:** `yolo`'s `aws-auth` loophole already populates `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` for `tillr-run`; less convenient for outside-jail laptop users who hold a single API key |

Whichever protocol [`OQ-NAB1`](#OQ-NAB1) picks first, the other two fit behind `native.Provider` as ~250–400-line sibling files in `tools/tillr-run/native/` without changing session files, tools, fences, or `extract-report`, selected by a `provider/model` prefix on `--model` (for example `anthropic/claude-sonnet-4-5`, `openrouter/openai/gpt-5`, `bedrock/us.anthropic.claude-…`) with base-URL override via environment.

---

## 4. Tool set, lane fence, and read-only seats without an outside harness

*Designer's decision (`D-NAB3`); the owner may overturn this.*

### 4.1 The first-party tool set

`native-agent` exposes at most eight tools, implemented in Go inside `tools/tillr-run/native/` with zero Node/Python helpers and zero MCP server:

| Tool | Exposed on `write` runs | Exposed on `read_only` runs | Purpose and bounds |
| :--- | :---: | :---: | :--- |
| `read_file` | Yes | Yes | Reads UTF-8 text (`path`, optional 1-indexed `offset` and `limit`, capped at `250` lines / `32 KiB` per call); refuses binary files and secret paths ([§4.2](#42-enforcing-the-lane-fence-and-read-only-seats-in-three-layers)) |
| `list_files` | Yes | Yes | Lists directory entries or glob matches under `<workDir>`, wired input dirs, or scratch (`os.Lstat`, capped at `500` entries) |
| `search_files` | Yes | Yes | Searches file contents by literal or Go regular expression (`pattern`, optional `path` and `glob`, capped at `200` matches) so scouts and reviewers never need `bash` merely to search code |
| `write_file` | Yes | Output/scratch only (unless `plan` quick-way) | Atomically writes UTF-8 content via temp-file + rename **only after `CheckWriteTarget` passes** |
| `edit_file` | Yes | Output/scratch only (unless `plan` quick-way) | Applies exact, unique, non-overlapping `edits[{old_text, new_text}]` replacements **only after `CheckWriteTarget` passes** |
| `bash` | Yes | Seat-scoped (`CLONE_NEWNS` ` MS_RDONLY` mount) | Runs `sh`/`bash -c` in `<workDir>` in its own process group with `TILLR_RUN_EXEC_ID`, `timeout_seconds`, scrubbed secret env, `BASH_ENV` wired to [`bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh), [`installGitGuard`](../../tools/tillr-run/run/git_guard.go), and bounded tail capture |
| `report_progress` | Yes | Yes | Appends verified criterion IDs (`c1`, `c2`, …) directly to `$TILLR_PROGRESS_FILE` (`run.progress` in [`progress.go`](../../tools/tillr-run/run/progress.go)) without needing `bash` |
| `land` / `obstacle` / `no_change` | `call-suspends` (`obstacle` in `footer` mode too) | `obstacle` (+ `no_change` when brief permits) | Validates ending invariants in Go, writes `landing-offer.json` + `landing-state.json` in scratch, and suspends the run (`call-suspends`) or emits canonical `OBSTACLE:` / `RESULT:` lines (`footer`) |

### 4.2 Enforcing the lane fence and read-only seats in three layers

In [`run-pi`](../../tools/tillr-run/backends/pi/run-pi), `pi`'s built-in `write`, `edit`, and `bash` tools know nothing of `fence.txt` or `read_only` workflow stages; if a worker edits an out-of-fence file or mutates a `read_only` worktree, `tillr-drain` discovers the mistake only after the process exits and fails the run (`RefusalFence` or `StopHostBoundaryViolation`). Owning the tools lets `tillr-run` enforce boundaries **preventatively** in two inner layers while keeping `tillr-drain`'s post-run snapshot as the independent third layer:

1. **Layer 1 — In-process pre-write target gate (`CheckWriteTarget`) and secret-read denylist:**
   - Before `write_file` or `edit_file` opens any path, `CheckWriteTarget` resolves the path's deepest existing ancestor with `filepath.EvalSymlinks` and rejects any symlink hop escaping the permitted root.
   - **On a `write` run:** writes are permitted *only* to:
     - `<workDir>/<rel>` where `<rel>` matches at least one prefix in `<laneDir>/fence.txt` (when `fence.txt` is non-empty), is not under `.git/`, and matches no pattern in [`config/tillr/deny-list.json`](../../config/tillr/deny-list.json);
     - `<root>/.tillr/loop/<run-id>/artifacts/` ([`nodeOutputsDir`](../../tools/tillr-drain/drain/node_outputs.go), for declared workflow `outputs`);
     - the run's scratch directory (`DurableDir` in [`exec.go`](../../tools/tillr-run/run/exec.go)) and `$TILLR_PROGRESS_FILE`.
     Any write targeting `/workspace` (main checkout), `.tillr/state.db`, `.git/tillr-gate/`, a sibling lane, or an unfenced path inside `<workDir>` is **refused immediately as a tool error (`is_error: true`, naming the permitted `fence.txt` prefixes) with zero bytes written**.
   - **On a `read_only` run** (identified by `TILLR_READ_ONLY_GROUP != ""`, `Seat == "triage"`, or explicit `TILLR_ACCESS=read_only` exported by staging/`Exec` so single-node `read_only` stages get the same signal as parallel reader groups):
     - `<workDir>` is **refused for `write_file` and `edit_file`**.
     - Writes to `<root>/.tillr/loop/<run-id>/artifacts/`, `$TILLR_PROGRESS_FILE`, and the run's scratch directory remain allowed so `scout`, `classify`, and `evaluator_slot` nodes write their declared `node_artifacts` cleanly ([`node_brief.go`](../../tools/tillr-drain/drain/node_brief.go)).
     - *The `plan` quick-way rule ([`node_brief.go`](../../tools/tillr-drain/drain/node_brief.go)):* when `stage.Seat == "plan"` allows `do-it-now`, `<workDir>` starts read-only and unlocks (still bounded by `fence.txt`) if and only if the worker first writes `<root>/.tillr/loop/<run-id>/artifacts/do-it-now`.
   - **Read denylist:** `read_file`, `list_files`, and `search_files` refuse paths resolving to credential or process-environment locations (`~/.config/yolo-agent-env`, `~/.config/tillr`, `~/.aws`, `~/.ssh`, `~/.claude`, `~/.pi/agent/auth*`, `/proc/*/environ`).
2. **Layer 2 — Subprocess (`bash`) confinement:**
   - **Secret scrubbing:** `native-agent` reads provider credentials into Go memory at startup and strips provider API keys, tokens, and auth-broker variables from `cmd.Env` of every `bash` child ([§5.2](#52-call-time-credential-resolution-and-child-env-scrubbing)).
   - **Kernel read-only mounts for `read_only` seats on Linux:** [`triage_guard_linux.go`](../../tools/tillr-run/run/triage_guard_linux.go) (`prepareTriageExecCmd`) already runs `triage` in a private Linux mount namespace (`CLONE_NEWNS`) with `repoRoot`, `laneDir`, `workDir`, `traceDir`, and `storeDir` bind-mounted read-only (`MS_BIND|MS_REC|MS_RDONLY`) and only `<durableDir>` re-mounted read-write. Generalizing that helper to every `read_only` run (adding `<root>/.tillr/loop/<run-id>/artifacts/` to the writable re-mounts while keeping `/tmp` and `$GOCACHE` writable, and pausing the `<workDir>` read-only mount only after `do-it-now` on a quick-way `plan` node) makes shell writes into `<workDir>` fail at the kernel with `EROFS` while still letting `review` and `verify` seats run `go test` and `uvx vantage-check`! On kernels or macOS hosts where unprivileged `CLONE_NEWNS` is unavailable, `bash` falls back cleanly (matching [`triage_guard_linux.go`](../../tools/tillr-run/run/triage_guard_linux.go)) to [`installTriageGuard`](../../tools/tillr-run/run/triage_guard.go) + the preventative `PATH` and `BASH_ENV` lane shims of [`tillr-lane-shims.md`](./tillr-lane-shims.md).
   - **Writer `bash` guardrails:** `bash` inherits `GIT_CEILING_DIRECTORIES`, [`installGitGuard`](../../tools/tillr-run/run/git_guard.go), [`bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh), and the lane shims of [`tillr-lane-shims.md`](./tillr-lane-shims.md). After each `bash` call on a `write` run, `native-agent` runs a fast `git status --porcelain -z` check against `fence.txt` and appends a prominent warning to the tool result if a shell command dirtied an out-of-fence path, so the worker reverts the stray file before committing.
3. **Layer 3 — Independent post-run audit in `tillr-drain`:** `TakeBoundary`, `read_only` dirty/HEAD checks in `workflow.go` and `judge.go`, and `fenceViolation` at settlement stay untouched as the independent final check.

---

## 5. Credentials, which tillr does not hold in this environment

### 5.1 What holds true in this environment today

Verified in this workspace on 2026-11-26:

1. **Zero credentials in `tillr`.** Neither the repository, `.tillr/state.db`, `.tillr.toml`, nor `backend-policy.*.json` stores API keys or tokens; `checkRequest` in [`exec.go`](../../tools/tillr-run/run/exec.go) refuses `env_refs`, and session/trace redaction rules ([`session-recording.md`](./session-recording.md)) forbid persisting secrets.
2. **Per-binary credential scoping inside `yolo` jails.** Inside a `yolo` container, `/home/agent/.yolo/bin/launch/<bin>` sources `~/.config/yolo-agent-env/<bin>.sh` and `~/.config/yolo-agent-env/provider-keys/<bin>.sh` before `exec`ing `<bin>`. Because [`yolo-packs/tillr/pack.json`](../../yolo-packs/tillr/pack.json) declares eleven `"kind": "program"` tools (`tillr`, `tillr-drain`, `tillr-run`, …) rather than a `yolo` `active_provider` agent entry:
   - External agent launchers (`claude`, `pi`, `copilot`) receive provider keys or host auth brokers (`claude-oauth-broker`, `openai-auth-broker`).
   - `tillr-run`'s launcher (`~/.config/yolo-agent-env/tillr-run.sh`) currently receives **only** workspace-wide loopholes such as `aws-auth` (`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`), and holds **no** Anthropic, OpenRouter, OpenAI, or Copilot API keys.

### 5.2 Call-time credential resolution and child-env scrubbing

*Designer's decision (`D-NAB4`); the owner may overturn this.*

1. **Read in memory at process start, never persisted.** `native-agent` resolves credentials only in memory when starting a run:
   - When `--account <slug>` is passed ([`request.go`](../../tools/tillr-run/run/request.go)), it checks `<PROVIDER>_API_KEY_<UPPER_SLUG>` (and optional `TILLR_NATIVE_KEY_CMD_<UPPER_SLUG>` / `TILLR_NATIVE_KEY_FILE_<UPPER_SLUG>` set in user/launcher env) before falling back to standard provider variables (`ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`, `OPENAI_API_KEY`, or standard AWS `AWS_*` / credential-process chain).
   - If the required credential is absent or empty, `run-native` prints `run-native: refused: missing <provider> credentials for model <model> (account <account>)` on stderr and exits `2` (`AdapterRefusedExit` in [`failover.go`](../../tools/tillr-drain/internal/policy/failover.go)) before spending a turn, holding the item cleanly with the exact missing variable name.
   - HTTP headers (`Authorization`, `x-api-key`, `X-Amz-Security-Token`) are never written to `<run-id>.log`, `<session-id>.jsonl`, `report.md`, or SQLite events.
2. **Scrubbed before `bash` tool execution.** Immediately after reading credentials into its HTTP client struct, `native-agent` builds a scrubbed environment slice for `bash` tool children that removes `*_API_KEY*`, `*_SECRET*`, `*_TOKEN*` (except `TILLR_*` non-secret run markers), `TILLR_NATIVE_KEY_*`, and broker endpoint-file paths. Unlike external CLI harnesses whose shell tools inherit the harness's own environment, a command or untrusted test run inside `bash` cannot read the model API key from `os.Environ()`.
3. **Crossing the `yolo` jail boundary ([`OQ-NAB2`](#OQ-NAB2)):** Outside a jail (`brew install` or `install.sh` in [`installing-tillr-outside-yolo.md`](./installing-tillr-outside-yolo.md)), standard user environment variables or a shell wrapper work immediately. Inside a `yolo` jail, granting `run-native` access to non-AWS providers changes the jail/pack credential boundary and is put to the owner in [`OQ-NAB2`](#OQ-NAB2) (scoping a dedicated `tillr-agent` program in `yolo`'s `provider-keys` gate vs a host-side `loopback-tls` loophole proxy vs user-scope `yolo config secret`).

---

## 6. What it lets tillr drop

Once `run-native` is proven across seats (and subject to [`OQ-NAB3`](#OQ-NAB3) on whether `run-pi` is deleted or kept as an optional stock-`pi` adapter), tillr can shed the entire `pi`-fork stack:

| Today's machinery | Where it lives | Why `run-native` makes it unnecessary |
| :--- | :--- | :--- |
| **Patched `pi` fork (`mschulkind-oss/tillr-pi`)** | External repo (`tillr-required` branch) + `yolo-packs/pi-fork` + [`OQ-HI2`](./installing-tillr-outside-yolo.md#OQ-HI2) | Exists solely for `parked-tool-calls` and `pi --features`; `native-agent` suspends `land`/`obstacle`/`no_change` natively in Go and needs no `tillr-pi` Homebrew formula or tarball |
| **`pi --features` startup probes & test-stub tax** | `probePiEndingCapability` in [`landing_call.go`](../../tools/tillr-drain/drain/landing_call.go), `pi_require_features` in [`pi-harness.sh`](../../tools/tillr-run/backends/pi/pi-harness.sh), `TILLR_PI_FEATURES` in `main_test.go` | `backends/native/ending-capability` statically declares `call-suspends`; zero external binary version skew exists |
| **[`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) entry 6 (`pi` compatibility mode & fork-shrinking work)** | [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) (entry 6) | All six couplings listed in entry 6 (`parked-tool-calls`, `SIGUSR1` stop-at-gap, `PI_SUBAGENTS_TEMP_ROOT`, `PI_BACKGROUND_TASK_JOURNAL`, `turn_end` usage fragility, and foreign `yolo-packs/matt/pack.json` reads) disappear on `run-native` |
| **Node.js `>= 22.19` & TypeScript worker extensions** | [`land.ts`](../../tools/tillr-run/backends/pi/land.ts), [`obstacle.ts`](../../tools/tillr-run/backends/pi/obstacle.ts), [`no_change.ts`](../../tools/tillr-run/backends/pi/no_change.ts), [`keep-going.ts`](../../tools/tillr-run/backends/pi/keep-going.ts), `testdata/*.test.mjs` | Replaced by ~300 lines of Go in `tools/tillr-run/native/` + shared [`claude/landing.go`](../../tools/tillr-run/claude/landing.go) helpers; outside-jail hosts need zero Node.js runtime to run workers |
| **Pi agent-dir & rolling helpers** | [`tools/tillr-run/pihelper/`](../../tools/tillr-run/pihelper/), seven `pi-*` faces on `tillr-run`, [`pi-agent-dir.sh`](../../tools/tillr-run/backends/pi/pi-agent-dir.sh), `RollPi` / `RollPiExtensions` in [`tools/tillr-roll/roll/roll.go`](../../tools/tillr-roll/roll/roll.go) | `native-agent` compiles into `tillr-run` itself, so rolling `tillr` rolls the agent loop, tools, and ending logic atomically in one binary |

**What stays:** the `backends/<name>/` process-and-record boundary (`D-UC22`), [`backends/claude/`](../../tools/tillr-run/backends/claude/run-claude) for spending flat-rate Claude Code subscriptions ([`tillr-claude-backend.md`](../reference/tillr-claude-backend.md)), and [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) entry 4's independent removal of generic `.forks/lanes/` handling from `tillr-land` and `tillr-drain`.

---

## 7. What is rejected and why

*Designer's decision (`D-NAB5`); the owner may overturn this.*

1. **Running the LLM conversation and tools in-process inside `tillr-drain` (bypassing `tillr-run exec` and `backends/native/`).** *Rejected:* crosses the process-and-record boundary ([`board-utilities.md`](./board-utilities.md), [`tillr-muse-backend.md`](../reference/tillr-muse-backend.md), `D-UC22`), loses process-group termination and `TILLR_RUN_EXEC_ID` orphan sweeping ([`exec.go`](../../tools/tillr-run/run/exec.go)), prevents per-run `CLONE_NEWNS` read-only bind mounts on Linux, and breaks standalone `tillr run exec` replay.
2. **Importing third-party Go LLM/agent SDKs (`anthropic-sdk-go`, `openai-go`, `langchaingo`, `aws-sdk-go-v2`) or shelling out to Python/Node.** *Rejected:* bloats `go.mod`/`go.sum` across [`just pack-sync`](../../yolo-packs/tillr/pack.json), hides raw HTTP `429` rate-limit headers needed by [`quota_window.go`](../../tools/tillr-drain/internal/policy/quota_window.go), and lags provider thinking/cache fields when direct `net/http` + SSE takes ~300 lines per wire protocol with `CGO_ENABLED=0`.
3. **Building an interactive TUI, slash-command shell, MCP client hub, or dynamic `.ts`/`.js` plugin loader.** *Rejected:* `tillr-run` executes non-interactive staged briefs; rebuilding an open plugin runtime inside `tillr-run` recreates the exact version-skew and extension-rolling surface (`TILLR_EXTENSION_SET_RECORD_FILE`) that `run-native` eliminates.
4. **Exposing nested worker-spawned `subagent` or `background_task` tools inside `run-native`.** *Rejected:* tillr already models multi-agent fan-out and staged roles as explicit workflow graphs (`scout -> plan -> piece -> review -> verify`, confirmed in [`OQ-PR3`](../tillr/decision-provenance-review.md#decision-ledger)), giving every stage its own lane fence, seat model, `read_only` enforcement, and `run.record` instead of scraping hidden child directories or SQLite background-task journals.
5. **Storing API keys, OAuth tokens, or bearer credentials in `.tillr/state.db`, `.tillr.toml`, or `backend-policy.*.json`.** *Rejected:* violates tillr's zero-secret store and profile rules ([`exec.go`](../../tools/tillr-run/run/exec.go), [`tillr-seat-profiles.md`](./tillr-seat-profiles.md)) and risks leaking secrets into traces, Observatory views, or commits.
6. **Relying on prompt prose and post-run `TakeBoundary` snapshots alone for fences and `read_only` seats.** *Rejected:* when tillr implements `write_file`, `edit_file`, and `bash` itself, catching out-of-fence or read-only writes *before* mutating disk converts a wasted run or `StopHostBoundaryViolation` into a free in-turn tool error while preserving `TakeBoundary` as the final audit.

---

## 8. Build slices, smallest first

*Designer's sequencing decision (`D-NAB6`); the owner may overturn this.*

Every slice below carries Go tests in the same commit and runs `just pack-sync` whenever `tools/tillr*` changes. **Slice 1 is deliberately sized as a single small build item (~300 LoC in `tools/tillr-run`)** that proves the complete `tillr-run exec` adapter contract offline against `httptest.Server` with zero live API spend. Slices 5 and 6 explicitly wait on [`OQ-NAB1`](#OQ-NAB1), [`OQ-NAB2`](#OQ-NAB2), and [`OQ-NAB3`](#OQ-NAB3); whether open questions on Slices 5–6 also hold Slices 1–4 follows [`OQ-PR21`](../tillr/decision-provenance-review.md#decision-ledger) and [`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8).

| Slice | Scope (smallest first) | Depends on | Files touched (`+ just pack-sync`) | Verification in the same commit |
| ---: | :--- | :--- | :--- | :--- |
| **1** | **Adapter skeleton, `NativeDialect` report extractor, and single-item turn/session/pause loop over `native.Provider`** *(single build item)* | None on mechanism ([`OQ-PR8`](../tillr/decision-provenance-review.md#OQ-PR8) for doc-wide question hold) | `tools/tillr-run/backends/native/{run-native,extract-report,pause-kind,ending-capability,README.md}`, `tools/tillr-run/extract/{report.go,dialects.go}`, `tools/tillr-run/native/{provider.go,session.go,loop.go}`, `tools/tillr-run/main.go` (`native-agent` face), `tools/tillr-run/README.md` | Offline Go tests in `tools/tillr-run/native/` and `tools/tillr-run/extract/` using a loopback mock `Provider`: (1) `run.Exec` cold start writes `Record.Harness`, `<session-id>.jsonl`, 19-key `report.md`, and `.usage.json`; (2) `$TILLR_PAUSE_REQUEST` stops cleanly at a turn gap (`StatePaused`) and `Resume` continues the same session; (3) interrupted trailing `tool_use` repairs cleanly on `Resume`; (4) `rate_limit_event` satisfies `RefusedWindow` and timeout errors record `run.model-timeout` |
| **2** | **In-process file, search, and progress tools with pre-write `fence.txt` and `read_only` enforcement** | Slice 1 | `tools/tillr-run/native/{tools.go,fence.go,search.go}`, `tools/tillr-run/run/exec.go` + `tools/tillr-drain/drain/stage.go` (`TILLR_ACCESS` env propagation) | Table-driven Go tests: `write_file`/`edit_file` allow fenced `<workDir>` paths, `<artifacts>/`, and scratch; refuse out-of-fence paths, symlink escapes, `deny-list.json`, and `read_only` `<workDir>` writes before touching disk; honor `plan` `do-it-now` unlock; refuse secret-path reads; append `report_progress` lines to `$TILLR_PROGRESS_FILE` |
| **3** | **Native `call-suspends` ending tools (`land`, `obstacle`, `no_change`), keep-going nudges, and `loop_detected` recovery** | Slice 2 | `tools/tillr-run/claude/landing.go` (shared offer/answer helpers), `tools/tillr-run/native/{ending.go,keep_going.go}` | End-to-end Go tests with `tillr-run` and `tillr-drain`: `land`, `obstacle`, and `no_change` return in-turn errors on dirty/`HEAD == base` trees, park with `landing-offer.json` when valid (with zero `--features` probe), replay `landing-answer.json` refusals as `is_error: true` tool results on `SessionTurn`, nudge uncommitted writer exits, and record `EndRepeatingCall` on identical-call streaks |
| **4** | **Constrained `bash` tool with secret-env scrubbing, `tillr-trace` shim, `installGitGuard`, and Linux `CLONE_NEWNS` read-only mounts** | Slice 2 | `tools/tillr-run/native/bash.go`, `tools/tillr-run/run/triage_guard_linux.go` (generalized read-only mount helper) | Go tests verifying: (1) provider `*_API_KEY*` / `*_TOKEN*` vars are absent inside `bash` children; (2) `TILLR_RUN_EXEC_ID` cleanup reaps background grandchildren; (3) `read_only` `bash` fails with `EROFS` on `<workDir>` writes when `CLONE_NEWNS` is permitted while `/tmp`, scratch, and `<artifacts>/` stay writable; (4) writer `bash` warns on out-of-fence dirty files |
| **5** | **First live HTTPS SSE provider wire adapter, `--account` credential lookup, model pricing table, and jail credential plumbing** | Slice 1, **[`OQ-NAB1`](#OQ-NAB1)**, and **[`OQ-NAB2`](#OQ-NAB2)** | `tools/tillr-run/native/<provider>.go`, `tools/tillr-run/native/auth.go`, `tools/tillr-run/extract/usage.go`, plus `yolo-packs/tillr/` if [`OQ-NAB2`](#OQ-NAB2) selects a pack change | `httptest.Server` TLS/SSE streaming suite covering chunk splits, `thinking` signature round-trips, `cache_control` breakpoints, HTTP `429` burst retry vs terminal `rate_limit_event`, SSE body-idle timeout, missing-key exit `2` refusal, and `--account` scoping |
| **6** | **Seat-by-seat live proof (`scout`/`classify`/`verify`, then writer seats), optional `backends/native/compact`, and `pi`-fork retirement** | Slices 3–5 and **[`OQ-NAB3`](#OQ-NAB3)** | `backends/native/compact` (optional), specimen policy docs, and — if [`OQ-NAB3`](#OQ-NAB3) chooses Option A — removal of `backends/pi/`, `pihelper/`, `probePiEndingCapability`, and `RollPi*` | Live comparison runs across read-only and writer seats recording completion rate, fence compliance, cache hit ratio, and cost per landed item; full repository gate green after any [`OQ-NAB3`](#OQ-NAB3) cleanup |

---

## Open Questions

1. 💬 **OQ-NAB1: Which provider wire interface should `run-native` implement first?**

   The loop, session transcript, and tools in `tools/tillr-run/native/` are provider-neutral (`D-NAB2`), so second and third wire protocols add ~250–400 lines of Go each without touching the loop. Choosing the *first* wire interface decides which provider accounts and models Slice 5 tests and spends on first ([§3.2](#32-choosing-the-first-live-wire-protocol-oq-nab1)).

   - **A — Anthropic Messages API (`POST /v1/messages` over HTTPS SSE, with base-URL override for both `api.anthropic.com` and OpenRouter's `/api/v1/messages` skin).** Matches `cache_control` prompt-cache breakpoints, `thinking` + `signature` blocks, `is_error: true` tool results, and `anthropic-ratelimit-*` headers 1-to-1, while reaching both direct Anthropic API keys and every model routed through OpenRouter.
   - **B — OpenAI Chat Completions API (`POST /v1/chat/completions` over HTTPS SSE, with base-URL override for OpenRouter, OpenAI, and compatible gateways).** Speaks OpenRouter's native endpoint (returning inline `usage.cost` USD) and OpenAI-compatible gateways directly, with vendor-specific extensions needed for explicit prompt-cache breakpoints and multi-turn reasoning state.
   - **C — AWS Bedrock `ConverseStream` (`POST /model/{modelId}/converse-stream` with pure-Go SigV4).** Uses the `aws-auth` credentials already injected into `tillr-run` inside this `yolo` jail today, at the cost of binary event-stream framing first and requiring AWS credentials for outside-jail users until a second wire adapter lands.

   <!-- vantage: question id=OQ-NAB1 leaning="A — Anthropic Messages API (/v1/messages) with base-URL override: explicit prompt-cache breakpoints, thinking signatures, error tool results, and rate-limit headers map cleanly onto tillr's model, and OpenRouter's /api/v1/messages endpoint lets the same adapter reach non-Anthropic models too." -->

   _Leaning:_ A — Anthropic Messages API (`/v1/messages`) with base-URL override: explicit prompt-cache breakpoints, thinking signatures, error tool results, and rate-limit headers map cleanly onto tillr's model, and OpenRouter's `/api/v1/messages` endpoint lets the same adapter reach non-Anthropic models too.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-NAB2: Inside a `yolo` jail, how should provider credentials reach `run-native` without `tillr` storing them?**

   Outside a `yolo` jail, `run-native` reads standard user environment variables (`ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`, `OPENAI_API_KEY`, `AWS_*`) in memory and strips them before running `bash` tools ([§5.2](#52-call-time-credential-resolution-and-child-env-scrubbing)). Inside a `yolo` jail, [`yolo-packs/tillr/pack.json`](../../yolo-packs/tillr/pack.json) declares eleven `"kind": "program"` binaries, so `yolo` currently injects `provider-keys` only into external agent launchers (`claude`, `pi`, `copilot`) and gives `tillr-run` only `aws-auth`.

   - **A — Add a dedicated minimal worker launcher (`tillr-agent`) in `yolo-packs/tillr` and `yolo`'s provider-scoping table so `yolo`'s `active_provider` / `provider-keys` and auth brokers grant credentials only to `tillr-agent` (never to `tillr-view`, `tillr-gate`, or interactive shells), with `tillr-agent` scrubbing those env vars before spawning `bash` tools.** Keeps `yolo`'s per-binary secret boundary intact with zero network proxy hop.
   - **B — Keep raw provider API keys on the host outside the jail behind a `yolo` `loopback-tls` loophole proxy (`endpoint` file + per-jail bearer token).** Raw long-lived API keys never enter the container at all, even in `tillr-agent`'s memory, at the cost of building and running a small host-side forwarding service in a `yolo` pack.
   - **C — Pass provider API keys via user-scope `yolo config secret` / workspace environment in jails that select `run-native`, relying on `native-agent`'s child-env scrubbing.** Requires zero pack or `yolo` changes, at the cost that interactive shells in that jail also see the secret env var.

   <!-- vantage: question id=OQ-NAB2 leaning="A — Scope provider-keys in yolo to a dedicated tillr-agent launcher binary and scrub secrets before bash child execution, with B available later if host-only key custody is wanted." -->

   _Leaning:_ A — Scope `provider-keys` in `yolo` to a dedicated `tillr-agent` launcher binary and scrub secrets before `bash` child execution, with B available later if host-only key custody is wanted.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-NAB3: Once `run-native` proves parity across read-only and writer seats, should `tillr` delete `backends/pi/` (`run-pi` and `pihelper`), or keep `run-pi` in stock-`pi` footer compatibility mode?**

   Removing `mschulkind-oss/tillr-pi` is a primary payoff of `run-native` ([§6](#6-what-it-lets-tillr-drop)) and touches [`OQ-HI2`](./installing-tillr-outside-yolo.md#OQ-HI2), [`roadmap.md` §2](../tillr/roadmap.md#2-a-core-that-carries-only-its-own-code) (entry 6), and `D-UC21` / `D-UC22` in [`tillr-utility-contracts.md`](./tillr-utility-contracts.md). [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) stays either way for flat-rate Claude Code subscriptions.

   - **A — Retire `backends/pi/` completely once `run-native` proves parity across seats.** Deletes `mschulkind-oss/tillr-pi`, `backends/pi/*.ts`, `pihelper/` (and its seven `pi-*` faces), `probePiEndingCapability`, and `RollPi` / `RollPiExtensions` in `tillr-roll`, closing [`OQ-HI2`](./installing-tillr-outside-yolo.md#OQ-HI2) and Roadmap entry 6 outright while interactive human sessions keep using stock `pi` directly.
   - **B — Retire only the `mschulkind-oss/tillr-pi` fork and keep a stripped `backends/pi/` adapter in `footer` mode for stock upstream `pi`.** Drops `parked-tool-calls`, `--features`, and `tillr-pi`, while still letting a user route a seat through stock `pi` CLI plugins/providers at the cost of keeping `backends/pi/` and `PiDialect` maintained.

   <!-- vantage: question id=OQ-NAB3 leaning="A — Retire backends/pi/ completely once run-native proves parity across seats: interactive sessions still use stock pi directly, run-claude covers Claude subscriptions, and deleting backends/pi/ + pihelper removes the entire Node/extension/rolling maintenance surface." -->

   _Leaning:_ A — Retire `backends/pi/` completely once `run-native` proves parity across seats: interactive sessions still use stock `pi` directly, `run-claude` covers Claude subscriptions, and deleting `backends/pi/` + `pihelper` removes the entire Node, extension, and rolling maintenance surface.

   **Answer:**

   > _(empty — fill in when decided)_

---

## Decision Ledger

Per [`decision-provenance-review.md` §6](../tillr/decision-provenance-review.md#6-how-this-stops-happening) (`D-PR1`, `D-PR2`), only `R-NAB1` quotes the owner's dated words; `D-NAB1` through `D-NAB6` are the designer's decisions written from the 2026-11-26 operator request, and the owner may overturn any of them.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| `R-NAB1` | **Owner request:** *"start building our native agent backend that we should have in a doc somewhere."* | 2026-11-26 | [§1.1](#11-search-of-tracked-documents-and-git-history) | No |
| `D-NAB1` | **Designer's decision; the owner may overturn this.** Implement the native backend across the existing `tillr-run` process-and-record boundary (`tools/tillr-run/backends/native/{run-native,extract-report,pause-kind,ending-capability}` invoking `tillr-run native-agent` and `tillr-run extract-report --dialect native`, preserving `D-UC22`), writing provider-neutral transcripts to `<artifactDir>/sessions/<session-id>.jsonl`, declaring `call-suspends` and `stops-at-a-gap` statically with zero `--features` probe or `SIGUSR1` wrapper, and emitting stream events compatible with `settled.go`, `progress.go`, and `quota_window.go`. **Rejected:** in-process LLM execution inside `tillr-drain`; interactive TUI or dynamic `.ts`/`.js` extensions. | 2026-11-26, designer | [§1.2](#12-what-the-native-agent-backend-is), [§2](#2-how-it-fits-the-adapter-contract) | No |
| `D-NAB2` | **Designer's decision; the owner may overturn this.** Isolate HTTP wire protocols behind a single streaming Go interface (`native.Provider` in `tools/tillr-run/native/`) built on standard-library `net/http` + SSE (`CGO_ENABLED=0`, zero third-party LLM SDKs), preserving thinking signatures, prompt-cache breakpoints, `is_error` tool results, and HTTP `429` rate-limit window headers, with the first live wire protocol selected by [`OQ-NAB1`](#OQ-NAB1). **Rejected:** vendor Go SDKs or Node/Python shims. | 2026-11-26, designer | [§3](#3-which-provider-interface-it-speaks-first), [`OQ-NAB1`](#OQ-NAB1) | No |
| `D-NAB3` | **Designer's decision; the owner may overturn this.** Provide eight built-in tools (`read_file`, `list_files`, `search_files`, `write_file`, `edit_file`, `bash`, `report_progress`, and shared Go `land`/`obstacle`/`no_change` offer helpers) and enforce confinement in three layers: (1) symlink-safe pre-write `CheckWriteTarget` in `write_file`/`edit_file` enforcing `<workDir>` + `fence.txt` + `deny-list.json`, `read_only` target lock (allowing `<artifacts>/`, scratch, `$TILLR_PROGRESS_FILE`, and `plan` `do-it-now`), and secret-path read refusals; (2) `bash` child confinement via Linux `CLONE_NEWNS` read-only bind mounts on `read_only` seats (generalized from `triage_guard_linux.go`) plus `installGitGuard`, `bash-shim.sh`, and post-call stray-dirty warnings on writers; and (3) unchanged post-run `TakeBoundary` and `fenceViolation` checks in `tillr-drain`. **Rejected:** nested worker `subagent`/`background_task` tools; relying on post-run snapshots alone. | 2026-11-26, designer | [§4](#4-tool-set-lane-fence-and-read-only-seats-without-an-outside-harness) | No |
| `D-NAB4` | **Designer's decision; the owner may overturn this.** Keep zero credentials in `.tillr/`, `.tillr.toml`, or seat policies; resolve credentials in memory at startup (`--account` env/helper lookup before default provider env vars, exiting `2` on missing credentials), never log auth headers, scrub secret env vars from `bash` child processes, and settle `yolo` jail credential delivery through [`OQ-NAB2`](#OQ-NAB2). **Rejected:** storing secrets in SQLite or policy JSON; inheriting raw API keys into `bash` tool subshells. | 2026-11-26, designer | [§5](#5-credentials-which-tillr-does-not-hold-in-this-environment), [`OQ-NAB2`](#OQ-NAB2) | No |
| `D-NAB5` | **Designer's decision; the owner may overturn this.** Record the six rejected alternatives of [§7](#7-what-is-rejected-and-why) (`tillr-drain` in-process loop, third-party LLM SDKs, TUI/plugin/MCP runtime, nested worker subagents, stored credentials, and post-hoc-only fence checks) while keeping `run-claude` for flat-rate Claude Code subscriptions and deferring `backends/pi/` deletion vs stock-`pi` retention to [`OQ-NAB3`](#OQ-NAB3). | 2026-11-26, designer | [§6](#6-what-it-lets-tillr-drop), [§7](#7-what-is-rejected-and-why), [`OQ-NAB3`](#OQ-NAB3) | No |
| `D-NAB6` | **Designer's decision; the owner may overturn this.** Sequence implementation into the six slices of [§8](#8-build-slices-smallest-first), starting with Slice 1 as a single self-contained build item (`backends/native/` + `NativeDialect` + turn/session/pause loop tested offline against a loopback mock `Provider` in `tools/tillr-run`), followed by fenced tools (Slice 2), native `call-suspends` endings (Slice 3), confined `bash` (Slice 4), the [`OQ-NAB1`](#OQ-NAB1)/[`OQ-NAB2`](#OQ-NAB2) live wire adapter (Slice 5), and live seat proof + [`OQ-NAB3`](#OQ-NAB3) `pi`-fork retirement (Slice 6). | 2026-11-26, designer | [§8](#8-build-slices-smallest-first) | No |
