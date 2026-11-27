---
title: "Session Recording — How Do We Record Agent Work Once and Query Every Claim, Cost, and Tick Later?"
status: in-review
stage: DECIDED
next: "Write the implementation plan for the four owed slices in Section 10.2, starting with reading a Claude run's log and run record."
tags: [design, tillr, observability, recording, provenance]
summary: "A recording layer that fills two missing observation seams (a fail-open shell command shim and per-run preservation of staged inputs), correlates the stores a run already writes, and checks a lane's report against the commands that actually ran."
vantage:
  status-chip: true
---

# Session Recording — How Do We Record Agent Work Once and Query Every Claim, Cost, and Tick Later?

**Status:** 2026-11-27. Every decision is made; no question is open. The command shim, the per-run
record directory and the [`tillr-trace`](../../tools/tillr-trace/README.md) query commands are
built, and they answer in full only for a run on the `pi` backend. Four slices are owed and seven
earlier proposals are dropped; [Section 10.2](#102-what-is-not-built) lists both. The decisions
of 2026-11-27 (`D-SR2` to `D-SR6`) are the designer's, and the owner may overturn any of them.
Where this document and the tillr-trace README differ, the README describes what runs.

> **In short.** Do not build a network proxy and do not duplicate the stores a run already writes.
> Add the two observations nothing else makes — every shell command a run starts, and the exact
> inputs a run was staged with — and answer questions by reading those artifacts on demand, with
> no persistent index.

**Why it matters.** Without a recording, nobody can see that a lane claimed five passing test
suites after running four, what the model was shown on a lane that has since been reused, which
commands a run made, or what each lane cost.

**Start at** [Section 2.2](#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy)
for the mechanisms and
[Section 8.3](#83-mechanical-claim-verification-tillr-trace-verify-claims) for claim verification.

**Reads with:** the [tillr-trace README](../../tools/tillr-trace/README.md) (what is built) and
[the observatory reference](../reference/observatory-redesign.md) (where a run's record is shown).

**Terms.** Three are coined here, and one is tillr's own:

- **`trace_key`** *(coined here)* — the correlation string
  `tick_id/item_id/run_id/session_id/span_id`, with `~` for an absent segment. It says where an
  observation sits in a workflow. It is not a content digest and not a database row id.
- **`obs.event`** *(coined here)* — one schema-versioned observation of a process, tool, provider
  or gate seam. It describes what was *observed*. It is not a workflow state transition; those
  belong to `.tillr/state.db`.
- **`claim_check`** *(coined here)* — a derived comparison of an assertion in a lane's report (such
  as "all five suites exit 0") with the `proc.exec` observations of the same run, including whether
  a suite ran after the last edit to the code it tests. It is not a gate verdict.
- **Run record** — `run.record.json`, the file [`tillr-run`](../../tools/tillr-run/) writes in a
  run's record directory when the run ends. It names the run's backend, state and exit code, and
  carries the tokens and dollars the backend reported, in one shape for every backend.

---

## 1. The problem

A run's history is spread over stores that are written independently: the agent's own session
transcript, `.tillr/state.db`, the drain's per-run record directory `.tillr/loop/<run-id>/`, gate
plans and verdicts, and git. Three kinds of evidence fall between them:

1. **What a run was shown.** A lane directory is reused, so a brief, fence and base sha kept only
   in the lane are overwritten by the next run.
2. **What a run actually executed.** An agent's `bash` tool result records the outer shell string
   and its exit code. It cannot say which suites ran inside a compound command, in which directory,
   or whether one ran after the last edit to the code it tests.
3. **What a run cost and called.** The adapter's report extraction reads the final report. The
   run record keeps the usage each turn reported, and only the run log holds the tool calls.

---

## 2. Seam Analysis: Shim, Proxy, Collector, or All Three?

No single mechanism observes every seam. Re-collecting what the agent, tillr and git already write
is waste; reading only existing files leaves subprocess executions and overwritten staging inputs
invisible.

### 2.1 What each mechanism can see

| Seam | Reader over existing files | Shell command shim | Out-of-process HTTP proxy |
| :--- | :--- | :--- | :--- |
| Provider call (tokens, cost, route) | Complete for finished turns, from the run record | Blind | Sees wire bytes, but sits on the critical path and needs a trusted certificate in every runtime |
| Agent tool calls | Complete, from the run log, for a backend whose log shape the reader parses | Sees shell spawns only | Only by re-parsing each provider's stream format |
| Run process (exit, timing) | Complete, from the store | Complete | Blind |
| Staged inputs (brief, fence, base sha) | Lost when a lane is reused, unless kept per run | — | Blind |
| Commands inside a shell | Blind | Complete: one row per command with its directory and exit code | Blind |
| Git operations | Commit history only | A worker's own `git` commands appear as shell rows | Blind |

### 2.2 Architectural Ruling: Collector, Command/Git Shim, and In-Process Hook (No Network Proxy)

Recording uses cooperating mechanisms and rejects an out-of-process HTTP proxy:

1. **A reader over existing artifacts.** [`tillr-trace`](../../tools/tillr-trace/README.md) parses
   the run log, `proc-exec.jsonl`, the gate plan and verdict, and opens `.tillr/state.db` read-only.
   It re-captures nothing. [Section 4.5](#45-one-reader-for-every-backend-d-sr2) says what it reads
   for each backend.
2. **A fail-open shell command shim.** Three run adapters
   ([`run-pi`](../../tools/tillr-run/backends/pi/run-pi),
   [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) and
   [`run-native`](../../tools/tillr-run/backends/native/run-native)) export `TILLR_TRACE_DIR` and
   set `BASH_ENV` to [`bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh), so every
   non-interactive shell a run starts appends one `proc.exec` line per command. `BASH_ENV` is
   chosen over system-call tracing, library preloading and kernel probes because it works
   unprivileged in a container and covers statically linked binaries. **Owed (`D-SR4`, the
   designer's decision):** [`run-agy`](../../tools/tillr-run/backends/agy/run-agy) and
   [`run-muse`](../../tools/tillr-run/backends/muse/run-muse) set neither variable
   (`rg -l BASH_ENV tools/tillr-run/backends` names only the `pi`, `claude` and `native`
   directories, 2026-11-27), so a run on either records no command. Each sets both, and unsets the
   git trace variables, exactly as the other three do. Where a backend's agent runs its shells
   somewhere the adapter's environment does not reach, the shim records nothing and the run's
   command record reads `missing`; that is the fail-open outcome and not a fault.
3. **Per-run preservation of staged inputs.** The brief, fence and base sha a run was staged with
   are kept in that run's own record directory, not only in the reusable lane.
4. **Provider usage from what the agent already reported.** Tokens and dollars are read from the
   run record, which holds what the agent's own stream reported. No network proxy is placed on the
   provider path.

> [!IMPORTANT]
> **Git trace2 is not recorded** (owner ruling, 2026-11-17). A `GIT_TRACE2_EVENT` variable set for
> a run is inherited by every test suite the run starts: measured, one run recorded 918,054 git
> commands and 906 runs held about 82 GB. The adapters unset the variables, and `tillr-trace` reads
> no git trace file. A worker's own `git` commands are `proc.exec` rows.

---

## 3. Placement

### 3.1 Constraints

A run's worktree is checked for writes outside its fence, so the recorder must not write inside a
worktree. The recorder must also survive lane reuse and must need no daemon, socket or network
route.

### 3.2 Concrete Directory Layout

Two directories hold one run's artifacts, and `tillr-trace` reads both:

```text
<root>/.tillr/loop/<run-id>/        the drain's per-run record directory
├── brief.md, fence.txt, base_sha   the staged inputs of this run
├── proc-exec.jsonl                 one line per shell command (the shim)
├── run.record.json                 the run record: backend, state, exit code, usage
└── gate.plan.json, gate.verdict.json

<trace-root>/runs/<run-id>/         an optional recorder directory
```

`<trace-root>` is `$TILLR_TRACE_ROOT`, or `$XDG_STATE_HOME/tillr-trace/v1`. The loop directory is
read first, and each artifact is taken from the first directory that has it. `.tillr/` is outside
every lane worktree, so frequent writes there never appear in a worktree's status.

**There is no persistent index** (owner ruling, 2026-11-18). A SQLite index of the trace would be
cached derived data: every table in it can be rebuilt from the store and the run artifacts. Each
command reads the artifacts its question needs; aggregates read the 100 most recent runs by
default. Measured wall times are in the
[tillr-trace README](../../tools/tillr-trace/README.md#no-index-every-answer-is-read-from-the-artifacts).

---

## 4. Event Schema and the Cross-Boundary Correlation Key

### 4.1 Terms

The three coined terms are defined in the header. An observation row is never a workflow state
transition.

### 4.2 The Five-Part Hierarchical `trace_key`

```text
<tick_id>/<item_id>/<run_id>/<session_id>/<span_id>
```

| Segment | Meaning | Across a resumed session |
| :--- | :--- | :--- |
| `tick_id` | The drain pass that dispatched the run; `~` for an interactive run | Advances with each pass |
| `item_id` | The item the run works on | Constant across the item's runs |
| `run_id` | One execution window | New for every attempt or resume |
| `session_id` | The agent's conversation | Constant, joining every run that shared one model context |
| `span_id` | One tool call, shell command or model turn | Unique per observation |

Grouping by `session_id` shows a whole multi-run conversation; filtering by `run_id` shows only what
happened after a resume. The adapters pass `TILLR_RUN_ID` and `TILLR_TRACE_DIR` to everything a run
spawns.

### 4.3 `obs.event` JSONL Schema

Every row has a flat header and a typed payload:

```json
{
  "obs_version": 1,
  "kind": "proc.exec",
  "ts_start": "<ISO-8601 UTC>",
  "ts_end": "<ISO-8601 UTC>",
  "duration_ms": 2788,
  "trace": {
    "tick_id": "<tick>", "item_id": "<item>", "run_id": "<run>",
    "session_id": "<session>", "span_id": "sh-00019"
  },
  "payload": {
    "argv": ["go", "test", "./..."],
    "repo_rel_cwd": "tools/tillr-gate",
    "exit_code": 0
  }
}
```

| `kind` | Read from | Payload |
| :--- | :--- | :--- |
| `tick.decision` | the store's `dispatches` | dispatch or hold, reason, route |
| `run.lifecycle` | the store's `runs` | state, exit code, base and head sha |
| `llm.turn` | the run record and the run log ([Section 4.5](#45-one-reader-for-every-backend-d-sr2)) | provider, model, tokens, cost, stop reason |
| `tool.call` | the run log | tool name, target path or command preview, error flag, result size |
| `proc.exec` | `proc-exec.jsonl` | command, directory, exit code, duration |
| `gate.plan` | `gate.plan.json` | plan id, base, tree, check ids |
| `gate.verdict` | `gate.verdict.json` | verdict, failed check ids, timings |
| `claim.verdict` | `tillr-trace` | claim text, `verified`, `disproved` or `unverified`, reason |
| `unreadable` | `tillr-trace` | the artifact that could not be read and why |

`unreadable` exists so that an artifact nobody could read is a recorded fact, not an empty result
that reads as "nothing happened". Kinds this design once named and no longer wants are listed in
[Section 10.2](#102-what-is-not-built).

### 4.4 Worker Test-Slot Wait Recording, Run Wall-Time Split, and Chronological Scheduling Decisions (`D-SR1`)

`D-SR1` (cross-referenced as `D-OR7` in
[the observatory reference](../reference/observatory-redesign.md#why-its-this-way)) is a designer's
decision from an operator-filed request, not an owner ruling; the owner may overturn it. It
specified three things, and two remain:

1. **Slot-wait recording is retired (`D-SR5`, the designer's decision).** `D-SR1` had the test
   runner append one line per test-slot wait to `slot-waits.jsonl`. The owner's ruling
   [`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1) of 2026-11-25 removed the limit those waits came
   from: tillr imposes no machine-wide limit on concurrent test processes, and the cross-run slot
   pool was deleted with its recording. Nothing writes the file now
   (`rg 'slot-waits' tools -g '*.go' -g '!*_test.go'` finds only readers in
   [`tools/tillr-view`](../../tools/tillr-view/) and a documentation check in `tools/tillr`,
   2026-11-27), and a run cannot wait on a slot that does not exist. The `slot.wait` row kind and
   the file leave this design. What still reads them is owed for removal
   ([Section 10.2](#102-what-is-not-built)).
2. **A per-run wall-time split.** [`tillr-view`](../../tools/tillr-view/) serves, on the run page
   and `GET /api/v1/runs/{id}`, total wall time, model time and tool time. A quantity nothing
   recorded reads `not recorded`, never `0`. Until the removal above lands, the split also carries
   a test-slot wait that reads `not recorded` on every run started after 2026-11-25.
3. **The drain's scheduling decisions for the run, in order.** Allocation and launch, gate start
   and end, refusal or landing, and retry or continuation, read on demand from the store and the
   run's record directory.

**Rejected:** showing `0 ms` for a quantity nothing recorded; a persistent trace index or a
writable store handle in `tillr-view`.

### 4.5 One reader for every backend (`D-SR2`)

The reader was written against the `pi` backend's log and reads nothing from any other. Measured
on 2026-11-27, `tillr-trace audit` of a finished run on the Claude backend reported the log `ok`
with `0 rows`: no `llm.turn`, no `tool.call` and no claim verdict. `tillr-trace cost --runs 30`
answered `no llm.turn row` while the run records of the same runs held their tokens and dollars.
That is the answer [Section 4.3](#43-obsevent-jsonl-schema) says a recorder must never give by
accident: an unread log shown as an empty one. Of the 300 most recent run record directories that
day, 34 were Claude runs.

`D-SR2` is the designer's decision of 2026-11-27, and the owner may overturn it:

1. **Tokens and dollars come from the run record, for every backend.** `tillr-run` already
   reduces each backend's usage report to one shape there, so the reader needs no per-backend
   arithmetic. A run with no run record takes its usage from the log when a parser for that log
   yields usage, and otherwise its tokens and dollars read `not recorded`, never `0`.
2. **Tool calls, turn timing and stop reason come from the run log, through a parser for that
   backend's log shape.** Two shapes are owed: the `pi` shape, which is built, and the Claude
   shape. Each parser maps its backend's tool names onto the three the claim join of
   [Section 8.3](#83-mechanical-claim-verification-tillr-trace-verify-claims) uses: a file edit, a
   file write and a shell command.
3. **A log in a shape no parser recognizes reads `unreadable`,** with a reason that names the
   backend. It never reads `ok` with no rows. Its claims are `unverified`. How the reader
   recognizes a shape is the implementer's choice.
4. **Dollars are never split by a guess.** The run record gives tokens for each model and one
   dollar total for the run. In the per-model table of `tillr-trace cost`, a run that spent tokens
   on more than one model is counted under `mixed`. Its dollars still count in full under its lane
   and its route.
5. **When the run record and the log disagree about usage, the run record is right**
   ([Section 6.3](#63-single-source-of-truth-which-store-is-authoritative-for-which-fact)).

The slice is done when `tillr-trace audit` of a Claude run lists its turns, tool calls and claim
verdicts, and `tillr-trace cost` over runs that are all Claude runs prints a dollar total equal to
the sum of those runs' run-record totals.

---

## 5. Redaction Boundary: What "EVERYTHING" Must Not Include

Recording everything without a mechanical redaction boundary turns the trace into a credential
cache.

### 5.1 What Is Excluded and How It Is Enforced at Two Mechanical Gates

The two gates are the emitter, which bounds what reaches disk, and the reader, which bounds what an
answer shows.

| Category | Rule | Enforced |
| :--- | :--- | :--- |
| Credentials and tokens | Anything shaped like a bearer token, a provider or forge key, a cloud access key or a `--token`/`--password` argument is replaced before it is shown | `tillr-trace` scrubs excerpts; the shim caps each command's text at 512 bytes |
| Process environment | Never recorded | The shim records command, directory and exit code only |
| File and tool-result content | Stored as path, size and digest, never as text; only `bash` command previews are kept, capped at 200 characters | `tillr-trace` |
| Third-party conversation and browser page dumps | Timing and byte count only | `tillr-trace`, by tool name |

**The emitter does not scrub, and will not (`D-SR6`, the designer's decision).**
`proc-exec.jsonl` holds the command text as typed, so a secret passed on a command line reaches
disk in that file. Scrubbing in the shim would not keep it off the disk: the run log beside it
records the same command string as the agent typed it, and the shim's budget
([Section 7.2](#72-overhead)) allows shell builtins only, which cannot match token shapes
reliably. The boundary that holds is the directory: `.tillr/` is ignored by git
(`git check-ignore .tillr/loop/x` matches, 2026-11-27), is never delivered, and is read only
through a reader that scrubs what it shows.

### 5.2 What the record cannot answer

Because of these exclusions the record cannot say what a credential's value was, what prose an agent
read from a file or page, or what a command printed. It can say which path was read, when, how many
bytes came back, and whether two reads returned the same content.

---

## 6. Storage and Store Authority

### 6.1 Volume

The shim writes one line per shell command. In one measured corpus of 1,204 runs, logs held 6.4 GB
and `proc-exec.jsonl` files 2.2 GB; the largest single `proc-exec.jsonl` was 152 MB. On
2026-11-27 this workspace's 1,290 run record directories held 3.7 GB (`du -sh .tillr/loop`).

### 6.2 Retention

This design sets no retention policy (`D-SR6`, the designer's decision). A run's record directory
is part of the execution record that
[the queue persistence reference](../reference/tillr-queue-persistence.md#why-its-this-way) owns:
it is retained across restarts, and deleting any of it is the unbuilt `tillr store retire-epoch`
and size caps named there. A second policy here would give one directory two owners. The recorder
never deletes, truncates or rewrites a record file.

### 6.3 Single Source of Truth: Which Store Is Authoritative for Which Fact

The recorder never competes with `.tillr/state.db`. Every fact has one authoritative store:

| Fact | Authoritative store | What `tillr-trace` does |
| :--- | :--- | :--- |
| Item lifecycle, dispatch decisions, run terminal state, landings | `.tillr/state.db` | Opens it read-only; never writes or migrates it |
| Gate plan and verdict | `.tillr/loop/<run-id>/gate.{plan,verdict}.json` | Parses them |
| Committed trees and landed commits | Git | Reads recorded shas |
| Tokens and dollars a run spent | The run record | Reads its usage ([Section 4.5](#45-one-reader-for-every-backend-d-sr2)) |
| Tool calls, turn timing, stop reason | The run log | Parses turn and tool rows |
| Staged inputs per run, shell commands | The run's record directory | Authoritative here: no other store keeps them |

When a derived row and a source file disagree, the source file is right, and every command prints
the artifact path its answer was read from.

---

## 7. Never on the Critical Path: Fail-Open Mechanics and Overhead Budget

The recorder must not be able to fail a launch, a run or a landing.

### 7.1 Four Mechanical Fail-Open Guarantees

1. **No daemon, no socket, no blocking pipe.** Emitters append short lines to regular files in the
   run's record directory. A directory that cannot be created or written records nothing.
2. **Exit-code preservation.** The shim saves `$?` as its trap's first instruction and returns it,
   and swallows every write failure. A broken shim cannot alter a command's exit code or output.
3. **No contention on `.tillr/state.db`.** Nothing in the recording path opens the store during a
   run. Readers open it read-only.
4. **Bounded capture.** Each command's recorded text is capped, and readers parse JSONL one line at
   a time, so peak memory is the rows kept, not the size of the largest file.

**Owed (`D-SR3`, the designer's decision): every line the shim writes is valid JSON, whatever the
command contains.** Measured on 2026-11-27, one multi-line command left a raw control character
inside a JSON string, so 1 of a run's 655 `proc-exec.jsonl` lines did not parse and the run's whole
command record read `corrupt`. The shim escapes or replaces every character JSON forbids in a
string; which of the two is the implementer's choice, inside the same builtins-only budget and
the same 512-byte cap. The slice is done when a command holding a tab, a newline and an escape
character yields one row that parses.

### 7.2 Overhead

The shim uses shell builtins only, with no fork per command; the budget is at most 1.5 ms per shell
command. The [tillr-trace test suite](../../tools/tillr-trace/shim_test.go) runs the shim against an
unwritable trace directory and asserts the command's behavior is unchanged.

---

## 8. How a Recording Becomes Evidence and Verifies Lane Claims

### 8.1 What can be replayed

| Tier | What | Why |
| :--- | :--- | :--- |
| Exact | Gate planning over a recorded change; claim verification over a recorded run | Pure functions of recorded inputs |
| Approximate | Re-running a gate suite on a recorded tree; re-running an agent from its preserved brief, fence and base | Same inputs; suites and model sampling vary |
| Not replayable | Provider rate limits and latency; interleaving of concurrent processes | Governed by systems and clocks outside the run |

### 8.2 Why the record is stronger than the report

A worker's closing summary is an untrusted claim. The record is checkable because it is written
outside the model's text: the store records the run's start, finish and exit; the shell runtime
writes `proc.exec` rows; and a suite attests a directory's code only if no file there was edited
after the suite ran.

> [!WARNING]
> `proc-exec.jsonl` is an ordinary file that the run's own shell can append to. A claim verdict
> separates an *honest* lane's prose from its record. It is not evidence about a hostile lane, and
> it never substitutes for the gate.

### 8.3 Mechanical Claim Verification (`tillr-trace verify-claims`)

Claim verification runs inside `tillr-trace audit` and `tillr-trace discrepancies`; there is no
separate `verify-claims` command. For each run it checks three classes of claim:

1. **Owed suites.** For each module directory `D` the run edited, let `T_edit(D)` be the time of
   the last edit under `D`, and `E_test(D)` the latest passing test command run in `D` with a start
   time after `T_edit(D)`. No such command, when a suite passed only before the edit, is
   `disproved`.
2. **Quantified prose.** "All five suites pass" is compared with the number of distinct modules
   observed passing; a backticked command claimed as passing must match a `proc.exec` row that
   exited 0.
3. **Candidate claims.** `RESULT: done` on a run whose head equals its base while its tree changed
   is `disproved`: the work was left uncommitted.

Verdicts are exactly `verified`, `disproved` and `unverified`. `unverified` is what a claim gets
when the record cannot answer it; absent evidence is never promoted to proof or disproof. The
[README](../../tools/tillr-trace/README.md) lists what fools the join.

---

## 9. Questions the record answers

| Question | Command |
| :--- | :--- |
| What did each lane, model and route cost? | `tillr-trace cost` |
| Which claims in a lane's report were true? | `tillr-trace audit <run-id>` |
| What happened in one unattended pass, in order? | `tillr-trace tick <id>` or `--last` |
| Where did a lane's report disagree with the record? | `tillr-trace discrepancies` |
| What did one run do, as one document? | `tillr-trace run <run-id>` |

---

## 10. Build scope

### 10.1 Scope and Files Touched (`ESTIMATE: 3–4 hours`, ~430 LoC)

The first slice is built and has three parts. Its reader answers in full only for a `pi` run
([Section 4.5](#45-one-reader-for-every-backend-d-sr2)):

| Part | Where |
| :--- | :--- |
| Per-run record directory holding the staged brief, fence and base sha | [`tools/tillr-drain`](../../tools/tillr-drain/) |
| Fail-open shell command shim, set by three of the five run adapters | [`bash-shim.sh`](../../tools/tillr-trace/bash-shim.sh), [`run-pi`](../../tools/tillr-run/backends/pi/run-pi), [`run-claude`](../../tools/tillr-run/backends/claude/run-claude), [`run-native`](../../tools/tillr-run/backends/native/run-native) |
| Reader and query commands `tick`, `audit`, `run`, `cost`, `discrepancies` | [`tools/tillr-trace`](../../tools/tillr-trace/) |

### 10.2 What is not built

**Owed.** Four slices, none of which waits on another. The first restores answers the current
backend has lost, so it goes first:

| Slice | Decision | Done when |
| :--- | :--- | :--- |
| The reader reads a Claude run's log and every backend's run record | `D-SR2`, [Section 4.5](#45-one-reader-for-every-backend-d-sr2) | The outcome stated at the end of that section |
| Every `proc.exec` line is valid JSON | `D-SR3`, [Section 7.1](#71-four-mechanical-fail-open-guarantees) | The outcome stated there |
| `run-agy` and `run-muse` set the shim | `D-SR4`, [Section 2.2](#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy) | A run on either backend leaves a `proc-exec.jsonl` with its commands, or reads `missing` where the adapter's environment does not reach the agent's shells |
| Remove what still reads slot waits | `D-SR5`, [Section 4.4](#44-worker-test-slot-wait-recording-run-wall-time-split-and-chronological-scheduling-decisions-d-sr1) | The run page and `GET /api/v1/runs/{id}` carry no test-slot wait, and no code or README under `tools/` names `slot-waits.jsonl` |

**Dropped (`D-SR6`, the designer's decision of 2026-11-27; the owner may overturn any line).** Each
was proposed in an earlier form of this design and none is built. Nothing in the tree reads or
waits on any of them:

| Proposal | Why it is dropped |
| :--- | :--- |
| Indexing interactive agent sessions that did not run under `tillr-run` | Such a session has no run, brief, fence or item to join to, and [`OQ-SR2`](#122-decision-ledger) rules the shim out of a shell a person opens |
| A content-addressed store of full command output | [Section 5.1](#51-what-is-excluded-and-how-it-is-enforced-at-two-mechanical-gates) excludes output text from the record, and the store would be the credential cache that section exists to prevent |
| A retention policy of this design's own | [Section 6.2](#62-retention): the persistence reference owns the directory |
| `llm.retry`, `tick.stage` and `boundary.check` rows, and writing `claim.verdict` rows to disk | A written verdict is cached derived data, which [`OQ-SR7`](#122-decision-ledger) rules out. The drain's own `steps.jsonl` already records a run's stages and `tillr-view` reads it directly |
| Restoring a run's staged inputs into a scratch worktree for re-dispatch | The inputs are kept and readable; dispatching again from an item is the drain's job and needs no restore |
| Reading more than one workspace in a single query | [`OQ-SR3`](#122-decision-ledger) keeps one trace per workspace; a second workspace is a second command |
| Scrubbing secrets at the emitter | [Section 5.1](#51-what-is-excluded-and-how-it-is-enforced-at-two-mechanical-gates) |

---

## 11. How a recorder fails, and what prevents each

| Failure | Prevention |
| :--- | :--- |
| Records accumulate and nobody can query them | The query commands ship with the emitter |
| Lane reuse destroys what a run was shown | Staged inputs are kept per run, outside the lane |
| A suite that ran before the last edit is counted as proof | The post-edit join of [Section 8.3](#83-mechanical-claim-verification-tillr-trace-verify-claims) |
| A compound shell command hides a skipped or failed suite | One `proc.exec` row per command, independent of the outer shell's exit |
| The recorder fails or slows a run | The fail-open guarantees of [Section 7.1](#71-four-mechanical-fail-open-guarantees) |
| A new agent backend records nothing | The shim sits in the adapter's environment, below the agent binary, and every adapter sets it (`D-SR4`, owed for two) |
| A backend's log is in a shape the reader does not parse, and the run reads as having done nothing | An unrecognized log reads `unreadable` and names the backend (`D-SR2`, owed) |

---

## 12. Decisions

### 12.2 Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-SR1 | A disproved report claim is **advisory**: it is recorded beside the verdict and never rejects a candidate before the gate. A parser heuristic that blocks a valid candidate is worse than a slow gate. Owner ruling | 2026-11-12 | [Section 8.3](#83-mechanical-claim-verification-tillr-trace-verify-claims) | Yes |
| OQ-SR2 | The shim is enabled for **every agent-spawned shell** and never for a shell a person opens. Owner ruling | 2026-11-12 | [Section 2.2](#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy) | For runs on the `pi`, `claude` and `native` adapters; `agy` and `muse` are owed (`D-SR4`) |
| OQ-SR3 | One trace **per workspace**; no shared writable file across workspaces. Owner ruling | 2026-11-12 | [Section 3.2](#32-concrete-directory-layout) | Yes |
| OQ-SR4 | Reader over existing artifacts plus a shell shim; no HTTP proxy | 2026-11-12 | [Section 2.2](#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy) | Yes |
| OQ-SR5 | Trace child commands through `BASH_ENV`. Git trace2 is not recorded (owner ruling, 2026-11-17) | 2026-11-17 | [Section 2.2](#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy) | Yes |
| OQ-SR6 | Keep each run's brief, fence and base sha in a per-run directory | 2026-11-12 | [Section 3.2](#32-concrete-directory-layout) | Yes |
| OQ-SR7 | `.tillr/state.db` is joined strictly read-only and stays the sole source of workflow state. No persistent index (owner ruling, 2026-11-18) | 2026-11-18 | [Section 6.3](#63-single-source-of-truth-which-store-is-authoritative-for-which-fact) | Yes |
| D-SR1 | Record worker test-slot waits fail-open, and show a per-run wall-time split and the run's scheduling decisions. Designer's decision; the owner may overturn it | 2026-11-20 | [Section 4.4](#44-worker-test-slot-wait-recording-run-wall-time-split-and-chronological-scheduling-decisions-d-sr1) | The wall-time split and the schedule: `run_timing.go` and `run_schedule.go` in `tools/tillr-view`. The slot-wait recording is retired by `D-SR5` |
| D-SR2 | The reader takes tokens and dollars from the run record for every backend and tool calls from the run log through a parser for each backend's log shape; an unrecognized log reads `unreadable`. **Rejected:** per-backend usage arithmetic in the reader; splitting a run's dollars across models by token share. Designer's decision; the owner may overturn it | 2026-11-27 | [Section 4.5](#45-one-reader-for-every-backend-d-sr2) | No: only the `pi` log shape is read |
| D-SR3 | Every line the shim writes is valid JSON whatever the command contains. Designer's decision; the owner may overturn it | 2026-11-27 | [Section 7.1](#71-four-mechanical-fail-open-guarantees) | No |
| D-SR4 | `run-agy` and `run-muse` set the shim as the other three adapters do, which completes [`OQ-SR2`](#122-decision-ledger). Designer's decision; the owner may overturn it | 2026-11-27 | [Section 2.2](#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy) | No |
| D-SR5 | Slot-wait recording is retired, and its readers are removed, because the owner's ruling [`OQ-GTC1`](./gate-test-capacity.md#OQ-GTC1) of 2026-11-25 deleted the cross-run slot pool. **Rejected:** keeping a wait that reads `not recorded` on every run. Designer's decision; the owner may overturn it | 2026-11-27 | [Section 4.4](#44-worker-test-slot-wait-recording-run-wall-time-split-and-chronological-scheduling-decisions-d-sr1) | No: the readers remain in `tools/tillr-view` |
| D-SR6 | Seven earlier proposals are dropped: interactive sessions, a store of command output, a retention policy of this design's own, the `llm.retry`, `tick.stage` and `boundary.check` rows with written claim verdicts, restoring staged inputs, multi-workspace queries, and scrubbing at the emitter. Designer's decision; the owner may overturn any of them | 2026-11-27 | [Section 10.2](#102-what-is-not-built) | Nothing to build |
