---
title: "Backend adapters, brief staging, and the Muse backend"
status: accepted
stage: CURRENT
verified: 2026-11-24
covers:
  - tools/tillr-run/run/request.go
  - tools/tillr-run/run/exec.go
  - tools/tillr-run/run/schema.go
  - tools/tillr-run/run/pause.go
  - tools/tillr-run/run/ending_capability.go
  - tools/tillr-run/backends/preflight.sh
  - tools/tillr-run/backends/report.py
  - tools/tillr-run/backends/usage.py
  - tools/tillr-run/backends/muse/
  - tools/tillr-run/backends/pi/
  - tools/tillr-run/backends/agy/
  - tools/tillr-run/backends/claude/
  - tools/tillr-drain/drain/policy.go
  - tools/tillr-drain/drain/seat_escalation.go
  - tools/tillr-drain/drain/stage.go
  - tools/tillr-drain/drain/failover.go
  - tools/tillr-drain/drain/backend_availability.go
  - tools/tillr-drain/drain/boundary.go
  - tools/tillr-drain/workflow/workflow.go
  - config/tillr/backend-policy.json
  - tests/tillr-muse-backend.test.py
tags: [tillr, run, drain, backends, muse, adapters, reference]
summary: "How tillr stages a ten-section lane brief, runs an agent harness behind an argv-and-record adapter contract, extracts one nineteen-key report, checks a per-run write boundary, and picks a backend from the policy file; the muse adapter ships and no seat dispatches to it."
vantage:
  status-chip: true
---

# Backend adapters, brief staging, and the Muse backend

**Status:** Source-verified 2026-11-24 against the tree.

Tillr runs an agent harness (a command-line coding agent such as `pi` or `claude`) across a process-and-record boundary, not through an in-process Go interface. `tillr-drain` picks a backend from its policy file, stages an untracked `brief.md` and `fence.txt` in the lane directory, and emits a `run.request` whose `backend` field names the backend and whose `command[0]` is that backend's adapter program under [`tools/tillr-run/backends/`](../../tools/tillr-run/backends/). `tillr-run exec` claims the lane, runs the adapter, captures its output, runs the adapter's `extract-report` helper to turn the harness's own stream into one nineteen-key `report.md`, and writes a terminal `run.record` that is materialized into the `runs` table.

Four adapters ship: `pi`, `claude`, `agy`, and `muse`. A fifth backend name, `command`, runs a workflow command node directly with no adapter. **The `muse` backend is declared and not dispatched to:** [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) lists it under `backends`, and neither `default_backend` nor any seat names it. It is in the roadmap's [icebox](../tillr/roadmap.md#icebox). The same is true of `agy` in that policy file.

| Component | Lives in |
| :--- | :--- |
| `Backend` struct, `Backend.Request`, the argv builder, `Request.Route` | [`tools/tillr-run/run/request.go`](../../tools/tillr-run/run/request.go) |
| Request check, subprocess execution, stream capture, report extraction (`readSpend`) | [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go) |
| `runs` and `quota_pauses` tables | [`tools/tillr-run/run/schema.go`](../../tools/tillr-run/run/schema.go) |
| `pause-kind` and `ending-capability` readers, continuation prompt | [`tools/tillr-run/run/pause.go`](../../tools/tillr-run/run/pause.go), [`ending_capability.go`](../../tools/tillr-run/run/ending_capability.go) |
| Shared preflight, the one `report.md` writer, the usage sidecar helper | [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh), [`report.py`](../../tools/tillr-run/backends/report.py), [`usage.py`](../../tools/tillr-run/backends/usage.py) |
| Ten-section brief template | [`tools/tillr-run/backends/brief-template.md`](../../tools/tillr-run/backends/brief-template.md) |
| Harness adapters | [`muse/`](../../tools/tillr-run/backends/muse/), [`pi/`](../../tools/tillr-run/backends/pi/), [`agy/`](../../tools/tillr-run/backends/agy/), [`claude/`](../../tools/tillr-run/backends/claude/) |
| Policy shape, `firstBackend`, seat lists and shares, seat escalation | [`tools/tillr-drain/drain/policy.go`](../../tools/tillr-drain/drain/policy.go), [`seat_escalation.go`](../../tools/tillr-drain/drain/seat_escalation.go) |
| Lane staging and brief rendering; derived fence (`FencePaths`) | [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go), [`tools/tillr-drain/workflow/workflow.go`](../../tools/tillr-drain/workflow/workflow.go) |
| Attempt classification (`ClassifyAttempt`), backend closure and probes | [`tools/tillr-drain/drain/failover.go`](../../tools/tillr-drain/drain/failover.go), [`backend_availability.go`](../../tools/tillr-drain/drain/backend_availability.go) |
| Boundary snapshots (`TakeBoundary`, `CompareRunObserved`) | [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go) |
| Tracked policies and the offline four-adapter contract suite | [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json), [`tests/tillr-muse-backend.test.py`](../../tests/tillr-muse-backend.test.py) |

---

## Adapter contract and write boundary

### Request, argv, and record seam

`run.Backend` holds one policy entry: `adapter`, `model` or a `models` rotation, and optional `effort`, `account`, `working_dir`, `timeout_seconds`, `harness_args`, `quota`, and `reserve`. `Backend.Request` is the one derivation of a `run.request`, used by the drain when it plans a dispatch and by the standalone `request` face. It builds this argv:

```text
<adapter> --backend <name> --model <model> [--effort <effort>] [--account <account>] [--timeout <seconds>] [-- <harness_args...>]
```

`--timeout` is written only for a positive `timeout_seconds`; omitted or `0` means the run has no wall ([run wall ruling](../design/run-wall-ruling.md)). A backend with a `models` rotation draws one id per run, keyed by the run id.

`checkRequest` in `exec.go` refuses a request before anything is created: a missing `run_id`, `backend`, or `command`; an absolute `working_dir`; a negative `timeout_ms`; or any `env_refs` (the config loader that would resolve them is not built). `Request.Route` reads the `--model` element back out of the argv, so the record's route is what the adapter was handed; a `command` run has no route.

`run.Exec` runs the child in its own process group, so a stop reaches the whole child tree. It exports `TILLR_RUN_ID`, `TILLR_ITEM_ID`, `TILLR_LANE_ID`, `TILLR_SESSION_MODE`, `TILLR_RESUME_PROMPT_FILE`, `TILLR_PAUSE_REQUEST`, `TILLR_PROGRESS_FILE`, and `TILLR_HARNESS_RECORD_FILE` to the adapter. The `run.record` carries an end cause, one of `normal-exit`, `settled-idle`, `repeating-call`, `time-limit`, `restart`, `signal`, `lease-reclaimed`, or `failed-to-start`.

### Adapter directory layout and preflight

Each adapter directory `tools/tillr-run/backends/<name>/` provides four files:

- `run-<name>` — the POSIX shell wrapper the request names as `command[0]`.
- `extract-report` — a Python helper `readSpend` runs after the child exits, with `--backend`, `--exit-code`, and `--out`, to write the report.
- `pause-kind` — one line read by `ReadPauseKind`: `stops-at-a-gap`, `stops-where-it-stands`, or `cannot-resume`.
- `ending-capability` — one line read by `ReadEndingCapability`: `footer`, `call-suspends`, or `call-then-message`.

Every `run-<name>` sources [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh). A preflight failure prints `run-<name>: refused: <reason>` on stderr and exits `2` before any model runs:

1. `--backend` must be the adapter's own name or a seat alias `<name>-<role>` (`require_backend`).
2. Options the adapter needs, such as `--model`, must be non-empty (`require_opt`); no adapter has a default model.
3. `--timeout`, when given, must be a whole number of seconds (`check_wall`).
4. The harness binary must be on `PATH` (`require_harness`), and the target must be the directory the run sits in (`resolve_target`).
5. The brief must carry every heading in `REQUIRED_HEADINGS` (`preflight_brief`). When the lane's staged `ending-capability` is `call-suspends`, `Finishing` replaces `Report shape` and `Stop conditions`.
6. The brief must carry no unfilled `{{NAME}}` template placeholder.

### Write boundary and per-run observation

A worker may run any inspection or test command inside its target directory, and writes only inside its item's fence (`D-MM1`). Two layers enforce this (`D-MM2`, `D-MM9`):

1. **Harness posture.** `run-muse` never passes `--disable-sandbox` or `--yolo` and roots `--workspace` at the target. `run-claude` and `run-agy` pass `--dangerously-skip-permissions`, because a headless run cannot answer a prompt, and rely on the surrounding container and the drain's snapshots. `UNMEASURED:` the live `muse` sandbox canary (a run told to attempt one write outside the worktree, with the denial confirmed in the stream and the snapshots) has never been run; [`muse/README.md`](../../tools/tillr-run/backends/muse/README.md) keeps it as a requirement of the first live run.
2. **Per-run boundary snapshots.** `TakeBoundary` records the repository's refs, its registered worktrees, and the working-tree status of the main checkout and of each named lane, before and after a run. `CompareRunObserved` splits what changed into two lists:
   - **Observed, never convicting:** ref movements and worktrees outside the drain's lanes. Ordinary git use by the owner or another agent moves a ref or a worktree registration exactly as an escaping worker would, and two snapshots cannot tell them apart (`TestOrdinaryGitUseDuringARunNeverConvictsIt`, `TestAWorktreeOutsideTheLoopsReservedPlacesIsObserved`).
   - **Convicting:** a path whose status changed in a watched sibling lane worktree, and the run's own lane worktree going away. A conviction holds the item with the reason `host-boundary-violation` (`StopHostBoundaryViolation`).

Separately, the drain checks a candidate commit's changed paths against the lane's `fence.txt` and refuses a candidate that leaves it, before the gate runs.

---

## Staged lane and brief contract

Staging prepares the lane directory for each dispatched run:

| Path | Role |
| :--- | :--- |
| `<lane-dir>/worktree/` | The git worktree the run works in (`run.WorktreeDir`). |
| `<lane-dir>/brief.md` | The brief, rendered from [`brief-template.md`](../../tools/tillr-run/backends/brief-template.md) with every placeholder filled. Untracked. |
| `<lane-dir>/fence.txt` | The derived fence, one repository-relative path per line (`fenceText`). |
| `<lane-dir>/base_sha` | The trunk commit the lane was staged from. |
| `<lane-dir>/session-id` | The harness session handle (`run.SessionIDFile`). |
| `<lane-dir>/<run-id>.log` | The adapter's stdout stream. A read-only group member writes under `<lane-dir>/nodes/<run-id>/` (`run.NodeDir`). |
| `<lane-dir>/stream.err` | The harness's stderr. |
| `<lane-dir>/report.md` | The extracted run report. |
| `<lane-dir>/durable/<run-id>/` | Per-run scratch (`run.DurableDir`): `progress`, `pause-request`, `harness`, `resume-prompt`, and the run's pinned `report.md`. |

`FencePaths` keeps the item's allowed paths first, in declared order (`TestFencePathsKeepTheItemsOwnPathsFirst`), then adds the companions a change to a `tools/tillr*` module is judged with: the module's mirror under `yolo-packs/tillr/files/local/share/tillr/<module>/`, the module's `README.md`, `yolo-packs/tillr/skills/tillr-operating/SKILL.md`, and `tests/tillr-docs.test.py`. A fence that reaches `tools/tillr-run/backends` also gains `tests/tillr-muse-backend.test.py`. The fence is derived in one place because a forgotten companion is silent.

`renderBrief` fills the template's ten sections: `Header`, `Task`, `Scope and boundaries`, `Seats`, `Settled decisions`, `Facts carried`, `Method`, `Verification commands`, `Report shape`, and `Stop conditions`. In the default lane-worktree mode the brief tells the worker to make one commit on the lane branch; in the main-checkout mode the worker leaves its edits uncommitted (`D-MM6`, `D-MM12`).

---

## Report extraction and attempt classification

### Unified `report.md` shape

After the child exits, `readSpend` runs the `extract-report` beside the adapter, copies `report.md` into the lane directory, and reads the `.usage.json` companion written next to it. A failed extraction never changes how the run ended: the usage stays unknown and the reason travels as a note on the record.

[`report.py`](../../tools/tillr-run/backends/report.py) is the single writer of `report.md`; each adapter's `extract-report` supplies only the parser for its own stream. Every report has nineteen `key: value` lines (`HEADER_KEYS`), a `---` separator, and the assistant's final text:

```text
# run report

backend: <name-or-unknown>
model: <model-id-or-unknown>
route: <route-or-unknown>
effort: <effort-or-unknown>
session: <session-id-or-unknown>
exit code: <int-or-unknown>
started: <time-or-unknown>
finished: <time-or-unknown>
stream lines: <int>
events: <int>
unparseable lines: <int>
tokens: <int-or-unknown>
cost usd: <number-or-unknown>
rate limit kind: <kind-or-unknown>
fraction: <number-or-unknown>
status: <status-or-unknown>
reset: <time-or-unknown>
complete: <yes|no>
result: <done|failed|blocked|trunk-broken|missing>

---

<body>
```

`footer_result` reads only the last non-empty line of the body against `^RESULT: (done|blocked|failed|trunk-broken)\s*$`. A `RESULT:` earlier in the body does not count, and `complete: yes` means that last line matched. The extractor's exit code states the outcome:

| Extractor exit | Constant | Condition |
| :--- | :--- | :--- |
| `0` | `EXIT_DONE` | The last line is `RESULT: done` |
| `1` | `EXIT_FAILED` | The last line is `RESULT: failed` |
| `2` | `EXIT_USAGE` | The stream could not be read or the report could not be written |
| `3` | `EXIT_BLOCKED` | The last line is `RESULT: blocked` |
| `4` | `EXIT_INCOMPLETE` | The stream carries no `RESULT:` footer |
| `5` | `EXIT_TRUNK_BROKEN` | The last line is `RESULT: trunk-broken` |
| `6` | `EXIT_HARNESS_ENDED` | No footer, and the harness ended the session itself (for example a repeated identical call) |

### Attempt classification

`ClassifyAttempt` turns one terminal `run.Record` and its log text into an `AttemptClass` (`D-MM11`). The order of the tests is the classification:

1. A `command` run that exited `0` or `1` is **answered**; any other `command` ending is **infrastructure**.
2. A run that failed to start is **infrastructure**.
3. A run that timed out, or whose adapter exited `124` from its inner `timeout`, is **unjudged**: a wall cut it short, a tree may hold half-finished work, and the run is inspected before anything is dispatched again.
4. A run that exited `0` is **answered**.
5. A run stopped by an external `SIGTERM` or `SIGINT` is **interrupted**: work is kept and no backend closes.
6. A run that exited `2` with the adapter's `refused:` line is **refused**: the brief or the environment is wrong, so the item is held for the operator with the adapter's words.
7. A non-zero exit whose log carries a `RESULT:` footer is **answered**; one with no report is **infrastructure**.

A failing test, an empty diff, an out-of-fence write, and a red gate are all **answered**, because they are statements about the work. Only **infrastructure** closes a backend.

---

## Backend policy, selection, and failover

[`policy.go`](../../tools/tillr-drain/drain/policy.go) loads the JSON document `policy_ref` names. The tracked default is [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json), with `backend-policy.*.json` variants beside it. Its keys:

- `default_backend` — required; the backend for an item that asks for no seat.
- `backends` — backend name to `run.Backend`. `Policy.Check` refuses an entry `Backend.Missing` rejects. No model id from a tracked policy may appear anywhere under `tools/tillr*` (`D-MM5`; the muse backend test scans for them).
- `seats` — seat name to one backend, or to a list. A seat that lists more than one backend must give every entry a positive `share`, and may set `pick` to `in-order`, `round-robin`, or `random`.
- `read_only_seats` and `seat_purposes` — which seats never write, and one sentence per seat for the brief.
- `escalate` — seat name to the ordered backends a seat obstacle climbs through (`escalationNext`).
- `overrides` — item id to backend, for that item's first dispatch (`D-MM8`). `checkOverrides` refuses a plan whose override names an item the store does not hold.

`firstBackend` resolves a first dispatch in order of specificity:

1. `overrides[item_id]`
2. The head of the item's seat list
3. `default_backend`

`checkSeats` refuses a plan in which an item asks for a seat the policy does not map, so the default never silently fills a named seat.

A run classified **infrastructure** closes its backend. Availability is projected from recorded facts only: the run's terminal event closes the backend, and a successful operator probe (`ProbeBackend`, recorded as `drain.backend-probe`) reopens it. A clock, a policy edit, a restart, or a quota reading does not. While a backend is closed, open backends take the work, and an item with no open backend holds with the closed backend named on its decision.

---

## Steering, continuation, and session modes

How a run is continued depends on why the earlier segment stopped and on the adapter's `pause-kind`:

- **Dirty-worktree build continuation (same `run_id`).** When a build worker ends with uncommitted in-fence work, the drain continues the same run; [build commit continuation](./tillr-build-commit.md) owns that behavior. `prepareContinuationSession` writes `<lane-dir>/durable/<run-id>/resume-prompt` (mode `0600`) and sets `TILLR_SESSION_MODE`:
  - A resumable adapter (`pi`, `claude`) gets `resume` and continues the session named in `<lane-dir>/session-id` with the continuation prompt.
  - A `cannot-resume` adapter (`muse`, `agy`), or any request that asks for a fresh session, gets `new` with a freshly minted session id. Its prompt names the earlier transcript and the preserved worktree, then carries the whole brief.
- **Pause.** A `stops-at-a-gap` adapter watches the path in `TILLR_PAUSE_REQUEST` and exits at a tool-call gap; any other adapter receives `SIGTERM`. A pause for a closed quota window is tracked in `quota_pauses`.
- **Returned turn (new run record, same session).** When a refusal sends a candidate back, the continuation prompt tells the worker to keep the earlier work and make one new commit, and carries the new run's brief (`D-UT17`).

The append-only log is what makes a resume safe: a resumed segment appends to `<run-id>.log` and never truncates it.

---

## Shipped adapters

All four adapters share `preflight.sh`, `report.py`, and `usage.py`, and [`tests/tillr-muse-backend.test.py`](../../tests/tillr-muse-backend.test.py) runs them offline against recorded streams under each adapter's `testdata/`.

| Backend | Pause kind | Ending capability | Harness invocation | Stream |
| :--- | :--- | :--- | :--- | :--- |
| `pi` | `stops-at-a-gap` | `footer` | `pi --print --mode json --model <model> --thinking <effort>`, with `--session-id` for a new session and `--session` for a resume; the prompt arrives on stdin. | JSON lines; usage is read from `turn_end` records, and a `loop_detected` record ends the report as harness-ended. |
| `claude` | `stops-where-it-stands` | `call-suspends` | `claude -p --output-format stream-json --verbose --strict-mcp-config --dangerously-skip-permissions`, with `--session-id` or `--resume`, plus `<harness_args...>`. | JSON lines, including `rate_limit_event` records that fill the report's rate-limit keys. |
| `muse` *(not dispatched)* | `cannot-resume` | `footer` | `muse model-profile show <model> --effort <effort>` to refuse an unknown model or effort, then `muse exec --json --prompt-file <prompt> --workspace <target>`, with `--permission-profile` and `--provider` passed through when given. | JSON lines; the text is rebuilt from `run.output.delta` records. |
| `agy` *(not dispatched)* | `cannot-resume` | `footer` | `agy --print --output-format json --model <model> --dangerously-skip-permissions`. The effort tier is part of the model id, so the adapter refuses `--effort`. | One JSON object. |

When `--timeout <N>` is passed, each adapter wraps its harness in `timeout <N>`; with no `--timeout` there is no inner wall.

---

## Current values

Source-verified 2026-11-24.

| Setting / identifier | Current value | Defined in |
| :--- | :--- | :--- |
| Default backend | `default_backend: "pi"`; every seat maps to `pi` except `design`, which maps to `pi-designer` | [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) |
| Backends declared and not dispatched to | `muse`, `agy` | [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) |
| Preflight refusal exit code | `2`, with `run-<name>: refused: ...` on stderr | `refuse` in [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh); `adapterRefusedExit` in [`failover.go`](../../tools/tillr-drain/drain/failover.go) |
| Adapter wall exit code | `124` | `adapterWallExit` in [`failover.go`](../../tools/tillr-drain/drain/failover.go) |
| Required brief headings | `Header`, `Task`, `Scope and boundaries`, `Seats`, `Settled decisions`, `Facts carried`, `Method`, `Verification commands`, `Report shape`, `Stop conditions` | `REQUIRED_HEADINGS` in [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh) |
| `report.md` header keys (nineteen, in order) | `backend`, `model`, `route`, `effort`, `session`, `exit code`, `started`, `finished`, `stream lines`, `events`, `unparseable lines`, `tokens`, `cost usd`, `rate limit kind`, `fraction`, `status`, `reset`, `complete`, `result` | `HEADER_KEYS` in [`report.py`](../../tools/tillr-run/backends/report.py) |
| Extractor exit codes | `0` done, `1` failed, `2` usage, `3` blocked, `4` incomplete, `5` trunk-broken, `6` harness-ended | `EXIT_*` in [`report.py`](../../tools/tillr-run/backends/report.py) |
| `pause-kind` values | `stops-at-a-gap` (`pi`), `stops-where-it-stands` (`claude`), `cannot-resume` (`muse`, `agy`) | `PauseAtGap`, `PauseWhereItStands`, `PauseCannotResume` in [`pause.go`](../../tools/tillr-run/run/pause.go) |
| `ending-capability` values | `footer` (`pi`, `muse`, `agy`), `call-suspends` (`claude`), `call-then-message` (no shipped adapter) | `EndingFooter`, `EndingCallSuspends`, `EndingCallThenMessage` in [`ending_capability.go`](../../tools/tillr-run/run/ending_capability.go) |
| Seat `pick` values | `in-order`, `round-robin`, `random` | `Policy.Check` in [`policy.go`](../../tools/tillr-drain/drain/policy.go) |
| Boundary conviction reason | `host-boundary-violation` | `StopHostBoundaryViolation` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) |

---

<a id="decision-ledger"></a>

## Why it's this way

Each row is a decision a maintainer reading only the code could undo by accident. Rows marked *owner ruling* answer the design's questions [`OQ-MM1`](#why-its-this-way) through [`OQ-MM6`](#why-its-this-way); the [decision provenance review](../tillr/decision-provenance-review.md) records which of the others the owner confirmed.

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-MM1 | The adapter contract is a write boundary, not an allowlist of commands: any inspection or verification command may run inside the target, while writes outside the boundary and changes to the store are forbidden *(2026-11-11)* | An agent needs unrestricted read and test tools to diagnose code; confining where writes land protects the repository and the queue without limiting investigation. |
| D-MM2 | The write boundary is enforced mechanically in layers — harness sandbox posture where one exists, plus before-and-after snapshots in the drain — and never by prompt text alone *(2026-11-11)* | A confused model can ignore an instruction; snapshots and the fence check detect an escape whether or not the harness cooperates. |
| D-MM3 | The `tillr-run` boundary is a subprocess argv and two records (`run.request`, `run.record`), with no wire protocol and no warm daemon *(2026-11-11)* | Command nodes and four different agent command-line tools share one lease, timeout, and logging implementation. |
| D-MM4 ([`OQ-MM1`](#why-its-this-way)) | `run.request`, `run.record`, and `runs` carry a required `backend` field, and `pi` and `agy` adapters ship beside `muse` *(owner ruling, 2026-11-12)* | Stdin and prompt-file delivery, a separate effort flag and an effort inside the model id, and a line stream and a single JSON object are all proven, so no one harness's assumptions harden into the runner. |
| D-MM5 ([`OQ-MM2`](#why-its-this-way)) | No model vendor name appears under `tools/tillr*`; model ids come only from the tracked `config/tillr/backend-policy*.json`, and preflight refuses an empty `--model` *(owner ruling, 2026-11-12)* | The tillr tree stays vendor-neutral, the active model ids stay versioned in one place, and no adapter falls back to a weaker default. |
| D-MM6 ([`OQ-MM4`](#why-its-this-way)) | A run works in an isolated lane worktree by default; an explicit main-checkout mode is allowed under a status-based fence check *(owner ruling, 2026-11-12)* | Worktrees isolate concurrent writers, and the main-checkout mode keeps a standalone `tillr-run` usable. |
| D-MM7 ([`OQ-MM6`](#why-its-this-way)) | A warm `muse` server process waits on a measurement of the cold-start cost across four runs; none is built *(owner ruling, 2026-11-12)* | A long-lived stateful service is not maintained for a startup cost nobody has measured, on a backend nothing dispatches to. |
| D-MM8 ([`OQ-MM3`](#why-its-this-way)) | Backend selection lives in the policy file (`default_backend`, `seats`, `overrides`), and an item has no backend column *(owner ruling, 2026-11-12)* | Routing is a dispatch-time choice; keeping it in the policy lets the operator switch providers for the whole queue in one edit. |
| D-MM9 ([`OQ-MM5`](#why-its-this-way)) | An unattended drain needs no namespace wrapper of its own: commit pinning and atomic landing are paired with per-run boundary checks, in which ordinary git refs and outside worktrees are observed and only a watched-lane escape convicts *(owner ruling, 2026-11-12; conviction scope ruled 2026-11-17)* | Nesting a second sandbox inside the container is avoided, cross-lane corruption is still caught, and ordinary git use beside the drain never stops an item. |
| D-MM10 | The backend is named `muse`, after the harness binary its adapter drives *(owner ruling, 2026-11-12)* | Every backend name names the command-line harness behind it, as `pi`, `agy`, and `claude` do. |
| D-MM11 | Only an **infrastructure** attempt closes a backend and moves work to another; **answered**, **refused**, **unjudged**, and **interrupted** attempts never do *(2026-11-12)* | Running judged work on a second model pays twice to learn what the gate already said, while a harness or provider outage says nothing about the item. |
| D-MM12 | A lane-worktree build run commits its deliverable on the lane branch, and a main-checkout run never commits *(2026-11-12)* | The gate and the lander get an immutable tree and commit message, and no worker advances the trunk. Recovery from an uncommitted exit is in [build commit continuation](./tillr-build-commit.md). |
