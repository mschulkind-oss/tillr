---
title: "Backend adapters, brief staging, and the Muse backend"
status: accepted
stage: CURRENT
verified: 2026-11-20
verified_commit: f7a5e29047375e1a233bf1f65be4f0d6b3013c14
covers:
  - tools/tillr-run/run/request.go
  - tools/tillr-run/run/exec.go
  - tools/tillr-run/run/schema.go
  - tools/tillr-run/run/spend.go
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
  - tools/tillr-drain/drain/stage.go
  - tools/tillr-drain/drain/failover.go
  - tools/tillr-drain/drain/backend_availability.go
  - tools/tillr-drain/drain/boundary.go
  - config/tillr/backend-policy.json
  - tests/tillr-muse-backend.test.py
tags: [tillr, run, drain, backends, muse, adapters, reference]
summary: "How tillr stages ten-section lane briefs, dispatches agent harnesses behind an argv-and-record adapter contract, extracts nineteen-key reports and usage sidecars, enforces per-run write boundaries, routes seats and infrastructure failovers across backends, and retains the dormant Meta Muse adapter."
vantage:
  status-chip: true
---

# Backend adapters, brief staging, and the Muse backend

**Status:** Verified 2026-11-20 against `f7a5e29047375e1a233bf1f65be4f0d6b3013c14`.

Tillr runs agent harnesses across a process-and-record boundary rather than an in-process Go interface. `tillr-drain` selects a backend row from its policy, stages an untracked `brief.md` and `fence.txt` in the claimed lane directory, and emits a `run.request` whose `backend` field names the adapter and whose `command[0]` points at that adapter's `run-<name>` executable under [`tools/tillr-run/backends/`](../../tools/tillr-run/backends/). `tillr-run exec` claims the lane lease, launches the adapter inside the target directory, captures its stdout stream and stderr, invokes the sibling `extract-report` helper to normalize harness-specific output into one nineteen-key `report.md` plus an optional `.usage.json` sidecar, and records a terminal `run.record` materialized into `runs`.

Four harness adapters ship under `tools/tillr-run/backends/`: `pi`, `claude`, `agy`, and `muse` (alongside the built-in `"command"` execution path for deterministic workflow command nodes). The shared adapter contract, brief preflight, report extractor, write-boundary diffing, and seat/preference failover are live in production drains. **The Meta `muse` backend itself is dormant and [iceboxed](../tillr/roadmap.md#icebox):** the Meta subscription lapsed on 2026-11-12, so [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) sets `default_backend` to `"pi"` and `preference` to `["pi"]`, keeping `muse` and `agy` declared for policy validation and offline fixture tests without dispatching live work to them.

| Component | Lives in |
| :--- | :--- |
| `Backend` struct, `Command()` builder, and `run.request` / `run.record` validation | [`tools/tillr-run/run/request.go`](../../tools/tillr-run/run/request.go) |
| Subprocess execution, stream capture, lease claim/release, and `runs` DDL | [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go), [`schema.go`](../../tools/tillr-run/run/schema.go) |
| Post-run `extract-report` invocation, `report.md` publication, and `.usage.json` reader | [`tools/tillr-run/run/spend.go`](../../tools/tillr-run/run/spend.go) |
| Adapter `pause-kind` and `ending-capability` readers and continuation prompt staging | [`tools/tillr-run/run/pause.go`](../../tools/tillr-run/run/pause.go), [`ending_capability.go`](../../tools/tillr-run/run/ending_capability.go) |
| Shared shell preflight, nineteen-key `report.md` writer, and `.usage.json` sidecar helper | [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh), [`report.py`](../../tools/tillr-run/backends/report.py), [`usage.py`](../../tools/tillr-run/backends/usage.py) |
| Tracked ten-section brief template | [`tools/tillr-run/backends/brief-template.md`](../../tools/tillr-run/backends/brief-template.md) |
| Harness adapters (`muse`, `pi`, `agy`, `claude`) | [`muse/`](../../tools/tillr-run/backends/muse/), [`pi/`](../../tools/tillr-run/backends/pi/), [`agy/`](../../tools/tillr-run/backends/agy/), [`claude/`](../../tools/tillr-run/backends/claude/) |
| Policy schema, seat routing, quota/escalation fields, and `firstBackend` / `NextBackend` | [`tools/tillr-drain/drain/policy.go`](../../tools/tillr-drain/drain/policy.go), [`backend_availability.go`](../../tools/tillr-drain/drain/backend_availability.go) |
| Lane worktree reset, `fence.txt` companion derivation, and `brief.md` rendering | [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) |
| Terminal attempt classification (`AttemptClass`) and boundary diffing (`CompareRunObserved`) | [`tools/tillr-drain/drain/failover.go`](../../tools/tillr-drain/drain/failover.go), [`boundary.go`](../../tools/tillr-drain/drain/boundary.go) |
| Tracked backend policies and offline four-adapter contract suite | [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json), [`tests/tillr-muse-backend.test.py`](../../tests/tillr-muse-backend.test.py) |

---

## Adapter contract and write boundary

### Request, argv, and record seam

`run.Backend` (`tools/tillr-run/run/request.go`) holds the execution parameters of one policy entry (`adapter`, `model`, optional `effort`, `account`, `timeout_seconds`, `harness_args`, `quota`, and `reserve`). Calling `Backend.Command(name)` builds the argv slice placed on `run.Request.Command`:

```text
<adapter> --backend <name> --model <model> [--effort <effort>] [--account <account>] [--timeout <seconds>] [-- <harness_args...>]
```

`validateRequest` enforces the seam before `tillr-run exec` allocates a lane or starts a child process:

- `request.Backend` must be non-empty (`backend is required`).
- Either `request.Backend == "command"` (a workflow command node whose `Command` runs directly without an adapter wrapper) or `filepath.Base(request.Command[0])` must equal `"run-" + request.Backend` (`backend %q does not match adapter %q`).
- `request.EnvRefs` must be empty (`SUB-2` secret resolution remains unbuilt; `exec` refuses requests carrying `env_refs` up front, while model IDs and harness flags travel in argv and tracked policy files per `D-MM5`).
- `extractRoute(request.Command)` copies the `--model` argument into `Record.Route` (nullable on `runs.route` when no `--model` argument is present, as on `"command"` runs).

`run.Exec` (`tools/tillr-run/run/exec.go`) runs the child with `cmd.Dir` set to `<lane-dir>/worktree` when `worktree` exists (falling back to `<lane-dir>` otherwise), sets `TILLR_RUN_ID`, `TILLR_PROGRESS_FILE`, `TILLR_PAUSE_FILE`, and `TILLR_HARNESS_FILE`, creates a dedicated process group (`Setpgid: true`) so wall timeouts and cancellations kill the whole child tree, streams stdout to `<lane-dir>/<run-id>.log` (or `<lane-dir>/nodes/<run-id>/<run-id>.log` when `ReadOnlyGroup != ""`) and stderr to `<lane-dir>/stream.err`, and records `EndCause` (`normal-exit`, `repeating-call`, `wall-timeout`, `sigterm`, `sigint`, or `lease-reaped`) plus any signal name (`Record.Signal`) on the emitted `run.record`. The `runs` materializer (`schema.go`, `apply.go`) persists `backend`, `item_id`, `route`, `harness`, `seat`, `workflow`, `node`, `state`, `exit_code`, `end_cause`, `usage_tokens`, `usage_cost_usd`, and `extension_set`.

### Adapter directory layout and preflight

Each harness directory `tools/tillr-run/backends/<name>/` provides four files:

- `run-<name>` — executable POSIX shell wrapper invoked as `Command[0]`.
- `extract-report` — executable Python helper invoked after the child exits (`spend.go`) as `extract-report --stream <log> --out <tmp/report.md> --exit <code-from-wait> --started <ms> --finished <ms> [--model <m>] [--effort <e>]`.
- `pause-kind` — plain one-line capability file (`run/pause.go`) declaring one of `stops-at-a-gap` (`pi`), `stops-where-it-stands` (`claude`), or `cannot-resume` (`muse`, `agy`).
- `ending-capability` — plain one-line capability file (`run/ending_capability.go`) declaring `footer`, `call-suspends`, or `call-then-message` (all four shipped adapters currently declare `footer`).

Every `run-<name>` sources [`tools/tillr-run/backends/preflight.sh`](../../tools/tillr-run/backends/preflight.sh) before invoking its harness. `preflight.sh` parses `--backend`, `--model`, `--effort`, `--account`, `--brief` (defaulting to `../brief.md` relative to cwd), `--target` (defaulting to `.`), `--timeout`, and `-- <harness_args...>`. Any failure exits `2` (`EXIT_REFUSED`) on stderr with `run-<name>: refused: <reason>` before spending model tokens:

1. `--backend` must equal the adapter's own `<name>`.
2. `--model` must be non-empty (no adapter ever falls back to a default model).
3. `--timeout`, when given, must be a positive integer.
4. `--target` must exist as a directory and `--brief` must exist as a non-empty file.
5. Unless `TILLR_BRIEF_KIND=freeform` (used for workflow stage prompts that are not full ten-section briefs), the brief must contain every required `## ` heading (`Header`, `Task`, `Fence`, `Seats`, `Settled decisions`, `Facts carried`, `Method`, plus `Verification commands`, `Report shape`, and `Stop conditions` under `footer`/`call-then-message`, or `Finishing` in place of the last two under `call-suspends`).
6. No unfilled `{{UPPER_SNAKE}}` placeholder may remain outside fenced code blocks (`preflight_has_unfilled_placeholder` strips fenced blocks before matching so literal `{{...}}` examples inside code blocks are permitted).

### Write boundary and per-run observation

A worker may run arbitrary inspection and targeted test commands inside its target directory, and writes only within its item's path fence (`D-MM1`). Enforcement combines two layers (`D-MM2`, `D-MM9`):

1. **Harness posture:** `run-muse` leaves the `muse` CLI's default sandbox enabled (never passing `--disable-sandbox` or `--yolo`) and roots `--workspace` at the resolved target path (`tests/tillr-muse-backend.test.py` pins this argv invariant). `run-pi`, `run-claude`, and `run-agy` invoke their CLIs in headless non-interactive mode with `--dangerously-skip-permissions` where required (`claude`, `agy`) and rely on the jail plus the drain's boundary snapshot. `UNMEASURED:` the live `muse` sandbox canary (instructing a live Ember run to attempt one write outside the worktree and confirming denial in both the stream and the boundary snapshot) has not been executed because the Meta subscription lapsed before a live `run-muse` item ran; `muse/README.md` retains the canary requirement if `muse` is un-iceboxed.
2. **Per-run boundary snapshots (`tools/tillr-drain/drain/boundary.go`):** Before and after each worker run, `TakeBoundary` snapshots git refs, `git worktree list --porcelain`, main-checkout `HEAD` and porcelain status, `.tillr/` store files outside `state.db*` and `loop/`, and sibling lane directories. `CompareRunObserved` separates benign concurrent activity from convictions:
   - **Excused outright:** movements of ordinary refs outside `refs/tillr/` (commits, tags, remotes, or branches on `main`), changes inside the run's own `<lane-dir>`, and expected sibling lane epochs recorded while concurrent runs stage or finish.
   - **Observed without convicting (`observed`):** unassigned `refs/tillr/*` ref movements, worktrees outside `.tillr/lanes/`, and branch switches or edits in the main checkout while an unattended lane run executes (`TestOrdinaryGitUseDuringARunNeverConvictsIt`, `TestAWorktreeOutsideTheLoopsReservedPlacesIsObserved`).
   - **Convicted (`violations`):** mutations or porcelain changes in a watched sibling lane worktree or disappearance of the run's own lane worktree. A conviction records `StopHostBoundaryViolation` (`"host-boundary-violation"`) as an item fault (`pass.go`), holding that item without aborting sibling lanes.

 Separately, at candidate judgement (`refusal.go`), `tillr-drain` checks the candidate commit's changed paths against `fence.txt` and refuses out-of-fence commits before gating.

---

## Staged lane and brief contract

`LaneStager.Stage` (`tools/tillr-drain/drain/stage.go`) materializes the directory `<lanes-dir>/<lane-id>/` for each dispatched build or workflow run:

| Path | Role |
| :--- | :--- |
| `<lane-dir>/worktree/` | Git worktree reset cleanly to `base_sha` on branch `tillr/lanes-<hash>/<lane-id>` (or reused across sequential nodes/continuations of the same attempt). |
| `<lane-dir>/brief.md` | Untracked instance rendered from [`tools/tillr-run/backends/brief-template.md`](../../tools/tillr-run/backends/brief-template.md) with all `{{PLACEHOLDER}}` tokens filled. |
| `<lane-dir>/fence.txt` | Allowed path prefixes, beginning with the item's `allowed_paths` in declared order followed by derived gate companions (`fencePaths`). |
| `<lane-dir>/base_sha` | Base trunk commit SHA at staging time. |
| `<lane-dir>/session-id` | Harness session handle written or updated (`0600`) when an adapter reports or resumes a session (`exec.go`, `pause.go`). |
| `<lane-dir>/<run-id>.log` | Raw stdout stream captured from the adapter (`<lane-dir>/nodes/<run-id>/<run-id>.log` for parallel read-only group members). |
| `<lane-dir>/stream.err` | Adapter and harness stderr captured during execution. |
| `<lane-dir>/report.md` | Normalized nineteen-key run report published by `readSpend` (`<lane-dir>/nodes/<run-id>/report.md` for read-only group members). |
| `<lane-dir>/durable/<run-id>/` | Per-run scratch directory (`progress`, `pause-request`, `harness`, `resume-prompt`), excluded from the git worktree and fence. |

`fencePaths` (`stage.go`) keeps the item's declared paths first (`TestFencePathsKeepTheItemsOwnPathsFirst`) and appends companion paths required by the repository gate when `tools/tillr*` paths are fenced: `yolo-packs/tillr/files/local/share/tillr/<module>/` mirrors, `yolo-packs/tillr/skills/tillr-operating/SKILL.md`, `tests/tillr-docs.test.py`, and — when `tools/tillr-run/backends` is fenced — `tests/tillr-muse-backend.test.py`. When an item fences a lane's fork checkout, staging rewrites the placeholder `lane-1` segment to the claimed `<lane-id>` (`rewriteForkLanePlaceholder`) and sets up the lane-private fork checkout.

`renderBrief` fills the ten sections of `brief-template.md` (`Header`, `Task`, `Fence`, `Seats`, `Settled decisions`, `Facts carried`, `Method`, `Verification commands`, `Report shape`, and `Stop conditions`, adapting the closing sections when `ending-capability` is `call-suspends`). In the default lane-worktree target mode (`D-MM6`, `D-MM12`), the brief instructs the worker to make exactly one conventional commit on the lane branch without item IDs or attribution trailers and never to run `just done` inside the lane; in the main-checkout mode (`D-MM6`), the brief instructs the worker to leave edits uncommitted under a status-based fence check.

---

## Report extraction and attempt classification

### Unified `report.md` and `.usage.json` shape

After the child exits, `readSpend` (`tools/tillr-run/run/spend.go`) locates `extract-report` beside `Command[0]`, runs it into a temporary directory, copies `report.md` into the lane (or node) directory, parses `tokens` and `cost usd` from the header, and reads `report.md.usage.json` if written (`usage.py`). Extraction failures never alter the child's own exit state; instead `tillr-run` appends a `run.usage` diagnostic event and leaves `usage_tokens` / `usage_cost_usd` null.

[`tools/tillr-run/backends/report.py`](../../tools/tillr-run/backends/report.py) is the single writer of `report.md`. Every report starts with `# run report`, followed by nineteen `key: value` lines (`HEADER_KEYS`), a `---` separator, and the assistant's final text body:

```text
# run report

backend: <pi|claude|muse|agy>
model: <model-id>
route: <provider-or-unknown>
effort: <effort-or-embedded-in-model-or-default>
session: <session-id-or-none>
exit code: <int>
started: <RFC3339-UTC-or-unknown>
finished: <RFC3339-UTC-or-unknown>
stream lines: <int>
events: <int>
unparseable lines: <int>
tokens: <int-or-unknown (breakdown)>
cost usd: <6-decimal-float-or-unknown>
rate limit kind: <window-kind-or-unknown>
fraction: <0-to-1-float-or-unknown>
status: <allowed|rejected|unknown>
reset: <unix-epoch-or-unknown>
complete: <yes|no>
result: <done|failed|blocked|trunk-broken|none>

---

<body>
```

`parse_footer` (`report.py`) inspects only the last non-empty line of the assistant's body (stripping surrounding Markdown `*`, `_`, or `` ` `` emphasis) against `^RESULT:\s*(done|failed|blocked|trunk-broken)\s*$`. A mention of `RESULT:` earlier in the body never satisfies completeness (`complete: yes` requires both `exit_code == 0` and a valid last-line `RESULT:` token). `write_report` maps outcomes to deterministic extractor exit codes:

| Extractor exit | Constant | Condition |
| :--- | :--- | :--- |
| `0` | `EXIT_OK` | `exit_code == 0` and final line is `RESULT: done` |
| `1` | `EXIT_MODEL_FAILED` | `exit_code == 0` and final line is `RESULT: failed` |
| `2` | `EXIT_BAD_USAGE` | CLI usage error or unreadable stream file |
| `3` | `EXIT_BLOCKED` | `exit_code == 0` and final line is `RESULT: blocked` |
| `4` | `EXIT_INCOMPLETE` | Non-zero harness exit, empty stream, unparseable envelope, or missing/malformed `RESULT:` footer |
| `5` | `EXIT_TRUNK_BROKEN` | `exit_code == 0` and final line is `RESULT: trunk-broken` |
| `6` | `EXIT_HARNESS_ENDED` | Clean harness stop without a model error, such as `pi`'s `loop_detected` repeating-call termination (`exec.go` maps exit `6` to `EndRepeatingCall`) |

### `ClassifyAttempt` (`failover.go`)

`ClassifyAttempt` (`tools/tillr-drain/drain/failover.go`) combines the `run.Record` and the lane's `report.md` into one of five `AttemptClass` values (`D-MM11`):

1. **`AttemptInterrupted`** — `EndCause` is `sigterm` or `sigint`. External stop; uncommitted work is preserved without closing the backend or failing over.
2. **`AttemptUnjudged`** — `EndCause` is `wall-timeout` or `lease-reaped`, or `ExitCode == 124` (the adapter's inner `timeout(1)`). A wall cut the run short; no failover occurs.
3. **`AttemptRefused`** — `ExitCode == 2` and `stream.err` contains `<adapter>: refused:`. Preflight rejected the staged inputs before any model ran; the item holds for operator/staging inspection without closing the backend.
4. **`AttemptAnswered`** — `Backend == "command"` with `ExitCode` `0` or `1`, `EndCause == "session-ended"`, or `report.md` carries `complete: yes` with `result` in `{done, failed, blocked, trunk-broken}`. The model answered; a failing test, empty diff, fence escape, or red gate is a statement about the work and **never** fails over to a second backend (`D-MM11`).
5. **`AttemptInfrastructure`** — launch failure (`ExitCode == nil`), non-zero harness exit without a refusal/wall/interruption signature, missing `report.md`, or `complete: no` (truncated stream, provider 5xx/rate-limit/auth failure). Only `AttemptInfrastructure` closes the failed backend and triggers failover.

---

## Backend policy, selection, and failover

[`tools/tillr-drain/drain/policy.go`](../../tools/tillr-drain/drain/policy.go) loads the JSON policy referenced by `policy_ref` (tracked defaults live at [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json), with `.claude.json` and `.mixed.json` variants alongside it):

- `default_backend` — required backend name used when neither an item override nor a seat mapping applies.
- `backends` — map of backend name to `run.Backend` (`adapter`, `model`, optional `effort`, `account`, `timeout_seconds`, `harness_args`, `quota`, `reserve`). Every named backend is validated by `Backend.Missing()` in `Policy.Check()`, and no model vendor string from `config/tillr/backend-policy*.json` may appear anywhere under `tools/tillr*` (`D-MM5`, enforced by `test_policy_and_vendor_hygiene` in `tests/tillr-muse-backend.test.py`).
- `preference` — optional ordered fallback list whose head must equal `default_backend` when non-empty (legacy `max_failovers` keys in JSON are ignored; `TestLegacyFailoverBudgetIsIgnored`).
- `seats` — optional map from seat name (`build`, `design`, `docs`, `edit`, `plan`, `review`, `scout`, `test-run`, `test-write`, `tricky`, `verify`) to one backend string, an ordered array of backend strings, an object `{"backends": [...], "pick": "in-order" | "round-robin" | "least-loaded"}`, or weighted share entries `[{"backend": "...", "share": N}, ...]`.
- `escalate` — optional map from backend name to an ordered list of stronger backend names used on seat escalation (`NextEscalation`).
- `overrides` — optional map from `item_id` to backend name (`D-MM8`). `checkOverrides` (`plan.go`) refuses the entire plan if an override names an item ID absent from the store.

First-dispatch selection (`firstBackend` in `policy.go`) resolves in strict precedence:

1. `overrides[item_id]` (`D-MM8`)
2. `seats[seat]` (when the run carries a mapped seat)
3. `default_backend`

When a run ends in `AttemptInfrastructure`, `tillr-drain` marks that backend closed until recovery/quota evidence reopens it (`backend_availability.go`) and `NextBackend` (`policy.go`) advances along the seat's backend list (when the seat maps a list and no item override forced the backend) or along `preference`, skipping closed backends and backends already tried on the current attempt. When no open untried backend remains, the item holds (`backend-window` when waiting on a reset timestamp, or infrastructure hold) rather than spinning on a dead route.

---

## Steering, continuation, and session modes

How `tillr-drain` and `tillr-run` continue or steer work depends on why the prior run stopped and what `pause-kind` its adapter declares (`run/pause.go`, [`tillr-build-commit.md`](./tillr-build-commit.md)):

- **Dirty-worktree build continuation (same `run_id`):** When a build worker exits cleanly with `RESULT: done` while leaving in-fence uncommitted files in `<lane-dir>/worktree`, `Loop.settle` continues the same run (`run.resume` event) under a fresh lane lease (`D-UT17`, [`tillr-build-commit.md`](./tillr-build-commit.md)). `prepareContinuationSession` (`run/pause.go`) writes `<lane-dir>/durable/<run-id>/resume-prompt` (`0600`) and exports `TILLR_RESUME_PROMPT_FILE` plus `TILLR_SESSION_MODE`:
  - Resumable adapters (`pi` with `stops-at-a-gap`, `claude` with `stops-where-it-stands`) receive `TILLR_SESSION_MODE=resume`, read `<lane-dir>/session-id`, and resume the existing harness conversation with the continuation prompt.
  - Non-resumable adapters (`muse` and `agy`, both `cannot-resume`) receive `TILLR_SESSION_MODE=new` (clearing any stale `session-id`) and prepend the continuation prompt ahead of `brief.md` in a fresh harness invocation against the preserved dirty worktree.
- **Quota pause and mid-run handoff:** When a subscription quota window closes mid-run, `tillr-run` writes `<lane-dir>/durable/<run-id>/pause-request` (`TILLR_PAUSE_FILE`) for `stops-at-a-gap` adapters (`pi`) or sends `SIGTERM` for `stops-where-it-stands` adapters (`claude`), recording a `quota_pauses` row so the run resumes once the window reopens or fails over cleanly.
- **Evaluated retry / conflict-fix (new `run_id`, preserved lane context):** When a gate check, reviewer, or merge conflict requires another pass on an inspected candidate, `tillr-drain` stages a new run on the lane worktree carrying the failure or conflict evidence in `Facts carried` rather than mutating a finished session transcript (`D-UT17`).

---

## Shipped adapters

All four adapters share `preflight.sh`, `report.py`, and `usage.py`, and are exercised offline over recorded success and truncated/malformed fixtures by [`tests/tillr-muse-backend.test.py`](../../tests/tillr-muse-backend.test.py):

| Backend | Pause kind | CLI invocation and prompt delivery | Stream dialect and usage capture |
| :--- | :--- | :--- | :--- |
| `pi` | `stops-at-a-gap` | Resolves rebuilt fork binary or `pi` on `PATH`; pipes prompt on stdin to `pi --print --mode json --model <model> --thinking <effort>` with `--session-id` (`new`) or `--session` (`resume`/`fork`), exporting `BOARD_DRAIN_*` session-control variables (`run-pi`). Writes resolved binary identity to `TILLR_HARNESS_FILE`. | JSONL (`session`, `message_end`, `turn_end`). Extracts assistant text blocks, token/cost totals across turns, `.usage.json` per-call rows, and `loop_detected` stops (exit `6`). |
| `claude` | `stops-where-it-stands` | Sets `YOLO_NO_LAUNCH_FLAGS=1`; pipes prompt on stdin to `claude -p --output-format stream-json --verbose --model <model> --effort <effort> --strict-mcp-config --dangerously-skip-permissions` plus `--session-id` / `--resume`, optional subscription `--settings`, and `<harness_args...>` (`run-claude`). | JSONL (`system`/`init`, `assistant`, `rate_limit_event`, `result`). Deduplicates streaming assistant snapshots by message ID, extracts `total_cost_usd` and rate-limit utilization/reset headers. |
| `muse` *(dormant)* | `cannot-resume` | Runs `muse model-profile show <model> --effort <effort>` to fail fast on unknown models/efforts, then `muse exec --json --prompt-file <prompt> --workspace <target> [--session-id <id>] [--provider <p>] [--permission-profile <prof>] [--model <m> --reasoning-effort <e>]` (`run-muse`, `MUSE_PROVIDER=echo` supported for zero-token rehearsals). | JSONL (`session.started`, `turn.started`, `run.output.delta`, `assistant.turn_completed`, `session.ended`, `error`). Reconstructs text from output deltas or final message; sums `input_tokens + output_tokens`; cost USD is `unknown` unless supplied. |
| `agy` *(dormant)* | `cannot-resume` | Rejects `--effort` (effort is encoded in the `agy` model ID); sets `YOLO_NO_LAUNCH_FLAGS=1`; pipes prompt on stdin to `agy --print --output-format json --model <model> [--print-timeout <N>s] --dangerously-skip-permissions` (`run-agy`). | Single JSON object envelope (`session_id`, `response`, `stats.models`). Sums `input + candidates + thoughts` tokens across model buckets; cost USD is `unknown`. |

When `--timeout <N>` is passed to any of `run-pi`, `run-claude`, `run-muse`, or `run-agy`, the adapter wraps its harness exec in `timeout "$wall" ...`; when `--timeout` is omitted (`timeout_seconds: 0`, per [`run-wall-ruling.md`](../design/run-wall-ruling.md)), no inner `timeout(1)` wrapper is used.

---

## Current values

Verified at `f7a5e29047375e1a233bf1f65be4f0d6b3013c14`.

| Setting / identifier | Current value | Defined in |
| :--- | :--- | :--- |
| Active default backend and preference | `default_backend: "pi"`, `preference: ["pi"]` (`muse` and `agy` declared, excluded from dispatch) | [`config/tillr/backend-policy.json`](../../config/tillr/backend-policy.json) |
| Preflight refusal exit code | `2` (`run-<name>: refused: ...` on stderr) | `EXIT_REFUSED` in [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh); `AttemptRefused` in [`failover.go`](../../tools/tillr-drain/drain/failover.go) |
| Required brief `## ` headings (`footer` / `call-then-message`) | `Header`, `Task`, `Fence`, `Seats`, `Settled decisions`, `Facts carried`, `Method`, `Verification commands`, `Report shape`, `Stop conditions` (`Finishing` replaces the last two for `call-suspends`) | `required_headings` in [`preflight.sh`](../../tools/tillr-run/backends/preflight.sh); [`brief-template.md`](../../tools/tillr-run/backends/brief-template.md) |
| `report.md` header keys (`19`, in order) | `backend`, `model`, `route`, `effort`, `session`, `exit code`, `started`, `finished`, `stream lines`, `events`, `unparseable lines`, `tokens`, `cost usd`, `rate limit kind`, `fraction`, `status`, `reset`, `complete`, `result` | `HEADER_KEYS` in [`report.py`](../../tools/tillr-run/backends/report.py) |
| Extractor exit codes | `0` (`done`), `1` (`failed`), `2` (bad usage), `3` (`blocked`), `4` (incomplete), `5` (`trunk-broken`), `6` (harness-ended / repeating-call) | `EXIT_*` in [`report.py`](../../tools/tillr-run/backends/report.py) |
| Adapter `pause-kind` values | `stops-at-a-gap` (`pi`), `stops-where-it-stands` (`claude`), `cannot-resume` (`muse`, `agy`) | `PauseKind` constants in [`run/pause.go`](../../tools/tillr-run/run/pause.go) |
| Adapter `ending-capability` values | `footer` (default across `pi`, `claude`, `muse`, `agy`), `call-suspends`, `call-then-message` | `EndingCapability` constants in [`run/ending_capability.go`](../../tools/tillr-run/run/ending_capability.go) |
| Boundary conviction hold / fault reason | `"host-boundary-violation"` (`StopHostBoundaryViolation`) | [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go), [`boundary.go`](../../tools/tillr-drain/drain/boundary.go) |

---

<a id="decision-ledger"></a>

## Why it's this way

Each row preserves an owner ruling ([`OQ-MM1`](#why-its-this-way)–[`OQ-MM6`](#why-its-this-way), `D-MM10`) or design decision (`D-MM1`–`D-MM3`, `D-MM11`, `D-MM12`) from the graduated Muse backend design, including the three rulings confirmed by the owner on 2026-11-20 in [`decision-provenance-review.md`](../tillr/decision-provenance-review.md) (`D-MM1`, `D-MM2`, `D-MM11`, `D-MM12`).

| ID | Decision | Why it stays |
| :--- | :--- | :--- |
| D-MM1 | Define the adapter contract as a write boundary rather than an allowlist of commands: any inspection or targeted verification command may run inside the target, while writes outside the boundary and coordinator/store mutations are forbidden *(2026-11-11)* | Agents need unrestricted read and test tools to diagnose code; confining where writes land protects repository and queue integrity without hamstringing investigation. |
| D-MM2 | Enforce the write boundary mechanically in layers — harness sandbox posture where available plus before/after boundary snapshots in the drain — rather than relying on prompt prose alone *(2026-11-11)* | Prompt instructions can be ignored by a confused model; mechanical snapshots and post-run fence checks detect escapes regardless of harness cooperation. |
| D-MM3 | Keep the `tillr-run` boundary as subprocess argv (`Command[0]` = `run-<backend>`) and records (`run.request` / `run.record`) with no wire protocol or warm daemon at the runner seam *(2026-11-11)* | Matches `board-utilities.md`'s record-and-process architecture so `command` nodes and four distinct agent CLIs share one lease, timeout, and logging implementation. |
| D-MM4 ([`OQ-MM1`](#why-its-this-way)) | Require a non-empty `backend` field on `run.request`, `run.record`, and `runs` matching `Command[0]`, and ship `pi` and `agy` proof adapters alongside `muse` in the initial slice *(owner ruling, 2026-11-12)* | Proving stdin vs prompt-file delivery, separate vs model-embedded effort, and JSONL vs single-envelope extraction up front prevented `muse`-specific assumptions from hardening into the runner. |
| D-MM5 ([`OQ-MM2`](#why-its-this-way)) | Keep all model vendor names out of `tools/tillr*` (which ships publicly) and supply model IDs exclusively through repository-tracked `config/tillr/backend-policy*.json`, refusing empty `--model` values in preflight *(owner ruling, 2026-11-12; pinned by `tests/tillr-muse-backend.test.py`)* | Preserves a vendor-neutral public `tillr` tree while keeping active model pins versioned in repository with zero silent fallback to a weaker default model. |
| D-MM6 ([`OQ-MM4`](#why-its-this-way)) | Default runs to isolated lane worktrees while permitting an explicit main-checkout target mode under a status-diff fence check *(owner ruling, 2026-11-12)* | Worktrees isolate concurrent writers cleanly, while retaining main-checkout support keeps standalone `tillr-run` usable when a worktree is intentionally omitted. |
| D-MM7 ([`OQ-MM6`](#why-its-this-way)) | Tie any warm `muse` server process to a four-run cold-prefix measurement at Stage 3 rather than building a daemon speculatively *(owner ruling, 2026-11-12; `MM-2`/`MM-3` are not built while `muse` is iceboxed)* | Prevents maintaining a long-lived stateful service for an unmeasured startup cost on a dormant backend. |
| D-MM8 ([`OQ-MM3`](#why-its-this-way)) | Keep backend selection in the policy file (`default_backend`, `seats`, `preference`, and `overrides` validated against known store items) rather than adding a backend column on items *(owner ruling, 2026-11-12)* | Routing is an operational dispatch choice; keeping it in policy lets the operator switch providers or icebox a backend across the whole queue in one config edit. |
| D-MM9 ([`OQ-MM5`](#why-its-this-way)) | Run unattended drains without requiring a Linux namespace wrapper first, pairing git SHA pinning and atomic CAS landing with per-run `CompareRunObserved` boundary checks *(owner ruling, 2026-11-12; narrowed 2026-11-17 so ordinary git refs and external worktrees are observed/excused while watched-lane escapes convict as `StopHostBoundaryViolation`)* | Avoids nesting container/namespace sandboxes inside `yolo` jails while still catching cross-lane filesystem corruption and letting normal operator git activity proceed alongside the drain. |
| D-MM10 | Name the Meta CLI backend `muse` after its harness binary (withdrawing the earlier transcription artifact `"metamodel"`), and split `MM-1` into `MM-1S` (substrate) and `MM-1A` (adapters) *(owner ruling, 2026-11-12)* | Aligns backend naming with `pi`, `agy`, and `claude`, where every backend name names the CLI harness its adapter drives. |
| D-MM11 | Fail over across backends only on `AttemptInfrastructure` (closing the failed backend and walking the seat list or `preference`), never on `AttemptAnswered`, `AttemptRefused`, `AttemptUnjudged`, or `AttemptInterrupted` *(2026-11-12; fixed `max_failovers: 1` cap retired in favor of availability-tracked backend closure)* | Re-running judged work (a failing test, empty diff, or red gate) on another model wastes spend to rediscover a real work defect, whereas harness/provider outages learn nothing about the item and should advance across open routes. |
| D-MM12 | Require lane-worktree build runs to commit their deliverable on the lane branch (`HEAD != base_sha` is the candidate) while main-checkout runs never commit *(2026-11-12; dirty-exit recovery later extended by [`tillr-build-commit.md`](./tillr-build-commit.md))* | Gives `tillr-gate` and `tillr-land` an immutable tree and commit message on the lane branch while ensuring no worker ever advances `main` directly. |
