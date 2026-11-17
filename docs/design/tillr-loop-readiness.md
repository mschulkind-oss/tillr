---
title: "Tillr — loop readiness: what blocks an overnight burn, and what to leave broken"
status: proposed
stage: CURRENT
updated: 2026-11-12
tags: [tillr, drain, gate, loop, readiness, design-ruling]
next: "Land B1–B4 (~115 LOC across 4 files), pass the end-to-end canary in a throwaway clone, then run a 1-lane 6-tick burn on item-seed-02 and item-seed-04"
---

# Tillr — loop readiness: what blocks an overnight burn, and what to leave broken

> **Carried out** in commit `1c742dcbd`, which cleared the four things that stopped the first burn. This document stays as the record.

**Status:** 2026-11-12. Read against [`main`](../../AGENTS.md) at `a7d942d2c8` and the live
canary run of 2026-11-12 (`item-canary-01` dispatched on `deepseek-v3.2`, committed on
`tillr/lane-1`, and halted at [`tools/tillr-gate`](../../tools/tillr-gate/main.go)).

> **In short.** Staging, backend dispatch, in-lane commits, attempt classification, and
> digest verification all work on trunk today. Four small defects ([B1](#b1-gate-delegation)–[B4](#b4-sterile-worktree-reset),
> `ESTIMATE: ~115 LOC` total) plus one subprocess timeout ([D-LR4](#d-lr4)) separate the current
> tree from a safe unattended burn — and three of the five seeded items in
> `config/tillr/seed-items.json` target the `patch/pi`
> fork series, which breaks the lane-worktree and ref-boundary model and must be held out of
> tonight's queue ([D-LR3](#d-lr3)).

**Why it matters.** The owner wants an unattended overnight loop that drains migrated work
and lands verified commits onto `main`. An unattended loop that stops early on an honest
refusal costs nothing; one that lands an unfenced edit onto `main`, trips its own boundary
breaker on a fork ref, or hangs until morning on a wedged backend socket is worse than not
running at all.

**The shape.** Keep [`tools/fork-check.py`](../../tools/fork-check.py) as tonight's judge behind
[`tools/tillr-gate`](../../tools/tillr-gate/gate.go)'s existing `--gate` wrapper (`RunGate`),
check `allowed_paths` (`fence.txt`) against the candidate's diff before the gate runs, populate
`allowed_paths` in `config/tillr/seed-items.json` while
holding the three `patch/pi` items out of the first burn, and run `--lanes 1 --max-ticks 6`
with a 25-minute per-run timeout. Leave the seven non-blocking gaps ([Section 6](#6-what-we-would-not-fix-tonight))
alone.

**Cost.** `ESTIMATE: ~115 LOC` of production changes across [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop),
`config/tillr/seed-items.json`,
[`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go),
[`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go), and
[`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go), plus `~55 LOC` of
tests — under an hour of implementation before the canary re-run.

**Start at** [Section 1](#1-what-the-canary-proved-and-the-blocking-set) for the ranked blockers,
[Section 2](#2-who-judges-a-candidate-tonight) for the exact gate argv,
[Section 3](#3-the-canary-item-versus-the-seed-items) for why `patch/pi` items fail in a lane
worktree, and [Section 6](#6-what-we-would-not-fix-tonight) for the temptations to skip.
**Needs your ruling:** none — all six questions from the brief are decided in
[D-LR1](#d-lr1)–[D-LR6](#d-lr6).
**Reads with:** [`tillr-day2.md`](tillr-day2.md), [`tillr-muse-backend.md`](tillr-muse-backend.md),
and [`tillr-seed-triage.md`](tillr-seed-triage.md).

---

## 1. What the canary proved, and the blocking set

On 2026-11-12 at commit `a7d942d2c8`, four consecutive canary invocations exercised
[`tools/tillr-drain`](../../tools/tillr-drain/main.go) against a one-item seed (`item-canary-01`,
with `allowed_paths: ["CANARY.md"]`). Together they established what already works in the
pipeline of [`tillr-day2.md`](tillr-day2.md) [§4.1](tillr-day2.md#41-per-tick-pipeline-in-tillr-drain-loop)
and where it stops:

1. **Staging works (`a7d942d2c8`).** [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go)
   (`LaneStager.Stage`) created `<lane-dir>/worktree` on branch `tillr/lane-1` at `main`'s
   `base_sha`, rendered all nine sections of `<lane-dir>/brief.md` per
   [`tillr-muse-backend.md`](tillr-muse-backend.md) [§3](tillr-muse-backend.md#3-how-a-tillr-item-becomes-a-prompt),
   and wrote `<lane-dir>/fence.txt` and `<lane-dir>/base_sha`.
2. **Backend failover and Muse dispatch work (`fd98fd5e95`, `a4cc6e4579`).** On the first
   canary attempt, `pi` failed in the infrastructure class and
   [`tools/tillr-drain/drain/failover.go`](../../tools/tillr-drain/drain/failover.go) failed
   over to `agy` on the next tick. On the fourth attempt, `deepseek-v3.2` ran through
   [`tools/tillr-run/backends/muse`](../../tools/tillr-run/backends/muse), read `brief.md`,
   wrote `CANARY.md`, committed `chore: add canary marker` on `tillr/lane-1` ([`tillr-muse-backend.md`](tillr-muse-backend.md)
   [`D-MM11`](tillr-muse-backend.md#decision-ledger)), and emitted `status: answered`.
3. **Candidate classification works (`a27243bf3c`).**
   [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (`classifyAttempt`)
   read `status: answered` from `run.log`, confirmed `HEAD != base_sha` in `<lane-dir>/worktree`,
   verified the host boundary ([`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go)),
   and promoted the run to `CandidateReady`.
4. **The gate step refused twice — first rightly, then on an unbuilt boundary.**
   - Against a hand-authored registry carrying a zero digest, [`tools/tillr-gate/registry.go`](../../tools/tillr-gate/registry.go)
     (`LoadRegistry`) exited `3`: the declarations hashed to a non-zero SHA-256 digest, proving
     digest verification works.
   - Against the real 65-declaration registry exported by `python3 tools/fork-check.py --registry-json`,
     [`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go) (`Run`) exited `4`:
     `the plan owes 3 in-process check(s) this face cannot run (file-size/..., scratch, design-citations): a verdict that omitted them would assert a transition that did not happen`.
5. **Landing and mechanical verdict were never reached.** Because `tillr-loop gate` exited `4`,
   [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (`verifyCandidate`)
   classified the tick as `AttemptInfrastructure` (`gate-error`), never called `tillr-loop land`
   or `tillr-loop verdict`, and left `item-canary-01` in `in-queue`.

<a id="d-lr1"></a>
> [!IMPORTANT]
> **`[D-LR1]` Four blockers must be fixed before an overnight burn; everything else waits.**
> The blocking set is [B1](#b1-gate-delegation) (wire `tillr-loop gate` to run
> [`tools/fork-check.py`](../../tools/fork-check.py) via `tillr-gate run --gate`),
> [B2](#b2-seed-items-fence-and-fork-filter) (populate `allowed_paths` in
> `config/tillr/seed-items.json` and hold the three
> `patch/pi` items), [B3](#b3-post-run-fence-enforcement) (enforce `fence.txt` against
> `git diff --name-only base..head` before the gate runs), and [B4](#b4-sterile-worktree-reset)
> (prune and force-remove any prior `<lane-dir>/worktree` before `git worktree add`).

### The ranked blocking set

| Rank | Blocker | What breaks tonight if unfixed | Smallest fix | Size (`ESTIMATE`) |
| :--- | :--- | :--- | :--- | :--- |
| **1** | <a id="b1-gate-delegation"></a>**B1 — `tillr-loop gate` runs `tillr-gate` natively instead of delegating to [`tools/fork-check.py`](../../tools/fork-check.py), and `CommandSteps.Gate` passes no `--registry` or `--gate-program`** | **Every candidate fails at `Gate` with `gate-error` (exit `4`).** In [`tools/tillr-gate/plan.go`](../../tools/tillr-gate/plan.go) (`owedInProcess`), `scratch` has `selects: []` in the real registry, so it is owed by **every** diff regardless of path; [`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go) (`Run`) refuses whenever `len(plan.InProcess) > 0`. And [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go) (`CommandSteps.Gate`) passes only `--store`, `--item-id`, `--run-id`, `--lane`, `--worktree`, `--base-sha`, and `--head-sha` to `tillr-loop gate`. | In [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop) (`run_gate`), default `registry` from `${TILLR_LOOP_REGISTRY:-}` (generating `<run-work-dir>/registry.json` via `python3 "$repo_root/tools/fork-check.py" --registry-json` when unset) and default `gate_program` to run `python3 tools/fork-check.py --since "$base_sha" --no-wait` with `PI_FORKS_ROOT="$repo_root"` (exact argv in [Section 2](#2-who-judges-a-candidate-tonight)). | `ESTIMATE: 25 LOC` in [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop) + `15 LOC` in [`tools/tillr-loop/test.py`](../../tools/tillr-loop/test.py) (~15 min) |
| **2** | <a id="b2-seed-items-fence-and-fork-filter"></a>**B2 — `config/tillr/seed-items.json` omits `allowed_paths` on all 5 items and leaves 3 `patch/pi` fork items `in-queue`** | **The loop trips `StopInfraCircuitBreaker` in `<1s` without dispatching a single run.** [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) (`briefValues`) refuses any item with `len(recorded.AllowedPaths) == 0`, and [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (`dispatchOne`) counts every `Stage` error toward `ConsecutiveInfra`. Three staging refusals halt the loop immediately. Even with `allowed_paths` added, the 3 `patch/pi` items break in a lane worktree ([Section 3](#3-the-canary-item-versus-the-seed-items)). | Add the `allowed_paths` lists from [`tillr-seed-triage.md`](tillr-seed-triage.md) ([The list](tillr-seed-triage.md#the-list)) to all 5 items in `config/tillr/seed-items.json`, and set `state: "blocked"` on `item-seed-01`, `item-seed-03`, and `item-seed-05` so tonight's queue dispatches only `item-seed-02` and `item-seed-04`. | `ESTIMATE: 25 LOC` in `config/tillr/seed-items.json` (~10 min) |
| **3** | <a id="b3-post-run-fence-enforcement"></a>**B3 — `allowed_paths` (`fence.txt`) is rendered into `brief.md` and never checked against the candidate's commit before `Gate`/`Land`** | **An out-of-scope edit inside `<lane-dir>/worktree` lands onto `main`.** [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go) (`TakeBoundary`) excuses the entire target `<lane-dir>/worktree`, and [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (`verifyCandidate`) goes straight from `CandidateReady` to `Steps.Gate`. A model that edits [`AGENTS.md`](../../AGENTS.md), [`tools/fork-check.py`](../../tools/fork-check.py), or an unrelated module inside its worktree is never stopped unless a gate check happens to fail. | Check `git -C <worktree> diff --name-only <base_sha>..<head_sha>` and `git -C <worktree> status --porcelain` against `<lane-dir>/fence.txt` before `Steps.Gate` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (`verifyCandidate`). On any out-of-fence or dirty path, treat as `GateFailed` (`boundary-scope-escape`, [Section 5](#5-the-stop-that-saves-the-owner)). | `ESTIMATE: 45 LOC` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) + `25 LOC` in [`tools/tillr-drain/loop_test.go`](../../tools/tillr-drain/loop_test.go) (~20 min) |
| **4** | <a id="b4-sterile-worktree-reset"></a>**B4 — `LaneStager.Stage` runs `git worktree add --force -B` without pruning stale registrations or removing dirty worktree directories** | **A prior crashed run or leftover untracked file in `<lane-dir>/worktree` either fails `Stage` (exit `255`) or leaks untracked files into the next tick's worktree.** Canary attempt 2 (`loop2.err` on 2026-11-12) failed with `fatal: cannot force update the branch 'tillr/lane-1' used by worktree at '<lane-dir>'`. Furthermore, `git worktree add --force` over an existing directory does not delete untracked files left by the previous run on that lane. | In [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) (`prepareWorktree`), run `git -C <repoRoot> worktree remove --force <worktree>` (ignoring "not a working tree"), `os.RemoveAll(<worktree>)`, and `git -C <repoRoot> worktree prune` before `git -C <repoRoot> worktree add --force -B <branch> <worktree> <baseSHA>`. | `ESTIMATE: 20 LOC` in [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) + `15 LOC` in [`tools/tillr-drain/stage_test.go`](../../tools/tillr-drain/stage_test.go) (~15 min) |

---

## 2. Who judges a candidate tonight

<a id="d-lr2"></a>
> [!IMPORTANT]
> **`[D-LR2]` [`tools/fork-check.py`](../../tools/fork-check.py) judges candidates tonight, driven by `tillr-gate run --gate`; [`tools/tillr-gate`](../../tools/tillr-gate/main.go) is the plan and verdict-envelope binder.**
> Do not port `file-size`, `scratch`, and `design-citations` into Go tonight, and do not bypass
> `tillr-gate` to call `fork-check.py` directly from `tillr-drain`. Use the `--gate` delegation
> face that [`tools/tillr-gate/gate.go`](../../tools/tillr-gate/gate.go) (`RunGate`) and
> [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop) (`--gate-program`) already
> implement.

### Why both halves are needed tonight

`tools/tillr-land/land/evaluate.go` (`Evaluate`; the file was removed in commit `e07510bba`, when the lander became one squash)
refuses to move `refs/heads/main` unless it is handed a `gate.verdict` envelope whose
`tree_sha` and `base_sha` match the `land.candidate` record and whose `plan_id` and
`registry_digest` are non-empty. Calling [`tools/fork-check.py`](../../tools/fork-check.py)
directly without `tillr-gate` produces no `gate.verdict` record, so `tillr-land pass` cannot
land anything. Conversely, calling `tillr-gate run` without `--gate` invokes
[`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go) (`Run`), which refuses whenever
`len(plan.InProcess) > 0` — and in the real registry exported by
`python3 tools/fork-check.py --registry-json`, `scratch` has `selects: []` and is therefore
owed on **every** candidate.

[`tools/tillr-gate/gate.go`](../../tools/tillr-gate/gate.go) (`RunGate`) was built for this exact
seam:

1. `tillr-gate plan` reads `gate.change.json` and the registry (`python3 tools/fork-check.py --registry-json`),
   verifies the registry's SHA-256 digest, computes the planned checks and `in_process` list,
   and writes `gate.plan.json` (exit `0` even when `in_process` is non-empty).
2. `tillr-gate run --gate <program> [--gate-arg ...]` reads `gate.plan.json`, verifies that
   `<worktree>`'s `HEAD^{tree}` still equals `plan.TreeSHA` (`verifyWorktreeTree`), runs
   `<program>` with `cmd.Dir = <worktree>`, writes a bounded triage log under `--logs`, and
   emits a `gate.verdict.json` bound to `plan.PlanID`, `plan.RegistryDigest`, `plan.BaseSHA`,
   and `plan.TreeSHA`.
3. Meanwhile, `tillr-gate`'s native command runner ([`tools/tillr-gate/run.go`](../../tools/tillr-gate/run.go))
   remains the subject of [`tools/tillr-gate/dualrun.go`](../../tools/tillr-gate/dualrun.go)
   until the three in-process checks are extracted into standalone scripts or ported to Go in a
   later wave.

### Exact argv `tillr-loop gate` must pass

Inside [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop) (`run_gate`), after
`"$gate_bin" plan` writes `$plan_file`, the adapter must invoke `"$gate_bin" run` with:

```sh
PI_FORKS_ROOT="$repo_root" \
"$gate_bin" run \
  --input "$plan_file" \
  --worktree "$worktree" \
  --output "$verdict_file" \
  --logs "$logs_dir" \
  --gate python3 \
  --gate-arg tools/fork-check.py \
  --gate-arg --since \
  --gate-arg "$base_sha" \
  --gate-arg --no-wait
```

Every token in that command is load-bearing:

- **`tools/fork-check.py` must be a relative path, never `"$repo_root/tools/fork-check.py"`.**
  [`tools/fork-check.py`](../../tools/fork-check.py) (`main` and `run_gate`) computes
  `root = Path(__file__).resolve().parents[1]`. Because `RunGate` in
  [`tools/tillr-gate/gate.go`](../../tools/tillr-gate/gate.go) sets `cmd.Dir = cfg.Worktree`,
  passing the relative path `tools/fork-check.py` resolves `Path(__file__)` inside `<worktree>`
  and judges the candidate worktree. Passing `"$repo_root/tools/fork-check.py"` would resolve
  `parents[1]` to `/workspace` and silently judge `main` instead of the candidate.
- **`--since "$base_sha"` scopes the Python gate to the candidate's exact range.** Without
  `--since`, `plan_gate` in [`tools/fork-check.py`](../../tools/fork-check.py) selects a
  baseline from `yolo-fork-check/state.json`, runs eight remote `git ls-remote` / `target --fetch`
  calls (`audit_upstreams=True`), and advances the branch baseline for `tillr/<lane>` in the
  shared `.git/yolo-fork-check/state.json` before `tillr-land` has landed the commit. With
  `--since "$base_sha"`, `fork-check.py` diffs `<base_sha>..HEAD` (matching `gate.change.json`),
  skips upstream drift probes, and still runs all integrity checks plus every check selected by
  the candidate's changed paths — including all three in-process checks (`file-size`, `scratch`,
  `design-citations`). This is the exact argv [`tools/tillr-gate/dualrun.go`](../../tools/tillr-gate/dualrun.go)
  (`observePython`) already uses.
- **`--no-wait` prevents a 30-minute lock stall.** By default `fork-check.py` waits `1800s` for
  `yolo-fork-check/state.lock`. Under serial `Verify` inside a single `tillr-drain loop`, no
  other lane holds the lock; if an external process does hold it, failing fast with exit `2`
  (`gate-error`) is safer than stalling the loop for half an hour.
- **`PI_FORKS_ROOT="$repo_root"` supplies the shared `.forks/` checkouts.** Any gate step
  selected by a pack edit (such as `matt-render`) looks for `.forks/pi-subagents` under
  `PI_FORKS_ROOT` while reading pack manifests from `<worktree>`.

### How the loop knows the verdict

The exit-code and JSON translation from [`tools/fork-check.py`](../../tools/fork-check.py)
through [`tools/tillr-gate/gate.go`](../../tools/tillr-gate/gate.go),
[`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop),
[`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go), and
[`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) is already aligned:

| `fork-check.py` outcome | `fork-check.py` exit | `tillr-gate run --gate` writes to `gate.verdict.json` & exits | `tillr-loop gate` stdout & exit | `loop.go` (`verifyCandidate`) action |
| :--- | :--- | :--- | :--- | :--- |
| **Verified** (all planned + in-process checks pass) | `0` | `result: "pass"`, exit `0` | `{"passed":true,"summary":"gate verdict pass (...)"}`, exit `0` | Calls `Steps.Land` (`tillr-land enqueue` + `tillr-land pass`, moving `main`) and `Steps.Verdict(..., "pass")` (`item.verdict` `done`, `actor_kind: "mechanical"`). Resets `ConsecutiveInfra = 0`. |
| **Check failed** (a test, build, or in-process guard failed) | `1` | `result: "fail"`, exit `1` | `{"passed":false,"summary":"gate verdict fail (...): --gate exited 1"}`, exit `0` | Classifies as `GateFailed` (`AttemptCandidateFailed`), resets `ConsecutiveInfra = 0`, calls `Steps.Verdict(..., "fail")` (`not-done`), and holds the item on subsequent ticks via `mayBeARetry` (`run-link-missing`). |
| **Refused / not a verdict** (dirty watched path, lock held, missing tool, or timeout) | `2` (or killed) | `result: "error"`, exit `4` | stderr error message, exit `4` | `CommandSteps.Gate` returns an error; `loop.go` classifies as `AttemptInfrastructure` (`gate-error`), increments `ConsecutiveInfra`, and triggers backend failover (stopping at 3). |

---

## 3. The canary item versus the seed items

<a id="d-lr3"></a>
> [!IMPORTANT]
> **`[D-LR3]` The first overnight burn must exclude fork work (`item-seed-01`, `item-seed-03`, `item-seed-05`) and run only `main`-tree items (`item-seed-02`, `item-seed-04`).**
> A tick that touches `patch/pi` cannot succeed under the current lane-worktree and
> host-boundary model: its target files do not exist in a `main` worktree, `.forks/` is
> gitignored and absent from the lane, and running [`bin/pi-forks`](../../bin/pi-forks) `export`
> moves `refs/heads/patch/pi` in the host repository, which
> [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go) flags as a
> fatal `host-boundary-violation`.

### Audit of the five seeded items in `config/tillr/seed-items.json`

| Item ID | Archived board item | Target paths ([`tillr-seed-triage.md`](tillr-seed-triage.md) — [The list](tillr-seed-triage.md#the-list)) | Present in `<lane-dir>/worktree` (`main`)? | Tonight's disposition |
| :--- | :--- | :--- | :--- | :--- |
| `item-seed-01` | 148 — move `pi` patch series' behavioral tail into settings/extensions | `yolo-packs/pi-fork/patches/`, `yolo-packs/matt/pack.json`, `yolo-packs/matt/files/extensions/`, `patch/pi` | **No** (`patch/pi` sources live in `.forks/pi`) | **Hold (`state: "blocked"`)** — requires `bin/pi-forks export --fork pi`, which moves `refs/heads/patch/pi` and trips `host-boundary-violation`. |
| `item-seed-02` | 613 — yolo profile names a default seat set (`seat-profiles`) | `config/yolo-jail/`, `tag-personal/config/yolo-jail/`, `tag-work/config/yolo-jail/`, `yolo-packs/seat-profiles/`, `tests/seat-profiles.test.mjs`, `docs/design/seat-profiles.md` | **Yes** (all ordinary files on `main`) | **Run tonight (`state: "in-queue"`)** after adding `allowed_paths`. |
| `item-seed-03` | 617 — `visibleWidth` bounded memo + benchmark | `packages/tui/src/utils.ts`, `packages/tui/test/` (on `patch/pi`) | **No** (files exist only inside `.forks/pi`, not on `main`) | **Hold (`state: "blocked"`)** — target paths do not exist in a `main` worktree. |
| `item-seed-04` | 618 — one supported profiler command; retire or delegate `pi-profile-live.mjs` | `tools/pi-profiler/`, `tools/pi-profile-live.mjs`, `docs/pi/` | **Yes** (all ordinary files on `main`) | **Run tonight (`state: "in-queue"`)** after adding `allowed_paths`. |
| `item-seed-05` | 623 — abort and name a repeating thinking stream | `packages/ai/src/stream*`, `packages/coding-agent/src/**/thinking*` (on `patch/pi`) | **No** (files exist only inside `.forks/pi`, not on `main`) | **Hold (`state: "blocked"`)** — target paths do not exist in a `main` worktree. |

### Why a tick touching `patch/pi` breaks in four places

1. **Target files are absent from the lane worktree.** [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go)
   (`prepareWorktree`) materializes `<lane-dir>/worktree` from `main` (`git worktree add --force -B tillr/<lane> <worktree> <base_sha>`).
   `packages/tui/src/utils.ts` (`item-seed-03`) and `packages/ai/src/stream*` (`item-seed-05`) do
   not exist on `main`; on `main`, the Pi fork exists only as `git format-patch` files under
   `yolo-packs/pi-fork/patches/`.
2. **`.forks/` is gitignored and absent from `<lane-dir>/worktree`.** As recorded in
   [`.gitignore`](../../.gitignore) (`/.forks/`) and [`AGENTS.md`](../../AGENTS.md), `.forks/`
   lives only at the root of `/workspace` and is never present inside a linked worktree.
   Furthermore, [`bin/pi-forks`](../../bin/pi-forks) refuses linked-worktree execution unless
   `PI_FORKS_ROOT=/workspace` is set.
3. **`bin/pi-forks export` trips `host-boundary-violation` and aborts the loop.** Even if a
   model knew to run `PI_FORKS_ROOT=/workspace bin/pi-forks setup --lane lane-1 --fork pi` and
   `bin/pi-forks export --lane lane-1 --fork pi`, [`AGENTS.md`](../../AGENTS.md) notes that
   `export --lane` **moves `refs/heads/patch/pi` in this repository** to the lane tip and
   fast-forwards `/workspace/.forks/pi`'s `main`. When the run finishes,
   [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go)
   (`compareRefs`) diffs every ref under `refs/heads/` against its pre-batch snapshot, excuses
   only `refs/heads/tillr/<lane>`, sees `ref refs/heads/patch/pi moved: <old> -> <new>`, and
   halts the entire overnight loop with `StopBoundaryViolation`.
4. **Hand-editing `yolo-packs/pi-fork/patches/*.patch` fails `fork-check.py` step 3 (INV-3).**
   If the model instead edits a `.patch` file directly inside `<lane-dir>/worktree` without
   moving `refs/heads/patch/pi`, [`tools/fork-check.py`](../../tools/fork-check.py) step 3 runs
   `bin/pi-forks patch-branch verify --fork pi`, which fails because the patch series in the
   worktree is no longer the exact export of `refs/heads/patch/pi`.

Supporting fork-series items in `tillr` will require a dedicated fork-lane mode (excusing
`refs/heads/patch/<fork>` in [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go),
teaching `tillr-land` to advance or roll back `refs/heads/patch/<fork>` alongside `main`, and
staging `.forks/lanes/<lane>/<fork>` in `stage.go`). That is a separate design slice, not a
pre-burn fix.

---

## 4. Boundaries of a night

<a id="d-lr4"></a>
> [!IMPORTANT]
> **`[D-LR4]` Run tonight's burn with `--lanes 1 --max-ticks 6` and a 25-minute per-run timeout in `CommandSteps.Dispatch`; do not run `--lanes 2` and do not build a spend daemon.**
> `--lanes 2`, serial `gate+land`, and the 3-infrastructure-failure circuit breaker are **not**
> sufficient on their own for an 8-hour unattended window: the planner does not check
> `allowed_paths` disjointness when pairing two lanes, the circuit breaker resets to `0` on
> every candidate failure, and `CommandSteps.Dispatch` has no subprocess timeout.

### Why `--lanes 1` instead of `--lanes 2` tonight

[`tillr-day2.md`](tillr-day2.md) [§4.3](tillr-day2.md#43-complete-stopping-conditions-for-tillr-drain-loop)
and [§5.2](tillr-day2.md#52-concurrency-rules-for-both-phases-of-day-2)
(`D-DAY2-6`) permitted `--lanes 2` on the premise that the planner pairs only items with
**disjoint `allowed_paths`**. In the built code:

1. **Path-disjoint pairing is not implemented.** [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go)
   (`readItems`) runs `SELECT item_id, state FROM items ORDER BY item_id` and never reads
   `allowed_paths`. With `--lanes 2`, `Plan` dispatches the first two runnable items into
   `lane-1` and `lane-2` even if their `allowed_paths` overlap.
2. **A 2-lane batch lands the second lane via an un-gated carry.** In
   [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (`runBatch`),
   both lanes stage at `base_sha = M0`, run in parallel, and then verify serially. When `lane-1`
   passes the gate and lands (`main` moves `M0 -> M1`), `lane-2` is gated against `M0..head2`
   and then handed to `tillr-land pass`. Because `trunkHead (M1) != c.BaseSHA (M0)`,
   `tools/tillr-land/land/faces.go` (`AttemptCandidate`; the file was removed in commit `e07510bba`)
   re-applies `M0..head2` onto `M1` (`Carry: true`) and updates `main` to `M2` **without
   re-running the gate on the combined tree `M2`**.
3. **One lane's boundary violation kills the sibling's valid work.** `checkBoundary` in
   [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) checks both lanes
   as one batch before `verifyCandidate` runs for either; if `lane-2` writes a stray file outside
   its worktree, `lane-1`'s clean candidate is discarded with the batch.

With two runnable non-fork items tonight (`item-seed-02` and `item-seed-04`), `--lanes 1` serializes
every tick cleanly at the current `main` tip (`Carry: false`) and finishes in under an hour.

### Why `--max-ticks` and a per-run timeout were proposed as walls for that burn

> **Superseded, 2026-11-12.** The tick budget is gone by ruling ([the momentum
> principle](momentum-principle.md)): the loop has no `--max-ticks`, no spend or token
> ceiling, and a repeated infrastructure fault is a backoff rather than a stop. The rows
> below are kept as the record of what this readiness pass proposed for that night's burn,
> not as the loop's contract — only the per-run timeout half survives, as `timeout_ms` on a
> run. Run the loop without a tick budget.

| Wall | Status in code today | Why it is mandatory before an 8-hour unattended run | Setting for tonight |
| :--- | :--- | :--- | :--- |
| **`--max-ticks N`** | **Built** in [`tools/tillr-drain/main.go`](../../tools/tillr-drain/main.go) (`--max-ticks`, `StopMaxTicksReached` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go)) | `ConsecutiveInfra` resets to `0` on **every** `AttemptCandidateFailed` (`candidate-no-op`, `GateFailed`, `LandRefused`) and `AttemptUnjudged` ([`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go)). Only [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go) (`mayBeARetry`) prevents an infinite candidate-failure loop, by holding an item once a terminal run row exists. If any code path left a run non-terminal, `--max-ticks 0` would re-dispatch all night while `ConsecutiveInfra` stayed `0`. `--max-ticks 6` caps worst-case spend at 6 model runs (`ESTIMATE: <$8` total across DeepSeek/Sonnet). | `--max-ticks 6` (2 runnable items × at most 2 backends + 2 spare ticks) |
| **`--run-timeout 25m`** | **Missing** — [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go) (`Dispatch`) and [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go) (`RunOnce`) run the backend with no deadline | A `muse` or `pi` subprocess that hangs on an unclosed stream or stalled HTTP read blocks `Dispatch` forever: the 30-minute lease in SQLite expires, no failover fires, and the loop sits frozen until morning. Wrapping `exec.CommandContext` in `CommandSteps.Dispatch` with a 25-minute `context.WithTimeout` (default `25 * time.Minute`, inside the 30-minute lease TTL) turns a hung backend into an `AttemptInfrastructure` failure and lets failover and the 3-failure breaker work. | `25m` default in [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go) (`ESTIMATE: 10 LOC`) |

---

## 5. The stop that saves the owner

<a id="d-lr5"></a>
> [!IMPORTANT]
> **`[D-LR5]` The worst overnight failure mode is landing an out-of-fence or dirty-worktree candidate onto `main`; the single check that catches it is the pre-gate Fence-and-Clean-Tree check ([B3](#b3-post-run-fence-enforcement)).**
> A broken judge does not burn a night's quota (it halts in at most 3 ticks via
> `StopInfraCircuitBreaker` or holds every item via `mayBeARetry`), and a lane never holds the
> landing lock during model execution or gating. Only a bad landing mutates `main` and poisons
> every subsequent tick's `base_sha`.

### Why the other two candidate failure modes are already bounded

1. **"Spending a night's quota on a broken judge" is already bounded to `<= 3` ticks (on `gate-error`) or `<= N` ticks (on `GateFailed`).**
   - When the gate fails to answer (exit `3` or `4`, as in the canary), `CommandSteps.Gate`
     returns an error, `loop.go` increments `ConsecutiveInfra`, and the loop halts at
     `StopInfraCircuitBreaker` (`3 consecutive infrastructure failures`) after 3 ticks.
   - When the gate answers `passed: false` (exit `0`), `loop.go` records a terminal `failed`
     run and `item.verdict` `not-done`. On the next tick, [`tools/tillr-drain/drain/plan.go`](../../tools/tillr-drain/drain/plan.go)
     (`mayBeARetry`) sees the prior terminal run in `runs`, holds the item with
     `ReasonRunLinkMissing`, and halts the loop with `StopAllHeld` once each runnable item has
     been tried once.
2. **"A lane holding the trunk" cannot happen during dispatch or gating.**
   - Model execution happens in `<lane-dir>/worktree` on `tillr/<lane>`; it never touches `main`
     or the landing lock.
   - `tillr-land pass` acquires the landing lock in [`tools/tillr-land/land/lock.go`](../../tools/tillr-land/land/lock.go) (it was in `faces.go` until commit `e07510bba` removed that file)
     only after `tillr-gate` has finished, and holds it only across a single SQLite write
     transaction and `git update-ref` compare-and-swap (milliseconds).

### The failure mode that ruins a night, and the one check that stops it

If a model editing `tools/pi-profiler/` (`item-seed-04`) also modifies [`tools/fork-check.py`](../../tools/fork-check.py),
weakens a shared test, or edits a file outside its item's `allowed_paths` — and the gate passes —
`tillr-land pass` fast-forwards `refs/heads/main` to that commit. Every subsequent tick stages
its worktree from the new `main` tip, compounding the unapproved change. Equally dangerous, if a
model leaves untracked or unstaged files in `<lane-dir>/worktree` that make a test pass on disk
while omitting them from the committed `HEAD^{tree}`, a check that does not watch those paths in
`fork-check.py` can pass against a working tree that does not match `HEAD^{tree}`.

**The single check that catches both ([B3](#b3-post-run-fence-enforcement)):** at the top of
`verifyCandidate` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go),
before `Steps.Gate` is called:

1. Verify `git -C <worktree> status --porcelain` is empty; if not, fail the candidate (`GateFailed`
   with summary `candidate worktree has uncommitted or untracked files`).
2. Read `<lane-dir>/fence.txt` and verify that every path returned by
   `git -C <worktree> diff --name-only <base_sha>..<head_sha>` matches an allowed path prefix or
   exact path in `fence.txt` (where an entry ending in `/` matches that directory subtree and
   any other entry matches the exact path); if any path is outside `fence.txt`, fail the
   candidate (`GateFailed` with summary `boundary-scope-escape: <path> is outside allowed_paths`)
   and record `Steps.Verdict(..., "fail")` without calling `Steps.Gate` or `Steps.Land`.

---

## 6. What we would not fix tonight

<a id="d-lr6"></a>
> [!IMPORTANT]
> **`[D-LR6]` Leave the seven gaps below broken tonight.**
> None of them can cause a bad landing, an unbounded spend loop, or a host-boundary corruption
> on a 1-lane burn of `item-seed-02` and `item-seed-04`. Fixing any of them tonight trades high-risk
> churn in working packages for zero overnight safety gain.

| Gap named by the lanes or canary | Why we deliberately leave it broken tonight | When to fix it |
| :--- | :--- | :--- |
| **1. `<lane-dir>` outside `<lane-dir>/worktree` is not boundary-watched** | [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go) (`TakeBoundary`) watches `/workspace` and git refs while excusing `<lane-dir>/worktree`. A model writing `../foo` into `<lane-dir>/` writes only into its own scratch directory, never into `/workspace` and never into the committed tree `tillr-land` lands on `main`. Watching `<lane-dir>` would require snapshotting after `Stage` and maintaining an excuse list for `run.log`, `report.md`, and backend temp files. | Only if a multi-tenant lane directory shares state with another tool. |
| **2. Nothing extracts `<lane-dir>/report.md`; classification reads `run.log`'s footer** | [`tools/tillr-run/backends/muse`](../../tools/tillr-run/backends/muse), `pi`, and `claude` already append `status: answered \| blocked \| failed \| malformed` to `run.log`, and `classifyAttempt` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) needs only that footer plus `git rev-parse HEAD`. Nothing in `tillr-gate`, `tillr-land`, or `tillr-item verdict` reads `report.md`. Adding an NDJSON-to-Markdown extractor tonight introduces a parser that can misfire on formatting trivia for an artifact no consumer reads. | When the morning digest or repair loop needs structured prose from failed runs. |
| **3. Four brief sections (`#2 Settled decisions`, `#4 Facts`, `#6 Method`, `#7 Verification`) are placeholders, and `body_ref` is referenced rather than inlined** | In `config/tillr/seed-items.json`, each item's `title`, `criteria` text (which already names the exact files, commands, and assertions), and `allowed_paths` (`#5 The fence`) carry the full task contract; `body_ref` (`docs/design/tillr-seed-triage.md#...`) is a file inside `<lane-dir>/worktree` that the model can read in one tool call, just as `deepseek-v3.2` did on the canary run. Expanding the SQLite `items` schema and seed format with four prose columns before draining 2 items is pure scope creep. | When authoring net-new items directly in `tillr-item` without a triage doc in the tree. |
| **4. `session-id` is not minted at staging** | Every run already has a unique `run.id` (`<item>-tick-<n>-lane-<k>`) across the SQLite store, `<lane-dir>`, `run.log`, and `.tillr/loop/<run-id>/`. Headless `muse -p`, `pi -p`, and `claude -p` runs are one-shot processes; retries are held until a `run.link` is recorded rather than resumed in-place. | If `tillr-run` adds multi-turn session resumption across ticks. |
| **5. All `Stage` errors classify as `AttemptInfrastructure` (including item defects)** | Once [B2](#b2-seed-items-fence-and-fork-filter) populates `allowed_paths` in `config/tillr/seed-items.json` and `tillr-item seed` validates the seed payload up front, no item-schema defect reaches `Stage` at runtime. Any remaining `Stage` failure is a git or filesystem error where `AttemptInfrastructure` + the 3-failure breaker is the right response. | When items can be mutated live in the store while a drain loop is running. |
| **6. Intra-backend route advance on a route-level signature ([`tillr-muse-backend.md`](tillr-muse-backend.md) [§2](tillr-muse-backend.md#2-the-staged-path)) is unbuilt** | [`tools/tillr-run/backends/muse`](../../tools/tillr-run/backends/muse) delegates OpenRouter provider routing to `muse -p`, and [`tools/tillr-drain/drain/failover.go`](../../tools/tillr-drain/drain/failover.go) already fails over to a distinct second backend (`deepseek-v3.2 -> claude-sonnet-4.6`, `pi -> agy`) on the first infrastructure failure. | Only if `tillr-run` calls OpenRouter HTTP endpoints directly with provider-pinning headers. |
| **7. Porting `file-size`, `scratch`, and `design-citations` into `tillr-gate`'s Go runner** | Delegating to [`tools/fork-check.py`](../../tools/fork-check.py) via `tillr-gate run --gate` ([D-LR2](#d-lr2)) executes all 3 in-process checks and all 65 command declarations today and emits a valid `gate.verdict` record for `tillr-land pass`. Porting those checks into Go tonight duplicates working Python logic before the first real candidate has ever landed through `tillr-land`. | Wave-next gate-parity work when retiring `tools/fork-check.py`. |

---

## 7. Pre-burn checklist and launch sequence

The coordinator should execute these steps in order before leaving the loop unattended:

1. **Implement [B1](#b1-gate-delegation)–[B4](#b4-sterile-worktree-reset) and the 25-minute dispatch timeout ([D-LR4](#d-lr4)):**
   - [`tools/tillr-loop/tillr-loop`](../../tools/tillr-loop/tillr-loop) (`run_gate`): auto-generate
     `<run-work-dir>/registry.json` via `python3 "$repo_root/tools/fork-check.py" --registry-json`
     when `--registry` / `TILLR_LOOP_REGISTRY` is unset, and default `--gate-program` to pass
     `--gate python3 --gate-arg tools/fork-check.py --gate-arg --since --gate-arg "$base_sha" --gate-arg --no-wait`
     with `PI_FORKS_ROOT="$repo_root"` to `"$gate_bin" run`.
   - `config/tillr/seed-items.json`: add `allowed_paths`
     to `item-seed-01` through `item-seed-05` per [`tillr-seed-triage.md`](tillr-seed-triage.md)
     ([The list](tillr-seed-triage.md#the-list)), and set `state: "blocked"` on
     `item-seed-01`, `item-seed-03`, and `item-seed-05`.
   - [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) (`verifyCandidate`):
     verify `git -C <worktree> status --porcelain` is empty and every changed path in
     `<base_sha>..<head_sha>` matches `<lane-dir>/fence.txt` before calling `Steps.Gate`.
   - [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) (`prepareWorktree`):
     force-remove any existing `<worktree>` and run `git worktree prune` before `git worktree add`.
   - [`tools/tillr-drain/drain/steps.go`](../../tools/tillr-drain/drain/steps.go) (`Dispatch`):
     wrap the `tillr-run` call in a 25-minute `context.WithTimeout`.
2. **Run the unit suites for the touched Go modules** (`CGO_ENABLED=0 go test ./...` in
   `tools/tillr-drain` and `tools/tillr-loop`).
3. **Re-run the 1-item canary (`item-canary-01`) in a throwaway clone of `/workspace`** through
   the full five-step pipeline (`Stage -> Dispatch -> Boundary -> Gate -> Land -> Verdict`), and
   verify that `refs/heads/main` advances by one commit and `item-canary-01` transitions to
   `done` (`actor_kind: "mechanical"`).
4. **Seed a fresh store from `config/tillr/seed-items.json`
   and verify `tillr-drain plan`:** confirm `item-seed-02` and `item-seed-04` plan as `dispatch`
   and `item-seed-01`, `item-seed-03`, and `item-seed-05` plan as `hold` (`item-not-runnable`).
5. **Launch the unattended burn with `--lanes 1 --max-ticks 6`** against the seeded store.
