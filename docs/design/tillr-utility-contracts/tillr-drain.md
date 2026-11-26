---
title: "Contract: tillr-drain"
status: in-review
stage: DESIGN
tags: [tillr, contract, drain]
summary: "What tillr-drain provides to the other tillr utilities and what it expects from them: its faces, its reader packages, the planner's answer, the hold reasons and their rules, its tables, its event kinds and the run directory. It is the largest provider and the largest consumer, and the pair it forms with tillr-view is the first slice of the build."
depends-on:
  - ../tillr-utility-contracts.md
---

# Contract: tillr-drain

**Status:** The contract examples and their checks are not built: `fd -t d '^contract$' tools` finds nothing. The lists below describe what the code provides and reads. This is one of the thirteen files of [every tillr utility has a written contract](../tillr-utility-contracts.md), which defines entry, expectation, contract example, reach-in and part, and holds the rules, the one open question and the build order. Which parts are contract and which are private is the designer's decision, and the owner may overturn it.

**What it is.** The drain plans the queue and drives the other utilities through it. It is one Go module at `tools/tillr-drain` with a command package, the engine package `drain`, and a set of smaller packages. The command package and `drain` are two **parts**: each has its own test suite, and a change in one can break the other's.

## 1. What it provides

### 1.1 Faces

`faceList` in `main.go` names them: `probe-backend`, `workflow`, `why`, `policy`, `plan`, `once`, `split`, `retry`, `clear-fault`, `accept-rewrite`, `acknowledge-patch-move`, `push`, `sweep-refs`, `acquit`, `examine`, `set-aside`, `boundary`, `resume`, `scale`, `stop`, `restart`, `loop`.

Exit codes are the library's six. These faces are run or named by another utility:

| Face | Read by | Arguments they pass | What they read back |
| :--- | :--- | :--- | :--- |
| `why --json` | `tillr-view` | `--store --root --json`, and optionally `--now --policy --lanes --questions-index - --questions-cmd` | One JSON object: `rows`, `summary`, `build`, and also `alarms`, `lanes`, `definition`, `trunk`. A row has `place item_id state position reason why class where mover since class_source waiting_on decision lane_status lane_reason refusals landing`. The summary has `lanes_busy lanes_reserved lanes_workers lanes_examining lanes_offered running could_run held owner_waiting drain_next design_next operator_next` |
| `loop --check` | `tillr-roll` | `--root --store --gate --land --verdict` | Exit 3 for a store this build cannot open, and the printed line `tillr-drain: schema migration required`; under `D-UC25` ([the overview](../tillr-utility-contracts.md#810-the-loop-verifies-support-files-and-install-root-revision-at-startup), the designer's decision from an operator request, which the owner may overturn), exit 2 naming both the binary and the missing support file or mismatched `<install_root>/tillr/buildstamp/support-revision` |
| `loop` | `tillr-roll`'s keeper, `tillr-store`'s schema test | `--root --gate --land --verdict --policy --lanes` and more | The process itself, found by name; the loop result file |
| `restart --wait` | `tillr-roll` | `--root --wait` | Two printed lines; the second holds the answer word `restarted` or `refused` |
| `stop` | the keeper | `--root` | Exit 0, or 2 when there is nothing to stop |
| `plan` | `tillr-view`'s tests | `--store --input --output` | A file holding an array of `drain.decision` records |
| `retry`, `clear-fault`, `acknowledge-patch-move`, `push`, `probe-backend` | people; `tillr-item` and `tillr-view` print them as remedies | — | — |

`StopExitCode` maps the loop's stop reason to its exit: `queue-drained` and `operator-stop` are 0, `host-boundary-violation` and `lanes-leased` are 1, `gate-harness-error` is 4, anything else 5.

### 1.2 Go packages

| Package | What it answers | Linked by | Names they use |
| :--- | :--- | :--- | :--- |
| `holdrules` | For a hold reason: its class, where it sits, who moves it | view | `For Rule Rules` |
| `obstacles` | The recorded obstacle holds | view | `Hold Read`; tests `HeldKind NewHold` |
| `alarms` | Backend alarms | view | `Alarm Closed Print Read ReadPolicy Window` |
| `faults` | Item faults that hold a lane | view | `Read` |
| `heavy` | The trunk's proof state | view | `Green HeavyStatus ProofAge ReadStatus` |
| `lanestate` | Which lanes hold a candidate | view | `Read` |
| `ownercriteria` | Criteria only the owner can meet | view | `OwnerCriterion ReadOwnerCriteria` |
| `patchmoves` | Patch-branch moves awaiting acknowledgment | view | `Pending`; tests `Acknowledge` |
| `presence` | Whether a loop owns the store; dead runs | view | `DeadRuns Owner SessionOwnerGone`; tests `CurrentOwner OpenLoopPresence StoreIdentity` |
| `preserved` | Work kept from ended runs | view | `PreservedWork ReadPreservedWork` |
| `quota` | Provider quota readings | view | `ProbeInterval QuotaStatus ReadStatuses`; tests `DDL QuotaWindow` |
| `restart` | The restart request, answer and build fact, read and written | view | `Answer AnswerRefused AwaitAnswer Build BuildFact ErrSuperseded File FileDigest LauncherCandidate PublishBuild ReadBuild ReadState Request Restarter RoleDrain RoleView` |
| `spend` | Recorded cost | view | `Read` |
| `stop` | Why the loop stopped, as a sentence and a next step | view | `Explain Explanation` |
| `redcap` | The gate policy; the red-cap hold type | land | `Hold` |
| `drain` | The engine | the command package; `tillr-store` and `tillr-watch`, for no name | see [§3](#3-contract-and-private) |

### 1.3 Records and event kinds

Records: `drain.decision` (written to `plan`'s output file), `drain.loop` (the loop result file), and the push outcome printed by `push --json`.

Event kinds: the drain registers its kinds under the owner `drain`, found with `rg -o '"drain\.[a-z0-9.\-]+"' tools/tillr-drain -g '!*_test.go'`. Other utilities read these by name:

| Kind | Read by | Fields they read |
| :--- | :--- | :--- |
| `drain.refusal` | view, item, watch rules | `step item_id detail replan` |
| `drain.revert` | view, item, watch rules | `result landing item_id run_id` |
| `drain.graph-completed` | item | `item_id` |
| `drain.started`, `drain.node-advanced` | view | `decision.item_id`, `decision.run_request.run_id` |
| `drain.gate-stall` | view | `candidate.item_id` |
| `drain.post-landing-action` | view | `action what detail kind` |
| `drain.lane-refused`, `drain.lane-restored` | view | `reason detail` |
| `drain.retry.ack` | view | the retry's id |
| `drain.obstacle-hold`, `drain.obstacle-hold-cleared` | view (through `obstacles`) | the `Hold` |
| `drain.restart-request`, `drain.restart-answer`, `drain.build-started` | view (through `restart`), `tillr-roll` | `role revision digest modified` |
| `drain.roll-failed` | written by `tillr-roll`, read by `alarms` | `commit step detail mover` |
| `drain.orphaned`, `drain.repair-held`, `drain.examined` | view | kind only |

The drain also appends `gate.plan` and `gate.verdict` events, which wrap the gate's records with nine fields of its own, and `boundary.check` and the five `call.*` kinds.

### 1.4 Tables

The drain's tables are in [the overview's list](../tillr-utility-contracts.md#23-who-owns-which-table). These are read from outside with the reader's own SQL:

| Table | Read by |
| :--- | :--- |
| `dispatches` | view, `tillr-trace` |
| `drain_passes`, `drain_pass_spend`, `drain_sessions`, `gate_spans`, `backend_windows` | view |
| `node_runs`, `node_artifacts`, `node_reused`, `graph_revisions` | view; the library's judge migration |
| `retries` | view, item |
| `workflow_selections` | item, the library's judge migration |
| `trunk_history` | land, which also writes it |
| `trunk_rewrites` | land, item |
| `last_designers`, `comment_runs`, `comment_deliveries`, `comment_arrivals`, `comment_reply_states`, `comment_confirmation` | view |

The rest have no reader outside the drain's module.

### 1.5 Files, refs and environment

| Place | Read by |
| :--- | :--- |
| `.tillr/loop/drain-<first>-<last>.json`, the loop result | view |
| `.tillr/loop/backend-policy.json` | view, the drain's `alarms` |
| `.tillr/loop/<run>/`: `steps.jsonl`, `pipeline.jsonl`, `brief.md`, `fence.txt`, `base_sha`, `run.record.json`, `run.stderr`, `gate.spans.json` | view, `tillr-trace`, `tillr-store`'s inventory |
| `.tillr/lanes/<lane>/`: the request file, `brief.md`, `session-id`, `ending-capability`, `landing-answer.json` | `tillr-run` and its adapters |
| `<store>.loops/<hash>.present` | view, through `presence` |
| `refs/tillr/green`, `refs/tillr/runs-long/`, `refs/tillr/held/`, `refs/tillr/published/<remote>/<branch>` | view, `tillr-roll` |
| `<git common directory>/tillr-gate/heavy.json`, `green.json` | view, through `heavy` |
| Environment for children: `TILLR_RUN_ID`, `TILLR_LOOP_STORE`, `TILLR_RUN_RESUME` to `tillr-run`; `TILLR_GATE_SPANS_OUT`, `TILLR_FAST_VERDICT_OUT`, `TILLR_FORK_DRIFT`, `TILLR_HEAVY_LOCK_FD` to gate programs; `TILLR_LAND_OBSERVE` to the land step; `TILLR_LOOP_ADAPTER_BUILD` to the adapter | each named utility |
| The process name `tillr-drain` or `tillr-drain-<hex>` with the argument `loop` | the keeper |

### 1.6 Names others match on

- **Hold reasons.** The keys of `holdrules.Rules`, with prefix forms such as `trunk-rewritten: `. `TestEveryReasonConstantHasAHoldRule` holds the list against the engine's constants.
- **Classes.** `alarm`, `needs you`, `waits on work`, `needs a cause`, `ready`, `moving`, `waits on the loop`.
- **Obstacle kinds.** `owner`, `trunk`, `tooling`, `seat`, `definition`; a hold's reason code is `obstacle-<kind>`.
- **Alarm kinds.** `backend-closed`, `backend-window`, `judge-backend-unavailable`.
- **Stop reasons.** `queue-drained`, `lanes-leased`, `operator-stop`, `host-boundary-violation`, `gate-harness-error`, `fault`.
- **Retry acknowledgments.** `gate-failed`, `unjudged`.
- **Restart answers.** `restarted`, `refused`.

Sentences other utilities compare, which the contract makes private: every mover's text after its owner, every `why` text after its reason code, the alarm line, the stop explanation's sentence and next step, and the `loop: …` report lines.

## 2. What it expects

| Expectation | Provider | Through | Where it is read |
| :--- | :--- | :--- | :--- |
| The envelope, exit codes, store opener, registries, event log, definition, settings, Vantage index | [tillr](./tillr.md) | Go import | throughout |
| Names of the `item` package: state changes, verdicts and judging, conflicts and waits, comments, sync, stored reads | [tillr-item](./tillr-item.md) | Go import | throughout `drain` |
| `docshare`: `Attributes Fence ReadQueue Start` | [tillr-item](./tillr-item.md) | Go import | `deny_list.go`, `document_prerequisite.go` |
| `tillr-item state`, its flags, exit codes and the `item.rejection` record's `rule` | [tillr-item](./tillr-item.md) | subprocess | `steps.go` |
| `tillr-item sync --json`, the `item.sync` record, and that exit 1 means it ran | [tillr-item](./tillr-item.md) | subprocess | `sync.go` |
| Rows of `items`, `item_criteria`, `item_edges`, `decisions`, `comment_taken`, `request_words`, `idea_intake`; and writes to `items`, `item_criteria`, `comment_intake`, `comment_intake_documents` | [tillr-item](./tillr-item.md) | SQL | throughout `drain` |
| Names of the `run` package: the request and record types, states, end causes, pause kinds, lane functions, directory helpers | [tillr-run](./tillr-run.md) | Go import | throughout `drain` |
| `tillr-run exec`, its arguments, and the `run.record` it prints | [tillr-run](./tillr-run.md) | subprocess | `steps.go` |
| Rows of `runs`, `lanes`, `read_only_runs`, `quota_pauses`, read and written | [tillr-run](./tillr-run.md) | SQL | throughout `drain` |
| The report footer's result word; adapter exit codes 2 and 124; the adapter directory's file names | [tillr-run](./tillr-run.md) | files | `failover.go` |
| `land`: `DecodeVerdict LandedRefPrefix OpenLock OpenTrunk PublishedRefPrefix RecordReplayLanding Trunk Verdict VerdictPass VerdictPassInherited` | [tillr-land](./tillr-land.md) | Go import | `drain` |
| `land.landing` from the land step: `result reason conflict_paths trunk_after lock_observation red_cap`; exit 0 landed, 1 refused | [tillr-land](./tillr-land.md) | subprocess, through the adapter | `steps.go`, `loop.go` |
| Rows of `landings`; the land lock file; the ref prefixes | [tillr-land](./tillr-land.md) | SQL, files | `drain` |
| `gate.verdict` and `gate.plan` as files; `tillr-gate checks --heavy-tree`; exit 0, 1, 6 | [tillr-gate](./tillr-gate.md) | subprocess, files | `steps.go`, `heavy_steps.go` |
| The check declarations under `[gate] checks_dir` in `.tillr.toml` (default `tools/gate/checks`; `D-UC23` of [the overview](../tillr-utility-contracts.md#88-the-check-declarations-directory-is-configured-in-tillrtoml), the designer's decision from an operator request, which the owner may overturn), including optional `<checks_dir>/format.json` | the operated repository, outside tillr | files | `heavy_steps.go`, `fixer_gate.go`, `fixer/fixer_gate.go`, `format_check.go`, `steps.go`, `obstacle_hold.go`, `workflow/workflow_task_check.go` |
| The brief template, `backends/brief-template.md`, as the default of `--brief-template`, read from the runner's installed directory. Not so today: the default is `<root>/tools/tillr-run/backends/brief-template.md`. `D-UC16` of [the overview](../tillr-utility-contracts.md#81-defaults-come-from-tillrs-installation), amended by `D-UC25` ([§8.10](../tillr-utility-contracts.md#810-the-loop-verifies-support-files-and-install-root-revision-at-startup)), the designer's decisions from operator requests, which the owner may overturn | [tillr-run](./tillr-run.md) | files | `main.go`, `recovery.go` |
| The fork gate, now the `tillr-drain fork-gate` face (built into `tillr-drain`, invoked from `steps.go`) rather than `fork-gate.py`. `D-UC16` | itself | subprocess | `steps.go` |
| The adapter's `describe` answer, its three step faces and `--check-argv` | [tillr-loop](./tillr-loop.md) | subprocess; an embedded copy | `hooks.go`, `adapter_build.go` |
| The roll command and what it prints | [tillr-roll](./tillr-roll.md) | subprocess | `post_landing.go` |

## 3. Contract and private

**Contract:**

- The `why --json` answer: the row and summary fields listed in [§1.1](#11-faces), with `reason`, `class`, `where`, `class_source` and the owner part of `mover` as names.
- The name sets of [§1.6](#16-names-others-match-on).
- The reader packages, by the names in [§1.2](#12-go-packages), and `redcap.Hold` until slice 8 moves it.
- Of the `drain` package: the names the command package uses in production. The further names its tests use are listed only if a test needs them after slice 3; the default is private.
- The faces and exit codes in [§1.1](#11-faces) that another utility runs.
- The event kinds and fields in [§1.3](#13-records-and-event-kinds), and the tables in [§1.4](#14-tables) for the columns read today.
- The places in [§1.5](#15-files-refs-and-environment).

**Private:**

- Every sentence. A mover is contract as "who" (`owner`, `operator`, `drain`, `design seat`) and private as "how".
- Everything a face prints on standard error, including `loop: …` lines.
- The other tables, the other event kinds, and their payloads.
- The plain-text output of `why`.
- Every exported name of `drain` and of the reader packages that is not listed.

## 4. How the contract is pinned

All pins live in `tools/tillr-drain` and run in its fast suite, in process.

- **The planner's answer.** For each example situation, the engine's `why` function is called on a fixture store and its JSON is compared with the example file. Examples cover each class, each obstacle kind, a conflict in each status, an owner-only criterion, a backend alarm, the empty queue, and a queue with every lane busy.
- **Name sets.** One test per set compares the file with the constants. The hold-reason test exists (`TestEveryReasonConstantHasAHoldRule`) and gains the file.
- **Reader packages.** One file per package uses every listed name with the caller's types. Each reader is run on the example stores and its answer compared.
- **Events.** One test compares the registered kinds with the list. Each kind another utility reads has a payload example made by the code that appends it.
- **Faces.** `stop`, `restart --wait` and `loop --check` are called in process for each exit code another utility branches on.
- **Builders.** `drain` publishes builders that make a hold, a dispatch, a pass and a session through its own write path. The view's tests use them in place of typed rows.
- **The engine for the command package.** The same, one level in: the engine's examples are what a command-package test reads, and the command package compares names and not report lines.

## 5. Reach-ins to fix

Listed with their fixes in [the overview](../tillr-utility-contracts.md#10-reach-ins-today).

- As provider: RI-01 to RI-08 (the view), RI-13 and RI-14 (its own command package), RI-24 (the item utility), RI-28 (land), RI-51 to RI-54 (the roller and keeper), RI-55 (`tillr-trace`).
- As consumer: RI-16 to RI-19 and RI-21 (the runner), RI-22, RI-23, RI-25, RI-26 (the item utility), RI-27, RI-29 to RI-32 (land), RI-34, RI-36 to RI-38 (the gate), RI-42 (registrations), RI-49 (the adapter copy).
