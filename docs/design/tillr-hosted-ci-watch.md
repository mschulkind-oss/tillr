---
title: "tillr watches the hosted CI run of each commit it publishes and acts on the result inside tillr"
status: in-review
stage: DESIGN
tags: [tillr, drain, watch, ci, triage, design]
summary: "Design for publication-driven, host-neutral CI watching in tillr-drain, per-commit drain.ci-run store events and scrubbed failure logs, deduplicated failure alerts routed to the triage seat without reverting or holding proved commits, multi-target trunk and fork observation, and five build slices. Nothing is built; three questions are open for the owner."
next: "Rule OQ-CI1, OQ-CI2 and OQ-CI3. Slices 1 to 3 of Section 8 do not wait on those rulings; Slice 4 follows OQ-CI1, OQ-CI2 and OQ-CI3."
depends-on:
  - landing-principles.md
  - tillr-watch.md
  - tillr-watch-alert-workflow.md
  - tillr-alert-triage-seat.md
  - general-agent-watch.md
  - operator-decisions-inside-tillr.md
  - ../reference/gate.md
  - ../reference/tillr-restart.md
  - ../reference/tillr-refused-push.md
vantage:
  status-chip: true
---

# tillr watches the hosted CI run of each commit it publishes and acts on the result inside tillr

**Status:** Not built, verified on 2026-11-26 (`rg -n 'ci\.target|ci-run-failed|drain\.ci-run|ci-logs' tools config` returned nothing). Every mechanism, event contract, routing choice, publishing boundary, deduplication rule, multi-repository rule and slice boundary in this document is the designer's decision answering a 2026-11-26 operator request (`item-request-75251137d9`), not an owner ruling, and the owner may overturn any of them; only the owner's quoted 2026-11-26 instruction in [§1](#1-the-request-the-2026-11-26-failures-and-the-standing-rulings) and the cited rulings in [`R-CI1`](#R-CI1) through [`R-CI7`](#R-CI7) are settled by the owner.

**Needs your ruling:** [OQ-CI1](#OQ-CI1), [OQ-CI2](#OQ-CI2), [OQ-CI3](#OQ-CI3).

**TL;DR:** When `tillr-drain` publishes a commit on a configured target (the repository's trunk or an owned fork), a publication-driven observer inside `tillr-drain loop` invokes a configured shell-less command — holding no credential inside `tillr` and doing zero polling when no published commit has an unsettled run — to record one `drain.ci-run` event per published commit's workflow run, with conclusion, failing job and step, normalized failure signature, and a secret-scrubbed failure log on disk. A hosted failure after a green local proof means the local and hosted environments disagree, never that the commit should be reverted: `tillr` neither reverts proved commits nor holds `refs/tillr/green` or future pushes ([OQ-CI1](#OQ-CI1)), and routes one deduplicated `ci-run-failed` episode per distinct failure signature to the built `triage` seat ([OQ-CI2](#OQ-CI2), [OQ-CI3](#OQ-CI3)) so cost scales with distinct faults rather than trunk commit rate.

---

## 1. The request, the 2026-11-26 failures, and the standing rulings

On 2026-11-26 the owner asked ([R-CI1](#R-CI1)):

> `"design a feature that ties into a CI watcher directly from tillr instead of the foreground agent"`

That day, commits that had passed the local fast gate and after-merge heavy proof were pushed automatically to `origin/main` (`push.what = "green"` in [`.tillr.toml`](../../.tillr.toml)) and failed on GitHub Actions inside [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) (`just check-ci`). Because nothing inside `tillr` read the hosted workflow results, the foreground operating session polled `gh` by hand, read the failed logs, and filed four repair items — among them adding the missing `go-vet` gate check so local proof and `just check-ci` enforce the same check (`3671fc24`), removing host assumptions such as `uvx` and ambient `PATH` binaries from tests run on a clean runner (`0d3415cf`), and making `sh` fingerprinting portable across hosts (`fbad218e`).

This design touches settled rulings across the landing, gate, restart, watch, triage and operator-decision ledgers:

1. **Cost must not rise with trunk activity** ([Landing Principles §2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [§3](./landing-principles.md#3-gate-what-will-be-the-tip-once)): any mechanism that spends an investigation, an item, a lane or a merge stall per published commit turns high trunk velocity into waste.
2. **Work that does not require owner authority settles inside `tillr`** ([Landing Principles §9](./landing-principles.md#9-fix-the-machinery-never-hand-carry-the-work) and [§16](./landing-principles.md#16-what-the-operator-can-settle-is-settled-inside-tillr), [`R-AT1`](./tillr-alert-triage-seat.md#R-AT1), [the operator-decisions design §1](./operator-decisions-inside-tillr.md#1-the-ruling)), while seat/model policy and non-dependency queue ordering remain the owner's ([the operator-decisions design §6](./operator-decisions-inside-tillr.md#6-what-is-the-owners)).
3. **Probe failure and check failure are distinct classes, and retries require a named change** ([Landing Principles §8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which), [§10](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall), [§11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), and [§13](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)): an unreachable CI host or missing credential must never look like either a passing run or a broken build, and repeating the same failure on a commit that predates the fix carries no new diagnosis.
4. **Local after-merge proof governs `refs/tillr/green`, rolls, and automatic pushes** ([`D-GR5`](../reference/gate.md#D-GR5), [`D-GR6`](../reference/gate.md#D-GR6), [`OQ-GR1`](../reference/gate.md#OQ-GR1), [`D-RT10`](../reference/tillr-restart.md#why-its-this-way)), all repository automation defaults off in `.tillr.toml` with unknown keys refused ([`D-RT7`](../reference/tillr-restart.md#why-its-this-way)), and remotes are not polled when no publication work is owed ([`tillr-refused-push.md` §8](../reference/tillr-refused-push.md#8-invariants-non-goals-and-risks)).
5. **`tillr-watch` is declarative and side-effect-free, and every alert settles through an accountable outcome** ([`D-TW2`](./tillr-watch.md#D-TW2), [`D-TW4`](./tillr-watch.md#D-TW4), [`D-TW50`](./tillr-watch.md#D-TW50), [`OQ-TW2`](./tillr-watch.md#OQ-TW2), [`R-AW1`](./tillr-watch-alert-workflow.md#R-AW1), [`R-AW4`](./tillr-watch-alert-workflow.md#R-AW4), [`R-AW5`](./tillr-watch-alert-workflow.md#R-AW5), [`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2), [`D-AW5`](./tillr-watch-alert-workflow.md#D-AW5)): watch rules execute no remedies and never cap, bench or hold work; whether `tillr-watch` itself may ever run an interval `command` probe remains an unruled open question ([`OQ-GW2`](./general-agent-watch.md#OQ-GW2)), and the watch engine imports nothing from `tillr` ([`general-agent-watch.md` Decision Ledger](./general-agent-watch.md#decision-ledger)). Instead, `tillr-drain` dispatches unsettled `triage`-routed alerts to the read-only `SeatTriage` (`scout` tier, landed through `25ceb253`; [`OQ-AT1`](./tillr-alert-triage-seat.md#OQ-AT1), [`D-AT3`](./tillr-alert-triage-seat.md#D-AT3), [`D-AT6`](./tillr-alert-triage-seat.md#D-AT6)).

---

## 2. Which program watches, how it learns a run's result, and what it records

### 2.1 Why `tillr-drain` watches rather than `tillr-watch` or a third daemon

Three programs could own hosted CI observation. As the designer's decision ([D-CI1](#D-CI1); owner may overturn), **`tillr-drain` owns CI observation** — running as an asynchronous, publication-driven background observer inside `tillr-drain loop` alongside `post_landing.go`, paired with a one-shot `tillr drain ci-status` subcommand for manual and test execution — while `tillr-watch` evaluates declarative rules over the resulting store events:

| Candidate | Why selected or rejected |
|---|---|
| **`tillr-drain loop` + one-shot `tillr drain ci-status` (selected, [D-CI1](#D-CI1))** | `tillr-drain` already loads `.tillr.toml`, executes `pushGreen` / `pushTrunk` and fork rolls, writes `.tillr/state.db` and `<root>/.tillr/loop/`, and manages in-place restarts. Triggering observation from publication refs means **zero commands run when every published commit has settled**. Keeping external I/O and log capture in `tillr-drain` leaves `tillr-watch` purely declarative and avoids blocking on [`OQ-GW2`](./general-agent-watch.md#OQ-GW2). |
| **`tillr-watch` (`tillr watch daemon`)** | Rejected. Watch rules are total, side-effect-free predicates ([`D-TW4`](./tillr-watch.md#D-TW4), [`OQ-TW2`](./tillr-watch.md#OQ-TW2)), `command` sources in `tillr-watch` are unruled ([`OQ-GW2`](./general-agent-watch.md#OQ-GW2)), and a timer source in the domain-free watch engine cannot wake on `push-ok`, track an unsettled commit set across rebases, retain per-run failure logs under `<root>/.tillr/loop/`, or avoid polling at idle. |
| **A separate long-lived daemon (`tillr-ci`)** | Rejected. A third long-lived daemon would require its own `restart` role, binary-roll plumbing, settings reload, and liveness watch rule (`drain-loop-no-pass` equivalent) to do work that only arises after `tillr-drain` publishes a commit. |

Unlike `push` or `roll`, an in-flight CI status poll is read-only and idempotent: it never blocks `tillr-drain restart` (`gateRestartWith`), cancels its child command on shutdown, and resumes on the new binary from `.tillr/state.db` and the target's `published_ref`.

### 2.2 How `tillr-drain` learns a run's result: the host-neutral command contract

As the designer's decision ([D-CI2](#D-CI2); owner may overturn), `tillr` embeds no GitHub client, no `gh` dependency, no host URL parser, and no credential store. `.tillr.toml` gains an optional `[ci]` section with zero or more `[[ci.target]]` blocks:

```toml
[[ci.target]]
name = "trunk"
published_ref = "refs/tillr/published/origin/main"
command = ["bin/ci-watch-github", "--repo", "mschulkind-oss/tillr", "--mode", "status"]
log_command = ["bin/ci-watch-github", "--repo", "mschulkind-oss/tillr", "--mode", "log"]
poll_interval_seconds = 30
registration_grace_seconds = 120
run_timeout_seconds = 3600
```

`command` and optional `log_command` are non-empty argument vectors executed directly with `exec.CommandContext` (no `sh -c` interpolation), `GIT_TERMINAL_PROMPT=0`, bounded stdout/stderr, and a per-invocation timeout (`probe_timeout_seconds`, default `30`). `tillr-drain` passes context via environment variables:

- `TILLR_CI_TARGET` (`name`), `TILLR_CI_PUBLISHED_REF` (`published_ref`), and `TILLR_CI_COMMIT` (full 40-hex SHA being checked);
- on `log_command` (run at most once per failed `(target, run_id)` when `command` did not already supply the log): `TILLR_CI_RUN_ID`, `TILLR_CI_WORKFLOW`, `TILLR_CI_JOB`, and `TILLR_CI_STEP`.

Following [Landing Principle §8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which), probe execution failure and a red CI run are strictly separated:

1. **Exit `0`:** stdout must be a single JSON object `{ "runs": [ ... ] }` describing the workflow runs registered on the host for `TILLR_CI_COMMIT`:
   - If `runs` is empty (`[]`) and the commit was first observed fewer than `registration_grace_seconds` ago, the commit stays in flight (`pending` registration). Once `registration_grace_seconds` elapses with `runs: []`, `tillr-drain` records one terminal `drain.ci-run` event with `conclusion = "no-run"` and stops polling that commit.
   - If any run in `runs` has `status` in `["queued", "in_progress", "waiting", "pending"]` (and `run_timeout_seconds` has not elapsed), that run stays in flight; any sibling workflow run in `runs` that has already reached `status == "completed"` records its own `drain.ci-run` event once (keyed by `(target, commit_sha, run_id)`).
   - Each completed run in `runs` carries `run_id` (non-empty string), `workflow` (string), `conclusion` from the closed set `["success", "failure", "cancelled", "skipped", "timed_out", "action_required", "neutral"]`, optional `url`, `started_at`, `completed_at`, `failing_job`, `failing_step`, optional `failing_jobs` (`[{job, step, conclusion, url}]`), and optional `failure_summary`. An unknown `status` or `conclusion` word is treated as a malformed probe output (`drain.ci-probe-error`), never coerced to `"success"`.
2. **Non-zero exit, timeout, or invalid JSON:** the probe could not answer (network down, broker unreachable, expired external token, HTTP 5xx, rate limit). `tillr-drain` leaves the commit unsettled, backs off (`poll_interval_seconds` doubling up to `300s`), and emits `drain.ci-probe-error` only on state/error-class transitions so offline or credential faults never flood the event log or masquerade as code failures.

### 2.3 Store records and retained failure logs

As the designer's decision ([D-CI3](#D-CI3); owner may overturn), `tillr-drain` writes all CI state to the existing `events` table in `.tillr/state.db` (no new SQLite table required in Slice 1) and retains scrubbed failure logs under `<root>/.tillr/loop/ci-logs/`:

- **`drain.ci-run` (one event per published commit's workflow run upon reaching a terminal state, `subject = target`):**
  - Identity and timing: `target`, `published_ref`, `delivery_ref` (when configured), `commit_sha`, `run_id`, `workflow`, `url`, `started_at`, `completed_at`, `observed_at`.
  - Outcome and failure location: `conclusion`, `failing_job`, `failing_step`, `failing_jobs`, `failure_summary`.
  - Log location: `log_path` — absolute path `<root>/.tillr/loop/ci-logs/<target>/<commit_sha>-<run_id>.log` populated whenever `conclusion` is in `["failure", "timed_out", "action_required"]` (empty on `"success"`, `"skipped"`, `"neutral"`, `"cancelled"`, `"no-run"`). Before writing `log_path` or event fields, `tillr-drain` scrubs authorization headers, bearer/GitHub/OAuth token prefixes, and URL userinfo credentials, and caps each retained log file (`max_log_bytes`, default `2 MiB`, keeping head and tail around failing markers).
  - Deduplication and ancestry fields (computed mechanically at write time; [§5](#5-repeated-failures-and-runs-still-in-progress-when-the-next-commit-is-published)): `failure_signature`, `failure_keys`, `repeat_of_unfixed`, `superseded_by_commit`, `superseded_by_green`, `supersedes_failure`, and `recurred_after_item`.
- **`drain.ci-probe-error` / `drain.ci-probe-ok` (`subject = target`):** emitted when a target transitions from healthy probing into probe failure (recording `commit_sha`, `exit_code`, `reason`, and scrubbed `stderr_excerpt`) and once (`drain.ci-probe-ok`) when probing recovers after an error.

Because `<root>/.tillr/loop/` is mounted read-only inside `SeatTriage`'s namespace (`triage_guard_linux.go` binds `<root>` read-only and hides only `.tillr/lanes`), `triage` reads `log_path` directly without widening its sandbox.

---

## 3. What a hosted failure means and what it does

### 3.1 Environment disagreement when local proof passed, never a revert

Under `push.what = "green"` ([`D-RT10`](../reference/tillr-restart.md#why-its-this-way)), every trunk commit pushed to the remote has already passed both the pre-merge gate and the local after-merge heavy proof (`refs/tillr/green`, [`D-GR5`](../reference/gate.md#D-GR5)). When hosted CI fails on a commit that passed local proof, **the local machine and the hosted runner disagree about the same tree**. On 2026-11-26 every failure fell into one of three disagreement classes:

1. **A check ran in hosted CI that local gate/proof omitted** (e.g. `3671fc24`: `just check-ci` ran `go vet`, which had no declaration under `tools/gate/checks/`). The repair fixes the offending code *and* closes the local gate gap where practical.
2. **A test or recipe depended on ambient development-host state absent on a clean runner** (e.g. `0d3415cf` and `fbad218e`: `uvx`, pre-built binaries on `PATH`, shell dialect, or scratch env vars). The repair makes the test or check hermetic.
3. **A hosted-only runner, workflow, or credential fault** (runner image regression on `ubuntu-latest` / `go-version: stable`, action download failure, billing/quota hold). No commit revert in the repository can cure an external runner fault.

Reverting the pushed commit $C_k$ is wrong on mechanism as well as meaning:

- **Batched green pushes obscure single-commit attribution:** one advance of `refs/tillr/green` routinely publishes a batch of $k$ landings at once ([`gate.md`](../reference/gate.md#measured-proof-vs-gate-contention-2026-11-25) measured batches of 4, 15, 31 and 47 landings); hosted CI runs on the published tip $C_k$, and runner/toolchain drift fails on $C_k$ even when no recent commit caused it.
- **No forced pushes:** `C_k` is already published on the remote ([`tillr-refused-push.md` §8](../reference/tillr-refused-push.md#8-invariants-non-goals-and-risks)), and local `auto_revert` ([`D-GR6`](../reference/gate.md#D-GR6)) relies on bisecting with a failing *local* check — which already passes here.
- **Holding pushes on red hosted CI deadlocks remote verification:** if `tillr-drain` held automatic pushes while hosted CI was red, the landed repair commit itself could not be pushed automatically to prove and clear the red hosted workflow; holding pushes while a hosted run is `in_progress` would serialize publication behind 5–15 minute remote runner queues.

As the designer's decision ([D-CI4](#D-CI4), [D-CI5](#D-CI5); owner may overturn), a hosted CI failure frames its finding around the **environment discrepancy** rather than a revert, and **the CI watcher never reverts a commit, never holds lanes or landings, and never holds `refs/tillr/green`, binary rolls, or automatic pushes**. Because requiring hosted CI to pass before advancing `"green"` or allowing hosted CI to hold publication would change what `"green"` means across [`D-GR5`](../reference/gate.md#D-GR5) and [`D-RT10`](../reference/tillr-restart.md#why-its-this-way), that boundary is recorded for the owner as [OQ-CI1](#OQ-CI1).

### 3.2 Failure action: deduplicated watch alert to `triage` versus a trunk fixer

Three candidate failure actions were weighed against the landing principles and the proof's authority:

1. **Candidate A — A `tillr-watch` episode routed to `receiver = "triage"` (selected as the designer's decision, [D-CI6](#D-CI6); owner may overturn via [OQ-CI2](#OQ-CI2)):**
   - A shipped rule `ci-run-failed` (`tools/tillr-watch/rules/ci-run-failed.toml`) opens one episode per `(target, workflow, failure_signature)` and routes it to `SeatTriage` ([`OQ-AT1`](./tillr-alert-triage-seat.md#OQ-AT1), [`D-AT3`](./tillr-alert-triage-seat.md#D-AT3)).
   - `SeatTriage` (`scout` tier, read-only) inspects the `drain.ci-run` payload, `log_path`, `.github/workflows/` (or equivalent), `tools/gate/checks/`, and open/recent items:
     - For a code, test, workflow, or gate-parity defect, `triage` files (or links) **one** deduplicated `fix-fault` request via `tillr item request --filed-by tillr-triage --alert <id> --dedup-key ci:<target>:<workflow>:<failure_signature>` and settles the episode ([`D-AT6`](./tillr-alert-triage-seat.md#D-AT6), [`D-AT7`](./tillr-alert-triage-seat.md#D-AT7)) without waking the foreground agent.
     - For an external runner outage, billing/quota block, credential failure, or ambiguous cause, `triage` escalates (`escalate_to = "operator-session"` or `"owner"`) with its diagnosis rather than wasting a build lane trying to patch GitHub's infrastructure in repository code ([`D-AT8`](./tillr-alert-triage-seat.md#D-AT8), [`D-AT11`](./tillr-alert-triage-seat.md#D-AT11)).
2. **Candidate B — A `fixer-main-<sha>` trunk fixer as for a red local proof:**
   - Rejected. In `tillr-drain`, `fixer-main-<sha>` (`trunk_repair.go`, [`gate.md` §The trunk fixer's gate](../reference/gate.md#the-trunk-fixers-gate)) is fenced *out* of `tools/gate/` ("the gate's own code stays outside a fixer's fence"), holds candidate lanes whose diffs intersect the failing local check's `reads`, and proves itself locally with `fixer-gate --only <check-id>`. On a hosted discrepancy when local proof is already green, no local check failed (`--only` has nothing red to prove locally, or passes trivially when `uvx` is installed locally), `tools/gate/checks/` is often the exact directory the fix must touch (`3671fc24`), and holding lanes on a green local proof violates [`OQ-GR1`](../reference/gate.md#OQ-GR1) and [`D-TW2`](./tillr-watch.md#D-TW2).
3. **Candidate C — Both a watch alert and direct mechanical item filing by `tillr-drain`:**
   - Having both `tillr-drain` mechanically file a `request` item and `SeatTriage` investigate the alert creates two actors racing to file items, and mechanical filing dispatches a full `build` lane even when hosted CI failed on an external runner outage or network blip. However, if the owner prefers zero `triage` LLM turns on clear first-seen CI failures (with `tillr-drain` filing the deduplicated `request` item directly and pre-linking a settled watch episode), [OQ-CI2](#OQ-CI2) offers that exact alternative cleanly.

**Why this satisfies the trunk-activity principle:** under Candidate A (and the deduplication rule of [§5.1](#51-one-alert-and-one-item-per-distinct-failure-not-per-commit)), `tillr-drain` writes one cheap SQLite row per finished workflow run, while watch episodes, `triage` runs, and filed repair items are created **once per distinct `failure_signature`**, never once per published commit, and hold zero build lanes.

**Evidence staged into the repair worker's lane ([D-CI7](#D-CI7), designer's decision; owner may overturn):** lane workers are forbidden from reading `.tillr/`. Paralleling `fixer.logs` (`fixer_logs_test.go`), when `LaneStager` stages a worker for an item linked via `alert_item_links` (or item metadata) to a `drain.ci-run` event, it copies `log_path` into `<durable_dir>/ci.logs/<commit_sha>-<run_id>.log` and renders the target, workflow, failing job/step, `failure_summary`, run-local log path, and environment-discrepancy check instructions into the worker's brief ([`T3` in `operator-decisions-inside-tillr.md` §4](./operator-decisions-inside-tillr.md#4-the-rules)). Whether filed CI repair requests enter at `--express` or ordinary priority is an ordering policy choice reserved for the owner in [OQ-CI3](#OQ-CI3) (designer leaning: express).

---

## 4. Staying optional, host-neutral, offline-safe, and credential-free

Four deployment cases must work without special builds or code changes in `tillr`:

1. **A repository with no hosted workflow:** `.tillr.toml` omits `[ci]` (all automation off by default under [`D-RT7`](../reference/tillr-restart.md#why-its-this-way)). `tillr-drain` starts no CI watcher, runs no child command, and emits no CI events. If `[ci]` is configured on a target where some commits trigger no workflow (for example path-filtered documentation commits), `runs: []` settles cleanly as `conclusion = "no-run"` after `registration_grace_seconds` without alerting (unless the target explicitly sets `alert_on_no_run = true`).
2. **A different hosting service:** `tillr` knows only the argv + JSON stdout contract of [§2.2](#22-how-tillr-drain-learns-a-runs-result-the-host-neutral-command-contract). GitHub Actions (`gh`), GitLab CI (`glab`), Buildkite, Forgejo/Gitea, Woodpecker, or an internal CI proxy each plug in through a small repository-owned or host-provided adapter script.
3. **No network:** a repository operating offline either omits `[ci]` or leaves `[[ci.target]]` configured; in the latter case, non-zero command exits transition the target once into `drain.ci-probe-error`, opening **one** `ci-watch-unavailable` episode per target (`group_by = "target"`, cleared automatically on `drain.ci-probe-ok`), with exponential poll backoff and zero bogus code-repair items.
4. **No credential inside `tillr`:** in this environment `tillr` runs inside `yolo-jail` with no API token in its store or config, while `/home/agent/.yolo/bin/block/gh` forwards `yolo gh -- "$@"` to the host credential broker (just as `git push` uses `git-remote-yolo`). Executing the configured `command` with the loop's inherited `PATH` and minimal broker environment variables lets the external broker authenticate on the host while `tillr` never sees, stores, or logs a credential — reinforced by mandatory token-pattern scrubbing before any log or stderr text reaches disk or `.tillr/state.db`.

---

## 5. Repeated failures and runs still in progress when the next commit is published

### 5.1 One alert and one item per distinct failure, not per commit

A repository's CI workflow frequently runs all checks inside a single job and step — in this repository [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) runs everything inside job `check`, step `Run just check-ci`. Grouping only by `(workflow, failing_job, failing_step)` would collapse unrelated bugs into one episode, whereas grouping by `commit_sha` or unnormalized log hashes would fire a separate alert and item on every commit published while a fault remains unfixed.

As the designer's decision ([D-CI8](#D-CI8); owner may overturn), `tillr-drain` computes two fields on every failed `drain.ci-run` event before appending it:

1. **Normalized `failure_signature` (and human-readable `failure_keys`):** extracted mechanically from the scrubbed log after stripping ANSI escapes, timestamps, runner workspace/temp paths, hex addresses, commit SHAs, and elapsed durations:
   - failing test identifiers (`--- FAIL: TestName` + `FAIL\t<pkg>`, pytest `FAILED <nodeid>`, Cargo/Node test headers);
   - compiler, `go vet`, linter, or formatter file-and-rule diagnostics (`<repo-rel-path>:<diagnostic-kind>` without volatile line numbers);
   - `just` / recipe failure target plus first canonical error line when no test or compiler anchor is present;
   - fallback `(workflow, failing_job, failing_step, conclusion)` only when `log_path` is unavailable.
2. **Streak suppression (`repeat_of_unfixed`) and post-fix recurrence (`recurred_after_item`):**
   - When commit $C_n$ fails with `(target, workflow, failure_signature = S)`, `tillr-drain` checks prior `drain.ci-run` events on `(target, workflow)` and any tracking item linked to `dedup_key = ci:<target>:<workflow>:<S>`:
     - **While the fault is already active and unfixed in $C_n$:** if an ancestor commit already failed with $S$ since the last `"success"` on `(target, workflow)`, **and** no tracking item for `ci:<target>:<workflow>:<S>` has landed in $C_n$'s git ancestry (`landed_sha` is empty or `git merge-base --is-ancestor <landed_sha> <C_n>` is false), `tillr-drain` sets `repeat_of_unfixed = true`. Every commit's `drain.ci-run` event is still recorded in `events`, yet `ci-run-failed` requires `event.repeat_of_unfixed == false` — so 20 commits published before the fix lands cause **zero** reopenings, **zero** extra `triage` runs, and **zero** duplicate items.
     - **If a putative fix landed in $C_n$ and $S$ still fails:** if a tracking item $I$ for `ci:<target>:<workflow>:<S>` *is* an ancestor of $C_n$ (`landed_sha` $\preceq C_n$) and $C_n$ still fails with $S$, `tillr-drain` sets `repeat_of_unfixed = false` and `recurred_after_item = I`. That matches `ci-run-failed`, reopening the episode (`recurred-after-item-landed`, [`D-AW5`](./tillr-watch-alert-workflow.md#D-AW5)) so `triage` investigates why $I$ did not cure $S$ and files a follow-up!
     - **Prefix clearing on recovery:** `ci-run-failed` sets `group_by = ["target", "workflow", "failure_signature"]` and `clear_when = 'event.conclusion == "success" and event.supersedes_failure == true'`. Passing events omit `failure_signature`, resolving every open episode under `(target, workflow, *)` via built composite prefix clearing ([`D-TW50`](./tillr-watch.md#D-TW50)), while any episode not yet settled by `triage` still completes its single accountable settlement ([`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2)).

### 5.2 Runs still in progress when the next commit is published

When commit $C_2$ ($C_1 \prec C_2$) is published while $C_1$'s hosted run $R_1$ is still `queued` or `in_progress`, dropping $C_1$ from observation would lose $R_1$'s `drain.ci-run` record — and on an active trunk pushing faster than CI finishes, a single-slot watcher could starve forever without recording a single completed run. Conversely, watching concurrent runs without git-ancestry ordering introduces two out-of-order completion hazards if $R_1$ and $R_2$ finish out of commit order.

As the designer's decision ([D-CI9](#D-CI9); owner may overturn), `tillr-drain` maintains a **bounded per-target in-flight commit set** (`max_in_flight`, default `8`) with git-ancestry guards:

1. **Concurrent runs both stay observed:** $C_1$ remains in the in-flight set alongside $C_2$ until $R_1$ reaches a terminal state (or `run_timeout_seconds`), recording its own `drain.ci-run` event. Only if more than `max_in_flight` published commits accumulate behind a multi-hour host queue is the oldest unsettled commit evicted with a `drain.ci-run` record of `conclusion = "superseded-unobserved"` and `superseded_by_commit = <newest_sha>`.
2. **Host-cancelled runs on supersession (`concurrency: cancel-in-progress`):** if $R_1$ concludes `"cancelled"` and a newer commit $C_2$ ($C_1 \prec C_2$, or a post-rebase fork tip) has already been published on the same `published_ref`, `drain.ci-run` records `conclusion = "cancelled"` with `superseded_by_commit = C_2` and opens no alert. (If the *current* `published_ref` tip concludes `"cancelled"` with `superseded_by_commit == ""`, an unreplaced tip run was cancelled; `ci-run-failed` ignores `"cancelled"` by default unless `alert_on_tip_cancelled = true`.)
3. **Out-of-order failure after newer green (`superseded_by_green`):** if newer $C_2$ ($C_1 \prec C_2$) has *already* recorded `conclusion == "success"` on `(target, workflow)` before older $C_1$ finishes `"failure"`, $C_1$'s `drain.ci-run` event records `conclusion = "failure"`, `superseded_by_commit = C_2`, and `superseded_by_green = true`. `ci-run-failed` requires `event.superseded_by_green == false`, so a late-finishing older commit never raises a false alarm for a fault that $C_2$ already fixed.
4. **Out-of-order green before newer failure (`supersedes_failure`):** when $C_1$ finishes `"success"`, `tillr-drain` sets `supersedes_failure = true` **only if no descendant commit $C_2$ ($C_1 \prec C_2$) on `(target, workflow)` has already recorded a failing conclusion**. An older passing commit finishing late therefore never clears an alert opened by a newer failing commit.

---

## 6. Watching more than one repository, including an owned fork

This repository publishes its own trunk (`origin/main`) and also owns a fork whose patch series lives on `refs/heads/patch/<fork>` ([`tools/tillr-land/README.md` §Fork delivery](../../tools/tillr-land/README.md#fork-delivery)) and is published to a separate hosted repository (for example by a `pre-push` hook or roll step; [`tillr-restart.md` §Pushing what the proof has passed](../reference/tillr-restart.md#pushing-what-the-proof-has-passed)).

As the designer's decision ([D-CI10](#D-CI10); owner may overturn), multi-repository watching requires **no second `tillr` store and no second `tillr-drain` process**:

1. **Multiple `[[ci.target]]` entries in one `.tillr.toml`:** each target declares a unique `name` (e.g. `"trunk"` and `"pi-fork"`), its `published_ref` (e.g. `"refs/tillr/published/origin/main"` for trunk and `"refs/published/patch/pi"` or `"refs/remotes/fork/patch/pi"` for an owned fork), optional `git_dir` when the ref lives in `.forks/<fork>` rather than the root repository's object store, optional `delivery_ref` (e.g. `"refs/heads/patch/pi"`), and its own `command` / `log_command` pointing at that repository's hosted slug.
2. **Detecting fork publications without idle remote polling:** `tillr-drain` re-reads each configured target's local `published_ref` at loop startup and after every pass step that can publish commits (post-landing `push`, `pi` / extension rolls, and landing completions whose hooks publish fork branches). Whenever `published_ref` resolves to a commit with no terminal `drain.ci-run` event, `tillr-drain` adds `(target, sha)` to that target's in-flight watch set.
3. **Fork `releaseRebase` lineage shifts:** unlike trunk `main`, a fork delivery branch `refs/heads/patch/<fork>` legitimately moves non-fast-forward during `releaseRebase` onto a newer upstream tag (archiving the prior tip under `refs/tillr/patch-history/<fork>/<old>`; [`tools/tillr-land/README.md` §Fork delivery](../../tools/tillr-land/README.md#fork-delivery)). When `published_ref` moves non-fast-forward (`git merge-base --is-ancestor <prev_sha> <new_sha>` is false), `tillr-drain` treats `<new_sha>` as superseding pre-rebase in-flight commits on that target (`superseded_by_commit = <new_sha>`) and begins watching `<new_sha>`.
4. **Target-isolated alerts, silences, and fork item routing:** every `drain.ci-run` and `drain.ci-probe-error` event sets `subject = target` and carries `target` and `delivery_ref`. `ci-run-failed` parameterizes `variables = { target = "event.target", workflow = "event.workflow", signature = "event.failure_signature" }`, so a bounded silence ([`R-AW5`](./tillr-watch-alert-workflow.md#R-AW5)) on `target=pi-fork` never mutes `target=trunk`. When `SeatTriage` (or direct filing) files a repair request for a fork target with `delivery_ref = "refs/heads/patch/<fork>"`, the request names the fork target and delivery branch so `classify` and `LaneStager` dispatch the repair through `tillr`'s existing fork lane and `fork-build` gate.

---

## 7. Failure modes, non-goals, and alternatives considered

### 7.1 Edge cases and failure modes

- **Matrix or multi-job workflows failing in multiple jobs on one run:** `drain.ci-run` records the primary `(failing_job, failing_step)` alongside the full `failing_jobs` array, and `failure_signature` incorporates normalized failure keys across all failed jobs in that `run_id` (capped to deterministic sorted keys), keeping one `drain.ci-run` event per `(target, commit_sha, run_id)` while preserving every failing job's evidence in `log_path`.
- **Single transient hosted runner flake:** if $C_1$ fails because of a transient runner glitch and $C_2$ (with no tracking item) passes `"success"` minutes later, `ci-run-failed` auto-resolves via prefix clearing; `SeatTriage` still inspects $C_1$'s log once ([`OQ-AW2`](./tillr-watch-alert-workflow.md#OQ-AW2)) and escalates if a rule/workflow retry or runner fix is warranted rather than silently dropping the flake.
- **Hung hosted workflow:** if a run stays `queued` or `in_progress` past `run_timeout_seconds` (default `3600`), `tillr-drain` records `drain.ci-run` with `conclusion = "timed_out"` (observed locally as host-stalled) so no commit remains in flight forever.

### 7.2 What this does not do

- Does not run hosted CI before landing or replace local fast gate or heavy proof (`tillr-gate`).
- Does not store API tokens, OAuth secrets, or SSH keys in `.tillr.toml` or `.tillr/state.db`, or embed a GitHub/GitLab HTTP client in Go.
- Does not automatically re-run failed hosted workflows (`gh run rerun` violates [`OQ-TW2`](./tillr-watch.md#OQ-TW2) and [Landing Principle §11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out): re-running without a diagnosis hides flakes instead of fixing or quarantining them).
- Does not revert published commits or hold local landings ([§3.1](#31-environment-disagreement-when-local-proof-passed-never-a-revert), subject to [OQ-CI1](#OQ-CI1)).

### 7.3 Alternatives considered

- **Polling GitHub from a foreground-agent background shell or cron job:** rejected by the owner's 2026-11-26 instruction ([R-CI1](#R-CI1)); dies with the foreground session, leaves no `.tillr/state.db` record, and wakes the foreground session to hand-file items.
- **Webhook receiver HTTP server inside `tillr-view` or `tillr-drain`:** rejected; `tillr` runs on developer workstations and inside `yolo-jail` namespaces with no inbound public ingress or webhook secret.
- **Adding `command` polling directly to `tillr-watch` rules instead of `tillr-drain`:** rejected in [§2.1](#21-why-tillr-drain-watches-rather-than-tillr-watch-or-a-third-daemon); blocks on [`OQ-GW2`](./general-agent-watch.md#OQ-GW2), polls when idle, and cannot manage per-commit in-flight sets, ancestry supersession, or retained log files.

---

## 8. Build slices, smallest first

Following [Landing Principle §15](./landing-principles.md#15-land-the-smallest-functional-change), the feature lands in five self-contained slices, smallest first. Slices 1–3 do not wait on [OQ-CI1](#OQ-CI1)–[OQ-CI3](#OQ-CI3); Slice 4 implements the owner's rulings on those three questions.

| Slice | Scope | What the operator stops doing by hand | Depends on |
|---|---|---|---|
| **1. Settings, command runner, log scrubber, and one-shot `tillr drain ci-status`** | Parse and validate `[[ci.target]]` in `tools/tillr/settings/settings.go` (default off; unknown keys refused under [`D-RT7`](../reference/tillr-restart.md#why-its-this-way)). Implement the shell-less `command` / `log_command` runner, secret scrubber, `<root>/.tillr/loop/ci-logs/` writer, `failure_signature` extractor, and `drain.ci-run` / `drain.ci-probe-error` events behind one-shot `tillr drain ci-status` (plus unit tests with fixture scripts covering pass, fail, pending, `no-run`, and probe error). | Stops looking up the currently published SHA on trunk and owned forks, running ad-hoc `gh run list` / `gh run view --log-failed` commands per repo, and copying failure logs by hand to inspect CI health. | None |
| **2. Publication-driven background watcher in `tillr-drain loop`** | Wire per-target in-flight commit tracking (`max_in_flight`, startup recovery of unsettled `published_ref` tips, fork `published_ref` and `releaseRebase` detection, ancestry-based `superseded_by_green` / `supersedes_failure`, and `repeat_of_unfixed` / `recurred_after_item`) into `tillr-drain loop`, with zero polling when all published commits are settled and clean cancellation across `tillr-drain restart`. | Stops keeping background shell loops or timed reminders after pushes to wait 5–15 minutes for hosted workflows on trunk and forks to finish. | Slice 1 |
| **3. Shipped `tillr-watch` rules and status surfaces** | Ship `ci-run-failed.toml` (composite `group_by = ["target", "workflow", "failure_signature"]` with [`D-TW50`](./tillr-watch.md#D-TW50) prefix clearing) and `ci-watch-unavailable.toml` (`group_by = "target"`), initially testable against `tillr watch evaluate`, plus read-only CI status in `tillr drain why` and the Observatory push/alerts views. | Stops querying events or hosting UIs to notice red CI or broken broker credentials, and stops mentally deduplicating the same failure across repeated green pushes. | Slice 2 |
| **4. Automated failure routing, item filing, and worker brief CI log staging** | Enable automated routing per [OQ-CI2](#OQ-CI2) (`receiver = "triage"` on `ci-run-failed` under [D-CI6](#D-CI6), or direct drain filing), apply [OQ-CI1](#OQ-CI1) and [OQ-CI3](#OQ-CI3), propagate `delivery_ref` for fork targets, and teach `LaneStager` to copy linked `drain.ci-run` logs into `<durable_dir>/ci.logs/` and render failure context in the worker brief. | Stops reading failing CI logs in the foreground session, diagnosing local-proof-vs-hosted-CI discrepancies, and hand-filing repair requests (the four hand-filed items of 2026-11-26). | Slice 3 + [OQ-CI1](#OQ-CI1), [OQ-CI2](#OQ-CI2), [OQ-CI3](#OQ-CI3) |
| **5. Repository adapter script and `.tillr.toml` targets here** | Add a small versioned `gh`-based adapter script (using the external `yolo gh` broker with zero credentials in `tillr`) and configure `[[ci.target]]` in this repository's `.tillr.toml` for `trunk` and its owned fork, updating Layer-2 module docs once all faces exist. | Stops starting any session-local CI watcher when running unattended drains on this repository and its fork. | Slice 4 |

---

## Open Questions

1. 💬 **OQ-CI1: When hosted CI fails on a published commit that passed the local after-merge proof, does local proof remain the sole authority for `"green"` and automatic publishing, or may a red hosted run hold publishing?**

   Today `refs/tillr/green` means the last trunk commit whose local after-merge proof passed ([`D-GR5`](../reference/gate.md#D-GR5)), `push.what = "green"` pushes `refs/tillr/green` ([`D-RT10`](../reference/tillr-restart.md#why-its-this-way)), and neither local proof redness nor watch alerts cap landings ([`OQ-GR1`](../reference/gate.md#OQ-GR1), [`D-TW2`](./tillr-watch.md#D-TW2)). Allowing hosted CI to hold pushes or block `refs/tillr/green` / `tillr-roll` would redefine what `"green"` means — and holding trunk pushes while hosted CI is red would prevent `tillr-drain` from automatically pushing the repair commit that turns hosted CI green again.

   - **Option A:** Local proof stays the sole authority for `refs/tillr/green`, binary rolls, and automatic pushes. The CI watcher never reverts a commit and never holds lanes, landings, rolls, or pushes; it records `drain.ci-run` and triggers repair/escalation inside `tillr`.
   - **Option B:** Never revert and never hold trunk pushes (so repairs can still be pushed to verify on CI), while adding an opt-in per-target `hold_roll_on_red_ci = true` (off by default) that pauses local `tillr-roll` / fork pin rolls while that target's latest completed CI run is red.
   - **Option C:** Add a new `push.what = "ci-green"` / push-hold mode that blocks further pushes or advances a separate `refs/tillr/ci-green` ref only after hosted CI passes, accepting remote runner latency in the publication loop and requiring an exemption path to push CI-fix commits.

   <!-- vantage: question id=OQ-CI1 leaning="A — local proof is fast, deterministic and owned by the repository; hosted CI failures after a green local proof are environment discrepancies to fix forward (and fold back into local gate checks where possible), whereas holding pushes deadlocks remote verification of the fix." -->
   _Leaning:_ A — local proof is fast, deterministic and owned by the repository; hosted CI failures after a green local proof are environment discrepancies to fix forward (and fold back into local gate checks where possible), whereas holding pushes deadlocks remote verification of the fix.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-CI2: Should a distinct hosted CI failure route through `tillr-watch` to the `triage` seat, or should `tillr-drain` also/instead file a repair request mechanically without a `triage` turn?**

   The `triage` seat (`scout` tier, read-only, landed through `25ceb253`) already claims unsettled `receiver = "triage"` episodes, inspects logs and open items, files or links one deduplicated `fix-fault` request (`--dedup-key`), and escalates external faults without spending a `build` lane. Alternatively, `tillr-drain` could mechanically file a `request` item on every new `failure_signature` without spending a `scout` turn, at the cost of dispatching a full code-writing `build` lane whenever hosted CI fails from an external runner outage, network flake, or billing limit.

   - **Option A:** Route `ci-run-failed` to `receiver = "triage"`. `SeatTriage` inspects the scrubbed log and open items once per distinct `failure_signature`, files or links one deduplicated `fix-fault` item for repository/workflow/gate-parity defects, and escalates external runner/credential outages to `operator-session` or `owner`.
   - **Option B:** Have `tillr-drain` mechanically file one deduplicated `request` item per distinct `failure_signature` immediately on `drain.ci-run` failure (and pre-settle the `ci-run-failed` episode against that item), skipping `SeatTriage` even when the failure may be an external host outage.
   - **Option C:** Make failure action configurable per `[[ci.target]]` (`on_failure = "triage" | "file-request" | "alert-only"`, defaulting to `"triage"`).

   <!-- vantage: question id=OQ-CI2 leaning="A — one cheap scout-tier triage turn per distinct failure signature prevents external GitHub/runner outages from dispatching full build lanes, links existing open items cleanly, and reuses the alert-triage and silence machinery already built." -->
   _Leaning:_ A — one cheap scout-tier triage turn per distinct failure signature prevents external GitHub/runner outages from dispatching full build lanes, links existing open items cleanly, and reuses the alert-triage and silence machinery already built.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-CI3: Should repair items filed for hosted CI failures enter the queue at express priority or ordinary priority?**

   Under [the operator-decisions design §6](./operator-decisions-inside-tillr.md#6-what-is-the-owners), moving an item ahead of ordinary queued work when no queued item is `blocked_by` it is an ordering policy choice that belongs to the owner. Because local proof is green ([`OQ-CI1`](#OQ-CI1) Option A), no local item is `blocked_by` a red hosted workflow — yet every push to `main` (or the owned fork) continues to fail publicly on the host until the discrepancy lands and is pushed.

   - **Option A:** File hosted-CI repair requests as `--express` (using the reserved express lanes in `.tillr.toml` `[lanes] express = 2`), so public CI returns to green promptly before many more batches publish on top of the discrepancy.
   - **Option B:** File hosted-CI repair requests at ordinary priority unless an item explicitly depends on them, keeping express lanes strictly for owner-marked urgent requests and blocking defects.
   - **Option C:** Default to `--express` on `trunk` and make priority configurable per `[[ci.target]]` (`repair_priority = "express" | "ordinary"`).

   <!-- vantage: question id=OQ-CI3 leaning="A (or C defaulting to express) — express lanes exist in .tillr.toml so small urgent repairs do not wait behind a deep ordinary backlog while every green push stays red on the public remote." -->
   _Leaning:_ A (or C defaulting to express) — express lanes exist in `.tillr.toml` so small urgent repairs do not wait behind a deep ordinary backlog while every green push stays red on the public remote.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

Every `D-CI*` row below is the designer's decision answering the 2026-11-26 operator request (`item-request-75251137d9`), not an owner ruling, and the owner may overturn it.

| ID | Decision | Date | Where | Built? |
|---|---|---|---|---|
| <a id="R-CI1"></a>**R-CI1** | **Owner ruling.** `"design a feature that ties into a CI watcher directly from tillr instead of the foreground agent"` | 2026-11-26 | [§1](#1-the-request-the-2026-11-26-failures-and-the-standing-rulings) | No |
| <a id="R-CI2"></a>**R-CI2** | **Owner ruling ([R-AT1](./tillr-alert-triage-seat.md#R-AT1), [operator-decisions §1](./operator-decisions-inside-tillr.md#1-the-ruling)).** Work the operating session could settle without the owner settles inside `tillr`, while seat/model policy and non-blocking queue ordering stay the owner's ([operator-decisions §6](./operator-decisions-inside-tillr.md#6-what-is-the-owners)). | 2026-11-21, 2026-11-25 | [§1](#1-the-request-the-2026-11-26-failures-and-the-standing-rulings), [§3.2](#32-failure-action-deduplicated-watch-alert-to-triage-versus-a-trunk-fixer) | Yes (triage seat) |
| <a id="R-CI3"></a>**R-CI3** | **Owner ruling ([OQ-TW2](./tillr-watch.md#OQ-TW2), [D-TW2](./tillr-watch.md#D-TW2), [R-AT2](./tillr-alert-triage-seat.md#R-AT2)).** Watch rules and watch triage execute no remedies, and alerts never fail, bench, or cap work. | 2026-11-20, 2026-11-21 | [§1](#1-the-request-the-2026-11-26-failures-and-the-standing-rulings), [§2.1](#21-why-tillr-drain-watches-rather-than-tillr-watch-or-a-third-daemon) | Yes |
| <a id="R-CI4"></a>**R-CI4** | **Owner ruling ([R-AW1](./tillr-watch-alert-workflow.md#R-AW1), [R-AW4](./tillr-watch-alert-workflow.md#R-AW4), [R-AW5](./tillr-watch-alert-workflow.md#R-AW5), [OQ-AW2](./tillr-watch-alert-workflow.md#OQ-AW2)).** Every alert gets an investigation and settles via a parameterized item-linked silence, `fix-rule`, or `fix-fault`, including episodes that auto-clear before triage finishes. | 2026-11-22, 2026-11-23 | [§1](#1-the-request-the-2026-11-26-failures-and-the-standing-rulings), [§5.1](#51-one-alert-and-one-item-per-distinct-failure-not-per-commit) | Yes |
| <a id="R-CI5"></a>**R-CI5** | **Owner ruling ([D-GR5](../reference/gate.md#D-GR5), [D-GR6](../reference/gate.md#D-GR6), [OQ-GR1](../reference/gate.md#OQ-GR1)).** Local after-merge proof governs `refs/tillr/green` and local `auto_revert`, with no landing count or time red cap. | 2026-11-15, 2026-11-17, 2026-11-18 | [§1](#1-the-request-the-2026-11-26-failures-and-the-standing-rulings), [§3.1](#31-environment-disagreement-when-local-proof-passed-never-a-revert) | Yes |
| <a id="R-CI6"></a>**R-CI6** | **Owner ruling ([D-RT7](../reference/tillr-restart.md#why-its-this-way), [D-RT10](../reference/tillr-restart.md#why-its-this-way)).** Repository automation in `.tillr.toml` defaults off with unknown keys refused; this repository sets `push.what = "green"`. | 2026-11-15, 2026-11-18 | [§2.2](#22-how-tillr-drain-learns-a-runs-result-the-host-neutral-command-contract), [§4](#4-staying-optional-host-neutral-offline-safe-and-credential-free) | Yes |
| <a id="R-CI7"></a>**R-CI7** | **Standing landing principles ([Landing Principles §2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch), [§3](./landing-principles.md#3-gate-what-will-be-the-tip-once), [§8](./landing-principles.md#8-a-gate-that-cannot-answer-must-say-which), [§11](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out), [§15](./landing-principles.md#15-land-the-smallest-functional-change)).** Cost must not grow with trunk activity; probe inability and check failure stay distinct classes; failures are diagnosed rather than waited out; changes land in smallest functional slices. | 2026-11-20 to 2026-11-26 | [§1](#1-the-request-the-2026-11-26-failures-and-the-standing-rulings), [§2.2](#22-how-tillr-drain-learns-a-runs-result-the-host-neutral-command-contract), [§5](#5-repeated-failures-and-runs-still-in-progress-when-the-next-commit-is-published), [§8](#8-build-slices-smallest-first) | Yes |
| <a id="OQ-CI1"></a>[OQ-CI1](#OQ-CI1) | _Open._ Whether local proof stays the sole authority for `"green"`, rolls, and automatic pushes when hosted CI fails (never reverting or holding pushes), or whether an opt-in roll/push hold is added. | — | [§3.1](#31-environment-disagreement-when-local-proof-passed-never-a-revert), [Open Questions](#open-questions) | No |
| <a id="OQ-CI2"></a>[OQ-CI2](#OQ-CI2) | _Open._ Whether distinct hosted CI failures route via `ci-run-failed` to `SeatTriage` (`receiver = "triage"`), file a repair request mechanically from `tillr-drain`, or make the action configurable per target. | — | [§3.2](#32-failure-action-deduplicated-watch-alert-to-triage-versus-a-trunk-fixer), [Open Questions](#open-questions) | No |
| <a id="OQ-CI3"></a>[OQ-CI3](#OQ-CI3) | _Open._ Whether repair requests filed for hosted CI failures use `--express` or ordinary queue priority. | — | [§3.2](#32-failure-action-deduplicated-watch-alert-to-triage-versus-a-trunk-fixer), [Open Questions](#open-questions) | No |
| <a id="D-CI1"></a>**D-CI1** | **Designer decision (owner may overturn).** Place CI observation inside `tillr-drain` (`tillr-drain loop` post-publication background observer + one-shot `tillr drain ci-status`), keeping `tillr-watch` purely declarative over store events and rejecting both `tillr-watch` command polling and a third long-lived daemon. | 2026-11-26 | [§2.1](#21-why-tillr-drain-watches-rather-than-tillr-watch-or-a-third-daemon) | No |
| <a id="D-CI2"></a>**D-CI2** | **Designer decision (owner may overturn).** Configure optional `[[ci.target]]` blocks in `.tillr.toml` (all off by default) with shell-less `command` and optional `log_command` argument vectors, zero credentials in `tillr`, JSON stdout status reporting, and a strict exit-`0` vs non-zero boundary between red CI runs and `drain.ci-probe-error` faults. | 2026-11-26 | [§2.2](#22-how-tillr-drain-learns-a-runs-result-the-host-neutral-command-contract), [§4](#4-staying-optional-host-neutral-offline-safe-and-credential-free) | No |
| <a id="D-CI3"></a>**D-CI3** | **Designer decision (owner may overturn).** Record one `drain.ci-run` event per published commit's workflow run in `events` (with conclusion, failing job/step, `failure_signature`, ancestry flags, and `log_path`), state-change `drain.ci-probe-error` / `drain.ci-probe-ok` events, and secret-scrubbed bounded failure logs at `<root>/.tillr/loop/ci-logs/<target>/<commit_sha>-<run_id>.log`. | 2026-11-26 | [§2.3](#23-store-records-and-retained-failure-logs) | No |
| <a id="D-CI4"></a>**D-CI4** | **Designer decision (owner may overturn).** Treat a hosted CI failure on a locally proved commit as an environment-disagreement finding (missing local gate check, host-coupled test, or runner fault), never as a commit revert. | 2026-11-26 | [§3.1](#31-environment-disagreement-when-local-proof-passed-never-a-revert) | No |
| <a id="D-CI5"></a>**D-CI5** | **Designer decision (owner may overturn; subject to [OQ-CI1](#OQ-CI1)).** Never revert commits and never hold lanes, landings, `refs/tillr/green`, binary rolls, or automatic pushes on red or in-progress hosted CI. | 2026-11-26 | [§3.1](#31-environment-disagreement-when-local-proof-passed-never-a-revert) | No |
| <a id="D-CI6"></a>**D-CI6** | **Designer decision (owner may overturn; subject to [OQ-CI2](#OQ-CI2) and [OQ-CI3](#OQ-CI3)).** Reject `fixer-main-<sha>` trunk fixers for hosted CI failures; ship `ci-run-failed` (`receiver = "triage"`) and `ci-watch-unavailable` rules so `SeatTriage` investigates once per distinct failure signature, files/links one deduplicated `--express` `fix-fault` item (`--dedup-key`), and escalates external runner/credential faults. | 2026-11-26 | [§3.2](#32-failure-action-deduplicated-watch-alert-to-triage-versus-a-trunk-fixer) | No |
| <a id="D-CI7"></a>**D-CI7** | **Designer decision (owner may overturn).** Teach `LaneStager` to copy linked `drain.ci-run` failure logs into `<durable_dir>/ci.logs/` and include failing job/step, assertion summary, and environment-discrepancy guidance in the worker brief so repair lanes never touch `.tillr/`. | 2026-11-26 | [§3.2](#32-failure-action-deduplicated-watch-alert-to-triage-versus-a-trunk-fixer) | No |
| <a id="D-CI8"></a>**D-CI8** | **Designer decision (owner may overturn).** Deduplicate by mechanical `failure_signature`, set `repeat_of_unfixed = true` on subsequent commits that lack a landed tracking item in their ancestry, clear episodes via [`D-TW50`](./tillr-watch.md#D-TW50) prefix clearing on `"success"`, and reopen (`recurred_after_item`) if a commit containing the landed fix still fails with the same signature. | 2026-11-26 | [§5.1](#51-one-alert-and-one-item-per-distinct-failure-not-per-commit) | No |
| <a id="D-CI9"></a>**D-CI9** | **Designer decision (owner may overturn).** Keep a bounded per-target set of in-flight published commits (`max_in_flight = 8`) across concurrent pushes and restarts, suppress alerts on supersession-cancelled runs, and guard out-of-order completions by git ancestry (`superseded_by_green` and `supersedes_failure`). | 2026-11-26 | [§5.2](#52-runs-still-in-progress-when-the-next-commit-is-published) | No |
| <a id="D-CI10"></a>**D-CI10** | **Designer decision (owner may overturn).** Watch multiple repositories (trunk plus owned forks) from one `tillr-drain loop` and one `.tillr/state.db` via multiple `[[ci.target]]` entries observing each target's `published_ref` (handling non-fast-forward fork `releaseRebase` shifts) and routing fork repair items via `delivery_ref`. | 2026-11-26 | [§6](#6-watching-more-than-one-repository-including-an-owned-fork) | No |
