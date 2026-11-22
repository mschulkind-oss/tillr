---
title: "Contract: tillr-view"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, view]
summary: "What tillr-view provides and expects, read from the code at dab5a2da0e: twelve faces, three record kinds, and the HTTP API its own web client reads. It owns no table and is the largest consumer: it names thirty-three tables in its own SQL, links fourteen drain packages, runs four other programs, and its tests write other utilities' rows by hand in 96 files. All three heavy failures were its suite."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-view

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** One Go program at `tools/tillr-view` and a web client under `web/`. It shows the store: status, what is waiting and why, lanes, runs, and the observatory server. It is a `main` package, so nothing can import it.

## 1. What it provides

### 1.1 Faces

| Face | What it prints |
| :--- | :--- |
| `status` | `view.status`, or text |
| `events` | `view.events` |
| `running` | `view.running` |
| `waiting` | With `--json`, an array of rows: `alarm quota graph_changes place item_id run_id backend lane_id document observation class position where why mover since waited class_source lane_status lane_reason`. Otherwise a table and a summary line |
| `passes`, `lanes`, `alarms` | With `--json`, an array |
| `run` | With `--json`, one object |
| `trace` | Text, from `tillr-trace` |
| `perf-log` | A summary of the server's own performance log |
| `restart` | The request id; with `--wait` the answer. Exit 1 when refused, 4 when the store is missing |
| `serve` | The HTTP server. `serve --check` opens the store and exits without listening |

Exit codes are the library's six.

One program runs a face and reads what it prints: the roller runs `restart --root --wait` and reads the two-line answer. The drain files a restart request that a running server answers, and names the program in two lists (`rg -n '"tillr-view"' tools -g '*.go' -g '!*_test.go' -g '!tools/tillr-view/*'` finds only those). Every other face is run by people and agents, from the operating skill.

### 1.2 The HTTP API

Read by the web client in the same directory, and by nothing else. Every answer is `snapshot` and `data`, or `snapshot` and `error` with `code` and `message`. Routes under `/api/v1`: `snapshot`, `why`, `waiting`, `passes`, `landings`, `lanes`, `designs`, `questions`, `perf/checks`, `perf/items`, `checks/{id}`, `items/{id}` and its `doc`, `runs/{id}` and its seven parts, the `ws` socket, and three that write: `items/{id}/place`, `ideas`, `client-timing`.

### 1.3 Go packages, events, tables

None. It registers no kind and owns no table.

### 1.4 Files and environment

Its performance log, which only it reads. It reads `TILLR_VANTAGE_BASE_URL`, `TILLR_VIEW_PERF_LOG` and `TILLR_VIEW_PERF_STARTUP`.

### 1.5 Names others match on

None that a program reads. The drain keeps a number equal to the view's session allowance, 5000 ms, by a comment.

## 2. What it expects

From the drain:

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| `why --json`: `rows`, `summary`, `build`; and that `build` equals the view's own build stamp | [tillr-drain](./tillr-drain.md) | subprocess | `why.go`, `siblings.go` |
| Fourteen packages: `alarms faults heavy holdrules lanestate obstacles ownercriteria patchmoves presence preserved quota restart spend stop`, by the names in [the drain's package table](./tillr-drain.md#12-go-packages) | [tillr-drain](./tillr-drain.md) | Go import | 18 files |
| For a hold reason: a class, a place and a mover; and the seven class names | [tillr-drain](./tillr-drain.md) | Go import (`holdrules`) | `waiting.go`, `lanes.go`, `lane_conditions.go` |
| Seventeen tables by column: `dispatches drain_passes drain_pass_spend drain_sessions gate_spans backend_windows node_runs node_artifacts node_reused graph_revisions retries last_designers comment_runs comment_deliveries comment_arrivals comment_reply_states comment_confirmation` | [tillr-drain](./tillr-drain.md) | SQL | `serveread.go`, `passes.go`, `waiting.go`, `designers.go`, and more |
| Event payloads by JSON path: `drain.refusal` (`step item_id detail replan`), `drain.started` and `drain.node-advanced` (`decision.item_id`, `decision.run_request.run_id`), `drain.gate-stall` (`candidate.item_id`), `drain.post-landing-action` (`action what detail kind`), `drain.lane-refused`, `drain.lane-restored`, `drain.retry.ack`, `drain.revert`, `drain.orphaned` | [tillr-drain](./tillr-drain.md) | SQL on `events` | `waiting.go`, `run_schedule.go`, `item_history.go`, and six more |
| The loop directory: `drain-*.json`, `backend-policy.json`, and per run `request.json brief.md fence.txt base_sha steps.jsonl pipeline.jsonl slot-waits.jsonl gate.spans.json` | [tillr-drain](./tillr-drain.md) | files | `serveobs.go`, `passes.go`, `lanes.go`, `running.go` |
| The restart request, answer and build fact, which it also writes | [tillr-drain](./tillr-drain.md) | Go import (`restart`) | `restart.go` |

From the others:

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, exit codes, the read-only store opener, the definition, the Vantage index; `events` by column; the recovery remedy's wording | [tillr](./tillr.md) | Go import, SQL | 43 files |
| `place` and `idea`: their flags, `item.place` and `item.idea`, and eight rejection rules | [tillr-item](./tillr-item.md) | subprocess | `serveplace.go`, `serveidea.go` |
| `workflowmeta.ReadSelection` | [tillr-item](./tillr-item.md) | Go import | `serve.go` |
| Ten tables by column: `items item_criteria item_edges decisions idea_intake design_without_item item_sync_residue comment_intake comment_intake_documents comment_batches`; the JSON in `items.waiting_on`; the six states | [tillr-item](./tillr-item.md) | SQL | `serveread.go`, and nine more |
| `item.record`, `item.verdict`, `item.conflict`, `item.update` (`placed_from`), `item.sync` payloads | [tillr-item](./tillr-item.md) | SQL on `events`, records | `payloads.go`, `serveread.go`, `waiting_conflict.go`, `status.go` |
| The idea words file's path | [tillr-item](./tillr-item.md) | files | `idea_words.go` |
| `runs`, `lanes`, `read_only_runs`, `quota_pauses` by column; run states and end causes | [tillr-run](./tillr-run.md) | SQL | `serveread.go`, and nine more |
| `run.record`, `run.request`, `run.allocate`, `run.resume`, `run.finish`, `run.usage`, `run.progress`, `run.child-session` payloads | [tillr-run](./tillr-run.md) | SQL on `events`, records | `payloads.go`, `usage.go`, `worker_records.go` |
| The harness stream log in the lane directory | [tillr-run](./tillr-run.md) | files | `transcript.go` |
| `landings` by column; `land.landing`; the result `squash-conflict`; the published ref's name | [tillr-land](./tillr-land.md) | SQL, Git | `landings.go`, `waiting.go`, `push_summary.go` |
| `candidates`, `landing_attempts`, `land.candidate`, `land.attempt`: retired, still read | [tillr-land](./tillr-land.md) | SQL | `serveread.go`, `payloads.go` |
| `gate.change`, `gate.plan` and `gate.verdict` files in the run directory; the `gate.verdict` event (`run_id verdict_id result`) | [tillr-gate](./tillr-gate.md), and [tillr-drain](./tillr-drain.md) for the event | files, SQL | `run_schedule.go`, `serveobs.go`, `running.go` |
| `run <id>`: the document `run`, `events`, `claims` | [tillr-trace](./tillr-trace.md) | subprocess | `serveread.go` |
| `proc-exec.jsonl`, which it also parses itself | [tillr-trace](./tillr-trace.md) | files | `run_timing.go`, `transcript.go` |

## 3. Contract and private

**Contract:**

- The faces as people run them: their names, their main flags, and the exit codes. The three record kinds' envelopes.
- `serve --check` and `restart --wait` with its answer words and exit codes, because the roller and the drain depend on a server that can be checked and restarted.

**Private:** nearly all of it.

- The HTTP API. Its one consumer ships in the same directory and is built with it; the web client's tests hold those shapes. It is the view's inner seam and not a seam between utilities.
- The `waiting` rows and every other `--json` array. No program reads them. If one starts to, the rows it reads become an entry.
- All text output, the summary line included.

That the view provides almost nothing to other utilities is the point of this file: its contract is short, and its list of expectations is the longest of the thirteen.

## 4. How the contract is pinned

**What the view provides** is pinned in `tools/tillr-view`: `TestHelpIsTheFaceSurface` exists; a test of `serve --check` and one of `restart`'s exit codes are added.

**What the view expects** is pinned in each provider, and this is where the view's suite changes most.

- **No sibling is built.** A view test that needs the planner's answer reads the drain's `why` examples. `buildPair`, `buildFreshDrainBinary` and the six builds of `tillr-item` go.
- **No row is typed.** A view test that needs a store asks the owners' builders for the situation: an item blocked on the owner, a held dispatch, a finished run, a landing. `serve-fixture.sql` and the 28 inline `CREATE TABLE` statements go.
- **No sentence is compared.** A view test compares a row's `reason`, `class`, `where` and the owner part of `mover`. The 21 files that compare `why` and `mover` text change to names.
- **No rule is walked.** `TestWaitingEveryDrainReasonHasARule` moves to the drain, where a new reason without a rule already fails.
- **One test of its own stays for the join:** given the drain's example answer, the view's `waiting` row carries the same reason, class and mover. That tests the view's code against a fixed input, which is all a consumer's suite should need.

The view keeps one check against the real planner: the build-stamp match at run time, which refuses a planner from a different build. That is a deployment check and not a test.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- Into the drain: RI-01 (builds and runs the drain, compares its sentences), RI-02 (writes drain tables by hand), RI-03 (SQL on seventeen drain tables and JSON paths), RI-04 (a second copy of the planner's rule), RI-05 (copies of drain shapes and numbers), RI-06 (writes the store through `restart`), RI-07 (fourteen packages with no list of names), RI-08 (builds over every module).
- Into the item utility: RI-09 to RI-12.
- Into the runner, land and the gate: RI-20 (queries a run state nothing writes), RI-30 to RI-32, RI-34, RI-39.
- Into the library and `tillr-trace`: RI-45 (the recovery wording), RI-57 (an unnamed decode of the trace document, and a second parser of the shim's lines).
