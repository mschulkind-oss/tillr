# tillr-trace — the query side of a recorded run

`tillr-trace` turns artifacts a run already left on disk into four answers: what one
tick did in order, what one run was asked / ran / changed / cost, what each lane and
route spent, and every case where a lane's report claims more than the record shows.

Design: [Session Recording](../../docs/design/session-recording.md) [Section 3.2](../../docs/design/session-recording.md#32-concrete-directory-layout) (layout),
[Section 4.2](../../docs/design/session-recording.md#42-the-five-part-hierarchical-trace_key) (`trace_key`), [Section 4.3](../../docs/design/session-recording.md#43-obsevent-jsonl-schema) (`obs.event`), [Section 6.3](../../docs/design/session-recording.md#63-single-source-of-truth-which-store-is-authoritative-for-which-fact) (store authority), [Section 8.3](../../docs/design/session-recording.md#83-mechanical-claim-verification-tillr-trace-verify-claims) (claim
verification), [Section 10.1](../../docs/design/session-recording.md#101-scope-and-files-touched-estimate-34-hours-430-loc) step 3 (this scope).

## What is authoritative, and what this is

**Nothing in this directory is a source of truth.** Every row it writes is derived and
rebuildable:

| Fact | Authoritative store | What `tillr-trace` does |
| --- | --- | --- |
| item, lane, run, candidate, decision, dispatch state | `.tillr/state.db` | opens it **read-only** (`mode=ro` + `query_only`) and attaches nothing |
| staged brief, fence, base/tree/head sha | `runs/<run-id>/` under the trace root, or `.tillr/loop/<run-id>/` | copies them into the index |
| what the model saw, spent, and called | `<run-id>.log` (`pi --mode json`) | parses `llm.turn` and `tool.call` rows |
| what actually ran | `proc-exec.jsonl` | parses `proc.exec` rows |
| what the gate planned and judged | `.tillr/loop/<run-id>/gate.{plan,verdict}.json` | parses `gate.plan` and `gate.verdict` rows |

## Where a run's records are read from

Two directories hold one run's artifacts, and both are read:

- `<trace-root>/runs/<run-id>/` — the recorder directory of
  [design §3.2](../../docs/design/session-recording.md#32-concrete-directory-layout);
- `<root>/.tillr/loop/<run-id>/` — the drain's per-run record directory, which is where
  `run-pi`'s emitters write (`proc-exec.jsonl`), where staging keeps
  the run's own `brief.md` / `fence.txt` / `base_sha`, and where the gate leaves
  `gate.plan.json` and `gate.verdict.json`.

The loop directory is read first, and each artifact is taken from the first directory
that has it. `--store` is what names it: `.tillr/loop` is the store file's sibling, so a
store at `<root>/.tillr/state.db` means `<root>/.tillr/loop`.

## The emitters

[`bash-shim.sh`](./bash-shim.sh) is the `BASH_ENV` half of
[design §2.2](../../docs/design/session-recording.md#22-architectural-ruling-collector-commandgit-shim-and-in-process-hook-no-network-proxy):
`run-pi` names it in `BASH_ENV`, so every non-interactive `bash` a run spawns sources it
and appends one line per shell command to `$TILLR_TRACE_DIR/proc-exec.jsonl`. The pack
ships this whole directory (`just pack-sync` mirrors it), and `run-pi` resolves it as
`../../../tillr-trace`: `~/.local/share/tillr/tillr-trace` from a pack install, the
sibling directory from this checkout.

There is no git recorder. The design's second emitter, git's `GIT_TRACE2_EVENT`, was removed
on 2026-11-17 by the owner's ruling: every test suite a run starts inherits it, and one run
recorded 918,054 git commands, 906 runs held about 82 GB, and the index built from them was
32 GB. The indexer no longer reads `git-trace2.jsonl` and writes no `git.trace2` row; a file
left on disk by the old recorder is ignored. Index layout 4 is the first without those rows,
and an index of layout 3 is deleted and rebuilt on the next sync. `runs_index.git_path` and
`git_state` remain only because `tillr-view` selects them by name: the path is always NULL
and the state is always `retired`.

The shim is fail-open
([§7.1](../../docs/design/session-recording.md#71-four-mechanical-fail-open-guarantees)):
the shim uses shell builtins only (no `date`, no `jq`, no fork per command, which is how
the ≤ 1.5 ms budget is held), saves `$?` as the trap's first instruction and returns it
from the EXIT trap, caps each line's command text at 512 bytes, and swallows every write
failure. A shell with no `TILLR_TRACE_DIR`, or a directory it cannot write, records
nothing and behaves exactly as it would without the shim. `runs_index.proc_state` is computed from what is on disk, so a run recorded this way reads `ok` and
one without the emitter still reads `missing` — and so does one whose `proc-exec.jsonl`
exists but is empty: a writer that touched the path is not a run that ran nothing.

`index.db` (default `$TILLR_TRACE_ROOT/index.db`) is Tier 3 of the design's retention
policy: drop it and rebuild with `--rebuild`. When an index row and a source file
disagree, the source file is right. Never cite an index row as evidence of what a run
did — cite the artifact path it was read from, which every command prints.

## How the index stays current

`ingest` is incremental. The trap it removes: the store's modification time changes on
every tick by design, so a watermark that covered the store made every tick rebuild the
whole corpus — measured on this repository's own corpus (2.7 GB of artifacts, 498,776
rows), a tick cost 69 s of re-parsing that had nothing new in it.

- **The watermark covers artifacts, not the store.** `ingested_files` records each
  artifact's size, modification time, the last byte parsed and the last row ordinal. The
  store is read on every sync (it is small, and authoritative for the rows it owns) and
  never used as a rebuild trigger.
- **A source is parsed from the last byte it was read to.** A build appends the rows the
  new lines justify; the rows already stored keep their ids, because an obs id is the
  source file's stamp plus the row's ordinal in that file. A run with nothing new parses
  nothing.
- **One source cannot abort the ingest.** A parser that raises on a source leaves that
  source `corrupt` with the exception named in its detail, drops that parse's partial rows,
  and the run reads `unreadable` in the audit; every other run is still indexed. A tool
  event whose `args`, `result` or `structuredContent` is text instead of an object keeps
  its row and loses only the field it could not read (2,091 of 5,316 measured tool results
  carried `structuredContent` as text on 2026-11-17).
- **A per-source carry crosses the boundary.** A tool call's start can land in one build
  and its end in the next, and so can a command's start and exit; the args, the last
  clock, the last turn number, the session id and the running token totals travel in the
  source's `carry` so neither a pair nor a total is lost or double counted.
- **A source that shrank is re-read from zero.** Its rows are deleted first, so a
  rewritten or truncated artifact replaces its rows instead of doubling them. A file that
  was only appended to is never re-read from the start — a build that resumed is proof
  the prefix was not touched.
- **The layout has a version.** `PRAGMA user_version` is the schema's; a mismatch
  deletes the file and rebuilds rather than migrating, because the index is derived and a
  half-built one is the one answer this tool must never give. Deleting is deliberate:
  emptying a large index in place journals every page of it first.
- **No statement in a sync walks the whole event table.** The row counter is kept in
  `index_meta` rather than read as `MAX(seq)`, one source's rows are deleted by their id
  range rather than by the unindexed `source` column, a gate record is re-read only when
  it changed, and a `tick.decision` already indexed is not written again. The suite asks
  SQLite's query planner about every statement a sync runs.
- **Each run is its own transaction, in WAL mode.** A sync stopped part way keeps every
  run it finished, and the next one starts after them. A killed writer's uncommitted work
  is ignored by the next open instead of being rolled back, and a reader never meets a
  hot journal it cannot recover — the defect that turned one killed ingest into a
  whole-corpus rebuild on 2026-11-17.
- **One writer at a time, without waiting.** A sync holds an exclusive lock on
  `index.db.lock` beside the index. An `ingest` that finds it held prints `index: busy`
  and exits 0 at once: the sync already running, or the next one, reads what is new.

The parser reads a JSONL source one line at a time, so peak memory is the rows it keeps,
not the size of the largest file: a 25 MiB log peaks around 450 KiB while parsing.

The store is never written, opened read-write, or migrated: `Store` in [`tillr_trace.py`](./tillr_trace.py)
creates its connection with `file:...?mode=ro` and a `PRAGMA query_only` lock behind it,
and the test suite runs the CLI against a `chmod 555` store copy and asserts a write on
that connection raises.

## The commands

| Command | Answers |
| --- | --- |
| `tillr-trace ingest` | the write the drain loop asks for after each tick, in the background: sync `index.db` with the artifacts the tick's runs left — parsing only what changed — and print where it is. Every face below syncs first, so this is the one a supervisor needs when it has no query to make. |
| `tillr-trace tick <id\|--last>` | acceptance Q4: one unattended tick, in order — every `obs.event` carrying that `tick_id`, plus `tick.decision` rows lifted from the store's `dispatches`. An empty tick says so; it is not reported as a verified quiet one. |
| `tillr-trace audit <run-id>` | acceptance Q2: the run's lane/item/store state, its preserved `brief.md`/`fence.txt`/`base_sha` locations, its model turns with tokens and cost, the calls it made with exit codes, and every claim verdict. |
| `tillr-trace cost` | acceptance Q1: per lane, per route, per model — runs, retries, LLM wall time, tokens in / cache-read / out / reasoning, US dollars. |
| `tillr-trace discrepancies [--since] [--all]` | acceptance Q7: every claim the record does not support. `--all` adds `unverified` rows. |

Common flags: `--trace-root` (default `$XDG_STATE_HOME/tillr-trace/v1`, or
`$TILLR_TRACE_ROOT`), `--store` (default `.tillr/state.db`), `--index`, `--rebuild`.
The loop passes `--store` explicitly and lets `--trace-root` default unless its own
`--trace-root` names one, so the index a later read opens is the one the tick built.

## The real log shape this parser was written against

Measured on `pi --mode json` output from a live canary run (2026-11-12), and pinned by
[`testdata/canary-run.log`](./testdata/canary-run.log):

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

Two shape facts the parser depends on, both measured:

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

## The post-edit temporal join ([Section 8.3](../../docs/design/session-recording.md#83-mechanical-claim-verification-tillr-trace-verify-claims))

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

What would fool the join, stated rather than hidden:

- **An edit made only through a shell command** (`sed -i`, `cat > f`, a heredoc). The
  shim records argv and cwd, not a path list, so `proc.exec` cannot say which file was
  written; counting such a command as an edit of its cwd would condemn valid runs. A
  module edited only that way shows no owed suite. `edit`/`write` tool calls are the
  only edit sources.
- **A suite that runs code outside its own `repo_rel_cwd`** (a workspace-wide
  `go test ./...` from the repository root) is attributed to the directory it ran in,
  so per-module claims read it as "no suite ran for this module".
- **A test command that is not one of the recognized forms.** It is invisible as a
  suite and the module reads as owed-but-untested, i.e. the tool errs toward
  `disproved` — never toward a false pass.
- **Clock skew between one process and another** within a turn: `tool.call` times are
  derived (above), so an edit and a test in the same second can order either way. The
  comparison is `>`, not `>=`, so a row that ties is not counted as "after".
- **A row written by the run itself.** `proc-exec.jsonl` is an ordinary file, and the
  shell a run controls can append a well-formed `proc.exec` row for a suite that never
  ran; `verify-claims` reads rows as written. A `claim.verdict` is therefore evidence
  about an *honest* lane's prose, never about a hostile one's, and no nonce would change
  that — the environment a nonce would have to be kept from is the one the run has.
- **A commit that squashes a later edit into an earlier sha.** The join reads tools and
  commands, never git objects, so nothing here substitutes for the gate.

## What this deliberately will not show

- **No prompt or tool-result content.** Excerpts are capped at 200 characters and
  scrubbed of anything shaped like a credential (`Bearer …`, `sk-…`, `ghp_…`, `AKIA…`,
  `--token/--api-key/--password/--secret`). Only `bash` command previews are kept;
  `read`/`write` bodies, assistant text, and `bash` output are stored as paths, sizes
  and digests, never as text.
- **No third-party conversation or browser DOM dumps** ([Section 5.1](../../docs/design/session-recording.md#51-what-is-excluded-and-how-it-is-enforced-at-two-mechanical-gates) rule 4): those tool names
  produce a row with timing and byte count only.
- **No full process environment, no `.env` values.** `proc-exec.jsonl` is the shim's
  own record and is read as written; this tool adds no environment capture of its own.
- **No cross-workspace federation** (`--all-workspaces` is Slice 3) and **no interactive
  `~/.pi/agent/sessions/` indexing** (Slice 2): only the run artifacts named above.

## What is not parsed yet

`.tillr/loop/<run-id>/steps.jsonl`, `boundary.check`, `tick.stage`, `llm.retry` (the
pi-fork hook), `claim.verdict` emission back into the shard files, `diff.patch.zst`, and
`cas/sha256/` blobs. `tick.stage` is only as good as
the manifest the emitters will write; until they land, `tick` shows what the store's
`dispatches` and the trace rows carry.

One row kind is **not** in the design's twelve: `unreadable`. It records an artifact
the indexer could not read (missing log, unreadable store, corrupt `proc-exec.jsonl`),
because the alternative — an empty result — reads as "nothing happened" and that is the
one answer a recorder must never give by accident.

## Tests

```bash
python3 tools/tillr-trace/test.py     # prints each case and exits 0
```

The measured fixture is [`testdata/canary-run.log`](./testdata/canary-run.log): a real `pi --mode json` stream
trimmed to its `session`, `turn_end`, `tool_execution_start` and `tool_execution_end`
lines (`message_start`/`message_update`/`agent_end` dropped). The token, cost and
tool-call totals it carries are the live run's — 7 turns, 36196 input / 1917 output /
216192 cache-read / 438 reasoning tokens, `$0.004746410`, 6 tool calls — and the test
asserts against those measured numbers, not against a re-estimate. Fixture directories
are created in the session's durable scratch directory, never in the system temporary one.
