---
title: "Tell a running worker when the trunk goes green, and let it rebase before landing"
status: in-review
stage: DESIGN
tags:
  - tillr
  - drain
  - run
  - landing
  - rebase
  - design
summary: "Specifies how a worker that started on an unproved or red trunk tip learns when refs/tillr/green advances past a broken base or when a named red check is repaired on the tip, how the notice reaches pi, Claude Code, muse and agy without scaling churn with trunk throughput, how a bounded opt-in rebase updates base_sha and NodeBaseSHA before the landing gate runs, and how conflicts route cleanly."
next: "The owner rules on OQ-WR1 through OQ-WR3; build slices wait on those rulings."
depends-on:
  - landing-principles.md
  - tillr-landing-call.md
  - tillr-graph-is-the-path.md
  - tillr-lane-shims.md
  - ../reference/gate.md
---

# Tell a running worker when the trunk goes green, and let it rebase before landing

**Status:** Nothing in this design is built: `rg -n 'trunk-green\.json|lane-rebase|rebased-base\.json' tools/` returns nothing. In the current tree, [`config/tillr/gate-policy.json`](../../config/tillr/gate-policy.json) sets `lane_base: "tip"` under [`D-GR55`](../reference/gate.md#D-GR55) in [`docs/reference/gate.md`](../reference/gate.md) (while `ReadGatePolicy` in [`tools/tillr-drain/redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go) keeps `"green"` as the absent-setting default from [`D-GR24`](../reference/gate.md#D-GR24)); staging warns a new run before launch when its base is unproved (`unprovedBaseNotice` in [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go)) or has an open fixer (`openTrunkRedNotice` in [`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go)); and `preRebaseRepairedTrunk` in [`tools/tillr-drain/drain/trunk_repair.go`](../../tools/tillr-drain/drain/trunk_repair.go) rebases a finished candidate onto a repaired trunk only *after* the worker has exited. Every mechanism, file format, tool contract, and rejected alternative here ([`D-WR1`](#D-WR1) through [`D-WR8`](#D-WR8)) is the designer's proposal from a filed request that the owner may overturn; the policy choices only the owner can settle are open in [OQ-WR1](#OQ-WR1), [OQ-WR2](#OQ-WR2), and [OQ-WR3](#OQ-WR3).

> **In short.** When lanes start from the trunk tip (`lane_base: "tip"`), a worker can begin on a commit that is not yet heavy-proved or that carries a broken check an automatic revert or `fixer-main-*` item clears while the worker is still running. Writing a monotonic `<lane>/trunk-green.json` notice on bounded green or check-repair transitions, surfacing it at turn gaps on `pi` and `claude` (and between workflow nodes everywhere), and offering a bounded rebase helper that advances the recorded `base_sha` lets a running worker move off a broken base before `land` without making worker churn rise with ordinary trunk velocity.

**Why it matters.** Without a mid-run signal, a worker started on a red or unproved tip spends turns debugging pre-existing failures in its own targeted test runs or exits early with `OBSTACLE: trunk` even after the trunk has already been repaired. Conversely, if a worker runs raw `git rebase main` on its own today without advancing the lane's recorded `base_sha`, [`tools/tillr-gate/change-gate.py`](../../tools/tillr-gate/change-gate.py) and `Trunk.Squash` in [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) treat every intervening sibling commit in `old_base..new_base` as part of this candidate's diff — and can re-introduce a commit that `main` later reverted ([Section 4.1](#41-why-raw-git-rebase-by-a-worker-is-unsafe-today)).

**The shape.** Four pieces:

- a monotonic status file, `<lane>/trunk-green.json`, written atomically by `tillr-drain` outside `<lane>/worktree` only when a lane's current base was unproved or red and the trunk clears that condition ([Section 2](#2-which-trunk-events-notify-a-running-worker));
- adapter-native delivery: turn-gap steering via a `pi` extension, tool-gap `additionalContext` via a Claude Code hook, passive file + `obstacle` interception everywhere, and updated `Base sha:` headers between workflow nodes on `muse`, `agy`, `pi`, and `claude` ([Section 3](#3-how-the-signal-reaches-a-running-worker-across-adapters));
- a bounded rebase helper (`tillr-run lane-rebase`, exposed as a `rebase_trunk` tool on `pi` and `claude`) paired with automatic loop rebase between sequential nodes and before the gate, recording an explicit `rebased-base.json` receipt in the run's durable directory so `BaseSHAFile`, `ItemBaseSHAFile`, `NodeBaseSHA`, `change-gate.py`, and `Trunk.Squash` all advance together ([Section 4](#4-who-rebases-worker-in-run-loop-between-nodes-or-both), [Section 5](#5-what-base_sha-nodebasesha-and-gateverdict-become-after-a-rebase));
- clean abort-and-restore conflict handling both inside a running turn and across workflow nodes ([Section 6](#6-what-happens-when-the-rebase-conflicts)).

**Cost.** Zero model tokens and zero git operations when a worker starts on an already-proved base or when no repair occurs while it runs; one short turn-gap notice at most once per monotonic repair target on `pi` and `claude`; no process restarts on any adapter.

**Start at [Section 2](#2-which-trunk-events-notify-a-running-worker)** for the event boundary, [Section 3](#3-how-the-signal-reaches-a-running-worker-across-adapters) for the four adapters, and [Section 4](#4-who-rebases-worker-in-run-loop-between-nodes-or-both) and [Section 5](#5-what-base_sha-nodebasesha-and-gateverdict-become-after-a-rebase) for the rebase and `base_sha` mechanics.

**Needs your ruling:** [OQ-WR1](#OQ-WR1) (which trunk events notify and offer a rebase), [OQ-WR2](#OQ-WR2) (opt-in worker rebase tool plus loop fallback vs. loop-only rebase), and [OQ-WR3](#OQ-WR3) (linear rebase advancing `base_sha` vs. merge take-in).

**Reads with:** [the landing principles](./landing-principles.md) (the rulings every landing and queue mechanism is tested against), [suspended landing calls](./tillr-landing-call.md) (`land`, `obstacle`, and `PreToolUse` / extension mechanics), [the graph design](./tillr-graph-is-the-path.md) ([Section 3.7](./tillr-graph-is-the-path.md#37-the-item-branch-and-the-latest-trunk) on `D-GP8` and `D-GP18`, and [Section 3.8](./tillr-graph-is-the-path.md#38-concurrent-writers-checkouts-and-the-join) on `D-GP16`), [lane shims](./tillr-lane-shims.md) (why `.tillr/` outside the run's durable directory is denied to workers), and [the gate reference](../reference/gate.md) (`refs/tillr/green`, [`D-GR24`](../reference/gate.md#D-GR24), [`D-GR55`](../reference/gate.md#D-GR55), and [`D-FG1`](../reference/gate.md#D-FG1)).

---

## 1. The owner's prompt and what the tree does today

### 1.1 The prompt and decision provenance

On 2026-11-25, when ruling that lanes start from the trunk tip, the owner asked:

> "maybe we can also notify when/if things go green and give the worker a chance to rebase while working before landing?"

Two provenance boundaries govern everything below:

- **What the owner stated:** starting lanes from the trunk tip, paired with the open question quoted verbatim above asking whether tillr can notify a running worker when things go green and give it a chance to rebase while working before landing.
- **What is the designer's proposal:** that quoted sentence is a prompt for design, not a settled ruling on mechanism. Every trigger rule, file path, adapter hook, rebase helper contract, and rejected alternative in this document ([`D-WR1`](#D-WR1) through [`D-WR8`](#D-WR8)) comes from the designer working a filed request and may be overturned by the owner. The policy choices that trade latency against proof certainty, worker agency against single-writer simplicity, and linear rebase against merge take-in are left open for the owner in [OQ-WR1](#OQ-WR1), [OQ-WR2](#OQ-WR2), and [OQ-WR3](#OQ-WR3).

### 1.2 Baseline in the repository today

In the tree as it stands:

1. **Lane start policy (`lane_base`).** [`config/tillr/gate-policy.json`](../../config/tillr/gate-policy.json) sets `lane_base: "tip"` under [`D-GR55`](../reference/gate.md#D-GR55) (while `ReadGatePolicy` in [`tools/tillr-drain/redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go) defaults an unset `lane_base` to `"green"` under [`D-GR24`](../reference/gate.md#D-GR24)), so `provedLaneBaseAt` in [`tools/tillr-drain/drain/lane_base.go`](../../tools/tillr-drain/drain/lane_base.go) cuts new lanes here from `refs/heads/main`. An ordinary worker routinely starts ahead of `refs/tillr/green` — and occasionally starts on a trunk commit whose after-merge proof is about to fail or already has an open `fixer-main-*` item.
2. **Staging-only unproved and red notices.** `unprovedBaseNotice` in [`tools/tillr-drain/drain/stage.go`](../../tools/tillr-drain/drain/stage.go) warns in the brief *before* launch when a non-fixer lane starts ahead of `refs/tillr/green`, and `openTrunkRedNotice` in [`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go) appends a one-time notice *before* launch if an open `fixer-main-*` already exists at staging time. If the proof turns red two minutes *after* the worker starts, or if the fixer lands or `refs/tillr/green` catches up three minutes *into* the worker's run, the running worker hears nothing.
3. **Post-exit rebase only.** `preRebaseRepairedTrunk` in [`tools/tillr-drain/drain/trunk_repair.go`](../../tools/tillr-drain/drain/trunk_repair.go) checks `RecordedTrunkRepair` in [`tools/tillr-drain/fixer/trunk_repair.go`](../../tools/tillr-drain/fixer/trunk_repair.go) only *after* the worker has finished its turn and offered a candidate. Similarly, `moveLaneOntoTip` in [`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go) rebases a lane only *after* a worker has already stopped on `OBSTACLE: trunk` (`obstacle_route.go`) or hit a conflict (`conflict.go`).

During the window between launch and `land`, a worker standing on a broken `base_sha` hits the trunk's failure whenever its targeted package tests overlap the broken package, burns turns investigating a bug it did not write, or stops on `OBSTACLE: trunk` and pays a full drain round-trip even when `main` is already fixed.

---

## 2. Which trunk events notify a running worker

### 2.1 Why ordinary trunk movement must never notify a worker

By [Landing Principle 2](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch) and [Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once), any mechanism whose cost rises with sibling landing velocity is pointed the wrong way. At ten or twenty concurrent lanes ([`D-GR2`](../reference/gate.md#D-GR2)), `refs/heads/main` and `refs/tillr/green` advance continually as unrelated items land and prove.

If every trunk landing or every green-pointer advance nudged every running worker to rebase:

- every active session would pay extra prompt tokens and tool calls proportional to sibling throughput;
- rebasing a worktree mid-run touches file modification times and commit ancestry across unrelated packages, invalidating local test caches and recompiling packages the worker never touched;
- a worker whose `base_sha` was **already proved green** (`containsCommit` in [`tools/tillr-drain/drain/lane_base.go`](../../tools/tillr-drain/drain/lane_base.go) reports `base_sha` reachable from `refs/tillr/green`, with no red check on `base_sha`) gains nothing before `Trunk.Squash` three-way merges `base_sha..head_sha` onto the tip at landing time.

Therefore, under the designer's proposal ([`D-WR1`](#D-WR1), subject to [OQ-WR1](#OQ-WR1)), a lane whose current `base_sha` was already proved green and carries no red check receives **no mid-run notification and no rebase offer** when later commits land or prove.

### 2.2 The two transitions that matter on an unproved or red base

When a worker's current effective `base_sha` was **unproved** (`base_sha` not yet an ancestor of `refs/tillr/green`) or **known-red** at dispatch or during the run, two state transitions in `tillr-drain` carry actionable information ([OQ-WR1](#OQ-WR1)):

| Event kind | State transition in `tillr-drain` | Is a tree repair needed? | What the worker is told / offered |
| :--- | :--- | :--- | :--- |
| **`base-proved` (silent catch-up)** | `refs/tillr/green` advances (`heavyGreen` in [`tools/tillr-drain/drain/heavy_aliases.go`](../../tools/tillr-drain/drain/heavy_aliases.go)) to `G` such that `containsCommit(root, base_sha, G)` holds, and **no** `auto_revert` or `fixer-main-*` repair landed in `base_sha..G`. | **No.** `base_sha` itself passed the proof; `G` differs from `base_sha` only by later sibling landings. | `<lane>/trunk-green.json` records `state: "base-proved"` for observability and `obstacle` checks, **without** injecting a turn-gap rebase prompt. |
| **`green-advanced` (proved repair past base)** | `refs/tillr/green` advances to `G` strictly newer than `base_sha` (or across a rewrite/revert of `base_sha`), and `base_sha..G` includes an automatic revert, a proved `fixer-main-*` landing, or a check removal since `base_sha` (or `base_sha` had failed a check replay). | **Yes.** `base_sha` carried a broken or obsolete state that `G` has both cleared and heavy-proved. | Writes `state: "green-advanced"`, `target_sha: G`, and the cleared check IDs; injects one turn-gap notice offering a rebase onto `G`. |
| **`check-repaired-on-tip` (fast repair before heavy proof finishes)** | While `base_sha` has a failing check (from `openTrunkRedNotice`, a base replay, or an earlier red heavy proof covering `base_sha`), either `RecordedTrunkRepair` in [`tools/tillr-drain/fixer/trunk_repair.go`](../../tools/tillr-drain/fixer/trunk_repair.go) records a repaired commit `R` strictly newer than `base_sha` (from `auto_revert` or a `fixer-main-*` landing that passed `fixer-gate` under [`D-FG1`](../reference/gate.md#D-FG1)), `ReadCheckProofs` in [`tools/tillr-drain/fixer/fixer_resolve.go`](../../tools/tillr-drain/fixer/fixer_resolve.go) records a check proof past `base_sha`, or the tip removed a check present on `base_sha` ([`D-GP18`](./tillr-graph-is-the-path.md#D-GP18)). | **Yes, ahead of full proof.** `R` clears the named check(s) now, though `refs/tillr/green` has not yet reached `R`. | Under [OQ-WR1](#OQ-WR1) Option A, writes `state: "check-repaired-on-tip"`, `target_sha: R` (or `main` containing `R`), naming the repaired check IDs and noting that heavy proof is still pending; under Option C, waits until `refs/tillr/green` reaches `R`. |

Why distinguish `green-advanced` from `check-repaired-on-tip` in [OQ-WR1](#OQ-WR1)? Because heavy proof takes minutes, whereas `auto_revert` or a `fixer-main-*` landing clears the broken check on `main` immediately (`preRebaseRepairedTrunk` already trusts `RecordedTrunkRepair` before the pre-merge gate). Rebasing onto `R` unblocks a stalled worker minutes earlier, at the small risk that `R` is itself unproved on unrelated heavy checks; waiting for `refs/tillr/green` guarantees the target commit is fully proved, at the cost of holding the worker on a broken base until the proof finishes. That trade-off is a policy decision for the owner ([OQ-WR1](#OQ-WR1)).

### 2.3 Monotonic lane status file and exclusions (`D-WR2`)

*(Designer's proposal; the owner may overturn this.)*

`tillr-drain` is the sole writer of `<lane>/trunk-green.json` inside `.tillr/lanes/<lane>/` — **outside** `<lane>/worktree`, so `git status` in the worker's repository stays clean and the worker cannot spoof an unauthorized target SHA.

```json
{
  "seq": 3,
  "state": "green-advanced",
  "staged_base_sha": "<40-hex-sha>",
  "target_sha": "<40-hex-sha>",
  "green_sha": "<40-hex-sha>",
  "repaired_checks": ["go-test-tillr-drain"],
  "reason": "fixer-main-a1b2c3d4e5f6 landed and refs/tillr/green proved target_sha",
  "updated_at": "2026-11-25T19:00:00Z"
}
```

- **Atomic and monotonic.** Written whole to a temporary sibling and renamed into place whenever `tillr-drain` observes a qualifying transition on its tick. `target_sha` only moves forward along `refs/heads/main` ancestry (`trunkAncestor(root, previous_target, new_target)` or across a recorded `trunk_rewrites` replacement). If three commits prove while the worker is running one long test command, `<lane>/trunk-green.json` holds only the latest `target_sha`, and the worker sees **one** coalesced notice at the next gap.
- **Cleared on lane staging.** Removed or reset when a new item claims the lane, so stale notices never leak across items.
- **Excluded items.** Fork-series lanes (`laneForkTarget != ""`), trunk fixers (`IsTrunkFixer`, which intentionally target and repair red trunk commits and already rebase before `fixer-gate`), and remote-sync items (`IsRemoteSyncItem`) never receive `<lane>/trunk-green.json` rebase offers.

---

## 3. How the signal reaches a running worker across adapters

The four adapters under `tools/tillr-run/backends/` (`pi`, `claude`, `muse`, `agy`) differ sharply in process control (`pause-kind`), delivery (`ending-capability`), stdin lifecycle, and hook support ([`D-WR3`](#D-WR3), designer's proposal):

| Adapter | Declared `pause-kind` / `ending-capability` | Live turn-gap channel | Lane file (`<lane>/trunk-green.json`) | Next workflow node's brief |
| :--- | :--- | :--- | :--- | :--- |
| **`pi`** ([`run-pi`](../../tools/tillr-run/backends/pi/run-pi)) | `stops-at-a-gap` / `call-suspends` (when `pi --features` prints `parked-tool-calls`, else `footer`) | **Yes — in-process Pi extension hook.** Reads `<lane>/trunk-green.json` at tool-result / turn boundaries and injects one concise steering message when `target_sha` exceeds the last acknowledged SHA in the run's durable directory. Zero process restarts; background tasks and prompt cache survive untouched. | Yes; also read by `rebase_trunk` and `obstacle.ts` (`kind == "trunk"`). | Yes (`Base sha:` header + one-line base-advance note). |
| **`claude`** ([`run-claude`](../../tools/tillr-run/backends/claude/run-claude)) | `stops-where-it-stands` / `call-suspends` (probed at loop start, else `footer`) | **Yes — `PostToolUse` hook `additionalContext`.** `run-claude` already generates `claude-settings.json` and `claude-mcp.json` via [`tools/tillr-run/claude/landing.go`](../../tools/tillr-run/claude/landing.go). Adding a `PostToolUse` command entry to `tillr-run claude-landing` reads `<lane>/trunk-green.json` at any tool gap and returns `hookSpecificOutput.additionalContext` once per `target_sha`, without signaling `claude -p`. | Yes; also read by `mcp__tillr__rebase_trunk` and `PreToolUse` on `mcp__tillr__obstacle`. | Yes (`Base sha:` header + one-line base-advance note). |
| **`muse`** ([`run-muse`](../../tools/tillr-run/backends/muse/run-muse)) | `cannot-resume` / `footer` | **None mid-run (never signal).** `run-muse` closes stdin on launch and cannot resume a killed session; signaling it destroys in-progress work. | Passive: worker brief notes `../trunk-green.json` so the worker can inspect it (or run `tillr-run lane-rebase`) if a base check fails. | **Primary live boundary:** loop rebases between nodes and updates the next node's brief; `preRebaseRepairedTrunk` covers single-node runs before the gate. |
| **`agy`** ([`run-agy`](../../tools/tillr-run/backends/agy/run-agy)) | `cannot-resume` / `footer` | **None mid-run (never signal).** Single JSON envelope on stdout, non-resumable; killing `agy` discards the whole run. | Passive: readable at `../trunk-green.json` and usable via `tillr-run lane-rebase` from shell commands. | **Primary live boundary:** loop rebases between nodes and updates the next node's brief; `preRebaseRepairedTrunk` covers single-node runs before the gate. |

### 3.1 Why in-process hooks beat stop-and-resume on `pi` and `claude`

Both [`run-pi`](../../tools/tillr-run/backends/pi/run-pi) and [`run-claude`](../../tools/tillr-run/backends/claude/run-claude) pipe `brief.md` into standard input at startup and close standard input, so the runner cannot write a new user message onto stdin mid-process.

On `pi`, `stop-at-gap` can translate `TILLR_PAUSE_REQUEST` into `SIGUSR1` to stop at a tool boundary and reopen with `--resume`, but stopping the process terminates any session-scoped background shell tasks the worker started, costs a process cold start, and forces an interruption even when the worker's own package is unaffected by the trunk repair. On `claude`, `pause-kind` is `stops-where-it-stands`: `SIGTERM` kills active tool calls mid-execution.

In-process hooks avoid every one of those faults:

- **One acknowledgement file in the run's durable directory** (`trunk-green-ack.json` inside `run.DurableDir` from [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go)) records the last `target_sha` surfaced to the session. When `<lane>/trunk-green.json` holds `state` in `{"green-advanced", "check-repaired-on-tip"}` and `target_sha` differs from both the current effective base and `trunk-green-ack.json`, the extension (`pi`) or `PostToolUse` hook (`claude`) writes `trunk-green-ack.json` and injects:

  ```text
  Trunk update: your base <old_short> had unproved/red checks (<checks>) that are now
  cleared at <target_short> (<state>). If base failures affect your tests or you are
  ready to verify before landing, call `rebase_trunk` to rebase onto <target_short> and
  advance your recorded base. If your work is unaffected right now, you may keep working.
  ```

- **Immediate `obstacle(kind: "trunk")` guard.** If a worker on `pi` or `claude` attempts to call `obstacle` with `kind: "trunk"` while `<lane>/trunk-green.json` already shows a newer `target_sha` clearing those checks (or proving a newer green commit), `obstacle` returns immediately inside the running process ([`tillr-landing-call.md` Section 3.2](./tillr-landing-call.md#32-what-is-answered-at-once)) instead of parking the run: it tells the worker that the trunk already repaired `<checks>` at `<target_short>` and instructs it to call `rebase_trunk` and re-run its check. That saves an entire drain replay-and-resume cycle.

---

## 4. Who rebases: worker in-run, loop between nodes, or both

### 4.1 Why raw `git rebase` by a worker is unsafe today

Before comparing options, trace what happens in the current codebase if a worker runs raw `git rebase refs/heads/main` inside `<lane>/worktree` on its own:

1. **Fence check vs. candidate base diverge.** `fenceDiffBase` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) computes `git merge-base --all HEAD refs/heads/main` *only* for `write_scope` fence validation. By contrast, `settleBase` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) still reads `.tillr/lanes/<lane>/base_sha` (`old_base`), and [`land.ts`](../../tools/tillr-run/backends/pi/land.ts) / `validateLandingOffer` in [`tools/tillr-drain/drain/landing_call.go`](../../tools/tillr-drain/drain/landing_call.go) still require `offer.Base == TILLR_GOAL_BASE` (`old_base`).
2. **The pre-merge gate judges everyone else's commits.** Because `Candidate.BaseSHA` remains `old_base` while `Candidate.HeadSHA` now descends from `new_base`, [`tools/tillr-gate/change-gate.py`](../../tools/tillr-gate/change-gate.py) and `go-affected` (`tools/gate/go_affected.py`) evaluate `old_base..rebased_head` — running `commit-identity`, `citations`, `documents`, and affected Go package tests across **every sibling item that landed in `old_base..new_base`**. Any unproved sibling issue in `old_base..new_base` falsely fails this candidate's gate (violating [Landing Principle 1](./landing-principles.md#1-a-refusal-must-be-a-verdict) and [Landing Principle 14](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)).
3. **`Trunk.Squash` can resurrect a reverted commit.** `Trunk.Squash` in [`tools/tillr-land/land/trunk.go`](../../tools/tillr-land/land/trunk.go) executes `git merge-tree --write-tree --merge-base=<old_base> <trunk> <rebased_head>`. If a broken commit `B` landed in `old_base..new_base` and `main` later reverted `B` at `trunk` *after* the worker rebased onto `new_base`, three-way merging with `--merge-base=<old_base>` sees `B`'s edits present in `old_base..rebased_head` and absent in `old_base..trunk`, so `Trunk.Squash` **re-applies `B` back onto `main`**.
4. **Workers cannot write `.tillr/lanes/<lane>/base_sha`.** The `.tillr/` directory is outside the target worktree and denied both by the worker brief and by the path boundary in [`tillr-lane-shims.md`](./tillr-lane-shims.md) (`D-LS6`).

Therefore, a worker must **never** run raw `git rebase` onto `main` without a coordinated advance of the recorded base (`D-WR4`).

### 4.2 The three options for who performs the rebase ([OQ-WR2](#OQ-WR2))

| Option | While a node is running | Between workflow nodes & before gate | Trade-off |
| :--- | :--- | :--- | :--- |
| **A: Bounded worker tool/helper while working + automatic loop rebase between nodes and pre-gate *(recommended leaning)*** | Worker gets an opt-in `rebase_trunk` tool (`pi`, `claude`) backed by `tillr-run lane-rebase` (also callable from shell on `muse`, `agy`). Worker decides *if* and *when* to rebase (e.g., right after committing or stashing, when a base test blocks it, or right before final verification). Writes an authorized receipt in the run's durable directory. | If the worker did not rebase in-run, the loop rebases automatically at sequential node boundaries and in `preRebaseRepairedTrunk` before the gate whenever `<lane>/trunk-green.json` holds an unapplied `green-advanced` or `check-repaired-on-tip` target. | Directly answers the owner's prompt (*"give the worker a chance to rebase while working before landing"*), preserves single-command safety (autostash, clean abort, validated `target_sha`), and guarantees `muse`/`agy` and untouched runs still catch up before gating. |
| **B: Loop-only rebase between nodes and pre-gate; informational-only mid-run notice** | Turn-gap notice tells the worker which base checks were repaired on trunk (so it does not waste turns debugging them or file `OBSTACLE: trunk`), but never moves `HEAD` mid-run. | Loop rebases between workflow nodes and in `preRebaseRepairedTrunk` before the gate. | Simplest git invariant — zero mid-run branch rewrites — but if the broken base prevents compiling or running the very package the worker is editing in a single-node `build` run, the worker cannot run its own tests before calling `land`. |
| **C: Loop pauses `stops-at-a-gap` workers, rebases externally, and `--resume`s** | `tillr-drain` writes `TILLR_PAUSE_REQUEST`, waits for `pi` to exit, runs `moveLaneOntoTip`, updates `BaseSHAFile`, and resumes `pi`. | Loop rebases between nodes and pre-gate on `claude`, `muse`, `agy`. | Works only on `pi`, kills background shell tasks, interrupts workers that did not need the repair, and leaves `claude` (`stops-where-it-stands`) without mid-run rebase. |

### 4.3 Linear rebase vs. `D-GP8` merge take-in ([OQ-WR3](#OQ-WR3))

In [`docs/design/tillr-graph-is-the-path.md` Section 3.7](./tillr-graph-is-the-path.md#37-the-item-branch-and-the-latest-trunk), the designer's unbuilt `D-GP8` proposed a `take-in` command node that *merges* the latest trunk into the item branch before every node that needs the worktree. By contrast:

- the built drain helpers (`moveLaneOntoTip` in [`tools/tillr-drain/drain/trunk.go`](../../tools/tillr-drain/drain/trunk.go) and `preRebaseRepairedTrunk` in [`tools/tillr-drain/drain/trunk_repair.go`](../../tools/tillr-drain/drain/trunk_repair.go)) **rebase** `base..HEAD` onto the target trunk commit and overwrite `BaseSHAFile` and `ItemBaseSHAFile`;
- [`tools/tillr-gate/change-gate.py`](../../tools/tillr-gate/change-gate.py) and `identityFindings` in [`tools/gate/change_common.go`](../../tools/gate/change_common.go) inspect `git log base..head` and `git diff base..head`, which on a branch holding a trunk merge commit includes every merged sibling trunk commit in `old_base..target_sha` unless `base` advances or every check learns to exclude second-parent ancestry;
- the owner's 2026-11-25 question specifically names **rebase** (*"give the worker a chance to rebase while working"*).

[OQ-WR3](#OQ-WR3) puts that choice explicitly to the owner, leaning toward **linear rebase onto `target_sha` with `base_sha` advancing to `target_sha`** and narrowing between-node take-in from "every trunk move" (`D-GP8`) to "when the current base was unproved or red and `<lane>/trunk-green.json` offers a repaired/proved target" (consistent with [Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)).

---

## 5. What `base_sha`, `NodeBaseSHA`, and `gate.verdict` become after a rebase

### 5.1 Advancing `base_sha` via an authorized durable receipt (`D-WR5`)

*(Designer's proposal; the owner may overturn this.)*

Why can `tillr-drain` not simply infer `Candidate.BaseSHA = git merge-base HEAD refs/heads/main` at settlement without an explicit rebase record?

- **Empty-work ambiguity.** Suppose a worker started at `old_base`, rebased onto `target_sha` with zero commits of its own (`HEAD == target_sha`), and exited. Pure git ancestry sees `old_base != HEAD` and `merge-base(HEAD, main) == target_sha`: without a record that the worker *advanced its base* to `target_sha`, `HEAD == effective_base` ("worker produced no commit") is indistinguishable from "worker authored `old_base..target_sha`" if `main` moved or rewrote.
- **Unauthorized target protection.** A worker must only advance its base to a commit `tillr-drain` authorized in `<lane>/trunk-green.json`, never to an arbitrary commit on `main` or another ref.

The bounded helper (`tillr-run lane-rebase`, called by `rebase_trunk`) keeps every state holder aligned without granting the worker write access to `.tillr/lanes/<lane>/`:

1. **Preconditions checked by `tillr-run lane-rebase`:**
   - Reads `<lane>/trunk-green.json` and verifies `target_sha` is non-empty and differs from `effective_base` (read from `rebased-base.json` in the run's durable directory if present, else `TILLR_GOAL_BASE`).
   - Verifies `git merge-base --is-ancestor <effective_base> <target_sha>` (or recorded rewrite lineage passed in `<lane>/trunk-green.json`) and `git merge-base --is-ancestor <effective_base> HEAD`.
   - Verifies no `.git/rebase-merge` or `.git/rebase-apply` directory already exists.
2. **Execution:**
   - Snapshot-saves `orig_head = HEAD` and runs `git rebase --autostash --onto <target_sha> <effective_base>` on single-base trunk lanes (see [Section 5.2](#52-multi-node-workflows-nodebasesha-and-concurrent-writers-d-wr6) for multi-node `NodeBaseSHA`).
   - If the rebase succeeds, writes `rebased-base.json` atomically inside the run's durable directory (`run.DurableDir` in [`tools/tillr-run/run/exec.go`](../../tools/tillr-run/run/exec.go)):
     ```json
     {
       "staged_base_sha": "<TILLR_GOAL_BASE>",
       "previous_base_sha": "<effective_base>",
       "effective_base_sha": "<target_sha>",
       "rebased_from_head": "<orig_head>",
       "rebased_to_head": "<new_head>",
       "trunk_green_seq": 3
     }
     ```
3. **In-run ending tools read `effective_base_sha`:**
   - [`land.ts`](../../tools/tillr-run/backends/pi/land.ts), [`keep-going.ts`](../../tools/tillr-run/backends/pi/keep-going.ts), [`no_change.ts`](../../tools/tillr-run/backends/pi/no_change.ts), and `PreToolUse` in [`tools/tillr-run/claude/landing.go`](../../tools/tillr-run/claude/landing.go) resolve `effectiveBase` from `rebased-base.json` (falling back to `TILLR_GOAL_BASE`).
   - Calling `land` when `HEAD == effectiveBase` is immediately rejected inside the call (`"There is no commit to offer: HEAD is still the current base"`), and `landing-offer.json` records `base: effectiveBase`.
4. **Drain settlement (`validateLandingOffer` and `settleBase`):**
   - Before accepting `rebased-base.json`, `tillr-drain` verifies that `effective_base_sha` is an ancestor of `refs/heads/main`, is at or between `staged_base_sha` and `<lane>/trunk-green.json`'s `target_sha`, and is an ancestor of `HEAD`.
   - `tillr-drain` then updates `.tillr/lanes/<lane>/base_sha` (`BaseSHAFile`) and `item-base-sha` (`ItemBaseSHAFile`) to `effective_base_sha`, sets `Candidate.BaseSHA = effective_base_sha` (preserving `Candidate.RebasedFromHead = orig_head`), and appends a `run.base-advanced` store event.

### 5.2 Multi-node workflows (`NodeBaseSHA`) and concurrent writers (`D-WR6`)

*(Designer's proposal; the owner may overturn this.)*

Multi-node workflows track two bases: `ItemBaseSHA` (the trunk commit the whole item branch is diffed against at gate and squash time) and `NodeBaseSHA` (the commit the current node started from, used by `fenceViolation` to enforce per-node `write_scope`).

| Workflow shape | What happens on rebase onto `target_sha` | How `ItemBaseSHA` and `NodeBaseSHA` update |
| :--- | :--- | :--- |
| **Single-node build, or node 1 of a sequential workflow (`NodeBaseSHA == ItemBaseSHA`, i.e. `laneBasedOnTrunk == true`)** | `effective_base..HEAD` is replayed onto `target_sha`. | Both `ItemBaseSHA` and `NodeBaseSHA` become `target_sha`. |
| **Between sequential writer nodes (`ItemBaseSHA < NodeBaseSHA == HEAD`)** | Before staging node $k+1$ in `stageDispatches`, if `<lane>/trunk-green.json` offers a newer `target_sha`, the loop rebases `ItemBaseSHA..HEAD` onto `target_sha` (rewriting prior node commits `C1..Ck` into `C1'..Ck'`). | `ItemBaseSHA` and `BaseSHAFile` advance to `target_sha`; node $k+1$'s `NodeBaseSHA` is staged as the new `HEAD` (`Ck'`), matching `nodeBaseOnCandidate` in [`tools/tillr-drain/drain/preserved.go`](../../tools/tillr-drain/drain/preserved.go). |
| **Mid-run inside a later sequential writer node (`ItemBaseSHA < NodeBaseSHA <= HEAD`)** | Replaying `ItemBaseSHA..HEAD` onto `target_sha` rewrites `NodeBaseSHA` (`Ck`) into `Ck'`. Without mapping `Ck -> Ck'`, `fenceViolation` would lose the boundary between node $k$'s commits and node $k+1$'s commits. | `tillr-run lane-rebase` places a temporary local ref `refs/Update-node-base` (or reads Git's `post-rewrite` Old->New SHA map during `git rebase --onto <target_sha> <ItemBaseSHA>`), records `"effective_node_base_sha": "<Ck'>"` in `rebased-base.json`, and settlement advances `ItemBaseSHA` to `target_sha` and `NodeBaseSHA` to `Ck'`. *(In Slice 1, mid-run `lane-rebase` may refuse when `NodeBaseSHA != ItemBaseSHA` and defer that rebase to the node boundary; Slice 3 enables full `NodeBaseSHA` rewrite mapping.)* |
| **Concurrent parallel writers ([`D-GP16`](./tillr-graph-is-the-path.md#D-GP16))** | Parallel `piece` checkouts share one frontier `<base>` so their downstream `join` node can run `git merge-tree --write-tree --merge-base=<base>`. | **Never rebase mid-fan-out.** `lane-rebase` is disabled on parallel per-node checkouts; the item branch rebases only before fan-out or after `join` completes. |

### 5.3 Gate verdict binding (`gate.verdict` and `tools/tillr-land`, `D-WR7`)

*(Designer's proposal; the owner may overturn this.)*

In [`tools/tillr-land/land/landing.go`](../../tools/tillr-land/land/landing.go), `decide` enforces one strict invariant on every landing:

```text
req.Verdict.BaseSHA == req.BaseSHA  &&  req.Verdict.TreeSHA == HeadSHA^{tree}
```

Any mismatch is refused as `ReasonVerdictMismatch`. Tracing the lifecycle before and after a candidate's gate shows why rebase-while-working never invalidates or duplicates a verdict:

1. **Rebase while working, between nodes, or in `preRebaseRepairedTrunk` (before the gate starts):**
   - No `gate.verdict` exists yet for this candidate offer.
   - The rebase moves `(old_base, old_head)` to `(target_sha, new_head)` and updates `Candidate.BaseSHA = target_sha`, `Candidate.HeadSHA = new_head`.
   - `tillr-gate` then runs **once** on `(target_sha, new_head)` and emits a `gate.verdict` binding `(BaseSHA: target_sha, TreeSHA: new_head^{tree})`.
   - `tillr-land` receives `LandingRequest{BaseSHA: target_sha, HeadSHA: new_head, Verdict: verdict}`, verifies `decide` passes, and calls `Trunk.Squash(target_sha, trunk_before, new_head)` to three-way merge `target_sha..new_head` onto `trunk_before`.
2. **After a candidate's gate has started or passed (in-flight gate or retained verdict):**
   - Once `tillr-gate` has started on `(base, head)` — or once a passing `gate.verdict` is held on `red-cap` or `trunk-rewritten` waiting for `landCandidate` in [`tools/tillr-drain/drain/loop.go`](../../tools/tillr-drain/drain/loop.go) — **no green-advance rebase touches the lane** (matching the `!reattaching` guard on `preRebaseRepairedTrunk` and [Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)).
   - The gate runs once; `Trunk.Squash` merges `base..head` onto the current `trunk_before` without a second gate.

---

## 6. What happens when the rebase conflicts

A rebase onto `target_sha` can conflict in two places: inside a live worker turn when the worker calls `rebase_trunk` (`tillr-run lane-rebase`), or outside a worker turn when the loop rebases between workflow nodes or in `preRebaseRepairedTrunk` ([`D-WR8`](#D-WR8), designer's proposal).

### 6.1 Conflict during an in-run worker rebase

When the worker invokes `rebase_trunk` / `tillr-run lane-rebase` while working and `git rebase --autostash --onto <target_sha> <effective_base>` stops on a merge conflict:

1. **Clean transactional abort.** `tillr-run lane-rebase` captures the conflicting path list (`git diff --name-only --diff-filter=U`), runs `git rebase --abort`, resets `HEAD` to `orig_head` if needed, and re-applies the autostash snapshot so `<lane>/worktree` returns byte-for-byte to the exact state it had before the call — never leaving `.git/rebase-merge` or unmerged index stages behind. `rebased-base.json` is not updated.
2. **Actionable result returned inside the turn.** The tool returns a non-fatal structured result naming `target_sha`, the conflicting paths, and the commits in `effective_base..target_sha` that touched those paths.
3. **Worker chooses between two valid paths:**
   - **Defer reconciliation until `land` (keep working on `effective_base`).** Rebasing while working is an opportunity, not a mandate. If the worker's own tests pass on `effective_base` (or if the conflict was caused by an intermediate commit on the worker's branch that a final squash eliminates), the worker can finish its change on `effective_base` and call `land`. At landing time `Trunk.Squash` merges trees (`effective_base..HEAD` onto `trunk_before`); if the conflict still exists there, `ResultSquashConflict` returns `ANSWER: conflict` to the same session via `conflictFix` in [`tools/tillr-drain/drain/conflict.go`](../../tools/tillr-drain/drain/conflict.go).
   - **Reconcile onto `target_sha` now (`rebase_trunk --leave-conflicts` or manual reconcile after advancing base).** When the worker *needs* `target_sha` right now because `effective_base` cannot compile or run its package's tests, passing `--reconcile` to `tillr-run lane-rebase` advances `rebased-base.json` to `target_sha`, resets the branch onto `target_sha` with the worker's tree merged via `git merge-tree` / `git checkout -m` conflict markers on the overlapping paths, and pins `orig_head` at a local recovery ref in the run's scratch notes.
   - **Why advancing `effective_base` keeps narrow `write_scope` fences clean:** If `effective_base..target_sha` modified a file *outside* this node's `write_scope`, advancing `effective_base` to `target_sha` means keeping `target_sha`'s version of that outside-fence file produces **zero diff** in `target_sha..HEAD`, so `fenceViolation` never accuses the worker of escaping its fence. Any path that actually conflicts with the worker's own edits is, by definition, a path the worker already modified inside its fence (else the worker had a prior scope escape).

### 6.2 Conflict during a between-node or pre-gate loop rebase

When the loop rebases a lane between sequential workflow nodes or inside `preRebaseRepairedTrunk` before the gate and hits a conflict:

1. **Abort and pin.** Following `abortCandidateRebase` in [`tools/tillr-drain/drain/trunk_repair.go`](../../tools/tillr-drain/drain/trunk_repair.go), the loop records the unmerged file list, runs `git rebase --abort` and `git reset --hard <candidate_head>`, and pins `<candidate_head>` at `refs/tillr/runs/<run_id>` (`preserveWork` in [`tools/tillr-drain/drain/preserved.go`](../../tools/tillr-drain/drain/preserved.go)).
2. **Route back to the authoring worker session, uncounted.** Following `conflictFix` in [`tools/tillr-drain/drain/conflict.go`](../../tools/tillr-drain/drain/conflict.go), the lane is placed on `target_sha` (`BaseSHAFile` and `ItemBaseSHAFile` updated to `target_sha`) and the worker session that authored the conflicting commits is resumed with `ANSWER: conflict` (under `call-suspends`) or a conflict brief (under `footer`) naming `target_sha`, the conflicting paths, and the pinned commit ref.
3. **Escalation on identical repeat only.** Under [Landing Principle 5](./landing-principles.md#5-only-verdicts-charge) and [Landing Principle 6](./landing-principles.md#6-a-budget-escalates-it-does-not-loop), conflict reconciliation never consumes an attempt counter; it escalates to the `design` seat only if `conflictEndingKey` in [`tools/tillr-drain/drain/returned.go`](../../tools/tillr-drain/drain/returned.go) matches an earlier conflict on the same sorted paths and normalized hunks, or if the worker calls `obstacle` (`definition`).

### 6.3 Supersession discovered during rebase

If rebasing `effective_base..HEAD` onto `target_sha` drops every commit because `target_sha` already contains the worker's exact change (`git rebase` skips already-applied patches, leaving `HEAD == target_sha`):

- Between nodes or pre-gate, `Trunk.Squash` / loop detection sees an empty diff against `target_sha` and settles the item through the existing supersession path (`settleAlreadyOnTrunk`, [Landing Principle 10](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall)).
- In-run, `tillr-run lane-rebase` reports explicitly that `target_sha` already contains every committed change from `effective_base..orig_head`, leaving `orig_head` pinned so the worker can inspect `target_sha` and either commit remaining work or finish cleanly (`no_change` where staged, or let `land` on `orig_head` hit `already-on-trunk`).

---

## 7. Check against the landing principles

Every piece of this design is checked against [`docs/design/landing-principles.md`](./landing-principles.md):

| Principle | How this design satisfies it |
| :--- | :--- |
| **[1. A refusal must be a verdict](./landing-principles.md#1-a-refusal-must-be-a-verdict)** | Neither `<lane>/trunk-green.json`, an unacknowledged turn-gap notice, nor a rebase conflict records a failing verdict on the item. Advancing `Candidate.BaseSHA` alongside an authorized rebase prevents `change-gate.py` from failing a candidate on sibling commits in `old_base..target_sha`. |
| **[2. Re-application preserves the change, not the patch](./landing-principles.md#2-re-application-preserves-the-change-not-the-patch)** | Workers on a proved green base never rebase merely to refresh patch context; `Trunk.Squash` continues to three-way merge trees at landing time without comparing `git patch-id`. |
| **[3. Gate what will be the tip, once](./landing-principles.md#3-gate-what-will-be-the-tip-once)** | Notifications and rebases fire **only** when the worker's base was unproved or red and the trunk clears that fault — never on ordinary sibling landings once a worker's base is proved green, and never after a candidate's gate has started or produced a standing `gate.verdict`. |
| **[4. Every outcome is state-changing](./landing-principles.md#4-every-outcome-is-state-changing)** | `trunk-green-ack.json` ensures each `target_sha` notifies a running session at most once; `rebased-base.json` advances `effective_base_sha` strictly forward along trunk ancestry; loop conflict routing advances the lane onto `target_sha` so the same `(candidate, tip, reason)` cannot repeat. |
| **[5. Only verdicts charge](./landing-principles.md#5-only-verdicts-charge)** | An aborted in-run rebase, an intercepted `obstacle(kind: "trunk")`, and a loop conflict-fix turn are all uncounted. |
| **[6. A budget escalates, it does not loop](./landing-principles.md#6-a-budget-escalates-it-does-not-loop)** | Repeated loop rebase conflicts compute `conflictEndingKey` over paths and normalized hunks and escalate to `design` on an identical repeat rather than looping or counting attempts. |
| **[7. A surface that hides parked work is a false report](./landing-principles.md#7-a-surface-that-hides-parked-work-is-a-false-report)** | This design introduces no new hold state; `run.base-advanced` events make mid-run and between-node base moves visible on the item and run trace pages. |
| **[10. Supersession is a verdict, not a stall](./landing-principles.md#10-supersession-is-a-verdict-not-a-stall)** | If rebasing onto `target_sha` yields an empty range because the trunk already holds the change, the item exits as `already-on-trunk` (`resolved-elsewhere`) rather than stalling on `"HEAD equals base"`. |
| **[11. A failure is diagnosed, not waited out](./landing-principles.md#11-a-failure-is-diagnosed-not-waited-out) & [13. A held state names a mover that can still act](./landing-principles.md#13-a-held-state-names-a-mover-that-can-still-act)** | Workers are never put on a timer or sleep poll waiting for `refs/tillr/green`; `<lane>/trunk-green.json` is written on concrete state transitions (`refs/tillr/green` advance or recorded check repair), and no run is held waiting when it can keep editing. |
| **[14. Convict a run on what it did, not on what happened while it ran](./landing-principles.md#14-convict-a-run-on-what-it-did-not-on-what-happened-while-it-ran)** | `<lane>/trunk-green.json` is written by `tillr-drain` outside `<lane>/worktree` and `rebased-base.json` lives in the run's own durable directory, so `CompareRunObserved` in [`tools/tillr-drain/drain/boundary.go`](../../tools/tillr-drain/drain/boundary.go) never convicts the run; advancing `Candidate.BaseSHA` ensures the gate judges only the worker's diff. |
| **[15. Land the smallest functional change](./landing-principles.md#15-land-the-smallest-functional-change)** | The implementation divides into three small, independently functional slices ([Section 8](#8-build-slices)), each leaving the trunk strictly healthier on its own. |

---

## 8. Build slices

Once the owner rules on [OQ-WR1](#OQ-WR1) through [OQ-WR3](#OQ-WR3), the work splits into three independently landable slices ([Landing Principle 15](./landing-principles.md#15-land-the-smallest-functional-change)):

1. **Slice 1 — `<lane>/trunk-green.json` writer, `tillr-run lane-rebase`, and `rebased-base.json` settlement.**
   - `tillr-drain` writes `<lane>/trunk-green.json` monotonically when an active lane's base was unproved or red and the qualifying transition from [OQ-WR1](#OQ-WR1) occurs, and extends `preRebaseRepairedTrunk` to cover `green-advanced` repairs as well as `RecordedTrunkRepair`.
   - `tillr-run lane-rebase` performs the validated autostash rebase for `laneBasedOnTrunk` lanes (`NodeBaseSHA == ItemBaseSHA`) and writes `rebased-base.json` in the run's durable directory.
   - `land.ts`, `keep-going.ts`, `no_change.ts`, `claude/landing.go`, `validateLandingOffer`, and `settleBase` honor `rebased-base.json` and record `run.base-advanced`.
2. **Slice 2 — Turn-gap notices, `rebase_trunk` tool, and `obstacle(kind: "trunk")` guard on `pi` and `claude`.**
   - Load the `pi` turn-gap extension and register `rebase_trunk` in `run-pi`; add the `PostToolUse` `additionalContext` hook and `mcp__tillr__rebase_trunk` in `tools/tillr-run/claude/landing.go`; intercept `obstacle(kind: "trunk")` when `<lane>/trunk-green.json` already repairs the named checks; add the one-line `../trunk-green.json` pointer to `brief-template.md` for `muse` and `agy`.
3. **Slice 3 — Between-node rebase and `NodeBaseSHA` rewrite mapping on sequential multi-node workflows.**
   - Rebase `ItemBaseSHA..HEAD` onto unapplied `<lane>/trunk-green.json` targets between sequential stages in `stageDispatches` (routing conflicts through `conflictFix`), update the next node's brief header, and map `NodeBaseSHA` across mid-node rebases when `NodeBaseSHA != ItemBaseSHA`.

---

## 9. What this does not cover

- **Changing the program-wide absent-setting default `lane_base: "green"` in `ReadGatePolicy` ([`tools/tillr-drain/redcap/policy.go`](../../tools/tillr-drain/redcap/policy.go)).** [`D-GR55`](../reference/gate.md#D-GR55) switched [`config/tillr/gate-policy.json`](../../config/tillr/gate-policy.json) to `lane_base: "tip"` in this repository while keeping `"green"` as the default for repositories that leave `lane_base` unset; in a repository configured with `lane_base: "green"`, ordinary lanes already start on `refs/tillr/green` and `trunk-green.json` simply stays silent.
- **Fork-series delivery (`refs/heads/patch/*`), trunk fixers (`fixer-main-*`), and remote-sync items.** Fork lanes track upstream fork bases rather than `refs/tillr/green`; trunk fixers intentionally stand on red trunk commits and already rebase onto `main` immediately before `fixer-gate`.
- **Item-owned worktrees ([`OQ-LC1`](./tillr-landing-call.md#OQ-LC1)).** When worktrees move from `.tillr/lanes/<lane>/` to `.tillr/items/<item-id>/`, `<lane>/trunk-green.json` moves unchanged to `.tillr/items/<item-id>/trunk-green.json`.

---

## Decision Ledger

`D-WR1` through `D-WR8` are the designer's proposals from a filed request; none is an owner ruling, and the owner may overturn any of them. Choices that belong to the owner are open in [OQ-WR1](#OQ-WR1) through [OQ-WR3](#OQ-WR3). The Built column is read from the tree.

| ID | Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="D-WR1"></a>`D-WR1` | **Designer's proposal (governed by [OQ-WR1](#OQ-WR1)); the owner may overturn this.** Never notify or offer a rebase to a running worker whose current `base_sha` was already proved green with no red check, nor when `refs/tillr/green` catches up to `base_sha` with no intervening revert, fixer repair, or check removal (`base-proved`). Notify only when the worker's base was unproved or red and the trunk clears a fault affecting that base. **Rejected:** notifying on every sibling trunk landing or every `refs/tillr/green` advance, whose token and cache-invalidation cost scales with trunk velocity against [Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once). | 2026-11-25 | [Section 2.1](#21-why-ordinary-trunk-movement-must-never-notify-a-worker), [Section 2.2](#22-the-two-transitions-that-matter-on-an-unproved-or-red-base) | No |
| <a id="D-WR2"></a>`D-WR2` | **Designer's proposal; the owner may overturn this.** `tillr-drain` writes `<lane>/trunk-green.json` atomically inside `.tillr/lanes/<lane>/` (outside `<lane>/worktree`), advancing `target_sha` monotonically along `main` ancestry and excluding fork lanes, `fixer-main-*`, and remote-sync items. **Rejected:** writing a status file inside `<lane>/worktree`, which dirties `git status` and lets a worker forge `target_sha`. | 2026-11-25 | [Section 2.3](#23-monotonic-lane-status-file-and-exclusions-d-wr2) | No |
| <a id="D-WR3"></a>`D-WR3` | **Designer's proposal; the owner may overturn this.** Deliver mid-run notices in-process at tool/turn gaps via a `pi` extension (`run-pi`) and a Claude Code `PostToolUse` `additionalContext` hook (`claude/landing.go`), backed by `trunk-green-ack.json` so each `target_sha` surfaces at most once; intercept `obstacle(kind: "trunk")` immediately when `<lane>/trunk-green.json` already clears the named checks; never signal `muse` or `agy` mid-run (`cannot-resume`), serving them via `<lane>/trunk-green.json`, between-node briefs, and pre-gate rebase. **Rejected:** pausing `pi` via `SIGUSR1` or killing `claude` via `SIGTERM` for an optional rebase hint. | 2026-11-25 | [Section 3](#3-how-the-signal-reaches-a-running-worker-across-adapters) | No |
| <a id="D-WR4"></a>`D-WR4` | **Designer's proposal (governed by [OQ-WR2](#OQ-WR2)); the owner may overturn this.** Forbid raw `git rebase` onto `main` by workers and provide a bounded `tillr-run lane-rebase` helper (`rebase_trunk` tool on `pi` and `claude`) paired with automatic loop rebase between sequential nodes and before the gate. **Rejected:** instructing workers to run raw `git rebase main` and leaving `Candidate.BaseSHA` at `old_base`, which makes `change-gate.py` judge sibling commits and lets `Trunk.Squash` re-apply reverted commits. | 2026-11-25 | [Section 4.1](#41-why-raw-git-rebase-by-a-worker-is-unsafe-today), [Section 4.2](#42-the-three-options-for-who-performs-the-rebase-oq-wr2) | No |
| <a id="D-WR5"></a>`D-WR5` | **Designer's proposal; the owner may overturn this.** Record an in-run rebase in `rebased-base.json` inside the run's durable directory (`run.DurableDir`), read `effective_base_sha` in `land`, `keep-going`, and `no_change`, and validate `effective_base_sha` against `<lane>/trunk-green.json` and `main` ancestry on settlement before updating `BaseSHAFile`, `ItemBaseSHAFile`, and `Candidate.BaseSHA`. **Rejected:** inferring `Candidate.BaseSHA` solely from `git merge-base HEAD main` with no receipt, which conflates an empty rebased branch (`HEAD == target_sha`) with worker-authored commits. | 2026-11-25 | [Section 5.1](#51-advancing-base_sha-via-an-authorized-durable-receipt-d-wr5) | No |
| <a id="D-WR6"></a>`D-WR6` | **Designer's proposal; the owner may overturn this.** Update `NodeBaseSHA` alongside `ItemBaseSHA` on sequential multi-node rebases (staging `NodeBaseSHA = Ck'` between nodes, and mapping `Ck -> Ck'` via Git rewrite records mid-node), and forbid independent mid-fan-out rebases on parallel writer checkouts (`D-GP16`). **Rejected:** rewriting prior node commits without updating `NodeBaseSHA`, which breaks per-node `write_scope` fence checks. | 2026-11-25 | [Section 5.2](#52-multi-node-workflows-nodebasesha-and-concurrent-writers-d-wr6) | No |
| <a id="D-WR7"></a>`D-WR7` | **Designer's proposal; the owner may overturn this.** Apply green-advance and check-repair rebases strictly **before** a candidate's pre-merge gate runs, so `tillr-gate` runs once on `(target_sha, new_head)` and writes one `gate.verdict` binding `(target_sha, new_head^{tree})` for `tillr-land`; never rebase an in-flight or already-gated candidate. **Rejected:** re-gating a candidate that already holds a passing `gate.verdict`, which violates [Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once). | 2026-11-25 | [Section 5.3](#53-gate-verdict-binding-gateverdict-and-toolstillr-land-d-wr7) | No |
| <a id="D-WR8"></a>`D-WR8` | **Designer's proposal; the owner may overturn this.** On an in-run rebase conflict, abort cleanly (`git rebase --abort` + restore `HEAD` and autostash) and let the worker choose between finishing on `effective_base` or reconciling onto `target_sha` (`--reconcile`); on a between-node or pre-gate loop rebase conflict, abort, pin the pre-rebase head at `refs/tillr/runs/<run_id>`, and return `ANSWER: conflict` to the authoring worker session on `target_sha` uncounted, escalating to `design` only on `conflictEndingKey` repeat. | 2026-11-25 | [Section 6](#6-what-happens-when-the-rebase-conflicts) | No |

---

## Open Questions

1. 💬 **OQ-WR1: Which trunk events should notify a running worker and offer a rebase?**

   When a worker starts from `refs/heads/main` (`lane_base: "tip"`), its `base_sha` may simply be unproved (ahead of `refs/tillr/green`), or it may carry a broken check that an automatic revert or `fixer-main-*` landing repairs on `main` minutes before the heavy proof reaches that repair.

   - **A: Notify on both proved repairs (`green-advanced`) and fast check repairs on the tip (`check-repaired-on-tip`), and stay silent on ordinary sibling landings and pure catch-up (`base-proved`).** A worker whose base was already green hears nothing; a worker whose unproved base simply gets proved with no intervening revert or fixer hears nothing; a worker whose base carried a fault gets a rebase offer as soon as `RecordedTrunkRepair` / `ReadCheckProofs` clears that fault on `main` or `refs/tillr/green` proves past a revert/fixer.
   - **B: Notify and offer a rebase only when `refs/tillr/green` itself advances past a broken base (`green-advanced`), never on an unproved tip repair.** Guarantees a worker never rebases onto an unproved repair commit that might fail a different heavy check, at the cost of leaving a blocked worker waiting through the full heavy proof run.
   - **C: Notify and offer a rebase whenever `refs/tillr/green` advances past `base_sha`, even when `base_sha` was already proved green.** Keeps long-running lanes closer to the green pointer even on healthy trunks, at the cost of turn-gap prompts and test-cache churn that rise with trunk velocity ([Landing Principle 3](./landing-principles.md#3-gate-what-will-be-the-tip-once)).

   <!-- vantage: question id=OQ-WR1 leaning="A — unblocks a worker stuck on a broken base as soon as auto_revert or fixer-gate clears the named check (matching preRebaseRepairedTrunk), while keeping healthy workers completely undisturbed as trunk throughput scales." -->

   _Leaning:_ A — unblocks a worker stuck on a broken base as soon as `auto_revert` or `fixer-gate` clears the named check (matching `preRebaseRepairedTrunk`), while keeping healthy workers completely undisturbed as trunk throughput scales.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-WR2: Should a running worker have an opt-in rebase tool while working, or should only the loop rebase between nodes and before the gate?**

   Raw `git rebase` by an LLM leaves `Candidate.BaseSHA` stale ([Section 4.1](#41-why-raw-git-rebase-by-a-worker-is-unsafe-today)), so mid-run rebasing requires either a bounded runner helper or deferring all rebases to loop transitions.

   - **A: Both, layered — expose a bounded opt-in `rebase_trunk` tool (`tillr-run lane-rebase`) while working, and have the loop automatically rebase between sequential nodes and before the gate if the worker did not.** Lets a `pi` or `claude` worker rebase immediately when a broken base blocks its own compile or test loop (or right before final verification), while covering `muse`, `agy`, and unaffected runs automatically with zero mid-run interruptions.
   - **B: Loop-only rebase between workflow nodes and before the gate; mid-run notification is informational only.** Tells `pi` and `claude` workers which base checks were repaired on `main` so they skip debugging them and do not file `OBSTACLE: trunk`, without ever rewriting `HEAD` mid-run; single-node runs blocked from compiling or running package tests wait until `preRebaseRepairedTrunk` runs after `land`.
   - **C: Loop pauses `stops-at-a-gap` workers (`pi`), rebases externally, and `--resume`s.** Keeps all git history rewrites inside `tillr-drain`, at the cost of killing background shell tasks, restarting the harness process, and leaving `claude` (`stops-where-it-stands`), `muse`, and `agy` without mid-run rebase.

   <!-- vantage: question id=OQ-WR2 leaning="A — directly fulfills 'give the worker a chance to rebase while working before landing' through a safe single-command boundary, with automatic loop fallback for harnesses or runs that do not invoke it." -->

   _Leaning:_ A — directly fulfills `"give the worker a chance to rebase while working before landing"` through a safe single-command boundary, with automatic loop fallback for harnesses or runs that do not invoke it.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-WR3: Should taking in a repaired or newly green trunk rebase `base..HEAD` and advance `base_sha`, or merge the trunk commit (`D-GP8`)?**

   [`tillr-graph-is-the-path.md` Section 3.7](./tillr-graph-is-the-path.md#37-the-item-branch-and-the-latest-trunk) (`D-GP8`, a designer decision) proposed merging the latest trunk into the item branch before every node, whereas `moveLaneOntoTip`, `preRebaseRepairedTrunk`, [`change-gate.py`](../../tools/tillr-gate/change-gate.py), and the 2026-11-25 prompt use linear rebase and advance `base_sha`.

   - **A: Linear rebase onto `target_sha`, advancing `base_sha` to `target_sha` (amending `D-GP8` to rebase only on green/repair transitions or explicit conflict resolution).** Keeps `base_sha..HEAD` linear and scoped strictly to the item's own commits so `change-gate.py`, `commit-identity`, `citations`, `go-affected`, and `Trunk.Squash` work unchanged without judging merged trunk history.
   - **B: Merge `target_sha` into the lane branch (`D-GP8` style) without rewriting earlier lane commits.** Preserves pre-merge commit SHAs on multi-node branches, at the cost of teaching `change-gate.py`, `commit-identity`, `citations`, `fenceViolation`, and `Trunk.Squash` to exclude merged trunk ancestry from `old_base..HEAD`.

   <!-- vantage: question id=OQ-WR3 leaning="A — matches the owner's rebase wording, the built moveLaneOntoTip and preRebaseRepairedTrunk helpers, and every change-scoped check's linear base..head contract." -->

   _Leaning:_ A — matches the owner's rebase wording, the built `moveLaneOntoTip` and `preRebaseRepairedTrunk` helpers, and every change-scoped check's linear `base..head` contract.

   **Answer:**

   > _(empty — fill in when decided)_
