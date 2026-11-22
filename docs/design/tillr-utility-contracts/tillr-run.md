---
title: "Contract: tillr-run"
status: in-review
stage: DESIGN
updated: 2026-11-21
tags: [tillr, contract, run]
summary: "What tillr-run provides and expects, read from the code at dab5a2da0e. It has two parts: the Go program and `run` package, which give the drain the run record, lanes, run states and end causes; and the adapter scripts, which give the Go part the report, the pause kind and the ending capability. The drain writes three of its four tables, and the report footer has three parsers with three grammars."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-run

**Status:** 2026-11-21. Read at `dab5a2da0e`; nothing here is built. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** The runner starts one agent in a lane and records what it did. It lives at `tools/tillr-run` and has two **parts**, each with its own tests in its own language:

- **The Go part**: the program `tillr-run` and the package `run`.
- **The adapters**: the scripts under `backends/`, one directory per agent harness, and the shared `report.py`. An **adapter** is the script that starts one harness and turns its output into the one report shape.

The Go part runs the adapters and reads the files they write, so the adapters are a provider to it. This pair has broken twice ([the overview's failures](../tillr-utility-contracts.md#3-the-failures-this-answers)).

## 1. What it provides

### 1.1 Faces

| Face | Flags others pass | What it prints | Exit |
| :--- | :--- | :--- | :--- |
| `exec` | `--store --lane-root --input --now`, and `--resume`, `--backends-dir` | `run.record` | The run's own exit code. The face's own failures keep 2 to 5, and are told from a run that exited 2 to 5 only by whether a record was printed |
| `request` | `--policy --backend --item --lane --run`, and `--seat --workflow --node --now` | `run.request` | 0; 2 for a backend the policy lacks |
| `lanes` | `--store` | `run.lanes` | 0 |
| `status` | `--store --run` | `run.status` | 0; 1 when the run is not in the store |
| `release` | `--store --lane --reason` | `run.release` | 0 released; 1 refused |
| `pause` | `--store --run`, and `--pause-timeout-seconds` | one line of text | 0; 2 when the run is not live |
| `mark-copied-usage`, `copied-usage` | `--store` and their own | text; a bare JSON array | 0 |

Only the drain runs `exec`, with exactly the first four flags and sometimes `--resume`.

### 1.2 Go packages

One: `run`. Only the drain imports it. The 48 names its production code uses:

- **Records and kinds:** `Record Request RequestRecord StatusRecord Release ArtifactRef Usage Backend`, and `RecordKind RequestKind AllocateKind FinishKind ReleaseKind ResumeKind UsageKind CopiedUsageKind PauseRequestKind`, and `Utility Version`.
- **States and causes:** `StateExited StateFailedToStart StatePaused StateTimedOut`, `EndNormalExit EndSettledIdle EndRestart EndSignal EndLeaseReclaimed EndRepeatingCall`, `LaneStateIdle LaneStateLeased`, `PauseAtGap PauseWhereItStands PauseCannotResume`, `EndingFooter EndingCallSuspends`, `ReleaseReleased`.
- **Functions:** `Apply ClaimLease ReleaseLaneInTx ReapExpiredLeases ReadEndingCapability ReadSessionID IsSessionID`, and the directory helpers `DurableDir NodeDir WorktreeDir`, and `SessionIDFile`.

### 1.3 Records and event kinds

Records: `run.record`, `run.request`, `run.lanes`, `run.status`, `run.release`. Of these, `run.request`, `run.lanes` and `run.status` are never appended as events.

| Event kind | Object | Payload fields |
| :--- | :--- | :--- |
| `run.allocate` | lane | `run_id item_id backend seat workflow node lane_id started_at correlation_id leased_until` |
| `run.finish` | run | the whole record: `run_id item_id lane_id backend route harness seat workflow node state end_cause signal settled_at harness_ended_at resumed exit_code started_at finished_at event_seq artifact_refs usage spend_status spend_note ending_exception repeating_call loop_recovery extension_set` |
| `run.resume` | run | `request run_id item_id lane_id backend session_id resumed_at` |
| `run.release` | lane | `lane_id run_id leased_until released_at result reason terminal_record` |
| `run.pause-request` | run | `timeout_ms` |
| `run.progress` | run | `run_id item_id lane_id criterion_id source_line` |
| `run.child-session` | run | `run_id item_id lane_id session_id role started_at_ms ended_at_ms turn_count end_clock transcript` |
| `run.usage`, `run.usage.copied`, `run.children-recorded`, `run.observation-error`, `run.record` | run | their own |

The request's payload: `read_only_group run_id item_id node_id seat workflow node lane_id backend command working_dir timeout_ms env_refs scratch_env correlation_id resume session_turn resume_prompt fresh_session`.

### 1.4 Tables

| Table | Read from outside by | Written from outside by |
| :--- | :--- | :--- |
| `runs` | drain, view (ten files), item, `tillr-trace` | — |
| `lanes` | drain (fourteen files), view | drain |
| `read_only_runs` | drain, view | drain |
| `quota_pauses` | drain, view | drain: the runner holds the DDL, and the rows are inserted by the drain |

### 1.5 Files and environment

| Place | Written by | Read by |
| :--- | :--- | :--- |
| `<lane>/<run>.log`, the harness's output | the Go part | drain, view, `tillr-trace` |
| `<lane>/report.md` and `<scratch>/report.md` | the Go part, from the adapter's extractor | drain |
| `<scratch>/progress`, one criterion id per line | the agent | the Go part |
| `<scratch>/landing-offer.json` | the adapters | the Go part's extractor, drain |
| `<scratch>/pause-request`, `resume-prompt` | the Go part | the adapters |
| `<scratch>/harness`, `extension-set` | the adapters | the Go part |
| `<adapter>/pause-kind`, `ending-capability`, `extract-report` | the repository | the Go part; the drain reads `ending-capability` through the package |

The environment `exec` sets for the adapter: `TILLR_RUN_ID`, `TILLR_ITEM_ID`, `TILLR_NODE_ID`, `TILLR_LANE_ID`, `TILLR_SESSION_MODE`, `TILLR_RESUME_PROMPT_FILE`, `TILLR_PAUSE_REQUEST`, `TILLR_PROGRESS_FILE`, `TILLR_CHILD_SESSIONS_ROOT`, `TILLR_CHILD_SESSIONS_MARKER`, `TILLR_READ_ONLY_GROUP`, `TILLR_PI_BUILDS_DIR`, `TILLR_PI_AGENT_DIRS_DIR`, `TILLR_HARNESS_RECORD_FILE`, `TILLR_EXTENSION_SET_RECORD_FILE`, `TILLR_RUN_EXEC_ID`, and the scratch directory variable. `TILLR_RUN_ID` and `TILLR_LANE_ID` are also read by `tillr-trace`'s shim and by the drain, from a process's environment.

### 1.6 Names others match on

- **Run states.** `exited`, `failed-to-start`, `timed-out`, `paused`.
- **End causes.** `settled-idle`, `normal-exit`, `restart`, `time-limit`, `lease-reclaimed`, `signal`, `failed-to-start`, `repeating-call`.
- **Lane states.** `leased`, `idle`.
- **Pause kinds.** `stops-at-a-gap`, `stops-where-it-stands`, `cannot-resume`.
- **Ending capabilities.** `footer`, `call-suspends`, `call-then-message`.
- **Release results and reasons.** `released`, `refused`; `lease-expired`, `lease-unexpired`, `lane-free`, `lane-unknown`.
- **Spend status.** `reported`, `unreported`, `not-applicable`.

### 1.7 The adapters' surface

What the adapter part provides to the Go part and to the drain:

- **The report.** A first line `# run report`; then one `key: value` line per header key, in this order: `backend`, `model`, `route`, `effort`, `session`, `exit code`, `started`, `finished`, `stream lines`, `events`, `unparseable lines`, `tokens`, `cost usd`, `rate limit kind`, `fraction`, `status`, `reset`, `complete`, `result`; then a line `---`; then the agent's text.
- **The footer.** The last non-blank line of the text: `RESULT: ` and one of `done`, `blocked`, `failed`, `trunk-broken`.
- **The obstacle block.** A line `OBSTACLE: <kind>` with kind one of `definition`, `trunk`, `tooling`, `seat`, `owner`, and field lines `CRITERIA EVIDENCE PATHS CHECKS BASE COMMAND TRIED MISSING QUESTION`.
- **The extractor's exit codes.** 0 done, 1 failed, 3 blocked, 4 no footer, 5 trunk-broken, 6 ended by the harness, 2 usage.
- **The adapter's exit codes.** 2 when it refused before running anything, with a line `run-<adapter>: refused: …`; 124 when the time limit ended it.
- **The usage file** beside the report: `models`, `turns`, `first_output_ms`, `output_tokens_per_second`.
- **The landing offer:** `call_id commit base transcript`, or for an obstacle `call_id tool base obstacle`.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, exit codes, store opener, registries, event append | [tillr](./tillr.md) | Go import | throughout |
| The `run.request` the drain writes, and the flags it passes `exec` | [tillr-drain](./tillr-drain.md) | subprocess, files | `main.go` |
| In the lane directory: `brief.md`, `session-id` (a UUID), `ending-capability`, `base_sha`, `fence.txt` | [tillr-drain](./tillr-drain.md) | files | `run/exec.go`, the adapters |
| `landing-answer.json` and `landing-state.json` in the scratch directory | [tillr-drain](./tillr-drain.md) | files | the adapters |
| `TILLR_LOOP_STORE`, `TILLR_RUN_ID`, `TILLR_RUN_RESUME` in its environment | [tillr-drain](./tillr-drain.md) | environment | `run/children.go` |
| That the drain inserted the `lanes` and `read_only_runs` rows for a read-only group before `exec` | [tillr-drain](./tillr-drain.md) | SQL | `run/exec.go` |
| The brief's required headings and its target line | [tillr-drain](./tillr-drain.md), from the runner's own template | files | `backends/preflight.sh`, the adapters |
| The shell shim, found by a relative path, and the loop directory it writes into | [tillr-trace](./tillr-trace.md), [tillr-drain](./tillr-drain.md) | files | `backends/pi/run-pi`, `backends/claude/run-claude` |
| **The Go part from the adapters:** everything in [§1.7](#17-the-adapters-surface), and the three files of each adapter directory | the adapter part | subprocess, files | `run/exec.go`, `run/settled.go`, `run/pause.go` |
| **The adapters from the Go part:** the argument list `--backend --model --effort --account --timeout`, the working directory, and the environment of [§1.5](#15-files-and-environment) | the Go part | subprocess | each `run-<harness>` |

It imports no other utility's package and runs no other tillr program.

## 3. Contract and private

**Contract:**

- `exec`, `status`, `release` and `lanes` with the flags and exits above; `run.record`'s fields; `run.request`'s fields.
- The seven name sets of [§1.6](#16-names-others-match-on).
- The 48 package names, and three to add so that the drain stops writing run tables: a claim for a read-only group, a settle for a read-only run, and a quota-pause write.
- `runs` and `lanes` for the columns read today. `read_only_runs` and `quota_pauses` become private once those three functions exist.
- `run.finish`, `run.allocate`, `run.resume` and `run.progress` with the fields listed.
- The whole of [§1.7](#17-the-adapters-surface), as names: the header keys, the four footer words, the obstacle kinds and fields, the two sets of exit codes.
- The lane and scratch file names of [§1.5](#15-files-and-environment), and the environment names.

**Private:**

- The text faces: `pause`, `mark-copied-usage`. `copied-usage`'s array.
- Everything on standard error, and the refusal line's words after `refused:`.
- The stream log's contents, the settled-idle rule, child-session discovery.
- The other event kinds' payloads.

## 4. How the contract is pinned

Each part pins its own side, in its own fast suite.

- **Exists today.** `TestTheRunUtilityRegistersItsOwnContract`, `TestExecAnswersTheRunsExitCodeAndStillRecordsIt`, `TestExecKeepsTheReservedCodesForTheFacesOwnFailures`, `TestStatusReportsAMissingRunAsANegativeDecision`, `TestTerminalEndCauses`, `TestShippedEndingCapabilities`, `TestExecRequestEnvironment`.
- **The adapters' examples.** The adapter part's Python suite renders one report per situation: each footer word, no footer, a harness that ended the run, a refused quota window, a commit offer, an obstacle offer. It writes each as a file with the extractor's exit code beside it. These are contract examples: the Python suite fails if `report.py` no longer produces them.
- **The Go part reads them.** `run`'s tests read those files and no longer start Python to learn the shape. `TestFinalReportFromCommitOffer` compares the two header values and the footer word, by name, against the commit-offer example.
- **One footer rule.** The footer words and the header keys are a name-set file in the adapter part. `report.py`, `run/settled.go` and the drain each compare against it; the drain's loose reading (the last `RESULT:` anywhere, bold allowed) either becomes the rule for all three or goes.
- **Name sets.** One test per set of [§1.6](#16-names-others-match-on) compares the file with the constants.
- **The record.** One `run.record` example per state and end cause, made by `exec` on the `command` backend, in process.
- **Builders.** `run` publishes builders that make a lane, a run in each state, and a finished run with its events, through `Apply` and `ClaimLease`. The view, the item utility and `tillr-trace` use them in place of typed `INSERT` lines.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- Between its own parts: RI-15 (header keys typed three times), RI-16 (three footer parsers), RI-17 (the obstacle field table typed three times).
- As provider: RI-18 (the drain derives adapter file names and types adapter exit codes), RI-19 (the drain writes run tables), RI-20 (run states typed as literals elsewhere), RI-21 (file names typed twice).
- As consumer: RI-18 (the adapters find `tillr-trace`'s shim by a relative path).
