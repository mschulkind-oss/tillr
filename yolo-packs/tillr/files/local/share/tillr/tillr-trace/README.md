# tillr-trace — the query side of a recorded run

`tillr-trace` turns artifacts a run already left on disk into four answers: what one
tick did in order, what one run was asked / ran / changed / cost, what each lane and
route spent, and every case where a lane's report claims more than the record shows.
A **tick** here is one unattended planning round of the loop, named by the `tick_id` a
run's `manifest.json` carries.

The design is [Session Recording](https://github.com/mschulkind-oss/tillr/blob/main/docs/design/session-recording.md): its layout,
`trace_key`, `obs.event` schema, store authority and claim verification sections describe
what this tool reads.

## What is authoritative, and what this is

**Nothing in this directory is a source of truth, and nothing it does is written down.**
Every command parses the artifacts it needs into an in-memory database, answers, and exits;
no index, cache or other file is created anywhere:

| Fact | Authoritative store | What `tillr-trace` does |
| --- | --- | --- |
| item, lane, run, candidate, decision, dispatch state | `.tillr/state.db` | opens it **read-only** (`mode=ro` + `query_only`) and attaches nothing |
| staged brief, fence, base/tree/head sha | `runs/<run-id>/` under the trace root, or `.tillr/loop/<run-id>/` | reads their paths and shas |
| what the model saw, spent, and called | `<run-id>.log` (`pi --mode json`) | parses `llm.turn` and `tool.call` rows |
| what actually ran | `proc-exec.jsonl` | parses `proc.exec` rows |
| what the gate planned and judged | `.tillr/loop/<run-id>/gate.{plan,verdict}.json` | parses `gate.plan` and `gate.verdict` rows |

## Where a run's records are read from

Two directories hold one run's artifacts, and both are read:

- `<trace-root>/runs/<run-id>/` — the recorder directory;
- `<root>/.tillr/loop/<run-id>/` — the drain's per-run record directory, which is where
  the run adapters' emitters write (`proc-exec.jsonl`), where staging keeps the run's own
  `brief.md` / `fence.txt` / `base_sha`, and where the gate leaves `gate.plan.json` and
  `gate.verdict.json`.

The loop directory is read first, and each artifact is taken from the first directory
that has it. `--store` is what names it: `.tillr/loop` is the store file's sibling, so a
store at `<root>/.tillr/state.db` means `<root>/.tillr/loop`.

## The emitters

[`bash-shim.sh`](./bash-shim.sh) is the shell-command recorder. The `run-pi` and
`run-claude` adapters under `tools/tillr-run/backends/` name it in `BASH_ENV`, so every
non-interactive `bash` a run spawns sources it and appends one line per shell command to
`$TILLR_TRACE_DIR/proc-exec.jsonl`. The adapters resolve this directory as
`../../../tillr-trace` from their own: the sibling directory in a checkout, and
`~/.local/share/tillr/tillr-trace` from a pack install (`just pack-sync` mirrors this
directory into the pack).

There is no git recorder. The adapters unset `GIT_TRACE2_EVENT` for a run, because every
test suite a run starts would inherit it and record each of its own git commands. This
tool does not read `git-trace2.jsonl` and produces no `git.trace2` row; such a file on
disk is ignored. A run's `git_path` and `git_state` exist only because `tillr-view` reads
them by name: the path is always null and the state is always `retired`.

The shim is fail-open. It uses shell builtins only (no `date`, no `jq`, no fork per
command), saves `$?` as the trap's first instruction and returns it from the EXIT trap,
caps each line's command text at 512 bytes, and swallows every write failure. A shell
with no `TILLR_TRACE_DIR`, or a directory it cannot write, records nothing and behaves
exactly as it would without the shim. A run's `proc_state` is computed from what is on
disk, so a recorded run reads `ok` and one without the emitter reads `missing` — and so
does one whose `proc-exec.jsonl` exists but is empty: a writer that touched the path is
not a run that ran nothing.

## No index: every answer is read from the artifacts

There is no persistent index. A SQLite index of the trace would be cached derived data:
every table in it can be rebuilt from the store and the run artifacts, so it keeps nothing
the artifacts do not. The rule is that no such cache exists until a measured performance
problem asks for one. The loop runs no indexer, `ingest` is an unknown command (exit 2),
and nothing creates or reads an `index.db`.

Each command reads only what it needs:

- **One run** (`audit <run-id>`, and `run <run-id>` for `tillr-view`) reads that run's log,
  `proc-exec.jsonl`, gate records and staged inputs.
- **One tick** (`tick`) reads the runs whose manifest names that tick.
- **Aggregates** (`cost`, `discrepancies`) read the **100 most recent runs** by the store's
  start time (`DEFAULT_RUNS`), which is a bounded default, not the whole corpus. `--runs N`
  reads N of them, and `--runs 0` reads every run. `cost` reads only the `turn_end` lines of
  each log.

The bound exists because an aggregate parses every artifact it covers on every call.
Measured on a corpus of about 1,200 runs (6.4 GB of logs, 2.2 GB of `proc-exec.jsonl`),
`cost` takes about 2 seconds over 100 runs and 20 seconds over every run, `discrepancies`
about 20 seconds over 100 runs and two and a half minutes over every run, and an `audit`
of a run with 268,000 `proc.exec` rows about 10 seconds.

When a row and a source file disagree, the source file is right, and every command prints
the artifact path each answer was read from. Cite that path, not the row.

A source that cannot be parsed does not abort a command: the parser that raised leaves
that source `corrupt` with the exception named, drops that source's partial rows, and the
run reads `unreadable` in the audit, while every other run is still read. A tool event
whose `args`, `result` or `structuredContent` is text instead of an object keeps its row
and loses only the field it could not read.

The parser reads a JSONL source one line at a time, so peak memory is the rows it keeps,
not the size of the largest file.

The store is never written, opened read-write, or migrated: `Store` in
[`tillr_trace.py`](./tillr_trace.py) creates its connection with `file:...?mode=ro` and a
`PRAGMA query_only` lock behind it, and the test suite runs the CLI against a read-only
store copy and asserts a write on that connection raises.

## The commands

| Command | Answers |
| --- | --- |
| `tillr-trace tick <id\|--last>` | one unattended tick, in order — every `obs.event` carrying that `tick_id`, plus `tick.decision` rows lifted from the store's `dispatches`. An empty tick says so; it is not reported as a verified quiet one. |
| `tillr-trace audit <run-id>` | the run's lane/item/store state, its preserved `brief.md`/`fence.txt`/`base_sha` locations, its model turns with tokens and cost, the calls it made with exit codes, and every claim verdict. |
| `tillr-trace run <run-id>` | the same run as one JSON document — `run` (its stream states and artifact paths), `events` (every row with its payload) and `claims` — which is what `tillr-view` reads. An unknown run answers `"run": null`. |
| `tillr-trace cost [--runs N]` | per lane, per route, per model — runs, retries, LLM wall time, tokens in / cache-read / out / reasoning, US dollars — over the N most recent runs. |
| `tillr-trace discrepancies [--since T] [--all] [--runs N]` | every claim the record does not support, over the N most recent runs. `--all` adds `unverified` rows. `--since` keeps the claims of runs that finished at or after `T`; a claim's time is its run's finish time, not the time it was read. |

Common inputs: `--trace-root`, the recorder directory whose `runs/<run-id>/` is read
(default `$TILLR_TRACE_ROOT`, or `$XDG_STATE_HOME/tillr-trace/v1`); `--store` (default
`.tillr/state.db`); and `--loop-dir` (default: the store's sibling `loop`). The trace root
is only read: a root that does not exist is a root with no runs in it.

## The log shape the parser reads

The parser reads `pi --mode json` output, and
[`testdata/canary-run.log`](./testdata/canary-run.log) pins the shape:

- one `session` line: `id` (the session id), `cwd` (the worktree), `version`;
- one `turn_end` per model turn, whose `message` carries `provider`, `model`,
  `responseModel`, `api`, `thinkingLevel`, `stopReason`, `durationMs`,
  `requestStartedAt`, `timestamp`, `credential.fingerprint`, and
  `usage` = `input` / `output` / `cacheRead` / `cacheWrite` / `reasoning` /
  `totalTokens` plus `cost.total`. That one line is every number `cost` reports;
- one `tool_execution_start` / `tool_execution_end` pair per tool call, with
  `toolCallId`, `toolName`, `args` (`path` for `read`/`edit`/`write`, `command` for
  `bash`), `result.structuredContent` (including `exit_code` for `bash`), `isError`
  and `durationMs`.

Two shape facts the parser depends on:

1. **`tool_execution_*` events carry no timestamp.** The only clocks in the stream are
   `session.timestamp`, `request_start.timestamp` and `turn_end.message.timestamp`. A
   `tool.call` row is therefore stamped with the last clock written before it, and its
   payload says `ts_source: "derived_from_preceding_clock"`. A tool call's time is good
   to the turn, not to the millisecond — which is why the temporal join below compares
   a tool call's derived time against a `proc.exec` row's own time only for *ordering*,
   and reports the anchor it used.
2. **Tokens and cost live on the assistant message, not on a separate usage event.**
   `message_start`/`message_update` carry partial usage; only `turn_end` is complete,
   so the parser reads `turn_end` alone and never double counts.

## The post-edit temporal join

A test suite attests code only if it ran **after** the last edit to what it tests. For
each module the run edited (from `tool.call` rows of `edit`/`write`, mapped to a module
directory by `module_of`), `verify_run` computes:

- `T_edit(D)` — the latest `tool.call` edit timestamp under module `D`;
- `E_test(D)` — the latest `proc.exec` whose `repo_rel_cwd` is `D` and whose argv is a
  test command (`go test`, `pytest`, `node --test`, `python3 …test…py`, `just done`,
  `uvx`, `npm test`), with `ts_start > T_edit(D)` and `exit_code == 0`.

No such row, with a passing suite that ran *only before* the edit → **disproved**,
naming the module and the edit time. A passing suite after the edit → **verified**.
Two more claim classes ride along: prose quantifiers (*"all five suites exit 0"*
compared with the number of distinct modules observed passing) and backticked command
claims, plus `RESULT: done` on a run whose `head_sha == base_sha` while its tree moved
(the uncommitted-worktree contradiction).

**Verdicts are exactly `verified`, `disproved`, `unverified`.** `unverified` is what a
claim gets when the record cannot answer it — no `proc-exec.jsonl` at all, no recorded
candidate shas, a log that would not parse. Absent evidence is never promoted to proof
or to disproof.

What would fool the join:

- **An edit made only through a shell command** (`sed -i`, `cat > f`, a heredoc). The
  shim records argv and cwd, not a path list, so `proc.exec` cannot say which file was
  written; counting such a command as an edit of its cwd would condemn valid runs. A
  module edited only that way shows no owed suite. `edit`/`write` tool calls are the
  only edit sources.
- **A suite that runs code outside its own `repo_rel_cwd`** (a workspace-wide
  `go test ./...` from the repository root) is attributed to the directory it ran in,
  so per-module claims read it as "no suite ran for this module".
- **A test command that is not one of the recognized forms.** It is invisible as a
  suite and the module reads as owed-but-untested, so the tool errs toward
  `disproved` — never toward a false pass.
- **Clock skew between one process and another** within a turn: `tool.call` times are
  derived (above), so an edit and a test in the same second can order either way. The
  comparison is `>`, not `>=`, so a row that ties is not counted as "after".
- **A row written by the run itself.** `proc-exec.jsonl` is an ordinary file, and the
  shell a run controls can append a well-formed `proc.exec` row for a suite that never
  ran; the join reads rows as written. A claim verdict is therefore evidence
  about an *honest* lane's prose, never about a hostile one's.
- **A commit that squashes a later edit into an earlier sha.** The join reads tools and
  commands, never git objects, so nothing here substitutes for the gate.

## What this does not show

- **No prompt or tool-result content.** A `tool.call` row keeps the tool name, the target
  path, a `bash` command preview, the error flag, the exit code and the result's byte
  count. Excerpts are capped at 200 characters and scrubbed of anything shaped like a
  credential (`Bearer …`, `sk-…`, `ghp_…`, `AKIA…`,
  `--token/--api-key/--password/--secret`).
- **No full process environment, no `.env` values.** `proc-exec.jsonl` is the shim's
  own record and is read as written; this tool adds no environment capture of its own.
- **No cross-workspace view and no interactive sessions.** Only the run artifacts named
  above are read.
- **No `steps.jsonl`, boundary, retry or patch records.** `.tillr/loop/<run-id>/steps.jsonl`
  is not parsed, and `tick` shows only what the store's `dispatches` and the trace rows
  carry.

One row kind is this tool's own: `unreadable`. It records an artifact that could not be
read (missing log, unreadable store, corrupt `proc-exec.jsonl`), because the alternative —
an empty result — reads as "nothing happened", and that is the one answer a recorder must
never give by accident.

## Tests

```bash
python3 tools/tillr-trace/test.py     # prints each case and exits 0
```

The fixture [`testdata/canary-run.log`](./testdata/canary-run.log) is a `pi --mode json`
stream recorded from a live run and trimmed to its `session`, `turn_end`,
`tool_execution_start` and `tool_execution_end` lines (`message_start`, `message_update`
and `agent_end` are dropped). The token, cost and tool-call totals it carries are that
run's — 7 turns, 36196 input / 1917 output / 216192 cache-read / 438 reasoning tokens,
`$0.004746410`, 6 tool calls — and the test asserts against those recorded numbers. Fixture
directories are created in the session's durable scratch directory, never in the system
temporary one.
